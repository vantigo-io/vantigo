package customers_test

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the customers file on its way in (customers import/export
// design D3): the file-level refusals, then what rows do — create, update by
// customer number, each group replaced whole or left alone, the vocabularies by
// name, the duplicate guard, the dry run — and the round trip that ties the
// import to the export.

type importResultJSON struct {
	DryRun  bool              `json:"dryRun"`
	Rows    int               `json:"rows"`
	Created int               `json:"created"`
	Updated int               `json:"updated"`
	Failed  int               `json:"failed"`
	Errors  []importErrorJSON `json:"errors"`
}

// importErrorJSON's Column is a plain string: absent on the wire (a row-level
// error) decodes as "".
type importErrorJSON struct {
	Row     int    `json:"row"`
	Column  string `json:"column"`
	Message string `json:"message"`
}

// csvFileOf is a customers file written the export's way: the BOM,
// semicolons, CRLF, a cell quoted when it has to be.
func csvFileOf(rows ...[]string) []byte {
	var b strings.Builder
	b.WriteString(csvBOM)
	for _, cells := range rows {
		for i, cell := range cells {
			if i > 0 {
				b.WriteByte(';')
			}
			if strings.ContainsAny(cell, ";\"\r\n") {
				cell = `"` + strings.ReplaceAll(cell, `"`, `""`) + `"`
			}
			b.WriteString(cell)
		}
		b.WriteString("\r\n")
	}
	return []byte(b.String())
}

// multipartBody wraps data as the one part named field, the way a browser's
// FormData sends a file.
func multipartBody(t *testing.T, field string, data []byte) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile(field, "customers.csv")
	if err != nil {
		t.Fatalf("create the %s part: %v", field, err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write the %s part: %v", field, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close the multipart writer: %v", err)
	}
	return w.FormDataContentType(), buf.Bytes()
}

// postImport POSTs data as the import's file part, query appended as given
// ("" or "?dryRun=false…").
func postImport(t *testing.T, c *modtest.Client, query string, data []byte) *modtest.Response {
	t.Helper()
	contentType, body := multipartBody(t, "file", data)
	return c.Do(http.MethodPost, "/api/v1/customers/import"+query, nil, modtest.RawBody(contentType, body))
}

// importResultOf decodes a 200, failing the test on anything else.
func importResultOf(t *testing.T, r *modtest.Response) importResultJSON {
	t.Helper()
	if r.Status != http.StatusOK {
		t.Fatalf("import: status %d body %s, want 200", r.Status, r.Body)
	}
	var result importResultJSON
	r.JSON(&result)
	return result
}

// fileRefusal is a 400's messages on "file", failing the test on anything else.
func fileRefusal(t *testing.T, r *modtest.Response) []string {
	t.Helper()
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	return problem.Errors["file"]
}

// customerIDByName is the id of the one customer called name.
func customerIDByName(t *testing.T, h *modtest.Harness, name string) int32 {
	t.Helper()
	return modtest.One[int32](t, h, `SELECT id FROM customers.customers WHERE name = $1`, name)
}

var (
	contactHeader = []string{"email", "phone", "website"}
	postalHeader  = []string{"postalLine1", "postalLine2", "postalPostalCode", "postalCity", "postalRegion", "postalCountry"}
	billingHeader = []string{"invoiceEmail", "reminderEmail", "paymentTermsDays", "currency", "language", "invoiceDelivery",
		"reminderDelivery", "peppolId", "gln", "buyerReference", "defaultBillRate"}
	legalHeader = []string{"legalCountry", "legalType", "legalId", "legalName"}
)

// cells joins groups of cells into one row.
func cells(groups ...[]string) []string {
	var out []string
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// TestPostCustomersImport_RefusesAFileItCannotRead: every file-level refusal
// is a 400 on "file" naming what is wrong — never a result — and none of them
// writes anything.
func TestPostCustomersImport_RefusesAFileItCannotRead(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	tooMany := [][]string{{"name"}}
	for i := 0; i <= 5000; i++ {
		tooMany = append(tooMany, []string{fmt.Sprintf("Kunde %d", i)})
	}
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"not UTF-8", []byte("name\r\n\xff\xfe\r\n"), "The file is not UTF-8 text"},
		{"a bare quote", []byte("name\r\nA \"b\" c\r\n"), "The file is not a semicolon-separated CSV file"},
		{"comma-separated", []byte("name,email\r\nA,a@x.no\r\n"), "The file is comma-separated"},
		{"an unknown column", csvFileOf([]string{"name", "nmae"}, []string{"A", "B"}), "Unknown columns: 'nmae'"},
		{"a column twice", csvFileOf([]string{"name", "Name"}, []string{"A", "B"}), "The column name appears more than once"},
		{"only the export's own columns", csvFileOf([]string{"id", "ownerName"}, []string{"1", "Kari"}), "The file carries no column an import writes"},
		{"half a group", csvFileOf([]string{"name", "email"}, []string{"A", "a@x.no"}), "The file carries some of the contact info columns but not phone, website"},
		{"more than 5000 rows", csvFileOf(tooMany...), "The file holds more than 5000 rows"},
		{"past 5 MB", bytes.Repeat([]byte("a"), 5*1024*1024+1), "A customer import is one CSV file of at most 5 MB"},
		{"an empty part", []byte{}, "A customer import is one CSV file of at most 5 MB"},
	} {
		messages := fileRefusal(t, postImport(t, c, "?dryRun=false", tc.data))
		if len(messages) == 0 || !strings.HasPrefix(messages[0], tc.want) {
			t.Errorf("%s: file = %q, want it to start %q", tc.name, messages, tc.want)
		}
	}

	contentType, body := multipartBody(t, "upload", csvFileOf([]string{"name"}, []string{"A"}))
	r := c.Do(http.MethodPost, "/api/v1/customers/import?dryRun=false", nil, modtest.RawBody(contentType, body))
	if messages := fileRefusal(t, r); len(messages) != 1 || !strings.Contains(messages[0], "the multipart part named 'file'") {
		t.Errorf("a part named upload: file = %q", messages)
	}
	if r := c.Do(http.MethodPost, "/api/v1/customers/import?dryRun=false", nil,
		modtest.RawBody("text/csv", csvFileOf([]string{"name"}, []string{"A"})),
		modtest.SkipContract("a body that is not multipart is off-contract by construction")); r.Status != http.StatusBadRequest {
		t.Errorf("not multipart: status %d, want 400", r.Status)
	}
	r = postImport(t, c, "?dryRun=yes", csvFileOf([]string{"name"}, []string{"A"}))
	var problem validationProblemJSON
	r.JSON(&problem)
	if r.Status != http.StatusBadRequest || len(problem.Errors["dryRun"]) != 1 || problem.Errors["dryRun"][0] != "'dryRun' must be one of 'true' or 'false', but was 'yes'." {
		t.Errorf("dryRun=yes: %d %+v", r.Status, problem)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers`); n != 0 {
		t.Errorf("%d customers written by refused files, want none", n)
	}
}

// TestPostCustomersImport_AColumnTheSenderMayNotWriteRefusesTheFileWhole is
// D3's rule: no import key, the file checked against the caller instead — the
// legal identity's columns against customers:legal-identity-manage, the
// billing profile's against customers:billing-manage — and a column its sender
// may not write refuses the whole file, naming the columns and the key. The
// router itself wants create, update and view together.
func TestPostCustomersImport_AColumnTheSenderMayNotWriteRefusesTheFileWhole(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	noIdentity := h.SignIn(t, "customers:view", "customers:create", "customers:update", "customers:billing-manage")
	noBilling := h.SignIn(t, "customers:view", "customers:create", "customers:update", "customers:legal-identity-manage")

	identityFile := csvFileOf(cells([]string{"name"}, legalHeader), []string{"Fjord AS", "no", "business", "923609016", "Fjord AS"})
	if messages := fileRefusal(t, postImport(t, noIdentity, "?dryRun=false", identityFile)); len(messages) != 1 ||
		!strings.Contains(messages[0], "legalCountry, legalType, legalId, legalName") || !strings.Contains(messages[0], "customers:legal-identity-manage") {
		t.Errorf("identity columns without the key: file = %q", messages)
	}
	billingFile := csvFileOf(cells([]string{"name"}, billingHeader), cells([]string{"Fjord AS"}, make([]string, 11)))
	if messages := fileRefusal(t, postImport(t, noBilling, "?dryRun=false", billingFile)); len(messages) != 1 ||
		!strings.Contains(messages[0], "invoiceEmail, reminderEmail") || !strings.Contains(messages[0], "customers:billing-manage") {
		t.Errorf("billing columns without the key: file = %q", messages)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers`); n != 0 {
		t.Errorf("%d customers written, want none", n)
	}

	for _, keys := range [][]string{
		{"customers:view", "customers:create"},
		// create and update without view: every hand write the import stands
		// in for needs view too, and row errors would describe customers the
		// caller may not see.
		{"customers:create", "customers:update"},
	} {
		if r := postImport(t, h.SignIn(t, keys...), "", csvFileOf([]string{"name"}, []string{"A"})); r.Status != http.StatusForbidden {
			t.Errorf("%v: status %d, want 403", keys, r.Status)
		}
	}
}

