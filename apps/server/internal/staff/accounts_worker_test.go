package staff_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/staff"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// The staff-accounts worker (plan Task 4b): a cycle inserts a preparing link
// for each person whose account is due, claims due links one at a time, reads
// the person in a snapshot it closes, asks identity on no connection, runs
// the decision table's steps — up to three, chained — and writes the result
// under the person's lock, on the lease it claimed with.

// oslo is the settings' default business time zone.
var oslo = mustZone("Europe/Oslo")

func mustZone(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// osloAt is the instant of a civil day and hour in Oslo.
func osloAt(day string, hour int) time.Time {
	d, err := time.ParseInLocation(time.DateOnly, day, oslo)
	if err != nil {
		panic(err)
	}
	return d.Add(time.Duration(hour) * time.Hour)
}

// dayPtr is a civil day for seedEmploymentDated's end.
func dayPtr(s string) *string { return &s }

// accountsWorker is the staff-accounts worker of h's composition built from
// d, as worker mode builds it: Accounts resolved onto it (Harness.Workers).
func accountsWorker(t *testing.T, h *harness, d module.Deps) *staff.AccountsWorker {
	t.Helper()
	for _, w := range h.Workers(d) {
		if a, ok := w.(*staff.AccountsWorker); ok {
			return a
		}
	}
	t.Fatal("no staff-accounts worker")
	return nil
}

// runAccounts runs one accounts cycle of h's worker and fails t on its error.
func runAccounts(t *testing.T, h *harness) {
	t.Helper()
	if err := accountsWorker(t, h, h.Deps()).RunCycle(context.Background()); err != nil {
		t.Fatalf("accounts cycle: %v", err)
	}
}

// longSessions keeps every session alive for a year of the harness clock,
// for a test that moves the clock by weeks and keeps its callers signed in.
func longSessions() []modtest.Option {
	var out []modtest.Option
	for _, key := range []string{"SESSION_IDLE_TIMEOUT", "SESSION_PRIVILEGED_IDLE_TIMEOUT", "SESSION_ABSOLUTE_LIFETIME", "SESSION_PRIVILEGED_ABSOLUTE_LIFETIME"} {
		out = append(out, modtest.WithEnv(key, "8760h"))
	}
	return out
}

// accountsWorld is one installation whose accounts are a fake provider.
type accountsWorld struct {
	h    *harness
	hr   *modtest.Client
	fake *fakeAccounts
}

func newAccountsWorld(t *testing.T, opts ...modtest.Option) accountsWorld {
	t.Helper()
	fake := newFakeAccounts()
	h := newHarness(t, append(append([]modtest.Option{withAccounts(fake)}, longSessions()...), opts...)...)
	hr, _ := signIn(t, h, "staff:manage", "staff:personal-data")
	return accountsWorld{h: h, hr: hr, fake: fake}
}

// cycle runs one accounts cycle on the world's own Deps.
func (w accountsWorld) cycle(t *testing.T) {
	t.Helper()
	runAccounts(t, w.h)
}

// employee is a person with a work email, one employment from start to end
// (nil: open) and the account flag on.
func (w accountsWorld) employee(t *testing.T, given, start string, end *string) gen.StaffPerson {
	t.Helper()
	return accountEmployee(t, w.h, w.hr, given, start, end)
}

func accountEmployee(t *testing.T, h *harness, hr *modtest.Client, given, start string, end *string) gen.StaffPerson {
	t.Helper()
	email := fmt.Sprintf("%s.%d@example.test", strings.ToLower(given), people.Add(1))
	p := createPerson(t, hr, with(newPerson(given, "Konto"), "workEmail", email))
	seedEmploymentDated(t, h, p.Id, 0, start, end)
	flagOn(t, hr, p.Id)
	return p
}

// flagOn turns personID's account flag on, its date derived.
func flagOn(t *testing.T, hr *modtest.Client, personID int64) {
	t.Helper()
	acc := getAccount(t, hr, personID)
	do(t, hr, http.MethodPut, accountPath(personID), map[string]any{"provisionAccount": true}, http.StatusOK, nil, ifMatch(acc.Revision))
}

// accountLink is the worker's columns of a person's link, as one string a
// test compares: state, managed, the user (or -), blocked_reason, last_error,
// attempts.
type accountLink struct {
	State, Reason, LastError, User, Synced string
	Managed                                bool
	Attempts                               int
	NextAttempt                            *time.Time
	Leased                                 bool
}

func linkOf(t *testing.T, h *harness, personID int64) (accountLink, bool) {
	t.Helper()
	var l accountLink
	err := h.Pool().QueryRow(context.Background(), `
		SELECT state, managed, coalesce(external_id, ''), coalesce(blocked_reason, ''), coalesce(last_error, ''), attempts,
			next_attempt_at, coalesce(synced_employee_number, ''), lease_id IS NOT NULL OR lease_until IS NOT NULL
		FROM staff.identity_links WHERE person_id = $1 AND provider = 'identity'`, personID).
		Scan(&l.State, &l.Managed, &l.User, &l.Reason, &l.LastError, &l.Attempts, &l.NextAttempt, &l.Synced, &l.Leased)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return accountLink{}, false
		}
		t.Fatalf("read person %d's link: %v", personID, err)
	}
	return l, true
}

func mustLink(t *testing.T, h *harness, personID int64) accountLink {
	t.Helper()
	l, ok := linkOf(t, h, personID)
	if !ok {
		t.Fatalf("person %d has no link", personID)
	}
	if l.Leased {
		t.Errorf("person %d's link is still leased after the cycle", personID)
	}
	return l
}

// workerLink writes a link in one of the worker's states to user, managed,
// due now.
func workerLink(t *testing.T, h *harness, personID int64, user uuid.UUID, state string) {
	t.Helper()
	managedLink(t, h, personID, user, state)
}

