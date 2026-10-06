package reminderrules

import (
	"testing"
	"time"
)

// seedRates is D6's seed, every value read on Lovdata (R4 §2.11), with ids so
// a letter's rate rows can be named.
func seedRates() []Rate {
	rows := []struct {
		kind, from, value string
	}{
		{KindLateInterest, "2024-01-01", "12.50"},
		{KindLateInterest, "2024-07-01", "12.50"},
		{KindLateInterest, "2025-01-01", "12.50"},
		{KindLateInterest, "2025-07-01", "12.25"},
		{KindLateInterest, "2026-01-01", "12.00"},
		{KindLateInterest, "2026-07-01", "12.25"},
		{KindCompensation, "2024-01-01", "470"},
		{KindCompensation, "2024-07-01", "460"},
		{KindCompensation, "2025-01-01", "470"},
		{KindCompensation, "2025-07-01", "460"},
		{KindCompensation, "2026-01-01", "460"},
		{KindCompensation, "2026-07-01", "430"},
		{KindInkassosats, "2019-01-01", "700"},
		{KindInkassosats, "2026-01-01", "750"},
	}
	rates := make([]Rate, 0, len(rows))
	for i, r := range rows {
		rates = append(rates, Rate{ID: int64(i + 1), Kind: r.kind, ValidFrom: day(r.from), Value: rat(r.value)})
	}
	return rates
}

// The regime is judged per letter, on its date (D6): NULL or after the
// letter's day is 1988, set and reached is 2026.
func TestReminderRules_RegimePerLetterDate(t *testing.T) {
	t.Parallel()
	from := day("2027-01-01")
	for _, c := range []struct {
		name   string
		from   *time.Time
		letter string
		want   Regime
	}{
		{"not set", nil, "2027-06-01", Regime1988},
		{"set after the letter", &from, "2026-12-31", Regime1988},
		{"set on the letter's day", &from, "2027-01-01", Regime2026},
		{"set before the letter", &from, "2027-03-01", Regime2026},
	} {
		if got := RegimeOn(Settings{Inkassolov2026From: c.from}, day(c.letter)); got != c.want {
			t.Errorf("%s: RegimeOn = %q, want %q", c.name, got, c.want)
		}
	}
}

// A row is in force from its valid_from until the next row of its kind; the
// boundaries are 1 January and 1 July for the half-yearly kinds.
func TestReminderRules_RateOn(t *testing.T) {
	t.Parallel()
	rates := seedRates()
	for _, c := range []struct {
		kind, on, want string
	}{
		{KindLateInterest, "2026-06-30", "12.00"},
		{KindLateInterest, "2026-07-01", "12.25"},
		{KindLateInterest, "2025-12-31", "12.25"},
		{KindLateInterest, "2026-01-01", "12.00"},
		{KindCompensation, "2026-06-30", "460"},
		{KindCompensation, "2026-07-01", "430"},
		{KindInkassosats, "2025-12-31", "700"},
		{KindInkassosats, "2026-01-01", "750"},
		{KindInkassosats, "2031-05-05", "750"},
	} {
		r, ok := RateOn(rates, c.kind, day(c.on))
		if !ok || r.Value.Cmp(rat(c.want)) != 0 || r.Kind != c.kind {
			t.Errorf("RateOn(%s, %s) = %v %v, want %s", c.kind, c.on, r.Value, ok, c.want)
		}
	}
	if r, ok := RateOn(rates, KindLateInterest, day("2023-12-31")); ok {
		t.Errorf("RateOn before the first row = %v, want none", r.Value)
	}
}

// Reading 5: a half-year a letter needs without a row starting on its first
// day is outdated — after the last row, in a gap between rows, and before the
// first row; the inkassosats, not half-yearly, only needs a row in force.
func TestReminderRules_OutdatedRates(t *testing.T) {
	t.Parallel()
	rates := seedRates()
	gap := []Rate{
		{ID: 1, Kind: KindLateInterest, ValidFrom: day("2026-01-01"), Value: rat("12.00")},
		{ID: 2, Kind: KindLateInterest, ValidFrom: day("2027-01-01"), Value: rat("12.50")},
	}
	for _, c := range []struct {
		name     string
		rates    []Rate
		kind     string
		from, to string
		want     *OutdatedRate
	}{
		{"covered", rates, KindLateInterest, "2026-06-16", "2026-07-15", nil},
		{"after the last row", rates, KindLateInterest, "2026-12-01", "2027-01-10", &OutdatedRate{KindLateInterest, "2027-H1"}},
		{"a gap between rows", gap, KindLateInterest, "2026-06-01", "2027-02-01", &OutdatedRate{KindLateInterest, "2026-H2"}},
		{"before the first row", rates, KindLateInterest, "2023-11-01", "2024-02-01", &OutdatedRate{KindLateInterest, "2023-H2"}},
		{"exactly on 1 January", rates, KindLateInterest, "2026-12-01", "2027-01-01", &OutdatedRate{KindLateInterest, "2027-H1"}},
		{"through 31 December", rates, KindLateInterest, "2026-12-01", "2026-12-31", nil},
		{"the compensation's own half-year", rates, KindCompensation, "2027-01-10", "2027-01-10", &OutdatedRate{KindCompensation, "2027-H1"}},
		{"the compensation in force", rates, KindCompensation, "2026-12-31", "2026-12-31", nil},
		{"the inkassosats needs only a row", rates, KindInkassosats, "2031-01-10", "2031-01-10", nil},
		{"no inkassosats yet", rates, KindInkassosats, "2018-06-01", "2018-06-01", &OutdatedRate{KindInkassosats, "2018-H1"}},
	} {
		got := Outdated(c.rates, c.kind, day(c.from), day(c.to))
		switch {
		case c.want == nil && got != nil:
			t.Errorf("%s: Outdated = %+v, want none", c.name, *got)
		case c.want != nil && (got == nil || *got != *c.want):
			t.Errorf("%s: Outdated = %+v, want %+v", c.name, got, *c.want)
		}
	}
}
