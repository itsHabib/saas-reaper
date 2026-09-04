package smtpnotify

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/incident"
)

// sink is a minimal SMTP responder: enough of the protocol for net/smtp to
// complete one unauthenticated session against loopback.
type sink struct {
	mu       sync.Mutex
	messages []string
	reject   bool
}

func (s *sink) start(t *testing.T) string {
	t.Helper()
	var config net.ListenConfig
	listener, err := config.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go s.serve(connection)
		}
	}()
	return listener.Addr().String()
}

func (s *sink) serve(connection net.Conn) {
	defer func() { _ = connection.Close() }()
	reader := bufio.NewReader(connection)
	writer := bufio.NewWriter(connection)
	reply := func(line string) {
		_, _ = writer.WriteString(line + "\r\n")
		_ = writer.Flush()
	}
	reply("220 sink ready")
	var body strings.Builder
	inData := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if inData {
			if trimmed == "." {
				inData = false
				s.mu.Lock()
				s.messages = append(s.messages, body.String())
				s.mu.Unlock()
				body.Reset()
				reply("250 queued")
				continue
			}
			body.WriteString(trimmed + "\n")
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "EHLO"), strings.HasPrefix(trimmed, "HELO"):
			reply("250-sink")
			reply("250 SIZE 1000000")
		case strings.HasPrefix(trimmed, "MAIL FROM"):
			reply("250 ok")
		case strings.HasPrefix(trimmed, "RCPT TO"):
			if s.reject {
				reply("550 no such mailbox")
				continue
			}
			reply("250 ok")
		case trimmed == "DATA":
			inData = true
			reply("354 send it")
		case trimmed == "QUIT":
			reply("221 bye")
			return
		default:
			reply("250 ok")
		}
	}
}

func (s *sink) received() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.messages...)
}

func pageMessage() incident.Message {
	return incident.Message{
		NotificationID: "ntf_0001",
		Kind:           incident.EventOpened,
		Responder:      incident.Responder{ID: "alice", Email: "alice@example.test"},
		Incident: incident.Incident{
			ID:       "inc_0001",
			DedupKey: "checkout-5xx",
			State:    incident.StateTriggered,
			Summary:  "checkout is down",
			Source:   "prometheus",
			Severity: incident.SeverityCritical,
			Level:    1,
			Repeat:   0,
			OpenedAt: time.Unix(1772450000, 0).UTC(),
		},
		ServiceName: "Checkout",
		SentAt:      time.Unix(1772450100, 0).UTC(),
	}
}

func TestEmailPageCarriesTheIncidentContext(t *testing.T) {
	relay := &sink{}
	address := relay.start(t)
	sender, err := New(address, "pager@reaper.invalid", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.Notify(context.Background(), pageMessage()); err != nil {
		t.Fatal(err)
	}
	messages := relay.received()
	if len(messages) != 1 {
		t.Fatalf("expected one message, got %d", len(messages))
	}
	body := messages[0]
	required := []string{
		"From: pager@reaper.invalid",
		"To: alice@example.test",
		"Subject: [CRITICAL] Checkout: checkout is down",
		"Message-ID: <ntf_0001@reaper-incidents>",
		"Incident inc_0001 is triggered (opened).",
		"Dedup key: checkout-5xx",
		"Escalation level: 1 (repeat 0)",
	}
	for _, needle := range required {
		if !strings.Contains(body, needle) {
			t.Fatalf("page is missing %q:\n%s", needle, body)
		}
	}
}

func TestRelayRejectionIsRetryableAndMissingRelayIsPermanent(t *testing.T) {
	relay := &sink{reject: true}
	address := relay.start(t)
	sender, err := New(address, "pager@reaper.invalid", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	err = sender.Notify(context.Background(), pageMessage())
	if err == nil || errors.Is(err, incident.ErrPermanent) {
		t.Fatalf("a relay rejection must be retryable, got %v", err)
	}

	unconfigured, err := New("", "pager@reaper.invalid", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := unconfigured.Notify(context.Background(), pageMessage()); !errors.Is(err, incident.ErrPermanent) {
		t.Fatalf("an unconfigured relay must fail permanently rather than retry forever, got %v", err)
	}

	unreachable, err := New("127.0.0.1:1", "pager@reaper.invalid", 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := unreachable.Notify(context.Background(), pageMessage()); err == nil || errors.Is(err, incident.ErrPermanent) {
		t.Fatalf("a refused relay connection must be retryable, got %v", err)
	}
}

func TestHeaderInjectionIsRefused(t *testing.T) {
	relay := &sink{}
	address := relay.start(t)
	sender, err := New(address, "pager@reaper.invalid", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	injected := pageMessage()
	injected.Responder.Email = "alice@example.test\r\nBcc: attacker@example.test"
	if err := sender.Notify(context.Background(), injected); !errors.Is(err, incident.ErrPermanent) {
		t.Fatalf("a recipient with line breaks must be refused, got %v", err)
	}
	newlineSummary := pageMessage()
	newlineSummary.Incident.Summary = "down\r\nBcc: attacker@example.test"
	if err := sender.Notify(context.Background(), newlineSummary); err != nil {
		t.Fatal(err)
	}
	body := relay.received()
	if len(body) != 1 {
		t.Fatalf("expected one message, got %d", len(body))
	}
	headers := strings.SplitN(body[0], "\n\n", 2)[0]
	for _, line := range strings.Split(headers, "\n") {
		if strings.HasPrefix(line, "Bcc:") {
			t.Fatalf("a summary must not be able to add a header line:\n%s", headers)
		}
	}
	if !strings.Contains(headers, "Subject: [CRITICAL] Checkout: down  Bcc: attacker@example.test") {
		t.Fatalf("line breaks in a summary must fold into the subject:\n%s", headers)
	}
}

func TestSenderConstructionRejectsBadConfiguration(t *testing.T) {
	cases := map[string]func() (*Sender, error){
		"zero timeout":  func() (*Sender, error) { return New("127.0.0.1:25", "a@b.test", 0) },
		"missing port":  func() (*Sender, error) { return New("127.0.0.1", "a@b.test", time.Second) },
		"blank sender":  func() (*Sender, error) { return New("127.0.0.1:25", "   ", time.Second) },
		"framed sender": func() (*Sender, error) { return New("127.0.0.1:25", "Pager <a@b.test>", time.Second) },
	}
	for name, build := range cases {
		if _, err := build(); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
}
