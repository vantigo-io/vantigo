package httpx

import (
	"net/http"
	"net/url"
	"strings"
)

// StripBasePath mounts the application under base (e.g. "/vantigo"): a
// request under the prefix continues with the prefix removed, and a request
// outside it passes through untouched — so container probes of /health/ready
// keep working, as with ASP.NET Core's UsePathBase. base "" is a no-op.
func StripBasePath(base string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if base == "" {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := r.URL.Path
			if p != base && !strings.HasPrefix(p, base+"/") {
				next.ServeHTTP(w, r)
				return
			}
			r2 := new(http.Request)
			*r2 = *r
			r2.URL = new(url.URL)
			*r2.URL = *r.URL
			r2.URL.Path = strings.TrimPrefix(p, base)
			if r2.URL.Path == "" {
				r2.URL.Path = "/"
			}
			r2.URL.RawPath = ""
			next.ServeHTTP(w, r2)
		})
	}
}
