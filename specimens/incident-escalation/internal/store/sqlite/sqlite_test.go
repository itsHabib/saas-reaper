package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/incident"
	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/oncall"
)

func openStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "incidents.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, path
}

func baseTime() time.Time {
	return time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
}

func seed(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	now := baseTime()
	responders := []incident.Responder{
		{ID: "alice", Email: "alice@example.test", WebhookURL: "http://127.0.0.1:1/a", WebhookSecret: "whsec_a", CreatedAt: now},
		{ID: "bob", Email: "bob@example.test", CreatedAt: now},
	}
	for _, responder := range responders {
		if err := store.CreateResponder(ctx, responder); err != nil {
			t.Fatal(err)
		}
	}
	schedule := oncall.Schedule{Layers: []oncall.Layer{{
		Name:       "primary",
		Start:      now.Add(-24 * time.Hour),
		Rotation:   oncall.Duration(24 * time.Hour),
		Responders: []string{"alice"},
	}}}
	if err := store.CreateSchedule(ctx, "primary", "Primary", schedule, now); err != nil {
		t.Fatal(err)
	}
	policy := incident.EscalationPolicy{
		ID:   "ladder",
		Name: "Ladder",
		Levels: []incident.Level{
			{Timeout: oncall.Duration(30 * time.Second), Schedules: []string{"primary"}},
			{Timeout: oncall.Duration(time.Minute), Responders: []string{"bob"}},
		},
		CreatedAt: now,
	}
	if err := store.CreatePolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	service := incident.Service{ID: "checkout", Name: "Checkout", RoutingKey: "rk", PolicyID: "ladder", CreatedAt: now}
	if err := store.CreateService(ctx, service); err != nil {
		t.Fatal(err)
	}
}

func openIncident(t *testing.T, store *Store, id, dedupKey string, escalateAt time.Time) incident.Incident {
	t.Helper()
	now := baseTime()
	opened := incident.Incident{
		ID:         id,
		ServiceID:  "checkout",
		DedupKey:   dedupKey,
		State:      incident.StateTriggered,
		Summary:    "checkout is down",
		Source:     "prometheus",
		Severity:   incident.SeverityCritical,
		PolicyID:   "ladder",
		EscalateAt: escalateAt,
		Revision:   1,
		OpenedAt:   now,
		UpdatedAt:  now,
	}
	event := incident.Event{IncidentID: id, Kind: incident.EventOpened, Actor: "service:checkout", At: now}
	page := incident.Notification{
		ID:            "ntf_" + id,
		IncidentID:    id,
		ResponderID:   "alice",
		Channel:       incident.ChannelWebhook,
		State:         incident.NotificationPending,
		NextAttemptAt: now,
		CreatedAt:     now,
	}
	if err := store.CreateIncident(context.Background(), opened, event, []incident.Notification{page}); err != nil {
		t.Fatal(err)
	}
	return opened
}

func TestDatabaseFileIsOwnerOnly(t *testing.T) {
	_, path := openStore(t)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected owner-only database, got %s", info.Mode().Perm())
	}
	if _, err := Open("   "); err == nil {
		t.Fatal("a blank path must be rejected")
	}
}

