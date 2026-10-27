package energy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
)

// This file is the MeteringPoints area (EP/MeteringPointsEndpoints.cs's
// bare group plus the single-item routes): getEnergyMeteringPoints,
// postEnergyMeteringPoints, getEnergyMeteringPoint and
// putEnergyMeteringPointsById. meters.go and supplyperiods.go cover the
// nested sub-resources.

const (
	addressStreetMaxLength = 200 // Address.StreetAddressMaxLength
	addressPostalMaxLength = 16  // Address.PostalCodeMaxLength
	addressCityMaxLength   = 100 // Address.CityMaxLength
	gridAreaMaxLength      = 64  // metering_points.grid_area varchar(64)

	// expectedAnnualLimit is the first value metering_points'
	// expected_annual_consumption_kwh numeric(14,3) cannot hold: eleven
	// digits before the point.
	expectedAnnualLimit = 1e11

	defaultPage     = int32(1)
	defaultPageSize = int32(25)
	maxPageSize     = int32(100)
)

// validateMeteringPointFields is the field validation MeteringPointRequest
// and MeteringPointUpdateRequest share (MeteringPointRequest.cs:17-38,
// :70-91): every check runs regardless of the others, each collected under
// its own field key. Create additionally validates meterNumber, which this
// helper does not touch — the caller adds that key itself.
func validateMeteringPointFields(gsrn *string, address *gen.AddressRequest, priceArea, gridArea *string,
	expectedAnnualConsumptionKwh *float64, latitude, longitude *float64, connectionStatus *string,
) map[string][]string {
	errs := map[string][]string{}
	if !isNullOrValidGsrn(gsrn) {
		errs["gsrn"] = []string{"GSRN must contain exactly 18 digits."}
	}
	validateAddressRequest(errs, address)
	if priceArea == nil || !priceAreaValid(*priceArea) {
		errs["priceArea"] = []string{"Price area must contain two uppercase letters followed by one or two digits."}
	}
	// The grid area is stored trimmed (trimmedOrNil), so the trimmed value is
	// what must fit its column.
	if gridArea != nil && utf16Length(strings.TrimSpace(*gridArea)) > gridAreaMaxLength {
		errs["gridArea"] = []string{fmt.Sprintf("Grid area cannot be longer than %d characters.", gridAreaMaxLength)}
	}
	switch {
	case expectedAnnualConsumptionKwh == nil:
	case *expectedAnnualConsumptionKwh < 0:
		errs["expectedAnnualConsumptionKwh"] = []string{"Expected annual consumption cannot be negative."}
	case roundKwh(*expectedAnnualConsumptionKwh) >= expectedAnnualLimit:
		// Compared as stored: the column rounds to three decimals, so a
		// value just under the limit can round onto it.
		errs["expectedAnnualConsumptionKwh"] = []string{"Expected annual consumption must be less than 100000000000."}
	}
	if msg := validateLocation(latitude, longitude); msg != "" {
		errs["location"] = []string{msg}
	}
	if connectionStatus != nil {
		if _, ok := parseConnectionStatus(connectionStatus); !ok {
			errs["connectionStatus"] = []string{"Connection status must be New, Connected or Disconnected."}
		}
	}
	return errs
}

// validateAddressRequest is MeteringPointRequest.Validate's address block
// (:22-30), shared verbatim by MeteringPointUpdateRequest (:74-82):
// address itself required, each of streetAddress/postalCode/city required
// and length-capped (the check against the *trimmed* value), and
// countryCode — only checked when present — exactly two letters, case not
// enforced at this layer (energy inventory §2.1: the domain would reject
// lowercase, but the DTO's own check does not).
func validateAddressRequest(errs map[string][]string, address *gen.AddressRequest) {
	if address == nil {
		errs["address"] = []string{"Address is required."}
		return
	}
	addAddressFieldError(errs, "streetAddress", address.StreetAddress, addressStreetMaxLength)
	addAddressFieldError(errs, "postalCode", address.PostalCode, addressPostalMaxLength)
	addAddressFieldError(errs, "city", address.City, addressCityMaxLength)
	if address.CountryCode != nil {
		cc := *address.CountryCode
		if utf16Length(cc) != 2 || !isAllLetters(cc) {
			errs["address.countryCode"] = []string{"Country code must contain two letters."}
		}
	}
}