// countWrites is how many of the fake's writes so far start with prefix.
func countWrites(f *fakeAccounts, prefix string) int {
	n := 0
	for _, c := range f.writeCalls() {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// The whole flow (spec §10, plan Task 4b) against identity's real provider:
// the flag on, nothing before the window; createBeforeDays (14) before the
// start a pending, disabled user is prepared and linked; on the start day it
// is enabled under the employee role and invited; the invitation accepted
// through identity's own endpoint, the user signs in and reads My page; the
// day after the end the user is disabled and signed out; a rehire re-enables
// them on the new start.
func TestAccountsWorker_TheWholeFlowAgainstIdentity(t *testing.T) {
	t.Parallel()
	h := newHarness(t, longSessions()...)
	hr, _ := signIn(t, h, "staff:manage")
	role := uuid.New()
	h.Exec(t, `INSERT INTO identity.roles (id, name, normalized_name, display_name, version, created_at, updated_at)
	           VALUES ($1, 'Employee', 'EMPLOYEE', 'Employee', $2, $3, $3)`, role, uuid.New(), h.Now())
	h.Exec(t, `INSERT INTO identity.role_permissions (role_id, permission_key) VALUES ($1, 'staff:access')`, role)
	h.Exec(t, `INSERT INTO identity.provisioning_roles (role_id, created_at) VALUES ($1, $2)`, role, h.Now())

	const email = "kari.konto@example.test"
	p := createPerson(t, hr, with(newPerson("Kari", "Konto"), "workEmail", email))
	seedEmploymentDated(t, h, p.Id, 0, "2026-10-01", dayPtr("2026-10-31"))
	flagOn(t, hr, p.Id)
	user := func() (disabled bool, by, number string, pending bool) {
		t.Helper()
		l := mustLink(t, h, p.Id)
		err := h.Pool().QueryRow(context.Background(), `
			SELECT is_disabled, coalesce(disabled_by, ''), coalesce(employee_number, ''), pending_since IS NOT NULL
			FROM identity.users WHERE id = $1`, l.User).Scan(&disabled, &by, &number, &pending)
		if err != nil {
			t.Fatalf("read the linked user: %v", err)
		}
		return
	}

	// Twenty days ahead: nothing is due.
	runAccounts(t, h)
	if _, ok := linkOf(t, h, p.Id); ok {
		t.Fatal("a link before the window opened")
	}

	// Fourteen days ahead: prepared, pending and disabled by Staff, not invited.
	setClock(t, h, osloAt("2026-09-17", 8))
	runAccounts(t, h)
	if l := mustLink(t, h, p.Id); l.State != "pending" || !l.Managed || l.User == "" || l.Synced != p.EmployeeNumber {
		t.Fatalf("in the window the link = %+v, want pending, managed, naming the user, synced %s", l, p.EmployeeNumber)
	}
	if disabled, by, number, pending := user(); !disabled || by != "staff" || !pending || number != p.EmployeeNumber {
		t.Errorf("the prepared user = disabled %v by %q, pending %v, number %q; want disabled by staff, pending, %s", disabled, by, pending, number, p.EmployeeNumber)
	}
	box := h.Deps().Mail.(*mail.Fake)
	if n := len(invitesTo(box, email)); n != 0 {
		t.Errorf("%d mails before the start, want none", n)
	}

	// The start day: enabled and invited, still pending until accepted.
	setClock(t, h, osloAt("2026-10-01", 7))
	runAccounts(t, h)
	if disabled, _, _, pending := user(); disabled || !pending {
		t.Errorf("on the start day the user = disabled %v pending %v, want enabled and pending", disabled, pending)
	}
	mails := invitesTo(box, email)
	if len(mails) != 1 {
		t.Fatalf("%d invitations on the start day, want one", len(mails))
	}
	_, raw, _ := strings.Cut(mails[0].TextBody, "?token=")
	token, err := url.QueryUnescape(strings.TrimSpace(raw))
	if err != nil {
		t.Fatalf("the invitation's token: %v", err)
	}
	kari := h.Client(t)
	const password = "IntegrationUserPassword123"
	if r := kari.Do(http.MethodPost, "/api/v1/identity/invitations/accept", map[string]any{"token": token, "password": password},
		modtest.SkipContract("identity's own operation")); r.Status != http.StatusCreated {
		t.Fatalf("accept: %d %s", r.Status, r.Body)
	}
	if r := kari.Do(http.MethodPost, "/api/v1/identity/login", map[string]any{"email": email, "password": password},
		modtest.SkipContract("identity's own operation")); r.Status != http.StatusOK {
		t.Fatalf("login: %d %s", r.Status, r.Body)
	}
	var me gen.StaffPerson
	do(t, kari, http.MethodGet, "/api/v1/staff/me", nil, http.StatusOK, &me)
	if me.Id != p.Id {
		t.Errorf("My page is person %d, want %d", me.Id, p.Id)
	}
	setClock(t, h, osloAt("2026-10-02", 7))
	runAccounts(t, h)
	if l := mustLink(t, h, p.Id); l.State != "active" {
		t.Errorf("after the acceptance the link = %+v, want active", l)
	}

	// The day after the end: disabled by Staff, the session ended.
	setClock(t, h, osloAt("2026-11-01", 7))
	runAccounts(t, h)
	if l := mustLink(t, h, p.Id); l.State != "disabled" {
		t.Errorf("after the end the link = %+v, want disabled", l)
	}
	if disabled, by, _, _ := user(); !disabled || by != "staff" {
		t.Errorf("after the end the user = disabled %v by %q, want disabled by staff", disabled, by)
	}
	if r := kari.Do(http.MethodGet, "/api/v1/staff/me", nil); r.Status != http.StatusUnauthorized {
		t.Errorf("My page after the end: %d, want 401", r.Status)
	}

	// A rehire: enabled again on the new start.
	seedEmploymentDated(t, h, p.Id, 0, "2026-12-01", nil)
	setClock(t, h, osloAt("2026-12-01", 7))
	runAccounts(t, h)
	if l := mustLink(t, h, p.Id); l.State != "active" || l.Reason != "" {
		t.Errorf("after the rehire the link = %+v, want active", l)
	}
	if disabled, _, _, _ := user(); disabled {
		t.Error("after the rehire the user is still disabled")
	}
	got := kindsOf(eventsAs(t, hr, p.Id))
	for _, kind := range []string{"account_prepared", "account_activated", "account_deactivated"} {
		if !slices.Contains(got, kind) {
			t.Errorf("HR's history %v lacks %s", got, kind)
		}
	}
	if slices.Contains(got, "user_linked") {
		t.Errorf("the worker wrote user_linked: %v", got)
	}
}

// invitesTo is every mail the fake sender recorded to address.
func invitesTo(box *mail.Fake, address string) []mail.Message {
	var out []mail.Message
	for _, m := range box.Messages() {
		if m.To == address {
			out = append(out, m)
		}
	}
	return out
}

// With createBeforeDays 0 an account due today is prepared and enabled in
// one claim (plan I2): the steps chain on the fresh ref, at most three.
func TestAccountsWorker_DueTodayIsActiveOnDayOne(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	w.h.Exec(t, `UPDATE staff.settings SET create_before_days = 0`)
	p := w.employee(t, "Dag", today(t, w.h), nil)
	w.cycle(t)
	writes := w.fake.writeCalls()
	if len(writes) < 2 || writes[0] != "Prepare:"+p.EmployeeNumber || writes[1] != "Activate" || len(writes) > 3 {
		t.Fatalf("writes = %v, want Prepare then Activate in one claim, at most three", writes)
	}
	l := mustLink(t, w.h, p.Id)
	ref := w.fake.user(uuid.MustParse(l.User))
	if l.State != "pending" || ref.DisabledBy != "" || ref.Invitation.State != contracts.InvitationActive {
		t.Errorf("link %+v, user %+v; want pending, enabled and invited on day one", l, ref)
	}
	if l.NextAttempt == nil || !l.NextAttempt.Equal(osloAt("2026-09-13", 0)) {
		t.Errorf("next attempt = %v, want the next civil midnight in Oslo", l.NextAttempt)
	}
}

// Two cycles at once share the work by the row claims: while one is inside
// Prepare holding the link's lease, the other claims nothing and finishes;
// the user is prepared once.
func TestAccountsWorker_TwoCyclesAtOnce(t *testing.T) {
	w := newAccountsWorld(t)
	p := w.employee(t, "Tor", "2026-01-01", nil)
	inside, release := make(chan struct{}), make(chan struct{})
	var first atomic.Bool
	w.fake.whenWriting(func(method string, _ uuid.UUID) {
		if method == "Prepare" && first.CompareAndSwap(false, true) {
			close(inside)
			<-release
		}
	})
	done := make(chan error, 1)
	worker := accountsWorker(t, w.h, w.h.Deps())
	go func() { done <- worker.RunCycle(context.Background()) }()
	<-inside
	w.cycle(t)
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("the first cycle: %v", err)
	}
	if n := countWrites(w.fake, "Prepare"); n != 1 {
		t.Errorf("Prepare called %d times, want once", n)
	}
	if l := mustLink(t, w.h, p.Id); l.User == "" {
		t.Errorf("the link = %+v, want the prepared user", l)
	}
}

// A lease another cycle holds is skipped until it lapses (five minutes),
// then the link is claimed again.
func TestAccountsWorker_ALapsedLeaseIsClaimedAgain(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Lise", "2026-01-01", nil)
	w.h.Exec(t, `INSERT INTO staff.identity_links (person_id, provider, external_id, state, managed, lease_id, lease_until, created_at, updated_at)
		VALUES ($1, 'identity', NULL, 'preparing', true, $2, $3, $4, $4)`, p.Id, uuid.New(), w.h.Now().Add(5*time.Minute), w.h.Now())
	w.cycle(t)
	if n := len(w.fake.writeCalls()); n != 0 {
		t.Fatalf("a leased link was worked: %v", w.fake.writeCalls())
	}
	w.h.Advance(5*time.Minute + time.Second)
	w.cycle(t)
	if n := countWrites(w.fake, "Prepare"); n != 1 {
		t.Errorf("after the lease lapsed Prepare was called %d times, want once", n)
	}
	if l := mustLink(t, w.h, p.Id); l.User == "" {
		t.Errorf("the link = %+v, want the prepared user", l)
	}
}

// A result write finds the lease moved — another cycle claimed the link
// after this one's lapsed — and writes nothing: the link and the history
// are the other cycle's.
func TestAccountsWorker_AResultOnAMovedLeaseIsRefused(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Mona", "2026-01-01", nil)
	w.fake.whenWriting(func(method string, _ uuid.UUID) {
		if method == "Prepare" {
			w.h.Exec(t, `UPDATE staff.identity_links SET lease_id = $2 WHERE person_id = $1`, p.Id, uuid.New())
		}
	})
	w.cycle(t)
	if countWrites(w.fake, "Prepare") != 1 {
		t.Fatalf("writes = %v, want a Prepare", w.fake.writeCalls())
	}
	l, _ := linkOf(t, w.h, p.Id)
	if l.State != "preparing" || l.User != "" {
		t.Errorf("the link = %+v, want it as the other lease holder left it", l)
	}
	if got := kindsOf(eventsAs(t, w.hr, p.Id)); slices.Contains(got, "account_prepared") {
		t.Errorf("history %v records the refused result", got)
	}
}

// The person deleted while Prepare runs (pre-flight I4): the result write
// finds the link gone, and the user Prepare made is deactivated — outside any
// transaction — and logged.
func TestAccountsWorker_APersonDeletedMidPrepare(t *testing.T) {
	w := newAccountsWorld(t)
	p := w.employee(t, "Siri", "2026-10-01", nil)
	setClock(t, w.h, osloAt("2026-09-20", 8))
	w.fake.whenWriting(func(method string, _ uuid.UUID) {
		if method != "Prepare" {
			return
		}
		for _, sql := range []string{
			`DELETE FROM staff.identity_links WHERE person_id = $1`,
			`DELETE FROM staff.events WHERE person_id = $1`,
			`DELETE FROM staff.employment_terms WHERE employment_id IN (SELECT id FROM staff.employments WHERE person_id = $1)`,
			`DELETE FROM staff.employments WHERE person_id = $1`,
			`DELETE FROM staff.people WHERE id = $1`,
		} {
			w.h.Exec(t, sql, p.Id)
		}
	})
	w.cycle(t)
	writes := w.fake.writeCalls()
	if len(writes) == 0 || writes[0] != "Prepare:"+p.EmployeeNumber || writes[len(writes)-1] != "Deactivate" {
		t.Fatalf("writes = %v, want Prepare and, last, Deactivate", writes)
	}
	if !strings.Contains(w.h.Logs(), "the person's link was gone") {
		t.Error("the orphaned user's deactivation was not logged")
	}
}

// The flag turned off between the call and the write (plan M9): the result
// is written, its events carry flagOffMeanwhile, and the next cycle does
// nothing more.
func TestAccountsWorker_TheFlagTurnedOffMeanwhile(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	w.h.Exec(t, `UPDATE staff.settings SET create_before_days = 0`)
	p := w.employee(t, "Unn", today(t, w.h), nil)
	w.fake.whenWriting(func(method string, _ uuid.UUID) {
		if method == "Activate" {
			w.h.Exec(t, `UPDATE staff.people SET provision_account = false WHERE id = $1`, p.Id)
		}
	})
	w.cycle(t)
	activated := eventOfKind(t, eventsAs(t, w.hr, p.Id), "account_activated")
	if activated.Payload["flagOffMeanwhile"] != true || activated.Payload["step"] != "activate" {
		t.Errorf("account_activated payload = %v, want step activate and flagOffMeanwhile", activated.Payload)
	}
	before := len(w.fake.writeCalls())
	setClock(t, w.h, osloAt("2026-09-14", 8))
	w.cycle(t)
	if n := len(w.fake.writeCalls()); n != before {
		t.Errorf("the next cycle wrote %v, want nothing more", w.fake.writeCalls()[before:])
	}
}

// A cycle needs no second connection (the lock rule, B2): on a pool of one,
// the claim, the snapshot and the result write each take the one connection
// and give it back before identity is called. A cycle that held one while
// asking for another would wait for itself; the deadline makes that a
// failure.
func TestAccountsWorker_ACycleOnAPoolOfOne(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	w.h.Exec(t, `UPDATE staff.settings SET create_before_days = 0`)
	p := w.employee(t, "Ena", today(t, w.h), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	one, err := db.Open(ctx, w.h.Pool().Config().ConnString(), db.WithMaxConns(1))
	if err != nil {
		t.Fatalf("open a pool of one: %v", err)
	}
	defer one.Close()
	deps := w.h.Deps()
	deps.Pool = one
	if err := accountsWorker(t, w.h, deps).RunCycle(ctx); err != nil {
		t.Fatalf("a cycle on a pool of one: %v", err)
	}
	if l := mustLink(t, w.h, p.Id); l.State != "pending" || l.User == "" {
		t.Errorf("the link = %+v, want pending with the user", l)
	}
}

// No call to identity is made inside one of the module's transactions or
// snapshots, over a whole cycle that prepares, activates, updates and
// deactivates (the lock rule; the package recorder).
func TestAccountsWorker_NoCallInsideATransaction(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	w.h.Exec(t, `UPDATE staff.settings SET create_before_days = 0`)
	before := lockedContractCalls.count()
	w.employee(t, "Ny", today(t, w.h), nil)
	leaver := w.employee(t, "Gaatt", "2020-01-01", dayPtr("2026-06-30"))
	gone := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: gone, State: contracts.AccountStateActive, Managed: true, EmployeeNumber: leaver.EmployeeNumber})
	workerLink(t, w.h, leaver.Id, gone, "active")
	w.cycle(t)
	for _, call := range []string{"Prepare", "Activate", "Update", "Deactivate"} {
		if countWrites(w.fake, call) == 0 {
			t.Errorf("the cycle made no %s call: %v", call, w.fake.writeCalls())
		}
	}
	if calls := lockedContractCalls.since(before); len(calls) > 0 {
		t.Errorf("calls inside a transaction:\n%s", strings.Join(calls, "\n"))
	}
}

