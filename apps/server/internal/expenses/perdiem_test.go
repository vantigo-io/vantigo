package expenses_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the per diem day through the API (design §4): the kind that
// exists only inside a travel claim, what it is worth, what it refuses, the
// suggestion that proposes a trip's days, and an approver's correction to a day
// rate. The arithmetic and the day counting are tested where they are written
// (perdiem_internal_test.go); this is everything the two are wired into.

// The seeded rates a test names rather than repeating.
const (
	perDiemSixToTwelveRate = 397.00
	perDiemOverTwelveRate  = 736.00
	perDiemHotelRate       = 1012.00
)

// A per diem day is priced by the table for its own date, and the line records
// the figures it was priced from.
func TestExpensesPerDiem_IsPricedFromTheDatedTable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	claim := createClaim(t, owner, nil)

	day := addLine(t, owner, claim.Id, perDiemBody(nil))
	if day.GrossAmount != perDiemSixToTwelveRate || day.OwedToEmployee != perDiemSixToTwelveRate {
		t.Errorf("gross %v owed %v, want %v for a 6–12 hour day",
			day.GrossAmount, day.OwedToEmployee, perDiemSixToTwelveRate)
	}
	if day.Currency != "NOK" {
		t.Errorf("currency = %q, want the installation's own", day.Currency)
	}
	if day.Billable || day.VatAmount != nil {
		t.Errorf("billable %v vat %v, want neither on a per diem day", day.Billable, day.VatAmount)
	}
	if day.Rate == nil || *day.Rate != perDiemSixToTwelveRate {
		t.Errorf("rate = %v, want the day rate it was priced at", day.Rate)
	}
	if day.PerDiem == nil {
		t.Fatalf("perDiem = nil, want the day rendered")
	}
	if day.PerDiem.Type != "day_6_12" || day.PerDiem.DayRate != perDiemSixToTwelveRate {
		t.Errorf("perDiem = %+v, want a 6–12 hour day at %v", *day.PerDiem, perDiemSixToTwelveRate)
	}
	// Every percentage is snapshotted, covered or not: the day's own record of
	// what the table said, which an override later reprices from.
	p := day.PerDiem.MealPercents
	if p.Breakfast == nil || *p.Breakfast != 20 || p.Lunch == nil || *p.Lunch != 30 || p.Dinner == nil || *p.Dinner != 50 {
		t.Errorf("mealPercents = %+v, want 20/30/50", p)
	}
	if day.PerDiem.BreakfastCovered || day.PerDiem.LunchCovered || day.PerDiem.DinnerCovered {
		t.Errorf("perDiem = %+v, want no meal covered", *day.PerDiem)
	}
	// It is not a mileage line and does not pretend to be one.
	if day.DistanceKm != nil || day.Passengers != nil || day.PassengerRate != nil {
		t.Errorf("day = %+v, want none of the mileage fields", day)
	}
}

// Every type of day at the rate the table prices it, with the meals ticked off
// it. The arithmetic itself is money's own table; this is that the right day
// rate and the right percentages reach it through the API.
func TestExpensesPerDiem_DeductsEveryCoveredMeal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	for name, tc := range map[string]struct {
		perDiemType              string
		breakfast, lunch, dinner bool
		date                     string
		want                     float64
	}{
		"a 6–12 hour day":               {"day_6_12", false, false, false, "2026-03-09", perDiemSixToTwelveRate},
		"a long day with a lunch":       {"day_over_12", false, true, false, "2026-03-10", 515.20},
		"a hotel night with a dinner":   {"overnight_hotel", false, false, true, "2026-03-11", 506.00},
		"a hotel night, everything fed": {"overnight_hotel", true, true, true, "2026-03-09", 0.00},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// A claim of its own for each: one per diem day per date, and two
			// of these fall on the same day of the trip.
			own := createClaim(t, owner, nil)
			day := addLine(t, owner, own.Id, perDiemBody(map[string]any{
				"perDiemType":      tc.perDiemType,
				"entryDate":        tc.date,
				"breakfastCovered": tc.breakfast,
				"lunchCovered":     tc.lunch,
				"dinnerCovered":    tc.dinner,
			}))
			if day.GrossAmount != tc.want {
				t.Errorf("gross = %v, want %v", day.GrossAmount, tc.want)
			}
			if day.PerDiem == nil || day.PerDiem.BreakfastCovered != tc.breakfast ||
				day.PerDiem.LunchCovered != tc.lunch || day.PerDiem.DinnerCovered != tc.dinner {
				t.Errorf("perDiem = %+v, want the meals it was given", day.PerDiem)
			}
		})
	}
}

// A per diem day standing on its own is not a thing: a trip is what gives a day
// its rate, its currency and the window its date has to fall in.
func TestExpensesPerDiem_IsRefusedOutsideATravelClaim(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	errs := refusedEntry(t, owner, http.MethodPost, entriesPath, perDiemBody(nil))
	if !mentions(errs["kind"], "A per diem belongs to a travel claim") {
		t.Errorf("errors = %v, want kind to say a per diem belongs to a travel claim", errs)
	}
}

