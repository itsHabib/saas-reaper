package ledger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Appender runs one all-or-nothing append transaction. The mechanism owns
// serialization and durability; the policy inside apply owns every decision.
type Appender interface {
	Append(ctx context.Context, apply func(AppendTransaction) error) error
}

// AppendTransaction is the narrow view of one open append transaction.
type AppendTransaction interface {
	Head(ctx context.Context, tenant string) (Head, error)
	Existing(ctx context.Context, tenant, id string) (Entry, bool, error)
	Insert(ctx context.Context, entry Entry) error
}

// Service appends validated events to tenant chains idempotently.
type Service struct {
	store  Appender
	now    func() time.Time
	source string
}

// NewService composes ledger policy with its append mechanism, clock, and the
// configured write principal recorded as every entry's source.
func NewService(store Appender, now func() time.Time, source string) (*Service, error) {
	if store == nil || now == nil {
		return nil, errors.New("ledger store and clock are required")
	}
	if err := validateField("source", source); err != nil {
		return nil, fmt.Errorf("ledger source principal: %w", err)
	}
	return &Service{store: store, now: now, source: source}, nil
}

// Append validates every event first, then records the whole batch in one
// transaction. A replayed (tenant, id) with identical content returns its
// original position without appending; a replay with different content fails
// the whole batch with ErrConflict.
func (s *Service) Append(ctx context.Context, events []Event) ([]Receipt, error) {
	if len(events) == 0 || len(events) > MaxBatch {
		return nil, fmt.Errorf("%w: a batch holds 1-%d events", ErrInvalid, MaxBatch)
	}
	normalized := make([]Event, 0, len(events))
	for index, event := range events {
		clean, err := Normalize(event)
		if err != nil {
			return nil, fmt.Errorf("event %d: %w", index, err)
		}
		normalized = append(normalized, clean)
	}
	recordedAt := FormatTime(s.now())
	var receipts []Receipt
	err := s.store.Append(ctx, func(tx AppendTransaction) error {
		batch := appendBatch{tx: tx, heads: map[string]Head{}, seen: map[string]Entry{}}
		for _, event := range normalized {
			receipt, err := batch.record(ctx, event, recordedAt, s.source)
			if err != nil {
				return err
			}
			receipts = append(receipts, receipt)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return receipts, nil
}

type appendBatch struct {
	tx    AppendTransaction
	heads map[string]Head
	seen  map[string]Entry
}

func (b *appendBatch) record(ctx context.Context, event Event, recordedAt, source string) (Receipt, error) {
	existing, found, err := b.lookup(ctx, event)
	if err != nil {
		return Receipt{}, err
	}
	if found {
		return replayReceipt(existing, event)
	}
	head, err := b.head(ctx, event.Tenant)
	if err != nil {
		return Receipt{}, err
	}
	entry := Entry{
		Tenant:       event.Tenant,
		Sequence:     head.Sequence + 1,
		ID:           event.ID,
		Actor:        event.Actor,
		Action:       event.Action,
		Target:       event.Target,
		OccurredAt:   event.OccurredAt,
		RecordedAt:   recordedAt,
		Source:       source,
		Metadata:     event.Metadata,
		PreviousHash: head.Hash,
	}
	entry.Hash, err = Link(head.Hash, entry)
	if err != nil {
		return Receipt{}, fmt.Errorf("link %s/%d: %w", entry.Tenant, entry.Sequence, err)
	}
	if err := b.tx.Insert(ctx, entry); err != nil {
		return Receipt{}, err
	}
	b.heads[entry.Tenant] = Head{Sequence: entry.Sequence, Hash: entry.Hash}
	b.seen[replayKey(entry.Tenant, entry.ID)] = entry
	return Receipt{Tenant: entry.Tenant, ID: entry.ID, Sequence: entry.Sequence, Hash: entry.Hash}, nil
}

func (b *appendBatch) lookup(ctx context.Context, event Event) (Entry, bool, error) {
	if entry, ok := b.seen[replayKey(event.Tenant, event.ID)]; ok {
		return entry, true, nil
	}
	return b.tx.Existing(ctx, event.Tenant, event.ID)
}

func (b *appendBatch) head(ctx context.Context, tenant string) (Head, error) {
	if head, ok := b.heads[tenant]; ok {
		return head, nil
	}
	head, err := b.tx.Head(ctx, tenant)
	if err != nil {
		return Head{}, err
	}
	if head.Sequence == 0 {
		head.Hash = GenesisHash
	}
	b.heads[tenant] = head
	return head, nil
}

func replayReceipt(existing Entry, event Event) (Receipt, error) {
	if !sameContent(existing, event) {
		return Receipt{}, fmt.Errorf("%w: event %s/%s was recorded with different content at sequence %d",
			ErrConflict, existing.Tenant, existing.ID, existing.Sequence)
	}
	return Receipt{
		Tenant:   existing.Tenant,
		ID:       existing.ID,
		Sequence: existing.Sequence,
		Hash:     existing.Hash,
		Replayed: true,
	}, nil
}

func sameContent(existing Entry, event Event) bool {
	return existing.Actor == event.Actor &&
		existing.Action == event.Action &&
		existing.Target == event.Target &&
		existing.OccurredAt == event.OccurredAt &&
		strings.TrimSpace(string(existing.Metadata)) == string(event.Metadata)
}

func replayKey(tenant, id string) string {
	return tenant + "\x00" + id
}
