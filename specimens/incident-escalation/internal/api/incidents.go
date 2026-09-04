package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/incident"
)

type incidentResponse struct {
	ID         string            `json:"id"`
	ServiceID  string            `json:"serviceId"`
	DedupKey   string            `json:"dedupKey"`
	State      incident.State    `json:"state"`
	Summary    string            `json:"summary"`
	Source     string            `json:"source"`
	Severity   incident.Severity `json:"severity"`
	Client     string            `json:"client,omitempty"`
	Policy     string            `json:"escalationPolicy"`
	Level      int               `json:"level"`
	Repeat     int               `json:"repeat"`
	EscalateAt *time.Time        `json:"escalateAt,omitempty"`
	Revision   int64             `json:"revision"`
	OpenedAt   time.Time         `json:"openedAt"`
	UpdatedAt  time.Time         `json:"updatedAt"`
}

type eventResponse struct {
	Sequence   int64              `json:"sequence"`
	IncidentID string             `json:"incidentId"`
	Kind       incident.EventKind `json:"kind"`
	Actor      string             `json:"actor"`
	Level      int                `json:"level"`
	Repeat     int                `json:"repeat"`
	Detail     string             `json:"detail,omitempty"`
	At         time.Time          `json:"at"`
}

type notificationResponse struct {
	ID            string                     `json:"id"`
	IncidentID    string                     `json:"incidentId"`
	ResponderID   string                     `json:"responderId"`
	Channel       incident.Channel           `json:"channel"`
	Level         int                        `json:"level"`
	Repeat        int                        `json:"repeat"`
	State         incident.NotificationState `json:"state"`
	AttemptCount  int                        `json:"attemptCount"`
	NextAttemptAt *time.Time                 `json:"nextAttemptAt,omitempty"`
	CreatedAt     time.Time                  `json:"createdAt"`
}

type attemptResponse struct {
	Sequence       int64                   `json:"sequence"`
	NotificationID string                  `json:"notificationId"`
	IncidentID     string                  `json:"incidentId"`
	ResponderID    string                  `json:"responderId"`
	Channel        incident.Channel        `json:"channel"`
	Number         int                     `json:"number"`
	Outcome        incident.AttemptOutcome `json:"outcome"`
	Error          string                  `json:"error,omitempty"`
	AttemptedAt    time.Time               `json:"attemptedAt"`
	NextAttemptAt  *time.Time              `json:"nextAttemptAt,omitempty"`
}

func (s *Server) listIncidents(w http.ResponseWriter, r *http.Request) {
	limit, err := readLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	filter := incident.IncidentFilter{
		ServiceID: r.URL.Query().Get("serviceId"),
		State:     incident.State(r.URL.Query().Get("state")),
	}
	incidents, err := s.reader.Incidents(r.Context(), filter, limit)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	views := make([]incidentResponse, 0, len(incidents))
	for _, current := range incidents {
		views = append(views, incidentView(current))
	}
	writeJSON(w, http.StatusOK, map[string]any{"incidents": views})
}

func (s *Server) getIncident(w http.ResponseWriter, r *http.Request) {
	current, err := s.reader.Incident(r.Context(), r.PathValue("incident"))
	if err != nil {
		writePolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, incidentView(current))
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("incident")
	if _, err := s.reader.Incident(r.Context(), id); err != nil {
		writePolicyError(w, err)
		return
	}
	events, err := s.reader.Events(r.Context(), id)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	views := make([]eventResponse, 0, len(events))
	for _, event := range events {
		views = append(views, eventResponse{
			Sequence:   event.Sequence,
			IncidentID: event.IncidentID,
			Kind:       event.Kind,
			Actor:      event.Actor,
			Level:      event.Level,
			Repeat:     event.Repeat,
			Detail:     event.Detail,
			At:         event.At,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": views})
}

func (s *Server) listNotifications(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("incident")
	if _, err := s.reader.Incident(r.Context(), id); err != nil {
		writePolicyError(w, err)
		return
	}
	notifications, err := s.reader.Notifications(r.Context(), id)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	views := make([]notificationResponse, 0, len(notifications))
	for _, notification := range notifications {
		views = append(views, notificationResponse{
			ID:            notification.ID,
			IncidentID:    notification.IncidentID,
			ResponderID:   notification.ResponderID,
			Channel:       notification.Channel,
			Level:         notification.Level,
			Repeat:        notification.Repeat,
			State:         notification.State,
			AttemptCount:  notification.AttemptCount,
			NextAttemptAt: optionalTime(notification.NextAttemptAt),
			CreatedAt:     notification.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": views})
}

func (s *Server) listAttempts(w http.ResponseWriter, r *http.Request) {
	limit, err := readLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	filter := incident.AttemptFilter{
		IncidentID:     r.URL.Query().Get("incidentId"),
		NotificationID: r.URL.Query().Get("notificationId"),
	}
	attempts, err := s.reader.Attempts(r.Context(), filter, limit)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	views := make([]attemptResponse, 0, len(attempts))
	for _, attempt := range attempts {
		views = append(views, attemptResponse{
			Sequence:       attempt.Sequence,
			NotificationID: attempt.NotificationID,
			IncidentID:     attempt.IncidentID,
			ResponderID:    attempt.ResponderID,
			Channel:        attempt.Channel,
			Number:         attempt.Number,
			Outcome:        attempt.Outcome,
			Error:          attempt.Error,
			AttemptedAt:    attempt.AttemptedAt,
			NextAttemptAt:  optionalTime(attempt.NextAttemptAt),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"attempts": views})
}

func readLimit(raw string) (int, error) {
	if raw == "" {
		return 100, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > 1000 {
		return 0, strconv.ErrSyntax
	}
	return limit, nil
}

func incidentView(current incident.Incident) incidentResponse {
	return incidentResponse{
		ID:         current.ID,
		ServiceID:  current.ServiceID,
		DedupKey:   current.DedupKey,
		State:      current.State,
		Summary:    current.Summary,
		Source:     current.Source,
		Severity:   current.Severity,
		Client:     current.Client,
		Policy:     current.PolicyID,
		Level:      current.Level,
		Repeat:     current.Repeat,
		EscalateAt: optionalTime(current.EscalateAt),
		Revision:   current.Revision,
		OpenedAt:   current.OpenedAt,
		UpdatedAt:  current.UpdatedAt,
	}
}

func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copied := value
	return &copied
}
