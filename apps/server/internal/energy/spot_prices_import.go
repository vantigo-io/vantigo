package energy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
	"github.com/vantigo-io/vantigo/server/internal/fileimport"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// This file is the spot price import (energy consumption billing design D2):
// day-ahead prices from a file, for the days the feed has no file for or an
// installation that does not fetch. It is the consumption import's shape
// (consumption_import.go) — one request, read through internal/fileimport
// with the same limits, every row checked, then all written in one
// transaction or nothing — without its mapping: a price file has one fixed
// layout found from its header words (the plan's reading 9), unlike readings,
// which come from many systems.

// policySpotPriceImport is the import's rate limit: the consumption import's
// figures under a name of its own, so the two imports do not share a budget.
var policySpotPriceImport = ratelimit.Policy{
	Name:    "EnergySpotPriceImport",
	Limit:   600,
	Window:  time.Hour,
	Message: "Too many spot price imports. Please wait a little before importing more.",
}

// maxSpotPriceImportRequestBytes is the router's cap for the operation: the
// consumption import's, a file of at most maxConsumptionImportBytes and the
// framing.
const maxSpotPriceImportRequestBytes = maxConsumptionImportRequestBytes

// spotPriceFigureLimit is the first value a numeric(12,6) column cannot hold:
// six digits before the point.
const spotPriceFigureLimit = 1e6

// spotPriceMaxInterval is the longest interval a file without an end column
// is read to give a row (the plan's reading 10): the day-ahead market's
// unit is an hour or a quarter of one, so a longer one is a missing row.
const spotPriceMaxInterval = time.Hour

const invalidSpotPriceImportUploadMessage = "A spot price import is one CSV file of at most 10 MB, sent as the multipart part named 'file'"

// spotPriceImportPart reads the multipart body: exactly one non-empty part
// named file, of at most maxConsumptionImportBytes. Any other part is
// skipped, as the consumption import skips what it does not know.
func spotPriceImportPart(mr *multipart.Reader) ([]byte, bool) {
	if mr == nil {
		return nil, false
	}
	var data []byte
	files := 0
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, false
		}
		if part.FormName() == importFormField {
			files++
			data, err = io.ReadAll(io.LimitReader(part, maxConsumptionImportBytes+1))
			if err != nil {
				files = 2 // an unreadable part is refused as a bad one
			}
		}
		_ = part.Close()
	}
	return data, files == 1 && len(data) > 0 && len(data) <= maxConsumptionImportBytes
}

// The fields of a price file, as its layout and its refusals name them.
const (
	spotPriceFieldArea  = "area"
	spotPriceFieldStart = "start"
	spotPriceFieldEnd   = "end"
	spotPriceFieldNok   = "nok"
	spotPriceFieldOre   = "øre"
	spotPriceFieldEur   = "eur"
	spotPriceFieldRate  = "rate"
)

// spotPriceImportAliases are the header words of each field (the plan's
// reading 9), matched after fileimport.HeaderKey as the consumption import
// matches its own, so "NOK_per_kWh", "nok per kwh" and "NOK/kWh" are close
// enough to be one word. øre is the NOK price written in øre: the same field,
// divided by 100.
var spotPriceImportAliases = map[string][]string{
	spotPriceFieldArea:  {"area", "price_area", "pricearea", "prisområde", "elspotområde", "område", "sone"},
	spotPriceFieldStart: {"start", "from", "fra", "time_start", "starttid"},
	spotPriceFieldEnd:   {"end", "to", "til", "time_end", "sluttid"},
	spotPriceFieldNok:   {"nok_per_kwh", "price", "pris", "kr/kwh", "nok/kwh"},
	spotPriceFieldOre:   {"øre/kwh", "ore_per_kwh"},
	spotPriceFieldEur:   {"eur_per_kwh", "eur/kwh"},
	spotPriceFieldRate:  {"exr", "exchange_rate", "kurs"},
}

