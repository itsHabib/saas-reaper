// Package httpdelivery sends one signed attempt without owning retry policy.
package httpdelivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/webhook-delivery/internal/delivery"
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

// Send posts exact payload bytes with the Standard Webhooks headers.
func (s *Sender) Send(
	ctx context.Context,
	destination string,
	payload []byte,
	headers delivery.Headers,
) (delivery.SendResult, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, destination, bytes.NewReader(payload))
	if err != nil {
		return delivery.SendResult{}, fmt.Errorf("create webhook request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "saas-reaper-webhook-specimen/1")
	request.Header.Set(delivery.HeaderWebhookID, headers.MessageID)
	request.Header.Set(delivery.HeaderWebhookTimestamp, headers.Timestamp)
	request.Header.Set(delivery.HeaderWebhookSignature, headers.Signature)
	response, err := s.client.Do(request)
	if err != nil {
		return delivery.SendResult{}, redactDestination(err)
	}
	_, drainErr := io.Copy(io.Discard, io.LimitReader(response.Body, responseDrainBytes))
	closeErr := response.Body.Close()
	result := delivery.SendResult{
		StatusCode: response.StatusCode,
		RetryAfter: parseRetryAfter(response.Header.Get("Retry-After"), headers.Timestamp),
	}
	if err := errors.Join(drainErr, closeErr); err != nil {
		return result, fmt.Errorf("close webhook response: %w", err)
	}
	return result, nil
}

func redactDestination(err error) error {
	var requestError *url.Error
	if errors.As(err, &requestError) {
		label := "outbound transport failure"
		if requestError.Timeout() {
			label = "outbound transport timeout"
		}
		return fmt.Errorf("send webhook request: %w", redactedRequestError{
			label: label,
			cause: requestError.Err,
		})
	}
	return fmt.Errorf("send webhook request: %w", redactedRequestError{
		label: "outbound transport failure",
		cause: err,
	})
}

type redactedRequestError struct {
	label string
	cause error
}

func (e redactedRequestError) Error() string {
	return e.label
}

func (e redactedRequestError) Unwrap() error {
	return e.cause
}

func parseRetryAfter(value, webhookTimestamp string) time.Duration {
	if value == "" {
		return 0
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	timestamp, err := strconv.ParseInt(webhookTimestamp, 10, 64)
	if err != nil {
		return 0
	}
	delay := when.Sub(time.Unix(timestamp, 0))
	if delay < 0 {
		return 0
	}
	return delay
}
