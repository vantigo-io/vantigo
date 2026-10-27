// Package graphtest is a fake Microsoft Entra ID for the entra module's tests:
// an http.RoundTripper answering the two hosts the module's client calls —
// login.microsoftonline.com's token endpoint and graph.microsoft.com/v1.0 —
// from memory, in the shapes Graph answers (Entra research §3.1–3.5,
// docs/superpowers/research/2026-10-19-entra-module.md). It is wired as the
// module's transport (modtest.WithTransport), never as a base URL, so the
// client under test builds the real hosts' addresses.
//
// It is exported so the module's tests and the tagged test's comparison can
// share it, and is test-only by convention: depguard refuses it to every
// non-test file.
//
// What it keeps faithful, because the client must cope with it: the token is
// a JWT whose roles claim carries the granted application permissions; a
// user is created with Graph's required properties, a taken UPN answering
// 400 Request_BadRequest with an ObjectConflict detail; GET /users/{id}
// without $select answers Graph's default property set, not every property;
// a soft-deleted user leaves /users but keeps its UPN; a licence needs a
// usage location first; the token's oid claim names the application's
// service principal, the one owner besides users a group may have; a sensitive write on a privileged user, or by an
// application without the User Administrator role, is 403
// Authorization_RequestDenied; a user synchronised from on-premises refuses
// every write with 400 Request_BadRequest; a member removed without /$ref
// deletes the user; a Team on a group younger than TeamReadyAfter is 404; a
// group's members are listed in pages of $top with an @odata.nextLink; a
// $batch holds at most twenty requests; a user's photo is a JPEG of at most
// 4 MB, 413 beyond; a $search or a $count is 400 Request_UnsupportedQuery
// without ConsistencyLevel: eventual, an in filter lists at most fifteen
// values; a user's calendars and events keep the extended property they
// were written with, are listed in pages of $top with an @odata.nextLink,
// and answer 403 for a mailbox outside the application's scope and 404
// MailboxNotEnabledForRESTAPI for a user with none (mailbox.go). Every error
// body is Graph's
// { "error": { "code", "message" } }, the token endpoint's OAuth's
// { "error", "error_description" }.
package graphtest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// The hosts the fake answers; any other is a transport error, as a dial to
// an address nobody listens on would be.
const (
	TokenHost = "login.microsoftonline.com"
	GraphHost = "graph.microsoft.com"
)

// The sensitive properties (research §3.1): writing one needs the User
// Administrator role besides the permission, and none may be written on a
// user holding an administrative role.
var sensitiveProperties = map[string]bool{
	"accountEnabled": true, "passwordProfile": true, "userPrincipalName": true,
	"businessPhones": true, "mobilePhone": true, "otherMails": true,
}

// defaultUserProperties are what GET /users/{id} answers without $select.
var defaultUserProperties = []string{
	"businessPhones", "displayName", "givenName", "jobTitle", "mail", "mobilePhone",
	"officeLocation", "preferredLanguage", "surname", "userPrincipalName", "id",
}

// FakeGraph is the fake tenant. Its configuration fields are read under its
// lock on every request: set them before the first request, or through
// Configure while requests may be in flight. Its state is read through
// Users, User, Deleted, Group and Calls.
type FakeGraph struct {
	// TenantID is the tenant the token endpoint answers for; a token
	// requested for another is refused invalid_request. Empty answers any.
	TenantID string
	// Secrets are the valid client secrets by client id. A client id absent
	// here is refused unauthorized_client, a secret not listed for it
	// invalid_client.
	Secrets map[string][]string
	// Roles are the application permissions a token carries in its roles
	// claim. None is a token without the claim: consent not granted.
	Roles []string
	// TokenLifetime is the token's expires_in; an hour when zero.
	TokenLifetime time.Duration
	// Organisation is the organisation's displayName.
	Organisation string
	// Domains are the tenant's domains; /organization lists the verified.
	Domains []Domain
	// Skus are the licences the tenant subscribes to, with their seats.
	Skus []Sku
	// RoleMissing is the User Administrator role not assigned to the
	// application: every sensitive write is 403.
	RoleMissing bool
	// HireDateDenied refuses employeeHireDate to the application, 403, as
	// the update page says an app-only caller is (research §3.1's
	// UNCERTAIN): a PATCH carrying it writes nothing.
	HireDateDenied bool
	// PhotoDenied refuses a user's photo to the application, 403, as a
	// tenant whose consent lacks the photo's permission does: a PUT or a
	// DELETE of /photo/$value changes nothing.
	PhotoDenied bool
	// PassPolicyDenied is the tenant's authentication methods policy
	// leaving the pass off: a pass's create is 400.
	PassPolicyDenied bool
	// TeamReadyAfter is how old a group must be before PUT /team stops
	// answering 404; fifteen minutes in the tenant, zero here unless set.
	TeamReadyAfter time.Duration
	// TeamNotFound makes the next n PUT /team answer 404 whatever the
	// group's age, as a Teams backend that has not seen the group yet does.
	TeamNotFound int
	// ServicePrincipalID is the application's service principal in the
	// tenant: the token's oid claim, and the one owner of a group that is
	// not a user. New sets a fresh one.
	ServicePrincipalID string
	// Now is the fake's clock, for the token's expiry, the pass's and the
	// group's age; time.Now when nil.
	Now func() time.Time

	mu            sync.Mutex
	users         []*User
	groups        []*Group
	tokens        map[string]time.Time
	throttle      int
	batchThrottle int
	batchWait     int // seconds, ThrottleBatchFor's; 0 is 1 as a string
	failNext      []int
	calls         []Call
	mailboxes     map[string]*mailbox
	serial        int
}

// Domain is one of the tenant's domains.
type Domain struct {
	Name       string
	IsVerified bool
	IsDefault  bool
}

// Sku is one licence the tenant subscribes to: its id, its part number, the
// seats bought and the seats assigned.
type Sku struct {
	SkuID      string
	PartNumber string
	Enabled    int
	Consumed   int
}

// OrgData is employeeOrgData, written whole.
type OrgData struct {
	CostCenter string `json:"costCenter,omitempty"`
	Division   string `json:"division,omitempty"`
}

// LicenceState is one entry of a user's licenseAssignmentStates.
type LicenceState struct {
	SkuID           string
	AssignedByGroup string // "" for a direct assignment
	State           string // "Active" | "Error"
	Error           string // "None" | "CountViolation" | …
}

// Pass is a Temporary Access Pass the fake issued. Value is kept for the
// test to compare with what the module reveals; it is never in Calls.
type Pass struct {
	ID                string
	Value             string
	LifetimeInMinutes int
	IsUsableOnce      bool
	CreatedAt         time.Time
}

// User is a user in the fake tenant, as Users and User answer it.
type User struct {
	ID                    string
	AccountEnabled        bool
	DisplayName           string
	GivenName             string
	Surname               string
	MailNickname          string
	UserPrincipalName     string
	Mail                  string
	EmployeeID            string
	EmployeeHireDate      *time.Time
	EmployeeLeaveDateTime *time.Time
	EmployeeType          string
	EmployeeOrgData       *OrgData
	JobTitle              string
	Department            string
	CompanyName           string
	OfficeLocation        string
	StreetAddress         string
	PostalCode            string
	City                  string
	Country               string
	BusinessPhones        []string
	MobilePhone           string
	UsageLocation         string
	PreferredLanguage     string
	// UserType is "Member" or "Guest", as Graph answers it; "" is answered
	// as "Member", Graph's default for an account the tenant created.
	UserType string
	// ForceChangePassword is the create's passwordProfile flag; the
	// password itself is never kept.
	ForceChangePassword bool
	// ManagerID is the manager's object id, set by PUT /manager/$ref.
	ManagerID string
	// Licences are the assignments, direct and by group, with their errors.
	Licences []LicenceState
	// SessionsRevokedAt is the last revokeSignInSessions.
	SessionsRevokedAt *time.Time
	// Pass is the user's one pass; a new one replaces it.
	Pass *Pass
	// Photo is the bytes of the user's photo as the last PUT of
	// /photo/$value sent them, PhotoContentType their media type; nil when
	// the user has none.
	Photo            []byte
	PhotoContentType string
	// OnPremisesSyncEnabled is a user synchronised from on-premises Active
	// Directory: every write is 400.
	OnPremisesSyncEnabled bool
	// Privileged is a user holding an administrative role: every sensitive
	// write and the delete are 403.
	Privileged bool
	// Deleted is a soft-deleted user: gone from /users, its UPN still held.
	Deleted bool
	// MailboxDenied is a mailbox outside the application's RBAC for
	// Applications scope: every calendar and event request is 403
	// ErrorAccessDenied.
	MailboxDenied bool
	// NoMailbox is a user with no mailbox in Exchange Online: every calendar
	// and event request is 404 MailboxNotEnabledForRESTAPI.
	NoMailbox bool
}

