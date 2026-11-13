package orders

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// This file is the analysis views (O35), under orders:analysis-view: the
// quote hit rate, the order intake and the backlog, each read from Orders'
// own tables and summed here, exactly, per currency and net of VAT — no
// warehouse, and no sum across currencies. The intake by category is the one
// view that reads beyond them: the catalog's category of each of the period's
// variants, read after Orders' own rows. Each answers JSON, or with
// format=csv the same table as a file.

// maxAnalysisDays is the longest period a view sums.
const maxAnalysisDays = 3660

// The backlog's age buckets: bokføringsforskriften § 5-2-2 asks an invoice
// within a month of delivery (R §8.1).
const (
	backlogRecentDays = 7
	backlogMonthDays  = 30
)

// analysisPeriod is a view's period: its days and the instants they span in
// the business time zone, [fromAt, toAt).
type analysisPeriod struct {
	from, to     openapi_types.Date
	fromAt, toAt time.Time
}

// readPeriod judges a period: to on or after from, at most maxAnalysisDays
// long. The days are read in zone.
func readPeriod(from, to openapi_types.Date, zone *time.Location) (analysisPeriod, error) {
	start := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, zone)
	end := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, zone).AddDate(0, 0, 1)
	var f fieldErrors
	switch {
	case !end.After(start):
		f.add("to", "The period ends on or after the day it begins")
	case end.Sub(start) > maxAnalysisDays*24*time.Hour:
		f.add("to", fmt.Sprintf("A period is at most %d days", maxAnalysisDays))
	}
	if f.failed() {
		return analysisPeriod{}, refuseInvalid(invalidAnalysisTitle, f)
	}
	return analysisPeriod{from: from, to: to, fromAt: start, toAt: end}, nil
}

// tally is a count and an exact net amount.
type tally struct {
	count  int32
	amount big.Rat
}

func (t *tally) add(amount *big.Rat) {
	t.count++
	t.amount.Add(&t.amount, amount)
}

func (t *tally) out() gen.OrdersCountAmount {
	return gen.OrdersCountAmount{Count: t.count, Amount: wire(&t.amount, amountPlaces)}
}

// hitRateTally is one row of the hit rate: the quotes sent and how they stand.
type hitRateTally struct {
	salesperson                                        *gen.OrdersPerson
	currency                                           string
	sent, accepted, declined, expired, cancelled, open tally
}

// add counts one quote: an accepted, declined, expired or cancelled quote as
// such, any other — still sent, or revised back to a draft — as open.
func (h *hitRateTally) add(status string, amount *big.Rat) {
	h.sent.add(amount)
	switch status {
	case quoteAccepted:
		h.accepted.add(amount)
	case quoteDeclined:
		h.declined.add(amount)
	case quoteExpired:
		h.expired.add(amount)
	case quoteCancelled:
		h.cancelled.add(amount)
	default:
		h.open.add(amount)
	}
}

func (h *hitRateTally) out() gen.OrdersHitRateRow {
	return gen.OrdersHitRateRow{
		Salesperson: h.salesperson, Currency: h.currency, Sent: h.sent.out(), Accepted: h.accepted.out(),
		Declined: h.declined.out(), Expired: h.expired.out(), Cancelled: h.cancelled.out(), Open: h.open.out(),
	}
}

