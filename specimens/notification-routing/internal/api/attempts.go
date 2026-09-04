package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/notification-routing/internal/routing"
)

// attemptResponse deliberately omits the delivery address: the audit-read authority is lower
// than management and never sees recipient contact data.
type attemptResponse struct {
	Sequence       int64                  `json:"sequence"`
	DeliveryID     string                 `json:"deliveryId"`
	NotificationID string                 `json:"notificationId"`
	RecipientID    string                 `json:"recipientId"`
	ChannelID      string                 `json:"channelId"`
	Actor          string                 `json:"actor"`
	Number         int                    `json:"number"`
	Outcome        routing.AttemptOutcome `json:"outcome"`
	State          routing.DeliveryState  `json:"state"`
	Code           int                    `json:"code,omitempty"`
	Error          string                 `json:"error,omitempty"`
	AttemptedAt    time.Time              `json:"attemptedAt"`
	NextAttemptAt  *time.Time             `json:"nextAttemptAt,omitempty"`
}

func (s *Server) listAttempts(w http.ResponseWriter, r *http.Request) {
	limit, err := attemptLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be an integer between 1 and 1000"})
		return
	}
	filter := routing.AttemptFilter{
		NotificationID: r.URL.Query().Get("notificationId"),
		ChannelID:      r.URL.Query().Get("channelId"),
	}
	attempts, err := s.attempts.Attempts(r.Context(), filter, limit)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	views := make([]attemptResponse, 0, len(attempts))
	for _, attempt := range attempts {
		views = append(views, attemptView(attempt))
	}
	writeJSON(w, http.StatusOK, map[string]any{"attempts": views})
}

func attemptLimit(raw string) (int, error) {
	if raw == "" {
		return 100, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > 1000 {
		return 0, strconv.ErrSyntax
	}
	return limit, nil
}

func attemptView(attempt routing.Attempt) attemptResponse {
	view := attemptResponse{
		Sequence:       attempt.Sequence,
		DeliveryID:     attempt.DeliveryID,
		NotificationID: attempt.NotificationID,
		RecipientID:    attempt.RecipientID,
		ChannelID:      attempt.ChannelID,
		Actor:          attempt.Actor,
		Number:         attempt.Number,
		Outcome:        attempt.Outcome,
		State:          attempt.State,
		Code:           attempt.Code,
		Error:          attempt.Error,
		AttemptedAt:    attempt.AttemptedAt,
	}
	if !attempt.NextAttemptAt.IsZero() {
		next := attempt.NextAttemptAt
		view.NextAttemptAt = &next
	}
	return view
}