// spotPriceAliasIndex is spotPriceImportAliases inverted, under each word's
// fileimport.HeaderKey.
var spotPriceAliasIndex = func() map[string]string {
	index := map[string]string{}
	for field, aliases := range spotPriceImportAliases {
		for _, alias := range aliases {
			index[fileimport.HeaderKey(alias)] = field
		}
	}
	return index
}()

// spotPriceFieldNoun names a field in a refusal of the header.
var spotPriceFieldNoun = map[string]string{
	spotPriceFieldArea:  "the price area",
	spotPriceFieldStart: "the start",
	spotPriceFieldEnd:   "the end",
	spotPriceFieldNok:   "the NOK price",
	spotPriceFieldEur:   "the EUR price",
	spotPriceFieldRate:  "the exchange rate",
}

// spotPriceLayout is the file's columns, 0-based, -1 for one it does not
// have; ore is whether the NOK price column is written in øre.
type spotPriceLayout struct {
	area, start, end, price, eur, rate int
	ore                                bool
}

// spotPriceLayoutOf finds the layout in a header (the plan's reading 9):
// each field from the one column whose header names it — two columns naming
// one field is the file's error, since which one is meant cannot be told —
// and the area, the start and the NOK price required. The messages are the
// refusal of the file, empty when the layout holds.
func spotPriceLayoutOf(header []string) (spotPriceLayout, []string) {
	columns := map[string]int{}
	var messages []string
	for i, h := range header {
		field, ok := spotPriceAliasIndex[fileimport.HeaderKey(h)]
		if !ok {
			continue
		}
		// øre and NOK are one field, the price.
		key := field
		if field == spotPriceFieldOre {
			key = spotPriceFieldNok
		}
		if first, taken := columns[key]; taken {
			messages = append(messages, fmt.Sprintf("Columns %d ('%s') and %d ('%s') both hold %s; keep one of them",
				first, header[first], i, h, spotPriceFieldNoun[key]))
			continue
		}
		columns[key] = i
	}
	column := func(field string) int {
		if c, ok := columns[field]; ok {
			return c
		}
		return -1
	}
	l := spotPriceLayout{
		area: column(spotPriceFieldArea), start: column(spotPriceFieldStart), end: column(spotPriceFieldEnd),
		price: column(spotPriceFieldNok), eur: column(spotPriceFieldEur), rate: column(spotPriceFieldRate),
	}
	if l.price >= 0 {
		l.ore = spotPriceAliasIndex[fileimport.HeaderKey(header[l.price])] == spotPriceFieldOre
	}
	for _, field := range []string{spotPriceFieldArea, spotPriceFieldStart, spotPriceFieldNok} {
		if _, ok := columns[field]; ok {
			continue
		}
		aliases := spotPriceImportAliases[field]
		if field == spotPriceFieldNok {
			aliases = append(slices.Clone(aliases), spotPriceImportAliases[spotPriceFieldOre]...)
		}
		messages = append(messages, fmt.Sprintf("The file has no column for %s; name one in the header as %s",
			spotPriceFieldNoun[field], strings.Join(aliases, ", ")))
	}
	return l, messages
}

// spotPriceImportRow is a row read: its number, its area and interval, and
// whether any of its cells had an error. A row with a start and an area takes
// part in its area's ordering even with an error elsewhere, so one bad price
// does not also make its neighbour's end unknown.
type spotPriceImportRow struct {
	row    int
	price  spotPriceRow
	hasEnd bool
	failed bool
}

// spotPriceEvaluation is a file's rows checked: per area, in file order, the
// rows that read an area and a start; the areas in the order the answer
// lists them; and every problem, in no particular order until sorted.
type spotPriceEvaluation struct {
	byArea map[string][]*spotPriceImportRow
	areas  []string
	errors []gen.SpotPriceImportError
}

func (e *spotPriceEvaluation) fail(row, column int, message string) {
	item := gen.SpotPriceImportError{Row: int32(row), Message: message}
	if column >= 0 {
		c := int32(column)
		item.Column = &c
	}
	e.errors = append(e.errors, item)
}

