package energy

import (
	"context"
	"errors"

	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
)

// errSpotPricesNotBuilt holds the three operations' place until task 3.
var errSpotPricesNotBuilt = errors.New("energy: spot prices are not built yet")

func (s *server) GetEnergySpotPrices(ctx context.Context, req gen.GetEnergySpotPricesRequestObject) (gen.GetEnergySpotPricesResponseObject, error) {
	return nil, errSpotPricesNotBuilt
}

func (s *server) GetEnergySpotPricesCoverage(ctx context.Context, req gen.GetEnergySpotPricesCoverageRequestObject) (gen.GetEnergySpotPricesCoverageResponseObject, error) {
	return nil, errSpotPricesNotBuilt
}

func (s *server) PostEnergySpotPricesImport(ctx context.Context, req gen.PostEnergySpotPricesImportRequestObject) (gen.PostEnergySpotPricesImportResponseObject, error) {
	return nil, errSpotPricesNotBuilt
}
