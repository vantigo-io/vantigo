package google_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/google"
	"github.com/vantigo-io/vantigo/server/internal/google/googletest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The lifecycle (Google Workspace design G17–G19): the provisioner executing
// Staff's decisions against the fake organisation — Prepare, Activate,
// Update, Deactivate, Delete, Account and RevealPass — with the correlation
// on the employee number, the address rule, the password shown once, the
// licences and the leaver's steps.

// staffActor is the accounts worker, the provisioner's usual caller.
var staffActor = contracts.Actor{System: contracts.SystemActorStaffAccounts}

// The licences the fake organisation holds.
const (
	licenceStandard = "Google-Apps/1010020028"
	licencePlus     = "Google-Apps/1010020025"
)

// lifecycle is a connected installation over the fake organisation, with
// its provisioner.
type lifecycle struct {
	h *harness
	g *googletest.FakeGoogle
	p contracts.IdentityProvisioner
}

// newLifecycle is an installation whose key is uploaded, whose admin is
// named, whose every scope is delegated and whose connection was tested.
func newLifecycle(t *testing.T, opts ...modtest.Option) *lifecycle {
	t.Helper()
	return lifecycleOn(t, newHarness(t, opts...))
}

// lifecycleOn connects h as newLifecycle does.
func lifecycleOn(t *testing.T, h *harness) *lifecycle {
	t.Helper()
	h.g.Delegate(saClientID, everyScope...)
	o, _ := configured(t, h)
	if r := testConnection(t, o); r.Status != "connected" {
		t.Fatalf("the test: %+v", r)
	}
	h.g.Products([]string{"Google-Apps"}, map[string]string{
		licenceStandard: "Google Workspace Business Standard", licencePlus: "Google Workspace Business Plus",
	})
	return &lifecycle{h: h, g: h.g, p: google.Module().IdentityProvisioner(h.Deps())}
}

// joiner is a person's spec as Staff sends it.
func joiner(number string) contracts.ExternalSpec {
	return contracts.ExternalSpec{
		EmployeeNumber: number, GivenName: "Kari", FamilyName: "Nordmann", DisplayName: "Kari Nordmann",
		WorkEmail: "kari.nordmann@nordlys.no", PreferredLanguage: "nb", WorkCountry: "NO",
		Fields: map[string]string{
			"jobTitle": "Konsulent", "department": "Rådgivning", "costCenter": "4200", "division": "Øst",
			"businessPhone": "+47 22 00 00 00", "employeeType": "Fast",
		},
		Licences: []string{licenceStandard},
	}
}