// TestPostCustomersImport_CreatesAndUpdatesByCustomerNumber_AsTheImporter: a
// blank number creates, a number selects the customer to update, and every
// event the rows record carries the importer — a row written by file is
// indistinguishable from the same edit made by hand.
func TestPostCustomersImport_CreatesAndUpdatesByCustomerNumber_AsTheImporter(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := authenticatedClientWithID(t, h)
	existing := createCustomer(t, c, "Gammel AS")

	result := importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(
		[]string{"customerNumber", "name", "type", "status"},
		[]string{"", "Ny Person", "person", ""},
		[]string{fmt.Sprint(existing.CustomerNumber), "Gammel og Ny AS", "", "disabled"},
	)))
	if result.DryRun || result.Rows != 2 || result.Created != 1 || result.Updated != 1 || result.Failed != 0 || len(result.Errors) != 0 {
		t.Fatalf("result = %+v, want 1 created, 1 updated", result)
	}

	created := fetchCustomerJSON(t, c, customerIDByName(t, h, "Ny Person"))
	if created.Status != "active" {
		t.Errorf("created status = %q, want active (the create default)", created.Status)
	}
	updated := fetchCustomerJSON(t, c, existing.Id)
	if updated.Name != "Gammel og Ny AS" || updated.Status != "disabled" || updated.Revision != 2 {
		t.Errorf("updated = %+v, want renamed and disabled in one write (revision 2)", updated)
	}
	for _, e := range []struct {
		customer  int32
		eventType string
	}{{created.Id, "customer.created"}, {existing.Id, "customer.updated"}, {existing.Id, "customer.status_changed"}} {
		actor := modtest.One[string](t, h, `SELECT actor_kind || '/' || actor_display || '/' || actor_user_id::text
		    FROM customers.customers_timeline_entries WHERE customer_id = $1 AND event_type = $2`, e.customer, e.eventType)
		if want := "user/" + userDisplayName(t, h, userID) + "/" + userID.String(); actor != want {
			t.Errorf("%s actor = %q, want %q", e.eventType, actor, want)
		}
	}
}

// customersTables is every table a row of the file can reach: the customer
// row itself (its name, status, identity, contact info, billing profile and
// group), its addresses, its tag links, the registry record an identity
// change invalidates, the timeline and the customer-number counter.
var customersTables = []string{"customers", "customer_addresses", "customer_tags", "customer_registry_records", "customers_timeline_entries", "counters"}

// tablesSnapshot is each of customersTables as one line — its row count and a
// digest of every column of every row — so two snapshots are equal only when
// nothing in them was written.
func tablesSnapshot(t *testing.T, h *modtest.Harness) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, table := range customersTables {
		out[table] = modtest.One[string](t, h, fmt.Sprintf(
			`SELECT count(*)::text || ' ' || md5(coalesce(string_agg(r::text, '|' ORDER BY r::text), '')) FROM customers.%s r`, table))
	}
	return out
}

// TestPostCustomersImport_DefaultsToADryRunThatWritesNothing_AndReportsTheRealRun
// is D3's dry run: without dryRun the import only checks — nothing kept in any
// table a row reaches, no customer number burned — and what it reports is
// exactly what the real run then does. Its rows touch every group: a create
// carrying all of them, an update changing all of them.
func TestPostCustomersImport_DefaultsToADryRunThatWritesNothing_AndReportsTheRealRun(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	existing := createCustomerWithIdentity(t, c, "Eksisterende AS", "no", "923609016")
	createAddress(t, c, existing.Id, map[string]any{"type": "postal", "line1": "Storgata 1", "postalCode": "0155", "city": "Oslo", "country": "no"})
	createGroup(t, c, map[string]any{"name": "Retail"})
	createTag(t, c, map[string]any{"name": "VIP"})
	header := cells([]string{"customerNumber", "name"}, legalHeader, contactHeader, postalHeader, billingHeader, []string{"group", "tags"})
	file := csvFileOf(header,
		cells([]string{"", "Ny Kunde AS"}, []string{"no", "business", "974760673", "Ny Kunde AS"}, []string{"post@ny.no", "", ""},
			[]string{"Nygata 2", "", "0151", "Oslo", "", "no"}, []string{"", "", "30", "NOK", "", "", "", "", "", "", ""}, []string{"Retail", "VIP"}),
		cells([]string{fmt.Sprint(existing.CustomerNumber), "Eksisterende og Omdøpt AS"}, []string{"no", "business", "912660680", "Omdøpt AS"},
			[]string{"", "22 33 44 55", ""}, []string{"Storgata 9", "", "0155", "Oslo", "", "no"}, []string{"", "", "14", "EUR", "", "", "", "", "", "", ""},
			[]string{"Retail", "VIP"}),
		cells([]string{"", "Feil AS"}, make([]string, 4), []string{"ikke-en-adresse", "", ""}, make([]string, 6), make([]string, 11), []string{"", ""}),
	)
	before := tablesSnapshot(t, h)

	dry := importResultOf(t, postImport(t, c, "", file))
	if !dry.DryRun || dry.Rows != 3 || dry.Created != 1 || dry.Updated != 1 || dry.Failed != 1 {
		t.Errorf("dry run = %+v, want 1 created, 1 updated, 1 failed", dry)
	}
	after := tablesSnapshot(t, h)
	for _, table := range customersTables {
		if after[table] != before[table] {
			t.Errorf("the dry run wrote to customers.%s: %s, was %s", table, after[table], before[table])
		}
	}
	if got := fetchCustomerJSON(t, c, existing.Id); got.Name != "Eksisterende AS" {
		t.Errorf("name after the dry run = %q, want unchanged", got.Name)
	}

	real := importResultOf(t, postImport(t, c, "?dryRun=false", file))
	if real.DryRun || real.Created != dry.Created || real.Updated != dry.Updated || real.Failed != dry.Failed ||
		fmt.Sprint(real.Errors) != fmt.Sprint(dry.Errors) {
		t.Errorf("real run = %+v, want what the dry run reported: %+v", real, dry)
	}
	if want := []importErrorJSON{{Row: 3, Column: "email", Message: "An email address must look like name@example.com, but was 'ikke-en-adresse'"}}; fmt.Sprint(real.Errors) != fmt.Sprint(want) {
		t.Errorf("errors = %+v, want %+v", real.Errors, want)
	}
	// The real run wrote where the dry run did not: the snapshot sees it.
	written := tablesSnapshot(t, h)
	for _, table := range []string{"customers", "customer_addresses", "customer_tags", "customers_timeline_entries", "counters"} {
		if written[table] == before[table] {
			t.Errorf("the real run left customers.%s as it was; the snapshot cannot tell a write there", table)
		}
	}
}

