package expenses

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/expenses/gen"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
)

// This file is the per diem day (design §4): the kind of line that exists only
// inside a travel claim, its arithmetic, the dated figures it is priced from,
// and the suggestion that proposes a trip's days from its departure and its
// return.
//
// Two of the three are **pure**: perDiemAmount is exact decimals and nothing
// else, and suggestPerDiem is two instants and a boolean. They are table-tested
// where they are written (perdiem_internal_test.go), and the handler below is a
// thin shell over the second — it writes nothing at all.
//
// **How a claim's days are derived.** A claim stores two instants; Postgres'
// timestamptz keeps the instant and not the offset it was typed in, so there is
// exactly one derivation of "the day" available and this module uses it
// everywhere: the UTC calendar day, utcDay(t) (entries_validation.go). It is
// what claimUnit judges the period lock on, what ListClaims' from/to filter
// applies in SQL, and what both the within-the-trip rule and the suggestion
// date a day by. One derivation, so a day a suggestion proposes can never fall
// outside the trip the save then judges it against.

// The kinds of per diem day (design §4). Each one names the rate kind it is
// priced at, except on a claim abroad, where the claim carries its own day rate
// and the type is recorded rather than looked up.
const (
	perDiem6To12         = "day_6_12"
	perDiemOver12        = "day_over_12"
	perDiemOvernightHtl  = "overnight_hotel"
	perDiemOvernightOthr = "overnight_other"
)

// perDiemTypes is every type a per diem day may carry, in the order design §4
// names them. A value outside it is a perDiemType field error.
var perDiemTypes = []string{perDiem6To12, perDiemOver12, perDiemOvernightHtl, perDiemOvernightOthr}

// perDiemRateKinds maps a day's type to the rate kind that prices it.
var perDiemRateKinds = map[string]string{
	perDiem6To12:         rateKindPerDiem6To12,
	perDiemOver12:        rateKindPerDiemOver12,
	perDiemOvernightHtl:  rateKindPerDiemHotel,
	perDiemOvernightOthr: rateKindPerDiemOther,
}

// perDiemNames is what a day of each type is called when its line is saved
// without a description of its own. The column is NOT NULL and a blank line in
// a trip's list would tell its reader nothing, so the server names the day
// after what it is — and the traveller may replace it with anything they like.
var perDiemNames = map[string]string{
	perDiem6To12:         "Kostgodtgjørelse 6–12 timer",
	perDiemOver12:        "Kostgodtgjørelse over 12 timer",
	perDiemOvernightHtl:  "Kostgodtgjørelse med overnatting på hotell",
	perDiemOvernightOthr: "Kostgodtgjørelse med annen overnatting",
}

// perDiemMeals is which of a day's three meals somebody else paid for. Each one
// ticked deducts its own percentage of that day's rate.
type perDiemMeals struct{ Breakfast, Lunch, Dinner bool }

// perDiemRates is the dated figures one per diem day is priced from: what a
// whole day of its type was worth, and what each meal deducts from it. A
// percentage is nil when the table prices none — which can only happen for a
// meal nobody covered, because a covered meal with no percentage is refused
// before it is ever priced.
type perDiemRates struct {
	DayRate   *big.Rat
	Breakfast *big.Rat
	Lunch     *big.Rat
	Dinner    *big.Rat
}

// perDiemAmount is what one per diem day pays its owner (design §4): the day
// rate less each covered meal's percentage of it, never below zero, and
// **rounded once** — the percentages are summed first and applied together, so
// a day with three deductions rounds the same way a person working it out on
// paper would.
func perDiemAmount(rates perDiemRates, meals perDiemMeals) *big.Rat {
	deducted := new(big.Rat)
	for _, covered := range []struct {
		ticked  bool
		percent *big.Rat
	}{
		{meals.Breakfast, rates.Breakfast},
		{meals.Lunch, rates.Lunch},
		{meals.Dinner, rates.Dinner},
	} {
		if covered.ticked && covered.percent != nil {
			deducted.Add(deducted, covered.percent)
		}
	}
	factor := new(big.Rat).Sub(big.NewRat(1, 1), new(big.Rat).Quo(deducted, hundred))
	if factor.Sign() < 0 {
		// Percentages an administrator set to more than a hundred between them
		// would otherwise make the day owe the company money.
		return new(big.Rat)
	}
	return roundHalfUp(new(big.Rat).Mul(rates.DayRate, factor), moneyPlaces)
}

