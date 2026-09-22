package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/itsHabib/saas-reaper/specimens/audit-ledger/internal/ledger"
)

type eventRequest struct {
	Tenant     string          `json:"tenant"`
	ID         string          `json:"id"`
	Actor      string          `json:"actor"`
	Action     string          `json:"action"`
	Target     string          `json:"target"`
	OccurredAt string          `json:"occurredAt"`
	Metadata   json.RawMessage `json:"metadata"`
}

type receiptResponse struct {
	Tenant   string `json:"tenant"`
	ID       string `json:"id"`
	Sequence int64  `json:"sequence"`
	Hash     string `json:"hash"`
	Replayed bool   `json:"replayed"`
}

func (s *Server) ingestEvents(w http.ResponseWriter, r *http.Request) {
	if err := requireJSON(r); err != nil {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": err.Error()})
		return
	}
	requests, err := decodeEvents(w, r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	events := make([]ledger.Event, 0, len(requests))
	for _, request := range requests {
		events = append(events, ledger.Event{
			Tenant:     request.Tenant,
			ID:         request.ID,
			Actor:      request.Actor,
			Action:     request.Action,
			Target:     request.Target,
			OccurredAt: request.OccurredAt,
			Metadata:   request.Metadata,
		})
	}
	receipts, err := s.ledger.Append(r.Context(), events)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	views := make([]receiptResponse, 0, len(receipts))
	appended := false
	for _, receipt := range receipts {
		appended = appended || !receipt.Replayed
		views = append(views, receiptResponse(receipt))
	}
	status := http.StatusOK
	if appended {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{"receipts": views})
}

// decodeEvents accepts one event object or one array of event objects.
func decodeEvents(w http.ResponseWriter, r *http.Request) ([]eventRequest, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxIngestBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("read request: %w", err)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("decode request: empty body")
	}
	if trimmed[0] == '[' {
		var batch []eventRequest
		if err := decodeStrict(trimmed, &batch); err != nil {
			return nil, err
		}
		return batch, nil
	}
	var single eventRequest
	if err := decodeStrict(trimmed, &single); err != nil {
		return nil, err
	}
	return []eventRequest{single}, nil
}

func decodeStrict(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode request: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("decode request: multiple JSON values")
	}
	return nil
}
