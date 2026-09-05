package tunnel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"
)

// memoryStore is the smallest faithful Store: it enforces the same uniqueness and revision
// rules the SQLite mechanism does so policy tests exercise real refusals.
type memoryStore struct {
	claims   map[string]Claim
	audit    []AuditEntry
	auditErr error
}

func newMemoryStore() *memoryStore {
	return &memoryStore{claims: map[string]Claim{}}
}

func (m *memoryStore) InsertClaim(_ context.Context, claim Claim, entry AuditEntry) error {
	if _, taken := m.claims[claim.Subdomain]; taken {
		return fmt.Errorf("%w: taken", ErrConflict)
	}
	m.claims[claim.Subdomain] = claim
	m.append(entry)
	return nil
}

func (m *memoryStore) ClaimByTokenHash(_ context.Context, hash string) (Claim, error) {
	for _, claim := range m.claims {
		if claim.TokenHash == hash {
			return claim, nil
		}
	}
	return Claim{}, fmt.Errorf("%w: claim", ErrNotFound)
}

func (m *memoryStore) ClaimBySubdomain(_ context.Context, subdomain string) (Claim, error) {
	claim, ok := m.claims[subdomain]
	if !ok {
		return Claim{}, fmt.Errorf("%w: claim", ErrNotFound)
	}
	return claim, nil
}

func (m *memoryStore) RevokeClaim(_ context.Context, subdomain string, expectedRevision int, at time.Time, entries []AuditEntry) (Claim, error) {
	claim, ok := m.claims[subdomain]
	if !ok || claim.Revision != expectedRevision || claim.Revoked {
		return Claim{}, fmt.Errorf("%w: revoke", ErrConflict)
	}
	claim.Revoked = true
	claim.Revision++
	claim.RevokedAt = at
	m.claims[subdomain] = claim
	for _, entry := range entries {
		m.append(entry)
	}
	return claim, nil
}

func (m *memoryStore) AppendAudit(_ context.Context, entries []AuditEntry) error {
	if m.auditErr != nil {
		return m.auditErr
	}
	for _, entry := range entries {
		m.append(entry)
	}
	return nil
}

func (m *memoryStore) ListClaims(context.Context) ([]Claim, error) {
	claims := make([]Claim, 0, len(m.claims))
	for _, claim := range m.claims {
		claims = append(claims, claim)
	}
	return claims, nil
}

func (m *memoryStore) append(entry AuditEntry) {
	entry.Sequence = int64(len(m.audit) + 1)
	m.audit = append(m.audit, entry)
}

func (m *memoryStore) kinds() []AuditKind {
	kinds := make([]AuditKind, 0, len(m.audit))
	for _, entry := range m.audit {
		kinds = append(kinds, entry.Kind)
	}
	return kinds
}

func newTestService(t *testing.T, store *memoryStore) (*Service, *Registry) {
	t.Helper()
	registry := NewRegistry()
	clock := func() time.Time { return time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC) }
	random := bytes.NewReader(bytes.Repeat([]byte{42}, 4096))
	service, err := NewService(store, registry, "operator", clock, random, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return service, registry
}

func sameKinds(got, want []AuditKind) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func TestNewServiceRequiresEveryCollaborator(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	if _, err := NewService(nil, NewRegistry(), "operator", time.Now, nil, logger); err == nil {
		t.Fatal("nil store accepted")
	}
	if _, err := NewService(newMemoryStore(), NewRegistry(), " ", time.Now, nil, logger); err == nil {
		t.Fatal("blank actor accepted")
	}
}

