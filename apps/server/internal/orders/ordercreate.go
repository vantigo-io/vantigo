package orders

// The one create command of an order (orders design O28): createOrder is the
// only path that writes a new order. Accept (accept.go) uses it now; the API's
// create, and a channel's later, use it unchanged. It is not a contract in
// this phase; it becomes contracts.SalesOrders.Create with its first channel
// (§14).

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// The order states (O18).
const (
	orderDraft     = "draft"
	orderConfirmed = "confirmed"
	orderCancelled = "cancelled"
)

// OrderOrigin is where a new order comes from (O27): its channel — manual,
// quote, copy, and later a channel's own — and the document, module or
// channel record it was made from.
type OrderOrigin struct {
	Channel         string
	SourceModule    *string
	SourceKind      *string
	SourceID        *string
	ExternalID      *string
	ExternalNumber  *string
	ClientRequestID *uuid.UUID
}

// CreateOrder is everything a new order is made from (O28): its origin, its
// header with the customer's snapshot (header.go), its priced lines
// (pricing.go) and who made it. A channel may hand in lines priced with
// price_source channel; their prices are kept, and only the amounts are
// computed, as every line's are.
type CreateOrder struct {
	Origin    OrderOrigin
	Header    header
	Lines     []pricedLine
	CreatedBy actor
	// SiteID is the Inventory site the order is fulfilled from (O46); nil
	// takes the settings' default site while Inventory keeps stock.
	SiteID *int64
}

// createOrder writes a new draft order and its lines on the caller's
// transaction, and answers them as stored: the order series' next number
// (O12), the header and its totals, the lines as given — an optional line is
// refused (optional_not_allowed), too many lines are refused
// (too_many_lines).
//
// The caller has locked the lines' variants on tx first (s.lockLines, MB rule
// 21) — before any of Orders' own rows — and has read every contract the
// header and the lines needed before the transaction. createOrder makes no
// call out of the module and publishes nothing: confirming is a step of its
// own (confirmOrderTx).
func (s *server) createOrder(ctx context.Context, _ pgx.Tx, txq *store.Queries, c CreateOrder) (store.OrdersOrder, []store.OrdersLine, error) {
	if len(c.Lines) > maxLines {
		return store.OrdersOrder{}, nil, refuseLines(invalidOrderTitle, []lineFailure{{index: maxLines, code: codeTooManyLines,
			detail: fmt.Sprintf("An order has at most %d lines.", maxLines)}})
	}
	var failures []lineFailure
	for i, l := range c.Lines {
		if l.optional {
			failures = append(failures, lineFailure{index: i, code: codeOptionalNotAllowed, detail: "An order has no optional lines."})
		}
	}
	if len(failures) > 0 {
		return store.OrdersOrder{}, nil, refuseLines(invalidOrderTitle, failures)
	}
	settings, err := readSettings(ctx, txq)
	if err != nil {
		return store.OrdersOrder{}, nil, err
	}
	number, err := allocateNumber(ctx, txq, seriesOrder, settings.orderSeriesStart)
	if err != nil {
		return store.OrdersOrder{}, nil, err
	}
	site := c.SiteID
	if site == nil && s.stockKept() {
		site = settings.defaultSiteID
	}
	settlePolicies(ctx, c.Lines, nil, s.atSite(site), false)
	h, t := c.Header, totalsOfPriced(c.Lines)
	row, err := txq.InsertOrder(ctx, store.InsertOrderParams{
		Number:     number,
		CustomerID: h.CustomerID, CustomerNumber: h.CustomerNumber, CustomerName: h.CustomerName, CustomerType: h.CustomerType,
		CustomerOrganisationNumber: h.CustomerOrganisationNumber, CustomerForeignID: h.CustomerForeignID,
		CustomerCountry: h.CustomerCountry, InvoiceAddressLine1: h.InvoiceAddressLine1, InvoiceAddressLine2: h.InvoiceAddressLine2,
		InvoicePostalCode: h.InvoicePostalCode, InvoiceCity: h.InvoiceCity, InvoiceCountry: h.InvoiceCountry,
		DeliveryAddressID: h.DeliveryAddressID, DeliveryName: h.DeliveryName, DeliveryAddressLine1: h.DeliveryAddressLine1,
		DeliveryAddressLine2: h.DeliveryAddressLine2, DeliveryPostalCode: h.DeliveryPostalCode, DeliveryCity: h.DeliveryCity,
		DeliveryCountry: h.DeliveryCountry, ContactID: h.ContactID, ContactName: h.ContactName, ContactEmail: h.ContactEmail,
		ContactPhone: h.ContactPhone, CustomerInvoiceEmail: h.CustomerInvoiceEmail, YourReference: h.YourReference, SalespersonID: h.SalespersonID,
		SalespersonDisplay: h.SalespersonDisplay, ProjectID: h.ProjectID, Currency: h.Currency,
		PricesIncludeVat: h.PricesIncludeVat, PaymentTermsDays: h.PaymentTermsDays, DiscountVisible: h.DiscountVisible,
		Language: h.Language, IntroText: h.IntroText, ClosingText: h.ClosingText, InternalNote: h.InternalNote,
		DeliveryDate: h.DeliveryDate,
		NetTotal:     numeric(t.net, amountPlaces), VatTotal: numeric(t.vat, amountPlaces), GrossTotal: numeric(t.gross, amountPlaces),
		Channel: c.Origin.Channel, SourceModule: c.Origin.SourceModule, SourceKind: c.Origin.SourceKind, SourceID: c.Origin.SourceID,
		ExternalID: c.Origin.ExternalID, ExternalNumber: c.Origin.ExternalNumber, ClientRequestID: c.Origin.ClientRequestID,
		CreatedByUserID: c.CreatedBy.id, CreatedByDisplay: c.CreatedBy.display, Now: s.deps.Clock(), SiteID: site,
	})
	if err != nil {
		return store.OrdersOrder{}, nil, fmt.Errorf("orders: insert order: %w", err)
	}
	lines, err := writeLines(ctx, txq, ownerOrder, row.ID, c.Lines)
	if err != nil {
		return store.OrdersOrder{}, nil, err
	}
	return row, lines, nil
}

