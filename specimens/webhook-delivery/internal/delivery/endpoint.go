package delivery

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const webhookSecretPrefix = "whsec_"

var endpointID = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,63}$`)

// Endpoint is one independently signed outbound destination.
type Endpoint struct {
	ID        string
	URL       string
	Secret    string
	Enabled   bool
	Revision  int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewEndpoint validates a first endpoint revision.
func NewEndpoint(id, destination, secret string, now time.Time) (Endpoint, error) {
	if !endpointID.MatchString(id) {
		return Endpoint{}, fmt.Errorf("%w: endpoint id must be 2-64 lowercase path-safe characters", ErrInvalid)
	}
	if err := validateDestination(destination); err != nil {
		return Endpoint{}, err
	}
	if err := validateSecret(secret); err != nil {
		return Endpoint{}, err
	}
	return Endpoint{
		ID:        id,
		URL:       destination,
		Secret:    secret,
		Enabled:   true,
		Revision:  1,
		CreatedAt: now.UTC(),
		UpdatedAt: now.UTC(),
	}, nil
}

func validateDestination(destination string) error {
	if strings.TrimSpace(destination) != destination || destination == "" {
		return fmt.Errorf("%w: endpoint URL must not be empty or padded", ErrInvalid)
	}
	parsed, err := url.ParseRequestURI(destination)
	if err != nil {
		return fmt.Errorf("%w: parse endpoint URL: %w", ErrInvalid, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%w: endpoint URL scheme must be http or https", ErrInvalid)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%w: endpoint URL host is required", ErrInvalid)
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("%w: endpoint URL cannot contain credentials or a fragment", ErrInvalid)
	}
	return nil
}

func validateSecret(secret string) error {
	if !strings.HasPrefix(secret, webhookSecretPrefix) {
		return fmt.Errorf("%w: endpoint secret must start with %s", ErrInvalid, webhookSecretPrefix)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, webhookSecretPrefix))
	if err != nil {
		return fmt.Errorf("%w: decode endpoint secret: %w", ErrInvalid, err)
	}
	if len(decoded) < 24 || len(decoded) > 64 {
		return fmt.Errorf("%w: endpoint secret must encode 24-64 bytes", ErrInvalid)
	}
	return nil
}
