package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/webhook-delivery/internal/delivery"
)

// Due joins pending delivery state to its private endpoint and immutable message data.
func (s *Store) Due(ctx context.Context, now time.Time, limit int) ([]delivery.Dispatch, error) {
	if now.IsZero() || limit < 1 || limit > 100 {
		return nil, fmt.Errorf("%w: due time and limit between 1 and 100 are required", delivery.ErrInvalid)
	}
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT
			d.id, d.message_id, d.endpoint_id, d.actor,
			e.url, e.secret, m.payload, d.attempt_count
		 FROM deliveries AS d
		 JOIN endpoints AS e ON e.id = d.endpoint_id
		 JOIN messages AS m ON m.id = d.message_id
		 WHERE d.state = ? AND e.enabled = 1 AND d.next_attempt_at <= ?
		 ORDER BY d.next_attempt_at, d.created_at, d.id
		 LIMIT ?`,
		delivery.StatePending,
		now.UTC().UnixNano(),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query due deliveries: %w", err)
	}
	defer rows.Close()
	due := make([]delivery.Dispatch, 0, limit)
	for rows.Next() {
		var item delivery.Dispatch
		if err := rows.Scan(
			&item.DeliveryID,
			&item.MessageID,
			&item.EndpointID,
			&item.Actor,
			&item.Destination,
			&item.Secret,
			&item.Payload,
			&item.AttemptCount,
		); err != nil {
			return nil, fmt.Errorf("scan due delivery: %w", err)
		}
		item.Payload = append([]byte(nil), item.Payload...)
		due = append(due, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate due deliveries: %w", err)
	}
	return due, nil
}

// Deliverable reports whether a delivery is still pending against an enabled endpoint.
//
// The dispatcher rechecks each batched delivery with this immediately before
// sending, so work canceled after the batch was selected is never sent.
func (s *Store) Deliverable(ctx context.Context, id string) (bool, error) {
	var pending int
	err := s.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*)
		 FROM deliveries AS d
		 JOIN endpoints AS e ON e.id = d.endpoint_id
		 WHERE d.id = ? AND d.state = ? AND e.enabled = 1`,
		id,
		delivery.StatePending,
	).Scan(&pending)
	if err != nil {
		return false, fmt.Errorf("recheck delivery %s: %w", id, err)
	}
	return pending == 1, nil
}

// RecordAttempt appends one audit row and applies its delivery transition atomically.
func (s *Store) RecordAttempt(ctx context.Context, attempt delivery.Attempt) error {
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
	if err := persistAttemptTransition(ctx, tx, attempt, current.attemptCount); err != nil {
		return err
	}
	if attempt.DisableEndpoint {
		if err := disableFromAttempt(ctx, tx, attempt); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delivery %s attempt: %w", attempt.DeliveryID, err)
	}
	return nil
}

func validateAttemptTarget(attempt delivery.Attempt, current attemptTarget) error {
	if current.state != delivery.StatePending {
		return fmt.Errorf("%w: delivery %s is %s", delivery.ErrConflict, attempt.DeliveryID, current.state)
	}
	if !current.endpointEnabled {
		return fmt.Errorf("%w: endpoint %s", delivery.ErrDisabled, current.endpointID)
	}
	if attempt.MessageID != current.messageID || attempt.EndpointID != current.endpointID {
		return fmt.Errorf("%w: attempt identity does not match delivery %s", delivery.ErrInvalid, attempt.DeliveryID)
	}
	if attempt.Actor != current.actor {
		return fmt.Errorf("%w: attempt actor does not match delivery actor", delivery.ErrInvalid)
	}
	if attempt.Number != current.attemptCount+1 {
		return fmt.Errorf(
			"%w: delivery %s attempt number %d follows %d",
			delivery.ErrConflict,
			attempt.DeliveryID,
			attempt.Number,
			current.attemptCount,
		)
	}
	return nil
}

