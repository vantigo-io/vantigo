package invoices

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/reminderrules"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the delivery fact (invoices payments and reminders design
// D8): an invoice not validly delivered does not fall due, and a varsel or a
// fee on it is invalid (FinKN 2017-492). A charge — a fee, the compensation
// or interest — needs a recorded delivery on or before the due date: an
// e-mail (invoices.deliveries), an EHF transmission delivered, or a manual
// delivery recorded here, the invoice handed over or posted. A manual
// delivery is recorded and removed under the invoice's lock alone (D18), and
// is never deleted: its removal is a reason, once, the payments' shape.

// The codes and titles of the manual deliveries' refusals.
const (
	codeDeliveryRemoved  = "delivery_removed"
	codeDeliveryReliedOn = "delivery_relied_on"

	cannotRecordDeliveryTitle = "The delivery cannot be recorded"
	cannotRemoveDeliveryTitle = "The delivery cannot be removed"
	invalidDeliveryTitle      = "Invalid delivery"
)

// errNoSuchDelivery is a removal naming a manual delivery that is not the
// invoice's.
var errNoSuchDelivery = errors.New("invoices: no such manual delivery on the invoice")

// The kinds a delivery the engine reads comes from (RuleDeliveries).
const (
	deliveryKindManual = "manual"
	deliveryKindEmail  = "email"
	deliveryKindEhf    = "ehf"
)

// deliveriesOf is the engine's Deliveries (reminderrules.Input): the day of
// every live delivery of an invoice, the earliest first — an e-mail by its
// sent_at's Oslo day, a delivered EHF transmission by its delivered_at's, a
// manual delivery not removed by its delivered_on (plan reading 34) — each a
// UTC midnight, as businessDay answers. read with q, which holds the
// invoice's lock, or a plain read. It is the rule-input loader's
// RuleDeliveries for one invoice: one definition of a qualifying delivery.
func deliveriesOf(ctx context.Context, q *store.Queries, invoiceID int64) ([]time.Time, error) {
	rows, err := q.RuleDeliveries(ctx, []int64{invoiceID})
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d's deliveries: %w", invoiceID, err)
	}
	days := make([]time.Time, 0, len(rows))
	for _, r := range rows {
		day, err := deliveryDay(invoiceID, r.Kind, r.At, r.DeliveredOn)
		if err != nil {
			return nil, err
		}
		days = append(days, day)
	}
	slices.SortFunc(days, time.Time.Compare)
	return days, nil
}

// deliveryDay is one qualifying delivery's day (a RuleDeliveries row): a
// manual delivery's delivered_on, an e-mail's or a delivered EHF
// transmission's at by its Oslo day — a UTC midnight either way.
func deliveryDay(invoiceID int64, kind string, at *time.Time, deliveredOn pgtype.Date) (time.Time, error) {
	switch {
	case kind == deliveryKindManual && deliveredOn.Valid:
		return utcDay(deliveredOn.Time), nil
	case (kind == deliveryKindEmail || kind == deliveryKindEhf) && at != nil:
		return businessDay(*at), nil
	default:
		return time.Time{}, fmt.Errorf("invoices: document %d has a %s delivery without its day", invoiceID, kind)
	}
}

// reliedOn is whether removing manual delivery d would take away the
// delivery a letter's charge stands on (plan reading 37): a letter of the
// invoice that carries its facts — sent, printed, or being sent; not
// withdrawn, not failed — carries a fee or the compensation not waived, or
// interest beyond what was waived, and no other delivery on or before the
// due date would remain. A printed or in-flight letter counts as a sent one does: its
// charges are on paper or on their way. Read with txq, which holds the
// invoice's lock.
func reliedOn(ctx context.Context, txq *store.Queries, inv store.InvoicesInvoice, d store.InvoicesManualDelivery) (bool, error) {
	due := utcDay(inv.DueDate.Time)
	if utcDay(d.DeliveredOn.Time).After(due) {
		// A delivery after the due date counts as none: nothing stands on it.
		return false, nil
	}
	stored, err := txq.ClaimingLettersOf(ctx, inv.ID)
	if err != nil {
		return false, fmt.Errorf("invoices: read document %d's letters: %w", inv.ID, err)
	}
	letters := make([]reminderrules.Letter, 0, len(stored))
	for _, r := range stored {
		l, err := letterOf(r)
		if err != nil {
			return false, err
		}
		letters = append(letters, l)
	}
	rows, err := txq.WaiversOf(ctx, inv.ID)
	if err != nil {
		return false, fmt.Errorf("invoices: read document %d's waivers: %w", inv.ID, err)
	}
	waived := map[string]bool{}
	interestWaived := new(big.Rat)
	for _, w := range rows {
		waived[fmt.Sprintf("%d %s", w.ReminderID, w.Kind)] = true
		if w.Kind == reminderrules.WaiverInterest {
			amount, err := ratFromNumeric(w.Amount)
			if err != nil {
				return false, err
			}
			interestWaived.Add(interestWaived, amount)
		}
	}
	charged := false
	for _, l := range letters {
		switch {
		case l.Fee != nil && l.Fee.Sign() > 0 && !waived[fmt.Sprintf("%d %s", l.ID, reminderrules.WaiverFee)],
			l.Compensation != nil && l.Compensation.Sign() > 0 && !waived[fmt.Sprintf("%d %s", l.ID, reminderrules.WaiverCompensation)],
			l.Interest != nil && l.Interest.Cmp(interestWaived) > 0:
			charged = true
		}
	}
	if !charged {
		return false, nil
	}
	days, err := deliveriesOf(ctx, txq, inv.ID)
	if err != nil {
		return false, err
	}
	before := 0
	for _, day := range days {
		if !day.After(due) {
			before++
		}
	}
	// d is live (the caller judged it), on or before the due date and among
	// days: removing it leaves one fewer.
	return before <= 1, nil
}

