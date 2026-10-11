package staff_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// The register's import (design S19; plan Task 3): POST /people/import reads
// the register's file and writes each row through the endpoints' own paths.

const (
	peopleImportPath   = "/api/v1/staff/people/import"
	peopleExportPath   = "/api/v1/staff/people/export.csv"
	importTemplatePath = "/api/v1/staff/people/import-template.csv"
)

// utf8BOM is the byte order mark the export writes first.
var utf8BOM = string([]byte{0xEF, 0xBB, 0xBF})

// csvOf is a file of rows, the first the header, joined the export's way.
func csvOf(rows ...string) string {
	return utf8BOM + strings.Join(rows, "\r\n") + "\r\n"
}

// postImport sends data as the file part of one import; dryRun "" leaves the
// flag out, which is a dry run.
func postImport(t *testing.T, c *modtest.Client, data []byte, dryRun string, opts ...modtest.RequestOption) *modtest.Response {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "staff.csv")
	if err != nil {
		t.Fatalf("create the file part: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write the file part: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close the multipart writer: %v", err)
	}
	path := peopleImportPath
	if dryRun != "" {
		path += "?dryRun=" + dryRun
	}
	return c.Do(http.MethodPost, path, nil, append([]modtest.RequestOption{modtest.RawBody(w.FormDataContentType(), buf.Bytes())}, opts...)...)
}

// importCSV runs one import and fails the test unless it answered 200.
func importCSV(t *testing.T, c *modtest.Client, file string, dryRun bool) gen.StaffImportResult {
	t.Helper()
	r := postImport(t, c, []byte(file), fmt.Sprint(dryRun))
	if r.Status != http.StatusOK {
		t.Fatalf("import: status %d body %s, want 200", r.Status, r.Body)
	}
	var out gen.StaffImportResult
	r.JSON(&out)
	return out
}

// rowErrors is a result's errors as "row/column: message" lines.
func rowErrors(res gen.StaffImportResult) []string {
	var out []string
	for _, e := range res.Errors {
		column := ""
		if e.Column != nil {
			column = *e.Column
		}
		out = append(out, fmt.Sprintf("%d/%s: %s", e.Row, column, e.Message))
	}
	return out
}

// hasRowError reports whether res has an error on row and column whose
// message contains part.
func hasRowError(res gen.StaffImportResult, row int32, column, part string) bool {
	for _, e := range res.Errors {
		c := ""
		if e.Column != nil {
			c = *e.Column
		}
		if e.Row == row && c == column && strings.Contains(e.Message, part) {
			return true
		}
	}
	return false
}

// getPerson reads one person as c may.
func getPerson(t *testing.T, c *modtest.Client, id int64) gen.StaffPerson {
	t.Helper()
	var out gen.StaffPerson
	do(t, c, http.MethodGet, personPath(id), nil, http.StatusOK, &out)
	return out
}

func countPeople(t *testing.T, h *harness) int {
	t.Helper()
	return h.Count(t, `SELECT count(*) FROM staff.people`)
}

