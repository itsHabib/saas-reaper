// Command reaper-flags serves the reaped feature-flag appliance.
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

	"github.com/itsHabib/saas-reaper-poc/internal/api"
	"github.com/itsHabib/saas-reaper-poc/internal/flags"
	"github.com/itsHabib/saas-reaper-poc/internal/snapshot"
	"github.com/itsHabib/saas-reaper-poc/internal/store/sqlite"
)

func main() {
	if err := run(); err != nil {
		slog.Error("reaper flags stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	adminToken := os.Getenv("REAPER_ADMIN_TOKEN")
	adminActor := os.Getenv("REAPER_ADMIN_ACTOR")
	evaluationToken := os.Getenv("REAPER_EVALUATION_TOKEN")
	if adminToken == "" || adminActor == "" || evaluationToken == "" {
		return errors.New("REAPER_ADMIN_TOKEN, REAPER_ADMIN_ACTOR, and REAPER_EVALUATION_TOKEN are required")
	}
	databasePath := environment("REAPER_DB", ".reaper/flags.db")
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o750); err != nil {
		return fmt.Errorf("create database directory: %w", err)
	}
	store, err := sqlite.Open(databasePath)
	if err != nil {
		return err
	}
	defer store.Close()
	service, err := flags.Open(context.Background(), store, snapshot.NewMemory())
	if err != nil {
		return err
	}
	apiServer, err := api.New(service, adminToken, adminActor, evaluationToken)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr:              environment("REAPER_ADDR", ":8080"),
		Handler:           apiServer.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("reaper flags listening", "address", httpServer.Addr, "database", databasePath)
		serverErrors <- httpServer.ListenAndServe()
	}()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownContext)
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func environment(name, fallback string) string {
	value := os.Getenv(name)
	if value != "" {
		return value
	}
	return fallback
}
