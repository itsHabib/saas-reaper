package edge

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/tunnel"
)

// pipeLink serves an in-process handler at the far end of every opened stream, which is
// exactly what an agent does, minus the network.
type pipeLink struct {
	handler http.Handler
	opened  int
	mu      sync.Mutex
}

func (p *pipeLink) Open(context.Context) (net.Conn, error) {
	p.mu.Lock()
	p.opened++
	p.mu.Unlock()
	client, server := net.Pipe()
	go func() {
		_ = (&http.Server{Handler: p.handler, ReadHeaderTimeout: time.Second}).Serve(&oneConnListener{conn: server, done: make(chan struct{})})
	}()
	return client, nil
}

func (p *pipeLink) Close(tunnel.CloseReason) error { return nil }

type oneConnListener struct {
	conn net.Conn
	done chan struct{}
	once sync.Once
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	var conn net.Conn
	l.once.Do(func() { conn = l.conn })
	if conn != nil {
		return conn, nil
	}
	<-l.done
	return nil, net.ErrClosed
}

func (l *oneConnListener) Close() error {
	l.once.Do(func() {})
	select {
	case <-l.done:
	default:
		close(l.done)
	}
	return nil
}

func (l *oneConnListener) Addr() net.Addr { return &net.TCPAddr{} }

type table map[string]tunnel.Link

func (t table) Lookup(subdomain string) (tunnel.Link, bool) {
	link, ok := t[subdomain]
	return link, ok
}

func startEdge(t *testing.T, routes table) *httptest.Server {
	t.Helper()
	proxy, err := New("tunnel.test", routes, "https", 5*time.Second, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(proxy)
	t.Cleanup(server.Close)
	return server
}

func get(t *testing.T, server *httptest.Server, host, path string) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = host
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func observer(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"name":  name,
			"host":  r.Host,
			"for":   r.Header.Get("X-Forwarded-For"),
			"fhost": r.Header.Get("X-Forwarded-Host"),
			"proto": r.Header.Get("X-Forwarded-Proto"),
		})
	})
}

func TestNewValidatesItsInputs(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	if _, err := New(" ", table{}, "https", time.Second, logger); err == nil {
		t.Fatal("blank domain accepted")
	}
	if _, err := New("tunnel.test", table{}, "ftp", time.Second, logger); err == nil {
		t.Fatal("bad forwarded proto accepted")
	}
	if _, err := New("tunnel.test", table{}, "https", 0, logger); err == nil {
		t.Fatal("zero timeout accepted")
	}
}

func TestRequestsRouteToTheirOwnLinkAndNowhereElse(t *testing.T) {
	acme := &pipeLink{handler: observer("acme")}
	umbrella := &pipeLink{handler: observer("umbrella")}
	server := startEdge(t, table{"acme": acme, "umbrella": umbrella})
	for _, tc := range []struct{ host, want string }{{"acme.tunnel.test", "acme"}, {"umbrella.tunnel.test:443", "umbrella"}} {
		response := get(t, server, tc.host, "/whoami")
		var seen map[string]string
		if err := json.NewDecoder(response.Body).Decode(&seen); err != nil {
			t.Fatal(err)
		}
		if seen["name"] != tc.want {
			t.Fatalf("%s reached %s", tc.host, seen["name"])
		}
		if seen["host"] != tc.host || seen["fhost"] != tc.host || seen["proto"] != "https" || !strings.HasPrefix(seen["for"], "127.0.0.1") {
			t.Fatalf("forwarded triple for %s = %v", tc.host, seen)
		}
	}
	if acme.opened != 1 || umbrella.opened != 1 {
		t.Fatalf("streams opened acme=%d umbrella=%d, want one each", acme.opened, umbrella.opened)
	}
}

func TestUnknownAndOfflineHostsAreRefusedWithoutTouchingAnyLink(t *testing.T) {
	acme := &pipeLink{handler: observer("acme")}
	server := startEdge(t, table{"acme": acme})
	cases := map[string]int{
		"tunnel.test":              http.StatusNotFound,
		"deep.acme.tunnel.test":    http.StatusNotFound,
		"acme.other.test":          http.StatusNotFound,
		"ghost.tunnel.test":        http.StatusBadGateway,
		"acme.tunnel.test.evil.io": http.StatusNotFound,
	}
	for host, want := range cases {
		if got := get(t, server, host, "/").StatusCode; got != want {
			t.Fatalf("%s = %d, want %d", host, got, want)
		}
	}
	if acme.opened != 0 {
		t.Fatal("a refused host opened a stream on the live link")
	}
}

