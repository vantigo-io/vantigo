package customers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is the Contacts area (EP/ContactsEndpoints.cs) and the
// Customers/{id}/contacts association endpoints (EP/CustomersEndpoints.cs's
// contacts sub-group): ten operations in all. Legal identity, timeline and
// lookup remain later tasks' stubs in unimplemented.go.
//
// customers inventory §1.1 lists no permission on any of these ten that is
// conditional on the request body the way postCustomers/putCustomersById's
// legal-identity-manage is (server.go's legalIdentityManage): every
// x-vantigo-access here is a flat permission or `+`-joined pair that
// module.Router enforces in full, so none of these handlers make a second
// Access.Check the way PostCustomers/PutCustomersById do.

// contactResponse is ContactResponse.FromDomain
// (Endpoints/Contacts/Dtos/ContactResponse.cs): every list/get/create/update
// contact query selects exactly contacts' own columns, so sqlc always hands
// this a store.CustomersContact regardless of which query produced it.
func contactResponse(c store.CustomersContact) gen.ContactResponse {
	return gen.ContactResponse{
		Id: c.ID, FirstName: c.FirstName, LastName: c.LastName,
		MiddleName: c.MiddleName, Prefix: c.Prefix, Suffix: c.Suffix, Phone: c.Phone, Email: c.Email,
	}
}

// parsedContact is ParsedContact (Endpoints/Contacts/Dtos/ContactRequest.cs):
// the validated, normalized values of a ContactRequest, ready to persist.
type parsedContact struct {
	FirstName, LastName                      string
	MiddleName, Prefix, Suffix, Phone, Email *string
}

// validateOptionalPersonName is ContactRequest.TryParseOptional<PersonName>
// (Endpoints/Contacts/Dtos/ContactRequest.cs:58-77): a blank or absent value
// is not an error at all, it is simply absent — only a non-blank, invalid
// value adds a field error.
func validateOptionalPersonName(field string, value *string, errs map[string][]string) *string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	v, err := validatePersonName(*value)
	if err != "" {
		errs[field] = []string{err}
		return nil
	}
	return &v
}

func validateOptionalNamePart(field string, value *string, errs map[string][]string) *string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	v, err := validateNamePart(*value)
	if err != "" {
		errs[field] = []string{err}
		return nil
	}
	return &v
}

func validateOptionalPhone(field string, value *string, errs map[string][]string) *string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	v, err := validatePhoneNumber(*value)
	if err != "" {
		errs[field] = []string{err}
		return nil
	}
	return &v
}

func validateOptionalEmail(field string, value *string, errs map[string][]string) *string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	v, err := validateEmailAddress(*value)
	if err != "" {
		errs[field] = []string{err}
		return nil
	}
	return &v
}

// validateContactRequest is ContactRequest.TryParse
// (Endpoints/Contacts/Dtos/ContactRequest.cs:26-54): firstName/lastName are
// required, every other field is optional with blank treated as absent.
// Every field is validated regardless of an earlier one's failure, and every
// error is reported together, keyed by the JSON field name.
func validateContactRequest(body gen.ContactRequest) (parsedContact, map[string][]string) {
	errs := map[string][]string{}

	firstName, err := validatePersonName(body.FirstName)
	if err != "" {
		errs["firstName"] = []string{err}
	}
	lastName, err := validatePersonName(body.LastName)
	if err != "" {
		errs["lastName"] = []string{err}
	}
	middleName := validateOptionalPersonName("middleName", body.MiddleName, errs)
	prefix := validateOptionalNamePart("prefix", body.Prefix, errs)
	suffix := validateOptionalNamePart("suffix", body.Suffix, errs)
	phone := validateOptionalPhone("phone", body.Phone, errs)
	email := validateOptionalEmail("email", body.Email, errs)

	if len(errs) > 0 {
		return parsedContact{}, errs
	}
	return parsedContact{FirstName: firstName, LastName: lastName, MiddleName: middleName, Prefix: prefix, Suffix: suffix, Phone: phone, Email: email}, nil
}