// Every field of the other two kinds is refused on its own field rather than
// stored and never used — and all of them at once, so one round trip tells the
// caller everything.
func TestExpensesPerDiem_RefusesEveryFieldItDoesNotCarry(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleManager)
	claim := createClaim(t, owner, map[string]any{"projectId": projectKraftVerket})

	errs := refusedEntry(t, owner, http.MethodPost, entriesPath, perDiemBody(map[string]any{
		"claimId":       claim.Id,
		"categoryId":    materialsCategory,
		"supplier":      "Hotell Bergen",
		"paidBy":        "employee",
		"grossAmount":   1000.00,
		"vatAmount":     250.00,
		"currency":      "EUR",
		"distanceKm":    120.0,
		"fromPlace":     "Oslo",
		"toPlace":       "Bergen",
		"passengers":    2,
		"billable":      true,
		"billingLineId": lineFixed,
		"markupPercent": 15.0,
		"billRatePerKm": 9.0,
	}))
	for _, field := range []string{
		"categoryId", "supplier", "paidBy", "grossAmount", "vatAmount", "currency",
		"distanceKm", "fromPlace", "toPlace", "passengers",
		"billable", "billingLineId", "markupPercent", "billRatePerKm",
	} {
		if len(errs[field]) == 0 {
			t.Errorf("errors = %v, want one on %s", errs, field)
		}
	}
}

// The type is what decides the rate, so a day without one, or with one nobody
// has heard of, is refused before anything is priced.
func TestExpensesPerDiem_NeedsATypeItKnows(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	claim := createClaim(t, owner, nil)

	for name, body := range map[string]map[string]any{
		"no type at all": perDiemBody(map[string]any{"claimId": claim.Id, "perDiemType": nil}),
		"a type nobody has heard of": perDiemBody(map[string]any{
			"claimId": claim.Id, "perDiemType": "overnight_yacht",
		}),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if errs := refusedEntry(t, owner, http.MethodPost, entriesPath, body); len(errs["perDiemType"]) == 0 {
				t.Errorf("errors = %v, want one on perDiemType", errs)
			}
		})
	}
}

// per_diem_overnight_other ships unseeded on purpose — the agreement knows one
// overnight rate, and a night somewhere else is priced at a figure the company
// sets. Until it does, such a day cannot be priced, and says so.
func TestExpensesPerDiem_RefusesATypeTheTablePricesNothingFor(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	claim := createClaim(t, owner, nil)

	errs := refusedEntry(t, owner, http.MethodPost, entriesPath,
		perDiemBody(map[string]any{"claimId": claim.Id, "perDiemType": "overnight_other"}))
	if !mentions(errs["perDiemType"], "per_diem_overnight_other") {
		t.Errorf("errors = %v, want perDiemType to name the rate that is missing", errs)
	}

	// Once an administrator enters one, the very same day saves.
	admin, _ := signIn(t, h, "expenses:manage")
	createRate(t, admin, map[string]any{
		"kind": "per_diem_overnight_other", "validFrom": "2026-01-01", "value": 450.00, "currency": "NOK",
	})
	day := addLine(t, owner, claim.Id, perDiemBody(map[string]any{"perDiemType": "overnight_other"}))
	if day.GrossAmount != 450.00 {
		t.Errorf("gross = %v, want the 450.00 the administrator entered", day.GrossAmount)
	}
}

// A meal somebody else paid for on a date the table prices no deduction for is
// refused rather than paid in full: a rate table an administrator has emptied
// must be visible, not silently generous.
func TestExpensesPerDiem_RefusesACoveredMealWithNoPercentage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	admin, _ := signIn(t, h, "expenses:manage")
	claim := createClaim(t, owner, nil)

	breakfast := ratesOfKind(listRates(t, admin), "meal_breakfast_percent")
	if len(breakfast) != 1 {
		t.Fatalf("meal_breakfast_percent rows = %d, want the one seeded", len(breakfast))
	}
	if r := admin.Do(http.MethodDelete, ratePath(breakfast[0].Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete the breakfast percentage: status %d body %s, want 204", r.Status, r.Body)
	}

	errs := refusedEntry(t, owner, http.MethodPost, entriesPath,
		perDiemBody(map[string]any{"claimId": claim.Id, "breakfastCovered": true}))
	if !mentions(errs["breakfastCovered"], "No deduction percentage applies on this date") {
		t.Errorf("errors = %v, want one on breakfastCovered", errs)
	}

	// A meal nobody covered needs no percentage, and the day saves without one.
	day := addLine(t, owner, claim.Id, perDiemBody(nil))
	if day.GrossAmount != perDiemSixToTwelveRate {
		t.Errorf("gross = %v, want the whole day", day.GrossAmount)
	}
	if day.PerDiem == nil || day.PerDiem.MealPercents.Breakfast != nil {
		t.Errorf("perDiem = %+v, want no breakfast percentage recorded", day.PerDiem)
	}
}

