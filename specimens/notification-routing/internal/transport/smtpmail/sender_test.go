package smtpmail

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"net"
	"net/mail"
	"strings"
	"sync"
	"testing"
	"time"

	gosmtp "github.com/emersion/go-smtp"

	"github.com/itsHabib/saas-reaper/specimens/notification-routing/internal/routing"
)

type fixtureBackend struct {
	mu       sync.Mutex
	messages []string
	rcptCode int
	dataCode int
}

type fixtureSession struct {
	backend *fixtureBackend
}

func (b *fixtureBackend) NewSession(*gosmtp.Conn) (gosmtp.Session, error) {
	return &fixtureSession{backend: b}, nil
}

func (*fixtureSession) Reset() {}

func (*fixtureSession) Logout() error { return nil }

func (*fixtureSession) Mail(string, *gosmtp.MailOptions) error { return nil }

func (s *fixtureSession) Rcpt(string, *gosmtp.RcptOptions) error {
	if s.backend.rcptCode != 0 {
		return &gosmtp.SMTPError{Code: s.backend.rcptCode, EnhancedCode: gosmtp.EnhancedCode{5, 1, 1}, Message: "no such user"}
	}
	return nil
}

func (s *fixtureSession) Data(r io.Reader) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if s.backend.dataCode != 0 {
		return &gosmtp.SMTPError{Code: s.backend.dataCode, EnhancedCode: gosmtp.EnhancedCode{4, 7, 0}, Message: "try later"}
	}
	s.backend.mu.Lock()
	defer s.backend.mu.Unlock()
	s.backend.messages = append(s.backend.messages, string(raw))
	return nil
}

func startFixture(t *testing.T, backend *fixtureBackend) string {
	t.Helper()
	listener, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := gosmtp.NewServer(backend)
	server.Domain = "fixture.local"
	server.MaxMessageBytes = 1 << 20
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return listener.Addr().String()
}

func newSender(t *testing.T, address string) *Sender {
	t.Helper()
	sender, err := New(Config{Address: address, From: "reaper@sender.example", Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return sender
}

func testEnvelope() routing.Envelope {
	return routing.Envelope{
		DeliveryID:     "del_one",
		NotificationID: "ntf_one",
		Address:        "billing@acme.example",
		Subject:        "Invoice inv_1 paid — thanks",
		Body:           "Hello Acme,\n\n4200 usd received.\n",
		Attempt:        2,
		AttemptedAt:    time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC),
	}
}

func TestDeliverSendsParsableMessageWithStableMessageID(t *testing.T) {
	backend := &fixtureBackend{}
	sender := newSender(t, startFixture(t, backend))
	receipt, err := sender.Deliver(context.Background(), testEnvelope())
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Code != 250 {
		t.Fatalf("receipt = %#v", receipt)
	}
	if len(backend.messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(backend.messages))
	}
	parsed, err := mail.ReadMessage(strings.NewReader(backend.messages[0]))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Header.Get("Message-ID") != "<del_one@sender.example>" || parsed.Header.Get("X-Reaper-Attempt") != "2" {
		t.Fatalf("headers = %#v", parsed.Header)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
	if err != nil {
		t.Fatal(err)
	}
	if subject != "Invoice inv_1 paid — thanks" {
		t.Fatalf("subject = %q", subject)
	}
	body, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, parsed.Body))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != testEnvelope().Body {
		t.Fatalf("body = %q", body)
	}
}

func TestDeliverClassifiesReplies(t *testing.T) {
	tests := []struct {
		name      string
		backend   *fixtureBackend
		permanent bool
		wantCode  int
	}{
		{name: "4xx at data is transient", backend: &fixtureBackend{dataCode: 451}, wantCode: 451},
		{name: "5xx at rcpt is permanent", backend: &fixtureBackend{rcptCode: 550}, permanent: true, wantCode: 550},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sender := newSender(t, startFixture(t, test.backend))
			receipt, err := sender.Deliver(context.Background(), testEnvelope())
			if err == nil {
				t.Fatal("delivery unexpectedly succeeded")
			}
			if errors.Is(err, routing.ErrPermanent) != test.permanent {
				t.Fatalf("permanent = %t, want %t: %v", errors.Is(err, routing.ErrPermanent), test.permanent, err)
			}
			if receipt.Code != test.wantCode {
				t.Fatalf("receipt = %#v, want code %d", receipt, test.wantCode)
			}
		})
	}
}

func TestDeliverRedactsRelayFromConnectionFailures(t *testing.T) {
	listener, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	sender := newSender(t, address)
	_, err = sender.Deliver(context.Background(), testEnvelope())
	if err == nil {
		t.Fatal("delivery to a closed port unexpectedly succeeded")
	}
	if errors.Is(err, routing.ErrPermanent) {
		t.Fatalf("connection failure classified permanent: %v", err)
	}
	if strings.Contains(err.Error(), address) || strings.Contains(err.Error(), "acme.example") {
		t.Fatalf("error exposed relay or mailbox: %q", err)
	}
	var networkError net.Error
	if !errors.As(err, &networkError) {
		t.Fatalf("error lost its network cause: %v", err)
	}
}

func TestNewRejectsIncompleteRelayConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{name: "no port", config: Config{Address: "127.0.0.1", From: "a@b.example", Timeout: time.Second}},
		{name: "display name sender", config: Config{Address: "127.0.0.1:25", From: "A <a@b.example>", Timeout: time.Second}},
		{name: "half credentials", config: Config{Address: "127.0.0.1:25", From: "a@b.example", Username: "u", Timeout: time.Second}},
		{name: "timeout too small", config: Config{Address: "127.0.0.1:25", From: "a@b.example", Timeout: time.Millisecond}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := New(test.config); err == nil {
				t.Fatal("configuration unexpectedly accepted")
			}
		})
	}
}
