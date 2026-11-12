package inventory

import (
	"bytes"
	"context"
	_ "embed" // the label font
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/code"
	"github.com/johnfercher/maroto/v2/pkg/components/col"
	"github.com/johnfercher/maroto/v2/pkg/components/page"
	"github.com/johnfercher/maroto/v2/pkg/components/row"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/config"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/fontrepository"
	"github.com/johnfercher/maroto/v2/pkg/props"
	"github.com/phpdave11/gofpdf"
	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// Location labels (design I7, §13.2): a PDF of A4 sheets of Avery L7160 (3 ×
// 7 labels of 63.5 × 38.1 mm) or L7163 (2 × 7 of 99.1 × 38.1 mm), filled row
// by row in full-code order. Each label carries the location's LOC token —
// "LOC" and its id, which survives renames and re-parenting and scans back on
// any keyboard layout — as a Code 128 barcode across its top, the full code
// large below it, and the name, when the location has one, under that. With
// subtree, every active location below each one asked for is printed too;
// a location asked for is printed whatever its status. At most 1000 labels
// (labels_too_many); a location that does not exist is a 400.
//
// The layout is maroto's, as Invoices' PDFs are, with the same embedded Noto
// Sans (fonts/LICENSE) and fixed dates. maroto draws each barcode as an
// image from boombuler's Code 128 encoder; gofpdf numbers the images in map
// order, so two renderings of several labels may differ in object order,
// never in what they print.

var (
	//go:embed fonts/NotoSans-Regular.ttf
	notoSansRegular []byte
	//go:embed fonts/NotoSans-Bold.ttf
	notoSansBold []byte
)

const labelFontFamily = "noto-sans"

// labelDate is the fixed creation and modification date of every label PDF.
var labelDate = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

func init() {
	gofpdf.SetDefaultCatalogSort(true)
	gofpdf.SetDefaultModificationDate(labelDate)
}

// maxLabels is the most labels one request prints.
const maxLabels = 1000

// labelSheet is an Avery sheet's geometry on A4, in millimetres, and how a
// label is laid out on it.
type labelSheet struct {
	columns, rows      int
	width, height      float64 // a label
	left, top, bottom  float64 // the sheet's margins
	gap                float64 // between two labels of a row
	barcodePercent     float64 // the barcode's width, of the label's
	codeSize, nameSize float64 // points
}

// labelSheets are the two layouts (§13.2), as Avery specifies them.
var labelSheets = map[gen.InventoryLabelLayout]labelSheet{
	gen.L7160: {columns: 3, rows: 7, width: 63.5, height: 38.1, left: 7.2, top: 15.15, bottom: 10, gap: 2.5,
		barcodePercent: 80, codeSize: 13, nameSize: 8},
	gen.L7163: {columns: 2, rows: 7, width: 99.1, height: 38.1, left: 4.65, top: 15.15, bottom: 10, gap: 2.5,
		barcodePercent: 60, codeSize: 16, nameSize: 9},
}

// perSheet is how many labels one sheet holds.
func (l labelSheet) perSheet() int { return l.columns * l.rows }

// label is what one label prints: the token as the barcode, the code large
// below it — a location's full code, a lot's number — and up to two lines
// under that.
type label struct {
	token    string
	fullCode string
	name     string
	extra    string
}

// labelOf is a location's label.
func labelOf(l store.InventoryLocation) label {
	out := label{token: locationToken(l.ID), fullCode: l.FullCode}
	if l.Name != nil {
		out.name = *l.Name
	}
	return out
}

// tenths is millimetres as maroto's grid units, which are tenths of a
// millimetre here.
func tenths(mm float64) int { return int(math.Round(mm * 10)) }

// layoutLabels lays the labels out on sheets, their pages ready to generate.
func layoutLabels(labels []label, sheet labelSheet, title string) (core.Maroto, error) {
	fonts, err := fontrepository.New().
		AddUTF8FontFromBytes(labelFontFamily, fontstyle.Normal, notoSansRegular).
		AddUTF8FontFromBytes(labelFontFamily, fontstyle.Bold, notoSansBold).
		Load()
	if err != nil {
		return nil, fmt.Errorf("inventory: load the label font: %w", err)
	}
	rowWidth := sheet.width*float64(sheet.columns) + sheet.gap*float64(sheet.columns-1)
	cfg := config.NewBuilder().
		WithCustomFonts(fonts).
		WithDefaultFont(&props.Font{Family: labelFontFamily, Size: sheet.nameSize}).
		WithSequentialMode().
		WithMaxGridSize(tenths(rowWidth)).
		WithLeftMargin(sheet.left).WithRightMargin(210-sheet.left-rowWidth).
		WithTopMargin(sheet.top).WithBottomMargin(sheet.bottom).
		WithCreationDate(labelDate).
		WithTitle(title, true).
		Build()
	doc := maroto.New(cfg)
	barcodeWidth := sheet.width * sheet.barcodePercent / 100
	for start := 0; start < len(labels); start += sheet.perSheet() {
		onSheet := labels[start:min(start+sheet.perSheet(), len(labels))]
		rows := make([]core.Row, 0, sheet.rows)
		for r := 0; r < sheet.rows; r++ {
			cols := make([]core.Col, 0, 2*sheet.columns-1)
			for c := 0; c < sheet.columns; c++ {
				if c > 0 {
					cols = append(cols, col.New(tenths(sheet.gap)))
				}
				cell := col.New(tenths(sheet.width))
				if i := r*sheet.columns + c; i < len(onSheet) {
					l := onSheet[i]
					cell.Add(
						code.NewBar(l.token, props.Barcode{
							Left: (sheet.width - barcodeWidth) / 2, Top: 3, Percent: sheet.barcodePercent,
							Proportion: props.Proportion{Width: 5, Height: 1},
						}),
						text.New(l.fullCode, props.Text{
							Top: 3 + barcodeWidth/5 + 2, Size: sheet.codeSize, Style: fontstyle.Bold, Align: align.Center,
							Left: 2, Right: 2,
						}),
					)
					nameTop := 3 + barcodeWidth/5 + 4 + sheet.codeSize*0.3528 + 1
					if l.name != "" {
						name := l.name
						if l.extra != "" {
							// One line only, so the line below never overlaps it.
							name = oneLine(name, sheet.nameSize, sheet.width-4)
						}
						cell.Add(text.New(name, props.Text{
							Top: nameTop, Size: sheet.nameSize, Align: align.Center,
							Left: 2, Right: 2,
						}))
					}
					if l.extra != "" {
						cell.Add(text.New(l.extra, props.Text{
							Top: nameTop + sheet.nameSize*0.3528 + 1.5, Size: sheet.nameSize, Style: fontstyle.Bold, Align: align.Center,
							Left: 2, Right: 2,
						}))
					}
				}
				cols = append(cols, cell)
			}
			rows = append(rows, row.New(sheet.height).Add(cols...))
		}
		doc.AddPages(page.New().Add(rows...))
	}
	return doc, nil
}

// renderLabels is the labels as a PDF on sheet.
func renderLabels(labels []label, sheet labelSheet, title string) ([]byte, error) {
	doc, err := layoutLabels(labels, sheet, title)
	if err != nil {
		return nil, err
	}
	out, err := doc.Generate()
	if err != nil {
		return nil, fmt.Errorf("inventory: render the labels: %w", err)
	}
	return out.GetBytes(), nil
}

// labelsPDF is the PDF answer with its download headers.
type labelsPDF struct {
	body        gen.PrintInventoryLabels200ApplicationpdfResponse
	disposition string
}

func (r labelsPDF) VisitPrintInventoryLabelsResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", r.disposition)
	return r.body.VisitPrintInventoryLabelsResponse(w)
}

