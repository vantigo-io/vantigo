package orders

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// headerInputOfOrder and headerInputOfOrderUpdate read the two order bodies.
func headerInputOfOrder(b gen.OrdersOrderInput) headerInput {
	in := headerInput{
		CustomerID: b.CustomerId, ContactID: b.ContactId, DeliveryAddressID: b.DeliveryAddressId, Currency: b.Currency,
		DeliveryDate: b.DeliveryDate, DiscountVisible: b.DiscountVisible, IntroText: b.IntroText, ClosingText: b.ClosingText,
		InternalNote: b.InternalNote, YourReference: b.YourReference, PaymentTermsDays: b.PaymentTermsDays,
		PricesIncludeVat: b.PricesIncludeVat, ProjectID: b.ProjectId, SalespersonID: b.SalespersonId,
	}
	if b.Language != nil {
		l := string(*b.Language)
		in.Language = &l
	}
	return in
}

func headerInputOfOrderUpdate(b gen.OrdersOrderUpdate) headerInput {
	in := headerInput{
		CustomerID: b.CustomerId, ContactID: b.ContactId, DeliveryAddressID: b.DeliveryAddressId, Currency: b.Currency,
		DeliveryDate: b.DeliveryDate, DiscountVisible: b.DiscountVisible, IntroText: b.IntroText, ClosingText: b.ClosingText,
		InternalNote: b.InternalNote, YourReference: b.YourReference, PaymentTermsDays: b.PaymentTermsDays,
		PricesIncludeVat: b.PricesIncludeVat, ProjectID: b.ProjectId, SalespersonID: b.SalespersonId,
	}
	if b.Language != nil {
		l := string(*b.Language)
		in.Language = &l
	}
	return in
}

// priceOrderInputs prices an order's lines for header h over existing (a
// save's current lines, nil on a create), before any transaction: no line is
// optional on an order (optional_not_allowed), and a kept line keeps its kind
// (line_kind_changed). frozen is a confirmed order's save: its kept lines are
// priced from their own rows (keptFrozen), and the catalog is read only for
// the others. A kit line's children follow it (withKits, manufacturing phase
// 2 M145).
func (s *server) priceOrderInputs(ctx context.Context, inputs []lineInput, existing []store.OrdersLine, h header, reprice, frozen bool) ([]pricedLine, error) {
	// A kit's children are written from their kit (kits.go).
	inputs, kept := withoutKitChildren(inputs, existing)
	if err := judgeKinds(inputs, existing); err != nil {
		return nil, err
	}
	old := byID(existing)
	reading := inputs
	if frozen {
		reading = nil
		for _, l := range inputs {
			if l.id != nil {
				if o, ok := old[*l.id]; ok && keptFrozen(l, o) {
					continue
				}
			}
			reading = append(reading, l)
		}
	}
	reads, err := s.readPricing(ctx, reading, h.Currency, s.deps.Clock())
	if err != nil {
		return nil, err
	}
	priced, failures := priceLines(inputs, old, reads, priceOptions{
		pricesIncludeVat: h.PricesIncludeVat, allowOptional: false, repriceKept: reprice, frozenKept: frozen, allowShipping: true,
	})
	if len(failures) > 0 {
		for i := range failures {
			if failures[i].index < len(kept) {
				failures[i].index = kept[failures[i].index]
			}
		}
		return nil, refuseLines(invalidOrderTitle, failures)
	}
	for i := range priced {
		priced[i].input = kept[i]
	}
	out, err := s.withKits(ctx, priced, existing, invalidOrderTitle)
	return out, atInputs(err, priced)
}

