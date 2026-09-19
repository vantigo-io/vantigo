package expenses_test

import (
	"net/http"
	"testing"
)

func TestExpensesRates_TheSeededOnesAreThereAndLabelled(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")

	rates := listRates(t, admin)
	if len(rates) != 2 {
		t.Fatalf("a fresh installation has %d rates, want the two seeded ones: %+v", len(rates), rates)
	}
	for _, want := range []struct {
		kind  string
		value float64
	}{{"mileage", 5.30}, {"mileage_passenger", 1.00}} {
		got := ratesOfKind(rates, want.kind)
		if len(got) != 1 {
			t.Fatalf("%s has %d rows, want one", want.kind, len(got))
		}
		r := got[0]
		if r.Value != want.value {
			t.Errorf("%s value = %v, want %v", want.kind, r.Value, want.value)
		}
		if r.ValidFrom != "2026-01-01" {
			t.Errorf("%s validFrom = %q, want 2026-01-01", want.kind, r.ValidFrom)
		}
		if r.Currency == nil || *r.Currency != "NOK" {
			t.Errorf("%s currency = %v, want NOK", want.kind, r.Currency)
		}
		if r.Source == nil || *r.Source != "State rate" {
			t.Errorf("%s source = %v, want it labelled 'State rate'", want.kind, r.Source)
		}
	}

	// The customer rate per kilometre is a company's own price, so nothing
	// ships one.
	if got := ratesOfKind(rates, "mileage_customer"); len(got) != 0 {
		t.Errorf("mileage_customer = %+v, want no seeded row", got)
	}
}

func TestExpensesRates_AddChangeAndRemove(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")

	created := createRate(t, admin, nil)
	if created.Kind != "mileage_customer" || created.Value != 7.5 || created.ValidFrom != "2026-01-01" {
		t.Fatalf("created = %+v, want the body back", created)
	}
	if created.Source != nil {
		t.Errorf("source = %v, want it absent — a row the company entered itself", created.Source)
	}

	r := admin.Do(http.MethodPut, ratePath(created.Id), map[string]any{
		"validFrom": "2026-02-01", "value": 8.25, "currency": "nok", "source": "Price list 2026",
	})
	if r.Status != http.StatusOK {
		t.Fatalf("change the rate: status %d body %s, want 200", r.Status, r.Body)
	}
	var updated rateJSON
	r.JSON(&updated)
	if updated.Id != created.Id || updated.Kind != "mileage_customer" {
		t.Errorf("updated = %+v, want the same row and kind — a replace does not move a rate to another kind", updated)
	}
	if updated.ValidFrom != "2026-02-01" || updated.Value != 8.25 {
		t.Errorf("updated = %+v, want the new day and value", updated)
	}
	if updated.Currency == nil || *updated.Currency != "NOK" {
		t.Errorf("currency = %v, want it upper-cased", updated.Currency)
	}
	if updated.Source == nil || *updated.Source != "Price list 2026" {
		t.Errorf("source = %v, want the label", updated.Source)
	}

	if r := admin.Do(http.MethodDelete, ratePath(created.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete the rate: status %d body %s, want 204", r.Status, r.Body)
	}
	if got := ratesOfKind(listRates(t, admin), "mileage_customer"); len(got) != 0 {
		t.Errorf("mileage_customer after the delete = %+v, want none", got)
	}
}

func TestExpensesRates_AnUnknownIdIsA404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")

	body := map[string]any{"validFrom": "2026-02-01", "value": 8.25, "currency": "NOK"}
	if r := admin.Do(http.MethodPut, ratePath(999_999), body); r.Status != http.StatusNotFound {
		t.Errorf("change a rate nobody has: status %d body %s, want 404", r.Status, r.Body)
	}
	if r := admin.Do(http.MethodDelete, ratePath(999_999), nil); r.Status != http.StatusNotFound {
		t.Errorf("delete a rate nobody has: status %d body %s, want 404", r.Status, r.Body)
	}
}

// One row per kind and day, enforced by the unique index rather than a
// read-then-write check, so two administrators adding the same day race to one
// row and one validFrom error.
func TestExpensesRates_OneRowPerKindAndDay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")

	errs := refused(t, admin, http.MethodPost, ratesPath,
		rateBody(map[string]any{"kind": "mileage", "value": 6}), "Invalid rate")
	if len(errs["validFrom"]) == 0 {
		t.Errorf("a second mileage row for 2026-01-01: errors = %v, want one on validFrom", errs)
	}

	// The same day on another kind is fine, and moving a row onto a taken day
	// is refused the same way.
	own := createRate(t, admin, map[string]any{"validFrom": "2026-06-01"})
	other := createRate(t, admin, map[string]any{"validFrom": "2026-07-01"})
	errs = refused(t, admin, http.MethodPut, ratePath(other.Id),
		map[string]any{"validFrom": "2026-06-01", "value": 9, "currency": "NOK"}, "Invalid rate")
	if len(errs["validFrom"]) == 0 {
		t.Errorf("moving a row onto a taken day: errors = %v, want one on validFrom", errs)
	}
	if own.Id == other.Id {
		t.Error("two creates answered the same id")
	}
}

