package graphtest_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
)

const (
	tenant   = "6d5f3f0e-1d2c-4b3a-9f8e-7a6b5c4d3e2f"
	clientID = "0f1e2d3c-4b5a-4968-8776-655443322110"
	secret   = "the-secret"
	skuE3    = "6fd2c87f-b296-42f0-b197-1e91e994b900"
	uuidLike = "11111111-2222-4333-8444-555555555555"
)

// tenantFake is a tenant with one client, two domains (one verified) and an
// E3 SKU with one seat left, on a clock the test moves.
func tenantFake(now *time.Time) *graphtest.FakeGraph {
	g := graphtest.New()
	g.TenantID = tenant
	g.Secrets[clientID] = []string{secret}
	g.Roles = []string{"User.ReadWrite.All", "Organization.Read.All"}
	g.Organisation = "Nordlys AS"
	g.Domains = []graphtest.Domain{{Name: "nordlys.no", IsVerified: true, IsDefault: true}, {Name: "nordlys.example", IsVerified: false}}
	g.Skus = []graphtest.Sku{{SkuID: skuE3, PartNumber: "ENTERPRISEPACK", Enabled: 2, Consumed: 1}}
	g.Now = func() time.Time { return *now }
	return g
}

// client is a caller of the fake with a token, the way the module's client
// will be.
type client struct {
	t     *testing.T
	http  *http.Client
	token string
}

func newClient(t *testing.T, g *graphtest.FakeGraph) *client {
	t.Helper()
	c := &client{t: t, http: &http.Client{Transport: g}}
	status, body := c.tokenRequest(tenant, clientID, secret)
	if status != http.StatusOK {
		t.Fatalf("token: status %d body %s", status, body)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		t.Fatal(err)
	}
	c.token = tok.AccessToken
	return c
}

func (c *client) tokenRequest(tenantID, id, secretValue string) (int, []byte) {
	c.t.Helper()
	form := url.Values{
		"grant_type": {"client_credentials"}, "client_id": {id}, "client_secret": {secretValue},
		"scope": {"https://graph.microsoft.com/.default"},
	}
	resp, err := c.http.Post("https://login.microsoftonline.com/"+tenantID+"/oauth2/v2.0/token",
		"application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// do calls Graph and answers the status, the header and the body decoded
// into a map (nil without one).
func (c *client) do(method, path string, body any) (int, http.Header, map[string]any) {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "https://graph.microsoft.com/v1.0"+path, reader)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			c.t.Fatalf("%s %s: decode %s: %v", method, path, raw, err)
		}
	}
	return resp.StatusCode, resp.Header, out
}

// errorCode is Graph's error.code of a body.
func errorCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func newUser(upn, number string) map[string]any {
	return map[string]any{
		"accountEnabled": false, "displayName": "Kari Nordmann", "givenName": "Kari", "surname": "Nordmann",
		"mailNickname": strings.Split(upn, "@")[0], "userPrincipalName": upn, "employeeId": number,
		"passwordProfile": map[string]any{"password": "Aa1!-not-kept", "forceChangePasswordNextSignIn": true},
	}
}

func TestToken(t *testing.T) {
	now := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	g := tenantFake(&now)
	c := newClient(t, g)

	roles, err := graphtest.Roles(c.token)
	if err != nil || !slices.Equal(roles, []string{"User.ReadWrite.All", "Organization.Read.All"}) {
		t.Errorf("roles = %v, %v; want the configured roles", roles, err)
	}
	for _, tc := range []struct {
		name, tenant, client, secret string
		status                       int
		code                         string
	}{
		{"a wrong secret", tenant, clientID, "guess", http.StatusUnauthorized, "invalid_client"},
		{"an unknown client", tenant, "11111111-1111-4111-8111-111111111111", secret, http.StatusBadRequest, "unauthorized_client"},
		{"another tenant", "22222222-2222-4222-8222-222222222222", clientID, secret, http.StatusBadRequest, "invalid_request"},
	} {
		status, body := c.tokenRequest(tc.tenant, tc.client, tc.secret)
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		if status != tc.status || e.Error != tc.code {
			t.Errorf("%s: %d %s, want %d %s", tc.name, status, body, tc.status, tc.code)
		}
	}

	if status, _, _ := c.do(http.MethodGet, "/organization", nil); status != http.StatusOK {
		t.Errorf("organization with the token: %d, want 200", status)
	}
	now = now.Add(time.Hour)
	if status, _, body := c.do(http.MethodGet, "/organization", nil); status != http.StatusUnauthorized || errorCode(body) != "InvalidAuthenticationToken" {
		t.Errorf("organization after the token's hour: %d %v, want 401 InvalidAuthenticationToken", status, body)
	}

	// No roles configured is a token without the claim: consent not granted.
	g.Configure(func(g *graphtest.FakeGraph) { g.Roles = nil })
	c = newClient(t, g)
	if roles, _ := graphtest.Roles(c.token); roles != nil {
		t.Errorf("roles without consent = %v, want none", roles)
	}
	// Never the secret in the log.
	for _, call := range g.Calls() {
		if strings.Contains(call.Path+call.Query, secret) {
			t.Errorf("a call logged the secret: %+v", call)
		}
	}
}

