package customers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is the Customers area's CRUD (EP/CustomersEndpoints.cs's bare
// group): getCustomers, postCustomers, getCustomer, putCustomersById and
// deleteCustomersById. stats.go holds the four dashboard operations mounted
// alongside them; contacts, legal identity, lookup and timeline are later
// tasks (unimplemented.go's remaining stubs).

// customerRow is the shape GetCustomer, ListCustomersByID and
// ListCustomersByName all reduce to before building a SafeCustomerResponse:
// one seam so safeCustomerResponse only has to know one shape, whichever
// sqlc-generated row it came from.
type customerRow struct {
	ID               int32
	CustomerNumber   int64
	Name             string
	Status           string
	LegalCountry     *string
	LegalID          *string
	LegalName        *string
	LegalSource      *string
	LegalType        *string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	EntryCount       int64
	LatestOccurredOn pgtype.Date
}

func fromCustomerRow(c store.CustomersCustomer, ts store.CustomerTimelineSummaryRow) customerRow {
	return customerRow{
		ID: c.ID, CustomerNumber: c.CustomerNumber, Name: c.Name, Status: c.Status,
		LegalCountry: c.LegalCountry, LegalID: c.LegalID, LegalName: c.LegalName, LegalSource: c.LegalSource, LegalType: c.LegalType,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		EntryCount: ts.EntryCount, LatestOccurredOn: ts.LatestOccurredOn,
	}
}

func fromListByIDRow(r store.ListCustomersByIDRow) customerRow {
	return customerRow{
		ID: r.ID, CustomerNumber: r.CustomerNumber, Name: r.Name, Status: r.Status,
		LegalCountry: r.LegalCountry, LegalID: r.LegalID, LegalName: r.LegalName, LegalSource: r.LegalSource, LegalType: r.LegalType,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		EntryCount: r.EntryCount, LatestOccurredOn: r.LatestOccurredOn,
	}
}

func fromListByNameRow(r store.ListCustomersByNameRow) customerRow {
	return customerRow{
		ID: r.ID, CustomerNumber: r.CustomerNumber, Name: r.Name, Status: r.Status,
		LegalCountry: r.LegalCountry, LegalID: r.LegalID, LegalName: r.LegalName, LegalSource: r.LegalSource, LegalType: r.LegalType,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		EntryCount: r.EntryCount, LatestOccurredOn: r.LatestOccurredOn,
	}
}

// identityFromRow is a persisted customer row's legal identity, nil when
// the row carries none (GetCustomer/UpdateCustomerEndpoint read
// customer.Identity the same way: all five columns null together, or all
// five set).
func identityFromRow(legalCountry, legalID, legalName, legalSource, legalType *string) *legalIdentity {
	if legalCountry == nil {
		return nil
	}
	return &legalIdentity{
		Country: deref(legalCountry), ID: deref(legalID), Name: deref(legalName),
		Source: deref(legalSource), Type: deref(legalType),
	}
}

// legalColumns is a *legalIdentity's five persisted columns: all nil for no
// identity, all set otherwise (inventory §2.1's owned-type invariant).
func legalColumns(identity *legalIdentity) (country, id, name, source, typ *string) {
	if identity == nil {
		return nil, nil, nil, nil, nil
	}
	return &identity.Country, &identity.ID, &identity.Name, &identity.Source, &identity.Type
}

// dateFromPgtype converts a nullable SQL date into the contract's date type,
// nil when the column was NULL.
func dateFromPgtype(d pgtype.Date) *openapi_types.Date {
	if !d.Valid {
		return nil
	}
	return &openapi_types.Date{Time: d.Time}
}

// safeCustomerResponse is SafeCustomerResponse's projection
// (GetCustomerEndpoint.cs:54-75, GetCustomersEndpoint.cs:82-103,
// UpdateCustomerEndpoint.cs:111-129): the legal-identity sub-object is only
// populated when the caller holds legal-identity-view (inventory §6).
func safeCustomerResponse(row customerRow, includeIdentity bool) gen.SafeCustomerResponse {
	resp := gen.SafeCustomerResponse{
		Id:             row.ID,
		CustomerNumber: row.CustomerNumber,
		Name:           row.Name,
		Status:         row.Status,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
		TimelineSummary: gen.SafeTimelineSummary{
			EntryCount:       int32(row.EntryCount),
			LatestOccurredOn: dateFromPgtype(row.LatestOccurredOn),
		},
	}
	if includeIdentity && row.LegalCountry != nil {
		resp.Identity = &gen.SafeCustomerIdentity{Country: *row.LegalCountry, Type: *row.LegalType, Id: *row.LegalID}
	}
	return resp
}

