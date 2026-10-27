package energy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
)

// This file is the Tariffs area (energy consumption billing design D3,
// energy tariffs plan readings 2–4): getEnergyTariffs, postEnergyTariffs,
// getEnergyTariff, putEnergyTariffsById, deleteEnergyTariffsById and
// postEnergyTariffsByIdArchive. A tariff is a header, its versions by date
// and each version's components; the body is judged whole by
// validateTariff (tariffs_validate.go) and written whole, the versions and
// components of a PUT replacing the stored ones in one transaction. Nothing
// here prices anything: settlements (delivery C) read what is stored.

const invalidTariffTitle = "Invalid tariff"

// The foreign keys that make a tariff in use (00076_energy_tariffs.sql):
// RESTRICT, so a delete that slips past the usage check is refused by them.
var tariffUseConstraints = []string{"supply_periods_tariff_id_fkey", "metering_points_grid_tariff_id_fkey"}

// errTariffNotFound ends a transaction whose tariff is gone: the handler
// answers 404.
var errTariffNotFound = errors.New("energy: tariff not found")

// osloToday is the civil day in Europe/Oslo at now: the day a tariff's
// "valid today" version is judged on, whatever the points' areas, as the
// market's day is (spotPriceDayZone).
func osloToday(now time.Time) (pgtype.Date, error) {
	loc, err := time.LoadLocation(spotPriceDayZone)
	if err != nil {
		return pgtype.Date{}, fmt.Errorf("energy: load the market day zone: %w", err)
	}
	n := now.In(loc)
	return pgtype.Date{Time: time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC), Valid: true}, nil
}

// apiDate and apiDatePtr are a stored date on the wire; NULL is nil.
func apiDate(d pgtype.Date) openapi_types.Date {
	return openapi_types.Date{Time: d.Time}
}

func apiDatePtr(d pgtype.Date) *openapi_types.Date {
	if !d.Valid {
		return nil
	}
	v := apiDate(d)
	return &v
}

func int32PtrOf(v *int16) *int32 {
	if v == nil {
		return nil
	}
	w := int32(*v)
	return &w
}

// tariffComponentOf is a stored component on the wire.
func tariffComponentOf(c store.EnergyTariffComponent) (gen.TariffComponent, error) {
	out := gen.TariffComponent{
		Id: c.ID, Position: int32(c.Position), Kind: c.Kind, Name: c.Name,
		Rate: floatPtrFromNumeric(c.Rate), RateNight: floatPtrFromNumeric(c.RateNight),
		RateReduced: floatPtrFromNumeric(c.RateReduced), RateBusiness: floatPtrFromNumeric(c.RateBusiness),
		DayFrom: int32PtrOf(c.DayFrom), DayTo: int32PtrOf(c.DayTo), WeekdaysOnly: c.WeekdaysOnly,
	}
	if c.Tiers != nil {
		var tiers []gen.TariffTier
		if err := json.Unmarshal(c.Tiers, &tiers); err != nil {
			return gen.TariffComponent{}, fmt.Errorf("energy: read component %d's tiers: %w", c.ID, err)
		}
		out.Tiers = &tiers
	}
	return out, nil
}

// tariffResponse is t with its versions in valid_from order, each with its
// components in position order (one query for all of them), and its usage.
func tariffResponse(ctx context.Context, q *store.Queries, t store.EnergyTariff) (gen.Tariff, error) {
	versions, err := q.ListTariffVersions(ctx, t.ID)
	if err != nil {
		return gen.Tariff{}, fmt.Errorf("energy: list tariff %d's versions: %w", t.ID, err)
	}
	components, err := q.ListTariffComponents(ctx, t.ID)
	if err != nil {
		return gen.Tariff{}, fmt.Errorf("energy: list tariff %d's components: %w", t.ID, err)
	}
	usage, err := q.TariffUsage(ctx, &t.ID)
	if err != nil {
		return gen.Tariff{}, fmt.Errorf("energy: read tariff %d's usage: %w", t.ID, err)
	}

	of := make(map[int32][]gen.TariffComponent, len(versions))
	for _, c := range components {
		wc, err := tariffComponentOf(c)
		if err != nil {
			return gen.Tariff{}, err
		}
		of[c.VersionID] = append(of[c.VersionID], wc)
	}
	out := gen.Tariff{
		Id: t.ID, Kind: t.Kind, Name: t.Name, Description: t.Description, Status: t.Status,
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
		Usage:    gen.TariffUsage{SupplyPeriods: usage.SupplyPeriodCount, MeteringPoints: usage.MeteringPointCount},
		Versions: make([]gen.TariffVersion, 0, len(versions)),
	}
	for _, v := range versions {
		cs := of[v.ID]
		if cs == nil {
			cs = []gen.TariffComponent{}
		}
		out.Versions = append(out.Versions, gen.TariffVersion{
			Id: v.ID, ValidFrom: apiDate(v.ValidFrom), ValidTo: apiDatePtr(v.ValidTo), Components: cs,
		})
	}
	return out, nil
}

