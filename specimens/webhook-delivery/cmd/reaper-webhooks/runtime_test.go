package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/webhook-delivery/internal/api"
	"github.com/itsHabib/saas-reaper/specimens/webhook-delivery/internal/delivery"
	"github.com/itsHabib/saas-reaper/specimens/webhook-delivery/internal/store/sqlite"
	"github.com/itsHabib/saas-reaper/specimens/webhook-delivery/internal/transport/httpdelivery"
)

func TestFailedSendAuditRedactsManagementEndpointURL(t *testing.T) {
	fixture := newRedactionRuntime(t)
	deliverFailedAttempt(t, fixture.dispatcher)
	response := readAudit(t, fixture.service, fixture.store, fixture.messageID)
	assertRedactedAttempt(t, response)
}

type redactionRuntime struct {
	service    *delivery.Service
	store      *sqlite.Store
	dispatcher *delivery.Dispatcher
	messageID  string
}

func newRedactionRuntime(t *testing.T) redactionRuntime {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "webhooks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	at := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	sequence := 0
	ids := func(prefix string) (string, error) {
		sequence++
		return prefix + strconv.Itoa(sequence), nil
	}
	service, err := delivery.NewService(
		store,
		"configured-admin",
		func() time.Time { return at },
		ids,
		func() (string, error) {
			return "whsec_MDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDA=", nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RegisterEndpoint(t.Context(), "http://127.0.0.1:0/hook?token=sentinel"); err != nil {
		t.Fatal(err)
	}
	publication, err := service.Publish(t.Context(), []byte(`{"event":"invoice.created"}`))
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := delivery.NewSchedule(nil)
	if err != nil {
		t.Fatal(err)
	}
	sender, err := httpdelivery.New(time.Second, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := delivery.NewDispatcher(
		store,
		sender,
		schedule,
		func() time.Time { return at },
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatal(err)
	}
	return redactionRuntime{
		service: service, store: store, dispatcher: dispatcher, messageID: publication.MessageID,
	}
}

func deliverFailedAttempt(t *testing.T, dispatcher *delivery.Dispatcher) {
	t.Helper()
	count, err := dispatcher.DeliverDue(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("delivery count = %d, want 1", count)
	}
}

func readAudit(
	t *testing.T,
	service *delivery.Service,
	store *sqlite.Store,
	messageID string,
) *httptest.ResponseRecorder {
	t.Helper()
	server, err := api.New(service, store, "management-token", "audit-read-token")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/v1/attempts?messageId="+messageID,
		nil,
	)
	request.Header.Set("Authorization", "Bearer audit-read-token")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body.String())
	}
	return response
}

func assertRedactedAttempt(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if strings.Contains(response.Body.String(), "sentinel") || strings.Contains(response.Body.String(), "/hook") {
		t.Fatalf("audit-read response exposed management endpoint URL: %s", response.Body.String())
	}
	var audit struct {
		Attempts []struct {
			Error string `json:"error"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &audit); err != nil {
		t.Fatal(err)
	}
	if len(audit.Attempts) != 1 || audit.Attempts[0].Error == "" {
		t.Fatalf("failed-send audit = %#v, want one redacted error", audit.Attempts)
	}
}