// confirmOrderTx confirms a draft order on the caller's transaction (O18),
// the caller holding the order's row and its lines' variants: it judges the
// order confirmable (customer_disabled, order_empty, line_unpriced,
// vat_code_unmapped), freezes it, records who confirmed it, reserves its
// goods lines' open quantity at its site when reserve is set (O46,
// insufficient_available), publishes orders.sales_order.confirmed.v1 on tx,
// and answers the order as it now is.
// It makes no call out of the module: the customer's billing profile was read
// before the transaction and rides in as customer. lifecycle.go sets it.
var confirmOrderTx func(ctx context.Context, s *server, tx pgx.Tx, txq *store.Queries, order store.OrdersOrder, lines []store.OrdersLine, o warningOverride,
	by actor, customer *contracts.CustomerBillingProfile, reserve bool) (store.OrdersOrder, error)

// afterOrderConfirmed is what follows a confirmation's commit, outside any
// transaction: storing the confirmation's PDF (O24), printed as body inside
// the confirm's transaction from the rows it confirmed, at the version the
// confirm produced. lifecycle.go sets it.
var afterOrderConfirmed func(ctx context.Context, s *server, order store.OrdersOrder, body []byte)

// orderView is an order with its lines as the API answers it (O13, O18),
// with the sales warnings that apply to it (documentWarnings) and, from its
// invoice allocations, the invoices issued from it (O41) and each line's
// draft-allocated quantity; nil allocations for an order that can have none
// yet — a new, a draft or a just-confirmed one.
func orderView(o store.OrdersOrder, lines []store.OrdersLine, warnings []gen.OrdersWarning, allocations []store.OrdersInvoiceAllocation) gen.OrdersOrder {
	h := headerOfOrder(o)
	out := gen.OrdersOrder{
		Id: o.ID, Number: o.Number, Status: gen.OrdersOrderStatus(o.Status), OnHold: o.OnHold, HoldReason: o.HoldReason,
		Locked: o.Locked, Version: o.Version, DeliveryStatus: gen.OrdersOrderDeliveryStatus(o.DeliveryStatus),
		InvoiceStatus: gen.OrdersOrderInvoiceStatus(o.InvoiceStatus),
		Done:          isDone(o, lines),
		Customer:      snapshotOut(h), DefaultRecipient: defaultRecipient(h), YourReference: o.YourReference, Currency: o.Currency, PricesIncludeVat: o.PricesIncludeVat,
		PaymentTermsDays: o.PaymentTermsDays, DiscountVisible: o.DiscountVisible, Language: gen.OrdersOrderLanguage(o.Language),
		IntroText: o.IntroText, ClosingText: o.ClosingText, InternalNote: o.InternalNote,
		Origin: gen.OrdersOrigin{
			Channel: o.Channel, SourceModule: o.SourceModule, SourceKind: o.SourceKind, SourceId: o.SourceID,
			ExternalId: o.ExternalID, ExternalNumber: o.ExternalNumber, PaymentReference: o.PaymentReference,
			PrepaidAmount: wirePtr(ratOf(o.PrepaidAmount), amountPlaces),
		},
		Totals: totalsOut(totalsOfRows(lines)), Lines: linesOut(lines, o.PricesIncludeVat),
		CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt, ProjectId: o.ProjectID,
		Salesperson: personOut(o.SalespersonID, o.SalespersonDisplay), DeliveryDate: dateOut(o.DeliveryDate),
		ConfirmedAt: o.ConfirmedAt, ConfirmedBy: personOut(o.ConfirmedByUserID, o.ConfirmedByDisplay), CancelledAt: o.CancelledAt,
		SourceQuoteId: sourceQuoteID(o), Warnings: warningsOrEmpty(warnings), SiteId: o.SiteID,
		Invoices: orderInvoicesOut(allocations, o.Currency),
	}
	drafts := allocationsByLine(allocations)
	for i := range out.Lines {
		out.Lines[i].QtyDraftAllocated = wire(drafts.drafted(out.Lines[i].Id), quantityPlaces)
	}
	return out
}

// sourceQuoteID is the quote an order was accepted from, when its origin
// names one ("<id>@<revision>", O27); nil otherwise.
func sourceQuoteID(o store.OrdersOrder) *int64 {
	if o.Channel != channelQuote || o.SourceKind == nil || *o.SourceKind != ownerQuote || o.SourceID == nil {
		return nil
	}
	id, _, ok := strings.Cut(*o.SourceID, "@")
	if !ok {
		return nil
	}
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return nil
	}
	return &n
}
