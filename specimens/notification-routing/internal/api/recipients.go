package api

import (
	"net/http"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/notification-routing/internal/routing"
)

type bindingRequest struct {
	Channel string `json:"channel"`
	Address string `json:"address"`
	Enabled *bool  `json:"enabled"`
}

type createRecipientRequest struct {
	ID       string           `json:"id"`
	Channels []bindingRequest `json:"channels"`
}

type bindingResponse struct {
	Channel string `json:"channel"`
	Enabled bool   `json:"enabled"`
}

type recipientResponse struct {
	ID        string            `json:"id"`
	Channels  []bindingResponse `json:"channels"`
	CreatedAt time.Time         `json:"createdAt"`
}

func (s *Server) createRecipient(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var request createRecipientRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	bindings := make([]routing.Binding, 0, len(request.Channels))
	for _, binding := range request.Channels {
		bindings = append(bindings, routing.Binding{
			ChannelID: binding.Channel, Address: binding.Address, Enabled: binding.Enabled == nil || *binding.Enabled,
		})
	}
	recipient, err := s.routing.CreateRecipient(r.Context(), request.ID, bindings)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	view := recipientResponse{ID: recipient.ID, Channels: []bindingResponse{}, CreatedAt: recipient.CreatedAt}
	for _, binding := range recipient.Bindings {
		view.Channels = append(view.Channels, bindingResponse{Channel: binding.ChannelID, Enabled: binding.Enabled})
	}
	writeJSON(w, http.StatusCreated, view)
}
