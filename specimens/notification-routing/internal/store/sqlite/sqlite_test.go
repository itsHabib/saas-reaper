package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/notification-routing/internal/routing"
)

func TestOpenEnforcesPrivateDatabaseMode(t *testing.T) {
	for _, preexisting := range []bool{false, true} {
		name := "new"
		if preexisting {
			name = "preexisting"
		}
		t.Run(name, func(t *testing.T) {
			assertPrivateDatabaseMode(t, preexisting)
		})
	}
}

func assertPrivateDatabaseMode(t *testing.T, preexisting bool) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "notifications.db")
	if preexisting {
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeTestStore(t, store) })
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("database mode = %#o, want 0600", info.Mode().Perm())
	}
}

func TestChannelTemplateAndRecipientRegistration(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	at := time.Unix(100, 0).UTC()
	email := seedChannel(t, store, "email", routing.KindSMTP, at)
	if err := store.RegisterChannel(ctx, email); !errors.Is(err, routing.ErrConflict) {
		t.Fatalf("duplicate channel error = %v, want conflict", err)
	}
	template := seedTemplate(t, store, "invoice-paid", "email", at)
	if err := store.CreateTemplate(ctx, template); !errors.Is(err, routing.ErrConflict) {
		t.Fatalf("duplicate template error = %v, want conflict", err)
	}
	orphan := template
	orphan.ChannelID = "missing"
	if err := store.CreateTemplate(ctx, orphan); !errors.Is(err, routing.ErrNotFound) {
		t.Fatalf("orphan template error = %v, want not found", err)
	}
	variants, err := store.Templates(ctx, "invoice-paid")
	if err != nil {
		t.Fatal(err)
	}
	if len(variants) != 1 || variants[0].Subject != template.Subject || variants[0].Body != template.Body {
		t.Fatalf("variants = %#v", variants)
	}
	recipient := seedRecipient(t, store, "cus_acme", at, routing.Binding{ChannelID: "email", Address: "a@b.example", Enabled: true})
	if err := store.CreateRecipient(ctx, recipient); !errors.Is(err, routing.ErrConflict) {
		t.Fatalf("duplicate recipient error = %v, want conflict", err)
	}
	orphanRecipient := routing.Recipient{ID: "cus_orphan", CreatedAt: at, Bindings: []routing.Binding{
		{ChannelID: "email", Address: "x@y.example", Enabled: true},
		{ChannelID: "missing", Address: "http://127.0.0.1:19402/x", Enabled: true},
	}}
	if err := store.CreateRecipient(ctx, orphanRecipient); !errors.Is(err, routing.ErrNotFound) {
		t.Fatalf("orphan binding error = %v, want not found", err)
	}
	if _, err := store.Recipient(ctx, "cus_orphan"); !errors.Is(err, routing.ErrNotFound) {
		t.Fatalf("rolled-back recipient error = %v, want not found", err)
	}
	loaded, err := store.Recipient(ctx, "cus_acme")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Bindings) != 1 || loaded.Bindings[0].Address != "a@b.example" || !loaded.Bindings[0].Enabled {
		t.Fatalf("loaded recipient = %#v", loaded)
	}
}

func TestSendDeduplicatesByKeyAndRejectsDifferentFingerprint(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	at := time.Unix(200, 0).UTC()
	seedChannel(t, store, "email", routing.KindSMTP, at)
	seedRecipient(t, store, "cus_acme", at, routing.Binding{ChannelID: "email", Address: "a@b.example", Enabled: true})
	notification := testNotification("ntf_one", "key-1", []byte(`{"a":1}`), at)
	delivery := testDelivery("del_one", notification, "email", at)
	first, err := store.Send(ctx, notification, []routing.Delivery{delivery})
	if err != nil {
		t.Fatal(err)
	}
	if first.Deduplicated || first.NotificationID != "ntf_one" || len(first.Deliveries) != 1 {
		t.Fatalf("first acceptance = %#v", first)
	}
	repeat := testNotification("ntf_two", "key-1", []byte(`{"a":1}`), at.Add(time.Second))
	second, err := store.Send(ctx, repeat, []routing.Delivery{testDelivery("del_two", repeat, "email", at)})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Deduplicated || second.NotificationID != "ntf_one" || len(second.Deliveries) != 1 || second.Deliveries[0].ID != "del_one" {
		t.Fatalf("second acceptance = %#v, want the first notification's deliveries", second)
	}
	if _, _, err := store.deliveryState(ctx, "del_two"); !errors.Is(err, routing.ErrNotFound) {
		t.Fatalf("deduplicated send persisted a delivery: %v", err)
	}
	different := testNotification("ntf_three", "key-1", []byte(`{"a":2}`), at)
	_, err = store.Send(ctx, different, []routing.Delivery{testDelivery("del_three", different, "email", at)})
	if !errors.Is(err, routing.ErrConflict) {
		t.Fatalf("different fingerprint error = %v, want conflict", err)
	}
}