// Group is a group in the fake tenant.
type Group struct {
	ID                 string
	DisplayName        string
	MailNickname       string
	Description        string
	MailEnabled        bool
	SecurityEnabled    bool
	GroupTypes         []string
	Visibility         string
	MembershipRule     string // set on a dynamic group
	IsAssignableToRole bool
	Owners             []string
	Members            []string
	// StaleMembers are ids the members list still answers although they
	// left — Graph's replication lag: removing one by $ref is 404.
	StaleMembers []string
	CreatedAt    time.Time
	Team         bool
	Deleted      bool
}

// Call is one request the fake answered: never a body, a header or a
// secret. Batch marks a request made inside a $batch.
type Call struct {
	Method string
	Host   string
	Path   string
	Query  string
	Status int
	Batch  bool
}

// New is an empty tenant answering any tenant id, with no client yet.
func New() *FakeGraph {
	return &FakeGraph{Secrets: map[string][]string{}, ServicePrincipalID: uuid.NewString()}
}

// Configure runs fn under the fake's lock, for a change of configuration
// while requests may be in flight.
func (g *FakeGraph) Configure(fn func(g *FakeGraph)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	fn(g)
}

// Throttle makes the next n calls to Graph (not the token endpoint) answer
// 429 TooManyRequests with Retry-After: 1.
func (g *FakeGraph) Throttle(n int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.throttle += n
}

// FailNext makes the next call to Graph (not the token endpoint) answer
// status with Graph's error body; each call queues one more.
func (g *FakeGraph) FailNext(status int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failNext = append(g.failNext, status)
}

// Calls is every request answered so far, in order.
func (g *FakeGraph) Calls() []Call {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.calls)
}

// AddUser seeds a user and answers it with its id, a fresh one when u has
// none. A user Prepare should find, a privileged one or a synchronised one
// is seeded this way.
func (g *FakeGraph) AddUser(u User) User {
	g.mu.Lock()
	defer g.mu.Unlock()
	if u.ID == "" {
		u.ID = uuid.NewString()
	}
	stored := u
	g.users = append(g.users, &stored)
	return copyUser(&stored)
}

// EditUser runs fn on the user with id under the fake's lock — marking an
// existing user privileged, say. It reports whether the user exists.
func (g *FakeGraph) EditUser(id string, fn func(u *User)) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	u := g.userByID(id, true)
	if u == nil {
		return false
	}
	fn(u)
	return true
}

// Users is every user not deleted, in the order they were made.
func (g *FakeGraph) Users() []User {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []User
	for _, u := range g.users {
		if !u.Deleted {
			out = append(out, copyUser(u))
		}
	}
	return out
}

// Deleted is every soft-deleted user.
func (g *FakeGraph) Deleted() []User {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []User
	for _, u := range g.users {
		if u.Deleted {
			out = append(out, copyUser(u))
		}
	}
	return out
}

// User is the user with id, deleted or not.
func (g *FakeGraph) User(id string) (User, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	u := g.userByID(id, true)
	if u == nil {
		return User{}, false
	}
	return copyUser(u), true
}

// AddGroup seeds a group — a dynamic one, a role-assignable one — and
// answers it with its id.
func (g *FakeGraph) AddGroup(gr Group) Group {
	g.mu.Lock()
	defer g.mu.Unlock()
	if gr.ID == "" {
		gr.ID = uuid.NewString()
	}
	if gr.CreatedAt.IsZero() {
		gr.CreatedAt = g.now()
	}
	stored := gr
	g.groups = append(g.groups, &stored)
	return copyGroup(&stored)
}

// EditGroup runs fn on the group with id under the fake's lock — making a
// group the module made dynamic, say. It reports whether the group exists.
func (g *FakeGraph) EditGroup(id string, fn func(gr *Group)) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	gr := g.groupByID(id, true)
	if gr == nil {
		return false
	}
	fn(gr)
	return true
}

// Groups is every group not deleted, in the order they were made.
func (g *FakeGraph) Groups() []Group {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []Group
	for _, gr := range g.groups {
		if !gr.Deleted {
			out = append(out, copyGroup(gr))
		}
	}
	return out
}

// Group is the group with id, deleted or not.
func (g *FakeGraph) Group(id string) (Group, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	gr := g.groupByID(id, true)
	if gr == nil {
		return Group{}, false
	}
	return copyGroup(gr), true
}

// RoundTrip answers req from the fake tenant. It reads and closes the
// request's body.
func (g *FakeGraph) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		body = b
	}
	if req.URL.Scheme != "https" || (req.URL.Host != TokenHost && req.URL.Host != GraphHost) {
		return nil, fmt.Errorf("graphtest: no such host: %s://%s", req.URL.Scheme, req.URL.Host)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	var a answer
	if req.URL.Host == TokenHost {
		a = g.token(req, body)
	} else {
		a = g.api(req, body)
	}
	g.calls = append(g.calls, Call{Method: req.Method, Host: req.URL.Host, Path: req.URL.Path, Query: req.URL.RawQuery, Status: a.status})
	return a.response(req), nil
}

// answer is one response before it is written.
type answer struct {
	status int
	header http.Header
	body   any    // nil is no body
	raw    []byte // a body sent as it is (a photo), its type in header
}

func (a answer) response(req *http.Request) *http.Response {
	h := a.header
	if h == nil {
		h = http.Header{}
	}
	b := a.raw
	if a.body != nil {
		b, _ = json.Marshal(a.body)
		h.Set("Content-Type", "application/json; charset=utf-8")
	}
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", a.status, http.StatusText(a.status)),
		StatusCode:    a.status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        h,
		Body:          io.NopCloser(bytes.NewReader(b)),
		ContentLength: int64(len(b)),
		Request:       req,
	}
}

// graphError is Graph's error body.
func graphError(status int, code, message string) answer {
	return answer{status: status, body: map[string]any{"error": map[string]any{"code": code, "message": message}}}
}

func (g *FakeGraph) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// --- The token endpoint ------------------------------------------------------

var tokenPath = regexp.MustCompile(`^/([^/]+)/oauth2/v2\.0/token$`)

// token is the client credentials grant (research §3.5).
func (g *FakeGraph) token(req *http.Request, body []byte) answer {
	oauth := func(status int, code, description string) answer {
		return answer{status: status, body: map[string]any{"error": code, "error_description": description}}
	}
	m := tokenPath.FindStringSubmatch(req.URL.Path)
	if req.Method != http.MethodPost || m == nil {
		return oauth(http.StatusNotFound, "invalid_request", "AADSTS900561: The endpoint only accepts POST requests.")
	}
	form, err := url.ParseQuery(string(body))
	if err != nil {
		return oauth(http.StatusBadRequest, "invalid_request", "AADSTS900144: The request body must be form-encoded.")
	}
	switch {
	case g.TenantID != "" && !strings.EqualFold(m[1], g.TenantID):
		return oauth(http.StatusBadRequest, "invalid_request", "AADSTS90002: Tenant not found.")
	case form.Get("grant_type") != "client_credentials":
		return oauth(http.StatusBadRequest, "unsupported_grant_type", "AADSTS70003: The app requested an unsupported grant type.")
	case form.Get("scope") != "https://graph.microsoft.com/.default":
		return oauth(http.StatusBadRequest, "invalid_scope", "AADSTS1002012: The provided value for scope is not valid.")
	}
	secrets, known := g.Secrets[form.Get("client_id")]
	if !known {
		return oauth(http.StatusBadRequest, "unauthorized_client", "AADSTS700016: Application not found in the directory.")
	}
	if !slices.Contains(secrets, form.Get("client_secret")) {
		return oauth(http.StatusUnauthorized, "invalid_client", "AADSTS7000215: Invalid client secret provided.")
	}

	lifetime := g.TokenLifetime
	if lifetime == 0 {
		lifetime = time.Hour
	}
	now := g.now()
	claims := map[string]any{
		"aud":   "https://graph.microsoft.com",
		"tid":   m[1],
		"appid": form.Get("client_id"),
		"iat":   now.Unix(),
		"exp":   now.Add(lifetime).Unix(),
	}
	if len(g.Roles) > 0 {
		claims["roles"] = slices.Clone(g.Roles)
	}
	if g.ServicePrincipalID != "" {
		claims["oid"] = g.ServicePrincipalID
	}
	token := unsignedJWT(claims)
	if g.tokens == nil {
		g.tokens = map[string]time.Time{}
	}
	g.tokens[token] = now.Add(lifetime)
	return answer{status: http.StatusOK, body: map[string]any{
		"token_type":     "Bearer",
		"expires_in":     int(lifetime / time.Second),
		"ext_expires_in": int(lifetime / time.Second),
		"access_token":   token,
	}}
}

