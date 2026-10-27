package energy_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/energy"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the metering-point import's rules (energy import design D1,
// D3, D4, D5, D7): each field's validation and default, the three modes, the
// in-file duplicate, every row of the link table, the dry run, the switch
// confirmation, the permissions per mapped field, the customers module off,
// and the endpoints' own refusals.

// allFieldsHeader is every field of the import under its own JSON name.
var allFieldsHeader = []string{
	"gsrn", "meterNumber", "streetAddress", "postalCode", "city", "countryCode", "priceArea", "gridArea",
	"expectedAnnualConsumptionKwh", "latitude", "longitude", "connectionStatus", "customerLegalId", "supplyStart",
}

// allFieldsRow is a valid row under allFieldsHeader with some cells replaced.
func allFieldsRow(gsrn string, set map[string]string) []string {
	values := map[string]string{
		"gsrn": gsrn, "meterNumber": "M-" + gsrn[14:], "streetAddress": "Storgata 1", "postalCode": "0150", "city": "Oslo",
		"countryCode": "NO", "priceArea": "NO1", "gridArea": "Nett AS", "expectedAnnualConsumptionKwh": "1000",
		"latitude": "59.9", "longitude": "10.7", "connectionStatus": "Connected", "customerLegalId": "", "supplyStart": "",
	}
	for k, v := range set {
		values[k] = v
	}
	row := make([]string, len(allFieldsHeader))
	for i, h := range allFieldsHeader {
		row[i] = values[h]
	}
	return row
}

// gsrnN is a synthetic 18-digit GSRN numbered n, for the tables below.
func gsrnN(n int) string { return fmt.Sprintf("70705759910000%04d", n) }

func TestEnergyImport_EachFieldIsValidatedByTheEndpointsOwnRules(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t)
	cases := []struct {
		name    string
		set     map[string]string
		field   string // the column the error is on, "" for none
		message string
	}{
		{"a 17-digit GSRN", map[string]string{"gsrn": "70705759910000999"}, "gsrn", "GSRN must contain exactly 18 digits."},
		{"no meter number on a create", map[string]string{"meterNumber": " "}, "meterNumber", "Meter number is required."},
		{"no street address", map[string]string{"streetAddress": ""}, "streetAddress", "This field is required."},
		{"a price area that is not one", map[string]string{"priceArea": "Oslo"}, "priceArea", "Price area must contain two uppercase letters followed by one or two digits."},
		{"a consumption that is not a number", map[string]string{"expectedAnnualConsumptionKwh": "lots"}, "expectedAnnualConsumptionKwh", "Expected annual consumption must be a number, but was 'lots'"},
		{"a negative consumption", map[string]string{"expectedAnnualConsumptionKwh": "-1"}, "expectedAnnualConsumptionKwh", "Expected annual consumption cannot be negative."},
		{"a grid area longer than its column", map[string]string{"gridArea": strings.Repeat("g", 65)}, "gridArea", "Grid area cannot be longer than 64 characters."},
		{"a consumption beyond its column", map[string]string{"expectedAnnualConsumptionKwh": "100 000 000 000"}, "expectedAnnualConsumptionKwh", "Expected annual consumption must be less than 100000000000."},
		{"a consumption rounding onto the limit", map[string]string{"expectedAnnualConsumptionKwh": "99999999999,9996"}, "expectedAnnualConsumptionKwh", "Expected annual consumption must be less than 100000000000."},
		{"a latitude off the globe", map[string]string{"latitude": "95"}, "latitude", "Latitude must be between -90 and 90."},
		{"a longitude off the globe", map[string]string{"longitude": "200,5"}, "longitude", "Longitude must be between -180 and 180."},
		{"a connection status that is not one", map[string]string{"connectionStatus": "kanskje"}, "connectionStatus", "Connection status must be New, Connected or Disconnected."},
		{"a country code of three letters", map[string]string{"countryCode": "NOR"}, "countryCode", "Country code must contain two letters."},
		{"an organisation number with a wrong check digit", map[string]string{"customerLegalId": "910000005"}, "customerLegalId", "A Norwegian organisation number must be nine digits with a valid check digit, but was '910000005'"},
		{"a date the calendar lacks", map[string]string{"customerLegalId": orgA, "supplyStart": "31.02.2026"}, "supplyStart", "A start date must be a date such as 01.01.2026, 01/01/2026 or 2026-01-01, but was '31.02.2026'"},
		{"a month-first date", map[string]string{"customerLegalId": orgA, "supplyStart": "2026/01/31"}, "supplyStart", "A start date must be a date such as 01.01.2026, 01/01/2026 or 2026-01-01, but was '2026/01/31'"},
	}
	rows := [][]string{allFieldsHeader}
	for i, tc := range cases {
		rows = append(rows, allFieldsRow(gsrnN(i+1), tc.set))
	}
	checked := checkEnergyImport(t, h, c, fileOf(rows...), mappingByFields(allFieldsHeader, "create", allFieldsHeader...))
	if checked.Failed != len(cases) {
		t.Errorf("checked = %+v, want every row failed", checked)
	}
	got := energyImportRows(t, c, checked.ID, "&outcome=failed")
	for i, tc := range cases {
		row := rowNamed(t, got, i+1)
		found := false
		for _, e := range row.Errors {
			if e.Column != nil && allFieldsHeader[*e.Column] == tc.field && e.Message == tc.message {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: errors %+v, want %q on %s", tc.name, row.Errors, tc.message, tc.field)
		}
	}
}

func TestEnergyImport_TheFieldsAreReadAsTheExportsWriteThem(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t)
	data := fileOf(allFieldsHeader,
		allFieldsRow(gsrn1, map[string]string{"priceArea": "no 2", "expectedAnnualConsumptionKwh": "12 345,75", "latitude": "59,91",
			"longitude": "10,75", "connectionStatus": "tilkoblet", "countryCode": "", "customerLegalId": "910 000 004", "supplyStart": "2026-01-01"}),
		allFieldsRow(gsrn2, map[string]string{"connectionStatus": "frakoblet", "countryCode": "se", "customerLegalId": orgA, "supplyStart": "01.01.2026", "gridArea": " "}),
		allFieldsRow(gsrn3, map[string]string{"connectionStatus": "ny", "customerLegalId": orgA, "supplyStart": "1/1/2026", "expectedAnnualConsumptionKwh": ""}),
		allFieldsRow(gsrn4, map[string]string{"connectionStatus": ""}),
	)
	done := importEnergyNow(t, h, c, data, mappingByFields(allFieldsHeader, "create", allFieldsHeader...))
	if done.Created != 4 || done.Failed != 0 {
		t.Fatalf("done = %+v, want four created", done)
	}
	type point struct {
		priceArea, country, status string
		grid                       *string
		expected, lat, lon         *float64
	}
	read := func(gsrn string) point {
		var p point
		if err := h.Pool().QueryRow(t.Context(), `SELECT price_area, country_code, connection_status, grid_area, expected_annual_consumption_kwh::float8, latitude, longitude
			FROM energy.metering_points WHERE gsrn = $1`, gsrn).Scan(&p.priceArea, &p.country, &p.status, &p.grid, &p.expected, &p.lat, &p.lon); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if p := read(gsrn1); p.priceArea != "NO2" || p.country != "NO" || p.status != "Connected" || p.expected == nil || *p.expected != 12345.75 ||
		p.lat == nil || *p.lat != 59.91 || p.lon == nil || *p.lon != 10.75 {
		t.Errorf("%s = %+v, want NO2, NO, Connected, 12345.75 at 59.91, 10.75", gsrn1, p)
	}
	if p := read(gsrn2); p.status != "Disconnected" || p.country != "SE" || p.grid != nil {
		t.Errorf("%s = %+v, want Disconnected, SE and no grid area", gsrn2, p)
	}
	if p := read(gsrn3); p.status != "New" || p.expected != nil {
		t.Errorf("%s = %+v, want New and no expected consumption", gsrn3, p)
	}
	if p := read(gsrn4); p.status != "New" {
		t.Errorf("%s = %+v, want a blank status to be New on a create", gsrn4, p)
	}
	// Every date form is midnight UTC on the first of January.
	if n := h.Count(t, `SELECT count(*) FROM energy.supply_periods WHERE start = '2026-01-01T00:00:00Z' AND customer_id = 1001`); n != 3 {
		t.Errorf("%d periods from 2026-01-01 for 1001, want 3", n)
	}
}