// evaluateSpotPriceFile checks every row of a file under l, local clock
// times in loc (Europe/Oslo: the market's day, whatever the area). Each cell
// is read first; then each area's rows, in file order, are held to one
// another (the plan's reading 10): with an end column a row may not start
// before the row before it nor inside its interval — a gap is allowed, so one
// file may fill several separate days; without one each row closes at the
// next row's start, the last as long as the row before it (an hour when it
// is the area's only row), and a row the next one does not follow (an
// unordered row) or follows only after a gap is the row's error. Nothing
// here asks the database.
func evaluateSpotPriceFile(file fileimport.File, l spotPriceLayout, loc *time.Location) spotPriceEvaluation {
	e := spotPriceEvaluation{byArea: map[string][]*spotPriceImportRow{}}
	zone := loc.String()
	// The autumn repeat is told apart per area, as each area's rows count
	// its own 02:00 hours.
	repeated := map[string]map[time.Time]int{}
	for _, record := range file.Rows {
		cell := func(column int) string {
			if column < 0 || column >= len(record.Cells) {
				return ""
			}
			return strings.TrimSpace(record.Cells[column])
		}
		before := len(e.errors)
		r := &spotPriceImportRow{row: record.Row, hasEnd: l.end >= 0}

		area := strings.ToUpper(cell(l.area))
		areaOK := false
		switch {
		case area == "":
			e.fail(record.Row, l.area, "A price area is required")
		case !priceAreaValid(area):
			e.fail(record.Row, l.area, fmt.Sprintf("'%s' is not a price area; write it as two letters and one or two digits, such as NO1", cell(l.area)))
		default:
			areaOK = true
			if repeated[area] == nil {
				repeated[area] = map[time.Time]int{}
			}
		}
		var start time.Time
		startOK := false
		if areaOK {
			start, startOK = e.readStart(record.Row, l.start, cell(l.start), loc, zone, repeated[area])
		} else {
			start, startOK = e.readStart(record.Row, l.start, cell(l.start), loc, zone, map[time.Time]int{})
		}
		if l.end >= 0 {
			end, endOK := e.readEnd(record.Row, l.end, cell(l.end), start, startOK, loc, zone)
			if startOK && endOK && !end.After(start) {
				e.fail(record.Row, l.end, "The end must be after the start")
			}
			r.price.End = end
		}

		nok, message := parseSpotPriceFigure(cell(l.price))
		if message != "" {
			e.fail(record.Row, l.price, message)
		} else if l.ore {
			nok = roundPrice(nok / 100)
		}
		r.price.NokPerKwh = nok
		if l.eur >= 0 && cell(l.eur) != "" {
			if v, message := parseSpotPriceFigure(cell(l.eur)); message != "" {
				e.fail(record.Row, l.eur, message)
			} else {
				r.price.EurPerKwh = &v
			}
		}
		if l.rate >= 0 && cell(l.rate) != "" {
			v, message := parseSpotPriceFigure(cell(l.rate))
			switch {
			case message != "":
				e.fail(record.Row, l.rate, message)
			case v <= 0:
				e.fail(record.Row, l.rate, "An exchange rate must be greater than zero")
			default:
				r.price.ExchangeRate = &v
			}
		}

		r.failed = len(e.errors) > before
		if !areaOK || !startOK {
			continue
		}
		r.price.PriceArea, r.price.Start = area, start
		if _, ok := e.byArea[area]; !ok {
			e.areas = append(e.areas, area)
		}
		e.byArea[area] = append(e.byArea[area], r)
	}
	slices.Sort(e.areas)
	for _, area := range e.areas {
		if l.end >= 0 {
			e.orderWithEnds(e.byArea[area])
		} else {
			e.closeWithoutEnds(e.byArea[area], l.start)
		}
	}
	return e
}

