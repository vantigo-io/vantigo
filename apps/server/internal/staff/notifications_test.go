package staff_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// mailsTo is every mail the mailbox holds for address, subject and body
// joined.
func mailsTo(h *harness, address string) []string {
	var out []string
	for _, m := range h.mail.messages() {
		if slices.Contains(m.To, address) {
			out = append(out, m.Subject+"\n"+m.TextBody)
		}
	}
	return out
}

// lastMail is the newest mail to address, failing the test when there is
// none.
func lastMail(t *testing.T, h *harness, address string) string {
	t.Helper()
	all := mailsTo(h, address)
	if len(all) == 0 {
		t.Fatalf("no mail to %s", address)
	}
	return all[len(all)-1]
}

// leaksIn fails the test when text holds any of words, case aside.
func leaksIn(t *testing.T, what, text string, words ...string) {
	t.Helper()
	for _, w := range words {
		if strings.Contains(strings.ToLower(text), strings.ToLower(w)) {
			t.Errorf("%s names %q:\n%s", what, w, text)
		}
	}
}

// The absence mails (design S12, plan Task 4, readings 4, 15, 26): only to
// addresses Staff holds — a manager-decider of the person, whose link names
// an active user, on a request and a registration; the person on a decision
// and a correction — at the work email, else the private one, in the
// recipient's language. A mail names the type only when it is neither a sick
// kind nor HR-only (an HR-only one is "leave"), and never a note or a
// degree. Holders of staff:approve and staff:manage get no mail; a refusal
// sends none; every send is made outside a transaction.
func TestNotifications_TheAbsenceMails(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h := w.h
	h.Exec(t, `UPDATE staff.people SET work_email = 'bjorn@example.test', preferred_language = 'en' WHERE id = $1`, w.boss.Id)
	h.Exec(t, `UPDATE staff.people SET private_email = 'kari@example.test', preferred_language = 'nb' WHERE id = $1`, w.kari.Id)

	ferie := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-10-05", "2026-10-09", "note", "Hytta på fjellet"))
	m := lastMail(t, h, "bjorn@example.test")
	for _, want := range []string{"Kari Nordmann", "Holiday", "2026-10-05", "2026-10-09"} {
		if !strings.Contains(m, want) {
			t.Errorf("the request's mail to the manager lacks %q:\n%s", want, m)
		}
	}
	leaksIn(t, "the request's mail", m, "Hytta")

	requestOwn(t, w.kariC, absence(t, h, "egenmelding", "2026-09-14", "2026-09-14"))
	leaksIn(t, "the egenmelding's mail to the manager", lastMail(t, h, "bjorn@example.test"), "self-certified", "egenmelding", "sick")
	register(t, w.hr, w.kari.Id, absence(t, h, "sykt_barn", "2026-09-15", "2026-09-15"))
	leaksIn(t, "the sick child's mail to the manager", lastMail(t, h, "bjorn@example.test"), "child", "barn", "sick")
	register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-09-21", "2026-09-25", "degreePercent", 40))
	leaksIn(t, "the sykmelding's mail to the manager", lastMail(t, h, "bjorn@example.test"), "sykmelding", "certified", "sick", "40")
	// An HR-only request is no manager's to hear of (review H3).
	before := len(mailsTo(h, "bjorn@example.test"))
	requestOwn(t, w.kariC, absence(t, h, "svangerskapspermisjon", "2026-11-02", "2026-11-06"))
	if len(mailsTo(h, "bjorn@example.test")) != before {
		t.Error("the manager was mailed about an HR-only request")
	}
	sent := len(h.mail.messages())

	// A refusal sends nothing.
	refused(t, w.kariC.Do(http.MethodPost, meAbsencesPath, absence(t, h, "ferie", "2026-10-07", "2026-10-07")), http.StatusConflict, "absence_overlap")
	refused(t, decide(w.kariC, approvePath(ferie.Id), ""), http.StatusForbidden, "own_request")
	if n := len(h.mail.messages()); n != sent {
		t.Errorf("a refusal sent %d mails", n-sent)
	}

	// The decision to the person, in Norwegian, without the note.
	if r := decide(w.approver, approvePath(ferie.Id), "Kos deg på fjellet"); r.Status != http.StatusOK {
		t.Fatalf("approve: %d %s", r.Status, r.Body)
	}
	m = lastMail(t, h, "kari@example.test")
	if !strings.Contains(m, "Ferie") || !strings.Contains(m, "godkjent") {
		t.Errorf("the approval's mail to Kari:\n%s", m)
	}
	leaksIn(t, "the approval's mail", m, "Kos deg", "Hytta")
	no := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-12-07", "2026-12-07"))
	if r := decide(w.bossC, rejectPath(no.Id), "Inventartelling"); r.Status != http.StatusOK {
		t.Fatalf("reject: %d %s", r.Status, r.Body)
	}
	m = lastMail(t, h, "kari@example.test")
	if !strings.Contains(m, "avslått") {
		t.Errorf("the rejection's mail to Kari:\n%s", m)
	}
	leaksIn(t, "the rejection's mail", m, "Inventartelling")
	do(t, w.hr, http.MethodPost, correctPath(ferie.Id), absence(t, h, "ferie", "2026-10-05", "2026-10-08"), http.StatusOK, nil, ifMatchAs(t, w.hr, ferie.Id))
	if m := lastMail(t, h, "kari@example.test"); !strings.Contains(m, "2026-10-08") {
		t.Errorf("the correction's mail to Kari:\n%s", m)
	}

	// Nobody holding a right is mailed: their addresses are not Staff's.
	for _, msg := range h.mail.messages() {
		for _, to := range msg.To {
			if to != "bjorn@example.test" && to != "kari@example.test" {
				t.Errorf("a mail to %s", to)
			}
		}
	}
	// Every send outside a transaction: the harness fails any test whose
	// request calls out from inside one; the person's and the manager's own
	// requests are checked here too.
	for _, user := range []uuid.UUID{w.kariUser, w.bossUser} {
		for _, c := range contractCalls.by(user) {
			if c.method == "SMTPSend" && c.inTx {
				t.Error("a mail was sent inside a transaction")
			}
		}
	}

	// A manager whose user is disabled is no manager-decider, and is not
	// told; neither is one with no address.
	sent = len(h.mail.messages())
	h.Exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, w.bossUser)
	requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-12-14", "2026-12-14"))
	h.Exec(t, `UPDATE identity.users SET is_disabled = false WHERE id = $1`, w.bossUser)
	h.Exec(t, `UPDATE staff.people SET work_email = NULL, private_email = NULL WHERE id = $1`, w.boss.Id)
	requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-12-15", "2026-12-15"))
	if n := len(h.mail.messages()); n != sent {
		t.Errorf("%d mails to a manager who cannot decide or has no address", n-sent)
	}
	if !strings.Contains(h.Logs(), "no email") {
		t.Error("the skipped mail is not logged")
	}
}

