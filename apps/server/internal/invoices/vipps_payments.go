package invoices

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the day's Vipps payments and their reconciliation
// (invoices payments and reminders design D17 rule 4, M15): one Oslo day's
// captures — on site and by pay link, since Vantigo cannot know where the
// customer stood — as JSON and as a file in the module's CSV form
// (csvfile.go), and an immutable record that the day was reconciled against
// the Vipps settlement, bokføringsforskriften § 5-4-5's daily count, which
// arguably reaches kontantfaktura sales (R4 §9.6 item 4) and is built in.
// Nothing here takes a lock: the reads are plain, and the record is one
// insert that sums the day as it stands.

const (
	invalidReconciliationTitle = "Invalid reconciliation"
	cannotReconcileTitle       = "The day cannot be reconciled"
	// maxReconciliationNoteLength is the note's column width.
	maxReconciliationNoteLength = 500
)

// vippsDayHeader is the file's header row, fixed and English, in its order.
var vippsDayHeader = []string{"Date", "Time", "Invoice number", "Buyer", "Flow", "Amount", "Reference"}

// osloDay is the instants a calendar day spans in Oslo: from its midnight
// to the next, so a day of a clock change is 23 or 25 hours.
func osloDay(day time.Time) (start, end time.Time) {
	day = utcDay(day)
	start = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, oslo)
	return start, time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, oslo)
}

// vippsDayRows reads one Oslo day's captures.
func vippsDayRows(ctx context.Context, q *store.Queries, day time.Time) ([]store.VippsPaymentsBetweenRow, error) {
	start, end := osloDay(day)
	rows, err := q.VippsPaymentsBetween(ctx, store.VippsPaymentsBetweenParams{DayStart: start, DayEnd: end})
	if err != nil {
		return nil, fmt.Errorf("invoices: read the Vipps payments of %s: %w", day.Format(time.DateOnly), err)
	}
	return rows, nil
}

// reconciliationWire is a record on the wire.
func reconciliationWire(r store.InvoicesVippsDayReconciliation) (gen.InvoicesVippsDayReconciliation, error) {
	out := gen.InvoicesVippsDayReconciliation{
		Day: wireDate(utcDay(r.Day.Time)), Payments: r.Payments, ReconciledBy: r.ReconciledByUserID,
		ReconciledAt: r.ReconciledAt, Note: r.Note,
	}
	var err error
	if out.Total, err = floatFromNumeric(r.Total); err != nil {
		return out, err
	}
	if out.OnSiteTotal, err = floatFromNumeric(r.OnSiteTotal); err != nil {
		return out, err
	}
	if out.PayLinkTotal, err = floatFromNumeric(r.PayLinkTotal); err != nil {
		return out, err
	}
	return out, nil
}

// GetInvoicesVippsPayments List a day's Vipps payments
// (GET /api/v1/invoices/vipps-payments)
func (s *server) GetInvoicesVippsPayments(ctx context.Context, req gen.GetInvoicesVippsPaymentsRequestObject) (gen.GetInvoicesVippsPaymentsResponseObject, error) {
	day := utcDay(req.Params.Date.Time)
	q := store.New(s.deps.Pool)
	rows, err := vippsDayRows(ctx, q, day)
	if err != nil {
		return nil, err
	}
	out := gen.InvoicesVippsPaymentsDay{Date: wireDate(day), Payments: make([]gen.InvoicesVippsPayment, 0, len(rows))}
	total, onSite, payLink := new(big.Rat), new(big.Rat), new(big.Rat)
	for _, r := range rows {
		amount, err := ratFromNumeric(r.CapturedAmount)
		if err != nil {
			return nil, err
		}
		total.Add(total, amount)
		if r.Flow == flowOnSite {
			onSite.Add(onSite, amount)
		} else {
			payLink.Add(payLink, amount)
		}
		out.Payments = append(out.Payments, gen.InvoicesVippsPayment{
			AttemptId: r.ID, Reference: r.Reference, InvoiceId: r.InvoiceID, InvoiceNumber: deref64(r.InvoiceNumber),
			BuyerName: deref(r.BuyerName), Flow: gen.InvoicesVippsPaymentFlow(r.Flow), Amount: floatFromRat(amount, 2),
			CapturedAt: *r.CapturedAt,
		})
	}
	out.Count = int32(len(rows))
	out.Total, out.OnSiteTotal, out.PayLinkTotal = floatFromRat(total, 2), floatFromRat(onSite, 2), floatFromRat(payLink, 2)
	rec, err := q.VippsDayReconciliation(ctx, pgDate(day))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return nil, fmt.Errorf("invoices: read the reconciliation of %s: %w", day.Format(time.DateOnly), err)
	default:
		w, err := reconciliationWire(rec)
		if err != nil {
			return nil, err
		}
		out.Reconciliation = &w
	}
	return gen.GetInvoicesVippsPayments200JSONResponse(out), nil
}