// orderWithEnds holds an area's rows, each with its own end, to the one
// before it in the file: not before it, not inside it.
func (e *spotPriceEvaluation) orderWithEnds(rows []*spotPriceImportRow) {
	var prev *spotPriceImportRow
	for _, r := range rows {
		if prev != nil {
			switch {
			case r.price.Start.Before(prev.price.Start):
				e.fail(r.row, -1, fmt.Sprintf("The row starts before row %d; the rows of an area must be in time order", prev.row))
				r.failed = true
				continue
			case r.price.Start.Before(prev.price.End):
				e.fail(r.row, -1, fmt.Sprintf("The row overlaps row %d (%s to %s)", prev.row, spotPriceTime(prev.price.Start), spotPriceTime(prev.price.End)))
				r.failed = true
				continue
			}
		}
		if !r.price.End.After(r.price.Start) {
			continue // the end's own error, already said; it bounds nothing
		}
		prev = r
	}
}

// closeWithoutEnds gives an area's rows their ends from a file without an
// end column: each the next row's start, the last as long as the one before
// it. A
// row that does not start after the one before it is unordered; a row the
// next one follows by more than an hour — or by more than the interval
// before it, so a missing quarter of an hour is seen in a quarter-hour file —
// leaves a gap, the row's error, since its price would otherwise stand for
// the missing rows' too.
func (e *spotPriceEvaluation) closeWithoutEnds(rows []*spotPriceImportRow, startColumn int) {
	var prev *spotPriceImportRow
	step := spotPriceMaxInterval
	for _, r := range rows {
		if prev != nil {
			if !r.price.Start.After(prev.price.Start) {
				e.fail(r.row, startColumn, fmt.Sprintf("The row does not start after row %d (%s); the rows of an area must be in time order",
					prev.row, spotPriceTime(prev.price.Start)))
				r.failed = true
				continue
			}
			interval := r.price.Start.Sub(prev.price.Start)
			prev.price.End = r.price.Start
			if interval > step {
				e.fail(prev.row, -1, fmt.Sprintf("Row %d starts %s after this row, more than the %s between the rows before; a row is missing between them, or give the file an end column",
					r.row, interval, step))
				prev.failed = true
			} else {
				step = interval
			}
		}
		prev = r
	}
	// The last row has no next one: it takes the length of the interval
	// before it, so a quarter-hour file's last quarter stays a quarter and
	// does not run into the next day, and an hour when it is the area's
	// only row (step starts at an hour).
	if prev != nil {
		prev.price.End = prev.price.Start.Add(step)
	}
}

// readStart reads a row's start as the consumption import does
// (consumptionStart): an instant as written, or a clock time in loc — the
// row's error when the clocks skip it, and when they repeat it the earlier
// instant the first time the area has it and the later one after.
func (e *spotPriceEvaluation) readStart(row, column int, cell string, loc *time.Location, zone string, repeated map[time.Time]int) (time.Time, bool) {
	ts, ok := parseConsumptionTimestamp(cell)
	switch {
	case cell == "":
		e.fail(row, column, "A start is required")
		return time.Time{}, false
	case !ok:
		e.fail(row, column, consumptionTimestampRefusal(cell))
		return time.Time{}, false
	case !ts.local:
		return ts.instant, true
	}
	instants := localInstants(ts.wall, loc, false)
	switch len(instants) {
	case 0:
		e.fail(row, column, consumptionSkippedRefusal(cell, zone))
		return time.Time{}, false
	case 1:
		return instants[0], true
	}
	n := repeated[ts.wall]
	repeated[ts.wall] = n + 1
	return instants[min(n, len(instants)-1)], true
}