// writeTariffVersions inserts in's versions for tariffID with their
// components, inside the caller's transaction.
func writeTariffVersions(ctx context.Context, txq *store.Queries, tariffID int32, in tariffInput) error {
	for _, v := range in.Versions {
		row, err := txq.InsertTariffVersion(ctx, store.InsertTariffVersionParams{TariffID: tariffID, ValidFrom: v.ValidFrom, ValidTo: v.ValidTo})
		if err != nil {
			return err
		}
		for _, c := range v.Components {
			c.VersionID = row.ID
			if err := txq.InsertTariffComponent(ctx, c); err != nil {
				return err
			}
		}
	}
	return nil
}

// isVersionOverlap is the exclusion tariff_versions_no_overlap (23P01)
// refusing an overlap validateTariff did not see: the backstop, answered as
// the same 400 on versions.
func isVersionOverlap(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23P01" && pgErr.ConstraintName == "tariff_versions_no_overlap"
}

// countOf is n with its noun: "1 supply period", "2 supply periods".
func countOf(n int32, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// versionOverlapErrors is isVersionOverlap's 400, a fresh map each time.
func versionOverlapErrors() map[string][]string {
	return map[string][]string{"versions": {"The tariff's versions overlap."}}
}

// GetEnergyTariffs List tariffs
// (GET /api/v1/energy/tariffs)
//
// Reading 4: kind and status, when given, must each be one of their two
// (400, the field named) -> every tariff they narrow to, not paged, active
// first, then by kind and name, each with its version count, the version
// valid today in Europe/Oslo and its usage.
func (s *server) GetEnergyTariffs(ctx context.Context, req gen.GetEnergyTariffsRequestObject) (gen.GetEnergyTariffsResponseObject, error) {
	errs := map[string][]string{}
	if k := req.Params.Kind; k != nil && *k != tariffKindPower && *k != tariffKindGrid {
		errs["kind"] = []string{"Kind must be power or grid."}
	}
	if st := req.Params.Status; st != nil && *st != tariffStatusActive && *st != tariffStatusArchived {
		errs["status"] = []string{"Status must be active or archived."}
	}
	if len(errs) > 0 {
		return gen.GetEnergyTariffs400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid query parameters", errs)), nil
	}

	today, err := osloToday(s.deps.Clock())
	if err != nil {
		return nil, err
	}
	rows, err := store.New(s.deps.Pool).ListTariffs(ctx, store.ListTariffsParams{Today: today, Kind: req.Params.Kind, Status: req.Params.Status})
	if err != nil {
		return nil, fmt.Errorf("energy: list tariffs: %w", err)
	}
	data := make([]gen.TariffSummary, 0, len(rows))
	for _, r := range rows {
		var current *gen.TariffValidity
		if r.CurrentValidFrom.Valid {
			current = &gen.TariffValidity{ValidFrom: apiDate(r.CurrentValidFrom), ValidTo: apiDatePtr(r.CurrentValidTo)}
		}
		data = append(data, gen.TariffSummary{
			Id: r.ID, Kind: r.Kind, Name: r.Name, Description: r.Description, Status: r.Status,
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, VersionCount: r.VersionCount, CurrentVersion: current,
			Usage: gen.TariffUsage{SupplyPeriods: r.SupplyPeriodCount, MeteringPoints: r.MeteringPointCount},
		})
	}
	return gen.GetEnergyTariffs200JSONResponse(data), nil
}

// PostEnergyTariffs Create a tariff
// (POST /api/v1/energy/tariffs)
//
// Reading 4: the body judged whole (400, every refusal keyed by its place)
// -> the header, active, and every version with its components in one
// transaction (an overlap the exclusion refuses is the same 400 on
// versions) -> 201 with the tariff as GET answers it.
func (s *server) PostEnergyTariffs(ctx context.Context, req gen.PostEnergyTariffsRequestObject) (gen.PostEnergyTariffsResponseObject, error) {
	body := gen.TariffRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	in, errs := validateTariff(body)
	if len(errs) > 0 {
		return gen.PostEnergyTariffs400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(invalidTariffTitle, errs)), nil
	}

	var created store.EnergyTariff
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var err error
		created, err = txq.InsertTariff(ctx, store.InsertTariffParams{Kind: in.Kind, Name: in.Name, Description: in.Description, Now: s.deps.Clock()})
		if err != nil {
			return err
		}
		return writeTariffVersions(ctx, txq, created.ID, in)
	})
	if isVersionOverlap(err) {
		return gen.PostEnergyTariffs400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(invalidTariffTitle, versionOverlapErrors())), nil
	}
	if err != nil {
		return nil, fmt.Errorf("energy: create tariff: %w", err)
	}
	resp, err := tariffResponse(ctx, store.New(s.deps.Pool), created)
	if err != nil {
		return nil, err
	}
	return gen.PostEnergyTariffs201JSONResponse(resp), nil
}