func TestOrganisationAndDomains(t *testing.T) {
	now := time.Now()
	c := newClient(t, tenantFake(&now))
	_, _, org := c.do(http.MethodGet, "/organization", nil)
	value := org["value"].([]any)[0].(map[string]any)
	domains := value["verifiedDomains"].([]any)
	if value["displayName"] != "Nordlys AS" || len(domains) != 1 || domains[0].(map[string]any)["name"] != "nordlys.no" {
		t.Errorf("organization = %v, want Nordlys AS with nordlys.no alone verified", value)
	}
	_, _, list := c.do(http.MethodGet, "/domains", nil)
	if len(list["value"].([]any)) != 2 {
		t.Errorf("domains = %v, want both", list)
	}
}

func TestUsers(t *testing.T) {
	now := time.Now()
	g := tenantFake(&now)
	c := newClient(t, g)

	status, _, created := c.do(http.MethodPost, "/users", newUser("kari.nordmann@nordlys.no", "E-1"))
	if status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, created)
	}
	id := created["id"].(string)
	stored, _ := g.User(id)
	if stored.AccountEnabled || !stored.ForceChangePassword || stored.EmployeeID != "E-1" {
		t.Errorf("stored = %+v, want disabled, the password change forced, E-1", stored)
	}

	// Graph's required properties.
	missing := newUser("ola@nordlys.no", "E-2")
	delete(missing, "passwordProfile")
	if status, _, body := c.do(http.MethodPost, "/users", missing); status != http.StatusBadRequest || errorCode(body) != "Request_BadRequest" {
		t.Errorf("create without a password: %d %v, want 400", status, body)
	}
	// A UPN outside the alphabet, or at an unverified domain.
	for _, upn := range []string{"ørjan@nordlys.no", "ola@nordlys.example"} {
		if status, _, _ := c.do(http.MethodPost, "/users", newUser(upn, "E-3")); status != http.StatusBadRequest {
			t.Errorf("create %s: %d, want 400", upn, status)
		}
	}

	// The conflict, in another case.
	status, _, body := c.do(http.MethodPost, "/users", newUser("KARI.NORDMANN@nordlys.no", "E-4"))
	details, _ := body["error"].(map[string]any)["details"].([]any)
	if status != http.StatusBadRequest || errorCode(body) != "Request_BadRequest" || len(details) != 1 ||
		details[0].(map[string]any)["code"] != "ObjectConflict" {
		t.Errorf("a taken UPN: %d %v, want 400 Request_BadRequest with an ObjectConflict detail", status, body)
	}

	// The filter, '' escaping a quote.
	g.AddUser(graphtest.User{UserPrincipalName: "o.brien@nordlys.no", EmployeeID: "O'1", DisplayName: "Ola O'Brien"})
	_, _, found := c.do(http.MethodGet, "/users?"+url.Values{"$filter": {"employeeId eq 'O''1'"}, "$select": {"id,employeeId"}}.Encode(), nil)
	rows := found["value"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["employeeId"] != "O'1" || len(rows[0].(map[string]any)) != 2 {
		t.Errorf("filter on O'1 = %v, want the one user with the two selected properties", found)
	}
	if status, _, body := c.do(http.MethodGet, "/users?"+url.Values{"$filter": {"startswith(displayName,'K')"}}.Encode(), nil); status != http.StatusBadRequest || errorCode(body) != "Request_UnsupportedQuery" {
		t.Errorf("another filter: %d %v, want 400 Request_UnsupportedQuery", status, body)
	}

	// $select, and Graph's default set without it.
	_, _, plain := c.do(http.MethodGet, "/users/"+id, nil)
	if _, has := plain["employeeId"]; has || plain["userPrincipalName"] != "kari.nordmann@nordlys.no" {
		t.Errorf("GET without $select = %v, want the default set, which has no employeeId", plain)
	}
	_, _, selected := c.do(http.MethodGet, "/users/"+id+"?$select=accountEnabled,employeeId", nil)
	if len(selected) != 2 || selected["employeeId"] != "E-1" {
		t.Errorf("GET with $select = %v, want exactly the two", selected)
	}

	// PATCH, the manager, the sessions.
	hire := "2026-11-02T00:00:00Z"
	if status, _, body := c.do(http.MethodPatch, "/users/"+id, map[string]any{
		"accountEnabled": true, "employeeHireDate": hire, "employeeOrgData": map[string]any{"costCenter": "100"},
	}); status != http.StatusNoContent {
		t.Fatalf("patch: %d %v", status, body)
	}
	manager := g.AddUser(graphtest.User{UserPrincipalName: "boss@nordlys.no"})
	if status, _, _ := c.do(http.MethodPut, "/users/"+id+"/manager/$ref", map[string]any{"@odata.id": "https://graph.microsoft.com/v1.0/users/" + manager.ID}); status != http.StatusNoContent {
		t.Errorf("manager: %d", status)
	}
	if status, _, _ := c.do(http.MethodPut, "/users/"+id+"/manager/$ref", map[string]any{"@odata.id": "https://graph.microsoft.com/v1.0/users/33333333-3333-4333-8333-333333333333"}); status != http.StatusNotFound {
		t.Errorf("an unknown manager: %d, want 404", status)
	}
	if status, _, _ := c.do(http.MethodPost, "/users/"+id+"/revokeSignInSessions", nil); status != http.StatusOK {
		t.Errorf("revoke: %d", status)
	}
	stored, _ = g.User(id)
	if !stored.AccountEnabled || stored.EmployeeHireDate == nil || stored.EmployeeHireDate.Format(time.RFC3339) != hire ||
		stored.EmployeeOrgData.CostCenter != "100" || stored.ManagerID != manager.ID || stored.SessionsRevokedAt == nil {
		t.Errorf("after the writes = %+v", stored)
	}

	// The delete is soft: gone from /users and from GET, the UPN still held.
	if status, _, _ := c.do(http.MethodDelete, "/users/"+id, nil); status != http.StatusNoContent {
		t.Errorf("delete: %d", status)
	}
	if status, _, _ := c.do(http.MethodGet, "/users/"+id, nil); status != http.StatusNotFound {
		t.Errorf("GET after the delete: %d, want 404", status)
	}
	if len(g.Deleted()) != 1 {
		t.Errorf("deleted = %v, want the one", g.Deleted())
	}
	if status, _, _ := c.do(http.MethodPost, "/users", newUser("kari.nordmann@nordlys.no", "E-5")); status != http.StatusBadRequest {
		t.Errorf("a deleted user's UPN: %d, want still taken", status)
	}
}