// likeReplacer escapes ILIKE's special characters as .NET's
// EscapeLikePattern does (GetCustomersEndpoint.cs:160-163): backslash
// first, so escaping % and _ never doubles the backslashes it just
// introduced.
var likeReplacer = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func likePattern(search string) string {
	return "%" + likeReplacer.Replace(search) + "%"
}

// validateGetCustomersParams is GetCustomersEndpoint.Validate
// (GetCustomersEndpoint.cs:112-145): every check runs regardless of the
// others, and every failure's message is collected, to be joined with a
// single space into one ProblemDetails.Detail (.NET's
// string.Join(" ", errors)).
func validateGetCustomersParams(p gen.GetCustomersParams) []string {
	var errs []string
	if p.Page != nil && *p.Page < 1 {
		errs = append(errs, fmt.Sprintf("'page' must be 1 or greater, but was %d.", *p.Page))
	}
	if p.PageSize != nil && (*p.PageSize < 1 || *p.PageSize > 100) {
		errs = append(errs, fmt.Sprintf("'pageSize' must be between 1 and 100, but was %d.", *p.PageSize))
	}
	if p.SortBy != nil && *p.SortBy != "id" && *p.SortBy != "name" {
		errs = append(errs, fmt.Sprintf("'sortBy' must be one of 'id' or 'name', but was '%s'.", *p.SortBy))
	}
	if p.SortDirection != nil && *p.SortDirection != "asc" && *p.SortDirection != "desc" {
		errs = append(errs, fmt.Sprintf("'sortDirection' must be one of 'asc' or 'desc', but was '%s'.", *p.SortDirection))
	}
	return errs
}

// GetCustomers List all customers
// (GET /api/v1/customers)
func (s *server) GetCustomers(ctx context.Context, req gen.GetCustomersRequestObject) (gen.GetCustomersResponseObject, error) {
	if msgs := validateGetCustomersParams(req.Params); len(msgs) > 0 {
		return gen.GetCustomers400ApplicationProblemPlusJSONResponse(problem("Invalid query parameters", strings.Join(msgs, " "))), nil
	}

	page := int32(1)
	if req.Params.Page != nil {
		page = *req.Params.Page
	}
	pageSize := int32(25)
	if req.Params.PageSize != nil {
		pageSize = *req.Params.PageSize
	}
	includeArchived := req.Params.IncludeArchived != nil && *req.Params.IncludeArchived
	var search *string
	if req.Params.Search != nil {
		if trimmed := strings.TrimSpace(*req.Params.Search); trimmed != "" {
			p := likePattern(trimmed)
			search = &p
		}
	}
	descending := req.Params.SortDirection != nil && *req.Params.SortDirection == "desc"
	sortByName := req.Params.SortBy != nil && *req.Params.SortBy == "name"

	q := store.New(s.deps.Pool)
	total, err := q.CountCustomers(ctx, store.CountCustomersParams{IncludeArchived: includeArchived, Search: search})
	if err != nil {
		return nil, fmt.Errorf("customers: count customers: %w", err)
	}

	includeIdentity := s.hasPermission(ctx, legalIdentityView)
	offset := (page - 1) * pageSize
	rows := make([]customerRow, 0)
	if sortByName {
		list, err := q.ListCustomersByName(ctx, store.ListCustomersByNameParams{
			IncludeArchived: includeArchived, Search: search, Descending: descending, PageSize: pageSize, RowOffset: offset,
		})
		if err != nil {
			return nil, fmt.Errorf("customers: list customers: %w", err)
		}
		for _, r := range list {
			rows = append(rows, fromListByNameRow(r))
		}
	} else {
		list, err := q.ListCustomersByID(ctx, store.ListCustomersByIDParams{
			IncludeArchived: includeArchived, Search: search, Descending: descending, PageSize: pageSize, RowOffset: offset,
		})
		if err != nil {
			return nil, fmt.Errorf("customers: list customers: %w", err)
		}
		for _, r := range list {
			rows = append(rows, fromListByIDRow(r))
		}
	}

	data := make([]gen.SafeCustomerResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, safeCustomerResponse(r, includeIdentity))
	}

	return gen.GetCustomers200JSONResponse{
		Data:       data,
		Pagination: paginationMetadata(page, pageSize, int32(total)),
	}, nil
}