// unsignedJWT is a JWT with alg none and an empty signature: the module
// reads the roles claim of a token it was given and checks no signature, so
// the fake signs nothing. The jti keeps two tokens issued in one second
// apart.
func unsignedJWT(claims map[string]any) string {
	claims["jti"] = uuid.NewString()
	header, _ := json.Marshal(map[string]string{"alg": "none", "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload) + "."
}

// --- Graph -------------------------------------------------------------------

// api answers one call to graph.microsoft.com: the bearer token checked,
// then a queued throttle or failure, then the route.
func (g *FakeGraph) api(req *http.Request, body []byte) answer {
	token, ok := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
	expires, issued := g.tokens[token]
	switch {
	case !ok || token == "":
		return graphError(http.StatusUnauthorized, "InvalidAuthenticationToken", "Access token is empty.")
	case !issued:
		return graphError(http.StatusUnauthorized, "InvalidAuthenticationToken", "Access token validation failure. Invalid audience.")
	case !g.now().Before(expires):
		return graphError(http.StatusUnauthorized, "InvalidAuthenticationToken", "Lifetime validation failed, the token is expired.")
	}
	if g.throttle > 0 {
		g.throttle--
		a := graphError(http.StatusTooManyRequests, "TooManyRequests", "Too many requests.")
		a.header = http.Header{"Retry-After": []string{"1"}}
		return a
	}
	if len(g.failNext) > 0 {
		status := g.failNext[0]
		g.failNext = g.failNext[1:]
		return graphError(status, failureCode(status), "The fake was told to fail this call.")
	}
	path, ok := strings.CutPrefix(req.URL.Path, "/v1.0")
	if !ok {
		return graphError(http.StatusBadRequest, "BadRequest", "Invalid version.")
	}
	if req.Method == http.MethodPost && path == "/$batch" {
		return g.batch(body)
	}
	if q := req.URL.Query(); (q.Has("$search") || q.Has("$count")) && !strings.EqualFold(req.Header.Get("ConsistencyLevel"), "eventual") {
		return graphError(http.StatusBadRequest, "Request_UnsupportedQuery",
			"Request with $search or $count query parameters only works with 'ConsistencyLevel' header set to 'eventual'.")
	}
	return g.route(req.Method, path, req.URL.Query(), req.Header.Get("Content-Type"), body)
}

// failureCode is Graph's code for a status FailNext queued.
func failureCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "Request_BadRequest"
	case http.StatusForbidden:
		return "Authorization_RequestDenied"
	case http.StatusNotFound:
		return "Request_ResourceNotFound"
	case http.StatusServiceUnavailable:
		return "serviceNotAvailable"
	case http.StatusGatewayTimeout:
		return "timeout"
	default:
		return "generalException"
	}
}

// route answers one Graph request by its method and path (after /v1.0),
// its body of contentType.
func (g *FakeGraph) route(method, path string, query url.Values, contentType string, body []byte) answer {
	seg := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case method == http.MethodGet && path == "/organization":
		return g.organization()
	case method == http.MethodGet && path == "/domains":
		return g.domains()
	case method == http.MethodGet && path == "/subscribedSkus":
		return g.subscribedSkus()
	case seg[0] == "users":
		return g.usersRoute(method, seg[1:], query, contentType, body)
	case seg[0] == "groups":
		return g.groupsRoute(method, seg[1:], query, body)
	}
	return graphError(http.StatusNotFound, "Request_ResourceNotFound", "Resource not found for the segment '"+seg[0]+"'.")
}

func (g *FakeGraph) organization() answer {
	var verified []map[string]any
	for _, d := range g.Domains {
		if d.IsVerified {
			verified = append(verified, map[string]any{"name": d.Name, "isDefault": d.IsDefault, "isInitial": strings.HasSuffix(d.Name, ".onmicrosoft.com"), "type": "Managed"})
		}
	}
	tenant := g.TenantID
	if tenant == "" {
		tenant = "00000000-0000-0000-0000-000000000000"
	}
	return answer{status: http.StatusOK, body: map[string]any{"value": []any{map[string]any{
		"id": tenant, "displayName": g.Organisation, "verifiedDomains": orEmpty(verified),
	}}}}
}

func (g *FakeGraph) domains() answer {
	var out []map[string]any
	for _, d := range g.Domains {
		out = append(out, map[string]any{"id": d.Name, "isVerified": d.IsVerified, "isDefault": d.IsDefault, "authenticationType": "Managed"})
	}
	return answer{status: http.StatusOK, body: map[string]any{"value": orEmpty(out)}}
}

func (g *FakeGraph) subscribedSkus() answer {
	var out []map[string]any
	for _, s := range g.Skus {
		out = append(out, map[string]any{
			"skuId": s.SkuID, "skuPartNumber": s.PartNumber, "capabilityStatus": "Enabled",
			"consumedUnits": s.Consumed,
			"prepaidUnits":  map[string]any{"enabled": s.Enabled, "suspended": 0, "warning": 0},
		})
	}
	return answer{status: http.StatusOK, body: map[string]any{"value": orEmpty(out)}}
}

// --- Users -------------------------------------------------------------------

func (g *FakeGraph) usersRoute(method string, seg []string, query url.Values, contentType string, body []byte) answer {
	if len(seg) == 0 {
		switch method {
		case http.MethodGet:
			return g.listUsers(query)
		case http.MethodPost:
			return g.createUser(body)
		}
		return methodNotAllowed()
	}
	u := g.userByID(seg[0], false)
	if u == nil {
		return notFound(seg[0])
	}
	rest := strings.Join(seg[1:], "/")
	switch {
	case rest == "" && method == http.MethodGet:
		return answer{status: http.StatusOK, body: g.selectUser(u, query.Get("$select"))}
	case rest == "" && method == http.MethodPatch:
		return g.patchUser(u, body)
	case rest == "" && method == http.MethodDelete:
		if a, refused := g.refuseSensitive(u); refused {
			return a
		}
		u.Deleted = true
		return answer{status: http.StatusNoContent}
	case rest == "manager/$ref" && method == http.MethodPut:
		return g.setManager(u, body)
	case rest == "revokeSignInSessions" && method == http.MethodPost:
		if u.OnPremisesSyncEnabled {
			return syncedRefusal()
		}
		now := g.now()
		u.SessionsRevokedAt = &now
		return answer{status: http.StatusOK, body: map[string]any{"value": true}}
	case rest == "assignLicense" && method == http.MethodPost:
		return g.assignLicense(u, body)
	case rest == "memberOf" && method == http.MethodGet:
		return g.memberOf(u)
	case rest == "authentication/temporaryAccessPassMethods" && method == http.MethodPost:
		return g.createPass(u, body)
	case rest == "photo/$value":
		return g.photo(u, method, contentType, body)
	case len(seg) > 1 && (seg[1] == "calendars" || seg[1] == "events"):
		return g.mailboxRoute(u, method, seg[1:], query, body)
	}
	return graphError(http.StatusBadRequest, "BadRequest", "Unsupported request: "+method+" users/{id}/"+rest+".")
}

