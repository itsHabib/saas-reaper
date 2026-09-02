package sqlite

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/webhook-delivery/internal/delivery"
)

const sqliteTestSecret = "whsec_MDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDA="

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

func TestStoreRejectsActorMismatches(t *testing.T) {
	store := openTestStore(t)
	at := time.Unix(90, 0).UTC()
	endpoint := testEndpoint(t, "ep_actor", at)
	if _, err := store.RegisterEndpoint(t.Context(), endpoint, 0); err != nil {
		t.Fatal(err)
	}
	message := testMessage("msg_actor", at)
	item := testDelivery("del_actor", message.ID, endpoint.ID, delivery.DeliveryOriginal, at)
	item.Actor = "different-actor"
	if _, err := store.Publish(t.Context(), message, []delivery.Delivery{item}); !errors.Is(err, delivery.ErrInvalid) {
		t.Fatalf("publication actor mismatch error = %v, want invalid", err)
	}
	item.Actor = message.Actor
	if _, err := store.Publish(t.Context(), message, []delivery.Delivery{item}); err != nil {
		t.Fatal(err)
	}
	attempt := testAttempt(item, "different-actor", 1, at, delivery.OutcomeDelivered, delivery.StateSucceeded)
	attempt.StatusCode = 204
	if err := store.RecordAttempt(t.Context(), attempt); !errors.Is(err, delivery.ErrInvalid) {
		t.Fatalf("attempt actor mismatch error = %v, want invalid", err)
	}
	assertDeliveryState(t, store, item.ID, delivery.StatePending, 0)
}

func assertPrivateDatabaseMode(t *testing.T, preexisting bool) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "webhooks.db")
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

func TestEndpointRevisionDisableAndPublicationRace(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	at := time.Unix(100, 0).UTC()
	endpoint := testEndpoint(t, "ep_primary", at)
	registered, err := store.RegisterEndpoint(ctx, endpoint, 0)
	if err != nil {
		t.Fatal(err)
	}
	if registered.Revision != 1 || !registered.Enabled {
		t.Fatalf("registered endpoint = %#v", registered)
	}
	if _, err := store.RegisterEndpoint(ctx, endpoint, 0); !errors.Is(err, delivery.ErrConflict) {
		t.Fatalf("duplicate registration error = %v, want conflict", err)
	}
	if _, err := store.DisableEndpoint(ctx, endpoint.ID, 2, at.Add(time.Second)); !errors.Is(err, delivery.ErrConflict) {
		t.Fatalf("stale disable error = %v, want conflict", err)
	}
	message := testMessage("msg_before_disable", at)
	item := testDelivery("del_before_disable", message.ID, endpoint.ID, delivery.DeliveryOriginal, at)
	if _, err := store.Publish(ctx, message, []delivery.Delivery{item}); err != nil {
		t.Fatal(err)
	}
	disabled, err := store.DisableEndpoint(ctx, endpoint.ID, 1, at.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Enabled || disabled.Revision != 2 {
		t.Fatalf("disabled endpoint = %#v", disabled)
	}
	due, err := store.Due(ctx, at.Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("disabled endpoint returned due work: %#v", due)
	}
	assertDeliveryState(t, store, item.ID, delivery.StateDisabled, 0)

	assertDisabledEndpointAcceptsNoWork(t, store, endpoint.ID, message.ID, at)
}

func assertDisabledEndpointAcceptsNoWork(
	t *testing.T,
	store *Store,
	endpointID string,
	messageID string,
	at time.Time,
) {
	t.Helper()
	ctx := context.Background()
	lateMessage := testMessage("msg_after_disable", at.Add(3*time.Second))
	late := testDelivery(
		"del_after_disable",
		lateMessage.ID,
		endpointID,
		delivery.DeliveryOriginal,
		lateMessage.CreatedAt,
	)
	queued, err := store.Publish(ctx, lateMessage, []delivery.Delivery{late})
	if err != nil {
		t.Fatalf("publication to newly disabled endpoint error = %v, want message kept with nothing queued", err)
	}
	if len(queued) != 0 {
		t.Fatalf("disabled endpoint was queued: %#v", queued)
	}
	if _, err := store.Message(ctx, lateMessage.ID); err != nil {
		t.Fatalf("message without enabled endpoints was not kept: %v", err)
	}
	replay := testDelivery(
		"del_disabled_replay",
		messageID,
		endpointID,
		delivery.DeliveryReplay,
		at.Add(4*time.Second),
	)
	if err := store.Replay(ctx, replay); !errors.Is(err, delivery.ErrDisabled) {
		t.Fatalf("disabled replay error = %v, want disabled", err)
	}
}

func TestPublishDueAttemptsReplayAndRestart(t *testing.T) {
	fixture := prepareRestartFixture(t)
	assertInitialDue(t, fixture)
	replay, replayAt := recordRetryAndReplay(t, fixture)
	reopened := reopenStore(t, fixture.store, fixture.path)
	assertRestartedMessageAndReplay(t, reopened, fixture, replay, replayAt)
	assertRestartedAttempts(t, reopened, fixture)
}

type restartFixture struct {
	store    *Store
	path     string
	at       time.Time
	endpoint delivery.Endpoint
	message  delivery.Message
	original delivery.Delivery
	payload  []byte
}

func prepareRestartFixture(t *testing.T) restartFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "webhooks.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(200, 123).UTC()
	endpoint := testEndpoint(t, "ep_restart", at)
	if _, err := store.RegisterEndpoint(t.Context(), endpoint, 0); err != nil {
		t.Fatal(err)
	}
	payload := []byte("{ \"exact\" : true }\n")
	message := delivery.Message{
		ID:        "msg_restart",
		Payload:   payload,
		Actor:     "configured-actor",
		CreatedAt: at,
	}
	original := testDelivery("del_original", message.ID, endpoint.ID, delivery.DeliveryOriginal, at)
	if _, err := store.Publish(t.Context(), message, []delivery.Delivery{original}); err != nil {
		t.Fatal(err)
	}
	return restartFixture{
		store:    store,
		path:     path,
		at:       at,
		endpoint: endpoint,
		message:  message,
		original: original,
		payload:  payload,
	}
}