// withManualDeliveries answers an issued invoice's manual deliveries on resp,
// removed ones included with their removal, the earliest first.
func withManualDeliveries(ctx context.Context, q *store.Queries, invoiceID int64, resp *gen.InvoicesInvoiceResponse) error {
	rows, err := q.ManualDeliveriesOf(ctx, invoiceID)
	if err != nil {
		return fmt.Errorf("invoices: read document %d's manual deliveries: %w", invoiceID, err)
	}
	out := make([]gen.InvoicesManualDelivery, 0, len(rows))
	for _, d := range rows {
		out = append(out, gen.InvoicesManualDelivery{
			Id: d.ID, Kind: gen.InvoicesManualDeliveryKind(d.Kind), DeliveredOn: wireDate(d.DeliveredOn.Time), Note: d.Note,
			RecordedAt: d.RecordedAt, RecordedByUserId: d.RecordedByUserID,
			RemovedAt: d.RemovedAt, RemovedByUserId: d.RemovedByUserID, RemovalReason: d.RemovalReason,
		})
	}
	resp.ManualDeliveries = &out
	return nil
}

// parseManualDelivery judges a manual delivery's body, every failure
// collected: a known kind, deliveredOn from the invoice's issue date to today
// (the Oslo business day), a note within its column.
func parseManualDelivery(body gen.InvoicesManualDeliveryRequest, issueDate, today time.Time) (store.InsertManualDeliveryParams, map[string][]string) {
	var errs map[string][]string
	if !body.Kind.Valid() {
		errs = withFieldError(errs, "kind", "A delivery is handed_over or posted")
	}
	on := utcDay(body.DeliveredOn.Time)
	switch {
	case body.DeliveredOn.IsZero():
		errs = withFieldError(errs, "deliveredOn", "A delivery needs the day it was made")
	case on.Before(issueDate):
		errs = withFieldError(errs, "deliveredOn", "An invoice is delivered on or after its issue date, "+issueDate.Format(time.DateOnly))
	case on.After(today):
		errs = withFieldError(errs, "deliveredOn", "A delivery is recorded today at the latest, "+today.Format(time.DateOnly))
	}
	note := optionalText(body.Note)
	if msg := maxLength("The note", note, 500); msg != "" {
		errs = withFieldError(errs, "note", msg)
	}
	return store.InsertManualDeliveryParams{Kind: string(body.Kind), DeliveredOn: pgDate(on), Note: note}, errs
}