// photoMaxBytes is the most a photo may weigh: Graph's "limited to 4 MB",
// read as decimal megabytes.
const photoMaxBytes = 4_000_000

// photo is /users/{id}/photo/$value: GET answers the bytes (404 when the
// user has none); PUT keeps the body when it is image/jpeg — 400 for
// another type or none, 413 over photoMaxBytes, 403 when PhotoDenied —
// and answers 200; DELETE removes it, 200 (404 when there is none, 403
// when PhotoDenied).
func (g *FakeGraph) photo(u *User, method, contentType string, body []byte) answer {
	switch method {
	case http.MethodGet:
		if u.Photo == nil {
			return graphError(http.StatusNotFound, "ImageNotFound", "Exception of type 'Microsoft.Fast.Profile.Core.Exception.ImageNotFoundException' was thrown.")
		}
		return answer{status: http.StatusOK, header: http.Header{"Content-Type": []string{u.PhotoContentType}}, raw: slices.Clone(u.Photo)}
	case http.MethodPut:
		switch {
		case g.PhotoDenied:
			return graphError(http.StatusForbidden, "Authorization_RequestDenied", "Insufficient privileges to complete the operation.")
		case len(body) > photoMaxBytes:
			return graphError(http.StatusRequestEntityTooLarge, "RequestEntityTooLarge", "The photo is larger than 4 MB.")
		case contentType != "image/jpeg" || len(body) == 0:
			return graphError(http.StatusBadRequest, "ErrorInvalidImage", "The photo is not an image the service takes.")
		}
		u.Photo, u.PhotoContentType = slices.Clone(body), contentType
		return answer{status: http.StatusOK}
	case http.MethodDelete:
		if g.PhotoDenied {
			return graphError(http.StatusForbidden, "Authorization_RequestDenied", "Insufficient privileges to complete the operation.")
		}
		if u.Photo == nil {
			return graphError(http.StatusNotFound, "ImageNotFound", "Exception of type 'Microsoft.Fast.Profile.Core.Exception.ImageNotFoundException' was thrown.")
		}
		u.Photo, u.PhotoContentType = nil, ""
		return answer{status: http.StatusOK}
	}
	return methodNotAllowed()
}

var (
	employeeIDFilter   = regexp.MustCompile(`^employeeId eq '((?:[^']|'')*)'$`)
	mailNicknameFilter = regexp.MustCompile(`^mailNickname eq '((?:[^']|'')*)'$`)
	// inFilter is "<property> in ('…', …)" on one of the three properties
	// the directory's matches filter on.
	inFilter = regexp.MustCompile(`^(mail|userPrincipalName|employeeId) in \(\s*('(?:[^']|'')*'(?:\s*,\s*'(?:[^']|'')*')*)\s*\)$`)
	// inValue is one quoted value of an in filter.
	inValue = regexp.MustCompile(`'((?:[^']|'')*)'`)
	// searchClause is one quoted clause of a $search: "property:value".
	searchClause = regexp.MustCompile(`^"(displayName|mail|userPrincipalName):([^"]*)"$`)
)

// inMaxValues is the most values one in filter may list, as Graph allows.
const inMaxValues = 15

// listUsers is GET /users as the module asks it: $filter=employeeId eq '…'
// (an account's lookup), $filter=<mail|userPrincipalName|employeeId> in
// ('…', …) of at most fifteen values, compared ignoring case (the
// directory's matches), or $search of "property:value" clauses on
// displayName, mail and userPrincipalName joined by OR, each a
// case-insensitive substring (the picker); with $orderby=displayName, $top
// and $count=true. Anything else is refused as Graph refuses an unsupported
// query; api refused a $search or a $count without ConsistencyLevel:
// eventual already. Deleted users are not listed.
func (g *FakeGraph) listUsers(query url.Values) answer {
	unsupported := graphError(http.StatusBadRequest, "Request_UnsupportedQuery", "Unsupported or invalid query filter clause specified.")
	var match func(u *User) bool
	switch filter, search := query.Get("$filter"), query.Get("$search"); {
	case filter != "" && search != "":
		return unsupported
	case search != "":
		var clauses [][]string
		for _, part := range strings.Split(search, " OR ") {
			m := searchClause.FindStringSubmatch(strings.TrimSpace(part))
			if m == nil {
				return graphError(http.StatusBadRequest, "Request_UnsupportedQuery", "Syntax error: invalid $search.")
			}
			clauses = append(clauses, m[1:])
		}
		match = func(u *User) bool {
			for _, c := range clauses {
				value := map[string]string{"displayName": u.DisplayName, "mail": u.Mail, "userPrincipalName": u.UserPrincipalName}[c[0]]
				if strings.Contains(strings.ToLower(value), strings.ToLower(c[1])) {
					return true
				}
			}
			return false
		}
	case employeeIDFilter.MatchString(filter):
		number := strings.ReplaceAll(employeeIDFilter.FindStringSubmatch(filter)[1], "''", "'")
		match = func(u *User) bool { return u.EmployeeID == number }
	case inFilter.MatchString(filter):
		m := inFilter.FindStringSubmatch(filter)
		var values []string
		for _, v := range inValue.FindAllStringSubmatch(m[2], -1) {
			values = append(values, strings.ReplaceAll(v[1], "''", "'"))
		}
		if len(values) > inMaxValues {
			return graphError(http.StatusBadRequest, "Request_UnsupportedQuery", "Too many values in the in operator.")
		}
		match = func(u *User) bool {
			value := map[string]string{"mail": u.Mail, "userPrincipalName": u.UserPrincipalName, "employeeId": u.EmployeeID}[m[1]]
			return value != "" && slices.ContainsFunc(values, func(v string) bool { return strings.EqualFold(v, value) })
		}
	default:
		return unsupported
	}
	var found []*User
	for _, u := range g.users {
		if !u.Deleted && match(u) {
			found = append(found, u)
		}
	}
	switch order := query.Get("$orderby"); order {
	case "":
	case "displayName":
		slices.SortStableFunc(found, func(a, b *User) int {
			return strings.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName))
		})
	default:
		return graphError(http.StatusBadRequest, "Request_UnsupportedQuery", "Unsupported $orderby: "+order+".")
	}
	count := len(found)
	if top := query.Get("$top"); top != "" {
		n, err := strconv.Atoi(top)
		if err != nil || n < 1 || n > 999 {
			return graphError(http.StatusBadRequest, "Request_BadRequest", "Invalid page size specified.")
		}
		found = found[:min(n, len(found))]
	}
	out := []any{}
	for _, u := range found {
		out = append(out, g.selectUser(u, query.Get("$select")))
	}
	body := map[string]any{"value": out}
	if query.Get("$count") == "true" {
		body["@odata.count"] = count
	}
	return answer{status: http.StatusOK, body: body}
}

// createUser is POST /users: Graph's five required properties, a UPN in the
// alphabet at a verified domain, free (deleted users still hold theirs).
func (g *FakeGraph) createUser(body []byte) answer {
	var props map[string]json.RawMessage
	if err := json.Unmarshal(body, &props); err != nil {
		return graphError(http.StatusBadRequest, "Request_BadRequest", "Invalid JSON.")
	}
	for _, required := range []string{"accountEnabled", "displayName", "mailNickname", "userPrincipalName", "passwordProfile"} {
		if _, ok := props[required]; !ok {
			return graphError(http.StatusBadRequest, "Request_BadRequest", "Property '"+required+"' is required when creating the object.")
		}
	}
	u := &User{}
	if a, failed := g.apply(u, props, true); failed {
		return a
	}
	if u.MailNickname == "" || len(u.MailNickname) > 64 || u.DisplayName == "" {
		return graphError(http.StatusBadRequest, "Request_BadRequest", "Invalid value specified for property 'mailNickname' or 'displayName'.")
	}
	if a, bad := g.checkUPN(u.UserPrincipalName, ""); bad {
		return a
	}
	u.ID = uuid.NewString()
	g.users = append(g.users, u)
	return answer{status: http.StatusCreated, body: g.selectUser(u, "")}
}

