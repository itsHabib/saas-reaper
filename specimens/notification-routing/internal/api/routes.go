// Package api translates authenticated management and audit HTTP into routing policy.
package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"mime"
	"net/http"
	"strings"

	"github.com/itsHabib/saas-reaper/specimens/notification-routing/internal/routing"
)

// AttemptReader exposes the append-only delivery evidence to the read plane.
type AttemptReader interface {
	Attempts(context.Context, routing.AttemptFilter, int) ([]routing.Attempt, error)
}

// Server exposes separately authenticated management and audit-read routes.
type Server struct {
	routing    *routing.Service
	attempts   AttemptReader
	adminToken string
	readToken  string
}

// New constructs the transport boundary and rejects authority collapse.
func New(service *routing.Service, attempts AttemptReader, adminToken, readToken string) (*Server, error) {
	if service == nil || attempts == nil {
		return nil, errors.New("routing service and attempt reader are required")
	}
	if strings.TrimSpace(adminToken) == "" || strings.TrimSpace(readToken) == "" {
		return nil, errors.New("management and audit-read tokens are required")
	}
	if secureEqual(adminToken, readToken) {
		return nil, errors.New("management and audit-read tokens must differ")
	}
	return &Server{routing: service, attempts: attempts, adminToken: adminToken, readToken: readToken}, nil
}

// Handler returns the complete local management, audit, and health surface.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("POST /v1/channels", s.authorize(s.adminToken, s.registerChannel))
	mux.HandleFunc("POST /v1/channels/{channel}/disable", s.authorize(s.adminToken, s.disableChannel))
	mux.HandleFunc("POST /v1/templates", s.authorize(s.adminToken, s.createTemplate))
	mux.HandleFunc("POST /v1/recipients", s.authorize(s.adminToken, s.createRecipient))
	mux.HandleFunc("POST /v1/notifications", s.authorize(s.adminToken, s.sendNotification))
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
	if errors.Is(err, routing.ErrInvalid) {
		status = http.StatusBadRequest
		message = err.Error()
	}
	if errors.Is(err, routing.ErrConflict) || errors.Is(err, routing.ErrDisabled) {
		status = http.StatusConflict
		message = err.Error()
	}
	if errors.Is(err, routing.ErrNotFound) {
		status = http.StatusNotFound
		message = err.Error()
	}
	writeJSON(w, status, map[string]string{"error": message})
}

// requireJSON reports whether the request declares a JSON body; it writes the rejection itself.
func requireJSON(w http.ResponseWriter, r *http.Request) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err == nil && contentType == "application/json" {
		return true
	}
	writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "content type must be application/json"})
	return false
}
