package staff

import "testing"

// Care days per calendar year (ftrl. § 9-6, Revision 2 Q7b): the children
// counted are those under twelve and the chronically ill or disabled ones
// (plan reading 29 — no child counted twice); 10 for one or two, 15 for three
// or more, doubled for a sole carer (20, 30); plus 10 per chronically ill
// child, 20 for a sole carer.
func TestCareDayEntitlement(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name         string
		under12, ill int
		sole         bool
		want         int
	}{
		{"no children", 0, 0, false, 0},
		{"no children, a sole carer", 0, 0, true, 0},
		{"one child", 1, 0, false, 10},
		{"two children", 2, 0, false, 10},
		{"three children", 3, 0, false, 15},
		{"a sole carer of one", 1, 0, true, 20},
		{"a sole carer of three", 3, 0, true, 30},
		{"one chronically ill child", 0, 1, false, 20},
		{"one chronically ill child, a sole carer", 0, 1, true, 40},
		{"three children of whom one chronically ill", 2, 1, false, 25},
	} {
		if got := careDayEntitlement(c.under12, c.ill, c.sole); got != c.want {
			t.Errorf("%s: entitlement = %d, want %d", c.name, got, c.want)
		}
	}
}

// The employer pays the first ten days itself and is refunded from day
// eleven (ftrl. § 9-8): the year's used days split at ten.
func TestCareDaySplit(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ used, employer, beyond int }{{0, 0, 0}, {7, 7, 0}, {10, 10, 0}, {13, 10, 3}} {
		if e, b := careDaySplit(c.used); e != c.employer || b != c.beyond {
			t.Errorf("%d used: employer %d, beyond %d, want %d and %d", c.used, e, b, c.employer, c.beyond)
		}
	}
}

// The four weeks' employment care days need (ftrl. § 9-8, review L10) count
// from the start of the unbroken employment a day lies in: an employment
// that began the day after another ended continues it; a gap of a day
// breaks it.
func TestUnbrokenStart(t *testing.T) {
	t.Parallel()
	open := func(first string) daySpan { return daySpan{First: day(t, first), Last: day(t, "9999-12-31")} }
	for _, c := range []struct {
		name        string
		employments []daySpan
		on          string
		want        string
	}{
		{"one employment", []daySpan{open("2026-09-01")}, "2026-09-14", "2026-09-01"},
		{"the day after another ended", []daySpan{selfDays(t, "2026-01-01", "2026-08-31"), open("2026-09-01")}, "2026-09-14", "2026-01-01"},
		{"a day's gap", []daySpan{selfDays(t, "2026-01-01", "2026-08-30"), open("2026-09-01")}, "2026-09-14", "2026-09-01"},
		{"overlapping ones", []daySpan{open("2026-09-01"), selfDays(t, "2025-03-01", "2026-09-10")}, "2026-09-14", "2025-03-01"},
		{"no employment that day", []daySpan{selfDays(t, "2026-01-01", "2026-08-31")}, "2026-09-14", ""},
	} {
		got := unbrokenStart(c.employments, day(t, c.on))
		if (got == nil) != (c.want == "") || (got != nil && !got.Equal(day(t, c.want))) {
			t.Errorf("%s: start = %v, want %q", c.name, got, c.want)
		}
	}
}