// readEnd reads a row's end as the consumption import does (consumptionEnd):
// a clock time read as the moment the interval closes, the earliest of its
// instants after the start when the clocks repeat it.
func (e *spotPriceEvaluation) readEnd(row, column int, cell string, start time.Time, startOK bool, loc *time.Location, zone string) (time.Time, bool) {
	ts, ok := parseConsumptionTimestamp(cell)
	switch {
	case cell == "":
		e.fail(row, column, "An end is required")
		return time.Time{}, false
	case !ok:
		e.fail(row, column, consumptionTimestampRefusal(cell))
		return time.Time{}, false
	case !ts.local:
		return ts.instant, true
	}
	instants := localInstants(ts.wall, loc, true)
	if len(instants) == 0 {
		e.fail(row, column, consumptionSkippedRefusal(cell, zone))
		return time.Time{}, false
	}
	if startOK {
		for _, instant := range instants {
			if instant.After(start) {
				return instant, true
			}
		}
	}
	return instants[len(instants)-1], true
}

// parseSpotPriceFigure reads a price or rate cell as the consumption import
// reads a quantity (parseConsumptionQuantity) — a decimal comma or point,
// spaces ignored, no exponent — but signed: the day-ahead market clears below
// zero on windy nights. The value is rounded to six decimals as the column
// stores it; the message is "" on success.
func parseSpotPriceFigure(cell string) (float64, string) {
	text := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\u00a0' || r == '\u202f' {
			return -1
		}
		return r
	}, strings.TrimSpace(cell))
	if text == "" {
		return 0, "A price is required"
	}
	if strings.Contains(text, ",") {
		if strings.Contains(text, ".") {
			return 0, fmt.Sprintf("'%s' is not a number; use either a decimal comma or a decimal point", cell)
		}
		text = strings.Replace(text, ",", ".", 1)
	}
	if !consumptionQuantityPattern.MatchString(text) {
		return 0, fmt.Sprintf("'%s' is not a number", cell)
	}
	v, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, fmt.Sprintf("'%s' is not a number", cell)
	}
	v = roundPrice(v)
	if math.Abs(v) >= spotPriceFigureLimit {
		return 0, fmt.Sprintf("A figure must be less than %.0f in size", spotPriceFigureLimit)
	}
	return v, ""
}

// spotPriceTime is an instant as a refusal names it: in the market's zone,
// with its offset, so the autumn's two 02:00 hours read apart.
func spotPriceTime(t time.Time) string {
	if loc, err := time.LoadLocation(spotPriceDayZone); err == nil {
		t = t.In(loc)
	}
	return t.Format("2006-01-02 15:04 -07:00")
}

// planSpotPriceImport classifies the evaluated rows without an error against
// what q holds, area by area (planSpotPriceWrite), turns each conflict into
// its row's error, and fills the answer: every area with its counts and its
// range over the rows left without an error, and the errors in file order,
// the first consumptionImportMaxErrors of them. It answers the plans to
// write and whether the file is clean.
func planSpotPriceImport(ctx context.Context, q *store.Queries, e spotPriceEvaluation, result *gen.SpotPriceImportResult) ([]spotPriceWritePlan, bool, error) {
	plans := make([]spotPriceWritePlan, 0, len(e.areas))
	for _, area := range e.areas {
		var rows []spotPriceRow
		var numbers []int
		for _, r := range e.byArea[area] {
			if !r.failed {
				rows = append(rows, r.price)
				numbers = append(numbers, r.row)
			}
		}
		plan, err := planSpotPriceWrite(ctx, q, area, rows)
		if err != nil {
			return nil, false, err
		}
		conflicted := map[int]bool{}
		for _, c := range plan.conflicts {
			conflicted[c.Index] = true
			e.fail(numbers[c.Index], -1, fmt.Sprintf("The interval %s to %s overlaps the stored price of %s from %s to %s, an interval of another length; prices of an area never overlap",
				spotPriceTime(rows[c.Index].Start), spotPriceTime(rows[c.Index].End), area, spotPriceTime(c.Stored.Start), spotPriceTime(c.Stored.End)))
		}
		inserted, replaced, unchanged := plan.Counts()
		item := gen.SpotPriceImportArea{PriceArea: area, Inserted: int32(inserted), Replaced: int32(replaced), Unchanged: int32(unchanged)}
		for i, r := range rows {
			if conflicted[i] {
				continue
			}
			if item.RangeStart == nil || r.Start.Before(*item.RangeStart) {
				item.RangeStart = &r.Start
			}
			if item.RangeEnd == nil || r.End.After(*item.RangeEnd) {
				item.RangeEnd = &r.End
			}
		}
		result.Areas = append(result.Areas, item)
		plans = append(plans, plan)
	}
	slices.SortStableFunc(e.errors, func(a, b gen.SpotPriceImportError) int { return int(a.Row - b.Row) })
	result.ErrorCount = int32(len(e.errors))
	result.Errors = e.errors[:min(len(e.errors), consumptionImportMaxErrors)]
	if result.Errors == nil {
		result.Errors = []gen.SpotPriceImportError{}
	}
	return plans, len(e.errors) == 0, nil
}

