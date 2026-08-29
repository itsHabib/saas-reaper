package delivery

import (
	"errors"
	"testing"
	"time"
)

func TestScheduleUsesLaterBoundedRetryAfter(t *testing.T) {
	schedule, err := NewSchedule([]time.Duration{time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	dispatch := Dispatch{DeliveryID: "del_1", MessageID: "msg_1", EndpointID: "ep_1", Actor: "demo"}
	attempt := schedule.resolve(dispatch, SendResult{StatusCode: 429, RetryAfter: 2 * time.Hour}, nil, now)
	if attempt.Outcome != OutcomeRetrying || !attempt.NextAttemptAt.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("attempt = %#v", attempt)
	}
	tooLong := schedule.resolve(dispatch, SendResult{StatusCode: 503, RetryAfter: 7 * 24 * time.Hour}, nil, now)
	if !tooLong.NextAttemptAt.Equal(now.Add(24 * time.Hour)) {
		t.Fatalf("capped next attempt = %s", tooLong.NextAttemptAt)
	}
}

func TestScheduleClassifiesSuccessFailureAndGone(t *testing.T) {
	schedule, err := NewSchedule([]time.Duration{time.Second})
	if err != nil {
		t.Fatal(err)
	}
	dispatch := Dispatch{DeliveryID: "del_1", MessageID: "msg_1", EndpointID: "ep_1"}
	now := time.Unix(100, 0)
	success := schedule.resolve(dispatch, SendResult{StatusCode: 204}, nil, now)
	if success.State != StateSucceeded || success.Outcome != OutcomeDelivered {
		t.Fatalf("success = %#v", success)
	}
	gone := schedule.resolve(dispatch, SendResult{StatusCode: 410}, nil, now)
	if gone.State != StateDisabled || !gone.DisableEndpoint {
		t.Fatalf("gone = %#v", gone)
	}
	exhaustedDispatch := dispatch
	exhaustedDispatch.AttemptCount = 1
	exhausted := schedule.resolve(exhaustedDispatch, SendResult{}, errors.New("offline"), now)
	if exhausted.State != StateExhausted || exhausted.Outcome != OutcomeExhausted {
		t.Fatalf("exhausted = %#v", exhausted)
	}
}