// A failure to reach identity is retried at each poll up to three attempts,
// then daily: the next civil midnight. last_error is a code.
func TestAccountsWorker_RetriesThreeTimesThenDaily(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Rita", "2026-01-01", nil)
	w.fake.failWith("Prepare", errFakeDown)
	w.cycle(t)
	l := mustLink(t, w.h, p.Id)
	if l.Attempts != 1 || l.LastError != "accounts_unavailable" || l.NextAttempt == nil || !l.NextAttempt.Equal(w.h.Now().Add(time.Hour)) {
		t.Fatalf("after one failure the link = %+v, want attempts 1, accounts_unavailable, the next poll", l)
	}
	w.cycle(t)
	if n := countWrites(w.fake, "Prepare"); n != 1 {
		t.Errorf("before the next poll Prepare was called %d times, want once", n)
	}
	for attempt := 2; attempt <= 3; attempt++ {
		w.h.Advance(time.Hour)
		w.cycle(t)
		if n := countWrites(w.fake, "Prepare"); n != attempt {
			t.Errorf("poll %d: Prepare called %d times", attempt, n)
		}
	}
	l = mustLink(t, w.h, p.Id)
	if l.Attempts != 3 || l.NextAttempt == nil || !l.NextAttempt.Equal(osloAt("2026-09-13", 0)) {
		t.Errorf("after three failures the link = %+v, want the next civil midnight", l)
	}
	w.h.Advance(time.Hour)
	w.cycle(t)
	if n := countWrites(w.fake, "Prepare"); n != 3 {
		t.Errorf("a fourth poll the same day called Prepare (%d calls)", n)
	}
	w.fake.failWith("Prepare", nil)
	setClock(t, w.h, osloAt("2026-09-13", 0))
	w.cycle(t)
	if l := mustLink(t, w.h, p.Id); countWrites(w.fake, "Prepare") != 4 || l.Attempts != 0 || l.LastError != "" || l.User == "" {
		t.Errorf("the next day: %d Prepare calls, link %+v; want the fourth to succeed and the failures cleared", countWrites(w.fake, "Prepare"), l)
	}
}

