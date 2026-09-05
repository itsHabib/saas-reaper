package tunnel

import "fmt"

// ClaimState is the durable half of a tunnel's status.
type ClaimState string

// Presence is the volatile half: whether an agent link is attached in this process.
type Presence string

// Event is a stimulus applied to a tunnel's status.
type Event string

// AuditKind names one append-only audit row.
type AuditKind string

// The complete state, presence, event, and audit vocabularies. They are public behavior.
const (
	ClaimActive  ClaimState = "active"
	ClaimRevoked ClaimState = "revoked"

	PresenceAbsent Presence = "absent"
	PresenceLive   Presence = "live"

	EventConnect  Event = "connect"
	EventLinkLost Event = "link-lost"
	EventRevoke   Event = "revoke"

	AuditClaimed      AuditKind = "claimed"
	AuditConnected    AuditKind = "connected"
	AuditSuperseded   AuditKind = "superseded"
	AuditDisconnected AuditKind = "disconnected"
	AuditRevoked      AuditKind = "revoked"
)

// Status is one tunnel's complete lifecycle position.
type Status struct {
	Claim    ClaimState
	Presence Presence
}

// Outcome is the result of applying one event: the next status, the audit rows that record
// the change in order, and the policy error when the event is refused. A refused event never
// changes status.
type Outcome struct {
	Status Status
	Audit  []AuditKind
	Err    error
}

// Transition is the single lifecycle table. Policy and every mechanism that reports a change
// call it, so the audit vocabulary and the reachable states cannot drift apart. It is total
// over every (status, event) pair; an unknown pair is a programming error and panics.
func Transition(current Status, event Event) Outcome {
	switch {
	case current.Claim == ClaimRevoked:
		return revokedTransition(current, event)
	case event == EventConnect && current.Presence == PresenceAbsent:
		return Outcome{Status: Status{ClaimActive, PresenceLive}, Audit: []AuditKind{AuditConnected}}
	case event == EventConnect:
		return Outcome{Status: Status{ClaimActive, PresenceLive}, Audit: []AuditKind{AuditSuperseded, AuditConnected}}
	case event == EventLinkLost && current.Presence == PresenceLive:
		return Outcome{Status: Status{ClaimActive, PresenceAbsent}, Audit: []AuditKind{AuditDisconnected}}
	case event == EventLinkLost:
		return Outcome{Status: current}
	case event == EventRevoke && current.Presence == PresenceLive:
		return Outcome{Status: Status{ClaimRevoked, PresenceAbsent}, Audit: []AuditKind{AuditDisconnected, AuditRevoked}}
	case event == EventRevoke:
		return Outcome{Status: Status{ClaimRevoked, PresenceAbsent}, Audit: []AuditKind{AuditRevoked}}
	}
	panic(fmt.Sprintf("tunnel: no transition for %+v on %q", current, event))
}

// revokedTransition keeps a revoked claim inert: nothing may attach, nothing is audited again,
// and a second revoke is a conflict rather than a duplicate audit row.
func revokedTransition(current Status, event Event) Outcome {
	if current.Presence == PresenceLive {
		panic(fmt.Sprintf("tunnel: revoked claim with a live link: %+v", current))
	}
	switch event {
	case EventConnect:
		return Outcome{Status: current, Err: fmt.Errorf("%w: claim is revoked", ErrRevoked)}
	case EventRevoke:
		return Outcome{Status: current, Err: fmt.Errorf("%w: claim is already revoked", ErrConflict)}
	case EventLinkLost:
		return Outcome{Status: current}
	}
	panic(fmt.Sprintf("tunnel: no transition for %+v on %q", current, event))
}

// Events lists every stimulus, for exhaustive walks.
func Events() []Event {
	return []Event{EventConnect, EventLinkLost, EventRevoke}
}
