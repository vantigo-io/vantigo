package invoices_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Holds and the hand-off (invoices payments and reminders design D11). The
// letters are planted by SQL: a printed one with its batch (the print batch
// arrives in Task 13), and a letter being sent — queued, its facts written,
// under a lease live on the harness clock (the worker arrives in Task 12).

func holdPath(id int64) string    { return fmt.Sprintf("%s/%d/hold", invoicesPath, id) }
func liftPath(id int64) string    { return fmt.Sprintf("%s/%d/hold/lift", invoicesPath, id) }
func handoffPath(id int64) string { return fmt.Sprintf("%s/%d/collection", invoicesPath, id) }
func handoffBackPath(id int64) string {
	return fmt.Sprintf("%s/%d/collection/withdraw", invoicesPath, id)
}

type letterLeftJSON struct {
	ReminderID   int64  `json:"reminderId"`
	Status       string `json:"status"`
	PrintBatchID *int64 `json:"printBatchId"`
}

type holdJSON struct {
	ID             int64   `json:"id"`
	Kind           string  `json:"kind"`
	Note           string  `json:"note"`
	PlacedBy       string  `json:"placedBy"`
	LiftedAt       *string `json:"liftedAt"`
	LiftedBy       *string `json:"liftedBy"`
	LiftNote       *string `json:"liftNote"`
	ChargesAllowed *bool   `json:"chargesAllowed"`
}

type handoffJSON struct {
	ID               int64   `json:"id"`
	HandedOn         string  `json:"handedOn"`
	Agency           string  `json:"agency"`
	AgencyReference  string  `json:"agencyReference"`
	Note             string  `json:"note"`
	CreatedBy        string  `json:"createdBy"`
	WithdrawnOn      *string `json:"withdrawnOn"`
	WithdrawnBy      *string `json:"withdrawnBy"`
	WithdrawalReason *string `json:"withdrawalReason"`
}

// heldInvoiceJSON is the part of the document holds and hand-offs show.
type heldInvoiceJSON struct {
	receivablesJSON
	Hold       *holdJSON       `json:"hold"`
	Handoff    *handoffJSON    `json:"handoff"`
	Reminders  []reminderJSON  `json:"reminders"`
	NextAction *nextActionJSON `json:"nextAction"`
}

type holdResultJSON struct {
	Invoice     heldInvoiceJSON  `json:"invoice"`
	LettersLeft []letterLeftJSON `json:"lettersLeft"`
}

// answered asserts res is a 200 hold or hand-off answer and decodes it.
func answered(t *testing.T, what string, res *modtest.Response) holdResultJSON {
	t.Helper()
	if res.Status != http.StatusOK {
		t.Fatalf("%s = %d %s, want 200", what, res.Status, res.Body)
	}
	var got holdResultJSON
	res.JSON(&got)
	return got
}

// heldInvoice reads document id as heldInvoiceJSON, as a payer.
func heldInvoice(t *testing.T, h *harness, id int64) heldInvoiceJSON {
	t.Helper()
	res := chargePayer(t, h).Do(http.MethodGet, fmt.Sprintf("%s/%d", invoicesPath, id), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /invoices/%d = %d %s", id, res.Status, res.Body)
	}
	var got heldInvoiceJSON
	res.JSON(&got)
	return got
}

// plantSending plants a letter of invoice id being sent — queued by e-mail,
// its facts written — whose lease ends at leaseUntil, and answers its id.
func plantSending(t *testing.T, h *harness, id int64, sequence int, leaseUntil time.Time) int64 {
	t.Helper()
	return plantID(t, h, `
		INSERT INTO invoices.reminders (invoice_id, run_id, sequence, level, channel, language, created_at, created_by_user_id,
		    status, sent_on, deadline, regime, principal_open, fee_kind, charges_earlier, interest, interest_waived,
		    interest_paid, total, attempts, next_attempt_at, first_attempt_at, lease_id, lease_until)
		VALUES ($1, $2, $3, 'reminder', 'email', 'nb', now(), gen_random_uuid(), 'queued', DATE '2026-09-12', DATE '2026-09-28',
		    'inkassolov_1988', 1000, 'none', 0, 0, 0, 0, 1000, 0, $4, $4, 'lease-1', $4::timestamptz + ($5 || ' seconds')::interval)
		RETURNING id`, id, plantRun(t, h), sequence, h.Now(), fmt.Sprint(int(leaseUntil.Sub(h.Now()).Seconds())))
}

