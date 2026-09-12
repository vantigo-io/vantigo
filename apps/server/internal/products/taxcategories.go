package products

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/products/gen"
	"github.com/vantigo-io/vantigo/server/internal/products/store"
)

// This file is the Tax categories sub-resource (EP/TaxCategories/*Endpoint.cs):
// getProductsTaxCategories, postProductsTaxCategories, getTaxCategory,
// putProductsTaxCategoriesById and deleteProductsTaxCategoriesById.

const taxCategoryNameMaxLength = 100 // TaxCategory.NameMaxLength

// validateTaxCategoryName is TaxCategoryRequest.Validate's name check
// (TaxCategoryRequest.cs:16-23).
func validateTaxCategoryName(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "'name' is required."
	}
	trimmed := strings.TrimSpace(raw)
	if n := utf16Length(trimmed); n > taxCategoryNameMaxLength {
		return "", fmt.Sprintf("'name' must be at most %d characters.", taxCategoryNameMaxLength)
	}
	return trimmed, ""
}

// taxCategoryKindsByLower canonicalizes TaxCategoryKind
// (Domain/Products/TaxCategoryKind.cs), parsed case-insensitively
// (Enum.TryParse ignoreCase:true) but always returned in its exact
// PascalCase spelling, matching validateProductType/validateProductStatus's
// shape in values.go.
var taxCategoryKindsByLower = map[string]string{
	"standard": "Standard",
	"reduced":  "Reduced",
	"zero":     "Zero",
	"exempt":   "Exempt",
}

// validateTaxCategoryKind is TaxCategoryRequest.Validate's kind check
// (:25-28).
func validateTaxCategoryKind(raw string) (string, string) {
	if canonical, ok := taxCategoryKindsByLower[strings.ToLower(raw)]; ok {
		return canonical, ""
	}
	return "", fmt.Sprintf("'kind' must be one of 'Standard', 'Reduced', 'Zero' or 'Exempt', but was '%s'.", raw)
}

// validateTaxCategoryRate is TaxCategoryRequest.Validate's rate check
// (:30-33): a fraction, 0 <= rate <= 1, no rounding at the app level —
// Postgres's numeric(5,4) column scale rounds on write (products inventory
// §2), exactly as .NET relied on it to.
func validateTaxCategoryRate(rate float64) string {
	if rate < 0 || rate > 1 {
		return fmt.Sprintf("'rate' must be between 0 and 1, but was %s.", formatAmount(rate))
	}
	return ""
}

// validateTaxCategoryRequest runs all three TaxCategoryRequest.Validate
// checks (:12-36), returning the normalized name/kind alongside the
// field-keyed errors CreateTaxCategoryEndpoint/UpdateTaxCategoryEndpoint
// both answer as one ValidationProblem.
func validateTaxCategoryRequest(body gen.TaxCategoryRequest) (name, kind string, errs map[string][]string) {
	errs = map[string][]string{}
	var nameErr, kindErr string
	name, nameErr = validateTaxCategoryName(body.Name)
	if nameErr != "" {
		errs["name"] = []string{nameErr}
	}
	kind, kindErr = validateTaxCategoryKind(body.Kind)
	if kindErr != "" {
		errs["kind"] = []string{kindErr}
	}
	if rateErr := validateTaxCategoryRate(body.Rate); rateErr != "" {
		errs["rate"] = []string{rateErr}
	}
	return name, kind, errs
}

// taxCategoryResponse is TaxCategoryResponse.FromDomain
// (TaxCategoryResponse.cs:13-19).
func taxCategoryResponse(id int32, name, kind string, rate pgtype.Numeric) gen.TaxCategoryResponse {
	return gen.TaxCategoryResponse{Id: id, Name: name, Kind: kind, Rate: floatFromNumeric(rate)}
}

// GetProductsTaxCategories List all tax categories
// (GET /api/v1/products/tax-categories)
//
// GetTaxCategoriesEndpoint.cs:9-22: ordered by name.
func (s *server) GetProductsTaxCategories(ctx context.Context, _ gen.GetProductsTaxCategoriesRequestObject) (gen.GetProductsTaxCategoriesResponseObject, error) {
	q := store.New(s.deps.Pool)
	rows, err := q.ListTaxCategoriesOrdered(ctx)
	if err != nil {
		return nil, fmt.Errorf("products: list tax categories: %w", err)
	}
	data := make([]gen.TaxCategoryResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, taxCategoryResponse(r.ID, r.Name, r.Kind, r.Rate))
	}
	return gen.GetProductsTaxCategories200JSONResponse(data), nil
}