// PostCustomers Create a new customer
// (POST /api/v1/customers)
//
// Ordering follows CreateCustomerEndpoint.cs:24-82 (inventory §1.4): (1) if
// identity is present and the caller lacks legal-identity-manage, 403
// immediately, before any field validation; (2) name, status and identity
// are then all validated and every error collected before any of them
// short-circuits the others. There is no existence check on create. The
// legal-identity-manage gate is an additional permission the router cannot
// enforce — x-vantigo-access for postCustomers is the flat
// permission:customers:create, since whether identity is required at all
// depends on the request body, which the router never inspects — so the
// handler is the only place it can live, the same shape as Task 11's
// conditional pricing permission (Global Constraints, "Contract-driven
// access"). It reuses hasPermission/requestFrom, never a second mechanism.
//
// The 201's Location header is faithful, not invented: CreateCustomerEndpoint.cs:106-109
// returns TypedResults.CreatedAtRoute, whose entire purpose (distinct from
// a plain Created/Ok result) is setting Location to the named route's URI —
// so customers.yaml now declares it and oapi-codegen generates
// PostCustomers201ResponseHeaders for it, rather than a hand-written
// response type.
func (s *server) PostCustomers(ctx context.Context, req gen.PostCustomersRequestObject) (gen.PostCustomersResponseObject, error) {
	body := gen.CreateCustomerRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	if body.Identity != nil && !s.hasPermission(ctx, legalIdentityManage) {
		return gen.PostCustomers403JSONResponse(forbiddenBody()), nil
	}

	errs := map[string][]string{}

	name, nameErr := validateFriendlyName(body.Name)
	if nameErr != "" {
		errs["name"] = []string{nameErr}
	}

	status := "active"
	if body.Status != nil {
		st, stErr := validateCustomerStatus(*body.Status)
		if stErr != "" {
			errs["status"] = []string{stErr}
		} else {
			status = st
		}
	}

	var identity *legalIdentity
	if body.Identity != nil {
		id, idErrs := validateLegalIdentity(body.Identity.Country, body.Identity.Type, body.Identity.Id, body.Identity.Name, body.Identity.Source)
		if idErrs != nil {
			for field, msgs := range idErrs {
				errs["identity."+field] = msgs
			}
		} else {
			identity = &id
		}
	}

	if len(errs) > 0 {
		return gen.PostCustomers400ApplicationProblemPlusJSONResponse(validationProblem("Invalid customer", errs)), nil
	}

	now := s.deps.Clock()
	legalCountry, legalID, legalName, legalSource, legalType := legalColumns(identity)
	var created store.CustomersCustomer
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		number, err := txq.NextCounterValue(ctx, "customer-number")
		if err != nil {
			return err
		}
		created, err = txq.InsertCustomer(ctx, store.InsertCustomerParams{
			CustomerNumber: number,
			Name:           name,
			Status:         status,
			LegalCountry:   legalCountry,
			LegalID:        legalID,
			LegalName:      legalName,
			LegalSource:    legalSource,
			LegalType:      legalType,
			Now:            now,
		})
		if err != nil {
			return err
		}
		return recordCustomerCreated(ctx, txq, now, created.ID, name, identity)
	})
	if err != nil {
		return nil, fmt.Errorf("customers: create customer: %w", err)
	}

	location := fmt.Sprintf("%s/api/v1/customers/%d", s.deps.Config.BasePath, created.ID)
	return gen.PostCustomers201JSONResponse{
		Body:    gen.CreateCustomerResponse{Id: created.ID, CustomerNumber: created.CustomerNumber},
		Headers: gen.PostCustomers201ResponseHeaders{Location: &location},
	}, nil
}

// GetCustomer Get a customer by id
// (GET /api/v1/customers/{id})
func (s *server) GetCustomer(ctx context.Context, req gen.GetCustomerRequestObject) (gen.GetCustomerResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetCustomer(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetCustomer404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get customer: %w", err)
	}
	summary, err := q.CustomerTimelineSummary(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("customers: timeline summary: %w", err)
	}
	includeIdentity := s.hasPermission(ctx, legalIdentityView)
	return gen.GetCustomer200JSONResponse(safeCustomerResponse(fromCustomerRow(row, summary), includeIdentity)), nil
}

