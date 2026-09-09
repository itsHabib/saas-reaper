package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/incident"
)

// maxEventBytes mirrors the upstream Events API v2 event size ceiling.
const maxEventBytes = 512000

// enqueueRequest is the PagerDuty Events API v2 wire shape. Unknown fields are
// accepted and ignored so real senders such as Alertmanager are never rejected
// for the optional fields they add.
type enqueueRequest struct {
	RoutingKey  string          `json:"routing_key"`
	EventAction string          `json:"event_action"`
	DedupKey    string          `json:"dedup_key"`
	Client      string          `json:"client"`
	Payload     *enqueuePayload `json:"payload"`
}

type enqueuePayload struct {
	Summary  string `json:"summary"`
	Source   string `json:"source"`
	Severity string `json:"severity"`
}

type enqueueAccepted struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	DedupKey string `json:"dedup_key"`
}

type enqueueRejected struct {
	Status  string   `json:"status"`
	Message string   `json:"message"`
	Errors  []string `json:"errors"`
}

// deferEvent answers a sender that must try the same event again. Alertmanager's
// retrier retries 5xx and 429 and drops every other status, so a conflict that
// outlived its bounded re-apply must never be reported as 409: that would
// silently discard a resolve and leave the incident escalating.
func deferEvent(w http.ResponseWriter, reason string) {
	writeJSON(w, http.StatusServiceUnavailable, enqueueRejected{
		Status:  "error",
		Message: "Event not processed",
		Errors:  []string{reason},
	})
}

func (s *Server) enqueue(w http.ResponseWriter, r *http.Request) {
	if !isJSON(r) {
		rejectEvent(w, "Content-Type must be application/json")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxEventBytes)
	var request enqueueRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		rejectEvent(w, "request body is not a valid event object")
		return
	}
	alert := incident.Alert{
		RoutingKey: request.RoutingKey,
		Action:     incident.Action(request.EventAction),
		DedupKey:   request.DedupKey,
		Client:     request.Client,
	}
	if request.Payload != nil {
		alert.Summary = request.Payload.Summary
		alert.Source = request.Payload.Source
		alert.Severity = incident.Severity(request.Payload.Severity)
	}
	receipt, err := s.desk.Ingest(r.Context(), alert)
	if errors.Is(err, incident.ErrInvalid) {
		rejectEvent(w, err.Error())
		return
	}
	if errors.Is(err, incident.ErrConflict) {
		deferEvent(w, "the incident changed concurrently; retry this event")
		return
	}
	if err != nil {
		writePolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, enqueueAccepted{
		Status:   "success",
		Message:  "Event processed",
		DedupKey: receipt.DedupKey,
	})
}

func rejectEvent(w http.ResponseWriter, reason string) {
	writeJSON(w, http.StatusBadRequest, enqueueRejected{
		Status:  "invalid event",
		Message: "Event object is invalid",
		Errors:  []string{reason},
	})
}
