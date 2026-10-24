package energy_test

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the spot price import (energy consumption billing design D2):
// day-ahead prices from a file in the fixed layout its header names, checked
// row by row and written all or nothing.

// spotPricePermissions is what a person who imports prices holds.
var spotPricePermissions = []string{"energy:prices-manage", "energy:prices-view"}

type spotPriceImportJSON struct {
	DryRun     bool                         `json:"dryRun"`
	Imported   bool                         `json:"imported"`
	Rows       int                          `json:"rows"`
	ErrorCount int                          `json:"errorCount"`
	Errors     []consumptionImportErrorJSON `json:"errors"`
	Areas      []spotPriceImportAreaJSON    `json:"areas"`
}

type spotPriceImportAreaJSON struct {
	PriceArea  string     `json:"priceArea"`
	Inserted   int        `json:"inserted"`
	Replaced   int        `json:"replaced"`
	Unchanged  int        `json:"unchanged"`
	RangeStart *time.Time `json:"rangeStart"`
	RangeEnd   *time.Time `json:"rangeEnd"`
}

// spotPriceFile is a file of testdata/spot_prices.
func spotPriceFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "spot_prices", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return data
}

// postSpotPriceImport POSTs data as the file part; dryRun "" leaves the
// parameter out.
func postSpotPriceImport(t *testing.T, c *modtest.Client, data []byte, dryRun string) *modtest.Response {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "prices.csv")
	if err != nil {
		t.Fatalf("create the file part: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write the file part: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close the multipart writer: %v", err)
	}
	path := "/api/v1/energy/spot-prices/import"
	if dryRun != "" {
		path += "?dryRun=" + dryRun
	}
	return c.Do(http.MethodPost, path, nil, modtest.RawBody(w.FormDataContentType(), buf.Bytes()))
}

func spotPriceImportOf(t *testing.T, r *modtest.Response, want int) spotPriceImportJSON {
	t.Helper()
	if r.Status != want {
		t.Fatalf("status %d body %s, want %d", r.Status, r.Body, want)
	}
	var result spotPriceImportJSON
	r.JSON(&result)
	return result
}

// importSpotPrices runs a file for real and wants it written.
func importSpotPrices(t *testing.T, c *modtest.Client, data []byte) spotPriceImportJSON {
	t.Helper()
	result := spotPriceImportOf(t, postSpotPriceImport(t, c, data, "false"), http.StatusOK)
	if !result.Imported || result.DryRun || result.ErrorCount != 0 {
		t.Fatalf("imported/dryRun/errorCount = %v/%v/%d: %+v", result.Imported, result.DryRun, result.ErrorCount, result)
	}
	return result
}

// spotPriceErrors are a result's errors as "row/column: message" lines.
func spotPriceErrors(result spotPriceImportJSON) string {
	out := []string{}
	for _, e := range result.Errors {
		column := "-"
		if e.Column != nil {
			column = fmt.Sprint(*e.Column)
		}
		out = append(out, fmt.Sprintf("%d/%s: %s", e.Row, column, e.Message))
	}
	return strings.Join(out, "\n")
}

// wantArea checks one area of a result: its counts and its range.
func wantArea(t *testing.T, got spotPriceImportAreaJSON, area string, inserted, replaced, unchanged int, from, to time.Time) {
	t.Helper()
	if got.PriceArea != area || got.Inserted != inserted || got.Replaced != replaced || got.Unchanged != unchanged {
		t.Errorf("area = %s %d/%d/%d, want %s %d/%d/%d", got.PriceArea, got.Inserted, got.Replaced, got.Unchanged, area, inserted, replaced, unchanged)
	}
	if got.RangeStart == nil || !got.RangeStart.Equal(from) || got.RangeEnd == nil || !got.RangeEnd.Equal(to) {
		t.Errorf("%s range = %v to %v, want %s to %s", area, got.RangeStart, got.RangeEnd, from, to)
	}
}

func spotPriceCount(t *testing.T, h *modtest.Harness, area string) int {
	t.Helper()
	return h.Count(t, `SELECT count(*) FROM energy.spot_prices WHERE price_area = $1`, area)
}

