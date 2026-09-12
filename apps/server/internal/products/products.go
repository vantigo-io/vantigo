package products

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	apicommon "github.com/vantigo-io/vantigo/server/internal/apicommon/gen"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/products/gen"
	"github.com/vantigo-io/vantigo/server/internal/products/store"
)

// This file is the Products area (EP/ProductsEndpoints.cs's bare group plus
// the variant sub-resource's creation, which shares the conditional pricing
// gate): getProducts, postProducts, getProduct, putProductsById and
// deleteProductsById. variants.go and prices.go cover the rest of the
// aggregate.

// likeReplacer/likePattern duplicate customers/customers.go's ILIKE
// escaping (EscapeLikePattern): depguard forbids this module importing
// customers, and the function is three lines.
var likeReplacer = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func likePattern(search string) string {
	return "%" + likeReplacer.Replace(search) + "%"
}

// paginationMetadata is PaginationMetadata.Create
// (Endpoints/Dtos/PaginationMetadata.cs:18-31), duplicated from customers'
// errors.go under the same name: depguard forbids this module importing
// customers, so the body is copied, but a reader comparing the two modules
// should not have to notice that one calls it something else.
func paginationMetadata(page, pageSize, totalCount int32) apicommon.PaginationMetadata {
	var totalPages int32
	if pageSize > 0 {
		totalPages = int32(math.Ceil(float64(totalCount) / float64(pageSize)))
	}
	return apicommon.PaginationMetadata{
		Page:            page,
		PageSize:        pageSize,
		TotalCount:      totalCount,
		TotalPages:      totalPages,
		HasNextPage:     page < totalPages,
		HasPreviousPage: page > 1 && totalCount > 0,
	}
}

// firstRepeatedInOrder is CreateProductEndpoint's
// variants.GroupBy(v => v.Sku).FirstOrDefault(g => g.Count() > 1)?.Key
// (:46-47): LINQ's GroupBy preserves first-occurrence order, so this is the
// first key, in order of first appearance, that occurs more than once —
// not necessarily the first *pair* encountered chronologically. "" means no
// request-internal duplicate.
func firstRepeatedInOrder(values []string) string {
	counts := map[string]int{}
	var order []string
	for _, v := range values {
		if counts[v] == 0 {
			order = append(order, v)
		}
		counts[v]++
	}
	for _, v := range order {
		if counts[v] > 1 {
			return v
		}
	}
	return ""
}

// selfAndDescendantCategoryIDs is
// ProductCategoryHierarchy.GetSelfAndDescendantIds
// (ProductCategoryHierarchy.cs:37-60): every category id reachable from
// root by following parent->children edges, root included. Only GetProducts
// needs this read-side walk; Task 12 owns category CRUD and cycle
// detection (WouldCreateCycle) separately.
func selfAndDescendantCategoryIDs(root int32, parents []store.ListCategoryParentsRow) []int32 {
	children := map[int32][]int32{}
	for _, p := range parents {
		if p.ParentID != nil {
			children[*p.ParentID] = append(children[*p.ParentID], p.ID)
		}
	}
	result := []int32{root}
	stack := []int32{root}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, child := range children[id] {
			result = append(result, child)
			stack = append(stack, child)
		}
	}
	return result
}