func assertInitialDue(t *testing.T, fixture restartFixture) {
	t.Helper()
	beforeDue, err := fixture.store.Due(t.Context(), fixture.at.Add(-time.Nanosecond), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(beforeDue) != 0 {
		t.Fatalf("delivery became due early: %#v", beforeDue)
	}
	due, err := fixture.store.Due(t.Context(), fixture.at, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("due = %#v, want one delivery", due)
	}
	if string(due[0].Payload) != string(fixture.payload) || due[0].Actor != fixture.message.Actor {
		t.Fatalf("due dispatch lost exact payload or actor: %#v", due[0])
	}
	if due[0].Destination != fixture.endpoint.URL || due[0].Secret != fixture.endpoint.Secret {
		t.Fatalf("due dispatch lost endpoint private data: %#v", due[0])
	}
}

func recordRetryAndReplay(t *testing.T, fixture restartFixture) (delivery.Delivery, time.Time) {
	t.Helper()
	retryAt := fixture.at.Add(5 * time.Second)
	retrying := testAttempt(
		fixture.original,
		fixture.message.Actor,
		1,
		fixture.at,
		delivery.OutcomeRetrying,
		delivery.StatePending,
	)
	retrying.StatusCode = 500
	retrying.NextAttemptAt = retryAt
	if err := fixture.store.RecordAttempt(t.Context(), retrying); err != nil {
		t.Fatal(err)
	}
	if due, err := fixture.store.Due(t.Context(), retryAt.Add(-time.Nanosecond), 10); err != nil || len(due) != 0 {
		t.Fatalf("retry became due early: due=%#v err=%v", due, err)
	}
	due, err := fixture.store.Due(t.Context(), retryAt, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].AttemptCount != 1 {
		t.Fatalf("retry due = %#v, want attempt count 1", due)
	}
	deliveredAt := retryAt.Add(time.Second)
	delivered := testAttempt(
		fixture.original,
		fixture.message.Actor,
		2,
		deliveredAt,
		delivery.OutcomeDelivered,
		delivery.StateSucceeded,
	)
	delivered.StatusCode = 204
	if err := fixture.store.RecordAttempt(t.Context(), delivered); err != nil {
		t.Fatal(err)
	}
	replayAt := deliveredAt.Add(time.Second)
	replay := testDelivery(
		"del_replay",
		fixture.message.ID,
		fixture.endpoint.ID,
		delivery.DeliveryReplay,
		replayAt,
	)
	replay.Actor = "replay-actor"
	if err := fixture.store.Replay(t.Context(), replay); err != nil {
		t.Fatal(err)
	}
	return replay, replayAt
}

func reopenStore(t *testing.T, current *Store, path string) *Store {
	t.Helper()
	if err := current.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeTestStore(t, reopened) })
	return reopened
}

