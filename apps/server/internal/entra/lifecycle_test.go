package entra_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The lifecycle (plan task 4, Entra design D13): the provisioner executing
// Staff's decisions against the fake tenant — Prepare, Activate,
// Deactivate, Update, Delete, Account and RevealPass — with the address
// rule, the pass, the sensitive actions' streak and the module's events.

// staffActor is the accounts worker, the provisioner's usual caller.
var staffActor = contracts.Actor{System: contracts.SystemActorStaffAccounts}

// tap stands between the module and the fake tenant: it keeps the password
// of each create, so a test can prove it is stored and logged nowhere, and
// answers a Graph refusal for the calls a test names ("METHOD path-suffix"
// → status) without the fake learning a new trick.
type tap struct {
	inner     http.RoundTripper
	mu        sync.Mutex
	passwords []string
	fail      map[string]int
}

func (w *tap) RoundTrip(req *http.Request) (*http.Response, error) {
	w.mu.Lock()
	for key, status := range w.fail {
		method, suffix, _ := strings.Cut(key, " ")
		if req.Method == method && strings.HasSuffix(req.URL.Path, suffix) {
			w.mu.Unlock()
			body, _ := json.Marshal(map[string]any{"error": map[string]any{"code": "Authorization_RequestDenied", "message": "The tap refused this call."}})
			return &http.Response{
				StatusCode: status, Status: http.StatusText(status), Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
				Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(body)),
				ContentLength: int64(len(body)), Request: req,
			}, nil
		}
	}
	if req.Method == http.MethodPost && req.URL.Host == graphtest.GraphHost && req.URL.Path == "/v1.0/users" && req.Body != nil {
		data, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			w.mu.Unlock()
			return nil, err
		}
		var create struct {
			PasswordProfile struct {
				Password string `json:"password"`
			} `json:"passwordProfile"`
		}
		_ = json.Unmarshal(data, &create)
		w.passwords = append(w.passwords, create.PasswordProfile.Password)
		req = req.Clone(req.Context())
		req.Body = io.NopCloser(bytes.NewReader(data))
	}
	w.mu.Unlock()
	return w.inner.RoundTrip(req)
}

func (w *tap) failOn(key string, status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.fail[key] = status
}

func (w *tap) clear() {
	w.mu.Lock()
	defer w.mu.Unlock()
	clear(w.fail)
}

func (w *tap) createdPasswords() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.passwords)
}

// lifecycle is a connected installation over the fake tenant, its UPN domain
// nordlys.no and its business zone Europe/Oslo, with its provisioner.
type lifecycle struct {
	h *harness
	g *graphtest.FakeGraph
	w *tap
	p contracts.IdentityProvisioner
}

func newLifecycle(t *testing.T) *lifecycle {
	t.Helper()
	w := &tap{fail: map[string]int{}}
	h, g := newTenantHarness(t, modtest.WithTransport(w))
	w.inner = g
	h.SetBusinessTimeZone(t, "Europe/Oslo")
	connected(t, h)
	h.Exec(t, `UPDATE entra.connection SET upn_domain = 'nordlys.no'`)
	return &lifecycle{h: h, g: g, w: w, p: provider(h)}
}

// The days the specs name: a hire day in winter time (01:00 in Oslo is
// 00:00Z) and a leave day in summer time (23:00 in Oslo is 21:00Z).
var (
	hireDay  = time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	leaveDay = time.Date(2027, 3, 31, 0, 0, 0, 0, time.UTC)
)

