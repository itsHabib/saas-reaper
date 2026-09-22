// Package sqlite persists responders, schedules, policies, services, incidents, pages, and audit.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	// Register the pure-Go SQLite database/sql driver.
	_ "modernc.org/sqlite"
)

// Store is the durable SQLite authority for the incident specimen.
type Store struct {
	db *sql.DB
}

// Open prepares a SQLite authority at path.
func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("sqlite path is required")
	}
	if err := secureDatabaseFile(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open incident sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.prepare(context.Background()); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return store, nil
}

func secureDatabaseFile(path string) error {
	// #nosec G304 -- the customer-controlled database path is trusted process configuration.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("create private incident sqlite file: %w", err)
	}
	modeErr := file.Chmod(0o600)
	closeErr := file.Close()
	if err := errors.Join(modeErr, closeErr); err != nil {
		return fmt.Errorf("secure incident sqlite file: %w", err)
	}
	return nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) prepare(ctx context.Context) error {
	pragmas := []string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA journal_mode = WAL`,
		`PRAGMA synchronous = FULL`,
		`PRAGMA busy_timeout = 5000`,
	}
	for _, statement := range pragmas {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("prepare incident sqlite pragma: %w", err)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin incident schema: %w", err)
	}
	defer tx.Rollback()
	for _, statement := range schema {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("prepare incident sqlite schema: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit incident schema: %w", err)
	}
	return nil
}

var schema = []string{
	`CREATE TABLE IF NOT EXISTS responders (
		id TEXT PRIMARY KEY,
		email TEXT NOT NULL,
		webhook_url TEXT NOT NULL,
		webhook_secret TEXT NOT NULL,
		created_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS schedules (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		definition TEXT NOT NULL,
		created_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS escalation_policies (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		repeat INTEGER NOT NULL CHECK (repeat >= 0),
		levels TEXT NOT NULL,
		created_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS services (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		routing_key TEXT NOT NULL UNIQUE,
		policy_id TEXT NOT NULL REFERENCES escalation_policies(id),
		created_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS incidents (
		id TEXT PRIMARY KEY,
		service_id TEXT NOT NULL REFERENCES services(id),
		dedup_key TEXT NOT NULL,
		state TEXT NOT NULL CHECK (state IN ('triggered', 'acknowledged', 'resolved')),
		summary TEXT NOT NULL,
		source TEXT NOT NULL,
		severity TEXT NOT NULL,
		client TEXT NOT NULL,
		policy_id TEXT NOT NULL REFERENCES escalation_policies(id),
		level INTEGER NOT NULL CHECK (level >= 0),
		repeat INTEGER NOT NULL CHECK (repeat >= 0),
		escalate_at INTEGER,
		revision INTEGER NOT NULL CHECK (revision >= 1),
		opened_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS incidents_open_dedup
		ON incidents (service_id, dedup_key) WHERE state <> 'resolved'`,
	`CREATE INDEX IF NOT EXISTS incidents_timer
		ON incidents (state, escalate_at) WHERE escalate_at IS NOT NULL`,
	`CREATE INDEX IF NOT EXISTS incidents_service
		ON incidents (service_id, opened_at DESC)`,
	`CREATE TABLE IF NOT EXISTS incident_events (
		sequence INTEGER PRIMARY KEY AUTOINCREMENT,
		incident_id TEXT NOT NULL REFERENCES incidents(id),
		kind TEXT NOT NULL,
		actor TEXT NOT NULL,
		level INTEGER NOT NULL,
		repeat INTEGER NOT NULL,
		detail TEXT NOT NULL,
		at INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS incident_events_incident
		ON incident_events (incident_id, sequence)`,
	`CREATE TRIGGER IF NOT EXISTS incident_events_no_update
		BEFORE UPDATE ON incident_events
		BEGIN
			SELECT RAISE(ABORT, 'incident journal is append-only');
		END`,
	`CREATE TRIGGER IF NOT EXISTS incident_events_no_delete
		BEFORE DELETE ON incident_events
		BEGIN
			SELECT RAISE(ABORT, 'incident journal is append-only');
		END`,
	`CREATE TABLE IF NOT EXISTS notifications (
		id TEXT PRIMARY KEY,
		incident_id TEXT NOT NULL REFERENCES incidents(id),
		responder_id TEXT NOT NULL REFERENCES responders(id),
		channel TEXT NOT NULL CHECK (channel IN ('webhook', 'email')),
		level INTEGER NOT NULL,
		repeat INTEGER NOT NULL,
		state TEXT NOT NULL CHECK (state IN ('pending', 'delivered', 'exhausted', 'failed')),
		attempt_count INTEGER NOT NULL CHECK (attempt_count >= 0),
		next_attempt_at INTEGER,
		created_at INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS notifications_due
		ON notifications (state, next_attempt_at, created_at, id)`,
	`CREATE INDEX IF NOT EXISTS notifications_incident
		ON notifications (incident_id, created_at)`,
	`CREATE TABLE IF NOT EXISTS notification_attempts (
		sequence INTEGER PRIMARY KEY AUTOINCREMENT,
		notification_id TEXT NOT NULL REFERENCES notifications(id),
		incident_id TEXT NOT NULL REFERENCES incidents(id),
		responder_id TEXT NOT NULL REFERENCES responders(id),
		channel TEXT NOT NULL,
		number INTEGER NOT NULL CHECK (number >= 1),
		outcome TEXT NOT NULL CHECK (outcome IN ('delivered', 'retrying', 'exhausted', 'failed')),
		error_text TEXT NOT NULL,
		attempted_at INTEGER NOT NULL,
		next_attempt_at INTEGER,
		state TEXT NOT NULL CHECK (state IN ('pending', 'delivered', 'exhausted', 'failed')),
		UNIQUE (notification_id, number)
	)`,
	`CREATE INDEX IF NOT EXISTS notification_attempts_incident
		ON notification_attempts (incident_id, sequence DESC)`,
	`CREATE TRIGGER IF NOT EXISTS notification_attempts_no_update
		BEFORE UPDATE ON notification_attempts
		BEGIN
			SELECT RAISE(ABORT, 'notification attempt audit is append-only');
		END`,
	`CREATE TRIGGER IF NOT EXISTS notification_attempts_no_delete
		BEFORE DELETE ON notification_attempts
		BEGIN
			SELECT RAISE(ABORT, 'notification attempt audit is append-only');
		END`,
}

type rowScanner interface {
	Scan(...any) error
}

func nullableTime(value time.Time) sql.NullInt64 {
	if value.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: value.UTC().UnixNano(), Valid: true}
}

func timeOf(value sql.NullInt64) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return time.Unix(0, value.Int64).UTC()
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	return strings.Contains(text, "UNIQUE constraint failed") || strings.Contains(text, "constraint failed: UNIQUE")
}
