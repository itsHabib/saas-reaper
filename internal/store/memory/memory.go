package memory

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/itsHabib/saas-reaper-poc/internal/flags"
)

// Store retains definitions and audit entries in process.
type Store struct {
	mu       sync.Mutex
	flags    map[string]map[string]flags.Flag
	audit    []flags.AuditEntry
	sequence int64
}

// New returns an empty in-memory store.
func New() *Store {
	return &Store{flags: make(map[string]map[string]flags.Flag)}
}

// Load returns independent definitions grouped by environment.
func (s *Store) Load(context.Context) (map[string][]flags.Flag, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	loaded := make(map[string][]flags.Flag, len(s.flags))
	for environment, current := range s.flags {
		for _, flag := range current {
			loaded[environment] = append(loaded[environment], flag.Copy())
		}
		sort.Slice(loaded[environment], func(i, j int) bool {
			return loaded[environment][i].Key < loaded[environment][j].Key
		})
	}
	return loaded, nil
}

// Publish compares the expected revision and records the next revision and audit.
func (s *Store) Publish(
	_ context.Context,
	environment string,
	flag flags.Flag,
	expectedRevision int64,
	actor string,
) (flags.Flag, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.flags[environment]
	if current == nil {
		current = make(map[string]flags.Flag)
		s.flags[environment] = current
	}
	previous, exists := current[flag.Key]
	if !exists && expectedRevision != 0 {
		return flags.Flag{}, fmt.Errorf("%w: expected %d, current 0", flags.ErrConflict, expectedRevision)
	}
	if exists && previous.Revision != expectedRevision {
		return flags.Flag{}, fmt.Errorf("%w: expected %d, current %d", flags.ErrConflict, expectedRevision, previous.Revision)
	}
	flag.Revision = previous.Revision + 1
	flag.UpdatedAt = time.Now().UTC()
	current[flag.Key] = flag.Copy()
	s.sequence++
	s.audit = append(s.audit, flags.AuditEntry{
		Sequence:    s.sequence,
		Environment: environment,
		Key:         flag.Key,
		Revision:    flag.Revision,
		Actor:       actor,
		Action:      "published",
		OccurredAt:  flag.UpdatedAt,
	})
	return flag.Copy(), nil
}

// Audit returns the most recent entries first.
func (s *Store) Audit(_ context.Context, limit int) ([]flags.AuditEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit > len(s.audit) {
		limit = len(s.audit)
	}
	entries := make([]flags.AuditEntry, 0, limit)
	for i := len(s.audit) - 1; i >= len(s.audit)-limit; i-- {
		entries = append(entries, s.audit[i])
	}
	return entries, nil
}