func TestEnergyImport_AnUnmappedRequiredFieldIsNamedOnACreate(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t)
	header := []string{"gsrn", "city"}
	checked := checkEnergyImport(t, h, c, fileOf(header, []string{gsrn1, "Oslo"}), mappingByFields(header, "create", header...))
	row := rowNamed(t, energyImportRows(t, c, checked.ID, ""), 1)
	var messages []string
	for _, e := range row.Errors {
		messages = append(messages, e.Message)
	}
	joined := strings.Join(messages, " | ")
	for _, want := range []string{"needs a meter number, and no meterNumber column is mapped", "needs a price area, and no priceArea column is mapped",
		"needs a street address, and no streetAddress column is mapped"} {
		if !strings.Contains(joined, want) {
			t.Errorf("errors %q, want one saying %q", joined, want)
		}
	}
}

func TestEnergyImport_TheModes(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t)
	existing := createPointByHand(t, c, gsrn1, "M-0012")
	data := fileOf(pointHeader, pointRow(gsrn1, "", ""), pointRow(gsrn2, "", ""))

	// create refuses the GSRN on file and creates the other.
	created := importEnergyNow(t, h, c, data, mappingByFields(pointHeader, "create", "gsrn", "meterNumber", "streetAddress", "postalCode", "city", "priceArea"))
	if created.Created != 1 || created.Failed != 1 {
		t.Errorf("create = %+v, want one created and one failed", created)
	}
	if r := rowNamed(t, energyImportRows(t, c, created.ID, ""), 1); len(r.Errors) != 1 || r.Errors[0].Message != "A metering point with that GSRN already exists." {
		t.Errorf("row 1 = %+v, want the duplicate GSRN refused", r)
	}

	// update refuses a GSRN nobody has and updates the one on file — only its
	// mapped city; the street and the price area it had stay.
	updateData := fileOf([]string{"gsrn", "city"}, []string{gsrn1, "Bergen"}, []string{gsrn3, "Bergen"})
	updated := importEnergyNow(t, h, c, updateData, mappingByFields([]string{"gsrn", "city"}, "update", "gsrn", "city"))
	if updated.Updated != 1 || updated.Failed != 1 {
		t.Errorf("update = %+v, want one updated and one failed", updated)
	}
	var street, city string
	if err := h.Pool().QueryRow(t.Context(), `SELECT street_address, city FROM energy.metering_points WHERE id = $1`, existing).Scan(&street, &city); err != nil {
		t.Fatal(err)
	}
	if street != "Testgata 1" || city != "Bergen" {
		t.Errorf("updated point = %q, %q, want the street kept and the city written", street, city)
	}
	if r := rowNamed(t, energyImportRows(t, c, updated.ID, ""), 2); len(r.Errors) != 1 || r.Errors[0].Message != "No metering point has GSRN "+gsrn3 {
		t.Errorf("row 2 = %+v, want the unknown GSRN refused", r)
	}

	// createOrUpdate does both; a blank optional cell clears its field.
	both := fileOf([]string{"gsrn", "meterNumber", "streetAddress", "postalCode", "city", "priceArea", "gridArea"},
		[]string{gsrn1, "", "Testgata 1", "0001", "Oslo", "NO1", ""},
		[]string{gsrn5, "M5", "Ny vei 5", "0005", "Oslo", "NO1", "Nett AS"})
	header := []string{"gsrn", "meterNumber", "streetAddress", "postalCode", "city", "priceArea", "gridArea"}
	h.Exec(t, `UPDATE energy.metering_points SET grid_area = 'Gammelt nett' WHERE id = $1`, existing)
	mixed := importEnergyNow(t, h, c, both, mappingByFields(header, "createOrUpdate", header...))
	if mixed.Created != 1 || mixed.Updated != 1 || mixed.Failed != 0 {
		t.Errorf("createOrUpdate = %+v, want one created and one updated", mixed)
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.metering_points WHERE id = $1 AND grid_area IS NULL`, existing); n != 1 {
		t.Error("a blank grid area did not clear the point's")
	}
}

func TestEnergyImport_AMeterIsNeverReplacedByImport(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t)
	createPointByHand(t, c, gsrn1, "M-0012")
	header := []string{"gsrn", "meterNumber", "city"}
	// The current meter's own number, or none, updates the point.
	same := importEnergyNow(t, h, c, fileOf(header, []string{gsrn1, "M-0012", "Bergen"}), mappingByFields(header, "update", header...))
	blank := importEnergyNow(t, h, c, fileOf(header, []string{gsrn1, "", "Bodø"}), mappingByFields(header, "update", header...))
	if same.Updated != 1 || blank.Updated != 1 {
		t.Errorf("same = %+v, blank = %+v, want each row updated", same, blank)
	}
	// Another number is refused: a meter is replaced by hand.
	checked := checkEnergyImport(t, h, c, fileOf(header, []string{gsrn1, "OTHER-1", "Bergen"}), mappingByFields(header, "update", header...))
	if checked.Failed != 1 {
		t.Fatalf("checked = %+v, want the different meter number refused", checked)
	}
	r := rowNamed(t, energyImportRows(t, c, checked.ID, ""), 1)
	if len(r.Errors) != 1 || r.Errors[0].Message != "The meter number differs from the current meter's; use Replace meter." {
		t.Errorf("row = %+v, want the meter-number refusal", r)
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.meters`); n != 1 {
		t.Errorf("%d meters, want the one created by hand", n)
	}
}

func TestEnergyImport_AGsrnTwiceInOneFileIsRefusedOnTheSecond(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t)
	data := fileOf(pointHeader, pointRow(gsrn1, "", ""), pointRow(gsrn2, "", ""), pointRow(gsrn1, "", ""))
	checked := checkEnergyImport(t, h, c, data, fullMapping("create"))
	if checked.Created != 2 || checked.Failed != 1 {
		t.Fatalf("checked = %+v, want two to create and the repeat failed", checked)
	}
	r := rowNamed(t, energyImportRows(t, c, checked.ID, ""), 3)
	if len(r.Errors) != 1 || r.Errors[0].Message != "Row 1 of this file already has this GSRN; a file lists a metering point once" {
		t.Errorf("row 3 = %+v, want the repeat refused naming row 1", r)
	}
	done := runEnergyImport(t, h, c, checked)
	if done.Created != 2 || done.Failed != 1 {
		t.Errorf("done = %+v, want the run to agree with the check", done)
	}
}