// perDiemMissing is which of the four figures the rate table had nothing for on
// the day in question. The day rate is always reported; a meal is reported only
// when it was actually covered, because a meal nobody had is priced by nothing
// and deducts nothing.
type perDiemMissing struct {
	DayRate   bool
	Breakfast bool
	Lunch     bool
	Dinner    bool
}

// any reports whether anything at all was missing.
func (m perDiemMissing) any() bool { return m.DayRate || m.Breakfast || m.Lunch || m.Dinner }

// perDiemFiguresFor reads the dated figures one per diem day of perDiemType on
// date is priced from.
//
// On a claim abroad the day rate is the claim's own — the traveller was paid at
// a figure this table knows nothing about — and the type is recorded rather
// than looked up; the meal percentages come from the table either way, because
// a breakfast somebody else paid for is the same fraction of a day wherever the
// day was spent.
//
// It reads this module's own table and nothing else, so it is safe under a
// lock; a missing row is a value the caller turns into a field error, never a
// failure.
func perDiemFiguresFor(ctx context.Context, q *store.Queries, claim store.ExpensesClaim,
	perDiemType string, date time.Time, meals perDiemMeals,
) (perDiemRates, perDiemMissing, error) {
	var rates perDiemRates
	var missing perDiemMissing

	if claim.Abroad {
		abroad, err := ratPtrFromNumeric(claim.AbroadDayRate)
		if err != nil {
			return perDiemRates{}, perDiemMissing{}, err
		}
		rates.DayRate = abroad
		missing.DayRate = abroad == nil
	} else {
		kind, ok := perDiemRateKinds[perDiemType]
		if !ok {
			return perDiemRates{}, perDiemMissing{DayRate: true}, nil
		}
		rate, err := rateFor(ctx, q, kind, date)
		switch {
		case errors.Is(err, errNoRate):
			missing.DayRate = true
		case err != nil:
			return perDiemRates{}, perDiemMissing{}, err
		default:
			rates.DayRate = rate.Value
		}
	}

	for _, meal := range []struct {
		kind    string
		covered bool
		into    **big.Rat
		absent  *bool
	}{
		{rateKindMealBreakfastPercent, meals.Breakfast, &rates.Breakfast, &missing.Breakfast},
		{rateKindMealLunchPercent, meals.Lunch, &rates.Lunch, &missing.Lunch},
		{rateKindMealDinnerPercent, meals.Dinner, &rates.Dinner, &missing.Dinner},
	} {
		percent, err := rateFor(ctx, q, meal.kind, date)
		switch {
		case errors.Is(err, errNoRate):
			// A covered meal with no percentage is refused rather than paid in
			// full: a table an administrator has emptied must be visible, not
			// silently generous.
			*meal.absent = meal.covered
		case err != nil:
			return perDiemRates{}, perDiemMissing{}, err
		default:
			*meal.into = percent.Value
		}
	}
	return rates, missing, nil
}

// pricePerDiem is what one per diem day is worth, written onto v: the day rate
// from the table in force on the line's own date (or the claim's own rate
// abroad), the three percentages as they stood that day, and the amount the two
// come to. It is **the one recompute**: a draft save runs it on every save, and
// the claim's submit freezes exactly what it last wrote.
//
// A missing figure is a field error and never a silently smaller amount — the
// day rate on perDiemType, a covered meal's percentage on that meal's own flag
// — so a rate table somebody emptied is visible rather than generous.
func (s *server) pricePerDiem(ctx context.Context, q *store.Queries, c *caller, v *entryValues,
	p parsedEntry, claim *store.ExpensesClaim, add func(field, msg string),
) error {
	v.Billable = false
	if claim == nil || p.PerDiemType == nil {
		// Either the body did not stand up or the claim did not resolve; both
		// have already been reported, and there is nothing consistent to price.
		return nil
	}
	v.Currency = perDiemCurrency(*claim, c.Settings.DefaultCurrency)

	rates, missing, err := perDiemFiguresFor(ctx, q, *claim, *p.PerDiemType, p.Date, p.Meals)
	if err != nil {
		return err
	}
	if missing.DayRate {
		add("perDiemType", perDiemNoDayRate(*p.PerDiemType, p.Date))
	}
	for field, absent := range map[string]bool{
		"breakfastCovered": missing.Breakfast,
		"lunchCovered":     missing.Lunch,
		"dinnerCovered":    missing.Dinner,
	} {
		if absent {
			add(field, perDiemNoMealRate)
		}
	}
	v.MealPercents = perDiemRates{Breakfast: rates.Breakfast, Lunch: rates.Lunch, Dinner: rates.Dinner}
	if missing.any() {
		return nil
	}
	v.Rate = rates.DayRate
	v.Gross = perDiemAmount(rates, p.Meals)
	return nil
}

