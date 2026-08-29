package delivery

import (
	"context"
	"errors"
	"testing"
	"time"
)

type dispatchMemory struct {
	due      []Dispatch
	attempts []Attempt
}

func (m *dispatchMemory) Due(context.Context, time.Time, int) ([]Dispatch, error) {
	return append([]Dispatch(nil), m.due...), nil
}

func (m *dispatchMemory) RecordAttempt(_ context.Context, attempt Attempt) error {
	m.attempts = append(m.attempts, attempt)
	if len(m.due) > 0 {
		m.due = m.due[1:]
	}
	return nil
}

type senderStub struct {
	result  SendResult
	err     error
	payload []byte
	headers Headers
}

type blockingSender struct {
	started chan struct{}
	release chan struct{}
}

func (s *blockingSender) Send(
	context.Context,
	string,
	[]byte,
	Headers,
) (SendResult, error) {
	close(s.started)
	<-s.release
	return SendResult{StatusCode: 204}, nil
}

type coordinatedStore struct {
	managementMemory
	dispatchMemory
	attemptRecorded chan struct{}
	disableEntered  chan struct{}
}

type disableFirstStore struct {
	managementMemory
	dispatchMemory
	disableEntered chan struct{}
	disableRelease chan struct{}
}

func (s *disableFirstStore) DisableEndpoint(
	ctx context.Context,
	id string,
	expectedRevision int64,
	at time.Time,
) (Endpoint, error) {
	close(s.disableEntered)
	<-s.disableRelease
	s.due = nil
	return s.managementMemory.DisableEndpoint(ctx, id, expectedRevision, at)
}

func (s *coordinatedStore) RecordAttempt(ctx context.Context, attempt Attempt) error {
	close(s.attemptRecorded)
	return s.dispatchMemory.RecordAttempt(ctx, attempt)
}

func (s *coordinatedStore) DisableEndpoint(
	ctx context.Context,
	id string,
	expectedRevision int64,
	at time.Time,
) (Endpoint, error) {
	close(s.disableEntered)
	select {
	case <-s.attemptRecorded:
	default:
		return Endpoint{}, errors.New("disable overtook the active attempt audit")
	}
	s.due = nil
	return s.managementMemory.DisableEndpoint(ctx, id, expectedRevision, at)
}

func (s *senderStub) Send(_ context.Context, _ string, payload []byte, headers Headers) (SendResult, error) {
	s.payload = append([]byte(nil), payload...)
	s.headers = headers
	return s.result, s.err
}

func TestDispatcherRecordsFailedAttemptBeforeRetry(t *testing.T) {
	store := &dispatchMemory{due: []Dispatch{{
		DeliveryID:  "del_one",
		MessageID:   "msg_one",
		EndpointID:  "ep_one",
		Actor:       "configured",
		Destination: "https://example.com/hook",
		Secret:      testSecret,
		Payload:     []byte(`{"ok":true}`),
	}}}
	sender := &senderStub{err: errors.New("connection refused")}
	schedule, err := NewSchedule([]time.Duration{time.Second})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := NewDispatcher(
		store,
		sender,
		schedule,
		func() time.Time { return time.Unix(50, 0) },
		NewAttemptCoordinator(),
	)
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
	if attempt.Outcome != OutcomeRetrying || attempt.Actor != "configured" || attempt.Error == "" {
		t.Fatalf("attempt = %#v", attempt)
	}
}

