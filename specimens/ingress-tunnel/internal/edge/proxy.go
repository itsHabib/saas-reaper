// Package edge is the public ingress mechanism: it resolves the Host header to a subdomain,
// looks up the live link, and reverse-proxies the request down a fresh stream. Streaming
// bodies, WebSocket upgrades, and forwarded headers are all the standard library's proxy.
package edge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/tunnel"
)

// Router is the routing table the edge consults.
type Router interface {
	Lookup(string) (tunnel.Link, bool)
}

// Proxy serves every claimed subdomain beneath one tunnel domain.
type Proxy struct {
	domain  string
	router  Router
	proxy   *httputil.ReverseProxy
	logger  *slog.Logger
	forward string
}

type linkKey struct{}

// New validates the edge. forwardProto is the scheme visitors used to reach this edge, which
// a TLS-terminating front such as Caddy already reports and a bare deployment must declare.
func New(domain string, router Router, forwardProto string, headerTimeout time.Duration, logger *slog.Logger) (*Proxy, error) {
	if strings.TrimSpace(domain) == "" || router == nil || logger == nil {
		return nil, errors.New("tunnel domain, router, and logger are required")
	}
	if forwardProto != "http" && forwardProto != "https" {
		return nil, errors.New("forwarded protocol must be http or https")
	}
	if headerTimeout <= 0 {
		return nil, errors.New("response header timeout must be positive")
	}
	p := &Proxy{domain: strings.ToLower(domain), router: router, logger: logger, forward: forwardProto}
	p.proxy = &httputil.ReverseProxy{
		Rewrite:       p.rewrite,
		Transport:     p.transport(headerTimeout),
		FlushInterval: -1,
		ErrorHandler:  p.upstreamError,
		ErrorLog:      slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	return p, nil
}

// ServeHTTP refuses any host that does not name exactly one live tunnel. A claimed-but-offline
// subdomain and an unclaimed one answer identically so the edge never reveals which names exist.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	subdomain, ok := tunnel.HostSubdomain(r.Host, p.domain)
	if !ok {
		refuse(w, http.StatusNotFound, "no tunnel is served at this host")
		return
	}
	link, ok := p.router.Lookup(subdomain)
	if !ok {
		refuse(w, http.StatusBadGateway, "no agent is connected for this host")
		return
	}
	p.proxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), linkKey{}, link)))
}

// rewrite keeps the visitor's Host so the agent can route on it, and stamps the forwarded
// triple from what this edge observed rather than from what the visitor claimed.
func (p *Proxy) rewrite(request *httputil.ProxyRequest) {
	subdomain, _ := tunnel.HostSubdomain(request.In.Host, p.domain)
	request.SetURL(&url.URL{Scheme: "http", Host: subdomain + ".tunnel.internal"})
	request.Out.Host = request.In.Host
	request.SetXForwarded()
	request.Out.Header.Set("X-Forwarded-Proto", p.forward)
}

// transport dials one fresh stream per request. Keep-alives are disabled on purpose: a pooled
// stream would outlive the link that owns it, and a superseded agent must never receive a
// request meant for its successor.
func (p *Proxy) transport(headerTimeout time.Duration) http.RoundTripper {
	return &http.Transport{
		DialContext:           dialLink,
		DisableKeepAlives:     true,
		ResponseHeaderTimeout: headerTimeout,
		ExpectContinueTimeout: time.Second,
	}
}

func dialLink(ctx context.Context, _, _ string) (net.Conn, error) {
	link, ok := ctx.Value(linkKey{}).(tunnel.Link)
	if !ok {
		return nil, errors.New("edge: request carries no tunnel link")
	}
	conn, err := link.Open(ctx)
	if err != nil {
		return nil, fmt.Errorf("open tunnel stream: %w", err)
	}
	return conn, nil
}

func (p *Proxy) upstreamError(w http.ResponseWriter, r *http.Request, err error) {
	p.logger.Warn("tunnel upstream failed", "host", r.Host, "error", err)
	if errors.Is(err, context.Canceled) {
		return
	}
	refuse(w, http.StatusBadGateway, "the agent did not answer")
}

func refuse(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "{\"error\":%q}\n", message)
}
