package products

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/products/gen"
	"github.com/vantigo-io/vantigo/server/internal/products/store"
)

// This file is the Packagings sub-resource of a variant (inventory design
// I24): getProductsByIdVariantsByVariantIdPackagings,
// postProductsByIdVariantsByVariantIdPackagings,
// putProductsByIdVariantsByVariantIdPackagingsByPackagingId and
// deleteProductsByIdVariantsByVariantIdPackagingsByPackagingId.
//
// A packaging holds either a quantity of the variant's unit, or a count of
// another packaging of the same variant, whose quantity it then derives:
// a Euro pallet of 40 boxes of 15 holds 600. The quantity is an exact
// decimal (numeric(14,3)), computed as a big.Rat and never in floating
// point, the way Inventory's and Invoices' quantities are.
//
// The lock order of every write, one transaction each:
//  1. the variant's row FOR UPDATE (LockVariantOfProduct) — so a variant's
//     packagings change one writer at a time, and never while a posting
//     holds the variant FOR SHARE through ProductCatalog.LockVariants,
//     which therefore reads stable packagings;
//  2. when the write carries a barcode, the advisory lock products:barcodes
//     (LockBarcodes), under which the barcode is checked against every
//     variant and every packaging and then written;
//  3. the variant's packaging rows.
//
// The variant writes (variants.go, products.go) take the same advisory
// lock after their own variant row, never before it, so the two orders
// agree.

const (
	packagingNameMaxLength  = 40
	maxPackagingsPerVariant = 10
	packagingQuantityPlaces = 3
)

// packagingKinds are the kinds a packaging may be, as stored.
var packagingKinds = []string{"inner", "case", "pallet", "other"}

// maxPackagingQuantity is the largest quantity numeric(14,3) holds.
var maxPackagingQuantity, _ = new(big.Rat).SetString("99999999999.999")

// parsedPackaging is a validated ProductPackagingRequest. Exactly one of
// Quantity and ContainsPackagingID is set; ContainsCount goes with the
// latter.
type parsedPackaging struct {
	Name                string
	Kind                string
	Quantity            *big.Rat
	ContainsPackagingID *int32
	ContainsCount       *int32
	Barcode             *string
	WeightKg            *float64
	LengthCm            *float64
	WidthCm             *float64
	HeightCm            *float64
	SortOrder           *int32
}

// validatePackagingRequest checks every field that needs nothing but the
// request, writing field errors into errs in this module's style.
func validatePackagingRequest(body gen.ProductPackagingRequest, errs map[string][]string) parsedPackaging {
	out := parsedPackaging{
		ContainsPackagingID: body.ContainsPackagingId, ContainsCount: body.ContainsCount,
		WeightKg: body.WeightKg, LengthCm: body.LengthCm, WidthCm: body.WidthCm, HeightCm: body.HeightCm,
		SortOrder: body.SortOrder,
	}

	out.Name = strings.TrimSpace(body.Name)
	switch {
	case out.Name == "":
		errs["name"] = []string{"'name' is required."}
	case utf8.RuneCountInString(out.Name) > packagingNameMaxLength:
		errs["name"] = []string{fmt.Sprintf("'name' must be at most %d characters.", packagingNameMaxLength)}
	}

	out.Kind = strings.ToLower(strings.TrimSpace(body.Kind))
	known := false
	for _, k := range packagingKinds {
		known = known || k == out.Kind
	}
	if !known {
		errs["kind"] = []string{fmt.Sprintf("'kind' must be one of 'inner', 'case', 'pallet' or 'other', but was '%s'.", body.Kind)}
	}

	contains := body.ContainsPackagingId != nil || body.ContainsCount != nil
	switch {
	case body.Quantity != nil && contains:
		errs["quantity"] = []string{"Give either 'quantity' or 'containsPackagingId' with 'containsCount', not both."}
	case body.Quantity != nil:
		q, msg := parsePackagingQuantity(*body.Quantity)
		if msg != "" {
			errs["quantity"] = []string{msg}
		}
		out.Quantity = q
	case contains:
		if body.ContainsPackagingId == nil {
			errs["containsPackagingId"] = []string{"'containsPackagingId' is required with 'containsCount'."}
		}
		if body.ContainsCount == nil {
			errs["containsCount"] = []string{"'containsCount' is required with 'containsPackagingId'."}
		} else if *body.ContainsCount < 2 {
			errs["containsCount"] = []string{fmt.Sprintf("'containsCount' must be 2 or greater, but was %d.", *body.ContainsCount)}
		}
	default:
		errs["quantity"] = []string{"Give either 'quantity' or 'containsPackagingId' with 'containsCount'."}
	}

	barcode, barcodeErr := validateBarcode(body.Barcode)
	if barcodeErr != "" {
		errs["barcode"] = []string{barcodeErr}
	}
	out.Barcode = barcode

	for _, f := range []struct {
		name string
		v    *float64
	}{{"weightKg", body.WeightKg}, {"lengthCm", body.LengthCm}, {"widthCm", body.WidthCm}, {"heightCm", body.HeightCm}} {
		if e := validateNonNegative(f.name, f.v); e != "" {
			errs[f.name] = []string{e}
		}
	}
	return out
}

