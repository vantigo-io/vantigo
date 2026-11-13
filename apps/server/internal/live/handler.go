package live

import (
	"errors"
	"net/http"
	"strings"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// Path is where server.New mounts the hub, outside Compose: the socket is a
// platform concern, not a contract operation (L2).
const Path = "/api/v1/live"

// sessionRule is what opening a socket requires: a signed-in session.
var sessionRule = contracts.Rule{Kind: contracts.RuleSession}

// ServeHTTP is GET /api/v1/live?v=1 (L2). Before upgrading it checks, in
// order: the method and the version; the origin — Sec-Fetch-Site same-origin
// or an Origin equal to APP_URL's, and a missing Origin is refused with 403,
// since CrossOriginProtection lets every GET through; the session, through
// Access.Check, answered with Access.Reject; and the rate of opening sockets,
// 30 a minute per user. The person's 21st socket on this replica is accepted
// and closed at once with 4429. The request's goroutine then owns the socket
// until it closes.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		httpx.WriteProblem(w, r, http.StatusMethodNotAllowed, "Only GET opens a live connection.")
		return
	}
	if r.URL.Query().Get("v") != "1" {
		httpx.WriteProblem(w, r, http.StatusBadRequest, "This version of the live protocol is not supported.")
		return
	}
	if !h.sameOrigin(r) {
		httpx.WriteProblem(w, r, http.StatusForbidden, "Cross-origin requests are not allowed.")
		return
	}
	// The station path (manufacturing phase 2 §17): a handshake with the
	// station cookie and no session cookie is a station's.
	if stations, ok := h.opts.Access.(contracts.StationAccess); ok {
		station, isStation, err := stations.CheckStation(r)
		if isStation {
			h.serveStation(w, r, station, err)
			return
		}
	}
	p, err := h.opts.Access.Check(r, sessionRule)
	if err != nil {
		if errors.Is(err, contracts.ErrUnauthenticated) || errors.Is(err, contracts.ErrForbidden) {
			h.opts.Access.Reject(w, r, sessionRule, err)
			return
		}
		httpx.WriteError(w, r, err)
		return
	}
	if h.opts.Limiter != nil {
		d, err := h.opts.Limiter.Hit(r.Context(), connectPolicy, p.UserID.String())
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		if !d.Allowed {
			ratelimit.Reject(w, r, connectPolicy, d)
			return
		}
	}

	c, attachErr := h.attach(p.UserID, p.SessionID)
	if errors.Is(attachErr, errHubClosed) {
		httpx.WriteProblem(w, r, http.StatusServiceUnavailable, "The server is shutting down.")
		return
	}
	// The origin is checked above, by the rule CrossOriginProtection applies;
	// the library's own check compares the Origin with Host, which a proxy
	// in front (Vite's in development) rewrites.
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		if c != nil {
			h.detach(c)
		}
		return // Accept has answered
	}
	if attachErr != nil {
		_ = ws.Close(CloseTooManyConnections, "too many connections")
		return
	}
	h.serveSocket(ws, c)
}

// serveStation opens a station's socket (§17): the station resolved by
// CheckStation (err its refusal, station_unknown, answered by Reject), the
// connect rate limit and the per-replica socket cap kept per station. The
// socket may subscribe only to the topics marked Station, which decide by the
// station's work centers; it announces no presence.
func (h *Hub) serveStation(w http.ResponseWriter, r *http.Request, station uuid.UUID, err error) {
	if err != nil {
		if errors.Is(err, contracts.ErrUnauthenticated) || errors.Is(err, contracts.ErrForbidden) {
			h.opts.Access.Reject(w, r, sessionRule, err)
			return
		}
		httpx.WriteError(w, r, err)
		return
	}
	if h.opts.Limiter != nil {
		d, err := h.opts.Limiter.Hit(r.Context(), connectPolicy, "station:"+station.String())
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		if !d.Allowed {
			ratelimit.Reject(w, r, connectPolicy, d)
			return
		}
	}
	c, attachErr := h.attachStation(station)
	if errors.Is(attachErr, errHubClosed) {
		httpx.WriteProblem(w, r, http.StatusServiceUnavailable, "The server is shutting down.")
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		if c != nil {
			h.detach(c)
		}
		return
	}
	if attachErr != nil {
		_ = ws.Close(CloseTooManyConnections, "too many connections")
		return
	}
	h.serveSocket(ws, c)
}

// sameOrigin is the handshake's origin rule.
func (h *Hub) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	if r.Header.Get("Sec-Fetch-Site") == "same-origin" {
		return true
	}
	return h.opts.AppOrigin != "" && strings.EqualFold(origin, h.opts.AppOrigin)
}