// linkTableFile is design D4's table, one row a situation, against points
// set up by linkTableSetup.
func linkTableFile() []byte {
	return fileOf(pointHeader,
		pointRow(gsrnN(1), "", "01.01.2026"),          // 1 none: no number
		pointRow(gsrnN(2), "910000063", "01.01.2026"), // 2 nobody holds it
		pointRow(gsrnN(3), orgF, "01.01.2026"),        // 3 two customers hold it
		pointRow(gsrnN(4), orgD, "01.01.2026"),        // 4 archived
		pointRow(gsrnN(5), orgE, "01.01.2026"),        // 5 merged away
		pointRow(gsrnN(6), orgA, "01.06.2026"),        // 6 P already 1001's: same, whatever S says
		pointRow(gsrnN(7), orgA, "01.01.2026"),        // 7 no P, S: assigned
		pointRow(gsrnN(8), orgA, ""),                  // 8 no P, no S: pending
		pointRow(gsrnN(9), orgA, "01.06.2026"),        // 9 P 1002's, S after its start: switched
		pointRow(gsrnN(10), orgA, ""),                 // 10 P 1002's, no S: pending switch
		pointRow(gsrnN(11), orgA, "01.01.2026"),       // 11 P 1002's from March, S before it: refused
		pointRow(gsrnN(12), orgA, "01.06.2026"),       // 12 an ended period covers S: refused as an overlap
		pointRow(gsrnN(13), "123", "01.01.2026"),      // 13 not a valid number
	)
}

// linkTableSetup creates the points the link table's rows find, and their
// periods by hand: 6 is 1001's, 9 and 10 1002's from February, 11 1002's from
// March, 12 had 1002 for all of 2026 and is ended.
func linkTableSetup(t *testing.T, c *modtest.Client) {
	t.Helper()
	for _, n := range []int{6, 9, 10, 11, 12} {
		createPointByHand(t, c, gsrnN(n), "M-"+gsrnN(n)[14:])
	}
	ids := map[int]int32{}
	for _, n := range []int{6, 9, 10, 11, 12} {
		r := c.Do(http.MethodGet, "/api/v1/energy/metering-points?search="+gsrnN(n), nil)
		var page struct {
			Data []meteringPointJSON `json:"data"`
		}
		r.JSON(&page)
		ids[n] = page.Data[0].Id
	}
	feb := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	assignByHand(t, c, ids[6], 1001, feb)
	assignByHand(t, c, ids[9], 1002, feb)
	assignByHand(t, c, ids[10], 1002, feb)
	assignByHand(t, c, ids[11], 1002, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	assignByHand(t, c, ids[12], 1002, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", ids[12]), nil)
	var periods []supplyPeriodJSON
	r.JSON(&periods)
	if end := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods/%d/end", ids[12], periods[0].Id),
		map[string]any{"end": time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)}); end.Status != http.StatusOK {
		t.Fatalf("end: status %d body %s", end.Status, end.Body)
	}
}

func TestEnergyImport_EveryRowOfTheLinkTable(t *testing.T) {
	t.Parallel()
	h, c, dir := importHarness(t)
	linkTableSetup(t, c)
	checked := checkEnergyImport(t, h, c, linkTableFile(), fullMapping("createOrUpdate"))
	if checked.Switched != 1 || checked.Pending != 2 || checked.PendingSwitches != 1 {
		t.Errorf("checked = %+v, want one switch, two pending, one of them a pending switch", checked)
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.metering_points`); n != 5 {
		t.Errorf("%d points after the check, want the five made by hand", n)
	}
	done := runEnergyImport(t, h, c, checked)
	if n := dir.lookups.Load(); n != 2 {
		t.Errorf("CustomersByLegalID called %d times, want once per run (2)", n)
	}
	rows := energyImportRows(t, c, done.ID, "")
	want := []struct {
		row     int
		outcome string
		link    string
		message string
	}{
		{1, "created", "none", ""},
		{2, "failed", "", "No customer has organisation number 910000063"},
		{3, "failed", "", "2 customers have organisation number 910000055 (1006 Fjord Tvilling AS, 1007 Fjord Tvilling Avd AS); the file cannot say which one is meant"},
		{4, "failed", "", "Customer 1004 Dvale Holding AS is archived and takes no more supply periods"},
		{5, "failed", "", "Customer 1005 Eiker Sammenslått AS was merged into customer 1001 and takes no more supply periods"},
		{6, "updated", "same", ""},
		{7, "created", "assigned", ""},
		{8, "created", "pending", ""},
		{9, "updated", "switched", ""},
		{10, "updated", "pending", ""},
		{11, "failed", "", "Switch date must be after the active period's start."},
		{12, "failed", "", "The metering point already has a non-cancelled supply period at that time. End the existing period first."},
		{13, "failed", "", "A Norwegian organisation number must be nine digits with a valid check digit, but was '123'"},
	}
	for _, w := range want {
		r := rowNamed(t, rows, w.row)
		if r.Outcome != w.outcome || r.Link != w.link {
			t.Errorf("row %d = %s/%s %+v, want %s/%s", w.row, r.Outcome, r.Link, r.Errors, w.outcome, w.link)
		}
		if w.message != "" && (len(r.Errors) != 1 || r.Errors[0].Message != w.message) {
			t.Errorf("row %d errors %+v, want %q", w.row, r.Errors, w.message)
		}
	}
	// A refused customer writes no point.
	for _, n := range []int{2, 3, 4, 5, 13} {
		if k := h.Count(t, `SELECT count(*) FROM energy.metering_points WHERE gsrn = $1`, gsrnN(n)); k != 0 {
			t.Errorf("row %d's point was written although its customer was refused", n)
		}
	}
	if r := rowNamed(t, rows, 9); r.SwitchFromCustomerID == nil || *r.SwitchFromCustomerID != 1002 || r.SwitchFromCustomerName != "Bølge Energi AS" ||
		r.CustomerName != "Aurora Kraft AS" || r.SupplyStart == nil || !r.SupplyStart.Equal(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("row 9 = %+v, want the switch from 1002 to 1001 on 1 June named", r)
	}
	if r := rowNamed(t, rows, 10); r.SwitchFromCustomerID == nil || *r.SwitchFromCustomerID != 1002 {
		t.Errorf("row 10 = %+v, want the pending switch from 1002", r)
	}
	// The switch: 1002's period ended on 1 June, 1001's from there.
	if activeCustomer(t, h, gsrnN(9)) != 1001 {
		t.Error("point 9 is not 1001's after the switch")
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.supply_periods p JOIN energy.metering_points mp ON mp.id = p.metering_point_id
		WHERE mp.gsrn = $1 AND p.customer_id = 1002 AND p.status = 'Ended' AND p."end" = '2026-06-01T00:00:00Z'`, gsrnN(9)); n != 1 {
		t.Error("1002's period on point 9 did not end on the switch date")
	}
	// same writes nothing: point 6 still has only 1001's February period.
	if n := h.Count(t, `SELECT count(*) FROM energy.supply_periods p JOIN energy.metering_points mp ON mp.id = p.metering_point_id
		WHERE mp.gsrn = $1`, gsrnN(6)); n != 1 {
		t.Errorf("point 6 has %d periods, want its one", n)
	}
	// A blank organisation number never ends or changes a period; pending
	// writes none.
	for _, n := range []int{8, 10} {
		want := int32(0)
		if n == 10 {
			want = 1002
		}
		if got := activeCustomer(t, h, gsrnN(n)); got != want {
			t.Errorf("point %d is %d's, want %d's", n, got, want)
		}
	}
	// The rows filter by link.
	if switched := energyImportRows(t, c, done.ID, "&link=switched"); len(switched) != 1 || switched[0].Row != 9 {
		t.Errorf("link=switched = %+v, want row 9", switched)
	}
}

