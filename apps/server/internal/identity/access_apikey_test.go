package identity_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// An API key in Access.Check (API access design K9-K19, K33, K34, K51).

// customersSettings is a key that cannot be delegated: the Owner tier, which
// no API key holds whatever its roles grant (K7).
var customersSettings = contracts.Permission{Key: "customers:settings", Display: "Customer settings", Description: "Change the customer settings.", Category: "Customers", Delegable: false}

func permissionRule(keys ...string) contracts.Rule {
	return contracts.Rule{Kind: contracts.RulePermission, Names: keys, API: contracts.APIRead}
}

func isUnauthenticated(err error) bool { return errors.Is(err, contracts.ErrUnauthenticated) }

func errorIsExpired(err error) bool { return errors.Is(err, contracts.ErrAPIKeyExpired) }

// keyOutcome names what Check answered a key: "ok", or the refusal K19 writes.
func keyOutcome(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, contracts.ErrAPINotAvailable):
		return "api_not_available"
	case errors.Is(err, contracts.ErrAPIKeyReadOnly):
		return "api_key_read_only"
	case errors.Is(err, contracts.ErrForbidden):
		return "forbidden"
	case errors.Is(err, contracts.ErrAPIKeyExpired):
		return "api_key_expired"
	case errors.Is(err, contracts.ErrUnauthenticated):
		return "unauthenticated"
	default:
		return "error: " + err.Error()
	}
}

// keyHarness is a harness with customers' keys in the catalog, an Owner, and
// an integration holding a role with customers:view and customers:create —
// and customers:settings and identity:manage, which no key may use.
func keyHarness(t *testing.T) (*harness, *client, uuid.UUID) {
	t.Helper()
	h := newHarness(t, withPermissions(customersView, customersCreate, customersSettings))
	owner, _ := h.bootstrapOwner(t)
	role := h.insertRole(t, "Webshop", "customers:view", "customers:create", "customers:settings", "identity:manage")
	return h, owner, createIntegration(t, owner, "Webshop sync", role).Integration.ID
}

// TestAPIKey_TheMatrix is K9's table: one rule of each kind, the marks, and
// APINever, for a full key, a narrowed one and a read-only one.
func TestAPIKey_TheMatrix(t *testing.T) {
	t.Parallel()
	h, owner, integration := keyHarness(t)
	full, fullSecret := createKey(t, owner, integration, map[string]any{"name": "Full"})
	_, narrowSecret := createKey(t, owner, integration, map[string]any{"name": "Narrow", "permissions": []string{"customers:view", "customers:settings"}})
	_, readSecret := createKey(t, owner, integration, map[string]any{"name": "Read", "readOnly": true})

	write := func(r contracts.Rule) contracts.Rule { r.API = contracts.APIAllowed; return r }
	rules := []struct {
		name                string
		rule                contracts.Rule
		full, narrow, rdonl string
	}{
		{"a permission it holds, a read", permissionRule("customers:view"), "ok", "ok", "ok"},
		{"a permission it holds, a write", write(permissionRule("customers:create")), "ok", "forbidden", "api_key_read_only"},
		{"two permissions", permissionRule("customers:view", "customers:create"), "ok", "forbidden", "ok"},
		{"a permission the roles lack", permissionRule("customers:purge"), "forbidden", "forbidden", "forbidden"},
		{"a permission no key holds", permissionRule("customers:settings"), "forbidden", "forbidden", "forbidden"},
		{"identity:manage", permissionRule("identity:manage"), "forbidden", "forbidden", "forbidden"},
		{"marked never", contracts.Rule{Kind: contracts.RulePermission, Names: []string{"customers:view"}}, "api_not_available", "api_not_available", "api_not_available"},
		{"anonymous, a read", contracts.Rule{Kind: contracts.RuleAnonymous, API: contracts.APIRead}, "ok", "ok", "ok"},
		{"anonymous, a write", contracts.Rule{Kind: contracts.RuleAnonymous, API: contracts.APIAllowed}, "ok", "ok", "api_key_read_only"},
		{"anonymous, never", contracts.Rule{Kind: contracts.RuleAnonymous}, "api_not_available", "api_not_available", "api_not_available"},
		{"a session rule", contracts.Rule{Kind: contracts.RuleSession, API: contracts.APIRead}, "forbidden", "forbidden", "forbidden"},
		{"a session rule as the router mounts it", contracts.Rule{Kind: contracts.RuleSession}, "api_not_available", "api_not_available", "api_not_available"},
		{"a policy", contracts.Rule{Kind: contracts.RulePolicy, Names: []string{"ActiveAccount"}, API: contracts.APIRead}, "forbidden", "forbidden", "forbidden"},
	}
	for _, rc := range rules {
		for i, secret := range []string{fullSecret, narrowSecret, readSecret} {
			want := []string{rc.full, rc.narrow, rc.rdonl}[i]
			p, err := h.access.Check(keyRequest(http.MethodGet, secret), rc.rule)
			if got := keyOutcome(err); got != want {
				t.Errorf("%s, key %d: %s, want %s", rc.name, i, got, want)
				continue
			}
			if err == nil && (p.UserID != integration || p.SessionID != uuid.Nil || p.MFAVerified || p.Via == nil || p.Via.Kind != contracts.ViaAPIKey || p.Via.KeyID == uuid.Nil) {
				t.Errorf("%s, key %d: principal %+v", rc.name, i, p)
			}
			var refusal *contracts.APIKeyRefusal
			if err != nil && (!errors.As(err, &refusal) || refusal.IntegrationID != integration || refusal.KeyID == uuid.Nil) {
				t.Errorf("%s, key %d: %v is no APIKeyRefusal", rc.name, i, err)
			}
		}
	}
	if p, err := h.access.Check(keyRequest(http.MethodGet, fullSecret), permissionRule("customers:view")); err != nil || p.Via.KeyID != full.ID {
		t.Errorf("the full key's principal %+v %v", p, err)
	}

	// The scope a key's document is built from (K36): its permissions less
	// APINever, cut to its narrowing.
	for i, c := range []struct {
		secret   string
		want     []string
		readOnly bool
	}{
		{fullSecret, []string{"customers:create", "customers:view"}, false},
		{narrowSecret, []string{"customers:view"}, false},
		{readSecret, []string{"customers:create", "customers:view"}, true},
	} {
		p, err := h.access.Check(keyRequest(http.MethodGet, c.secret), contracts.Rule{Kind: contracts.RuleAnonymous, API: contracts.APIRead})
		if err != nil {
			t.Fatal(err)
		}
		perms, readOnly, err := h.access.Scope(context.Background(), p)
		if err != nil || !slices.Equal(perms, c.want) || readOnly != c.readOnly {
			t.Errorf("scope %d = %v %v %v, want %v %v", i, perms, readOnly, err, c.want, c.readOnly)
		}
	}
	if _, _, err := h.access.Scope(context.Background(), contracts.Principal{UserID: integration}); err == nil {
		t.Error("the scope of a principal with no key")
	}
}

