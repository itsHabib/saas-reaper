package tunnel

import (
	"context"
	"net"
	"testing"
)

type fakeLink struct {
	name   string
	closed int
	reason CloseReason
}

func (f *fakeLink) Open(context.Context) (net.Conn, error) { return nil, nil }
func (f *fakeLink) Close(reason CloseReason) error {
	f.closed++
	f.reason = reason
	return nil
}

func TestRegistrySupersedesAndIgnoresStaleDetach(t *testing.T) {
	registry := NewRegistry()
	first := &fakeLink{name: "first"}
	second := &fakeLink{name: "second"}
	if previous := registry.Attach("acme", first, 1); previous != nil {
		t.Fatal("first attach reported a previous link")
	}
	if previous := registry.Attach("acme", second, 2); previous != first {
		t.Fatalf("second attach returned previous=%v", previous)
	}
	if registry.Detach("acme", 1) {
		t.Fatal("stale generation detached the live link")
	}
	if link, ok := registry.Lookup("acme"); !ok || link != second {
		t.Fatalf("lookup after stale detach = %v,%t want second", link, ok)
	}
	if !registry.Detach("acme", 2) {
		t.Fatal("current generation failed to detach")
	}
	if registry.Presence("acme") != PresenceAbsent {
		t.Fatal("detached tunnel still present")
	}
}

func TestRegistryEvictReturnsWhateverServes(t *testing.T) {
	registry := NewRegistry()
	if _, ok := registry.Evict("acme"); ok {
		t.Fatal("evicting an empty subdomain reported a link")
	}
	link := &fakeLink{name: "only"}
	registry.Attach("acme", link, 1)
	if live := registry.Live(); len(live) != 1 {
		t.Fatalf("live snapshot = %v", live)
	}
	evicted, ok := registry.Evict("acme")
	if !ok || evicted != link {
		t.Fatalf("evict = %v,%t", evicted, ok)
	}
	if _, ok := registry.Lookup("acme"); ok {
		t.Fatal("evicted link still routable")
	}
	if _, ok := registry.Lookup("umbrella"); ok {
		t.Fatal("unknown subdomain resolved to a link")
	}
}
