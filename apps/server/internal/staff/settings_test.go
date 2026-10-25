package staff_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

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
	body := map[string]any{"nextEmployeeNumber": 1}
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
		"employeeNumberPrefix": "EMP-", "nextEmployeeNumber": 100,
		"pensionProvider": "Storebrand", "injuryInsurer": "Gjensidige",
	}), http.StatusOK, &saved, ifMatch(1))
	if saved.TimeZone != "Europe/Oslo" || saved.EmployeeNumberPrefix != "EMP-" || saved.NextEmployeeNumber != 100 ||
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

// The business time zone is the workspace's (time zones design D2): the
// settings and meta answer it, a replace neither takes nor touches it, and
// "today" follows it — half past midnight on 1 July in Oslo is still 30 June
// in New York.
func TestSettings_TheZoneIsTheWorkspaces(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")

	if got := getSettings(t, hr); got.TimeZone != "Europe/Oslo" {
		t.Errorf("a fresh installation's zone = %q, want the workspace's default", got.TimeZone)
	}
	h.SetBusinessTimeZone(t, "America/New_York")
	if got := getSettings(t, hr); got.TimeZone != "America/New_York" || got.Revision != 1 {
		t.Errorf("after the workspace changed = %+v, want America/New_York at revision 1", got)
	}
	var saved gen.StaffSettings
	do(t, hr, http.MethodPut, settingsPath, settingsBody(map[string]any{"timeZone": "Europe/Oslo"}), http.StatusOK, &saved, ifMatch(1))
	if saved.TimeZone != "America/New_York" {
		t.Errorf("a replace naming a zone answered %q, want the workspace's kept", saved.TimeZone)
	}
	var meta gen.StaffMeta
	do(t, hr, http.MethodGet, metaPath, nil, http.StatusOK, &meta)
	if meta.TimeZone != "America/New_York" || meta.Today.Format("2006-01-02") != civilDay(t, h, "America/New_York") {
		t.Errorf("meta = %s today %s, want America/New_York and its day", meta.TimeZone, meta.Today.Format("2006-01-02"))
	}
}

