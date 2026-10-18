package expenses_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/expenses"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

func TestExpensesSettings_RoundTripAndReadableByEveryAccessHolder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	employee, _ := signIn(t, h)

	if got := getSettings(t, employee); got.DefaultCurrency != "NOK" {
		t.Errorf("a fresh installation: settings = %+v, want NOK", got)
	}

	saved := putSettings(t, admin, settingsBody(map[string]any{
		"defaultCurrency":      "eur",
		"defaultMarkupPercent": 7.25,
		"lockedBefore":         "2026-09-10",
		"receiptRequiredOver":  1000.5,
	}))
	if saved.DefaultCurrency != "EUR" {
		t.Errorf("defaultCurrency = %q, want it upper-cased to EUR", saved.DefaultCurrency)
	}
	if saved.DefaultMarkupPercent == nil || *saved.DefaultMarkupPercent != 7.25 {
		t.Errorf("defaultMarkupPercent = %v, want 7.25", saved.DefaultMarkupPercent)
	}
	if saved.LockedBefore == nil || *saved.LockedBefore != "2026-09-10" {
		t.Errorf("lockedBefore = %v, want 2026-09-10", saved.LockedBefore)
	}
	if saved.ReceiptRequiredOver == nil || *saved.ReceiptRequiredOver != 1000.5 {
		t.Errorf("receiptRequiredOver = %v, want 1000.5", saved.ReceiptRequiredOver)
	}

	// Everyone who may use the app reads them: the lock and the receipt rule
	// decide what they may record, and the client shows both.
	got := getSettings(t, employee)
	if got.DefaultCurrency != saved.DefaultCurrency ||
		got.LockedBefore == nil || *got.LockedBefore != *saved.LockedBefore ||
		got.ReceiptRequiredOver == nil || *got.ReceiptRequiredOver != *saved.ReceiptRequiredOver {
		t.Errorf("an employee reads %+v, want the same settings the administrator saved, %+v", got, saved)
	}
}

// The default markup is a commercial figure — what the company adds to a
// supplier cost before it invoices it on — and design §5 keeps that side of an
// expense for financial rights. It is expenses:manage's in both reads that
// carry it, and neither read needs it for the expense form: the server applies
// the default itself when a billable outlay names no markup.
func TestExpensesSettings_TheDefaultMarkupIsAnAdministratorsToRead(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	employee, _ := signIn(t, h)
	putSettings(t, admin, settingsBody(map[string]any{"defaultMarkupPercent": 12.5}))

	if got := getSettings(t, admin); got.DefaultMarkupPercent == nil || *got.DefaultMarkupPercent != 12.5 {
		t.Errorf("an administrator reads defaultMarkupPercent %v, want 12.5", got.DefaultMarkupPercent)
	}
	if got := getSettings(t, employee); got.DefaultMarkupPercent != nil {
		t.Errorf("an employee reads defaultMarkupPercent %v, want none", got.DefaultMarkupPercent)
	}
	for name, raw := range map[string]map[string]any{
		"the settings": rawSettings(t, employee),
		"meta":         rawMeta(t, employee),
	} {
		if _, ok := raw["defaultMarkupPercent"]; ok {
			t.Errorf("%s = %v, want no defaultMarkupPercent key at all for an employee", name, raw)
		}
	}
	if meta := getMeta(t, admin); meta.DefaultMarkupPercent == nil || *meta.DefaultMarkupPercent != 12.5 {
		t.Errorf("an administrator's meta carries defaultMarkupPercent %v, want 12.5", meta.DefaultMarkupPercent)
	}
}

// A receipt threshold of zero is a policy, not a mistake: every employee-paid
// outlay then needs a receipt. Clearing the setting is what turns the rule off.
func TestExpensesSettings_AThresholdOfZeroMeansAlwaysRequireAReceipt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")

	saved := putSettings(t, admin, settingsBody(map[string]any{"receiptRequiredOver": 0}))
	if saved.ReceiptRequiredOver == nil || *saved.ReceiptRequiredOver != 0 {
		t.Errorf("receiptRequiredOver = %v, want zero kept as a value", saved.ReceiptRequiredOver)
	}
	if raw := rawSettings(t, admin); raw["receiptRequiredOver"] != float64(0) {
		t.Errorf("settings = %v, want receiptRequiredOver present and zero", raw)
	}
}

// The optional settings are absent, never null, and a full replace that leaves
// one out clears it (the module's shaping rule).
func TestExpensesSettings_TheOptionalOnesAreAbsentNotNullAndAreClearedByOmission(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")

	raw := rawSettings(t, admin)
	if _, ok := raw["lockedBefore"]; ok {
		t.Errorf("settings = %v, want no lockedBefore key at all when no lock is set", raw)
	}
	if _, ok := raw["receiptRequiredOver"]; ok {
		t.Errorf("settings = %v, want no receiptRequiredOver key at all when no threshold is set", raw)
	}

	putSettings(t, admin, settingsBody(map[string]any{"lockedBefore": "2026-09-10", "receiptRequiredOver": 250}))
	putSettings(t, admin, settingsBody(nil))
	raw = rawSettings(t, admin)
	if _, ok := raw["lockedBefore"]; ok {
		t.Errorf("settings = %v, want the lock lifted by a replace that leaves it out", raw)
	}
	if _, ok := raw["receiptRequiredOver"]; ok {
		t.Errorf("settings = %v, want the threshold cleared by a replace that leaves it out", raw)
	}
}

