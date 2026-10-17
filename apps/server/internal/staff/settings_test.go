package staff_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// getSettings reads the settings and fails the test unless it answered 200.
func getSettings(t *testing.T, c *modtest.Client) gen.StaffSettings {
	t.Helper()
	var out gen.StaffSettings
	do(t, c, http.MethodGet, settingsPath, nil, http.StatusOK, &out)
	return out
}

// settingsBody is a valid settings replace with fields overridden.
func settingsBody(fields map[string]any) map[string]any {
	body := map[string]any{"timeZone": "Europe/Oslo", "nextEmployeeNumber": 1}
	for k, v := range fields {
		if v == nil {
			delete(body, k)
			continue
		}
		body[k] = v
	}
	return body
}

// A fresh installation answers the migration's defaults; a replace answers
// what it stored, moves the revision on, and a second replace clears what it
// leaves out. The optional fields are absent, never null.
func TestSettings_RoundTrip(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")

	r := hr.Do(http.MethodGet, settingsPath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get settings: %d %s", r.Status, r.Body)
	}
	fresh := raw(t, r)
	if fresh["timeZone"] != "Europe/Oslo" || fresh["employeeNumberPrefix"] != "" || fresh["nextEmployeeNumber"] != float64(1) || fresh["revision"] != float64(1) {
		t.Errorf("a fresh installation = %v, want Europe/Oslo, no prefix, 1, revision 1", fresh)
	}
	for _, key := range []string{"pensionProvider", "injuryInsurer"} {
		if _, ok := fresh[key]; ok {
			t.Errorf("a fresh installation carries %s, want it absent", key)
		}
	}

	var saved gen.StaffSettings
	do(t, hr, http.MethodPut, settingsPath, settingsBody(map[string]any{
		"timeZone": "Europe/Stockholm", "employeeNumberPrefix": "EMP-", "nextEmployeeNumber": 100,
		"pensionProvider": "Storebrand", "injuryInsurer": "Gjensidige",
	}), http.StatusOK, &saved, ifMatch(1))
	if saved.TimeZone != "Europe/Stockholm" || saved.EmployeeNumberPrefix != "EMP-" || saved.NextEmployeeNumber != 100 ||
		saved.PensionProvider == nil || *saved.PensionProvider != "Storebrand" ||
		saved.InjuryInsurer == nil || *saved.InjuryInsurer != "Gjensidige" || saved.Revision != 2 {
		t.Errorf("saved = %+v, want what was sent at revision 2", saved)
	}
	if got := getSettings(t, hr); got.TimeZone != saved.TimeZone || got.EmployeeNumberPrefix != saved.EmployeeNumberPrefix ||
		got.NextEmployeeNumber != saved.NextEmployeeNumber || got.Revision != saved.Revision ||
		got.PensionProvider == nil || *got.PensionProvider != "Storebrand" {
		t.Errorf("read back %+v, want %+v", got, saved)
	}

	r = hr.Do(http.MethodPut, settingsPath, settingsBody(nil), ifMatch(2))
	if r.Status != http.StatusOK {
		t.Fatalf("replace: %d %s", r.Status, r.Body)
	}
	cleared := raw(t, r)
	for _, key := range []string{"pensionProvider", "injuryInsurer"} {
		if _, ok := cleared[key]; ok {
			t.Errorf("a replace without %s left it at %v, want it cleared and absent", key, cleared[key])
		}
	}
	if cleared["employeeNumberPrefix"] != "" {
		t.Errorf("a replace without a prefix left %v, want none", cleared["employeeNumberPrefix"])
	}
}

// The settings are HR's: staff:manage reads and writes them, and the uniform
// 403 answers anyone else, staff:view included.
func TestSettings_AreStaffManagesOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	viewer, _ := signIn(t, h, "staff:view", "staff:personal-data")

	if r := viewer.Do(http.MethodGet, settingsPath, nil); r.Status != http.StatusForbidden {
		t.Errorf("get without staff:manage: %d, want 403", r.Status)
	}
	if r := viewer.Do(http.MethodPut, settingsPath, settingsBody(nil), ifMatch(1)); r.Status != http.StatusForbidden {
		t.Errorf("put without staff:manage: %d, want 403", r.Status)
	}
}

