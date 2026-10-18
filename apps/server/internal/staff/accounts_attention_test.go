package staff_test

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// The account attention items (plan Task 4c): staff:manage's alone, derived
// from the people and their links as the worker wrote them — no directory or
// accounts call at read time.

// accountItemTypes is every account item's type.
var accountItemTypes = []gen.StaffStatsAttentionItemType{
	gen.AccountNoEmployment, gen.AccountEmailMissing, gen.AccountNotFound, gen.AccountNotManaged,
	gen.AccountBlockedByOwner, gen.AccountDisabledByOwner, gen.AccountUserGone, gen.AccountPrivileged,
	gen.AccountMismatch, gen.AccountModeChanged, gen.AccountEmailDiffers, gen.AccountEmailTaken,
	gen.AccountNumberTaken, gen.AccountFailing, gen.InvitationUnanswered, gen.InvitationRevoked,
	gen.EmployeeRoleMissing, gen.EmployeeRoleUnsafe, gen.AccountModeMismatch,
}

// accountItems is the account items among items.
func accountItems(items []gen.StaffStatsAttentionItem) []gen.StaffStatsAttentionItem {
	var out []gen.StaffStatsAttentionItem
	for _, i := range items {
		if slices.Contains(accountItemTypes, i.Type) {
			out = append(out, i)
		}
	}
	return out
}

// seedWorkerLink writes a worker link for personID with a reason and an
// error, attempts, in state.
func seedWorkerLink(t *testing.T, h *harness, personID int64, state, reason, lastError string, attempts int) {
	t.Helper()
	nilIf := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	h.Exec(t, `
		INSERT INTO staff.identity_links (person_id, provider, external_id, state, managed, blocked_reason, last_error, attempts, created_at, updated_at)
		VALUES ($1, 'identity', $2, $3, true, $4, $5, $6, $7, $7)`,
		personID, uuid.New().String(), state, nilIf(reason), nilIf(lastError), attempts, h.Now())
}

