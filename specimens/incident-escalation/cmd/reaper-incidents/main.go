// Command reaper-incidents serves the customer-owned incident escalation specimen.
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
	"sync"
	"syscall"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/api"
	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/incident"
	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/store/sqlite"
	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/transport/smtpnotify"
	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/transport/webhooknotify"
	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/worker"
)

const workerBatch = 25

func main() {
	if err := run(); err != nil {
		slog.Error("reaper incidents stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configuration, err := loadConfig()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(configuration.databasePath), 0o700); err != nil {
		return fmt.Errorf("create incident database directory: %w", err)
	}
	store, err := sqlite.Open(configuration.databasePath)
	if err != nil {
		return err
	}
	defer closeStore(store)
	// The clock offset exists so restart-durability proofs can move time forward
	// on a fresh process; every policy decision reads this one clock.
	offset := configuration.clockOffset
	now := func() time.Time { return time.Now().Add(offset) }
	desk, err := incident.NewDesk(store, configuration.adminActor, now, incident.RandomID, incident.RandomSecret)
	if err != nil {
		return err
	}
	dispatcher, err := buildDispatcher(configuration, store, now)
	if err != nil {
		return err
	}
	pollers, err := buildPollers(configuration, desk, dispatcher)
	if err != nil {
		return err
	}
	apiServer, err := api.New(desk, store, configuration.adminToken, configuration.readToken)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr:              configuration.address,
		Handler:           apiServer.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return serve(httpServer, pollers, configuration.databasePath)
}

func buildDispatcher(configuration config, store *sqlite.Store, now incident.Clock) (*incident.Dispatcher, error) {
	schedule, err := incident.NewRetrySchedule(configuration.retryDelays)
	if err != nil {
		return nil, err
	}
	webhooks, err := webhooknotify.New(configuration.requestTimeout)
	if err != nil {
		return nil, err
	}
	email, err := smtpnotify.New(configuration.smtpAddress, configuration.smtpFrom, configuration.requestTimeout)
	if err != nil {
		return nil, err
	}
	notifiers := map[incident.Channel]incident.Notifier{
		incident.ChannelWebhook: webhooks,
		incident.ChannelEmail:   email,
	}
	lease := configuration.requestTimeout + 15*time.Second
	return incident.NewDispatcher(store, notifiers, schedule, now, lease)
}

func buildPollers(configuration config, desk *incident.Desk, dispatcher *incident.Dispatcher) ([]*worker.Poller, error) {
	escalations, err := worker.NewPoller(
		"escalation-timer",
		worker.RunnerFunc(desk.EscalateDue),
		worker.TimerWaiter{},
		slog.Default(),
		configuration.pollInterval,
		workerBatch,
	)
	if err != nil {
		return nil, err
	}
	notifications, err := worker.NewPoller(
		"notifications",
		worker.RunnerFunc(dispatcher.DeliverDue),
		worker.TimerWaiter{},
		slog.Default(),
		configuration.pollInterval,
		workerBatch,
	)
	if err != nil {
		return nil, err
	}
	return []*worker.Poller{escalations, notifications}, nil
}

func serve(httpServer *http.Server, pollers []*worker.Poller, databasePath string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var pollersDone sync.WaitGroup
	for _, poller := range pollers {
		pollersDone.Add(1)
		go func() {
			defer pollersDone.Done()
			poller.Run(ctx)
		}()
	}
	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("reaper incidents listening", "address", httpServer.Addr, "database", databasePath)
		serverErrors <- httpServer.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		stop()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		shutdownErr := httpServer.Shutdown(shutdownContext)
		pollersDone.Wait()
		return shutdownErr
	case listenErr := <-serverErrors:
		stop()
		pollersDone.Wait()
		if errors.Is(listenErr, http.ErrServerClosed) {
			return nil
		}
		return listenErr
	}
}

func closeStore(store *sqlite.Store) {
	if err := store.Close(); err != nil {
		slog.Error("close incident store", "error", err)
	}
}
