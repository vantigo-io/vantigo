package purchasing

// Receiving (purchasing design P24–P28, P30, A6, A7, A11, A21): the receive
// screen's read, a scanned code, and Receive itself — one receipt and one
// Inventory movement of kind receipt, in one transaction on Purchasing's
// pool, through contracts.StockLedger.Receive (module-boundaries rule 24).
// Receiving never touches the general ledger.
//
// The lock order (rules 21 and 24, A7): the order's variants through
// Products' LockVariants first, then the order row — so two receipts of one
// order queue, and the second sees the first's quantities —, then the
// receipt counter and the receipt's rows, then Inventory's rows inside
// Receive. Every other contract read — the order's lines as read, today, the
// actor — is made on the pool before the transaction. Inventory's deadlock
// retry is not available on a caller's transaction, so the whole of it, the
// pool read included, is tried up to three times (A7).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/store"
)

// receiveAttempts is how many times a receipt's transaction is tried (A7).
const receiveAttempts = 3

// errRetryReceipt is an attempt's answer when the order's lines changed
// under it between the pool read and the lock, or the database broke a
// deadlock: the whole receipt is tried again.
var errRetryReceipt = errors.New("purchasing: the receipt is tried again")

// isDeadlock reports whether err is PostgreSQL's deadlock_detected (40P01)
// or serialization_failure (40001).
func isDeadlock(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "40P01" || pgErr.Code == "40001")
}

// retryExhausted is the 409 of a receipt still contended on its last try.
func retryExhausted() error {
	return conflict(codeVersionConflict, "Version conflict",
		"The order kept changing while it was received; nothing was received. Try again.")
}

// The ledger codes that are a line's input, answered 400; every other code
// of Inventory's is a state of the stock, answered 409.
var inputStockCodes = []string{
	contracts.StockLotRequired, contracts.StockLotNotAllowed, contracts.StockLotUnknown, contracts.StockLotNumberInvalid,
	contracts.StockLotDatesInvalid, contracts.StockSerialQuantity, contracts.StockLineInvalid, contracts.StockLocationUnknown,
	contracts.StockQuantityInvalid, contracts.StockLotDatesDiffer,
}

// stockRefused turns Inventory's refusal into this module's problem, its
// lines named by rowOf(the ledger line's index) — the request's or the
// receipt's row — and keeping Inventory's codes.
func stockRefused(err error, title string, rowOf func(int) int, inputIsBadRequest bool) error {
	var r *contracts.StockRefusal
	if !errors.As(err, &r) {
		return fmt.Errorf("purchasing: the stock ledger failed: %w", err)
	}
	failures := make([]lineFailure, 0, len(r.Lines))
	seen := map[[2]any]bool{}
	for _, fl := range r.Lines {
		row := rowOf(fl.Index)
		if row < 0 || seen[[2]any{row, fl.Code}] {
			continue
		}
		seen[[2]any{row, fl.Code}] = true
		failures = append(failures, lineFailure{index: row, code: fl.Code, detail: fl.Detail})
	}
	p := linesProblem(title, failures)
	status := int32(http.StatusConflict)
	if inputIsBadRequest && slices.Contains(inputStockCodes, r.Code) {
		status = http.StatusBadRequest
	}
	code := r.Code
	p.Status, p.Code = &status, &code
	if len(failures) == 0 {
		p.Lines = nil
		p.Detail = ptr(r.Detail)
	}
	return refuse(p)
}

// GetPurchasingReceiveForm Get the receive screen of a purchase order
// (GET /api/v1/purchasing/orders/{orderId}/receive)
func (s *server) GetPurchasingReceiveForm(ctx context.Context, req gen.GetPurchasingReceiveFormRequestObject) (gen.GetPurchasingReceiveFormResponseObject, error) {
	resp := answers[gen.GetPurchasingReceiveFormResponseObject]{
		notFound: func(p gen.PurchasingProblem) gen.GetPurchasingReceiveFormResponseObject {
			return gen.GetPurchasingReceiveForm404ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.PurchasingProblem) gen.GetPurchasingReceiveFormResponseObject {
			return gen.GetPurchasingReceiveForm409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.PurchasingProblem) gen.GetPurchasingReceiveFormResponseObject {
			return gen.GetPurchasingReceiveForm503ApplicationProblemPlusJSONResponse(p)
		},
	}
	form, err := s.receiveForm(ctx, req.OrderId)
	if err != nil {
		return answer(err, resp)
	}
	return gen.GetPurchasingReceiveForm200JSONResponse(form), nil
}

