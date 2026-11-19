package contracts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
)

// Principal is the authenticated caller of one request.
type Principal struct {
	UserID      uuid.UUID
	SessionID   uuid.UUID
	Roles       []string
	MFAVerified bool
	SCIM        bool // authenticated by the SCIM bearer token; no user
	// Via is set when the principal is the session's own, reached through
	// something acting for the person rather than by the person's browser:
	// the assistant, through an assistant grant. It is also set, with no
	// person and no session, for an agent acting through an agent grant. It is
	// nil for every other principal, and a module's audit can say "on the
	// person's behalf through the assistant, in this run".
	Via *Via
}

// Via says what acted for the principal.
type Via struct {
	// Kind names it: "assistant" or "agent".
	Kind string
	// ConversationID is the assistant conversation of an "assistant" Via.
	ConversationID uuid.UUID
	RunID          uuid.UUID
	// AgentID, CustomerID and ApprovedBy are set for an "agent" Via: the
	// agent, the customer the run is scoped to (nil for a run with none) and
	// the reviewer whose approval the grant carries (nil for a run's own).
	AgentID    uuid.UUID
	CustomerID *int32
	ApprovedBy *uuid.UUID
	// StationID is set for a "station" Via: the shared shop-floor station the
	// operator signed in at (manufacturing phase 2 M134). The principal is the
	// operator, with the operator session as SessionID.
	StationID uuid.UUID
	// KeyID is set for an "api_key" Via: the API key the request presented
	// (API access design K10). The principal is the key's integration, with
	// no session.
	KeyID uuid.UUID
	// RateLimitPerMinute is set for an "api_key" Via: the requests a minute
	// the key's integration may make, across all its keys, which the router
	// counts once Check has admitted the key (K24).
	RateLimitPerMinute int
}

// ViaAssistant is Via.Kind of an assistant grant, ViaAgent of an agent grant,
// ViaStation of an operator signed in at a shared shop-floor station, ViaAPIKey
// of an integration authenticated by an API key.
const (
	ViaAssistant = "assistant"
	ViaAgent     = "agent"
	ViaStation   = "station"
	ViaAPIKey    = "api_key"
)

// StationPermissions are all a shared station's operator may ever hold
// (manufacturing phase 2 M134): Check admits an operator only while they hold
// every one of them, and a rule naming any other key refuses them.
var StationPermissions = []string{"manufacturing:access", "manufacturing:shop-floor"}

// Station reports the station the principal acts at, and whether it is a
// station's operator at all (Via.Kind ViaStation).
func (p Principal) Station() (uuid.UUID, bool) {
	if p.Via == nil || p.Via.Kind != ViaStation {
		return uuid.Nil, false
	}
	return p.Via.StationID, true
}

// principalContextKey is unexported so only this package can set or read the
// context value.
type principalContextKey struct{}

// WithPrincipal attaches p to ctx.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, p)
}

// PrincipalFrom retrieves the Principal WithPrincipal attached to ctx.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalContextKey{}).(Principal)
	return p, ok
}

// ViaAPIKeyFrom reports whether the context's principal is an integration
// calling with an API key (Via.Kind ViaAPIKey): a caller that is no person, so
// never the subject of a record a person is (API access design K56).
func ViaAPIKeyFrom(ctx context.Context) bool {
	p, ok := PrincipalFrom(ctx)
	return ok && p.Via != nil && p.Via.Kind == ViaAPIKey
}

// UserID answers the user the context acts for: the Principal's UserID that
// WithPrincipal (a request's session) or AsUser (a worker acting on a
// person's behalf) attached, and false when there is none or it names no
// user (a SCIM caller, uuid.Nil).
func UserID(ctx context.Context) (uuid.UUID, bool) {
	p, ok := PrincipalFrom(ctx)
	if !ok || p.UserID == uuid.Nil {
		return uuid.Nil, false
	}
	return p.UserID, true
}

// requestContextKey and responseWriterContextKey are unexported so only
// this package can set or read the context values.
type (
	requestContextKey        struct{}
	responseWriterContextKey struct{}
)

// WithRequest attaches r and its response writer to ctx. The router does
// this for every operation it dispatches, so a generated strict handler —
// which is handed only a context — can still reach the request it is
// answering: the client address, the cookies, the trace id, or a second
// Access.Check on a permission the router could not evaluate statically.
func WithRequest(ctx context.Context, w http.ResponseWriter, r *http.Request) context.Context {
	ctx = context.WithValue(ctx, requestContextKey{}, r)
	return context.WithValue(ctx, responseWriterContextKey{}, w)
}

// RequestFrom retrieves the request WithRequest attached to ctx.
func RequestFrom(ctx context.Context) (*http.Request, bool) {
	r, ok := ctx.Value(requestContextKey{}).(*http.Request)
	return r, ok
}

