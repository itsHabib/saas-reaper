package api

import (
	"net/http"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/notification-routing/internal/routing"
)

type registerChannelRequest struct {
	ID   string              `json:"id"`
	Kind routing.ChannelKind `json:"kind"`
}

type disableChannelRequest struct {
	ExpectedRevision int64 `json:"expectedRevision"`
}

type channelResponse struct {
	ID        string              `json:"id"`
	Kind      routing.ChannelKind `json:"kind"`
	Enabled   bool                `json:"enabled"`
	Revision  int64               `json:"revision"`
	CreatedAt time.Time           `json:"createdAt"`
	UpdatedAt time.Time           `json:"updatedAt"`
}

func (s *Server) registerChannel(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var request registerChannelRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	channel, err := s.routing.RegisterChannel(r.Context(), request.ID, request.Kind)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, channelView(channel))
}

func (s *Server) disableChannel(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var request disableChannelRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	channel, err := s.routing.DisableChannel(r.Context(), r.PathValue("channel"), request.ExpectedRevision)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, channelView(channel))
}

func channelView(channel routing.Channel) channelResponse {
	return channelResponse{
		ID:        channel.ID,
		Kind:      channel.Kind,
		Enabled:   channel.Enabled,
		Revision:  channel.Revision,
		CreatedAt: channel.CreatedAt,
		UpdatedAt: channel.UpdatedAt,
	}
}
