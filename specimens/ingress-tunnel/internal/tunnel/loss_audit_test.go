package tunnel

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLostKeepsOwnershipUntilAuditCommits(t *testing.T) {
	store := newMemoryStore()
	service, registry := newTestService(t, store)
	issued, err := service.Claim(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Attach(context.Background(), issued.Claim, &fakeLink{}); err != nil {
		t.Fatal(err)
	}
	connection, err := service.Attach(context.Background(), issued.Claim, &fakeLink{})
	if err != nil {
		t.Fatal(err)
	}
	before := len(store.audit)
	store.auditErr = errors.New("disk full")
	if err := connection.Lost(context.Background()); !errors.Is(err, store.auditErr) {
		t.Fatalf("loss = %v", err)
	}
	if registry.Presence("acme") != PresenceLive || len(store.audit) != before {
		t.Fatal("failed commit changed presence or audit")
	}
	if _, ok := service.superseded["acme"]; !ok {
		t.Fatal("failed commit removed cooldown")
	}
	store.auditErr = nil
	if err := connection.Lost(context.Background()); err != nil {
		t.Fatal(err)
	}
	if registry.Presence("acme") != PresenceAbsent || len(store.audit) != before+1 || store.audit[before].Kind != AuditDisconnected {
		t.Fatal("retry did not commit exactly one disconnect and detach")
	}
	if err := connection.Lost(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.audit) != before+1 {
		t.Fatal("repeat loss duplicated audit")
	}
}

type pausedRevokeStore struct {
	*memoryStore
	committed chan struct{}
	resume    chan struct{}
}

func (s *pausedRevokeStore) RevokeClaim(ctx context.Context, name string, revision int, at time.Time, entries []AuditEntry) (Claim, error) {
	claim, err := s.memoryStore.RevokeClaim(ctx, name, revision, at, entries)
	close(s.committed)
	<-s.resume
	return claim, err
}

func TestTunnelsWaitsForCommittedRevokeToEvict(t *testing.T) {
	store := newMemoryStore()
	service, _ := newTestService(t, store)
	issued, err := service.Claim(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Attach(context.Background(), issued.Claim, &fakeLink{}); err != nil {
		t.Fatal(err)
	}
	paused := &pausedRevokeStore{memoryStore: store, committed: make(chan struct{}), resume: make(chan struct{})}
	service.store = paused
	revoked := make(chan error, 1)
	go func() { _, err := service.Revoke(context.Background(), "acme", 1); revoked <- err }()
	<-paused.committed
	views := make(chan []View, 1)
	go func() {
		listed, err := service.Tunnels(context.Background())
		if err != nil {
			t.Error(err)
		}
		views <- listed
	}()
	select {
	case view := <-views:
		t.Fatalf("read escaped mid-transition: %+v", view)
	case <-time.After(50 * time.Millisecond):
	}
	close(paused.resume)
	if err := <-revoked; err != nil {
		t.Fatal(err)
	}
	view := <-views
	if len(view) != 1 || view[0].State != ClaimRevoked || view[0].Presence != PresenceAbsent {
		t.Fatalf("view = %+v", view)
	}
}
