package orders

// A shipping line's estimate (orders design O53): the carrier's price for a
// quote's or an order's freight, read through Rates with the document's
// delivery address, the estimated weight of its goods lines and the default
// account and service of the order's site (a quote's: the settings' default
// site), plus the settings' markup. The freight product, when the settings
// name one, gives the line its variant, name and VAT code; the line is saved
// with price_source carrier, and a price typed over it is manual. Nothing is
// written here.

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
	"github.com/vantigo-io/vantigo/server/internal/shipping"
)

// EstimateShipping Estimate a shipping line
// (POST /api/v1/orders/shipping-estimate)
func (s *server) EstimateShipping(ctx context.Context, req gen.EstimateShippingRequestObject) (gen.EstimateShippingResponseObject, error) {
	resp := shipmentAnswers(
		func(p gen.OrdersProblem) gen.EstimateShippingResponseObject {
			return gen.EstimateShipping400ApplicationProblemPlusJSONResponse(p)
		},
		func() gen.EstimateShippingResponseObject { return gen.EstimateShipping404Response{} },
		func(p gen.OrdersProblem) gen.EstimateShippingResponseObject {
			return gen.EstimateShipping409ApplicationProblemPlusJSONResponse(p)
		},
		func(p gen.OrdersProblem) gen.EstimateShippingResponseObject {
			return gen.EstimateShipping503ApplicationProblemPlusJSONResponse(p)
		},
		func(p gen.OrdersProblem) gen.EstimateShippingResponseObject {
			return gen.EstimateShipping422ApplicationProblemPlusJSONResponse(p)
		})
	f := s.unknownProperties(ctx, shippingEstimateInputSchema)
	b := *req.Body
	if (b.QuoteId == nil) == (b.OrderId == nil) {
		f.add("quoteId", "Name exactly one of quoteId and orderId")
	}
	if f.failed() {
		return gen.EstimateShipping400ApplicationProblemPlusJSONResponse(invalid(invalidShipmentTitle, f)), nil
	}

	// The document, its lines, the site and the settings.
	var h header
	var lines []store.OrdersLine
	var site *int64
	var settings ordersSettings
	var account *store.OrdersCarrierAccount
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if settings, err = readSettings(ctx, q); err != nil {
			return err
		}
		if b.OrderId != nil {
			o, ls, err := readOrder(ctx, q, *b.OrderId)
			if err != nil {
				return err
			}
			h, lines, site = headerOfOrder(o), ls, o.SiteID
		} else {
			quote, err := q.GetQuote(ctx, *b.QuoteId)
			if noRows(err) {
				return notFound()
			}
			if err != nil {
				return fmt.Errorf("orders: read quote %d: %w", *b.QuoteId, err)
			}
			if lines, err = linesOf(ctx, q, ownerQuote, quote.ID); err != nil {
				return err
			}
			h = headerOfQuote(quote)
		}
		if site == nil {
			site = settings.defaultSiteID
		}
		account, err = defaultAccount(ctx, q, site)
		return err
	})
	if err != nil {
		return answer(err, resp)
	}
	if account == nil {
		return answer(noCarrierAccount(), resp)
	}
	to := shipTo(h)
	if strings.TrimSpace(to.PostalCode) == "" {
		return answer(conflict(codeAddressMissing, "No address to ship to",
			"The document has no delivery or invoice postal code to ask the carrier for a price to. Choose a delivery address first."), resp)
	}
	c, err := s.carrierOf(*account)
	if err != nil {
		return answer(carrierFailed(err, ""), resp)
	}
	if !c.Capabilities().Rates {
		return answer(conflict(codeNoCarrierAccount, "The carrier has no prices",
			fmt.Sprintf("%s answers no prices; type the shipping line's price.", carrierName(account.Carrier))), resp)
	}
	service := deref(account.DefaultService)
	if service == "" {
		if all := carrierServices(account.Carrier); len(all) > 0 {
			service = all[0].Code
		}
	}

	// The estimated weight of the goods lines, and the freight product.
	quantities := map[int32]*big.Rat{}
	var ids []int32
	for _, l := range lines {
		if l.Kind != kindProduct || l.VariantID == nil || !l.Quantity.Valid || l.Optional {
			continue
		}
		if quantities[*l.VariantID] == nil {
			quantities[*l.VariantID] = new(big.Rat)
			ids = append(ids, *l.VariantID)
		}
		quantities[*l.VariantID].Add(quantities[*l.VariantID], ratOf(l.Quantity))
	}
	reading := []lineInput{{kind: kindShipping, variantID: settings.freightVariantID}}
	for _, id := range ids {
		reading = append(reading, lineInput{kind: kindProduct, variantID: &id})
	}
	reads, err := s.readPricing(ctx, reading, h.Currency, s.deps.Clock())
	if err != nil {
		return nil, err
	}
	slices.Sort(ids)
	weight := proposedWeight(quantities, reads.variants)

	seller, err := s.seller(ctx)
	if err != nil {
		return nil, err
	}
	opts, err := c.Rates(ctx, shipping.RateRequest{
		From: senderOf(*account, seller), To: to, Packages: []shipping.Package{{WeightGrams: weight}}, Services: []string{service},
		ShippingDate: s.deps.Clock(),
	})
	if err != nil {
		return answer(carrierFailed(err, "The carrier did not answer with a price. Try again later, or type the price."), resp)
	}
	var chosen *shipping.Option
	// The default service's option, else the cheapest.
	for i := range opts {
		o := &opts[i]
		switch {
		case chosen == nil:
			chosen = o
		case (o.Service == service) != (chosen.Service == service):
			if o.Service == service {
				chosen = o
			}
		case o.Price.Minor < chosen.Price.Minor:
			chosen = o
		}
	}
	if chosen == nil {
		return answer(refuse(problem(http.StatusUnprocessableEntity, codeCarrierRefused, "The carrier has no price",
			fmt.Sprintf("%s answered no price for %s to %s.", carrierName(account.Carrier), serviceName(account.Carrier, service, "en"), to.PostalCode))), resp)
	}
	if !strings.EqualFold(chosen.Price.Currency, h.Currency) {
		return answer(conflict(codeCurrencyMismatch, "The carrier prices in another currency",
			fmt.Sprintf("The carrier's price is in %s and the document is in %s; type the shipping line's price.", chosen.Price.Currency, h.Currency)), resp)
	}

	carrierPrice := moneyOf(chosen.Price)
	markup := orZero(settings.freightMarkup)
	net := round2(new(big.Rat).Mul(carrierPrice, new(big.Rat).Add(big.NewRat(1, 1), new(big.Rat).Quo(markup, hundred))))
	out := gen.OrdersShippingEstimate{
		AccountId: account.ID, AccountName: account.Name, Service: chosen.Service,
		ServiceName: serviceName(account.Carrier, chosen.Service, h.Language), WeightGrams: weight,
		CarrierPrice: wire(carrierPrice, amountPlaces), MarkupPercent: wire(markup, 2), NetPrice: wire(net, amountPlaces),
		Currency: chosen.Price.Currency, Estimate: chosen.Estimate,
	}
	// The line's name, variant and VAT code: the freight product's, else the
	// standard rate's — freight follows the VAT of the goods.
	name := "Frakt"
	if h.Language == "en" {
		name = "Shipping"
	}
	out.Name = fmt.Sprintf("%s – %s %s", name, carrierName(account.Carrier), out.ServiceName)
	code := reads.defaultCode()
	if v, ok := reads.variants[derefInt32(settings.freightVariantID)]; ok && settings.freightVariantID != nil {
		out.VariantId = &v.ID
		out.Name = v.ProductName
		if c, ok := reads.taxes[v.TaxCategoryID]; ok {
			code = &c
		}
	}
	unit := net
	if code != nil {
		out.VatCodeId = &code.ID
		if h.PricesIncludeVat {
			unit = round2(withVAT(net, mustRat(code.RatePercent)))
		}
	}
	out.UnitPrice = wire(unit, amountPlaces)
	return gen.EstimateShipping200JSONResponse(out), nil
}