func TestSendSkipsChannelDisabledAfterPolicySnapshot(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	at := time.Unix(300, 0).UTC()
	seedChannel(t, store, "email", routing.KindSMTP, at)
	seedChannel(t, store, "chat", routing.KindSlackWebhook, at)
	seedRecipient(t, store, "cus_acme", at,
		routing.Binding{ChannelID: "email", Address: "a@b.example", Enabled: true},
		routing.Binding{ChannelID: "chat", Address: "http://127.0.0.1:19402/hook", Enabled: true},
	)
	if _, err := store.DisableChannel(ctx, "chat", 1, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	notification := testNotification("ntf_one", "key-1", []byte(`{"a":1}`), at.Add(2*time.Second))
	acceptance, err := store.Send(ctx, notification, []routing.Delivery{
		testDelivery("del_email", notification, "email", at),
		testDelivery("del_chat", notification, "chat", at),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(acceptance.Deliveries) != 1 || acceptance.Deliveries[0].ChannelID != "email" {
		t.Fatalf("acceptance = %#v, want only the email delivery queued", acceptance)
	}
	if _, _, err := store.deliveryState(ctx, "del_chat"); !errors.Is(err, routing.ErrNotFound) {
		t.Fatalf("disabled channel delivery state error = %v, want not found", err)
	}
	due, err := store.Due(ctx, at.Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].DeliveryID != "del_email" || due[0].Kind != routing.KindSMTP {
		t.Fatalf("due = %#v", due)
	}
}

func TestDisableCancelsPendingAndAuditsInFlightAttemptWithoutTransition(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	at := time.Unix(400, 0).UTC()
	seedChannel(t, store, "chat", routing.KindSlackWebhook, at)
	seedRecipient(t, store, "cus_acme", at, routing.Binding{ChannelID: "chat", Address: "http://127.0.0.1:19402/hook", Enabled: true})
	notification := testNotification("ntf_one", "key-1", []byte(`{"a":1}`), at)
	if _, err := store.Send(ctx, notification, []routing.Delivery{testDelivery("del_one", notification, "chat", at)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DisableChannel(ctx, "chat", 2, at); !errors.Is(err, routing.ErrConflict) {
		t.Fatalf("stale disable error = %v, want conflict", err)
	}
	disabled, err := store.DisableChannel(ctx, "chat", 1, at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Enabled || disabled.Revision != 2 {
		t.Fatalf("disabled channel = %#v", disabled)
	}
	if _, err := store.DisableChannel(ctx, "chat", 2, at.Add(time.Second)); !errors.Is(err, routing.ErrDisabled) {
		t.Fatalf("second disable error = %v, want disabled", err)
	}
	assertDeliveryState(t, store, "del_one", routing.StateCanceled, 0)
	due, err := store.Due(ctx, at.Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("canceled delivery returned as due: %#v", due)
	}
	inFlight := testAttempt("del_one", notification, "chat", 1, at.Add(2*time.Second), routing.OutcomeDelivered)
	inFlight.Code = 200
	if err := store.RecordAttempt(ctx, inFlight); err != nil {
		t.Fatalf("in-flight attempt after cancel: %v", err)
	}
	assertDeliveryState(t, store, "del_one", routing.StateCanceled, 0)
	attempts, err := store.Attempts(ctx, routing.AttemptFilter{NotificationID: "ntf_one"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || attempts[0].Outcome != routing.OutcomeDelivered || attempts[0].State != routing.StateCanceled {
		t.Fatalf("attempts = %#v, want one delivered outcome recorded against the canceled state", attempts)
	}
}

func TestAttemptsTransitionRestartAndAppendOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notifications.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(500, 0).UTC()
	retryAt := at.Add(5 * time.Second)
	notification := seedPendingDelivery(t, store, at)
	recordFirstRetry(t, store, notification, at, retryAt)
	rejectMalformedAttempts(t, store, notification, retryAt)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeTestStore(t, reopened) })
	assertRestartedDeliveryResumes(t, reopened, notification, retryAt)
	assertAttemptAuditIsAppendOnly(t, reopened, retryAt)
}

func seedPendingDelivery(t *testing.T, store *Store, at time.Time) routing.Notification {
	t.Helper()
	ctx := context.Background()
	seedChannel(t, store, "email", routing.KindSMTP, at)
	seedRecipient(t, store, "cus_acme", at, routing.Binding{ChannelID: "email", Address: "a@b.example", Enabled: true})
	notification := testNotification("ntf_one", "key-1", []byte(`{"a":1}`), at)
	if _, err := store.Send(ctx, notification, []routing.Delivery{testDelivery("del_one", notification, "email", at)}); err != nil {
		t.Fatal(err)
	}
	if due, err := store.Due(ctx, at.Add(-time.Nanosecond), 10); err != nil || len(due) != 0 {
		t.Fatalf("delivery became due early: due=%#v err=%v", due, err)
	}
	return notification
}

func recordFirstRetry(t *testing.T, store *Store, notification routing.Notification, at, retryAt time.Time) {
	t.Helper()
	retrying := testAttempt("del_one", notification, "email", 1, at, routing.OutcomeRetrying)
	retrying.Code = 451
	retrying.NextAttemptAt = retryAt
	if err := store.RecordAttempt(context.Background(), retrying); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAttempt(context.Background(), retrying); !errors.Is(err, routing.ErrConflict) {
		t.Fatalf("replayed attempt error = %v, want conflict", err)
	}
}

func rejectMalformedAttempts(t *testing.T, store *Store, notification routing.Notification, retryAt time.Time) {
	t.Helper()
	ctx := context.Background()
	wrongActor := testAttempt("del_one", notification, "email", 2, retryAt, routing.OutcomeDelivered)
	wrongActor.Actor = "different-actor"
	if err := store.RecordAttempt(ctx, wrongActor); !errors.Is(err, routing.ErrInvalid) {
		t.Fatalf("actor mismatch error = %v, want invalid", err)
	}
	badTransition := testAttempt("del_one", notification, "email", 2, retryAt, routing.OutcomeDelivered)
	badTransition.State = routing.StatePending
	if err := store.RecordAttempt(ctx, badTransition); !errors.Is(err, routing.ErrInvalid) {
		t.Fatalf("bad transition error = %v, want invalid", err)
	}
	if due, err := store.Due(ctx, retryAt.Add(-time.Nanosecond), 10); err != nil || len(due) != 0 {
		t.Fatalf("retry became due early: due=%#v err=%v", due, err)
	}
}

func assertRestartedDeliveryResumes(t *testing.T, reopened *Store, notification routing.Notification, retryAt time.Time) {
	t.Helper()
	ctx := context.Background()
	due, err := reopened.Due(ctx, retryAt, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].AttemptCount != 1 || due[0].Subject != "Invoice" || due[0].Body != "Paid" {
		t.Fatalf("restarted due = %#v", due)
	}
	delivered := testAttempt("del_one", notification, "email", 2, retryAt.Add(time.Second), routing.OutcomeDelivered)
	delivered.Code = 250
	if err := reopened.RecordAttempt(ctx, delivered); err != nil {
		t.Fatal(err)
	}
	assertDeliveryState(t, reopened, "del_one", routing.StateDelivered, 2)
}

func assertAttemptAuditIsAppendOnly(t *testing.T, reopened *Store, retryAt time.Time) {
	t.Helper()
	ctx := context.Background()
	attempts, err := reopened.Attempts(ctx, routing.AttemptFilter{ChannelID: "email"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 || attempts[0].Number != 2 || attempts[1].Number != 1 || !attempts[1].NextAttemptAt.Equal(retryAt) {
		t.Fatalf("newest-first attempts = %#v", attempts)
	}
	if _, err := reopened.db.ExecContext(
		ctx, `UPDATE delivery_attempts SET error_text = 'x' WHERE sequence = ?`, attempts[0].Sequence,
	); err == nil {
		t.Fatal("attempt audit update unexpectedly succeeded")
	}
	if _, err := reopened.db.ExecContext(
		ctx, `DELETE FROM delivery_attempts WHERE sequence = ?`, attempts[0].Sequence,
	); err == nil {
		t.Fatal("attempt audit delete unexpectedly succeeded")
	}
}

func TestAttemptInsertFailureRollsBackDeliveryTransition(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	at := time.Unix(600, 0).UTC()
	seedChannel(t, store, "email", routing.KindSMTP, at)
	seedRecipient(t, store, "cus_acme", at, routing.Binding{ChannelID: "email", Address: "a@b.example", Enabled: true})
	notification := testNotification("ntf_one", "key-1", []byte(`{"a":1}`), at)
	if _, err := store.Send(ctx, notification, []routing.Delivery{testDelivery("del_one", notification, "email", at)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `CREATE TRIGGER force_attempt_insert_failure
		BEFORE INSERT ON delivery_attempts BEGIN SELECT RAISE(ABORT, 'forced attempt insert failure'); END`); err != nil {
		t.Fatal(err)
	}
	delivered := testAttempt("del_one", notification, "email", 1, at.Add(time.Second), routing.OutcomeDelivered)
	if err := store.RecordAttempt(ctx, delivered); err == nil {
		t.Fatal("forced attempt insert unexpectedly succeeded")
	}
	assertDeliveryState(t, store, "del_one", routing.StatePending, 0)
	if _, err := store.db.ExecContext(ctx, `DROP TRIGGER force_attempt_insert_failure`); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAttempt(ctx, delivered); err != nil {
		t.Fatal(err)
	}
	assertDeliveryState(t, store, "del_one", routing.StateDelivered, 1)
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "notifications.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeTestStore(t, store) })
	return store
}

func closeTestStore(t *testing.T, store *Store) {
	t.Helper()
	if err := store.Close(); err != nil {
		t.Errorf("close store: %v", err)
	}
}

func seedChannel(t *testing.T, store *Store, id string, kind routing.ChannelKind, at time.Time) routing.Channel {
	t.Helper()
	channel, err := routing.NewChannel(id, kind, at)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterChannel(context.Background(), channel); err != nil {
		t.Fatal(err)
	}
	return channel
}

func seedTemplate(t *testing.T, store *Store, key, channelID string, at time.Time) routing.Template {
	t.Helper()
	template, err := routing.NewTemplate(key, channelID, "Invoice {{id}}", "Paid {{amount}}", at)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTemplate(context.Background(), template); err != nil {
		t.Fatal(err)
	}
	return template
}

func seedRecipient(t *testing.T, store *Store, id string, at time.Time, bindings ...routing.Binding) routing.Recipient {
	t.Helper()
	recipient := routing.Recipient{ID: id, Bindings: bindings, CreatedAt: at}
	if err := store.CreateRecipient(context.Background(), recipient); err != nil {
		t.Fatal(err)
	}
	return recipient
}

func testNotification(id, key string, payload []byte, at time.Time) routing.Notification {
	return routing.Notification{
		ID:             id,
		IdempotencyKey: key,
		Fingerprint:    routing.Fingerprint("invoice-paid", "cus_acme", payload),
		TemplateKey:    "invoice-paid",
		RecipientID:    "cus_acme",
		Payload:        payload,
		Actor:          "configured-actor",
		CreatedAt:      at,
	}
}

func testDelivery(id string, notification routing.Notification, channelID string, at time.Time) routing.Delivery {
	return routing.Delivery{
		ID:             id,
		NotificationID: notification.ID,
		RecipientID:    notification.RecipientID,
		ChannelID:      channelID,
		Actor:          notification.Actor,
		Address:        "a@b.example",
		Subject:        "Invoice",
		Body:           "Paid",
		State:          routing.StatePending,
		NextAttemptAt:  at,
		CreatedAt:      at,
	}
}

func testAttempt(
	deliveryID string,
	notification routing.Notification,
	channelID string,
	number int,
	at time.Time,
	outcome routing.AttemptOutcome,
) routing.Attempt {
	transition, _ := routing.TransitionFor(outcome)
	return routing.Attempt{
		DeliveryID:     deliveryID,
		NotificationID: notification.ID,
		RecipientID:    notification.RecipientID,
		ChannelID:      channelID,
		Actor:          notification.Actor,
		Number:         number,
		Outcome:        outcome,
		AttemptedAt:    at,
		State:          transition.State,
	}
}

func assertDeliveryState(t *testing.T, store *Store, id string, wantState routing.DeliveryState, wantAttempts int) {
	t.Helper()
	state, attemptCount, err := store.deliveryState(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if state != wantState || attemptCount != wantAttempts {
		t.Fatalf("delivery %s state/count = %s/%d, want %s/%d", id, state, attemptCount, wantState, wantAttempts)
	}
}
