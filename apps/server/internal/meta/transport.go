package meta

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/netguard"
)

// This file is the module's outbound HTTP: one client that reaches Meta's
// Graph API and Meta's CDN and nothing else, through netguard, copied from
// google's and entra's rather than imported, because no module imports
// another. The Graph client (graph.go) and the attachment fetch of the
// splitting worker both build on newHTTPClient.

// The hosts the module may reach. The Graph API is one host; the files a
// person sent live on Meta's CDN, whose host names vary by region
// (scontent.xx.fbcdn.net, lookaside.fbsbx.com), so they are matched by suffix.
const (
	graphHost = "graph.facebook.com"
	// graphBase is the Graph API's base, with the version the module is
	// written against.
	graphBase = "https://" + graphHost + "/v21.0"
)

// cdnSuffixes are the domains Meta's CDN serves an attachment from.
var cdnSuffixes = []string{".fbcdn.net", ".fbsbx.com"}

// The client's figures.
const (
	dialTimeout            = 10 * time.Second
	tlsHandshakeTimeout    = 10 * time.Second
	responseHeaderTimeout  = 15 * time.Second
	maxResponseHeaderBytes = 64 << 10
	idleConnTimeout        = 90 * time.Second
	// callBudget bounds one exchange: the connection, the request, the headers
	// and the body together.
	callBudget = 30 * time.Second
	// fileBudget bounds a file's fetch or upload, which moves megabytes.
	fileBudget = 2 * time.Minute
)

// processTransport is the production transport, built once for the process.
// Proxy is nil on purpose: a proxy would send the request to an address the
// guard never checked.
var processTransport = sync.OnceValue(func() *http.Transport {
	return &http.Transport{
		Proxy:                  nil,
		DialContext:            netguard.DialContext(net.DefaultResolver, (&net.Dialer{Timeout: dialTimeout}).DialContext),
		ForceAttemptHTTP2:      true,
		TLSHandshakeTimeout:    tlsHandshakeTimeout,
		ResponseHeaderTimeout:  responseHeaderTimeout,
		MaxResponseHeaderBytes: maxResponseHeaderBytes,
		MaxIdleConns:           4,
		MaxIdleConnsPerHost:    2,
		IdleConnTimeout:        idleConnTimeout,
	}
})

// hostGuard refuses a request to any host outside allow, over https, with no
// user, no port and no dot segment, before it reaches the transport.
type hostGuard struct {
	next  http.RoundTripper
	allow func(host string) bool
}

func (g hostGuard) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := g.check(req.URL); err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	return g.next.RoundTrip(req)
}

func (g hostGuard) check(u *url.URL) error {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Port() != "" ||
		!g.allow(u.Hostname()) || strings.Contains(u.Path, "/../") || strings.HasSuffix(u.Path, "/..") {
		host := ""
		if u != nil {
			host = u.Host
		}
		return fmt.Errorf("meta: a request to a host outside the fixed list: %q", host)
	}
	return nil
}

// graphOnly admits the Graph API's host alone.
func graphOnly(host string) bool { return host == graphHost }

// graphOrCDN admits the Graph API's host and Meta's CDN.
func graphOrCDN(host string) bool {
	if host == graphHost {
		return true
	}
	for _, s := range cdnSuffixes {
		if strings.HasSuffix(host, s) && len(host) > len(s) {
			return true
		}
	}
	return false
}

// newHTTPClient builds a client over d: Deps.HTTPTransport when a test set one
// (modtest.WithTransport), the guarded transport otherwise, behind hostGuard
// either way. Redirects are not followed: a Graph answer never redirects, and
// a CDN URL that does is refused rather than followed to a host nobody checked.
func newHTTPClient(d module.Deps, allow func(string) bool, timeout time.Duration) *http.Client {
	var rt http.RoundTripper = processTransport()
	if d.HTTPTransport != nil {
		rt = d.HTTPTransport
	}
	return &http.Client{
		Transport:     hostGuard{next: rt, allow: allow},
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// loggerOf is d's logger, or one that discards.
func loggerOf(d module.Deps) *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.New(slog.DiscardHandler)
}
