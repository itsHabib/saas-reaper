package routing

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestScheduleClassifiesEveryOutcome(t *testing.T) {
	schedule, err := NewSchedule([]time.Duration{time.Second})
	if err != nil {
		t.Fatal(err)
	}
	dispatch := Dispatch{DeliveryID: "del_1", NotificationID: "ntf_1", ChannelID: "email", Actor: "demo"}
	now := time.Unix(100, 0)
	later := now.Add(20 * time.Second)

	delivered := schedule.resolve(dispatch, Receipt{Code: 250}, nil, now, later)
	if delivered.Outcome != OutcomeDelivered || delivered.State != StateDelivered || delivered.Code != 250 {
		t.Fatalf("delivered = %#v", delivered)
	}
	if !delivered.NextAttemptAt.IsZero() {
		t.Fatalf("delivered scheduled a retry: %#v", delivered)
	}

	transient := schedule.resolve(dispatch, Receipt{Code: 451}, errors.New("try later"), now, later)
	if transient.Outcome != OutcomeRetrying || transient.State != StatePending {
		t.Fatalf("transient = %#v", transient)
	}
	if !transient.NextAttemptAt.Equal(later.Add(time.Second)) {
		t.Fatalf("next attempt = %s, want one second after completion", transient.NextAttemptAt)
	}

	permanent := schedule.resolve(dispatch, Receipt{Code: 550}, fmt.Errorf("%w: no such user", ErrPermanent), now, later)
	if permanent.Outcome != OutcomeRejected || permanent.State != StateFailed || permanent.Error != "permanent transport rejection: no such user" {
		t.Fatalf("permanent = %#v", permanent)
	}

	final := dispatch
	final.AttemptCount = 1
	exhausted := schedule.resolve(final, Receipt{}, errors.New("offline"), now, later)
	if exhausted.Outcome != OutcomeExhausted || exhausted.State != StateExhausted || exhausted.Number != 2 {
		t.Fatalf("exhausted = %#v", exhausted)
	}
}

func TestScheduleClampsCompletionBeforeAttempt(t *testing.T) {
	schedule, err := NewSchedule([]time.Duration{time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	attempt := schedule.resolve(Dispatch{DeliveryID: "del_1"}, Receipt{}, errors.New("x"), now, now.Add(-time.Hour))
	if !attempt.NextAttemptAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("next attempt = %s, want attempt time plus one minute", attempt.NextAttemptAt)
	}
}

func TestNewScheduleRejectsNonPositiveOrOversizedDelays(t *testing.T) {
	if _, err := NewSchedule([]time.Duration{0}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero delay error = %v, want invalid", err)
	}
	if _, err := NewSchedule(make([]time.Duration, 21)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized schedule error = %v, want invalid", err)
	}
}

func TestTransitionForRejectsUnknownOutcomes(t *testing.T) {
	if _, ok := TransitionFor(AttemptOutcome("skipped")); ok {
		t.Fatal("unknown outcome produced a transition")
	}
	retrying, ok := TransitionFor(OutcomeRetrying)
	if !ok || !retrying.RetryScheduled || retrying.State != StatePending {
		t.Fatalf("retrying transition = %#v", retrying)
	}
}