// PostEnergySpotPricesImport Import spot prices from a file
// (POST /api/v1/energy/spot-prices/import)
//
// Design D2: the part, the file read, the layout from the header (400 on
// file), every row checked; a dry run answers what a run would do, a run
// classifies the rows again and writes every area in one transaction — or
// answers 422 with the same body and writes nothing when any row has an
// error (the plan's reading 11).
func (s *server) PostEnergySpotPricesImport(ctx context.Context, req gen.PostEnergySpotPricesImportRequestObject) (gen.PostEnergySpotPricesImportResponseObject, error) {
	dryRun := req.Params.DryRun == nil || *req.Params.DryRun
	data, ok := spotPriceImportPart(req.Body)
	if !ok {
		return gen.PostEnergySpotPricesImport400ApplicationProblemPlusJSONResponse(invalidImportFile(invalidSpotPriceImportUploadMessage)), nil
	}
	file, refusal := fileimport.Read(data, consumptionImportLimits)
	if refusal != "" {
		return gen.PostEnergySpotPricesImport400ApplicationProblemPlusJSONResponse(invalidImportFile(refusal)), nil
	}
	layout, refusals := spotPriceLayoutOf(file.Header)
	if len(refusals) > 0 {
		return gen.PostEnergySpotPricesImport400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(
			invalidImportFileTitle, map[string][]string{"file": refusals})), nil
	}
	loc, err := time.LoadLocation(spotPriceDayZone)
	if err != nil {
		return nil, fmt.Errorf("energy: load the spot price day zone: %w", err)
	}
	evaluation := evaluateSpotPriceFile(file, layout, loc)
	newResult := func() gen.SpotPriceImportResult {
		return gen.SpotPriceImportResult{DryRun: dryRun, Rows: int32(len(file.Rows)), Areas: []gen.SpotPriceImportArea{}}
	}

	if dryRun {
		result := newResult()
		if _, _, err := planSpotPriceImport(ctx, store.New(s.deps.Pool), evaluation, &result); err != nil {
			return nil, err
		}
		return gen.PostEnergySpotPricesImport200JSONResponse(result), nil
	}

	var result gen.SpotPriceImportResult
	clean := false
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		// The classification is made again in the transaction that writes,
		// so the counts answered are what was written.
		result = newResult()
		plans, ok, err := planSpotPriceImport(ctx, store.New(tx), evaluation, &result)
		if err != nil || !ok {
			return err
		}
		clean = true
		now := s.deps.Clock()
		for _, plan := range plans {
			if err := applySpotPriceWrite(ctx, tx, plan, spotPriceSourceImport, now); err != nil {
				return err
			}
		}
		return nil
	})
	switch {
	case err != nil:
		return nil, fmt.Errorf("energy: import spot prices: %w", err)
	case !clean:
		return gen.PostEnergySpotPricesImport422JSONResponse(result), nil
	}
	result.Imported = true
	return gen.PostEnergySpotPricesImport200JSONResponse(result), nil
}