// A link that succeeded is acted on once per civil day, across the October
// change from summer time in Europe/Oslo (a 25-hour day) too: hourly polls
// claim it once a day, at the first poll after the civil midnight.
func TestAccountsWorker_OncePerCivilDayAcrossTheDSTChange(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Vera", "2026-01-01", nil)
	user := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: user, State: contracts.AccountStateActive, Managed: true, EmployeeNumber: p.EmployeeNumber, HasCredential: true})
	workerLink(t, w.h, p.Id, user, "active")
	setClock(t, w.h, osloAt("2026-10-24", 12))
	claims := map[string]int{}
	for i := 0; i < 64; i++ {
		before := len(w.fake.accountCalls())
		w.cycle(t)
		if len(w.fake.accountCalls()) > before {
			claims[w.h.Now().In(oslo).Format(time.DateOnly)]++
		}
		w.h.Advance(time.Hour)
	}
	want := map[string]int{"2026-10-24": 1, "2026-10-25": 1, "2026-10-26": 1, "2026-10-27": 1}
	if fmt.Sprint(claims) != fmt.Sprint(want) {
		t.Errorf("claims per civil day = %v, want %v", claims, want)
	}
}

// Every refusal identity answers and every report the decision table makes
// is recorded on the link as its blocked_reason — a code — with the link's
// state as the plan names it; none counts as a failed attempt.
func TestAccountsWorker_EachBlockedReasonIsRecorded(t *testing.T) {
	w := newAccountsWorld(t)
	for _, tc := range []struct {
		name, reason, state string
		// fail is the method made to fail, with err.
		fail string
		err  error
		// ref, when set, is the user the person's link names already, in
		// the given link state.
		ref       func(number string) contracts.AccountRef
		linkState string
		federated bool
	}{
		{name: "the address taken", fail: "Prepare", err: contracts.ErrAccountEmailTaken, reason: "account_email_taken", state: "blocked"},
		{name: "the number taken", fail: "Prepare", err: contracts.ErrEmployeeNumberTaken, reason: "employee_number_taken", state: "blocked"},
		{name: "not found, federated", fail: "Prepare", err: contracts.ErrAccountNotFound, reason: "not_found", state: "blocked"},
		{name: "privileged on Prepare", fail: "Prepare", err: contracts.ErrAccountPrivileged, reason: "account_privileged", state: "blocked"},
		{name: "no employee role", fail: "Activate", err: contracts.ErrEmployeeRoleMissing, reason: "employee_role_missing", state: "blocked"},
		{name: "an unsafe employee role", fail: "Activate", err: contracts.ErrEmployeeRoleUnsafe, reason: "employee_role_unsafe", state: "blocked"},
		{name: "blocked by an Owner on Activate", fail: "Activate", err: contracts.ErrBlockedByOwner, reason: "blocked_by_owner", state: "blocked"},
		{name: "the user gone on Activate", fail: "Activate", err: contracts.ErrUserGone, reason: "user_gone", state: "failed"},
		{name: "the user gone", linkState: "active", reason: "user_gone", state: "failed",
			ref: func(string) contracts.AccountRef { return contracts.AccountRef{} }},
		{name: "disabled by an Owner", linkState: "active", reason: "disabled_by_owner", state: "blocked",
			ref: func(n string) contracts.AccountRef {
				return contracts.AccountRef{State: contracts.AccountStateDisabled, DisabledBy: contracts.DisabledByOwner, Managed: true, EmployeeNumber: n}
			}},
		{name: "a pending user blocked by an Owner", linkState: "pending", reason: "blocked_by_owner", state: "blocked",
			ref: func(n string) contracts.AccountRef {
				return contracts.AccountRef{State: contracts.AccountStatePending, DisabledBy: contracts.DisabledByOwner, Managed: true, EmployeeNumber: n}
			}},
		{name: "the mode changed", linkState: "pending", reason: "mode_changed", state: "pending", federated: true,
			ref: func(n string) contracts.AccountRef {
				return contracts.AccountRef{State: contracts.AccountStatePending, Managed: true, EmployeeNumber: n}
			}},
		{name: "another employee number", linkState: "active", reason: "mismatch", state: "blocked",
			ref: func(string) contracts.AccountRef {
				return contracts.AccountRef{State: contracts.AccountStateActive, Managed: true, EmployeeNumber: "SOMEONE-ELSE"}
			}},
		{name: "the invitation revoked by an Owner", linkState: "pending", reason: "invitation_revoked", state: "pending",
			ref: func(n string) contracts.AccountRef {
				return contracts.AccountRef{State: contracts.AccountStatePending, Managed: true, EmployeeNumber: n,
					Invitation: contracts.Invitation{State: contracts.InvitationRevokedByOwner, Issued: 1}}
			}},
		{name: "no longer managed", linkState: "active", reason: "account_not_managed", state: "active",
			ref: func(n string) contracts.AccountRef {
				return contracts.AccountRef{State: contracts.AccountStateActive, EmployeeNumber: n}
			}},
		{name: "privileged", linkState: "active", reason: "account_privileged", state: "blocked",
			ref: func(n string) contracts.AccountRef {
				return contracts.AccountRef{State: contracts.AccountStateActive, Managed: true, Privileged: true, EmployeeNumber: n}
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := w.employee(t, "Blokk", "2026-01-01", nil)
			mode := contracts.AccountModeLocal
			if tc.federated {
				mode = contracts.AccountModeFederated
			}
			w.fake.set(func(f *fakeAccounts) { f.mode = mode })
			defer w.fake.set(func(f *fakeAccounts) { f.mode = contracts.AccountModeLocal })
			if tc.ref != nil {
				ref := tc.ref(p.EmployeeNumber)
				ref.UserID = uuid.New()
				if ref.State != "" {
					w.fake.setUser(ref)
				}
				workerLink(t, w.h, p.Id, ref.UserID, tc.linkState)
			}
			if tc.fail != "" {
				w.fake.failWith(tc.fail, tc.err)
				defer w.fake.failWith(tc.fail, nil)
			}
			activations := countWrites(w.fake, "Activate")
			w.cycle(t)
			l := mustLink(t, w.h, p.Id)
			if l.Reason != tc.reason || l.State != tc.state || l.Attempts != 0 || l.LastError != "" {
				t.Errorf("the link = %+v, want %s with state %s and no failed attempt", l, tc.reason, tc.state)
			}
			if tc.reason == "invitation_revoked" && countWrites(w.fake, "Activate") != activations {
				t.Error("an invitation an Owner revoked was issued again")
			}
		})
	}
}

