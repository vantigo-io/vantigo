package staff_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff"
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
// already off, the link's last_error set and accountFailing raised for HR;
// the worker retries the disable asked for at every poll (review M1) until
// identity answers, and then does nothing more.
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
	// The flag is off, but the disable asked for is the worker's to finish
	// (review M1): retried at every poll while it fails.
	deactivations := countWrites(w.fake, "Deactivate")
	w.h.Advance(time.Hour + time.Minute)
	w.cycle(t)
	if n := countWrites(w.fake, "Deactivate"); n != deactivations+1 {
		t.Errorf("the next poll called Deactivate %d more times, want once", n-deactivations)
	}
	w.fake.failWith("Deactivate", nil)
	w.h.Advance(time.Hour + time.Minute)
	w.cycle(t)
	if ref := w.fake.user(u); ref.State != contracts.AccountStateDisabled {
		t.Errorf("after identity answered the user = %+v, want disabled by the worker", ref)
	}
	writes := len(w.fake.writeCalls())
	setClock(t, w.h, osloAt("2026-09-14", 8))
	w.cycle(t)
	if n := len(w.fake.writeCalls()); n != writes {
		t.Errorf("a cycle after the disable wrote %v, want nothing", w.fake.writeCalls()[writes:])
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

// disableMarker is personID's link's disable_requested_at, nil when unset.
func disableMarker(t *testing.T, h *harness, personID int64) *time.Time {
	t.Helper()
	return modtest.One[*time.Time](t, h.Harness, `SELECT disable_requested_at FROM staff.identity_links WHERE person_id = $1`, personID)
}

// hasItem is whether c's attention items carry id.
func hasItem(t *testing.T, c *modtest.Client, id string) (gen.StaffStatsAttentionItem, bool) {
	t.Helper()
	items := getAttention(t, c)
	i := slices.IndexFunc(items, func(i gen.StaffStatsAttentionItem) bool { return i.Id == id })
	if i < 0 {
		return gen.StaffStatsAttentionItem{}, false
	}
	return items[i], true
}

// waitFor polls cond for up to fifteen seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A Disable now whose client goes away while identity is still disabling
// the user (review M1): the work goes on without the request, the link
// records that a disable was asked for (disable_requested_at), the failure
// raises accountFailing, and the next cycle — the flag off — deactivates
// the user and clears the marker.
func TestAccountsDisable_AnInterruptedDisableIsFinished(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Avbrutt", "2026-01-01", nil)
	u := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStateActive, Managed: true, EmployeeNumber: p.EmployeeNumber})
	workerLink(t, w.h, p.Id, u, "active")
	release := make(chan struct{})
	w.fake.blockUntil("Deactivate", release)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.hr.Do(http.MethodPost, accountDisablePath(p.Id), nil, modtest.Context(ctx))
	}()
	waitFor(t, "the claim", func() bool { return mustLeased(t, w.h, p.Id) })
	cancel()
	<-done
	if disableMarker(t, w.h, p.Id) == nil {
		t.Fatal("no disable recorded on the link after the client went away")
	}
	w.fake.failWith("Deactivate", errFakeDown)
	close(release)
	w.fake.blockUntil("Deactivate", nil)
	waitFor(t, "the result", func() bool { return !mustLeased(t, w.h, p.Id) })
	if l, _ := linkOf(t, w.h, p.Id); l.LastError != "accounts_unavailable" {
		t.Errorf("the link = %+v, want the failure recorded", l)
	}
	id := "accountFailing/" + strconv.FormatInt(p.Id, 10)
	if _, ok := hasItem(t, w.hr, id); !ok {
		t.Errorf("HR's items lack %s", id)
	}
	w.fake.failWith("Deactivate", nil)
	w.h.Advance(time.Hour + time.Minute)
	w.cycle(t)
	if ref := w.fake.user(u); ref.State != contracts.AccountStateDisabled {
		t.Errorf("after the next cycle the user = %+v, want disabled", ref)
	}
	if m := disableMarker(t, w.h, p.Id); m != nil {
		t.Errorf("the marker is still set: %v", m)
	}
	if _, ok := hasItem(t, w.hr, id); ok {
		t.Errorf("%s is still raised", id)
	}
}

