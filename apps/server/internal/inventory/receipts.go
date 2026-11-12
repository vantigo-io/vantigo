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
		line.newLot = readNewLot(&f, field+".lot", l.Lot)
		namesLots = namesLots || line.newLot != nil || l.Serials != nil
		if line.lotID != 0 && (line.newLot != nil || l.Serials != nil) {
			f.add(field, "A line names a lotId, a lot or serials, one of them")
		}
		if l.Serials == nil {
			line.quantity, line.entry, line.failCode, line.failText = readEntered(l.Quantity, l.Entered, positiveQuantity)
			lines = append(lines, line)
			continue
		}
		lines = append(lines, serialLines(&f, field, line, l)...)
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

// serialLines expands a receipt line's serials into one line of 1 per serial
// (M57), each naming its serial as a lot by number with the line's lot's
// dates and supplier lot. The line's quantity, when given, must equal their
// number; an entry in packagings is refused. A failure is the line's.
func serialLines(f *fieldErrors, field string, line engineLine, l gen.InventoryReceiptLine) []engineLine {
	base := line.newLot
	if base == nil {
		base = &lotRequest{compareProduction: true}
	}
	if base.number != "" {
		f.add(field+".lot.number", "With serials, each serial is the lot's number; the lot names none")
	}
	numbers, code, detail := serialNumbers(l.Serials)
	switch {
	case l.Entered != nil:
		code, detail = codeEnteredInvalid, "Serials are counted one by one; a line with serials is not entered in packagings."
	case code == "" && l.Quantity != nil && ratFromFloat(*l.Quantity).Cmp(big.NewRat(int64(len(numbers)), 1)) != 0:
		code, detail = codeSerialQuantity, fmt.Sprintf("The line's quantity is %v; its serials number %d.", *l.Quantity, len(numbers))
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