func (l *lifecycle) prepare(t *testing.T, spec contracts.ExternalSpec) contracts.ExternalAccount {
	t.Helper()
	acc, err := l.p.Prepare(context.Background(), staffActor, spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return acc
}

func (l *lifecycle) activate(t *testing.T, id string, spec contracts.ExternalSpec) contracts.ExternalAccount {
	t.Helper()
	acc, err := l.p.Activate(context.Background(), staffActor, id, spec)
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	return acc
}

func (l *lifecycle) user(t *testing.T, id string) googletest.User {
	t.Helper()
	u, ok := l.g.User(id)
	if !ok {
		t.Fatalf("the organisation holds no user %s", id)
	}
	return u
}

// events are the module's events of action, oldest first, as their codes.
func (l *lifecycle) events(t *testing.T, action string) [][]string {
	t.Helper()
	rows, err := l.h.Pool().Query(context.Background(), `SELECT codes FROM google.events WHERE action = $1 ORDER BY id`, action)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out [][]string
	for rows.Next() {
		var codes []string
		if err := rows.Scan(&codes); err != nil {
			t.Fatal(err)
		}
		out = append(out, codes)
	}
	return out
}

// writes are the calls to Google's APIs (not the token endpoint) from the
// from-th on that are not reads, as "METHOD path".
func (l *lifecycle) writes(from int) []string {
	var out []string
	for _, c := range l.g.Calls()[from:] {
		if c.Host != googletest.TokenHost && c.Method != http.MethodGet {
			out = append(out, c.Method+" "+c.Path)
		}
	}
	return out
}

// orgID is an organization external id.
func orgID(number string) map[string]any {
	return map[string]any{"type": "organization", "value": number}
}

// --- Prepare ------------------------------------------------------------------

func TestPrepare_CreatesTheUserSuspendedWithTheWorkEmail(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	l.h.Exec(t, `UPDATE google.connection SET org_unit_path = '/Ansatte'`)
	acc := l.prepare(t, joiner("E1042"))
	if !acc.Created || acc.State != "disabled" || acc.Email != "kari.nordmann@nordlys.no" || acc.ExternalID == "" || acc.HasPass {
		t.Fatalf("Prepare answered %+v", acc)
	}
	u := l.user(t, acc.ExternalID)
	if !u.Suspended || !u.ChangePasswordAtNextLogin || len(u.Password) != 32 || u.OrgUnitPath != "/Ansatte" ||
		u.GivenName != "Kari" || u.FamilyName != "Nordmann" {
		t.Errorf("the user made: %+v", u)
	}
	if len(u.ExternalIDs) != 1 || u.ExternalIDs[0]["type"] != "organization" || u.ExternalIDs[0]["value"] != "E1042" {
		t.Errorf("the external ids: %v", u.ExternalIDs)
	}
	if len(u.Organizations) != 1 || u.Organizations[0]["title"] != "Konsulent" || u.Organizations[0]["department"] != "Rådgivning" ||
		u.Organizations[0]["costCenter"] != "4200" || u.Organizations[0]["primary"] != true {
		t.Errorf("the organisations: %v", u.Organizations)
	}
	if len(u.Phones) != 1 || u.Phones[0]["type"] != "work" || u.Phones[0]["value"] != "+47 22 00 00 00" {
		t.Errorf("the phones: %v", u.Phones)
	}
	if len(u.Languages) != 1 || u.Languages[0]["languageCode"] != "no" {
		t.Errorf("the languages: %v", u.Languages)
	}
	// The division and the employee type have no attribute at Google: ignored,
	// and not reported.
	if len(acc.Reported) != 0 {
		t.Errorf("reported %v", acc.Reported)
	}
	created := modtest.One[bool](t, l.h.Harness, `SELECT created_by_us FROM google.accounts WHERE user_id = $1`, acc.ExternalID)
	if !created {
		t.Error("the row does not say the module created the user")
	}
	if ev := l.events(t, "google.user.prepared"); len(ev) != 1 || ev[0][0] != "created" {
		t.Errorf("the events: %v", ev)
	}
	// The create's password is nowhere but at Google.
	if strings.Contains(l.h.Logs(), u.Password) || l.h.Count(t, `SELECT count(*) FROM google.accounts WHERE pass_sealed IS NOT NULL`) != 0 {
		t.Error("the create's password was kept")
	}
}

func TestPrepare_ThePatternAndItsSuffixes(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	l.g.AddUser(googletest.User{PrimaryEmail: "ola.nordmann@nordlys.no", GivenName: "Ola", FamilyName: "Nordmann"})
	spec := joiner("E2001")
	spec.GivenName, spec.WorkEmail = "Ola", "ola+hr@nordlys.no"
	acc := l.prepare(t, spec)
	if acc.Email != "ola.nordmann2@nordlys.no" {
		t.Errorf("the address = %q, want the pattern's second", acc.Email)
	}
	// A work email another account holds is taken: no suffix is tried.
	spec = joiner("E2002")
	spec.WorkEmail = "ola.nordmann@nordlys.no"
	if _, err := l.p.Prepare(context.Background(), staffActor, spec); !errors.Is(err, contracts.ErrExternalAddressTaken) {
		t.Errorf("a taken work email: %v", err)
	}
	// The account rules' domain and pattern.
	l.h.Exec(t, `UPDATE google.connection SET address_domain = 'nordlys.example', address_pattern = 'g.family'`)
	spec = joiner("E2003")
	spec.GivenName, spec.FamilyName, spec.WorkEmail = "Ørjan", "Åsmundsen", ""
	if acc := l.prepare(t, spec); acc.Email != "o.asmundsen@nordlys.example" {
		t.Errorf("the account rules' address = %q", acc.Email)
	}
}

func TestPrepare_FindsTheNumberOnlyAsAnOrganizationIDEqualToIt(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	// What the search answers for E1042 (U11): a prefix, another type, and
	// the user whose organization id it is, in another case.
	l.g.AddUser(googletest.User{PrimaryEmail: "prefix@nordlys.no", ExternalIDs: []map[string]any{orgID("E10420")}})
	l.g.AddUser(googletest.User{PrimaryEmail: "custom@nordlys.no", ExternalIDs: []map[string]any{{"type": "custom", "customType": "badge", "value": "E1042"}}})
	kari := l.g.AddUser(googletest.User{PrimaryEmail: "kari@nordlys.no", GivenName: "Kari", FamilyName: "N", ExternalIDs: []map[string]any{orgID("e1042")}})
	_, err := l.p.Prepare(context.Background(), staffActor, joiner("E1042"))
	if !errors.Is(err, contracts.ErrExternalUnconfirmed) {
		t.Fatalf("Prepare of a found user: %v, want ErrExternalUnconfirmed", err)
	}
	row := modtest.One[string](t, l.h.Harness, `SELECT user_id FROM google.accounts WHERE employee_number = 'E1042'`)
	if row != kari.ID {
		t.Errorf("linked %s, want Kari's %s", row, kari.ID)
	}
	// The search was asked by the external id.
	asked := slices.ContainsFunc(l.g.Calls(), func(c googletest.Call) bool {
		return c.Path == "/admin/directory/v1/users" && strings.Contains(c.Query, "query=externalId%3AE1042") && strings.Contains(c.Query, "customer=my_customer")
	})
	if !asked {
		t.Error("the search was not users.list(customer=my_customer, query=externalId:E1042)")
	}
}

func TestPrepare_AFoundUserIsAdoptedUntrustedUntilConfirmed(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	found := l.g.AddUser(googletest.User{PrimaryEmail: "kari@nordlys.no", GivenName: "Kari", FamilyName: "N", ExternalIDs: []map[string]any{orgID("E1042")}})
	from := len(l.g.Calls())
	if _, err := l.p.Prepare(context.Background(), staffActor, joiner("E1042")); !errors.Is(err, contracts.ErrExternalUnconfirmed) {
		t.Fatalf("Prepare: %v", err)
	}
	if w := l.writes(from); len(w) != 0 {
		t.Errorf("an unconfirmed adoption was written to: %v", w)
	}
	acc, err := l.p.Account(context.Background(), found.ID)
	if err != nil || acc == nil || !slices.Contains(acc.Reported, "adoption_unconfirmed") {
		t.Fatalf("Account: %+v %v", acc, err)
	}
	for name, call := range map[string]func() error{
		"Activate": func() error {
			_, err := l.p.Activate(context.Background(), staffActor, found.ID, joiner("E1042"))
			return err
		},
		"Deactivate": func() error {
			_, err := l.p.Deactivate(context.Background(), staffActor, found.ID, joiner("E1042"))
			return err
		},
		"Delete": func() error { return l.p.Delete(context.Background(), staffActor, found.ID) },
	} {
		if err := call(); !errors.Is(err, contracts.ErrExternalUnconfirmed) {
			t.Errorf("%s of an unconfirmed adoption: %v", name, err)
		}
	}
	m, _ := manager(t, l.h)
	if r := m.Do(http.MethodPost, "/api/v1/google/accounts/"+found.ID+"/confirm", nil); r.Status != http.StatusOK {
		t.Fatalf("confirm: %d %s", r.Status, r.Body)
	}
	if r := m.Do(http.MethodPost, "/api/v1/google/accounts/"+found.ID+"/confirm", nil); r.Status != http.StatusConflict || codeOf(t, r) != "adoption_confirmed" {
		t.Errorf("confirm again: %d %s", r.Status, r.Body)
	}
	if r := m.Do(http.MethodPost, "/api/v1/google/accounts/999/confirm", nil); r.Status != http.StatusNotFound {
		t.Errorf("confirm an unknown account: %d", r.Status)
	}
	acc2 := l.prepare(t, joiner("E1042"))
	if acc2.Created || acc2.ExternalID != found.ID || slices.Contains(acc2.Reported, "adoption_unconfirmed") {
		t.Errorf("Prepare after the confirmation: %+v", acc2)
	}
	if u := l.user(t, found.ID); u.FamilyName != "Nordmann" || len(u.Organizations) != 1 {
		t.Errorf("the adopted user was not brought to the spec: %+v", u)
	}
}

func TestPrepare_TwoUsersHoldingTheNumberAreAmbiguous(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	l.g.AddUser(googletest.User{PrimaryEmail: "a@nordlys.no", ExternalIDs: []map[string]any{orgID("E1042")}})
	l.g.AddUser(googletest.User{PrimaryEmail: "b@nordlys.no", ExternalIDs: []map[string]any{orgID("E1042")}})
	if _, err := l.p.Prepare(context.Background(), staffActor, joiner("E1042")); !errors.Is(err, contracts.ErrExternalAmbiguous) {
		t.Errorf("Prepare: %v", err)
	}
}

func TestPrepare_AnAdministratorIsPrivileged(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	l.g.AddUser(googletest.User{PrimaryEmail: "boss@nordlys.no", IsAdmin: true, ExternalIDs: []map[string]any{orgID("E1042")}})
	if _, err := l.p.Prepare(context.Background(), staffActor, joiner("E1042")); !errors.Is(err, contracts.ErrExternalPrivileged) {
		t.Errorf("Prepare: %v", err)
	}
}

// loseInsert answers the first users.insert with a 503 after the fake
// carried it out: the user is made, the answer lost.
type loseInsert struct {
	inner http.RoundTripper
	lost  atomic.Bool
}

func (w *loseInsert) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := w.inner.RoundTrip(req)
	if err != nil || req.Method != http.MethodPost || req.URL.Path != "/admin/directory/v1/users" || !w.lost.CompareAndSwap(false, true) {
		return resp, err
	}
	_ = resp.Body.Close()
	body := `{"error":{"code":503,"message":"lost","errors":[{"reason":"backendError"}]}}`
	return &http.Response{
		StatusCode: http.StatusServiceUnavailable, Status: "503", Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req,
	}, nil
}

