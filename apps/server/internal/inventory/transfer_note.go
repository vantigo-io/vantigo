package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/code"
	"github.com/johnfercher/maroto/v2/pkg/components/col"
	"github.com/johnfercher/maroto/v2/pkg/components/line"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/config"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/fontrepository"
	"github.com/johnfercher/maroto/v2/pkg/props"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// The følgeseddel (phase 2 design I51): the paper that travels with a
// transfer, for a transfer sent or later — a draft has none, and answers the
// 404. Its number and the token TRF-<number> as Code 128, which the lookup
// and the receive page's scan field open (I52); the from and to site with
// their addresses; when it was sent and by whom; the expected arrival and the
// reference — never the transfer's internal note; the lines — SKU, name, quantity with what was entered in
// packagings, and the source location's full code; and a line for the
// receiver's signature. Laid out with maroto as the location labels are, in
// the same fonts, with its words in Norwegian and English together. Built on
// demand, never stored.

// noteRowsBuilt is the tests' window on what a note prints: every line's
// cells, in order (SetTransferNoteHook). It is an atomic pointer, so a test
// may install it while other tests' handlers render notes.
var noteRowsBuilt atomic.Pointer[func(rows [][]string)]

// noteRows is the lines of the note: SKU, name, quantity, source location.
func noteRows(r transferRead) [][]string {
	out := make([][]string, 0, len(r.lines))
	for _, l := range r.lines {
		ref := r.variantOf(l)
		location := ""
		if l.FromLocationID != nil {
			location = r.locations[*l.FromLocationID].FullCode
		}
		quantity := decimalText(ratFromNumeric(l.Sent)) + " " + ref.Unit
		if e := enteredText(l.Entered); e != "" {
			quantity += " (" + e + ")"
		}
		out = append(out, []string{l.Sku, l.ProductName, strings.TrimSpace(quantity), location})
	}
	return out
}

// enteredText is a stored entry as the note says it: "2 × Box, 3 loose".
func enteredText(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var snap enteredSnapshot
	if json.Unmarshal(raw, &snap) != nil {
		return ""
	}
	parts := make([]string, 0, len(snap.Packagings)+1)
	for _, p := range snap.Packagings {
		parts = append(parts, fmt.Sprintf("%d × %s", p.Count, p.Name))
	}
	if snap.Loose != nil && ratFromText(*snap.Loose).Sign() > 0 {
		parts = append(parts, decimalText(ratFromText(*snap.Loose))+" loose")
	}
	return strings.Join(parts, ", ")
}

// siteLines is a site as the note's party block prints it: its code and
// name, then each line of its address.
func siteLines(s store.InventorySite) []string {
	out := []string{s.Code + " " + s.Name}
	for _, p := range []*string{s.AddressLine1, s.AddressLine2} {
		if p != nil {
			out = append(out, *p)
		}
	}
	if place := strings.TrimSpace(deref(s.PostalCode) + " " + deref(s.City)); place != "" {
		out = append(out, place)
	}
	if s.Country != nil {
		out = append(out, *s.Country)
	}
	return out
}

