package purchasing

// What the Purchasing screens read under purchasing:access alone (P13, P16,
// P23): the register's suppliers with what Purchasing keeps of each, the
// sites, the buyers, the meta, the dashboard's figures and a catalog search
// for a line — so neither Accounting's, Inventory's nor Products' keys are
// needed to buy.

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/store"
)

// Accounting's keys the meta answers on (A12, A25): who may write a supplier
// in the register, and who may open Accounting's supplier pages.
const (
	permAccountingRead            = "accounting:read"
	permAccountingPost            = "accounting:post"
	permAccountingSuppliersManage = "accounting:suppliers-manage"
)

// suppliersView is the register's suppliers with their supplier items and
// open orders counted.
func (s *server) suppliersView(ctx context.Context, entries []contracts.SupplierEntry) ([]gen.PurchasingSupplier, error) {
	ids := make([]int64, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	q := s.store()
	itemRows, err := q.SupplierItemCounts(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("purchasing: count the supplier items: %w", err)
	}
	orderRows, err := q.OpenOrderCounts(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("purchasing: count the open orders: %w", err)
	}
	items, orders := map[int64]int32{}, map[int64]int32{}
	for _, r := range itemRows {
		items[r.SupplierID] = r.Items
	}
	for _, r := range orderRows {
		orders[r.SupplierID] = r.Orders
	}
	out := make([]gen.PurchasingSupplier, 0, len(entries))
	for _, e := range entries {
		out = append(out, gen.PurchasingSupplier{
			Id: e.ID, Number: e.Number, Name: e.Name, OrganisationNumber: e.OrganisationNumber, Country: e.Country,
			AddressLine: e.AddressLine, PostalCode: e.PostalCode, City: e.City, Email: e.Email, Currency: e.Currency,
			PaymentTermsDays: int32(e.PaymentTermsDays), Active: e.Active, IsPerson: e.IsPerson, Anonymised: e.Anonymised,
			SupplierItemCount: items[e.ID], OpenOrderCount: orders[e.ID], Revision: e.Revision,
		})
	}
	return out, nil
}

// ListPurchasingSuppliers List suppliers
// (GET /api/v1/purchasing/suppliers)
func (s *server) ListPurchasingSuppliers(ctx context.Context, req gen.ListPurchasingSuppliersRequestObject) (gen.ListPurchasingSuppliersResponseObject, error) {
	q, limit := "", 50
	if req.Params.Q != nil {
		q = strings.TrimSpace(*req.Params.Q)
	}
	if req.Params.Limit != nil {
		limit = int(min(max(*req.Params.Limit, 1), 200))
	}
	entries, err := s.searchSuppliers(ctx, q, limit)
	if err != nil {
		return answer(err, answers[gen.ListPurchasingSuppliersResponseObject]{
			unavailable: func(p gen.PurchasingProblem) gen.ListPurchasingSuppliersResponseObject {
				return gen.ListPurchasingSuppliers503ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	items, err := s.suppliersView(ctx, entries)
	if err != nil {
		return nil, err
	}
	return gen.ListPurchasingSuppliers200JSONResponse(gen.PurchasingSupplierList{Items: items}), nil
}

// GetPurchasingSupplier Get a supplier
// (GET /api/v1/purchasing/suppliers/{supplierId})
func (s *server) GetPurchasingSupplier(ctx context.Context, req gen.GetPurchasingSupplierRequestObject) (gen.GetPurchasingSupplierResponseObject, error) {
	resp := answers[gen.GetPurchasingSupplierResponseObject]{
		notFound: func(p gen.PurchasingProblem) gen.GetPurchasingSupplierResponseObject {
			return gen.GetPurchasingSupplier404ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.PurchasingProblem) gen.GetPurchasingSupplierResponseObject {
			return gen.GetPurchasingSupplier503ApplicationProblemPlusJSONResponse(p)
		},
	}
	e, err := s.supplier(ctx, req.SupplierId)
	if err != nil {
		return answer(err, resp)
	}
	if e == nil {
		return answer(notFound("supplier"), resp)
	}
	items, err := s.suppliersView(ctx, []contracts.SupplierEntry{*e})
	if err != nil {
		return nil, err
	}
	return gen.GetPurchasingSupplier200JSONResponse(items[0]), nil
}

// ListPurchasingSites List the receiving sites
// (GET /api/v1/purchasing/sites)
func (s *server) ListPurchasingSites(ctx context.Context, _ gen.ListPurchasingSitesRequestObject) (gen.ListPurchasingSitesResponseObject, error) {
	_, list, err := s.sites(ctx)
	if err != nil {
		return answer(err, answers[gen.ListPurchasingSitesResponseObject]{
			unavailable: func(p gen.PurchasingProblem) gen.ListPurchasingSitesResponseObject {
				return gen.ListPurchasingSites503ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	out := gen.PurchasingSiteList{Items: make([]gen.PurchasingSite, 0, len(list))}
	for _, site := range list {
		out.Items = append(out.Items, gen.PurchasingSite{Id: site.ID, Code: site.Code, Name: site.Name, Active: site.Active})
	}
	return gen.ListPurchasingSites200JSONResponse(out), nil
}

// ListPurchasingBuyers List the buyers
// (GET /api/v1/purchasing/buyers)
//
// The active holders of purchasing:orders-manage, by display name.
func (s *server) ListPurchasingBuyers(ctx context.Context, req gen.ListPurchasingBuyersRequestObject) (gen.ListPurchasingBuyersResponseObject, error) {
	q := ""
	if req.Params.Q != nil {
		q = strings.ToLower(strings.TrimSpace(*req.Params.Q))
	}
	holders, err := s.permissionHolders(ctx, permOrdersManage)
	if err != nil {
		return nil, err
	}
	out := gen.PurchasingBuyerList{Items: []gen.PurchasingPerson{}}
	for _, h := range holders {
		if q != "" && !strings.Contains(strings.ToLower(h.DisplayName), q) {
			continue
		}
		id := h.ID
		out.Items = append(out.Items, gen.PurchasingPerson{Id: &id, DisplayName: h.DisplayName})
	}
	slices.SortFunc(out.Items, func(a, b gen.PurchasingPerson) int {
		if c := strings.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName)); c != 0 {
			return c
		}
		return strings.Compare(a.Id.String(), b.Id.String())
	})
	return gen.ListPurchasingBuyers200JSONResponse(out), nil
}

// GetPurchasingMeta Get what the Purchasing screens need
// (GET /api/v1/purchasing/meta)
func (s *server) GetPurchasingMeta(ctx context.Context, _ gen.GetPurchasingMetaRequestObject) (gen.GetPurchasingMetaResponseObject, error) {
	settings, err := readSettings(ctx, s.store())
	if err != nil {
		return nil, err
	}
	_, available, err := s.sendingChannel(ctx)
	if err != nil {
		return nil, err
	}
	read := s.has(ctx, permAccountingRead)
	return gen.GetPurchasingMeta200JSONResponse(gen.PurchasingMeta{
		SendingAvailable: available, BaseCurrency: baseCurrency, DefaultSiteId: settings.defaultSiteID,
		PriceTolerancePercent:      trimmed(settings.priceTolerance, 2),
		CanManageSuppliers:         s.has(ctx, permAccountingSuppliersManage) || (s.has(ctx, permAccountingPost) && read),
		CanOpenAccountingSuppliers: read,
	}), nil
}

// SearchPurchasingVariants Search the catalog for an order line
// (GET /api/v1/purchasing/variants)
//
// Goods and services of active products, each with the supplier's item when
// supplierId is given and the supplier sells the variant; with
// onlySupplierItems, only what the supplier sells.
func (s *server) SearchPurchasingVariants(ctx context.Context, req gen.SearchPurchasingVariantsRequestObject) (gen.SearchPurchasingVariantsResponseObject, error) {
	resp := answers[gen.SearchPurchasingVariantsResponseObject]{
		unavailable: func(p gen.PurchasingProblem) gen.SearchPurchasingVariantsResponseObject {
			return gen.SearchPurchasingVariants503ApplicationProblemPlusJSONResponse(p)
		},
	}
	p := req.Params
	q, limit := "", 20
	if p.Q != nil {
		q = strings.TrimSpace(*p.Q)
	}
	if p.Limit != nil {
		limit = int(min(max(*p.Limit, 1), 50))
	}
	var variants []contracts.VariantEntry
	var items map[int32]store.PurchasingSupplierItem
	var err error
	if p.SupplierId != nil && p.OnlySupplierItems != nil && *p.OnlySupplierItems {
		rows, err := s.store().FilterSupplierItems(ctx, store.FilterSupplierItemsParams{SupplierID: p.SupplierId, VariantIds: []int32{}, MaxRows: maxListedItems})
		if err != nil {
			return nil, fmt.Errorf("purchasing: read the supplier's items: %w", err)
		}
		items = map[int32]store.PurchasingSupplierItem{}
		var ids []int32
		for _, r := range rows {
			items[r.VariantID] = r
			ids = append(ids, r.VariantID)
		}
		byID, err := s.catalogVariants(ctx, ids)
		if err != nil {
			return answer(err, resp)
		}
		needle := strings.ToLower(q)
		for _, v := range byID {
			it := items[v.ID]
			if needle != "" && !strings.Contains(strings.ToLower(v.SKU+" "+v.ProductName+" "+deref(v.Barcode)+" "+deref(it.SupplierItemNumber)), needle) {
				continue
			}
			variants = append(variants, v)
		}
		slices.SortFunc(variants, func(a, b contracts.VariantEntry) int { return strings.Compare(a.SKU, b.SKU) })
		if len(variants) > limit {
			variants = variants[:limit]
		}
	} else {
		if variants, err = s.searchVariants(ctx, q, contracts.VariantSearch{Types: []string{typeGoods, typeService}, Limit: limit}); err != nil {
			return answer(err, resp)
		}
		if p.SupplierId != nil {
			ids := make([]int32, 0, len(variants))
			for _, v := range variants {
				ids = append(ids, v.ID)
			}
			if items, err = itemsOf(ctx, s.store(), *p.SupplierId, ids); err != nil {
				return nil, err
			}
		}
	}
	var sup *contracts.SupplierEntry
	if p.SupplierId != nil && len(items) > 0 {
		if sup, err = s.supplier(ctx, *p.SupplierId); err != nil {
			return answer(err, resp)
		}
	}
	out := gen.PurchasingVariantList{Items: make([]gen.PurchasingVariantOption, 0, len(variants))}
	for _, v := range variants {
		opt := gen.PurchasingVariantOption{
			VariantId: v.ID, ProductId: v.ProductID, Sku: v.SKU, Name: v.ProductName, Unit: v.Unit,
			ProductType: gen.PurchasingVariantOptionProductType(v.ProductType), ProductStatus: v.ProductStatus,
			Packagings: make([]gen.PurchasingPackaging, 0, len(v.Packagings)),
		}
		for _, pk := range v.Packagings {
			opt.Packagings = append(opt.Packagings, gen.PurchasingPackaging{Id: pk.ID, Name: pk.Name, Quantity: trimmed(mustRat(pk.Quantity), quantityPlaces)})
		}
		if it, ok := items[v.ID]; ok {
			entry := v
			opt.SupplierItem = ptr(itemView(it, &entry, sup))
		}
		out.Items = append(out.Items, opt)
	}
	return gen.SearchPurchasingVariants200JSONResponse(out), nil
}
