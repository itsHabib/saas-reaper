package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/store/sqlite"
	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/tunnel"
)

const (
	adminToken = "management-token"
	readToken  = "read-token"
)

type fixture struct {
	server   *httptest.Server
	service  *tunnel.Service
	registry *tunnel.Registry
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "tunnel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	registry := tunnel.NewRegistry()
	logger := slog.New(slog.DiscardHandler)
	service, err := tunnel.NewService(store, registry, "operator", time.Now, nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(service, store, adminToken, readToken)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	server.Register(mux)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)
	return fixture{server: httpServer, service: service, registry: registry}
}

func (f fixture) do(t *testing.T, method, path, token, contentType, body string) (*http.Response, []byte) {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), method, f.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := f.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, raw
}

type stubLink struct{}

func (stubLink) Open(context.Context) (net.Conn, error) { return nil, net.ErrClosed }
func (stubLink) Close(tunnel.CloseReason) error         { return nil }

func TestNewRejectsAuthorityCollapse(t *testing.T) {
	if _, err := New(nil, nil, "a", "b"); err == nil {
		t.Fatal("nil collaborators accepted")
	}
	f := newFixture(t)
	if _, err := New(f.service, nil, "same", "same"); err == nil {
		t.Fatal("identical tokens accepted")
	}
}

func TestClaimRevokeAndReadKeepTheTokenOutOfTheReadPlane(t *testing.T) {
	f := newFixture(t)
	response, raw := f.do(t, http.MethodPost, "/v1/tunnels", adminToken, "application/json", `{"subdomain":"acme"}`)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("claim = %d %s", response.StatusCode, raw)
	}
	var claimed claimResponse
	if err := json.Unmarshal(raw, &claimed); err != nil {
		t.Fatal(err)
	}
	if claimed.Subdomain != "acme" || !strings.HasPrefix(claimed.Token, "rtk_") || claimed.Revision != 1 {
		t.Fatalf("claimed = %+v", claimed)
	}
	claim, err := f.service.Authenticate(context.Background(), claimed.Token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Attach(context.Background(), claim, stubLink{}); err != nil {
		t.Fatal(err)
	}
	assertListRedacted(t, f, claimed.Token)
	response, raw = f.do(t, http.MethodPost, "/v1/tunnels/acme/revoke", adminToken, "application/json", `{"expectedRevision":1}`)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"state":"revoked"`) || !strings.Contains(string(raw), `"revokedAt"`) {
		t.Fatalf("revoke = %d %s", response.StatusCode, raw)
	}
	if f.registry.Presence("acme") != tunnel.PresenceAbsent {
		t.Fatal("revoke left the link attached")
	}
	assertAuditRedacted(t, f, claimed.Token)
}

func assertListRedacted(t *testing.T, f fixture, token string) {
	t.Helper()
	response, raw := f.do(t, http.MethodGet, "/v1/tunnels", readToken, "", "")
	if response.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"presence":"live"`) {
		t.Fatalf("list = %d %s", response.StatusCode, raw)
	}
	if strings.Contains(string(raw), token) || strings.Contains(string(raw), tunnel.HashToken(token)) {
		t.Fatal("the read plane exposed credential material")
	}
}

func assertAuditRedacted(t *testing.T, f fixture, token string) {
	t.Helper()
	response, raw := f.do(t, http.MethodGet, "/v1/audit?limit=10", readToken, "", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("audit = %d", response.StatusCode)
	}
	for _, kind := range []string{`"kind":"claimed"`, `"kind":"connected"`, `"kind":"disconnected"`, `"kind":"revoked"`} {
		if !strings.Contains(string(raw), kind) {
			t.Fatalf("audit lacks %s: %s", kind, raw)
		}
	}
	if strings.Contains(string(raw), token) {
		t.Fatal("audit exposed the token")
	}
}

func TestTokensAreSeparatedInBothDirections(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		method, path, token string
	}{
		{http.MethodPost, "/v1/tunnels", readToken},
		{http.MethodPost, "/v1/tunnels", ""},
		{http.MethodPost, "/v1/tunnels/acme/revoke", readToken},
		{http.MethodGet, "/v1/tunnels", adminToken},
		{http.MethodGet, "/v1/audit", adminToken},
		{http.MethodGet, "/v1/audit", ""},
	}
	for _, tc := range cases {
		response, _ := f.do(t, tc.method, tc.path, tc.token, "application/json", `{"subdomain":"acme"}`)
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s %s with %q = %d, want 401", tc.method, tc.path, tc.token, response.StatusCode)
		}
	}
}

func TestRejectionsCarryTheRightStatus(t *testing.T) {
	f := newFixture(t)
	if response, _ := f.do(t, http.MethodPost, "/v1/tunnels", adminToken, "text/plain", `{"subdomain":"acme"}`); response.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("wrong content type = %d", response.StatusCode)
	}
	if response, _ := f.do(t, http.MethodPost, "/v1/tunnels", adminToken, "application/json", `{"subdomain":"Bad"}`); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid subdomain = %d", response.StatusCode)
	}
	if response, _ := f.do(t, http.MethodPost, "/v1/tunnels", adminToken, "application/json", `{"subdomain":"acme","extra":1}`); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field = %d", response.StatusCode)
	}
	if response, _ := f.do(t, http.MethodPost, "/v1/tunnels", adminToken, "application/json", `{"subdomain":"acme"}`); response.StatusCode != http.StatusCreated {
		t.Fatalf("first claim = %d", response.StatusCode)
	}
	if response, _ := f.do(t, http.MethodPost, "/v1/tunnels", adminToken, "application/json", `{"subdomain":"acme"}`); response.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate claim = %d", response.StatusCode)
	}
	if response, _ := f.do(t, http.MethodPost, "/v1/tunnels/ghost/revoke", adminToken, "application/json", `{"expectedRevision":1}`); response.StatusCode != http.StatusNotFound {
		t.Fatalf("revoke unknown = %d", response.StatusCode)
	}
	if response, _ := f.do(t, http.MethodPost, "/v1/tunnels/acme/revoke", adminToken, "application/json", `{"expectedRevision":9}`); response.StatusCode != http.StatusConflict {
		t.Fatalf("stale revoke = %d", response.StatusCode)
	}
	if response, _ := f.do(t, http.MethodGet, "/v1/audit?limit=0", readToken, "", ""); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad limit = %d", response.StatusCode)
	}
	if response, _ := f.do(t, http.MethodGet, "/healthz", "", "", ""); response.StatusCode != http.StatusOK {
		t.Fatalf("health = %d", response.StatusCode)
	}
}
