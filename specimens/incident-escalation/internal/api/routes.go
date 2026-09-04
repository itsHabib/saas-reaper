// Package api translates Events API v2 ingest, management, and audit HTTP into incident policy.
package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"mime"
	"net/http"
	"strings"

	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/incident"
)

// Reader exposes incidents, their journal, and the page audit to the read plane.
type Reader interface {
	Incident(context.Context, string) (incident.Incident, error)
	Incidents(context.Context, incident.Filter, int) ([]incident.Incident, error)
	Events(context.Context, string) ([]incident.Event, error)
	Notifications(context.Context, string) ([]incident.Notification, error)
	Attempts(context.Context, incident.AttemptFilter, int) ([]incident.Attempt, error)
}

// Server exposes the ingest, management, and audit-read routes with separate authorities.
type Server struct {
	desk       *incident.Desk
	reader     Reader
	adminToken string
	readToken  string
}

// New constructs the transport boundary and rejects authority collapse.
func New(desk *incident.Desk, reader Reader, adminToken, readToken string) (*Server, error) {
	if desk == nil || reader == nil {
		return nil, errors.New("incident desk and reader are required")
	}
	if strings.TrimSpace(adminToken) == "" || strings.TrimSpace(readToken) == "" {
		return nil, errors.New("management and audit-read tokens are required")
	}
	if secureEqual(adminToken, readToken) {
		return nil, errors.New("management and audit-read tokens must differ")
	}
	return &Server{desk: desk, reader: reader, adminToken: adminToken, readToken: readToken}, nil
}

// Handler returns the complete ingest, management, audit, and health surface.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("POST /v2/enqueue", s.enqueue)
	mux.HandleFunc("POST /v1/responders", s.authorize(s.adminToken, s.createResponder))
	mux.HandleFunc("POST /v1/schedules", s.authorize(s.adminToken, s.createSchedule))
	mux.HandleFunc("POST /v1/escalation-policies", s.authorize(s.adminToken, s.createPolicy))
	mux.HandleFunc("POST /v1/services", s.authorize(s.adminToken, s.createService))
	mux.HandleFunc("POST /v1/incidents/{incident}/acknowledge", s.authorize(s.adminToken, s.acknowledge))
	mux.HandleFunc("POST /v1/incidents/{incident}/resolve", s.authorize(s.adminToken, s.resolve))
	mux.HandleFunc("GET /v1/schedules/{schedule}/on-call", s.authorize(s.readToken, s.onCall))
	mux.HandleFunc("GET /v1/incidents", s.authorize(s.readToken, s.listIncidents))
	mux.HandleFunc("GET /v1/incidents/{incident}", s.authorize(s.readToken, s.getIncident))
	mux.HandleFunc("GET /v1/incidents/{incident}/events", s.authorize(s.readToken, s.listEvents))
	mux.HandleFunc("GET /v1/incidents/{incident}/notifications", s.authorize(s.readToken, s.listNotifications))
	mux.HandleFunc("GET /v1/attempts", s.authorize(s.readToken, s.listAttempts))
	return mux
}

func health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) authorize(token string, next http.HandlerFunc) http.HandlerFunc {
	expected := "Bearer " + token
	return func(w http.ResponseWriter, r *http.Request) {
		if secureEqual(r.Header.Get("Authorization"), expected) {
			next(w, r)
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	}
}

func secureEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func writePolicyError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "internal server error"
	if errors.Is(err, incident.ErrInvalid) {
		status = http.StatusBadRequest
		message = err.Error()
	}
	if errors.Is(err, incident.ErrConflict) {
		status = http.StatusConflict
		message = err.Error()
	}
	if errors.Is(err, incident.ErrNotFound) {
		status = http.StatusNotFound
		message = err.Error()
	}
	writeJSON(w, status, map[string]string{"error": message})
}

func isJSON(r *http.Request) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && contentType == "application/json"
}

func requireJSON(w http.ResponseWriter, r *http.Request) bool {
	if isJSON(r) {
		return true
	}
	writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "content type must be application/json"})
	return false
}