func TestSpotPriceImport_DryRunChecksAndWritesNothingThenARunWrites(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, spotPricePermissions...)
	data := spotPriceFile(t, "no1_hourly.csv")
	from, to := utc(2026, time.January, 14, 23), utc(2026, time.January, 15, 23)

	// dryRun is true when absent.
	dry := spotPriceImportOf(t, postSpotPriceImport(t, c, data, ""), http.StatusOK)
	if !dry.DryRun || dry.Imported || dry.Rows != 24 || dry.ErrorCount != 0 || len(dry.Errors) != 0 || len(dry.Areas) != 1 {
		t.Fatalf("dry run = %+v, want dryRun, 24 rows, no error, one area", dry)
	}
	wantArea(t, dry.Areas[0], "NO1", 24, 0, 0, from, to)
	if n := spotPriceCount(t, h, "NO1"); n != 0 {
		t.Fatalf("a dry run wrote %d prices", n)
	}

	run := importSpotPrices(t, c, data)
	wantArea(t, run.Areas[0], "NO1", 24, 0, 0, from, to)
	if n := h.Count(t, `SELECT count(*) FROM energy.spot_prices
WHERE price_area = 'NO1' AND source = 'Import' AND received_at = $1 AND "end" - "start" = interval '1 hour'`, h.Now()); n != 24 {
		t.Errorf("%d hourly NO1 prices imported now, want 24", n)
	}
}

func TestSpotPriceImport_AQuarterHourFileWithoutAnEndColumnClosesEachRowAtTheNext(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, spotPricePermissions...)

	// The last row takes the quarter the rows before it are, so the day ends
	// at its own midnight, not 00:45.
	result := importSpotPrices(t, c, spotPriceFile(t, "no2_quarter_no_end.csv"))
	if result.Rows != 96 || len(result.Areas) != 1 {
		t.Fatalf("result = %+v, want 96 rows in one area", result)
	}
	wantArea(t, result.Areas[0], "NO2", 96, 0, 0, utc(2026, time.January, 14, 23), utc(2026, time.January, 15, 23))
	if n := h.Count(t, `SELECT count(*) FROM energy.spot_prices WHERE price_area = 'NO2' AND "end" - "start" = interval '15 minutes'`); n != 96 {
		t.Errorf("%d quarter-hour NO2 prices, want 96", n)
	}

	// A single row without an end is an hour.
	single := importSpotPrices(t, c, fileOf([]string{"sone", "fra", "pris"}, []string{"NO2", "2026-01-20 12:00", "0,5"}))
	wantArea(t, single.Areas[0], "NO2", 1, 0, 0, utc(2026, time.January, 20, 11), utc(2026, time.January, 20, 12))
}

