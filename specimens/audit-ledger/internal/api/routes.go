// Package api translates authenticated ingest and tenant-scoped read HTTP into ledger policy.
package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"mime"
	"net/http"
	"strings"

	"github.com/itsHabib/saas-reaper/specimens/audit-ledger/internal/ledger"
)

// Ingester appends validated events on behalf of the write authority.
type Ingester interface {
	Append(context.Context, []ledger.Event) ([]ledger.Receipt, error)
}

// Reader exposes recorded chains to the read authority.
type Reader interface {
	Head(context.Context, string) (ledger.Head, error)
	Entries(context.Context, string, int64, int) ([]ledger.Entry, error)
	Export(context.Context, string, func(ledger.Entry) error) error
}

// Server exposes separately authenticated write and tenant-scoped read routes.
type Server struct {
	ledger      Ingester
	reader      Reader
	writeToken  string
	readToken   string
	readTenants map[string]struct{}
}

// New constructs the transport boundary and rejects authority collapse. The
// read token may see exactly the configured tenants; every other tenant name
// is indistinguishable from one that does not exist.
func New(ingester Ingester, reader Reader, writeToken, readToken string, readTenants []string) (*Server, error) {
	if ingester == nil || reader == nil {
		return nil, errors.New("ledger ingester and reader are required")
	}
	if strings.TrimSpace(writeToken) == "" || strings.TrimSpace(readToken) == "" {
		return nil, errors.New("write and read tokens are required")
	}
	if secureEqual(writeToken, readToken) {
		return nil, errors.New("write and read tokens must differ")
	}
	scope, err := tenantScope(readTenants)
	if err != nil {
		return nil, err
	}
	return &Server{
		ledger:      ingester,
		reader:      reader,
		writeToken:  writeToken,
		readToken:   readToken,
		readTenants: scope,
	}, nil
}

func tenantScope(tenants []string) (map[string]struct{}, error) {
	if len(tenants) == 0 {
		return nil, errors.New("read token requires at least one scoped tenant")
	}
	scope := make(map[string]struct{}, len(tenants))
	for _, tenant := range tenants {
		if err := ledger.ValidateTenant(tenant); err != nil {
			return nil, err
		}
		scope[tenant] = struct{}{}
	}
	return scope, nil
}

// Handler returns the complete local ingest, read, and health surface.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("POST /v1/events", s.authorize(s.writeToken, s.ingestEvents))
	mux.HandleFunc("GET /v1/tenants/{tenant}/head", s.authorize(s.readToken, s.scoped(s.readHead)))
	mux.HandleFunc("GET /v1/tenants/{tenant}/events", s.authorize(s.readToken, s.scoped(s.listEvents)))
	mux.HandleFunc("GET /v1/tenants/{tenant}/export", s.authorize(s.readToken, s.scoped(s.exportEvents)))
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

type tenantHandler func(http.ResponseWriter, *http.Request, string)

func (s *Server) scoped(next tenantHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenant := r.PathValue("tenant")
		if _, ok := s.readTenants[tenant]; !ok {
			writeTenantNotFound(w)
			return
		}
		next(w, r, tenant)
	}
}

func writeTenantNotFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
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
	if errors.Is(err, ledger.ErrInvalid) {
		status = http.StatusBadRequest
		message = err.Error()
	}
	if errors.Is(err, ledger.ErrConflict) {
		status = http.StatusConflict
		message = err.Error()
	}
	writeJSON(w, status, map[string]string{"error": message})
}

func requireJSON(r *http.Request) error {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err == nil && contentType == "application/json" {
		return nil
	}
	return errors.New("content type must be application/json")
}
