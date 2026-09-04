package incident

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/oncall"
)

// Store is the durable authority the pager desk consumes; interfaces live with the consumer.
type Store interface {
	CreateResponder(context.Context, Responder) error
	CreateSchedule(context.Context, string, string, oncall.Schedule, time.Time) error
	CreatePolicy(context.Context, EscalationPolicy) error
	CreateService(context.Context, Service) error
	Schedule(context.Context, string) (oncall.Schedule, error)
	ServiceByRoutingKey(context.Context, string) (Service, error)
	Targets(context.Context, string) (Targets, error)
	Incident(context.Context, string) (Incident, error)
	OpenIncident(context.Context, string, string) (Incident, error)
	CreateIncident(context.Context, Incident, Event, []Notification) error
	Transition(context.Context, Incident, int64, Event, []Notification) error
	DueEscalations(context.Context, time.Time, int) ([]Incident, error)
}

// Desk is the pager policy: it decides, the store persists, transports page.
type Desk struct {
	store    Store
	actor    string
	now      Clock
	generate IDGenerator
	secret   func() (string, error)
}

// NewDesk composes policy with its authority, principal, clock, and identity sources.
func NewDesk(store Store, actor string, now Clock, generate IDGenerator, secret func() (string, error)) (*Desk, error) {
	if store == nil || now == nil || generate == nil || secret == nil {
		return nil, fmt.Errorf("%w: store, clock, id generator, and secret generator are required", ErrInvalid)
	}
	if strings.TrimSpace(actor) == "" {
		return nil, fmt.Errorf("%w: a non-blank management actor is required", ErrInvalid)
	}
	return &Desk{store: store, actor: actor, now: now, generate: generate, secret: secret}, nil
}
