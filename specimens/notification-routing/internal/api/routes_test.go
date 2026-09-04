package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/notification-routing/internal/routing"
)

const (
	apiAdminToken = "management-token"
	apiReadToken  = "audit-read-token"
)

type apiTestStore struct {
	channels     []routing.Channel
	templates    []routing.Template
	recipients   map[string]routing.Recipient
	notification routing.Notification
	deliveries   []routing.Delivery
	attempts     []routing.Attempt
	sendCalls    int
	attemptCalls int
}

func (s *apiTestStore) RegisterChannel(_ context.Context, channel routing.Channel) error {
	s.channels = append(s.channels, channel)
	return nil
}

func (s *apiTestStore) DisableChannel(_ context.Context, id string, expected int64, at time.Time) (routing.Channel, error) {
	for index := range s.channels {
		if s.channels[index].ID != id {
			continue
		}
		if s.channels[index].Revision != expected {
			return routing.Channel{}, routing.ErrConflict
		}
		s.channels[index].Enabled = false
		s.channels[index].Revision++
		s.channels[index].UpdatedAt = at
		return s.channels[index], nil
	}
	return routing.Channel{}, routing.ErrNotFound
}

func (s *apiTestStore) ListChannels(context.Context) ([]routing.Channel, error) {
	return append([]routing.Channel(nil), s.channels...), nil
}

func (s *apiTestStore) CreateTemplate(_ context.Context, template routing.Template) error {
	s.templates = append(s.templates, template)
	return nil
}

func (s *apiTestStore) Templates(_ context.Context, key string) ([]routing.Template, error) {
	var variants []routing.Template
	for _, template := range s.templates {
		if template.Key == key {
			variants = append(variants, template)
		}
	}
	return variants, nil
}

func (s *apiTestStore) CreateRecipient(_ context.Context, recipient routing.Recipient) error {
	if s.recipients == nil {
		s.recipients = map[string]routing.Recipient{}
	}
	s.recipients[recipient.ID] = recipient
	return nil
}

func (s *apiTestStore) Recipient(_ context.Context, id string) (routing.Recipient, error) {
	recipient, ok := s.recipients[id]
	if !ok {
		return routing.Recipient{}, routing.ErrNotFound
	}
	return recipient, nil
}

func (s *apiTestStore) Send(_ context.Context, notification routing.Notification, deliveries []routing.Delivery) (routing.Acceptance, error) {
	s.sendCalls++
	if s.notification.IdempotencyKey == notification.IdempotencyKey {
		acceptance := routing.Acceptance{NotificationID: s.notification.ID, Deduplicated: true}
		for _, item := range s.deliveries {
			acceptance.Deliveries = append(acceptance.Deliveries, routing.QueuedDelivery{ID: item.ID, ChannelID: item.ChannelID})
		}
		return acceptance, nil
	}
	s.notification = notification
	s.deliveries = append([]routing.Delivery(nil), deliveries...)
	acceptance := routing.Acceptance{NotificationID: notification.ID}
	for _, item := range deliveries {
		acceptance.Deliveries = append(acceptance.Deliveries, routing.QueuedDelivery{ID: item.ID, ChannelID: item.ChannelID})
	}
	return acceptance, nil
}

func (s *apiTestStore) Attempts(context.Context, routing.AttemptFilter, int) ([]routing.Attempt, error) {
	s.attemptCalls++
	return append([]routing.Attempt(nil), s.attempts...), nil
}

func newAPITestHandler(t *testing.T) (http.Handler, *apiTestStore) {
	t.Helper()
	store := &apiTestStore{}
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	sequence := 0
	ids := func(prefix string) (string, error) {
		sequence++
		return fmt.Sprintf("%s%d", prefix, sequence), nil
	}
	service, err := routing.NewService(store, "configured-admin", func() time.Time { return now }, ids)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(service, store, apiAdminToken, apiReadToken)
	if err != nil {
		t.Fatal(err)
	}
	return server.Handler(), store
}

func serveAPIRequest(handler http.Handler, method, path, token string, body io.Reader) *httptest.ResponseRecorder {
	request := httptest.NewRequestWithContext(context.Background(), method, path, body)
	request.Header.Set("Authorization", "Bearer "+token)
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func mustStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, want, response.Body.String())
	}
}

