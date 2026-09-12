package energy

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

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

	defaultPage     = int32(1)
	defaultPageSize = int32(25)
	maxPageSize     = int32(100)
)

// validateMeteringPointFields is the field validation MeteringPointRequest
// and MeteringPointUpdateRequest share (MeteringPointRequest.cs:17-38,
// :70-91): every check runs regardless of the others, each collected under
// its own field key. Create additionally validates meterNumber, which this
// helper does not touch — the caller adds that key itself.
func validateMeteringPointFields(gsrn *string, address *gen.AddressRequest, priceArea *string,
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
	if expectedAnnualConsumptionKwh != nil && *expectedAnnualConsumptionKwh < 0 {
		errs["expectedAnnualConsumptionKwh"] = []string{"Expected annual consumption cannot be negative."}
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

func meteringPointResponseOf(row store.EnergyMeteringPoint, meterNumber *string) gen.MeteringPointResponse {
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
	}
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
	return meteringPointResponseOf(row, meterNumber), nil
}

// totalPagesOf is PaginationMetadata.Create's ceiling division
// (Endpoints/Dtos/PaginationMetadata.cs:5-6).
func totalPagesOf(totalCount, pageSize int32) int32 {
	if pageSize <= 0 {
		return 0
	}
	return int32(math.Ceil(float64(totalCount) / float64(pageSize)))
}

// GetEnergyMeteringPoints List metering points
// (GET /api/v1/energy/metering-points)
//
// GetMeteringPointsEndpoint.cs:15-32: page<1 or pageSize outside 1..100 is
// a plain (not field-keyed) 400 Problem; search matches the GSRN, the city,
// the street address or the current meter's number, ILIKE, escaped as
// EscapeLikePattern does (:35).
func (s *server) GetEnergyMeteringPoints(ctx context.Context, req gen.GetEnergyMeteringPointsRequestObject) (gen.GetEnergyMeteringPointsResponseObject, error) {
	p := req.Params
	if (p.Page != nil && *p.Page < 1) || (p.PageSize != nil && (*p.PageSize < 1 || *p.PageSize > maxPageSize)) {
		return gen.GetEnergyMeteringPoints400ApplicationProblemPlusJSONResponse(problem(
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

	q := store.New(s.deps.Pool)
	total, err := q.CountMeteringPoints(ctx, searchPattern)
	if err != nil {
		return nil, fmt.Errorf("energy: count metering points: %w", err)
	}
	rows, err := q.ListMeteringPoints(ctx, store.ListMeteringPointsParams{
		SearchPattern: searchPattern, RowOffset: (page - 1) * pageSize, PageSize: pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("energy: list metering points: %w", err)
	}

	data := make([]gen.MeteringPointResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, gen.MeteringPointResponse{
			Id:                           r.ID,
			Gsrn:                         r.Gsrn,
			MeterNumber:                  r.ActiveMeterNumber,
			Address:                      gen.AddressResponse{StreetAddress: r.StreetAddress, PostalCode: r.PostalCode, City: r.City, CountryCode: r.CountryCode},
			PriceArea:                    r.PriceArea,
			GridArea:                     r.GridArea,
			ExpectedAnnualConsumptionKwh: floatPtrFromNumeric(r.ExpectedAnnualConsumptionKwh),
			Latitude:                     r.Latitude,
			Longitude:                    r.Longitude,
			ConnectionStatus:             r.ConnectionStatus,
			CreatedAt:                    r.CreatedAt,
			UpdatedAt:                    r.UpdatedAt,
		})
	}
	return gen.GetEnergyMeteringPoints200JSONResponse(gen.PaginatedResponseOfMeteringPointResponse{
		Data: data,
		Pagination: gen.EnergyPaginationMetadata{
			Page: page, PageSize: pageSize, TotalCount: int32(total), TotalPages: totalPagesOf(int32(total), pageSize),
		},
	}), nil
}

// PostEnergyMeteringPoints Create a metering point
// (POST /api/v1/energy/metering-points)
//
// CreateMeteringPointEndpoint.cs:16-25: field validation (including the
// initial meter's number) -> duplicate-GSRN pre-check (409) -> insert the
// metering point and its first meter together, in one transaction.
func (s *server) PostEnergyMeteringPoints(ctx context.Context, req gen.PostEnergyMeteringPointsRequestObject) (gen.PostEnergyMeteringPointsResponseObject, error) {
	body := gen.MeteringPointRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	errs := validateMeteringPointFields(body.Gsrn, body.Address, body.PriceArea,
		body.ExpectedAnnualConsumptionKwh, body.Latitude, body.Longitude, body.ConnectionStatus)
	if msg := validateMeterNumber(body.MeterNumber); msg != "" {
		errs["meterNumber"] = []string{msg}
	}
	if len(errs) > 0 {
		return gen.PostEnergyMeteringPoints400ApplicationProblemPlusJSONResponse(validationProblem("Invalid metering point", errs)), nil
	}

	q := store.New(s.deps.Pool)
	exists, err := q.GsrnExists(ctx, *body.Gsrn)
	if err != nil {
		return nil, fmt.Errorf("energy: check gsrn: %w", err)
	}
	if exists {
		return gen.PostEnergyMeteringPoints409ApplicationProblemPlusJSONResponse(problemStatus(
			"Duplicate GSRN", "A metering point with that GSRN already exists.", http.StatusConflict)), nil
	}

	now := s.deps.Clock()
	connStatus, _ := parseConnectionStatus(body.ConnectionStatus)
	expected, err := numericFromFloatPtr(body.ExpectedAnnualConsumptionKwh)
	if err != nil {
		return nil, fmt.Errorf("energy: create metering point: %w", err)
	}
	var created store.EnergyMeteringPoint
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var ierr error
		created, ierr = txq.InsertMeteringPoint(ctx, store.InsertMeteringPointParams{
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
			Now:                          now,
		})
		if ierr != nil {
			return ierr
		}
		_, ierr = txq.InsertMeter(ctx, store.InsertMeterParams{
			MeteringPointID: created.ID, MeterNumber: strings.TrimSpace(*body.MeterNumber), InstalledAt: now,
		})
		return ierr
	})
	if err != nil {
		return nil, fmt.Errorf("energy: create metering point: %w", err)
	}

	resp, err := meteringPointResponse(ctx, q, created)
	if err != nil {
		return nil, err
	}
	return gen.PostEnergyMeteringPoints201JSONResponse(resp), nil
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
// line 32/§8 oddity 3) -> existence (404) -> duplicate-GSRN pre-check
// excluding self (409) -> apply. Meters are never touched here.
func (s *server) PutEnergyMeteringPointsById(ctx context.Context, req gen.PutEnergyMeteringPointsByIdRequestObject) (gen.PutEnergyMeteringPointsByIdResponseObject, error) {
	body := gen.MeteringPointUpdateRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	errs := validateMeteringPointFields(body.Gsrn, body.Address, body.PriceArea,
		body.ExpectedAnnualConsumptionKwh, body.Latitude, body.Longitude, body.ConnectionStatus)
	if len(errs) > 0 {
		return gen.PutEnergyMeteringPointsById400ApplicationProblemPlusJSONResponse(validationProblem("Invalid metering point", errs)), nil
	}

	q := store.New(s.deps.Pool)
	if _, err := q.GetMeteringPointByID(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return gen.PutEnergyMeteringPointsById404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("energy: get metering point: %w", err)
	}

	dupExists, err := q.GsrnExistsExcludingID(ctx, store.GsrnExistsExcludingIDParams{ID: req.Id, Gsrn: *body.Gsrn})
	if err != nil {
		return nil, fmt.Errorf("energy: check gsrn: %w", err)
	}
	if dupExists {
		return gen.PutEnergyMeteringPointsById409ApplicationProblemPlusJSONResponse(problemStatus(
			"Duplicate GSRN", "A metering point with that GSRN already exists.", http.StatusConflict)), nil
	}

	connStatus, _ := parseConnectionStatus(body.ConnectionStatus)
	expected, err := numericFromFloatPtr(body.ExpectedAnnualConsumptionKwh)
	if err != nil {
		return nil, fmt.Errorf("energy: update metering point: %w", err)
	}
	updated, err := q.UpdateMeteringPoint(ctx, store.UpdateMeteringPointParams{
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
	})
	if err != nil {
		return nil, fmt.Errorf("energy: update metering point: %w", err)
	}

	resp, err := meteringPointResponse(ctx, q, updated)
	if err != nil {
		return nil, err
	}
	return gen.PutEnergyMeteringPointsById200JSONResponse(resp), nil
}
