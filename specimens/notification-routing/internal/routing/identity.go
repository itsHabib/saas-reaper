package routing

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
)

// IDGenerator creates opaque identifiers with a responsibility-revealing prefix.
type IDGenerator func(prefix string) (string, error)

var (
	ownedID        = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,63}$`)
	idempotencyKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
)

// RandomID creates a 128-bit identifier from the operating system random source.
func RandomID(prefix string) (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate %s identifier: %w", prefix, err)
	}
	return prefix + hex.EncodeToString(value), nil
}

func validateOwnedID(label, id string) error {
	if !ownedID.MatchString(id) {
		return fmt.Errorf("%w: %s id must be 2-64 lowercase path-safe characters", ErrInvalid, label)
	}
	return nil
}
