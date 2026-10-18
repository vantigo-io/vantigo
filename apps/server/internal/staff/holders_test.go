package staff_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// emailOf is user's address in identity: where a holder's mail goes.
func emailOf(t *testing.T, h *harness, user uuid.UUID) string {
	t.Helper()
	return modtest.One[string](t, h.Harness, `SELECT email FROM identity.users WHERE id = $1`, user)
}

// ownerUser is a signed-in Owner: a user holding identity's built-in Owner
// role and no role of their own, who holds every key by the wildcard.
func ownerUser(t *testing.T, h *harness) (*modtest.Client, uuid.UUID) {
	t.Helper()
	c, id := h.SignInUser(t)
	h.Exec(t, `INSERT INTO identity.user_roles (user_id, role_id) SELECT $1, id FROM identity.roles WHERE name = 'Owner'`, id)
	return c, id
}

// failingHolders is identity's directory with PermissionHolders failing:
// the directory down for the one read the holders' rules make.
type failingHolders struct{ contracts.UserDirectory }

func (failingHolders) PermissionHolders(context.Context, string) ([]contracts.Holder, error) {
	return nil, errors.New("the directory is down")
}

func withFailingHolders() modtest.Option {
	return modtest.WithUsers(func(d contracts.UserDirectory) contracts.UserDirectory { return failingHolders{d} })
}

// recipients is every address the mailbox holds a mail to, in order.
func recipients(h *harness) []string {
	var out []string
	for _, m := range h.mail.messages() {
		out = append(out, m.To...)
	}
	return out
}

// A request of a person who has a manager-decider mails the manager alone
// (the owner's answer 3): the holders of staff:approve and staff:manage, an
// Owner among them, read the inbox and are mailed nothing.
func TestHolderMails_TheManagerAloneWhenThePersonHasOne(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h := w.h
	ownerUser(t, h)
	h.Exec(t, `UPDATE staff.people SET work_email = 'bjorn@example.test' WHERE id = $1`, w.boss.Id)
	requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-10-05", "2026-10-09"))
	if got := recipients(h); !slices.Equal(got, []string{"bjorn@example.test"}) {
		t.Errorf("the request mailed %v, want the manager alone", got)
	}
}

