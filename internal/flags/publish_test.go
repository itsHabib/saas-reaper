package flags_test

import (
	"context"
	"errors"
	"testing"

	"github.com/itsHabib/saas-reaper/internal/flags"
	"github.com/itsHabib/saas-reaper/internal/snapshot"
	"github.com/itsHabib/saas-reaper/internal/store/memory"
)

func TestPublishUsesOptimisticRevisionAndRebuildsProjection(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	service, err := flags.Open(ctx, store, snapshot.NewMemory())
	if err != nil {
		t.Fatalf("open service: %v", err)
	}
	first, err := service.Publish(ctx, "production", testFlag(), 0, "owner@example.com")
	if err != nil {
		t.Fatalf("publish first revision: %v", err)
	}
	if first.Revision != 1 {
		t.Fatalf("first revision = %d, want 1", first.Revision)
	}
	updated := testFlag()
	updated.DefaultVariant = "on"
	second, err := service.Publish(ctx, "production", updated, 1, "owner@example.com")
	if err != nil {
		t.Fatalf("publish second revision: %v", err)
	}
	if second.Revision != 2 {
		t.Fatalf("second revision = %d, want 2", second.Revision)
	}
	_, err = service.Publish(ctx, "production", updated, 1, "stale@example.com")
	if !errors.Is(err, flags.ErrConflict) {
		t.Fatalf("stale publish error = %v, want conflict", err)
	}
	reopened, err := flags.Open(ctx, store, snapshot.NewMemory())
	if err != nil {
		t.Fatalf("reopen service: %v", err)
	}
	evaluated, err := reopened.Evaluate("production", updated.Key, map[string]any{"targetingKey": "user-2"})
	if err != nil {
		t.Fatalf("evaluate reopened service: %v", err)
	}
	if evaluated.Variant != "on" || evaluated.Revision != 2 {
		t.Fatalf("reopened evaluation = %#v, want on at revision 2", evaluated)
	}
	audit, err := reopened.Audit(ctx, 10)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if len(audit) != 2 || audit[0].Revision != 2 || audit[1].Revision != 1 {
		t.Fatalf("audit = %#v, want revisions 2 then 1", audit)
	}
}

func TestPublishRejectsNegativeExpectedRevision(t *testing.T) {
	service, err := flags.Open(context.Background(), memory.New(), snapshot.NewMemory())
	if err != nil {
		t.Fatalf("open service: %v", err)
	}
	_, err = service.Publish(context.Background(), "production", testFlag(), -1, "owner@example.com")
	if !errors.Is(err, flags.ErrInvalid) {
		t.Fatalf("negative revision error = %v, want invalid", err)
	}
}

func testFlag() flags.Flag {
	return flags.Flag{
		Key:            "checkout-v2",
		Kind:           flags.Boolean,
		Enabled:        true,
		DefaultVariant: "off",
		Variants: map[string]any{
			"off": false,
			"on":  true,
		},
	}
}
