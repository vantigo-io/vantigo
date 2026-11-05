package orders

// The period calendar of a subscription (orders design O43, §11): pure
// functions over civil days, each a midnight UTC, with no database and no
// clock in them.
//
// The periods run from the start date by the interval, anchored on the first
// of the start's month: period k covers [anchor + k·n months, anchor +
// (k+1)·n months − 1 day], so a month ends on its last day whatever the
// start's day was. The first period begins on the start date itself, and is
// partial when the start is not the first of a month: its lines are
// pro-rated by days, as a fraction of the full period it is part of, to øre
// — the only pro-rating of this phase. A subscription started on 31 January,
// monthly, has a first period from 31 January to 31 January at 1/31 of the
// month.
//
// A period is due through a date in advance when its first day is on or
// before it, in arrears when its last day is; and never when it starts more
// than twelve months after today (bokføringsforskriften § 5-2-6).

import (
	"math/big"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/docmath"
)

// The intervals and the billing of a subscription's plan (O42).
const (
	intervalMonthly    = "monthly"
	intervalQuarterly  = "quarterly"
	intervalHalfYearly = "half_yearly"
	intervalYearly     = "yearly"

	billingInAdvance = "in_advance"
	billingInArrears = "in_arrears"
)

// advanceLimitMonths is how far ahead of today a period may start and still
// be billed (O43).
const advanceLimitMonths = 12

// intervalMonths is how many months a period of interval covers; 0 for an
// interval that is none of the four.
func intervalMonths(interval string) int {
	switch interval {
	case intervalMonthly:
		return 1
	case intervalQuarterly:
		return 3
	case intervalHalfYearly:
		return 6
	case intervalYearly:
		return 12
	}
	return 0
}

// civil is t's date as a midnight UTC.
func civil(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// period is one billing period, its first and last day inclusive.
type period struct {
	index      int
	start, end time.Time
	// full is the first day of the full period this one is part of: start,
	// but for a partial first period.
	full time.Time
}

// partial reports whether the period is a partial first period.
func (p period) partial() bool { return !p.full.Equal(p.start) }

// fraction is the share of its full period a period bills: 1, or for a
// partial first period its days over the full period's days.
func (p period) fraction() *big.Rat {
	if !p.partial() {
		return big.NewRat(1, 1)
	}
	return big.NewRat(daysBetween(p.start, p.end)+1, daysBetween(p.full, p.end)+1)
}

// daysBetween is the number of days from a to b.
func daysBetween(a, b time.Time) int64 {
	return int64(b.Sub(a).Hours()+0.5) / 24
}

// calendar is a subscription's plan as the periods see it.
type calendar struct {
	months int
	start  time.Time
	// end is the plan's end date; a period starting after it is not one of
	// the subscription's. nil is no end.
	end *time.Time
}

// newCalendar is the calendar of a plan; ok false for an interval that is
// none of the four.
func newCalendar(interval string, start time.Time, end *time.Time) (calendar, bool) {
	months := intervalMonths(interval)
	if months == 0 {
		return calendar{}, false
	}
	c := calendar{months: months, start: civil(start)}
	if end != nil {
		e := civil(*end)
		c.end = &e
	}
	return c, true
}

// anchor is the first of the start's month.
func (c calendar) anchor() time.Time {
	return time.Date(c.start.Year(), c.start.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// period is the k-th period, from 0.
func (c calendar) period(k int) period {
	full := c.anchor().AddDate(0, k*c.months, 0)
	p := period{index: k, start: full, full: full, end: full.AddDate(0, c.months, -1)}
	if k == 0 {
		p.start = c.start
	}
	return p
}

// within reports whether a period is one of the subscription's: it starts on
// or before the end, when there is one.
func (c calendar) within(p period) bool {
	return c.end == nil || !p.start.After(*c.end)
}

// periodOn is the period day falls in, and whether there is one: a day
// before the start, or in a period after the end, has none.
func (c calendar) periodOn(day time.Time) (period, bool) {
	day = civil(day)
	if day.Before(c.start) {
		return period{}, false
	}
	a := c.anchor()
	months := (day.Year()-a.Year())*12 + int(day.Month()) - int(a.Month())
	p := c.period(months / c.months)
	return p, c.within(p)
}

// isPeriodStart reports whether day is the first day of one of the
// subscription's periods.
func (c calendar) isPeriodStart(day time.Time) bool {
	p, ok := c.periodOn(day)
	return ok && p.start.Equal(civil(day))
}

// periodsThrough is every period of the subscription starting on or before
// last, from the first, in order.
func (c calendar) periodsThrough(last time.Time) []period {
	var out []period
	for k := 0; ; k++ {
		p := c.period(k)
		if p.start.After(last) || !c.within(p) {
			return out
		}
		out = append(out, p)
	}
}

// dueThrough reports whether a period billed as billing is due through
// through (O43): its first day on or before it in advance, its last day in
// arrears.
func dueThrough(billing string, p period, through time.Time) bool {
	if billing == billingInArrears {
		return !p.end.After(civil(through))
	}
	return !p.start.After(civil(through))
}

// latestBillableStart is the last day a period may start on and be billed,
// today: twelve months ahead (O43).
func latestBillableStart(today time.Time) time.Time {
	return civil(today).AddDate(0, advanceLimitMonths, 0)
}

// periodAmount is what one line bills for one period (O37, O43): its own
// quantity, unit price and discount for a full period; for a partial one,
// docmath's net of the full period times the period's fraction, rounded to
// øre, billed as the line's quantity at a unit price of that amount ÷ the
// quantity at eight decimals and no discount — so docmath, which Invoices
// builds its line by, reproduces the pro-rated amount exactly. Where eight
// decimals cannot carry it, the period bills a quantity of 1 at the amount.
type periodAmount struct {
	quantity, unitPrice, discount, amount *big.Rat
}

func periodAmountOf(p period, quantity, unitPrice, discount *big.Rat) periodAmount {
	full := docmath.Line(quantity, unitPrice, discount)
	if !p.partial() {
		return periodAmount{quantity: quantity, unitPrice: unitPrice, discount: discount, amount: full.Net}
	}
	amount := docmath.Round2(new(big.Rat).Mul(full.Net, p.fraction()))
	price := roundTo(new(big.Rat).Quo(amount, quantity), pricePlaces)
	if docmath.Line(quantity, price, new(big.Rat)).Net.Cmp(amount) == 0 {
		return periodAmount{quantity: quantity, unitPrice: price, discount: new(big.Rat), amount: amount}
	}
	return periodAmount{quantity: big.NewRat(1, 1), unitPrice: amount, discount: new(big.Rat), amount: amount}
}

// roundTo is v at places decimals, half away from zero.
func roundTo(v *big.Rat, places int) *big.Rat {
	r, ok := new(big.Rat).SetString(v.FloatString(places))
	if !ok {
		panic("orders: not a decimal: " + v.FloatString(places))
	}
	return r
}