func persistAttemptTransition(
	ctx context.Context,
	tx *sql.Tx,
	attempt delivery.Attempt,
	currentAttemptCount int,
) error {
	if err := insertAttempt(ctx, tx, attempt); err != nil {
		return err
	}
	result, err := tx.ExecContext(
		ctx,
		`UPDATE deliveries
		 SET state = ?, attempt_count = ?, next_attempt_at = ?
		 WHERE id = ? AND state = ? AND attempt_count = ?`,
		attempt.State,
		attempt.Number,
		nullTime(attempt.NextAttemptAt),
		attempt.DeliveryID,
		delivery.StatePending,
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
		return fmt.Errorf("%w: delivery %s changed during attempt", delivery.ErrConflict, attempt.DeliveryID)
	}
	return nil
}

// Attempts returns newest-first append-only evidence with optional message and endpoint filters.
func (s *Store) Attempts(
	ctx context.Context,
	filter delivery.AttemptFilter,
	limit int,
) ([]delivery.Attempt, error) {
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("%w: attempt limit must be between 1 and 1000", delivery.ErrInvalid)
	}
	var query strings.Builder
	query.WriteString(`SELECT
		sequence, delivery_id, message_id, endpoint_id, actor,
		number, outcome, status_code, error_text, webhook_timestamp,
		attempted_at, next_attempt_at, state, disable_endpoint
		FROM delivery_attempts WHERE 1 = 1`)
	arguments := make([]any, 0, 3)
	if filter.MessageID != "" {
		query.WriteString(` AND message_id = ?`)
		arguments = append(arguments, filter.MessageID)
	}
	if filter.EndpointID != "" {
		query.WriteString(` AND endpoint_id = ?`)
		arguments = append(arguments, filter.EndpointID)
	}
	query.WriteString(` ORDER BY sequence DESC LIMIT ?`)
	arguments = append(arguments, limit)
	rows, err := s.db.QueryContext(ctx, query.String(), arguments...)
	if err != nil {
		return nil, fmt.Errorf("query delivery attempts: %w", err)
	}
	defer rows.Close()
	attempts := make([]delivery.Attempt, 0, limit)
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

type attemptTarget struct {
	messageID       string
	endpointID      string
	actor           string
	state           delivery.DeliveryState
	attemptCount    int
	endpointEnabled bool
}

func loadAttemptTarget(ctx context.Context, tx *sql.Tx, id string) (attemptTarget, error) {
	var target attemptTarget
	var enabled int
	err := tx.QueryRowContext(
		ctx,
		`SELECT d.message_id, d.endpoint_id, d.actor, d.state, d.attempt_count, e.enabled
		 FROM deliveries AS d
		 JOIN endpoints AS e ON e.id = d.endpoint_id
		 WHERE d.id = ?`,
		id,
	).Scan(
		&target.messageID,
		&target.endpointID,
		&target.actor,
		&target.state,
		&target.attemptCount,
		&enabled,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return attemptTarget{}, fmt.Errorf("%w: delivery %s", delivery.ErrNotFound, id)
	}
	if err != nil {
		return attemptTarget{}, fmt.Errorf("load delivery %s attempt target: %w", id, err)
	}
	target.endpointEnabled = enabled == 1
	return target, nil
}

