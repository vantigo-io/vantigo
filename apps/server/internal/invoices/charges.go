package invoices

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/reminderrules"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the charges (invoices payments and reminders design D9): what
// an issued invoice's sent reminders claimed — a fee or the compensation,
// and the interest — kept apart from its principal. The payments, the open
// amount and the state never read them (Finanstilsynet's 2020 letter: fees
// and interest are never folded into the principal). They are paid by charge
// payments and released by waivers; what is outstanding is
// reminderrules.Charges' formula over the sent letters, the waivers and the
// live charge payments — the same terms a letter's own total states.
//
// Every write takes one lock, the invoice FOR UPDATE through lockInvoice
// (D18: a charge payment, a waiver — the invoice alone; the children's parent
// triggers share it), and reads every figure it decides on after it, so a
// letter, a waiver or a charge payment another transaction holds the invoice
// for has committed by then.

// The codes and titles of the charges' refusals.
const (
	codeNoChargesOutstanding            = "no_charges_outstanding"
	codeChargePaymentExceedsOutstanding = "charge_payment_exceeds_outstanding"
	codeChargeNotClaimed                = "charge_not_claimed"
	codeCreditNoteNoReminders           = "credit_note_no_reminders"

	cannotRegisterChargeTitle = "The charge payment cannot be registered"
	cannotRemoveChargeTitle   = "The charge payment cannot be removed"
	cannotWaiveTitle          = "The charges cannot be waived"
	invalidChargePaymentTitle = "Invalid charge payment"
	invalidWaiverTitle        = "Invalid charge waiver"
)

// maxWaiversPerRequest bounds one waive request's waivers.
const maxWaiversPerRequest = 50

// The sources of a charge payment, as invoices.charge_payments stores them.
const sourceManual = "manual"

var (
	// errNoSuchChargePayment is a removal naming a charge payment that is not
	// the invoice's.
	errNoSuchChargePayment = errors.New("invoices: no such charge payment on the invoice")
	// errNoSuchLetter is a waiver naming a letter that is not the invoice's.
	errNoSuchLetter = errors.New("invoices: no such letter on the invoice")
)

// chargeRows is what chargesOf read: the sent letters by sequence, every
// waiver and every charge payment (removed ones included), and the same as
// the engine's inputs — the letters, the waivers and the live charge
// payments only.
type chargeRows struct {
	letters  []store.InvoicesReminder
	waivers  []store.InvoicesChargeWaiver
	payments []store.InvoicesChargePayment

	ruleLetters  []reminderrules.Letter
	ruleWaivers  []reminderrules.Waiver
	livePayments []reminderrules.ChargePayment
}

// ratOrNil reads a nullable numeric: nil for NULL.
func ratOrNil(n pgtype.Numeric) (*big.Rat, error) {
	if !n.Valid {
		return nil, nil
	}
	return ratFromNumeric(n)
}

// dayOrNil reads a nullable date: nil for NULL.
func dayOrNil(d pgtype.Date) *time.Time {
	if !d.Valid {
		return nil
	}
	day := utcDay(d.Time)
	return &day
}

// letterOf is a stored letter as the engine reads it (reminderrules.Letter):
// its facts nil where it has none.
func letterOf(r store.InvoicesReminder) (reminderrules.Letter, error) {
	l := reminderrules.Letter{
		ID: r.ID, Sequence: int(r.Sequence), Level: reminderrules.Level(r.Level), AnnouncesCollection: r.AnnouncesCollection,
		Status: r.Status, SentOn: dayOrNil(r.SentOn), Deadline: dayOrNil(r.Deadline),
	}
	if r.FeeKind != nil {
		l.FeeKind = reminderrules.FeeKind(*r.FeeKind)
	}
	var err error
	for _, f := range []struct {
		dst **big.Rat
		n   pgtype.Numeric
	}{{&l.Fee, r.Fee}, {&l.Compensation, r.Compensation}, {&l.Interest, r.Interest}, {&l.PrincipalOpen, r.PrincipalOpen}} {
		if *f.dst, err = ratOrNil(f.n); err != nil {
			return reminderrules.Letter{}, fmt.Errorf("invoices: read letter %d's facts: %w", r.ID, err)
		}
	}
	return l, nil
}