// TestPostCustomersImport_TwoRowsCreatingOneIdentity_TheSecondIsARowErrorInBothRuns:
// a dry run's rows each roll back, so the database cannot see that row 1
// already created the identity row 2 creates — the in-file check does, before
// any row runs, and the two runs agree. allowDuplicateIdentity lets both in.
func TestPostCustomersImport_TwoRowsCreatingOneIdentity_TheSecondIsARowErrorInBothRuns(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	file := csvFileOf(cells([]string{"name"}, legalHeader),
		[]string{"Fjord AS", "no", "business", "923609016", "Fjord AS"},
		[]string{"Fjord Kopi AS", "no", "business", "923 609 016", "Fjord AS"},
	)
	want := []importErrorJSON{{Row: 2, Column: "legalId",
		Message: "Row 1 of this file already creates a customer with this legal identity. Import with allowDuplicateIdentity=true to keep both."}}
	for _, query := range []string{"", "?dryRun=false"} {
		result := importResultOf(t, postImport(t, c, query, file))
		if result.Created != 1 || result.Failed != 1 || fmt.Sprint(result.Errors) != fmt.Sprint(want) {
			t.Errorf("%q: result = %+v, want row 2 refused with %+v", query, result, want)
		}
	}
	if result := importResultOf(t, postImport(t, c, "?dryRun=false&allowDuplicateIdentity=true", file)); result.Created != 2 || result.Failed != 0 {
		t.Errorf("allowDuplicateIdentity=true: result = %+v, want both created", result)
	}
}

// TestPostCustomersImport_ACarriedGroupIsReplacedWhole_AnAbsentOneIsLeftAlone
// is D3's group rule: a group in the header is written as its endpoint writes
// it — a blank cell clears that field, all of an address's cells blank remove
// the primary address, a blank tags cell clears the tags — and a group not in
// the header is not touched.
func TestPostCustomersImport_ACarriedGroupIsReplacedWhole_AnAbsentOneIsLeftAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Full AS", "contactInfo": map[string]any{"email": "gammel@full.no", "phone": "22 33 44 55"},
	})
	var full createdCustomerJSON
	r.JSON(&full)
	createAddress(t, c, full.Id, map[string]any{"type": "postal", "line1": "Storgata 1", "postalCode": "0155", "city": "Oslo", "country": "no"})
	if r := putBillingProfile(t, c, full.Id, map[string]any{"currency": "NOK", "paymentTermsDays": 30}); r.Status != http.StatusOK {
		t.Fatalf("billing: status %d body %s", r.Status, r.Body)
	}
	vip := createTag(t, c, map[string]any{"name": "VIP"})
	if r := putCustomerTags(t, c, full.Id, []string{vip.Id}); r.Status != http.StatusOK {
		t.Fatalf("tags: status %d body %s", r.Status, r.Body)
	}
	number := fmt.Sprint(full.CustomerNumber)

	result := importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(
		cells([]string{"customerNumber"}, contactHeader, postalHeader, []string{"tags"}),
		cells([]string{number, "ny@full.no", "", ""}, make([]string, 6), []string{""}),
	)))
	if result.Updated != 1 || result.Failed != 0 {
		t.Fatalf("first file: result = %+v", result)
	}
	got := fetchCustomerJSON(t, c, full.Id)
	if got.ContactInfo.Email == nil || *got.ContactInfo.Email != "ny@full.no" || got.ContactInfo.Phone != nil || len(got.Tags) != 0 {
		t.Errorf("customer = %+v, want the new email, the phone cleared, no tags", got)
	}
	if addresses := listAddresses(t, c, full.Id); len(addresses.Data) != 0 {
		t.Errorf("addresses = %+v, want the primary postal address removed", addresses.Data)
	}
	if p := fetchBillingProfile(t, c, full.Id); p.Currency == nil || *p.Currency != "NOK" || p.PaymentTermsDays == nil || *p.PaymentTermsDays != 30 {
		t.Errorf("billing profile = %+v, want it untouched by a file without its columns", p)
	}

	result = importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(
		cells([]string{"customerNumber"}, billingHeader),
		cells([]string{number}, []string{"", "", "", "EUR", "", "", "", "", "", "", "1250,50"}),
	)))
	if result.Updated != 1 || result.Failed != 0 {
		t.Fatalf("second file: result = %+v", result)
	}
	if p := fetchBillingProfile(t, c, full.Id); p.Currency == nil || *p.Currency != "EUR" || p.PaymentTermsDays != nil || p.DefaultBillRate == nil || *p.DefaultBillRate != 1250.5 {
		t.Errorf("billing profile = %+v, want EUR, the rate, and the blank terms cleared", p)
	}
}

// TestPostCustomersImport_GroupAndTagsAreNamedCaseInsensitively_AnUnknownNameIsARowError:
// a file names the vocabulary as a person reads it, whatever the case, and a
// word the vocabulary does not have is that row's error — never a word created
// behind anybody's back.
func TestPostCustomersImport_GroupAndTagsAreNamedCaseInsensitively_AnUnknownNameIsARowError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	createGroup(t, c, map[string]any{"name": "Retail"})
	createTag(t, c, map[string]any{"name": "VIP"})
	createTag(t, c, map[string]any{"name": "Prospect"})

	result := importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(
		[]string{"name", "group", "tags"},
		[]string{"A AS", "rETAIL", "vip | PROSPECT"},
		[]string{"B AS", "Wholesale", ""},
		[]string{"C AS", "", "VIP|Nope"},
	)))
	want := []importErrorJSON{
		{Row: 2, Column: "group", Message: "No customer group is named 'Wholesale'"},
		{Row: 3, Column: "tags", Message: "No tag is named 'Nope'"},
	}
	if result.Created != 1 || result.Failed != 2 || fmt.Sprint(result.Errors) != fmt.Sprint(want) {
		t.Fatalf("result = %+v, want A created and %+v", result, want)
	}
	a := fetchCustomerJSON(t, c, customerIDByName(t, h, "A AS"))
	if a.Group == nil || a.Group.Name != "Retail" || len(a.Tags) != 2 || a.Tags[0].Name != "Prospect" || a.Tags[1].Name != "VIP" {
		t.Errorf("A = group %+v tags %+v, want Retail and Prospect, VIP", a.Group, a.Tags)
	}
}

