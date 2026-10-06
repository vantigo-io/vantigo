package invoices_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Reminder runs (invoices payments and reminders design D10): the preview,
// which writes nothing, and the run, which makes the letters the caller saw,
// one invoice per transaction under its lock, without their facts. The
// installation sends e-mail (MAIL_DRIVER smtp), so a customer with a
// reminder address is written to by e-mail; Acme's is purring@acme.example.

// runReady is an installation that can send e-mail, Acme with a reminder
// address and Kari without one, reminders on with set (see remindersOn).
func runReady(t *testing.T, set string, opts ...modtest.Option) *harness {
	t.Helper()
	fake := &fakeSMTP{}
	h := newHarness(t, append([]modtest.Option{modtest.WithSMTPSend(fake.send), modtest.WithEnv("MAIL_DRIVER", "smtp"),
		modtest.WithEnv("SMTP_HOST", "smtp.example.invalid"), modtest.WithEnv("SMTP_FROM", "faktura@example.invalid")}, opts...)...)
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.ReminderEmail = "purring@acme.example" })
	remindersOn(t, h, set)
	return h
}

type previewLetterJSON struct {
	InvoiceID   int64           `json:"invoiceId"`
	Number      int64           `json:"number"`
	CustomerID  int32           `json:"customerId"`
	BuyerName   string          `json:"buyerName"`
	Action      string          `json:"action"`
	Letter      letterFactsJSON `json:"letter"`
	ChargeNotes []string        `json:"chargeNotes"`
	Channel     string          `json:"channel"`
	Recipient   string          `json:"recipient"`
	Warnings    []string        `json:"warnings"`
}

type previewHeldJSON struct {
	InvoiceID  int64          `json:"invoiceId"`
	Number     int64          `json:"number"`
	NextAction nextActionJSON `json:"nextAction"`
}

type previewJSON struct {
	Letters          []previewLetterJSON `json:"letters"`
	BlockedOrWaiting []previewHeldJSON   `json:"blockedOrWaiting"`
	Freshness        freshnessJSON       `json:"freshness"`
	Warnings         []string            `json:"warnings"`
}

type reminderJSON struct {
	ID                  int64    `json:"id"`
	InvoiceID           int64    `json:"invoiceId"`
	Sequence            int32    `json:"sequence"`
	Level               string   `json:"level"`
	AnnouncesCollection bool     `json:"announcesCollection"`
	Channel             string   `json:"channel"`
	Recipient           *string  `json:"recipient"`
	Language            string   `json:"language"`
	Status              string   `json:"status"`
	RunID               int64    `json:"runId"`
	CreatedBy           string   `json:"createdBy"`
	SentOn              *string  `json:"sentOn"`
	Deadline            *string  `json:"deadline"`
	FeeKind             *string  `json:"feeKind"`
	Fee                 *float64 `json:"fee"`
	Interest            *float64 `json:"interest"`
	Total               *float64 `json:"total"`
	Attempts            int32    `json:"attempts"`
	NextAttemptAt       *string  `json:"nextAttemptAt"`
}

type runJSON struct {
	ID                      int64   `json:"id"`
	RunOn                   string  `json:"runOn"`
	CreatedBy               string  `json:"createdBy"`
	Letters                 *int    `json:"letters"`
	Skipped                 *int    `json:"skipped"`
	LastBookedOn            *string `json:"lastBookedOn"`
	StaleImportAcknowledged bool    `json:"staleImportAcknowledged"`
}

type runSkipJSON struct {
	InvoiceID int64  `json:"invoiceId"`
	Reason    string `json:"reason"`
}

type runResultJSON struct {
	Run     runJSON        `json:"run"`
	Created []reminderJSON `json:"created"`
	Skipped []runSkipJSON  `json:"skipped"`
}

// runProblemJSON is a run's 409 as a client reads it.
type runProblemJSON struct {
	Code         string  `json:"code"`
	Detail       string  `json:"detail"`
	Kind         *string `json:"kind"`
	HalfYear     *string `json:"halfYear"`
	LastBookedOn *string `json:"lastBookedOn"`
}

// previewOf previews a run as a payer, failing unless 200.
func previewOf(t *testing.T, h *harness) previewJSON {
	t.Helper()
	res := payer(t, h).Do(http.MethodPost, reminderRunsPath, map[string]any{"dryRun": true})
	if res.Status != http.StatusOK {
		t.Fatalf("the preview = %d %s, want 200", res.Status, res.Body)
	}
	var p previewJSON
	res.JSON(&p)
	return p
}

// item is a run's item: the invoice and the action its preview showed.
func item(id int64, action string) map[string]any {
	return map[string]any{"invoiceId": id, "action": action}
}

// runBody is a run's body over items.
func runBody(ack *bool, items ...map[string]any) map[string]any {
	body := map[string]any{"dryRun": false, "items": items}
	if ack != nil {
		body["acknowledgeStaleImport"] = *ack
	}
	return body
}

