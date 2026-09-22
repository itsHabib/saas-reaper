package routing

import (
	"fmt"
	"time"
)

const maxBindings = 8

// Binding is one recipient address on one channel plus the recipient's preference for it.
type Binding struct {
	ChannelID string
	Address   string
	Enabled   bool
}

// Recipient is a customer-owned identity with per-channel addresses.
type Recipient struct {
	ID        string
	Bindings  []Binding
	CreatedAt time.Time
}

// NewRecipient validates every address against the kind of the channel it binds.
func NewRecipient(id string, bindings []Binding, channels []Channel, now time.Time) (Recipient, error) {
	if err := validateOwnedID("recipient", id); err != nil {
		return Recipient{}, err
	}
	if len(bindings) == 0 || len(bindings) > maxBindings {
		return Recipient{}, fmt.Errorf("%w: recipient needs 1-%d channel bindings", ErrInvalid, maxBindings)
	}
	kinds := make(map[string]ChannelKind, len(channels))
	for _, channel := range channels {
		kinds[channel.ID] = channel.Kind
	}
	seen := make(map[string]bool, len(bindings))
	copied := make([]Binding, 0, len(bindings))
	for _, binding := range bindings {
		if err := validateBinding(binding, kinds, seen); err != nil {
			return Recipient{}, err
		}
		seen[binding.ChannelID] = true
		copied = append(copied, binding)
	}
	return Recipient{ID: id, Bindings: copied, CreatedAt: now.UTC()}, nil
}

func validateBinding(binding Binding, kinds map[string]ChannelKind, seen map[string]bool) error {
	kind, known := kinds[binding.ChannelID]
	if !known {
		return fmt.Errorf("%w: channel %q", ErrNotFound, binding.ChannelID)
	}
	if seen[binding.ChannelID] {
		return fmt.Errorf("%w: channel %q is bound twice", ErrInvalid, binding.ChannelID)
	}
	if err := validateAddress(kind, binding.Address); err != nil {
		return fmt.Errorf("channel %s: %w", binding.ChannelID, err)
	}
	return nil
}