func TestSensitiveAndSynced(t *testing.T) {
	now := time.Now()
	g := tenantFake(&now)
	c := newClient(t, g)
	admin := g.AddUser(graphtest.User{UserPrincipalName: "admin@nordlys.no", Privileged: true, AccountEnabled: true})
	synced := g.AddUser(graphtest.User{UserPrincipalName: "synced@nordlys.no", OnPremisesSyncEnabled: true})
	plain := g.AddUser(graphtest.User{UserPrincipalName: "plain@nordlys.no"})

	for _, w := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPatch, "/users/" + admin.ID, map[string]any{"accountEnabled": false}},
		{http.MethodPost, "/users/" + admin.ID + "/authentication/temporaryAccessPassMethods", map[string]any{"lifetimeInMinutes": 60}},
		{http.MethodDelete, "/users/" + admin.ID, nil},
	} {
		if status, _, body := c.do(w.method, w.path, w.body); status != http.StatusForbidden || errorCode(body) != "Authorization_RequestDenied" {
			t.Errorf("%s %s on a privileged user: %d %v, want 403 Authorization_RequestDenied", w.method, w.path, status, body)
		}
	}
	// A non-sensitive property is written on a privileged user.
	if status, _, _ := c.do(http.MethodPatch, "/users/"+admin.ID, map[string]any{"jobTitle": "CEO"}); status != http.StatusNoContent {
		t.Errorf("a job title on a privileged user: %d, want 204", status)
	}
	if u, _ := g.User(admin.ID); !u.AccountEnabled {
		t.Error("the refused PATCH wrote accountEnabled")
	}

	if status, _, body := c.do(http.MethodPatch, "/users/"+synced.ID, map[string]any{"jobTitle": "Engineer"}); status != http.StatusBadRequest || errorCode(body) != "Request_BadRequest" {
		t.Errorf("a synced user: %d %v, want 400 Request_BadRequest", status, body)
	}

	// Without the role every sensitive write is refused.
	g.Configure(func(g *graphtest.FakeGraph) { g.RoleMissing = true })
	if status, _, _ := c.do(http.MethodPatch, "/users/"+plain.ID, map[string]any{"accountEnabled": true}); status != http.StatusForbidden {
		t.Errorf("accountEnabled without the role: %d, want 403", status)
	}
	g.Configure(func(g *graphtest.FakeGraph) { g.RoleMissing, g.HireDateDenied = false, true })
	if status, _, _ := c.do(http.MethodPatch, "/users/"+plain.ID, map[string]any{"accountEnabled": true, "employeeHireDate": "2026-11-02T00:00:00Z"}); status != http.StatusForbidden {
		t.Errorf("the hire date denied: %d, want 403", status)
	}
	if u, _ := g.User(plain.ID); u.AccountEnabled || u.EmployeeHireDate != nil {
		t.Errorf("the refused PATCH wrote %+v, want nothing", u)
	}
}