func TestPrepare_ACreateWhoseAnswerIsLostIsReadBack(t *testing.T) {
	t.Parallel()
	w := &loseInsert{}
	h := newHarness(t, modtest.WithTransport(w))
	w.inner = h.g
	l := lifecycleOn(t, h)
	acc := l.prepare(t, joiner("E1042"))
	if !acc.Created || acc.ExternalID == "" {
		t.Fatalf("Prepare: %+v", acc)
	}
	inserts := 0
	for _, c := range l.g.Calls() {
		if c.Method == http.MethodPost && c.Path == "/admin/directory/v1/users" {
			inserts++
		}
	}
	if inserts != 1 || !w.lost.Load() {
		t.Errorf("%d inserts (lost %v), want the one: a create is never made twice", inserts, w.lost.Load())
	}
	if again := l.prepare(t, joiner("E1042")); again.Created || again.ExternalID != acc.ExternalID {
		t.Errorf("the next Prepare adopted the module's own user: %+v", again)
	}
}

// --- Activate and the password ------------------------------------------------

func TestActivate_LiftsTheSuspensionWithAPasswordShownOnce(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	acc := l.prepare(t, joiner("E1042"))
	acc = l.activate(t, acc.ExternalID, joiner("E1042"))
	if acc.State != "enabled" || !acc.HasPass {
		t.Fatalf("Activate answered %+v", acc)
	}
	u := l.user(t, acc.ExternalID)
	if u.Suspended || !u.ChangePasswordAtNextLogin || len(u.Password) != 16 {
		t.Errorf("the user after Activate: %+v", u)
	}
	if !l.g.Holds(licenceStandard, u.PrimaryEmail) {
		t.Error("the group's licence was not assigned")
	}
	pass, err := l.p.RevealPass(context.Background(), staffActor, acc.ExternalID)
	if err != nil || pass.Value != u.Password || !pass.ExpiresAt.Equal(l.h.Now().Add(7*24*time.Hour)) {
		t.Fatalf("RevealPass: %+v %v", pass, err)
	}
	if _, err := l.p.RevealPass(context.Background(), staffActor, acc.ExternalID); !errors.Is(err, contracts.ErrPassGone) {
		t.Errorf("a second reveal: %v", err)
	}
	if strings.Contains(l.h.Logs(), u.Password) {
		t.Error("the log carries the password")
	}
	if ev := l.events(t, "google.pass.revealed"); len(ev) != 1 || strings.Contains(strings.Join(ev[0], " "), u.Password) {
		t.Errorf("the reveal's events: %v", ev)
	}
	// Activate again: nothing to do, no new password over a working
	// account.
	before := l.user(t, acc.ExternalID).Password
	l.activate(t, acc.ExternalID, joiner("E1042"))
	if l.user(t, acc.ExternalID).Password != before {
		t.Error("a second Activate set a new password")
	}
}

