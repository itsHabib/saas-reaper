package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/tunnel"
)

const claimColumns = `subdomain, token_hash, revision, revoked, created_at, revoked_at`

// InsertClaim stores a new claim and its audit row in one transaction. A taken subdomain is a
// conflict; the unique token hash can only collide if the randomness did.
func (s *Store) InsertClaim(ctx context.Context, claim tunnel.Claim, entry tunnel.AuditEntry) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin claim insert: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO claims (subdomain, token_hash, revision, revoked, created_at, revoked_at)
		 VALUES (?, ?, ?, 0, ?, NULL)`,
		claim.Subdomain, claim.TokenHash, claim.Revision, claim.CreatedAt.UTC().Format(timeLayout))
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: subdomain %q is already claimed", tunnel.ErrConflict, claim.Subdomain)
	}
	if err != nil {
		return fmt.Errorf("insert claim: %w", err)
	}
	if err := insertAudit(ctx, tx, []tunnel.AuditEntry{entry}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit claim insert: %w", err)
	}
	return nil
}

// ClaimByTokenHash resolves a credential hash to its claim, revoked or not.
func (s *Store) ClaimByTokenHash(ctx context.Context, hash string) (tunnel.Claim, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+claimColumns+` FROM claims WHERE token_hash = ?`, hash)
	return scanClaim(row)
}

// ClaimBySubdomain resolves a subdomain to its claim, revoked or not.
func (s *Store) ClaimBySubdomain(ctx context.Context, subdomain string) (tunnel.Claim, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+claimColumns+` FROM claims WHERE subdomain = ?`, subdomain)
	return scanClaim(row)
}

// RevokeClaim advances the claim to revoked at the expected revision and appends the audit rows
// in the same transaction. A revision mismatch, including a concurrent revoke, is a conflict.
func (s *Store) RevokeClaim(ctx context.Context, subdomain string, expectedRevision int, at time.Time, entries []tunnel.AuditEntry) (tunnel.Claim, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return tunnel.Claim{}, fmt.Errorf("begin claim revoke: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx,
		`UPDATE claims SET revoked = 1, revision = revision + 1, revoked_at = ?
		 WHERE subdomain = ? AND revision = ? AND revoked = 0`,
		at.UTC().Format(timeLayout), subdomain, expectedRevision)
	if err != nil {
		return tunnel.Claim{}, fmt.Errorf("revoke claim: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return tunnel.Claim{}, fmt.Errorf("revoke claim rows: %w", err)
	}
	if changed != 1 {
		return tunnel.Claim{}, fmt.Errorf("%w: claim %q changed underneath the revoke", tunnel.ErrConflict, subdomain)
	}
	if err := insertAudit(ctx, tx, entries); err != nil {
		return tunnel.Claim{}, err
	}
	claim, err := scanClaim(tx.QueryRowContext(ctx, `SELECT `+claimColumns+` FROM claims WHERE subdomain = ?`, subdomain))
	if err != nil {
		return tunnel.Claim{}, err
	}
	if err := tx.Commit(); err != nil {
		return tunnel.Claim{}, fmt.Errorf("commit claim revoke: %w", err)
	}
	return claim, nil
}

// ListClaims returns every claim in creation order.
func (s *Store) ListClaims(ctx context.Context) ([]tunnel.Claim, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+claimColumns+` FROM claims ORDER BY created_at, subdomain`)
	if err != nil {
		return nil, fmt.Errorf("list claims: %w", err)
	}
	defer rows.Close()
	var claims []tunnel.Claim
	for rows.Next() {
		claim, err := scanClaim(rows)
		if err != nil {
			return nil, err
		}
		claims = append(claims, claim)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate claims: %w", err)
	}
	return claims, nil
}

type scanner interface {
	Scan(...any) error
}

func scanClaim(row scanner) (tunnel.Claim, error) {
	var claim tunnel.Claim
	var revoked int
	var createdAt string
	var revokedAt sql.NullString
	err := row.Scan(&claim.Subdomain, &claim.TokenHash, &claim.Revision, &revoked, &createdAt, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return tunnel.Claim{}, fmt.Errorf("%w: claim", tunnel.ErrNotFound)
	}
	if err != nil {
		return tunnel.Claim{}, fmt.Errorf("scan claim: %w", err)
	}
	claim.Revoked = revoked == 1
	claim.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return tunnel.Claim{}, fmt.Errorf("parse claim created_at: %w", err)
	}
	if !revokedAt.Valid {
		return claim, nil
	}
	claim.RevokedAt, err = time.Parse(time.RFC3339Nano, revokedAt.String)
	if err != nil {
		return tunnel.Claim{}, fmt.Errorf("parse claim revoked_at: %w", err)
	}
	return claim, nil
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
