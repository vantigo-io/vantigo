package customers_test

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is what every test of the import shares (customers import wizard
// design D5, D7): the wire shapes, a file written the export's way, the
// upload, the mapping, the worker driven through RunCycle — nothing waits on
// a real interval — and importFileNow, the whole flow in one call for the
// tests of other areas that only need "this file, imported".

// importJSON is CustomerImport as a test reads it.
type importJSON struct {
	ID            int32                 `json:"id"`
	State         string                `json:"state"`
	FileName      string                `json:"fileName"`
	SizeBytes     int64                 `json:"sizeBytes"`
	Encoding      string                `json:"encoding"`
	Separator     string                `json:"separator"`
	Rows          int                   `json:"rows"`
	Header        []string              `json:"header"`
	Sample        []importSampleRowJSON `json:"sample"`
	Mapping       *importMappingJSON    `json:"mapping"`
	Suggested     importMappingJSON     `json:"suggested"`
	Processed     int                   `json:"processed"`
	Created       int                   `json:"created"`
	Updated       int                   `json:"updated"`
	Failed        int                   `json:"failed"`
	Skipped       int                   `json:"skipped"`
	StopRequested bool                  `json:"stopRequested"`
	Error         string                `json:"error"`
	CreatedByName string                `json:"createdByName"`
	CreatedAt     string                `json:"createdAt"`
	RequestedAt   *string               `json:"requestedAt"`
	StartedAt     *string               `json:"startedAt"`
	FinishedAt    *string               `json:"finishedAt"`
}

type importSampleRowJSON struct {
	Row    int                  `json:"row"`
	Cells  []string             `json:"cells"`
	Errors []importRowErrorJSON `json:"errors"`
}

// importRowErrorJSON's Column is a pointer: absent on the wire for the row as
// a whole.
type importRowErrorJSON struct {
	Column  *int   `json:"column"`
	Message string `json:"message"`
}

type importMappingJSON struct {
	Mode                   string                    `json:"mode"`
	MatchBy                string                    `json:"matchBy,omitempty"`
	AllowDuplicateIdentity bool                      `json:"allowDuplicateIdentity"`
	Columns                []importMappingColumnJSON `json:"columns"`
}

type importMappingColumnJSON struct {
	Field  string  `json:"field"`
	Column *int    `json:"column,omitempty"`
	Value  *string `json:"value,omitempty"`
}

type importRowJSON struct {
	Row            int                  `json:"row"`
	Cells          []string             `json:"cells"`
	Outcome        string               `json:"outcome"`
	CustomerID     *int32               `json:"customerId"`
	CustomerNumber *int64               `json:"customerNumber"`
	Errors         []importRowErrorJSON `json:"errors"`
}

type importRowsPageJSON struct {
	Data       []importRowJSON `json:"data"`
	Pagination struct {
		TotalCount int `json:"totalCount"`
	} `json:"pagination"`
}

type importListJSON struct {
	Data       []importJSON `json:"data"`
	Pagination struct {
		TotalCount int `json:"totalCount"`
	} `json:"pagination"`
}

// csvFileOf is a customers file written the export's way: the BOM,
// semicolons, CRLF, a cell quoted when it has to be.
func csvFileOf(rows ...[]string) []byte {
	return csvFileWith(';', rows...)
}

// csvFileWith is csvFileOf with another separator and no byte order mark.
func csvFileWith(sep rune, rows ...[]string) []byte {
	var b strings.Builder
	if sep == ';' {
		b.WriteString(csvBOM)
	}
	for _, cells := range rows {
		for i, cell := range cells {
			if i > 0 {
				b.WriteRune(sep)
			}
			if strings.ContainsAny(cell, string(sep)+"\"\r\n") {
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
	return multipartBodyNamed(t, field, "customers.csv", data)
}

func multipartBodyNamed(t *testing.T, field, fileName string, data []byte) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile(field, fileName)
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

// postUpload POSTs data as the upload's file part.
func postUpload(t *testing.T, c *modtest.Client, data []byte) *modtest.Response {
	t.Helper()
	contentType, body := multipartBody(t, "file", data)
	return c.Do(http.MethodPost, "/api/v1/customers/imports", nil, modtest.RawBody(contentType, body))
}

// uploadImport is postUpload decoded, failing the test on anything but 201.
func uploadImport(t *testing.T, c *modtest.Client, data []byte) importJSON {
	t.Helper()
	return importOf(t, postUpload(t, c, data), http.StatusCreated)
}

// importOf decodes an import answer of the status wanted, failing the test
// on anything else.
func importOf(t *testing.T, r *modtest.Response, want int) importJSON {
	t.Helper()
	if r.Status != want {
		t.Fatalf("status %d body %s, want %d", r.Status, r.Body, want)
	}
	var imp importJSON
	r.JSON(&imp)
	return imp
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

// mappingRefusal is a mapping PUT's 400 field map.
func mappingRefusal(t *testing.T, r *modtest.Response) map[string][]string {
	t.Helper()
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	return problem.Errors
}

// conflictCodeOf is a 409's code (CustomerConflictProblem's own, top-level
// field), failing the test on another status.
func conflictCodeOf(t *testing.T, r *modtest.Response) string {
	t.Helper()
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var problem struct {
		Code string `json:"code"`
	}
	r.JSON(&problem)
	return problem.Code
}

func importPath(id int32, suffix string) string {
	return fmt.Sprintf("/api/v1/customers/imports/%d%s", id, suffix)
}

func putMapping(t *testing.T, c *modtest.Client, id int32, mapping any) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, importPath(id, "/mapping"), mapping)
}

func postCheck(t *testing.T, c *modtest.Client, id int32) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPost, importPath(id, "/check"), nil)
}

func postRun(t *testing.T, c *modtest.Client, id int32) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPost, importPath(id, "/run"), nil)
}

