package staff_test

import (
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// Run now and Disable now (plan Task 4c): the worker's cycle by hand, for
// everyone or one person, the caller as the actor; and the flag turned off
// with the user disabled at once (the owner's answer 2).

const accountsRunPath = "/api/v1/staff/accounts/run"

func accountRunPath(id int64) string     { return accountPath(id) + "/run" }
func accountDisablePath(id int64) string { return accountPath(id) + "/disable" }

// runPerson is Run now for personID, answering 200.
func runPerson(t *testing.T, c *modtest.Client, personID int64) gen.StaffPersonAccountRun {
	t.Helper()
	var out gen.StaffPersonAccountRun
	do(t, c, http.MethodPost, accountRunPath(personID), nil, http.StatusOK, &out)
	return out
}

// leaseLink makes personID's link held by another cycle for five minutes.
func leaseLink(t *testing.T, h *harness, personID int64) {
	t.Helper()
	h.Exec(t, `UPDATE staff.identity_links SET lease_id = $2, lease_until = $3 WHERE person_id = $1`,
		personID, uuid.New(), h.Now().Add(5*time.Minute))
}

// pendingRevoked is a pending user, enabled, whose invitation an Owner
// revoked, linked to p's worker link in the pending state.
func (w accountsWorld) pendingRevoked(t *testing.T, p gen.StaffPerson) uuid.UUID {
	t.Helper()
	u := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStatePending, Managed: true, EmployeeNumber: p.EmployeeNumber,
		Invitation: contracts.Invitation{State: contracts.InvitationRevokedByOwner, Issued: 1}})
	workerLink(t, w.h, p.Id, u, "pending")
	return u
}

// Run now for one person gives identity a fresh invitation (the first step's
// alone): a pending user whose invitation an Owner revoked, which the worker
// leaves revoked, is invited again; the link's reason is cleared, and the
// caller is the actor identity is told of and the history names.
func TestAccountsRun_APersonsRunSendsAFreshInvitation(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Fersk", "2026-01-01", nil)
	u := w.pendingRevoked(t, p)
	w.cycle(t)
	if l := mustLink(t, w.h, p.Id); l.Reason != "invitation_revoked" || countWrites(w.fake, "Activate") != 0 {
		t.Fatalf("the worker's own cycle: link %+v, writes %v; want invitation_revoked and no Activate", l, w.fake.writeCalls())
	}
	got := runPerson(t, w.hr, p.Id)
	if writes := w.fake.writeCalls(); len(writes) == 0 || writes[0] != "Activate:fresh" || countWrites(w.fake, "Activate:fresh") != 1 {
		t.Errorf("writes = %v, want one fresh Activate, first", writes)
	}
	if !got.InvitationSent {
		t.Error("invitationSent = false, want true")
	}
	if ref := w.fake.user(u); ref.Invitation.State != contracts.InvitationActive {
		t.Errorf("the user's invitation = %+v, want active", ref.Invitation)
	}
	if got.Account.Link == nil || got.Account.Link.Reason != nil || got.Account.Link.State != "pending" {
		t.Errorf("the account's link = %+v, want pending with no reason", got.Account.Link)
	}
	for _, a := range w.fake.writeActors() {
		if a.UserID == nil || a.System != "" {
			t.Errorf("identity was told of %+v, want the caller", a)
		}
	}
	e := eventOfKind(t, eventsAs(t, w.hr, p.Id), "account_activated")
	if e.Actor.Kind != "user" {
		t.Errorf("account_activated's actor = %+v, want the caller", e.Actor)
	}
}

// A fresh invitation within identity's ten minutes sends nothing (an
// ordinary Activate): the run is done all the same, 200, invitationSent
// false, the link left on its reason.
func TestAccountsRun_NothingSentWithinTenMinutesIsDone(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Snart", "2026-01-01", nil)
	w.pendingRevoked(t, p)
	w.fake.set(func(f *fakeAccounts) { f.recentInvitation = true })
	got := runPerson(t, w.hr, p.Id)
	if got.InvitationSent {
		t.Error("invitationSent = true, want false")
	}
	if l := mustLink(t, w.h, p.Id); l.Reason != "invitation_revoked" || l.LastError != "" || l.Attempts != 0 {
		t.Errorf("the link = %+v, want invitation_revoked and no failure", l)
	}
}

