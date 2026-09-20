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

// errNoDayRate is perDiemAmount's answer to being asked what a day with no day
// rate is worth. Every caller knows the rate first — from the table, from the
// claim, or from an approver's own body — and turns this into the field error
// that names what is missing; the function is total rather than trusting them,
// because a nil rate here would otherwise be a panic in a money path.
var errNoDayRate = errors.New("expenses: a per diem day has no day rate")

// perDiemAmount is what one per diem day pays its owner (design §4): the day
// rate less each covered meal's percentage of it, never below zero, and
// **rounded once** — the percentages are summed first and applied together, so
// a day with three deductions rounds the same way a person working it out on
// paper would.
func perDiemAmount(rates perDiemRates, meals perDiemMeals) (*big.Rat, error) {
	if rates.DayRate == nil {
		return nil, errNoDayRate
	}
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
		return new(big.Rat), nil
	}
	return roundHalfUp(new(big.Rat).Mul(rates.DayRate, factor), moneyPlaces), nil
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
		add("perDiemType", perDiemNoDayRate(*claim, *p.PerDiemType, p.Date))
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
	if v.Gross, err = perDiemAmount(rates, p.Meals); err != nil {
		return err
	}
	return nil
}

// repriceUnderLock judges and prices one per diem line again against the claim
// the transaction is holding, and writes the result back onto the prepared save.
// It closes the gap between prepare — which reads the claim outside any lock,
// because that is where the project directory may be asked — and the write: a
// claim edit that committed in between moves neither the line's revision nor its
// columns, so nothing else would notice.
//
// It answers a field and a message rather than a map, because the handler that
// calls it reports one stale refusal at a time.
func (s *server) repriceUnderLock(ctx context.Context, txq *store.Queries, c *caller,
	p *prepared, claim store.ExpensesClaim,
) (string, string, error) {
	if from, to := claimDays(claim, c.zone()); p.Parsed.Date.Before(from) || p.Parsed.Date.After(to) {
		return "entryDate", perDiemOutsideTrip(from, to), nil
	}
	var errs map[string][]string
	add := func(field, msg string) { errs = withFieldError(errs, field, msg) }
	if err := s.pricePerDiem(ctx, txq, c, &p.Values, p.Parsed, &claim, add); err != nil {
		return "", "", err
	}
	for _, field := range []string{"perDiemType", "breakfastCovered", "lunchCovered", "dinnerCovered"} {
		if msgs := errs[field]; len(msgs) > 0 {
			return field, msgs[0], nil
		}
	}
	columns, err := columnsOf(p.Parsed, p.Values)
	if err != nil {
		return "", "", err
	}
	p.Columns = columns
	return "", "", nil
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

// perDiemNoDayRate is the perDiemType message when nothing prices a day of that
// kind on that date. per_diem_overnight_other is the one that ships unseeded —
// the agreement knows a single overnight rate — so a night somewhere that is
// not a hotel says this until an administrator enters the company's own figure.
//
// A claim abroad is not priced from the table at all, so it names the field that
// really is missing: its own day rate. Unreachable while parseAbroad requires
// one, and written out rather than left to say something untrue if it ever is.
func perDiemNoDayRate(claim store.ExpensesClaim, perDiemType string, date time.Time) string {
	if claim.Abroad {
		return "This travel claim is abroad and carries no abroadDayRate to pay the day at"
	}
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
func claimDays(claim store.ExpensesClaim, loc *time.Location) (time.Time, time.Time) {
	return businessDay(claim.DepartureAt, loc), businessDay(claim.ReturnAt, loc)
}

// A per diem day is the first kind whose price and whose validity depend on
// fields of the *claim* rather than on its own. So the claim has a second
// writer to answer for: PUT /claims/{id}, which may move the trip's window or
// change what a day abroad is paid at. The three functions below are that
// answer, and they run inside the claim's own row lock beside its lines.

// claimAfterEdit is the claim as the update is about to write it, in the shape
// the per diem rules read — they ask a claim what it *is*, and under the lock
// the row still holds what it was.
func claimAfterEdit(locked store.ExpensesClaim, p parsedClaim, dayRate pgtype.Numeric) store.ExpensesClaim {
	locked.Abroad = p.Abroad
	locked.AbroadDayRate = dayRate
	locked.AbroadCurrency = p.AbroadCurrency
	locked.DepartureAt = p.DepartureAt
	locked.ReturnAt = p.ReturnAt
	return locked
}

// claimPricingChanged reports whether an edit touched anything a per diem day
// is priced from: whether the trip is abroad, what a day abroad is paid at, and
// in which currency. Nothing else on a claim reaches a day's amount.
func claimPricingChanged(before, after store.ExpensesClaim) (bool, error) {
	if before.Abroad != after.Abroad || !sameCurrencyCode(before.AbroadCurrency, after.AbroadCurrency) {
		return true, nil
	}
	was, err := ratPtrFromNumeric(before.AbroadDayRate)
	if err != nil {
		return false, err
	}
	now, err := ratPtrFromNumeric(after.AbroadDayRate)
	if err != nil {
		return false, err
	}
	switch {
	case was == nil && now == nil:
		return false, nil
	case was == nil || now == nil:
		return true, nil
	}
	return was.Cmp(now) != 0, nil
}

// claimPricingField names the field of the edit that made a trip's days need
// repricing, so a refusal points at something the caller actually changed.
func claimPricingField(before, after store.ExpensesClaim) string {
	switch {
	case before.Abroad != after.Abroad:
		return "abroad"
	case !sameCurrencyCode(before.AbroadCurrency, after.AbroadCurrency):
		return "abroadCurrency"
	}
	return "abroadDayRate"
}

// sameCurrencyCode reports whether two optional currency codes are the same.
func sameCurrencyCode(a, b *string) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	}
	return *a == *b
}

