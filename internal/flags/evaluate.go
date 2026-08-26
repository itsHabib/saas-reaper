package flags

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// Snapshot is the validated read projection consumed by evaluation.
type Snapshot interface {
	Replace(map[string][]Flag)
	Put(string, Flag)
	Get(string, string) (Flag, bool)
	List(string) []Flag
}

// Evaluate resolves one flag from the current environment projection.
func (s *Service) Evaluate(environment, key string, context map[string]any) (Evaluation, error) {
	if err := validateEnvironment(environment); err != nil {
		return Evaluation{}, err
	}
	flag, ok := s.snapshot.Get(environment, key)
	if !ok {
		return Evaluation{}, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	return evaluate(flag, context)
}

// List returns copied current definitions ordered by key.
func (s *Service) List(environment string) ([]Flag, error) {
	if err := validateEnvironment(environment); err != nil {
		return nil, err
	}
	return s.snapshot.List(environment), nil
}

func evaluate(flag Flag, context map[string]any) (Evaluation, error) {
	targetingKey, ok := context["targetingKey"].(string)
	if !ok || targetingKey == "" {
		return Evaluation{}, fmt.Errorf("%w: targetingKey must be a non-empty string", ErrInvalid)
	}
	if !flag.Enabled {
		return resolve(flag, flag.DefaultVariant, "DISABLED"), nil
	}
	for _, rule := range flag.Rules {
		if !matches(rule, context) {
			continue
		}
		return resolve(flag, rule.Variant, "TARGETING_MATCH"), nil
	}
	if flag.Rollout == nil {
		return resolve(flag, flag.DefaultVariant, "STATIC"), nil
	}
	if !inRollout(flag.Key, *flag.Rollout, context) {
		return resolve(flag, flag.DefaultVariant, "STATIC"), nil
	}
	return resolve(flag, flag.Rollout.Variant, "SPLIT"), nil
}

func matches(rule Rule, context map[string]any) bool {
	value, ok := context[rule.Attribute].(string)
	if !ok {
		return false
	}
	return value == rule.Equals
}

func inRollout(key string, rollout Rollout, context map[string]any) bool {
	value, ok := context[rollout.Attribute].(string)
	if !ok {
		return false
	}
	digest := sha256.Sum256([]byte(key + "\x00" + value))
	bucket := binary.BigEndian.Uint64(digest[:8]) % 10_000
	threshold := uint64(rollout.Percentage) * 100 //nolint:gosec // Flag validation constrains percentage to 0..100.
	return bucket < threshold
}

func resolve(flag Flag, variant, reason string) Evaluation {
	return Evaluation{
		Key:      flag.Key,
		Value:    flag.Variants[variant],
		Reason:   reason,
		Variant:  variant,
		Revision: flag.Revision,
	}
}
