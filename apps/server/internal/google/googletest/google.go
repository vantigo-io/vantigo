// Package googletest is a fake Google for the google module's tests (Google
// Workspace design G16): an http.RoundTripper standing in for
// oauth2.googleapis.com, admin.googleapis.com, for the person's own
// connection (authorize.go) accounts.google.com, for the calendar sync
// (calendar.go) Calendar v3 on www.googleapis.com, and for the mail
// channels (gmail.go) gmail.googleapis.com, handed to the module as
// Deps.HTTPTransport (modtest.WithTransport). Its token endpoint verifies
// the JWT bearer assertion as Google does — the RS256 signature against the
// public half of a key the test registered, the issuer, the audience, the
// expiry, the subject and the scope — and issues a token only for a scope
// the test delegated. Directory answers customers.get(my_customer) and
// domains.list to a token carrying the scope each needs. Each later
// delivery adds the APIs it calls.
//
// Only _test.go files may import it (depguard's test-fakes rule).
package googletest

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// The hosts the fake answers.
const (
	TokenHost     = "oauth2.googleapis.com"
	AdminHost     = "admin.googleapis.com"
	LicensingHost = "licensing.googleapis.com"
	APIsHost      = "www.googleapis.com"
)

// TokenURL is the token endpoint, and the audience an assertion must name.
const TokenURL = "https://" + TokenHost + "/token"

// The scopes the fake checks.
const (
	ScopeCustomer       = "https://www.googleapis.com/auth/admin.directory.customer.readonly"
	ScopeDomains        = "https://www.googleapis.com/auth/admin.directory.domain.readonly"
	ScopeUsers          = "https://www.googleapis.com/auth/admin.directory.user"
	ScopeGroups         = "https://www.googleapis.com/auth/admin.directory.group"
	ScopeGroupsSettings = "https://www.googleapis.com/auth/apps.groups.settings"
	ScopeLicensing      = "https://www.googleapis.com/auth/apps.licensing"
)

// ServiceAccount is a service account and one key of it: what a test
// uploads (KeyFile) and what the fake verifies the assertion with.
type ServiceAccount struct {
	Email     string
	ClientID  string
	ProjectID string
	KeyID     string
	Key       *rsa.PrivateKey
}

// NewServiceAccount is a service account of project with a fresh 2048-bit
// key; email is its client_email and clientID its numeric client id.
func NewServiceAccount(email, clientID, project string) ServiceAccount {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(fmt.Sprintf("googletest: generate a key: %v", err))
	}
	id := make([]byte, 20)
	_, _ = rand.Read(id)
	return ServiceAccount{Email: email, ClientID: clientID, ProjectID: project, KeyID: hex.EncodeToString(id), Key: key}
}

// Rotated is the same service account with a new key.
func (a ServiceAccount) Rotated() ServiceAccount {
	return NewServiceAccount(a.Email, a.ClientID, a.ProjectID)
}

// KeyFile is the account's JSON key file as the Cloud console downloads it.
func (a ServiceAccount) KeyFile() string {
	der, err := x509.MarshalPKCS8PrivateKey(a.Key)
	if err != nil {
		panic(fmt.Sprintf("googletest: marshal a key: %v", err))
	}
	file := map[string]string{
		"type":                        "service_account",
		"project_id":                  a.ProjectID,
		"private_key_id":              a.KeyID,
		"private_key":                 string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email":                a.Email,
		"client_id":                   a.ClientID,
		"auth_uri":                    "https://accounts.google.com/o/oauth2/auth",
		"token_uri":                   TokenURL,
		"auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
		"client_x509_cert_url":        "https://www.googleapis.com/robot/v1/metadata/x509/" + url.PathEscape(a.Email),
		"universe_domain":             "googleapis.com",
	}
	b, _ := json.MarshalIndent(file, "", "  ")
	return string(b)
}

// Domain is one of the organisation's domains.
type Domain struct {
	Name      string
	Verified  bool
	IsPrimary bool
}