// TestPostCustomersImport_ATagsCellIsNamesJoinedBySeparators: '|' is never
// part of a tag's name (validateTagName refuses it), so a cell holding it can
// only mean the tags on either side — both of them, never a third word.
func TestPostCustomersImport_ATagsCellIsNamesJoinedBySeparators(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	createTag(t, c, map[string]any{"name": "Inn"})
	createTag(t, c, map[string]any{"name": "Ut"})

	result := importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf([]string{"name", "tags"}, []string{"Pipe AS", "Inn|Ut"})))
	if result.Created != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v, want the row created", result)
	}
	if got := fetchCustomerJSON(t, c, customerIDByName(t, h, "Pipe AS")); len(got.Tags) != 2 || got.Tags[0].Name != "Inn" || got.Tags[1].Name != "Ut" {
		t.Errorf("tags = %+v, want Inn and Ut", got.Tags)
	}
}

// TestPostCustomersImport_ADecomposedNameFindsItsWord: the vocabularies are
// stored NFC (validateTagName, validateGroupName), and a file saved on a Mac
// may spell the same word decomposed — an a and a combining ring for å. It is
// one word to every reader, so it is one word to the import.
func TestPostCustomersImport_ADecomposedNameFindsItsWord(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	createGroup(t, c, map[string]any{"name": "Kafé"})
	createTag(t, c, map[string]any{"name": "Små"})

	result := importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(
		[]string{"name", "group", "tags"},
		[]string{"Bakeri AS", "Kafe\u0301", "SMA\u030a"},
	)))
	if result.Created != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v, want the row created", result)
	}
	got := fetchCustomerJSON(t, c, customerIDByName(t, h, "Bakeri AS"))
	if got.Group == nil || got.Group.Name != "Kafé" || len(got.Tags) != 1 || got.Tags[0].Name != "Små" {
		t.Errorf("group %+v tags %+v, want Kafé and Små", got.Group, got.Tags)
	}
}

// TestPostCustomersImport_TakesABodyPastTheRoutersDefaultCap: a full 5000-row
// file is a few MB, past the 1 MiB every other operation is capped at, so the
// import's own cap (importBodyLimits) must be the one in effect. About 1.4 MB
// of rows whose names are too long keeps it cheap: every row fails in plan,
// before any transaction.
func TestPostCustomersImport_TakesABodyPastTheRoutersDefaultCap(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	rows := [][]string{{"name"}}
	for i := 0; i < 2000; i++ {
		rows = append(rows, []string{fmt.Sprintf("%04d %s", i, strings.Repeat("x", 700))})
	}
	data := csvFileOf(rows...)
	if len(data) <= 1<<20 {
		t.Fatalf("the file is %d bytes; it must be past 1 MiB to mean anything", len(data))
	}
	result := importResultOf(t, postImport(t, c, "", data))
	if result.Rows != 2000 || result.Failed != 2000 {
		t.Errorf("result: rows %d failed %d, want 2000 read and refused", result.Rows, result.Failed)
	}
}

// TestPostCustomersImport_AnInvalidTypeIsReportedOnce: a type cell the
// validator refuses is that row's error on type, and nothing else is judged
// against a type the row does not have — no identity mismatch against the
// business a blank type would default to.
func TestPostCustomersImport_AnInvalidTypeIsReportedOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	result := importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(
		cells([]string{"name", "type"}, legalHeader),
		[]string{"Ola Nordmann", "persn", "se", "person", "19800101-1234", "Ola Nordmann"},
	)))
	want := []importErrorJSON{{Row: 1, Column: "type", Message: "A customer type must be one of 'business' or 'person', but was 'persn'"}}
	if result.Failed != 1 || fmt.Sprint(result.Errors) != fmt.Sprint(want) {
		t.Errorf("result = %+v, want only %+v", result, want)
	}
}

// TestPostCustomersImport_ADuplicateIdentityIsARowError_UnlessAllowed is the
// create endpoint's own 409, per row, and its own flag, for the whole file.
func TestPostCustomersImport_ADuplicateIdentityIsARowError_UnlessAllowed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	holder := createCustomerWithIdentity(t, c, "Holder AS", "no", "923609016")
	file := csvFileOf(cells([]string{"name"}, legalHeader), []string{"Tvilling AS", "no", "business", "923 609 016", "Tvilling AS"})

	result := importResultOf(t, postImport(t, c, "?dryRun=false", file))
	if result.Failed != 1 || len(result.Errors) != 1 || result.Errors[0].Column != "legalId" ||
		!strings.HasPrefix(result.Errors[0].Message, "Another customer already has this legal identity") ||
		!strings.Contains(result.Errors[0].Message, fmt.Sprintf("%d Holder AS", holder.CustomerNumber)) {
		t.Fatalf("result = %+v, want the duplicate refused on legalId, naming the holder", result)
	}
	if result := importResultOf(t, postImport(t, c, "?dryRun=false&allowDuplicateIdentity=true", file)); result.Created != 1 || result.Failed != 0 {
		t.Errorf("allowDuplicateIdentity=true: result = %+v, want it created", result)
	}
}

// TestPostCustomersImport_RowErrorsNameTheirRowAndColumn_AndNeverStopTheFile:
// rows run in file order, each on its own; a row that fails is reported by
// number and, for a field, by column, and the rows after it still run.
func TestPostCustomersImport_RowErrorsNameTheirRowAndColumn_AndNeverStopTheFile(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	existing := createCustomer(t, c, "Bedrift AS")

	result := importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(
		cells([]string{"customerNumber", "name", "type"}, contactHeader),
		[]string{"", "Første AS", "", "", "", ""},
		[]string{"999999", "Spøkelse AS", "", "", "", ""},
		[]string{"", "Feil AS", "", "nei", "12", ""},
		[]string{"", "Kort AS"},
		[]string{fmt.Sprint(existing.CustomerNumber), "Bedrift AS", "person", "", "", ""},
		[]string{"", "Siste AS", "", "", "", ""},
	)))
	want := []importErrorJSON{
		{Row: 2, Column: "customerNumber", Message: "No customer has number 999999"},
		{Row: 3, Column: "email", Message: "An email address must look like name@example.com, but was 'nei'"},
		{Row: 3, Column: "phone", Message: "A phone number may only contain digits, spaces and + - ( ), and needs at least five digits, but was '12'"},
		{Row: 4, Message: "This row has 2 cells, but the header has 6"},
		{Row: 5, Column: "type", Message: fmt.Sprintf("A customer's type is changed on its own, never by an import; this row says 'person', but customer %d is 'business'", existing.CustomerNumber)},
	}
	if result.Rows != 6 || result.Created != 2 || result.Failed != 4 || fmt.Sprint(result.Errors) != fmt.Sprint(want) {
		t.Fatalf("result = %+v\nwant 2 created and errors %+v", result, want)
	}
	customerIDByName(t, h, "Siste AS") // the row after four failures still ran
}