func TestLicences(t *testing.T) {
	now := time.Now()
	g := tenantFake(&now)
	c := newClient(t, g)
	first := g.AddUser(graphtest.User{UserPrincipalName: "first@nordlys.no"})
	second := g.AddUser(graphtest.User{UserPrincipalName: "second@nordlys.no", UsageLocation: "NO"})
	add := map[string]any{"addLicenses": []any{map[string]any{"skuId": skuE3, "disabledPlans": []any{}}}, "removeLicenses": []any{}}

	if status, _, _ := c.do(http.MethodPost, "/users/"+first.ID+"/assignLicense", add); status != http.StatusBadRequest {
		t.Errorf("a licence without a usage location: %d, want 400", status)
	}
	if status, _, _ := c.do(http.MethodPost, "/users/"+second.ID+"/assignLicense", add); status != http.StatusOK {
		t.Errorf("the last seat: %d, want 200", status)
	}
	g.EditUser(first.ID, func(u *graphtest.User) { u.UsageLocation = "NO" })
	status, _, body := c.do(http.MethodPost, "/users/"+first.ID+"/assignLicense", add)
	states := body["licenseAssignmentStates"].([]any)
	if status != http.StatusOK || len(states) != 1 || states[0].(map[string]any)["error"] != "CountViolation" || len(body["assignedLicenses"].([]any)) != 0 {
		t.Errorf("no seat left: %d %v, want 200 with a CountViolation state and nothing assigned", status, body)
	}
	_, _, skus := c.do(http.MethodGet, "/subscribedSkus", nil)
	sku := skus["value"].([]any)[0].(map[string]any)
	if sku["consumedUnits"].(float64) != 2 || sku["prepaidUnits"].(map[string]any)["enabled"].(float64) != 2 {
		t.Errorf("subscribedSkus = %v, want 2 of 2", sku)
	}
	remove := map[string]any{"addLicenses": []any{}, "removeLicenses": []any{skuE3}}
	if status, _, _ := c.do(http.MethodPost, "/users/"+second.ID+"/assignLicense", remove); status != http.StatusOK {
		t.Errorf("remove: %d", status)
	}
	if u, _ := g.User(second.ID); len(u.Licences) != 0 {
		t.Errorf("after the removal = %+v, want none", u.Licences)
	}
}

