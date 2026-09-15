package identity

import (
	"net/http"
	"time"
)

// sessionCookieName carries the session token (spec *Sessions*).
const sessionCookieName = "vantigo.session"

// loginTicketCookieName carries a pending two-factor sign-in's ticket from
// POST /login to POST /login/2fa (spec *Sessions*, the 2FA step).
const loginTicketCookieName = "vantigo.2fa"

// loginTicketLifetime is how long a login ticket, row and cookie alike,
// stays valid: ASP.NET's TwoFactorUserIdScheme default, which the .NET
// flow relied on.
const loginTicketLifetime = 5 * time.Minute

// cookies are the cookies a response sets before its generated Visit
// method writes the status and body.
type cookies []*http.Cookie

func (cs cookies) set(w http.ResponseWriter) {
	for _, c := range cs {
		http.SetCookie(w, c)
	}
}

// newSessionCookie hands token to the browser: HttpOnly, SameSite=Strict,
// scoped to the base path, and Secure exactly when APP_URL is an https
// origin (a Secure cookie never reaches an http one). A persistent
// cookie (rememberMe on /login/2fa) lives for the standard absolute
// lifetime; the server enforces the tighter privileged bound per request
// whatever the cookie says. Any other cookie ends with the browser session.
func (a *Access) newSessionCookie(token string, persistent bool) *http.Cookie {
	c := a.cookie(sessionCookieName, token)
	if persistent {
		c.MaxAge = int(a.cfg.Sessions.Absolute.Seconds())
	}
	return c
}

// expiredSessionCookie tells the browser to drop the session cookie.
func (a *Access) expiredSessionCookie() *http.Cookie {
	c := a.cookie(sessionCookieName, "")
	c.MaxAge = -1
	return c
}

// clearSessionCookie sets expiredSessionCookie on w.
func (a *Access) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, a.expiredSessionCookie())
}

// newLoginTicketCookie hands a login ticket to the browser with the session
// cookie's attributes, expiring with the ticket.
func (a *Access) newLoginTicketCookie(token string) *http.Cookie {
	c := a.cookie(loginTicketCookieName, token)
	c.MaxAge = int(loginTicketLifetime.Seconds())
	return c
}

// expiredLoginTicketCookie tells the browser to drop a login ticket.
func (a *Access) expiredLoginTicketCookie() *http.Cookie {
	c := a.cookie(loginTicketCookieName, "")
	c.MaxAge = -1
	return c
}

func (a *Access) cookie(name, value string) *http.Cookie {
	path := a.cfg.BasePath
	if path == "" {
		path = "/"
	}
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   a.cfg.UsesHTTPS(),
	}
}

// sessionTokenFrom returns the session cookie's value, if the request has
// one.
func sessionTokenFrom(r *http.Request) (string, bool) {
	return cookieFrom(r, sessionCookieName)
}

// loginTicketFrom returns the login ticket cookie's value, if the request
// has one.
func loginTicketFrom(r *http.Request) (string, bool) {
	return cookieFrom(r, loginTicketCookieName)
}

func cookieFrom(r *http.Request, name string) (string, bool) {
	c, err := r.Cookie(name)
	if err != nil || c.Value == "" {
		return "", false
	}
	return c.Value, true
}
