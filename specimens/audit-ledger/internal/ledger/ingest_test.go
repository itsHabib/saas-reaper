package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// memoryAppender is a policy-test double: it applies the transaction against an
// in-memory map and discards every write when apply fails.
type memoryAppender struct {
	entries map[string][]Entry
}

func (m *memoryAppender) Append(_ context.Context, apply func(AppendTransaction) error) error {
	staged := &memoryTransaction{entries: map[string][]Entry{}}
	for tenant, chain := range m.entries {
		staged.entries[tenant] = append([]Entry(nil), chain...)
	}
	if err := apply(staged); err != nil {
		return err
	}
	m.entries = staged.entries
	return nil
}

type memoryTransaction struct {
	entries map[string][]Entry
}

func (t *memoryTransaction) Head(_ context.Context, tenant string) (Head, error) {
	chain := t.entries[tenant]
	if len(chain) == 0 {
		return Head{}, nil
	}
	last := chain[len(chain)-1]
	return Head{Sequence: last.Sequence, Hash: last.Hash}, nil
}

func (t *memoryTransaction) Existing(_ context.Context, tenant, id string) (Entry, bool, error) {
	for _, entry := range t.entries[tenant] {
		if entry.ID == id {
			return entry, true, nil
		}
	}
	return Entry{}, false, nil
}

func (t *memoryTransaction) Insert(_ context.Context, entry Entry) error {
	t.entries[entry.Tenant] = append(t.entries[entry.Tenant], entry)
	return nil
}

func fixedClock() time.Time {
	return time.Date(2026, 8, 30, 12, 0, 0, 500_000_000, time.UTC)
}

func sample(tenant, id string) Event {
	return Event{
		Tenant:     tenant,
		ID:         id,
		Actor:      "user:ada",
		Action:     "document.viewed",
		Target:     "document:" + id,
		OccurredAt: "2026-08-30T11:00:00+01:00",
		Metadata:   json.RawMessage(`{"b": 2, "a": 1}`),
	}
}

func newTestService(t *testing.T) (*Service, *memoryAppender) {
	t.Helper()
	store := &memoryAppender{entries: map[string][]Entry{}}
	service, err := NewService(store, fixedClock, "test-ingest")
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return service, store
}

