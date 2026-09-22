// Package smtpmail speaks SMTP to the customer's own relay without owning retry policy.
package smtpmail

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/notification-routing/internal/routing"
)

const (
	acceptedReplyCode = 250
	base64LineBytes   = 76
	helloName         = "reaper-notifications"
)

// Config is the customer-owned relay the sender speaks to.
type Config struct {
	Address  string
	From     string
	Username string
	Password string
	Timeout  time.Duration
}

// Sender delivers one rendered envelope per SMTP session.
type Sender struct {
	config Config
	domain string
}

// New validates the relay configuration once at composition time.
func New(config Config) (*Sender, error) {
	host, _, err := net.SplitHostPort(config.Address)
	if err != nil || host == "" {
		return nil, fmt.Errorf("smtp address must be host:port: %w", err)
	}
	at := strings.LastIndexByte(config.From, '@')
	if strings.ContainsAny(config.From, " <>\r\n") || at < 1 || at == len(config.From)-1 {
		return nil, errors.New("smtp sender address must be a bare mailbox")
	}
	if (config.Username == "") != (config.Password == "") {
		return nil, errors.New("smtp username and password must be set together")
	}
	if config.Timeout < time.Second || config.Timeout > time.Minute {
		return nil, errors.New("smtp timeout must be between one second and one minute")
	}
	return &Sender{config: config, domain: config.From[at+1:]}, nil
}

// Deliver runs one complete SMTP transaction and classifies the reply.
//
// A 5xx reply wraps routing.ErrPermanent; 4xx replies and transport failures are transient.
func (s *Sender) Deliver(ctx context.Context, envelope routing.Envelope) (routing.Receipt, error) {
	ctx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()
	client, err := s.dial(ctx)
	if err != nil {
		return routing.Receipt{}, classify(fmt.Errorf("connect smtp relay: %w", err))
	}
	defer func() { _ = client.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stop()
	if err := s.transact(client, envelope); err != nil {
		receipt := receiptFor(err)
		return receipt, classify(err)
	}
	return routing.Receipt{Code: acceptedReplyCode}, nil
}

func (s *Sender) dial(ctx context.Context) (*smtp.Client, error) {
	dialer := net.Dialer{Timeout: s.config.Timeout}
	connection, err := dialer.DialContext(ctx, "tcp", s.config.Address)
	if err != nil {
		return nil, err
	}
	host, _, _ := net.SplitHostPort(s.config.Address)
	client, err := smtp.NewClient(connection, host)
	if err != nil {
		return nil, errors.Join(err, connection.Close())
	}
	return client, nil
}

func (s *Sender) transact(client *smtp.Client, envelope routing.Envelope) error {
	if err := client.Hello(helloName); err != nil {
		return fmt.Errorf("smtp hello: %w", err)
	}
	if err := s.secure(client); err != nil {
		return err
	}
	if err := client.Mail(s.config.From); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := client.Rcpt(envelope.Address); err != nil {
		return fmt.Errorf("smtp rcpt to: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	_, writeErr := writer.Write(s.message(envelope))
	closeErr := writer.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return fmt.Errorf("smtp message: %w", err)
	}
	// The relay accepted the message at DATA completion; a failed QUIT is not a delivery failure.
	_ = client.Quit()
	return nil
}

func (s *Sender) secure(client *smtp.Client) error {
	host, _, _ := net.SplitHostPort(s.config.Address)
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if s.config.Username == "" {
		return nil
	}
	if err := client.Auth(smtp.PlainAuth("", s.config.Username, s.config.Password, host)); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}
	return nil
}

// message renders RFC 5322 bytes. Message-ID is the delivery identity, so every retry of one
// delivery carries the same ID and a mailbox can collapse at-least-once duplicates.
func (s *Sender) message(envelope routing.Envelope) []byte {
	var buffer bytes.Buffer
	fmt.Fprintf(&buffer, "From: <%s>\r\n", s.config.From)
	fmt.Fprintf(&buffer, "To: <%s>\r\n", envelope.Address)
	fmt.Fprintf(&buffer, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", envelope.Subject))
	fmt.Fprintf(&buffer, "Date: %s\r\n", envelope.AttemptedAt.UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&buffer, "Message-ID: <%s@%s>\r\n", envelope.DeliveryID, s.domain)
	fmt.Fprintf(&buffer, "X-Reaper-Notification: %s\r\n", envelope.NotificationID)
	fmt.Fprintf(&buffer, "X-Reaper-Attempt: %d\r\n", envelope.Attempt)
	buffer.WriteString("MIME-Version: 1.0\r\n")
	buffer.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	buffer.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	encoded := base64.StdEncoding.EncodeToString([]byte(envelope.Body))
	for len(encoded) > base64LineBytes {
		buffer.WriteString(encoded[:base64LineBytes])
		buffer.WriteString("\r\n")
		encoded = encoded[base64LineBytes:]
	}
	buffer.WriteString(encoded)
	buffer.WriteString("\r\n")
	return buffer.Bytes()
}

func receiptFor(err error) routing.Receipt {
	var reply *textproto.Error
	if errors.As(err, &reply) {
		return routing.Receipt{Code: reply.Code}
	}
	return routing.Receipt{}
}

// classify decides terminality and redacts in one place, because the returned error is what
// the dispatcher persists in the attempt audit and the lower-authority read token can see.
//
// A reply code is safe structured evidence and is kept. Reply text is not: relays routinely
// echo the rejected mailbox or their own hostname into it. Every path therefore returns a
// fixed label, with the original preserved through Unwrap for local diagnosis only.
func classify(err error) error {
	var reply *textproto.Error
	if errors.As(err, &reply) {
		redacted := redactedError{label: "smtp relay replied " + strconv.Itoa(reply.Code), cause: err}
		if reply.Code >= 500 && reply.Code <= 599 {
			return fmt.Errorf("%w: %w", routing.ErrPermanent, redacted)
		}
		return redacted
	}
	return redact(err)
}

// redact keeps the failure class and cause chain but never the relay address or mailbox.
func redact(err error) error {
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return redactedError{label: "smtp transport timeout", cause: err}
	}
	return redactedError{label: "smtp transport failure", cause: err}
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
