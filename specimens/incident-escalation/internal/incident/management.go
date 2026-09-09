package incident

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/oncall"
)

// CreateResponder registers who can be paged; a webhook secret is minted when a URL is given.
func (d *Desk) CreateResponder(ctx context.Context, responder Responder) (Responder, error) {
	if err := validateResponder(responder); err != nil {
		return Responder{}, err
	}
	responder.WebhookSecret = ""
	if responder.WebhookURL != "" {
		secret, err := d.secret()
		if err != nil {
			return Responder{}, err
		}
		responder.WebhookSecret = secret
	}
	responder.CreatedAt = d.now().UTC()
	if err := d.store.CreateResponder(ctx, responder); err != nil {
		return Responder{}, err
	}
	return responder, nil
}

// CreateSchedule stores a validated on-call declaration.
func (d *Desk) CreateSchedule(ctx context.Context, id, name string, schedule oncall.Schedule) error {
	if err := validateSlug("schedule", id); err != nil {
		return err
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: schedule name is required", ErrInvalid)
	}
	if err := schedule.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return d.store.CreateSchedule(ctx, id, name, schedule, d.now().UTC())
}

// OnCall answers who holds the pager for a schedule at an instant; zero means now.
func (d *Desk) OnCall(ctx context.Context, scheduleID string, at time.Time) (string, bool, error) {
	if err := validateSlug("schedule", scheduleID); err != nil {
		return "", false, err
	}
	schedule, err := d.store.Schedule(ctx, scheduleID)
	if err != nil {
		return "", false, err
	}
	if at.IsZero() {
		at = d.now()
	}
	responder, ok := schedule.OnCall(at)
	return responder, ok, nil
}

// CreatePolicy stores a validated escalation ladder.
func (d *Desk) CreatePolicy(ctx context.Context, policy EscalationPolicy) error {
	if err := validatePolicy(policy); err != nil {
		return err
	}
	policy.CreatedAt = d.now().UTC()
	return d.store.CreatePolicy(ctx, policy)
}

// CreateService binds a monitored system to a policy and mints its routing key once.
func (d *Desk) CreateService(ctx context.Context, service Service) (Service, error) {
	if err := validateService(service); err != nil {
		return Service{}, err
	}
	key, err := d.generate("")
	if err != nil {
		return Service{}, err
	}
	service.RoutingKey = key
	service.CreatedAt = d.now().UTC()
	if err := d.store.CreateService(ctx, service); err != nil {
		return Service{}, err
	}
	return service, nil
}

// Acknowledge takes ownership on behalf of the configured management principal.
func (d *Desk) Acknowledge(ctx context.Context, incidentID string) (Incident, error) {
	return d.manage(ctx, incidentID, SignalAcknowledge)
}

// Resolve closes an incident on behalf of the configured management principal.
func (d *Desk) Resolve(ctx context.Context, incidentID string) (Incident, error) {
	return d.manage(ctx, incidentID, SignalResolve)
}

func (d *Desk) manage(ctx context.Context, incidentID string, signal Signal) (Incident, error) {
	current, err := d.store.Incident(ctx, incidentID)
	if err != nil {
		return Incident{}, err
	}
	err = d.signal(ctx, current, signal, d.actor)
	if errors.Is(err, ErrConflict) && current.State != StateResolved {
		current, err = d.store.Incident(ctx, incidentID)
		if err != nil {
			return Incident{}, err
		}
		err = d.signal(ctx, current, signal, d.actor)
	}
	if err != nil {
		return Incident{}, err
	}
	return d.store.Incident(ctx, incidentID)
}