// A replace is guarded by the revision it was read at: a stale If-Match is
// 409 revision_conflict and changes nothing, and none at all is a 400.
func TestSettings_IfMatchGuardsTheReplace(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")

	do(t, hr, http.MethodPut, settingsPath, settingsBody(map[string]any{"nextEmployeeNumber": 5}), http.StatusOK, nil, ifMatch(1))
	refused(t, hr.Do(http.MethodPut, settingsPath, settingsBody(map[string]any{"nextEmployeeNumber": 9}), ifMatch(1)),
		http.StatusConflict, "revision_conflict")
	if got := getSettings(t, hr); got.NextEmployeeNumber != 5 || got.Revision != 2 {
		t.Errorf("after a stale replace = %+v, want 5 at revision 2", got)
	}
	if r := hr.Do(http.MethodPut, settingsPath, settingsBody(nil)); r.Status != http.StatusBadRequest {
		t.Errorf("no If-Match: %d %s, want 400", r.Status, r.Body)
	}
	if r := hr.Do(http.MethodPut, settingsPath, settingsBody(nil), modtest.Header("If-Match", "abc")); r.Status != http.StatusBadRequest {
		t.Errorf("an If-Match that is no revision: %d %s, want 400", r.Status, r.Body)
	}
	do(t, hr, http.MethodPut, settingsPath, settingsBody(nil), http.StatusOK, nil, modtest.Header("If-Match", `"2"`))
}

// The zone is validated twice — by Go's tzdata and by the database's — and
// what is compared is what each means by the name: 'CET' is a zone with
// summer time to Go and a fixed +01:00 abbreviation to Postgres, so it is
// refused even though both "know" it.
func TestSettings_TheZoneIsCheckedByGoAndByTheDatabase(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")

	for _, zone := range []string{"", "Local", "Mars/Olympus_Mons", "CET"} {
		p := refused(t, hr.Do(http.MethodPut, settingsPath, settingsBody(map[string]any{"timeZone": zone}), ifMatch(1)),
			http.StatusBadRequest, "")
		if len(p.Errors["timeZone"]) == 0 {
			t.Errorf("zone %q: errors %v, want one on timeZone", zone, p.Errors)
		}
	}
	if got := getSettings(t, hr); got.TimeZone != "Europe/Oslo" || got.Revision != 1 {
		t.Errorf("after the refusals = %+v, want the zone untouched", got)
	}
	do(t, hr, http.MethodPut, settingsPath, settingsBody(map[string]any{"timeZone": "America/New_York"}), http.StatusOK, nil, ifMatch(1))
}

// The prefix and the next number must make an employee number of at most 16
// characters (design S3): an eight-character prefix leaves eight digits.
func TestSettings_ThePrefixAndTheNextNumberFitSixteenCharacters(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")

	p := refused(t, hr.Do(http.MethodPut, settingsPath,
		settingsBody(map[string]any{"employeeNumberPrefix": "ABCDEFGH", "nextEmployeeNumber": 123456789}), ifMatch(1)),
		http.StatusBadRequest, "employee_number_too_long")
	if len(p.Errors["employeeNumberPrefix"]) == 0 || len(p.Errors["nextEmployeeNumber"]) == 0 {
		t.Errorf("errors = %v, want both fields named", p.Errors)
	}
	var saved gen.StaffSettings
	do(t, hr, http.MethodPut, settingsPath,
		settingsBody(map[string]any{"employeeNumberPrefix": "ABCDEFGH", "nextEmployeeNumber": 12345678}), http.StatusOK, &saved, ifMatch(1))
	if saved.EmployeeNumberPrefix != "ABCDEFGH" || saved.NextEmployeeNumber != 12345678 {
		t.Errorf("saved = %+v, want the sixteen-character number accepted", saved)
	}
}