// mustLeased is whether personID's link holds a live lease.
func mustLeased(t *testing.T, h *harness, personID int64) bool {
	t.Helper()
	return modtest.One[bool](t, h.Harness, `SELECT coalesce(lease_until > $2, false) FROM staff.identity_links WHERE person_id = $1`, personID, h.Now())
}

// A cycle that takes the link between Disable now's commit and its claim
// (review M1): accounts_busy with the flag already off — and the marker
// left, which the worker finishes once that lease has gone.
func TestAccountsDisable_ALinkClaimedAfterTheCommitIsFinished(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Mellom", "2026-01-01", nil)
	u := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStateActive, Managed: true, EmployeeNumber: p.EmployeeNumber})
	workerLink(t, w.h, p.Id, u, "active")
	var hookErr error
	staff.OnDisableCommitted(t, w.h.Deps(), p.Id, func() {
		_, hookErr = w.h.Pool().Exec(context.Background(), `UPDATE staff.identity_links SET lease_id = $2, lease_until = $3 WHERE person_id = $1`,
			p.Id, uuid.New(), w.h.Now().Add(5*time.Minute))
	})
	refused(t, w.hr.Do(http.MethodPost, accountDisablePath(p.Id), nil), http.StatusConflict, "accounts_busy")
	if hookErr != nil {
		t.Fatalf("lease the link after the commit: %v", hookErr)
	}
	if acc := getAccount(t, w.hr, p.Id); acc.ProvisionAccount {
		t.Error("the flag is on")
	}
	if disableMarker(t, w.h, p.Id) == nil {
		t.Fatal("no disable recorded on the link")
	}
	w.h.Advance(6 * time.Minute)
	w.cycle(t)
	if ref := w.fake.user(u); ref.State != contracts.AccountStateDisabled {
		t.Errorf("after the lease lapsed the user = %+v, want disabled by the worker", ref)
	}
	if l := mustLink(t, w.h, p.Id); l.State != "disabled" || disableMarker(t, w.h, p.Id) != nil {
		t.Errorf("the link = %+v, want disabled and the marker cleared\n%s", l, w.h.Logs())
	}
}

// An erased person's account (review M2): Disable now disables it; when
// identity fails, accountFailing is raised for the erased record under a
// neutral title, and the worker finishes the disable.
func TestAccountsDisable_AnErasedPerson(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	erase := func(given string) (gen.StaffPerson, uuid.UUID) {
		p := w.employee(t, given, "2020-01-01", dayPtr("2026-06-30"))
		u := uuid.New()
		w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStateActive, Managed: true, EmployeeNumber: p.EmployeeNumber})
		workerLink(t, w.h, p.Id, u, "active")
		w.h.Exec(t, `UPDATE staff.identity_links SET next_attempt_at = $2 WHERE person_id = $1`, p.Id, w.h.Now().Add(48*time.Hour))
		w.h.Exec(t, `UPDATE staff.people SET erased_at = $2 WHERE id = $1`, p.Id, w.h.Now())
		return p, u
	}
	ok, uOK := erase("Slettetja")
	var got gen.StaffPersonAccount
	do(t, w.hr, http.MethodPost, accountDisablePath(ok.Id), nil, http.StatusOK, &got)
	if got.Link == nil || got.Link.State != "disabled" || w.fake.user(uOK).State != contracts.AccountStateDisabled {
		t.Errorf("an erased person's Disable now = %+v, want the user disabled", got.Link)
	}

	failing, uFail := erase("Slettetnei")
	w.fake.failWith("Deactivate", errFakeDown)
	refused(t, w.hr.Do(http.MethodPost, accountDisablePath(failing.Id), nil), http.StatusServiceUnavailable, "accounts_unavailable")
	item, found := hasItem(t, w.hr, "accountFailing/"+strconv.FormatInt(failing.Id, 10))
	if !found {
		t.Fatal("no accountFailing item for the erased person")
	}
	if item.Title != "An erased person" || strings.Contains(item.Title, "Slettetnei") {
		t.Errorf("the item's title = %q, want the neutral title", item.Title)
	}
	w.fake.failWith("Deactivate", nil)
	w.h.Advance(time.Hour + time.Minute)
	w.cycle(t)
	if ref := w.fake.user(uFail); ref.State != contracts.AccountStateDisabled {
		t.Errorf("after the next cycle the erased person's user = %+v, want disabled", ref)
	}
	if disableMarker(t, w.h, failing.Id) != nil {
		t.Error("the marker is still set")
	}
}

