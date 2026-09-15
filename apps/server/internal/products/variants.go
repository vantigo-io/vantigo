package products

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/products/gen"
	"github.com/vantigo-io/vantigo/server/internal/products/store"
)

// This file is the Variants sub-resource (EP/Products/Variants/*Endpoint.cs):
// getProductsByIdVariants, postProductsByIdVariants,
// putProductsByIdVariantsByVariantId and
// deleteProductsByIdVariantsByVariantId.

// GetProductsByIdVariants List variants of a product
// (GET /api/v1/products/{id}/variants)
func (s *server) GetProductsByIdVariants(ctx context.Context, req gen.GetProductsByIdVariantsRequestObject) (gen.GetProductsByIdVariantsResponseObject, error) {
	q := store.New(s.deps.Pool)
	exists, err := q.ProductExists(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("products: check product exists: %w", err)
	}
	if !exists {
		return gen.GetProductsByIdVariants404Response{}, nil
	}

	rows, err := q.ListVariantsByProduct(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("products: list variants: %w", err)
	}
	variantIDs := make([]int32, len(rows))
	for i, v := range rows {
		variantIDs[i] = v.ID
	}
	prices, err := q.ListPricesByVariantIDs(ctx, variantIDs)
	if err != nil {
		return nil, fmt.Errorf("products: list prices: %w", err)
	}
	pricesByVariant := map[int32][]store.ProductsProductPrice{}
	for _, p := range prices {
		pricesByVariant[p.VariantID] = append(pricesByVariant[p.VariantID], p)
	}

	moment := s.deps.Clock()
	data := make([]gen.ProductVariantResponse, 0, len(rows))
	for _, v := range rows {
		data = append(data, variantResponse(v, pricesByVariant[v.ID], moment))
	}
	return gen.GetProductsByIdVariants200JSONResponse(data), nil
}

// PostProductsByIdVariants Add a variant to a product
// (POST /api/v1/products/{id}/variants)
//
// AddProductVariantEndpoint.cs:16-59 (products inventory §1.3): (1) the
// conditional pricing-view+pricing-manage gate, the same shape as
// PostProducts (products.go's doc comment, server.go's hasPermission); (2)
// field validation; (3) product exists (404); (4) duplicate SKU (409); (5)
// duplicate barcode (409); (6) insert -> 201, Location the actual new
// variant (AddProductVariantEndpoint.cs:59) — unlike the sibling price
// sub-resource's Location (prices.go's PostProductsByIdVariantsByVariantIdPrices).
func (s *server) PostProductsByIdVariants(ctx context.Context, req gen.PostProductsByIdVariantsRequestObject) (gen.PostProductsByIdVariantsResponseObject, error) {
	body := gen.VariantRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	if variantContainsPricingData(body) && (!s.hasPermission(ctx, pricingView) || !s.hasPermission(ctx, pricingManage)) {
		return gen.PostProductsByIdVariants403JSONResponse(apicommon.ForbiddenBody()), nil
	}

	errs := map[string][]string{}
	parsed := validateVariantRequest("", body, errs)
	if len(errs) > 0 {
		return gen.PostProductsByIdVariants400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid variant", errs)), nil
	}

	q := store.New(s.deps.Pool)
	exists, err := q.ProductExists(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("products: check product exists: %w", err)
	}
	if !exists {
		return gen.PostProductsByIdVariants404Response{}, nil
	}

	skuExists, err := q.VariantSkuExists(ctx, parsed.Sku)
	if err != nil {
		return nil, fmt.Errorf("products: check sku: %w", err)
	}
	if skuExists {
		return gen.PostProductsByIdVariants409ApplicationProblemPlusJSONResponse(apicommon.ProblemStatus(
			"Duplicate SKU", fmt.Sprintf("A variant with SKU '%s' already exists.", parsed.Sku), http.StatusConflict)), nil
	}

	if parsed.Barcode != nil {
		barcodeExists, err := q.VariantBarcodeExists(ctx, parsed.Barcode)
		if err != nil {
			return nil, fmt.Errorf("products: check barcode: %w", err)
		}
		if barcodeExists {
			return gen.PostProductsByIdVariants409ApplicationProblemPlusJSONResponse(apicommon.ProblemStatus(
				"Duplicate barcode", fmt.Sprintf("A variant with barcode '%s' already exists.", *parsed.Barcode), http.StatusConflict)), nil
		}
	}

	now := s.deps.Clock()
	var variant store.ProductsProductVariant
	var priceRows []store.ProductsProductPrice
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		nums, err := numericsFromVariant(parsed)
		if err != nil {
			return err
		}
		variant, err = txq.InsertProductVariant(ctx, store.InsertProductVariantParams{
			ProductID: req.Id, Sku: parsed.Sku, Barcode: parsed.Barcode, Unit: parsed.Unit,
			StandardCost: nums.StandardCost, WeightKg: nums.WeightKg,
			LengthCm: nums.LengthCm, WidthCm: nums.WidthCm, HeightCm: nums.HeightCm,
			OptionValues: marshalOptionValues(parsed.OptionValues), Now: now,
		})
		if err != nil {
			return err
		}
		for _, price := range parsed.Prices {
			amount, aerr := numericFromFloat(price.Amount)
			if aerr != nil {
				return aerr
			}
			row, err := txq.InsertProductPrice(ctx, store.InsertProductPriceParams{
				VariantID: variant.ID, Currency: price.Currency, Amount: amount,
				ValidFrom: price.ValidFrom, ValidTo: price.ValidTo,
			})
			if err != nil {
				return err
			}
			priceRows = append(priceRows, row)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("products: add variant: %w", err)
	}

	location := fmt.Sprintf("%s/api/v1/products/%d/variants/%d", s.deps.Config.BasePath, req.Id, variant.ID)
	return gen.PostProductsByIdVariants201JSONResponse{
		Body:    variantResponse(variant, priceRows, now),
		Headers: gen.PostProductsByIdVariants201ResponseHeaders{Location: &location},
	}, nil
}

