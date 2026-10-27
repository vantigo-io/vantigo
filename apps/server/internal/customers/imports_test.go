package customers_test

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the import's endpoints and its rows (customers import wizard
// design D2–D5, D7): the upload and its refusals, the mapping and its
// refusals, the preview, the lifecycle's transitions, the rows and the
// failed-rows file — and, through the worker, every rule a row is written
// under: create and update by number and by legal id, a mapped field written
// and an unmapped one kept, the identity's defaults, fixed values, the
// duplicate guard, the vocabularies by name. The worker's own behaviour —
// chunks, stop, resume, the sweep, the nudge — is import_worker_test.go.

// uploadClient is a client holding every key the import can need, and the
// display name the uploader's events carry.
func uploadClient(t *testing.T, h *modtest.Harness) (*modtest.Client, string) {
	t.Helper()
	c, id := authenticatedClientWithID(t, h)
	return c, userDisplayName(t, h, id)
}

func col(i int) *int { return &i }

func val(s string) *string { return &s }

// mappingOf is a mapping of fields to columns by position, in mode with its
// match key.
func mappingOf(mode, matchBy string, fields ...string) importMappingJSON {
	m := importMappingJSON{Mode: mode, MatchBy: matchBy}
	for i, f := range fields {
		if f == "" {
			continue
		}
		m.Columns = append(m.Columns, importMappingColumnJSON{Field: f, Column: col(i)})
	}
	return m
}

func TestPostCustomersImports_ReadsTheFileOnce_AndAnswersADraftWithItsSuggestion(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, uploader := uploadClient(t, h)
	file := csvFileWith(',', []string{"Kundenr", "Firmanavn", "Org.nr", "E-post", "Noe annet"},
		[]string{"", "Acme AS", "923 609 016", "post@acme.no", "x"},
		[]string{"", "", "", "", ""},
		[]string{"1001", "Beta AS", "", "", "y"})
	imp := uploadImport(t, c, file)
	if imp.State != "draft" || imp.Rows != 2 || imp.Encoding != "utf-8" || imp.Separator != "," || imp.FileName != "customers.csv" || imp.SizeBytes != int64(len(file)) {
		t.Errorf("import = %+v, want a draft of 2 rows read with commas", imp)
	}
	if strings.Join(imp.Header, "|") != "Kundenr|Firmanavn|Org.nr|E-post|Noe annet" {
		t.Errorf("header = %q", imp.Header)
	}
	if len(imp.Sample) != 2 || imp.Sample[0].Row != 1 || imp.Sample[1].Row != 2 || imp.Sample[1].Cells[1] != "Beta AS" || len(imp.Sample[0].Errors) != 0 {
		t.Errorf("sample = %+v, want the two rows, numbered, with no errors before a mapping", imp.Sample)
	}
	if imp.Mapping != nil {
		t.Errorf("mapping = %+v, want none yet", imp.Mapping)
	}
	suggested := map[string]int{}
	for _, c := range imp.Suggested.Columns {
		suggested[c.Field] = *c.Column
	}
	if imp.Suggested.Mode != "createOrUpdate" || imp.Suggested.MatchBy != "legalId" ||
		suggested["customerNumber"] != 0 || suggested["name"] != 1 || suggested["legalId"] != 2 || suggested["email"] != 3 || len(suggested) != 4 {
		t.Errorf("suggested = %+v, want createOrUpdate by legalId with four columns", imp.Suggested)
	}
	if imp.CreatedByName != uploader {
		t.Errorf("createdByName = %q, want %q", imp.CreatedByName, uploader)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.import_rows WHERE import_id = $1`, imp.ID); n != 2 {
		t.Errorf("%d rows stored, want 2", n)
	}
	if got := getImport(t, c, imp.ID); got.ID != imp.ID || got.State != "draft" {
		t.Errorf("GET = %+v", got)
	}
}

func TestPostCustomersImports_RefusesWhatItCannotTake(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := uploadClient(t, h)

	twoParts := func() (string, []byte) {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		for _, name := range []string{"a.csv", "b.csv"} {
			part, _ := w.CreateFormFile("file", name)
			_, _ = part.Write([]byte("name\nAcme\n"))
		}
		_ = w.Close()
		return w.FormDataContentType(), buf.Bytes()
	}
	noPart := func() (string, []byte) {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		_ = w.WriteField("other", "x")
		_ = w.Close()
		return w.FormDataContentType(), buf.Bytes()
	}
	for _, tc := range []struct {
		name string
		body func() (string, []byte)
		want string
	}{
		{"no file part", noPart, "one CSV file of at most 50 MB"},
		{"two file parts", twoParts, "one CSV file of at most 50 MB"},
		{"an empty file", func() (string, []byte) { return multipartBody(t, "file", nil) }, "one CSV file of at most 50 MB"},
		{"only blank rows", func() (string, []byte) { return multipartBody(t, "file", []byte("\r\n;;\r\n")) }, "The file is empty"},
		{"too many columns", func() (string, []byte) {
			return multipartBody(t, "file", []byte(strings.Repeat("c;", 200)+"c\nx\n"))
		}, "more than 200 columns"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			contentType, body := tc.body()
			r := c.Do(http.MethodPost, "/api/v1/customers/imports", nil, modtest.RawBody(contentType, body))
			if msgs := fileRefusal(t, r); len(msgs) != 1 || !strings.Contains(msgs[0], tc.want) {
				t.Errorf("refusal = %q, want one message saying %q", msgs, tc.want)
			}
		})
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.imports`); n != 0 {
		t.Errorf("%d imports after the refusals, want none", n)
	}
}

func TestPostCustomersImports_TheRowCap_AndTheByteCap(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := uploadClient(t, h)

	var tall bytes.Buffer
	tall.WriteString("name\n")
	for i := 0; i <= 100_000; i++ {
		tall.WriteString("x\n")
	}
	if msgs := fileRefusal(t, postUpload(t, c, tall.Bytes())); len(msgs) != 1 || !strings.Contains(msgs[0], "more than 100000 rows") {
		t.Errorf("one row past the cap: %q", msgs)
	}
	atCap := tall.Bytes()[:tall.Len()-2]
	if imp := uploadImport(t, c, atCap); imp.Rows != 100_000 {
		t.Errorf("at the cap: %d rows, want 100000", imp.Rows)
	}

	// One byte past 50 MiB is the upload refusal; the router's own cap fires
	// first for a body that much larger, and the part read reports it the same way.
	big := bytes.Repeat([]byte("x"), 50*1024*1024+1)
	copy(big, []byte("name\n"))
	if msgs := fileRefusal(t, postUpload(t, c, big)); len(msgs) != 1 || !strings.Contains(msgs[0], "at most 50 MB") {
		t.Errorf("one byte past the cap: %q", msgs)
	}
}

