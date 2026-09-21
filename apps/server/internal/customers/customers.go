package customers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is the Customers area's CRUD (EP/CustomersEndpoints.cs's bare
// group): getCustomers, postCustomers, getCustomer, putCustomersById and
// deleteCustomersById. stats.go holds the four dashboard operations mounted
// alongside them; customer_type.go the dedicated type change; contacts,
// legal identity, lookup and timeline live in their own files.

// customerRow is the shape GetCustomer and ListCustomers both reduce to
// before building a SafeCustomerResponse: one seam so safeCustomerResponse
// only has to know one shape, whichever sqlc-generated row it came from.
type customerRow struct {
	ID               int32
	CustomerNumber   int64
	Name             string
	Status           string
	Type             string
	LegalCountry     *string
	LegalID          *string
	LegalName        *string
	LegalSource      *string
	LegalType        *string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	Revision         int32
	EntryCount       int64
	LatestOccurredOn pgtype.Date
}

func fromCustomerRow(c store.CustomersCustomer, ts store.CustomerTimelineSummaryRow) customerRow {
	return customerRow{
		ID: c.ID, CustomerNumber: c.CustomerNumber, Name: c.Name, Status: c.Status, Type: c.Type,
		LegalCountry: c.LegalCountry, LegalID: c.LegalID, LegalName: c.LegalName, LegalSource: c.LegalSource, LegalType: c.LegalType,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, Revision: c.Revision,
		EntryCount: ts.EntryCount, LatestOccurredOn: ts.LatestOccurredOn,
	}
}