func TestEnergyImport_TheDryRunKeepsNothing(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t)
	linkTableSetup(t, c)
	countAll := func() string {
		return fmt.Sprintf("%d points, %d meters, %d periods, %d ended",
			h.Count(t, `SELECT count(*) FROM energy.metering_points`), h.Count(t, `SELECT count(*) FROM energy.meters`),
			h.Count(t, `SELECT count(*) FROM energy.supply_periods`), h.Count(t, `SELECT count(*) FROM energy.supply_periods WHERE status = 'Ended'`))
	}
	before := countAll()
	checked := checkEnergyImport(t, h, c, linkTableFile(), fullMapping("createOrUpdate"))
	if checked.Created == 0 || checked.Switched == 0 {
		t.Fatalf("checked = %+v, want creates and a switch to have been tried", checked)
	}
	if after := countAll(); after != before {
		t.Errorf("after the check %s, want %s", after, before)
	}
	// A point the check would create has no id.
	if r := rowNamed(t, energyImportRows(t, c, checked.ID, ""), 7); r.MeteringPointID != nil || r.Gsrn != gsrnN(7) {
		t.Errorf("row 7 = %+v, want its GSRN and no point id", r)
	}
}

func TestEnergyImport_TheRunConfirmsItsSwitches(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t)
	linkTableSetup(t, c)
	checked := checkEnergyImport(t, h, c, linkTableFile(), fullMapping("createOrUpdate"))
	for _, confirm := range []*int{nil, ptr(0), ptr(2)} {
		r := postEnergyRun(c, checked.ID, confirm)
		if code := energyConflictCode(t, r); code != "import_switch_not_confirmed" {
			t.Errorf("confirm %v: code %q, want import_switch_not_confirmed", confirm, code)
		}
		var problem struct {
			Switches int `json:"switches"`
		}
		r.JSON(&problem)
		if problem.Switches != 1 {
			t.Errorf("confirm %v: switches %d, want 1", confirm, problem.Switches)
		}
	}
	energyImportOf(t, postEnergyRun(c, checked.ID, ptr(1)), http.StatusOK)

	// A check that counted no switch runs without a count, and not with one.
	plain := checkEnergyImport(t, h, c, fileOf(pointHeader, pointRow(gsrn1, "", "")), fullMapping("create"))
	if code := energyConflictCode(t, postEnergyRun(c, plain.ID, ptr(1))); code != "import_switch_not_confirmed" {
		t.Errorf("code %q, want import_switch_not_confirmed for a count the check did not find", code)
	}
	energyImportOf(t, postEnergyRun(c, plain.ID, nil), http.StatusOK)
}

func TestEnergyImport_TheRunNeverSwitchesMoreThanConfirmed(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t)
	pointID := createPointByHand(t, c, gsrn1, "M-0012")
	data := fileOf(pointHeader, pointRow(gsrn1, orgA, "01.06.2026"))
	checked := checkEnergyImport(t, h, c, data, fullMapping("update"))
	if checked.Switched != 0 || checked.Updated != 1 {
		t.Fatalf("checked = %+v, want an assignment and no switch", checked)
	}
	// Between the check and the run, the point is given to 1002 by hand: the
	// row would now switch it, which nobody confirmed.
	assignByHand(t, c, pointID, 1002, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	done := runEnergyImport(t, h, c, checked)
	if done.Failed != 1 || done.Switched != 0 {
		t.Errorf("done = %+v, want the unconfirmed switch refused", done)
	}
	if activeCustomer(t, h, gsrn1) != 1002 {
		t.Error("the point was switched although no switch was confirmed")
	}
}

func TestEnergyImport_Attach(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t)
	// Four pending rows: 1 and 2 with no point supplied, 3 1002's (a pending
	// switch), 4 1002's from August.
	id3 := createPointByHand(t, c, gsrn3, "M-0036")
	id4 := createPointByHand(t, c, gsrn4, "M-0043")
	assignByHand(t, c, id3, 1002, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	assignByHand(t, c, id4, 1002, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	data := fileOf(pointHeader, pointRow(gsrn1, orgA, ""), pointRow(gsrn2, orgC, ""), pointRow(gsrn3, orgA, ""), pointRow(gsrn4, orgA, ""))
	done := importEnergyNow(t, h, c, data, fullMapping("createOrUpdate"))
	if done.Pending != 4 || done.PendingSwitches != 2 {
		t.Fatalf("done = %+v, want four pending, two of them switches", done)
	}

	// Meanwhile point 1 is given to 1001 by hand (it becomes same), and point 2
	// to 1002 (attaching it is now a switch the import did not see).
	assignByHand(t, c, pointByGsrn(t, h, gsrn1), 1001, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	assignByHand(t, c, pointByGsrn(t, h, gsrn2), 1002, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))

	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	// The switches now are three — 2, 3 and 4 — so two, the import's own
	// figure, is refused.
	r := postEnergyAttach(c, done.ID, start, 2)
	if code := energyConflictCode(t, r); code != "import_switch_not_confirmed" {
		t.Fatalf("code %q, want import_switch_not_confirmed", code)
	}
	if bad := c.Do(http.MethodPost, energyImportPath(done.ID, "/attach"), map[string]any{"start": "2026-07-01T00:00:00+02:00", "confirmSwitches": 3}); bad.Status != http.StatusBadRequest {
		t.Errorf("a start with an offset: status %d, want 400", bad.Status)
	}
	attaching := energyImportOf(t, postEnergyAttach(c, done.ID, start, 3), http.StatusOK)
	if attaching.State != "attaching" || attaching.AttachStart == nil || !attaching.AttachStart.Equal(start) {
		t.Errorf("attaching = %+v, want attaching from 1 July", attaching)
	}
	runEnergyImportWorker(t, h)
	after := getEnergyImport(t, c, done.ID)
	if after.State != "done" || after.Attached != 2 || after.Pending != 1 {
		t.Errorf("after the attach = %+v, want done, two attached and one still pending", after)
	}
	rows := energyImportRows(t, c, done.ID, "")
	if r := rowNamed(t, rows, 1); r.Link != "same" {
		t.Errorf("row 1 = %+v, want same: attached by hand meanwhile", r)
	}
	if r := rowNamed(t, rows, 2); r.Link != "attached" || r.SwitchFromCustomerID == nil || *r.SwitchFromCustomerID != 1002 {
		t.Errorf("row 2 = %+v, want attached, switched from 1002 as re-read at attach time", r)
	}
	if r := rowNamed(t, rows, 3); r.Link != "attached" || r.SupplyStart == nil || !r.SupplyStart.Equal(start) {
		t.Errorf("row 3 = %+v, want attached from 1 July", r)
	}
	if r := rowNamed(t, rows, 4); r.Link != "pending" || len(r.Errors) != 1 || r.Errors[0].Message != "Switch date must be after the active period's start." {
		t.Errorf("row 4 = %+v, want still pending with the switch's refusal", r)
	}
	if activeCustomer(t, h, gsrn2) != 1003 || activeCustomer(t, h, gsrn3) != 1001 || activeCustomer(t, h, gsrn4) != 1002 {
		t.Error("the attach did not leave 2 with 1003, 3 with 1001 and 4 with 1002")
	}

	// A retry with a later date attaches the last one.
	energyImportOf(t, postEnergyAttach(c, done.ID, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), 1), http.StatusOK)
	runEnergyImportWorker(t, h)
	if final := getEnergyImport(t, c, done.ID); final.Pending != 0 || final.Attached != 3 || final.State != "done" {
		t.Errorf("after the retry = %+v, want nothing pending and three attached", final)
	}
	if activeCustomer(t, h, gsrn4) != 1001 {
		t.Error("point 4 was not attached on the retry")
	}
	// Nothing left: the attach is refused.
	if code := energyConflictCode(t, postEnergyAttach(c, done.ID, start, 0)); code != "import_nothing_pending" {
		t.Errorf("code %q, want import_nothing_pending", code)
	}
}