func TestPutImportMapping_RefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := uploadClient(t, h)
	imp := uploadImport(t, c, csvFileOf([]string{"a", "b", "c"}, []string{"1", "2", "3"}))

	for _, tc := range []struct {
		name    string
		mapping any
		field   string
		want    string
	}{
		{"an unknown mode", map[string]any{"mode": "upsert", "allowDuplicateIdentity": false, "columns": []any{}}, "mode", "one of 'create', 'update' or 'createOrUpdate'"},
		{"update without a match key", importMappingJSON{Mode: "update", Columns: []importMappingColumnJSON{{Field: "name", Column: col(0)}}}, "matchBy", "needs 'matchBy'"},
		{"an unknown match key", map[string]any{"mode": "update", "matchBy": "email", "allowDuplicateIdentity": false, "columns": []any{map[string]any{"field": "name", "column": 0}}}, "matchBy", "one of 'customerNumber' or 'legalId'"},
		{"an unknown field", mappingOf("create", "", "name", "ownerName"), "columns", "not a field an import writes"},
		{"a field twice", importMappingJSON{Mode: "create", Columns: []importMappingColumnJSON{{Field: "name", Column: col(0)}, {Field: "name", Column: col(1)}}}, "columns", "maps 'name' a second time"},
		{"a column twice", importMappingJSON{Mode: "create", Columns: []importMappingColumnJSON{{Field: "name", Column: col(0)}, {Field: "email", Column: col(0)}}}, "columns", "maps column 0 a second time"},
		{"a column past the header", importMappingJSON{Mode: "create", Columns: []importMappingColumnJSON{{Field: "name", Column: col(3)}}}, "columns", "columns 0 to 2"},
		{"both a column and a value", importMappingJSON{Mode: "create", Columns: []importMappingColumnJSON{{Field: "name", Column: col(0), Value: val("x")}}}, "columns", "both a column and a value"},
		{"neither a column nor a value", importMappingJSON{Mode: "create", Columns: []importMappingColumnJSON{{Field: "name"}}}, "columns", "neither a column nor a value"},
		{"a fixed customer number", importMappingJSON{Mode: "update", MatchBy: "customerNumber", Columns: []importMappingColumnJSON{{Field: "customerNumber", Value: val("1")}, {Field: "name", Column: col(0)}}}, "columns", "a match key is read from a column"},
		{"no field at all", importMappingJSON{Mode: "create"}, "columns", "No field is mapped"},
		{"only the match key", importMappingJSON{Mode: "update", MatchBy: "customerNumber", Columns: []importMappingColumnJSON{{Field: "customerNumber", Column: col(0)}}}, "columns", "No field is mapped"},
		{"the match column not mapped", importMappingJSON{Mode: "createOrUpdate", MatchBy: "legalId", Columns: []importMappingColumnJSON{{Field: "name", Column: col(0)}}}, "columns", "needs a column mapped to 'legalId'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			errs := mappingRefusal(t, putMapping(t, c, imp.ID, tc.mapping))
			if msgs := errs[tc.field]; len(msgs) == 0 || !strings.Contains(strings.Join(msgs, " "), tc.want) {
				t.Errorf("errors = %v, want one on %s saying %q", errs, tc.field, tc.want)
			}
		})
	}
	if after := getImport(t, c, imp.ID); after.Mapping != nil {
		t.Errorf("a refused mapping was saved: %+v", after.Mapping)
	}
}

func TestPutImportMapping_NeedsTheKeysTheContentNeedsByHand(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := uploadClient(t, h)
	imp := uploadImport(t, owner, csvFileOf([]string{"name", "legalId", "currency", "customerNumber"}, []string{"Acme", "923609016", "NOK", ""}))
	base := []string{"customers:import", "customers:view"}
	for _, tc := range []struct {
		name    string
		keys    []string
		mapping importMappingJSON
		field   string
		want    string
	}{
		{"create without customers:create", []string{"customers:update"}, mappingOf("create", "", "name"), "mode", "customers:create"},
		{"update without customers:update", []string{"customers:create"}, mappingOf("update", "customerNumber", "name", "", "", "customerNumber"), "mode", "customers:update"},
		{"createOrUpdate without customers:update", []string{"customers:create"}, mappingOf("createOrUpdate", "customerNumber", "name", "", "", "customerNumber"), "mode", "customers:update"},
		{"an identity field without legal-identity-manage", []string{"customers:create", "customers:billing-manage"}, mappingOf("create", "", "name", "legalId"), "columns", "customers:legal-identity-manage"},
		{"a billing field without billing-manage", []string{"customers:create", "customers:legal-identity-manage"}, mappingOf("create", "", "name", "", "currency"), "columns", "customers:billing-manage"},
		{"a fixed billing value without billing-manage", []string{"customers:create"}, importMappingJSON{Mode: "create", Columns: []importMappingColumnJSON{{Field: "name", Column: col(0)}, {Field: "currency", Value: val("NOK")}}}, "columns", "customers:billing-manage"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := h.SignIn(t, append(slices.Clone(base), tc.keys...)...)
			errs := mappingRefusal(t, putMapping(t, c, imp.ID, tc.mapping))
			if msgs := errs[tc.field]; len(msgs) == 0 || !strings.Contains(strings.Join(msgs, " "), tc.want) {
				t.Errorf("errors = %v, want one on %s naming %s", errs, tc.field, tc.want)
			}
		})
	}
	// With every key, the same mapping is saved.
	full := h.SignIn(t, append(slices.Clone(base), "customers:create", "customers:update", "customers:legal-identity-manage", "customers:billing-manage")...)
	saved := importOf(t, putMapping(t, full, imp.ID, mappingOf("createOrUpdate", "customerNumber", "name", "legalId", "currency", "customerNumber")), http.StatusOK)
	if saved.Mapping == nil || saved.Mapping.Mode != "createOrUpdate" || len(saved.Mapping.Columns) != 4 {
		t.Errorf("saved = %+v, want the mapping as sent", saved.Mapping)
	}
}

func TestImports_AreBehindTheImportKey_AndView(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, tc := range []struct {
		name string
		keys []string
	}{
		{"every write key but import", []string{"customers:view", "customers:create", "customers:update", "customers:legal-identity-manage", "customers:billing-manage"}},
		{"import without view", []string{"customers:import", "customers:create", "customers:update"}},
	} {
		c := h.SignIn(t, tc.keys...)
		if r := postUpload(t, c, csvFileOf([]string{"name"}, []string{"Acme"})); r.Status != http.StatusForbidden {
			t.Errorf("%s: upload: status %d, want 403", tc.name, r.Status)
		}
		if r := c.Do(http.MethodGet, "/api/v1/customers/imports", nil); r.Status != http.StatusForbidden {
			t.Errorf("%s: list: status %d, want 403", tc.name, r.Status)
		}
	}
	// The door alone admits the upload; what the mapping may write is the
	// mapping's business (TestPutImportMapping_NeedsTheKeysTheContentNeedsByHand).
	door := h.SignIn(t, "customers:import", "customers:view")
	if r := postUpload(t, door, csvFileOf([]string{"name"}, []string{"Acme"})); r.Status != http.StatusCreated {
		t.Errorf("import and view alone: upload status %d body %s, want 201", r.Status, r.Body)
	}
}