// Each item's trigger, id, title and entity, for staff:manage; none of them
// for the person's manager, an Approver or staff:personal-data alone; none
// for a person whose flag is off or who is erased; and the read asks
// identity nothing.
func TestAccountAttention_EachItemAndItsAudience(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	boss := createPerson(t, w.hr, newPerson("Sjef", "Sjefsen"))
	seedEmploymentDated(t, w.h, boss.Id, 0, "2020-01-01", nil)
	bossC, bossUser := signIn(t, w.h)
	link(t, w.h, boss.Id, bossUser)
	approver, _ := signIn(t, w.h, "staff:approve")
	pd, _ := signIn(t, w.h, "staff:personal-data")

	type want struct {
		kind gen.StaffStatsAttentionItemType
		id   int64
		name string
	}
	var wants []want
	person := func(given string, employed bool) gen.StaffPerson {
		t.Helper()
		p := createPerson(t, w.hr, newPerson(given, "Varsel"))
		w.h.Exec(t, `UPDATE staff.people SET work_email = $2 WHERE id = $1`, p.Id, strings.ToLower(given)+".varsel@example.test")
		if employed {
			seedEmploymentDated(t, w.h, p.Id, boss.Id, "2026-01-01", nil)
		}
		flagOn(t, w.hr, p.Id)
		return p
	}
	for _, tc := range []struct {
		kind                     gen.StaffStatsAttentionItemType
		state, reason, lastError string
		attempts                 int
	}{
		{gen.AccountNotFound, "blocked", "not_found", "", 0},
		{gen.AccountNotManaged, "active", "account_not_managed", "", 0},
		{gen.AccountBlockedByOwner, "blocked", "blocked_by_owner", "", 0},
		{gen.AccountDisabledByOwner, "blocked", "disabled_by_owner", "", 0},
		{gen.AccountUserGone, "failed", "user_gone", "", 0},
		{gen.AccountPrivileged, "blocked", "account_privileged", "", 0},
		{gen.AccountMismatch, "blocked", "mismatch", "", 0},
		{gen.AccountModeChanged, "pending", "mode_changed", "", 0},
		{gen.AccountEmailDiffers, "active", "email_differs", "", 0},
		{gen.AccountEmailTaken, "blocked", "account_email_taken", "", 0},
		{gen.AccountNumberTaken, "blocked", "employee_number_taken", "", 0},
		{gen.InvitationUnanswered, "pending", "invitation_unanswered", "", 0},
		{gen.InvitationRevoked, "pending", "invitation_revoked", "", 0},
		{gen.AccountFailing, "pending", "", "accounts_unavailable", 3},
		{gen.AccountFailing, "failed", "invalid_actor", "", 0},
	} {
		p := person(string(tc.kind)+strconv.Itoa(len(wants)), true)
		seedWorkerLink(t, w.h, p.Id, tc.state, tc.reason, tc.lastError, tc.attempts)
		wants = append(wants, want{tc.kind, p.Id, p.DisplayName})
	}
	noJob := person("Jobbløs", false)
	wants = append(wants, want{gen.AccountNoEmployment, noJob.Id, noJob.DisplayName})
	noMail := person("Postløs", true)
	w.h.Exec(t, `UPDATE staff.people SET work_email = NULL WHERE id = $1`, noMail.Id)
	wants = append(wants, want{gen.AccountEmailMissing, noMail.Id, noMail.DisplayName})

	// What raises nothing: two failed attempts, a flag off, an erased person,
	// a leaver without the address.
	twice := person("Toganger", true)
	seedWorkerLink(t, w.h, twice.Id, "pending", "", "accounts_unavailable", 2)
	off := person("Avslått", true)
	seedWorkerLink(t, w.h, off.Id, "blocked", "mismatch", "", 0)
	w.h.Exec(t, `UPDATE staff.people SET provision_account = false WHERE id = $1`, off.Id)
	erased := person("Slettet", true)
	seedWorkerLink(t, w.h, erased.Id, "blocked", "mismatch", "", 0)
	w.h.Exec(t, `UPDATE staff.people SET erased_at = $2 WHERE id = $1`, erased.Id, w.h.Now())
	leaver := createPerson(t, w.hr, newPerson("Sluttet", "Varsel"))
	seedEmploymentDated(t, w.h, leaver.Id, 0, "2020-01-01", dayPtr("2026-06-30"))
	flagOn(t, w.hr, leaver.Id)

	// The installation's items: one each, counting the links.
	for _, reason := range []string{"employee_role_missing", "employee_role_missing", "employee_role_unsafe", "mode_mismatch"} {
		seedWorkerLink(t, w.h, person("Install"+strconv.Itoa(len(wants)), true).Id, "pending", reason, "", 0)
		wants = append(wants, want{})
	}

	reader, readerID := signIn(t, w.h, "staff:manage")
	got := accountItems(getAttention(t, reader))
	for _, c := range contractCalls.by(readerID) {
		if strings.HasPrefix(c.method, "Accounts.") {
			t.Errorf("the attention read asked identity: %s", c.method)
		}
	}
	for _, wt := range wants {
		if wt.kind == "" {
			continue
		}
		id := string(wt.kind) + "/" + strconv.FormatInt(wt.id, 10)
		i := slices.IndexFunc(got, func(i gen.StaffStatsAttentionItem) bool { return i.Id == id })
		if i < 0 {
			t.Errorf("HR's items lack %s", id)
			continue
		}
		if it := got[i]; it.Type != wt.kind || it.Title != wt.name || it.EntityId != strconv.FormatInt(wt.id, 10) || it.Count != nil {
			t.Errorf("item %s = %+v, want type %s, title %q, entity the person", id, it, wt.kind, wt.name)
		}
	}
	for _, inst := range []struct {
		kind  gen.StaffStatsAttentionItemType
		count int32
	}{{gen.EmployeeRoleMissing, 2}, {gen.EmployeeRoleUnsafe, 1}, {gen.AccountModeMismatch, 1}} {
		items := ofType(got, inst.kind)
		if len(items) != 1 || items[0].Id != string(inst.kind) || items[0].EntityId != "settings" || items[0].Count == nil || *items[0].Count != inst.count {
			t.Errorf("%s items = %+v, want one, entity settings, count %d", inst.kind, items, inst.count)
		}
	}
	for _, p := range []int64{twice.Id, off.Id, erased.Id, leaver.Id} {
		for _, it := range got {
			if it.EntityId == strconv.FormatInt(p, 10) {
				t.Errorf("an item for person %d: %+v", p, it)
			}
		}
	}
	if n := len(got); n != len(wants)-4+3 {
		t.Errorf("%d account items, want %d: %+v", n, len(wants)-4+3, got)
	}
	for name, c := range map[string]*modtest.Client{"the manager": bossC, "an Approver": approver, "staff:personal-data alone": pd} {
		var items []gen.StaffStatsAttentionItem
		do(t, c, http.MethodGet, attentionPath, nil, http.StatusOK, &items)
		if a := accountItems(items); len(a) > 0 {
			t.Errorf("%s reads account items: %+v", name, a)
		}
	}
}