// Call is one request the fake answered: the host, the method, the path
// and the query, the body it carried, and for a token request the subject
// and the scope it asked for.
type Call struct {
	Host    string
	Method  string
	Path    string
	Query   string
	Body    string
	Status  int
	Subject string
	Scope   string
}

// FakeGoogle is the fake. Set its fields before the first request, or
// through Configure while requests may be in flight.
type FakeGoogle struct {
	// Accounts are the service accounts the token endpoint knows, by
	// client_email; each key the test added (AddKey), by key id. A deleted
	// key is one removed from here.
	Accounts map[string]map[string]*rsa.PublicKey
	// Delegated are the scopes delegated to a client id in the Admin
	// console's domain-wide delegation; a token asked for another is
	// refused unauthorized_client (U14).
	Delegated map[string][]string
	// ClientIDs are each service account's numeric client id, by
	// client_email.
	ClientIDs map[string]string
	// Admins are the admins the organisation has, lower-case: a subject
	// neither among them nor a Directory user who is not suspended is
	// refused invalid_grant, "Invalid email or User ID", as Google does.
	Admins []string
	// AdminRefused is the admin to impersonate not allowed the Directory's
	// reads: each answers 403.
	AdminRefused bool
	// CustomerID, Organisation and Domains are the organisation.
	CustomerID   string
	Organisation string
	Domains      []Domain
	// TokenLifetime is a token's expires_in; 3599 seconds when zero.
	TokenLifetime time.Duration
	// Words is Google's error text, put in every refusal so a test can prove
	// it is never logged, stored or answered.
	Words string
	// Now is the fake's clock for the tokens it issues; time.Now when nil.
	// An assertion's iat and exp are checked against the wall clock, which
	// golang.org/x/oauth2 signs them with.
	Now func() time.Time
	// OnCall, when set, runs after each request is answered, outside the
	// fake's lock: a test's way into the middle of a flow.
	OnCall func(Call)

	// WebClients are the Internal web clients the token endpoint knows: id to
	// secret (authorize.go, AddWebClient).
	WebClients map[string]string

	// The Directory's users, groups and licences (directory.go).
	directory
	// The authorization server's memory (authorize.go).
	auth authState
	// The people's calendars (calendar.go).
	cal calendarState
	// The mailboxes (gmail.go).
	gmail gmailState

	mu       sync.Mutex
	tokens   map[string]issued
	failNext []int
	throttle int
	calls    []Call
	failOn   map[string]int
	// failReason is the reason RefuseOn gave a failOn entry; "forbidden"
	// when none.
	failReason map[string]string
}

// issued is a token the fake issued: for whom, which scopes, until when.
type issued struct {
	subject string
	scopes  []string
	until   time.Time
}

// New is a fake with nothing configured.
func New() *FakeGoogle {
	return &FakeGoogle{
		Accounts:  map[string]map[string]*rsa.PublicKey{},
		Delegated: map[string][]string{},
		ClientIDs: map[string]string{},
		Words:     "Google's own words about the refusal, never to be repeated",
	}
}

// Configure runs fn under the fake's lock.
func (g *FakeGoogle) Configure(fn func(g *FakeGoogle)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	fn(g)
}

// AddKey makes the token endpoint know a's key.
func (g *FakeGoogle) AddKey(a ServiceAccount) {
	g.Configure(func(g *FakeGoogle) {
		if g.Accounts[a.Email] == nil {
			g.Accounts[a.Email] = map[string]*rsa.PublicKey{}
		}
		g.Accounts[a.Email][a.KeyID] = &a.Key.PublicKey
		g.ClientIDs[a.Email] = a.ClientID
	})
}

// DeleteKey forgets a's key, as deleting it in the Cloud console does.
func (g *FakeGoogle) DeleteKey(a ServiceAccount) {
	g.Configure(func(g *FakeGoogle) { delete(g.Accounts[a.Email], a.KeyID) })
}

// Delegate delegates scopes to clientID, replacing what was delegated.
func (g *FakeGoogle) Delegate(clientID string, scopes ...string) {
	g.Configure(func(g *FakeGoogle) { g.Delegated[clientID] = slices.Clone(scopes) })
}

