package purchasing

// The price-list import (P14, decision 20): a CSV of one supplier's items,
// read by internal/fileimport — UTF-8 or Windows-1252, the separator ; , or
// tab detected — synchronously, as Inventory's opening-balance import is,
// with no job table. The header names a column sku or barcode — a SKU, or a
// variant's or a packaging's barcode, which also sets the packaging — and
// may name supplier_item_number, supplier_item_name, price, packaging (a
// packaging's name), lead_time_days, minimum_quantity and multiple, in any
// order and case; at most 5000 rows. Every row is resolved before any
// transaction. A dry run — the default — answers each row's outcome:
// created or updated, or why it fails. A commit writes every row or, when
// any fails, none; a row creates the supplier's item of the variant or
// updates it, an empty cell keeping the item's value, its price set by
// import.

import (
	"context"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/fileimport"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/store"
)

const (
	maxImportRows    = 5000
	maxImportBytes   = 4 << 20
	maxImportColumns = 20
)

var importLimits = fileimport.Limits{MaxBytes: maxImportBytes, MaxRows: maxImportRows, MaxColumns: maxImportColumns}

// importColumns are the header's names as fileimport.HeaderKey reads them.
var importColumns = []string{"sku", "barcode", "supplieritemnumber", "supplieritemname", "price", "packaging", "leadtimedays", "minimumquantity", "multiple"}

// importProblem is the 400 of a file or a request the import cannot read.
func importProblem(field, message string) gen.ImportPurchasingSupplierItemsResponseObject {
	var f fieldErrors
	f.addCoded(field, codeImportInvalid, message)
	return gen.ImportPurchasingSupplierItems400ApplicationProblemPlusJSONResponse(invalid(invalidImportTitle, f))
}

// importRow is one row resolved: its outcome, and for a row that is ok the
// item's fields as the file gives them.
type importRow struct {
	out       gen.PurchasingImportRow
	variantID int32
	variant   contracts.VariantEntry
	number    *string
	name      *string
	price     *big.Rat
	packaging *int32
	setsPack  bool
	leadTime  *int32
	minimum   *big.Rat
	multiple  *big.Rat
}

func (r importRow) ok() bool {
	return r.out.Outcome == gen.Created || r.out.Outcome == gen.Updated
}

