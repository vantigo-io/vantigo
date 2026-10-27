package energy

import (
	"context"
	"errors"

	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
)

// GetEnergyMeteringPointsByIdSettlements lists a metering point's settlements
// (GET /api/v1/energy/metering-points/{id}/settlements). Stubbed until the
// endpoint lands (energy settlements plan, task 4).
func (s *server) GetEnergyMeteringPointsByIdSettlements(ctx context.Context, req gen.GetEnergyMeteringPointsByIdSettlementsRequestObject) (gen.GetEnergyMeteringPointsByIdSettlementsResponseObject, error) {
	return nil, errors.New("energy: the settlements endpoint is not built yet")
}
