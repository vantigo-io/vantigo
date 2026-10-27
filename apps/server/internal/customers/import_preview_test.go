package customers_test

import (
	"net/http"
	"slices"
	"strconv"
	"testing"
)

// This file is the check's preview (import_worker.go): what a row the check
// found would be created or updated would write — the resolved name, type,
// identity and postal address, and the fields Brønnøysund filled in — kept on
// the row for the checked page, never on a failed row, and gone once the run
// has run. Every registry here is registry_test.go's fake transport.

// rowsByNumber is an import's rows keyed by their row number.
func rowsByNumber(page importRowsPageJSON) map[int]importRowJSON {
	rows := map[int]importRowJSON{}
	for _, r := range page.Data {
		rows[r.Row] = r
	}
	return rows
}

func TestImportPreview_TheCheckRecordsWhatEachCreateWouldWrite_AndTheRunClearsIt(t *testing.T) {
	t.Parallel()
	registry := registryOf(map[string]string{validOrgNumbers[0]: equinorRegistryBody})
	h := newRegistryHarness(t, registry)
	c, _ := uploadClient(t, h)

	header := []string{"Navn", "Org.nr", "Adresse", "Postnr", "Poststed", "Land"}
	checked := checkNow(t, c, h,
		csvFileOf(header,
			// Name and address left to the registry.
			[]string{"", validOrgNumbers[0], "", "", "", ""},
			// Complete in the file: nothing from the registry.
			[]string{"Komplett AS", validOrgNumbers[2], "Storgata 1", "0150", "Oslo", "NO"},
			// No country: refused, with no preview.
			[]string{"Feil AS", "", "Storgata 1", "", "", ""}),
		mappingOf("create", "", "name", "legalId", "postalLine1", "postalPostalCode", "postalCity", "postalCountry"))
	if checked.Created != 2 || checked.Failed != 1 {
		t.Fatalf("checked = %+v, errors %v", checked, importResultOf(t, c, checked).Errors)
	}
	rows := rowsByNumber(importRowsOf(t, c, checked.ID, ""))

	filled := rows[1].Preview
	if filled == nil {
		t.Fatalf("row 1 = %+v, want a preview", rows[1])
	}
	if filled.Name != "EQUINOR ASA" || filled.Type != "business" {
		t.Errorf("row 1 preview = %+v, want the registry's name on a business", filled)
	}
	if id := filled.LegalIdentity; id == nil || id.Country != "no" || id.ID != validOrgNumbers[0] || id.Name != "EQUINOR ASA" || id.Source != "brreg" {
		t.Errorf("row 1 identity = %+v, want the registry's", filled.LegalIdentity)
	}
	if a := filled.PostalAddress; a == nil || a.Line1 != "Postboks 8500" || a.Line2 != nil || deref(a.PostalCode) != "4035" || deref(a.City) != "STAVANGER" || a.Country != "no" {
		t.Errorf("row 1 address = %+v, want the registry's postal address", filled.PostalAddress)
	}
	if want := []string{"name", "legalName", "postalLine1", "postalPostalCode", "postalCity", "postalCountry"}; !slices.Equal(filled.FromRegistry, want) {
		t.Errorf("row 1 fromRegistry = %v, want %v", filled.FromRegistry, want)
	}

	complete := rows[2].Preview
	if complete == nil {
		t.Fatalf("row 2 = %+v, want a preview", rows[2])
	}
	if complete.Name != "Komplett AS" || complete.LegalIdentity == nil || complete.LegalIdentity.Name != "Komplett AS" || complete.LegalIdentity.Source != "manual" {
		t.Errorf("row 2 preview = %+v, want the file's name and a manual identity", complete)
	}
	if a := complete.PostalAddress; a == nil || a.Line1 != "Storgata 1" || deref(a.PostalCode) != "0150" || deref(a.City) != "Oslo" || a.Country != "no" {
		t.Errorf("row 2 address = %+v, want the file's", complete.PostalAddress)
	}
	if complete.FromRegistry == nil || len(complete.FromRegistry) != 0 {
		t.Errorf("row 2 fromRegistry = %#v, want an empty list", complete.FromRegistry)
	}

	if rows[3].Outcome != "failed" || rows[3].Preview != nil {
		t.Errorf("row 3 = %+v, want failed with no preview", rows[3])
	}

	// The run's rows carry no preview: a created row is the customer itself.
	importOf(t, postRun(t, c, checked.ID), http.StatusOK)
	runImportWorker(t, h)
	if done := getImport(t, c, checked.ID); done.State != "done" || done.Created != 2 {
		t.Fatalf("after the run = %+v", done)
	}
	for _, r := range importRowsOf(t, c, checked.ID, "").Data {
		if r.Preview != nil {
			t.Errorf("row %d (%s) kept the check's preview after the run: %+v", r.Row, r.Outcome, r.Preview)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.import_rows WHERE import_id = $1 AND preview IS NOT NULL`, checked.ID); n != 0 {
		t.Errorf("%d rows hold a preview after the run, want none", n)
	}
}

func TestImportPreview_AnUpdateFillingAGapMarksTheRegistrysParts(t *testing.T) {
	t.Parallel()
	registry := registryOf(map[string]string{validOrgNumbers[1]: kontorRegistryBody})
	h := newRegistryHarness(t, registry)
	c, _ := uploadClient(t, h)
	cust := createCustomerWithIdentity(t, c, "No Address AS", "no", validOrgNumbers[1])

	checked := checkNow(t, c, h,
		csvFileOf([]string{"Org.nr", "Poststed"}, []string{validOrgNumbers[1], "Fana"}),
		mappingOf("update", "legalId", "legalId", "postalCity"))
	if checked.Updated != 1 {
		t.Fatalf("checked = %+v, errors %v", checked, importResultOf(t, c, checked).Errors)
	}
	page := importRowsOf(t, c, checked.ID, "updated")
	if len(page.Data) != 1 || page.Data[0].Preview == nil {
		t.Fatalf("updated rows = %+v, want one with a preview", page.Data)
	}
	preview := page.Data[0].Preview
	// The name and the identity on file stand; the address is the registry's
	// under the file's city.
	if preview.Name != "No Address AS" || preview.LegalIdentity == nil || preview.LegalIdentity.Source != "manual" || preview.LegalIdentity.ID != validOrgNumbers[1] {
		t.Errorf("preview = %+v, want the name and identity on file", preview)
	}
	if a := preview.PostalAddress; a == nil || a.Line1 != "Strandkaien 2" || deref(a.Line2) != "3. etasje" || deref(a.PostalCode) != "5003" || deref(a.City) != "Fana" || a.Country != "no" {
		t.Errorf("address = %+v, want the registry's under the file's city", preview.PostalAddress)
	}
	if want := []string{"postalLine1", "postalLine2", "postalPostalCode", "postalCountry"}; !slices.Equal(preview.FromRegistry, want) {
		t.Errorf("fromRegistry = %v, want %v", preview.FromRegistry, want)
	}
	if page.Data[0].CustomerID == nil || *page.Data[0].CustomerID != cust.Id {
		t.Errorf("customer = %v, want %d", page.Data[0].CustomerID, cust.Id)
	}
}

func TestImportPreview_AFileNameKeepsTheRegistrysLegalNameFlaggedAlone(t *testing.T) {
	t.Parallel()
	registry := registryOf(map[string]string{validOrgNumbers[0]: equinorRegistryBody})
	h := newRegistryHarness(t, registry)
	c, _ := uploadClient(t, h)

	// The file names the customer and maps no address: the name is the file's,
	// the legal name and the address the registry's.
	checked := checkNow(t, c, h,
		csvFileOf([]string{"Navn", "Org.nr"}, []string{"Equinor", validOrgNumbers[0]}),
		mappingOf("create", "", "name", "legalId"))
	page := importRowsOf(t, c, checked.ID, "created")
	if len(page.Data) != 1 || page.Data[0].Preview == nil {
		t.Fatalf("created rows = %+v, want one with a preview", page.Data)
	}
	preview := page.Data[0].Preview
	if preview.Name != "Equinor" || preview.LegalIdentity == nil || preview.LegalIdentity.Name != "EQUINOR ASA" || preview.LegalIdentity.Source != "brreg" {
		t.Errorf("preview = %+v, want the file's name and the registry's legal name", preview)
	}
	if slices.Contains(preview.FromRegistry, "name") || !slices.Contains(preview.FromRegistry, "legalName") {
		t.Errorf("fromRegistry = %v, want legalName flagged and the name not", preview.FromRegistry)
	}
}

func TestImportPreview_AnUpdateShowsTheAddressItKeeps_OrThatItIsRemoved(t *testing.T) {
	t.Parallel()
	h := newRegistryHarness(t, registryOf(nil))
	c, _ := uploadClient(t, h)
	keeps := createCustomer(t, c, "Keeps AS")
	clears := createCustomer(t, c, "Clears AS")
	// The region is not mapped below, so only the kept address has one: a
	// region left on file would keep the cleared address from being blank.
	for id, region := range map[int32]any{keeps.Id: "Oslo", clears.Id: nil} {
		if r := postAddress(t, c, id, map[string]any{"type": "postal", "line1": "Storgata 1", "postalCode": "0150", "city": "Oslo", "region": region, "country": "NO", "isPrimary": true}); r.Status != http.StatusCreated {
			t.Fatalf("address: %d %s", r.Status, r.Body)
		}
	}
	number := func(n int64) string { return strconv.FormatInt(n, 10) }

	// No postal field mapped: the address on file is what the customer keeps.
	checked := checkNow(t, c, h,
		csvFileOf([]string{"Kundenr", "E-post"}, []string{number(keeps.CustomerNumber), "post@keeps.no"}),
		mappingOf("update", "customerNumber", "customerNumber", "email"))
	rows := importRowsOf(t, c, checked.ID, "updated").Data
	if len(rows) != 1 || rows[0].Preview == nil {
		t.Fatalf("updated rows = %+v, want one with a preview", rows)
	}
	if p := rows[0].Preview; p.PostalAddress == nil || p.PostalAddress.Line1 != "Storgata 1" || deref(p.PostalAddress.Region) != "Oslo" || p.Cleared {
		t.Errorf("preview = %+v, want the address on file kept", p)
	}

	// Every postal cell blank: the run removes the address, and the preview says so.
	header := []string{"Kundenr", "Adresse", "Postnr", "Poststed", "Land"}
	checked = checkNow(t, c, h,
		csvFileOf(header, []string{number(clears.CustomerNumber), "", "", "", ""}),
		mappingOf("update", "customerNumber", "customerNumber", "postalLine1", "postalPostalCode", "postalCity", "postalCountry"))
	rows = importRowsOf(t, c, checked.ID, "updated").Data
	if len(rows) != 1 || rows[0].Preview == nil {
		t.Fatalf("updated rows = %+v, want one with a preview", rows)
	}
	if p := rows[0].Preview; p.PostalAddress != nil || !p.Cleared {
		t.Errorf("preview = %+v, want no address and the removal marked", p)
	}
}

func TestImportPreview_AStoppedCheckAndAMappingChangeClearThePreviews(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := uploadClient(t, h)
	mapping := mappingOf("create", "", "name", "email")
	previews := func(id int32) int {
		return h.Count(t, `SELECT count(*) FROM customers.import_rows WHERE import_id = $1 AND preview IS NOT NULL`, id)
	}

	// A check stopped after its first chunk wrote previews: none is kept.
	imp := uploadImport(t, c, manyRows(700))
	importOf(t, putMapping(t, c, imp.ID, mapping), http.StatusOK)
	importOf(t, postCheck(t, c, imp.ID), http.StatusOK)
	flagStopUnderWay(t, h, imp.ID)
	runImportWorker(t, h)
	if n := previews(imp.ID); n != 0 {
		t.Errorf("%d rows keep a preview after a stopped check", n)
	}

	// A checked import whose mapping is saved again goes back to draft without them.
	checked := checkNow(t, c, h, manyRows(3), mapping)
	if n := previews(checked.ID); n != 3 {
		t.Fatalf("%d previews after the check, want 3", n)
	}
	importOf(t, putMapping(t, c, checked.ID, mapping), http.StatusOK)
	if n := previews(checked.ID); n != 0 {
		t.Errorf("%d rows keep a preview after the mapping changed", n)
	}
}
