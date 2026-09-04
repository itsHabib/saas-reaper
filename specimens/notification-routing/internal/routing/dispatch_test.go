package routing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type dispatchMemory struct {
	due      []Dispatch
	attempts []Attempt
	failFor  map[string]error
}

func (m *dispatchMemory) Due(context.Context, time.Time, int) ([]Dispatch, error) {
	return append([]Dispatch(nil), m.due...), nil
}

func (m *dispatchMemory) RecordAttempt(_ context.Context, attempt Attempt) error {
	if err := m.failFor[attempt.DeliveryID]; err != nil {
		return err
	}
	m.attempts = append(m.attempts, attempt)
	return nil
}

type transportStub struct {
	receipt   Receipt
	err       error
	envelopes []Envelope
}

func (s *transportStub) Deliver(_ context.Context, envelope Envelope) (Receipt, error) {
	s.envelopes = append(s.envelopes, envelope)
	return s.receipt, s.err
}

type cancellationTransport struct {
	started chan struct{}
}

func (s *cancellationTransport) Deliver(ctx context.Context, _ Envelope) (Receipt, error) {
	close(s.started)
	<-ctx.Done()
	return Receipt{}, ctx.Err()
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

func testDispatch(id string, kind ChannelKind) Dispatch {
	return Dispatch{
		DeliveryID:     id,
		NotificationID: "ntf_one",
		RecipientID:    "cus_acme",
		ChannelID:      "email",
		Kind:           kind,
		Actor:          "configured",
		Address:        "billing@acme.example",
		Subject:        "Invoice inv_1",
		Body:           "Paid 4200",
	}
}

func TestDispatcherRecordsFailedAttemptBeforeRetry(t *testing.T) {
	store := &dispatchMemory{due: []Dispatch{testDispatch("del_one", KindSMTP)}}
	transport := &transportStub{receipt: Receipt{Code: 451}, err: errors.New("greylisted")}
	schedule, err := NewSchedule([]time.Duration{time.Second})
	if err != nil {
		t.Fatal(err)
	}
	times := []time.Time{time.Unix(50, 0), time.Unix(50, 0), time.Unix(70, 0)}
	clock := func() time.Time {
		now := times[0]
		times = times[1:]
		return now
	}
	dispatcher, err := NewDispatcher(store, map[ChannelKind]Transport{KindSMTP: transport}, schedule, clock)
	if err != nil {
		t.Fatal(err)
	}
	count, err := dispatcher.DeliverDue(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || len(store.attempts) != 1 {
		t.Fatalf("count = %d, attempts = %#v", count, store.attempts)
	}
	attempt := store.attempts[0]
	if attempt.Outcome != OutcomeRetrying || attempt.Actor != "configured" || attempt.Code != 451 || attempt.Error != "greylisted" {
		t.Fatalf("attempt = %#v", attempt)
	}
	if !attempt.NextAttemptAt.Equal(time.Unix(71, 0)) {
		t.Fatalf("next attempt = %s, want one second after completion", attempt.NextAttemptAt)
	}
	envelope := transport.envelopes[0]
	if envelope.Attempt != 1 || envelope.Subject != "Invoice inv_1" || envelope.Address != "billing@acme.example" {
		t.Fatalf("envelope = %#v", envelope)
	}
}

func TestDispatcherContinuesPastAFailedAuditWrite(t *testing.T) {
	store := &dispatchMemory{
		due: []Dispatch{
			testDispatch("del_poisoned", KindSMTP),
			testDispatch("del_healthy", KindSMTP),
		},
		failFor: map[string]error{"del_poisoned": errors.New("audit unavailable")},
	}
	transport := &transportStub{receipt: Receipt{Code: 250}}
	schedule, err := NewSchedule(nil)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := NewDispatcher(
		store, map[ChannelKind]Transport{KindSMTP: transport}, schedule, func() time.Time { return time.Unix(80, 0) },
	)
	if err != nil {
		t.Fatal(err)
	}
	count, err := dispatcher.DeliverDue(context.Background(), 10)
	if count != 2 || err == nil || !strings.Contains(err.Error(), "del_poisoned") {
		t.Fatalf("count = %d, err = %v; want both attempted and the poisoned audit reported", count, err)
	}
	if len(store.attempts) != 1 || store.attempts[0].DeliveryID != "del_healthy" {
		t.Fatalf("attempts = %#v, want the healthy sibling audited", store.attempts)
	}
}

func TestDispatcherRejectsUnknownTransportKindPermanently(t *testing.T) {
	store := &dispatchMemory{due: []Dispatch{testDispatch("del_orphan", KindSlackWebhook)}}
	transport := &transportStub{}
	schedule, err := NewSchedule([]time.Duration{time.Second})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := NewDispatcher(
		store, map[ChannelKind]Transport{KindSMTP: transport}, schedule, func() time.Time { return time.Unix(80, 0) },
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dispatcher.DeliverDue(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if len(transport.envelopes) != 0 {
		t.Fatalf("wrong transport received envelopes: %#v", transport.envelopes)
	}
	if len(store.attempts) != 1 || store.attempts[0].Outcome != OutcomeRejected || store.attempts[0].State != StateFailed {
		t.Fatalf("attempts = %#v, want one permanent rejection", store.attempts)
	}
}

func TestDispatcherAuditsAnAttemptCanceledDuringShutdown(t *testing.T) {
	store := &cancellationStore{dispatchMemory: dispatchMemory{due: []Dispatch{testDispatch("del_shutdown", KindSMTP)}}}
	schedule, err := NewSchedule(nil)
	if err != nil {
		t.Fatal(err)
	}
	transport := &cancellationTransport{started: make(chan struct{})}
	dispatcher, err := NewDispatcher(
		store, map[ChannelKind]Transport{KindSMTP: transport}, schedule, func() time.Time { return time.Unix(80, 0) },
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, deliverErr := dispatcher.DeliverDue(ctx, 1)
		result <- deliverErr
	}()
	<-transport.started
	cancel()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if len(store.attempts) != 1 || store.attempts[0].Outcome != OutcomeExhausted {
		t.Fatalf("shutdown attempts = %#v, want one exhausted audit", store.attempts)
	}
}

func TestNewDispatcherRejectsUnknownKindsAndBadLimits(t *testing.T) {
	schedule, err := NewSchedule(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewDispatcher(&dispatchMemory{}, map[ChannelKind]Transport{"fax": &transportStub{}}, schedule, time.Now)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown kind error = %v, want invalid", err)
	}
	dispatcher, err := NewDispatcher(&dispatchMemory{}, map[ChannelKind]Transport{KindSMTP: &transportStub{}}, schedule, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{0, 101} {
		if _, err := dispatcher.DeliverDue(context.Background(), limit); !errors.Is(err, ErrInvalid) {
			t.Fatalf("limit %d error = %v, want invalid", limit, err)
		}
	}
	if _, err := NewDispatcher(&dispatchMemory{}, nil, schedule, time.Now); err == nil {
		t.Fatal("dispatcher accepted no transports")
	}
}