// ImportPurchasingSupplierItems Import a supplier's price list
// (POST /api/v1/purchasing/supplier-items/import)
func (s *server) ImportPurchasingSupplierItems(ctx context.Context, req gen.ImportPurchasingSupplierItemsRequestObject) (gen.ImportPurchasingSupplierItemsResponseObject, error) {
	if req.Body == nil {
		return importProblem("file", fileimport.EmptyMessage), nil
	}
	data, err := io.ReadAll(io.LimitReader(req.Body, maxImportBytes+1))
	if err != nil {
		return importProblem("file", fmt.Sprintf(fileimport.UnreadableMessage, err)), nil
	}
	if len(data) > maxImportBytes {
		return importProblem("file", fmt.Sprintf("The file is larger than %d MB, which is more than one import takes", maxImportBytes>>20)), nil
	}
	file, refusal := fileimport.Read(data, importLimits)
	if refusal != "" {
		return importProblem("file", refusal), nil
	}
	column := map[string]int{}
	for i, h := range file.Header {
		key := fileimport.HeaderKey(h)
		if !slices.Contains(importColumns, key) {
			return importProblem("file", fmt.Sprintf("The column %q is not one the import reads: sku or barcode, supplier_item_number, supplier_item_name, price, packaging, lead_time_days, minimum_quantity, multiple", h)), nil
		}
		column[key] = i
	}
	_, hasSKU := column["sku"]
	_, hasBarcode := column["barcode"]
	if !hasSKU && !hasBarcode {
		return importProblem("file", "The header must name a column sku or barcode"), nil
	}
	sup, err := s.supplier(ctx, req.Params.SupplierId)
	if err != nil {
		if status, p, ok := answerOf(err); ok && status == 503 {
			return gen.ImportPurchasingSupplierItems503ApplicationProblemPlusJSONResponse(p), nil
		}
		return nil, err
	}
	if sup == nil {
		var f fieldErrors
		f.addCoded("supplierId", codeSupplierNotFound, "No supplier has this id")
		return gen.ImportPurchasingSupplierItems400ApplicationProblemPlusJSONResponse(invalid(invalidImportTitle, f)), nil
	}
	rows, err := s.resolveImport(ctx, sup.ID, file, column)
	if err != nil {
		if status, p, ok := answerOf(err); ok && status == 503 {
			return gen.ImportPurchasingSupplierItems503ApplicationProblemPlusJSONResponse(p), nil
		}
		return nil, err
	}
	result := gen.PurchasingImportResult{Ok: true, Rows: make([]gen.PurchasingImportRow, 0, len(rows))}
	for _, r := range rows {
		result.Rows = append(result.Rows, r.out)
		if !r.ok() {
			result.Ok = false
		}
	}
	if (req.Params.DryRun == nil || *req.Params.DryRun) || !result.Ok {
		return gen.ImportPurchasingSupplierItems200JSONResponse(result), nil
	}
	today, err := s.today(ctx)
	if err != nil {
		return nil, err
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var created, updated int32
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		var ids []int32
		for _, r := range rows {
			ids = append(ids, r.variantID)
		}
		locked, err := s.lockVariants(ctx, tx, ids)
		if err != nil {
			return err
		}
		held, err := s.lockSuppliers(ctx, tx, sup.ID)
		if err != nil {
			return err
		}
		entry, ok := held[sup.ID]
		if !ok {
			return refuseField(invalidImportTitle, "supplierId", codeSupplierNotFound, "No supplier has this id")
		}
		for _, id := range distinctSorted(ids) {
			if err := txq.LockVariantItems(ctx, id); err != nil {
				return fmt.Errorf("purchasing: lock variant %d's items: %w", id, err)
			}
		}
		existing, err := itemsOf(ctx, txq, sup.ID, ids)
		if err != nil {
			return err
		}
		now := s.deps.Clock()
		for i, r := range rows {
			if _, ok := locked[r.variantID]; !ok {
				return refuseField(invalidImportTitle, "file", codeImportInvalid, fmt.Sprintf("Row %d's variant is gone", r.out.Row))
			}
			it, has := existing[r.variantID]
			if !has {
				fields := itemFields{number: r.number, name: r.name, price: r.price, packagingID: r.packaging, leadTime: r.leadTime,
					minimum: r.minimum, mult: r.multiple}
				if _, err := insertItem(ctx, txq, sup.ID, r.variantID, entry, fields, priceByImport, by, today, now); err != nil {
					return err
				}
				rows[i].out.Outcome = gen.Created
				created++
				continue
			}
			params := store.UpdateSupplierItemParams{
				ID: it.ID, SupplierItemNumber: orKeep(r.number, it.SupplierItemNumber), SupplierItemName: orKeep(r.name, it.SupplierItemName),
				Price: it.Price, Currency: it.Currency, PriceSetOn: it.PriceSetOn, PriceSetBy: it.PriceSetBy,
				PriceSetByUserID: it.PriceSetByUserID, PackagingID: it.PackagingID, LeadTimeDays: it.LeadTimeDays,
				MinimumQuantity: it.MinimumQuantity, OrderMultiple: it.OrderMultiple, Preferred: it.Preferred, Now: now,
			}
			if r.price != nil {
				params.Price, params.Currency = numeric(r.price, pricePlaces), supplierCurrency(entry)
				params.PriceSetOn, params.PriceSetBy, params.PriceSetByUserID = dayCol(today), ptr(priceByImport), nil
			}
			if r.setsPack {
				params.PackagingID = r.packaging
			}
			if r.leadTime != nil {
				params.LeadTimeDays = r.leadTime
			}
			if r.minimum != nil {
				params.MinimumQuantity = numeric(r.minimum, quantityPlaces)
			}
			if r.multiple != nil {
				params.OrderMultiple = numeric(r.multiple, quantityPlaces)
			}
			if _, err := txq.UpdateSupplierItem(ctx, params); err != nil {
				return fmt.Errorf("purchasing: update supplier item %d: %w", it.ID, err)
			}
			rows[i].out.Outcome = gen.Updated
			updated++
		}
		return nil
	})
	if err != nil {
		if status, p, ok := answerOf(err); ok {
			switch status {
			case 400:
				return gen.ImportPurchasingSupplierItems400ApplicationProblemPlusJSONResponse(p), nil
			case 503:
				return gen.ImportPurchasingSupplierItems503ApplicationProblemPlusJSONResponse(p), nil
			}
		}
		return nil, err
	}
	result.Committed, result.Created, result.Updated = true, created, updated
	result.Rows = result.Rows[:0]
	for _, r := range rows {
		result.Rows = append(result.Rows, r.out)
	}
	return gen.ImportPurchasingSupplierItems200JSONResponse(result), nil
}

