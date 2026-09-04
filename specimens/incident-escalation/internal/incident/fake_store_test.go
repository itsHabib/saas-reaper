package incident

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/oncall"
)

// memoryStore is a deterministic in-memory authority for policy tests.
type memoryStore struct {
	mu             sync.Mutex
	responders     map[string]Responder
	schedules      map[string]oncall.Schedule
	policies       map[string]EscalationPolicy
	services       map[string]Service
	incidents      map[string]Incident
	events         []Event
	notifications  map[string]Notification
	attempts       []Attempt
	claims         int
	failClaim      error
	failRecord     error
	failCreate     int
	failTransition int
	failDue        error
	raceAfterDue   string
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		responders:    map[string]Responder{},
		schedules:     map[string]oncall.Schedule{},
		policies:      map[string]EscalationPolicy{},
		services:      map[string]Service{},
		incidents:     map[string]Incident{},
		notifications: map[string]Notification{},
	}
}

func (m *memoryStore) CreateResponder(_ context.Context, responder Responder) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.responders[responder.ID]; exists {
		return ErrConflict
	}
	m.responders[responder.ID] = responder
	return nil
}

func (m *memoryStore) CreateSchedule(_ context.Context, id, _ string, schedule oncall.Schedule, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.schedules[id] = schedule
	return nil
}

func (m *memoryStore) CreatePolicy(_ context.Context, policy EscalationPolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.policies[policy.ID] = policy
	return nil
}

func (m *memoryStore) CreateService(_ context.Context, service Service) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.services[service.ID] = service
	return nil
}

func (m *memoryStore) Schedule(_ context.Context, id string) (oncall.Schedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	schedule, ok := m.schedules[id]
	if !ok {
		return oncall.Schedule{}, ErrNotFound
	}
	return schedule, nil
}

func (m *memoryStore) ServiceByRoutingKey(_ context.Context, key string) (Service, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, service := range m.services {
		if service.RoutingKey == key {
			return service, nil
		}
	}
	return Service{}, ErrNotFound
}

func (m *memoryStore) Targets(_ context.Context, policyID string) (Targets, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	policy, ok := m.policies[policyID]
	if !ok {
		return Targets{}, ErrNotFound
	}
	targets := Targets{Policy: policy, Schedules: map[string]oncall.Schedule{}, Responders: map[string]Responder{}}
	for id, schedule := range m.schedules {
		targets.Schedules[id] = schedule
	}
	for id, responder := range m.responders {
		targets.Responders[id] = responder
	}
	return targets, nil
}

func (m *memoryStore) Incident(_ context.Context, id string) (Incident, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.incidents[id]
	if !ok {
		return Incident{}, ErrNotFound
	}
	return current, nil
}

func (m *memoryStore) OpenIncident(_ context.Context, serviceID, dedupKey string) (Incident, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, current := range m.incidents {
		if current.ServiceID == serviceID && current.DedupKey == dedupKey && current.State != StateResolved {
			return current, nil
		}
	}
	return Incident{}, ErrNotFound
}

func (m *memoryStore) CreateIncident(_ context.Context, opened Incident, event Event, notifications []Notification) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failCreate > 0 {
		m.failCreate--
		return ErrConflict
	}
	for _, current := range m.incidents {
		if current.ServiceID == opened.ServiceID && current.DedupKey == opened.DedupKey && current.State != StateResolved {
			return ErrConflict
		}
	}
	m.incidents[opened.ID] = opened
	m.append(event, notifications)
	return nil
}

func (m *memoryStore) Transition(_ context.Context, next Incident, expected int64, event Event, notifications []Notification) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failTransition > 0 {
		m.failTransition--
		return fmt.Errorf("%w: simulated lost race", ErrConflict)
	}
	current, ok := m.incidents[next.ID]
	if !ok || current.Revision != expected {
		return fmt.Errorf("%w: revision %d", ErrConflict, expected)
	}
	m.incidents[next.ID] = next
	m.append(event, notifications)
	return nil
}

func (m *memoryStore) append(event Event, notifications []Notification) {
	event.Sequence = int64(len(m.events) + 1)
	m.events = append(m.events, event)
	for _, notification := range notifications {
		m.notifications[notification.ID] = notification
	}
}

func (m *memoryStore) DueEscalations(_ context.Context, now time.Time, limit int) ([]Incident, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failDue != nil {
		return nil, m.failDue
	}
	var due []Incident
	for _, current := range m.incidents {
		if current.State == StateTriggered && !current.EscalateAt.IsZero() && !now.Before(current.EscalateAt) {
			due = append(due, current)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].ID < due[j].ID })
	if len(due) > limit {
		due = due[:limit]
	}
	m.raceRevisionLocked()
	return due, nil
}

// raceRevisionLocked advances one incident's revision after a due read, so a
// caller acting on the row it just loaded loses the optimistic race.
func (m *memoryStore) raceRevisionLocked() {
	if m.raceAfterDue == "" {
		return
	}
	for id, current := range m.incidents {
		if current.DedupKey != m.raceAfterDue {
			continue
		}
		current.Revision += 5
		m.incidents[id] = current
	}
	m.raceAfterDue = ""
}

