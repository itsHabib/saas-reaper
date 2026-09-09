package main

import (
	"testing"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/incident"
)

func requiredEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("REAPER_INCIDENT_ADMIN_TOKEN", "management")
	t.Setenv("REAPER_INCIDENT_ADMIN_ACTOR", "operator")
	t.Setenv("REAPER_INCIDENT_READ_TOKEN", "read")
}

func TestDefaultsAndOverrides(t *testing.T) {
	requiredEnvironment(t)
	loaded, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.address != ":8080" || loaded.databasePath != ".reaper/incidents.db" {
		t.Fatalf("unexpected defaults %#v", loaded)
	}
	if loaded.pollInterval != 250*time.Millisecond || loaded.requestTimeout != 20*time.Second {
		t.Fatalf("unexpected timing defaults %#v", loaded)
	}
	if loaded.clockOffset != 0 {
		t.Fatalf("the clock offset must default to zero, got %s", loaded.clockOffset)
	}
	if len(loaded.retryDelays) != len(incident.DefaultNotifyRetryDelays) {
		t.Fatalf("unexpected default schedule %v", loaded.retryDelays)
	}
	t.Setenv("REAPER_INCIDENT_ADDR", "127.0.0.1:19500")
	t.Setenv("REAPER_INCIDENT_DB", "/tmp/incidents.db")
	t.Setenv("REAPER_INCIDENT_NOTIFY_RETRY_DELAYS", "1s,2s")
	t.Setenv("REAPER_INCIDENT_POLL_INTERVAL", "50ms")
	t.Setenv("REAPER_INCIDENT_REQUEST_TIMEOUT", "2s")
	t.Setenv("REAPER_INCIDENT_CLOCK_OFFSET", "90s")
	overridden, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if overridden.address != "127.0.0.1:19500" || overridden.databasePath != "/tmp/incidents.db" {
		t.Fatalf("overrides were ignored %#v", overridden)
	}
	if len(overridden.retryDelays) != 2 || overridden.retryDelays[1] != 2*time.Second {
		t.Fatalf("unexpected schedule %v", overridden.retryDelays)
	}
	if overridden.clockOffset != 90*time.Second {
		t.Fatalf("unexpected clock offset %s", overridden.clockOffset)
	}
}

func TestAuthorityValuesAreRequired(t *testing.T) {
	cases := []string{
		"REAPER_INCIDENT_ADMIN_TOKEN",
		"REAPER_INCIDENT_ADMIN_ACTOR",
		"REAPER_INCIDENT_READ_TOKEN",
	}
	for _, missing := range cases {
		requiredEnvironment(t)
		t.Setenv(missing, "")
		if _, err := loadConfig(); err == nil {
			t.Fatalf("%s must be required", missing)
		}
	}
}

func TestDurationsMustBePositiveAndUnpadded(t *testing.T) {
	cases := map[string]string{
		"REAPER_INCIDENT_NOTIFY_RETRY_DELAYS": "1s, 2s",
		"REAPER_INCIDENT_POLL_INTERVAL":       "0s",
		"REAPER_INCIDENT_REQUEST_TIMEOUT":     "-1s",
		"REAPER_INCIDENT_CLOCK_OFFSET":        "soon",
	}
	for name, value := range cases {
		requiredEnvironment(t)
		t.Setenv(name, value)
		if _, err := loadConfig(); err == nil {
			t.Fatalf("%s=%q must be rejected", name, value)
		}
	}
}