func TestPass(t *testing.T) {
	now := time.Now()
	g := tenantFake(&now)
	c := newClient(t, g)
	u := g.AddUser(graphtest.User{UserPrincipalName: "new@nordlys.no"})
	path := "/users/" + u.ID + "/authentication/temporaryAccessPassMethods"

	status, _, body := c.do(http.MethodPost, path, map[string]any{"lifetimeInMinutes": 480, "isUsableOnce": true})
	stored, _ := g.User(u.ID)
	if status != http.StatusCreated || body["temporaryAccessPass"] == "" || stored.Pass == nil || stored.Pass.Value != body["temporaryAccessPass"] {
		t.Errorf("a pass: %d %v, stored %+v", status, body, stored.Pass)
	}
	if status, _, _ := c.do(http.MethodPost, path, map[string]any{"lifetimeInMinutes": 5}); status != http.StatusBadRequest {
		t.Errorf("a pass of 5 minutes: %d, want 400", status)
	}
	g.Configure(func(g *graphtest.FakeGraph) { g.PassPolicyDenied = true })
	if status, _, _ := c.do(http.MethodPost, path, map[string]any{"lifetimeInMinutes": 60}); status != http.StatusBadRequest {
		t.Errorf("the policy off: %d, want 400", status)
	}
	for _, call := range g.Calls() {
		if strings.Contains(call.Path+call.Query, stored.Pass.Value) {
			t.Errorf("a call logged the pass: %+v", call)
		}
	}
}

func TestThrottleAndFailures(t *testing.T) {
	now := time.Now()
	g := tenantFake(&now)
	c := newClient(t, g)

	g.Throttle(2)
	for i := range 2 {
		status, header, body := c.do(http.MethodGet, "/organization", nil)
		if status != http.StatusTooManyRequests || header.Get("Retry-After") != "1" || errorCode(body) != "TooManyRequests" {
			t.Errorf("throttled call %d: %d %v %v, want 429 with Retry-After 1", i, status, header, body)
		}
	}
	if status, _, _ := c.do(http.MethodGet, "/organization", nil); status != http.StatusOK {
		t.Errorf("after the throttle: %d, want 200", status)
	}
	g.FailNext(http.StatusServiceUnavailable)
	if status, _, _ := c.do(http.MethodGet, "/organization", nil); status != http.StatusServiceUnavailable {
		t.Errorf("FailNext: %d, want 503", status)
	}
	calls := g.Calls()
	statuses := make([]int, 0, len(calls))
	for _, call := range calls {
		statuses = append(statuses, call.Status)
	}
	if !slices.Equal(statuses, []int{200, 429, 429, 200, 503}) {
		t.Errorf("calls = %v, want the token, two 429s, a 200 and the 503", statuses)
	}

	// An unknown host is a transport error.
	if _, err := (&http.Client{Transport: g}).Get("https://example.com/"); err == nil {
		t.Error("an unknown host answered")
	}
}