// PrintInventoryLabels Print location labels
// (POST /api/v1/inventory/labels)
func (s *server) PrintInventoryLabels(ctx context.Context, req gen.PrintInventoryLabelsRequestObject) (gen.PrintInventoryLabelsResponseObject, error) {
	if req.Body == nil {
		return gen.PrintInventoryLabels400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, "", invalidLabelsTitle, "The request has no body.")), nil
	}
	in := *req.Body
	f := s.unknownProperties(ctx, labelsInputSchema)
	sheet, ok := labelSheets[in.Layout]
	if !ok {
		f.add("layout", "The layout must be l7160 or l7163")
	}
	if len(in.LocationIds) == 0 {
		f.add("locationIds", "Name at least one location")
	}
	if f.failed() {
		return gen.PrintInventoryLabels400ApplicationProblemPlusJSONResponse(invalid(invalidLabelsTitle, f)), nil
	}
	ids := distinct(in.LocationIds)
	if len(ids) > maxLabels {
		return gen.PrintInventoryLabels400ApplicationProblemPlusJSONResponse(labelsTooMany(strconv.Itoa(len(ids)))), nil
	}
	subtree := in.Subtree != nil && *in.Subtree
	var rows []store.InventoryLocation
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if subtree {
			rows, err = q.ActiveSubtrees(ctx, store.ActiveSubtreesParams{Ids: ids, RowLimit: maxLabels + 1})
		} else {
			rows, err = q.LocationsByIDs(ctx, ids)
		}
		if err != nil {
			return fmt.Errorf("inventory: read the locations to label: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(rows) > maxLabels {
		// The subtree's read stops one past the cap, so the count is a floor.
		return gen.PrintInventoryLabels400ApplicationProblemPlusJSONResponse(labelsTooMany(fmt.Sprintf("more than %d", maxLabels))), nil
	}
	found := make(map[int64]bool, len(rows))
	for _, r := range rows {
		found[r.ID] = true
	}
	for _, id := range ids {
		if !found[id] {
			f.addCoded("locationIds", codeLocationNotFound, fmt.Sprintf("No location has the id %d", id))
		}
	}
	if f.failed() {
		return gen.PrintInventoryLabels400ApplicationProblemPlusJSONResponse(invalid(invalidLabelsTitle, f)), nil
	}
	slices.SortStableFunc(rows, func(a, b store.InventoryLocation) int {
		if a.FullCode < b.FullCode {
			return -1
		}
		if a.FullCode > b.FullCode {
			return 1
		}
		return 0
	})
	labels := make([]label, 0, len(rows))
	for _, r := range rows {
		labels = append(labels, labelOf(r))
	}
	body, err := renderLabels(labels, sheet, "Location labels")
	if err != nil {
		return nil, err
	}
	return labelsPDF{
		body:        gen.PrintInventoryLabels200ApplicationpdfResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))},
		disposition: fmt.Sprintf("attachment; filename=%q", fmt.Sprintf("location-labels-%s.pdf", in.Layout)),
	}, nil
}