func TestAppendChainsPerTenantAndVerifies(t *testing.T) {
	service, store := newTestService(t)
	receipts, err := service.Append(context.Background(), []Event{
		sample("acme", "evt-1"), sample("globex", "evt-1"), sample("acme", "evt-2"),
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	want := []Receipt{
		{Tenant: "acme", ID: "evt-1", Sequence: 1},
		{Tenant: "globex", ID: "evt-1", Sequence: 1},
		{Tenant: "acme", ID: "evt-2", Sequence: 2},
	}
	for index, receipt := range receipts {
		if receipt.Tenant != want[index].Tenant || receipt.Sequence != want[index].Sequence || receipt.Replayed {
			t.Fatalf("receipt %d = %+v, want %+v", index, receipt, want[index])
		}
	}
	for tenant, chain := range store.entries {
		head, err := Verify(chain)
		if err != nil {
			t.Fatalf("%s chain: %v", tenant, err)
		}
		if head.Sequence != int64(len(chain)) {
			t.Fatalf("%s head %+v, want %d entries", tenant, head, len(chain))
		}
	}
	first := store.entries["acme"][0]
	if first.OccurredAt != "2026-08-30T10:00:00Z" || first.RecordedAt != "2026-08-30T12:00:00.5Z" {
		t.Fatalf("timestamps were not normalized: %+v", first)
	}
	if string(first.Metadata) != `{"a":1,"b":2}` || first.Source != "test-ingest" || first.PreviousHash != GenesisHash {
		t.Fatalf("entry was not canonicalized from policy: %+v", first)
	}
}

func TestAppendReplayIsIdempotentAndConflictAware(t *testing.T) {
	service, store := newTestService(t)
	original, err := service.Append(context.Background(), []Event{sample("acme", "evt-1")})
	if err != nil {
		t.Fatalf("first append: %v", err)
	}
	replay := sample("acme", "evt-1")
	replay.Metadata = json.RawMessage(`{"a":1,"b":2}`)
	replayed, err := service.Append(context.Background(), []Event{replay})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replayed[0].Replayed || replayed[0].Sequence != original[0].Sequence || replayed[0].Hash != original[0].Hash {
		t.Fatalf("replay receipt %+v differs from original %+v", replayed[0], original[0])
	}
	if len(store.entries["acme"]) != 1 {
		t.Fatalf("replay appended: %d entries", len(store.entries["acme"]))
	}
	conflict := sample("acme", "evt-1")
	conflict.Actor = "user:mallory"
	_, err = service.Append(context.Background(), []Event{sample("acme", "evt-2"), conflict})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting replay error = %v, want ErrConflict", err)
	}
	if len(store.entries["acme"]) != 1 {
		t.Fatalf("a conflicting batch appended its valid sibling: %d entries", len(store.entries["acme"]))
	}
}

func TestAppendRejectsWholeBatchOnInvalidEvent(t *testing.T) {
	service, store := newTestService(t)
	bad := sample("acme", "evt-2")
	bad.Metadata = json.RawMessage(`{"ratio": 0.5}`)
	_, err := service.Append(context.Background(), []Event{sample("acme", "evt-1"), bad})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "event 1") {
		t.Fatalf("error = %v, want ErrInvalid naming event 1", err)
	}
	if len(store.entries) != 0 {
		t.Fatalf("invalid batch appended: %+v", store.entries)
	}
	if _, err := service.Append(context.Background(), nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty batch error = %v", err)
	}
	oversized := make([]Event, MaxBatch+1)
	if _, err := service.Append(context.Background(), oversized); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized batch error = %v", err)
	}
}

func TestAppendDuplicateWithinBatch(t *testing.T) {
	service, store := newTestService(t)
	receipts, err := service.Append(context.Background(), []Event{sample("acme", "evt-1"), sample("acme", "evt-1")})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if receipts[0].Replayed || !receipts[1].Replayed || receipts[1].Sequence != 1 {
		t.Fatalf("in-batch duplicate receipts %+v", receipts)
	}
	if len(store.entries["acme"]) != 1 {
		t.Fatalf("in-batch duplicate appended twice")
	}
}

func TestNormalizeRejections(t *testing.T) {
	cases := map[string]func(*Event){
		"empty tenant":       func(e *Event) { e.Tenant = "" },
		"upper tenant":       func(e *Event) { e.Tenant = "Acme" },
		"hyphen edge tenant": func(e *Event) { e.Tenant = "-acme" },
		"long tenant":        func(e *Event) { e.Tenant = strings.Repeat("a", MaxTenantBytes+1) },
		"empty id":           func(e *Event) { e.ID = "" },
		"padded actor":       func(e *Event) { e.Actor = " user:ada" },
		"control action":     func(e *Event) { e.Action = "a\nb" },
		"long target":        func(e *Event) { e.Target = strings.Repeat("t", MaxFieldBytes+1) },
		"invalid utf8":       func(e *Event) { e.Actor = "\xff" },
		"missing time":       func(e *Event) { e.OccurredAt = "" },
		"bad time":           func(e *Event) { e.OccurredAt = "2026-08-30" },
		"float metadata":     func(e *Event) { e.Metadata = json.RawMessage(`{"n": 1.0}`) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			event := sample("acme", "evt-1")
			mutate(&event)
			if _, err := Normalize(event); !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestNewServiceRequiresPrincipal(t *testing.T) {
	store := &memoryAppender{entries: map[string][]Entry{}}
	for _, principal := range []string{"", "  ", "bad\tprincipal"} {
		if _, err := NewService(store, fixedClock, principal); err == nil {
			t.Fatalf("accepted principal %q", principal)
		}
	}
	if _, err := NewService(nil, fixedClock, "ok"); err == nil {
		t.Fatal("accepted nil store")
	}
}