// A hand run whose client goes away (review M3) finishes all the same: the
// result is written and no live lease is left.
func TestAccountsRun_AnInterruptedRunFinishes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		path func(id int64) string
	}{
		{"one person", accountRunPath},
		{"everyone", func(int64) string { return accountsRunPath }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newAccountsWorld(t)
			p := w.employee(t, "Brutt", "2026-01-01", nil)
			release, inside := make(chan struct{}), make(chan struct{})
			var once bool
			w.fake.whenWriting(func(method string, _ uuid.UUID) {
				if method == "Activate" && !once {
					once = true
					close(inside)
				}
			})
			w.fake.blockUntil("Activate", release)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				defer close(done)
				w.hr.Do(http.MethodPost, tc.path(p.Id), nil, modtest.Context(ctx))
			}()
			<-inside
			cancel()
			<-done
			close(release)
			waitFor(t, "the result", func() bool { return !mustLeased(t, w.h, p.Id) })
			l := mustLink(t, w.h, p.Id)
			if l.LastError != "" || l.State != "pending" {
				t.Errorf("the link = %+v, want the activation written, no failure", l)
			}
			if ref := w.fake.user(uuid.MustParse(l.User)); ref.Invitation.State != contracts.InvitationActive {
				t.Errorf("the user = %+v, want invited", ref)
			}
		})
	}
}

// accountFailing for a person whose flag is off comes only from a disable
// requested (review L1): three failures, then the flag turned off, raise
// nothing.
func TestAccountAttention_AFlagTurnedOffAfterFailuresRaisesNothing(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Tre", "2026-01-01", nil)
	u := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStatePending, Managed: true, EmployeeNumber: p.EmployeeNumber,
		Invitation: contracts.Invitation{State: contracts.InvitationNone}})
	workerLink(t, w.h, p.Id, u, "pending")
	w.h.Exec(t, `UPDATE staff.identity_links SET last_error = 'accounts_unavailable', attempts = 3 WHERE person_id = $1`, p.Id)
	id := "accountFailing/" + strconv.FormatInt(p.Id, 10)
	if _, ok := hasItem(t, w.hr, id); !ok {
		t.Fatalf("HR's items lack %s with the flag on", id)
	}
	acc := getAccount(t, w.hr, p.Id)
	do(t, w.hr, http.MethodPut, accountPath(p.Id), map[string]any{"provisionAccount": false}, http.StatusOK, nil, ifMatch(acc.Revision))
	if item, ok := hasItem(t, w.hr, id); ok {
		t.Errorf("with the flag off HR still reads %+v", item)
	}
}

// A failure after the claim releases the lease (review L3): the facts
// unreadable, Disable now and the worker's cycle leave no live lease, so the
// next request is not accounts_busy.
func TestAccountsRun_AFailureAfterTheClaimReleasesTheLease(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Slipp", "2026-01-01", nil)
	u := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStateActive, Managed: true, EmployeeNumber: p.EmployeeNumber})
	workerLink(t, w.h, p.Id, u, "active")
	w.h.Exec(t, `ALTER TABLE staff.settings RENAME TO settings_away`)
	_ = accountsWorker(t, w.h, w.h.Deps()).RunCycle(context.Background())
	if mustLeased(t, w.h, p.Id) {
		t.Error("the cycle left a live lease after its facts read failed")
	}
	if r := w.hr.Do(http.MethodPost, accountDisablePath(p.Id), nil, modtest.SkipContract("a fault the test injected")); r.Status != http.StatusInternalServerError {
		t.Fatalf("Disable now with the facts unreadable: %d, want 500", r.Status)
	}
	w.h.Exec(t, `ALTER TABLE staff.settings_away RENAME TO settings`)
	do(t, w.hr, http.MethodPost, accountDisablePath(p.Id), nil, http.StatusOK, nil)
	if ref := w.fake.user(u); ref.State != contracts.AccountStateDisabled {
		t.Errorf("the user = %+v, want disabled", ref)
	}
}

