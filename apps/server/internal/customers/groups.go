package customers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is the vocabulary half of phase 4 delivery D (customer groups
// design D2): GET/POST /customers/groups and PUT/DELETE
// /customers/groups/{groupId}. The membership — PUT /customers/{id}/group and
// the group on every customer response — is group_membership.go.
//
// It is tags.go's vocabulary with two deliberate differences, and each is a
// decision rather than a variation:
//
//  1. **The second field is a payment term, not a colour.** It is validated by
//     the billing profile's own validatePaymentTermsDays (0-365,
//     billing_values.go) under this request's own field name, so one rule
//     covers a group's default and a customer's override and the two can never
//     disagree about what 366 days means. A CHECK on the column backs it
//     (migration 00027), because a default outside the range would be
//     inherited by every member.
//  2. **A group with members is never deleted.** The tags' cascade is right for
//     a label — a tag going away says nothing about the customer — and wrong
//     for a default: detaching the members would change every one of their
//     effective payment terms with no record on any customer. So DELETE counts
//     first and answers 409 group_in_use with that count, and the column's
//     foreign key is RESTRICT rather than SET NULL so a writer that raced the
//     count cannot get past it either.
//
// The vocabulary's own writes record no timeline event at all (design D2: the
// group is the vocabulary, not the customer), and that includes moving a
// group's default, which changes what every member inherits. That is by design
// and the docs say so — the alternative is an event on every member of a group,
// written by somebody who was editing a group rather than a customer.
//
// No new permission key: reads on customers:view, writes on customers:update
// (design D2). A group's name and default are installation policy, the same
// reasoning that put the tag vocabulary on customers:update, and a customer's
// own override stays where it is, behind customers:billing-manage.

// groupSummaryOf is CustomerGroupSummary's projection. customer_count arrives
// as a bigint from a count(*) sub-select and the contract's field is int32: a
// vocabulary with two billion members of one group is not a case worth a wider
// type, and the narrowing is here, once, rather than at each call site
// (tagSummaryOf's own reasoning).
func groupSummaryOf(id uuid.UUID, name string, defaultPaymentTermsDays *int32, customerCount int64) gen.CustomerGroupSummary {
	return gen.CustomerGroupSummary{
		Id: id, Name: name, DefaultPaymentTermsDays: defaultPaymentTermsDays, CustomerCount: int32(customerCount),
	}
}

// groupExistsConflict is the 409 both the create and the update answer for a
// name another group already holds, ignoring case. It carries code
// group_exists, the tags' tag_exists precedent, so a UI can say "you already
// have that group" and put the message under the one field the person typed in
// instead of showing a bare 409.
func groupExistsConflict() gen.CustomerConflictProblem {
	title := "Customer group already exists"
	detail := "A customer group with this name already exists. Group names are compared without regard to case."
	code := "group_exists"
	status := int32(http.StatusConflict)
	return gen.CustomerConflictProblem{Title: &title, Detail: &detail, Code: &code, Status: &status}
}

// groupInUseConflict is DELETE /customers/groups/{groupId}'s refusal for a
// group somebody still belongs to (design D2). The count is in the detail, not
// only in the code: "move them first" is only actionable if the caller learns
// how many there are, and the list's own groupId filter is where they are
// found. The singular is worth the branch — a refusal that says "1 customers"
// is the kind of thing nobody notices until a customer does.
func groupInUseConflict(members int64) gen.CustomerConflictProblem {
	title := "Customer group is in use"
	noun, pronoun := "customers", "them"
	if members == 1 {
		noun, pronoun = "customer", "it"
	}
	detail := fmt.Sprintf("This group still has %d %s. Move %s to another group, or out of every group, before deleting it.", members, noun, pronoun)
	code := "group_in_use"
	status := int32(http.StatusConflict)
	return gen.CustomerConflictProblem{Title: &title, Detail: &detail, Code: &code, Status: &status}
}

// customersGroupFK is the foreign key customers.customers.group_id carries
// (migration 00027, named by PostgreSQL's own convention). Named here because
// the delete matches it BY NAME rather than catching any violation: this is the
// one that means "somebody moved a customer into the group after I counted",
// and any other constraint failure is a bug the caller must hear about as a
// 500. Matched as a restrict_violation (23001), not a foreign_key_violation
// (23503): ON DELETE RESTRICT reports the former when the referenced row is
// deleted, so a 23503 check here would never fire and the race it exists for
// would surface as a 500 (db.IsRestrictViolation says why the two differ).
const customersGroupFK = "customers_group_id_fkey"

// validateGroupRequest is POST/PUT /customers/groups' shared validator: both
// fields checked independently and both errors reported together, keyed by the
// request's own field names — the module's all-errors-at-once shape
// (validateTagRequest, validateBillingProfile). An absent default is nil, never
// an error; only one outside 0-365 is.
func validateGroupRequest(name string, defaultPaymentTermsDays *int32) (string, *int32, map[string][]string) {
	errs := map[string][]string{}
	normalizedName, nameErr := validateGroupName(name)
	if nameErr != "" {
		errs["name"] = []string{nameErr}
	}
	days := validatePaymentTermsDays(defaultPaymentTermsDays, "defaultPaymentTermsDays", errs)
	if len(errs) > 0 {
		return "", nil, errs
	}
	return normalizedName, days, nil
}

// GetCustomersGroups List every customer group
// (GET /api/v1/customers/groups)
//
// Unpaged, name-ascending, each group with its member count — the tag
// vocabulary's own bet, restated (design D2): tens of groups, not thousands,
// and both the picker and the Manage groups modal want the whole list. An
// installation that outgrows it gets paging as an additive contract change.
func (s *server) GetCustomersGroups(ctx context.Context, _ gen.GetCustomersGroupsRequestObject) (gen.GetCustomersGroupsResponseObject, error) {
	q := store.New(s.deps.Pool)
	rows, err := q.ListCustomerGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("customers: list customer groups: %w", err)
	}
	data := make([]gen.CustomerGroupSummary, 0, len(rows))
	for _, row := range rows {
		data = append(data, groupSummaryOf(row.ID, row.Name, row.DefaultPaymentTermsDays, row.CustomerCount))
	}
	return gen.GetCustomersGroups200JSONResponse(data), nil
}

