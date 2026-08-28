package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/itsHabib/saas-reaper/internal/flags"
	// Register the pure-Go SQLite database/sql driver.
	_ "modernc.org/sqlite"
)

// Store persists current definitions and an append-only publication audit.
type Store struct {
	db *sql.DB
}

// Open prepares a SQLite authority at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.prepare(context.Background()); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return store, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) prepare(ctx context.Context) error {
	statements := []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA busy_timeout = 5000`,
		`CREATE TABLE IF NOT EXISTS flags (
			environment TEXT NOT NULL,
			key TEXT NOT NULL,
			definition BLOB NOT NULL,
			revision INTEGER NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (environment, key)
		)`,
		`CREATE TABLE IF NOT EXISTS audit (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			environment TEXT NOT NULL,
			key TEXT NOT NULL,
			revision INTEGER NOT NULL,
			actor TEXT NOT NULL,
			action TEXT NOT NULL,
			definition BLOB NOT NULL,
			occurred_at TEXT NOT NULL
		)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("prepare sqlite: %w", err)
		}
	}
	return nil
}

// Load returns validated definitions grouped by environment.
func (s *Store) Load(ctx context.Context) (map[string][]flags.Flag, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT environment, definition FROM flags ORDER BY environment, key`)
	if err != nil {
		return nil, fmt.Errorf("query flags: %w", err)
	}
	defer rows.Close()
	loaded := make(map[string][]flags.Flag)
	for rows.Next() {
		var environment string
		var definition []byte
		if err := rows.Scan(&environment, &definition); err != nil {
			return nil, fmt.Errorf("scan flag: %w", err)
		}
		flag, err := flags.Decode(definition)
		if err != nil {
			return nil, fmt.Errorf("decode stored flag: %w", err)
		}
		loaded[environment] = append(loaded[environment], flag)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate flags: %w", err)
	}
	return loaded, nil
}

// Publish atomically compares revision, stores the definition, and appends audit.
func (s *Store) Publish(
	ctx context.Context,
	environment string,
	flag flags.Flag,
	expectedRevision int64,
	actor string,
) (flags.Flag, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return flags.Flag{}, fmt.Errorf("begin publish: %w", err)
	}
	defer tx.Rollback()
	currentRevision, exists, err := readRevision(ctx, tx, environment, flag.Key)
	if err != nil {
		return flags.Flag{}, err
	}
	if !exists && expectedRevision != 0 {
		return flags.Flag{}, conflict(expectedRevision, 0)
	}
	if exists && currentRevision != expectedRevision {
		return flags.Flag{}, conflict(expectedRevision, currentRevision)
	}
	flag.Revision = currentRevision + 1
	flag.UpdatedAt = time.Now().UTC()
	definition, err := json.Marshal(flag)
	if err != nil {
		return flags.Flag{}, fmt.Errorf("encode flag: %w", err)
	}
	if err := writeFlag(ctx, tx, environment, flag, definition); err != nil {
		return flags.Flag{}, err
	}
	if err := appendAudit(ctx, tx, environment, flag, actor, definition); err != nil {
		return flags.Flag{}, err
	}
	if err := tx.Commit(); err != nil {
		return flags.Flag{}, fmt.Errorf("commit publish: %w", err)
	}
	return flag.Copy(), nil
}

func readRevision(ctx context.Context, tx *sql.Tx, environment, key string) (int64, bool, error) {
	var revision int64
	err := tx.QueryRowContext(
		ctx,
		`SELECT revision FROM flags WHERE environment = ? AND key = ?`,
		environment,
		key,
	).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read current revision: %w", err)
	}
	return revision, true, nil
}

func writeFlag(ctx context.Context, tx *sql.Tx, environment string, flag flags.Flag, definition []byte) error {
	_, err := tx.ExecContext(
		ctx,
		`INSERT INTO flags (environment, key, definition, revision, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(environment, key) DO UPDATE SET
			definition = excluded.definition,
			revision = excluded.revision,
			updated_at = excluded.updated_at`,
		environment,
		flag.Key,
		definition,
		flag.Revision,
		flag.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("write flag: %w", err)
	}
	return nil
}

func appendAudit(
	ctx context.Context,
	tx *sql.Tx,
	environment string,
	flag flags.Flag,
	actor string,
	definition []byte,
) error {
	_, err := tx.ExecContext(
		ctx,
		`INSERT INTO audit (environment, key, revision, actor, action, definition, occurred_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		environment,
		flag.Key,
		flag.Revision,
		actor,
		"published",
		definition,
		flag.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("append audit: %w", err)
	}
	return nil
}

func conflict(expected, current int64) error {
	return fmt.Errorf("%w: expected %d, current %d", flags.ErrConflict, expected, current)
}
