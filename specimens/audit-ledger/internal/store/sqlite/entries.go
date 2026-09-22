package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/itsHabib/saas-reaper/specimens/audit-ledger/internal/ledger"
)

const entryColumns = `tenant, sequence, id, actor, action, target,
	occurred_at, recorded_at, source, metadata, previous_hash, hash`

// Append runs apply inside one immediate write transaction and commits only
// when apply returns nil.
func (s *Store) Append(ctx context.Context, apply func(ledger.AppendTransaction) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin audit append: %w", err)
	}
	defer tx.Rollback()
	if err := apply(appendTransaction{tx: tx}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit audit append: %w", err)
	}
	return nil
}

type appendTransaction struct {
	tx *sql.Tx
}

func (t appendTransaction) Head(ctx context.Context, tenant string) (ledger.Head, error) {
	return head(ctx, t.tx, tenant)
}

func (t appendTransaction) Existing(ctx context.Context, tenant, id string) (ledger.Entry, bool, error) {
	row := t.tx.QueryRowContext(ctx,
		`SELECT `+entryColumns+` FROM entries WHERE tenant = ? AND id = ?`, tenant, id)
	entry, err := scanEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.Entry{}, false, nil
	}
	if err != nil {
		return ledger.Entry{}, false, fmt.Errorf("read existing audit entry: %w", err)
	}
	return entry, true, nil
}

func (t appendTransaction) Insert(ctx context.Context, entry ledger.Entry) error {
	_, err := t.tx.ExecContext(ctx,
		`INSERT INTO entries (`+entryColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.Tenant, entry.Sequence, entry.ID, entry.Actor, entry.Action, entry.Target,
		entry.OccurredAt, entry.RecordedAt, entry.Source, string(entry.Metadata),
		entry.PreviousHash, entry.Hash)
	if err != nil {
		return fmt.Errorf("insert audit entry %s/%d: %w", entry.Tenant, entry.Sequence, err)
	}
	return nil
}

// Head returns the latest position of one tenant chain; an unknown or empty
// tenant reports sequence zero and the genesis hash.
func (s *Store) Head(ctx context.Context, tenant string) (ledger.Head, error) {
	return head(ctx, s.db, tenant)
}

type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func head(ctx context.Context, db querier, tenant string) (ledger.Head, error) {
	row := db.QueryRowContext(ctx,
		`SELECT sequence, hash FROM entries WHERE tenant = ? ORDER BY sequence DESC LIMIT 1`, tenant)
	var current ledger.Head
	err := row.Scan(&current.Sequence, &current.Hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.Head{Sequence: 0, Hash: ledger.GenesisHash}, nil
	}
	if err != nil {
		return ledger.Head{}, fmt.Errorf("read audit head for %s: %w", tenant, err)
	}
	return current, nil
}

// Entries returns up to limit entries of one tenant with sequence greater than
// after, in ascending sequence order.
func (s *Store) Entries(ctx context.Context, tenant string, after int64, limit int) ([]ledger.Entry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+entryColumns+` FROM entries
			WHERE tenant = ? AND sequence > ? ORDER BY sequence ASC LIMIT ?`,
		tenant, after, limit)
	if err != nil {
		return nil, fmt.Errorf("list audit entries for %s: %w", tenant, err)
	}
	defer rows.Close()
	entries := make([]ledger.Entry, 0, limit)
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("scan audit entry for %s: %w", tenant, err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit entries for %s: %w", tenant, err)
	}
	return entries, nil
}

// Export streams every entry of one tenant in sequence order to visit, in one
// read snapshot, stopping at the first visit error.
func (s *Store) Export(ctx context.Context, tenant string, visit func(ledger.Entry) error) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+entryColumns+` FROM entries WHERE tenant = ? ORDER BY sequence ASC`, tenant)
	if err != nil {
		return fmt.Errorf("export audit entries for %s: %w", tenant, err)
	}
	defer rows.Close()
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return fmt.Errorf("scan audit export for %s: %w", tenant, err)
		}
		if err := visit(entry); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate audit export for %s: %w", tenant, err)
	}
	return nil
}

func scanEntry(row rowScanner) (ledger.Entry, error) {
	var entry ledger.Entry
	var metadata string
	err := row.Scan(
		&entry.Tenant, &entry.Sequence, &entry.ID, &entry.Actor, &entry.Action, &entry.Target,
		&entry.OccurredAt, &entry.RecordedAt, &entry.Source, &metadata, &entry.PreviousHash, &entry.Hash,
	)
	if err != nil {
		return ledger.Entry{}, err
	}
	entry.Metadata = json.RawMessage(metadata)
	return entry, nil
}