// openGoodsLines is an order's receivable lines with something still to
// come, read on the pool; the order is judged open.
func (s *server) openGoodsLines(ctx context.Context, orderID int64) (store.PurchasingOrder, []store.PurchasingOrderLine, error) {
	var o store.PurchasingOrder
	var lines []store.PurchasingOrderLine
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		o, err = q.GetOrder(ctx, orderID)
		if noRows(err) {
			return notFound("purchase order")
		}
		if err != nil {
			return fmt.Errorf("purchasing: read order %d: %w", orderID, err)
		}
		lines, err = q.OrderLines(ctx, orderID)
		return err
	})
	if err != nil {
		return o, nil, err
	}
	if err := judgeSent(o); err != nil {
		return o, nil, err
	}
	open := lines[:0:0]
	for _, l := range lines {
		if receivable(l) && lineOpen(l).Sign() > 0 {
			open = append(open, l)
		}
	}
	return o, open, nil
}

// receiveForm is the receive screen's order: its open goods lines with the
// remainder, each variant's tracking and default location, and the site's
// active locations — no prices (A21).
func (s *server) receiveForm(ctx context.Context, orderID int64) (gen.PurchasingReceiveForm, error) {
	o, open, err := s.openGoodsLines(ctx, orderID)
	if err != nil {
		return gen.PurchasingReceiveForm{}, err
	}
	var ids []int32
	for _, l := range open {
		ids = append(ids, *l.VariantID)
	}
	ids = distinctSorted(ids)
	variants, err := s.catalogVariants(ctx, ids)
	if err != nil {
		return gen.PurchasingReceiveForm{}, err
	}
	sites, _, err := s.sites(ctx)
	if err != nil {
		return gen.PurchasingReceiveForm{}, err
	}
	locs, err := s.siteLocations(ctx, o.SiteID)
	if err != nil {
		return gen.PurchasingReceiveForm{}, err
	}
	holding, err := s.stockLocations(ctx, ids, o.SiteID)
	if err != nil {
		return gen.PurchasingReceiveForm{}, err
	}
	site := sites[o.SiteID]
	out := gen.PurchasingReceiveForm{OrderId: o.ID, Number: o.Number, Status: o.Status, SupplierName: o.SupplierName,
		SiteId: o.SiteID, SiteName: site.Name, UpdatedAt: o.UpdatedAt,
		Lines: make([]gen.PurchasingReceiveLine, 0, len(open)), Locations: []gen.PurchasingLocation{}}
	var root *int64
	for _, l := range locs {
		if !l.Active {
			continue
		}
		if l.Root {
			root = ptr(l.ID)
		}
		out.Locations = append(out.Locations, gen.PurchasingLocation{Id: l.ID, FullCode: l.FullCode, Root: l.Root})
	}
	for _, l := range open {
		v := variants[*l.VariantID]
		line := gen.PurchasingReceiveLine{
			OrderLineId: l.ID, Position: l.Position, VariantId: *l.VariantID, Sku: l.Sku, Name: l.Name, Unit: l.Unit,
			PackagingId: l.PackagingID, PackagingName: l.PackagingName, PackagingQuantity: numericText(l.PackagingFactor, quantityPlaces),
			Quantity: trimmed(ratOrZero(l.Quantity), quantityPlaces), Received: trimmed(ratOrZero(l.Received), quantityPlaces),
			Open: trimmed(lineOpen(l), quantityPlaces), Tracking: gen.PurchasingReceiveLineTracking(trackingOf(v)),
			ExpiryDays: v.ExpiryDays, BestBeforeDays: v.BestBeforeDays, DefaultLocationId: defaultLocation(holding, *l.VariantID, root),
		}
		out.Lines = append(out.Lines, line)
	}
	return out, nil
}

// trackingOf is a variant's tracking mode, none when the catalog says none.
func trackingOf(v contracts.VariantEntry) string {
	if v.Tracking == "lot" || v.Tracking == "serial" {
		return v.Tracking
	}
	return "none"
}

