package purchasing

// Received, not invoiced (purchasing design P41): per supplier and order
// line, what was received on or before a day and not taken back by its end
// — a text or service line marked delivered by then — less what posted
// invoices dated on or before it bill, valued at the order's net per unit.
// The accountant's basis for a manual accrual at a period's end; nothing is
// posted from it.

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/store"
)

// uninvoiced builds the list as of a day; a nil day is today.
func (s *server) uninvoiced(ctx context.Context, asOf *openapi_types.Date, supplierID *int64) (gen.PurchasingInvoiceUninvoiced, error) {
	zone, err := s.businessZone(ctx)
	if err != nil {
		return gen.PurchasingInvoiceUninvoiced{}, err
	}
	day := dayOf(s.deps.Clock(), zone)
	if asOf != nil {
		day = asOf.Time
	}
	end := dayEnd(day, zone)
	out := gen.PurchasingInvoiceUninvoiced{AsOf: openapi_types.Date{Time: day}, Rows: []gen.PurchasingInvoiceUninvoicedRow{},
		Totals: []gen.PurchasingInvoiceUninvoicedTotal{}}
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		received, err := q.ReceivedAsOf(ctx, store.ReceivedAsOfParams{AsOf: dayCol(day), DayEnd: end})
		if err != nil {
			return fmt.Errorf("purchasing: read what was received: %w", err)
		}
		invoiced, err := q.PostedInvoicedAsOf(ctx, dayCol(day))
		if err != nil {
			return fmt.Errorf("purchasing: read what was invoiced: %w", err)
		}
		lines, err := q.LinesForAccrual(ctx, supplierID)
		if err != nil {
			return fmt.Errorf("purchasing: read the order lines: %w", err)
		}
		in, billed := map[int64]*big.Rat{}, map[int64]*big.Rat{}
		for _, r := range received {
			in[r.OrderLineID] = ratOrZero(r.Received)
		}
		for _, r := range invoiced {
			billed[r.OrderLineID] = ratOrZero(r.Invoiced)
		}
		totals := map[string]*big.Rat{}
		var currencies []string
		for _, l := range lines {
			line := store.PurchasingOrderLine{ID: l.ID, Kind: l.Kind, ProductType: l.ProductType, Quantity: l.Quantity, LineNet: l.LineNet}
			got := in[l.ID]
			if l.Kind != kindVariant || l.ProductType == nil || *l.ProductType != typeGoods {
				got = nil
				if l.DeliveredAt != nil && l.DeliveredAt.Before(end) {
					got = ratOrZero(l.Quantity)
				}
			}
			if got == nil {
				continue
			}
			was := billed[l.ID]
			if was == nil {
				was = new(big.Rat)
			}
			left := new(big.Rat).Sub(got, was)
			if left.Sign() <= 0 {
				continue
			}
			amount := netOf(line, left)
			name := l.Name
			if name == "" {
				name = l.Description
			}
			out.Rows = append(out.Rows, gen.PurchasingInvoiceUninvoicedRow{SupplierId: l.OrderSupplierID, SupplierName: l.OrderSupplierName,
				OrderId: l.OrderID, OrderNumber: l.OrderNumber, OrderLineId: l.ID, Position: l.Position, Name: name, Unit: l.Unit,
				Received: trimmed(got, quantityPlaces), Invoiced: trimmed(was, quantityPlaces), Quantity: trimmed(left, quantityPlaces),
				UnitNet: priceText(roundTo(unitNet(line), pricePlaces)), Amount: amountText(amount), Currency: l.OrderCurrency})
			if totals[l.OrderCurrency] == nil {
				totals[l.OrderCurrency] = new(big.Rat)
				currencies = append(currencies, l.OrderCurrency)
			}
			totals[l.OrderCurrency].Add(totals[l.OrderCurrency], amount)
		}
		for _, c := range currencies {
			out.Totals = append(out.Totals, gen.PurchasingInvoiceUninvoicedTotal{Currency: c, Amount: amountText(totals[c])})
		}
		return nil
	})
	return out, err
}