func (d csvDownload) VisitGetInvoicesVippsPaymentsCsvResponse(w http.ResponseWriter) error {
	return d.VisitGetInvoicesExportCsvResponse(w)
}

// GetInvoicesVippsPaymentsCsv Export a day's Vipps payments as CSV
// (GET /api/v1/invoices/vipps-payments.csv)
//
// The same rows as the JSON list, in the module's CSV form: the date and
// time of the capture in Oslo, the buyer guarded as typed text.
func (s *server) GetInvoicesVippsPaymentsCsv(ctx context.Context, req gen.GetInvoicesVippsPaymentsCsvRequestObject) (gen.GetInvoicesVippsPaymentsCsvResponseObject, error) {
	day := utcDay(req.Params.Date.Time)
	rows, err := vippsDayRows(ctx, store.New(s.deps.Pool), day)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString(csvByteOrderMark)
	header := make([]csvValue, len(vippsDayHeader))
	for i, name := range vippsDayHeader {
		header[i] = csvValue{text: name}
	}
	writeCSVRow(&b, header)
	for _, r := range rows {
		amount, err := csvDecimal(r.CapturedAmount, false)
		if err != nil {
			return nil, err
		}
		at := r.CapturedAt.In(oslo)
		writeCSVRow(&b, []csvValue{
			{text: at.Format(time.DateOnly)},
			{text: at.Format(time.TimeOnly)},
			{text: csvInt(r.InvoiceNumber)},
			{text: csvText(r.BuyerName), guard: true},
			{text: r.Flow},
			{text: amount},
			{text: r.Reference, guard: true},
		})
	}
	return csvDownload{body: b.Bytes(), fileName: fmt.Sprintf("vipps-payments-%s.csv", day.Format(time.DateOnly))}, nil
}

// PostInvoicesVippsPaymentsReconciliations Reconcile a day's Vipps payments
// (POST /api/v1/invoices/vipps-payments/reconciliations)
//
// One insert sums the day as it stands and records it with the caller, the
// time and the note, once; no lock. The day must be over in Oslo.
func (s *server) PostInvoicesVippsPaymentsReconciliations(ctx context.Context, req gen.PostInvoicesVippsPaymentsReconciliationsRequestObject) (gen.PostInvoicesVippsPaymentsReconciliationsResponseObject, error) {
	var errs map[string][]string
	if req.Body.Day.IsZero() {
		errs = withFieldError(errs, "day", "day is the Oslo day reconciled, YYYY-MM-DD")
	}
	note := ""
	if req.Body.Note != nil {
		note = strings.TrimSpace(*req.Body.Note)
	}
	if utf8.RuneCountInString(note) > maxReconciliationNoteLength {
		errs = withFieldError(errs, "note", fmt.Sprintf("The note is at most %d characters", maxReconciliationNoteLength))
	}
	if errs != nil {
		return gen.PostInvoicesVippsPaymentsReconciliations400ApplicationProblemPlusJSONResponse(invalid(invalidReconciliationTitle, errs)), nil
	}
	now := s.deps.Clock() // the request's one clock read
	day := utcDay(req.Body.Day.Time)
	if !day.Before(businessDay(now)) {
		return gen.PostInvoicesVippsPaymentsReconciliations409ApplicationProblemPlusJSONResponse(conflict(codeDayNotOver, cannotReconcileTitle,
			fmt.Sprintf("%s is not over yet: a day is reconciled from the day after.", day.Format(time.DateOnly)))), nil
	}
	start, end := osloDay(day)
	rec, err := store.New(s.deps.Pool).InsertVippsDayReconciliation(ctx, store.InsertVippsDayReconciliationParams{
		Day: pgDate(day), ReconciledByUserID: callerID(ctx), Now: now, Note: note, DayStart: start, DayEnd: end,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return gen.PostInvoicesVippsPaymentsReconciliations409ApplicationProblemPlusJSONResponse(conflict(codeDayAlreadyReconciled, cannotReconcileTitle,
			fmt.Sprintf("%s was reconciled before; its record never changes.", day.Format(time.DateOnly)))), nil
	case err != nil:
		return nil, fmt.Errorf("invoices: reconcile the Vipps payments of %s: %w", day.Format(time.DateOnly), err)
	}
	w, err := reconciliationWire(rec)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesVippsPaymentsReconciliations201JSONResponse(w), nil
}
