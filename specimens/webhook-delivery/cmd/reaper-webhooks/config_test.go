package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/webhook-delivery/internal/delivery"
)

func TestLoadConfigRequiresEveryAuthorityVariable(t *testing.T) {
	required := []string{
		"REAPER_WEBHOOK_ADMIN_TOKEN",
		"REAPER_WEBHOOK_ADMIN_ACTOR",
		"REAPER_WEBHOOK_READ_TOKEN",
	}
	for _, missing := range required {
		t.Run(missing, func(t *testing.T) {
			clearWebhookEnvironment(t)
			for _, name := range required {
				t.Setenv(name, "configured")
			}
			t.Setenv(missing, "")
			_, err := loadConfig()
			want := "REAPER_WEBHOOK_ADMIN_TOKEN, REAPER_WEBHOOK_ADMIN_ACTOR, and REAPER_WEBHOOK_READ_TOKEN are required"
			if err == nil || err.Error() != want {
				t.Fatalf("error = %v, want %q", err, want)
			}
		})
	}
}

func TestLoadConfigParsesRetryAndClockSettings(t *testing.T) {
	clearWebhookEnvironment(t)
	t.Setenv("REAPER_WEBHOOK_ADMIN_TOKEN", "management-token")
	t.Setenv("REAPER_WEBHOOK_ADMIN_ACTOR", "operator@example.test")
	t.Setenv("REAPER_WEBHOOK_READ_TOKEN", "audit-read-token")
	t.Setenv("REAPER_WEBHOOK_RETRY_DELAYS", "1s,250ms,2m")
	t.Setenv("REAPER_WEBHOOK_POLL_INTERVAL", "75ms")
	t.Setenv("REAPER_WEBHOOK_REQUEST_TIMEOUT", "3s")

	configuration, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	wantDelays := []time.Duration{time.Second, 250 * time.Millisecond, 2 * time.Minute}
	if !slices.Equal(configuration.retryDelays, wantDelays) {
		t.Fatalf("retry delays = %v, want %v", configuration.retryDelays, wantDelays)
	}
	if configuration.pollInterval != 75*time.Millisecond {
		t.Fatalf("poll interval = %s, want 75ms", configuration.pollInterval)
	}
	if configuration.requestTimeout != 3*time.Second {
		t.Fatalf("request timeout = %s, want 3s", configuration.requestTimeout)
	}
}

func TestRetryDelaysUseIndependentDefaults(t *testing.T) {
	delays, err := retryDelays("")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(delays, delivery.DefaultRetryDelays) {
		t.Fatalf("retry delays = %v, want defaults %v", delays, delivery.DefaultRetryDelays)
	}
	original := delivery.DefaultRetryDelays[0]
	delays[0] = time.Nanosecond
	if delivery.DefaultRetryDelays[0] != original {
		t.Fatal("default retry schedule aliases caller-owned slice")
	}
}

func TestRetryDelaysRejectMalformedValues(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "padded",
			raw:  "1s, 2s",
			want: "REAPER_WEBHOOK_RETRY_DELAYS must contain unpadded positive durations",
		},
		{
			name: "zero",
			raw:  "0s",
			want: "REAPER_WEBHOOK_RETRY_DELAYS durations must be positive",
		},
		{
			name: "negative",
			raw:  "-1s",
			want: "REAPER_WEBHOOK_RETRY_DELAYS durations must be positive",
		},
		{
			name: "trailing separator",
			raw:  "1s,",
			want: "REAPER_WEBHOOK_RETRY_DELAYS must contain unpadded positive durations",
		},
		{
			name: "invalid duration",
			raw:  "soon",
			want: "parse REAPER_WEBHOOK_RETRY_DELAYS:",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := retryDelays(test.raw)
			if err == nil || !strings.HasPrefix(err.Error(), test.want) {
				t.Fatalf("error = %v, want prefix %q", err, test.want)
			}
		})
	}
}

func clearWebhookEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"REAPER_WEBHOOK_ADDR",
		"REAPER_WEBHOOK_DB",
		"REAPER_WEBHOOK_ADMIN_TOKEN",
		"REAPER_WEBHOOK_ADMIN_ACTOR",
		"REAPER_WEBHOOK_READ_TOKEN",
		"REAPER_WEBHOOK_RETRY_DELAYS",
		"REAPER_WEBHOOK_POLL_INTERVAL",
		"REAPER_WEBHOOK_REQUEST_TIMEOUT",
	} {
		t.Setenv(name, "")
	}
}
