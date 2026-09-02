package main

import (
	"strings"
	"testing"
)

func TestLoadConfigRequiresAuthorityValues(t *testing.T) {
	t.Setenv("REAPER_AUDIT_WRITE_TOKEN", "")
	t.Setenv("REAPER_AUDIT_WRITE_PRINCIPAL", "")
	t.Setenv("REAPER_AUDIT_READ_TOKEN", "")
	t.Setenv("REAPER_AUDIT_READ_TENANTS", "acme")
	if _, err := loadConfig(); err == nil {
		t.Fatal("missing authority values were accepted")
	}
}

func TestLoadConfigParsesTenantScope(t *testing.T) {
	t.Setenv("REAPER_AUDIT_WRITE_TOKEN", "write")
	t.Setenv("REAPER_AUDIT_WRITE_PRINCIPAL", "ingest")
	t.Setenv("REAPER_AUDIT_READ_TOKEN", "read")
	t.Setenv("REAPER_AUDIT_READ_TENANTS", "acme,globex")
	t.Setenv("REAPER_AUDIT_ADDR", "127.0.0.1:19609")
	loaded, err := loadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if strings.Join(loaded.readTenants, "|") != "acme|globex" || loaded.address != "127.0.0.1:19609" {
		t.Fatalf("config %+v", loaded)
	}
	for _, raw := range []string{"", "acme, globex", "acme,,globex"} {
		t.Setenv("REAPER_AUDIT_READ_TENANTS", raw)
		if _, err := loadConfig(); err == nil {
			t.Fatalf("tenant scope %q accepted", raw)
		}
	}
}
