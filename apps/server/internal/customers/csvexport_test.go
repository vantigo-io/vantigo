package customers_test

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the customers file on its way out (customers import/export
// design D1, D2): GET /customers/export and GET /customers/import/template.
// The bytes are a contract with a spreadsheet, so the golden test pins them
// exactly; the rest read the file the way a spreadsheet would.

// csvBOM is the UTF-8 byte order mark the file opens with.
const csvBOM = "\xef\xbb\xbf"

// exportProblemJSON is a bare problem's title and detail — the export's 400s
// carry no errors object.
type exportProblemJSON struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// exportCSV GETs the export with query (no leading '?') and fails on anything
// but 200.
func exportCSV(t *testing.T, c *modtest.Client, query string) *modtest.Response {
	t.Helper()
	r := c.Do(http.MethodGet, "/api/v1/customers/export?"+query, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET /customers/export?%s: status %d body %s, want 200", query, r.Status, r.Body)
	}
	return r
}

// exportTable reads a file the way a spreadsheet does: the BOM off, semicolons,
// quotes — header first.
func exportTable(t *testing.T, body []byte) (header []string, rows [][]string) {
	t.Helper()
	reader := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(body, []byte(csvBOM))))
	reader.Comma = ';'
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("read the file: %v\n%s", err, body)
	}
	if len(records) == 0 {
		t.Fatalf("the file has no header row")
	}
	return records[0], records[1:]
}

// exportedNames is the name column of an export, in file order.
func exportedNames(t *testing.T, c *modtest.Client, query string) []string {
	t.Helper()
	header, rows := exportTable(t, exportCSV(t, c, query).Body)
	at := slices.Index(header, "name")
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row[at])
	}
	return names
}

// importableHeader is every column an import writes, in D1's order.
const importableHeader = "customerNumber;name;type;status;legalCountry;legalType;legalId;legalName;email;phone;website;" +
	"postalLine1;postalLine2;postalPostalCode;postalCity;postalRegion;postalCountry;" +
	"invoiceLine1;invoiceLine2;invoicePostalCode;invoiceCity;invoiceRegion;invoiceCountry;" +
	"invoiceEmail;reminderEmail;paymentTermsDays;currency;language;invoiceDelivery;reminderDelivery;peppolId;gln;buyerReference;defaultBillRate;" +
	"group;tags"

// TestGetCustomersExport_IsExactlyTheseBytes is the golden sample: a customer
// with every group filled in — a name carrying the separator and quotes, a phone
// number a spreadsheet would read as a formula, a rate with the decimal comma —
// and a bare one whose very name starts like a formula.
func TestGetCustomersExport_IsExactlyTheseBytes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := authenticatedClientWithID(t, h)
	setDisplayName(t, h, userID, "Kari Nordmann")

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name":        `Fjord; "Nord" AS`,
		"identity":    map[string]any{"country": "no", "type": "business", "id": "923609016", "name": "Fjord Nord AS", "source": "manual"},
		"contactInfo": map[string]any{"email": "post@fjord.no", "phone": "+47 22 33 44 55"},
	})
	if r.Status != http.StatusCreated {
		t.Fatalf("create: status %d body %s", r.Status, r.Body)
	}
	var fjord createdCustomerJSON
	r.JSON(&fjord)
	createAddress(t, c, fjord.Id, map[string]any{"type": "postal", "line1": "Storgata 1", "postalCode": "0155", "city": "Oslo", "country": "no"})
	if r := putBillingProfile(t, c, fjord.Id, map[string]any{"paymentTermsDays": 30, "currency": "NOK", "defaultBillRate": 1250.5}); r.Status != http.StatusOK {
		t.Fatalf("billing: status %d body %s", r.Status, r.Body)
	}
	retail := createGroup(t, c, map[string]any{"name": "Retail"})
	if r := putCustomerGroup(t, c, fjord.Id, map[string]any{"groupId": retail.Id}); r.Status != http.StatusOK {
		t.Fatalf("group: status %d body %s", r.Status, r.Body)
	}
	vip := createTag(t, c, map[string]any{"name": "VIP"})
	key := createTag(t, c, map[string]any{"name": "Key"})
	if r := putCustomerTags(t, c, fjord.Id, []string{vip.Id, key.Id}); r.Status != http.StatusOK {
		t.Fatalf("tags: status %d body %s", r.Status, r.Body)
	}
	if r := putOwner(t, c, fjord.Id, map[string]any{"ownerUserId": userID.String()}); r.Status != http.StatusOK {
		t.Fatalf("owner: status %d body %s", r.Status, r.Body)
	}
	bare := createCustomer(t, c, "=Formel AS")
	stamp := h.Now().UTC().Format(time.RFC3339)

	fjordRow := []string{
		fmt.Sprint(fjord.CustomerNumber), `"Fjord; ""Nord"" AS"`, "business", "active",
		"no", "business", "923609016", "Fjord Nord AS",
		"post@fjord.no", "'+47 22 33 44 55", "",
		"Storgata 1", "", "0155", "Oslo", "", "no",
		"", "", "", "", "", "",
		"", "", "30", "NOK", "", "", "", "", "", "", "1250,50",
		"Retail", "Key|VIP",
		fmt.Sprint(fjord.Id), "Kari Nordmann", stamp, stamp,
	}
	bareRow := append([]string{fmt.Sprint(bare.CustomerNumber), "'=Formel AS", "business", "active"}, make([]string, 32)...)
	bareRow = append(bareRow, fmt.Sprint(bare.Id), "", stamp, stamp)
	want := csvBOM + strings.Join([]string{
		importableHeader + ";id;ownerName;createdAt;updatedAt",
		strings.Join(fjordRow, ";"),
		strings.Join(bareRow, ";"),
		"",
	}, "\r\n")

	if got := string(exportCSV(t, c, "").Body); got != want {
		t.Errorf("the export is\n%q\nwant\n%q", got, want)
	}
}

