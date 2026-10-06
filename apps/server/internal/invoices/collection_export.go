package invoices

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/reminderrules"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is GET /invoices/collection-export.csv (invoices payments and
// reminders design D11): the collection agency's file — the invoices handed
// off in a period, or the invoices named — one row per invoice, in the
// module's CSV form (csvfile.go). The principal stands apart from the
// charges, and what was waived is out of the claimed columns, in a column of
// its own. Every figure is the reminder engine's, read with the rows and the
// deliveries on one read-only snapshot of the pool at today — no lock — and
// the E-mail column is the customer directory's, read before that snapshot
// opens.

// collectionHeader is D11's header row, fixed and English, in its order.
var collectionHeader = []string{
	"Invoice number", "Issue date", "Due date", "Delivery", "Delivered", "KID", "Customer number", "Debtor",
	"Debtor type", "Org no", "Foreign id", "Address line 1", "Address line 2", "Postal code", "City", "Country",
	"E-mail", "Gross", "Credited", "Paid", "Principal open", "Payments", "Fees claimed", "Compensation claimed",
	"Charges waived", "Interest rate", "Interest from", "Interest to", "Interest accrued", "Charges paid", "Letters",
	"Notice sent", "Notice deadline", "Disputed", "Handed on", "Agency", "Agency reference",
}

// collectionMaxRows is the file's cap (D11): 500 invoices, as a run's.
const collectionMaxRows = 500

// collectionListSeparator joins a compact list's entries in one cell.
const collectionListSeparator = " | "

func (d csvDownload) VisitGetInvoicesCollectionExportCsvResponse(w http.ResponseWriter) error {
	return d.VisitGetInvoicesExportCsvResponse(w)
}

// badCollectionQuery is the file's 400: a bare problem.
func badCollectionQuery(detail string) gen.GetInvoicesCollectionExportCsv400ApplicationProblemPlusJSONResponse {
	return gen.GetInvoicesCollectionExportCsv400ApplicationProblemPlusJSONResponse(apicommon.Problem(invalidQueryTitle, detail))
}

// GetInvoicesCollectionExportCsv Export invoices for a collection agency as CSV
// (GET /api/v1/invoices/collection-export.csv)
//
// The order of its reads: the selection's rows on the pool, no transaction
// open, judged against the cap; the reminder addresses from the directory,
// still with no transaction open — the directory's own reads take a pool
// connection of their own, so a call while this request held one could
// starve a small pool; then one REPEATABLE READ, read-only transaction that
// reads the rows again with the engine's inputs and the deliveries, so the
// file is one snapshot. It takes no lock.
func (s *server) GetInvoicesCollectionExportCsv(ctx context.Context, req gen.GetInvoicesCollectionExportCsvRequestObject) (gen.GetInvoicesCollectionExportCsvResponseObject, error) {
	p := req.Params
	byDates := p.HandedFrom != nil || p.HandedTo != nil
	byIDs := p.InvoiceId != nil && len(*p.InvoiceId) > 0
	params := store.CollectionExportRowsParams{Ids: []int64{}, Limit: collectionMaxRows + 1}
	if byIDs {
		// Each invoice once, before the cap counts them.
		params.Ids = slices.Compact(slices.Sorted(slices.Values(*p.InvoiceId)))
	}
	switch {
	case byDates == byIDs:
		return badCollectionQuery("Choose the invoices one way: handedFrom and handedTo, or invoiceId."), nil
	case byDates && (p.HandedFrom == nil || p.HandedTo == nil):
		return badCollectionQuery("handedFrom and handedTo are given together."), nil
	case byDates && p.HandedFrom.After(p.HandedTo.Time):
		return badCollectionQuery("'handedFrom' must be on or before 'handedTo'."), nil
	case byDates:
		params.HandedFrom, params.HandedTo = pgDate(utcDay(p.HandedFrom.Time)), pgDate(utcDay(p.HandedTo.Time))
	case len(params.Ids) > collectionMaxRows:
		return badCollectionQuery(fmt.Sprintf("At most %d invoices are exported at once.", collectionMaxRows)), nil
	}
	today := businessDay(s.deps.Clock())

	selected, err := store.New(s.deps.Pool).CollectionExportRows(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the collection export: %w", err)
	}
	if refusal := judgeCollectionRows(selected, params.Ids); refusal != "" {
		return badCollectionQuery(refusal), nil
	}
	emails := s.reminderAddresses(ctx, selected)

	var refusal string
	var body []byte
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		rows, err := q.CollectionExportRows(ctx, params)
		if err != nil {
			return fmt.Errorf("invoices: read the collection export: %w", err)
		}
		// The selection again, on the snapshot: a hand-off made since the
		// first read can push it past the cap.
		if refusal = judgeCollectionRows(rows, params.Ids); refusal != "" {
			return nil
		}
		ids := make([]int64, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.ID)
			if _, read := emails[r.CustomerID]; !read {
				// A customer the first read did not see (a hand-off made, or
				// a merge, since): its address is not read under the snapshot.
				s.deps.Logger.WarnContext(ctx, "invoices: a customer's reminder address was not read for the collection export",
					"customerId", r.CustomerID)
				emails[r.CustomerID] = ""
			}
		}
		ins, err := s.readRuleInputs(ctx, q, ids, today)
		if err != nil {
			return err
		}
		deliveries, err := q.RuleDeliveries(ctx, ids)
		if err != nil {
			return fmt.Errorf("invoices: read the collection export's deliveries: %w", err)
		}
		first, err := firstDeliveries(deliveries)
		if err != nil {
			return err
		}
		var b bytes.Buffer
		b.WriteString(csvByteOrderMark)
		header := make([]csvValue, len(collectionHeader))
		for i, name := range collectionHeader {
			header[i] = csvValue{text: name}
		}
		writeCSVRow(&b, header)
		for _, r := range rows {
			in, ok := ins[r.ID]
			if !ok {
				return fmt.Errorf("invoices: document %d is not an issued invoice the rules judge", r.ID)
			}
			writeCSVRow(&b, collectionCells(r, in, first[r.ID], emails[r.CustomerID], today))
		}
		body = b.Bytes()
		return nil
	})
	switch {
	case err != nil:
		return nil, err
	case refusal != "":
		return badCollectionQuery(refusal), nil
	}
	return csvDownload{body: body, fileName: fmt.Sprintf("invoices-collection-%s.csv", today.Format(time.DateOnly))}, nil
}