func TestDisableWaitsForActiveSendAndAudit(t *testing.T) {
	at := time.Unix(60, 0).UTC()
	store := &coordinatedStore{
		managementMemory: managementMemory{endpoints: []Endpoint{{
			ID: "ep_one", Enabled: true, Revision: 1,
		}}},
		dispatchMemory: dispatchMemory{due: []Dispatch{{
			DeliveryID: "del_one", MessageID: "msg_one", EndpointID: "ep_one",
			Actor: "configured", Destination: "https://example.com/hook",
			Secret: testSecret, Payload: []byte(`{"ok":true}`),
		}}},
		attemptRecorded: make(chan struct{}),
		disableEntered:  make(chan struct{}),
	}
	schedule, err := NewSchedule(nil)
	if err != nil {
		t.Fatal(err)
	}
	coordination := NewAttemptCoordinator()
	sender := &blockingSender{started: make(chan struct{}), release: make(chan struct{})}
	dispatcher, err := NewDispatcher(store, sender, schedule, func() time.Time { return at }, coordination)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(
		store,
		"configured",
		func() time.Time { return at },
		func(prefix string) (string, error) { return prefix + "unused", nil },
		func() (string, error) { return testSecret, nil },
		coordination,
	)
	if err != nil {
		t.Fatal(err)
	}
	deliveryResult := make(chan error, 1)
	go func() {
		_, deliverErr := dispatcher.DeliverDue(t.Context(), 1)
		deliveryResult <- deliverErr
	}()
	<-sender.started
	disableStarted := make(chan struct{})
	disableResult := make(chan error, 1)
	go func() {
		close(disableStarted)
		_, disableErr := service.DisableEndpoint(t.Context(), "ep_one", 1)
		disableResult <- disableErr
	}()
	<-disableStarted
	select {
	case <-store.disableEntered:
		t.Fatal("disable entered persistence before the active attempt completed")
	case <-time.After(25 * time.Millisecond):
	}
	close(sender.release)
	if err := <-deliveryResult; err != nil {
		t.Fatal(err)
	}
	if err := <-disableResult; err != nil {
		t.Fatal(err)
	}
	count, err := dispatcher.DeliverDue(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("post-disable delivery count = %d, want 0", count)
	}
}

func TestSendRechecksDueWorkAfterConcurrentDisable(t *testing.T) {
	at := time.Unix(70, 0).UTC()
	store := &disableFirstStore{
		managementMemory: managementMemory{endpoints: []Endpoint{{
			ID: "ep_one", Enabled: true, Revision: 1,
		}}},
		dispatchMemory: dispatchMemory{due: []Dispatch{{
			DeliveryID: "del_one", MessageID: "msg_one", EndpointID: "ep_one",
			Actor: "configured", Destination: "https://example.com/hook",
			Secret: testSecret, Payload: []byte(`{"ok":true}`),
		}}},
		disableEntered: make(chan struct{}),
		disableRelease: make(chan struct{}),
	}
	coordination := NewAttemptCoordinator()
	service, err := NewService(
		store,
		"configured",
		func() time.Time { return at },
		func(prefix string) (string, error) { return prefix + "unused", nil },
		func() (string, error) { return testSecret, nil },
		coordination,
	)
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := NewSchedule(nil)
	if err != nil {
		t.Fatal(err)
	}
	sender := &senderStub{}
	dispatcher, err := NewDispatcher(store, sender, schedule, func() time.Time { return at }, coordination)
	if err != nil {
		t.Fatal(err)
	}
	disableResult := make(chan error, 1)
	go func() {
		_, disableErr := service.DisableEndpoint(t.Context(), "ep_one", 1)
		disableResult <- disableErr
	}()
	<-store.disableEntered
	deliveryResult := make(chan struct {
		count int
		err   error
	}, 1)
	go func() {
		count, deliverErr := dispatcher.DeliverDue(t.Context(), 1)
		deliveryResult <- struct {
			count int
			err   error
		}{count: count, err: deliverErr}
	}()
	select {
	case result := <-deliveryResult:
		t.Fatalf("delivery completed before disable commit: count=%d err=%v", result.count, result.err)
	case <-time.After(25 * time.Millisecond):
	}
	close(store.disableRelease)
	if err := <-disableResult; err != nil {
		t.Fatal(err)
	}
	result := <-deliveryResult
	if result.err != nil {
		t.Fatal(result.err)
	}
	if result.count != 0 || len(sender.payload) != 0 {
		t.Fatalf("post-disable result count/payload = %d/%q, want 0/empty", result.count, sender.payload)
	}
}
