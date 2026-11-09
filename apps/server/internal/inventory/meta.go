package inventory

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
)

// GetInventoryMeta Inventory capabilities
// (GET /api/v1/inventory/meta)
//
// The caller's seven keys (§5), so the app shows only what works. The access
// is the session: a caller with none of them learns that, not a 403.
func (s *server) GetInventoryMeta(ctx context.Context, _ gen.GetInventoryMetaRequestObject) (gen.GetInventoryMetaResponseObject, error) {
	return gen.GetInventoryMeta200JSONResponse(gen.InventoryMeta{
		CanViewLocations:   s.has(ctx, permLocationsView),
		CanManageLocations: s.has(ctx, permLocationsManage),
		CanViewStock:       s.has(ctx, permStockView),
		CanAdjust:          s.has(ctx, permStockAdjust),
		CanReceive:         s.has(ctx, permReceiptsManage),
		CanCount:           s.has(ctx, permCountsManage),
		CanTransfer:        s.has(ctx, permTransfersManage),
	}), nil
}
