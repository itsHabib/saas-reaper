package incident

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/oncall"
)

func TestEscalationFiresExactlyAtTheTimeoutAndPagesTheNextLevel(t *testing.T) {
	desk, store, clock := seededDesk(t, 0)
	ctx := context.Background()
	if _, err := desk.Ingest(ctx, triggerAlert("k")); err != nil {
		t.Fatal(err)
	}
	current, err := store.OpenIncident(ctx, "checkout", "k")
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(29 * time.Second)
	fired, err := desk.EscalateDue(ctx, 10)
	if err != nil || fired != 0 {
		t.Fatalf("the timer must not fire early: %d %v", fired, err)
	}
	clock.Advance(time.Second)
	fired, err = desk.EscalateDue(ctx, 10)
	if err != nil || fired != 1 {
		t.Fatalf("the timer must fire at its instant: %d %v", fired, err)
	}
	escalated, err := store.Incident(ctx, current.ID)
	if err != nil {
		t.Fatal(err)
	}
	if escalated.Level != 1 {
		t.Fatalf("expected level 1, got %d", escalated.Level)
	}
	pages := store.notificationsFor(current.ID)
	if len(pages) != 3 {
		t.Fatalf("alice's two channels plus bob's one webhook, got %d", len(pages))
	}
	level1 := 0
	for _, page := range pages {
		if page.Level != 1 {
			continue
		}
		level1++
		if page.ResponderID != "bob" {
			t.Fatalf("level 1 pages bob, got %s", page.ResponderID)
		}
	}
	if level1 != 1 {
		t.Fatalf("expected one level-1 page, got %d", level1)
	}
}

func TestAcknowledgeStopsEscalation(t *testing.T) {
	desk, store, clock := seededDesk(t, 0)
	ctx := context.Background()
	if _, err := desk.Ingest(ctx, triggerAlert("k")); err != nil {
		t.Fatal(err)
	}
	current, err := store.OpenIncident(ctx, "checkout", "k")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := desk.Acknowledge(ctx, current.ID); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Hour)
	fired, err := desk.EscalateDue(ctx, 10)
	if err != nil || fired != 0 {
		t.Fatalf("an acknowledged incident never escalates: %d %v", fired, err)
	}
	if got := len(store.notificationsFor(current.ID)); got != 2 {
		t.Fatalf("no further pages may be planned, got %d", got)
	}
}

