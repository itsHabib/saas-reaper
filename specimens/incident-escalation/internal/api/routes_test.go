package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/incident"
	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/store/sqlite"
)

const (
	adminToken = "management-token"
	readToken  = "audit-read-token"
)

func newHarness(t *testing.T) (http.Handler, *sqlite.Store, *incident.Desk) {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "incidents.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := func() time.Time { return time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC) }
	desk, err := incident.NewDesk(store, "operator", now, incident.RandomID, incident.RandomSecret)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(desk, store, adminToken, readToken)
	if err != nil {
		t.Fatal(err)
	}
	return server.Handler(), store, desk
}

func do(t *testing.T, handler http.Handler, method, target, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func decode(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return body
}

func seedCatalog(t *testing.T, handler http.Handler) string {
	t.Helper()
	responder := `{"id":"alice","email":"alice@example.test","webhookUrl":"http://127.0.0.1:19500/page/alice"}`
	if got := do(t, handler, http.MethodPost, "/v1/responders", adminToken, responder).Code; got != http.StatusCreated {
		t.Fatalf("create responder: %d", got)
	}
	schedule := `{"id":"primary","name":"Primary","layers":[{"name":"weekly","start":"2026-01-05T09:00:00Z","rotation":"168h","responders":["alice"]}]}`
	if got := do(t, handler, http.MethodPost, "/v1/schedules", adminToken, schedule).Code; got != http.StatusCreated {
		t.Fatalf("create schedule: %d", got)
	}
	policy := `{"id":"ladder","name":"Ladder","repeat":0,"levels":[{"timeout":"30s","schedules":["primary"]}]}`
	if got := do(t, handler, http.MethodPost, "/v1/escalation-policies", adminToken, policy).Code; got != http.StatusCreated {
		t.Fatalf("create policy: %d", got)
	}
	service := `{"id":"checkout","name":"Checkout","escalationPolicy":"ladder"}`
	recorder := do(t, handler, http.MethodPost, "/v1/services", adminToken, service)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create service: %d %s", recorder.Code, recorder.Body.String())
	}
	body := decode(t, recorder)
	key, _ := body["routingKey"].(string)
	if key == "" {
		t.Fatal("service creation must mint a routing key")
	}
	return key
}

// The ingest contract is byte-level: Alertmanager's pagerduty receiver posts this
// exact object shape and treats any non-2xx as a failure to be retried.
func TestEventsAPIv2ContractMatchesTheUpstreamWireShape(t *testing.T) {
	handler, _, _ := newHarness(t)
	routingKey := seedCatalog(t, handler)
	event := `{
		"routing_key": "` + routingKey + `",
		"event_action": "trigger",
		"dedup_key": "abc123",
		"client": "Alertmanager",
		"client_url": "http://alertmanager:9093",
		"images": [],
		"links": [],
		"payload": {
			"summary": "checkout is down",
			"source": "prometheus",
			"severity": "critical",
			"class": "alert",
			"component": "checkout",
			"group": "web",
			"custom_details": {"firing": "1", "num_firing": "1"}
		}
	}`
	recorder := do(t, handler, http.MethodPost, "/v2/enqueue", "", event)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", recorder.Code, recorder.Body.String())
	}
	body := decode(t, recorder)
	if body["status"] != "success" || body["message"] != "Event processed" || body["dedup_key"] != "abc123" {
		t.Fatalf("response does not match the Events API v2 contract: %#v", body)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected a JSON response, got %q", got)
	}
}

