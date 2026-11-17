package purchasing

// Supplier items (P11–P13, decision 20): what one supplier sells of one
// variant — its number and name for it, a price per the purchase packaging
// in the currency the price was set in, the packaging, the lead time, the
// minimum and the multiple — one per supplier and variant. The first item of
// a variant is its preferred one; preferred true on another moves it. Every
// write locks the variant first (rule 21) and, when it makes an item, holds
// the supplier FOR SHARE (rule 28), so neither's delete passes it; the
// variant's items are serialised by an advisory lock while the preference is
// judged.

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/store"
)

// maxListedItems bounds a supplier-item list's read before it is sorted and
// paged.
const maxListedItems = 10000

// Who set a supplier item's price (P11).
const (
	priceByPerson = "person"
	priceByImport = "import"
)

// itemView is a supplier item as the API answers it, with its variant as
// the catalog has it now and its supplier as the register has it now.
func itemView(it store.PurchasingSupplierItem, v *contracts.VariantEntry, sup *contracts.SupplierEntry) gen.PurchasingSupplierItem {
	out := gen.PurchasingSupplierItem{
		Id: it.ID, SupplierId: it.SupplierID, VariantId: it.VariantID, SupplierItemNumber: it.SupplierItemNumber,
		SupplierItemName: it.SupplierItemName, Currency: it.Currency, PriceSetOn: dateOut(it.PriceSetOn),
		PackagingId: it.PackagingID, LeadTimeDays: it.LeadTimeDays, MinimumQuantity: numericText(it.MinimumQuantity, quantityPlaces),
		Multiple: numericText(it.OrderMultiple, quantityPlaces), Preferred: it.Preferred, Revision: it.Revision, UpdatedAt: it.UpdatedAt,
	}
	if p := ratOf(it.Price); p != nil {
		out.Price = ptr(priceText(p))
	}
	if it.PriceSetBy != nil {
		out.PriceSetBy = ptr(gen.PurchasingSupplierItemPriceSetBy(*it.PriceSetBy))
	}
	if v != nil {
		out.ProductId, out.Sku, out.ProductName, out.Unit, out.ProductStatus = v.ProductID, v.SKU, v.ProductName, v.Unit, v.ProductStatus
		if it.PackagingID != nil {
			if pk := packagingOf(*v, *it.PackagingID); pk != nil {
				out.PackagingName, out.PackagingQuantity = ptr(pk.Name), ptr(trimmed(mustRat(pk.Quantity), quantityPlaces))
			} else {
				out.PackagingMissing = true
			}
		}
	}
	if sup != nil {
		out.SupplierName = sup.Name
		out.CurrencyDiffers = sup.Currency != "" && !strings.EqualFold(sup.Currency, it.Currency)
	}
	return out
}

// itemsView is supplier items as the API answers them, reading their
// variants and suppliers before any transaction.
func (s *server) itemsView(ctx context.Context, items []store.PurchasingSupplierItem) ([]gen.PurchasingSupplierItem, error) {
	var variantIDs []int32
	var supplierIDs []int64
	for _, it := range items {
		variantIDs = append(variantIDs, it.VariantID)
		supplierIDs = append(supplierIDs, it.SupplierID)
	}
	variants, err := s.catalogVariants(ctx, variantIDs)
	if err != nil {
		return nil, err
	}
	suppliers, err := s.suppliers(ctx, supplierIDs)
	if err != nil {
		return nil, err
	}
	out := make([]gen.PurchasingSupplierItem, 0, len(items))
	for _, it := range items {
		var v *contracts.VariantEntry
		if e, ok := variants[it.VariantID]; ok {
			v = &e
		}
		var sup *contracts.SupplierEntry
		if e, ok := suppliers[it.SupplierID]; ok {
			sup = &e
		}
		out = append(out, itemView(it, v, sup))
	}
	return out, nil
}

