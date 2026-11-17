package purchasing

// Receipts (purchasing design §4, delivery B): what the receive, the undo and
// the delivered mark share — the stock sources a receipt names, an order's
// state derived from its lines (A48), a receipt as the API answers it, the
// order as a receiver sees it (A47), and the reads of an order's receipts.
// No view of a receipt carries a price (A21): the price a receipt line keeps
// (P28, A49) is for a later valuation, and no endpoint answers it.

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/store"
)

// seriesReceipt is the receipts' own series (P28), counted from 1.
const seriesReceipt = "receipt"

// The stock sources a receipt names (P3, A27): the receipt is the movement's
// document, the order its source line — which Inventory's movement page
// links to.
const (
	sourceKindOrder   = "order"
	sourceKindReceipt = "receipt"
)

func orderSource(id int64) contracts.StockSource {
	return contracts.StockSource{Module: moduleName, Kind: sourceKindOrder, ID: id}
}

func receiptSource(id int64) contracts.StockSource {
	return contracts.StockSource{Module: moduleName, Kind: sourceKindReceipt, ID: id}
}

// receivable reports whether a line is one a receipt takes: an open goods
// variant line — not closed, not marked delivered.
func receivable(l store.PurchasingOrderLine) bool {
	return l.Kind == kindVariant && l.VariantID != nil && l.ProductType != nil && *l.ProductType == typeGoods &&
		l.ClosedAt == nil && l.DeliveredAt == nil
}

// lineDone reports whether nothing more is to come on a line: closed,
// marked delivered, or a goods line received in full (P18).
func lineDone(l store.PurchasingOrderLine) bool {
	if l.ClosedAt != nil || l.DeliveredAt != nil {
		return true
	}
	if l.Kind == kindVariant && l.ProductType != nil && *l.ProductType == typeGoods {
		return ratOrZero(l.Received).Cmp(ratOrZero(l.Quantity)) >= 0
	}
	return false
}

// keptRemainder reports whether a line, were it closed, ended with something
// that never came: less received than ordered and no delivered mark.
func keptRemainder(l store.PurchasingOrderLine) bool {
	return l.DeliveredAt == nil && ratOrZero(l.Quantity).Cmp(ratOrZero(l.Received)) > 0
}

// derivedState is the state an order's lines make, whatever ended them
// (P18, P27, A40, A48): when nothing stays open, received if every line came
// in full or was marked delivered, closed if a closed line kept a remainder
// — at the latest such line's closed_at, which is what Reopen opens again —;
// else partially received when anything was received or delivered, and
// sent otherwise.
func derivedState(lines []store.PurchasingOrderLine) (string, *time.Time) {
	open, progress := false, false
	var closedAt *time.Time
	for _, l := range lines {
		if !lineDone(l) {
			open = true
		}
		if ratOrZero(l.Received).Sign() > 0 || l.DeliveredAt != nil {
			progress = true
		}
		if l.ClosedAt != nil && keptRemainder(l) && (closedAt == nil || l.ClosedAt.After(*closedAt)) {
			closedAt = l.ClosedAt
		}
	}
	switch {
	case !open && len(lines) > 0 && closedAt != nil:
		return statusClosed, closedAt
	case !open && len(lines) > 0:
		return statusReceived, nil
	case progress:
		return statusPartial, nil
	}
	return statusSent, nil
}

// settleOrder moves an open or received order to the state its lines make
// (A48), or only touches it when the state stands; a closed, cancelled or
// draft order is only touched — Reopen is the way back from a close. An end
// is recorded as setOrderEnd does.
func settleOrder(ctx context.Context, tx pgx.Tx, txq *store.Queries, o store.PurchasingOrder, by actor, now time.Time) (string, error) {
	if !isOpen(o.Status) && o.Status != statusReceived {
		return o.Status, txq.TouchOrder(ctx, store.TouchOrderParams{ID: o.ID, Now: now})
	}
	lines, err := txq.OrderLines(ctx, o.ID)
	if err != nil {
		return "", fmt.Errorf("purchasing: read order %d's lines: %w", o.ID, err)
	}
	status, closedAt := derivedState(lines)
	if status == o.Status {
		return status, txq.TouchOrder(ctx, store.TouchOrderParams{ID: o.ID, Now: now})
	}
	return status, setOrderState(ctx, tx, txq, o, status, closedAt, by, now)
}