// CreateSalesOrder Create an order
// (POST /api/v1/orders/orders)
//
// The API's create goes through the one create command (O28) with channel
// manual. Everything the order is made from — the billing profile, the
// settings, the salesperson, the project, the catalog, the list prices and the
// VAT codes — is read before its transaction, which locks the variants first
// (MB rule 21) and then takes the number and writes the order. A repeated
// clientRequestId answers the first order with 200 (O27).
func (s *server) CreateSalesOrder(ctx context.Context, req gen.CreateSalesOrderRequestObject) (gen.CreateSalesOrderResponseObject, error) {
	resp := answers[gen.CreateSalesOrderResponseObject]{
		badRequest: func(p gen.OrdersProblem) gen.CreateSalesOrderResponseObject {
			return gen.CreateSalesOrder400ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.OrdersProblem) gen.CreateSalesOrderResponseObject {
			return gen.CreateSalesOrder409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.OrdersProblem) gen.CreateSalesOrderResponseObject {
			return gen.CreateSalesOrder503ApplicationProblemPlusJSONResponse(p)
		},
	}
	f := s.unknownProperties(ctx, orderInputSchema)
	if f.failed() {
		return gen.CreateSalesOrder400ApplicationProblemPlusJSONResponse(invalid(invalidOrderTitle, f)), nil
	}
	body := *req.Body
	requestID := uuid.UUID(body.ClientRequestId)
	if repeat, err := s.repeatedOrder(ctx, requestID, body.CustomerId); repeat != nil || err != nil {
		if err != nil {
			return answer(err, resp)
		}
		return gen.CreateSalesOrder200JSONResponse(*repeat), nil
	}

	in := headerInputOfOrder(body)
	reads, err := s.readHeader(ctx, in, nil)
	if err != nil {
		return answer(err, resp)
	}
	h := resolveHeader(&f, in, nil, reads)
	customerUsable(&f, reads.profile)
	if body.SiteId != nil {
		if err := s.judgeSite(ctx, &f, "siteId", *body.SiteId); err != nil {
			return nil, err
		}
	}
	if f.failed() {
		return gen.CreateSalesOrder400ApplicationProblemPlusJSONResponse(invalid(invalidOrderTitle, f)), nil
	}
	var inputs []lineInput
	if body.Lines != nil {
		inputs = lineInputsOf(*body.Lines)
	}
	priced, err := s.priceOrderInputs(ctx, inputs, nil, h, false, false)
	if err != nil {
		return answer(err, resp)
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	o, lines, _, err := s.createOrderOnce(ctx, CreateOrder{
		Origin: OrderOrigin{Channel: channelManual, ClientRequestID: &requestID}, Header: h, Lines: priced, CreatedBy: by,
		SiteID: body.SiteId,
	})
	err = atInputs(err, priced)
	if db.IsUniqueViolation(err, "ux_orders_client_request") {
		if repeat, err := s.repeatedOrder(ctx, requestID, body.CustomerId); repeat != nil || err != nil {
			if err != nil {
				return answer(err, resp)
			}
			return gen.CreateSalesOrder200JSONResponse(*repeat), nil
		}
	}
	if err != nil {
		return answer(err, resp)
	}
	warnings, err := documentWarnings(ctx, store.New(s.deps.Pool), o.CustomerID, lines)
	if err != nil {
		return nil, err
	}
	return gen.CreateSalesOrder201JSONResponse(orderView(o, lines, warnings, nil)), nil
}

// repeatedOrder is the order an earlier create with requestID made, nil when
// none did; a repeat for another customer is client_request_reused.
func (s *server) repeatedOrder(ctx context.Context, requestID uuid.UUID, customerID int32) (*gen.OrdersOrder, error) {
	row, err := store.New(s.deps.Pool).OrderByClientRequest(ctx, &requestID)
	if noRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("orders: read the order of request %s: %w", requestID, err)
	}
	if row.CustomerID != customerID {
		return nil, conflict(codeClientRequestReused, "The request id was used before",
			"An earlier create with this clientRequestId made an order for another customer.")
	}
	out, err := s.getOrderView(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// createOrderOnce is the create command as a channel calls it (O27, O28): an
// origin that carries an external id the channel already handed in answers
// the order it made, with created false, and writes nothing — before the
// transaction, and again when a racing create took the reference first. It
// locks the lines' variants first (MB rule 21). The API's create calls it with
// no external id; the first channel's contract (contracts.SalesOrders.Create,
// §14) wraps it unchanged.
func (s *server) createOrderOnce(ctx context.Context, c CreateOrder) (store.OrdersOrder, []store.OrdersLine, bool, error) {
	existing := func() (store.OrdersOrder, []store.OrdersLine, bool, error) {
		if c.Origin.ExternalID == nil {
			return store.OrdersOrder{}, nil, false, nil
		}
		q := store.New(s.deps.Pool)
		o, err := q.OrderByExternal(ctx, store.OrderByExternalParams{Channel: c.Origin.Channel, ExternalID: c.Origin.ExternalID})
		if noRows(err) {
			return store.OrdersOrder{}, nil, false, nil
		}
		if err != nil {
			return o, nil, false, fmt.Errorf("orders: read the order of %s %s: %w", c.Origin.Channel, *c.Origin.ExternalID, err)
		}
		lines, err := linesOf(ctx, q, ownerOrder, o.ID)
		return o, lines, true, err
	}
	if o, lines, found, err := existing(); found || err != nil {
		return o, lines, false, err
	}
	var order store.OrdersOrder
	var lines []store.OrdersLine
	err := s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if err := s.lockLines(ctx, tx, invalidOrderTitle, c.Lines); err != nil {
			return err
		}
		var err error
		order, lines, err = s.createOrder(ctx, tx, txq, c)
		return err
	})
	if db.IsUniqueViolation(err, "ux_orders_external") {
		o, lines, _, err := existing()
		return o, lines, false, err
	}
	if err != nil {
		return order, lines, false, err
	}
	return order, lines, true, nil
}
