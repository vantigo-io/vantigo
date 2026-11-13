package manufacturing

// The module's reads beside its records (manufacturing design M7, M8, M10):
// what the caller may do, the overview's counts, the active Inventory sites a
// work center may stand at and the catalog's goods variants a BoM may name.

import (
	"context"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

// currency is the workspace currency every cost of this module is in (M8).
const currency = "NOK"

// goodsType is the catalog's product type a BoM may name (M10).
const goodsType = "Goods"

// The variant search's page (searchManufacturingVariants).
const (
	defaultVariantLimit = 20
	maxVariantLimit     = 50
)

// GetManufacturingMeta is what the caller may do in the app.
func (s *server) GetManufacturingMeta(ctx context.Context, _ gen.GetManufacturingMetaRequestObject) (gen.GetManufacturingMetaResponseObject, error) {
	return gen.GetManufacturingMeta200JSONResponse{
		CanManageBoms:    s.has(ctx, permBomsManage),
		CanSeeCosts:      s.has(ctx, permCostsView),
		CanManageOrders:  s.has(ctx, permOrdersManage),
		CanWorkShopFloor: s.has(ctx, permShopFloor),
		CanManageQuality: s.has(ctx, permQualityManage),
		CanTraceLots:     s.deps.Stock != nil && contracts.HasPermissions(ctx, s.deps.Access, permInventoryLocationsView, permInventoryStockView),
		Currency:         currency,
	}, nil
}

// GetManufacturingStats is the overview's counts: work centers, BoMs and
// the order cards (M7), today being the business calendar's, answered so
// the overview's week is the business calendar's too.
func (s *server) GetManufacturingStats(ctx context.Context, _ gen.GetManufacturingStatsRequestObject) (gen.GetManufacturingStatsResponseObject, error) {
	today, tomorrow, err := s.today(ctx)
	if err != nil {
		return nil, err
	}
	q := store.New(s.deps.Pool)
	stats, err := q.Stats(ctx)
	if err != nil {
		return nil, err
	}
	orders, err := q.OrderStats(ctx, store.OrderStatsParams{Tomorrow: tomorrow, Today: pgtype.Date{Time: today, Valid: true}})
	if err != nil {
		return nil, err
	}
	var suggestions int32
	if s.deps.Stock != nil {
		sites, err := s.activeSites(ctx)
		if err != nil {
			return nil, err
		}
		ids := make([]int64, 0, len(sites))
		for _, site := range sites {
			ids = append(ids, site.ID)
		}
		if suggestions, err = q.CountOpenMakeSuggestions(ctx, ids); err != nil {
			return nil, err
		}
	}
	return gen.GetManufacturingStats200JSONResponse{
		ActiveWorkCenters: stats.ActiveWorkCenters, BlockedWorkCenters: stats.BlockedWorkCenters, ActiveBoms: stats.ActiveBoms,
		OrdersToStartToday: orders.ToStartToday, OrdersInProgress: orders.InProgress, OrdersLate: orders.Late,
		SuggestionsToMake: suggestions,
		Today:             openapi_types.Date{Time: today},
	}, nil
}

// ListManufacturingSites is the active Inventory sites, by code: where a work
// center may stand (M8).
func (s *server) ListManufacturingSites(ctx context.Context, _ gen.ListManufacturingSitesRequestObject) (gen.ListManufacturingSitesResponseObject, error) {
	sites, err := s.activeSites(ctx)
	if err != nil {
		return answer(err, answers[gen.ListManufacturingSitesResponseObject]{
			unavailable: func(p gen.ManufacturingProblem) gen.ListManufacturingSitesResponseObject {
				return gen.ListManufacturingSites503ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	items := make([]gen.ManufacturingSite, 0, len(sites))
	for _, site := range sites {
		items = append(items, gen.ManufacturingSite{Id: site.ID, Code: site.Code, Name: site.Name, RootLocationId: site.RootLocationID})
	}
	return gen.ListManufacturingSites200JSONResponse{Items: items}, nil
}

// SearchManufacturingVariants is the catalog's goods variants of active
// products matching q, by SKU, each with whether it has an active BoM.
func (s *server) SearchManufacturingVariants(ctx context.Context, req gen.SearchManufacturingVariantsRequestObject) (gen.SearchManufacturingVariantsResponseObject, error) {
	limit := defaultVariantLimit
	if req.Params.Limit != nil {
		limit = min(max(int(*req.Params.Limit), 1), maxVariantLimit)
	}
	q := ""
	if req.Params.Q != nil {
		q = strings.TrimSpace(*req.Params.Q)
	}
	found, err := s.searchVariants(ctx, q, contracts.VariantSearch{Types: []string{goodsType}, Limit: limit})
	if err != nil {
		return answer(err, answers[gen.SearchManufacturingVariantsResponseObject]{
			unavailable: func(p gen.ManufacturingProblem) gen.SearchManufacturingVariantsResponseObject {
				return gen.SearchManufacturingVariants503ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	ids := make([]int32, 0, len(found))
	for _, v := range found {
		ids = append(ids, v.ID)
	}
	withBom, err := store.New(s.deps.Pool).VariantsWithActiveBom(ctx, ids)
	if err != nil {
		return nil, err
	}
	costs := s.has(ctx, permCostsView)
	items := make([]gen.ManufacturingVariantSearchItem, 0, len(found))
	for _, v := range found {
		ref := variantRef(v.ID, &v, costs)
		items = append(items, gen.ManufacturingVariantSearchItem{
			Id: ref.Id, Sku: ref.Sku, ProductName: ref.ProductName, Unit: ref.Unit, ProductType: ref.ProductType,
			ProductStatus: ref.ProductStatus, StandardCost: ref.StandardCost, HasBom: slices.Contains(withBom, v.ID),
		})
	}
	return gen.SearchManufacturingVariants200JSONResponse{Items: items}, nil
}

// activeSites is Stock's sites that are active, by code.
func (s *server) activeSites(ctx context.Context) ([]contracts.StockSite, error) {
	sites, err := s.stockSites(ctx)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(sites, func(site contracts.StockSite) bool { return !site.Active }), nil
}

// siteOf is the active site id names, or false.
func siteOf(sites []contracts.StockSite, id int64) (contracts.StockSite, bool) {
	for _, site := range sites {
		if site.ID == id {
			return site, true
		}
	}
	return contracts.StockSite{}, false
}

// variantRef is a catalog variant as this module names it, its standard cost
// shaped by costs (M38). A variant the catalog no longer has (an archived
// BoM's, after its delete) is named by its id alone.
func variantRef(id int32, v *contracts.VariantEntry, costs bool) gen.ManufacturingVariantRef {
	if v == nil {
		return gen.ManufacturingVariantRef{Id: id, StandardCost: costJSON(costs, nil)}
	}
	var standard *string
	if v.StandardCost != "" {
		standard = &v.StandardCost
	}
	return gen.ManufacturingVariantRef{
		Id: v.ID, Sku: v.SKU, ProductName: v.ProductName, Unit: v.Unit, ProductType: v.ProductType,
		ProductStatus: v.ProductStatus, StandardCost: costJSON(costs, standard),
	}
}

// refOf is variantRef of id as the catalog answered it in entries.
func refOf(entries map[int32]contracts.VariantEntry, id int32, costs bool) gen.ManufacturingVariantRef {
	if v, ok := entries[id]; ok {
		return variantRef(id, &v, costs)
	}
	return variantRef(id, nil, costs)
}