// ResponseWriterFrom retrieves the response writer WithRequest attached to
// ctx.
func ResponseWriterFrom(ctx context.Context) (http.ResponseWriter, bool) {
	w, ok := ctx.Value(responseWriterContextKey{}).(http.ResponseWriter)
	return w, ok
}

// asUserContextKey is unexported so only this package can set or read the
// AsUser mark.
type asUserContextKey struct{}

// AsUser is the context of a read on one user's behalf with no request behind
// it: a Principal of that id alone — no session, no roles — and the mark
// HasPermission reads. A module uses it for a background read whose caller is
// a token it has already matched to the user, never to widen a request
// (calendar feed design D5: the feed, once its token has named the person,
// asks the calendar sources for them under each source's own gate).
func AsUser(ctx context.Context, userID uuid.UUID) context.Context {
	ctx = WithPrincipal(ctx, Principal{UserID: userID})
	return context.WithValue(ctx, asUserContextKey{}, userID)
}

// AsUserFrom retrieves the user AsUser marked ctx as read on behalf of. A
// Principal set by WithPrincipal alone carries no mark: only AsUser opens
// HasPermission's second rule.
func AsUserFrom(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(asUserContextKey{}).(uuid.UUID)
	return id, ok
}

// HasPermission reports whether the caller of the request in ctx holds key,
// evaluated by a the way the router evaluates an operation's
// x-vantigo-access permission rule — or, with no request in ctx but the AsUser
// mark, whether that user holds it (HasPermissions). Any failure — neither a
// request nor the mark in ctx, infrastructure errors included — reads as
// false, so a gate built on it fails closed. It is for the handler-side checks
// the router cannot make: a permission conditional on request-body content,
// and a calendar source's gate.
func HasPermission(ctx context.Context, a Access, key string) bool {
	return HasPermissions(ctx, a, key)
}

// HasPermissions is HasPermission for several keys at once: it reports whether
// the caller holds every one of them. The keys travel in a single
// Rule{Kind: RulePermission, Names: keys}, which Access evaluates as an AND
// (identity/access.go's permitted refuses on the first key the caller does not
// hold) — one Check, not one per key, because a Check is a session lookup plus
// a permission query, and a handler gating on two permissions together should
// pay for one round of that rather than two.
//
// It has two rules, in order. A request in ctx is checked through Check, as
// the router checks it, whatever else ctx carries. With no request but the
// AsUser mark, the keys are asked of UserHolds for that user, in one question
// as well: there is no session to check, and identity still answers whether
// the user — active, not locked out — holds them. Anything else is false.
//
// It fails closed exactly as HasPermission does. Naming no key at all is false
// too, and without asking Access: an empty Names list satisfies the AND
// vacuously, which is never the gate a caller passing no keys meant to open.
func HasPermissions(ctx context.Context, a Access, keys ...string) bool {
	if len(keys) == 0 {
		return false
	}
	if r, ok := RequestFrom(ctx); ok {
		// A station's operator reached the handler only through the router's
		// wrap, for an operation open to stations (WithPrincipal is set by
		// nothing else on a request), and the wrap's Check found them holding
		// every one of StationPermissions (M134). The handler's question is
		// answered from that principal, never by a second Check: what the
		// operator may hold is StationPermissions and nothing more, and a
		// second Check could end the operator's session in the middle of the
		// handler.
		if p, ok := PrincipalFrom(ctx); ok {
			if _, station := p.Station(); station {
				for _, key := range keys {
					if !slices.Contains(StationPermissions, key) {
						return false
					}
				}
				return true
			}
		}
		rule := Rule{Kind: RulePermission, Names: keys}
		// An integration's key reached the handler only through the router's
		// wrap, which has already judged the operation's x-vantigo-api mark and
		// a read-only key against it. The handler's question is about
		// permissions alone, so it is asked as a read (API access design K33):
		// with the zero APIRefused every handler-side check would refuse a
		// key, and with APIAllowed a read-only key would be refused even on a
		// GET.
		if p, ok := PrincipalFrom(ctx); ok && p.Via != nil && p.Via.Kind == ViaAPIKey {
			rule.API = APIRead
		}
		_, err := a.Check(r, rule)
		return err == nil
	}
	if userID, ok := AsUserFrom(ctx); ok {
		held, err := a.UserHolds(ctx, userID, keys...)
		return err == nil && held
	}
	return false
}

