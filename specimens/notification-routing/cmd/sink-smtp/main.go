// Command sink-smtp is a real SMTP server (emersion/go-smtp) that records every accepted message
// for the local proofs. It is a genuine third-party protocol implementation, not a socket reader.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/mail"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	gosmtp "github.com/emersion/go-smtp"
)

const maxMessageBytes = 1 << 20

type receipt struct {
	Sequence     int64    `json:"sequence"`
	From         string   `json:"from"`
	To           []string `json:"to"`
	Subject      string   `json:"subject"`
	Body         string   `json:"body"`
	MessageID    string   `json:"messageId"`
	Notification string   `json:"notificationId"`
	Attempt      string   `json:"attempt"`
	Rejected     bool     `json:"rejected"`
}

type sink struct {
	result    string
	failFirst int64
	messages  atomic.Int64
	writeMu   sync.Mutex
}

type session struct {
	sink *sink
	from string
	to   []string
}

func main() {
	if err := run(); err != nil {
		slog.Error("SMTP sink stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	address, err := requireEnvironment("SINK_ADDR")
	if err != nil {
		return err
	}
	if err := validateLoopback(address); err != nil {
		return err
	}
	result, err := requireEnvironment("SINK_RESULT")
	if err != nil {
		return err
	}
	ready, err := requireEnvironment("SINK_READY")
	if err != nil {
		return err
	}
	failFirst, err := nonnegativeInteger(os.Getenv("SINK_FAIL_FIRST"))
	if err != nil {
		return err
	}
	fixture := &sink{result: result, failFirst: failFirst}
	server := gosmtp.NewServer(fixture)
	server.Domain = "sink.local"
	server.MaxMessageBytes = maxMessageBytes
	server.MaxRecipients = 8
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(context.Background(), "tcp", address)
	if err != nil {
		return fmt.Errorf("listen smtp sink: %w", err)
	}
	if err := os.WriteFile(ready, []byte(address+"\n"), 0o600); err != nil {
		return fmt.Errorf("write ready marker: %w", err)
	}
	return server.Serve(listener)
}

func (s *sink) NewSession(*gosmtp.Conn) (gosmtp.Session, error) {
	return &session{sink: s}, nil
}

func (s *session) Reset() {
	s.from = ""
	s.to = nil
}

func (*session) Logout() error {
	return nil
}

func (s *session) Mail(from string, _ *gosmtp.MailOptions) error {
	s.from = from
	return nil
}

func (s *session) Rcpt(to string, _ *gosmtp.RcptOptions) error {
	s.to = append(s.to, to)
	return nil
}

// Data parses the message with net/mail, decodes the declared transfer encoding, and either
// records it or rejects it with a transient 451 for the first SINK_FAIL_FIRST messages.
func (s *session) Data(r io.Reader) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	record, err := parseMessage(raw)
	if err != nil {
		return &gosmtp.SMTPError{Code: 554, EnhancedCode: gosmtp.EnhancedCode{5, 6, 0}, Message: err.Error()}
	}
	record.Sequence = s.sink.messages.Add(1)
	record.From = s.from
	record.To = append([]string(nil), s.to...)
	record.Rejected = record.Sequence <= s.sink.failFirst
	if err := s.sink.append(record); err != nil {
		return &gosmtp.SMTPError{Code: 451, EnhancedCode: gosmtp.EnhancedCode{4, 3, 0}, Message: "record sink result"}
	}
	if record.Rejected {
		return &gosmtp.SMTPError{Code: 451, EnhancedCode: gosmtp.EnhancedCode{4, 7, 1}, Message: "injected temporary failure"}
	}
	return nil
}

func parseMessage(raw []byte) (receipt, error) {
	message, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		return receipt{}, fmt.Errorf("parse message: %w", err)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(message.Header.Get("Subject"))
	if err != nil {
		return receipt{}, fmt.Errorf("decode subject: %w", err)
	}
	decoded, err := io.ReadAll(messageBody(message))
	if err != nil {
		return receipt{}, fmt.Errorf("decode body: %w", err)
	}
	return receipt{
		Subject:      subject,
		Body:         string(decoded),
		MessageID:    message.Header.Get("Message-ID"),
		Notification: message.Header.Get("X-Reaper-Notification"),
		Attempt:      message.Header.Get("X-Reaper-Attempt"),
	}, nil
}

// messageBody decodes the transfer encoding the sender declared.
func messageBody(message *mail.Message) io.Reader {
	if strings.EqualFold(message.Header.Get("Content-Transfer-Encoding"), "base64") {
		return base64.NewDecoder(base64.StdEncoding, message.Body)
	}
	return message.Body
}

func (s *sink) append(value receipt) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	file, err := os.OpenFile(s.result, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open sink result: %w", err)
	}
	encodeErr := json.NewEncoder(file).Encode(value)
	closeErr := file.Close()
	if err := errors.Join(encodeErr, closeErr); err != nil {
		return fmt.Errorf("append sink result: %w", err)
	}
	return nil
}

func requireEnvironment(name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func validateLoopback(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("SINK_ADDR must be a loopback host:port: %w", err)
	}
	if host != "127.0.0.1" && host != "localhost" {
		return errors.New("SINK_ADDR must use a loopback host")
	}
	return nil
}

func nonnegativeInteger(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, errors.New("SINK_FAIL_FIRST must be a nonnegative integer")
	}
	return value, nil
}