// validateGetProductsParams is GetProductsEndpoint.Validate
// (GetProductsEndpoint.cs:86-134): every check runs regardless of the
// others, and every failure's message is collected, to be joined with a
// single space into one ProblemDetails.Detail (.NET's
// string.Join(" ", errors)) — unlike every mutating endpoint's
// field-keyed HttpValidationProblemDetails (products inventory §7 oddity
// 8).
func validateGetProductsParams(p gen.GetProductsParams) []string {
	var errs []string
	if p.Page != nil && *p.Page < 1 {
		errs = append(errs, fmt.Sprintf("'page' must be 1 or greater, but was %d.", *p.Page))
	}
	if p.PageSize != nil && (*p.PageSize < 1 || *p.PageSize > 100) {
		errs = append(errs, fmt.Sprintf("'pageSize' must be between 1 and 100, but was %d.", *p.PageSize))
	}
	if p.SortBy != nil && *p.SortBy != "id" && *p.SortBy != "name" && *p.SortBy != "sku" {
		errs = append(errs, fmt.Sprintf("'sortBy' must be one of 'id', 'name' or 'sku', but was '%s'.", *p.SortBy))
	}
	if p.SortDirection != nil && *p.SortDirection != "asc" && *p.SortDirection != "desc" {
		errs = append(errs, fmt.Sprintf("'sortDirection' must be one of 'asc' or 'desc', but was '%s'.", *p.SortDirection))
	}
	if p.Status != nil {
		if _, err := validateProductStatus(*p.Status); err != "" {
			errs = append(errs, err)
		}
	}
	if p.CategoryId != nil && *p.CategoryId < 1 {
		errs = append(errs, fmt.Sprintf("'categoryId' must be 1 or greater, but was %d.", *p.CategoryId))
	}
	if p.CategoryId != nil && p.Uncategorized != nil && *p.Uncategorized {
		errs = append(errs, "'categoryId' and 'uncategorized' cannot be combined.")
	}
	return errs
}

// buildProductResponses batch-loads every product's variants, prices,
// category and tax category in a handful of queries (never one per row)
// and assembles each into a gen.ProductResponse, in rows' order.
func (s *server) buildProductResponses(ctx context.Context, q *store.Queries, rows []store.ProductsProduct, moment time.Time) ([]gen.ProductResponse, error) {
	if len(rows) == 0 {
		return []gen.ProductResponse{}, nil
	}

	productIDs := make([]int32, len(rows))
	categoryIDSet := map[int32]bool{}
	taxCategoryIDSet := map[int32]bool{}
	for i, p := range rows {
		productIDs[i] = p.ID
		if p.CategoryID != nil {
			categoryIDSet[*p.CategoryID] = true
		}
		taxCategoryIDSet[p.TaxCategoryID] = true
	}

	variants, err := q.ListVariantsByProductIDs(ctx, productIDs)
	if err != nil {
		return nil, fmt.Errorf("products: list variants: %w", err)
	}
	variantIDs := make([]int32, len(variants))
	variantsByProduct := map[int32][]store.ProductsProductVariant{}
	for i, v := range variants {
		variantIDs[i] = v.ID
		variantsByProduct[v.ProductID] = append(variantsByProduct[v.ProductID], v)
	}

	prices, err := q.ListPricesByVariantIDs(ctx, variantIDs)
	if err != nil {
		return nil, fmt.Errorf("products: list prices: %w", err)
	}
	pricesByVariant := map[int32][]store.ProductsProductPrice{}
	for _, p := range prices {
		pricesByVariant[p.VariantID] = append(pricesByVariant[p.VariantID], p)
	}

	categoryIDs := make([]int32, 0, len(categoryIDSet))
	for id := range categoryIDSet {
		categoryIDs = append(categoryIDs, id)
	}
	categories, err := q.ListCategoryRefs(ctx, categoryIDs)
	if err != nil {
		return nil, fmt.Errorf("products: list categories: %w", err)
	}
	categoryByID := make(map[int32]gen.ProductCategoryResponse, len(categories))
	for _, c := range categories {
		categoryByID[c.ID] = categoryRef(c.ID, c.Name)
	}

	taxCategoryIDs := make([]int32, 0, len(taxCategoryIDSet))
	for id := range taxCategoryIDSet {
		taxCategoryIDs = append(taxCategoryIDs, id)
	}
	taxCategories, err := q.ListTaxCategoryRefs(ctx, taxCategoryIDs)
	if err != nil {
		return nil, fmt.Errorf("products: list tax categories: %w", err)
	}
	taxCategoryByID := make(map[int32]gen.ProductTaxCategoryResponse, len(taxCategories))
	for _, t := range taxCategories {
		taxCategoryByID[t.ID] = taxCategoryRef(t.ID, t.Name, t.Kind, t.Rate)
	}

	data := make([]gen.ProductResponse, 0, len(rows))
	for _, p := range rows {
		rowVariants := variantsByProduct[p.ID]
		variantResponses := make([]gen.ProductVariantResponse, 0, len(rowVariants))
		for _, v := range rowVariants {
			variantResponses = append(variantResponses, variantResponse(v, pricesByVariant[v.ID], moment))
		}
		var category *gen.ProductCategoryResponse
		if p.CategoryID != nil {
			if c, ok := categoryByID[*p.CategoryID]; ok {
				category = &c
			}
		}
		data = append(data, productResponse(p, category, taxCategoryByID[p.TaxCategoryID], variantResponses))
	}
	return data, nil
}