// orKeep is v when the file gives it, else what the item has.
func orKeep(v, keep *string) *string {
	if v != nil {
		return v
	}
	return keep
}

// resolveImport answers every row resolved, reading the catalog once for
// the SKUs together and once per barcode, and the supplier's items, before
// any transaction.
func (s *server) resolveImport(ctx context.Context, supplierID int64, file fileimport.File, column map[string]int) ([]importRow, error) {
	cell := func(r fileimport.Record, name string) string {
		if i, ok := column[name]; ok && i < len(r.Cells) {
			return strings.TrimSpace(r.Cells[i])
		}
		return ""
	}
	var skus []string
	for _, r := range file.Rows {
		if sku := cell(r, "sku"); sku != "" {
			skus = append(skus, sku)
		}
	}
	bySKU, err := s.lookupSKUs(ctx, skus)
	if err != nil {
		return nil, err
	}
	rows := make([]importRow, 0, len(file.Rows))
	var variantIDs []int32
	for _, rec := range file.Rows {
		r := importRow{out: gen.PurchasingImportRow{Row: int32(rec.Row + 1)}}
		fail := func(outcome gen.PurchasingImportOutcome, detail string) {
			r.out.Outcome, r.out.Detail = outcome, ptr(detail)
		}
		sku, barcode := cell(rec, "sku"), cell(rec, "barcode")
		var matches []contracts.CodeMatch
		switch {
		case sku != "":
			r.out.Code = sku
			for _, v := range bySKU[sku] {
				matches = append(matches, contracts.CodeMatch{Variant: v})
			}
		case barcode != "":
			r.out.Code = barcode
			if matches, err = s.lookupCode(ctx, barcode); err != nil {
				return nil, err
			}
		default:
			fail(gen.LineInvalid, "The row names neither a SKU nor a barcode")
			rows = append(rows, r)
			continue
		}
		switch len(matches) {
		case 0:
			fail(gen.VariantNotFound, "No variant has this code")
			rows = append(rows, r)
			continue
		case 1:
		default:
			fail(gen.VariantAmbiguous, fmt.Sprintf("%d variants have this code", len(matches)))
			rows = append(rows, r)
			continue
		}
		m := matches[0]
		r.variant, r.variantID = m.Variant, m.Variant.ID
		r.out.VariantId = ptr(m.Variant.ID)
		if m.Packaging != nil {
			r.packaging, r.setsPack = &m.Packaging.ID, true
		}
		if code := r.read(rec, cell); code != "" {
			rows = append(rows, r)
			continue
		}
		variantIDs = append(variantIDs, r.variantID)
		rows = append(rows, r)
	}
	existing, err := itemsOf(ctx, s.store(), supplierID, variantIDs)
	if err != nil {
		return nil, err
	}
	seen := map[int32]int32{}
	for i := range rows {
		r := &rows[i]
		if r.out.Outcome != "" {
			continue
		}
		if first, dup := seen[r.variantID]; dup {
			r.out.Outcome, r.out.Detail = gen.DuplicateRow, ptr(fmt.Sprintf("Row %d names the same variant", first))
			continue
		}
		seen[r.variantID] = r.out.Row
		r.out.Outcome = gen.Created
		if _, ok := existing[r.variantID]; ok {
			r.out.Outcome = gen.Updated
		}
	}
	return rows, nil
}