func getImport(t *testing.T, c *modtest.Client, id int32) importJSON {
	t.Helper()
	return importOf(t, c.Do(http.MethodGet, importPath(id, ""), nil), http.StatusOK)
}

// importRowsOf is one import's rows of an outcome ("" for every row), the
// first page of 500.
func importRowsOf(t *testing.T, c *modtest.Client, id int32, outcome string) importRowsPageJSON {
	t.Helper()
	query := "?pageSize=500"
	if outcome != "" {
		query += "&outcome=" + outcome
	}
	r := c.Do(http.MethodGet, importPath(id, "/rows")+query, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("rows: status %d body %s, want 200", r.Status, r.Body)
	}
	var page importRowsPageJSON
	r.JSON(&page)
	return page
}

// runImportWorker is one cycle of the import worker, which must run.
func runImportWorker(t *testing.T, h *modtest.Harness) {
	t.Helper()
	if ran, err := customers.NewImportWorker(h.Deps()).RunCycle(context.Background()); err != nil || !ran {
		t.Fatalf("RunCycle = %v, %v; want true, nil", ran, err)
	}
}

// columnsByHeader maps every header that names an import field to that
// field, 0-based — a file written with the export's columns maps itself.
func columnsByHeader(header []string) []importMappingColumnJSON {
	var columns []importMappingColumnJSON
	for i, h := range header {
		if h == "" || h == "error" || h == "id" || h == "ownerName" || h == "createdAt" || h == "updatedAt" {
			continue
		}
		col := i
		columns = append(columns, importMappingColumnJSON{Field: h, Column: &col})
	}
	return columns
}

// mappingByHeader is the mapping an export's file gets: every column its own
// field, createOrUpdate by customer number when there is one, create
// otherwise.
func mappingByHeader(header []string, allowDuplicateIdentity bool) importMappingJSON {
	m := importMappingJSON{Mode: "create", AllowDuplicateIdentity: allowDuplicateIdentity, Columns: columnsByHeader(header)}
	for _, h := range header {
		if h == "customerNumber" {
			m.Mode, m.MatchBy = "createOrUpdate", "customerNumber"
		}
	}
	return m
}