// PutCustomersById Update a customer
// (PUT /api/v1/customers/{id})
//
// Ordering follows UpdateCustomerEndpoint.cs:23-129 (inventory §1.4): (1)
// the same legal-identity-manage 403 gate as PostCustomers, first, ahead of
// everything else — before field validation, before the 404 lookup, and
// before the post-404 identity re-validation; (2) name and status are
// validated next, together, before the customer lookup; (3) the lookup
// itself, 404 if missing; (4) only once the customer is found is the
// identity re-validated, as its own ValidationProblem — so an invalid name
// against a missing id answers 400 (validation wins) while an invalid
// identity against a missing id answers 404 (existence wins), but identity
// supplied without legal-identity-manage against a missing id answers 403
// (the permission gate wins over both).
//
// When the request omits identity, the persisted identity is left
// unchanged: UpdateCustomerEndpoint.cs:71 seeds customerIdentity from
// customer.Identity and only overwrites it when request.Identity is
// present (:72-87). The endpoint's own doc comment claims omitting it
// "removes" the identity, but no .NET test exercises PUT-with-no-identity
// against a customer that has one (the test named for that,
// UpdateCustomer_WithoutIdentity_RemovesExistingIdentity, actually calls
// DELETE .../legal-identity, a Task 8 operation) — the code, not the
// comment, is the port's ground truth here.
func (s *server) PutCustomersById(ctx context.Context, req gen.PutCustomersByIdRequestObject) (gen.PutCustomersByIdResponseObject, error) {
	body := gen.UpdateCustomerRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	if body.Identity != nil && !s.hasPermission(ctx, legalIdentityManage) {
		return gen.PutCustomersById403JSONResponse(forbiddenBody()), nil
	}
	errs := map[string][]string{}

	name, nameErr := validateFriendlyName(body.Name)
	if nameErr != "" {
		errs["name"] = []string{nameErr}
	}

	var status string
	hasStatus := false
	if body.Status != nil {
		st, stErr := validateCustomerStatus(*body.Status)
		if stErr != "" {
			errs["status"] = []string{stErr}
		} else {
			status, hasStatus = st, true
		}
	}

	if len(errs) > 0 {
		return gen.PutCustomersById400ApplicationProblemPlusJSONResponse(validationProblem("Invalid customer", errs)), nil
	}

	q := store.New(s.deps.Pool)
	existing, err := q.GetCustomer(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCustomersById404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get customer: %w", err)
	}

	beforeIdentity := identityFromRow(existing.LegalCountry, existing.LegalID, existing.LegalName, existing.LegalSource, existing.LegalType)
	afterIdentity := beforeIdentity
	if body.Identity != nil {
		parsed, idErrs := validateLegalIdentity(body.Identity.Country, body.Identity.Type, body.Identity.Id, body.Identity.Name, body.Identity.Source)
		if idErrs != nil {
			fieldErrs := make(map[string][]string, len(idErrs))
			for field, msgs := range idErrs {
				fieldErrs["identity."+field] = msgs
			}
			return gen.PutCustomersById400ApplicationProblemPlusJSONResponse(validationProblem("Invalid customer", fieldErrs)), nil
		}
		afterIdentity = &parsed
	}

	finalStatus := existing.Status
	if hasStatus {
		finalStatus = status
	}
	changed := existing.Name != name || !identityEqual(beforeIdentity, afterIdentity)
	statusChanged := finalStatus != existing.Status

	now := s.deps.Clock()
	updatedAt := existing.UpdatedAt
	if changed || statusChanged {
		updatedAt = now
	}

	legalCountry, legalID, legalName, legalSource, legalType := legalColumns(afterIdentity)
	var updated store.CustomersCustomer
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var err error
		updated, err = txq.UpdateCustomer(ctx, store.UpdateCustomerParams{
			ID: req.Id, Name: name, Status: finalStatus,
			LegalCountry: legalCountry, LegalID: legalID, LegalName: legalName, LegalSource: legalSource, LegalType: legalType,
			UpdatedAt: updatedAt,
		})
		if err != nil {
			return err
		}
		if changed {
			if err := recordCustomerUpdated(ctx, txq, now, req.Id, existing.Name, beforeIdentity, name, afterIdentity); err != nil {
				return err
			}
		}
		if statusChanged {
			if err := recordCustomerStatusChanged(ctx, txq, now, req.Id, existing.Status, finalStatus); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("customers: update customer: %w", err)
	}

	summary, err := q.CustomerTimelineSummary(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("customers: timeline summary: %w", err)
	}
	includeIdentity := s.hasPermission(ctx, legalIdentityView)
	return gen.PutCustomersById200JSONResponse(safeCustomerResponse(fromCustomerRow(updated, summary), includeIdentity)), nil
}

// DeleteCustomersById Archive a customer (customers are never hard-deleted)
// (DELETE /api/v1/customers/{id})
//
// Archives, never deletes (DeleteCustomerEndpoint.cs): idempotent by
// construction, not by concurrency control (inventory §4) — a customer
// already archived is left untouched and no timeline event is emitted a
// second time.
func (s *server) DeleteCustomersById(ctx context.Context, req gen.DeleteCustomersByIdRequestObject) (gen.DeleteCustomersByIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	existing, err := q.GetCustomer(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.DeleteCustomersById404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get customer: %w", err)
	}
	if existing.Status == "archived" {
		return gen.DeleteCustomersById204Response{}, nil
	}

	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if _, err := txq.SetCustomerStatus(ctx, store.SetCustomerStatusParams{ID: req.Id, Status: "archived", Now: now}); err != nil {
			return err
		}
		return recordCustomerStatusChanged(ctx, txq, now, req.Id, existing.Status, "archived")
	})
	if err != nil {
		return nil, fmt.Errorf("customers: archive customer: %w", err)
	}
	return gen.DeleteCustomersById204Response{}, nil
}
