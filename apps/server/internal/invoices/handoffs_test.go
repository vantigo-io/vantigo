package invoices_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
)

// handoffBody is a hand-off's body: handed to agency on handedOn.
func handoffBody(handedOn, agency string) map[string]any {
	return map[string]any{"handedOn": handedOn, "agency": agency}
}

// withField is body with key set to value.
func withField(body map[string]any, key string, value any) map[string]any {
	body[key] = value
	return body
}

// The hand-off (D11), refused in order on Saturday 12 September 2026: the
// fields first — handedOn missing or after today, the agency blank or too
// long, the reference and the note too long, all before the document is
// read; then 404; credit_note_no_reminders, invoice_draft; then handedOn
// before the issue date; then under the lock invoice_settled before
// invoice_handed_off before invoice_not_delivered — which the
// acknowledgement lets through, and a delivery on the due day itself
// passes. The hand-off withdraws the letters in flight
// handed_off and leaves the printed one, named; the engine then answers none,
// handed_off; and a payment is still registered.
func TestHandoff_AndRefusals(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	c := chargePayer(t, h)

	badOn(t, "no handedOn", c.Do(http.MethodPost, handoffPath(99999), map[string]any{"agency": "Kredinor AS"}), "handedOn")
	badOn(t, "handedOn tomorrow", c.Do(http.MethodPost, handoffPath(99999), handoffBody("2026-09-13", "Kredinor AS")), "handedOn")
	badOn(t, "a blank agency", c.Do(http.MethodPost, handoffPath(99999), handoffBody("2026-09-12", " ")), "agency")
	badOn(t, "an agency of 201", c.Do(http.MethodPost, handoffPath(99999), handoffBody("2026-09-12", strings.Repeat("a", 201))), "agency")
	badOn(t, "a reference of 101", c.Do(http.MethodPost, handoffPath(99999),
		withField(handoffBody("2026-09-12", "Kredinor AS"), "agencyReference", strings.Repeat("r", 101))), "agencyReference")
	badOn(t, "a note of 501", c.Do(http.MethodPost, handoffPath(99999),
		withField(handoffBody("2026-09-12", "Kredinor AS"), "note", strings.Repeat("n", 501))), "note")
	bareNotFound(t, "no document", c.Do(http.MethodPost, handoffPath(99999), handoffBody("2026-09-12", "Kredinor AS")))
	original := thousand(t, h)
	creditNote := creditOf(t, h, original.ID, map[int32]float64{1: 1})
	conflictAs(t, "a credit-note draft", c.Do(http.MethodPost, handoffPath(creditNote.ID), handoffBody("2026-06-01", "Kredinor AS")), "credit_note_no_reminders")
	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	conflictAs(t, "a draft", c.Do(http.MethodPost, handoffPath(draft.ID), handoffBody("2026-06-01", "Kredinor AS")), "invoice_draft")

	inv := deliveredOn(t, h, 101, customerAcme, "2026-07-15") // issued 1 July
	badOn(t, "handedOn before the issue date", c.Do(http.MethodPost, handoffPath(inv), handoffBody("2026-06-30", "Kredinor AS")), "handedOn")
	queued := plantLetterIn(t, h, inv, 1, "queued")
	printed := plantLetterIn(t, h, inv, 2, "printed")
	got := answered(t, "the hand-off", c.Do(http.MethodPost, handoffPath(inv),
		withField(withField(handoffBody("2026-07-01", " Kredinor AS "), "agencyReference", " K-77 "), "note", "Overlevert på telefon")))
	if ho := got.Invoice.Handoff; ho == nil || ho.HandedOn != "2026-07-01" || ho.Agency != "Kredinor AS" || ho.AgencyReference != "K-77" ||
		ho.Note != "Overlevert på telefon" || ho.WithdrawnOn != nil || ho.CreatedBy == "" {
		t.Errorf("the hand-off = %+v, want it live with the trimmed fields", ho)
	}
	if s := letterStateOf(t, h, queued); s != (letterState{"withdrawn", "handed_off", false}) {
		t.Errorf("the queued letter = %+v, want withdrawn handed_off by no user", s)
	}
	if len(got.LettersLeft) != 1 || got.LettersLeft[0].ReminderID != printed || got.LettersLeft[0].Status != "printed" {
		t.Errorf("lettersLeft = %+v, want the printed letter %d", got.LettersLeft, printed)
	}
	if n := got.Invoice.NextAction; n == nil || n.Action != "none" || !slices.Equal(n.Reasons, []string{"handed_off"}) {
		t.Errorf("the next action = %+v, want none, handed_off", n)
	}
	conflictAs(t, "a second hand-off", c.Do(http.MethodPost, handoffPath(inv), handoffBody("2026-09-12", "Lindorff")), "invoice_handed_off")
	// Payments are still registered: the claim is still the creditor's.
	if paid := registered(t, h, inv, pay(1000, "2026-09-12")); paid.State != "paid" {
		t.Errorf("a payment while handed off = %s, want it registered and the invoice paid", paid.State)
	}
	// Paid and handed off: settled is judged first.
	conflictAs(t, "settled and handed off", c.Do(http.MethodPost, handoffPath(inv), handoffBody("2026-09-12", "Lindorff")), "invoice_settled")

	// No delivery on or before the due date: refused unless acknowledged; a
	// delivery after the due date counts for nothing. Handed off already is
	// judged before it.
	undelivered := plantOverdue(t, h, overdueSpec{number: 102, customer: customerAcme, issue: "2026-07-01", due: "2026-07-15"})
	plantManualDelivery(t, h, undelivered, "2026-07-16")
	p := conflictAs(t, "not delivered", c.Do(http.MethodPost, handoffPath(undelivered), handoffBody("2026-09-12", "Kredinor AS")), "invoice_not_delivered")
	if !strings.Contains(p.Detail, "record a manual delivery") {
		t.Errorf("the detail = %q, want it to say to record a manual delivery first", p.Detail)
	}
	answered(t, "acknowledged", c.Do(http.MethodPost, handoffPath(undelivered),
		withField(handoffBody("2026-09-12", "Kredinor AS"), "acknowledgeNotDelivered", true)))
	conflictAs(t, "handed off and not delivered", c.Do(http.MethodPost, handoffPath(undelivered), handoffBody("2026-09-12", "Kredinor AS")), "invoice_handed_off")
	if n := h.Count(t, `SELECT count(*) FROM invoices.collection_handoffs WHERE invoice_id IN ($1, $2)`, inv, undelivered); n != 2 {
		t.Errorf("%d hand-offs, want one each", n)
	}
	// A delivery on the due day itself is on or before it: no acknowledgement.
	onTheDay := plantOverdue(t, h, overdueSpec{number: 103, customer: customerAcme, issue: "2026-07-01", due: "2026-07-15"})
	plantManualDelivery(t, h, onTheDay, "2026-07-15")
	answered(t, "delivered on the due day", c.Do(http.MethodPost, handoffPath(onTheDay), handoffBody("2026-09-12", "Kredinor AS")))
}

