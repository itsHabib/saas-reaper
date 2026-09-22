package api

import (
	"net/http"
	"time"
)

type createTemplateRequest struct {
	Key     string `json:"key"`
	Channel string `json:"channel"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

type templateResponse struct {
	Key       string    `json:"key"`
	Channel   string    `json:"channel"`
	Subject   string    `json:"subject"`
	Body      string    `json:"body"`
	Variables []string  `json:"variables"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Server) createTemplate(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var request createTemplateRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	template, err := s.routing.CreateTemplate(r.Context(), request.Key, request.Channel, request.Subject, request.Body)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	variables := template.Variables
	if variables == nil {
		variables = []string{}
	}
	writeJSON(w, http.StatusCreated, templateResponse{
		Key:       template.Key,
		Channel:   template.ChannelID,
		Subject:   template.Subject,
		Body:      template.Body,
		Variables: variables,
		CreatedAt: template.CreatedAt,
	})
}