// Against identity's real provider: Run now prepares, enables and invites;
// an Owner revokes the invitation; Run now within ten minutes of the last
// invitation sends nothing and is done; eleven minutes on it sends one.
func TestAccountsRun_TheTenMinutesAgainstIdentity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	role := uuid.New()
	h.Exec(t, `INSERT INTO identity.roles (id, name, normalized_name, display_name, version, created_at, updated_at)
	           VALUES ($1, 'Employee', 'EMPLOYEE', 'Employee', $2, $3, $3)`, role, uuid.New(), h.Now())
	h.Exec(t, `INSERT INTO identity.role_permissions (role_id, permission_key) VALUES ($1, 'staff:access')`, role)
	h.Exec(t, `INSERT INTO identity.provisioning_roles (role_id, created_at) VALUES ($1, $2)`, role, h.Now())
	h.Exec(t, `UPDATE staff.settings SET create_before_days = 0`)
	p := accountEmployee(t, h, hr, "Timinutt", today(t, h), nil)
	box := h.Deps().Mail.(*mail.Fake)
	address := modtest.One[string](t, h.Harness, `SELECT work_email FROM staff.people WHERE id = $1`, p.Id)

	if got := runPerson(t, hr, p.Id); !got.InvitationSent || len(invitesTo(box, address)) != 1 {
		t.Fatalf("the first run: invitationSent %v, %d mails; want one sent", got.InvitationSent, len(invitesTo(box, address)))
	}
	l := mustLink(t, h, p.Id)
	h.Exec(t, `UPDATE identity.invitations SET revoked_at = $2, revoked_reason = 'owner' WHERE user_id = $1 AND revoked_at IS NULL`,
		uuid.MustParse(l.User), h.Now())
	h.Advance(time.Minute)
	if got := runPerson(t, hr, p.Id); got.InvitationSent || len(invitesTo(box, address)) != 1 {
		t.Errorf("within ten minutes: invitationSent %v, %d mails; want none sent", got.InvitationSent, len(invitesTo(box, address)))
	}
	h.Advance(11 * time.Minute)
	if got := runPerson(t, hr, p.Id); !got.InvitationSent || len(invitesTo(box, address)) != 2 {
		t.Errorf("eleven minutes on: invitationSent %v, %d mails; want one more sent", got.InvitationSent, len(invitesTo(box, address)))
	}
}

// Run now writes the preparing link of a person due who has none, and works
// it whatever its next attempt: a link the worker finished today is worked
// again.
func TestAccountsRun_APersonsRunPreparesAndIgnoresTheSchedule(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Plan", "2026-01-01", nil)
	runPerson(t, w.hr, p.Id)
	if l := mustLink(t, w.h, p.Id); l.User == "" || l.State != "pending" {
		t.Fatalf("after the run the link = %+v, want the prepared user, pending", l)
	}
	if got := runPerson(t, w.hr, p.Id); got.InvitationSent {
		t.Error("a run with an invitation open reports one sent")
	}
	before := countWrites(w.fake, "Activate")
	w.cycle(t)
	if countWrites(w.fake, "Activate") != before {
		t.Fatalf("the worker acted on a link done today: %v", w.fake.writeCalls())
	}
	ref := w.fake.user(uuid.MustParse(mustLink(t, w.h, p.Id).User))
	ref.Invitation = contracts.Invitation{State: contracts.InvitationExpired, Issued: 1}
	w.fake.setUser(ref)
	if got := runPerson(t, w.hr, p.Id); !got.InvitationSent {
		t.Errorf("a run before the next attempt sent no invitation for an expired one: writes %v", w.fake.writeCalls())
	}
}

