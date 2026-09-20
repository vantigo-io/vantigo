package expenses

import (
	"math/big"
	"testing"
	"time"
)

// The per diem arithmetic and the day counting of design §4, tested where they
// are written rather than through the API: both are pure functions — one over
// exact decimals, one over two instants — and neither needs a database, a
// request or a claim to say what it says.

// ratOrNil is an exact decimal from its text, nil for "the table prices none".
func ratOrNil(t *testing.T, text string) *big.Rat {
	t.Helper()
	if text == "" {
		return nil
	}
	return rat(t, text)
}

// Every type of day against every combination of covered meals, at the rates
// the product ships with: 397, 736 and 1012 kroner a day, and 20 %, 30 % and
// 50 % off for a breakfast, a lunch and a dinner somebody else paid for.
func TestPerDiemAmount_IsTheDayRateLessEveryCoveredMeal(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		dayRate                    string
		breakfast, lunch, dinner   string
		bCovered, lCovered, dCover bool
		want                       string
	}{
		// per_diem_6_12, 397.00
		"6–12 h, nothing covered":      {"397.00", "20", "30", "50", false, false, false, "397.00"},
		"6–12 h, breakfast":            {"397.00", "20", "30", "50", true, false, false, "317.60"},
		"6–12 h, lunch":                {"397.00", "20", "30", "50", false, true, false, "277.90"},
		"6–12 h, dinner":               {"397.00", "20", "30", "50", false, false, true, "198.50"},
		"6–12 h, breakfast and lunch":  {"397.00", "20", "30", "50", true, true, false, "198.50"},
		"6–12 h, breakfast and dinner": {"397.00", "20", "30", "50", true, false, true, "119.10"},
		"6–12 h, lunch and dinner":     {"397.00", "20", "30", "50", false, true, true, "79.40"},
		"6–12 h, all three":            {"397.00", "20", "30", "50", true, true, true, "0.00"},

		// per_diem_over_12, 736.00
		"over 12 h, nothing covered":      {"736.00", "20", "30", "50", false, false, false, "736.00"},
		"over 12 h, breakfast":            {"736.00", "20", "30", "50", true, false, false, "588.80"},
		"over 12 h, lunch":                {"736.00", "20", "30", "50", false, true, false, "515.20"},
		"over 12 h, dinner":               {"736.00", "20", "30", "50", false, false, true, "368.00"},
		"over 12 h, breakfast and lunch":  {"736.00", "20", "30", "50", true, true, false, "368.00"},
		"over 12 h, breakfast and dinner": {"736.00", "20", "30", "50", true, false, true, "220.80"},
		"over 12 h, lunch and dinner":     {"736.00", "20", "30", "50", false, true, true, "147.20"},
		"over 12 h, all three":            {"736.00", "20", "30", "50", true, true, true, "0.00"},

		// per_diem_overnight_hotel, 1012.00
		"a hotel night, nothing covered":      {"1012.00", "20", "30", "50", false, false, false, "1012.00"},
		"a hotel night, breakfast":            {"1012.00", "20", "30", "50", true, false, false, "809.60"},
		"a hotel night, lunch":                {"1012.00", "20", "30", "50", false, true, false, "708.40"},
		"a hotel night, dinner":               {"1012.00", "20", "30", "50", false, false, true, "506.00"},
		"a hotel night, breakfast and lunch":  {"1012.00", "20", "30", "50", true, true, false, "506.00"},
		"a hotel night, breakfast and dinner": {"1012.00", "20", "30", "50", true, false, true, "303.60"},
		"a hotel night, lunch and dinner":     {"1012.00", "20", "30", "50", false, true, true, "202.40"},
		"a hotel night, all three":            {"1012.00", "20", "30", "50", true, true, true, "0.00"},

		// A claim abroad is the very same arithmetic over the claim's own rate.
		"abroad, the claim's own rate":            {"90.00", "20", "30", "50", false, false, false, "90.00"},
		"abroad, the claim's own rate, breakfast": {"90.00", "20", "30", "50", true, false, false, "72.00"},

		// One rounding, half away from zero: 123.45 less half of it is 61.725.
		"rounded half up":          {"123.45", "20", "50", "50", false, true, false, "61.73"},
		"rounded half up, a krone": {"1.05", "20", "50", "50", false, true, false, "0.53"},
		// 100.01 less a third: rounding the deduction first would answer 66.67.
		"rounded once, at the end": {"100.01", "33.33", "30", "50", true, false, false, "66.68"},

		// Percentages an administrator set over a hundred between them: the day
		// pays nothing, never less than nothing.
		"never below zero": {"397.00", "60.00", "50.00", "50.00", true, true, false, "0.00"},

		// A percentage the table does not price deducts nothing. A *covered*
		// meal on such a day is refused before it ever reaches here; a meal
		// nobody covered simply has no figure to record.
		"no percentage in the table": {"397.00", "", "", "", false, false, false, "397.00"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rates := perDiemRates{
				DayRate:   rat(t, tc.dayRate),
				Breakfast: ratOrNil(t, tc.breakfast),
				Lunch:     ratOrNil(t, tc.lunch),
				Dinner:    ratOrNil(t, tc.dinner),
			}
			meals := perDiemMeals{Breakfast: tc.bCovered, Lunch: tc.lCovered, Dinner: tc.dCover}
			got := perDiemAmount(rates, meals)
			if got.Cmp(rat(t, tc.want)) != 0 {
				t.Errorf("perDiemAmount(%s, %+v) = %s, want %s", tc.dayRate, meals, got.FloatString(4), tc.want)
			}
		})
	}
}

