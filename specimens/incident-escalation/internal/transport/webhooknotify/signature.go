package webhooknotify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	headerWebhookID        = "webhook-id"
	headerWebhookSignature = "webhook-signature"
	headerWebhookTimestamp = "webhook-timestamp"
	webhookSecretPrefix    = "whsec_"
)

type signedHeaders struct {
	messageID string
	signature string
	timestamp string
}

// sign follows the Standard Webhooks shape copied from the webhook-delivery specimen:
// HMAC-SHA256 over "{id}.{timestamp}.{payload}" with the decoded whsec_ key.
func sign(secret, messageID string, sentAt time.Time, payload []byte) (signedHeaders, error) {
	if !strings.HasPrefix(secret, webhookSecretPrefix) {
		return signedHeaders{}, fmt.Errorf("responder secret must start with %s", webhookSecretPrefix)
	}
	if messageID == "" || strings.Contains(messageID, ".") {
		return signedHeaders{}, errors.New("webhook id must be non-empty and contain no full stop")
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, webhookSecretPrefix))
	if err != nil {
		return signedHeaders{}, fmt.Errorf("decode responder secret: %w", err)
	}
	if len(key) != 32 {
		return signedHeaders{}, errors.New("responder secret must decode to 32 bytes")
	}
	timestamp := strconv.FormatInt(sentAt.Unix(), 10)
	content := make([]byte, 0, len(messageID)+len(timestamp)+len(payload)+2)
	content = append(content, messageID...)
	content = append(content, '.')
	content = append(content, timestamp...)
	content = append(content, '.')
	content = append(content, payload...)
	mac := hmac.New(sha256.New, key)
	if _, err := mac.Write(content); err != nil {
		return signedHeaders{}, fmt.Errorf("sign page content: %w", err)
	}
	return signedHeaders{
		messageID: messageID,
		signature: "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil)),
		timestamp: timestamp,
	}, nil
}
