// Package configurator exposes the Reaper catalog and ZIP generator in a browser.
package configurator

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/itsHabib/saas-reaper/internal/factory"
)

const maxRecipeBytes = 1 << 20

//go:embed page.html
var pages embed.FS

// Handler returns the complete local product surface.
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", page)
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /api/catalog", catalog)
	mux.HandleFunc("POST /api/generate", generate)
	return mux
}

// Serve starts the local configurator at address.
func Serve(address string) error {
	server := &http.Server{
		Addr:              address,
		Handler:           Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return server.ListenAndServe()
}

func page(response http.ResponseWriter, _ *http.Request) {
	body, err := pages.ReadFile("page.html")
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = response.Write(body)
}

func health(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
}

func catalog(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, factory.ProductCatalog())
}

func generate(response http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(response, request.Body, maxRecipeBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var recipe factory.Recipe
	if err := decoder.Decode(&recipe); err != nil {
		writeError(response, http.StatusBadRequest, fmt.Errorf("decode recipe: %w", err))
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(response, http.StatusBadRequest, errors.New("recipe must contain one JSON value"))
		return
	}
	recipe.Delivery.Format = "zip"
	work, err := os.MkdirTemp("", "saas-reaper-download-")
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	defer os.RemoveAll(work)
	result, err := factory.Generate(recipe, filepath.Join(work, recipe.Name))
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, err)
		return
	}
	body, err := os.ReadFile(result.Archive)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	response.Header().Set("Content-Type", "application/zip")
	response.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, recipe.Name))
	response.Header().Set("Content-Length", strconv.Itoa(len(body)))
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(body)
}

func writeError(response http.ResponseWriter, status int, err error) {
	writeJSON(response, status, map[string]string{"error": err.Error()})
}

func writeJSON(response http.ResponseWriter, status int, body any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(body)
}