// upnLocal is the alphabet before the @ (research §3.1).
var upnLocal = regexp.MustCompile(`^[A-Za-z0-9'.\-_!#^~]+$`)

// checkUPN refuses a UPN outside the alphabet, at an unverified domain, or
// held by another user — a deleted one included.
func (g *FakeGraph) checkUPN(upn, self string) (answer, bool) {
	local, domain, ok := strings.Cut(upn, "@")
	verified := false
	for _, d := range g.Domains {
		verified = verified || (d.IsVerified && strings.EqualFold(d.Name, domain))
	}
	if !ok || !upnLocal.MatchString(local) || (len(g.Domains) > 0 && !verified) {
		return graphError(http.StatusBadRequest, "Request_BadRequest", "Property userPrincipalName is invalid."), true
	}
	for _, other := range g.users {
		if other.ID != self && strings.EqualFold(other.UserPrincipalName, upn) {
			return answer{status: http.StatusBadRequest, body: map[string]any{"error": map[string]any{
				"code":    "Request_BadRequest",
				"message": "Another object with the same value for property userPrincipalName already exists.",
				"details": []any{map[string]any{
					"code": "ObjectConflict", "target": "userPrincipalName",
					"message": "Another object with the same value for property userPrincipalName already exists.",
				}},
			}}}, true
		}
	}
	return answer{}, false
}

// patchUser is PATCH /users/{id}: nothing on a synchronised user, no
// sensitive property on a privileged one or without the role, no hire date
// when HireDateDenied — and each refusal writes nothing at all.
func (g *FakeGraph) patchUser(u *User, body []byte) answer {
	var props map[string]json.RawMessage
	if err := json.Unmarshal(body, &props); err != nil {
		return graphError(http.StatusBadRequest, "Request_BadRequest", "Invalid JSON.")
	}
	if u.OnPremisesSyncEnabled {
		return syncedRefusal()
	}
	for key := range props {
		if sensitiveProperties[key] {
			if a, refused := g.refuseSensitive(u); refused {
				return a
			}
		}
	}
	if _, ok := props["employeeHireDate"]; ok && g.HireDateDenied {
		return graphError(http.StatusForbidden, "Authorization_RequestDenied", "Insufficient privileges to complete the operation.")
	}
	changed := copyUser(u)
	if a, failed := g.apply(&changed, props, false); failed {
		return a
	}
	if changed.UserPrincipalName != u.UserPrincipalName {
		if a, bad := g.checkUPN(changed.UserPrincipalName, u.ID); bad {
			return a
		}
	}
	keep := u.Pass
	*u = changed
	u.Pass = keep
	return answer{status: http.StatusNoContent}
}

// refuseSensitive is the 403 of a sensitive action on a privileged user, or
// by an application without the User Administrator role.
func (g *FakeGraph) refuseSensitive(u *User) (answer, bool) {
	if g.RoleMissing || u.Privileged {
		return graphError(http.StatusForbidden, "Authorization_RequestDenied", "Insufficient privileges to complete the operation."), true
	}
	return answer{}, false
}

func syncedRefusal() answer {
	return graphError(http.StatusBadRequest, "Request_BadRequest",
		"Unable to update the specified properties for on-premises mastered Directory Sync objects or objects currently undergoing migration.")
}

// apply writes props onto u, Graph's names to the fake's fields. An unknown
// or read-only property, or a value of the wrong type, is a 400 and leaves
// the caller to discard u.
func (g *FakeGraph) apply(u *User, props map[string]json.RawMessage, create bool) (answer, bool) {
	bad := func(key string) (answer, bool) {
		return graphError(http.StatusBadRequest, "Request_BadRequest", "Invalid value specified for property '"+key+"' of resource 'User'."), true
	}
	strs := map[string]*string{
		"displayName": &u.DisplayName, "givenName": &u.GivenName, "surname": &u.Surname,
		"mailNickname": &u.MailNickname, "userPrincipalName": &u.UserPrincipalName, "mail": &u.Mail,
		"employeeId": &u.EmployeeID, "employeeType": &u.EmployeeType, "jobTitle": &u.JobTitle,
		"department": &u.Department, "companyName": &u.CompanyName, "officeLocation": &u.OfficeLocation,
		"streetAddress": &u.StreetAddress, "postalCode": &u.PostalCode, "city": &u.City, "country": &u.Country,
		"mobilePhone": &u.MobilePhone, "usageLocation": &u.UsageLocation, "preferredLanguage": &u.PreferredLanguage,
	}
	for key, raw := range props {
		null := string(raw) == "null"
		switch key {
		case "accountEnabled":
			if json.Unmarshal(raw, &u.AccountEnabled) != nil || null {
				return bad(key)
			}
		case "passwordProfile":
			var p struct {
				Password                      *string `json:"password"`
				ForceChangePasswordNextSignIn bool    `json:"forceChangePasswordNextSignIn"`
			}
			if json.Unmarshal(raw, &p) != nil || (create && (p.Password == nil || *p.Password == "")) {
				return bad(key)
			}
			u.ForceChangePassword = p.ForceChangePasswordNextSignIn
		case "employeeHireDate", "employeeLeaveDateTime":
			target := &u.EmployeeHireDate
			if key == "employeeLeaveDateTime" {
				target = &u.EmployeeLeaveDateTime
			}
			if null {
				*target = nil
				continue
			}
			var v time.Time
			if json.Unmarshal(raw, &v) != nil {
				return bad(key)
			}
			v = v.UTC()
			*target = &v
		case "employeeOrgData":
			if null {
				u.EmployeeOrgData = nil
				continue
			}
			var o OrgData
			if json.Unmarshal(raw, &o) != nil {
				return bad(key)
			}
			u.EmployeeOrgData = &o
		case "businessPhones":
			var phones []string
			if json.Unmarshal(raw, &phones) != nil {
				return bad(key)
			}
			u.BusinessPhones = phones
		default:
			target, ok := strs[key]
			if !ok {
				return graphError(http.StatusBadRequest, "Request_BadRequest", "Property '"+key+"' does not exist on type 'microsoft.graph.user' or is read-only."), true
			}
			if null {
				*target = ""
				continue
			}
			if json.Unmarshal(raw, target) != nil {
				return bad(key)
			}
		}
	}
	if len(u.EmployeeID) > 16 || (u.UsageLocation != "" && len(u.UsageLocation) != 2) {
		return bad("employeeId or usageLocation")
	}
	return answer{}, false
}

// userType is an account's userType as Graph answers it: "Member" unless
// the account says otherwise.
func userType(t string) string {
	if t == "" {
		return "Member"
	}
	return t
}

// selectUser is the user as Graph answers it: the properties $select names,
// or the default set without one.
func (g *FakeGraph) selectUser(u *User, selectList string) map[string]any {
	all := map[string]any{
		"id": u.ID, "accountEnabled": u.AccountEnabled, "displayName": u.DisplayName,
		"givenName": nullable(u.GivenName), "surname": nullable(u.Surname), "mailNickname": u.MailNickname,
		"userPrincipalName": u.UserPrincipalName, "mail": nullable(u.Mail), "employeeId": nullable(u.EmployeeID),
		"employeeHireDate": instant(u.EmployeeHireDate), "employeeLeaveDateTime": instant(u.EmployeeLeaveDateTime),
		"userType":     userType(u.UserType),
		"employeeType": nullable(u.EmployeeType), "jobTitle": nullable(u.JobTitle), "department": nullable(u.Department),
		"companyName": nullable(u.CompanyName), "officeLocation": nullable(u.OfficeLocation),
		"streetAddress": nullable(u.StreetAddress), "postalCode": nullable(u.PostalCode), "city": nullable(u.City),
		"country": nullable(u.Country), "businessPhones": orEmptyStrings(u.BusinessPhones),
		"mobilePhone": nullable(u.MobilePhone), "usageLocation": nullable(u.UsageLocation),
		"preferredLanguage": nullable(u.PreferredLanguage), "onPremisesSyncEnabled": syncFlag(u.OnPremisesSyncEnabled),
		"signInSessionsValidFromDateTime": instant(u.SessionsRevokedAt),
	}
	if u.EmployeeOrgData != nil {
		all["employeeOrgData"] = map[string]any{"costCenter": nullable(u.EmployeeOrgData.CostCenter), "division": nullable(u.EmployeeOrgData.Division)}
	} else {
		all["employeeOrgData"] = nil
	}
	assigned := []any{}
	states := []any{}
	for _, l := range u.Licences {
		if l.State == "Active" {
			assigned = append(assigned, map[string]any{"skuId": l.SkuID, "disabledPlans": []string{}})
		}
		states = append(states, map[string]any{
			"skuId": l.SkuID, "assignedByGroup": nullable(l.AssignedByGroup), "state": l.State,
			"error": l.Error, "disabledPlans": []string{},
		})
	}
	all["assignedLicenses"] = assigned
	all["licenseAssignmentStates"] = states

	keys := defaultUserProperties
	if selectList != "" {
		keys = strings.Split(selectList, ",")
	}
	out := map[string]any{}
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if v, ok := all[k]; ok {
			out[k] = v
		}
	}
	return out
}