func (m *memoryStore) DueNotifications(_ context.Context, now time.Time, limit int) ([]Dispatch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var due []Dispatch
	for _, notification := range m.notifications {
		if notification.State != NotificationPending || now.Before(notification.NextAttemptAt) {
			continue
		}
		due = append(due, Dispatch{
			Notification: notification,
			Responder:    m.responders[notification.ResponderID],
			Incident:     m.incidents[notification.IncidentID],
			ServiceName:  "svc",
		})
	}
	sort.Slice(due, func(i, j int) bool { return due[i].Notification.ID < due[j].Notification.ID })
	if len(due) > limit {
		due = due[:limit]
	}
	return due, nil
}

func (m *memoryStore) ClaimNotification(_ context.Context, notification Notification, until time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failClaim != nil {
		return m.failClaim
	}
	current, ok := m.notifications[notification.ID]
	if !ok || current.State != NotificationPending || current.AttemptCount != notification.AttemptCount ||
		!current.NextAttemptAt.Equal(notification.NextAttemptAt) {
		return ErrConflict
	}
	current.NextAttemptAt = until
	m.notifications[notification.ID] = current
	m.claims++
	return nil
}

func (m *memoryStore) RecordAttempt(_ context.Context, attempt Attempt) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failRecord != nil {
		return m.failRecord
	}
	current, ok := m.notifications[attempt.NotificationID]
	if !ok || current.State != NotificationPending || current.AttemptCount != attempt.Number-1 {
		return ErrConflict
	}
	current.State = attempt.State
	current.AttemptCount = attempt.Number
	current.NextAttemptAt = attempt.NextAttemptAt
	m.notifications[attempt.NotificationID] = current
	attempt.Sequence = int64(len(m.attempts) + 1)
	m.attempts = append(m.attempts, attempt)
	return nil
}

func (m *memoryStore) eventKinds(incidentID string) []EventKind {
	m.mu.Lock()
	defer m.mu.Unlock()
	var kinds []EventKind
	for _, event := range m.events {
		if event.IncidentID == incidentID {
			kinds = append(kinds, event.Kind)
		}
	}
	return kinds
}

func (m *memoryStore) notificationsFor(incidentID string) []Notification {
	m.mu.Lock()
	defer m.mu.Unlock()
	var found []Notification
	for _, notification := range m.notifications {
		if notification.IncidentID == incidentID {
			found = append(found, notification)
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].ID < found[j].ID })
	return found
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(delta time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(delta)
}

func sequentialIDs() IDGenerator {
	counter := 0
	var mu sync.Mutex
	return func(prefix string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		counter++
		return fmt.Sprintf("%s%04d", prefix, counter), nil
	}
}

func fixedSecret() (string, error) {
	return "whsec_" + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", nil
}

var errTransport = errors.New("transport failure")

type scriptedNotifier struct {
	mu       sync.Mutex
	results  []error
	messages []Message
}

func (n *scriptedNotifier) Notify(_ context.Context, message Message) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.messages = append(n.messages, message)
	if len(n.results) == 0 {
		return nil
	}
	result := n.results[0]
	n.results = n.results[1:]
	return result
}

func twoLevelPolicy(repeat int) EscalationPolicy {
	return EscalationPolicy{
		ID:   "ladder",
		Name: "Ladder",
		Levels: []Level{
			{Timeout: oncall.Duration(30 * time.Second), Schedules: []string{"primary"}},
			{Timeout: oncall.Duration(45 * time.Second), Responders: []string{"bob"}},
		},
		Repeat: repeat,
	}
}

func seededDesk(t interface{ Fatal(...any) }, repeat int) (*Desk, *memoryStore, *fakeClock) {
	store := newMemoryStore()
	clock := &fakeClock{now: time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)}
	desk, err := NewDesk(store, "operator", clock.Now, sequentialIDs(), fixedSecret)
	if err != nil {
		t.Fatal(err)
	}
	store.responders["alice"] = Responder{ID: "alice", Email: "alice@example.test", WebhookURL: "http://127.0.0.1:1/a", WebhookSecret: "whsec_x"}
	store.responders["bob"] = Responder{ID: "bob", WebhookURL: "http://127.0.0.1:1/b", WebhookSecret: "whsec_y"}
	store.schedules["primary"] = oncall.Schedule{Layers: []oncall.Layer{{
		Name:       "always",
		Start:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Rotation:   oncall.Duration(24 * time.Hour),
		Responders: []string{"alice"},
	}}}
	store.policies["ladder"] = twoLevelPolicy(repeat)
	store.services["checkout"] = Service{ID: "checkout", Name: "Checkout", RoutingKey: "rk-checkout", PolicyID: "ladder"}
	return desk, store, clock
}

func triggerAlert(dedupKey string) Alert {
	return Alert{
		RoutingKey: "rk-checkout",
		Action:     ActionTrigger,
		DedupKey:   dedupKey,
		Summary:    "checkout is down",
		Source:     "prometheus",
		Severity:   SeverityCritical,
		Client:     "Alertmanager",
	}
}