// perDiemCurrency is the currency one per diem day is paid in: the claim's own
// on a trip abroad, and the installation's otherwise. Nothing is ever
// converted (design §4).
func perDiemCurrency(claim store.ExpensesClaim, defaultCurrency string) string {
	if claim.Abroad && claim.AbroadCurrency != nil {
		return *claim.AbroadCurrency
	}
	return defaultCurrency
}

// The messages a per diem's own fields carry.
const (
	perDiemNeedsClaim   = "A per diem belongs to a travel claim"
	perDiemNotBillable  = "A per diem day is never billed on to a customer"
	perDiemNoMealRate   = "No deduction percentage applies on this date"
	perDiemNoPassengers = "A per diem day carries no passenger supplement"
)

// perDiemNoDayRate is the perDiemType message when the table prices no day of
// that kind on that date. per_diem_overnight_other is the one that ships
// unseeded — the agreement knows a single overnight rate — so a night
// somewhere that is not a hotel says this until an administrator enters the
// company's own figure.
func perDiemNoDayRate(perDiemType string, date time.Time) string {
	return fmt.Sprintf("No %s rate applies on %s", perDiemRateKinds[perDiemType], date.Format(time.DateOnly))
}

// perDiemDayTaken is the entryDate message when the claim already holds a per
// diem day for that date. One day of a trip is one line, whatever else it
// holds.
func perDiemDayTaken(date time.Time) string {
	return fmt.Sprintf("This travel claim already has a per diem day for %s", date.Format(time.DateOnly))
}

// perDiemOutsideTrip is the entryDate message when the day does not fall inside
// the trip, departure day and return day included.
func perDiemOutsideTrip(from, to time.Time) string {
	return fmt.Sprintf("A per diem day falls between %s and %s, the days this trip covers",
		from.Format(time.DateOnly), to.Format(time.DateOnly))
}

// claimDays is the calendar days a trip covers, its departure day and its
// return day included — the one derivation this module makes of a claim's
// dates (see the file header).
func claimDays(claim store.ExpensesClaim) (time.Time, time.Time) {
	return utcDay(claim.DepartureAt), utcDay(claim.ReturnAt)
}

// parsePerDiem is the per diem half of design §4: which kind of day it was and
// which meals somebody else paid for. What the day is worth is not the caller's
// to say — the rate table and the claim price it — so an amount, a VAT, a payer
// or a currency on it is refused, and so is every field of the two kinds that
// do carry them.
//
// Whether the day falls inside the trip, and whether the claim already holds
// one for it, need the claim and are asked afterwards (claimLineRules and the
// write's own lock).
func parsePerDiem(p *parsedEntry, body entryBody, add func(field, msg string)) {
	switch perDiemType := strings.TrimSpace(derefString(body.PerDiemType)); {
	case perDiemType == "":
		add("perDiemType", "A per diem day says which kind of day it was")
	case !slices.Contains(perDiemTypes, perDiemType):
		add("perDiemType", fmt.Sprintf("'%s' is not a per diem day; must be one of %s",
			perDiemType, strings.Join(perDiemTypes, ", ")))
	default:
		p.PerDiemType = &perDiemType
	}
	p.Meals = perDiemMeals{
		Breakfast: body.BreakfastCovered != nil && *body.BreakfastCovered,
		Lunch:     body.LunchCovered != nil && *body.LunchCovered,
		Dinner:    body.DinnerCovered != nil && *body.DinnerCovered,
	}

	for field, given := range map[string]bool{
		"categoryId":  body.CategoryID != nil,
		"supplier":    body.Supplier != nil,
		"paidBy":      body.PaidBy != nil,
		"grossAmount": body.GrossAmount != nil,
		"vatAmount":   body.VatAmount != nil,
		"currency":    body.Currency != nil && strings.TrimSpace(*body.Currency) != "",
	} {
		if given {
			add(field, notOnKind(field, kindPerDiem))
		}
	}
	refuseMileageFields(body, kindPerDiem, add)

	// Design §4: a per diem day is the employee's own money back and never a
	// customer's. Naming a billing line or asking for it to be billable is
	// refused rather than accepted and quietly dropped; the markup and the
	// customer rate per kilometre are already refused by parseProjectFields,
	// which lets neither onto anything but a billable outlay or billable
	// mileage.
	if body.Billable != nil && *body.Billable {
		add("billable", perDiemNotBillable)
	}
	if body.BillingLineID != nil {
		add("billingLineId", perDiemNotBillable)
	}
}

