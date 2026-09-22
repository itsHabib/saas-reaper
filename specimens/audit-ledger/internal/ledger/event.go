package ledger

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxBatch bounds the number of events in one append request.
	MaxBatch = 500
	// MaxFieldBytes bounds every identifying string field.
	MaxFieldBytes = 512
	// MaxTenantBytes bounds the tenant name.
	MaxTenantBytes = 64
)

var tenantPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// Event is one client-submitted audit fact before the ledger assigns its position.
type Event struct {
	Tenant     string
	ID         string
	Actor      string
	Action     string
	Target     string
	OccurredAt string
	Metadata   json.RawMessage
}

// Entry is one recorded, chained ledger position.
type Entry struct {
	Tenant       string
	Sequence     int64
	ID           string
	Actor        string
	Action       string
	Target       string
	OccurredAt   string
	RecordedAt   string
	Source       string
	Metadata     json.RawMessage
	Hash         string
	PreviousHash string
}

// Head is the latest recorded position of one tenant chain.
type Head struct {
	Sequence int64
	Hash     string
}

// Receipt is the ledger's answer for one submitted event.
type Receipt struct {
	Tenant   string
	ID       string
	Sequence int64
	Hash     string
	Replayed bool
}

// Normalize validates an event against ledger policy and returns its canonical form:
// a UTC RFC 3339 occurrence time and canonical metadata bytes.
func Normalize(event Event) (Event, error) {
	if err := validateTenant(event.Tenant); err != nil {
		return Event{}, err
	}
	fields := map[string]string{
		"id":     event.ID,
		"actor":  event.Actor,
		"action": event.Action,
		"target": event.Target,
	}
	for _, name := range []string{"id", "actor", "action", "target"} {
		if err := validateField(name, fields[name]); err != nil {
			return Event{}, err
		}
	}
	occurredAt, err := normalizeTime(event.OccurredAt)
	if err != nil {
		return Event{}, err
	}
	metadata, err := CanonicalValue(event.Metadata)
	if err != nil {
		return Event{}, fmt.Errorf("%w: metadata: %w", ErrInvalid, err)
	}
	return Event{
		Tenant:     event.Tenant,
		ID:         event.ID,
		Actor:      event.Actor,
		Action:     event.Action,
		Target:     event.Target,
		OccurredAt: occurredAt,
		Metadata:   metadata,
	}, nil
}

// ValidateTenant reports whether a tenant name is acceptable in a route or event.
func ValidateTenant(tenant string) error {
	return validateTenant(tenant)
}

func validateTenant(tenant string) error {
	if len(tenant) == 0 || len(tenant) > MaxTenantBytes || !tenantPattern.MatchString(tenant) {
		return fmt.Errorf("%w: tenant must be 1-%d lowercase letters, digits, or interior hyphens", ErrInvalid, MaxTenantBytes)
	}
	return nil
}

func validateField(name, value string) error {
	if value == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalid, name)
	}
	if len(value) > MaxFieldBytes {
		return fmt.Errorf("%w: %s exceeds %d bytes", ErrInvalid, name, MaxFieldBytes)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%w: %s must not have surrounding whitespace", ErrInvalid, name)
	}
	if !utf8.ValidString(value) || strings.ContainsRune(value, utf8.RuneError) {
		return fmt.Errorf("%w: %s must be valid UTF-8 without U+FFFD", ErrInvalid, name)
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: %s must not contain control characters", ErrInvalid, name)
	}
	return nil
}

func normalizeTime(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("%w: occurredAt is required", ErrInvalid)
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return "", fmt.Errorf("%w: occurredAt must be RFC 3339: %w", ErrInvalid, err)
	}
	return FormatTime(parsed), nil
}

// FormatTime renders a ledger timestamp: UTC, RFC 3339, trailing sub-second zeros trimmed.
func FormatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
