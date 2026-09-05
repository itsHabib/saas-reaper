// Package metrics is the Prometheus mechanism behind the edge's observer: it turns request
// outcomes and stream opens into counters and histograms, and exposes them for a scraper on
// the loopback diagnostics listener.
package metrics

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/edge"
)

// Registry holds every series the server exposes.
type Registry struct {
	registry    *prometheus.Registry
	requests    *prometheus.CounterVec
	bytes       *prometheus.CounterVec
	durations   *prometheus.HistogramVec
	upgrades    *prometheus.CounterVec
	streamOpens *prometheus.HistogramVec
	streamFails *prometheus.CounterVec
}

// New builds the registry. live reports the number of attached links and is sampled on scrape.
func New(live func() int) (*Registry, error) {
	if live == nil {
		return nil, errors.New("live link counter is required")
	}
	r := &Registry{
		registry: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "reaper_tunnel_requests_total",
			Help: "Requests answered by the edge, by subdomain and status class.",
		}, []string{"subdomain", "status"}),
		bytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "reaper_tunnel_response_bytes_total",
			Help: "Response bytes written by the edge, by subdomain.",
		}, []string{"subdomain"}),
		durations: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "reaper_tunnel_request_seconds",
			Help:    "Time from request arrival to the last byte, by subdomain.",
			Buckets: prometheus.ExponentialBuckets(0.005, 2, 12),
		}, []string{"subdomain"}),
		upgrades: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "reaper_tunnel_upgrades_total",
			Help: "WebSocket upgrades carried through the edge, by subdomain.",
		}, []string{"subdomain"}),
		streamOpens: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "reaper_tunnel_stream_open_seconds",
			Help:    "Time to open one stream to an agent, by subdomain.",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 12),
		}, []string{"subdomain"}),
		streamFails: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "reaper_tunnel_stream_open_failures_total",
			Help: "Stream opens that failed, by subdomain.",
		}, []string{"subdomain"}),
	}
	links := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "reaper_tunnel_links_live",
		Help: "Agent links attached right now.",
	}, func() float64 { return float64(live()) })
	for _, collector := range []prometheus.Collector{
		r.requests, r.bytes, r.durations, r.upgrades, r.streamOpens, r.streamFails, links,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	} {
		if err := r.registry.Register(collector); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// Request records one answered request. Unresolvable hosts are grouped under "none" so a scan
// of nonexistent names shows up as one series rather than thousands.
func (r *Registry) Request(observation edge.Observation) {
	subdomain := observation.Subdomain
	if subdomain == "" {
		subdomain = "none"
	}
	r.requests.WithLabelValues(subdomain, statusClass(observation.Status)).Inc()
	r.bytes.WithLabelValues(subdomain).Add(float64(observation.Bytes))
	r.durations.WithLabelValues(subdomain).Observe(observation.Duration.Seconds())
	if observation.Upgraded {
		r.upgrades.WithLabelValues(subdomain).Inc()
	}
}

// StreamOpen records one stream open toward an agent.
func (r *Registry) StreamOpen(open edge.StreamOpen) {
	r.streamOpens.WithLabelValues(open.Subdomain).Observe(open.Duration.Seconds())
	if open.Err != nil {
		r.streamFails.WithLabelValues(open.Subdomain).Inc()
	}
}

// Handler serves the registry in the Prometheus exposition format.
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.registry, promhttp.HandlerOpts{})
}

func statusClass(status int) string {
	if status < 100 || status > 599 {
		return "unknown"
	}
	return strconv.Itoa(status/100) + "xx"
}