// A dry run is the real run rolled back: it reports what each row would do —
// a create, an update, a failure with its column — and keeps nothing: no
// person, no event, the employee-number sequence where it was. Only the real
// run writes, and records the installation's people_imported event.
func TestImport_DryRunReportsAndKeepsNothing_RealRunCreatesAndUpdates(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	kari := createPerson(t, hr, with(newPerson("Kari", "Nordmann"), "employeeNumber", "E-1"))
	file := csvOf(
		"employeeNumber;givenName;familyName;workEmail;phoneMobile",
		"e-1;Kari;Nordmann-Hansen;kari@example.no;+47 900 00 000",
		"E-2;Ola;Nordmann;ola@example.no;",
		"E-3;;Hansen;;",
	)
	people, events := countPeople(t, h), h.Count(t, `SELECT count(*) FROM staff.events`)

	dry := importCSV(t, hr, file, true)
	if !dry.DryRun || dry.Rows != 3 || dry.Created != 1 || dry.Updated != 1 || dry.Failed != 1 {
		t.Errorf("dry run = %+v, want rows 3, created 1, updated 1, failed 1", dry)
	}
	if !hasRowError(dry, 3, "givenName", "given name") {
		t.Errorf("dry run errors = %v, want row 3's givenName", rowErrors(dry))
	}
	if got := countPeople(t, h); got != people {
		t.Errorf("people after a dry run = %d, want %d", got, people)
	}
	if got := h.Count(t, `SELECT count(*) FROM staff.events`); got != events {
		t.Errorf("events after a dry run = %d, want %d", got, events)
	}
	if got := getPerson(t, hr, kari.Id); got.FamilyName != "Nordmann" {
		t.Errorf("Kari after a dry run = %q, want unchanged", got.FamilyName)
	}

	// A dry run with the flag left out is a dry run too.
	r := postImport(t, hr, []byte(file), "")
	var flagless gen.StaffImportResult
	r.JSON(&flagless)
	if !flagless.DryRun || countPeople(t, h) != people {
		t.Errorf("import without dryRun = %+v, want a dry run that kept nothing", flagless)
	}
	refused(t, postImport(t, hr, []byte(file), "yes"), http.StatusBadRequest, "")

	real := importCSV(t, hr, file, false)
	if real.DryRun || real.Rows != 3 || real.Created != 1 || real.Updated != 1 || real.Failed != 1 {
		t.Errorf("real run = %+v, want rows 3, created 1, updated 1, failed 1", real)
	}
	updated := getPerson(t, hr, kari.Id)
	if updated.FamilyName != "Nordmann-Hansen" || updated.WorkEmail == nil || *updated.WorkEmail != "kari@example.no" ||
		updated.PhoneMobile == nil || *updated.PhoneMobile != "+47 900 00 000" || updated.EmployeeNumber != "E-1" {
		t.Errorf("Kari after the import = %+v", updated)
	}
	if got := countPeople(t, h); got != people+1 {
		t.Errorf("people after the real run = %d, want %d", got, people+1)
	}
	ola := modtest.One[int64](t, h.Harness, `SELECT id FROM staff.people WHERE employee_number = 'E-2'`)
	if kinds := kinds(listEvents(t, hr, ola)); len(kinds) != 1 || kinds[0] != "person_created" {
		t.Errorf("Ola's history = %v, want person_created", kinds)
	}
	if kinds := kinds(listEvents(t, hr, kari.Id)); len(kinds) != 2 || kinds[0] != "person_updated" {
		t.Errorf("Kari's history = %v, want person_updated over person_created", kinds)
	}
	payload := modtest.One[string](t, h.Harness, `
		SELECT payload::text FROM staff.events WHERE person_id IS NULL AND kind = 'people_imported'`)
	for _, want := range []string{`"rows": 3`, `"created": 1`, `"updated": 1`, `"failed": 1`} {
		if !strings.Contains(payload, want) {
			t.Errorf("people_imported payload = %s, want %s", payload, want)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE kind = 'people_imported'`); n != 1 {
		t.Errorf("people_imported events = %d, want the real run's one", n)
	}
}

// The import is HR's: a caller without staff:manage is the uniform 403.
func TestImport_NeedsManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	viewer, _ := signIn(t, h, "staff:view", "staff:personal-data")
	file := []byte(csvOf("employeeNumber;givenName;familyName", "E-1;Kari;Nordmann"))
	if r := postImport(t, viewer, file, "true"); r.Status != http.StatusForbidden {
		t.Errorf("import without staff:manage = %d %s, want 403", r.Status, r.Body)
	}
	for _, path := range []string{peopleExportPath, importTemplatePath} {
		if r := viewer.Do(http.MethodGet, path, nil); r.Status != http.StatusForbidden {
			t.Errorf("%s without staff:manage = %d, want 403", path, r.Status)
		}
	}
}

// The header is judged before any row, every refusal at once: a column the
// table does not know is refused by name, the export-only columns and a
// failed-rows file's error column are ignored, and the file needs its
// employeeNumber column.
func TestImport_Header(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")

	p := refused(t, postImport(t, hr, []byte(csvOf("employeeNumber;givenName;familyName;shoeSize;Hobby", "E-1;Kari;Nordmann;42;ski")), "true"),
		http.StatusBadRequest, "")
	if msgs := strings.Join(p.Errors["file"], " "); !strings.Contains(msgs, "'shoeSize'") || !strings.Contains(msgs, "'Hobby'") {
		t.Errorf("unknown columns refusal = %v, want both named", p.Errors)
	}
	p = refused(t, postImport(t, hr, []byte(csvOf("givenName;familyName", "Kari;Nordmann")), "true"), http.StatusBadRequest, "")
	if msgs := strings.Join(p.Errors["file"], " "); !strings.Contains(msgs, "employeeNumber") {
		t.Errorf("no employeeNumber column = %v, want it named", p.Errors)
	}
	p = refused(t, postImport(t, hr, []byte(csvOf("employeeNumber;givenName;GIVENNAME", "E-1;Kari;Kari")), "true"), http.StatusBadRequest, "")
	if msgs := strings.Join(p.Errors["file"], " "); !strings.Contains(msgs, "more than once") {
		t.Errorf("a repeated column = %v, want it refused", p.Errors)
	}

	res := importCSV(t, hr, csvOf(
		"EmployeeNumber;givenName;familyName;status;currentTitle;currentDepartment;currentBusinessUnit;currentLocation;currentManagerEmployeeNumber;currentFtePercent;nationalIdOnFile;error",
		"E-1;Kari;Nordmann;active;Sjef;HR;Oslo;Kontoret;E-9;100,00;yes;an earlier failure",
	), false)
	if res.Created != 1 || res.Failed != 0 {
		t.Fatalf("export-only columns = %+v %v, want the row created and the columns ignored", res, rowErrors(res))
	}
}

// A comma-separated file is import_not_semicolon, a file over 5 MiB
// import_too_large — just over, or past the request's own cap — and one
// with more data rows than STAFF_IMPORT_MAX_ROWS import_too_many_rows, each
// before a row is read.
func TestImport_FileBounds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")

	refused(t, postImport(t, hr, []byte("employeeNumber,givenName,familyName\r\nE-1,Kari,Nordmann\r\n"), "true"),
		http.StatusBadRequest, "import_not_semicolon")

	header := "employeeNumber;givenName;familyName\r\n"
	over := header + strings.Repeat("x", 5*1024*1024+1-len(header))
	refused(t, postImport(t, hr, []byte(over), "true"), http.StatusBadRequest, "import_too_large")
	refused(t, postImport(t, hr, []byte(header+strings.Repeat("x", 6*1024*1024)), "true"), http.StatusBadRequest, "import_too_large")

	var rows strings.Builder
	rows.WriteString(header)
	for i := range 5001 {
		fmt.Fprintf(&rows, "E-%d;Kari;Nordmann\r\n", i)
	}
	refused(t, postImport(t, hr, []byte(rows.String()), "true"), http.StatusBadRequest, "import_too_many_rows")
	if got := countPeople(t, h); got != 0 {
		t.Errorf("people after the refusals = %d, want 0", got)
	}
}

// Every row needs its employee number, and two rows of one file with the
// same employee number or the same work email — however cased — refuse the
// second, in a dry run as in the real run (whose rows can see each other, but
// whose dry run's cannot).
func TestImport_RowKeysAndInFileDuplicates(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	file := csvOf(
		"employeeNumber;givenName;familyName;workEmail",
		";Kari;Nordmann;kari@example.no",
		"E-1;Ola;Nordmann;ola@example.no",
		"e-1;Per;Nordmann;per@example.no",
		"E-2;Pål;Nordmann;OLA@example.no",
		"E-3;Liv;Nordmann",
	)
	for _, dryRun := range []bool{true, false} {
		res := importCSV(t, hr, file, dryRun)
		if res.Created != 1 || res.Failed != 4 {
			t.Errorf("dryRun %v: %+v %v, want only row 2 created", dryRun, res, rowErrors(res))
		}
		for _, want := range []struct {
			row    int32
			column string
		}{{1, "employeeNumber"}, {3, "employeeNumber"}, {4, "workEmail"}, {5, ""}} {
			if !hasRowError(res, want.row, want.column, "") {
				t.Errorf("dryRun %v: errors %v, want one on row %d %q", dryRun, rowErrors(res), want.row, want.column)
			}
		}
		if !hasRowError(res, 3, "employeeNumber", "Row 2") || !hasRowError(res, 4, "workEmail", "Row 2") {
			t.Errorf("dryRun %v: errors %v, want the duplicates to name row 2", dryRun, rowErrors(res))
		}
	}
}

// Two rows of one file with the same national identity number refuse the
// second in a dry run as in the real run — the dry run's rows each roll back,
// so only the file can tell — naming the earlier row and never a digit of
// the number.
func TestImport_InFileDuplicateNationalID(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	pd, _ := signIn(t, h, "staff:manage", "staff:personal-data")
	number := testNationalID("018190", 347)
	file := csvOf(
		"employeeNumber;givenName;familyName;birthDate;nationalIdKind;nationalId",
		"D-1;Kari;Nordmann;1990-01-01;fodselsnummer;"+number,
		"D-2;Kari;Nordmann-Hansen;1990-01-01;fodselsnummer;"+number,
	)
	for _, dryRun := range []bool{true, false} {
		res := importCSV(t, pd, file, dryRun)
		if res.Created != 1 || res.Failed != 1 || !hasRowError(res, 2, "nationalId", "Row 1 of this file") {
			t.Errorf("dryRun %v: %+v %v, want row 2 refused on its nationalId, naming row 1", dryRun, res, rowErrors(res))
		}
		tally, err := json.Marshal(res)
		if err != nil {
			t.Fatalf("encode the tally: %v", err)
		}
		if leaks(string(tally), number) {
			t.Errorf("dryRun %v: the tally carries digits of the national identity number", dryRun)
		}
	}
}

// A row the create path refuses lands in the errors with the column it is
// about, in the path's own words — a work email another person has, a value
// out of its list, a department code nobody has — and writes nothing.
func TestImport_RowRefusedByTheCreatePath(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	company(t, hr)
	createPerson(t, hr, with(newPerson("Kari", "Nordmann"), "workEmail", "kari@example.no"))
	res := importCSV(t, hr, csvOf(
		"employeeNumber;givenName;familyName;workEmail;gender;birthDate;startDate;employmentType;ftePercent;department",
		"E-1;Ola;Nordmann;KARI@example.no;;;;;;",
		"E-2;Per;Nordmann;;mann;;;;;",
		"E-3;Liv;Nordmann;;;1990-13-01;;;;",
		"E-4;Siv;Nordmann;;;;2026-01-01;fast;100;NOPE",
		"E-5;Eva;Nordmann;;;;2026-01-01;fast;abc;",
		"E-6;Ida;Nordmann;;;;;fast;100;",
	), false)
	if res.Created != 0 || res.Failed != 6 {
		t.Fatalf("result = %+v %v, want every row refused", res, rowErrors(res))
	}
	for _, want := range []struct {
		row          int32
		column, part string
	}{
		{1, "workEmail", "Another person already has this work email"},
		{2, "gender", "'mann'"},
		{3, "birthDate", "1990-13-01"},
		{4, "department", "'NOPE'"},
		{5, "ftePercent", "'abc'"},
		{6, "startDate", ""},
	} {
		if !hasRowError(res, want.row, want.column, want.part) {
			t.Errorf("errors %v, want row %d on %s with %q", rowErrors(res), want.row, want.column, want.part)
		}
	}
	if got := countPeople(t, h); got != 1 {
		t.Errorf("people = %d, want only Kari", got)
	}
}

// A create with a startDate makes the first employment and its first terms
// row from the first-employment columns in the row's one transaction — the
// structure named as a person reads it, a manager by employee number,
// including one an earlier row of the file creates — and a midlertidig row
// carries its ground and expected end. A row that names a manager no one has
// writes neither the person nor the employment.
func TestImport_FirstEmployment(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	unit := company(t, hr)
	le := createLegalEntity(t, hr, map[string]any{"name": "Bergen AS"})
	bergen := createBusinessUnit(t, hr, map[string]any{"legalEntityId": le.Id, "name": "Bergen", "orgNumber": "923 609 016"})
	dept := createDepartment(t, hr, map[string]any{"code": "HR", "name": "Personal"})
	loc := createLocation(t, hr, "Kontoret")

	file := csvOf(
		"employeeNumber;givenName;familyName;startDate;businessUnit;employmentType;temporaryGround;expectedEndDate;ftePercent;appointmentForm;workingHoursScheme;fullTimeHoursPerWeek;occupationCode;title;department;location;managerEmployeeNumber",
		"B-1;Kari;Nordmann;2026-01-01;;fast;;;100;;;;;Sjef;hr;Kontoret;",
		"B-2;Ola;Nordmann;2026-02-01;923609016;midlertidig;b;2026-12-31;50,5;;not_shift;37,5;2310100;Vikar;HR;;b-1",
		"B-3;Per;Nordmann;2026-02-01;Oslo;fast;;;100;;;;;;;;NOBODY",
		"B-4;Liv;Nordmann;;;;;;;;;;;;;;",
	)
	dry := importCSV(t, hr, file, true)
	if dry.Created != 3 || dry.Failed != 1 || !hasRowError(dry, 3, "managerEmployeeNumber", "NOBODY") {
		t.Errorf("dry run = %+v %v, want rows 1, 2 and 4 created and row 3 refused on its manager", dry, rowErrors(dry))
	}
	res := importCSV(t, hr, file, false)
	if res.Created != 3 || res.Failed != 1 || !hasRowError(res, 3, "managerEmployeeNumber", "NOBODY") {
		t.Fatalf("real run = %+v %v, want rows 1, 2 and 4 created and row 3 refused on its manager", res, rowErrors(res))
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE employee_number = 'B-3'`); n != 0 {
		t.Errorf("row 3's person was written without its employment")
	}

	kari := modtest.One[int64](t, h.Harness, `SELECT id FROM staff.people WHERE employee_number = 'B-1'`)
	ola := modtest.One[int64](t, h.Harness, `SELECT id FROM staff.people WHERE employee_number = 'B-2'`)
	liv := modtest.One[int64](t, h.Harness, `SELECT id FROM staff.people WHERE employee_number = 'B-4'`)
	var employments []gen.StaffEmployment
	do(t, hr, http.MethodGet, employmentsPath(kari), nil, http.StatusOK, &employments)
	if len(employments) != 1 || employments[0].BusinessUnitId == nil || *employments[0].BusinessUnitId != unit.Id || employments[0].StartDate.String() != "2026-01-01" {
		t.Fatalf("Kari's employments = %+v, want one from 2026-01-01 in the default unit", employments)
	}
	first := listTerms(t, hr, employments[0].Id)[0]
	if first.EmploymentType != "fast" || first.FtePercent != 100 || first.Title == nil || *first.Title != "Sjef" ||
		first.DepartmentId == nil || *first.DepartmentId != dept.Id || first.LocationId == nil || *first.LocationId != loc.Id {
		t.Errorf("Kari's first terms = %+v", first)
	}

	do(t, hr, http.MethodGet, employmentsPath(ola), nil, http.StatusOK, &employments)
	if len(employments) != 1 || employments[0].BusinessUnitId == nil || *employments[0].BusinessUnitId != bergen.Id {
		t.Fatalf("Ola's employments = %+v, want one in Bergen, found by its org number", employments)
	}
	temp := listTerms(t, hr, employments[0].Id)[0]
	if temp.EmploymentType != "midlertidig" || temp.TemporaryGround == nil || *temp.TemporaryGround != "b" ||
		temp.ExpectedEndDate == nil || temp.ExpectedEndDate.String() != "2026-12-31" || temp.FtePercent != 50.5 ||
		temp.AppointmentForm == nil || *temp.AppointmentForm != "midlertidig" || temp.FullTimeHoursPerWeek == nil ||
		*temp.FullTimeHoursPerWeek != 37.5 || temp.OccupationCode == nil ||
		*temp.OccupationCode != "2310100" || temp.ManagerPersonId == nil || *temp.ManagerPersonId != kari {
		t.Errorf("Ola's first terms = %+v", temp)
	}
	if kinds := kinds(listEvents(t, hr, ola)); len(kinds) != 3 {
		t.Errorf("Ola's history = %v, want the person, the employment and the terms", kinds)
	}
	do(t, hr, http.MethodGet, employmentsPath(liv), nil, http.StatusOK, &employments)
	if len(employments) != 0 {
		t.Errorf("Liv's employments = %+v, want none without a startDate", employments)
	}

	// A midlertidig row without its ground is the terms' own refusal, on the
	// column it is about.
	res = importCSV(t, hr, csvOf(
		"employeeNumber;givenName;familyName;startDate;employmentType;ftePercent",
		"B-5;Siv;Nordmann;2026-01-01;midlertidig;100",
	), false)
	if res.Failed != 1 || !hasRowError(res, 1, "temporaryGround", "") {
		t.Errorf("midlertidig without a ground = %+v %v, want temporaryGround refused", res, rowErrors(res))
	}
}

// An update writes the person's own columns, only those the file carries: a
// blank cell clears an optional field, a column not in the file is left
// alone, and the first-employment cells of the row are not read — an update
// never touches employments.
func TestImport_UpdateNeverTouchesEmployments(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	company(t, hr)
	p, e := employed(t, hr, "Kari", "2025-01-01", terms("title", "Rådgiver"))
	do(t, hr, http.MethodPut, personPath(p.Id), with(newPerson("Kari", "Nordmann"), "phoneWork", "22 33 44 55", "city", "Oslo"),
		http.StatusOK, nil, ifMatch(p.Revision))
	before := listTerms(t, hr, e.Id)

	res := importCSV(t, hr, csvOf(
		"employeeNumber;givenName;familyName;phoneWork;startDate;employmentType;ftePercent;title",
		p.EmployeeNumber+";Kari;Nordmann;;2026-06-01;midlertidig;20;Sjef",
	), false)
	if res.Updated != 1 || res.Failed != 0 {
		t.Fatalf("update = %+v %v", res, rowErrors(res))
	}
	got := getPerson(t, hr, p.Id)
	if got.PhoneWork != nil || got.City == nil || *got.City != "Oslo" {
		t.Errorf("after the update phoneWork = %v city = %v, want cleared and kept", got.PhoneWork, got.City)
	}
	var employments []gen.StaffEmployment
	do(t, hr, http.MethodGet, employmentsPath(p.Id), nil, http.StatusOK, &employments)
	after := listTerms(t, hr, e.Id)
	if len(employments) != 1 || len(after) != len(before) || after[0].Revision != before[0].Revision {
		t.Errorf("employments %+v terms %+v, want the one employment untouched", employments, after)
	}
}

// An erased person is read-only: their row is refused person_erased through
// the central guard, and the rest of the file runs.
func TestImport_ErasedPersonsRowIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, with(newPerson("Kari", "Nordmann"), "employeeNumber", "E-1"))
	h.Exec(t, `UPDATE staff.people SET erased_at = $2 WHERE id = $1`, p.Id, h.Now())
	res := importCSV(t, hr, csvOf("employeeNumber;givenName;familyName", "E-1;Kari;Hansen", "E-2;Ola;Nordmann"), false)
	if res.Failed != 1 || res.Created != 1 || !hasRowError(res, 1, "", "erased") {
		t.Errorf("result = %+v %v, want row 1 refused as erased and row 2 created", res, rowErrors(res))
	}
	if got := getPerson(t, hr, p.Id); got.FamilyName != "Nordmann" {
		t.Errorf("erased person's family name = %q, want unchanged", got.FamilyName)
	}
}

