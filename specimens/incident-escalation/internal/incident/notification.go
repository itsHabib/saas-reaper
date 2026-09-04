package incident

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const maxRecordedErrorBytes = 1024

// DefaultNotifyRetryDelays is the documented bounded schedule after the immediate attempt.
var DefaultNotifyRetryDelays = []time.Duration{
	10 * time.Second,
	30 * time.Second,
	time.Minute,
	5 * time.Minute,
}

// NotificationState is the durable position of one page.
type NotificationState string

const (
	// NotificationPending means another attempt is due or in flight.
	NotificationPending NotificationState = "pending"
	// NotificationDelivered means the transport accepted the page.
	NotificationDelivered NotificationState = "delivered"
	// NotificationExhausted means the bounded schedule ran out.
	NotificationExhausted NotificationState = "exhausted"
	// NotificationFailed means the transport reported a permanent failure.
	NotificationFailed NotificationState = "failed"
)

// Notification is one page owed to one responder on one channel.
type Notification struct {
	ID            string
	IncidentID    string
	ResponderID   string
	Channel       Channel
	Level         int
	Repeat        int
	State         NotificationState
	AttemptCount  int
	NextAttemptAt time.Time
	CreatedAt     time.Time
}

// AttemptOutcome is the durable interpretation of one transport call.
type AttemptOutcome string

const (
	// OutcomeDelivered records transport acceptance.
	OutcomeDelivered AttemptOutcome = "delivered"
	// OutcomeRetrying records a failure with another bounded attempt scheduled.
	OutcomeRetrying AttemptOutcome = "retrying"
	// OutcomeExhausted records a failure after the final permitted attempt.
	OutcomeExhausted AttemptOutcome = "exhausted"
	// OutcomeFailed records a permanent transport failure.
	OutcomeFailed AttemptOutcome = "failed"
)

// Attempt is the append-only audit row and the atomic notification transition.
type Attempt struct {
	Sequence       int64
	NotificationID string
	IncidentID     string
	ResponderID    string
	Channel        Channel
	Number         int
	Outcome        AttemptOutcome
	Error          string
	AttemptedAt    time.Time
	NextAttemptAt  time.Time
	State          NotificationState
}

// AttemptFilter narrows the append-only page audit without changing its ordering.
type AttemptFilter struct {
	IncidentID     string
	NotificationID string
}

// Message is the complete, private input one transport needs to page a responder.
type Message struct {
	NotificationID string
	Kind           EventKind
	Responder      Responder
	Incident       Incident
	ServiceName    string
	SentAt         time.Time
}

// Notifier is the policy-owned seam every outbound page crosses.
//
// A nil error is delivery. An error wrapping ErrPermanent ends the page without
// retry. Any other error is retried on the bounded schedule.
type Notifier interface {
	Notify(context.Context, Message) error
}

// RetrySchedule converts one attempt result into the next persistent state.
type RetrySchedule struct {
	delays []time.Duration
}

// NewRetrySchedule validates a finite retry schedule.
func NewRetrySchedule(delays []time.Duration) (RetrySchedule, error) {
	if len(delays) > 20 {
		return RetrySchedule{}, fmt.Errorf("%w: retry schedule accepts at most 20 delays", ErrInvalid)
	}
	copyOfDelays := append([]time.Duration(nil), delays...)
	for _, delay := range copyOfDelays {
		if delay <= 0 {
			return RetrySchedule{}, fmt.Errorf("%w: retry delays must be positive", ErrInvalid)
		}
	}
	return RetrySchedule{delays: copyOfDelays}, nil
}

// MaxAttempts returns the immediate attempt plus every configured retry.
func (s RetrySchedule) MaxAttempts() int {
	return len(s.delays) + 1
}

// Resolve turns one transport outcome into the append-only attempt and its transition.
func (s RetrySchedule) Resolve(notification Notification, sendErr error, attemptedAt, completedAt time.Time) Attempt {
	number := notification.AttemptCount + 1
	attempt := Attempt{
		NotificationID: notification.ID,
		IncidentID:     notification.IncidentID,
		ResponderID:    notification.ResponderID,
		Channel:        notification.Channel,
		Number:         number,
		Error:          boundedError(sendErr),
		AttemptedAt:    attemptedAt.UTC(),
	}
	if sendErr == nil {
		attempt.Outcome = OutcomeDelivered
		attempt.State = NotificationDelivered
		return attempt
	}
	if errors.Is(sendErr, ErrPermanent) {
		attempt.Outcome = OutcomeFailed
		attempt.State = NotificationFailed
		return attempt
	}
	if number > len(s.delays) {
		attempt.Outcome = OutcomeExhausted
		attempt.State = NotificationExhausted
		return attempt
	}
	completion := completedAt.UTC()
	if completion.Before(attemptedAt) {
		completion = attemptedAt.UTC()
	}
	attempt.Outcome = OutcomeRetrying
	attempt.State = NotificationPending
	attempt.NextAttemptAt = completion.Add(s.delays[number-1])
	return attempt
}

func boundedError(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	if len(text) <= maxRecordedErrorBytes {
		return text
	}
	return text[:maxRecordedErrorBytes]
}