// Run now refuses while the worker holds the person's link (accounts_busy),
// on an erased person (person_erased) and an unknown one (404) — and with
// the flag off runs nothing.
func TestAccountsRun_APersonsRunsRefusals(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	busy := w.employee(t, "Opptatt", "2026-01-01", nil)
	w.pendingRevoked(t, busy)
	leaseLink(t, w.h, busy.Id)
	refused(t, w.hr.Do(http.MethodPost, accountRunPath(busy.Id), nil), http.StatusConflict, "accounts_busy")

	erased := w.employee(t, "Slettet", "2026-01-01", nil)
	w.h.Exec(t, `UPDATE staff.people SET erased_at = $2 WHERE id = $1`, erased.Id, w.h.Now())
	refused(t, w.hr.Do(http.MethodPost, accountRunPath(erased.Id), nil), http.StatusConflict, "person_erased")

	if r := w.hr.Do(http.MethodPost, accountRunPath(erased.Id+100000), nil); r.Status != http.StatusNotFound {
		t.Errorf("an unknown person: %d, want 404", r.Status)
	}

	off := createPerson(t, w.hr, newPerson("Av", "Konto"))
	seedEmploymentDated(t, w.h, off.Id, 0, "2026-01-01", nil)
	if got := runPerson(t, w.hr, off.Id); got.InvitationSent || got.Account.Link != nil {
		t.Errorf("a run with the flag off = %+v, want nothing done", got)
	}
	if writes := w.fake.writeCalls(); len(writes) != 0 {
		t.Errorf("writes = %v, want none", writes)
	}
	approver, _ := signIn(t, w.h, "staff:approve")
	if r := approver.Do(http.MethodPost, accountRunPath(busy.Id), nil); r.Status != http.StatusForbidden {
		t.Errorf("an Approver's run: %d, want 403", r.Status)
	}
}

// Run accounts now: one cycle with the caller as the actor, answering what
// it did — a prepared account done, a refusal (privileged) done, a read
// identity failed failed and retried — and nothing remaining.
func TestAccountsRun_TheRunsCounts(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	w.employee(t, "Ny", "2026-01-01", nil)
	down := w.employee(t, "Nede", "2026-01-01", nil)
	uDown := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: uDown, State: contracts.AccountStateActive, Managed: true, EmployeeNumber: down.EmployeeNumber})
	workerLink(t, w.h, down.Id, uDown, "active")
	w.fake.failReading(uDown, errFakeDown)
	owner := w.employee(t, "Eier", "2026-01-01", nil)
	uOwner := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: uOwner, State: contracts.AccountStateActive, Managed: true, Privileged: true, EmployeeNumber: owner.EmployeeNumber})
	workerLink(t, w.h, owner.Id, uOwner, "active")

	var got gen.StaffAccountsRun
	do(t, w.hr, http.MethodPost, accountsRunPath, nil, http.StatusOK, &got)
	if got != (gen.StaffAccountsRun{Attempted: 3, Done: 2, Failed: 1, Remaining: 0}) {
		t.Errorf("the run = %+v, want 3 attempted, 2 done, 1 failed, none remaining", got)
	}
	for _, a := range w.fake.writeActors() {
		if a.UserID == nil || a.System != "" {
			t.Errorf("identity was told of %+v, want the caller", a)
		}
	}
	if l := mustLink(t, w.h, down.Id); l.LastError != "accounts_unavailable" || l.Attempts != 1 {
		t.Errorf("the failed link = %+v, want accounts_unavailable, one attempt", l)
	}
	approver, _ := signIn(t, w.h, "staff:approve")
	if r := approver.Do(http.MethodPost, accountsRunPath, nil); r.Status != http.StatusForbidden {
		t.Errorf("an Approver's run: %d, want 403", r.Status)
	}
}

