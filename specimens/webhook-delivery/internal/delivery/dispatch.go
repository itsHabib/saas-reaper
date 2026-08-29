package delivery

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Dispatch is the complete private input for one outbound attempt.
type Dispatch struct {
	DeliveryID   string
	MessageID    string
	EndpointID   string
	Actor        string
	Destination  string
	Secret       string
	Payload      []byte
	AttemptCount int
}

// DispatchStore supplies due work and atomically records attempt plus transition.
type DispatchStore interface {
	Due(context.Context, time.Time, int) ([]Dispatch, error)
	RecordAttempt(context.Context, Attempt) error
}

// Sender owns the HTTP mechanism used by retry policy.
type Sender interface {
	Send(context.Context, string, []byte, Headers) (SendResult, error)
}

// Dispatcher signs due work immediately before sending and persists every outcome.
type Dispatcher struct {
	store    DispatchStore
	sender   Sender
	schedule Schedule
	now      Clock
	attempts *AttemptCoordinator
}

// NewDispatcher composes policy with swappable persistence, HTTP, and time mechanisms.
func NewDispatcher(
	store DispatchStore,
	sender Sender,
	schedule Schedule,
	now Clock,
	attempts *AttemptCoordinator,
) (*Dispatcher, error) {
	if store == nil || sender == nil || now == nil || attempts == nil {
		return nil, fmt.Errorf("%w: dispatch store, sender, clock, and attempt coordinator are required", ErrInvalid)
	}
	return &Dispatcher{store: store, sender: sender, schedule: schedule, now: now, attempts: attempts}, nil
}

// DeliverDue attempts at most limit due deliveries in deterministic queue order.
func (d *Dispatcher) DeliverDue(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		return 0, fmt.Errorf("%w: due limit must be between 1 and 100", ErrInvalid)
	}
	var dispatchErrors []error
	attempted := 0
	for attempted < limit {
		found := false
		err := d.attempts.run(ctx, func() error {
			due, dueErr := d.store.Due(ctx, d.now().UTC(), 1)
			if dueErr != nil {
				return fmt.Errorf("load due deliveries: %w", dueErr)
			}
			if len(due) == 0 {
				return nil
			}
			found = true
			return d.deliver(ctx, due[0])
		})
		if found {
			attempted++
		}
		if err != nil {
			dispatchErrors = append(dispatchErrors, err)
			break
		}
		if !found {
			break
		}
	}
	return attempted, errors.Join(dispatchErrors...)
}

func (d *Dispatcher) deliver(ctx context.Context, item Dispatch) error {
	attemptedAt := d.now().UTC()
	headers, signErr := Sign(item.Secret, item.MessageID, attemptedAt, item.Payload)
	result := SendResult{}
	var sendErr error
	if signErr == nil {
		result, sendErr = d.sender.Send(ctx, item.Destination, item.Payload, headers)
	}
	attempt := d.schedule.resolve(item, result, errors.Join(signErr, sendErr), attemptedAt)
	if err := d.store.RecordAttempt(ctx, attempt); err != nil {
		return fmt.Errorf("record delivery %s attempt %d: %w", item.DeliveryID, attempt.Number, err)
	}
	return nil
}
