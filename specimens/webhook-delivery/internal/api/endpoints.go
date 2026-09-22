package api

import (
	"net/http"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/webhook-delivery/internal/delivery"
)

type registerEndpointRequest struct {
	URL string `json:"url"`
}

type disableEndpointRequest struct {
	ExpectedRevision int64 `json:"expectedRevision"`
}

type endpointResponse struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Enabled   bool      `json:"enabled"`
	Revision  int64     `json:"revision"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Secret    string    `json:"secret,omitempty"`
}

func (s *Server) registerEndpoint(w http.ResponseWriter, r *http.Request) {
	if err := requireJSON(w, r); err != nil {
		return
	}
	var request registerEndpointRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	endpoint, err := s.delivery.RegisterEndpoint(r.Context(), request.URL)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	view := endpointView(endpoint)
	view.Secret = endpoint.Secret
	writeJSON(w, http.StatusCreated, view)
}

func (s *Server) disableEndpoint(w http.ResponseWriter, r *http.Request) {
	if err := requireJSON(w, r); err != nil {
		return
	}
	var request disableEndpointRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	endpoint, err := s.delivery.DisableEndpoint(
		r.Context(),
		r.PathValue("endpoint"),
		request.ExpectedRevision,
	)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, endpointView(endpoint))
}

func endpointView(endpoint delivery.Endpoint) endpointResponse {
	return endpointResponse{
		ID:        endpoint.ID,
		URL:       endpoint.URL,
		Enabled:   endpoint.Enabled,
		Revision:  endpoint.Revision,
		CreatedAt: endpoint.CreatedAt,
		UpdatedAt: endpoint.UpdatedAt,
	}
}
