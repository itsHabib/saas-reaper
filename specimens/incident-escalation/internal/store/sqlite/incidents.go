package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/incident"
)

const incidentColumns = `id, service_id, dedup_key, state, summary, source, severity, client,
	policy_id, level, repeat, escalate_at, revision, opened_at, updated_at`

// Incident loads one record by identity.
func (s *Store) Incident(ctx context.Context, id string) (incident.Incident, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+incidentColumns+` FROM incidents WHERE id = ?`, id)
	loaded, err := scanIncident(row)
	if errors.Is(err, sql.ErrNoRows) {
		return incident.Incident{}, fmt.Errorf("%w: incident %s", incident.ErrNotFound, id)
	}
	if err != nil {
		return incident.Incident{}, fmt.Errorf("load incident %s: %w", id, err)
	}
	return loaded, nil
}

// OpenIncident finds the one unresolved incident for a service and dedup key.
func (s *Store) OpenIncident(ctx context.Context, serviceID, dedupKey string) (incident.Incident, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT `+incidentColumns+` FROM incidents
		 WHERE service_id = ? AND dedup_key = ? AND state <> ?`,
		serviceID,
		dedupKey,
		incident.StateResolved,
	)
	loaded, err := scanIncident(row)
	if errors.Is(err, sql.ErrNoRows) {
		return incident.Incident{}, fmt.Errorf("%w: open incident for dedup key", incident.ErrNotFound)
	}
	if err != nil {
		return incident.Incident{}, fmt.Errorf("load open incident: %w", err)
	}
	return loaded, nil
}

// Incidents lists newest first, optionally narrowed by service and state.
func (s *Store) Incidents(ctx context.Context, filter incident.IncidentFilter, limit int) ([]incident.Incident, error) {
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("%w: incident limit must be between 1 and 1000", incident.ErrInvalid)
	}
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT `+incidentColumns+` FROM incidents
		 WHERE (? = '' OR service_id = ?) AND (? = '' OR state = ?)
		 ORDER BY opened_at DESC, id DESC
		 LIMIT ?`,
		filter.ServiceID,
		filter.ServiceID,
		string(filter.State),
		string(filter.State),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query incidents: %w", err)
	}
	defer rows.Close()
	return collectIncidents(rows)
}

// DueEscalations lists triggered incidents whose durable timer has passed, oldest first.
func (s *Store) DueEscalations(ctx context.Context, now time.Time, limit int) ([]incident.Incident, error) {
	if now.IsZero() || limit < 1 || limit > 100 {
		return nil, fmt.Errorf("%w: due time and limit between 1 and 100 are required", incident.ErrInvalid)
	}
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT `+incidentColumns+` FROM incidents
		 WHERE state = ? AND escalate_at IS NOT NULL AND escalate_at <= ?
		 ORDER BY escalate_at, id
		 LIMIT ?`,
		incident.StateTriggered,
		now.UTC().UnixNano(),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query due escalations: %w", err)
	}
	defer rows.Close()
	return collectIncidents(rows)
}

func collectIncidents(rows *sql.Rows) ([]incident.Incident, error) {
	var incidents []incident.Incident
	for rows.Next() {
		loaded, err := scanIncident(rows)
		if err != nil {
			return nil, fmt.Errorf("scan incident: %w", err)
		}
		incidents = append(incidents, loaded)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incidents: %w", err)
	}
	return incidents, nil
}

func scanIncident(row rowScanner) (incident.Incident, error) {
	var loaded incident.Incident
	var escalateAt sql.NullInt64
	var openedAt, updatedAt int64
	err := row.Scan(
		&loaded.ID,
		&loaded.ServiceID,
		&loaded.DedupKey,
		&loaded.State,
		&loaded.Summary,
		&loaded.Source,
		&loaded.Severity,
		&loaded.Client,
		&loaded.PolicyID,
		&loaded.Level,
		&loaded.Repeat,
		&escalateAt,
		&loaded.Revision,
		&openedAt,
		&updatedAt,
	)
	if err != nil {
		return incident.Incident{}, err
	}
	loaded.EscalateAt = timeOf(escalateAt)
	loaded.OpenedAt = time.Unix(0, openedAt).UTC()
	loaded.UpdatedAt = time.Unix(0, updatedAt).UTC()
	return loaded, nil
}

// CreateIncident inserts the incident, its opening journal row, and its first pages atomically.
func (s *Store) CreateIncident(
	ctx context.Context,
	opened incident.Incident,
	event incident.Event,
	notifications []incident.Notification,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin incident %s: %w", opened.ID, err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO incidents (`+incidentColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		opened.ID,
		opened.ServiceID,
		opened.DedupKey,
		opened.State,
		opened.Summary,
		opened.Source,
		opened.Severity,
		opened.Client,
		opened.PolicyID,
		opened.Level,
		opened.Repeat,
		nullableTime(opened.EscalateAt),
		opened.Revision,
		opened.OpenedAt.UTC().UnixNano(),
		opened.UpdatedAt.UTC().UnixNano(),
	)
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: an incident is already open for this dedup key", incident.ErrConflict)
	}
	if err != nil {
		return fmt.Errorf("insert incident %s: %w", opened.ID, err)
	}
	if err := appendJournal(ctx, tx, event, notifications); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit incident %s: %w", opened.ID, err)
	}
	return nil
}

