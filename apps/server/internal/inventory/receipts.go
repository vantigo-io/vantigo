package inventory

import (
	"context"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
)

// Goods receipts (design I15, I12): a default location, lines of a variant,
// an optional location overriding it, and a quantity, with the supplier's
// name and reference and a note. Every line is a line to a location. A
// receipt refuses a service's variant (variant_not_stockable) and a
// discontinued product's (variant_discontinued); a Draft product's is
// received, since goods arrive before they go on sale.

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
	for i, l := range in.Lines {
		to := in.ToLocationId
		if l.LocationId != nil {
			to = *l.LocationId
		}
		line := engineLine{index: i, variantID: l.VariantId, to: &to}
		q, msg := parseQuantity(l.Quantity, positiveQuantity)
		if msg != "" {
			line.failCode, line.failText = codeQuantityInvalid, msg
		}
		line.quantity = q
		lines = append(lines, line)
	}
	if f.failed() {
		return gen.PostInventoryReceipt400ApplicationProblemPlusJSONResponse(invalid(invalidPostingTitle, f)), nil
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
	}, lines, true)
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
