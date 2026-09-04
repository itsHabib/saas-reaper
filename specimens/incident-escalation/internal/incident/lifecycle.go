package incident

import (
	"fmt"
	"time"
)

// State is the durable incident lifecycle position.
type State string

const (
	// StateTriggered means the incident is open and nobody has taken ownership.
	StateTriggered State = "triggered"
	// StateAcknowledged means a responder owns the incident; escalation is stopped.
	StateAcknowledged State = "acknowledged"
	// StateResolved is terminal; a later trigger with the same dedup key opens a new incident.
	StateResolved State = "resolved"
)

// Severity is the Events API v2 severity vocabulary.
type Severity string

const (
	// SeverityCritical is the highest Events API v2 severity.
	SeverityCritical Severity = "critical"
	// SeverityError is the default severity Alertmanager sends.
	SeverityError Severity = "error"
	// SeverityWarning is a non-urgent Events API v2 severity.
	SeverityWarning Severity = "warning"
	// SeverityInfo is the lowest Events API v2 severity.
	SeverityInfo Severity = "info"
)

// Incident is the durable record, including its escalation timer.
type Incident struct {
	ID         string
	ServiceID  string
	DedupKey   string
	State      State
	Summary    string
	Source     string
	Severity   Severity
	Client     string
	PolicyID   string
	Level      int
	Repeat     int
	EscalateAt time.Time
	Revision   int64
	OpenedAt   time.Time
	UpdatedAt  time.Time
}

// IncidentFilter narrows an incident listing without changing its ordering.
type IncidentFilter struct {
	ServiceID string
	State     State
}

// EventKind classifies one append-only incident journal row.
type EventKind string

const (
	// EventOpened records incident creation and the first page.
	EventOpened EventKind = "opened"
	// EventRetriggered records a duplicate trigger folded into an open incident.
	EventRetriggered EventKind = "retriggered"
	// EventAcknowledged records ownership and the end of escalation.
	EventAcknowledged EventKind = "acknowledged"
	// EventResolved records the terminal transition.
	EventResolved EventKind = "resolved"
	// EventEscalated records the timer firing and the next level being paged.
	EventEscalated EventKind = "escalated"
	// EventEscalationExhausted records the timer firing with no level left to page.
	EventEscalationExhausted EventKind = "escalation_exhausted"
)

// Event is one append-only journal row describing an incident transition.
type Event struct {
	Sequence   int64
	IncidentID string
	Kind       EventKind
	Actor      string
	Level      int
	Repeat     int
	Detail     string
	At         time.Time
}

// Signal is an input to the lifecycle transition function.
type Signal string

const (
	// SignalTrigger is an Events API v2 trigger against an existing incident.
	SignalTrigger Signal = "trigger"
	// SignalAcknowledge takes ownership; it may arrive from ingest or management.
	SignalAcknowledge Signal = "acknowledge"
	// SignalResolve closes the incident; it may arrive from ingest or management.
	SignalResolve Signal = "resolve"
	// SignalTimeout is the durable escalation timer reaching its due instant.
	SignalTimeout Signal = "timeout"
)

// Transition is the complete result of one lifecycle step.
type Transition struct {
	Incident Incident
	Event    Event
	Changed  bool
	Notify   bool
}

// Open creates a triggered incident at level zero with its first timer armed.
func Open(id string, service Service, policy EscalationPolicy, alert Alert, actor string, now time.Time) Transition {
	now = now.UTC()
	opened := Incident{
		ID:         id,
		ServiceID:  service.ID,
		DedupKey:   alert.DedupKey,
		State:      StateTriggered,
		Summary:    alert.Summary,
		Source:     alert.Source,
		Severity:   alert.Severity,
		Client:     alert.Client,
		PolicyID:   policy.ID,
		EscalateAt: now.Add(time.Duration(policy.Levels[0].Timeout)),
		Revision:   1,
		OpenedAt:   now,
		UpdatedAt:  now,
	}
	return Transition{
		Incident: opened,
		Event:    journal(opened, EventOpened, actor, now, ""),
		Changed:  true,
		Notify:   true,
	}
}

// Step is the single lifecycle transition table shared by ingest, management, and the timer.
//
// A resolved incident accepts no signal. Trigger against an open incident is a
// journaled duplicate. Acknowledge disarms the timer. Timeout climbs one level,
// then loops while repeats remain, then journals exhaustion and disarms.
func Step(current Incident, policy EscalationPolicy, signal Signal, actor string, now time.Time) (Transition, error) {
	if current.State == StateResolved {
		return Transition{}, fmt.Errorf("%w: incident %s is resolved", ErrConflict, current.ID)
	}
	if policy.ID != current.PolicyID || len(policy.Levels) == 0 {
		return Transition{}, fmt.Errorf("%w: incident %s does not follow policy %q", ErrInvalid, current.ID, policy.ID)
	}
	now = now.UTC()
	switch signal {
	case SignalTrigger:
		return advance(current, EventRetriggered, actor, now, false), nil
	case SignalAcknowledge:
		return acknowledge(current, actor, now), nil
	case SignalResolve:
		current.State = StateResolved
		current.EscalateAt = time.Time{}
		return advance(current, EventResolved, actor, now, false), nil
	case SignalTimeout:
		return timeout(current, policy, actor, now), nil
	}
	return Transition{}, fmt.Errorf("%w: unknown lifecycle signal %q", ErrInvalid, signal)
}

func acknowledge(current Incident, actor string, now time.Time) Transition {
	if current.State == StateAcknowledged {
		return Transition{Incident: current}
	}
	current.State = StateAcknowledged
	current.EscalateAt = time.Time{}
	return advance(current, EventAcknowledged, actor, now, false)
}

func timeout(current Incident, policy EscalationPolicy, actor string, now time.Time) Transition {
	if current.State != StateTriggered || current.EscalateAt.IsZero() || now.Before(current.EscalateAt) {
		return Transition{Incident: current}
	}
	if current.Level+1 < len(policy.Levels) {
		current.Level++
		current.EscalateAt = now.Add(time.Duration(policy.Levels[current.Level].Timeout))
		return advance(current, EventEscalated, actor, now, true)
	}
	if current.Repeat < policy.Repeat {
		current.Repeat++
		current.Level = 0
		current.EscalateAt = now.Add(time.Duration(policy.Levels[0].Timeout))
		transition := advance(current, EventEscalated, actor, now, true)
		transition.Event.Detail = fmt.Sprintf("repeat %d of %d", current.Repeat, policy.Repeat)
		return transition
	}
	current.EscalateAt = time.Time{}
	return advance(current, EventEscalationExhausted, actor, now, false)
}

func advance(next Incident, kind EventKind, actor string, now time.Time, notify bool) Transition {
	next.Revision++
	next.UpdatedAt = now
	detail := ""
	return Transition{
		Incident: next,
		Event:    journal(next, kind, actor, now, detail),
		Changed:  true,
		Notify:   notify,
	}
}

func journal(incident Incident, kind EventKind, actor string, now time.Time, detail string) Event {
	return Event{
		IncidentID: incident.ID,
		Kind:       kind,
		Actor:      actor,
		Level:      incident.Level,
		Repeat:     incident.Repeat,
		Detail:     detail,
		At:         now,
	}
}