func addAddressFieldError(errs map[string][]string, field string, value *string, maxLength int) {
	if value == nil || strings.TrimSpace(*value) == "" {
		errs["address."+field] = []string{"This field is required."}
		return
	}
	if utf16Length(strings.TrimSpace(*value)) > maxLength {
		errs["address."+field] = []string{fmt.Sprintf("This field cannot be longer than %d characters.", maxLength)}
	}
}

// validateLocation is MeteringPoint.ValidateLocation (MeteringPoint.cs:24-29):
// latitude is checked first, and a bad latitude and a bad longitude
// together still yield only latitude's message — both land under the
// single "location" field key.
func validateLocation(latitude, longitude *float64) string {
	if latitude != nil && (*latitude < -90 || *latitude > 90) {
		return "Latitude must be between -90 and 90."
	}
	if longitude != nil && (*longitude < -180 || *longitude > 180) {
		return "Longitude must be between -180 and 180."
	}
	return ""
}

// resolveCountryCode is Address.ValidateCountry as CreateMeteringPointEndpoint
// and UpdateMeteringPointEndpoint call it (ToDomain:44, UpdateMeteringPointEndpoint.cs:21):
// nil becomes "NO"; validateAddressRequest above has already guaranteed a
// non-nil value is exactly two letters, so upper-casing is all that is left
// to do.
func resolveCountryCode(cc *string) string {
	if cc == nil {
		return "NO"
	}
	return strings.ToUpper(strings.TrimSpace(*cc))
}

// trimmedOrNil is GridArea's "trimmed-or-null on write" rule (energy
// inventory §2.1, MeteringPointRequest.cs:46): blank or whitespace-only
// becomes nil, anything else is trimmed.
func trimmedOrNil(v *string) *string {
	if v == nil {
		return nil
	}
	t := strings.TrimSpace(*v)
	if t == "" {
		return nil
	}
	return &t
}

// meteringPointResponseOf builds the response for row; names holds the grid
// tariff's name (tariffNames), so a page of points reads them with one query.
// The VAT treatment is derived here, every time, never stored (vat.go).
func meteringPointResponseOf(row store.EnergyMeteringPoint, meterNumber *string, names map[int32]string) gen.MeteringPointResponse {
	household, taxClass := row.Household, row.ElectricityTaxClass
	vat := vatTreatment(row.Household, row.CountryCode, row.PostalCode)
	return gen.MeteringPointResponse{
		Id:                           row.ID,
		Gsrn:                         row.Gsrn,
		MeterNumber:                  meterNumber,
		Address:                      gen.AddressResponse{StreetAddress: row.StreetAddress, PostalCode: row.PostalCode, City: row.City, CountryCode: row.CountryCode},
		PriceArea:                    row.PriceArea,
		GridArea:                     row.GridArea,
		ExpectedAnnualConsumptionKwh: floatPtrFromNumeric(row.ExpectedAnnualConsumptionKwh),
		Latitude:                     row.Latitude,
		Longitude:                    row.Longitude,
		ConnectionStatus:             row.ConnectionStatus,
		CreatedAt:                    row.CreatedAt,
		UpdatedAt:                    row.UpdatedAt,
		GridTariffId:                 row.GridTariffID,
		GridTariffName:               tariffNameOf(names, row.GridTariffID),
		Household:                    &household,
		ElectricityTaxClass:          &taxClass,
		VatTreatment:                 &vat,
	}
}