// parsePackagingQuantity is a quantity as the exact decimal it was written
// as — its shortest round-tripping text read by big.Rat, never SetFloat64 —
// or why it is not one: more than 0, at most three decimals (refused, never
// rounded), within the column.
func parsePackagingQuantity(v float64) (*big.Rat, string) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil, "'quantity' must be a number."
	}
	text := strconv.FormatFloat(v, 'f', -1, 64)
	if _, decimals, ok := strings.Cut(text, "."); ok && len(decimals) > packagingQuantityPlaces {
		return nil, "'quantity' must have at most three decimals."
	}
	r, ok := new(big.Rat).SetString(text)
	if !ok {
		return nil, "'quantity' must be a number."
	}
	if r.Sign() <= 0 {
		return nil, "'quantity' must be more than 0."
	}
	if r.Cmp(maxPackagingQuantity) > 0 {
		return nil, "'quantity' must be at most 99999999999.999."
	}
	return r, ""
}

// ratFromNumeric is a numeric column's value as an exact decimal; NULL is 0.
// Inventory's quantity.go has its twin; no module imports another.
func ratFromNumeric(n pgtype.Numeric) *big.Rat {
	if !n.Valid || n.Int == nil {
		return new(big.Rat)
	}
	r := new(big.Rat).SetInt(n.Int)
	if n.Exp == 0 {
		return r
	}
	exp := int64(n.Exp)
	if exp < 0 {
		exp = -exp
	}
	scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(exp), nil))
	if n.Exp > 0 {
		return r.Mul(r, scale)
	}
	return r.Quo(r, scale)
}

// numericFromRat stores an exact decimal of at most three places.
func numericFromRat(v *big.Rat) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	text := v.FloatString(packagingQuantityPlaces)
	if err := n.Scan(text); err != nil {
		return pgtype.Numeric{}, fmt.Errorf("products: %s is not a storable quantity: %w", text, err)
	}
	return n, nil
}

// quantityText is a stored quantity as the contract's decimal text, three
// places.
func quantityText(n pgtype.Numeric) string {
	return ratFromNumeric(n).FloatString(packagingQuantityPlaces)
}

