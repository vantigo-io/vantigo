// Package security holds the browser-facing hardening applied to every
// response: the security header set with the content security policy, and
// host filtering.
package security

import (
	"net/http"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/httpx"
)

// ReferrerPolicy: an internal back office; no outbound link needs to disclose
// the page a user came from.
const ReferrerPolicy = "no-referrer"

// PermissionsPolicy denies powerful features the application never uses.
const PermissionsPolicy = "accelerometer=(), autoplay=(), browsing-topics=(), camera=(), display-capture=(), " +
	"encrypted-media=(), fullscreen=(self), geolocation=(), gyroscope=(), " +
	"idle-detection=(), local-fonts=(), magnetometer=(), microphone=(), midi=(), " +
	"payment=(), picture-in-picture=(), screen-wake-lock=(), serial=(), usb=(), " +
	"xr-spatial-tracking=()"

// hstsValue is ASP.NET Core's UseHsts default: 30 days, no includeSubDomains.
const hstsValue = "max-age=2592000"

// ContentSecurityPolicy builds the policy that allows inlineScriptHash — the
// runtime-configuration script web.Index injects — as the only inline script.
// style-src keeps 'unsafe-inline' because Mantine renders its CSS variables
// and component styles as inline <style> elements and React style props as
// style attributes. img-src allows https: for the configurable logo URL and
// remote images in the sandboxed email preview.
func ContentSecurityPolicy(inlineScriptHash string) string {
	return strings.Join([]string{
		"default-src 'self'",
		"base-uri 'self'",
		"object-src 'none'",
		"frame-ancestors 'none'",
		"form-action 'self'",
		"script-src 'self' " + inlineScriptHash,
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob: https:",
		"font-src 'self' data:",
		"connect-src 'self'",
		"frame-src 'self'",
		"worker-src 'self'",
		"manifest-src 'self'",
	}, "; ")
}

// HeaderOptions configures Headers.
type HeaderOptions struct {
	// InlineScriptHash is the CSP source expression for the index document's
	// runtime-configuration script ('sha256-…').
	InlineScriptHash string
	// ReportOnly sends the policy as Content-Security-Policy-Report-Only.
	ReportOnly bool
	// HSTS enables Strict-Transport-Security on https requests to non-loopback
	// hosts. Off in development.
	HSTS bool
}

// Headers sets the security header set on every response, API responses
// included. It must run after httpx.Forwarded so the HSTS decision sees the
// scheme the client used.
func Headers(o HeaderOptions) func(http.Handler) http.Handler {
	policyHeader := "Content-Security-Policy"
	if o.ReportOnly {
		policyHeader = "Content-Security-Policy-Report-Only"
	}
	policy := ContentSecurityPolicy(o.InlineScriptHash)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set(policyHeader, policy)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", ReferrerPolicy)
			h.Set("Permissions-Policy", PermissionsPolicy)
			if o.HSTS && httpx.Scheme(r) == "https" && !isLoopback(requestHostname(r.Host)) {
				h.Set("Strict-Transport-Security", hstsValue)
			}
			next.ServeHTTP(w, r)
		})
	}
}
