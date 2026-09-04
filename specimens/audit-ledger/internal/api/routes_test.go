package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/audit-ledger/internal/ledger"
	"github.com/itsHabib/saas-reaper/specimens/audit-ledger/internal/store/sqlite"
)

const (
	writeToken = "write-token"
	readToken  = "read-token"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	clock := func() time.Time { return time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC) }
	service, err := ledger.NewService(store, clock, "api-test")
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	server, err := New(service, store, writeToken, readToken, []string{"acme", "globex"})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	return httpServer
}

func do(t *testing.T, server *httptest.Server, method, path, token, body string) (int, string) {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), method, server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, path, err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return response.StatusCode, string(raw)
}

func eventBody(tenant, id string) string {
	return `{"tenant":"` + tenant + `","id":"` + id + `","actor":"user:ada","action":"doc.viewed",` +
		`"target":"doc:` + id + `","occurredAt":"2026-08-30T11:00:00Z","metadata":{"k":"v"}}`
}

func TestNewRejectsAuthorityCollapse(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	service, err := ledger.NewService(store, time.Now, "api-test")
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	cases := map[string]func() error{
		"same tokens":    func() error { _, err := New(service, store, "same", "same", []string{"acme"}); return err },
		"blank token":    func() error { _, err := New(service, store, " ", readToken, []string{"acme"}); return err },
		"no tenants":     func() error { _, err := New(service, store, writeToken, readToken, nil); return err },
		"bad tenant":     func() error { _, err := New(service, store, writeToken, readToken, []string{"Acme"}); return err },
		"nil dependency": func() error { _, err := New(nil, store, writeToken, readToken, []string{"acme"}); return err },
	}
	for name, construct := range cases {
		if err := construct(); err == nil {
			t.Fatalf("%s: constructed", name)
		}
	}
}

func TestTokenSeparation(t *testing.T) {
	server := newTestServer(t)
	if status, _ := do(t, server, http.MethodPost, "/v1/events", readToken, eventBody("acme", "e1")); status != http.StatusUnauthorized {
		t.Fatalf("read token appended: %d", status)
	}
	if status, _ := do(t, server, http.MethodGet, "/v1/tenants/acme/head", writeToken, ""); status != http.StatusUnauthorized {
		t.Fatalf("write token read: %d", status)
	}
	if status, _ := do(t, server, http.MethodGet, "/v1/tenants/acme/events", "", ""); status != http.StatusUnauthorized {
		t.Fatalf("anonymous read: %d", status)
	}
}

func TestIngestSingleBatchAndReplayStatuses(t *testing.T) {
	server := newTestServer(t)
	status, body := do(t, server, http.MethodPost, "/v1/events", writeToken, eventBody("acme", "e1"))
	if status != http.StatusCreated || !strings.Contains(body, `"sequence":1`) {
		t.Fatalf("single ingest: %d %s", status, body)
	}
	batch := "[" + eventBody("acme", "e1") + "," + eventBody("acme", "e2") + "]"
	status, body = do(t, server, http.MethodPost, "/v1/events", writeToken, batch)
	if status != http.StatusCreated || !strings.Contains(body, `"replayed":true`) || !strings.Contains(body, `"sequence":2`) {
		t.Fatalf("batch ingest: %d %s", status, body)
	}
	status, body = do(t, server, http.MethodPost, "/v1/events", writeToken, batch)
	if status != http.StatusOK || strings.Contains(body, `"replayed":false`) {
		t.Fatalf("full replay: %d %s", status, body)
	}
	conflict := strings.Replace(eventBody("acme", "e1"), "user:ada", "user:mallory", 1)
	if status, _ := do(t, server, http.MethodPost, "/v1/events", writeToken, conflict); status != http.StatusConflict {
		t.Fatalf("conflict: %d", status)
	}
}

func TestIngestRejections(t *testing.T) {
	server := newTestServer(t)
	cases := map[string]struct {
		body        string
		contentType bool
		want        int
	}{
		"unknown field":  {`{"tenant":"acme","id":"e1","extra":1}`, true, http.StatusBadRequest},
		"empty body":     {" ", true, http.StatusBadRequest},
		"empty batch":    {"[]", true, http.StatusBadRequest},
		"two values":     {"{} {}", true, http.StatusBadRequest},
		"float metadata": {strings.Replace(eventBody("acme", "e1"), `{"k":"v"}`, `{"k":1.5}`, 1), true, http.StatusBadRequest},
		"bad tenant":     {eventBody("Acme", "e1"), true, http.StatusBadRequest},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			status, body := do(t, server, http.MethodPost, "/v1/events", writeToken, tc.body)
			if status != tc.want {
				t.Fatalf("status %d body %s, want %d", status, body, tc.want)
			}
		})
	}
	request, err := http.NewRequestWithContext(
		context.Background(), http.MethodPost, server.URL+"/v1/events", strings.NewReader(eventBody("acme", "e1")))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+writeToken)
	request.Header.Set("Content-Type", "text/plain")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain accepted: %d", response.StatusCode)
	}
	if status, _ := do(t, server, http.MethodGet, "/v1/tenants/acme/head", readToken, ""); status != http.StatusOK {
		t.Fatalf("rejections changed readability: %d", status)
	}
}