// TestAPIKey_AnEmptyNarrowingHoldsNothing: a key made with permissions: []
// is narrowed to nothing, unlike one made without permissions, and is refused
// every permission rule; its scope is empty. A narrowing entry no key may
// hold is never shown as granted, whatever the roles give.
func TestAPIKey_AnEmptyNarrowingHoldsNothing(t *testing.T) {
	t.Parallel()
	h, owner, integration := keyHarness(t)
	empty, secret := createKey(t, owner, integration, map[string]any{"name": "Nothing", "permissions": []string{}})
	if empty.Permissions == nil || len(*empty.Permissions) != 0 {
		t.Fatalf("the key's narrowing %v, want an empty one", empty.Permissions)
	}
	for _, rule := range []contracts.Rule{
		permissionRule("customers:view"),
		permissionRule("customers:create"),
		{Kind: contracts.RulePermission, Names: []string{"customers:create"}, API: contracts.APIAllowed},
	} {
		if _, err := h.access.Check(keyRequest(http.MethodGet, secret), rule); keyOutcome(err) != "forbidden" {
			t.Errorf("%v: %s, want forbidden", rule.Names, keyOutcome(err))
		}
	}
	p, err := h.access.Check(keyRequest(http.MethodGet, secret), contracts.Rule{Kind: contracts.RuleAnonymous, API: contracts.APIRead})
	if err != nil {
		t.Fatal(err)
	}
	if perms, _, err := h.access.Scope(context.Background(), p); err != nil || len(perms) != 0 {
		t.Errorf("scope %v %v, want none", perms, err)
	}

	never, _ := createKey(t, owner, integration, map[string]any{"name": "Never", "permissions": []string{"customers:view", "identity:manage"}})
	for _, m := range *never.Permissions {
		if want := m.Key == "customers:view"; m.GrantedByRoles != want || m.APINever == want {
			t.Errorf("%s: granted %t, apiNever %t", m.Key, m.GrantedByRoles, m.APINever)
		}
	}
}

