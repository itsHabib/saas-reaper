// Package slackwebhook posts Slack-compatible incoming-webhook payloads without owning retry policy.
package slackwebhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/notification-routing/internal/routing"
)

const responseDrainBytes = 4 << 10

// Sender owns the bounded outbound HTTP client.
type Sender struct {
	client *http.Client
}

// New constructs a sender that treats redirects as delivery failures.
func New(timeout time.Duration) (*Sender, error) {
	if timeout < time.Second || timeout > time.Minute {
		return nil, errors.New("request timeout must be between one second and one minute")
	}
	return &Sender{client: &http.Client{
		Timeout: timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}, nil
}

type webhookPayload struct {
	Text string `json:"text"`
}

// Deliver posts the documented minimal incoming-webhook body: a JSON object with "text".
//
// 2xx is accepted. 408, 429, and 5xx are transient. Every other status wraps routing.ErrPermanent,
// matching Slack's documented permanent errors (invalid_payload, channel_not_found, and so on).
func (s *Sender) Deliver(ctx context.Context, envelope routing.Envelope) (routing.Receipt, error) {
	body, err := json.Marshal(webhookPayload{Text: envelope.Body})
	if err != nil {
		return routing.Receipt{}, fmt.Errorf("%w: encode webhook payload: %w", routing.ErrPermanent, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, envelope.Address, bytes.NewReader(body))
	if err != nil {
		return routing.Receipt{}, fmt.Errorf("%w: create webhook request: %w", routing.ErrPermanent, err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "saas-reaper-notification-specimen/1")
	request.Header.Set("X-Reaper-Delivery", envelope.DeliveryID)
	request.Header.Set("X-Reaper-Attempt", strconv.Itoa(envelope.Attempt))
	response, err := s.client.Do(request)
	if err != nil {
		return routing.Receipt{}, redact(err)
	}
	// The body is drained so the connection can be reused, and discarded: it is
	// remote-controlled text and nothing that crosses this seam may carry it.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, responseDrainBytes))
	_ = response.Body.Close()
	receipt := routing.Receipt{Code: response.StatusCode}
	return receipt, classify(receipt)
}

func classify(receipt routing.Receipt) error {
	if receipt.Code >= 200 && receipt.Code <= 299 {
		return nil
	}
	transient := receipt.Code == http.StatusRequestTimeout ||
		receipt.Code == http.StatusTooManyRequests ||
		receipt.Code >= 500
	if transient {
		return fmt.Errorf("webhook status %d", receipt.Code)
	}
	return fmt.Errorf("%w: webhook status %d", routing.ErrPermanent, receipt.Code)
}

func redact(err error) error {
	var requestError *url.Error
	if errors.As(err, &requestError) {
		label := "webhook transport failure"
		if requestError.Timeout() {
			label = "webhook transport timeout"
		}
		return fmt.Errorf("send webhook request: %w", redactedError{label: label, cause: requestError.Err})
	}
	return fmt.Errorf("send webhook request: %w", redactedError{label: "webhook transport failure", cause: err})
}

type redactedError struct {
	label string
	cause error
}

func (e redactedError) Error() string {
	return e.label
}

func (e redactedError) Unwrap() error {
	return e.cause
}
