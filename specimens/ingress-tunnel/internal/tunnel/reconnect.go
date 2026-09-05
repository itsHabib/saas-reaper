package tunnel

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// DefaultReconnectDelays is the public agent reconnect schedule. The final delay repeats
// forever: an agent never gives up on a link that was merely lost, it only gives up on a
// credential the server refuses.
var DefaultReconnectDelays = []time.Duration{
	time.Second,
	2 * time.Second,
	5 * time.Second,
	10 * time.Second,
	30 * time.Second,
}

// Schedule is a bounded, capped backoff.
type Schedule struct {
	delays []time.Duration
}

// NewSchedule validates a schedule of positive delays.
func NewSchedule(delays []time.Duration) (Schedule, error) {
	if len(delays) == 0 {
		return Schedule{}, errors.New("reconnect schedule needs at least one delay")
	}
	for _, delay := range delays {
		if delay <= 0 {
			return Schedule{}, errors.New("reconnect delays must be positive")
		}
	}
	return Schedule{delays: append([]time.Duration(nil), delays...)}, nil
}

// Delay returns the wait before the given failed attempt is retried; attempt counts from one.
func (s Schedule) Delay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > len(s.delays) {
		return s.delays[len(s.delays)-1]
	}
	return s.delays[attempt-1]
}

// ParseDuration reads one configured duration the way every composition root must: unpadded,
// parseable, and strictly positive. name is the setting being parsed, for the error text.
func ParseDuration(name, raw string) (time.Duration, error) {
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

// ParseDelays reads a comma-separated schedule with ParseDuration applied to every part.
func ParseDelays(name, raw string) ([]time.Duration, error) {
	parts := strings.Split(raw, ",")
	delays := make([]time.Duration, 0, len(parts))
	for _, part := range parts {
		delay, err := ParseDuration(name, part)
		if err != nil {
			return nil, err
		}
		delays = append(delays, delay)
	}
	return delays, nil
}
