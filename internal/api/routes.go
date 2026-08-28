package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/itsHabib/saas-reaper/internal/flags"
)

const maxRequestBytes = 1 << 20

// Server exposes separately authenticated management and evaluation routes.
type Server struct {
	flags           *flags.Service
	adminToken      string
	adminActor      string
	evaluationToken string
}

// New constructs an HTTP server around an initialized flag service.
func New(service *flags.Service, adminToken, adminActor, evaluationToken string) (*Server, error) {
	if strings.TrimSpace(adminToken) == "" {
		return nil, errors.New("admin token is required")
	}
	if strings.TrimSpace(adminActor) == "" {
		return nil, errors.New("admin actor is required")
	}
	if strings.TrimSpace(evaluationToken) == "" {
		return nil, errors.New("evaluation token is required")
	}
	if secureEqual(adminToken, evaluationToken) {
		return nil, errors.New("admin and evaluation tokens must differ")
	}
	return &Server{
		flags:           service,
		adminToken:      adminToken,
		adminActor:      adminActor,
		evaluationToken: evaluationToken,
	}, nil
}

// Handler returns the complete management, evaluation, and health surface.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc(
		"PUT /v1/environments/{environment}/flags/{key}",
		s.authorize(s.adminToken, s.publish),
	)
	mux.HandleFunc(
		"GET /v1/environments/{environment}/flags",
		s.authorize(s.adminToken, s.list),
	)
	mux.HandleFunc("GET /v1/audit", s.authorize(s.adminToken, s.audit))
	mux.HandleFunc(
		"POST /environments/{environment}/ofrep/v1/evaluate/flags/{key}",
		s.authorize(s.evaluationToken, s.evaluate),
	)
	mux.HandleFunc(
		"POST /environments/{environment}/ofrep/v1/evaluate/flags",
		s.authorize(s.evaluationToken, s.evaluateAll),
	)
	return mux
}

func health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) authorize(token string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		provided := strings.TrimPrefix(header, "Bearer ")
		if secureEqual(provided, token) {
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

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode request: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("decode request: multiple JSON values")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if status == http.StatusNoContent || status == http.StatusNotModified {
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}
