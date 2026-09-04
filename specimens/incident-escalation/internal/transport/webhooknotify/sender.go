// Package webhooknotify pages a responder with one signed HTTP POST.
package webhooknotify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/incident"
)

const maxDrainBytes = 64 << 10

// Sender is the webhook channel behind the policy-owned Notifier seam.
type Sender struct {
	client *http.Client
}

// New builds a sender that never follows redirects and bounds every request.
func New(timeout time.Duration) (*Sender, error) {
	if timeout <= 0 {
		return nil, errors.New("a positive request timeout is required")
	}
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &Sender{client: client}, nil
}

type pagePayload struct {
	Type         string          `json:"type"`
	Notification string          `json:"notificationId"`
	Responder    string          `json:"responder"`
	Service      string          `json:"service"`
	Incident     incidentPayload `json:"incident"`
	SentAt       time.Time       `json:"sentAt"`
}

type incidentPayload struct {
	ID       string            `json:"id"`
	DedupKey string            `json:"dedupKey"`
	State    incident.State    `json:"state"`
	Summary  string            `json:"summary"`
	Source   string            `json:"source"`
	Severity incident.Severity `json:"severity"`
	Level    int               `json:"level"`
	Repeat   int               `json:"repeat"`
	OpenedAt time.Time         `json:"openedAt"`
}

// Notify signs and posts one page; 2xx is delivery, other 4xx is permanent, the rest retries.
func (s *Sender) Notify(ctx context.Context, message incident.Message) error {
	payload, err := json.Marshal(pagePayload{
		Type:         "incident." + string(message.Kind),
		Notification: message.NotificationID,
		Responder:    message.Responder.ID,
		Service:      message.ServiceName,
		Incident: incidentPayload{
			ID:       message.Incident.ID,
			DedupKey: message.Incident.DedupKey,
			State:    message.Incident.State,
			Summary:  message.Incident.Summary,
			Source:   message.Incident.Source,
			Severity: message.Incident.Severity,
			Level:    message.Incident.Level,
			Repeat:   message.Incident.Repeat,
			OpenedAt: message.Incident.OpenedAt,
		},
		SentAt: message.SentAt.UTC(),
	})
	if err != nil {
		return incident.NewPageError("page_encode_failed", true)
	}
	headers, err := sign(message.Responder.WebhookSecret, message.NotificationID, message.SentAt, payload)
	if err != nil {
		return incident.NewPageError("page_signing_failed", true)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, message.Responder.WebhookURL, bytes.NewReader(payload))
	if err != nil {
		return incident.NewPageError("page_request_invalid", true)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "reaper-incidents/1.0")
	request.Header.Set(headerWebhookID, headers.messageID)
	request.Header.Set(headerWebhookTimestamp, headers.timestamp)
	request.Header.Set(headerWebhookSignature, headers.signature)
	response, err := s.client.Do(request)
	if err != nil {
		// A transport error names the destination, so only its class is audited.
		return incident.NewPageError(transportReason(err), false)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxDrainBytes))
	if err := response.Body.Close(); err != nil {
		slog.Debug("close page response body", "error", err)
	}
	return classify(response.StatusCode)
}

func classify(status int) error {
	if status >= 200 && status <= 299 {
		return nil
	}
	reason := "http_status_" + strconv.Itoa(status)
	if status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500 {
		return incident.NewPageError(reason, false)
	}
	return incident.NewPageError(reason, true)
}

// transportReason classifies a failed request without naming its destination.
func transportReason(err error) string {
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "connection_failed"
}
