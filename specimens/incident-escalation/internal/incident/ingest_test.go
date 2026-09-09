package incident

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIngestDedupsTriggersIntoOneIncident(t *testing.T) {
	desk, store, clock := seededDesk(t, 0)
	ctx := context.Background()
	first, err := desk.Ingest(ctx, triggerAlert("checkout-5xx"))
	if err != nil {
		t.Fatal(err)
	}
	if first.DedupKey != "checkout-5xx" {
		t.Fatalf("receipt must echo the dedup key, got %q", first.DedupKey)
	}
	clock.Advance(time.Second)
	if _, err := desk.Ingest(ctx, triggerAlert("checkout-5xx")); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)
	if _, err := desk.Ingest(ctx, triggerAlert("checkout-5xx")); err != nil {
		t.Fatal(err)
	}
	if len(store.incidents) != 1 {
		t.Fatalf("three triggers with one dedup key must open one incident, got %d", len(store.incidents))
	}
	var id string
	for key := range store.incidents {
		id = key
	}
	kinds := store.eventKinds(id)
	if len(kinds) != 3 || kinds[0] != EventOpened || kinds[1] != EventRetriggered || kinds[2] != EventRetriggered {
		t.Fatalf("duplicates must be journaled, got %v", kinds)
	}
	pages := store.notificationsFor(id)
	if len(pages) != 2 {
		t.Fatalf("only the opening page fans out to alice's two channels, got %d", len(pages))
	}
}

func TestIngestSeparatesDedupKeysAndReopensAfterResolve(t *testing.T) {
	desk, store, clock := seededDesk(t, 0)
	ctx := context.Background()
	if _, err := desk.Ingest(ctx, triggerAlert("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := desk.Ingest(ctx, triggerAlert("b")); err != nil {
		t.Fatal(err)
	}
	if len(store.incidents) != 2 {
		t.Fatalf("distinct dedup keys open distinct incidents, got %d", len(store.incidents))
	}
	resolve := triggerAlert("a")
	resolve.Action = ActionResolve
	if _, err := desk.Ingest(ctx, resolve); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	if _, err := desk.Ingest(ctx, triggerAlert("a")); err != nil {
		t.Fatal(err)
	}
	if len(store.incidents) != 3 {
		t.Fatalf("a trigger after resolve opens a new incident, got %d", len(store.incidents))
	}
}

func TestIngestAcknowledgeAndResolveFromTheWire(t *testing.T) {
	desk, store, _ := seededDesk(t, 0)
	ctx := context.Background()
	if _, err := desk.Ingest(ctx, triggerAlert("k")); err != nil {
		t.Fatal(err)
	}
	ack := Alert{RoutingKey: "rk-checkout", Action: ActionAcknowledge, DedupKey: "k"}
	if _, err := desk.Ingest(ctx, ack); err != nil {
		t.Fatal(err)
	}
	current, err := store.OpenIncident(ctx, "checkout", "k")
	if err != nil {
		t.Fatal(err)
	}
	if current.State != StateAcknowledged || !current.EscalateAt.IsZero() {
		t.Fatalf("wire acknowledge must stop escalation, got %#v", current)
	}
	if got := current.Revision; got != 2 {
		t.Fatalf("acknowledge advances the revision once, got %d", got)
	}
	resolve := Alert{RoutingKey: "rk-checkout", Action: ActionResolve, DedupKey: "k"}
	if _, err := desk.Ingest(ctx, resolve); err != nil {
		t.Fatal(err)
	}
	if _, err := store.OpenIncident(ctx, "checkout", "k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resolve closes the incident, got %v", err)
	}
}

