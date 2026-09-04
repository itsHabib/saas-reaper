package incident

import (
	"context"
	"errors"
	"fmt"
)

// ingestAttempts bounds how many times one event re-reads and re-applies after
// losing an optimistic race. Concurrent events for one dedup key serialize
// through the store, so a small bound is enough for the fan-in a sender
// produces; exhausting it is reported as a conflict the caller should retry.
const ingestAttempts = 8

// ServiceActor is the audit principal derived from a verified routing key.
func ServiceActor(service Service) string {
	return "service:" + service.ID
}

// Ingest applies one Events API v2 event; the routing key is the only credential.
//
// Trigger opens an incident unless one is already open for the dedup key, in
// which case it is journaled as a duplicate. Acknowledge and resolve act on the
// open incident and are accepted silently when none exists, matching the
// upstream contract. A uniqueness or revision race is re-read and re-applied a
// bounded number of times, because dropping the event would lose a resolve.
func (d *Desk) Ingest(ctx context.Context, alert Alert) (Receipt, error) {
	if err := validateAlert(alert); err != nil {
		return Receipt{}, err
	}
	service, err := d.store.ServiceByRoutingKey(ctx, alert.RoutingKey)
	if errors.Is(err, ErrNotFound) {
		return Receipt{}, fmt.Errorf("%w: routing_key is not recognized", ErrInvalid)
	}
	if err != nil {
		return Receipt{}, err
	}
	if alert.DedupKey == "" {
		generated, generateErr := d.generate("")
		if generateErr != nil {
			return Receipt{}, generateErr
		}
		alert.DedupKey = generated
	}
	// A lost race means another event for this dedup key landed first, so the
	// incident must be re-read and the signal re-applied. Dropping the event
	// here would silently lose an acknowledge or a resolve.
	for range ingestAttempts {
		err = d.apply(ctx, service, alert)
		if !errors.Is(err, ErrConflict) {
			break
		}
	}
	if err != nil {
		return Receipt{}, err
	}
	return Receipt{DedupKey: alert.DedupKey}, nil
}

func (d *Desk) apply(ctx context.Context, service Service, alert Alert) error {
	current, err := d.store.OpenIncident(ctx, service.ID, alert.DedupKey)
	if errors.Is(err, ErrNotFound) {
		if alert.Action != ActionTrigger {
			return nil
		}
		return d.open(ctx, service, alert)
	}
	if err != nil {
		return err
	}
	return d.signal(ctx, current, Signal(alert.Action), ServiceActor(service))
}

func (d *Desk) open(ctx context.Context, service Service, alert Alert) error {
	targets, err := d.store.Targets(ctx, service.PolicyID)
	if err != nil {
		return err
	}
	id, err := d.generate("inc_")
	if err != nil {
		return err
	}
	transition := Open(id, service, targets.Policy, alert, ServiceActor(service), d.now())
	notifications, err := d.plan(transition, targets)
	if err != nil {
		return err
	}
	return d.store.CreateIncident(ctx, transition.Incident, transition.Event, notifications)
}

func (d *Desk) signal(ctx context.Context, current Incident, signal Signal, actor string) error {
	targets, err := d.store.Targets(ctx, current.PolicyID)
	if err != nil {
		return err
	}
	transition, err := Step(current, targets.Policy, signal, actor, d.now())
	if err != nil {
		return err
	}
	if !transition.Changed {
		return nil
	}
	notifications, err := d.plan(transition, targets)
	if err != nil {
		return err
	}
	return d.store.Transition(ctx, transition.Incident, current.Revision, transition.Event, notifications)
}

func (d *Desk) plan(transition Transition, targets Targets) ([]Notification, error) {
	if !transition.Notify {
		return nil, nil
	}
	responders, err := targets.LevelResponders(transition.Incident.Level, transition.Event.At)
	if err != nil {
		return nil, err
	}
	return PlanNotifications(transition, responders, d.generate)
}