// checkNow uploads data, saves mapping and runs the check through the
// worker, answering the checked import.
func checkNow(t *testing.T, c *modtest.Client, h *modtest.Harness, data []byte, mapping importMappingJSON) importJSON {
	t.Helper()
	imp := uploadImport(t, c, data)
	importOf(t, putMapping(t, c, imp.ID, mapping), http.StatusOK)
	importOf(t, postCheck(t, c, imp.ID), http.StatusOK)
	runImportWorker(t, h)
	checked := getImport(t, c, imp.ID)
	if checked.State != "checked" {
		t.Fatalf("after the check the import is %q (%s), want checked", checked.State, checked.Error)
	}
	return checked
}

// importNow is checkNow followed by the run, answering the finished import.
// A check that found nothing to import is answered as it is, done or not.
func importNow(t *testing.T, c *modtest.Client, h *modtest.Harness, data []byte, mapping importMappingJSON) importJSON {
	t.Helper()
	checked := checkNow(t, c, h, data, mapping)
	if checked.Created+checked.Updated == 0 {
		return checked
	}
	importOf(t, postRun(t, c, checked.ID), http.StatusOK)
	runImportWorker(t, h)
	done := getImport(t, c, checked.ID)
	if done.State != "done" {
		t.Fatalf("after the run the import is %q (%s), want done", done.State, done.Error)
	}
	return done
}

// importErrorJSON is one row's error as the old import answered it, with the
// column as the file's header: what the tests of other areas compare on.
type importErrorJSON struct {
	Row     int
	Column  string
	Message string
}

// importResultJSON is what a file did, gathered from the import and its
// failed rows: the shape the tests of other areas read.
type importResultJSON struct {
	Rows    int
	Created int
	Updated int
	Failed  int
	Errors  []importErrorJSON
}

// importFileNow is "this file, imported": the export's own columns mapped by
// header, checked and run through the worker, the failed rows' errors
// gathered with their headers. For a test whose subject is not the import.
func importFileNow(t *testing.T, c *modtest.Client, h *modtest.Harness, data []byte) importResultJSON {
	t.Helper()
	imp := uploadImport(t, c, data)
	done := importNow(t, c, h, data, mappingByHeader(imp.Header, false))
	return importResultOf(t, c, done)
}

// importResultOf gathers an import's counts and its failed rows' errors.
func importResultOf(t *testing.T, c *modtest.Client, imp importJSON) importResultJSON {
	t.Helper()
	result := importResultJSON{Rows: imp.Rows, Created: imp.Created, Updated: imp.Updated, Failed: imp.Failed}
	for _, row := range importRowsOf(t, c, imp.ID, "failed").Data {
		for _, e := range row.Errors {
			column := ""
			if e.Column != nil && *e.Column < len(imp.Header) {
				column = imp.Header[*e.Column]
			}
			result.Errors = append(result.Errors, importErrorJSON{Row: row.Row, Column: column, Message: e.Message})
		}
	}
	return result
}

// customerIDByName is the id of the one customer called name.
func customerIDByName(t *testing.T, h *modtest.Harness, name string) int32 {
	t.Helper()
	return modtest.One[int32](t, h, `SELECT id FROM customers.customers WHERE name = $1`, name)
}

// legalHeader is the legal identity's four columns, as the export spells them.
var legalHeader = []string{"legalCountry", "legalType", "legalId", "legalName"}

// cells joins groups of cells into one row.
func cells(groups ...[]string) []string {
	var out []string
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// waitForALockWait waits until some other session of the harness's database
// is waiting on a lock — the request or the worker a test started in a
// goroutine has reached the statement the test's open transaction holds up.
func waitForALockWait(t *testing.T, h *modtest.Harness) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if h.Count(t, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND pid <> pg_backend_pid() AND wait_event_type = 'Lock'`) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no session came to wait on a lock")
}
