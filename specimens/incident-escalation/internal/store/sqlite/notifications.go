package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/incident"
)

func insertNotification(ctx context.Context, tx *sql.Tx, notification incident.Notification) error {
	_, err := tx.ExecContext(
		ctx,
		`INSERT INTO notifications
		 (id, incident_id, responder_id, channel, level, repeat, state, attempt_count, next_attempt_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		notification.ID,
		notification.IncidentID,
		notification.ResponderID,
		notification.Channel,
		notification.Level,
		notification.Repeat,
		notification.State,
		notification.AttemptCount,
		nullableTime(notification.NextAttemptAt),
		notification.CreatedAt.UTC().UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("insert notification %s: %w", notification.ID, err)
	}
	return nil
}

// DueNotifications joins pending pages to their responder contact and incident context.
func (s *Store) DueNotifications(ctx context.Context, now time.Time, limit int) ([]incident.Dispatch, error) {
	if now.IsZero() || limit < 1 || limit > 100 {
		return nil, fmt.Errorf("%w: due time and limit between 1 and 100 are required", incident.ErrInvalid)
	}
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT
			n.id, n.incident_id, n.responder_id, n.channel, n.level, n.repeat,
			n.state, n.attempt_count, n.next_attempt_at, n.created_at,
			r.email, r.webhook_url, r.webhook_secret, r.created_at,
			s.name, i.id, i.service_id, i.dedup_key, i.state, i.summary, i.source, i.severity, i.client,
			i.policy_id, i.level, i.repeat, i.escalate_at, i.revision, i.opened_at, i.updated_at
		 FROM notifications AS n
		 JOIN responders AS r ON r.id = n.responder_id
		 JOIN incidents AS i ON i.id = n.incident_id
		 JOIN services AS s ON s.id = i.service_id
		 WHERE n.state = ? AND n.next_attempt_at <= ?
		 ORDER BY n.next_attempt_at, n.created_at, n.id
		 LIMIT ?`,
		incident.NotificationPending,
		now.UTC().UnixNano(),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query due notifications: %w", err)
	}
	defer rows.Close()
	due := make([]incident.Dispatch, 0, limit)
	for rows.Next() {
		item, err := scanDispatch(rows)
		if err != nil {
			return nil, fmt.Errorf("scan due notification: %w", err)
		}
		due = append(due, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate due notifications: %w", err)
	}
	return due, nil
}

func scanDispatch(row rowScanner) (incident.Dispatch, error) {
	var item incident.Dispatch
	var nextAttemptAt, escalateAt sql.NullInt64
	var createdAt, responderCreatedAt, openedAt, updatedAt int64
	err := row.Scan(
		&item.Notification.ID,
		&item.Notification.IncidentID,
		&item.Notification.ResponderID,
		&item.Notification.Channel,
		&item.Notification.Level,
		&item.Notification.Repeat,
		&item.Notification.State,
		&item.Notification.AttemptCount,
		&nextAttemptAt,
		&createdAt,
		&item.Responder.Email,
		&item.Responder.WebhookURL,
		&item.Responder.WebhookSecret,
		&responderCreatedAt,
		&item.ServiceName,
		&item.Incident.ID,
		&item.Incident.ServiceID,
		&item.Incident.DedupKey,
		&item.Incident.State,
		&item.Incident.Summary,
		&item.Incident.Source,
		&item.Incident.Severity,
		&item.Incident.Client,
		&item.Incident.PolicyID,
		&item.Incident.Level,
		&item.Incident.Repeat,
		&escalateAt,
		&item.Incident.Revision,
		&openedAt,
		&updatedAt,
	)
	if err != nil {
		return incident.Dispatch{}, err
	}
	item.Notification.NextAttemptAt = timeOf(nextAttemptAt)
	item.Notification.CreatedAt = time.Unix(0, createdAt).UTC()
	item.Responder.ID = item.Notification.ResponderID
	item.Responder.CreatedAt = time.Unix(0, responderCreatedAt).UTC()
	item.Incident.EscalateAt = timeOf(escalateAt)
	item.Incident.OpenedAt = time.Unix(0, openedAt).UTC()
	item.Incident.UpdatedAt = time.Unix(0, updatedAt).UTC()
	return item, nil
}

// ClaimNotification leases one pending page until the given instant.
//
// The update is fenced on state, attempt count, and the observed due time, so
// two workers cannot both send the same attempt and a page whose audit write
// later fails waits for the lease instead of being re-sent every tick.
func (s *Store) ClaimNotification(ctx context.Context, notification incident.Notification, until time.Time) error {
	result, err := s.db.ExecContext(
		ctx,
		`UPDATE notifications SET next_attempt_at = ?
		 WHERE id = ? AND state = ? AND attempt_count = ? AND next_attempt_at = ?`,
		until.UTC().UnixNano(),
		notification.ID,
		incident.NotificationPending,
		notification.AttemptCount,
		nullableTime(notification.NextAttemptAt),
	)
	if err != nil {
		return fmt.Errorf("claim notification %s: %w", notification.ID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count notification %s claim: %w", notification.ID, err)
	}
	if affected != 1 {
		return fmt.Errorf("%w: notification %s was claimed elsewhere", incident.ErrConflict, notification.ID)
	}
	return nil
}

// RecordAttempt appends one audit row and applies its notification transition atomically.
func (s *Store) RecordAttempt(ctx context.Context, attempt incident.Attempt) error {
	if err := validateAttempt(attempt); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin notification %s attempt: %w", attempt.NotificationID, err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(
		ctx,
		`UPDATE notifications SET state = ?, attempt_count = ?, next_attempt_at = ?
		 WHERE id = ? AND state = ? AND attempt_count = ?`,
		attempt.State,
		attempt.Number,
		nullableTime(attempt.NextAttemptAt),
		attempt.NotificationID,
		incident.NotificationPending,
		attempt.Number-1,
	)
	if err != nil {
		return fmt.Errorf("advance notification %s: %w", attempt.NotificationID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count notification %s advance: %w", attempt.NotificationID, err)
	}
	if affected != 1 {
		return fmt.Errorf("%w: notification %s is not awaiting attempt %d", incident.ErrConflict, attempt.NotificationID, attempt.Number)
	}
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO notification_attempts
		 (notification_id, incident_id, responder_id, channel, number, outcome, error_text, attempted_at, next_attempt_at, state)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		attempt.NotificationID,
		attempt.IncidentID,
		attempt.ResponderID,
		attempt.Channel,
		attempt.Number,
		attempt.Outcome,
		attempt.Error,
		attempt.AttemptedAt.UTC().UnixNano(),
		nullableTime(attempt.NextAttemptAt),
		attempt.State,
	)
	if err != nil {
		return fmt.Errorf("append notification %s attempt %d: %w", attempt.NotificationID, attempt.Number, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit notification %s attempt: %w", attempt.NotificationID, err)
	}
	return nil
}

func validateAttempt(attempt incident.Attempt) error {
	if attempt.NotificationID == "" || attempt.Number < 1 || attempt.AttemptedAt.IsZero() {
		return fmt.Errorf("%w: attempt needs a notification, number, and time", incident.ErrInvalid)
	}
	pending := attempt.State == incident.NotificationPending
	if pending != !attempt.NextAttemptAt.IsZero() {
		return fmt.Errorf("%w: only a pending attempt carries a next attempt time", incident.ErrInvalid)
	}
	return nil
}

// Attempts lists the append-only page audit newest first.
func (s *Store) Attempts(ctx context.Context, filter incident.AttemptFilter, limit int) ([]incident.Attempt, error) {
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("%w: attempt limit must be between 1 and 1000", incident.ErrInvalid)
	}
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT sequence, notification_id, incident_id, responder_id, channel, number,
			outcome, error_text, attempted_at, next_attempt_at, state
		 FROM notification_attempts
		 WHERE (? = '' OR incident_id = ?) AND (? = '' OR notification_id = ?)
		 ORDER BY sequence DESC
		 LIMIT ?`,
		filter.IncidentID,
		filter.IncidentID,
		filter.NotificationID,
		filter.NotificationID,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query notification attempts: %w", err)
	}
	defer rows.Close()
	var attempts []incident.Attempt
	for rows.Next() {
		attempt, err := scanAttempt(rows)
		if err != nil {
			return nil, err
		}
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notification attempts: %w", err)
	}
	return attempts, nil
}

func scanAttempt(row rowScanner) (incident.Attempt, error) {
	var attempt incident.Attempt
	var attemptedAt int64
	var nextAttemptAt sql.NullInt64
	err := row.Scan(
		&attempt.Sequence,
		&attempt.NotificationID,
		&attempt.IncidentID,
		&attempt.ResponderID,
		&attempt.Channel,
		&attempt.Number,
		&attempt.Outcome,
		&attempt.Error,
		&attemptedAt,
		&nextAttemptAt,
		&attempt.State,
	)
	if err != nil {
		return incident.Attempt{}, fmt.Errorf("scan notification attempt: %w", err)
	}
	attempt.AttemptedAt = time.Unix(0, attemptedAt).UTC()
	attempt.NextAttemptAt = timeOf(nextAttemptAt)
	return attempt, nil
}

// Notifications lists one incident's pages in creation order.
func (s *Store) Notifications(ctx context.Context, incidentID string) ([]incident.Notification, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, incident_id, responder_id, channel, level, repeat, state, attempt_count, next_attempt_at, created_at
		 FROM notifications WHERE incident_id = ? ORDER BY created_at, id`,
		incidentID,
	)
	if err != nil {
		return nil, fmt.Errorf("query incident %s notifications: %w", incidentID, err)
	}
	defer rows.Close()
	var notifications []incident.Notification
	for rows.Next() {
		var notification incident.Notification
		var nextAttemptAt sql.NullInt64
		var createdAt int64
		if err := rows.Scan(
			&notification.ID,
			&notification.IncidentID,
			&notification.ResponderID,
			&notification.Channel,
			&notification.Level,
			&notification.Repeat,
			&notification.State,
			&notification.AttemptCount,
			&nextAttemptAt,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan notification: %w", err)
		}
		notification.NextAttemptAt = timeOf(nextAttemptAt)
		notification.CreatedAt = time.Unix(0, createdAt).UTC()
		notifications = append(notifications, notification)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notifications: %w", err)
	}
	return notifications, nil
}