func TestActivate_RunNowSetsAFreshPasswordOnlyBeforeTheFirstSignIn(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	acc := l.prepare(t, joiner("E1042"))
	l.activate(t, acc.ExternalID, joiner("E1042"))
	first := l.user(t, acc.ExternalID).Password
	fresh := joiner("E1042")
	fresh.Flags.Fresh = true
	l.activate(t, acc.ExternalID, fresh)
	second := l.user(t, acc.ExternalID).Password
	if second == first {
		t.Fatal("Run now before the first sign-in set no new password")
	}
	l.g.SetUser(acc.ExternalID, func(u *googletest.User) { u.LastLoginTime = "2026-09-13T08:00:00.000Z" })
	l.activate(t, acc.ExternalID, fresh)
	if l.user(t, acc.ExternalID).Password != second {
		t.Error("Run now after a sign-in set a new password over a working account")
	}
}

func TestActivate_APasswordThePolicyRefusesIsReported(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	acc := l.prepare(t, joiner("E1042"))
	l.g.Configure(func(g *googletest.FakeGoogle) { g.RefusePassword = true })
	acc = l.activate(t, acc.ExternalID, joiner("E1042"))
	if acc.State != "enabled" || acc.HasPass || !slices.Contains(acc.Reported, "pass_not_allowed") {
		t.Errorf("Activate answered %+v", acc)
	}
}

