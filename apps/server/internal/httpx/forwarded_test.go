package httpx

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestForwarded(t *testing.T) {
	tests := []struct {
		name       string
		hops       int
		peers      []netip.Prefix
		remote     string
		tls        bool
		xff, proto []string
		wantIP     string
		wantScheme string
	}{
		{"no trusted proxies ignores the headers", 0, nil, "192.0.2.10:5555", false, []string{"203.0.113.7"}, []string{"https"}, "192.0.2.10", "http"},
		{"one hop takes the rightmost entry", 1, nil, "10.0.0.2:5555", false, []string{"198.51.100.1, 203.0.113.7"}, []string{"https"}, "203.0.113.7", "https"},
		{"two hops take the second from the right", 2, nil, "10.0.0.3:5555", false, []string{"203.0.113.7, 10.0.0.2"}, nil, "203.0.113.7", "http"},
		{"repeated header lines are one list", 2, nil, "10.0.0.3:5555", false, []string{"203.0.113.7", "10.0.0.2"}, nil, "203.0.113.7", "http"},
		{"fewer entries than hops falls back to the peer", 2, nil, "10.0.0.3:5555", false, []string{"203.0.113.7"}, nil, "10.0.0.3", "http"},
		{"a malformed entry falls back to the peer", 1, nil, "10.0.0.2:5555", false, []string{"not-an-ip"}, nil, "10.0.0.2", "http"},
		{"bracketed IPv6 with a port", 1, nil, "10.0.0.2:5555", false, []string{"[2001:db8::1]:443"}, nil, "2001:db8::1", "http"},
		{"an unknown proto is ignored", 1, nil, "10.0.0.2:5555", false, nil, []string{"gopher"}, "10.0.0.2", "http"},
		{"the last proto entry wins", 1, nil, "10.0.0.2:5555", false, nil, []string{"http, https"}, "10.0.0.2", "https"},
		{"TLS on the connection is https", 0, nil, "192.0.2.10:5555", true, nil, nil, "192.0.2.10", "https"},
		{"a peer outside the CIDRs keeps RemoteAddr and the connection scheme", 1, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, "192.0.2.10:5555", false, []string{"203.0.113.7"}, []string{"https"}, "192.0.2.10", "http"},
		{"a peer inside a CIDR gets the hops rule applied", 1, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, "10.0.0.2:5555", false, []string{"203.0.113.7"}, []string{"https"}, "203.0.113.7", "https"},
		{"an IPv4-mapped peer matches an IPv4 prefix", 1, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, "[::ffff:10.0.0.2]:5555", false, []string{"203.0.113.7"}, []string{"https"}, "203.0.113.7", "https"},
		{"empty prefixes keep the old hops-only behaviour", 1, nil, "10.0.0.2:5555", false, []string{"203.0.113.7"}, []string{"https"}, "203.0.113.7", "https"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remote
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
			}
			for _, v := range tc.xff {
				req.Header.Add("X-Forwarded-For", v)
			}
			for _, v := range tc.proto {
				req.Header.Add("X-Forwarded-Proto", v)
			}

			var ip, scheme string
			Forwarded(tc.hops, tc.peers)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				ip, scheme = ClientIP(r), Scheme(r)
			})).ServeHTTP(httptest.NewRecorder(), req)

			if ip != tc.wantIP || scheme != tc.wantScheme {
				t.Errorf("ClientIP/Scheme = %q/%q, want %q/%q", ip, scheme, tc.wantIP, tc.wantScheme)
			}
		})
	}
}

func TestClientIPAndSchemeWithoutTheMiddleware(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.10:5555"
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	if ClientIP(req) != "192.0.2.10" || Scheme(req) != "http" {
		t.Errorf("ClientIP/Scheme = %q/%q", ClientIP(req), Scheme(req))
	}
}
