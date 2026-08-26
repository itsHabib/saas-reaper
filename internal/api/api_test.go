package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/itsHabib/saas-reaper-poc/internal/api"
	"github.com/itsHabib/saas-reaper-poc/internal/flags"
	"github.com/itsHabib/saas-reaper-poc/internal/snapshot"
	"github.com/itsHabib/saas-reaper-poc/internal/store/memory"
)

const (
	adminToken      = "admin-test-token"
	adminActor      = "api-test"
	evaluationToken = "evaluation-test-token"
)

func TestManagementAndOFREPSurfacesStaySeparated(t *testing.T) {
	server := newServer(t)
	defer server.Close()
	publishBody := map[string]any{
		"expectedRevision": 0,
		"flag": map[string]any{
			"kind":           "boolean",
			"enabled":        true,
			"defaultVariant": "off",
			"variants": map[string]any{
				"off": false,
				"on":  true,
			},
			"rules": []map[string]any{{
				"attribute": "organization.id",
				"equals":    "acme",
				"variant":   "on",
			}},
		},
	}
	unauthorized := doJSON(t, http.MethodPut, server.URL+"/v1/environments/production/flags/checkout-v2", "", publishBody, nil)
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized publish status = %d", unauthorized.StatusCode)
	}
	closeResponse(t, unauthorized)
	publishBody["actor"] = "forged-actor"
	forged := doJSON(t, http.MethodPut, server.URL+"/v1/environments/production/flags/checkout-v2", adminToken, publishBody, nil)
	if forged.StatusCode != http.StatusBadRequest {
		t.Fatalf("caller-supplied actor status = %d, want 400", forged.StatusCode)
	}
	closeResponse(t, forged)
	delete(publishBody, "actor")
	published := doJSON(t, http.MethodPut, server.URL+"/v1/environments/production/flags/checkout-v2", adminToken, publishBody, nil)
	assertStatus(t, published, http.StatusOK)
	var stored flags.Flag
	decodeResponse(t, published, &stored)
	if stored.Key != "checkout-v2" || stored.Revision != 1 {
		t.Fatalf("published flag = %#v", stored)
	}
	auditResponse := doJSON(t, http.MethodGet, server.URL+"/v1/audit", adminToken, nil, nil)
	assertStatus(t, auditResponse, http.StatusOK)
	var auditBody struct {
		Audit []flags.AuditEntry `json:"audit"`
	}
	decodeResponse(t, auditResponse, &auditBody)
	if len(auditBody.Audit) != 1 || auditBody.Audit[0].Actor != adminActor {
		t.Fatalf("audit = %#v, want configured actor %q", auditBody.Audit, adminActor)
	}
	evaluated := doJSON(
		t,
		http.MethodPost,
		server.URL+"/environments/production/ofrep/v1/evaluate/flags/checkout-v2",
		evaluationToken,
		map[string]any{"context": map[string]any{"targetingKey": "user-9", "organization.id": "acme"}},
		nil,
	)
	assertStatus(t, evaluated, http.StatusOK)
	var result map[string]any
	decodeResponse(t, evaluated, &result)
	if result["variant"] != "on" || result["reason"] != "TARGETING_MATCH" || result["value"] != true {
		t.Fatalf("evaluation = %#v", result)
	}
	adminOnEvaluation := doJSON(
		t,
		http.MethodPost,
		server.URL+"/environments/production/ofrep/v1/evaluate/flags/checkout-v2",
		adminToken,
		map[string]any{"context": map[string]any{"targetingKey": "user-9"}},
		nil,
	)
	if adminOnEvaluation.StatusCode != http.StatusUnauthorized {
		t.Fatalf("admin token on evaluation status = %d", adminOnEvaluation.StatusCode)
	}
	closeResponse(t, adminOnEvaluation)
}

func TestOFREPBulkEvaluationUsesETag(t *testing.T) {
	server := newServer(t)
	defer server.Close()
	publishFixture(t, server.URL)
	url := server.URL + "/environments/production/ofrep/v1/evaluate/flags"
	first := doJSON(
		t,
		http.MethodPost,
		url,
		evaluationToken,
		map[string]any{"context": map[string]any{"targetingKey": "user-1"}},
		nil,
	)
	assertStatus(t, first, http.StatusOK)
	etag := first.Header.Get("ETag")
	if etag == "" {
		t.Fatal("bulk evaluation did not return ETag")
	}
	closeResponse(t, first)
	second := doJSON(
		t,
		http.MethodPost,
		url,
		evaluationToken,
		map[string]any{"context": map[string]any{"targetingKey": "user-1"}},
		map[string]string{"If-None-Match": etag},
	)
	if second.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional bulk status = %d, want 304", second.StatusCode)
	}
	closeResponse(t, second)
}

