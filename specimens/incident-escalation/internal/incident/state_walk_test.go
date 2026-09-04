package incident

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// walkKey is the durable shape of an incident that the transition table can distinguish.
type walkKey struct {
	state  State
	level  int
	repeat int
	armed  bool
}

func keyOf(current Incident) walkKey {
	return walkKey{state: current.State, level: current.Level, repeat: current.Repeat, armed: !current.EscalateAt.IsZero()}
}

// TestLifecycleStateSpaceExhaustiveWalk drives every signal from every reachable
// durable state. The reachable count is pinned: a change to the transition table
// that adds or removes a state must update this number deliberately.
func TestLifecycleStateSpaceExhaustiveWalk(t *testing.T) {
	policy := twoLevelPolicy(1)
	start, now := openedIncident(t, policy)
	queue := []Incident{start}
	seen := map[walkKey]Incident{keyOf(start): start}
	signals := []Signal{SignalTrigger, SignalAcknowledge, SignalResolve, SignalTimeout}
	edges := 0
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, signal := range signals {
			next, err := walkStep(current, policy, signal, now)
			if err != nil {
				continue
			}
			edges++
			assertWalkInvariants(t, current, next, signal, policy)
			key := keyOf(next.Incident)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = next.Incident
			queue = append(queue, next.Incident)
		}
	}
	const pinnedReachableStates = 13
	if len(seen) != pinnedReachableStates {
		t.Fatalf("reachable durable states changed: got %d want %d: %s", len(seen), pinnedReachableStates, describe(seen))
	}
	counts := map[State]int{}
	for key := range seen {
		counts[key.state]++
	}
	if counts[StateTriggered] != 5 || counts[StateAcknowledged] != 4 || counts[StateResolved] != 4 {
		t.Fatalf("unexpected per-state counts %v", counts)
	}
	if edges == 0 {
		t.Fatal("walk produced no transitions")
	}
}

func walkStep(current Incident, policy EscalationPolicy, signal Signal, now time.Time) (Transition, error) {
	at := now.Add(time.Hour)
	if signal == SignalTimeout && !current.EscalateAt.IsZero() {
		at = current.EscalateAt
	}
	transition, err := Step(current, policy, signal, "walk", at)
	if errors.Is(err, ErrConflict) && current.State == StateResolved {
		return Transition{}, err
	}
	if err != nil {
		return Transition{}, err
	}
	return transition, nil
}

func assertWalkInvariants(t *testing.T, current Incident, next Transition, signal Signal, policy EscalationPolicy) {
	t.Helper()
	assertLadderInvariants(t, next, policy)
	assertRevisionInvariants(t, current, next)
	assertSignalInvariants(t, next, signal)
}

func assertLadderInvariants(t *testing.T, next Transition, policy EscalationPolicy) {
	t.Helper()
	moved := next.Incident
	if moved.Level < 0 || moved.Level >= len(policy.Levels) {
		t.Fatalf("walk left the ladder: %#v", moved)
	}
	if moved.Repeat < 0 || moved.Repeat > policy.Repeat {
		t.Fatalf("walk exceeded the repeat bound: %#v", moved)
	}
	if moved.State != StateTriggered && !moved.EscalateAt.IsZero() {
		t.Fatalf("only a triggered incident may hold an armed timer: %#v", moved)
	}
	if next.Notify && moved.State != StateTriggered {
		t.Fatalf("pages are planned only while triggered: %#v", next)
	}
}

func assertRevisionInvariants(t *testing.T, current Incident, next Transition) {
	t.Helper()
	if next.Changed && next.Incident.Revision != current.Revision+1 {
		t.Fatalf("a changed transition advances exactly one revision: %d -> %d", current.Revision, next.Incident.Revision)
	}
	if !next.Changed && next.Incident.Revision != current.Revision {
		t.Fatalf("an unchanged transition must not touch the revision: %#v", next)
	}
}

func assertSignalInvariants(t *testing.T, next Transition, signal Signal) {
	t.Helper()
	moved := next.Incident
	if signal == SignalAcknowledge && next.Changed && moved.State != StateAcknowledged {
		t.Fatalf("acknowledge must land in acknowledged: %#v", moved)
	}
	if signal == SignalResolve && moved.State != StateResolved {
		t.Fatalf("resolve must land in resolved: %#v", moved)
	}
	if signal == SignalTimeout && next.Changed &&
		next.Event.Kind != EventEscalated && next.Event.Kind != EventEscalationExhausted {
		t.Fatalf("timeout journals escalation or exhaustion only: %#v", next.Event)
	}
	if next.Event.Kind == EventEscalationExhausted && (next.Notify || !moved.EscalateAt.IsZero()) {
		t.Fatalf("exhaustion must disarm and stop paging: %#v", next)
	}
}

func describe(seen map[walkKey]Incident) string {
	var text strings.Builder
	for key := range seen {
		fmt.Fprintf(&text, " %s/L%d/R%d/armed=%t", key.state, key.level, key.repeat, key.armed)
	}
	return text.String()
}
