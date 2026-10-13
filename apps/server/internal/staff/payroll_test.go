package staff_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The payroll export behind its port (design S18; plan Task 4, readings 12
// and 13).

func payrollPath(query string) string { return "/api/v1/staff/payroll/export?" + query }

// payrollEvent is the payload of the one payroll_exported event of the
// installation, recorded with actor; it fails when there is not exactly one.
func payrollEvent(t *testing.T, h *harness, actor uuid.UUID) map[string]any {
	t.Helper()
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE kind = 'payroll_exported'`); n != 1 {
		t.Fatalf("payroll_exported events = %d, want 1", n)
	}
	raw := modtest.One[string](t, h.Harness, `
		SELECT payload::text FROM staff.events
		WHERE kind = 'payroll_exported' AND person_id IS NULL AND actor_user_id = $1`, actor)
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return out
}

// A month of three people, two more outside it, compared byte for byte with
// testdata/payroll-golden.csv: a fast monthly employee whose percentage
// changed in March and changes again in October (the row in force on the
// last day of September, 80 %), with a department code and a cost centre of
// their own; a midlertidig hourly employee ending on 15 September, whose
// cost centre is the department's; one who started on 14 September, hired
// out on a shift scheme with a seniority date and a pay grade and no
// department. The one who ended in August and the one starting in October
// are not in it. The file is served as staff-payroll-2026-09.csv, uncached;
// it holds no digit of the national identity number or the bank account
// the first of them has on file; and the export is the installation's
// payroll_exported event with the month, the format, the rows and the
// SHA-256 of the very bytes answered.
func TestPayroll_TheGoldenMonth(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, hrID := signIn(t, h, "staff:manage")
	pd, _ := signIn(t, h, "staff:manage", "staff:personal-data")
	le := createLegalEntity(t, hr, map[string]any{"name": "Kraft-Verket AS"})
	unit := createBusinessUnit(t, hr, map[string]any{"legalEntityId": le.Id, "name": "Oslo", "orgNumber": "923609016"})
	sales := createDepartment(t, hr, map[string]any{"code": "SALG", "name": "Salg", "costCentre": "4100"})
	stores := createDepartment(t, hr, map[string]any{"code": "LAGER", "name": "Lager", "costCentre": "5100"})
	pay := func(name string, amount int, period string) []any {
		return []any{map[string]any{"name": name, "amount": amount, "currency": "NOK", "period": period}}
	}
	hire := func(number, given, family, start string, first map[string]any) (int64, int64) {
		p := createPerson(t, hr, with(newPerson(given, family), "employeeNumber", number))
		e := createEmployment(t, hr, p.Id, newEmployment(start, first, "businessUnitId", unit.Id))
		return p.Id, e.Id
	}

	kari, kariJob := hire("P-1", "Kari", "Nordmann", "2024-01-01", terms("occupationCode", "2512105",
		"remunerationType", "monthly", "departmentId", sales.Id, "costCentre", "4200", "payComponents", pay("Base", 50000, "monthly")))
	addTerms(t, hr, kariJob, map[string]any{"validFrom": "2026-03-01", "ftePercent": 80, "payComponents": pay("Base", 52000, "monthly")})
	addTerms(t, hr, kariJob, map[string]any{"validFrom": "2026-10-01", "ftePercent": 60})
	number := testNationalID("018190", 347)
	do(t, pd, http.MethodPut, nationalIDPath(kari), map[string]any{"kind": "fodselsnummer", "value": number}, http.StatusOK, nil)
	do(t, pd, http.MethodPut, bankAccountPath(kari), map[string]any{"kind": "bban", "value": "12345678903"}, http.StatusOK, nil)

	ola, olaJob := hire("P-2", "Ola", "Hansen", "2026-01-01", terms("employmentType", "midlertidig", "temporaryGround", "a",
		"ftePercent", 50, "occupationCode", "9321101", "remunerationType", "hourly", "departmentId", stores.Id,
		"payComponents", pay("Timelønn", 250, "hourly")))
	do(t, hr, http.MethodPost, endPath(olaJob), map[string]any{"endDate": "2026-09-15", "endReason": "contract_ended"}, http.StatusOK, nil)

	siri, _ := hire("P-3", "Siri", "Berg", "2026-09-14", terms("appointmentForm", "fast_utleid", "hiredOutTo", "Kunde AS",
		"workingHoursScheme", "shift_365", "remunerationType", "monthly", "seniorityDate", "2020-05-01", "payGrade", "52",
		"payComponents", pay("Base", 48000, "monthly")))

	// Delivery B's absence (Task 6): Kari's foreldrepermisjon at 50 % from
	// 24 August, continued from 12 September to the month's end — one leave
	// over fourteen days, under its first row's id, its whole days; Ola's
	// 50 % sykmelding and two hours' avspasering; Siri's ten-day welfare
	// leave, counted in her leave days but too short to be a leave row.
	absenceOf := func(person int64, key, first, last string, kv ...any) int64 {
		var out struct {
			ID int64 `json:"id"`
		}
		body := with(map[string]any{"personId": person, "typeId": typeID(t, h, key), "firstDay": first, "lastDay": last}, kv...)
		do(t, hr, http.MethodPost, absencesPath, body, http.StatusCreated, &out)
		h.Exec(t, `UPDATE staff.absences SET status = 'approved' WHERE id = $1 AND status = 'requested'`, out.ID)
		return out.ID
	}
	absenceOf(kari, "foreldrepermisjon", "2026-08-24", "2026-09-11", "percent", 50)
	absenceOf(kari, "foreldrepermisjon", "2026-09-12", "2026-09-30", "percent", 50)
	absenceOf(ola, "sykmelding", "2026-09-07", "2026-09-11", "degreePercent", 50)
	absenceOf(ola, "avspasering", "2026-09-03", "2026-09-03", "hours", 2)
	absenceOf(siri, "velferdspermisjon", "2026-09-21", "2026-09-30")

	_, perJob := hire("P-4", "Per", "Ferdig", "2025-01-01", terms())
	do(t, hr, http.MethodPost, endPath(perJob), map[string]any{"endDate": "2026-08-31", "endReason": "employee_resigned"}, http.StatusOK, nil)
	hire("P-5", "Nina", "Ny", "2026-10-01", terms())

	r := hr.Do(http.MethodGet, payrollPath("month=2026-09&format=csv"), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("export: %d %s", r.Status, r.Body)
	}
	for name, want := range map[string]string{
		"Content-Type":           "text/csv; charset=utf-8",
		"Content-Disposition":    `attachment; filename="staff-payroll-2026-09.csv"`,
		"Cache-Control":          "private, no-store",
		"X-Content-Type-Options": "nosniff",
	} {
		if got := r.Header(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	golden, err := os.ReadFile("testdata/payroll-golden.csv")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(r.Body, golden) {
		t.Errorf("the file differs from testdata/payroll-golden.csv:\ngot:\n%q\nwant:\n%q", r.Body, golden)
	}
	if text := string(r.Body); leaks(text, number) || strings.Contains(text, "8903") || strings.Contains(text, "12345678903") {
		t.Errorf("the payroll file carries a sealed value")
	}

	sum := sha256.Sum256(r.Body)
	payload := payrollEvent(t, h, hrID)
	for key, want := range map[string]any{"month": "2026-09", "format": "csv", "rows": float64(4), "sha256": hex.EncodeToString(sum[:])} {
		if payload[key] != want {
			t.Errorf("payroll_exported %s = %v, want %v", key, payload[key], want)
		}
	}
	if len(payload) != 4 {
		t.Errorf("payroll_exported payload = %v, want month, format, rows and sha256 alone", payload)
	}
	if classes := modtest.One[[]string](t, h.Harness, `SELECT payload_classes FROM staff.events WHERE kind = 'payroll_exported'`); len(classes) != 0 {
		t.Errorf("payroll_exported classes = %v, want none: the event carries no personal data", classes)
	}
}

// format may be left out: it is csv.
func TestPayroll_TheFormatDefaultsToCSV(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, hrID := signIn(t, h, "staff:manage")
	r := hr.Do(http.MethodGet, payrollPath("month=2026-02"), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("export: %d %s", r.Status, r.Body)
	}
	if got := r.Header("Content-Disposition"); got != `attachment; filename="staff-payroll-2026-02.csv"` {
		t.Errorf("Content-Disposition = %q", got)
	}
	if got := payrollEvent(t, h, hrID)["format"]; got != "csv" {
		t.Errorf("format = %v, want csv", got)
	}
}

// A month nobody was employed in is the header alone, and still an export:
// an event with rows 0.
func TestPayroll_AnEmptyMonthIsTheHeaderAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, hrID := signIn(t, h, "staff:manage")
	company(t, hr)
	employed(t, hr, "Kari", "2026-01-01", terms())
	r := hr.Do(http.MethodGet, payrollPath("month=2025-12&format=csv"), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("export: %d %s", r.Status, r.Body)
	}
	golden, err := os.ReadFile("testdata/payroll-golden.csv")
	if err != nil {
		t.Fatal(err)
	}
	header := golden[:bytes.Index(golden, []byte("\r\n"))+2]
	if !bytes.Equal(r.Body, header) {
		t.Errorf("file = %q, want the header alone %q", r.Body, header)
	}
	if got := payrollEvent(t, h, hrID)["rows"]; got != float64(0) {
		t.Errorf("rows = %v, want 0", got)
	}
}

// A month that is not YYYY-MM with a month 01–12 is payroll_month_invalid —
// an empty month and no month at all alike — a format other than csv
// payroll_format_unsupported; neither is an export, so neither is recorded.
func TestPayroll_RefusesAMonthOrAFormatItCannotWrite(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	for _, query := range []string{"month=2026-1", "month=2026-13", "month=2026-00", "month=abcd", "month=", "month=2026-09-01", "format=csv"} {
		p := refused(t, hr.Do(http.MethodGet, payrollPath(query), nil), http.StatusBadRequest, "payroll_month_invalid")
		if len(p.Errors["month"]) == 0 {
			t.Errorf("%q: errors %v, want one on month", query, p.Errors)
		}
	}
	for _, format := range []string{"xlsx", "CSV", ""} {
		p := refused(t, hr.Do(http.MethodGet, payrollPath("month=2026-09&format="+format), nil), http.StatusBadRequest, "payroll_format_unsupported")
		if len(p.Errors["format"]) == 0 {
			t.Errorf("%q: errors %v, want one on format", format, p.Errors)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE kind = 'payroll_exported'`); n != 0 {
		t.Errorf("payroll_exported events = %d, want none", n)
	}
}

