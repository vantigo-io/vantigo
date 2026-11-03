package inventory

import (
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
)

// TestCodes is the code rules (I1, I2, I7): upper-cased, 1–16 of A–Z and 0–9,
// never a LOC token; and a token read back.
func TestCodes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		raw, code string
		ok        bool
	}{
		{" osl ", "OSL", true},
		{"a1", "A1", true},
		{"ABCDEFGHIJKLMNOP", "ABCDEFGHIJKLMNOP", true},
		{"ABCDEFGHIJKLMNOPQ", "ABCDEFGHIJKLMNOPQ", false},
		{"", "", false},
		{"A-1", "A-1", false},
		{"A 1", "A 1", false},
		{"ø", "Ø", false},
		{"loc12", "LOC12", false},
		{"LOC", "LOC", true},
		{"LOCA", "LOCA", true},
	} {
		code := normalizeCode(tc.raw)
		if code != tc.code || (codeMessage(code) == "") != tc.ok {
			t.Errorf("%q: code %q valid %v, want %q valid %v", tc.raw, code, codeMessage(code) == "", tc.code, tc.ok)
		}
	}
	if got := joinFullCode("OSL-A", "03"); got != "OSL-A-03" {
		t.Errorf("joinFullCode = %q", got)
	}
	if got := rePrefixed("OSL-A-03-2", "OSL-A", "BGO-B-7"); got != "BGO-B-7-03-2" {
		t.Errorf("rePrefixed = %q", got)
	}
	if id, ok := parseLocationToken(" loc1042 "); !ok || id != 1042 || locationToken(id) != "LOC1042" {
		t.Errorf("token = %d %v", id, ok)
	}
	for _, bad := range []string{"LOC", "LOC0", "LOCX1", "1042", "OSL-1"} {
		if _, ok := parseLocationToken(bad); ok {
			t.Errorf("%q read as a token", bad)
		}
	}
}

// TestBulkExpansion is I5's grid: padding from the start's width, letter
// ranges, the prefix, and the refusals.
func TestBulkExpansion(t *testing.T) {
	t.Parallel()
	s := func(v string) *string { return &v }
	levels, f := expandLevels([]gen.InventoryBulkLevel{
		{Kind: gen.InventoryLocationKindAisle, From: "a", To: s("c")},
		{Kind: gen.InventoryLocationKindRack, From: "08", To: s("11"), Prefix: s("r")},
		{Kind: gen.InventoryLocationKindShelf, From: "1"},
	})
	if f.failed() {
		t.Fatalf("expand: %v", f.errs)
	}
	if !slices.Equal(levels[0].codes, []string{"A", "B", "C"}) || !slices.Equal(levels[1].codes, []string{"R08", "R09", "R10", "R11"}) ||
		!slices.Equal(levels[2].codes, []string{"1"}) {
		t.Errorf("levels = %+v", levels)
	}
	if n := bulkCount(levels); n != 3+12+12 {
		t.Errorf("count = %d, want 27", n)
	}
	plan := planBulk("OSL", levels)
	if len(plan[2]) != 12 || plan[2][0].fullCode != "OSL-A-R08-1" || plan[2][0].parent != "OSL-A-R08" || plan[2][11].fullCode != "OSL-C-R11-1" {
		t.Errorf("plan = %+v", plan[2])
	}
	if levels, _ := expandLevels([]gen.InventoryBulkLevel{{Kind: gen.InventoryLocationKindBin, From: "1", To: s("100")}}); len(levels[0].codes) != 100 || levels[0].codes[99] != "100" || levels[0].codes[0] != "1" {
		t.Errorf("1–100 = %v…", levels[0].codes[:3])
	}

	for name, tc := range map[string]struct {
		levels []gen.InventoryBulkLevel
		code   string
	}{
		"backwards":       {[]gen.InventoryBulkLevel{{Kind: gen.InventoryLocationKindBin, From: "9", To: s("1")}}, codeCodeInvalid},
		"mixed":           {[]gen.InventoryBulkLevel{{Kind: gen.InventoryLocationKindBin, From: "A", To: s("9")}}, codeCodeInvalid},
		"two letters":     {[]gen.InventoryBulkLevel{{Kind: gen.InventoryLocationKindBin, From: "AA", To: s("AC")}}, codeCodeInvalid},
		"token":           {[]gen.InventoryBulkLevel{{Kind: gen.InventoryLocationKindBin, From: "1", To: s("3"), Prefix: s("LOC")}}, codeCodeInvalid},
		"too long":        {[]gen.InventoryBulkLevel{{Kind: gen.InventoryLocationKindBin, From: "1", Prefix: s("ABCDEFGHIJKLMNO1")}}, codeCodeInvalid},
		"one level 1001":  {[]gen.InventoryBulkLevel{{Kind: gen.InventoryLocationKindBin, From: "0", To: s("1000")}}, codeBulkTooLarge},
		"grid of 1010":    {[]gen.InventoryBulkLevel{{Kind: gen.InventoryLocationKindRack, From: "1", To: s("10")}, {Kind: gen.InventoryLocationKindBin, From: "1", To: s("100")}}, codeBulkTooLarge},
		"huge range":      {[]gen.InventoryBulkLevel{{Kind: gen.InventoryLocationKindBin, From: "1", To: s("9999999999999999")}}, codeBulkTooLarge},
		"site kind":       {[]gen.InventoryBulkLevel{{Kind: gen.InventoryLocationKindSite, From: "1"}}, ""},
		"four levels":     {make([]gen.InventoryBulkLevel, 4), ""},
		"punctuated code": {[]gen.InventoryBulkLevel{{Kind: gen.InventoryLocationKindBin, From: "A-1"}}, codeCodeInvalid},
	} {
		_, f := expandLevels(tc.levels)
		if !f.failed() || f.code != tc.code {
			t.Errorf("%s: failed %v code %q (%v), want code %q", name, f.failed(), f.code, f.errs, tc.code)
		}
	}
	// Exactly 1000 is allowed.
	if levels, f := expandLevels([]gen.InventoryBulkLevel{{Kind: gen.InventoryLocationKindRack, From: "1", To: s("10")}, {Kind: gen.InventoryLocationKindBin, From: "1", To: s("99")}}); f.failed() || bulkCount(levels) != 1000 {
		t.Errorf("10 × 99 + 10: %v %d", f.errs, bulkCount(levels))
	}
}