// The run's budget: once sixty seconds have passed no claim is begun — the
// one in hand finishes — and what is left is reported remaining, for the
// worker's next cycle.
func TestAccountsRun_TheBudget(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	w.employee(t, "En", "2026-01-01", nil)
	w.employee(t, "To", "2026-01-01", nil)
	w.employee(t, "Tre", "2026-01-01", nil)
	var once bool
	w.fake.whenWriting(func(method string, _ uuid.UUID) {
		if method == "Prepare" && !once {
			once = true
			w.h.Advance(61 * time.Second)
		}
	})
	var got gen.StaffAccountsRun
	do(t, w.hr, http.MethodPost, accountsRunPath, nil, http.StatusOK, &got)
	if got != (gen.StaffAccountsRun{Attempted: 1, Done: 1, Failed: 0, Remaining: 2}) {
		t.Errorf("the run = %+v, want one attempted and done, two remaining", got)
	}
	w.cycle(t)
	if n := countWrites(w.fake, "Prepare"); n != 3 {
		t.Errorf("after the worker's cycle Prepare was called %d times, want 3", n)
	}
}

// With no module providing accounts, Run accounts now is 503
// accounts_unavailable, as is a person's run.
func TestAccountsRun_WithoutAccounts(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithoutAccounts())
	hr, _ := signIn(t, h, "staff:manage")
	refused(t, hr.Do(http.MethodPost, accountsRunPath, nil), http.StatusServiceUnavailable, "accounts_unavailable")
	p := accountEmployee(t, h, hr, "Uten", "2026-01-01", nil)
	refused(t, hr.Do(http.MethodPost, accountRunPath(p.Id), nil), http.StatusServiceUnavailable, "accounts_unavailable")
}

// Disable now (the owner's answer 2): the flag turned off — one
// account_flag_updated event, the flag's revision moved, the person's
// byte-equal for their manager — and the user disabled with the caller as
// the actor, account_deactivated recorded; the next cycle does nothing.
func TestAccountsDisable_TurnsTheFlagOffAndDisables(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	boss := createPerson(t, w.hr, newPerson("Sjef", "Sjefsen"))
	seedEmploymentDated(t, w.h, boss.Id, 0, "2020-01-01", nil)
	bossC, bossUser := signIn(t, w.h)
	link(t, w.h, boss.Id, bossUser)
	p := createPerson(t, w.hr, newPerson("Stopp", "Konto"))
	w.h.Exec(t, `UPDATE staff.people SET work_email = 'stopp.konto@example.test' WHERE id = $1`, p.Id)
	seedEmploymentDated(t, w.h, p.Id, boss.Id, "2026-01-01", nil)
	flagOn(t, w.hr, p.Id)
	u := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStateActive, Managed: true, EmployeeNumber: p.EmployeeNumber, HasCredential: true})
	workerLink(t, w.h, p.Id, u, "active")
	before := getAccount(t, w.hr, p.Id)
	asManager := bossC.Do(http.MethodGet, personPath(p.Id), nil)

	var got gen.StaffPersonAccount
	do(t, w.hr, http.MethodPost, accountDisablePath(p.Id), nil, http.StatusOK, &got)
	if got.ProvisionAccount || got.Revision == before.Revision || got.Link == nil || got.Link.State != "disabled" {
		t.Errorf("after Disable now the account = %+v, want the flag off, its revision moved, the link disabled", got)
	}
	if ref := w.fake.user(u); ref.State != contracts.AccountStateDisabled || ref.DisabledBy != contracts.DisabledByStaff {
		t.Errorf("the user = %+v, want disabled by Staff", ref)
	}
	if actors := w.fake.writeActors(); len(actors) != 1 || actors[0].UserID == nil {
		t.Errorf("identity was told of %+v, want the caller", actors)
	}
	events := eventsAs(t, w.hr, p.Id)
	flag := eventOfKind(t, events, "account_flag_updated")
	if fields, _ := flag.Payload["fields"].(map[string]any); fields["provisionAccount"] == nil || len(fields) != 1 {
		t.Errorf("account_flag_updated = %v, want provisionAccount alone", flag.Payload)
	}
	if e := eventOfKind(t, events, "account_deactivated"); e.Actor.Kind != "user" {
		t.Errorf("account_deactivated's actor = %+v, want the caller", e.Actor)
	}
	if after := bossC.Do(http.MethodGet, personPath(p.Id), nil); string(after.Body) != string(asManager.Body) {
		t.Errorf("the manager reads the person differently:\nbefore %s\nafter  %s", asManager.Body, after.Body)
	}
	writes := len(w.fake.writeCalls())
	setClock(t, w.h, osloAt("2026-09-14", 8))
	w.cycle(t)
	if n := len(w.fake.writeCalls()); n != writes {
		t.Errorf("the next cycle wrote %v, want nothing", w.fake.writeCalls()[writes:])
	}
}

