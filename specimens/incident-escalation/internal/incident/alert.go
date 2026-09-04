package incident

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Action is the Events API v2 event_action vocabulary.
type Action string

const (
	// ActionTrigger opens or re-triggers an incident.
	ActionTrigger Action = "trigger"
	// ActionAcknowledge takes ownership of the open incident with the dedup key.
	ActionAcknowledge Action = "acknowledge"
	// ActionResolve closes the open incident with the dedup key.
	ActionResolve Action = "resolve"
)

const (
	maxDedupKeyRunes = 255
	maxSummaryRunes  = 1024
	maxSourceRunes   = 1024
	maxClientRunes   = 256
)

// Alert is the policy-level view of one Events API v2 event after transport decoding.
type Alert struct {
	RoutingKey string
	Action     Action
	DedupKey   string
	Summary    string
	Source     string
	Severity   Severity
	Client     string
}

// Receipt is what the ingest contract promises back for an accepted event.
type Receipt struct {
	DedupKey string
}

func validateAlert(alert Alert) error {
	if strings.TrimSpace(alert.RoutingKey) == "" {
		return fmt.Errorf("%w: routing_key is required", ErrInvalid)
	}
	if utf8.RuneCountInString(alert.DedupKey) > maxDedupKeyRunes {
		return fmt.Errorf("%w: dedup_key exceeds %d characters", ErrInvalid, maxDedupKeyRunes)
	}
	if utf8.RuneCountInString(alert.Client) > maxClientRunes {
		return fmt.Errorf("%w: client exceeds %d characters", ErrInvalid, maxClientRunes)
	}
	switch alert.Action {
	case ActionTrigger:
		return validateTriggerPayload(alert)
	case ActionAcknowledge, ActionResolve:
		if alert.DedupKey == "" {
			return fmt.Errorf("%w: dedup_key is required for %s", ErrInvalid, alert.Action)
		}
		return nil
	}
	return fmt.Errorf("%w: event_action must be trigger, acknowledge, or resolve", ErrInvalid)
}

func validateTriggerPayload(alert Alert) error {
	if strings.TrimSpace(alert.Summary) == "" || utf8.RuneCountInString(alert.Summary) > maxSummaryRunes {
		return fmt.Errorf("%w: payload.summary is required and at most %d characters", ErrInvalid, maxSummaryRunes)
	}
	if strings.TrimSpace(alert.Source) == "" || utf8.RuneCountInString(alert.Source) > maxSourceRunes {
		return fmt.Errorf("%w: payload.source is required and at most %d characters", ErrInvalid, maxSourceRunes)
	}
	switch alert.Severity {
	case SeverityCritical, SeverityError, SeverityWarning, SeverityInfo:
		return nil
	}
	return fmt.Errorf("%w: payload.severity must be critical, error, warning, or info", ErrInvalid)
}