// One day of a trip is one line, whatever else the trip holds — decided under
// the claim's own row lock, so two days racing for one date cannot both take
// it.
func TestExpensesPerDiem_IsOneLinePerDate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	claim := createClaim(t, owner, nil)

	first := addLine(t, owner, claim.Id, perDiemBody(nil))
	errs := refusedEntry(t, owner, http.MethodPost, entriesPath,
		perDiemBody(map[string]any{"claimId": claim.Id, "perDiemType": "overnight_hotel"}))
	if !mentions(errs["entryDate"], "already has a per diem day for 2026-03-10") {
		t.Errorf("errors = %v, want entryDate to name the day already taken", errs)
	}

	// Another date is fine, and so is a mileage line on the very same day: the
	// rule is about per diem days, not about days.
	addLine(t, owner, claim.Id, perDiemBody(map[string]any{"entryDate": "2026-03-09"}))
	addLine(t, owner, claim.Id, mileageBody(nil))

	// A save that keeps the day where it is does not find itself.
	kept := updateEntry(t, owner, first.Id, perDiemBody(map[string]any{
		"perDiemType": "day_over_12", "revision": first.Revision,
	}))
	if kept.GrossAmount != perDiemOverTwelveRate {
		t.Errorf("gross = %v, want the long day's rate", kept.GrossAmount)
	}
	// Moving it onto a day already taken is refused.
	if errs := refusedEntry(t, owner, http.MethodPut, entryPath(first.Id), perDiemBody(map[string]any{
		"entryDate": "2026-03-09", "revision": kept.Revision,
	})); len(errs["entryDate"]) == 0 {
		t.Errorf("errors = %v, want one on entryDate", errs)
	}
}

// A per diem day is a day *of the trip*: its date falls between the departure
// day and the return day, both included.
func TestExpensesPerDiem_FallsInsideTheTrip(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	claim := createClaim(t, owner, nil) // 2026-03-09T07:00Z → 2026-03-11T16:00Z

	for _, date := range []string{"2026-03-09", "2026-03-10", "2026-03-11"} {
		if day := addLine(t, owner, claim.Id, perDiemBody(map[string]any{"entryDate": date})); day.EntryDate != date {
			t.Errorf("entryDate = %q, want %q", day.EntryDate, date)
		}
	}
	for _, date := range []string{"2026-03-08", "2026-03-12"} {
		errs := refusedEntry(t, owner, http.MethodPost, entriesPath,
			perDiemBody(map[string]any{"claimId": claim.Id, "entryDate": date}))
		if !mentions(errs["entryDate"], "between 2026-03-09 and 2026-03-11") {
			t.Errorf("%s: errors = %v, want entryDate to name the days the trip covers", date, errs)
		}
	}
}

// A trip abroad is paid at the claim's own day rate, in the currency the
// traveller was paid in — the dated table knows nothing about it — while the
// meal percentages come from the table as usual: a breakfast somebody else paid
// for is the same fraction of a day wherever the day was spent.
func TestExpensesPerDiem_AbroadTakesTheClaimsOwnRateAndCurrency(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	claim := createClaim(t, owner, map[string]any{
		"abroad": true, "abroadDayRate": 90.00, "abroadCurrency": "EUR",
	})

	day := addLine(t, owner, claim.Id, perDiemBody(map[string]any{"breakfastCovered": true}))
	if day.Currency != "EUR" {
		t.Errorf("currency = %q, want the claim's own", day.Currency)
	}
	if day.GrossAmount != 72.00 {
		t.Errorf("gross = %v, want 90.00 less a fifth", day.GrossAmount)
	}
	if day.PerDiem == nil || day.PerDiem.DayRate != 90.00 {
		t.Errorf("perDiem = %+v, want the claim's day rate", day.PerDiem)
	}
	// The type is still required, and still recorded: what kind of day it was
	// is a fact about the trip whoever reads it later will want.
	if day.PerDiem.Type != "day_6_12" {
		t.Errorf("type = %q, want it recorded", day.PerDiem.Type)
	}
	// A day of the one unseeded type is fine abroad — the claim prices it.
	other := addLine(t, owner, claim.Id, perDiemBody(map[string]any{
		"entryDate": "2026-03-09", "perDiemType": "overnight_other",
	}))
	if other.GrossAmount != 90.00 {
		t.Errorf("gross = %v, want the claim's own rate", other.GrossAmount)
	}
}

// A per diem day keeps the description its owner gave it and gets none of the
// server's own: what the day *is* is its perDiemType, which every reader has,
// and a name the server invented would sit in the column in one language for
// ever. A day saved without one carries the empty string.
func TestExpensesPerDiem_CarriesOnlyTheDescriptionItWasGiven(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	claim := createClaim(t, owner, nil)

	unnamed := addLine(t, owner, claim.Id, perDiemBody(nil))
	if unnamed.Description != "" {
		t.Errorf("description = %q, want the empty string and no invented name", unnamed.Description)
	}
	named := addLine(t, owner, claim.Id, perDiemBody(map[string]any{
		"entryDate": "2026-03-09", "description": "Dag to på anlegget",
	}))
	if named.Description != "Dag to på anlegget" {
		t.Errorf("description = %q, want the one given", named.Description)
	}
}