// setOrderState sets the state derivedState made and records an end the
// same way whichever action completed the order (A48): a change-log row of
// its own, received or closed, and for a close purchasing.order.closed.v1 —
// a remainder stopped counting as on order. A received order publishes no
// closed event: nothing was left.
func setOrderState(ctx context.Context, tx pgx.Tx, txq *store.Queries, o store.PurchasingOrder, status string, closedAt *time.Time, by actor, now time.Time) error {
	if _, err := txq.SetOrderState(ctx, store.SetOrderStateParams{ID: o.ID, Status: status, ClosedAt: closedAt,
		CancelledAt: o.CancelledAt, Now: now}); err != nil {
		return fmt.Errorf("purchasing: set order %d's state: %w", o.ID, err)
	}
	switch status {
	case statusReceived:
		return logChange(ctx, txq, o.ID, by, now, change{Action: "received"})
	case statusClosed:
		if err := logChange(ctx, txq, o.ID, by, now, change{Action: "closed"}); err != nil {
			return err
		}
		return publishClosed(ctx, tx, o, now)
	}
	return nil
}

// receiptEventLines is a receipt's lines as its events carry them.
func receiptEventLines(lines []store.ReceiptLinesOfRow) []contracts.PurchaseReceiptLine {
	out := make([]contracts.PurchaseReceiptLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, contracts.PurchaseReceiptLine{OrderLineID: l.OrderLineID, VariantID: l.VariantID,
			Quantity: trimmed(ratOrZero(l.Quantity), quantityPlaces)})
	}
	return out
}

// receivedByLine sums a receipt's lines per order line.
func receivedByLine(lines []store.ReceiptLinesOfRow) (map[int64]*big.Rat, []int64) {
	sums := map[int64]*big.Rat{}
	var order []int64
	for _, l := range lines {
		if sums[l.OrderLineID] == nil {
			sums[l.OrderLineID] = new(big.Rat)
			order = append(order, l.OrderLineID)
		}
		sums[l.OrderLineID].Add(sums[l.OrderLineID], ratOrZero(l.Quantity))
	}
	return sums, order
}

// receiptView is a receipt as the API answers it; codes names the site's
// locations by id, "" for one Inventory no longer answers.
func receiptView(r store.PurchasingReceipt, orderNumber int64, lines []store.ReceiptLinesOfRow, codes map[int64]string) gen.PurchasingReceipt {
	out := gen.PurchasingReceipt{
		Id: r.ID, Number: r.Number, OrderId: r.OrderID, OrderNumber: orderNumber, SiteId: r.SiteID,
		Status: gen.PurchasingReceiptStatus("posted"), ReceivedOn: openapi_types.Date{Time: r.ReceivedOn.Time},
		DeliveryNote: r.DeliveryNote, Note: r.Note, CreatedAt: r.CreatedAt,
		ReceivedBy: personOut(r.ReceivedByUserID, r.ReceivedByDisplay), Lines: make([]gen.PurchasingReceiptLine, 0, len(lines)),
	}
	if r.MovementID != nil {
		out.MovementId = *r.MovementID
	}
	if r.UndoneAt != nil {
		out.Status = gen.PurchasingReceiptStatus("undone")
		out.UndoneAt, out.UndoneBy, out.UndoNote, out.ReversalMovementId = r.UndoneAt, personOut(r.UndoneByUserID, r.UndoneByDisplay), r.UndoNote, r.ReversalMovementID
	}
	for _, l := range lines {
		if l.ReceiptID != r.ID {
			continue
		}
		line := gen.PurchasingReceiptLine{
			Id: l.ID, Position: l.Position, OrderLineId: l.OrderLineID, VariantId: l.VariantID, Name: l.Name, Unit: l.Unit,
			Quantity: trimmed(ratOrZero(l.Quantity), quantityPlaces), EnteredQuantity: trimmed(ratOrZero(l.EnteredQuantity), quantityPlaces),
			InPackaging: l.InPackaging, PackagingName: l.PackagingName, LocationId: l.LocationID, LocationCode: codes[l.LocationID],
			LotId: l.LotID, LotNumber: l.LotNumber, SupplierLot: l.SupplierLot, ProductionDate: dateOut(l.ProductionDate),
			BestBefore: dateOut(l.BestBefore), Expiry: dateOut(l.Expiry), Serials: l.Serials,
		}
		if line.Serials == nil {
			line.Serials = []string{}
		}
		if l.Sku != "" {
			line.Sku = ptr(l.Sku)
		}
		out.Lines = append(out.Lines, line)
	}
	return out
}

