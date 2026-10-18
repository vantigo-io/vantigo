package energy_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the consumption import (energy import design D8): one metering
// point's readings from a file, read, checked and written by one endpoint.

// consumptionImportPermissions is what a person who imports readings holds.
var consumptionImportPermissions = []string{
	"energy:import", "energy:consumption-manage", "energy:consumption-view",
	"energy:metering-points-manage", "energy:metering-points-view", "energy:meters-manage", "energy:meters-view",
}

type consumptionImportJSON struct {
	DryRun     bool                         `json:"dryRun"`
	Evaluated  bool                         `json:"evaluated"`
	Imported   bool                         `json:"imported"`
	Encoding   string                       `json:"encoding"`
	Separator  string                       `json:"separator"`
	Header     []string                     `json:"header"`
	Sample     []energyImportSampleRowJSON  `json:"sample"`
	Rows       int                          `json:"rows"`
	Suggested  map[string]any               `json:"suggested"`
	New        int                          `json:"new"`
	Replacing  int                          `json:"replacing"`
	Unchanged  int                          `json:"unchanged"`
	ErrorCount int                          `json:"errorCount"`
	Errors     []consumptionImportErrorJSON `json:"errors"`
	RangeStart *time.Time                   `json:"rangeStart"`
	RangeEnd   *time.Time                   `json:"rangeEnd"`
}

type consumptionImportErrorJSON struct {
	Row     int    `json:"row"`
	Column  *int   `json:"column"`
	Message string `json:"message"`
}

// consumptionImportHarness is an installation with one metering point (price
// area NO1, so Europe/Oslo) and a client holding every key the import needs.
func consumptionImportHarness(t *testing.T) (*modtest.Harness, *modtest.Client, meteringPointJSON) {
	t.Helper()
	h := newHarness(t)
	c := h.SignIn(t, consumptionImportPermissions...)
	return h, c, createMeteringPoint(t, c)
}

// postConsumptionImport POSTs data as the file part and, unless mapping is
// nil, the mapping part — a string as it is, anything else as JSON. dryRun ""
// leaves the parameter out.
func postConsumptionImport(t *testing.T, c *modtest.Client, pointID int32, data []byte, mapping any, dryRun string) *modtest.Response {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "readings.csv")
	if err != nil {
		t.Fatalf("create the file part: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write the file part: %v", err)
	}
	if mapping != nil {
		raw, ok := mapping.(string)
		if !ok {
			b, err := json.Marshal(mapping)
			if err != nil {
				t.Fatalf("encode the mapping: %v", err)
			}
			raw = string(b)
		}
		if err := w.WriteField("mapping", raw); err != nil {
			t.Fatalf("write the mapping part: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close the multipart writer: %v", err)
	}
	path := fmt.Sprintf("/api/v1/energy/metering-points/%d/consumption/import", pointID)
	if dryRun != "" {
		path += "?dryRun=" + dryRun
	}
	return c.Do(http.MethodPost, path, nil, modtest.RawBody(w.FormDataContentType(), buf.Bytes()))
}

func consumptionImportOf(t *testing.T, r *modtest.Response, want int) consumptionImportJSON {
	t.Helper()
	if r.Status != want {
		t.Fatalf("status %d body %s, want %d", r.Status, r.Body, want)
	}
	var result consumptionImportJSON
	r.JSON(&result)
	return result
}

// importConsumption runs a file for real and wants it written.
func importConsumption(t *testing.T, c *modtest.Client, pointID int32, data []byte, mapping any) consumptionImportJSON {
	t.Helper()
	result := consumptionImportOf(t, postConsumptionImport(t, c, pointID, data, mapping, "false"), http.StatusOK)
	if !result.Imported {
		t.Fatalf("imported = false: %+v", result)
	}
	return result
}

// currentReadings are the point's current intervals, ordered by start.
func currentReadings(t *testing.T, c *modtest.Client, pointID int32) []consumptionJSON {
	t.Helper()
	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/energy/metering-points/%d/consumption", pointID), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list consumption: status %d body %s", r.Status, r.Body)
	}
	var rows []consumptionJSON
	r.JSON(&rows)
	return rows
}

// startEndQuantity is a mapping with start, end and quantity in columns 0-2.
var startEndQuantity = map[string]any{"start": 0, "end": 1, "quantityKwh": 2}

// errorsOf is a result's errors as "row/column: message" lines, for a test to
// compare at a glance.
func errorsOf(result consumptionImportJSON) []string {
	out := []string{}
	for _, e := range result.Errors {
		column := "-"
		if e.Column != nil {
			column = fmt.Sprint(*e.Column)
		}
		out = append(out, fmt.Sprintf("%d/%s: %s", e.Row, column, e.Message))
	}
	return out
}

