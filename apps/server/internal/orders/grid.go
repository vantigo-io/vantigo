package orders

import (
	"context"
	"slices"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// This file is the variant grid (O34): Add from grid shows a product's
// variants as a matrix along the options they differ by — size and colour —
// built from ProductCatalog.VariantsOfProduct and each variant's option
// values, and the editor turns a quantity typed in a cell into that variant's
// line. A product with more than two options, an option with more than
// maxGridValues values, or a variant without a value on one of the options is
// answered as a list instead.

// maxGridValues is the most values one axis of the grid holds.
const maxGridValues = 30

// gridAxes are the options a product's variants are laid out along, each with
// its values in the order the variants first name them, and whether they make
// a grid: one or two options, every variant valued on each, at most
// maxGridValues values an option. The options are taken by name, as typed.
func gridAxes(variants []contracts.VariantEntry) ([]gen.OrdersGridOption, bool) {
	var names []string
	for _, v := range variants {
		for name := range v.OptionValues {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	slices.SortFunc(names, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	if len(names) == 0 || len(names) > 2 {
		return nil, false
	}
	axes := make([]gen.OrdersGridOption, 0, len(names))
	for _, name := range names {
		axis := gen.OrdersGridOption{Name: name, Values: []string{}}
		for _, v := range variants {
			value, ok := v.OptionValues[name]
			if !ok || strings.TrimSpace(value) == "" {
				return nil, false
			}
			if !slices.Contains(axis.Values, value) {
				axis.Values = append(axis.Values, value)
			}
		}
		if len(axis.Values) > maxGridValues {
			return nil, false
		}
		axes = append(axes, axis)
	}
	return axes, true
}

// GetOrdersVariantGrid Get a product's variant grid
// (GET /api/v1/orders/products/{productId}/grid)
func (s *server) GetOrdersVariantGrid(ctx context.Context, req gen.GetOrdersVariantGridRequestObject) (gen.GetOrdersVariantGridResponseObject, error) {
	resp := answers[gen.GetOrdersVariantGridResponseObject]{
		unavailable: func(p gen.OrdersProblem) gen.GetOrdersVariantGridResponseObject {
			return gen.GetOrdersVariantGrid503ApplicationProblemPlusJSONResponse(p)
		},
	}
	currency := baseCurrency
	if req.Params.Currency != nil {
		currency = strings.ToUpper(strings.TrimSpace(*req.Params.Currency))
	}
	if len(currency) != 3 {
		var f fieldErrors
		f.add("currency", "A currency is three letters, as NOK")
		return gen.GetOrdersVariantGrid400ApplicationProblemPlusJSONResponse(invalid(invalidQueryTitle, f)), nil
	}
	variants, err := s.variantsOfProduct(ctx, req.ProductId)
	if err != nil {
		return answer(err, resp)
	}
	if len(variants) == 0 {
		return gen.GetOrdersVariantGrid404Response{}, nil
	}
	inputs := make([]lineInput, 0, len(variants))
	for _, v := range variants {
		id := v.ID
		inputs = append(inputs, lineInput{kind: kindProduct, variantID: &id})
	}
	reads, err := s.readPricing(ctx, inputs, currency, s.deps.Clock())
	if err != nil {
		return answer(err, resp)
	}
	cells := make([]gen.OrdersGridVariant, 0, len(variants))
	for _, v := range variants {
		values := map[string]string{}
		for name, value := range v.OptionValues {
			values[name] = value
		}
		cell := gen.OrdersGridVariant{VariantId: v.ID, Sku: v.SKU, Unit: v.Unit, Values: values}
		if price, ok := reads.prices[v.ID]; ok {
			cell.Price = wirePtr(price, pricePlaces)
		}
		cells = append(cells, cell)
	}
	out := gen.OrdersVariantGrid{
		ProductId: req.ProductId, ProductName: variants[0].ProductName, Currency: currency,
		Options: []gen.OrdersGridOption{}, Cells: []gen.OrdersGridVariant{}, Variants: []gen.OrdersGridVariant{},
	}
	if axes, ok := gridAxes(variants); ok {
		out.Mode, out.Options, out.Cells = gen.Grid, axes, cells
	} else {
		out.Mode, out.Variants = gen.List, cells
	}
	return gen.GetOrdersVariantGrid200JSONResponse(out), nil
}