// locationCodes is the site's locations by id, before any transaction.
func (s *server) locationCodes(ctx context.Context, siteID int64) (map[int64]string, error) {
	locs, err := s.siteLocations(ctx, siteID)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]string, len(locs))
	for _, l := range locs {
		out[l.ID] = l.FullCode
	}
	return out, nil
}

// receiptsOf reads receipts and their lines in one snapshot.
func receiptsOf(ctx context.Context, q *store.Queries, receipts []store.PurchasingReceipt) ([]store.ReceiptLinesOfRow, error) {
	ids := make([]int64, 0, len(receipts))
	for _, r := range receipts {
		ids = append(ids, r.ID)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	lines, err := q.ReceiptLinesOf(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("purchasing: read the receipts' lines: %w", err)
	}
	return lines, nil
}

// getReceiptView reads one receipt and answers its view.
func (s *server) getReceiptView(ctx context.Context, id int64) (gen.PurchasingReceipt, error) {
	var r store.PurchasingReceipt
	var o store.PurchasingOrder
	var lines []store.ReceiptLinesOfRow
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		r, err = q.GetReceipt(ctx, id)
		if noRows(err) {
			return notFound("receipt")
		}
		if err != nil {
			return fmt.Errorf("purchasing: read receipt %d: %w", id, err)
		}
		if o, err = q.GetOrder(ctx, r.OrderID); err != nil {
			return fmt.Errorf("purchasing: read order %d: %w", r.OrderID, err)
		}
		lines, err = receiptsOf(ctx, q, []store.PurchasingReceipt{r})
		return err
	})
	if err != nil {
		return gen.PurchasingReceipt{}, err
	}
	codes, err := s.locationCodes(ctx, r.SiteID)
	if err != nil {
		return gen.PurchasingReceipt{}, err
	}
	return receiptView(r, deref64(o.Number), lines, codes), nil
}

// deref64 is *p, 0 when nil.
func deref64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// receiptResult is a receipt and its order as they now stand (A47): the
// order's progress, which has no price, for every caller, and the order's
// full view only for a holder of purchasing:access.
func (s *server) receiptResult(ctx context.Context, receiptID int64) (gen.PurchasingReceiptResult, error) {
	r, err := s.getReceiptView(ctx, receiptID)
	if err != nil {
		return gen.PurchasingReceiptResult{}, err
	}
	progress, order, err := s.orderAfterReceiving(ctx, r.OrderId)
	if err != nil {
		return gen.PurchasingReceiptResult{}, err
	}
	return gen.PurchasingReceiptResult{Receipt: r, Progress: progress, Order: order}, nil
}

// orderAfterReceiving is an order after a receipt, an undo or a delivered
// mark (A47): its progress, and its full view — prices and all — only when
// the caller holds purchasing:access, which purchasing:receive alone does
// not give.
func (s *server) orderAfterReceiving(ctx context.Context, orderID int64) (gen.PurchasingOrderProgress, *gen.PurchasingOrder, error) {
	var o store.PurchasingOrder
	var lines []store.PurchasingOrderLine
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if o, err = q.GetOrder(ctx, orderID); err != nil {
			return fmt.Errorf("purchasing: read order %d: %w", orderID, err)
		}
		lines, err = q.OrderLines(ctx, orderID)
		return err
	})
	if err != nil {
		return gen.PurchasingOrderProgress{}, nil, err
	}
	progress := progressOf(o, lines)
	if !s.has(ctx, permAccess) {
		return progress, nil, nil
	}
	view, err := s.getOrderView(ctx, orderID)
	if err != nil {
		return gen.PurchasingOrderProgress{}, nil, err
	}
	return progress, &view, nil
}

