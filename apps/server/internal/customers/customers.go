package customers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
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
	Email            *string
	Phone            *string
	Website          *string
	OwnerUserID      *uuid.UUID
	EntryCount       int64
	LatestOccurredOn pgtype.Date
}

// customerRowFrom is fromCustomerRow and its three sibling adapters' shared
// core: since customers.customers gained eleven billing columns (ten in
// 00019, the default bill rate in 00028) none of
// GetCustomer/UpdateCustomer/SetCustomerType/UpdateCustomerContactInfo
// selects (invoice-ready customer design D4's controller ruling — the
// billing profile must never reach SafeCustomerResponse), sqlc can no longer
// reuse store.CustomersCustomer as any of their return types: each query's
// own explicit column list only matches the table's full column set for the
// one query (GetCustomerBillingProfile) that actually selects them all, so
// every other query now gets its own generated row type instead, even though
// all four still select exactly the same sixteen columns. This one function
// holds the actual field mapping; each adapter below is a one-line unpacking
// of its own row type into it.
func customerRowFrom(id int32, customerNumber int64, name, status, typ string, legalCountry, legalID, legalName, legalSource, legalType *string, createdAt, updatedAt time.Time, revision int32, email, phone, website *string, ownerUserID *uuid.UUID, ts store.CustomerTimelineSummaryRow) customerRow {
	return customerRow{
		ID: id, CustomerNumber: customerNumber, Name: name, Status: status, Type: typ,
		LegalCountry: legalCountry, LegalID: legalID, LegalName: legalName, LegalSource: legalSource, LegalType: legalType,
		CreatedAt: createdAt, UpdatedAt: updatedAt, Revision: revision,
		Email: email, Phone: phone, Website: website, OwnerUserID: ownerUserID,
		EntryCount: ts.EntryCount, LatestOccurredOn: ts.LatestOccurredOn,
	}
}

// fromCustomerRow is GetCustomer's own row (store.GetCustomerRow) reduced to
// a customerRow — every GetCustomer(ctx, id) call in this module funnels
// through here, whichever handler made it.
func fromCustomerRow(c store.GetCustomerRow, ts store.CustomerTimelineSummaryRow) customerRow {
	return customerRowFrom(c.ID, c.CustomerNumber, c.Name, c.Status, c.Type, c.LegalCountry, c.LegalID, c.LegalName, c.LegalSource, c.LegalType, c.CreatedAt, c.UpdatedAt, c.Revision, c.Email, c.Phone, c.Website, c.OwnerUserID, ts)
}

// fromUpdateCustomerRow is UpdateCustomer's own row (PutCustomersById's write).
func fromUpdateCustomerRow(c store.UpdateCustomerRow, ts store.CustomerTimelineSummaryRow) customerRow {
	return customerRowFrom(c.ID, c.CustomerNumber, c.Name, c.Status, c.Type, c.LegalCountry, c.LegalID, c.LegalName, c.LegalSource, c.LegalType, c.CreatedAt, c.UpdatedAt, c.Revision, c.Email, c.Phone, c.Website, c.OwnerUserID, ts)
}

// fromSetCustomerTypeRow is SetCustomerType's own row
// (PutCustomersByIdType's write, customer_type.go).
func fromSetCustomerTypeRow(c store.SetCustomerTypeRow, ts store.CustomerTimelineSummaryRow) customerRow {
	return customerRowFrom(c.ID, c.CustomerNumber, c.Name, c.Status, c.Type, c.LegalCountry, c.LegalID, c.LegalName, c.LegalSource, c.LegalType, c.CreatedAt, c.UpdatedAt, c.Revision, c.Email, c.Phone, c.Website, c.OwnerUserID, ts)
}

// fromUpdateCustomerContactInfoRow is UpdateCustomerContactInfo's own row
// (PutCustomersByIdContactInfo's write, contact_info.go).
func fromUpdateCustomerContactInfoRow(c store.UpdateCustomerContactInfoRow, ts store.CustomerTimelineSummaryRow) customerRow {
	return customerRowFrom(c.ID, c.CustomerNumber, c.Name, c.Status, c.Type, c.LegalCountry, c.LegalID, c.LegalName, c.LegalSource, c.LegalType, c.CreatedAt, c.UpdatedAt, c.Revision, c.Email, c.Phone, c.Website, c.OwnerUserID, ts)
}