func TestPutImportMapping_AnswersThePreviewUnderTheMapping(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := uploadClient(t, h)
	imp := uploadImport(t, c, csvFileOf([]string{"Navn", "Telefon", "Gruppe", "Kundenr"},
		[]string{"Acme AS", "+47 22 33 44 55", "Kunder", ""},
		[]string{"", "nope", "", ""}))

	// The phone column mapped as the email: the second cell's error, on column 1.
	preview := importOf(t, putMapping(t, c, imp.ID, mappingOf("create", "", "name", "email")), http.StatusOK)
	if len(preview.Sample) != 2 {
		t.Fatalf("sample = %+v", preview.Sample)
	}
	if errs := preview.Sample[0].Errors; len(errs) != 1 || errs[0].Column == nil || *errs[0].Column != 1 || !strings.Contains(errs[0].Message, "email address") {
		t.Errorf("row 1 errors = %+v, want the phone refused as an email on column 1", errs)
	}
	// Row 2: a blank name on a create is the name column's error; "nope" the email's.
	if errs := preview.Sample[1].Errors; len(errs) != 2 || errs[0].Column == nil || *errs[0].Column != 0 || errs[1].Column == nil || *errs[1].Column != 1 {
		t.Errorf("row 2 errors = %+v, want one on column 0 (the name) and one on column 1, in file order", errs)
	}

	// A fixed value with an unknown group: every row's error, with no column.
	fixed := importMappingJSON{Mode: "create", Columns: []importMappingColumnJSON{{Field: "name", Column: col(0)}, {Field: "group", Value: val("Nobody")}}}
	preview = importOf(t, putMapping(t, c, imp.ID, fixed), http.StatusOK)
	if errs := preview.Sample[0].Errors; len(errs) != 1 || errs[0].Column != nil || errs[0].Message != "No customer group is named 'Nobody'" {
		t.Errorf("row 1 errors under a fixed unknown group = %+v", errs)
	}

	// The preview of a create with no name column is the row's own problem.
	preview = importOf(t, putMapping(t, c, imp.ID, mappingOf("create", "", "", "phone")), http.StatusOK)
	if errs := preview.Sample[0].Errors; len(errs) != 1 || errs[0].Column != nil || !strings.Contains(errs[0].Message, "needs a name") {
		t.Errorf("row 1 errors with no name mapped = %+v", errs)
	}
	// Under an update mode an unmapped field's problem is the customer's to
	// answer, so the preview keeps only the mapped fields' own errors.
	preview = importOf(t, putMapping(t, c, imp.ID, mappingOf("update", "customerNumber", "", "phone", "", "customerNumber")), http.StatusOK)
	if errs := preview.Sample[0].Errors; len(errs) != 0 {
		t.Errorf("row 1 errors under update mode = %+v, want none (the name comes from the customer)", errs)
	}
	if errs := preview.Sample[1].Errors; len(errs) != 1 || *errs[0].Column != 1 {
		t.Errorf("row 2 errors under update mode = %+v, want the phone's alone", errs)
	}
	if got := getImport(t, c, imp.ID); got.Mapping == nil || got.Mapping.Mode != "update" || len(got.Sample[0].Errors) != 0 {
		t.Errorf("GET after the mapping = %+v, want the mapping and the same preview", got)
	}
}

func TestImportLifecycle_TheTransitions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := uploadClient(t, h)
	file := csvFileOf([]string{"name"}, []string{"Acme AS"})
	imp := uploadImport(t, c, file)

	// Nothing runs without a mapping; nothing runs before a check.
	if r := postCheck(t, c, imp.ID); r.Status != http.StatusBadRequest || !strings.Contains(string(r.Body), "Map the file's columns") {
		t.Errorf("check without a mapping: status %d body %s, want 400 on mapping", r.Status, r.Body)
	}
	if code := conflictCodeOf(t, postRun(t, c, imp.ID)); code != "import_not_checked" {
		t.Errorf("run of a draft: code %s, want import_not_checked", code)
	}
	if code := conflictCodeOf(t, c.Do(http.MethodPost, importPath(imp.ID, "/stop"), nil)); code != "import_finished" {
		t.Errorf("stop of a draft: code %s, want import_finished", code)
	}
	importOf(t, putMapping(t, c, imp.ID, mappingOf("create", "", "name")), http.StatusOK)

	// A check asked for, not yet taken up: checking with no start; a second
	// check is 409; a mapping save is 409; a delete is 409; a stop goes
	// straight back to draft, nothing having run.
	checking := importOf(t, postCheck(t, c, imp.ID), http.StatusOK)
	if checking.State != "checking" || checking.RequestedAt == nil || checking.StartedAt != nil {
		t.Errorf("after check = %+v, want checking, requested, not started", checking)
	}
	if code := conflictCodeOf(t, postCheck(t, c, imp.ID)); code != "import_running" {
		t.Errorf("check while checking: code %s", code)
	}
	if code := conflictCodeOf(t, putMapping(t, c, imp.ID, mappingOf("create", "", "name"))); code != "import_running" {
		t.Errorf("mapping while checking: code %s", code)
	}
	if code := conflictCodeOf(t, c.Do(http.MethodDelete, importPath(imp.ID, ""), nil)); code != "import_running" {
		t.Errorf("delete while checking: code %s", code)
	}
	if back := importOf(t, c.Do(http.MethodPost, importPath(imp.ID, "/stop"), nil), http.StatusOK); back.State != "draft" || back.RequestedAt != nil {
		t.Errorf("stop of an unstarted check = %+v, want draft again", back)
	}

	// Checked, with something to import: the run is allowed; done takes no more.
	checked := checkNow(t, c, h, file, mappingOf("create", "", "name"))
	if checked.Created != 1 || checked.Failed != 0 || checked.Processed != 1 || checked.FinishedAt == nil {
		t.Errorf("checked = %+v, want one row that would be created", checked)
	}
	// A check of a checked import is a check again.
	if again := importOf(t, postCheck(t, c, checked.ID), http.StatusOK); again.State != "checking" || again.Created != 0 {
		t.Errorf("check again = %+v, want checking with the counts reset", again)
	}
	runImportWorker(t, h)
	importing := importOf(t, postRun(t, c, checked.ID), http.StatusOK)
	if importing.State != "importing" {
		t.Errorf("after run = %+v", importing)
	}
	// Not taken up yet: the run's request cleared the check's results, so a
	// stop checks the file again — never checked with counts and no rows.
	if back := importOf(t, c.Do(http.MethodPost, importPath(checked.ID, "/stop"), nil), http.StatusOK); back.State != "checking" || back.Created != 0 || back.RequestedAt == nil {
		t.Errorf("stop of an unstarted run = %+v, want checking again with the counts reset", back)
	}
	runImportWorker(t, h)
	if rechecked := getImport(t, c, checked.ID); rechecked.State != "checked" || rechecked.Created != 1 || len(importRowsOf(t, c, checked.ID, "created").Data) != 1 {
		t.Errorf("after the stop's check = %+v, want checked with its row again", rechecked)
	}
	importOf(t, postRun(t, c, checked.ID), http.StatusOK)
	runImportWorker(t, h)
	done := getImport(t, c, checked.ID)
	if done.State != "done" || done.Created != 1 || done.FinishedAt == nil {
		t.Errorf("done = %+v", done)
	}
	for name, r := range map[string]*modtest.Response{
		"check":   postCheck(t, c, done.ID),
		"run":     postRun(t, c, done.ID),
		"stop":    c.Do(http.MethodPost, importPath(done.ID, "/stop"), nil),
		"mapping": putMapping(t, c, done.ID, mappingOf("create", "", "name")),
	} {
		if code := conflictCodeOf(t, r); code != "import_finished" {
			t.Errorf("%s of a done import: code %s, want import_finished", name, code)
		}
	}

	// A check that finds nothing to import cannot be run.
	nothing := checkNow(t, c, h, csvFileOf([]string{"name", "email"}, []string{"Acme AS", "nope"}), mappingOf("create", "", "name", "email"))
	if nothing.Failed != 1 || nothing.Created != 0 {
		t.Fatalf("nothing = %+v", nothing)
	}
	if code := conflictCodeOf(t, postRun(t, c, nothing.ID)); code != "import_nothing_to_import" {
		t.Errorf("run with nothing to import: code %s", code)
	}
	// Saving a mapping on a checked import returns it to draft with the
	// results cleared.
	redrafted := importOf(t, putMapping(t, c, nothing.ID, mappingOf("create", "", "name", "email")), http.StatusOK)
	if redrafted.State != "draft" || redrafted.Failed != 0 || redrafted.Processed != 0 || redrafted.FinishedAt != nil {
		t.Errorf("redrafted = %+v, want draft with the counts cleared", redrafted)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.import_rows WHERE import_id = $1 AND outcome IS NOT NULL`, nothing.ID); n != 0 {
		t.Errorf("%d rows keep an outcome after the mapping changed, want none", n)
	}

	// Deletes: a draft and a done import go with their rows; an unknown id is 404.
	for _, id := range []int32{redrafted.ID, done.ID} {
		if r := c.Do(http.MethodDelete, importPath(id, ""), nil); r.Status != http.StatusNoContent {
			t.Errorf("delete %d: status %d body %s", id, r.Status, r.Body)
		}
		if n := h.Count(t, `SELECT count(*) FROM customers.import_rows WHERE import_id = $1`, id); n != 0 {
			t.Errorf("%d rows left after deleting import %d", n, id)
		}
	}
	for name, r := range map[string]*modtest.Response{
		"get":    c.Do(http.MethodGet, importPath(done.ID, ""), nil),
		"delete": c.Do(http.MethodDelete, importPath(done.ID, ""), nil),
		"check":  postCheck(t, c, done.ID),
		"rows":   c.Do(http.MethodGet, importPath(done.ID, "/rows"), nil),
		"failed": c.Do(http.MethodGet, importPath(done.ID, "/failed-rows"), nil),
	} {
		if r.Status != http.StatusNotFound {
			t.Errorf("%s of a deleted import: status %d, want 404", name, r.Status)
		}
	}
	// The customer the done import created is untouched by its deletion.
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE name = 'Acme AS'`); n != 1 {
		t.Errorf("%d customers named Acme AS after the import was deleted, want 1", n)
	}
}

