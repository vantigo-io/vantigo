package energy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
)

// This file is the Meters sub-resource: getEnergyMeteringPointsByIdMeters
// and postEnergyMeteringPointsByIdMeters.

const meterNumberMaxLength = 64 // Meter.MeterNumber's DB column, enforced in ValidateMeterNumber

// validateMeterNumber is Meter.ValidateMeterNumber (DM/Meters/Meter.cs:16-18):
// blank (including whitespace-only) is a required-field error; the length
// check runs against the trimmed value. Trimming alone is always allowed.
func validateMeterNumber(value *string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "Meter number is required."
	}
	if utf16Length(strings.TrimSpace(*value)) > meterNumberMaxLength {
		return "Meter number cannot be longer than 64 characters."
	}
	return ""
}

func meterResponseOf(m store.EnergyMeter) gen.MeterResponse {
	return gen.MeterResponse{
		Id: m.ID, MeteringPointId: m.MeteringPointID, MeterNumber: m.MeterNumber,
		InstalledAt: m.InstalledAt, RemovedAt: m.RemovedAt,
	}
}

// GetEnergyMeteringPointsByIdMeters List meter history
// (GET /api/v1/energy/metering-points/{id}/meters)
//
// GetMetersEndpoint.cs:14-18: existence (404) -> every meter for the point,
// ordered by InstalledAt ascending.
func (s *server) GetEnergyMeteringPointsByIdMeters(ctx context.Context, req gen.GetEnergyMeteringPointsByIdMetersRequestObject) (gen.GetEnergyMeteringPointsByIdMetersResponseObject, error) {
	q := store.New(s.deps.Pool)
	exists, err := q.MeteringPointExists(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("energy: check metering point exists: %w", err)
	}
	if !exists {
		return gen.GetEnergyMeteringPointsByIdMeters404Response{}, nil
	}
	rows, err := q.ListMetersByMeteringPoint(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("energy: list meters: %w", err)
	}
	data := make([]gen.MeterResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, meterResponseOf(r))
	}
	return gen.GetEnergyMeteringPointsByIdMeters200JSONResponse(data), nil
}

// PostEnergyMeteringPointsByIdMeters Replace a meter
// (POST /api/v1/energy/metering-points/{id}/meters)
//
// ReplaceMeterEndpoint.cs:15-38 (energy inventory §1.1 line 34, corrected by
// this task's dispatch): existence *before* validation — the opposite order
// of PUT /{id} above — then, inside a transaction, a conflicting
// `installedAt <= active.InstalledAt` is a 400 ValidationProblem on field
// installedAt, not a 409, despite being a business conflict; the contract
// declares no 409 for this operation, which matches. Closing the old meter
// and inserting the new one happen in the same transaction so a metering
// point is never observably left with zero active meters.
func (s *server) PostEnergyMeteringPointsByIdMeters(ctx context.Context, req gen.PostEnergyMeteringPointsByIdMetersRequestObject) (gen.PostEnergyMeteringPointsByIdMetersResponseObject, error) {
	q := store.New(s.deps.Pool)
	exists, err := q.MeteringPointExists(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("energy: check metering point exists: %w", err)
	}
	if !exists {
		return gen.PostEnergyMeteringPointsByIdMeters404Response{}, nil
	}

	body := gen.ReplaceMeterRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	errs := map[string][]string{}
	if msg := validateMeterNumber(body.MeterNumber); msg != "" {
		errs["meterNumber"] = []string{msg}
	}
	if body.InstalledAt == nil {
		errs["installedAt"] = []string{"Installed at is required."}
	}
	if len(errs) > 0 {
		return gen.PostEnergyMeteringPointsByIdMeters400ApplicationProblemPlusJSONResponse(validationProblem("Invalid meter", errs)), nil
	}

	installedAt := *body.InstalledAt
	var created store.EnergyMeter
	var conflictMessage string
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		active, aerr := txq.GetActiveMeter(ctx, req.Id)
		hasActive := true
		switch {
		case errors.Is(aerr, pgx.ErrNoRows):
			hasActive = false
		case aerr != nil:
			return aerr
		}

		if hasActive && !installedAt.After(active.InstalledAt) {
			conflictMessage = "Installed at must be after the active meter's installation time."
			return nil
		}
		if hasActive {
			if serr := txq.SetMeterRemovedAt(ctx, store.SetMeterRemovedAtParams{ID: active.ID, RemovedAt: installedAt}); serr != nil {
				return serr
			}
		}
		var ierr error
		created, ierr = txq.InsertMeter(ctx, store.InsertMeterParams{
			MeteringPointID: req.Id, MeterNumber: strings.TrimSpace(*body.MeterNumber), InstalledAt: installedAt,
		})
		return ierr
	})
	if err != nil {
		return nil, fmt.Errorf("energy: replace meter: %w", err)
	}
	if conflictMessage != "" {
		return gen.PostEnergyMeteringPointsByIdMeters400ApplicationProblemPlusJSONResponse(validationProblem(
			"Invalid meter", map[string][]string{"installedAt": {conflictMessage}})), nil
	}
	return gen.PostEnergyMeteringPointsByIdMeters201JSONResponse(meterResponseOf(created)), nil
}