// Transition applies one lifecycle step, its journal row, and any new pages atomically.
//
// The update is fenced on the expected revision so a concurrent step is a
// conflict rather than a silent overwrite.
func (s *Store) Transition(
	ctx context.Context,
	next incident.Incident,
	expectedRevision int64,
	event incident.Event,
	notifications []incident.Notification,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin incident %s transition: %w", next.ID, err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(
		ctx,
		`UPDATE incidents
		 SET state = ?, level = ?, repeat = ?, escalate_at = ?, revision = ?, updated_at = ?
		 WHERE id = ? AND revision = ?`,
		next.State,
		next.Level,
		next.Repeat,
		nullableTime(next.EscalateAt),
		next.Revision,
		next.UpdatedAt.UTC().UnixNano(),
		next.ID,
		expectedRevision,
	)
	if err != nil {
		return fmt.Errorf("update incident %s: %w", next.ID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count incident %s update: %w", next.ID, err)
	}
	if affected != 1 {
		return fmt.Errorf("%w: incident %s changed since revision %d", incident.ErrConflict, next.ID, expectedRevision)
	}
	if err := appendJournal(ctx, tx, event, notifications); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit incident %s transition: %w", next.ID, err)
	}
	return nil
}

func appendJournal(ctx context.Context, tx *sql.Tx, event incident.Event, notifications []incident.Notification) error {
	_, err := tx.ExecContext(
		ctx,
		`INSERT INTO incident_events (incident_id, kind, actor, level, repeat, detail, at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		event.IncidentID,
		event.Kind,
		event.Actor,
		event.Level,
		event.Repeat,
		event.Detail,
		event.At.UTC().UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("append incident %s journal: %w", event.IncidentID, err)
	}
	for _, notification := range notifications {
		if err := insertNotification(ctx, tx, notification); err != nil {
			return err
		}
	}
	return nil
}

// Events lists one incident's journal in append order.
func (s *Store) Events(ctx context.Context, incidentID string) ([]incident.Event, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT sequence, incident_id, kind, actor, level, repeat, detail, at
		 FROM incident_events WHERE incident_id = ? ORDER BY sequence`,
		incidentID,
	)
	if err != nil {
		return nil, fmt.Errorf("query incident %s events: %w", incidentID, err)
	}
	defer rows.Close()
	var events []incident.Event
	for rows.Next() {
		var event incident.Event
		var at int64
		if err := rows.Scan(
			&event.Sequence,
			&event.IncidentID,
			&event.Kind,
			&event.Actor,
			&event.Level,
			&event.Repeat,
			&event.Detail,
			&at,
		); err != nil {
			return nil, fmt.Errorf("scan incident event: %w", err)
		}
		event.At = time.Unix(0, at).UTC()
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incident events: %w", err)
	}
	return events, nil
}
