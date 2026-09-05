package tunnel

import "testing"

func TestHostSubdomainResolvesExactlyOneLabel(t *testing.T) {
	cases := []struct {
		host string
		want string
		ok   bool
	}{
		{"acme.tunnel.example.com", "acme", true},
		{"ACME.Tunnel.Example.COM", "acme", true},
		{"acme.tunnel.example.com:8443", "acme", true},
		{"acme.tunnel.example.com.", "acme", true},
		{"tunnel.example.com", "", false},
		{"deep.acme.tunnel.example.com", "", false},
		{"acme.example.com", "", false},
		{"acmetunnel.example.com", "", false},
		{"", "", false},
		{"www.tunnel.example.com", "www", true},
		{"-acme.tunnel.example.com", "", false},
		{"acme.tunnel.example.com.evil.net", "", false},
	}
	for _, tc := range cases {
		got, ok := HostSubdomain(tc.host, "tunnel.example.com")
		if got != tc.want || ok != tc.ok {
			t.Fatalf("HostSubdomain(%q) = %q,%t want %q,%t", tc.host, got, ok, tc.want, tc.ok)
		}
	}
}