func seedRouting(t *testing.T, handler http.Handler) {
	t.Helper()
	for _, body := range []string{
		`{"id":"email","kind":"smtp"}`,
		`{"id":"chat","kind":"slack-webhook"}`,
	} {
		mustStatus(t, serveAPIRequest(handler, http.MethodPost, "/v1/channels", apiAdminToken, strings.NewReader(body)), http.StatusCreated)
	}
	for _, body := range []string{
		`{"key":"invoice-paid","channel":"email","subject":"Invoice {{invoice.id}}","body":"Paid {{invoice.amount}}"}`,
		`{"key":"invoice-paid","channel":"chat","body":"{{customer}} paid {{invoice.amount}}"}`,
	} {
		mustStatus(t, serveAPIRequest(handler, http.MethodPost, "/v1/templates", apiAdminToken, strings.NewReader(body)), http.StatusCreated)
	}
	recipient := `{"id":"cus_acme","channels":[
		{"channel":"email","address":"billing@acme.example"},
		{"channel":"chat","address":"http://127.0.0.1:19402/services/T/B/x","enabled":false}]}`
	mustStatus(t, serveAPIRequest(handler, http.MethodPost, "/v1/recipients", apiAdminToken, strings.NewReader(recipient)), http.StatusCreated)
}

func TestHandlerSeparatesManagementAndAuditReadTokens(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		token      string
		body       string
		wantStatus int
		wantReads  int
	}{
		{name: "read token cannot manage", method: http.MethodPost, path: "/v1/channels", token: apiReadToken, body: `{"id":"email","kind":"smtp"}`, wantStatus: http.StatusUnauthorized},
		{name: "read token cannot send", method: http.MethodPost, path: "/v1/notifications", token: apiReadToken, body: `{}`, wantStatus: http.StatusUnauthorized},
		{name: "management token cannot read audit", method: http.MethodGet, path: "/v1/attempts", token: apiAdminToken, wantStatus: http.StatusUnauthorized},
		{name: "management token manages", method: http.MethodPost, path: "/v1/channels", token: apiAdminToken, body: `{"id":"email","kind":"smtp"}`, wantStatus: http.StatusCreated},
		{name: "read token reads audit", method: http.MethodGet, path: "/v1/attempts", token: apiReadToken, wantStatus: http.StatusOK, wantReads: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, store := newAPITestHandler(t)
			response := serveAPIRequest(handler, test.method, test.path, test.token, strings.NewReader(test.body))
			mustStatus(t, response, test.wantStatus)
			if store.attemptCalls != test.wantReads {
				t.Fatalf("attempt reads = %d, want %d", store.attemptCalls, test.wantReads)
			}
		})
	}
}

func TestNewRejectsCollapsedAuthorityTokens(t *testing.T) {
	store := &apiTestStore{}
	service, err := routing.NewService(store, "configured-admin", time.Now, routing.RandomID)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"shared", "shared"}, {"", apiReadToken}, {apiAdminToken, ""}} {
		if _, err := New(service, store, pair[0], pair[1]); err == nil {
			t.Fatalf("tokens %q/%q were accepted", pair[0], pair[1])
		}
	}
}