func TestOFREPBulkEvaluationETagVariesByContext(t *testing.T) {
	server := newServer(t)
	defer server.Close()
	publishOrganizationRule(t, server.URL)
	url := server.URL + "/environments/production/ofrep/v1/evaluate/flags"
	first := doJSON(
		t,
		http.MethodPost,
		url,
		evaluationToken,
		map[string]any{"context": map[string]any{"targetingKey": "user-9", "organization.id": "acme"}},
		nil,
	)
	assertStatus(t, first, http.StatusOK)
	etag := first.Header.Get("ETag")
	closeResponse(t, first)
	crossContext := doJSON(
		t,
		http.MethodPost,
		url,
		evaluationToken,
		map[string]any{"context": map[string]any{"targetingKey": "user-9", "organization.id": "other"}},
		map[string]string{"If-None-Match": etag},
	)
	assertStatus(t, crossContext, http.StatusOK)
	var body struct {
		Flags []struct {
			Key   string `json:"key"`
			Value any    `json:"value"`
		} `json:"flags"`
	}
	decodeResponse(t, crossContext, &body)
	if len(body.Flags) != 1 || body.Flags[0].Value != false {
		t.Fatalf("cross-context bulk evaluation = %#v, want fresh false decision", body.Flags)
	}
}

func publishOrganizationRule(t *testing.T, baseURL string) {
	t.Helper()
	body := map[string]any{
		"expectedRevision": 0,
		"flag": map[string]any{
			"kind":           "boolean",
			"enabled":        true,
			"defaultVariant": "off",
			"variants":       map[string]any{"off": false, "on": true},
			"rules": []map[string]any{{
				"attribute": "organization.id",
				"equals":    "acme",
				"variant":   "on",
			}},
		},
	}
	response := doJSON(t, http.MethodPut, baseURL+"/v1/environments/production/flags/checkout-v2", adminToken, body, nil)
	assertStatus(t, response, http.StatusOK)
	closeResponse(t, response)
}

func TestOFREPRejectsMissingTargetingKey(t *testing.T) {
	server := newServer(t)
	defer server.Close()
	response := doJSON(
		t,
		http.MethodPost,
		server.URL+"/environments/production/ofrep/v1/evaluate/flags/checkout-v2",
		evaluationToken,
		map[string]any{"context": map[string]any{}},
		nil,
	)
	assertStatus(t, response, http.StatusBadRequest)
	var failure map[string]any
	decodeResponse(t, response, &failure)
	if failure["errorCode"] != "TARGETING_KEY_MISSING" {
		t.Fatalf("failure = %#v", failure)
	}
}

func TestServerRejectsSharedAuthorityToken(t *testing.T) {
	service, err := flags.Open(context.Background(), memory.New(), snapshot.NewMemory())
	if err != nil {
		t.Fatalf("open flags: %v", err)
	}
	if _, err := api.New(service, "shared", adminActor, "shared"); err == nil {
		t.Fatal("shared management and evaluation token must be rejected")
	}
}

func TestAuthorizationRequiresBearerScheme(t *testing.T) {
	server := newServer(t)
	defer server.Close()
	request, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		server.URL+"/v1/environments/production/flags",
		nil,
	)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set("Authorization", adminToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	defer closeResponse(t, response)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("raw token status = %d, want 401", response.StatusCode)
	}
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	service, err := flags.Open(context.Background(), memory.New(), snapshot.NewMemory())
	if err != nil {
		t.Fatalf("open flags: %v", err)
	}
	handler, err := api.New(service, adminToken, adminActor, evaluationToken)
	if err != nil {
		t.Fatalf("new API: %v", err)
	}
	return httptest.NewServer(handler.Handler())
}

func publishFixture(t *testing.T, baseURL string) {
	t.Helper()
	body := map[string]any{
		"expectedRevision": 0,
		"flag": map[string]any{
			"kind":           "boolean",
			"enabled":        true,
			"defaultVariant": "off",
			"variants":       map[string]any{"off": false, "on": true},
			"rollout": map[string]any{
				"attribute":  "targetingKey",
				"percentage": 30,
				"variant":    "on",
			},
		},
	}
	response := doJSON(t, http.MethodPut, baseURL+"/v1/environments/production/flags/checkout-v2", adminToken, body, nil)
	assertStatus(t, response, http.StatusOK)
	closeResponse(t, response)
}

func doJSON(
	t *testing.T,
	method string,
	url string,
	token string,
	body any,
	headers map[string]string,
) *http.Response {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	request, err := http.NewRequestWithContext(t.Context(), method, url, bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	return response
}

func assertStatus(t *testing.T, response *http.Response, expected int) {
	t.Helper()
	if response.StatusCode == expected {
		return
	}
	body, _ := io.ReadAll(response.Body)
	closeResponse(t, response)
	t.Fatalf("status = %d, want %d; body=%s", response.StatusCode, expected, body)
}

func decodeResponse(t *testing.T, response *http.Response, destination any) {
	t.Helper()
	defer closeResponse(t, response)
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func closeResponse(t *testing.T, response *http.Response) {
	t.Helper()
	if err := response.Body.Close(); err != nil {
		t.Fatalf("close response body: %v", err)
	}
}
