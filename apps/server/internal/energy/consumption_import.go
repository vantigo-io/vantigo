package energy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // the market time zones, wherever the binary runs

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
	"github.com/vantigo-io/vantigo/server/internal/fileimport"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// This file is the consumption import (energy import design D8): one
// metering point's readings from a file, in one request and one transaction —
// no import record and no worker, since 50 000 rows is seconds. The same
// endpoint reads a file (no mapping: the header, a sample and a suggestion),
// checks it (a dry run: the counts, the errors, the range) and writes it (all
// or nothing). Each row is a manual reading's interval under the manual
// reading's rules (consumption.go), with source Import.

// The consumption import's bounds (design D8): a file of readings is long and
// narrow. maxConsumptionImportRequestBytes is the router's cap for the
// operation: the file plus room for the mapping part and the framing.
const (
	maxConsumptionImportBytes        = 10 * 1024 * 1024
	maxConsumptionImportRequestBytes = maxConsumptionImportBytes + 64*1024
	maxConsumptionImportRows         = 50_000
	maxConsumptionImportColumns      = 50
	maxConsumptionImportMappingBytes = 16 * 1024
	// consumptionImportSampleRows is how many rows the answer shows of the
	// file, consumptionImportMaxErrors how many of its errors.
	consumptionImportSampleRows = 10
	consumptionImportMaxErrors  = 200
	// consumptionQuantityLimit is the first value consumption_intervals'
	// quantity_kwh numeric(14,3) cannot hold: eleven digits before the point.
	consumptionQuantityLimit = 1e11
)

// consumptionImportLimits are the bounds above as the reader takes them.
var consumptionImportLimits = fileimport.Limits{
	MaxBytes:   maxConsumptionImportBytes,
	MaxRows:    maxConsumptionImportRows,
	MaxColumns: maxConsumptionImportColumns,
}

// consumptionSourceImport is the source a consumption import writes, beside
// Manual (the manual reading) and Elhub.
const consumptionSourceImport = "Import"

// policyConsumptionImport is the consumption import's rate limit: the
// metering-point upload's, since every call reads a file. A person's import is
// three calls (read, check, import).
var policyConsumptionImport = ratelimit.Policy{
	Name:    "EnergyConsumptionImport",
	Limit:   600,
	Window:  time.Hour,
	Message: "Too many consumption imports. Please wait a little before importing more.",
}

// The refusals of a request the rows never see.
const (
	invalidConsumptionImportUploadMessage  = "A consumption import is one CSV file of at most 10 MB, sent as the multipart part named 'file'"
	invalidConsumptionImportMappingTitle   = "Invalid mapping"
	invalidConsumptionImportMappingPart    = "The mapping is at most one multipart part named 'mapping' of at most 16 KB"
	invalidConsumptionImportMappingJSON    = "The mapping must be a JSON object naming the columns of start, end or resolution, quantityKwh, and optionally quality and gsrn"
	consumptionImportMappingMissingMessage = "An import needs a mapping; send the part named 'mapping', or check the file first with dryRun=true"
)

// consumptionImportParts reads the multipart body: exactly one non-empty file
// part of at most maxConsumptionImportBytes, and at most one mapping part of
// at most maxConsumptionImportMappingBytes. The errors are the contract's
// field map, keyed file or mapping.
func consumptionImportParts(mr *multipart.Reader) (data, mapping []byte, hasMapping bool, errs map[string][]string) {
	errs = map[string][]string{}
	if mr == nil {
		errs["file"] = []string{invalidConsumptionImportUploadMessage}
		return nil, nil, false, errs
	}
	files := 0
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			errs["file"] = []string{invalidConsumptionImportUploadMessage}
			return nil, nil, false, errs
		}
		switch part.FormName() {
		case importFormField:
			files++
			data, err = io.ReadAll(io.LimitReader(part, maxConsumptionImportBytes+1))
			if err != nil {
				files = 2 // an unreadable part is refused as a bad one
			}
		case "mapping":
			if hasMapping {
				errs["mapping"] = []string{invalidConsumptionImportMappingPart}
			}
			hasMapping = true
			mapping, err = io.ReadAll(io.LimitReader(part, maxConsumptionImportMappingBytes+1))
			if err != nil || len(mapping) > maxConsumptionImportMappingBytes {
				errs["mapping"] = []string{invalidConsumptionImportMappingPart}
			}
		}
		_ = part.Close()
	}
	if files != 1 || len(data) == 0 || len(data) > maxConsumptionImportBytes {
		errs["file"] = []string{invalidConsumptionImportUploadMessage}
	}
	return data, mapping, hasMapping, errs
}