// TestPostCustomersImport_ARepeatedIdentityKeepsItsSource: a file cannot say
// where an identity came from, so a new one is manual (design D1) — but a row
// repeating the identity on file is no change at all, and a Brreg pick stays a
// Brreg pick with no event recorded.
func TestPostCustomersImport_ARepeatedIdentityKeepsItsSource(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	fjord := createCustomerWithIdentity(t, c, "Fjord AS", "no", "923609016")
	h.Exec(t, `UPDATE customers.customers SET legal_source = 'brreg' WHERE id = $1`, fjord.Id)
	events := countTimelineEvents(t, h, fjord.Id, "customer.updated")

	result := importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(
		cells([]string{"customerNumber"}, legalHeader),
		[]string{fmt.Sprint(fjord.CustomerNumber), "NO", "business", "923609016", "Fjord AS"},
	)))
	if result.Updated != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v", result)
	}
	if row := fetchLegalRow(t, h, fjord.Id); row.Source != "brreg" {
		t.Errorf("source = %q, want brreg kept", row.Source)
	}
	if n := countTimelineEvents(t, h, fjord.Id, "customer.updated"); n != events {
		t.Errorf("customer.updated events = %d, want %d: nothing changed", n, events)
	}
}

// TestPostCustomersImport_IgnoresTheExportOnlyColumnsAndTheErrorColumn: an
// export re-imports without editing (D1), and so does the failed-rows file the
// browser builds, with its error column (D4).
func TestPostCustomersImport_IgnoresTheExportOnlyColumnsAndTheErrorColumn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	result := importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(
		[]string{"name", "id", "ownerName", "createdAt", "updatedAt", "error"},
		[]string{"Rettet AS", "1234", "Kari Nordmann", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "email: something was wrong"},
	)))
	if result.Created != 1 || result.Failed != 0 {
		t.Errorf("result = %+v, want the row created", result)
	}
}

// TestCustomersExportImport_ARoundTripChangesNothing: import what the export
// wrote and every customer comes back as it was — every row an update, nothing
// written, no event, and the next export byte for byte the first.
func TestCustomersExportImport_ARoundTripChangesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := authenticatedClientWithID(t, h)
	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name":        `Fjord; "Nord" AS`,
		"identity":    map[string]any{"country": "no", "type": "business", "id": "923609016", "name": "Fjord Nord AS", "source": "manual"},
		"contactInfo": map[string]any{"email": "post@fjord.no", "phone": "+47 22 33 44 55", "website": "https://fjord.no"},
	})
	var fjord createdCustomerJSON
	r.JSON(&fjord)
	createAddress(t, c, fjord.Id, map[string]any{"type": "postal", "label": "HQ", "line1": "Storgata 1", "postalCode": "0155", "city": "Oslo", "country": "no"})
	createAddress(t, c, fjord.Id, map[string]any{"type": "invoice", "line1": "Postboks 7", "postalCode": "0101", "city": "Oslo", "country": "no"})
	if r := putBillingProfile(t, c, fjord.Id, map[string]any{
		"invoiceEmail": "faktura@fjord.no", "paymentTermsDays": 14, "currency": "NOK", "language": "nb",
		"invoiceDelivery": "ehf", "peppolId": "0192:923609016", "buyerReference": "PO-42", "defaultBillRate": 1250.5,
	}); r.Status != http.StatusOK {
		t.Fatalf("billing: status %d body %s", r.Status, r.Body)
	}
	retail := createGroup(t, c, map[string]any{"name": "Retail"})
	putCustomerGroup(t, c, fjord.Id, map[string]any{"groupId": retail.Id})
	vip := createTag(t, c, map[string]any{"name": "VIP"})
	putCustomerTags(t, c, fjord.Id, []string{vip.Id})
	putOwner(t, c, fjord.Id, map[string]any{"ownerUserId": userID.String()})
	createCustomerOfType(t, c, "Ola Nordmann", "person")
	createCustomer(t, c, "=Formel AS")

	// A Brreg pick, so the round trip also shows a repeated identity keeping
	// its source (importedIdentity): the file cannot say brreg, and a manual
	// identity would read back unchanged either way.
	h.Exec(t, `UPDATE customers.customers SET legal_source = 'brreg' WHERE id = $1`, fjord.Id)

	first := exportCSV(t, c, "").Body
	events := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries`)
	result := importResultOf(t, postImport(t, c, "?dryRun=false", first))
	if result.Rows != 3 || result.Updated != 3 || result.Created != 0 || result.Failed != 0 {
		t.Fatalf("result = %+v, want three updates", result)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries`); n != events {
		t.Errorf("timeline entries = %d, want %d: a round trip writes nothing", n, events)
	}
	if second := exportCSV(t, c, "").Body; !bytes.Equal(first, second) {
		t.Errorf("the second export differs:\n%q\nfrom\n%q", second, first)
	}
}

// TestPostCustomersImport_AHeaderIsBoundedByTheTable: a header wider than the
// table can describe is one refusal, whatever it holds — a 5 MB header of
// semicolons is five million nameless cells, and a refusal each would be a
// response hundreds of MB large — and the nameless columns of a header that
// fits are one refusal between them, their positions listed up to ten.
func TestPostCustomersImport_AHeaderIsBoundedByTheTable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	semicolons := append(append([]byte(csvBOM), bytes.Repeat([]byte(";"), 5*1024*1024-len(csvBOM)-2)...), '\r', '\n')
	r := postImport(t, c, "?dryRun=false", semicolons)
	if len(r.Body) > 1024 {
		t.Fatalf("a header of semicolons: a %d-byte answer, want one short refusal", len(r.Body))
	}
	want := fmt.Sprintf("The header has %d columns, but the file describes at most 41", 5*1024*1024-len(csvBOM)-2+1)
	if messages := fileRefusal(t, r); len(messages) != 1 || messages[0] != want {
		t.Errorf("a header of semicolons: file = %q, want [%q]", messages, want)
	}

	for _, tc := range []struct {
		name   string
		header []string
		want   string
	}{
		{"one", []string{"name", ""}, "Column 2 has no name"},
		{"three", []string{"", "name", "", ""}, "Columns 1, 3 and 4 have no name"},
		{"past ten", append([]string{"name"}, make([]string, 40)...), "Columns 2, 3, 4, 5, 6, 7, 8, 9, 10, 11 and 30 more have no name"},
	} {
		if messages := fileRefusal(t, postImport(t, c, "?dryRun=false", csvFileOf(tc.header))); len(messages) != 1 || messages[0] != tc.want {
			t.Errorf("%s: file = %q, want [%q]", tc.name, messages, tc.want)
		}
	}
}