// The withdrawal (D11): 400 on the fields — withdrawnOn missing or after
// today, the reason blank or too long; 404; invoice_not_handed_off before a
// hand-off and after its withdrawal; 400 on a withdrawnOn before the
// hand-off's handedOn, decided under the lock; then the hand-off is ended
// with who, when and why, and the engine judges the invoice again.
func TestHandoff_Withdraw(t *testing.T) {
	t.Parallel()
	h := runReady(t, "")
	c := chargePayer(t, h)
	inv := deliveredOn(t, h, 1, customerAcme, "2026-08-03")
	back := func(on, reason string) map[string]any { return map[string]any{"withdrawnOn": on, "reason": reason} }

	badOn(t, "no withdrawnOn", c.Do(http.MethodPost, handoffBackPath(inv), map[string]any{"reason": "Betalt"}), "withdrawnOn")
	badOn(t, "withdrawnOn tomorrow", c.Do(http.MethodPost, handoffBackPath(inv), back("2026-09-13", "Betalt")), "withdrawnOn")
	badOn(t, "a blank reason", c.Do(http.MethodPost, handoffBackPath(inv), back("2026-09-12", " ")), "reason")
	badOn(t, "a reason of 201", c.Do(http.MethodPost, handoffBackPath(inv), back("2026-09-12", strings.Repeat("r", 201))), "reason")
	bareNotFound(t, "no document", c.Do(http.MethodPost, handoffBackPath(99999), back("2026-09-12", "Betalt")))
	conflictAs(t, "never handed off", c.Do(http.MethodPost, handoffBackPath(inv), back("2026-09-12", "Betalt")), "invoice_not_handed_off")

	answered(t, "the hand-off", c.Do(http.MethodPost, handoffPath(inv), handoffBody("2026-09-10", "Kredinor AS")))
	badOn(t, "withdrawn before it was made", c.Do(http.MethodPost, handoffBackPath(inv), back("2026-09-09", "Betalt")), "withdrawnOn")
	got := answered(t, "the withdrawal", c.Do(http.MethodPost, handoffBackPath(inv), back("2026-09-11", " Kunden betalte direkte ")))
	if ho := got.Invoice.Handoff; ho == nil || ho.WithdrawnOn == nil || *ho.WithdrawnOn != "2026-09-11" || ho.WithdrawnBy == nil ||
		ho.WithdrawalReason == nil || *ho.WithdrawalReason != "Kunden betalte direkte" {
		t.Errorf("the withdrawn hand-off = %+v, want withdrawn on 2026-09-11 with who and why", ho)
	}
	if n := got.Invoice.NextAction; n == nil || n.Action != "reminder" {
		t.Errorf("the next action after the withdrawal = %+v, want a reminder again", n)
	}
	if len(got.LettersLeft) != 0 {
		t.Errorf("lettersLeft = %+v, want none", got.LettersLeft)
	}
	conflictAs(t, "withdrawn twice", c.Do(http.MethodPost, handoffBackPath(inv), back("2026-09-12", "Igjen")), "invoice_not_handed_off")
	// Handed off again after a withdrawal: the latest is the document's.
	again := answered(t, "a second hand-off", c.Do(http.MethodPost, handoffPath(inv), handoffBody("2026-09-12", "Lindorff AS")))
	if ho := again.Invoice.Handoff; ho == nil || ho.Agency != "Lindorff AS" || ho.WithdrawnOn != nil {
		t.Errorf("the document's hand-off = %+v, want the live one to Lindorff AS", ho)
	}
}