// The directory's queries (the plan's reading 6): $search over displayName,
// mail and userPrincipalName, a case-insensitive substring per clause,
// joined by OR; $orderby=displayName, $top and $count; an in filter on
// mail, userPrincipalName or employeeId of at most fifteen values, a quote
// doubled; and a $search or a $count refused without ConsistencyLevel:
// eventual, as Graph refuses it.
func TestDirectoryQueries(t *testing.T) {
	now := time.Now()
	g := tenantFake(&now)
	c := newClient(t, g)
	g.AddUser(graphtest.User{DisplayName: "Kari Nordmann", UserPrincipalName: "kari@nordlys.no", Mail: "Kari.N@nordlys.no", EmployeeID: "E-1"})
	g.AddUser(graphtest.User{DisplayName: "Anne Kariussen", UserPrincipalName: "anne@nordlys.no", EmployeeID: "e-2"})
	g.AddUser(graphtest.User{DisplayName: "Ola O'Brien", UserPrincipalName: "ola@nordlys.no", Mail: "O'Brien@nordlys.no"})
	g.AddUser(graphtest.User{DisplayName: "Per", UserPrincipalName: "per@nordlys.no", Mail: "per.kari@nordlys.no", Deleted: true})

	get := func(q url.Values, eventual bool) (int, map[string]any) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, "https://graph.microsoft.com/v1.0/users?"+q.Encode(), nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		if eventual {
			req.Header.Set("ConsistencyLevel", "eventual")
		}
		resp, err := c.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	names := func(body map[string]any) []string {
		var out []string
		for _, v := range body["value"].([]any) {
			out = append(out, v.(map[string]any)["displayName"].(string))
		}
		return out
	}

	search := url.Values{
		"$search": {`"displayName:KARI" OR "mail:kari" OR "userPrincipalName:kari"`}, "$select": {"id,displayName"},
		"$orderby": {"displayName"}, "$count": {"true"},
	}
	if status, body := get(search, false); status != http.StatusBadRequest || errorCode(body) != "Request_UnsupportedQuery" {
		t.Errorf("a search without ConsistencyLevel: %d %v, want 400 Request_UnsupportedQuery", status, body)
	}
	status, body := get(search, true)
	if status != http.StatusOK || !slices.Equal(names(body), []string{"Anne Kariussen", "Kari Nordmann"}) || body["@odata.count"] != float64(2) {
		t.Errorf("the search: %d %v, want both Karis by display name, counted", status, body)
	}
	search.Set("$top", "1")
	if _, body := get(search, true); !slices.Equal(names(body), []string{"Anne Kariussen"}) || body["@odata.count"] != float64(2) {
		t.Errorf("the search's first: %v", body)
	}
	if status, _ := get(url.Values{"$search": {"kari"}}, true); status != http.StatusBadRequest {
		t.Errorf("an unquoted search: %d, want 400", status)
	}
	if status, _ := get(url.Values{"$filter": {"mail in ('kari.n@nordlys.no')"}, "$count": {"true"}}, false); status != http.StatusBadRequest {
		t.Errorf("a count without ConsistencyLevel: %d, want 400", status)
	}

	for _, c := range []struct {
		filter string
		want   []string
	}{
		{"mail in ('kari.n@nordlys.no', 'o''brien@nordlys.no', 'per.kari@nordlys.no')", []string{"Kari Nordmann", "Ola O'Brien"}},
		{"userPrincipalName in ('ANNE@nordlys.no')", []string{"Anne Kariussen"}},
		{"employeeId in ('E-2','e-1')", []string{"Kari Nordmann", "Anne Kariussen"}},
	} {
		status, body := get(url.Values{"$filter": {c.filter}, "$select": {"id,displayName"}, "$count": {"true"}}, true)
		if status != http.StatusOK || !slices.Equal(names(body), c.want) {
			t.Errorf("%s: %d %v, want %v", c.filter, status, body, c.want)
		}
	}
	sixteen := make([]string, 16)
	for i := range sixteen {
		sixteen[i] = "'x" + strconv.Itoa(i) + "@nordlys.no'"
	}
	if status, _ := get(url.Values{"$filter": {"mail in (" + strings.Join(sixteen, ", ") + ")"}}, true); status != http.StatusBadRequest {
		t.Errorf("an in filter of sixteen values: %d, want 400", status)
	}
	if status, _ := get(url.Values{"$filter": {"displayName in ('Per')"}}, true); status != http.StatusBadRequest {
		t.Errorf("an in filter on displayName: %d, want 400", status)
	}
}