// validateGetContactsParams is GetContactsEndpoint.Validate
// (Endpoints/Contacts/GetContactsEndpoint.cs:112-145): every check runs
// regardless of the others, and every failure's message is collected, to be
// joined with a single space into one ProblemDetails.Detail.
func validateGetContactsParams(p gen.GetCustomersContactsParams) []string {
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

// searchPatterns splits search on the space character and escapes each term
// as customers.go's likePattern does (GetContactsEndpoint.cs:41-45:
// `request.Search.Split(' ', RemoveEmptyEntries | TrimEntries)`): one
// already-globbed, already-ILIKE-escaped pattern per term, for
// CountContacts/ListContactsBy{ID,Name}'s "every term matches at least one
// field" predicate. Splitting on ' ' alone, not strings.Fields' full
// Unicode-whitespace class, matters: .NET treats a tab-separated "a\tb" as
// one term, never two.
func searchPatterns(search *string) []string {
	if search == nil {
		return nil
	}
	var patterns []string
	for _, term := range strings.Split(*search, " ") {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		patterns = append(patterns, likePattern(term))
	}
	return patterns
}

// GetCustomersContacts List all contacts
// (GET /api/v1/customers/contacts)
func (s *server) GetCustomersContacts(ctx context.Context, req gen.GetCustomersContactsRequestObject) (gen.GetCustomersContactsResponseObject, error) {
	if msgs := validateGetContactsParams(req.Params); len(msgs) > 0 {
		return gen.GetCustomersContacts400ApplicationProblemPlusJSONResponse(apicommon.Problem("Invalid query parameters", strings.Join(msgs, " "))), nil
	}

	page := int32(1)
	if req.Params.Page != nil {
		page = *req.Params.Page
	}
	pageSize := int32(25)
	if req.Params.PageSize != nil {
		pageSize = *req.Params.PageSize
	}
	patterns := searchPatterns(req.Params.Search)
	sortByID := req.Params.SortBy != nil && *req.Params.SortBy == "id"
	descending := req.Params.SortDirection != nil && *req.Params.SortDirection == "desc"

	q := store.New(s.deps.Pool)
	total, err := q.CountContacts(ctx, patterns)
	if err != nil {
		return nil, fmt.Errorf("customers: count contacts: %w", err)
	}

	offset := (page - 1) * pageSize
	var rows []store.CustomersContact
	if sortByID {
		rows, err = q.ListContactsByID(ctx, store.ListContactsByIDParams{Patterns: patterns, Descending: descending, PageSize: pageSize, RowOffset: offset})
	} else {
		rows, err = q.ListContactsByName(ctx, store.ListContactsByNameParams{Patterns: patterns, Descending: descending, PageSize: pageSize, RowOffset: offset})
	}
	if err != nil {
		return nil, fmt.Errorf("customers: list contacts: %w", err)
	}

	ids := make([]int32, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	associations, err := q.AssociationsForContacts(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("customers: contact associations: %w", err)
	}
	byContact := make(map[int32][]gen.CustomerReference, len(rows))
	for _, a := range associations {
		byContact[a.ContactID] = append(byContact[a.ContactID], gen.CustomerReference{Id: a.CustomerID, CustomerNumber: a.CustomerNumber, Name: a.CustomerName})
	}

	data := make([]gen.GetContactsResponse, 0, len(rows))
	for _, r := range rows {
		customers := byContact[r.ID]
		item := gen.GetContactsResponse{Contact: contactResponse(r), CustomerCount: int32(len(customers))}
		if len(customers) == 1 {
			single := customers[0]
			item.Customer = &single
		}
		data = append(data, item)
	}

	return gen.GetCustomersContacts200JSONResponse{
		Data:       data,
		Pagination: apicommon.Pagination(page, pageSize, int32(total)),
	}, nil
}

// createdContactResponse is PostCustomersContacts' 201. The generated
// PostCustomersContacts201JSONResponse has no Location header because the
// contract declares none for this response; this type adds it directly, as
// .NET's TypedResults.CreatedAtRoute does (CreateContactEndpoint.cs:32-35).
type createdContactResponse struct {
	body     gen.ContactResponse
	location string
}

func (r createdContactResponse) VisitPostCustomersContactsResponse(w http.ResponseWriter) error {
	w.Header().Set("Location", r.location)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	return json.NewEncoder(w).Encode(r.body)
}

// PostCustomersContacts Create a new contact
// (POST /api/v1/customers/contacts)
func (s *server) PostCustomersContacts(ctx context.Context, req gen.PostCustomersContactsRequestObject) (gen.PostCustomersContactsResponseObject, error) {
	body := gen.ContactRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	parsed, errs := validateContactRequest(body)
	if errs != nil {
		return gen.PostCustomersContacts400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid contact", errs)), nil
	}

	q := store.New(s.deps.Pool)
	created, err := q.InsertContact(ctx, store.InsertContactParams{
		FirstName: parsed.FirstName, LastName: parsed.LastName, MiddleName: parsed.MiddleName,
		Prefix: parsed.Prefix, Suffix: parsed.Suffix, Phone: parsed.Phone, Email: parsed.Email,
		Now: s.deps.Clock(),
	})
	if err != nil {
		return nil, fmt.Errorf("customers: create contact: %w", err)
	}

	return createdContactResponse{
		body:     contactResponse(created),
		location: fmt.Sprintf("%s/api/v1/customers/contacts/%d", s.deps.Config.BasePath, created.ID),
	}, nil
}

// GetContact Get a contact by id
// (GET /api/v1/customers/contacts/{id})
func (s *server) GetContact(ctx context.Context, req gen.GetContactRequestObject) (gen.GetContactResponseObject, error) {
	q := store.New(s.deps.Pool)
	c, err := q.GetContact(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetContact404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get contact: %w", err)
	}
	return gen.GetContact200JSONResponse(contactResponse(c)), nil
}

// PutCustomersContactsById Update a contact
// (PUT /api/v1/customers/contacts/{id})
//
// UpdateContactEndpoint.cs:15-38 validates before it looks the contact up
// (inventory §1.4's "CreateContact / UpdateContact: validate first, then (for
// update) look up"): a body that is both invalid and aimed at a missing id
// answers 400, never 404.
func (s *server) PutCustomersContactsById(ctx context.Context, req gen.PutCustomersContactsByIdRequestObject) (gen.PutCustomersContactsByIdResponseObject, error) {
	body := gen.ContactRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	parsed, errs := validateContactRequest(body)
	if errs != nil {
		return gen.PutCustomersContactsById400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid contact", errs)), nil
	}

	q := store.New(s.deps.Pool)
	updated, err := q.UpdateContact(ctx, store.UpdateContactParams{
		ID: req.Id, FirstName: parsed.FirstName, LastName: parsed.LastName, MiddleName: parsed.MiddleName,
		Prefix: parsed.Prefix, Suffix: parsed.Suffix, Phone: parsed.Phone, Email: parsed.Email,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCustomersContactsById404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: update contact: %w", err)
	}
	return gen.PutCustomersContactsById200JSONResponse(contactResponse(updated)), nil
}

// DeleteCustomersContactsById Delete a contact
// (DELETE /api/v1/customers/contacts/{id})
//
// DeleteContactEndpoint.cs:14-43: a SELECT ... FOR UPDATE lock on the
// contact row (customers inventory §4), then the row of every customer it is
// attached to (typed contact roles design D2, in ascending customer_id order),
// then every association it still carries is recorded as a "removed" timeline
// event against that association's customer, and only then is the contact
// (and, via ON DELETE CASCADE, its associations and their role rows) deleted
// and a new primary promoted wherever this contact was one — all in one
// transaction.
func (s *server) DeleteCustomersContactsById(ctx context.Context, req gen.DeleteCustomersContactsByIdRequestObject) (gen.DeleteCustomersContactsByIdResponseObject, error) {
	// Resolved before the transaction opens: the directory lookup actorFor can
	// make is an out-of-process call this module never wants to make while
	// holding a row lock (customers foundation design D1, actor.go).
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	now := s.deps.Clock()
	err = db.RetrySerializable(ctx, contactRoleWriteAttempts, func() error {
		return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			txq := store.New(tx)
			contact, err := txq.GetContactForUpdate(ctx, req.Id)
			if err != nil {
				return err
			}
			associations, err := txq.ListAssociationsForContact(ctx, req.Id)
			if err != nil {
				return err
			}
			roleRows, err := txq.ContactRolesForContact(ctx, req.Id)
			if err != nil {
				return err
			}
			rolesByCustomer := make(map[int32][]contactRole, len(associations))
			for _, r := range roleRows { // already ordered by customer, then the fixed role order
				rolesByCustomer[r.CustomerID] = append(rolesByCustomer[r.CustomerID], contactRole{Role: r.Role, Primary: r.IsPrimary})
			}

			// Every customer this contact is attached to has to be locked
			// before its roles are re-arranged (typed contact roles design
			// D2), and in the order ListAssociationsForContact answers —
			// ascending customer_id, which the query's own ORDER BY
			// guarantees. A deterministic order across all callers is what
			// keeps two concurrent deletes of two contacts that share two
			// customers from deadlocking with each other; the retry above
			// exists for the other cycle, the one against an attach.
			for _, a := range associations {
				if _, err := txq.LockCustomer(ctx, a.CustomerID); err != nil {
					return err
				}
			}
			for _, a := range associations {
				if err := recordContactRemoved(ctx, txq, now, a.CustomerID, contact, a.Title, rolesByCustomer[a.CustomerID],
					a.Phone, a.Email, act.Kind, act.Display, act.UserID); err != nil {
					return err
				}
			}

			// The contact goes, and with it every association and every role
			// row (two cascades: contacts → customers_contacts →
			// customer_contact_roles). Only then is a promotion safe, the same
			// delete-before-promote order the detach keeps.
			if err := txq.DeleteContact(ctx, req.Id); err != nil {
				return err
			}
			for _, a := range associations {
				promotions, err := releaseRoles(ctx, txq, a.CustomerID, req.Id, rolesByCustomer[a.CustomerID])
				if err != nil {
					return err
				}
				if err := recordPromotions(ctx, txq, now, a.CustomerID, promotions, act); err != nil {
					return err
				}
			}
			return nil
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.DeleteCustomersContactsById404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: delete contact: %w", err)
	}
	return gen.DeleteCustomersContactsById204Response{}, nil
}

// GetCustomersContactsByIdCustomers List the customers a contact is associated with
// (GET /api/v1/customers/contacts/{id}/customers)
func (s *server) GetCustomersContactsByIdCustomers(ctx context.Context, req gen.GetCustomersContactsByIdCustomersRequestObject) (gen.GetCustomersContactsByIdCustomersResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.GetContact(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return gen.GetCustomersContactsByIdCustomers404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("customers: get contact: %w", err)
	}

	rows, err := q.ListCustomerAssociationsForContact(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("customers: list contact customers: %w", err)
	}
	roleRows, err := q.ContactRolesForContact(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("customers: list contact roles: %w", err)
	}
	byCustomer := make(map[int32][]contactRole, len(rows))
	for _, r := range roleRows {
		byCustomer[r.CustomerID] = append(byCustomer[r.CustomerID], contactRole{Role: r.Role, Primary: r.IsPrimary})
	}

	data := make([]gen.GetContactCustomersContactCustomerResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, gen.GetContactCustomersContactCustomerResponse{
			Customer: gen.GetContactCustomersCustomerReference{Id: r.ID, CustomerNumber: r.CustomerNumber, Name: r.Name},
			Role:     deref(r.Title), Title: r.Title, Roles: genContactRoles(byCustomer[r.ID]),
			Phone: r.Phone, Email: r.Email,
		})
	}
	return gen.GetCustomersContactsByIdCustomers200JSONResponse{Data: data}, nil
}

// GetCustomersByIdContacts List the contacts associated with a customer
// (GET /api/v1/customers/{id}/contacts)
func (s *server) GetCustomersByIdContacts(ctx context.Context, req gen.GetCustomersByIdContactsRequestObject) (gen.GetCustomersByIdContactsResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.GetCustomer(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return gen.GetCustomersByIdContacts404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("customers: get customer: %w", err)
	}

	rows, err := q.ListContactAssociationsForCustomer(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("customers: list customer contacts: %w", err)
	}
	roleRows, err := q.ContactRolesForCustomer(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("customers: list customer contact roles: %w", err)
	}
	// One query for the whole list, never one per row (design D3), grouped the
	// way CustomerTagsForCustomers' answer is: the rows arrive in the fixed
	// role order already, so appending preserves it.
	byContact := make(map[int32][]contactRole, len(rows))
	for _, r := range roleRows {
		byContact[r.ContactID] = append(byContact[r.ContactID], contactRole{Role: r.Role, Primary: r.IsPrimary})
	}

	data := make([]gen.CustomerContactResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, gen.CustomerContactResponse{
			Contact: gen.ContactResponse{
				Id: r.ID, FirstName: r.FirstName, LastName: r.LastName,
				MiddleName: r.MiddleName, Prefix: r.Prefix, Suffix: r.Suffix, Phone: r.ContactPhone, Email: r.ContactEmail,
			},
			Role: deref(r.Title), Title: r.Title, Roles: genContactRoles(byContact[r.ID]),
			Phone: r.AssociationPhone, Email: r.AssociationEmail,
		})
	}
	return gen.GetCustomersByIdContacts200JSONResponse{Data: data}, nil
}

// errAssociationTargetNotFound and errAlreadyAttached are PostCustomersByIdContacts'
// two non-400 refusals, threaded out of the transaction fn as sentinel
// errors so db.WithTx's own error path stays a plain "did it fail" signal.
var (
	errAssociationTargetNotFound = errors.New("customers: customer or contact not found")
	errAlreadyAttached           = errors.New("customers: contact already associated")
)

// contactRoleWriteAttempts is how often an association write's transaction runs
// before the deadlock it keeps losing escapes as a 500. Three, as tags.go's
// tagWriteAttempts and identity's serializableAttempts both settled on.
//
// The deadlock is real and is between two handlers in this very file. An
// attach locks the CUSTOMER row (LockCustomer, so the role bookkeeping
// serializes) and then the CONTACT row (GetContactForUpdate, the ported lock
// that serializes attach against a concurrent delete of the same contact),
// while DELETE /customers/contacts/{id} locks the contact row first — it has
// to, that is the lock's whole purpose — and only then the rows of every
// customer it must promote a new primary for. Two opposite lock orders, so the
// two can cycle; PostgreSQL breaks it by killing one side (40P01). Being the
// victim of a lock-order cycle is not something either caller did wrong, so
// the retry runs the loser again from a fresh snapshot, in which one of the two
// writes has simply already happened.
//
// Reversing one of the orders instead was considered and rejected: the delete
// cannot lock the customers before the contact, because which customers those
// are is what reading the contact's associations tells it, and an association
// added between that read and the lock would need a retry anyway.
const contactRoleWriteAttempts = 3

// PostCustomersByIdContacts Associate a contact with a customer
// (POST /api/v1/customers/{id}/contacts)
//
// AttachCustomerContactEndpoint.cs:18-68 (customers inventory §1.4): (1)
// connection field validation, before any database access at all; (2) the
// customer row's FOR NO KEY UPDATE lock, which is also its existence check
// (typed contact roles design D2: every write to customer_contact_roles takes
// it first), then a SELECT ... FOR UPDATE lock on the contact row, both inside
// one transaction — 404 if either row is missing; (3) the already-attached
// check, 409 if so. Validation runs before existence, the opposite order from
// PutCustomersByIdContactsByContactId below — pinned by
// TestAttachContact_InvalidConnectionAgainstUnknownCustomer_Returns400.
func (s *server) PostCustomersByIdContacts(ctx context.Context, req gen.PostCustomersByIdContactsRequestObject) (gen.PostCustomersByIdContactsResponseObject, error) {
	body := gen.AttachCustomerContactRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	assoc, errs := validateCustomerContactRequest(body.Title, body.Role, body.Roles, body.Phone, body.Email, 0)
	if errs != nil {
		return gen.PostCustomersByIdContacts400ApplicationProblemPlusJSONResponse(associationProblem(errs)), nil
	}

	now := s.deps.Clock()
	// Resolved before the transaction opens (customers foundation design D1,
	// actor.go): a successful write always follows past validation, and the
	// 404/409 refusals are only knowable inside the transaction, so one wasted
	// directory call on those paths is accepted rather than resolving it twice.
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	var response gen.CustomerContactResponse
	err = db.RetrySerializable(ctx, contactRoleWriteAttempts, func() error {
		return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			txq := store.New(tx)

			// The customer row's lock comes first, before the contact's: every
			// write that touches customer_contact_roles takes it (typed
			// contact roles design D2), and it is also this handler's
			// existence check, replacing the plain GetCustomer it used to make.
			customer, err := txq.LockCustomer(ctx, req.Id)
			if errors.Is(err, pgx.ErrNoRows) {
				return errAssociationTargetNotFound
			}
			if err != nil {
				return err
			}
			contact, err := txq.GetContactForUpdate(ctx, body.ContactId)
			if errors.Is(err, pgx.ErrNoRows) {
				return errAssociationTargetNotFound
			}
			if err != nil {
				return err
			}

			attached, err := txq.AssociationExists(ctx, store.AssociationExistsParams{CustomerID: req.Id, ContactID: body.ContactId})
			if err != nil {
				return err
			}
			if attached {
				return errAlreadyAttached
			}

			if err := txq.InsertAssociation(ctx, store.InsertAssociationParams{
				CustomerID: req.Id, ContactID: body.ContactId, Title: assoc.Title, Phone: assoc.Phone, Email: assoc.Email,
			}); err != nil {
				return err
			}
			// nil existing: the association was created a statement ago, so
			// every role it is given is a new one and the first-holder rule is
			// the only one that can apply.
			roles, promotions, err := applyRoles(ctx, txq, req.Id, body.ContactId, nil, assoc.Roles, now)
			if err != nil {
				return err
			}
			if err := recordContactAttached(ctx, txq, now, customer.ID, contact, assoc.Title, roles, assoc.Phone, assoc.Email, act.Kind, act.Display, act.UserID); err != nil {
				return err
			}
			if err := recordPromotions(ctx, txq, now, req.Id, promotions, act); err != nil {
				return err
			}

			response = gen.CustomerContactResponse{
				Contact: contactResponse(contact), Role: deref(assoc.Title), Title: assoc.Title,
				Roles: genContactRoles(roles), Phone: assoc.Phone, Email: assoc.Email,
			}
			return nil
		})
	})
	switch {
	case errors.Is(err, errAssociationTargetNotFound):
		return gen.PostCustomersByIdContacts404Response{}, nil
	case errors.Is(err, errAlreadyAttached):
		detail := fmt.Sprintf("Contact %d is already associated with customer %d.", body.ContactId, req.Id)
		return gen.PostCustomersByIdContacts409ApplicationProblemPlusJSONResponse(apicommon.ProblemStatus("Contact already associated", detail, http.StatusConflict)), nil
	case errors.Is(err, errRolePrimaryTransitionRefused):
		// Unreachable on an attach — nothing is held yet, so no primary can be
		// cleared — but handled rather than falling into the 500 below, because
		// "unreachable" is a property of applyRoles' phase 1 and not of this
		// call site, and a future change to either should surface as the 400 it
		// is.
		return gen.PostCustomersByIdContacts400ApplicationProblemPlusJSONResponse(associationProblem(roleErrorsFor(err))), nil
	case err != nil:
		return nil, fmt.Errorf("customers: attach contact: %w", err)
	}
	return gen.PostCustomersByIdContacts200JSONResponse(response), nil
}