// TestQuantities is I11: exact decimals from the double the wire carries,
// three decimals at most, never rounded, within numeric(14,3).
func TestQuantities(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		v    float64
		rule quantityRule
		want string // the decimal, or "" when refused
	}{
		{0.1, positiveQuantity, "0.100"},
		{0.3, positiveQuantity, "0.300"},
		{1.005, positiveQuantity, "1.005"},
		{99999999999.999, positiveQuantity, "99999999999.999"},
		{100000000000, positiveQuantity, ""},
		{1.2345, positiveQuantity, ""},
		{0.0001, positiveQuantity, ""},
		{0, positiveQuantity, ""},
		{-1, positiveQuantity, ""},
		{-2.5, signedQuantity, "-2.500"},
		{0, signedQuantity, ""},
		{0, nonNegativeQuantity, "0.000"},
		{-0.001, nonNegativeQuantity, ""},
		{math.NaN(), positiveQuantity, ""},
		{math.Inf(1), positiveQuantity, ""},
	} {
		r, msg := parseQuantity(tc.v, tc.rule)
		got := ""
		if msg == "" {
			got = r.FloatString(quantityPlaces)
		}
		if got != tc.want {
			t.Errorf("parseQuantity(%v, %d) = %q (%s), want %q", tc.v, tc.rule, got, msg, tc.want)
		}
	}
	// 0.1 + 0.2 is 0.3 exactly through the numeric round trip.
	a, _ := parseQuantity(0.1, positiveQuantity)
	b, _ := parseQuantity(0.2, positiveQuantity)
	n, err := numericFromRat(a.Add(a, b))
	if err != nil || ratFromNumeric(n).FloatString(3) != "0.300" || wireQuantity(ratFromNumeric(n)) != 0.3 {
		t.Errorf("0.1 + 0.2 = %v, %v", ratFromNumeric(n), err)
	}
	for v, want := range map[string]string{"34.000": "34", "1.500": "1.5", "0.125": "0.125", "0": "0", "-0.000": "0"} {
		if got := decimalText(ratFromText(v)); got != want {
			t.Errorf("decimalText(%s) = %s, want %s", v, got, want)
		}
	}
}

// TestCursor is the movement list's cursor, read back as it was written.
func TestCursor(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 3, 12, 30, 15, 123456000, time.UTC)
	c := formatCursor(at, 1042)
	got, id, ok := parseCursor(c)
	if !ok || !got.Equal(at) || id != 1042 || strings.ContainsAny(c, "+/=") {
		t.Errorf("cursor %q = %v %d %v", c, got, id, ok)
	}
	if _, _, ok := parseCursor("x"); ok {
		t.Error("x read as a cursor")
	}
	if ws := weekStart(time.Date(2026, 11, 1, 23, 30, 0, 0, time.UTC), time.UTC); !ws.Equal(time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("week of Sunday 1 November = %v, want Monday 26 October", ws)
	}
}
