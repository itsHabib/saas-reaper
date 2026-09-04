package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/audit-ledger/internal/ledger"
)

func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit.db")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, path
}

func testService(t *testing.T, store *Store) *ledger.Service {
	t.Helper()
	clock := func() time.Time { return time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC) }
	service, err := ledger.NewService(store, clock, "store-test")
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return service
}

func event(tenant, id string) ledger.Event {
	return ledger.Event{
		Tenant:     tenant,
		ID:         id,
		Actor:      "user:ada",
		Action:     "document.viewed",
		Target:     "document:" + id,
		OccurredAt: "2026-08-30T11:00:00Z",
		Metadata:   json.RawMessage(`{"id":"` + id + `"}`),
	}
}

func TestOpenCreatesPrivateFile(t *testing.T) {
	_, path := openTestStore(t)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("database mode %v, want 0600", info.Mode().Perm())
	}
	if _, err := Open(""); err == nil {
		t.Fatal("accepted empty path")
	}
}

func TestAppendOnlyTriggersRejectMutation(t *testing.T) {
	store, _ := openTestStore(t)
	service := testService(t, store)
	if _, err := service.Append(context.Background(), []ledger.Event{event("acme", "evt-1")}); err != nil {
		t.Fatalf("append: %v", err)
	}
	statements := []string{
		`UPDATE entries SET actor = 'mallory' WHERE tenant = 'acme' AND sequence = 1`,
		`DELETE FROM entries WHERE tenant = 'acme' AND sequence = 1`,
	}
	for _, statement := range statements {
		_, err := store.db.ExecContext(context.Background(), statement)
		if err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Fatalf("%s: error %v, want append-only trigger abort", statement, err)
		}
	}
	entries, err := store.Entries(context.Background(), "acme", 0, 10)
	if err != nil {
		t.Fatalf("entries: %v", err)
	}
	if len(entries) != 1 || entries[0].Actor != "user:ada" {
		t.Fatalf("entries after rejected mutations: %+v", entries)
	}
}

func TestAppendIsAtomicAndSurvivesReopen(t *testing.T) {
	store, path := openTestStore(t)
	service := testService(t, store)
	_, err := service.Append(context.Background(), []ledger.Event{event("acme", "evt-1"), event("acme", "evt-2")})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	conflict := event("acme", "evt-1")
	conflict.Actor = "user:mallory"
	_, err = service.Append(context.Background(), []ledger.Event{event("acme", "evt-3"), conflict})
	if !errors.Is(err, ledger.ErrConflict) {
		t.Fatalf("conflict error = %v", err)
	}
	failing := func(ledger.AppendTransaction) error { return errors.New("forced") }
	insertThenFail := func(tx ledger.AppendTransaction) error {
		entry := ledger.Entry{Tenant: "acme", Sequence: 3, ID: "evt-ghost", Actor: "a", Action: "b", Target: "c",
			OccurredAt: "x", RecordedAt: "y", Source: "z", Metadata: json.RawMessage("null"),
			PreviousHash: ledger.GenesisHash, Hash: ledger.GenesisHash}
		if err := tx.Insert(context.Background(), entry); err != nil {
			return err
		}
		return failing(tx)
	}
	if err := store.Append(context.Background(), insertThenFail); err == nil {
		t.Fatal("failing transaction committed")
	}
	before, err := store.Head(context.Background(), "acme")
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if before.Sequence != 2 {
		t.Fatalf("head after rolled-back appends = %+v, want sequence 2", before)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	after, err := reopened.Head(context.Background(), "acme")
	if err != nil {
		t.Fatalf("head after reopen: %v", err)
	}
	if after != before {
		t.Fatalf("head changed across reopen: %+v -> %+v", before, after)
	}
	var exported []ledger.Entry
	err = reopened.Export(context.Background(), "acme", func(entry ledger.Entry) error {
		exported = append(exported, entry)
		return nil
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	head, err := ledger.Verify(exported)
	if err != nil || head != after {
		t.Fatalf("exported chain verify = %+v, %v; want %+v", head, err, after)
	}
}

func TestConcurrentAppendsStayContiguous(t *testing.T) {
	store, _ := openTestStore(t)
	service := testService(t, store)
	const writers = 8
	const perWriter = 5
	var wg sync.WaitGroup
	failures := make(chan error, writers)
	for writer := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range perWriter {
				id := fmt.Sprintf("evt-%d-%d", writer, index)
				if _, err := service.Append(context.Background(), []ledger.Event{event("acme", id)}); err != nil {
					failures <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatalf("concurrent append: %v", err)
	}
	entries, err := store.Entries(context.Background(), "acme", 0, 1000)
	if err != nil {
		t.Fatalf("entries: %v", err)
	}
	if len(entries) != writers*perWriter {
		t.Fatalf("%d entries, want %d", len(entries), writers*perWriter)
	}
	if _, err := ledger.Verify(entries); err != nil {
		t.Fatalf("chain after concurrent appends: %v", err)
	}
}

func TestEntriesPaginateAndIsolateTenants(t *testing.T) {
	store, _ := openTestStore(t)
	service := testService(t, store)
	batch := []ledger.Event{event("globex", "evt-1")}
	for index := 1; index <= 7; index++ {
		batch = append(batch, event("acme", fmt.Sprintf("evt-%d", index)))
	}
	if _, err := service.Append(context.Background(), batch); err != nil {
		t.Fatalf("append: %v", err)
	}
	var walked []int64
	after := int64(0)
	for {
		page, err := store.Entries(context.Background(), "acme", after, 3)
		if err != nil {
			t.Fatalf("page after %d: %v", after, err)
		}
		if len(page) == 0 {
			break
		}
		for _, entry := range page {
			if entry.Tenant != "acme" {
				t.Fatalf("foreign tenant row %+v", entry)
			}
			walked = append(walked, entry.Sequence)
			after = entry.Sequence
		}
	}
	if fmt.Sprint(walked) != "[1 2 3 4 5 6 7]" {
		t.Fatalf("walk %v", walked)
	}
	head, err := store.Head(context.Background(), "nobody")
	if err != nil || head.Sequence != 0 || head.Hash != ledger.GenesisHash {
		t.Fatalf("unknown tenant head = %+v, %v", head, err)
	}
}