func TestSpotPriceImport_ASecondImportCountsInsertedReplacedAndUnchanged(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, spotPricePermissions...)
	original := spotPriceFile(t, "no1_hourly.csv")
	importSpotPrices(t, c, original)
	firstImport := h.Now()
	h.Advance(time.Hour)

	// One figure changed (07:00, 0,586415 → 0,9) and a row added for the
	// next day's first hour.
	changed := strings.Replace(string(original), "NO1;2026-01-15 07:00;2026-01-15 08:00;0,586415;", "NO1;2026-01-15 07:00;2026-01-15 08:00;0,9;", 1)
	if changed == string(original) {
		t.Fatal("the 07:00 row was not found to change")
	}
	changed += "NO1;2026-01-16 00:00;2026-01-16 01:00;0,25;;\r\n"

	dry := spotPriceImportOf(t, postSpotPriceImport(t, c, []byte(changed), "true"), http.StatusOK)
	wantArea(t, dry.Areas[0], "NO1", 1, 1, 23, utc(2026, time.January, 14, 23), utc(2026, time.January, 16, 0))
	run := importSpotPrices(t, c, []byte(changed))
	wantArea(t, run.Areas[0], "NO1", 1, 1, 23, utc(2026, time.January, 14, 23), utc(2026, time.January, 16, 0))

	if n := spotPriceCount(t, h, "NO1"); n != 25 {
		t.Errorf("%d NO1 prices, want 25", n)
	}
	// The replaced row carries the new figure and this import's time; the
	// unchanged ones keep the first import's.
	if n := h.Count(t, `SELECT count(*) FROM energy.spot_prices WHERE price_area = 'NO1' AND "start" = $1 AND nok_per_kwh = 0.9 AND received_at = $2`,
		utc(2026, time.January, 15, 6), h.Now()); n != 1 {
		t.Errorf("the 07:00 price was not replaced with 0.9 at %s", h.Now())
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.spot_prices WHERE price_area = 'NO1' AND received_at = $1`, firstImport); n != 23 {
		t.Errorf("%d prices kept the first import's time, want 23", n)
	}
}

func TestSpotPriceImport_AnIntervalOverlappingAStoredOneOfAnotherLengthIsTheRowsError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, spotPricePermissions...)
	// NO3 holds 2026-01-16 00:00–01:00 as one hour.
	importSpotPrices(t, c, fileOf([]string{"area", "start", "end", "price"}, []string{"NO3", "2026-01-16 00:00", "2026-01-16 01:00", "0,5"}))

	data := spotPriceFile(t, "mixed_overlap.csv")
	wantErrors := strings.Join([]string{
		"5/-: The interval 2026-01-16 00:00 +01:00 to 2026-01-16 00:15 +01:00 overlaps the stored price of NO3 from 2026-01-16 00:00 +01:00 to 2026-01-16 01:00 +01:00, an interval of another length; prices of an area never overlap",
		"6/-: The interval 2026-01-16 00:15 +01:00 to 2026-01-16 00:30 +01:00 overlaps the stored price of NO3 from 2026-01-16 00:00 +01:00 to 2026-01-16 01:00 +01:00, an interval of another length; prices of an area never overlap",
		"7/-: The interval 2026-01-16 00:30 +01:00 to 2026-01-16 00:45 +01:00 overlaps the stored price of NO3 from 2026-01-16 00:00 +01:00 to 2026-01-16 01:00 +01:00, an interval of another length; prices of an area never overlap",
		"8/-: The interval 2026-01-16 00:45 +01:00 to 2026-01-16 01:00 +01:00 overlaps the stored price of NO3 from 2026-01-16 00:00 +01:00 to 2026-01-16 01:00 +01:00, an interval of another length; prices of an area never overlap",
	}, "\n")
	check := func(result spotPriceImportJSON) {
		t.Helper()
		if got := spotPriceErrors(result); got != wantErrors || result.ErrorCount != 4 {
			t.Errorf("errors (%d) =\n%s\nwant\n%s", result.ErrorCount, got, wantErrors)
		}
		if result.Imported || result.Rows != 8 || len(result.Areas) != 2 {
			t.Fatalf("result = %+v, want not imported, 8 rows, two areas", result)
		}
		// Areas in order; NO3's rows all conflict, so it counts nothing and
		// has no range.
		if a := result.Areas[0]; a.PriceArea != "NO3" || a.Inserted+a.Replaced+a.Unchanged != 0 || a.RangeStart != nil {
			t.Errorf("NO3 = %+v, want no counts and no range", a)
		}
		wantArea(t, result.Areas[1], "NO4", 4, 0, 0, utc(2026, time.January, 15, 23), utc(2026, time.January, 16, 3))
	}

	check(spotPriceImportOf(t, postSpotPriceImport(t, c, data, "true"), http.StatusOK))
	// A run answers 422 with the same body, and writes neither area: NO4's
	// clean rows wait for NO3's.
	run := spotPriceImportOf(t, postSpotPriceImport(t, c, data, "false"), http.StatusUnprocessableEntity)
	if run.DryRun {
		t.Error("a run answered dryRun = true")
	}
	check(run)
	if n, m := spotPriceCount(t, h, "NO4"), spotPriceCount(t, h, "NO3"); n != 0 || m != 1 {
		t.Errorf("NO4 %d, NO3 %d prices after a refused run, want 0 and 1", n, m)
	}
}

func TestSpotPriceImport_AFileOfSeveralAreasIsWrittenInOneTransaction(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, spotPricePermissions...)

	// Both areas, interleaved, on the autumn night: each area's own 02:00
	// is the earlier hour the first time and the later one after.
	rows := [][]string{{"Prisområde", "Fra", "NOK/kWh"}}
	for _, clock := range []string{"00", "01", "02", "02", "03"} {
		rows = append(rows, []string{"NO1", "25.10.2026 " + clock + ":00", "1"}, []string{"NO5", "25.10.2026 " + clock + ":00", "2"})
	}
	result := importSpotPrices(t, c, fileOf(rows...))
	if len(result.Areas) != 2 {
		t.Fatalf("areas = %+v, want NO1 and NO5", result.Areas)
	}
	from, to := time.Date(2026, time.October, 24, 22, 0, 0, 0, time.UTC), time.Date(2026, time.October, 25, 3, 0, 0, 0, time.UTC)
	wantArea(t, result.Areas[0], "NO1", 5, 0, 0, from, to)
	wantArea(t, result.Areas[1], "NO5", 5, 0, 0, from, to)
	if n := h.Count(t, `SELECT count(DISTINCT xmin::text) FROM energy.spot_prices`); n != 1 {
		t.Errorf("the prices were written by %d transactions, want 1", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.spot_prices WHERE "end" - "start" = interval '1 hour'`); n != 10 {
		t.Errorf("%d hourly prices, want 10", n)
	}
}

