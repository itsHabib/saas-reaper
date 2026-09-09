package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/webhook-delivery/internal/delivery"
)

type config struct {
	address        string
	databasePath   string
	adminToken     string
	adminActor     string
	readToken      string
	retryDelays    []time.Duration
	pollInterval   time.Duration
	requestTimeout time.Duration
}

func loadConfig() (config, error) {
	loaded := config{
		address:        environment("REAPER_WEBHOOK_ADDR", ":8080"),
		databasePath:   environment("REAPER_WEBHOOK_DB", ".reaper/webhooks.db"),
		adminToken:     os.Getenv("REAPER_WEBHOOK_ADMIN_TOKEN"),
		adminActor:     os.Getenv("REAPER_WEBHOOK_ADMIN_ACTOR"),
		readToken:      os.Getenv("REAPER_WEBHOOK_READ_TOKEN"),
		pollInterval:   250 * time.Millisecond,
		requestTimeout: 20 * time.Second,
	}
	if loaded.adminToken == "" || loaded.adminActor == "" || loaded.readToken == "" {
		return config{}, errors.New(
			"REAPER_WEBHOOK_ADMIN_TOKEN, REAPER_WEBHOOK_ADMIN_ACTOR, and REAPER_WEBHOOK_READ_TOKEN are required",
		)
	}
	delays, err := retryDelays(os.Getenv("REAPER_WEBHOOK_RETRY_DELAYS"))
	if err != nil {
		return config{}, err
	}
	loaded.retryDelays = delays
	if raw := os.Getenv("REAPER_WEBHOOK_POLL_INTERVAL"); raw != "" {
		loaded.pollInterval, err = positiveDuration("REAPER_WEBHOOK_POLL_INTERVAL", raw)
		if err != nil {
			return config{}, err
		}
	}
	if raw := os.Getenv("REAPER_WEBHOOK_REQUEST_TIMEOUT"); raw != "" {
		loaded.requestTimeout, err = positiveDuration("REAPER_WEBHOOK_REQUEST_TIMEOUT", raw)
		if err != nil {
			return config{}, err
		}
	}
	return loaded, nil
}

func retryDelays(raw string) ([]time.Duration, error) {
	if raw == "" {
		return append([]time.Duration(nil), delivery.DefaultRetryDelays...), nil
	}
	parts := strings.Split(raw, ",")
	delays := make([]time.Duration, 0, len(parts))
	for _, part := range parts {
		delay, err := positiveDuration("REAPER_WEBHOOK_RETRY_DELAYS", part)
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

func environment(name, fallback string) string {
	value := os.Getenv(name)
	if value != "" {
		return value
	}
	return fallback
}
