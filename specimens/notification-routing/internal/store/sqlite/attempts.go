package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/notification-routing/internal/routing"
)

// Due joins pending delivery state to the transport kind of its still-enabled channel.
func (s *Store) Due(ctx context.Context, now time.Time, limit int) ([]routing.Dispatch, error) {
	if now.IsZero() || limit < 1 || limit > 100 {
		return nil, fmt.Errorf("%w: due time and limit between 1 and 100 are required", routing.ErrInvalid)
	}
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT
			d.id, d.notification_id, d.recipient_id, d.channel_id, c.kind,
			d.actor, d.address, d.subject, d.body, d.attempt_count
		 FROM deliveries AS d
		 JOIN channels AS c ON c.id = d.channel_id
		 WHERE d.state = ? AND c.enabled = 1 AND d.next_attempt_at <= ?
		 ORDER BY d.next_attempt_at, d.created_at, d.id
		 LIMIT ?`,
		routing.StatePending,
		now.UTC().UnixNano(),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query due deliveries: %w", err)
	}
	defer rows.Close()
	due := make([]routing.Dispatch, 0, limit)
	for rows.Next() {
		var item routing.Dispatch
		if err := rows.Scan(
			&item.DeliveryID, &item.NotificationID, &item.RecipientID, &item.ChannelID, &item.Kind,
			&item.Actor, &item.Address, &item.Subject, &item.Body, &item.AttemptCount,
		); err != nil {
			return nil, fmt.Errorf("scan due delivery: %w", err)
		}
		due = append(due, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate due deliveries: %w", err)
	}
	return due, nil
}

// Deliverable reports whether a delivery loaded in an earlier batch is still pending on a
// still-enabled channel. The dispatcher calls it immediately before each transport call so a
// channel disabled partway through a batch cannot have the rest of that batch sent to it.
func (s *Store) Deliverable(ctx context.Context, deliveryID string) (bool, error) {
	var found int
	err := s.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*)
		 FROM deliveries AS d
		 JOIN channels AS c ON c.id = d.channel_id
		 WHERE d.id = ? AND d.state = ? AND c.enabled = 1`,
		deliveryID,
		routing.StatePending,
	).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("recheck delivery %s: %w", deliveryID, err)
	}
	return found == 1, nil
}

// RecordAttempt appends one audit row and applies its delivery transition atomically.
//
// A delivery canceled by channel disablement while its send was in flight keeps the audit
// row (the transport call happened) but stays canceled; the transition is not applied.
func (s *Store) RecordAttempt(ctx context.Context, attempt routing.Attempt) error {
	if err := validateAttempt(attempt); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delivery %s attempt: %w", attempt.DeliveryID, err)
	}
	defer tx.Rollback()
	current, err := loadAttemptTarget(ctx, tx, attempt.DeliveryID)
	if err != nil {
		return err
	}
	if err := validateAttemptTarget(attempt, current); err != nil {
		return err
	}
	if current.state == routing.StateCanceled {
		attempt.State = routing.StateCanceled
		attempt.NextAttemptAt = time.Time{}
		if err := insertAttempt(ctx, tx, attempt); err != nil {
			return err
		}
		return commitAttempt(tx, attempt)
	}
	if current.state != routing.StatePending {
		return fmt.Errorf("%w: delivery %s is %s", routing.ErrConflict, attempt.DeliveryID, current.state)
	}
	if err := insertAttempt(ctx, tx, attempt); err != nil {
		return err
	}
	if err := transitionDelivery(ctx, tx, attempt, current.attemptCount); err != nil {
		return err
	}
	return commitAttempt(tx, attempt)
}

func commitAttempt(tx *sql.Tx, attempt routing.Attempt) error {
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delivery %s attempt: %w", attempt.DeliveryID, err)
	}
	return nil
}

type attemptTarget struct {
	notificationID string
	recipientID    string
	channelID      string
	actor          string
	state          routing.DeliveryState
	attemptCount   int
}

func loadAttemptTarget(ctx context.Context, tx *sql.Tx, id string) (attemptTarget, error) {
	var target attemptTarget
	err := tx.QueryRowContext(
		ctx,
		`SELECT notification_id, recipient_id, channel_id, actor, state, attempt_count FROM deliveries WHERE id = ?`,
		id,
	).Scan(
		&target.notificationID, &target.recipientID, &target.channelID, &target.actor, &target.state, &target.attemptCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return attemptTarget{}, fmt.Errorf("%w: delivery %s", routing.ErrNotFound, id)
	}
	if err != nil {
		return attemptTarget{}, fmt.Errorf("load delivery %s attempt target: %w", id, err)
	}
	return target, nil
}

func validateAttemptTarget(attempt routing.Attempt, current attemptTarget) error {
	identityMatches := attempt.NotificationID == current.notificationID &&
		attempt.RecipientID == current.recipientID &&
		attempt.ChannelID == current.channelID
	if !identityMatches {
		return fmt.Errorf("%w: attempt identity does not match delivery %s", routing.ErrInvalid, attempt.DeliveryID)
	}
	if attempt.Actor != current.actor {
		return fmt.Errorf("%w: attempt actor does not match delivery actor", routing.ErrInvalid)
	}
	if attempt.Number != current.attemptCount+1 {
		return fmt.Errorf(
			"%w: delivery %s attempt number %d follows %d",
			routing.ErrConflict, attempt.DeliveryID, attempt.Number, current.attemptCount,
		)
	}
	return nil
}

