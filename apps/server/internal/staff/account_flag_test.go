package staff_test

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

func accountPath(id int64) string { return personPath(id) + "/account" }

// getAccount reads a person's account flag and fails the test unless it
// answered 200.
func getAccount(t *testing.T, c *modtest.Client, id int64) gen.StaffPersonAccount {
	t.Helper()
	var out gen.StaffPersonAccount
	do(t, c, http.MethodGet, accountPath(id), nil, http.StatusOK, &out)
	return out
}

// managedLink writes a link in one of the accounts worker's states to user,
// managed — the worker is Task 4's, and the reads need only the row.
func managedLink(t *testing.T, h *harness, personID int64, user uuid.UUID, state string) {
	t.Helper()
	h.Exec(t, `
		INSERT INTO staff.identity_links (person_id, provider, external_id, state, managed, created_at, updated_at)
		VALUES ($1, 'identity', $2, $3, true, $4, $4)`, personID, user.String(), state, h.Now())
}

func dayOf(d *gen.StaffPersonAccount, effective bool) string {
	v := d.ProvisionFrom
	if effective {
		v = d.ProvisionFromEffective
	}
	if v == nil {
		return ""
	}
	return v.Format(time.DateOnly)
}

// The account flag is the links group's (design §5, plan Global
// Constraints): the person themselves, staff:manage and staff:personal-data
// read it; her manager, the Approver, a colleague and an outsider get the
// unknown id's 404, byte for byte. A fresh flag is off, at the work address,
// and the self-service link shows as HR's link.
func TestAccount_ReadByAudience(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	unknown := w.nobody.Do(http.MethodGet, accountPath(w.kari.Id+100000), nil)
	if unknown.Status != http.StatusNotFound {
		t.Fatalf("an unknown person: %d, want 404", unknown.Status)
	}
	for _, c := range []struct {
		name   string
		client *modtest.Client
		want   int
	}{
		{"Kari herself", w.kariC, http.StatusOK},
		{"staff:manage", w.hr, http.StatusOK},
		{"staff:personal-data", w.pd, http.StatusOK},
		{"her manager", w.bossC, http.StatusNotFound},
		{"the Approver", w.approver, http.StatusNotFound},
		{"a colleague", w.viewer, http.StatusNotFound},
		{"an outsider", w.nobody, http.StatusNotFound},
	} {
		r := c.client.Do(http.MethodGet, accountPath(w.kari.Id), nil)
		if r.Status != c.want {
			t.Errorf("%s: %d %s, want %d", c.name, r.Status, r.Body, c.want)
			continue
		}
		if c.want == http.StatusNotFound {
			if string(r.Body) != string(unknown.Body) {
				t.Errorf("%s: %q, want the unknown id's 404 %q", c.name, r.Body, unknown.Body)
			}
			for _, header := range []string{"Content-Type", "Content-Length"} {
				if r.Header(header) != unknown.Header(header) {
					t.Errorf("%s: %s %q, want the unknown id's %q", c.name, header, r.Header(header), unknown.Header(header))
				}
			}
			continue
		}
		var got gen.StaffPersonAccount
		r.JSON(&got)
		if got.ProvisionAccount || got.AccountEmailKind != gen.StaffAccountEmailKind("work") || got.Address.Kind != "work" ||
			got.Link == nil || got.Link.State != "linked" || got.Link.Managed || got.Link.User == nil || got.Link.User.Id != w.kariUser {
			t.Errorf("%s reads %+v (link %+v), want the flag off at work and HR's link to Kari's user", c.name, got, got.Link)
		}
	}
}

