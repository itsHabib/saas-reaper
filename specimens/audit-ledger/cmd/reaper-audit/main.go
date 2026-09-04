// Command reaper-audit serves the customer-owned append-only audit ledger specimen.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/audit-ledger/internal/api"
	"github.com/itsHabib/saas-reaper/specimens/audit-ledger/internal/ledger"
	"github.com/itsHabib/saas-reaper/specimens/audit-ledger/internal/store/sqlite"
)

func main() {
	if err := run(); err != nil {
		slog.Error("reaper audit stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configuration, err := loadConfig()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(configuration.databasePath), 0o700); err != nil {
		return fmt.Errorf("create audit database directory: %w", err)
	}
	store, err := sqlite.Open(configuration.databasePath)
	if err != nil {
		return err
	}
	defer closeStore(store)
	service, err := ledger.NewService(store, time.Now, configuration.writePrincipal)
	if err != nil {
		return err
	}
	apiServer, err := api.New(
		service,
		store,
		configuration.writeToken,
		configuration.readToken,
		configuration.readTenants,
	)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr:              configuration.address,
		Handler:           apiServer.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("reaper audit listening", "address", httpServer.Addr, "database", configuration.databasePath)
		serverErrors <- httpServer.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownContext)
	case listenErr := <-serverErrors:
		if errors.Is(listenErr, http.ErrServerClosed) {
			return nil
		}
		return listenErr
	}
}

func closeStore(store *sqlite.Store) {
	if err := store.Close(); err != nil {
		slog.Error("close audit store", "error", err)
	}
}
