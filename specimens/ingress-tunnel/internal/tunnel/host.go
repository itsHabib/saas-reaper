package tunnel

import (
	"net"
	"strings"
)

// HostSubdomain resolves an inbound Host header against the tunnel domain. It accepts exactly
// one well-formed label below the domain: the apex, nested labels, other domains, and empty
// hosts all resolve to nothing, so a request can never fall through to a tunnel it did not
// name. Only the grammar is applied here; whether a label is claimable is decided at claim time
// and whether it is served is decided by the routing table.
func HostSubdomain(host, domain string) (string, bool) {
	name := strings.ToLower(strings.TrimSuffix(hostWithoutPort(host), "."))
	suffix := "." + strings.ToLower(strings.TrimSuffix(domain, "."))
	if !strings.HasSuffix(name, suffix) {
		return "", false
	}
	label := strings.TrimSuffix(name, suffix)
	if label == "" || strings.Contains(label, ".") {
		return "", false
	}
	if validateLabel(label) != nil {
		return "", false
	}
	return label, true
}

func hostWithoutPort(host string) string {
	if !strings.Contains(host, ":") {
		return host
	}
	name, _, err := net.SplitHostPort(host)
	if err != nil {
		return host
	}
	return name
}
