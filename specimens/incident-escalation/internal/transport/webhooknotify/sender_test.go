package webhooknotify

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"

	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/incident"
)

const testSecret = "whsec_" + "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA="

func testMessage(url string) incident.Message {
	return incident.Message{
		NotificationID: "ntf_0001",
		Kind:           incident.EventEscalated,
		Responder: incident.Responder{
			ID:            "alice",
			WebhookURL:    url,
			WebhookSecret: testSecret,
		},
		Incident: incident.Incident{
			ID:       "inc_0001",
			DedupKey: "checkout-5xx",
			State:    incident.StateTriggered,
			Summary:  "checkout is down",
			Source:   "prometheus",
			Severity: incident.SeverityCritical,
			Level:    1,
			OpenedAt: time.Unix(1772450000, 0).UTC(),
		},
		ServiceName: "Checkout",
		SentAt:      time.Now(),
	}
}

// The official Standard Webhooks Go verifier is the referee for the signing bytes.
func TestOfficialVerifierAcceptsTheSignedPageAndRejectsTampering(t *testing.T) {
	verifier, err := standardwebhooks.NewWebhook(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	var verified bool
	var tamperedRejected bool
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Error(readErr)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		verified = verifier.Verify(body, r.Header) == nil
		tampered := r.Header.Clone()
		tampered.Set("webhook-signature", tamperOne(r.Header.Get("webhook-signature")))
		tamperedRejected = verifier.Verify(body, tampered) != nil
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	sender, err := New(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.Notify(context.Background(), testMessage(server.URL)); err != nil {
		t.Fatalf("a 204 must be delivery, got %v", err)
	}
	if !verified {
		t.Fatal("the official verifier rejected a genuine page")
	}
	if !tamperedRejected {
		t.Fatal("the official verifier accepted a tampered signature")
	}
	if payload["type"] != "incident.escalated" || payload["notificationId"] != "ntf_0001" {
		t.Fatalf("unexpected page body %#v", payload)
	}
	body, _ := payload["incident"].(map[string]any)
	if body["dedupKey"] != "checkout-5xx" || body["severity"] != "critical" || body["level"] != float64(1) {
		t.Fatalf("the page must carry the incident context: %#v", body)
	}
}

func TestStatusClassificationSeparatesRetryFromPermanent(t *testing.T) {
	statuses := map[int]string{
		200: "delivered",
		202: "delivered",
		204: "delivered",
		400: "permanent",
		401: "permanent",
		404: "permanent",
		410: "permanent",
		408: "retry",
		429: "retry",
		500: "retry",
		503: "retry",
	}
	for status, want := range statuses {
		current := status
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(current)
		}))
		sender, err := New(5 * time.Second)
		if err != nil {
			t.Fatal(err)
		}
		err = sender.Notify(context.Background(), testMessage(server.URL))
		server.Close()
		switch want {
		case "delivered":
			if err != nil {
				t.Fatalf("status %d must be delivery, got %v", status, err)
			}
		case "permanent":
			if !errors.Is(err, incident.ErrPermanent) {
				t.Fatalf("status %d must be permanent, got %v", status, err)
			}
		case "retry":
			if err == nil || errors.Is(err, incident.ErrPermanent) {
				t.Fatalf("status %d must be retryable, got %v", status, err)
			}
		}
	}
}

func TestTransportFailureIsRetryableAndBadSecretIsPermanent(t *testing.T) {
	sender, err := New(500 * time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	unreachable := testMessage("http://127.0.0.1:1/page")
	err = sender.Notify(context.Background(), unreachable)
	if err == nil || errors.Is(err, incident.ErrPermanent) {
		t.Fatalf("a refused connection must be retryable, got %v", err)
	}
	badSecret := testMessage("http://127.0.0.1:1/page")
	badSecret.Responder.WebhookSecret = "not-a-standard-webhooks-secret"
	if err := sender.Notify(context.Background(), badSecret); !errors.Is(err, incident.ErrPermanent) {
		t.Fatalf("an unusable secret must be permanent, got %v", err)
	}
	shortKey := testMessage("http://127.0.0.1:1/page")
	shortKey.Responder.WebhookSecret = "whsec_" + base64.StdEncoding.EncodeToString([]byte("short"))
	if err := sender.Notify(context.Background(), shortKey); !errors.Is(err, incident.ErrPermanent) {
		t.Fatalf("a short key must be permanent, got %v", err)
	}
	dotted := testMessage("http://127.0.0.1:1/page")
	dotted.NotificationID = "ntf.0001"
	if err := sender.Notify(context.Background(), dotted); !errors.Is(err, incident.ErrPermanent) {
		t.Fatalf("a webhook id containing a full stop must be permanent, got %v", err)
	}
	if _, err := New(0); err == nil {
		t.Fatal("a zero timeout must be rejected")
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	var reached bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	sender, err := New(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	err = sender.Notify(context.Background(), testMessage(redirector.URL))
	if !errors.Is(err, incident.ErrPermanent) {
		t.Fatalf("a redirect is a permanent misconfiguration, got %v", err)
	}
	if reached {
		t.Fatal("the sender must not follow a redirect")
	}
}

func tamperOne(signature string) string {
	body := []byte(signature)
	index := len(body) - 3
	if body[index] == 'A' {
		body[index] = 'B'
		return string(body)
	}
	body[index] = 'A'
	return string(body)
}
