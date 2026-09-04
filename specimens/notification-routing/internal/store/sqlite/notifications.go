package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/itsHabib/saas-reaper/specimens/notification-routing/internal/routing"
)

// Send atomically stores a notification and its rendered deliveries, or returns the earlier
// acceptance for a reused idempotency key.
//
// A channel disabled between policy's snapshot and this commit drops only its own delivery;
// the notification and every sibling delivery still commit.
func (s *Store) Send(
	ctx context.Context,
	notification routing.Notification,
	deliveries []routing.Delivery,
) (routing.Acceptance, error) {
	if err := validateNotification(notification); err != nil {
		return routing.Acceptance{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return routing.Acceptance{}, fmt.Errorf("begin notification %s: %w", notification.ID, err)
	}
	defer tx.Rollback()
	inserted, err := insertNotification(ctx, tx, notification)
	if err != nil {
		return routing.Acceptance{}, err
	}
	if !inserted {
		return earlierAcceptance(ctx, tx, notification)
	}
	acceptance := routing.Acceptance{NotificationID: notification.ID, Deliveries: []routing.QueuedDelivery{}}
	for _, item := range deliveries {
		queued, err := insertDelivery(ctx, tx, notification, item)
		if err != nil {
			return routing.Acceptance{}, err
		}
		if !queued {
			continue
		}
		acceptance.Deliveries = append(acceptance.Deliveries, routing.QueuedDelivery{ID: item.ID, ChannelID: item.ChannelID})
	}
	if err := tx.Commit(); err != nil {
		return routing.Acceptance{}, fmt.Errorf("commit notification %s: %w", notification.ID, err)
	}
	return acceptance, nil
}

// AcceptedNotification returns the acceptance an idempotency key already produced, with the
// fingerprint of the send that produced it, or ErrNotFound when the key is unused.
func (s *Store) AcceptedNotification(ctx context.Context, key string) (routing.Acceptance, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return routing.Acceptance{}, "", fmt.Errorf("begin idempotency lookup: %w", err)
	}
	defer tx.Rollback()
	acceptance, fingerprint, err := acceptanceForKey(ctx, tx, key)
	if err != nil {
		return routing.Acceptance{}, "", err
	}
	return acceptance, fingerprint, nil
}

func acceptanceForKey(ctx context.Context, tx *sql.Tx, key string) (routing.Acceptance, string, error) {
	var existingID string
	var fingerprint string
	err := tx.QueryRowContext(
		ctx,
		`SELECT id, fingerprint FROM notifications WHERE idempotency_key = ?`,
		key,
	).Scan(&existingID, &fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return routing.Acceptance{}, "", fmt.Errorf("%w: idempotency key %s", routing.ErrNotFound, key)
	}
	if err != nil {
		return routing.Acceptance{}, "", fmt.Errorf("load notification for idempotency key: %w", err)
	}
	rows, err := tx.QueryContext(
		ctx,
		`SELECT id, channel_id FROM deliveries WHERE notification_id = ? ORDER BY created_at, id`,
		existingID,
	)
	if err != nil {
		return routing.Acceptance{}, "", fmt.Errorf("load earlier deliveries for %s: %w", existingID, err)
	}
	defer rows.Close()
	acceptance := routing.Acceptance{
		NotificationID: existingID,
		Deduplicated:   true,
		Deliveries:     []routing.QueuedDelivery{},
	}
	for rows.Next() {
		var queued routing.QueuedDelivery
		if err := rows.Scan(&queued.ID, &queued.ChannelID); err != nil {
			return routing.Acceptance{}, "", fmt.Errorf("scan earlier delivery for %s: %w", existingID, err)
		}
		acceptance.Deliveries = append(acceptance.Deliveries, queued)
	}
	if err := rows.Err(); err != nil {
		return routing.Acceptance{}, "", fmt.Errorf("iterate earlier deliveries for %s: %w", existingID, err)
	}
	return acceptance, fingerprint, nil
}

func insertNotification(ctx context.Context, tx *sql.Tx, notification routing.Notification) (bool, error) {
	result, err := tx.ExecContext(
		ctx,
		`INSERT INTO notifications (
			id, idempotency_key, fingerprint, template_key, recipient_id, payload, actor, created_at
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(idempotency_key) DO NOTHING`,
		notification.ID,
		notification.IdempotencyKey,
		notification.Fingerprint,
		notification.TemplateKey,
		notification.RecipientID,
		notification.Payload,
		notification.Actor,
		notification.CreatedAt.UnixNano(),
	)
	if err != nil {
		return false, fmt.Errorf("insert notification %s: %w", notification.ID, err)
	}
	written, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count inserted notification %s: %w", notification.ID, err)
	}
	return written == 1, nil
}