// GetEnergyTariff Get a tariff
// (GET /api/v1/energy/tariffs/{id})
//
// Reading 4: existence (404) -> the header, the versions in valid_from
// order with their components in position order, and the usage.
func (s *server) GetEnergyTariff(ctx context.Context, req gen.GetEnergyTariffRequestObject) (gen.GetEnergyTariffResponseObject, error) {
	q := store.New(s.deps.Pool)
	t, err := q.GetTariff(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetEnergyTariff404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("energy: get tariff: %w", err)
	}
	resp, err := tariffResponse(ctx, q, t)
	if err != nil {
		return nil, err
	}
	return gen.GetEnergyTariff200JSONResponse(resp), nil
}

// PutEnergyTariffsById Replace a tariff
// (PUT /api/v1/energy/tariffs/{id})
//
// Reading 4: the body judged whole (400) -> in one transaction, the tariff
// locked FOR UPDATE (404) -> the body's kind must be the stored kind (400 on
// kind) -> the name and description written, every version deleted with its
// components and the body's inserted (an overlap the exclusion refuses is
// the same 400 on versions) -> 200. The status is not touched: an archived
// tariff stays archived and may still be edited, since it still prices the
// periods it covers and a wrong figure in it must stay correctable.
func (s *server) PutEnergyTariffsById(ctx context.Context, req gen.PutEnergyTariffsByIdRequestObject) (gen.PutEnergyTariffsByIdResponseObject, error) {
	body := gen.TariffRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	in, errs := validateTariff(body)
	if len(errs) > 0 {
		return gen.PutEnergyTariffsById400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(invalidTariffTitle, errs)), nil
	}

	var updated store.EnergyTariff
	kindChanged := false
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		stored, err := txq.GetTariffForUpdate(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return errTariffNotFound
		}
		if err != nil {
			return err
		}
		if stored.Kind != in.Kind {
			kindChanged = true
			return nil
		}
		updated, err = txq.UpdateTariffHeader(ctx, store.UpdateTariffHeaderParams{
			ID: req.Id, Name: in.Name, Description: in.Description, Now: s.deps.Clock(),
		})
		if err != nil {
			return err
		}
		if err := txq.DeleteTariffVersions(ctx, req.Id); err != nil {
			return err
		}
		return writeTariffVersions(ctx, txq, req.Id, in)
	})
	switch {
	case errors.Is(err, errTariffNotFound):
		return gen.PutEnergyTariffsById404Response{}, nil
	case isVersionOverlap(err):
		return gen.PutEnergyTariffsById400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(invalidTariffTitle, versionOverlapErrors())), nil
	case err != nil:
		return nil, fmt.Errorf("energy: replace tariff: %w", err)
	case kindChanged:
		return gen.PutEnergyTariffsById400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(
			invalidTariffTitle, map[string][]string{"kind": {"A tariff's kind cannot be changed."}})), nil
	}
	resp, err := tariffResponse(ctx, store.New(s.deps.Pool), updated)
	if err != nil {
		return nil, err
	}
	return gen.PutEnergyTariffsById200JSONResponse(resp), nil
}