func TestEscalationSkipsALostRaceAndKeepsGoing(t *testing.T) {
	desk, store, clock := seededDesk(t, 0)
	ctx := context.Background()
	for _, key := range []string{"a", "b"} {
		if _, err := desk.Ingest(ctx, triggerAlert(key)); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(30 * time.Second)
	// Another writer advances one incident between the due read and its
	// transition, so that row's optimistic update must lose.
	store.raceAfterDue = "a"
	fired, err := desk.EscalateDue(ctx, 10)
	if err != nil {
		t.Fatalf("a lost race is benign, got %v", err)
	}
	if fired != 1 {
		t.Fatalf("the healthy incident must still escalate, got %d", fired)
	}
}

func TestEscalateDueSurfacesLoadFailuresAndRejectsBadLimits(t *testing.T) {
	desk, store, _ := seededDesk(t, 0)
	ctx := context.Background()
	for _, limit := range []int{0, 101} {
		if _, err := desk.EscalateDue(ctx, limit); !errors.Is(err, ErrInvalid) {
			t.Fatalf("limit %d must be rejected, got %v", limit, err)
		}
	}
	store.failDue = errors.New("authority unavailable")
	if _, err := desk.EscalateDue(ctx, 10); err == nil {
		t.Fatal("a failed due query must surface")
	}
}

func TestLevelRespondersFollowTheScheduleAtTheEscalationInstant(t *testing.T) {
	start := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	targets := Targets{
		Policy: EscalationPolicy{ID: "p", Levels: []Level{{
			Timeout:   oncall.Duration(time.Minute),
			Schedules: []string{"weekly"},
		}}},
		Schedules: map[string]oncall.Schedule{"weekly": {
			Layers: []oncall.Layer{{
				Name:       "weekly",
				Start:      start,
				Rotation:   oncall.Duration(7 * 24 * time.Hour),
				Responders: []string{"alice", "bob"},
			}},
			Overrides: []oncall.Override{{
				Responder: "carol",
				Start:     start.Add(24 * time.Hour),
				End:       start.Add(48 * time.Hour),
			}},
		}},
		Responders: map[string]Responder{
			"alice": {ID: "alice", Email: "a@example.test"},
			"bob":   {ID: "bob", Email: "b@example.test"},
			"carol": {ID: "carol", Email: "c@example.test"},
		},
	}
	cases := map[time.Time]string{
		start:                         "alice",
		start.Add(36 * time.Hour):     "carol",
		start.Add(7 * 24 * time.Hour): "bob",
	}
	for at, want := range cases {
		responders, err := targets.LevelResponders(0, at)
		if err != nil {
			t.Fatal(err)
		}
		if len(responders) != 1 || responders[0].ID != want {
			t.Fatalf("at %s expected %s, got %#v", at, want, responders)
		}
	}
	if _, err := targets.LevelResponders(1, start); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an absent level must be rejected, got %v", err)
	}
	targets.Responders = map[string]Responder{}
	if _, err := targets.LevelResponders(0, start); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a dangling responder must be reported, got %v", err)
	}
}

func TestPlanNotificationsCoversEveryChannelOnlyWhenPaging(t *testing.T) {
	transition := Transition{
		Incident: Incident{ID: "inc", Level: 1, Repeat: 2},
		Event:    Event{At: time.Unix(10, 0).UTC()},
		Notify:   true,
	}
	responders := []Responder{
		{ID: "alice", Email: "a@example.test", WebhookURL: "http://127.0.0.1:1/a"},
		{ID: "bob", Email: "b@example.test"},
	}
	planned, err := PlanNotifications(transition, responders, sequentialIDs())
	if err != nil {
		t.Fatal(err)
	}
	if len(planned) != 3 {
		t.Fatalf("two channels for alice and one for bob, got %d", len(planned))
	}
	for _, notification := range planned {
		if notification.Level != 1 || notification.Repeat != 2 || notification.State != NotificationPending {
			t.Fatalf("planned page must carry the ladder position: %#v", notification)
		}
		if !notification.NextAttemptAt.Equal(transition.Event.At) {
			t.Fatalf("a page is due immediately, got %s", notification.NextAttemptAt)
		}
	}
	transition.Notify = false
	quiet, err := PlanNotifications(transition, responders, sequentialIDs())
	if err != nil || len(quiet) != 0 {
		t.Fatalf("a non-paging transition plans nothing: %d %v", len(quiet), err)
	}
}

func TestDeskRejectsIncompleteComposition(t *testing.T) {
	store := newMemoryStore()
	clock := &fakeClock{now: time.Now()}
	cases := map[string]func() (*Desk, error){
		"no store": func() (*Desk, error) { return NewDesk(nil, "a", clock.Now, sequentialIDs(), fixedSecret) },
		"no clock": func() (*Desk, error) { return NewDesk(store, "a", nil, sequentialIDs(), fixedSecret) },
		"no ids":   func() (*Desk, error) { return NewDesk(store, "a", clock.Now, nil, fixedSecret) },
		"no secret": func() (*Desk, error) {
			return NewDesk(store, "a", clock.Now, sequentialIDs(), nil)
		},
		"blank actor": func() (*Desk, error) {
			return NewDesk(store, "   ", clock.Now, sequentialIDs(), fixedSecret)
		},
	}
	for name, build := range cases {
		if _, err := build(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: expected ErrInvalid, got %v", name, err)
		}
	}
}
