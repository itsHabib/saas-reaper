// Command sink-slack validates incoming-webhook requests against Slack's documented payload shape
// and records each one for the local proofs.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const maxPayloadBytes = 64 << 10

// documentedFields are the top-level keys Slack's incoming-webhook payload accepts.
var documentedFields = map[string]bool{
	"text": true, "blocks": true, "attachments": true, "thread_ts": true, "mrkdwn": true,
	"username": true, "icon_emoji": true, "icon_url": true, "channel": true,
	"unfurl_links": true, "unfurl_media": true, "response_type": true,
	"replace_original": true, "delete_original": true,
}

type receipt struct {
	Sequence   int64  `json:"sequence"`
	Text       string `json:"text"`
	DeliveryID string `json:"deliveryId"`
	Attempt    string `json:"attempt"`
	Valid      bool   `json:"valid"`
	Rejected   bool   `json:"rejected"`
	Violation  string `json:"violation,omitempty"`
}

type sink struct {
	result    string
	failFirst int64
	requests  atomic.Int64
	writeMu   sync.Mutex
}

func main() {
	if err := run(); err != nil {
		slog.Error("Slack sink stopped", "error", err)
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
	failFirst, err := nonnegativeInteger(os.Getenv("SINK_FAIL_FIRST"))
	if err != nil {
		return err
	}
	fixture := &sink{result: result, failFirst: failFirst}
	server := &http.Server{
		Addr:              address,
		Handler:           fixture.handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	return server.ListenAndServe()
}

func (s *sink) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /services/{team}/{bot}/{token}", s.receive)
	return mux
}

// receive mirrors Slack's documented responses: "ok" on success, 400 invalid_payload or no_text
// for shape violations, and an injected 503 for the first SINK_FAIL_FIRST requests.
func (s *sink) receive(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPayloadBytes)
	record := receipt{
		Sequence:   s.requests.Add(1),
		DeliveryID: r.Header.Get("X-Reaper-Delivery"),
		Attempt:    r.Header.Get("X-Reaper-Attempt"),
	}
	text, violation := validatePayload(r)
	record.Text = text
	record.Valid = violation == ""
	record.Violation = violation
	record.Rejected = record.Valid && record.Sequence <= s.failFirst
	if err := s.append(record); err != nil {
		http.Error(w, "record sink result", http.StatusInternalServerError)
		return
	}
	if !record.Valid {
		http.Error(w, violation, http.StatusBadRequest)
		return
	}
	if record.Rejected {
		http.Error(w, "service_unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = io.WriteString(w, "ok")
}

func validatePayload(r *http.Request) (string, string) {
	if r.Header.Get("Content-Type") != "application/json" {
		return "", "invalid_payload"
	}
	var payload map[string]json.RawMessage
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&payload); err != nil {
		return "", "invalid_payload"
	}
	for field := range payload {
		if !documentedFields[field] {
			return "", "invalid_payload"
		}
	}
	var text string
	if raw, present := payload["text"]; present {
		if err := json.Unmarshal(raw, &text); err != nil {
			return "", "invalid_payload"
		}
	}
	_, hasBlocks := payload["blocks"]
	_, hasAttachments := payload["attachments"]
	if text == "" && !hasBlocks && !hasAttachments {
		return "", "no_text"
	}
	return text, ""
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