// consumptionLayout is a validated mapping: the 0-based column of each field,
// -1 for one not mapped, and step — the length of every interval — when the
// mapping gives a resolution instead of an end column.
type consumptionLayout struct {
	start, end, quantity, quality, gsrn int
	step                                time.Duration
}

// consumptionResolutionSteps are the resolutions a mapping may give instead of
// an end column.
var consumptionResolutionSteps = map[gen.ConsumptionImportMappingResolution]time.Duration{
	gen.PT1H:  time.Hour,
	gen.PT15M: 15 * time.Minute,
}

// parseConsumptionMapping reads the mapping part against a header of width
// columns (design D8): start and quantityKwh required, exactly one of end and
// resolution, every column within the file and none mapped twice. The errors
// are the contract's field map, keyed mapping or the field.
func parseConsumptionMapping(raw []byte, width int) (consumptionLayout, map[string][]string) {
	errs := map[string][]string{}
	var m gen.ConsumptionImportMapping
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil || dec.More() {
		errs["mapping"] = []string{invalidConsumptionImportMappingJSON}
		return consumptionLayout{}, errs
	}
	taken := map[int32]string{}
	column := func(field string, c *int32, required bool) int {
		switch {
		case c == nil && required:
			errs[field] = append(errs[field], fmt.Sprintf("Map the column that holds %s", consumptionFieldNoun[field]))
		case c == nil:
		case *c < 0 || int(*c) >= width:
			errs[field] = append(errs[field], fmt.Sprintf("Column %d is not in the file, which has columns 0 to %d", *c, width-1))
		case taken[*c] != "":
			errs[field] = append(errs[field], fmt.Sprintf("Column %d is already mapped to %s", *c, taken[*c]))
		default:
			taken[*c] = field
			return int(*c)
		}
		return -1
	}
	l := consumptionLayout{
		start:    column("start", m.Start, true),
		end:      column("end", m.End, false),
		quantity: column("quantityKwh", m.QuantityKwh, true),
		quality:  column("quality", m.Quality, false),
		gsrn:     column("gsrn", m.Gsrn, false),
	}
	switch {
	case m.End != nil && m.Resolution != nil:
		errs["resolution"] = append(errs["resolution"], "Map an end column or choose a resolution, not both")
	case m.End == nil && m.Resolution == nil:
		errs["resolution"] = append(errs["resolution"], "Map an end column or choose a resolution (PT1H or PT15M)")
	case m.Resolution != nil:
		step, ok := consumptionResolutionSteps[*m.Resolution]
		if !ok {
			errs["resolution"] = append(errs["resolution"], fmt.Sprintf("A resolution must be PT1H or PT15M, but was '%s'", *m.Resolution))
		}
		l.step = step
	}
	if len(errs) > 0 {
		return consumptionLayout{}, errs
	}
	return l, nil
}

// consumptionFieldNoun names a required field in its refusal.
var consumptionFieldNoun = map[string]string{
	"start":       "each reading's start",
	"quantityKwh": "each reading's quantity in kWh",
}

// consumptionImportAliases are the header words that mean a field (design
// D8), matched after fileimport.HeaderKey, so "Fra og med", "fra-og-med" and
// "FRAOGMED" are one word. A field's own JSON name matches too.
var consumptionImportAliases = map[string][]string{
	"start":       {"fra", "fra og med", "start", "from", "starttid"},
	"end":         {"til", "til og med", "end", "to", "slutt"},
	"quantityKwh": {"kwh", "forbruk", "volum", "quantity", "mengde", "forbruk kwh", "quantityKwh"},
	"quality":     {"kvalitet", "quality"},
	"gsrn":        {"målepunkt", "målepunktid", "gsrn", "meteringpointid"},
}