// packagingResponse is a packaging row on the wire.
func packagingResponse(p store.ProductsVariantPackaging) gen.ProductPackagingResponse {
	return gen.ProductPackagingResponse{
		Id: p.ID, Name: p.Name, Kind: p.Kind,
		ContainsPackagingId: p.ContainsPackagingID, ContainsCount: p.ContainsCount,
		Quantity: floatFromNumeric(p.Quantity), Barcode: p.Barcode,
		WeightKg: floatPtrFromNumeric(p.WeightKg), LengthCm: floatPtrFromNumeric(p.LengthCm),
		WidthCm: floatPtrFromNumeric(p.WidthCm), HeightCm: floatPtrFromNumeric(p.HeightCm),
		SortOrder: p.SortOrder, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

// packagingsByVariant batch-loads the packagings of variantIDs in one query,
// grouped by variant, each group by sort order then id.
func packagingsByVariant(ctx context.Context, q *store.Queries, variantIDs []int32) (map[int32][]store.ProductsVariantPackaging, error) {
	out := map[int32][]store.ProductsVariantPackaging{}
	if len(variantIDs) == 0 {
		return out, nil
	}
	rows, err := q.ListPackagingsByVariantIDs(ctx, variantIDs)
	if err != nil {
		return nil, fmt.Errorf("products: list packagings: %w", err)
	}
	for _, r := range rows {
		out[r.VariantID] = append(out[r.VariantID], r)
	}
	return out, nil
}

// barcodeHolder answers which kind of row other than the one being written
// already carries barcode — "variant", "packaging" or "" — under the
// products:barcodes advisory lock it takes first, so the answer holds until
// the caller's transaction ends. exceptVariant and exceptPackaging are the
// row being written (0 for none).
func barcodeHolder(ctx context.Context, txq *store.Queries, barcode string, exceptVariant, exceptPackaging int32) (string, error) {
	if err := txq.LockBarcodes(ctx); err != nil {
		return "", fmt.Errorf("lock barcodes: %w", err)
	}
	taken, err := txq.VariantBarcodeExistsExcluding(ctx, store.VariantBarcodeExistsExcludingParams{Barcode: &barcode, ID: exceptVariant})
	if err != nil {
		return "", fmt.Errorf("check variant barcode: %w", err)
	}
	if taken {
		return "variant", nil
	}
	taken, err = txq.PackagingBarcodeExists(ctx, store.PackagingBarcodeExistsParams{Barcode: barcode, ExcludeID: exceptPackaging})
	if err != nil {
		return "", fmt.Errorf("check packaging barcode: %w", err)
	}
	if taken {
		return "packaging", nil
	}
	return "", nil
}

// packagingRefusal is a refusal a packaging write answers instead of
// writing: a 400 with field errors or a titled 409.
type packagingRefusal struct {
	errs     map[string][]string
	conflict *apicommon.ProblemDetails
}

func invalidPackaging(field, msg string) *packagingRefusal {
	return &packagingRefusal{errs: map[string][]string{field: {msg}}}
}

func packagingConflict(title, detail string) *packagingRefusal {
	p := apicommon.ProblemStatus(title, detail, http.StatusConflict)
	return &packagingRefusal{conflict: &p}
}

// packagingWrite is one create or replace, run by writePackaging.
type packagingWrite struct {
	productID, variantID int32
	packagingID          int32 // 0 on a create
	in                   parsedPackaging
}

// errPackagingNotFound is a packaging write's 404: the variant is not under
// the product, or the packaging not under the variant.
var errPackagingNotFound = errors.New("products: packaging not found")

// errPackagingRefused rolls a packaging write's transaction back when it
// answers a refusal instead, so nothing it wrote before refusing — a
// replaced packaging whose dependant would overflow — survives.
var errPackagingRefused = errors.New("products: packaging write refused")

// writePackaging creates or replaces a packaging in one transaction, in the
// lock order this file's comment gives, recomputing every packaging built
// from it. It answers the row written, or a refusal, or
// errPackagingNotFound.
func (s *server) writePackaging(ctx context.Context, w packagingWrite) (store.ProductsVariantPackaging, *packagingRefusal, error) {
	var written store.ProductsVariantPackaging
	var refused *packagingRefusal
	now := s.deps.Clock()
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if _, err := txq.LockVariantOfProduct(ctx, store.LockVariantOfProductParams{ID: w.variantID, ProductID: w.productID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errPackagingNotFound
			}
			return fmt.Errorf("lock variant: %w", err)
		}
		rows, err := txq.ListPackagingsByVariant(ctx, w.variantID)
		if err != nil {
			return fmt.Errorf("list packagings: %w", err)
		}
		byID := make(map[int32]store.ProductsVariantPackaging, len(rows))
		for _, r := range rows {
			byID[r.ID] = r
		}

		var current store.ProductsVariantPackaging
		if w.packagingID != 0 {
			var ok bool
			if current, ok = byID[w.packagingID]; !ok {
				return errPackagingNotFound
			}
		} else if len(rows) >= maxPackagingsPerVariant {
			refused = invalidPackaging("packagings", fmt.Sprintf("A variant has at most %d packagings.", maxPackagingsPerVariant))
			return errPackagingRefused
		}

		quantity := w.in.Quantity
		if w.in.ContainsPackagingID != nil {
			contained, ok := byID[*w.in.ContainsPackagingID]
			if !ok {
				refused = invalidPackaging("containsPackagingId", fmt.Sprintf("Packaging %d is not a packaging of this variant.", *w.in.ContainsPackagingID))
				return errPackagingRefused
			}
			if w.packagingID != 0 && buildsOn(byID, contained, w.packagingID) {
				detail := fmt.Sprintf("'%s' cannot hold '%s', which is built from it.", current.Name, contained.Name)
				if contained.ID == w.packagingID {
					detail = fmt.Sprintf("'%s' cannot hold itself.", current.Name)
				}
				refused = packagingConflict("Packaging cycle", detail)
				return errPackagingRefused
			}
			quantity = new(big.Rat).Mul(big.NewRat(int64(*w.in.ContainsCount), 1), ratFromNumeric(contained.Quantity))
			if quantity.Cmp(maxPackagingQuantity) > 0 {
				refused = invalidPackaging("containsCount", "The packaging would hold more than 99999999999.999.")
				return errPackagingRefused
			}
		}

		if w.in.Barcode != nil {
			holder, err := barcodeHolder(ctx, txq, *w.in.Barcode, 0, w.packagingID)
			if err != nil {
				return err
			}
			if holder != "" {
				refused = packagingConflict("Barcode taken", fmt.Sprintf("A %s already carries the barcode '%s'.", holder, *w.in.Barcode))
				return errPackagingRefused
			}
		}

		qty, err := numericFromRat(quantity)
		if err != nil {
			return err
		}
		weight, err := numericFromFloatPtr(w.in.WeightKg)
		if err != nil {
			return err
		}
		length, err := numericFromFloatPtr(w.in.LengthCm)
		if err != nil {
			return err
		}
		width, err := numericFromFloatPtr(w.in.WidthCm)
		if err != nil {
			return err
		}
		height, err := numericFromFloatPtr(w.in.HeightCm)
		if err != nil {
			return err
		}
		var count *int32
		if w.in.ContainsPackagingID != nil {
			count = w.in.ContainsCount
		}

		if w.packagingID == 0 {
			sortOrder := int32(len(rows))
			if w.in.SortOrder != nil {
				sortOrder = *w.in.SortOrder
			}
			written, err = txq.InsertPackaging(ctx, store.InsertPackagingParams{
				VariantID: w.variantID, Name: w.in.Name, Kind: w.in.Kind,
				ContainsPackagingID: w.in.ContainsPackagingID, ContainsCount: count, Quantity: qty, Barcode: w.in.Barcode,
				WeightKg: weight, LengthCm: length, WidthCm: width, HeightCm: height, SortOrder: sortOrder, Now: now,
			})
			if err != nil {
				return fmt.Errorf("insert packaging: %w", err)
			}
			return nil
		}

		sortOrder := current.SortOrder
		if w.in.SortOrder != nil {
			sortOrder = *w.in.SortOrder
		}
		written, err = txq.UpdatePackaging(ctx, store.UpdatePackagingParams{
			ID: w.packagingID, Name: w.in.Name, Kind: w.in.Kind,
			ContainsPackagingID: w.in.ContainsPackagingID, ContainsCount: count, Quantity: qty, Barcode: w.in.Barcode,
			WeightKg: weight, LengthCm: length, WidthCm: width, HeightCm: height, SortOrder: sortOrder, Now: now,
		})
		if err != nil {
			return fmt.Errorf("update packaging: %w", err)
		}
		byID[written.ID] = written
		if refused, err = recomputeDependants(ctx, txq, rows, byID, written.ID, now); err == nil && refused != nil {
			return errPackagingRefused
		}
		return err
	})
	if errors.Is(err, errPackagingRefused) {
		return store.ProductsVariantPackaging{}, refused, nil
	}
	if err != nil {
		return store.ProductsVariantPackaging{}, nil, err
	}
	return written, refused, nil
}

