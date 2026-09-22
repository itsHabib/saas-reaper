// Package agent is the customer-side mechanism: hold one link to the server, serve every
// request stream into the local target, and reconnect on the policy schedule when the link is
// lost. A refused credential or a deliberate eviction ends the agent; a lost link never does.
package agent

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/tunnel"
)

// Listener yields one connection per proxied request and, once it has ended, says why.
type Listener interface {
	net.Listener
	Reason() error
}

// Dialer opens one link.
type Dialer interface {
	Dial(context.Context) (Listener, error)
}

// Waiter owns the replaceable passage of reconnect time.
type Waiter interface {
	Wait(context.Context, time.Duration) error
}

// TimerWaiter uses a cancelable wall-clock timer.
type TimerWaiter struct{}

// Wait blocks until the duration passes or the context is canceled.
func (TimerWaiter) Wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Agent forwards one tunnel into one local target.
type Agent struct {
	dialer   Dialer
	waiter   Waiter
	schedule tunnel.Schedule
	logger   *slog.Logger
	handler  http.Handler
}

// New validates the composition. target is the local origin the tunnel exposes.
func New(dialer Dialer, target *url.URL, schedule tunnel.Schedule, waiter Waiter, logger *slog.Logger) (*Agent, error) {
	if dialer == nil || target == nil || waiter == nil || logger == nil {
		return nil, errors.New("dialer, target, waiter, and logger are required")
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, errors.New("target must use http or https")
	}
	if target.Host == "" {
		return nil, errors.New("target needs a host")
	}
	proxy := &httputil.ReverseProxy{
		Rewrite:       rewriteTo(target),
		FlushInterval: -1,
		ErrorLog:      slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	return &Agent{dialer: dialer, waiter: waiter, schedule: schedule, logger: logger, handler: proxy}, nil
}

// rewriteTo points each stream's request at the local target while preserving the forwarded
// triple the edge stamped. In rewrite mode the standard proxy strips those headers from the
// outbound request before this runs, so they are copied back from the inbound one; the target
// sees its own host, as it would behind any local proxy.
func rewriteTo(target *url.URL) func(*httputil.ProxyRequest) {
	return func(request *httputil.ProxyRequest) {
		request.SetURL(target)
		request.Out.Host = target.Host
		for _, name := range []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
			request.Out.Header.Del(name)
			for _, value := range request.In.Header.Values(name) {
				request.Out.Header.Add(name, value)
			}
		}
	}
}

// Run holds the tunnel open until the context ends, the server refuses the credential, or the
// server evicts this agent on purpose. A lost link is always retried.
func (a *Agent) Run(ctx context.Context) error {
	failures := 0
	for ctx.Err() == nil {
		var err error
		failures, err = a.attempt(ctx, failures)
		if err != nil {
			return err
		}
		if !a.pause(ctx, failures) {
			return nil
		}
	}
	return nil
}

// attempt dials once and, on success, serves until the link ends. It returns the failure
// count the next pause is computed from, and an error only when the run must stop.
func (a *Agent) attempt(ctx context.Context, failures int) (int, error) {
	listener, err := a.dialer.Dial(ctx)
	if permanent(err) {
		return failures, err
	}
	if err != nil {
		a.logger.Warn("tunnel dial failed; retrying", "error", err, "delay", a.schedule.Delay(failures+1))
		return failures + 1, nil
	}
	a.logger.Info("tunnel link established")
	a.serve(ctx, listener)
	if reason := listener.Reason(); permanent(reason) {
		return 0, reason
	}
	a.logger.Warn("tunnel link lost; reconnecting", "delay", a.schedule.Delay(1))
	return 1, nil
}

// pause waits out the schedule for the given failure count and reports whether the run should
// go on; a canceled wait ends the run cleanly.
func (a *Agent) pause(ctx context.Context, failures int) bool {
	return a.waiter.Wait(ctx, a.schedule.Delay(failures)) == nil
}

// serve answers request streams until the listener closes or the context ends.
func (a *Agent) serve(ctx context.Context, listener Listener) {
	server := &http.Server{
		Handler:           a.handler,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(a.logger.Handler(), slog.LevelWarn),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(listener)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		a.closeListener(listener)
		<-done
	}
	// Serve returning does not close the listener it was given; a lost link is closed here
	// on every path so a flapping server cannot leak one socket per reconnect.
	a.closeListener(listener)
	if err := server.Close(); err != nil {
		a.logger.Warn("close stream server", "error", err)
	}
}

func (a *Agent) closeListener(listener Listener) {
	if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		a.logger.Warn("close tunnel link", "error", err)
	}
}

// permanent reports a refusal that retrying with the same credential can never overcome.
func permanent(err error) bool {
	var refused interface{ Permanent() bool }
	if errors.As(err, &refused) {
		return refused.Permanent()
	}
	return false
}