// A request of a person with no manager-decider mails every active holder
// of staff:approve or staff:manage once — an Owner by the wildcard, though
// they hold both — never the requester's own user, a disabled or pending
// holder, or one whose address is a placeholder; at the identity address, in
// the linked person's language, else the user's, else nb; the type named by
// B's rule and the link to the absence area. Every send is made after the
// request committed and outside any transaction, as is the directory's read.
// A registration of such a person mails no holder.
func TestHolderMails_TheHoldersByRightWhenNoManagerDecides(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h := w.h
	_, owner := ownerUser(t, h)
	h.Exec(t, `UPDATE identity.users SET preferred_language = 'en' WHERE id = $1`, owner)
	_, approverUser := signIn(t, h, "staff:approve")
	hrUser := modtest.One[uuid.UUID](t, h.Harness, `
		SELECT ur.user_id FROM identity.user_roles ur JOIN identity.role_permissions rp ON rp.role_id = ur.role_id
		WHERE rp.permission_key = 'staff:manage' AND ur.user_id NOT IN (
			SELECT external_id::uuid FROM staff.identity_links WHERE external_id IS NOT NULL)
		ORDER BY ur.user_id LIMIT 1`)
	olavUser := modtest.One[uuid.UUID](t, h.Harness, `SELECT external_id::uuid FROM staff.identity_links WHERE person_id = $1`, w.olav.Id)
	hildeUser := modtest.One[uuid.UUID](t, h.Harness, `SELECT external_id::uuid FROM staff.identity_links WHERE person_id = $1`, w.hilde.Id)
	// Olav's person reads English, his user Norwegian: the person wins.
	h.Exec(t, `UPDATE staff.people SET preferred_language = 'en' WHERE id = $1`, w.olav.Id)
	h.Exec(t, `UPDATE identity.users SET preferred_language = 'nb' WHERE id = $1`, olavUser)
	_, disabled := signIn(t, h, "staff:manage")
	h.Exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, disabled)
	_, pending := signIn(t, h, "staff:approve")
	h.Exec(t, `UPDATE identity.users SET pending_since = $2 WHERE id = $1`, pending, h.Now())
	_, placeholder := signIn(t, h, "staff:manage")
	h.Exec(t, `UPDATE identity.users SET email = 'scim-1@sso.invalid', normalized_email = 'SCIM-1@SSO.INVALID' WHERE id = $1`, placeholder)

	var committed []bool
	onContractCall(t, hildeUser, func(method string) {
		if method == "SMTPSend" {
			n := 0
			_ = h.Pool().QueryRow(context.Background(), `SELECT count(*) FROM staff.absences WHERE person_id = $1`, w.hilde.Id).Scan(&n)
			committed = append(committed, n == 1)
		}
	})
	requestOwn(t, w.hildC, absence(t, h, "ferie", "2026-10-05", "2026-10-09"))

	want := map[string]string{ // address → language
		emailOf(t, h, owner):        "en",
		emailOf(t, h, approverUser): "nb",
		emailOf(t, h, hrUser):       "nb",
		emailOf(t, h, olavUser):     "en",
	}
	approverW := modtest.One[uuid.UUID](t, h.Harness, `
		SELECT ur.user_id FROM identity.user_roles ur JOIN identity.role_permissions rp ON rp.role_id = ur.role_id
		WHERE rp.permission_key = 'staff:approve' AND ur.user_id NOT IN ($1, $2) ORDER BY ur.user_id LIMIT 1`, approverUser, pending)
	want[emailOf(t, h, approverW)] = "nb"
	got := recipients(h)
	slices.Sort(got)
	wantTo := make([]string, 0, len(want))
	for to := range want {
		wantTo = append(wantTo, to)
	}
	slices.Sort(wantTo)
	if !slices.Equal(got, wantTo) {
		t.Fatalf("the request mailed %v, want each holder by right once: %v", got, wantTo)
	}
	for to, language := range want {
		m := lastMail(t, h, to)
		words := map[string][]string{
			"en": {"Absence request from Hilde Nordmann", "Holiday", "2026-10-05", "2026-10-09", "http://modules.example.com/staff/absence"},
			"nb": {"Fraværssøknad fra Hilde Nordmann", "Ferie", "2026-10-05", "2026-10-09", "http://modules.example.com/staff/absence"},
		}[language]
		for _, word := range words {
			if !strings.Contains(m, word) {
				t.Errorf("the mail to %s (%s) lacks %q:\n%s", to, language, word, m)
			}
		}
	}
	for _, not := range []uuid.UUID{hildeUser, disabled, pending} {
		if n := len(mailsTo(h, emailOf(t, h, not))); n != 0 {
			t.Errorf("%d mails to %s, who is the requester, disabled or pending", n, not)
		}
	}
	if len(committed) != len(want) || slices.Contains(committed, false) {
		t.Errorf("the sends saw the request committed: %v, want %d times true", committed, len(want))
	}
	saw := map[string]bool{}
	for _, c := range contractCalls.by(hildeUser) {
		saw[c.method] = true
		if c.inTx && (c.method == "SMTPSend" || c.method == "Users.PermissionHolders") {
			t.Errorf("%s was called inside a transaction", c.method)
		}
	}
	if !saw["Users.PermissionHolders"] || !saw["SMTPSend"] {
		t.Errorf("the request's calls %v lack the holders' read or the send", saw)
	}

	sent := len(h.mail.messages())
	register(t, w.hr, w.hilde.Id, absence(t, h, "sykmelding", "2026-11-02", "2026-11-06"))
	if n := len(h.mail.messages()); n != sent {
		t.Errorf("a registration mailed %d holders", n-sent)
	}
}

