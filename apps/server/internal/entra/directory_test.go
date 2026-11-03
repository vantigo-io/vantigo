package entra_test

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The directory search and the exact matches (Entra phase 2, delivery B;
// the plan's readings 3 to 6), behind contracts.DirectorySearch since
// Google Workspace design G8: an Owner's two reads of the fake tenant
// through identity's endpoints, which ask this module's provider, with
// identity's users and holders read back through contracts.SSOConnections.

// The wire of identity's directory endpoints, as these tests read it (the
// module's tests may not import identity's generated types).
type (
	dirHolder struct {
		Kind   string     `json:"kind"`
		UserID *uuid.UUID `json:"userId"`
	}
	dirUser struct {
		ObjectID          string     `json:"objectId"`
		DisplayName       string     `json:"displayName"`
		UserPrincipalName string     `json:"userPrincipalName"`
		Mail              *string    `json:"mail"`
		EmployeeNumber    *string    `json:"employeeNumber"`
		Enabled           bool       `json:"enabled"`
		Guest             *bool      `json:"guest"`
		Holder            *dirHolder `json:"holder"`
	}
	dirUsers struct {
		Users []dirUser `json:"users"`
	}
	dirMatch struct {
		UserID    uuid.UUID `json:"userId"`
		Account   dirUser   `json:"account"`
		MatchedBy []string  `json:"matchedBy"`
	}
	dirMatches struct {
		Matches    []dirMatch `json:"matches"`
		Candidates int        `json:"candidates"`
		Ambiguous  int        `json:"ambiguous"`
		Truncated  bool       `json:"truncated"`
	}
	identityError struct {
		Error struct {
			Code   string              `json:"code"`
			Fields map[string][]string `json:"fields"`
		} `json:"error"`
	}
)

// identityOperation marks an exchange with identity's directory endpoints:
// this package's recorder validates against Entra's contract alone, and
// identity's own tests validate those operations against its.
var identityOperation = modtest.SkipContract("identity's directory operation, validated by identity's own tests")

// identityCode is identity's refusal code, "" without one.
func identityCode(r *modtest.Response) string {
	var e identityError
	r.JSON(&e)
	return e.Error.Code
}

// enableOIDC configures the workforce provider in identity's settings row,
// as an Owner's save would, and moves the clock past the settings' cache so
// every reader sees it.
func enableOIDC(t *testing.T, h *harness) {
	t.Helper()
	h.Exec(t, `INSERT INTO identity.sso_settings (id, version, oidc_enabled, oidc_provider, oidc_authority, oidc_client_id, updated_at)
	           VALUES (1, 1, true, 'entra', $1, $2, $3)
	           ON CONFLICT (id) DO UPDATE SET oidc_enabled = true, oidc_provider = 'entra', oidc_authority = EXCLUDED.oidc_authority,
	               oidc_client_id = EXCLUDED.oidc_client_id, version = identity.sso_settings.version + 1`,
		"https://login.microsoftonline.com/"+tenantID+"/v2.0", clientID, h.Now())
	h.Advance(time.Minute)
}

// addUser is a user of identity's with email, and employee number when not
// "", displayed as their address so identity's order is the addresses'.
func addUser(t *testing.T, h *harness, email, number string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var n *string
	if number != "" {
		n = &number
	}
	h.Exec(t, `INSERT INTO identity.users (id, email, normalized_email, display_name, employee_number, employee_number_key, version, created_at, updated_at)
	           VALUES ($1, $2, upper($2), $2, $3::text, upper($3::text), $4, $5, $5)`, id, email, n, uuid.New(), h.Now())
	return id
}

// mapAccount makes a SCIM mapping, under an external id of its own, hold
// upn for userID, as the provider's push would.
func mapAccount(t *testing.T, h *harness, userID uuid.UUID, upn string) {
	t.Helper()
	h.Exec(t, `INSERT INTO identity.scim_user_mappings (resource_id, user_id, external_id, user_name, upstream_active, version, etag, created_at, updated_at)
	           VALUES ($1, $2, $3, $4, true, 1, 'etag', $5, $5)`, uuid.New(), userID, uuid.NewString(), upn, h.Now())
}

// account seeds an enabled account in the fake tenant and answers its id.
func account(g *graphtest.FakeGraph, name, upn, mail, number string) string {
	return g.AddUser(graphtest.User{
		ID: uuid.NewString(), DisplayName: name, UserPrincipalName: upn, Mail: mail, EmployeeID: number, AccountEnabled: true,
	}).ID
}

// directoryHarness is a connected tenant with single sign-on configured,
// and its Owner.
func directoryHarness(t *testing.T) (*harness, *graphtest.FakeGraph, *modtest.Client) {
	t.Helper()
	h, g := newTenantHarness(t)
	enableOIDC(t, h)
	return h, g, connected(t, h)
}

// usersCalls is every GET /users the fake answered, with its query
// decoded.
func usersCalls(g *graphtest.FakeGraph) []url.Values {
	var out []url.Values
	for _, c := range g.Calls() {
		if c.Host == graphtest.GraphHost && c.Method == http.MethodGet && c.Path == "/v1.0/users" {
			q, _ := url.ParseQuery(c.Query)
			out = append(out, q)
		}
	}
	return out
}

func TestDirectory_Search(t *testing.T) {
	t.Parallel()
	h, g, o := directoryHarness(t)
	nordmann := account(g, "Kari Nordmann", "kari.nordmann@nordlys.no", "Kari.Nordmann@nordlys.no", "E-1")
	hansen := g.AddUser(graphtest.User{ID: uuid.NewString(), DisplayName: "Kari Hansen", UserPrincipalName: "k.hansen@nordlys.no"}).ID
	account(g, "Ola Nordmann", "ola@nordlys.no", "", "")
	holder := addUser(t, h, "holder@example.test", "")
	mapAccount(t, h, holder, "K.Hansen@nordlys.no")

	search := func(q string) *modtest.Response {
		return o.Do(http.MethodGet, directoryPath+"?"+url.Values{"search": {q}}.Encode(), nil, identityOperation)
	}
	r := search("kari")
	if r.Status != http.StatusOK {
		t.Fatalf("search: %d %s", r.Status, r.Body)
	}
	var got dirUsers
	r.JSON(&got)
	if len(got.Users) != 2 {
		t.Fatalf("search kari: %+v, want the two Karis", got.Users)
	}
	first, second := got.Users[0], got.Users[1]
	if first.ObjectID != hansen || first.Enabled || first.Mail != nil || first.EmployeeNumber != nil ||
		first.Holder == nil || first.Holder.Kind != "mapped" || first.Holder.UserID == nil || *first.Holder.UserID != holder {
		t.Errorf("the first hit: %+v (holder %+v), want Kari Hansen, disabled, mapped to the holder", first, first.Holder)
	}
	if second.ObjectID != nordmann || !second.Enabled || second.Mail == nil || *second.Mail != "Kari.Nordmann@nordlys.no" ||
		second.EmployeeNumber == nil || *second.EmployeeNumber != "E-1" || second.Holder != nil {
		t.Errorf("the second hit: %+v, want Kari Nordmann, enabled, unheld", second)
	}

	// What was asked: the search over the three properties, by display
	// name, 25 at most, counted — and the fake answered it, which it does
	// only with ConsistencyLevel: eventual (graphtest's TestDirectoryQueries).
	calls := usersCalls(g)
	last := calls[len(calls)-1]
	if last.Get("$search") != `"displayName:kari" OR "mail:kari" OR "userPrincipalName:kari"` || last.Get("$orderby") != "displayName" ||
		last.Get("$top") != "25" || last.Get("$count") != "true" {
		t.Errorf("the search asked %v", last)
	}

	// A quote and a backslash are left out before the search is quoted.
	if r := search(`"kari\`); r.Status != http.StatusOK {
		t.Errorf(`search "kari\: %d %s`, r.Status, r.Body)
	}
	calls = usersCalls(g)
	if s := calls[len(calls)-1].Get("$search"); s != `"displayName:kari" OR "mail:kari" OR "userPrincipalName:kari"` {
		t.Errorf("the stripped search asked %q", s)
	}

	// Too short once trimmed, or once stripped: 400 on search, nothing
	// asked.
	before := len(usersCalls(g))
	for _, q := range []string{" k ", `"k"`, ""} {
		r := search(q)
		var e identityError
		r.JSON(&e)
		if r.Status != http.StatusBadRequest || e.Error.Code != "invalid_request" || len(e.Error.Fields["search"]) == 0 {
			t.Errorf("search %q: %d %s, want 400 invalid_request on search", q, r.Status, r.Body)
		}
	}
	if len(usersCalls(g)) != before {
		t.Error("a refused search reached the tenant")
	}

	// Throttled twice in a row: 503 provider_unreachable.
	g.Throttle(2)
	if r := search("kari"); r.Status != http.StatusServiceUnavailable || identityCode(r) != "provider_unreachable" {
		t.Errorf("a throttled search: %d %s, want 503 provider_unreachable", r.Status, r.Body)
	}
}

func TestDirectory_SearchWithAnInvalidSecret(t *testing.T) {
	t.Parallel()
	h, _ := newTenantHarness(t)
	enableOIDC(t, h)
	o, _ := owner(t, h)
	register(t, o)
	addSecret(t, o, "not-the-secret", "Wrong", h.Now().AddDate(1, 0, 0))
	if r := o.Do(http.MethodGet, directoryPath+"?search=kari", nil, identityOperation); r.Status != http.StatusServiceUnavailable || identityCode(r) != "provider_unreachable" {
		t.Errorf("a search with a refused secret: %d %s, want 503 provider_unreachable", r.Status, r.Body)
	}
}

func TestDirectory_Matches(t *testing.T) {
	t.Parallel()
	h, g, o := directoryHarness(t)

	// Proposed: by mail and principal name both; by principal name; by
	// employee number, ignoring case; a disabled account, by mail.
	anne := addUser(t, h, "anne@nordlys.no", "")
	anneAccount := account(g, "Anne", "anne@nordlys.no", "Anne@Nordlys.no", "")
	bjorn := addUser(t, h, "bjorn@nordlys.no", "")
	bjornAccount := account(g, "Bjørn", "BJORN@nordlys.no", "", "")
	carl := addUser(t, h, "carl@other.example", "E-3")
	carlAccount := account(g, "Carl", "carl.c@nordlys.no", "", "e-3")
	ivar := addUser(t, h, "ivar@nordlys.no", "")
	ivarAccount := g.AddUser(graphtest.User{ID: uuid.NewString(), DisplayName: "Ivar", UserPrincipalName: "ivar.i@nordlys.no", Mail: "ivar@nordlys.no"}).ID
	kjell := addUser(t, h, "kjell@nordlys.no", "")
	kjellAccount := account(g, "Kjell", "kjell.k@nordlys.no", "kjell@nordlys.no", "")

	// Left out, ambiguous: two accounts for Dina (one by mail, one by her
	// number); one account for Eva (by mail) and Frida (by number).
	addUser(t, h, "dina@nordlys.no", "E-4")
	account(g, "Dina", "dina.d@nordlys.no", "dina@nordlys.no", "")
	account(g, "Dina too", "dina2@nordlys.no", "", "E-4")
	addUser(t, h, "eva@nordlys.no", "")
	addUser(t, h, "frida@other.example", "E-6")
	account(g, "Eva", "eva.e@nordlys.no", "eva@nordlys.no", "E-6")

	// Left out, not ambiguous: Gro's account is held by another user's SCIM
	// mapping; Jon has no account.
	addUser(t, h, "gro@nordlys.no", "")
	account(g, "Gro", "gro@nordlys.no", "", "")
	holder := addUser(t, h, "holder@example.test", "")
	mapAccount(t, h, holder, "gro@nordlys.no")
	addUser(t, h, "jon@nordlys.no", "")

	r := o.Do(http.MethodGet, matchesPath, nil, identityOperation)
	if r.Status != http.StatusOK {
		t.Fatalf("matches: %d %s", r.Status, r.Body)
	}
	var got dirMatches
	r.JSON(&got)
	if got.Candidates != 11 || got.Ambiguous != 3 || got.Truncated {
		t.Errorf("candidates %d ambiguous %d truncated %v, want 11, 3, false", got.Candidates, got.Ambiguous, got.Truncated)
	}
	type want struct {
		user    uuid.UUID
		account string
		by      []string
		enabled bool
	}
	wants := []want{
		{anne, anneAccount, []string{"mail", "principalName"}, true},
		{bjorn, bjornAccount, []string{"principalName"}, true},
		{carl, carlAccount, []string{"employeeNumber"}, true},
		{ivar, ivarAccount, []string{"mail"}, false},
		{kjell, kjellAccount, []string{"mail"}, true},
	}
	if len(got.Matches) != len(wants) {
		t.Fatalf("matches: %+v, want %d", got.Matches, len(wants))
	}
	for i, w := range wants {
		m := got.Matches[i]
		if m.UserID != w.user || m.Account.ObjectID != w.account || !slices.Equal(m.MatchedBy, w.by) ||
			m.Account.Enabled != w.enabled || m.Account.Holder != nil {
			t.Errorf("match %d: %+v, want %+v", i, m, w)
		}
	}

	// Every read was an in filter of at most fifteen values, answered (so
	// with ConsistencyLevel: eventual), counted.
	calls := usersCalls(g)
	if len(calls) != 3 {
		t.Errorf("the matches asked %d times, want 3 (mail, principal name, employee number)", len(calls))
	}
	for _, q := range calls {
		if !strings.Contains(q.Get("$filter"), " in (") || q.Get("$count") != "true" {
			t.Errorf("the matches asked %v", q)
		}
	}
	for _, c := range g.Calls() {
		if c.Status != http.StatusOK {
			t.Errorf("the fake refused %s %s: %d", c.Method, c.Path, c.Status)
		}
	}
	if logs := strings.ToLower(h.Logs()); strings.Contains(logs, "anne@nordlys.no") || strings.Contains(logs, "dina@nordlys.no") {
		t.Error("an address reached the log")
	}
}

// A guest account matches nobody: Liv's guest account by mail is not
// proposed, nor counted ambiguous; Mona's guest account beside her member
// account leaves the member alone, proposed. A candidate's own SCIM mapping
// of the account does not block proposing it; another user's does. The
// search answers a guest marked as one, a member unmarked.
func TestDirectory_MatchesLeaveOutGuestsAndOwnMappings(t *testing.T) {
	t.Parallel()
	h, g, o := directoryHarness(t)
	addUser(t, h, "liv@nordlys.no", "")
	g.AddUser(graphtest.User{ID: uuid.NewString(), DisplayName: "Liv (guest)", UserPrincipalName: "liv_nordlys.no#EXT#@tenant.example",
		Mail: "liv@nordlys.no", AccountEnabled: true, UserType: "Guest"})
	mona := addUser(t, h, "mona@nordlys.no", "")
	g.AddUser(graphtest.User{ID: uuid.NewString(), DisplayName: "Mona (guest)", UserPrincipalName: "mona_nordlys.no#EXT#@tenant.example",
		Mail: "mona@nordlys.no", AccountEnabled: true, UserType: "Guest"})
	monaAccount := account(g, "Mona", "mona@nordlys.no", "", "")
	nils := addUser(t, h, "nils@nordlys.no", "")
	nilsAccount := account(g, "Nils", "nils@nordlys.no", "", "")
	mapAccount(t, h, nils, "Nils@nordlys.no")

	r := o.Do(http.MethodGet, matchesPath, nil, identityOperation)
	if r.Status != http.StatusOK {
		t.Fatalf("matches: %d %s", r.Status, r.Body)
	}
	var got dirMatches
	r.JSON(&got)
	if got.Candidates != 3 || got.Ambiguous != 0 || len(got.Matches) != 2 {
		t.Fatalf("matches: %+v, want Mona's and Nils's of 3 candidates, none ambiguous", got)
	}
	if m := got.Matches[0]; m.UserID != mona || m.Account.ObjectID != monaAccount || m.Account.Guest != nil ||
		!slices.Equal(m.MatchedBy, []string{"principalName"}) {
		t.Errorf("the first match: %+v, want Mona's member account by principal name", m)
	}
	if m := got.Matches[1]; m.UserID != nils || m.Account.ObjectID != nilsAccount {
		t.Errorf("the second match: %+v, want Nils's own mapped account", m)
	}

	r = o.Do(http.MethodGet, directoryPath+"?"+url.Values{"search": {"mona"}}.Encode(), nil, identityOperation)
	var found dirUsers
	r.JSON(&found)
	if len(found.Users) != 2 || found.Users[0].Guest != nil || found.Users[1].Guest == nil || !*found.Users[1].Guest {
		t.Errorf("search mona: %+v, want the member unmarked, then the guest marked", found.Users)
	}
}

// Sixteen addresses are two in filters of each property; nobody with a
// number, no employeeId filter. A quote in an address is doubled.
func TestDirectory_MatchesChunkTheFilters(t *testing.T) {
	t.Parallel()
	h, g, o := directoryHarness(t)
	for i := range 15 {
		addUser(t, h, "user"+string(rune('a'+i))+"@nordlys.no", "")
	}
	obrien := addUser(t, h, "o'brien@nordlys.no", "")
	obrienAccount := account(g, "Ola O'Brien", "ola.obrien@nordlys.no", "O'Brien@nordlys.no", "")
	r := o.Do(http.MethodGet, matchesPath, nil, identityOperation)
	if r.Status != http.StatusOK {
		t.Fatalf("matches: %d %s", r.Status, r.Body)
	}
	var got dirMatches
	r.JSON(&got)
	if got.Candidates != 16 || len(got.Matches) != 1 || got.Matches[0].UserID != obrien || got.Matches[0].Account.ObjectID != obrienAccount {
		t.Errorf("matches: %+v, want O'Brien's alone", got)
	}
	var properties []string
	for _, q := range usersCalls(g) {
		f := q.Get("$filter")
		property, _, _ := strings.Cut(f, " ")
		properties = append(properties, property)
		if n := strings.Count(f, "@"); n > 15 {
			t.Errorf("a filter of %d values", n)
		}
	}
	if !slices.Equal(properties, []string{"mail", "mail", "userPrincipalName", "userPrincipalName"}) {
		t.Errorf("the filters were on %v", properties)
	}
}

// More than 1000 users waiting: the first 1000 are read, truncated says so.
func TestDirectory_MatchesTruncate(t *testing.T) {
	t.Parallel()
	h, _, o := directoryHarness(t)
	h.Exec(t, `INSERT INTO identity.users (id, email, normalized_email, display_name, version, created_at, updated_at)
	           SELECT gen_random_uuid(), 'bulk-' || n || '@nordlys.no', upper('bulk-' || n || '@nordlys.no'), 'bulk-' || n, gen_random_uuid(), $1, $1
	           FROM generate_series(1, 1001) AS n`, h.Now())
	r := o.Do(http.MethodGet, matchesPath, nil, identityOperation)
	if r.Status != http.StatusOK {
		t.Fatalf("matches: %d %s", r.Status, r.Body)
	}
	var got dirMatches
	r.JSON(&got)
	if got.Candidates != 1000 || !got.Truncated || len(got.Matches) != 0 || got.Ambiguous != 0 {
		t.Errorf("matches: candidates %d truncated %v matches %d ambiguous %d, want 1000, true, 0, 0",
			got.Candidates, got.Truncated, len(got.Matches), got.Ambiguous)
	}
}

// While single sign-on is off there is no directory to connect from: 409
// no_directory for both reads, and the tenant is not asked.
func TestDirectory_MatchesWithoutSSO(t *testing.T) {
	t.Parallel()
	h, g := newTenantHarness(t)
	o := connected(t, h)
	addUser(t, h, "anne@nordlys.no", "")
	before := len(usersCalls(g))
	if r := o.Do(http.MethodGet, matchesPath, nil, identityOperation); r.Status != http.StatusConflict || identityCode(r) != "no_directory" {
		t.Errorf("matches without SSO: %d %s, want 409 no_directory", r.Status, r.Body)
	}
	if r := o.Do(http.MethodGet, directoryPath+"?search=anne", nil, identityOperation); r.Status != http.StatusConflict || identityCode(r) != "no_directory" {
		t.Errorf("a search without SSO: %d %s, want 409 no_directory", r.Status, r.Body)
	}
	if len(usersCalls(g)) != before {
		t.Error("the matches asked the tenant without SSO")
	}
}

// The tenant refusing the secret, down, or throttling twice: 503
// provider_unreachable.
func TestDirectory_MatchesWhenTheTenantCannotBeAsked(t *testing.T) {
	t.Parallel()
	t.Run("a refused secret", func(t *testing.T) {
		t.Parallel()
		h, _ := newTenantHarness(t)
		enableOIDC(t, h)
		o, _ := owner(t, h)
		register(t, o)
		addSecret(t, o, "not-the-secret", "Wrong", h.Now().AddDate(1, 0, 0))
		addUser(t, h, "anne@nordlys.no", "")
		if r := o.Do(http.MethodGet, matchesPath, nil, identityOperation); r.Status != http.StatusServiceUnavailable || identityCode(r) != "provider_unreachable" {
			t.Errorf("matches with a refused secret: %d %s, want 503 provider_unreachable", r.Status, r.Body)
		}
	})
	t.Run("the tenant down", func(t *testing.T) {
		t.Parallel()
		h, g, o := directoryHarness(t)
		addUser(t, h, "anne@nordlys.no", "")
		g.FailNext(http.StatusServiceUnavailable)
		g.FailNext(http.StatusServiceUnavailable)
		if r := o.Do(http.MethodGet, matchesPath, nil, identityOperation); r.Status != http.StatusServiceUnavailable || identityCode(r) != "provider_unreachable" {
			t.Errorf("matches with the tenant down: %d %s, want 503 provider_unreachable", r.Status, r.Body)
		}
	})
	t.Run("throttled", func(t *testing.T) {
		t.Parallel()
		h, g, o := directoryHarness(t)
		addUser(t, h, "anne@nordlys.no", "")
		g.Throttle(2)
		if r := o.Do(http.MethodGet, matchesPath, nil, identityOperation); r.Status != http.StatusServiceUnavailable || identityCode(r) != "provider_unreachable" {
			t.Errorf("throttled matches: %d %s, want 503 provider_unreachable", r.Status, r.Body)
		}
	})
}

// The Owner's single sign-on settings name this module's directory while
// Entra is the configured provider, and none while single sign-on is off.
func TestDirectory_SettingsNameTheDirectory(t *testing.T) {
	t.Parallel()
	h, _ := newTenantHarness(t)
	o := ownerWithMFA(t, h)
	type settings struct {
		Directory *struct {
			Provider    string `json:"provider"`
			DisplayName string `json:"displayName"`
		} `json:"directory"`
	}
	read := func() settings {
		t.Helper()
		r := o.Do(http.MethodGet, "/api/v1/identity/owner/sso", nil, identityOperation)
		if r.Status != http.StatusOK {
			t.Fatalf("the settings: %d %s", r.Status, r.Body)
		}
		var got settings
		r.JSON(&got)
		return got
	}
	if got := read(); got.Directory != nil {
		t.Errorf("directory without SSO: %+v, want null", got.Directory)
	}
	enableOIDC(t, h)
	if got := read(); got.Directory == nil || got.Directory.Provider != "entra" || got.Directory.DisplayName != "Microsoft Entra ID" {
		t.Errorf("directory with Entra configured: %+v, want entra, Microsoft Entra ID", got.Directory)
	}
}