func TestImportRows_CreateAndUpdateByCustomerNumber_AsTheUploader(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, uploader := uploadClient(t, h)
	existing := createCustomer(t, c, "Old Name AS")
	file := csvFileOf([]string{"customerNumber", "name", "email"},
		[]string{"", "New One AS", "one@new.no"},
		[]string{strconv.FormatInt(existing.CustomerNumber, 10), "Renamed AS", "renamed@old.no"},
		[]string{"99999", "Nobody AS", ""})
	done := importNow(t, c, h, file, mappingOf("createOrUpdate", "customerNumber", "customerNumber", "name", "email"))
	if done.Created != 1 || done.Updated != 1 || done.Failed != 1 || done.Processed != 3 {
		t.Fatalf("done = %+v, want 1 created, 1 updated, 1 failed", done)
	}

	rows := importRowsOf(t, c, done.ID, "")
	if len(rows.Data) != 3 || rows.Pagination.TotalCount != 3 {
		t.Fatalf("rows = %+v", rows)
	}
	created := rows.Data[0]
	if created.Outcome != "created" || created.CustomerID == nil || created.CustomerNumber == nil || len(created.Errors) != 0 {
		t.Errorf("row 1 = %+v, want created with its customer", created)
	}
	if got := fetchCustomerJSON(t, c, *created.CustomerID); got.Name != "New One AS" || deref(got.ContactInfo.Email) != "one@new.no" {
		t.Errorf("created customer = %+v", got)
	}
	updated := rows.Data[1]
	if updated.Outcome != "updated" || updated.CustomerID == nil || *updated.CustomerID != existing.Id {
		t.Errorf("row 2 = %+v, want the existing customer updated", updated)
	}
	if got := fetchCustomerJSON(t, c, existing.Id); got.Name != "Renamed AS" || deref(got.ContactInfo.Email) != "renamed@old.no" {
		t.Errorf("updated customer = %+v", got)
	}
	// An unknown number in createOrUpdate is a typo, not a new customer.
	failed := rows.Data[2]
	if failed.Outcome != "failed" || len(failed.Errors) != 1 || failed.Errors[0].Column == nil || *failed.Errors[0].Column != 0 || failed.Errors[0].Message != "No customer has number 99999" {
		t.Errorf("row 3 = %+v, want refused on column 0", failed)
	}
	if onlyFailed := importRowsOf(t, c, done.ID, "failed"); len(onlyFailed.Data) != 1 || onlyFailed.Data[0].Row != 3 {
		t.Errorf("failed rows = %+v", onlyFailed)
	}
	if onlyCreated := importRowsOf(t, c, done.ID, "created"); len(onlyCreated.Data) != 1 || onlyCreated.Data[0].Row != 1 {
		t.Errorf("created rows = %+v", onlyCreated)
	}

	// Every event carries the uploader, snapshotted on the import.
	for _, id := range []int32{*created.CustomerID, existing.Id} {
		for _, e := range timelineOf(t, c, id) {
			if e.ActorKind != "user" || deref(e.ActorDisplay) != uploader {
				t.Errorf("customer %d event %s actor = %s %q, want user %q", id, e.EventType, e.ActorKind, deref(e.ActorDisplay), uploader)
			}
		}
	}
	if n := len(entriesOfType(timelineOf(t, c, existing.Id), "customer.updated")); n != 1 {
		t.Errorf("the update recorded %d customer.updated events, want 1", n)
	}
	// The rows' paging and the outcome filter's refusal.
	if r := c.Do(http.MethodGet, importPath(done.ID, "/rows?outcome=ignored"), nil); r.Status != http.StatusBadRequest || !strings.Contains(string(r.Body), "'skipped'") {
		t.Errorf("rows?outcome=ignored: status %d body %s, want 400 naming the four outcomes", r.Status, r.Body)
	}
	if r := c.Do(http.MethodGet, importPath(done.ID, "/rows?pageSize=501"), nil); r.Status != http.StatusBadRequest {
		t.Errorf("rows?pageSize=501: status %d, want 400", r.Status)
	}
}

