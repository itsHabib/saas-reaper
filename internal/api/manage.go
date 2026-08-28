package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/itsHabib/saas-reaper/internal/flags"
)

type publishRequest struct {
	ExpectedRevision int64      `json:"expectedRevision"`
	Flag             flags.Flag `json:"flag"`
}

func (s *Server) publish(w http.ResponseWriter, r *http.Request) {
	var request publishRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	key := r.PathValue("key")
	if request.Flag.Key != "" && request.Flag.Key != key {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path key and flag key differ"})
		return
	}
	request.Flag.Key = key
	published, err := s.flags.Publish(
		r.Context(),
		r.PathValue("environment"),
		request.Flag,
		request.ExpectedRevision,
		s.adminActor,
	)
	if err != nil {
		writeFlagError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, published)
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	listed, err := s.flags.List(r.PathValue("environment"))
	if err != nil {
		writeFlagError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"flags": listed})
}

func (s *Server) audit(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be an integer"})
			return
		}
		limit = parsed
	}
	entries, err := s.flags.Audit(r.Context(), limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read audit"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"audit": entries})
}

func writeFlagError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, flags.ErrInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, flags.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, flags.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}