// hitRate sums the quotes last sent in a period, per salesperson and currency
// (by the salesperson's name) and per currency.
func hitRate(rows []store.QuotesSentBetweenRow, p analysisPeriod) gen.OrdersHitRate {
	byPerson := map[string]*hitRateTally{}
	byCurrency := map[string]*hitRateTally{}
	for _, r := range rows {
		amount := ratOrZero(r.NetTotal)
		who := "\x00" + deref(r.SalespersonDisplay)
		if r.SalespersonID != nil {
			who = r.SalespersonID.String()
		}
		key := who + "\x00" + r.Currency
		t, ok := byPerson[key]
		if !ok {
			t = &hitRateTally{salesperson: personOut(r.SalespersonID, r.SalespersonDisplay), currency: r.Currency}
			byPerson[key] = t
		}
		t.add(r.Status, amount)
		total, ok := byCurrency[r.Currency]
		if !ok {
			total = &hitRateTally{currency: r.Currency}
			byCurrency[r.Currency] = total
		}
		total.add(r.Status, amount)
	}
	out := gen.OrdersHitRate{From: p.from, To: p.to, Rows: []gen.OrdersHitRateRow{}, Totals: []gen.OrdersHitRateRow{}}
	for _, t := range byPerson {
		out.Rows = append(out.Rows, t.out())
	}
	slices.SortFunc(out.Rows, func(a, b gen.OrdersHitRateRow) int {
		return cmp.Or(strings.Compare(strings.ToLower(personName(a.Salesperson)), strings.ToLower(personName(b.Salesperson))),
			strings.Compare(a.Currency, b.Currency))
	})
	for _, t := range byCurrency {
		out.Totals = append(out.Totals, t.out())
	}
	slices.SortFunc(out.Totals, func(a, b gen.OrdersHitRateRow) int { return strings.Compare(a.Currency, b.Currency) })
	return out
}

// personName is a person's display name, "" for none.
func personName(p *gen.OrdersPerson) string {
	if p == nil {
		return ""
	}
	return p.DisplayName
}

