// Package sqlite persists tenant audit chains in one append-only SQLite table.
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

// Store is the durable SQLite authority for the audit ledger specimen.
type Store struct {
	db *sql.DB
}

// Open prepares a SQLite authority at path. The pool holds exactly one
// connection and every transaction begins immediately, so appends from one
// process are serialized by construction.
func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("sqlite path is required")
	}
	if err := secureDatabaseFile(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("open audit sqlite: %w", err)
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
		return fmt.Errorf("create private audit sqlite file: %w", err)
	}
	modeErr := file.Chmod(0o600)
	closeErr := file.Close()
	if err := errors.Join(modeErr, closeErr); err != nil {
		return fmt.Errorf("secure audit sqlite file: %w", err)
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
			return fmt.Errorf("prepare audit sqlite pragma: %w", err)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin audit schema: %w", err)
	}
	defer tx.Rollback()
	statements := []string{
		`CREATE TABLE IF NOT EXISTS entries (
			tenant TEXT NOT NULL,
			sequence INTEGER NOT NULL CHECK (sequence >= 1),
			id TEXT NOT NULL,
			actor TEXT NOT NULL,
			action TEXT NOT NULL,
			target TEXT NOT NULL,
			occurred_at TEXT NOT NULL,
			recorded_at TEXT NOT NULL,
			source TEXT NOT NULL,
			metadata TEXT NOT NULL,
			previous_hash TEXT NOT NULL CHECK (length(previous_hash) = 64),
			hash TEXT NOT NULL CHECK (length(hash) = 64),
			PRIMARY KEY (tenant, sequence),
			UNIQUE (tenant, id)
		)`,
		`CREATE TRIGGER IF NOT EXISTS entries_no_update
			BEFORE UPDATE ON entries
			BEGIN
				SELECT RAISE(ABORT, 'audit ledger is append-only');
			END`,
		`CREATE TRIGGER IF NOT EXISTS entries_no_delete
			BEFORE DELETE ON entries
			BEGIN
				SELECT RAISE(ABORT, 'audit ledger is append-only');
			END`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("prepare audit sqlite schema: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit audit schema: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(...any) error
}
