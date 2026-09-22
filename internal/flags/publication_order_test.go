package flags_test

import (
	"context"
	"testing"

	"github.com/itsHabib/saas-reaper/internal/flags"
	"github.com/itsHabib/saas-reaper/internal/snapshot"
	"github.com/itsHabib/saas-reaper/internal/store/memory"
)

type delayedPublication struct {
	flags.Store
	committed chan struct{}
	resume    chan struct{}
}

func (s *delayedPublication) Publish(ctx context.Context, environment string, flag flags.Flag, expected int64, actor string) (flags.Flag, error) {
	published, err := s.Store.Publish(ctx, environment, flag, expected, actor)
	if err != nil || expected != 1 {
		return published, err
	}
	close(s.committed)
	<-s.resume
	return published, nil
}

func TestPublicationCompletionOrderCannotRegressEvaluation(t *testing.T) {
	ctx := context.Background()
	store := &delayedPublication{Store: memory.New(), committed: make(chan struct{}), resume: make(chan struct{})}
	service, err := flags.Open(ctx, store, snapshot.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(ctx, "production", testFlag(), 0, "operator"); err != nil {
		t.Fatal(err)
	}
	completed := make(chan error, 1)
	go func() {
		_, err := service.Publish(ctx, "production", testFlag(), 1, "operator")
		completed <- err
	}()
	<-store.committed
	latest := testFlag()
	latest.DefaultVariant = "on"
	_, publishErr := service.Publish(ctx, "production", latest, 2, "operator")
	close(store.resume)
	if err := <-completed; err != nil {
		t.Fatal(err)
	}
	if publishErr != nil {
		t.Fatal(publishErr)
	}
	result, err := service.Evaluate("production", latest.Key, map[string]any{"targetingKey": "user"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Revision != 3 || result.Value != true {
		t.Fatalf("evaluation regressed after delayed revision 2: %#v", result)
	}
}
