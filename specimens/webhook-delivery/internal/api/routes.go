// Package api translates authenticated management and audit HTTP into delivery policy.
package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"mime"
	"net/http"
	"strings"

	"github.com/itsHabib/saas-reaper/specimens/webhook-delivery/internal/delivery"
)

// AttemptReader exposes the append-only delivery evidence to the read plane.
type AttemptReader interface {
	Attempts(context.Context, delivery.AttemptFilter, int) ([]delivery.Attempt, error)
}

// Server exposes separately authenticated management and audit-read routes.
type Server struct {
	delivery   *delivery.Service
	attempts   AttemptReader
	adminToken string
	readToken  string
}

// New constructs the transport boundary and rejects authority collapse.
func New(service *delivery.Service, attempts AttemptReader, adminToken, readToken string) (*Server, error) {
	if service == nil || attempts == nil {
		return nil, errors.New("delivery service and attempt reader are required")
	}
	if strings.TrimSpace(adminToken) == "" || strings.TrimSpace(readToken) == "" {
		return nil, errors.New("management and audit-read tokens are required")
	}
	if secureEqual(adminToken, readToken) {
		return nil, errors.New("management and audit-read tokens must differ")
	}
	return &Server{delivery: service, attempts: attempts, adminToken: adminToken, readToken: readToken}, nil
}

// Handler returns the complete local management, audit, and health surface.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("POST /v1/endpoints", s.authorize(s.adminToken, s.registerEndpoint))
	mux.HandleFunc("POST /v1/endpoints/{endpoint}/disable", s.authorize(s.adminToken, s.disableEndpoint))
	mux.HandleFunc("POST /v1/messages", s.authorize(s.adminToken, s.publishMessage))
	mux.HandleFunc(
		"POST /v1/messages/{message}/replay/{endpoint}",
		s.authorize(s.adminToken, s.replayMessage),
	)
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
	if errors.Is(err, delivery.ErrInvalid) {
		status = http.StatusBadRequest
		message = err.Error()
	}
	if errors.Is(err, delivery.ErrConflict) || errors.Is(err, delivery.ErrDisabled) {
		status = http.StatusConflict
		message = err.Error()
	}
	if errors.Is(err, delivery.ErrNotFound) {
		status = http.StatusNotFound
		message = err.Error()
	}
	writeJSON(w, status, map[string]string{"error": message})
}

func requireJSON(w http.ResponseWriter, r *http.Request) error {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err == nil && contentType == "application/json" {
		return nil
	}
	writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "content type must be application/json"})
	return errors.New("content type must be application/json")
}