func wantErrors(t *testing.T, result consumptionImportJSON, want ...string) {
	t.Helper()
	got := errorsOf(result)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("errors =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if result.ErrorCount != len(want) {
		t.Errorf("errorCount = %d, want %d", result.ErrorCount, len(want))
	}
}

func TestConsumptionImport_WithoutAMappingReadsTheFileAndSuggests(t *testing.T) {
	t.Parallel()
	_, c, point := consumptionImportHarness(t)

	data := fileOf(
		[]string{"Målepunkt", "Fra og med", "Til", "Forbruk (kWh)", "Kvalitet"},
		[]string{point.Gsrn, "01.01.2026 00:00", "01.01.2026 01:00", "1,5", "Målt"},
		[]string{point.Gsrn, "01.01.2026 01:00", "01.01.2026 02:00", "2,5", "Målt"},
	)
	result := consumptionImportOf(t, postConsumptionImport(t, c, point.Id, data, nil, ""), http.StatusOK)
	if result.Evaluated || result.Imported || !result.DryRun {
		t.Errorf("evaluated/imported/dryRun = %v/%v/%v, want false/false/true", result.Evaluated, result.Imported, result.DryRun)
	}
	if result.Rows != 2 || len(result.Sample) != 2 || result.Sample[0].Row != 1 || result.Sample[1].Cells[3] != "2,5" {
		t.Errorf("rows %d, sample %+v", result.Rows, result.Sample)
	}
	if result.Encoding != "utf-8" || result.Separator != ";" || result.Header[1] != "Fra og med" {
		t.Errorf("encoding %q separator %q header %v", result.Encoding, result.Separator, result.Header)
	}
	want := map[string]any{"gsrn": 0.0, "start": 1.0, "end": 2.0, "quality": 4.0}
	for field, column := range want {
		if result.Suggested[field] != column {
			t.Errorf("suggested[%s] = %v, want %v (suggested %v)", field, result.Suggested[field], column, result.Suggested)
		}
	}
	// "Forbruk (kWh)" is forbrukkwh, an alias; no resolution beside an end.
	if result.Suggested["quantityKwh"] != 3.0 || result.Suggested["resolution"] != nil {
		t.Errorf("suggested = %v, want quantityKwh 3 and no resolution", result.Suggested)
	}
	if result.New != 0 || len(currentReadings(t, c, point.Id)) != 0 {
		t.Errorf("a read wrote or counted something: %+v", result)
	}

	// One timestamp per line: the resolution is suggested as an hour.
	hourly := consumptionImportOf(t, postConsumptionImport(t, c, point.Id, fileOf(
		[]string{"Fra", "kWh"}, []string{"01.01.2026 00:00", "1"},
	), nil, ""), http.StatusOK)
	if hourly.Suggested["start"] != 0.0 || hourly.Suggested["quantityKwh"] != 1.0 || hourly.Suggested["resolution"] != "PT1H" || hourly.Suggested["end"] != nil {
		t.Errorf("suggested = %v, want start 0, quantityKwh 1, resolution PT1H", hourly.Suggested)
	}

	// A run needs a mapping.
	r := postConsumptionImport(t, c, point.Id, data, nil, "false")
	if r.Status != http.StatusBadRequest {
		t.Fatalf("a run without a mapping: status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if len(problem.Errors["mapping"]) != 1 {
		t.Errorf("errors = %v, want one on mapping", problem.Errors)
	}
}

func TestConsumptionImport_ReadsOffsetsAndLocalTimesInTheMarketZone(t *testing.T) {
	t.Parallel()
	_, c, point := consumptionImportHarness(t)

	// January: Oslo is UTC+1, so a local 02:00 is 01:00Z.
	header := []string{"start", "end", "kwh"}
	rows := [][]string{
		{"2026-01-05T00:00:00Z", "2026-01-05T01:00:00+01:00", "1"}, // 00:00Z-00:00Z: no interval
		{"2026-01-05 02:00+01:00", "2026-01-05 03:00+0100", "2"},   // 01:00Z-02:00Z
		{"2026-01-05 04:00", "2026-01-05T05:00", "3"},              // local: 03:00Z-04:00Z
		{"05.01.2026 06:00", "05.01.2026 07:00:00", "4"},           // 05:00Z-06:00Z
		{"5/1/2026 8:00", "05/01/2026 09:00", "5"},                 // 07:00Z-08:00Z
		{"2026-01-05T10:00:00.000", "2026-01-05T10:15:00", "6,25"}, // 09:00Z-09:15Z
	}
	result := consumptionImportOf(t, postConsumptionImport(t, c, point.Id, fileOf(append([][]string{header}, rows...)...), startEndQuantity, ""), http.StatusOK)
	// The first row ends where it starts: the manual reading's own rule.
	wantErrors(t, result, "1/1: End must be later than start.")
	if !result.Evaluated || result.New != 5 {
		t.Fatalf("evaluated %v new %d, want true and 5", result.Evaluated, result.New)
	}
	if !result.RangeStart.Equal(utc(2026, time.January, 5, 1)) || !result.RangeEnd.Equal(utc(2026, time.January, 5, 9).Add(15*time.Minute)) {
		t.Errorf("range %v - %v", result.RangeStart, result.RangeEnd)
	}

	fixed := fileOf(append([][]string{header}, rows[1:]...)...)
	importConsumption(t, c, point.Id, fixed, startEndQuantity)
	readings := currentReadings(t, c, point.Id)
	want := []struct {
		start, end time.Time
		kwh        float64
	}{
		{utc(2026, time.January, 5, 1), utc(2026, time.January, 5, 2), 2},
		{utc(2026, time.January, 5, 3), utc(2026, time.January, 5, 4), 3},
		{utc(2026, time.January, 5, 5), utc(2026, time.January, 5, 6), 4},
		{utc(2026, time.January, 5, 7), utc(2026, time.January, 5, 8), 5},
		{utc(2026, time.January, 5, 9), utc(2026, time.January, 5, 9).Add(15 * time.Minute), 6.25},
	}
	if len(readings) != len(want) {
		t.Fatalf("readings = %+v, want %d", readings, len(want))
	}
	for i, w := range want {
		got := readings[i]
		if !got.Start.Equal(w.start) || !got.End.Equal(w.end) || got.QuantityKwh != w.kwh {
			t.Errorf("reading %d = %v-%v %v, want %v-%v %v", i, got.Start, got.End, got.QuantityKwh, w.start, w.end, w.kwh)
		}
		if got.Source != "Import" || got.Quality != "Measured" {
			t.Errorf("reading %d source %q quality %q, want Import and Measured", i, got.Source, got.Quality)
		}
	}
}

func TestConsumptionImport_TheSpringHourTheClocksSkipIsTheRowsError(t *testing.T) {
	t.Parallel()
	_, c, point := consumptionImportHarness(t)

	// 29 March 2026: Oslo's clocks go from 02:00 to 03:00 at 01:00Z.
	data := fileOf(
		[]string{"fra", "kwh"},
		[]string{"29.03.2026 01:00", "1"},
		[]string{"29.03.2026 02:00", "2"},
		[]string{"29.03.2026 02:30", "2"},
		[]string{"29.03.2026 03:00", "3"},
	)
	result := consumptionImportOf(t, postConsumptionImport(t, c, point.Id, data,
		map[string]any{"start": 0, "resolution": "PT1H", "quantityKwh": 1}, ""), http.StatusOK)
	wantErrors(t, result,
		"2/0: 29.03.2026 02:00 is not a time in Europe/Oslo: the clocks skip it when summer time begins",
		"3/0: 29.03.2026 02:30 is not a time in Europe/Oslo: the clocks skip it when summer time begins")
	if result.New != 2 || !result.RangeStart.Equal(utc(2026, time.March, 29, 0)) || !result.RangeEnd.Equal(utc(2026, time.March, 29, 2)) {
		t.Errorf("new %d range %v - %v, want 2 and 00:00Z-02:00Z", result.New, result.RangeStart, result.RangeEnd)
	}

	// With an end column: 01:00-02:00 ends at 01:00Z, and 03:00-04:00 starts
	// there too.
	ends := fileOf(
		[]string{"fra", "til", "kwh"},
		[]string{"29.03.2026 01:00", "29.03.2026 02:00", "1"},
		[]string{"29.03.2026 01:45", "29.03.2026 03:00", "1"},
		[]string{"29.03.2026 03:00", "29.03.2026 04:00", "3"},
	)
	importConsumption(t, c, point.Id, ends, startEndQuantity)
	readings := currentReadings(t, c, point.Id)
	if len(readings) != 3 ||
		!readings[0].Start.Equal(utc(2026, time.March, 29, 0)) || !readings[0].End.Equal(utc(2026, time.March, 29, 1)) ||
		!readings[1].Start.Equal(utc(2026, time.March, 29, 0).Add(45*time.Minute)) || !readings[1].End.Equal(utc(2026, time.March, 29, 1)) ||
		!readings[2].Start.Equal(utc(2026, time.March, 29, 1)) || !readings[2].End.Equal(utc(2026, time.March, 29, 2)) {
		t.Errorf("readings = %+v", readings)
	}
}

func TestConsumptionImport_TheAutumnHourTheClocksRepeatIsReadInFileOrder(t *testing.T) {
	t.Parallel()
	_, c, point := consumptionImportHarness(t)

	// 25 October 2026: Oslo's clocks go from 03:00 back to 02:00 at 01:00Z,
	// so 02:00-03:00 happens twice: 00:00Z-01:00Z, then 01:00Z-02:00Z.
	data := fileOf(
		[]string{"fra", "til", "kwh"},
		[]string{"25.10.2026 01:00", "25.10.2026 02:00", "1"},
		[]string{"25.10.2026 02:00", "25.10.2026 03:00", "2"},
		[]string{"25.10.2026 02:00", "25.10.2026 03:00", "3"},
		[]string{"25.10.2026 03:00", "25.10.2026 04:00", "4"},
	)
	importConsumption(t, c, point.Id, data, startEndQuantity)
	readings := currentReadings(t, c, point.Id)
	want := [][2]time.Time{
		{utc(2026, time.October, 24, 23), utc(2026, time.October, 25, 0)},
		{utc(2026, time.October, 25, 0), utc(2026, time.October, 25, 1)},
		{utc(2026, time.October, 25, 1), utc(2026, time.October, 25, 2)},
		{utc(2026, time.October, 25, 2), utc(2026, time.October, 25, 3)},
	}
	if len(readings) != len(want) {
		t.Fatalf("readings = %+v, want %d", readings, len(want))
	}
	for i, w := range want {
		if !readings[i].Start.Equal(w[0]) || !readings[i].End.Equal(w[1]) || readings[i].QuantityKwh != float64(i+1) {
			t.Errorf("reading %d = %v-%v %v, want %v-%v %d", i, readings[i].Start, readings[i].End, readings[i].QuantityKwh, w[0], w[1], i+1)
		}
	}

	// Quarter-hours with a resolution: the first 02:00-02:45 are summer time,
	// the second ones winter time; a third 02:00 is the second's duplicate.
	_, c2, point2 := consumptionImportHarness(t)
	rows := [][]string{{"fra", "kwh"}}
	for pass := range 2 {
		for minute := 0; minute < 60; minute += 15 {
			rows = append(rows, []string{fmt.Sprintf("25.10.2026 02:%02d", minute), fmt.Sprint(pass + 1)})
		}
	}
	rows = append(rows, []string{"25.10.2026 02:00", "9"})
	result := consumptionImportOf(t, postConsumptionImport(t, c2, point2.Id, fileOf(rows...),
		map[string]any{"start": 0, "resolution": "PT15M", "quantityKwh": 1}, ""), http.StatusOK)
	wantErrors(t, result, "9/-: The same interval as row 5")
	if result.New != 8 || !result.RangeStart.Equal(utc(2026, time.October, 25, 0)) || !result.RangeEnd.Equal(utc(2026, time.October, 25, 2)) {
		t.Errorf("new %d range %v - %v, want 8 and 00:00Z-02:00Z", result.New, result.RangeStart, result.RangeEnd)
	}
}

func TestConsumptionImport_AResolutionGivesTheEnd(t *testing.T) {
	t.Parallel()
	_, c, point := consumptionImportHarness(t)

	importConsumption(t, c, point.Id, fileOf(
		[]string{"from", "quantity"},
		[]string{"2026-02-01T00:00:00Z", "1"},
		[]string{"2026-02-01T01:00:00Z", "2"},
	), map[string]any{"start": 0, "resolution": "PT1H", "quantityKwh": 1})
	importConsumption(t, c, point.Id, fileOf(
		[]string{"from", "quantity"},
		[]string{"2026-02-02T00:00:00Z", "0,25"},
		[]string{"2026-02-02T00:15:00Z", "0,5"},
	), map[string]any{"start": 0, "resolution": "PT15M", "quantityKwh": 1})
	readings := currentReadings(t, c, point.Id)
	if len(readings) != 4 {
		t.Fatalf("readings = %+v, want 4", readings)
	}
	for i, length := range []time.Duration{time.Hour, time.Hour, 15 * time.Minute, 15 * time.Minute} {
		if got := readings[i].End.Sub(readings[i].Start); got != length {
			t.Errorf("reading %d is %v long, want %v", i, got, length)
		}
	}
	if !readings[3].Start.Equal(time.Date(2026, time.February, 2, 0, 15, 0, 0, time.UTC)) || readings[3].QuantityKwh != 0.5 {
		t.Errorf("reading 3 = %+v", readings[3])
	}
}

func TestConsumptionImport_ReadsAQuantityAsTheColumnStoresIt(t *testing.T) {
	t.Parallel()
	_, c, point := consumptionImportHarness(t)

	data := fileOf(
		[]string{"fra", "kwh"},
		[]string{"2026-02-01T00:00Z", "1,5"},
		[]string{"2026-02-01T01:00Z", "1 234,5"},
		[]string{"2026-02-01T02:00Z", "1.2345"},
		[]string{"2026-02-01T03:00Z", "0"},
		[]string{"2026-02-01T04:00Z", "1e5"},
		[]string{"2026-02-01T05:00Z", "1.234,5"},
		[]string{"2026-02-01T06:00Z", "-1"},
		[]string{"2026-02-01T07:00Z", "99999999999.9995"},
		[]string{"2026-02-01T08:00Z", ""},
		[]string{"2026-02-01T09:00Z", "99999999999.999"},
	)
	mapping := map[string]any{"start": 0, "resolution": "PT1H", "quantityKwh": 1}
	result := consumptionImportOf(t, postConsumptionImport(t, c, point.Id, data, mapping, "false"), http.StatusUnprocessableEntity)
	wantErrors(t, result,
		"5/1: '1e5' is not a number",
		"6/1: '1.234,5' is not a number; use either a decimal comma or a decimal point",
		"7/1: Quantity must be zero or greater.",
		"8/1: Quantity must be less than 100000000000.",
		"9/1: A quantity is required")
	if result.Imported || result.New != 5 || len(currentReadings(t, c, point.Id)) != 0 {
		t.Fatalf("a refused run wrote or counted wrong: %+v", result)
	}

	lines := strings.Split(string(data), "\r\n")
	clean := []byte(strings.Join(append(lines[:5], lines[10:]...), "\r\n"))
	importConsumption(t, c, point.Id, clean, mapping)
	got := []float64{}
	for _, r := range currentReadings(t, c, point.Id) {
		got = append(got, r.QuantityKwh)
	}
	if fmt.Sprint(got) != fmt.Sprint([]float64{1.5, 1234.5, 1.235, 0, 99999999999.999}) {
		t.Errorf("quantities = %v", got)
	}
}

func TestConsumptionImport_ReadsQualityInEnglishAndNorwegian(t *testing.T) {
	t.Parallel()
	_, c, point := consumptionImportHarness(t)

	data := fileOf(
		[]string{"fra", "kwh", "kvalitet"},
		[]string{"2026-02-01T00:00Z", "1", "målt"},
		[]string{"2026-02-01T01:00Z", "1", "Stipulert"},
		[]string{"2026-02-01T02:00Z", "1", "ESTIMERT"},
		[]string{"2026-02-01T03:00Z", "1", "korrigert"},
		[]string{"2026-02-01T04:00Z", "1", "Manuell"},
		[]string{"2026-02-01T05:00Z", "1", ""},
		[]string{"2026-02-01T06:00Z", "1", "corrected"},
		[]string{"2026-02-01T07:00Z", "1", "Guessed"},
	)
	mapping := map[string]any{"start": 0, "resolution": "PT1H", "quantityKwh": 1, "quality": 2}
	result := consumptionImportOf(t, postConsumptionImport(t, c, point.Id, data, mapping, ""), http.StatusOK)
	wantErrors(t, result, "8/2: 'Guessed' is not a quality; use Measured, Estimated, Corrected or Manual")

	importConsumption(t, c, point.Id, data[:bytes.LastIndex(data[:len(data)-2], []byte("\r\n"))+2], mapping)
	got := []string{}
	for _, r := range currentReadings(t, c, point.Id) {
		got = append(got, r.Quality)
	}
	if want := "Measured Estimated Estimated Corrected Manual Measured Corrected"; strings.Join(got, " ") != want {
		t.Errorf("qualities = %v, want %s", got, want)
	}
}

func TestConsumptionImport_ARowForAnotherMeteringPointIsRefused(t *testing.T) {
	t.Parallel()
	_, c, point := consumptionImportHarness(t)
	other := createMeteringPoint(t, c)

	data := fileOf(
		[]string{"målepunktid", "fra", "kwh"},
		[]string{point.Gsrn, "2026-02-01T00:00Z", "1"},
		[]string{other.Gsrn, "2026-02-01T01:00Z", "1"},
	)
	mapping := map[string]any{"gsrn": 0, "start": 1, "resolution": "PT1H", "quantityKwh": 2}
	result := consumptionImportOf(t, postConsumptionImport(t, c, point.Id, data, mapping, "false"), http.StatusUnprocessableEntity)
	wantErrors(t, result, fmt.Sprintf("2/0: The row is for metering point '%s', not this one (%s)", other.Gsrn, point.Gsrn))
	if result.Imported || len(currentReadings(t, c, point.Id)) != 0 || len(currentReadings(t, c, other.Id)) != 0 {
		t.Errorf("the wrong file wrote something: %+v", result)
	}
}

func TestConsumptionImport_ComparesWithTheCurrentIntervals(t *testing.T) {
	t.Parallel()
	h, c, point := consumptionImportHarness(t)
	day := utc(2026, time.February, 1, 0)
	// Two manual readings (quality Manual) and an Elhub one (Estimated).
	addConsumption(t, c, point.Id, day, day.Add(time.Hour), 1)
	addConsumption(t, c, point.Id, day.Add(time.Hour), day.Add(2*time.Hour), 2)
	addElhubConsumption(t, h, point.Id, day.Add(2*time.Hour), day.Add(3*time.Hour), 3)
	// An interval that overlaps the file's without being equal to any row.
	addConsumption(t, c, point.Id, day.Add(3*time.Hour), day.Add(5*time.Hour), 7)
	replacedID := modtest.One[int64](t, h,
		`SELECT id FROM energy.consumption_intervals WHERE metering_point_id=$1 AND start=$2 AND is_current`, point.Id, day.Add(time.Hour))

	data := fileOf(
		[]string{"fra", "kwh", "kvalitet"},
		[]string{"2026-02-01T00:00Z", "1,000", "Manual"},   // unchanged
		[]string{"2026-02-01T01:00Z", "2.5", "Manual"},     // replacing: another quantity
		[]string{"2026-02-01T02:00Z", "3", "Measured"},     // replacing: another quality
		[]string{"2026-02-01T03:00Z", "4", "Measured"},     // new, beside the overlapping 03:00-05:00
		[]string{"2026-02-01T04:00:00+00:00", "5", "Målt"}, // new
	)
	mapping := map[string]any{"start": 0, "resolution": "PT1H", "quantityKwh": 1, "quality": 2}
	check := consumptionImportOf(t, postConsumptionImport(t, c, point.Id, data, mapping, "true"), http.StatusOK)
	if check.New != 2 || check.Replacing != 2 || check.Unchanged != 1 || check.ErrorCount != 0 || check.Imported {
		t.Fatalf("check = %+v, want 2 new, 2 replacing, 1 unchanged", check)
	}
	if got := h.Count(t, `SELECT count(*) FROM energy.consumption_intervals WHERE metering_point_id=$1`, point.Id); got != 4 {
		t.Fatalf("a dry run wrote: %d intervals, want 4", got)
	}

	run := importConsumption(t, c, point.Id, data, mapping)
	if run.New != 2 || run.Replacing != 2 || run.Unchanged != 1 || run.DryRun {
		t.Errorf("run = %+v, want 2 new, 2 replacing, 1 unchanged", run)
	}
	readings := currentReadings(t, c, point.Id)
	got := []string{}
	for _, r := range readings {
		got = append(got, fmt.Sprintf("%02d-%02d %v %s %s", r.Start.UTC().Hour(), r.End.UTC().Hour(), r.QuantityKwh, r.Quality, r.Source))
	}
	want := []string{
		"00-01 1 Manual Manual", "01-02 2.5 Manual Import", "02-03 3 Measured Import",
		"03-04 4 Measured Import", "03-05 7 Manual Manual", "04-05 5 Measured Import",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("readings =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// The replaced interval is history, and the new one points at it.
	if got := modtest.One[int64](t, h, `SELECT supersedes_id FROM energy.consumption_intervals WHERE metering_point_id=$1 AND start=$2 AND is_current`,
		point.Id, day.Add(time.Hour)); got != replacedID {
		t.Errorf("supersedes_id = %d, want %d", got, replacedID)
	}
	if got := h.Count(t, `SELECT count(*) FROM energy.consumption_intervals WHERE metering_point_id=$1 AND NOT is_current`, point.Id); got != 2 {
		t.Errorf("history = %d intervals, want 2", got)
	}

	// The same file again changes nothing.
	again := importConsumption(t, c, point.Id, data, mapping)
	if again.New != 0 || again.Replacing != 0 || again.Unchanged != 5 {
		t.Errorf("again = %+v, want 5 unchanged", again)
	}
	if got := h.Count(t, `SELECT count(*) FROM energy.consumption_intervals WHERE metering_point_id=$1`, point.Id); got != 8 {
		t.Errorf("intervals = %d, want 8 (6 current, 2 history)", got)
	}
}

func TestConsumptionImport_ARepeatedIntervalIsTheSecondRowsError(t *testing.T) {
	t.Parallel()
	_, c, point := consumptionImportHarness(t)

	data := fileOf(
		[]string{"fra", "til", "kwh"},
		[]string{"2026-02-01T00:00Z", "2026-02-01T01:00Z", "1"},
		[]string{"01.02.2026 02:00", "01.02.2026 03:00", "2"},
		[]string{"2026-02-01T00:00Z", "2026-02-01T02:00Z", "3"}, // row 1's start, another end
	)
	result := consumptionImportOf(t, postConsumptionImport(t, c, point.Id, data, startEndQuantity, ""), http.StatusOK)
	wantErrors(t, result)
	if result.New != 3 {
		t.Errorf("new = %d, want 3 (a different end is another interval)", result.New)
	}
	dup := fileOf(
		[]string{"fra", "til", "kwh"},
		[]string{"2026-02-01T00:00Z", "2026-02-01T01:00Z", "1"},
		[]string{"01.02.2026 01:00", "01.02.2026 02:00", "2"}, // row 1's instants, in Oslo time
	)
	result = consumptionImportOf(t, postConsumptionImport(t, c, point.Id, dup, startEndQuantity, "false"), http.StatusUnprocessableEntity)
	wantErrors(t, result, "2/-: The same interval as row 1")
	if len(currentReadings(t, c, point.Id)) != 0 {
		t.Error("a refused run wrote")
	}
}

func TestConsumptionImport_OneBadRowWritesNothing(t *testing.T) {
	t.Parallel()
	h, c, point := consumptionImportHarness(t)

	rows := [][]string{{"fra", "kwh"}}
	for i := range 500 {
		rows = append(rows, []string{utc(2026, time.February, 1, 0).Add(time.Duration(i) * time.Hour).Format(time.RFC3339), "1"})
	}
	rows = append(rows, []string{"not a time", "1"})
	mapping := map[string]any{"start": 0, "resolution": "PT1H", "quantityKwh": 1}
	result := consumptionImportOf(t, postConsumptionImport(t, c, point.Id, fileOf(rows...), mapping, "false"), http.StatusUnprocessableEntity)
	wantErrors(t, result, "501/0: 'not a time' is not a date and time; write it as 2025-01-31 13:00, 31.01.2025 13:00, or with an offset as 2025-01-31T13:00+01:00")
	if result.Imported || result.New != 500 {
		t.Errorf("result = %+v", result)
	}
	if got := h.Count(t, `SELECT count(*) FROM energy.consumption_intervals WHERE metering_point_id=$1`, point.Id); got != 0 {
		t.Errorf("intervals = %d, want 0", got)
	}
}

func TestConsumptionImport_ListsTheFirst200Errors(t *testing.T) {
	t.Parallel()
	_, c, point := consumptionImportHarness(t)
	rows := [][]string{{"fra", "kwh"}}
	for range 250 {
		rows = append(rows, []string{"", "x"})
	}
	result := consumptionImportOf(t, postConsumptionImport(t, c, point.Id, fileOf(rows...),
		map[string]any{"start": 0, "resolution": "PT1H", "quantityKwh": 1}, ""), http.StatusOK)
	if result.ErrorCount != 500 || len(result.Errors) != 200 || result.Errors[199].Row != 100 {
		t.Errorf("errorCount %d, errors %d, last row %d; want 500, 200, 100", result.ErrorCount, len(result.Errors), result.Errors[len(result.Errors)-1].Row)
	}
	if result.RangeStart != nil || result.RangeEnd != nil {
		t.Errorf("range %v - %v, want none", result.RangeStart, result.RangeEnd)
	}
}

func TestConsumptionImport_WritesMonthsLongPast(t *testing.T) {
	t.Parallel()
	h, c, point := consumptionImportHarness(t)
	// Three years back, beyond the partitions the baseline creates.
	first := time.Date(time.Now().Year()-3, time.January, 10, 0, 0, 0, 0, time.UTC)
	second := first.AddDate(0, 1, 0)
	data := fileOf(
		[]string{"fra", "kwh"},
		[]string{first.Format(time.RFC3339), "1"},
		[]string{second.Format(time.RFC3339), "2"},
	)
	importConsumption(t, c, point.Id, data, map[string]any{"start": 0, "resolution": "PT1H", "quantityKwh": 1})
	for _, month := range []time.Time{first, second} {
		name := "consumption_intervals_" + month.Format("2006_01")
		if got := h.Count(t, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'energy' AND c.relname = $1`, name); got != 1 {
			t.Errorf("partition %s: %d, want 1", name, got)
		}
	}
	if got := h.Count(t, `SELECT count(*) FROM energy.consumption_intervals WHERE metering_point_id=$1 AND start < $2`, point.Id, first.AddDate(1, 0, 0)); got != 2 {
		t.Errorf("intervals = %d, want 2", got)
	}
}

func TestConsumptionImport_RefusesABadMapping(t *testing.T) {
	t.Parallel()
	_, c, point := consumptionImportHarness(t)
	data := fileOf([]string{"fra", "til", "kwh"}, []string{"2026-02-01T00:00Z", "2026-02-01T01:00Z", "1"})

	cases := []struct {
		name    string
		mapping any
		field   string
		message string
	}{
		{"not JSON", "{start:", "mapping", "The mapping must be a JSON object naming the columns of start, end or resolution, quantityKwh, and optionally quality and gsrn"},
		{"an unknown field", map[string]any{"start": 0, "end": 1, "quantityKwh": 2, "price": 1}, "mapping", "The mapping must be a JSON object naming the columns of start, end or resolution, quantityKwh, and optionally quality and gsrn"},
		{"no start", map[string]any{"end": 1, "quantityKwh": 2}, "start", "Map the column that holds each reading's start"},
		{"no quantity", map[string]any{"start": 0, "end": 1}, "quantityKwh", "Map the column that holds each reading's quantity in kWh"},
		{"neither end nor resolution", map[string]any{"start": 0, "quantityKwh": 2}, "resolution", "Map an end column or choose a resolution (PT1H or PT15M)"},
		{"both end and resolution", map[string]any{"start": 0, "end": 1, "resolution": "PT1H", "quantityKwh": 2}, "resolution", "Map an end column or choose a resolution, not both"},
		{"another resolution", map[string]any{"start": 0, "resolution": "P1D", "quantityKwh": 2}, "resolution", "A resolution must be PT1H or PT15M, but was 'P1D'"},
		{"a column past the file", map[string]any{"start": 0, "end": 1, "quantityKwh": 3}, "quantityKwh", "Column 3 is not in the file, which has columns 0 to 2"},
		{"a column twice", map[string]any{"start": 0, "end": 1, "quantityKwh": 2, "quality": 2}, "quality", "Column 2 is already mapped to quantityKwh"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// An invalid mapping may be off the contract's own shape; the
			// answer is what is pinned here.
			r := postConsumptionImport(t, c, point.Id, data, tc.mapping, "")
			if r.Status != http.StatusBadRequest {
				t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
			}
			var problem validationProblemJSON
			r.JSON(&problem)
			if got := problem.Errors[tc.field]; len(got) != 1 || got[0] != tc.message {
				t.Errorf("errors = %v, want %s: %q", problem.Errors, tc.field, tc.message)
			}
		})
	}
}

func TestConsumptionImport_HoldsItsCaps(t *testing.T) {
	t.Parallel()
	_, c, point := consumptionImportHarness(t)
	mapping := map[string]any{"start": 0, "resolution": "PT1H", "quantityKwh": 1}
	fileError := func(t *testing.T, r *modtest.Response) string {
		t.Helper()
		if r.Status != http.StatusBadRequest {
			t.Fatalf("status %d body %.300s, want 400", r.Status, r.Body)
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		if len(problem.Errors["file"]) != 1 {
			t.Fatalf("errors = %v, want one on file", problem.Errors)
		}
		return problem.Errors["file"][0]
	}

	t.Run("50 000 rows", func(t *testing.T) {
		t.Parallel()
		var b strings.Builder
		b.WriteString("fra;kwh\n")
		for i := range 50_001 {
			fmt.Fprintf(&b, "%s;1\n", utc(2020, time.January, 1, 0).Add(time.Duration(i)*time.Hour).Format(time.RFC3339))
		}
		if got := fileError(t, postConsumptionImport(t, c, point.Id, []byte(b.String()), mapping, "")); got !=
			"The file holds more than 50000 rows, which is more than one import takes; import it in slices of at most 50000" {
			t.Errorf("refusal = %q", got)
		}
	})
	t.Run("10 MB", func(t *testing.T) {
		t.Parallel()
		data := append([]byte("fra;kwh\n"), bytes.Repeat([]byte("x"), 10*1024*1024)...)
		if got := fileError(t, postConsumptionImport(t, c, point.Id, data, mapping, "")); !strings.Contains(got, "at most 10 MB") {
			t.Errorf("refusal = %q", got)
		}
	})
	t.Run("50 columns", func(t *testing.T) {
		t.Parallel()
		header := make([]string, 51)
		for i := range header {
			header[i] = fmt.Sprintf("c%d", i)
		}
		if got := fileError(t, postConsumptionImport(t, c, point.Id, fileOf(header), mapping, "")); got != "The file has more than 50 columns, which is more than one import takes" {
			t.Errorf("refusal = %q", got)
		}
	})
	t.Run("an unknown point", func(t *testing.T) {
		t.Parallel()
		if r := postConsumptionImport(t, c, 999999, fileOf([]string{"fra"}), nil, ""); r.Status != http.StatusNotFound {
			t.Errorf("status %d, want 404", r.Status)
		}
	})
}

// TestConsumptionImport_FourYearsOfHoursInOneRun is the size the design
// promises seconds for: 35 040 hourly readings, across 48 months.
func TestConsumptionImport_FourYearsOfHoursInOneRun(t *testing.T) {
	t.Parallel()
	h, c, point := consumptionImportHarness(t)
	var b strings.Builder
	b.WriteString("Fra og med;Til;Forbruk\n")
	at := time.Date(2021, time.January, 1, 0, 0, 0, 0, time.UTC)
	for range 35_040 {
		fmt.Fprintf(&b, "%s;%s;%s\n", at.Format(time.RFC3339), at.Add(time.Hour).Format(time.RFC3339), "1,234")
		at = at.Add(time.Hour)
	}
	began := time.Now()
	result := importConsumption(t, c, point.Id, []byte(b.String()), startEndQuantity)
	t.Logf("35 040 readings imported in %v", time.Since(began))
	if result.New != 35_040 {
		t.Errorf("new = %d, want 35040", result.New)
	}
	if got := h.Count(t, `SELECT count(*) FROM energy.consumption_intervals WHERE metering_point_id=$1 AND source='Import'`, point.Id); got != 35_040 {
		t.Errorf("intervals = %d, want 35040", got)
	}
}
