package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/notification-routing/internal/routing"
)

// CreateTemplate stores one immutable channel variant of a template key.
func (s *Store) CreateTemplate(ctx context.Context, template routing.Template) error {
	if template.Key == "" || template.ChannelID == "" || template.Body == "" || template.CreatedAt.IsZero() {
		return fmt.Errorf("%w: template key, channel, body, and timestamp are required", routing.ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin template %s/%s: %w", template.Key, template.ChannelID, err)
	}
	defer tx.Rollback()
	if _, err := channelByID(ctx, tx, template.ChannelID); err != nil {
		return err
	}
	result, err := tx.ExecContext(
		ctx,
		`INSERT INTO templates (key, channel_id, subject, body, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(key, channel_id) DO NOTHING`,
		template.Key,
		template.ChannelID,
		template.Subject,
		template.Body,
		template.CreatedAt.UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("insert template %s/%s: %w", template.Key, template.ChannelID, err)
	}
	written, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count inserted template %s/%s: %w", template.Key, template.ChannelID, err)
	}
	if written != 1 {
		return fmt.Errorf("%w: template %s already has a %s variant", routing.ErrConflict, template.Key, template.ChannelID)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit template %s/%s: %w", template.Key, template.ChannelID, err)
	}
	return nil
}

// Templates returns every channel variant of one key in stable creation order.
func (s *Store) Templates(ctx context.Context, key string) ([]routing.Template, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT key, channel_id, subject, body, created_at
		 FROM templates WHERE key = ? ORDER BY created_at, channel_id`,
		key,
	)
	if err != nil {
		return nil, fmt.Errorf("query template %s: %w", key, err)
	}
	defer rows.Close()
	variants := make([]routing.Template, 0)
	for rows.Next() {
		var variant routing.Template
		var createdAt int64
		if err := rows.Scan(&variant.Key, &variant.ChannelID, &variant.Subject, &variant.Body, &createdAt); err != nil {
			return nil, fmt.Errorf("scan template %s: %w", key, err)
		}
		variant.CreatedAt = time.Unix(0, createdAt).UTC()
		variants = append(variants, variant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate template %s: %w", key, err)
	}
	return variants, nil
}
