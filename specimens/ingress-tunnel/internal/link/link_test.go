package link

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/tunnel"
)

// fakeConnector accepts one token and records every attached link and reported loss.
type fakeConnector struct {
	mu        sync.Mutex
	token     string
	attachErr error
	links     []tunnel.Link
	lost      chan struct{}
}

func (f *fakeConnector) Authenticate(_ context.Context, token string) (tunnel.Claim, error) {
	if token != f.token {
		return tunnel.Claim{}, tunnel.ErrUnauthorized
	}
	return tunnel.Claim{Subdomain: "acme", TokenHash: tunnel.HashToken(token)}, nil
}

func (f *fakeConnector) Attach(_ context.Context, _ tunnel.Claim, link tunnel.Link) (tunnel.Connection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.attachErr != nil {
		return tunnel.Connection{}, f.attachErr
	}
	f.links = append(f.links, link)
	return tunnel.Connection{Subdomain: "acme"}, nil
}

func (f *fakeConnector) link(t *testing.T) tunnel.Link {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		count := len(f.links)
		f.mu.Unlock()
		if count > 0 {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.links[len(f.links)-1]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no link attached")
	return nil
}

func testConfig() Config {
	return Config{KeepAliveInterval: 100 * time.Millisecond, WriteTimeout: time.Second}
}

func startServer(t *testing.T, connector *fakeConnector) *httptest.Server {
	t.Helper()
	handler, err := NewHandler(connector, testConfig(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /v1/connect", handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func dial(t *testing.T, server *httptest.Server, token string) (*Listener, error) {
	t.Helper()
	dialer, err := NewDialer(server.URL, token, 5*time.Second, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	return dialer.Dial(context.Background())
}

func TestNewDialerValidatesEndpointAndCredential(t *testing.T) {
	if _, err := NewDialer("ftp://x", "t", time.Second, testConfig()); err == nil {
		t.Fatal("ftp accepted")
	}
	if _, err := NewDialer("http://x", " ", time.Second, testConfig()); err == nil {
		t.Fatal("blank token accepted")
	}
	if _, err := NewDialer("http://x", "t", 0, testConfig()); err == nil {
		t.Fatal("zero timeout accepted")
	}
	dialer, err := NewDialer("https://tunnel.example.com/base/", "t", time.Second, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if dialer.endpoint != "https://tunnel.example.com/base/v1/connect" {
		t.Fatalf("endpoint = %q", dialer.endpoint)
	}
}

func TestStreamsCrossTheLinkInBothDirections(t *testing.T) {
	connector := &fakeConnector{token: "good", lost: make(chan struct{})}
	server := startServer(t, connector)
	listener, err := dial(t, server, "good")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		line, _ := bufio.NewReader(conn).ReadString('\n')
		_, _ = io.WriteString(conn, "agent saw "+line)
	}()
	stream, err := connector.link(t).Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if _, err := io.WriteString(stream, "hello\n"); err != nil {
		t.Fatal(err)
	}
	reply, err := bufio.NewReader(stream).ReadString('\n')
	if err != nil || reply != "agent saw hello\n" {
		t.Fatalf("reply = %q, %v", reply, err)
	}
}

func TestDialWithABadTokenIsAPermanentRejection(t *testing.T) {
	connector := &fakeConnector{token: "good"}
	server := startServer(t, connector)
	_, err := dial(t, server, "bad")
	var rejection RejectedError
	if !errors.As(err, &rejection) || rejection.Status != http.StatusUnauthorized || !rejection.Permanent() {
		t.Fatalf("bad token dial = %v", err)
	}
	if len(connector.links) != 0 {
		t.Fatal("a refused token attached a link")
	}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/v1/connect", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("connect without a token = %d, want 401", response.StatusCode)
	}
}

func TestAttachFailureClosesTheLinkOnBothEnds(t *testing.T) {
	connector := &fakeConnector{token: "good", attachErr: tunnel.ErrRevoked}
	server := startServer(t, connector)
	listener, err := dial(t, server, "good")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	if _, err := listener.Accept(); err == nil {
		t.Fatal("agent listener stayed open after the server refused to attach")
	}
}

func TestSessionDoneFiresWhenTheAgentGoesAway(t *testing.T) {
	connector := &fakeConnector{token: "good"}
	server := startServer(t, connector)
	listener, err := dial(t, server, "good")
	if err != nil {
		t.Fatal(err)
	}
	session, ok := connector.link(t).(*Session)
	if !ok {
		t.Fatal("attached link is not a session")
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("session did not notice the closed agent link")
	}
	if _, err := session.Open(context.Background()); err == nil {
		t.Fatal("a dead session opened a stream")
	}
	if err := session.Close(tunnel.CloseShutdown); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closing a dead session = %v", err)
	}
}

func TestAnEvictedAgentLearnsTheReason(t *testing.T) {
	for _, reason := range []tunnel.CloseReason{tunnel.CloseSuperseded, tunnel.CloseRevoked} {
		connector := &fakeConnector{token: "good"}
		server := startServer(t, connector)
		listener, err := dial(t, server, "good")
		if err != nil {
			t.Fatal(err)
		}
		session := connector.link(t)
		if err := session.Close(reason); err != nil {
			t.Fatal(err)
		}
		_, err = listener.Accept()
		var evicted EvictedError
		if !errors.As(err, &evicted) || evicted.Reason != reason || !evicted.Permanent() {
			t.Fatalf("accept after %s eviction = %v", reason, err)
		}
		_ = listener.Close()
	}
}

func TestAPlainLinkLossIsNotAnEviction(t *testing.T) {
	connector := &fakeConnector{token: "good"}
	server := startServer(t, connector)
	listener, err := dial(t, server, "good")
	if err != nil {
		t.Fatal(err)
	}
	if err := connector.link(t).Close(tunnel.CloseShutdown); err != nil {
		t.Fatal(err)
	}
	_, err = listener.Accept()
	var evicted EvictedError
	if err == nil || errors.As(err, &evicted) {
		t.Fatalf("accept after a server shutdown = %v, want a plain loss", err)
	}
}

func TestSessionOpenHonorsACanceledContext(t *testing.T) {
	connector := &fakeConnector{token: "good"}
	server := startServer(t, connector)
	listener, err := dial(t, server, "good")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := connector.link(t).Open(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("open with canceled context = %v", err)
	}
}
