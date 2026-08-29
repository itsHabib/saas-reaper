// Command reaper-webhooks serves the customer-owned outbound webhook specimen.
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

	"github.com/itsHabib/saas-reaper/specimens/webhook-delivery/internal/api"
	"github.com/itsHabib/saas-reaper/specimens/webhook-delivery/internal/delivery"
	"github.com/itsHabib/saas-reaper/specimens/webhook-delivery/internal/store/sqlite"
	"github.com/itsHabib/saas-reaper/specimens/webhook-delivery/internal/transport/httpdelivery"
	"github.com/itsHabib/saas-reaper/specimens/webhook-delivery/internal/worker"
)

func main() {
	if err := run(); err != nil {
		slog.Error("reaper webhooks stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configuration, err := loadConfig()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(configuration.databasePath), 0o700); err != nil {
		return fmt.Errorf("create webhook database directory: %w", err)
	}
	store, err := sqlite.Open(configuration.databasePath)
	if err != nil {
		return err
	}
	defer closeStore(store)
	schedule, err := delivery.NewSchedule(configuration.retryDelays)
	if err != nil {
		return err
	}
	sender, err := httpdelivery.New(configuration.requestTimeout)
	if err != nil {
		return err
	}
	attempts := delivery.NewAttemptCoordinator()
	service, err := delivery.NewService(
		store,
		configuration.adminActor,
		time.Now,
		delivery.RandomID,
		delivery.RandomSecret,
		attempts,
	)
	if err != nil {
		return err
	}
	dispatcher, err := delivery.NewDispatcher(store, sender, schedule, time.Now, attempts)
	if err != nil {
		return err
	}
	poller, err := worker.NewPoller(
		dispatcher,
		worker.TimerWaiter{},
		slog.Default(),
		configuration.pollInterval,
		25,
	)
	if err != nil {
		return err
	}
	apiServer, err := api.New(service, store, configuration.adminToken, configuration.readToken)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr:              configuration.address,
		Handler:           apiServer.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      configuration.requestTimeout + 10*time.Second,
		IdleTimeout:       60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go poller.Run(ctx)
	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("reaper webhooks listening", "address", httpServer.Addr, "database", configuration.databasePath)
		serverErrors <- httpServer.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
		slog.Error("close webhook store", "error", err)
	}
}
