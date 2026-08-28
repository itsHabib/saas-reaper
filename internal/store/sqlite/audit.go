package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/itsHabib/saas-reaper/internal/flags"
)

// Audit returns the most recent durable publication entries first.
func (s *Store) Audit(ctx context.Context, limit int) ([]flags.AuditEntry, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT sequence, environment, key, revision, actor, action, occurred_at
		 FROM audit ORDER BY sequence DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query audit: %w", err)
	}
	defer rows.Close()
	entries := make([]flags.AuditEntry, 0, limit)
	for rows.Next() {
		entry, err := scanAudit(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit: %w", err)
	}
	return entries, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanAudit(row rowScanner) (flags.AuditEntry, error) {
	var entry flags.AuditEntry
	var occurredAt string
	if err := row.Scan(
		&entry.Sequence,
		&entry.Environment,
		&entry.Key,
		&entry.Revision,
		&entry.Actor,
		&entry.Action,
		&occurredAt,
	); err != nil {
		return flags.AuditEntry{}, fmt.Errorf("scan audit: %w", err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, occurredAt)
	if err != nil {
		return flags.AuditEntry{}, fmt.Errorf("parse audit time: %w", err)
	}
	entry.OccurredAt = parsed
	return entry, nil
}
