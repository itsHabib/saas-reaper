package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"time"
)

// slackCallback is a synthetic receiver used only by the local proof. It verifies the raw
// body with Slack's v0 signing protocol and acknowledges without sending any Slack messages.
func slackCallback(secret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		timestamp := r.Header.Get("X-Slack-Request-Timestamp")
		seconds, err := strconv.ParseInt(timestamp, 10, 64)
		if err != nil {
			http.Error(w, "timestamp", http.StatusUnauthorized)
			return
		}
		age := time.Since(time.Unix(seconds, 0))
		if age > 5*time.Minute || age < -5*time.Minute {
			http.Error(w, "stale", http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			http.Error(w, "body", http.StatusBadRequest)
			return
		}
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = io.WriteString(mac, "v0:"+timestamp+":")
		_, _ = mac.Write(body)
		expected := "v0=" + hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(expected), []byte(r.Header.Get("X-Slack-Signature"))) {
			http.Error(w, "signature", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}