func insertAttempt(ctx context.Context, tx *sql.Tx, attempt delivery.Attempt) error {
	_, err := tx.ExecContext(
		ctx,
		`INSERT INTO delivery_attempts (
			delivery_id, message_id, endpoint_id, actor, number,
			outcome, status_code, error_text, webhook_timestamp,
			attempted_at, next_attempt_at, state, disable_endpoint
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		attempt.DeliveryID,
		attempt.MessageID,
		attempt.EndpointID,
		attempt.Actor,
		attempt.Number,
		attempt.Outcome,
		attempt.StatusCode,
		attempt.Error,
		attempt.WebhookTimestamp,
		attempt.AttemptedAt.UnixNano(),
		nullTime(attempt.NextAttemptAt),
		attempt.State,
		boolValue(attempt.DisableEndpoint),
	)
	if err != nil {
		return fmt.Errorf("append delivery %s attempt %d: %w", attempt.DeliveryID, attempt.Number, err)
	}
	return nil
}

func disableFromAttempt(ctx context.Context, tx *sql.Tx, attempt delivery.Attempt) error {
	result, err := tx.ExecContext(
		ctx,
		`UPDATE endpoints
		 SET enabled = 0, revision = revision + 1, updated_at = ?
		 WHERE id = ? AND enabled = 1`,
		attempt.AttemptedAt.UnixNano(),
		attempt.EndpointID,
	)
	if err != nil {
		return fmt.Errorf("disable endpoint %s from attempt: %w", attempt.EndpointID, err)
	}
	written, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count endpoint %s attempt disable: %w", attempt.EndpointID, err)
	}
	if written != 1 {
		return fmt.Errorf("%w: endpoint %s changed during attempt", delivery.ErrConflict, attempt.EndpointID)
	}
	if _, err := tx.ExecContext(
		ctx,
		`UPDATE deliveries
		 SET state = ?, next_attempt_at = NULL
		 WHERE endpoint_id = ? AND state = ? AND id <> ?`,
		delivery.StateDisabled,
		attempt.EndpointID,
		delivery.StatePending,
		attempt.DeliveryID,
	); err != nil {
		return fmt.Errorf("cancel endpoint %s pending deliveries: %w", attempt.EndpointID, err)
	}
	return nil
}

func scanAttempt(row rowScanner) (delivery.Attempt, error) {
	var attempt delivery.Attempt
	var attemptedAt int64
	var nextAttemptAt sql.NullInt64
	var disableEndpoint int
	if err := row.Scan(
		&attempt.Sequence,
		&attempt.DeliveryID,
		&attempt.MessageID,
		&attempt.EndpointID,
		&attempt.Actor,
		&attempt.Number,
		&attempt.Outcome,
		&attempt.StatusCode,
		&attempt.Error,
		&attempt.WebhookTimestamp,
		&attemptedAt,
		&nextAttemptAt,
		&attempt.State,
		&disableEndpoint,
	); err != nil {
		return delivery.Attempt{}, fmt.Errorf("scan delivery attempt: %w", err)
	}
	attempt.AttemptedAt = time.Unix(0, attemptedAt).UTC()
	if nextAttemptAt.Valid {
		attempt.NextAttemptAt = time.Unix(0, nextAttemptAt.Int64).UTC()
	}
	attempt.DisableEndpoint = disableEndpoint == 1
	return attempt, nil
}

func validateAttempt(attempt delivery.Attempt) error {
	if attemptIdentityMissing(attempt) {
		return fmt.Errorf("%w: complete attempt identity and actor are required", delivery.ErrInvalid)
	}
	if attempt.Number < 1 || attempt.AttemptedAt.IsZero() {
		return fmt.Errorf("%w: positive attempt number and timestamp are required", delivery.ErrInvalid)
	}
	if attempt.WebhookTimestamp != attempt.AttemptedAt.Unix() {
		return fmt.Errorf("%w: webhook timestamp must match the attempt time", delivery.ErrInvalid)
	}
	if attempt.StatusCode < 0 {
		return fmt.Errorf("%w: status code cannot be negative", delivery.ErrInvalid)
	}
	return validateAttemptTransition(attempt)
}

func validateAttemptTransition(attempt delivery.Attempt) error {
	expected, known := attempt.Outcome.Transition()
	if !known {
		return invalidTransition(attempt)
	}
	if attempt.State != expected.State {
		return invalidTransition(attempt)
	}
	if attempt.DisableEndpoint != expected.DisableEndpoint {
		return invalidTransition(attempt)
	}
	if !attempt.NextAttemptAt.IsZero() != expected.RetryScheduled {
		return invalidTransition(attempt)
	}
	return nil
}

func attemptIdentityMissing(attempt delivery.Attempt) bool {
	return attempt.DeliveryID == "" ||
		attempt.MessageID == "" ||
		attempt.EndpointID == "" ||
		attempt.Actor == ""
}

func invalidTransition(attempt delivery.Attempt) error {
	return fmt.Errorf(
		"%w: outcome %s cannot transition to %s",
		delivery.ErrInvalid,
		attempt.Outcome,
		attempt.State,
	)
}

func nullTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC().UnixNano()
}

func boolValue(value bool) int {
	if value {
		return 1
	}
	return 0
}