// FailOn makes every request whose method and path match ("PATCH
// /admin/directory/v1/users/123" — the path's end is enough) answer status,
// until FailOn is called with status 0.
func (g *FakeGoogle) FailOn(methodPath string, status int) {
	g.Configure(func(g *FakeGoogle) {
		delete(g.failReason, methodPath)
		if g.failOn == nil {
			g.failOn = map[string]int{}
		}
		if status == 0 {
			delete(g.failOn, methodPath)
			return
		}
		g.failOn[methodPath] = status
	})
}

// RefuseOn is FailOn answering Google's reason with the status: 403
// quotaExceeded or dailyLimitExceeded as Calendar answers a quota spent, 403
// forbiddenForNonOrganizer as it refuses one event, 404 notFound. Status 0
// clears it.
func (g *FakeGoogle) RefuseOn(methodPath string, status int, reason string) {
	g.FailOn(methodPath, status)
	if status == 0 {
		return
	}
	g.Configure(func(g *FakeGoogle) {
		if g.failReason == nil {
			g.failReason = map[string]string{}
		}
		g.failReason[methodPath] = reason
	})
}

// failing is the status FailOn set for req, 0 for none.
func (g *FakeGoogle) failing(req *http.Request) int {
	status, _ := g.failure(req)
	return status
}

// failure is the status FailOn or RefuseOn set for req and its reason
// ("forbidden" unless RefuseOn named one); 0 for none.
func (g *FakeGoogle) failure(req *http.Request) (int, string) {
	for key, status := range g.failOn {
		method, path, _ := strings.Cut(key, " ")
		if req.Method == method && strings.HasSuffix(req.URL.Path, path) {
			reason := g.failReason[key]
			if reason == "" {
				reason = "forbidden"
			}
			return status, reason
		}
	}
	return 0, ""
}

// FailNext makes the next requests answer status, one each, in order.
func (g *FakeGoogle) FailNext(status ...int) {
	g.Configure(func(g *FakeGoogle) { g.failNext = append(g.failNext, status...) })
}

// Throttle makes the next n requests answer 429.
func (g *FakeGoogle) Throttle(n int) {
	g.Configure(func(g *FakeGoogle) { g.throttle = n })
}

// Calls are the requests answered so far.
func (g *FakeGoogle) Calls() []Call {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.calls)
}

// RoundTrip answers one request.
func (g *FakeGoogle) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		body = b
	}
	if req.URL.Scheme != "https" || !slices.Contains([]string{TokenHost, AdminHost, LicensingHost, APIsHost, AccountsHost, GmailHost}, req.URL.Host) {
		return nil, fmt.Errorf("googletest: no such host: %s://%s", req.URL.Scheme, req.URL.Host)
	}

	g.mu.Lock()
	call := Call{Host: req.URL.Host, Method: req.Method, Path: req.URL.Path, Query: req.URL.RawQuery}
	if req.URL.Host != TokenHost {
		call.Body = string(body)
	}
	var a answer
	switch {
	case g.failing(req) > 0:
		status, reason := g.failure(req)
		a = answer{status: status, body: g.apiError(status, reason)}
	case g.throttle > 0:
		g.throttle--
		a = answer{status: http.StatusTooManyRequests, header: http.Header{"Retry-After": {"1"}}, body: g.apiError(http.StatusTooManyRequests, "rateLimitExceeded")}
	case len(g.failNext) > 0:
		status := g.failNext[0]
		g.failNext = g.failNext[1:]
		a = answer{status: status, body: g.apiError(status, "backendError")}
	case req.URL.Host == AccountsHost:
		a = g.discovery(req)
	case req.URL.Host == APIsHost && req.Method == http.MethodGet && req.URL.Path == "/oauth2/v3/certs":
		a = g.certs()
	case req.URL.Host == TokenHost && req.Method == http.MethodPost && req.URL.Path == "/revoke":
		form, _ := url.ParseQuery(string(body))
		a = g.revoke(form)
	case req.URL.Host == TokenHost:
		a = g.token(req, body, &call)
	case req.URL.Host == APIsHost && req.Method == http.MethodPost && req.URL.Path == "/batch/calendar/v3":
		a = g.calendarBatch(req, body)
	case req.URL.Host == APIsHost && strings.HasPrefix(req.URL.Path, "/calendar/v3/"):
		a = g.calendarCall(req, body)
	case req.URL.Host == GmailHost:
		a = g.gmailCall(req, body)
	default:
		a = g.admin(req, body)
	}
	call.Status = a.status
	g.calls = append(g.calls, call)
	hook := g.OnCall
	g.mu.Unlock()
	if hook != nil {
		hook(call)
	}
	return a.response(req), nil
}