// A request of an HR-only type mails no holder of staff:approve or
// staff:manage, whatever the manager rule, and each other active holder of
// staff:personal-data — an Owner by the wildcard — one neutral mail: no
// type, no dates, not the requester's own user.
func TestHolderMails_AnHROnlyRequestMailsThePersonalDataHoldersNeutrally(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h := w.h
	_, owner := ownerUser(t, h)
	pdPerson, _ := employed(t, w.hr, "Per", "2020-01-01", terms())
	pdC, pdUser := signIn(t, h, "staff:personal-data")
	link(t, h, pdPerson.Id, pdUser)
	pdOnly := modtest.One[uuid.UUID](t, h.Harness, `
		SELECT ur.user_id FROM identity.user_roles ur JOIN identity.role_permissions rp ON rp.role_id = ur.role_id
		WHERE rp.permission_key = 'staff:personal-data' AND ur.user_id <> $1 ORDER BY ur.user_id LIMIT 1`, pdUser)
	h.Exec(t, `UPDATE identity.users SET preferred_language = 'en' WHERE id = $1`, pdOnly)

	requestOwn(t, pdC, absence(t, h, "svangerskapspermisjon", "2026-11-02", "2026-11-06"))
	got := recipients(h)
	slices.Sort(got)
	want := []string{emailOf(t, h, owner), emailOf(t, h, pdOnly)}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("the HR-only request mailed %v, want the other personal-data holders %v", got, want)
	}
	for _, m := range h.mail.messages() {
		text := m.Subject + "\n" + m.TextBody
		leaksIn(t, "the HR-only request's mail", scrubRandom(text), "svangerskap", "pregnan", "permisjon", "leave", "2026-", "Per Nordmann")
		if !strings.Contains(text, "awaits your decision") && !strings.Contains(text, "venter på avgjørelsen din") {
			t.Errorf("the mail does not say a request awaits a decision:\n%s", text)
		}
	}
	if m := lastMail(t, h, emailOf(t, h, pdOnly)); !strings.Contains(m, "awaits your decision") {
		t.Errorf("the mail to an English user is not in English:\n%s", m)
	}

	// Kari's, whose manager has an active user: still no approve or manage
	// holder, no manager, and the personal-data holders, Per now among them.
	sent := len(h.mail.messages())
	requestOwn(t, w.kariC, absence(t, h, "religios_hoytid", "2026-12-01", "2026-12-01"))
	got = recipients(h)[sent:]
	slices.Sort(got)
	want = []string{emailOf(t, h, owner), emailOf(t, h, pdOnly), emailOf(t, h, pdUser)}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("Kari's HR-only request mailed %v, want %v", got, want)
	}
}

// The directory failing: a request is recorded and no holder is mailed, the
// failure logged without an address.
func TestHolderMails_TheDirectoryFailing(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t, withFailingHolders())
	h := w.h
	hilde := requestOwn(t, w.hildC, absence(t, h, "ferie", "2026-10-05", "2026-10-09"))
	kari := requestOwn(t, w.kariC, absence(t, h, "svangerskapspermisjon", "2026-11-02", "2026-11-06"))
	for _, id := range []int64{hilde.Id, kari.Id} {
		if statusIn(t, h, id) != "requested" {
			t.Errorf("request %d was not recorded", id)
		}
	}
	if n := len(h.mail.messages()); n != 0 {
		t.Errorf("%d mails with the directory down", n)
	}
	if !strings.Contains(h.Logs(), "the holders could not be read") {
		t.Errorf("the skipped mails are not logged:\n%s", h.Logs())
	}
}

// mailPathLine is a log line of the absence mails' path.
var mailPathLine = regexp.MustCompile(`(?m)^.*absence mail.*$`)

