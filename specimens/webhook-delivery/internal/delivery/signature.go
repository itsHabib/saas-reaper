package delivery

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// HeaderWebhookID carries the stable message identity across retries and replays.
	HeaderWebhookID = "webhook-id"
	// HeaderWebhookSignature carries the versioned HMAC signature.
	HeaderWebhookSignature = "webhook-signature"
	// HeaderWebhookTimestamp carries the Unix timestamp for the current attempt.
	HeaderWebhookTimestamp = "webhook-timestamp"
)

// Headers are the Standard Webhooks authentication headers for one attempt.
type Headers struct {
	MessageID string
	Signature string
	Timestamp string
}

// Sign authenticates the exact payload bytes and attempt metadata.
func Sign(secret, messageID string, attemptedAt time.Time, payload []byte) (Headers, error) {
	if err := validateSecret(secret); err != nil {
		return Headers{}, err
	}
	if messageID == "" || strings.Contains(messageID, ".") {
		return Headers{}, fmt.Errorf("%w: webhook id must be non-empty and contain no full stop", ErrInvalid)
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, webhookSecretPrefix))
	if err != nil {
		return Headers{}, fmt.Errorf("decode signing secret: %w", err)
	}
	timestamp := strconv.FormatInt(attemptedAt.Unix(), 10)
	content := make([]byte, 0, len(messageID)+len(timestamp)+len(payload)+2)
	content = append(content, messageID...)
	content = append(content, '.')
	content = append(content, timestamp...)
	content = append(content, '.')
	content = append(content, payload...)
	mac := hmac.New(sha256.New, key)
	if _, err := mac.Write(content); err != nil {
		return Headers{}, fmt.Errorf("sign webhook content: %w", err)
	}
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return Headers{
		MessageID: messageID,
		Signature: "v1," + signature,
		Timestamp: timestamp,
	}, nil
}
