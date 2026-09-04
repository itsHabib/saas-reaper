package delivery

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// MaxPayloadBytes is the largest exact payload accepted for signing and delivery.
const MaxPayloadBytes = 1 << 20

// Message preserves the exact bytes signed and sent for one logical event.
type Message struct {
	ID        string
	Payload   []byte
	Actor     string
	CreatedAt time.Time
}

// Delivery tracks one original or replay dispatch of a message to an endpoint.
type Delivery struct {
	ID            string
	MessageID     string
	EndpointID    string
	Actor         string
	Kind          DeliveryKind
	State         DeliveryState
	AttemptCount  int
	NextAttemptAt time.Time
	CreatedAt     time.Time
}

// DeliveryKind distinguishes automatic fanout from a manual replay.
//
//nolint:revive // DeliveryKind is explicit at persistence and transport boundaries.
type DeliveryKind string

const (
	// DeliveryOriginal is the initial fanout created with a message.
	DeliveryOriginal DeliveryKind = "original"
	// DeliveryReplay is an operator-requested new delivery of a stored message.
	DeliveryReplay DeliveryKind = "replay"
)

// DeliveryState is the persisted retry state.
//
//nolint:revive // DeliveryState is explicit at persistence and transport boundaries.
type DeliveryState string

const (
	// StatePending can be attempted when its due time arrives.
	StatePending DeliveryState = "pending"
	// StateSucceeded records a terminal 2xx delivery.
	StateSucceeded DeliveryState = "succeeded"
	// StateExhausted records a terminal exhausted retry budget.
	StateExhausted DeliveryState = "exhausted"
	// StateDisabled records cancellation by endpoint disablement.
	StateDisabled DeliveryState = "disabled"
	// StateFailed records a terminal permanent failure that no retry can repair.
	StateFailed DeliveryState = "failed"
)

func newMessage(id string, payload []byte, actor string, now time.Time) (Message, error) {
	if !strings.HasPrefix(id, "msg_") || strings.Contains(id, ".") {
		return Message{}, fmt.Errorf("%w: generated message id is unsafe", ErrInvalid)
	}
	if len(payload) == 0 || len(payload) > MaxPayloadBytes {
		return Message{}, fmt.Errorf("%w: payload must contain 1-%d bytes", ErrInvalid, MaxPayloadBytes)
	}
	if !json.Valid(payload) {
		return Message{}, fmt.Errorf("%w: payload must be one valid JSON value", ErrInvalid)
	}
	if strings.TrimSpace(actor) == "" {
		return Message{}, fmt.Errorf("%w: configured actor is required", ErrInvalid)
	}
	return Message{
		ID:        id,
		Payload:   append([]byte(nil), payload...),
		Actor:     actor,
		CreatedAt: now.UTC(),
	}, nil
}

func newDelivery(id, messageID, endpointID, actor string, kind DeliveryKind, now time.Time) Delivery {
	return Delivery{
		ID:            id,
		MessageID:     messageID,
		EndpointID:    endpointID,
		Actor:         actor,
		Kind:          kind,
		State:         StatePending,
		NextAttemptAt: now.UTC(),
		CreatedAt:     now.UTC(),
	}
}