// TestAPIKey_WhatIsNotAKey is K16: a malformed or unknown vtg_ key is 401
// under every rule, anonymous too; a bearer token without the prefix is no
// key; several Authorization headers are no caller.
func TestAPIKey_WhatIsNotAKey(t *testing.T) {
	t.Parallel()
	h, owner, integration := keyHarness(t)
	_, secret := createKey(t, owner, integration, nil)
	anonymous := contracts.Rule{Kind: contracts.RuleAnonymous, API: contracts.APIRead}
	minted, _ := identity.NewAPIKeyForTest()
	for name, key := range map[string]string{
		"a mistyped key": secret[:20] + map[bool]string{true: "b", false: "a"}[secret[20] == 'a'] + secret[21:],
		"a short key":    secret[:40],
		"an unknown key": minted,
	} {
		for _, rule := range []contracts.Rule{anonymous, permissionRule("customers:view")} {
			if _, err := h.access.Check(keyRequest(http.MethodGet, key), rule); keyOutcome(err) != "unauthenticated" {
				t.Errorf("%s under %v: %s", name, rule.Kind, keyOutcome(err))
			}
		}
	}
	if p, err := h.access.Check(keyRequest(http.MethodGet, "not-a-key"), anonymous); err != nil || p.UserID != uuid.Nil {
		t.Errorf("a bearer token without vtg_ under anonymous: %+v %v, want no caller", p, err)
	}
	r := keyRequest(http.MethodGet, secret)
	r.Header.Add("Authorization", "Bearer "+secret)
	if _, err := h.access.Check(r, anonymous); keyOutcome(err) != "unauthenticated" {
		t.Errorf("two Authorization headers: %s", keyOutcome(err))
	}
	// The scheme is matched without regard to case.
	r = keyRequest(http.MethodGet, "")
	r.Header.Set("Authorization", "bEaReR "+secret)
	if _, err := h.access.Check(r, permissionRule("customers:view")); err != nil {
		t.Errorf("bEaReR: %v", err)
	}
}

// TestAPIKey_TheRouterAnswersK19 holds every refusal to its status and code
// through the router's wrap and Reject, never a 500, with WWW-Authenticate
// on a 401, and the codes the router never reaches in identity's contract
// through Reject itself.
func TestAPIKey_TheRouterAnswersK19(t *testing.T) {
	t.Parallel()
	h, owner, integration := keyHarness(t)
	_, secret := createKey(t, owner, integration, map[string]any{"expiresInDays": 30})
	_, readSecret := createKey(t, owner, integration, map[string]any{"name": "Read", "readOnly": true})
	_, narrowSecret := createKey(t, owner, integration, map[string]any{"name": "Narrow", "permissions": []string{"customers:view"}})
	c := h.client(t)
	bearer := func(k string) reqOpt { return header("Authorization", "Bearer "+k) }

	// Every identity operation is never for a key: 403 api_not_available,
	// the anonymous ones too.
	if r := c.do(http.MethodGet, integrationsPath, nil, bearer(secret)); r.status != http.StatusForbidden || r.code() != "api_not_available" {
		t.Errorf("a policy operation: %d %s", r.status, r.body)
	}
	if r := c.do(http.MethodGet, "/api/v1/identity/bootstrap-status", nil, bearer(secret), skipContract("its contract declares only 200")); r.status != http.StatusForbidden || r.code() != "api_not_available" {
		t.Errorf("an anonymous operation: %d %s", r.status, r.body)
	}
	minted, _ := identity.NewAPIKeyForTest()
	for name, key := range map[string]string{"malformed": "vtg_nope", "unknown": minted} {
		r := c.do(http.MethodGet, integrationsPath, nil, bearer(key))
		if r.status != http.StatusUnauthorized || r.code() != "unauthenticated" || r.header("WWW-Authenticate") != `Bearer realm="vantigo"` {
			t.Errorf("%s: %d %s %q", name, r.status, r.body, r.header("WWW-Authenticate"))
		}
	}

	// Each code as Reject writes it.
	reject := func(key string, rule contracts.Rule) *httptest.ResponseRecorder {
		req := keyRequest(http.MethodPost, key)
		_, err := h.access.Check(req, rule)
		if err == nil {
			t.Fatalf("Check admitted %v", rule)
		}
		w := httptest.NewRecorder()
		h.access.Reject(w, req, rule, err)
		return w
	}
	writeRule := contracts.Rule{Kind: contracts.RulePermission, Names: []string{"customers:create"}, API: contracts.APIAllowed}
	for _, c := range []struct {
		name, key string
		rule      contracts.Rule
		status    int
		code      string
	}{
		{"read-only", readSecret, writeRule, http.StatusForbidden, "api_key_read_only"},
		{"outside the narrowing", narrowSecret, writeRule, http.StatusForbidden, "forbidden"},
		{"never", secret, contracts.Rule{Kind: contracts.RulePermission, Names: []string{"customers:view"}}, http.StatusForbidden, "api_not_available"},
	} {
		w := reject(c.key, c.rule)
		if w.Code != c.status || !strings.Contains(w.Body.String(), `"code":"`+c.code+`"`) || w.Header().Get("WWW-Authenticate") != "" {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body)
		}
	}

	h.advance(31 * 24 * time.Hour)
	r := c.do(http.MethodGet, integrationsPath, nil, bearer(secret))
	if r.status != http.StatusUnauthorized || r.code() != "api_key_expired" || r.header("WWW-Authenticate") != `Bearer realm="vantigo"` {
		t.Errorf("an expired key: %d %s", r.status, r.body)
	}
}

