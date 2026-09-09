package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/webhook-delivery/internal/delivery"
)

type attemptResponse struct {
	Sequence         int64                   `json:"sequence"`
	DeliveryID       string                  `json:"deliveryId"`
	MessageID        string                  `json:"messageId"`
	EndpointID       string                  `json:"endpointId"`
	Actor            string                  `json:"actor"`
	Number           int                     `json:"number"`
	Outcome          delivery.AttemptOutcome `json:"outcome"`
	StatusCode       int                     `json:"statusCode,omitempty"`
	Error            string                  `json:"error,omitempty"`
	WebhookTimestamp int64                   `json:"webhookTimestamp"`
	AttemptedAt      time.Time               `json:"attemptedAt"`
	NextAttemptAt    *time.Time              `json:"nextAttemptAt,omitempty"`
}

func (s *Server) listAttempts(w http.ResponseWriter, r *http.Request) {
	limit, err := attemptLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	filter := delivery.AttemptFilter{
		MessageID:  r.URL.Query().Get("messageId"),
		EndpointID: r.URL.Query().Get("endpointId"),
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

func attemptView(attempt delivery.Attempt) attemptResponse {
	view := attemptResponse{
		Sequence:         attempt.Sequence,
		DeliveryID:       attempt.DeliveryID,
		MessageID:        attempt.MessageID,
		EndpointID:       attempt.EndpointID,
		Actor:            attempt.Actor,
		Number:           attempt.Number,
		Outcome:          attempt.Outcome,
		StatusCode:       attempt.StatusCode,
		Error:            attempt.Error,
		WebhookTimestamp: attempt.WebhookTimestamp,
		AttemptedAt:      attempt.AttemptedAt,
	}
	if !attempt.NextAttemptAt.IsZero() {
		next := attempt.NextAttemptAt
		view.NextAttemptAt = &next
	}
	return view
}
