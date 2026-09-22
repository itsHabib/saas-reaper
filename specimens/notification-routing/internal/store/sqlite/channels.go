package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/notification-routing/internal/routing"
)

// RegisterChannel atomically creates the first channel revision.
func (s *Store) RegisterChannel(ctx context.Context, channel routing.Channel) error {
	if channel.ID == "" || !channel.Kind.Known() || !channel.Enabled || channel.Revision != 1 {
		return fmt.Errorf("%w: registered channel must be an enabled known kind at revision 1", routing.ErrInvalid)
	}
	if channel.CreatedAt.IsZero() || channel.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: channel timestamps are required", routing.ErrInvalid)
	}
	result, err := s.db.ExecContext(
		ctx,
		`INSERT INTO channels (id, kind, enabled, revision, created_at, updated_at)
		 VALUES (?, ?, 1, 1, ?, ?)
		 ON CONFLICT(id) DO NOTHING`,
		channel.ID,
		channel.Kind,
		channel.CreatedAt.UnixNano(),
		channel.UpdatedAt.UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("register channel %s: %w", channel.ID, err)
	}
	written, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count registered channel %s: %w", channel.ID, err)
	}
	if written != 1 {
		return fmt.Errorf("%w: channel %s already exists", routing.ErrConflict, channel.ID)
	}
	return nil
}

// DisableChannel revisions a channel and cancels its pending deliveries in one transaction.
func (s *Store) DisableChannel(
	ctx context.Context,
	id string,
	expectedRevision int64,
	at time.Time,
) (routing.Channel, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return routing.Channel{}, fmt.Errorf("begin disable channel %s: %w", id, err)
	}
	defer tx.Rollback()
	current, err := channelByID(ctx, tx, id)
	if err != nil {
		return routing.Channel{}, err
	}
	if !current.Enabled {
		return routing.Channel{}, fmt.Errorf("%w: channel %s", routing.ErrDisabled, id)
	}
	if current.Revision != expectedRevision {
		return routing.Channel{}, fmt.Errorf(
			"%w: channel %s expected revision %d, current %d",
			routing.ErrConflict, id, expectedRevision, current.Revision,
		)
	}
	updatedAt := at.UTC()
	result, err := tx.ExecContext(
		ctx,
		`UPDATE channels
		 SET enabled = 0, revision = revision + 1, updated_at = ?
		 WHERE id = ? AND enabled = 1 AND revision = ?`,
		updatedAt.UnixNano(),
		id,
		expectedRevision,
	)
	if err != nil {
		return routing.Channel{}, fmt.Errorf("disable channel %s: %w", id, err)
	}
	written, err := result.RowsAffected()
	if err != nil {
		return routing.Channel{}, fmt.Errorf("count disabled channel %s: %w", id, err)
	}
	if written != 1 {
		return routing.Channel{}, fmt.Errorf("%w: channel %s changed during disable", routing.ErrConflict, id)
	}
	if _, err := tx.ExecContext(
		ctx,
		`UPDATE deliveries SET state = ?, next_attempt_at = NULL WHERE channel_id = ? AND state = ?`,
		routing.StateCanceled,
		id,
		routing.StatePending,
	); err != nil {
		return routing.Channel{}, fmt.Errorf("cancel channel %s deliveries: %w", id, err)
	}
	current.Enabled = false
	current.Revision++
	current.UpdatedAt = updatedAt
	if err := tx.Commit(); err != nil {
		return routing.Channel{}, fmt.Errorf("commit disable channel %s: %w", id, err)
	}
	return current, nil
}

// ListChannels returns every channel in stable creation order.
func (s *Store) ListChannels(ctx context.Context) ([]routing.Channel, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, kind, enabled, revision, created_at, updated_at FROM channels ORDER BY created_at, id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	defer rows.Close()
	channels := make([]routing.Channel, 0)
	for rows.Next() {
		channel, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		channels = append(channels, channel)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate channels: %w", err)
	}
	return channels, nil
}

func channelByID(ctx context.Context, queryer rowQueryer, id string) (routing.Channel, error) {
	row := queryer.QueryRowContext(
		ctx,
		`SELECT id, kind, enabled, revision, created_at, updated_at FROM channels WHERE id = ?`,
		id,
	)
	channel, err := scanChannel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return routing.Channel{}, fmt.Errorf("%w: channel %s", routing.ErrNotFound, id)
	}
	return channel, err
}

func scanChannel(row rowScanner) (routing.Channel, error) {
	var channel routing.Channel
	var enabled int
	var createdAt int64
	var updatedAt int64
	if err := row.Scan(&channel.ID, &channel.Kind, &enabled, &channel.Revision, &createdAt, &updatedAt); err != nil {
		return routing.Channel{}, err
	}
	channel.Enabled = enabled == 1
	channel.CreatedAt = time.Unix(0, createdAt).UTC()
	channel.UpdatedAt = time.Unix(0, updatedAt).UTC()
	return channel, nil
}