// letterState is a letter's status, its withdrawal reason and whether a
// user withdrew it.
type letterState struct {
	status, reason string
	byUser         bool
}

func letterStateOf(t *testing.T, h *harness, id int64) letterState {
	t.Helper()
	var s letterState
	if err := h.Pool().QueryRow(context.Background(), `
		SELECT status, coalesce(withdrawal_reason, ''), withdrawn_by_user_id IS NOT NULL FROM invoices.reminders WHERE id = $1`,
		id).Scan(&s.status, &s.reason, &s.byUser); err != nil {
		t.Fatalf("read letter %d: %v", id, err)
	}
	return s
}

// holdNote is a hold's body.
func holdNote(note string) map[string]any { return map[string]any{"note": note} }

// liftBody is a lift's body.
func liftBody(chargesAllowed bool, note string) map[string]any {
	return map[string]any{"chargesAllowed": chargesAllowed, "note": note}
}

// A hold (D11): 400 on the note first; then 404; credit_note_no_reminders
// for a credit note (a draft one too: the kind first), invoice_draft for a
// draft; then, under the lock, the hold — every letter still in flight
// withdrawn on_hold with no user (queued, awaiting print, failed, and a
// queued one with its facts whose lease has expired or is NULL), while a printed letter and a letter
// being sent are left and named in lettersLeft, by sequence; and a second
// hold is invoice_on_hold.
func TestHold_PlaceAndRefusals(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	c := chargePayer(t, h)
	inv := thousand(t, h)

	badOn(t, "a blank note", c.Do(http.MethodPost, holdPath(inv.ID), holdNote("  ")), "note")
	badOn(t, "a note of 501", c.Do(http.MethodPost, holdPath(inv.ID), holdNote(strings.Repeat("n", 501))), "note")
	badOn(t, "a blank note on no document", c.Do(http.MethodPost, holdPath(99999), holdNote("")), "note")
	bareNotFound(t, "no document", c.Do(http.MethodPost, holdPath(99999), holdNote("Bestrider")))
	creditNote := creditOf(t, h, inv.ID, map[int32]float64{1: 1})
	conflictAs(t, "a credit-note draft", c.Do(http.MethodPost, holdPath(creditNote.ID), holdNote("Bestrider")), "credit_note_no_reminders")
	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	conflictAs(t, "a draft", c.Do(http.MethodPost, holdPath(draft.ID), holdNote("Bestrider")), "invoice_draft")

	queued := plantLetterIn(t, h, inv.ID, 1, "queued")
	awaiting := plantLetterIn(t, h, inv.ID, 2, "awaiting_print")
	failed := plantLetterIn(t, h, inv.ID, 3, "failed")
	printed := plantLetterIn(t, h, inv.ID, 4, "printed")
	sending := plantSending(t, h, inv.ID, 5, h.Now().Add(30*time.Second))
	expired := plantSending(t, h, inv.ID, 6, h.Now().Add(-time.Second))
	// Facts written and no lease at all: no live lease, so not being sent.
	unleased := plantSending(t, h, inv.ID, 7, h.Now().Add(30*time.Second))
	h.Exec(t, `UPDATE invoices.reminders SET lease_id = NULL, lease_until = NULL WHERE id = $1`, unleased)
	batch := modtest.One[int64](t, h.Harness, `SELECT print_batch_id FROM invoices.reminders WHERE id = $1`, printed)

	got := answered(t, "the hold", c.Do(http.MethodPost, holdPath(inv.ID), holdNote(" Kunden bestrider timene ")))
	if hd := got.Invoice.Hold; hd == nil || hd.Kind != "disputed" || hd.Note != "Kunden bestrider timene" || hd.LiftedAt != nil || hd.PlacedBy == "" {
		t.Errorf("the hold = %+v, want a live disputed hold with the trimmed note", hd)
	}
	wantLeft := []letterLeftJSON{{ReminderID: printed, Status: "printed", PrintBatchID: &batch}, {ReminderID: sending, Status: "queued"}}
	if !slices.EqualFunc(got.LettersLeft, wantLeft, func(a, b letterLeftJSON) bool {
		return a.ReminderID == b.ReminderID && a.Status == b.Status && idText(a.PrintBatchID) == idText(b.PrintBatchID)
	}) {
		t.Errorf("lettersLeft = %+v, want the printed letter (batch %d) and the one being sent", got.LettersLeft, batch)
	}
	for _, id := range []int64{queued, awaiting, failed, expired, unleased} {
		if s := letterStateOf(t, h, id); s != (letterState{"withdrawn", "on_hold", false}) {
			t.Errorf("letter %d = %+v, want withdrawn on_hold by no user", id, s)
		}
	}
	if s := letterStateOf(t, h, printed); s.status != "printed" {
		t.Errorf("the printed letter = %+v, want it left printed", s)
	}
	if s := letterStateOf(t, h, sending); s.status != "queued" {
		t.Errorf("the letter being sent = %+v, want it left queued", s)
	}
	if got.Invoice.NextAction == nil || got.Invoice.NextAction.Action != "blocked" || !slices.Contains(got.Invoice.NextAction.Reasons, "on_hold") {
		t.Errorf("the next action = %+v, want blocked on_hold", got.Invoice.NextAction)
	}
	conflictAs(t, "a second hold", c.Do(http.MethodPost, holdPath(inv.ID), holdNote("Igjen")), "invoice_on_hold")
	if n := h.Count(t, `SELECT count(*) FROM invoices.invoice_holds WHERE invoice_id = $1`, inv.ID); n != 1 {
		t.Errorf("%d holds, want 1", n)
	}
}

