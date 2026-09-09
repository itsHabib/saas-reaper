package link

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// Dialer opens links from the agent side.
type Dialer struct {
	endpoint      string
	token         string
	client        *http.Client
	configuration Config
}

// NewDialer validates the control endpoint and credential. serverURL is the control base
// (http or https); the connect path is appended here so callers never spell the protocol.
func NewDialer(serverURL, token string, timeout time.Duration, configuration Config) (*Dialer, error) {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("parse tunnel server url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("tunnel server url must use http or https")
	}
	if parsed.Host == "" {
		return nil, errors.New("tunnel server url needs a host")
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("agent token is required")
	}
	if timeout <= 0 {
		return nil, errors.New("dial timeout must be positive")
	}
	if err := configuration.validate(); err != nil {
		return nil, err
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/v1/connect"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return &Dialer{
		endpoint:      parsed.String(),
		token:         token,
		client:        &http.Client{Timeout: timeout},
		configuration: configuration,
	}, nil
}

// Dial authenticates and returns a listener of request streams. A status-code refusal is
// returned as a RejectedError so the agent can tell a revoked credential from a network blip.
func (d *Dialer) Dial(ctx context.Context) (*Listener, error) {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+d.token)
	socket, response, err := websocket.Dial(ctx, d.endpoint, &websocket.DialOptions{
		HTTPClient:      d.client,
		HTTPHeader:      header,
		CompressionMode: websocket.CompressionDisabled,
	})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil && response != nil && response.StatusCode != http.StatusSwitchingProtocols {
		return nil, RejectedError{Status: response.StatusCode}
	}
	if err != nil {
		return nil, fmt.Errorf("dial tunnel server: %w", err)
	}
	socket.SetReadLimit(readLimit)
	linkCtx := context.WithoutCancel(ctx)
	listener, err := newListener(socket, websocket.NetConn(linkCtx, socket, websocket.MessageBinary), d.configuration)
	if err != nil {
		_ = socket.CloseNow()
		return nil, fmt.Errorf("multiplex tunnel link: %w", err)
	}
	return listener, nil
}