// GetProducts List all products
// (GET /api/v1/products)
func (s *server) GetProducts(ctx context.Context, req gen.GetProductsRequestObject) (gen.GetProductsResponseObject, error) {
	if msgs := validateGetProductsParams(req.Params); len(msgs) > 0 {
		return gen.GetProducts400ApplicationProblemPlusJSONResponse(problem("Invalid query parameters", strings.Join(msgs, " "))), nil
	}

	page := int32(1)
	if req.Params.Page != nil {
		page = *req.Params.Page
	}
	pageSize := int32(25)
	if req.Params.PageSize != nil {
		pageSize = *req.Params.PageSize
	}

	q := store.New(s.deps.Pool)

	var status *string
	if req.Params.Status != nil {
		canonical, _ := validateProductStatus(*req.Params.Status)
		status = &canonical
	}

	var searchPattern, searchExact *string
	if req.Params.Search != nil {
		if trimmed := strings.TrimSpace(*req.Params.Search); trimmed != "" {
			p := likePattern(trimmed)
			searchPattern, searchExact = &p, &trimmed
		}
	}

	uncategorized := req.Params.Uncategorized != nil && *req.Params.Uncategorized
	var categoryIDs []int32
	if req.Params.CategoryId != nil {
		parents, err := q.ListCategoryParents(ctx)
		if err != nil {
			return nil, fmt.Errorf("products: list category parents: %w", err)
		}
		categoryIDs = selfAndDescendantCategoryIDs(*req.Params.CategoryId, parents)
	}

	total, err := q.CountProducts(ctx, store.CountProductsParams{
		Status: status, CategoryIds: categoryIDs, Uncategorized: uncategorized, SearchPattern: searchPattern, SearchExact: searchExact,
	})
	if err != nil {
		return nil, fmt.Errorf("products: count products: %w", err)
	}

	descending := req.Params.SortDirection != nil && *req.Params.SortDirection == "desc"
	offset := (page - 1) * pageSize

	var rows []store.ProductsProduct
	switch {
	case req.Params.SortBy != nil && *req.Params.SortBy == "name":
		rows, err = q.ListProductsByName(ctx, store.ListProductsByNameParams{
			Status: status, CategoryIds: categoryIDs, Uncategorized: uncategorized, SearchPattern: searchPattern, SearchExact: searchExact,
			Descending: descending, PageSize: pageSize, RowOffset: offset,
		})
	case req.Params.SortBy != nil && *req.Params.SortBy == "sku":
		rows, err = q.ListProductsBySku(ctx, store.ListProductsBySkuParams{
			Status: status, CategoryIds: categoryIDs, Uncategorized: uncategorized, SearchPattern: searchPattern, SearchExact: searchExact,
			Descending: descending, PageSize: pageSize, RowOffset: offset,
		})
	default:
		rows, err = q.ListProductsByID(ctx, store.ListProductsByIDParams{
			Status: status, CategoryIds: categoryIDs, Uncategorized: uncategorized, SearchPattern: searchPattern, SearchExact: searchExact,
			Descending: descending, PageSize: pageSize, RowOffset: offset,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("products: list products: %w", err)
	}

	data, err := s.buildProductResponses(ctx, q, rows, s.deps.Clock())
	if err != nil {
		return nil, err
	}

	return gen.GetProducts200JSONResponse{
		Data:       data,
		Pagination: paginationMetadata(page, pageSize, int32(total)),
	}, nil
}

// PostProducts Create a new product
// (POST /api/v1/products)
//
// Ordering follows CreateProductEndpoint.cs:24-101 (products inventory
// §1.3): (1) the conditional pricing-view+pricing-manage gate, when any
// variant carries pricing data — this task's dispatch corrections make it
// TWO permissions, not one, and the router cannot see it at all since it
// depends on the request body (server.go's hasPermission doc comment); (2)
// field validation, every variant's own fields and its prices' pairwise
// Conflicts, all collected together; (3) duplicate SKU (request-internal,
// then catalog-wide) -> 409; (4) duplicate barcode (request-internal, then
// catalog-wide) -> 409; (5) categoryId existence -> 400 field error; (6)
// taxCategoryId existence -> 400 field error; (7) insert -> 201.
//
// CreateProductEndpoint.cs:24-28 also re-checks products:variants-view
// before the pricing gate — provably redundant, since postProducts's own
// x-vantigo-access already requires variants-view, so module.Router would
// never let a caller lacking it reach this handler at all. Porting it as a
// second live Access.Check would violate the Global Constraint that this
// module's handlers make exactly one such check (the pricing gate); since
// the .NET check can never actually deny anyone the router would not have
// already denied, omitting it changes no observable behavior.
func (s *server) PostProducts(ctx context.Context, req gen.PostProductsRequestObject) (gen.PostProductsResponseObject, error) {
	body := gen.ProductRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	var variantBodies []gen.VariantRequest
	if body.Variants != nil {
		variantBodies = *body.Variants
	}
	if containsPricingData(variantBodies) && (!s.hasPermission(ctx, pricingView) || !s.hasPermission(ctx, pricingManage)) {
		return gen.PostProducts403JSONResponse(forbiddenBody()), nil
	}

	parsed, errs := validateProductRequest(body, true, true)
	if len(errs) > 0 {
		return gen.PostProducts400ApplicationProblemPlusJSONResponse(validationProblem("Invalid product", errs)), nil
	}

	q := store.New(s.deps.Pool)

	skus := make([]string, len(parsed.Variants))
	for i, v := range parsed.Variants {
		skus[i] = v.Sku
	}
	duplicateSku := firstRepeatedInOrder(skus)
	dbSkuConflict := false
	if duplicateSku == "" {
		var err error
		dbSkuConflict, err = q.VariantAnySkuExists(ctx, skus)
		if err != nil {
			return nil, fmt.Errorf("products: check sku conflict: %w", err)
		}
	}
	if duplicateSku != "" || dbSkuConflict {
		// CreateProductEndpoint.cs:53: the quoted SKU is duplicateSku when
		// there was a request-internal duplicate, else — even when the real
		// conflict is with a *different* variant of the batch — always
		// variants[0].Sku. Ported faithfully, oddity included.
		quoted := duplicateSku
		if quoted == "" {
			quoted = skus[0]
		}
		return gen.PostProducts409ApplicationProblemPlusJSONResponse(problemStatus(
			"Duplicate SKU", fmt.Sprintf("A variant with SKU '%s' already exists.", quoted), http.StatusConflict)), nil
	}

	var barcodes []string
	seen := map[string]bool{}
	duplicateBarcode := false
	for _, v := range parsed.Variants {
		if v.Barcode == nil {
			continue
		}
		if seen[*v.Barcode] {
			duplicateBarcode = true
		}
		seen[*v.Barcode] = true
		barcodes = append(barcodes, *v.Barcode)
	}
	dbBarcodeConflict := false
	if !duplicateBarcode && len(barcodes) > 0 {
		var err error
		dbBarcodeConflict, err = q.VariantAnyBarcodeExists(ctx, barcodes)
		if err != nil {
			return nil, fmt.Errorf("products: check barcode conflict: %w", err)
		}
	}
	if duplicateBarcode || dbBarcodeConflict {
		return gen.PostProducts409ApplicationProblemPlusJSONResponse(problemStatus(
			"Duplicate barcode", "A variant with that barcode already exists.", http.StatusConflict)), nil
	}

	var categoryRefResp *gen.ProductCategoryResponse
	if parsed.CategoryID != nil {
		cat, err := q.GetCategoryRef(ctx, *parsed.CategoryID)
		if errors.Is(err, pgx.ErrNoRows) {
			return gen.PostProducts400ApplicationProblemPlusJSONResponse(validationProblem("Invalid product",
				map[string][]string{"categoryId": {fmt.Sprintf("Category %d does not exist.", *parsed.CategoryID)}})), nil
		}
		if err != nil {
			return nil, fmt.Errorf("products: get category: %w", err)
		}
		ref := categoryRef(cat.ID, cat.Name)
		categoryRefResp = &ref
	}

	taxCat, err := q.GetTaxCategoryRef(ctx, parsed.TaxCategoryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostProducts400ApplicationProblemPlusJSONResponse(validationProblem("Invalid product",
			map[string][]string{"taxCategoryId": {fmt.Sprintf("Tax category %d does not exist.", parsed.TaxCategoryID)}})), nil
	}
	if err != nil {
		return nil, fmt.Errorf("products: get tax category: %w", err)
	}

	status := "Draft"
	if parsed.Status != nil {
		status = *parsed.Status
	}

	now := s.deps.Clock()
	var created store.ProductsProduct
	variantRows := make([]store.ProductsProductVariant, 0, len(parsed.Variants))
	priceRows := map[int32][]store.ProductsProductPrice{}
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var err error
		created, err = txq.InsertProduct(ctx, store.InsertProductParams{
			Name: parsed.Name, Description: parsed.Description, CategoryID: parsed.CategoryID,
			Type: parsed.Type, Status: status, TaxCategoryID: parsed.TaxCategoryID, Now: now,
		})
		if err != nil {
			return err
		}

		for _, v := range parsed.Variants {
			nums, nerr := numericsFromVariant(v)
			if nerr != nil {
				return nerr
			}
			variant, err := txq.InsertProductVariant(ctx, store.InsertProductVariantParams{
				ProductID: created.ID, Sku: v.Sku, Barcode: v.Barcode, Unit: v.Unit,
				StandardCost: nums.StandardCost, WeightKg: nums.WeightKg,
				LengthCm: nums.LengthCm, WidthCm: nums.WidthCm, HeightCm: nums.HeightCm,
				OptionValues: marshalOptionValues(v.OptionValues), Now: now,
			})
			if err != nil {
				return err
			}
			variantRows = append(variantRows, variant)

			for _, price := range v.Prices {
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
				priceRows[variant.ID] = append(priceRows[variant.ID], row)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("products: create product: %w", err)
	}

	variantResponses := make([]gen.ProductVariantResponse, 0, len(variantRows))
	for _, v := range variantRows {
		variantResponses = append(variantResponses, variantResponse(v, priceRows[v.ID], now))
	}

	location := fmt.Sprintf("%s/api/v1/products/%d", s.deps.Config.BasePath, created.ID)
	return gen.PostProducts201JSONResponse{
		Body:    productResponse(created, categoryRefResp, taxCategoryRef(taxCat.ID, taxCat.Name, taxCat.Kind, taxCat.Rate), variantResponses),
		Headers: gen.PostProducts201ResponseHeaders{Location: &location},
	}, nil
}

// GetProduct Get a product by id
// (GET /api/v1/products/{id})
func (s *server) GetProduct(ctx context.Context, req gen.GetProductRequestObject) (gen.GetProductResponseObject, error) {
	q := store.New(s.deps.Pool)
	p, err := q.GetProductByID(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetProduct404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("products: get product: %w", err)
	}
	data, err := s.buildProductResponses(ctx, q, []store.ProductsProduct{p}, s.deps.Clock())
	if err != nil {
		return nil, err
	}
	return gen.GetProduct200JSONResponse(data[0]), nil
}

// PutProductsById Update a product
// (PUT /api/v1/products/{id})
//
// UpdateProductEndpoint.cs:19-66 (products inventory §7 oddity 2):
// Validate(requireVariants:false, validateVariants:false) — request.Variants
// is neither validated nor ever read; a client PUTting a full product with
// changed variants gets a 200 with the variants completely unchanged. Order:
// field validation -> product exists (404) -> categoryId existence (400) ->
// taxCategoryId existence (400) -> apply -> save.
func (s *server) PutProductsById(ctx context.Context, req gen.PutProductsByIdRequestObject) (gen.PutProductsByIdResponseObject, error) {
	body := gen.ProductRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	parsed, errs := validateProductRequest(body, false, false)
	if len(errs) > 0 {
		return gen.PutProductsById400ApplicationProblemPlusJSONResponse(validationProblem("Invalid product", errs)), nil
	}

	q := store.New(s.deps.Pool)
	existing, err := q.GetProductByID(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutProductsById404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("products: get product: %w", err)
	}

	if parsed.CategoryID != nil {
		if _, err := q.GetCategoryRef(ctx, *parsed.CategoryID); errors.Is(err, pgx.ErrNoRows) {
			return gen.PutProductsById400ApplicationProblemPlusJSONResponse(validationProblem("Invalid product",
				map[string][]string{"categoryId": {fmt.Sprintf("Category %d does not exist.", *parsed.CategoryID)}})), nil
		} else if err != nil {
			return nil, fmt.Errorf("products: get category: %w", err)
		}
	}

	if _, err := q.GetTaxCategoryRef(ctx, parsed.TaxCategoryID); errors.Is(err, pgx.ErrNoRows) {
		return gen.PutProductsById400ApplicationProblemPlusJSONResponse(validationProblem("Invalid product",
			map[string][]string{"taxCategoryId": {fmt.Sprintf("Tax category %d does not exist.", parsed.TaxCategoryID)}})), nil
	} else if err != nil {
		return nil, fmt.Errorf("products: get tax category: %w", err)
	}

	status := existing.Status
	if parsed.Status != nil {
		status = *parsed.Status
	}

	// updated_at moves only when a field actually changed, matching EF's
	// change-tracker semantics (StampTimestamps only stamps an entity in
	// EntityState.Modified) and internal/customers/customers.go:432-436's
	// identical "changed" convention for the same reason: an unexplained
	// divergence between two modules in one port is worse than either
	// behaviour alone.
	changed := existing.Name != parsed.Name ||
		!stringPtrEqual(existing.Description, parsed.Description) ||
		!int32PtrEqual(existing.CategoryID, parsed.CategoryID) ||
		existing.Type != parsed.Type ||
		existing.TaxCategoryID != parsed.TaxCategoryID ||
		status != existing.Status
	updatedAt := existing.UpdatedAt
	if changed {
		updatedAt = s.deps.Clock()
	}

	updated, err := q.UpdateProduct(ctx, store.UpdateProductParams{
		Name: parsed.Name, Description: parsed.Description, CategoryID: parsed.CategoryID,
		Type: parsed.Type, Status: status, TaxCategoryID: parsed.TaxCategoryID, UpdatedAt: updatedAt, ID: req.Id,
	})
	if err != nil {
		return nil, fmt.Errorf("products: update product: %w", err)
	}

	data, err := s.buildProductResponses(ctx, q, []store.ProductsProduct{updated}, s.deps.Clock())
	if err != nil {
		return nil, err
	}
	return gen.PutProductsById200JSONResponse(data[0]), nil
}

// DeleteProductsById Archive a product
// (DELETE /api/v1/products/{id})
//
// Archives, never deletes (ArchiveProductEndpoint.cs:28-32): idempotent —
// a product already Discontinued is left untouched.
func (s *server) DeleteProductsById(ctx context.Context, req gen.DeleteProductsByIdRequestObject) (gen.DeleteProductsByIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	existing, err := q.GetProductByID(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.DeleteProductsById404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("products: get product: %w", err)
	}
	if existing.Status == "Discontinued" {
		return gen.DeleteProductsById204Response{}, nil
	}

	if _, err := q.ArchiveProduct(ctx, store.ArchiveProductParams{Now: s.deps.Clock(), ID: req.Id}); err != nil {
		return nil, fmt.Errorf("products: archive product: %w", err)
	}
	return gen.DeleteProductsById204Response{}, nil
}