func TestEnergyImport_AttachRefusesACustomerArchivedSince(t *testing.T) {
	t.Parallel()
	h, c, dir := importHarness(t)
	done := importEnergyNow(t, h, c, fileOf(pointHeader, pointRow(gsrn1, orgC, "")), fullMapping("create"))
	dir.archive(1003)
	energyImportOf(t, postEnergyAttach(c, done.ID, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), 0), http.StatusOK)
	runEnergyImportWorker(t, h)
	r := rowNamed(t, energyImportRows(t, c, done.ID, ""), 1)
	if r.Link != "pending" || len(r.Errors) != 1 || r.Errors[0].Message != "Customer 1003 Cirrus Drift AS is archived and takes no more supply periods" {
		t.Errorf("row = %+v, want still pending with the archived refusal", r)
	}
}

// TestEnergyImport_ADataExceptionFailsTheRowNotTheRun: a value the database
// refuses as too long or out of range (SQLSTATE class 22) — one the checks
// let through — fails its row with a generic message, in the check and the
// run alike. It used to fail the whole run, which the worker retried every
// cycle, oldest first, so the import behind it never ran.
func TestEnergyImport_ADataExceptionFailsTheRowNotTheRun(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t)
	// A trigger stands in for a column the checks do not know the limit of.
	h.Exec(t, `CREATE FUNCTION energy.refuse_poison_for_test() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.city = 'Poison' THEN
				RAISE EXCEPTION 'value too long for type character varying' USING ERRCODE = '22001';
			END IF;
			RETURN NEW;
		END $$`)
	h.Exec(t, `CREATE TRIGGER refuse_poison_for_test BEFORE INSERT OR UPDATE ON energy.metering_points
		FOR EACH ROW EXECUTE FUNCTION energy.refuse_poison_for_test()`)
	poisoned := pointRow(gsrn2, "", "")
	poisoned[4] = "Poison"
	first := uploadEnergyImport(t, c, fileOf(pointHeader, pointRow(gsrn1, "", ""), poisoned))
	second := uploadEnergyImport(t, c, fileOf(pointHeader, pointRow(gsrn3, "", "")))
	for _, imp := range []energyImportJSON{first, second} {
		energyImportOf(t, putEnergyMapping(c, imp.ID, fullMapping("create")), http.StatusOK)
		energyImportOf(t, postEnergyCheck(c, imp.ID), http.StatusOK)
	}
	runEnergyImportWorker(t, h)
	checked := getEnergyImport(t, c, first.ID)
	if checked.State != "checked" || checked.Error != "" || checked.Created != 1 || checked.Failed != 1 {
		t.Fatalf("the first check = %+v, want checked with one row created and the poisoned one failed", checked)
	}
	if behind := getEnergyImport(t, c, second.ID); behind.State != "checked" || behind.Created != 1 {
		t.Errorf("the import behind it = %+v, want checked in the same cycle", behind)
	}
	row := rowNamed(t, energyImportRows(t, c, first.ID, "&outcome=failed"), 2)
	if len(row.Errors) != 1 || row.Errors[0].Column != nil || row.Errors[0].Message != "A value is out of range or too long for its field." {
		t.Errorf("the poisoned row's errors = %+v, want the one generic message on no column", row.Errors)
	}
	done := runEnergyImport(t, h, c, checked)
	if done.Error != "" || done.Created != 1 || done.Failed != 1 {
		t.Errorf("the run = %+v, want one created and the poisoned row failed", done)
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.metering_points WHERE gsrn = $1`, gsrn2); n != 0 {
		t.Errorf("the poisoned row wrote %d points, want none", n)
	}
}

// TestEnergyImport_ACheckAskedAsARunIsTakenUpKeepsTheRunsOutcomes: a check
// asked for while the worker takes the import's run up — the handler having
// read a state that allows the check — is the running import's 409, and the
// outcomes the run writes are kept. It used to clear every row's outcome
// before finding the import no longer checkable.
func TestEnergyImport_ACheckAskedAsARunIsTakenUpKeepsTheRunsOutcomes(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t)
	checked := checkEnergyImport(t, h, c, manyPoints(2), manyPointsMapping)
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The worker under way, not yet committed: the import importing, its
	// first row's outcome written.
	if _, err := tx.Exec(ctx, `UPDATE energy.imports SET state = 'importing', started_at = now() WHERE id = $1`, checked.ID); err != nil {
		t.Fatalf("take the run up: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE energy.import_rows SET outcome = 'created' WHERE import_id = $1 AND row_no = 1`, checked.ID); err != nil {
		t.Fatalf("write the first outcome: %v", err)
	}
	answered := make(chan *modtest.Response, 1)
	go func() { answered <- postEnergyCheck(c, checked.ID) }()
	waitForALockWait(t, h)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if code := energyConflictCode(t, <-answered); code != "import_running" {
		t.Errorf("code %q, want import_running", code)
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.import_rows WHERE import_id = $1 AND row_no = 1 AND outcome = 'created'`, checked.ID); n != 1 {
		t.Error("the run's outcome on row 1 was cleared by the check")
	}
	if imp := getEnergyImport(t, c, checked.ID); imp.State != "importing" {
		t.Errorf("state %q, want importing", imp.State)
	}
}

// TestEnergyImport_AnAttachNeedsAStart: an attach without start decoded to
// the zero time, which is UTC and passed, and queued an attach from year 1.
func TestEnergyImport_AnAttachNeedsAStart(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t)
	done := importEnergyNow(t, h, c, fileOf(pointHeader, pointRow(gsrn1, orgA, "")), fullMapping("create"))
	r := c.Do(http.MethodPost, energyImportPath(done.ID, "/attach"), map[string]any{"confirmSwitches": 0},
		modtest.SkipContract("the body leaves out the start the contract requires, which is the point"))
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if got := problem.Errors["start"]; len(got) != 1 || got[0] != "Start is required." {
		t.Errorf("Errors[start] = %v, want [\"Start is required.\"]", got)
	}
	if after := getEnergyImport(t, c, done.ID); after.State != "done" || after.AttachStart != nil {
		t.Errorf("after = %+v, want still done with no attach asked for", after)
	}
}

// TestEnergyImport_ARefusedAttachKeepsItsPendingSwitch: a pending row the
// attach refuses stays a pending switch — its switch_from as the attach read
// it — rather than losing it and undercounting pendingSwitches.
func TestEnergyImport_ARefusedAttachKeepsItsPendingSwitch(t *testing.T) {
	t.Parallel()
	h, c, dir := importHarness(t)
	id1 := createPointByHand(t, c, gsrn1, "M-0012")
	assignByHand(t, c, id1, 1002, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	done := importEnergyNow(t, h, c, fileOf(pointHeader, pointRow(gsrn1, orgA, "")), fullMapping("createOrUpdate"))
	if done.Pending != 1 || done.PendingSwitches != 1 {
		t.Fatalf("done = %+v, want one pending switch", done)
	}
	dir.archive(1001)
	energyImportOf(t, postEnergyAttach(c, done.ID, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), 1), http.StatusOK)
	runEnergyImportWorker(t, h)
	after := getEnergyImport(t, c, done.ID)
	if after.State != "done" || after.Pending != 1 || after.PendingSwitches != 1 {
		t.Errorf("after the refused attach = %+v, want one pending switch still", after)
	}
	if r := rowNamed(t, energyImportRows(t, c, done.ID, ""), 1); r.Link != "pending" || len(r.Errors) != 1 ||
		r.SwitchFromCustomerID == nil || *r.SwitchFromCustomerID != 1002 {
		t.Errorf("row 1 = %+v, want pending, refused, switching from 1002", r)
	}
}

// TestEnergyImport_AConsumptionIsComparedAsStored: the expected annual
// consumption is rounded to the column's three decimals before it is
// compared, so a row whose value rounds to what is on file writes nothing,
// and one that rounds to another value writes that value.
func TestEnergyImport_AConsumptionIsComparedAsStored(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t)
	id := createPointByHand(t, c, gsrn1, "M-0012")
	h.Exec(t, `UPDATE energy.metering_points SET expected_annual_consumption_kwh = 1000, updated_at = '2000-01-01T00:00:00Z' WHERE id = $1`, id)
	header := []string{"gsrn", "expectedAnnualConsumptionKwh"}
	mapping := mappingByFields(header, "update", header...)

	importEnergyNow(t, h, c, fileOf(header, []string{gsrn1, "1000,0004"}), mapping)
	if n := h.Count(t, `SELECT count(*) FROM energy.metering_points WHERE id = $1 AND updated_at = '2000-01-01T00:00:00Z'`, id); n != 1 {
		t.Error("a value rounding to what is on file moved updated_at")
	}

	importEnergyNow(t, h, c, fileOf(header, []string{gsrn1, "1000,0005"}), mapping)
	if n := h.Count(t, `SELECT count(*) FROM energy.metering_points WHERE id = $1 AND expected_annual_consumption_kwh = 1000.001
		AND updated_at <> '2000-01-01T00:00:00Z'`, id); n != 1 {
		t.Error("a value rounding to 1000.001 was not written as 1000.001")
	}
}

func TestEnergyImport_TheLifecycleConflicts(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t)
	data := fileOf(pointHeader, pointRow(gsrn1, "", ""))
	draft := uploadEnergyImport(t, c, data)
	if code := energyConflictCode(t, postEnergyRun(c, draft.ID, nil)); code != "import_not_checked" {
		t.Errorf("run on a draft: %q", code)
	}
	if code := energyConflictCode(t, c.Do(http.MethodPost, energyImportPath(draft.ID, "/stop"), nil)); code != "import_finished" {
		t.Errorf("stop on a draft: %q", code)
	}
	if code := energyConflictCode(t, postEnergyAttach(c, draft.ID, h.Now(), 0)); code != "import_nothing_pending" {
		t.Errorf("attach on a draft: %q", code)
	}
	if r := postEnergyCheck(c, draft.ID); r.Status != http.StatusBadRequest {
		t.Errorf("check with no mapping: status %d, want 400", r.Status)
	}

	// A check asked for and not started: running; stopped, it is a draft again.
	energyImportOf(t, putEnergyMapping(c, draft.ID, fullMapping("create")), http.StatusOK)
	energyImportOf(t, postEnergyCheck(c, draft.ID), http.StatusOK)
	for name, r := range map[string]*modtest.Response{
		"mapping": putEnergyMapping(c, draft.ID, fullMapping("create")),
		"check":   postEnergyCheck(c, draft.ID),
		"run":     postEnergyRun(c, draft.ID, nil),
		"attach":  postEnergyAttach(c, draft.ID, h.Now(), 0),
		"delete":  c.Do(http.MethodDelete, energyImportPath(draft.ID, ""), nil),
	} {
		if code := energyConflictCode(t, r); code != "import_running" {
			t.Errorf("%s while checking: %q, want import_running", name, code)
		}
	}
	if stopped := energyImportOf(t, c.Do(http.MethodPost, energyImportPath(draft.ID, "/stop"), nil), http.StatusOK); stopped.State != "draft" {
		t.Errorf("a stopped unstarted check is %q, want draft", stopped.State)
	}

	// Nothing to import: a check where every row fails.
	failing := checkEnergyImport(t, h, c, fileOf(pointHeader, pointRow("707057599", "", "")), fullMapping("create"))
	if code := energyConflictCode(t, postEnergyRun(c, failing.ID, nil)); code != "import_nothing_to_import" {
		t.Errorf("run with nothing: %q", code)
	}

	// Done: the mapping, the check and the run are finished; the attach with no
	// pending row has nothing pending.
	done := importEnergyNow(t, h, c, data, fullMapping("create"))
	for name, r := range map[string]*modtest.Response{
		"mapping": putEnergyMapping(c, done.ID, fullMapping("create")),
		"check":   postEnergyCheck(c, done.ID),
		"run":     postEnergyRun(c, done.ID, nil),
	} {
		if code := energyConflictCode(t, r); code != "import_finished" {
			t.Errorf("%s on a done import: %q, want import_finished", name, code)
		}
	}
	if code := energyConflictCode(t, postEnergyAttach(c, done.ID, h.Now(), 0)); code != "import_nothing_pending" {
		t.Errorf("attach with none pending: %q", code)
	}

	// An attach asked for and not started stops back to done.
	pending := importEnergyNow(t, h, c, fileOf(pointHeader, pointRow(gsrn2, orgA, "")), fullMapping("create"))
	energyImportOf(t, postEnergyAttach(c, pending.ID, h.Now(), 0), http.StatusOK)
	if stopped := energyImportOf(t, c.Do(http.MethodPost, energyImportPath(pending.ID, "/stop"), nil), http.StatusOK); stopped.State != "done" {
		t.Errorf("a stopped unstarted attach is %q, want done", stopped.State)
	}

	// The 404s.
	for _, r := range []*modtest.Response{
		c.Do(http.MethodGet, energyImportPath(999999, ""), nil),
		c.Do(http.MethodGet, energyImportPath(999999, "/rows"), nil),
		c.Do(http.MethodGet, energyImportPath(999999, "/failed-rows"), nil),
		c.Do(http.MethodDelete, energyImportPath(999999, ""), nil),
	} {
		if r.Status != http.StatusNotFound {
			t.Errorf("status %d body %s, want 404", r.Status, r.Body)
		}
	}
	// A done import is deleted, its points kept.
	if r := c.Do(http.MethodDelete, energyImportPath(done.ID, ""), nil); r.Status != http.StatusNoContent {
		t.Errorf("delete: status %d, want 204", r.Status)
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.metering_points WHERE gsrn = $1`, gsrn1); n != 1 {
		t.Error("deleting the import took its point with it")
	}
}

