package manufacturing

// The reports (manufacturing design M42): production cost, work-center
// utilisation and time deviation, each for a period of days in the business
// time zone and optionally one site, as JSON or as a CSV file. Production
// cost is every figure a cost, so its operation needs
// manufacturing:costs-view entirely (the router's rule); the other two carry
// no cost.

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

const (
	// reportDefaultDays is the default period: the 30 days to today.
	reportDefaultDays = 29
	// reportMaxDays is how far to may lie after from.
	reportMaxDays = 366
	reportTitle   = "The report was not read"
)

// reportPeriod is a report's days, from … to both counted, and the instants
// they span in the zone.
type reportPeriod struct {
	from, to time.Time
	window   span
	zone     *time.Location
	now      time.Time
}

// period judges a report's from and to (M42): to defaults to today and from
// to 29 days before to; from after to, or to more than 366 days after from,
// is a 400, the latter period_too_long.
func (s *server) period(ctx context.Context, from, to *openapi_types.Date) (reportPeriod, error) {
	zone, now, err := s.scheduleClock(ctx)
	if err != nil {
		return reportPeriod{}, err
	}
	p := reportPeriod{zone: zone, now: now, to: holidays.DayIn(now, zone)}
	if to != nil {
		p.to = civilDay(to.Time)
	}
	p.from = p.to.AddDate(0, 0, -reportDefaultDays)
	if from != nil {
		p.from = civilDay(from.Time)
	}
	switch {
	case p.from.After(p.to):
		return p, refuseField(reportTitle, "from", "", "Must not be after to")
	case p.to.Sub(p.from) > reportMaxDays*24*time.Hour:
		return p, refuseField(reportTitle, "to", codePeriodTooLong, fmt.Sprintf("At most %d days after from", reportMaxDays))
	}
	p.window = span{start: dayStart(p.from, zone), end: dayStart(p.to.AddDate(0, 0, 1), zone)}
	return p, nil
}

// reportFile is the CSV file's name.
func (p reportPeriod) fileName(report string) string {
	return fmt.Sprintf("manufacturing-%s-%s-%s.csv", report, p.from.Format(time.DateOnly), p.to.Format(time.DateOnly))
}

// ---- production cost -----------------------------------------------------