// consumptionAliasIndex is consumptionImportAliases inverted, under each
// word's fileimport.HeaderKey.
var consumptionAliasIndex = func() map[string]string {
	index := map[string]string{}
	for field, aliases := range consumptionImportAliases {
		for _, alias := range aliases {
			index[fileimport.HeaderKey(alias)] = field
		}
	}
	return index
}()

// suggestConsumptionMapping is the mapping the modal starts from: each field
// from the first column whose header names it, and PT1H when no column names
// an end — many exports give one timestamp per line.
func suggestConsumptionMapping(header []string) gen.ConsumptionImportMapping {
	var m gen.ConsumptionImportMapping
	fields := map[string]**int32{"start": &m.Start, "end": &m.End, "quantityKwh": &m.QuantityKwh, "quality": &m.Quality, "gsrn": &m.Gsrn}
	for i, h := range header {
		field, ok := consumptionAliasIndex[fileimport.HeaderKey(h)]
		if !ok || *fields[field] != nil {
			continue
		}
		column := int32(i)
		*fields[field] = &column
	}
	if m.End == nil {
		resolution := gen.PT1H
		m.Resolution = &resolution
	}
	return m
}

// The timestamp layouts a cell is read with (design D8): ISO 8601 with T or a
// space, with or without seconds, and the Norwegian dd.mm.yyyy and the
// dd/mm/yyyy forms, a one-digit day, month or hour accepted. A fraction of a
// second after the seconds is accepted by time.Parse itself. Those with an
// offset are an instant; the others a clock time in the market time zone.
var (
	consumptionOffsetLayouts = []string{
		"2006-01-02T15:04:05Z07:00", "2006-01-02T15:04Z07:00", "2006-01-02 15:04:05Z07:00", "2006-01-02 15:04Z07:00",
		"2006-01-02T15:04:05Z0700", "2006-01-02T15:04Z0700", "2006-01-02 15:04:05Z0700", "2006-01-02 15:04Z0700",
	}
	consumptionLocalLayouts = []string{
		"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04",
		"2.1.2006 15:04:05", "2.1.2006 15:04", "2/1/2006 15:04:05", "2/1/2006 15:04",
	}
)

// consumptionTimestamp is a timestamp cell read: an instant when the cell
// carried an offset, otherwise the clock time it names (as a UTC time.Time
// whose fields are the clock's) for the caller to place in the market zone.
type consumptionTimestamp struct {
	instant time.Time
	wall    time.Time
	local   bool
}

// parseConsumptionTimestamp reads one timestamp cell; ok is false for a cell
// no layout reads.
func parseConsumptionTimestamp(cell string) (consumptionTimestamp, bool) {
	cell = strings.TrimSpace(cell)
	for _, layout := range consumptionOffsetLayouts {
		if t, err := time.Parse(layout, cell); err == nil {
			return consumptionTimestamp{instant: t.UTC()}, true
		}
	}
	for _, layout := range consumptionLocalLayouts {
		if t, err := time.Parse(layout, cell); err == nil {
			return consumptionTimestamp{wall: t, local: true}, true
		}
	}
	return consumptionTimestamp{}, false
}

// localInstants are the instants, ascending, at which a clock in loc reads
// wall: none for a time the clocks skip in spring, two for one they repeat in
// autumn. asEnd reads wall as the moment an interval closes, so an instant
// also counts when the clock read wall just before it — 03:00 on the autumn
// day closes both the first and the second 02:00 hour.
func localInstants(wall time.Time, loc *time.Location, asEnd bool) []time.Time {
	offsets := []int{}
	for _, probe := range []time.Time{wall.Add(-26 * time.Hour), wall, wall.Add(26 * time.Hour)} {
		if _, off := probe.In(loc).Zone(); !slices.Contains(offsets, off) {
			offsets = append(offsets, off)
		}
	}
	var instants []time.Time
	for _, off := range offsets {
		instant := wall.Add(-time.Duration(off) * time.Second)
		_, at := instant.In(loc).Zone()
		_, before := instant.Add(-time.Nanosecond).In(loc).Zone()
		if (at == off || (asEnd && before == off)) && !slices.ContainsFunc(instants, instant.Equal) {
			instants = append(instants, instant)
		}
	}
	slices.SortFunc(instants, func(a, b time.Time) int { return a.Compare(b) })
	return instants
}