// A per diem day takes no receipt, exactly as a mileage line does not.
func TestExpensesPerDiem_TakesNoReceipt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	claim := createClaim(t, owner, nil)
	day := addLine(t, owner, claim.Id, perDiemBody(nil))

	errs := refusedReceipt(t, owner, day.Id, "kvittering.png", "image/png", testPNG(t, 8, 8))
	if !mentions(errs["entryId"], "Only an outlay carries a receipt") {
		t.Errorf("errors = %v, want one on entryId", errs)
	}
}

// The claim's own totals count its per diem days, and the list can be narrowed
// to them.
func TestExpensesPerDiem_CountsTowardsTheClaimsTotals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	claim := createClaim(t, owner, nil)
	addLine(t, owner, claim.Id, perDiemBody(nil))
	addLine(t, owner, claim.Id, outlayBody(nil))

	read := getClaim(t, owner, claim.Id)
	if len(read.Totals) != 1 {
		t.Fatalf("totals = %+v, want one currency", read.Totals)
	}
	if want := perDiemSixToTwelveRate + 1250.00; read.Totals[0].Gross != want {
		t.Errorf("gross = %v, want %v", read.Totals[0].Gross, want)
	}
	if read.Totals[0].OwedToEmployee != perDiemSixToTwelveRate+1250.00 {
		t.Errorf("owed = %v, want the day and the outlay both", read.Totals[0].OwedToEmployee)
	}

	page := listEntries(t, owner, "?kind=per_diem")
	if len(page.Data) != 1 || page.Data[0].Kind != "per_diem" {
		t.Errorf("kind=per_diem gave %d rows, want the one day", len(page.Data))
	}
}

// Nothing but a per diem line carries the per diem fields, and nothing but a
// per diem line renders the object.
func TestExpensesPerDiem_IsRefusedOnEveryOtherKind(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	errs := refusedEntry(t, owner, http.MethodPost, entriesPath, mileageBody(map[string]any{
		"perDiemType": "day_6_12", "breakfastCovered": true, "lunchCovered": true, "dinnerCovered": true,
	}))
	for _, field := range []string{"perDiemType", "breakfastCovered", "lunchCovered", "dinnerCovered"} {
		if len(errs[field]) == 0 {
			t.Errorf("errors = %v, want one on %s", errs, field)
		}
	}
	if mileage := createEntry(t, owner, mileageBody(nil)); mileage.PerDiem != nil {
		t.Errorf("perDiem = %+v on a mileage line, want it absent", mileage.PerDiem)
	}
}

// The suggestion is a read that writes nothing: it proposes the days a trip's
// own times imply, priced with the table as it stands.
func TestExpensesPerDiemSuggestion_ProposesTheDaysAndPricesThem(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	// 2026-03-09T07:00Z → 2026-03-11T16:00Z is 57 hours: two whole days and a
	// part-period of nine hours.
	claim := createClaim(t, owner, nil)

	days := suggestDays(t, owner, claim.Id, true)
	want := []string{"2026-03-09", "2026-03-10", "2026-03-11"}
	if len(days) != len(want) {
		t.Fatalf("days = %+v, want three", days)
	}
	for i, date := range want {
		if days[i].EntryDate != date || days[i].PerDiemType != "overnight_hotel" {
			t.Errorf("day %d = %+v, want a hotel night on %s", i, days[i], date)
		}
		if days[i].DayRate == nil || *days[i].DayRate != perDiemHotelRate {
			t.Errorf("day %d rate = %v, want %v", i, days[i].DayRate, perDiemHotelRate)
		}
		if days[i].Amount == nil || *days[i].Amount != perDiemHotelRate {
			t.Errorf("day %d amount = %v, want the whole day", i, days[i].Amount)
		}
		if days[i].Exists {
			t.Errorf("day %d says it exists, want not yet", i)
		}
	}

	// Without an overnight the same trip is one long day.
	if noNight := suggestDays(t, owner, claim.Id, false); len(noNight) != 1 ||
		noNight[0].PerDiemType != "day_over_12" || noNight[0].EntryDate != "2026-03-09" {
		t.Errorf("days = %+v, want one long day on the departure", noNight)
	}

	// Nothing was written.
	if read := getClaim(t, owner, claim.Id); len(read.Lines) != 0 {
		t.Errorf("lines = %d, want the suggestion to have written nothing", len(read.Lines))
	}

	// A day the claim already holds is marked rather than hidden: what to do
	// about it is the client's decision.
	addLine(t, owner, claim.Id, perDiemBody(map[string]any{"entryDate": "2026-03-10"}))
	again := suggestDays(t, owner, claim.Id, true)
	if len(again) != 3 || again[0].Exists || !again[1].Exists || again[2].Exists {
		t.Errorf("days = %+v, want only the middle one marked as existing", again)
	}
}