// TestGetCustomersExport_IsServedAsAFileNobodyCaches pins the headers a browser
// needs to save the file rather than show it, and its name: the day it was
// made, UTC.
func TestGetCustomersExport_IsServedAsAFileNobodyCaches(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	createCustomer(t, c, "Fil AS")

	r := exportCSV(t, c, "")
	for header, want := range map[string]string{
		"Content-Type":        "text/csv; charset=utf-8",
		"Content-Disposition": fmt.Sprintf("attachment; filename=%q", "customers-"+h.Now().UTC().Format(time.DateOnly)+".csv"),
		"Cache-Control":       "private, no-store",
	} {
		if got := r.Header(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

// TestGetCustomersExport_LegalIdentityColumnsAreAbsentWithoutLegalIdentityView
// is D2's permission shape: without customers:legal-identity-view the four
// columns are not in the file at all — absent, not blank, so the file re-imports
// without touching an identity — while a caller who may see them gets them.
func TestGetCustomersExport_LegalIdentityColumnsAreAbsentWithoutLegalIdentityView(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner := authenticatedClient(t, h)
	createCustomerWithIdentity(t, owner, "Fjord AS", "no", "923609016")
	legal := []string{"legalCountry", "legalType", "legalId", "legalName"}

	header, rows := exportTable(t, exportCSV(t, owner, "").Body)
	if len(header) != 40 || rows[0][slices.Index(header, "legalId")] != "923609016" {
		t.Errorf("with legal-identity-view: %d columns, legalId %q; want 40 and 923609016", len(header), rows[0][slices.Index(header, "legalId")])
	}

	viewer := h.SignIn(t, "customers:view")
	header, rows = exportTable(t, exportCSV(t, viewer, "").Body)
	for _, column := range legal {
		if slices.Contains(header, column) {
			t.Errorf("without legal-identity-view the header carries %s", column)
		}
	}
	if len(header) != 36 || len(rows) != 1 || len(rows[0]) != 36 {
		t.Errorf("without legal-identity-view: header %d columns, rows %v; want 36 and one row of 36", len(header), rows)
	}
}

// TestGetCustomersExport_HonoursTheListsFiltersAndSort: every filter the list
// takes narrows the file the same way, and the sort orders it — the file is the
// list a person is looking at. An invalid parameter is the list's own 400.
func TestGetCustomersExport_HonoursTheListsFiltersAndSort(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := authenticatedClientWithID(t, h)
	// Beta before Alpha, so id order and name order differ and a sort that fell
	// back to the default would show.
	beta := createCustomerOfType(t, c, "Beta Person", "person")
	alpha := createCustomer(t, c, "Alpha AS")
	gamma := createCustomer(t, c, "Gamma AS")
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", gamma.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("archive: status %d body %s", r.Status, r.Body)
	}
	vip := createTag(t, c, map[string]any{"name": "VIP"})
	if r := putCustomerTags(t, c, alpha.Id, []string{vip.Id}); r.Status != http.StatusOK {
		t.Fatalf("tags: status %d body %s", r.Status, r.Body)
	}
	retail := createGroup(t, c, map[string]any{"name": "Retail"})
	if r := putCustomerGroup(t, c, beta.Id, map[string]any{"groupId": retail.Id}); r.Status != http.StatusOK {
		t.Fatalf("group: status %d body %s", r.Status, r.Body)
	}
	if r := putOwner(t, c, alpha.Id, map[string]any{"ownerUserId": userID.String()}); r.Status != http.StatusOK {
		t.Fatalf("owner: status %d body %s", r.Status, r.Body)
	}

	for query, want := range map[string][]string{
		"":                                    {"Beta Person", "Alpha AS"},
		"includeArchived=true":                {"Beta Person", "Alpha AS", "Gamma AS"},
		"status=archived":                     {"Gamma AS"},
		"type=person":                         {"Beta Person"},
		"tagId=" + vip.Id:                     {"Alpha AS"},
		"groupId=" + retail.Id:                {"Beta Person"},
		"groupId=none":                        {"Alpha AS"},
		"ownerId=me":                          {"Alpha AS"},
		"search=gam&includeArchived=true":     {"Gamma AS"},
		"sortBy=name":                         {"Alpha AS", "Beta Person"},
		"sortBy=name&sortDirection=desc":      {"Beta Person", "Alpha AS"},
		"sortDirection=desc":                  {"Alpha AS", "Beta Person"},
		"search=" + url.QueryEscape("Nobody"): {},
	} {
		if got := exportedNames(t, c, query); !slices.Equal(got, want) {
			t.Errorf("?%s: names = %q, want %q", query, got, want)
		}
	}

	r := c.Do(http.MethodGet, "/api/v1/customers/export?status=Active", nil)
	var problem exportProblemJSON
	r.JSON(&problem)
	if r.Status != http.StatusBadRequest || problem.Title != "Invalid query parameters" ||
		problem.Detail != "'status' must be one of 'active', 'disabled' or 'archived', but was 'Active'." {
		t.Errorf("status=Active: %d %+v, want the list's own 400", r.Status, problem)
	}
}

// TestGetCustomersExport_MoreThan5000Customers_IsRefusedAskingForANarrowerFilter
// is D2's cap at its edge: 5000 customers are one file, 5001 are a 400 with no
// errors object that asks for a narrower filter — and the narrower filter works.
// The customers are written in one statement: the cap is about the file, not
// about how they came to exist.
func TestGetCustomersExport_MoreThan5000Customers_IsRefusedAskingForANarrowerFilter(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	h.Exec(t, `INSERT INTO customers.customers (customer_number, name, status, created_at, updated_at)
	           SELECT 100000 + n, 'Bulk ' || n, 'active', $1, $1 FROM generate_series(1, 5001) AS n`, h.Now())

	r := c.Do(http.MethodGet, "/api/v1/customers/export", nil)
	var problem exportProblemJSON
	r.JSON(&problem)
	if r.Status != http.StatusBadRequest || problem.Title != "Too many customers to export" || !strings.Contains(problem.Detail, "more than 5000 customers") {
		t.Fatalf("5001 customers: %d %+v, want the cap's 400", r.Status, problem)
	}
	if got := exportedNames(t, c, "search="+url.QueryEscape("Bulk 5001")); !slices.Equal(got, []string{"Bulk 5001"}) {
		t.Errorf("narrowed: names = %q, want [Bulk 5001]", got)
	}

	h.Exec(t, `DELETE FROM customers.customers WHERE customer_number = 105001`)
	if _, rows := exportTable(t, exportCSV(t, c, "").Body); len(rows) != 5000 {
		t.Errorf("5000 customers: %d rows, want 5000", len(rows))
	}
}

// TestGetCustomersImportTemplate_IsTheImportableHeaderAlone: every column an
// import writes — the legal identity's included, whoever asks, since the
// template is the file's shape and not anybody's data — and nothing else.
func TestGetCustomersImportTemplate_IsTheImportableHeaderAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	viewer := h.SignIn(t, "customers:view")

	r := viewer.Do(http.MethodGet, "/api/v1/customers/import/template", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("template: status %d body %s", r.Status, r.Body)
	}
	if got, want := string(r.Body), csvBOM+importableHeader+"\r\n"; got != want {
		t.Errorf("template is\n%q\nwant\n%q", got, want)
	}
	if got, want := r.Header("Content-Disposition"), `attachment; filename="customers-import-template.csv"`; got != want {
		t.Errorf("Content-Disposition = %q, want %q", got, want)
	}
}
