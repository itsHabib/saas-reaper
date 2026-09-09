package delivery

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ManagementStore is the authority consumed by endpoint and publication policy.
type ManagementStore interface {
	RegisterEndpoint(context.Context, Endpoint, int64) (Endpoint, error)
	DisableEndpoint(context.Context, string, int64, time.Time) (Endpoint, error)
	ListEndpoints(context.Context) ([]Endpoint, error)
	Publish(context.Context, Message, []Delivery) ([]Delivery, error)
	Message(context.Context, string) (Message, error)
	Endpoint(context.Context, string) (Endpoint, error)
	Replay(context.Context, Delivery) error
}

// Clock supplies policy timestamps and is replaceable in tests.
type Clock func() time.Time

// Service applies management policy before crossing the persistence boundary.
type Service struct {
	store  ManagementStore
	actor  string
	now    Clock
	ids    IDGenerator
	secret func() (string, error)
}

// Publication identifies the immutable message and queued deliveries.
type Publication struct {
	MessageID   string
	DeliveryIDs []string
}

// NewService binds one configured management principal to every mutation.
func NewService(
	store ManagementStore,
	actor string,
	now Clock,
	ids IDGenerator,
	secret func() (string, error),
) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: management store is required", ErrInvalid)
	}
	if strings.TrimSpace(actor) == "" {
		return nil, fmt.Errorf("%w: configured actor is required", ErrInvalid)
	}
	if now == nil || ids == nil || secret == nil {
		return nil, fmt.Errorf("%w: clock, identifier generator, and secret generator are required", ErrInvalid)
	}
	return &Service{store: store, actor: actor, now: now, ids: ids, secret: secret}, nil
}

// RegisterEndpoint creates the first immutable-secret endpoint revision.
func (s *Service) RegisterEndpoint(
	ctx context.Context,
	destination string,
) (Endpoint, error) {
	id, err := s.ids("ep_")
	if err != nil {
		return Endpoint{}, err
	}
	secret, err := s.secret()
	if err != nil {
		return Endpoint{}, err
	}
	endpoint, err := NewEndpoint(id, destination, secret, s.now())
	if err != nil {
		return Endpoint{}, err
	}
	return s.store.RegisterEndpoint(ctx, endpoint, 0)
}

// DisableEndpoint stops queued and future delivery to an endpoint revision.
//
// The store transaction commits immediately; an attempt whose send overlapped
// the disable is rejected when it tries to record itself.
func (s *Service) DisableEndpoint(ctx context.Context, id string, expectedRevision int64) (Endpoint, error) {
	if !endpointID.MatchString(id) || expectedRevision < 1 {
		return Endpoint{}, fmt.Errorf("%w: valid endpoint id and positive expected revision are required", ErrInvalid)
	}
	return s.store.DisableEndpoint(ctx, id, expectedRevision, s.now().UTC())
}

// Publish stores exact payload bytes and queues one delivery per endpoint still enabled at commit.
func (s *Service) Publish(ctx context.Context, payload []byte) (Publication, error) {
	messageID, err := s.ids("msg_")
	if err != nil {
		return Publication{}, err
	}
	now := s.now().UTC()
	message, err := newMessage(messageID, payload, s.actor, now)
	if err != nil {
		return Publication{}, err
	}
	endpoints, err := s.store.ListEndpoints(ctx)
	if err != nil {
		return Publication{}, fmt.Errorf("list publication endpoints: %w", err)
	}
	deliveries, err := s.deliveries(message.ID, endpoints, DeliveryOriginal, now)
	if err != nil {
		return Publication{}, err
	}
	queued, err := s.store.Publish(ctx, message, deliveries)
	if err != nil {
		return Publication{}, fmt.Errorf("persist publication: %w", err)
	}
	return publicationResult(message.ID, queued), nil
}

// Replay queues one fresh delivery for an existing message and enabled endpoint.
func (s *Service) Replay(ctx context.Context, messageID, endpointID string) (Publication, error) {
	message, err := s.store.Message(ctx, messageID)
	if err != nil {
		return Publication{}, fmt.Errorf("load replay message: %w", err)
	}
	endpoint, err := s.store.Endpoint(ctx, endpointID)
	if err != nil {
		return Publication{}, fmt.Errorf("load replay endpoint: %w", err)
	}
	if !endpoint.Enabled {
		return Publication{}, ErrDisabled
	}
	deliveries, err := s.deliveries(message.ID, []Endpoint{endpoint}, DeliveryReplay, s.now().UTC())
	if err != nil {
		return Publication{}, err
	}
	if err := s.store.Replay(ctx, deliveries[0]); err != nil {
		return Publication{}, fmt.Errorf("persist replay: %w", err)
	}
	return publicationResult(message.ID, deliveries), nil
}

func (s *Service) deliveries(
	messageID string,
	endpoints []Endpoint,
	kind DeliveryKind,
	now time.Time,
) ([]Delivery, error) {
	deliveries := make([]Delivery, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if !endpoint.Enabled {
			continue
		}
		id, err := s.ids("del_")
		if err != nil {
			return nil, err
		}
		deliveries = append(deliveries, newDelivery(id, messageID, endpoint.ID, s.actor, kind, now))
	}
	return deliveries, nil
}

func publicationResult(messageID string, deliveries []Delivery) Publication {
	result := Publication{MessageID: messageID, DeliveryIDs: make([]string, 0, len(deliveries))}
	for _, item := range deliveries {
		result.DeliveryIDs = append(result.DeliveryIDs, item.ID)
	}
	return result
}