// A day the table prices nothing for is suggested without a rate and without an
// amount, rather than left out: the traveller was away, whatever the table
// says.
func TestExpensesPerDiemSuggestion_LeavesTheRateOutWhenNoneApplies(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	admin, _ := signIn(t, h, "expenses:manage")
	claim := createClaim(t, owner, nil)

	hotel := ratesOfKind(listRates(t, admin), "per_diem_overnight_hotel")
	if len(hotel) != 1 {
		t.Fatalf("per_diem_overnight_hotel rows = %d, want the one seeded", len(hotel))
	}
	if r := admin.Do(http.MethodDelete, ratePath(hotel[0].Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete the hotel rate: status %d body %s, want 204", r.Status, r.Body)
	}

	days := suggestDays(t, owner, claim.Id, true)
	if len(days) != 3 {
		t.Fatalf("days = %+v, want three", days)
	}
	for i, day := range days {
		if day.DayRate != nil || day.Amount != nil {
			t.Errorf("day %d = %+v, want no rate and no amount", i, day)
		}
	}
}

// Abroad, the suggestion prices every day at the claim's own rate.
func TestExpensesPerDiemSuggestion_AbroadPricesAtTheClaimsOwnRate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	claim := createClaim(t, owner, map[string]any{
		"abroad": true, "abroadDayRate": 90.00, "abroadCurrency": "EUR",
		"departureAt": "2026-03-09T07:00:00Z", "returnAt": "2026-03-09T20:00:00Z",
	})

	days := suggestDays(t, owner, claim.Id, false)
	if len(days) != 1 || days[0].PerDiemType != "day_over_12" {
		t.Fatalf("days = %+v, want one long day", days)
	}
	if days[0].DayRate == nil || *days[0].DayRate != 90.00 || days[0].Amount == nil || *days[0].Amount != 90.00 {
		t.Errorf("day = %+v, want the claim's own rate", days[0])
	}
}

// The suggestion follows the claim's own visibility rule: whoever may see the
// trip may ask for it, and a stranger gets the bare 404 an unknown id gets.
func TestExpensesPerDiemSuggestion_FollowsTheClaimsVisibility(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	stranger, _ := signIn(t, h)
	reader, _ := signIn(t, h, "expenses:view-all")
	claim := createClaim(t, owner, nil)

	suggestDays(t, reader, claim.Id, true)

	body := map[string]any{"overnight": true}
	if r := stranger.Do(http.MethodPost, perDiemSuggestionPath(claim.Id), body); r.Status != http.StatusNotFound {
		t.Errorf("a stranger: status %d body %s, want 404", r.Status, r.Body)
	}
	if r := owner.Do(http.MethodPost, perDiemSuggestionPath(claim.Id+9999), body); r.Status != http.StatusNotFound {
		t.Errorf("an unknown claim: status %d body %s, want 404", r.Status, r.Body)
	}
}

// An approver may correct a per diem day's rate exactly as they may a mileage
// line's, and the day is repriced from the deductions it was **saved** with —
// never from the table as it stands today.
func TestExpensesPerDiemRateOverride_RepricesFromTheStoredPercentages(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	approver, approverID := signIn(t, h, "expenses:approve")
	admin, _ := signIn(t, h, "expenses:manage")
	claim := createClaim(t, owner, nil)

	day := addLine(t, owner, claim.Id, perDiemBody(map[string]any{"breakfastCovered": true}))
	if day.GrossAmount != 317.60 {
		t.Fatalf("gross = %v, want 397.00 less a fifth", day.GrossAmount)
	}
	// The claim flow is a later delivery's; the rule is written against the
	// claim's status column, so that is what this drives.
	seedClaimStatus(t, h, claim.Id, "submitted")

	// The table changes after the day was saved: the override must not pick
	// this up, or an approver correcting a rate would silently change a
	// deduction too.
	breakfast := ratesOfKind(listRates(t, admin), "meal_breakfast_percent")
	if r := admin.Do(http.MethodPut, ratePath(breakfast[0].Id), map[string]any{
		"validFrom": "2026-01-01", "value": 50.00, "source": "Endret",
	}); r.Status != http.StatusOK {
		t.Fatalf("change the breakfast percentage: status %d body %s, want 200", r.Status, r.Body)
	}

	submitted := getEntry(t, approver, day.Id)
	if !submitted.Capabilities.CanOverrideRate {
		t.Fatalf("capabilities = %+v, want canOverrideRate on a submitted claim's day", submitted.Capabilities)
	}
	overridden := overrideRate(t, approver, day.Id, map[string]any{
		"rate": 500.00, "revision": submitted.Revision,
	})
	if overridden.GrossAmount != 400.00 {
		t.Errorf("gross = %v, want 500.00 less the fifth the day was saved with", overridden.GrossAmount)
	}
	if overridden.PerDiem == nil || overridden.PerDiem.DayRate != 500.00 {
		t.Errorf("perDiem = %+v, want the overridden day rate", overridden.PerDiem)
	}
	if p := overridden.PerDiem.MealPercents; p.Breakfast == nil || *p.Breakfast != 20 {
		t.Errorf("mealPercents = %+v, want the 20 the day was saved with", p)
	}
	if overridden.RateOverride == nil || overridden.RateOverride.ByUser.UserId != approverID {
		t.Errorf("rateOverride = %+v, want it recorded against the approver", overridden.RateOverride)
	}
	if overridden.RateOverride.TableValue == nil || *overridden.RateOverride.TableValue != perDiemSixToTwelveRate {
		t.Errorf("tableValue = %v, want the %v the table had said",
			overridden.RateOverride.TableValue, perDiemSixToTwelveRate)
	}
	if overridden.RateOverride.PassengerTableValue != nil {
		t.Errorf("passengerTableValue = %v, want nothing on a day that has no supplement",
			overridden.RateOverride.PassengerTableValue)
	}
}