// No log line of the mail path carries an address (Task 2a review LOW-4):
// not a send's error naming its recipient, not a skipped placeholder, not a
// holder without one.
func TestHolderMails_NoAddressInTheLogs(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h := w.h
	_, placeholder := signIn(t, h, "staff:manage")
	h.Exec(t, `UPDATE identity.users SET email = 'scim-2@sso.invalid', normalized_email = 'SCIM-2@SSO.INVALID' WHERE id = $1`, placeholder)
	h.mail.failWith(func(out mail.Outbound) error {
		return fmt.Errorf("550 5.1.1 <%s>: recipient address rejected", strings.ToUpper(out.To[0]))
	})
	requestOwn(t, w.hildC, absence(t, h, "ferie", "2026-10-05", "2026-10-09"))
	requestOwn(t, w.kariC, absence(t, h, "svangerskapspermisjon", "2026-11-02", "2026-11-06"))
	lines := mailPathLine.FindAllString(h.Logs(), -1)
	if len(lines) == 0 {
		t.Fatal("the mail path logged nothing")
	}
	failed := 0
	for _, line := range lines {
		if strings.Contains(line, "@") {
			t.Errorf("a log line of the mail path carries an address: %s", line)
		}
		if strings.Contains(line, "could not be sent") {
			failed++
		}
	}
	if failed == 0 {
		t.Errorf("no failed send was logged:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(h.Logs(), placeholder.String()) {
		t.Error("the holder skipped for a placeholder address is not logged by id")
	}
}

// soleWorld is an installation where Kari is the one active holder of
// staff:personal-data: her manager Bjørn, HR holding staff:manage alone, an
// Approver. second, when set, is another holder of staff:personal-data,
// created disabled unless active — so two worlds differ in that alone.
type soleWorld struct {
	h                    *harness
	hr, approver, bossC  *modtest.Client
	kariC, secondC       *modtest.Client
	kari, boss           gen.StaffPerson
	kariUser, secondUser uuid.UUID
}

func newSoleWorld(t *testing.T, secondActive bool, opts ...modtest.Option) soleWorld {
	t.Helper()
	h := newHarness(t, opts...)
	w := soleWorld{h: h}
	w.hr, _ = signIn(t, h, "staff:manage")
	company(t, w.hr)
	w.boss, _ = employed(t, w.hr, "Bjørn", "2019-01-01", terms())
	w.kari, _ = employed(t, w.hr, "Kari", "2020-01-01", terms("managerPersonId", w.boss.Id))
	w.kariC, w.kariUser = signIn(t, h, "staff:personal-data")
	link(t, h, w.kari.Id, w.kariUser)
	var bossUser uuid.UUID
	w.bossC, bossUser = signIn(t, h)
	link(t, h, w.boss.Id, bossUser)
	w.approver, _ = signIn(t, h, "staff:approve")
	w.secondC, w.secondUser = signIn(t, h, "staff:personal-data")
	h.Exec(t, `UPDATE identity.users SET is_disabled = $2 WHERE id = $1`, w.secondUser, !secondActive)
	return w
}

// The sole active holder of staff:personal-data decides their own HR-only
// request (the owner's Q9): it is in their inbox and their count, the
// approval records self_approved and absence_self_approved, sends no mail,
// and raises hrOnlySelfApproved — to staff:personal-data holders alone, a
// holder added later included, until the absence has ended; never naming
// the type. HR without staff:personal-data is shown neither it nor
// selfApproved.
func TestSoleHolder_ApprovesTheirOwnHROnlyRequest(t *testing.T) {
	t.Parallel()
	w := newSoleWorld(t, false)
	h := w.h
	a := requestOwn(t, w.kariC, absence(t, h, "svangerskapspermisjon", "2026-11-02", "2026-11-06"))

	var inbox gen.PaginatedResponseOfStaffApproval
	do(t, w.kariC, http.MethodGet, approvalsPath, nil, http.StatusOK, &inbox)
	if len(inbox.Data) != 1 || inbox.Data[0].Absence.Id != a.Id || inbox.Pagination.TotalCount != 1 {
		t.Errorf("the sole holder's inbox = %+v, want their own request", inbox)
	}
	if item := onlyItem(t, getAttention(t, w.kariC), gen.AbsenceAwaitingDecision); countOf(item) != 1 {
		t.Errorf("the sole holder's count = %+v, want 1", item)
	}

	sent := len(h.mail.messages())
	if got := approved(t, w.kariC, a.Id); got["status"] != "approved" {
		t.Fatalf("the sole holder's approval: %v", got)
	}
	if !modtest.One[bool](t, h.Harness, `SELECT self_approved FROM staff.absences WHERE id = $1`, a.Id) {
		t.Error("the approval is not recorded self_approved")
	}
	if e := eventsAbout(t, w.kariC, w.kari.Id, a.Id); len(e) != 2 || e[1].Kind != "absence_self_approved" && e[0].Kind != "absence_self_approved" {
		t.Errorf("the events = %v, want absence_self_approved", e)
	}
	if n := len(h.mail.messages()); n != sent {
		t.Errorf("the self-approval sent %d mails", n-sent)
	}

	item := onlyItem(t, getAttention(t, w.kariC), gen.HrOnlySelfApproved)
	if item.Id != fmt.Sprintf("hrOnlySelfApproved/%d", a.Id) || item.EntityId != idString(w.kari.Id) || item.Title != "Kari Nordmann" || item.Count != nil {
		t.Errorf("hrOnlySelfApproved = %+v", item)
	}
	leaksIn(t, "the item", fmt.Sprintf("%+v", item), "svangerskap", "permisjon", "leave")
	for name, c := range map[string]*modtest.Client{"HR without personal-data": w.hr, "her manager": w.bossC, "an Approver": w.approver} {
		items := getAttention(t, c)
		if got := append(ofType(items, gen.HrOnlySelfApproved), ofType(items, gen.SelfApproved)...); len(got) != 0 {
			t.Errorf("%s is shown %+v", name, got)
		}
	}
	later, _ := signIn(t, h, "staff:personal-data")
	if got := ofType(getAttention(t, later), gen.HrOnlySelfApproved); len(got) != 1 {
		t.Errorf("a holder added later is shown %+v, want the item", got)
	}
	h.Exec(t, `UPDATE staff.absences SET first_day = '2026-08-03', last_day = '2026-08-07' WHERE id = $1`, a.Id)
	if got := ofType(getAttention(t, later), gen.HrOnlySelfApproved); len(got) != 0 {
		t.Errorf("an ended absence is shown %+v", got)
	}
}

// The rule fails toward waiting: a second active holder of
// staff:personal-data — or an Owner, by the wildcard — makes the caller's
// own HR-only request own_request, gone from their inbox; a second holder
// disabled makes them sole again; the directory failing refuses it.
func TestSoleHolder_RefusedUnlessSole(t *testing.T) {
	t.Parallel()
	t.Run("a second active holder", func(t *testing.T) {
		t.Parallel()
		w := newSoleWorld(t, true)
		a := requestOwn(t, w.kariC, absence(t, w.h, "svangerskapspermisjon", "2026-11-02", "2026-11-06"))
		refused(t, decide(w.kariC, approvePath(a.Id), ""), http.StatusForbidden, "own_request")
		var inbox gen.PaginatedResponseOfStaffApproval
		do(t, w.kariC, http.MethodGet, approvalsPath, nil, http.StatusOK, &inbox)
		if len(inbox.Data) != 0 {
			t.Errorf("the inbox lists the caller's own: %+v", inbox.Data)
		}
		w.h.Exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, w.secondUser)
		if got := approved(t, w.kariC, a.Id); got["status"] != "approved" {
			t.Errorf("sole again, the approval: %v", got)
		}
	})
	t.Run("an Owner", func(t *testing.T) {
		t.Parallel()
		w := newSoleWorld(t, false)
		ownerUser(t, w.h)
		a := requestOwn(t, w.kariC, absence(t, w.h, "svangerskapspermisjon", "2026-11-02", "2026-11-06"))
		refused(t, decide(w.kariC, approvePath(a.Id), ""), http.StatusForbidden, "own_request")
	})
	t.Run("the directory failing", func(t *testing.T) {
		t.Parallel()
		w := newSoleWorld(t, false, withFailingHolders())
		a := requestOwn(t, w.kariC, absence(t, w.h, "svangerskapspermisjon", "2026-11-02", "2026-11-06"))
		refused(t, decide(w.kariC, approvePath(a.Id), ""), http.StatusForbidden, "own_request")
		var inbox gen.PaginatedResponseOfStaffApproval
		do(t, w.kariC, http.MethodGet, approvalsPath, nil, http.StatusOK, &inbox)
		if len(inbox.Data) != 0 {
			t.Errorf("the inbox lists the caller's own with the directory down: %+v", inbox.Data)
		}
	})
}