// read reads a matched row's optional cells, answering the outcome of the
// first that fails, "" when every one reads.
func (r *importRow) read(rec fileimport.Record, cell func(fileimport.Record, string) string) gen.PurchasingImportOutcome {
	fail := func(outcome gen.PurchasingImportOutcome, detail string) gen.PurchasingImportOutcome {
		r.out.Outcome, r.out.Detail = outcome, ptr(detail)
		return outcome
	}
	r.number, r.name = textPtr(cell(rec, "supplieritemnumber")), textPtr(cell(rec, "supplieritemname"))
	if r.number != nil && len([]rune(*r.number)) > 60 {
		return fail(gen.LineInvalid, "The supplier's item number is at most 60 characters")
	}
	if r.name != nil && len([]rune(*r.name)) > 255 {
		return fail(gen.LineInvalid, "The supplier's name for the item is at most 255 characters")
	}
	if p := cell(rec, "price"); p != "" {
		price, ok := parseDecimal(importDecimal(p), pricePlaces, maxPrice)
		if !ok {
			return fail(gen.PriceInvalid, fmt.Sprintf("The price %q is not a number such as 12.50 with at most four decimals", p))
		}
		r.price = price
	}
	if name := cell(rec, "packaging"); name != "" {
		r.packaging, r.setsPack = nil, true
		for _, pk := range r.variant.Packagings {
			if strings.EqualFold(pk.Name, name) {
				r.packaging = ptr(pk.ID)
			}
		}
		if r.packaging == nil {
			return fail(gen.PackagingNotFound, fmt.Sprintf("%s has no packaging %q", r.variant.SKU, name))
		}
	}
	if lt := cell(rec, "leadtimedays"); lt != "" {
		n, err := strconv.Atoi(lt)
		if err != nil || n < 0 || n > 3650 {
			return fail(gen.LeadTimeInvalid, "A lead time is 0 to 3650 whole days")
		}
		r.leadTime = ptr(int32(n))
	}
	for _, q := range []struct {
		name string
		into **big.Rat
	}{{"minimumquantity", &r.minimum}, {"multiple", &r.multiple}} {
		if text := cell(rec, q.name); text != "" {
			v, ok := readQuantity(strings.Replace(text, ",", ".", 1))
			if !ok {
				return fail(gen.QuantityInvalid, fmt.Sprintf("%q is not a quantity more than 0 with at most three decimals", text))
			}
			*q.into = v
		}
	}
	return ""
}

// Norwegian number forms an import's price may take: digits grouped by
// three with a space (a non-breaking or a narrow one too), or with a point,
// and a decimal comma.
var (
	groupedBySpace = regexp.MustCompile("^[0-9]{1,3}([ \u00a0\u202f][0-9]{3})+([.,][0-9]+)?$")
	groupedByPoint = regexp.MustCompile(`^[0-9]{1,3}(\.[0-9]{3})+,[0-9]+$`)
)

// importDecimal is a price cell as parseDecimal reads it: "1 234,50" and
// "1.234,50" are 1234.50, a lone decimal comma a point. A form that could be
// read two ways — "1.234" — is left as it is, a decimal point.
func importDecimal(cell string) string {
	t := strings.TrimSpace(cell)
	switch {
	case groupedBySpace.MatchString(t):
		t = strings.NewReplacer(" ", "", "\u00a0", "", "\u202f", "").Replace(t)
	case groupedByPoint.MatchString(t):
		t = strings.ReplaceAll(t, ".", "")
	}
	if strings.Count(t, ",") == 1 && !strings.Contains(t, ".") {
		t = strings.Replace(t, ",", ".", 1)
	}
	return t
}