// buildsOn reports whether contained is self, or is built, directly or
// through others, from self — whether self holding contained would close a
// cycle. The variant's packagings form no cycle before the write, so the
// walk up the chain ends.
func buildsOn(byID map[int32]store.ProductsVariantPackaging, contained store.ProductsVariantPackaging, self int32) bool {
	for p, ok := contained, true; ok; {
		if p.ID == self {
			return true
		}
		if p.ContainsPackagingID == nil {
			return false
		}
		p, ok = byID[*p.ContainsPackagingID]
	}
	return false
}

// recomputeDependants re-derives every packaging built, directly or
// through others, from changed, parents before children — each packaging
// is built from at most one other, so a breadth-first walk from changed is
// the dependency order — and writes each whose quantity moved. rows is the
// variant's packagings in their order, byID the same with changed's new
// state. A dependant that would overflow the column refuses the write.
func recomputeDependants(ctx context.Context, txq *store.Queries, rows []store.ProductsVariantPackaging, byID map[int32]store.ProductsVariantPackaging, changed int32, now time.Time) (*packagingRefusal, error) {
	queue := []int32{changed}
	for len(queue) > 0 {
		parent := byID[queue[0]]
		queue = queue[1:]
		for _, r := range rows {
			if r.ContainsPackagingID == nil || *r.ContainsPackagingID != parent.ID || r.ID == changed {
				continue
			}
			child := byID[r.ID]
			derived := new(big.Rat).Mul(big.NewRat(int64(*child.ContainsCount), 1), ratFromNumeric(parent.Quantity))
			if derived.Cmp(maxPackagingQuantity) > 0 {
				return invalidPackaging("quantity", fmt.Sprintf("'%s', built from this packaging, would hold more than 99999999999.999.", child.Name)), nil
			}
			if derived.Cmp(ratFromNumeric(child.Quantity)) != 0 {
				qty, err := numericFromRat(derived)
				if err != nil {
					return nil, err
				}
				if err := txq.SetPackagingQuantity(ctx, store.SetPackagingQuantityParams{ID: child.ID, Quantity: qty, Now: now}); err != nil {
					return nil, fmt.Errorf("recompute packaging %d: %w", child.ID, err)
				}
				child.Quantity = qty
				byID[child.ID] = child
			}
			queue = append(queue, child.ID)
		}
	}
	return nil, nil
}