func TestSpotPriceImport_ReadsØreAndNegativePrices(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, spotPricePermissions...)
	result := importSpotPrices(t, c, spotPriceFile(t, "no5_ore.csv"))
	wantArea(t, result.Areas[0], "NO5", 3, 0, 0, utc(2026, time.January, 16, 23), utc(2026, time.January, 17, 2))

	got := spotPricesOf(t, c, "NO5", utc(2026, time.January, 16, 0), utc(2026, time.January, 18, 0))
	want := []float64{0.4567, -0.015, 1}
	if len(got) != len(want) {
		t.Fatalf("%d prices, want %d", len(got), len(want))
	}
	for i, p := range got {
		if p.NokPerKwh != want[i] || p.EurPerKwh != nil {
			t.Errorf("price %d = %v (eur %v), want %v and no EUR", i, p.NokPerKwh, p.EurPerKwh, want[i])
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.spot_prices WHERE nok_per_kwh < 0`); n != 1 {
		t.Errorf("%d negative prices stored, want 1", n)
	}
}

func TestSpotPriceImport_RowErrors(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, spotPricePermissions...)

	// Without an end column.
	result := spotPriceImportOf(t, postSpotPriceImport(t, c, fileOf(
		[]string{"area", "start", "price", "eur_per_kwh", "exr"},
		[]string{"NO1", "2026-03-29 00:00", "1", "", ""},
		[]string{"NO1", "2026-03-29 01:00", "1", "0,1", "0"},
		[]string{"NO1", "2026-03-29 02:30", "1", "", ""},
		[]string{"NO1", "2026-03-29 03:00", "1,2.5", "", ""},
		[]string{"NO1", "2026-03-29 03:00", "1", "", ""},
		[]string{"NO1", "2026-03-29 03:15", "1", "", ""},
		[]string{"NO1", "2026-03-29 05:00", "abc", "", ""},
		[]string{"X1", "2026-03-29 05:00", "1", "", ""},
		[]string{"", "soon", "", "", ""},
		[]string{"NO2", "2026-03-29 05:00", "1", "", ""},
	), "false"), http.StatusUnprocessableEntity)
	want := strings.Join([]string{
		"2/4: An exchange rate must be greater than zero",
		"3/1: 2026-03-29 02:30 is not a time in Europe/Oslo: the clocks skip it when summer time begins",
		"4/2: '1,2.5' is not a number; use either a decimal comma or a decimal point",
		"5/1: The row does not start after row 4 (2026-03-29 03:00 +02:00); the rows of an area must be in time order",
		"6/-: Row 7 starts 1h45m0s after this row, more than the 15m0s between the rows before; a row is missing between them, or give the file an end column",
		"7/2: 'abc' is not a number",
		"8/0: 'X1' is not a price area; write it as two letters and one or two digits, such as NO1",
		"9/0: A price area is required",
		"9/1: 'soon' is not a date and time; write it as 2025-01-31 13:00, 31.01.2025 13:00, or with an offset as 2025-01-31T13:00+01:00",
		"9/2: A price is required",
	}, "\n")
	if got := spotPriceErrors(result); got != want || result.ErrorCount != 10 {
		t.Errorf("errors (%d) =\n%s\nwant\n%s", result.ErrorCount, got, want)
	}
	if result.Imported || result.Rows != 10 {
		t.Errorf("result = %+v, want 10 rows and not imported", result)
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.spot_prices`); n != 0 {
		t.Errorf("a refused run wrote %d prices", n)
	}

	// With an end column: a gap is allowed, an overlap and an unordered
	// row are not, nor an end before the start, nor an interval longer
	// than an hour.
	ends := spotPriceImportOf(t, postSpotPriceImport(t, c, fileOf(
		[]string{"area", "start", "end", "price"},
		[]string{"NO1", "2026-01-10 00:00", "2026-01-10 01:00", "1"},
		[]string{"NO1", "2026-01-12 00:00", "2026-01-12 01:00", "1"},
		[]string{"NO1", "2026-01-12 00:30", "2026-01-12 01:30", "1"},
		[]string{"NO1", "2026-01-11 00:00", "2026-01-11 01:00", "1"},
		[]string{"NO1", "2026-01-13 01:00", "2026-01-13 00:00", "1"},
		[]string{"NO2", "2026-01-01 00:00", "2027-01-01 00:00", "1"},
	), "true"), http.StatusOK)
	want = strings.Join([]string{
		"3/-: The row overlaps row 2 (2026-01-12 00:00 +01:00 to 2026-01-12 01:00 +01:00)",
		"4/-: The row starts before row 2; the rows of an area must be in time order",
		"5/2: The end must be after the start",
		"6/2: An interval may be at most an hour long",
	}, "\n")
	if got := spotPriceErrors(ends); got != want {
		t.Errorf("errors =\n%s\nwant\n%s", got, want)
	}
	wantArea(t, ends.Areas[0], "NO1", 2, 0, 0, utc(2026, time.January, 9, 23), utc(2026, time.January, 12, 0))
	if a := ends.Areas[1]; a.PriceArea != "NO2" || a.Inserted != 0 || a.RangeStart != nil {
		t.Errorf("NO2 = %+v, want its one row refused, nothing counted", a)
	}
}

