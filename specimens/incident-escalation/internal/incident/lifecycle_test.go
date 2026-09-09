package incident

import (
	"errors"
	"testing"
	"time"
)

func openedIncident(t *testing.T, policy EscalationPolicy) (Incident, time.Time) {
	t.Helper()
	now := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	service := Service{ID: "checkout", PolicyID: policy.ID}
	transition := Open("inc_1", service, policy, triggerAlert("k"), ServiceActor(service), now)
	if !transition.Notify || transition.Event.Kind != EventOpened || transition.Incident.Revision != 1 {
		t.Fatalf("unexpected open transition %#v", transition)
	}
	if !transition.Incident.EscalateAt.Equal(now.Add(30 * time.Second)) {
		t.Fatalf("first timer should be opened + level 0 timeout, got %s", transition.Incident.EscalateAt)
	}
	return transition.Incident, now
}

func TestTimeoutDoesNotFireBeforeItsInstant(t *testing.T) {
	policy := twoLevelPolicy(1)
	current, now := openedIncident(t, policy)
	early, err := Step(current, policy, SignalTimeout, TimerActor, now.Add(29*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if early.Changed {
		t.Fatalf("the timer must not fire early: %#v", early)
	}
}

func TestTimeoutClimbsToTheNextLevel(t *testing.T) {
	policy := twoLevelPolicy(1)
	current, now := openedIncident(t, policy)
	first, err := Step(current, policy, SignalTimeout, TimerActor, now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !first.Changed || !first.Notify {
		t.Fatalf("escalation must page the next level: %#v", first)
	}
	if first.Incident.Level != 1 || first.Event.Kind != EventEscalated {
		t.Fatalf("expected level 1 escalation, got %#v", first)
	}
	if !first.Incident.EscalateAt.Equal(now.Add(75 * time.Second)) {
		t.Fatalf("next timer should be fire time + level 1 timeout, got %s", first.Incident.EscalateAt)
	}
}

func TestTimeoutRepeatsTheLadderThenExhausts(t *testing.T) {
	policy := twoLevelPolicy(1)
	current, now := openedIncident(t, policy)
	first, err := Step(current, policy, SignalTimeout, TimerActor, now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Step(first.Incident, policy, SignalTimeout, TimerActor, first.Incident.EscalateAt)
	if err != nil {
		t.Fatal(err)
	}
	if second.Incident.Level != 0 || second.Incident.Repeat != 1 || !second.Notify {
		t.Fatalf("expected a repeat loop back to level 0, got %#v", second)
	}
	if second.Event.Detail != "repeat 1 of 1" {
		t.Fatalf("the journal must record the repeat, got %q", second.Event.Detail)
	}
	third, err := Step(second.Incident, policy, SignalTimeout, TimerActor, second.Incident.EscalateAt)
	if err != nil {
		t.Fatal(err)
	}
	if third.Incident.Level != 1 || third.Incident.Repeat != 1 {
		t.Fatalf("expected the final level, got %#v", third)
	}
	last, err := Step(third.Incident, policy, SignalTimeout, TimerActor, third.Incident.EscalateAt)
	if err != nil {
		t.Fatal(err)
	}
	if last.Notify || last.Event.Kind != EventEscalationExhausted || !last.Incident.EscalateAt.IsZero() {
		t.Fatalf("expected exhaustion with the timer disarmed, got %#v", last)
	}
	if last.Incident.State != StateTriggered {
		t.Fatalf("exhaustion leaves the incident open, got %s", last.Incident.State)
	}
}

func TestAcknowledgeDisarmsTimerAndIsIdempotent(t *testing.T) {
	policy := twoLevelPolicy(0)
	current, now := openedIncident(t, policy)
	acked, err := Step(current, policy, SignalAcknowledge, "operator", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if acked.Incident.State != StateAcknowledged || !acked.Incident.EscalateAt.IsZero() || acked.Notify {
		t.Fatalf("acknowledge must disarm without paging, got %#v", acked)
	}
	again, err := Step(acked.Incident, policy, SignalAcknowledge, "operator", now.Add(2*time.Second))
	if err != nil || again.Changed {
		t.Fatalf("second acknowledge must be a no-op: %#v %v", again, err)
	}
	fired, err := Step(acked.Incident, policy, SignalTimeout, TimerActor, now.Add(time.Hour))
	if err != nil || fired.Changed {
		t.Fatalf("timeout after acknowledge must be ignored: %#v %v", fired, err)
	}
	dup, err := Step(acked.Incident, policy, SignalTrigger, "service:checkout", now.Add(3*time.Second))
	if err != nil || dup.Event.Kind != EventRetriggered || dup.Incident.State != StateAcknowledged || dup.Notify {
		t.Fatalf("trigger on acknowledged incident is journaled only: %#v %v", dup, err)
	}
}

func TestResolveIsTerminal(t *testing.T) {
	policy := twoLevelPolicy(0)
	current, now := openedIncident(t, policy)
	resolved, err := Step(current, policy, SignalResolve, "service:checkout", now)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Incident.State != StateResolved || !resolved.Incident.EscalateAt.IsZero() || resolved.Incident.Revision != 2 {
		t.Fatalf("unexpected resolve %#v", resolved)
	}
	for _, signal := range []Signal{SignalTrigger, SignalAcknowledge, SignalResolve, SignalTimeout} {
		if _, err := Step(resolved.Incident, policy, signal, "x", now); !errors.Is(err, ErrConflict) {
			t.Fatalf("%s on resolved incident should conflict, got %v", signal, err)
		}
	}
}

func TestStepRejectsForeignPolicyAndUnknownSignal(t *testing.T) {
	policy := twoLevelPolicy(0)
	current, now := openedIncident(t, policy)
	other := policy
	other.ID = "other"
	if _, err := Step(current, other, SignalTimeout, TimerActor, now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for a foreign policy, got %v", err)
	}
	if _, err := Step(current, policy, Signal("nudge"), TimerActor, now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for an unknown signal, got %v", err)
	}
}
