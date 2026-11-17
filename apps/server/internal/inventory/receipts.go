package inventory

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
)

// Goods receipts (design I15, I12): a default location, lines of a variant,
// an optional location overriding it, and a quantity, with the supplier's
// name and reference and a note. Every line is a line to a location. A
// receipt refuses a service's variant (variant_not_stockable) and a
// discontinued product's (variant_discontinued); a Draft product's is
// received, since goods arrive before they go on sale. A line gives its
// quantity, or what was entered in packagings (entered.go).
//
// A line of a tracked variant names its lot (manufacturing phase 2 M57, M59):
// lotId, an existing lot; lot, a lot by number to be found or made — an empty
// number generates L<n>; or, for a serial variant, serials, expanded into one
// line of 1 per serial, each a lot by number with the line's lot's dates.

// PostInventoryReceipt Receive goods
// (POST /api/v1/inventory/movements/receipts)
func (s *server) PostInventoryReceipt(ctx context.Context, req gen.PostInventoryReceiptRequestObject) (gen.PostInventoryReceiptResponseObject, error) {
	if req.Body == nil {
		return gen.PostInventoryReceipt400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, "", invalidPostingTitle, "The request has no body.")), nil
	}
	in := *req.Body
	f := s.unknownProperties(ctx, receiptInputSchema)
	note := optionalText(&f, "note", in.Note, maxNoteLength)
	supplierName := optionalText(&f, "supplierName", in.SupplierName, maxSupplierName)
	supplierReference := optionalText(&f, "supplierReference", in.SupplierReference, maxSupplierRef)
	lines := make([]engineLine, 0, len(in.Lines))
	namesLots := false
	for i, l := range in.Lines {
		to := in.ToLocationId
		if l.LocationId != nil {
			to = *l.LocationId
		}
		field := fmt.Sprintf("lines[%d]", i)
		line := engineLine{index: i, variantID: l.VariantId, to: &to, lotID: lotIDOf(&f, field+".lotId", l.LotId)}
		namesLots = namesLots || l.Lot != nil || l.Serials != nil
		if l.Serials == nil {
			line.quantity, line.entry, line.failCode, line.failText = readEntered(l.Quantity, l.Entered, positiveQuantity)
		}
		var given *big.Rat
		if l.Quantity != nil {
			given = ratFromFloat(*l.Quantity)
		}
		lines = append(lines, receiptLines(&f, field, line, l.Lot, l.Serials, given, l.Entered != nil)...)
	}
	if f.failed() {
		return gen.PostInventoryReceipt400ApplicationProblemPlusJSONResponse(invalid(invalidPostingTitle, f)), nil
	}
	// The day a new lot's dates default from (M62), read before the
	// transaction; a receipt naming no lot needs none.
	var today time.Time
	if namesLots {
		var err error
		if today, err = s.today(ctx); err != nil {
			return nil, err
		}
	}
	hash, err := requestHash(string(gen.Receipt), in)
	if err != nil {
		return nil, err
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.postMovement(ctx, movementHeader{
		clientRequestID: in.ClientRequestId, requestHash: hash, siteID: in.SiteId, kind: string(gen.Receipt),
		note: note, supplierName: supplierName, supplierReference: supplierReference, poster: who,
	}, lines, true, today)
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusBadRequest:
			return gen.PostInventoryReceipt400ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusConflict:
			return gen.PostInventoryReceipt409ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusServiceUnavailable:
			return gen.PostInventoryReceipt503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	mv, err := s.movementOf(ctx, res.movementID)
	if err != nil {
		return nil, err
	}
	if res.replay {
		return gen.PostInventoryReceipt200JSONResponse(mv), nil
	}
	return gen.PostInventoryReceipt201JSONResponse(mv), nil
}

// receiptLines reads a receipt line's lot and serials, as the receipt
// endpoint and the ledger's Receive both do (purchasing delivery 0): the lot
// by number with its shape and its dates in order, the lot named one way —
// a lot id, a lot, or serials — and the serials expanded into one line of 1
// per serial (M57), each naming its serial as a lot by number with the
// line's lot's dates, supplier lot and note. line carries the line's index,
// variant, location and lot id, and its quantity when it names no serials;
// given is the quantity the line states, nil for none, which with serials
// must equal their number, and entered says it was entered in packagings,
// which serials refuse. A field's failure goes on f under field; a serial
// failure is the line's (failCode), the line answered alone.
func receiptLines(f *fieldErrors, field string, line engineLine, lot *gen.InventoryNewLot, serials *gen.InventorySerials, given *big.Rat, entered bool) []engineLine {
	line.newLot = readNewLot(f, field+".lot", lot)
	if line.lotID != 0 && (line.newLot != nil || serials != nil) {
		f.add(field, "A line names a lotId, a lot or serials, one of them")
	}
	if serials == nil {
		return []engineLine{line}
	}
	base := line.newLot
	if base == nil {
		base = &lotRequest{compareProduction: true}
	}
	if base.number != "" {
		f.add(field+".lot.number", "With serials, each serial is the lot's number; the lot names none")
	}
	numbers, code, detail := serialNumbers(serials)
	switch {
	case entered:
		code, detail = codeEnteredInvalid, "Serials are counted one by one; a line with serials is not entered in packagings."
	case code == "" && given != nil && given.Cmp(big.NewRat(int64(len(numbers)), 1)) != 0:
		code, detail = codeSerialQuantity, fmt.Sprintf("The line's quantity is %s; its serials number %d.", decimalText(given), len(numbers))
	}
	if code != "" {
		line.failCode, line.failText, line.newLot = code, detail, nil
		return []engineLine{line}
	}
	out := make([]engineLine, 0, len(numbers))
	for _, n := range numbers {
		serial := line
		lot := *base
		lot.number = n
		serial.newLot, serial.quantity = &lot, big.NewRat(1, 1)
		out = append(out, serial)
	}
	return out
}