// TestAPIKey_TakesPrecedenceOverTheCookieAndLeavesIt is K16 and K18: a
// request with a key is the key's, whatever session the browser holds, and a
// refused key never signs the browser out.
func TestAPIKey_TakesPrecedenceOverTheCookieAndLeavesIt(t *testing.T) {
	t.Parallel()
	_, owner, integration := keyHarness(t)
	_, secret := createKey(t, owner, integration, nil)

	if r := owner.do(http.MethodGet, integrationsPath, nil, header("Authorization", "Bearer "+secret)); r.status != http.StatusForbidden || r.code() != "api_not_available" {
		t.Errorf("the Owner's browser with a key: %d %s, want the key's answer", r.status, r.body)
	}
	r := owner.do(http.MethodGet, integrationsPath, nil, header("Authorization", "Bearer vtg_typo"))
	if r.status != http.StatusUnauthorized || r.setCookie(identity.SessionCookieName) != nil {
		t.Errorf("a bad key in the Owner's browser: %d, Set-Cookie %v", r.status, r.setCookie(identity.SessionCookieName))
	}
	if r := owner.do(http.MethodGet, integrationsPath, nil); r.status != http.StatusOK {
		t.Errorf("the Owner after a bad key: %d, want still signed in", r.status)
	}
}

