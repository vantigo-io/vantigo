package orders

// The order's side of invoicing from orders (orders design O26, O36, O41):
// its invoice allocations as the order page shows them — the invoices issued
// from it and each line's draft-allocated quantity — the rules a draft
// allocation sets on an edit and a cancel, and the order page's Release
// draft allocations. The allocations themselves are written by
// BillableOrders (billable_orders.go) and stamped by the invoiced-work
// holder (invoiced_work.go).

import (
	"context"
	"fmt"
	"math/big"
	"strconv"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// orderAllocations is every invoice allocation of an order on q, in id
// order.
func orderAllocations(ctx context.Context, q *store.Queries, orderID int64) ([]store.OrdersInvoiceAllocation, error) {
	rows, err := q.OrderAllocations(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("orders: read order %d's invoice allocations: %w", orderID, err)
	}
	return rows, nil
}

// lineAllocation is what an order line's allocations hold: the quantity of
// its draft allocations, and whether any allocation keeps the line — a
// draft, an invoiced one, or a released one with an invoice's stamp, which
// the order's invoices list (O41).
type lineAllocation struct {
	draft *big.Rat
	held  bool
}

// lineAllocations are an order's allocations by line id.
type lineAllocations map[int64]lineAllocation

// drafted is what a line's draft allocations hold, 0 for none.
func (a lineAllocations) drafted(lineID int64) *big.Rat {
	return orZero(a[lineID].draft)
}

// allocationsByLine sums an order's allocations per line.
func allocationsByLine(rows []store.OrdersInvoiceAllocation) lineAllocations {
	out := lineAllocations{}
	for _, r := range rows {
		l := out[r.LineID]
		if r.State == contracts.AllocationDraft {
			l.draft = new(big.Rat).Add(orZero(l.draft), ratOrZero(r.Quantity))
		}
		if r.State != contracts.AllocationReleased || r.InvoiceID != nil {
			l.held = true
		}
		out[r.LineID] = l
	}
	return out
}

// orderInvoicesOut are the invoices an order's allocations were stamped by
// (O41), in the order they were first stamped: each invoice's number, issue
// date and the amount of the order it bills, in the order's currency;
// released when every allocation of it was given back by a credit note.
func orderInvoicesOut(rows []store.OrdersInvoiceAllocation, currency string) []gen.OrdersOrderInvoice {
	out := []gen.OrdersOrderInvoice{}
	at := map[int64]int{}
	amounts := map[int64]*big.Rat{}
	for _, r := range rows {
		if r.InvoiceID == nil || r.InvoiceNumber == nil {
			continue
		}
		id := *r.InvoiceID
		i, ok := at[id]
		if !ok {
			i = len(out)
			at[id] = i
			amounts[id] = new(big.Rat)
			out = append(out, gen.OrdersOrderInvoice{
				InvoiceId: id, InvoiceNumber: *r.InvoiceNumber, IssueDate: openapi_types.Date{Time: r.IssueDate.Time},
				Currency: currency, State: gen.OrdersOrderInvoiceStateReleased,
			})
		}
		amounts[id].Add(amounts[id], ratOrZero(r.Amount))
		out[i].Amount = wire(amounts[id], amountPlaces)
		if r.State == contracts.AllocationInvoiced {
			out[i].State = gen.OrdersOrderInvoiceStateInvoiced
		}
	}
	return out
}

// judgeNoDraftInvoice refuses a cancel of an order a draft invoice holds an
// allocation of (order_in_draft_invoice, O36). Orders cannot name the draft;
// the refusal offers Release draft allocations instead. The caller holds the
// order's row, which every Allocate takes first.
func judgeNoDraftInvoice(ctx context.Context, txq *store.Queries, o store.OrdersOrder) error {
	rows, err := orderAllocations(ctx, txq, o.ID)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.State == contracts.AllocationDraft {
			return conflict(codeOrderInDraftInvoice, "The order is in a draft invoice",
				fmt.Sprintf("Part of order %d is in a draft invoice. Issue or delete the draft, or release the order's draft allocations, first.", o.Number))
		}
	}
	return nil
}

// ReleaseSalesOrderDraftAllocations Release an order's draft allocations
// (POST /api/v1/orders/orders/{id}/release-draft-allocations)
//
// Every draft allocation of the order is released at once (O36), so its
// quantity is offered to invoice again; a draft invoice still holding one
// is then refused at issue (source_not_invoiceable). It is logged as
// draft_allocations_released, with how many, at the version it found, and
// moves the order's updated_at; an order with none is answered as it is,
// and nothing is logged.
func (s *server) ReleaseSalesOrderDraftAllocations(ctx context.Context, req gen.ReleaseSalesOrderDraftAllocationsRequestObject) (gen.ReleaseSalesOrderDraftAllocationsResponseObject, error) {
	out, err := s.changeOrder(ctx, req.Id, req.Body.Version, req.Body.ExpectedUpdatedAt, false,
		func(ctx context.Context, tx pgx.Tx, txq *store.Queries, o store.OrdersOrder, lines []store.OrdersLine) (store.OrdersOrder, []changeAction, error) {
			now := s.deps.Clock()
			released, err := txq.ReleaseOrderDraftAllocations(ctx, store.ReleaseOrderDraftAllocationsParams{OrderID: o.ID, Now: &now})
			if err != nil {
				return o, nil, fmt.Errorf("orders: release order %d's draft allocations: %w", o.ID, err)
			}
			if len(released) == 0 {
				return o, nil, nil
			}
			row, err := s.bumpProgress(ctx, tx, txq, o, lines, o.Version)
			if err != nil {
				return o, nil, err
			}
			return row, []changeAction{{Kind: actionDraftAllocationsReleased, To: ptr(strconv.Itoa(len(released)))}}, nil
		})
	if err != nil {
		return answer(err, orderAnswers(nil,
			func() gen.ReleaseSalesOrderDraftAllocationsResponseObject {
				return gen.ReleaseSalesOrderDraftAllocations404Response{}
			},
			func(p gen.OrdersProblem) gen.ReleaseSalesOrderDraftAllocationsResponseObject {
				return gen.ReleaseSalesOrderDraftAllocations409ApplicationProblemPlusJSONResponse(p)
			}))
	}
	return gen.ReleaseSalesOrderDraftAllocations200JSONResponse(out), nil
}
