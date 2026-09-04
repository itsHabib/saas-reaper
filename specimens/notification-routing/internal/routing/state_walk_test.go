package routing

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

// walkEvents are every stimulus a pending delivery can receive: a transport result of each
// class, or the channel being disabled underneath it.
type walkEvent struct {
	name    string
	receipt Receipt
	err     error
	disable bool
}

var walkEvents = []walkEvent{
	{name: "accepted", receipt: Receipt{Code: 250}},
	{name: "transient", receipt: Receipt{Code: 451}, err: errors.New("try later")},
	{name: "permanent", receipt: Receipt{Code: 550}, err: fmt.Errorf("%w: unknown mailbox", ErrPermanent)},
	{name: "network", err: errors.New("connection refused")},
	{name: "channel disabled", disable: true},
}

// TestDeliveryStateSpaceExhaustiveWalk pins the reachable durable states of one delivery under a
// three-attempt schedule. A change to the schedule shape, the outcome table, or the cancel rule
// must change this count deliberately.
func TestDeliveryStateSpaceExhaustiveWalk(t *testing.T) {
	schedule, err := NewSchedule([]time.Duration{time.Second, 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	queue := []walkState{{state: StatePending, enabled: true}}
	seen := map[string]bool{}
	terminal := map[DeliveryState]int{}
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
			terminal[current.state]++
			continue
		}
		if !current.enabled {
			t.Fatalf("disabled channel left a delivery pending: %#v", current)
		}
		queue = append(queue, walkTransitions(schedule, current)...)
	}
	const wantReachable = 13
	if len(seen) != wantReachable {
		t.Fatalf("reachable states = %d, want %d: %v", len(seen), wantReachable, seen)
	}
	want := map[DeliveryState]int{StateDelivered: 3, StateFailed: 3, StateExhausted: 1, StateCanceled: 3}
	for state, count := range want {
		if terminal[state] != count {
			t.Fatalf("terminal %s reached %d ways, want %d", state, terminal[state], count)
		}
	}
}

func walkTransitions(schedule Schedule, current walkState) []walkState {
	dispatch := Dispatch{DeliveryID: "del_walk", NotificationID: "ntf_walk", ChannelID: "email", AttemptCount: current.attempts}
	now := time.Unix(int64(current.attempts+1), 0)
	next := make([]walkState, 0, len(walkEvents))
	for _, event := range walkEvents {
		if event.disable {
			next = append(next, walkState{state: StateCanceled, attempts: current.attempts, audit: current.audit, enabled: false})
			continue
		}
		attempt := schedule.resolve(dispatch, event.receipt, event.err, now, now)
		transition, ok := TransitionFor(attempt.Outcome)
		if !ok || transition.State != attempt.State || transition.RetryScheduled != !attempt.NextAttemptAt.IsZero() {
			panic(fmt.Sprintf("policy transition disagrees with resolved attempt: %#v", attempt))
		}
		next = append(next, walkState{state: attempt.State, attempts: attempt.Number, audit: current.audit + 1, enabled: true})
	}
	return next
}
