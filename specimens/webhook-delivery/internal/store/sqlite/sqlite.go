// Package sqlite persists webhook endpoints, messages, delivery state, and attempt audit.
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

// Store is the durable SQLite authority for the webhook specimen.
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
		return nil, fmt.Errorf("open webhook sqlite: %w", err)
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
		return fmt.Errorf("create private webhook sqlite file: %w", err)
	}
	modeErr := file.Chmod(0o600)
	closeErr := file.Close()
	if err := errors.Join(modeErr, closeErr); err != nil {
		return fmt.Errorf("secure webhook sqlite file: %w", err)
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
			return fmt.Errorf("prepare webhook sqlite pragma: %w", err)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin webhook schema: %w", err)
	}
	defer tx.Rollback()
	statements := []string{
		`CREATE TABLE IF NOT EXISTS endpoints (
			id TEXT PRIMARY KEY,
			url TEXT NOT NULL,
			secret TEXT NOT NULL,
			enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
			revision INTEGER NOT NULL CHECK (revision >= 1),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			payload BLOB NOT NULL,
			actor TEXT NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS deliveries (
			id TEXT PRIMARY KEY,
			message_id TEXT NOT NULL REFERENCES messages(id),
			endpoint_id TEXT NOT NULL REFERENCES endpoints(id),
			actor TEXT NOT NULL,
			kind TEXT NOT NULL CHECK (kind IN ('original', 'replay')),
			state TEXT NOT NULL CHECK (state IN ('pending', 'succeeded', 'exhausted', 'disabled')),
			attempt_count INTEGER NOT NULL CHECK (attempt_count >= 0),
			next_attempt_at INTEGER,
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS deliveries_due
			ON deliveries (state, next_attempt_at, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS deliveries_endpoint
			ON deliveries (endpoint_id, state)`,
		`CREATE TABLE IF NOT EXISTS delivery_attempts (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			delivery_id TEXT NOT NULL REFERENCES deliveries(id),
			message_id TEXT NOT NULL REFERENCES messages(id),
			endpoint_id TEXT NOT NULL REFERENCES endpoints(id),
			actor TEXT NOT NULL,
			number INTEGER NOT NULL CHECK (number >= 1),
			outcome TEXT NOT NULL CHECK (
				outcome IN ('delivered', 'retrying', 'exhausted', 'endpoint_disabled')
			),
			status_code INTEGER NOT NULL CHECK (status_code >= 0),
			error_text TEXT NOT NULL,
			webhook_timestamp INTEGER NOT NULL,
			attempted_at INTEGER NOT NULL,
			next_attempt_at INTEGER,
			state TEXT NOT NULL CHECK (state IN ('pending', 'succeeded', 'exhausted', 'disabled')),
			disable_endpoint INTEGER NOT NULL CHECK (disable_endpoint IN (0, 1)),
			UNIQUE (delivery_id, number)
		)`,
		`CREATE INDEX IF NOT EXISTS attempts_message
			ON delivery_attempts (message_id, sequence DESC)`,
		`CREATE INDEX IF NOT EXISTS attempts_endpoint
			ON delivery_attempts (endpoint_id, sequence DESC)`,
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
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("prepare webhook sqlite schema: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit webhook schema: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(...any) error
}
