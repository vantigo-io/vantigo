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

	if got := getSettings(t, employee); got.DefaultCurrency != "NOK" || got.DefaultMarkupPercent != 0 {
		t.Errorf("a fresh installation: settings = %+v, want NOK and no markup", got)
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
	if saved.DefaultMarkupPercent != 7.25 {
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
	if got.DefaultCurrency != saved.DefaultCurrency || got.DefaultMarkupPercent != saved.DefaultMarkupPercent ||
		got.LockedBefore == nil || *got.LockedBefore != *saved.LockedBefore ||
		got.ReceiptRequiredOver == nil || *got.ReceiptRequiredOver != *saved.ReceiptRequiredOver {
		t.Errorf("an employee reads %+v, want the same settings the administrator saved, %+v", got, saved)
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
		"a threshold of zero":      {map[string]any{"receiptRequiredOver": 0}, "receiptRequiredOver"},
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
