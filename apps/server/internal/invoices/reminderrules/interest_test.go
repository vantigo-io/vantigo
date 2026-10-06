package reminderrules

import (
	"testing"
)

// seg is a segment as the test writes it.
type seg struct{ from, to, rate, base string }

// D8's late interest, pinned: simple, from the day after E to L inclusive,
// each day on the principal open at its end less the payments of the days
// before (a credit note lowers its own day, a payment the day after —
// reading 7), split at every rate row, credit note and payment, actual/365,
// rounded to the øre once.
func TestReminderRules_Interest(t *testing.T) {
	t.Parallel()
	interestOn := func(l string) Input {
		in := baseInput(l)
		in.Settings.LateInterest = true
		return in
	}
	for _, c := range []struct {
		name  string
		in    Input
		total string
		from  string
		segs  []seg
	}{
		{
			// 15 × 12.00 + 15 × 12.25 on 10 000 = 36 375 / 365
			name: "no credit", in: interestOn("2026-07-15"), total: "99.66", from: "2026-06-16",
			segs: []seg{{"2026-06-16", "2026-06-30", "12.00", "10000"}, {"2026-07-01", "2026-07-15", "12.25", "10000"}},
		},
		{
			// … + 4 days on 10 000 and 11 on 6 000 at 12.25 = 30 985 / 365
			name: "a credit note of 4 000 issued 5 Jul lowers its own day",
			in: func() Input {
				in := interestOn("2026-07-15")
				in.Credits = []Credit{{IssueDate: day("2026-07-05"), Gross: rat("4000")}}
				return in
			}(),
			total: "84.89", from: "2026-06-16",
			segs: []seg{
				{"2026-06-16", "2026-06-30", "12.00", "10000"},
				{"2026-07-01", "2026-07-04", "12.25", "10000"},
				{"2026-07-05", "2026-07-15", "12.25", "6000"},
			},
		},
		{
			// … + 5 days on 10 000 and 10 on 6 000 = 31 475 / 365 = 86.2329;
			// rounding each segment would give 49.32 + 16.78 + 20.14 = 86.24.
			name: "a payment of 4 000 paid 5 Jul lowers the day after",
			in: func() Input {
				in := interestOn("2026-07-15")
				in.Payments = []Payment{{PaidOn: day("2026-07-05"), Amount: rat("4000")}}
				return in
			}(),
			total: "86.23", from: "2026-06-16",
			segs: []seg{
				{"2026-06-16", "2026-06-30", "12.00", "10000"},
				{"2026-07-01", "2026-07-05", "12.25", "10000"},
				{"2026-07-06", "2026-07-15", "12.25", "6000"},
			},
		},
		{
			// Paid in full on 5 Jul: its own day bears interest, none after.
			name: "interest on the day of payment",
			in: func() Input {
				in := interestOn("2026-07-15")
				in.Payments = []Payment{{PaidOn: day("2026-07-05"), Amount: rat("10000")}}
				return in
			}(),
			total: "66.10", from: "2026-06-16",
			segs: []seg{{"2026-06-16", "2026-06-30", "12.00", "10000"}, {"2026-07-01", "2026-07-05", "12.25", "10000"}},
		},
		{
			// 16 × 12.25 + 15 × 12.00 on 10 000 = 37 600 / 365
			name: "across 1 January",
			in: func() Input {
				in := interestOn("2026-01-15")
				in.Invoice.IssueDate, in.Invoice.DueDate = day("2025-12-01"), day("2025-12-15")
				in.Deliveries[0] = day("2025-12-01")
				return in
			}(),
			total: "103.01", from: "2025-12-16",
			segs: []seg{{"2025-12-16", "2025-12-31", "12.25", "10000"}, {"2026-01-01", "2026-01-15", "12.00", "10000"}},
		},
		{
			name: "a Saturday due date runs from the day after the Monday",
			in: func() Input {
				in := interestOn("2026-07-15")
				in.Invoice.DueDate = day("2026-06-13")
				return in
			}(),
			total: "99.66", from: "2026-06-16",
			segs: []seg{{"2026-06-16", "2026-06-30", "12.00", "10000"}, {"2026-07-01", "2026-07-15", "12.25", "10000"}},
		},
		{
			name: "an interest waiver never restarts it",
			in: func() Input {
				in := interestOn("2026-07-15")
				in.Waivers = []Waiver{{ID: 1, ReminderID: 1, Kind: WaiverInterest, Amount: rat("20")}}
				return in
			}(),
			total: "99.66", from: "2026-06-16",
			segs: []seg{{"2026-06-16", "2026-06-30", "12.00", "10000"}, {"2026-07-01", "2026-07-15", "12.25", "10000"}},
		},
		{
			// 15 × 12.00 + 4 × 12.25 on 10 000 = 22 900 / 365; from 5 Jul the base is
			// below zero and bears nothing, never a negative interest.
			name: "over-credited",
			in: func() Input {
				in := interestOn("2026-07-15")
				in.Credits = []Credit{{IssueDate: day("2026-07-05"), Gross: rat("12000")}}
				return in
			}(),
			total: "62.74", from: "2026-06-16",
			segs: []seg{{"2026-06-16", "2026-06-30", "12.00", "10000"}, {"2026-07-01", "2026-07-04", "12.25", "10000"}},
		},
		{
			// Settled in 2026-H2 and asked about in 2027-H1, which has no row: the
			// days after the settlement need no rate.
			name: "settled before a half-year without its row",
			in: func() Input {
				in := interestOn("2027-01-10")
				in.Payments = []Payment{{PaidOn: day("2026-07-05"), Amount: rat("10000")}}
				return in
			}(),
			total: "66.10", from: "2026-06-16",
			segs: []seg{{"2026-06-16", "2026-06-30", "12.00", "10000"}, {"2026-07-01", "2026-07-05", "12.25", "10000"}},
		},
		{name: "settled before it began", in: func() Input {
			in := interestOn("2027-01-10")
			in.Payments = []Payment{{PaidOn: day("2026-06-14"), Amount: rat("10000")}}
			return in
		}(), total: "0"},
		{name: "the last day of 2026", in: interestOn("2026-12-31"), total: "666.85", from: "2026-06-16",
			segs: []seg{{"2026-06-16", "2026-06-30", "12.00", "10000"}, {"2026-07-01", "2026-12-31", "12.25", "10000"}}},
		{name: "on E itself, nothing", in: interestOn("2026-06-15"), total: "0"},
		{name: "the first day", in: interestOn("2026-06-16"), total: "3.29", from: "2026-06-16",
			segs: []seg{{"2026-06-16", "2026-06-16", "12.00", "10000"}}},
		{name: "off", in: baseInput("2026-07-15"), total: "0"},
		{name: "no_charges", in: func() Input {
			in := interestOn("2026-07-15")
			in.Mode = ModeNoCharges
			return in
		}(), total: "0"},
		{name: "not delivered", in: func() Input {
			in := interestOn("2026-07-15")
			in.Deliveries = nil
			return in
		}(), total: "0"},
	} {
		total, from, segs, outdated := Interest(c.in)
		eqRat(t, c.name+": interest", total, c.total)
		if outdated != nil {
			t.Errorf("%s: outdated %+v", c.name, *outdated)
		}
		switch {
		case c.from == "" && from != nil:
			t.Errorf("%s: from %s, want none", c.name, from)
		case c.from != "" && (from == nil || !from.Equal(day(c.from))):
			t.Errorf("%s: from %v, want %s", c.name, from, c.from)
		}
		if len(segs) != len(c.segs) {
			t.Errorf("%s: %d segments %+v, want %d", c.name, len(segs), segs, len(c.segs))
			continue
		}
		for i, s := range segs {
			w := c.segs[i]
			if !s.From.Equal(day(w.from)) || !s.To.Equal(day(w.to)) || s.Rate.Cmp(rat(w.rate)) != 0 || s.Base.Cmp(rat(w.base)) != 0 {
				t.Errorf("%s: segment %d = %s–%s %s on %s, want %+v", c.name, i,
					s.From.Format("2006-01-02"), s.To.Format("2006-01-02"), s.Rate.FloatString(2), s.Base.FloatString(2), w)
			}
		}
	}

	// Exactly on 1 January the new half-year is needed.
	if _, _, _, outdated := Interest(interestOn("2027-01-01")); outdated == nil || *outdated != (OutdatedRate{KindLateInterest, "2027-H1"}) {
		t.Errorf("on 1 Jan 2027: outdated %+v, want late_interest_percent 2027-H1", outdated)
	}
	// A half-year without its row answers outdated, never a stale rate.
	in := interestOn("2027-01-10")
	if _, _, _, outdated := Interest(in); outdated == nil || *outdated != (OutdatedRate{KindLateInterest, "2027-H1"}) {
		t.Errorf("into 2027-H1: outdated %+v, want late_interest_percent 2027-H1", outdated)
	}
}
