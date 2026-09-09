package incident

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const attemptAuditTimeout = 10 * time.Second

// Dispatch is the complete private input for one outbound page.
type Dispatch struct {
	Notification Notification
	Responder    Responder
	Incident     Incident
	ServiceName  string
}

// DispatchStore supplies due pages, leases them, and atomically records every attempt.
type DispatchStore interface {
	DueNotifications(context.Context, time.Time, int) ([]Dispatch, error)
	ClaimNotification(context.Context, Notification, time.Time) error
	RecordAttempt(context.Context, Attempt) error
}

// Dispatcher leases one due page, sends it on its channel, and persists the outcome.
//
// The lease advances next_attempt_at before any I/O, so a page whose audit
// write fails is not re-sent every tick and never blocks the pages behind it;
// two processes on one database cannot both send the same attempt.
type Dispatcher struct {
	store     DispatchStore
	notifiers map[Channel]Notifier
	schedule  RetrySchedule
	now       Clock
	lease     time.Duration
}

// NewDispatcher composes policy with swappable persistence, transports, and time.
func NewDispatcher(
	store DispatchStore,
	notifiers map[Channel]Notifier,
	schedule RetrySchedule,
	now Clock,
	lease time.Duration,
) (*Dispatcher, error) {
	if store == nil || now == nil {
		return nil, fmt.Errorf("%w: dispatch store and clock are required", ErrInvalid)
	}
	for _, channel := range []Channel{ChannelWebhook, ChannelEmail} {
		if notifiers[channel] == nil {
			return nil, fmt.Errorf("%w: a notifier for the %s channel is required", ErrInvalid, channel)
		}
	}
	if lease <= 0 {
		return nil, fmt.Errorf("%w: a positive notification lease is required", ErrInvalid)
	}
	return &Dispatcher{store: store, notifiers: notifiers, schedule: schedule, now: now, lease: lease}, nil
}

// DeliverDue sends at most limit due pages in deterministic queue order.
func (d *Dispatcher) DeliverDue(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		return 0, fmt.Errorf("%w: due limit must be between 1 and 100", ErrInvalid)
	}
	due, err := d.store.DueNotifications(ctx, d.now().UTC(), limit)
	if err != nil {
		return 0, fmt.Errorf("load due notifications: %w", err)
	}
	var failures []error
	attempted := 0
	for _, item := range due {
		err := d.deliver(ctx, item)
		if errors.Is(err, ErrConflict) {
			continue
		}
		if err != nil {
			failures = append(failures, err)
		}
		attempted++
	}
	return attempted, errors.Join(failures...)
}

func (d *Dispatcher) deliver(ctx context.Context, item Dispatch) error {
	notifier := d.notifiers[item.Notification.Channel]
	if notifier == nil {
		return fmt.Errorf("%w: no notifier for channel %q", ErrInvalid, item.Notification.Channel)
	}
	attemptedAt := d.now().UTC()
	if err := d.store.ClaimNotification(ctx, item.Notification, attemptedAt.Add(d.lease)); err != nil {
		return err
	}
	sendErr := notifier.Notify(ctx, Message{
		NotificationID: item.Notification.ID,
		Kind:           messageKind(item.Notification),
		Responder:      item.Responder,
		Incident:       item.Incident,
		ServiceName:    item.ServiceName,
		SentAt:         attemptedAt,
	})
	attempt := d.schedule.Resolve(item.Notification, sendErr, attemptedAt, d.now().UTC())
	auditContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), attemptAuditTimeout)
	defer cancel()
	if err := d.store.RecordAttempt(auditContext, attempt); err != nil {
		return fmt.Errorf("record notification %s attempt %d: %w", item.Notification.ID, attempt.Number, err)
	}
	return nil
}

func messageKind(notification Notification) EventKind {
	if notification.Level == 0 && notification.Repeat == 0 {
		return EventOpened
	}
	return EventEscalated
}
