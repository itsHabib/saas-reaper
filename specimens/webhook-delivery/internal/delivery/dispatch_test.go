package delivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"
)

var errAuditUnavailable = errors.New("audit database unavailable")

type dispatchMemory struct {
	due      []Dispatch
	attempts []Attempt
	reject   func(Attempt) error
}

func (m *dispatchMemory) Due(_ context.Context, _ time.Time, limit int) ([]Dispatch, error) {
	return append([]Dispatch(nil), m.due[:min(limit, len(m.due))]...), nil
}

func (m *dispatchMemory) RecordAttempt(_ context.Context, attempt Attempt) error {
	if m.reject != nil {
		if err := m.reject(attempt); err != nil {
			return err
		}
	}
	m.attempts = append(m.attempts, attempt)
	m.due = slices.DeleteFunc(m.due, func(item Dispatch) bool { return item.DeliveryID == attempt.DeliveryID })
	return nil
}

func (m *dispatchMemory) attemptedIDs() []string {
	ids := make([]string, 0, len(m.attempts))
	for _, attempt := range m.attempts {
		ids = append(ids, attempt.DeliveryID)
	}
	return ids
}

type senderStub struct {
	result  SendResult
	err     error
	calls   int
	payload []byte
	headers Headers
}

func (s *senderStub) Send(_ context.Context, _ string, payload []byte, headers Headers) (SendResult, error) {
	s.calls++
	s.payload = append([]byte(nil), payload...)
	s.headers = headers
	return s.result, s.err
}

type cancellationSender struct {
	started chan struct{}
}

func (s *cancellationSender) Send(
	ctx context.Context,
	_ string,
	_ []byte,
	_ Headers,
) (SendResult, error) {
	close(s.started)
	<-ctx.Done()
	return SendResult{}, ctx.Err()
}

type cancellationStore struct {
	dispatchMemory
}

func (s *cancellationStore) RecordAttempt(ctx context.Context, attempt Attempt) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.dispatchMemory.RecordAttempt(ctx, attempt)
}

func testDispatch(id string) Dispatch {
	return Dispatch{
		DeliveryID:  id,
		MessageID:   "msg_" + strings.TrimPrefix(id, "del_"),
		EndpointID:  "ep_one",
		Actor:       "configured",
		Destination: "https://example.com/hook",
		Secret:      testSecret,
		Payload:     []byte(`{"ok":true}`),
	}
}

