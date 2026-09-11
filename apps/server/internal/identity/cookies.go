package identity

import (
	"net/http"
)

// sessionCookieName carries the session token (spec *Sessions*).
const sessionCookieName = "vantigo.session"

// setSessionCookie hands token to the browser: HttpOnly, SameSite=Strict,
// scoped to the base path, and Secure unless the installation runs in
// development or has knowingly allowed plaintext transport. A persistent
// cookie (rememberMe on /login/2fa) lives for the standard absolute
// lifetime; the server enforces the tighter privileged bound per request
// whatever the cookie says. Any other cookie ends with the browser session.
func (a *Access) setSessionCookie(w http.ResponseWriter, token string, persistent bool) {
	c := a.sessionCookie(token)
	if persistent {
		c.MaxAge = int(a.cfg.Sessions.Absolute.Seconds())
	}
	http.SetCookie(w, c)
}

// clearSessionCookie tells the browser to drop the session cookie.
func (a *Access) clearSessionCookie(w http.ResponseWriter) {
	c := a.sessionCookie("")
	c.MaxAge = -1
	http.SetCookie(w, c)
}

func (a *Access) sessionCookie(value string) *http.Cookie {
	path := a.cfg.BasePath
	if path == "" {
		path = "/"
	}
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     path,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   !a.cfg.IsDevelopment() && !a.cfg.AllowInsecureTransport,
	}
}

// sessionTokenFrom returns the session cookie's value, if the request has
// one.
func sessionTokenFrom(r *http.Request) (string, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	return c.Value, true
}
