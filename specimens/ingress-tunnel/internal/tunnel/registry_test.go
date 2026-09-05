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
	firstGeneration, previous := registry.Attach("acme", first)
	if previous != nil {
		t.Fatal("first attach reported a previous link")
	}
	secondGeneration, previous := registry.Attach("acme", second)
	if previous != first || secondGeneration <= firstGeneration {
		t.Fatalf("second attach returned previous=%v generation=%d", previous, secondGeneration)
	}
	if registry.Detach("acme", firstGeneration) {
		t.Fatal("stale generation detached the live link")
	}
	if link, ok := registry.Lookup("acme"); !ok || link != second {
		t.Fatalf("lookup after stale detach = %v,%t want second", link, ok)
	}
	if !registry.Detach("acme", secondGeneration) {
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
	registry.Attach("acme", link)
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