// perDiemDescription is what a per diem line is called: whatever its owner
// typed, or the name of the kind of day it is.
func perDiemDescription(described string, perDiemType *string) string {
	if described != "" {
		return described
	}
	if perDiemType == nil {
		return ""
	}
	return perDiemNames[*perDiemType]
}

// suggestedDay is one day the suggestion proposes: when its period starts, and
// which kind of day it is.
type suggestedDay struct {
	Date time.Time
	Type string
}

// perDiemPartPeriodHours is how long a part-period has to run to earn a day of
// its own: strictly longer than six hours, the same six hours a trip has to
// last before it earns anything at all.
const perDiemPartPeriodHours = 6 * time.Hour

// suggestPerDiem is design §4's day counting, and nothing else: two instants
// and whether the traveller slept away, in, a list of days out. It writes
// nothing, asks nothing and knows no rates — pricing the days it proposes is
// the handler's job, and recording them is the traveller's.
//
// The rule, in one sentence: **a period earns a day when it is a full 24 hours
// or a part longer than six.**
//
//   - Under six hours the trip earns nothing at all.
//   - Without an overnight it is one day, dated on the departure: day_6_12 up
//     to and including twelve hours, day_over_12 beyond. A trip of several days
//     that nobody slept away on is still one day: the traveller who did not
//     stay the night gets one day's rate, whatever the clock says.
//   - With an overnight it is one day per full 24-hour period from the
//     departure, plus one more when what is left over runs longer than six
//     hours. The periods are 24 hours from the departure instant, never
//     calendar midnights, and each day is dated on the UTC day its own period
//     starts.
//
// Every overnight day is proposed as overnight_hotel, the type the agreement
// prices; a traveller who stayed somewhere else changes it on the line.
func suggestPerDiem(departure, returns time.Time, overnight bool) []suggestedDay {
	duration := returns.Sub(departure)
	if duration < perDiemPartPeriodHours {
		return nil
	}
	if !overnight {
		dayType := perDiem6To12
		if duration > 12*time.Hour {
			dayType = perDiemOver12
		}
		return []suggestedDay{{Date: utcDay(departure), Type: dayType}}
	}

	const period = 24 * time.Hour
	days := int(duration / period)
	if duration%period > perDiemPartPeriodHours {
		days++
	}
	out := make([]suggestedDay, 0, days)
	for i := range days {
		out = append(out, suggestedDay{
			Date: utcDay(departure.Add(time.Duration(i) * period)),
			Type: perDiemOvernightHtl,
		})
	}
	return out
}