// chargesOf reads an invoice's sent letters, waivers and charge payments with
// q — which holds the invoice's lock, or a plain read for a response — and
// answers its charges through reminderrules.Charges: only sent letters claim,
// and only the live charge payments are passed.
func chargesOf(ctx context.Context, q *store.Queries, invoiceID int64) (reminderrules.ChargeState, chargeRows, error) {
	var rows chargeRows
	var err error
	if rows.letters, err = q.SentLettersOf(ctx, invoiceID); err != nil {
		return reminderrules.ChargeState{}, rows, fmt.Errorf("invoices: read document %d's sent letters: %w", invoiceID, err)
	}
	if rows.waivers, err = q.WaiversOf(ctx, invoiceID); err != nil {
		return reminderrules.ChargeState{}, rows, fmt.Errorf("invoices: read document %d's waivers: %w", invoiceID, err)
	}
	if rows.payments, err = q.ChargePaymentsOf(ctx, invoiceID); err != nil {
		return reminderrules.ChargeState{}, rows, fmt.Errorf("invoices: read document %d's charge payments: %w", invoiceID, err)
	}
	for _, r := range rows.letters {
		l, err := letterOf(r)
		if err != nil {
			return reminderrules.ChargeState{}, rows, err
		}
		rows.ruleLetters = append(rows.ruleLetters, l)
	}
	for _, w := range rows.waivers {
		amount, err := ratFromNumeric(w.Amount)
		if err != nil {
			return reminderrules.ChargeState{}, rows, err
		}
		rows.ruleWaivers = append(rows.ruleWaivers, reminderrules.Waiver{ID: w.ID, ReminderID: w.ReminderID, Kind: w.Kind, Amount: amount})
	}
	for _, p := range rows.payments {
		if p.RemovedAt != nil {
			continue
		}
		amount, err := ratFromNumeric(p.Amount)
		if err != nil {
			return reminderrules.ChargeState{}, rows, err
		}
		rows.livePayments = append(rows.livePayments, reminderrules.ChargePayment{ID: p.ID, PaidOn: utcDay(p.PaidOn.Time), Amount: amount})
	}
	return reminderrules.Charges(rows.ruleLetters, rows.ruleWaivers, rows.livePayments), rows, nil
}

// insertChargePayment records a charge payment (D9). The caller holds the
// invoice FOR UPDATE and has judged, under that lock, that p.Amount is at
// most the charges outstanding; p names the source — manual, or ocr/camt054
// with the bank line — the currency, who and when. The bank match and the
// queue's apply call it too.
func insertChargePayment(ctx context.Context, txq *store.Queries, p store.InsertChargePaymentParams) (store.InvoicesChargePayment, error) {
	row, err := txq.InsertChargePayment(ctx, p)
	if err != nil {
		return row, fmt.Errorf("invoices: register a charge payment against %d: %w", p.InvoiceID, err)
	}
	return row, nil
}

// waiverRequest is one charge to waive: a letter and the kind of its charge.
type waiverRequest struct {
	reminderID int64
	kind       string // reminderrules.WaiverFee, WaiverCompensation or WaiverInterest
}

