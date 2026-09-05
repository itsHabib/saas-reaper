package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/link"
)

type config struct {
	controlAddress string
	edgeAddress    string
	domain         string
	databasePath   string
	adminToken     string
	adminActor     string
	readToken      string
	forwardProto   string
	headerTimeout  time.Duration
	link           link.Config
}

func loadConfig() (config, error) {
	loaded := config{
		controlAddress: environment("REAPER_TUNNEL_CONTROL_ADDR", ":8081"),
		edgeAddress:    environment("REAPER_TUNNEL_EDGE_ADDR", ":8080"),
		domain:         strings.ToLower(os.Getenv("REAPER_TUNNEL_DOMAIN")),
		databasePath:   environment("REAPER_TUNNEL_DB", ".reaper/tunnel.db"),
		adminToken:     os.Getenv("REAPER_TUNNEL_ADMIN_TOKEN"),
		adminActor:     os.Getenv("REAPER_TUNNEL_ADMIN_ACTOR"),
		readToken:      os.Getenv("REAPER_TUNNEL_READ_TOKEN"),
		forwardProto:   environment("REAPER_TUNNEL_FORWARD_PROTO", "https"),
		headerTimeout:  30 * time.Second,
		link:           link.DefaultConfig(),
	}
	if loaded.adminToken == "" || loaded.adminActor == "" || loaded.readToken == "" {
		return config{}, errors.New(
			"REAPER_TUNNEL_ADMIN_TOKEN, REAPER_TUNNEL_ADMIN_ACTOR, and REAPER_TUNNEL_READ_TOKEN are required",
		)
	}
	if loaded.domain == "" || strings.HasPrefix(loaded.domain, ".") || strings.Contains(loaded.domain, "/") {
		return config{}, errors.New("REAPER_TUNNEL_DOMAIN must name the domain tunnels are served beneath")
	}
	var err error
	if raw := os.Getenv("REAPER_TUNNEL_HEADER_TIMEOUT"); raw != "" {
		loaded.headerTimeout, err = positiveDuration("REAPER_TUNNEL_HEADER_TIMEOUT", raw)
		if err != nil {
			return config{}, err
		}
	}
	if raw := os.Getenv("REAPER_TUNNEL_KEEPALIVE"); raw != "" {
		loaded.link.KeepAliveInterval, err = positiveDuration("REAPER_TUNNEL_KEEPALIVE", raw)
		if err != nil {
			return config{}, err
		}
	}
	return loaded, nil
}

func positiveDuration(name, raw string) (time.Duration, error) {
	if strings.TrimSpace(raw) != raw || raw == "" {
		return 0, fmt.Errorf("%s must be an unpadded positive duration", name)
	}
	duration, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%s must be positive", name)
	}
	return duration, nil
}

func environment(name, fallback string) string {
	value := os.Getenv(name)
	if value != "" {
		return value
	}
	return fallback
}
