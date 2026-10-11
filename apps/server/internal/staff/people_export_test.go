package staff_test

import (
	"encoding/csv"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The register's export and the import template (design S19; plan Task 3,
// readings 9 and 10).

// personColumns are the work and private groups' columns, after the key.
var personColumns = []string{
	"employeeNumber",
	"givenName", "familyName", "displayName", "workEmail", "phoneWork", "workCountry", "preferredLanguage",
	"birthDate", "gender", "phoneMobile", "phonePrivate", "privateEmail", "street", "postalCode", "city", "country",
}

var (
	sealedColumns     = []string{"nationalIdKind", "nationalIdCountry", "nationalId", "bankAccountKind", "bankAccount"}
	employmentColumns = []string{
		"startDate", "businessUnit", "employmentType", "temporaryGround", "expectedEndDate", "ftePercent",
		"appointmentForm", "workingHoursScheme", "fullTimeHoursPerWeek", "occupationCode", "title", "department",
		"location", "managerEmployeeNumber",
	}
	exportOnlyColumns = []string{
		"status", "currentTitle", "currentDepartment", "currentBusinessUnit", "currentLocation",
		"currentManagerEmployeeNumber", "currentFtePercent", "nationalIdOnFile",
	}
)

// readExport reads a register file as a spreadsheet would: the byte order
// mark, then semicolon-separated records. It answers the rows by employee
// number, each a map from column to cell, and the header.
func readExport(t *testing.T, body []byte) ([]string, map[string]map[string]string) {
	t.Helper()
	text := string(body)
	if !strings.HasPrefix(text, utf8BOM) {
		t.Fatalf("the file does not start with a byte order mark: %q", text[:min(len(text), 20)])
	}
	if !strings.Contains(text, "\r\n") {
		t.Errorf("the file's lines do not end in CRLF")
	}
	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(text, utf8BOM)))
	r.Comma = ';'
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("read the file: %v", err)
	}
	rows := map[string]map[string]string{}
	for _, rec := range records[1:] {
		row := map[string]string{}
		for i, name := range records[0] {
			row[name] = rec[i]
		}
		rows[row["employeeNumber"]] = row
	}
	return records[0], rows
}