func TestForwardedHeadersCannotBeSpoofedByTheVisitor(t *testing.T) {
	server := startEdge(t, table{"acme": &pipeLink{handler: observer("acme")}})
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "acme.tunnel.test"
	request.Header.Set("X-Forwarded-Host", "victim.example")
	request.Header.Set("X-Forwarded-Proto", "gopher")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var seen map[string]string
	if err := json.NewDecoder(response.Body).Decode(&seen); err != nil {
		t.Fatal(err)
	}
	if seen["fhost"] != "acme.tunnel.test" || seen["proto"] != "https" {
		t.Fatalf("spoofed headers reached the agent: %v", seen)
	}
}

func TestForwardedForIsAppendedBehindALoopbackFrontAndDroppedOtherwise(t *testing.T) {
	cases := []struct {
		peer string
		want []string
	}{
		{"127.0.0.1:4000", []string{"203.0.113.9, 127.0.0.1"}},
		{"[::1]:4000", []string{"203.0.113.9, ::1"}},
		{"198.51.100.4:4000", []string{"198.51.100.4"}},
	}
	for _, tc := range cases {
		in, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://acme.tunnel.test/", nil)
		if err != nil {
			t.Fatal(err)
		}
		in.RemoteAddr = tc.peer
		in.Header.Set("X-Forwarded-For", "203.0.113.9")
		out := in.Clone(in.Context())
		out.Header.Del("X-Forwarded-For")
		proxy, err := New("tunnel.test", table{}, "https", time.Second, slog.New(slog.DiscardHandler))
		if err != nil {
			t.Fatal(err)
		}
		proxy.rewrite(&httputil.ProxyRequest{In: in, Out: out})
		if got := out.Header.Values("X-Forwarded-For"); len(got) != 1 || got[0] != tc.want[0] {
			t.Fatalf("peer %s forwarded-for = %v, want %v", tc.peer, got, tc.want)
		}
	}
}

func TestStreamedChunksArriveBeforeTheResponseEnds(t *testing.T) {
	release := make(chan struct{})
	streamer := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, "first\n")
		flusher.Flush()
		<-release
		_, _ = io.WriteString(w, "second\n")
	})
	server := startEdge(t, table{"acme": &pipeLink{handler: streamer}})
	response := get(t, server, "acme.tunnel.test", "/stream")
	buffer := make([]byte, 64)
	n, err := response.Body.Read(buffer)
	if err != nil || string(buffer[:n]) != "first\n" {
		t.Fatalf("first read = %q, %v; the edge buffered the stream", buffer[:n], err)
	}
	close(release)
	rest, err := io.ReadAll(response.Body)
	if err != nil || string(rest) != "second\n" {
		t.Fatalf("rest = %q, %v", rest, err)
	}
}

func TestWebSocketUpgradesSurviveTheTunnel(t *testing.T) {
	echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer func() { _ = socket.CloseNow() }()
		kind, message, err := socket.Read(r.Context())
		if err != nil {
			return
		}
		_ = socket.Write(r.Context(), kind, append([]byte("echo:"), message...))
	})
	server := startEdge(t, table{"acme": &pipeLink{handler: echo}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	socket, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/ws", &websocket.DialOptions{Host: "acme.tunnel.test"})
	if response != nil && response.Body != nil {
		defer func() { _ = response.Body.Close() }()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = socket.CloseNow() }()
	if err := socket.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	_, reply, err := socket.Read(ctx)
	if err != nil || string(reply) != "echo:ping" {
		t.Fatalf("reply = %q, %v", reply, err)
	}
}

func TestAnAgentThatCannotAnswerIsABadGateway(t *testing.T) {
	broken := &failingLink{}
	server := startEdge(t, table{"acme": broken})
	if got := get(t, server, "acme.tunnel.test", "/").StatusCode; got != http.StatusBadGateway {
		t.Fatalf("broken link = %d, want 502", got)
	}
}

type failingLink struct{}

func (failingLink) Open(context.Context) (net.Conn, error) { return nil, net.ErrClosed }
func (failingLink) Close(tunnel.CloseReason) error         { return nil }
