package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/itsHabib/saas-reaper/internal/flags"
)

type evaluationRequest struct {
	Context map[string]any `json:"context"`
}

type evaluationSuccess struct {
	Key      string         `json:"key"`
	Value    any            `json:"value"`
	Reason   string         `json:"reason"`
	Variant  string         `json:"variant"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type evaluationFailure struct {
	Key          string `json:"key"`
	ErrorCode    string `json:"errorCode"`
	ErrorDetails string `json:"errorDetails,omitempty"`
}

func (s *Server) evaluate(w http.ResponseWriter, r *http.Request) {
	request, ok := readEvaluationRequest(w, r, r.PathValue("key"))
	if !ok {
		return
	}
	evaluated, err := s.flags.Evaluate(
		r.PathValue("environment"),
		r.PathValue("key"),
		request.Context,
	)
	if err != nil {
		writeEvaluationError(w, r.PathValue("key"), err)
		return
	}
	writeJSON(w, http.StatusOK, ofrepSuccess(evaluated))
}

func (s *Server) evaluateAll(w http.ResponseWriter, r *http.Request) {
	request, ok := readEvaluationRequest(w, r, "")
	if !ok {
		return
	}
	listed, err := s.flags.List(r.PathValue("environment"))
	if err != nil {
		writeEvaluationError(w, "", err)
		return
	}
	etag, err := evaluationETag(listed, request.Context)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"errorDetails": "internal error"})
		return
	}
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		writeJSON(w, http.StatusNotModified, nil)
		return
	}
	evaluated := make([]any, 0, len(listed))
	for _, flag := range listed {
		result, err := s.flags.Evaluate(r.PathValue("environment"), flag.Key, request.Context)
		if err != nil {
			evaluated = append(evaluated, ofrepFailure(flag.Key, "GENERAL", err.Error()))
			continue
		}
		evaluated = append(evaluated, ofrepSuccess(result))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"flags": evaluated,
		"metadata": map[string]any{
			"version": strings.Trim(etag, "\""),
		},
	})
}

func readEvaluationRequest(w http.ResponseWriter, r *http.Request, key string) (evaluationRequest, bool) {
	var request evaluationRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, ofrepFailure(key, "INVALID_CONTEXT", err.Error()))
		return evaluationRequest{}, false
	}
	targetingKey, exists := request.Context["targetingKey"]
	if !exists {
		writeJSON(w, http.StatusBadRequest, ofrepFailure(key, "TARGETING_KEY_MISSING", "targetingKey is required"))
		return evaluationRequest{}, false
	}
	if _, ok := targetingKey.(string); !ok {
		writeJSON(w, http.StatusBadRequest, ofrepFailure(key, "INVALID_CONTEXT", "targetingKey must be a string"))
		return evaluationRequest{}, false
	}
	return request, true
}

func writeEvaluationError(w http.ResponseWriter, key string, err error) {
	if errors.Is(err, flags.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, ofrepFailure(key, "FLAG_NOT_FOUND", err.Error()))
		return
	}
	if errors.Is(err, flags.ErrInvalid) {
		writeJSON(w, http.StatusBadRequest, ofrepFailure(key, "INVALID_CONTEXT", err.Error()))
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"errorDetails": "internal error"})
}

func ofrepSuccess(evaluated flags.Evaluation) evaluationSuccess {
	return evaluationSuccess{
		Key:     evaluated.Key,
		Value:   evaluated.Value,
		Reason:  evaluated.Reason,
		Variant: evaluated.Variant,
		Metadata: map[string]any{
			"revision": evaluated.Revision,
		},
	}
}

func ofrepFailure(key, code, details string) evaluationFailure {
	return evaluationFailure{Key: key, ErrorCode: code, ErrorDetails: details}
}

// evaluationETag must cover the evaluation context, not only the definitions,
// or a caching client replays one context's decisions for another.
func evaluationETag(listed []flags.Flag, context map[string]any) (string, error) {
	hash := sha256.New()
	for _, flag := range listed {
		_, _ = fmt.Fprintf(hash, "%s:%d\x00", flag.Key, flag.Revision)
	}
	encoded, err := json.Marshal(context)
	if err != nil {
		return "", fmt.Errorf("encode evaluation context: %w", err)
	}
	_, _ = hash.Write(encoded)
	return strconv.Quote(hex.EncodeToString(hash.Sum(nil))), nil
}