func assertRestartedMessageAndReplay(
	t *testing.T,
	reopened *Store,
	fixture restartFixture,
	replay delivery.Delivery,
	replayAt time.Time,
) {
	t.Helper()
	stored, err := reopened.Message(t.Context(), fixture.message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored.Payload) != string(fixture.payload) || stored.Actor != fixture.message.Actor {
		t.Fatalf("restarted message = %#v", stored)
	}
	restartedDue, err := reopened.Due(t.Context(), replayAt, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(restartedDue) != 1 || restartedDue[0].DeliveryID != replay.ID {
		t.Fatalf("restarted due = %#v, want replay", restartedDue)
	}
	if restartedDue[0].MessageID != fixture.original.MessageID {
		t.Fatalf("replay webhook id = %q, want %q", restartedDue[0].MessageID, fixture.original.MessageID)
	}
	if restartedDue[0].Actor != replay.Actor {
		t.Fatalf("replay actor = %q, want %q", restartedDue[0].Actor, replay.Actor)
	}
}

func assertRestartedAttempts(t *testing.T, reopened *Store, fixture restartFixture) {
	t.Helper()
	attempts, err := reopened.Attempts(
		t.Context(),
		delivery.AttemptFilter{MessageID: fixture.message.ID},
		10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 || attempts[0].Number != 2 || attempts[1].Number != 1 {
		t.Fatalf("newest-first attempts = %#v", attempts)
	}
	filtered, err := reopened.Attempts(
		t.Context(),
		delivery.AttemptFilter{EndpointID: fixture.endpoint.ID},
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].Sequence != attempts[0].Sequence {
		t.Fatalf("filtered attempts = %#v, want newest attempt", filtered)
	}
}

func TestEndpointGoneAttemptCancelsOtherPendingAndAuditIsAppendOnly(t *testing.T) {
	store, endpoint, first, second, goneAt := prepareEndpointGoneFixture(t)
	recordEndpointGone(t, store, first, goneAt)
	assertEndpointGoneState(t, store, endpoint, first, second, goneAt)
	assertAttemptAppendOnly(t, store)
}

func prepareEndpointGoneFixture(
	t *testing.T,
) (*Store, delivery.Endpoint, delivery.Delivery, delivery.Delivery, time.Time) {
	t.Helper()
	store := openTestStore(t)
	at := time.Unix(300, 0).UTC()
	endpoint := testEndpoint(t, "ep_gone", at)
	if _, err := store.RegisterEndpoint(t.Context(), endpoint, 0); err != nil {
		t.Fatal(err)
	}
	firstMessage := testMessage("msg_gone_one", at)
	first := testDelivery("del_gone_one", firstMessage.ID, endpoint.ID, delivery.DeliveryOriginal, at)
	if _, err := store.Publish(t.Context(), firstMessage, []delivery.Delivery{first}); err != nil {
		t.Fatal(err)
	}
	secondMessage := testMessage("msg_gone_two", at.Add(time.Second))
	second := testDelivery(
		"del_gone_two",
		secondMessage.ID,
		endpoint.ID,
		delivery.DeliveryOriginal,
		secondMessage.CreatedAt,
	)
	if _, err := store.Publish(t.Context(), secondMessage, []delivery.Delivery{second}); err != nil {
		t.Fatal(err)
	}
	return store, endpoint, first, second, at.Add(2 * time.Second)
}

func recordEndpointGone(t *testing.T, store *Store, first delivery.Delivery, goneAt time.Time) {
	t.Helper()
	gone := testAttempt(
		first,
		"configured-actor",
		1,
		goneAt,
		delivery.OutcomeEndpointDisabled,
		delivery.StateDisabled,
	)
	gone.StatusCode = 410
	gone.DisableEndpoint = true
	if err := store.RecordAttempt(t.Context(), gone); err != nil {
		t.Fatal(err)
	}
}

func assertEndpointGoneState(
	t *testing.T,
	store *Store,
	endpoint delivery.Endpoint,
	first delivery.Delivery,
	second delivery.Delivery,
	goneAt time.Time,
) {
	t.Helper()
	storedEndpoint, err := store.Endpoint(t.Context(), endpoint.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedEndpoint.Enabled || storedEndpoint.Revision != 2 {
		t.Fatalf("410 endpoint = %#v", storedEndpoint)
	}
	assertDeliveryState(t, store, first.ID, delivery.StateDisabled, 1)
	assertDeliveryState(t, store, second.ID, delivery.StateDisabled, 0)
	due, err := store.Due(t.Context(), goneAt.Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("410-disabled endpoint returned work: %#v", due)
	}
}

func assertAttemptAppendOnly(t *testing.T, store *Store) {
	t.Helper()
	attempts, err := store.Attempts(t.Context(), delivery.AttemptFilter{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || !attempts[0].DisableEndpoint || attempts[0].Outcome != delivery.OutcomeEndpointDisabled {
		t.Fatalf("410 attempt = %#v", attempts)
	}
	if _, err := store.db.ExecContext(
		t.Context(),
		`UPDATE delivery_attempts SET error_text = error_text WHERE sequence = ?`,
		attempts[0].Sequence,
	); err == nil {
		t.Fatal("attempt audit update unexpectedly succeeded")
	}
	if _, err := store.db.ExecContext(
		t.Context(),
		`DELETE FROM delivery_attempts WHERE sequence = ?`,
		attempts[0].Sequence,
	); err == nil {
		t.Fatal("attempt audit delete unexpectedly succeeded")
	}
}

func TestAttemptInsertFailureRollsBackDeliveryTransition(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	at := time.Unix(400, 0).UTC()
	endpoint := testEndpoint(t, "ep_rollback", at)
	if _, err := store.RegisterEndpoint(ctx, endpoint, 0); err != nil {
		t.Fatal(err)
	}
	message := testMessage("msg_rollback", at)
	item := testDelivery("del_rollback", message.ID, endpoint.ID, delivery.DeliveryOriginal, at)
	if _, err := store.Publish(ctx, message, []delivery.Delivery{item}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(
		ctx,
		`CREATE TRIGGER force_attempt_insert_failure
		 BEFORE INSERT ON delivery_attempts
		 BEGIN
			SELECT RAISE(ABORT, 'forced attempt insert failure');
		 END`,
	); err != nil {
		t.Fatal(err)
	}
	retry := testAttempt(item, message.Actor, 1, at.Add(time.Second), delivery.OutcomeRetrying, delivery.StatePending)
	retry.StatusCode = 500
	retry.NextAttemptAt = at.Add(time.Minute)
	if err := store.RecordAttempt(ctx, retry); err == nil {
		t.Fatal("forced attempt insert unexpectedly succeeded")
	}
	assertDeliveryState(t, store, item.ID, delivery.StatePending, 0)
	attempts, err := store.Attempts(ctx, delivery.AttemptFilter{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 0 {
		t.Fatalf("failed insert left audit rows: %#v", attempts)
	}
	due, err := store.Due(ctx, at.Add(time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].AttemptCount != 0 {
		t.Fatalf("failed insert changed due state: %#v", due)
	}
	if _, err := store.db.ExecContext(ctx, `DROP TRIGGER force_attempt_insert_failure`); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAttempt(ctx, retry); err != nil {
		t.Fatalf("record after removing failure trigger: %v", err)
	}
	assertDeliveryState(t, store, item.ID, delivery.StatePending, 1)
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "webhooks.db"))
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

func testEndpoint(t *testing.T, id string, at time.Time) delivery.Endpoint {
	t.Helper()
	endpoint, err := delivery.NewEndpoint(id, "http://127.0.0.1:19001/webhook", sqliteTestSecret, at)
	if err != nil {
		t.Fatal(err)
	}
	return endpoint
}

func testMessage(id string, at time.Time) delivery.Message {
	return delivery.Message{
		ID:        id,
		Payload:   []byte(`{"event":"test"}`),
		Actor:     "configured-actor",
		CreatedAt: at,
	}
}

func testDelivery(
	id string,
	messageID string,
	endpointID string,
	kind delivery.DeliveryKind,
	at time.Time,
) delivery.Delivery {
	return delivery.Delivery{
		ID:            id,
		MessageID:     messageID,
		EndpointID:    endpointID,
		Actor:         "configured-actor",
		Kind:          kind,
		State:         delivery.StatePending,
		NextAttemptAt: at,
		CreatedAt:     at,
	}
}

func testAttempt(
	item delivery.Delivery,
	actor string,
	number int,
	at time.Time,
	outcome delivery.AttemptOutcome,
	state delivery.DeliveryState,
) delivery.Attempt {
	return delivery.Attempt{
		DeliveryID:       item.ID,
		MessageID:        item.MessageID,
		EndpointID:       item.EndpointID,
		Actor:            actor,
		Number:           number,
		Outcome:          outcome,
		WebhookTimestamp: at.Unix(),
		AttemptedAt:      at,
		State:            state,
	}
}

func assertDeliveryState(
	t *testing.T,
	store *Store,
	id string,
	wantState delivery.DeliveryState,
	wantAttempts int,
) {
	t.Helper()
	var state delivery.DeliveryState
	var attemptCount int
	if err := store.db.QueryRowContext(
		t.Context(),
		`SELECT state, attempt_count FROM deliveries WHERE id = ?`,
		id,
	).Scan(&state, &attemptCount); err != nil {
		t.Fatal(err)
	}
	if state != wantState || attemptCount != wantAttempts {
		t.Fatalf(
			"delivery %s state/count = %s/%d, want %s/%d",
			id,
			state,
			attemptCount,
			wantState,
			wantAttempts,
		)
	}
}

func TestRecordAttemptPersistsPermanentFailureAndRejectsForeignTransitions(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	at := time.Unix(500, 0).UTC()
	endpoint := testEndpoint(t, "ep_failed", at)
	if _, err := store.RegisterEndpoint(ctx, endpoint, 0); err != nil {
		t.Fatal(err)
	}
	message := testMessage("msg_failed", at)
	item := testDelivery("del_failed", message.ID, endpoint.ID, delivery.DeliveryOriginal, at)
	if _, err := store.Publish(ctx, message, []delivery.Delivery{item}); err != nil {
		t.Fatal(err)
	}
	foreign := testAttempt(item, message.Actor, 1, at, delivery.OutcomeFailed, delivery.StateExhausted)
	if err := store.RecordAttempt(ctx, foreign); !errors.Is(err, delivery.ErrInvalid) {
		t.Fatalf("state outside the outcome's transition error = %v, want invalid", err)
	}
	unknown := testAttempt(item, message.Actor, 1, at, delivery.AttemptOutcome("mystery"), delivery.StateFailed)
	if err := store.RecordAttempt(ctx, unknown); !errors.Is(err, delivery.ErrInvalid) {
		t.Fatalf("unknown outcome error = %v, want invalid", err)
	}
	assertDeliveryState(t, store, item.ID, delivery.StatePending, 0)
	failed := testAttempt(item, message.Actor, 1, at, delivery.OutcomeFailed, delivery.StateFailed)
	failed.Error = "permanent delivery failure: unsignable secret"
	if err := store.RecordAttempt(ctx, failed); err != nil {
		t.Fatal(err)
	}
	assertDeliveryState(t, store, item.ID, delivery.StateFailed, 1)
	due, err := store.Due(ctx, at.Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("failed delivery stayed due: %#v", due)
	}
	if _, err := store.Endpoint(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	attempts, err := store.Attempts(ctx, delivery.AttemptFilter{MessageID: message.ID}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || attempts[0].Outcome != delivery.OutcomeFailed || attempts[0].State != delivery.StateFailed {
		t.Fatalf("failed audit = %#v, want one failed row", attempts)
	}
}

func TestPublishSkipsAnEndpointDisabledSinceTheSnapshot(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	at := time.Unix(600, 0).UTC()
	healthy := testEndpoint(t, "ep_healthy", at)
	racing := testEndpoint(t, "ep_racing", at)
	for _, endpoint := range []delivery.Endpoint{healthy, racing} {
		if _, err := store.RegisterEndpoint(ctx, endpoint, 0); err != nil {
			t.Fatal(err)
		}
	}
	message := testMessage("msg_fanout", at)
	toHealthy := testDelivery("del_healthy", message.ID, healthy.ID, delivery.DeliveryOriginal, at)
	toRacing := testDelivery("del_racing", message.ID, racing.ID, delivery.DeliveryOriginal, at)
	// The caller snapshotted both endpoints enabled; the disable lands before the publication commits.
	if _, err := store.DisableEndpoint(ctx, racing.ID, 1, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	queued, err := store.Publish(ctx, message, []delivery.Delivery{toHealthy, toRacing})
	if err != nil {
		t.Fatalf("publication failed because a sibling endpoint was disabled: %v", err)
	}
	if len(queued) != 1 || queued[0].ID != toHealthy.ID {
		t.Fatalf("queued = %#v, want only the healthy delivery", queued)
	}
	if _, err := store.Message(ctx, message.ID); err != nil {
		t.Fatal(err)
	}
	assertDeliveryState(t, store, toHealthy.ID, delivery.StatePending, 0)
	var racingRows int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deliveries WHERE id = ?`, toRacing.ID).Scan(&racingRows); err != nil {
		t.Fatal(err)
	}
	if racingRows != 0 {
		t.Fatalf("disabled endpoint received %d queued deliveries, want 0", racingRows)
	}
	unknownMessage := testMessage("msg_unknown", at)
	unknown := testDelivery("del_unknown", unknownMessage.ID, "ep_missing", delivery.DeliveryOriginal, at)
	if _, err := store.Publish(ctx, unknownMessage, []delivery.Delivery{unknown}); !errors.Is(err, delivery.ErrNotFound) {
		t.Fatalf("unknown endpoint error = %v, want not found", err)
	}
}

type blockingSender struct {
	started chan struct{}
	release chan struct{}
}

func (s *blockingSender) Send(context.Context, string, []byte, delivery.Headers) (delivery.SendResult, error) {
	close(s.started)
	<-s.release
	return delivery.SendResult{StatusCode: 204}, nil
}

func TestDisableDuringSendIsALostRaceWithNoAuditRow(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	at := time.Unix(700, 0).UTC()
	endpoint := testEndpoint(t, "ep_race", at)
	if _, err := store.RegisterEndpoint(ctx, endpoint, 0); err != nil {
		t.Fatal(err)
	}
	message := testMessage("msg_race", at)
	inFlight := testDelivery("del_in_flight", message.ID, endpoint.ID, delivery.DeliveryOriginal, at)
	sibling := testDelivery("del_sibling", message.ID, endpoint.ID, delivery.DeliveryOriginal, at.Add(time.Minute))
	if _, err := store.Publish(ctx, message, []delivery.Delivery{inFlight, sibling}); err != nil {
		t.Fatal(err)
	}
	schedule, err := delivery.NewSchedule(nil)
	if err != nil {
		t.Fatal(err)
	}
	sender := &blockingSender{started: make(chan struct{}), release: make(chan struct{})}
	dispatcher, err := delivery.NewDispatcher(store, sender, schedule, func() time.Time { return at }, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		count int
		err   error
	}
	result := make(chan outcome, 1)
	go func() {
		count, deliverErr := dispatcher.DeliverDue(ctx, 10)
		result <- outcome{count: count, err: deliverErr}
	}()
	<-sender.started
	if _, err := store.DisableEndpoint(ctx, endpoint.ID, 1, at.Add(time.Second)); err != nil {
		t.Fatalf("disable waited on or failed behind an active send: %v", err)
	}
	close(sender.release)
	got := <-result
	if got.err != nil || got.count != 1 {
		t.Fatalf("lost race result = %d/%v, want one silent attempt", got.count, got.err)
	}
	assertDeliveryState(t, store, inFlight.ID, delivery.StateDisabled, 0)
	assertDeliveryState(t, store, sibling.ID, delivery.StateDisabled, 0)
	attempts, err := store.Attempts(ctx, delivery.AttemptFilter{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 0 {
		t.Fatalf("lost race left audit rows: %#v", attempts)
	}
	due, err := store.Due(ctx, at.Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("disabled endpoint still has due work: %#v", due)
	}
}
