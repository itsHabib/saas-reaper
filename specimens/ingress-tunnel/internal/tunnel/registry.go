package tunnel

import (
	"context"
	"net"
	"sync"
)

// CloseReason tells the far end why its link ended, so an agent can tell a loss it should
// recover from apart from an eviction it must not fight.
type CloseReason string

// The close reasons policy can hand a link.
const (
	CloseSuperseded CloseReason = "superseded"
	CloseRevoked    CloseReason = "revoked"
	CloseShutdown   CloseReason = "shutdown"
)

// Link is one attached agent connection as policy sees it: a way to open a fresh stream to the
// agent and a way to close everything with a stated reason. The mechanism behind it is not
// policy's concern.
type Link interface {
	Open(context.Context) (net.Conn, error)
	Close(CloseReason) error
}

type attachment struct {
	link       Link
	generation uint64
}

// Registry is the in-memory routing table from subdomain to the one live link that serves it.
// Presence is volatile by nature: a restart empties the table and agents reconnect. The table
// does not decide anything; the Service sequences its mutations under its own lock.
type Registry struct {
	mu   sync.Mutex
	live map[string]attachment
}

// NewRegistry constructs an empty routing table.
func NewRegistry() *Registry {
	return &Registry{live: map[string]attachment{}}
}

// Attach installs link as the sole server of subdomain under the caller's generation. When an
// earlier link was attached it is returned so the caller can close it; the earlier link's later
// loss report is ignored because its generation no longer matches.
func (r *Registry) Attach(subdomain string, link Link, generation uint64) Link {
	r.mu.Lock()
	defer r.mu.Unlock()
	previous, had := r.live[subdomain]
	r.live[subdomain] = attachment{link: link, generation: generation}
	if !had {
		return nil
	}
	return previous.link
}

// Detach removes the attachment only when generation still identifies it. It reports whether
// the table changed, which is what decides whether a disconnection is audited.
func (r *Registry) Detach(subdomain string, generation uint64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.live[subdomain]
	if !ok || current.generation != generation {
		return false
	}
	delete(r.live, subdomain)
	return true
}

// Evict removes whatever serves subdomain regardless of generation and returns it for closing.
func (r *Registry) Evict(subdomain string) (Link, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.live[subdomain]
	if !ok {
		return nil, false
	}
	delete(r.live, subdomain)
	return current.link, true
}

// Lookup returns the live link for subdomain.
func (r *Registry) Lookup(subdomain string) (Link, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.live[subdomain]
	if !ok {
		return nil, false
	}
	return current.link, true
}

// Presence reports whether subdomain has a live link.
func (r *Registry) Presence(subdomain string) Presence {
	if _, ok := r.Lookup(subdomain); ok {
		return PresenceLive
	}
	return PresenceAbsent
}

// Live returns one consistent snapshot of every subdomain with a link.
func (r *Registry) Live() map[string]struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	live := make(map[string]struct{}, len(r.live))
	for subdomain := range r.live {
		live[subdomain] = struct{}{}
	}
	return live
}