// PostProductsTaxCategories Create a tax category
// (POST /api/v1/products/tax-categories)
//
// CreateTaxCategoryEndpoint.cs:9-39 (products inventory §1.3): Validate ->
// duplicate name (409, :22-29) -> insert -> 201 with Location
// (CreatedAtRoute, :35-38; the contract did not declare this header before
// this task — verified against source, this task's report).
func (s *server) PostProductsTaxCategories(ctx context.Context, req gen.PostProductsTaxCategoriesRequestObject) (gen.PostProductsTaxCategoriesResponseObject, error) {
	body := gen.TaxCategoryRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	name, kind, errs := validateTaxCategoryRequest(body)
	if len(errs) > 0 {
		return gen.PostProductsTaxCategories400ApplicationProblemPlusJSONResponse(validationProblem("Invalid tax category", errs)), nil
	}

	q := store.New(s.deps.Pool)
	conflict, err := q.TaxCategoryNameExists(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("products: check tax category name: %w", err)
	}
	if conflict {
		return gen.PostProductsTaxCategories409ApplicationProblemPlusJSONResponse(problemStatus(
			"Duplicate tax category name", fmt.Sprintf("A tax category named '%s' already exists.", name), http.StatusConflict)), nil
	}

	rate, err := numericFromFloat(body.Rate)
	if err != nil {
		return nil, err
	}
	created, err := q.InsertTaxCategory(ctx, store.InsertTaxCategoryParams{
		Name: name, Kind: kind, Rate: rate, Now: s.deps.Clock(),
	})
	if err != nil {
		return nil, fmt.Errorf("products: insert tax category: %w", err)
	}

	location := fmt.Sprintf("%s/api/v1/products/tax-categories/%d", s.deps.Config.BasePath, created.ID)
	return gen.PostProductsTaxCategories201JSONResponse{
		Body:    taxCategoryResponse(created.ID, created.Name, created.Kind, created.Rate),
		Headers: gen.PostProductsTaxCategories201ResponseHeaders{Location: &location},
	}, nil
}

// GetTaxCategory Get a tax category by id
// (GET /api/v1/products/tax-categories/{id})
func (s *server) GetTaxCategory(ctx context.Context, req gen.GetTaxCategoryRequestObject) (gen.GetTaxCategoryResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetTaxCategoryRef(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetTaxCategory404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("products: get tax category: %w", err)
	}
	return gen.GetTaxCategory200JSONResponse(taxCategoryResponse(row.ID, row.Name, row.Kind, row.Rate)), nil
}

// PutProductsTaxCategoriesById Update a tax category
// (PUT /api/v1/products/tax-categories/{id})
//
// UpdateTaxCategoryEndpoint.cs:10-47 (products inventory §1.3): Validate ->
// exists (404) -> duplicate name excluding self (409, :30-38) -> apply.
func (s *server) PutProductsTaxCategoriesById(ctx context.Context, req gen.PutProductsTaxCategoriesByIdRequestObject) (gen.PutProductsTaxCategoriesByIdResponseObject, error) {
	body := gen.TaxCategoryRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	name, kind, errs := validateTaxCategoryRequest(body)
	if len(errs) > 0 {
		return gen.PutProductsTaxCategoriesById400ApplicationProblemPlusJSONResponse(validationProblem("Invalid tax category", errs)), nil
	}

	q := store.New(s.deps.Pool)
	if _, err := q.GetTaxCategoryRef(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return gen.PutProductsTaxCategoriesById404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("products: get tax category: %w", err)
	}

	conflict, err := q.TaxCategoryNameExistsExcluding(ctx, store.TaxCategoryNameExistsExcludingParams{Name: name, ID: req.Id})
	if err != nil {
		return nil, fmt.Errorf("products: check tax category name: %w", err)
	}
	if conflict {
		return gen.PutProductsTaxCategoriesById409ApplicationProblemPlusJSONResponse(problemStatus(
			"Duplicate tax category name", fmt.Sprintf("A tax category named '%s' already exists.", name), http.StatusConflict)), nil
	}

	rate, err := numericFromFloat(body.Rate)
	if err != nil {
		return nil, err
	}
	updated, err := q.UpdateTaxCategory(ctx, store.UpdateTaxCategoryParams{
		Name: name, Kind: kind, Rate: rate, UpdatedAt: s.deps.Clock(), ID: req.Id,
	})
	if err != nil {
		return nil, fmt.Errorf("products: update tax category: %w", err)
	}
	return gen.PutProductsTaxCategoriesById200JSONResponse(taxCategoryResponse(updated.ID, updated.Name, updated.Kind, updated.Rate)), nil
}

// DeleteProductsTaxCategoriesById Delete a tax category
// (DELETE /api/v1/products/tax-categories/{id})
//
// DeleteTaxCategoryEndpoint.cs:8-33: exists (404) -> delete, restricted
// while any product still references the tax category.
//
// Like DeleteProductsCategoriesById (categories.go), this handler attempts
// the delete directly rather than running .NET's separate AnyAsync
// pre-check first, so a blocked delete always exercises the real
// tax_category_id Restrict foreign key: the "in use" .NET test this task
// ports doubles as proof the SQLSTATE mapping below actually fires, no
// contrived race required. products inventory §4/§7 oddity 7 (corrected
// 2026-09-12) and errors.go's isRestrictConflict cover the two SQLSTATEs
// mapped and why this is a deliberate divergence from .NET's 500.
func (s *server) DeleteProductsTaxCategoriesById(ctx context.Context, req gen.DeleteProductsTaxCategoriesByIdRequestObject) (gen.DeleteProductsTaxCategoriesByIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.GetTaxCategoryRef(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return gen.DeleteProductsTaxCategoriesById404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("products: get tax category: %w", err)
	}

	if err := q.DeleteTaxCategory(ctx, req.Id); err != nil {
		if !isRestrictConflict(err) {
			return nil, fmt.Errorf("products: delete tax category: %w", err)
		}
		return gen.DeleteProductsTaxCategoriesById409ApplicationProblemPlusJSONResponse(problemStatus(
			"Tax category has products", "Reassign the products before deleting the tax category.", http.StatusConflict)), nil
	}
	return gen.DeleteProductsTaxCategoriesById204Response{}, nil
}