// progressOf is an order as receiving sees it: its state and each line's
// quantities, no price.
func progressOf(o store.PurchasingOrder, lines []store.PurchasingOrderLine) gen.PurchasingOrderProgress {
	out := gen.PurchasingOrderProgress{Id: o.ID, Number: o.Number, Status: gen.PurchasingOrderProgressStatus(o.Status),
		UpdatedAt: o.UpdatedAt, Lines: make([]gen.PurchasingProgressLine, 0, len(lines))}
	for _, l := range lines {
		out.Lines = append(out.Lines, gen.PurchasingProgressLine{
			Id: l.ID, Position: l.Position, Kind: gen.PurchasingProgressLineKind(l.Kind), Name: l.Name, Unit: l.Unit,
			Quantity: trimmed(ratOrZero(l.Quantity), quantityPlaces), Received: trimmed(ratOrZero(l.Received), quantityPlaces),
			Open: trimmed(lineOpen(l), quantityPlaces), DeliveredAt: l.DeliveredAt, ClosedAt: l.ClosedAt,
		})
	}
	return out
}

// ListPurchasingOrderReceipts List a purchase order's receipts
// (GET /api/v1/purchasing/orders/{orderId}/receipts)
func (s *server) ListPurchasingOrderReceipts(ctx context.Context, req gen.ListPurchasingOrderReceiptsRequestObject) (gen.ListPurchasingOrderReceiptsResponseObject, error) {
	var o store.PurchasingOrder
	var receipts []store.PurchasingReceipt
	var lines []store.ReceiptLinesOfRow
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		o, err = q.GetOrder(ctx, req.OrderId)
		if noRows(err) {
			return notFound("purchase order")
		}
		if err != nil {
			return fmt.Errorf("purchasing: read order %d: %w", req.OrderId, err)
		}
		if receipts, err = q.ReceiptsOfOrder(ctx, o.ID); err != nil {
			return fmt.Errorf("purchasing: read order %d's receipts: %w", o.ID, err)
		}
		lines, err = receiptsOf(ctx, q, receipts)
		return err
	})
	if err != nil {
		return answer(err, answers[gen.ListPurchasingOrderReceiptsResponseObject]{
			notFound: func(p gen.PurchasingProblem) gen.ListPurchasingOrderReceiptsResponseObject {
				return gen.ListPurchasingOrderReceipts404ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	out := gen.ListPurchasingOrderReceipts200JSONResponse{Items: make([]gen.PurchasingReceipt, 0, len(receipts))}
	if len(receipts) == 0 {
		return out, nil
	}
	codes, err := s.locationCodes(ctx, o.SiteID)
	if err != nil {
		return nil, err
	}
	for _, r := range receipts {
		out.Items = append(out.Items, receiptView(r, deref64(o.Number), lines, codes))
	}
	return out, nil
}

// GetPurchasingReceipt Get a receipt
// (GET /api/v1/purchasing/receipts/{receiptId})
func (s *server) GetPurchasingReceipt(ctx context.Context, req gen.GetPurchasingReceiptRequestObject) (gen.GetPurchasingReceiptResponseObject, error) {
	view, err := s.getReceiptView(ctx, req.ReceiptId)
	if err != nil {
		return answer(err, answers[gen.GetPurchasingReceiptResponseObject]{
			notFound: func(p gen.PurchasingProblem) gen.GetPurchasingReceiptResponseObject {
				return gen.GetPurchasingReceipt404ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	return gen.GetPurchasingReceipt200JSONResponse(view), nil
}