func earlierAcceptance(ctx context.Context, tx *sql.Tx, notification routing.Notification) (routing.Acceptance, error) {
	acceptance, fingerprint, err := acceptanceForKey(ctx, tx, notification.IdempotencyKey)
	if err != nil {
		return routing.Acceptance{}, err
	}
	if fingerprint != notification.Fingerprint {
		return routing.Acceptance{}, fmt.Errorf(
			"%w: idempotency key %s was accepted for a different template, recipient, or payload",
			routing.ErrConflict, notification.IdempotencyKey,
		)
	}
	return acceptance, nil
}

func insertDelivery(ctx context.Context, tx *sql.Tx, notification routing.Notification, item routing.Delivery) (bool, error) {
	if err := validateNewDelivery(notification, item); err != nil {
		return false, err
	}
	channel, err := channelByID(ctx, tx, item.ChannelID)
	if err != nil {
		return false, err
	}
	if !channel.Enabled {
		return false, nil
	}
	result, err := tx.ExecContext(
		ctx,
		`INSERT INTO deliveries (
			id, notification_id, recipient_id, channel_id, actor, address, subject, body,
			state, attempt_count, next_attempt_at, created_at
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)
		 ON CONFLICT(id) DO NOTHING`,
		item.ID,
		item.NotificationID,
		item.RecipientID,
		item.ChannelID,
		item.Actor,
		item.Address,
		item.Subject,
		item.Body,
		routing.StatePending,
		item.NextAttemptAt.UnixNano(),
		item.CreatedAt.UnixNano(),
	)
	if err != nil {
		return false, fmt.Errorf("insert delivery %s: %w", item.ID, err)
	}
	written, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count inserted delivery %s: %w", item.ID, err)
	}
	if written != 1 {
		return false, fmt.Errorf("%w: delivery %s already exists", routing.ErrConflict, item.ID)
	}
	return true, nil
}

func validateNotification(notification routing.Notification) error {
	if notification.ID == "" || notification.IdempotencyKey == "" || notification.Fingerprint == "" {
		return fmt.Errorf("%w: notification identity, idempotency key, and fingerprint are required", routing.ErrInvalid)
	}
	if notification.TemplateKey == "" || notification.RecipientID == "" || notification.Actor == "" {
		return fmt.Errorf("%w: notification template, recipient, and actor are required", routing.ErrInvalid)
	}
	if len(notification.Payload) == 0 || notification.CreatedAt.IsZero() {
		return fmt.Errorf("%w: notification payload and timestamp are required", routing.ErrInvalid)
	}
	return nil
}

func validateNewDelivery(notification routing.Notification, item routing.Delivery) error {
	if item.ID == "" || item.ChannelID == "" || item.Address == "" || item.Body == "" {
		return fmt.Errorf("%w: delivery identity, channel, address, and body are required", routing.ErrInvalid)
	}
	if item.NotificationID != notification.ID || item.RecipientID != notification.RecipientID || item.Actor != notification.Actor {
		return fmt.Errorf("%w: delivery %s does not belong to notification %s", routing.ErrInvalid, item.ID, notification.ID)
	}
	if item.State != routing.StatePending || item.AttemptCount != 0 {
		return fmt.Errorf("%w: new delivery must be pending without attempts", routing.ErrInvalid)
	}
	if item.NextAttemptAt.IsZero() || item.CreatedAt.IsZero() {
		return fmt.Errorf("%w: new delivery timestamps are required", routing.ErrInvalid)
	}
	return nil
}

// deliveryState is a test-facing read of durable delivery state.
func (s *Store) deliveryState(ctx context.Context, id string) (routing.DeliveryState, int, error) {
	var state routing.DeliveryState
	var attemptCount int
	err := s.db.QueryRowContext(ctx, `SELECT state, attempt_count FROM deliveries WHERE id = ?`, id).Scan(&state, &attemptCount)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, fmt.Errorf("%w: delivery %s", routing.ErrNotFound, id)
	}
	if err != nil {
		return "", 0, fmt.Errorf("read delivery %s: %w", id, err)
	}
	return state, attemptCount, nil
}
