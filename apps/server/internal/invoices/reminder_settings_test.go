package invoices_test

import (
	"context"
	"maps"
	"math/big"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/reminderrules"
)

const reminderSettingsPath = "/api/v1/invoices/settings/reminders"

// reminderSettingsJSON is the reminder settings as a client reads them.
type reminderSettingsJSON struct {
	Enabled               bool      `json:"enabled"`
	FirstReminderDays     int32     `json:"firstReminderDays"`
	DeadlineDays          int32     `json:"deadlineDays"`
	GraceDays             int32     `json:"graceDays"`
	RemindersBeforeNotice int32     `json:"remindersBeforeNotice"`
	CollectionNotice      bool      `json:"collectionNotice"`
	PersonCharge          string    `json:"personCharge"`
	BusinessCharge        string    `json:"businessCharge"`
	LateInterest          bool      `json:"lateInterest"`
	StaleImportDays       int32     `json:"staleImportDays"`
	Inkassolov2026From    *string   `json:"inkassolov2026From"`
	RegimeReviewedThrough string    `json:"regimeReviewedThrough"`
	RegimeReviewedBy      *string   `json:"regimeReviewedBy"`
	RegimeReviewedAt      time.Time `json:"regimeReviewedAt"`
	Revision              int32     `json:"revision"`
	UpdatedAt             time.Time `json:"updatedAt"`
	UpdatedBy             *string   `json:"updatedBy"`
}

// reminderSettingsBody is a complete PUT body: the defaults but enabled, at
// revision.
func reminderSettingsBody(revision int32) map[string]any {
	return map[string]any{
		"enabled": true, "firstReminderDays": 14, "deadlineDays": 14, "graceDays": 3, "remindersBeforeNotice": 1,
		"collectionNotice": true, "personCharge": "fee", "businessCharge": "fee", "lateInterest": false,
		"staleImportDays": 3, "inkassolov2026From": nil, "regimeReviewedThrough": "2026-12-31", "revision": revision,
	}
}

// with is body with one field set, or removed when value is absent.
func with(body map[string]any, field string, value any) map[string]any {
	out := maps.Clone(body)
	if value == absent {
		delete(out, field)
	} else {
		out[field] = value
	}
	return out
}

// absent marks a field left out of a body.
var absent = &struct{}{}

// readReminderSettings reads them as an invoices:access holder.
func readReminderSettings(t *testing.T, h *harness) reminderSettingsJSON {
	t.Helper()
	res := h.SignIn(t, "invoices:access").Do(http.MethodGet, reminderSettingsPath, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /settings/reminders = %d %s", res.Status, res.Body)
	}
	var s reminderSettingsJSON
	res.JSON(&s)
	return s
}