// defaultLocation is where a line is put unless the person says otherwise
// (P24): the variant's primary location at the site when it holds stock
// there, else the location holding most of it, else the site's own node.
func defaultLocation(holding []contracts.StockLocation, variantID int32, root *int64) *int64 {
	var best *contracts.StockLocation
	for i := range holding {
		h := &holding[i]
		if h.VariantID != variantID {
			continue
		}
		if h.Primary {
			return ptr(h.LocationID)
		}
		if best == nil || mustRatOr(h.Quantity).Cmp(mustRatOr(best.Quantity)) > 0 {
			best = h
		}
	}
	if best != nil {
		return ptr(best.LocationID)
	}
	return root
}

// mustRatOr is text as a decimal, 0 when it is not one.
func mustRatOr(text string) *big.Rat {
	r, ok := new(big.Rat).SetString(text)
	if !ok {
		return new(big.Rat)
	}
	return r
}

// LookupPurchasingReceiveCode Look a scanned code up on the receive screen
// (GET /api/v1/purchasing/orders/{orderId}/receive/code)
func (s *server) LookupPurchasingReceiveCode(ctx context.Context, req gen.LookupPurchasingReceiveCodeRequestObject) (gen.LookupPurchasingReceiveCodeResponseObject, error) {
	resp := answers[gen.LookupPurchasingReceiveCodeResponseObject]{
		badRequest: func(p gen.PurchasingProblem) gen.LookupPurchasingReceiveCodeResponseObject {
			return gen.LookupPurchasingReceiveCode400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func(p gen.PurchasingProblem) gen.LookupPurchasingReceiveCodeResponseObject {
			return gen.LookupPurchasingReceiveCode404ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.PurchasingProblem) gen.LookupPurchasingReceiveCodeResponseObject {
			return gen.LookupPurchasingReceiveCode503ApplicationProblemPlusJSONResponse(p)
		},
	}
	code := strings.TrimSpace(req.Params.Code)
	if code == "" || utf8.RuneCountInString(code) > 100 {
		return answer(refuseField(invalidQueryTitle, "code", "", "A code is 1 to 100 characters"), resp)
	}
	_, open, err := s.openGoodsLines(ctx, req.OrderId)
	if err != nil {
		// A closed or received order has no open line: no match, not a refusal.
		if status, _, ok := answerOf(err); !ok || status != http.StatusConflict {
			return answer(err, resp)
		}
	}
	out := gen.LookupPurchasingReceiveCode200JSONResponse{Items: []gen.PurchasingReceiveCode{}}
	if len(open) == 0 {
		return out, nil
	}
	var matches []contracts.CodeMatch
	for _, c := range codeForms(code) {
		if matches, err = s.lookupCode(ctx, c); err != nil {
			return nil, err
		}
		if len(matches) > 0 {
			break
		}
	}
	for _, l := range open {
		for _, m := range matches {
			if m.Variant.ID != *l.VariantID {
				continue
			}
			item := gen.PurchasingReceiveCode{OrderLineId: l.ID, Quantity: "1"}
			if m.Packaging != nil {
				item.PackagingId, item.PackagingName = ptr(m.Packaging.ID), ptr(m.Packaging.Name)
				item.Quantity = trimmed(mustRatOr(m.Packaging.Quantity), quantityPlaces)
			}
			out.Items = append(out.Items, item)
			break
		}
	}
	return out, nil
}

// codeForms is a scanned code and, for a GTIN-14 with leading zeros, the
// shorter GTINs a barcode is kept as (GTIN-13, -12, -8).
func codeForms(code string) []string {
	out := []string{code}
	if len(code) != 14 || strings.Trim(code, "0123456789") != "" {
		return out
	}
	for _, n := range []int{13, 12, 8} {
		if strings.Trim(code[:14-n], "0") == "" {
			out = append(out, code[14-n:])
		}
	}
	return out
}

// plannedReceiptLine is one checked line of a receipt request.
type plannedReceiptLine struct {
	in          gen.PurchasingReceiptLineInput
	orderLine   store.PurchasingOrderLine
	quantity    *big.Rat // in the variant's unit
	entered     *big.Rat
	inPackaging bool
}

// receiptRequest is a Receive's checked input.
type receiptRequest struct {
	orderID      int64
	clientID     [16]byte
	hash         string
	deliveryNote string
	note         string
	acceptOver   bool
	lines        []gen.PurchasingReceiptLineInput
}

// receiptHash is what a replay is judged by (A11): the order and the body
// without its clientRequestId.
func receiptHash(orderID int64, b gen.PurchasingReceiptInput) string {
	b.ClientRequestId = [16]byte{}
	body, _ := json.Marshal(struct {
		OrderID int64                      `json:"orderId"`
		Body    gen.PurchasingReceiptInput `json:"body"`
	}{orderID, b})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// planReceipt judges every line of the request against the order's lines
// as read, naming every failing line: a line that is not an open goods line
// of the order (line_not_receivable), a quantity that is not more than 0
// with at most three decimals (quantity_invalid), one in packagings on a
// line without one (packaging_not_found).
func planReceipt(lines []store.PurchasingOrderLine, in []gen.PurchasingReceiptLineInput) ([]plannedReceiptLine, []lineFailure) {
	byID := make(map[int64]store.PurchasingOrderLine, len(lines))
	for _, l := range lines {
		byID[l.ID] = l
	}
	var failures []lineFailure
	out := make([]plannedReceiptLine, 0, len(in))
	for i, l := range in {
		ol, ok := byID[l.OrderLineId]
		if !ok || !receivable(ol) {
			failures = append(failures, lineFailure{index: i, code: codeLineNotReceivable,
				detail: "The line is not an open goods line of this order."})
			continue
		}
		entered, ok := parseDecimal(l.Quantity, quantityPlaces, maxQuantity)
		if !ok || entered.Sign() <= 0 {
			failures = append(failures, lineFailure{index: i, code: codeQuantityInvalid,
				detail: "A quantity is more than 0, with at most three decimals."})
			continue
		}
		p := plannedReceiptLine{in: l, orderLine: ol, entered: entered, quantity: entered}
		if l.InPackaging != nil && *l.InPackaging {
			factor := ratOf(ol.PackagingFactor)
			if factor == nil || factor.Sign() <= 0 {
				failures = append(failures, lineFailure{index: i, code: codePackagingNotFound,
					detail: "The line has no purchase packaging; give the quantity in the unit."})
				continue
			}
			p.inPackaging = true
			p.quantity = roundTo(new(big.Rat).Mul(entered, factor), quantityPlaces)
			if p.quantity.Sign() <= 0 || p.quantity.Cmp(maxQuantity) > 0 {
				failures = append(failures, lineFailure{index: i, code: codeQuantityInvalid, detail: "The quantity is out of range."})
				continue
			}
		}
		out = append(out, p)
	}
	return out, failures
}

// ReceivePurchasingOrder Receive goods on a purchase order
// (POST /api/v1/purchasing/orders/{orderId}/receipts)
func (s *server) ReceivePurchasingOrder(ctx context.Context, req gen.ReceivePurchasingOrderRequestObject) (gen.ReceivePurchasingOrderResponseObject, error) {
	resp := answers[gen.ReceivePurchasingOrderResponseObject]{
		badRequest: func(p gen.PurchasingProblem) gen.ReceivePurchasingOrderResponseObject {
			return gen.ReceivePurchasingOrder400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func(p gen.PurchasingProblem) gen.ReceivePurchasingOrderResponseObject {
			return gen.ReceivePurchasingOrder404ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.PurchasingProblem) gen.ReceivePurchasingOrderResponseObject {
			return gen.ReceivePurchasingOrder409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.PurchasingProblem) gen.ReceivePurchasingOrderResponseObject {
			return gen.ReceivePurchasingOrder503ApplicationProblemPlusJSONResponse(p)
		},
	}
	b := *req.Body
	var f fieldErrors
	r := receiptRequest{orderID: req.OrderId, clientID: b.ClientRequestId, hash: receiptHash(req.OrderId, b),
		acceptOver: b.AcceptOver != nil && *b.AcceptOver, lines: b.Lines}
	if b.DeliveryNote != nil {
		if r.deliveryNote = strings.TrimSpace(*b.DeliveryNote); utf8.RuneCountInString(r.deliveryNote) > 100 {
			f.add("deliveryNote", "A delivery note number is at most 100 characters")
		}
	}
	if b.Note != nil {
		if r.note = strings.TrimSpace(*b.Note); utf8.RuneCountInString(r.note) > 1000 {
			f.add("note", "A note is at most 1000 characters")
		}
	}
	switch {
	case len(b.Lines) == 0:
		f.add("lines", "A receipt has at least one line")
	case len(b.Lines) > maxLines:
		f.addCoded("lines", codeTooManyLines, fmt.Sprintf("A receipt has at most %d lines", maxLines))
	}
	if f.failed() {
		return gen.ReceivePurchasingOrder400ApplicationProblemPlusJSONResponse(invalid(invalidReceiptTitle, f)), nil
	}
	id, err := s.receive(ctx, r)
	if err != nil {
		return answer(err, resp)
	}
	out, err := s.receiptResult(ctx, id)
	if err != nil {
		return nil, err
	}
	return gen.ReceivePurchasingOrder201JSONResponse(out), nil
}

// replayed answers the receipt a clientRequestId made, judging the replay:
// the same order and body is that receipt; anything else is
// client_request_reused. ok is false when the id made none.
func replayed(ctx context.Context, q *store.Queries, r receiptRequest) (int64, bool, error) {
	prior, err := q.ReceiptByRequest(ctx, r.clientID)
	if noRows(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("purchasing: read the receipt of a request: %w", err)
	}
	if prior.OrderID != r.orderID || prior.RequestHash != r.hash {
		return 0, false, clientRequestReused()
	}
	return prior.ID, true, nil
}

// receive posts the receipt, trying the whole of it up to receiveAttempts
// times (A7), and answers its id.
func (s *server) receive(ctx context.Context, r receiptRequest) (int64, error) {
	if id, ok, err := replayed(ctx, s.store(), r); err != nil || ok {
		return id, err
	}
	for attempt := 1; ; attempt++ {
		id, err := s.receiveOnce(ctx, r)
		if errors.Is(err, errRetryReceipt) || isDeadlock(err) {
			if attempt < receiveAttempts {
				continue
			}
			return 0, retryExhausted()
		}
		if db.IsUniqueViolation(err, "ux_receipts_client_request") {
			id, ok, rerr := replayed(ctx, s.store(), r)
			if rerr != nil {
				return 0, rerr
			}
			if !ok {
				return 0, clientRequestReused()
			}
			return id, nil
		}
		return id, err
	}
}

// receiveOnce is one attempt: the pool reads, then the transaction.
func (s *server) receiveOnce(ctx context.Context, r receiptRequest) (int64, error) {
	o, lines, err := s.orderAndLines(ctx, r.orderID)
	if err != nil {
		return 0, err
	}
	if err := judgeSent(o); err != nil {
		return 0, err
	}
	planned, failures := planReceipt(lines, r.lines)
	if len(failures) > 0 {
		return 0, refuseLines(invalidReceiptTitle, failures)
	}
	zone, err := s.businessZone(ctx)
	if err != nil {
		return 0, err
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return 0, err
	}
	if s.deps.StockLedger == nil {
		return 0, errDependencyUnavailable
	}
	variants := make([]int32, 0, len(planned))
	for _, p := range planned {
		variants = append(variants, *p.orderLine.VariantID)
	}
	var receiptID int64
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if _, err := s.lockVariants(ctx, tx, variants); err != nil {
			return err
		}
		locked, err := txq.LockOrder(ctx, o.ID)
		if err != nil {
			return fmt.Errorf("purchasing: lock order %d: %w", o.ID, err)
		}
		if id, ok, err := replayed(ctx, txq, r); err != nil || ok {
			receiptID = id
			return err
		}
		if err := judgeSent(locked); err != nil {
			return err
		}
		now := s.deps.Clock()
		today := dayOf(now, zone)
		current, err := txq.OrderLines(ctx, o.ID)
		if err != nil {
			return fmt.Errorf("purchasing: read order %d's lines: %w", o.ID, err)
		}
		fresh, failures := planReceipt(current, r.lines)
		if len(failures) > 0 {
			return refuseLines(invalidReceiptTitle, failures)
		}
		for i := range fresh {
			if *fresh[i].orderLine.VariantID != *planned[i].orderLine.VariantID {
				return errRetryReceipt // a line's variant is not the one locked
			}
		}
		if err := judgeOver(fresh, r.acceptOver); err != nil {
			return err
		}
		number, err := txq.AllocateNumber(ctx, store.AllocateNumberParams{CounterName: seriesReceipt, SeriesStart: 1})
		if err != nil {
			return fmt.Errorf("purchasing: allocate a receipt number: %w", err)
		}
		rec, err := txq.InsertReceipt(ctx, store.InsertReceiptParams{
			Number: number, OrderID: o.ID, SiteID: locked.SiteID, ReceivedOn: dayCol(today), DeliveryNote: r.deliveryNote,
			Note: r.note, ReceivedByUserID: by.id, ReceivedByDisplay: by.display, ClientRequestID: r.clientID,
			RequestHash: r.hash, Now: now,
		})
		if err != nil {
			return fmt.Errorf("purchasing: insert the receipt of order %d: %w", o.ID, err)
		}
		ledger := make([]contracts.ReceiveLine, 0, len(fresh))
		for i, p := range fresh {
			params, line := receiptLineOf(rec.ID, int32(i+1), p, locked.Currency)
			if err := txq.InsertReceiptLine(ctx, params); err != nil {
				return fmt.Errorf("purchasing: insert a line of receipt %d: %w", rec.ID, err)
			}
			ledger = append(ledger, line)
		}
		posted, err := s.deps.StockLedger.Receive(ctx, tx, orderSource(o.ID), contracts.StockReceipt{
			Document: receiptSource(rec.ID), SiteID: locked.SiteID, Lines: ledger, SupplierName: locked.SupplierName,
			SupplierReference: r.deliveryNote, Note: r.note, PostedByUserID: by.id, PostedByDisplay: deref(by.display), Today: today,
		})
		if err != nil {
			if isDeadlock(err) {
				return err
			}
			return stockRefused(err, "The goods cannot be received", func(i int) int { return i }, true)
		}
		if err := txq.SetReceiptMovement(ctx, store.SetReceiptMovementParams{ID: rec.ID, MovementID: &posted.MovementID}); err != nil {
			return fmt.Errorf("purchasing: link receipt %d's movement: %w", rec.ID, err)
		}
		written, err := txq.ReceiptLinesOf(ctx, []int64{rec.ID})
		if err != nil {
			return fmt.Errorf("purchasing: read receipt %d's lines: %w", rec.ID, err)
		}
		sums, order := receivedByLine(written)
		for _, id := range order {
			if err := txq.AddReceived(ctx, store.AddReceivedParams{ID: id, Delta: numeric(sums[id], quantityPlaces)}); err != nil {
				return fmt.Errorf("purchasing: count line %d received: %w", id, err)
			}
		}
		status, err := setDerivedStatus(ctx, txq, locked, now)
		if err != nil {
			return err
		}
		actions := []change{{Action: "receipt_posted", To: ptr(fmt.Sprint(number))}}
		if status == statusReceived && locked.Status != statusReceived {
			actions = append(actions, change{Action: "received"})
		}
		if err := logChange(ctx, txq, o.ID, by, now, actions...); err != nil {
			return err
		}
		ev := contracts.PurchaseReceiptPosted{
			ReceiptID: rec.ID, Number: number, OrderID: o.ID, OrderNumber: deref64(locked.Number), SupplierID: locked.SupplierID,
			SiteID: locked.SiteID, MovementID: posted.MovementID, ReceivedOn: today.Format(time.DateOnly),
			Lines: receiptEventLines(written), PostedAt: now,
		}
		if err := events.Publish(ctx, tx, moduleName, ev, now); err != nil {
			return fmt.Errorf("purchasing: publish receipt %d: %w", rec.ID, err)
		}
		receiptID = rec.ID
		return nil
	})
	return receiptID, err
}

// orderAndLines reads an order and its lines in one snapshot.
func (s *server) orderAndLines(ctx context.Context, id int64) (store.PurchasingOrder, []store.PurchasingOrderLine, error) {
	var o store.PurchasingOrder
	var lines []store.PurchasingOrderLine
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		o, err = q.GetOrder(ctx, id)
		if noRows(err) {
			return notFound("purchase order")
		}
		if err != nil {
			return fmt.Errorf("purchasing: read order %d: %w", id, err)
		}
		lines, err = q.OrderLines(ctx, id)
		return err
	})
	return o, lines, err
}

