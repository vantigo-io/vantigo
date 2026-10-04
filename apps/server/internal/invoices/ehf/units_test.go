package ehf_test

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/invoices/ehf"
)

// Every word of D5's table maps to its UNECE Recommendation 20 code (EHF and
// KID design D5), one case per entry.
func TestUnitCode_EveryTableEntry(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ unit, want string }{
		{"stk", "C62"}, {"pcs", "C62"}, {"piece", "C62"},
		{"time", "HUR"}, {"timer", "HUR"}, {"h", "HUR"}, {"hour", "HUR"}, {"hours", "HUR"},
		{"min", "MIN"},
		{"dag", "DAY"}, {"day", "DAY"},
		{"uke", "WEE"}, {"week", "WEE"},
		{"mnd", "MON"}, {"month", "MON"},
		{"år", "ANN"}, {"year", "ANN"},
		{"kg", "KGM"},
		{"g", "GRM"},
		{"m", "MTR"},
		{"m2", "MTK"}, {"m²", "MTK"},
		{"m3", "MTQ"}, {"m³", "MTQ"},
		{"l", "LTR"}, {"liter", "LTR"}, {"litre", "LTR"},
		{"km", "KMT"},
		{"pakke", "XPK"}, {"pack", "XPK"}, {"pk", "XPK"},
		{"sett", "SET"}, {"set", "SET"},
		{"kWh", "KWH"},
	} {
		if got := ehf.UnitCode(c.unit); got != c.want {
			t.Errorf("UnitCode(%q) = %q, want %q", c.unit, got, c.want)
		}
	}
}

// The words are matched trimmed, case-insensitively and with trailing
// punctuation stripped: "stk." is "stk".
func TestUnitCode_TrimsCaseAndTrailingPunctuation(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ unit, want string }{
		{"stk.", "C62"}, {" Stk ", "C62"}, {"TIMER", "HUR"}, {"t.", "C62"},
		{"mnd.", "MON"}, {"KWH", "KWH"}, {"År", "ANN"}, {"kg,", "KGM"}, {"pk.)", "XPK"},
	} {
		if got := ehf.UnitCode(c.unit); got != c.want {
			t.Errorf("UnitCode(%q) = %q, want %q", c.unit, got, c.want)
		}
	}
}

// An empty unit and an unknown word are C62 (one); "t" is deliberately not
// mapped — hour or tonne — so it falls back too.
func TestUnitCode_FallsBackToC62(t *testing.T) {
	t.Parallel()
	for _, unit := range []string{"", "   ", "t", "T", "tonn", "lass", "..."} {
		if got := ehf.UnitCode(unit); got != "C62" {
			t.Errorf("UnitCode(%q) = %q, want C62", unit, got)
		}
	}
}

// KnownUnitCode is the invariant's table: every code UnitCode can answer, and
// nothing else.
func TestKnownUnitCode(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"C62", "HUR", "MIN", "DAY", "WEE", "MON", "ANN", "KGM", "GRM", "MTR", "MTK", "MTQ", "LTR", "KMT", "XPK", "SET", "KWH"} {
		if !ehf.KnownUnitCode(code) {
			t.Errorf("KnownUnitCode(%q) = false", code)
		}
	}
	for _, code := range []string{"", "TNE", "hur", "EA"} {
		if ehf.KnownUnitCode(code) {
			t.Errorf("KnownUnitCode(%q) = true", code)
		}
	}
}