// A flag-off leaver whose deactivation keeps failing raises accountFailing
// for HR, as a flag-on failure does (the coordinator, on the D2 gap): the
// worker deactivates such a leaver whatever the flag, so a failure there is a
// leaver keeping access. Three failed polls raise it, and it clears once the
// deactivation succeeds. A flag-off link with no leaver — an employment still
// open — raises nothing for its failures (L1's rule kept).
func TestAccountAttention_AFlagOffLeaversFailingDeactivationIsRaised(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	end := shiftDay(t, today(t, w.h), -1)
	leaver := createPerson(t, w.hr, with(newPerson("Gått", "Ut"), "workEmail", "gatt.ut@example.test"))
	seedEmploymentDated(t, w.h, leaver.Id, 0, shiftDay(t, today(t, w.h), -100), &end)
	u := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStateActive, Managed: true,
		EmployeeNumber: leaver.EmployeeNumber, HasCredential: true, Invitation: contracts.Invitation{State: contracts.InvitationNone}})
	workerLink(t, w.h, leaver.Id, u, "active")
	stayer := createPerson(t, w.hr, with(newPerson("Blir", "Her"), "workEmail", "blir.her@example.test"))
	seedEmploymentDated(t, w.h, stayer.Id, 0, shiftDay(t, today(t, w.h), -100), nil)
	seedWorkerLink(t, w.h, stayer.Id, "active", "", "accounts_unavailable", 3)

	w.fake.failWith("Deactivate", errFakeDown)
	id := "accountFailing/" + strconv.FormatInt(leaver.Id, 10)
	for poll := 1; poll <= 3; poll++ {
		if poll > 1 {
			w.h.Advance(time.Hour)
		}
		w.cycle(t)
		if _, ok := hasItem(t, w.hr, id); ok != (poll == 3) {
			t.Fatalf("after %d failed polls HR reads %s: %v, want it from the third", poll, id, ok)
		}
	}
	if n := countWrites(w.fake, "Deactivate"); n != 3 {
		t.Errorf("Deactivate was tried %d times, want once a poll", n)
	}
	if item, ok := hasItem(t, w.hr, "accountFailing/"+strconv.FormatInt(stayer.Id, 10)); ok {
		t.Errorf("a flag-off link with no leaver raises %+v, want nothing", item)
	}

	w.fake.failWith("Deactivate", nil)
	w.h.Advance(time.Hour)
	w.cycle(t)
	if !w.fake.staffDisabledUser(u) {
		t.Fatal("the leaver was not deactivated once identity answered")
	}
	if item, ok := hasItem(t, w.hr, id); ok {
		t.Errorf("after the deactivation HR still reads %+v", item)
	}
}