var (
	ErrUnauthenticated = errors.New("unauthenticated")
	ErrForbidden       = errors.New("forbidden")
	// ErrStationScope is a station's operator refused an operation that is not
	// open to stations, or a work center that is not the station's: 403
	// station_scope (manufacturing phase 2 M131, M135). It is an ErrForbidden.
	ErrStationScope = fmt.Errorf("%w: station_scope", ErrForbidden)
	// ErrAPINotAvailable is an API key refused an operation marked
	// x-vantigo-api: never: 403 api_not_available (API access design K9, K34).
	ErrAPINotAvailable = fmt.Errorf("%w: api_not_available", ErrForbidden)
	// ErrAPIKeyReadOnly is a read-only API key refused an operation that is not
	// a read: 403 api_key_read_only (K9, K34).
	ErrAPIKeyReadOnly = fmt.Errorf("%w: api_key_read_only", ErrForbidden)
	// ErrAPIKeyExpired is an API key that matched but has expired: 401
	// api_key_expired (K12, K34). It is an ErrUnauthenticated.
	ErrAPIKeyExpired = fmt.Errorf("%w: api_key_expired", ErrUnauthenticated)
)

// APIKeyRefusal is Access.Check's refusal of a request that presented an API
// key identity found (API access design K19, K34): the key and its
// integration, why it was refused, and the sentinel Err that decides the
// answer. It unwraps to Err, so the router's wrap, compose.go and the live
// handler answer it as the 401 or 403 the sentinel is, and Reject writes its
// code. A key identity did not find, or one that does not parse, is a plain
// ErrUnauthenticated: there is nothing to attribute it to.
//
// Reason is one of revoked, disabled, archived, expired, network, never,
// read_only and forbidden; the request log (APIRequest.Reason) adds the
// router's own rate_limited.
type APIKeyRefusal struct {
	KeyID         uuid.UUID
	IntegrationID uuid.UUID
	Reason        string
	Err           error
}

func (e *APIKeyRefusal) Error() string { return "api key refused: " + e.Reason }
func (e *APIKeyRefusal) Unwrap() error { return e.Err }

// The reasons the request log records a refused API key request under: an
// APIKeyRefusal's, and the router's own APIReasonRateLimited.
const (
	APIReasonRevoked     = "revoked"
	APIReasonDisabled    = "disabled"
	APIReasonArchived    = "archived"
	APIReasonExpired     = "expired"
	APIReasonNetwork     = "network"
	APIReasonNever       = "never"
	APIReasonReadOnly    = "read_only"
	APIReasonForbidden   = "forbidden"
	APIReasonRateLimited = "rate_limited"
)

// APIRequest is one request an API key made that identity could attribute
// (API access design K23): the key and its integration, the operation and how
// it was answered. It never carries a body, a query string or a path
// parameter's value, which can hold personal data; OperationID and TraceID
// find the request in the server's traces.
type APIRequest struct {
	// At is when the request arrived; the recorder stamps its own clock when
	// it is zero.
	At            time.Time
	KeyID         uuid.UUID
	IntegrationID uuid.UUID
	Method        string
	OperationID   string
	Status        int
	// Reason is set for a refusal: an APIKeyRefusal's, or
	// APIReasonRateLimited.
	Reason   string
	Duration time.Duration
	ClientIP string
	TraceID  string
}

// APIRequestRecorder is the request log's port (K23, K35): the router's wrap
// calls Record once a key request it can attribute has been answered, an
// admitted one or a refused one. identity's Access implements it beside
// Access; the router finds it by a type assertion on the Access it holds, as
// the live hub finds StationAccess. Record never blocks on the database and
// never fails the request: the log is an operational record, not the audit
// trail, and what it cannot keep it drops.
type APIRequestRecorder interface {
	Record(ctx context.Context, req APIRequest)
}

// APIKeyScope is what reaches an API key's scope without importing identity:
// the document of what the key may call (API access design K28, K36).
// identity's Access implements it beside Access; a caller finds it by a type
// assertion on the Access it holds, as the live hub finds StationAccess.
type APIKeyScope interface {
	// Scope answers what the key of p (Via.Kind ViaAPIKey) reaches now: the
	// permission keys its integration holds through every effective role,
	// less APINever, cut down to the key's narrowing when it has one, in
	// ordinal order; and whether the key is read-only. A principal that is
	// not an API key's, or a key that no longer exists, is an error.
	Scope(ctx context.Context, p Principal) (permissions []string, readOnly bool, err error)
}

// The values of an integration's MCP setting (MCP server design M1): what its
// keys may do at /api/mcp.
const (
	MCPAccessOff       = "off"
	MCPAccessRead      = "read"
	MCPAccessReadWrite = "read_write"
)

// MCPKeyAccess is what the MCP endpoint needs of an API key, read at once
// (MCP server design M22): its integration's MCP setting, and the scope
// APIKeyScope answers.
type MCPKeyAccess struct {
	// Mode is the integration's mcp_access: MCPAccessOff, MCPAccessRead or
	// MCPAccessReadWrite.
	Mode string
	// Permissions and ReadOnly are APIKeyScope.Scope's answer for the key.
	Permissions []string
	ReadOnly    bool
}