// judgeCollectionRows is the selection's 400, "" when it passes: more rows
// than the file holds, or an id among ids that is not an issued invoice.
func judgeCollectionRows(rows []store.CollectionExportRowsRow, ids []int64) string {
	if len(rows) > collectionMaxRows {
		return fmt.Sprintf("This export would hold more than %d invoices; narrow the period.", collectionMaxRows)
	}
	var missing []string
	for _, id := range ids {
		if !slices.ContainsFunc(rows, func(r store.CollectionExportRowsRow) bool { return r.ID == id }) {
			missing = append(missing, fmt.Sprint(id))
		}
	}
	if missing != nil {
		return "Not an issued invoice: " + strings.Join(missing, ", ")
	}
	return ""
}

// reminderAddresses is each customer's reminder address among rows, one
// directory read per customer; a failure leaves it empty, logged at warn.
func (s *server) reminderAddresses(ctx context.Context, rows []store.CollectionExportRowsRow) map[int32]string {
	emails := map[int32]string{}
	for _, r := range rows {
		if _, done := emails[r.CustomerID]; done {
			continue
		}
		emails[r.CustomerID] = ""
		profile, err := s.customerProfile(ctx, r.CustomerID)
		if err != nil {
			s.deps.Logger.WarnContext(ctx, "invoices: a customer's reminder address could not be read for the collection export",
				"customerId", r.CustomerID, "error", err.Error())
			continue
		}
		if profile != nil {
			emails[r.CustomerID] = strings.TrimSpace(profile.ReminderEmail)
		}
	}
	return emails
}

// firstDelivery is an invoice's first recorded delivery: its kind as a
// person reads it and its day.
type firstDelivery struct {
	kind string
	day  time.Time
}

// firstDeliveries is each invoice's first live delivery among rows — the
// engine's own deliveries (RuleDeliveries), a manual record named by its
// own kind, handed_over or posted — the earliest day first and, on one day,
// the kind in name order.
func firstDeliveries(rows []store.RuleDeliveriesRow) (map[int64]firstDelivery, error) {
	out := map[int64]firstDelivery{}
	for _, r := range rows {
		day, err := deliveryDay(r.InvoiceID, r.Kind, r.At, r.DeliveredOn)
		if err != nil {
			return nil, err
		}
		kind := r.Kind
		if kind == deliveryKindManual {
			kind = r.ManualKind
		}
		if f, ok := out[r.InvoiceID]; !ok || day.Before(f.day) || (day.Equal(f.day) && kind < f.kind) {
			out[r.InvoiceID] = firstDelivery{kind: kind, day: day}
		}
	}
	return out, nil
}

