package customers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is the owner half of phase 4 delivery A (owner and tags design
// D1): PUT /customers/{id}/owner, GET /customers/assignable-users, and the
// decoration every customer response goes through on its way out.
//
// The owner is a column on customers.customers, which is the decision the
// whole file follows from: it shares the row's revision, so this PUT is
// contact_info.go's PUT with one column instead of three — same ordering, same
// guard, same no-op rule, same 409 — and a concurrent edit cannot lose it.
// What is new is that the VALUE belongs to another module: identity's users,
// reachable only through contracts.UserDirectory (Deps.Users). That has two
// consequences neither of which is optional:
//
//   - every directory call happens OUTSIDE a transaction (actor.go's rule):
//     an out-of-process call under a row lock is how an outage becomes a
//     database incident. This handler resolves the candidate, the previous
//     owner's name and the actor before db.WithTx opens, and decorates the
//     response after it commits.
//   - a stored owner_user_id can outlive the account it names. Migration
//     00024 deliberately has no foreign key, so the three states the
//     directory can report — a name, a disabled account, nothing at all —
//     are all reachable, and customerDecoration.owner below is the one place
//     each of them turns into a response.

// assignableOwnerLimit is how many candidates the owner picker's search
// answers, and the same twenty projects' own assignable-user search settled on
// (internal/projects/people.go): a picker's first page, not a report. Someone
// who cannot find a colleague in twenty rows types more of their name.
const assignableOwnerLimit = 20

// ownerNotFound and ownerDisabled are the two ways ownerUserId can fail,
// worded as projects words its own (people.go:39-45). Both are field errors
// rather than 404s: the customer exists and the caller may edit it, so what is
// wrong is the body they sent.
func ownerNotFound(id uuid.UUID) string {
	return fmt.Sprintf("User %s does not exist", id)
}

func ownerDisabled(id uuid.UUID) string {
	return fmt.Sprintf("User %s is disabled and cannot own a customer", id)
}

// uuidPtrEqual reports whether a and b name the same user, nil (unowned)
// included — the comparison PutCustomersByIdOwner's no-op check makes. The
// nil/nil case is the one an implementation that only compares dereferenced
// values gets wrong, and it is a real request: clearing the owner of a
// customer that has none must write nothing at all.
func uuidPtrEqual(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// customerDecoration is everything a SafeCustomerResponse carries that is not
// on the customer row: the owner's display name, borrowed from identity's
// directory for the length of one response (owner and tags design D1), the
// customer's tags, which live in their own table (D2), the customer's group,
// whose name lives in the group vocabulary (customer groups design D3), and the
// customer it was merged into, if any (customers merge design D3), and its
// anonymisation (customers GDPR design D4). It exists so safeCustomerResponse
// knows one shape whether it is rendering one customer or a page of
// twenty-five, and so the five lookups happen once per response rather than
// once per row.
type customerDecoration struct {
	owners     map[uuid.UUID]contracts.UserEntry
	tags       map[int32][]gen.CustomerTag
	groups     map[int32]gen.CustomerGroupRef
	mergedInto map[int32]gen.CustomerReference
	// anonymisations is each customer's anonymisation, scheduled or done
	// (customers GDPR design D4); a customer never scheduled has no entry.
	anonymisations map[int32]gen.CustomerAnonymisation
}

// decorate resolves the owners, tags, groups, merge markers and anonymisations
// of rows in one directory call and four queries. q must be a pool-backed store.Queries,
// never a transaction's: the directory call inside is out-of-process and must
// not happen under a lock, so every caller decorates after its write has
// committed.
//
// An empty rows is not an error and makes no calls at all — an empty list page
// is an ordinary answer, and asking the directory about nobody is a round trip
// for a map that will stay empty.
func (s *server) decorate(ctx context.Context, q *store.Queries, rows ...customerRow) (customerDecoration, error) {
	return s.decorateKnowing(ctx, q, nil, rows...)
}

// decorateKnowing is decorate for the one caller that has ALREADY resolved an
// owner it is about to render: PutCustomersByIdOwner looks the new owner up to
// decide whether it may own a customer at all, and that entry is the very entry
// the decoration would fetch again a moment later. Passing it in makes a change
// of owner cost two directory calls — the candidate and the actor — instead of
// three. known is nil for every other caller, and for a PUT that CLEARS the
// owner, which resolves nobody.
//
// It is deliberately not folded into decorate's own signature: nine of the
// ten callers have nothing to pass, and a nil first argument at each of them
// would be noise standing in for the one place this matters. Everything
// decorate's own doc comment says still holds, the pool-backed q included.
func (s *server) decorateKnowing(ctx context.Context, q *store.Queries, known *contracts.UserEntry, rows ...customerRow) (customerDecoration, error) {
	dec := customerDecoration{
		owners:         map[uuid.UUID]contracts.UserEntry{},
		tags:           map[int32][]gen.CustomerTag{},
		groups:         map[int32]gen.CustomerGroupRef{},
		mergedInto:     map[int32]gen.CustomerReference{},
		anonymisations: map[int32]gen.CustomerAnonymisation{},
	}
	if len(rows) == 0 {
		return dec, nil
	}

	customerIDs := make([]int32, 0, len(rows))
	userIDs := make([]uuid.UUID, 0, len(rows))
	seen := make(map[uuid.UUID]bool, len(rows))
	if known != nil {
		// Seeded into both maps at once: dec.owners so the response names this
		// user, seen so the distinct pass below does not ask the directory for an
		// answer the caller already has.
		dec.owners[known.ID] = *known
		seen[known.ID] = true
	}
	for _, r := range rows {
		customerIDs = append(customerIDs, r.ID)
		if r.OwnerUserID == nil || seen[*r.OwnerUserID] {
			continue
		}
		// Distinct ids only: a page where one person owns every row must ask
		// the directory about them once.
		seen[*r.OwnerUserID] = true
		userIDs = append(userIDs, *r.OwnerUserID)
	}

	links, err := q.CustomerTagsForCustomers(ctx, customerIDs)
	if err != nil {
		return customerDecoration{}, fmt.Errorf("customers: load customer tags: %w", err)
	}
	for _, l := range links {
		dec.tags[l.CustomerID] = append(dec.tags[l.CustomerID], gen.CustomerTag{Id: l.ID, Name: l.Name, Color: l.Color})
	}

	// The group is one more batched query over the same customer ids (customer
	// groups design D3), and it is a query rather than a column on every
	// customer row's own SELECT for the tags' reason: one shape of
	// group-on-a-response, one place it can be wrong, and five existing row
	// types that would otherwise each have to grow a column they have no other
	// use for. A customer in no group simply has no row here.
	groups, err := q.CustomerGroupsForCustomers(ctx, customerIDs)
	if err != nil {
		return customerDecoration{}, fmt.Errorf("customers: load customer groups: %w", err)
	}
	for _, g := range groups {
		dec.groups[g.CustomerID] = gen.CustomerGroupRef{Id: g.ID, Name: g.Name}
	}

	// The merge marker is one more batched query over the same ids (customers
	// merge design D3), for the group's reason: the survivor's number and name
	// live on another row, and a join on every customer row type would widen
	// five of them for a field almost every customer answers without.
	markers, err := q.MergedIntoForCustomers(ctx, customerIDs)
	if err != nil {
		return customerDecoration{}, fmt.Errorf("customers: load merge markers: %w", err)
	}
	for _, m := range markers {
		dec.mergedInto[m.CustomerID] = gen.CustomerReference{Id: m.ID, CustomerNumber: m.CustomerNumber, Name: m.Name}
	}

	// The anonymisation is one more batched query over the same ids (customers
	// GDPR design D4), for the merge marker's reason: two columns almost every
	// customer answers without, on five row types that would otherwise each
	// grow them.
	schedules, err := q.AnonymisationForCustomers(ctx, customerIDs)
	if err != nil {
		return customerDecoration{}, fmt.Errorf("customers: load anonymisations: %w", err)
	}
	for _, a := range schedules {
		dec.anonymisations[a.CustomerID] = gen.CustomerAnonymisation{
			AnonymiseOn:  openapi_types.Date{Time: a.AnonymiseOn.Time},
			AnonymisedAt: a.AnonymisedAt,
		}
	}

	if len(userIDs) > 0 {
		users, err := s.deps.Users.Users(ctx, userIDs)
		if err != nil {
			// A directory that cannot be reached is a 500, not a page of
			// customers with their owners quietly missing: "unowned" is a
			// claim, and this module has no business making it up.
			return customerDecoration{}, fmt.Errorf("customers: resolve customer owners: %w", err)
		}
		for _, u := range users {
			dec.owners[u.ID] = u
		}
	}
	return dec, nil
}

// owner is one customer's owner as the contract reports it, or nil when the
// customer is unowned. An id the directory answered nothing for is still an
// owner — reported as unknownUserDisplay and inactive, the actorFor
// precedent — because contracts.UserDirectory's absence means "no such
// account", not "the lookup failed" (its own doc comment), and design D1 keeps
// the customer's owner either way.
func (d customerDecoration) owner(id *uuid.UUID) *gen.CustomerOwner {
	if id == nil {
		return nil
	}
	if u, ok := d.owners[*id]; ok {
		return &gen.CustomerOwner{UserId: u.ID, DisplayName: u.DisplayName, Active: u.Active}
	}
	// unknownUserDisplay is actor.go's own constant, shared rather than a second
	// literal: an owner the directory forgot and an actor the directory forgot
	// are the same fact about the same directory, and two copies of the words
	// would drift the day one of them is reworded.
	return &gen.CustomerOwner{UserId: *id, DisplayName: unknownUserDisplay, Active: false}
}

// tagsFor is one customer's tags, never nil: the contract promises an array,
// and a customer with no tags answers [] rather than null (owner and tags
// design D2).
func (d customerDecoration) tagsFor(customerID int32) []gen.CustomerTag {
	if tags, ok := d.tags[customerID]; ok {
		return tags
	}
	return []gen.CustomerTag{}
}

// group is one customer's group as the contract reports it, or nil when the
// customer belongs to none — absent on the wire, never null, the idiom owner
// beside it already follows (customer groups design D3).
func (d customerDecoration) group(customerID int32) *gen.CustomerGroupRef {
	if g, ok := d.groups[customerID]; ok {
		return &g
	}
	return nil
}

// merged is the customer this one was merged into, or nil when it was not —
// absent on the wire, never null, the idiom group beside it follows.
func (d customerDecoration) merged(customerID int32) *gen.CustomerReference {
	if m, ok := d.mergedInto[customerID]; ok {
		return &m
	}
	return nil
}

// anonymisationOf is one customer's anonymisation, scheduled or done, or nil —
// absent on the wire, never null, the idiom merged beside it follows.
func (d customerDecoration) anonymisationOf(customerID int32) *gen.CustomerAnonymisation {
	if a, ok := d.anonymisations[customerID]; ok {
		return &a
	}
	return nil
}

// PutCustomersByIdOwner Set or clear a customer's owner
// (PUT /api/v1/customers/{id}/owner)
//
// Ordering is PutCustomersByIdContactInfo's, step for step, because the owner
// is a column on the same row: (1) the customer lookup, 404; (2) a supplied
// revision that disagrees with the row just read, 409 — ahead of the no-op
// check, so resubmitting the current owner with a stale revision is still a
// conflict; (3) the no-op check: the same owner (nil included) writes nothing
// at all (customers foundation design D5); (4) the candidate's own validation
// against the directory, 400 keyed ownerUserId; (5) the guarded write and its
// timeline event in one transaction.
//
// Steps 3 and 4 are in that order on purpose. The check "may this user own a
// customer" applies to a CHANGE of owner, and design D1 rules that an owner
// disabled after being assigned keeps the customer — so a no-op resubmit of a
// now-disabled owner must answer 200 with that owner, not a 400 telling the
// caller their own stored state is invalid.
func (s *server) PutCustomersByIdOwner(ctx context.Context, req gen.PutCustomersByIdOwnerRequestObject) (gen.PutCustomersByIdOwnerResponseObject, error) {
	body := gen.PutCustomerOwnerRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	existing, err := q.GetCustomer(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCustomersByIdOwner404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get customer: %w", err)
	}

	if body.Revision != nil && *body.Revision != existing.Revision {
		return gen.PutCustomersByIdOwner409ApplicationProblemPlusJSONResponse(customerRevisionConflict(*body.Revision, existing.Revision)), nil
	}

	includeIdentity := s.hasPermission(ctx, legalIdentityView)
	before, after := existing.OwnerUserID, body.OwnerUserId

	// newOwner, when the write below sets one, is the directory entry step (4)
	// already resolved: handed to the decoration so the response names the owner
	// without asking the directory a second time about the user this handler just
	// asked about. nil on the no-op path, which resolves nobody, and on a clear.
	respond := func(row customerRow, newOwner *contracts.UserEntry) (gen.PutCustomersByIdOwnerResponseObject, error) {
		dec, err := s.decorateKnowing(ctx, q, newOwner, row)
		if err != nil {
			return nil, err
		}
		return gen.PutCustomersByIdOwner200JSONResponse(safeCustomerResponse(row, includeIdentity, dec)), nil
	}

	if uuidPtrEqual(before, after) {
		summary, err := q.CustomerTimelineSummary(ctx, req.Id)
		if err != nil {
			return nil, fmt.Errorf("customers: timeline summary: %w", err)
		}
		return respond(fromCustomerRow(existing, summary), nil)
	}

	// Every directory call this handler makes happens here, before the
	// transaction: the candidate's eligibility, and the previous owner's name
	// for the event's before snapshot.
	var afterSnapshot *ownerSnapshot
	var newOwner *contracts.UserEntry
	if after != nil {
		user, err := s.deps.Users.User(ctx, *after)
		if err != nil {
			return nil, fmt.Errorf("customers: resolve owner candidate: %w", err)
		}
		switch {
		case user == nil:
			return gen.PutCustomersByIdOwner400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(
				"Invalid owner", map[string][]string{"ownerUserId": {ownerNotFound(*after)}})), nil
		case !user.Active:
			return gen.PutCustomersByIdOwner400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(
				"Invalid owner", map[string][]string{"ownerUserId": {ownerDisabled(*after)}})), nil
		}
		afterSnapshot = &ownerSnapshot{UserID: user.ID, DisplayName: user.DisplayName}
		newOwner = user
	}

	var beforeSnapshot *ownerSnapshot
	if before != nil {
		user, err := s.deps.Users.User(ctx, *before)
		if err != nil {
			return nil, fmt.Errorf("customers: resolve previous owner: %w", err)
		}
		// The name is snapshotted into the payload rather than resolved when
		// the timeline is read: an account renamed or deleted later must not
		// rewrite what the timeline says happened.
		beforeSnapshot = &ownerSnapshot{UserID: *before, DisplayName: unknownUserDisplay}
		if user != nil {
			beforeSnapshot.DisplayName = user.DisplayName
		}
	}

	now := s.deps.Clock()
	// Resolved before the transaction opens, and only here: by this point the
	// handler always records customer.owner_changed (the no-op returned above),
	// so the actor is always needed (customers foundation design D1).
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	var updated store.UpdateCustomerOwnerRow
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		// The customer's lock and the merged-away refusal first (customers
		// merge design D2), as every customer-scoped write takes them.
		if _, err := lockWritableCustomer(ctx, txq, req.Id); err != nil {
			return err
		}
		var err error
		updated, err = txq.UpdateCustomerOwner(ctx, store.UpdateCustomerOwnerParams{
			ID: req.Id, OwnerUserID: after, UpdatedAt: now, ExpectedRevision: body.Revision,
		})
		if err != nil {
			return err
		}
		return recordCustomerOwnerChanged(ctx, txq, now, req.Id, beforeSnapshot, afterSnapshot, act.Kind, act.Display, act.UserID)
	})
	switch {
	case isReadOnlyCustomer(err):
		return gen.PutCustomersByIdOwner409ApplicationProblemPlusJSONResponse(readOnlyProblem(err)), nil
	case errors.Is(err, pgx.ErrNoRows):
		// The guarded UPDATE matched no row: a concurrent writer moved the
		// revision between the read above and this write, answered by
		// re-reading and reporting the row's now-current revision — the same
		// race PutCustomersByIdContactInfo's own guarded write answers.
		fresh, ferr := q.GetCustomer(ctx, req.Id)
		if errors.Is(ferr, pgx.ErrNoRows) {
			return gen.PutCustomersByIdOwner404Response{}, nil
		}
		if ferr != nil {
			return nil, fmt.Errorf("customers: re-read customer after conflict: %w", ferr)
		}
		return gen.PutCustomersByIdOwner409ApplicationProblemPlusJSONResponse(customerRevisionConflict(existing.Revision, fresh.Revision)), nil
	case err != nil:
		return nil, fmt.Errorf("customers: update customer owner: %w", err)
	}

	summary, err := q.CustomerTimelineSummary(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("customers: timeline summary: %w", err)
	}
	return respond(fromUpdateCustomerOwnerRow(updated, summary), newOwner)
}

