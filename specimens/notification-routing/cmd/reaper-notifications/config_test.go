package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/notification-routing/internal/routing"
)

var configVariables = []string{
	"REAPER_NOTIFY_ADDR",
	"REAPER_NOTIFY_DB",
	"REAPER_NOTIFY_ADMIN_TOKEN",
	"REAPER_NOTIFY_ADMIN_ACTOR",
	"REAPER_NOTIFY_READ_TOKEN",
	"REAPER_NOTIFY_SMTP_ADDR",
	"REAPER_NOTIFY_SMTP_FROM",
	"REAPER_NOTIFY_SMTP_USERNAME",
	"REAPER_NOTIFY_SMTP_PASSWORD",
	"REAPER_NOTIFY_RETRY_DELAYS",
	"REAPER_NOTIFY_POLL_INTERVAL",
	"REAPER_NOTIFY_REQUEST_TIMEOUT",
}

func setRequired(t *testing.T) {
	t.Helper()
	for _, name := range configVariables {
		t.Setenv(name, "")
	}
	t.Setenv("REAPER_NOTIFY_ADMIN_TOKEN", "management-token")
	t.Setenv("REAPER_NOTIFY_ADMIN_ACTOR", "operator@example.test")
	t.Setenv("REAPER_NOTIFY_READ_TOKEN", "audit-read-token")
	t.Setenv("REAPER_NOTIFY_SMTP_ADDR", "127.0.0.1:19401")
	t.Setenv("REAPER_NOTIFY_SMTP_FROM", "reaper@sender.example")
}

func TestLoadConfigRequiresEveryAuthorityAndRelayVariable(t *testing.T) {
	for _, missing := range []string{
		"REAPER_NOTIFY_ADMIN_TOKEN", "REAPER_NOTIFY_ADMIN_ACTOR", "REAPER_NOTIFY_READ_TOKEN",
		"REAPER_NOTIFY_SMTP_ADDR", "REAPER_NOTIFY_SMTP_FROM",
	} {
		t.Run(missing, func(t *testing.T) {
			setRequired(t)
			t.Setenv(missing, "")
			if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "are required") {
				t.Fatalf("error = %v, want a required-variable error", err)
			}
		})
	}
}

func TestLoadConfigParsesRetryAndClockSettings(t *testing.T) {
	setRequired(t)
	t.Setenv("REAPER_NOTIFY_RETRY_DELAYS", "1s,250ms,2m")
	t.Setenv("REAPER_NOTIFY_POLL_INTERVAL", "75ms")
	t.Setenv("REAPER_NOTIFY_REQUEST_TIMEOUT", "3s")
	configuration, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	wantDelays := []time.Duration{time.Second, 250 * time.Millisecond, 2 * time.Minute}
	if !slices.Equal(configuration.retryDelays, wantDelays) {
		t.Fatalf("retry delays = %v, want %v", configuration.retryDelays, wantDelays)
	}
	if configuration.pollInterval != 75*time.Millisecond || configuration.requestTimeout != 3*time.Second {
		t.Fatalf("poll/request = %s/%s", configuration.pollInterval, configuration.requestTimeout)
	}
}

func TestRetryDelaysDefaultsAndRejections(t *testing.T) {
	delays, err := retryDelays("")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(delays, routing.DefaultRetryDelays) {
		t.Fatalf("retry delays = %v, want defaults", delays)
	}
	delays[0] = time.Nanosecond
	if routing.DefaultRetryDelays[0] == time.Nanosecond {
		t.Fatal("default retry schedule aliases caller-owned slice")
	}
	for _, raw := range []string{"1s, 2s", "0s", "-1s", "1s,", "soon"} {
		if _, err := retryDelays(raw); err == nil {
			t.Fatalf("retry delays %q were accepted", raw)
		}
	}
}