// setManager is PUT /users/{id}/manager/$ref with an @odata.id naming the
// manager.
func (g *FakeGraph) setManager(u *User, body []byte) answer {
	id, ok := refID(body)
	if !ok {
		return graphError(http.StatusBadRequest, "Request_BadRequest", "The @odata.id is missing or invalid.")
	}
	if u.OnPremisesSyncEnabled {
		return syncedRefusal()
	}
	if g.userByID(id, false) == nil {
		return notFound(id)
	}
	u.ManagerID = id
	return answer{status: http.StatusNoContent}
}

// assignLicense adds and removes direct assignments. A licence needs the
// user's usage location first; an unknown SKU refuses the whole call; a SKU
// with no seat left is recorded on licenseAssignmentStates as CountViolation
// and the rest are assigned. The answer is the user.
func (g *FakeGraph) assignLicense(u *User, body []byte) answer {
	var req struct {
		AddLicenses []struct {
			SkuID string `json:"skuId"`
		} `json:"addLicenses"`
		RemoveLicenses []string `json:"removeLicenses"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.AddLicenses == nil || req.RemoveLicenses == nil {
		return graphError(http.StatusBadRequest, "Request_BadRequest", "addLicenses and removeLicenses are both required.")
	}
	if u.OnPremisesSyncEnabled {
		return syncedRefusal()
	}
	if len(req.AddLicenses) > 0 && u.UsageLocation == "" {
		return graphError(http.StatusBadRequest, "Request_BadRequest", "License assignment cannot be done for user with invalid usage location.")
	}
	for _, add := range req.AddLicenses {
		if g.sku(add.SkuID) == nil {
			return graphError(http.StatusBadRequest, "Request_BadRequest", "License "+add.SkuID+" does not correspond to a valid company License.")
		}
	}
	for _, id := range req.RemoveLicenses {
		i := slices.IndexFunc(u.Licences, func(l LicenceState) bool { return l.SkuID == id && l.AssignedByGroup == "" })
		if i < 0 {
			return graphError(http.StatusBadRequest, "Request_BadRequest", "User does not have a corresponding license.")
		}
		if u.Licences[i].State == "Active" {
			g.sku(id).Consumed--
		}
		u.Licences = slices.Delete(u.Licences, i, i+1)
	}
	for _, add := range req.AddLicenses {
		if slices.ContainsFunc(u.Licences, func(l LicenceState) bool {
			return l.SkuID == add.SkuID && l.AssignedByGroup == "" && l.State == "Active"
		}) {
			continue
		}
		u.Licences = slices.DeleteFunc(u.Licences, func(l LicenceState) bool { return l.SkuID == add.SkuID && l.AssignedByGroup == "" })
		s := g.sku(add.SkuID)
		if s.Consumed >= s.Enabled {
			u.Licences = append(u.Licences, LicenceState{SkuID: add.SkuID, State: "Error", Error: "CountViolation"})
			continue
		}
		s.Consumed++
		u.Licences = append(u.Licences, LicenceState{SkuID: add.SkuID, State: "Active", Error: "None"})
	}
	return answer{status: http.StatusOK, body: g.selectUser(u, "id,userPrincipalName,assignedLicenses,licenseAssignmentStates")}
}

func (g *FakeGraph) sku(id string) *Sku {
	for i := range g.Skus {
		if strings.EqualFold(g.Skus[i].SkuID, id) {
			return &g.Skus[i]
		}
	}
	return nil
}

// memberOf is GET /users/{id}/memberOf: every group the user is in.
func (g *FakeGraph) memberOf(u *User) answer {
	out := []any{}
	for _, gr := range g.groups {
		if !gr.Deleted && slices.Contains(gr.Members, u.ID) {
			out = append(out, groupJSON(gr))
		}
	}
	return answer{status: http.StatusOK, body: map[string]any{"value": out}}
}

// createPass is POST /users/{id}/authentication/temporaryAccessPassMethods:
// one pass per user, a new one replacing the old, its value answered on the
// create alone.
func (g *FakeGraph) createPass(u *User, body []byte) answer {
	var req struct {
		LifetimeInMinutes *int `json:"lifetimeInMinutes"`
		IsUsableOnce      bool `json:"isUsableOnce"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return graphError(http.StatusBadRequest, "badRequest", "Invalid JSON.")
	}
	if a, refused := g.refuseSensitive(u); refused {
		return a
	}
	if g.PassPolicyDenied {
		return graphError(http.StatusBadRequest, "badRequest", "Temporary Access Pass is not enabled for the user by the authentication methods policy.")
	}
	lifetime := 60
	if req.LifetimeInMinutes != nil {
		lifetime = *req.LifetimeInMinutes
	}
	if lifetime < 10 || lifetime > 43200 {
		return graphError(http.StatusBadRequest, "badRequest", "The lifetime is outside the policy's range.")
	}
	now := g.now()
	p := &Pass{ID: uuid.NewString(), Value: "TAP" + strings.ReplaceAll(uuid.NewString(), "-", "")[:9], LifetimeInMinutes: lifetime, IsUsableOnce: req.IsUsableOnce, CreatedAt: now}
	u.Pass = p
	return answer{status: http.StatusCreated, body: map[string]any{
		"id": p.ID, "temporaryAccessPass": p.Value, "createdDateTime": now.UTC().Format(time.RFC3339),
		"startDateTime": now.UTC().Format(time.RFC3339), "lifetimeInMinutes": p.LifetimeInMinutes,
		"isUsableOnce": p.IsUsableOnce, "isUsable": true, "methodUsabilityReason": "EnabledByPolicy",
	}}
}

// --- Groups ------------------------------------------------------------------

