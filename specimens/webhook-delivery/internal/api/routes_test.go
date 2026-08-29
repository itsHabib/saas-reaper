package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/webhook-delivery/internal/delivery"
)

const (
	apiAdminToken = "management-token"
	apiReadToken  = "audit-read-token"
	apiSecret     = "whsec_MDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDA="
)

var errBrokenRequestBody = errors.New("injected read failure")

type apiTestStore struct {
	endpoint      delivery.Endpoint
	message       delivery.Message
	deliveries    []delivery.Delivery
	attempts      []delivery.Attempt
	registerCalls int
	attemptCalls  int
}

func (s *apiTestStore) RegisterEndpoint(
	_ context.Context,
	endpoint delivery.Endpoint,
	_ int64,
) (delivery.Endpoint, error) {
	s.registerCalls++
	s.endpoint = endpoint
	return endpoint, nil
}

func (s *apiTestStore) DisableEndpoint(
	_ context.Context,
	id string,
	expectedRevision int64,
	now time.Time,
) (delivery.Endpoint, error) {
	if s.endpoint.ID != id {
		return delivery.Endpoint{}, delivery.ErrNotFound
	}
	if s.endpoint.Revision != expectedRevision {
		return delivery.Endpoint{}, delivery.ErrConflict
	}
	s.endpoint.Enabled = false
	s.endpoint.Revision++
	s.endpoint.UpdatedAt = now
	return s.endpoint, nil
}

func (s *apiTestStore) ListEndpoints(context.Context) ([]delivery.Endpoint, error) {
	if s.endpoint.ID == "" {
		return nil, nil
	}
	return []delivery.Endpoint{s.endpoint}, nil
}

func (s *apiTestStore) Publish(
	_ context.Context,
	message delivery.Message,
	deliveries []delivery.Delivery,
) error {
	s.message = message
	s.deliveries = append([]delivery.Delivery(nil), deliveries...)
	return nil
}

func (s *apiTestStore) Message(_ context.Context, id string) (delivery.Message, error) {
	if s.message.ID != id {
		return delivery.Message{}, delivery.ErrNotFound
	}
	return s.message, nil
}

func (s *apiTestStore) Endpoint(_ context.Context, id string) (delivery.Endpoint, error) {
	if s.endpoint.ID != id {
		return delivery.Endpoint{}, delivery.ErrNotFound
	}
	return s.endpoint, nil
}

func (s *apiTestStore) Replay(_ context.Context, item delivery.Delivery) error {
	s.deliveries = append(s.deliveries, item)
	return nil
}

func (s *apiTestStore) Attempts(
	_ context.Context,
	_ delivery.AttemptFilter,
	_ int,
) ([]delivery.Attempt, error) {
	s.attemptCalls++
	return append([]delivery.Attempt(nil), s.attempts...), nil
}

func TestHandlerSeparatesManagementAndAuditReadTokens(t *testing.T) {
	tests := []struct {
		name          string
		method        string
		path          string
		token         string
		body          string
		wantStatus    int
		wantRegisters int
		wantReads     int
	}{
		{
			name:       "read token cannot manage",
			method:     http.MethodPost,
			path:       "/v1/endpoints",
			token:      apiReadToken,
			body:       `{"url":"http://127.0.0.1:19001/hook"}`,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "management token cannot read audit",
			method:     http.MethodGet,
			path:       "/v1/attempts",
			token:      apiAdminToken,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:          "management token manages",
			method:        http.MethodPost,
			path:          "/v1/endpoints",
			token:         apiAdminToken,
			body:          `{"url":"http://127.0.0.1:19001/hook"}`,
			wantStatus:    http.StatusCreated,
			wantRegisters: 1,
		},
		{
			name:       "read token reads audit",
			method:     http.MethodGet,
			path:       "/v1/attempts",
			token:      apiReadToken,
			wantStatus: http.StatusOK,
			wantReads:  1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, store := newAPITestHandler(t)
			response := serveAPIRequest(handler, test.method, test.path, test.token, strings.NewReader(test.body))
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, test.wantStatus, response.Body.String())
			}
			if store.registerCalls != test.wantRegisters {
				t.Fatalf("register calls = %d, want %d", store.registerCalls, test.wantRegisters)
			}
			if store.attemptCalls != test.wantReads {
				t.Fatalf("attempt reads = %d, want %d", store.attemptCalls, test.wantReads)
			}
		})
	}
}