// PUT /people/{id}/account is staff:manage's, a full replace of the three
// fields under If-Match: what it leaves out is cleared, a change is one
// account_flag_updated event of class C naming each changed field, and a
// replace that changes nothing writes nothing. The flag on with the chosen
// address missing is accepted. The event is listed to the person, HR and
// staff:personal-data, never to her manager.
func TestAccount_PutReplacesTheFlag(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h, hr := w.h, w.hr
	before := getAccount(t, hr, w.kari.Id)
	full := map[string]any{"provisionAccount": true, "provisionFrom": "2026-11-01", "accountEmailKind": "private"}

	for name, c := range map[string]*modtest.Client{"Kari herself": w.kariC, "staff:personal-data": w.pd, "her manager": w.bossC} {
		if r := c.Do(http.MethodPut, accountPath(w.kari.Id), full, ifMatch(before.Revision)); r.Status != http.StatusForbidden {
			t.Errorf("%s writing the flag: %d, want 403", name, r.Status)
		}
	}
	for _, body := range []map[string]any{
		with(full, "accountEmailKind", "home"),
		with(full, "userId", uuid.NewString()),
	} {
		refused(t, hr.Do(http.MethodPut, accountPath(w.kari.Id), body, ifMatch(before.Revision)), http.StatusBadRequest, "")
	}

	var saved gen.StaffPersonAccount
	do(t, hr, http.MethodPut, accountPath(w.kari.Id), full, http.StatusOK, &saved, ifMatch(before.Revision))
	if !saved.ProvisionAccount || dayOf(&saved, false) != "2026-11-01" || dayOf(&saved, true) != "2026-11-01" ||
		saved.AccountEmailKind != "private" || saved.Address.Kind != "private" || saved.Address.Present || saved.Revision != before.Revision+1 {
		t.Errorf("saved = %+v, want the flag on from 2026-11-01 at the private address (missing), the revision moved on", saved)
	}
	refused(t, hr.Do(http.MethodPut, accountPath(w.kari.Id), full, ifMatch(before.Revision)), http.StatusConflict, "revision_conflict")

	events := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND kind = 'account_flag_updated'`, w.kari.Id)
	var same gen.StaffPersonAccount
	do(t, hr, http.MethodPut, accountPath(w.kari.Id), full, http.StatusOK, &same, ifMatch(saved.Revision))
	if same.Revision != saved.Revision || h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND kind = 'account_flag_updated'`, w.kari.Id) != events {
		t.Errorf("an unchanged replace moved the revision to %d or wrote an event, want neither", same.Revision)
	}
	if events != 1 {
		t.Errorf("account_flag_updated events = %d, want 1", events)
	}

	var replaced gen.StaffPersonAccount
	do(t, hr, http.MethodPut, accountPath(w.kari.Id), map[string]any{"provisionAccount": true}, http.StatusOK, &replaced, ifMatch(saved.Revision))
	if replaced.ProvisionFrom != nil || replaced.AccountEmailKind != "work" {
		t.Errorf("a replace naming the flag alone = %+v, want provisionFrom cleared and the work address", replaced)
	}
	// Kari's employment from 2020-01-01 is open, so the effective date is
	// its first day once HR's own date is cleared.
	if dayOf(&replaced, true) != "2020-01-01" {
		t.Errorf("effective = %q, want her employment's start 2020-01-01", dayOf(&replaced, true))
	}

	var kinds []string
	for _, e := range eventsAs(t, hr, w.kari.Id) {
		if e.Kind == "account_flag_updated" {
			kinds = append(kinds, fmt.Sprint(e.Payload["fields"]))
		}
	}
	if len(kinds) != 2 {
		t.Fatalf("HR reads %d account_flag_updated events, want 2", len(kinds))
	}
	// Newest first: the replace naming the flag alone, then the first change.
	if !strings.Contains(kinds[1], "provisionAccount") || !strings.Contains(kinds[1], "2026-11-01") || !strings.Contains(kinds[1], "private") ||
		strings.Contains(kinds[0], "provisionAccount") || !strings.Contains(kinds[0], "provisionFrom") || !strings.Contains(kinds[0], "accountEmailKind") {
		t.Errorf("the events' fields = %v, want the first naming all three, the second provisionFrom and accountEmailKind alone", kinds)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND kind = 'account_flag_updated' AND payload_classes = '{C}'`, w.kari.Id); n != 2 {
		t.Errorf("account_flag_updated of class C = %d, want 2", n)
	}
	for name, c := range map[string]*modtest.Client{"Kari herself": w.kariC, "staff:personal-data": w.pd} {
		if !slices.Contains(kindsOf(eventsAs(t, c, w.kari.Id)), "account_flag_updated") {
			t.Errorf("%s's history lacks account_flag_updated", name)
		}
	}
	var page gen.PaginatedResponseOfStaffEvent
	do(t, w.bossC, http.MethodGet, pagedPath(eventsPath(w.kari.Id), 1, 100), nil, http.StatusOK, &page)
	if slices.Contains(kindsOf(page.Data), "account_flag_updated") || int(page.Pagination.TotalCount) != len(page.Data) {
		t.Errorf("her manager's history = %v (total %d), want no account event, counted or listed", kindsOf(page.Data), page.Pagination.TotalCount)
	}
}

// The effective date (spec reading 14): HR's date when set, else the first
// day of the earliest employment that has not ended by today — the end day
// itself still counts — else none.
func TestAccount_TheEffectiveDate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	effective := func() string {
		got := getAccount(t, hr, p.Id)
		return dayOf(&got, true)
	}
	if got := effective(); got != "" {
		t.Errorf("no employment: effective %q, want none", got)
	}
	ended := "2021-12-31"
	seedEmploymentDated(t, h, p.Id, 0, "2020-01-01", &ended)
	if got := effective(); got != "" {
		t.Errorf("an ended employment alone: effective %q, want none", got)
	}
	seedEmploymentDated(t, h, p.Id, 0, "2027-03-01", nil)
	if got := effective(); got != "2027-03-01" {
		t.Errorf("a future employment: effective %q, want 2027-03-01", got)
	}
	endsToday := today(t, h)
	seedEmploymentDated(t, h, p.Id, 0, "2026-02-01", &endsToday)
	if got := effective(); got != "2026-02-01" {
		t.Errorf("an employment ending today beside a later one: effective %q, want 2026-02-01, the earliest not ended", got)
	}
	acc := getAccount(t, hr, p.Id)
	do(t, hr, http.MethodPut, accountPath(p.Id), map[string]any{"provisionAccount": true, "provisionFrom": "2025-12-01"}, http.StatusOK, &acc, ifMatch(acc.Revision))
	if got := effective(); got != "2025-12-01" {
		t.Errorf("HR's early date: effective %q, want 2025-12-01", got)
	}

	// Between employments the date is the one the accounts worker acts on
	// (accountFrom, Task 4b): a stored date from before the last ended
	// employment is that one's, and the later start stands for it; a stored
	// date after it is the later one's own, honoured early.
	ola := createPerson(t, hr, newPerson("Ola", "Mellom"))
	seedEmploymentDated(t, h, ola.Id, 0, "2020-01-01", &ended)
	seedEmploymentDated(t, h, ola.Id, 0, "2027-03-01", nil)
	set := func(from string) string {
		acc := getAccount(t, hr, ola.Id)
		do(t, hr, http.MethodPut, accountPath(ola.Id), map[string]any{"provisionAccount": true, "provisionFrom": from}, http.StatusOK, &acc, ifMatch(acc.Revision))
		return dayOf(&acc, true)
	}
	if got := set("2019-06-01"); got != "2027-03-01" {
		t.Errorf("a stored date from before the ended employment: effective %q, want the later start 2027-03-01", got)
	}
	if got := set("2027-01-15"); got != "2027-01-15" {
		t.Errorf("a stored date after the ended employment: effective %q, want it honoured, 2027-01-15", got)
	}
}

// The link as identity reports it, from a provider the test shapes: the
// mode, a pending managed user's invitation and the worker's state. identity
// is asked once per read, outside every transaction, and with no address to
// compare: the view shows none. A mode mismatch or no provider leaves the
// mode out; identity failing leaves the invitation out and still answers.
func TestAccount_TheLinkAsIdentityReportsIt(t *testing.T) {
	t.Parallel()
	fake := newFakeAccounts()
	fake.set(func(f *fakeAccounts) { f.mode = contracts.AccountModeFederated })
	h := newHarness(t, withAccounts(fake))
	hr, hrID := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, with(newPerson("Kari", "Nordmann"), "workEmail", "kari@example.test"))
	user := uuid.New()
	managedLink(t, h, p.Id, user, "pending")
	fake.setUser(contracts.AccountRef{
		UserID: user, State: contracts.AccountStatePending, DisabledBy: contracts.DisabledByStaff, Managed: true,
		Invitation: contracts.Invitation{State: contracts.InvitationExhausted, Issued: 3},
	})

	got := getAccount(t, hr, p.Id)
	if got.Mode == nil || *got.Mode != "federated" {
		t.Errorf("mode = %v, want federated", got.Mode)
	}
	if !got.Address.Present || got.Address.Kind != "work" {
		t.Errorf("address = %+v, want the work address, present", got.Address)
	}
	if got.Link == nil || got.Link.State != "pending" || !got.Link.Managed || got.Link.Invitation == nil ||
		got.Link.Invitation.State != "exhausted" || got.Link.Invitation.Issued != 3 || got.Link.User == nil || got.Link.User.Id != user {
		t.Errorf("link = %+v, want pending, managed, the exhausted invitation and the user", got.Link)
	}
	if calls := fake.accountCalls(); len(calls) != 1 || calls[0].user != user || calls[0].wantEmail != "" {
		t.Errorf("identity was asked %+v, want once about the user with no address to compare", calls)
	}
	for _, c := range contractCalls.by(hrID) {
		if strings.HasPrefix(c.method, "Accounts.") && c.inTx {
			t.Errorf("%s was called inside a transaction", c.method)
		}
	}

	fake.set(func(f *fakeAccounts) { f.modeErr = contracts.ErrModeMismatch })
	if got := getAccount(t, hr, p.Id); got.Mode != nil {
		t.Errorf("on a mode mismatch mode = %v, want it absent", *got.Mode)
	}
	fake.set(func(f *fakeAccounts) { f.modeErr = nil; f.accountErr = errors.New("identity is down") })
	if got := getAccount(t, hr, p.Id); got.Link == nil || got.Link.Invitation != nil || got.Mode == nil {
		t.Errorf("identity failing: %+v, want the link and the mode, no invitation", got.Link)
	}

	bare := newHarness(t, modtest.WithoutAccounts())
	hr2, _ := signIn(t, bare, "staff:manage")
	q := createPerson(t, hr2, newPerson("Ola", "Hansen"))
	managedLink(t, bare, q.Id, uuid.New(), "pending")
	if got := getAccount(t, hr2, q.Id); got.Mode != nil || got.Link == nil || got.Link.Invitation != nil {
		t.Errorf("without a provider: %+v, want no mode, the link without an invitation", got)
	}
}

// Against identity's real provider: a Staff-managed pending user, as
// identity holds one, reads pending with no invitation yet, and the mode is
// the local one identity's mount recorded.
func TestAccount_AgainstIdentitysProvider(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	user := uuid.New()
	email := fmt.Sprintf("pending-%s@example.test", user)
	h.Exec(t, `
		INSERT INTO identity.users (id, email, normalized_email, display_name, version, is_disabled, disabled_by,
			managed_by, employee_number, employee_number_key, pending_since, created_at, updated_at)
		VALUES ($1, $2, upper($2), 'Kari Nordmann', $3, true, 'staff', 'staff', $4::text, upper($4::text), $5, $5, $5)`,
		user, email, uuid.New(), p.EmployeeNumber, h.Now())
	managedLink(t, h, p.Id, user, "pending")

	got := getAccount(t, hr, p.Id)
	if got.Mode == nil || *got.Mode != "local" {
		t.Errorf("mode = %v, want local, as identity recorded it", got.Mode)
	}
	if got.Link == nil || got.Link.Invitation == nil || got.Link.Invitation.State != "none" || got.Link.Invitation.Issued != 0 {
		t.Errorf("link = %+v, want the pending user's invitation none, 0 issued", got.Link)
	}
	meta := raw(t, hr.Do(http.MethodGet, metaPath, nil))
	if meta["accountMode"] != "local" || meta["provisioningRolesSet"] != false {
		t.Errorf("meta = accountMode %v provisioningRolesSet %v, want local and false", meta["accountMode"], meta["provisioningRolesSet"])
	}
}

// Erased, the flag is read-only like the rest of the record.
func TestAccount_AnErasedPersonsFlagIsReadOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage", "staff:personal-data", "staff:view")
	r := seedErased(t, h, hr)
	got := getAccount(t, hr, r.person.Id)
	refused(t, hr.Do(http.MethodPut, accountPath(r.person.Id), map[string]any{"provisionAccount": true}, ifMatch(got.Revision)), http.StatusConflict, "person_erased")
	if after := getAccount(t, hr, r.person.Id); after.ProvisionAccount || after.Revision != got.Revision {
		t.Errorf("after the refusal %+v, want the flag off at revision %d", after, got.Revision)
	}
}

// Both personal-data files carry the account flag with the link's state
// (plan Task 3, "Exports").
func TestAccount_TheExportsCarryTheFlag(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	acc := getAccount(t, w.hr, w.kari.Id)
	do(t, w.hr, http.MethodPut, accountPath(w.kari.Id), map[string]any{"provisionAccount": true, "provisionFrom": "2026-11-01", "accountEmailKind": "private"}, http.StatusOK, nil, ifMatch(acc.Revision))
	for name, file := range map[string]gen.StaffPersonalData{
		"the complete file": exportOf(t, w.pd.Do(http.MethodGet, personalDataPath(w.kari.Id), nil)),
		"her own":           exportOf(t, w.kariC.Do(http.MethodGet, "/api/v1/staff/me/export", nil)),
	} {
		a := file.Account
		if !a.ProvisionAccount || a.ProvisionFrom == nil || a.ProvisionFrom.Format(time.DateOnly) != "2026-11-01" ||
			a.AccountEmailKind != "private" || a.LinkState == nil || *a.LinkState != "linked" {
			t.Errorf("%s's account = %+v, want the flag on from 2026-11-01 at private and the link linked", name, a)
		}
	}
}

// The flag has its own revision (review M1): a change of it moves nothing a
// manager, an Approver or a colleague reads of the person — not the person's
// revision, not the ETag — so the change is no signal to anyone outside the
// links group. GET /people/{id} answers them byte for byte as before.
func TestAccount_AChangeIsInvisibleOutsideTheLinksGroup(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	type read struct {
		status int
		body   string
		etag   string
	}
	readAs := func() map[string]read {
		out := map[string]read{}
		for name, c := range map[string]*modtest.Client{"her manager": w.bossC, "the Approver": w.approver, "a colleague": w.viewer} {
			r := c.Do(http.MethodGet, personPath(w.kari.Id), nil)
			out[name] = read{r.Status, string(r.Body), r.Header("ETag")}
		}
		return out
	}
	before := readAs()
	personRevision := getAccount(t, w.hr, w.kari.Id)
	do(t, w.hr, http.MethodPut, accountPath(w.kari.Id), map[string]any{"provisionAccount": true, "provisionFrom": "2026-11-01"},
		http.StatusOK, nil, ifMatch(personRevision.Revision))
	after := readAs()
	for name, b := range before {
		if b.status != http.StatusOK {
			t.Fatalf("%s reads the person: %d, want 200", name, b.status)
		}
		if a := after[name]; a != b {
			t.Errorf("%s reads the person differently after the flag changed:\nbefore %+v\nafter  %+v", name, b, a)
		}
	}
	if got := w.h.Count(t, `SELECT revision FROM staff.people WHERE id = $1`, w.kari.Id); int32(got) != w.kari.Revision {
		t.Errorf("the person's revision = %d after the flag changed, want %d", got, w.kari.Revision)
	}
}

// The replace must name the flag (review M2): the decoder reads a body
// without provisionAccount, or with it null, as false, and a replace that leaves it out is not
// "turn the account off". It is a 400 naming provisionAccount, and nothing
// changes: the flag stays on, no event, the flag's revision where it was.
func TestAccount_AReplaceMustNameTheFlag(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	acc := getAccount(t, hr, p.Id)
	do(t, hr, http.MethodPut, accountPath(p.Id), map[string]any{"provisionAccount": true}, http.StatusOK, &acc, ifMatch(acc.Revision))
	events := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND kind = 'account_flag_updated'`, p.Id)
	for _, body := range []map[string]any{{}, {"provisionFrom": nil, "accountEmailKind": "work"}, {"provisionAccount": nil}} {
		pr := refused(t, hr.Do(http.MethodPut, accountPath(p.Id), body, ifMatch(acc.Revision)), http.StatusBadRequest, "")
		if len(pr.Errors["provisionAccount"]) == 0 {
			t.Errorf("%v: errors = %v, want provisionAccount named", body, pr.Errors)
		}
	}
	if got := getAccount(t, hr, p.Id); !got.ProvisionAccount || got.Revision != acc.Revision {
		t.Errorf("after the refusals %+v, want the flag on at revision %d", got, acc.Revision)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND kind = 'account_flag_updated'`, p.Id); n != events {
		t.Errorf("account_flag_updated events = %d after the refusals, want %d", n, events)
	}
}

// The account events are the links group's (review L6): an Approver and a
// colleague, who may read the person, are listed none of them and count
// none — their history (an Approver's, which seesEvents refuses, and a
// colleague's) answers as it did before the flag changed.
func TestAccount_TheEventsAreNotTheApproversOrAColleagues(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	read := func() map[string]string {
		out := map[string]string{}
		for name, c := range map[string]*modtest.Client{"the Approver": w.approver, "a colleague": w.viewer} {
			r := c.Do(http.MethodGet, pagedPath(eventsPath(w.kari.Id), 1, 100), nil)
			out[name] = fmt.Sprintf("%d %s", r.Status, r.Body)
		}
		return out
	}
	before := read()
	acc := getAccount(t, w.hr, w.kari.Id)
	do(t, w.hr, http.MethodPut, accountPath(w.kari.Id), map[string]any{"provisionAccount": true}, http.StatusOK, nil, ifMatch(acc.Revision))
	if !slices.Contains(kindsOf(eventsAs(t, w.hr, w.kari.Id)), "account_flag_updated") {
		t.Fatal("HR's history lacks account_flag_updated")
	}
	after := read()
	for name, b := range before {
		if after[name] != b || strings.Contains(after[name], "account_") {
			t.Errorf("%s's history changed with the flag:\nbefore %s\nafter  %s", name, b, after[name])
		}
	}
}
