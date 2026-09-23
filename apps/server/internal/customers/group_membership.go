package customers

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is the membership half of phase 4 delivery D (customer groups
// design D3): PUT /customers/{id}/group. The vocabulary it writes an id from is
// groups.go.
//
// It is owner.go's PUT with one difference, and the difference is what makes it
// simpler rather than merely different: the value lives in THIS module's own
// table (customers.customer_groups, migration 00027), so the candidate is
// resolved with one query instead of a call into identity's directory — no
// out-of-process call to keep out of the transaction, and a real foreign key
// under the column, so the write cannot land on a group that does not exist
// even if this handler's own check were wrong.
//
// Everything else is owner.go's, deliberately: the ordering is (1) the customer
// lookup, 404; (2) a supplied revision that disagrees with the row just read,
// 409 — ahead of the no-op check, so resubmitting the current group with a stale
// revision is still a conflict; (3) the no-op check, nil-safe, which writes
// nothing at all (customers foundation design D5); (4) the candidate's own
// existence, 400 keyed groupId; (5) the guarded write and its timeline event in
// one transaction.
//
// Steps 3 and 4 are in that order for owner.go's own reason: the check "does
// this group exist" applies to a CHANGE of group, and a no-op resubmit must not
// be refused for the state the customer is already in. Unlike the owner's
// disabled-account case this is mostly theoretical — a group with members
// cannot be deleted (design D2) — and it is kept anyway, because the ordering
// is the module's rule and a reader comparing the two files must find the same
// shape.

// groupMembershipReadAttempts is how many times PutCustomersByIdGroup reads
// the customer and its membership, looking for a consistent pair, before a
// request without a revision gives up with the revision conflict. Three
// attempts because one retry covers the ordinary case of a single concurrent
// write. A customer that moves under every one of three attempts is changing
// faster than any answer about it would stay true.
const groupMembershipReadAttempts = 3

// groupNotFound is the field error for a groupId no group holds, worded as
// ownerNotFound (owner.go) and tagNotFound (tags.go) word their own. A field
// error and not a 404: the customer exists and the caller may edit it, so what
// is wrong is the body they sent.
func groupNotFound(id uuid.UUID) string {
	return fmt.Sprintf("Customer group %s does not exist", id)
}

// PutCustomersByIdGroup Put a customer in a group, or take it out of every group
// (PUT /api/v1/customers/{id}/group)
func (s *server) PutCustomersByIdGroup(ctx context.Context, req gen.PutCustomersByIdGroupRequestObject) (gen.PutCustomersByIdGroupResponseObject, error) {
	body := gen.PutCustomerGroupRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	// The customer and its current group are two separate unlocked reads, so
	// a write can land between them. The owner handler has no such window,
	// because its before comes from the same read as its revision check. Here a
	// move landing in between would pair GetCustomer's revision N with the
	// group at N+1. If the caller asked for exactly that group, the no-op check
	// below would then answer 200 with revision N beside a group that revision
	// never had. CustomerGroupMembership therefore returns the revision it saw,
	// and the two revisions must agree before anything below trusts the pair.
	//
	// What a disagreement means depends on whether the caller sent a revision.
	// With one, the row has moved past it, so the caller's revision is stale:
	// the revision conflict, in the words the guarded write's own fallback
	// uses. Without one, the caller asked for the change to apply
	// unconditionally (customers foundation design D5; the guarded write's
	// "expected_revision IS NULL OR …" says the same). Refusing it would break
	// that rule, and the owner endpoint would succeed in the same race. So both
	// reads run again, and the handler proceeds with the first consistent
	// pair. The retries are bounded at groupMembershipReadAttempts. A customer
	// still changing under every attempt answers the same 409, which is the
	// honest answer: this customer keeps changing, try again.
	q := store.New(s.deps.Pool)
	var existing store.GetCustomerRow
	var current store.CustomerGroupMembershipRow
	for attempt := 1; ; attempt++ {
		var err error
		existing, err = q.GetCustomer(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return gen.PutCustomersByIdGroup404Response{}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("customers: get customer: %w", err)
		}

		if body.Revision != nil && *body.Revision != existing.Revision {
			return gen.PutCustomersByIdGroup409ApplicationProblemPlusJSONResponse(customerRevisionConflict(*body.Revision, existing.Revision)), nil
		}

		// The customer's current group, with the name the event's before
		// snapshot needs: one query rather than a column on GetCustomer (see
		// CustomerGroupMembership's own comment in queries/groups.sql).
		// pgx.ErrNoRows would mean the customer vanished between the two reads,
		// which is the same 404 as above.
		//
		// current.GroupID is *uuid.UUID (the customer's own nullable column) and
		// current.GroupName is *string. If the generated row types ever say
		// otherwise, fix the QUERY, not this handler: uuidPtrEqual and deref
		// below both take pointers, and a "" that means "no group" would be a
		// second spelling of the nil the column already has.
		current, err = q.CustomerGroupMembership(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return gen.PutCustomersByIdGroup404Response{}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("customers: read customer group membership: %w", err)
		}

		if current.Revision == existing.Revision {
			break
		}
		if body.Revision != nil || attempt == groupMembershipReadAttempts {
			return gen.PutCustomersByIdGroup409ApplicationProblemPlusJSONResponse(customerRevisionConflict(existing.Revision, current.Revision)), nil
		}
	}

	includeIdentity := s.hasPermission(ctx, legalIdentityView)
	before, after := current.GroupID, body.GroupId

	respond := func(row customerRow) (gen.PutCustomersByIdGroupResponseObject, error) {
		dec, err := s.decorate(ctx, q, row)
		if err != nil {
			return nil, err
		}
		return gen.PutCustomersByIdGroup200JSONResponse(safeCustomerResponse(row, includeIdentity, dec)), nil
	}

	if uuidPtrEqual(before, after) {
		// The same group, nil included: nothing written, no revision bump, no
		// event, and no actor resolved (customers foundation design D5). The
		// response is still the whole customer, decorated — which is why the
		// no-op is answered here rather than as a 304 or an empty body.
		summary, err := q.CustomerTimelineSummary(ctx, req.Id)
		if err != nil {
			return nil, fmt.Errorf("customers: timeline summary: %w", err)
		}
		return respond(fromCustomerRow(existing, summary))
	}

	var afterSnapshot *groupSnapshot
	if after != nil {
		group, err := q.GetCustomerGroup(ctx, *after)
		if errors.Is(err, pgx.ErrNoRows) {
			return gen.PutCustomersByIdGroup400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(
				"Invalid customer group", map[string][]string{"groupId": {groupNotFound(*after)}})), nil
		}
		if err != nil {
			return nil, fmt.Errorf("customers: resolve customer group: %w", err)
		}
		afterSnapshot = &groupSnapshot{GroupID: group.ID, Name: group.Name}
	}

	var beforeSnapshot *groupSnapshot
	if before != nil {
		// The name comes from the join this handler already made, not from a
		// second read: the group cannot have been deleted while this customer
		// belonged to it (the foreign key's RESTRICT, design D2), so the name
		// read a moment ago is the name to snapshot.
		beforeSnapshot = &groupSnapshot{GroupID: *before, Name: deref(current.GroupName)}
	}

	now := s.deps.Clock()
	// Resolved before the transaction opens, and only here: by this point the
	// handler always records customer.group_changed (the no-op returned above),
	// so the actor is always needed (customers foundation design D1).
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	var updated store.SetCustomerGroupRow
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var err error
		updated, err = txq.SetCustomerGroup(ctx, store.SetCustomerGroupParams{
			ID: req.Id, GroupID: after, UpdatedAt: now, ExpectedRevision: body.Revision,
		})
		if err != nil {
			return err
		}
		return recordCustomerGroupChanged(ctx, txq, now, req.Id, beforeSnapshot, afterSnapshot, act.Kind, act.Display, act.UserID)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// The guarded UPDATE matched no row: a concurrent writer moved the
		// revision between the read above and this write, answered by re-reading
		// and reporting the row's now-current revision — the same race
		// PutCustomersByIdOwner's own guarded write answers, in the same words
		// and with no code.
		fresh, ferr := q.GetCustomer(ctx, req.Id)
		if errors.Is(ferr, pgx.ErrNoRows) {
			return gen.PutCustomersByIdGroup404Response{}, nil
		}
		if ferr != nil {
			return nil, fmt.Errorf("customers: re-read customer after conflict: %w", ferr)
		}
		return gen.PutCustomersByIdGroup409ApplicationProblemPlusJSONResponse(customerRevisionConflict(existing.Revision, fresh.Revision)), nil
	case db.IsForeignKeyViolation(err, customersGroupFK):
		// The group was deleted between the resolve above and this write — the
		// one window the resolve cannot close, since nothing here locks the
		// vocabulary (and design D2 makes it narrow: only an EMPTY group can be
		// deleted, so the TARGET group must have had no members a moment ago —
		// the customer's own current group, if any, is irrelevant). Answered as the field error the resolve itself would
		// have given a moment later, exactly as PutCustomersByIdTags answers its
		// own late foreign-key violation.
		return gen.PutCustomersByIdGroup400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(
			"Invalid customer group", map[string][]string{"groupId": {groupNotFound(*after)}})), nil
	case err != nil:
		return nil, fmt.Errorf("customers: set customer group: %w", err)
	}

	summary, err := q.CustomerTimelineSummary(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("customers: timeline summary: %w", err)
	}
	return respond(fromSetCustomerGroupRow(updated, summary))
}