// DeleteEnergyTariffsById Delete an unused tariff
// (DELETE /api/v1/energy/tariffs/{id})
//
// Reading 4, the design's "cannot be deleted, only archived" made concrete:
// in one transaction, the tariff locked FOR UPDATE (404) -> how many supply
// periods (of any status) and metering points name it, judged under the
// lock (409 "Tariff in use" when any) -> the delete, its versions and
// components going with it -> 204. The foreign keys' RESTRICT is the
// backstop for a row naming it after the count: its 23001 (23503 before
// PostgreSQL 18) is the same 409.
func (s *server) DeleteEnergyTariffsById(ctx context.Context, req gen.DeleteEnergyTariffsByIdRequestObject) (gen.DeleteEnergyTariffsByIdResponseObject, error) {
	var usage store.TariffUsageRow
	inUse := false
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if _, err := txq.GetTariffForUpdate(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
			return errTariffNotFound
		} else if err != nil {
			return err
		}
		var err error
		usage, err = txq.TariffUsage(ctx, &req.Id)
		if err != nil {
			return err
		}
		if usage.SupplyPeriodCount > 0 || usage.MeteringPointCount > 0 {
			inUse = true
			return nil
		}
		_, err = txq.DeleteTariff(ctx, req.Id)
		return err
	})
	switch {
	case errors.Is(err, errTariffNotFound):
		return gen.DeleteEnergyTariffsById404Response{}, nil
	case db.IsRestrictViolation(err, tariffUseConstraints...) || db.IsForeignKeyViolation(err, tariffUseConstraints...):
		return gen.DeleteEnergyTariffsById409ApplicationProblemPlusJSONResponse(apicommon.ProblemStatus(
			"Tariff in use", "A supply period or a metering point names the tariff. Archive it instead.", http.StatusConflict)), nil
	case err != nil:
		return nil, fmt.Errorf("energy: delete tariff: %w", err)
	case inUse:
		return gen.DeleteEnergyTariffsById409ApplicationProblemPlusJSONResponse(apicommon.ProblemStatus(
			"Tariff in use", fmt.Sprintf("The tariff is named by %s and %s. Archive it instead.",
				countOf(usage.SupplyPeriodCount, "supply period", "supply periods"),
				countOf(usage.MeteringPointCount, "metering point", "metering points")), http.StatusConflict)), nil
	}
	return gen.DeleteEnergyTariffsById204Response{}, nil
}

// PostEnergyTariffsByIdArchive Archive a tariff
// (POST /api/v1/energy/tariffs/{id}/archive)
//
// Reading 4: in one transaction, the tariff locked FOR UPDATE (404) -> an
// archived tariff answered as it is (idempotent: its updated_at does not
// move), an active one archived -> 200. There is no way back in B: an
// archived tariff cannot be assigned anew, and it still prices what it is
// assigned to.
func (s *server) PostEnergyTariffsByIdArchive(ctx context.Context, req gen.PostEnergyTariffsByIdArchiveRequestObject) (gen.PostEnergyTariffsByIdArchiveResponseObject, error) {
	var t store.EnergyTariff
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var err error
		t, err = txq.GetTariffForUpdate(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return errTariffNotFound
		}
		if err != nil || t.Status == tariffStatusArchived {
			return err
		}
		t, err = txq.ArchiveTariff(ctx, store.ArchiveTariffParams{ID: req.Id, Now: s.deps.Clock()})
		return err
	})
	if errors.Is(err, errTariffNotFound) {
		return gen.PostEnergyTariffsByIdArchive404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("energy: archive tariff: %w", err)
	}
	resp, err := tariffResponse(ctx, store.New(s.deps.Pool), t)
	if err != nil {
		return nil, err
	}
	return gen.PostEnergyTariffsByIdArchive200JSONResponse(resp), nil
}
