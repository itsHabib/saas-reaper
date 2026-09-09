package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/link"
	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/tunnel"
)

type config struct {
	controlAddress string
	edgeAddress    string
	diagAddress    string
	pprof          bool
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
		diagAddress:    environment("REAPER_TUNNEL_DIAG_ADDR", "127.0.0.1:8082"),
		pprof:          os.Getenv("REAPER_TUNNEL_PPROF") == "1",
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
	if err := loopbackOnly("REAPER_TUNNEL_DIAG_ADDR", loaded.diagAddress); err != nil {
		return config{}, err
	}
	var err error
	if raw := os.Getenv("REAPER_TUNNEL_HEADER_TIMEOUT"); raw != "" {
		loaded.headerTimeout, err = tunnel.ParseDuration("REAPER_TUNNEL_HEADER_TIMEOUT", raw)
		if err != nil {
			return config{}, err
		}
	}
	if raw := os.Getenv("REAPER_TUNNEL_KEEPALIVE"); raw != "" {
		loaded.link.KeepAliveInterval, err = tunnel.ParseDuration("REAPER_TUNNEL_KEEPALIVE", raw)
		if err != nil {
			return config{}, err
		}
	}
	return loaded, nil
}

// loopbackOnly refuses to expose the diagnostics listener beyond the host: metrics name every
// claimed subdomain and pprof is a debugging surface, so neither belongs on a routable address.
func loopbackOnly(name, address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("parse %s: %w", name, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("%s must bind a loopback address", name)
	}
	return nil
}

func environment(name, fallback string) string {
	value := os.Getenv(name)
	if value != "" {
		return value
	}
	return fallback
}