// ListPurchasingReceivedNotInvoiced List what is received and not invoiced
// (GET /api/v1/purchasing/received-not-invoiced)
func (s *server) ListPurchasingReceivedNotInvoiced(ctx context.Context, req gen.ListPurchasingReceivedNotInvoicedRequestObject) (gen.ListPurchasingReceivedNotInvoicedResponseObject, error) {
	out, err := s.uninvoiced(ctx, req.Params.AsOf, req.Params.SupplierId)
	if err != nil {
		return nil, err
	}
	return gen.ListPurchasingReceivedNotInvoiced200JSONResponse(out), nil
}

// uninvoicedCSV serves the list's file as an attachment, never cached.
type uninvoicedCSV struct {
	name string
	body gen.ExportPurchasingReceivedNotInvoiced200TextcsvResponse
}

func (r uninvoicedCSV) VisitExportPurchasingReceivedNotInvoicedResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="`+r.name+`"`)
	return r.body.VisitExportPurchasingReceivedNotInvoicedResponse(w)
}

// ExportPurchasingReceivedNotInvoiced Download what is received and not
// invoiced as CSV (GET /api/v1/purchasing/received-not-invoiced/csv): the
// form a Norwegian spreadsheet opens without asking, Accounting's own —
// semicolons, a byte order mark, CRLF, the decimal comma, and the formula
// guard on text.
func (s *server) ExportPurchasingReceivedNotInvoiced(ctx context.Context, req gen.ExportPurchasingReceivedNotInvoicedRequestObject) (gen.ExportPurchasingReceivedNotInvoicedResponseObject, error) {
	list, err := s.uninvoiced(ctx, req.Params.AsOf, req.Params.SupplierId)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString("\ufeff")
	writeCSV(&b, []csvCell{{text: "supplier_id"}, {text: "supplier"}, {text: "order"}, {text: "line"}, {text: "item"}, {text: "unit"},
		{text: "received"}, {text: "invoiced"}, {text: "not_invoiced"}, {text: "unit_net"}, {text: "amount"}, {text: "currency"}})
	comma := func(v string) csvCell { return csvCell{text: strings.Replace(v, ".", ",", 1)} }
	for _, r := range list.Rows {
		writeCSV(&b, []csvCell{{text: fmt.Sprint(r.SupplierId)}, {text: r.SupplierName, guard: true}, {text: fmt.Sprint(deref64(r.OrderNumber))},
			{text: fmt.Sprint(r.Position)}, {text: r.Name, guard: true}, {text: r.Unit, guard: true}, comma(r.Received), comma(r.Invoiced),
			comma(r.Quantity), comma(r.UnitNet), comma(r.Amount), {text: r.Currency}})
	}
	data := b.Bytes()
	name := fmt.Sprintf("received-not-invoiced-%s.csv", list.AsOf.Format(time.DateOnly))
	return uninvoicedCSV{name: name, body: gen.ExportPurchasingReceivedNotInvoiced200TextcsvResponse{Body: bytes.NewReader(data),
		ContentLength: int64(len(data))}}, nil
}

// csvCell is one cell before it is written: its text, and whether the
// formula guard applies (Accounting's csvfile.go, copied: no module imports
// another).
type csvCell struct {
	text  string
	guard bool
}

func writeCSV(b *bytes.Buffer, cells []csvCell) {
	for i, c := range cells {
		if i > 0 {
			b.WriteByte(';')
		}
		v := c.text
		if c.guard && strings.IndexAny(v, "=+-@\t\r") == 0 {
			v = "'" + v
		}
		if strings.ContainsAny(v, ";\"\r\n") {
			v = `"` + strings.ReplaceAll(v, `"`, `""`) + `"`
		}
		b.WriteString(v)
	}
	b.WriteString("\r\n")
}