// meteringPointOfListRow is a list row's point without its meter's number, so
// the list and the customer's points build their responses as the item does.
func meteringPointOfListRow(r store.ListMeteringPointsRow) store.EnergyMeteringPoint {
	return store.EnergyMeteringPoint{
		ID: r.ID, Gsrn: r.Gsrn, StreetAddress: r.StreetAddress, PostalCode: r.PostalCode, City: r.City,
		CountryCode: r.CountryCode, PriceArea: r.PriceArea, GridArea: r.GridArea,
		ExpectedAnnualConsumptionKwh: r.ExpectedAnnualConsumptionKwh, Latitude: r.Latitude, Longitude: r.Longitude,
		ConnectionStatus: r.ConnectionStatus, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		GridTariffID: r.GridTariffID, Household: r.Household, ElectricityTaxClass: r.ElectricityTaxClass,
	}
}

// validateBillingFacts checks the point's billing facts a create or a replace
// carries (energy consumption billing design D3, D4): the tax class's word and
// the grid tariff's assignability (tariffRefusal against tariff, read by
// shareTariff inside the write's transaction; current is the tariff the point
// holds now, nil on a create). It answers the class read and adds each refusal
// to errs under its field.
func validateBillingFacts(errs map[string][]string, tariff *store.GetTariffForShareRow, gridTariffID *int32, taxClass *string, current *int32) string {
	class, ok := parseTaxClass(taxClass)
	if !ok {
		errs["electricityTaxClass"] = []string{taxClassMessage}
	}
	if msg := tariffRefusal(gridTariffID, tariff, tariffKindGrid, current); msg != "" {
		errs["gridTariffId"] = []string{msg}
	}
	return class
}

// boolOrFalse is an optional request flag: absent is false.
func boolOrFalse(v *bool) bool {
	return v != nil && *v
}

// activeMeterNumber is MeteringPointResponse.FromDomain's
// `point.Meters.FirstOrDefault(meter => meter.RemovedAt is null)?.MeterNumber`
// (Dtos/MeteringPointResponse.cs:20): the current meter's number, or nil if
// somehow none exists.
func activeMeterNumber(ctx context.Context, q *store.Queries, meteringPointID int32) (*string, error) {
	meter, err := q.GetActiveMeter(ctx, meteringPointID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("energy: get active meter: %w", err)
	}
	return &meter.MeterNumber, nil
}

// meteringPointResponse builds a full MeteringPointResponse for row,
// reading its current meter's number with a follow-up query — there is no
// in-app tracked entity to read the already-loaded Meters collection from
// the way EF's SaveChangesAsync leaves one.
func meteringPointResponse(ctx context.Context, q *store.Queries, row store.EnergyMeteringPoint) (gen.MeteringPointResponse, error) {
	meterNumber, err := activeMeterNumber(ctx, q, row.ID)
	if err != nil {
		return gen.MeteringPointResponse{}, err
	}
	names, err := tariffNames(ctx, q, row.GridTariffID)
	if err != nil {
		return gen.MeteringPointResponse{}, err
	}
	return meteringPointResponseOf(row, meterNumber, names), nil
}

