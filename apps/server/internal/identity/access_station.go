package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// This file is how Access answers a shared shop-floor station (manufacturing
// phase 2 M129–M138). A station is a paired device, never a person: its
// cookie alone may only sign operators in and out and listen (M130). Every
// module request is an operator's — the station cookie and an operator token
// in Authorization: Station <token> — and Check answers one only to the
// router's wrap, for an operation marked x-vantigo-station: allowed, with the
// operator's permissions cut down to Manufacturing's shop floor (M134).

// stationCookieName carries the station token (M129).
const stationCookieName = "vantigo.station"

// stationTokenLifetime is how long a pairing holds: the token and its cookie
// alike. After it the station answers station_unknown and is paired again.
const stationTokenLifetime = 90 * 24 * time.Hour

// operatorSessionAbsolute bounds an operator session whatever its activity.
const operatorSessionAbsolute = 12 * time.Hour

// stationAuthScheme is the Authorization scheme of an operator's request.
const stationAuthScheme = "Station"

// stationPermissions are all a station's operator may ever hold (M134), and
// what they must hold to act at all: every station request is checked for
// both, whatever its own rule names.
var stationPermissions = contracts.StationPermissions

// The end reasons of an operator session (the CHECK on end_reason).
const (
	endSwitchedOut        = "switched_out"
	endIdle               = "idle"
	endExpired            = "expired"
	endRevoked            = "revoked"
	endReplaced           = "replaced"
	endUserInactive       = "user_inactive"
	endCredentialsChanged = "credentials_changed"
	endSignedOut          = "signed_out"
	endByAdmin            = "ended_by_admin"
)

// stationRefusal is a station request refused with its own code: a 401 that
// says which credential failed, or the 409 of a browser that is a person's as
// well. It unwraps to ErrUnauthenticated so the router hands it to Reject,
// which writes the status and code it carries.
type stationRefusal struct {
	status  int
	code    string
	message string
}

func (e *stationRefusal) Error() string { return "identity: station refused: " + e.code }
func (e *stationRefusal) Unwrap() error { return contracts.ErrUnauthenticated }

var (
	errStationUnknown = &stationRefusal{http.StatusUnauthorized, "station_unknown",
		"This browser is not a paired station, or its pairing has ended. Ask an administrator for a pairing code."}
	errOperatorSessionEnded = &stationRefusal{http.StatusUnauthorized, "operator_session_ended",
		"The operator is no longer signed in at this station. Switch in again."}
	errStationHasSession = &stationRefusal{http.StatusConflict, "station_has_session",
		"This browser is also signed in to Vantigo as a person. Sign out of Vantigo in this browser to use it as a station."}
)

// stationScopeMessage is the words of a 403 station_scope.
const stationScopeMessage = "This is not something a shop-floor station can do."