func TestEnergyImport_ThePermissionsPerMappedField(t *testing.T) {
	t.Parallel()
	h, owner, _ := importHarness(t)
	imp := uploadEnergyImport(t, owner, fileOf(pointHeader, pointRow(gsrn1, orgA, "")))
	door := []string{"energy:import", "energy:metering-points-view"}
	cases := []struct {
		name    string
		extra   []string
		mapping energyMappingJSON
		field   string
		key     string
	}{
		{"update without metering-points-manage", nil, mappingByFields(pointHeader, "update", "gsrn", "city"), "mode", "energy:metering-points-manage"},
		{"create without meters-manage", []string{"energy:metering-points-manage"}, mappingByFields(pointHeader, "create", "gsrn", "city"), "mode", "energy:meters-manage"},
		{"customerLegalId without supply-periods-manage", []string{"energy:metering-points-manage"}, mappingByFields(pointHeader, "update", "gsrn", "customerLegalId"), "columns", "energy:supply-periods-manage"},
	}
	for _, tc := range cases {
		c := h.SignIn(t, append(append([]string{}, door...), tc.extra...)...)
		errs := energyMappingRefusal(t, putEnergyMapping(c, imp.ID, tc.mapping))
		if !strings.Contains(strings.Join(errs[tc.field], " "), tc.key) {
			t.Errorf("%s: errors %v, want %s named on %s", tc.name, errs, tc.key, tc.field)
		}
	}
	// Holding what the mapping needs, it saves.
	enough := h.SignIn(t, append(door, "energy:metering-points-manage", "energy:supply-periods-manage")...)
	energyImportOf(t, putEnergyMapping(enough, imp.ID, mappingByFields(pointHeader, "update", "gsrn", "customerLegalId")), http.StatusOK)
	// The attach needs supply-periods-manage at the door.
	if r := h.SignIn(t, door...).Do(http.MethodPost, energyImportPath(imp.ID, "/attach"), map[string]any{"start": h.Now()}); r.Status != http.StatusForbidden {
		t.Errorf("attach without supply-periods-manage: status %d, want 403", r.Status)
	}
}