// Every field is judged on its own, each bad one a 400 naming it.
func TestSettings_EachBadFieldIsA400OnThatField(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	long := strings.Repeat("x", 201)

	for _, c := range []struct {
		field string
		value any
	}{
		{"employeeNumberPrefix", "EMP 1"},
		{"employeeNumberPrefix", "TOOLONGPX"},
		{"employeeNumberPrefix", "ÆØÅ"},
		{"nextEmployeeNumber", 0},
		{"nextEmployeeNumber", -4},
		{"pensionProvider", long},
		{"pensionProvider", "   "},
		{"injuryInsurer", long},
		{"timeZone", "Europe/Nowhere"},
	} {
		p := refused(t, hr.Do(http.MethodPut, settingsPath, settingsBody(map[string]any{c.field: c.value}), ifMatch(1)),
			http.StatusBadRequest, "")
		if len(p.Errors[c.field]) == 0 || len(p.Errors) != 1 {
			t.Errorf("%s = %v: errors %v, want exactly one field named %s", c.field, c.value, p.Errors, c.field)
		}
	}
	if got := getSettings(t, hr); got.Revision != 1 {
		t.Errorf("after the refusals revision = %d, want nothing saved", got.Revision)
	}
}

// The two account settings (delivery C): createBeforeDays 0 to 90, 14 by
// default; deleteExternalAfterDays 1 to 3650 or null for never, absent by
// default. Each keeps its value when a replace leaves it out (plan M8); a
// null deleteExternalAfterDays clears it.
func TestSettings_TheAccountSettings(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	fresh := raw(t, hr.Do(http.MethodGet, settingsPath, nil))
	if fresh["createBeforeDays"] != float64(14) {
		t.Errorf("createBeforeDays = %v, want 14", fresh["createBeforeDays"])
	}
	if _, ok := fresh["deleteExternalAfterDays"]; ok {
		t.Errorf("deleteExternalAfterDays = %v, want absent", fresh["deleteExternalAfterDays"])
	}
	for _, body := range []map[string]any{
		{"createBeforeDays": -1}, {"createBeforeDays": 91},
		{"deleteExternalAfterDays": 0}, {"deleteExternalAfterDays": 3651},
	} {
		pr := refused(t, hr.Do(http.MethodPut, settingsPath, settingsBody(body), ifMatch(1)), http.StatusBadRequest, "")
		for field := range body {
			if len(pr.Errors[field]) == 0 {
				t.Errorf("%v: errors = %v, want %s named", body, pr.Errors, field)
			}
		}
	}
	var saved gen.StaffSettings
	do(t, hr, http.MethodPut, settingsPath, settingsBody(map[string]any{"createBeforeDays": 0, "deleteExternalAfterDays": 3650}), http.StatusOK, &saved, ifMatch(1))
	if saved.CreateBeforeDays != 0 || saved.DeleteExternalAfterDays == nil || *saved.DeleteExternalAfterDays != 3650 {
		t.Errorf("saved %d / %v, want 0 / 3650", saved.CreateBeforeDays, saved.DeleteExternalAfterDays)
	}
	do(t, hr, http.MethodPut, settingsPath, settingsBody(nil), http.StatusOK, &saved, ifMatch(saved.Revision))
	if saved.CreateBeforeDays != 0 || saved.DeleteExternalAfterDays == nil || *saved.DeleteExternalAfterDays != 3650 {
		t.Errorf("left out: %d / %v, want both kept, 0 / 3650", saved.CreateBeforeDays, saved.DeleteExternalAfterDays)
	}
	do(t, hr, http.MethodPut, settingsPath, settingsBody(map[string]any{"createBeforeDays": 90}), http.StatusOK, &saved, ifMatch(saved.Revision))
	body := settingsBody(nil)
	body["deleteExternalAfterDays"] = nil
	var cleared gen.StaffSettings
	do(t, hr, http.MethodPut, settingsPath, body, http.StatusOK, &cleared, ifMatch(saved.Revision))
	if cleared.CreateBeforeDays != 90 || cleared.DeleteExternalAfterDays != nil {
		t.Errorf("a null deleteExternalAfterDays: %d / %v, want 90 kept and it cleared", cleared.CreateBeforeDays, cleared.DeleteExternalAfterDays)
	}
}
