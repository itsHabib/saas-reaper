package httpdelivery

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/webhook-delivery/internal/delivery"
)

func TestSenderRedactsDestinationFromNetworkErrors(t *testing.T) {
	sender, err := New(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = sender.Send(
		t.Context(),
		"http://127.0.0.1:0/webhook?token=sentinel",
		[]byte(`{"event":"invoice.created"}`),
		delivery.Headers{MessageID: "msg_1", Timestamp: "1787947200", Signature: "v1,test"},
	)
	if err == nil {
		t.Fatal("network send unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), "sentinel") || strings.Contains(err.Error(), "/webhook") {
		t.Fatalf("send error exposed destination: %q", err)
	}
	var networkError *net.OpError
	if !errors.As(err, &networkError) {
		t.Fatalf("send error did not preserve network cause: %v", err)
	}
}

func TestSenderRefusesRedirects(t *testing.T) {
	var targetCalls atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/target" {
			targetCalls.Add(1)
			w.WriteHeader(http.StatusNoContent)
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
	result, err := sender.Send(
		context.Background(),
		server.URL+"/redirect",
		[]byte(`{"event":"invoice.created"}`),
		delivery.Headers{MessageID: "msg_1", Timestamp: "1787947200", Signature: "v1,test"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d", result.StatusCode, http.StatusTemporaryRedirect)
	}
	if targetCalls.Load() != 0 {
		t.Fatalf("redirect target calls = %d, want 0", targetCalls.Load())
	}
}

func TestSenderParsesRetryAfter(t *testing.T) {
	attemptedAt := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		retryAfter string
		want       time.Duration
	}{
		{name: "delay seconds", retryAfter: "7", want: 7 * time.Second},
		{name: "HTTP date", retryAfter: attemptedAt.Add(19 * time.Second).Format(http.TimeFormat), want: 19 * time.Second},
		{name: "past HTTP date", retryAfter: attemptedAt.Add(-time.Second).Format(http.TimeFormat), want: 0},
		{name: "invalid value", retryAfter: "tomorrow", want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", test.retryAfter)
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer server.Close()

			sender, err := New(time.Second)
			if err != nil {
				t.Fatal(err)
			}
			result, err := sender.Send(
				context.Background(),
				server.URL,
				[]byte(`{"event":"invoice.created"}`),
				delivery.Headers{
					MessageID: "msg_1",
					Timestamp: strconv.FormatInt(attemptedAt.Unix(), 10),
					Signature: "v1,test",
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if result.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d", result.StatusCode, http.StatusServiceUnavailable)
			}
			if result.RetryAfter != test.want {
				t.Fatalf("retry after = %s, want %s", result.RetryAfter, test.want)
			}
		})
	}
}
