package products

import (
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/products/gen"
	"github.com/vantigo-io/vantigo/server/internal/products/store"
)

// This file ports the response-shaping logic of
// Endpoints/Products/Dtos/ProductResponse.cs (ProductResponse.FromDomain,
// ProductVariantResponse.FromDomain, ProductTaxCategoryResponse.FromDomain)
// and ProductPriceResponse.cs.

// unmarshalOptionValues decodes a variant's stored option_values JSON back
// into a map, an empty map for an absent/empty column rather than nil (the
// contract's optionValues is a required field, never omitted).
func unmarshalOptionValues(raw []byte) map[string]string {
	if len(raw) == 0 {
		return map[string]string{}
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]string{}
	}
	return m
}

// marshalOptionValues encodes a variant's option values for storage. nil
// becomes "{}", never SQL NULL: ProductVariant.OptionValues is a
// Dictionary<string,string>, never null, in .NET (ProductVariant.cs:54).
func marshalOptionValues(m map[string]string) []byte {
	if m == nil {
		m = map[string]string{}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return []byte("{}")
	}
	return b
}

// domainPriceFromRow adapts a persisted price row to pricing.go's domain
// type, for effective-price resolution and Conflicts checks.
func domainPriceFromRow(r store.ProductsProductPrice) productPrice {
	return productPrice{ID: r.ID, Currency: r.Currency, Amount: floatFromNumeric(r.Amount), ValidFrom: r.ValidFrom, ValidTo: r.ValidTo}
}

// priceResponseFromRow is ProductPriceResponse.FromDomain (ProductPriceResponse.cs:16-23).
func priceResponseFromRow(r store.ProductsProductPrice) gen.ProductPriceResponse {
	return gen.ProductPriceResponse{Id: r.ID, Currency: r.Currency, Amount: floatFromNumeric(r.Amount), ValidFrom: r.ValidFrom, ValidTo: r.ValidTo}
}

// priceResponseFromDomain is priceResponseFromRow for a productPrice value
// already resolved in Go (getEffectivePrices' output), rather than a fresh
// store row.
func priceResponseFromDomain(p productPrice) gen.ProductPriceResponse {
	return gen.ProductPriceResponse{Id: p.ID, Currency: p.Currency, Amount: p.Amount, ValidFrom: p.ValidFrom, ValidTo: p.ValidTo}
}

// variantResponse is ProductVariantResponse.FromDomain (:84-101): the
// variant's own fields, its option values camelCased on the way out
// (products inventory §7 oddity 5), and its effective prices resolved at
// moment (ProductPricing.GetEffectivePrices).
func variantResponse(v store.ProductsProductVariant, prices []store.ProductsProductPrice, moment time.Time) gen.ProductVariantResponse {
	domainPrices := make([]productPrice, len(prices))
	for i, p := range prices {
		domainPrices[i] = domainPriceFromRow(p)
	}
	effective := getEffectivePrices(domainPrices, moment)
	effectiveResponses := make([]gen.ProductPriceResponse, 0, len(effective))
	for _, p := range effective {
		effectiveResponses = append(effectiveResponses, priceResponseFromDomain(p))
	}

	return gen.ProductVariantResponse{
		Id:              v.ID,
		Sku:             v.Sku,
		Barcode:         v.Barcode,
		Unit:            v.Unit,
		StandardCost:    floatPtrFromNumeric(v.StandardCost),
		WeightKg:        floatPtrFromNumeric(v.WeightKg),
		LengthCm:        floatPtrFromNumeric(v.LengthCm),
		WidthCm:         floatPtrFromNumeric(v.WidthCm),
		HeightCm:        floatPtrFromNumeric(v.HeightCm),
		OptionValues:    camelizeOptionValueKeys(unmarshalOptionValues(v.OptionValues)),
		EffectivePrices: effectiveResponses,
		CreatedAt:       v.CreatedAt,
		UpdatedAt:       v.UpdatedAt,
	}
}

// categoryRef is ProductCategoryResponse's construction from either
// GetCategoryRef's or ListCategoryRefs' row (identical shape, distinct
// sqlc-generated types).
func categoryRef(id int32, name string) gen.ProductCategoryResponse {
	return gen.ProductCategoryResponse{Id: id, Name: name}
}

// taxCategoryRef is ProductTaxCategoryResponse.FromDomain
// (ProductResponse.cs:119-125), from either GetTaxCategoryRef's or
// ListTaxCategoryRefs' row.
func taxCategoryRef(id int32, name, kind string, rate pgtype.Numeric) gen.ProductTaxCategoryResponse {
	return gen.ProductTaxCategoryResponse{Id: id, Name: name, Kind: kind, Rate: floatFromNumeric(rate)}
}

// productResponse is ProductResponse.FromDomain (ProductResponse.cs:33-64):
// shared product fields plus every variant, with a single-variant
// product's sellable fields (sku, unit, standardCost, barcode, dimensions,
// effective prices) flattened onto the top level for an inline UX — null/
// empty for a multi-variant product.
func productResponse(p store.ProductsProduct, category *gen.ProductCategoryResponse, taxCategory gen.ProductTaxCategoryResponse, variants []gen.ProductVariantResponse) gen.ProductResponse {
	resp := gen.ProductResponse{
		Id:              p.ID,
		Name:            p.Name,
		Type:            p.Type,
		Status:          p.Status,
		Description:     p.Description,
		Category:        category,
		TaxCategory:     taxCategory,
		Variants:        variants,
		EffectivePrices: []gen.ProductPriceResponse{},
		CreatedAt:       p.CreatedAt,
		UpdatedAt:       p.UpdatedAt,
	}
	if len(variants) == 1 {
		sole := variants[0]
		resp.Sku, resp.Unit = &sole.Sku, &sole.Unit
		resp.StandardCost, resp.Barcode = sole.StandardCost, sole.Barcode
		resp.WeightKg, resp.LengthCm, resp.WidthCm, resp.HeightCm = sole.WeightKg, sole.LengthCm, sole.WidthCm, sole.HeightCm
		resp.EffectivePrices = sole.EffectivePrices
	}
	return resp
}
