package inventory

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johnfercher/maroto/v2/pkg/core"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// pdfPages counts a PDF's page objects (/Type /Page, not /Pages): how many
// sheets a label PDF has, read without decoding its content.
var pdfPages = regexp.MustCompile(`/Type /Page[^s]`)

// barcodeTokens is every value the layout hands maroto's barcode component —
// boombuler's Code 128 encoder's input — in layout order. boombuler has no
// decoder, so the tokens are read from the layout, not the bytes.
func barcodeTokens[N interface {
	GetData() core.Structure
	GetNexts() []N
}](n N, out *[]string) {
	if d := n.GetData(); d.Type == "barcode" {
		*out = append(*out, fmt.Sprint(d.Value))
	}
	for _, next := range n.GetNexts() {
		barcodeTokens(next, out)
	}
}

// TestLabels_Sheets is I7's PDF: 21 labels to an L7160 sheet and 14 to an
// L7163, each label's barcode the location's LOC token, in the order given.
func TestLabels_Sheets(t *testing.T) {
	t.Parallel()
	labels := make([]label, 0, 30)
	for i := range 30 {
		labels = append(labels, label{token: locationToken(int64(1001 + i)), fullCode: fmt.Sprintf("OSL-A-%02d", i+1), name: "Hylle æøå"})
	}
	for _, tc := range []struct {
		layout gen.InventoryLabelLayout
		n      int
		pages  int
	}{
		{gen.L7160, 21, 1}, {gen.L7160, 22, 2}, {gen.L7160, 30, 2}, {gen.L7163, 14, 1}, {gen.L7163, 15, 2}, {gen.L7163, 30, 3}, {gen.L7163, 1, 1},
	} {
		sheet := labelSheets[tc.layout]
		doc, err := layoutLabels(labels[:tc.n], sheet, "Location labels")
		if err != nil {
			t.Fatalf("%s × %d: %v", tc.layout, tc.n, err)
		}
		var tokens []string
		barcodeTokens(doc.GetStructure(), &tokens)
		want := make([]string, 0, tc.n)
		for _, l := range labels[:tc.n] {
			want = append(want, l.token)
		}
		if !slices.Equal(tokens, want) {
			t.Errorf("%s × %d: tokens = %v, want %v", tc.layout, tc.n, tokens, want)
		}
		pdf, err := renderLabels(labels[:tc.n], sheet, "Location labels")
		if err != nil {
			t.Fatalf("%s × %d: render: %v", tc.layout, tc.n, err)
		}
		if got := len(pdfPages.FindAll(pdf, -1)); got != tc.pages {
			t.Errorf("%s × %d: %d pages, want %d", tc.layout, tc.n, got, tc.pages)
		}
	}
	if s := labelSheets[gen.L7160]; s.perSheet() != 21 {
		t.Errorf("L7160 holds %d, want 21", s.perSheet())
	}
	if s := labelSheets[gen.L7163]; s.perSheet() != 14 {
		t.Errorf("L7163 holds %d, want 14", s.perSheet())
	}
}

// TestLotLabels_Layout is M73's label: the LOT<id> token as the barcode,
// the number large, the SKU and name, the expiry — else the best-before.
func TestLotLabels_Layout(t *testing.T) {
	t.Parallel()
	expiry := pgtype.Date{Time: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC), Valid: true}
	lots := []store.InventoryLot{
		{ID: 1001, Number: "A-1", Sku: "MILK-1", ProductName: "Milk", Expiry: expiry},
		{ID: 1002, Number: "SN0001", Sku: "PUMP-1", ProductName: "Pump", BestBefore: expiry},
		{ID: 1003, Number: "B-7", Sku: "BOLT-M8", ProductName: "Bolt"},
	}
	var labels []label
	for _, l := range lots {
		labels = append(labels, lotLabelOf(l))
	}
	want := []label{
		{token: "LOT1001", fullCode: "A-1", name: "MILK-1 · Milk", extra: "EXP 2026-12-31"},
		{token: "LOT1002", fullCode: "SN0001", name: "PUMP-1 · Pump", extra: "BB 2026-12-31"},
		{token: "LOT1003", fullCode: "B-7", name: "BOLT-M8 · Bolt"},
	}
	if !slices.Equal(labels, want) {
		t.Errorf("labels = %+v, want %+v", labels, want)
	}
	doc, err := layoutLabels(labels, labelSheets[gen.L7160], "Lot labels")
	if err != nil {
		t.Fatal(err)
	}
	var tokens []string
	barcodeTokens(doc.GetStructure(), &tokens)
	if !slices.Equal(tokens, []string{"LOT1001", "LOT1002", "LOT1003"}) {
		t.Errorf("tokens = %v", tokens)
	}
	pdf, err := renderLabels(labels, labelSheets[gen.L7160], "Lot labels")
	if err != nil {
		t.Fatal(err)
	}
	if got := len(pdfPages.FindAll(pdf, -1)); got != 1 {
		t.Errorf("%d pages, want 1", got)
	}
}

// TestLotLabels_NameOnOneLine is the SKU · name line of a lot label cut to
// one line with an ellipsis, so the expiry below it never overlaps it; a
// short one prints whole.
func TestLotLabels_NameOnOneLine(t *testing.T) {
	t.Parallel()
	for _, layout := range []gen.InventoryLabelLayout{gen.L7160, gen.L7163} {
		sheet := labelSheets[layout]
		if got := oneLine("MILK-1 · Milk", sheet.nameSize, sheet.width-4); got != "MILK-1 · Milk" {
			t.Errorf("%s: short name = %q, want it whole", layout, got)
		}
		long := "WIDGET-XL-2000 · " + strings.Repeat("Extra wide stainless steel bracket ", 4)
		got := oneLine(long, sheet.nameSize, sheet.width-4)
		if !strings.HasSuffix(got, "…") || len([]rune(got)) >= len([]rune(long)) || !strings.HasPrefix(got, "WIDGET-XL-2000 · ") {
			t.Errorf("%s: long name = %q, want it cut with an ellipsis", layout, got)
		}
		// Upper-case W is among the widest glyphs: about 60 of them pass 59.5 mm at 8 pt.
		if got := oneLine(strings.Repeat("W", 60), sheet.nameSize, sheet.width-4); !strings.HasSuffix(got, "…") {
			t.Errorf("%s: wide name = %q, want it cut", layout, got)
		}
	}
}