// With the log mail driver no absence mail is attempted.
func TestNotifications_NoneWithoutSMTP(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t, modtest.WithEnv("MAIL_DRIVER", "log"))
	h := w.h
	h.Exec(t, `UPDATE staff.people SET work_email = 'bjorn@example.test' WHERE id = $1`, w.boss.Id)
	a := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-10-05", "2026-10-09"))
	approved(t, w.bossC, a.Id)
	if n := len(h.mail.messages()); n != 0 {
		t.Errorf("%d mails with the log driver", n)
	}
}

// ---- Races (plan Global Constraints "Races") ------------------------------

// raceOn holds Kari's lock on a connection of its own, runs hold in that
// transaction, sends send as the second writer, proves it parked on the
// first by pg_blocking_pids, commits, and answers the second's response.
func raceOn(t *testing.T, w decisionWorld, hold string, args []any, send func(ctx context.Context) *modtest.Response) *modtest.Response {
	t.Helper()
	h := w.h
	probe := ownConn(t, h)
	before := deadlocksIn(t, probe)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	firstConn := ownConn(t, h)
	first, err := firstConn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = first.Rollback(context.Background()) }()
	if _, err := first.Exec(ctx, `SELECT id FROM staff.people WHERE id = $1 FOR NO KEY UPDATE`, w.kari.Id); err != nil {
		t.Fatalf("the first's lock: %v", err)
	}
	if _, err := first.Exec(ctx, hold, args...); err != nil {
		t.Fatalf("the first's write: %v", err)
	}
	answered := make(chan *modtest.Response, 1)
	go func() { answered <- send(ctx) }()
	_, blockers := waiterOn(t, probe)
	if want := []uint32{firstConn.PgConn().PID()}; !slices.Equal(blockers, want) {
		t.Fatalf("the second waits on %v, want the first %v", blockers, want)
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	r := <-answered
	if after := deadlocksIn(t, probe); after != before {
		t.Errorf("deadlocks = %d, was %d", after, before)
	}
	return r
}

