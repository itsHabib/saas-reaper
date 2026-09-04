// Package worker drives due-work policy on an injectable polling clock.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// Runner is one due-work policy consumed by the polling mechanism.
type Runner interface {
	RunDue(context.Context, int) (int, error)
}

// RunnerFunc adapts a policy method to the Runner seam.
type RunnerFunc func(context.Context, int) (int, error)

// RunDue calls the adapted policy method.
func (f RunnerFunc) RunDue(ctx context.Context, limit int) (int, error) {
	return f(ctx, limit)
}

// Waiter owns the replaceable passage of polling time.
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

// Poller repeatedly drains bounded batches of due work.
type Poller struct {
	name     string
	runner   Runner
	waiter   Waiter
	logger   *slog.Logger
	interval time.Duration
	batch    int
}

// NewPoller validates and composes the background mechanism.
func NewPoller(
	name string,
	runner Runner,
	waiter Waiter,
	logger *slog.Logger,
	interval time.Duration,
	batch int,
) (*Poller, error) {
	if name == "" || runner == nil || waiter == nil || logger == nil {
		return nil, errors.New("name, runner, waiter, and logger are required")
	}
	if interval <= 0 || batch < 1 || batch > 100 {
		return nil, errors.New("positive poll interval and batch between 1 and 100 are required")
	}
	return &Poller{name: name, runner: runner, waiter: waiter, logger: logger, interval: interval, batch: batch}, nil
}

// Run processes work until cancellation; a full batch is followed immediately by another.
func (p *Poller) Run(ctx context.Context) {
	for ctx.Err() == nil {
		count, err := p.runner.RunDue(ctx, p.batch)
		if err != nil {
			p.logger.ErrorContext(ctx, "run due work", "poller", p.name, "error", err)
		}
		if err == nil && count == p.batch {
			continue
		}
		if err := p.waiter.Wait(ctx, p.interval); err != nil {
			return
		}
	}
}