// contactFromAssociationRow rebuilds a store.CustomersContact from
// GetAssociationWithContact's joined row, for the timeline recorder
// functions that take a contact row directly.
func contactFromAssociationRow(r store.GetAssociationWithContactRow) store.CustomersContact {
	return store.CustomersContact{
		ID: r.ID, FirstName: r.FirstName, LastName: r.LastName, MiddleName: r.MiddleName,
		Prefix: r.Prefix, Suffix: r.Suffix, Phone: r.ContactPhone, Email: r.ContactEmail, CreatedAt: r.CreatedAt,
	}
}

// PutCustomersByIdContactsByContactId Update a customer's contact association
// (PUT /api/v1/customers/{id}/contacts/{contactId})
//
// UpdateCustomerContactEndpoint.cs:16-53 (customers inventory §1.4): (1) the
// association lookup, 404 if missing; (2) field validation, 400 — the
// opposite order from PostCustomersByIdContacts above, pinned by
// TestUpdateCustomerContact_InvalidConnectionAgainstUnknownAssociation_Returns404.
// A change to the title, the phone, the email or the role set records a
// "relationship updated" timeline event (UpdateCustomerContactEndpoint.cs:43-49,
// widened by typed contact roles design D4, whose summary names what moved);
// resubmitting the same values records nothing at all, transaction included.
func (s *server) PutCustomersByIdContactsByContactId(ctx context.Context, req gen.PutCustomersByIdContactsByContactIdRequestObject) (gen.PutCustomersByIdContactsByContactIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	existing, err := q.GetAssociationWithContact(ctx, store.GetAssociationWithContactParams{CustomerID: req.Id, ContactID: req.ContactId})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCustomersByIdContactsByContactId404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get association: %w", err)
	}

	currentRoles, err := contactRolesOf(ctx, q, req.Id, req.ContactId)
	if err != nil {
		return nil, fmt.Errorf("customers: read association roles: %w", err)
	}

	body := gen.CustomerContactRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	// rolesWhenOmitted is what the association already holds: `roles` omitted
	// means "leave them alone" (design D3), so the title-or-role rule must not
	// refuse a request that only changes a phone number on an association that
	// already has three roles.
	assoc, errs := validateCustomerContactRequest(body.Title, body.Role, body.Roles, body.Phone, body.Email, len(currentRoles))
	if errs != nil {
		return gen.PutCustomersByIdContactsByContactId400ApplicationProblemPlusJSONResponse(associationProblem(errs)), nil
	}

	// An omitted `roles` is the current set, so the rest of this handler can
	// treat "what to hold" as one thing. Every element's Primary is nil — "leave
	// this one alone" — and not the flag copied out of currentRoles: a request
	// that did not mention roles at all must be unable to move a primary flag,
	// and nil is the only value of the three that guarantees that even if the
	// set changed under us between the unlocked read and the lock.
	want := assoc.Roles
	if !assoc.RolesGiven {
		want = make([]requestedRole, 0, len(currentRoles))
		for _, r := range currentRoles {
			want = append(want, requestedRole{Role: r.Role, Primary: nil})
		}
	}

	// The refusal is decided before the no-op shortcut below, on the set the
	// unlocked read found: a request asking to clear the primary flag of a role
	// this contact is the only or the primary holder of is refused (design D2)
	// even when it changes nothing else, because answering 200 to it would tell
	// the client its `primary: false` was honoured. Only an EXPLICIT false is
	// this refusal — an omitted flag means "leave it alone" and is never
	// refused. applyRoles refuses again under the lock, and that check is the
	// authoritative one; this one only makes sure the shortcut cannot swallow it.
	currentPrimary := heldPrimary(currentRoles)
	for _, r := range want {
		if wasPrimary, ok := currentPrimary[r.Role]; ok && wasPrimary && r.clearsPrimary() {
			return gen.PutCustomersByIdContactsByContactId400ApplicationProblemPlusJSONResponse(
				associationProblem(map[string][]string{"roles": {rolePrimaryTransitionMessage(r.Role)}})), nil
		}
	}

	answer := gen.PutCustomersByIdContactsByContactId200JSONResponse{
		Contact: contactResponse(contactFromAssociationRow(existing)), Role: deref(assoc.Title), Title: assoc.Title,
		Roles: genContactRoles(currentRoles), Phone: assoc.Phone, Email: assoc.Email,
	}

	fieldsChanged := deref(existing.Title) != deref(assoc.Title) ||
		deref(existing.AssociationPhone) != deref(assoc.Phone) ||
		deref(existing.AssociationEmail) != deref(assoc.Email)
	if !fieldsChanged && !rolesChanged(currentRoles, requestedAsHeld(want, currentRoles)) {
		// Nothing moved: the no-op rule every write in this module follows
		// (customers foundation design D5), and the reason this handler no
		// longer opens a transaction for one — the UPDATE would rewrite
		// identical values, the role bookkeeping would rewrite identical rows,
		// no event would be recorded anyway, and the actor lookup below would
		// be a directory call made for a request that writes nothing. A
		// concurrent writer can make this answer stale, which is what
		// last-wins on an off-the-row resource means (tags.go says the same).
		return answer, nil
	}

	now := s.deps.Clock()
	// Resolved before the transaction opens, and only now that a write is
	// certain to follow (customers foundation design D1, actor.go).
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	err = db.RetrySerializable(ctx, contactRoleWriteAttempts, func() error {
		return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			txq := store.New(tx)
			if _, err := txq.LockCustomer(ctx, req.Id); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return errAssociationTargetNotFound
				}
				return err
			}
			// Re-read under the lock: the unlocked read above answered the
			// 404 and shaped the validation, but a concurrent detach could
			// have removed the association since, and inserting role rows for
			// an association that no longer exists is a foreign-key violation
			// rather than the 404 it really is. Detach takes this same lock,
			// so re-reading inside it is a complete answer, not a narrower
			// window.
			locked, err := txq.GetAssociationWithContact(ctx, store.GetAssociationWithContactParams{CustomerID: req.Id, ContactID: req.ContactId})
			if errors.Is(err, pgx.ErrNoRows) {
				return errAssociationTargetNotFound
			}
			if err != nil {
				return err
			}
			before, err := contactRolesOf(ctx, txq, req.Id, req.ContactId)
			if err != nil {
				return err
			}

			if err := txq.UpdateAssociation(ctx, store.UpdateAssociationParams{
				CustomerID: req.Id, ContactID: req.ContactId, Title: assoc.Title, Phone: assoc.Phone, Email: assoc.Email,
			}); err != nil {
				return err
			}
			after, promotions, err := applyRoles(ctx, txq, req.Id, req.ContactId, before, want, now)
			if err != nil {
				return err
			}
			answer.Roles = genContactRoles(after)

			// Recomputed against what the lock actually found: a concurrent
			// writer may already have made this exact change, and an event
			// claiming a change that did not happen is worse than the wasted
			// actor lookup above (the same trade addresses.go documents).
			if deref(locked.Title) != deref(assoc.Title) ||
				deref(locked.AssociationPhone) != deref(assoc.Phone) ||
				deref(locked.AssociationEmail) != deref(assoc.Email) ||
				rolesChanged(before, after) {
				if err := recordContactRelationshipUpdated(ctx, txq, now, req.Id, contactFromAssociationRow(locked),
					relationshipUpdateAction(before, after), assoc.Title, after, assoc.Phone, assoc.Email,
					act.Kind, act.Display, act.UserID); err != nil {
					return err
				}
			}
			return recordPromotions(ctx, txq, now, req.Id, promotions, act)
		})
	})
	switch {
	case errors.Is(err, errAssociationTargetNotFound):
		return gen.PutCustomersByIdContactsByContactId404Response{}, nil
	case errors.Is(err, errRolePrimaryTransitionRefused):
		return gen.PutCustomersByIdContactsByContactId400ApplicationProblemPlusJSONResponse(associationProblem(roleErrorsFor(err))), nil
	case err != nil:
		return nil, fmt.Errorf("customers: update association: %w", err)
	}

	return answer, nil
}