// A per diem day carries no passenger supplement, so naming one on one is
// refused rather than stored and never used.
func TestExpensesPerDiemRateOverride_RefusesAPassengerSupplement(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")
	claim := createClaim(t, owner, nil)
	day := addLine(t, owner, claim.Id, perDiemBody(nil))
	seedClaimStatus(t, h, claim.Id, "submitted")

	read := getEntry(t, approver, day.Id)
	errs := refusedEntry(t, approver, http.MethodPut, entryRatePath(day.Id), map[string]any{
		"rate": 500.00, "passengerRate": 2.00, "revision": read.Revision,
	})
	if !mentions(errs["passengerRate"], "no passenger supplement") {
		t.Errorf("errors = %v, want one on passengerRate", errs)
	}
}

// An outlay still carries no rate to override, and the refusal now names both
// the kinds that do.
func TestExpensesRateOverride_StillRefusesAnOutlay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")

	outlay := createEntry(t, owner, outlayBody(nil))
	submitted := submitEntries(t, owner, outlay.Id)
	errs := refusedEntry(t, approver, http.MethodPut, entryRatePath(outlay.Id), map[string]any{
		"rate": 8.00, "revision": submitted[0].Revision,
	})
	if !mentions(errs["kind"], "Only a mileage line or a per diem day carries a rate to override") {
		t.Errorf("errors = %v, want one on kind", errs)
	}
}

// The six rows this delivery seeds are everybody's to read — what a day away is
// worth is what the person away is being paid — and every one of them can be
// put back after an administrator has changed it.
func TestExpensesPerDiemRates_AreVisibleAndResettable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employee, _ := signIn(t, h)
	admin, _ := signIn(t, h, "expenses:manage")

	seeded := map[string]float64{
		"per_diem_6_12":            397.00,
		"per_diem_over_12":         736.00,
		"per_diem_overnight_hotel": 1012.00,
		"meal_breakfast_percent":   20.00,
		"meal_lunch_percent":       30.00,
		"meal_dinner_percent":      50.00,
	}
	rates := listRates(t, employee)
	for kind, value := range seeded {
		rows := ratesOfKind(rates, kind)
		if len(rows) != 1 || rows[0].Value != value {
			t.Errorf("%s = %+v, want one row of %v visible to an employee", kind, rows, value)
		}
	}

	// Each one restored after it was changed, and the percentages back with no
	// currency at all.
	for kind := range seeded {
		row := ratesOfKind(listRates(t, admin), kind)[0]
		if r := admin.Do(http.MethodDelete, ratePath(row.Id), nil); r.Status != http.StatusNoContent {
			t.Fatalf("delete the %s rate: status %d body %s, want 204", kind, r.Status, r.Body)
		}
		restored := ratesOfKind(resetRates(t, admin, kind), kind)
		if len(restored) != 1 || restored[0].Value != seeded[kind] {
			t.Errorf("%s after a reset = %+v, want the shipped row back", kind, restored)
		}
		percent := kind == "meal_breakfast_percent" || kind == "meal_lunch_percent" || kind == "meal_dinner_percent"
		switch {
		case percent && restored[0].Currency != nil:
			t.Errorf("%s currency = %v, want none on a percentage", kind, *restored[0].Currency)
		case !percent && (restored[0].Currency == nil || *restored[0].Currency != "NOK"):
			t.Errorf("%s currency = %v, want NOK", kind, restored[0].Currency)
		}
	}

	// The one kind that ships with nothing restores nothing — a clean no-op,
	// not a failure.
	if after := ratesOfKind(resetRates(t, admin, "per_diem_overnight_other"), "per_diem_overnight_other"); len(after) != 0 {
		t.Errorf("per_diem_overnight_other after a reset = %+v, want nothing to restore", after)
	}
}

// ---------------------------------------------------------------------------
// A per diem day is never priced: the project's own door refuses it too
// ---------------------------------------------------------------------------