// A person's manager without staff:manage is refused all three (review
// L6), as the contract's access rule says.
func TestAccountsRun_AManagerIsRefused(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	boss := createPerson(t, w.hr, newPerson("Sjef", "Sjefsen"))
	seedEmploymentDated(t, w.h, boss.Id, 0, "2020-01-01", nil)
	bossC, bossUser := signIn(t, w.h)
	link(t, w.h, boss.Id, bossUser)
	p := createPerson(t, w.hr, newPerson("Ansatt", "Konto"))
	seedEmploymentDated(t, w.h, p.Id, boss.Id, "2026-01-01", nil)
	for _, path := range []string{accountsRunPath, accountRunPath(p.Id), accountDisablePath(p.Id)} {
		if r := bossC.Do(http.MethodPost, path, nil); r.Status != http.StatusForbidden {
			t.Errorf("the manager's POST %s: %d, want 403", path, r.Status)
		}
	}
	if n := len(w.fake.writeCalls()); n != 0 {
		t.Errorf("writes = %v, want none", w.fake.writeCalls())
	}
}

// A refusal identity answers to Disable now (re-check MEDIUM-1, final): the
// marker is kept, the refusal is the link's reason and raises its own item —
// never accountFailing, even ten minutes on — and the disable is tried again
// on the daily schedule, not at every poll.
func TestAccountsDisable_ARefusalIsAnAnswer(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, code string
		item       gen.StaffStatsAttentionItemType
		seed       func(w accountsWorld, p gen.StaffPerson)
	}{
		{"privileged", "account_privileged", gen.AccountPrivileged, func(w accountsWorld, p gen.StaffPerson) {
			u := uuid.New()
			w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStateActive, Managed: true, Privileged: true, EmployeeNumber: p.EmployeeNumber})
			workerLink(t, w.h, p.Id, u, "active")
		}},
		{"no longer managed", "account_not_managed", gen.AccountNotManaged, func(w accountsWorld, p gen.StaffPerson) {
			u := uuid.New()
			w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStateActive, EmployeeNumber: p.EmployeeNumber})
			workerLink(t, w.h, p.Id, u, "active")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newAccountsWorld(t)
			p := w.employee(t, "Svar", "2026-01-01", nil)
			tc.seed(w, p)
			refused(t, w.hr.Do(http.MethodPost, accountDisablePath(p.Id), nil), http.StatusConflict, tc.code)
			if disableMarker(t, w.h, p.Id) == nil {
				t.Error("the refusal cleared the marker")
			}
			w.h.Advance(11 * time.Minute)
			id := strconv.FormatInt(p.Id, 10)
			if item, ok := hasItem(t, w.hr, "accountFailing/"+id); ok {
				t.Errorf("a refusal raised %+v", item)
			}
			if _, ok := hasItem(t, w.hr, string(tc.item)+"/"+id); !ok {
				t.Errorf("HR's items lack %s/%s", tc.item, id)
			}
			deactivations := countWrites(w.fake, "Deactivate")
			w.h.Advance(time.Hour)
			w.cycle(t)
			if n := countWrites(w.fake, "Deactivate"); n != deactivations {
				t.Errorf("the next poll called Deactivate %d more times, want the daily schedule", n-deactivations)
			}
			w.h.Advance(25 * time.Hour)
			w.cycle(t)
			if n := countWrites(w.fake, "Deactivate"); n != deactivations+1 {
				t.Errorf("a day on Deactivate was called %d more times, want once", n-deactivations)
			}
		})
	}
}