func TestSendRendersFansOutAndDeduplicates(t *testing.T) {
	handler, store := newAPITestHandler(t)
	seedRouting(t, handler)
	body := `{"template":"invoice-paid","recipient":"cus_acme","idempotencyKey":"inv-1","payload":{"customer":"Acme","invoice":{"id":"inv_1","amount":4200}}}`
	first := serveAPIRequest(handler, http.MethodPost, "/v1/notifications", apiAdminToken, strings.NewReader(body))
	mustStatus(t, first, http.StatusAccepted)
	var accepted acceptanceResponse
	if err := json.Unmarshal(first.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.Deduplicated || len(accepted.Deliveries) != 1 || accepted.Deliveries[0].Channel != "email" {
		t.Fatalf("acceptance = %#v, want one email delivery for the enabled binding", accepted)
	}
	if store.deliveries[0].Subject != "Invoice inv_1" || store.deliveries[0].Body != "Paid 4200" || store.notification.Actor != "configured-admin" {
		t.Fatalf("delivery = %#v, notification = %#v", store.deliveries[0], store.notification)
	}
	second := serveAPIRequest(handler, http.MethodPost, "/v1/notifications", apiAdminToken, strings.NewReader(body))
	mustStatus(t, second, http.StatusOK)
	var repeated acceptanceResponse
	if err := json.Unmarshal(second.Body.Bytes(), &repeated); err != nil {
		t.Fatal(err)
	}
	if !repeated.Deduplicated || repeated.NotificationID != accepted.NotificationID {
		t.Fatalf("repeat acceptance = %#v, want deduplicated %s", repeated, accepted.NotificationID)
	}
}

func TestSendRejectsMissingVariableAndCallerSelectedActor(t *testing.T) {
	handler, store := newAPITestHandler(t)
	seedRouting(t, handler)
	missing := `{"template":"invoice-paid","recipient":"cus_acme","idempotencyKey":"inv-2","payload":{"invoice":{"id":"inv_2"}}}`
	response := serveAPIRequest(handler, http.MethodPost, "/v1/notifications", apiAdminToken, strings.NewReader(missing))
	mustStatus(t, response, http.StatusBadRequest)
	if !strings.Contains(response.Body.String(), `\"invoice.amount\" is missing`) || store.sendCalls != 0 {
		t.Fatalf("body = %s, send calls = %d", response.Body.String(), store.sendCalls)
	}
	actor := `{"template":"invoice-paid","recipient":"cus_acme","idempotencyKey":"inv-3","payload":{},"actor":"caller"}`
	response = serveAPIRequest(handler, http.MethodPost, "/v1/notifications", apiAdminToken, strings.NewReader(actor))
	mustStatus(t, response, http.StatusBadRequest)
	if !strings.Contains(response.Body.String(), `unknown field \"actor\"`) {
		t.Fatalf("body = %s", response.Body.String())
	}
	wrongType := serveAPIRequest(handler, http.MethodPost, "/v1/notifications", apiAdminToken, strings.NewReader(missing))
	wrongType.Header().Set("Content-Type", "text/plain")
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/notifications", strings.NewReader(missing))
	request.Header.Set("Authorization", "Bearer "+apiAdminToken)
	request.Header.Set("Content-Type", "text/plain")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	mustStatus(t, recorder, http.StatusUnsupportedMediaType)
}

func TestAttemptReadNeverExposesAddresses(t *testing.T) {
	handler, store := newAPITestHandler(t)
	next := time.Date(2026, time.September, 1, 12, 5, 0, 0, time.UTC)
	store.attempts = []routing.Attempt{{
		Sequence: 1, DeliveryID: "del_1", NotificationID: "ntf_1", RecipientID: "cus_acme", ChannelID: "email",
		Actor: "configured-admin", Number: 1, Outcome: routing.OutcomeRetrying, State: routing.StatePending,
		Code: 451, Error: "smtp transport failure", AttemptedAt: next.Add(-time.Minute), NextAttemptAt: next,
	}}
	response := serveAPIRequest(handler, http.MethodGet, "/v1/attempts?notificationId=ntf_1&limit=5", apiReadToken, nil)
	mustStatus(t, response, http.StatusOK)
	body := response.Body.String()
	if strings.Contains(body, "acme.example") || strings.Contains(body, "address") {
		t.Fatalf("audit exposed address data: %s", body)
	}
	if !strings.Contains(body, `"nextAttemptAt":"2026-09-01T12:05:00Z"`) || !strings.Contains(body, `"state":"pending"`) {
		t.Fatalf("audit body = %s", body)
	}
	mustStatus(t, serveAPIRequest(handler, http.MethodGet, "/v1/attempts?limit=0", apiReadToken, nil), http.StatusBadRequest)
}

func TestChannelDisableReturnsRevisionAndConflict(t *testing.T) {
	handler, _ := newAPITestHandler(t)
	seedRouting(t, handler)
	disable := serveAPIRequest(handler, http.MethodPost, "/v1/channels/chat/disable", apiAdminToken, strings.NewReader(`{"expectedRevision":1}`))
	mustStatus(t, disable, http.StatusOK)
	if !strings.Contains(disable.Body.String(), `"enabled":false`) || !strings.Contains(disable.Body.String(), `"revision":2`) {
		t.Fatalf("disable body = %s", disable.Body.String())
	}
	stale := serveAPIRequest(handler, http.MethodPost, "/v1/channels/chat/disable", apiAdminToken, strings.NewReader(`{"expectedRevision":1}`))
	mustStatus(t, stale, http.StatusConflict)
	missing := serveAPIRequest(handler, http.MethodPost, "/v1/channels/sms/disable", apiAdminToken, strings.NewReader(`{"expectedRevision":1}`))
	mustStatus(t, missing, http.StatusNotFound)
}
