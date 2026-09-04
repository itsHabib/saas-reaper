package delivery

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// IDGenerator creates opaque identifiers with a responsibility-revealing prefix.
type IDGenerator func(prefix string) (string, error)

// RandomID creates a 128-bit identifier from the operating system random source.
func RandomID(prefix string) (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate %s identifier: %w", prefix, err)
	}
	return prefix + hex.EncodeToString(value), nil
}

// RandomSecret creates a Standard Webhooks symmetric secret from 256 random bits.
func RandomSecret() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate endpoint secret: %w", err)
	}
	return webhookSecretPrefix + base64.StdEncoding.EncodeToString(value), nil
}