// GetEnergyMeteringPoints List metering points
// (GET /api/v1/energy/metering-points)
//
// GetMeteringPointsEndpoint.cs:15-32: page<1 or pageSize outside 1..100 is
// a plain (not field-keyed) 400 Problem; search matches the GSRN, the city,
// the street address or the current meter's number, ILIKE, escaped as
// EscapeLikePattern does (:35). withoutCustomer=true narrows it to the points
// without a customer now (energy import design D9).
func (s *server) GetEnergyMeteringPoints(ctx context.Context, req gen.GetEnergyMeteringPointsRequestObject) (gen.GetEnergyMeteringPointsResponseObject, error) {
	p := req.Params
	if (p.Page != nil && *p.Page < 1) || (p.PageSize != nil && (*p.PageSize < 1 || *p.PageSize > maxPageSize)) {
		return gen.GetEnergyMeteringPoints400ApplicationProblemPlusJSONResponse(apicommon.Problem(
			"Invalid query parameters", "Page must be at least 1 and pageSize must be between 1 and 100.")), nil
	}
	page, pageSize := defaultPage, defaultPageSize
	if p.Page != nil {
		page = *p.Page
	}
	if p.PageSize != nil {
		pageSize = *p.PageSize
	}

	var searchPattern *string
	if p.Search != nil && strings.TrimSpace(*p.Search) != "" {
		pattern := likePattern(strings.TrimSpace(*p.Search))
		searchPattern = &pattern
	}

	// withoutCustomer (energy import design D9): the points with no Active
	// supply period covering this instant.
	var withoutCustomerAt *time.Time
	if p.WithoutCustomer != nil && *p.WithoutCustomer {
		now := s.deps.Clock()
		withoutCustomerAt = &now
	}

	q := store.New(s.deps.Pool)
	total, err := q.CountMeteringPoints(ctx, store.CountMeteringPointsParams{SearchPattern: searchPattern, WithoutCustomerAt: withoutCustomerAt})
	if err != nil {
		return nil, fmt.Errorf("energy: count metering points: %w", err)
	}
	rows, err := q.ListMeteringPoints(ctx, store.ListMeteringPointsParams{
		SearchPattern: searchPattern, WithoutCustomerAt: withoutCustomerAt, RowOffset: (page - 1) * pageSize, PageSize: pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("energy: list metering points: %w", err)
	}

	gridTariffs := make([]*int32, 0, len(rows))
	for _, r := range rows {
		gridTariffs = append(gridTariffs, r.GridTariffID)
	}
	names, err := tariffNames(ctx, q, gridTariffs...)
	if err != nil {
		return nil, err
	}
	data := make([]gen.MeteringPointResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, meteringPointResponseOf(meteringPointOfListRow(r), r.ActiveMeterNumber, names))
	}
	return gen.GetEnergyMeteringPoints200JSONResponse(gen.PaginatedResponseOfMeteringPointResponse{
		Data: data,
		Pagination: gen.EnergyPaginationMetadata{
			Page: page, PageSize: pageSize, TotalCount: int32(total), TotalPages: apicommon.TotalPages(int32(total), pageSize),
		},
	}), nil
}

// PostEnergyMeteringPoints Create a metering point
// (POST /api/v1/energy/metering-points)
//
// CreateMeteringPointEndpoint.cs:16-25: field validation (including the
// initial meter's number and the billing facts) -> duplicate-GSRN pre-check
// (409) -> insert the metering point and its first meter together, in one
// transaction. The grid tariff is judged inside that transaction, read FOR
// SHARE first (energy tariffs plan, reading 5), so a racing archive or delete
// is seen; a foreign-key violation on it answers the same 400 "does not exist".
func (s *server) PostEnergyMeteringPoints(ctx context.Context, req gen.PostEnergyMeteringPointsRequestObject) (gen.PostEnergyMeteringPointsResponseObject, error) {
	body := gen.MeteringPointRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	errs := validateMeteringPointFields(body.Gsrn, body.Address, body.PriceArea, body.GridArea,
		body.ExpectedAnnualConsumptionKwh, body.Latitude, body.Longitude, body.ConnectionStatus)
	if msg := validateMeterNumber(body.MeterNumber); msg != "" {
		errs["meterNumber"] = []string{msg}
	}
	var created store.EnergyMeteringPoint
	refused, duplicate := false, false
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		tariff, terr := shareTariff(ctx, txq, body.GridTariffId)
		if terr != nil {
			return terr
		}
		taxClass := validateBillingFacts(errs, tariff, body.GridTariffId, body.ElectricityTaxClass, nil)
		// A refusal is the outcome, not an error: nothing is written.
		if len(errs) > 0 {
			refused = true
			return nil
		}
		exists, gerr := txq.GsrnExists(ctx, *body.Gsrn)
		if gerr != nil {
			return fmt.Errorf("check gsrn: %w", gerr)
		}
		if exists {
			duplicate = true
			return nil
		}
		connStatus, _ := parseConnectionStatus(body.ConnectionStatus)
		expected, nerr := numericFromFloatPtr(body.ExpectedAnnualConsumptionKwh)
		if nerr != nil {
			return nerr
		}
		var ierr error
		created, ierr = insertMeteringPoint(ctx, txq, store.InsertMeteringPointParams{
			Gsrn:                         *body.Gsrn,
			StreetAddress:                strings.TrimSpace(*body.Address.StreetAddress),
			PostalCode:                   strings.TrimSpace(*body.Address.PostalCode),
			City:                         strings.TrimSpace(*body.Address.City),
			CountryCode:                  resolveCountryCode(body.Address.CountryCode),
			PriceArea:                    *body.PriceArea,
			GridArea:                     trimmedOrNil(body.GridArea),
			ExpectedAnnualConsumptionKwh: expected,
			Latitude:                     body.Latitude,
			Longitude:                    body.Longitude,
			ConnectionStatus:             connStatus,
			Now:                          s.deps.Clock(),
			GridTariffID:                 body.GridTariffId,
			Household:                    boolOrFalse(body.Household),
			ElectricityTaxClass:          taxClass,
		}, strings.TrimSpace(*body.MeterNumber))
		return ierr
	})
	if fk := tariffFKRefusal(err, meteringPointGridFK, "gridTariffId", body.GridTariffId); fk != nil {
		errs, refused, err = fk, true, nil
	}
	switch {
	case err != nil:
		return nil, fmt.Errorf("energy: create metering point: %w", err)
	case refused:
		return gen.PostEnergyMeteringPoints400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid metering point", errs)), nil
	case duplicate:
		return gen.PostEnergyMeteringPoints409ApplicationProblemPlusJSONResponse(apicommon.ProblemStatus(
			"Duplicate GSRN", "A metering point with that GSRN already exists.", http.StatusConflict)), nil
	}

	q := store.New(s.deps.Pool)
	resp, err := meteringPointResponse(ctx, q, created)
	if err != nil {
		return nil, err
	}
	return gen.PostEnergyMeteringPoints201JSONResponse(resp), nil
}

