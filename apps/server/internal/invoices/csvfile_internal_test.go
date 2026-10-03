package invoices

import (
	"math/big"
	"testing"
)

// A cell (D5): a quote is doubled and the cell quoted; a separator, a CR or
// an LF quotes it; the guard puts an apostrophe before what a spreadsheet
// would read as a formula and then quotes as any cell; and an amount, never
// guarded, keeps its minus — a guarded -1,00 would be text.
func TestCSVCell(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		value string
		guard bool
		want  string
	}{
		{"Kunde AS", true, "Kunde AS"},
		{`Si "hei"`, true, `"Si ""hei"""`},
		{"a;b", true, `"a;b"`},
		{"linje 1\r\nlinje 2", true, "\"linje 1\r\nlinje 2\""},
		{"linje 1\nlinje 2", false, "\"linje 1\nlinje 2\""},
		{"=cmd", true, "'=cmd"},
		{"=a;b", true, `"'=a;b"`},
		{"@SUM(A1)", true, "'@SUM(A1)"},
		{"\tfane", true, "'\tfane"},
		{"\rretur", true, "\"'\rretur\""},
		{"-1,00", false, "-1,00"},
		{"-1,00", true, "'-1,00"},
		{"+47", false, "+47"},
	} {
		if got := csvCell(c.value, c.guard); got != c.want {
			t.Errorf("csvCell(%q, %v) = %q, want %q", c.value, c.guard, got, c.want)
		}
	}
}

// An amount (D5) is rounded to øre half away from zero and only then negated
// for a credit note, so a negated amount that rounds to nothing is 0,00,
// never -0,00.
func TestCSVAmount(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		value  *big.Rat
		negate bool
		want   string
	}{
		{big.NewRat(4, 1000), true, "0,00"},
		{big.NewRat(-4, 1000), false, "0,00"},
		{big.NewRat(0, 1), true, "0,00"},
		{big.NewRat(150045, 1000), false, "150,05"},
		{big.NewRat(150045, 1000), true, "-150,05"},
		{big.NewRat(-150045, 1000), false, "-150,05"},
		{big.NewRat(123450, 100), true, "-1234,50"},
	} {
		if got := csvAmount(c.value, c.negate); got != c.want {
			t.Errorf("csvAmount(%s, %v) = %q, want %q", c.value.FloatString(4), c.negate, got, c.want)
		}
	}
}