// run posts a run as c.
func run(c *modtest.Client, body map[string]any, opts ...modtest.RequestOption) *modtest.Response {
	return c.Do(http.MethodPost, reminderRunsPath, body, opts...)
}

// made asserts res is a 201 and answers the run.
func made(t *testing.T, what string, res *modtest.Response) runResultJSON {
	t.Helper()
	if res.Status != http.StatusCreated {
		t.Fatalf("%s = %d %s, want 201", what, res.Status, res.Body)
	}
	var r runResultJSON
	res.JSON(&r)
	return r
}

// refusedRun asserts res is a 409 with code and answers it.
func refusedRun(t *testing.T, what string, res *modtest.Response, code string) runProblemJSON {
	t.Helper()
	var p runProblemJSON
	if res.Status != http.StatusConflict {
		t.Errorf("%s = %d %s, want 409 %s", what, res.Status, res.Body, code)
		return p
	}
	res.JSON(&p)
	if p.Code != code {
		t.Errorf("%s = %s (%s), want %s", what, p.Code, p.Detail, code)
	}
	return p
}

// letterRows is how many letters invoice id has.
func letterRows(t *testing.T, h *harness, id int64) int {
	t.Helper()
	return h.Count(t, `SELECT count(*) FROM invoices.reminders WHERE invoice_id = $1`, id)
}

var yes, no = ptr(true), ptr(false)

// The preview (D10) — on Saturday 12 September 2026, late interest on, no
// bank file ever imported: each letter as it would go today — Acme's 1 000
// due 3 August, delivered: a reminder with the fee 38 (750 ÷ 20, rounded),
// interest 13.42 (4 August–12 September, 40 days at 12.25 %) from 4 August
// in one segment, the total 1 051.42, the deadline 26 September moved off
// the Saturday to Monday 28, by e-mail to Acme's reminder address; Kari's by
// paper, reminder_email_missing; an undelivered one fee-free with its note —
// the invoices waiting or held with their reasons; the bank data stale;
// nothing written and no lock taken.
func TestReminderRun_Preview(t *testing.T) {
	h := runReady(t, "late_interest = true")
	acme := deliveredOn(t, h, 1, customerAcme, "2026-08-03")
	kari := deliveredOn(t, h, 2, customerPerson, "2026-08-03")
	undelivered := plantOverdue(t, h, overdueSpec{number: 3, customer: customerAcme, issue: "2026-07-01", due: "2026-08-03"})
	waiting := deliveredOn(t, h, 4, customerAcme, "2026-08-05")
	plantLetterSent(t, h, waiting, 1, "reminder", "2026-09-05", "", "")
	held := deliveredOn(t, h, 5, customerAcme, "2026-08-03")
	h.Exec(t, `INSERT INTO invoices.invoice_holds (invoice_id, kind, note, placed_at, placed_by_user_id)
		VALUES ($1, 'disputed', 'Bestrider', now(), gen_random_uuid())`, held)
	letters := h.Count(t, `SELECT count(*) FROM invoices.reminders`)
	runs := h.Count(t, `SELECT count(*) FROM invoices.reminder_runs`)
	seen := &lockSeen{}
	defer invoices.SetLockTaken(seen.note)()

	p := previewOf(t, h)
	if len(p.Letters) != 3 {
		t.Fatalf("the preview's letters = %+v, want three", p.Letters)
	}
	byID := map[int64]previewLetterJSON{}
	for _, l := range p.Letters {
		byID[l.InvoiceID] = l
	}
	l := byID[acme]
	f := l.Letter
	switch {
	case l.Action != "reminder" || f.Level != "reminder" || f.Regime != "inkassolov_1988" || f.AnnouncesCollection:
		t.Errorf("Acme's letter = %+v, want a reminder under the 1988 regime", l)
	case f.FeeKind != "reminder_fee" || f.Fee != 38 || f.Inkassosats == nil || *f.Inkassosats != 750 || f.Compensation != 0:
		t.Errorf("Acme's fee = %s %v of %v, want reminder_fee 38 of 750", f.FeeKind, f.Fee, f.Inkassosats)
	case f.Interest != 13.42 || f.InterestFrom == nil || *f.InterestFrom != "2026-08-04" ||
		!slices.Equal(f.InterestSegments, []segmentJSON{{From: "2026-08-04", To: "2026-09-12", Rate: 12.25, Base: 1000}}):
		t.Errorf("Acme's interest = %v from %v in %+v, want 13.42 from 4 August in one segment", f.Interest, f.InterestFrom, f.InterestSegments)
	case f.PrincipalOpen != 1000 || f.ChargesEarlier != 0 || f.InterestWaived != 0 || f.InterestPaid != 0 || f.Total != 1051.42:
		t.Errorf("Acme's figures = %+v, want 1 000 open and 1 051.42 in all", f)
	case f.Deadline != "2026-09-28":
		t.Errorf("Acme's deadline = %s, want Monday 28 September, off Saturday the 26th", f.Deadline)
	case l.Channel != "email" || l.Recipient != "purring@acme.example" || len(l.Warnings) != 0 || len(l.ChargeNotes) != 0:
		t.Errorf("Acme's channel = %s to %q, warnings %v, notes %v; want e-mail to purring@acme.example, none", l.Channel, l.Recipient, l.Warnings, l.ChargeNotes)
	}
	if k := byID[kari]; k.Channel != "paper" || k.Recipient != "" || !slices.Equal(k.Warnings, []string{"reminder_email_missing"}) {
		t.Errorf("Kari's channel = %s to %q with %v, want paper, reminder_email_missing", k.Channel, k.Recipient, k.Warnings)
	}
	if u := byID[undelivered]; u.Letter.FeeKind != "none" || u.Letter.Fee != 0 || u.Letter.Interest != 0 ||
		!slices.Equal(u.ChargeNotes, []string{"not_delivered"}) {
		t.Errorf("the undelivered one = %+v, want a fee-free letter without interest, noted not_delivered", u)
	}
	held2 := map[int64][]string{}
	for _, b := range p.BlockedOrWaiting {
		held2[b.InvoiceID] = b.NextAction.Reasons
	}
	if len(held2) != 2 || !slices.Equal(held2[waiting], []string{"waiting"}) || !slices.Equal(held2[held], []string{"on_hold"}) {
		t.Errorf("blocked or waiting = %v, want the waiting one and the held one with their reasons", held2)
	}
	if !p.Freshness.Stale || p.Freshness.LastBookedOn != nil || !slices.Equal(p.Warnings, []string{"bank_data_stale"}) {
		t.Errorf("freshness = %+v, warnings %v; want stale with no booking, bank_data_stale", p.Freshness, p.Warnings)
	}
	if got := h.Count(t, `SELECT count(*) FROM invoices.reminders`); got != letters {
		t.Errorf("letters after the preview = %d, want %d: nothing written", got, letters)
	}
	if got := h.Count(t, `SELECT count(*) FROM invoices.reminder_runs`); got != runs {
		t.Errorf("runs after the preview = %d, want %d: nothing written", got, runs)
	}
	if got := seen.take(); len(got) != 0 {
		t.Errorf("the preview locked %v, want nothing", got)
	}
}

