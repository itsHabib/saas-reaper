package api

import (
	"net/http"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/incident"
	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/oncall"
)

type responderRequest struct {
	ID         string `json:"id"`
	Email      string `json:"email,omitempty"`
	WebhookURL string `json:"webhookUrl,omitempty"`
}

type responderResponse struct {
	ID            string    `json:"id"`
	Email         string    `json:"email,omitempty"`
	WebhookURL    string    `json:"webhookUrl,omitempty"`
	WebhookSecret string    `json:"webhookSecret,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}

type scheduleRequest struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Layers    []oncall.Layer    `json:"layers"`
	Overrides []oncall.Override `json:"overrides,omitempty"`
}

type policyRequest struct {
	ID     string           `json:"id"`
	Name   string           `json:"name"`
	Repeat int              `json:"repeat"`
	Levels []incident.Level `json:"levels"`
}

type serviceRequest struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	EscalationPolicy string `json:"escalationPolicy"`
}

type serviceResponse struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	EscalationPolicy string    `json:"escalationPolicy"`
	RoutingKey       string    `json:"routingKey,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
}

func (s *Server) createResponder(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var request responderRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	responder, err := s.desk.CreateResponder(r.Context(), incident.Responder{
		ID:         request.ID,
		Email:      request.Email,
		WebhookURL: request.WebhookURL,
	})
	if err != nil {
		writePolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, responderResponse{
		ID:            responder.ID,
		Email:         responder.Email,
		WebhookURL:    responder.WebhookURL,
		WebhookSecret: responder.WebhookSecret,
		CreatedAt:     responder.CreatedAt,
	})
}

func (s *Server) createSchedule(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var request scheduleRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	schedule := oncall.Schedule{Layers: request.Layers, Overrides: request.Overrides}
	if err := s.desk.CreateSchedule(r.Context(), request.ID, request.Name, schedule); err != nil {
		writePolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":        request.ID,
		"name":      request.Name,
		"layers":    schedule.Layers,
		"overrides": schedule.Overrides,
	})
}

func (s *Server) createPolicy(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var request policyRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	policy := incident.EscalationPolicy{
		ID:     request.ID,
		Name:   request.Name,
		Repeat: request.Repeat,
		Levels: request.Levels,
	}
	if err := s.desk.CreatePolicy(r.Context(), policy); err != nil {
		writePolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, request)
}

func (s *Server) createService(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var request serviceRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	service, err := s.desk.CreateService(r.Context(), incident.Service{
		ID:       request.ID,
		Name:     request.Name,
		PolicyID: request.EscalationPolicy,
	})
	if err != nil {
		writePolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, serviceResponse{
		ID:               service.ID,
		Name:             service.Name,
		EscalationPolicy: service.PolicyID,
		RoutingKey:       service.RoutingKey,
		CreatedAt:        service.CreatedAt,
	})
}

func (s *Server) acknowledge(w http.ResponseWriter, r *http.Request) {
	current, err := s.desk.Acknowledge(r.Context(), r.PathValue("incident"))
	if err != nil {
		writePolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, incidentView(current))
}

func (s *Server) resolve(w http.ResponseWriter, r *http.Request) {
	current, err := s.desk.Resolve(r.Context(), r.PathValue("incident"))
	if err != nil {
		writePolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, incidentView(current))
}

func (s *Server) onCall(w http.ResponseWriter, r *http.Request) {
	var at time.Time
	if raw := r.URL.Query().Get("at"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "at must be an RFC 3339 instant"})
			return
		}
		at = parsed
	}
	responder, onCall, err := s.desk.OnCall(r.Context(), r.PathValue("schedule"), at)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	view := map[string]any{"schedule": r.PathValue("schedule"), "onCall": onCall}
	if onCall {
		view["responder"] = responder
	}
	writeJSON(w, http.StatusOK, view)
}
