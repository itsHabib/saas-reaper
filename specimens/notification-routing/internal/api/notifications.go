package api

import (
	"encoding/json"
	"net/http"

	"github.com/itsHabib/saas-reaper/specimens/notification-routing/internal/routing"
)

type sendRequest struct {
	Template       string          `json:"template"`
	Recipient      string          `json:"recipient"`
	Payload        json.RawMessage `json:"payload"`
	IdempotencyKey string          `json:"idempotencyKey"`
}

type queuedDeliveryResponse struct {
	ID      string `json:"id"`
	Channel string `json:"channel"`
}

type acceptanceResponse struct {
	NotificationID string                   `json:"notificationId"`
	Deduplicated   bool                     `json:"deduplicated"`
	Deliveries     []queuedDeliveryResponse `json:"deliveries"`
}

// sendNotification answers 202 for a new acceptance and 200 for a deduplicated re-send.
func (s *Server) sendNotification(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var request sendRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	acceptance, err := s.routing.Send(r.Context(), routing.SendRequest{
		TemplateKey:    request.Template,
		RecipientID:    request.Recipient,
		Payload:        request.Payload,
		IdempotencyKey: request.IdempotencyKey,
	})
	if err != nil {
		writePolicyError(w, err)
		return
	}
	status := http.StatusAccepted
	if acceptance.Deduplicated {
		status = http.StatusOK
	}
	view := acceptanceResponse{
		NotificationID: acceptance.NotificationID,
		Deduplicated:   acceptance.Deduplicated,
		Deliveries:     make([]queuedDeliveryResponse, 0, len(acceptance.Deliveries)),
	}
	for _, queued := range acceptance.Deliveries {
		view.Deliveries = append(view.Deliveries, queuedDeliveryResponse{ID: queued.ID, Channel: queued.ChannelID})
	}
	writeJSON(w, status, view)
}