// The run's refusals, in order (D10; plan reading 27): the fields first —
// no items, an invoice twice, an id that is not an issued invoice — then
// reminders_disabled even with an outdated letter among the items; then,
// judged before anything is written, collection_rates_outdated naming the
// kind and the half-year (an invoice due in 2023, whose interest needs
// 2023-H2) — in 2027 before collection_regime_unreviewed, which a fee letter
// then meets alone with interest off — and bank_import_stale: with the flag
// absent refused (no lastBookedOn while no file was ever imported, the
// latest booking once one was), with it true made, and a run of fee-free
// letters only never held back. A refusal writes nothing.
func TestReminderRun_RefusalsInOrder(t *testing.T) {
	t.Parallel()
	h := runReady(t, "late_interest = true")
	h.Exec(t, `UPDATE invoices.reminder_settings SET enabled = false`)
	c := payer(t, h)
	fee := deliveredOn(t, h, 1, customerAcme, "2026-08-03")
	old := plantOverdue(t, h, overdueSpec{number: 2, customer: customerAcme, issue: "2023-10-01", due: "2023-11-01", deliveredOnIssueOn: true})
	feeFree := deliveredOn(t, h, 3, customerForeign, "2026-08-03")
	plantPolicy(t, h, customerForeign, "no_charges", "")
	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID

	badOn(t, "no items", run(c, map[string]any{"dryRun": false}), "items")
	badOn(t, "an empty run", run(c, runBody(nil)), "items")
	badOn(t, "an invoice twice", run(c, runBody(nil, item(fee, "reminder"), item(fee, "reminder"))), "items")
	badOn(t, "a draft", run(c, runBody(nil, item(draft, "reminder"))), "items")
	badOn(t, "no such invoice", run(c, runBody(nil, item(999999, "reminder"))), "items")
	refusedRun(t, "reminders off", run(c, runBody(nil, item(old, "reminder"), item(fee, "reminder"))), "reminders_disabled")
	remindersOn(t, h, "")

	p := refusedRun(t, "an interest period in 2023-H2", run(c, runBody(yes, item(fee, "reminder"), item(old, "reminder"))), "collection_rates_outdated")
	if p.Kind == nil || *p.Kind != "late_interest_percent" || p.HalfYear == nil || *p.HalfYear != "2023-H2" {
		t.Errorf("collection_rates_outdated names %v %v, want late_interest_percent 2023-H2", p.Kind, p.HalfYear)
	}

	moveClockTo(t, h, time.Date(2027, time.January, 4, 8, 0, 0, 0, time.UTC))
	c = payer(t, h) // a session of the new day
	p = refusedRun(t, "2027 with interest on", run(c, runBody(yes, item(fee, "reminder"))), "collection_rates_outdated")
	if p.HalfYear == nil || *p.HalfYear != "2027-H1" {
		t.Errorf("collection_rates_outdated in 2027 names %v, want 2027-H1 — judged before the review", p.HalfYear)
	}
	h.Exec(t, `UPDATE invoices.reminder_settings SET late_interest = false`)
	refusedRun(t, "a fee after the review", run(c, runBody(yes, item(fee, "reminder"))), "collection_regime_unreviewed")
	h.Exec(t, `UPDATE invoices.reminder_settings SET regime_reviewed_through = DATE '2027-06-30'`)

	p = refusedRun(t, "a fee, no bank file ever", run(c, runBody(nil, item(fee, "reminder"))), "bank_import_stale")
	if p.LastBookedOn != nil {
		t.Errorf("bank_import_stale without a file names %v, want no lastBookedOn", *p.LastBookedOn)
	}
	plantBankFile(t, h, "2026-12-20")
	p = refusedRun(t, "a fee, booked to 20 December", run(c, runBody(no, item(fee, "reminder"))), "bank_import_stale")
	if p.LastBookedOn == nil || *p.LastBookedOn != "2026-12-20" {
		t.Errorf("bank_import_stale names %v, want 2026-12-20", p.LastBookedOn)
	}
	if n := letterRows(t, h, fee); n != 0 {
		t.Errorf("letters after the refusals = %d, want none", n)
	}
	if r := made(t, "fee-free letters on stale data", run(c, runBody(nil, item(feeFree, "reminder")))); len(r.Created) != 1 {
		t.Errorf("the fee-free run = %+v, want its letter: never held back by stale data", r)
	}
	if r := made(t, "the fee confirmed", run(c, runBody(yes, item(fee, "reminder")))); len(r.Created) != 1 || !r.Run.StaleImportAcknowledged {
		t.Errorf("the confirmed run = %+v, want its letter and the confirmation recorded", r)
	}
}