// Disable now's refusals: HR's self-service link to a user Staff does not
// manage is account_not_managed, an Owner's account_privileged — the flag
// off either way; a link the worker holds is accounts_busy with nothing
// changed; an unknown person 404.
func TestAccountsDisable_TheRefusals(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	for _, tc := range []struct {
		name, code string
		seed       func(p gen.StaffPerson)
	}{
		{"not managed", "account_not_managed", func(p gen.StaffPerson) {
			u := uuid.New()
			w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStateActive, EmployeeNumber: p.EmployeeNumber})
			link(t, w.h, p.Id, u)
		}},
		{"privileged", "account_privileged", func(p gen.StaffPerson) {
			u := uuid.New()
			w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStateActive, Managed: true, Privileged: true, EmployeeNumber: p.EmployeeNumber})
			workerLink(t, w.h, p.Id, u, "active")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := w.employee(t, "Nei", "2026-01-01", nil)
			tc.seed(p)
			refused(t, w.hr.Do(http.MethodPost, accountDisablePath(p.Id), nil), http.StatusConflict, tc.code)
			if acc := getAccount(t, w.hr, p.Id); acc.ProvisionAccount {
				t.Error("the flag is still on")
			}
			if l := mustLink(t, w.h, p.Id); l.Leased {
				t.Errorf("the link is still leased: %+v", l)
			}
		})
	}
	p := w.employee(t, "Holdt", "2026-01-01", nil)
	u := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStateActive, Managed: true, EmployeeNumber: p.EmployeeNumber})
	workerLink(t, w.h, p.Id, u, "active")
	leaseLink(t, w.h, p.Id)
	deactivations := countWrites(w.fake, "Deactivate")
	refused(t, w.hr.Do(http.MethodPost, accountDisablePath(p.Id), nil), http.StatusConflict, "accounts_busy")
	if acc := getAccount(t, w.hr, p.Id); !acc.ProvisionAccount {
		t.Error("a busy Disable now turned the flag off")
	}
	if n := countWrites(w.fake, "Deactivate"); n != deactivations {
		t.Errorf("Deactivate called %d more times while the link was leased", n-deactivations)
	}
	if r := w.hr.Do(http.MethodPost, accountDisablePath(p.Id+100000), nil); r.Status != http.StatusNotFound {
		t.Errorf("an unknown person: %d, want 404", r.Status)
	}
	approver, _ := signIn(t, w.h, "staff:approve", "staff:personal-data")
	if r := approver.Do(http.MethodPost, accountDisablePath(p.Id), nil); r.Status != http.StatusForbidden {
		t.Errorf("an Approver with personal-data: %d, want 403", r.Status)
	}
}