func TestExpensesSettings_RefusesAFieldItCannotStore(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")

	for name, tc := range map[string]struct {
		overrides map[string]any
		field     string
	}{
		"no currency":              {map[string]any{"defaultCurrency": ""}, "defaultCurrency"},
		"a currency of two":        {map[string]any{"defaultCurrency": "NO"}, "defaultCurrency"},
		"a negative markup":        {map[string]any{"defaultMarkupPercent": -1}, "defaultMarkupPercent"},
		"a markup over 1000":       {map[string]any{"defaultMarkupPercent": 1000.01}, "defaultMarkupPercent"},
		"a markup of three":        {map[string]any{"defaultMarkupPercent": 1.234}, "defaultMarkupPercent"},
		"a negative threshold":     {map[string]any{"receiptRequiredOver": -5}, "receiptRequiredOver"},
		"a threshold of three":     {map[string]any{"receiptRequiredOver": 10.001}, "receiptRequiredOver"},
		"a threshold that is huge": {map[string]any{"receiptRequiredOver": 10_000_000_000.0}, "receiptRequiredOver"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			errs := refused(t, admin, http.MethodPut, settingsPath, settingsBody(tc.overrides), "Invalid expense settings")
			if len(errs[tc.field]) == 0 {
				t.Errorf("errors = %v, want one on %s", errs, tc.field)
			}
		})
	}
}

// A caller who may use the app but not administer it may read the settings and
// may not change them.
func TestExpensesSettings_ChangingThemNeedsManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employee, _ := signIn(t, h)

	r := employee.Do(http.MethodPut, settingsPath, settingsBody(nil))
	if r.Status != http.StatusForbidden {
		t.Errorf("an employee changing the settings: status %d body %s, want 403", r.Status, r.Body)
	}
}

// The business time zone is the workspace's (time zones design D2): everyone
// reads it on the settings — a client that labels a trip's days has to label
// them in it and not in the browser's — a replace neither takes nor touches
// it, and an Owner's change under Workspace administration is what moves it.
func TestExpensesSettings_TheBusinessTimeZoneIsTheWorkspaces(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	employee, _ := signIn(t, h)

	if zone := getSettings(t, employee).TimeZone; zone != "Europe/Oslo" {
		t.Errorf("time zone = %q, want the workspace's default", zone)
	}
	h.SetBusinessTimeZone(t, "America/New_York")
	if zone := getSettings(t, employee).TimeZone; zone != "America/New_York" {
		t.Errorf("time zone after the workspace changed = %q, want America/New_York", zone)
	}
	if zone := putSettings(t, admin, settingsBody(map[string]any{"timeZone": "Europe/Oslo"})).TimeZone; zone != "America/New_York" {
		t.Errorf("time zone after a replace naming one = %q, want the workspace's kept", zone)
	}
}

// The property the zone exists for, over the zones the workspace accepts:
// the day Go derives from an instant and the day Postgres derives from the
// same instant are the same day. It is the sentence docs/src/content/docs/en/reference/expenses.md states as absolute, so
// it is a test rather than an assertion — instants either side of local
// midnight, in January and in July, so a zone the two disagree about only in
// summer cannot slip through.
func TestExpensesSettings_GoAndPostgresNameTheSameDay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	for _, name := range []string{"Europe/Oslo", "UTC", "America/New_York", "Asia/Kolkata", "Pacific/Chatham"} {
		h.SetBusinessTimeZone(t, name)
		loc, err := time.LoadLocation(name)
		if err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
		for _, month := range []time.Month{time.January, time.July} {
			// Local midnight on the 15th, and the minutes either side of it:
			// the boundary every derived date turns on.
			midnight := time.Date(2026, month, 15, 0, 0, 0, 0, loc)
			for _, offset := range []time.Duration{-time.Minute, 0, time.Minute, 12 * time.Hour} {
				instant := midnight.Add(offset)
				inGo := expenses.BusinessDay(instant, loc).Format(time.DateOnly)
				inSQL := modtest.One[string](t, h.Harness,
					`SELECT (($1::timestamptz AT TIME ZONE $2)::date)::text`, instant, name)
				if inGo != inSQL {
					t.Errorf("%s at %s: Go says %s and Postgres says %s — the two must name the same day",
						name, instant.UTC().Format(time.RFC3339), inGo, inSQL)
				}
			}
		}
	}
}