// The marker's step reads the user first (re-check MEDIUM-1): a disable that
// failed, after which an Owner disabled the user in identity, is cleared by
// the next cycle with no call, and accountFailing goes.
func TestAccountsDisable_AUserDisabledMeanwhileClearsTheMarker(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Eiervekk", "2026-01-01", nil)
	u := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStateActive, Managed: true, EmployeeNumber: p.EmployeeNumber})
	workerLink(t, w.h, p.Id, u, "active")
	w.fake.failWith("Deactivate", errFakeDown)
	refused(t, w.hr.Do(http.MethodPost, accountDisablePath(p.Id), nil), http.StatusServiceUnavailable, "accounts_unavailable")
	ref := w.fake.user(u)
	ref.State, ref.DisabledBy = contracts.AccountStateDisabled, contracts.DisabledByOwner
	w.fake.setUser(ref)
	deactivations := countWrites(w.fake, "Deactivate")
	w.h.Advance(time.Hour + time.Minute)
	w.cycle(t)
	if n := countWrites(w.fake, "Deactivate"); n != deactivations {
		t.Errorf("the cycle called Deactivate %d more times for a user already disabled", n-deactivations)
	}
	if m := disableMarker(t, w.h, p.Id); m != nil {
		t.Errorf("the marker is still set: %v", m)
	}
	if item, ok := hasItem(t, w.hr, "accountFailing/"+strconv.FormatInt(p.Id, 10)); ok {
		t.Errorf("still raised: %+v", item)
	}
}

// An erased person whose flag is on (re-check LOW-1) does not let the
// marker lapse: the user is deactivated.
func TestAccountsDisable_AnErasedPersonsMarkerDoesNotLapse(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Påslått", "2020-01-01", nil)
	u := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStateActive, Managed: true, EmployeeNumber: p.EmployeeNumber})
	workerLink(t, w.h, p.Id, u, "active")
	w.h.Exec(t, `UPDATE staff.identity_links SET disable_requested_at = $2 WHERE person_id = $1`, p.Id, w.h.Now())
	w.h.Exec(t, `UPDATE staff.people SET erased_at = $2 WHERE id = $1`, p.Id, w.h.Now())
	w.cycle(t)
	if ref := w.fake.user(u); ref.State != contracts.AccountStateDisabled {
		t.Errorf("the erased person's user = %+v, want disabled", ref)
	}
	if m := disableMarker(t, w.h, p.Id); m != nil {
		t.Errorf("the marker is still set: %v", m)
	}
}

// A worker's link carrying the marker is not HR's to relink or unlink
// (re-check LOW-2): both are link_managed and the marker is kept, so the
// clearing in SetPersonLink only ever meets a self-service link.
func TestAccountsDisable_AMarkedLinkIsNotRelinked(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Merket", "2026-01-01", nil)
	u := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStateActive, Managed: true, EmployeeNumber: p.EmployeeNumber})
	workerLink(t, w.h, p.Id, u, "active")
	w.h.Exec(t, `UPDATE staff.identity_links SET disable_requested_at = $2 WHERE person_id = $1`, p.Id, w.h.Now())
	_, other := signIn(t, w.h)
	w.fake.setUser(contracts.AccountRef{UserID: other, State: contracts.AccountStateActive})
	refused(t, w.hr.Do(http.MethodPut, userPath(p.Id), map[string]any{"userId": other}), http.StatusConflict, "link_managed")
	refused(t, w.hr.Do(http.MethodPut, userPath(p.Id), map[string]any{"userId": nil}), http.StatusConflict, "link_managed")
	if disableMarker(t, w.h, p.Id) == nil {
		t.Error("a refused relink cleared the marker")
	}
}

