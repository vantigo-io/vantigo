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

// cookie is the one place identity builds a cookie, so every cookie above
// shares these attributes. Secure follows APP_URL's scheme rather than being
// always true: a Secure cookie is never sent to an http origin, so on an
// installation the operator deliberately runs on plain http (docs
// transport-security.md, "What each choice costs") a constant true would not
// protect the session, it would make signing in impossible. https is the
// documented deployment and the only one where a session is safe from the
// network; http is a choice the operator makes for a path they control end to
// end, and the attribute simply tells the truth about which one this is.
//
// CodeQL's go/cookie-secure-not-set reports the SetCookie in set above for
// this function's cookies. It is a false positive, and not about the value
// here: the query flags a cookie write that no boolean reaches at all
// (isInsecureDefault in its own CookieWithoutSecure.qll), and its taint
// tracking loses this field between the literal below and the loop in set —
// the same loop reached through a response's cookies field. Hard-coding
// Secure: true was measured against CodeQL 2.27 (CI's version) and the alert
// is unchanged by it; writing false through a separate function instead makes
// the query report it as an explicit insecure cookie. So there is nothing to
// fix here, and the alert is one to dismiss with a reason.
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
