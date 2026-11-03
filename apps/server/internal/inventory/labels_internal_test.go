package inventory

import (
	"fmt"
	"regexp"
	"slices"
	"testing"

	"github.com/johnfercher/maroto/v2/pkg/core"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
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
		doc, err := layoutLabels(labels[:tc.n], sheet)
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
		pdf, err := renderLabels(labels[:tc.n], sheet)
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