// GetProductsByIdVariantsByVariantIdPackagings List a variant's packagings
// (GET /api/v1/products/{id}/variants/{variantId}/packagings)
func (s *server) GetProductsByIdVariantsByVariantIdPackagings(ctx context.Context, req gen.GetProductsByIdVariantsByVariantIdPackagingsRequestObject) (gen.GetProductsByIdVariantsByVariantIdPackagingsResponseObject, error) {
	q := store.New(s.deps.Pool)
	exists, err := q.VariantOfProductExists(ctx, store.VariantOfProductExistsParams{ID: req.VariantId, ProductID: req.Id})
	if err != nil {
		return nil, fmt.Errorf("products: check variant exists: %w", err)
	}
	if !exists {
		return gen.GetProductsByIdVariantsByVariantIdPackagings404Response{}, nil
	}
	rows, err := q.ListPackagingsByVariant(ctx, req.VariantId)
	if err != nil {
		return nil, fmt.Errorf("products: list packagings: %w", err)
	}
	out := make(gen.GetProductsByIdVariantsByVariantIdPackagings200JSONResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, packagingResponse(r))
	}
	return out, nil
}

// PostProductsByIdVariantsByVariantIdPackagings Add a packaging to a variant
// (POST /api/v1/products/{id}/variants/{variantId}/packagings)
func (s *server) PostProductsByIdVariantsByVariantIdPackagings(ctx context.Context, req gen.PostProductsByIdVariantsByVariantIdPackagingsRequestObject) (gen.PostProductsByIdVariantsByVariantIdPackagingsResponseObject, error) {
	body := gen.ProductPackagingRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	errs := map[string][]string{}
	parsed := validatePackagingRequest(body, errs)
	if len(errs) > 0 {
		return gen.PostProductsByIdVariantsByVariantIdPackagings400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid packaging", errs)), nil
	}
	written, refused, err := s.writePackaging(ctx, packagingWrite{productID: req.Id, variantID: req.VariantId, in: parsed})
	switch {
	case errors.Is(err, errPackagingNotFound):
		return gen.PostProductsByIdVariantsByVariantIdPackagings404Response{}, nil
	case err != nil:
		return nil, fmt.Errorf("products: add packaging: %w", err)
	case refused != nil && refused.conflict != nil:
		return gen.PostProductsByIdVariantsByVariantIdPackagings409ApplicationProblemPlusJSONResponse(*refused.conflict), nil
	case refused != nil:
		return gen.PostProductsByIdVariantsByVariantIdPackagings400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid packaging", refused.errs)), nil
	}
	return gen.PostProductsByIdVariantsByVariantIdPackagings201JSONResponse(packagingResponse(written)), nil
}

