package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/itsHabib/saas-reaper-poc/internal/flags"
	"github.com/itsHabib/saas-reaper-poc/internal/snapshot"
	"github.com/itsHabib/saas-reaper-poc/internal/store/sqlite"
)

func TestSQLitePersistsFlagAndAuditAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flags.db")
	store, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	ctx := context.Background()
	service, err := flags.Open(ctx, store, snapshot.NewMemory())
	if err != nil {
		t.Fatalf("open service: %v", err)
	}
	flag := flags.Flag{
		Key:            "new-nav",
		Kind:           flags.String,
		Enabled:        true,
		DefaultVariant: "control",
		Variants: map[string]any{
			"control": "old",
			"new":     "new",
		},
	}
	if _, err := service.Publish(ctx, "staging", flag, 0, "sqlite-test"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, err := service.Publish(ctx, "staging", flag, 0, "stale-writer"); !errors.Is(err, flags.ErrConflict) {
		t.Fatalf("stale publish error = %v, want conflict", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}
	reopenedStore, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("reopen sqlite: %v", err)
	}
	defer reopenedStore.Close()
	reopened, err := flags.Open(ctx, reopenedStore, snapshot.NewMemory())
	if err != nil {
		t.Fatalf("reopen service: %v", err)
	}
	evaluated, err := reopened.Evaluate("staging", "new-nav", map[string]any{"targetingKey": "user-7"})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if evaluated.Value != "old" || evaluated.Revision != 1 {
		t.Fatalf("evaluation = %#v, want old at revision 1", evaluated)
	}
	audit, err := reopened.Audit(ctx, 10)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(audit) != 1 || audit[0].Key != "new-nav" || audit[0].Actor != "sqlite-test" {
		t.Fatalf("audit = %#v", audit)
	}
}