// The safety net a kept marker is (re-check MEDIUM-1, final): Disable now
// refused account_not_managed, an Owner adopts the user again, and the
// worker's next daily try disables them; the item goes.
func TestAccountsDisable_AReadoptedUserIsDisabledByTheNextTry(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Tilbake", "2026-01-01", nil)
	u := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStateActive, EmployeeNumber: p.EmployeeNumber})
	workerLink(t, w.h, p.Id, u, "active")
	refused(t, w.hr.Do(http.MethodPost, accountDisablePath(p.Id), nil), http.StatusConflict, "account_not_managed")
	ref := w.fake.user(u)
	ref.Managed, ref.EmployeeNumber = true, p.EmployeeNumber // adoption names the number
	w.fake.setUser(ref)
	w.h.Advance(25 * time.Hour)
	w.cycle(t)
	if got := w.fake.user(u); got.State != contracts.AccountStateDisabled {
		t.Errorf("after the re-adoption the user = %+v, want disabled", got)
	}
	if disableMarker(t, w.h, p.Id) != nil {
		t.Error("the marker is still set")
	}
	if item, ok := hasItem(t, w.hr, "accountNotManaged/"+strconv.FormatInt(p.Id, 10)); ok {
		t.Errorf("still raised: %+v", item)
	}
}

// A refusal, then an Owner disables the user in identity: the next try reads
// them disabled and clears the marker and the item with no call.
func TestAccountsDisable_ARefusalThenAnOwnersDisableClears(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Eierstopp", "2026-01-01", nil)
	u := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStateActive, Managed: true, Privileged: true, EmployeeNumber: p.EmployeeNumber})
	workerLink(t, w.h, p.Id, u, "active")
	refused(t, w.hr.Do(http.MethodPost, accountDisablePath(p.Id), nil), http.StatusConflict, "account_privileged")
	ref := w.fake.user(u)
	ref.State, ref.DisabledBy = contracts.AccountStateDisabled, contracts.DisabledByOwner
	w.fake.setUser(ref)
	deactivations := countWrites(w.fake, "Deactivate")
	w.h.Advance(25 * time.Hour)
	w.cycle(t)
	if n := countWrites(w.fake, "Deactivate"); n != deactivations {
		t.Errorf("Deactivate called %d more times for a user an Owner disabled", n-deactivations)
	}
	if disableMarker(t, w.h, p.Id) != nil {
		t.Error("the marker is still set")
	}
	if item, ok := hasItem(t, w.hr, "accountPrivileged/"+strconv.FormatInt(p.Id, 10)); ok {
		t.Errorf("still raised: %+v", item)
	}
}

// An old refusal from when the flag was on raises nothing once a plain PUT
// turns the flag off (re-check LOW-1): flag-off items come from a marker.
func TestAccountAttention_AnOldRefusalRaisesNothingWithTheFlagOff(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Gammel", "2026-01-01", nil)
	seedWorkerLink(t, w.h, p.Id, "blocked", "account_privileged", "", 0)
	id := "accountPrivileged/" + strconv.FormatInt(p.Id, 10)
	if _, ok := hasItem(t, w.hr, id); !ok {
		t.Fatalf("HR's items lack %s with the flag on", id)
	}
	acc := getAccount(t, w.hr, p.Id)
	do(t, w.hr, http.MethodPut, accountPath(p.Id), map[string]any{"provisionAccount": false}, http.StatusOK, nil, ifMatch(acc.Revision))
	if item, ok := hasItem(t, w.hr, id); ok {
		t.Errorf("with the flag off HR still reads %+v", item)
	}
}

// A marked link whose user the directory keeps inactive (re-check LOW-2) is
// not taken for disabled: Deactivate is called once and Staff's own record
// disables the user.
func TestAccountsDisable_ADirectoryInactiveUserIsStillDisabled(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Katalog", "2026-01-01", nil)
	u := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStateDisabled, DisabledBy: contracts.DisabledByDirectory,
		Managed: true, EmployeeNumber: p.EmployeeNumber})
	workerLink(t, w.h, p.Id, u, "active")
	w.h.Exec(t, `UPDATE staff.identity_links SET disable_requested_at = $2 WHERE person_id = $1`, p.Id, w.h.Now())
	w.h.Exec(t, `UPDATE staff.people SET provision_account = false WHERE id = $1`, p.Id)
	w.cycle(t)
	if n := countWrites(w.fake, "Deactivate"); n != 1 {
		t.Errorf("Deactivate called %d times, want once", n)
	}
	if !w.fake.staffDisabledUser(u) {
		t.Error("Staff's own record of the user is not disabled")
	}
	if disableMarker(t, w.h, p.Id) != nil {
		t.Error("the marker is still set")
	}
}