// insertMeteringPoint is the create's write, inside the caller's
// transaction: the metering point and its first meter, installed at the
// point's own creation instant, together. POST /metering-points and the
// import (import.go) both write a new point through it.
func insertMeteringPoint(ctx context.Context, txq *store.Queries, params store.InsertMeteringPointParams, meterNumber string) (store.EnergyMeteringPoint, error) {
	created, err := txq.InsertMeteringPoint(ctx, params)
	if err != nil {
		return store.EnergyMeteringPoint{}, err
	}
	if _, err := txq.InsertMeter(ctx, store.InsertMeterParams{
		MeteringPointID: created.ID, MeterNumber: meterNumber, InstalledAt: params.Now,
	}); err != nil {
		return store.EnergyMeteringPoint{}, err
	}
	return created, nil
}

// GetEnergyMeteringPoint Get a metering point
// (GET /api/v1/energy/metering-points/{id})
func (s *server) GetEnergyMeteringPoint(ctx context.Context, req gen.GetEnergyMeteringPointRequestObject) (gen.GetEnergyMeteringPointResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetMeteringPointByID(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetEnergyMeteringPoint404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("energy: get metering point: %w", err)
	}
	resp, err := meteringPointResponse(ctx, q, row)
	if err != nil {
		return nil, err
	}
	return gen.GetEnergyMeteringPoint200JSONResponse(resp), nil
}