// The sealed columns need staff:personal-data: a file carrying any of them
// from HR without it is refused whole, import_sealed_columns, and the same
// file from a holder imports — each value through the sealed path, with its
// rules (a duplicate, a birth date the number disagrees with, a number that
// is no number), and never a digit of it in the result, a log line or an
// event. A blank sealed cell changes nothing.
func TestImport_SealedColumns(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	pd, _ := signIn(t, h, "staff:manage", "staff:personal-data")
	number := testNationalID("018190", 347)
	other := testNationalID("028190", 100)
	taken := testNationalID("038190", 200)
	holder := createPerson(t, hr, with(newPerson("Siv", "Holder"), "birthDate", "1990-01-03"))
	do(t, pd, http.MethodPut, nationalIDPath(holder.Id), map[string]any{"kind": "fodselsnummer", "value": taken}, http.StatusOK, nil)
	account := "12345678903"

	file := csvOf(
		"employeeNumber;givenName;familyName;birthDate;nationalIdKind;nationalId;bankAccountKind;bankAccount",
		"S-1;Kari;Nordmann;1990-01-01;fodselsnummer;"+number+";bban;"+account,
		"S-2;Ola;Nordmann;1991-05-05;fodselsnummer;"+other+";;",
		"S-3;Per;Nordmann;1990-01-03;fodselsnummer;"+taken+";;",
		"S-4;Liv;Nordmann;;fodselsnummer;0101;bban;1234",
		"S-5;Eva;Nordmann;;;;;",
	)
	p := refused(t, postImport(t, hr, []byte(file), "true"), http.StatusBadRequest, "import_sealed_columns")
	if msgs := strings.Join(p.Errors["file"], " "); !strings.Contains(msgs, "staff:personal-data") {
		t.Errorf("sealed refusal = %v, want the permission named", p.Errors)
	}
	for _, column := range []string{"nationalIdKind", "nationalIdCountry", "nationalId", "bankAccountKind", "bankAccount"} {
		refused(t, postImport(t, hr, []byte(csvOf("employeeNumber;givenName;familyName;"+column, "S-9;Kari;Nordmann;")), "true"),
			http.StatusBadRequest, "import_sealed_columns")
	}

	res := importCSV(t, pd, file, false)
	if res.Created != 2 || res.Failed != 3 {
		t.Fatalf("result = %+v %v, want rows 1 and 5 created", res, rowErrors(res))
	}
	for _, want := range []struct {
		row          int32
		column, part string
	}{
		{2, "nationalId", "birth date"},
		{3, "nationalId", "Another person already has this national identity number"},
		{4, "nationalId", ""},
		{4, "bankAccount", ""},
	} {
		if !hasRowError(res, want.row, want.column, want.part) {
			t.Errorf("errors %v, want row %d on %s with %q", rowErrors(res), want.row, want.column, want.part)
		}
	}
	kari := modtest.One[int64](t, h.Harness, `SELECT id FROM staff.people WHERE employee_number = 'S-1'`)
	var revealed gen.StaffNationalIdReveal
	do(t, pd, http.MethodPost, nationalIDRevealPath(kari), nil, http.StatusOK, &revealed)
	if revealed.Value != number {
		t.Errorf("revealed national id is not the imported one")
	}
	var bank gen.StaffBankAccountReveal
	do(t, pd, http.MethodPost, bankAccountRevealPath(kari), nil, http.StatusOK, &bank)
	if bank.Value != account {
		t.Errorf("revealed bank account is not the imported one")
	}
	if kinds := kinds(listEvents(t, hr, kari)); !strings.Contains(strings.Join(kinds, ","), "national_id_set") ||
		!strings.Contains(strings.Join(kinds, ","), "bank_account_set") {
		t.Errorf("Kari's history = %v, want the sealed paths' events", kinds)
	}
	eva := modtest.One[int64](t, h.Harness, `SELECT id FROM staff.people WHERE employee_number = 'S-5'`)
	if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1 AND (national_id_mac IS NOT NULL OR bank_account_sealed IS NOT NULL)`, eva); n != 0 {
		t.Errorf("blank sealed cells wrote a value")
	}

	result := fmt.Sprint(rowErrors(res))
	events := strings.Join(modtest.One[[]string](t, h.Harness, `SELECT array_agg(payload::text) FROM staff.events`), " ")
	logs := scrubRandom(h.Logs())
	for _, n := range []string{number, other, taken} {
		for where, text := range map[string]string{"result": result, "logs": logs, "events": events} {
			if leaks(text, n) {
				t.Errorf("the %s carry digits of a national identity number", where)
			}
		}
	}
	for where, text := range map[string]string{"result": result, "logs": logs, "events": events} {
		if carriesMoreThanLast4(text, account) {
			t.Errorf("the %s carry more than the last four digits of the bank account", where)
		}
	}

	// A filled cell on an update sets the value; a blank one changes nothing.
	res = importCSV(t, pd, csvOf("employeeNumber;givenName;familyName;nationalIdKind;nationalId", "S-1;Kari;Nordmann;;"), false)
	if res.Updated != 1 {
		t.Errorf("blank sealed cells on an update = %+v %v", res, rowErrors(res))
	}
	do(t, pd, http.MethodPost, nationalIDRevealPath(kari), nil, http.StatusOK, &revealed)
	if revealed.Value != number {
		t.Errorf("a blank national id cell changed the number on file")
	}
}

// A real run whose caller goes away mid-file keeps the rows it ran, and still
// records people_imported with the counts so far — on a context the
// request's end does not cancel — so the history says what the file did.
func TestImport_CancelledRealRunRecordsItsTally(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	pd, pdID := signIn(t, h, "staff:manage", "staff:personal-data")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The first row's bank-change mail goes after its commit: the caller hangs
	// up there, and the server has noticed before the next row opens.
	onContractCall(t, pdID, func(method string) {
		if method == "SMTPSend" {
			cancel()
			time.Sleep(500 * time.Millisecond)
		}
	})
	file := csvOf(
		"employeeNumber;givenName;familyName;privateEmail;bankAccountKind;bankAccount",
		"X-1;Kari;Nordmann;kari@privat.no;bban;12345678903",
		"X-2;Ola;Nordmann;;;",
		"X-3;Per;Nordmann;;;",
	)
	if r := postImport(t, pd, []byte(file), "false", modtest.Context(ctx)); r.Status != 0 {
		t.Fatalf("import = %d %s, want the request cancelled", r.Status, r.Body)
	}
	deadline := time.Now().Add(10 * time.Second)
	for h.Count(t, `SELECT count(*) FROM staff.events WHERE kind = 'people_imported'`) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no people_imported event after a real run cut short")
		}
		time.Sleep(20 * time.Millisecond)
	}
	payload := modtest.One[string](t, h.Harness, `
		SELECT payload::text FROM staff.events WHERE person_id IS NULL AND kind = 'people_imported'`)
	for _, want := range []string{`"rows": 3`, `"created": 1`, `"updated": 0`, `"failed": 0`} {
		if !strings.Contains(payload, want) {
			t.Errorf("people_imported payload = %s, want %s", payload, want)
		}
	}
	if got := countPeople(t, h); got != 1 {
		t.Errorf("people after the cut-short run = %d, want the first row's", got)
	}
}

// A bank account an import changes mails the person once that row has
// committed — the harness fails any mail sent from inside a transaction —
// and a dry run mails nobody; the same account again is no change and no
// mail.
func TestImport_BankChangeMailPerRowAfterCommit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	pd, _ := signIn(t, h, "staff:manage", "staff:personal-data")
	file := csvOf(
		"employeeNumber;givenName;familyName;privateEmail;bankAccountKind;bankAccount",
		"M-1;Kari;Nordmann;kari@privat.no;bban;12345678903",
		"M-2;Ola;Nordmann;ola@privat.no;bban;1234.56.78903",
		"M-3;Per;Nordmann;per@privat.no;;",
	)
	importCSV(t, pd, file, true)
	if sent := h.mail.messages(); len(sent) != 0 {
		t.Fatalf("a dry run mailed %d people", len(sent))
	}
	importCSV(t, pd, file, false)
	sent := h.mail.messages()
	if len(sent) != 2 || sent[0].To[0] != "kari@privat.no" || sent[1].To[0] != "ola@privat.no" {
		t.Fatalf("mails = %+v, want Kari's and Ola's", sent)
	}
	if !strings.Contains(sent[0].TextBody, "8903") || carriesMoreThanLast4(sent[0].TextBody, "12345678903") {
		t.Errorf("mail = %q, want the last four digits alone", sent[0].TextBody)
	}
	importCSV(t, pd, file, false)
	if n := len(h.mail.messages()); n != 2 {
		t.Errorf("mails after the same file again = %d, want still 2", n)
	}
}
