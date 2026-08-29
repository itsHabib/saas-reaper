package delivery

import (
	"context"
	"testing"
	"time"
)

type managementMemory struct {
	endpoints []Endpoint
	messages  map[string]Message
	published []Delivery
	replayed  Delivery
}

func (m *managementMemory) RegisterEndpoint(_ context.Context, endpoint Endpoint, _ int64) (Endpoint, error) {
	m.endpoints = append(m.endpoints, endpoint)
	return endpoint, nil
}

func (m *managementMemory) DisableEndpoint(
	_ context.Context,
	id string,
	_ int64,
	at time.Time,
) (Endpoint, error) {
	for index := range m.endpoints {
		if m.endpoints[index].ID != id {
			continue
		}
		m.endpoints[index].Enabled = false
		m.endpoints[index].UpdatedAt = at
		return m.endpoints[index], nil
	}
	return Endpoint{}, ErrNotFound
}

func (m *managementMemory) ListEndpoints(context.Context) ([]Endpoint, error) {
	return append([]Endpoint(nil), m.endpoints...), nil
}

func (m *managementMemory) Publish(_ context.Context, message Message, deliveries []Delivery) error {
	if m.messages == nil {
		m.messages = map[string]Message{}
	}
	m.messages[message.ID] = message
	m.published = append([]Delivery(nil), deliveries...)
	return nil
}

func (m *managementMemory) Message(_ context.Context, id string) (Message, error) {
	message, ok := m.messages[id]
	if !ok {
		return Message{}, ErrNotFound
	}
	return message, nil
}

func (m *managementMemory) Endpoint(_ context.Context, id string) (Endpoint, error) {
	for _, endpoint := range m.endpoints {
		if endpoint.ID == id {
			return endpoint, nil
		}
	}
	return Endpoint{}, ErrNotFound
}

func (m *managementMemory) Replay(_ context.Context, replay Delivery) error {
	m.replayed = replay
	return nil
}

func TestServicePreservesPayloadActorAndReplayIdentity(t *testing.T) {
	store := &managementMemory{endpoints: []Endpoint{
		{ID: "enabled", Enabled: true},
		{ID: "disabled", Enabled: false},
	}}
	ids := []string{"msg_one", "del_one", "del_replay"}
	nextID := func(string) (string, error) {
		id := ids[0]
		ids = ids[1:]
		return id, nil
	}
	service, err := NewService(
		store,
		"configured-actor",
		func() time.Time { return time.Unix(10, 0) },
		nextID,
		func() (string, error) { return testSecret, nil },
		NewAttemptCoordinator(),
	)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("{ \"kept\" : true }\n")
	publication, err := service.Publish(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	stored := store.messages[publication.MessageID]
	if string(stored.Payload) != string(payload) || stored.Actor != "configured-actor" {
		t.Fatalf("stored message = %#v", stored)
	}
	if len(store.published) != 1 || store.published[0].EndpointID != "enabled" {
		t.Fatalf("deliveries = %#v", store.published)
	}
	restartedService, err := NewService(
		store,
		"replay-actor",
		func() time.Time { return time.Unix(20, 0) },
		nextID,
		func() (string, error) { return testSecret, nil },
		NewAttemptCoordinator(),
	)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := restartedService.Replay(context.Background(), publication.MessageID, "enabled")
	if err != nil {
		t.Fatal(err)
	}
	if replay.MessageID != publication.MessageID || store.replayed.Kind != DeliveryReplay || store.replayed.Actor != "replay-actor" {
		t.Fatalf("replay = %#v, stored = %#v", replay, store.replayed)
	}
}