// A covered meal the table prices no deduction for deducts nothing here — the
// refusal is the caller's, not the arithmetic's, so this function can never be
// the thing that silently pays a whole day out.
func TestPerDiemAmount_ACoveredMealWithNoPercentageDeductsNothing(t *testing.T) {
	t.Parallel()
	got := perDiemAmount(perDiemRates{DayRate: rat(t, "397.00")}, perDiemMeals{Breakfast: true, Dinner: true})
	if got.Cmp(rat(t, "397.00")) != 0 {
		t.Errorf("perDiemAmount with no percentages = %s, want 397.00", got.FloatString(2))
	}
}

// instant is one of the two ends of a trip.
func instant(t *testing.T, text string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, text)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	return v
}

// dayOf renders one suggested day as "YYYY-MM-DD type", so a table says the
// whole answer on one line.
func dayOf(d suggestedDay) string { return d.Date.Format(time.DateOnly) + " " + d.Type }

func daysOf(days []suggestedDay) []string {
	out := make([]string, 0, len(days))
	for _, d := range days {
		out = append(out, dayOf(d))
	}
	return out
}

// The day counting of design §4 and Global Constraints, boundary by boundary.
// The periods are 24 hours from the departure instant, never calendar
// midnights, and each line is dated on the day its own period starts.
func TestSuggestPerDiem_CountsTheDaysOfATrip(t *testing.T) {
	t.Parallel()
	const departure = "2026-03-09T07:00:00Z"
	for name, tc := range map[string]struct {
		departure, returns string
		overnight          bool
		want               []string
	}{
		"under six hours earns nothing":     {departure, "2026-03-09T12:59:00Z", false, nil},
		"six hours exactly is a 6–12 day":   {departure, "2026-03-09T13:00:00Z", false, []string{"2026-03-09 day_6_12"}},
		"twelve hours exactly is still one": {departure, "2026-03-09T19:00:00Z", false, []string{"2026-03-09 day_6_12"}},
		"a minute past twelve hours is the long day": {
			departure, "2026-03-09T19:01:00Z", false, []string{"2026-03-09 day_over_12"},
		},
		"a day and a half with no overnight is still one long day": {
			departure, "2026-03-10T13:00:00Z", false, []string{"2026-03-09 day_over_12"},
		},

		"under six hours with an overnight earns nothing too": {
			departure, "2026-03-09T12:59:00Z", true, nil,
		},
		// The six hours reads two ways on purpose: inclusive as the threshold a
		// whole trip has to clear, exclusive for the remainder after a full
		// 24-hour period. So six hours exactly with an overnight is a day, while
		// six hours left over at the end of a longer trip is not another one.
		"six hours exactly with an overnight is one day": {
			departure, "2026-03-09T13:00:00Z", true, []string{"2026-03-09 overnight_hotel"},
		},
		"a night away shorter than a day is one day": {
			departure, "2026-03-10T03:00:00Z", true, []string{"2026-03-09 overnight_hotel"},
		},
		"twenty-four hours exactly is one day": {
			departure, "2026-03-10T07:00:00Z", true, []string{"2026-03-09 overnight_hotel"},
		},
		"twenty-six hours is one day, the two hours over ignored": {
			departure, "2026-03-10T09:00:00Z", true, []string{"2026-03-09 overnight_hotel"},
		},
		"thirty hours exactly is one day, six hours over being not more than six": {
			departure, "2026-03-10T13:00:00Z", true, []string{"2026-03-09 overnight_hotel"},
		},
		"a minute past thirty hours is two days": {
			departure, "2026-03-10T13:01:00Z", true,
			[]string{"2026-03-09 overnight_hotel", "2026-03-10 overnight_hotel"},
		},
		"thirty-one hours is two days": {
			departure, "2026-03-10T14:00:00Z", true,
			[]string{"2026-03-09 overnight_hotel", "2026-03-10 overnight_hotel"},
		},
		"forty-eight hours exactly is two days": {
			departure, "2026-03-11T07:00:00Z", true,
			[]string{"2026-03-09 overnight_hotel", "2026-03-10 overnight_hotel"},
		},
		"a minute past fifty-four hours is three days": {
			departure, "2026-03-11T13:01:00Z", true,
			[]string{"2026-03-09 overnight_hotel", "2026-03-10 overnight_hotel", "2026-03-11 overnight_hotel"},
		},

		// The periods run from the departure instant, so a trip that leaves
		// late at night has its second period start late the next night — not
		// at the midnight in between.
		"the periods are not calendar midnights": {
			"2026-03-09T23:00:00Z", "2026-03-12T00:00:00Z", true,
			[]string{"2026-03-09 overnight_hotel", "2026-03-10 overnight_hotel"},
		},
		// A return before the departure cannot be saved, and is nothing here.
		"a return before the departure earns nothing": {
			departure, "2026-03-08T07:00:00Z", false, nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := daysOf(suggestPerDiem(instant(t, tc.departure), instant(t, tc.returns), tc.overnight))
			if len(got) != len(tc.want) {
				t.Fatalf("suggestPerDiem = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("day %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// The suggestion reads the instants as they were entered and dates each day in
// UTC — exactly the derivation claimUnit makes for the period lock, so a
// suggested day can never fall on a different day from the one the claim is
// judged on.
func TestSuggestPerDiem_DatesADayInUTC(t *testing.T) {
	t.Parallel()
	// 01:00 on the 10th in Oslo is 23:00 on the 9th in UTC.
	departure := instant(t, "2026-03-10T01:00:00+02:00")
	days := suggestPerDiem(departure, departure.Add(8*time.Hour), false)
	if len(days) != 1 || days[0].Date.Format(time.DateOnly) != "2026-03-09" {
		t.Errorf("suggestPerDiem = %v, want one day on 2026-03-09", daysOf(days))
	}
	if days[0].Date.Location() != time.UTC {
		t.Errorf("the day is in %v, want UTC", days[0].Date.Location())
	}
}
