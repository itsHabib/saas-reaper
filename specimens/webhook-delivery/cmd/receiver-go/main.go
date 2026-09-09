// Command receiver-go is the official Go verifier fixture used by local proofs.
package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
)

const maxReceiverPayload = 1 << 20

type receipt struct {
	Attempt          int64           `json:"attempt"`
	MessageID        string          `json:"messageId"`
	Timestamp        string          `json:"timestamp"`
	Payload          json.RawMessage `json:"payload"`
	PayloadBase64    string          `json:"payloadBase64"`
	Accepted         bool            `json:"accepted"`
	TamperedRejected bool            `json:"tamperedRejected"`
}

type receiver struct {
	verifier  *standardwebhooks.Webhook
	result    string
	failFirst int64
	attempts  atomic.Int64
	writeMu   sync.Mutex
}

func main() {
	if err := run(); err != nil {
		slog.Error("Go webhook receiver stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	address, err := requireEnvironment("RECEIVER_ADDR")
	if err != nil {
		return err
	}
	if err := validateLoopback(address); err != nil {
		return err
	}
	secret, err := requireEnvironment("RECEIVER_SECRET")
	if err != nil {
		return err
	}
	result, err := requireEnvironment("RECEIVER_RESULT")
	if err != nil {
		return err
	}
	verifier, err := standardwebhooks.NewWebhook(secret)
	if err != nil {
		return fmt.Errorf("create official Go verifier: %w", err)
	}
	failFirst, err := nonnegativeInteger(os.Getenv("RECEIVER_FAIL_FIRST"))
	if err != nil {
		return err
	}
	fixture := &receiver{verifier: verifier, result: result, failFirst: failFirst}
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

func (r *receiver) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /webhook", r.receive)
	return mux
}

func (r *receiver) receive(w http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(w, request.Body, maxReceiverPayload)
	payload, readErr := io.ReadAll(request.Body)
	accepted := readErr == nil && r.verifier.Verify(payload, request.Header) == nil
	tampered := request.Header.Clone()
	tampered.Set("webhook-signature", tamperSignature(request.Header.Get("webhook-signature")))
	tamperedRejected := r.verifier.Verify(payload, tampered) != nil
	attempt := r.attempts.Add(1)
	record := receipt{
		Attempt:          attempt,
		MessageID:        request.Header.Get("webhook-id"),
		Timestamp:        request.Header.Get("webhook-timestamp"),
		Payload:          append(json.RawMessage(nil), payload...),
		PayloadBase64:    base64.StdEncoding.EncodeToString(payload),
		Accepted:         accepted,
		TamperedRejected: tamperedRejected,
	}
	if err := r.append(record); err != nil {
		http.Error(w, "record receiver result", http.StatusInternalServerError)
		return
	}
	if !accepted {
		http.Error(w, "signature rejected", http.StatusUnauthorized)
		return
	}
	if !tamperedRejected {
		http.Error(w, "tampered signature accepted", http.StatusInternalServerError)
		return
	}
	if attempt <= r.failFirst {
		http.Error(w, "injected receiver failure", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r *receiver) append(value receipt) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	file, err := os.OpenFile(r.result, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open receiver result: %w", err)
	}
	encodeErr := json.NewEncoder(file).Encode(value)
	closeErr := file.Close()
	if err := errors.Join(encodeErr, closeErr); err != nil {
		return fmt.Errorf("append receiver result: %w", err)
	}
	return nil
}

func tamperSignature(value string) string {
	parts := strings.Split(value, " ")
	for index, part := range parts {
		separator := strings.IndexByte(part, ',')
		if separator < 0 || separator == len(part)-1 {
			continue
		}
		replacement := byte('A')
		if part[separator+1] == 'A' {
			replacement = 'B'
		}
		parts[index] = part[:separator+1] + string(replacement) + part[separator+2:]
	}
	return strings.Join(parts, " ")
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
		return fmt.Errorf("RECEIVER_ADDR must be a loopback host:port: %w", err)
	}
	if host != "127.0.0.1" && host != "localhost" {
		return errors.New("RECEIVER_ADDR must use a loopback host")
	}
	return nil
}

func nonnegativeInteger(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, errors.New("RECEIVER_FAIL_FIRST must be a nonnegative integer")
	}
	return value, nil
}