func TestIngestRejectsInvalidEventsWithTheUpstreamErrorShape(t *testing.T) {
	handler, _, _ := newHarness(t)
	routingKey := seedCatalog(t, handler)
	cases := map[string]string{
		"unknown routing key": `{"routing_key":"nope","event_action":"trigger","payload":{"summary":"s","source":"p","severity":"error"}}`,
		"missing payload":     `{"routing_key":"` + routingKey + `","event_action":"trigger"}`,
		"bad action":          `{"routing_key":"` + routingKey + `","event_action":"page","payload":{"summary":"s","source":"p","severity":"error"}}`,
		"bad severity":        `{"routing_key":"` + routingKey + `","event_action":"trigger","payload":{"summary":"s","source":"p","severity":"nope"}}`,
		"not json":            `not json at all`,
	}
	for name, event := range cases {
		recorder := do(t, handler, http.MethodPost, "/v2/enqueue", "", event)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d", name, recorder.Code)
		}
		body := decode(t, recorder)
		if body["status"] != "invalid event" || body["message"] != "Event object is invalid" {
			t.Fatalf("%s: unexpected error shape %#v", name, body)
		}
		if _, ok := body["errors"].([]any); !ok {
			t.Fatalf("%s: the error list is part of the contract: %#v", name, body)
		}
	}
	form := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v2/enqueue", strings.NewReader("routing_key=x"))
	form.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, form)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("a non-JSON content type must be rejected, got %d", recorder.Code)
	}
}

func TestTokenSeparationInBothDirections(t *testing.T) {
	handler, _, _ := newHarness(t)
	routingKey := seedCatalog(t, handler)
	event := `{"routing_key":"` + routingKey + `","event_action":"trigger","dedup_key":"k","payload":{"summary":"s","source":"p","severity":"error"}}`
	if got := do(t, handler, http.MethodPost, "/v2/enqueue", "", event).Code; got != http.StatusAccepted {
		t.Fatalf("ingest: %d", got)
	}
	readOnly := []string{"/v1/incidents", "/v1/attempts", "/v1/schedules/primary/on-call"}
	for _, target := range readOnly {
		if got := do(t, handler, http.MethodGet, target, adminToken, "").Code; got != http.StatusUnauthorized {
			t.Fatalf("%s must reject the management token, got %d", target, got)
		}
		if got := do(t, handler, http.MethodGet, target, readToken, "").Code; got != http.StatusOK {
			t.Fatalf("%s must accept the read token, got %d", target, got)
		}
	}
	writes := map[string]string{
		"/v1/responders":          `{"id":"carol","email":"carol@example.test"}`,
		"/v1/schedules":           `{"id":"s2","name":"S2","layers":[]}`,
		"/v1/escalation-policies": `{"id":"p2","name":"P2","levels":[]}`,
		"/v1/services":            `{"id":"s3","name":"S3","escalationPolicy":"ladder"}`,
	}
	for target, body := range writes {
		if got := do(t, handler, http.MethodPost, target, readToken, body).Code; got != http.StatusUnauthorized {
			t.Fatalf("%s must reject the read token, got %d", target, got)
		}
	}
	for _, target := range []string{"/v1/incidents/x/acknowledge", "/v1/incidents/x/resolve"} {
		if got := do(t, handler, http.MethodPost, target, readToken, "").Code; got != http.StatusUnauthorized {
			t.Fatalf("%s must reject the read token, got %d", target, got)
		}
	}
	if got := do(t, handler, http.MethodGet, "/v1/incidents", "", "").Code; got != http.StatusUnauthorized {
		t.Fatalf("an unauthenticated read must be rejected, got %d", got)
	}
}

