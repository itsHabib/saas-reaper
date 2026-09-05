package link

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/coder/websocket"

	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/tunnel"
)

// Connector is the policy the accept handler drives: prove the credential before upgrading,
// attach after, and report the loss when the link ends.
type Connector interface {
	Authenticate(context.Context, string) (tunnel.Claim, error)
	Attach(context.Context, tunnel.Claim, tunnel.Link) (tunnel.Connection, error)
}

// Handler upgrades authenticated agents into attached links.
type Handler struct {
	connector     Connector
	configuration Config
	logger        *slog.Logger
}

// NewHandler validates the accept side.
func NewHandler(connector Connector, configuration Config, logger *slog.Logger) (*Handler, error) {
	if connector == nil || logger == nil {
		return nil, errors.New("connector and logger are required")
	}
	if err := configuration.validate(); err != nil {
		return nil, err
	}
	return &Handler{connector: connector, configuration: configuration, logger: logger}, nil
}

// ServeHTTP authenticates the bearer token as plain HTTP, so a refused agent reads a status
// code rather than a failed upgrade, then holds the link open until it ends.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

// serve runs for the life of the link. The request context ends when the client goes away, so
// the attachment is registered against a detached background context and cleaned up explicitly.
func (h *Handler) serve(ctx context.Context, claim tunnel.Claim, socket *websocket.Conn) {
	linkCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	session, err := newSession(socket, websocket.NetConn(linkCtx, socket, websocket.MessageBinary), h.configuration)
	if err != nil {
		h.logger.Warn("multiplex agent link", "subdomain", claim.Subdomain, "error", err)
		_ = socket.Close(websocket.StatusInternalError, "multiplex failed")
		return
	}
	connection, err := h.connector.Attach(linkCtx, claim, session)
	if err != nil {
		h.logger.Warn("attach agent link", "subdomain", claim.Subdomain, "error", err)
		_ = session.Close(closeReasonFor(err))
		return
	}
	h.logger.Info("agent attached", "subdomain", claim.Subdomain)
	select {
	case <-session.Done():
	case <-ctx.Done():
		_ = session.Close(tunnel.CloseShutdown)
	}
	if err := connection.Lost(context.WithoutCancel(linkCtx)); err != nil {
		h.logger.Error("record agent disconnect", "subdomain", claim.Subdomain, "error", err)
	}
	h.logger.Info("agent detached", "subdomain", claim.Subdomain)
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

// Permanent reports whether retrying with the same credential can never succeed.
func (r RejectedError) Permanent() bool {
	return r.Status == http.StatusUnauthorized || r.Status == http.StatusForbidden
}