// idText is a nullable id as text, for comparing.
func idText(id *int64) string {
	if id == nil {
		return "<nil>"
	}
	return fmt.Sprint(*id)
}

// The lift (D11) on Saturday 12 September 2026, reminders on with late
// interest. chargesAllowed false — the objection had reasonable grounds —
// waives every fee and compensation claimed and not yet waived,
// objection_upheld with the lift's note, skipping a fee waived before, and
// never interest; the engine then bars charges for good: the next letter
// is fee-free with charges_barred, and still claims interest. true waives
// nothing, and the next letter carries its fee. A body without
// chargesAllowed is a 400; no live hold is invoice_not_on_hold.
func TestHold_LiftBarring(t *testing.T) {
	t.Parallel()
	h := runReady(t, "late_interest = true")
	c := chargePayer(t, h)

	// Two letters' charges on one invoice: a fee waived already, another
	// fee and a compensation.
	several := deliveredOn(t, h, 1, customerAcme, "2026-07-06")
	waivedFirst := plantSent(t, h, several, sentFacts{1, "2026-07-21", "reminder_fee", "35", "", "0"})
	fee := plantSent(t, h, several, sentFacts{2, "2026-08-05", "reminder_fee", "35", "", "1.20"})
	compensation := plantSent(t, h, several, sentFacts{3, "2026-08-20", "compensation", "", "360", "2.40"})
	okAs(t, "a goodwill waiver first", c.Do(http.MethodPost, waivePath(several), waive("goodwill", waiver(waivedFirst, "fee"))))

	badOn(t, "no chargesAllowed", c.Do(http.MethodPost, liftPath(several), map[string]any{"note": "x"}), "chargesAllowed")
	badOn(t, "chargesAllowed null", c.Do(http.MethodPost, liftPath(several), map[string]any{"chargesAllowed": nil}), "chargesAllowed")
	badOn(t, "a note of 501", c.Do(http.MethodPost, liftPath(several), liftBody(false, strings.Repeat("n", 501))), "note")
	conflictAs(t, "no hold yet", c.Do(http.MethodPost, liftPath(several), liftBody(false, "")), "invoice_not_on_hold")
	bareNotFound(t, "no document", c.Do(http.MethodPost, liftPath(99999), liftBody(false, "")))

	answered(t, "the hold", c.Do(http.MethodPost, holdPath(several), holdNote("Bestrider")))
	lifted := answered(t, "the barring lift", c.Do(http.MethodPost, liftPath(several), liftBody(false, "Innsigelsen var begrunnet")))
	if hd := lifted.Invoice.Hold; hd == nil || hd.LiftedAt == nil || hd.ChargesAllowed == nil || *hd.ChargesAllowed ||
		hd.LiftNote == nil || *hd.LiftNote != "Innsigelsen var begrunnet" {
		t.Errorf("the lifted hold = %+v, want lifted, charges barred, with its note", hd)
	}
	type w struct {
		letter       int64
		kind, reason string
		amount       float64
		note         string
	}
	var waivers []w
	for _, x := range *lifted.Invoice.Waivers {
		waivers = append(waivers, w{x.ReminderID, x.Kind, x.Reason, x.Amount, x.Note})
	}
	want := []w{
		{waivedFirst, "fee", "goodwill", 35, ""},
		{fee, "fee", "objection_upheld", 35, "Innsigelsen var begrunnet"},
		{compensation, "compensation", "objection_upheld", 360, "Innsigelsen var begrunnet"},
	}
	if !slices.Equal(waivers, want) {
		t.Errorf("the waivers = %+v, want %+v — a waiver per fee and compensation, none of interest", waivers, want)
	}
	// Interest is not a cost: the latest letter's 2.40 stays claimed.
	chargesAre(t, "after the barring lift", lifted.Invoice.receivablesJSON, 432.4, 430, 0, 2.4, nil)
	conflictAs(t, "a second lift", c.Do(http.MethodPost, liftPath(several), liftBody(false, "")), "invoice_not_on_hold")

	// The engine, on two invoices alike: one reminder with a fee, its
	// deadline missed — barred by one lift, allowed by the other.
	barred := deliveredOn(t, h, 2, customerPerson, "2026-07-06")
	plantLetterSent(t, h, barred, 1, "reminder", "2026-08-05", "35", "1.20")
	allowed := deliveredOn(t, h, 3, customerPerson, "2026-07-06")
	plantLetterSent(t, h, allowed, 1, "reminder", "2026-08-05", "35", "1.20")
	for _, id := range []int64{barred, allowed} {
		answered(t, "a hold", c.Do(http.MethodPost, holdPath(id), holdNote("Bestrider")))
	}
	b := answered(t, "the barring lift", c.Do(http.MethodPost, liftPath(barred), liftBody(false, "")))
	a := answered(t, "the allowing lift", c.Do(http.MethodPost, liftPath(allowed), liftBody(true, "Grunnløs")))
	if n := len(*a.Invoice.Waivers); n != 0 {
		t.Errorf("chargesAllowed true wrote %d waivers, want none", n)
	}
	if hd := a.Invoice.Hold; hd == nil || hd.ChargesAllowed == nil || !*hd.ChargesAllowed {
		t.Errorf("the allowing lift's hold = %+v, want chargesAllowed true", hd)
	}
	nb, na := b.Invoice.NextAction, a.Invoice.NextAction
	if nb == nil || nb.Letter == nil || nb.Letter.Fee != 0 || nb.Letter.Compensation != 0 || !slices.Contains(nb.ChargeNotes, "charges_barred") ||
		nb.Letter.Interest <= 0 {
		t.Errorf("the barred invoice's next letter = %+v, want it fee-free, charges_barred, with interest", nb)
	}
	if na == nil || na.Letter == nil || na.Letter.Fee <= 0 || slices.Contains(na.ChargeNotes, "charges_barred") {
		t.Errorf("the allowed invoice's next letter = %+v, want it with a fee", na)
	}
}