// The recipient and the channel (D10): Acme's billing profile says paper, so
// its two letters go by paper with no address; Svenska's says e-mail with an
// address, so its letter is queued for e-mail, due at once, in English — its
// buyer snapshot's language; Kari wants e-mail and has no address, so paper.
// The directory is asked once per customer, three times, and never under a
// lock (the harness fails a locked call). Without SMTP every letter goes by
// paper, mail_unavailable.
func TestReminderRun_RecipientAndChannel(t *testing.T) {
	t.Parallel()
	h := runReady(t, "")
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.ReminderDelivery = "paper" })
	h.customers.edit(customerForeign, func(p *contracts.CustomerBillingProfile) { p.ReminderEmail = "ap@svenska.example" })
	acme1 := deliveredOn(t, h, 1, customerAcme, "2026-08-03")
	acme2 := deliveredOn(t, h, 2, customerAcme, "2026-08-04")
	svenska := plantOverdue(t, h, overdueSpec{number: 3, customer: customerForeign, issue: "2026-07-01", due: "2026-08-03", language: "en"})
	kari := deliveredOn(t, h, 4, customerPerson, "2026-08-03")
	var mu sync.Mutex
	asked := map[int32]int{}
	h.customers.afterProfileRead(func(id int32) {
		mu.Lock()
		defer mu.Unlock()
		asked[id]++
	})

	r := made(t, "the run", run(payer(t, h), runBody(yes, item(acme1, "reminder"), item(acme2, "reminder"),
		item(svenska, "reminder"), item(kari, "reminder"))))
	if len(r.Created) != 4 {
		t.Fatalf("the run made %+v, want four letters", r.Created)
	}
	for _, l := range r.Created {
		switch l.InvoiceID {
		case acme1, acme2, kari:
			if l.Channel != "paper" || l.Status != "awaiting_print" || l.Recipient == nil || *l.Recipient != "" || l.NextAttemptAt != nil {
				t.Errorf("letter of %d = %+v, want paper awaiting print, no address", l.InvoiceID, l)
			}
		case svenska:
			if l.Channel != "email" || l.Status != "queued" || l.Recipient == nil || *l.Recipient != "ap@svenska.example" ||
				l.Language != "en" || l.NextAttemptAt == nil {
				t.Errorf("Svenska's letter = %+v, want e-mail queued to ap@svenska.example, due now, in English", l)
			}
		}
		if l.InvoiceID != svenska && l.Language != "nb" {
			t.Errorf("letter of %d is in %q, want nb", l.InvoiceID, l.Language)
		}
	}
	mu.Lock()
	if want := map[int32]int{customerAcme: 1, customerForeign: 1, customerPerson: 1}; fmt.Sprint(asked) != fmt.Sprint(want) {
		t.Errorf("the directory was asked %v, want once per customer: %v", asked, want)
	}
	mu.Unlock()

	off := newHarness(t)
	remindersOn(t, off, "")
	off.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.ReminderEmail = "purring@acme.example" })
	deliveredOn(t, off, 1, customerAcme, "2026-08-03")
	p := previewOf(t, off)
	if len(p.Letters) != 1 || p.Letters[0].Channel != "paper" || !slices.Equal(p.Letters[0].Warnings, []string{"mail_unavailable"}) {
		t.Errorf("without SMTP = %+v, want paper, mail_unavailable", p.Letters)
	}
}