// Effective is the narrower of the integration's setting and the key (M3): a
// read-only key is MCPAccessRead under MCPAccessReadWrite.
func (m MCPKeyAccess) Effective() string {
	if m.Mode == MCPAccessReadWrite && m.ReadOnly {
		return MCPAccessRead
	}
	switch m.Mode {
	case MCPAccessRead, MCPAccessReadWrite:
		return m.Mode
	}
	return MCPAccessOff
}

// MCPAccess is how the MCP endpoint reads an API key's MCP setting and scope
// without importing identity (MCP server design M6, M22). identity's Access
// implements it beside APIKeyScope; the endpoint finds it by a type assertion.
type MCPAccess interface {
	// MCP answers, for the key of p (Via.Kind ViaAPIKey), its integration's
	// MCP setting and the key's scope as Scope answers it. A principal that is
	// not an API key's, or a key that no longer exists, is an error.
	MCP(ctx context.Context, p Principal) (MCPKeyAccess, error)
}

// StationAccess is what the live hub asks of identity for a shared station's
// socket (manufacturing phase 2 §17), which carries the station cookie and no
// operator: a browser WebSocket cannot set Authorization. identity's Access
// implements it beside Access; the hub finds it by a type assertion.
type StationAccess interface {
	// CheckStation resolves the station cookie of a handshake. It answers
	// false, with no error, when r is not a station's handshake — no station
	// cookie, or a session cookie beside it, which makes it a person's. A
	// station that is unknown, revoked, expired or reached from outside its
	// network allowlist is ErrUnauthenticated, for Access.Reject. pairing names
	// the pairing the cookie belongs to, opaque to the caller, which hands it
	// back to StationLive: a station paired again is another pairing.
	CheckStation(r *http.Request) (stationID uuid.UUID, pairing []byte, isStation bool, err error)
	// StationLive reports whether the station is still paired — by the
	// pairing CheckStation answered, so a socket of the browser a station was
	// paired away from ends — unrevoked and unexpired, and ip — the address
	// the socket's handshake came from — still inside its network allowlist,
	// for the socket's 60-second re-check.
	StationLive(ctx context.Context, stationID uuid.UUID, pairing []byte, ip string) (bool, error)
	// StationKnown reports whether a station of that id is registered, in
	// use or not: what a module that keeps settings per station checks an id
	// against before it keeps one.
	StationKnown(ctx context.Context, stationID uuid.UUID) (bool, error)
}

// Access authenticates a request and evaluates an access rule. Identity
// implements it; every module's router uses it.
type Access interface {
	// Check returns the caller if rule is satisfied. For RuleAnonymous it
	// never fails on credentials: it returns the caller when a valid session
	// exists, else a zero Principal. It returns ErrUnauthenticated or
	// ErrForbidden (possibly wrapped) when the rule is not met, and any other
	// error for infrastructure failures.
	Check(r *http.Request, rule Rule) (Principal, error)
	// Reject writes the response for ErrUnauthenticated/ErrForbidden under rule.
	Reject(w http.ResponseWriter, r *http.Request, rule Rule, err error)
	// UserHolds reports whether userID — active, not locked out — holds every
	// key: an owner holds all, anyone else each through an effective role. It
	// is for a read made on a user's behalf where there is no request to
	// check: the calendar feed. Any failure is an error, never a false.
	UserHolds(ctx context.Context, userID uuid.UUID, keys ...string) (bool, error)
	// UserSatisfies reports whether userID — active, not locked out — meets
	// rule, a permission rule as UserHolds, or a policy rule as far as it can be
	// known with no session behind the question: the policies that name a role
	// (Owner, SystemAdmin) and the stewardship of roles are decided for the
	// user's roles as they stand, and a second factor, which only a session has,
	// is not assumed (so a policy that requires one is not satisfied unless the
	// installation requires none). It is for a person who decides what another
	// principal may do on the person's authority: the agents module's review
	// queue, where a reviewer's approval must not grant more than the reviewer
	// could do (virtual employees design V20). Any failure is an error, never a
	// false.
	UserSatisfies(ctx context.Context, userID uuid.UUID, rule Rule) (bool, error)
	// SessionLive reports whether the session sessionID is still live — not
	// revoked, expired or idle-expired, and its user still one a session may
	// belong to — and whose it is. Unlike Check it never slides the idle
	// window: the live hub (srv/live, design L11) re-checks an open socket
	// with it every 60 seconds, and a hidden tab must not keep its session
	// alive for ever. A session that is not live is (uuid.Nil, false, nil);
	// any failure to read is an error.
	SessionLive(ctx context.Context, sessionID uuid.UUID) (uuid.UUID, bool, error)
}
