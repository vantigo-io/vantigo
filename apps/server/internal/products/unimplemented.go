package products

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/products/gen"
)

// Every operation of products.yaml, each answering module.ErrNotImplemented,
// which the strict server's response-error handler turns into a 501. Mounting
// them all is what satisfies module.Router's "never registered" check, so the
// contract is fully routed from the first commit and each later task replaces
// the stubs of the area it implements.

// GetProducts List all products
// (GET /api/v1/products)
func (s *server) GetProducts(context.Context, gen.GetProductsRequestObject) (gen.GetProductsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PostProducts Create a new product
// (POST /api/v1/products)
func (s *server) PostProducts(context.Context, gen.PostProductsRequestObject) (gen.PostProductsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetProductsCategories List all categories
// (GET /api/v1/products/categories)
func (s *server) GetProductsCategories(context.Context, gen.GetProductsCategoriesRequestObject) (gen.GetProductsCategoriesResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PostProductsCategories Create a new category
// (POST /api/v1/products/categories)
func (s *server) PostProductsCategories(context.Context, gen.PostProductsCategoriesRequestObject) (gen.PostProductsCategoriesResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// DeleteProductsCategoriesById Delete a category
// (DELETE /api/v1/products/categories/{id})
func (s *server) DeleteProductsCategoriesById(context.Context, gen.DeleteProductsCategoriesByIdRequestObject) (gen.DeleteProductsCategoriesByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetCategory Get a category by id
// (GET /api/v1/products/categories/{id})
func (s *server) GetCategory(context.Context, gen.GetCategoryRequestObject) (gen.GetCategoryResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PutProductsCategoriesById Update a category
// (PUT /api/v1/products/categories/{id})
func (s *server) PutProductsCategoriesById(context.Context, gen.PutProductsCategoriesByIdRequestObject) (gen.PutProductsCategoriesByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetProductsStatsAttention Get products dashboard attention items
// (GET /api/v1/products/stats/attention)
func (s *server) GetProductsStatsAttention(context.Context, gen.GetProductsStatsAttentionRequestObject) (gen.GetProductsStatsAttentionResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetProductsStatsSummary Get products dashboard summary
// (GET /api/v1/products/stats/summary)
func (s *server) GetProductsStatsSummary(context.Context, gen.GetProductsStatsSummaryRequestObject) (gen.GetProductsStatsSummaryResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetProductsStatsTimeseries Get products dashboard time series
// (GET /api/v1/products/stats/timeseries)
func (s *server) GetProductsStatsTimeseries(context.Context, gen.GetProductsStatsTimeseriesRequestObject) (gen.GetProductsStatsTimeseriesResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetProductsTaxCategories List all tax categories
// (GET /api/v1/products/tax-categories)
func (s *server) GetProductsTaxCategories(context.Context, gen.GetProductsTaxCategoriesRequestObject) (gen.GetProductsTaxCategoriesResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PostProductsTaxCategories Create a tax category
// (POST /api/v1/products/tax-categories)
func (s *server) PostProductsTaxCategories(context.Context, gen.PostProductsTaxCategoriesRequestObject) (gen.PostProductsTaxCategoriesResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// DeleteProductsTaxCategoriesById Delete a tax category
// (DELETE /api/v1/products/tax-categories/{id})
func (s *server) DeleteProductsTaxCategoriesById(context.Context, gen.DeleteProductsTaxCategoriesByIdRequestObject) (gen.DeleteProductsTaxCategoriesByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetTaxCategory Get a tax category by id
// (GET /api/v1/products/tax-categories/{id})
func (s *server) GetTaxCategory(context.Context, gen.GetTaxCategoryRequestObject) (gen.GetTaxCategoryResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PutProductsTaxCategoriesById Update a tax category
// (PUT /api/v1/products/tax-categories/{id})
func (s *server) PutProductsTaxCategoriesById(context.Context, gen.PutProductsTaxCategoriesByIdRequestObject) (gen.PutProductsTaxCategoriesByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// DeleteProductsById Archive a product
// (DELETE /api/v1/products/{id})
func (s *server) DeleteProductsById(context.Context, gen.DeleteProductsByIdRequestObject) (gen.DeleteProductsByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetProduct Get a product by id
// (GET /api/v1/products/{id})
func (s *server) GetProduct(context.Context, gen.GetProductRequestObject) (gen.GetProductResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PutProductsById Update a product
// (PUT /api/v1/products/{id})
func (s *server) PutProductsById(context.Context, gen.PutProductsByIdRequestObject) (gen.PutProductsByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetProductsByIdVariants List variants of a product
// (GET /api/v1/products/{id}/variants)
func (s *server) GetProductsByIdVariants(context.Context, gen.GetProductsByIdVariantsRequestObject) (gen.GetProductsByIdVariantsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PostProductsByIdVariants Add a variant to a product
// (POST /api/v1/products/{id}/variants)
func (s *server) PostProductsByIdVariants(context.Context, gen.PostProductsByIdVariantsRequestObject) (gen.PostProductsByIdVariantsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// DeleteProductsByIdVariantsByVariantId Remove a product variant
// (DELETE /api/v1/products/{id}/variants/{variantId})
func (s *server) DeleteProductsByIdVariantsByVariantId(context.Context, gen.DeleteProductsByIdVariantsByVariantIdRequestObject) (gen.DeleteProductsByIdVariantsByVariantIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PutProductsByIdVariantsByVariantId Update a product variant
// (PUT /api/v1/products/{id}/variants/{variantId})
func (s *server) PutProductsByIdVariantsByVariantId(context.Context, gen.PutProductsByIdVariantsByVariantIdRequestObject) (gen.PutProductsByIdVariantsByVariantIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetProductsByIdVariantsByVariantIdPrices List prices of a variant
// (GET /api/v1/products/{id}/variants/{variantId}/prices)
func (s *server) GetProductsByIdVariantsByVariantIdPrices(context.Context, gen.GetProductsByIdVariantsByVariantIdPricesRequestObject) (gen.GetProductsByIdVariantsByVariantIdPricesResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PostProductsByIdVariantsByVariantIdPrices Add a price to a variant
// (POST /api/v1/products/{id}/variants/{variantId}/prices)
func (s *server) PostProductsByIdVariantsByVariantIdPrices(context.Context, gen.PostProductsByIdVariantsByVariantIdPricesRequestObject) (gen.PostProductsByIdVariantsByVariantIdPricesResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// DeleteProductsByIdVariantsByVariantIdPricesByPriceId Remove a variant price
// (DELETE /api/v1/products/{id}/variants/{variantId}/prices/{priceId})
func (s *server) DeleteProductsByIdVariantsByVariantIdPricesByPriceId(context.Context, gen.DeleteProductsByIdVariantsByVariantIdPricesByPriceIdRequestObject) (gen.DeleteProductsByIdVariantsByVariantIdPricesByPriceIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PutProductsByIdVariantsByVariantIdPricesByPriceId Update a variant price
// (PUT /api/v1/products/{id}/variants/{variantId}/prices/{priceId})
func (s *server) PutProductsByIdVariantsByVariantIdPricesByPriceId(context.Context, gen.PutProductsByIdVariantsByVariantIdPricesByPriceIdRequestObject) (gen.PutProductsByIdVariantsByVariantIdPricesByPriceIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}