func TestExpensesRates_RefusesAValueTheKindCannotCarry(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")

	for name, tc := range map[string]struct {
		overrides map[string]any
		field     string
	}{
		"a kind nobody knows":             {map[string]any{"kind": "parking"}, "kind"},
		"no kind":                         {map[string]any{"kind": ""}, "kind"},
		"a money kind with no currency":   {map[string]any{"currency": nil}, "currency"},
		"a money kind of two letters":     {map[string]any{"currency": "NO"}, "currency"},
		"a money value of zero":           {map[string]any{"value": 0}, "value"},
		"a money value below zero":        {map[string]any{"value": -1}, "value"},
		"a money value of three decimals": {map[string]any{"value": 5.005}, "value"},
		"a percentage with a currency": {
			map[string]any{"kind": "meal_breakfast_percent", "value": 20, "currency": "NOK"}, "currency",
		},
		"a percentage over 100": {
			map[string]any{"kind": "meal_lunch_percent", "value": 100.01, "currency": nil}, "value",
		},
		"a percentage below zero": {
			map[string]any{"kind": "meal_dinner_percent", "value": -0.5, "currency": nil}, "value",
		},
		"no valid-from day": {map[string]any{"validFrom": nil}, "validFrom"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			errs := refused(t, admin, http.MethodPost, ratesPath, rateBody(tc.overrides), "Invalid rate")
			if len(errs[tc.field]) == 0 {
				t.Errorf("errors = %v, want one on %s", errs, tc.field)
			}
		})
	}
}

// A percentage kind carries no currency at all, and 0 and 100 are both inside
// the range.
func TestExpensesRates_APercentageCarriesNoCurrency(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")

	created := createRate(t, admin, map[string]any{
		"kind": "meal_breakfast_percent", "value": 20, "currency": nil,
	})
	if created.Currency != nil {
		t.Errorf("currency = %v, want none on a percentage kind", created.Currency)
	}
	createRate(t, admin, map[string]any{
		"kind": "meal_lunch_percent", "value": 0, "currency": nil, "validFrom": "2026-01-02",
	})
	createRate(t, admin, map[string]any{
		"kind": "meal_dinner_percent", "value": 100, "currency": nil, "validFrom": "2026-01-03",
	})
}

// The list is flat, by kind and then the latest first, because the client
// groups it.
func TestExpensesRates_AreListedByKindAndThenTheLatestFirst(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")

	createRate(t, admin, map[string]any{"kind": "mileage", "validFrom": "2027-01-01", "value": 5.5})
	createRate(t, admin, map[string]any{"kind": "mileage", "validFrom": "2026-07-01", "value": 5.4})

	rates := listRates(t, admin)
	var kinds []string
	for _, r := range rates {
		kinds = append(kinds, r.Kind)
	}
	for i := 1; i < len(kinds); i++ {
		if kinds[i] < kinds[i-1] {
			t.Fatalf("kinds = %v, want them grouped in order", kinds)
		}
	}
	mileage := ratesOfKind(rates, "mileage")
	if len(mileage) != 3 {
		t.Fatalf("mileage rows = %+v, want three", mileage)
	}
	if mileage[0].ValidFrom != "2027-01-01" || mileage[1].ValidFrom != "2026-07-01" || mileage[2].ValidFrom != "2026-01-01" {
		t.Errorf("mileage rows = %+v, want the latest first", mileage)
	}
}