func TestSpotPriceImport_AFigureTheColumnCannotHoldIsTheRowsError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, spotPricePermissions...)
	result := spotPriceImportOf(t, postSpotPriceImport(t, c, fileOf(
		[]string{"area", "start", "price", "eur_per_kwh"},
		[]string{"NO1", "2026-01-10 00:00", "1000000", ""},
		[]string{"NO1", "2026-01-10 01:00", "999999,9999995", ""},
		[]string{"NO1", "2026-01-10 02:00", "-1000000", ""},
		[]string{"NO1", "2026-01-10 03:00", "999999,999999", "1000000"},
	), "false"), http.StatusUnprocessableEntity)
	want := strings.Join([]string{
		"1/2: A figure must be less than 1000000 in size",
		"2/2: A figure must be less than 1000000 in size",
		"3/2: A figure must be less than 1000000 in size",
		"4/3: A figure must be less than 1000000 in size",
	}, "\n")
	if got := spotPriceErrors(result); got != want || result.ErrorCount != 4 {
		t.Errorf("errors (%d) =\n%s\nwant\n%s", result.ErrorCount, got, want)
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.spot_prices`); n != 0 {
		t.Errorf("a refused run wrote %d prices", n)
	}
}

func TestSpotPriceImport_AnswersTheFirst200ErrorsAndCountsThemAll(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, spotPricePermissions...)
	rows := [][]string{{"area", "start", "price"}}
	start := time.Date(2026, time.January, 10, 0, 0, 0, 0, time.UTC)
	for i := range 250 {
		rows = append(rows, []string{"NO1", start.Add(time.Duration(i) * time.Hour).Format("2006-01-02 15:04"), "abc"})
	}
	result := spotPriceImportOf(t, postSpotPriceImport(t, c, fileOf(rows...), "true"), http.StatusOK)
	if result.ErrorCount != 250 || len(result.Errors) != 200 || result.Rows != 250 {
		t.Fatalf("errorCount %d, %d errors, %d rows; want 250 counted, the first 200 answered, 250 rows", result.ErrorCount, len(result.Errors), result.Rows)
	}
	if first, last := result.Errors[0].Row, result.Errors[199].Row; first != 1 || last != 200 {
		t.Errorf("the errors answered run from row %d to %d, want 1 to 200", first, last)
	}
}

func TestSpotPriceImport_ConcurrentImportsOfOneFileAllSucceed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, spotPricePermissions...)
	data := spotPriceFile(t, "no1_hourly.csv")
	const n = 5
	fns := make([]func() *modtest.Response, n)
	for i := range fns {
		fns[i] = func() *modtest.Response { return postSpotPriceImport(t, c, data, "false") }
	}
	inserted, unchanged := 0, 0
	for i, r := range race(fns...) {
		if r.Status != http.StatusOK {
			t.Errorf("import %d: status %d body %s, want 200", i, r.Status, r.Body)
			continue
		}
		var result spotPriceImportJSON
		r.JSON(&result)
		if !result.Imported || len(result.Areas) != 1 {
			t.Errorf("import %d = %+v, want imported, one area", i, result)
			continue
		}
		switch a := result.Areas[0]; {
		case a.Inserted == 24 && a.Unchanged == 0 && a.Replaced == 0:
			inserted++
		case a.Unchanged == 24 && a.Inserted == 0 && a.Replaced == 0:
			unchanged++
		default:
			t.Errorf("import %d counted %d/%d/%d, want all inserted or all unchanged", i, a.Inserted, a.Replaced, a.Unchanged)
		}
	}
	if inserted != 1 || unchanged != n-1 {
		t.Errorf("%d imports inserted and %d found the file unchanged, want 1 and %d", inserted, unchanged, n-1)
	}
	if got := spotPriceCount(t, h, "NO1"); got != 24 {
		t.Errorf("%d NO1 prices, want 24", got)
	}
}

func TestSpotPriceImport_RefusesAFileItCannotRead(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, spotPricePermissions...)

	r := postSpotPriceImport(t, c, fileOf([]string{"when", "kWh"}, []string{"2026-01-01 00:00", "1"}), "")
	if r.Status != http.StatusBadRequest {
		t.Fatalf("no layout: status %d body %s, want 400", r.Status, r.Body)
	}
	for _, words := range []string{"no column for the price area", "no column for the start", "no column for the NOK price; name one in the header as nok_per_kwh, price, pris, kr/kwh, nok/kwh, øre/kwh, ore_per_kwh"} {
		if !strings.Contains(string(r.Body), words) {
			t.Errorf("body %s does not say %q", r.Body, words)
		}
	}

	r = postSpotPriceImport(t, c, fileOf([]string{"area", "start", "pris", "Øre/kWh"}, []string{"NO1", "2026-01-01 00:00", "1", "100"}), "")
	if r.Status != http.StatusBadRequest || !strings.Contains(string(r.Body), "Columns 2 ('pris') and 3 ('Øre/kWh') both hold the NOK price") {
		t.Errorf("two price columns: status %d body %s, want 400 naming both", r.Status, r.Body)
	}

	r = postSpotPriceImport(t, c, []byte{}, "")
	if r.Status != http.StatusBadRequest || !strings.Contains(string(r.Body), "sent as the multipart part named 'file'") {
		t.Errorf("an empty file: status %d body %s, want 400 on file", r.Status, r.Body)
	}

	// Two parts named file: which one is meant cannot be told.
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, name := range []string{"a.csv", "b.csv"} {
		part, err := w.CreateFormFile("file", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(spotPriceFile(t, "no5_ore.csv")); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r = c.Do(http.MethodPost, "/api/v1/energy/spot-prices/import?dryRun=true", nil, modtest.RawBody(w.FormDataContentType(), buf.Bytes()))
	if r.Status != http.StatusBadRequest || !strings.Contains(string(r.Body), "sent as the multipart part named 'file'") {
		t.Errorf("two file parts: status %d body %s, want 400 on file", r.Status, r.Body)
	}
}

func TestSpotPriceImport_NeedsPricesManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	data := spotPriceFile(t, "no5_ore.csv")
	for _, held := range [][]string{{}, {"energy:prices-view"}, {"energy:prices-manage"}} {
		if r := postSpotPriceImport(t, h.SignIn(t, held...), data, "true"); r.Status != http.StatusForbidden {
			t.Errorf("holding %v: status %d, want 403", held, r.Status)
		}
	}
}