// The export is the work and private groups and where each person stands
// today — a cell a spreadsheet would run as a formula guarded by an
// apostrophe — never a sealed value — nationalIdOnFile yes or no, nothing of a bank
// account — served as staff-people-<today>.csv, uncached, and recorded as the
// installation's people_exported event.
func TestExport_ShapesTheRegister(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, hrID := signInNamed(t, h, "Hanne HR", "staff:manage")
	pd, _ := signIn(t, h, "staff:manage", "staff:personal-data")
	unit := company(t, hr)
	dept := createDepartment(t, hr, map[string]any{"code": "HR", "name": "Personal"})
	loc := createLocation(t, hr, "Kontoret")
	boss := createPerson(t, hr, with(newPerson("Berit", "Sjef"), "employeeNumber", "M-1"))
	createEmployment(t, hr, boss.Id, newEmployment("2020-01-01", terms()))
	kari := createPerson(t, hr, with(newPerson("Kari", "Nordmann"), "employeeNumber", "K-1", "birthDate", "1990-01-01",
		"phoneWork", "+47 22 33 44 55", "street", `Storgata 1; "bakgården"`, "notes", "HR's own"))
	createEmployment(t, hr, kari.Id, newEmployment("2021-01-01", terms("ftePercent", 80, "title", "Rådgiver",
		"departmentId", dept.Id, "locationId", loc.Id, "managerPersonId", boss.Id)))
	createEmployment(t, hr, kari.Id, newEmployment("2022-01-01", terms("ftePercent", 20)))
	number := testNationalID("018190", 347)
	do(t, pd, http.MethodPut, nationalIDPath(kari.Id), map[string]any{"kind": "fodselsnummer", "value": number}, http.StatusOK, nil)
	do(t, pd, http.MethodPut, bankAccountPath(kari.Id), map[string]any{"kind": "bban", "value": "12345678903"}, http.StatusOK, nil)
	createPerson(t, hr, with(newPerson("Ola", "Nordmann"), "employeeNumber", "O-1"))

	r := hr.Do(http.MethodGet, peopleExportPath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("export: %d %s", r.Status, r.Body)
	}
	if got, want := r.Header("Content-Disposition"), `attachment; filename="staff-people-`+today(t, h)+`.csv"`; got != want {
		t.Errorf("Content-Disposition = %q, want %q", got, want)
	}
	if got := r.Header("Content-Type"); got != "text/csv; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := r.Header("Cache-Control"); got != "private, no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
	header, rows := readExport(t, r.Body)
	if want := slices.Concat(personColumns, exportOnlyColumns); !slices.Equal(header, want) {
		t.Errorf("header = %v, want %v", header, want)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	k := rows["K-1"]
	for column, want := range map[string]string{
		"givenName": "Kari", "birthDate": "1990-01-01", "phoneWork": "'+47 22 33 44 55", "street": `Storgata 1; "bakgården"`,
		"workCountry": "NO", "preferredLanguage": "nb", "status": "active", "currentTitle": "Rådgiver",
		"currentDepartment": "HR", "currentBusinessUnit": unit.Name, "currentLocation": "Kontoret",
		"currentManagerEmployeeNumber": "M-1", "currentFtePercent": "100,00", "nationalIdOnFile": "yes",
	} {
		if k[column] != want {
			t.Errorf("Kari's %s = %q, want %q", column, k[column], want)
		}
	}
	if o := rows["O-1"]; o["status"] != "none" || o["nationalIdOnFile"] != "no" || o["currentFtePercent"] != "" {
		t.Errorf("Ola's row = %v, want none, no national id and no FTE", o)
	}
	if text := string(r.Body); leaks(text, number) || strings.Contains(text, "8903") || strings.Contains(text, "HR's own") {
		t.Errorf("the export carries a sealed value or HR's notes")
	}

	payload := modtest.One[string](t, h.Harness, `
		SELECT payload::text FROM staff.events WHERE person_id IS NULL AND kind = 'people_exported' AND actor_user_id = $1`, hrID)
	if !strings.Contains(payload, `"rows": 3`) {
		t.Errorf("people_exported payload = %s, want rows 3", payload)
	}
}

// More people than STAFF_IMPORT_MAX_ROWS is export_too_many, and no event:
// nothing was exported.
func TestExport_Cap(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	h.Exec(t, `
		INSERT INTO staff.people (employee_number, given_name, family_name, display_name, created_at, updated_at)
		SELECT 'C-' || n, 'Kari', 'Nordmann', 'Kari Nordmann', $1, $1 FROM generate_series(1, 5001) AS n`, h.Now())
	refused(t, hr.Do(http.MethodGet, peopleExportPath, nil), http.StatusBadRequest, "export_too_many")
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE kind = 'people_exported'`); n != 0 {
		t.Errorf("people_exported events = %d, want none", n)
	}
	h.Exec(t, `DELETE FROM staff.people WHERE employee_number = 'C-5001'`)
	if r := hr.Do(http.MethodGet, peopleExportPath, nil); r.Status != http.StatusOK {
		t.Errorf("export at the cap = %d, want 200", r.Status)
	}
}

// A file the export wrote re-imports unchanged (reading 9): every row an
// update that changes nothing — no revision moves, no event is written — the
// guarded cells read back as they were typed, the export-only columns ignored.
// An erased person is in the export too, and their unchanged row is no
// write, so the read-only rule has nothing to refuse.
func TestExport_ReimportsUnchanged(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage", "staff:personal-data")
	seedErased(t, h, hr)
	createPerson(t, hr, with(newPerson("Kari", "Nordmann"), "birthDate", "1990-01-01", "gender", "female",
		"phoneWork", "+47 22 33 44 55", "phoneMobile", "=1+1", "street", `Storgata 1; "bakgården"`, "postalCode", "0150",
		"city", "Oslo", "country", "NO", "privateEmail", "kari@privat.no", "workEmail", "kari@example.no",
		"displayName", "Kari N.", "preferredLanguage", "en", "workCountry", "SE"))
	ola, _ := employed(t, hr, "Ola", "2024-01-01", terms("title", "-Vikar"))
	createPerson(t, hr, newPerson("Per", "O'Brien"))

	revisions := func() []int32 {
		return modtest.One[[]int32](t, h.Harness, `SELECT array_agg(revision ORDER BY id) FROM staff.people`)
	}
	before, events := revisions(), h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id IS NOT NULL`)
	r := hr.Do(http.MethodGet, peopleExportPath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("export: %d %s", r.Status, r.Body)
	}
	res := importCSV(t, hr, string(r.Body), false)
	if res.Rows != 4 || res.Updated != 4 || res.Created != 0 || res.Failed != 0 {
		t.Fatalf("re-import = %+v %v, want four unchanged updates, the erased person's among them", res, rowErrors(res))
	}
	if after := revisions(); !slices.Equal(after, before) {
		t.Errorf("revisions %v → %v, want unchanged", before, after)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id IS NOT NULL`); n != events {
		t.Errorf("person events %d → %d, want none written", events, n)
	}
	if got := getPerson(t, hr, ola.Id); got.Title == nil || *got.Title != "-Vikar" {
		t.Errorf("Ola's title = %v, want untouched", got.Title)
	}
}

// The template is the header alone: the person's columns, the first
// employment's, and the sealed ones only for a staff:personal-data holder —
// so a template sent back by the one who downloaded it is never refused for
// its header.
func TestImportTemplate_PerPermission(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	pd, _ := signIn(t, h, "staff:manage", "staff:personal-data")
	for _, c := range []struct {
		name   string
		client *modtest.Client
		want   []string
	}{
		{"manage", hr, slices.Concat(personColumns, employmentColumns)},
		{"personal-data", pd, slices.Concat(personColumns, sealedColumns, employmentColumns)},
	} {
		r := c.client.Do(http.MethodGet, importTemplatePath, nil)
		if r.Status != http.StatusOK {
			t.Fatalf("%s: template %d %s", c.name, r.Status, r.Body)
		}
		if got := r.Header("Content-Disposition"); got != `attachment; filename="staff-people-import-template.csv"` {
			t.Errorf("%s: Content-Disposition = %q", c.name, got)
		}
		header, rows := readExport(t, r.Body)
		if !slices.Equal(header, c.want) || len(rows) != 0 {
			t.Errorf("%s: template = %v and %d rows, want %v alone", c.name, header, len(rows), c.want)
		}
		if res := importCSV(t, c.client, string(r.Body), true); res.Rows != 0 {
			t.Errorf("%s: the template imported as %+v, want no rows", c.name, res)
		}
	}
}