// A link blocked is claimed again on its schedule, never skipped for ever
// (the decision table never reads blocked as an input): a user blocked as
// privileged, demoted and adopted again by an Owner, is activated by the
// next due cycle.
func TestAccountsWorker_ABlockedLinkRecovers(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Pia", "2026-01-01", nil)
	user := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: user, State: contracts.AccountStatePending, DisabledBy: contracts.DisabledByStaff,
		Managed: true, Privileged: true, EmployeeNumber: p.EmployeeNumber})
	workerLink(t, w.h, p.Id, user, "pending")
	w.cycle(t)
	if l := mustLink(t, w.h, p.Id); l.Reason != "account_privileged" || l.State != "blocked" {
		t.Fatalf("the link = %+v, want blocked account_privileged", l)
	}
	ref := w.fake.user(user)
	ref.Privileged, ref.EmployeeNumber = false, p.EmployeeNumber // the Owner's adoption names the number again
	w.fake.setUser(ref)
	setClock(t, w.h, osloAt("2026-09-13", 0))
	w.cycle(t)
	if n := countWrites(w.fake, "Activate"); n != 1 {
		t.Errorf("after the demotion Activate was called %d times, want once", n)
	}
	if l := mustLink(t, w.h, p.Id); l.Reason != "" || l.State != "pending" {
		t.Errorf("after the demotion the link = %+v, want pending with no reason", l)
	}
}

// Under a mode mismatch (identity's ErrModeMismatch) a leaver is still
// disabled — Deactivate reads no mode — and every other step is held: the
// links it holds record mode_mismatch, their state kept, nothing called.
func TestAccountsWorker_AModeMismatchRunsOnlyDeactivations(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	leaver := w.employee(t, "Borte", "2020-01-01", dayPtr("2026-06-30"))
	left := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: left, State: contracts.AccountStateActive, Managed: true, EmployeeNumber: leaver.EmployeeNumber, HasCredential: true})
	workerLink(t, w.h, leaver.Id, left, "active")
	due := w.employee(t, "Klar", "2026-01-01", nil)
	waiting := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: waiting, State: contracts.AccountStatePending, DisabledBy: contracts.DisabledByStaff, Managed: true, EmployeeNumber: due.EmployeeNumber})
	workerLink(t, w.h, due.Id, waiting, "pending")
	fresh := w.employee(t, "Ny", "2026-01-01", nil)
	w.fake.set(func(f *fakeAccounts) { f.mode, f.modeErr = "", contracts.ErrModeMismatch })
	w.cycle(t)
	if got := w.fake.writeCalls(); !slices.Equal(got, []string{"Deactivate"}) {
		t.Errorf("writes = %v, want the leaver's Deactivate alone", got)
	}
	if l := mustLink(t, w.h, leaver.Id); l.State != "disabled" {
		t.Errorf("the leaver's link = %+v, want disabled", l)
	}
	for _, id := range []int64{due.Id, fresh.Id} {
		l := mustLink(t, w.h, id)
		if l.Reason != "mode_mismatch" || l.LastError != "" {
			t.Errorf("person %d's link = %+v, want mode_mismatch", id, l)
		}
	}
	if l := mustLink(t, w.h, due.Id); l.State != "pending" {
		t.Errorf("the held link's state = %s, want pending kept", l.State)
	}
}

// A person HR renumbers is updated in identity exactly once (Task 4a's
// review): the Update records the number it wrote as synced, and the
// re-run's facts take the spec as written.
func TestAccountsWorker_ARenumberMakesOneUpdate(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Nils", "2026-01-01", nil)
	user := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: user, State: contracts.AccountStateActive, Managed: true, EmployeeNumber: p.EmployeeNumber, HasCredential: true})
	workerLink(t, w.h, p.Id, user, "active")
	w.cycle(t)
	before := countWrites(w.fake, "Update")
	w.h.Exec(t, `UPDATE staff.people SET employee_number = 'R-NEW' WHERE id = $1`, p.Id)
	setClock(t, w.h, osloAt("2026-09-13", 1))
	w.cycle(t)
	if got := countWrites(w.fake, "Update") - before; got != 1 {
		t.Errorf("the renumber made %d Update calls (%v), want exactly one", got, w.fake.writeCalls())
	}
	if l := mustLink(t, w.h, p.Id); l.Synced != "R-NEW" || l.Reason != "" {
		t.Errorf("the link = %+v, want synced R-NEW", l)
	}
}

// A link HR pointed at another user starts over (SetPersonLink resets the
// synced number): a user an Owner adopted holding an old number of the
// person's is someone else's account — mismatch, and nothing written.
func TestAccountsWorker_AResetLinkDoesNotTrustAnOldNumber(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Odd", "2026-01-01", nil)
	_, first := signIn(t, w.h)
	_, second := signIn(t, w.h)
	w.fake.setUser(contracts.AccountRef{UserID: first, State: contracts.AccountStateActive})
	w.fake.setUser(contracts.AccountRef{UserID: second, State: contracts.AccountStateActive})
	link(t, w.h, p.Id, first)
	w.h.Exec(t, `UPDATE staff.identity_links SET synced_employee_number = 'OLD-9' WHERE person_id = $1`, p.Id)
	linkThroughAPI(t, w.hr, p.Id, second)
	w.fake.setUser(contracts.AccountRef{UserID: second, State: contracts.AccountStateActive, Managed: true, EmployeeNumber: "OLD-9", HasCredential: true})
	w.cycle(t)
	if l := mustLink(t, w.h, p.Id); l.Reason != "mismatch" || l.State != "blocked" {
		t.Errorf("the link = %+v, want blocked mismatch", l)
	}
	if got := w.fake.writeCalls(); len(got) != 0 {
		t.Errorf("writes = %v, want none to another's account", got)
	}
}

// No worker write moves the person's revision or updated_at (Task 3's
// review): a manager and an Approver read the person and their history byte
// for byte as before across an activation, and the Approver across the
// deactivation the day after the end (when the manager, the employment
// over, reads the person no more); neither is listed an account event —
// which HR reads as {step, state}.
func TestAccountsWorker_AManagerSeesNothingOfACycle(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	w.h.Exec(t, `UPDATE staff.settings SET create_before_days = 0`)
	boss := createPerson(t, w.hr, newPerson("Sjef", "Sjefsen"))
	seedEmploymentDated(t, w.h, boss.Id, 0, "2020-01-01", nil)
	bossC, bossUser := signIn(t, w.h)
	link(t, w.h, boss.Id, bossUser)
	approver, _ := signIn(t, w.h, "staff:approve")
	p := createPerson(t, w.hr, with(newPerson("Eva", "Konto"), "workEmail", "eva.konto@example.test"))
	seedEmploymentDated(t, w.h, p.Id, boss.Id, "2026-01-01", dayPtr(today(t, w.h)))
	flagOn(t, w.hr, p.Id)
	read := func(readers ...*modtest.Client) string {
		t.Helper()
		var b strings.Builder
		for i, c := range readers {
			person := c.Do(http.MethodGet, personPath(p.Id), nil)
			events := c.Do(http.MethodGet, pagedPath(eventsPath(p.Id), 1, 100), nil)
			if person.Status != http.StatusOK {
				t.Fatalf("reader %d's GET /people/%d at %s = %d, want 200", i, p.Id, w.h.Now(), person.Status)
			}
			fmt.Fprintf(&b, "%s\n%d %s\n", person.Body, events.Status, events.Body)
		}
		b.WriteString(modtest.One[string](t, w.h.Harness, `SELECT revision::text || ' ' || updated_at::text FROM staff.people WHERE id = $1`, p.Id))
		return b.String()
	}
	for _, phase := range []struct {
		name, call string
		at         time.Time
		readers    []*modtest.Client
	}{
		{name: "the activation", call: "Activate", at: w.h.Now(), readers: []*modtest.Client{bossC, approver}},
		{name: "the deactivation", call: "Deactivate", at: osloAt("2026-09-13", 7), readers: []*modtest.Client{approver}},
	} {
		setClock(t, w.h, phase.at)
		before := read(phase.readers...)
		w.cycle(t)
		if countWrites(w.fake, phase.call) != 1 {
			t.Fatalf("%s: writes = %v, want one %s", phase.name, w.fake.writeCalls(), phase.call)
		}
		if after := read(phase.readers...); after != before || strings.Contains(after, "account_") {
			t.Errorf("%s moved what a manager and an Approver read:\nbefore %s\nafter  %s", phase.name, before, after)
		}
	}
	e := eventOfKind(t, eventsAs(t, w.hr, p.Id), "account_deactivated")
	if e.Payload["step"] != "deactivate" || e.Payload["state"] != "disabled" || len(e.Payload) != 2 {
		t.Errorf("HR reads account_deactivated as %v, want {step, state}", e.Payload)
	}
}