// GetCustomersAssignableUsers Search users assignable as a customer's owner
// (GET /api/v1/customers/assignable-users)
//
// The reason contracts.UserDirectory exists (its own doc comment, and
// projects' GetProjectsByIdAssignableUsers before this): the only user listing
// identity offers needs the authorization-management policy, so without this a
// salesperson who is not an administrator could not pick a colleague.
//
// It sat behind customers:update until follow-ups design D1 moved it to
// customers:view. The reason is the new caller: a timeline writer picking a
// follow-up's assignee holds customers:timeline-manage and need not hold
// customers:update at all, and what this operation answers — the display names
// of active users — is what every customer READER (customers:view) already
// sees, as the owner on every customer it reads. There was nothing here for
// customers:update to protect.
//
// Unlike projects' version there is nothing to exclude — a customer has one
// owner, not a team, so the current owner is a legitimate result and the
// picker wants it on the list — so this is SearchUsers and a cap, and nothing
// else. SearchUsers answers active users only, so a disabled account is never
// a candidate and nothing here has to filter for that.
func (s *server) GetCustomersAssignableUsers(ctx context.Context, req gen.GetCustomersAssignableUsersRequestObject) (gen.GetCustomersAssignableUsersResponseObject, error) {
	limit := int32(assignableOwnerLimit)
	if req.Params.Limit != nil {
		if *req.Params.Limit < 1 || *req.Params.Limit > assignableOwnerLimit {
			return gen.GetCustomersAssignableUsers400ApplicationProblemPlusJSONResponse(apicommon.Problem(
				"Invalid query parameters",
				fmt.Sprintf("'limit' must be between 1 and %d, but was %d.", assignableOwnerLimit, *req.Params.Limit))), nil
		}
		limit = *req.Params.Limit
	}

	query := ""
	if req.Params.Query != nil {
		query = strings.TrimSpace(*req.Params.Query)
	}

	found, err := s.deps.Users.SearchUsers(ctx, query, int(limit))
	if err != nil {
		return nil, fmt.Errorf("customers: search assignable users: %w", err)
	}
	data := make([]gen.CustomerAssignableUser, 0, len(found))
	for _, e := range found {
		data = append(data, gen.CustomerAssignableUser{UserId: e.ID, DisplayName: e.DisplayName})
		if len(data) == int(limit) {
			// The directory clamps its own limit, but this module's cap is its
			// own promise and is enforced here rather than assumed of a
			// collaborator.
			break
		}
	}
	return gen.GetCustomersAssignableUsers200JSONResponse(data), nil
}