// collectionCells is one invoice's cells in the header's order (D11). The
// text columns are guarded; the number, the dates and the amounts never are.
func collectionCells(r store.CollectionExportRowsRow, in reminderrules.Input, delivered firstDelivery, email string, today time.Time) []csvValue {
	text := func(s string) csvValue { return csvValue{text: s, guard: true} }
	plain := func(s string) csvValue { return csvValue{text: s} }
	amount := func(v *big.Rat) csvValue { return plain(csvAmount(v, false)) }
	day := func(d *time.Time) csvValue {
		if d == nil {
			return plain("")
		}
		return plain(d.Format(time.DateOnly))
	}

	credited, paid := new(big.Rat), new(big.Rat)
	for _, c := range in.Credits {
		credited.Add(credited, c.Gross)
	}
	payments := make([]string, 0, len(in.Payments))
	for _, p := range in.Payments {
		paid.Add(paid, p.Amount)
		payments = append(payments, p.PaidOn.Format(time.DateOnly)+" "+csvAmount(p.Amount, false))
	}

	// The charges apart: each sent letter's fee and compensation not
	// waived, every waiver's total, the live charge payments.
	fees, compensation := new(big.Rat), new(big.Rat)
	var letters []string
	var notice *reminderrules.Letter
	for i, l := range in.Letters {
		if l.Status != reminderrules.StatusSent || l.SentOn == nil {
			continue
		}
		letters = append(letters, letterCell(l, in.Waivers))
		if l.Level == reminderrules.LevelNotice && (notice == nil || l.Sequence > notice.Sequence) {
			notice = &in.Letters[i]
		}
		if l.Fee != nil && !waived(in.Waivers, l.ID, reminderrules.WaiverFee) {
			fees.Add(fees, l.Fee)
		}
		if l.Compensation != nil && !waived(in.Waivers, l.ID, reminderrules.WaiverCompensation) {
			compensation.Add(compensation, l.Compensation)
		}
	}
	state := reminderrules.Charges(in.Letters, in.Waivers, in.ChargePayments)

	// The late interest to today as a letter today would claim it, less what
	// was waived of it; empty where none applies, none has accrued yet, or
	// a rate it needs is missing.
	rate, from, accrued := plain(""), plain(""), plain("")
	total, start, segs, outdated := reminderrules.Interest(in)
	switch {
	case outdated != nil:
	case start == nil:
		accrued = amount(new(big.Rat))
	default:
		net := new(big.Rat).Set(total)
		for _, w := range in.Waivers {
			if w.Kind == reminderrules.WaiverInterest {
				net.Sub(net, w.Amount)
			}
		}
		if net.Sign() < 0 {
			net.SetInt64(0)
		}
		accrued, from = amount(net), day(start)
		if len(segs) > 0 {
			rate = amount(segs[len(segs)-1].Rate)
		}
	}

	deliveredCell := ""
	if delivered.kind != "" {
		deliveredCell = delivered.kind + " " + delivered.day.Format(time.DateOnly)
	}
	noticeSent, noticeDeadline := plain(""), plain("")
	if notice != nil {
		noticeSent, noticeDeadline = day(notice.SentOn), day(notice.Deadline)
	}
	disputed := "no"
	if r.Disputed {
		disputed = "yes"
	}
	return []csvValue{
		plain(csvInt(r.Number)),
		plain(csvDate(r.IssueDate)),
		plain(csvDate(r.DueDate)),
		text(csvDelivery(store.ExportRowsRow{DeliveryDate: r.DeliveryDate, DeliveryFrom: r.DeliveryFrom, DeliveryTo: r.DeliveryTo})),
		text(deliveredCell),
		// The module's own digits and at most a MOD11 '-': guarded as the
		// accountant's file guards it, as defence in depth.
		text(csvText(r.Kid)),
		text(csvInt(r.BuyerCustomerNumber)),
		text(csvText(r.BuyerName)),
		text(csvText(r.BuyerType)),
		text(csvText(r.BuyerOrganisationNumber)),
		text(csvText(r.BuyerForeignID)),
		text(csvText(r.BuyerAddressLine1)),
		text(csvText(r.BuyerAddressLine2)),
		text(csvText(r.BuyerPostalCode)),
		text(csvText(r.BuyerCity)),
		text(csvText(r.BuyerCountry)),
		text(email),
		amount(in.Invoice.Gross),
		amount(credited),
		amount(paid),
		amount(principalOpenOf(in)),
		text(strings.Join(payments, collectionListSeparator)),
		amount(fees),
		amount(compensation),
		amount(state.Waived),
		rate, from, plain(today.Format(time.DateOnly)), accrued,
		amount(state.Paid),
		text(strings.Join(letters, collectionListSeparator)),
		noticeSent, noticeDeadline,
		text(disputed),
		plain(csvDate(r.HandedOn)),
		text(csvText(r.Agency)),
		text(csvText(r.AgencyReference)),
	}
}

// letterCell is one sent letter in the Letters column: its day, level and
// deadline, and the fee or compensation it claimed — "(waived)" when a
// waiver released it.
func letterCell(l reminderrules.Letter, ws []reminderrules.Waiver) string {
	cell := l.SentOn.Format(time.DateOnly) + " " + string(l.Level)
	if l.Deadline != nil {
		cell += " deadline " + l.Deadline.Format(time.DateOnly)
	}
	for _, c := range []struct {
		kind   string
		amount *big.Rat
	}{{reminderrules.WaiverFee, l.Fee}, {reminderrules.WaiverCompensation, l.Compensation}} {
		if c.amount == nil || c.amount.Sign() <= 0 {
			continue
		}
		cell += " " + c.kind + " " + csvAmount(c.amount, false)
		if waived(ws, l.ID, c.kind) {
			cell += " (waived)"
		}
	}
	return cell
}

// waived is whether a waiver of kind released letter id's charge.
func waived(ws []reminderrules.Waiver, id int64, kind string) bool {
	return slices.ContainsFunc(ws, func(w reminderrules.Waiver) bool { return w.ReminderID == id && w.Kind == kind })
}
