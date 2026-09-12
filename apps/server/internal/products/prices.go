package products

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/products/gen"
	"github.com/vantigo-io/vantigo/server/internal/products/store"
)

// This file is the Prices sub-resource (EP/Products/Prices/*Endpoint.cs):
// getProductsByIdVariantsByVariantIdPrices,
// postProductsByIdVariantsByVariantIdPrices,
// putProductsByIdVariantsByVariantIdPricesByPriceId and
// deleteProductsByIdVariantsByVariantIdPricesByPriceId. None of these four
// make a handler-side permission check: pricing-manage/pricing-view are
// always the flat, unconditional x-vantigo-access module.Router already
// enforces for every one of them.

// GetProductsByIdVariantsByVariantIdPrices List prices of a variant
// (GET /api/v1/products/{id}/variants/{variantId}/prices)
func (s *server) GetProductsByIdVariantsByVariantIdPrices(ctx context.Context, req gen.GetProductsByIdVariantsByVariantIdPricesRequestObject) (gen.GetProductsByIdVariantsByVariantIdPricesResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.GetVariantByProductAndID(ctx, store.GetVariantByProductAndIDParams{ID: req.VariantId, ProductID: req.Id}); errors.Is(err, pgx.ErrNoRows) {
		return gen.GetProductsByIdVariantsByVariantIdPrices404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("products: get variant: %w", err)
	}

	rows, err := q.ListPricesByVariant(ctx, req.VariantId)
	if err != nil {
		return nil, fmt.Errorf("products: list prices: %w", err)
	}
	data := make([]gen.ProductPriceResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, priceResponseFromRow(r))
	}
	return gen.GetProductsByIdVariantsByVariantIdPrices200JSONResponse(data), nil
}

// PostProductsByIdVariantsByVariantIdPrices Add a price to a variant
// (POST /api/v1/products/{id}/variants/{variantId}/prices)
//
// AddProductPriceEndpoint.cs:15-55: field validation -> variant exists
// scoped to product (404) -> ProductPricing.Conflicts against every
// existing price of that variant (409 "Overlapping price") -> append ->
// 201.
//
// products inventory §7 oddity 3, deliberate: AddProductPriceEndpoint.cs:52-54
// returns TypedResults.Created($"/api/v1/products/{id}/variants/{variantId}/prices", …)
// — the URL of the *collection*, not .../prices/{price.Id}. Every other
// Created response in the module points at the actual new resource; this
// one does not, and the port keeps it exactly that wrong. The contract
// declares a Location header on this operation's 201 (openapi/products.yaml)
// specifically so this handler can set it — before this task it declared
// none at all, an omission the same as a sibling task found for its own
// wrongly-emitted .NET header.
func (s *server) PostProductsByIdVariantsByVariantIdPrices(ctx context.Context, req gen.PostProductsByIdVariantsByVariantIdPricesRequestObject) (gen.PostProductsByIdVariantsByVariantIdPricesResponseObject, error) {
	body := gen.ProductPriceRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	errs := map[string][]string{}
	validatePriceRequest("", body, errs)
	if len(errs) > 0 {
		return gen.PostProductsByIdVariantsByVariantIdPrices400ApplicationProblemPlusJSONResponse(validationProblem("Invalid price", errs)), nil
	}

	q := store.New(s.deps.Pool)
	variant, err := q.GetVariantByProductAndID(ctx, store.GetVariantByProductAndIDParams{ID: req.VariantId, ProductID: req.Id})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostProductsByIdVariantsByVariantIdPrices404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("products: get variant: %w", err)
	}

	existingRows, err := q.ListPricesByVariant(ctx, req.VariantId)
	if err != nil {
		return nil, fmt.Errorf("products: list prices: %w", err)
	}

	candidate := toParsedPrice(body).domain(0)
	for _, r := range existingRows {
		if pricesConflict(candidate, domainPriceFromRow(r)) {
			return gen.PostProductsByIdVariantsByVariantIdPrices409ApplicationProblemPlusJSONResponse(problemStatus(
				"Overlapping price", fmt.Sprintf("The price overlaps an existing %s price of the same kind.", candidate.Currency), http.StatusConflict)), nil
		}
	}

	created, err := q.InsertProductPrice(ctx, store.InsertProductPriceParams{
		VariantID: variant.ID, Currency: candidate.Currency, Amount: numericFromFloat(candidate.Amount),
		ValidFrom: candidate.ValidFrom, ValidTo: candidate.ValidTo,
	})
	if err != nil {
		return nil, fmt.Errorf("products: add price: %w", err)
	}

	location := fmt.Sprintf("%s/api/v1/products/%d/variants/%d/prices", s.deps.Config.BasePath, req.Id, req.VariantId)
	return gen.PostProductsByIdVariantsByVariantIdPrices201JSONResponse{
		Body:    priceResponseFromRow(created),
		Headers: gen.PostProductsByIdVariantsByVariantIdPrices201ResponseHeaders{Location: &location},
	}, nil
}