// ListPurchasingSupplierItems List supplier items
// (GET /api/v1/purchasing/supplier-items)
func (s *server) ListPurchasingSupplierItems(ctx context.Context, req gen.ListPurchasingSupplierItemsRequestObject) (gen.ListPurchasingSupplierItemsResponseObject, error) {
	p := req.Params
	page, size, f := paging(p.Page, p.PageSize)
	variantIDs := []int32{}
	if p.VariantId != nil {
		if len(*p.VariantId) > 200 {
			f.add("variantId", "At most 200 variants")
		}
		variantIDs = *p.VariantId
	}
	if f.failed() {
		return gen.ListPurchasingSupplierItems400ApplicationProblemPlusJSONResponse(invalid(invalidQueryTitle, f)), nil
	}
	rows, err := s.store().FilterSupplierItems(ctx, store.FilterSupplierItemsParams{SupplierID: p.SupplierId, VariantIds: variantIDs, MaxRows: maxListedItems})
	if err != nil {
		return nil, fmt.Errorf("purchasing: list the supplier items: %w", err)
	}
	all, err := s.itemsView(ctx, rows)
	if err != nil {
		return nil, err
	}
	if p.Q != nil {
		if q := strings.ToLower(strings.TrimSpace(*p.Q)); q != "" {
			all = slices.DeleteFunc(all, func(it gen.PurchasingSupplierItem) bool {
				return !strings.Contains(strings.ToLower(it.Sku+" "+it.ProductName+" "+deref(it.SupplierItemNumber)+" "+deref(it.SupplierItemName)), q)
			})
		}
	}
	slices.SortStableFunc(all, func(a, b gen.PurchasingSupplierItem) int {
		if c := strings.Compare(strings.ToLower(a.SupplierName), strings.ToLower(b.SupplierName)); c != 0 {
			return c
		}
		return strings.Compare(a.Sku, b.Sku)
	})
	out := gen.PurchasingSupplierItemPage{Items: []gen.PurchasingSupplierItem{}, Total: int32(len(all)), Page: page, PageSize: size}
	from := int((page - 1) * size)
	if from < len(all) {
		out.Items = all[from:min(from+int(size), len(all))]
	}
	return gen.ListPurchasingSupplierItems200JSONResponse(out), nil
}

