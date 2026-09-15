package products

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/products/gen"
	"github.com/vantigo-io/vantigo/server/internal/products/store"
)

// This file is the Categories sub-resource (EP/Categories/*Endpoint.cs):
// getProductsCategories, postProductsCategories, getCategory,
// putProductsCategoriesById and deleteProductsCategoriesById.

const categoryNameMaxLength = 200 // ProductCategory.NameMaxLength

// validateCategoryName is CategoryRequest.Validate's name check
// (CategoryRequest.cs:13-27): blank is a required-field error; the length
// check runs against the trimmed value.
func validateCategoryName(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "'name' is required."
	}
	trimmed := strings.TrimSpace(raw)
	if n := utf16Length(trimmed); n > categoryNameMaxLength {
		return "", fmt.Sprintf("'name' must be at most %d characters.", categoryNameMaxLength)
	}
	return trimmed, ""
}

// categoryResponse is CategoryResponse.FromDomain (CategoryResponse.cs:18-24).
// productCount is a caller-supplied parameter, not computed here, because
// FromDomain's own default (0) is not overridden by three of the four
// operations that build one: CreateCategoryEndpoint, GetCategoryEndpoint and
// UpdateCategoryEndpoint all call FromDomain(category) with no productCount
// argument at all, so a freshly created, singly fetched, or just-updated
// category always reports productCount:0 on the wire, correct total or not
// — only GetCategoriesEndpoint (the list) ever computes and passes a real
// count. Not documented in the products inventory; found by reading
// CategoryResponse.cs/GetCategoryEndpoint.cs/CreateCategoryEndpoint.cs/
// UpdateCategoryEndpoint.cs directly, the way Task 11 verified the Location
// header question against source rather than the inventory's summary.
func categoryResponse(row store.ProductsProductCategory, productCount int32) gen.CategoryResponse {
	return gen.CategoryResponse{Id: row.ID, Name: row.Name, ParentId: row.ParentID, ProductCount: productCount}
}

// wouldCreateCycle is ProductCategoryHierarchy.WouldCreateCycle
// (ProductCategoryHierarchy.cs:14-31): walks the ancestor chain from
// newParentID looking for categoryID. parentByID must carry every category
// (ListCategoryParents' full adjacency list, the same "catalog is small"
// premise products.go's selfAndDescendantCategoryIDs relies on) — a key
// absent from the map reads as nil, exactly like .NET's
// Dictionary.GetValueOrDefault, so an ancestor chain simply stops instead
// of panicking.
func wouldCreateCycle(categoryID int32, newParentID *int32, parentByID map[int32]*int32) bool {
	current := newParentID
	for current != nil {
		if *current == categoryID {
			return true
		}
		current = parentByID[*current]
	}
	return false
}

// GetProductsCategories List all categories
// (GET /api/v1/products/categories)
//
// GetCategoriesEndpoint.cs:14-39: the flat adjacency list ordered by name
// then id, each with its *direct* product count (never the subtree total —
// clients derive that from the flat list themselves).
func (s *server) GetProductsCategories(ctx context.Context, _ gen.GetProductsCategoriesRequestObject) (gen.GetProductsCategoriesResponseObject, error) {
	q := store.New(s.deps.Pool)
	rows, err := q.ListCategoriesOrdered(ctx)
	if err != nil {
		return nil, fmt.Errorf("products: list categories: %w", err)
	}
	counts, err := q.ListCategoryProductCounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("products: list category product counts: %w", err)
	}
	countByID := make(map[int32]int32, len(counts))
	for _, c := range counts {
		if c.ID != nil {
			countByID[*c.ID] = int32(c.Count)
		}
	}

	data := make([]gen.CategoryResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, categoryResponse(r, countByID[r.ID]))
	}
	return gen.GetProductsCategories200JSONResponse(data), nil
}

