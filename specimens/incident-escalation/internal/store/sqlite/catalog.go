package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/incident"
	"github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal/oncall"
)

// CreateResponder inserts one responder; duplicates conflict.
func (s *Store) CreateResponder(ctx context.Context, responder incident.Responder) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO responders (id, email, webhook_url, webhook_secret, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		responder.ID,
		responder.Email,
		responder.WebhookURL,
		responder.WebhookSecret,
		responder.CreatedAt.UTC().UnixNano(),
	)
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: responder %s already exists", incident.ErrConflict, responder.ID)
	}
	if err != nil {
		return fmt.Errorf("insert responder %s: %w", responder.ID, err)
	}
	return nil
}

// CreateSchedule inserts one declaration after checking every named responder exists.
func (s *Store) CreateSchedule(
	ctx context.Context,
	id string,
	name string,
	schedule oncall.Schedule,
	createdAt time.Time,
) error {
	definition, err := json.Marshal(schedule)
	if err != nil {
		return fmt.Errorf("encode schedule %s: %w", id, err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin schedule %s: %w", id, err)
	}
	defer tx.Rollback()
	if err := requireResponders(ctx, tx, schedule.Responders()); err != nil {
		return err
	}
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO schedules (id, name, definition, created_at) VALUES (?, ?, ?, ?)`,
		id,
		name,
		string(definition),
		createdAt.UTC().UnixNano(),
	)
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: schedule %s already exists", incident.ErrConflict, id)
	}
	if err != nil {
		return fmt.Errorf("insert schedule %s: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schedule %s: %w", id, err)
	}
	return nil
}

// Schedule loads one declaration.
func (s *Store) Schedule(ctx context.Context, id string) (oncall.Schedule, error) {
	var definition string
	err := s.db.QueryRowContext(ctx, `SELECT definition FROM schedules WHERE id = ?`, id).Scan(&definition)
	if errors.Is(err, sql.ErrNoRows) {
		return oncall.Schedule{}, fmt.Errorf("%w: schedule %s", incident.ErrNotFound, id)
	}
	if err != nil {
		return oncall.Schedule{}, fmt.Errorf("load schedule %s: %w", id, err)
	}
	return decodeSchedule(id, definition)
}

func decodeSchedule(id, definition string) (oncall.Schedule, error) {
	var schedule oncall.Schedule
	if err := json.Unmarshal([]byte(definition), &schedule); err != nil {
		return oncall.Schedule{}, fmt.Errorf("decode schedule %s: %w", id, err)
	}
	return schedule, nil
}

// CreatePolicy inserts one ladder after checking every referenced schedule and responder exists.
func (s *Store) CreatePolicy(ctx context.Context, policy incident.EscalationPolicy) error {
	levels, err := json.Marshal(policy.Levels)
	if err != nil {
		return fmt.Errorf("encode policy %s: %w", policy.ID, err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin policy %s: %w", policy.ID, err)
	}
	defer tx.Rollback()
	for _, level := range policy.Levels {
		if err := requireResponders(ctx, tx, level.Responders); err != nil {
			return err
		}
		if err := requireSchedules(ctx, tx, level.Schedules); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO escalation_policies (id, name, repeat, levels, created_at) VALUES (?, ?, ?, ?, ?)`,
		policy.ID,
		policy.Name,
		policy.Repeat,
		string(levels),
		policy.CreatedAt.UTC().UnixNano(),
	)
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: escalation policy %s already exists", incident.ErrConflict, policy.ID)
	}
	if err != nil {
		return fmt.Errorf("insert policy %s: %w", policy.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit policy %s: %w", policy.ID, err)
	}
	return nil
}

