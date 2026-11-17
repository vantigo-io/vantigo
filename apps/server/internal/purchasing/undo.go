package purchasing

// Taking a receipt back (purchasing design P29, A5, A10, A16):
// contracts.StockLedger.ReverseReceipt posts one movement of kind
// receipt_reversal — the receipt's lines out of the locations and lots they
// went into, whatever the lots' holds, releasing the holds the receipt set —
// and the order lines' received quantities go down, in one transaction. The
// receipt is kept, marked undone. It locks as Receive does: the variants,
// then the order, then the receipt, then Inventory's rows.
//
// receipt_invoiced (A10) is judged against order_lines.invoiced, which only
// the invoice match writes (delivery D, posted and provisional alike):
// nothing is invoiced before D, and the rule is in place for it.

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/store"
)

// UndoPurchasingReceipt Undo a receipt
// (POST /api/v1/purchasing/receipts/{receiptId}/undo)
func (s *server) UndoPurchasingReceipt(ctx context.Context, req gen.UndoPurchasingReceiptRequestObject) (gen.UndoPurchasingReceiptResponseObject, error) {
	resp := answers[gen.UndoPurchasingReceiptResponseObject]{
		badRequest: func(p gen.PurchasingProblem) gen.UndoPurchasingReceiptResponseObject {
			return gen.UndoPurchasingReceipt400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func(p gen.PurchasingProblem) gen.UndoPurchasingReceiptResponseObject {
			return gen.UndoPurchasingReceipt404ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.PurchasingProblem) gen.UndoPurchasingReceiptResponseObject {
			return gen.UndoPurchasingReceipt409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.PurchasingProblem) gen.UndoPurchasingReceiptResponseObject {
			return gen.UndoPurchasingReceipt503ApplicationProblemPlusJSONResponse(p)
		},
	}
	note := ""
	if req.Body != nil && req.Body.Note != nil {
		if note = strings.TrimSpace(*req.Body.Note); utf8.RuneCountInString(note) > 1000 {
			return answer(refuseField(invalidReceiptTitle, "note", "", "A note is at most 1000 characters"), resp)
		}
	}
	var err error
	for attempt := 1; ; attempt++ {
		err = s.undoOnce(ctx, req.ReceiptId, note)
		if !isDeadlock(err) && !errors.Is(err, errRetryReceipt) {
			break
		}
		if attempt == receiveAttempts {
			err = retryExhausted()
			break
		}
	}
	if err != nil {
		return answer(err, resp)
	}
	out, err := s.receiptResult(ctx, req.ReceiptId)
	if err != nil {
		return nil, err
	}
	return gen.UndoPurchasingReceipt200JSONResponse(out), nil
}

// undoOnce is one attempt at an undo.
func (s *server) undoOnce(ctx context.Context, receiptID int64, note string) error {
	var rec store.PurchasingReceipt
	var lines []store.ReceiptLinesOfRow
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		rec, err = q.GetReceipt(ctx, receiptID)
		if noRows(err) {
			return notFound("receipt")
		}
		if err != nil {
			return fmt.Errorf("purchasing: read receipt %d: %w", receiptID, err)
		}
		lines, err = receiptsOf(ctx, q, []store.PurchasingReceipt{rec})
		return err
	})
	if err != nil {
		return err
	}
	if rec.UndoneAt != nil {
		return receiptUndone(rec)
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return err
	}
	if s.deps.StockLedger == nil {
		return errDependencyUnavailable
	}
	variants := make([]int32, 0, len(lines))
	for _, l := range lines {
		variants = append(variants, l.VariantID)
	}
	// rowOf maps the reversal's movement lines back to the receipt's lines:
	// each serial line was expanded into one line per serial.
	var rows []int
	for i, l := range lines {
		n := max(len(l.Serials), 1)
		for range n {
			rows = append(rows, i)
		}
	}
	rowOf := func(i int) int {
		if i < 0 || i >= len(rows) {
			return -1
		}
		return rows[i]
	}
	return s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if _, err := s.lockVariants(ctx, tx, variants); err != nil {
			return err
		}
		o, err := txq.LockOrder(ctx, rec.OrderID)
		if err != nil {
			return fmt.Errorf("purchasing: lock order %d: %w", rec.OrderID, err)
		}
		locked, err := txq.LockReceipt(ctx, rec.ID)
		if err != nil {
			return fmt.Errorf("purchasing: lock receipt %d: %w", rec.ID, err)
		}
		if locked.UndoneAt != nil {
			return receiptUndone(locked)
		}
		current, err := txq.OrderLines(ctx, o.ID)
		if err != nil {
			return fmt.Errorf("purchasing: read order %d's lines: %w", o.ID, err)
		}
		byID := make(map[int64]store.PurchasingOrderLine, len(current))
		for _, l := range current {
			byID[l.ID] = l
		}
		sums, order := receivedByLine(lines)
		var failures []lineFailure
		for i, l := range lines {
			ol := byID[l.OrderLineID]
			left := new(big.Rat).Sub(ratOrZero(ol.Received), sums[l.OrderLineID])
			if left.Cmp(ratOrZero(ol.Invoiced)) < 0 {
				failures = append(failures, lineFailure{index: i, code: codeReceiptInvoiced, detail: fmt.Sprintf(
					"%s: %s %s is invoiced; the undo would leave %s received.", ol.Name,
					trimmed(ratOrZero(ol.Invoiced), quantityPlaces), ol.Unit, trimmed(left, quantityPlaces))})
			}
		}
		if len(failures) > 0 {
			p := linesProblem("The receipt is invoiced", failures)
			status, code := int32(409), codeReceiptInvoiced
			p.Status, p.Code = &status, &code
			p.Detail = ptr("Part of what this receipt brought in is matched to a supplier invoice. Unmatch the invoice first.")
			return refuse(p)
		}
		posted, err := s.deps.StockLedger.ReverseReceipt(ctx, tx, receiptSource(rec.ID), contracts.StockReversal{
			Note: note, PostedByUserID: by.id, PostedByDisplay: deref(by.display),
		})
		if err != nil {
			if isDeadlock(err) {
				return err
			}
			return stockRefused(err, "The receipt cannot be undone", rowOf, false)
		}
		now := s.deps.Clock()
		if _, err := txq.MarkReceiptUndone(ctx, store.MarkReceiptUndoneParams{ID: rec.ID, Now: &now, UndoneByUserID: by.id,
			UndoneByDisplay: by.display, UndoNote: textPtr(note), ReversalMovementID: &posted.MovementID}); err != nil {
			return fmt.Errorf("purchasing: mark receipt %d undone: %w", rec.ID, err)
		}
		for _, id := range order {
			delta := new(big.Rat).Neg(sums[id])
			if err := txq.AddReceived(ctx, store.AddReceivedParams{ID: id, Delta: numeric(delta, quantityPlaces)}); err != nil {
				return fmt.Errorf("purchasing: take line %d's receipt back: %w", id, err)
			}
		}
		if err := logChange(ctx, txq, o.ID, by, now, change{Action: "receipt_undone", To: ptr(fmt.Sprint(rec.Number))}); err != nil {
			return err
		}
		if _, err := settleOrder(ctx, tx, txq, o, by, now); err != nil {
			return err
		}
		ev := contracts.PurchaseReceiptUndone{
			ReceiptID: rec.ID, Number: rec.Number, OrderID: o.ID, OrderNumber: deref64(o.Number), SupplierID: o.SupplierID,
			SiteID: o.SiteID, ReversalMovementID: posted.MovementID, Lines: receiptEventLines(lines), UndoneAt: now,
		}
		if err := events.Publish(ctx, tx, moduleName, ev, now); err != nil {
			return fmt.Errorf("purchasing: publish the undo of receipt %d: %w", rec.ID, err)
		}
		return nil
	})
}

// receiptUndone is the 409 of an undo of a receipt undone already.
func receiptUndone(r store.PurchasingReceipt) error {
	return conflict(codeReceiptUndone, "The receipt is undone",
		fmt.Sprintf("Receipt %d was undone already; nothing was changed.", r.Number))
}