func TestImportRows_UpdateModeRefusesARowWithNoMatch_AndABlankKey(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := uploadClient(t, h)
	existing := createCustomer(t, c, "Known AS")
	file := csvFileOf([]string{"customerNumber", "name"},
		[]string{strconv.FormatInt(existing.CustomerNumber, 10), "Known Renamed AS"},
		[]string{"", "Blank Key AS"},
		[]string{"abc", "Bad Key AS"})
	done := importNow(t, c, h, file, mappingOf("update", "customerNumber", "customerNumber", "name"))
	result := importResultOf(t, c, done)
	if done.Updated != 1 || done.Created != 0 || done.Failed != 2 {
		t.Fatalf("done = %+v", done)
	}
	want := []importErrorJSON{
		{Row: 2, Column: "customerNumber", Message: "This row has no customer number to find the customer by"},
		{Row: 3, Column: "customerNumber", Message: "A customer number must be a whole number, but was 'abc'"},
	}
	if fmt.Sprint(result.Errors) != fmt.Sprint(want) {
		t.Errorf("errors = %v, want %v", result.Errors, want)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers`); n != 1 {
		t.Errorf("%d customers, want the one known (update mode never creates)", n)
	}
}

func TestImportRows_MatchByLegalId(t *testing.T) {
	t.Parallel()
	h := newHarness(t, unknownToTheRegistry())
	c, _ := uploadClient(t, h)
	holder := createCustomerWithIdentity(t, c, "Holder AS", "no", validOrgNumbers[0])
	// Two customers share the second identity, allowed by hand.
	createCustomerWithIdentity(t, c, "Twin One AS", "no", validOrgNumbers[1])
	createCustomerWithIdentity(t, c, "Twin Two AS", "no", validOrgNumbers[1], map[string]any{"allowDuplicateIdentity": true})

	file := csvFileOf([]string{"Navn", "Org.nr", "E-post"},
		[]string{"Holder Renamed AS", validOrgNumbers[0], "post@holder.no"},
		[]string{"Newcomer AS", validOrgNumbers[2], "post@new.no"},
		[]string{"Either Twin AS", validOrgNumbers[1], ""},
		[]string{"No Id AS", "", ""})
	mapping := mappingOf("createOrUpdate", "legalId", "name", "legalId", "email")
	done := importNow(t, c, h, file, mapping)
	result := importResultOf(t, c, done)
	if done.Updated != 1 || done.Created != 2 || done.Failed != 1 {
		t.Fatalf("createOrUpdate by legal id = %+v, errors %v", done, result.Errors)
	}
	if got := fetchCustomerJSON(t, c, holder.Id); got.Name != "Holder Renamed AS" || deref(got.ContactInfo.Email) != "post@holder.no" {
		t.Errorf("the holder = %+v, want it updated by its organisation number", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE name = 'Newcomer AS' AND legal_id = $1 AND legal_country = 'no' AND legal_type = 'business' AND legal_name = 'Newcomer AS' AND legal_source = 'manual'`, validOrgNumbers[2]); n != 1 {
		t.Errorf("the newcomer was not created with the identity's defaults")
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE name = 'No Id AS' AND legal_id IS NULL`); n != 1 {
		t.Errorf("a row with no legal id was not simply created")
	}
	if len(result.Errors) != 1 || result.Errors[0].Row != 3 || result.Errors[0].Column != "Org.nr" ||
		!strings.Contains(result.Errors[0].Message, "2 customers have this legal identity") || !strings.Contains(result.Errors[0].Message, "Twin One AS") {
		t.Errorf("the twins' row = %v, want refused naming both holders", result.Errors)
	}

	// Update mode: no holder is the row's error, in the identity's own words.
	strict := importNow(t, c, h, csvFileOf([]string{"Navn", "Org.nr"}, []string{"Ghost AS", validOrgNumbers[3]}), mappingOf("update", "legalId", "name", "legalId"))
	if errs := importResultOf(t, c, strict).Errors; len(errs) != 1 || errs[0].Message != "No customer has the legal identity NO "+validOrgNumbers[3] {
		t.Errorf("update by an unknown legal id = %v", errs)
	}
}

func TestImportRows_AMappedFieldIsWritten_AnUnmappedOneIsKept(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := uploadClient(t, h)
	cust := createCustomer(t, c, "Keep Me AS")
	number := strconv.FormatInt(cust.CustomerNumber, 10)
	if r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/contact-info", cust.Id), map[string]any{"email": "old@keep.no", "phone": "+47 22 00 00 00", "website": "https://keep.no"}); r.Status != http.StatusOK {
		t.Fatalf("contact info: %d %s", r.Status, r.Body)
	}
	if r := postAddress(t, c, cust.Id, map[string]any{"type": "postal", "line1": "Storgata 1", "postalCode": "0150", "city": "Oslo", "country": "NO", "isPrimary": true}); r.Status != http.StatusCreated {
		t.Fatalf("address: %d %s", r.Status, r.Body)
	}
	if r := putBillingProfile(t, c, cust.Id, map[string]any{"paymentTermsDays": 14, "currency": "NOK", "invoiceEmail": "faktura@keep.no"}); r.Status != http.StatusOK {
		t.Fatalf("billing: %d %s", r.Status, r.Body)
	}

	// Email mapped alone: the phone and the website stay. The city mapped
	// alone: the street stays. The terms mapped alone: the currency stays.
	done := importNow(t, c, h,
		csvFileOf([]string{"nr", "epost", "poststed", "dager"}, []string{number, "new@keep.no", "Bergen", "30"}),
		mappingOf("update", "customerNumber", "customerNumber", "email", "postalCity", "paymentTermsDays"))
	if done.Updated != 1 || done.Failed != 0 {
		t.Fatalf("done = %+v, errors %v", done, importResultOf(t, c, done).Errors)
	}
	got := fetchCustomerJSON(t, c, cust.Id)
	if deref(got.ContactInfo.Email) != "new@keep.no" || deref(got.ContactInfo.Phone) != "+47 22 00 00 00" || deref(got.ContactInfo.Website) != "https://keep.no" {
		t.Errorf("contact info = %+v, want the email changed and the rest kept", got.ContactInfo)
	}
	var addresses addressListJSON
	getAddresses(t, c, cust.Id).JSON(&addresses)
	if len(addresses.Data) != 1 || addresses.Data[0].Line1 != "Storgata 1" || deref(addresses.Data[0].City) != "Bergen" || deref(addresses.Data[0].PostalCode) != "0150" {
		t.Errorf("addresses = %+v, want the city changed and the street kept", addresses.Data)
	}
	var billing billingProfileJSON
	getBillingProfile(t, c, cust.Id).JSON(&billing)
	if billing.PaymentTermsDays == nil || *billing.PaymentTermsDays != 30 || deref(billing.Currency) != "NOK" || deref(billing.InvoiceEmail) != "faktura@keep.no" {
		t.Errorf("billing = %+v, want the terms changed and the rest kept", billing)
	}

	// A mapped cell that is blank clears its field; a blank name is refused.
	done = importNow(t, c, h,
		csvFileOf([]string{"nr", "epost", "navn"}, []string{number, "", ""}),
		mappingOf("update", "customerNumber", "customerNumber", "email", "name"))
	if errs := importResultOf(t, c, done).Errors; done.Failed != 1 || len(errs) != 1 || errs[0].Column != "navn" {
		t.Errorf("a blank name on an update = %+v %v, want refused on the name", done, errs)
	}
	done = importNow(t, c, h, csvFileOf([]string{"nr", "epost"}, []string{number, ""}), mappingOf("update", "customerNumber", "customerNumber", "email"))
	if got := fetchCustomerJSON(t, c, cust.Id); done.Updated != 1 || got.ContactInfo.Email != nil || deref(got.ContactInfo.Phone) != "+47 22 00 00 00" {
		t.Errorf("a blank mapped email = %+v, want it cleared and the phone kept", got.ContactInfo)
	}

	// The city mapped alone onto a customer with no postal address: an address
	// needs a line and a country, which the row cannot give — the row's error.
	other := createCustomer(t, c, "No Address AS")
	done = importNow(t, c, h,
		csvFileOf([]string{"nr", "poststed"}, []string{strconv.FormatInt(other.CustomerNumber, 10), "Bergen"}),
		mappingOf("update", "customerNumber", "customerNumber", "postalCity"))
	if errs := importResultOf(t, c, done).Errors; done.Failed != 1 || len(errs) == 0 {
		t.Errorf("a city alone with no address on file = %+v %v, want refused", done, errs)
	}
	// An unchanged row writes nothing: the revision stands.
	before := fetchCustomerJSON(t, c, cust.Id)
	done = importNow(t, c, h, csvFileOf([]string{"nr", "epost"}, []string{number, ""}), mappingOf("update", "customerNumber", "customerNumber", "email"))
	if after := fetchCustomerJSON(t, c, cust.Id); done.Updated != 1 || after.Revision != before.Revision {
		t.Errorf("a row repeating what is on file moved the revision from %d to %d", before.Revision, after.Revision)
	}
}

func TestImportRows_TypeSynonyms_FixedValues_AndVocabularyByName(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := uploadClient(t, h)
	group := createGroup(t, c, map[string]any{"name": "Importert 2026"})
	tag := createTag(t, c, map[string]any{"name": "Tripletex"})
	vip := createTag(t, c, map[string]any{"name": "VIP"})

	file := csvFileOf([]string{"Navn", "Type", "Merkelapper"},
		[]string{"Firma AS", "Bedrift", "vip"},
		[]string{"Kari Nordmann", "Privatperson", ""},
		[]string{"Ukjent AS", "Noe", "Finnes ikke"})
	mapping := importMappingJSON{Mode: "create", Columns: []importMappingColumnJSON{
		{Field: "name", Column: col(0)}, {Field: "type", Column: col(1)}, {Field: "tags", Column: col(2)},
		{Field: "status", Value: val("disabled")}, {Field: "group", Value: val("importert 2026")}, {Field: "currency", Value: val("NOK")},
	}}
	done := importNow(t, c, h, file, mapping)
	result := importResultOf(t, c, done)
	if done.Created != 2 || done.Failed != 1 {
		t.Fatalf("done = %+v errors %v", done, result.Errors)
	}
	firma := fetchCustomerJSON(t, c, customerIDByName(t, h, "Firma AS"))
	if firma.Status != "disabled" || firma.Group == nil || firma.Group.Id != group.Id || len(firma.Tags) != 1 || firma.Tags[0].Id != vip.Id {
		t.Errorf("Firma AS = %+v, want disabled, in the group, tagged VIP (matched ignoring case)", firma)
	}
	if typ := modtest.One[string](t, h, `SELECT type FROM customers.customers WHERE name = 'Firma AS'`); typ != "business" {
		t.Errorf("Bedrift read as %q, want business", typ)
	}
	if typ := modtest.One[string](t, h, `SELECT type FROM customers.customers WHERE name = 'Kari Nordmann'`); typ != "person" {
		t.Errorf("Privatperson read as %q, want person", typ)
	}
	var billing billingProfileJSON
	getBillingProfile(t, c, firma.Id).JSON(&billing)
	if deref(billing.Currency) != "NOK" {
		t.Errorf("a fixed currency was not written: %+v", billing)
	}
	if len(result.Errors) != 2 || result.Errors[0].Row != 3 || result.Errors[0].Column != "Type" || result.Errors[1].Column != "Merkelapper" || result.Errors[1].Message != "No tag is named 'Finnes ikke'" {
		t.Errorf("row 3 errors = %v, want the type and the tag refused, in file order", result.Errors)
	}
	_ = tag
}

func TestImportRows_TheDuplicateGuard_InTheFileAndOnFile(t *testing.T) {
	t.Parallel()
	h := newHarness(t, unknownToTheRegistry())
	c, _ := uploadClient(t, h)
	createCustomerWithIdentity(t, c, "Taken AS", "no", validOrgNumbers[0])
	file := csvFileOf([]string{"name", "legalId"},
		[]string{"First AS", validOrgNumbers[1]},
		[]string{"Second AS", validOrgNumbers[1]},
		[]string{"Taker AS", validOrgNumbers[0]},
		[]string{"Bad Digit AS", "923609017"})

	checked := checkNow(t, c, h, file, mappingOf("create", "", "name", "legalId"))
	result := importResultOf(t, c, checked)
	if checked.Created != 1 || checked.Failed != 3 {
		t.Fatalf("checked = %+v errors %v", checked, result.Errors)
	}
	if e := result.Errors[0]; e.Row != 2 || e.Column != "legalId" || !strings.Contains(e.Message, "Row 1 of this file already has this legal identity") {
		t.Errorf("the in-file duplicate = %+v", e)
	}
	if e := result.Errors[1]; e.Row != 3 || e.Column != "legalId" || !strings.Contains(e.Message, "Taken AS") || !strings.Contains(e.Message, "Allow a customer to share") {
		t.Errorf("the duplicate on file = %+v, want the holder named and the way past", e)
	}
	if e := result.Errors[2]; e.Row != 4 || !strings.Contains(e.Message, "check digit") {
		t.Errorf("the bad check digit = %+v", e)
	}
	// The check kept nothing.
	if n := h.Count(t, `SELECT count(*) FROM customers.customers`); n != 1 {
		t.Errorf("%d customers after the check, want the one on file", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries`); n != 1 {
		t.Errorf("%d timeline entries after the check, want the one creation's", n)
	}

	// Allowed, the duplicate on file is created; the in-file one still is not.
	allowed := mappingOf("create", "", "name", "legalId")
	allowed.AllowDuplicateIdentity = true
	done := importNow(t, c, h, file, allowed)
	if done.Created != 2 || done.Failed != 2 {
		t.Errorf("allowed = %+v, want First and Taker created, Second and Bad Digit refused", done)
	}
}