func TestTenantScopeHidesExistence(t *testing.T) {
	server := newTestServer(t)
	if status, _ := do(t, server, http.MethodPost, "/v1/events", writeToken, eventBody("initech", "e1")); status != http.StatusCreated {
		t.Fatalf("unscoped tenant append: %d", status)
	}
	for _, route := range []string{"head", "events", "export"} {
		existingStatus, existingBody := do(t, server, http.MethodGet, "/v1/tenants/initech/"+route, readToken, "")
		absentStatus, absentBody := do(t, server, http.MethodGet, "/v1/tenants/nobody/"+route, readToken, "")
		if existingStatus != http.StatusNotFound || absentStatus != http.StatusNotFound {
			t.Fatalf("%s: %d / %d, want 404 / 404", route, existingStatus, absentStatus)
		}
		if existingBody != absentBody {
			t.Fatalf("%s: existence leaked: %q vs %q", route, existingBody, absentBody)
		}
	}
	status, body := do(t, server, http.MethodGet, "/v1/tenants/globex/head", readToken, "")
	if status != http.StatusOK || !strings.Contains(body, ledger.GenesisHash) {
		t.Fatalf("scoped empty tenant head: %d %s", status, body)
	}
}

func TestReadsPaginateAndExport(t *testing.T) {
	server := newTestServer(t)
	var batch []string
	for _, id := range []string{"e1", "e2", "e3", "e4", "e5"} {
		batch = append(batch, eventBody("acme", id))
	}
	if status, _ := do(t, server, http.MethodPost, "/v1/events", writeToken, "["+strings.Join(batch, ",")+"]"); status != http.StatusCreated {
		t.Fatalf("batch: %d", status)
	}
	walked := walkPages(t, server)
	if len(walked) != 5 || walked[0] != 1 || walked[4] != 5 {
		t.Fatalf("walk %v", walked)
	}
	for _, query := range []string{"after=-1", "limit=0", "limit=1001", "after=x"} {
		if status, _ := do(t, server, http.MethodGet, "/v1/tenants/acme/events?"+query, readToken, ""); status != http.StatusBadRequest {
			t.Fatalf("%s accepted: %d", query, status)
		}
	}
	entries := exportEntries(t, server)
	head, err := ledger.Verify(entries)
	if err != nil || head.Sequence != 5 {
		t.Fatalf("exported chain: %+v %v", head, err)
	}
	_, body := do(t, server, http.MethodGet, "/v1/tenants/acme/head", readToken, "")
	if !strings.Contains(body, head.Hash) {
		t.Fatalf("head %s not reported by /head: %s", head.Hash, body)
	}
}

func walkPages(t *testing.T, server *httptest.Server) []int64 {
	t.Helper()
	var walked []int64
	after := int64(0)
	for range 10 {
		_, body := do(t, server, http.MethodGet, "/v1/tenants/acme/events?after="+jsonNumber(after)+"&limit=2", readToken, "")
		var page struct {
			Events []struct {
				Sequence int64 `json:"sequence"`
			} `json:"events"`
			Next int64 `json:"next"`
		}
		if err := json.Unmarshal([]byte(body), &page); err != nil {
			t.Fatalf("decode page: %v", err)
		}
		if len(page.Events) == 0 {
			return walked
		}
		for _, entry := range page.Events {
			walked = append(walked, entry.Sequence)
		}
		after = page.Next
	}
	return walked
}

func exportEntries(t *testing.T, server *httptest.Server) []ledger.Entry {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/v1/tenants/acme/export", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+readToken)
	exported, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	defer func() { _ = exported.Body.Close() }()
	if exported.Header.Get("Content-Type") != "application/x-ndjson" {
		t.Fatalf("export content type %q", exported.Header.Get("Content-Type"))
	}
	var entries []ledger.Entry
	scanner := bufio.NewScanner(exported.Body)
	for scanner.Scan() {
		var view entryResponse
		if err := json.Unmarshal(scanner.Bytes(), &view); err != nil {
			t.Fatalf("decode export line: %v", err)
		}
		entries = append(entries, ledger.Entry{
			Tenant: view.Tenant, Sequence: view.Sequence, ID: view.ID, Actor: view.Actor, Action: view.Action,
			Target: view.Target, OccurredAt: view.OccurredAt, RecordedAt: view.RecordedAt, Source: view.Source,
			Metadata: view.Metadata, Hash: view.Hash,
		})
	}
	return entries
}

func jsonNumber(value int64) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