// The reminder settings (D6, D7): the migration's defaults, the review
// through 2026-12-31 and nobody's; every field required and bounded — a 400
// on the field, nothing written; a stale revision a 409; invoices:access
// reads and only invoices:manage writes; a write records who and when, and
// the review's who and when move only with the regime's two fields.
func TestReminderSettings_DefaultsBoundsRevisionPermission(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	start := h.Now()

	got := readReminderSettings(t, h)
	if got.Enabled || got.FirstReminderDays != 14 || got.DeadlineDays != 14 || got.GraceDays != 3 ||
		got.RemindersBeforeNotice != 1 || !got.CollectionNotice || got.PersonCharge != "fee" || got.BusinessCharge != "fee" ||
		got.LateInterest || got.StaleImportDays != 3 || got.Inkassolov2026From != nil ||
		got.RegimeReviewedThrough != "2026-12-31" || got.RegimeReviewedBy != nil || got.Revision != 1 || got.UpdatedBy != nil {
		t.Fatalf("the defaults = %+v", got)
	}

	for _, perms := range [][]string{{"invoices:access"}, {"invoices:access", "invoices:payments"}, {"invoices:access", "invoices:issue"}} {
		if res := h.SignIn(t, perms...).Do(http.MethodPut, reminderSettingsPath, reminderSettingsBody(1)); res.Status != http.StatusForbidden {
			t.Errorf("PUT as %v = %d, want 403", perms, res.Status)
		}
	}

	base := reminderSettingsBody(1)
	limit := "2027-09-12" // a year after 2026-09-12 (Oslo)
	for _, c := range []struct {
		field string
		value any
	}{
		{"enabled", absent}, {"enabled", nil}, {"enabled", "yes"},
		{"firstReminderDays", absent}, {"firstReminderDays", 0}, {"firstReminderDays", 61},
		{"deadlineDays", absent}, {"deadlineDays", 13}, {"deadlineDays", 61},
		{"graceDays", absent}, {"graceDays", 0}, {"graceDays", 11},
		{"remindersBeforeNotice", absent}, {"remindersBeforeNotice", -1}, {"remindersBeforeNotice", 3}, {"remindersBeforeNotice", 1.5},
		{"collectionNotice", absent}, {"collectionNotice", nil},
		{"personCharge", absent}, {"personCharge", "compensation"}, {"personCharge", ""},
		{"businessCharge", absent}, {"businessCharge", "both"},
		{"lateInterest", absent}, {"lateInterest", 1},
		{"staleImportDays", absent}, {"staleImportDays", 0}, {"staleImportDays", 31},
		{"inkassolov2026From", absent}, {"inkassolov2026From", "1. januar"},
		{"regimeReviewedThrough", absent}, {"regimeReviewedThrough", nil}, {"regimeReviewedThrough", "2027-09-13"},
		{"revision", absent}, {"revision", nil}, {"revision", "1"},
	} {
		res := manager(t, h).Do(http.MethodPut, reminderSettingsPath, with(base, c.field, c.value))
		if res.Status != http.StatusBadRequest {
			t.Errorf("PUT with %s = %v: %d %s, want 400", c.field, c.value, res.Status, res.Body)
			continue
		}
		if p := problemOf(t, res); len(p.Errors[c.field]) == 0 {
			t.Errorf("PUT with %s = %v: 400 %v, want it on %s", c.field, c.value, p.Errors, c.field)
		}
	}
	if s := readReminderSettings(t, h); s.Revision != 1 {
		t.Fatalf("after the refusals the revision = %d, want 1: nothing written", s.Revision)
	}

	// Every field at an edge it allows, the review moved: the write is
	// stored, who and when with it, and the review's too.
	h.Advance(time.Hour)
	editor, who := h.SignInUser(t, "invoices:access", "invoices:manage")
	edges := map[string]any{
		"enabled": true, "firstReminderDays": 60, "deadlineDays": 60, "graceDays": 1, "remindersBeforeNotice": 0,
		"collectionNotice": false, "personCharge": "none", "businessCharge": "compensation", "lateInterest": true,
		"staleImportDays": 30, "inkassolov2026From": nil, "regimeReviewedThrough": limit, "revision": 1,
	}
	res := editor.Do(http.MethodPut, reminderSettingsPath, edges)
	if res.Status != http.StatusOK {
		t.Fatalf("PUT the edges = %d %s, want 200", res.Status, res.Body)
	}
	var saved reminderSettingsJSON
	res.JSON(&saved)
	if !saved.Enabled || saved.FirstReminderDays != 60 || saved.DeadlineDays != 60 || saved.GraceDays != 1 ||
		saved.RemindersBeforeNotice != 0 || saved.CollectionNotice || saved.PersonCharge != "none" ||
		saved.BusinessCharge != "compensation" || !saved.LateInterest || saved.StaleImportDays != 30 ||
		saved.RegimeReviewedThrough != limit || saved.Revision != 2 {
		t.Errorf("saved = %+v", saved)
	}
	if saved.UpdatedBy == nil || *saved.UpdatedBy != who.String() || !saved.UpdatedAt.Equal(h.Now()) {
		t.Errorf("updated by %v at %s, want %s at %s", saved.UpdatedBy, saved.UpdatedAt, who, h.Now())
	}
	if saved.RegimeReviewedBy == nil || *saved.RegimeReviewedBy != who.String() || !saved.RegimeReviewedAt.Equal(h.Now()) {
		t.Errorf("the review moved by %v at %s, want %s at %s", saved.RegimeReviewedBy, saved.RegimeReviewedAt, who, h.Now())
	}
	if got := readReminderSettings(t, h); !reflect.DeepEqual(got, saved) {
		t.Errorf("GET after the PUT = %+v, want %+v", got, saved)
	}

	// A write that leaves both regime fields as they are keeps the review's
	// who and when; setting the 2026 regime's day records them.
	h.Advance(time.Hour)
	reviewedAt := saved.RegimeReviewedAt
	other, otherID := h.SignInUser(t, "invoices:access", "invoices:manage")
	res = other.Do(http.MethodPut, reminderSettingsPath, with(edges, "revision", 2))
	res.JSON(&saved)
	if res.Status != http.StatusOK || saved.RegimeReviewedBy == nil || *saved.RegimeReviewedBy != who.String() ||
		!saved.RegimeReviewedAt.Equal(reviewedAt) || *saved.UpdatedBy != otherID.String() {
		t.Errorf("a write leaving the regime = %d, review by %v at %s, updated by %v; want the review kept, the update %s's",
			res.Status, saved.RegimeReviewedBy, saved.RegimeReviewedAt, saved.UpdatedBy, otherID)
	}
	h.Advance(time.Hour)
	res = other.Do(http.MethodPut, reminderSettingsPath, with(with(edges, "revision", 3), "inkassolov2026From", "2027-01-01"))
	res.JSON(&saved)
	if res.Status != http.StatusOK || saved.Inkassolov2026From == nil || *saved.Inkassolov2026From != "2027-01-01" ||
		saved.RegimeReviewedBy == nil || *saved.RegimeReviewedBy != otherID.String() || !saved.RegimeReviewedAt.Equal(h.Now()) {
		t.Errorf("setting the 2026 regime's day = %d, %v, review by %v at %s; want it recorded as %s's, now",
			res.Status, saved.Inkassolov2026From, saved.RegimeReviewedBy, saved.RegimeReviewedAt, otherID)
	}
	if reviewedAt.Equal(start) {
		t.Errorf("the first review's time = the migration's, want the PUT's")
	}

	res = manager(t, h).Do(http.MethodPut, reminderSettingsPath, with(edges, "revision", 3))
	if p := problemOf(t, res); res.Status != http.StatusConflict || p.Code != "" || p.Detail == "" {
		t.Errorf("a stale revision = %d %s, want 409 without a code, the detail naming both", res.Status, res.Body)
	}
	if s := readReminderSettings(t, h); s.Revision != 4 {
		t.Errorf("after the stale write the revision = %d, want 4", s.Revision)
	}
}