// The entry doors refuse `billable` and `billingLineId` on their own fields.
// This is the same rule on the *project's* door, which is the one a project
// manager reaches for — without it a per diem day could be made billable, and
// then invoiced, by exactly the role the design put on the other side of the
// line.
func TestExpensesPerDiem_IsNeverPricedFromTheProjectsSide(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	manager, managerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	h.projects.addRole(projectKraftVerket, managerID, roleManager)

	claim := createClaim(t, owner, map[string]any{"projectId": projectKraftVerket})
	day := addLine(t, owner, claim.Id, perDiemBody(nil))

	// The manager sees the project's money on it and is told, truthfully, that
	// there is nothing here for them to set.
	read := getEntry(t, manager, day.Id)
	if !read.Capabilities.CanSeeBilling {
		t.Fatalf("capabilities = %+v, want the project's manager to see its billing", read.Capabilities)
	}
	if read.Capabilities.CanSetBilling || read.Capabilities.CanMarkInvoiced {
		t.Errorf("capabilities = %+v, want neither canSetBilling nor canMarkInvoiced on a per diem day",
			read.Capabilities)
	}

	errs := refusedEntry(t, manager, http.MethodPut, entryBillingPath(day.Id), map[string]any{
		"billable": true, "billingLineId": lineFixed, "revision": read.Revision,
	})
	if !mentions(errs["kind"], "never billed on to a customer") {
		t.Errorf("errors = %v, want one on kind", errs)
	}

	// The dialog's own picker refuses it in the same words, so the two doors
	// cannot drift — the property that operation's comment promises.
	if errs := refusedEntry(t, manager, http.MethodGet, billingLinesPath(day.Id), nil); len(errs["kind"]) == 0 {
		t.Errorf("errors = %v, want the picker refused on kind too", errs)
	}

	// And nothing moved: the day is still nobody's to bill.
	if n := h.Count(t, `SELECT count(*) FROM expenses.entries
		WHERE id = $1 AND billable = false AND billing_line_id IS NULL`, day.Id); n != 1 {
		t.Errorf("the day was made billable after all")
	}

	// A mileage line of the same claim is priced exactly as it always was.
	mileage := addLine(t, owner, claim.Id, mileageBody(nil))
	priced := setBilling(t, manager, mileage.Id, map[string]any{
		"billable": true, "billRatePerKm": 9.0, "revision": mileage.Revision,
	})
	if !priced.Billable {
		t.Errorf("the mileage line = %+v, want it still priceable", priced)
	}
}

// A day that somehow went billable before that door was closed still cannot be
// put on an invoice: the second door says so for itself rather than trusting
// the first.
func TestExpensesPerDiem_IsNeverInvoiced(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	manager, managerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	h.projects.addRole(projectKraftVerket, managerID, roleManager)

	claim := createClaim(t, owner, map[string]any{"projectId": projectKraftVerket})
	day := addLine(t, owner, claim.Id, perDiemBody(nil))
	// The state the closed door can no longer produce, written the only way it
	// now can be, so the guard below is the thing under test.
	h.Exec(t, `UPDATE expenses.entries SET billable = true, bill_amount = 100.00 WHERE id = $1`, day.Id)
	seedClaimStatus(t, h, claim.Id, "approved")

	read := getEntry(t, manager, day.Id)
	if read.Capabilities.CanMarkInvoiced {
		t.Errorf("capabilities = %+v, want canMarkInvoiced false on a per diem day", read.Capabilities)
	}
	errs := refusedEntry(t, manager, http.MethodPost, entryInvoicedPath(day.Id), map[string]any{
		"revision": read.Revision,
	})
	if !mentions(errs["kind"], "never billed on to a customer") {
		t.Errorf("errors = %v, want one on kind", errs)
	}
}

// ---------------------------------------------------------------------------
// A claim's own edits keep its days honest
// ---------------------------------------------------------------------------

// The trip's window is what says which dates a per diem day may fall on, so
// narrowing it past a day already recorded is refused — naming every stranded
// date, because removing them is the caller's decision and not the server's.
func TestExpensesPerDiem_ANarrowedTripRefusesRatherThanStrandItsDays(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	claim := createClaim(t, owner, nil) // 2026-03-09 → 2026-03-11
	for _, date := range []string{"2026-03-09", "2026-03-10", "2026-03-11"} {
		addLine(t, owner, claim.Id, perDiemBody(map[string]any{"entryDate": date}))
	}
	read := getClaim(t, owner, claim.Id)

	errs := refusedClaim(t, owner, http.MethodPut, claimPath(claim.Id), claimBody(map[string]any{
		"departureAt": "2026-03-10T07:00:00Z", "returnAt": "2026-03-10T20:00:00Z",
		"revision": read.Revision,
	}))
	if !mentions(errs["departureAt"], "2026-03-09") || !mentions(errs["departureAt"], "remove it first") {
		t.Errorf("errors = %v, want departureAt to name the day left before the trip", errs)
	}
	if !mentions(errs["returnAt"], "2026-03-11") {
		t.Errorf("errors = %v, want returnAt to name the day left after the trip", errs)
	}
	// Nothing moved: not the claim, and not its days.
	if after := getClaim(t, owner, claim.Id); after.Revision != read.Revision || len(after.Lines) != 3 {
		t.Errorf("claim = %+v, want it untouched", after)
	}

	// Widening is fine, and so is a window that still covers every day.
	updateClaim(t, owner, claim.Id, map[string]any{
		"departureAt": "2026-03-08T07:00:00Z", "returnAt": "2026-03-12T20:00:00Z",
		"revision": read.Revision,
	})
}

