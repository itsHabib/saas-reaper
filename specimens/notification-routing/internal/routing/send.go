package routing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ManagementStore is the authority consumed by channel, template, recipient, and send policy.
type ManagementStore interface {
	RegisterChannel(context.Context, Channel) error
	DisableChannel(context.Context, string, int64, time.Time) (Channel, error)
	ListChannels(context.Context) ([]Channel, error)
	CreateTemplate(context.Context, Template) error
	Templates(context.Context, string) ([]Template, error)
	CreateRecipient(context.Context, Recipient) error
	Recipient(context.Context, string) (Recipient, error)
	AcceptedNotification(context.Context, string) (Acceptance, string, error)
	Send(context.Context, Notification, []Delivery) (Acceptance, error)
}

// Clock supplies policy timestamps and is replaceable in tests.
type Clock func() time.Time

// SendRequest is the validated intent behind POST /v1/notifications.
type SendRequest struct {
	TemplateKey    string
	RecipientID    string
	Payload        []byte
	IdempotencyKey string
}

// QueuedDelivery identifies one channel delivery accepted for a notification.
type QueuedDelivery struct {
	ID        string
	ChannelID string
}

// Acceptance is the durable answer to a send, first time or deduplicated.
type Acceptance struct {
	NotificationID string
	Deduplicated   bool
	Deliveries     []QueuedDelivery
}

// Service applies management policy before crossing the persistence boundary.
type Service struct {
	store ManagementStore
	actor string
	now   Clock
	ids   IDGenerator
}

// NewService binds one configured management principal to every mutation.
func NewService(store ManagementStore, actor string, now Clock, ids IDGenerator) (*Service, error) {
	if store == nil || now == nil || ids == nil {
		return nil, fmt.Errorf("%w: management store, clock, and identifier generator are required", ErrInvalid)
	}
	if strings.TrimSpace(actor) == "" {
		return nil, fmt.Errorf("%w: configured actor is required", ErrInvalid)
	}
	return &Service{store: store, actor: actor, now: now, ids: ids}, nil
}

// RegisterChannel creates the first revision of a channel bound to one transport kind.
func (s *Service) RegisterChannel(ctx context.Context, id string, kind ChannelKind) (Channel, error) {
	channel, err := NewChannel(id, kind, s.now())
	if err != nil {
		return Channel{}, err
	}
	if err := s.store.RegisterChannel(ctx, channel); err != nil {
		return Channel{}, fmt.Errorf("persist channel %s: %w", id, err)
	}
	return channel, nil
}

// DisableChannel stops queued and future delivery on a channel revision.
func (s *Service) DisableChannel(ctx context.Context, id string, expectedRevision int64) (Channel, error) {
	if err := validateOwnedID("channel", id); err != nil {
		return Channel{}, err
	}
	if expectedRevision < 1 {
		return Channel{}, fmt.Errorf("%w: positive expected revision is required", ErrInvalid)
	}
	return s.store.DisableChannel(ctx, id, expectedRevision, s.now().UTC())
}

// CreateTemplate stores one channel variant of a named notification.
func (s *Service) CreateTemplate(ctx context.Context, key, channelID, subject, body string) (Template, error) {
	template, err := NewTemplate(key, channelID, subject, body, s.now())
	if err != nil {
		return Template{}, err
	}
	if err := s.store.CreateTemplate(ctx, template); err != nil {
		return Template{}, fmt.Errorf("persist template %s/%s: %w", key, channelID, err)
	}
	return template, nil
}

// CreateRecipient stores a recipient whose addresses match their channel kinds.
func (s *Service) CreateRecipient(ctx context.Context, id string, bindings []Binding) (Recipient, error) {
	channels, err := s.store.ListChannels(ctx)
	if err != nil {
		return Recipient{}, fmt.Errorf("list channels for recipient %s: %w", id, err)
	}
	recipient, err := NewRecipient(id, bindings, channels, s.now())
	if err != nil {
		return Recipient{}, err
	}
	if err := s.store.CreateRecipient(ctx, recipient); err != nil {
		return Recipient{}, fmt.Errorf("persist recipient %s: %w", id, err)
	}
	return recipient, nil
}