// PutProductsByIdVariantsByVariantIdPricesByPriceId Update a variant price
// (PUT /api/v1/products/{id}/variants/{variantId}/prices/{priceId})
//
// UpdateProductPriceEndpoint.cs:17-61: field validation -> variant AND
// price (both scoped) exist (404 if either missing) -> Conflicts against
// every *other* price (excluding itself) (409) -> apply -> 200.
func (s *server) PutProductsByIdVariantsByVariantIdPricesByPriceId(ctx context.Context, req gen.PutProductsByIdVariantsByVariantIdPricesByPriceIdRequestObject) (gen.PutProductsByIdVariantsByVariantIdPricesByPriceIdResponseObject, error) {
	body := gen.ProductPriceRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	errs := map[string][]string{}
	validatePriceRequest("", body, errs)
	if len(errs) > 0 {
		return gen.PutProductsByIdVariantsByVariantIdPricesByPriceId400ApplicationProblemPlusJSONResponse(validationProblem("Invalid price", errs)), nil
	}

	q := store.New(s.deps.Pool)
	if _, err := q.GetPriceScoped(ctx, store.GetPriceScopedParams{PriceID: req.PriceId, VariantID: req.VariantId, ProductID: req.Id}); errors.Is(err, pgx.ErrNoRows) {
		return gen.PutProductsByIdVariantsByVariantIdPricesByPriceId404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("products: get price: %w", err)
	}

	allPrices, err := q.ListPricesByVariant(ctx, req.VariantId)
	if err != nil {
		return nil, fmt.Errorf("products: list prices: %w", err)
	}

	candidate := toParsedPrice(body).domain(0)
	for _, r := range allPrices {
		if r.ID == req.PriceId {
			continue
		}
		if pricesConflict(candidate, domainPriceFromRow(r)) {
			return gen.PutProductsByIdVariantsByVariantIdPricesByPriceId409ApplicationProblemPlusJSONResponse(problemStatus(
				"Overlapping price", fmt.Sprintf("The price overlaps an existing %s price of the same kind.", candidate.Currency), http.StatusConflict)), nil
		}
	}

	updated, err := q.UpdatePrice(ctx, store.UpdatePriceParams{
		Currency: candidate.Currency, Amount: numericFromFloat(candidate.Amount),
		ValidFrom: candidate.ValidFrom, ValidTo: candidate.ValidTo, ID: req.PriceId,
	})
	if err != nil {
		return nil, fmt.Errorf("products: update price: %w", err)
	}
	return gen.PutProductsByIdVariantsByVariantIdPricesByPriceId200JSONResponse(priceResponseFromRow(updated)), nil
}

// DeleteProductsByIdVariantsByVariantIdPricesByPriceId Remove a variant price
// (DELETE /api/v1/products/{id}/variants/{variantId}/prices/{priceId})
//
// DeleteProductPriceEndpoint.cs:13-33: a single scoped-existence query
// (404) -> delete -> 204. No "last price" rule — a variant may end up with
// zero prices.
func (s *server) DeleteProductsByIdVariantsByVariantIdPricesByPriceId(ctx context.Context, req gen.DeleteProductsByIdVariantsByVariantIdPricesByPriceIdRequestObject) (gen.DeleteProductsByIdVariantsByVariantIdPricesByPriceIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.GetPriceScoped(ctx, store.GetPriceScopedParams{PriceID: req.PriceId, VariantID: req.VariantId, ProductID: req.Id}); errors.Is(err, pgx.ErrNoRows) {
		return gen.DeleteProductsByIdVariantsByVariantIdPricesByPriceId404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("products: get price: %w", err)
	}

	if err := q.DeleteProductPrice(ctx, req.PriceId); err != nil {
		return nil, fmt.Errorf("products: delete price: %w", err)
	}
	return gen.DeleteProductsByIdVariantsByVariantIdPricesByPriceId204Response{}, nil
}