// civilDay is the harness clock's calendar day in zone, as yyyy-MM-dd.
func civilDay(t *testing.T, h *harness, zone string) string {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	return h.Now().In(loc).Format("2006-01-02")
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
		{"competenceReviewIntervalMonths", 61},
		{"competenceReviewIntervalMonths", -1},
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
	// A null is no zero: the required nextEmployeeNumber null is refused.
	nullNext := settingsBody(nil)
	nullNext["nextEmployeeNumber"] = nil
	refused(t, hr.Do(http.MethodPut, settingsPath, nullNext, ifMatch(1)), http.StatusBadRequest, "")
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

// The competence review interval (staff competence design §3.9, S11): 0 to
// 60 months, 12 by default; 0 turns the overdue item off; left out, it keeps
// its value.
func TestSettings_TheCompetenceReviewInterval(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	if fresh := raw(t, hr.Do(http.MethodGet, settingsPath, nil)); fresh["competenceReviewIntervalMonths"] != float64(12) {
		t.Errorf("competenceReviewIntervalMonths = %v, want 12", fresh["competenceReviewIntervalMonths"])
	}
	for _, v := range []int{-1, 61} {
		pr := refused(t, hr.Do(http.MethodPut, settingsPath, settingsBody(map[string]any{"competenceReviewIntervalMonths": v}), ifMatch(1)),
			http.StatusBadRequest, "")
		if len(pr.Errors["competenceReviewIntervalMonths"]) == 0 || len(pr.Errors) != 1 {
			t.Errorf("%d: errors = %v, want exactly competenceReviewIntervalMonths named", v, pr.Errors)
		}
	}
	var saved gen.StaffSettings
	do(t, hr, http.MethodPut, settingsPath, settingsBody(map[string]any{"competenceReviewIntervalMonths": 0}), http.StatusOK, &saved, ifMatch(1))
	if saved.CompetenceReviewIntervalMonths != 0 {
		t.Errorf("saved %d, want 0", saved.CompetenceReviewIntervalMonths)
	}
	do(t, hr, http.MethodPut, settingsPath, settingsBody(map[string]any{"competenceReviewIntervalMonths": 60}), http.StatusOK, &saved, ifMatch(saved.Revision))
	do(t, hr, http.MethodPut, settingsPath, settingsBody(nil), http.StatusOK, &saved, ifMatch(saved.Revision))
	if saved.CompetenceReviewIntervalMonths != 60 {
		t.Errorf("left out: %d, want 60 kept", saved.CompetenceReviewIntervalMonths)
	}
}

// The two erase settings the owner asked for (delivery C, Task 5b; B's open
// questions 11 and 12): erasePregnancyHealthTypes and eraseSickCaseDates,
// staff:manage's, false by default. Each keeps its value when a replace
// leaves it out, as the other settings a form may not edit do; a null is
// refused on the field — never read as false, which would turn the erase
// off unasked — and so is a value that is no boolean.
func TestSettings_TheEraseSettings(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	fresh := raw(t, hr.Do(http.MethodGet, settingsPath, nil))
	for _, key := range []string{"erasePregnancyHealthTypes", "eraseSickCaseDates"} {
		if fresh[key] != false {
			t.Errorf("a fresh installation's %s = %v, want false", key, fresh[key])
		}
	}
	saved := raw(t, hr.Do(http.MethodPut, settingsPath, settingsBody(map[string]any{"erasePregnancyHealthTypes": true}), ifMatch(1)))
	if saved["erasePregnancyHealthTypes"] != true || saved["eraseSickCaseDates"] != false {
		t.Errorf("saved %v / %v, want true / false", saved["erasePregnancyHealthTypes"], saved["eraseSickCaseDates"])
	}
	saved = raw(t, hr.Do(http.MethodPut, settingsPath, settingsBody(map[string]any{"eraseSickCaseDates": true}), ifMatch(2)))
	if saved["erasePregnancyHealthTypes"] != true || saved["eraseSickCaseDates"] != true {
		t.Errorf("left out, erasePregnancyHealthTypes = %v (want kept, true); eraseSickCaseDates = %v (want true)",
			saved["erasePregnancyHealthTypes"], saved["eraseSickCaseDates"])
	}
	saved = raw(t, hr.Do(http.MethodPut, settingsPath, settingsBody(nil), ifMatch(3)))
	if saved["erasePregnancyHealthTypes"] != true || saved["eraseSickCaseDates"] != true || saved["revision"] != float64(4) {
		t.Errorf("both left out: %v / %v at %v, want both kept at revision 4", saved["erasePregnancyHealthTypes"], saved["eraseSickCaseDates"], saved["revision"])
	}
	for _, key := range []string{"erasePregnancyHealthTypes", "eraseSickCaseDates"} {
		for _, bad := range []any{nil, "yes"} {
			body := settingsBody(nil)
			body[key] = bad
			r := hr.Do(http.MethodPut, settingsPath, body, ifMatch(4))
			if r.Status != http.StatusBadRequest {
				t.Errorf("%s = %v: %d %s, want 400", key, bad, r.Status, r.Body)
				continue
			}
			if bad == nil {
				if pr := refused(t, r, http.StatusBadRequest, ""); len(pr.Errors[key]) == 0 {
					t.Errorf("%s null: errors = %v, want %s named", key, pr.Errors, key)
				}
			}
		}
	}
	saved = raw(t, hr.Do(http.MethodPut, settingsPath, settingsBody(map[string]any{"erasePregnancyHealthTypes": false, "eraseSickCaseDates": false}), ifMatch(4)))
	if saved["erasePregnancyHealthTypes"] != false || saved["eraseSickCaseDates"] != false {
		t.Errorf("turned off: %v / %v, want false / false", saved["erasePregnancyHealthTypes"], saved["eraseSickCaseDates"])
	}
	// staff:view reads neither: the settings are staff:manage's.
	viewer, _ := signIn(t, h, "staff:view")
	if r := viewer.Do(http.MethodGet, settingsPath, nil); r.Status != http.StatusForbidden {
		t.Errorf("staff:view reading the settings: %d, want 403", r.Status)
	}
}

// Turning an erase setting on or off is recorded (review L4): an
// installation event settings_updated naming the field, from and to, with
// the caller as its actor; a replace that changes neither records none.
func TestSettings_AnEraseSwitchIsRecorded(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, hrID := signIn(t, h, "staff:manage")
	do(t, hr, http.MethodPut, settingsPath, settingsBody(map[string]any{"erasePregnancyHealthTypes": true}), http.StatusOK, nil, ifMatch(1))
	do(t, hr, http.MethodPut, settingsPath, settingsBody(nil), http.StatusOK, nil, ifMatch(2))
	do(t, hr, http.MethodPut, settingsPath, settingsBody(map[string]any{"erasePregnancyHealthTypes": false, "eraseSickCaseDates": true}), http.StatusOK, nil, ifMatch(3))
	got := modtest.One[[]string](t, h.Harness, `SELECT coalesce(array_agg(payload->'fields' ORDER BY id)::text[], '{}') FROM staff.events
		WHERE person_id IS NULL AND kind = 'settings_updated' AND actor_user_id = $1`, hrID)
	want := []string{
		`{"erasePregnancyHealthTypes": {"to": true, "from": false}}`,
		`{"eraseSickCaseDates": {"to": true, "from": false}, "erasePregnancyHealthTypes": {"to": false, "from": true}}`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("settings_updated events = %v, want %v", got, want)
	}
}

// The default account kind (Entra design D5): local on a fresh installation
// whose identity is local, kept when a replace leaves it out, refused
// provider_missing on the field for a kind no enabled module offers, and the
// kind a new person takes (InsertPerson, which POST /people and the import
// share) while people already registered keep theirs.
func TestSettings_TheDefaultAccountKind(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	fresh := getSettings(t, hr)
	if fresh.DefaultAccountKind != "local" {
		t.Errorf("a fresh installation's default kind = %v, want local", fresh.DefaultAccountKind)
	}
	before := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	for _, kind := range []string{"entra", "okta"} {
		pr := refused(t, hr.Do(http.MethodPut, settingsPath, settingsBody(map[string]any{"defaultAccountKind": kind}), ifMatch(fresh.Revision)),
			http.StatusBadRequest, "provider_missing")
		if len(pr.Errors["defaultAccountKind"]) != 1 {
			t.Errorf("%s: errors = %v, want one on defaultAccountKind", kind, pr.Errors)
		}
	}
	var saved gen.StaffSettings
	do(t, hr, http.MethodPut, settingsPath, settingsBody(map[string]any{"defaultAccountKind": "external"}), http.StatusOK, &saved, ifMatch(fresh.Revision))
	do(t, hr, http.MethodPut, settingsPath, settingsBody(nil), http.StatusOK, &saved, ifMatch(saved.Revision))
	if saved.DefaultAccountKind != "external" {
		t.Errorf("left out: default kind = %v, want external kept", saved.DefaultAccountKind)
	}
	after := createPerson(t, hr, newPerson("Ola", "Hansen"))
	if got := getAccount(t, hr, after.Id).AccountKind; got != "external" {
		t.Errorf("a person created after: kind %v, want the default, external", got)
	}
	if got := getAccount(t, hr, before.Id).AccountKind; got != "local" {
		t.Errorf("a person created before: kind %v, want local kept", got)
	}

	withProvider := newHarness(t, modtest.WithIdentityProvisioners(entraProvider()))
	hr2, _ := signIn(t, withProvider, "staff:manage")
	s2 := getSettings(t, hr2)
	do(t, hr2, http.MethodPut, settingsPath, settingsBody(map[string]any{"defaultAccountKind": "entra"}), http.StatusOK, &s2, ifMatch(s2.Revision))
	if s2.DefaultAccountKind != "entra" {
		t.Errorf("with the provider: default kind = %v, want entra", s2.DefaultAccountKind)
	}
	if got := getAccount(t, hr2, createPerson(t, hr2, newPerson("Per", "Hansen")).Id).AccountKind; got != "entra" {
		t.Errorf("a new person under the entra default: kind %v, want entra", got)
	}
}

// The directory profile's switches (Entra design D8): the twelve switchable
// rows with their defaults; a replace merges the keys it names over the
// stored ones and keeps the rest, and keeps all when it names none; a key
// that is not a switchable row — unknown, or one always written — is a field
// error on directoryFields.<key>, and nothing is saved.
func TestSettings_TheDirectoryFields(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	fresh := getSettings(t, hr)
	defaults := map[string]bool{
		"employeeHireDate": true, "employeeLeaveDateTime": true, "employeeType": true, "jobTitle": true,
		"department": true, "companyName": true, "employeeOrgData": true, "officeLocation": true,
		"workAddress": false, "phones": false, "preferredLanguage": true, "manager": true,
	}
	if len(fresh.DirectoryFields) != len(defaults) {
		t.Errorf("a fresh installation's switches = %v, want the twelve defaults", fresh.DirectoryFields)
	}
	for k, v := range defaults {
		if fresh.DirectoryFields[k] != v {
			t.Errorf("default %s = %v, want %v", k, fresh.DirectoryFields[k], v)
		}
	}

	for _, key := range []string{"bogus", "givenName", "userPrincipalName"} {
		pr := refused(t, hr.Do(http.MethodPut, settingsPath, settingsBody(map[string]any{"directoryFields": map[string]bool{key: false, "phones": true}}),
			ifMatch(fresh.Revision)), http.StatusBadRequest, "")
		if len(pr.Errors["directoryFields."+key]) != 1 {
			t.Errorf("%s: errors = %v, want one on directoryFields.%s", key, pr.Errors, key)
		}
	}
	if got := getSettings(t, hr); got.Revision != fresh.Revision || got.DirectoryFields["phones"] {
		t.Errorf("after the refusals: revision %d, phones %v; want nothing saved", got.Revision, got.DirectoryFields["phones"])
	}

	var saved gen.StaffSettings
	do(t, hr, http.MethodPut, settingsPath, settingsBody(map[string]any{"directoryFields": map[string]bool{"phones": true, "manager": false}}),
		http.StatusOK, &saved, ifMatch(fresh.Revision))
	do(t, hr, http.MethodPut, settingsPath, settingsBody(map[string]any{"directoryFields": map[string]bool{"jobTitle": false}}),
		http.StatusOK, &saved, ifMatch(saved.Revision))
	do(t, hr, http.MethodPut, settingsPath, settingsBody(nil), http.StatusOK, &saved, ifMatch(saved.Revision))
	want := map[string]bool{}
	for k, v := range defaults {
		want[k] = v
	}
	want["phones"], want["manager"], want["jobTitle"] = true, false, false
	for k, v := range want {
		if saved.DirectoryFields[k] != v {
			t.Errorf("after the merges: %s = %v, want %v", k, saved.DirectoryFields[k], v)
		}
	}
}
