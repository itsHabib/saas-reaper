package configurator

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/itsHabib/saas-reaper-poc/internal/factory"
)

func TestPageAndCatalog(t *testing.T) {
	pageRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	pageResponse := httptest.NewRecorder()
	Handler().ServeHTTP(pageResponse, pageRequest)
	if pageResponse.Code != http.StatusOK {
		t.Fatalf("page status = %d", pageResponse.Code)
	}
	if !strings.Contains(pageResponse.Body.String(), "Build and download repository") {
		t.Fatal("page does not contain the product action")
	}

	catalogRequest := httptest.NewRequest(http.MethodGet, "/api/catalog", nil)
	catalogResponse := httptest.NewRecorder()
	Handler().ServeHTTP(catalogResponse, catalogRequest)
	if catalogResponse.Code != http.StatusOK {
		t.Fatalf("catalog status = %d", catalogResponse.Code)
	}
	if !strings.Contains(catalogResponse.Body.String(), "gcp-cloud-run") {
		t.Fatal("catalog does not expose the GCP deployment")
	}
}

func TestGenerateDownloadsOwnedRepository(t *testing.T) {
	recipe := factory.DefaultRecipe()
	recipe.Name = "browser-flags"
	body, err := json.Marshal(recipe)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/generate", bytes.NewReader(body))
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("generate status = %d: %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("content type = %q", response.Header().Get("Content-Type"))
	}
	archive, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	assertDownloadedFile(t, archive, "browser-flags/REAPER.yaml", "language: go")
	assertDownloadedFile(t, archive, "browser-flags/internal/flags/evaluate.go", "func Evaluate")
	assertDownloadedFile(t, archive, "browser-flags/.agents/skills/onboard-domain/SKILL.md", "Onboard a customer domain")
}

func TestGenerateRejectsUnsafeSelection(t *testing.T) {
	recipe := factory.DefaultRecipe()
	recipe.Name = "unsafe-flags"
	recipe.Deployment.Target = "kubernetes"
	body, err := json.Marshal(recipe)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/generate", bytes.NewReader(body))
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("generate status = %d, want 422", response.Code)
	}
	if !strings.Contains(response.Body.String(), "requires postgres") {
		t.Fatalf("unexpected error: %s", response.Body.String())
	}
}

func assertDownloadedFile(t *testing.T, archive *zip.Reader, name, fragment string) {
	t.Helper()
	for _, file := range archive.File {
		if file.Name != name {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(reader)
		closeErr := reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		if !bytes.Contains(body, []byte(fragment)) {
			t.Fatalf("%s does not contain %q", name, fragment)
		}
		return
	}
	t.Fatalf("download does not contain %s", name)
}
