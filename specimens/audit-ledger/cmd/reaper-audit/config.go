package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

type config struct {
	address        string
	databasePath   string
	writeToken     string
	writePrincipal string
	readToken      string
	readTenants    []string
}

func loadConfig() (config, error) {
	loaded := config{
		address:        environment("REAPER_AUDIT_ADDR", ":8080"),
		databasePath:   environment("REAPER_AUDIT_DB", ".reaper/audit.db"),
		writeToken:     os.Getenv("REAPER_AUDIT_WRITE_TOKEN"),
		writePrincipal: os.Getenv("REAPER_AUDIT_WRITE_PRINCIPAL"),
		readToken:      os.Getenv("REAPER_AUDIT_READ_TOKEN"),
	}
	if loaded.writeToken == "" || loaded.writePrincipal == "" || loaded.readToken == "" {
		return config{}, errors.New(
			"REAPER_AUDIT_WRITE_TOKEN, REAPER_AUDIT_WRITE_PRINCIPAL, and REAPER_AUDIT_READ_TOKEN are required",
		)
	}
	tenants, err := readTenants(os.Getenv("REAPER_AUDIT_READ_TENANTS"))
	if err != nil {
		return config{}, err
	}
	loaded.readTenants = tenants
	return loaded, nil
}

func readTenants(raw string) ([]string, error) {
	if raw == "" {
		return nil, errors.New("REAPER_AUDIT_READ_TENANTS must list at least one tenant")
	}
	parts := strings.Split(raw, ",")
	tenants := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != part || part == "" {
			return nil, fmt.Errorf("REAPER_AUDIT_READ_TENANTS entry %q must be an unpadded tenant name", part)
		}
		tenants = append(tenants, part)
	}
	return tenants, nil
}

func environment(name, fallback string) string {
	value := os.Getenv(name)
	if value != "" {
		return value
	}
	return fallback
}