// PostCustomersGroups Create a customer group
// (POST /api/v1/customers/groups)
//
// The duplicate check is the INSERT's own unique violation rather than a SELECT
// first (PostCustomersTags' own reasoning): two callers creating 'Retail' at
// the same moment is exactly the race a check-then-insert loses, and
// ux_customers_groups_name_lower is the only thing that can decide it.
// db.IsUniqueViolation names the constraint, so a uuid collision on the primary
// key stays a 500 rather than being reported as a duplicate name.
func (s *server) PostCustomersGroups(ctx context.Context, req gen.PostCustomersGroupsRequestObject) (gen.PostCustomersGroupsResponseObject, error) {
	body := gen.CustomerGroupRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	name, days, errs := validateGroupRequest(body.Name, body.DefaultPaymentTermsDays)
	if errs != nil {
		return gen.PostCustomersGroups400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid customer group", errs)), nil
	}

	q := store.New(s.deps.Pool)
	group, err := q.InsertCustomerGroup(ctx, store.InsertCustomerGroupParams{
		ID: uuid.New(), Name: name, DefaultPaymentTermsDays: days, Now: s.deps.Clock(),
	})
	if err != nil {
		if db.IsUniqueViolation(err, "ux_customers_groups_name_lower") {
			return gen.PostCustomersGroups409ApplicationProblemPlusJSONResponse(groupExistsConflict()), nil
		}
		return nil, fmt.Errorf("customers: create customer group: %w", err)
	}
	// A group nobody belongs to yet: the count is 0 by construction, so this
	// needs no second read.
	return gen.PostCustomersGroups201JSONResponse(groupSummaryOf(group.ID, group.Name, group.DefaultPaymentTermsDays, 0)), nil
}