// fromListRow is store.ListCustomersRow's customerRow, the one list-query
// shape now that ListCustomersByID/ListCustomersByName (one per sortBy
// value) are a single ListCustomers with sort_by as a query parameter
// (customers foundation design D4).
func fromListRow(r store.ListCustomersRow) customerRow {
	return customerRow{
		ID: r.ID, CustomerNumber: r.CustomerNumber, Name: r.Name, Status: r.Status, Type: r.Type,
		LegalCountry: r.LegalCountry, LegalID: r.LegalID, LegalName: r.LegalName, LegalSource: r.LegalSource, LegalType: r.LegalType,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Revision: r.Revision,
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
		Type:           &row.Type,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
		Revision:       &row.Revision,
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

// customerRevisionConflictTitle is the title every revision-guarded write in
// this file (and customer_type.go) answers 409 with — customers foundation
// design D5, the module's own revision convention (time/projects'
// "revision", not the timeline's "expectedRevision").
const customerRevisionConflictTitle = "Customer revision conflict"

// customerRevisionConflict builds the 409 body for a stale revision: read is
// the revision the caller supplied (and, by the time this is called, is
// known to disagree with the row), now is the row's current one.
func customerRevisionConflict(read, now int32) gen.CustomerConflictProblem {
	title := customerRevisionConflictTitle
	detail := fmt.Sprintf("The customer has been changed since revision %d was read; it is now at revision %d.", read, now)
	status := int32(http.StatusConflict)
	return gen.CustomerConflictProblem{Title: &title, Detail: &detail, Status: &status}
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
	if p.SortBy != nil && *p.SortBy != "id" && *p.SortBy != "name" && *p.SortBy != "customerNumber" && *p.SortBy != "createdAt" && *p.SortBy != "updatedAt" {
		errs = append(errs, fmt.Sprintf("'sortBy' must be one of 'id', 'name', 'customerNumber', 'createdAt' or 'updatedAt', but was '%s'.", *p.SortBy))
	}
	if p.SortDirection != nil && *p.SortDirection != "asc" && *p.SortDirection != "desc" {
		errs = append(errs, fmt.Sprintf("'sortDirection' must be one of 'asc' or 'desc', but was '%s'.", *p.SortDirection))
	}
	// status/type are matched case-sensitively, exactly as sortBy is above —
	// unlike validateCustomerStatus/validateCustomerType (values.go), which
	// normalize a request body's value before comparing it. A query
	// parameter is never normalized: 'Active' is rejected, not silently
	// lowercased.
	if p.Status != nil && *p.Status != "active" && *p.Status != "disabled" && *p.Status != "archived" {
		errs = append(errs, fmt.Sprintf("'status' must be one of 'active', 'disabled' or 'archived', but was '%s'.", *p.Status))
	}
	if p.Type != nil && *p.Type != "business" && *p.Type != "person" {
		errs = append(errs, fmt.Sprintf("'type' must be one of 'business' or 'person', but was '%s'.", *p.Type))
	}
	return errs
}

// GetCustomers List all customers
// (GET /api/v1/customers)
func (s *server) GetCustomers(ctx context.Context, req gen.GetCustomersRequestObject) (gen.GetCustomersResponseObject, error) {
	if msgs := validateGetCustomersParams(req.Params); len(msgs) > 0 {
		return gen.GetCustomers400ApplicationProblemPlusJSONResponse(apicommon.Problem("Invalid query parameters", strings.Join(msgs, " "))), nil
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
	var search, searchCompact *string
	if req.Params.Search != nil {
		if trimmed := strings.TrimSpace(*req.Params.Search); trimmed != "" {
			p := likePattern(trimmed)
			search = &p
			// search_compact matches the customer number and legal id the
			// way a person actually types them: "923 609 016" finds a legal
			// id stored, with no spaces, as "923609016" (customers
			// foundation design D4).
			cp := likePattern(stripWhitespace(trimmed))
			searchCompact = &cp
		}
	}
	descending := req.Params.SortDirection != nil && *req.Params.SortDirection == "desc"
	sortBy := "id"
	if req.Params.SortBy != nil {
		sortBy = *req.Params.SortBy
	}

	// search_identity/search_contacts gate the legal-identity and
	// contact/association branches of search: a caller who cannot see that
	// data through its own endpoints must not be able to use search as an
	// oracle for it either (customers foundation design D4). Computed once
	// here and reused for both queries below, so the count and the page
	// never disagree about what search reaches.
	searchIdentity := s.hasPermission(ctx, legalIdentityView)
	searchContacts := s.hasPermission(ctx, contactsView) && s.hasPermission(ctx, associationsView)

	q := store.New(s.deps.Pool)
	total, err := q.CountCustomers(ctx, store.CountCustomersParams{
		IncludeArchived: includeArchived, Status: req.Params.Status, CustomerType: req.Params.Type,
		Search: search, SearchCompact: searchCompact, SearchIdentity: searchIdentity, SearchContacts: searchContacts,
	})
	if err != nil {
		return nil, fmt.Errorf("customers: count customers: %w", err)
	}

	offset := (page - 1) * pageSize
	list, err := q.ListCustomers(ctx, store.ListCustomersParams{
		IncludeArchived: includeArchived, Status: req.Params.Status, CustomerType: req.Params.Type,
		Search: search, SearchCompact: searchCompact, SearchIdentity: searchIdentity, SearchContacts: searchContacts,
		SortBy: sortBy, Descending: descending, PageSize: pageSize, RowOffset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("customers: list customers: %w", err)
	}
	rows := make([]customerRow, 0, len(list))
	for _, r := range list {
		rows = append(rows, fromListRow(r))
	}

	// searchIdentity doubles as includeIdentity here: legalIdentityView
	// answers both "may search reach the legal identity" and "may the
	// response show it", so one hasPermission call serves both.
	data := make([]gen.SafeCustomerResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, safeCustomerResponse(r, searchIdentity))
	}

	return gen.GetCustomers200JSONResponse{
		Data:       data,
		Pagination: apicommon.Pagination(page, pageSize, int32(total)),
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
// access"). It reuses hasPermission, never a second mechanism.
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
		return gen.PostCustomers403JSONResponse(apicommon.ForbiddenBody()), nil
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

	// The customer type (00007_customers_type.sql) defaults to business — a
	// name alone has always meant a company in this module — and, once an
	// identity is attached, the two must agree: identityTypeMismatch reports
	// the disagreement under identity.type alongside the other errors.
	customerType := "business"
	if body.Type != nil {
		ct, ctErr := validateCustomerType(*body.Type)
		if ctErr != "" {
			errs["type"] = []string{ctErr}
		} else {
			customerType = ct
		}
	}

	var identity *legalIdentity
	if body.Identity != nil {
		id, idErrs := validateLegalIdentity(body.Identity.Country, body.Identity.Type, body.Identity.Id, body.Identity.Name, body.Identity.Source)
		if idErrs != nil {
			for field, msgs := range idErrs {
				errs["identity."+field] = msgs
			}
		} else if mismatch := identityTypeMismatch(customerType, &id); mismatch != "" && errs["type"] == nil {
			errs["identity.type"] = []string{mismatch}
		} else {
			identity = &id
		}
	}

	if len(errs) > 0 {
		return gen.PostCustomers400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid customer", errs)), nil
	}

	now := s.deps.Clock()
	// Resolved before the transaction opens: the directory lookup
	// actorFor can make is an out-of-process call (customers foundation
	// design D1, actor.go).
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	legalCountry, legalID, legalName, legalSource, legalType := legalColumns(identity)
	var created store.CustomersCustomer
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		number, err := txq.NextCounterValue(ctx, "customer-number")
		if err != nil {
			return err
		}
		created, err = txq.InsertCustomer(ctx, store.InsertCustomerParams{
			CustomerNumber: number,
			Name:           name,
			Status:         status,
			Type:           customerType,
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
		return recordCustomerCreated(ctx, txq, now, created.ID, name, identity, act.Kind, act.Display, act.UserID)
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
// Ordering follows UpdateCustomerEndpoint.cs:23-129 (inventory §1.4), with
// the revision guard (customers foundation design D5) inserted where the
// controller ruling for that design places it: (1) the same
// legal-identity-manage 403 gate as PostCustomers, first, ahead of
// everything else — before field validation, before the 404 lookup, and
// before the post-404 identity re-validation; (2) name and status are
// validated next, together, before the customer lookup; (3) the lookup
// itself, 404 if missing; (4) a supplied revision that disagrees with the
// row just read, 409 — before the identity is even looked at, since a
// caller working from a stale picture should be told to re-read before
// anything about the request body is judged further; (5) only once the
// customer is found and its revision confirmed is the identity
// re-validated, as its own ValidationProblem — so an invalid name against a
// missing id answers 400 (validation wins), an invalid identity against a
// missing id answers 404 (existence wins), identity supplied without
// legal-identity-manage against a missing id answers 403 (the permission
// gate wins over both), and a stale revision wins over an invalid identity.
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
		return gen.PutCustomersById403JSONResponse(apicommon.ForbiddenBody()), nil
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
		return gen.PutCustomersById400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid customer", errs)), nil
	}

	q := store.New(s.deps.Pool)
	existing, err := q.GetCustomer(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCustomersById404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get customer: %w", err)
	}

	// A stale revision is a 409 regardless of what the rest of the request
	// would do — including a request that, once parsed, turns out to be a
	// no-op (customers foundation design D5): the caller's picture of the
	// row is stale either way.
	if body.Revision != nil && *body.Revision != existing.Revision {
		return gen.PutCustomersById409ApplicationProblemPlusJSONResponse(customerRevisionConflict(*body.Revision, existing.Revision)), nil
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
			return gen.PutCustomersById400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid customer", fieldErrs)), nil
		}
		// The body carries no customer type — PutCustomersByIdType is the
		// only way to change it — so the identity must agree with the type
		// the row already has.
		if mismatch := identityTypeMismatch(existing.Type, &parsed); mismatch != "" {
			return gen.PutCustomersById400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid customer", map[string][]string{"identity.type": {mismatch}})), nil
		}
		afterIdentity = &parsed
	}

	finalStatus := existing.Status
	if hasStatus {
		finalStatus = status
	}
	changed := existing.Name != name || !identityEqual(beforeIdentity, afterIdentity)
	statusChanged := finalStatus != existing.Status

	// No-op rule (customers foundation design D5): a request that leaves the
	// customer exactly as it was writes nothing at all — not even an
	// identical rewrite of the same values — so neither updated_at nor
	// revision moves, the same as before this design existed. A stale
	// revision has already been refused above, so reaching here with a
	// revision means it agreed with existing.Revision, which this response
	// still carries unchanged.
	if !changed && !statusChanged {
		summary, err := q.CustomerTimelineSummary(ctx, req.Id)
		if err != nil {
			return nil, fmt.Errorf("customers: timeline summary: %w", err)
		}
		includeIdentity := s.hasPermission(ctx, legalIdentityView)
		return gen.PutCustomersById200JSONResponse(safeCustomerResponse(fromCustomerRow(existing, summary), includeIdentity)), nil
	}

	now := s.deps.Clock()

	// Resolved before the transaction opens: this update always records at
	// least one event once it reaches here (the no-op case returned above),
	// so the actor is always needed (customers foundation design D1,
	// actor.go).
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	legalCountry, legalID, legalName, legalSource, legalType := legalColumns(afterIdentity)
	var updated store.CustomersCustomer
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var err error
		updated, err = txq.UpdateCustomer(ctx, store.UpdateCustomerParams{
			ID: req.Id, Name: name, Status: finalStatus,
			LegalCountry: legalCountry, LegalID: legalID, LegalName: legalName, LegalSource: legalSource, LegalType: legalType,
			UpdatedAt: now, ExpectedRevision: body.Revision,
		})
		if err != nil {
			return err
		}
		if changed {
			if err := recordCustomerUpdated(ctx, txq, now, req.Id, existing.Name, beforeIdentity, name, afterIdentity, act.Kind, act.Display, act.UserID); err != nil {
				return err
			}
		}
		if statusChanged {
			if err := recordCustomerStatusChanged(ctx, txq, now, req.Id, existing.Status, finalStatus, act.Kind, act.Display, act.UserID); err != nil {
				return err
			}
		}
		return nil
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// The guarded UPDATE's WHERE clause matched no row: a concurrent
		// writer moved the revision between our read above and this write.
		// Re-read to report the row's now-current revision (customers
		// foundation design D5's controller ruling) — a customer is never
		// hard-deleted, so this only answers 404 if something else entirely
		// removed the row out from under us.
		fresh, ferr := q.GetCustomer(ctx, req.Id)
		if errors.Is(ferr, pgx.ErrNoRows) {
			return gen.PutCustomersById404Response{}, nil
		}
		if ferr != nil {
			return nil, fmt.Errorf("customers: re-read customer after conflict: %w", ferr)
		}
		return gen.PutCustomersById409ApplicationProblemPlusJSONResponse(customerRevisionConflict(existing.Revision, fresh.Revision)), nil
	case err != nil:
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

	// Resolved before the transaction opens: DeleteCustomersById always
	// records a status-changed event once it reaches here (the archived
	// no-op returned above), so the actor is always needed.
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if _, err := txq.SetCustomerStatus(ctx, store.SetCustomerStatusParams{ID: req.Id, Status: "archived", Now: now}); err != nil {
			return err
		}
		return recordCustomerStatusChanged(ctx, txq, now, req.Id, existing.Status, "archived", act.Kind, act.Display, act.UserID)
	})
	if err != nil {
		return nil, fmt.Errorf("customers: archive customer: %w", err)
	}
	return gen.DeleteCustomersById204Response{}, nil
}
