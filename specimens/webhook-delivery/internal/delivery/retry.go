package delivery

import (
	"fmt"
	"net/http"
	"time"
)

const maxRecordedErrorBytes = 1024

// DefaultRetryDelays is the documented bounded schedule after the immediate attempt.
var DefaultRetryDelays = []time.Duration{
	5 * time.Second,
	5 * time.Minute,
	30 * time.Minute,
	2 * time.Hour,
	5 * time.Hour,
	10 * time.Hour,
	14 * time.Hour,
	20 * time.Hour,
	24 * time.Hour,
}

// Schedule converts one attempt result into the next persistent state.
type Schedule struct {
	delays        []time.Duration
	maxRetryAfter time.Duration
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
	return Schedule{delays: copyOfDelays, maxRetryAfter: 24 * time.Hour}, nil
}

// MaxAttempts returns the immediate attempt plus every configured retry.
func (s Schedule) MaxAttempts() int {
	return len(s.delays) + 1
}

// SendResult carries HTTP mechanism evidence into retry policy.
type SendResult struct {
	StatusCode int
	RetryAfter time.Duration
}

// AttemptOutcome is the durable interpretation of one outbound call.
type AttemptOutcome string

const (
	// OutcomeDelivered records a successful 2xx response.
	OutcomeDelivered AttemptOutcome = "delivered"
	// OutcomeRetrying records a failure with another bounded attempt scheduled.
	OutcomeRetrying AttemptOutcome = "retrying"
	// OutcomeExhausted records a failure after the final permitted attempt.
	OutcomeExhausted AttemptOutcome = "exhausted"
	// OutcomeEndpointDisabled records a 410 response and disables the endpoint.
	OutcomeEndpointDisabled AttemptOutcome = "endpoint_disabled"
)

// Attempt is the append-only audit row and the atomic delivery transition.
type Attempt struct {
	Sequence         int64
	DeliveryID       string
	MessageID        string
	EndpointID       string
	Actor            string
	Number           int
	Outcome          AttemptOutcome
	StatusCode       int
	Error            string
	WebhookTimestamp int64
	AttemptedAt      time.Time
	NextAttemptAt    time.Time
	State            DeliveryState
	DisableEndpoint  bool
}

// AttemptFilter narrows the append-only audit without changing its ordering.
type AttemptFilter struct {
	MessageID  string
	EndpointID string
}

func (s Schedule) resolve(dispatch Dispatch, result SendResult, sendErr error, attemptedAt time.Time) Attempt {
	number := dispatch.AttemptCount + 1
	attempt := Attempt{
		DeliveryID:       dispatch.DeliveryID,
		MessageID:        dispatch.MessageID,
		EndpointID:       dispatch.EndpointID,
		Actor:            dispatch.Actor,
		Number:           number,
		StatusCode:       result.StatusCode,
		Error:            boundedError(sendErr),
		WebhookTimestamp: attemptedAt.Unix(),
		AttemptedAt:      attemptedAt.UTC(),
	}
	if sendErr == nil && result.StatusCode >= 200 && result.StatusCode <= 299 {
		attempt.Outcome = OutcomeDelivered
		attempt.State = StateSucceeded
		return attempt
	}
	if sendErr == nil && result.StatusCode == http.StatusGone {
		attempt.Outcome = OutcomeEndpointDisabled
		attempt.State = StateDisabled
		attempt.DisableEndpoint = true
		return attempt
	}
	if number > len(s.delays) {
		attempt.Outcome = OutcomeExhausted
		attempt.State = StateExhausted
		return attempt
	}
	delay := s.delays[number-1]
	retryAfter := min(result.RetryAfter, s.maxRetryAfter)
	if retryAfter > delay {
		delay = retryAfter
	}
	attempt.Outcome = OutcomeRetrying
	attempt.State = StatePending
	attempt.NextAttemptAt = attemptedAt.Add(delay).UTC()
	return attempt
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