// A trip's own money is what a per diem day is paid at, so an edit to it
// reprices every day the claim holds — in the same transaction, under the same
// lock, so no day is ever left at yesterday's figure.
func TestExpensesPerDiem_AClaimsMoneyChangingRepricesItsDays(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	claim := createClaim(t, owner, nil)
	day := addLine(t, owner, claim.Id, perDiemBody(map[string]any{"breakfastCovered": true}))
	if day.GrossAmount != 317.60 {
		t.Fatalf("gross = %v, want 397.00 less a fifth", day.GrossAmount)
	}

	// Domestic → abroad: the claim's own rate and currency, the deduction still
	// the table's.
	abroad := updateClaim(t, owner, claim.Id, map[string]any{
		"abroad": true, "abroadDayRate": 90.00, "abroadCurrency": "EUR", "revision": claim.Revision,
	})
	assertPerDiemRow(t, h, day.Id, "90.00", "72.00", "EUR")
	if abroad.Lines[0].Revision == day.Revision {
		t.Errorf("revision = %d, want the repriced day to have moved on", abroad.Lines[0].Revision)
	}

	// A correction to the rate alone reprices too.
	corrected := updateClaim(t, owner, claim.Id, map[string]any{
		"abroad": true, "abroadDayRate": 100.00, "abroadCurrency": "EUR", "revision": abroad.Revision,
	})
	assertPerDiemRow(t, h, day.Id, "100.00", "80.00", "EUR")

	// And back to domestic: the table prices the day again.
	updateClaim(t, owner, claim.Id, map[string]any{"revision": corrected.Revision})
	assertPerDiemRow(t, h, day.Id, "397.00", "317.60", "NOK")
}

// A change that would leave a day with nothing to price it refuses the whole
// edit: half a claim repriced is worse than an edit the caller can simply undo.
func TestExpensesPerDiem_AClaimEditThatCannotPriceADayIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	// A day of the one type the table prices nothing for is only recordable
	// abroad, where the claim pays it.
	claim := createClaim(t, owner, map[string]any{
		"abroad": true, "abroadDayRate": 90.00, "abroadCurrency": "EUR",
	})
	day := addLine(t, owner, claim.Id, perDiemBody(map[string]any{"perDiemType": "overnight_other"}))

	errs := refusedClaim(t, owner, http.MethodPut, claimPath(claim.Id),
		claimBody(map[string]any{"revision": claim.Revision}))
	if !mentions(errs["abroad"], "2026-03-10") || !mentions(errs["abroad"], "per_diem_overnight_other") {
		t.Errorf("errors = %v, want abroad to name the date and the rate that is missing", errs)
	}
	// The claim is still abroad and the day is still paid.
	assertPerDiemRow(t, h, day.Id, "90.00", "90.00", "EUR")
	if after := getClaim(t, owner, claim.Id); !after.Abroad {
		t.Errorf("claim = %+v, want the refused edit to have changed nothing", after)
	}
}

// assertPerDiemRow reads one per diem line's stored figures straight out of the
// table: what the response renders is one thing, what the column holds is the
// thing a later freeze will carry.
func assertPerDiemRow(t *testing.T, h *harness, id int64, rate, gross, currency string) {
	t.Helper()
	got := modtest.One[string](t, h.Harness, `SELECT concat_ws(' ', rate, gross_amount, currency)
		FROM expenses.entries WHERE id = $1`, id)
	if want := rate + " " + gross + " " + currency; got != want {
		t.Errorf("the stored day = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// One day per date, proved under contention
// ---------------------------------------------------------------------------

// The one-per-date rule is decided under the claim's own row lock. Two requests
// for the same day arriving at once is the only thing that proves the lock is
// really held: without it both reads see no day and both inserts succeed.
func TestExpensesPerDiem_TwoDaysRacingForOneDate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	other, _ := signIn(t, h, "expenses:manage")

	for round := range raceRounds {
		claim := createClaim(t, owner, nil)
		body := perDiemBody(map[string]any{"claimId": claim.Id})
		var a, b int
		race(
			func() { a = owner.Do(http.MethodPost, entriesPath, body).Status },
			func() { b = other.Do(http.MethodPost, entriesPath, body).Status },
		)
		answers := []int{a, b}
		slices.Sort(answers)
		if !slices.Equal(answers, []int{http.StatusCreated, http.StatusBadRequest}) {
			t.Fatalf("round %d: %d and %d, want one 201 and one refusal", round, a, b)
		}
		if n := h.Count(t, `SELECT count(*) FROM expenses.entries
			WHERE claim_id = $1 AND kind = 'per_diem' AND entry_date = DATE '2026-03-10'`, claim.Id); n != 1 {
			t.Fatalf("round %d: %d per diem days for one date, want exactly one", round, n)
		}
	}
}
