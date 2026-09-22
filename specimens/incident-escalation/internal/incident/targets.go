package incident

import (
	"fmt"
	"sort"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/oncall"
)

// Targets is everything one escalation policy can page, loaded once per decision.
type Targets struct {
	Policy     EscalationPolicy
	Schedules  map[string]oncall.Schedule
	Responders map[string]Responder
}

// LevelResponders resolves one level to concrete responders at an instant, sorted and unique.
func (t Targets) LevelResponders(level int, at time.Time) ([]Responder, error) {
	if level < 0 || level >= len(t.Policy.Levels) {
		return nil, fmt.Errorf("%w: policy %s has no level %d", ErrInvalid, t.Policy.ID, level)
	}
	ids := map[string]bool{}
	step := t.Policy.Levels[level]
	for _, scheduleID := range step.Schedules {
		schedule, ok := t.Schedules[scheduleID]
		if !ok {
			return nil, fmt.Errorf("%w: schedule %s referenced by policy %s", ErrNotFound, scheduleID, t.Policy.ID)
		}
		responder, onCall := schedule.OnCall(at)
		if !onCall {
			continue
		}
		ids[responder] = true
	}
	for _, responderID := range step.Responders {
		ids[responderID] = true
	}
	responders := make([]Responder, 0, len(ids))
	for id := range ids {
		responder, ok := t.Responders[id]
		if !ok {
			return nil, fmt.Errorf("%w: responder %s referenced by policy %s", ErrNotFound, id, t.Policy.ID)
		}
		responders = append(responders, responder)
	}
	sort.Slice(responders, func(i, j int) bool { return responders[i].ID < responders[j].ID })
	return responders, nil
}

// PlanNotifications creates one pending page per responder channel for a transition.
func PlanNotifications(transition Transition, responders []Responder, generate IDGenerator) ([]Notification, error) {
	if !transition.Notify {
		return nil, nil
	}
	incident := transition.Incident
	notifications := make([]Notification, 0, len(responders)*2)
	for _, responder := range responders {
		for _, channel := range responder.Channels() {
			id, err := generate("ntf_")
			if err != nil {
				return nil, err
			}
			notifications = append(notifications, Notification{
				ID:            id,
				IncidentID:    incident.ID,
				ResponderID:   responder.ID,
				Channel:       channel,
				Level:         incident.Level,
				Repeat:        incident.Repeat,
				State:         NotificationPending,
				NextAttemptAt: transition.Event.At,
				CreatedAt:     transition.Event.At,
			})
		}
	}
	return notifications, nil
}