// GetPurchasingSupplierItem Get a supplier item
// (GET /api/v1/purchasing/supplier-items/{itemId})
func (s *server) GetPurchasingSupplierItem(ctx context.Context, req gen.GetPurchasingSupplierItemRequestObject) (gen.GetPurchasingSupplierItemResponseObject, error) {
	view, err := s.itemViewOf(ctx, req.ItemId)
	if err != nil {
		return answer(err, answers[gen.GetPurchasingSupplierItemResponseObject]{
			notFound: func(p gen.PurchasingProblem) gen.GetPurchasingSupplierItemResponseObject {
				return gen.GetPurchasingSupplierItem404ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	return gen.GetPurchasingSupplierItem200JSONResponse(view), nil
}

// itemViewOf reads one supplier item and answers its view.
func (s *server) itemViewOf(ctx context.Context, id int64) (gen.PurchasingSupplierItem, error) {
	it, err := s.store().GetSupplierItem(ctx, id)
	if noRows(err) {
		return gen.PurchasingSupplierItem{}, notFound("supplier item")
	}
	if err != nil {
		return gen.PurchasingSupplierItem{}, fmt.Errorf("purchasing: read supplier item %d: %w", id, err)
	}
	views, err := s.itemsView(ctx, []store.PurchasingSupplierItem{it})
	if err != nil {
		return gen.PurchasingSupplierItem{}, err
	}
	return views[0], nil
}

// itemFields are a supplier item's judged fields.
type itemFields struct {
	number, name    *string
	price           *big.Rat
	packagingID     *int32
	leadTime        *int32
	minimum, mult   *big.Rat
	priceGiven      bool
	preferredWanted bool
}

// readItemFields judges a supplier item's fields into f.
func readItemFields(f *fieldErrors, number, name, price, minimum, multiple *string, leadTime *int32) itemFields {
	out := itemFields{number: textPtr(deref(number)), name: textPtr(deref(name)), leadTime: leadTime}
	if out.number != nil && utf8.RuneCountInString(*out.number) > 60 {
		f.add("supplierItemNumber", "The supplier's item number is at most 60 characters")
	}
	if out.name != nil && utf8.RuneCountInString(*out.name) > 255 {
		f.add("supplierItemName", "The supplier's name for the item is at most 255 characters")
	}
	if price != nil {
		p, ok := parseDecimal(*price, pricePlaces, maxPrice)
		if !ok {
			f.addCoded("price", codePriceInvalid, "A price is 0 or more, with at most four decimals")
		}
		out.price, out.priceGiven = p, true
	}
	for _, q := range []struct {
		field string
		text  *string
		into  **big.Rat
	}{{"minimumQuantity", minimum, &out.minimum}, {"multiple", multiple, &out.mult}} {
		if q.text == nil {
			continue
		}
		v, ok := readQuantity(*q.text)
		if !ok {
			f.addCoded(q.field, codeQuantityInvalid, "A quantity is more than 0, with at most three decimals")
		}
		*q.into = v
	}
	if leadTime != nil && (*leadTime < 0 || *leadTime > 3650) {
		f.add("leadTimeDays", "A lead time is 0 to 3650 days")
	}
	return out
}

// today is today in the business zone, before any transaction.
func (s *server) today(ctx context.Context) (time.Time, error) {
	zone, err := s.businessZone(ctx)
	if err != nil {
		return time.Time{}, err
	}
	return dayOf(s.deps.Clock(), zone), nil
}

// CreatePurchasingSupplierItem Create a supplier item
// (POST /api/v1/purchasing/supplier-items)
func (s *server) CreatePurchasingSupplierItem(ctx context.Context, req gen.CreatePurchasingSupplierItemRequestObject) (gen.CreatePurchasingSupplierItemResponseObject, error) {
	resp := answers[gen.CreatePurchasingSupplierItemResponseObject]{
		badRequest: func(p gen.PurchasingProblem) gen.CreatePurchasingSupplierItemResponseObject {
			return gen.CreatePurchasingSupplierItem400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func(p gen.PurchasingProblem) gen.CreatePurchasingSupplierItemResponseObject {
			return gen.CreatePurchasingSupplierItem404ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.PurchasingProblem) gen.CreatePurchasingSupplierItemResponseObject {
			return gen.CreatePurchasingSupplierItem409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.PurchasingProblem) gen.CreatePurchasingSupplierItemResponseObject {
			return gen.CreatePurchasingSupplierItem503ApplicationProblemPlusJSONResponse(p)
		},
	}
	b := *req.Body
	var f fieldErrors
	fields := readItemFields(&f, b.SupplierItemNumber, b.SupplierItemName, b.Price, b.MinimumQuantity, b.Multiple, b.LeadTimeDays)
	fields.packagingID = b.PackagingId
	fields.preferredWanted = b.Preferred != nil && *b.Preferred
	if f.failed() {
		return gen.CreatePurchasingSupplierItem400ApplicationProblemPlusJSONResponse(invalid(invalidSupplierItemTitle, f)), nil
	}
	today, err := s.today(ctx)
	if err != nil {
		return nil, err
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var id int64
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		variants, err := s.lockVariants(ctx, tx, []int32{b.VariantId})
		if err != nil {
			return err
		}
		v, ok := variants[b.VariantId]
		if !ok {
			return refuseField(invalidSupplierItemTitle, "variantId", codeVariantNotFound, fmt.Sprintf("No variant has the id %d", b.VariantId))
		}
		if fields.packagingID != nil && packagingOf(v, *fields.packagingID) == nil {
			return refuseField(invalidSupplierItemTitle, "packagingId", codePackagingNotFound, fmt.Sprintf("%s has no packaging %d", v.SKU, *fields.packagingID))
		}
		held, err := s.lockSuppliers(ctx, tx, b.SupplierId)
		if err != nil {
			return err
		}
		sup, ok := held[b.SupplierId]
		if !ok {
			return refuseField(invalidSupplierItemTitle, "supplierId", codeSupplierNotFound, fmt.Sprintf("No supplier has the id %d", b.SupplierId))
		}
		if err := txq.LockVariantItems(ctx, b.VariantId); err != nil {
			return fmt.Errorf("purchasing: lock variant %d's items: %w", b.VariantId, err)
		}
		row, err := insertItem(ctx, txq, b.SupplierId, v.ID, sup, fields, priceByPerson, by, today, s.deps.Clock())
		if err != nil {
			return err
		}
		id = row.ID
		return nil
	})
	if err != nil {
		return answer(err, resp)
	}
	view, err := s.itemViewOf(ctx, id)
	if err != nil {
		return nil, err
	}
	return gen.CreatePurchasingSupplierItem201JSONResponse(view), nil
}

// supplierCurrency is the currency a supplier's prices are in: its own in
// the register, else the books'.
func supplierCurrency(sup contracts.SupplierEntry) string {
	if c := strings.ToUpper(strings.TrimSpace(sup.Currency)); c != "" {
		return c
	}
	return baseCurrency
}

// insertItem writes a new supplier item under the variant's advisory lock:
// one per supplier and variant (supplier_item_exists), preferred when it is
// the variant's first or asked to be.
func insertItem(ctx context.Context, txq *store.Queries, supplierID int64, variantID int32, sup contracts.SupplierEntry, fields itemFields,
	setBy string, by actor, today, now time.Time) (store.PurchasingSupplierItem, error) {
	exists, err := txq.SupplierItemExists(ctx, store.SupplierItemExistsParams{SupplierID: supplierID, VariantID: variantID})
	if err != nil {
		return store.PurchasingSupplierItem{}, fmt.Errorf("purchasing: read the supplier's item: %w", err)
	}
	if exists {
		return store.PurchasingSupplierItem{}, conflict(codeSupplierItemExists, "The supplier has an item of this variant",
			"A supplier has one item per variant; change that one instead.")
	}
	hasPreferred, err := txq.VariantHasPreferred(ctx, variantID)
	if err != nil {
		return store.PurchasingSupplierItem{}, fmt.Errorf("purchasing: read variant %d's preferred item: %w", variantID, err)
	}
	preferred := !hasPreferred || fields.preferredWanted
	if preferred && hasPreferred {
		if err := txq.ClearPreferred(ctx, store.ClearPreferredParams{VariantID: variantID, ExceptID: 0, Now: now}); err != nil {
			return store.PurchasingSupplierItem{}, fmt.Errorf("purchasing: move variant %d's preference: %w", variantID, err)
		}
	}
	params := store.InsertSupplierItemParams{
		SupplierID: supplierID, VariantID: variantID, SupplierItemNumber: fields.number, SupplierItemName: fields.name,
		Price: numeric(fields.price, pricePlaces), Currency: supplierCurrency(sup), PackagingID: fields.packagingID,
		LeadTimeDays: fields.leadTime, MinimumQuantity: numeric(fields.minimum, quantityPlaces),
		OrderMultiple: numeric(fields.mult, quantityPlaces), Preferred: preferred, Now: now,
	}
	if fields.price != nil {
		params.PriceSetOn, params.PriceSetBy = dayCol(today), &setBy
		if setBy == priceByPerson {
			params.PriceSetByUserID = by.id
		}
	}
	row, err := txq.InsertSupplierItem(ctx, params)
	if err != nil {
		return row, fmt.Errorf("purchasing: create the supplier item: %w", err)
	}
	return row, nil
}

// UpdatePurchasingSupplierItem Update a supplier item
// (PUT /api/v1/purchasing/supplier-items/{itemId})
//
// A price written is set today by the caller, in the supplier's currency
// now. Preferred true takes the variant's preference from its other item;
// false leaves the preference where it is — it moves only to another item,
// so the variant's preferred item stays preferred.
func (s *server) UpdatePurchasingSupplierItem(ctx context.Context, req gen.UpdatePurchasingSupplierItemRequestObject) (gen.UpdatePurchasingSupplierItemResponseObject, error) {
	resp := answers[gen.UpdatePurchasingSupplierItemResponseObject]{
		badRequest: func(p gen.PurchasingProblem) gen.UpdatePurchasingSupplierItemResponseObject {
			return gen.UpdatePurchasingSupplierItem400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func(p gen.PurchasingProblem) gen.UpdatePurchasingSupplierItemResponseObject {
			return gen.UpdatePurchasingSupplierItem404ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.PurchasingProblem) gen.UpdatePurchasingSupplierItemResponseObject {
			return gen.UpdatePurchasingSupplierItem409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.PurchasingProblem) gen.UpdatePurchasingSupplierItemResponseObject {
			return gen.UpdatePurchasingSupplierItem503ApplicationProblemPlusJSONResponse(p)
		},
	}
	b := *req.Body
	var f fieldErrors
	fields := readItemFields(&f, b.SupplierItemNumber, b.SupplierItemName, b.Price, b.MinimumQuantity, b.Multiple, b.LeadTimeDays)
	fields.packagingID = b.PackagingId
	if f.failed() {
		return gen.UpdatePurchasingSupplierItem400ApplicationProblemPlusJSONResponse(invalid(invalidSupplierItemTitle, f)), nil
	}
	cur, err := s.store().GetSupplierItem(ctx, req.ItemId)
	if noRows(err) {
		return answer(notFound("supplier item"), resp)
	}
	if err != nil {
		return nil, fmt.Errorf("purchasing: read supplier item %d: %w", req.ItemId, err)
	}
	sup, err := s.supplier(ctx, cur.SupplierID)
	if err != nil {
		return answer(err, resp)
	}
	today, err := s.today(ctx)
	if err != nil {
		return nil, err
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		variants, err := s.lockVariants(ctx, tx, []int32{cur.VariantID})
		if err != nil {
			return err
		}
		if err := txq.LockVariantItems(ctx, cur.VariantID); err != nil {
			return fmt.Errorf("purchasing: lock variant %d's items: %w", cur.VariantID, err)
		}
		it, err := txq.LockSupplierItem(ctx, req.ItemId)
		if noRows(err) {
			return notFound("supplier item")
		}
		if err != nil {
			return fmt.Errorf("purchasing: lock supplier item %d: %w", req.ItemId, err)
		}
		if it.Revision != b.Revision {
			return versionConflict("supplier item's revision", it.Revision, b.Revision)
		}
		if fields.packagingID != nil && !samePackaging(fields.packagingID, it.PackagingID) {
			v, ok := variants[it.VariantID]
			if !ok || packagingOf(v, *fields.packagingID) == nil {
				return refuseField(invalidSupplierItemTitle, "packagingId", codePackagingNotFound, "The variant has no such packaging")
			}
		}
		now := s.deps.Clock()
		params := store.UpdateSupplierItemParams{
			ID: it.ID, SupplierItemNumber: fields.number, SupplierItemName: fields.name, Price: numeric(fields.price, pricePlaces),
			Currency: it.Currency, PriceSetOn: it.PriceSetOn, PriceSetBy: it.PriceSetBy, PriceSetByUserID: it.PriceSetByUserID,
			PackagingID: fields.packagingID, LeadTimeDays: fields.leadTime, MinimumQuantity: numeric(fields.minimum, quantityPlaces),
			OrderMultiple: numeric(fields.mult, quantityPlaces), Preferred: it.Preferred || b.Preferred, Now: now,
		}
		priceChanged := !samePtrRat(fields.price, ratOf(it.Price))
		if priceChanged || (sup != nil && fields.price != nil && supplierCurrency(*sup) != it.Currency) {
			params.PriceSetOn, params.PriceSetBy, params.PriceSetByUserID = pgDate(fields.price, today), nil, nil
			if fields.price != nil {
				params.PriceSetBy, params.PriceSetByUserID = ptr(priceByPerson), by.id
			}
			if sup != nil {
				params.Currency = supplierCurrency(*sup)
			}
		}
		if b.Preferred && !it.Preferred {
			if err := txq.ClearPreferred(ctx, store.ClearPreferredParams{VariantID: it.VariantID, ExceptID: it.ID, Now: now}); err != nil {
				return fmt.Errorf("purchasing: move variant %d's preference: %w", it.VariantID, err)
			}
		}
		if _, err := txq.UpdateSupplierItem(ctx, params); err != nil {
			return fmt.Errorf("purchasing: save supplier item %d: %w", it.ID, err)
		}
		return nil
	})
	if err != nil {
		return answer(err, resp)
	}
	view, err := s.itemViewOf(ctx, req.ItemId)
	if err != nil {
		return nil, err
	}
	return gen.UpdatePurchasingSupplierItem200JSONResponse(view), nil
}

// samePtrRat reports whether two optional decimals are equal.
func samePtrRat(a, b *big.Rat) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Cmp(b) == 0
}

// pgDate is today as a column when a price is set, NULL when it is cleared.
func pgDate(price *big.Rat, today time.Time) pgtype.Date {
	if price == nil {
		return pgtype.Date{}
	}
	return dayCol(today)
}

// DeletePurchasingSupplierItem Delete a supplier item
// (DELETE /api/v1/purchasing/supplier-items/{itemId})
//
// When it was the variant's preferred item, the variant's oldest other item
// becomes preferred. Orders keep what they took from it.
func (s *server) DeletePurchasingSupplierItem(ctx context.Context, req gen.DeletePurchasingSupplierItemRequestObject) (gen.DeletePurchasingSupplierItemResponseObject, error) {
	cur, err := s.store().GetSupplierItem(ctx, req.ItemId)
	if noRows(err) {
		return gen.DeletePurchasingSupplierItem404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, codeNotFound, "Not found", "No supplier item has this id.")), nil
	}
	if err != nil {
		return nil, fmt.Errorf("purchasing: read supplier item %d: %w", req.ItemId, err)
	}
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if err := txq.LockVariantItems(ctx, cur.VariantID); err != nil {
			return fmt.Errorf("purchasing: lock variant %d's items: %w", cur.VariantID, err)
		}
		it, err := txq.LockSupplierItem(ctx, req.ItemId)
		if noRows(err) {
			return notFound("supplier item")
		}
		if err != nil {
			return fmt.Errorf("purchasing: lock supplier item %d: %w", req.ItemId, err)
		}
		if err := txq.DeleteSupplierItem(ctx, it.ID); err != nil {
			return fmt.Errorf("purchasing: delete supplier item %d: %w", it.ID, err)
		}
		if it.Preferred {
			return txq.PromoteOldestItem(ctx, store.PromoteOldestItemParams{Variant: it.VariantID, Now: s.deps.Clock()})
		}
		return nil
	})
	if err != nil {
		return answer(err, answers[gen.DeletePurchasingSupplierItemResponseObject]{
			notFound: func(p gen.PurchasingProblem) gen.DeletePurchasingSupplierItemResponseObject {
				return gen.DeletePurchasingSupplierItem404ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	return gen.DeletePurchasingSupplierItem204Response{}, nil
}