func TestNewRejectsCollapsedAuthorityTokens(t *testing.T) {
	store := &apiTestStore{}
	service, err := delivery.NewService(
		store,
		"configured-admin",
		time.Now,
		func(prefix string) (string, error) { return prefix + "1", nil },
		func() (string, error) { return apiSecret, nil },
		delivery.NewAttemptCoordinator(),
	)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		adminToken string
		readToken  string
		want       string
	}{
		{
			name:       "same token",
			adminToken: "shared-token",
			readToken:  "shared-token",
			want:       "management and audit-read tokens must differ",
		},
		{
			name:       "missing management token",
			adminToken: "",
			readToken:  apiReadToken,
			want:       "management and audit-read tokens are required",
		},
		{
			name:       "missing read token",
			adminToken: apiAdminToken,
			readToken:  "",
			want:       "management and audit-read tokens are required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := New(service, store, test.adminToken, test.readToken)
			if err == nil || err.Error() != test.want {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestManagementBodyRejectsCallerSelectedActor(t *testing.T) {
	handler, store := newAPITestHandler(t)
	body := `{"url":"http://127.0.0.1:19001/hook","actor":"caller-selected"}`
	response := serveAPIRequest(handler, http.MethodPost, "/v1/endpoints", apiAdminToken, strings.NewReader(body))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	want := "{\"error\":\"decode request: json: unknown field \\\"actor\\\"\"}\n"
	if response.Body.String() != want {
		t.Fatalf("body = %q, want %q", response.Body.String(), want)
	}
	if store.registerCalls != 0 {
		t.Fatalf("register calls = %d, want 0", store.registerCalls)
	}
}

func TestEndpointSecretIsReturnedOnceThenRedacted(t *testing.T) {
	handler, _ := newAPITestHandler(t)
	register := serveAPIRequest(
		handler,
		http.MethodPost,
		"/v1/endpoints",
		apiAdminToken,
		strings.NewReader(`{"url":"http://127.0.0.1:19001/hook"}`),
	)
	if register.Code != http.StatusCreated {
		t.Fatalf("register status = %d; body = %s", register.Code, register.Body.String())
	}
	var created endpointResponse
	if err := json.Unmarshal(register.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Secret != apiSecret {
		t.Fatalf("registration secret = %q, want generated secret", created.Secret)
	}

	disable := serveAPIRequest(
		handler,
		http.MethodPost,
		fmt.Sprintf("/v1/endpoints/%s/disable", created.ID),
		apiAdminToken,
		strings.NewReader(`{"expectedRevision":1}`),
	)
	if disable.Code != http.StatusOK {
		t.Fatalf("disable status = %d; body = %s", disable.Code, disable.Body.String())
	}
	var disabled map[string]json.RawMessage
	if err := json.Unmarshal(disable.Body.Bytes(), &disabled); err != nil {
		t.Fatal(err)
	}
	if _, exposed := disabled["secret"]; exposed {
		t.Fatalf("disable response exposed secret: %s", disable.Body.String())
	}
	if strings.Contains(disable.Body.String(), apiSecret) {
		t.Fatalf("disable response contains secret: %s", disable.Body.String())
	}
}

func TestPublishPreservesExactPayloadAndConfiguredActor(t *testing.T) {
	handler, store := newAPITestHandler(t)
	payload := "{\n  \"event\": \"invoice.created\", \"actor\": \"payload-value\"\n}\n"
	response := serveAPIRequest(handler, http.MethodPost, "/v1/messages", apiAdminToken, strings.NewReader(payload))
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusAccepted, response.Body.String())
	}
	if string(store.message.Payload) != payload {
		t.Fatalf("stored payload = %q, want exact %q", store.message.Payload, payload)
	}
	if store.message.Actor != "configured-admin" {
		t.Fatalf("stored actor = %q, want configured actor", store.message.Actor)
	}
}

func TestPublishReportsExactReadErrors(t *testing.T) {
	handler, _ := newAPITestHandler(t)
	tests := []struct {
		name string
		body io.ReadCloser
		want string
	}{
		{
			name: "reader failure",
			body: &brokenRequestBody{},
			want: "{\"error\":\"read payload: injected read failure\"}\n",
		},
		{
			name: "size bound",
			body: io.NopCloser(strings.NewReader(strings.Repeat("x", delivery.MaxPayloadBytes+1))),
			want: "{\"error\":\"read payload: http: request body too large\"}\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/messages", nil)
			request.Body = test.body
			request.Header.Set("Authorization", "Bearer "+apiAdminToken)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			if response.Body.String() != test.want {
				t.Fatalf("body = %q, want %q", response.Body.String(), test.want)
			}
		})
	}
}

type brokenRequestBody struct {
	read bool
}

func (b *brokenRequestBody) Read(destination []byte) (int, error) {
	if b.read {
		return 0, errBrokenRequestBody
	}
	b.read = true
	return copy(destination, "{"), nil
}

func (*brokenRequestBody) Close() error {
	return nil
}

func newAPITestHandler(t *testing.T) (http.Handler, *apiTestStore) {
	t.Helper()
	store := &apiTestStore{}
	now := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	sequence := 0
	ids := func(prefix string) (string, error) {
		sequence++
		return fmt.Sprintf("%s%d", prefix, sequence), nil
	}
	service, err := delivery.NewService(store, "configured-admin", func() time.Time {
		return now
	}, ids, func() (string, error) {
		return apiSecret, nil
	}, delivery.NewAttemptCoordinator())
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(service, store, apiAdminToken, apiReadToken)
	if err != nil {
		t.Fatal(err)
	}
	return server.Handler(), store
}

func serveAPIRequest(
	handler http.Handler,
	method string,
	path string,
	token string,
	body io.Reader,
) *httptest.ResponseRecorder {
	request := httptest.NewRequestWithContext(context.Background(), method, path, body)
	request.Header.Set("Authorization", "Bearer "+token)
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