// hrOnlyAwaitingDecision leaves out the sole holder's own request once they
// may decide it — it is in their absenceAwaitingDecision instead — and
// counts it again, for them and for the new holder, once a second holder
// exists.
func TestSoleHolder_NotStalledOnceSelfApprovable(t *testing.T) {
	t.Parallel()
	w := newSoleWorld(t, false)
	h := w.h
	a := requestOwn(t, w.kariC, absence(t, h, "svangerskapspermisjon", "2026-11-02", "2026-11-06"))
	h.Exec(t, `UPDATE staff.absences SET created_at = created_at - interval '8 days' WHERE id = $1`, a.Id)
	items := getAttention(t, w.kariC)
	if got := ofType(items, gen.HrOnlyAwaitingDecision); len(got) != 0 {
		t.Errorf("the sole holder's own self-approvable request stalls: %+v", got)
	}
	if item := onlyItem(t, items, gen.AbsenceAwaitingDecision); countOf(item) != 1 {
		t.Errorf("absenceAwaitingDecision = %+v, want 1", item)
	}
	h.Exec(t, `UPDATE identity.users SET is_disabled = false WHERE id = $1`, w.secondUser)
	for name, c := range map[string]*modtest.Client{"Kari": w.kariC, "the second holder": w.secondC} {
		if item := onlyItem(t, getAttention(t, c), gen.HrOnlyAwaitingDecision); countOf(item) != 1 {
			t.Errorf("%s: hrOnlyAwaitingDecision = %+v, want 1", name, item)
		}
	}
	if got := ofType(getAttention(t, w.kariC), gen.AbsenceAwaitingDecision); len(got) != 0 {
		t.Errorf("Kari, no longer sole, counts her own: %+v", got)
	}
}