// The skips (D10): an item whose customer was anonymised since the preview
// is skipped customer_anonymised; one a payment settled between the preview
// and the run, and one whose action is not the engine's, action_changed.
// Nothing is written for them, and the run counts them.
func TestReminderRun_SkipReasons(t *testing.T) {
	t.Parallel()
	h := runReady(t, "")
	gone := deliveredOn(t, h, 1, customerPerson, "2026-08-03")
	paid := deliveredOn(t, h, 2, customerAcme, "2026-08-03")
	other := deliveredOn(t, h, 3, customerAcme, "2026-08-04")
	if p := previewOf(t, h); len(p.Letters) != 3 {
		t.Fatalf("the preview = %+v, want three letters", p.Letters)
	}
	h.Exec(t, `INSERT INTO invoices.erased_customers (customer_id, erased_at) VALUES ($1, now())`, customerPerson)
	registered(t, h, paid, pay(1000, "2026-09-12"))

	r := made(t, "the run", run(payer(t, h), runBody(yes, item(gone, "reminder"), item(paid, "reminder"), item(other, "collection_notice"))))
	want := []runSkipJSON{{gone, "customer_anonymised"}, {paid, "action_changed"}, {other, "action_changed"}}
	if !slices.Equal(r.Skipped, want) || len(r.Created) != 0 {
		t.Errorf("the run = created %+v, skipped %+v; want none, and %+v", r.Created, r.Skipped, want)
	}
	if r.Run.Letters == nil || *r.Run.Letters != 0 || r.Run.Skipped == nil || *r.Run.Skipped != 3 {
		t.Errorf("the run's counts = %v and %v, want 0 and 3", r.Run.Letters, r.Run.Skipped)
	}
	for _, id := range []int64{gone, paid, other} {
		if n := letterRows(t, h, id); n != 0 {
			t.Errorf("letters of %d = %d, want none", id, n)
		}
	}
}

// A run takes 500 invoices and no more (D10): 501 is a 400 on items; 500 are
// each judged in a transaction of its own — 499 of an anonymised customer,
// skipped customer_anonymised right after their lock, and one that is made —
// and counted on the run.
func TestReminderRun_FiveHundredItems(t *testing.T) {
	t.Parallel()
	h := runReady(t, "")
	h.Exec(t, `
		INSERT INTO invoices.invoices (kind, status, number, customer_id, issue_date, due_date, exchange_rate_date,
		    seller_legal_name, buyer_name, gross_total, issued_at, created_by_user_id, created_at, updated_at)
		SELECT 'invoice', 'issued', n, CASE WHEN n = 500 THEN $1::int ELSE $2::int END, DATE '2026-07-01', DATE '2026-08-03',
		    DATE '2026-07-01', 'Selger AS', 'Kunde AS', 100, now(), gen_random_uuid(), now(), now()
		FROM generate_series(1, 501) AS n`, customerAcme, customerPerson)
	h.Exec(t, `INSERT INTO invoices.erased_customers (customer_id, erased_at) VALUES ($1, now())`, customerPerson)
	rows, err := h.Pool().Query(context.Background(), `SELECT id FROM invoices.invoices ORDER BY number`)
	if err != nil {
		t.Fatal(err)
	}
	var items []map[string]any
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		items = append(items, item(id, "reminder"))
	}
	rows.Close()
	c := payer(t, h)
	badOn(t, "501 items", run(c, runBody(yes, items...)), "items")
	r := made(t, "500 items", run(c, runBody(yes, items[:500]...)))
	if len(r.Created) != 1 || len(r.Skipped) != 499 || r.Run.Letters == nil || *r.Run.Letters != 1 ||
		r.Run.Skipped == nil || *r.Run.Skipped != 499 {
		t.Errorf("500 items = %d made and %d skipped, counted %v and %v; want 1 and 499", len(r.Created), len(r.Skipped),
			r.Run.Letters, r.Run.Skipped)
	}
}