func productionCostAnswers() answers[gen.GetManufacturingProductionCostReportResponseObject] {
	return answers[gen.GetManufacturingProductionCostReportResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.GetManufacturingProductionCostReportResponseObject {
			return gen.GetManufacturingProductionCostReport400ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.GetManufacturingProductionCostReportResponseObject {
			return gen.GetManufacturingProductionCostReport503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// GetManufacturingProductionCostReport is the done orders closed in the
// period, grouped by product, with their cost as fixed at close (M41, M42).
func (s *server) GetManufacturingProductionCostReport(ctx context.Context, req gen.GetManufacturingProductionCostReportRequestObject) (gen.GetManufacturingProductionCostReportResponseObject, error) {
	p, err := s.period(ctx, req.Params.From, req.Params.To)
	if err != nil {
		return answer(err, productionCostAnswers())
	}
	var orders []store.ManufacturingOrder
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		orders, err = q.DoneOrdersClosedBetween(ctx, store.DoneOrdersClosedBetweenParams{FromAt: p.window.start, ToAt: p.window.end, SiteID: req.Params.SiteId})
		return err
	})
	if err != nil {
		return nil, err
	}
	var ids []int32
	for _, o := range orders {
		ids = append(ids, o.VariantID)
		if a := unmarshalCost(o.Actual); a != nil {
			for _, b := range a.ByProducts {
				ids = append(ids, b.VariantID)
			}
		}
	}
	entries, err := s.catalogVariants(ctx, slices.Compact(slices.Sorted(slices.Values(ids))))
	if err != nil {
		return answer(err, productionCostAnswers())
	}
	out := productionCostOf(orders, entries, p, req.Params.SiteId)
	if req.Params.Format != nil && *req.Params.Format == gen.GetManufacturingProductionCostReportParamsFormatCsv {
		return csvDownload{body: productionCostCSV(out), fileName: p.fileName("production-cost")}, nil
	}
	return gen.GetManufacturingProductionCostReport200JSONResponse(out), nil
}

// productionCostOf groups the orders by product (M42): per order its fixed
// estimate, actual, deviation and cost per unit; per product the totals,
// the deviation over the orders with both and the cost per unit of the
// total good output.
func productionCostOf(orders []store.ManufacturingOrder, entries map[int32]contracts.VariantEntry, p reportPeriod, siteID *int64) gen.ManufacturingProductionCostReport {
	out := gen.ManufacturingProductionCostReport{From: openapi_types.Date{Time: p.from}, To: openapi_types.Date{Time: p.to}, SiteId: siteID,
		Groups: []gen.ManufacturingProductionCostGroup{}, ByProducts: []gen.ManufacturingProductionCostByproduct{}}
	type sums struct{ produced, estimate, actual, main, devEstimate, deviation *big.Rat }
	byVariant := map[int32]*gen.ManufacturingProductionCostGroup{}
	totals := map[int32]*sums{}
	var order []int32
	for _, o := range orders {
		g, ok := byVariant[o.VariantID]
		if !ok {
			g = &gen.ManufacturingProductionCostGroup{Variant: refOf(entries, o.VariantID, true), Orders: []gen.ManufacturingProductionCostRow{}}
			byVariant[o.VariantID] = g
			totals[o.VariantID] = &sums{new(big.Rat), new(big.Rat), new(big.Rat), new(big.Rat), new(big.Rat), new(big.Rat)}
			order = append(order, o.VariantID)
		}
		t := totals[o.VariantID]
		row := gen.ManufacturingProductionCostRow{OrderId: o.ID, Number: orderNumber(o.Number, o.Suffix),
			Quantity: numericText(o.Quantity, quantityPlaces), Produced: numericText(o.Produced, quantityPlaces)}
		if o.ClosedAt != nil {
			row.ClosedAt = *o.ClosedAt
		}
		estimate, actual := unmarshalCost(o.Estimate), unmarshalCost(o.Actual)
		if estimate != nil {
			row.Estimate = &estimate.Total
			t.estimate.Add(t.estimate, mustRat(estimate.Total))
		}
		if actual != nil {
			row.Actual = &actual.Total
			row.CostPerUnit = actual.CostPerUnit
			// The main product's part only (manufacturing phase 2 M122): the
			// product's cost per unit is what it carried.
			main := actual.Total
			if actual.MainTotal != nil {
				main = *actual.MainTotal
			}
			t.actual.Add(t.actual, mustRat(actual.Total))
			t.main.Add(t.main, mustRat(main))
			for _, b := range actual.ByProducts {
				out.ByProducts = append(out.ByProducts, gen.ManufacturingProductionCostByproduct{OrderId: o.ID, Number: row.Number,
					Variant: refOf(entries, b.VariantID, true), CostShare: b.Share, Produced: b.Produced, Actual: b.Amount,
					CostPerUnit: b.CostPerUnit, NotProduced: b.NotProduced})
			}
			// The cost per unit of the total is the actual over the output it
			// paid for: an order with no actual adds to neither.
			t.produced.Add(t.produced, ratOrZero(o.Produced))
		}
		if estimate != nil && actual != nil {
			d := deviationPart(actual.Total, estimate.Total)
			row.Deviation, row.DeviationPercent = &d.Amount, d.Percent
			t.deviation.Add(t.deviation, mustRat(d.Amount))
			t.devEstimate.Add(t.devEstimate, mustRat(estimate.Total))
		}
		g.Orders = append(g.Orders, row)
	}
	slices.SortFunc(order, func(a, b int32) int {
		return cmp.Or(strings.Compare(byVariant[a].Variant.ProductName, byVariant[b].Variant.ProductName),
			strings.Compare(byVariant[a].Variant.Sku, byVariant[b].Variant.Sku), cmp.Compare(a, b))
	})
	for _, id := range order {
		g, t := byVariant[id], totals[id]
		g.Totals = gen.ManufacturingProductionCostTotals{
			Orders: int32(len(g.Orders)), Produced: text(t.produced, quantityPlaces), Estimate: amountText(t.estimate),
			Actual: amountText(t.actual), Deviation: amountText(t.deviation), DeviationPercent: percentOf(t.deviation, t.devEstimate),
			CostPerUnit: costPerUnitOf(t.main, t.produced),
		}
		out.Groups = append(out.Groups, *g)
	}
	return out
}

func productionCostCSV(r gen.ManufacturingProductionCostReport) []byte {
	header := []string{"sku", "product", "order", "closedAt", "quantity", "produced", "estimate", "actual", "deviation", "deviationPercent", "costPerUnit"}
	var rows [][]string
	for _, g := range r.Groups {
		for _, o := range g.Orders {
			rows = append(rows, []string{g.Variant.Sku, g.Variant.ProductName, o.Number, o.ClosedAt.UTC().Format(time.RFC3339),
				csvDecimal(o.Quantity), csvDecimal(o.Produced), csvOptional(o.Estimate), csvOptional(o.Actual), csvOptional(o.Deviation),
				csvOptional(o.DeviationPercent), csvOptional(o.CostPerUnit)})
		}
	}
	return csvFile(header, rows)
}

// ---- utilisation ---------------------------------------------------------

func utilisationAnswers() answers[gen.GetManufacturingUtilisationReportResponseObject] {
	return answers[gen.GetManufacturingUtilisationReportResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.GetManufacturingUtilisationReportResponseObject {
			return gen.GetManufacturingUtilisationReport400ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// GetManufacturingUtilisationReport is, per work center and ISO week, the
// capacity, the planned time, the productive time and the downtime by
// reason, counting only the period's days (M42).
func (s *server) GetManufacturingUtilisationReport(ctx context.Context, req gen.GetManufacturingUtilisationReportRequestObject) (gen.GetManufacturingUtilisationReportResponseObject, error) {
	p, err := s.period(ctx, req.Params.From, req.Params.To)
	if err != nil {
		return answer(err, utilisationAnswers())
	}
	var (
		centers  []store.ManufacturingWorkCenter
		closures []store.ManufacturingWorkCenterClosure
		slots    []store.SlotsAtWorkCentersBetweenRow
		logs     []store.ManufacturingTimeLog
	)
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if centers, err = q.ListWorkCenters(ctx, store.ListWorkCentersParams{SiteID: req.Params.SiteId}); err != nil {
			return err
		}
		ids := make([]int64, 0, len(centers))
		for _, c := range centers {
			ids = append(ids, c.ID)
		}
		ids = orEmpty(ids)
		if closures, err = q.ClosuresOfWorkCenters(ctx, store.ClosuresOfWorkCentersParams{WorkCenterIds: ids, FromDate: dateValue(p.from), ToDate: dateValue(p.to)}); err != nil {
			return err
		}
		if slots, err = q.SlotsAtWorkCentersBetween(ctx, store.SlotsAtWorkCentersBetweenParams{WorkCenterIds: ids, FromAt: p.window.start, ToAt: p.window.end}); err != nil {
			return err
		}
		logs, err = q.LogsAtWorkCentersBetween(ctx, store.LogsAtWorkCentersBetweenParams{WorkCenterIds: ids, FromAt: p.window.start, ToAt: p.window.end})
		return err
	})
	if err != nil {
		return nil, err
	}
	out := utilisationOf(centers, closures, slots, logs, p, req.Params.SiteId)
	if req.Params.Format != nil && *req.Params.Format == gen.GetManufacturingUtilisationReportParamsFormatCsv {
		return csvDownload{body: utilisationCSV(out), fileName: p.fileName("utilisation")}, nil
	}
	return gen.GetManufacturingUtilisationReport200JSONResponse(out), nil
}

// utilisationOf is the utilisation rows (M42): a row for each center and
// ISO week with any capacity or any time; an archived center with no
// planned, productive or down time is left out, whatever its calendar. Productive time and downtime are each the union of the
// center's logs, so two operators on one machine count its time once.
func utilisationOf(centers []store.ManufacturingWorkCenter, closures []store.ManufacturingWorkCenterClosure, slots []store.SlotsAtWorkCentersBetweenRow,
	logs []store.ManufacturingTimeLog, p reportPeriod, siteID *int64) gen.ManufacturingUtilisationReport {
	out := gen.ManufacturingUtilisationReport{From: openapi_types.Date{Time: p.from}, To: openapi_types.Date{Time: p.to}, SiteId: siteID,
		Rows: []gen.ManufacturingUtilisationRow{}}
	closuresOf := map[int64][]store.ManufacturingWorkCenterClosure{}
	for _, c := range closures {
		closuresOf[c.WorkCenterID] = append(closuresOf[c.WorkCenterID], c)
	}
	slotsOf := map[int64][]span{}
	for _, sl := range slots {
		slotsOf[sl.WorkCenterID] = append(slotsOf[sl.WorkCenterID], span{start: sl.StartsAt, end: sl.EndsAt})
	}
	productiveOf := map[int64][]span{}
	downtimeOf := map[int64]map[string][]span{}
	for _, l := range logs {
		sp := logSpan(l, p.now)
		switch {
		case l.Kind == logProductive:
			productiveOf[l.WorkCenterID] = append(productiveOf[l.WorkCenterID], sp)
		case l.Kind == logDowntime && l.Reason != nil:
			if downtimeOf[l.WorkCenterID] == nil {
				downtimeOf[l.WorkCenterID] = map[string][]span{}
			}
			downtimeOf[l.WorkCenterID][*l.Reason] = append(downtimeOf[l.WorkCenterID][*l.Reason], sp)
		}
	}
	for _, c := range centers {
		cal := calendarOf(c, closuresOf[c.ID], p.zone)
		// The weeks the period touches, each cut to the period's days.
		for monday := p.from.AddDate(0, 0, -((int(p.from.Weekday()) + 6) % 7)); !monday.After(p.to); monday = monday.AddDate(0, 0, 7) {
			first, last := monday, monday.AddDate(0, 0, 6)
			if first.Before(p.from) {
				first = p.from
			}
			if last.After(p.to) {
				last = p.to
			}
			w := span{start: dayStart(first, p.zone), end: dayStart(last.AddDate(0, 0, 1), p.zone)}
			var capacity, planned time.Duration
			for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
				capacity += cal.capacity(d)
			}
			for _, sl := range slotsOf[c.ID] {
				planned += overlap(sl, w)
			}
			productive := unionLength(clip(productiveOf[c.ID], w))
			var all []span
			byReason := []gen.ManufacturingDowntimeMinutes{}
			reasons := make([]string, 0, len(downtimeOf[c.ID]))
			for r := range downtimeOf[c.ID] {
				reasons = append(reasons, r)
			}
			slices.Sort(reasons)
			for _, r := range reasons {
				in := clip(downtimeOf[c.ID][r], w)
				all = append(all, in...)
				if d := unionLength(in); d > 0 {
					byReason = append(byReason, gen.ManufacturingDowntimeMinutes{Reason: gen.ManufacturingDowntimeReason(r), Minutes: int64(d / time.Minute)})
				}
			}
			downtime := unionLength(all)
			// An archived center has no calendar to report: its row stays
			// only for the time that was planned or logged at it.
			if c.Status != statusActive && planned == 0 && productive == 0 && downtime == 0 {
				continue
			}
			year, week := monday.ISOWeek()
			out.Rows = append(out.Rows, gen.ManufacturingUtilisationRow{
				WorkCenter: centerRefOf(c, c.ID), Week: fmt.Sprintf("%04d-W%02d", year, week), WeekStart: openapi_types.Date{Time: monday},
				CapacityMinutes: int64(capacity / time.Minute), PlannedMinutes: int64(planned / time.Minute),
				ProductiveMinutes: int64(productive / time.Minute), DowntimeMinutes: int64(downtime / time.Minute),
				Downtime:           byReason,
				UtilisationPercent: percentOf(big.NewRat(int64(productive/time.Second), 1), big.NewRat(int64(capacity/time.Second), 1)),
			})
		}
	}
	return out
}

func utilisationCSV(r gen.ManufacturingUtilisationReport) []byte {
	reasons := []gen.ManufacturingDowntimeReason{gen.Breakdown, gen.MaterialMissing, gen.Setup, gen.WaitingForOperator, gen.Quality, gen.Other}
	header := []string{"workCenter", "name", "week", "weekStart", "capacityMinutes", "plannedMinutes", "productiveMinutes", "downtimeMinutes", "utilisationPercent"}
	for _, reason := range reasons {
		header = append(header, "downtime_"+string(reason))
	}
	rows := make([][]string, 0, len(r.Rows))
	for _, row := range r.Rows {
		cells := []string{row.WorkCenter.Code, row.WorkCenter.Name, row.Week, row.WeekStart.Format(time.DateOnly),
			strconv.FormatInt(row.CapacityMinutes, 10), strconv.FormatInt(row.PlannedMinutes, 10), strconv.FormatInt(row.ProductiveMinutes, 10),
			strconv.FormatInt(row.DowntimeMinutes, 10), csvOptional(row.UtilisationPercent)}
		for _, reason := range reasons {
			var minutes int64
			for _, d := range row.Downtime {
				if d.Reason == reason {
					minutes = d.Minutes
				}
			}
			cells = append(cells, strconv.FormatInt(minutes, 10))
		}
		rows = append(rows, cells)
	}
	return csvFile(header, rows)
}

// ---- time deviation ------------------------------------------------------

func timeDeviationAnswers() answers[gen.GetManufacturingTimeDeviationReportResponseObject] {
	return answers[gen.GetManufacturingTimeDeviationReportResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.GetManufacturingTimeDeviationReportResponseObject {
			return gen.GetManufacturingTimeDeviationReport400ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.GetManufacturingTimeDeviationReportResponseObject {
			return gen.GetManufacturingTimeDeviationReport503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// GetManufacturingTimeDeviationReport is, per BoM operation (its BoM and
// sequence), the average actual against the expected minutes of its last 10
// done work orders finished in the period (M42).
func (s *server) GetManufacturingTimeDeviationReport(ctx context.Context, req gen.GetManufacturingTimeDeviationReportRequestObject) (gen.GetManufacturingTimeDeviationReportResponseObject, error) {
	p, err := s.period(ctx, req.Params.From, req.Params.To)
	if err != nil {
		return answer(err, timeDeviationAnswers())
	}
	var (
		wos     []store.RecentDoneWorkOrdersRow
		logs    []store.ManufacturingTimeLog
		boms    []store.ManufacturingBom
		centers []store.ManufacturingWorkCenter
	)
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if wos, err = q.RecentDoneWorkOrders(ctx, store.RecentDoneWorkOrdersParams{FromAt: p.window.start, ToAt: p.window.end, SiteID: req.Params.SiteId}); err != nil {
			return err
		}
		var ids, bomIDs, centerIDs []int64
		for _, w := range wos {
			ids, bomIDs, centerIDs = append(ids, w.ID), append(bomIDs, w.BomID), append(centerIDs, w.WorkCenterID)
		}
		if logs, err = q.LogsOfWorkOrders(ctx, orEmpty(ids)); err != nil {
			return err
		}
		if boms, err = q.BomsByIDs(ctx, orEmpty(slices.Compact(slices.Sorted(slices.Values(bomIDs))))); err != nil {
			return err
		}
		centers, err = q.WorkCentersByIDs(ctx, orEmpty(slices.Compact(slices.Sorted(slices.Values(centerIDs)))))
		return err
	})
	if err != nil {
		return nil, err
	}
	var variantIDs []int32
	for _, b := range boms {
		variantIDs = append(variantIDs, b.VariantID)
	}
	entries, err := s.catalogVariants(ctx, slices.Compact(slices.Sorted(slices.Values(variantIDs))))
	if err != nil {
		return answer(err, timeDeviationAnswers())
	}
	out := timeDeviationOf(wos, logs, boms, centers, entries, p, req.Params.SiteId, s.has(ctx, permCostsView))
	if req.Params.Format != nil && *req.Params.Format == gen.GetManufacturingTimeDeviationReportParamsFormatCsv {
		return csvDownload{body: timeDeviationCSV(out), fileName: p.fileName("time-deviation")}, nil
	}
	return gen.GetManufacturingTimeDeviationReport200JSONResponse(out), nil
}

// timeDeviationOf averages, per BoM and sequence, the expected minutes and
// the actual — each work order's union of productive logs — of the rows,
// which come newest first, at most 10 per operation.
func timeDeviationOf(wos []store.RecentDoneWorkOrdersRow, logs []store.ManufacturingTimeLog, boms []store.ManufacturingBom,
	centers []store.ManufacturingWorkCenter, entries map[int32]contracts.VariantEntry, p reportPeriod, siteID *int64, costs bool) gen.ManufacturingTimeDeviationReport {
	out := gen.ManufacturingTimeDeviationReport{From: openapi_types.Date{Time: p.from}, To: openapi_types.Date{Time: p.to}, SiteId: siteID,
		Rows: []gen.ManufacturingTimeDeviationRow{}}
	productive := map[int64][]span{}
	for _, l := range logs {
		if l.WorkOrderID != nil && l.Kind == logProductive {
			productive[*l.WorkOrderID] = append(productive[*l.WorkOrderID], logSpan(l, p.now))
		}
	}
	bomOf := map[int64]store.ManufacturingBom{}
	for _, b := range boms {
		bomOf[b.ID] = b
	}
	centerOf := map[int64]store.ManufacturingWorkCenter{}
	for _, c := range centers {
		centerOf[c.ID] = c
	}
	type key struct {
		bom int64
		seq int32
	}
	type group struct {
		first            store.RecentDoneWorkOrdersRow
		n                int64
		expected, actual *big.Rat
	}
	groups := map[key]*group{}
	var keys []key
	for _, w := range wos {
		k := key{w.BomID, w.Sequence}
		g, ok := groups[k]
		if !ok {
			g = &group{first: w, expected: new(big.Rat), actual: new(big.Rat)}
			groups[k] = g
			keys = append(keys, k)
		}
		g.n++
		g.expected.Add(g.expected, big.NewRat(w.ExpectedMinutes, 1))
		g.actual.Add(g.actual, durationMinutes(unionLength(productive[w.ID])))
	}
	for _, k := range keys {
		g := groups[k]
		n := big.NewRat(g.n, 1)
		expected, actual := new(big.Rat).Quo(g.expected, n), new(big.Rat).Quo(g.actual, n)
		b := bomOf[k.bom]
		out.Rows = append(out.Rows, gen.ManufacturingTimeDeviationRow{
			BomId: k.bom, BomCode: b.Code, Variant: refOf(entries, b.VariantID, costs), Sequence: k.seq, Name: g.first.Name,
			WorkCenter: centerRefOf(centerOf[g.first.WorkCenterID], g.first.WorkCenterID), WorkOrders: int32(g.n),
			AverageExpectedMinutes: expected.FloatString(percentDisplayPlaces), AverageActualMinutes: actual.FloatString(percentDisplayPlaces),
			DeviationPercent: percentOf(new(big.Rat).Sub(actual, expected), expected),
		})
	}
	return out
}

func timeDeviationCSV(r gen.ManufacturingTimeDeviationReport) []byte {
	header := []string{"sku", "product", "bom", "sequence", "operation", "workCenter", "workOrders", "averageExpectedMinutes", "averageActualMinutes", "deviationPercent"}
	rows := make([][]string, 0, len(r.Rows))
	for _, row := range r.Rows {
		rows = append(rows, []string{row.Variant.Sku, row.Variant.ProductName, csvText(row.BomCode), strconv.Itoa(int(row.Sequence)), row.Name,
			row.WorkCenter.Code, strconv.Itoa(int(row.WorkOrders)), csvDecimal(row.AverageExpectedMinutes), csvDecimal(row.AverageActualMinutes),
			csvOptional(row.DeviationPercent)})
	}
	return csvFile(header, rows)
}

// ---- the CSV file --------------------------------------------------------

// The files are Orders' analysis form, ported because no module imports
// another: semicolons, a byte order mark, CRLF, RFC 4180 quoting, the
// decimal comma and the formula guard — what a Norwegian Excel opens
// without an import dialog. The headers are the API's names, untranslated.
const (
	csvByteOrderMark = "\ufeff"
	csvSeparator     = ';'
	csvLineEnd       = "\r\n"
)

// csvDownload is a report as a file: the generated text/csv responses name
// no encoding and no file, and this one has to.
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

func (d csvDownload) VisitGetManufacturingProductionCostReportResponse(w http.ResponseWriter) error {
	return d.write(w)
}

func (d csvDownload) VisitGetManufacturingUtilisationReportResponse(w http.ResponseWriter) error {
	return d.write(w)
}

func (d csvDownload) VisitGetManufacturingTimeDeviationReportResponse(w http.ResponseWriter) error {
	return d.write(w)
}

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

func writeCSVRow(b *bytes.Buffer, cells []string) {
	for i, cell := range cells {
		if i > 0 {
			b.WriteRune(csvSeparator)
		}
		b.WriteString(csvCell(cell))
	}
	b.WriteString(csvLineEnd)
}

// csvCell is one cell: an apostrophe before what a spreadsheet would read
// as a formula, then quoted if it holds what would end the cell or the row.
func csvCell(value string) string {
	if strings.IndexAny(value, "=+-@\t\r") == 0 && !csvNumber.MatchString(value) {
		value = "'" + value
	}
	if !strings.ContainsAny(value, ";\"\r\n") {
		return value
	}
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

// csvNumber is a number this module wrote: a negative one is no formula and
// keeps its minus unguarded, so a spreadsheet reads it as a number.
var csvNumber = regexp.MustCompile(`^-?[0-9]+(,[0-9]+)?$`)

// csvDecimal is exact decimal text with the decimal comma.
func csvDecimal(v string) string { return strings.Replace(v, ".", ",", 1) }

// csvOptional is csvDecimal of an optional value, empty when nil.
func csvOptional(v *string) string {
	if v == nil {
		return ""
	}
	return csvDecimal(*v)
}

// csvText is an optional text, empty when nil.
func csvText(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