func testDispatcher(t *testing.T, store DispatchStore, sender Sender, clock Clock) *Dispatcher {
	t.Helper()
	schedule, err := NewSchedule([]time.Duration{time.Second})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := NewDispatcher(store, sender, schedule, clock, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return dispatcher
}

func TestDispatcherRecordsFailedAttemptBeforeRetry(t *testing.T) {
	store := &dispatchMemory{due: []Dispatch{testDispatch("del_one")}}
	sender := &senderStub{err: errors.New("connection refused")}
	times := []time.Time{time.Unix(50, 0), time.Unix(50, 0), time.Unix(70, 0)}
	clock := func() time.Time {
		now := times[0]
		times = times[1:]
		return now
	}
	dispatcher := testDispatcher(t, store, sender, clock)
	count, err := dispatcher.DeliverDue(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || len(store.attempts) != 1 {
		t.Fatalf("count = %d, attempts = %#v", count, store.attempts)
	}
	attempt := store.attempts[0]
	if attempt.Outcome != OutcomeRetrying || attempt.Actor != "configured" || attempt.Error == "" {
		t.Fatalf("attempt = %#v", attempt)
	}
	if !attempt.NextAttemptAt.Equal(time.Unix(71, 0)) {
		t.Fatalf("next attempt = %s, want one second after completion", attempt.NextAttemptAt)
	}
}

func TestDispatcherAuditsAnAttemptCanceledDuringShutdown(t *testing.T) {
	store := &cancellationStore{dispatchMemory: dispatchMemory{due: []Dispatch{
		testDispatch("del_shutdown"),
		testDispatch("del_never_started"),
	}}}
	schedule, err := NewSchedule(nil)
	if err != nil {
		t.Fatal(err)
	}
	sender := &cancellationSender{started: make(chan struct{})}
	dispatcher, err := NewDispatcher(
		store,
		sender,
		schedule,
		func() time.Time { return time.Unix(80, 0) },
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, deliverErr := dispatcher.DeliverDue(ctx, 2)
		result <- deliverErr
	}()
	<-sender.started
	cancel()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if len(store.attempts) != 1 || store.attempts[0].Outcome != OutcomeExhausted {
		t.Fatalf("shutdown attempts = %#v, want one exhausted audit", store.attempts)
	}
	if store.attempts[0].DeliveryID != "del_shutdown" {
		t.Fatalf("shutdown burned the retry budget of an unstarted delivery: %#v", store.attempts)
	}
}

func TestDispatcherParksAnUnauditableDeliveryAndKeepsTheQueueMoving(t *testing.T) {
	store := &dispatchMemory{
		due: []Dispatch{testDispatch("del_poisoned"), testDispatch("del_behind")},
		reject: func(attempt Attempt) error {
			if attempt.DeliveryID == "del_poisoned" {
				return errAuditUnavailable
			}
			return nil
		},
	}
	sender := &senderStub{result: SendResult{StatusCode: 204}}
	now := time.Unix(100, 0).UTC()
	dispatcher := testDispatcher(t, store, sender, func() time.Time { return now })

	count, err := dispatcher.DeliverDue(t.Context(), 2)
	if !errors.Is(err, errAuditUnavailable) || !strings.Contains(err.Error(), "del_poisoned") {
		t.Fatalf("error = %v, want the poisoned audit failure", err)
	}
	if count != 2 || !slices.Equal(store.attemptedIDs(), []string{"del_behind"}) {
		t.Fatalf("count/attempted = %d/%v, want the delivery behind the poisoned head attempted", count, store.attemptedIDs())
	}

	// The poisoned row stays first in queue order but is parked, so it is not re-sent every poll.
	count, err = dispatcher.DeliverDue(t.Context(), 2)
	if err != nil || count != 0 || sender.calls != 2 {
		t.Fatalf("parked pass = %d/%v with %d sends, want nothing attempted", count, err, sender.calls)
	}

	now = now.Add(auditFailureBackoff)
	count, err = dispatcher.DeliverDue(t.Context(), 2)
	if !errors.Is(err, errAuditUnavailable) || count != 1 || sender.calls != 3 {
		t.Fatalf("post-backoff pass = %d/%v with %d sends, want the parked delivery retried", count, err, sender.calls)
	}
}

func TestDispatcherTreatsAStoreRejectedAttemptAsALostRace(t *testing.T) {
	for _, lost := range []error{ErrConflict, ErrDisabled} {
		t.Run(lost.Error(), func(t *testing.T) {
			store := &dispatchMemory{
				due: []Dispatch{testDispatch("del_raced"), testDispatch("del_behind")},
				reject: func(attempt Attempt) error {
					if attempt.DeliveryID == "del_raced" {
						return fmt.Errorf("%w: endpoint ep_one", lost)
					}
					return nil
				},
			}
			sender := &senderStub{result: SendResult{StatusCode: 204}}
			now := time.Unix(110, 0).UTC()
			dispatcher := testDispatcher(t, store, sender, func() time.Time { return now })
			count, err := dispatcher.DeliverDue(t.Context(), 2)
			if err != nil {
				t.Fatalf("lost race surfaced as an error: %v", err)
			}
			if count != 2 || !slices.Equal(store.attemptedIDs(), []string{"del_behind"}) {
				t.Fatalf("count/attempted = %d/%v, want the raced attempt dropped silently", count, store.attemptedIDs())
			}
			if len(dispatcher.parked) != 0 {
				t.Fatalf("lost race parked a delivery: %#v", dispatcher.parked)
			}
		})
	}
}

func TestDispatcherTerminatesAnUnsignableDeliveryAfterOneAudit(t *testing.T) {
	unsignable := testDispatch("del_unsignable")
	unsignable.Secret = "whsec_not-base64"
	store := &dispatchMemory{due: []Dispatch{unsignable, testDispatch("del_behind")}}
	sender := &senderStub{result: SendResult{StatusCode: 204}}
	dispatcher := testDispatcher(t, store, sender, func() time.Time { return time.Unix(120, 0).UTC() })
	count, err := dispatcher.DeliverDue(t.Context(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || len(store.attempts) != 2 || sender.calls != 1 {
		t.Fatalf("count/attempts/sends = %d/%d/%d, want the unsignable delivery audited without a send", count, len(store.attempts), sender.calls)
	}
	failed := store.attempts[0]
	if failed.DeliveryID != "del_unsignable" || failed.Outcome != OutcomeFailed || failed.State != StateFailed {
		t.Fatalf("unsignable attempt = %#v, want a terminal failed audit", failed)
	}
	if failed.StatusCode != 0 || !failed.NextAttemptAt.IsZero() || !strings.Contains(failed.Error, "permanent") {
		t.Fatalf("unsignable attempt = %#v, want no status, no retry, and a permanent error", failed)
	}
	if store.attempts[1].Outcome != OutcomeDelivered {
		t.Fatalf("sibling attempt = %#v, want delivered", store.attempts[1])
	}
}