// renderNote is the note as a PDF; zone is the business zone its dates are
// printed in.
func renderNote(r transferRead, zone *time.Location) ([]byte, error) {
	fonts, err := fontrepository.New().
		AddUTF8FontFromBytes(labelFontFamily, fontstyle.Normal, notoSansRegular).
		AddUTF8FontFromBytes(labelFontFamily, fontstyle.Bold, notoSansBold).
		Load()
	if err != nil {
		return nil, fmt.Errorf("inventory: load the note's font: %w", err)
	}
	t := r.transfer
	token := transferToken(t.Number)
	cfg := config.NewBuilder().
		WithCustomFonts(fonts).
		WithDefaultFont(&props.Font{Family: labelFontFamily, Size: 9}).
		WithSequentialMode().
		WithLeftMargin(15).WithRightMargin(15).WithTopMargin(15).
		WithCreationDate(labelDate).
		WithTitle("Følgeseddel "+token, true).
		Build()
	doc := maroto.New(cfg)
	bold := props.Text{Style: fontstyle.Bold}

	doc.AddRows(text.NewRow(12, "Følgeseddel / Transfer note", props.Text{Style: fontstyle.Bold, Size: 18}))
	doc.AddRow(20,
		text.NewCol(6, fmt.Sprintf("Overføring %d / Transfer %d", t.Number, t.Number), props.Text{Style: fontstyle.Bold, Size: 12, Top: 2}),
		code.NewBarCol(6, token, props.Barcode{Percent: 90, Proportion: props.Proportion{Width: 20, Height: 4}}),
	)
	doc.AddRow(4.5, text.NewCol(6, "Fra / From", bold), text.NewCol(6, "Til / To", bold))
	from, to := siteLines(r.sites[t.FromSiteID]), siteLines(r.sites[t.ToSiteID])
	for i := range max(len(from), len(to)) {
		cell := func(lines []string) string {
			if i < len(lines) {
				return lines[i]
			}
			return ""
		}
		doc.AddRow(4.5, text.NewCol(6, cell(from)), text.NewCol(6, cell(to)))
	}
	doc.AddRows(line.NewRow(4))
	meta := [][2]string{}
	if t.SentAt != nil {
		meta = append(meta, [2]string{"Sendt / Sent", t.SentAt.In(zone).Format("2006-01-02 15:04") + ", " + posterOf(t.SentByUserID, t.SentByDisplay).DisplayName})
	}
	if t.ExpectedArrival.Valid {
		meta = append(meta, [2]string{"Forventet ankomst / Expected", t.ExpectedArrival.Time.Format("2006-01-02")})
	}
	if t.Reference != nil {
		meta = append(meta, [2]string{"Referanse / Reference", *t.Reference})
	}
	for _, kv := range meta {
		doc.AddAutoRow(text.NewCol(4, kv[0], bold), text.NewCol(8, kv[1]))
	}
	doc.AddRows(line.NewRow(4))
	sizes := []int{2, 5, 2, 3}
	row := func(cells []string, header bool) {
		cols := make([]core.Col, 0, len(cells))
		for i, c := range cells {
			style := props.Text{}
			if i == 2 {
				style.Align = align.Right
			}
			if header {
				style.Style = fontstyle.Bold
			}
			cols = append(cols, text.NewCol(sizes[i], c, style))
		}
		doc.AddAutoRow(cols...)
	}
	row([]string{"SKU", "Vare / Item", "Antall / Quantity", "Fra lokasjon / From location"}, true)
	rows := noteRows(r)
	if hook := noteRowsBuilt.Load(); hook != nil {
		(*hook)(rows)
	}
	for _, cells := range rows {
		row(cells, false)
	}
	doc.AddRows(line.NewRow(12))
	doc.AddRow(8, text.NewCol(6, "Mottatt av / Received by", bold), text.NewCol(6, "Dato / Date", bold))
	doc.AddRow(14, col.New(12))
	doc.AddRow(4, line.NewCol(5), col.New(1), line.NewCol(6))
	out, err := doc.Generate()
	if err != nil {
		return nil, fmt.Errorf("inventory: render the note of transfer %d: %w", t.ID, err)
	}
	return out.GetBytes(), nil
}

// notePDF is the PDF answer with its download headers.
type notePDF struct {
	body        gen.GetInventoryTransferNote200ApplicationpdfResponse
	disposition string
}

func (r notePDF) VisitGetInventoryTransferNoteResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", r.disposition)
	return r.body.VisitGetInventoryTransferNoteResponse(w)
}

// GetInventoryTransferNote Print a transfer note
// (GET /api/v1/inventory/transfers/{id}/note.pdf)
func (s *server) GetInventoryTransferNote(ctx context.Context, req gen.GetInventoryTransferNoteRequestObject) (gen.GetInventoryTransferNoteResponseObject, error) {
	r, err := s.readTransfer(ctx, req.Id)
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusNotFound:
			return gen.GetInventoryTransferNote404Response{}, nil
		case http.StatusServiceUnavailable:
			return gen.GetInventoryTransferNote503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	if r.transfer.Status == transferDraft {
		return gen.GetInventoryTransferNote404Response{}, nil
	}
	zone, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	body, err := renderNote(r, zone)
	if err != nil {
		return nil, err
	}
	return notePDF{
		body:        gen.GetInventoryTransferNote200ApplicationpdfResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))},
		disposition: fmt.Sprintf("attachment; filename=%q", fmt.Sprintf("folgeseddel-%s.pdf", transferToken(r.transfer.Number))),
	}, nil
}
