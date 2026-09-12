package products

import (
	"fmt"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/products/gen"
)

// This file ports the .NET Products module's request-shape validation
// (Endpoints/Products/Dtos/ProductRequest.cs, ProductPriceRequest.cs,
// products inventory §1.3): the pieces of validation that need the
// generated contract's request types, kept separate from values.go's
// primitive-typed value objects.

// parsedProduct is the validated, normalized values of a ProductRequest
// shared by CreateProductEndpoint and UpdateProductEndpoint
// (ProductRequest.Validate:21-69). Status is nil when the request omitted
// it: CreateProductEndpoint then defaults to "Draft"; UpdateProductEndpoint
// leaves the persisted status unchanged (CreateProductEndpoint.cs:90-92,
// UpdateProductEndpoint.cs:55-58). Variants is only populated when
// validateVariants is true — putProductsById never reads it at all
// (products inventory §7 oddity 2).
type parsedProduct struct {
	Name          string
	Type          string
	Status        *string
	TaxCategoryID int32
	Description   *string
	CategoryID    *int32
	Variants      []parsedVariant
}

// validateProductRequest is ProductRequest.Validate (:21-69). requireVariants
// gates the "at least one variant" check (create only, always true there);
// validateVariants gates whether request.Variants is read and validated at
// all (false for update — UpdateProductEndpoint.cs:19 calls
// Validate(requireVariants:false, validateVariants:false), so a PUT body's
// variants are neither validated nor ever reach parsedProduct.Variants).
func validateProductRequest(body gen.ProductRequest, requireVariants, validateVariants bool) (parsedProduct, map[string][]string) {
	errs := map[string][]string{}

	name, nameErr := validateProductName(body.Name)
	if nameErr != "" {
		errs["name"] = []string{nameErr}
	}

	typ, typeErr := validateProductType(body.Type)
	if typeErr != "" {
		errs["type"] = []string{typeErr}
	}

	var status *string
	if body.Status != nil {
		st, statusErr := validateProductStatus(*body.Status)
		if statusErr != "" {
			errs["status"] = []string{statusErr}
		} else {
			status = &st
		}
	}

	if e := validateTaxCategoryID(body.TaxCategoryId); e != "" {
		errs["taxCategoryId"] = []string{e}
	}

	if e := validateProductDescriptionLength(body.Description); e != "" {
		errs["description"] = []string{e}
	}

	var variantBodies []gen.VariantRequest
	if body.Variants != nil {
		variantBodies = *body.Variants
	}
	if requireVariants && len(variantBodies) == 0 {
		errs["variants"] = []string{"At least one variant is required."}
	}

	var variants []parsedVariant
	if validateVariants {
		for i, v := range variantBodies {
			variants = append(variants, validateVariantRequest(fmt.Sprintf("variants[%d].", i), v, errs))
		}
	}

	return parsedProduct{
		Name: name, Type: typ, Status: status, TaxCategoryID: body.TaxCategoryId,
		Description: normalizeDescription(body.Description), CategoryID: body.CategoryId,
		Variants: variants,
	}, errs
}

// parsedPrice is the validated, normalized values of a ProductPriceRequest
// (ProductPriceRequest.ToDomain:39-45).
type parsedPrice struct {
	Currency  string
	Amount    float64
	ValidFrom *time.Time
	ValidTo   *time.Time
}

// domain is parsedPrice as the pricing.go domain type, for Conflicts/
// effective-price resolution. id is only meaningful once the price is
// persisted; a pre-insert candidate uses 0, which pricing.go's Conflicts
// never reads.
func (p parsedPrice) domain(id int32) productPrice {
	return productPrice{ID: id, Currency: p.Currency, Amount: p.Amount, ValidFrom: p.ValidFrom, ValidTo: p.ValidTo}
}

// validatePriceRequest is ProductPriceRequest.Validate (:18-37), writing
// into errs under prefix+field.
func validatePriceRequest(prefix string, body gen.ProductPriceRequest, errs map[string][]string) {
	if _, err := validateCurrency(body.Currency); err != "" {
		errs[prefix+"currency"] = []string{err}
	}
	if body.Amount < 0 {
		errs[prefix+"amount"] = []string{fmt.Sprintf("'amount' must be zero or greater, but was %s.", formatAmount(body.Amount))}
	}
	if body.ValidFrom != nil && body.ValidTo != nil && !body.ValidTo.After(*body.ValidFrom) {
		errs[prefix+"validTo"] = []string{"'validTo' must be after 'validFrom'."}
	}
}

