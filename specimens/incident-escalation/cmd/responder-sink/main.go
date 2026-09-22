// Command responder-sink receives pages for local proofs and verifies them with the
// official Standard Webhooks Go library.
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
	"strings"
	"sync"
	"time"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
)

const maxPageBytes = 1 << 20

type receipt struct {
	Responder        string          `json:"responder"`
	NotificationID   string          `json:"notificationId"`
	Attempt          int             `json:"attempt"`
	Accepted         bool            `json:"accepted"`
	TamperedRejected bool            `json:"tamperedRejected"`
	Payload          json.RawMessage `json:"payload"`
}

type sink struct {
	secretsPath string
	resultPath  string
	failFirst   int
	mu          sync.Mutex
	attempts    map[string]int
}

func main() {
	if err := run(); err != nil {
		slog.Error("responder sink stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	address, err := requireEnvironment("SINK_ADDR")
	if err != nil {
		return err
	}
	if err := validateBindAddress(address); err != nil {
		return err
	}
	secretsPath, err := requireEnvironment("SINK_SECRETS")
	if err != nil {
		return err
	}
	resultPath, err := requireEnvironment("SINK_RESULT")
	if err != nil {
		return err
	}
	failFirst := 0
	if raw := os.Getenv("SINK_FAIL_FIRST"); raw != "" {
		failFirst, err = strconv.Atoi(raw)
		if err != nil || failFirst < 0 {
			return errors.New("SINK_FAIL_FIRST must be a non-negative integer")
		}
	}
	fixture := &sink{secretsPath: secretsPath, resultPath: resultPath, failFirst: failFirst, attempts: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("POST /page/{responder}", fixture.page)
	server := &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
	slog.Info("responder sink listening", "address", address)
	return server.ListenAndServe()
}

func (s *sink) page(w http.ResponseWriter, r *http.Request) {
	responder := r.PathValue("responder")
	secret, err := s.secretFor(responder)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxPageBytes))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	verifier, err := standardwebhooks.NewWebhook(secret)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := verifier.Verify(body, r.Header); err != nil {
		http.Error(w, "official verifier rejected the page: "+err.Error(), http.StatusBadRequest)
		return
	}
	tampered := r.Header.Clone()
	tampered.Set("webhook-signature", tamper(r.Header.Get("webhook-signature")))
	tamperedRejected := verifier.Verify(body, tampered) != nil
	attempt := s.count(responder)
	if attempt <= s.failFirst {
		http.Error(w, "simulated responder outage", http.StatusInternalServerError)
		return
	}
	entry := receipt{
		Responder:        responder,
		NotificationID:   r.Header.Get("webhook-id"),
		Attempt:          attempt,
		Accepted:         true,
		TamperedRejected: tamperedRejected,
		Payload:          json.RawMessage(body),
	}
	if err := s.record(entry); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *sink) secretFor(responder string) (string, error) {
	// #nosec G304 -- the secrets file path is trusted proof configuration.
	raw, err := os.ReadFile(s.secretsPath)
	if err != nil {
		return "", fmt.Errorf("read secrets: %w", err)
	}
	secrets := map[string]string{}
	if err := json.Unmarshal(raw, &secrets); err != nil {
		return "", fmt.Errorf("decode secrets: %w", err)
	}
	secret, ok := secrets[responder]
	if !ok {
		return "", fmt.Errorf("no secret for responder %q", responder)
	}
	return secret, nil
}

func (s *sink) count(responder string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts[responder]++
	return s.attempts[responder]
}

func (s *sink) record(entry receipt) error {
	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode receipt: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// #nosec G304 -- the receipt path is trusted proof configuration.
	file, err := os.OpenFile(s.resultPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open receipts: %w", err)
	}
	_, writeErr := file.Write(append(line, '\n'))
	return errors.Join(writeErr, file.Close())
}

func tamper(signature string) string {
	if !strings.HasPrefix(signature, "v1,") || len(signature) < 8 {
		return signature + "x"
	}
	body := []byte(signature[3:])
	index := len(body) - 3
	if body[index] == 'A' {
		body[index] = 'B'
		return "v1," + string(body)
	}
	body[index] = 'A'
	return "v1," + string(body)
}

func requireEnvironment(name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

// validateBindAddress keeps the fixture off any routable interface. It runs
// either on the host loopback or inside the demo's private container network,
// where the unspecified address is the container's own namespace and the port
// is never published to the host.
func validateBindAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("SINK_ADDR must be host:port: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return errors.New("SINK_ADDR must bind an IP address")
	}
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsPrivate() {
		return nil
	}
	return errors.New("SINK_ADDR must bind a loopback, unspecified, or private address")
}