// Two deciders at once (plan Task 4): the first has approved under Kari's
// lock; the second waits on it and finds the row no longer requested.
func TestDecisions_TwoDecidersRace(t *testing.T) {
	w := newDecisionWorld(t, modtest.WithPoolMaxConns(2))
	a := requestOwn(t, w.kariC, absence(t, w.h, "ferie", "2026-10-05", "2026-10-09"))
	r := raceOn(t, w, `UPDATE staff.absences SET status = 'approved', revision = revision + 1 WHERE id = $1`, []any{a.Id},
		func(ctx context.Context) *modtest.Response {
			return w.approver.Do(http.MethodPost, approvePath(a.Id), map[string]any{}, modtest.Context(ctx))
		})
	refused(t, r, http.StatusConflict, "absence_not_requested")
}

// An approval racing the person's cancel, both ways: the cancel first, and
// the approval finds no request; the approval first, and the person's cancel
// finds a decided row, which only its decider cancels.
func TestDecisions_ApproveRacesTheCancel(t *testing.T) {
	t.Run("the cancel first", func(t *testing.T) {
		w := newDecisionWorld(t, modtest.WithPoolMaxConns(2))
		a := requestOwn(t, w.kariC, absence(t, w.h, "ferie", "2026-10-05", "2026-10-09"))
		r := raceOn(t, w, `UPDATE staff.absences SET status = 'cancelled', revision = revision + 1 WHERE id = $1`, []any{a.Id},
			func(ctx context.Context) *modtest.Response {
				return w.bossC.Do(http.MethodPost, approvePath(a.Id), map[string]any{}, modtest.Context(ctx))
			})
		refused(t, r, http.StatusConflict, "absence_not_requested")
	})
	t.Run("the approval first", func(t *testing.T) {
		w := newDecisionWorld(t, modtest.WithPoolMaxConns(2))
		a := requestOwn(t, w.kariC, absence(t, w.h, "ferie", "2026-10-05", "2026-10-09"))
		r := raceOn(t, w, `UPDATE staff.absences SET status = 'approved', revision = revision + 1 WHERE id = $1`, []any{a.Id},
			func(ctx context.Context) *modtest.Response {
				return w.kariC.Do(http.MethodPost, cancelAbsencePath(a.Id), nil, modtest.Context(ctx))
			})
		refused(t, r, http.StatusConflict, "absence_not_cancellable")
		if statusIn(t, w.h, a.Id) != "approved" {
			t.Error("the person's cancel undid the approval")
		}
	})
}

// HR's correction racing the decider's cancel (pre-flight M7): the cancel
// has committed under the lock; the correction then finds a cancelled row.
func TestDecisions_CorrectRacesTheCancel(t *testing.T) {
	w := newDecisionWorld(t, modtest.WithPoolMaxConns(2))
	a := requestOwn(t, w.kariC, absence(t, w.h, "ferie", "2026-10-05", "2026-10-09"))
	approved(t, w.bossC, a.Id)
	match := ifMatchAs(t, w.hr, a.Id)
	r := raceOn(t, w, `UPDATE staff.absences SET status = 'cancelled' WHERE id = $1`, []any{a.Id},
		func(ctx context.Context) *modtest.Response {
			return w.hr.Do(http.MethodPost, correctPath(a.Id), absence(t, w.h, "ferie", "2026-10-05", "2026-10-06"), match, modtest.Context(ctx))
		})
	refused(t, r, http.StatusConflict, "absence_not_cancellable")
}
