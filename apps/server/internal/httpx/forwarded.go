package httpx

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type forwardedKey struct{}

type forwarded struct {
	clientIP string
	scheme   string
}

// Forwarded resolves the client address and scheme once per request and makes
// them available through ClientIP and Scheme.
//
// With trustedHops = 0 (the default) X-Forwarded-* headers are ignored: the
// peer is the client. With N > 0, the N trusted proxies in front of the
// process each appended one entry to X-Forwarded-For, so the client is the
// Nth entry from the right; everything to its left was written by the client
// and is not trusted. X-Forwarded-Proto's last entry is taken as the scheme.
// X-Forwarded-Host is never honoured: proxies must preserve Host, which host
// filtering checks.
func Forwarded(trustedHops int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f := forwarded{clientIP: peerIP(r), scheme: connScheme(r)}
			if trustedHops > 0 {
				if ip, ok := forwardedClient(r.Header.Values("X-Forwarded-For"), trustedHops); ok {
					f.clientIP = ip
				}
				if proto := lastEntry(r.Header.Values("X-Forwarded-Proto")); proto == "http" || proto == "https" {
					f.scheme = proto
				}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), forwardedKey{}, f)))
		})
	}
}

// ClientIP is the client's address as resolved by Forwarded, or the peer
// address when the middleware did not run.
func ClientIP(r *http.Request) string {
	if f, ok := r.Context().Value(forwardedKey{}).(forwarded); ok {
		return f.clientIP
	}
	return peerIP(r)
}

// Scheme is "https" or "http" as resolved by Forwarded, or the connection's
// own scheme when the middleware did not run.
func Scheme(r *http.Request) string {
	if f, ok := r.Context().Value(forwardedKey{}).(forwarded); ok {
		return f.scheme
	}
	return connScheme(r)
}

func peerIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func connScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func forwardedClient(values []string, hops int) (string, bool) {
	var entries []string
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				entries = append(entries, p)
			}
		}
	}
	if len(entries) < hops {
		return "", false
	}
	candidate := entries[len(entries)-hops]
	if addr, err := netip.ParseAddr(candidate); err == nil {
		return addr.String(), true
	}
	if ap, err := netip.ParseAddrPort(candidate); err == nil {
		return ap.Addr().String(), true
	}
	return "", false
}

func lastEntry(values []string) string {
	if len(values) == 0 {
		return ""
	}
	parts := strings.Split(values[len(values)-1], ",")
	return strings.ToLower(strings.TrimSpace(parts[len(parts)-1]))
}
