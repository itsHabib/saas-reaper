package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/link"
	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/tunnel"
)

type config struct {
	serverURL   string
	token       string
	target      *url.URL
	dialTimeout time.Duration
	reconnect   []time.Duration
	link        link.Config
}

func loadConfig() (config, error) {
	loaded := config{
		serverURL:   os.Getenv("REAPER_TUNNEL_AGENT_SERVER"),
		token:       os.Getenv("REAPER_TUNNEL_AGENT_TOKEN"),
		dialTimeout: 15 * time.Second,
		link:        link.DefaultConfig(),
	}
	if loaded.serverURL == "" || loaded.token == "" || os.Getenv("REAPER_TUNNEL_AGENT_TARGET") == "" {
		return config{}, errors.New(
			"REAPER_TUNNEL_AGENT_SERVER, REAPER_TUNNEL_AGENT_TOKEN, and REAPER_TUNNEL_AGENT_TARGET are required",
		)
	}
	target, err := url.Parse(os.Getenv("REAPER_TUNNEL_AGENT_TARGET"))
	if err != nil {
		return config{}, fmt.Errorf("parse REAPER_TUNNEL_AGENT_TARGET: %w", err)
	}
	loaded.target = target
	loaded.reconnect, err = reconnectDelays(os.Getenv("REAPER_TUNNEL_AGENT_RECONNECT_DELAYS"))
	if err != nil {
		return config{}, err
	}
	if raw := os.Getenv("REAPER_TUNNEL_AGENT_KEEPALIVE"); raw != "" {
		loaded.link.KeepAliveInterval, err = positiveDuration("REAPER_TUNNEL_AGENT_KEEPALIVE", raw)
		if err != nil {
			return config{}, err
		}
	}
	return loaded, nil
}

func reconnectDelays(raw string) ([]time.Duration, error) {
	if raw == "" {
		return append([]time.Duration(nil), tunnel.DefaultReconnectDelays...), nil
	}
	parts := strings.Split(raw, ",")
	delays := make([]time.Duration, 0, len(parts))
	for _, part := range parts {
		delay, err := positiveDuration("REAPER_TUNNEL_AGENT_RECONNECT_DELAYS", part)
		if err != nil {
			return nil, err
		}
		delays = append(delays, delay)
	}
	return delays, nil
}

func positiveDuration(name, raw string) (time.Duration, error) {
	if strings.TrimSpace(raw) != raw || raw == "" {
		return 0, fmt.Errorf("%s must contain unpadded positive durations", name)
	}
	duration, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%s durations must be positive", name)
	}
	return duration, nil
}
