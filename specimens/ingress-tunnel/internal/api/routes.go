// Package api translates authenticated management and audit HTTP into tunnel policy.
package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"mime"
	"net/http"
	"strings"

	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/tunnel"
)

// AuditReader exposes the append-only lifecycle evidence to the read plane.
type AuditReader interface {
	Audit(context.Context, int) ([]tunnel.AuditEntry, error)
}

// Server exposes separately authenticated management and read routes.
type Server struct {
	tunnels    *tunnel.Service
	audit      AuditReader
	adminToken string
	readToken  string
}

// New constructs the transport boundary and rejects authority collapse.
func New(service *tunnel.Service, audit AuditReader, adminToken, readToken string) (*Server, error) {
	if service == nil || audit == nil {
		return nil, errors.New("tunnel service and audit reader are required")
	}
	if strings.TrimSpace(adminToken) == "" || strings.TrimSpace(readToken) == "" {
		return nil, errors.New("management and read tokens are required")
	}
	if secureEqual(adminToken, readToken) {
		return nil, errors.New("management and read tokens must differ")
	}
	return &Server{tunnels: service, audit: audit, adminToken: adminToken, readToken: readToken}, nil
}

// Register mounts the management, read, and health routes on mux.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("POST /v1/tunnels", s.authorize(s.adminToken, s.claimTunnel))
	mux.HandleFunc("POST /v1/tunnels/{subdomain}/revoke", s.authorize(s.adminToken, s.revokeTunnel))
	mux.HandleFunc("GET /v1/tunnels", s.authorize(s.readToken, s.listTunnels))
	mux.HandleFunc("GET /v1/audit", s.authorize(s.readToken, s.listAudit))
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
	if errors.Is(err, tunnel.ErrInvalid) {
		status = http.StatusBadRequest
		message = err.Error()
	}
	if errors.Is(err, tunnel.ErrConflict) || errors.Is(err, tunnel.ErrRevoked) {
		status = http.StatusConflict
		message = err.Error()
	}
	if errors.Is(err, tunnel.ErrNotFound) {
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
