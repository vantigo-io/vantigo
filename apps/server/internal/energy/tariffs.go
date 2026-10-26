package energy

import (
	"context"
	"errors"

	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
)

// This file is the Tariffs area (energy consumption billing design D3,
// energy tariffs plan readings 2–4): getEnergyTariffs, postEnergyTariffs,
// getEnergyTariff, putEnergyTariffsById, deleteEnergyTariffsById and
// postEnergyTariffsByIdArchive. Stubbed until task 3.

var errTariffsNotBuilt = errors.New("energy: tariffs are not built yet")

func (s *server) GetEnergyTariffs(ctx context.Context, req gen.GetEnergyTariffsRequestObject) (gen.GetEnergyTariffsResponseObject, error) {
	return nil, errTariffsNotBuilt
}

func (s *server) PostEnergyTariffs(ctx context.Context, req gen.PostEnergyTariffsRequestObject) (gen.PostEnergyTariffsResponseObject, error) {
	return nil, errTariffsNotBuilt
}

func (s *server) GetEnergyTariff(ctx context.Context, req gen.GetEnergyTariffRequestObject) (gen.GetEnergyTariffResponseObject, error) {
	return nil, errTariffsNotBuilt
}

func (s *server) PutEnergyTariffsById(ctx context.Context, req gen.PutEnergyTariffsByIdRequestObject) (gen.PutEnergyTariffsByIdResponseObject, error) {
	return nil, errTariffsNotBuilt
}

func (s *server) DeleteEnergyTariffsById(ctx context.Context, req gen.DeleteEnergyTariffsByIdRequestObject) (gen.DeleteEnergyTariffsByIdResponseObject, error) {
	return nil, errTariffsNotBuilt
}

func (s *server) PostEnergyTariffsByIdArchive(ctx context.Context, req gen.PostEnergyTariffsByIdArchiveRequestObject) (gen.PostEnergyTariffsByIdArchiveResponseObject, error) {
	return nil, errTariffsNotBuilt
}
