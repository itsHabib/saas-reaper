// Package link is the control-connection mechanism: one WebSocket per agent, multiplexed into
// per-request streams with yamux. The server opens streams toward the agent; the agent accepts
// them. Policy never sees a WebSocket or a yamux frame, only a tunnel.Link.
package link

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/tunnel"
)

// readLimit must exceed the largest yamux frame, which is one stream window plus its header.
var readLimit = int64(yamux.DefaultConfig().MaxStreamWindowSize) + 4096

// Close status codes in the application range carry the eviction reason to the agent. They are
// public behavior: a superseded agent must stop, a revoked one can never come back.
const (
	statusSuperseded websocket.StatusCode = 4001
	statusRevoked    websocket.StatusCode = 4003
)

// farewellGrace bounds how long an eviction waits for the agent to acknowledge the close frame
// before the socket is cut. A healthy agent answers in milliseconds; a wedged one is not
// allowed to keep its streams alive behind a handshake it will never finish.
const farewellGrace = 2 * time.Second

// Config tunes liveness detection for both ends of a link.
type Config struct {
	KeepAliveInterval time.Duration
	WriteTimeout      time.Duration
}

// DefaultConfig is the production liveness shape.
func DefaultConfig() Config {
	return Config{KeepAliveInterval: 10 * time.Second, WriteTimeout: 10 * time.Second}
}

func (c Config) validate() error {
	if c.KeepAliveInterval <= 0 || c.WriteTimeout <= 0 {
		return errors.New("link keep-alive interval and write timeout must be positive")
	}
	return nil
}

func (c Config) yamux() *yamux.Config {
	configuration := yamux.DefaultConfig()
	configuration.EnableKeepAlive = true
	configuration.KeepAliveInterval = c.KeepAliveInterval
	configuration.ConnectionWriteTimeout = c.WriteTimeout
	configuration.LogOutput = io.Discard
	return configuration
}

// Session is the server end of one attached agent link. It satisfies tunnel.Link.
type Session struct {
	socket *websocket.Conn
	mux    *yamux.Session
	closed atomic.Bool
}

// Open starts one fresh stream to the agent for one request. yamux's open has no context of
// its own, so the wait is raced against ctx; a stream that arrives after the caller gave up is
// closed rather than leaked.
func (s *Session) Open(ctx context.Context) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.closed.Load() {
		return nil, errors.New("link is closed")
	}
	type opened struct {
		stream net.Conn
		err    error
	}
	result := make(chan opened, 1)
	go func() {
		stream, err := s.mux.Open()
		result <- opened{stream: stream, err: err}
	}()
	select {
	case <-ctx.Done():
		go func() {
			if late := <-result; late.err == nil {
				_ = late.stream.Close()
			}
		}()
		return nil, ctx.Err()
	case ready := <-result:
		if ready.err != nil {
			return nil, ready.err
		}
		return ready.stream, nil
	}
}

// Close refuses new streams at once and tells the agent why in the background. The close
// frame goes out before the multiplexer is torn down so the reason reaches the agent; the
// handshake is given farewellGrace and then the socket is cut, so policy never blocks and a
// wedged peer cannot hold its streams open. Done fires once the multiplexer is closed.
func (s *Session) Close(reason tunnel.CloseReason) error {
	if s.closed.Swap(true) {
		return nil
	}
	go s.farewell(reason)
	return nil
}

func (s *Session) farewell(reason tunnel.CloseReason) {
	acknowledged := make(chan struct{})
	go func() {
		defer close(acknowledged)
		_ = s.socket.Close(closeStatus(reason), string(reason))
	}()
	select {
	case <-acknowledged:
	case <-time.After(farewellGrace):
	}
	s.cut()
}

// cut ends the link immediately with no farewell; the shutdown deadline uses it.
func (s *Session) cut() {
	s.closed.Store(true)
	_ = s.socket.CloseNow()
	_ = s.mux.Close()
}

// Done closes when the link has ended for any reason, including a missed keep-alive.
func (s *Session) Done() <-chan struct{} {
	return s.mux.CloseChan()
}

func newSession(socket *websocket.Conn, conn net.Conn, configuration Config) (*Session, error) {
	mux, err := yamux.Client(conn, configuration.yamux())
	if err != nil {
		return nil, err
	}
	return &Session{socket: socket, mux: mux}, nil
}

func closeStatus(reason tunnel.CloseReason) websocket.StatusCode {
	switch reason {
	case tunnel.CloseSuperseded:
		return statusSuperseded
	case tunnel.CloseRevoked:
		return statusRevoked
	}
	return websocket.StatusGoingAway
}

// EvictedError is reported by the agent-side listener when the server ended the link on purpose.
type EvictedError struct {
	Reason tunnel.CloseReason
}

func (e EvictedError) Error() string {
	return fmt.Sprintf("the server evicted this agent: %s", e.Reason)
}

// Permanent reports that reconnecting with the same credential is pointless or harmful.
func (EvictedError) Permanent() bool { return true }

// observedConn hands yamux the WebSocket as a net.Conn and remembers the first read failure,
// which is where the server's close status arrives on the agent side.
type observedConn struct {
	net.Conn
	mu      sync.Mutex
	readErr error
}

func (o *observedConn) Read(p []byte) (int, error) {
	n, err := o.Conn.Read(p)
	if err == nil {
		return n, nil
	}
	o.mu.Lock()
	if o.readErr == nil {
		o.readErr = err
	}
	o.mu.Unlock()
	return n, err
}

func (o *observedConn) closeStatus() websocket.StatusCode {
	o.mu.Lock()
	defer o.mu.Unlock()
	return websocket.CloseStatus(o.readErr)
}

// Listener is the agent end: every accepted connection is one request from the edge.
type Listener struct {
	socket *websocket.Conn
	conn   *observedConn
	mux    *yamux.Session
}

// Accept waits for the next request stream.
func (l *Listener) Accept() (net.Conn, error) {
	return l.mux.Accept()
}

// Reason reports why the link ended once it has: an EvictedError when the server ended it on
// purpose, nil for any other loss. Before the link ends it is nil.
func (l *Listener) Reason() error {
	switch l.conn.closeStatus() {
	case statusSuperseded:
		return EvictedError{Reason: tunnel.CloseSuperseded}
	case statusRevoked:
		return EvictedError{Reason: tunnel.CloseRevoked}
	}
	return nil
}

// Close ends the link.
func (l *Listener) Close() error {
	muxErr := l.mux.Close()
	socketErr := l.socket.CloseNow()
	if errors.Is(socketErr, net.ErrClosed) {
		socketErr = nil
	}
	return errors.Join(muxErr, socketErr)
}

// Addr describes the multiplexed listener.
func (l *Listener) Addr() net.Addr {
	return l.mux.Addr()
}

func newListener(socket *websocket.Conn, conn net.Conn, configuration Config) (*Listener, error) {
	observed := &observedConn{Conn: conn}
	mux, err := yamux.Server(observed, configuration.yamux())
	if err != nil {
		return nil, err
	}
	return &Listener{socket: socket, conn: observed, mux: mux}, nil
}
