package products

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/products/gen"
)

// Every operation of products.yaml not yet implemented, each answering
// module.ErrNotImplemented, which the strict server's response-error
// handler turns into a 501. Mounting them all is what satisfies
// module.Router's "never registered" check, so the contract is fully
// routed from the first commit and each later task replaces the stubs of
// the area it implements. Task 11 implemented products.go, variants.go and
// prices.go's thirteen operations; this file now holds only categories, tax
// categories and stats (Task 12).

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
