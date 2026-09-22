package incident

import (
	"fmt"
	"strings"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/oncall"
)

const (
	maxLevels       = 10
	maxRepeats      = 9
	minLevelTimeout = time.Second
	maxLevelTimeout = 24 * time.Hour
)

// Level names who is paged at one escalation step and how long they have to acknowledge.
type Level struct {
	Timeout    oncall.Duration `json:"timeout"`
	Schedules  []string        `json:"schedules,omitempty"`
	Responders []string        `json:"responders,omitempty"`
}

// EscalationPolicy is the ordered ladder an unacknowledged incident climbs.
type EscalationPolicy struct {
	ID        string
	Name      string
	Levels    []Level
	Repeat    int
	CreatedAt time.Time
}

func validatePolicy(policy EscalationPolicy) error {
	if err := validateSlug("escalation policy", policy.ID); err != nil {
		return err
	}
	if strings.TrimSpace(policy.Name) == "" {
		return fmt.Errorf("%w: escalation policy name is required", ErrInvalid)
	}
	if len(policy.Levels) == 0 || len(policy.Levels) > maxLevels {
		return fmt.Errorf("%w: between 1 and %d escalation levels are required", ErrInvalid, maxLevels)
	}
	if policy.Repeat < 0 || policy.Repeat > maxRepeats {
		return fmt.Errorf("%w: repeat must be between 0 and %d", ErrInvalid, maxRepeats)
	}
	for index, level := range policy.Levels {
		if err := validateLevel(level); err != nil {
			return fmt.Errorf("level %d: %w", index, err)
		}
	}
	return nil
}

func validateLevel(level Level) error {
	timeout := time.Duration(level.Timeout)
	if timeout < minLevelTimeout || timeout > maxLevelTimeout {
		return fmt.Errorf("%w: level timeout must be between %s and %s", ErrInvalid, minLevelTimeout, maxLevelTimeout)
	}
	if len(level.Schedules)+len(level.Responders) == 0 {
		return fmt.Errorf("%w: a level targets at least one schedule or responder", ErrInvalid)
	}
	for _, schedule := range level.Schedules {
		if err := validateSlug("schedule", schedule); err != nil {
			return err
		}
	}
	for _, responder := range level.Responders {
		if err := validateSlug("responder", responder); err != nil {
			return err
		}
	}
	return nil
}

// Service is a monitored system whose routing key authenticates its alert source.
type Service struct {
	ID         string
	Name       string
	RoutingKey string
	PolicyID   string
	CreatedAt  time.Time
}

func validateService(service Service) error {
	if err := validateSlug("service", service.ID); err != nil {
		return err
	}
	if strings.TrimSpace(service.Name) == "" {
		return fmt.Errorf("%w: service name is required", ErrInvalid)
	}
	return validateSlug("escalation policy", service.PolicyID)
}