// Identity failing on Disable now: 503 accounts_unavailable, the flag
// already off, the link's last_error set and accountFailing raised for HR —
// no cycle retries a flag that is off, so HR does — and the next cycle
// does nothing. A retry once identity answers disables the user.
func TestAccountsDisable_IdentityFailingLeavesTheFlagOff(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Feil", "2026-01-01", nil)
	u := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStateActive, Managed: true, EmployeeNumber: p.EmployeeNumber})
	workerLink(t, w.h, p.Id, u, "active")
	w.fake.failWith("Deactivate", errFakeDown)
	refused(t, w.hr.Do(http.MethodPost, accountDisablePath(p.Id), nil), http.StatusServiceUnavailable, "accounts_unavailable")
	if acc := getAccount(t, w.hr, p.Id); acc.ProvisionAccount {
		t.Error("the flag is on after the failure")
	}
	if l := mustLink(t, w.h, p.Id); l.LastError != "accounts_unavailable" || l.Attempts != 1 || l.State != "active" {
		t.Errorf("the link = %+v, want accounts_unavailable, one attempt, still active", l)
	}
	id := "accountFailing/" + strconv.FormatInt(p.Id, 10)
	if !slices.ContainsFunc(getAttention(t, w.hr), func(i gen.StaffStatsAttentionItem) bool { return i.Id == id }) {
		t.Errorf("HR's items lack %s", id)
	}
	writes := len(w.fake.writeCalls())
	setClock(t, w.h, osloAt("2026-09-14", 8))
	w.cycle(t)
	if n := len(w.fake.writeCalls()); n != writes {
		t.Errorf("the next cycle wrote %v, want nothing", w.fake.writeCalls()[writes:])
	}
	w.fake.failWith("Deactivate", nil)
	do(t, w.hr, http.MethodPost, accountDisablePath(p.Id), nil, http.StatusOK, nil)
	if ref := w.fake.user(u); ref.State != contracts.AccountStateDisabled {
		t.Errorf("after the retry the user = %+v, want disabled", ref)
	}
}

// Disable now for a person with no link turns the flag off, 200, and asks
// identity nothing.
func TestAccountsDisable_APersonWithNoLink(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Ingen", "2026-10-01", nil)
	var got gen.StaffPersonAccount
	do(t, w.hr, http.MethodPost, accountDisablePath(p.Id), nil, http.StatusOK, &got)
	if got.ProvisionAccount || got.Link != nil {
		t.Errorf("the account = %+v, want the flag off and no link", got)
	}
	if writes := w.fake.writeCalls(); len(writes) != 0 {
		t.Errorf("writes = %v, want none", writes)
	}
}

// Both runs and Disable now need no second connection (the lock rule): on a
// pool of one each finishes, identity called on no connection.
func TestAccountsRun_OnAPoolOfOne(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t, modtest.WithPoolMaxConns(1))
	w.h.Exec(t, `UPDATE staff.settings SET create_before_days = 0`)
	a := w.employee(t, "Alle", today(t, w.h), nil)
	b := w.employee(t, "Bare", today(t, w.h), nil)
	within := func(name string, fn func()) {
		t.Helper()
		done := make(chan struct{})
		go func() { defer close(done); fn() }()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Fatalf("%s did not finish on a pool of one", name)
		}
	}
	var all gen.StaffAccountsRun
	within("Run accounts now", func() {
		r := w.hr.Do(http.MethodPost, accountsRunPath, nil)
		if r.Status == http.StatusOK {
			r.JSON(&all)
		}
	})
	if all.Attempted != 2 || all.Failed != 0 {
		t.Errorf("the run on a pool of one = %+v, want both worked", all)
	}
	within("Run now", func() {
		if r := w.hr.Do(http.MethodPost, accountRunPath(a.Id), nil); r.Status != http.StatusOK {
			t.Errorf("Run now: %d %s", r.Status, r.Body)
		}
	})
	within("Disable now", func() {
		if r := w.hr.Do(http.MethodPost, accountDisablePath(b.Id), nil); r.Status != http.StatusOK {
			t.Errorf("Disable now: %d %s", r.Status, r.Body)
		}
	})
	if l := mustLink(t, w.h, b.Id); l.State != "disabled" {
		t.Errorf("after Disable now the link = %+v, want disabled", l)
	}
}
