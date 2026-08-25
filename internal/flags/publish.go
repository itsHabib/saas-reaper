package flags

import (
	"context"
	"fmt"
	"strings"
)

// Store is the durable authority required by flag policy.
type Store interface {
	Load(context.Context) (map[string][]Flag, error)
	Publish(context.Context, string, Flag, int64, string) (Flag, error)
	Audit(context.Context, int) ([]AuditEntry, error)
}

// Service composes durable publication with the current evaluation projection.
type Service struct {
	store    Store
	snapshot Snapshot
}

// Open reconstructs the evaluation projection from durable state.
func Open(ctx context.Context, store Store, snapshot Snapshot) (*Service, error) {
	loaded, err := store.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("load flags: %w", err)
	}
	snapshot.Replace(loaded)
	return &Service{store: store, snapshot: snapshot}, nil
}

// Publish validates, durably revisions, audits, and projects one definition.
func (s *Service) Publish(
	ctx context.Context,
	environment string,
	flag Flag,
	expectedRevision int64,
	actor string,
) (Flag, error) {
	if err := validateEnvironment(environment); err != nil {
		return Flag{}, err
	}
	if err := flag.Validate(); err != nil {
		return Flag{}, err
	}
	if expectedRevision < 0 {
		return Flag{}, fmt.Errorf("%w: expected revision cannot be negative", ErrInvalid)
	}
	if strings.TrimSpace(actor) == "" {
		return Flag{}, fmt.Errorf("%w: actor is required", ErrInvalid)
	}
	published, err := s.store.Publish(ctx, environment, flag, expectedRevision, actor)
	if err != nil {
		return Flag{}, err
	}
	s.snapshot.Put(environment, published)
	return published, nil
}

func validateEnvironment(environment string) error {
	if strings.TrimSpace(environment) == "" {
		return fmt.Errorf("%w: environment is required", ErrInvalid)
	}
	return validateKey(environment)
}