// Disable now on a link whose user carries another employee number (the
// re-check's INFO): someone else's account — nothing is done to it, 409
// account_mismatch, the link blocked mismatch and accountMismatch shown; the
// marker is kept, so the disable runs once HR has put the number right.
func TestAccountsDisable_AMismatchedUserIsNotDisabled(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Feilnr", "2026-01-01", nil)
	u := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStateActive, Managed: true, EmployeeNumber: "SOMEONE-ELSE"})
	workerLink(t, w.h, p.Id, u, "active")
	refused(t, w.hr.Do(http.MethodPost, accountDisablePath(p.Id), nil), http.StatusConflict, "account_mismatch")
	if n := countWrites(w.fake, "Deactivate"); n != 0 {
		t.Errorf("Deactivate called %d times for someone else's account", n)
	}
	if l := mustLink(t, w.h, p.Id); l.State != "blocked" || l.Reason != "mismatch" {
		t.Errorf("the link = %+v, want blocked mismatch", l)
	}
	if _, ok := hasItem(t, w.hr, "accountMismatch/"+strconv.FormatInt(p.Id, 10)); !ok {
		t.Error("no accountMismatch item")
	}
	w.h.Exec(t, `UPDATE staff.people SET employee_number = 'SOMEONE-ELSE' WHERE id = $1`, p.Id)
	w.h.Advance(25 * time.Hour)
	w.cycle(t)
	if got := w.fake.user(u); got.State != contracts.AccountStateDisabled {
		t.Errorf("once the number matches the user = %+v, want disabled", got)
	}
}

// Disable now on HR's own self-service link (the Task 6 review): a link the
// worker never acts on. The flag is turned off as asked, but no disable is
// recorded and identity is not called: 409 account_not_managed, the flag
// off; no item ten minutes on; and an Owner adopting the user afterwards
// does not get them disabled — HR unlinks a hand link, or an Owner
// disables the user in identity.
func TestAccountsDisable_ASelfServiceLinkIsNotDisabled(t *testing.T) {
	t.Parallel()
	for _, flag := range []bool{true, false} {
		t.Run(fmt.Sprintf("flag %v", flag), func(t *testing.T) {
			w := newAccountsWorld(t)
			p := w.employee(t, "Hand", "2026-01-01", nil)
			if !flag {
				w.h.Exec(t, `UPDATE staff.people SET provision_account = false WHERE id = $1`, p.Id)
			}
			u := uuid.New()
			w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStateActive})
			link(t, w.h, p.Id, u)
			refused(t, w.hr.Do(http.MethodPost, accountDisablePath(p.Id), nil), http.StatusConflict, "account_not_managed")
			if acc := getAccount(t, w.hr, p.Id); acc.ProvisionAccount {
				t.Error("the flag is on")
			}
			if m := disableMarker(t, w.h, p.Id); m != nil {
				t.Errorf("a disable was recorded on a hand link: %v", m)
			}
			if n := countWrites(w.fake, "Deactivate"); n != 0 {
				t.Errorf("Deactivate called %d times for a hand link", n)
			}
			w.h.Advance(11 * time.Minute)
			if a := accountItems(getAttention(t, w.hr)); len(a) != 0 {
				t.Errorf("items for a hand link: %+v", a)
			}
			ref := w.fake.user(u)
			ref.Managed, ref.EmployeeNumber = true, p.EmployeeNumber
			w.fake.setUser(ref)
			w.h.Advance(25 * time.Hour)
			w.cycle(t)
			if got := w.fake.user(u); got.State != contracts.AccountStateActive {
				t.Errorf("after an Owner's adoption the user = %+v, want left active", got)
			}
		})
	}
}