// A Prepare that finds a user another person's link still names (review
// HIGH 1: X renumbered, identity not yet told, P given X's old number) is
// recorded before anything else is done with it: the clash with X's link is
// a mismatch, and the chain stops — no Activate, no Update of X's user, on
// either day.
func TestAccountsWorker_APreparedUserAnotherLinkNamesIsNotTouched(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	x := w.employee(t, "Xavier", "2020-01-01", dayPtr("2026-06-30"))
	u := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStateDisabled, DisabledBy: contracts.DisabledByStaff,
		Managed: true, EmployeeNumber: x.EmployeeNumber, HasCredential: true})
	workerLink(t, w.h, x.Id, u, "disabled")
	w.h.Exec(t, `UPDATE staff.identity_links SET synced_employee_number = $2, next_attempt_at = $3 WHERE person_id = $1`,
		x.Id, x.EmployeeNumber, w.h.Now().Add(72*time.Hour))
	old := x.EmployeeNumber
	w.h.Exec(t, `UPDATE staff.people SET employee_number = 'X-NEW' WHERE id = $1`, x.Id)
	p := w.employee(t, "Petra", "2026-01-01", nil)
	w.h.Exec(t, `UPDATE staff.people SET employee_number = $2 WHERE id = $1`, p.Id, old)
	before := w.fake.user(u)
	for _, day := range []time.Time{w.h.Now(), osloAt("2026-09-13", 1)} {
		setClock(t, w.h, day)
		w.cycle(t)
		if countWrites(w.fake, "Activate")+countWrites(w.fake, "Update") > 0 {
			t.Fatalf("the chain acted on X's user: %v", w.fake.writeCalls())
		}
		if got := w.fake.user(u); got != before {
			t.Errorf("X's user changed: %+v, want %+v", got, before)
		}
		l := mustLink(t, w.h, p.Id)
		if l.Reason != "mismatch" || l.State != "blocked" || l.User != "" || l.Synced != "" {
			t.Errorf("P's link = %+v, want blocked mismatch naming no user, nothing synced", l)
		}
	}
	if l := mustLink(t, w.h, x.Id); l.User != u.String() {
		t.Errorf("X's link = %+v, want it naming its user still", l)
	}
}

// HR relinking a self-service link while a cycle holds it (review MEDIUM 2)
// wins: the relink clears the lease, and the result write, which names the
// user it claimed, finds another and writes nothing.
func TestAccountsWorker_ARelinkDuringAClaimIsKept(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Rita", "2026-01-01", nil)
	_, first := signIn(t, w.h)
	_, second := signIn(t, w.h)
	w.fake.setUser(contracts.AccountRef{UserID: first, State: contracts.AccountStateActive})
	w.fake.setUser(contracts.AccountRef{UserID: second, State: contracts.AccountStateActive})
	link(t, w.h, p.Id, first)
	var once atomic.Bool
	w.fake.whenReading(func(id uuid.UUID) {
		if id == first && once.CompareAndSwap(false, true) {
			linkThroughAPI(t, w.hr, p.Id, second)
		}
	})
	w.cycle(t)
	if l := mustLink(t, w.h, p.Id); l.User != second.String() || l.State != "linked" {
		t.Errorf("the link = %+v, want HR's relink to %s, linked", l, second)
	}
	if !strings.Contains(w.h.Logs(), "names another user") {
		t.Error("the dropped write was not logged as a relink")
	}
}

// The same with the user changed under the claim by any writer that kept
// the lease: the result names the user it claimed and is not written.
func TestAccountsWorker_AResultForAnotherUserIsNotWritten(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Rut", "2026-01-01", nil)
	first, second := uuid.New(), uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: first, State: contracts.AccountStateActive})
	w.fake.setUser(contracts.AccountRef{UserID: second, State: contracts.AccountStateActive})
	link(t, w.h, p.Id, first)
	w.fake.whenReading(func(id uuid.UUID) {
		if id == first {
			w.h.Exec(t, `UPDATE staff.identity_links SET external_id = $2 WHERE person_id = $1`, p.Id, second.String())
		}
	})
	w.cycle(t)
	if l, _ := linkOf(t, w.h, p.Id); l.User != second.String() {
		t.Errorf("link = %+v, want the change to %s kept", l, second)
	}
}

// The lease is renewed before each call of a chain (review MEDIUM 3): a
// chain whose calls take longer together than one lease keeps its claim, so
// a second cycle meanwhile claims nothing and the user is activated once.
func TestAccountsWorker_TheLeaseIsRenewedThroughTheChain(t *testing.T) {
	w := newAccountsWorld(t)
	w.h.Exec(t, `UPDATE staff.settings SET create_before_days = 0`)
	w.employee(t, "Lang", today(t, w.h), nil)
	var second atomic.Bool
	worker := accountsWorker(t, w.h, w.h.Deps())
	w.fake.whenWriting(func(method string, _ uuid.UUID) {
		switch {
		case method == "Prepare" && !second.Load():
			w.h.Advance(4 * time.Minute)
		case method == "Activate" && second.CompareAndSwap(false, true):
			w.h.Advance(2 * time.Minute)
			if err := worker.RunCycle(context.Background()); err != nil {
				t.Errorf("the second cycle: %v", err)
			}
		}
	})
	w.cycle(t)
	if n := countWrites(w.fake, "Activate"); n != 1 {
		t.Errorf("Activate called %d times (%v), want once: the second cycle took over a live claim", n, w.fake.writeCalls())
	}
}

// The person deleted while a Prepare found an existing user — one an Owner
// adopted, active — leaves that user alone (review MEDIUM 3): only a user
// the claim itself created is deactivated.
func TestAccountsWorker_AFoundUserIsNotDeactivatedWhenThePersonIsGone(t *testing.T) {
	w := newAccountsWorld(t)
	p := w.employee(t, "Funn", "2026-01-01", nil)
	adopted := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: adopted, State: contracts.AccountStateActive, Managed: true, EmployeeNumber: p.EmployeeNumber, HasCredential: true})
	w.fake.whenWriting(func(method string, _ uuid.UUID) {
		if method != "Prepare" {
			return
		}
		for _, sql := range []string{
			`DELETE FROM staff.identity_links WHERE person_id = $1`,
			`DELETE FROM staff.events WHERE person_id = $1`,
			`DELETE FROM staff.employment_terms WHERE employment_id IN (SELECT id FROM staff.employments WHERE person_id = $1)`,
			`DELETE FROM staff.employments WHERE person_id = $1`,
			`DELETE FROM staff.people WHERE id = $1`,
		} {
			w.h.Exec(t, sql, p.Id)
		}
	})
	w.cycle(t)
	if got := w.fake.writeCalls(); !slices.Equal(got, []string{"Prepare:" + p.EmployeeNumber}) {
		t.Errorf("writes = %v, want the Prepare alone", got)
	}
	if ref := w.fake.user(adopted); ref.DisabledBy != "" || ref.State != contracts.AccountStateActive {
		t.Errorf("the adopted user = %+v, want them left active", ref)
	}
}