func TestActivate_ALicenceNoneIsLeftOfIsReported(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	l.g.Seats(licencePlus, 0)
	spec := joiner("E1042")
	spec.Licences = []string{licenceStandard, licencePlus}
	acc := l.prepare(t, spec)
	acc = l.activate(t, acc.ExternalID, spec)
	if !slices.Contains(acc.Reported, "licence_unavailable:1010020025") || slices.Contains(acc.Reported, "licence_unavailable:1010020028") {
		t.Errorf("reported %v", acc.Reported)
	}
	if !l.g.Holds(licenceStandard, acc.Email) {
		t.Error("the licence left was not assigned")
	}
}

func TestRevealPass_GoneAfterItsLifetime(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	l.h.Exec(t, `UPDATE google.connection SET pass_lifetime_days = 1`)
	acc := l.prepare(t, joiner("E1042"))
	l.activate(t, acc.ExternalID, joiner("E1042"))
	l.h.Advance(25 * time.Hour)
	if _, err := l.p.RevealPass(context.Background(), staffActor, acc.ExternalID); !errors.Is(err, contracts.ErrPassGone) {
		t.Errorf("a reveal after the lifetime: %v", err)
	}
	if n := l.h.Count(t, `SELECT count(*) FROM google.accounts WHERE pass_sealed IS NOT NULL`); n != 0 {
		t.Errorf("%d passwords kept after their lifetime", n)
	}
}

// --- Update -------------------------------------------------------------------