// waiveCharges writes the waivers ws of invoice invoiceID (D9), judged in
// order, each against what the earlier ones left. The caller holds the
// invoice FOR UPDATE (lockInvoice); every figure is read here, after it. A
// letter that is not the invoice's is errNoSuchLetter (404). A fee or the
// compensation is waived whole; it is charge_not_claimed when the letter is
// not sent, claimed no such charge, or it is waived already. Interest is
// waived on the latest sent letter, as an amount: the cumulative interest it
// claimed less every interest waiver and the charge payments allocated to
// interest (reminderrules.Allocate) — what is claimed and unpaid, nothing
// accrued since — and charge_not_claimed on an earlier letter or when none is
// left. A refusal answers the problem and writes nothing the caller keeps:
// it rolls back with errRefused. The bank match (deadline_met), the lift that
// bars charges and the posted re-judge call it too, with their own reason.
func waiveCharges(ctx context.Context, txq *store.Queries, invoiceID int64, ws []waiverRequest, reason, note string, by uuid.UUID, at time.Time) (*gen.InvoicesConflictProblem, error) {
	_, rows, err := chargesOf(ctx, txq, invoiceID)
	if err != nil {
		return nil, err
	}
	sent := make(map[int64]reminderrules.Letter, len(rows.ruleLetters))
	var latest *reminderrules.Letter
	for i, l := range rows.ruleLetters {
		if l.SentOn == nil {
			continue
		}
		sent[l.ID] = l
		if latest == nil || l.SentOn.After(*latest.SentOn) || (l.SentOn.Equal(*latest.SentOn) && l.Sequence > latest.Sequence) {
			latest = &rows.ruleLetters[i]
		}
	}
	waivers := rows.ruleWaivers
	notClaimed := func(detail string) (*gen.InvoicesConflictProblem, error) {
		return ptr(conflict(codeChargeNotClaimed, cannotWaiveTitle, detail)), nil
	}
	for _, w := range ws {
		letter, ok := sent[w.reminderID]
		if !ok {
			// Not a sent letter of this invoice: another invoice's, or none
			// at all, is not found; one of its own still in flight or
			// withdrawn claimed nothing.
			if _, err := txq.ReminderOf(ctx, store.ReminderOfParams{ID: w.reminderID, InvoiceID: invoiceID}); errors.Is(err, pgx.ErrNoRows) {
				return nil, errNoSuchLetter
			} else if err != nil {
				return nil, fmt.Errorf("invoices: read letter %d of %d: %w", w.reminderID, invoiceID, err)
			}
			return notClaimed(fmt.Sprintf("Letter %d was never sent, so it claimed no charge.", w.reminderID))
		}
		var amount *big.Rat
		var through *time.Time
		switch w.kind {
		case reminderrules.WaiverFee, reminderrules.WaiverCompensation:
			claimed := letter.Fee
			if w.kind == reminderrules.WaiverCompensation {
				claimed = letter.Compensation
			}
			if claimed == nil || claimed.Sign() <= 0 {
				return notClaimed(fmt.Sprintf("Letter %d claimed no %s.", letter.ID, w.kind))
			}
			for _, earlier := range waivers {
				if earlier.ReminderID == letter.ID && earlier.Kind == w.kind {
					return notClaimed(fmt.Sprintf("Letter %d's %s is waived already.", letter.ID, w.kind))
				}
			}
			amount = claimed
		case reminderrules.WaiverInterest:
			if latest == nil || letter.ID != latest.ID {
				return notClaimed(fmt.Sprintf("Interest is claimed cumulatively by the latest letter sent, %d, and is waived on it.", latest.ID))
			}
			_, interestPaid, _ := reminderrules.Allocate(rows.ruleLetters, waivers, rows.livePayments)
			amount = new(big.Rat)
			if letter.Interest != nil {
				amount.Set(letter.Interest)
			}
			for _, earlier := range waivers {
				if earlier.Kind == reminderrules.WaiverInterest {
					amount.Sub(amount, earlier.Amount)
				}
			}
			amount.Sub(amount, interestPaid)
			if amount.Sign() <= 0 {
				return notClaimed(fmt.Sprintf("No interest letter %d claimed is left unpaid: it is waived or paid.", letter.ID))
			}
			through = letter.SentOn
		default:
			return nil, fmt.Errorf("invoices: a waiver of kind %q", w.kind)
		}
		n, err := numericFromRat(amount, 2)
		if err != nil {
			return nil, fmt.Errorf("invoices: a waiver's amount as numeric: %w", err)
		}
		params := store.InsertWaiverParams{
			InvoiceID: invoiceID, ReminderID: letter.ID, Kind: w.kind, Amount: n,
			Reason: reason, Note: note, WaivedByUserID: by, WaivedAt: at,
		}
		if through != nil {
			params.InterestThrough = pgDate(*through)
		}
		row, err := txq.InsertWaiver(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("invoices: waive letter %d's %s: %w", letter.ID, w.kind, err)
		}
		stored, err := ratFromNumeric(row.Amount)
		if err != nil {
			return nil, err
		}
		waivers = append(waivers, reminderrules.Waiver{ID: row.ID, ReminderID: row.ReminderID, Kind: row.Kind, Amount: stored})
	}
	return nil, nil
}