// A run's letter carries no facts until it is sent (D10, amendment 12): the
// preview showed its fee, interest and deadline; the row the run wrote has
// no sent_on, deadline, regime, amounts or charge notes — every one NULL —
// and the answer none; the invoice is now blocked letter_pending.
func TestReminderRun_LettersCarryNoFactsUntilSent(t *testing.T) {
	t.Parallel()
	h := runReady(t, "late_interest = true")
	id := deliveredOn(t, h, 1, customerAcme, "2026-08-03")
	if p := previewOf(t, h); len(p.Letters) != 1 || p.Letters[0].Letter.Fee != 38 || p.Letters[0].Letter.Interest == 0 {
		t.Fatalf("the preview = %+v, want the letter with its fee and interest", p.Letters)
	}
	r := made(t, "the run", run(payer(t, h), runBody(yes, item(id, "reminder"))))
	if len(r.Created) != 1 {
		t.Fatalf("the run = %+v, want one letter", r)
	}
	l := r.Created[0]
	if l.SentOn != nil || l.Deadline != nil || l.FeeKind != nil || l.Fee != nil || l.Interest != nil || l.Total != nil ||
		l.Sequence != 1 || l.Level != "reminder" || l.Attempts != 0 {
		t.Errorf("the letter = %+v, want sequence 1, a reminder, no facts", l)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.reminders WHERE id = $1 AND sent_on IS NULL AND deadline IS NULL
		AND regime IS NULL AND principal_open IS NULL AND fee_kind IS NULL AND fee IS NULL AND compensation IS NULL
		AND charges_earlier IS NULL AND interest IS NULL AND interest_waived IS NULL AND interest_paid IS NULL
		AND interest_from IS NULL AND interest_segments IS NULL AND inkassosats IS NULL AND total IS NULL
		AND charge_notes IS NULL AND sent_at IS NULL`, l.ID); n != 1 {
		t.Errorf("the row of letter %d carries facts, want none until it is sent", l.ID)
	}
	if got := overdueOf(t, reader(t, h), ""); got.Items[0].NextAction.Action != "blocked" ||
		!slices.Equal(got.Items[0].NextAction.Reasons, []string{"letter_pending"}) {
		t.Errorf("after the run = %+v, want blocked letter_pending", got.Items[0].NextAction)
	}
}

// The run row (D10; plan reading 3): written before its letters with the
// day, the caller, the bank data's last booking and the confirmation; its
// letters and skipped NULL while the run is making letters, then set once at
// the end — and never again, the trigger's.
func TestReminderRun_TheRunRow(t *testing.T) {
	h := runReady(t, "")
	plantBankFile(t, h, "2026-09-01")
	a := deliveredOn(t, h, 1, customerAcme, "2026-08-03")
	b := deliveredOn(t, h, 2, customerAcme, "2026-08-04")
	var during []string
	defer invoices.SetRunItemAfterLock(func(_ context.Context, invoiceID int64) error {
		var letters, skipped *int32
		err := h.Pool().QueryRow(context.Background(), `SELECT letters, skipped FROM invoices.reminder_runs ORDER BY id DESC LIMIT 1`).Scan(&letters, &skipped)
		during = append(during, fmt.Sprint(invoiceID, letters == nil, skipped == nil, err))
		return nil
	})()
	c, userID := h.SignInUser(t, "invoices:access", "invoices:payments")
	r := made(t, "the run", run(c, runBody(yes, item(a, "reminder"), item(b, "collection_notice"))))
	if want := []string{fmt.Sprint(a, true, true, nil), fmt.Sprint(b, true, true, nil)}; !slices.Equal(during, want) {
		t.Errorf("the counts during the run = %v, want NULL for both items: %v", during, want)
	}
	run := r.Run
	if run.RunOn != "2026-09-12" || run.CreatedBy != userID.String() || run.LastBookedOn == nil || *run.LastBookedOn != "2026-09-01" ||
		!run.StaleImportAcknowledged || run.Letters == nil || *run.Letters != 1 || run.Skipped == nil || *run.Skipped != 1 {
		t.Errorf("the run = %+v, want 12 September, the caller, booked to 1 September, confirmed, 1 letter and 1 skipped", run)
	}
	if _, err := h.Pool().Exec(context.Background(), `UPDATE invoices.reminder_runs SET letters = 5, skipped = 0 WHERE id = $1`, run.ID); err == nil {
		t.Error("a second count was written, want the trigger to refuse it: the counts are set once")
	}
}

// The runs, newest first and paged, and one run with its letters as they
// stand now (D10; plan reading 26); an unknown run is a 404.
func TestReminderRun_ListAndDetail(t *testing.T) {
	t.Parallel()
	h := runReady(t, "")
	c := payer(t, h)
	a := deliveredOn(t, h, 1, customerAcme, "2026-08-03")
	b := deliveredOn(t, h, 2, customerPerson, "2026-08-03")
	first := made(t, "the first run", run(c, runBody(yes, item(a, "reminder"))))
	second := made(t, "the second run", run(c, runBody(yes, item(b, "reminder"))))

	res := c.Do(http.MethodGet, reminderRunsPath+"?pageSize=1", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("the runs = %d %s", res.Status, res.Body)
	}
	var page struct {
		Data       []runJSON `json:"data"`
		Pagination struct {
			TotalCount int `json:"totalCount"`
		} `json:"pagination"`
	}
	res.JSON(&page)
	if len(page.Data) != 1 || page.Data[0].ID != second.Run.ID || page.Pagination.TotalCount != 2 {
		t.Errorf("the runs' first page = %+v, want the second run of two", page)
	}
	res = c.Do(http.MethodGet, fmt.Sprintf("%s/%d", reminderRunsPath, first.Run.ID), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("the first run = %d %s", res.Status, res.Body)
	}
	var detail struct {
		Run     runJSON        `json:"run"`
		Letters []reminderJSON `json:"letters"`
	}
	res.JSON(&detail)
	if detail.Run.ID != first.Run.ID || len(detail.Letters) != 1 || detail.Letters[0].InvoiceID != a || detail.Letters[0].Status != "queued" {
		t.Errorf("the first run = %+v, want its one queued letter of %d", detail, a)
	}
	if res := c.Do(http.MethodGet, reminderRunsPath+"/999999", nil); res.Status != http.StatusNotFound {
		t.Errorf("an unknown run = %d, want 404", res.Status)
	}
	if res := c.Do(http.MethodGet, reminderRunsPath+"?page=0", nil); res.Status != http.StatusBadRequest {
		t.Errorf("page 0 = %d, want 400", res.Status)
	}
}

// Outdated rates refuse (D6; plan reading 5): on 4 January 2027 with late
// interest on and no row for 2027-H1, a letter with interest is refused 409
// naming late_interest_percent and 2027-H1, and the list warns; a fee-free
// letter without interest — a customer whose policy is no_charges — is made.
func TestCollectionRates_Outdated(t *testing.T) {
	t.Parallel()
	h := runReady(t, "late_interest = true, regime_reviewed_through = DATE '2027-06-30'")
	plantBankFile(t, h, "2027-01-03")
	interest := deliveredOn(t, h, 1, customerAcme, "2026-11-02")
	plain := deliveredOn(t, h, 2, customerForeign, "2026-11-02")
	plantPolicy(t, h, customerForeign, "no_charges", "")
	moveClockTo(t, h, time.Date(2027, time.January, 4, 8, 0, 0, 0, time.UTC))
	c := payer(t, h)

	p := refusedRun(t, "a letter with interest", run(c, runBody(nil, item(interest, "reminder"), item(plain, "reminder"))), "collection_rates_outdated")
	if p.Kind == nil || *p.Kind != "late_interest_percent" || p.HalfYear == nil || *p.HalfYear != "2027-H1" ||
		!strings.Contains(p.Detail, "2027-H1") {
		t.Errorf("the refusal names %v %v (%s), want late_interest_percent and 2027-H1", p.Kind, p.HalfYear, p.Detail)
	}
	if got := overdueOf(t, reader(t, h), ""); !slices.Contains(got.Warnings, "collection_rates_outdated") ||
		got.Items[0].NextAction.Outdated == nil || got.Items[0].NextAction.Outdated.HalfYear != "2027-H1" {
		t.Errorf("the list = %v, %+v; want the warning and the item blocked on 2027-H1", got.Warnings, got.Items[0].NextAction)
	}
	if r := made(t, "the fee-free letter", run(c, runBody(nil, item(plain, "reminder")))); len(r.Created) != 1 {
		t.Errorf("the fee-free run = %+v, want its letter", r)
	}
}

// The regime review lapses (D6): on 1 January 2027 with neither the review
// nor inkassolov_2026_from moved, a fee letter is refused
// collection_regime_unreviewed, and so is a collection notice without a fee;
// a fee-free reminder is made. Moving the review through the settings' PUT
// clears it, and the fee letter is made.
func TestRegime_ReviewLapses(t *testing.T) {
	t.Parallel()
	h := runReady(t, "")
	plantBankFile(t, h, "2026-12-31")
	fee := deliveredOn(t, h, 1, customerAcme, "2026-11-02")
	notice := deliveredOn(t, h, 2, customerForeign, "2026-10-01")
	plantLetterSent(t, h, notice, 1, "reminder", "2026-11-02", "", "")
	reminder := deliveredOn(t, h, 3, customerForeign, "2026-11-02")
	plantPolicy(t, h, customerForeign, "no_charges", "")
	moveClockTo(t, h, time.Date(2027, time.January, 1, 9, 0, 0, 0, time.UTC))
	c := payer(t, h)

	refusedRun(t, "a fee letter", run(c, runBody(nil, item(fee, "reminder"))), "collection_regime_unreviewed")
	refusedRun(t, "a collection notice without a fee", run(c, runBody(nil, item(notice, "collection_notice"))), "collection_regime_unreviewed")
	if r := made(t, "a fee-free reminder", run(c, runBody(nil, item(reminder, "reminder")))); len(r.Created) != 1 {
		t.Errorf("the fee-free reminder = %+v, want it made", r)
	}

	settings := readReminderSettings(t, h)
	body := with(reminderSettingsBody(settings.Revision), "regimeReviewedThrough", "2027-06-30")
	if res := manager(t, h).Do(http.MethodPut, reminderSettingsPath, body); res.Status != http.StatusOK {
		t.Fatalf("moving the review = %d %s", res.Status, res.Body)
	}
	if r := made(t, "the fee letter after the review", run(c, runBody(nil, item(fee, "reminder")))); len(r.Created) != 1 {
		t.Errorf("the fee letter after the review = %+v, want it made", r)
	}
}

// Each item locks its invoice and nothing else (D18): the seam sees the
// items' invoices in the run's order, one each, and the letters are
// inserted.
func TestReminderRun_LockOrder(t *testing.T) {
	h := runReady(t, "")
	a := deliveredOn(t, h, 1, customerAcme, "2026-08-03")
	b := deliveredOn(t, h, 2, customerAcme, "2026-08-04")
	seen := &lockSeen{}
	defer invoices.SetLockTaken(seen.note)()
	r := made(t, "the run", run(payer(t, h), runBody(yes, item(b, "reminder"), item(a, "reminder"))))
	if got, want := seen.take(), []string{"invoice " + idKey(b), "invoice " + idKey(a)}; !slices.Equal(got, want) {
		t.Errorf("the run locked %v, want %v", got, want)
	}
	if len(r.Created) != 2 || letterRows(t, h, a) != 1 || letterRows(t, h, b) != 1 {
		t.Errorf("the run made %+v, want a letter each", r.Created)
	}
}

// Meta's reminder capabilities (D1): remindersEnabled is the settings'
// switch; canRunReminders is invoices:payments'.
func TestMeta_RemindersCapabilities(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	type metaReminders struct {
		RemindersEnabled bool `json:"remindersEnabled"`
		Capabilities     struct {
			CanRunReminders bool `json:"canRunReminders"`
		} `json:"capabilities"`
	}
	read := func(c *modtest.Client) metaReminders {
		t.Helper()
		res := c.Do(http.MethodGet, invoicesPath+"/meta", nil)
		if res.Status != http.StatusOK {
			t.Fatalf("GET /meta = %d %s", res.Status, res.Body)
		}
		var m metaReminders
		res.JSON(&m)
		return m
	}
	if m := read(payer(t, h)); m.RemindersEnabled || !m.Capabilities.CanRunReminders {
		t.Errorf("a payer, reminders off = %+v, want off and may run", m)
	}
	remindersOn(t, h, "")
	if m := read(reader(t, h)); !m.RemindersEnabled || m.Capabilities.CanRunReminders {
		t.Errorf("a reader, reminders on = %+v, want on and may not run", m)
	}
}

// An issued invoice answers its next action, its letters and the interest
// accrued today (D8, D10, D12): before a run its reminder with the letter's
// facts and the charges' interestToday; after it the letter, its recipient
// to a payer only, and blocked letter_pending.
func TestDocument_NextActionAndReminders(t *testing.T) {
	t.Parallel()
	h := runReady(t, "late_interest = true")
	id := deliveredOn(t, h, 1, customerAcme, "2026-08-03")
	type documentJSON struct {
		Charges struct {
			InterestToday *float64 `json:"interestToday"`
		} `json:"charges"`
		NextAction *nextActionJSON `json:"nextAction"`
		Reminders  *[]reminderJSON `json:"reminders"`
	}
	read := func(c *modtest.Client) documentJSON {
		t.Helper()
		res := c.Do(http.MethodGet, invoicePath(id), nil)
		if res.Status != http.StatusOK {
			t.Fatalf("GET /invoices/%d = %d %s", id, res.Status, res.Body)
		}
		var d documentJSON
		res.JSON(&d)
		return d
	}
	d := read(reader(t, h))
	if d.NextAction == nil || d.NextAction.Action != "reminder" || d.NextAction.Letter == nil || d.NextAction.Letter.Fee != 38 ||
		d.Charges.InterestToday == nil || *d.Charges.InterestToday != 13.42 || d.Reminders == nil || len(*d.Reminders) != 0 {
		t.Errorf("before the run = %+v, want a reminder with its fee, interestToday 13.42 and no letters", d)
	}
	made(t, "the run", run(payer(t, h), runBody(yes, item(id, "reminder"))))
	d = read(reader(t, h))
	if d.NextAction == nil || d.NextAction.Action != "blocked" || d.Reminders == nil || len(*d.Reminders) != 1 ||
		(*d.Reminders)[0].Recipient != nil {
		t.Errorf("after the run, as a reader = %+v, want blocked and the letter without its recipient", d)
	}
	if d := read(payer(t, h)); d.Reminders == nil || len(*d.Reminders) != 1 || (*d.Reminders)[0].Recipient == nil ||
		*(*d.Reminders)[0].Recipient != "purring@acme.example" {
		t.Errorf("after the run, as a payer = %+v, want the letter to purring@acme.example", d.Reminders)
	}
	if invoices.LetterLanguage(ptr("en")) != "en" || invoices.LetterLanguage(ptr("nb")) != "nb" || invoices.LetterLanguage(nil) != "nb" {
		t.Error("a letter's language is the snapshot's en, else nb")
	}
}