func TestOneOpenIncidentPerDedupKeyAndReopenAfterResolve(t *testing.T) {
	store, _ := openStore(t)
	seed(t, store)
	ctx := context.Background()
	first := openIncident(t, store, "inc_1", "checkout-5xx", baseTime().Add(30*time.Second))

	duplicate := first
	duplicate.ID = "inc_2"
	err := store.CreateIncident(ctx, duplicate, incident.Event{IncidentID: "inc_2", Kind: incident.EventOpened, At: baseTime()}, nil)
	if !errors.Is(err, incident.ErrConflict) {
		t.Fatalf("a second open incident for one dedup key must conflict, got %v", err)
	}

	resolved := first
	resolved.State = incident.StateResolved
	resolved.EscalateAt = time.Time{}
	resolved.Revision = 2
	resolved.UpdatedAt = baseTime().Add(time.Minute)
	event := incident.Event{IncidentID: first.ID, Kind: incident.EventResolved, Actor: "operator", At: resolved.UpdatedAt}
	if err := store.Transition(ctx, resolved, 1, event, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.OpenIncident(ctx, "checkout", "checkout-5xx"); !errors.Is(err, incident.ErrNotFound) {
		t.Fatalf("a resolved incident is no longer open, got %v", err)
	}
	reopened := duplicate
	reopened.OpenedAt = baseTime().Add(2 * time.Minute)
	reopened.UpdatedAt = reopened.OpenedAt
	if err := store.CreateIncident(ctx, reopened, incident.Event{IncidentID: "inc_2", Kind: incident.EventOpened, At: reopened.OpenedAt}, nil); err != nil {
		t.Fatalf("a trigger after resolve must open a new incident: %v", err)
	}
}

func TestTransitionIsFencedOnTheExpectedRevision(t *testing.T) {
	store, _ := openStore(t)
	seed(t, store)
	ctx := context.Background()
	current := openIncident(t, store, "inc_1", "k", baseTime().Add(30*time.Second))
	next := current
	next.State = incident.StateAcknowledged
	next.EscalateAt = time.Time{}
	next.Revision = 2
	next.UpdatedAt = baseTime().Add(time.Second)
	event := incident.Event{IncidentID: current.ID, Kind: incident.EventAcknowledged, Actor: "operator", At: next.UpdatedAt}
	if err := store.Transition(ctx, next, 1, event, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Transition(ctx, next, 1, event, nil); !errors.Is(err, incident.ErrConflict) {
		t.Fatalf("a stale revision must conflict, got %v", err)
	}
	events, err := store.Events(ctx, current.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("the conflicting transition must not append a journal row, got %d", len(events))
	}
}

func TestIncidentJournalAndAttemptAuditAreAppendOnly(t *testing.T) {
	store, _ := openStore(t)
	seed(t, store)
	ctx := context.Background()
	current := openIncident(t, store, "inc_1", "k", baseTime().Add(30*time.Second))
	attempt := incident.Attempt{
		NotificationID: "ntf_inc_1",
		IncidentID:     current.ID,
		ResponderID:    "alice",
		Channel:        incident.ChannelWebhook,
		Number:         1,
		Outcome:        incident.OutcomeDelivered,
		AttemptedAt:    baseTime(),
		State:          incident.NotificationDelivered,
	}
	if err := store.RecordAttempt(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`UPDATE incident_events SET actor = 'forged'`,
		`DELETE FROM incident_events`,
		`UPDATE notification_attempts SET outcome = 'delivered'`,
		`DELETE FROM notification_attempts`,
	}
	for _, statement := range statements {
		if _, err := store.db.ExecContext(ctx, statement); err == nil {
			t.Fatalf("append-only trigger did not reject: %s", statement)
		}
	}
}

func TestRecordAttemptAdvancesStateAndRejectsReplay(t *testing.T) {
	store, _ := openStore(t)
	seed(t, store)
	ctx := context.Background()
	current := openIncident(t, store, "inc_1", "k", baseTime().Add(30*time.Second))
	retry := incident.Attempt{
		NotificationID: "ntf_inc_1",
		IncidentID:     current.ID,
		ResponderID:    "alice",
		Channel:        incident.ChannelWebhook,
		Number:         1,
		Outcome:        incident.OutcomeRetrying,
		Error:          "transport failure",
		AttemptedAt:    baseTime(),
		NextAttemptAt:  baseTime().Add(10 * time.Second),
		State:          incident.NotificationPending,
	}
	if err := store.RecordAttempt(ctx, retry); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAttempt(ctx, retry); !errors.Is(err, incident.ErrConflict) {
		t.Fatalf("re-recording attempt 1 must conflict, got %v", err)
	}
	notifications, err := store.Notifications(ctx, current.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 1 || notifications[0].AttemptCount != 1 || notifications[0].State != incident.NotificationPending {
		t.Fatalf("unexpected notification state %#v", notifications)
	}
	attempts, err := store.Attempts(ctx, incident.AttemptFilter{IncidentID: current.ID}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || attempts[0].Number != 1 || attempts[0].Outcome != incident.OutcomeRetrying {
		t.Fatalf("exactly one audit row per try: %#v", attempts)
	}
	bad := retry
	bad.Number = 2
	bad.State = incident.NotificationDelivered
	bad.NextAttemptAt = baseTime().Add(time.Minute)
	if err := store.RecordAttempt(ctx, bad); !errors.Is(err, incident.ErrInvalid) {
		t.Fatalf("only a pending attempt carries a next time, got %v", err)
	}
}

func TestClaimNotificationFencesConcurrentSenders(t *testing.T) {
	store, _ := openStore(t)
	seed(t, store)
	ctx := context.Background()
	openIncident(t, store, "inc_1", "k", baseTime().Add(30*time.Second))
	due, err := store.DueNotifications(ctx, baseTime(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("expected one due page, got %d", len(due))
	}
	if due[0].Responder.WebhookSecret != "whsec_a" || due[0].ServiceName != "Checkout" {
		t.Fatalf("the dispatch must carry private contact data: %#v", due[0])
	}
	lease := baseTime().Add(30 * time.Second)
	if err := store.ClaimNotification(ctx, due[0].Notification, lease); err != nil {
		t.Fatal(err)
	}
	if err := store.ClaimNotification(ctx, due[0].Notification, lease); !errors.Is(err, incident.ErrConflict) {
		t.Fatalf("a second claim on the same observation must lose, got %v", err)
	}
	stillDue, err := store.DueNotifications(ctx, baseTime(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(stillDue) != 0 {
		t.Fatalf("a leased page is not due until the lease expires, got %d", len(stillDue))
	}
}

func TestDueEscalationsHonorsStateAndTime(t *testing.T) {
	store, _ := openStore(t)
	seed(t, store)
	ctx := context.Background()
	armed := openIncident(t, store, "inc_1", "a", baseTime().Add(30*time.Second))
	openIncident(t, store, "inc_2", "b", time.Time{})
	due, err := store.DueEscalations(ctx, baseTime().Add(29*time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("nothing is due before the timer instant, got %d", len(due))
	}
	due, err = store.DueEscalations(ctx, baseTime().Add(30*time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != armed.ID {
		t.Fatalf("only the armed incident is due: %#v", due)
	}
	if _, err := store.DueEscalations(ctx, time.Time{}, 10); !errors.Is(err, incident.ErrInvalid) {
		t.Fatalf("a zero instant must be rejected, got %v", err)
	}
}

func TestDurableStateSurvivesReopeningTheSameFile(t *testing.T) {
	store, path := openStore(t)
	seed(t, store)
	ctx := context.Background()
	current := openIncident(t, store, "inc_1", "k", baseTime().Add(30*time.Second))
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	loaded, err := reopened.Incident(ctx, current.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.EscalateAt.Equal(current.EscalateAt) {
		t.Fatalf("the escalation timer must survive a restart: %s vs %s", loaded.EscalateAt, current.EscalateAt)
	}
	due, err := reopened.DueEscalations(ctx, baseTime().Add(time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("the reconstructed timer must be due after a restart, got %d", len(due))
	}
	pending, err := reopened.DueNotifications(ctx, baseTime(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending pages must survive a restart, got %d", len(pending))
	}
}

func TestCatalogRejectsDanglingAndDuplicateRegistrations(t *testing.T) {
	store, _ := openStore(t)
	seed(t, store)
	ctx := context.Background()
	now := baseTime()
	if err := store.CreateResponder(ctx, incident.Responder{ID: "alice", CreatedAt: now}); !errors.Is(err, incident.ErrConflict) {
		t.Fatalf("a duplicate responder must conflict, got %v", err)
	}
	ghost := oncall.Schedule{Layers: []oncall.Layer{{
		Name:       "ghost",
		Start:      now,
		Rotation:   oncall.Duration(time.Hour),
		Responders: []string{"nobody"},
	}}}
	if err := store.CreateSchedule(ctx, "ghost", "Ghost", ghost, now); !errors.Is(err, incident.ErrNotFound) {
		t.Fatalf("a schedule naming an unknown responder must be rejected, got %v", err)
	}
	if _, err := store.Schedule(ctx, "ghost"); !errors.Is(err, incident.ErrNotFound) {
		t.Fatalf("the rejected schedule must not have been stored, got %v", err)
	}
	danglingPolicy := incident.EscalationPolicy{
		ID:        "dangling",
		Name:      "Dangling",
		Levels:    []incident.Level{{Timeout: oncall.Duration(time.Minute), Schedules: []string{"missing"}}},
		CreatedAt: now,
	}
	if err := store.CreatePolicy(ctx, danglingPolicy); !errors.Is(err, incident.ErrNotFound) {
		t.Fatalf("a policy naming an unknown schedule must be rejected, got %v", err)
	}
	orphan := incident.Service{ID: "orphan", Name: "Orphan", RoutingKey: "rk-2", PolicyID: "missing", CreatedAt: now}
	if err := store.CreateService(ctx, orphan); !errors.Is(err, incident.ErrNotFound) {
		t.Fatalf("a service naming an unknown policy must be rejected, got %v", err)
	}
}

func TestTargetsResolvesEverythingAPolicyCanPage(t *testing.T) {
	store, _ := openStore(t)
	seed(t, store)
	targets, err := store.Targets(context.Background(), "ladder")
	if err != nil {
		t.Fatal(err)
	}
	if len(targets.Policy.Levels) != 2 || targets.Policy.Repeat != 0 {
		t.Fatalf("unexpected policy %#v", targets.Policy)
	}
	if _, ok := targets.Schedules["primary"]; !ok {
		t.Fatal("the level schedule must be loaded")
	}
	for _, id := range []string{"alice", "bob"} {
		if _, ok := targets.Responders[id]; !ok {
			t.Fatalf("responder %s must be loaded", id)
		}
	}
	responders, err := targets.LevelResponders(0, baseTime())
	if err != nil {
		t.Fatal(err)
	}
	if len(responders) != 1 || responders[0].ID != "alice" {
		t.Fatalf("level 0 resolves through the schedule: %#v", responders)
	}
	if _, err := store.Targets(context.Background(), "missing"); !errors.Is(err, incident.ErrNotFound) {
		t.Fatalf("an unknown policy must be reported, got %v", err)
	}
}

func TestIncidentAndAttemptReadsAreFilteredAndBounded(t *testing.T) {
	store, _ := openStore(t)
	seed(t, store)
	ctx := context.Background()
	openIncident(t, store, "inc_1", "a", baseTime().Add(30*time.Second))
	openIncident(t, store, "inc_2", "b", baseTime().Add(30*time.Second))
	all, err := store.Incidents(ctx, incident.Filter{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected two incidents, got %d", len(all))
	}
	byService, err := store.Incidents(ctx, incident.Filter{ServiceID: "checkout", State: incident.StateTriggered}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(byService) != 2 {
		t.Fatalf("filter must match, got %d", len(byService))
	}
	none, err := store.Incidents(ctx, incident.Filter{State: incident.StateResolved}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("no incident is resolved, got %d", len(none))
	}
	if _, err := store.Incidents(ctx, incident.Filter{}, 0); !errors.Is(err, incident.ErrInvalid) {
		t.Fatalf("a zero limit must be rejected, got %v", err)
	}
	if _, err := store.Attempts(ctx, incident.AttemptFilter{}, 1001); !errors.Is(err, incident.ErrInvalid) {
		t.Fatalf("an over-large limit must be rejected, got %v", err)
	}
	if _, err := store.Incident(ctx, "missing"); !errors.Is(err, incident.ErrNotFound) {
		t.Fatalf("an unknown incident must be reported, got %v", err)
	}
	if _, err := store.ServiceByRoutingKey(ctx, "nope"); !errors.Is(err, incident.ErrNotFound) {
		t.Fatalf("an unknown routing key must be reported, got %v", err)
	}
}