func insertAttempt(ctx context.Context, tx *sql.Tx, attempt routing.Attempt) error {
	_, err := tx.ExecContext(
		ctx,
		`INSERT INTO delivery_attempts (
			delivery_id, notification_id, recipient_id, channel_id, actor, number,
			outcome, code, error_text, attempted_at, next_attempt_at, state
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		attempt.DeliveryID,
		attempt.NotificationID,
		attempt.RecipientID,
		attempt.ChannelID,
		attempt.Actor,
		attempt.Number,
		attempt.Outcome,
		attempt.Code,
		attempt.Error,
		attempt.AttemptedAt.UnixNano(),
		nullTime(attempt.NextAttemptAt),
		attempt.State,
	)
	if err != nil {
		return fmt.Errorf("append delivery %s attempt %d: %w", attempt.DeliveryID, attempt.Number, err)
	}
	return nil
}

func transitionDelivery(ctx context.Context, tx *sql.Tx, attempt routing.Attempt, currentAttemptCount int) error {
	result, err := tx.ExecContext(
		ctx,
		`UPDATE deliveries
		 SET state = ?, attempt_count = ?, next_attempt_at = ?
		 WHERE id = ? AND state = ? AND attempt_count = ?`,
		attempt.State,
		attempt.Number,
		nullTime(attempt.NextAttemptAt),
		attempt.DeliveryID,
		routing.StatePending,
		currentAttemptCount,
	)
	if err != nil {
		return fmt.Errorf("transition delivery %s: %w", attempt.DeliveryID, err)
	}
	written, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count delivery %s transition: %w", attempt.DeliveryID, err)
	}
	if written != 1 {
		return fmt.Errorf("%w: delivery %s changed during attempt", routing.ErrConflict, attempt.DeliveryID)
	}
	return nil
}

// Attempts returns newest-first append-only evidence with optional notification and channel filters.
func (s *Store) Attempts(
	ctx context.Context,
	filter routing.AttemptFilter,
	limit int,
) ([]routing.Attempt, error) {
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("%w: attempt limit must be between 1 and 1000", routing.ErrInvalid)
	}
	var query strings.Builder
	query.WriteString(`SELECT
		sequence, delivery_id, notification_id, recipient_id, channel_id, actor,
		number, outcome, code, error_text, attempted_at, next_attempt_at, state
		FROM delivery_attempts WHERE 1 = 1`)
	arguments := make([]any, 0, 3)
	if filter.NotificationID != "" {
		query.WriteString(` AND notification_id = ?`)
		arguments = append(arguments, filter.NotificationID)
	}
	if filter.ChannelID != "" {
		query.WriteString(` AND channel_id = ?`)
		arguments = append(arguments, filter.ChannelID)
	}
	query.WriteString(` ORDER BY sequence DESC LIMIT ?`)
	arguments = append(arguments, limit)
	rows, err := s.db.QueryContext(ctx, query.String(), arguments...)
	if err != nil {
		return nil, fmt.Errorf("query delivery attempts: %w", err)
	}
	defer rows.Close()
	attempts := make([]routing.Attempt, 0, limit)
	for rows.Next() {
		attempt, err := scanAttempt(rows)
		if err != nil {
			return nil, err
		}
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate delivery attempts: %w", err)
	}
	return attempts, nil
}

func scanAttempt(row rowScanner) (routing.Attempt, error) {
	var attempt routing.Attempt
	var attemptedAt int64
	var nextAttemptAt sql.NullInt64
	if err := row.Scan(
		&attempt.Sequence, &attempt.DeliveryID, &attempt.NotificationID, &attempt.RecipientID,
		&attempt.ChannelID, &attempt.Actor, &attempt.Number, &attempt.Outcome, &attempt.Code,
		&attempt.Error, &attemptedAt, &nextAttemptAt, &attempt.State,
	); err != nil {
		return routing.Attempt{}, fmt.Errorf("scan delivery attempt: %w", err)
	}
	attempt.AttemptedAt = time.Unix(0, attemptedAt).UTC()
	if nextAttemptAt.Valid {
		attempt.NextAttemptAt = time.Unix(0, nextAttemptAt.Int64).UTC()
	}
	return attempt, nil
}

func validateAttempt(attempt routing.Attempt) error {
	identityMissing := attempt.DeliveryID == "" || attempt.NotificationID == "" ||
		attempt.RecipientID == "" || attempt.ChannelID == "" || attempt.Actor == ""
	if identityMissing {
		return fmt.Errorf("%w: complete attempt identity and actor are required", routing.ErrInvalid)
	}
	if attempt.Number < 1 || attempt.AttemptedAt.IsZero() || attempt.Code < 0 {
		return fmt.Errorf("%w: positive attempt number, timestamp, and nonnegative code are required", routing.ErrInvalid)
	}
	transition, known := routing.TransitionFor(attempt.Outcome)
	if !known || attempt.State != transition.State || !attempt.NextAttemptAt.IsZero() != transition.RetryScheduled {
		return fmt.Errorf(
			"%w: outcome %s cannot transition to %s",
			routing.ErrInvalid, attempt.Outcome, attempt.State,
		)
	}
	return nil
}

func nullTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC().UnixNano()
}
