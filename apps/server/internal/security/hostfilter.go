package security

import (
	"net"
	"net/http"
	"slices"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/httpx"
)

// loopbackHosts stay allowed whatever APP_URL is: the container HEALTHCHECK
// (`vantigo healthcheck`) probes http://127.0.0.1:<port>/health/ready, and
// host filtering runs before routing, so it cannot exempt that path by name.
var loopbackHosts = []string{"localhost", "127.0.0.1", "::1"}

// AllowedHosts is APP_URL's host plus loopback.
func AllowedHosts(appHostname string) []string {
	hosts := []string{strings.ToLower(appHostname)}
	for _, h := range loopbackHosts {
		if !slices.Contains(hosts, h) {
			hosts = append(hosts, h)
		}
	}
	return hosts
}

// HostFilter rejects any request whose Host header is not in allowed (the port
// is ignored), so a request carrying someone else's host name never reaches
// the application.
func HostFilter(allowed []string) func(http.Handler) http.Handler {
	set := make(map[string]bool, len(allowed))
	for _, h := range allowed {
		set[strings.ToLower(h)] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !set[requestHostname(r.Host)] {
				httpx.WriteProblem(w, r, http.StatusBadRequest, "The request host is not allowed.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requestHostname is the Host header without port or IPv6 brackets, lower-case.
func requestHostname(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return strings.ToLower(h)
	}
	return strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(host, "["), "]"))
}

func isLoopback(hostname string) bool { return slices.Contains(loopbackHosts, hostname) }