// TestImportRows_ARepeatedCustomerIsSkipped is a supplier's export: one row
// per metering point, the customer repeated on each. A repeat whose mapped
// cells are all equal to the first row's — trimmed, an unmapped column free to
// differ — is skipped in the check and in the run alike, counted apart and
// kept out of the failed-rows file; a repeat that differs in a mapped cell is
// still the row's error.
func TestImportRows_ARepeatedCustomerIsSkipped(t *testing.T) {
	t.Parallel()
	h := newHarness(t, unknownToTheRegistry())
	c, _ := uploadClient(t, h)
	file := csvFileOf([]string{"name", "legalId", "MALEPUNKT"},
		[]string{"Kraft AS", validOrgNumbers[2], "707057500000000001"},
		[]string{"Kraft AS", validOrgNumbers[2], "707057500000000002"},
		[]string{" Kraft AS ", validOrgNumbers[2], "707057500000000003"},
		[]string{"Kraft Holding AS", validOrgNumbers[2], "707057500000000004"},
		[]string{"Annen AS", validOrgNumbers[3], "707057500000000005"})
	mapping := mappingOf("createOrUpdate", "legalId", "name", "legalId")

	checked := checkNow(t, c, h, file, mapping)
	if checked.Created != 2 || checked.Skipped != 2 || checked.Failed != 1 || checked.Processed != 5 {
		t.Fatalf("checked = %+v, want 2 created, 2 skipped, 1 failed", checked)
	}
	skipped := importRowsOf(t, c, checked.ID, "skipped")
	if skipped.Pagination.TotalCount != 2 || skipped.Data[0].Row != 2 || skipped.Data[1].Row != 3 {
		t.Fatalf("skipped rows = %+v, want rows 2 and 3", skipped)
	}
	for _, row := range skipped.Data {
		if row.Outcome != "skipped" || len(row.Errors) != 1 || row.Errors[0].Column != nil || row.Errors[0].Message != "Same customer as row 1." {
			t.Errorf("skipped row %d = %+v, want the row-level 'Same customer as row 1.'", row.Row, row)
		}
	}
	result := importResultOf(t, c, checked)
	if len(result.Errors) != 1 || result.Errors[0].Row != 4 || !strings.Contains(result.Errors[0].Message, "Row 1 of this file already has this legal identity") {
		t.Errorf("failed = %+v, want row 4, which names the customer otherwise, refused as a duplicate", result.Errors)
	}

	importOf(t, postRun(t, c, checked.ID), http.StatusOK)
	runImportWorker(t, h)
	done := getImport(t, c, checked.ID)
	if done.State != "done" || done.Created != 2 || done.Skipped != 2 || done.Failed != 1 || done.Processed != 5 {
		t.Fatalf("done = %+v, want 2 created, 2 skipped, 1 failed", done)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE name = 'Kraft AS'`); n != 1 {
		t.Errorf("%d customers named Kraft AS, want the one", n)
	}
	if got := importRowsOf(t, c, done.ID, "skipped").Pagination.TotalCount; got != 2 {
		t.Errorf("%d skipped rows after the run, want 2", got)
	}
	r := c.Do(http.MethodGet, importPath(done.ID, "/failed-rows"), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("failed rows: %d %s", r.Status, r.Body)
	}
	if lines := strings.Split(strings.TrimSuffix(string(r.Body), "\r\n"), "\r\n"); len(lines) != 2 || !strings.Contains(lines[1], "Kraft Holding AS") {
		t.Errorf("failed rows file = %q, want the header and the differing repeat alone", r.Body)
	}
}

// TestImportRows_ARepeatedCustomerIsSkipped_WithDuplicatesAllowed: allowing a
// shared identity changes nothing about the file's own repeats — an identical
// one is skipped, a differing one refused, as without it.
func TestImportRows_ARepeatedCustomerIsSkipped_WithDuplicatesAllowed(t *testing.T) {
	t.Parallel()
	h := newHarness(t, unknownToTheRegistry())
	c, _ := uploadClient(t, h)
	file := csvFileOf([]string{"name", "legalId"},
		[]string{"Kraft AS", validOrgNumbers[4]},
		[]string{"Kraft AS", validOrgNumbers[4]},
		[]string{"Kraft Holding AS", validOrgNumbers[4]})
	mapping := mappingOf("create", "", "name", "legalId")
	mapping.AllowDuplicateIdentity = true
	done := importNow(t, c, h, file, mapping)
	if done.Created != 1 || done.Skipped != 1 || done.Failed != 1 {
		t.Errorf("done = %+v, want 1 created, 1 skipped, 1 failed", done)
	}
}

func TestImportRows_ATypeChangeIsRefused_AndATooLongRow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := uploadClient(t, h)
	cust := createCustomer(t, c, "Business AS")
	done := importNow(t, c, h,
		csvFileOf([]string{"customerNumber", "type"},
			[]string{strconv.FormatInt(cust.CustomerNumber, 10), "person"},
			[]string{strconv.FormatInt(cust.CustomerNumber, 10), "business", "extra"}),
		mappingOf("update", "customerNumber", "customerNumber", "type"))
	errs := importResultOf(t, c, done).Errors
	if done.Failed != 2 || len(errs) != 2 || !strings.Contains(errs[0].Message, "never by an import") || errs[1].Message != "This row has 3 cells, but the header has 2" {
		t.Errorf("done = %+v errors %v", done, errs)
	}
}

// TestImportRows_ADataExceptionFailsTheRowNotTheRun: a value the database
// refuses as too long or out of range (SQLSTATE class 22) — one the checks
// let through — fails its row with a generic message, in the check and the
// run alike. It used to fail the whole run, which the worker retried every
// cycle, oldest first, so the import behind it never ran.
func TestImportRows_ADataExceptionFailsTheRowNotTheRun(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := uploadClient(t, h)
	// A trigger stands in for a column the checks do not know the limit of.
	h.Exec(t, `CREATE FUNCTION customers.refuse_poison_for_test() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.name = 'Poison AS' THEN
				RAISE EXCEPTION 'numeric field overflow' USING ERRCODE = '22003';
			END IF;
			RETURN NEW;
		END $$`)
	h.Exec(t, `CREATE TRIGGER refuse_poison_for_test BEFORE INSERT OR UPDATE ON customers.customers
		FOR EACH ROW EXECUTE FUNCTION customers.refuse_poison_for_test()`)
	mapping := mappingOf("create", "", "name", "email")
	first := uploadImport(t, c, csvFileOf([]string{"name", "email"}, []string{"Good AS", "good@example.test"}, []string{"Poison AS", "poison@example.test"}))
	second := uploadImport(t, c, csvFileOf([]string{"name", "email"}, []string{"Behind AS", "behind@example.test"}))
	for _, imp := range []importJSON{first, second} {
		importOf(t, putMapping(t, c, imp.ID, mapping), http.StatusOK)
		importOf(t, postCheck(t, c, imp.ID), http.StatusOK)
	}
	runImportWorker(t, h)
	checked := getImport(t, c, first.ID)
	if checked.State != "checked" || checked.Error != "" || checked.Created != 1 || checked.Failed != 1 {
		t.Fatalf("the first check = %+v, want checked with one row created and the poisoned one failed", checked)
	}
	if behind := getImport(t, c, second.ID); behind.State != "checked" || behind.Created != 1 {
		t.Errorf("the import behind it = %+v, want checked in the same cycle", behind)
	}
	failed := importRowsOf(t, c, first.ID, "failed").Data
	if len(failed) != 1 || failed[0].Row != 2 || len(failed[0].Errors) != 1 || failed[0].Errors[0].Column != nil ||
		failed[0].Errors[0].Message != "A value is out of range or too long for its field." {
		t.Errorf("failed rows = %+v, want row 2 with the one generic message on no column", failed)
	}
	importOf(t, postRun(t, c, first.ID), http.StatusOK)
	runImportWorker(t, h)
	if done := getImport(t, c, first.ID); done.State != "done" || done.Error != "" || done.Created != 1 || done.Failed != 1 {
		t.Errorf("the run = %+v, want done with one created and the poisoned row failed", done)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE name = 'Poison AS'`); n != 0 {
		t.Errorf("the poisoned row wrote %d customers, want none", n)
	}
}