// PostProductsCategories Create a new category
// (POST /api/v1/products/categories)
//
// CreateCategoryEndpoint.cs:16-59 (products inventory §1.3): Validate(name)
// -> parentId existence (400 field, :27-33) -> duplicate sibling name (409,
// :35-44) -> insert -> 201 with Location (CreatedAtRoute, :55-58; the
// contract did not declare this header before this task — verified against
// source the same way Task 11 verified its own three Location questions,
// see this task's report).
func (s *server) PostProductsCategories(ctx context.Context, req gen.PostProductsCategoriesRequestObject) (gen.PostProductsCategoriesResponseObject, error) {
	body := gen.CategoryRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	name, nameErr := validateCategoryName(body.Name)
	if nameErr != "" {
		return gen.PostProductsCategories400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(
			"Invalid category", map[string][]string{"name": {nameErr}})), nil
	}

	q := store.New(s.deps.Pool)
	if body.ParentId != nil {
		exists, err := q.CategoryExists(ctx, *body.ParentId)
		if err != nil {
			return nil, fmt.Errorf("products: check parent category: %w", err)
		}
		if !exists {
			return gen.PostProductsCategories400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid category",
				map[string][]string{"parentId": {fmt.Sprintf("Category %d does not exist.", *body.ParentId)}})), nil
		}
	}

	conflict, err := q.CategorySiblingNameExists(ctx, store.CategorySiblingNameExistsParams{ParentID: body.ParentId, Name: name})
	if err != nil {
		return nil, fmt.Errorf("products: check sibling name: %w", err)
	}
	if conflict {
		return gen.PostProductsCategories409ApplicationProblemPlusJSONResponse(apicommon.ProblemStatus(
			"Duplicate category name", fmt.Sprintf("A category named '%s' already exists under the same parent.", name),
			http.StatusConflict)), nil
	}

	created, err := q.InsertCategory(ctx, store.InsertCategoryParams{Name: name, ParentID: body.ParentId})
	if err != nil {
		return nil, fmt.Errorf("products: insert category: %w", err)
	}

	location := fmt.Sprintf("%s/api/v1/products/categories/%d", s.deps.Config.BasePath, created.ID)
	return gen.PostProductsCategories201JSONResponse{
		Body:    categoryResponse(created, 0),
		Headers: gen.PostProductsCategories201ResponseHeaders{Location: &location},
	}, nil
}

// GetCategory Get a category by id
// (GET /api/v1/products/categories/{id})
func (s *server) GetCategory(ctx context.Context, req gen.GetCategoryRequestObject) (gen.GetCategoryResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetCategoryByID(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetCategory404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("products: get category: %w", err)
	}
	return gen.GetCategory200JSONResponse(categoryResponse(row, 0)), nil
}

// PutProductsCategoriesById Update a category
// (PUT /api/v1/products/categories/{id})
//
// UpdateCategoryEndpoint.cs:16-81 (products inventory §1.3): Validate(name)
// -> category exists (404) -> parentId == id self-parent (400 field,
// checked *before* parent existence, :36-41) -> parent exists (400 field,
// :43-54) -> cycle check via WouldCreateCycle (409, :56-62) -> duplicate
// sibling name excluding self (409, :65-74) -> apply.
func (s *server) PutProductsCategoriesById(ctx context.Context, req gen.PutProductsCategoriesByIdRequestObject) (gen.PutProductsCategoriesByIdResponseObject, error) {
	body := gen.CategoryRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	name, nameErr := validateCategoryName(body.Name)
	if nameErr != "" {
		return gen.PutProductsCategoriesById400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(
			"Invalid category", map[string][]string{"name": {nameErr}})), nil
	}

	q := store.New(s.deps.Pool)
	if _, err := q.GetCategoryByID(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return gen.PutProductsCategoriesById404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("products: get category: %w", err)
	}

	if body.ParentId != nil && *body.ParentId == req.Id {
		return gen.PutProductsCategoriesById400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid category",
			map[string][]string{"parentId": {"A category cannot be its own parent."}})), nil
	}

	if body.ParentId != nil {
		parents, err := q.ListCategoryParents(ctx)
		if err != nil {
			return nil, fmt.Errorf("products: list category parents: %w", err)
		}
		parentByID := make(map[int32]*int32, len(parents))
		for _, p := range parents {
			parentByID[p.ID] = p.ParentID
		}
		if _, ok := parentByID[*body.ParentId]; !ok {
			return gen.PutProductsCategoriesById400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid category",
				map[string][]string{"parentId": {fmt.Sprintf("Category %d does not exist.", *body.ParentId)}})), nil
		}
		if wouldCreateCycle(req.Id, body.ParentId, parentByID) {
			return gen.PutProductsCategoriesById409ApplicationProblemPlusJSONResponse(apicommon.ProblemStatus(
				"Category cycle", "The category cannot be moved under one of its own descendants.", http.StatusConflict)), nil
		}
	}

	conflict, err := q.CategorySiblingNameExistsExcluding(ctx, store.CategorySiblingNameExistsExcludingParams{
		ParentID: body.ParentId, Name: name, ID: req.Id,
	})
	if err != nil {
		return nil, fmt.Errorf("products: check sibling name: %w", err)
	}
	if conflict {
		return gen.PutProductsCategoriesById409ApplicationProblemPlusJSONResponse(apicommon.ProblemStatus(
			"Duplicate category name", fmt.Sprintf("A category named '%s' already exists under the same parent.", name),
			http.StatusConflict)), nil
	}

	updated, err := q.UpdateCategory(ctx, store.UpdateCategoryParams{Name: name, ParentID: body.ParentId, ID: req.Id})
	if err != nil {
		return nil, fmt.Errorf("products: update category: %w", err)
	}
	return gen.PutProductsCategoriesById200JSONResponse(categoryResponse(updated, 0)), nil
}

