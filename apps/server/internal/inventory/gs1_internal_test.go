package inventory

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/code128"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// GS1-128 labels (manufacturing phase 2 M93): the GTIN read and padded, the
// element string in its order with one FNC1 at its start, and the string
// decoded back from the bars boombuler's encoder draws, with a table of the
// encoder's own symbols.

func TestGS1_GTIN14(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"96385074", "00000096385074", true},       // GTIN-8
		{"036000291452", "00036000291452", true},   // GTIN-12 (UPC-A)
		{"4006381333931", "04006381333931", true},  // GTIN-13
		{"10012345678902", "10012345678902", true}, // GTIN-14
		{" 4006381333931 ", "04006381333931", true},
		{"4006381333932", "", false}, // a wrong check digit
		{"400638133393", "", false},  // twelve digits whose check digit is wrong
		{"40063813339311", "", false},
		{"ABC-123", "", false},
		{"1234567", "", false},
		{"", "", false},
	} {
		got, ok := gtin14(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("gtin14(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// day is a date column's value.
func day(y int, m time.Month, d int) pgtype.Date {
	return pgtype.Date{Time: time.Date(y, m, d, 0, 0, 0, 0, time.UTC), Valid: true}
}

// code128Table is the encoder's symbol of each value, read from the bars it
// draws for strings whose symbols are known: a set C digit pair is its own
// value, FNC1 is 102, a switch to set B 100 and to set C 99, and the three
// starts 103–105 — every value a label's string needs. boombuler has no decoder, so the test makes one of its own
// table.
func code128Table(t *testing.T) map[string]int {
	t.Helper()
	table := map[string]int{}
	symbol := func(content string, at, value int) {
		bars := barsOf(t, content)
		table[bars[at*11:at*11+11]] = value
	}
	for v := range 100 {
		symbol(fmt.Sprintf("%02d%02d", v, v), 1, v) // four digits open set C
	}
	symbol("0000", 0, 105)       // start C
	symbol("a", 0, 104)          // start B
	symbol("ñ0000", 1, 102)      // FNC1
	symbol("ñ0000a", 4, 100)     // code B
	symbol("a0000", 2, 99)       // code C
	symbol("\x00", 0, 103)       // start A
	symbol("a0000aa", 6, 'a'-32) // set B's a again, as a check of the table
	// Every value but 101 (a switch to set A, which no label needs).
	if len(table) != 105 {
		t.Fatalf("the table has %d symbols, want 105", len(table))
	}
	return table
}

// barsOf is the content's bars as the encoder draws them, a 1 per dark
// module.
func barsOf(t *testing.T, content string) string {
	t.Helper()
	code, err := code128.Encode(content)
	if err != nil {
		t.Fatalf("encode %q: %v", content, err)
	}
	return bars(code)
}

func bars(code barcode.Barcode) string {
	var b strings.Builder
	for x := range code.Bounds().Dx() {
		if r, _, _, _ := code.At(x, 0).RGBA(); r == 0 {
			b.WriteByte('1')
		} else {
			b.WriteByte('0')
		}
	}
	return b.String()
}

// decode reads a Code 128's bars back: its start, its data — FNC1 as 'ñ' —
// and its check symbol verified.
func decode(t *testing.T, table map[string]int, bars string) (start int, data string) {
	t.Helper()
	if (len(bars)-13)%11 != 0 {
		t.Fatalf("%d modules is not a Code 128", len(bars))
	}
	n := (len(bars) - 13) / 11
	values := make([]int, n)
	for i := range n {
		v, ok := table[bars[i*11:i*11+11]]
		if !ok {
			t.Fatalf("symbol %d is not in the table", i)
		}
		values[i] = v
	}
	sum := values[0]
	for i := 1; i < n-1; i++ {
		sum += i * values[i]
	}
	if sum%103 != values[n-1] {
		t.Fatalf("the check symbol is %d, want %d", values[n-1], sum%103)
	}
	set := values[0]
	var out strings.Builder
	for _, v := range values[1 : n-1] {
		switch {
		case v == 102:
			out.WriteRune(code128.FNC1)
		case set == 105 && v == 100:
			set = 104
		case set == 104 && v == 99:
			set = 105
		case set == 105:
			fmt.Fprintf(&out, "%02d", v)
		case set == 104:
			out.WriteByte(byte(32 + v))
		default:
			t.Fatalf("symbol %d in set %d", v, set)
		}
	}
	return values[0], out.String()
}

func TestGS1_ElementStrings(t *testing.T) {
	t.Parallel()
	table := code128Table(t)
	lot := store.InventoryLot{ID: 1001, Number: "L-1001/a", Sku: "BREAD-1", ProductName: "Bread",
		ProductionDate: day(2026, 11, 1), Expiry: day(2026, 12, 31), BestBefore: day(2026, 12, 20)}
	serial := store.InventoryLot{ID: 1002, Number: "SN0042", Serial: true, Sku: "PUMP-1", ProductName: "Pump", BestBefore: day(2027, 1, 15)}
	bare := store.InventoryLot{ID: 1003, Number: "99"}
	for _, tc := range []struct {
		name           string
		lot            store.InventoryLot
		gtin           string
		data, readable string
	}{
		// The GTIN-13 padded; (11) and (17); the best-before left out beside
		// an expiry; the lot last.
		{"a lot", lot, "04006381333931", "ñ0104006381333931112611011726123110L-1001/a", "(01)04006381333931(11)261101(17)261231(10)L-1001/a"},
		// A serial with a best-before and no expiry: (15), then (21) last.
		{"a serial", serial, "00000096385074", "ñ01000000963850741527011521SN0042", "(01)00000096385074(15)270115(21)SN0042"},
		// No dates: the GTIN and the lot alone, the lot all digits.
		{"no dates", bare, "10012345678902", "ñ01100123456789021099", "(01)10012345678902(10)99"},
	} {
		data, readable := gs1Strings(tc.gtin, tc.lot)
		if data != tc.data || readable != tc.readable {
			t.Errorf("%s: %q, %q; want %q, %q", tc.name, data, readable, tc.data, tc.readable)
			continue
		}
		if strings.Count(data, string(code128.FNC1)) != 1 || !strings.HasPrefix(data, string(code128.FNC1)) {
			t.Errorf("%s: FNC1 is not once, at the start: %q", tc.name, data)
		}
		start, decoded := decode(t, table, barsOf(t, data))
		// A GS1-128 opens with start C and FNC1, and reads back whole.
		if start != 105 || decoded != data {
			t.Errorf("%s: decoded start %d, %q; want start C, %q", tc.name, start, decoded, data)
		}
	}
}

func TestGS1_Labels(t *testing.T) {
	t.Parallel()
	gtin := "4006381333931"
	bad := "4006381333932"
	lot := store.InventoryLot{ID: 1001, Number: "B1", Sku: "BREAD-1", ProductName: "Bread", Expiry: day(2026, 12, 31)}
	gs1 := gs1LabelOf(lot, &gtin)
	if gs1.token != "ñ010400638133393117261231"+"10B1" || gs1.readable != "(01)04006381333931(17)261231(10)B1" || gs1.fullCode != "B1" ||
		gs1.extra != "EXP 2026-12-31" {
		t.Errorf("the GS1 label = %+v", gs1)
	}
	for _, barcode := range []*string{nil, &bad} {
		if l := gs1LabelOf(lot, barcode); l.token != "LOT1001" || l.readable != noGTINNote {
			t.Errorf("a label without a valid GTIN = %+v", l)
		}
	}
	// maroto hands the element string to the encoder as it is, and draws it.
	labels := []label{gs1, gs1LabelOf(lot, nil)}
	doc, err := layoutLabels(labels, labelSheets[gen.L7163], "GS1-128 lot labels")
	if err != nil {
		t.Fatal(err)
	}
	var tokens []string
	barcodeTokens(doc.GetStructure(), &tokens)
	if len(tokens) != 2 || tokens[0] != gs1.token || tokens[1] != "LOT1001" {
		t.Errorf("tokens = %q", tokens)
	}
	if _, err := renderLabels(labels, labelSheets[gen.L7160], "GS1-128 lot labels"); err != nil {
		t.Errorf("render: %v", err)
	}
}
