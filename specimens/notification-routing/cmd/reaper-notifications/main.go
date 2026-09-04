// Command reaper-notifications serves the customer-owned notification routing specimen.
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

	"github.com/itsHabib/saas-reaper/specimens/notification-routing/internal/api"
	"github.com/itsHabib/saas-reaper/specimens/notification-routing/internal/routing"
	"github.com/itsHabib/saas-reaper/specimens/notification-routing/internal/store/sqlite"
	"github.com/itsHabib/saas-reaper/specimens/notification-routing/internal/transport/slackwebhook"
	"github.com/itsHabib/saas-reaper/specimens/notification-routing/internal/transport/smtpmail"
	"github.com/itsHabib/saas-reaper/specimens/notification-routing/internal/worker"
)

func main() {
	if err := run(); err != nil {
		slog.Error("reaper notifications stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configuration, err := loadConfig()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(configuration.databasePath), 0o700); err != nil {
		return fmt.Errorf("create notification database directory: %w", err)
	}
	store, err := sqlite.Open(configuration.databasePath)
	if err != nil {
		return err
	}
	defer closeStore(store)
	transports, err := buildTransports(configuration)
	if err != nil {
		return err
	}
	schedule, err := routing.NewSchedule(configuration.retryDelays)
	if err != nil {
		return err
	}
	service, err := routing.NewService(store, configuration.adminActor, time.Now, routing.RandomID)
	if err != nil {
		return err
	}
	dispatcher, err := routing.NewDispatcher(store, transports, schedule, time.Now)
	if err != nil {
		return err
	}
	poller, err := worker.NewPoller(dispatcher, worker.TimerWaiter{}, slog.Default(), configuration.pollInterval, 25)
	if err != nil {
		return err
	}
	apiServer, err := api.New(service, store, configuration.adminToken, configuration.readToken)
	if err != nil {
		return err
	}
	return serve(configuration, apiServer.Handler(), poller)
}

// buildTransports is the only place concrete wire mechanisms meet policy's channel kinds.
func buildTransports(configuration config) (map[routing.ChannelKind]routing.Transport, error) {
	mail, err := smtpmail.New(smtpmail.Config{
		Address:  configuration.smtpAddress,
		From:     configuration.smtpFrom,
		Username: configuration.smtpUsername,
		Password: configuration.smtpPassword,
		Timeout:  configuration.requestTimeout,
	})
	if err != nil {
		return nil, err
	}
	chat, err := slackwebhook.New(configuration.requestTimeout)
	if err != nil {
		return nil, err
	}
	return map[routing.ChannelKind]routing.Transport{
		routing.KindSMTP:         mail,
		routing.KindSlackWebhook: chat,
	}, nil
}

func serve(configuration config, handler http.Handler, poller *worker.Poller) error {
	httpServer := &http.Server{
		Addr:              configuration.address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pollerDone := make(chan struct{})
	go func() {
		defer close(pollerDone)
		poller.Run(ctx)
	}()
	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("reaper notifications listening", "address", httpServer.Addr, "database", configuration.databasePath)
		serverErrors <- httpServer.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		stop()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		shutdownErr := httpServer.Shutdown(shutdownContext)
		<-pollerDone
		return shutdownErr
	case listenErr := <-serverErrors:
		stop()
		<-pollerDone
		if errors.Is(listenErr, http.ErrServerClosed) {
			return nil
		}
		return listenErr
	}
}

func closeStore(store *sqlite.Store) {
	if err := store.Close(); err != nil {
		slog.Error("close notification store", "error", err)
	}
}