// What the engine is handed (D7, Task 7b's door): reminderSettings maps the
// row field by field, its two days as UTC midnights, and ratesOf every rate
// row of the three kinds, its day a UTC midnight and its value exact.
func TestReminderSettings_TheEngineReadsThem(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	body := map[string]any{
		"enabled": true, "firstReminderDays": 20, "deadlineDays": 21, "graceDays": 4, "remindersBeforeNotice": 2,
		"collectionNotice": false, "personCharge": "none", "businessCharge": "compensation", "lateInterest": true,
		"staleImportDays": 5, "inkassolov2026From": "2027-01-01", "regimeReviewedThrough": "2027-06-30", "revision": 1,
	}
	if res := manager(t, h).Do(http.MethodPut, reminderSettingsPath, body); res.Status != http.StatusOK {
		t.Fatalf("PUT = %d %s", res.Status, res.Body)
	}
	addedRate(t, h, rateBody("late_interest_percent", "2027-01-01", 12.35))
	settings, rates, err := invoices.EngineSettingsForTest(context.Background(), h.Pool())
	if err != nil {
		t.Fatalf("EngineSettingsForTest: %v", err)
	}
	from := time.Date(2027, time.January, 1, 0, 0, 0, 0, time.UTC)
	want := reminderrules.Settings{
		Enabled: true, FirstReminderDays: 20, DeadlineDays: 21, GraceDays: 4, RemindersBeforeNotice: 2, StaleImportDays: 5,
		CollectionNotice: false, LateInterest: true, PersonCharge: "none", BusinessCharge: "compensation",
		Inkassolov2026From: &from, RegimeReviewedThrough: time.Date(2027, time.June, 30, 0, 0, 0, 0, time.UTC),
	}
	if !reflect.DeepEqual(settings, want) {
		t.Errorf("the engine's settings = %+v, want %+v", settings, want)
	}
	if len(rates) != 15 {
		t.Fatalf("the engine's rates = %d rows, want the 14 seeds and the one added", len(rates))
	}
	r, ok := reminderrules.RateOn(rates, reminderrules.KindLateInterest, from)
	if !ok || r.Value.Cmp(big.NewRat(1235, 100)) != 0 || !r.ValidFrom.Equal(from) || r.ValidFrom.Location() != time.UTC {
		t.Errorf("the interest in force on 2027-01-01 = %+v, want the added 12.35 from a UTC midnight", r)
	}
	if r, ok := reminderrules.RateOn(rates, reminderrules.KindInkassosats, from); !ok || r.Value.Cmp(big.NewRat(750, 1)) != 0 {
		t.Errorf("the inkassosats on 2027-01-01 = %+v, want 750", r)
	}
}