// requestedAsHeld predicts what applyRoles will leave the association holding,
// so the no-op check can compare like with like. It resolves the two things a
// request does not state outright (see requestedRole):
//
//   - a role the association already holds with an OMITTED flag keeps the flag
//     it has, which is the whole reason the flag is a pointer; and
//   - a role it already holds with an explicit true is primary, while an
//     explicit true on a role it does not hold yet may or may not be (the
//     first-holder rule needs a holder count this function does not have) —
//     which does not matter, because a role the association does not hold is a
//     MEMBERSHIP change and rolesChanged has already answered true whatever
//     flag is predicted for it.
func requestedAsHeld(want []requestedRole, held []contactRole) []contactRole {
	wasPrimary := heldPrimary(held)
	out := make([]contactRole, 0, len(want))
	for _, r := range want {
		primary, alreadyHeld := wasPrimary[r.Role]
		if !alreadyHeld {
			primary = r.wantsPrimary()
		} else if r.wantsPrimary() {
			primary = true
		}
		out = append(out, contactRole{Role: r.Role, Primary: primary})
	}
	return out
}

// DeleteCustomersByIdContactsByContactId Remove a contact association from a customer
// (DELETE /api/v1/customers/{id}/contacts/{contactId})
//
// DetachCustomerContactEndpoint.cs:15-36: the contact itself is kept, only
// the association row is removed, and a "detached" timeline event is
// recorded against the customer. Under the customer row's lock, because the
// roles the association held leave with it and each one it was primary for
// promotes another holder (typed contact roles design D2).
func (s *server) DeleteCustomersByIdContactsByContactId(ctx context.Context, req gen.DeleteCustomersByIdContactsByContactIdRequestObject) (gen.DeleteCustomersByIdContactsByContactIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.GetAssociationWithContact(ctx, store.GetAssociationWithContactParams{CustomerID: req.Id, ContactID: req.ContactId}); errors.Is(err, pgx.ErrNoRows) {
		return gen.DeleteCustomersByIdContactsByContactId404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("customers: get association: %w", err)
	}

	// Resolved before the transaction opens: this handler always records a
	// "detached" event once it reaches here (the 404 case wastes one call).
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	now := s.deps.Clock()
	err = db.RetrySerializable(ctx, contactRoleWriteAttempts, func() error {
		return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			txq := store.New(tx)
			if _, err := txq.LockCustomer(ctx, req.Id); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return errAssociationTargetNotFound
				}
				return err
			}
			// Re-read under the lock, the same reason the PUT does: the 404
			// above was decided outside it.
			locked, err := txq.GetAssociationWithContact(ctx, store.GetAssociationWithContactParams{CustomerID: req.Id, ContactID: req.ContactId})
			if errors.Is(err, pgx.ErrNoRows) {
				return errAssociationTargetNotFound
			}
			if err != nil {
				return err
			}
			held, err := contactRolesOf(ctx, txq, req.Id, req.ContactId)
			if err != nil {
				return err
			}

			// The association row goes first, and its roles go with it through
			// the composite foreign key's ON DELETE CASCADE (migration 00025).
			// Only then can another holder be promoted: while this contact's
			// is_primary row still exists, promoting one would put two
			// primaries of one role in ux_customer_contact_roles_primary at
			// once — the same delete-before-promote order
			// DeleteCustomersByIdAddressesByAddressId keeps, for the same
			// index-shaped reason.
			if err := txq.DeleteAssociation(ctx, store.DeleteAssociationParams{CustomerID: req.Id, ContactID: req.ContactId}); err != nil {
				return err
			}
			promotions, err := releaseRoles(ctx, txq, req.Id, req.ContactId, held)
			if err != nil {
				return err
			}

			if err := recordContactDetached(ctx, txq, now, req.Id, contactFromAssociationRow(locked), locked.Title, held,
				locked.AssociationPhone, locked.AssociationEmail, act.Kind, act.Display, act.UserID); err != nil {
				return err
			}
			return recordPromotions(ctx, txq, now, req.Id, promotions, act)
		})
	})
	switch {
	case errors.Is(err, errAssociationTargetNotFound):
		return gen.DeleteCustomersByIdContactsByContactId404Response{}, nil
	case err != nil:
		return nil, fmt.Errorf("customers: detach contact: %w", err)
	}
	return gen.DeleteCustomersByIdContactsByContactId204Response{}, nil
}

// recordPromotions records design D4's promotion event for each contact that
// became a role's primary as a side effect of the write just made: on the
// promoted contact, with the acting user who caused it. It reads each promoted
// association back — the event's payload is that association's own title,
// phone, email and full role set, not a fragment — which is one query per
// promotion and at most three per write, since a contact can be primary for at
// most the three roles there are.
func recordPromotions(ctx context.Context, txq *store.Queries, now time.Time, customerID int32, promotions []rolePromotion, act actor) error {
	for _, p := range promotions {
		row, err := txq.GetAssociationWithContact(ctx, store.GetAssociationWithContactParams{CustomerID: customerID, ContactID: p.ContactID})
		if err != nil {
			return err
		}
		roles, err := contactRolesOf(ctx, txq, customerID, p.ContactID)
		if err != nil {
			return err
		}
		if err := recordContactPromoted(ctx, txq, now, customerID, contactFromAssociationRow(row), p.Role,
			row.Title, roles, row.AssociationPhone, row.AssociationEmail, act.Kind, act.Display, act.UserID); err != nil {
			return err
		}
	}
	return nil
}
