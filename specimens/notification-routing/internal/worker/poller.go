// Package worker executes due-delivery policy on an injectable polling clock.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// Runner is the delivery policy consumed by the polling mechanism.
type Runner interface {
	DeliverDue(context.Context, int) (int, error)
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
	runner   Runner
	waiter   Waiter
	logger   *slog.Logger
	interval time.Duration
	batch    int
}

// NewPoller validates and composes the background mechanism.
func NewPoller(runner Runner, waiter Waiter, logger *slog.Logger, interval time.Duration, batch int) (*Poller, error) {
	if runner == nil || waiter == nil || logger == nil {
		return nil, errors.New("runner, waiter, and logger are required")
	}
	if interval <= 0 || batch < 1 || batch > 100 {
		return nil, errors.New("positive poll interval and batch between 1 and 100 are required")
	}
	return &Poller{runner: runner, waiter: waiter, logger: logger, interval: interval, batch: batch}, nil
}

// Run processes work until cancellation. A partially failed batch is logged and the poller
// waits one interval before the next batch so a persistently failing audit cannot spin.
func (p *Poller) Run(ctx context.Context) {
	for ctx.Err() == nil {
		count, err := p.runner.DeliverDue(ctx, p.batch)
		if err != nil {
			p.logger.ErrorContext(ctx, "deliver due notifications", "error", err)
		}
		if err == nil && count == p.batch {
			continue
		}
		if err := p.waiter.Wait(ctx, p.interval); err != nil {
			return
		}
	}
}