func TestExpensesRatesReset_PutsBackWhatIsMissingAndTouchesNothingElse(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")

	seeded := ratesOfKind(listRates(t, admin), "mileage")[0]
	own := createRate(t, admin, map[string]any{"kind": "mileage", "validFrom": "2026-07-01", "value": 6.1})
	if r := admin.Do(http.MethodDelete, ratePath(seeded.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete the seeded rate: status %d body %s, want 204", r.Status, r.Body)
	}

	after := resetRates(t, admin, "mileage")
	mileage := ratesOfKind(after, "mileage")
	if len(mileage) != 2 {
		t.Fatalf("mileage after the reset = %+v, want the restored seed beside the company's own row", mileage)
	}
	if mileage[0].ValidFrom != "2026-07-01" || mileage[0].Value != 6.1 {
		t.Errorf("the company's own row = %+v, want it untouched", mileage[0])
	}
	restored := mileage[1]
	if restored.ValidFrom != "2026-01-01" || restored.Value != 5.30 {
		t.Errorf("restored = %+v, want the seeded 2026-01-01 row back", restored)
	}
	if restored.Source == nil || *restored.Source != "State rate" {
		t.Errorf("restored source = %v, want it labelled again", restored.Source)
	}
	if restored.Id == seeded.Id {
		t.Errorf("restored id = %d, want a new row rather than the deleted one's id", restored.Id)
	}
	if own.Kind != "mileage" {
		t.Errorf("the company's own row is on %q, want mileage", own.Kind)
	}
}

// A reset really resets: a seeded day an administrator edited goes back to the
// value, currency and label it shipped with. It is the one write that undoes
// their own edit, which is exactly what "reset to default" promises.
func TestExpensesRatesReset_PutsAnEditedSeededDayBack(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")

	seeded := ratesOfKind(listRates(t, admin), "mileage")[0]
	r := admin.Do(http.MethodPut, ratePath(seeded.Id), map[string]any{
		"validFrom": "2026-01-01", "value": 4, "currency": "EUR",
	})
	if r.Status != http.StatusOK {
		t.Fatalf("change the seeded rate: status %d body %s, want 200", r.Status, r.Body)
	}

	after := ratesOfKind(resetRates(t, admin, "mileage"), "mileage")
	if len(after) != 1 {
		t.Fatalf("mileage after the reset = %+v, want the one seeded day", after)
	}
	if after[0].Value != 5.30 {
		t.Errorf("value = %v, want the shipped 5.30 back", after[0].Value)
	}
	if after[0].Currency == nil || *after[0].Currency != "NOK" {
		t.Errorf("currency = %v, want NOK back", after[0].Currency)
	}
	if after[0].Source == nil || *after[0].Source != "State rate" {
		t.Errorf("source = %v, want the label back", after[0].Source)
	}
	if after[0].Id != seeded.Id {
		t.Errorf("id = %d, want the same row put back rather than a new one", after[0].Id)
	}

	// A second reset is a no-op, and a kind that ships with nothing has
	// nothing to restore.
	again := ratesOfKind(resetRates(t, admin, "mileage"), "mileage")
	if len(again) != 1 || again[0].Value != 5.30 {
		t.Errorf("mileage after a second reset = %+v, want it unchanged", again)
	}
	if got := ratesOfKind(resetRates(t, admin, "mileage_customer"), "mileage_customer"); len(got) != 0 {
		t.Errorf("resetting mileage_customer produced %+v, want nothing — it ships unseeded", got)
	}
}

// A reset touches only the days the product shipped: the company's own rows,
// on their own days, are left exactly as they are.
func TestExpensesRatesReset_LeavesTheCompanysOwnRowsAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")

	own := createRate(t, admin, map[string]any{
		"kind": "mileage", "validFrom": "2026-07-01", "value": 6.1, "source": "Styrevedtak",
	})
	after := ratesOfKind(resetRates(t, admin, "mileage"), "mileage")
	if len(after) != 2 {
		t.Fatalf("mileage after the reset = %+v, want the company's row beside the seeded one", after)
	}
	if after[0].Id != own.Id || after[0].Value != 6.1 {
		t.Errorf("the company's own row = %+v, want it untouched", after[0])
	}
	if after[0].Source == nil || *after[0].Source != "Styrevedtak" {
		t.Errorf("source = %v, want their own label kept", after[0].Source)
	}
}

func TestExpensesRatesReset_RefusesAKindNobodyKnows(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")

	errs := refused(t, admin, http.MethodPost, ratesResetPath, map[string]any{"kind": "parking"}, "Invalid rate")
	if len(errs["kind"]) == 0 {
		t.Errorf("errors = %v, want one on kind", errs)
	}
}

func TestExpensesRates_AreAnAdministratorsToChange(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employee, _ := signIn(t, h)

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, ratesPath},
		{http.MethodPost, ratesResetPath},
		{http.MethodPut, ratePath(1001)},
		{http.MethodDelete, ratePath(1001)},
	} {
		if r := employee.Do(tc.method, tc.path, nil); r.Status != http.StatusForbidden {
			t.Errorf("%s %s as an employee: status %d body %s, want 403", tc.method, tc.path, r.Status, r.Body)
		}
	}
}

// Everyone with expenses:access reads the rates their own expense form
// previews a mileage line with — what the kilometres will be reimbursed at is
// not a secret from the person driving them. The one kind held back is
// mileage_customer: what the company charges its customer per kilometre is a
// commercial price, and an employee's form never shows it.
func TestExpensesRates_AreReadableByEveryAccessHolderExceptTheCustomerPrice(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	employee, _ := signIn(t, h)

	createRate(t, admin, map[string]any{"kind": "mileage_customer", "validFrom": "2026-01-01", "value": 7.5})

	forAdmin := listRates(t, admin)
	if got := ratesOfKind(forAdmin, "mileage_customer"); len(got) != 1 {
		t.Fatalf("mileage_customer for expenses:manage = %+v, want the row", got)
	}

	forEmployee := listRates(t, employee)
	if got := ratesOfKind(forEmployee, "mileage_customer"); len(got) != 0 {
		t.Errorf("mileage_customer for an employee = %+v, want it held back — it is the company's price to its customer", got)
	}
	for _, kind := range []string{"mileage", "mileage_passenger"} {
		got := ratesOfKind(forEmployee, kind)
		if len(got) != 1 {
			t.Errorf("%s for an employee = %+v, want the row their form previews with", kind, got)
		}
	}
	if len(forEmployee) != len(forAdmin)-1 {
		t.Errorf("an employee read %d rates and an administrator %d, want every kind but the one", len(forEmployee), len(forAdmin))
	}
}
