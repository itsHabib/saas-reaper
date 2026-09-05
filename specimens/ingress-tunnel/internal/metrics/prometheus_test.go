package metrics

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/edge"
)

func scrape(t *testing.T, registry *Registry) string {
	t.Helper()
	server := httptest.NewServer(registry.Handler())
	defer server.Close()
	response, err := http.Get(server.URL) //nolint:noctx // a test scrape
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestNewRequiresTheLiveCounter(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("nil live counter accepted")
	}
}

func TestObservationsBecomeSeries(t *testing.T) {
	registry, err := New(func() int { return 2 })
	if err != nil {
		t.Fatal(err)
	}
	registry.Request(edge.Observation{Subdomain: "acme", Status: 200, Bytes: 512, Duration: 20 * time.Millisecond})
	registry.Request(edge.Observation{Subdomain: "acme", Status: 101, Upgraded: true, Duration: time.Millisecond})
	registry.Request(edge.Observation{Subdomain: "", Status: 404, Duration: time.Millisecond})
	registry.StreamOpen(edge.StreamOpen{Subdomain: "acme", Duration: 2 * time.Millisecond})
	registry.StreamOpen(edge.StreamOpen{Subdomain: "acme", Duration: time.Millisecond, Err: errors.New("link is closed")})
	body := scrape(t, registry)
	for _, want := range []string{
		`reaper_tunnel_requests_total{status="2xx",subdomain="acme"} 1`,
		`reaper_tunnel_requests_total{status="1xx",subdomain="acme"} 1`,
		`reaper_tunnel_requests_total{status="4xx",subdomain="none"} 1`,
		`reaper_tunnel_response_bytes_total{subdomain="acme"} 512`,
		`reaper_tunnel_upgrades_total{subdomain="acme"} 1`,
		`reaper_tunnel_stream_open_failures_total{subdomain="acme"} 1`,
		`reaper_tunnel_stream_open_seconds_count{subdomain="acme"} 2`,
		`reaper_tunnel_links_live 2`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("scrape lacks %q:\n%s", want, body)
		}
	}
}