func TestGroups(t *testing.T) {
	now := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	g := tenantFake(&now)
	g.TeamReadyAfter = 15 * time.Minute
	c := newClient(t, g)
	var members []string
	for i := range 21 {
		members = append(members, g.AddUser(graphtest.User{UserPrincipalName: "u" + string(rune('a'+i)) + "@nordlys.no"}).ID)
	}

	status, _, created := c.do(http.MethodPost, "/groups", map[string]any{
		"displayName": "Engineering", "mailNickname": "engineering", "mailEnabled": false, "securityEnabled": true,
		"groupTypes": []any{}, "owners@odata.bind": []any{"https://graph.microsoft.com/v1.0/servicePrincipals/" + g.ServicePrincipalID},
	})
	if status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, created)
	}
	id := created["id"].(string)
	if status, _, _ := c.do(http.MethodPost, "/groups", map[string]any{
		"displayName": "Nobody's", "mailNickname": "nobody", "mailEnabled": false, "securityEnabled": true,
		"owners@odata.bind": []any{"https://graph.microsoft.com/v1.0/users/" + uuidLike},
	}); status != http.StatusNotFound {
		t.Errorf("a create naming an owner the tenant lacks: %d, want 404", status)
	}

	// The owners: the service principal, a user added and removed by $ref.
	if status, _, _ := c.do(http.MethodPost, "/groups/"+id+"/owners/$ref", map[string]any{"@odata.id": "https://graph.microsoft.com/v1.0/users/" + members[0]}); status != http.StatusNoContent {
		t.Errorf("an owner by $ref: %d", status)
	}
	if status, _, body := c.do(http.MethodPost, "/groups/"+id+"/owners/$ref", map[string]any{"@odata.id": "https://graph.microsoft.com/v1.0/users/" + members[0]}); status != http.StatusBadRequest {
		t.Errorf("the owner again: %d %v, want 400", status, body)
	}
	_, _, owners := c.do(http.MethodGet, "/groups/"+id+"/owners?$select=id", nil)
	if list := owners["value"].([]any); len(list) != 2 || list[0].(map[string]any)["@odata.type"] != "#microsoft.graph.servicePrincipal" ||
		list[1].(map[string]any)["@odata.type"] != "#microsoft.graph.user" {
		t.Errorf("the owners: %v", owners)
	}
	if status, _, _ := c.do(http.MethodDelete, "/groups/"+id+"/owners/"+members[0]+"/$ref", nil); status != http.StatusNoContent {
		t.Errorf("an owner removed by $ref: %d", status)
	}
	if gr, _ := g.Group(id); len(gr.Owners) != 1 || gr.Owners[0] != g.ServicePrincipalID {
		t.Errorf("the owners after the removal: %v", gr.Owners)
	}
	bind := func(ids []string) map[string]any {
		refs := make([]any, len(ids))
		for i, m := range ids {
			refs[i] = "https://graph.microsoft.com/v1.0/directoryObjects/" + m
		}
		return map[string]any{"members@odata.bind": refs}
	}
	if status, _, _ := c.do(http.MethodPatch, "/groups/"+id, bind(members)); status != http.StatusBadRequest {
		t.Errorf("21 members in one PATCH: %d, want 400", status)
	}
	if status, _, _ := c.do(http.MethodPatch, "/groups/"+id, bind(members[:20])); status != http.StatusNoContent {
		t.Errorf("20 members: %d, want 204", status)
	}
	_, _, list := c.do(http.MethodGet, "/groups/"+id+"/members?$select=id", nil)
	if len(list["value"].([]any)) != 20 {
		t.Errorf("members = %d, want 20", len(list["value"].([]any)))
	}
	// Paged: $top of them, the rest behind @odata.nextLink.
	_, _, page := c.do(http.MethodGet, "/groups/"+id+"/members?$select=id&$top=15", nil)
	next, _ := page["@odata.nextLink"].(string)
	if len(page["value"].([]any)) != 15 || !strings.HasPrefix(next, "https://graph.microsoft.com/v1.0/groups/"+id+"/members?") {
		t.Fatalf("the first page: %v", page)
	}
	_, _, page = c.do(http.MethodGet, strings.TrimPrefix(next, "https://graph.microsoft.com/v1.0"), nil)
	if len(page["value"].([]any)) != 5 || page["@odata.nextLink"] != nil {
		t.Errorf("the second page: %v", page)
	}
	_, _, of := c.do(http.MethodGet, "/users/"+members[0]+"/memberOf", nil)
	if len(of["value"].([]any)) != 1 {
		t.Errorf("memberOf = %v, want the group", of)
	}

	// $batch: twenty removals by $ref, each answered on its own.
	var requests []any
	for i, m := range members[:20] {
		requests = append(requests, map[string]any{"id": string(rune('a' + i)), "method": "DELETE", "url": "/groups/" + id + "/members/" + m + "/$ref"})
	}
	status, _, batch := c.do(http.MethodPost, "/$batch", map[string]any{"requests": requests})
	responses := batch["responses"].([]any)
	if status != http.StatusOK || len(responses) != 20 || responses[0].(map[string]any)["status"].(float64) != 204 {
		t.Errorf("batch: %d %v", status, batch)
	}
	if status, _, _ := c.do(http.MethodPost, "/$batch", map[string]any{"requests": append(requests, requests[0])}); status != http.StatusBadRequest {
		t.Errorf("a batch of 21: %d, want 400", status)
	}

	// A stale member: listed, and 404 when removed.
	g.EditGroup(id, func(gr *graphtest.Group) { gr.StaleMembers = []string{members[0]} })
	if _, _, list := c.do(http.MethodGet, "/groups/"+id+"/members?$select=id", nil); len(list["value"].([]any)) != 1 {
		t.Errorf("the stale member is not listed: %v", list)
	}
	if status, _, _ := c.do(http.MethodDelete, "/groups/"+id+"/members/"+members[0]+"/$ref", nil); status != http.StatusNotFound {
		t.Errorf("a stale member removed: %d, want 404", status)
	}

	// The trap: a member removed without $ref deletes the user.
	if status, _, _ := c.do(http.MethodPost, "/groups/"+id+"/members/$ref", map[string]any{"@odata.id": "https://graph.microsoft.com/v1.0/directoryObjects/" + members[20]}); status != http.StatusNoContent {
		t.Errorf("add by $ref: %d", status)
	}
	c.do(http.MethodDelete, "/groups/"+id+"/members/"+members[20], nil)
	if u, _ := g.User(members[20]); !u.Deleted {
		t.Error("a DELETE without $ref did not delete the user")
	}

	// The Team: the template named, 404 while the group is younger than
	// TeamReadyAfter, and TeamNotFound more times after.
	template := map[string]any{"template@odata.bind": "https://graph.microsoft.com/v1.0/teamsTemplates('standard')"}
	if status, _, _ := c.do(http.MethodPut, "/groups/"+id+"/team", map[string]any{}); status != http.StatusBadRequest {
		t.Errorf("a team without the template: %d, want 400", status)
	}
	if status, _, _ := c.do(http.MethodPut, "/groups/"+id+"/team", template); status != http.StatusNotFound {
		t.Errorf("a team at once: %d, want 404", status)
	}
	now = now.Add(15 * time.Minute)
	g.Configure(func(g *graphtest.FakeGraph) { g.TeamNotFound = 1 })
	c = newClient(t, g)
	if status, _, _ := c.do(http.MethodPut, "/groups/"+id+"/team", template); status != http.StatusNotFound {
		t.Errorf("a team the backend has not seen: %d, want 404", status)
	}
	if status, _, _ := c.do(http.MethodPut, "/groups/"+id+"/team", template); status != http.StatusCreated {
		t.Errorf("a team after fifteen minutes: %d, want 201", status)
	}
	if status, _, _ := c.do(http.MethodPut, "/groups/"+id+"/team", template); status != http.StatusConflict {
		t.Errorf("a second team: %d, want 409", status)
	}
}