func (g *FakeGraph) groupsRoute(method string, seg []string, query url.Values, body []byte) answer {
	if len(seg) == 0 {
		switch method {
		case http.MethodGet:
			m := mailNicknameFilter.FindStringSubmatch(query.Get("$filter"))
			if m == nil {
				return graphError(http.StatusBadRequest, "Request_UnsupportedQuery", "Unsupported or invalid query filter clause specified.")
			}
			nick := strings.ReplaceAll(m[1], "''", "'")
			out := []any{}
			for _, gr := range g.groups {
				if !gr.Deleted && strings.EqualFold(gr.MailNickname, nick) {
					out = append(out, groupJSON(gr))
				}
			}
			return answer{status: http.StatusOK, body: map[string]any{"value": out}}
		case http.MethodPost:
			return g.createGroup(body)
		}
		return methodNotAllowed()
	}
	gr := g.groupByID(seg[0], false)
	if gr == nil {
		return notFound(seg[0])
	}
	rest := seg[1:]
	switch {
	case len(rest) == 0 && method == http.MethodGet:
		return answer{status: http.StatusOK, body: groupJSON(gr)}
	case len(rest) == 0 && method == http.MethodPatch:
		return g.patchGroup(gr, body)
	case len(rest) == 0 && method == http.MethodDelete:
		gr.Deleted = true
		return answer{status: http.StatusNoContent}
	case len(rest) == 1 && rest[0] == "members" && method == http.MethodGet:
		return g.listMembers(gr, query)
	case len(rest) == 1 && rest[0] == "owners" && method == http.MethodGet:
		out := []any{}
		for _, id := range gr.Owners {
			out = append(out, map[string]any{"@odata.type": g.directoryType(id), "id": id})
		}
		return answer{status: http.StatusOK, body: map[string]any{"value": out}}
	case len(rest) == 2 && rest[0] == "owners" && rest[1] == "$ref" && method == http.MethodPost:
		id, ok := refID(body)
		if !ok {
			return graphError(http.StatusBadRequest, "Request_BadRequest", "The @odata.id is missing or invalid.")
		}
		if !g.ownerExists(id) {
			return notFound(id)
		}
		if slices.ContainsFunc(gr.Owners, func(o string) bool { return strings.EqualFold(o, id) }) {
			return graphError(http.StatusBadRequest, "Request_BadRequest", "One or more added object references already exist for the following modified properties: 'owners'.")
		}
		gr.Owners = append(gr.Owners, id)
		return answer{status: http.StatusNoContent}
	case len(rest) == 3 && rest[0] == "owners" && rest[2] == "$ref" && method == http.MethodDelete:
		i := slices.IndexFunc(gr.Owners, func(o string) bool { return strings.EqualFold(o, rest[1]) })
		if i < 0 {
			return notFound(rest[1])
		}
		gr.Owners = slices.Delete(gr.Owners, i, i+1)
		return answer{status: http.StatusNoContent}
	case len(rest) == 2 && rest[0] == "members" && rest[1] == "$ref" && method == http.MethodPost:
		id, ok := refID(body)
		if !ok {
			return graphError(http.StatusBadRequest, "Request_BadRequest", "The @odata.id is missing or invalid.")
		}
		return g.addMembers(gr, []string{id})
	case len(rest) == 3 && rest[0] == "members" && rest[2] == "$ref" && method == http.MethodDelete:
		i := slices.IndexFunc(gr.Members, func(m string) bool { return strings.EqualFold(m, rest[1]) })
		if i < 0 {
			gr.StaleMembers = slices.DeleteFunc(gr.StaleMembers, func(m string) bool { return strings.EqualFold(m, rest[1]) })
			return notFound(rest[1])
		}
		if gr.MembershipRule != "" {
			return graphError(http.StatusBadRequest, "Request_BadRequest", "Cannot update the membership of a dynamic group.")
		}
		gr.Members = slices.Delete(gr.Members, i, i+1)
		return answer{status: http.StatusNoContent}
	case len(rest) == 2 && rest[0] == "members" && method == http.MethodDelete:
		// The trap (research §3.3): without /$ref the member's object
		// itself is deleted.
		u := g.userByID(rest[1], false)
		if u == nil {
			return notFound(rest[1])
		}
		u.Deleted = true
		gr.Members = slices.DeleteFunc(gr.Members, func(id string) bool { return id == u.ID })
		return answer{status: http.StatusNoContent}
	case len(rest) == 1 && rest[0] == "team" && method == http.MethodPut:
		var team struct {
			Template string `json:"template@odata.bind"`
		}
		switch {
		case json.Unmarshal(body, &team) != nil || team.Template != teamsTemplate:
			return graphError(http.StatusBadRequest, "BadRequest", "The template@odata.bind is missing or names no template.")
		case g.TeamNotFound > 0:
			g.TeamNotFound--
			return graphError(http.StatusNotFound, "NotFound", "Failed to execute Templates backend request CreateTeamFromGroupWithTemplateRequest.")
		case g.now().Sub(gr.CreatedAt) < g.TeamReadyAfter:
			return graphError(http.StatusNotFound, "NotFound", "Failed to execute Templates backend request CreateTeamFromGroupWithTemplateRequest.")
		case len(gr.Owners) == 0:
			return graphError(http.StatusBadRequest, "BadRequest", "The group must have at least one owner.")
		case gr.Team:
			return graphError(http.StatusConflict, "Conflict", "The group already has a team.")
		}
		gr.Team = true
		return answer{status: http.StatusCreated, body: map[string]any{"id": gr.ID}}
	}
	return graphError(http.StatusBadRequest, "BadRequest", "Unsupported request: "+method+" groups/{id}/"+strings.Join(rest, "/")+".")
}

// groupWrite is a group's create or PATCH body.
type groupWrite struct {
	DisplayName     *string  `json:"displayName"`
	MailNickname    *string  `json:"mailNickname"`
	Description     *string  `json:"description"`
	MailEnabled     *bool    `json:"mailEnabled"`
	SecurityEnabled *bool    `json:"securityEnabled"`
	GroupTypes      []string `json:"groupTypes"`
	Visibility      *string  `json:"visibility"`
	Owners          []string `json:"owners@odata.bind"`
	Members         []string `json:"members@odata.bind"`
}

// createGroup is POST /groups: the four required properties, at most twenty
// owners and members together.
func (g *FakeGraph) createGroup(body []byte) answer {
	var w groupWrite
	if err := json.Unmarshal(body, &w); err != nil || w.DisplayName == nil || w.MailNickname == nil || w.MailEnabled == nil || w.SecurityEnabled == nil {
		return graphError(http.StatusBadRequest, "Request_BadRequest", "displayName, mailNickname, mailEnabled and securityEnabled are required.")
	}
	if len(*w.MailNickname) > 64 || len(w.Owners)+len(w.Members) > 20 {
		return graphError(http.StatusBadRequest, "Request_BadRequest", "Invalid value specified for property 'mailNickname', or more than 20 relationships.")
	}
	gr := &Group{ID: uuid.NewString(), DisplayName: *w.DisplayName, MailNickname: *w.MailNickname,
		MailEnabled: *w.MailEnabled, SecurityEnabled: *w.SecurityEnabled, GroupTypes: w.GroupTypes, CreatedAt: g.now()}
	if w.Description != nil {
		gr.Description = *w.Description
	}
	if w.Visibility != nil {
		gr.Visibility = *w.Visibility
	}
	for _, ref := range w.Owners {
		id := lastSegment(ref)
		if !g.ownerExists(id) {
			return notFound(id)
		}
		gr.Owners = append(gr.Owners, id)
	}
	for _, ref := range w.Members {
		id := lastSegment(ref)
		if g.userByID(id, false) == nil {
			return notFound(id)
		}
		gr.Members = append(gr.Members, id)
	}
	g.groups = append(g.groups, gr)
	return answer{status: http.StatusCreated, body: groupJSON(gr)}
}

// patchGroup is PATCH /groups/{id}: the properties, and members@odata.bind
// with at most twenty, all or nothing.
func (g *FakeGraph) patchGroup(gr *Group, body []byte) answer {
	var w groupWrite
	if err := json.Unmarshal(body, &w); err != nil {
		return graphError(http.StatusBadRequest, "Request_BadRequest", "Invalid JSON.")
	}
	if len(w.Members) > 0 {
		ids := make([]string, len(w.Members))
		for i, ref := range w.Members {
			ids[i] = lastSegment(ref)
		}
		if a := g.addMembers(gr, ids); a.status != http.StatusNoContent {
			return a
		}
	}
	if w.DisplayName != nil {
		gr.DisplayName = *w.DisplayName
	}
	if w.Description != nil {
		gr.Description = *w.Description
	}
	if w.Visibility != nil {
		gr.Visibility = *w.Visibility
	}
	return answer{status: http.StatusNoContent}
}

// addMembers adds ids to gr all or nothing: at most twenty, every one a
// user, none a member already, and never to a dynamic group.
func (g *FakeGraph) addMembers(gr *Group, ids []string) answer {
	if len(ids) > 20 {
		return graphError(http.StatusBadRequest, "Request_BadRequest", "The request exceeds the limit of 20 references.")
	}
	if gr.MembershipRule != "" {
		return graphError(http.StatusBadRequest, "Request_BadRequest", "Cannot update the membership of a dynamic group.")
	}
	for _, id := range ids {
		if g.userByID(id, false) == nil {
			return notFound(id)
		}
		if slices.Contains(gr.Members, id) {
			return graphError(http.StatusBadRequest, "Request_BadRequest", "One or more added object references already exist for the following modified properties: 'members'.")
		}
	}
	gr.Members = append(gr.Members, ids...)
	return answer{status: http.StatusNoContent}
}