func TestManagementAcknowledgeAndResolveDriveTheLifecycle(t *testing.T) {
	handler, store, _ := newHarness(t)
	routingKey := seedCatalog(t, handler)
	event := `{"routing_key":"` + routingKey + `","event_action":"trigger","dedup_key":"k","payload":{"summary":"s","source":"p","severity":"error"}}`
	if got := do(t, handler, http.MethodPost, "/v2/enqueue", "", event).Code; got != http.StatusAccepted {
		t.Fatalf("ingest: %d", got)
	}
	current, err := store.OpenIncident(context.Background(), "checkout", "k")
	if err != nil {
		t.Fatal(err)
	}
	acked := do(t, handler, http.MethodPost, "/v1/incidents/"+current.ID+"/acknowledge", adminToken, "")
	if acked.Code != http.StatusOK {
		t.Fatalf("acknowledge: %d %s", acked.Code, acked.Body.String())
	}
	body := decode(t, acked)
	if body["state"] != "acknowledged" {
		t.Fatalf("expected acknowledged, got %#v", body)
	}
	if _, armed := body["escalateAt"]; armed {
		t.Fatalf("an acknowledged incident must not report an armed timer: %#v", body)
	}
	resolved := do(t, handler, http.MethodPost, "/v1/incidents/"+current.ID+"/resolve", adminToken, "")
	if resolved.Code != http.StatusOK {
		t.Fatalf("resolve: %d", resolved.Code)
	}
	if decode(t, resolved)["state"] != "resolved" {
		t.Fatal("expected resolved")
	}
	again := do(t, handler, http.MethodPost, "/v1/incidents/"+current.ID+"/resolve", adminToken, "")
	if again.Code != http.StatusConflict {
		t.Fatalf("resolving twice must conflict, got %d", again.Code)
	}
	missing := do(t, handler, http.MethodPost, "/v1/incidents/inc_missing/acknowledge", adminToken, "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("an unknown incident must be 404, got %d", missing.Code)
	}
}

