// Package sqlite persists channels, templates, recipients, notifications, delivery state, and attempt audit.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	// Register the pure-Go SQLite database/sql driver.
	_ "modernc.org/sqlite"
)

// Store is the durable SQLite authority for the notification specimen.
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
		return nil, fmt.Errorf("open notification sqlite: %w", err)
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
		return fmt.Errorf("create private notification sqlite file: %w", err)
	}
	modeErr := file.Chmod(0o600)
	closeErr := file.Close()
	if err := errors.Join(modeErr, closeErr); err != nil {
		return fmt.Errorf("secure notification sqlite file: %w", err)
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
			return fmt.Errorf("prepare notification sqlite pragma: %w", err)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin notification schema: %w", err)
	}
	defer tx.Rollback()
	for _, statement := range schema {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("prepare notification sqlite schema: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit notification schema: %w", err)
	}
	return nil
}

var schema = []string{
	`CREATE TABLE IF NOT EXISTS channels (
		id TEXT PRIMARY KEY,
		kind TEXT NOT NULL CHECK (kind IN ('smtp', 'slack-webhook')),
		enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
		revision INTEGER NOT NULL CHECK (revision >= 1),
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS templates (
		key TEXT NOT NULL,
		channel_id TEXT NOT NULL REFERENCES channels(id),
		subject TEXT NOT NULL,
		body TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		PRIMARY KEY (key, channel_id)
	)`,
	`CREATE TABLE IF NOT EXISTS recipients (
		id TEXT PRIMARY KEY,
		created_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS recipient_channels (
		recipient_id TEXT NOT NULL REFERENCES recipients(id),
		channel_id TEXT NOT NULL REFERENCES channels(id),
		position INTEGER NOT NULL CHECK (position >= 0),
		address TEXT NOT NULL,
		enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
		PRIMARY KEY (recipient_id, channel_id)
	)`,
	`CREATE TABLE IF NOT EXISTS notifications (
		id TEXT PRIMARY KEY,
		idempotency_key TEXT NOT NULL UNIQUE,
		fingerprint TEXT NOT NULL,
		template_key TEXT NOT NULL,
		recipient_id TEXT NOT NULL REFERENCES recipients(id),
		payload BLOB NOT NULL,
		actor TEXT NOT NULL,
		created_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS deliveries (
		id TEXT PRIMARY KEY,
		notification_id TEXT NOT NULL REFERENCES notifications(id),
		recipient_id TEXT NOT NULL REFERENCES recipients(id),
		channel_id TEXT NOT NULL REFERENCES channels(id),
		actor TEXT NOT NULL,
		address TEXT NOT NULL,
		subject TEXT NOT NULL,
		body TEXT NOT NULL,
		state TEXT NOT NULL CHECK (state IN ('pending', 'delivered', 'failed', 'exhausted', 'canceled')),
		attempt_count INTEGER NOT NULL CHECK (attempt_count >= 0),
		next_attempt_at INTEGER,
		created_at INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS deliveries_due
		ON deliveries (state, next_attempt_at, created_at, id)`,
	`CREATE INDEX IF NOT EXISTS deliveries_channel
		ON deliveries (channel_id, state)`,
	`CREATE INDEX IF NOT EXISTS deliveries_notification
		ON deliveries (notification_id, created_at, id)`,
	`CREATE TABLE IF NOT EXISTS delivery_attempts (
		sequence INTEGER PRIMARY KEY AUTOINCREMENT,
		delivery_id TEXT NOT NULL REFERENCES deliveries(id),
		notification_id TEXT NOT NULL REFERENCES notifications(id),
		recipient_id TEXT NOT NULL REFERENCES recipients(id),
		channel_id TEXT NOT NULL REFERENCES channels(id),
		actor TEXT NOT NULL,
		number INTEGER NOT NULL CHECK (number >= 1),
		outcome TEXT NOT NULL CHECK (outcome IN ('delivered', 'retrying', 'rejected', 'exhausted')),
		code INTEGER NOT NULL CHECK (code >= 0),
		error_text TEXT NOT NULL,
		attempted_at INTEGER NOT NULL,
		next_attempt_at INTEGER,
		state TEXT NOT NULL CHECK (state IN ('pending', 'delivered', 'failed', 'exhausted', 'canceled')),
		UNIQUE (delivery_id, number)
	)`,
	`CREATE INDEX IF NOT EXISTS attempts_notification
		ON delivery_attempts (notification_id, sequence DESC)`,
	`CREATE INDEX IF NOT EXISTS attempts_channel
		ON delivery_attempts (channel_id, sequence DESC)`,
	`CREATE TRIGGER IF NOT EXISTS delivery_attempts_no_update
		BEFORE UPDATE ON delivery_attempts
		BEGIN
			SELECT RAISE(ABORT, 'delivery attempt audit is append-only');
		END`,
	`CREATE TRIGGER IF NOT EXISTS delivery_attempts_no_delete
		BEFORE DELETE ON delivery_attempts
		BEGIN
			SELECT RAISE(ABORT, 'delivery attempt audit is append-only');
		END`,
}

type rowScanner interface {
	Scan(...any) error
}

type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func boolValue(value bool) int {
	if value {
		return 1
	}
	return 0
}
