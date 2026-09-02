// Package httpdelivery sends one signed attempt without owning retry policy.
package httpdelivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	logger *slog.Logger
}

// New constructs a sender that treats redirects as delivery failures.
func New(timeout time.Duration, logger *slog.Logger) (*Sender, error) {
	if timeout < time.Second || timeout > time.Minute {
		return nil, errors.New("request timeout must be between one second and one minute")
	}
	if logger == nil {
		return nil, errors.New("sender logger is required")
	}
	return &Sender{
		client: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		logger: logger,
	}, nil
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
	}
	result.RetryAfter, result.RetryAt = parseRetryAfter(response.Header.Get("Retry-After"))
	bodyErr := errors.Join(drainErr, closeErr)
	if bodyErr == nil {
		return result, nil
	}
	if !accepted(response.StatusCode) {
		return result, fmt.Errorf("close webhook response: %w", bodyErr)
	}
	// The receiver already accepted the request; a torn response body cannot un-deliver it.
	s.logger.WarnContext(ctx, "webhook accepted before its response body could be drained",
		"status", response.StatusCode, "error", bodyErr)
	return result, nil
}

func accepted(statusCode int) bool {
	return statusCode >= 200 && statusCode <= 299
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

func parseRetryAfter(value string) (time.Duration, time.Time) {
	if value == "" {
		return 0, time.Time{}
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err == nil && seconds >= 0 {
		const maxDuration = time.Duration(1<<63 - 1)
		if seconds > int64(maxDuration/time.Second) {
			return maxDuration, time.Time{}
		}
		return time.Duration(seconds) * time.Second, time.Time{}
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0, time.Time{}
	}
	return 0, when.UTC()
}