// TestPostCustomersImport_ABlankAddressGroupDoesTheSameTwice: all of an
// address's cells blank remove the primary address only when it is the only
// one of its type. With others of the type the row is refused — removing the
// primary promotes the next, so the same file would remove one more address
// each time it ran — and the same file imported twice answers the same.
func TestPostCustomersImport_ABlankAddressGroupDoesTheSameTwice(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	one := createCustomer(t, c, "Én Adresse AS")
	createAddress(t, c, one.Id, map[string]any{"type": "postal", "line1": "Storgata 1", "postalCode": "0155", "city": "Oslo", "country": "no"})
	two := createCustomer(t, c, "To Adresser AS")
	createAddress(t, c, two.Id, map[string]any{"type": "postal", "line1": "Storgata 1", "postalCode": "0155", "city": "Oslo", "country": "no"})
	createAddress(t, c, two.Id, map[string]any{"type": "postal", "line1": "Lillegata 2", "postalCode": "0155", "city": "Oslo", "country": "no"})
	createAddress(t, c, two.Id, map[string]any{"type": "visiting", "line1": "Besøksveien 3", "postalCode": "0155", "city": "Oslo", "country": "no"})
	file := csvFileOf(cells([]string{"customerNumber"}, postalHeader),
		cells([]string{fmt.Sprint(one.CustomerNumber)}, make([]string, 6)),
		cells([]string{fmt.Sprint(two.CustomerNumber)}, make([]string, 6)),
	)
	want := []importErrorJSON{{Row: 2, Column: "postalLine1",
		Message: "This customer has 2 postal addresses; the file can only describe the primary one — remove the others by hand before clearing it"}}

	for run := 1; run <= 2; run++ {
		result := importResultOf(t, postImport(t, c, "?dryRun=false", file))
		if result.Updated != 1 || result.Failed != 1 || fmt.Sprint(result.Errors) != fmt.Sprint(want) {
			t.Errorf("run %d: result = %+v, want the lone address cleared and %+v", run, result, want)
		}
		if got := listAddresses(t, c, one.Id).Data; len(got) != 0 {
			t.Errorf("run %d: the lone address's customer has %+v, want none", run, got)
		}
		if got := listAddresses(t, c, two.Id).Data; len(got) != 3 {
			t.Errorf("run %d: the two-address customer has %d addresses, want all 3 kept", run, len(got))
		}
	}
}