// A held invoice still takes payments (D11): the creditor still owns the
// claim, and the debtor may pay what is not disputed.
func TestHold_PaymentsStillRegistered(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := thousand(t, h)
	c := chargePayer(t, h)
	answered(t, "the hold", c.Do(http.MethodPost, holdPath(inv.ID), holdNote("Bestrider en av timene")))
	paid := registered(t, h, inv.ID, pay(920, "2026-09-12"))
	if paid.State != "partially_paid" || paid.OpenAmount == nil || *paid.OpenAmount != 80 {
		t.Errorf("after the payment = %s open %v, want partially paid with 80 open", paid.State, money(paid.OpenAmount))
	}
	if hd := heldInvoice(t, h, inv.ID).Hold; hd == nil || hd.LiftedAt != nil {
		t.Errorf("the hold after the payment = %+v, want it still live", hd)
	}
}

// Each write takes the invoice first, then its letters (D18): a hold and a
// hand-off lock the invoice, then the letters they withdraw, by id; a lift —
// a barring one too, whose waivers are inserted under the invoice's lock —
// and a hand-off's withdrawal lock the invoice alone. A printed letter is
// not locked: it is left.
func TestHoldsAndHandoffs_LockOrder(t *testing.T) {
	h := runReady(t, "")
	c := chargePayer(t, h)
	held := deliveredOn(t, h, 1, customerPerson, "2026-07-06")
	fee := plantLetterSent(t, h, held, 1, "reminder", "2026-08-05", "35", "")
	q1 := plantLetterIn(t, h, held, 2, "queued")
	plantLetterIn(t, h, held, 3, "printed")
	q2 := plantLetterIn(t, h, held, 4, "awaiting_print")
	handed := deliveredOn(t, h, 2, customerPerson, "2026-07-06")
	q3 := plantLetterIn(t, h, handed, 1, "failed")

	seen := &lockSeen{}
	defer invoices.SetLockTaken(seen.note)()
	check := func(what string, want ...string) {
		t.Helper()
		if got := seen.take(); !slices.Equal(got, want) {
			t.Errorf("%s locked %v, want %v", what, got, want)
		}
	}
	answered(t, "the hold", c.Do(http.MethodPost, holdPath(held), holdNote("Bestrider")))
	check("the hold", "invoice "+idKey(held), "reminder "+idKey(min(q1, q2)), "reminder "+idKey(max(q1, q2)))
	lifted := answered(t, "the barring lift", c.Do(http.MethodPost, liftPath(held), liftBody(false, "")))
	check("the barring lift", "invoice "+idKey(held))
	if ws := *lifted.Invoice.Waivers; len(ws) != 1 || ws[0].ReminderID != fee || ws[0].Reason != "objection_upheld" {
		t.Errorf("the barring lift's waivers = %+v, want the fee's, written under the invoice's lock", ws)
	}
	answered(t, "the hand-off", c.Do(http.MethodPost, handoffPath(handed), handoffBody("2026-09-12", "Kredinor AS")))
	check("the hand-off", "invoice "+idKey(handed), "reminder "+idKey(q3))
	answered(t, "the withdrawal", c.Do(http.MethodPost, handoffBackPath(handed), map[string]any{"withdrawnOn": "2026-09-12", "reason": "Betalt direkte"}))
	check("the withdrawal", "invoice "+idKey(handed))
}