// PostExpensesClaimsByIdPerDiemSuggestion Suggest a trip's per diem days
// (POST /api/v1/expenses/claims/{id}/per-diem-suggestion)
//
// A thin shell over suggestPerDiem: the days it counts, each priced with the
// rate table as it stands today (or the claim's own day rate abroad), and a
// note of which dates the claim already holds a day for. **Nothing is
// written.** What to do about a day already recorded is the client's decision
// — replace it, skip it, show it greyed — and not one this module makes on its
// behalf.
//
// It is a POST because the answer depends on a body: whether the traveller
// slept away is a fact the two instants cannot tell, and there is no honest
// answer without it. Whoever may *see* the claim may ask for it; it changes
// nothing, so there is nothing narrower to protect.
func (s *server) PostExpensesClaimsByIdPerDiemSuggestion(ctx context.Context,
	req gen.PostExpensesClaimsByIdPerDiemSuggestionRequestObject,
) (gen.PostExpensesClaimsByIdPerDiemSuggestionResponseObject, error) {
	body := gen.ExpensesPerDiemSuggestionRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	claim, _, found, err := s.visibleClaim(ctx, q, c, req.Id, false)
	if err != nil {
		return nil, err
	}
	if !found {
		return gen.PostExpensesClaimsByIdPerDiemSuggestion404Response{}, nil
	}

	lines, err := q.ListClaimLines(ctx, &claim.ID)
	if err != nil {
		return nil, fmt.Errorf("expenses: read a travel claim's expenses: %w", err)
	}
	taken := map[string]bool{}
	for _, line := range lines {
		if line.Kind == kindPerDiem {
			taken[line.EntryDate.Time.Format(time.DateOnly)] = true
		}
	}

	days := suggestPerDiem(claim.DepartureAt, claim.ReturnAt, body.Overnight)
	out := make([]gen.ExpensesPerDiemSuggestedDay, 0, len(days))
	for _, day := range days {
		suggested := gen.ExpensesPerDiemSuggestedDay{
			EntryDate:   openapi_types.Date{Time: day.Date},
			PerDiemType: day.Type,
			Exists:      taken[day.Date.Format(time.DateOnly)],
		}
		// Priced with no meal covered: what the day is worth before the
		// traveller ticks anything off it.
		rates, missing, err := perDiemFiguresFor(ctx, q, claim, day.Type, day.Date, perDiemMeals{})
		if err != nil {
			return nil, err
		}
		if !missing.DayRate {
			suggested.DayRate = ptrTo(floatOfRat(rates.DayRate))
			suggested.Amount = ptrTo(floatOfRat(perDiemAmount(rates, perDiemMeals{})))
		}
		out = append(out, suggested)
	}
	return gen.PostExpensesClaimsByIdPerDiemSuggestion200JSONResponse(out), nil
}

// perDiemStored is the day's own record of how it was priced: which meals were
// covered, and what each of them deducted, as the columns hold them. It is what
// an approver's rate override reprices from — the day rate changes, the
// deductions the line was saved with do not — so a correction to the rate never
// silently drops a meal somebody else paid for.
func perDiemStored(row store.ExpensesEntry) (perDiemRates, perDiemMeals, error) {
	rates := perDiemRates{}
	for _, p := range []struct {
		column pgtype.Numeric
		into   **big.Rat
	}{
		{row.MealBreakfastPercent, &rates.Breakfast},
		{row.MealLunchPercent, &rates.Lunch},
		{row.MealDinnerPercent, &rates.Dinner},
	} {
		value, err := ratPtrFromNumeric(p.column)
		if err != nil {
			return perDiemRates{}, perDiemMeals{}, err
		}
		*p.into = value
	}
	meals := perDiemMeals{
		Breakfast: row.BreakfastCovered,
		Lunch:     row.LunchCovered,
		Dinner:    row.DinnerCovered,
	}
	return rates, meals, nil
}

// perDiemResponse renders the per diem half of one line, nil on every other
// kind. The day rate is the line's own rate column — so an approver's override
// moves it and the amount with it — and the three percentages are what the
// table said on the day the line was last priced, not what it says now.
func perDiemResponse(row store.ExpensesEntry) (*gen.ExpensesEntryPerDiem, error) {
	if row.Kind != kindPerDiem {
		return nil, nil
	}
	dayRate, err := floatFromNumeric(row.Rate)
	if err != nil {
		return nil, err
	}
	percents := gen.ExpensesEntryMealPercents{}
	for _, p := range []struct {
		column pgtype.Numeric
		into   **float64
	}{
		{row.MealBreakfastPercent, &percents.Breakfast},
		{row.MealLunchPercent, &percents.Lunch},
		{row.MealDinnerPercent, &percents.Dinner},
	} {
		value, err := floatPtrFromNumeric(p.column)
		if err != nil {
			return nil, err
		}
		*p.into = value
	}
	out := &gen.ExpensesEntryPerDiem{
		BreakfastCovered: row.BreakfastCovered,
		LunchCovered:     row.LunchCovered,
		DinnerCovered:    row.DinnerCovered,
		DayRate:          dayRate,
		MealPercents:     percents,
	}
	if row.PerDiemType != nil {
		out.Type = *row.PerDiemType
	}
	return out, nil
}
