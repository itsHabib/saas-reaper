package slackwebhook

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/notification-routing/internal/routing"
)

func testEnvelope(address string) routing.Envelope {
	return routing.Envelope{
		DeliveryID:     "del_one",
		NotificationID: "ntf_one",
		Address:        address,
		Body:           "Acme paid 4200 &lt;usd&gt;",
		Attempt:        1,
		AttemptedAt:    time.Unix(1, 0),
	}
}

func TestDeliverPostsTextPayloadAndClassifiesStatuses(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		wantErr   bool
		permanent bool
	}{
		{name: "ok", status: http.StatusOK},
		{name: "no text is permanent", status: http.StatusBadRequest, wantErr: true, permanent: true},
		{name: "channel archived is permanent", status: http.StatusGone, wantErr: true, permanent: true},
		{name: "rate limited is transient", status: http.StatusTooManyRequests, wantErr: true},
		{name: "server error is transient", status: http.StatusServiceUnavailable, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var body atomic.Pointer[map[string]any]
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var decoded map[string]any
				if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
					http.Error(w, "invalid_payload", http.StatusBadRequest)
					return
				}
				body.Store(&decoded)
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, "ok")
			}))
			defer server.Close()
			sender, err := New(time.Second)
			if err != nil {
				t.Fatal(err)
			}
			receipt, err := sender.Deliver(context.Background(), testEnvelope(server.URL+"/services/T0/B0/x"))
			if (err != nil) != test.wantErr || errors.Is(err, routing.ErrPermanent) != test.permanent {
				t.Fatalf("err = %v, want error %t permanent %t", err, test.wantErr, test.permanent)
			}
			if receipt.Code != test.status {
				t.Fatalf("receipt = %#v, want status %d", receipt, test.status)
			}
			decoded := body.Load()
			if decoded == nil || len(*decoded) != 1 || (*decoded)["text"] != "Acme paid 4200 &lt;usd&gt;" {
				t.Fatalf("posted payload = %#v, want exactly {\"text\": body}", decoded)
			}
		})
	}
}

func TestDeliverRefusesRedirects(t *testing.T) {
	var targetCalls atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/target" {
			targetCalls.Add(1)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Location", server.URL+"/target")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	sender, err := New(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := sender.Deliver(context.Background(), testEnvelope(server.URL+"/redirect"))
	if !errors.Is(err, routing.ErrPermanent) || receipt.Code != http.StatusTemporaryRedirect {
		t.Fatalf("redirect err/receipt = %v/%#v, want permanent 307", err, receipt)
	}
	if targetCalls.Load() != 0 {
		t.Fatalf("redirect target calls = %d, want 0", targetCalls.Load())
	}
}

func TestDeliverRedactsDestinationFromNetworkErrors(t *testing.T) {
	sender, err := New(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = sender.Deliver(context.Background(), testEnvelope("http://127.0.0.1:0/services/secret-token"))
	if err == nil {
		t.Fatal("network send unexpectedly succeeded")
	}
	if errors.Is(err, routing.ErrPermanent) {
		t.Fatalf("network failure classified permanent: %v", err)
	}
	if strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("send error exposed destination: %q", err)
	}
	var networkError *net.OpError
	if !errors.As(err, &networkError) {
		t.Fatalf("send error did not preserve network cause: %v", err)
	}
}