func TestUpdate_WritesWhatChangedAndTheManager(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	boss := l.prepare(t, func() contracts.ExternalSpec {
		s := joiner("E0001")
		s.GivenName, s.WorkEmail = "Siri", "siri@nordlys.no"
		return s
	}())
	acc := l.prepare(t, joiner("E1042"))
	l.activate(t, acc.ExternalID, joiner("E1042"))
	// Somebody's own phone of another type stays.
	l.g.SetUser(acc.ExternalID, func(u *googletest.User) {
		u.Phones = append(u.Phones, map[string]any{"type": "home", "value": "+47 99 99 99 99"})
	})
	spec := joiner("E1042")
	spec.Fields["jobTitle"] = "Seniorkonsulent"
	spec.Fields["mobilePhone"] = "+47 900 00 000"
	spec.ManagerExternalID = boss.ExternalID
	from := len(l.g.Calls())
	got, err := l.p.Update(context.Background(), staffActor, acc.ExternalID, spec)
	if err != nil {
		t.Fatal(err)
	}
	u := l.user(t, acc.ExternalID)
	if u.Organizations[0]["title"] != "Seniorkonsulent" || u.Organizations[0]["department"] != "Rådgivning" {
		t.Errorf("the organisations: %v", u.Organizations)
	}
	if len(u.Relations) != 1 || u.Relations[0]["type"] != "manager" || u.Relations[0]["value"] != "siri@nordlys.no" {
		t.Errorf("the manager: %v", u.Relations)
	}
	types := []string{}
	for _, p := range u.Phones {
		types = append(types, p["type"].(string))
	}
	if !slices.Contains(types, "home") || !slices.Contains(types, "mobile") || !slices.Contains(types, "work") {
		t.Errorf("the phones: %v", u.Phones)
	}
	if u.PrimaryEmail != "kari.nordmann@nordlys.no" || len(got.Reported) != 0 {
		t.Errorf("Update answered %+v", got)
	}
	if w := l.writes(from); len(w) != 1 || !strings.HasPrefix(w[0], "PATCH /admin/directory/v1/users/") {
		t.Errorf("Update's writes: %v, want one patch", w)
	}
	// The same spec again writes nothing.
	from = len(l.g.Calls())
	if _, err := l.p.Update(context.Background(), staffActor, acc.ExternalID, spec); err != nil {
		t.Fatal(err)
	}
	if w := l.writes(from); len(w) != 0 {
		t.Errorf("an Update with nothing changed wrote %v", w)
	}
	// A manager the organisation does not hold is reported.
	spec.ManagerExternalID = "123456789"
	got, err = l.p.Update(context.Background(), staffActor, acc.ExternalID, spec)
	if err != nil || !slices.Contains(got.Reported, "manager_not_found") {
		t.Errorf("an unknown manager: %+v %v", got, err)
	}
}

func TestUpdate_TheLicencesTheModuleAssignedFollowTheGroups(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	acc := l.prepare(t, joiner("E1042"))
	l.activate(t, acc.ExternalID, joiner("E1042"))
	// An administrator's own licence is the organisation's.
	l.g.Assign(licencePlus, acc.Email)
	spec := joiner("E1042")
	spec.Licences = nil
	if _, err := l.p.Update(context.Background(), staffActor, acc.ExternalID, spec); err != nil {
		t.Fatal(err)
	}
	if l.g.Holds(licenceStandard, acc.Email) || !l.g.Holds(licencePlus, acc.Email) {
		t.Errorf("after the group lost its licence: standard %v plus %v", l.g.Holds(licenceStandard, acc.Email), l.g.Holds(licencePlus, acc.Email))
	}
}

// --- Deactivate, Delete, Account ---------------------------------------------