// PutProductsByIdVariantsByVariantId Update a product variant
// (PUT /api/v1/products/{id}/variants/{variantId})
//
// UpdateProductVariantEndpoint.cs:13-66 (products inventory §1.3): field
// validation -> variant exists scoped to (id, variantId) (404) -> if the SKU
// changed: the product must be Draft (409 "SKU is immutable"), then a
// duplicate-SKU check excluding this variant (409) -> a duplicate-barcode
// check excluding this variant, unconditionally — it runs even when the SKU
// did not change (409) -> apply -> save. pricing-manage is required
// unconditionally by this operation's own x-vantigo-access (verified against
// the contract, products inventory §1.1 line 51), so no handler-side
// permission check belongs here at all.
func (s *server) PutProductsByIdVariantsByVariantId(ctx context.Context, req gen.PutProductsByIdVariantsByVariantIdRequestObject) (gen.PutProductsByIdVariantsByVariantIdResponseObject, error) {
	body := gen.VariantRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	errs := map[string][]string{}
	parsed := validateVariantRequest("", body, errs)
	if len(errs) > 0 {
		return gen.PutProductsByIdVariantsByVariantId400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid variant", errs)), nil
	}

	q := store.New(s.deps.Pool)
	existing, err := q.GetVariantByProductAndID(ctx, store.GetVariantByProductAndIDParams{ID: req.VariantId, ProductID: req.Id})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutProductsByIdVariantsByVariantId404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("products: get variant: %w", err)
	}

	if existing.Sku != parsed.Sku {
		status, err := q.GetProductStatus(ctx, req.Id)
		if err != nil {
			return nil, fmt.Errorf("products: get product status: %w", err)
		}
		if status != "Draft" {
			return gen.PutProductsByIdVariantsByVariantId409ApplicationProblemPlusJSONResponse(apicommon.ProblemStatus(
				"SKU is immutable", "The SKU cannot be changed after the product has been activated.", http.StatusConflict)), nil
		}
		skuExists, err := q.VariantSkuExistsExcluding(ctx, store.VariantSkuExistsExcludingParams{Sku: parsed.Sku, ID: req.VariantId})
		if err != nil {
			return nil, fmt.Errorf("products: check sku: %w", err)
		}
		if skuExists {
			return gen.PutProductsByIdVariantsByVariantId409ApplicationProblemPlusJSONResponse(apicommon.ProblemStatus(
				"Duplicate SKU", fmt.Sprintf("A variant with SKU '%s' already exists.", parsed.Sku), http.StatusConflict)), nil
		}
	}

	if parsed.Barcode != nil {
		barcodeExists, err := q.VariantBarcodeExistsExcluding(ctx, store.VariantBarcodeExistsExcludingParams{Barcode: parsed.Barcode, ID: req.VariantId})
		if err != nil {
			return nil, fmt.Errorf("products: check barcode: %w", err)
		}
		if barcodeExists {
			return gen.PutProductsByIdVariantsByVariantId409ApplicationProblemPlusJSONResponse(apicommon.ProblemStatus(
				"Duplicate barcode", fmt.Sprintf("A variant with barcode '%s' already exists.", *parsed.Barcode), http.StatusConflict)), nil
		}
	}

	nums, err := numericsFromVariant(parsed)
	if err != nil {
		return nil, err
	}
	updated, err := q.UpdateProductVariant(ctx, store.UpdateProductVariantParams{
		Sku: parsed.Sku, Barcode: parsed.Barcode, Unit: parsed.Unit,
		StandardCost: nums.StandardCost, WeightKg: nums.WeightKg,
		LengthCm: nums.LengthCm, WidthCm: nums.WidthCm, HeightCm: nums.HeightCm,
		OptionValues: marshalOptionValues(parsed.OptionValues), UpdatedAt: s.deps.Clock(), ID: req.VariantId,
	})
	if err != nil {
		return nil, fmt.Errorf("products: update variant: %w", err)
	}

	// Prices are untouched by this endpoint — managed only through the price
	// sub-resource (prices.go) — so the response reads back whatever the
	// variant already had.
	prices, err := q.ListPricesByVariant(ctx, req.VariantId)
	if err != nil {
		return nil, fmt.Errorf("products: list prices: %w", err)
	}
	return gen.PutProductsByIdVariantsByVariantId200JSONResponse(variantResponse(updated, prices, s.deps.Clock())), nil
}

