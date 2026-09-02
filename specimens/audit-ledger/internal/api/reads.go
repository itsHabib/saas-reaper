package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/itsHabib/saas-reaper/specimens/audit-ledger/internal/ledger"
)

const (
	defaultPageLimit = 100
	maxPageLimit     = 1000
)

type entryResponse struct {
	Tenant     string          `json:"tenant"`
	Sequence   int64           `json:"sequence"`
	ID         string          `json:"id"`
	Actor      string          `json:"actor"`
	Action     string          `json:"action"`
	Target     string          `json:"target"`
	OccurredAt string          `json:"occurredAt"`
	RecordedAt string          `json:"recordedAt"`
	Source     string          `json:"source"`
	Metadata   json.RawMessage `json:"metadata"`
	Hash       string          `json:"hash"`
}

type headResponse struct {
	Tenant   string `json:"tenant"`
	Sequence int64  `json:"sequence"`
	Hash     string `json:"hash"`
}

func (s *Server) readHead(w http.ResponseWriter, r *http.Request, tenant string) {
	head, err := s.reader.Head(r.Context(), tenant)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, headResponse{Tenant: tenant, Sequence: head.Sequence, Hash: head.Hash})
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request, tenant string) {
	after, err := nonNegative("after", r.URL.Query().Get("after"), 0)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	limit, err := nonNegative("limit", r.URL.Query().Get("limit"), defaultPageLimit)
	if err != nil || limit < 1 || limit > maxPageLimit {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("limit must be 1-%d", maxPageLimit)})
		return
	}
	entries, err := s.reader.Entries(r.Context(), tenant, after, int(limit))
	if err != nil {
		writePolicyError(w, err)
		return
	}
	views := make([]entryResponse, 0, len(entries))
	next := after
	for _, entry := range entries {
		views = append(views, entryView(entry))
		next = entry.Sequence
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": views, "next": next})
}

// exportEvents streams the tenant chain as NDJSON, one entry per line in
// sequence order. The line encoding is ordinary JSON, not the canonical form;
// verifiers re-canonicalize from parsed values.
func (s *Server) exportEvents(w http.ResponseWriter, r *http.Request, tenant string) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	err := s.reader.Export(r.Context(), tenant, func(entry ledger.Entry) error {
		return encoder.Encode(entryView(entry))
	})
	if err != nil {
		// Headers are committed; an aborted stream is the only honest signal.
		panic(http.ErrAbortHandler)
	}
}

func nonNegative(name, raw string, fallback int64) (int64, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", name)
	}
	return value, nil
}

func entryView(entry ledger.Entry) entryResponse {
	return entryResponse{
		Tenant:     entry.Tenant,
		Sequence:   entry.Sequence,
		ID:         entry.ID,
		Actor:      entry.Actor,
		Action:     entry.Action,
		Target:     entry.Target,
		OccurredAt: entry.OccurredAt,
		RecordedAt: entry.RecordedAt,
		Source:     entry.Source,
		Metadata:   entry.Metadata,
		Hash:       entry.Hash,
	}
}