func TestEnergyImport_TheMappingsOwnRules(t *testing.T) {
	t.Parallel()
	_, c, _ := importHarness(t)
	imp := uploadEnergyImport(t, c, fileOf(pointHeader, pointRow(gsrn1, orgA, "")))
	col := func(i int) *int { return &i }
	cases := []struct {
		name    string
		mapping energyMappingJSON
		field   string
		want    string
	}{
		{"an unknown mode", energyMappingJSON{Mode: "replace", Columns: fullMapping("create").Columns}, "mode", "A mode must be one of"},
		{"an unknown field", energyMappingJSON{Mode: "create", Columns: append(fullMapping("create").Columns, energyMappingColumnJSON{Field: "owner", Value: ptr("x")})}, "columns", "not a field an import writes"},
		{"gsrn as a fixed value", energyMappingJSON{Mode: "update", Columns: []energyMappingColumnJSON{{Field: "gsrn", Value: ptr(gsrn1)}, {Field: "city", Column: col(4)}}}, "columns", "gives gsrn one value for every row"},
		{"no gsrn", energyMappingJSON{Mode: "update", Columns: []energyMappingColumnJSON{{Field: "city", Column: col(4)}}}, "columns", "A column must be mapped to 'gsrn'"},
		{"gsrn alone", energyMappingJSON{Mode: "update", Columns: []energyMappingColumnJSON{{Field: "gsrn", Column: col(0)}}}, "columns", "No field is mapped besides gsrn"},
		{"supplyStart without customerLegalId", energyMappingJSON{Mode: "update", Columns: []energyMappingColumnJSON{{Field: "gsrn", Column: col(0)}, {Field: "supplyStart", Column: col(7)}}}, "columns", "needs customerLegalId"},
		{"a column twice", energyMappingJSON{Mode: "update", Columns: []energyMappingColumnJSON{{Field: "gsrn", Column: col(0)}, {Field: "city", Column: col(0)}}}, "columns", "maps column 0 a second time"},
		{"a column outside the header", energyMappingJSON{Mode: "update", Columns: []energyMappingColumnJSON{{Field: "gsrn", Column: col(0)}, {Field: "city", Column: col(40)}}}, "columns", "names column 40"},
		{"both a column and a value", energyMappingJSON{Mode: "update", Columns: []energyMappingColumnJSON{{Field: "gsrn", Column: col(0)}, {Field: "city", Column: col(4), Value: ptr("Oslo")}}}, "columns", "both a column and a value"},
	}
	for _, tc := range cases {
		errs := energyMappingRefusal(t, putEnergyMapping(c, imp.ID, tc.mapping))
		if !strings.Contains(strings.Join(errs[tc.field], " "), tc.want) {
			t.Errorf("%s: errors %v, want %q on %s", tc.name, errs, tc.want, tc.field)
		}
	}
	// A fixed value for every row is taken: every point in this file is NO3.
	fixed := fullMapping("create")
	for i := range fixed.Columns {
		if fixed.Columns[i].Field == "priceArea" {
			fixed.Columns[i] = energyMappingColumnJSON{Field: "priceArea", Value: ptr("NO 3")}
		}
	}
	saved := energyImportOf(t, putEnergyMapping(c, imp.ID, fixed), http.StatusOK)
	if len(saved.Sample) != 1 || len(saved.Sample[0].Errors) != 0 {
		t.Errorf("sample = %+v, want the row clean under a fixed price area", saved.Sample)
	}
}

func TestEnergyImport_TheSampleShowsEachRowsErrors(t *testing.T) {
	t.Parallel()
	_, c, _ := importHarness(t)
	imp := uploadEnergyImport(t, c, fileOf(pointHeader, pointRow(gsrn1, "", ""), pointRow(gsrn2, "", "")[:3], []string{"", "", "", "", "", "", "", "9000"}))
	if len(imp.Sample) != 3 || len(imp.Sample[0].Errors) != 0 {
		t.Fatalf("sample = %+v, want three rows and no errors before a mapping", imp.Sample)
	}
	saved := energyImportOf(t, putEnergyMapping(c, imp.ID, fullMapping("create")), http.StatusOK)
	if len(saved.Sample[0].Errors) != 0 || len(saved.Sample[1].Errors) == 0 || len(saved.Sample[2].Errors) != 0 {
		t.Errorf("sample = %+v, want the short row's errors and none on the clean row or the totals line", saved.Sample)
	}
}

func TestEnergyImport_TheCustomersModuleOff(t *testing.T) {
	t.Parallel()
	h := modtest.New(t, modtest.WithRecorder(recorder), modtest.WithModule(energy.Module()))
	c := h.SignIn(t, importPermissions...)
	imp := uploadEnergyImport(t, c, fileOf(pointHeader, pointRow(gsrn1, orgA, "")))
	errs := energyMappingRefusal(t, putEnergyMapping(c, imp.ID, fullMapping("create")))
	if !strings.Contains(strings.Join(errs["columns"], " "), "The customers module is off") {
		t.Errorf("errors %v, want customerLegalId refused with the customers module off", errs)
	}
	// Without the customer columns the points import.
	header := pointHeader[:6]
	done := importEnergyNow(t, h, c, fileOf(header, pointRow(gsrn1, "", "")[:6]), mappingByFields(header, "create", header...))
	if done.Created != 1 {
		t.Errorf("done = %+v, want the point created", done)
	}
	rows := energyImportRows(t, c, done.ID, "")
	if rows[0].Link != "none" || rows[0].CustomerName != "" {
		t.Errorf("row = %+v, want link none and no customer named", rows[0])
	}
}