// DeleteProductsCategoriesById Delete a category
// (DELETE /api/v1/products/categories/{id})
//
// DeleteCategoryEndpoint.cs:12-48: exists (404) -> delete, restricted while
// the category still has subcategories or assigned products.
//
// Unlike .NET, this handler does not run "has subcategories"/"has products"
// as separate SELECT pre-checks before the delete: it attempts the delete
// directly, so a blocked delete always exercises the table's real
// parent_id/category_id Restrict foreign keys (products inventory §3) —
// and, when blocked, diagnoses *why* with the same two follow-up queries
// .NET runs first, in the same order, to answer with the same title/detail
// text .NET's pre-check would have. This is a deliberate divergence in
// mechanism, not outcome: for any single request the observable result
// (404 / "has subcategories" 409 / "has products" 409 / 204) is identical
// to .NET's precheck-then-delete order, and it means the ordinary,
// non-racy "delete a category with a child" test already proves the real
// SQLSTATE mapping below, with no contrived concurrency needed.
//
// products inventory §4/§7 oddity 7 (corrected 2026-09-12): a literal
// `ON DELETE RESTRICT` raises Postgres SQLSTATE 23001 (restrict_violation),
// confirmed against a real instance in internal/db/schema_test.go;
// 23503 (foreign_key_violation) is not reachable through this schema today
// (no NO ACTION/deferred FK exists here) but is mapped defensively too,
// since a cascade/deferred path could still raise it. .NET's global
// exception handler special-cases only 23505/23P01
// (VantigoExceptionHandler.cs:97-98) and would fall through to a bare 500
// here; this module answers the documented 409 instead — see errors.go's
// isRestrictConflict and this task's report.
func (s *server) DeleteProductsCategoriesById(ctx context.Context, req gen.DeleteProductsCategoriesByIdRequestObject) (gen.DeleteProductsCategoriesByIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.GetCategoryByID(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return gen.DeleteProductsCategoriesById404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("products: get category: %w", err)
	}

	if err := q.DeleteCategory(ctx, req.Id); err != nil {
		if !isRestrictConflict(err) {
			return nil, fmt.Errorf("products: delete category: %w", err)
		}
		hasChildren, chErr := q.CategoryHasSubcategories(ctx, req.Id)
		if chErr != nil {
			return nil, fmt.Errorf("products: check subcategories: %w", chErr)
		}
		if hasChildren {
			return gen.DeleteProductsCategoriesById409ApplicationProblemPlusJSONResponse(apicommon.ProblemStatus(
				"Category has subcategories", "Delete or move the subcategories before deleting the category.",
				http.StatusConflict)), nil
		}
		hasProducts, pErr := q.CategoryHasProducts(ctx, req.Id)
		if pErr != nil {
			return nil, fmt.Errorf("products: check products: %w", pErr)
		}
		if hasProducts {
			return gen.DeleteProductsCategoriesById409ApplicationProblemPlusJSONResponse(apicommon.ProblemStatus(
				"Category has products", "Reassign or uncategorise the products before deleting the category.",
				http.StatusConflict)), nil
		}
		// Lost an actual race: whatever blocked the delete a moment ago is
		// already gone by the time these two follow-up checks ran. Still a
		// real conflict from the caller's point of view — the delete did
		// fail — so it still answers 409, just with neither of .NET's two
		// specific titles, since neither reason is true any more.
		return gen.DeleteProductsCategoriesById409ApplicationProblemPlusJSONResponse(apicommon.ProblemStatus(
			"Category is still referenced",
			"The category could not be deleted because something still referenced it. Try again.",
			http.StatusConflict)), nil
	}
	return gen.DeleteProductsCategoriesById204Response{}, nil
}
