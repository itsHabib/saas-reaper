// Command reaper-tunnel-agent exposes one local origin through a claimed subdomain on a
// reaper-tunnel server. It needs only the server URL, its credential, and the target.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/agent"
	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/link"
	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/tunnel"
)

func main() {
	if err := run(); err != nil {
		slog.Error("reaper tunnel agent stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configuration, err := loadConfig()
	if err != nil {
		return err
	}
	dialer, err := link.NewDialer(configuration.serverURL, configuration.token, configuration.dialTimeout, configuration.link)
	if err != nil {
		return err
	}
	schedule, err := tunnel.NewSchedule(configuration.reconnect)
	if err != nil {
		return err
	}
	logger := slog.Default()
	forwarder, err := agent.New(listenerDialer{dialer}, configuration.target, schedule, agent.TimerWaiter{}, logger)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("reaper tunnel agent starting", "server", configuration.serverURL, "target", configuration.target.String())
	return forwarder.Run(ctx)
}

// listenerDialer adapts the concrete link dialer to the agent's interface: the dialer returns
// its concrete type, as the house style asks, and the agent consumes only what it needs.
type listenerDialer struct {
	dialer *link.Dialer
}

func (d listenerDialer) Dial(ctx context.Context) (agent.Listener, error) {
	listener, err := d.dialer.Dial(ctx)
	if err != nil {
		return nil, err
	}
	return listener, nil
}