// answer is one response before it is written: body is JSON, raw the bytes
// of another type, which header names.
type answer struct {
	status int
	header http.Header
	body   any
	raw    []byte
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
		Status: fmt.Sprintf("%d %s", a.status, http.StatusText(a.status)), StatusCode: a.status,
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: h, Body: io.NopCloser(bytes.NewReader(b)), ContentLength: int64(len(b)), Request: req,
	}
}

func (g *FakeGoogle) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// oauthError is the token endpoint's refusal.
func (g *FakeGoogle) oauthError(status int, code string) answer {
	return answer{status: status, body: map[string]any{"error": code, "error_description": g.Words}}
}

// apiError is a Google API's refusal in the JSON API shape.
func (g *FakeGoogle) apiError(status int, reason string) map[string]any {
	return map[string]any{"error": map[string]any{
		"code": status, "message": g.Words,
		"errors": []map[string]any{{"message": g.Words, "domain": "global", "reason": reason}},
	}}
}

// claims are the assertion's claims the fake reads.
type claims struct {
	Iss   string `json:"iss"`
	Sub   string `json:"sub"`
	Scope string `json:"scope"`
	Aud   string `json:"aud"`
	Iat   int64  `json:"iat"`
	Exp   int64  `json:"exp"`
}

// token is the JWT bearer grant: the assertion's header, signature and
// claims verified as Google does, then the subject and the scopes.
func (g *FakeGoogle) token(req *http.Request, body []byte, call *Call) answer {
	if req.Method != http.MethodPost || req.URL.Path != "/token" {
		return answer{status: http.StatusNotFound, body: g.apiError(http.StatusNotFound, "notFound")}
	}
	form, err := url.ParseQuery(string(body))
	if err != nil {
		return g.oauthError(http.StatusBadRequest, "invalid_request")
	}
	switch form.Get("grant_type") {
	case "authorization_code":
		return g.authorizationCode(form)
	case "refresh_token":
		return g.refreshToken(form)
	case "urn:ietf:params:oauth:grant-type:jwt-bearer":
	default:
		return g.oauthError(http.StatusBadRequest, "unsupported_grant_type")
	}
	parts := strings.Split(form.Get("assertion"), ".")
	if len(parts) != 3 {
		return g.oauthError(http.StatusBadRequest, "invalid_grant")
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	var c claims
	if !decodePart(parts[0], &header) || !decodePart(parts[1], &c) || header.Alg != "RS256" {
		return g.oauthError(http.StatusBadRequest, "invalid_grant")
	}
	call.Subject, call.Scope = c.Sub, c.Scope
	keys, known := g.Accounts[c.Iss]
	if !known {
		return g.oauthError(http.StatusBadRequest, "invalid_grant")
	}
	pub, ok := keys[header.Kid]
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if !ok || err != nil {
		return g.oauthError(http.StatusBadRequest, "invalid_grant")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig) != nil {
		return g.oauthError(http.StatusBadRequest, "invalid_grant")
	}
	wall := time.Now().Unix()
	if c.Aud != TokenURL || c.Exp <= wall || c.Iat > wall+300 || c.Exp-c.Iat > 3600 {
		return g.oauthError(http.StatusBadRequest, "invalid_grant")
	}
	if c.Sub == "" || !g.knownSubject(c.Sub) {
		// Google's own description of an unknown or suspended subject,
		// which the module reads to tell it from a refused key.
		return answer{status: http.StatusBadRequest, body: map[string]any{"error": "invalid_grant", "error_description": "Invalid email or User ID"}}
	}
	scopes := strings.Fields(c.Scope)
	delegated := g.Delegated[g.ClientIDs[c.Iss]]
	if len(scopes) == 0 || slices.ContainsFunc(scopes, func(s string) bool { return !slices.Contains(delegated, s) }) {
		return g.oauthError(http.StatusUnauthorized, "unauthorized_client")
	}
	lifetime := g.TokenLifetime
	if lifetime == 0 {
		lifetime = 3599 * time.Second
	}
	value := make([]byte, 24)
	_, _ = rand.Read(value)
	token := "ya29.fake-" + base64.RawURLEncoding.EncodeToString(value)
	if g.tokens == nil {
		g.tokens = map[string]issued{}
	}
	g.tokens[token] = issued{subject: strings.ToLower(c.Sub), scopes: scopes, until: g.now().Add(lifetime)}
	return answer{status: http.StatusOK, body: map[string]any{
		"access_token": token, "expires_in": int(lifetime / time.Second), "token_type": "Bearer",
	}}
}

// decodePart decodes one base64url JWT part as JSON into into.
func decodePart(part string, into any) bool {
	b, err := base64.RawURLEncoding.DecodeString(part)
	return err == nil && json.Unmarshal(b, into) == nil
}

// bearer is the request's token, while it is good.
func (g *FakeGoogle) bearer(req *http.Request) (issued, bool) {
	value, ok := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return issued{}, false
	}
	tok, ok := g.tokens[value]
	if !ok || !g.now().Before(tok.until) {
		return issued{}, false
	}
	return tok, true
}