// PutCustomersGroupsByGroupId Rename a customer group or change its default payment term
// (PUT /api/v1/customers/groups/{groupId})
//
// A FULL REPLACE of both fields (design D2): a body without
// defaultPaymentTermsDays CLEARS the group's default rather than leaving the one
// it had, so the request says what the group is rather than what changed —
// oapi-codegen's *int32 already collapses absent and null, which is why the
// validator never has to tell them apart.
//
// No timeline event anywhere, the vocabulary's rule, and that includes the
// default: changing it changes what every member inherits, at once, by design.
// Renaming a group to the name it already has is a plain 200, not a conflict
// with itself — the unique index compares lower(name) and the row being updated
// is the row being compared against, so the database agrees.
//
// One statement, UPDATE … RETURNING with the count (UpdateCustomerTagRow's own
// reason): the body this answers is what the Manage groups modal keeps on
// screen, so the count has to be the one the list would report, and reading it
// back separately left a window in which a group deleted just after the update
// turned a successful write into a 404.
func (s *server) PutCustomersGroupsByGroupId(ctx context.Context, req gen.PutCustomersGroupsByGroupIdRequestObject) (gen.PutCustomersGroupsByGroupIdResponseObject, error) {
	body := gen.CustomerGroupRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	name, days, errs := validateGroupRequest(body.Name, body.DefaultPaymentTermsDays)
	if errs != nil {
		return gen.PutCustomersGroupsByGroupId400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid customer group", errs)), nil
	}

	q := store.New(s.deps.Pool)
	updated, err := q.UpdateCustomerGroupRow(ctx, store.UpdateCustomerGroupRowParams{
		ID: req.GroupId, Name: name, DefaultPaymentTermsDays: days, Now: s.deps.Clock(),
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return gen.PutCustomersGroupsByGroupId404Response{}, nil
	case db.IsUniqueViolation(err, "ux_customers_groups_name_lower"):
		return gen.PutCustomersGroupsByGroupId409ApplicationProblemPlusJSONResponse(groupExistsConflict()), nil
	case err != nil:
		return nil, fmt.Errorf("customers: update customer group: %w", err)
	}
	return gen.PutCustomersGroupsByGroupId200JSONResponse(
		groupSummaryOf(updated.ID, updated.Name, updated.DefaultPaymentTermsDays, updated.CustomerCount)), nil
}

// DeleteCustomersGroupsByGroupId Delete a customer group
// (DELETE /api/v1/customers/groups/{groupId})
//
// Two statements, in this order and for this reason (design D2): the member
// count, so a refusal can say what has to be moved, then the delete. The FK's
// RESTRICT is not a second opinion but the backstop for the window between
// them — a customer moved into the group in that moment raises 23001 on
// customers_group_id_fkey, which is answered as the same 409 by counting again,
// exactly as PutCustomersByIdTags maps its own late foreign-key violation back
// to the field error the resolve would have given a moment later.
//
// Not idempotent — deleting an already-absent group is a 404, the tags' own
// asymmetry — because "it is gone" and "it was never there" are different
// answers to somebody who just clicked Delete twice. No transaction and no
// retry: nothing here shares a lock order with another write the way tags'
// cascade does with its set replace, so there is no deadlock to retry.
func (s *server) DeleteCustomersGroupsByGroupId(ctx context.Context, req gen.DeleteCustomersGroupsByGroupIdRequestObject) (gen.DeleteCustomersGroupsByGroupIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	// The count's parameter is a *uuid.UUID because sqlc types it from the
	// nullable column it compares against; a path id is never nil.
	groupID := req.GroupId
	members, err := q.CountCustomerGroupMembers(ctx, &groupID)
	if err != nil {
		return nil, fmt.Errorf("customers: count customer group members: %w", err)
	}
	if members > 0 {
		return gen.DeleteCustomersGroupsByGroupId409ApplicationProblemPlusJSONResponse(groupInUseConflict(members)), nil
	}

	rows, err := q.DeleteCustomerGroup(ctx, req.GroupId)
	if db.IsRestrictViolation(err, customersGroupFK) {
		// Somebody moved a customer into the group between the count and this
		// statement. Count again and answer the refusal the count itself would
		// have given: the caller asked to delete a group that has members, which
		// is a fact about the group whichever side of the DELETE it became true
		// on.
		late, cerr := q.CountCustomerGroupMembers(ctx, &groupID)
		if cerr != nil {
			return nil, fmt.Errorf("customers: re-count customer group members after a restrict violation: %w", cerr)
		}
		if late > 0 {
			return gen.DeleteCustomersGroupsByGroupId409ApplicationProblemPlusJSONResponse(groupInUseConflict(late)), nil
		}
		// Empty again, so whoever was in it has since moved out and there is no
		// refusal to report. The delete did fail, and falling through to the 500
		// says so rather than inventing a count of zero.
	}
	if err != nil {
		return nil, fmt.Errorf("customers: delete customer group: %w", err)
	}
	if rows == 0 {
		return gen.DeleteCustomersGroupsByGroupId404Response{}, nil
	}
	return gen.DeleteCustomersGroupsByGroupId204Response{}, nil
}
