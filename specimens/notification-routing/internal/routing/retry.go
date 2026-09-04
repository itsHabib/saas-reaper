package routing

import (
	"errors"
	"fmt"
	"time"
)

const maxRecordedErrorBytes = 1024

// DefaultRetryDelays is the documented bounded schedule after the immediate attempt.
var DefaultRetryDelays = []time.Duration{
	10 * time.Second,
	time.Minute,
	5 * time.Minute,
	30 * time.Minute,
	2 * time.Hour,
	6 * time.Hour,
}

// Schedule converts one attempt result into the next persistent state.
type Schedule struct {
	delays []time.Duration
}

// NewSchedule validates a finite retry schedule.
func NewSchedule(delays []time.Duration) (Schedule, error) {
	if len(delays) > 20 {
		return Schedule{}, fmt.Errorf("%w: retry schedule accepts at most 20 delays", ErrInvalid)
	}
	copyOfDelays := append([]time.Duration(nil), delays...)
	for _, delay := range copyOfDelays {
		if delay <= 0 {
			return Schedule{}, fmt.Errorf("%w: retry delays must be positive", ErrInvalid)
		}
	}
	return Schedule{delays: copyOfDelays}, nil
}

// MaxAttempts returns the immediate attempt plus every configured retry.
func (s Schedule) MaxAttempts() int {
	return len(s.delays) + 1
}

// Receipt carries transport evidence into retry policy.
//
// It deliberately carries only the protocol's result code. Remote-controlled text — an SMTP
// reply message, a webhook response body — never crosses this seam, so redaction has exactly
// one boundary: the error a transport returns, which classify redacts before the dispatcher
// persists it. A future consumer of this struct cannot reintroduce the leak by reading a field.
type Receipt struct {
	Code int
}

// AttemptOutcome is the durable interpretation of one transport call.
type AttemptOutcome string

const (
	// OutcomeDelivered records transport acceptance.
	OutcomeDelivered AttemptOutcome = "delivered"
	// OutcomeRetrying records a transient failure with another bounded attempt scheduled.
	OutcomeRetrying AttemptOutcome = "retrying"
	// OutcomeRejected records a permanent transport rejection.
	OutcomeRejected AttemptOutcome = "rejected"
	// OutcomeExhausted records a transient failure after the final permitted attempt.
	OutcomeExhausted AttemptOutcome = "exhausted"
)

// Transition is the delivery-state consequence of one attempt outcome.
type Transition struct {
	State          DeliveryState
	RetryScheduled bool
}

// TransitionFor is the single outcome-to-state table shared by policy and persistence.
func TransitionFor(outcome AttemptOutcome) (Transition, bool) {
	switch outcome {
	case OutcomeDelivered:
		return Transition{State: StateDelivered}, true
	case OutcomeRetrying:
		return Transition{State: StatePending, RetryScheduled: true}, true
	case OutcomeRejected:
		return Transition{State: StateFailed}, true
	case OutcomeExhausted:
		return Transition{State: StateExhausted}, true
	default:
		return Transition{}, false
	}
}

// Attempt is the append-only audit row and the atomic delivery transition.
type Attempt struct {
	Sequence       int64
	DeliveryID     string
	NotificationID string
	RecipientID    string
	ChannelID      string
	Actor          string
	Number         int
	Outcome        AttemptOutcome
	Code           int
	Error          string
	AttemptedAt    time.Time
	NextAttemptAt  time.Time
	State          DeliveryState
}

// AttemptFilter narrows the append-only audit without changing its ordering.
type AttemptFilter struct {
	NotificationID string
	ChannelID      string
}

func (s Schedule) resolve(
	dispatch Dispatch,
	receipt Receipt,
	sendErr error,
	attemptedAt time.Time,
	completedAt time.Time,
) Attempt {
	number := dispatch.AttemptCount + 1
	attempt := Attempt{
		DeliveryID:     dispatch.DeliveryID,
		NotificationID: dispatch.NotificationID,
		RecipientID:    dispatch.RecipientID,
		ChannelID:      dispatch.ChannelID,
		Actor:          dispatch.Actor,
		Number:         number,
		Code:           receipt.Code,
		Error:          boundedError(sendErr),
		AttemptedAt:    attemptedAt.UTC(),
	}
	if sendErr == nil {
		return attempt.conclude(OutcomeDelivered)
	}
	if errors.Is(sendErr, ErrPermanent) {
		return attempt.conclude(OutcomeRejected)
	}
	if number > len(s.delays) {
		return attempt.conclude(OutcomeExhausted)
	}
	completion := completedAt.UTC()
	if completion.Before(attemptedAt) {
		completion = attemptedAt.UTC()
	}
	attempt.NextAttemptAt = completion.Add(s.delays[number-1])
	return attempt.conclude(OutcomeRetrying)
}

func (a Attempt) conclude(outcome AttemptOutcome) Attempt {
	transition, _ := TransitionFor(outcome)
	a.Outcome = outcome
	a.State = transition.State
	return a
}

func boundedError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) <= maxRecordedErrorBytes {
		return message
	}
	return message[:maxRecordedErrorBytes]
}