// A leaver the SCIM directory keeps inactive is deactivated every run —
// identity's own record whatever the directory says — but the history says
// so once (review LOW 1): the state moved, or the user changed.
func TestAccountsWorker_ADirectoryInactiveLeaverIsRecordedOnce(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Scim", "2020-01-01", dayPtr("2026-06-30"))
	u := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStateDisabled, DisabledBy: contracts.DisabledByDirectory,
		Managed: true, EmployeeNumber: p.EmployeeNumber, HasCredential: true})
	workerLink(t, w.h, p.Id, u, "active")
	for _, day := range []time.Time{w.h.Now(), osloAt("2026-09-13", 1)} {
		setClock(t, w.h, day)
		w.cycle(t)
	}
	if n := countWrites(w.fake, "Deactivate"); n != 2 {
		t.Errorf("Deactivate called %d times, want every run", n)
	}
	n := 0
	for _, e := range eventsAs(t, w.hr, p.Id) {
		if e.Kind == "account_deactivated" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d account_deactivated events over two days, want one", n)
	}
}

// A successful call whose re-read fails is claimed again at the next poll,
// not the next day (review LOW 2): the step stood, its follow-up is owed.
func TestAccountsWorker_AFailedReReadIsRetriedAtTheNextPoll(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Lese", "2026-01-01", nil)
	w.fake.whenWriting(func(method string, _ uuid.UUID) {
		if method == "Prepare" {
			w.fake.set(func(f *fakeAccounts) { f.accountErr = errFakeDown })
		}
	})
	w.cycle(t)
	l := mustLink(t, w.h, p.Id)
	if l.User == "" || l.NextAttempt == nil || !l.NextAttempt.Equal(w.h.Now().Add(time.Hour)) {
		t.Errorf("the link = %+v, want the prepared user and the next poll", l)
	}
}

// A due deactivation that cannot reach identity is retried at every poll
// for as long as it fails (review LOW 5): a leaver loses access as soon as
// identity answers, never a day later.
func TestAccountsWorker_ADeactivationIsRetriedAtEveryPoll(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Slutt", "2020-01-01", dayPtr("2026-06-30"))
	u := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStateActive, Managed: true, EmployeeNumber: p.EmployeeNumber, HasCredential: true})
	workerLink(t, w.h, p.Id, u, "active")
	w.fake.failWith("Deactivate", errFakeDown)
	for poll := 1; poll <= 5; poll++ {
		w.cycle(t)
		if n := countWrites(w.fake, "Deactivate"); n != poll {
			t.Fatalf("poll %d: Deactivate called %d times", poll, n)
		}
		w.h.Advance(time.Hour)
	}
	if l := mustLink(t, w.h, p.Id); l.Attempts != 5 || l.LastError != "accounts_unavailable" {
		t.Errorf("the link = %+v, want five attempts counted", l)
	}
}

// The real provider on a pool of one (review LOW 8): identity's calls take
// the one connection too, so the cycle must hold none while it calls.
func TestAccountsWorker_ACycleOnAPoolOfOneAgainstIdentity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	role := uuid.New()
	h.Exec(t, `INSERT INTO identity.roles (id, name, normalized_name, display_name, version, created_at, updated_at)
	           VALUES ($1, 'Employee', 'EMPLOYEE', 'Employee', $2, $3, $3)`, role, uuid.New(), h.Now())
	h.Exec(t, `INSERT INTO identity.role_permissions (role_id, permission_key) VALUES ($1, 'staff:access')`, role)
	h.Exec(t, `INSERT INTO identity.provisioning_roles (role_id, created_at) VALUES ($1, $2)`, role, h.Now())
	h.Exec(t, `UPDATE staff.settings SET create_before_days = 0`)
	p := accountEmployee(t, h, hr, "Ein", today(t, h), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	one, err := db.Open(ctx, h.Pool().Config().ConnString(), db.WithMaxConns(1))
	if err != nil {
		t.Fatalf("open a pool of one: %v", err)
	}
	defer one.Close()
	deps := h.Deps()
	deps.Pool = one
	if err := accountsWorker(t, h, deps).RunCycle(ctx); err != nil {
		t.Fatalf("a cycle on a pool of one: %v", err)
	}
	l := mustLink(t, h, p.Id)
	if l.State != "pending" || l.User == "" {
		t.Errorf("the link = %+v, want pending with the user", l)
	}
	if disabled := modtest.One[bool](t, h.Harness, `SELECT is_disabled FROM identity.users WHERE id = $1`, l.User); disabled {
		t.Error("the user was not enabled on the pool of one")
	}
}

// A leaver whose user identity cannot read is retried at every poll, never
// daily (reading 25, review MEDIUM 1): the read fails for four polls, and the
// first poll identity answers again deactivates the user.
func TestAccountsWorker_ALeaversFailedReadIsRetriedAtEveryPoll(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	p := w.employee(t, "Slutt", "2020-01-01", dayPtr("2026-06-30"))
	u := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStateActive, Managed: true, EmployeeNumber: p.EmployeeNumber, HasCredential: true})
	workerLink(t, w.h, p.Id, u, "active")
	w.fake.set(func(f *fakeAccounts) { f.accountErr = errFakeDown })
	for poll := 1; poll <= 4; poll++ {
		w.cycle(t)
		w.h.Advance(time.Hour)
	}
	if n := countWrites(w.fake, "Deactivate"); n != 0 {
		t.Fatalf("Deactivate called %d times while identity could not be read", n)
	}
	w.fake.set(func(f *fakeAccounts) { f.accountErr = nil })
	w.cycle(t)
	if n := countWrites(w.fake, "Deactivate"); n != 1 {
		t.Errorf("identity answers again, but Deactivate was called %d times at the next poll; link %+v", n, mustLink(t, w.h, p.Id))
	}
}

// An unchanged mismatch on a link naming no user is recorded once, not on
// every day it is claimed again (review LOW 1).
func TestAccountsWorker_AnUnchangedMismatchIsRecordedOnce(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	x := w.employee(t, "Xavier", "2020-01-01", dayPtr("2026-06-30"))
	u := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: u, State: contracts.AccountStateDisabled, DisabledBy: contracts.DisabledByStaff,
		Managed: true, EmployeeNumber: x.EmployeeNumber, HasCredential: true})
	workerLink(t, w.h, x.Id, u, "disabled")
	w.h.Exec(t, `UPDATE staff.identity_links SET synced_employee_number = $2, next_attempt_at = $3 WHERE person_id = $1`,
		x.Id, x.EmployeeNumber, w.h.Now().Add(720*time.Hour))
	old := x.EmployeeNumber
	w.h.Exec(t, `UPDATE staff.people SET employee_number = 'X-NEW' WHERE id = $1`, x.Id)
	p := w.employee(t, "Petra", "2026-01-01", nil)
	w.h.Exec(t, `UPDATE staff.people SET employee_number = $2 WHERE id = $1`, p.Id, old)
	for _, day := range []time.Time{w.h.Now(), osloAt("2026-09-13", 1), osloAt("2026-09-14", 1), osloAt("2026-09-15", 1)} {
		setClock(t, w.h, day)
		w.cycle(t)
	}
	n := 0
	for _, e := range eventsAs(t, w.hr, p.Id) {
		if e.Kind == "account_mismatched" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d account_mismatched events for one unchanged mismatch over four days, want one", n)
	}
}