// DeleteProductsByIdVariantsByVariantId Remove a product variant
// (DELETE /api/v1/products/{id}/variants/{variantId})
//
// DeleteProductVariantEndpoint.cs:9-28: variant exists scoped to (id,
// variantId) (404) -> "is this the last variant of the product?" (409 "Last
// variant") -> delete, cascading the variant's prices (which is why this
// operation's x-vantigo-access requires pricing-manage as well as
// variants-manage — products inventory §1.1 line 52).
func (s *server) DeleteProductsByIdVariantsByVariantId(ctx context.Context, req gen.DeleteProductsByIdVariantsByVariantIdRequestObject) (gen.DeleteProductsByIdVariantsByVariantIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	_, err := q.GetVariantByProductAndID(ctx, store.GetVariantByProductAndIDParams{ID: req.VariantId, ProductID: req.Id})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.DeleteProductsByIdVariantsByVariantId404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("products: get variant: %w", err)
	}

	remaining, err := q.CountOtherVariantsOfProduct(ctx, store.CountOtherVariantsOfProductParams{ProductID: req.Id, ID: req.VariantId})
	if err != nil {
		return nil, fmt.Errorf("products: count variants: %w", err)
	}
	if remaining == 0 {
		return gen.DeleteProductsByIdVariantsByVariantId409ApplicationProblemPlusJSONResponse(apicommon.ProblemStatus(
			"Last variant", "A product must have at least one variant.", http.StatusConflict)), nil
	}

	if err := q.DeleteProductVariant(ctx, req.VariantId); err != nil {
		return nil, fmt.Errorf("products: delete variant: %w", err)
	}
	return gen.DeleteProductsByIdVariantsByVariantId204Response{}, nil
}