// PutEnergyMeteringPointsById Update a metering point
// (PUT /api/v1/energy/metering-points/{id})
//
// UpdateMeteringPointEndpoint.cs:14-31: field validation *before* the
// existence check (opposite of ReplaceMeter's order, energy inventory §1.1
// line 32/§8 oddity 3) -> existence (404) -> the billing facts (400, energy
// tariffs plan reading 7: absent means no grid tariff, not a household,
// ordinary) -> duplicate-GSRN pre-check excluding self (409) -> apply. Meters
// are never touched here. Everything after the field validation is one
// transaction: the grid tariff read FOR SHARE (reading 5), then the point read
// FOR NO KEY UPDATE, so the tariff is judged against the one the point holds
// as it is written and a racing archive or delete is seen; a foreign-key
// violation on the tariff answers the same 400 "does not exist".
func (s *server) PutEnergyMeteringPointsById(ctx context.Context, req gen.PutEnergyMeteringPointsByIdRequestObject) (gen.PutEnergyMeteringPointsByIdResponseObject, error) {
	body := gen.MeteringPointUpdateRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	errs := validateMeteringPointFields(body.Gsrn, body.Address, body.PriceArea, body.GridArea,
		body.ExpectedAnnualConsumptionKwh, body.Latitude, body.Longitude, body.ConnectionStatus)
	if len(errs) > 0 {
		return gen.PutEnergyMeteringPointsById400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid metering point", errs)), nil
	}

	var updated store.EnergyMeteringPoint
	notFound, refused, duplicate := false, false, false
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		// The tariff before the point's row, as every assignment takes them
		// (shareTariff).
		tariff, terr := shareTariff(ctx, txq, body.GridTariffId)
		if terr != nil {
			return terr
		}
		stored, gerr := txq.GetMeteringPointByIDForUpdate(ctx, req.Id)
		if errors.Is(gerr, pgx.ErrNoRows) {
			notFound = true
			return nil
		} else if gerr != nil {
			return fmt.Errorf("get metering point: %w", gerr)
		}
		// The billing facts are judged after the existence check: whether the
		// grid tariff may be written depends on the one the point holds.
		taxClass := validateBillingFacts(errs, tariff, body.GridTariffId, body.ElectricityTaxClass, stored.GridTariffID)
		if len(errs) > 0 {
			refused = true
			return nil
		}
		dupExists, derr := txq.GsrnExistsExcludingID(ctx, store.GsrnExistsExcludingIDParams{ID: req.Id, Gsrn: *body.Gsrn})
		if derr != nil {
			return fmt.Errorf("check gsrn: %w", derr)
		}
		if dupExists {
			duplicate = true
			return nil
		}
		connStatus, _ := parseConnectionStatus(body.ConnectionStatus)
		expected, nerr := numericFromFloatPtr(body.ExpectedAnnualConsumptionKwh)
		if nerr != nil {
			return nerr
		}
		var uerr error
		updated, uerr = txq.UpdateMeteringPoint(ctx, store.UpdateMeteringPointParams{
			ID:                           req.Id,
			Gsrn:                         *body.Gsrn,
			StreetAddress:                strings.TrimSpace(*body.Address.StreetAddress),
			PostalCode:                   strings.TrimSpace(*body.Address.PostalCode),
			City:                         strings.TrimSpace(*body.Address.City),
			CountryCode:                  resolveCountryCode(body.Address.CountryCode),
			PriceArea:                    *body.PriceArea,
			GridArea:                     trimmedOrNil(body.GridArea),
			ExpectedAnnualConsumptionKwh: expected,
			Latitude:                     body.Latitude,
			Longitude:                    body.Longitude,
			ConnectionStatus:             connStatus,
			UpdatedAt:                    s.deps.Clock(),
			GridTariffID:                 body.GridTariffId,
			Household:                    boolOrFalse(body.Household),
			ElectricityTaxClass:          taxClass,
		})
		return uerr
	})
	if fk := tariffFKRefusal(err, meteringPointGridFK, "gridTariffId", body.GridTariffId); fk != nil {
		errs, refused, err = fk, true, nil
	}
	switch {
	case err != nil:
		return nil, fmt.Errorf("energy: update metering point: %w", err)
	case notFound:
		return gen.PutEnergyMeteringPointsById404Response{}, nil
	case refused:
		return gen.PutEnergyMeteringPointsById400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid metering point", errs)), nil
	case duplicate:
		return gen.PutEnergyMeteringPointsById409ApplicationProblemPlusJSONResponse(apicommon.ProblemStatus(
			"Duplicate GSRN", "A metering point with that GSRN already exists.", http.StatusConflict)), nil
	}

	q := store.New(s.deps.Pool)
	resp, err := meteringPointResponse(ctx, q, updated)
	if err != nil {
		return nil, err
	}
	return gen.PutEnergyMeteringPointsById200JSONResponse(resp), nil
}