func TestEnergyImport_TheFailedRowsFile(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t)
	data := fileOf(pointHeader, pointRow(gsrn1, "", ""), pointRow("123456789012", "", ""), pointRow(gsrn2, "=1+1", ""))
	checked := checkEnergyImport(t, h, c, data, fullMapping("create"))
	r := c.Do(http.MethodGet, energyImportPath(checked.ID, "/failed-rows"), nil)
	if r.Status != http.StatusOK || !strings.HasPrefix(r.Header("Content-Type"), "text/csv") ||
		!strings.Contains(r.Header("Content-Disposition"), fmt.Sprintf("energy-import-%d-failed-rows.csv", checked.ID)) {
		t.Fatalf("failed rows: status %d, headers %q %q", r.Status, r.Header("Content-Type"), r.Header("Content-Disposition"))
	}
	body := string(r.Body)
	lines := strings.Split(strings.TrimSuffix(strings.TrimPrefix(body, "\ufeff"), "\r\n"), "\r\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "error;gsrn;") {
		t.Fatalf("file = %q, want the header and the two failed rows", body)
	}
	if !strings.HasPrefix(lines[1], "gsrn: GSRN must contain exactly 18 digits.;123456789012;") {
		t.Errorf("line 1 = %q, want the GSRN's error on its column", lines[1])
	}
	if !strings.Contains(lines[2], ";'=1+1;") {
		t.Errorf("line 2 = %q, want the formula guarded", lines[2])
	}
	// The list and the rows pages.
	list := c.Do(http.MethodGet, "/api/v1/energy/imports?page=1&pageSize=10", nil)
	var page struct {
		Data       []energyImportJSON `json:"data"`
		Pagination struct {
			TotalCount int `json:"totalCount"`
		} `json:"pagination"`
	}
	list.JSON(&page)
	if list.Status != http.StatusOK || page.Pagination.TotalCount != 1 || page.Data[0].ID != checked.ID {
		t.Errorf("list: status %d %+v, want the one import", list.Status, page)
	}
	for _, q := range []string{"?pageSize=0", "?page=0", "?outcome=lost", "?link=maybe"} {
		if r := c.Do(http.MethodGet, energyImportPath(checked.ID, "/rows")+q, nil); r.Status != http.StatusBadRequest {
			t.Errorf("rows%s: status %d, want 400", q, r.Status)
		}
	}
	if r := c.Do(http.MethodGet, "/api/v1/energy/imports?pageSize=101", nil); r.Status != http.StatusBadRequest {
		t.Errorf("list pageSize=101: status %d, want 400", r.Status)
	}
}

func TestEnergyImport_TheUploadsRefusals(t *testing.T) {
	t.Parallel()
	_, c, _ := importHarness(t)
	if r := postEnergyUpload(t, c, nil); r.Status != http.StatusBadRequest {
		t.Errorf("an empty file: status %d, want 400", r.Status)
	}
	if r := postEnergyUpload(t, c, []byte("\r\n\r\n")); r.Status != http.StatusBadRequest {
		t.Errorf("a file with no header: status %d, want 400", r.Status)
	}
	imp := uploadEnergyImport(t, c, fileOf(pointHeader, pointRow(gsrn1, "", "")))
	if imp.CreatedByName == "" || imp.FileName != "points.csv" {
		t.Errorf("upload = %+v, want the uploader named and the file's name kept", imp)
	}
}

func TestGetMeteringPoints_WithoutCustomer(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t)
	now := h.Now()
	none := createPointByHand(t, c, gsrn1, "M1")
	active := createPointByHand(t, c, gsrn2, "M2")
	ended := createPointByHand(t, c, gsrn3, "M3")
	future := createPointByHand(t, c, gsrn4, "M4")
	assignByHand(t, c, active, 1001, now.AddDate(0, -1, 0))
	assignByHand(t, c, ended, 1001, now.AddDate(0, -2, 0))
	assignByHand(t, c, future, 1001, now.AddDate(0, 1, 0))
	var periods []supplyPeriodJSON
	c.Do(http.MethodGet, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", ended), nil).JSON(&periods)
	if r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods/%d/end", ended, periods[0].Id),
		map[string]any{"end": now.AddDate(0, 0, -1)}); r.Status != http.StatusOK {
		t.Fatalf("end: status %d body %s", r.Status, r.Body)
	}
	ids := func(query string) []int32 {
		r := c.Do(http.MethodGet, "/api/v1/energy/metering-points"+query, nil)
		var page struct {
			Data       []meteringPointJSON `json:"data"`
			Pagination struct {
				TotalCount int `json:"totalCount"`
			} `json:"pagination"`
		}
		r.JSON(&page)
		var out []int32
		for _, p := range page.Data {
			out = append(out, p.Id)
		}
		if page.Pagination.TotalCount != len(out) {
			t.Errorf("%s: total %d, %d listed", query, page.Pagination.TotalCount, len(out))
		}
		return out
	}
	if got := ids("?withoutCustomer=true"); fmt.Sprint(got) != fmt.Sprint([]int32{none, ended, future}) {
		t.Errorf("withoutCustomer=true = %v, want %v (no period, ended, starting later)", got, []int32{none, ended, future})
	}
	if got := ids("?withoutCustomer=false"); len(got) != 4 {
		t.Errorf("withoutCustomer=false = %v, want every point", got)
	}
	if got := ids("?withoutCustomer=true&search=" + gsrn1); fmt.Sprint(got) != fmt.Sprint([]int32{none}) {
		t.Errorf("withoutCustomer with search = %v, want %v", got, []int32{none})
	}
}

// TestEnergyImport_AnUpdateKeepsThePointsBillingFacts pins energy tariffs
// plan reading 6: the file carries no contract, so an update of a matched
// point keeps its grid tariff, household use and tax class, whatever else it
// writes.
func TestEnergyImport_AnUpdateKeepsThePointsBillingFacts(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t)
	existing := createPointByHand(t, c, gsrn1, "M-0012")
	var tariffID int32
	if err := h.Pool().QueryRow(t.Context(), `INSERT INTO energy.tariffs (kind, name, status, created_at, updated_at)
		VALUES ('grid', 'Nett', 'active', now(), now()) RETURNING id`).Scan(&tariffID); err != nil {
		t.Fatal(err)
	}
	h.Exec(t, `UPDATE energy.metering_points SET grid_tariff_id = $2, household = true, electricity_tax_class = 'reduced' WHERE id = $1`, existing, tariffID)

	header := []string{"gsrn", "streetAddress", "postalCode", "city", "priceArea", "gridArea"}
	updated := importEnergyNow(t, h, c, fileOf(header, []string{gsrn1, "Storgata 1", "9008", "Tromsø", "NO4", "Nett nord"}),
		mappingByFields(header, "update", header...))
	if updated.Updated != 1 || updated.Failed != 0 {
		t.Fatalf("update = %+v, want the point updated", updated)
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.metering_points
		WHERE id = $1 AND city = 'Tromsø' AND grid_tariff_id = $2 AND household AND electricity_tax_class = 'reduced'`, existing, tariffID); n != 1 {
		t.Error("the import's update did not keep the point's grid tariff, household use and tax class")
	}
}