// TestPostCustomersImport_EveryBranchOfARow writes each of the import's own
// branches through one row and reads the result back the way a person would —
// the hand GETs, the timeline — so the replace-whole rule is pinned for every
// group, not only the ones a round trip reaches: an address inserted,
// replaced with its label kept, or repeated to no effect; an address error on
// the file's column; the invoice address; the address cap; an identity
// replaced, cleared, of the wrong type or another customer's; a group set,
// changed and cleared; and a group or tag deleted after the file named it.
// Not parallel: the last two delete the word through SetImportHeldHook,
// which is the package's.
func TestPostCustomersImport_EveryBranchOfARow(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	storgata := map[string]any{"type": "postal", "label": "HQ", "line1": "Storgata 1", "postalCode": "0155", "city": "Oslo", "country": "no"}
	storgataCells := []string{"Storgata 1", "", "0155", "Oslo", "", "no"}
	invoiceHeader := []string{"invoiceLine1", "invoiceLine2", "invoicePostalCode", "invoiceCity", "invoiceRegion", "invoiceCountry"}
	number := func(cust createdCustomerJSON) string { return fmt.Sprint(cust.CustomerNumber) }
	groupOf := func(t *testing.T, id int32) string {
		if g := fetchCustomerJSON(t, c, id).Group; g != nil {
			return g.Name
		}
		return ""
	}

	for _, tc := range []struct {
		name string
		// row builds the one row under header; it may set up the customer it
		// names, and answers the check to run on success.
		header []string
		row    func(t *testing.T) ([]string, func(t *testing.T))
		// held, when set, runs inside the import before its first row.
		held     func(t *testing.T)
		wantErrs []importErrorJSON
	}{
		{
			name: "a postal address is inserted", header: cells([]string{"customerNumber"}, postalHeader),
			row: func(t *testing.T) ([]string, func(t *testing.T)) {
				cust := createCustomer(t, c, "Ingen Adresse AS")
				return cells([]string{number(cust)}, storgataCells), func(t *testing.T) {
					got := listAddresses(t, c, cust.Id).Data
					if len(got) != 1 || got[0].Type != "postal" || !got[0].IsPrimary || got[0].Line1 != "Storgata 1" || got[0].Label != nil ||
						got[0].PostalCode == nil || *got[0].PostalCode != "0155" || got[0].City == nil || *got[0].City != "Oslo" {
						t.Errorf("addresses = %+v, want one primary postal Storgata 1, 0155 Oslo", got)
					}
				}
			},
		},
		{
			name: "a postal address is replaced and keeps its label", header: cells([]string{"customerNumber"}, postalHeader),
			row: func(t *testing.T) ([]string, func(t *testing.T)) {
				cust := createCustomer(t, c, "Flytter AS")
				createAddress(t, c, cust.Id, storgata)
				return cells([]string{number(cust)}, []string{"Nygata 9", "3. etasje", "0151", "Oslo", "", "no"}), func(t *testing.T) {
					got := listAddresses(t, c, cust.Id).Data
					if len(got) != 1 || got[0].Line1 != "Nygata 9" || got[0].Line2 == nil || *got[0].Line2 != "3. etasje" || got[0].Label == nil || *got[0].Label != "HQ" {
						t.Errorf("addresses = %+v, want Nygata 9 labelled HQ", got)
					}
					if n := countTimelineEvents(t, h, cust.Id, "customer.address_updated"); n != 1 {
						t.Errorf("customer.address_updated = %d, want 1", n)
					}
				}
			},
		},
		{
			name: "a postal address repeated is no write", header: cells([]string{"customerNumber"}, postalHeader),
			row: func(t *testing.T) ([]string, func(t *testing.T)) {
				cust := createCustomer(t, c, "Står Stille AS")
				before := createAddress(t, c, cust.Id, storgata)
				return cells([]string{number(cust)}, storgataCells), func(t *testing.T) {
					got := listAddresses(t, c, cust.Id).Data
					if len(got) != 1 || !got[0].UpdatedAt.Equal(before.UpdatedAt) || got[0].Label == nil || *got[0].Label != "HQ" {
						t.Errorf("addresses = %+v, want %+v untouched", got, before)
					}
					if n := countTimelineEvents(t, h, cust.Id, "customer.address_updated"); n != 0 {
						t.Errorf("customer.address_updated = %d, want none", n)
					}
				}
			},
		},
		{
			name: "an address error names the file's column", header: cells([]string{"name"}, postalHeader),
			row: func(t *testing.T) ([]string, func(t *testing.T)) {
				return cells([]string{"Uten Postnummer AS"}, []string{"Storgata 1", "", "", "Oslo", "", "no"}), nil
			},
			wantErrs: []importErrorJSON{{Row: 1, Column: "postalPostalCode", Message: "A Norwegian address needs a four-digit postal code"}},
		},
		{
			name: "a partly blank address is refused, not half-read", header: cells([]string{"name"}, postalHeader),
			row: func(t *testing.T) ([]string, func(t *testing.T)) {
				return cells([]string{"Halv Adresse AS"}, []string{"", "", "", "Göteborg", "", "se"}), nil
			},
			wantErrs: []importErrorJSON{{Row: 1, Column: "postalLine1", Message: "An address's first line cannot be null or empty"}},
		},
		{
			name: "an invoice address is created with the customer", header: cells([]string{"name"}, invoiceHeader),
			row: func(t *testing.T) ([]string, func(t *testing.T)) {
				return cells([]string{"Faktura AS"}, []string{"Postboks 7", "", "0101", "Oslo", "", "no"}), func(t *testing.T) {
					got := listAddresses(t, c, customerIDByName(t, h, "Faktura AS")).Data
					if len(got) != 1 || got[0].Type != "invoice" || !got[0].IsPrimary || got[0].Line1 != "Postboks 7" {
						t.Errorf("addresses = %+v, want one primary invoice address, Postboks 7", got)
					}
				}
			},
		},
		{
			name: "the address cap is the row's error", header: cells([]string{"customerNumber"}, postalHeader),
			row: func(t *testing.T) ([]string, func(t *testing.T)) {
				cust := createCustomer(t, c, "Full Adressebok AS")
				for i := 0; i < 50; i++ {
					insertAddress(t, h, cust.Id, "delivery", i == 0)
				}
				return cells([]string{number(cust)}, storgataCells), nil
			},
			wantErrs: []importErrorJSON{{Row: 1, Column: "postalLine1", Message: "A customer can have at most 50 addresses"}},
		},
		{
			name: "an identity is replaced, as a manual one", header: cells([]string{"customerNumber"}, legalHeader),
			row: func(t *testing.T) ([]string, func(t *testing.T)) {
				cust := createCustomerWithIdentity(t, c, "Bytter AS", "no", "923609016")
				h.Exec(t, `UPDATE customers.customers SET legal_source = 'brreg' WHERE id = $1`, cust.Id)
				return []string{number(cust), "no", "business", "974760673", "Bytter Nytt AS"}, func(t *testing.T) {
					if got := fetchCustomerJSON(t, c, cust.Id).Identity; got == nil || got.Id != "974760673" {
						t.Errorf("identity = %+v, want 974760673", got)
					}
					if row := fetchLegalRow(t, h, cust.Id); row.Source != "manual" || row.Name != "Bytter Nytt AS" {
						t.Errorf("legal row = %+v, want manual, Bytter Nytt AS", row)
					}
				}
			},
		},
		{
			name: "four blank identity cells clear it", header: cells([]string{"customerNumber"}, legalHeader),
			row: func(t *testing.T) ([]string, func(t *testing.T)) {
				cust := createCustomerWithIdentity(t, c, "Uten Identitet AS", "no", "912660680")
				return []string{number(cust), "", "", "", ""}, func(t *testing.T) {
					if got := fetchCustomerJSON(t, c, cust.Id).Identity; got != nil {
						t.Errorf("identity = %+v, want none", got)
					}
				}
			},
		},
		{
			name: "an identity of the other type is refused on update", header: cells([]string{"customerNumber"}, legalHeader),
			row: func(t *testing.T) ([]string, func(t *testing.T)) {
				cust := createCustomerOfType(t, c, "Kari Nordmann", "person")
				return []string{number(cust), "no", "business", "929745760", "Kari AS"}, nil
			},
			wantErrs: []importErrorJSON{{Row: 1, Column: "legalType", Message: "A legal identity's type must match the customer type 'person', but was 'business'"}},
		},
		{
			name: "another customer's identity is refused on update", header: cells([]string{"customerNumber"}, legalHeader),
			row: func(t *testing.T) ([]string, func(t *testing.T)) {
				createCustomerWithIdentity(t, c, "Eier AS", "no", "981276957")
				cust := createCustomer(t, c, "Låner AS")
				return []string{number(cust), "no", "business", "981276957", "Låner AS"}, nil
			},
			wantErrs: []importErrorJSON{{Row: 1, Column: "legalId", Message: "Another customer already has this legal identity (%s Eier AS). Import with allowDuplicateIdentity=true to keep both."}},
		},
		{
			name: "a group is set", header: []string{"customerNumber", "group"},
			row: func(t *testing.T) ([]string, func(t *testing.T)) {
				createGroup(t, c, map[string]any{"name": "Settes"})
				cust := createCustomer(t, c, "Gruppeløs AS")
				return []string{number(cust), "settes"}, func(t *testing.T) {
					if got := groupOf(t, cust.Id); got != "Settes" {
						t.Errorf("group = %q, want Settes", got)
					}
				}
			},
		},
		{
			name: "a group is changed", header: []string{"customerNumber", "group"},
			row: func(t *testing.T) ([]string, func(t *testing.T)) {
				from := createGroup(t, c, map[string]any{"name": "Fra"})
				createGroup(t, c, map[string]any{"name": "Til"})
				cust := createCustomer(t, c, "Bytter Gruppe AS")
				putCustomerGroup(t, c, cust.Id, map[string]any{"groupId": from.Id})
				return []string{number(cust), "Til"}, func(t *testing.T) {
					if got := groupOf(t, cust.Id); got != "Til" {
						t.Errorf("group = %q, want Til", got)
					}
				}
			},
		},
		{
			name: "a blank group cell takes the customer out", header: []string{"customerNumber", "group"},
			row: func(t *testing.T) ([]string, func(t *testing.T)) {
				in := createGroup(t, c, map[string]any{"name": "Forlates"})
				cust := createCustomer(t, c, "Forlater AS")
				putCustomerGroup(t, c, cust.Id, map[string]any{"groupId": in.Id})
				return []string{number(cust), ""}, func(t *testing.T) {
					if got := groupOf(t, cust.Id); got != "" {
						t.Errorf("group = %q, want none", got)
					}
				}
			},
		},
		{
			name: "a group deleted after the file named it", header: []string{"name", "group"},
			row: func(t *testing.T) ([]string, func(t *testing.T)) {
				createGroup(t, c, map[string]any{"name": "Borte"})
				return []string{"Sen Gruppe AS", "Borte"}, nil
			},
			held: func(t *testing.T) {
				h.Exec(t, `DELETE FROM customers.customer_groups WHERE name = 'Borte'`)
			},
			wantErrs: []importErrorJSON{{Row: 1, Column: "group", Message: "Customer group %s does not exist"}},
		},
		{
			name: "a tag deleted after the file named it", header: []string{"name", "tags"},
			row: func(t *testing.T) ([]string, func(t *testing.T)) {
				createTag(t, c, map[string]any{"name": "Forsvunnet"})
				return []string{"Sen Tagg AS", "Forsvunnet"}, nil
			},
			held: func(t *testing.T) {
				h.Exec(t, `DELETE FROM customers.tags WHERE name = 'Forsvunnet'`)
			},
			wantErrs: []importErrorJSON{{Row: 1, Column: "tags", Message: "A tag this row names was deleted while the file was being imported"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row, check := tc.row(t)
			// The two messages that carry an id or a number are filled in here,
			// once the fixture that names them exists.
			for i, e := range tc.wantErrs {
				switch e.Column {
				case "group":
					tc.wantErrs[i].Message = fmt.Sprintf(e.Message, modtest.One[string](t, h,
						`SELECT id::text FROM customers.customer_groups WHERE name = 'Borte'`))
				case "legalId":
					tc.wantErrs[i].Message = fmt.Sprintf(e.Message, fmt.Sprint(modtest.One[int64](t, h,
						`SELECT customer_number FROM customers.customers WHERE name = 'Eier AS'`)))
				}
			}
			if tc.held != nil {
				defer customers.SetImportHeldHook(func(ctx context.Context) context.Context {
					tc.held(t)
					return ctx
				})()
			}
			result := importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(tc.header, row)))
			if tc.wantErrs != nil {
				if result.Failed != 1 || fmt.Sprint(result.Errors) != fmt.Sprint(tc.wantErrs) {
					t.Fatalf("result = %+v, want %+v", result, tc.wantErrs)
				}
				return
			}
			if result.Failed != 0 || result.Created+result.Updated != 1 {
				t.Fatalf("result = %+v, want the row written", result)
			}
			check(t)
		})
	}
}

