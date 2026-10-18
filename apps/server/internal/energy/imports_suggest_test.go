package energy_test

import (
	"net/http"
	"testing"
	"time"
)

// This file is the supplier's export (energy import design D3): the header a
// supplier's export of active business customers has, word for word, with
// synthetic rows — check-digit-valid GSRNs and organisation numbers that are
// nobody's — a UTF-8 byte order mark, CRLF, day-first dates with slashes, a
// decimal comma, a spaced price area and the totals line such exports end
// with. It must map itself under the alias table, and import.

// supplierHeader is the export's header as it is written.
var supplierHeader = []string{
	"KUNDENR", "KUNDENAVN", "ORG. NR.", "EPOST", "MOBILNR", "POSTADRESSE", "POSTNR", "POSTSTED",
	"MÅLEPUNKTID", "MÅLERNR", "PRISOMRÅDE", "NETTEIER", "ANLEGGSADRESSE", "POSTNR ANLEGG",
	"POSTSTED ANLEGG", "KOMMUNE ANLEGG", "STARTDATO KUNDE", "FORVENTET ÅRSFORBRUK kWh", "Norgespris fra",
}

// supplierFile is the export: three customers, four points — one customer on
// two of them, one point with no start date — and the totals line.
func supplierFile() []byte {
	return fileOf(
		supplierHeader,
		[]string{"10001", "Aurora Kraft AS", "910 000 004", "post@aurora.test", "40000001", "Postboks 1", "0101", "OSLO",
			gsrn1, "M1001", "NO 1", "Nettselskap Øst AS", "Industriveien 1", "0581", "OSLO", "OSLO", "01/01/2026", "120000", ""},
		[]string{"10001", "Aurora Kraft AS", "910000004", "post@aurora.test", "40000001", "Postboks 1", "0101", "OSLO",
			gsrn2, "M1002", "NO1", "Nettselskap Øst AS", "Lagerveien 2", "0582", "OSLO", "OSLO", "01/01/2026", "85 000,5", "01.10.2025"},
		[]string{"10002", "Bølge Energi AS", "910000012", "post@bolge.test", "40000002", "Postboks 2", "7001", "TRONDHEIM",
			gsrn3, "M1003", "NO 3", "Nettselskap Midt AS", "Kaia 3", "7010", "TRONDHEIM", "TRONDHEIM", "15/02/2026", "300000", ""},
		[]string{"10003", "Cirrus Drift AS", "910000020", "post@cirrus.test", "40000003", "Postboks 3", "5001", "BERGEN",
			gsrn4, "M1004", "NO 5", "Nettselskap Vest AS", "Bryggen 4", "5003", "BERGEN", "BERGEN", "", "5000", ""},
		[]string{"", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "8247598", ""},
	)
}

func TestEnergyImport_TheSuppliersExportMapsItself(t *testing.T) {
	t.Parallel()
	_, c, _ := importHarness(t)
	imp := uploadEnergyImport(t, c, supplierFile())
	if imp.State != "draft" || imp.Encoding != "utf-8" || imp.Separator != ";" || imp.Rows != 5 || len(imp.Header) != 19 {
		t.Fatalf("upload = %+v, want a draft of five rows under nineteen columns, UTF-8 and semicolons", imp)
	}
	got := map[string]int{}
	for _, col := range imp.Suggested.Columns {
		if col.Column == nil {
			t.Fatalf("suggested %s with a value, want a column", col.Field)
		}
		got[col.Field] = *col.Column
	}
	want := map[string]int{
		"customerLegalId": 2, "gsrn": 8, "meterNumber": 9, "priceArea": 10, "gridArea": 11, "streetAddress": 12,
		"postalCode": 13, "city": 14, "supplyStart": 16, "expectedAnnualConsumptionKwh": 17,
	}
	if len(got) != len(want) {
		t.Errorf("suggested %v, want exactly %v", got, want)
	}
	for field, column := range want {
		if got[field] != column {
			t.Errorf("%s suggested from column %d (%q), want %d (%q)", field, got[field], supplierHeader[got[field]], column, supplierHeader[column])
		}
	}
	if imp.Suggested.Mode != "createOrUpdate" {
		t.Errorf("suggested mode %q, want createOrUpdate", imp.Suggested.Mode)
	}
}