// CreateService inserts one monitored system bound to an existing policy.
func (s *Store) CreateService(ctx context.Context, service incident.Service) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO services (id, name, routing_key, policy_id, created_at) VALUES (?, ?, ?, ?, ?)`,
		service.ID,
		service.Name,
		service.RoutingKey,
		service.PolicyID,
		service.CreatedAt.UTC().UnixNano(),
	)
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: service %s already exists", incident.ErrConflict, service.ID)
	}
	if isForeignKeyViolation(err) {
		return fmt.Errorf("%w: escalation policy %s", incident.ErrNotFound, service.PolicyID)
	}
	if err != nil {
		return fmt.Errorf("insert service %s: %w", service.ID, err)
	}
	return nil
}

// ServiceByRoutingKey resolves the ingest credential to its service.
func (s *Store) ServiceByRoutingKey(ctx context.Context, routingKey string) (incident.Service, error) {
	var service incident.Service
	var createdAt int64
	err := s.db.QueryRowContext(
		ctx,
		`SELECT id, name, routing_key, policy_id, created_at FROM services WHERE routing_key = ?`,
		routingKey,
	).Scan(&service.ID, &service.Name, &service.RoutingKey, &service.PolicyID, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return incident.Service{}, fmt.Errorf("%w: routing key", incident.ErrNotFound)
	}
	if err != nil {
		return incident.Service{}, fmt.Errorf("load service by routing key: %w", err)
	}
	service.CreatedAt = time.Unix(0, createdAt).UTC()
	return service, nil
}

// Targets loads a policy with every schedule and responder it can page.
func (s *Store) Targets(ctx context.Context, policyID string) (incident.Targets, error) {
	policy, err := s.policy(ctx, policyID)
	if err != nil {
		return incident.Targets{}, err
	}
	targets := incident.Targets{
		Policy:     policy,
		Schedules:  map[string]oncall.Schedule{},
		Responders: map[string]incident.Responder{},
	}
	responderIDs := map[string]bool{}
	for _, level := range policy.Levels {
		for _, responderID := range level.Responders {
			responderIDs[responderID] = true
		}
		if err := s.loadLevelSchedules(ctx, level.Schedules, targets.Schedules, responderIDs); err != nil {
			return incident.Targets{}, err
		}
	}
	for responderID := range responderIDs {
		responder, err := s.responder(ctx, responderID)
		if err != nil {
			return incident.Targets{}, err
		}
		targets.Responders[responderID] = responder
	}
	return targets, nil
}

func (s *Store) loadLevelSchedules(
	ctx context.Context,
	ids []string,
	schedules map[string]oncall.Schedule,
	responderIDs map[string]bool,
) error {
	for _, scheduleID := range ids {
		schedule, err := s.Schedule(ctx, scheduleID)
		if err != nil {
			return err
		}
		schedules[scheduleID] = schedule
		for _, responderID := range schedule.Responders() {
			responderIDs[responderID] = true
		}
	}
	return nil
}

func (s *Store) policy(ctx context.Context, id string) (incident.EscalationPolicy, error) {
	policy := incident.EscalationPolicy{ID: id}
	var levels string
	var createdAt int64
	err := s.db.QueryRowContext(
		ctx,
		`SELECT name, repeat, levels, created_at FROM escalation_policies WHERE id = ?`,
		id,
	).Scan(&policy.Name, &policy.Repeat, &levels, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return incident.EscalationPolicy{}, fmt.Errorf("%w: escalation policy %s", incident.ErrNotFound, id)
	}
	if err != nil {
		return incident.EscalationPolicy{}, fmt.Errorf("load policy %s: %w", id, err)
	}
	if err := json.Unmarshal([]byte(levels), &policy.Levels); err != nil {
		return incident.EscalationPolicy{}, fmt.Errorf("decode policy %s levels: %w", id, err)
	}
	policy.CreatedAt = time.Unix(0, createdAt).UTC()
	return policy, nil
}

func (s *Store) responder(ctx context.Context, id string) (incident.Responder, error) {
	responder := incident.Responder{ID: id}
	var createdAt int64
	err := s.db.QueryRowContext(
		ctx,
		`SELECT email, webhook_url, webhook_secret, created_at FROM responders WHERE id = ?`,
		id,
	).Scan(&responder.Email, &responder.WebhookURL, &responder.WebhookSecret, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return incident.Responder{}, fmt.Errorf("%w: responder %s", incident.ErrNotFound, id)
	}
	if err != nil {
		return incident.Responder{}, fmt.Errorf("load responder %s: %w", id, err)
	}
	responder.CreatedAt = time.Unix(0, createdAt).UTC()
	return responder, nil
}

func requireResponders(ctx context.Context, tx *sql.Tx, ids []string) error {
	return requireRows(ctx, tx, `SELECT COUNT(*) FROM responders WHERE id = ?`, "responder", ids)
}

func requireSchedules(ctx context.Context, tx *sql.Tx, ids []string) error {
	return requireRows(ctx, tx, `SELECT COUNT(*) FROM schedules WHERE id = ?`, "schedule", ids)
}

func requireRows(ctx context.Context, tx *sql.Tx, query, kind string, ids []string) error {
	for _, id := range ids {
		var count int
		if err := tx.QueryRowContext(ctx, query, id).Scan(&count); err != nil {
			return fmt.Errorf("check %s %s: %w", kind, id, err)
		}
		if count == 0 {
			return fmt.Errorf("%w: %s %s", incident.ErrNotFound, kind, id)
		}
	}
	return nil
}

func isForeignKeyViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "FOREIGN KEY constraint failed")
}
