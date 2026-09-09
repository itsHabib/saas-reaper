// Package sqlite persists tunnel claims and the append-only lifecycle audit.
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

// Store is the durable SQLite authority for the ingress-tunnel specimen.
type Store struct {
	db *sql.DB
}

// timeLayout is fixed width, unlike RFC3339Nano, so the text a claim's created_at is stored as
// sorts the way the instants do.
const timeLayout = "2006-01-02T15:04:05.000000000Z07:00"

var schema = []string{
	`CREATE TABLE IF NOT EXISTS claims (
		subdomain TEXT PRIMARY KEY,
		token_hash TEXT NOT NULL UNIQUE,
		revision INTEGER NOT NULL,
		revoked INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		revoked_at TEXT
	)`,
	`CREATE TABLE IF NOT EXISTS audit (
		sequence INTEGER PRIMARY KEY AUTOINCREMENT,
		at TEXT NOT NULL,
		subdomain TEXT NOT NULL,
		kind TEXT NOT NULL,
		actor TEXT NOT NULL,
		detail TEXT NOT NULL
	)`,
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
		return nil, fmt.Errorf("open tunnel sqlite: %w", err)
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
		return fmt.Errorf("create private tunnel sqlite file: %w", err)
	}
	modeErr := file.Chmod(0o600)
	closeErr := file.Close()
	if err := errors.Join(modeErr, closeErr); err != nil {
		return fmt.Errorf("secure tunnel sqlite file: %w", err)
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
			return fmt.Errorf("prepare tunnel sqlite pragma: %w", err)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tunnel schema: %w", err)
	}
	defer tx.Rollback()
	for _, statement := range schema {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("prepare tunnel sqlite schema: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tunnel schema: %w", err)
	}
	return nil
}
