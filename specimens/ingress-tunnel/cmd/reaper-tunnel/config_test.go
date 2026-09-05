package main

import (
	"testing"
	"time"
)

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("REAPER_TUNNEL_ADMIN_TOKEN", "admin")
	t.Setenv("REAPER_TUNNEL_ADMIN_ACTOR", "operator")
	t.Setenv("REAPER_TUNNEL_READ_TOKEN", "read")
	t.Setenv("REAPER_TUNNEL_DOMAIN", "Tunnel.Example.com")
}

func TestLoadConfigRequiresTokensAndDomain(t *testing.T) {
	t.Setenv("REAPER_TUNNEL_ADMIN_TOKEN", "")
	t.Setenv("REAPER_TUNNEL_ADMIN_ACTOR", "")
	t.Setenv("REAPER_TUNNEL_READ_TOKEN", "")
	t.Setenv("REAPER_TUNNEL_DOMAIN", "")
	if _, err := loadConfig(); err == nil {
		t.Fatal("missing tokens accepted")
	}
	setRequired(t)
	t.Setenv("REAPER_TUNNEL_DOMAIN", "")
	if _, err := loadConfig(); err == nil {
		t.Fatal("missing domain accepted")
	}
}

func TestLoadConfigAppliesDefaultsAndOverrides(t *testing.T) {
	setRequired(t)
	t.Setenv("REAPER_TUNNEL_HEADER_TIMEOUT", "")
	t.Setenv("REAPER_TUNNEL_KEEPALIVE", "")
	loaded, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.domain != "tunnel.example.com" || loaded.forwardProto != "https" || loaded.headerTimeout != 30*time.Second {
		t.Fatalf("defaults = %+v", loaded)
	}
	t.Setenv("REAPER_TUNNEL_HEADER_TIMEOUT", "2s")
	t.Setenv("REAPER_TUNNEL_KEEPALIVE", "250ms")
	loaded, err = loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.headerTimeout != 2*time.Second || loaded.link.KeepAliveInterval != 250*time.Millisecond {
		t.Fatalf("overrides = %+v", loaded)
	}
	t.Setenv("REAPER_TUNNEL_KEEPALIVE", "-1s")
	if _, err := loadConfig(); err == nil {
		t.Fatal("negative keepalive accepted")
	}
	t.Setenv("REAPER_TUNNEL_KEEPALIVE", " 1s")
	if _, err := loadConfig(); err == nil {
		t.Fatal("padded duration accepted")
	}
}