func TestClaimIssuesAOneTimeTokenAndAuditsIt(t *testing.T) {
	store := newMemoryStore()
	service, _ := newTestService(t, store)
	issued, err := service.Claim(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	if issued.Token == "" || issued.Claim.TokenHash != HashToken(issued.Token) {
		t.Fatal("issued token does not match the stored hash")
	}
	if issued.Claim.Revision != 1 || issued.Claim.Revoked {
		t.Fatalf("fresh claim = %+v", issued.Claim)
	}
	if !sameKinds(store.kinds(), []AuditKind{AuditClaimed}) || store.audit[0].Actor != "operator" {
		t.Fatalf("audit after claim = %+v", store.audit)
	}
	if _, err := service.Claim(context.Background(), "acme"); !errors.Is(err, ErrConflict) {
		t.Fatalf("second claim of the same subdomain = %v", err)
	}
	if _, err := service.Claim(context.Background(), "Bad.Name"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid subdomain = %v", err)
	}
}

func TestAuthenticateRefusesUnknownMalformedAndRevoked(t *testing.T) {
	store := newMemoryStore()
	service, _ := newTestService(t, store)
	issued, err := service.Claim(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(context.Background(), "garbage"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("malformed token = %v", err)
	}
	unknown, err := NewToken(bytes.NewReader(bytes.Repeat([]byte{9}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(context.Background(), unknown); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unknown token = %v", err)
	}
	claim, err := service.Authenticate(context.Background(), issued.Token)
	if err != nil || claim.Subdomain != "acme" {
		t.Fatalf("valid token = %+v, %v", claim, err)
	}
	if _, err := service.Revoke(context.Background(), "acme", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(context.Background(), issued.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked token = %v", err)
	}
}

func TestAttachSupersedesAndIgnoresTheOldLoss(t *testing.T) {
	store := newMemoryStore()
	service, registry := newTestService(t, store)
	issued, err := service.Claim(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	first := &fakeLink{name: "first"}
	firstConnection, err := service.Attach(context.Background(), issued.Claim, first)
	if err != nil {
		t.Fatal(err)
	}
	second := &fakeLink{name: "second"}
	secondConnection, err := service.Attach(context.Background(), issued.Claim, second)
	if err != nil {
		t.Fatal(err)
	}
	if first.closed != 1 || first.reason != CloseSuperseded || second.closed != 0 {
		t.Fatalf("supersede closed first=%d(%s) second=%d", first.closed, first.reason, second.closed)
	}
	if link, _ := registry.Lookup("acme"); link != second {
		t.Fatal("second link is not routable")
	}
	if err := firstConnection.Lost(context.Background()); err != nil {
		t.Fatal(err)
	}
	if registry.Presence("acme") != PresenceLive {
		t.Fatal("the superseded link's loss detached its successor")
	}
	if err := secondConnection.Lost(context.Background()); err != nil {
		t.Fatal(err)
	}
	if registry.Presence("acme") != PresenceAbsent {
		t.Fatal("the live link's loss left it attached")
	}
	want := []AuditKind{AuditClaimed, AuditConnected, AuditSuperseded, AuditConnected, AuditDisconnected}
	if !sameKinds(store.kinds(), want) {
		t.Fatalf("audit = %v, want %v", store.kinds(), want)
	}
	if store.audit[1].Actor != AgentActor("acme") {
		t.Fatalf("connection audit actor = %q", store.audit[1].Actor)
	}
}

func TestAttachRollsBackWhenTheAuditCannotBeWritten(t *testing.T) {
	store := newMemoryStore()
	service, registry := newTestService(t, store)
	issued, err := service.Claim(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	store.auditErr = errors.New("disk full")
	if _, err := service.Attach(context.Background(), issued.Claim, &fakeLink{}); err == nil {
		t.Fatal("attach succeeded without an audit row")
	}
	if registry.Presence("acme") != PresenceAbsent {
		t.Fatal("an unaudited link stayed routable")
	}
}

func TestAFailedAuditNeverEvictsTheIncumbent(t *testing.T) {
	store := newMemoryStore()
	service, registry := newTestService(t, store)
	issued, err := service.Claim(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	incumbent := &fakeLink{name: "incumbent"}
	if _, err := service.Attach(context.Background(), issued.Claim, incumbent); err != nil {
		t.Fatal(err)
	}
	store.auditErr = errors.New("disk full")
	if _, err := service.Attach(context.Background(), issued.Claim, &fakeLink{name: "challenger"}); err == nil {
		t.Fatal("a second attach succeeded without an audit row")
	}
	if incumbent.closed != 0 {
		t.Fatal("the incumbent was superseded by an attach whose audit never committed")
	}
	if link, _ := registry.Lookup("acme"); link != incumbent {
		t.Fatal("the incumbent is no longer routable")
	}
}

func TestAttachRefusesRevokedAndReissuedClaims(t *testing.T) {
	store := newMemoryStore()
	service, _ := newTestService(t, store)
	issued, err := service.Claim(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	stale := issued.Claim
	stale.TokenHash = "someone-other"
	if _, err := service.Attach(context.Background(), stale, &fakeLink{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("reissued claim = %v", err)
	}
	if _, err := service.Revoke(context.Background(), "acme", 1); err != nil {
		t.Fatal(err)
	}
	link := &fakeLink{}
	if _, err := service.Attach(context.Background(), issued.Claim, link); !errors.Is(err, ErrRevoked) {
		t.Fatalf("attach after revoke = %v", err)
	}
	if _, err := service.Attach(context.Background(), issued.Claim, nil); err == nil {
		t.Fatal("nil link accepted")
	}
}

func TestRevokeClosesTheLiveLinkAndAuditsBoth(t *testing.T) {
	store := newMemoryStore()
	service, registry := newTestService(t, store)
	issued, err := service.Claim(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	link := &fakeLink{}
	connection, err := service.Attach(context.Background(), issued.Claim, link)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Revoke(context.Background(), "acme", 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision = %v", err)
	}
	revoked, err := service.Revoke(context.Background(), "acme", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !revoked.Revoked || revoked.Revision != 2 || revoked.RevokedAt.IsZero() {
		t.Fatalf("revoked claim = %+v", revoked)
	}
	if link.closed != 1 || link.reason != CloseRevoked || registry.Presence("acme") != PresenceAbsent {
		t.Fatalf("revoke closed=%d(%s) presence=%s", link.closed, link.reason, registry.Presence("acme"))
	}
	want := []AuditKind{AuditClaimed, AuditConnected, AuditDisconnected, AuditRevoked}
	if !sameKinds(store.kinds(), want) {
		t.Fatalf("audit = %v, want %v", store.kinds(), want)
	}
	if err := connection.Lost(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !sameKinds(store.kinds(), want) {
		t.Fatal("the evicted link's loss produced a second disconnect row")
	}
}

func TestRevokeRefusesRepeatsAndUnknownClaims(t *testing.T) {
	store := newMemoryStore()
	service, _ := newTestService(t, store)
	if _, err := service.Claim(context.Background(), "acme"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Revoke(context.Background(), "acme", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Revoke(context.Background(), "acme", 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("second revoke = %v", err)
	}
	if _, err := service.Revoke(context.Background(), "ghost", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoke unknown = %v", err)
	}
}

func TestTunnelsJoinsClaimsWithPresenceAndNoSecrets(t *testing.T) {
	store := newMemoryStore()
	service, _ := newTestService(t, store)
	acme, err := service.Claim(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Claim(context.Background(), "umbrella"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Attach(context.Background(), acme.Claim, &fakeLink{}); err != nil {
		t.Fatal(err)
	}
	views, err := service.Tunnels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	presence := map[string]Presence{}
	for _, view := range views {
		presence[view.Subdomain] = view.Presence
	}
	if presence["acme"] != PresenceLive || presence["umbrella"] != PresenceAbsent {
		t.Fatalf("presence = %v", presence)
	}
}
