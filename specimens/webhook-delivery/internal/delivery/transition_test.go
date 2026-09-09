package delivery

import (
	"errors"
	"testing"
	"time"
)

var everyOutcome = []AttemptOutcome{
	OutcomeDelivered,
	OutcomeRetrying,
	OutcomeExhausted,
	OutcomeEndpointDisabled,
	OutcomeFailed,
}

func TestEveryOutcomeOwnsExactlyOneTransition(t *testing.T) {
	if len(attemptTransitions) != len(everyOutcome) {
		t.Fatalf("transition table has %d rows for %d outcomes", len(attemptTransitions), len(everyOutcome))
	}
	for _, outcome := range everyOutcome {
		transition, known := outcome.Transition()
		if !known {
			t.Fatalf("outcome %s has no transition", outcome)
		}
		if transition.RetryScheduled != (transition.State == StatePending) {
			t.Fatalf("outcome %s schedules a retry outside the pending state: %#v", outcome, transition)
		}
		if transition.DisableEndpoint && transition.State != StateDisabled {
			t.Fatalf("outcome %s disables an endpoint without the disabled state: %#v", outcome, transition)
		}
	}
	if _, known := AttemptOutcome("mystery").Transition(); known {
		t.Fatal("unknown outcome reported a transition")
	}
}

func TestResolveHonoursTheExportedTransition(t *testing.T) {
	schedule, err := NewSchedule([]time.Duration{time.Second})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	inputs := []struct {
		attemptCount int
		result       SendResult
		err          error
	}{
		{result: SendResult{StatusCode: 204}},
		{result: SendResult{StatusCode: 500}},
		{attemptCount: 1, result: SendResult{StatusCode: 500}},
		{result: SendResult{StatusCode: 410}},
		{err: errors.Join(errPermanent, errors.New("bad secret"))},
	}
	reached := map[AttemptOutcome]bool{}
	for _, input := range inputs {
		dispatch := Dispatch{DeliveryID: "del_1", MessageID: "msg_1", EndpointID: "ep_1", AttemptCount: input.attemptCount}
		attempt := schedule.resolve(dispatch, input.result, input.err, now, now)
		transition, known := attempt.Outcome.Transition()
		if !known {
			t.Fatalf("resolve produced unknown outcome %q", attempt.Outcome)
		}
		if attempt.State != transition.State || attempt.DisableEndpoint != transition.DisableEndpoint {
			t.Fatalf("attempt %#v diverges from transition %#v", attempt, transition)
		}
		if !attempt.NextAttemptAt.IsZero() != transition.RetryScheduled {
			t.Fatalf("attempt %#v retry schedule diverges from transition %#v", attempt, transition)
		}
		reached[attempt.Outcome] = true
	}
	for _, outcome := range everyOutcome {
		if !reached[outcome] {
			t.Fatalf("resolve never produced outcome %s", outcome)
		}
	}
}

func TestScheduleTerminatesPermanentFailureWithoutRetry(t *testing.T) {
	schedule, err := NewSchedule(DefaultRetryDelays)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	dispatch := Dispatch{DeliveryID: "del_1", MessageID: "msg_1", EndpointID: "ep_1"}
	failed := schedule.resolve(dispatch, SendResult{}, errors.Join(errPermanent, errors.New("bad secret")), now, now)
	if failed.Outcome != OutcomeFailed || failed.State != StateFailed || !failed.NextAttemptAt.IsZero() {
		t.Fatalf("permanent failure = %#v, want terminal failed state", failed)
	}
	if failed.DisableEndpoint || failed.StatusCode != 0 || failed.Error == "" {
		t.Fatalf("permanent failure = %#v, want endpoint kept, no status, recorded error", failed)
	}
	transient := schedule.resolve(dispatch, SendResult{}, errors.New("connection refused"), now, now)
	if transient.Outcome != OutcomeRetrying {
		t.Fatalf("transient failure = %#v, want retrying", transient)
	}
}
