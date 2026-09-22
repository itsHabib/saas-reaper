package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/webhook-delivery/internal/delivery"
)

// Publish atomically stores one exact message body and returns the deliveries it queued.
//
// An endpoint disabled since the caller snapshotted it is skipped inside the
// transaction rather than failing the message and its healthy siblings.
func (s *Store) Publish(
	ctx context.Context,
	message delivery.Message,
	deliveries []delivery.Delivery,
) ([]delivery.Delivery, error) {
	if err := validateMessage(message); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin publication %s: %w", message.ID, err)
	}
	defer tx.Rollback()
	if err := insertMessage(ctx, tx, message); err != nil {
		return nil, err
	}
	queued := make([]delivery.Delivery, 0, len(deliveries))
	for _, item := range deliveries {
		if item.MessageID != message.ID || item.Actor != message.Actor || item.Kind != delivery.DeliveryOriginal {
			return nil, fmt.Errorf("%w: publication delivery does not match message %s", delivery.ErrInvalid, message.ID)
		}
		enabled, err := endpointEnabled(ctx, tx, item.EndpointID)
		if err != nil {
			return nil, err
		}
		if !enabled {
			continue
		}
		if err := insertDelivery(ctx, tx, item); err != nil {
			return nil, err
		}
		queued = append(queued, item)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit publication %s: %w", message.ID, err)
	}
	return queued, nil
}

// Message returns one immutable message with an independent payload copy.
func (s *Store) Message(ctx context.Context, id string) (delivery.Message, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT id, payload, actor, created_at FROM messages WHERE id = ?`,
		id,
	)
	message, err := scanMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return delivery.Message{}, fmt.Errorf("%w: message %s", delivery.ErrNotFound, id)
	}
	if err != nil {
		return delivery.Message{}, fmt.Errorf("read message %s: %w", id, err)
	}
	return message, nil
}

// Replay queues a new delivery of an existing message to a currently enabled endpoint.
func (s *Store) Replay(ctx context.Context, item delivery.Delivery) error {
	if item.Kind != delivery.DeliveryReplay {
		return fmt.Errorf("%w: replay delivery kind is required", delivery.ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replay %s: %w", item.ID, err)
	}
	defer tx.Rollback()
	if err := requireMessage(ctx, tx, item.MessageID); err != nil {
		return err
	}
	if err := requireEndpointEnabled(ctx, tx, item.EndpointID); err != nil {
		return err
	}
	if err := insertDelivery(ctx, tx, item); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit replay %s: %w", item.ID, err)
	}
	return nil
}

func insertMessage(ctx context.Context, tx *sql.Tx, message delivery.Message) error {
	result, err := tx.ExecContext(
		ctx,
		`INSERT INTO messages (id, payload, actor, created_at)
		 VALUES (?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`,
		message.ID,
		message.Payload,
		message.Actor,
		message.CreatedAt.UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("insert message %s: %w", message.ID, err)
	}
	written, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count inserted message %s: %w", message.ID, err)
	}
	if written != 1 {
		return fmt.Errorf("%w: message %s already exists", delivery.ErrConflict, message.ID)
	}
	return nil
}

func insertDelivery(ctx context.Context, tx *sql.Tx, item delivery.Delivery) error {
	if err := validateNewDelivery(item); err != nil {
		return err
	}
	result, err := tx.ExecContext(
		ctx,
		`INSERT INTO deliveries (
			id, message_id, endpoint_id, actor, kind, state,
			attempt_count, next_attempt_at, created_at
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO NOTHING`,
		item.ID,
		item.MessageID,
		item.EndpointID,
		item.Actor,
		item.Kind,
		item.State,
		item.AttemptCount,
		item.NextAttemptAt.UnixNano(),
		item.CreatedAt.UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("insert delivery %s: %w", item.ID, err)
	}
	written, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count inserted delivery %s: %w", item.ID, err)
	}
	if written != 1 {
		return fmt.Errorf("%w: delivery %s already exists", delivery.ErrConflict, item.ID)
	}
	return nil
}

func requireMessage(ctx context.Context, tx *sql.Tx, id string) error {
	var found string
	err := tx.QueryRowContext(ctx, `SELECT id FROM messages WHERE id = ?`, id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: message %s", delivery.ErrNotFound, id)
	}
	if err != nil {
		return fmt.Errorf("check message %s: %w", id, err)
	}
	return nil
}

func requireEndpointEnabled(ctx context.Context, tx *sql.Tx, id string) error {
	enabled, err := endpointEnabled(ctx, tx, id)
	if err != nil {
		return err
	}
	if !enabled {
		return fmt.Errorf("%w: endpoint %s", delivery.ErrDisabled, id)
	}
	return nil
}

func endpointEnabled(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var enabled int
	err := tx.QueryRowContext(ctx, `SELECT enabled FROM endpoints WHERE id = ?`, id).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("%w: endpoint %s", delivery.ErrNotFound, id)
	}
	if err != nil {
		return false, fmt.Errorf("check endpoint %s: %w", id, err)
	}
	return enabled == 1, nil
}

func scanMessage(row rowScanner) (delivery.Message, error) {
	var message delivery.Message
	var createdAt int64
	if err := row.Scan(&message.ID, &message.Payload, &message.Actor, &createdAt); err != nil {
		return delivery.Message{}, err
	}
	message.Payload = append([]byte(nil), message.Payload...)
	message.CreatedAt = time.Unix(0, createdAt).UTC()
	return message, nil
}

func validateMessage(message delivery.Message) error {
	if message.ID == "" || len(message.Payload) == 0 || message.Actor == "" || message.CreatedAt.IsZero() {
		return fmt.Errorf("%w: complete message identity, payload, actor, and timestamp are required", delivery.ErrInvalid)
	}
	return nil
}

func validateNewDelivery(item delivery.Delivery) error {
	if item.ID == "" || item.MessageID == "" || item.EndpointID == "" || item.Actor == "" {
		return fmt.Errorf("%w: delivery identity and actor are required", delivery.ErrInvalid)
	}
	if item.Kind != delivery.DeliveryOriginal && item.Kind != delivery.DeliveryReplay {
		return fmt.Errorf("%w: unsupported delivery kind %q", delivery.ErrInvalid, item.Kind)
	}
	if item.State != delivery.StatePending || item.AttemptCount != 0 {
		return fmt.Errorf("%w: new delivery must be pending without attempts", delivery.ErrInvalid)
	}
	if item.NextAttemptAt.IsZero() || item.CreatedAt.IsZero() {
		return fmt.Errorf("%w: new delivery timestamps are required", delivery.ErrInvalid)
	}
	return nil
}
