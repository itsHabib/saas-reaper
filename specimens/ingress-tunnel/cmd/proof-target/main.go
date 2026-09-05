// Command proof-target is the local origin the proofs expose through a tunnel. It reports what
// it received, echoes bodies, streams chunks with gaps, and echoes WebSocket messages, so the
// harness can assert routing, forwarding, streaming, and upgrades from the outside.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/coder/websocket"
)

func main() {
	if err := run(); err != nil {
		slog.Error("proof target stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	address := os.Getenv("TARGET_ADDR")
	name := os.Getenv("TARGET_NAME")
	if address == "" || name == "" {
		return errors.New("TARGET_ADDR and TARGET_NAME are required")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /whoami", whoami(name))
	mux.HandleFunc("POST /echo", echo)
	mux.HandleFunc("GET /stream", stream)
	mux.HandleFunc("GET /ws", websocketEcho(name))
	server := &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	failures := make(chan error, 1)
	go func() { failures <- server.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownContext)
	case err := <-failures:
		return err
	}
}

type observation struct {
	Name           string `json:"name"`
	Host           string `json:"host"`
	Method         string `json:"method"`
	Path           string `json:"path"`
	Query          string `json:"query"`
	ForwardedFor   string `json:"forwardedFor"`
	ForwardedHost  string `json:"forwardedHost"`
	ForwardedProto string `json:"forwardedProto"`
}

func whoami(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(observation{
			Name:           name,
			Host:           r.Host,
			Method:         r.Method,
			Path:           r.URL.Path,
			Query:          r.URL.RawQuery,
			ForwardedFor:   r.Header.Get("X-Forwarded-For"),
			ForwardedHost:  r.Header.Get("X-Forwarded-Host"),
			ForwardedProto: r.Header.Get("X-Forwarded-Proto"),
		})
	}
}

// echo returns the exact bytes it read and their count in a header, so the harness can prove
// a request body crossed the tunnel unaltered.
func echo(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Echo-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}

// stream writes numbered chunks separated by real time gaps. A buffering path would deliver
// them together; the harness measures that the first arrives before the last is written.
func stream(w http.ResponseWriter, r *http.Request) {
	chunks, gap, err := streamShape(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("X-Accel-Buffering", "no")
	for index := range chunks {
		if index > 0 {
			time.Sleep(gap)
		}
		_, _ = fmt.Fprintf(w, "chunk-%d\n", index+1)
		flusher.Flush()
	}
}

func streamShape(r *http.Request) (int, time.Duration, error) {
	chunks, err := strconv.Atoi(r.URL.Query().Get("chunks"))
	if err != nil || chunks < 1 || chunks > 100 {
		return 0, 0, errors.New("chunks must be between 1 and 100")
	}
	gap, err := time.ParseDuration(r.URL.Query().Get("gap"))
	if err != nil || gap <= 0 || gap > 5*time.Second {
		return 0, 0, errors.New("gap must be a positive duration up to 5s")
	}
	return chunks, gap, nil
}

// websocketEcho answers each text message with the target's name prepended, which proves both
// the upgrade and which origin served it.
func websocketEcho(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		socket, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer func() { _ = socket.CloseNow() }()
		for {
			kind, message, err := socket.Read(r.Context())
			if err != nil {
				return
			}
			reply := []byte(name + ":" + string(message))
			if err := socket.Write(r.Context(), kind, reply); err != nil {
				return
			}
		}
	}
}