// claimWindowMoved reports whether an edit moved either end of the trip.
func claimWindowMoved(before, after store.ExpensesClaim) bool {
	return !before.DepartureAt.Equal(after.DepartureAt) || !before.ReturnAt.Equal(after.ReturnAt)
}

// perDiemStrandedByWindow is the departureAt/returnAt refusal when narrowing a
// trip would leave days it already holds outside it.
//
// It **refuses** rather than deleting: a day somebody recorded is theirs, and a
// trip correction quietly taking money off a claim is the one outcome nobody
// would forgive. The message names every stranded date, so one round trip tells
// the caller exactly which days to remove first.
func perDiemStrandedByWindow(lines []store.ExpensesEntry, after store.ExpensesClaim, loc *time.Location) map[string][]string {
	from, to := claimDays(after, loc)
	var early, late []string
	for _, line := range lines {
		if line.Kind != kindPerDiem {
			continue
		}
		switch date := line.EntryDate.Time; {
		case date.Before(from):
			early = append(early, date.Format(time.DateOnly))
		case date.After(to):
			late = append(late, date.Format(time.DateOnly))
		}
	}
	var errs map[string][]string
	if len(early) > 0 {
		errs = withFieldError(errs, "departureAt", fmt.Sprintf(
			"This trip holds a per diem day on %s, which a departure of %s leaves outside it; remove %s first",
			strings.Join(early, ", "), from.Format(time.DateOnly), theDay(len(early))))
	}
	if len(late) > 0 {
		errs = withFieldError(errs, "returnAt", fmt.Sprintf(
			"This trip holds a per diem day on %s, which a return of %s leaves outside it; remove %s first",
			strings.Join(late, ", "), to.Format(time.DateOnly), theDay(len(late))))
	}
	return errs
}