// teamsTemplate is the template a team is made from a group with.
const teamsTemplate = "https://graph.microsoft.com/v1.0/teamsTemplates('standard')"

// listMembers is GET /groups/{id}/members: $top of them a page (100 when
// absent, 999 at most), the next page named by @odata.nextLink with a
// $skiptoken. The stale members are listed after the members.
func (g *FakeGraph) listMembers(gr *Group, query url.Values) answer {
	top := 100
	if v := query.Get("$top"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 999 {
			return graphError(http.StatusBadRequest, "Request_BadRequest", "Invalid page size specified.")
		}
		top = n
	}
	skip := 0
	if v := query.Get("$skiptoken"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return graphError(http.StatusBadRequest, "Request_BadRequest", "Invalid skip token.")
		}
		skip = n
	}
	all := append(slices.Clone(gr.Members), gr.StaleMembers...)
	out := []any{}
	for i := skip; i < len(all) && i < skip+top; i++ {
		out = append(out, map[string]any{"@odata.type": g.directoryType(all[i]), "id": all[i]})
	}
	body := map[string]any{"value": out}
	if skip+top < len(all) {
		next := url.Values{"$top": {strconv.Itoa(top)}, "$skiptoken": {strconv.Itoa(skip + top)}}
		if s := query.Get("$select"); s != "" {
			next.Set("$select", s)
		}
		body["@odata.nextLink"] = "https://" + GraphHost + "/v1.0/groups/" + gr.ID + "/members?" + next.Encode()
	}
	return answer{status: http.StatusOK, body: body}
}

// directoryType is the @odata.type of the directory object id: the
// service principal, or a user.
func (g *FakeGraph) directoryType(id string) string {
	if g.ServicePrincipalID != "" && strings.EqualFold(id, g.ServicePrincipalID) {
		return "#microsoft.graph.servicePrincipal"
	}
	return "#microsoft.graph.user"
}

// ownerExists reports whether id may be an owner: the service principal, or
// a user not deleted.
func (g *FakeGraph) ownerExists(id string) bool {
	return (g.ServicePrincipalID != "" && strings.EqualFold(id, g.ServicePrincipalID)) || g.userByID(id, false) != nil
}

func groupJSON(gr *Group) map[string]any {
	return map[string]any{
		"@odata.type": "#microsoft.graph.group", "id": gr.ID, "displayName": gr.DisplayName,
		"mailNickname": gr.MailNickname, "description": nullable(gr.Description), "mailEnabled": gr.MailEnabled,
		"securityEnabled": gr.SecurityEnabled, "groupTypes": orEmptyStrings(gr.GroupTypes),
		"visibility": nullable(gr.Visibility), "membershipRule": nullable(gr.MembershipRule),
		"isAssignableToRole": gr.IsAssignableToRole, "createdDateTime": gr.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// --- $batch ------------------------------------------------------------------

// batch is POST /$batch: at most twenty requests, each answered on its own
// inside the one 200, each recorded in Calls as a Batch call.
func (g *FakeGraph) batch(body []byte) answer {
	var req struct {
		Requests []struct {
			ID      string            `json:"id"`
			Method  string            `json:"method"`
			URL     string            `json:"url"`
			Body    json.RawMessage   `json:"body"`
			Headers map[string]string `json:"headers"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(body, &req); err != nil || len(req.Requests) == 0 {
		return graphError(http.StatusBadRequest, "BadRequest", "Invalid batch payload format.")
	}
	if len(req.Requests) > 20 {
		return graphError(http.StatusBadRequest, "BadRequest", "Number of requests in the batch exceeds the limit of 20.")
	}
	responses := make([]any, 0, len(req.Requests))
	for _, r := range req.Requests {
		u, err := url.Parse(r.URL)
		var a answer
		switch {
		case err != nil || !strings.HasPrefix(u.Path, "/"):
			a = graphError(http.StatusBadRequest, "BadRequest", "Invalid request URL.")
			u = &url.URL{}
		case len(r.Body) > 0 && !strings.HasPrefix(headerOf(r.Headers, "Content-Type"), "application/json"):
			a = graphError(http.StatusBadRequest, "BadRequest", "Content-Type header must be specified for a request with a body.")
		case g.batchThrottle > 0:
			g.batchThrottle--
			a = graphError(http.StatusTooManyRequests, "ApplicationThrottled", "Application is over its MailboxConcurrency limit.")
			a.header = http.Header{"Retry-After": []string{"1"}}
			if g.batchWait > 0 {
				a.header.Set("Retry-After", strconv.Itoa(g.batchWait))
			}
		default:
			a = g.route(strings.ToUpper(r.Method), u.Path, u.Query(), "application/json", r.Body)
		}
		g.calls = append(g.calls, Call{Method: strings.ToUpper(r.Method), Host: GraphHost, Path: "/v1.0" + u.Path, Query: u.RawQuery, Status: a.status, Batch: true})
		item := map[string]any{"id": r.ID, "status": a.status}
		if a.body != nil {
			item["body"] = a.body
		}
		switch {
		case a.header != nil && g.batchWait > 0:
			// ThrottleBatchFor's answers carry the header as a number.
			item["headers"] = map[string]any{"Retry-After": g.batchWait}
		case a.header != nil:
			item["headers"] = map[string]string{"Retry-After": a.header.Get("Retry-After")}
		}
		responses = append(responses, item)
	}
	return answer{status: http.StatusOK, body: map[string]any{"responses": responses}}
}

// --- helpers -----------------------------------------------------------------

func (g *FakeGraph) userByID(id string, includeDeleted bool) *User {
	for _, u := range g.users {
		if strings.EqualFold(u.ID, id) && (includeDeleted || !u.Deleted) {
			return u
		}
	}
	return nil
}

func (g *FakeGraph) groupByID(id string, includeDeleted bool) *Group {
	for _, gr := range g.groups {
		if strings.EqualFold(gr.ID, id) && (includeDeleted || !gr.Deleted) {
			return gr
		}
	}
	return nil
}

func notFound(id string) answer {
	return graphError(http.StatusNotFound, "Request_ResourceNotFound",
		"Resource '"+id+"' does not exist or one of its queried reference-property objects are not present.")
}

func methodNotAllowed() answer {
	return graphError(http.StatusMethodNotAllowed, "Request_BadRequest", "The HTTP method is not allowed for the resource.")
}

// refID is the object id an { "@odata.id": "…/{id}" } body names.
func refID(body []byte) (string, bool) {
	var ref struct {
		ODataID string `json:"@odata.id"`
	}
	if err := json.Unmarshal(body, &ref); err != nil || ref.ODataID == "" {
		return "", false
	}
	return lastSegment(ref.ODataID), true
}

func lastSegment(ref string) string {
	return ref[strings.LastIndex(ref, "/")+1:]
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func syncFlag(synced bool) any {
	if !synced {
		return nil
	}
	return true
}

func instant(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func orEmpty(list []map[string]any) []map[string]any {
	if list == nil {
		return []map[string]any{}
	}
	return list
}

func orEmptyStrings(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

func copyUser(u *User) User {
	c := *u
	c.BusinessPhones = slices.Clone(u.BusinessPhones)
	c.Licences = slices.Clone(u.Licences)
	c.Photo = slices.Clone(u.Photo)
	if u.EmployeeOrgData != nil {
		o := *u.EmployeeOrgData
		c.EmployeeOrgData = &o
	}
	if u.Pass != nil {
		p := *u.Pass
		c.Pass = &p
	}
	return c
}

func copyGroup(gr *Group) Group {
	c := *gr
	c.GroupTypes = slices.Clone(gr.GroupTypes)
	c.Owners = slices.Clone(gr.Owners)
	c.Members = slices.Clone(gr.Members)
	c.StaleMembers = slices.Clone(gr.StaleMembers)
	return c
}

// Roles decodes a token's roles claim, for a test asserting what a token the
// fake issued carries. It checks no signature, as the module does not.
func Roles(token string) ([]string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("graphtest: not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("graphtest: payload: %w", err)
	}
	var claims struct {
		Roles []string `json:"roles"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("graphtest: claims: %w", err)
	}
	return claims.Roles, nil
}