// TestPostCustomersImport_OneAtATime: while one import runs — held open inside
// its run by SetImportHeldHook — a second one, dry or real and from anybody,
// is a 409 that writes nothing, and once the first has answered the next one
// runs. Not parallel: the hook is the package's.
func TestPostCustomersImport_OneAtATime(t *testing.T) {
	h := newHarness(t)
	first := authenticatedClient(t, h)
	second := authenticatedClient(t, h)

	// Only the first import to reach its rows is held; any other passes
	// straight through, so a lock that failed to refuse the second would show
	// as a 200 here rather than as a hang.
	entered, release := make(chan struct{}), make(chan struct{})
	var held atomic.Bool
	var releaseOnce sync.Once
	releaseFirst := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseFirst()
	defer customers.SetImportHeldHook(func(ctx context.Context) context.Context {
		if held.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
		return ctx
	})()
	done := make(chan *modtest.Response, 1)
	go func() {
		done <- postImport(t, first, "?dryRun=false", csvFileOf([]string{"name"}, []string{"Først AS"}))
	}()
	select {
	case <-entered:
	case <-time.After(20 * time.Second):
		t.Fatalf("the first import never reached its rows")
	}

	for _, query := range []string{"", "?dryRun=false"} {
		r := postImport(t, second, query, csvFileOf([]string{"name"}, []string{"Nummer To AS"}))
		var problem struct {
			Title  string `json:"title"`
			Status int    `json:"status"`
		}
		r.JSON(&problem)
		if r.Status != http.StatusConflict || problem.Status != http.StatusConflict || problem.Title != "An import is already running" {
			t.Errorf("%q while one runs: %d %+v, want the 409", query, r.Status, problem)
		}
	}
	releaseFirst()
	if result := importResultOf(t, <-done); result.Created != 1 {
		t.Errorf("the first import: %+v, want its row created", result)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE name = 'Nummer To AS'`); n != 0 {
		t.Errorf("%d customers written by a refused import, want none", n)
	}
	if result := importResultOf(t, postImport(t, second, "?dryRun=false", csvFileOf([]string{"name"}, []string{"Nummer To AS"}))); result.Created != 1 {
		t.Errorf("after the first answered: %+v, want the row created", result)
	}
}

// TestPostCustomersImport_ABodyPastTheRoutersCapIsTheUploadRefusal: a body
// past maxImportRequestBytes is cut off by the router's http.MaxBytesReader,
// not by the file part's own limit — here the file part is small and valid,
// behind a padding part that carries the body past the cap — and the read
// that fails is the same 400 an oversized part gets, with nothing written.
func TestPostCustomersImport_ABodyPastTheRoutersCapIsTheUploadRefusal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	padding, err := w.CreateFormField("padding")
	if err != nil {
		t.Fatalf("create the padding part: %v", err)
	}
	if _, err := padding.Write(bytes.Repeat([]byte("x"), 5*1024*1024+128*1024)); err != nil {
		t.Fatalf("write the padding part: %v", err)
	}
	file, err := w.CreateFormFile("file", "customers.csv")
	if err != nil {
		t.Fatalf("create the file part: %v", err)
	}
	if _, err := file.Write(csvFileOf([]string{"name"}, []string{"Over Taket AS"})); err != nil {
		t.Fatalf("write the file part: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close the multipart writer: %v", err)
	}

	r := c.Do(http.MethodPost, "/api/v1/customers/import?dryRun=false", nil, modtest.RawBody(w.FormDataContentType(), buf.Bytes()))
	if messages := fileRefusal(t, r); len(messages) != 1 || !strings.HasPrefix(messages[0], "A customer import is one CSV file of at most 5 MB") {
		t.Errorf("a body past the cap: file = %q", messages)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers`); n != 0 {
		t.Errorf("%d customers written, want none", n)
	}
}

// TestPostCustomersImport_AnErrorNamesItsColumnAsTheFileSpellsIt: headers match
// without regard to case, and an error names the header the person wrote, so
// it points at the cell they will look for. A create in a file with no name
// column is the row's error: the column it would name is not in the file.
func TestPostCustomersImport_AnErrorNamesItsColumnAsTheFileSpellsIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	result := importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(
		[]string{"Name", "EMAIL", "Phone", "website"},
		[]string{"Store Bokstaver AS", "nei", "", ""},
	)))
	want := []importErrorJSON{{Row: 1, Column: "EMAIL", Message: "An email address must look like name@example.com, but was 'nei'"}}
	if fmt.Sprint(result.Errors) != fmt.Sprint(want) {
		t.Errorf("errors = %+v, want %+v", result.Errors, want)
	}

	result = importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(contactHeader, []string{"post@navnlos.no", "", ""})))
	want = []importErrorJSON{{Row: 1, Message: "A new customer needs a name, and this file has no name column"}}
	if fmt.Sprint(result.Errors) != fmt.Sprint(want) {
		t.Errorf("no name column: errors = %+v, want %+v", result.Errors, want)
	}
}

// TestPostCustomersImport_ACancelledImportStopsAndFreesTheLock: an import whose
// caller went away stops before its next row — here before its first, the
// context cancelled as the run starts — writes nothing more, and leaves the
// one-import lock free, so the next import runs at once rather than being
// refused while an abandoned run finishes. (Where between rows it stops is
// TestImportRows_StopsBeforeTheRowAfterACancel's, in the package's own
// tests.) Not parallel: the hook is the package's.
func TestPostCustomersImport_ACancelledImportStopsAndFreesTheLock(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	file := csvFileOf([]string{"name"}, []string{"Forlatt AS"}, []string{"Glemt AS"})

	restore := customers.SetImportHeldHook(func(ctx context.Context) context.Context {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		return cancelled
	})
	// Only the run's context is cancelled, not the connection, so the server
	// still answers — the 500 an error nobody is waiting for becomes, which
	// the contract does not describe.
	contentType, body := multipartBody(t, "file", file)
	r := c.Do(http.MethodPost, "/api/v1/customers/import?dryRun=false", nil, modtest.RawBody(contentType, body),
		modtest.SkipContract("a cancelled run's answer is for a caller that is gone"))
	restore()
	if r.Status != http.StatusInternalServerError {
		t.Errorf("the cancelled import: status %d body %s, want the run ended by its error", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers`); n != 0 {
		t.Errorf("%d customers written by a cancelled import, want none", n)
	}
	if result := importResultOf(t, postImport(t, c, "?dryRun=false", file)); result.Created != 2 {
		t.Errorf("the next import: %+v, want both rows created", result)
	}
}
