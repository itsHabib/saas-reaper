package flags

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrConflict means a publish used a stale expected revision.
	ErrConflict = errors.New("flag revision conflict")
	// ErrInvalid means a definition, environment, actor, or context broke policy.
	ErrInvalid = errors.New("invalid flag")
	// ErrNotFound means the requested flag has no published definition.
	ErrNotFound = errors.New("flag not found")
)

// Kind identifies the value family shared by every variant in a flag.
type Kind string

// Supported flag value kinds.
const (
	Boolean Kind = "boolean"
	String  Kind = "string"
)

// Flag is one validated, revisioned feature definition.
type Flag struct {
	Key            string         `json:"key"`
	Kind           Kind           `json:"kind"`
	Enabled        bool           `json:"enabled"`
	DefaultVariant string         `json:"defaultVariant"`
	Variants       map[string]any `json:"variants"`
	Rules          []Rule         `json:"rules,omitempty"`
	Rollout        *Rollout       `json:"rollout,omitempty"`
	Revision       int64          `json:"revision"`
	UpdatedAt      time.Time      `json:"updatedAt"`
}

// Rule selects a variant when one approved string attribute equals a value.
type Rule struct {
	Attribute string `json:"attribute"`
	Equals    string `json:"equals"`
	Variant   string `json:"variant"`
}

// Rollout selects a variant for a stable percentage of one approved attribute.
type Rollout struct {
	Attribute  string `json:"attribute"`
	Percentage int    `json:"percentage"`
	Variant    string `json:"variant"`
}

// Evaluation records the selected value and why it won.
type Evaluation struct {
	Key      string
	Value    any
	Reason   string
	Variant  string
	Revision int64
}

// AuditEntry identifies one durable definition publication.
type AuditEntry struct {
	Sequence    int64     `json:"sequence"`
	Environment string    `json:"environment"`
	Key         string    `json:"key"`
	Revision    int64     `json:"revision"`
	Actor       string    `json:"actor"`
	Action      string    `json:"action"`
	OccurredAt  time.Time `json:"occurredAt"`
}

// Validate enforces definition shape and the domain targeting boundary.
func (f Flag) Validate() error {
	if err := validateKey(f.Key); err != nil {
		return err
	}
	if f.Kind != Boolean && f.Kind != String {
		return fmt.Errorf("%w: kind must be boolean or string", ErrInvalid)
	}
	if len(f.Variants) == 0 {
		return fmt.Errorf("%w: at least one variant is required", ErrInvalid)
	}
	if _, ok := f.Variants[f.DefaultVariant]; !ok {
		return fmt.Errorf("%w: default variant %q is not defined", ErrInvalid, f.DefaultVariant)
	}
	for name, value := range f.Variants {
		if err := validateVariant(f.Kind, name, value); err != nil {
			return err
		}
	}
	for i, rule := range f.Rules {
		if err := validateRule(f, rule); err != nil {
			return fmt.Errorf("rule %d: %w", i, err)
		}
	}
	if f.Rollout == nil {
		return nil
	}
	return validateRollout(f, *f.Rollout)
}

// Copy returns an independent flag suitable for a read projection.
func (f Flag) Copy() Flag {
	copyOfFlag := f
	copyOfFlag.Variants = make(map[string]any, len(f.Variants))
	for key, value := range f.Variants {
		copyOfFlag.Variants[key] = value
	}
	copyOfFlag.Rules = append([]Rule(nil), f.Rules...)
	if f.Rollout == nil {
		return copyOfFlag
	}
	rollout := *f.Rollout
	copyOfFlag.Rollout = &rollout
	return copyOfFlag
}

// Decode reconstructs and validates a stored flag definition.
func Decode(data []byte) (Flag, error) {
	var flag Flag
	if err := json.Unmarshal(data, &flag); err != nil {
		return Flag{}, fmt.Errorf("decode flag: %w", err)
	}
	if err := flag.Validate(); err != nil {
		return Flag{}, err
	}
	return flag, nil
}

func validateKey(key string) error {
	if key == "" {
		return fmt.Errorf("%w: key is required", ErrInvalid)
	}
	for _, char := range key {
		if isKeyCharacter(char) {
			continue
		}
		return fmt.Errorf("%w: key %q contains unsupported character %q", ErrInvalid, key, char)
	}
	return nil
}

func isKeyCharacter(char rune) bool {
	if char >= 'a' && char <= 'z' {
		return true
	}
	if char >= 'A' && char <= 'Z' {
		return true
	}
	if char >= '0' && char <= '9' {
		return true
	}
	return strings.ContainsRune("._-", char)
}

func validateVariant(kind Kind, name string, value any) error {
	if name == "" {
		return fmt.Errorf("%w: variant name is required", ErrInvalid)
	}
	switch kind {
	case Boolean:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%w: variant %q must contain a boolean", ErrInvalid, name)
		}
	case String:
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%w: variant %q must contain a string", ErrInvalid, name)
		}
	}
	return nil
}