// judgeOver refuses a receipt that takes a line over its remainder unless
// the request accepts it (P26): every row of such a line is named.
func judgeOver(planned []plannedReceiptLine, acceptOver bool) error {
	if acceptOver {
		return nil
	}
	sums := map[int64]*big.Rat{}
	for _, p := range planned {
		if sums[p.orderLine.ID] == nil {
			sums[p.orderLine.ID] = new(big.Rat)
		}
		sums[p.orderLine.ID].Add(sums[p.orderLine.ID], p.quantity)
	}
	var failures []lineFailure
	for i, p := range planned {
		open := lineOpen(p.orderLine)
		if sums[p.orderLine.ID].Cmp(open) > 0 {
			failures = append(failures, lineFailure{index: i, code: codeOverReceipt, detail: fmt.Sprintf(
				"%s: %s %s received of %s; %s more is over the remainder.", p.orderLine.Name,
				trimmed(ratOrZero(p.orderLine.Received), quantityPlaces), p.orderLine.Unit,
				trimmed(ratOrZero(p.orderLine.Quantity), quantityPlaces), trimmed(sums[p.orderLine.ID], quantityPlaces))})
		}
	}
	if len(failures) == 0 {
		return nil
	}
	pr := linesProblem("More than ordered", failures)
	status, code := int32(http.StatusConflict), codeOverReceipt
	pr.Status, pr.Code = &status, &code
	pr.Detail = ptr("A line is received beyond what is left of it; send acceptOver to receive it anyway. Nothing was received.")
	return refuse(pr)
}

