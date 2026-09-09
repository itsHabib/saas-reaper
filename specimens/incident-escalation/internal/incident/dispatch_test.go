package incident

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func dispatcherFor(t *testing.T, store *memoryStore, clock *fakeClock, notifier Notifier) *Dispatcher {
	t.Helper()
	schedule, err := NewRetrySchedule([]time.Duration{time.Second, 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	notifiers := map[Channel]Notifier{ChannelWebhook: notifier, ChannelEmail: notifier}
	dispatcher, err := NewDispatcher(store, notifiers, schedule, clock.Now, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return dispatcher
}

func TestDispatcherAuditsExactlyOneRowPerTry(t *testing.T) {
	desk, store, clock := seededDesk(t, 0)
	ctx := context.Background()
	if _, err := desk.Ingest(ctx, triggerAlert("k")); err != nil {
		t.Fatal(err)
	}
	notifier := &scriptedNotifier{results: []error{errTransport, nil, nil, nil}}
	dispatcher := dispatcherFor(t, store, clock, notifier)

	sent, err := dispatcher.DeliverDue(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if sent != 2 {
		t.Fatalf("alice has two channels, got %d sends", sent)
	}
	if len(store.attempts) != 2 {
		t.Fatalf("one audit row per try, got %d", len(store.attempts))
	}
	outcomes := map[AttemptOutcome]int{}
	for _, attempt := range store.attempts {
		outcomes[attempt.Outcome]++
	}
	if outcomes[OutcomeRetrying] != 1 || outcomes[OutcomeDelivered] != 1 {
		t.Fatalf("expected one retry and one delivery, got %v", outcomes)
	}

	again, err := dispatcher.DeliverDue(ctx, 10)
	if err != nil || again != 0 {
		t.Fatalf("nothing is due before the retry delay: %d %v", again, err)
	}
	clock.Advance(time.Second)
	if _, err := dispatcher.DeliverDue(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if len(store.attempts) != 3 {
		t.Fatalf("the retry adds exactly one audit row, got %d", len(store.attempts))
	}
	last := store.attempts[2]
	if last.Number != 2 || last.Outcome != OutcomeDelivered {
		t.Fatalf("unexpected retry audit %#v", last)
	}
}

func TestDispatcherExhaustsAndStopsRetrying(t *testing.T) {
	desk, store, clock := seededDesk(t, 0)
	ctx := context.Background()
	if _, err := desk.Ingest(ctx, triggerAlert("k")); err != nil {
		t.Fatal(err)
	}
	notifier := &scriptedNotifier{results: []error{
		errTransport, errTransport, errTransport, errTransport, errTransport, errTransport,
	}}
	dispatcher := dispatcherFor(t, store, clock, notifier)
	for range 4 {
		if _, err := dispatcher.DeliverDue(ctx, 10); err != nil {
			t.Fatal(err)
		}
		clock.Advance(5 * time.Second)
	}
	if len(store.attempts) != 6 {
		t.Fatalf("two pages times three bounded attempts, got %d", len(store.attempts))
	}
	for _, notification := range store.notificationsFor(store.attempts[0].IncidentID) {
		if notification.State != NotificationExhausted {
			t.Fatalf("expected exhausted, got %s", notification.State)
		}
	}
	if _, err := dispatcher.DeliverDue(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if len(store.attempts) != 6 {
		t.Fatalf("an exhausted page is never retried, got %d rows", len(store.attempts))
	}
}

func TestDispatcherStopsPermanentFailuresWithoutRetrying(t *testing.T) {
	desk, store, clock := seededDesk(t, 0)
	ctx := context.Background()
	if _, err := desk.Ingest(ctx, triggerAlert("k")); err != nil {
		t.Fatal(err)
	}
	permanent := fmt.Errorf("%w: page rejected with status 404", ErrPermanent)
	notifier := &scriptedNotifier{results: []error{permanent, permanent}}
	dispatcher := dispatcherFor(t, store, clock, notifier)
	if _, err := dispatcher.DeliverDue(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if len(store.attempts) != 2 {
		t.Fatalf("expected two audited attempts, got %d", len(store.attempts))
	}
	for _, attempt := range store.attempts {
		if attempt.Outcome != OutcomeFailed || attempt.State != NotificationFailed || !attempt.NextAttemptAt.IsZero() {
			t.Fatalf("a permanent failure is terminal, got %#v", attempt)
		}
	}
	clock.Advance(time.Hour)
	if sent, err := dispatcher.DeliverDue(ctx, 10); err != nil || sent != 0 {
		t.Fatalf("a failed page is never retried: %d %v", sent, err)
	}
}

// A page whose audit write fails must not block the pages behind it, and must
// not be re-sent on the next tick: the lease taken before the send moves its due
// time forward even though the transition was never recorded.
func TestFailedAuditNeitherStarvesTheQueueNorReplaysImmediately(t *testing.T) {
	desk, store, clock := seededDesk(t, 0)
	ctx := context.Background()
	if _, err := desk.Ingest(ctx, triggerAlert("k")); err != nil {
		t.Fatal(err)
	}
	notifier := &scriptedNotifier{}
	dispatcher := dispatcherFor(t, store, clock, notifier)
	store.failRecord = errors.New("audit authority is unavailable")

	sent, err := dispatcher.DeliverDue(ctx, 10)
	if err == nil {
		t.Fatal("a failed audit write must surface an error")
	}
	if sent != 2 {
		t.Fatalf("every due page is still attempted, got %d", sent)
	}
	if len(notifier.messages) != 2 {
		t.Fatalf("the poisoned page must not block its sibling, got %d sends", len(notifier.messages))
	}
	before := len(notifier.messages)
	store.failRecord = nil
	if _, err := dispatcher.DeliverDue(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if len(notifier.messages) != before {
		t.Fatalf("the leased page must wait rather than re-send every tick, got %d", len(notifier.messages))
	}
	clock.Advance(31 * time.Second)
	if _, err := dispatcher.DeliverDue(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if len(notifier.messages) != before+2 {
		t.Fatalf("after the lease expires the page is retried, got %d", len(notifier.messages))
	}
}

// The claim is the only thing that decides who sends: a page claimed elsewhere
// is skipped without a send and without an audit row.
func TestLostClaimSkipsTheSendEntirely(t *testing.T) {
	desk, store, clock := seededDesk(t, 0)
	ctx := context.Background()
	if _, err := desk.Ingest(ctx, triggerAlert("k")); err != nil {
		t.Fatal(err)
	}
	notifier := &scriptedNotifier{}
	dispatcher := dispatcherFor(t, store, clock, notifier)
	store.failClaim = fmt.Errorf("%w: claimed elsewhere", ErrConflict)
	sent, err := dispatcher.DeliverDue(ctx, 10)
	if err != nil {
		t.Fatalf("a lost claim is benign, got %v", err)
	}
	if sent != 0 || len(notifier.messages) != 0 || len(store.attempts) != 0 {
		t.Fatalf("a lost claim must not send or audit: %d %d %d", sent, len(notifier.messages), len(store.attempts))
	}
}

func TestDispatcherRejectsIncompleteComposition(t *testing.T) {
	store := newMemoryStore()
	clock := &fakeClock{now: time.Now()}
	schedule, err := NewRetrySchedule(nil)
	if err != nil {
		t.Fatal(err)
	}
	notifier := &scriptedNotifier{}
	full := map[Channel]Notifier{ChannelWebhook: notifier, ChannelEmail: notifier}
	cases := map[string]func() (*Dispatcher, error){
		"no store": func() (*Dispatcher, error) { return NewDispatcher(nil, full, schedule, clock.Now, time.Second) },
		"no clock": func() (*Dispatcher, error) { return NewDispatcher(store, full, schedule, nil, time.Second) },
		"no email": func() (*Dispatcher, error) {
			return NewDispatcher(store, map[Channel]Notifier{ChannelWebhook: notifier}, schedule, clock.Now, time.Second)
		},
		"no webhook": func() (*Dispatcher, error) {
			return NewDispatcher(store, map[Channel]Notifier{ChannelEmail: notifier}, schedule, clock.Now, time.Second)
		},
		"zero lease":  func() (*Dispatcher, error) { return NewDispatcher(store, full, schedule, clock.Now, 0) },
		"no notifier": func() (*Dispatcher, error) { return NewDispatcher(store, nil, schedule, clock.Now, time.Second) },
	}
	for name, build := range cases {
		if _, err := build(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: expected ErrInvalid, got %v", name, err)
		}
	}
	dispatcher := dispatcherFor(t, store, clock, notifier)
	for _, limit := range []int{0, 101} {
		if _, err := dispatcher.DeliverDue(context.Background(), limit); !errors.Is(err, ErrInvalid) {
			t.Fatalf("limit %d must be rejected, got %v", limit, err)
		}
	}
}

func TestRetryScheduleRejectsUnboundedInput(t *testing.T) {
	if _, err := NewRetrySchedule([]time.Duration{0}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a zero delay must be rejected, got %v", err)
	}
	twentyOne := make([]time.Duration, 21)
	for index := range twentyOne {
		twentyOne[index] = time.Second
	}
	if _, err := NewRetrySchedule(twentyOne); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an over-long schedule must be rejected, got %v", err)
	}
	schedule, err := NewRetrySchedule([]time.Duration{time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if schedule.MaxAttempts() != 2 {
		t.Fatalf("max attempts is the immediate try plus retries, got %d", schedule.MaxAttempts())
	}
	backwards := schedule.Resolve(
		Notification{ID: "n", State: NotificationPending},
		errTransport,
		time.Unix(100, 0),
		time.Unix(50, 0),
	)
	if backwards.NextAttemptAt.Before(time.Unix(100, 0).UTC()) {
		t.Fatalf("a backwards clock must not schedule into the past, got %s", backwards.NextAttemptAt)
	}
}