// withCharges answers an issued invoice's charges on resp (D9): the block —
// claimed, waived, paid, outstanding and refundDue only when it is above
// zero — every charge payment (removed ones included with their removal),
// every waiver and every manual delivery (D8).
func withCharges(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice, resp *gen.InvoicesInvoiceResponse) error {
	state, rows, err := chargesOf(ctx, q, inv.ID)
	if err != nil {
		return err
	}
	resp.Charges = &gen.InvoicesCharges{
		Claimed: floatFromRat(state.Claimed, 2), Waived: floatFromRat(state.Waived, 2),
		Paid: floatFromRat(state.Paid, 2), Outstanding: floatFromRat(state.Outstanding, 2),
	}
	if state.RefundDue.Sign() > 0 {
		resp.Charges.RefundDue = ptr(floatFromRat(state.RefundDue, 2))
	}
	payments := make([]gen.InvoicesChargePayment, 0, len(rows.payments))
	for _, p := range rows.payments {
		amount, err := ratFromNumeric(p.Amount)
		if err != nil {
			return err
		}
		payments = append(payments, gen.InvoicesChargePayment{
			Id: p.ID, PaidOn: wireDate(p.PaidOn.Time), Amount: floatFromRat(amount, 2), Currency: p.Currency,
			Source: gen.InvoicesChargePaymentSource(p.Source), BankTransactionId: p.BankTransactionID,
			Reference: p.Reference, Note: p.Note, RegisteredAt: p.RegisteredAt, RegisteredByUserId: p.RegisteredByUserID,
			RemovedAt: p.RemovedAt, RemovedByUserId: p.RemovedByUserID, RemovalReason: p.RemovalReason,
		})
	}
	resp.ChargePayments = &payments
	waivers := make([]gen.InvoicesChargeWaiver, 0, len(rows.waivers))
	for _, w := range rows.waivers {
		amount, err := ratFromNumeric(w.Amount)
		if err != nil {
			return err
		}
		waivers = append(waivers, gen.InvoicesChargeWaiver{
			Id: w.ID, ReminderId: w.ReminderID, Kind: gen.InvoicesChargeWaiverKind(w.Kind), Amount: floatFromRat(amount, 2),
			InterestThrough: wireDateOf(w.InterestThrough), Reason: gen.InvoicesChargeWaiverReason(w.Reason), Note: w.Note,
			WaivedBy: w.WaivedByUserID, WaivedAt: w.WaivedAt,
		})
	}
	resp.Waivers = &waivers
	return withManualDeliveries(ctx, q, inv.ID, resp)
}

// issuedInvoiceFor reads document id on the pool for a charges write and
// judges what it is before the body: nil and no problem when it is gone;
// a credit note, draft or issued, is creditNoteCode (the kind decided first,
// as the payments decide it); a draft invoice_draft.
func issuedInvoiceFor(ctx context.Context, q *store.Queries, id int64, creditNoteCode, title, creditNoteDetail, draftDetail string) (*store.InvoicesInvoice, *gen.InvoicesConflictProblem, error) {
	inv, err := q.GetInvoice(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("invoices: read document %d: %w", id, err)
	}
	switch {
	case inv.Kind == kindCreditNote:
		return nil, ptr(conflict(creditNoteCode, title, creditNoteDetail)), nil
	case inv.Status != statusIssued:
		return nil, ptr(conflict(codeInvoiceDraft, title, draftDetail)), nil
	}
	return &inv, nil, nil
}

// removalReason judges a removal's reason, trimmed: 1 to 200 characters.
func removalReason(reason string) (string, map[string][]string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return reason, fieldError("reason", "A removal needs a reason")
	}
	if msg := maxLength("A reason", reason, 200); msg != "" {
		return reason, fieldError("reason", msg)
	}
	return reason, nil
}

