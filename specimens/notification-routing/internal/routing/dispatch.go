package routing

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const attemptAuditTimeout = 10 * time.Second

// Dispatch is the complete private input for one transport attempt.
type Dispatch struct {
	DeliveryID     string
	NotificationID string
	RecipientID    string
	ChannelID      string
	Kind           ChannelKind
	Actor          string
	Address        string
	Subject        string
	Body           string
	AttemptCount   int
}

// Envelope is the narrow, rendered input a transport mechanism needs.
type Envelope struct {
	DeliveryID     string
	NotificationID string
	Address        string
	Subject        string
	Body           string
	Attempt        int
	AttemptedAt    time.Time
}

// Transport speaks one customer-owned wire protocol and classifies its failures.
//
// A returned error that wraps ErrPermanent ends retries; any other error is transient.
type Transport interface {
	Deliver(context.Context, Envelope) (Receipt, error)
}

// DispatchStore supplies due work and atomically records attempt plus transition.
type DispatchStore interface {
	Due(context.Context, time.Time, int) ([]Dispatch, error)
	RecordAttempt(context.Context, Attempt) error
}

// Dispatcher routes due deliveries to the transport for their channel kind and audits every outcome.
type Dispatcher struct {
	store      DispatchStore
	transports map[ChannelKind]Transport
	schedule   Schedule
	now        Clock
}

// NewDispatcher composes policy with swappable persistence, transport, and time mechanisms.
func NewDispatcher(
	store DispatchStore,
	transports map[ChannelKind]Transport,
	schedule Schedule,
	now Clock,
) (*Dispatcher, error) {
	if store == nil || now == nil || len(transports) == 0 {
		return nil, fmt.Errorf("%w: dispatch store, clock, and at least one transport are required", ErrInvalid)
	}
	copied := make(map[ChannelKind]Transport, len(transports))
	for kind, transport := range transports {
		if !kind.Known() || transport == nil {
			return nil, fmt.Errorf("%w: transport for unknown kind %q", ErrInvalid, kind)
		}
		copied[kind] = transport
	}
	return &Dispatcher{store: store, transports: copied, schedule: schedule, now: now}, nil
}

// DeliverDue attempts one batch of due deliveries in queue order.
//
// A failed audit write for one delivery never stops its siblings: the loop continues and the
// joined errors are returned for logging. Deterministic audit rejections (a channel disabled or
// a state changed under the send) are resolved by the store, so no row can poison the queue.
func (d *Dispatcher) DeliverDue(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		return 0, fmt.Errorf("%w: due limit must be between 1 and 100", ErrInvalid)
	}
	due, err := d.store.Due(ctx, d.now().UTC(), limit)
	if err != nil {
		return 0, fmt.Errorf("load due deliveries: %w", err)
	}
	var dispatchErrors []error
	for _, item := range due {
		if err := d.deliver(ctx, item); err != nil {
			dispatchErrors = append(dispatchErrors, err)
		}
	}
	return len(due), errors.Join(dispatchErrors...)
}

func (d *Dispatcher) deliver(ctx context.Context, item Dispatch) error {
	attemptedAt := d.now().UTC()
	receipt, sendErr := d.send(ctx, item, attemptedAt)
	completedAt := d.now().UTC()
	attempt := d.schedule.resolve(item, receipt, sendErr, attemptedAt, completedAt)
	auditContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), attemptAuditTimeout)
	defer cancel()
	if err := d.store.RecordAttempt(auditContext, attempt); err != nil {
		return fmt.Errorf("record delivery %s attempt %d: %w", item.DeliveryID, attempt.Number, err)
	}
	return nil
}

func (d *Dispatcher) send(ctx context.Context, item Dispatch, attemptedAt time.Time) (Receipt, error) {
	transport, configured := d.transports[item.Kind]
	if !configured {
		return Receipt{}, fmt.Errorf("%w: no transport configured for channel kind %q", ErrPermanent, item.Kind)
	}
	return transport.Deliver(ctx, Envelope{
		DeliveryID:     item.DeliveryID,
		NotificationID: item.NotificationID,
		Address:        item.Address,
		Subject:        item.Subject,
		Body:           item.Body,
		Attempt:        item.AttemptCount + 1,
		AttemptedAt:    attemptedAt,
	})
}
