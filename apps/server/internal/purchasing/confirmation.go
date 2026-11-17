package purchasing

// The supplier's confirmation (P19): its order reference and the confirmed
// day, per order and optionally per line, on a sent order. A fact, no state;
// it ends the order's unconfirmed attention item. Empty values clear it.

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/store"
)

// ConfirmPurchasingOrder Record the supplier's confirmation
// (PUT /api/v1/purchasing/orders/{orderId}/confirmation)
func (s *server) ConfirmPurchasingOrder(ctx context.Context, req gen.ConfirmPurchasingOrderRequestObject) (gen.ConfirmPurchasingOrderResponseObject, error) {
	resp := answers[gen.ConfirmPurchasingOrderResponseObject]{
		badRequest: func(p gen.PurchasingProblem) gen.ConfirmPurchasingOrderResponseObject {
			return gen.ConfirmPurchasingOrder400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func(p gen.PurchasingProblem) gen.ConfirmPurchasingOrderResponseObject {
			return gen.ConfirmPurchasingOrder404ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.PurchasingProblem) gen.ConfirmPurchasingOrderResponseObject {
			return gen.ConfirmPurchasingOrder409ApplicationProblemPlusJSONResponse(p)
		},
	}
	b := *req.Body
	reference := textPtr(deref(b.SupplierReference))
	if reference != nil && utf8.RuneCountInString(*reference) > 100 {
		return gen.ConfirmPurchasingOrder400ApplicationProblemPlusJSONResponse(invalid(invalidConfirmTitle, fieldErrorsOf("supplierReference", "A reference is at most 100 characters"))), nil
	}
	var lines []gen.PurchasingConfirmationLine
	if b.Lines != nil {
		lines = *b.Lines
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		o, err := lockedOrder(ctx, txq, req.OrderId, b.ExpectedUpdatedAt)
		if err != nil {
			return err
		}
		if err := judgeSent(o); err != nil {
			return err
		}
		saved, err := txq.OrderLines(ctx, o.ID)
		if err != nil {
			return fmt.Errorf("purchasing: read order %d's lines: %w", o.ID, err)
		}
		byID := map[int64]store.PurchasingOrderLine{}
		for _, l := range saved {
			byID[l.ID] = l
		}
		var failures []lineFailure
		for i, l := range lines {
			if _, ok := byID[l.LineId]; !ok {
				failures = append(failures, lineFailure{index: i, code: codeLineNotFound, detail: fmt.Sprintf("The order has no line %d", l.LineId)})
			}
		}
		if len(failures) > 0 {
			return refuseLines(invalidConfirmTitle, failures)
		}
		now := s.deps.Clock()
		confirmedOn := dateFromAPI(b.ConfirmedOn)
		params := store.SetOrderConfirmationParams{ID: o.ID, SupplierReference: reference, ConfirmedOn: dateOf(confirmedOn), Now: now}
		if reference != nil || confirmedOn != nil {
			params.ConfirmedAt, params.ConfirmedByUserID, params.ConfirmedByDisplay = &now, by.id, by.display
		}
		if _, err := txq.SetOrderConfirmation(ctx, params); err != nil {
			return fmt.Errorf("purchasing: record order %d's confirmation: %w", o.ID, err)
		}
		actions := []change{{Action: "confirmed"}}
		if from, to := deref(o.SupplierReference), deref(reference); from != to {
			actions = append(actions, fieldChange("supplierReference", from, to))
		}
		if from, to := dayText(timeOfDate(o.ConfirmedOn)), dayText(confirmedOn); from != to {
			actions = append(actions, fieldChange("confirmedOn", from, to))
		}
		for _, l := range lines {
			day := dateFromAPI(l.ConfirmedOn)
			if err := txq.SetLineConfirmedOn(ctx, store.SetLineConfirmedOnParams{ID: l.LineId, ConfirmedOn: dateOf(day)}); err != nil {
				return fmt.Errorf("purchasing: record line %d's confirmed day: %w", l.LineId, err)
			}
			if from, to := dayText(timeOfDate(byID[l.LineId].ConfirmedOn)), dayText(day); from != to {
				actions = append(actions, change{Action: "line_changed", LineID: ptr(l.LineId), Field: ptr("confirmedOn"), From: ptr(from), To: ptr(to)})
			}
		}
		return logChange(ctx, txq, o.ID, by, now, actions...)
	})
	if err != nil {
		return answer(err, resp)
	}
	view, err := s.getOrderView(ctx, req.OrderId)
	if err != nil {
		return nil, err
	}
	return gen.ConfirmPurchasingOrder200JSONResponse(view), nil
}

// fieldErrorsOf is one field's error.
func fieldErrorsOf(field, message string) fieldErrors {
	var f fieldErrors
	f.add(field, strings.TrimSpace(message))
	return f
}
