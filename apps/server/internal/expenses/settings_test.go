package expenses_test

import (
	"net/http"
	"testing"
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

// The installation's business time zone is what every date derived from a
// travel claim's two instants is taken in, so it has to be a name **both**
// halves of the system know: Go derives those dates in the handlers and
// Postgres derives the same ones in the claims list's filter, and the two carry
// their own copies of the world's time zones.
func TestExpensesSettings_TheBusinessTimeZoneMustBeOneBothHalvesKnow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	employee, _ := signIn(t, h)

	// Everyone reads it: a client that labels a trip's days has to label them
	// in this zone and not in the browser's.
	if zone := getSettings(t, employee).TimeZone; zone != "Europe/Oslo" {
		t.Errorf("time zone = %q, want the shipped default", zone)
	}

	for name, value := range map[string]any{
		"a name nobody has heard of":   "Europe/Osloo",
		"an offset rather than a zone": "+02:00",
		// "Local" is whatever zone the server process happens to run in, which
		// is not a statement about the company's calendar.
		"the process's own zone": "Local",
		"nothing at all":         "  ",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			errs := refused(t, admin, http.MethodPut, settingsPath,
				settingsBody(map[string]any{"timeZone": value}), invalidSettingsTitle)
			if len(errs["timeZone"]) == 0 {
				t.Errorf("errors = %v, want one on timeZone", errs)
			}
		})
	}

	// A name both of them know is stored, and a replace that leaves the field
	// out keeps it — every trip in the installation is dated by it, so an
	// omission must not move them all.
	if zone := putSettings(t, admin, settingsBody(map[string]any{"timeZone": "America/New_York"})).TimeZone; zone != "America/New_York" {
		t.Errorf("time zone = %q, want the one set", zone)
	}
	if zone := putSettings(t, admin, settingsBody(nil)).TimeZone; zone != "America/New_York" {
		t.Errorf("time zone after a replace that left it out = %q, want it kept", zone)
	}
}
