package api

import (
	"encoding/json"
	"net/http"
)

const maxIngestBytes = 4 << 20

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if status == http.StatusNoContent {
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}