// joiner is a person's spec as Staff sends it.
func joiner(number string) contracts.ExternalSpec {
	hire := hireDay
	return contracts.ExternalSpec{
		EmployeeNumber: number, GivenName: "Kari", FamilyName: "Nordmann", DisplayName: "Kari Nordmann",
		WorkEmail: "kari.nordmann@nordlys.no", PreferredLanguage: "nb", WorkCountry: "NO", HireDate: &hire,
		Fields: map[string]string{
			"jobTitle": "Konsulent", "department": "Rådgivning", "costCenter": "4200", "division": "Øst",
			"businessPhone": "+47 22 00 00 00",
		},
		Licences: []string{"ENTERPRISEPACK"},
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

func (l *lifecycle) deactivate(t *testing.T, id string, spec contracts.ExternalSpec) contracts.ExternalAccount {
	t.Helper()
	acc, err := l.p.Deactivate(context.Background(), staffActor, id, spec)
	if err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	return acc
}

func (l *lifecycle) user(t *testing.T, id string) graphtest.User {
	t.Helper()
	u, ok := l.g.User(id)
	if !ok {
		t.Fatalf("the tenant holds no user %s", id)
	}
	return u
}

// graphCalls are the calls to Graph (not the token endpoint) from the from-th
// on.
func (l *lifecycle) graphCalls(from int) []graphtest.Call {
	var out []graphtest.Call
	for _, c := range l.g.Calls()[from:] {
		if c.Host == graphtest.GraphHost {
			out = append(out, c)
		}
	}
	return out
}

// writes is calls as "METHOD last-segment", reads left out.
func writes(calls []graphtest.Call) []string {
	var out []string
	for _, c := range calls {
		if c.Method == http.MethodGet {
			continue
		}
		out = append(out, c.Method+" "+c.Path[strings.LastIndex(c.Path, "/")+1:])
	}
	return out
}

func countCalls(calls []graphtest.Call, method, suffix string) int {
	n := 0
	for _, c := range calls {
		if c.Method == method && strings.HasSuffix(c.Path, suffix) {
			n++
		}
	}
	return n
}

// events are the module's events of action, oldest first, as their codes.
func (l *lifecycle) events(t *testing.T, action string) [][]string {
	t.Helper()
	rows, err := l.h.Pool().Query(context.Background(), `SELECT codes FROM entra.events WHERE action = $1 ORDER BY id`, action)
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

func hasCode(codes []string, code string) bool { return slices.Contains(codes, code) }

// --- Prepare ------------------------------------------------------------------

func TestPrepare_CreatesTheUserDisabledWithTheWorkEmail(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	acc := l.prepare(t, joiner("E-1"))
	if !acc.Created || acc.State != "disabled" || acc.UserPrincipalName != "kari.nordmann@nordlys.no" ||
		acc.Email != "kari.nordmann@nordlys.no" || acc.HasPass || len(acc.Reported) != 0 {
		t.Errorf("Prepare answered %+v", acc)
	}
	u := l.user(t, acc.ExternalID)
	if u.AccountEnabled || u.UserPrincipalName != "kari.nordmann@nordlys.no" || u.MailNickname != "kari.nordmann" ||
		u.DisplayName != "Kari Nordmann" || u.GivenName != "Kari" || u.Surname != "Nordmann" || u.EmployeeID != "E-1" ||
		u.UsageLocation != "NO" || u.PreferredLanguage != "nb-NO" || !u.ForceChangePassword || u.JobTitle != "Konsulent" ||
		u.Department != "Rådgivning" || u.EmployeeOrgData == nil || *u.EmployeeOrgData != (graphtest.OrgData{CostCenter: "4200", Division: "Øst"}) ||
		!slices.Equal(u.BusinessPhones, []string{"+47 22 00 00 00"}) || u.Mail != "" || len(u.Licences) != 0 {
		t.Errorf("the user created: %+v", u)
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.accounts WHERE object_id = $1 AND employee_number = 'E-1'
	    AND user_principal_name = 'kari.nordmann@nordlys.no' AND state = 'disabled' AND created_by_us AND pass_sealed IS NULL`, acc.ExternalID); n != 1 {
		t.Errorf("the account's row: %d rows match", n)
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.events WHERE action = 'entra.user.prepared' AND object_id = $1
	    AND upn = 'kari.nordmann@nordlys.no' AND actor_system = $2 AND 'created' = ANY(codes)`, acc.ExternalID, contracts.SystemActorStaffAccounts); n != 1 {
		t.Errorf("the prepared event: %d rows match", n)
	}

	// Idempotent: the second call finds the user by its employee id.
	mark := len(l.g.Calls())
	again := l.prepare(t, joiner("E-1"))
	if again.Created || again.ExternalID != acc.ExternalID || again.State != "disabled" || again.UserPrincipalName != acc.UserPrincipalName {
		t.Errorf("the second Prepare answered %+v", again)
	}
	if n := len(l.g.Users()); n != 1 {
		t.Errorf("%d users in the tenant, want one", n)
	}
	if n := countCalls(l.graphCalls(mark), http.MethodPost, "/users"); n != 0 {
		t.Errorf("the second Prepare created %d users", n)
	}
	if n := countCalls(l.graphCalls(mark), http.MethodPatch, "/users/"+acc.ExternalID); n != 0 {
		t.Errorf("the second Prepare patched an unchanged user %d times", n)
	}
}

func TestPrepare_ThePatternWhenTheWorkEmailCannotBeTheAddress(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	spec := joiner("E-1")
	spec.WorkEmail = "kari@nordlys.example" // an unverified domain
	if acc := l.prepare(t, spec); acc.UserPrincipalName != "kari.nordmann@nordlys.no" || acc.Email != "kari.nordmann@nordlys.no" {
		t.Errorf("an unverified domain: %+v", acc)
	}

	folded := joiner("E-2")
	folded.WorkEmail, folded.GivenName, folded.FamilyName, folded.DisplayName = "", "Ørjan", "Åsmundsen", "Ørjan Åsmundsen"
	acc := l.prepare(t, folded)
	if acc.UserPrincipalName != "orjan.asmundsen@nordlys.no" {
		t.Errorf("Ørjan Åsmundsen's address: %q", acc.UserPrincipalName)
	}
	if u := l.user(t, acc.ExternalID); u.MailNickname != "orjan.asmundsen" || u.DisplayName != "Ørjan Åsmundsen" || u.GivenName != "Ørjan" {
		t.Errorf("the folded user: %+v", u)
	}
}

func TestPrepare_SuffixesAPatternOnConflict(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	l.g.AddUser(graphtest.User{DisplayName: "Kari N.", MailNickname: "kari.nordmann", UserPrincipalName: "kari.nordmann@nordlys.no", EmployeeID: "X-1"})
	l.g.AddUser(graphtest.User{DisplayName: "Kari N.", MailNickname: "kari.nordmann2", UserPrincipalName: "kari.nordmann2@nordlys.no", EmployeeID: "X-2"})
	spec := joiner("E-1")
	spec.WorkEmail = ""
	acc := l.prepare(t, spec)
	if acc.UserPrincipalName != "kari.nordmann3@nordlys.no" || !acc.Created {
		t.Errorf("the third Kari Nordmann: %+v", acc)
	}
	if u := l.user(t, acc.ExternalID); u.MailNickname != "kari.nordmann3" {
		t.Errorf("the mail nickname: %q", u.MailNickname)
	}
}

func TestPrepare_AWorkEmailHeldByAnotherIsAddressTaken(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	l.g.AddUser(graphtest.User{DisplayName: "Kari N.", MailNickname: "kari.nordmann", UserPrincipalName: "kari.nordmann@nordlys.no", EmployeeID: "X-1"})
	if _, err := l.p.Prepare(context.Background(), staffActor, joiner("E-1")); !errors.Is(err, contracts.ErrExternalAddressTaken) {
		t.Errorf("Prepare: %v, want ErrExternalAddressTaken", err)
	}
	if n := len(l.g.Users()); n != 1 {
		t.Errorf("%d users, want the one seeded", n)
	}
}

func TestPrepare_FindsTheUserByEmployeeID(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	seeded := l.g.AddUser(graphtest.User{
		DisplayName: "Kari Nordmann", MailNickname: "kari", UserPrincipalName: "kari@nordlys.no", Mail: "kari.n@nordlys.no",
		EmployeeID: "E-4", JobTitle: "Trainee", AccountEnabled: true, UsageLocation: "NO",
	})
	mark := len(l.g.Calls())
	spec := joiner("E-4")
	spec.Fields = map[string]string{"jobTitle": "Konsulent"}
	acc := l.prepare(t, spec)
	if acc.Created || acc.ExternalID != seeded.ID || acc.State != "enabled" || acc.UserPrincipalName != "kari@nordlys.no" || acc.Email != "kari.n@nordlys.no" {
		t.Errorf("the found user: %+v", acc)
	}
	u := l.user(t, seeded.ID)
	if u.JobTitle != "Konsulent" || u.EmployeeID != "E-4" || u.UserPrincipalName != "kari@nordlys.no" || u.GivenName != "Kari" {
		t.Errorf("the found user brought to the spec: %+v", u)
	}
	if n := countCalls(l.graphCalls(mark), http.MethodPost, "/users"); n != 0 {
		t.Errorf("Prepare created %d users beside the one it found", n)
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.accounts WHERE object_id = $1 AND NOT created_by_us AND state = 'enabled'`, seeded.ID); n != 1 {
		t.Errorf("the found user's row: %d rows match", n)
	}
}

func TestPrepare_TheFilterEscapesTheEmployeeNumber(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	seeded := l.g.AddUser(graphtest.User{DisplayName: "Siobhan O'Brien", MailNickname: "siobhan", UserPrincipalName: "siobhan@nordlys.no", EmployeeID: "O'B-1"})
	mark := len(l.g.Calls())
	spec := joiner("O'B-1")
	spec.Fields = nil
	acc := l.prepare(t, spec)
	if acc.ExternalID != seeded.ID || acc.Created {
		t.Errorf("O'B-1: %+v, want the seeded user found", acc)
	}
	var filters []string
	for _, c := range l.graphCalls(mark) {
		if c.Method == http.MethodGet && c.Path == "/v1.0/users" {
			q, err := url.ParseQuery(c.Query)
			if err != nil {
				t.Fatal(err)
			}
			filters = append(filters, q.Get("$filter"))
			if !strings.Contains(c.Query, "%27O%27%27B-1%27") {
				t.Errorf("the query is not encoded: %s", c.Query)
			}
		}
	}
	if !slices.Equal(filters, []string{"employeeId eq 'O''B-1'"}) {
		t.Errorf("the filters: %q", filters)
	}
}

func TestPrepare_TwoUsersHoldingTheNumberAreAmbiguous(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	l.g.AddUser(graphtest.User{DisplayName: "A", MailNickname: "a", UserPrincipalName: "a@nordlys.no", EmployeeID: "E-2"})
	l.g.AddUser(graphtest.User{DisplayName: "B", MailNickname: "b", UserPrincipalName: "b@nordlys.no", EmployeeID: "E-2"})
	if _, err := l.p.Prepare(context.Background(), staffActor, joiner("E-2")); !errors.Is(err, contracts.ErrExternalAmbiguous) {
		t.Errorf("Prepare: %v, want ErrExternalAmbiguous", err)
	}
	if n := len(l.g.Users()); n != 2 {
		t.Errorf("%d users, want the two seeded", n)
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.accounts`); n != 0 {
		t.Errorf("%d rows, want none", n)
	}
}

func TestPrepare_ASyncedUserIsRefused(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	seeded := l.g.AddUser(graphtest.User{DisplayName: "Kari", MailNickname: "kari", UserPrincipalName: "kari@nordlys.no", EmployeeID: "E-3", OnPremisesSyncEnabled: true})
	if _, err := l.p.Prepare(context.Background(), staffActor, joiner("E-3")); !errors.Is(err, contracts.ErrExternalSyncedOnPremises) {
		t.Errorf("Prepare: %v, want ErrExternalSyncedOnPremises", err)
	}
	if u := l.user(t, seeded.ID); u.JobTitle != "" {
		t.Errorf("the synced user was written: %+v", u)
	}
}

func TestPrepare_WritesTheManager(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	mgr := l.g.AddUser(graphtest.User{DisplayName: "Mona", MailNickname: "mona", UserPrincipalName: "mona@nordlys.no", EmployeeID: "M-1", AccountEnabled: true})
	spec := joiner("E-1")
	spec.ManagerExternalID = mgr.ID
	acc := l.prepare(t, spec)
	if u := l.user(t, acc.ExternalID); u.ManagerID != mgr.ID || len(acc.Reported) != 0 {
		t.Errorf("the manager: %q (reported %v), want %s", u.ManagerID, acc.Reported, mgr.ID)
	}

	gone := joiner("E-2")
	gone.WorkEmail = ""
	gone.GivenName = "Per"
	gone.ManagerExternalID = uuid.NewString()
	acc = l.prepare(t, gone)
	if !acc.Created || !hasCode(acc.Reported, contracts.ReportedManagerNotFound) {
		t.Errorf("a manager the tenant does not hold: %+v", acc)
	}
	got, err := l.p.Account(context.Background(), acc.ExternalID)
	if err != nil || got == nil || !hasCode(got.Reported, contracts.ReportedManagerNotFound) {
		t.Errorf("Account after: %+v %v, want manager_not_found reported", got, err)
	}
}

// --- Activate -----------------------------------------------------------------

func TestActivate_EnablesWritesTheHireDateAssignsAndIssuesThePass(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	spec := joiner("E-1")
	id := l.prepare(t, spec).ExternalID

	acc := l.activate(t, id, spec)
	if acc.State != "enabled" || !acc.HasPass || len(acc.Reported) != 0 || acc.ExternalID != id || acc.Created {
		t.Errorf("Activate answered %+v", acc)
	}
	u := l.user(t, id)
	if !u.AccountEnabled || u.EmployeeHireDate == nil || !u.EmployeeHireDate.Equal(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("enabled with the hire day's 01:00 in Oslo: %v %v", u.AccountEnabled, u.EmployeeHireDate)
	}
	if len(u.Licences) != 1 || u.Licences[0].SkuID != skuE3 || u.Licences[0].State != "Active" || u.Licences[0].AssignedByGroup != "" {
		t.Errorf("the licences: %+v", u.Licences)
	}
	if u.Pass == nil || u.Pass.LifetimeInMinutes != 480 || !u.Pass.IsUsableOnce {
		t.Fatalf("the pass: %+v", u.Pass)
	}
	first := *u.Pass
	expires := modtest.One[time.Time](t, l.h.Harness, `SELECT pass_expires_at FROM entra.accounts WHERE object_id = $1`, id)
	if !expires.Equal(first.CreatedAt.Add(480 * time.Minute).Truncate(time.Second)) {
		t.Errorf("the pass expires %v, want %v", expires, first.CreatedAt.Add(480*time.Minute))
	}
	sealed := modtest.One[[]byte](t, l.h.Harness, `SELECT pass_sealed FROM entra.accounts WHERE object_id = $1`, id)
	if len(sealed) == 0 || bytes.Contains(sealed, []byte(first.Value)) {
		t.Errorf("the pass is not sealed: %d bytes", len(sealed))
	}
	if n := len(l.events(t, "entra.pass.issued")); n != 1 {
		t.Errorf("%d pass events, want one", n)
	}
	if ev := l.events(t, "entra.user.activated"); len(ev) != 1 || !hasCode(ev[0], "enabled") {
		t.Errorf("the activated events: %v", ev)
	}

	// Idempotent: enabled already, no second pass, no second assignment.
	mark := len(l.g.Calls())
	again := l.activate(t, id, spec)
	if again.State != "enabled" || !again.HasPass {
		t.Errorf("the second Activate answered %+v", again)
	}
	calls := l.graphCalls(mark)
	if n := countCalls(calls, http.MethodPost, "/temporaryAccessPassMethods"); n != 0 {
		t.Errorf("the second Activate issued %d passes", n)
	}
	if n := countCalls(calls, http.MethodPost, "/assignLicense"); n != 0 {
		t.Errorf("the second Activate assigned licences %d times", n)
	}
	if n := countCalls(calls, http.MethodPatch, "/users/"+id); n != 0 {
		t.Errorf("the second Activate patched an enabled user %d times", n)
	}

	// HR's Run now: a fresh pass replaces the one not yet revealed.
	fresh := spec
	fresh.Flags.Fresh = true
	if acc := l.activate(t, id, fresh); !acc.HasPass {
		t.Errorf("Activate with Fresh: %+v", acc)
	}
	second := l.user(t, id).Pass
	if second == nil || second.ID == first.ID {
		t.Fatalf("Fresh issued no new pass: %+v", second)
	}

	// The pass is shown once, the second one.
	reader := uuid.New()
	pass, err := l.p.RevealPass(context.Background(), contracts.Actor{UserID: &reader}, id)
	if err != nil || pass.Value != second.Value || !pass.ExpiresAt.Equal(second.CreatedAt.Add(480*time.Minute).Truncate(time.Second)) {
		t.Errorf("RevealPass: %+v %v, want the second pass", pass, err)
	}
	if _, err := l.p.RevealPass(context.Background(), contracts.Actor{UserID: &reader}, id); !errors.Is(err, contracts.ErrPassGone) {
		t.Errorf("the second RevealPass: %v, want ErrPassGone", err)
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.events WHERE action = 'entra.pass.revealed' AND actor_user_id = $1 AND object_id = $2`, reader, id); n != 1 {
		t.Errorf("%d reveal events, want one", n)
	}
	if acc, err := l.p.Account(context.Background(), id); err != nil || acc == nil || acc.HasPass || acc.State != "enabled" {
		t.Errorf("Account after the reveal: %+v %v", acc, err)
	}
	// Enabled with the pass revealed, Activate issues none without Fresh.
	mark = len(l.g.Calls())
	if acc := l.activate(t, id, spec); acc.HasPass {
		t.Errorf("Activate after the reveal: %+v", acc)
	}
	if n := countCalls(l.graphCalls(mark), http.MethodPost, "/temporaryAccessPassMethods"); n != 0 {
		t.Errorf("Activate after the reveal issued %d passes", n)
	}
}

func TestActivate_AHireDateRefusedIsReported(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	l.g.Configure(func(g *graphtest.FakeGraph) { g.HireDateDenied = true })
	spec := joiner("E-1")
	id := l.prepare(t, spec).ExternalID
	acc := l.activate(t, id, spec)
	if acc.State != "enabled" || !hasCode(acc.Reported, contracts.ReportedHireDateNotWritten) || !acc.HasPass {
		t.Errorf("Activate answered %+v", acc)
	}
	if u := l.user(t, id); !u.AccountEnabled || u.EmployeeHireDate != nil {
		t.Errorf("the user: enabled %v, hire date %v", u.AccountEnabled, u.EmployeeHireDate)
	}
	if got, err := l.p.Account(context.Background(), id); err != nil || got == nil || !hasCode(got.Reported, contracts.ReportedHireDateNotWritten) {
		t.Errorf("Account: %+v %v, want hire_date_not_written", got, err)
	}
}

func TestActivate_TheHireDateSwitchedOff(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	l.h.Exec(t, `UPDATE entra.connection SET write_hire_date = false`)
	spec := joiner("E-1")
	id := l.prepare(t, spec).ExternalID
	if acc := l.activate(t, id, spec); acc.State != "enabled" || len(acc.Reported) != 0 {
		t.Errorf("Activate answered %+v", acc)
	}
	if u := l.user(t, id); u.EmployeeHireDate != nil {
		t.Errorf("the hire date was written: %v", u.EmployeeHireDate)
	}
}

func TestActivate_ReportsTheLicencesItCannotAssign(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	l.g.Configure(func(g *graphtest.FakeGraph) { g.Skus[1].Enabled = 0 })
	spec := joiner("E-1")
	spec.Licences = []string{"ENTERPRISEPACK", "STANDARDPACK", "NOSUCHPACK"}
	id := l.prepare(t, spec).ExternalID
	acc := l.activate(t, id, spec)
	if !hasCode(acc.Reported, "licence_unavailable:STANDARDPACK") || !hasCode(acc.Reported, "licence_unavailable:NOSUCHPACK") ||
		hasCode(acc.Reported, "licence_unavailable:ENTERPRISEPACK") || acc.State != "enabled" {
		t.Errorf("Activate answered %+v", acc)
	}
	u := l.user(t, id)
	if !slices.ContainsFunc(u.Licences, func(s graphtest.LicenceState) bool { return s.SkuID == skuE3 && s.State == "Active" }) {
		t.Errorf("the licence with seats was not assigned: %+v", u.Licences)
	}
}

func TestActivate_APassThePolicyRefusesIsReported(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	l.g.Configure(func(g *graphtest.FakeGraph) { g.PassPolicyDenied = true })
	spec := joiner("E-1")
	id := l.prepare(t, spec).ExternalID
	acc := l.activate(t, id, spec)
	if acc.State != "enabled" || acc.HasPass || !hasCode(acc.Reported, contracts.ReportedPassNotAllowed) {
		t.Errorf("Activate answered %+v", acc)
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.accounts WHERE object_id = $1 AND pass_sealed IS NULL`, id); n != 1 {
		t.Error("a pass was kept")
	}
}

func TestRevealPass_GoneAfterItsLifetime(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	spec := joiner("E-1")
	id := l.prepare(t, spec).ExternalID
	l.activate(t, id, spec)
	l.h.Advance(481 * time.Minute)
	if acc, err := l.p.Account(context.Background(), id); err != nil || acc == nil || acc.HasPass {
		t.Errorf("Account after the pass's lifetime: %+v %v", acc, err)
	}
	if _, err := l.p.RevealPass(context.Background(), staffActor, id); !errors.Is(err, contracts.ErrPassGone) {
		t.Errorf("RevealPass: %v, want ErrPassGone", err)
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.accounts WHERE object_id = $1 AND pass_sealed IS NULL`, id); n != 1 {
		t.Error("the expired pass was not cleared")
	}
	if _, err := l.p.RevealPass(context.Background(), staffActor, uuid.NewString()); !errors.Is(err, contracts.ErrPassGone) {
		t.Errorf("RevealPass of an unknown account: %v, want ErrPassGone", err)
	}
	if n := len(l.events(t, "entra.pass.revealed")); n != 0 {
		t.Errorf("%d reveal events for passes never shown", n)
	}
}

// --- Deactivate ---------------------------------------------------------------

// leaverGroups puts the user in one group of each kind and answers the
// static security group's and the Microsoft 365 group's ids — the two the
// leaver's "all" removes — and the three it leaves.
func (l *lifecycle) leaverGroups(id string) (removed, kept []string) {
	static := l.g.AddGroup(graphtest.Group{DisplayName: "Rådgivning", MailNickname: "radgivning", SecurityEnabled: true, Members: []string{id}})
	unified := l.g.AddGroup(graphtest.Group{DisplayName: "Alle", MailNickname: "alle", MailEnabled: true, GroupTypes: []string{"Unified"}, Members: []string{id}})
	dynamic := l.g.AddGroup(graphtest.Group{DisplayName: "Dynamisk", MailNickname: "dyn", SecurityEnabled: true, GroupTypes: []string{"DynamicMembership"},
		MembershipRule: `user.department -eq "Rådgivning"`, Members: []string{id}})
	role := l.g.AddGroup(graphtest.Group{DisplayName: "Admins", MailNickname: "admins", SecurityEnabled: true, IsAssignableToRole: true, Members: []string{id}})
	list := l.g.AddGroup(graphtest.Group{DisplayName: "Liste", MailNickname: "liste", MailEnabled: true, Members: []string{id}})
	return []string{static.ID, unified.ID}, []string{dynamic.ID, role.ID, list.ID}
}

func (l *lifecycle) member(t *testing.T, groupID, userID string) bool {
	t.Helper()
	g, ok := l.g.Group(groupID)
	if !ok {
		t.Fatalf("no group %s", groupID)
	}
	return slices.Contains(g.Members, userID)
}

func leaver(spec contracts.ExternalSpec) contracts.ExternalSpec {
	leave := leaveDay
	spec.LeaveDate = &leave
	return spec
}

func TestDeactivate_RunsTheLeaverStepsInOrder(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	spec := joiner("E-1")
	id := l.prepare(t, spec).ExternalID
	l.activate(t, id, spec)
	removed, kept := l.leaverGroups(id)
	l.g.EditUser(id, func(u *graphtest.User) {
		u.Licences = append(u.Licences, graphtest.LicenceState{SkuID: skuE1, AssignedByGroup: removed[0], State: "Active", Error: "None"})
	})

	mark := len(l.g.Calls())
	acc := l.deactivate(t, id, leaver(spec))
	if acc.State != "disabled" || acc.HasPass || len(acc.Reported) != 0 {
		t.Errorf("Deactivate answered %+v", acc)
	}
	if got, want := writes(l.graphCalls(mark)), []string{
		"PATCH " + id, "POST revokeSignInSessions", "PATCH " + id, "POST assignLicense", "DELETE $ref", "DELETE $ref",
	}; !slices.Equal(got, want) {
		t.Errorf("the leaver's writes:\n got %q\nwant %q", got, want)
	}
	u := l.user(t, id)
	if u.AccountEnabled || u.SessionsRevokedAt == nil || u.EmployeeLeaveDateTime == nil ||
		!u.EmployeeLeaveDateTime.Equal(time.Date(2027, 3, 31, 21, 0, 0, 0, time.UTC)) {
		t.Errorf("the leaver: enabled %v, sessions %v, leave %v", u.AccountEnabled, u.SessionsRevokedAt, u.EmployeeLeaveDateTime)
	}
	if len(u.Licences) != 1 || u.Licences[0].SkuID != skuE1 || u.Licences[0].AssignedByGroup == "" {
		t.Errorf("the direct licence stripped, the group's kept: %+v", u.Licences)
	}
	for _, g := range removed {
		if l.member(t, g, id) {
			t.Errorf("still a member of %s", g)
		}
	}
	for _, g := range kept {
		if !l.member(t, g, id) {
			t.Errorf("removed from %s, a dynamic, role-assignable or mail-only group", g)
		}
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.accounts WHERE object_id = $1 AND state = 'disabled' AND pass_sealed IS NULL`, id); n != 1 {
		t.Error("the row is not disabled with the pass cleared")
	}
	ev := l.events(t, "entra.user.deactivated")
	if len(ev) != 1 || !slices.Equal(ev[0], []string{"disabled", "sessions_revoked", "leave_date_written", "licences_stripped", "groups_removed"}) {
		t.Errorf("the deactivated events: %v", ev)
	}
	if _, err := l.p.RevealPass(context.Background(), staffActor, id); !errors.Is(err, contracts.ErrPassGone) {
		t.Errorf("the leaver's pass: %v, want ErrPassGone", err)
	}
}

func TestDeactivate_AFailedStepLeavesTheRestDisabledFirst(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	spec := joiner("E-1")
	id := l.prepare(t, spec).ExternalID
	l.activate(t, id, spec)
	removed, _ := l.leaverGroups(id)

	l.w.failOn("POST /revokeSignInSessions", http.StatusForbidden)
	acc := l.deactivate(t, id, leaver(spec))
	if acc.State != "disabled" || !hasCode(acc.Reported, "leaver_step_failed:revoke_sessions") {
		t.Errorf("Deactivate answered %+v", acc)
	}
	u := l.user(t, id)
	if u.AccountEnabled || u.EmployeeLeaveDateTime != nil || len(u.Licences) != 1 || !l.member(t, removed[0], id) {
		t.Errorf("the steps after the failed one ran: enabled %v, leave %v, licences %v", u.AccountEnabled, u.EmployeeLeaveDateTime, u.Licences)
	}
	if got, err := l.p.Account(context.Background(), id); err != nil || got == nil || got.State != "disabled" || !hasCode(got.Reported, "leaver_step_failed:revoke_sessions") {
		t.Errorf("Account: %+v %v", got, err)
	}

	// The next cycle runs the rest; the user is disabled already.
	l.w.clear()
	mark := len(l.g.Calls())
	acc = l.deactivate(t, id, leaver(spec))
	if acc.State != "disabled" || len(acc.Reported) != 0 {
		t.Errorf("the second Deactivate answered %+v", acc)
	}
	if got := writes(l.graphCalls(mark)); len(got) == 0 || got[0] != "POST revokeSignInSessions" {
		t.Errorf("the second Deactivate's writes: %q", got)
	}
	u = l.user(t, id)
	if u.EmployeeLeaveDateTime == nil || len(u.Licences) != 0 || l.member(t, removed[0], id) {
		t.Errorf("the rest did not run: leave %v, licences %v", u.EmployeeLeaveDateTime, u.Licences)
	}
}

func TestDeactivate_EveryStepSwitchedOff(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	l.h.Exec(t, `UPDATE entra.connection SET leaver_revoke_sessions = false, leaver_write_leave_date = false,
	    leaver_strip_licences = false, leaver_remove_groups = 'none'`)
	spec := joiner("E-1")
	id := l.prepare(t, spec).ExternalID
	l.activate(t, id, spec)
	removed, _ := l.leaverGroups(id)
	mark := len(l.g.Calls())
	if acc := l.deactivate(t, id, leaver(spec)); acc.State != "disabled" {
		t.Errorf("Deactivate answered %+v", acc)
	}
	if got := writes(l.graphCalls(mark)); !slices.Equal(got, []string{"PATCH " + id}) {
		t.Errorf("the writes: %q, want the disable alone", got)
	}
	u := l.user(t, id)
	if u.AccountEnabled || u.EmployeeLeaveDateTime != nil || u.SessionsRevokedAt != nil || len(u.Licences) != 1 || !l.member(t, removed[0], id) {
		t.Errorf("a step switched off ran: %+v", u)
	}
}

// --- Update, Delete, Account --------------------------------------------------

func TestUpdate_WritesWhatChangedAndNeverTheEmployeeID(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	spec := joiner("E-1")
	mgr := l.g.AddUser(graphtest.User{DisplayName: "Mona", MailNickname: "mona", UserPrincipalName: "mona@nordlys.no", EmployeeID: "M-1"})
	id := l.prepare(t, spec).ExternalID
	l.activate(t, id, spec)

	moved := spec
	moved.EmployeeNumber = "E-999"
	moved.ManagerExternalID = mgr.ID
	moved.Fields = map[string]string{"jobTitle": "Seniorkonsulent", "division": "Vest", "department": ""}
	acc, err := l.p.Update(context.Background(), staffActor, id, moved)
	if err != nil || acc.State != "enabled" || acc.ExternalID != id {
		t.Fatalf("Update: %+v %v", acc, err)
	}
	u := l.user(t, id)
	if u.EmployeeID != "E-1" || u.JobTitle != "Seniorkonsulent" || u.Department != "" || u.ManagerID != mgr.ID ||
		u.EmployeeOrgData == nil || *u.EmployeeOrgData != (graphtest.OrgData{CostCenter: "4200", Division: "Vest"}) ||
		!slices.Equal(u.BusinessPhones, []string{"+47 22 00 00 00"}) {
		t.Errorf("the mover: %+v", u)
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.accounts WHERE object_id = $1 AND employee_number = 'E-1'`, id); n != 1 {
		t.Error("the row's employee number was rewritten")
	}
	ev := l.events(t, "entra.user.updated")
	if len(ev) != 1 || !hasCode(ev[0], "changed:jobTitle") || !hasCode(ev[0], "changed:employeeOrgData") || !hasCode(ev[0], "changed:department") || hasCode(ev[0], "changed:employeeId") {
		t.Errorf("the updated events: %v", ev)
	}

	// Nothing changed: no PATCH.
	mark := len(l.g.Calls())
	if _, err := l.p.Update(context.Background(), staffActor, id, moved); err != nil {
		t.Fatal(err)
	}
	if n := countCalls(l.graphCalls(mark), http.MethodPatch, "/users/"+id); n != 0 {
		t.Errorf("an unchanged Update patched %d times", n)
	}
}

func TestDelete_RefusesAnEnabledUserThenSoftDeletes(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	spec := joiner("E-1")
	id := l.prepare(t, spec).ExternalID
	l.activate(t, id, spec)
	ctx := context.Background()
	if err := l.p.Delete(ctx, staffActor, id); !errors.Is(err, contracts.ErrExternalEnabled) {
		t.Errorf("Delete of an enabled user: %v, want ErrExternalEnabled", err)
	}
	if len(l.g.Deleted()) != 0 {
		t.Fatal("an enabled user was deleted")
	}
	l.deactivate(t, id, leaver(spec))
	if err := l.p.Delete(ctx, staffActor, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if d := l.g.Deleted(); len(d) != 1 || d[0].ID != id {
		t.Errorf("the deleted users: %+v", d)
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.accounts WHERE object_id = $1 AND state = 'deleted'`, id); n != 1 {
		t.Error("the row is not marked deleted")
	}
	if ev := l.events(t, "entra.user.deleted"); len(ev) != 2 || !slices.Equal(ev[0], []string{"refused:enabled"}) || len(ev[1]) != 0 {
		t.Errorf("the deleted events: %q, want the refusal, then the delete", ev)
	}
	if acc, err := l.p.Account(ctx, id); err != nil || acc != nil {
		t.Errorf("Account of a deleted user: %+v %v, want nil", acc, err)
	}
	if err := l.p.Delete(ctx, staffActor, id); err != nil {
		t.Errorf("a second Delete: %v, want none", err)
	}

	// A rehire is a new user; the deleted one still holds its address.
	rehire := joiner("E-1")
	rehire.WorkEmail = ""
	acc := l.prepare(t, rehire)
	if !acc.Created || acc.ExternalID == id || acc.UserPrincipalName != "kari.nordmann2@nordlys.no" {
		t.Errorf("the rehire: %+v", acc)
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.accounts WHERE employee_number = 'E-1'`); n != 1 {
		t.Errorf("%d rows hold the number, want the rehire's", n)
	}
}

func TestAccount_ReadsTheUserAndAnswersNilWhenGone(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	ctx := context.Background()
	for _, id := range []string{uuid.NewString(), "not-an-id"} {
		if acc, err := l.p.Account(ctx, id); err != nil || acc != nil {
			t.Errorf("Account(%q): %+v %v, want nil", id, acc, err)
		}
	}
	spec := joiner("E-1")
	id := l.prepare(t, spec).ExternalID
	before := modtest.One[time.Time](t, l.h.Harness, `SELECT updated_at FROM entra.accounts WHERE object_id = $1`, id)
	l.h.Advance(time.Hour)
	acc, err := l.p.Account(ctx, id)
	if err != nil || acc == nil || acc.ExternalID != id || acc.State != "disabled" || acc.UserPrincipalName != "kari.nordmann@nordlys.no" ||
		acc.Email != "kari.nordmann@nordlys.no" || acc.Created || acc.HasPass {
		t.Errorf("Account: %+v %v", acc, err)
	}
	if after := modtest.One[time.Time](t, l.h.Harness, `SELECT updated_at FROM entra.accounts WHERE object_id = $1`, id); !after.Equal(before) {
		t.Error("Account wrote the row")
	}
	// A user an administrator removed: nil, and the row is left as it is.
	l.g.EditUser(id, func(u *graphtest.User) { u.Deleted = true })
	if acc, err := l.p.Account(ctx, id); err != nil || acc != nil {
		t.Errorf("Account of a removed user: %+v %v", acc, err)
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.accounts WHERE object_id = $1 AND state = 'disabled'`, id); n != 1 {
		t.Error("Account changed the row of a removed user")
	}
}

// --- Sensitive actions, the connection, the actor -----------------------------

func TestSensitive_APrivilegedUserIsRefusedAndTheStreakCounts(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	spec := joiner("E-1")
	id := l.prepare(t, spec).ExternalID
	l.g.EditUser(id, func(u *graphtest.User) { u.Privileged = true })
	ctx := context.Background()
	streak := func() int { return l.h.Count(t, `SELECT sensitive_denied_streak FROM entra.connection`) }

	for i := 1; i <= 3; i++ {
		if _, err := l.p.Activate(ctx, staffActor, id, spec); !errors.Is(err, contracts.ErrExternalPrivileged) {
			t.Fatalf("Activate %d: %v, want ErrExternalPrivileged", i, err)
		}
		if n := streak(); n != i {
			t.Errorf("the streak after %d refusals: %d", i, n)
		}
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.accounts WHERE object_id = $1 AND last_error = 'privileged' AND 'privileged' = ANY(reported)`, id); n != 1 {
		t.Error("the row does not carry the refusal")
	}
	if acc, err := l.p.Account(ctx, id); err != nil || acc == nil || !hasCode(acc.Reported, contracts.ReportedPrivileged) {
		t.Errorf("Account: %+v %v, want privileged reported", acc, err)
	}
	if info, err := l.p.Provider(ctx); err != nil || info.Status != contracts.ProviderConnected || info.Reason != "role_missing" {
		t.Errorf("Provider after three refusals: %+v %v", info, err)
	}
	if u := l.user(t, id); u.AccountEnabled {
		t.Error("the privileged user was enabled")
	}

	l.g.EditUser(id, func(u *graphtest.User) { u.Privileged = false })
	acc := l.activate(t, id, spec)
	if acc.State != "enabled" || hasCode(acc.Reported, contracts.ReportedPrivileged) {
		t.Errorf("Activate once the role is gone: %+v", acc)
	}
	if n := streak(); n != 0 {
		t.Errorf("the streak after a success: %d", n)
	}
	if n := l.h.Count(t, `SELECT count(*) FROM entra.accounts WHERE object_id = $1 AND last_error IS NULL`, id); n != 1 {
		t.Error("the refusal stayed on the row")
	}

	// The delete is a sensitive action too.
	l.deactivate(t, id, leaver(spec))
	l.g.EditUser(id, func(u *graphtest.User) { u.Privileged = true })
	if err := l.p.Delete(ctx, staffActor, id); !errors.Is(err, contracts.ErrExternalPrivileged) {
		t.Errorf("Delete of a privileged user: %v, want ErrExternalPrivileged", err)
	}
}

func TestProvisioner_TheConnectionDecidesWhatRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	spec := joiner("E-1")
	id := uuid.NewString()

	// Unconfigured: nothing runs, Deactivate included, and nothing is asked.
	h, g := newTenantHarness(t)
	p := provider(h)
	_, prepare := p.Prepare(ctx, staffActor, spec)
	_, activate := p.Activate(ctx, staffActor, id, spec)
	_, deactivate := p.Deactivate(ctx, staffActor, id, spec)
	_, update := p.Update(ctx, staffActor, id, spec)
	_, account := p.Account(ctx, id)
	for name, err := range map[string]error{
		"Prepare": prepare, "Activate": activate, "Deactivate": deactivate, "Update": update,
		"Delete": p.Delete(ctx, staffActor, id), "Account": account,
	} {
		if !errors.Is(err, contracts.ErrProviderUnavailable) {
			t.Errorf("unconfigured %s: %v, want ErrProviderUnavailable", name, err)
		}
	}
	if n := len(g.Calls()); n != 0 {
		t.Errorf("an unconfigured provider made %d calls", n)
	}

	// Failing with a working secret: Deactivate and Account run, the rest wait.
	l := newLifecycle(t)
	id = l.prepare(t, spec).ExternalID
	l.activate(t, id, spec)
	l.h.Exec(t, `UPDATE entra.connection SET status = 'failing', status_reason = 'tenant_unreachable'`)
	_, prepare = l.p.Prepare(ctx, staffActor, spec)
	_, activate = l.p.Activate(ctx, staffActor, id, spec)
	_, update = l.p.Update(ctx, staffActor, id, spec)
	for name, err := range map[string]error{
		"Prepare": prepare, "Activate": activate, "Update": update, "Delete": l.p.Delete(ctx, staffActor, id),
	} {
		if !errors.Is(err, contracts.ErrProviderUnavailable) {
			t.Errorf("failing %s: %v, want ErrProviderUnavailable", name, err)
		}
	}
	if acc, err := l.p.Account(ctx, id); err != nil || acc == nil || acc.State != "enabled" {
		t.Errorf("failing Account: %+v %v", acc, err)
	}
	if acc := l.deactivate(t, id, leaver(spec)); acc.State != "disabled" {
		t.Errorf("failing Deactivate: %+v", acc)
	}
	if u := l.user(t, id); u.AccountEnabled {
		t.Error("the leaver is still enabled")
	}
}

func TestProvisioner_RefusesAnInvalidActor(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	ctx := context.Background()
	spec := joiner("E-1")
	id := l.prepare(t, spec).ExternalID
	mark := len(l.g.Calls())
	nobody := uuid.Nil
	for _, actor := range []contracts.Actor{{}, {System: "system:someone"}, {UserID: &nobody}} {
		_, prepare := l.p.Prepare(ctx, actor, spec)
		_, activate := l.p.Activate(ctx, actor, id, spec)
		_, deactivate := l.p.Deactivate(ctx, actor, id, spec)
		_, update := l.p.Update(ctx, actor, id, spec)
		_, reveal := l.p.RevealPass(ctx, actor, id)
		for name, err := range map[string]error{
			"Prepare": prepare, "Activate": activate, "Deactivate": deactivate, "Update": update,
			"Delete": l.p.Delete(ctx, actor, id), "RevealPass": reveal,
		} {
			if !errors.Is(err, contracts.ErrInvalidActor) {
				t.Errorf("%s by %+v: %v, want ErrInvalidActor", name, actor, err)
			}
		}
	}
	if n := len(l.g.Calls()) - mark; n != 0 {
		t.Errorf("an invalid actor's writes made %d calls", n)
	}
}

func TestLifecycle_NoPasswordPassOrSecretOutsideTheSealedColumns(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	ctx := context.Background()
	spec := joiner("E-1")
	id := l.prepare(t, spec).ExternalID
	l.activate(t, id, spec)
	pass, err := l.p.RevealPass(ctx, staffActor, id)
	if err != nil {
		t.Fatal(err)
	}
	fresh := spec
	fresh.Flags.Fresh = true
	l.activate(t, id, fresh)
	second := l.user(t, id).Pass.Value
	l.deactivate(t, id, leaver(spec))
	if err := l.p.Delete(ctx, staffActor, id); err != nil {
		t.Fatal(err)
	}

	secrets := append([]string{pass.Value, second, secretValue}, l.w.createdPasswords()...)
	if len(secrets) != 4 || slices.Contains(secrets, "") {
		t.Fatalf("the secrets to look for: %d", len(secrets))
	}
	var dump strings.Builder
	for _, table := range []string{"entra.accounts", "entra.events", "entra.connection", "entra.credentials"} {
		rows, err := l.h.Pool().Query(ctx, `SELECT row_to_json(t)::text FROM `+table+` AS t`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			dump.WriteString(s + "\n")
		}
		rows.Close()
	}
	logs := l.h.Logs()
	for i, s := range secrets {
		if strings.Contains(logs, s) {
			t.Errorf("secret %d is in the log", i)
		}
		if strings.Contains(dump.String(), s) {
			t.Errorf("secret %d is in a row", i)
		}
	}
	// The events carry the UPN and codes alone: no address in a code.
	rows, err := l.h.Pool().Query(ctx, `SELECT codes FROM entra.events`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var codes []string
		if err := rows.Scan(&codes); err != nil {
			t.Fatal(err)
		}
		for _, c := range codes {
			if strings.Contains(c, "@") || strings.Contains(c, " ") {
				t.Errorf("an event code that is not a code: %q", c)
			}
		}
	}
}