func TestIngestAcceptsUnmatchedAcknowledgeAndResolve(t *testing.T) {
	desk, store, _ := seededDesk(t, 0)
	ctx := context.Background()
	for _, action := range []Action{ActionAcknowledge, ActionResolve} {
		receipt, err := desk.Ingest(ctx, Alert{RoutingKey: "rk-checkout", Action: action, DedupKey: "ghost"})
		if err != nil {
			t.Fatalf("%s for an unknown dedup key must be accepted: %v", action, err)
		}
		if receipt.DedupKey != "ghost" {
			t.Fatalf("receipt must echo the dedup key, got %q", receipt.DedupKey)
		}
	}
	if len(store.incidents) != 0 {
		t.Fatalf("no incident may be created, got %d", len(store.incidents))
	}
}

func TestIngestGeneratesADedupKeyWhenAbsent(t *testing.T) {
	desk, store, _ := seededDesk(t, 0)
	alert := triggerAlert("")
	receipt, err := desk.Ingest(context.Background(), alert)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.DedupKey == "" {
		t.Fatal("the service must mint a dedup key when the sender omits one")
	}
	for _, current := range store.incidents {
		if current.DedupKey != receipt.DedupKey {
			t.Fatalf("stored dedup key %q does not match the receipt %q", current.DedupKey, receipt.DedupKey)
		}
	}
}

func TestIngestRejectsBadEventsAndUnknownRoutingKeys(t *testing.T) {
	desk, _, _ := seededDesk(t, 0)
	ctx := context.Background()
	cases := map[string]Alert{
		"unknown routing key": {RoutingKey: "nope", Action: ActionTrigger, Summary: "s", Source: "p", Severity: SeverityError},
		"missing routing key": {Action: ActionTrigger, Summary: "s", Source: "p", Severity: SeverityError},
		"bad action":          {RoutingKey: "rk-checkout", Action: Action("page"), Summary: "s", Source: "p", Severity: SeverityError},
		"missing summary":     {RoutingKey: "rk-checkout", Action: ActionTrigger, Source: "p", Severity: SeverityError},
		"missing source":      {RoutingKey: "rk-checkout", Action: ActionTrigger, Summary: "s", Severity: SeverityError},
		"bad severity":        {RoutingKey: "rk-checkout", Action: ActionTrigger, Summary: "s", Source: "p", Severity: Severity("bad")},
		"ack without key":     {RoutingKey: "rk-checkout", Action: ActionAcknowledge},
	}
	for name, alert := range cases {
		if _, err := desk.Ingest(ctx, alert); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: expected ErrInvalid, got %v", name, err)
		}
	}
}

func TestIngestRetriesOnceThroughAnOpeningRace(t *testing.T) {
	desk, store, _ := seededDesk(t, 0)
	store.failCreate = 1
	if _, err := desk.Ingest(context.Background(), triggerAlert("racy")); err != nil {
		t.Fatalf("a lost opening race must be retried: %v", err)
	}
	if len(store.incidents) != 1 {
		t.Fatalf("the retry must open exactly one incident, got %d", len(store.incidents))
	}
}

func TestManagementActionsUseTheConfiguredPrincipal(t *testing.T) {
	desk, store, _ := seededDesk(t, 0)
	ctx := context.Background()
	if _, err := desk.Ingest(ctx, triggerAlert("k")); err != nil {
		t.Fatal(err)
	}
	current, err := store.OpenIncident(ctx, "checkout", "k")
	if err != nil {
		t.Fatal(err)
	}
	acked, err := desk.Acknowledge(ctx, current.ID)
	if err != nil {
		t.Fatal(err)
	}
	if acked.State != StateAcknowledged {
		t.Fatalf("expected acknowledged, got %s", acked.State)
	}
	actors := map[string]bool{}
	for _, event := range store.events {
		actors[event.Actor] = true
	}
	if !actors["service:checkout"] || !actors["operator"] {
		t.Fatalf("the journal must attribute the wire and the operator separately: %v", actors)
	}
	if _, err := desk.Resolve(ctx, current.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := desk.Resolve(ctx, current.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("resolving twice must conflict, got %v", err)
	}
}