func TestReadSurfaceExposesJournalAndPagesWithoutSecrets(t *testing.T) {
	handler, _, _ := newHarness(t)
	routingKey := seedCatalog(t, handler)
	event := `{"routing_key":"` + routingKey + `","event_action":"trigger","dedup_key":"k","payload":{"summary":"s","source":"p","severity":"error"}}`
	if got := do(t, handler, http.MethodPost, "/v2/enqueue", "", event).Code; got != http.StatusAccepted {
		t.Fatalf("ingest: %d", got)
	}
	listing := do(t, handler, http.MethodGet, "/v1/incidents", readToken, "")
	raw, err := io.ReadAll(listing.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "whsec_") {
		t.Fatal("the read surface must never expose a signing secret")
	}
	var incidents struct {
		Incidents []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"incidents"`
	}
	if err := json.Unmarshal(raw, &incidents); err != nil {
		t.Fatal(err)
	}
	if len(incidents.Incidents) != 1 {
		t.Fatalf("expected one incident, got %d", len(incidents.Incidents))
	}
	id := incidents.Incidents[0].ID
	events := do(t, handler, http.MethodGet, "/v1/incidents/"+id+"/events", readToken, "")
	if events.Code != http.StatusOK || !strings.Contains(events.Body.String(), `"kind":"opened"`) {
		t.Fatalf("the journal read is missing the opening row: %d %s", events.Code, events.Body.String())
	}
	pages := do(t, handler, http.MethodGet, "/v1/incidents/"+id+"/notifications", readToken, "")
	if pages.Code != http.StatusOK || !strings.Contains(pages.Body.String(), `"channel":"webhook"`) {
		t.Fatalf("the page read is missing the planned pages: %s", pages.Body.String())
	}
	if strings.Contains(pages.Body.String(), "http://127.0.0.1:19500") {
		t.Fatal("the page read must not expose destination URLs")
	}
	for _, target := range []string{"/v1/incidents/missing", "/v1/incidents/missing/events", "/v1/incidents/missing/notifications"} {
		if got := do(t, handler, http.MethodGet, target, readToken, "").Code; got != http.StatusNotFound {
			t.Fatalf("%s must be 404, got %d", target, got)
		}
	}
	if got := do(t, handler, http.MethodGet, "/v1/attempts?limit=0", readToken, "").Code; got != http.StatusBadRequest {
		t.Fatalf("a bad limit must be 400, got %d", got)
	}
}

func TestOnCallReadResolvesTheDeclaredSchedule(t *testing.T) {
	handler, _, _ := newHarness(t)
	seedCatalog(t, handler)
	recorder := do(t, handler, http.MethodGet, "/v1/schedules/primary/on-call?at=2026-01-05T10:00:00Z", readToken, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("on-call: %d %s", recorder.Code, recorder.Body.String())
	}
	body := decode(t, recorder)
	if body["onCall"] != true || body["responder"] != "alice" {
		t.Fatalf("unexpected on-call answer %#v", body)
	}
	before := do(t, handler, http.MethodGet, "/v1/schedules/primary/on-call?at=2020-01-01T00:00:00Z", readToken, "")
	if decode(t, before)["onCall"] != false {
		t.Fatal("nobody is on call before the layer starts")
	}
	if got := do(t, handler, http.MethodGet, "/v1/schedules/primary/on-call?at=yesterday", readToken, "").Code; got != http.StatusBadRequest {
		t.Fatalf("a bad instant must be 400, got %d", got)
	}
	if got := do(t, handler, http.MethodGet, "/v1/schedules/missing/on-call", readToken, "").Code; got != http.StatusNotFound {
		t.Fatalf("an unknown schedule must be 404, got %d", got)
	}
}

func TestManagementRejectsMalformedBodiesAndDuplicateRegistrations(t *testing.T) {
	handler, _, _ := newHarness(t)
	seedCatalog(t, handler)
	duplicate := `{"id":"alice","email":"alice@example.test"}`
	if got := do(t, handler, http.MethodPost, "/v1/responders", adminToken, duplicate).Code; got != http.StatusConflict {
		t.Fatalf("a duplicate responder must be 409, got %d", got)
	}
	unknownField := `{"id":"dave","email":"dave@example.test","pager":"nokia"}`
	if got := do(t, handler, http.MethodPost, "/v1/responders", adminToken, unknownField).Code; got != http.StatusBadRequest {
		t.Fatalf("an unknown management field must be 400, got %d", got)
	}
	contactless := `{"id":"eve"}`
	if got := do(t, handler, http.MethodPost, "/v1/responders", adminToken, contactless).Code; got != http.StatusBadRequest {
		t.Fatalf("a responder with no channel must be 400, got %d", got)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/responders", strings.NewReader(`{"id":"f"}`))
	request.Header.Set("Authorization", "Bearer "+adminToken)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("a missing content type must be 415, got %d", recorder.Code)
	}
	badSchedule := `{"id":"broken","name":"Broken","layers":[{"name":"l","start":"2026-01-05T09:00:00Z","rotation":"1s","responders":["alice"]}]}`
	if got := do(t, handler, http.MethodPost, "/v1/schedules", adminToken, badSchedule).Code; got != http.StatusBadRequest {
		t.Fatalf("an invalid rotation must be 400, got %d", got)
	}
}

func TestServerRejectsAuthorityCollapse(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "incidents.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	desk, err := incident.NewDesk(store, "operator", time.Now, incident.RandomID, incident.RandomSecret)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func() (*Server, error){
		"no desk":     func() (*Server, error) { return New(nil, store, "a", "b") },
		"no reader":   func() (*Server, error) { return New(desk, nil, "a", "b") },
		"blank admin": func() (*Server, error) { return New(desk, store, "  ", "b") },
		"blank read":  func() (*Server, error) { return New(desk, store, "a", "  ") },
		"same tokens": func() (*Server, error) { return New(desk, store, "same", "same") },
	}
	for name, build := range cases {
		if _, err := build(); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
}

func TestHealthNeedsNoCredential(t *testing.T) {
	handler, _, _ := newHarness(t)
	recorder := do(t, handler, http.MethodGet, "/healthz", "", "")
	if recorder.Code != http.StatusOK || decode(t, recorder)["status"] != "ok" {
		t.Fatalf("unexpected health response %d", recorder.Code)
	}
}
