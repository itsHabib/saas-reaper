package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/tunnel"
)

type scriptedDialer struct {
	mu        sync.Mutex
	outcomes  []error
	listeners chan net.Listener
	dials     int
}

func (d *scriptedDialer) Dial(context.Context) (net.Listener, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dials++
	if len(d.outcomes) > 0 {
		err := d.outcomes[0]
		d.outcomes = d.outcomes[1:]
		if err != nil {
			return nil, err
		}
	}
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	d.listeners <- listener
	return listener, nil
}

type recordingWaiter struct {
	mu     sync.Mutex
	delays []time.Duration
}

func (w *recordingWaiter) Wait(ctx context.Context, delay time.Duration) error {
	w.mu.Lock()
	w.delays = append(w.delays, delay)
	w.mu.Unlock()
	return ctx.Err()
}

type refusal struct{ permanent bool }

func (refusal) Error() string     { return "refused" }
func (r refusal) Permanent() bool { return r.permanent }
func schedule(t *testing.T) tunnel.Schedule {
	t.Helper()
	s, err := tunnel.NewSchedule([]time.Duration{time.Millisecond, 2 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newAgent(t *testing.T, dialer Dialer, target string, waiter Waiter) *Agent {
	t.Helper()
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(dialer, parsed, schedule(t), waiter, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestNewValidatesTheTarget(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	if _, err := New(nil, &url.URL{Scheme: "http", Host: "x"}, schedule(t), TimerWaiter{}, logger); err == nil {
		t.Fatal("nil dialer accepted")
	}
	if _, err := New(&scriptedDialer{}, &url.URL{Scheme: "ftp", Host: "x"}, schedule(t), TimerWaiter{}, logger); err == nil {
		t.Fatal("ftp target accepted")
	}
	if _, err := New(&scriptedDialer{}, &url.URL{Scheme: "http"}, schedule(t), TimerWaiter{}, logger); err == nil {
		t.Fatal("hostless target accepted")
	}
}

func TestRequestsReachTheTargetWithForwardingPreserved(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"host":  r.Host,
			"for":   r.Header.Get("X-Forwarded-For"),
			"fhost": r.Header.Get("X-Forwarded-Host"),
			"proto": r.Header.Get("X-Forwarded-Proto"),
			"path":  r.URL.Path,
		})
	}))
	defer target.Close()
	dialer := &scriptedDialer{listeners: make(chan net.Listener, 1)}
	a := newAgent(t, dialer, target.URL, TimerWaiter{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	listener := <-dialer.listeners
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+listener.Addr().String()+"/hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "acme.tunnel.test"
	request.Header.Set("X-Forwarded-For", "203.0.113.9")
	request.Header.Set("X-Forwarded-Host", "acme.tunnel.test")
	request.Header.Set("X-Forwarded-Proto", "https")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var seen map[string]string
	if err := json.NewDecoder(response.Body).Decode(&seen); err != nil {
		t.Fatal(err)
	}
	targetHost := target.Listener.Addr().String()
	if seen["host"] != targetHost || seen["for"] != "203.0.113.9" || seen["fhost"] != "acme.tunnel.test" || seen["proto"] != "https" || seen["path"] != "/hello" {
		t.Fatalf("target saw %v", seen)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run after cancel = %v", err)
	}
}

func TestTransientDialFailuresFollowTheScheduleThenReconnect(t *testing.T) {
	dialer := &scriptedDialer{
		outcomes:  []error{errors.New("refused"), errors.New("refused"), errors.New("refused")},
		listeners: make(chan net.Listener, 1),
	}
	waiter := &recordingWaiter{}
	ctx, cancel := context.WithCancel(context.Background())
	a := newAgent(t, dialer, "http://127.0.0.1:1", waiter)
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	listener := <-dialer.listeners
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		dialer.mu.Lock()
		dials := dialer.dials
		dialer.mu.Unlock()
		if dials >= 5 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run = %v", err)
	}
	waiter.mu.Lock()
	defer waiter.mu.Unlock()
	want := []time.Duration{time.Millisecond, 2 * time.Millisecond, 2 * time.Millisecond, time.Millisecond}
	if len(waiter.delays) < len(want) {
		t.Fatalf("delays = %v, want at least %v", waiter.delays, want)
	}
	for index, delay := range want {
		if waiter.delays[index] != delay {
			t.Fatalf("delays = %v, want prefix %v", waiter.delays, want)
		}
	}
}

func TestAPermanentRefusalEndsTheAgent(t *testing.T) {
	dialer := &scriptedDialer{outcomes: []error{refusal{permanent: true}}, listeners: make(chan net.Listener, 1)}
	a := newAgent(t, dialer, "http://127.0.0.1:1", &recordingWaiter{})
	err := a.Run(context.Background())
	var refused refusal
	if !errors.As(err, &refused) {
		t.Fatalf("run = %v, want the refusal", err)
	}
	if dialer.dials != 1 {
		t.Fatalf("dials = %d after a permanent refusal", dialer.dials)
	}
}

func TestANonPermanentRefusalIsRetried(t *testing.T) {
	dialer := &scriptedDialer{outcomes: []error{refusal{permanent: false}}, listeners: make(chan net.Listener, 1)}
	waiter := &recordingWaiter{}
	ctx, cancel := context.WithCancel(context.Background())
	a := newAgent(t, dialer, "http://127.0.0.1:1", waiter)
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	listener := <-dialer.listeners
	_ = listener.Close()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run = %v", err)
	}
	if dialer.dials < 2 {
		t.Fatalf("dials = %d, want a retry after the temporary refusal", dialer.dials)
	}
}