// receiptLineOf is a planned line's row and its ledger line.
func receiptLineOf(receiptID int64, position int32, p plannedReceiptLine, currency string) (store.InsertReceiptLineParams, contracts.ReceiveLine) {
	params := store.InsertReceiptLineParams{
		ReceiptID: receiptID, Position: position, OrderLineID: p.orderLine.ID, VariantID: *p.orderLine.VariantID,
		LocationID: p.in.LocationId, Quantity: numeric(p.quantity, quantityPlaces), EnteredQuantity: numeric(p.entered, quantityPlaces),
		InPackaging: p.inPackaging, LotID: p.in.LotId, UnitPrice: p.orderLine.UnitPrice, Currency: currency, Serials: []string{},
	}
	if p.inPackaging {
		params.PackagingName = p.orderLine.PackagingName
	}
	line := contracts.ReceiveLine{VariantID: *p.orderLine.VariantID, LineID: p.orderLine.ID, ToLocationID: p.in.LocationId,
		Quantity: trimmed(p.quantity, quantityPlaces)}
	if p.in.LotId != nil {
		line.LotID = *p.in.LotId
	}
	if p.in.Lot != nil {
		l := p.in.Lot
		lot := &contracts.ReceiveLot{Number: strings.TrimSpace(deref(l.Number)), SupplierLot: strings.TrimSpace(deref(l.SupplierLot)),
			ProductionDate: dateFromAPI(l.ProductionDate), BestBefore: dateFromAPI(l.BestBefore), Expiry: dateFromAPI(l.Expiry)}
		line.Lot = lot
		params.LotNumber, params.SupplierLot = textPtr(lot.Number), textPtr(lot.SupplierLot)
		params.ProductionDate, params.BestBefore, params.Expiry = dateOf(lot.ProductionDate), dateOf(lot.BestBefore), dateOf(lot.Expiry)
		params.LotNumber = truncPtr(params.LotNumber, 20)
		params.SupplierLot = truncPtr(params.SupplierLot, 40)
	}
	if p.in.Serials != nil && len(*p.in.Serials) > 0 {
		serials := make([]string, 0, len(*p.in.Serials))
		for _, sn := range *p.in.Serials {
			serials = append(serials, strings.TrimSpace(sn))
		}
		line.Serials = serials
		params.Serials = serials
	}
	return params, line
}

// truncPtr is *p cut to n characters; the ledger refuses what is longer
// than it keeps, so this only keeps the row's column from failing first.
func truncPtr(p *string, n int) *string {
	if p == nil {
		return nil
	}
	return ptr(truncate(*p, n))
}
