package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/itsHabib/saas-reaper/internal/api"
	"github.com/itsHabib/saas-reaper/internal/flags"
	"github.com/itsHabib/saas-reaper/internal/snapshot"
	"github.com/itsHabib/saas-reaper/internal/store/memory"
)

type advancingSnapshot struct{ flags.Snapshot }

func (s advancingSnapshot) List(environment string) []flags.Flag {
	listed := s.Snapshot.List(environment)
	for _, flag := range listed {
		flag.Revision++
		flag.DefaultVariant = "on"
		s.Put(environment, flag)
	}
	return listed
}

func TestBulkEvaluationUsesTheListedSnapshot(t *testing.T) {
	projection := advancingSnapshot{snapshot.NewMemory()}
	service, err := flags.Open(context.Background(), memory.New(), projection)
	if err != nil {
		t.Fatal(err)
	}
	server, err := api.New(service, adminToken, adminActor, evaluationToken)
	if err != nil {
		t.Fatal(err)
	}
	running := httptest.NewServer(server.Handler())
	defer running.Close()
	publishFixture(t, running.URL)
	response := doJSON(t, http.MethodPost, running.URL+"/environments/production/ofrep/v1/evaluate/flags", evaluationToken,
		map[string]any{"context": map[string]any{"targetingKey": "user-2"}}, nil)
	assertStatus(t, response, http.StatusOK)
	var body struct {
		Flags []struct {
			Value    bool
			Metadata struct{ Revision int64 }
		}
	}
	decodeResponse(t, response, &body)
	if len(body.Flags) != 1 || body.Flags[0].Metadata.Revision != 1 || body.Flags[0].Value {
		t.Fatalf("bulk response reread a newer revision than its snapshot: %#v", body)
	}
}

func TestBulkEvaluationRejectsEmptyTargetingKey(t *testing.T) {
	server := newServer(t)
	defer server.Close()
	response := doJSON(t, http.MethodPost, server.URL+"/environments/production/ofrep/v1/evaluate/flags", evaluationToken,
		map[string]any{"context": map[string]any{"targetingKey": ""}}, nil)
	assertStatus(t, response, http.StatusBadRequest)
	closeResponse(t, response)
}
