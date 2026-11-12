package inventory

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// Lot labels (manufacturing phase 2 M73): the location labels' sheets
// (labels.go) with a lot on each — the LOT<id> token as the Code 128 across
// the top, which the lookup reads back (M66), the lot number large, the SKU
// and product name, and the expiry, else the best-before date. In the order
// the ids are given, copies of each in a run. A serial is a lot of its own,
// so serials print one label each. These are internal labels; with format
// gs1 each is a GS1-128 label instead (M93, gs1.go).

// lotToken is a lot's token, as its label's barcode carries it: LOT1001.
func lotToken(id int64) string { return lotTokenPrefix + strconv.FormatInt(id, 10) }

// lotLabelOf is a lot's label.
func lotLabelOf(l store.InventoryLot) label {
	out := label{token: lotToken(l.ID), fullCode: l.Number, name: l.Sku + " · " + l.ProductName}
	switch {
	case l.Expiry.Valid:
		out.extra = "EXP " + l.Expiry.Time.Format(time.DateOnly)
	case l.BestBefore.Valid:
		out.extra = "BB " + l.BestBefore.Time.Format(time.DateOnly)
	}
	return out
}

// lotLabelsPDF is the PDF answer with its download headers.
type lotLabelsPDF struct {
	body        gen.PrintInventoryLotLabels200ApplicationpdfResponse
	disposition string
}

func (r lotLabelsPDF) VisitPrintInventoryLotLabelsResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", r.disposition)
	return r.body.VisitPrintInventoryLotLabelsResponse(w)
}

// PrintInventoryLotLabels Print lot labels
// (GET /api/v1/inventory/lots/labels.pdf)
func (s *server) PrintInventoryLotLabels(ctx context.Context, req gen.PrintInventoryLotLabelsRequestObject) (gen.PrintInventoryLotLabelsResponseObject, error) {
	p := req.Params
	var f fieldErrors
	layout := gen.L7160
	if p.Layout != nil {
		layout = *p.Layout
	}
	sheet, ok := labelSheets[layout]
	if !ok {
		f.add("layout", "The layout must be l7160 or l7163")
	}
	copies := 1
	if p.Copies != nil {
		copies = int(*p.Copies)
		if copies < 1 || copies > maxLabels {
			f.add("copies", fmt.Sprintf("The copies are 1 to %d", maxLabels))
		}
	}
	if len(p.Ids) == 0 {
		f.add("ids", "Name at least one lot")
	}
	gs1 := false
	if p.Format != nil {
		if !p.Format.Valid() {
			f.add("format", "The format is internal or gs1")
		}
		gs1 = *p.Format == gen.Gs1
	}
	if f.failed() {
		return gen.PrintInventoryLotLabels400ApplicationProblemPlusJSONResponse(invalid(invalidLabelsTitle, f)), nil
	}
	if n := len(p.Ids) * copies; n > maxLabels {
		return gen.PrintInventoryLotLabels400ApplicationProblemPlusJSONResponse(labelsTooMany(strconv.Itoa(n))), nil
	}
	rows, err := store.New(s.deps.Pool).LotsByIDs(ctx, distinct(p.Ids))
	if err != nil {
		return nil, fmt.Errorf("inventory: read the lots to label: %w", err)
	}
	byID := make(map[int64]store.InventoryLot, len(rows))
	variantIDs := make([]int32, 0, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
		variantIDs = append(variantIDs, r.VariantID)
	}
	// A GS1-128 label carries the variant's GTIN, read through the catalog
	// (M93); a variant the catalog no longer has has none.
	barcodes := map[int32]*string{}
	if gs1 {
		entries, err := s.catalogVariants(ctx, distinct(variantIDs))
		if status, p, ok := answerOf(err); ok && status == http.StatusServiceUnavailable {
			return gen.PrintInventoryLotLabels503ApplicationProblemPlusJSONResponse(p), nil
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			barcodes[e.ID] = e.Barcode
		}
	}
	labels := make([]label, 0, len(p.Ids)*copies)
	for _, id := range p.Ids {
		lot, ok := byID[id]
		if !ok {
			f.addCoded("ids", codeLotUnknown, fmt.Sprintf("No lot has the id %d", id))
			continue
		}
		one := lotLabelOf(lot)
		if gs1 {
			one = gs1LabelOf(lot, barcodes[lot.VariantID])
		}
		for range copies {
			labels = append(labels, one)
		}
	}
	if f.failed() {
		return gen.PrintInventoryLotLabels400ApplicationProblemPlusJSONResponse(invalid(invalidLabelsTitle, f)), nil
	}
	title, name := "Lot labels", "lot-labels"
	if gs1 {
		title, name = "GS1-128 lot labels", "gs1-lot-labels"
	}
	body, err := renderLabels(labels, sheet, title)
	if err != nil {
		return nil, err
	}
	return lotLabelsPDF{
		body:        gen.PrintInventoryLotLabels200ApplicationpdfResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))},
		disposition: fmt.Sprintf("attachment; filename=%q", fmt.Sprintf("%s-%s.pdf", name, layout)),
	}, nil
}
