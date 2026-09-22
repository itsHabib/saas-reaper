package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/notification-routing/internal/routing"
)

// CreateRecipient stores a recipient and its channel bindings in one transaction.
func (s *Store) CreateRecipient(ctx context.Context, recipient routing.Recipient) error {
	if recipient.ID == "" || len(recipient.Bindings) == 0 || recipient.CreatedAt.IsZero() {
		return fmt.Errorf("%w: recipient identity, bindings, and timestamp are required", routing.ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin recipient %s: %w", recipient.ID, err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(
		ctx,
		`INSERT INTO recipients (id, created_at) VALUES (?, ?) ON CONFLICT(id) DO NOTHING`,
		recipient.ID,
		recipient.CreatedAt.UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("insert recipient %s: %w", recipient.ID, err)
	}
	written, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count inserted recipient %s: %w", recipient.ID, err)
	}
	if written != 1 {
		return fmt.Errorf("%w: recipient %s already exists", routing.ErrConflict, recipient.ID)
	}
	for position, binding := range recipient.Bindings {
		if err := insertBinding(ctx, tx, recipient.ID, position, binding); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit recipient %s: %w", recipient.ID, err)
	}
	return nil
}

func insertBinding(ctx context.Context, tx *sql.Tx, recipientID string, position int, binding routing.Binding) error {
	if binding.ChannelID == "" || binding.Address == "" {
		return fmt.Errorf("%w: binding channel and address are required", routing.ErrInvalid)
	}
	if _, err := channelByID(ctx, tx, binding.ChannelID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO recipient_channels (recipient_id, channel_id, position, address, enabled)
		 VALUES (?, ?, ?, ?, ?)`,
		recipientID,
		binding.ChannelID,
		position,
		binding.Address,
		boolValue(binding.Enabled),
	); err != nil {
		return fmt.Errorf("insert recipient %s binding %s: %w", recipientID, binding.ChannelID, err)
	}
	return nil
}

// Recipient returns one recipient with bindings in their declared order.
func (s *Store) Recipient(ctx context.Context, id string) (routing.Recipient, error) {
	var createdAt int64
	err := s.db.QueryRowContext(ctx, `SELECT created_at FROM recipients WHERE id = ?`, id).Scan(&createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return routing.Recipient{}, fmt.Errorf("%w: recipient %s", routing.ErrNotFound, id)
	}
	if err != nil {
		return routing.Recipient{}, fmt.Errorf("read recipient %s: %w", id, err)
	}
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT channel_id, address, enabled FROM recipient_channels WHERE recipient_id = ? ORDER BY position`,
		id,
	)
	if err != nil {
		return routing.Recipient{}, fmt.Errorf("query recipient %s bindings: %w", id, err)
	}
	defer rows.Close()
	recipient := routing.Recipient{ID: id, CreatedAt: time.Unix(0, createdAt).UTC()}
	for rows.Next() {
		var binding routing.Binding
		var enabled int
		if err := rows.Scan(&binding.ChannelID, &binding.Address, &enabled); err != nil {
			return routing.Recipient{}, fmt.Errorf("scan recipient %s binding: %w", id, err)
		}
		binding.Enabled = enabled == 1
		recipient.Bindings = append(recipient.Bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return routing.Recipient{}, fmt.Errorf("iterate recipient %s bindings: %w", id, err)
	}
	return recipient, nil
}
