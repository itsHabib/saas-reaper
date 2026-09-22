package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/tunnel"
)

func openTest(t *testing.T, path string) *Store {
	t.Helper()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func claimAt(subdomain, hash string, at time.Time) tunnel.Claim {
	return tunnel.Claim{Subdomain: subdomain, TokenHash: hash, Revision: 1, CreatedAt: at}
}

func entry(subdomain string, kind tunnel.AuditKind, at time.Time) tunnel.AuditEntry {
	return tunnel.AuditEntry{At: at, Subdomain: subdomain, Kind: kind, Actor: "test", Detail: "d"}
}

func TestOpenRejectsBlankPath(t *testing.T) {
	if _, err := Open(" "); err == nil {
		t.Fatal("blank path accepted")
	}
}

func TestInsertClaimIsAtomicWithItsAudit(t *testing.T) {
	store := openTest(t, filepath.Join(t.TempDir(), "tunnel.db"))
	ctx := context.Background()
	at := time.Date(2026, 9, 4, 1, 2, 3, 4000, time.UTC)
	if err := store.InsertClaim(ctx, claimAt("acme", "hash-a", at), entry("acme", tunnel.AuditClaimed, at)); err != nil {
		t.Fatal(err)
	}
	err := store.InsertClaim(ctx, claimAt("acme", "hash-b", at), entry("acme", tunnel.AuditClaimed, at))
	if !errors.Is(err, tunnel.ErrConflict) {
		t.Fatalf("duplicate subdomain = %v", err)
	}
	rows, err := store.Audit(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Kind != tunnel.AuditClaimed || rows[0].Sequence != 1 {
		t.Fatalf("audit after a refused insert = %+v", rows)
	}
	claim, err := store.ClaimByTokenHash(ctx, "hash-a")
	if err != nil || claim.Subdomain != "acme" || !claim.CreatedAt.Equal(at) {
		t.Fatalf("claim by hash = %+v, %v", claim, err)
	}
	if _, err := store.ClaimByTokenHash(ctx, "hash-b"); !errors.Is(err, tunnel.ErrNotFound) {
		t.Fatalf("losing hash = %v", err)
	}
	if _, err := store.ClaimBySubdomain(ctx, "ghost"); !errors.Is(err, tunnel.ErrNotFound) {
		t.Fatalf("unknown subdomain = %v", err)
	}
}

func TestRevokeClaimAdvancesRevisionAtomicallyWithAudit(t *testing.T) {
	store := openTest(t, filepath.Join(t.TempDir(), "tunnel.db"))
	ctx := context.Background()
	at := time.Date(2026, 9, 4, 1, 0, 0, 0, time.UTC)
	if err := store.InsertClaim(ctx, claimAt("acme", "hash-a", at), entry("acme", tunnel.AuditClaimed, at)); err != nil {
		t.Fatal(err)
	}
	later := at.Add(time.Minute)
	_, err := store.RevokeClaim(ctx, "acme", 7, later, []tunnel.AuditEntry{entry("acme", tunnel.AuditRevoked, later)})
	if !errors.Is(err, tunnel.ErrConflict) {
		t.Fatalf("stale revision = %v", err)
	}
	rows, _ := store.Audit(ctx, 10)
	if len(rows) != 1 {
		t.Fatalf("refused revoke left audit rows: %+v", rows)
	}
	revoked, err := store.RevokeClaim(ctx, "acme", 1, later, []tunnel.AuditEntry{
		entry("acme", tunnel.AuditDisconnected, later),
		entry("acme", tunnel.AuditRevoked, later),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !revoked.Revoked || revoked.Revision != 2 || !revoked.RevokedAt.Equal(later) {
		t.Fatalf("revoked = %+v", revoked)
	}
	if _, err := store.RevokeClaim(ctx, "acme", 2, later, nil); !errors.Is(err, tunnel.ErrConflict) {
		t.Fatalf("second revoke = %v", err)
	}
	assertRevokeAudit(t, store)
}

func assertRevokeAudit(t *testing.T, store *Store) {
	t.Helper()
	rows, err := store.Audit(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].Kind != tunnel.AuditRevoked || rows[1].Kind != tunnel.AuditDisconnected || rows[2].Kind != tunnel.AuditClaimed {
		t.Fatalf("audit newest-first = %+v", rows)
	}
	if rows[0].Sequence != 3 || rows[2].Sequence != 1 {
		t.Fatalf("audit sequences = %d..%d", rows[2].Sequence, rows[0].Sequence)
	}
}

func TestListClaimsOrdersSubSecondCreationsChronologically(t *testing.T) {
	store := openTest(t, filepath.Join(t.TempDir(), "tunnel.db"))
	ctx := context.Background()
	base := time.Date(2026, 9, 4, 10, 0, 8, 0, time.UTC)
	// RFC3339Nano would render these as 08Z, 08.2Z, and 08.12Z, whose byte order is not their
	// time order; the fixed-width layout keeps them chronological.
	creations := []struct {
		subdomain string
		at        time.Time
	}{
		{"zulu", base},
		{"yankee", base.Add(120 * time.Millisecond)},
		{"xray", base.Add(200 * time.Millisecond)},
	}
	for _, creation := range creations {
		if err := store.InsertClaim(ctx, claimAt(creation.subdomain, "hash-"+creation.subdomain, creation.at), entry(creation.subdomain, tunnel.AuditClaimed, creation.at)); err != nil {
			t.Fatal(err)
		}
	}
	claims, err := store.ListClaims(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 3 || claims[0].Subdomain != "zulu" || claims[1].Subdomain != "yankee" || claims[2].Subdomain != "xray" {
		t.Fatalf("claims are not in creation order: %+v", claims)
	}
}

func TestClaimsAndAuditSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel.db")
	ctx := context.Background()
	at := time.Date(2026, 9, 4, 1, 0, 0, 0, time.UTC)
	first := openTest(t, path)
	if err := first.InsertClaim(ctx, claimAt("acme", "hash-a", at), entry("acme", tunnel.AuditClaimed, at)); err != nil {
		t.Fatal(err)
	}
	if err := first.InsertClaim(ctx, claimAt("umbrella", "hash-u", at.Add(time.Second)), entry("umbrella", tunnel.AuditClaimed, at)); err != nil {
		t.Fatal(err)
	}
	if err := first.AppendAudit(ctx, []tunnel.AuditEntry{entry("acme", tunnel.AuditConnected, at)}); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := openTest(t, path)
	claims, err := second.ListClaims(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 2 || claims[0].Subdomain != "acme" || claims[1].Subdomain != "umbrella" {
		t.Fatalf("claims after reopen = %+v", claims)
	}
	rows, err := second.Audit(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Kind != tunnel.AuditConnected || rows[0].Sequence != 3 {
		t.Fatalf("audit after reopen (limit 2) = %+v", rows)
	}
}
