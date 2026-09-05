package link

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/coder/websocket"

	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/tunnel"
)

// Connector is the policy the accept handler drives: prove the credential before upgrading,
// attach after, and report the loss when the link ends.
type Connector interface {
	Authenticate(context.Context, string) (tunnel.Claim, error)
	Attach(context.Context, tunnel.Claim, tunnel.Link) (tunnel.Connection, error)
}

// Handler upgrades authenticated agents into attached links and owns their lifetime. Because
// an upgraded connection is hijacked from net/http, neither the request context nor
// http.Server.Shutdown ends a link; Shutdown here does.
type Handler struct {
	connector     Connector
	configuration Config
	logger        *slog.Logger

	mu      sync.Mutex
	closing chan struct{}
	live    map[*Session]struct{}
	links   sync.WaitGroup
}

// NewHandler validates the accept side.
func NewHandler(connector Connector, configuration Config, logger *slog.Logger) (*Handler, error) {
	if connector == nil || logger == nil {
		return nil, errors.New("connector and logger are required")
	}
	if err := configuration.validate(); err != nil {
		return nil, err
	}
	return &Handler{
		connector:     connector,
		configuration: configuration,
		logger:        logger,
		closing:       make(chan struct{}),
		live:          map[*Session]struct{}{},
	}, nil
}

// ServeHTTP authenticates the bearer token as plain HTTP, so a refused agent reads a status
// code rather than a failed upgrade, then holds the link open until it ends.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.isClosing() {
		reject(w, http.StatusServiceUnavailable, "the server is shutting down")
		return
	}
	token, ok := bearer(r)
	if !ok {
		reject(w, http.StatusUnauthorized, "agent token is required")
		return
	}
	claim, err := h.connector.Authenticate(r.Context(), token)
	if errors.Is(err, tunnel.ErrUnauthorized) {
		reject(w, http.StatusUnauthorized, "agent token is not usable")
		return
	}
	if errors.Is(err, tunnel.ErrConflict) {
		reject(w, http.StatusConflict, "another agent holds this claim")
		return
	}
	if err != nil {
		h.logger.Error("authenticate agent", "error", err)
		reject(w, http.StatusInternalServerError, "internal server error")
		return
	}
	socket, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		h.logger.Warn("upgrade agent link", "subdomain", claim.Subdomain, "error", err)
		return
	}
	socket.SetReadLimit(readLimit)
	h.serve(r.Context(), claim, socket)
}

// serve runs for the life of the link. The connection is hijacked, so the request context is
// only a parent for values; the link lives on a detached context and ends when the agent goes
// away, when policy evicts it, or when the handler shuts down.
func (h *Handler) serve(ctx context.Context, claim tunnel.Claim, socket *websocket.Conn) {
	linkCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	session, err := newSession(socket, websocket.NetConn(linkCtx, socket, websocket.MessageBinary), h.configuration)
	if err != nil {
		h.logger.Warn("multiplex agent link", "subdomain", claim.Subdomain, "error", err)
		if err := socket.Close(websocket.StatusInternalError, "multiplex failed"); err != nil {
			h.logger.Warn("close unmultiplexed agent link", "subdomain", claim.Subdomain, "error", err)
		}
		return
	}
	if !h.track(session) {
		_ = session.Close(tunnel.CloseShutdown)
		<-session.Done()
		return
	}
	defer h.untrack(session)
	connection, err := h.connector.Attach(linkCtx, claim, session)
	if err != nil {
		h.logger.Warn("attach agent link", "subdomain", claim.Subdomain, "error", err)
		_ = session.Close(closeReasonFor(err))
		<-session.Done()
		return
	}
	h.logger.Info("agent attached", "subdomain", claim.Subdomain)
	select {
	case <-session.Done():
	case <-h.closing:
		_ = session.Close(tunnel.CloseShutdown)
		<-session.Done()
	}
	if err := connection.Lost(linkCtx); err != nil {
		h.logger.Error("record agent disconnect", "subdomain", claim.Subdomain, "error", err)
	}
	h.logger.Info("agent detached", "subdomain", claim.Subdomain)
}

// Shutdown tells every attached agent the server is going away and waits, up to ctx, for
// their links to end. At the deadline every remaining link is cut outright, so the caller may
// close the store afterwards without a link still writing to it. New upgrades are refused
// from the first call onward.
func (h *Handler) Shutdown(ctx context.Context) error {
	h.mu.Lock()
	if !h.isClosing() {
		close(h.closing)
	}
	h.mu.Unlock()
	done := make(chan struct{})
	go func() {
		h.links.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
	}
	h.mu.Lock()
	for session := range h.live {
		session.cut()
	}
	h.mu.Unlock()
	<-done
	return fmt.Errorf("agent links were cut at the shutdown deadline: %w", ctx.Err())
}

func (h *Handler) isClosing() bool {
	select {
	case <-h.closing:
		return true
	default:
		return false
	}
}

// track registers a link for shutdown; it refuses once shutdown has begun so a link cannot
// slip in after the wait group was drained.
func (h *Handler) track(session *Session) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.isClosing() {
		return false
	}
	h.live[session] = struct{}{}
	h.links.Add(1)
	return true
}

func (h *Handler) untrack(session *Session) {
	h.mu.Lock()
	delete(h.live, session)
	h.mu.Unlock()
	h.links.Done()
}

// closeReasonFor tells a refused agent whether its credential is gone for good.
func closeReasonFor(err error) tunnel.CloseReason {
	if errors.Is(err, tunnel.ErrRevoked) || errors.Is(err, tunnel.ErrUnauthorized) {
		return tunnel.CloseRevoked
	}
	return tunnel.CloseShutdown
}

func bearer(r *http.Request) (string, bool) {
	value := r.Header.Get("Authorization")
	if !strings.HasPrefix(value, "Bearer ") {
		return "", false
	}
	token := strings.TrimPrefix(value, "Bearer ")
	return token, token != ""
}

func reject(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// RejectedError is a dial refused by the server with a status code, as distinct from a network
// failure. Unauthorized rejections are permanent for the agent.
type RejectedError struct {
	Status int
}

func (r RejectedError) Error() string {
	return fmt.Sprintf("server refused the link with status %d", r.Status)
}

// Permanent reports whether retrying with the same credential is pointless: it is unusable, or
// another agent holds the claim and this one lost.
func (r RejectedError) Permanent() bool {
	return r.Status == http.StatusUnauthorized || r.Status == http.StatusForbidden || r.Status == http.StatusConflict
}
