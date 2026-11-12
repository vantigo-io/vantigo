package inventory

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
)

// Moves (design I15, I13): lines of a variant, from, to and a quantity, inside
// one site — stock goes between sites on a transfer (transfers.go), never on
// a move (move_crosses_sites) — and never from a location to itself
// (same_location). A move accepts a discontinued product's variant, and a
// service's, so stock left on either can still be put where it belongs
// (I12, design §9).

// PostInventoryMove Move stock
// (POST /api/v1/inventory/movements/moves)
func (s *server) PostInventoryMove(ctx context.Context, req gen.PostInventoryMoveRequestObject) (gen.PostInventoryMoveResponseObject, error) {
	if req.Body == nil {
		return gen.PostInventoryMove400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, "", invalidPostingTitle, "The request has no body.")), nil
	}
	in := *req.Body
	f := s.unknownProperties(ctx, moveInputSchema)
	note := optionalText(&f, "note", in.Note, maxNoteLength)
	lines := make([]engineLine, 0, len(in.Lines))
	for i, l := range in.Lines {
		from, to := l.FromLocationId, l.ToLocationId
		// The lot leaves from and lands on to unchanged (manufacturing phase 2
		// M59); a move ignores holds and expiry, so held stock can go to a
		// hold shelf.
		line := engineLine{index: i, variantID: l.VariantId, from: &from, to: &to, lotID: lotIDOf(&f, fmt.Sprintf("lines[%d].lotId", i), l.LotId)}
		line.quantity, line.entry, line.failCode, line.failText = readEntered(l.Quantity, l.Entered, positiveQuantity)
		if from == to {
			line.failCode, line.failText = codeSameLocation, "A move goes from one location to another."
		}
		lines = append(lines, line)
	}
	if f.failed() {
		return gen.PostInventoryMove400ApplicationProblemPlusJSONResponse(invalid(invalidPostingTitle, f)), nil
	}
	hash, err := requestHash(string(gen.Move), in)
	if err != nil {
		return nil, err
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.postMovement(ctx, movementHeader{
		clientRequestID: in.ClientRequestId, requestHash: hash, siteID: in.SiteId, kind: string(gen.Move),
		note: note, poster: who,
	}, lines, false, time.Time{})
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusBadRequest:
			return gen.PostInventoryMove400ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusConflict:
			return gen.PostInventoryMove409ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusServiceUnavailable:
			return gen.PostInventoryMove503ApplicationProblemPlusJSONResponse(p), nil
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
		return gen.PostInventoryMove200JSONResponse(mv), nil
	}
	return gen.PostInventoryMove201JSONResponse(mv), nil
}
