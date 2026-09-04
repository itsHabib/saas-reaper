package routing

import (
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"time"
)

// ChannelKind names the customer-owned transport protocol a channel speaks.
type ChannelKind string

const (
	// KindSMTP delivers email through the customer's own SMTP relay.
	KindSMTP ChannelKind = "smtp"
	// KindSlackWebhook posts Slack-compatible incoming-webhook payloads.
	KindSlackWebhook ChannelKind = "slack-webhook"
)

const maxAddressBytes = 254

// Channel binds one customer-facing channel name to a transport kind.
type Channel struct {
	ID        string
	Kind      ChannelKind
	Enabled   bool
	Revision  int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewChannel validates a first channel revision.
func NewChannel(id string, kind ChannelKind, now time.Time) (Channel, error) {
	if err := validateOwnedID("channel", id); err != nil {
		return Channel{}, err
	}
	if !kind.Known() {
		return Channel{}, fmt.Errorf("%w: channel kind %q is not supported", ErrInvalid, kind)
	}
	return Channel{
		ID:        id,
		Kind:      kind,
		Enabled:   true,
		Revision:  1,
		CreatedAt: now.UTC(),
		UpdatedAt: now.UTC(),
	}, nil
}

// Known reports whether policy recognizes the transport kind.
func (k ChannelKind) Known() bool {
	return k == KindSMTP || k == KindSlackWebhook
}

func validateAddress(kind ChannelKind, address string) error {
	if strings.TrimSpace(address) != address || address == "" || len(address) > maxAddressBytes {
		return fmt.Errorf("%w: address must be 1-%d unpadded bytes", ErrInvalid, maxAddressBytes)
	}
	switch kind {
	case KindSMTP:
		return validateMailbox(address)
	case KindSlackWebhook:
		return validateWebhookURL(address)
	default:
		return fmt.Errorf("%w: channel kind %q is not supported", ErrInvalid, kind)
	}
}

func validateMailbox(address string) error {
	parsed, err := mail.ParseAddress(address)
	if err != nil {
		return fmt.Errorf("%w: parse email address: %w", ErrInvalid, err)
	}
	if parsed.Address != address {
		return fmt.Errorf("%w: email address must be a bare mailbox without a display name", ErrInvalid)
	}
	return nil
}

func validateWebhookURL(address string) error {
	parsed, err := url.ParseRequestURI(address)
	if err != nil {
		return fmt.Errorf("%w: parse webhook URL: %w", ErrInvalid, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%w: webhook URL scheme must be http or https", ErrInvalid)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%w: webhook URL host is required", ErrInvalid)
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("%w: webhook URL cannot contain credentials or a fragment", ErrInvalid)
	}
	return nil
}
