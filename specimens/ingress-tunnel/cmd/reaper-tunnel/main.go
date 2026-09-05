// Command reaper-tunnel serves the customer-owned ingress-tunnel specimen: a public edge that
// routes by subdomain and a control plane where agents attach and operators manage claims.
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

	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/api"
	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/edge"
	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/link"
	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/store/sqlite"
	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/tunnel"
)

func main() {
	if err := run(); err != nil {
		slog.Error("reaper tunnel stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configuration, err := loadConfig()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(configuration.databasePath), 0o700); err != nil {
		return fmt.Errorf("create tunnel database directory: %w", err)
	}
	store, err := sqlite.Open(configuration.databasePath)
	if err != nil {
		return err
	}
	defer closeStore(store)
	logger := slog.Default()
	registry := tunnel.NewRegistry()
	service, err := tunnel.NewService(store, registry, configuration.adminActor, time.Now, nil, logger)
	if err != nil {
		return err
	}
	management, err := api.New(service, store, configuration.adminToken, configuration.readToken)
	if err != nil {
		return err
	}
	accept, err := link.NewHandler(service, configuration.link, logger)
	if err != nil {
		return err
	}
	public, err := edge.New(configuration.domain, registry, configuration.forwardProto, configuration.headerTimeout, logger)
	if err != nil {
		return err
	}
	control := http.NewServeMux()
	management.Register(control)
	control.Handle("GET /v1/connect", accept)
	return serve(configuration, control, public, logger)
}

// serve runs the control and edge listeners together and stops both on the first signal or
// listener failure. The edge has no write timeout because a tunneled response may stream for
// as long as the visitor and agent keep it open.
func serve(configuration config, control, public http.Handler, logger *slog.Logger) error {
	controlServer := &http.Server{
		Addr:              configuration.controlAddress,
		Handler:           control,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	edgeServer := &http.Server{
		Addr:              configuration.edgeAddress,
		Handler:           public,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	failures := make(chan error, 2)
	go func() {
		logger.Info("reaper tunnel control listening", "address", controlServer.Addr, "database", configuration.databasePath)
		failures <- controlServer.ListenAndServe()
	}()
	go func() {
		logger.Info("reaper tunnel edge listening", "address", edgeServer.Addr, "domain", configuration.domain)
		failures <- edgeServer.ListenAndServe()
	}()
	var listenErr error
	select {
	case <-ctx.Done():
	case listenErr = <-failures:
		stop()
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	shutdownErr := errors.Join(edgeServer.Shutdown(shutdownContext), controlServer.Shutdown(shutdownContext))
	if errors.Is(listenErr, http.ErrServerClosed) {
		listenErr = nil
	}
	return errors.Join(listenErr, shutdownErr)
}

func closeStore(store *sqlite.Store) {
	if err := store.Close(); err != nil {
		slog.Error("close tunnel store", "error", err)
	}
}