// fromUpdateCustomerOwnerRow is UpdateCustomerOwner's own row
// (PutCustomersByIdOwner's write, owner.go).
func fromUpdateCustomerOwnerRow(c store.UpdateCustomerOwnerRow, ts store.CustomerTimelineSummaryRow) customerRow {
	return customerRowFrom(c.ID, c.CustomerNumber, c.Name, c.Status, c.Type, c.LegalCountry, c.LegalID, c.LegalName, c.LegalSource, c.LegalType, c.CreatedAt, c.UpdatedAt, c.Revision, c.Email, c.Phone, c.Website, c.OwnerUserID, ts)
}

// fromSetCustomerGroupRow is SetCustomerGroup's own row
// (PutCustomersByIdGroup's write, group_membership.go). Its column list is
// UpdateCustomerOwner's, which is why it needs no new parameter: the group
// itself is decorated onto the response afterwards, not read from this row.
func fromSetCustomerGroupRow(c store.SetCustomerGroupRow, ts store.CustomerTimelineSummaryRow) customerRow {
	return customerRowFrom(c.ID, c.CustomerNumber, c.Name, c.Status, c.Type, c.LegalCountry, c.LegalID, c.LegalName, c.LegalSource, c.LegalType, c.CreatedAt, c.UpdatedAt, c.Revision, c.Email, c.Phone, c.Website, c.OwnerUserID, ts)
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
		Email: r.Email, Phone: r.Phone, Website: r.Website, OwnerUserID: r.OwnerUserID,
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
// contactInfo (invoice-ready customer design D2) is never gated this way —
// it is always set, ungated, since D2 makes it as visible as the name and
// status every list caller already sees. dec is the part of the response that
// does not come from the customer row: the owner's display name, which lives
// in identity's directory and is resolved per response (owner and tags design
// D1), the customer's tags, which live in their own table (D2), the customer's
// group, which lives in the module's own vocabulary table (customer groups
// design D3), and the customer it was merged into (customers merge design D3).
// All four are as ungated as contactInfo — design D4's ruling: an owner is not
// sensitive data and tags and groups are classification, so customers:view is
// the whole gate.
func safeCustomerResponse(row customerRow, includeIdentity bool, dec customerDecoration) gen.SafeCustomerResponse {
	tags := dec.tagsFor(row.ID)
	resp := gen.SafeCustomerResponse{
		Id:             row.ID,
		CustomerNumber: row.CustomerNumber,
		Name:           row.Name,
		Status:         row.Status,
		Type:           &row.Type,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
		Revision:       &row.Revision,
		ContactInfo:    &gen.CustomerContactInfo{Email: row.Email, Phone: row.Phone, Website: row.Website},
		Owner:          dec.owner(row.OwnerUserID),
		Group:          dec.group(row.ID),
		MergedInto:     dec.merged(row.ID),
		Tags:           &tags,
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

// searchPhoneEligible is search_phone's own gate (final review fix M2): a
// term short on digits — "1", or a customer-number/legal-id fragment like
// "10" — would otherwise ILIKE-match nearly every phone number's compacted
// form by accident, so the phone branch of CountCustomers/ListCustomers only
// activates once the compact search term carries at least three ASCII
// digits, comfortably clearing a genuine phone-number fragment ("922 12" →
// "92212") while a short, non-phone term never reaches it.
func searchPhoneEligible(compact string) bool {
	digits := 0
	for _, r := range compact {
		if r >= '0' && r <= '9' {
			digits++
			if digits >= 3 {
				return true
			}
		}
	}
	return false
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
	// ownerId, tagId and groupId are checked for SHAPE here and resolved in
	// GetCustomers: 'me' needs the request's principal, which this function
	// deliberately does not see — it is a pure parameter check, unit-tested as
	// one, and every message it collects is joined into the one 400 detail.
	if p.OwnerId != nil && !validOwnerFilter(*p.OwnerId) {
		errs = append(errs, fmt.Sprintf("'ownerId' must be a user id, 'me' or 'none', but was '%s'.", *p.OwnerId))
	}
	if p.TagId != nil && !validUUIDParam(*p.TagId) {
		errs = append(errs, fmt.Sprintf("'tagId' must be a tag id, but was '%s'.", *p.TagId))
	}
	if p.GroupId != nil && !validGroupFilter(*p.GroupId) {
		errs = append(errs, fmt.Sprintf("'groupId' must be a group id or 'none', but was '%s'.", *p.GroupId))
	}
	return errs
}

// validOwnerFilter and validUUIDParam are the ownerId/tagId parameter shapes
// (owner and tags design D1, D2). 'me' and 'none' are matched
// case-sensitively, as every other query parameter in this function is — a
// query parameter is never normalized here, so 'Me' is rejected rather than
// silently accepted.
func validOwnerFilter(raw string) bool {
	return raw == "me" || raw == "none" || validUUIDParam(raw)
}

// validGroupFilter is the groupId parameter's shape (customer groups design
// D3): a group id, or the literal 'none'. There is no 'me' to resolve, so
// unlike ownerId this needs no session at all — and 'none' is matched
// case-sensitively, as every other query parameter in this function is.
func validGroupFilter(raw string) bool {
	return raw == "none" || validUUIDParam(raw)
}

func validUUIDParam(raw string) bool {
	_, err := uuid.Parse(raw)
	return err == nil
}

// customerListFilter is GET /customers' query parameters resolved into what
// CountCustomers and ListCustomers take: the filters, the search with its three
// permission-gated reaches, and the sort. GetCustomers and GetCustomersExport
// (csvexport.go) both build one through customerListFilterFor, so the file a
// person downloads is exactly the list they are looking at — the same filters,
// the same search reach, the same order (customers import/export design D2).
type customerListFilter struct {
	includeArchived       bool
	status, customerType  *string
	search, searchCompact *string
	searchPhone           bool
	// searchIdentity is legalIdentityView: it decides both whether search may
	// reach the legal identity and whether a response (or a file) shows it.
	searchIdentity bool
	searchContacts bool
	ownerNone      bool
	ownerID        *uuid.UUID
	tagID          *uuid.UUID
	groupNone      bool
	groupID        *uuid.UUID
	sortBy         string
	descending     bool
}

// customerListFilterFor resolves p, or answers the one 400 detail the list
// gives for it (every message validateGetCustomersParams collects, joined with
// a space, or the 'me'-without-a-session refusal).
func (s *server) customerListFilterFor(ctx context.Context, p gen.GetCustomersParams) (customerListFilter, string) {
	if msgs := validateGetCustomersParams(p); len(msgs) > 0 {
		return customerListFilter{}, strings.Join(msgs, " ")
	}
	f := customerListFilter{
		includeArchived: p.IncludeArchived != nil && *p.IncludeArchived,
		status:          p.Status,
		customerType:    p.Type,
		sortBy:          "id",
		descending:      p.SortDirection != nil && *p.SortDirection == "desc",
	}
	if p.SortBy != nil {
		f.sortBy = *p.SortBy
	}
	if p.Search != nil {
		if trimmed := strings.TrimSpace(*p.Search); trimmed != "" {
			search := likePattern(trimmed)
			f.search = &search
			// search_compact matches the customer number and legal id the
			// way a person actually types them: "923 609 016" finds a legal
			// id stored, with no spaces, as "923609016" (customers
			// foundation design D4).
			compact := stripWhitespace(trimmed)
			searchCompact := likePattern(compact)
			f.searchCompact = &searchCompact
			// search_phone gates the phone branch on the same compact term
			// (final review fix M2): searchPhoneEligible above.
			f.searchPhone = searchPhoneEligible(compact)
		}
	}

	// ownerId's three forms resolve to the two SQL parameters the list queries
	// take (owner and tags design D1): 'none' is a NULL test, and both a user
	// id and 'me' are an equality — 'me' resolved from the SESSION, never from
	// anything the request says about who the caller is. The router already
	// refused an unauthenticated call (design D4), so the missing-principal
	// branch below is unreachable in production; it is a 400 rather than a
	// silent "everyone's customers", because answering the wrong customers is
	// the one outcome a "my customers" filter must never have.
	if p.OwnerId != nil {
		switch *p.OwnerId {
		case "none":
			f.ownerNone = true
		case "me":
			principal, ok := contracts.PrincipalFrom(ctx)
			if !ok || principal.UserID == uuid.Nil {
				return customerListFilter{}, "'ownerId' cannot be 'me' without a signed-in user."
			}
			id := principal.UserID
			f.ownerID = &id
		default:
			// validateGetCustomersParams already refused anything unparseable,
			// so err is impossible here; the guard means an impossible value
			// filters nothing rather than panicking.
			if id, err := uuid.Parse(*p.OwnerId); err == nil {
				f.ownerID = &id
			}
		}
	}
	if p.TagId != nil {
		if id, err := uuid.Parse(*p.TagId); err == nil {
			f.tagID = &id
		}
	}
	// groupId's two forms resolve to the two SQL parameters the list queries
	// take (design D3). No 'me' here and nothing session-dependent: a group is a
	// bucket the installation defines, not a relationship to the caller.
	if p.GroupId != nil {
		if *p.GroupId == "none" {
			f.groupNone = true
		} else if id, err := uuid.Parse(*p.GroupId); err == nil {
			// validateGetCustomersParams already refused anything unparseable, so
			// err is impossible here; the guard means an impossible value filters
			// nothing rather than panicking.
			f.groupID = &id
		}
	}

	// search_identity/search_contacts gate the legal-identity and
	// contact/association branches of search: a caller who cannot see that
	// data through its own endpoints must not be able to use search as an
	// oracle for it either (customers foundation design D4). Computed once
	// here and reused for both queries, so the count and the page never
	// disagree about what search reaches.
	//
	// Each answer costs an access check — a session lookup plus a permission
	// query — so the list asks for as few as it needs: searchIdentity
	// unconditionally, because legalIdentityView also decides whether the
	// identity is shown; searchContacts only when there is a search term at
	// all, since with no term the contact/association branch of the query is
	// unreachable and the flag cannot change a single row either way. The two
	// contact permissions travel as one AND-gated check (hasPermissions,
	// server.go) rather than two.
	f.searchIdentity = s.hasPermission(ctx, legalIdentityView)
	f.searchContacts = f.search != nil && s.hasPermissions(ctx, contactsView, associationsView)
	return f, ""
}

// countParams is f as CountCustomers takes it.
func (f customerListFilter) countParams() store.CountCustomersParams {
	return store.CountCustomersParams{
		IncludeArchived: f.includeArchived, Status: f.status, CustomerType: f.customerType,
		Search: f.search, SearchCompact: f.searchCompact, SearchIdentity: f.searchIdentity, SearchContacts: f.searchContacts,
		SearchPhone: f.searchPhone,
		OwnerNone:   f.ownerNone, OwnerID: f.ownerID, TagID: f.tagID,
		GroupNone: f.groupNone, GroupID: f.groupID,
	}
}

// listParams is f as ListCustomers takes it, for one page.
func (f customerListFilter) listParams(pageSize, offset int32) store.ListCustomersParams {
	return store.ListCustomersParams{
		IncludeArchived: f.includeArchived, Status: f.status, CustomerType: f.customerType,
		Search: f.search, SearchCompact: f.searchCompact, SearchIdentity: f.searchIdentity, SearchContacts: f.searchContacts,
		SearchPhone: f.searchPhone,
		OwnerNone:   f.ownerNone, OwnerID: f.ownerID, TagID: f.tagID,
		GroupNone: f.groupNone, GroupID: f.groupID,
		SortBy: f.sortBy, Descending: f.descending, PageSize: pageSize, RowOffset: offset,
	}
}

// GetCustomers List all customers
// (GET /api/v1/customers)
func (s *server) GetCustomers(ctx context.Context, req gen.GetCustomersRequestObject) (gen.GetCustomersResponseObject, error) {
	filter, detail := s.customerListFilterFor(ctx, req.Params)
	if detail != "" {
		return gen.GetCustomers400ApplicationProblemPlusJSONResponse(apicommon.Problem("Invalid query parameters", detail)), nil
	}

	page := int32(1)
	if req.Params.Page != nil {
		page = *req.Params.Page
	}
	pageSize := int32(25)
	if req.Params.PageSize != nil {
		pageSize = *req.Params.PageSize
	}

	q := store.New(s.deps.Pool)
	total, err := q.CountCustomers(ctx, filter.countParams())
	if err != nil {
		return nil, fmt.Errorf("customers: count customers: %w", err)
	}
	list, err := q.ListCustomers(ctx, filter.listParams(pageSize, (page-1)*pageSize))
	if err != nil {
		return nil, fmt.Errorf("customers: list customers: %w", err)
	}
	rows := make([]customerRow, 0, len(list))
	for _, r := range list {
		rows = append(rows, fromListRow(r))
	}

	// One directory call and one tags query for the whole page (owner and tags
	// design D1, D2), never one per row: 25 rows would otherwise be 25
	// out-of-process calls for data that is on the wire either way.
	dec, err := s.decorate(ctx, q, rows...)
	if err != nil {
		return nil, err
	}
	// searchIdentity doubles as includeIdentity here: legalIdentityView
	// answers both "may search reach the legal identity" and "may the
	// response show it", so one hasPermission call serves both.
	data := make([]gen.SafeCustomerResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, safeCustomerResponse(r, filter.searchIdentity, dec))
	}

	return gen.GetCustomers200JSONResponse{
		Data:       data,
		Pagination: apicommon.Pagination(page, pageSize, int32(total)),
	}, nil
}

// newCustomer is what PostCustomers inserts, already validated: the create
// endpoint and the CSV importer (import.go) build one from their own input and
// insert it through insertNewCustomer, so a customer created by file is the
// create endpoint's customer, number and event included.
type newCustomer struct {
	Name     string
	Status   string
	Type     string
	Identity *legalIdentity
	Contact  contactInfo
}

// insertNewCustomer is PostCustomers' transaction body: the duplicate check
// (customers foundation design D6) first when duplicateCheck says so, and
// before NextCounterValue, so a refused create burns no number; then the row
// and customer.created. A conflict returns errDuplicateIdentity with the body
// to answer it with — the transaction is rolled back by the time the caller
// sees the error, so the body travels beside it. excludeID is 0: there is no
// existing row to exclude on a create. nameHolders and act were resolved
// before the transaction opened (duplicates.go, actor.go).
func (s *server) insertNewCustomer(ctx context.Context, txq *store.Queries, c newCustomer, duplicateCheck, nameHolders bool, now time.Time, act actor) (store.InsertCustomerRow, *gen.CustomerConflictProblem, error) {
	if duplicateCheck && c.Identity != nil {
		problem, err := s.duplicateIdentityProblem(ctx, txq, *c.Identity, 0, nameHolders)
		if err != nil {
			return store.InsertCustomerRow{}, nil, err
		}
		if problem != nil {
			return store.InsertCustomerRow{}, problem, errDuplicateIdentity
		}
	}
	number, err := txq.NextCounterValue(ctx, "customer-number")
	if err != nil {
		return store.InsertCustomerRow{}, nil, err
	}
	legalCountry, legalID, legalName, legalSource, legalType := legalColumns(c.Identity)
	created, err := txq.InsertCustomer(ctx, store.InsertCustomerParams{
		CustomerNumber: number, Name: c.Name, Status: c.Status, Type: c.Type,
		LegalCountry: legalCountry, LegalID: legalID, LegalName: legalName, LegalSource: legalSource, LegalType: legalType,
		Now: now, Email: c.Contact.Email, Phone: c.Contact.Phone, Website: c.Contact.Website,
	})
	if err != nil {
		return store.InsertCustomerRow{}, nil, err
	}
	return created, nil, recordCustomerCreated(ctx, txq, now, created.ID, c.Name, c.Identity, act.Kind, act.Display, act.UserID)
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
//
// (3) Once validation passes, an identity is checked for a
// duplicate-legal-identity conflict (customers foundation design D6): 409
// unless the request carries allowDuplicateIdentity: true. There is no
// "unchanged" exemption on create — every create is a fresh identity by
// definition — so the check simply runs whenever identity is present and
// not overridden. It runs inside the write's own transaction
// (duplicates.go), and before NextCounterValue: aborting the transaction on
// a conflict must not have burned a customer number a refused create never
// uses.
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

	// contactInfo (invoice-ready customer design D2): optional, so a customer
	// can be created complete. validateContactInfo is the same validator
	// PutCustomersByIdContactInfo uses (contact_info.go); errors nest under
	// "contactInfo.<field>" the same way identity's do under "identity.<field>".
	var contact contactInfo
	if body.ContactInfo != nil {
		ci, ciErrs := validateContactInfo(body.ContactInfo.Email, body.ContactInfo.Phone, body.ContactInfo.Website)
		if ciErrs != nil {
			for field, msgs := range ciErrs {
				errs["contactInfo."+field] = msgs
			}
		} else {
			contact = ci
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

	allowDuplicateIdentity := body.AllowDuplicateIdentity != nil && *body.AllowDuplicateIdentity

	// Whether a duplicate conflict may name the customer that already holds
	// the identity (duplicates.go): resolved here, before the transaction
	// opens, because it is an access check — and only when the check can
	// actually run, so a create that will never raise the conflict pays
	// nothing for the answer.
	needsDuplicateCheck := identity != nil && !allowDuplicateIdentity
	nameHolders := needsDuplicateCheck && s.hasPermission(ctx, customersView)

	var created store.InsertCustomerRow
	var conflict *gen.CustomerConflictProblem
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		created, conflict, err = s.insertNewCustomer(ctx, store.New(tx),
			newCustomer{Name: name, Status: status, Type: customerType, Identity: identity, Contact: contact},
			needsDuplicateCheck, nameHolders, now, act)
		return err
	})
	if errors.Is(err, errDuplicateIdentity) {
		return gen.PostCustomers409ApplicationProblemPlusJSONResponse(*conflict), nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: create customer: %w", err)
	}

	// The registry fetch for a Brreg pick (Brreg in full design D2), after
	// the transaction has committed and before the response is built: the
	// customer already exists, so a registry that blinks costs the record,
	// never the create. The error is logged and dropped for exactly that
	// reason — a user who just picked a company from the registry must not
	// be told their create failed because the registry was slow — and the
	// Registry card offers a Refresh for the record that is not there yet.
	if orgnr := brregPickOrganisationNumber(identity, customerType); orgnr != "" {
		if err := s.fetchAndStoreRegistryRecord(ctx, created.ID, orgnr, identity.Name, act); err != nil {
			s.logRegistryFetchFailure(ctx, created.ID, err)
		}
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
	customer := fromCustomerRow(row, summary)
	dec, err := s.decorate(ctx, q, customer)
	if err != nil {
		return nil, err
	}
	return gen.GetCustomer200JSONResponse(safeCustomerResponse(customer, includeIdentity, dec)), nil
}

// customerCore is what PUT /customers/{id} writes — the name, the status and
// the legal identity — as one value, before and after.
type customerCore struct {
	Name     string
	Status   string
	Identity *legalIdentity
}

// writeCustomerCore is the transaction body PutCustomersById and
// PutCustomersByIdLegalIdentity share, and the one the CSV importer
// (import.go) writes a row's name, status and identity through: the duplicate
// check when duplicateCheck says so (a conflict returns errDuplicateIdentity
// and its body, before anything is written — no revision bump, no event); the
// UPDATE, guarded when expectedRevision is set (pgx.ErrNoRows then means a
// stale revision); the registry record's invalidation when the identity moved
// (fix round 2, C2 — a new identity makes the record on file the old
// company's, so it goes in the same transaction, under the customer-row lock
// the UPDATE holds: the same lock a refresh takes first, so the two can never
// interleave); and the events — customer.updated for a name or identity
// change, customer.status_changed for a status change. The caller has already
// decided the write is not a no-op and resolved act and nameHolders before
// the transaction opened.
func (s *server) writeCustomerCore(ctx context.Context, txq *store.Queries, id int32, customerType string, before, after customerCore, expectedRevision *int32, duplicateCheck, nameHolders bool, now time.Time, act actor) (store.UpdateCustomerRow, *gen.CustomerConflictProblem, error) {
	if duplicateCheck && after.Identity != nil {
		problem, err := s.duplicateIdentityProblem(ctx, txq, *after.Identity, id, nameHolders)
		if err != nil {
			return store.UpdateCustomerRow{}, nil, err
		}
		if problem != nil {
			return store.UpdateCustomerRow{}, problem, errDuplicateIdentity
		}
	}
	legalCountry, legalID, legalName, legalSource, legalType := legalColumns(after.Identity)
	updated, err := txq.UpdateCustomer(ctx, store.UpdateCustomerParams{
		ID: id, Name: after.Name, Status: after.Status,
		LegalCountry: legalCountry, LegalID: legalID, LegalName: legalName, LegalSource: legalSource, LegalType: legalType,
		UpdatedAt: now, ExpectedRevision: expectedRevision,
	})
	if err != nil {
		return store.UpdateCustomerRow{}, nil, err
	}
	identityChanged := !identityEqual(before.Identity, after.Identity)
	if identityChanged {
		if err := invalidateRegistryRecord(ctx, txq, id, after.Identity, customerType); err != nil {
			return store.UpdateCustomerRow{}, nil, err
		}
	}
	if before.Name != after.Name || identityChanged {
		if err := recordCustomerUpdated(ctx, txq, now, id, before.Name, before.Identity, after.Name, after.Identity, act.Kind, act.Display, act.UserID); err != nil {
			return store.UpdateCustomerRow{}, nil, err
		}
	}
	if before.Status != after.Status {
		if err := recordCustomerStatusChanged(ctx, txq, now, id, before.Status, after.Status, act.Kind, act.Display, act.UserID); err != nil {
			return store.UpdateCustomerRow{}, nil, err
		}
	}
	return updated, nil, nil
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
// gate wins over both), and a stale revision wins over an invalid identity;
// (6) only once the identity itself has been accepted does the
// duplicate-legal-identity check (customers foundation design D6) run,
// immediately before the write, and only when the request's (country, id)
// differs from the row's own (identityCountryAndIDEqual, duplicates.go) —
// resubmitting the same identity, even with a new name/source/type, is
// never a conflict with itself — and the request does not carry
// allowDuplicateIdentity: true. Like PostCustomers, it runs inside the
// write's own transaction and aborts it on a conflict, so a stale revision
// still wins over a duplicate identity (checked first) and a genuine no-op
// request never reaches the check at all (it returns 200 before opening a
// transaction).
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

	// needsDuplicateCheck (customers foundation design D6): true only when
	// the request actually proposes a different (country, id) than the row
	// already has (never on a bare name/source/type edit, and never with no
	// identity at all) and the caller has not opted out.
	allowDuplicateIdentity := body.AllowDuplicateIdentity != nil && *body.AllowDuplicateIdentity
	needsDuplicateCheck := afterIdentity != nil && !identityCountryAndIDEqual(beforeIdentity, afterIdentity) && !allowDuplicateIdentity

	finalStatus := existing.Status
	if hasStatus {
		finalStatus = status
	}
	// identityChanged is its own condition, not just part of changed: it is
	// what decides whether the registry record on file is still this
	// customer's company's, and whether a Brreg pick made here is worth a
	// fetch (Brreg in full design D2, fix round 2 C2). A bare rename changes
	// neither.
	identityChanged := !identityEqual(beforeIdentity, afterIdentity)
	changed := existing.Name != name || identityChanged
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
		row := fromCustomerRow(existing, summary)
		dec, err := s.decorate(ctx, q, row)
		if err != nil {
			return nil, err
		}
		return gen.PutCustomersById200JSONResponse(safeCustomerResponse(row, includeIdentity, dec)), nil
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

	// Resolved here for the same reason, and on the same rule, as
	// PostCustomers's above: whether a duplicate conflict may name the other
	// customer is an access check (duplicates.go), so it happens before the
	// transaction opens and only when the check can run at all.
	nameHolders := needsDuplicateCheck && s.hasPermission(ctx, customersView)

	var updated store.UpdateCustomerRow
	var conflict *gen.CustomerConflictProblem
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		updated, conflict, err = s.writeCustomerCore(ctx, store.New(tx), req.Id, existing.Type,
			customerCore{Name: existing.Name, Status: existing.Status, Identity: beforeIdentity},
			customerCore{Name: name, Status: finalStatus, Identity: afterIdentity},
			body.Revision, needsDuplicateCheck, nameHolders, now, act)
		return err
	})
	switch {
	case errors.Is(err, errDuplicateIdentity):
		return gen.PutCustomersById409ApplicationProblemPlusJSONResponse(*conflict), nil
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

	// The same after-commit fetch the create and PUT .../legal-identity make
	// (Brreg in full design D2, fix round 2 C2): this operation's body carries
	// an identity too — it is the edit modal's own Brreg picker path — so a
	// pick made here reads the new company's record straight away rather than
	// leaving the customer with none until somebody clicks Refresh. Only for a
	// changed, brreg-sourced Norwegian business identity, and its failure is
	// logged and dropped for the reason the create's is: the update has
	// already succeeded.
	if identityChanged {
		if orgnr := brregPickOrganisationNumber(afterIdentity, existing.Type); orgnr != "" {
			if err := s.fetchAndStoreRegistryRecord(ctx, req.Id, orgnr, afterIdentity.Name, act); err != nil {
				s.logRegistryFetchFailure(ctx, req.Id, err)
			}
		}
	}

	summary, err := q.CustomerTimelineSummary(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("customers: timeline summary: %w", err)
	}
	includeIdentity := s.hasPermission(ctx, legalIdentityView)
	row := fromUpdateCustomerRow(updated, summary)
	dec, err := s.decorate(ctx, q, row)
	if err != nil {
		return nil, err
	}
	return gen.PutCustomersById200JSONResponse(safeCustomerResponse(row, includeIdentity, dec)), nil
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
