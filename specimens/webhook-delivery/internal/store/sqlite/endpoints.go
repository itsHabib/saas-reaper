package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/webhook-delivery/internal/delivery"
)

// RegisterEndpoint atomically creates the first endpoint revision.
func (s *Store) RegisterEndpoint(
	ctx context.Context,
	endpoint delivery.Endpoint,
	expectedRevision int64,
) (delivery.Endpoint, error) {
	if expectedRevision != 0 {
		return delivery.Endpoint{}, revisionConflict(endpoint.ID, expectedRevision, 0)
	}
	if err := validateFirstEndpoint(endpoint); err != nil {
		return delivery.Endpoint{}, err
	}
	result, err := s.db.ExecContext(
		ctx,
		`INSERT INTO endpoints (id, url, secret, enabled, revision, created_at, updated_at)
		 VALUES (?, ?, ?, 1, 1, ?, ?)
		 ON CONFLICT(id) DO NOTHING`,
		endpoint.ID,
		endpoint.URL,
		endpoint.Secret,
		endpoint.CreatedAt.UnixNano(),
		endpoint.UpdatedAt.UnixNano(),
	)
	if err != nil {
		return delivery.Endpoint{}, fmt.Errorf("register endpoint %s: %w", endpoint.ID, err)
	}
	written, err := result.RowsAffected()
	if err != nil {
		return delivery.Endpoint{}, fmt.Errorf("count registered endpoint %s: %w", endpoint.ID, err)
	}
	if written != 1 {
		return delivery.Endpoint{}, fmt.Errorf("%w: endpoint %s already exists", delivery.ErrConflict, endpoint.ID)
	}
	return endpoint, nil
}

// DisableEndpoint revisions an endpoint and cancels its pending deliveries in one transaction.
func (s *Store) DisableEndpoint(
	ctx context.Context,
	id string,
	expectedRevision int64,
	at time.Time,
) (delivery.Endpoint, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return delivery.Endpoint{}, fmt.Errorf("begin disable endpoint %s: %w", id, err)
	}
	defer tx.Rollback()
	current, err := endpointByID(ctx, tx, id)
	if err != nil {
		return delivery.Endpoint{}, err
	}
	if !current.Enabled {
		return delivery.Endpoint{}, fmt.Errorf("%w: endpoint %s", delivery.ErrDisabled, id)
	}
	if current.Revision != expectedRevision {
		return delivery.Endpoint{}, revisionConflict(id, expectedRevision, current.Revision)
	}
	updatedAt := at.UTC()
	result, err := tx.ExecContext(
		ctx,
		`UPDATE endpoints
		 SET enabled = 0, revision = revision + 1, updated_at = ?
		 WHERE id = ? AND enabled = 1 AND revision = ?`,
		updatedAt.UnixNano(),
		id,
		expectedRevision,
	)
	if err != nil {
		return delivery.Endpoint{}, fmt.Errorf("disable endpoint %s: %w", id, err)
	}
	written, err := result.RowsAffected()
	if err != nil {
		return delivery.Endpoint{}, fmt.Errorf("count disabled endpoint %s: %w", id, err)
	}
	if written != 1 {
		return delivery.Endpoint{}, fmt.Errorf("%w: endpoint %s changed during disable", delivery.ErrConflict, id)
	}
	if _, err := tx.ExecContext(
		ctx,
		`UPDATE deliveries
		 SET state = ?, next_attempt_at = NULL
		 WHERE endpoint_id = ? AND state = ?`,
		delivery.StateDisabled,
		id,
		delivery.StatePending,
	); err != nil {
		return delivery.Endpoint{}, fmt.Errorf("cancel endpoint %s deliveries: %w", id, err)
	}
	current.Enabled = false
	current.Revision++
	current.UpdatedAt = updatedAt
	if err := tx.Commit(); err != nil {
		return delivery.Endpoint{}, fmt.Errorf("commit disable endpoint %s: %w", id, err)
	}
	return current, nil
}

// ListEndpoints returns every endpoint in stable creation order.
func (s *Store) ListEndpoints(ctx context.Context) ([]delivery.Endpoint, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, url, secret, enabled, revision, created_at, updated_at
		 FROM endpoints ORDER BY created_at, id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list endpoints: %w", err)
	}
	defer rows.Close()
	endpoints := make([]delivery.Endpoint, 0)
	for rows.Next() {
		endpoint, err := scanEndpoint(rows)
		if err != nil {
			return nil, err
		}
		endpoints = append(endpoints, endpoint)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate endpoints: %w", err)
	}
	return endpoints, nil
}

// Endpoint returns one endpoint, including its private signing secret.
func (s *Store) Endpoint(ctx context.Context, id string) (delivery.Endpoint, error) {
	return endpointByID(ctx, s.db, id)
}

func endpointByID(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (delivery.Endpoint, error) {
	row := queryer.QueryRowContext(
		ctx,
		`SELECT id, url, secret, enabled, revision, created_at, updated_at
		 FROM endpoints WHERE id = ?`,
		id,
	)
	endpoint, err := scanEndpoint(row)
	if errors.Is(err, sql.ErrNoRows) {
		return delivery.Endpoint{}, fmt.Errorf("%w: endpoint %s", delivery.ErrNotFound, id)
	}
	return endpoint, err
}

func scanEndpoint(row rowScanner) (delivery.Endpoint, error) {
	var endpoint delivery.Endpoint
	var enabled int
	var createdAt int64
	var updatedAt int64
	if err := row.Scan(
		&endpoint.ID,
		&endpoint.URL,
		&endpoint.Secret,
		&enabled,
		&endpoint.Revision,
		&createdAt,
		&updatedAt,
	); err != nil {
		return delivery.Endpoint{}, err
	}
	endpoint.Enabled = enabled == 1
	endpoint.CreatedAt = time.Unix(0, createdAt).UTC()
	endpoint.UpdatedAt = time.Unix(0, updatedAt).UTC()
	return endpoint, nil
}

func validateFirstEndpoint(endpoint delivery.Endpoint) error {
	if endpoint.ID == "" || endpoint.URL == "" || endpoint.Secret == "" {
		return fmt.Errorf("%w: endpoint identity, URL, and secret are required", delivery.ErrInvalid)
	}
	if !endpoint.Enabled || endpoint.Revision != 1 {
		return fmt.Errorf("%w: registered endpoint must be enabled at revision 1", delivery.ErrInvalid)
	}
	if endpoint.CreatedAt.IsZero() || endpoint.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: endpoint timestamps are required", delivery.ErrInvalid)
	}
	return nil
}

func revisionConflict(id string, expected, current int64) error {
	return fmt.Errorf(
		"%w: endpoint %s expected revision %d, current %d",
		delivery.ErrConflict,
		id,
		expected,
		current,
	)
}