// theDay is "it" or "them", so a refusal naming one date and one naming three
// are both sentences.
func theDay(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// repriceClaimPerDiem works out what every per diem day of a claim is worth
// once the claim's own money has changed — a trip flipped abroad or back, or an
// abroad day rate corrected. It reads this module's own table only, so it runs
// inside the claim's lock beside the lines it is about.
//
// A day it cannot price refuses the **whole** edit, naming the date and what is
// missing: flipping a trip back to domestic when the table prices no day of
// that type is a change the caller can undo, and half a claim repriced is not.
func repriceClaimPerDiem(ctx context.Context, txq *store.Queries, lines []store.ExpensesEntry,
	before, after store.ExpensesClaim, defaultCurrency string, now time.Time,
) (map[string][]string, error) {
	field := claimPricingField(before, after)
	for _, line := range lines {
		if line.Kind != kindPerDiem || line.PerDiemType == nil {
			continue
		}
		date := line.EntryDate.Time
		_, meals, err := perDiemStored(line)
		if err != nil {
			return nil, err
		}
		rates, missing, err := perDiemFiguresFor(ctx, txq, after, *line.PerDiemType, date, meals)
		if err != nil {
			return nil, err
		}
		if missing.any() {
			return fieldError(field, fmt.Sprintf(
				"This would leave the per diem day on %s unpriced: %s. Remove the day first, or leave the trip as it is",
				date.Format(time.DateOnly), unpricedReason(after, missing, *line.PerDiemType, date))), nil
		}
		amount, err := perDiemAmount(rates, meals)
		if err != nil {
			return nil, err
		}
		params := store.RepricePerDiemLineParams{
			ID:       line.ID,
			Currency: perDiemCurrency(after, defaultCurrency),
			Now:      now,
		}
		for _, conv := range []struct {
			value *big.Rat
			into  *pgtype.Numeric
		}{
			{rates.DayRate, &params.Rate},
			{rates.Breakfast, &params.MealBreakfastPercent},
			{rates.Lunch, &params.MealLunchPercent},
			{rates.Dinner, &params.MealDinnerPercent},
			{amount, &params.GrossAmount},
		} {
			if *conv.into, err = numericFromRatPtr(conv.value, moneyPlaces); err != nil {
				return nil, err
			}
		}
		if err := txq.RepricePerDiemLine(ctx, params); err != nil {
			return nil, fmt.Errorf("expenses: reprice a travel claim's per diem day: %w", err)
		}
	}
	return nil, nil
}

// unpricedReason is the half of the refusal above that says what is missing —
// the day's own rate, or a covered meal's percentage.
func unpricedReason(after store.ExpensesClaim, missing perDiemMissing, perDiemType string, date time.Time) string {
	if missing.DayRate {
		return perDiemNoDayRate(after, perDiemType, date)
	}
	return perDiemNoMealRate
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

// suggestedDay is one day the suggestion proposes: when its period starts, and
// which kind of day it is.
type suggestedDay struct {
	Date time.Time
	Type string
}

// perDiemPartPeriodHours is the six hours two different rules are written in
// terms of: the least a trip has to last before it earns anything at all, and —
// for the *remainder* after at least one full 24-hour period — how long what is
// left has to run to earn a day of its own, which is strictly longer.
const perDiemPartPeriodHours = 6 * time.Hour

// suggestPerDiem is design §4's day counting, and nothing else: two instants
// and whether the traveller slept away, in, a list of days out. It writes
// nothing, asks nothing and knows no rates — pricing the days it proposes is
// the handler's job, and recording them is the traveller's.
//
// The rule, in one sentence: **a trip of at least six hours earns a day, and an
// overnight trip earns one more for every full 24 hours plus a remainder longer
// than six.**
//
//   - Under six hours the trip earns nothing at all.
//   - Without an overnight it is one day, dated on the departure: day_6_12 up
//     to and including twelve hours, day_over_12 beyond. A trip of several days
//     that nobody slept away on is still one day: the traveller who did not
//     stay the night gets one day's rate, whatever the clock says.
//   - With an overnight it is one day per full 24-hour period from the
//     departure, plus one more when what is left over runs *strictly* longer
//     than six hours — and never fewer than one, so any overnight trip at all,
//     from six hours up, is a day. The periods are 24 hours from the departure
//     instant, never calendar midnights, and each day is dated on the UTC day
//     its own period starts.
//
// The six hours therefore reads two ways on purpose: it is inclusive as the
// threshold a whole trip has to clear (six hours exactly earns a day, with an
// overnight or without), and exclusive for the remainder after a full period
// (30 h 00 is one day, 30 h 01 is two). A trip that is short but slept away on
// is still a trip; six hours of leftover at the end of one is not another day.
//
// Every overnight day is proposed as overnight_hotel, the type the agreement
// prices; a traveller who stayed somewhere else changes it on the line.
func suggestPerDiem(departure, returns time.Time, overnight bool, loc *time.Location) []suggestedDay {
	duration := returns.Sub(departure)
	if duration < perDiemPartPeriodHours {
		return nil
	}
	if !overnight {
		dayType := perDiem6To12
		if duration > 12*time.Hour {
			dayType = perDiemOver12
		}
		return []suggestedDay{{Date: businessDay(departure, loc), Type: dayType}}
	}

	const period = 24 * time.Hour
	days := int(duration / period)
	switch {
	case days == 0:
		// Shorter than a full period. The trip has already cleared six hours
		// (above) and somebody slept away on it, so it is one day — the
		// remainder rule does not apply, because there is no period for it to
		// be the remainder of.
		days = 1
	case duration%period > perDiemPartPeriodHours:
		days++
	}
	out := make([]suggestedDay, 0, days)
	for i := range days {
		out = append(out, suggestedDay{
			Date: businessDay(departure.Add(time.Duration(i)*period), loc),
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
	claim, _, found, err := s.visibleClaim(ctx, q, c, req.Id, claimFigures{})
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

	days := suggestPerDiem(claim.DepartureAt, claim.ReturnAt, body.Overnight, c.zone())
	pricer, err := s.suggestionPricer(ctx, q, claim, days)
	if err != nil {
		return nil, err
	}
	out := make([]gen.ExpensesPerDiemSuggestedDay, 0, len(days))
	for _, day := range days {
		suggested := gen.ExpensesPerDiemSuggestedDay{
			EntryDate:   openapi_types.Date{Time: day.Date},
			PerDiemType: day.Type,
			Exists:      taken[day.Date.Format(time.DateOnly)],
		}
		// Priced with no meal covered: what the day is worth before the
		// traveller ticks anything off it. No meal means no meal percentage is
		// read at all — three lookups a day that would be multiplied by nothing.
		if rate, err := pricer.on(day.Date); err == nil {
			amount, err := perDiemAmount(perDiemRates{DayRate: rate}, perDiemMeals{})
			if err != nil {
				return nil, err
			}
			suggested.DayRate = ptrTo(floatOfRat(rate))
			suggested.Amount = ptrTo(floatOfRat(amount))
		} else if !errors.Is(err, errNoRate) {
			return nil, err
		}
		out = append(out, suggested)
	}
	return gen.PostExpensesClaimsByIdPerDiemSuggestion200JSONResponse(out), nil
}

// dayRatePricer prices every day of one suggestion from figures read **once**.
//
// The shape matters: the longest trip the contract allows is 366 days, and a
// lookup per day would be a round trip per day inside one request. Every day of
// one suggestion is of the same type, so the rows that price it are one read of
// one rate kind — or, abroad, the claim's own single figure — and which row is
// in force on each day is then decided in Go, exactly as rateFor decides it in
// SQL: the greatest valid_from on or before the day.
type dayRatePricer struct {
	// abroad is the claim's own day rate, which prices every day of a trip
	// abroad whatever its type. nil on a domestic trip.
	abroad *big.Rat
	// rows are one rate kind's rows, ascending by valid_from. nil abroad.
	rows []store.ExpensesRate
}

// on is the rate in force on date, errNoRate when nothing prices it.
func (p dayRatePricer) on(date time.Time) (*big.Rat, error) {
	if p.abroad != nil {
		return p.abroad, nil
	}
	for i := len(p.rows) - 1; i >= 0; i-- {
		if !p.rows[i].ValidFrom.Time.After(date) {
			return ratFromNumeric(p.rows[i].Value)
		}
	}
	return nil, errNoRate
}

// suggestionPricer reads what one suggestion needs to price its days: nothing
// at all when it proposes none, the claim's own rate abroad, and otherwise one
// query for the one rate kind every day of it shares.
func (s *server) suggestionPricer(ctx context.Context, q *store.Queries, claim store.ExpensesClaim,
	days []suggestedDay,
) (dayRatePricer, error) {
	if len(days) == 0 {
		return dayRatePricer{}, nil
	}
	if claim.Abroad {
		abroad, err := ratPtrFromNumeric(claim.AbroadDayRate)
		if err != nil {
			return dayRatePricer{}, err
		}
		return dayRatePricer{abroad: abroad}, nil
	}
	rows, err := q.RatesOfKind(ctx, perDiemRateKinds[days[0].Type])
	if err != nil {
		return dayRatePricer{}, fmt.Errorf("expenses: read the per diem rates: %w", err)
	}
	return dayRatePricer{rows: rows}, nil
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