func toParsedPrice(body gen.ProductPriceRequest) parsedPrice {
	return parsedPrice{
		Currency:  strings.ToUpper(strings.TrimSpace(body.Currency)),
		Amount:    body.Amount,
		ValidFrom: body.ValidFrom,
		ValidTo:   body.ValidTo,
	}
}

// parsedVariant is the validated, normalized values of a VariantRequest
// (VariantRequest.Validate:88-143, VariantRequest.ToDomain:145-157).
type parsedVariant struct {
	Sku          string
	Barcode      *string
	Unit         string
	StandardCost *float64
	WeightKg     *float64
	LengthCm     *float64
	WidthCm      *float64
	HeightCm     *float64
	OptionValues map[string]string
	Prices       []parsedPrice
}

// validateVariantRequest is VariantRequest.Validate (:88-143), writing every
// field error into errs under prefix+field, and prefix+"prices[i]."/
// prefix+"prices[i]" for price sub-errors — prefix is "" for the standalone
// add/update-variant endpoints and "variants[i]." for each entry of a
// create-product request's variants array.
func validateVariantRequest(prefix string, body gen.VariantRequest, errs map[string][]string) parsedVariant {
	sku, skuErr := validateSKU(body.Sku)
	if skuErr != "" {
		errs[prefix+"sku"] = []string{skuErr}
	}
	barcode, barcodeErr := validateBarcode(body.Barcode)
	if barcodeErr != "" {
		errs[prefix+"barcode"] = []string{barcodeErr}
	}
	unit, unitErr := validateUnit(body.Unit)
	if unitErr != "" {
		errs[prefix+"unit"] = []string{unitErr}
	}
	if e := validateNonNegative("standardCost", body.StandardCost); e != "" {
		errs[prefix+"standardCost"] = []string{e}
	}
	if e := validateNonNegative("weightKg", body.WeightKg); e != "" {
		errs[prefix+"weightKg"] = []string{e}
	}
	if e := validateNonNegative("lengthCm", body.LengthCm); e != "" {
		errs[prefix+"lengthCm"] = []string{e}
	}
	if e := validateNonNegative("widthCm", body.WidthCm); e != "" {
		errs[prefix+"widthCm"] = []string{e}
	}
	if e := validateNonNegative("heightCm", body.HeightCm); e != "" {
		errs[prefix+"heightCm"] = []string{e}
	}

	var prices []parsedPrice
	if body.Prices != nil {
		reqPrices := *body.Prices
		prices = make([]parsedPrice, len(reqPrices))
		for i, p := range reqPrices {
			validatePriceRequest(fmt.Sprintf("%sprices[%d].", prefix, i), p, errs)
			prices[i] = toParsedPrice(p)
		}
		// Pairwise conflict check across every candidate, regardless of
		// whether an individual price already failed its own field
		// validation (ProductRequest.cs:130-141): the error is keyed by the
		// *later* index of the conflicting pair, .NET's
		// `errors[$"{prefix}prices[{other}]"]`.
		for i := range prices {
			for other := i + 1; other < len(prices); other++ {
				if pricesConflict(prices[i].domain(0), prices[other].domain(0)) {
					errs[fmt.Sprintf("%sprices[%d]", prefix, other)] = []string{
						fmt.Sprintf("The price overlaps another %s price of the same kind.", prices[other].Currency),
					}
				}
			}
		}
	}

	optionValues := map[string]string{}
	if body.OptionValues != nil {
		optionValues = *body.OptionValues
	}

	return parsedVariant{
		Sku: sku, Barcode: barcode, Unit: unit,
		StandardCost: body.StandardCost, WeightKg: body.WeightKg, LengthCm: body.LengthCm,
		WidthCm: body.WidthCm, HeightCm: body.HeightCm,
		OptionValues: optionValues, Prices: prices,
	}
}

// containsPricingData is ProductRequest.ContainsPricingData /
// VariantRequest.ContainsPricingData (products inventory §1.1, §7 oddity
// 1): true when any variant in the request carries a non-null standardCost
// or a non-empty prices array — the trigger for the conditional
// pricing-view+pricing-manage gate this task's dispatch corrections
// describe (server.go's hasPermission).
func containsPricingData(variants []gen.VariantRequest) bool {
	for _, v := range variants {
		if variantContainsPricingData(v) {
			return true
		}
	}
	return false
}

func variantContainsPricingData(v gen.VariantRequest) bool {
	return v.StandardCost != nil || (v.Prices != nil && len(*v.Prices) > 0)
}
