package main

import (
	"testing"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/tunnel"
)

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("REAPER_TUNNEL_AGENT_SERVER", "https://tunnel.example.com")
	t.Setenv("REAPER_TUNNEL_AGENT_TOKEN", "rtk_x")
	t.Setenv("REAPER_TUNNEL_AGENT_TARGET", "http://127.0.0.1:3000")
	t.Setenv("REAPER_TUNNEL_AGENT_RECONNECT_DELAYS", "")
	t.Setenv("REAPER_TUNNEL_AGENT_KEEPALIVE", "")
}

func TestLoadConfigRequiresServerTokenAndTarget(t *testing.T) {
	setRequired(t)
	t.Setenv("REAPER_TUNNEL_AGENT_TARGET", "")
	if _, err := loadConfig(); err == nil {
		t.Fatal("missing target accepted")
	}
}

func TestLoadConfigDefaultsToThePublicReconnectSchedule(t *testing.T) {
	setRequired(t)
	loaded, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.reconnect) != len(tunnel.DefaultReconnectDelays) || loaded.target.Host != "127.0.0.1:3000" {
		t.Fatalf("defaults = %+v", loaded)
	}
	t.Setenv("REAPER_TUNNEL_AGENT_RECONNECT_DELAYS", "100ms,200ms")
	loaded, err = loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.reconnect) != 2 || loaded.reconnect[1] != 200*time.Millisecond {
		t.Fatalf("injected schedule = %v", loaded.reconnect)
	}
	t.Setenv("REAPER_TUNNEL_AGENT_RECONNECT_DELAYS", "100ms,0s")
	if _, err := loadConfig(); err == nil {
		t.Fatal("zero delay accepted")
	}
}
