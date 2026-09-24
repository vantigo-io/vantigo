package customers_test

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

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

// TestPostCustomersImport_DefaultsToADryRunThatWritesNothing_AndReportsTheRealRun
// is D3's dry run: without dryRun the import only checks — no customer, no
// event, no customer number burned — and what it reports is exactly what the
// real run then does.
func TestPostCustomersImport_DefaultsToADryRunThatWritesNothing_AndReportsTheRealRun(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	existing := createCustomer(t, c, "Eksisterende AS")
	file := csvFileOf(
		cells([]string{"customerNumber", "name"}, contactHeader),
		[]string{"", "Ny Kunde AS", "post@ny.no", "", ""},
		[]string{fmt.Sprint(existing.CustomerNumber), "Eksisterende og Omdøpt AS", "", "", ""},
		[]string{"", "Feil AS", "ikke-en-adresse", "", ""},
	)
	customers := h.Count(t, `SELECT count(*) FROM customers.customers`)
	events := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries`)
	counter := modtest.One[int64](t, h, `SELECT next_value FROM customers.counters WHERE counter_name = 'customer-number'`)

	dry := importResultOf(t, postImport(t, c, "", file))
	if !dry.DryRun || dry.Rows != 3 || dry.Created != 1 || dry.Updated != 1 || dry.Failed != 1 {
		t.Errorf("dry run = %+v, want 1 created, 1 updated, 1 failed", dry)
	}
	if h.Count(t, `SELECT count(*) FROM customers.customers`) != customers ||
		h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries`) != events ||
		modtest.One[int64](t, h, `SELECT next_value FROM customers.counters WHERE counter_name = 'customer-number'`) != counter {
		t.Errorf("the dry run wrote something")
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