func TestEnergyImport_TheSuppliersExportImports(t *testing.T) {
	t.Parallel()
	h, c, dir := importHarness(t)
	imp := uploadEnergyImport(t, c, supplierFile())
	checked := checkEnergyImport(t, h, c, supplierFile(), imp.Suggested)
	if checked.Created != 4 || checked.Failed != 0 || checked.Skipped != 1 || checked.Pending != 1 || checked.Switched != 0 {
		t.Fatalf("checked = %+v, want four to create, the totals line skipped and one pending", checked)
	}
	done := runEnergyImport(t, h, c, checked)
	if done.Created != 4 || done.Failed != 0 || done.Skipped != 1 || done.Pending != 1 || done.Processed != 5 {
		t.Fatalf("done = %+v, want four created, the totals line skipped and one pending", done)
	}
	// Customers were resolved once per run: the check's and the run's.
	if n := dir.lookups.Load(); n != 2 {
		t.Errorf("CustomersByLegalID called %d times, want once per run (2)", n)
	}

	// The points carry the installation's address, the normalised price area,
	// the decimal comma read as a point, and their first meter.
	var street, postal, city, priceArea, grid string
	var expected float64
	if err := h.Pool().QueryRow(t.Context(), `SELECT street_address, postal_code, city, price_area, grid_area, expected_annual_consumption_kwh::float8
		FROM energy.metering_points WHERE gsrn = $1`, gsrn2).Scan(&street, &postal, &city, &priceArea, &grid, &expected); err != nil {
		t.Fatal(err)
	}
	if street != "Lagerveien 2" || postal != "0582" || city != "OSLO" || priceArea != "NO1" || grid != "Nettselskap Øst AS" || expected != 85000.5 {
		t.Errorf("point %s = %s, %s, %s, %s, %s, %v; want the installation's address, NO1 and 85000.5", gsrn2, street, postal, city, priceArea, grid, expected)
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.meters m JOIN energy.metering_points mp ON mp.id = m.metering_point_id
		WHERE mp.gsrn = $1 AND m.meter_number = 'M1003' AND m.removed_at IS NULL`, gsrn3); n != 1 {
		t.Errorf("%d current meters M1003 on %s, want 1", n, gsrn3)
	}
	// The three points with a start date are supplied from it, at midnight
	// UTC; the one without is pending, matched to its customer.
	for _, tc := range []struct {
		gsrn     string
		customer int32
		start    time.Time
	}{
		{gsrn1, 1001, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{gsrn2, 1001, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{gsrn3, 1002, time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC)},
	} {
		var customer int32
		var start time.Time
		if err := h.Pool().QueryRow(t.Context(), `SELECT p.customer_id, p.start FROM energy.supply_periods p
			JOIN energy.metering_points mp ON mp.id = p.metering_point_id WHERE mp.gsrn = $1 AND p.status = 'Active'`, tc.gsrn).Scan(&customer, &start); err != nil {
			t.Fatal(err)
		}
		if customer != tc.customer || !start.Equal(tc.start) {
			t.Errorf("%s supplied to %d from %v, want %d from %v", tc.gsrn, customer, start, tc.customer, tc.start)
		}
	}
	if activeCustomer(t, h, gsrn4) != 0 {
		t.Errorf("%s has a supply period, want none (no start date)", gsrn4)
	}
	rows := energyImportRows(t, c, done.ID, "")
	if r := rowNamed(t, rows, 4); r.Link != "pending" || r.CustomerID == nil || *r.CustomerID != 1003 || r.CustomerName != "Cirrus Drift AS" {
		t.Errorf("row 4 = %+v, want pending for customer 1003 Cirrus Drift AS", r)
	}
	if r := rowNamed(t, rows, 5); r.Outcome != "skipped" || len(r.Errors) != 1 {
		t.Errorf("the totals line = %+v, want skipped with its reason", r)
	}
	if r := rowNamed(t, rows, 1); r.Link != "assigned" || r.MeteringPointID == nil || r.Gsrn != gsrn1 {
		t.Errorf("row 1 = %+v, want assigned with its point", r)
	}
}

func TestEnergyImport_TheInstallationAliasWinsWhateverTheOrder(t *testing.T) {
	t.Parallel()
	_, c, _ := importHarness(t)
	for _, header := range [][]string{
		{"målepunkt-id", "Postnr", "Poststed", "Adresse", "Postnr anlegg", "Poststed anlegg", "Anleggsadresse"},
		{"målepunkt-id", "Postnr anlegg", "Poststed anlegg", "Anleggsadresse", "Postnr", "Poststed", "Adresse"},
	} {
		imp := uploadEnergyImport(t, c, fileOf(header, []string{gsrn1, "0101", "OSLO", "Postboks 1", "0581", "OSLO", "Industriveien 1"}))
		for _, col := range imp.Suggested.Columns {
			name := header[*col.Column]
			switch col.Field {
			case "postalCode", "city", "streetAddress":
				if name != "Postnr anlegg" && name != "Poststed anlegg" && name != "Anleggsadresse" {
					t.Errorf("header %v: %s suggested from %q, want the installation's column", header, col.Field, name)
				}
			}
		}
		if len(imp.Suggested.Columns) != 4 {
			t.Errorf("header %v: suggested %+v, want gsrn and the installation's three", header, imp.Suggested.Columns)
		}
	}
}

func TestEnergyImport_TheLastMappingForAHeaderIsSuggested(t *testing.T) {
	t.Parallel()
	_, c, _ := importHarness(t)
	data := fileOf(pointHeader, pointRow(gsrn1, "", ""))
	first := uploadEnergyImport(t, c, data)
	mapping := mappingByFields(pointHeader, "update", "gsrn", "city")
	energyImportOf(t, putEnergyMapping(c, first.ID, mapping), http.StatusOK)
	second := uploadEnergyImport(t, c, data)
	if second.Suggested.Mode != "update" || len(second.Suggested.Columns) != 2 {
		t.Errorf("suggested %+v, want the mapping saved on the same header", second.Suggested)
	}
}