// photoRequest sends method to a user's /photo/$value with body as
// contentType, and answers the status and Graph's error code ("" for none).
func (c *client) photoRequest(method, id, contentType string, body []byte) (int, string) {
	c.t.Helper()
	req, err := http.NewRequest(method, "https://graph.microsoft.com/v1.0/users/"+id+"/photo/$value", bytes.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, errorCode(out)
}

// The photo: PUT keeps a JPEG and refuses another type or none (400), the
// photo denied refuses the PUT and the DELETE (403), and a DELETE of none is
// 404.
func TestPhoto(t *testing.T) {
	now := time.Now()
	g := tenantFake(&now)
	c := newClient(t, g)
	u := g.AddUser(graphtest.User{UserPrincipalName: "foto@nordlys.no", EmployeeID: "E-9", DisplayName: "Foto"})
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xD9}

	if status, code := c.photoRequest(http.MethodPut, u.ID, "image/png", jpeg); status != http.StatusBadRequest || code != "ErrorInvalidImage" {
		t.Errorf("PUT as image/png: %d %s, want 400 ErrorInvalidImage", status, code)
	}
	if status, _ := c.photoRequest(http.MethodPut, u.ID, "image/jpeg", nil); status != http.StatusBadRequest {
		t.Errorf("PUT of nothing: %d, want 400", status)
	}
	if stored, _ := g.User(u.ID); stored.Photo != nil {
		t.Error("a refused PUT stored a photo")
	}
	if status, _ := c.photoRequest(http.MethodPut, u.ID, "image/jpeg", jpeg); status != http.StatusOK {
		t.Fatalf("PUT a JPEG: %d, want 200", status)
	}

	g.Configure(func(g *graphtest.FakeGraph) { g.PhotoDenied = true })
	if status, _ := c.photoRequest(http.MethodPut, u.ID, "image/jpeg", jpeg); status != http.StatusForbidden {
		t.Errorf("PUT denied: %d, want 403", status)
	}
	if status, _ := c.photoRequest(http.MethodDelete, u.ID, "", nil); status != http.StatusForbidden {
		t.Errorf("DELETE denied: %d, want 403", status)
	}
	if stored, _ := g.User(u.ID); stored.Photo == nil {
		t.Error("a denied DELETE removed the photo")
	}
	g.Configure(func(g *graphtest.FakeGraph) { g.PhotoDenied = false })
	if status, _ := c.photoRequest(http.MethodDelete, u.ID, "", nil); status != http.StatusOK {
		t.Errorf("DELETE: %d, want 200", status)
	}
	if status, _ := c.photoRequest(http.MethodDelete, u.ID, "", nil); status != http.StatusNotFound {
		t.Errorf("DELETE of none: %d, want 404", status)
	}
}
