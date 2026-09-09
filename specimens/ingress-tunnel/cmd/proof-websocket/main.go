// Command proof-websocket dials a WebSocket through the public edge with an explicit Host
// header, sends one message, and prints the reply. Exit status is the proof.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/coder/websocket"
)

func main() {
	if err := run(); err != nil {
		slog.Error("proof websocket failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	endpoint := os.Getenv("PROBE_URL")
	host := os.Getenv("PROBE_HOST")
	message := os.Getenv("PROBE_MESSAGE")
	if endpoint == "" || host == "" || message == "" {
		return errors.New("PROBE_URL, PROBE_HOST, and PROBE_MESSAGE are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	socket, response, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{
		Host:       host,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	})
	if response != nil && response.Body != nil {
		defer func() { _ = response.Body.Close() }()
	}
	if err != nil {
		return fmt.Errorf("dial through edge: %w", err)
	}
	defer func() { _ = socket.CloseNow() }()
	if err := socket.Write(ctx, websocket.MessageText, []byte(message)); err != nil {
		return fmt.Errorf("send through tunnel: %w", err)
	}
	_, reply, err := socket.Read(ctx)
	if err != nil {
		return fmt.Errorf("read through tunnel: %w", err)
	}
	fmt.Println(string(reply))
	return socket.Close(websocket.StatusNormalClosure, "done")
}
