// Package edge is the public ingress mechanism: it resolves the Host header to a subdomain,
// looks up the live link, and reverse-proxies the request down a fresh stream. Streaming
// bodies, WebSocket upgrades, and forwarded headers are all the standard library's proxy.
package edge

import (
	"context"
	"encoding/json"
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
	domain   string
	router   Router
	proxy    *httputil.ReverseProxy
	logger   *slog.Logger
	forward  string
	observer Observer
}

// route is what one request resolved to; it rides the request context from ServeHTTP to the
// rewrite and the dial so the host is parsed once and the link is the one that was looked up.
type route struct {
	subdomain string
	link      tunnel.Link
}

type routeKey struct{}

// New validates the edge. forwardProto is the scheme visitors used to reach this edge, which
// a TLS-terminating front such as Caddy already reports and a bare deployment must declare.
// observer receives every request outcome; pass NoObserver to record nothing.
func New(domain string, router Router, forwardProto string, headerTimeout time.Duration, observer Observer, logger *slog.Logger) (*Proxy, error) {
	if strings.TrimSpace(domain) == "" || router == nil || observer == nil || logger == nil {
		return nil, errors.New("tunnel domain, router, observer, and logger are required")
	}
	if forwardProto != "http" && forwardProto != "https" {
		return nil, errors.New("forwarded protocol must be http or https")
	}
	if headerTimeout <= 0 {
		return nil, errors.New("response header timeout must be positive")
	}
	p := &Proxy{domain: strings.ToLower(domain), router: router, logger: logger, forward: forwardProto, observer: observer}
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
// Every outcome, refused or proxied, is observed once and logged once. The standard proxy
// abandons a response whose copy fails by panicking with http.ErrAbortHandler; the observation
// is deferred so a truncated response is recorded as aborted before the panic continues.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	recorded := &recorder{ResponseWriter: w}
	// The subdomain is resolved before anything that can panic, so an aborted response is
	// attributed to the tunnel it belonged to rather than to no tunnel at all.
	subdomain, ok := tunnel.HostSubdomain(r.Host, p.domain)
	defer func() {
		aborted := recover()
		p.record(r, subdomain, recorded, started, aborted != nil)
		if aborted != nil {
			panic(aborted)
		}
	}()
	if !ok {
		refuse(recorded, http.StatusNotFound, "no tunnel is served at this host")
		return
	}
	p.serve(recorded, r, subdomain)
}

func (p *Proxy) record(r *http.Request, subdomain string, recorded *recorder, started time.Time, aborted bool) {
	observation := Observation{
		Subdomain: subdomain,
		Method:    r.Method,
		Status:    recorded.status,
		Bytes:     recorded.bytes,
		Duration:  time.Since(started),
		Upgraded:  recorded.upgraded,
		Aborted:   aborted,
		Peer:      r.RemoteAddr,
	}
	p.observer.Request(observation)
	p.logger.Info("edge request",
		"subdomain", subdomain,
		"host", r.Host,
		"method", r.Method,
		"path", r.URL.Path,
		"status", observation.Status,
		"bytes", observation.Bytes,
		"duration", observation.Duration,
		"upgraded", observation.Upgraded,
		"aborted", observation.Aborted,
		"peer", observation.Peer,
	)
}

// serve answers one request for a resolved subdomain.
func (p *Proxy) serve(w http.ResponseWriter, r *http.Request, subdomain string) {
	link, ok := p.router.Lookup(subdomain)
	if !ok {
		refuse(w, http.StatusBadGateway, "no agent is connected for this host")
		return
	}
	resolved := route{subdomain: subdomain, link: link}
	p.proxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), routeKey{}, resolved)))
}

// rewrite keeps the visitor's Host so the agent can route on it, and stamps the forwarded
// triple from what this edge observed rather than from what the visitor claimed. The standard
// proxy strips the inbound forwarded headers before this runs; the client chain is restored
// only when the immediate peer is the local TLS front, so a visitor who reaches the edge
// directly cannot supply one, and Caddy's real client address is appended to rather than lost.
func (p *Proxy) rewrite(request *httputil.ProxyRequest) {
	resolved, _ := request.In.Context().Value(routeKey{}).(route)
	request.SetURL(&url.URL{Scheme: "http", Host: resolved.subdomain + ".tunnel.internal"})
	request.Out.Host = request.In.Host
	if chain := trustedForwardedFor(request.In); len(chain) > 0 {
		request.Out.Header["X-Forwarded-For"] = chain
	}
	request.SetXForwarded()
	request.Out.Header.Set("X-Forwarded-Proto", p.forward)
}

// trustedForwardedFor returns the inbound X-Forwarded-For chain when the peer is a loopback
// front such as Caddy on the same host, and nothing otherwise.
func trustedForwardedFor(in *http.Request) []string {
	host, _, err := net.SplitHostPort(in.RemoteAddr)
	if err != nil {
		return nil
	}
	peer := net.ParseIP(host)
	if peer == nil || !peer.IsLoopback() {
		return nil
	}
	return in.Header.Values("X-Forwarded-For")
}

// transport dials one fresh stream per request. Keep-alives are disabled on purpose: a pooled
// stream would outlive the link that owns it, and a superseded agent must never receive a
// request meant for its successor. Streams are cheap; the trade is deliberate.
func (p *Proxy) transport(headerTimeout time.Duration) http.RoundTripper {
	return &http.Transport{
		DialContext:           p.dialRoute,
		DisableKeepAlives:     true,
		ResponseHeaderTimeout: headerTimeout,
		ExpectContinueTimeout: time.Second,
	}
}

func (p *Proxy) dialRoute(ctx context.Context, _, _ string) (net.Conn, error) {
	resolved, ok := ctx.Value(routeKey{}).(route)
	if !ok {
		return nil, errors.New("edge: request carries no tunnel route")
	}
	started := time.Now()
	conn, err := resolved.link.Open(ctx)
	p.observer.StreamOpen(StreamOpen{Subdomain: resolved.subdomain, Duration: time.Since(started), Err: err})
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
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