// PutProductsByIdVariantsByVariantIdPackagingsByPackagingId Change a packaging
// (PUT /api/v1/products/{id}/variants/{variantId}/packagings/{packagingId})
//
// A whole replace; an absent sortOrder keeps the packaging's own.
func (s *server) PutProductsByIdVariantsByVariantIdPackagingsByPackagingId(ctx context.Context, req gen.PutProductsByIdVariantsByVariantIdPackagingsByPackagingIdRequestObject) (gen.PutProductsByIdVariantsByVariantIdPackagingsByPackagingIdResponseObject, error) {
	body := gen.ProductPackagingRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	errs := map[string][]string{}
	parsed := validatePackagingRequest(body, errs)
	if len(errs) > 0 {
		return gen.PutProductsByIdVariantsByVariantIdPackagingsByPackagingId400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid packaging", errs)), nil
	}
	written, refused, err := s.writePackaging(ctx, packagingWrite{productID: req.Id, variantID: req.VariantId, packagingID: req.PackagingId, in: parsed})
	switch {
	case errors.Is(err, errPackagingNotFound):
		return gen.PutProductsByIdVariantsByVariantIdPackagingsByPackagingId404Response{}, nil
	case err != nil:
		return nil, fmt.Errorf("products: change packaging: %w", err)
	case refused != nil && refused.conflict != nil:
		return gen.PutProductsByIdVariantsByVariantIdPackagingsByPackagingId409ApplicationProblemPlusJSONResponse(*refused.conflict), nil
	case refused != nil:
		return gen.PutProductsByIdVariantsByVariantIdPackagingsByPackagingId400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid packaging", refused.errs)), nil
	}
	return gen.PutProductsByIdVariantsByVariantIdPackagingsByPackagingId200JSONResponse(packagingResponse(written)), nil
}

// DeleteProductsByIdVariantsByVariantIdPackagingsByPackagingId Remove a
// packaging
// (DELETE /api/v1/products/{id}/variants/{variantId}/packagings/{packagingId})
//
// Refused with 409 "Packaging in use" while another packaging is built from
// it. Stock is never touched: it is always in the variant's unit.
func (s *server) DeleteProductsByIdVariantsByVariantIdPackagingsByPackagingId(ctx context.Context, req gen.DeleteProductsByIdVariantsByVariantIdPackagingsByPackagingIdRequestObject) (gen.DeleteProductsByIdVariantsByVariantIdPackagingsByPackagingIdResponseObject, error) {
	var refused *packagingRefusal
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if _, err := txq.LockVariantOfProduct(ctx, store.LockVariantOfProductParams{ID: req.VariantId, ProductID: req.Id}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errPackagingNotFound
			}
			return fmt.Errorf("lock variant: %w", err)
		}
		rows, err := txq.ListPackagingsByVariant(ctx, req.VariantId)
		if err != nil {
			return fmt.Errorf("list packagings: %w", err)
		}
		var target *store.ProductsVariantPackaging
		for i := range rows {
			if rows[i].ID == req.PackagingId {
				target = &rows[i]
			}
		}
		if target == nil {
			return errPackagingNotFound
		}
		for _, r := range rows {
			if r.ContainsPackagingID != nil && *r.ContainsPackagingID == target.ID {
				refused = packagingConflict("Packaging in use",
					fmt.Sprintf("'%s' is built from '%s'. Change or remove '%s' first.", r.Name, target.Name, r.Name))
				return errPackagingRefused
			}
		}
		if err := txq.DeletePackaging(ctx, target.ID); err != nil {
			return fmt.Errorf("delete packaging: %w", err)
		}
		return nil
	})
	switch {
	case errors.Is(err, errPackagingNotFound):
		return gen.DeleteProductsByIdVariantsByVariantIdPackagingsByPackagingId404Response{}, nil
	case errors.Is(err, errPackagingRefused):
		return gen.DeleteProductsByIdVariantsByVariantIdPackagingsByPackagingId409ApplicationProblemPlusJSONResponse(*refused.conflict), nil
	case err != nil:
		return nil, fmt.Errorf("products: remove packaging: %w", err)
	}
	return gen.DeleteProductsByIdVariantsByVariantIdPackagingsByPackagingId204Response{}, nil
}
