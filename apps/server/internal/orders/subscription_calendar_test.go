package orders

import (
	"math/big"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/docmath"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// The periods run by the interval from the first of the start's month,
// months end on their last day — February too, in a leap year — and a start
// that is not the first makes a partial first period (orders design O43,
// §11).
func TestCalendar_Periods(t *testing.T) {
	t.Parallel()
	type span struct{ start, end time.Time }
	cases := []struct {
		name     string
		interval string
		start    time.Time
		through  time.Time
		want     []span
	}{
		{"31 January, monthly", intervalMonthly, day(2027, 1, 31), day(2027, 3, 1), []span{
			{day(2027, 1, 31), day(2027, 1, 31)}, {day(2027, 2, 1), day(2027, 2, 28)}, {day(2027, 3, 1), day(2027, 3, 31)},
		}},
		{"a leap February", intervalMonthly, day(2028, 1, 1), day(2028, 2, 1), []span{
			{day(2028, 1, 1), day(2028, 1, 31)}, {day(2028, 2, 1), day(2028, 2, 29)},
		}},
		{"quarterly from mid-February", intervalQuarterly, day(2027, 2, 15), day(2027, 8, 1), []span{
			{day(2027, 2, 15), day(2027, 4, 30)}, {day(2027, 5, 1), day(2027, 7, 31)}, {day(2027, 8, 1), day(2027, 10, 31)},
		}},
		{"half-yearly", intervalHalfYearly, day(2026, 7, 1), day(2027, 1, 1), []span{
			{day(2026, 7, 1), day(2026, 12, 31)}, {day(2027, 1, 1), day(2027, 6, 30)},
		}},
		{"yearly across a leap year", intervalYearly, day(2027, 3, 1), day(2028, 3, 1), []span{
			{day(2027, 3, 1), day(2028, 2, 29)}, {day(2028, 3, 1), day(2029, 2, 28)},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, ok := newCalendar(tc.interval, tc.start, nil)
			if !ok {
				t.Fatal("no calendar")
			}
			got := c.periodsThrough(tc.through)
			if len(got) != len(tc.want) {
				t.Fatalf("%d periods, want %d: %+v", len(got), len(tc.want), got)
			}
			for i, p := range got {
				if !p.start.Equal(tc.want[i].start) || !p.end.Equal(tc.want[i].end) {
					t.Errorf("period %d = %s–%s, want %s–%s", i, p.start.Format(time.DateOnly), p.end.Format(time.DateOnly),
						tc.want[i].start.Format(time.DateOnly), tc.want[i].end.Format(time.DateOnly))
				}
			}
		})
	}
}

// A period's day finds it, a period start is one, and the end keeps the
// periods after it out.
func TestCalendar_PeriodOnAndEnd(t *testing.T) {
	t.Parallel()
	end := day(2027, 3, 15)
	c, _ := newCalendar(intervalMonthly, day(2027, 1, 31), &end)
	if p, ok := c.periodOn(day(2027, 2, 14)); !ok || !p.start.Equal(day(2027, 2, 1)) || p.index != 1 {
		t.Errorf("14 February's period = %+v, %v; want February, index 1", p, ok)
	}
	if _, ok := c.periodOn(day(2027, 1, 30)); ok {
		t.Error("a day before the start has a period")
	}
	if _, ok := c.periodOn(day(2027, 4, 1)); ok {
		t.Error("a period after the end is the subscription's")
	}
	if !c.isPeriodStart(day(2027, 3, 1)) || c.isPeriodStart(day(2027, 3, 2)) || !c.isPeriodStart(day(2027, 1, 31)) {
		t.Error("period starts misjudged")
	}
	if got := c.periodsThrough(day(2030, 1, 1)); len(got) != 3 {
		t.Errorf("%d periods through 2030 with an end in March 2027, want 3", len(got))
	}
}

// In advance a period is due from its first day, in arrears from its last;
// nothing starting more than twelve months ahead is billable.
func TestCalendar_Due(t *testing.T) {
	t.Parallel()
	c, _ := newCalendar(intervalMonthly, day(2027, 1, 1), nil)
	jan := c.period(0)
	if !dueThrough(billingInAdvance, jan, day(2027, 1, 1)) || dueThrough(billingInAdvance, jan, day(2026, 12, 31)) {
		t.Error("in advance is due from the period's first day")
	}
	if dueThrough(billingInArrears, jan, day(2027, 1, 30)) || !dueThrough(billingInArrears, jan, day(2027, 1, 31)) {
		t.Error("in arrears is due from the period's last day")
	}
	if got := latestBillableStart(day(2026, 9, 12)); !got.Equal(day(2027, 9, 12)) {
		t.Errorf("the latest billable start = %s, want twelve months ahead", got.Format(time.DateOnly))
	}
}

// A partial first period bills its days as a fraction of the full period,
// to øre, as a unit price docmath reproduces exactly; a full one bills the
// line as it is.
func TestCalendar_Proration(t *testing.T) {
	t.Parallel()
	r := func(s string) *big.Rat { v, _ := new(big.Rat).SetString(s); return v }
	c, _ := newCalendar(intervalMonthly, day(2027, 1, 31), nil)
	first := c.period(0)
	if !first.partial() || first.fraction().Cmp(big.NewRat(1, 31)) != 0 {
		t.Fatalf("31 January's period: partial %v, fraction %s; want 1/31", first.partial(), first.fraction())
	}
	a := periodAmountOf(first, r("1"), r("310.00"), r("0"))
	if a.amount.Cmp(r("10.00")) != 0 {
		t.Errorf("1/31 of 310.00 = %s, want 10.00", a.amount.FloatString(2))
	}
	for _, tc := range []struct{ qty, price, discount string }{
		{"3", "199.00", "10"}, {"7", "33.335", "0"}, {"0.5", "1249.50", "12.5"}, {"12", "0.99", "0"},
	} {
		got := periodAmountOf(first, r(tc.qty), r(tc.price), r(tc.discount))
		full := docmath.Line(r(tc.qty), r(tc.price), r(tc.discount)).Net
		want := docmath.Round2(new(big.Rat).Mul(full, big.NewRat(1, 31)))
		if got.amount.Cmp(want) != 0 || docmath.Line(got.quantity, got.unitPrice, got.discount).Net.Cmp(want) != 0 {
			t.Errorf("%s × %s less %s%%: %s, docmath %s; want %s", tc.qty, tc.price, tc.discount, got.amount.FloatString(2),
				docmath.Line(got.quantity, got.unitPrice, got.discount).Net.FloatString(2), want.FloatString(2))
		}
		if !hasPlaces(got.unitPrice, pricePlaces) {
			t.Errorf("unit price %s has more than eight decimals", got.unitPrice.FloatString(10))
		}
	}
	full := periodAmountOf(c.period(1), r("3"), r("199.00"), r("10"))
	if full.discount.Cmp(r("10")) != 0 || full.amount.Cmp(r("537.30")) != 0 {
		t.Errorf("a full period = %+v, want the line as it is", full)
	}
}
