package routing

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Notification is one accepted send request and its exact payload bytes.
type Notification struct {
	ID             string
	IdempotencyKey string
	Fingerprint    string
	TemplateKey    string
	RecipientID    string
	Payload        []byte
	Actor          string
	CreatedAt      time.Time
}

// Delivery is the rendered, addressed work for one channel of one notification.
type Delivery struct {
	ID             string
	NotificationID string
	RecipientID    string
	ChannelID      string
	Actor          string
	Address        string
	Subject        string
	Body           string
	State          DeliveryState
	AttemptCount   int
	NextAttemptAt  time.Time
	CreatedAt      time.Time
}

// DeliveryState is the persisted state of one channel delivery.
type DeliveryState string

const (
	// StatePending can be attempted when its due time arrives.
	StatePending DeliveryState = "pending"
	// StateDelivered records a terminal transport acceptance.
	StateDelivered DeliveryState = "delivered"
	// StateFailed records a terminal permanent transport rejection.
	StateFailed DeliveryState = "failed"
	// StateExhausted records a terminal exhausted retry budget.
	StateExhausted DeliveryState = "exhausted"
	// StateCanceled records cancellation by channel disablement.
	StateCanceled DeliveryState = "canceled"
)

// Fingerprint binds an idempotency key to the exact send it first accepted.
func Fingerprint(templateKey, recipientID string, payload []byte) string {
	digest := sha256.New()
	digest.Write([]byte(templateKey))
	digest.Write([]byte{0})
	digest.Write([]byte(recipientID))
	digest.Write([]byte{0})
	digest.Write(payload)
	return hex.EncodeToString(digest.Sum(nil))
}

func newNotification(id, key, templateKey, recipientID string, payload []byte, actor string, now time.Time) (Notification, error) {
	if !strings.HasPrefix(id, "ntf_") {
		return Notification{}, fmt.Errorf("%w: generated notification id is unsafe", ErrInvalid)
	}
	if !idempotencyKey.MatchString(key) {
		return Notification{}, fmt.Errorf("%w: idempotency key must be 1-128 printable key characters", ErrInvalid)
	}
	if strings.TrimSpace(actor) == "" {
		return Notification{}, fmt.Errorf("%w: configured actor is required", ErrInvalid)
	}
	return Notification{
		ID:             id,
		IdempotencyKey: key,
		Fingerprint:    Fingerprint(templateKey, recipientID, payload),
		TemplateKey:    templateKey,
		RecipientID:    recipientID,
		Payload:        append([]byte(nil), payload...),
		Actor:          actor,
		CreatedAt:      now.UTC(),
	}, nil
}

func newDelivery(id string, notification Notification, binding Binding, rendered Rendered, now time.Time) Delivery {
	return Delivery{
		ID:             id,
		NotificationID: notification.ID,
		RecipientID:    notification.RecipientID,
		ChannelID:      binding.ChannelID,
		Actor:          notification.Actor,
		Address:        binding.Address,
		Subject:        rendered.Subject,
		Body:           rendered.Body,
		State:          StatePending,
		NextAttemptAt:  now.UTC(),
		CreatedAt:      now.UTC(),
	}
}
