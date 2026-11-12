package orders

// What a salesperson's screens read under orders:access alone: the meta (can
// a document be sent now, the default validity and terms), the salespeople,
// and a catalog search priced for a line — so neither orders:settings nor
// Products' keys are needed to make a quote or an order.

import (
	"context"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// GetOrdersMeta Get what the Orders screens need
// (GET /api/v1/orders/meta)
func (s *server) GetOrdersMeta(ctx context.Context, _ gen.GetOrdersMetaRequestObject) (gen.GetOrdersMetaResponseObject, error) {
	settings, err := readSettings(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	_, available, err := s.sendingChannel(ctx)
	if err != nil {
		return nil, err
	}
	return gen.GetOrdersMeta200JSONResponse(gen.OrdersMeta{
		SendingAvailable: available, QuoteValidityDays: settings.quoteValidityDays,
		DefaultPaymentTermsDays: settings.defaultPaymentTermsDays, BaseCurrency: baseCurrency,
		StockKept: s.deps.Stock != nil, CanOverrideExpired: s.has(ctx, permInventoryExpiredOverride),
		// A delivery's lots link to Inventory's lot page and trace (M94),
		// which read stock: both views.
		CanTraceLots: s.deps.Stock != nil && contracts.HasPermissions(ctx, s.deps.Access, permInventoryLocationsView, permInventoryStockView),
	}), nil
}

// ListOrdersSalespeople List the salespeople
// (GET /api/v1/orders/salespeople)
//
// The active holders of orders:quotes-manage and of orders:sales-manage,
// through contracts.UserDirectory, once each, by display name.
func (s *server) ListOrdersSalespeople(ctx context.Context, req gen.ListOrdersSalespeopleRequestObject) (gen.ListOrdersSalespeopleResponseObject, error) {
	q := ""
	if req.Params.Q != nil {
		q = strings.ToLower(strings.TrimSpace(*req.Params.Q))
	}
	seen := map[uuid.UUID]bool{}
	out := gen.OrdersSalespersonList{Items: []gen.OrdersSalesperson{}}
	for _, key := range []string{permQuotesManage, permSalesManage} {
		holders, err := s.permissionHolders(ctx, key)
		if err != nil {
			return nil, err
		}
		for _, h := range holders {
			if seen[h.ID] || (q != "" && !strings.Contains(strings.ToLower(h.DisplayName), q)) {
				continue
			}
			seen[h.ID] = true
			out.Items = append(out.Items, gen.OrdersSalesperson{Id: h.ID, DisplayName: h.DisplayName})
		}
	}
	slices.SortFunc(out.Items, func(a, b gen.OrdersSalesperson) int {
		if c := strings.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName)); c != 0 {
			return c
		}
		return strings.Compare(a.Id.String(), b.Id.String())
	})
	return gen.ListOrdersSalespeople200JSONResponse(out), nil
}

// SearchOrdersVariants Search the catalog for a line
// (GET /api/v1/orders/variants)
//
// Goods and services of active products, each with its list price in the
// currency today and its category's VAT code, as a line would take them
// (O16).
func (s *server) SearchOrdersVariants(ctx context.Context, req gen.SearchOrdersVariantsRequestObject) (gen.SearchOrdersVariantsResponseObject, error) {
	p := req.Params
	currency := baseCurrency
	if p.Currency != nil {
		currency = strings.ToUpper(strings.TrimSpace(*p.Currency))
	}
	limit := 20
	if p.Limit != nil {
		limit = int(*p.Limit)
	}
	if len(currency) != 3 || limit < 1 || limit > 50 {
		var f fieldErrors
		if len(currency) != 3 {
			f.add("currency", "A currency is three letters, as NOK")
		}
		if limit < 1 || limit > 50 {
			f.add("limit", "limit is 1 to 50")
		}
		return gen.SearchOrdersVariants400ApplicationProblemPlusJSONResponse(invalid(invalidQueryTitle, f)), nil
	}
	q := ""
	if p.Q != nil {
		q = strings.TrimSpace(*p.Q)
	}
	found, err := s.searchVariants(ctx, q, contracts.VariantSearch{Limit: limit})
	if err != nil {
		return answer(err, answers[gen.SearchOrdersVariantsResponseObject]{
			unavailable: func(p gen.OrdersProblem) gen.SearchOrdersVariantsResponseObject {
				return gen.SearchOrdersVariants503ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	inputs := make([]lineInput, 0, len(found))
	for _, v := range found {
		id := v.ID
		inputs = append(inputs, lineInput{kind: kindProduct, variantID: &id})
	}
	reads, err := s.readPricing(ctx, inputs, currency, s.deps.Clock())
	if err != nil {
		return answer(err, answers[gen.SearchOrdersVariantsResponseObject]{
			unavailable: func(p gen.OrdersProblem) gen.SearchOrdersVariantsResponseObject {
				return gen.SearchOrdersVariants503ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	out := gen.OrdersVariantList{Items: []gen.OrdersVariant{}}
	for _, v := range found {
		item := gen.OrdersVariant{
			Id: v.ID, ProductId: v.ProductID, Sku: v.SKU, Name: v.ProductName, Unit: v.Unit,
			ProductType: gen.OrdersVariantProductType(v.ProductType), Description: v.Description, Currency: currency,
			Packagings: []gen.OrdersVariantPackaging{},
		}
		if price, ok := reads.prices[v.ID]; ok {
			item.Price = wirePtr(price, pricePlaces)
		}
		if code, ok := reads.taxes[v.TaxCategoryID]; ok {
			id, c := code.ID, code.Code
			item.VatCodeId, item.VatCode = &id, &c
			item.VatRatePercent = wirePtr(mustRat(code.RatePercent), ratePlaces)
		}
		for _, pk := range v.Packagings {
			item.Packagings = append(item.Packagings, gen.OrdersVariantPackaging{
				Id: pk.ID, Name: pk.Name, Quantity: wire(mustRat(pk.Quantity), quantityPlaces),
			})
		}
		out.Items = append(out.Items, item)
	}
	return gen.SearchOrdersVariants200JSONResponse(out), nil
}

// sendChannelsView is the settings' channels to send from and what the
// stored choice is now, as Invoices' settings answer them; it asks
// Communications, so it runs outside any transaction.
func (s *server) sendChannelsView(ctx context.Context, out *gen.OrdersSettings) error {
	channels, err := s.outboundChannels(ctx)
	if err != nil {
		return err
	}
	out.SendChannels = make([]gen.OrdersSendChannel, 0, len(channels))
	out.SendChannelState = gen.Disabled
	for _, c := range channels {
		out.SendChannels = append(out.SendChannels, gen.OrdersSendChannel{
			Id: c.ID, Address: c.Address, DisplayName: c.DisplayName, Provider: c.Provider,
			ProviderDisplayName: c.ProviderDisplayName, Available: c.Available,
		})
		if out.SendChannelId != nil && c.ID == *out.SendChannelId {
			out.SendChannelState = gen.Unavailable
			if c.Available {
				out.SendChannelState = gen.Ready
			}
		}
	}
	if out.SendChannelId != nil && out.SendChannelState == gen.Disabled {
		out.SendChannelState = gen.Gone
	}
	return nil
}
