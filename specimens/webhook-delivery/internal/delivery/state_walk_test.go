package delivery

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

type walkState struct {
	state    DeliveryState
	attempts int
	audit    int
	enabled  bool
}

func TestRetryStateSpaceExhaustiveWalk(t *testing.T) {
	schedule, err := NewSchedule([]time.Duration{time.Second, 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	queue := []walkState{{state: StatePending, enabled: true}}
	seen := map[string]bool{}
	terminal := map[DeliveryState]bool{}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		key := fmt.Sprintf("%s/%d/%d/%t", current.state, current.attempts, current.audit, current.enabled)
		if seen[key] {
			continue
		}
		seen[key] = true
		if current.attempts > schedule.MaxAttempts() || current.audit != current.attempts {
			t.Fatalf("invalid durable state: %#v", current)
		}
		if current.state != StatePending {
			terminal[current.state] = true
			continue
		}
		if !current.enabled {
			t.Fatalf("disabled endpoint remained pending: %#v", current)
		}
		queue = append(queue, walkTransitions(schedule, current)...)
	}
	for _, expected := range []DeliveryState{StateSucceeded, StateExhausted, StateDisabled} {
		if !terminal[expected] {
			t.Fatalf("state walk never reached %s", expected)
		}
	}
}

func walkTransitions(schedule Schedule, current walkState) []walkState {
	dispatch := Dispatch{
		DeliveryID:   "del_walk",
		MessageID:    "msg_walk",
		EndpointID:   "ep_walk",
		AttemptCount: current.attempts,
	}
	now := time.Unix(int64(current.attempts+1), 0)
	results := []struct {
		result SendResult
		err    error
	}{
		{result: SendResult{StatusCode: 204}},
		{result: SendResult{StatusCode: 500}},
		{result: SendResult{StatusCode: 410}},
		{err: errors.New("network failure")},
	}
	next := make([]walkState, 0, len(results))
	for _, result := range results {
		attempt := schedule.resolve(dispatch, result.result, result.err, now, now)
		next = append(next, walkState{
			state:    attempt.State,
			attempts: attempt.Number,
			audit:    current.audit + 1,
			enabled:  !attempt.DisableEndpoint,
		})
	}
	return next
}