func TestDeactivate_SuspendsThenTheLeaversSteps(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	acc := l.prepare(t, joiner("E1042"))
	l.activate(t, acc.ExternalID, joiner("E1042"))
	// A licence an administrator assigned, of the organisation's cached
	// ones, and a group the module made beside one it did not.
	l.g.Assign(licencePlus, acc.Email)
	m, _ := manager(t, l.h)
	if r := m.Do(http.MethodGet, "/api/v1/google/connection/skus", nil); r.Status != http.StatusOK {
		t.Fatalf("GET /connection/skus: %d %s", r.Status, r.Body)
	}
	ours, err := l.p.EnsureGroup(context.Background(), staffActor, contracts.GroupSpec{GroupID: "7", Key: "radgivning", Name: "Rådgivning"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.p.SetMembers(context.Background(), staffActor, "7", []string{acc.ExternalID}); err != nil {
		t.Fatal(err)
	}
	theirs := l.g.AddGroup(googletest.Group{Email: "alle@nordlys.no", Members: map[string]string{acc.ExternalID: "MEMBER"}})

	got, err := l.p.Deactivate(context.Background(), staffActor, acc.ExternalID, joiner("E1042"))
	if err != nil {
		t.Fatal(err)
	}
	u := l.user(t, acc.ExternalID)
	if !u.Suspended || got.State != "disabled" || u.SignedOut != 1 || len(got.Reported) != 0 {
		t.Errorf("Deactivate answered %+v; the user %+v", got, u)
	}
	if l.g.Holds(licenceStandard, acc.Email) || l.g.Holds(licencePlus, acc.Email) {
		t.Error("a leaver kept a licence")
	}
	if g, _ := l.g.Group(ours.ExternalID); len(g.Members) != 0 {
		t.Errorf("the leaver is still in the module's group: %v", g.Members)
	}
	if g, _ := l.g.Group(theirs.ID); len(g.Members) != 1 {
		t.Errorf("managed removed the leaver from a group the module did not make: %v", g.Members)
	}
	// A rehire's Activate later; meanwhile Deactivate again is idempotent.
	if _, err := l.p.Deactivate(context.Background(), staffActor, acc.ExternalID, joiner("E1042")); err != nil {
		t.Errorf("Deactivate again: %v", err)
	}
	// all: every group.
	l.h.Exec(t, `UPDATE google.connection SET leaver_remove_groups = 'all'`)
	if _, err := l.p.Deactivate(context.Background(), staffActor, acc.ExternalID, joiner("E1042")); err != nil {
		t.Fatal(err)
	}
	if g, _ := l.g.Group(theirs.ID); len(g.Members) != 0 {
		t.Errorf("all left the leaver in a group: %v", g.Members)
	}
}

func TestDeactivate_ASignOutRefusedIsReported(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	acc := l.prepare(t, joiner("E1042"))
	l.activate(t, acc.ExternalID, joiner("E1042"))
	l.g.Configure(func(g *googletest.FakeGoogle) { g.RefuseSignOut = true })
	got, err := l.p.Deactivate(context.Background(), staffActor, acc.ExternalID, joiner("E1042"))
	if err != nil || got.State != "disabled" || !slices.Contains(got.Reported, "sign_out_refused") {
		t.Errorf("Deactivate: %+v %v", got, err)
	}
	if l.g.Holds(licenceStandard, acc.Email) {
		t.Error("the licence stayed")
	}
}

func TestDeactivate_AFailedStepIsReportedTheUserSuspended(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	acc := l.prepare(t, joiner("E1042"))
	l.activate(t, acc.ExternalID, joiner("E1042"))
	l.g.FailOn("DELETE /user/"+acc.Email, http.StatusInternalServerError)
	got, err := l.p.Deactivate(context.Background(), staffActor, acc.ExternalID, joiner("E1042"))
	if err != nil || !slices.Contains(got.Reported, "leaver_step_failed:remove_licences") || !l.user(t, acc.ExternalID).Suspended {
		t.Errorf("Deactivate: %+v %v", got, err)
	}
	if l.user(t, acc.ExternalID).SignedOut != 0 {
		t.Error("a step after the failed one ran")
	}
	if strings.Contains(l.h.Logs(), acc.Email) {
		t.Error("the log names the leaver's address")
	}
}

func TestDelete_RefusesAnActiveUserThenDeletes(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	acc := l.prepare(t, joiner("E1042"))
	l.activate(t, acc.ExternalID, joiner("E1042"))
	if err := l.p.Delete(context.Background(), staffActor, acc.ExternalID); !errors.Is(err, contracts.ErrExternalEnabled) {
		t.Fatalf("Delete of an active user: %v", err)
	}
	if _, err := l.p.Deactivate(context.Background(), staffActor, acc.ExternalID, joiner("E1042")); err != nil {
		t.Fatal(err)
	}
	if err := l.p.Delete(context.Background(), staffActor, acc.ExternalID); err != nil {
		t.Fatal(err)
	}
	if _, ok := l.g.User(acc.ExternalID); ok {
		t.Error("the user is still there")
	}
	got, err := l.p.Account(context.Background(), acc.ExternalID)
	if err != nil || got != nil {
		t.Errorf("Account of a deleted user: %+v %v", got, err)
	}
	if state := modtest.One[string](t, l.h.Harness, `SELECT state FROM google.accounts WHERE user_id = $1`, acc.ExternalID); state != "deleted" {
		t.Errorf("the row's state = %q", state)
	}
	// A rehire after the delete gets a new user.
	again := l.prepare(t, joiner("E1042"))
	if !again.Created || again.ExternalID == acc.ExternalID {
		t.Errorf("the rehire's Prepare: %+v", again)
	}
}

func TestAccount_ReadsTheSuspensionAndAnswersNilWhenGone(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	acc := l.prepare(t, joiner("E1042"))
	got, err := l.p.Account(context.Background(), acc.ExternalID)
	if err != nil || got == nil || got.State != "disabled" {
		t.Fatalf("Account: %+v %v", got, err)
	}
	l.g.SetUser(acc.ExternalID, func(u *googletest.User) { u.Suspended = false })
	if got, _ := l.p.Account(context.Background(), acc.ExternalID); got.State != "enabled" {
		t.Errorf("Account after a change outside Vantigo: %+v", got)
	}
	l.g.DeleteUser(acc.ExternalID)
	if got, err := l.p.Account(context.Background(), acc.ExternalID); got != nil || err != nil {
		t.Errorf("Account of a user gone: %+v %v", got, err)
	}
	if got, err := l.p.Account(context.Background(), "not-an-id"); got != nil || err != nil {
		t.Errorf("Account of no id: %+v %v", got, err)
	}
}

// --- The status ---------------------------------------------------------------

func TestProvider_TheStatusStaffReads(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	p := google.Module().IdentityProvisioner(h.Deps())
	if p.Key() != "google" {
		t.Fatalf("Key = %q", p.Key())
	}
	status := func() contracts.ProviderInfo {
		info, err := p.Provider(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	if info := status(); info.Status != contracts.ProviderUnconfigured || info.Name != "Google Workspace" {
		t.Errorf("a fresh installation: %+v", info)
	}
	o, _ := configured(t, h)
	testConnection(t, o)
	if info := status(); info.Status != contracts.ProviderUnconfigured || info.Reason != "not_delegated" {
		t.Errorf("pending delegation: %+v", info)
	}
	h.g.Delegate(saClientID, googletest.ScopeCustomer, googletest.ScopeDomains)
	testConnection(t, o)
	if info := status(); info.Status != contracts.ProviderFailing || info.Reason != "scopes_missing" {
		t.Errorf("A's scopes alone: %+v", info)
	}
	if _, err := p.Prepare(context.Background(), staffActor, joiner("E1042")); !errors.Is(err, contracts.ErrProviderUnavailable) {
		t.Errorf("Prepare while failing: %v", err)
	}
	h.g.Delegate(saClientID, everyScope...)
	testConnection(t, o)
	if info := status(); info.Status != contracts.ProviderConnected || info.Reason != "" {
		t.Errorf("every scope: %+v", info)
	}
}

func TestProvisioner_RefusesAnInvalidActor(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	var none contracts.Actor
	if _, err := l.p.Prepare(context.Background(), none, joiner("E1042")); !errors.Is(err, contracts.ErrInvalidActor) {
		t.Errorf("Prepare: %v", err)
	}
	if _, err := l.p.RevealPass(context.Background(), none, "1"); !errors.Is(err, contracts.ErrInvalidActor) {
		t.Errorf("RevealPass: %v", err)
	}
	if err := l.p.DeleteGroup(context.Background(), none, "1"); !errors.Is(err, contracts.ErrInvalidActor) {
		t.Errorf("DeleteGroup: %v", err)
	}
}

func TestLifecycle_NoAddressOrGooglesWordsInTheLogOrTheEvents(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	l.g.FailOn("POST /signOut", http.StatusForbidden)
	acc := l.prepare(t, joiner("E1042"))
	l.activate(t, acc.ExternalID, joiner("E1042"))
	if _, err := l.p.Deactivate(context.Background(), staffActor, acc.ExternalID, joiner("E1042")); err != nil {
		t.Fatal(err)
	}
	logs := l.h.Logs()
	if strings.Contains(logs, acc.Email) || strings.Contains(logs, l.g.Words) {
		t.Error("the log carries an address or Google's words")
	}
	events := modtest.One[string](t, l.h.Harness, `SELECT coalesce(string_agg(array_to_string(codes, ' ') || ' ' || coalesce(subject_id, ''), ' '), '') FROM google.events`)
	if strings.Contains(events, "@") || strings.Contains(events, l.g.Words) {
		t.Errorf("the events carry an address or Google's words: %s", events)
	}
}
