package tunnel

import (
	"errors"
	"fmt"
	"testing"
)

// walk is the result of exhausting every (status, event) pair reachable from the initial status.
type walk struct {
	seen     map[Status]bool
	audits   map[AuditKind]int
	refusals map[string]error
	edges    int
}

func newWalk() *walk {
	return &walk{seen: map[Status]bool{}, audits: map[AuditKind]int{}, refusals: map[string]error{}}
}

// visit applies every event to current, records what happened, and returns the statuses that
// were reached. A refused event must leave the status untouched.
func (w *walk) visit(t *testing.T, current Status) []Status {
	t.Helper()
	if current.Claim == ClaimRevoked && current.Presence == PresenceLive {
		t.Fatalf("reached a revoked claim with a live link: %+v", current)
	}
	var next []Status
	for _, event := range Events() {
		outcome := Transition(current, event)
		w.edges++
		if outcome.Err != nil {
			w.refusals[fmt.Sprintf("%s/%s/%s", current.Claim, current.Presence, event)] = outcome.Err
			if outcome.Status != current {
				t.Fatalf("refused event %s changed status %+v to %+v", event, current, outcome.Status)
			}
			continue
		}
		for _, kind := range outcome.Audit {
			w.audits[kind]++
		}
		next = append(next, outcome.Status)
	}
	return next
}

// TestLifecycleStateSpaceExhaustiveWalk pins the reachable statuses of one tunnel under every
// event. A change to the table must change these counts deliberately. The walk also proves
// the table is total over the reachable space and that a revoked claim never carries a link.
func TestLifecycleStateSpaceExhaustiveWalk(t *testing.T) {
	w := newWalk()
	queue := []Status{{Claim: ClaimActive, Presence: PresenceAbsent}}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if w.seen[current] {
			continue
		}
		w.seen[current] = true
		queue = append(queue, w.visit(t, current)...)
	}
	const wantReachable = 3
	if len(w.seen) != wantReachable {
		t.Fatalf("reachable statuses = %d, want %d: %v", len(w.seen), wantReachable, w.seen)
	}
	if w.edges != wantReachable*len(Events()) {
		t.Fatalf("walked %d edges, want every (status, event) pair = %d", w.edges, wantReachable*len(Events()))
	}
	assertAudits(t, w.audits)
	assertRefusals(t, w.refusals)
}

func assertAudits(t *testing.T, audits map[AuditKind]int) {
	t.Helper()
	want := map[AuditKind]int{AuditConnected: 2, AuditSuperseded: 1, AuditDisconnected: 2, AuditRevoked: 2}
	for kind, count := range want {
		if audits[kind] != count {
			t.Fatalf("audit %s emitted on %d edges, want %d", kind, audits[kind], count)
		}
	}
	if audits[AuditClaimed] != 0 {
		t.Fatal("the lifecycle table must not emit the claim row; only creation does")
	}
}

func assertRefusals(t *testing.T, refusals map[string]error) {
	t.Helper()
	if len(refusals) != 2 {
		t.Fatalf("refused edges = %v, want exactly connect and revoke on a revoked claim", refusals)
	}
	if !errors.Is(refusals["revoked/absent/connect"], ErrRevoked) || !errors.Is(refusals["revoked/absent/revoke"], ErrConflict) {
		t.Fatalf("refusal classes are wrong: %v", refusals)
	}
}

func TestTransitionOrdersDisconnectBeforeRevoke(t *testing.T) {
	outcome := Transition(Status{ClaimActive, PresenceLive}, EventRevoke)
	if len(outcome.Audit) != 2 || outcome.Audit[0] != AuditDisconnected || outcome.Audit[1] != AuditRevoked {
		t.Fatalf("revoking a live tunnel audited %v, want disconnected then revoked", outcome.Audit)
	}
	outcome = Transition(Status{ClaimActive, PresenceLive}, EventConnect)
	if len(outcome.Audit) != 2 || outcome.Audit[0] != AuditSuperseded || outcome.Audit[1] != AuditConnected {
		t.Fatalf("reconnecting a live tunnel audited %v, want superseded then connected", outcome.Audit)
	}
}

func TestTransitionPanicsOnAnImpossibleStatus(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a revoked claim with a live link did not panic")
		}
	}()
	Transition(Status{ClaimRevoked, PresenceLive}, EventLinkLost)
}