// TestAPIKey_NetworksBehindATrustedProxy is K14: the address is the client's
// as the trusted proxy forwards it; outside the networks the key is 401 and
// its use is not recorded.
func TestAPIKey_NetworksBehindATrustedProxy(t *testing.T) {
	t.Parallel()
	h, owner, integration := keyHarness(t)
	inside, outside := h.client(t), h.client(t)
	key, secret := createKey(t, owner, integration, map[string]any{"allowedNetworks": []string{inside.ip + "/32"}})
	bearer := header("Authorization", "Bearer "+secret)

	if r := outside.do(http.MethodGet, integrationsPath, nil, bearer); r.status != http.StatusUnauthorized || r.code() != "unauthenticated" {
		t.Errorf("outside: %d %s", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.api_keys WHERE id = $1 AND last_used_at IS NULL`, key.ID); n != 1 {
		t.Error("an outside request recorded the key's use")
	}
	if r := inside.do(http.MethodGet, integrationsPath, nil, bearer); r.status != http.StatusForbidden || r.code() != "api_not_available" {
		t.Errorf("inside: %d %s, want past the network to the mark", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.api_keys WHERE id = $1 AND last_used_at IS NULL`, key.ID); n != 1 {
		t.Error("a refused request recorded the key's use")
	}
	// An admitted call from the same address is the use, recorded with it.
	req := keyRequest(http.MethodGet, secret)
	req.RemoteAddr = inside.ip + ":1234"
	if _, err := h.access.Check(req, permissionRule("customers:view")); err != nil {
		t.Fatalf("an admitted call from inside: %v", err)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.api_keys WHERE id = $1 AND last_used_ip = $2`, key.ID, inside.ip); n != 1 {
		t.Error("the admitted request's use is not recorded with its address")
	}
}

// TestAPIKey_TheNetworkIsJudgedBeforeTheKeysState: a caller outside the
// networks is 401 unauthenticated, reason network, even for a key that has
// expired, so it learns nothing of the key.
func TestAPIKey_TheNetworkIsJudgedBeforeTheKeysState(t *testing.T) {
	t.Parallel()
	h, owner, integration := keyHarness(t)
	inside, outside := h.client(t), h.client(t)
	_, secret := createKey(t, owner, integration, map[string]any{"expiresInDays": 30, "allowedNetworks": []string{inside.ip + "/32"}})
	bearer := header("Authorization", "Bearer "+secret)
	h.advance(31 * 24 * time.Hour)

	r := outside.do(http.MethodGet, integrationsPath, nil, bearer)
	if r.status != http.StatusUnauthorized || r.code() != "unauthenticated" {
		t.Errorf("an expired key from outside: %d %s, want unauthenticated", r.status, r.body)
	}
	req := keyRequest(http.MethodGet, secret)
	_, err := h.access.Check(req, permissionRule("customers:view"))
	var refusal *contracts.APIKeyRefusal
	if !errors.As(err, &refusal) || refusal.Reason != "network" || !isUnauthenticated(err) {
		t.Errorf("an expired key from outside: %v, want the network's refusal", err)
	}
	if r := inside.do(http.MethodGet, integrationsPath, nil, bearer); r.status != http.StatusUnauthorized || r.code() != "api_key_expired" {
		t.Errorf("an expired key from inside: %d %s, want api_key_expired", r.status, r.body)
	}
}

// TestAPIKey_TheWorkerAuditsAnExpiryOnceAndDeletesTheRowLater is K12 and K15,
// with the worker as module.Workers builds it.
func TestAPIKey_TheWorkerAuditsAnExpiryOnceAndDeletesTheRowLater(t *testing.T) {
	t.Parallel()
	h, owner, integration := keyHarness(t)
	expiring, _ := createKey(t, owner, integration, map[string]any{"expiresInDays": 30})
	revoked, _ := createKey(t, owner, integration, map[string]any{"name": "Revoked", "expiresInDays": 30})
	if r := owner.do(http.MethodPost, integrationsPath+"/"+integration.String()+"/keys/"+revoked.ID.String()+"/revoke", nil); r.status != http.StatusOK {
		t.Fatalf("revoke: %d", r.status)
	}
	createKey(t, owner, integration, map[string]any{"name": "Long", "expiresInDays": 365})

	var w interface {
		Name() string
		RunCycle(context.Context) (int, int, error)
	}
	for _, candidate := range module.Workers(h.deps, h.mods...) {
		if c, ok := candidate.(interface {
			Name() string
			RunCycle(context.Context) (int, int, error)
		}); ok && c.Name() == "identity.api-keys" {
			w = c
		}
	}
	if w == nil {
		t.Fatal("no identity.api-keys worker")
	}
	ctx := context.Background()
	if rec, del, err := w.RunCycle(ctx); err != nil || rec != 0 || del != 0 {
		t.Errorf("before any expiry: %d %d %v", rec, del, err)
	}
	h.advance(31 * 24 * time.Hour)
	if rec, del, err := w.RunCycle(ctx); err != nil || rec != 1 || del != 0 {
		t.Errorf("after the expiry: %d %d %v, want one audited", rec, del, err)
	}
	if rec, _, err := w.RunCycle(ctx); err != nil || rec != 0 {
		t.Errorf("again: %d %v, want none", rec, err)
	}
	events := h.auditEvents(t, "api_key.expired")
	if len(events) != 1 || events[0].Actor != nil || !strings.Contains(events[0].Details, "system:api-keys") ||
		!strings.Contains(events[0].After, expiring.ID.String()) || *events[0].TargetUser != integration {
		t.Errorf("audit %+v", events)
	}
	h.advance(90 * 24 * time.Hour)
	if _, del, err := w.RunCycle(ctx); err != nil || del != 2 {
		t.Errorf("90 days on: %d deleted %v, want the expired and the revoked", del, err)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.api_keys WHERE integration_id = $1`, integration); n != 1 {
		t.Errorf("%d keys left, want the long one", n)
	}
	if len(h.auditEvents(t, "api_key.expired")) != 1 || len(h.auditEvents(t, "api_key.created")) != 3 {
		t.Error("the audit rows outlive the keys")
	}
}

// TestAPIKey_NoSecretInTheLog is K11: making, rolling and using a key leaves
// no vtg_ in any log line.
func TestAPIKey_NoSecretInTheLog(t *testing.T) {
	t.Parallel()
	h, owner, integration := keyHarness(t)
	key, secret := createKey(t, owner, integration, nil)
	r := owner.do(http.MethodPost, integrationsPath+"/"+integration.String()+"/keys/"+key.ID.String()+"/roll", nil)
	if r.status != http.StatusCreated {
		t.Fatalf("roll: %d", r.status)
	}
	c := h.client(t)
	c.do(http.MethodGet, integrationsPath, nil, header("Authorization", "Bearer "+secret))
	c.do(http.MethodGet, integrationsPath, nil, header("Authorization", "Bearer vtg_"+strings.Repeat("x", 49)))
	if log := string(h.log.Bytes()); strings.Contains(log, "vtg_") || strings.Contains(log, secret[4:]) {
		t.Errorf("the log carries a key:\n%s", log)
	}
}
