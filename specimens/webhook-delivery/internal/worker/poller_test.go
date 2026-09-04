package worker

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

var errInjectedWait = errors.New("injected waiter stop")

type runnerStub struct {
	calls int
	count int
	err   error
}

func (r *runnerStub) DeliverDue(context.Context, int) (int, error) {
	r.calls++
	return r.count, r.err
}

type waiterStub struct {
	calls int
	delay time.Duration
	err   error
}

func (w *waiterStub) Wait(_ context.Context, delay time.Duration) error {
	w.calls++
	w.delay = delay
	return w.err
}

func TestTimerWaiterReturnsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (TimerWaiter{}).Wait(ctx, time.Hour)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}

func TestPollerUsesInjectedWaiter(t *testing.T) {
	runner := &runnerStub{}
	waiter := &waiterStub{err: errInjectedWait}
	logger := slog.New(slog.DiscardHandler)
	poller, err := NewPoller(runner, waiter, logger, 125*time.Millisecond, 10)
	if err != nil {
		t.Fatal(err)
	}

	poller.Run(context.Background())
	if runner.calls != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.calls)
	}
	if waiter.calls != 1 {
		t.Fatalf("waiter calls = %d, want 1", waiter.calls)
	}
	if waiter.delay != 125*time.Millisecond {
		t.Fatalf("wait delay = %s, want 125ms", waiter.delay)
	}
}

func TestPollerWaitsAfterFullBatchFailure(t *testing.T) {
	runner := &runnerStub{count: 10, err: errors.New("audit unavailable")}
	waiter := &waiterStub{err: errInjectedWait}
	logger := slog.New(slog.DiscardHandler)
	poller, err := NewPoller(runner, waiter, logger, 125*time.Millisecond, 10)
	if err != nil {
		t.Fatal(err)
	}

	poller.Run(context.Background())
	if runner.calls != 1 || waiter.calls != 1 {
		t.Fatalf("runner calls = %d, waiter calls = %d; want one each", runner.calls, waiter.calls)
	}
}

func TestPollerStopsBeforeWorkWhenCanceled(t *testing.T) {
	runner := &runnerStub{}
	waiter := &waiterStub{}
	logger := slog.New(slog.DiscardHandler)
	poller, err := NewPoller(runner, waiter, logger, time.Second, 10)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	poller.Run(ctx)
	if runner.calls != 0 {
		t.Fatalf("runner calls = %d, want 0", runner.calls)
	}
	if waiter.calls != 0 {
		t.Fatalf("waiter calls = %d, want 0", waiter.calls)
	}
}
