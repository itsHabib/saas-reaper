package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/tunnel"
)

// AppendAudit records lifecycle rows that carry no durable claim change, such as an agent
// connecting or disconnecting, atomically as one batch.
func (s *Store) AppendAudit(ctx context.Context, entries []tunnel.AuditEntry) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin audit append: %w", err)
	}
	defer tx.Rollback()
	if err := insertAudit(ctx, tx, entries); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit audit append: %w", err)
	}
	return nil
}

// Audit returns the newest rows first, at most limit of them.
func (s *Store) Audit(ctx context.Context, limit int) ([]tunnel.AuditEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT sequence, at, subdomain, kind, actor, detail FROM audit ORDER BY sequence DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("read audit: %w", err)
	}
	defer rows.Close()
	entries := []tunnel.AuditEntry{}
	for rows.Next() {
		var entry tunnel.AuditEntry
		var at string
		var kind string
		if err := rows.Scan(&entry.Sequence, &at, &entry.Subdomain, &kind, &entry.Actor, &entry.Detail); err != nil {
			return nil, fmt.Errorf("scan audit: %w", err)
		}
		entry.Kind = tunnel.AuditKind(kind)
		entry.At, err = time.Parse(time.RFC3339Nano, at)
		if err != nil {
			return nil, fmt.Errorf("parse audit at: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit: %w", err)
	}
	return entries, nil
}

func insertAudit(ctx context.Context, tx *sql.Tx, entries []tunnel.AuditEntry) error {
	for _, entry := range entries {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO audit (at, subdomain, kind, actor, detail) VALUES (?, ?, ?, ?, ?)`,
			entry.At.UTC().Format(time.RFC3339Nano), entry.Subdomain, string(entry.Kind), entry.Actor, entry.Detail)
		if err != nil {
			return fmt.Errorf("append audit %s: %w", entry.Kind, err)
		}
	}
	return nil
}