// PostInvoicesByIdManualDeliveries Record a manual delivery
// (POST /api/v1/invoices/{id}/manual-deliveries)
func (s *server) PostInvoicesByIdManualDeliveries(ctx context.Context, req gen.PostInvoicesByIdManualDeliveriesRequestObject) (gen.PostInvoicesByIdManualDeliveriesResponseObject, error) {
	q := store.New(s.deps.Pool)
	inv, refusal, err := issuedInvoiceFor(ctx, q, req.Id, codeCreditNoteNoReminders, cannotRecordDeliveryTitle,
		"A credit note is never reminded of, so its delivery is not recorded here.",
		"A draft is not a sales document: issue it before recording its delivery.")
	if err != nil {
		return nil, err
	}
	if refusal != nil {
		return gen.PostInvoicesByIdManualDeliveries409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	if inv == nil {
		return gen.PostInvoicesByIdManualDeliveries404Response{}, nil
	}
	delivery, errs := parseManualDelivery(*req.Body, inv.IssueDate.Time, businessDay(s.deps.Clock()))
	if errs != nil {
		return gen.PostInvoicesByIdManualDeliveries400ApplicationProblemPlusJSONResponse(invalid(invalidDeliveryTitle, errs)), nil
	}
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		locked, err := lockInvoice(ctx, txq, inv.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errDocumentGone
		}
		if err != nil {
			return fmt.Errorf("invoices: lock document %d: %w", inv.ID, err)
		}
		delivery.InvoiceID, delivery.RecordedByUserID, delivery.RecordedAt = locked.ID, callerID(ctx), s.deps.Clock()
		if _, err := txq.InsertManualDelivery(ctx, delivery); err != nil {
			return fmt.Errorf("invoices: record a delivery of %d: %w", locked.ID, err)
		}
		return nil
	})
	if errors.Is(err, errDocumentGone) {
		return gen.PostInvoicesByIdManualDeliveries404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	resp, err := s.invoiceResponse(ctx, q, *inv, nil)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesByIdManualDeliveries200JSONResponse(resp), nil
}

// PostInvoicesByIdManualDeliveriesByDeliveryIdRemove Remove a manual delivery
// (POST /api/v1/invoices/{id}/manual-deliveries/{deliveryId}/remove)
func (s *server) PostInvoicesByIdManualDeliveriesByDeliveryIdRemove(ctx context.Context, req gen.PostInvoicesByIdManualDeliveriesByDeliveryIdRemoveRequestObject) (gen.PostInvoicesByIdManualDeliveriesByDeliveryIdRemoveResponseObject, error) {
	reason, errs := removalReason(req.Body.Reason)
	if errs != nil {
		return gen.PostInvoicesByIdManualDeliveriesByDeliveryIdRemove400ApplicationProblemPlusJSONResponse(invalid(invalidRemovalTitle, errs)), nil
	}
	var refusal *gen.InvoicesConflictProblem
	var inv store.InvoicesInvoice
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		// The invoice first, then the record, the letters, the waivers and
		// the other deliveries, all read after the lock.
		var err error
		inv, err = lockInvoice(ctx, txq, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return errDocumentGone
		}
		if err != nil {
			return fmt.Errorf("invoices: lock document %d: %w", req.Id, err)
		}
		d, err := txq.GetManualDelivery(ctx, store.GetManualDeliveryParams{ID: req.DeliveryId, InvoiceID: inv.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			return errNoSuchDelivery
		}
		if err != nil {
			return fmt.Errorf("invoices: read delivery %d of %d: %w", req.DeliveryId, inv.ID, err)
		}
		if d.RemovedAt != nil {
			refusal = ptr(conflict(codeDeliveryRemoved, cannotRemoveDeliveryTitle,
				"This delivery record is already removed. A removal is never undone; record the delivery again."))
			return errRefused
		}
		relied, err := reliedOn(ctx, txq, inv, d)
		if err != nil {
			return err
		}
		if relied {
			refusal = ptr(conflict(codeDeliveryReliedOn, cannotRemoveDeliveryTitle,
				"A reminder claims a charge that stands on this delivery, and no other delivery on or before the due date "+
					"remains. If the record is a mistake, waive those charges as claimed in error first, then remove it."))
			return errRefused
		}
		n, err := txq.RemoveManualDelivery(ctx, store.RemoveManualDeliveryParams{
			RemovedAt: s.deps.Clock(), RemovedByUserID: callerID(ctx), RemovalReason: reason, ID: d.ID, InvoiceID: inv.ID,
		})
		if err != nil {
			return fmt.Errorf("invoices: remove delivery %d of %d: %w", d.ID, inv.ID, err)
		}
		if n != 1 {
			return fmt.Errorf("invoices: remove delivery %d of %d: %d rows, want 1", d.ID, inv.ID, n)
		}
		return nil
	})
	if refusal != nil {
		return gen.PostInvoicesByIdManualDeliveriesByDeliveryIdRemove409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	if errors.Is(err, errDocumentGone) || errors.Is(err, errNoSuchDelivery) {
		return gen.PostInvoicesByIdManualDeliveriesByDeliveryIdRemove404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	resp, err := s.invoiceResponse(ctx, store.New(s.deps.Pool), inv, nil)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesByIdManualDeliveriesByDeliveryIdRemove200JSONResponse(resp), nil
}
