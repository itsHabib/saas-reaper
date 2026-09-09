package incident

import (
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"time"
)

// Channel names one notification transport a responder can be paged on.
type Channel string

const (
	// ChannelWebhook pages through a signed HTTP POST to the responder's URL.
	ChannelWebhook Channel = "webhook"
	// ChannelEmail pages through SMTP to the responder's address.
	ChannelEmail Channel = "email"
)

// Responder is a person or system that can hold the pager.
type Responder struct {
	ID            string
	Email         string
	WebhookURL    string
	WebhookSecret string
	CreatedAt     time.Time
}

// Channels lists the transports this responder can be reached on, in fixed order.
func (r Responder) Channels() []Channel {
	channels := make([]Channel, 0, 2)
	if r.WebhookURL != "" {
		channels = append(channels, ChannelWebhook)
	}
	if r.Email != "" {
		channels = append(channels, ChannelEmail)
	}
	return channels
}

func validateResponder(responder Responder) error {
	if err := validateSlug("responder", responder.ID); err != nil {
		return err
	}
	if responder.Email == "" && responder.WebhookURL == "" {
		return fmt.Errorf("%w: a responder needs an email address or a webhook URL", ErrInvalid)
	}
	if responder.Email != "" {
		if err := validateEmail(responder.Email); err != nil {
			return err
		}
	}
	if responder.WebhookURL == "" {
		return nil
	}
	return validateWebhookURL(responder.WebhookURL)
}

func validateEmail(address string) error {
	parsed, err := mail.ParseAddress(address)
	if err != nil || parsed.Address != address || parsed.Name != "" {
		return fmt.Errorf("%w: email must be a bare valid address", ErrInvalid)
	}
	return nil
}

func validateWebhookURL(raw string) error {
	if strings.TrimSpace(raw) != raw || len(raw) > 2048 {
		return fmt.Errorf("%w: webhook URL must be unpadded and at most 2048 bytes", ErrInvalid)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: webhook URL: %w", ErrInvalid, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%w: webhook URL must use http or https", ErrInvalid)
	}
	if parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("%w: webhook URL needs a host and no credentials or fragment", ErrInvalid)
	}
	return nil
}