// labelsTooMany is the 400 of more than maxLabels labels; would is how many
// the request would print, as known.
func labelsTooMany(would string) gen.InventoryProblem {
	return problem(http.StatusBadRequest, codeLabelsTooMany, invalidLabelsTitle,
		fmt.Sprintf("A request prints at most %d labels; this one would print %s labels.", maxLabels, would))
}

// labelFont is the regular label font, parsed once, for measuring a line.
var labelFont = sync.OnceValues(func() (*sfnt.Font, error) { return sfnt.Parse(notoSansRegular) })

// oneLine is text as it fits on one line width millimetres wide at size
// points in the regular label font: whole, or cut with an ellipsis.
func oneLine(text string, size, width float64) string {
	f, err := labelFont()
	if err != nil {
		return text
	}
	var buf sfnt.Buffer
	em := fixed.I(int(f.UnitsPerEm()))
	mm := func(r rune) float64 {
		g, err := f.GlyphIndex(&buf, r)
		if err != nil {
			return 0
		}
		adv, err := f.GlyphAdvance(&buf, g, em, font.HintingNone)
		if err != nil {
			return 0
		}
		return float64(adv) / float64(em) * size * 0.3528
	}
	runes := []rune(text)
	total := 0.0
	for _, r := range runes {
		total += mm(r)
	}
	if total <= width {
		return text
	}
	room := width - mm('…')
	used := 0.0
	for i, r := range runes {
		if used+mm(r) > room {
			return strings.TrimRight(string(runes[:i]), " ·") + "…"
		}
		used += mm(r)
	}
	return text
}