// consumptionQuantityPattern is a quantity cell once its spaces are gone and a
// decimal comma is a point: digits, optionally a fraction, optionally a minus
// (refused afterwards with the manual reading's own message).
var consumptionQuantityPattern = regexp.MustCompile(`^-?(\d+(\.\d*)?|\.\d+)$`)

// parseConsumptionQuantity reads a quantity cell (design D8): a decimal comma
// or point, spaces (a thousands separator) ignored, no exponent; the value
// rounded to three decimals as the column stores it. The message is "" on
// success.
func parseConsumptionQuantity(cell string) (float64, string) {
	text := strings.Map(func(r rune) rune {
		if r == ' ' || r == ' ' || r == ' ' {
			return -1
		}
		return r
	}, strings.TrimSpace(cell))
	if text == "" {
		return 0, "A quantity is required"
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
	if err != nil {
		return 0, fmt.Sprintf("'%s' is not a number", cell)
	}
	v = roundKwh(v)
	if v < 0 {
		return 0, "Quantity must be zero or greater."
	}
	if v >= consumptionQuantityLimit {
		return 0, fmt.Sprintf("Quantity must be less than %.0f.", consumptionQuantityLimit)
	}
	return v, ""
}

// consumptionRow is a row of the file that passed every check: its interval in
// UTC, its quantity as stored and its quality's canonical name.
type consumptionRow struct {
	row        int
	start, end time.Time
	quantity   float64
	quality    string
}

// consumptionEvaluation is a file's rows checked: the rows without an error,
// in file order, every problem counted and the first ones kept.
type consumptionEvaluation struct {
	rows       []consumptionRow
	errors     []gen.ConsumptionImportError
	errorCount int
}

func (e *consumptionEvaluation) fail(row, column int, message string) {
	e.errorCount++
	if len(e.errors) >= consumptionImportMaxErrors {
		return
	}
	item := gen.ConsumptionImportError{Row: int32(row), Message: message}
	if column >= 0 {
		c := int32(column)
		item.Column = &c
	}
	e.errors = append(e.errors, item)
}

// evaluateConsumptionFile checks every row of a file under l for the metering
// point gsrn whose market time zone is loc (design D8): each mapped cell read,
// a local start the clocks repeat taken as the earlier instant the first time
// the file has that clock time and the later one after, a local end as the
// earliest of its instants after the start, the interval held to the manual
// reading's rules, and a second row with one interval refused. Nothing here
// asks the database.
func evaluateConsumptionFile(file fileimport.File, l consumptionLayout, gsrn string, loc *time.Location) consumptionEvaluation {
	var e consumptionEvaluation
	zone := loc.String()
	repeated := map[time.Time]int{}
	seen := map[[2]time.Time]int{}
	for _, record := range file.Rows {
		cell := func(column int) string {
			if column < 0 || column >= len(record.Cells) {
				return ""
			}
			return strings.TrimSpace(record.Cells[column])
		}
		before := e.errorCount

		start, startOK := consumptionStart(&e, record.Row, l.start, cell(l.start), loc, zone, repeated)
		var end time.Time
		endOK := false
		if l.step > 0 {
			end, endOK = start.Add(l.step), startOK
		} else {
			end, endOK = consumptionEnd(&e, record.Row, l.end, cell(l.end), start, startOK, loc, zone)
		}
		quantity, message := parseConsumptionQuantity(cell(l.quantity))
		if message != "" {
			e.fail(record.Row, l.quantity, message)
		}
		quality := "Measured"
		if l.quality >= 0 {
			if q, ok := parseConsumptionQuality(cell(l.quality)); ok {
				quality = q
			} else {
				e.fail(record.Row, l.quality, fmt.Sprintf("'%s' is not a quality; use Measured, Estimated, Corrected or Manual", cell(l.quality)))
			}
		}
		if l.gsrn >= 0 && cell(l.gsrn) != gsrn {
			e.fail(record.Row, l.gsrn, fmt.Sprintf("The row is for metering point '%s', not this one (%s)", cell(l.gsrn), gsrn))
		}
		if startOK && endOK && message == "" {
			// The manual reading's own rules; the quantity's is already
			// checked above, against its column, so what is left is the end.
			if msg := validateConsumptionInterval(start, end, quantity); msg != "" {
				e.fail(record.Row, l.end, msg)
			}
		}
		if e.errorCount > before {
			continue
		}
		key := [2]time.Time{start, end}
		if first, ok := seen[key]; ok {
			e.fail(record.Row, -1, fmt.Sprintf("The same interval as row %d", first))
			continue
		}
		seen[key] = record.Row
		e.rows = append(e.rows, consumptionRow{row: record.Row, start: start, end: end, quantity: quantity, quality: quality})
	}
	return e
}

// consumptionStart reads a row's start: an instant as written, or a clock time
// in loc — the row's error when the clocks skip it, and when they repeat it
// the earlier instant the first time the file has it and the later one after.
func consumptionStart(e *consumptionEvaluation, row, column int, cell string, loc *time.Location, zone string,
	repeated map[time.Time]int,
) (time.Time, bool) {
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

// consumptionEnd reads a row's end column: an instant as written, or a clock
// time in loc read as the moment the interval closes — the row's error when
// the clocks skip it, and the earliest of its instants after the start when
// they repeat it.
func consumptionEnd(e *consumptionEvaluation, row, column int, cell string, start time.Time, startOK bool,
	loc *time.Location, zone string,
) (time.Time, bool) {
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

func consumptionTimestampRefusal(cell string) string {
	return fmt.Sprintf("'%s' is not a date and time; write it as 2025-01-31 13:00, 31.01.2025 13:00, or with an offset as 2025-01-31T13:00+01:00", cell)
}

func consumptionSkippedRefusal(cell, zone string) string {
	return fmt.Sprintf("%s is not a time in %s: the clocks skip it when summer time begins", cell, zone)
}

// consumptionReplacement is a row that replaces a current interval: the
// interval's id and start, which the supersede chain points at.
type consumptionReplacement struct {
	row   consumptionRow
	id    int64
	start time.Time
}

// consumptionPlan is what a checked file would do to the point's current
// intervals (design D8): the rows to insert, those of them that replace an
// interval, and how many repeat one exactly.
type consumptionPlan struct {
	news      []consumptionRow
	replacing []consumptionReplacement
	unchanged int
}

// planConsumption compares rows with the point's current intervals: an
// interval with the row's exact start and end and the same quantity and
// quality leaves the row unchanged, one with another quantity or quality is
// replaced, and a row no interval matches is new. Overlapping intervals that
// are not equal are not compared, as with a manual reading.
func planConsumption(ctx context.Context, q *store.Queries, pointID int32, rows []consumptionRow) (consumptionPlan, error) {
	var plan consumptionPlan
	if len(rows) == 0 {
		return plan, nil
	}
	from, to := rows[0].start, rows[0].start
	for _, r := range rows[1:] {
		if r.start.Before(from) {
			from = r.start
		}
		if r.start.After(to) {
			to = r.start
		}
	}
	current, err := q.ListCurrentConsumptionForImport(ctx, store.ListCurrentConsumptionForImportParams{
		MeteringPointID: pointID, FromTs: from, ToTs: to,
	})
	if err != nil {
		return plan, fmt.Errorf("energy: read the current consumption: %w", err)
	}
	byInterval := make(map[[2]time.Time]store.ListCurrentConsumptionForImportRow, len(current))
	for _, c := range current {
		byInterval[[2]time.Time{c.Start.UTC(), c.End.UTC()}] = c
	}
	for _, r := range rows {
		c, ok := byInterval[[2]time.Time{r.start, r.end}]
		switch {
		case !ok:
			plan.news = append(plan.news, r)
		case floatFromNumeric(c.QuantityKwh) == r.quantity && c.Quality == r.quality:
			plan.unchanged++
		default:
			plan.replacing = append(plan.replacing, consumptionReplacement{row: r, id: c.ID, start: c.Start})
		}
	}
	return plan, nil
}

// writeConsumption writes a plan in tx: the partitions of every month a row
// starts in, the replaced intervals no longer current, then every row copied
// in with source Import, a replacing row pointing at the interval it replaces.
func writeConsumption(ctx context.Context, tx pgx.Tx, pointID int32, plan consumptionPlan, now time.Time) error {
	inserts := len(plan.news) + len(plan.replacing)
	if inserts == 0 {
		return nil
	}
	q := store.New(tx)
	starts := make([]time.Time, 0, inserts)
	for _, r := range plan.news {
		starts = append(starts, r.start)
	}
	ids := make([]int64, 0, len(plan.replacing))
	replacedStarts := make([]time.Time, 0, len(plan.replacing))
	for _, r := range plan.replacing {
		starts = append(starts, r.row.start)
		ids = append(ids, r.id)
		replacedStarts = append(replacedStarts, r.start)
	}
	if err := q.EnsureConsumptionPartitions(ctx, starts); err != nil {
		return fmt.Errorf("energy: create the consumption partitions: %w", err)
	}
	if len(ids) > 0 {
		if err := q.SetConsumptionIntervalsNotCurrent(ctx, store.SetConsumptionIntervalsNotCurrentParams{Ids: ids, Starts: replacedStarts}); err != nil {
			return fmt.Errorf("energy: supersede the replaced consumption: %w", err)
		}
	}
	row := func(r consumptionRow, supersedesID *int64, supersedesStart *time.Time) ([]any, error) {
		quantity, err := numericFromFloat(r.quantity)
		if err != nil {
			return nil, err
		}
		return []any{pointID, r.start, r.end, quantity, r.quality, consumptionSourceImport, now, true, supersedesID, supersedesStart}, nil
	}
	_, err := tx.CopyFrom(ctx, pgx.Identifier{"energy", "consumption_intervals"},
		[]string{"metering_point_id", "start", "end", "quantity_kwh", "quality", "source", "received_at", "is_current", "supersedes_id", "supersedes_start"},
		pgx.CopyFromSlice(inserts, func(i int) ([]any, error) {
			if i < len(plan.news) {
				return row(plan.news[i], nil, nil)
			}
			r := plan.replacing[i-len(plan.news)]
			return row(r.row, &r.id, &r.start)
		}))
	if err != nil {
		return fmt.Errorf("energy: write the imported consumption: %w", err)
	}
	return nil
}

// errConsumptionPointGone is a point deleted between the request's first read
// and its transaction: the answer is the 404 the first read would have given.
var errConsumptionPointGone = errors.New("energy: the metering point is gone")

// PostEnergyMeteringPointsByIdConsumptionImport Import consumption readings
// from a file (POST /api/v1/energy/metering-points/{id}/consumption/import)
//
// Design D8: the point (404), the parts, the file read; without a mapping the
// file's facts only. With one, every row checked; a dry run answers what a run
// would do, a run writes it in one transaction under the point's lock — the
// rows checked and compared again inside it — or answers 422 and writes
// nothing when any row has an error.
func (s *server) PostEnergyMeteringPointsByIdConsumptionImport(ctx context.Context, req gen.PostEnergyMeteringPointsByIdConsumptionImportRequestObject) (gen.PostEnergyMeteringPointsByIdConsumptionImportResponseObject, error) {
	dryRun := req.Params.DryRun == nil || *req.Params.DryRun
	point, err := store.New(s.deps.Pool).GetMeteringPointByID(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostEnergyMeteringPointsByIdConsumptionImport404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("energy: get metering point: %w", err)
	}

	data, rawMapping, hasMapping, partErrs := consumptionImportParts(req.Body)
	if len(partErrs) > 0 {
		return gen.PostEnergyMeteringPointsByIdConsumptionImport400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem(invalidImportFileTitle, partErrs)), nil
	}
	file, refusal := fileimport.Read(data, consumptionImportLimits)
	if refusal != "" {
		return gen.PostEnergyMeteringPointsByIdConsumptionImport400ApplicationProblemPlusJSONResponse(invalidImportFile(refusal)), nil
	}
	result := consumptionImportResult(file, dryRun)
	if !hasMapping {
		if !dryRun {
			return gen.PostEnergyMeteringPointsByIdConsumptionImport400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(
				invalidConsumptionImportMappingTitle, map[string][]string{"mapping": {consumptionImportMappingMissingMessage}})), nil
		}
		return gen.PostEnergyMeteringPointsByIdConsumptionImport200JSONResponse(result), nil
	}
	layout, mappingErrs := parseConsumptionMapping(rawMapping, len(file.Header))
	if len(mappingErrs) > 0 {
		return gen.PostEnergyMeteringPointsByIdConsumptionImport400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem(invalidConsumptionImportMappingTitle, mappingErrs)), nil
	}
	result.Evaluated = true

	evaluate := func(ctx context.Context, q *store.Queries, gsrn, priceArea string) (consumptionPlan, bool, error) {
		loc, err := time.LoadLocation(marketTimeZone(&priceArea))
		if err != nil {
			return consumptionPlan{}, false, fmt.Errorf("energy: load the market time zone: %w", err)
		}
		e := evaluateConsumptionFile(file, layout, gsrn, loc)
		result.ErrorCount, result.Errors = int32(e.errorCount), e.errors
		if result.Errors == nil {
			result.Errors = []gen.ConsumptionImportError{}
		}
		for _, r := range e.rows {
			if result.RangeStart == nil || r.start.Before(*result.RangeStart) {
				result.RangeStart = &r.start
			}
			if result.RangeEnd == nil || r.end.After(*result.RangeEnd) {
				result.RangeEnd = &r.end
			}
		}
		plan, err := planConsumption(ctx, q, req.Id, e.rows)
		if err != nil {
			return consumptionPlan{}, false, err
		}
		result.New, result.Replacing, result.Unchanged = int32(len(plan.news)), int32(len(plan.replacing)), int32(plan.unchanged)
		return plan, e.errorCount == 0, nil
	}

	if dryRun {
		if _, _, err := evaluate(ctx, store.New(s.deps.Pool), point.Gsrn, point.PriceArea); err != nil {
			return nil, err
		}
		return gen.PostEnergyMeteringPointsByIdConsumptionImport200JSONResponse(result), nil
	}

	clean := false
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		locked, err := q.LockMeteringPointForConsumptionImport(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return errConsumptionPointGone
		}
		if err != nil {
			return fmt.Errorf("energy: lock the metering point: %w", err)
		}
		plan, ok, err := evaluate(ctx, q, locked.Gsrn, locked.PriceArea)
		if err != nil || !ok {
			return err
		}
		clean = true
		return writeConsumption(ctx, tx, req.Id, plan, s.deps.Clock())
	})
	switch {
	case errors.Is(err, errConsumptionPointGone):
		return gen.PostEnergyMeteringPointsByIdConsumptionImport404Response{}, nil
	case err != nil:
		return nil, fmt.Errorf("energy: import consumption: %w", err)
	case !clean:
		return gen.PostEnergyMeteringPointsByIdConsumptionImport422JSONResponse(result), nil
	}
	result.Imported = true
	return gen.PostEnergyMeteringPointsByIdConsumptionImport200JSONResponse(result), nil
}

// consumptionImportResult is the answer's file half: how the file was read,
// its header, its first rows and the suggested mapping, every count zero.
func consumptionImportResult(file fileimport.File, dryRun bool) gen.ConsumptionImportResult {
	sample := make([]gen.ConsumptionImportSampleRow, 0, consumptionImportSampleRows)
	for _, r := range file.Rows[:min(len(file.Rows), consumptionImportSampleRows)] {
		sample = append(sample, gen.ConsumptionImportSampleRow{Row: int32(r.Row), Cells: r.Cells})
	}
	return gen.ConsumptionImportResult{
		DryRun: dryRun, Encoding: file.Encoding, Separator: string(file.Separator), Header: file.Header,
		Sample: sample, Rows: int32(len(file.Rows)), Suggested: suggestConsumptionMapping(file.Header),
		Errors: []gen.ConsumptionImportError{},
	}
}