// The export is staff:manage's: a reader of the directory, an approver and a
// holder of staff:access alone get the uniform 403, and nothing is recorded.
func TestPayroll_ManageOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, permissions := range [][]string{{"staff:view"}, {"staff:approve"}, {}} {
		c, _ := signIn(t, h, permissions...)
		if r := c.Do(http.MethodGet, payrollPath("month=2026-09"), nil); r.Status != http.StatusForbidden {
			t.Errorf("%v: %d, want 403", permissions, r.Status)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE kind = 'payroll_exported'`); n != 0 {
		t.Errorf("payroll_exported events = %d, want none", n)
	}
}

// An erased person's past employment is still in the month it was in: who
// was employed when, the name included, is class C and outlives the erase,
// and payroll may need to report that month again.
func TestPayroll_AnErasedPersonsPastMonth(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage", "staff:personal-data")
	erased := seedErased(t, h, hr)
	r := hr.Do(http.MethodGet, payrollPath("month=2024-12"), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("export: %d %s", r.Status, r.Body)
	}
	header, rows := readPayroll(t, r.Body)
	if len(rows) != 1 {
		t.Fatalf("rows = %v, want the erased person's one", rows)
	}
	row := map[string]string{}
	for i, name := range header {
		row[name] = rows[0][i]
	}
	for column, want := range map[string]string{
		"Employee number": erased.person.EmployeeNumber, "Name": "Erik Glemt", "Employment number": *erased.employment.EmploymentNumber,
		"End": "2024-12-31", "End reason": "arbeidstakerHarSagtOppSelv",
	} {
		if row[column] != want {
			t.Errorf("%s = %q, want %q", column, row[column], want)
		}
	}
}

// readPayroll reads a payroll file as a spreadsheet would: the byte order
// mark, then semicolon-separated records, CRLF-ended.
func readPayroll(t *testing.T, body []byte) ([]string, [][]string) {
	t.Helper()
	text := string(body)
	if !strings.HasPrefix(text, utf8BOM) {
		t.Fatalf("the file does not start with a byte order mark")
	}
	var records [][]string
	for line := range strings.SplitSeq(strings.TrimSuffix(strings.TrimPrefix(text, utf8BOM), "\r\n"), "\r\n") {
		records = append(records, strings.Split(line, ";"))
	}
	return records[0], records[1:]
}
