// Package agent is the customer-side mechanism: hold one link to the server, serve every
// request stream into the local target, and reconnect on the policy schedule when the link is
// lost. A refused credential ends the agent; a lost link never does.
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

// Dialer opens one link; the returned listener yields one connection per proxied request.
type Dialer interface {
	Dial(context.Context) (net.Listener, error)
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
// triple the edge stamped; the target sees its own host, as it would behind any local proxy.
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
		listener, err := a.dialer.Dial(ctx)
		if err == nil {
			failures = 0
			a.logger.Info("tunnel link established")
			if err := a.serve(ctx, listener); permanent(err) {
				return err
			}
			a.logger.Warn("tunnel link lost; reconnecting")
			failures++
			if !a.pause(ctx, failures) {
				return nil
			}
			continue
		}
		if permanent(err) {
			return err
		}
		failures++
		a.logger.Warn("tunnel dial failed; retrying", "error", err, "delay", a.schedule.Delay(failures))
		if !a.pause(ctx, failures) {
			return nil
		}
	}
	return nil
}

// pause waits out the schedule for the given failure count and reports whether the run should
// go on; a canceled wait ends the run cleanly.
func (a *Agent) pause(ctx context.Context, failures int) bool {
	return a.waiter.Wait(ctx, a.schedule.Delay(failures)) == nil
}

// serve answers request streams until the listener closes or the context ends. It returns the
// listener's final error so an eviction can be told apart from a lost link.
func (a *Agent) serve(ctx context.Context, listener net.Listener) error {
	server := &http.Server{
		Handler:           a.handler,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(a.logger.Handler(), slog.LevelWarn),
	}
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(reportingListener{Listener: listener, final: done})
	}()
	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		_ = listener.Close()
		err = <-done
	}
	_ = server.Close()
	return err
}

// reportingListener preserves the Accept error that ended Serve, which net/http otherwise
// replaces with its own generic failure.
type reportingListener struct {
	net.Listener
	final chan error
}

func (r reportingListener) Accept() (net.Conn, error) {
	conn, err := r.Listener.Accept()
	if err != nil && permanent(err) {
		select {
		case r.final <- err:
		default:
		}
	}
	return conn, err
}

// permanent reports a refusal that retrying with the same credential can never overcome.
func permanent(err error) bool {
	var refused interface{ Permanent() bool }
	if errors.As(err, &refused) {
		return refused.Permanent()
	}
	return false
}
