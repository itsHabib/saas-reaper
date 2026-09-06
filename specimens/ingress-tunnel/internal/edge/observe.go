package edge

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"time"
)

// Observation is what the edge knows about one request once it has been answered: which
// subdomain it named, how it was answered, how much was written, and how long it took. Refused
// requests carry the status the edge chose and no subdomain when the host resolved to none.
type Observation struct {
	Subdomain string
	Method    string
	Status    int
	Bytes     int64
	Duration  time.Duration
	Upgraded  bool
	Aborted   bool
	Peer      string
}

// StreamOpen is the cost of opening one stream to an agent, with the error when it failed.
type StreamOpen struct {
	Subdomain string
	Duration  time.Duration
	Err       error
}

// Observer receives every request outcome and every stream open. It is consumed by the edge
// and implemented by whatever the composition root wires: a metrics registry, a log, or both.
type Observer interface {
	Request(Observation)
	StreamOpen(StreamOpen)
}

// NoObserver discards everything; it is the default when nothing is wired.
type NoObserver struct{}

// Request discards the observation.
func (NoObserver) Request(Observation) {}

// StreamOpen discards the observation.
func (NoObserver) StreamOpen(StreamOpen) {}

// recorder captures the status and byte count a handler produced without changing what the
// visitor receives. It passes hijacking through so WebSocket upgrades still work, and marks
// them so the observer can tell an upgrade from a request that answered normally.
type recorder struct {
	http.ResponseWriter
	status   int
	bytes    int64
	upgraded bool
	wrote    bool
}

// WriteHeader records the final status. Informational responses such as 103 pass through
// without settling it, so an origin that sends early hints before its real answer is recorded
// by that answer; a 101 is final because the connection changes hands.
func (r *recorder) WriteHeader(status int) {
	informational := status >= 100 && status < 200 && status != http.StatusSwitchingProtocols
	if !r.wrote && !informational {
		r.status = status
		r.wrote = true
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(p []byte) (int, error) {
	if !r.wrote {
		r.status = http.StatusOK
		r.wrote = true
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += int64(n)
	return n, err
}

// Flush keeps streaming responses streaming through the recorder.
func (r *recorder) Flush() {
	flusher, ok := r.ResponseWriter.(http.Flusher)
	if ok {
		flusher.Flush()
	}
}

// Hijack hands the connection to the proxy for an upgrade and records that it happened.
func (r *recorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("edge: response does not support hijacking")
	}
	conn, rw, err := hijacker.Hijack()
	if err == nil {
		r.upgraded = true
		if !r.wrote {
			r.status = http.StatusSwitchingProtocols
			r.wrote = true
		}
	}
	return conn, rw, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *recorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}