// No leak (pre-flight I9): to a manager, an Approver and HR without
// staff:personal-data, a sole holder's self-approval of an HR-only request
// reads exactly as the same row approved by another holder of
// staff:personal-data — the inbox, the attention items and their counts,
// /stats, the person's events (the page and its count), the absence list,
// the calendar and the person's own record with its revision, byte for byte
// once the random user ids are taken out.
func TestSoleHolder_NoLeak(t *testing.T) {
	t.Parallel()
	reads := func(w soleWorld) map[string]string {
		out := map[string]string{}
		for name, c := range map[string]*modtest.Client{"the manager": w.bossC, "an Approver": w.approver, "HR without personal-data": w.hr} {
			for _, path := range []string{
				approvalsPath,
				"/api/v1/staff/stats/attention",
				statsPath,
				pagedPath(eventsPath(w.kari.Id), 1, 100),
				fmt.Sprintf("%s?personId=%d", absencesPath, w.kari.Id),
				calendarPath + "?from=2026-10-01&to=2026-12-31",
				personPath(w.kari.Id),
			} {
				r := c.Do(http.MethodGet, path, nil)
				out[name+" GET "+path] = fmt.Sprintf("%d %s", r.Status, scrubRandom(string(r.Body)))
			}
		}
		return out
	}
	world := func(secondDecides bool) map[string]string {
		w := newSoleWorld(t, secondDecides)
		requestOwn(t, w.kariC, absence(t, w.h, "ferie", "2026-10-05", "2026-10-09"))
		a := requestOwn(t, w.kariC, absence(t, w.h, "svangerskapspermisjon", "2026-11-02", "2026-11-06"))
		decider := w.kariC
		if secondDecides {
			decider = w.secondC
		}
		approved(t, decider, a.Id)
		if got := modtest.One[bool](t, w.h.Harness, `SELECT self_approved FROM staff.absences WHERE id = $1`, a.Id); got == secondDecides {
			t.Fatalf("self_approved = %v with the second deciding %v", got, secondDecides)
		}
		return reads(w)
	}
	sole, other := world(false), world(true)
	for key, got := range sole {
		if want := other[key]; got != want {
			t.Errorf("%s differs after a sole holder's self-approval:\n got %s\nwant %s", key, got, want)
		}
	}
	if len(sole) != 21 {
		t.Errorf("%d reads compared, want 21", len(sole))
	}
}