// operatorTokenFrom returns the operator token of an Authorization: Station
// header; the scheme is matched without regard to case.
func operatorTokenFrom(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	if !ok || !strings.EqualFold(scheme, stationAuthScheme) {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

// isStationRequest reports whether r presents station credentials for an
// operator: such a request is judged as a station's only (M134).
func isStationRequest(r *http.Request) bool {
	scheme, _, _ := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	return strings.EqualFold(scheme, stationAuthScheme)
}

// stationTokenFrom returns the station cookie's value, if r has one.
func stationTokenFrom(r *http.Request) (string, bool) {
	return cookieFrom(r, stationCookieName)
}

// newStationCookie hands a station token to the browser for its lifetime,
// with every other cookie's attributes: HttpOnly, SameSite=Strict, Secure on
// https (M138).
func (a *Access) newStationCookie(token string) *http.Cookie {
	c := a.cookie(stationCookieName, token)
	c.MaxAge = int(stationTokenLifetime.Seconds())
	return c
}

// liveSessionCookie reports whether r carries a session cookie of a live
// session, without sliding it. A dead cookie is no person's browser: it is
// cleared on a 401 like any other and does not make a station refuse.
func (a *Access) liveSessionCookie(ctx context.Context, r *http.Request, now time.Time) (bool, error) {
	raw, ok := sessionTokenFrom(r)
	if !ok {
		return false, nil
	}
	hash, ok := parseToken(raw)
	if !ok {
		return false, nil
	}
	sso, err := a.sso.get(ctx)
	if err != nil {
		return false, fmt.Errorf("identity: resolve session: %w", err)
	}
	bounds := a.cfg.Sessions
	_, err = a.q.GetSessionByTokenHash(ctx, store.GetSessionByTokenHashParams{
		TokenHash:          hash,
		ScimEnabled:        sso.SCIM != nil,
		Now:                now,
		Idle:               interval(bounds.Idle),
		PrivilegedIdle:     interval(bounds.PrivilegedIdle),
		Absolute:           interval(bounds.Absolute),
		PrivilegedAbsolute: interval(bounds.PrivilegedAbsolute),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("identity: resolve session: %w", err)
	}
	return true, nil
}

// resolveStation resolves r's station cookie at now: a station that is
// paired, not revoked, within its 90 days, and reached from an address its
// allowlist admits (M129), the address being the client's as the platform's
// rate limiter resolves it behind the proxy. Anything else is
// errStationUnknown. The station's last activity is recorded at most once a
// minute.
func (a *Access) resolveStation(ctx context.Context, r *http.Request, now time.Time) (store.IdentityStation, error) {
	var none store.IdentityStation
	raw, ok := stationTokenFrom(r)
	if !ok {
		return none, errStationUnknown
	}
	hash, ok := parseToken(raw)
	if !ok {
		return none, errStationUnknown
	}
	st, err := a.q.GetStationByTokenHash(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return none, errStationUnknown
	}
	if err != nil {
		return none, fmt.Errorf("identity: resolve station: %w", err)
	}
	if !stationLive(st, now) {
		return none, errStationUnknown
	}
	ip := httpx.ClientIP(r)
	if !addressAllowed(st.AllowedNetworks, ip) {
		return none, errStationUnknown
	}
	var ipp *string
	if ip != "" {
		ipp = &ip
	}
	if err := a.q.TouchStation(ctx, store.TouchStationParams{ID: st.ID, Now: now, Ip: ipp}); err != nil {
		return none, fmt.Errorf("identity: touch station: %w", err)
	}
	return st, nil
}

// stationLive reports whether st holds a token that is in force at now.
func stationLive(st store.IdentityStation, now time.Time) bool {
	return st.RevokedAt == nil && st.TokenHash != nil && st.TokenExpiresAt != nil && st.TokenExpiresAt.After(now)
}

// addressAllowed reports whether ip is inside one of networks; no networks
// admit every address, and an address that does not parse is admitted by
// none.
func addressAllowed(networks []netip.Prefix, ip string) bool {
	if len(networks) == 0 {
		return true
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	addr = addr.Unmap().WithZone("")
	return slices.ContainsFunc(networks, func(p netip.Prefix) bool { return p.Contains(addr) })
}

// CheckStation is contracts.StationAccess's handshake check for the live
// socket's station path (§17): a handshake with the station cookie and no
// live session is a station's, resolved as resolveStation resolves it. A
// session cookie beside the station's is judged as the HTTP path judges it
// (liveSessionCookie): a live session makes the handshake a person's, a dead
// one does not.
func (a *Access) CheckStation(r *http.Request) (uuid.UUID, bool, error) {
	if _, ok := stationTokenFrom(r); !ok {
		return uuid.Nil, false, nil
	}
	now := a.now()
	live, err := a.liveSessionCookie(r.Context(), r, now)
	if err != nil {
		return uuid.Nil, true, err
	}
	if live {
		return uuid.Nil, false, nil
	}
	st, err := a.resolveStation(r.Context(), r, now)
	if err != nil {
		return uuid.Nil, true, err
	}
	return st.ID, true, nil
}

// StationLive is contracts.StationAccess's re-check: the station is still
// paired, unrevoked and within its 90 days, and ip, the address of the
// socket's handshake, is still inside its network allowlist.
func (a *Access) StationLive(ctx context.Context, stationID uuid.UUID, ip string) (bool, error) {
	st, err := a.q.GetStation(ctx, stationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("identity: read whether the station is live: %w", err)
	}
	return stationLive(st, a.now()) && addressAllowed(st.AllowedNetworks, ip), nil
}

// StationKnown is contracts.StationAccess's question of a station id: one is
// registered, paired, revoked or neither.
func (a *Access) StationKnown(ctx context.Context, stationID uuid.UUID) (bool, error) {
	_, err := a.q.GetStation(ctx, stationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("identity: read whether the station is known: %w", err)
	}
	return true, nil
}

var _ contracts.StationAccess = (*Access)(nil)

// checkStation is Check for a request that presents an operator token
// (M134). It refuses, in order: a rule that does not admit stations at all
// (ErrUnauthenticated — a handler's own Check, a platform endpoint);
// a browser that also holds a live session (station_has_session); the
// station cookie (station_unknown); the operator token — unknown, ended, of
// another station, idle past the station's timeout, older than 12 hours, or
// of a person who may no longer act (operator_session_ended, ending the
// session); an operator who no longer holds every one of stationPermissions
// (operator_session_ended, ending the session), whatever the rule names, so
// losing manufacturing:shop-floor ends reads too; an operation not open to
// stations (station_scope); and then the rule itself, which must be a
// permission rule naming only stationPermissions. The principal answered
// holds stationPermissions and no role (contracts.HasPermissions answers a
// station's operator from it).
func (a *Access) checkStation(ctx context.Context, r *http.Request, rule contracts.Rule, now time.Time) (contracts.Principal, error) {
	if rule.Station == contracts.StationRefused {
		return contracts.Principal{}, contracts.ErrUnauthenticated
	}
	live, err := a.liveSessionCookie(ctx, r, now)
	if err != nil {
		return contracts.Principal{}, err
	}
	if live {
		return contracts.Principal{}, errStationHasSession
	}
	st, err := a.resolveStation(ctx, r, now)
	if err != nil {
		return contracts.Principal{}, err
	}
	raw, _ := operatorTokenFrom(r)
	hash, ok := parseToken(raw)
	if !ok {
		return contracts.Principal{}, errOperatorSessionEnded
	}
	op, err := a.q.GetOperatorSessionByTokenHash(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return contracts.Principal{}, errOperatorSessionEnded
	}
	if err != nil {
		return contracts.Principal{}, fmt.Errorf("identity: resolve operator: %w", err)
	}
	// A token copied to another station's browser is not this station's
	// operator (M134); the session itself is left alone, it is its own
	// station's.
	if op.StationID != st.ID {
		return contracts.Principal{}, errOperatorSessionEnded
	}
	reason := a.operatorEndReason(ctx, st, op, now)
	if reason == "" {
		// The operator must hold the whole shop floor for any station
		// request, reads included: a person whose role lost
		// manufacturing:shop-floor is signed out at their next request.
		holds, err := a.operatorHolds(ctx, op, stationPermissions, now)
		if err != nil {
			return contracts.Principal{}, err
		}
		if !holds {
			reason = endUserInactive
		}
	}
	if reason != "" {
		if err := a.endOperatorSession(ctx, r, st, op.ID, op.UserID, reason, now); err != nil {
			return contracts.Principal{}, err
		}
		return contracts.Principal{}, errOperatorSessionEnded
	}
	if rule.Station != contracts.StationAllowed {
		return contracts.Principal{}, contracts.ErrStationScope
	}
	if rule.Kind != contracts.RulePermission || len(rule.Names) == 0 {
		return contracts.Principal{}, contracts.ErrForbidden
	}
	for _, name := range rule.Names {
		if !slices.Contains(stationPermissions, name) {
			return contracts.Principal{}, contracts.ErrForbidden
		}
	}
	// The idle window slides once it is a quarter of the timeout old, at most
	// a minute, so an operator active within the timeout is never ended idle.
	every := min(time.Duration(st.IdleMinutes)*time.Minute/4, time.Minute)
	if err := a.q.TouchOperatorSession(ctx, store.TouchOperatorSessionParams{ID: op.ID, Now: now, Every: interval(every)}); err != nil {
		return contracts.Principal{}, fmt.Errorf("identity: touch operator session: %w", err)
	}
	return contracts.Principal{
		UserID:    op.UserID,
		SessionID: op.ID,
		Via:       &contracts.Via{Kind: contracts.ViaStation, StationID: st.ID},
	}, nil
}

// endOperatorSession ends one operator session for reason, with its audit
// operator_signed_out, in one transaction. A session another request ended
// first is left as it is, and audited once.
func (a *Access) endOperatorSession(ctx context.Context, r *http.Request, st store.IdentityStation, sessionID, userID uuid.UUID, reason string, now time.Time) error {
	err := db.WithTx(ctx, a.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		n, err := q.EndOperatorSession(ctx, store.EndOperatorSessionParams{ID: sessionID, Now: &now, EndReason: &reason})
		if err != nil || n == 0 {
			return err
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			targetUser: &userID, action: auditOperatorSignedOut, before: struct{}{},
			after: operatorAudit{StationID: st.ID, Station: st.Name, SessionID: sessionID, Reason: reason},
		})
	})
	if err != nil {
		return fmt.Errorf("identity: end operator session: %w", err)
	}
	return nil
}

// endUserOperatorSessions ends every operator session of userID, at every
// station, for reason, on the caller's transaction q, auditing each one
// operator_signed_out with actor (nil for none). A PIN or a badge set or
// cleared ends them (credentials_changed), so does every revocation of the
// person's sessions (signed_out), and an erasure (user_inactive).
func endUserOperatorSessions(ctx context.Context, q *store.Queries, r *http.Request, actor *uuid.UUID, userID uuid.UUID, reason string, now time.Time) error {
	ended, err := q.EndUserOperatorSessions(ctx, store.EndUserOperatorSessionsParams{UserID: &userID, Now: &now, EndReason: &reason})
	if err != nil {
		return fmt.Errorf("identity: end operator sessions: %w", err)
	}
	for _, row := range ended {
		if err := writeAudit(ctx, q, r, now, auditEvent{
			actor: actor, targetUser: &userID, action: auditOperatorSignedOut, before: struct{}{},
			after: operatorAudit{StationID: row.StationID, Station: row.StationName, SessionID: row.ID, Reason: reason},
		}); err != nil {
			return err
		}
	}
	return nil
}

// operatorEndReason is why an operator session can no longer stand at now,
// "" when it can: older than 12 hours, idle past the station's timeout
// (measured by the server's clock, never the tablet's), or of a person a
// session could not belong to — disabled, pending, not a person, or while
// SCIM is on deactivated by the directory unless an Owner (GetSessionByTokenHash's
// user predicates).
func (a *Access) operatorEndReason(ctx context.Context, st store.IdentityStation, op store.GetOperatorSessionByTokenHashRow, now time.Time) string {
	if now.Sub(op.StartedAt) >= operatorSessionAbsolute {
		return endExpired
	}
	if now.Sub(op.LastActivityAt) >= time.Duration(st.IdleMinutes)*time.Minute {
		return endIdle
	}
	if op.IsDisabled || op.Pending || op.Kind != "person" {
		return endUserInactive
	}
	if op.ScimUpstreamActive != nil && !*op.ScimUpstreamActive && !op.IsOwner {
		sso, err := a.sso.get(ctx)
		if err != nil || sso.SCIM != nil {
			return endUserInactive // fail closed when the settings cannot be read
		}
	}
	return ""
}

// operatorHolds reports whether the operator holds every key at now: not
// locked out, then an Owner every key, anyone else each through an effective
// role (permitted's rule). The Owner's second factor is not asked here: an
// operator never has one, and what a station's operator may hold is cut down
// to the shop floor already (M134), so an Owner at a station holds those
// two keys and nothing more.
func (a *Access) operatorHolds(ctx context.Context, op store.GetOperatorSessionByTokenHashRow, keys []string, now time.Time) (bool, error) {
	if !isActive(op.IsDisabled, op.LockoutEnd, now) {
		return false, nil
	}
	if op.IsOwner {
		return true, nil
	}
	for _, key := range keys {
		ok, err := a.q.EffectivePermissionForUser(ctx, store.EffectivePermissionForUserParams{UserID: op.UserID, PermissionKey: key, Now: now})
		if err != nil {
			return false, fmt.Errorf("identity: check permission: %w", err)
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}
