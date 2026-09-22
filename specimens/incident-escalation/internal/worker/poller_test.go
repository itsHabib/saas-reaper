package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type countingWaiter struct {
	mu    sync.Mutex
	waits int
	stop  int
}

func (w *countingWaiter) Wait(_ context.Context, _ time.Duration) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.waits++
	if w.stop > 0 && w.waits >= w.stop {
		return errors.New("stopped")
	}
	return nil
}

func TestPollerDrainsAFullBatchWithoutWaiting(t *testing.T) {
	waiter := &countingWaiter{stop: 1}
	calls := 0
	runner := RunnerFunc(func(context.Context, int) (int, error) {
		calls++
		if calls < 3 {
			return 5, nil
		}
		return 1, nil
	})
	poller, err := NewPoller("test", runner, waiter, slog.Default(), time.Millisecond, 5)
	if err != nil {
		t.Fatal(err)
	}
	poller.Run(context.Background())
	if calls != 3 {
		t.Fatalf("a full batch must be followed immediately by another drain, got %d calls", calls)
	}
	if waiter.waits != 1 {
		t.Fatalf("only the short batch waits, got %d", waiter.waits)
	}
}

func TestPollerKeepsRunningAfterAnError(t *testing.T) {
	waiter := &countingWaiter{stop: 3}
	calls := 0
	runner := RunnerFunc(func(context.Context, int) (int, error) {
		calls++
		return 0, errors.New("authority unavailable")
	})
	poller, err := NewPoller("test", runner, waiter, slog.Default(), time.Millisecond, 5)
	if err != nil {
		t.Fatal(err)
	}
	poller.Run(context.Background())
	if calls != 3 {
		t.Fatalf("an error must not stop the loop, got %d calls", calls)
	}
}

func TestPollerStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runner := RunnerFunc(func(context.Context, int) (int, error) {
		cancel()
		return 0, nil
	})
	poller, err := NewPoller("test", runner, TimerWaiter{}, slog.Default(), time.Hour, 5)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		poller.Run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the poller did not stop on cancellation")
	}
}

func TestTimerWaiterHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (TimerWaiter{}).Wait(ctx, time.Hour); err == nil {
		t.Fatal("a canceled wait must return an error")
	}
	if err := (TimerWaiter{}).Wait(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("a completed wait must succeed, got %v", err)
	}
}

func TestPollerRejectsIncompleteComposition(t *testing.T) {
	runner := RunnerFunc(func(context.Context, int) (int, error) { return 0, nil })
	cases := map[string]func() (*Poller, error){
		"no name":    func() (*Poller, error) { return NewPoller("", runner, TimerWaiter{}, slog.Default(), time.Second, 5) },
		"no runner":  func() (*Poller, error) { return NewPoller("t", nil, TimerWaiter{}, slog.Default(), time.Second, 5) },
		"no waiter":  func() (*Poller, error) { return NewPoller("t", runner, nil, slog.Default(), time.Second, 5) },
		"no logger":  func() (*Poller, error) { return NewPoller("t", runner, TimerWaiter{}, nil, time.Second, 5) },
		"zero tick":  func() (*Poller, error) { return NewPoller("t", runner, TimerWaiter{}, slog.Default(), 0, 5) },
		"zero batch": func() (*Poller, error) { return NewPoller("t", runner, TimerWaiter{}, slog.Default(), time.Second, 0) },
		"large batch": func() (*Poller, error) {
			return NewPoller("t", runner, TimerWaiter{}, slog.Default(), time.Second, 101)
		},
	}
	for name, build := range cases {
		if _, err := build(); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
}
