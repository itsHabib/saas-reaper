package delivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

const (
	attemptAuditTimeout = 10 * time.Second
	// auditFailureBackoff keeps a delivery whose attempt could not be audited off the queue head.
	auditFailureBackoff = 30 * time.Second
	// maxDueBatch is the largest batch the store will select in one pass.
	maxDueBatch = 100
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
	Deliverable(context.Context, string) (bool, error)
	RecordAttempt(context.Context, Attempt) error
}

// Sender owns the HTTP mechanism used by retry policy.
type Sender interface {
	Send(context.Context, string, []byte, Headers) (SendResult, error)
}

// Dispatcher signs due work immediately before sending and persists every outcome.
//
// One poller drives a Dispatcher. The store's attempt transaction is the only
// arbiter between an in-flight send and a concurrent disable: an attempt the
// store rejects as conflicting or disabled lost that race and is not retried.
type Dispatcher struct {
	store    DispatchStore
	sender   Sender
	schedule Schedule
	now      Clock
	logger   *slog.Logger
	parked   map[string]time.Time
}

// NewDispatcher composes policy with swappable persistence, HTTP, and time mechanisms.
func NewDispatcher(
	store DispatchStore,
	sender Sender,
	schedule Schedule,
	now Clock,
	logger *slog.Logger,
) (*Dispatcher, error) {
	if store == nil || sender == nil || now == nil || logger == nil {
		return nil, fmt.Errorf("%w: dispatch store, sender, clock, and logger are required", ErrInvalid)
	}
	return &Dispatcher{
		store:    store,
		sender:   sender,
		schedule: schedule,
		now:      now,
		logger:   logger,
		parked:   map[string]time.Time{},
	}, nil
}

// DeliverDue attempts one batch of at most limit due deliveries in deterministic queue order.
//
// Each delivery is rechecked immediately before its send, so a cancellation
// this batch itself caused - a 410 disabling an endpoint shared by later items
// - never produces an unaudited POST to an endpoint already disabled. A
// delivery whose attempt cannot be audited is parked in memory for
// auditFailureBackoff, and selection over-fetches by the parked count so a
// fully parked prefix cannot hide auditable work behind it.
func (d *Dispatcher) DeliverDue(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > maxDueBatch {
		return 0, fmt.Errorf("%w: due limit must be between 1 and %d", ErrInvalid, maxDueBatch)
	}
	now := d.now().UTC()
	d.releaseParked(now)
	due, err := d.store.Due(ctx, now, min(limit+len(d.parked), maxDueBatch))
	if err != nil {
		return 0, fmt.Errorf("load due deliveries: %w", err)
	}
	attempted := 0
	var dispatchErrors []error
	for _, item := range due {
		if ctx.Err() != nil || attempted == limit {
			break
		}
		sent, err := d.attempt(ctx, item)
		if err != nil {
			dispatchErrors = append(dispatchErrors, err)
		}
		if sent {
			attempted++
		}
	}
	return attempted, errors.Join(dispatchErrors...)
}

// attempt reports whether the delivery was sent; a skipped delivery is neither sent nor an error.
func (d *Dispatcher) attempt(ctx context.Context, item Dispatch) (bool, error) {
	if _, parked := d.parked[item.DeliveryID]; parked {
		return false, nil
	}
	deliverable, err := d.store.Deliverable(ctx, item.DeliveryID)
	if err != nil {
		return false, fmt.Errorf("recheck delivery %s: %w", item.DeliveryID, err)
	}
	if !deliverable {
		d.logger.InfoContext(ctx, "skipped a delivery canceled since its batch was selected",
			"delivery", item.DeliveryID, "endpoint", item.EndpointID)
		return false, nil
	}
	return true, d.deliver(ctx, item)
}

func (d *Dispatcher) releaseParked(now time.Time) {
	for id, until := range d.parked {
		if !until.After(now) {
			delete(d.parked, id)
		}
	}
}

func (d *Dispatcher) deliver(ctx context.Context, item Dispatch) error {
	attemptedAt := d.now().UTC()
	headers, err := Sign(item.Secret, item.MessageID, attemptedAt, item.Payload)
	if err != nil {
		return d.record(ctx, item, SendResult{}, fmt.Errorf("%w: %w", errPermanent, err), attemptedAt)
	}
	result, sendErr := d.sender.Send(ctx, item.Destination, item.Payload, headers)
	return d.record(ctx, item, result, sendErr, attemptedAt)
}

func (d *Dispatcher) record(
	ctx context.Context,
	item Dispatch,
	result SendResult,
	sendErr error,
	attemptedAt time.Time,
) error {
	completedAt := d.now().UTC()
	attempt := d.schedule.resolve(item, result, sendErr, attemptedAt, completedAt)
	auditContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), attemptAuditTimeout)
	defer cancel()
	err := d.store.RecordAttempt(auditContext, attempt)
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrConflict) || errors.Is(err, ErrDisabled) {
		d.logger.InfoContext(ctx, "delivery attempt lost a concurrent transition",
			"delivery", item.DeliveryID, "attempt", attempt.Number, "reason", err)
		return nil
	}
	d.parked[item.DeliveryID] = completedAt.Add(auditFailureBackoff)
	return fmt.Errorf(
		"record delivery %s attempt %d (parked for %s): %w",
		item.DeliveryID,
		attempt.Number,
		auditFailureBackoff,
		err,
	)
}