// Send renders every deliverable channel variant first, then queues all of them atomically.
func (s *Service) Send(ctx context.Context, request SendRequest) (Acceptance, error) {
	if err := validateOwnedID("template", request.TemplateKey); err != nil {
		return Acceptance{}, err
	}
	if err := validateOwnedID("recipient", request.RecipientID); err != nil {
		return Acceptance{}, err
	}
	payload, err := ParsePayload(request.Payload)
	if err != nil {
		return Acceptance{}, err
	}
	id, err := s.ids("ntf_")
	if err != nil {
		return Acceptance{}, err
	}
	now := s.now().UTC()
	notification, err := newNotification(
		id, request.IdempotencyKey, request.TemplateKey, request.RecipientID, request.Payload, s.actor, now,
	)
	if err != nil {
		return Acceptance{}, err
	}
	settled, resolved, err := s.resolveIdempotencyKey(ctx, notification)
	if err != nil {
		return Acceptance{}, err
	}
	if settled {
		return resolved, nil
	}
	targets, err := s.loadTargets(ctx, request.TemplateKey, request.RecipientID)
	if err != nil {
		return Acceptance{}, err
	}
	deliveries, err := s.renderDeliveries(notification, payload, targets, now)
	if err != nil {
		return Acceptance{}, err
	}
	acceptance, err := s.store.Send(ctx, notification, deliveries)
	if err != nil {
		return Acceptance{}, fmt.Errorf("persist notification: %w", err)
	}
	return acceptance, nil
}

// resolveIdempotencyKey settles a reused key before any target loading or rendering, so key
// reuse answers the same way whether or not the replacement request would independently
// validate. Store.Send stays the arbiter for keys first used concurrently with this one.
func (s *Service) resolveIdempotencyKey(ctx context.Context, notification Notification) (bool, Acceptance, error) {
	accepted, fingerprint, err := s.store.AcceptedNotification(ctx, notification.IdempotencyKey)
	if errors.Is(err, ErrNotFound) {
		return false, Acceptance{}, nil
	}
	if err != nil {
		return false, Acceptance{}, fmt.Errorf("load idempotency key: %w", err)
	}
	if fingerprint != notification.Fingerprint {
		return false, Acceptance{}, fmt.Errorf(
			"%w: idempotency key %s was accepted for a different template, recipient, or payload",
			ErrConflict, notification.IdempotencyKey,
		)
	}
	return true, accepted, nil
}

type sendTargets struct {
	recipient Recipient
	variants  map[string]Template
	channels  map[string]Channel
}

func (s *Service) loadTargets(ctx context.Context, templateKey, recipientID string) (sendTargets, error) {
	variants, err := s.store.Templates(ctx, templateKey)
	if err != nil {
		return sendTargets{}, fmt.Errorf("load template %s: %w", templateKey, err)
	}
	if len(variants) == 0 {
		return sendTargets{}, fmt.Errorf("%w: template %s", ErrNotFound, templateKey)
	}
	recipient, err := s.store.Recipient(ctx, recipientID)
	if err != nil {
		return sendTargets{}, fmt.Errorf("load recipient %s: %w", recipientID, err)
	}
	channels, err := s.store.ListChannels(ctx)
	if err != nil {
		return sendTargets{}, fmt.Errorf("list channels: %w", err)
	}
	targets := sendTargets{
		recipient: recipient,
		variants:  make(map[string]Template, len(variants)),
		channels:  make(map[string]Channel, len(channels)),
	}
	for _, variant := range variants {
		targets.variants[variant.ChannelID] = variant
	}
	for _, channel := range channels {
		targets.channels[channel.ID] = channel
	}
	return targets, nil
}

// renderDeliveries applies the fan-out rule: a binding the recipient enabled, on an enabled
// channel, for which the template has a variant. Rendering failures reject the whole send.
func (s *Service) renderDeliveries(
	notification Notification,
	payload Payload,
	targets sendTargets,
	now time.Time,
) ([]Delivery, error) {
	deliveries := make([]Delivery, 0, len(targets.recipient.Bindings))
	for _, binding := range targets.recipient.Bindings {
		channel, variant, deliverable := targets.route(binding)
		if !deliverable {
			continue
		}
		rendered, err := variant.Render(payload, channel.Kind)
		if err != nil {
			return nil, err
		}
		id, err := s.ids("del_")
		if err != nil {
			return nil, err
		}
		deliveries = append(deliveries, newDelivery(id, notification, binding, rendered, now))
	}
	return deliveries, nil
}

func (t sendTargets) route(binding Binding) (Channel, Template, bool) {
	if !binding.Enabled {
		return Channel{}, Template{}, false
	}
	channel, exists := t.channels[binding.ChannelID]
	if !exists || !channel.Enabled {
		return Channel{}, Template{}, false
	}
	variant, exists := t.variants[binding.ChannelID]
	if !exists {
		return Channel{}, Template{}, false
	}
	return channel, variant, true
}
