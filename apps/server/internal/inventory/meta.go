package inventory

import (
	"context"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
)

// GetInventoryMeta Inventory capabilities
// (GET /api/v1/inventory/meta)
//
// The caller's eight keys (§5; manufacturing phase 2 M68's expired-override)
// and today in the workspace zone, the day a lot's expiry is judged on (M62), so the app shows only what works. The access
// is the session: a caller with none of them learns that, not a 403.
func (s *server) GetInventoryMeta(ctx context.Context, _ gen.GetInventoryMetaRequestObject) (gen.GetInventoryMetaResponseObject, error) {
	today, err := s.today(ctx)
	if err != nil {
		return nil, err
	}
	return gen.GetInventoryMeta200JSONResponse(gen.InventoryMeta{
		CanViewLocations:   s.has(ctx, permLocationsView),
		CanManageLocations: s.has(ctx, permLocationsManage),
		CanViewStock:       s.has(ctx, permStockView),
		CanAdjust:          s.has(ctx, permStockAdjust),
		CanReceive:         s.has(ctx, permReceiptsManage),
		CanCount:           s.has(ctx, permCountsManage),
		CanTransfer:        s.has(ctx, permTransfersManage),
		CanOverrideExpired: s.has(ctx, permExpiredOverride),
		Today:              openapi_types.Date{Time: today},
	}), nil
}