// admin answers one API call: the token checked, then its scope.
func (g *FakeGoogle) admin(req *http.Request, body []byte) answer {
	tok, ok := g.bearer(req)
	if !ok {
		return answer{status: http.StatusUnauthorized, body: g.apiError(http.StatusUnauthorized, "authError")}
	}
	need := ""
	var route func() answer
	switch {
	case req.URL.Host == AdminHost && req.Method == http.MethodGet && req.URL.Path == "/admin/directory/v1/customers/my_customer":
		need, route = ScopeCustomer, g.customer
	case req.URL.Host == AdminHost && req.Method == http.MethodGet && req.URL.Path == "/admin/directory/v1/customer/my_customer/domains":
		need, route = ScopeDomains, g.domains
	default:
		need, route = g.directoryRoute(req, body)
		if route == nil {
			return answer{status: http.StatusNotFound, body: g.apiError(http.StatusNotFound, "notFound")}
		}
	}
	if !slices.Contains(tok.scopes, need) {
		return answer{status: http.StatusForbidden, body: g.apiError(http.StatusForbidden, "insufficientPermissions")}
	}
	if g.AdminRefused {
		return answer{status: http.StatusForbidden, body: g.apiError(http.StatusForbidden, "forbidden")}
	}
	return route()
}

// primary is the primary domain's name, "" without one.
func (g *FakeGoogle) primary() string {
	for _, d := range g.Domains {
		if d.IsPrimary {
			return d.Name
		}
	}
	return ""
}

func (g *FakeGoogle) customer() answer {
	return answer{status: http.StatusOK, body: map[string]any{
		"kind": "admin#directory#customer", "id": g.CustomerID, "customerDomain": g.primary(),
		"postalAddress": map[string]any{"organizationName": g.Organisation, "countryCode": "NO"},
	}}
}

func (g *FakeGoogle) domains() answer {
	out := []map[string]any{}
	for _, d := range g.Domains {
		out = append(out, map[string]any{"domainName": d.Name, "verified": d.Verified, "isPrimary": d.IsPrimary})
	}
	return answer{status: http.StatusOK, body: map[string]any{"kind": "admin#directory#domains", "domains": out}}
}