// GetOrdersHitRate Get the quote hit rate
// (GET /api/v1/orders/analysis/hit-rate)
func (s *server) GetOrdersHitRate(ctx context.Context, req gen.GetOrdersHitRateRequestObject) (gen.GetOrdersHitRateResponseObject, error) {
	zone, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	p, err := readPeriod(req.Params.From, req.Params.To, zone)
	if err != nil {
		return answer(err, answers[gen.GetOrdersHitRateResponseObject]{
			badRequest: func(p gen.OrdersProblem) gen.GetOrdersHitRateResponseObject {
				return gen.GetOrdersHitRate400ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	var salesperson *uuid.UUID
	if req.Params.SalespersonId != nil {
		id := *req.Params.SalespersonId
		salesperson = &id
	}
	rows, err := store.New(s.deps.Pool).QuotesSentBetween(ctx, store.QuotesSentBetweenParams{
		FromAt: &p.fromAt, ToAt: &p.toAt, SalespersonID: salesperson,
	})
	if err != nil {
		return nil, fmt.Errorf("orders: read the quotes sent: %w", err)
	}
	out := hitRate(rows, p)
	if req.Params.Format != nil && *req.Params.Format == gen.GetOrdersHitRateParamsFormatCsv {
		return csvDownload{body: hitRateCSV(out), fileName: fmt.Sprintf("orders-hit-rate-%s-%s.csv", p.from, p.to)}, nil
	}
	return gen.GetOrdersHitRate200JSONResponse(out), nil
}

// intakeGroup is one group of the intake in one currency: its orders and its
// net amount.
type intakeGroup struct {
	key, label, currency string
	orders               map[int64]bool
	amount               big.Rat
}

// intakeSums gathers the intake's groups, keyed by group and currency.
type intakeSums map[string]*intakeGroup

func (s intakeSums) add(key, label, currency string, order int64, amount *big.Rat) *intakeGroup {
	k := key + "\x00" + currency
	g, ok := s[k]
	if !ok {
		g = &intakeGroup{key: key, label: label, currency: currency, orders: map[int64]bool{}}
		s[k] = g
	}
	if g.label == "" {
		g.label = label
	}
	g.orders[order] = true
	g.amount.Add(&g.amount, amount)
	return g
}

// rows are the groups as the API answers them: by key for months, else by
// label.
func (s intakeSums) rows(byKey bool) []gen.OrdersIntakeRow {
	out := make([]gen.OrdersIntakeRow, 0, len(s))
	for _, g := range s {
		out = append(out, gen.OrdersIntakeRow{
			Key: g.key, Label: g.label, Currency: g.currency, Count: int32(len(g.orders)), Amount: wire(&g.amount, amountPlaces),
		})
	}
	slices.SortFunc(out, func(a, b gen.OrdersIntakeRow) int {
		if byKey {
			return cmp.Or(strings.Compare(a.Key, b.Key), strings.Compare(a.Currency, b.Currency))
		}
		return cmp.Or(strings.Compare(strings.ToLower(a.Label), strings.ToLower(b.Label)), strings.Compare(a.Key, b.Key),
			strings.Compare(a.Currency, b.Currency))
	})
	return out
}

// totals are the groups summed per currency: every order once, and the
// groups' amounts.
func (s intakeSums) totals() []gen.OrdersCurrencySum {
	byCurrency := intakeSums{}
	for _, g := range s {
		var t *intakeGroup
		for order := range g.orders {
			t = byCurrency.add("", "", g.currency, order, new(big.Rat))
		}
		t.amount.Add(&t.amount, &g.amount)
	}
	out := make([]gen.OrdersCurrencySum, 0, len(byCurrency))
	for _, t := range byCurrency {
		out = append(out, gen.OrdersCurrencySum{Currency: t.currency, Count: int32(len(t.orders)), Amount: wire(&t.amount, amountPlaces)})
	}
	slices.SortFunc(out, func(a, b gen.OrdersCurrencySum) int { return strings.Compare(a.Currency, b.Currency) })
	return out
}

// GetOrdersIntake Get the order intake
// (GET /api/v1/orders/analysis/intake)
//
// The orders confirmed in the period and not cancelled since: by month,
// salesperson or customer at their net totals, by product or category at
// their product lines' net amounts. A free-text line is its own group, with
// no key, and so is a line whose variant has no category.
func (s *server) GetOrdersIntake(ctx context.Context, req gen.GetOrdersIntakeRequestObject) (gen.GetOrdersIntakeResponseObject, error) {
	resp := answers[gen.GetOrdersIntakeResponseObject]{
		badRequest: func(p gen.OrdersProblem) gen.GetOrdersIntakeResponseObject {
			return gen.GetOrdersIntake400ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.OrdersProblem) gen.GetOrdersIntakeResponseObject {
			return gen.GetOrdersIntake503ApplicationProblemPlusJSONResponse(p)
		},
	}
	groupBy := gen.OrdersIntakeGroupByMonth
	if req.Params.GroupBy != nil {
		groupBy = gen.OrdersIntakeGroupBy(*req.Params.GroupBy)
	}
	if !groupBy.Valid() {
		var f fieldErrors
		f.add("groupBy", "groupBy is month, salesperson, customer, product or category")
		return gen.GetOrdersIntake400ApplicationProblemPlusJSONResponse(invalid(invalidAnalysisTitle, f)), nil
	}
	zone, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	p, err := readPeriod(req.Params.From, req.Params.To, zone)
	if err != nil {
		return answer(err, resp)
	}
	byLines := groupBy == gen.OrdersIntakeGroupByProduct || groupBy == gen.OrdersIntakeGroupByCategory
	var orders []store.OrdersConfirmedBetweenRow
	var lines []store.IntakeLinesRow
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if orders, err = q.OrdersConfirmedBetween(ctx, store.OrdersConfirmedBetweenParams{FromAt: &p.fromAt, ToAt: &p.toAt}); err != nil {
			return fmt.Errorf("orders: read the orders confirmed: %w", err)
		}
		if !byLines {
			return nil
		}
		ids := make([]int64, 0, len(orders))
		for _, o := range orders {
			ids = append(ids, o.ID)
		}
		if lines, err = q.IntakeLines(ctx, ids); err != nil {
			return fmt.Errorf("orders: read the orders' lines: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sums := intakeSums{}
	currencyOf := map[int64]string{}
	for _, o := range orders {
		currencyOf[o.ID] = o.Currency
		amount := ratOrZero(o.NetTotal)
		switch groupBy {
		case gen.OrdersIntakeGroupByMonth:
			month := o.ConfirmedAt.In(zone).Format("2006-01")
			sums.add(month, month, o.Currency, o.ID, amount)
		case gen.OrdersIntakeGroupBySalesperson:
			key := ""
			if o.SalespersonID != nil {
				key = o.SalespersonID.String()
			}
			sums.add(key, deref(o.SalespersonDisplay), o.Currency, o.ID, amount)
		case gen.OrdersIntakeGroupByCustomer:
			sums.add(strconv.Itoa(int(o.CustomerID)), o.CustomerName, o.Currency, o.ID, amount)
		}
	}
	switch groupBy {
	case gen.OrdersIntakeGroupByProduct:
		for _, l := range lines {
			key, label := "", ""
			if l.ProductID != nil {
				key, label = strconv.Itoa(int(*l.ProductID)), deref(l.Name)
			}
			sums.add(key, label, currencyOf[l.OwnerID], l.OwnerID, ratOrZero(l.LineNet))
		}
	case gen.OrdersIntakeGroupByCategory:
		var ids []int32
		for _, l := range lines {
			if l.VariantID != nil && !slices.Contains(ids, *l.VariantID) {
				ids = append(ids, *l.VariantID)
			}
		}
		slices.Sort(ids)
		variants, err := s.catalogVariants(ctx, ids)
		if err != nil {
			return answer(err, resp)
		}
		for _, l := range lines {
			key, label := "", ""
			if l.VariantID != nil {
				if v, ok := variants[*l.VariantID]; ok && v.CategoryID != nil {
					key, label = strconv.Itoa(int(*v.CategoryID)), v.CategoryName
				}
			}
			sums.add(key, label, currencyOf[l.OwnerID], l.OwnerID, ratOrZero(l.LineNet))
		}
	}
	out := gen.OrdersIntake{
		From: p.from, To: p.to, GroupBy: groupBy, Rows: sums.rows(groupBy == gen.OrdersIntakeGroupByMonth), Totals: sums.totals(),
	}
	if req.Params.Format != nil && *req.Params.Format == gen.GetOrdersIntakeParamsFormatCsv {
		return csvDownload{body: intakeCSV(out), fileName: fmt.Sprintf("orders-intake-%s-%s-%s.csv", groupBy, p.from, p.to)}, nil
	}
	return gen.GetOrdersIntake200JSONResponse(out), nil
}

// backlogTally is one currency's backlog.
type backlogTally struct {
	ordered, delivered                                    map[int64]bool
	orderedAmount, deliveredAmount, recent, inMonth, late big.Rat
}

// backlog sums the backlog lines as of today, a day in the business time
// zone: each line's quantity not delivered, and delivered not invoiced, at
// its net amount over its quantity rounded to øre per line — the customer
// summary's valuation — the latter aged from the latest delivery marked on the
// line, else the order's confirmation. A kit's child line is invoiced as its
// kit (manufacturing phase 2 M145): never delivered not invoiced.
func backlog(rows []store.BacklogLinesRow, today time.Time, zone *time.Location) gen.OrdersBacklog {
	byCurrency := map[string]*backlogTally{}
	for _, r := range rows {
		quantity := ratOrZero(r.Quantity)
		if quantity.Sign() <= 0 {
			continue
		}
		t, ok := byCurrency[r.Currency]
		if !ok {
			t = &backlogTally{ordered: map[int64]bool{}, delivered: map[int64]bool{}}
			byCurrency[r.Currency] = t
		}
		net := ratOrZero(r.LineNet)
		value := func(q *big.Rat) *big.Rat { return round2(new(big.Rat).Quo(new(big.Rat).Mul(net, q), quantity)) }
		delivered := ratOrZero(r.QtyDelivered)
		if open := openQuantity(quantity, ratOrZero(r.QtyCancelled), delivered); open.Sign() > 0 {
			t.ordered[r.OwnerID] = true
			t.orderedAmount.Add(&t.orderedAmount, value(open))
		}
		if notInvoiced := new(big.Rat).Sub(delivered, ratOrZero(r.QtyInvoiced)); notInvoiced.Sign() > 0 && !r.KitChild {
			v := value(notInvoiced)
			t.delivered[r.OwnerID] = true
			t.deliveredAmount.Add(&t.deliveredAmount, v)
			switch age := int(today.Sub(dayOf(r.DeliveredSince, zone)).Hours() / 24); {
			case age <= backlogRecentDays:
				t.recent.Add(&t.recent, v)
			case age <= backlogMonthDays:
				t.inMonth.Add(&t.inMonth, v)
			default:
				t.late.Add(&t.late, v)
			}
		}
	}
	out := gen.OrdersBacklog{
		AsOf: openapi_types.Date{Time: today}, OrderedNotDelivered: []gen.OrdersCurrencySum{},
		DeliveredNotInvoiced: []gen.OrdersBacklogAged{},
	}
	currencies := make([]string, 0, len(byCurrency))
	for c := range byCurrency {
		currencies = append(currencies, c)
	}
	slices.Sort(currencies)
	for _, c := range currencies {
		t := byCurrency[c]
		if len(t.ordered) > 0 {
			out.OrderedNotDelivered = append(out.OrderedNotDelivered, gen.OrdersCurrencySum{
				Currency: c, Count: int32(len(t.ordered)), Amount: wire(&t.orderedAmount, amountPlaces),
			})
		}
		if len(t.delivered) > 0 {
			out.DeliveredNotInvoiced = append(out.DeliveredNotInvoiced, gen.OrdersBacklogAged{
				Currency: c, Count: int32(len(t.delivered)), Amount: wire(&t.deliveredAmount, amountPlaces),
				Days0To7: wire(&t.recent, amountPlaces), Days8To30: wire(&t.inMonth, amountPlaces), Over30Days: wire(&t.late, amountPlaces),
			})
		}
	}
	return out
}

// GetOrdersBacklog Get the order backlog
// (GET /api/v1/orders/analysis/backlog)
func (s *server) GetOrdersBacklog(ctx context.Context, req gen.GetOrdersBacklogRequestObject) (gen.GetOrdersBacklogResponseObject, error) {
	zone, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := store.New(s.deps.Pool).BacklogLines(ctx)
	if err != nil {
		return nil, fmt.Errorf("orders: read the backlog: %w", err)
	}
	today := dayOf(s.deps.Clock(), zone)
	out := backlog(rows, today, zone)
	if req.Params.Format != nil && *req.Params.Format == gen.GetOrdersBacklogParamsFormatCsv {
		return csvDownload{body: backlogCSV(out), fileName: fmt.Sprintf("orders-backlog-%s.csv", today.Format(time.DateOnly))}, nil
	}
	return gen.GetOrdersBacklog200JSONResponse(out), nil
}

// The analysis files are Customers' and Expenses' CSV form, ported because no
// module imports another: semicolons, a byte order mark, CRLF, RFC 4180
// quoting, the decimal comma and the formula guard — the form a Norwegian
// Excel opens without an import dialog. The headers are the API's own names,
// English and untranslated, as the other exports' are.
const (
	csvByteOrderMark = "\ufeff"
	csvSeparator     = ';'
	csvLineEnd       = "\r\n"
)

// csvDownload is an analysis view as a file: the generated text/csv responses
// name no encoding and no file, and this one has to.
type csvDownload struct {
	body     []byte
	fileName string
}

func (d csvDownload) write(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", d.fileName))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(d.body)))
	w.WriteHeader(http.StatusOK)
	_, err := io.Copy(w, bytes.NewReader(d.body))
	return err
}

func (d csvDownload) VisitGetOrdersHitRateResponse(w http.ResponseWriter) error { return d.write(w) }
func (d csvDownload) VisitGetOrdersIntakeResponse(w http.ResponseWriter) error  { return d.write(w) }
func (d csvDownload) VisitGetOrdersBacklogResponse(w http.ResponseWriter) error { return d.write(w) }

// csvFile is the rows as a file, the header first.
func csvFile(header []string, rows [][]string) []byte {
	var b bytes.Buffer
	b.WriteString(csvByteOrderMark)
	writeCSVRow(&b, header)
	for _, r := range rows {
		writeCSVRow(&b, r)
	}
	return b.Bytes()
}

// writeCSVRow writes one row, each cell guarded and quoted as it needs.
func writeCSVRow(b *bytes.Buffer, cells []string) {
	for i, cell := range cells {
		if i > 0 {
			b.WriteRune(csvSeparator)
		}
		b.WriteString(csvCell(cell))
	}
	b.WriteString(csvLineEnd)
}

// csvCell is one cell: an apostrophe in front of what a spreadsheet would read
// as a formula, then quoted if it holds what would end the cell or the row.
func csvCell(value string) string {
	if strings.IndexAny(value, "=+-@\t\r") == 0 {
		value = "'" + value
	}
	if !strings.ContainsAny(value, ";\"\r\n") {
		return value
	}
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

// csvAmount is an amount with the decimal comma; amounts are written by this
// module and never take the formula guard.
func csvAmount(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', amountPlaces, 64), ".", ",", 1)
}

func csvCount(n int32) string { return strconv.Itoa(int(n)) }

func hitRateCSV(h gen.OrdersHitRate) []byte {
	header := []string{
		"salesperson", "currency", "sentCount", "sentAmount", "acceptedCount", "acceptedAmount", "declinedCount",
		"declinedAmount", "expiredCount", "expiredAmount", "cancelledCount", "cancelledAmount", "openCount", "openAmount",
	}
	row := func(who string, r gen.OrdersHitRateRow) []string {
		return []string{
			who, r.Currency, csvCount(r.Sent.Count), csvAmount(r.Sent.Amount), csvCount(r.Accepted.Count), csvAmount(r.Accepted.Amount),
			csvCount(r.Declined.Count), csvAmount(r.Declined.Amount), csvCount(r.Expired.Count), csvAmount(r.Expired.Amount),
			csvCount(r.Cancelled.Count), csvAmount(r.Cancelled.Amount), csvCount(r.Open.Count), csvAmount(r.Open.Amount),
		}
	}
	rows := make([][]string, 0, len(h.Rows)+len(h.Totals))
	for _, r := range h.Rows {
		rows = append(rows, row(personName(r.Salesperson), r))
	}
	for _, r := range h.Totals {
		rows = append(rows, row("Total", r))
	}
	return csvFile(header, rows)
}

func intakeCSV(in gen.OrdersIntake) []byte {
	rows := make([][]string, 0, len(in.Rows)+len(in.Totals))
	for _, r := range in.Rows {
		rows = append(rows, []string{r.Key, r.Label, r.Currency, csvCount(r.Count), csvAmount(r.Amount)})
	}
	for _, t := range in.Totals {
		rows = append(rows, []string{"", "Total", t.Currency, csvCount(t.Count), csvAmount(t.Amount)})
	}
	return csvFile([]string{string(in.GroupBy), "label", "currency", "orders", "amount"}, rows)
}

func backlogCSV(b gen.OrdersBacklog) []byte {
	rows := make([][]string, 0, len(b.OrderedNotDelivered)+len(b.DeliveredNotInvoiced))
	for _, r := range b.OrderedNotDelivered {
		rows = append(rows, []string{"orderedNotDelivered", r.Currency, csvCount(r.Count), csvAmount(r.Amount), "", "", ""})
	}
	for _, r := range b.DeliveredNotInvoiced {
		rows = append(rows, []string{
			"deliveredNotInvoiced", r.Currency, csvCount(r.Count), csvAmount(r.Amount),
			csvAmount(r.Days0To7), csvAmount(r.Days8To30), csvAmount(r.Over30Days),
		})
	}
	return csvFile([]string{"backlog", "currency", "orders", "amount", "days0To7", "days8To30", "over30Days"}, rows)
}
