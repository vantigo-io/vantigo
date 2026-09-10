package httpx

import "net/http"

// Chain wraps h in middleware so that the first one listed is the outermost:
// Chain(h, a, b) serves a(b(h)).
func Chain(h http.Handler, middleware ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middleware) - 1; i >= 0; i-- {
		h = middleware[i](h)
	}
	return h
}
