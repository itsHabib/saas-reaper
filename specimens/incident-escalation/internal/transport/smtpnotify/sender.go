// Package smtpnotify pages a responder with one plain SMTP message.
package smtpnotify

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/incident"
)

// Sender is the email channel behind the policy-owned Notifier seam.
//
// An empty address means the channel is not configured: every email page then
// fails permanently and is audited as such rather than retried forever.
type Sender struct {
	address string
	from    string
	timeout time.Duration
}

// New validates the relay address and sender identity.
func New(address, from string, timeout time.Duration) (*Sender, error) {
	if timeout <= 0 {
		return nil, errors.New("a positive SMTP timeout is required")
	}
	if address == "" {
		return &Sender{timeout: timeout}, nil
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		return nil, fmt.Errorf("SMTP address must be host:port: %w", err)
	}
	if strings.TrimSpace(from) == "" || strings.ContainsAny(from, "\r\n<> ") {
		return nil, errors.New("SMTP sender must be a bare address")
	}
	return &Sender{address: address, from: from, timeout: timeout}, nil
}

// Notify delivers one page to the responder's address over an unauthenticated relay.
func (s *Sender) Notify(ctx context.Context, message incident.Message) error {
	if s.address == "" {
		return incident.NewPageError("relay_unconfigured", true)
	}
	to := message.Responder.Email
	if strings.ContainsAny(to, "\r\n") {
		return incident.NewPageError("recipient_invalid", true)
	}
	body := render(message, s.from, to)
	dialer := net.Dialer{Timeout: s.timeout}
	connection, err := dialer.DialContext(ctx, "tcp", s.address)
	if err != nil {
		// A dial error names the relay, so only its class is audited.
		return incident.NewPageError(relayReason(err, "relay_unreachable"), false)
	}
	deadline := time.Now().Add(s.timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		_ = connection.Close()
		return incident.NewPageError("relay_session_failed", false)
	}
	host, _, _ := net.SplitHostPort(s.address)
	client, err := smtp.NewClient(connection, host)
	if err != nil {
		_ = connection.Close()
		return incident.NewPageError(relayReason(err, "relay_session_failed"), false)
	}
	sendErr := send(client, s.from, to, body)
	if sendErr != nil {
		return errors.Join(sendErr, client.Close())
	}
	return nil
}

// send returns audit-safe classifications only: a relay echoes the rejected
// mailbox and its own hostname in reply text, which the audit must never carry.
func send(client *smtp.Client, from, to string, body []byte) error {
	if err := client.Mail(from); err != nil {
		return incident.NewPageError(relayReason(err, "relay_rejected_sender"), false)
	}
	if err := client.Rcpt(to); err != nil {
		return incident.NewPageError(relayReason(err, "relay_rejected_recipient"), false)
	}
	writer, err := client.Data()
	if err != nil {
		return incident.NewPageError(relayReason(err, "relay_rejected_data"), false)
	}
	if _, err := writer.Write(body); err != nil {
		_ = writer.Close()
		return incident.NewPageError("relay_write_failed", false)
	}
	if err := writer.Close(); err != nil {
		return incident.NewPageError(relayReason(err, "relay_rejected_body"), false)
	}
	if err := client.Quit(); err != nil {
		return incident.NewPageError(relayReason(err, "relay_close_failed"), false)
	}
	return nil
}

// relayReason keeps the SMTP status code, which is safe evidence, and discards
// the reply text, which is not.
func relayReason(err error, fallback string) string {
	var reply *textproto.Error
	if errors.As(err, &reply) {
		return "smtp_status_" + strconv.Itoa(reply.Code)
	}
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "timeout"
	}
	return fallback
}

func render(message incident.Message, from, to string) []byte {
	subject := fmt.Sprintf(
		"[%s] %s: %s",
		strings.ToUpper(string(message.Incident.Severity)),
		message.ServiceName,
		message.Incident.Summary,
	)
	subject = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return ' '
		}
		return r
	}, subject)
	var builder strings.Builder
	fmt.Fprintf(&builder, "From: %s\r\n", from)
	fmt.Fprintf(&builder, "To: %s\r\n", to)
	fmt.Fprintf(&builder, "Subject: %s\r\n", subject)
	fmt.Fprintf(&builder, "Date: %s\r\n", message.SentAt.UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&builder, "Message-ID: <%s@reaper-incidents>\r\n", message.NotificationID)
	builder.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n")
	fmt.Fprintf(&builder, "Incident %s is %s (%s).\r\n", message.Incident.ID, message.Incident.State, message.Kind)
	fmt.Fprintf(&builder, "Service: %s\r\n", message.ServiceName)
	fmt.Fprintf(&builder, "Severity: %s\r\n", message.Incident.Severity)
	fmt.Fprintf(&builder, "Source: %s\r\n", message.Incident.Source)
	fmt.Fprintf(&builder, "Dedup key: %s\r\n", message.Incident.DedupKey)
	fmt.Fprintf(&builder, "Escalation level: %d (repeat %d)\r\n", message.Incident.Level, message.Incident.Repeat)
	fmt.Fprintf(&builder, "Opened: %s\r\n", message.Incident.OpenedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&builder, "Notification: %s\r\n", message.NotificationID)
	return []byte(builder.String())
}