// A person erased, or their flag turned off, while Prepare runs (review LOW
// 2): the prepared user is recorded on the link, and nothing more is done
// with them — no Activate, no Update.
func TestAccountsWorker_APreparedUserOfAPersonErasedOrTurnedOffIsLeft(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, sql string }{
		{"erased", `UPDATE staff.people SET erased_at = now() WHERE id = $1`},
		{"the flag off", `UPDATE staff.people SET provision_account = false WHERE id = $1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newAccountsWorld(t)
			w.h.Exec(t, `UPDATE staff.settings SET create_before_days = 0`)
			p := w.employee(t, "Midt", today(t, w.h), nil)
			w.fake.whenWriting(func(method string, _ uuid.UUID) {
				if method == "Prepare" {
					w.h.Exec(t, tc.sql, p.Id)
				}
			})
			w.cycle(t)
			if got := w.fake.writeCalls(); len(got) != 1 || got[0] != "Prepare:"+p.EmployeeNumber {
				t.Errorf("writes = %v, want the Prepare alone", got)
			}
			if l, _ := linkOf(t, w.h, p.Id); l.User == "" {
				t.Errorf("the link = %+v, want the prepared user recorded", l)
			}
		})
	}
}

// employeeRoleForStaff names one custom role, carrying staff:access, as the
// roles Staff may give — the Owner's step before any account is enabled.
func employeeRoleForStaff(t *testing.T, h *harness) {
	t.Helper()
	role := uuid.New()
	h.Exec(t, `INSERT INTO identity.roles (id, name, normalized_name, display_name, version, created_at, updated_at)
	           VALUES ($1, 'Employee', 'EMPLOYEE', 'Employee', $2, $3, $3)`, role, uuid.New(), h.Now())
	h.Exec(t, `INSERT INTO identity.role_permissions (role_id, permission_key) VALUES ($1, 'staff:access')`, role)
	h.Exec(t, `INSERT INTO identity.provisioning_roles (role_id, created_at) VALUES ($1, $2)`, role, h.Now())
}

// A pending account's address follows the person's record only until the
// first invitation is delivered (the owner's decision D1 of 2026-10-18),
// against identity's real provider. Prepared in the window and never invited,
// a new address is written to identity. Activated on the start day — the
// invitation delivered — a later change is not: identity keeps the address
// the person was invited at, nothing is sent to the new one, and the link
// reports email_differs for an Owner to settle.
func TestAccountsWorker_AnInvitedUsersAddressIsKeptAgainstIdentity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	employeeRoleForStaff(t, h)
	start := shiftDay(t, today(t, h), 3)
	p := accountEmployee(t, h, hr, "Adresse", start, nil)
	userEmail := func() string {
		t.Helper()
		return modtest.One[string](t, h.Harness, `SELECT email FROM identity.users WHERE id = $1`, mustLink(t, h, p.Id).User)
	}
	box := h.Deps().Mail.(*mail.Fake)

	// In the window: prepared, never invited — the address follows.
	runAccounts(t, h)
	if l := mustLink(t, h, p.Id); l.State != "pending" || l.User == "" {
		t.Fatalf("in the window the link = %+v, want pending with the user", l)
	}
	const before = "before.invite@example.test"
	h.Exec(t, `UPDATE staff.people SET work_email = $2 WHERE id = $1`, p.Id, before)
	setClock(t, h, h.Now().Add(24*time.Hour))
	runAccounts(t, h)
	if got := userEmail(); got != before {
		t.Errorf("before any invitation identity's address = %q, want %q", got, before)
	}

	// The start day: enabled and invited at that address.
	setClock(t, h, osloAt(start, 7))
	runAccounts(t, h)
	if n := len(invitesTo(box, before)); n != 1 {
		t.Fatalf("%d invitations to %s on the start day, want one", n, before)
	}

	// After the delivery: a new address is not written, nor invited.
	const after = "after.invite@example.test"
	h.Exec(t, `UPDATE staff.people SET work_email = $2 WHERE id = $1`, p.Id, after)
	setClock(t, h, osloAt(shiftDay(t, start, 1), 7))
	runAccounts(t, h)
	if got := userEmail(); got != before {
		t.Errorf("after the invitation identity's address = %q, want %q kept", got, before)
	}
	if n := len(invitesTo(box, after)); n != 0 {
		t.Errorf("%d mails to the new address, want none", n)
	}
	if n := modtest.One[int64](t, h.Harness, `SELECT count(*) FROM identity.invitations WHERE normalized_email = upper($1)`, after); n != 0 {
		t.Errorf("%d invitations to the new address, want none", n)
	}
	if l := mustLink(t, h, p.Id); l.Reason != "email_differs" || l.State != "pending" {
		t.Errorf("the link = %+v, want pending, email_differs", l)
	}
}

// Leaving disables anyway (the owner's decision D2): with the account flag
// off, a Staff-managed user on the worker's link is left alone while the
// person is employed — nothing is created, enabled or written — and is
// deactivated the day after the last employment ends. HR's self-service link
// to a user an Owner has adopted for Staff is HR's, and left as it is.
func TestAccountsWorker_ALeaverIsDisabledWithTheFlagOff(t *testing.T) {
	t.Parallel()
	w := newAccountsWorld(t)
	end := shiftDay(t, today(t, w.h), 2)
	person := func(given string) (gen.StaffPerson, uuid.UUID) {
		t.Helper()
		p := createPerson(t, w.hr, with(newPerson(given, "Slutter"), "workEmail", strings.ToLower(given)+"@example.test"))
		seedEmploymentDated(t, w.h, p.Id, 0, shiftDay(t, today(t, w.h), -100), &end)
		user := uuid.New()
		w.fake.setUser(contracts.AccountRef{UserID: user, State: contracts.AccountStateActive, Managed: true,
			EmployeeNumber: p.EmployeeNumber, HasCredential: true, Invitation: contracts.Invitation{State: contracts.InvitationNone}})
		return p, user
	}
	leaver, leaverUser := person("Leaver")
	workerLink(t, w.h, leaver.Id, leaverUser, "active")
	selfService, selfUser := person("Selv")
	link(t, w.h, selfService.Id, selfUser)
	w.h.Exec(t, `UPDATE staff.identity_links SET managed = true WHERE person_id = $1`, selfService.Id)

	// Employed, the flag off: nothing at all.
	w.cycle(t)
	if writes := w.fake.writeCalls(); len(writes) != 0 {
		t.Fatalf("writes while employed with the flag off = %v, want none", writes)
	}
	// The last day: still employed.
	setClock(t, w.h, osloAt(end, 12))
	w.cycle(t)
	if writes := w.fake.writeCalls(); len(writes) != 0 {
		t.Fatalf("writes on the last day = %v, want none", writes)
	}
	// The day after the end: the leaver's account is deactivated.
	setClock(t, w.h, osloAt(shiftDay(t, end, 1), 7))
	w.cycle(t)
	if writes := w.fake.writeCalls(); !slices.Equal(writes, []string{"Deactivate"}) {
		t.Fatalf("writes the day after the end = %v, want one Deactivate", writes)
	}
	if !w.fake.staffDisabledUser(leaverUser) || w.fake.staffDisabledUser(selfUser) {
		t.Errorf("disabled: the leaver %v, the self-service user %v; want the leaver alone",
			w.fake.staffDisabledUser(leaverUser), w.fake.staffDisabledUser(selfUser))
	}
	if l := mustLink(t, w.h, leaver.Id); l.State != "disabled" {
		t.Errorf("the leaver's link = %+v, want disabled", l)
	}
	if l := mustLink(t, w.h, selfService.Id); l.State != "linked" {
		t.Errorf("the self-service link = %+v, want linked, untouched", l)
	}
	// Again the next day: nothing more.
	setClock(t, w.h, osloAt(shiftDay(t, end, 2), 7))
	w.cycle(t)
	if n := countWrites(w.fake, "Deactivate"); n > 2 {
		t.Errorf("%d Deactivate calls, want the leaver's alone", n)
	}
	if n := countWrites(w.fake, "Activate") + countWrites(w.fake, "Prepare") + countWrites(w.fake, "Update"); n != 0 {
		t.Errorf("%d other writes with the flag off, want none", n)
	}
}