// PostInvoicesByIdChargePayments Register a charge payment
// (POST /api/v1/invoices/{id}/charge-payments)
func (s *server) PostInvoicesByIdChargePayments(ctx context.Context, req gen.PostInvoicesByIdChargePaymentsRequestObject) (gen.PostInvoicesByIdChargePaymentsResponseObject, error) {
	q := store.New(s.deps.Pool)
	inv, refusal, err := issuedInvoiceFor(ctx, q, req.Id, codeCreditNoteNoPayments, cannotRegisterChargeTitle,
		"A charge payment is registered against an invoice, never a credit note.",
		"A draft is not a sales document: it has no charges to pay.")
	if err != nil {
		return nil, err
	}
	if refusal != nil {
		return gen.PostInvoicesByIdChargePayments409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	if inv == nil {
		return gen.PostInvoicesByIdChargePayments404Response{}, nil
	}
	// The payment's own fields and rules (D2).
	payment, errs, err := parsePayment(gen.InvoicesPaymentRequest(*req.Body), inv.IssueDate.Time, businessDay(s.deps.Clock()))
	if err != nil {
		return nil, err
	}
	if errs != nil {
		return gen.PostInvoicesByIdChargePayments400ApplicationProblemPlusJSONResponse(invalid(invalidChargePaymentTitle, errs)), nil
	}
	amt, err := ratFromNumeric(payment.Amount)
	if err != nil {
		return nil, err
	}

	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		locked, err := lockInvoice(ctx, txq, inv.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errDocumentGone
		}
		if err != nil {
			return fmt.Errorf("invoices: lock document %d: %w", inv.ID, err)
		}
		state, _, err := chargesOf(ctx, txq, locked.ID)
		if err != nil {
			return err
		}
		switch {
		case state.Outstanding.Sign() <= 0:
			refusal = ptr(conflict(codeNoChargesOutstanding, cannotRegisterChargeTitle,
				"No charge is outstanding on this invoice: its sent reminders claimed none, or every charge is waived or paid."))
			return errRefused
		case amt.Cmp(state.Outstanding) > 0:
			refusal = ptr(conflict(codeChargePaymentExceedsOutstanding, cannotRegisterChargeTitle, fmt.Sprintf(
				"This charge payment is %s, and %s of the invoice's charges is outstanding. An overpayment is not registered.",
				amt.FloatString(2), state.Outstanding.FloatString(2))))
			refusal.ChargesOutstanding = ptr(floatFromRat(state.Outstanding, 2))
			return errRefused
		}
		_, err = insertChargePayment(ctx, txq, store.InsertChargePaymentParams{
			InvoiceID: locked.ID, PaidOn: payment.PaidOn, Amount: payment.Amount, Currency: locked.Currency, Source: sourceManual,
			Reference: payment.Reference, Note: payment.Note, RegisteredByUserID: callerID(ctx), RegisteredAt: s.deps.Clock(),
		})
		return err
	})
	if refusal != nil {
		return gen.PostInvoicesByIdChargePayments409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	if errors.Is(err, errDocumentGone) {
		return gen.PostInvoicesByIdChargePayments404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	resp, err := s.invoiceResponse(ctx, q, *inv, nil)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesByIdChargePayments200JSONResponse(resp), nil
}

// PostInvoicesByIdChargePaymentsByChargePaymentIdRemove Remove a charge payment
// (POST /api/v1/invoices/{id}/charge-payments/{chargePaymentId}/remove)
func (s *server) PostInvoicesByIdChargePaymentsByChargePaymentIdRemove(ctx context.Context, req gen.PostInvoicesByIdChargePaymentsByChargePaymentIdRemoveRequestObject) (gen.PostInvoicesByIdChargePaymentsByChargePaymentIdRemoveResponseObject, error) {
	reason, errs := removalReason(req.Body.Reason)
	if errs != nil {
		return gen.PostInvoicesByIdChargePaymentsByChargePaymentIdRemove400ApplicationProblemPlusJSONResponse(invalid(invalidRemovalTitle, errs)), nil
	}
	var refusal *gen.InvoicesConflictProblem
	var inv store.InvoicesInvoice
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		// The invoice first, the charge payment after the lock: two removals
		// of one queue here, and the second reads the first's removal.
		var err error
		inv, err = lockInvoice(ctx, txq, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return errDocumentGone
		}
		if err != nil {
			return fmt.Errorf("invoices: lock document %d: %w", req.Id, err)
		}
		payment, err := txq.GetChargePayment(ctx, store.GetChargePaymentParams{ID: req.ChargePaymentId, InvoiceID: inv.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			return errNoSuchChargePayment
		}
		if err != nil {
			return fmt.Errorf("invoices: read charge payment %d of %d: %w", req.ChargePaymentId, inv.ID, err)
		}
		if payment.RemovedAt != nil {
			refusal = ptr(conflict(codePaymentRemoved, cannotRemoveChargeTitle,
				"This charge payment is already removed. A removal is never undone; register the payment again."))
			return errRefused
		}
		n, err := txq.RemoveChargePayment(ctx, store.RemoveChargePaymentParams{
			RemovedAt: s.deps.Clock(), RemovedByUserID: callerID(ctx), RemovalReason: reason, ID: payment.ID, InvoiceID: inv.ID,
		})
		if err != nil {
			return fmt.Errorf("invoices: remove charge payment %d of %d: %w", payment.ID, inv.ID, err)
		}
		if n != 1 {
			return fmt.Errorf("invoices: remove charge payment %d of %d: %d rows, want 1", payment.ID, inv.ID, n)
		}
		return nil
	})
	if refusal != nil {
		return gen.PostInvoicesByIdChargePaymentsByChargePaymentIdRemove409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	if errors.Is(err, errDocumentGone) || errors.Is(err, errNoSuchChargePayment) {
		return gen.PostInvoicesByIdChargePaymentsByChargePaymentIdRemove404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	resp, err := s.invoiceResponse(ctx, store.New(s.deps.Pool), inv, nil)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesByIdChargePaymentsByChargePaymentIdRemove200JSONResponse(resp), nil
}

// parseWaive judges a waive request's body, every failure collected: one to
// maxWaiversPerRequest waivers of a known kind, a person's reason, a note
// within its column.
func parseWaive(body gen.InvoicesChargeWaiveRequest) ([]waiverRequest, string, map[string][]string) {
	var errs map[string][]string
	switch {
	case len(body.Waivers) == 0:
		errs = withFieldError(errs, "waivers", "Name at least one charge to waive")
	case len(body.Waivers) > maxWaiversPerRequest:
		errs = withFieldError(errs, "waivers", fmt.Sprintf("At most %d charges are waived at once", maxWaiversPerRequest))
	}
	ws := make([]waiverRequest, 0, len(body.Waivers))
	for _, w := range body.Waivers {
		if !w.Kind.Valid() {
			errs = withFieldError(errs, "waivers", "A charge is a fee, the compensation or interest")
			break
		}
		ws = append(ws, waiverRequest{reminderID: w.ReminderId, kind: string(w.Kind)})
	}
	if !body.Reason.Valid() {
		errs = withFieldError(errs, "reason", "A waiver's reason is objection_upheld, claimed_in_error or goodwill")
	}
	note := optionalText(body.Note)
	if msg := maxLength("The note", note, 500); msg != "" {
		errs = withFieldError(errs, "note", msg)
	}
	return ws, note, errs
}

// PostInvoicesByIdChargesWaive Waive reminder charges
// (POST /api/v1/invoices/{id}/charges/waive)
func (s *server) PostInvoicesByIdChargesWaive(ctx context.Context, req gen.PostInvoicesByIdChargesWaiveRequestObject) (gen.PostInvoicesByIdChargesWaiveResponseObject, error) {
	ws, note, errs := parseWaive(*req.Body)
	if errs != nil {
		return gen.PostInvoicesByIdChargesWaive400ApplicationProblemPlusJSONResponse(invalid(invalidWaiverTitle, errs)), nil
	}
	q := store.New(s.deps.Pool)
	inv, refusal, err := issuedInvoiceFor(ctx, q, req.Id, codeCreditNoteNoReminders, cannotWaiveTitle,
		"A credit note is never reminded of, so it has no charges to waive.",
		"A draft is not a sales document: it has no charges to waive.")
	if err != nil {
		return nil, err
	}
	if refusal != nil {
		return gen.PostInvoicesByIdChargesWaive409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	if inv == nil {
		return gen.PostInvoicesByIdChargesWaive404Response{}, nil
	}
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		locked, err := lockInvoice(ctx, txq, inv.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errDocumentGone
		}
		if err != nil {
			return fmt.Errorf("invoices: lock document %d: %w", inv.ID, err)
		}
		problem, err := waiveCharges(ctx, txq, locked.ID, ws, string(req.Body.Reason), note, callerID(ctx), s.deps.Clock())
		if err != nil {
			return err
		}
		if problem != nil {
			refusal = problem
			return errRefused
		}
		return nil
	})
	if refusal != nil {
		return gen.PostInvoicesByIdChargesWaive409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	if errors.Is(err, errDocumentGone) || errors.Is(err, errNoSuchLetter) {
		return gen.PostInvoicesByIdChargesWaive404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	resp, err := s.invoiceResponse(ctx, q, *inv, nil)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesByIdChargesWaive200JSONResponse(resp), nil
}
