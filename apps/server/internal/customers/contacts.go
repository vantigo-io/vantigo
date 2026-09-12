package customers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

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

// validatedAssociation is the validated, normalized values of a
// CustomerContactRequest (Endpoints/Customers/Contacts/Dtos/CustomerContactRequest.cs).
type validatedAssociation struct {
	Role         string
	Phone, Email *string
}

// validateCustomerContactRequest is CustomerContactRequest.TryApplyTo
// (Endpoints/Customers/Contacts/Dtos/CustomerContactRequest.cs:22-66): role
// is required, phone and email optional with blank treated as absent. Shared
// by Attach (whose Request embeds the same three fields, "Connection") and
// Update.
func validateCustomerContactRequest(role string, phone, email *string) (validatedAssociation, map[string][]string) {
	errs := map[string][]string{}

	r, err := validateContactRole(role)
	if err != "" {
		errs["role"] = []string{err}
	}
	p := validateOptionalPhone("phone", phone, errs)
	e := validateOptionalEmail("email", email, errs)

	if len(errs) > 0 {
		return validatedAssociation{}, errs
	}
	return validatedAssociation{Role: r, Phone: p, Email: e}, nil
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
		return gen.GetCustomersContacts400ApplicationProblemPlusJSONResponse(problem("Invalid query parameters", strings.Join(msgs, " "))), nil
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
		Pagination: paginationMetadata(page, pageSize, int32(total)),
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
		return gen.PostCustomersContacts400ApplicationProblemPlusJSONResponse(validationProblem("Invalid contact", errs)), nil
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
		return gen.PutCustomersContactsById400ApplicationProblemPlusJSONResponse(validationProblem("Invalid contact", errs)), nil
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
// contact row (customers inventory §4), then every association it still
// carries is recorded as a "removed" timeline event against that
// association's customer, and only then is the contact (and, via ON DELETE
// CASCADE, its associations) deleted — all in one transaction.
func (s *server) DeleteCustomersContactsById(ctx context.Context, req gen.DeleteCustomersContactsByIdRequestObject) (gen.DeleteCustomersContactsByIdResponseObject, error) {
	now := s.deps.Clock()
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		contact, err := txq.GetContactForUpdate(ctx, req.Id)
		if err != nil {
			return err
		}
		associations, err := txq.ListAssociationsForContact(ctx, req.Id)
		if err != nil {
			return err
		}
		for _, a := range associations {
			if err := recordContactRemoved(ctx, txq, now, a.CustomerID, contact, a.Role, a.Phone, a.Email); err != nil {
				return err
			}
		}
		return txq.DeleteContact(ctx, req.Id)
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
	data := make([]gen.GetContactCustomersContactCustomerResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, gen.GetContactCustomersContactCustomerResponse{
			Customer: gen.GetContactCustomersCustomerReference{Id: r.ID, CustomerNumber: r.CustomerNumber, Name: r.Name},
			Role:     r.Role, Phone: r.Phone, Email: r.Email,
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
	data := make([]gen.CustomerContactResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, gen.CustomerContactResponse{
			Contact: gen.ContactResponse{
				Id: r.ID, FirstName: r.FirstName, LastName: r.LastName,
				MiddleName: r.MiddleName, Prefix: r.Prefix, Suffix: r.Suffix, Phone: r.ContactPhone, Email: r.ContactEmail,
			},
			Role: r.Role, Phone: r.AssociationPhone, Email: r.AssociationEmail,
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

// PostCustomersByIdContacts Associate a contact with a customer
// (POST /api/v1/customers/{id}/contacts)
//
// AttachCustomerContactEndpoint.cs:18-68 (customers inventory §1.4): (1)
// connection field validation, before any database access at all; (2) a
// SELECT ... FOR UPDATE lock on the contact row plus the customer's own
// existence, both inside one transaction — 404 if either is missing; (3) the
// already-attached check, 409 if so. Validation runs before existence, the
// opposite order from PutCustomersByIdContactsByContactId below — pinned by
// TestAttachContact_InvalidConnectionAgainstUnknownCustomer_Returns400.
func (s *server) PostCustomersByIdContacts(ctx context.Context, req gen.PostCustomersByIdContactsRequestObject) (gen.PostCustomersByIdContactsResponseObject, error) {
	body := gen.AttachCustomerContactRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	assoc, errs := validateCustomerContactRequest(body.Role, body.Phone, body.Email)
	if errs != nil {
		return gen.PostCustomersByIdContacts400ApplicationProblemPlusJSONResponse(validationProblem("Invalid contact association", errs)), nil
	}

	now := s.deps.Clock()
	var response gen.CustomerContactResponse
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)

		customer, err := txq.GetCustomer(ctx, req.Id)
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
			CustomerID: req.Id, ContactID: body.ContactId, Role: assoc.Role, Phone: assoc.Phone, Email: assoc.Email,
		}); err != nil {
			return err
		}
		if err := recordContactAttached(ctx, txq, now, customer.ID, contact, assoc.Role, assoc.Phone, assoc.Email); err != nil {
			return err
		}

		response = gen.CustomerContactResponse{Contact: contactResponse(contact), Role: assoc.Role, Phone: assoc.Phone, Email: assoc.Email}
		return nil
	})
	switch {
	case errors.Is(err, errAssociationTargetNotFound):
		return gen.PostCustomersByIdContacts404Response{}, nil
	case errors.Is(err, errAlreadyAttached):
		detail := fmt.Sprintf("Contact %d is already associated with customer %d.", body.ContactId, req.Id)
		return gen.PostCustomersByIdContacts409ApplicationProblemPlusJSONResponse(problemStatus("Contact already associated", detail, http.StatusConflict)), nil
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
// A change to role, phone or email records a "relationship updated" timeline
// event (UpdateCustomerContactEndpoint.cs:43-49); resubmitting the same
// values records nothing.
func (s *server) PutCustomersByIdContactsByContactId(ctx context.Context, req gen.PutCustomersByIdContactsByContactIdRequestObject) (gen.PutCustomersByIdContactsByContactIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	existing, err := q.GetAssociationWithContact(ctx, store.GetAssociationWithContactParams{CustomerID: req.Id, ContactID: req.ContactId})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCustomersByIdContactsByContactId404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get association: %w", err)
	}

	body := gen.CustomerContactRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	assoc, errs := validateCustomerContactRequest(body.Role, body.Phone, body.Email)
	if errs != nil {
		return gen.PutCustomersByIdContactsByContactId400ApplicationProblemPlusJSONResponse(validationProblem("Invalid contact association", errs)), nil
	}

	changed := existing.Role != assoc.Role || deref(existing.AssociationPhone) != deref(assoc.Phone) || deref(existing.AssociationEmail) != deref(assoc.Email)

	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if err := txq.UpdateAssociation(ctx, store.UpdateAssociationParams{
			CustomerID: req.Id, ContactID: req.ContactId, Role: assoc.Role, Phone: assoc.Phone, Email: assoc.Email,
		}); err != nil {
			return err
		}
		if changed {
			return recordContactRelationshipUpdated(ctx, txq, now, req.Id, contactFromAssociationRow(existing), assoc.Role, assoc.Phone, assoc.Email)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("customers: update association: %w", err)
	}

	return gen.PutCustomersByIdContactsByContactId200JSONResponse{
		Contact: contactResponse(contactFromAssociationRow(existing)), Role: assoc.Role, Phone: assoc.Phone, Email: assoc.Email,
	}, nil
}

// DeleteCustomersByIdContactsByContactId Remove a contact association from a customer
// (DELETE /api/v1/customers/{id}/contacts/{contactId})
//
// DetachCustomerContactEndpoint.cs:15-36: the contact itself is kept, only
// the association row is removed, and a "detached" timeline event is
// recorded against the customer.
func (s *server) DeleteCustomersByIdContactsByContactId(ctx context.Context, req gen.DeleteCustomersByIdContactsByContactIdRequestObject) (gen.DeleteCustomersByIdContactsByContactIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	existing, err := q.GetAssociationWithContact(ctx, store.GetAssociationWithContactParams{CustomerID: req.Id, ContactID: req.ContactId})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.DeleteCustomersByIdContactsByContactId404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get association: %w", err)
	}

	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if err := txq.DeleteAssociation(ctx, store.DeleteAssociationParams{CustomerID: req.Id, ContactID: req.ContactId}); err != nil {
			return err
		}
		return recordContactDetached(ctx, txq, now, req.Id, contactFromAssociationRow(existing), existing.Role, existing.AssociationPhone, existing.AssociationEmail)
	})
	if err != nil {
		return nil, fmt.Errorf("customers: detach contact: %w", err)
	}
	return gen.DeleteCustomersByIdContactsByContactId204Response{}, nil
}