// TestImportLifecycle_ACheckAskedAsARunIsTakenUpKeepsTheRunsOutcomes: a
// check asked for while the worker takes the import's run up — the handler
// having read a state that allows the check — is the running import's 409,
// and the outcomes the run writes are kept. It used to clear every row's
// outcome before finding the import no longer checkable.
func TestImportLifecycle_ACheckAskedAsARunIsTakenUpKeepsTheRunsOutcomes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := uploadClient(t, h)
	checked := checkNow(t, c, h, manyRows(2), mappingOf("create", "", "name", "email"))
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The worker under way, not yet committed: the import importing, its
	// first row's outcome written.
	if _, err := tx.Exec(ctx, `UPDATE customers.imports SET state = 'importing', started_at = now() WHERE id = $1`, checked.ID); err != nil {
		t.Fatalf("take the run up: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE customers.import_rows SET outcome = 'created' WHERE import_id = $1 AND row_no = 1`, checked.ID); err != nil {
		t.Fatalf("write the first outcome: %v", err)
	}
	answered := make(chan *modtest.Response, 1)
	go func() { answered <- postCheck(t, c, checked.ID) }()
	waitForALockWait(t, h)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if code := conflictCodeOf(t, <-answered); code != "import_running" {
		t.Errorf("code %q, want import_running", code)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.import_rows WHERE import_id = $1 AND row_no = 1 AND outcome = 'created'`, checked.ID); n != 1 {
		t.Error("the run's outcome on row 1 was cleared by the check")
	}
}

func TestGetImportFailedRows_IsTheFileToFixAndUploadAgain(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := uploadClient(t, h)
	file := csvFileOf([]string{"Navn", "E-post", ""},
		[]string{"Good AS", "ok@good.no", "x"},
		[]string{"", "nope", "y;z"})
	done := importNow(t, c, h, file, mappingOf("create", "", "name", "email"))
	if done.Created != 1 || done.Failed != 1 {
		t.Fatalf("done = %+v", done)
	}
	r := c.Do(http.MethodGet, importPath(done.ID, "/failed-rows"), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("failed rows: %d %s", r.Status, r.Body)
	}
	want := csvBOM + "error;Navn;E-post;\r\n" +
		"Navn: A friendly name cannot be null or empty | E-post: An email address must look like name@example.com, but was 'nope';;nope;\"y;z\"\r\n"
	if string(r.Body) != want {
		t.Errorf("failed rows file =\n%q\nwant\n%q", r.Body, want)
	}
	if got := r.Header("Content-Disposition"); got != fmt.Sprintf(`attachment; filename="customers-import-%d-failed-rows.csv"`, done.ID) {
		t.Errorf("Content-Disposition = %q", got)
	}
	// Uploaded again, the file maps as the import that made it did: the error
	// column is nobody's, the others keep their mapping.
	again := uploadImport(t, c, r.Body)
	if again.Suggested.Mode != "create" || len(again.Suggested.Columns) != 2 || *again.Suggested.Columns[0].Column != 1 || *again.Suggested.Columns[1].Column != 2 {
		t.Errorf("the failed-rows file's suggestion = %+v, want name from column 1 and email from column 2, the error column unmapped", again.Suggested)
	}
}

func TestGetCustomersImports_ListsNewestChangeFirst(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := uploadClient(t, h)
	first := uploadImport(t, c, csvFileOf([]string{"name"}, []string{"A"}))
	h.Advance(time.Minute)
	second := uploadImport(t, c, csvFileOf([]string{"name"}, []string{"B"}))
	h.Advance(time.Minute)
	importOf(t, putMapping(t, c, first.ID, mappingOf("create", "", "name")), http.StatusOK)

	r := c.Do(http.MethodGet, "/api/v1/customers/imports?pageSize=1", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list: %d %s", r.Status, r.Body)
	}
	var page importListJSON
	r.JSON(&page)
	if page.Pagination.TotalCount != 2 || len(page.Data) != 1 || page.Data[0].ID != first.ID {
		t.Errorf("page 1 = %+v, want the import last changed first (the mapping save)", page)
	}
	r = c.Do(http.MethodGet, "/api/v1/customers/imports?pageSize=1&page=2", nil)
	r.JSON(&page)
	if len(page.Data) != 1 || page.Data[0].ID != second.ID {
		t.Errorf("page 2 = %+v", page)
	}
	if r := c.Do(http.MethodGet, "/api/v1/customers/imports?pageSize=101", nil); r.Status != http.StatusBadRequest {
		t.Errorf("pageSize=101: status %d, want 400", r.Status)
	}
	if r := c.Do(http.MethodGet, "/api/v1/customers/imports?page=0", nil); r.Status != http.StatusBadRequest {
		t.Errorf("page=0: status %d, want 400", r.Status)
	}
}

func TestPostCustomersImports_IsRateLimitedPerAddress(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := uploadClient(t, h)
	file := csvFileOf([]string{"name"}, []string{"A"})
	for i := 0; i < 600; i++ {
		if r := postUpload(t, c, file); r.Status != http.StatusCreated {
			t.Fatalf("upload %d: status %d body %s", i+1, r.Status, r.Body)
		}
	}
	if r := postUpload(t, c, file); r.Status != http.StatusTooManyRequests || r.Header("Retry-After") == "" {
		t.Errorf("the 601st upload: status %d Retry-After %q, want 429 with Retry-After", r.Status, r.Header("Retry-After"))
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
