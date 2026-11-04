package google_test

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/google/googletest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The directory search and the exact matches (Google Workspace design G23;
// the plan, docs/superpowers/plans/2026-11-03-google-link-users.md): an
// Owner's two reads of the fake organisation through identity's endpoints,
// which ask this module's provider while google is the configured OIDC
// provider, with identity's users and holders read back through
// contracts.SSOConnections.

// The paths of identity's directory endpoints and of an announced link.
const (
	directoryPath = "/api/v1/identity/owner/sso/directory/users"
	matchesPath   = "/api/v1/identity/owner/sso/directory/matches"
)

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
			Code string `json:"code"`
		} `json:"error"`
	}
)

// identityOperation marks an exchange with identity's endpoints: this
// package's recorder validates against Google's contract alone, and
// identity's own tests validate those operations against its.
var identityOperation = modtest.SkipContract("identity's operation, validated by identity's own tests")

// identityCode is identity's refusal code, "" without one.
func identityCode(r *modtest.Response) string {
	var e identityError
	r.JSON(&e)
	return e.Error.Code
}

// enableGoogleSSO configures Google as the workforce provider in identity's
// settings row, as an Owner's save would, and moves the clock past the
// settings' cache so every reader sees it.
func enableGoogleSSO(t *testing.T, h *harness) {
	t.Helper()
	h.Exec(t, `INSERT INTO identity.sso_settings (id, version, oidc_enabled, oidc_provider, oidc_authority, oidc_client_id, oidc_allowed_domains, updated_at)
	           VALUES (1, 1, true, 'google', 'https://accounts.google.com', $1, '{nordlys.no}', $2)
	           ON CONFLICT (id) DO UPDATE SET oidc_enabled = true, oidc_provider = 'google', oidc_authority = EXCLUDED.oidc_authority,
	               oidc_client_id = EXCLUDED.oidc_client_id, oidc_allowed_domains = EXCLUDED.oidc_allowed_domains,
	               version = identity.sso_settings.version + 1`, webClientID, h.Now())
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
// address for userID, as a push would.
func mapAccount(t *testing.T, h *harness, userID uuid.UUID, address string) {
	t.Helper()
	h.Exec(t, `INSERT INTO identity.scim_user_mappings (resource_id, user_id, external_id, user_name, upstream_active, version, etag, created_at, updated_at)
	           VALUES ($1, $2, $3, $4, true, 1, 'etag', $5, $5)`, uuid.New(), userID, uuid.NewString(), address, h.Now())
}

// person puts an account in the fake organisation and answers its id.
func person(g *googletest.FakeGoogle, given, family, email, number string, aliases ...string) string {
	u := googletest.User{GivenName: given, FamilyName: family, PrimaryEmail: email, Aliases: aliases}
	if number != "" {
		u.ExternalIDs = []map[string]any{{"type": "organization", "value": number}}
	}
	return g.AddUser(u).ID
}

// directoryHarness is a connected organisation with Google single sign-on
// configured, and its Owner.
func directoryHarness(t *testing.T) (*harness, *googletest.FakeGoogle, *modtest.Client) {
	t.Helper()
	l := newLifecycle(t)
	enableGoogleSSO(t, l.h)
	o, id := owner(t, l.h)
	l.h.Exec(t, `UPDATE identity.sessions SET mfa_verified_at = $2 WHERE user_id = $1`, id, l.h.Now())
	return l.h, l.g, o
}

// listCalls is every users.list the fake answered, with its query decoded.
func listCalls(g *googletest.FakeGoogle) []url.Values {
	var out []url.Values
	for _, c := range g.Calls() {
		if c.Host == googletest.AdminHost && c.Method == http.MethodGet && c.Path == "/admin/directory/v1/users" {
			q, _ := url.ParseQuery(c.Query)
			out = append(out, q)
		}
	}
	return out
}

// search is GET /directory/users?search=q as o.
func search(o *modtest.Client, q string) *modtest.Response {
	return o.Do(http.MethodGet, directoryPath+"?"+url.Values{"search": {q}}.Encode(), nil, identityOperation)
}

func TestDirectorySearch_TwoPrefixQueriesMergedByID(t *testing.T) {
	t.Parallel()
	h, g, o := directoryHarness(t)
	// Kari Nordmann by her name and by her address; Kari Hansen by her
	// name, suspended, held by another user's mapping; Ola by an alias
	// starting kari, and by nothing else.
	nordmann := person(g, "Kari", "Nordmann", "kari.nordmann@nordlys.no", "E-1")
	hansen := person(g, "Kari", "Hansen", "k.hansen@nordlys.no", "")
	g.SetUser(hansen, func(u *googletest.User) { u.Suspended = true })
	ola := person(g, "Ola", "Berg", "ola@nordlys.no", "", "kari.vikar@nordlys.no")
	person(g, "Per", "Nordmann", "per@nordlys.no", "")
	holder := addUser(t, h, "holder@example.test", "")
	mapAccount(t, h, holder, "K.Hansen@nordlys.no")

	before := len(listCalls(g))
	r := search(o, "kari")
	if r.Status != http.StatusOK {
		t.Fatalf("search: %d %s", r.Status, r.Body)
	}
	var got dirUsers
	r.JSON(&got)
	ids := []string{}
	for _, u := range got.Users {
		ids = append(ids, u.ObjectID)
	}
	if !slices.Equal(ids, []string{hansen, nordmann, ola}) {
		t.Fatalf("search kari: %v, want Kari Hansen, Kari Nordmann, Ola Berg by full name", ids)
	}
	first, second, third := got.Users[0], got.Users[1], got.Users[2]
	if first.DisplayName != "Kari Hansen" || first.UserPrincipalName != "k.hansen@nordlys.no" || first.Enabled ||
		first.Guest != nil || first.Holder == nil || first.Holder.Kind != "mapped" || first.Holder.UserID == nil || *first.Holder.UserID != holder {
		t.Errorf("the first: %+v (holder %+v), want Kari Hansen, suspended, mapped to the holder", first, first.Holder)
	}
	if !second.Enabled || second.Mail == nil || *second.Mail != "kari.nordmann@nordlys.no" ||
		second.EmployeeNumber == nil || *second.EmployeeNumber != "E-1" || second.Holder != nil {
		t.Errorf("the second: %+v, want Kari Nordmann enabled, unheld, with her number", second)
	}
	if third.UserPrincipalName != "ola@nordlys.no" {
		t.Errorf("the third: %+v, want Ola found by his alias", third)
	}

	// Two queries, never an OR: the name and the address, as prefixes, 20
	// each, basic.
	calls := listCalls(g)[before:]
	var queries []string
	for _, q := range calls {
		queries = append(queries, q.Get("query"))
		if q.Get("customer") != "my_customer" || q.Get("maxResults") != "20" || q.Get("projection") != "basic" {
			t.Errorf("the search asked %v", q)
		}
	}
	if !slices.Equal(queries, []string{"name:'kari*'", "email:'kari*'"}) {
		t.Errorf("the queries: %q", queries)
	}

	// The characters a query would read are left out before it is quoted;
	// a search of nothing but them reaches identity's own refusal.
	if r := search(o, `ka'r*i`); r.Status != http.StatusOK {
		t.Errorf("search ka'r*i: %d %s", r.Status, r.Body)
	}
	calls = listCalls(g)
	if q := calls[len(calls)-1].Get("query"); q != "email:'kari*'" {
		t.Errorf("the stripped search asked %q", q)
	}
	before = len(listCalls(g))
	if r := search(o, `'*'`); r.Status != http.StatusOK && r.Status != http.StatusBadRequest {
		t.Errorf("search '*': %d %s", r.Status, r.Body)
	}
	if len(listCalls(g)) != before {
		t.Error("a search of nothing reached Google")
	}
	if logs := strings.ToLower(h.Logs()); strings.Contains(logs, "kari") {
		t.Error("the search reached the log")
	}
}

// Twenty at most: the merged answer is cut to twenty, by full name.
func TestDirectorySearch_TwentyAtMost(t *testing.T) {
	t.Parallel()
	_, g, o := directoryHarness(t)
	for i := range 25 {
		person(g, "Anna", fmt.Sprintf("Nr%02d", i), fmt.Sprintf("anna%02d@nordlys.no", i), "")
	}
	r := search(o, "anna")
	var got dirUsers
	r.JSON(&got)
	if r.Status != http.StatusOK || len(got.Users) != 20 || got.Users[0].DisplayName != "Anna Nr00" || got.Users[19].DisplayName != "Anna Nr19" {
		t.Errorf("search anna: %d, %d users, want the first 20 by name", r.Status, len(got.Users))
	}
}

// The email query is a prefix ignoring case, as Google's: a primary email
// held in mixed case is found by a lower-case search.
func TestDirectorySearch_TheEmailPrefixIgnoresCase(t *testing.T) {
	t.Parallel()
	_, g, o := directoryHarness(t)
	id := person(g, "Kari", "Nordmann", "kari@nordlys.no", "")
	g.SetUser(id, func(u *googletest.User) { u.PrimaryEmail = "Ola.Nordmann@Nordlys.no" })
	r := search(o, "ola.nord")
	var got dirUsers
	r.JSON(&got)
	if r.Status != http.StatusOK || len(got.Users) != 1 || got.Users[0].ObjectID != id {
		t.Errorf("search ola.nord: %d %s, want the mixed-case primary email's account", r.Status, r.Body)
	}
}

func TestDirectoryMatches_ExactByAddressAliasAndNumber(t *testing.T) {
	t.Parallel()
	h, g, o := directoryHarness(t)

	// Proposed: Anne by her primary email (mail and principal name); Bjørn
	// by an alias; Carl by his number, ignoring case; Ivar suspended, by
	// his non-editable alias.
	anne := addUser(t, h, "Anne@Nordlys.no", "")
	anneAccount := person(g, "Anne", "A", "anne@nordlys.no", "")
	bjorn := addUser(t, h, "bjorn@nordlys.no", "")
	bjornAccount := person(g, "Bjørn", "B", "bjorn.b@nordlys.no", "", "bjorn@nordlys.no")
	carl := addUser(t, h, "carl@other.example", "E-3")
	carlAccount := person(g, "Carl", "C", "carl.c@nordlys.no", "e-3")
	ivar := addUser(t, h, "ivar@nordlys.example", "")
	ivarAccount := person(g, "Ivar", "I", "ivar.i@nordlys.no", "")
	g.SetUser(ivarAccount, func(u *googletest.User) {
		u.Suspended = true
		u.NonEditableAliases = []string{"ivar@nordlys.example"}
	})

	// Left out, ambiguous: two accounts for Dina (one by address, one by
	// her number); one account for Eva (by address) and Frida (by number).
	addUser(t, h, "dina@nordlys.no", "E-4")
	person(g, "Dina", "D", "dina@nordlys.no", "")
	person(g, "Dina", "Too", "dina2@nordlys.no", "E-4")
	addUser(t, h, "eva@nordlys.no", "")
	addUser(t, h, "frida@other.example", "E-6")
	person(g, "Eva", "E", "eva@nordlys.no", "E-6")

	// Left out, not ambiguous: Gro's account is held by another user's
	// mapping; Jon has no account; Liv's address is on her account only as a
	// home address an administrator typed, and Kjell's only as a work
	// address on a verified domain, neither of which is the account's own.
	addUser(t, h, "gro@nordlys.no", "")
	person(g, "Gro", "G", "gro@nordlys.no", "")
	holder := addUser(t, h, "holder@example.test", "")
	mapAccount(t, h, holder, "gro@nordlys.no")
	addUser(t, h, "jon@nordlys.no", "")
	addUser(t, h, "liv@gmail.example", "")
	livAccount := person(g, "Liv", "L", "liv@nordlys.no", "")
	g.SetUser(livAccount, func(u *googletest.User) {
		u.OtherEmails = []map[string]any{{"address": "liv@gmail.example", "type": "home"}}
	})
	addUser(t, h, "kjell@nordlys.no", "")
	kjellAccount := person(g, "Kjell", "K", "kjell.k@nordlys.no", "")
	g.SetUser(kjellAccount, func(u *googletest.User) {
		u.OtherEmails = []map[string]any{{"address": "Kjell@Nordlys.no", "type": "work"}}
	})

	before := len(listCalls(g))
	r := o.Do(http.MethodGet, matchesPath, nil, identityOperation)
	if r.Status != http.StatusOK {
		t.Fatalf("matches: %d %s", r.Status, r.Body)
	}
	var got dirMatches
	r.JSON(&got)
	// Thirteen: the twelve above and the Owner who configured the
	// connection, who has no account.
	if got.Candidates != 13 || got.Ambiguous != 3 || got.Truncated {
		t.Errorf("candidates %d ambiguous %d truncated %v, want 13, 3, false", got.Candidates, got.Ambiguous, got.Truncated)
	}
	type want struct {
		user    uuid.UUID
		account string
		by      []string
		enabled bool
	}
	wants := []want{
		{anne, anneAccount, []string{"mail", "principalName"}, true},
		{bjorn, bjornAccount, []string{"mail"}, true},
		{carl, carlAccount, []string{"employeeNumber"}, true},
		{ivar, ivarAccount, []string{"mail"}, false},
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
	if m := got.Matches[1].Account; m.UserPrincipalName != "bjorn.b@nordlys.no" || m.Mail == nil || *m.Mail != "bjorn.b@nordlys.no" {
		t.Errorf("Bjørn's account: %+v, want the primary email as both keys", m)
	}

	// The users were read once, unfiltered, in pages of 500.
	calls := listCalls(g)[before:]
	if len(calls) != 1 || calls[0].Get("query") != "" || calls[0].Get("maxResults") != "500" || calls[0].Get("projection") != "basic" {
		t.Errorf("the matches asked %v", calls)
	}
	if logs := strings.ToLower(h.Logs()); strings.Contains(logs, "anne@nordlys.no") || strings.Contains(logs, "dina@nordlys.no") {
		t.Error("an address reached the log")
	}
}

// A pick announced: the link names the Google id and the primary email, as
// identity keys a Google sign-in by its sub (G7, G24); the matches then
// leave the user out, connected.
func TestDirectoryMatches_APickAnnouncedNamesTheGoogleID(t *testing.T) {
	t.Parallel()
	h, g, o := directoryHarness(t)
	anne := addUser(t, h, "anne@nordlys.no", "")
	anneAccount := person(g, "Anne", "A", "anne@nordlys.no", "")

	r := search(o, "anne")
	var found dirUsers
	r.JSON(&found)
	if len(found.Users) != 1 || found.Users[0].ObjectID != anneAccount {
		t.Fatalf("search anne: %+v", found.Users)
	}
	pick := found.Users[0]
	if r := o.Do(http.MethodPost, "/api/v1/identity/owner/users/"+anne.String()+"/sso-link",
		map[string]any{"objectId": pick.ObjectID, "userPrincipalName": pick.UserPrincipalName}, identityOperation); r.Status != http.StatusOK {
		t.Fatalf("announce: %d %s", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM identity.expected_links WHERE user_id = $1 AND object_id = $2 AND lower(user_principal_name) = $3`,
		anne, anneAccount, "anne@nordlys.no"); n != 1 {
		t.Error("the announced link does not name the Google id and the primary email")
	}

	r = o.Do(http.MethodGet, matchesPath, nil, identityOperation)
	var got dirMatches
	r.JSON(&got)
	// The one candidate left is the Owner who configured the connection.
	if got.Candidates != 1 || len(got.Matches) != 0 {
		t.Errorf("matches after the announcement: %+v, want Anne no longer a candidate", got)
	}
	r = search(o, "anne")
	r.JSON(&found)
	if len(found.Users) != 1 || found.Users[0].Holder == nil || found.Users[0].Holder.Kind != "expected" ||
		found.Users[0].Holder.UserID == nil || *found.Users[0].Holder.UserID != anne {
		t.Errorf("search anne after the announcement: %+v, want held by her announced link", found.Users)
	}
}

// More than 1000 users waiting: the first 1000 are read, truncated says so.
func TestDirectoryMatches_TruncateTheCandidates(t *testing.T) {
	t.Parallel()
	h, _, o := directoryHarness(t)
	h.Exec(t, `INSERT INTO identity.users (id, email, normalized_email, display_name, version, created_at, updated_at)
	           SELECT gen_random_uuid(), 'bulk-' || n || '@nordlys.no', upper('bulk-' || n || '@nordlys.no'), 'bulk-' || n, gen_random_uuid(), $1, $1
	           FROM generate_series(1, 1001) AS n`, h.Now())
	r := o.Do(http.MethodGet, matchesPath, nil, identityOperation)
	var got dirMatches
	r.JSON(&got)
	if r.Status != http.StatusOK || got.Candidates != 1000 || !got.Truncated || len(got.Matches) != 0 {
		t.Errorf("matches: %d candidates %d truncated %v matches %d, want 1000, true, 0", r.Status, got.Candidates, got.Truncated, len(got.Matches))
	}
}

// An organisation of more than 10,000 accounts: twenty pages are read,
// truncated says so, and the employee number is not matched on — a second
// account holding it may be in the pages not read — while an address,
// unique in Google, still is.
func TestDirectoryMatches_TruncateTheDirectory(t *testing.T) {
	t.Parallel()
	h, g, o := directoryHarness(t)
	// The account holding the number is among the first read.
	person(g, "Ber", "Nummer", "nummer@nordlys.no", "E-9")
	for i := range 10_001 {
		g.AddUser(googletest.User{GivenName: "Bulk", FamilyName: fmt.Sprint(i), PrimaryEmail: fmt.Sprintf("bulk%05d@nordlys.no", i)})
	}
	anne := addUser(t, h, "bulk00003@nordlys.no", "")
	addUser(t, h, "someone@other.example", "E-9")

	before := len(listCalls(g))
	r := o.Do(http.MethodGet, matchesPath, nil, identityOperation)
	if r.Status != http.StatusOK {
		t.Fatalf("matches: %d %s", r.Status, r.Body)
	}
	var got dirMatches
	r.JSON(&got)
	// Three: the two above and the Owner who configured the connection.
	if !got.Truncated || got.Candidates != 3 || len(got.Matches) != 1 || got.Matches[0].UserID != anne {
		t.Errorf("matches: %+v, want truncated with Anne's address match alone", got)
	}
	if n := len(listCalls(g)) - before; n != 20 {
		t.Errorf("%d pages read, want 20", n)
	}
}

// While single sign-on is off there is no directory: 409 no_directory, and
// Google is not asked. While Entra is the provider, Google's search is not
// the one asked either.
func TestDirectorySearch_OnlyWhileGoogleIsTheProvider(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	o, _ := owner(t, l.h)
	addUser(t, l.h, "anne@nordlys.no", "")
	before := len(listCalls(l.g))
	if r := o.Do(http.MethodGet, matchesPath, nil, identityOperation); r.Status != http.StatusConflict || identityCode(r) != "no_directory" {
		t.Errorf("matches without SSO: %d %s, want 409 no_directory", r.Status, r.Body)
	}
	if r := search(o, "anne"); r.Status != http.StatusConflict || identityCode(r) != "no_directory" {
		t.Errorf("a search without SSO: %d %s, want 409 no_directory", r.Status, r.Body)
	}
	if len(listCalls(l.g)) != before {
		t.Error("Google was asked without SSO")
	}
}

// Google that cannot be asked — no key or admin, the scope not delegated,
// Google down — is 503 provider_unreachable.
func TestDirectorySearch_GoogleUnavailableIs503(t *testing.T) {
	t.Parallel()
	t.Run("not configured", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		enableGoogleSSO(t, h)
		o, _ := owner(t, h)
		addUser(t, h, "anne@nordlys.no", "")
		for _, r := range []*modtest.Response{search(o, "anne"), o.Do(http.MethodGet, matchesPath, nil, identityOperation)} {
			if r.Status != http.StatusServiceUnavailable || identityCode(r) != "provider_unreachable" {
				t.Errorf("unconfigured: %d %s, want 503 provider_unreachable", r.Status, r.Body)
			}
		}
	})
	t.Run("the scope not delegated", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.g.Delegate(saClientID, googletest.ScopeCustomer, googletest.ScopeDomains)
		configured(t, h)
		enableGoogleSSO(t, h)
		o, _ := owner(t, h)
		if r := search(o, "anne"); r.Status != http.StatusServiceUnavailable || identityCode(r) != "provider_unreachable" {
			t.Errorf("not delegated: %d %s, want 503 provider_unreachable", r.Status, r.Body)
		}
	})
	t.Run("Google down", func(t *testing.T) {
		t.Parallel()
		h, g, o := directoryHarness(t)
		addUser(t, h, "anne@nordlys.no", "")
		g.FailNext(http.StatusServiceUnavailable, http.StatusServiceUnavailable)
		if r := o.Do(http.MethodGet, matchesPath, nil, identityOperation); r.Status != http.StatusServiceUnavailable || identityCode(r) != "provider_unreachable" {
			t.Errorf("Google down: %d %s, want 503 provider_unreachable", r.Status, r.Body)
		}
	})
}

// The Owner's single sign-on settings name Google's directory while Google
// is the configured provider.
func TestDirectorySearch_SettingsNameTheDirectory(t *testing.T) {
	t.Parallel()
	_, _, o := directoryHarness(t)
	r := o.Do(http.MethodGet, "/api/v1/identity/owner/sso", nil, identityOperation)
	var body struct {
		Directory *struct {
			Provider    string `json:"provider"`
			DisplayName string `json:"displayName"`
		} `json:"directory"`
	}
	r.JSON(&body)
	if r.Status != http.StatusOK || body.Directory == nil || body.Directory.Provider != "google" || body.Directory.DisplayName != "Google Workspace" {
		t.Errorf("settings: %d %s, want directory google, Google Workspace", r.Status, r.Body)
	}
}
