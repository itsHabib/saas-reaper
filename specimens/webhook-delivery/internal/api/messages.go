package api

import (
	"io"
	"net/http"

	"github.com/itsHabib/saas-reaper/specimens/webhook-delivery/internal/delivery"
)

type publicationResponse struct {
	MessageID   string   `json:"messageId"`
	DeliveryIDs []string `json:"deliveryIds"`
}

func (s *Server) publishMessage(w http.ResponseWriter, r *http.Request) {
	if err := requireJSON(w, r); err != nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, delivery.MaxPayloadBytes)
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read payload: " + err.Error()})
		return
	}
	publication, err := s.delivery.Publish(r.Context(), payload)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, publicationView(publication))
}

func (s *Server) replayMessage(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1)
	body, err := io.ReadAll(r.Body)
	if err != nil || len(body) != 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "replay request body must be empty"})
		return
	}
	publication, err := s.delivery.Replay(
		r.Context(),
		r.PathValue("message"),
		r.PathValue("endpoint"),
	)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, publicationView(publication))
}

func publicationView(publication delivery.Publication) publicationResponse {
	return publicationResponse{MessageID: publication.MessageID, DeliveryIDs: publication.DeliveryIDs}
}
