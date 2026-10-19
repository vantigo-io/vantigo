package security

import (
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/httpx"
)

// Route is one entry of the public host's allowlist: the methods it admits
// and a base-relative path, matched exactly unless Prefix or OneSegment says
// otherwise.
type Route struct {
	Methods []string
	// Path is the base-relative path; it ends in "/" when Prefix or
	// OneSegment is set.
	Path string
	// Prefix admits every path under Path.
	Prefix bool
	// OneSegment admits Path followed by exactly one non-empty segment and
	// nothing after it.
	OneSegment bool
}

func (rt Route) matches(method, p string) bool {
	if !slices.Contains(rt.Methods, method) {
		return false
	}
	switch {
	case rt.Prefix:
		return strings.HasPrefix(p, rt.Path) && len(p) > len(rt.Path)
	case rt.OneSegment:
		rest, ok := strings.CutPrefix(p, rt.Path)
		return ok && rest != "" && !strings.Contains(rest, "/")
	default:
		return p == rt.Path
	}
}

// PayRoutes is everything the public host serves (payments port design D14,
// its plan's reading 23): the pay page's SPA index, its bundle and icon, the
// pay page's three anonymous Invoices operations — the read is a POST, so the
// token never travels in an API URL — the calendar's answer page and its two
// anonymous operations on the same pattern (calendar invitations design D5),
// the calendar feed, and the health probes. The feed is the one anonymous GET
// with its token in the path (calendar feed design D2): a calendar program
// takes one address and asks it with nothing of ours, so it is admitted under
// GET alone, as one segment, with no page beside it. No maintenance poll: the
// root layout makes none on the pay page. No Vipps webhook path: webhooks are
// deferred (the plan's Task 11) and nothing serves one. The list names only
// what is served. The paths are base-relative: the gate runs after
// StripBasePath, so the list takes no base path.
func PayRoutes() []Route {
	getHead := []string{http.MethodGet, http.MethodHead}
	get := []string{http.MethodGet}
	post := []string{http.MethodPost}
	return []Route{
		{Methods: getHead, Path: "/pay"},
		{Methods: getHead, Path: "/assets/", Prefix: true},
		{Methods: getHead, Path: "/favicon.png"},
		{Methods: post, Path: "/api/v1/invoices/pay/read"},
		{Methods: post, Path: "/api/v1/invoices/pay/attempts"},
		{Methods: get, Path: "/api/v1/invoices/pay/attempts/", OneSegment: true},
		{Methods: getHead, Path: "/rsvp"},
		{Methods: post, Path: "/api/v1/calendar/rsvp/read"},
		{Methods: post, Path: "/api/v1/calendar/rsvp"},
		{Methods: get, Path: "/api/v1/calendar/feed/", OneSegment: true},
		{Methods: getHead, Path: "/health/", Prefix: true},
	}
}

// PublicHostGate serves only allow on publicHost (payments port design D14):
// a request whose Host, port ignored, is publicHost and whose method and
// base-relative path match no route answers 404 — a problem under /api/, an
// empty body elsewhere — before any routing. A path that is not already clean
// ("..", ".", an empty segment) matches nothing, so no prefix can be climbed
// out of, and so does a path escaped other than canonically (url.URL.RawPath
// set), since the API router splits the escaped path and the two must agree.
// A request on any other host passes untouched. It must run after
// StripBasePath.
func PublicHostGate(publicHost string, allow []Route) func(http.Handler) http.Handler {
	publicHost = strings.ToLower(publicHost)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if requestHostname(r.Host) != publicHost || admitted(allow, r.Method, r.URL) {
				next.ServeHTTP(w, r)
				return
			}
			if p := r.URL.Path; p == "/api" || strings.HasPrefix(p, "/api/") {
				httpx.NotFound(w, r)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		})
	}
}

func admitted(allow []Route, method string, u *url.URL) bool {
	// A path escaped other than canonically — an encoded "/" above all — is
	// matched here on its decoded form but split by the API router on its
	// escaped one, so the two would disagree on which route it is: refused.
	if u.RawPath != "" {
		return false
	}
	p := u.Path
	if clean := path.Clean(p); clean != p && clean+"/" != p {
		return false
	}
	for _, rt := range allow {
		if rt.matches(method, p) {
			return true
		}
	}
	return false
}
