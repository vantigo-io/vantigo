package planning

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// This file is the terms' writes (design S14, S23, plan reading 2): set or
// replace them whole, and clear them. Each reads what it asks another module
// about before its transaction (rule 3) — the project, the customers a
// non-compete names, the actor — then takes the person's lock, as every
// write of an allocation does, re-reads the row and its terms FOR UPDATE,
// writes, and records terms_set or terms_cleared with the fields before and
// after. The terms have a revision of their own (§4.8): If-Match is
// optional on the PUT, left out on a row that has no terms yet.

// PutPlanningAllocationTerms Set an allocation's terms
// (PUT /api/v1/planning/allocations/{id}/terms)
func (s *server) PutPlanningAllocationTerms(ctx context.Context, req gen.PutPlanningAllocationTermsRequestObject) (gen.PutPlanningAllocationTermsResponseObject, error) {
	var revision *int32
	if req.Params.IfMatch != nil {
		r, ok := parseIfMatch(*req.Params.IfMatch)
		if !ok {
			return gen.PutPlanningAllocationTerms400ApplicationProblemPlusJSONResponse(invalid(invalidAllocationTitle, badIfMatch())), nil
		}
		revision = &r
	}
	var body gen.PlanningTermsInput
	if req.Body != nil {
		body = *req.Body
	}
	var out gen.PlanningAllocation
	err := s.retryMoved(func() error {
		var err error
		out, err = s.setTerms(ctx, req.Id, revision, body)
		return err
	})
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusBadRequest:
			return gen.PutPlanningAllocationTerms400ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusNotFound:
			return gen.PutPlanningAllocationTerms404Response{}, nil
		case http.StatusConflict:
			return gen.PutPlanningAllocationTerms409ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusServiceUnavailable:
			return gen.PutPlanningAllocationTerms503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.PutPlanningAllocationTerms200JSONResponse(out), nil
}

// setTerms is PUT /allocations/{id}/terms (S14): the body's field rules, the
// row known, its project open — a change of the terms is a change of the
// row (S4, plan reading 2) — each customer of a non-compete known to the
// directory; then under the person's lock the row re-read (one that moved
// since starts the write again, retryMoved), the terms row read FOR UPDATE
// and the revision judged — supplied and stored both absent, or equal —
// before the terms are inserted at revision 1 or replaced whole. The insert
// always records terms_set, its fields possibly none; a replace that changes
// nothing writes nothing and records nothing (amendment 18's rule for a
// row). The answer is the allocation with its terms and derived dates.
func (s *server) setTerms(ctx context.Context, id int64, revision *int32, body gen.PlanningTermsInput) (gen.PlanningAllocation, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return gen.PlanningAllocation{}, err
	}
	f := s.unknownProperties(ctx, termsRequestSchema)
	fields, invalidFields := parseTerms(body)
	f.merge(invalidFields)
	if f.failed() {
		return gen.PlanningAllocation{}, refuseInvalid(invalidAllocationTitle, f)
	}
	current, found, err := s.allocation(ctx, id)
	if err != nil {
		return gen.PlanningAllocation{}, err
	}
	if !found {
		return gen.PlanningAllocation{}, notFound()
	}
	project, err := s.projectEntry(ctx, current.ProjectID)
	if err != nil {
		return gen.PlanningAllocation{}, err
	}
	if projectClosed(project) {
		return gen.PlanningAllocation{}, refuseProjectClosed(project)
	}
	names, err := s.customerNames(ctx, fields.nonCompeteCustomerIDs)
	if err != nil {
		return gen.PlanningAllocation{}, err
	}
	if unknown := unknownCustomers(fields, names); unknown.failed() {
		return gen.PlanningAllocation{}, refuseInvalid(invalidAllocationTitle, unknown)
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return gen.PlanningAllocation{}, err
	}

	now := s.deps.Clock()
	var row store.PlanningAllocation
	err = s.withPersonLock(ctx, current.PersonID, func(ctx context.Context, txq *store.Queries) error {
		if row, err = lockedAllocation(ctx, txq, id); err != nil {
			return err
		}
		// The project judged and the lock taken were the row's at current's
		// revision; a row changed since may be on another of either.
		if row.Revision != current.Revision {
			return errRowMoved
		}
		before, err := lockedTerms(ctx, txq, id)
		if err != nil {
			return err
		}
		if err := checkTermsRevision(before, revision); err != nil {
			return err
		}
		after := termsRow(id, fields)
		changed := termsChanges(before, &after)
		if len(changed) == 0 && before != nil {
			return nil
		}
		if before == nil {
			_, err = txq.InsertTerms(ctx, store.InsertTermsParams{
				AllocationID: id, NoticeCount: after.NoticeCount, NoticeUnit: after.NoticeUnit,
				NoticeGivenOn: after.NoticeGivenOn, MinimumUntil: after.MinimumUntil,
				ReplacementNeedsApproval: after.ReplacementNeedsApproval, HandoverDays: after.HandoverDays,
				NonCompeteUntil: after.NonCompeteUntil, NonCompeteCustomerIds: after.NonCompeteCustomerIds,
				Reference: after.Reference, Now: now, UserID: &c.UserID,
			})
		} else {
			_, err = txq.UpdateTerms(ctx, store.UpdateTermsParams{
				AllocationID: id, NoticeCount: after.NoticeCount, NoticeUnit: after.NoticeUnit,
				NoticeGivenOn: after.NoticeGivenOn, MinimumUntil: after.MinimumUntil,
				ReplacementNeedsApproval: after.ReplacementNeedsApproval, HandoverDays: after.HandoverDays,
				NonCompeteUntil: after.NonCompeteUntil, NonCompeteCustomerIds: after.NonCompeteCustomerIds,
				Reference: after.Reference, Now: now, UserID: &c.UserID,
			})
		}
		if err != nil {
			return fmt.Errorf("planning: set the terms of allocation %d: %w", id, err)
		}
		e := allocationEvent(row, eventTermsSet)
		if before == nil {
			// The insert is a write and so a row in the record (S23), its
			// fields whatever it set — none, for an empty body.
			e.Payload = map[string]any{"fields": changed}
			return s.recordEvent(ctx, txq, who, e)
		}
		return s.recordChanges(ctx, txq, who, e, changed)
	})
	if err != nil {
		return gen.PlanningAllocation{}, err
	}
	people, err := s.peopleOfRows(ctx, c, []store.PlanningAllocation{row})
	if err != nil {
		return gen.PlanningAllocation{}, err
	}
	var person *contracts.PersonEntry
	if p, ok := people[row.PersonID]; ok {
		person = &p
	}
	out := []gen.PlanningAllocation{allocationResponse(row, person, project, c.Manage)}
	if err := s.applyTerms(ctx, out, true); err != nil {
		return gen.PlanningAllocation{}, err
	}
	return out[0], nil
}

// lockedTerms reads an allocation's terms row FOR UPDATE inside a write's
// transaction; nil when the allocation has none.
func lockedTerms(ctx context.Context, txq *store.Queries, id int64) (*store.PlanningAllocationTerm, error) {
	t, err := txq.LockTerms(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("planning: lock the terms of allocation %d: %w", id, err)
	}
	return &t, nil
}

// checkTermsRevision is plan reading 2's If-Match rule under the lock: no
// header on a row with no terms, or the terms' own revision; a header on a
// row without terms, none on a row with terms, or a stale one is the 409
// revision_conflict.
func checkTermsRevision(stored *store.PlanningAllocationTerm, supplied *int32) error {
	switch {
	case stored == nil && supplied == nil:
		return nil
	case stored == nil:
		return refuseConflict(conflict(codeRevisionConflict, "Revision conflict",
			fmt.Sprintf("The allocation has no terms; the supplied revision was %d. Leave If-Match out to set them.", *supplied)))
	case supplied == nil:
		return refuseConflict(conflict(codeRevisionConflict, "Revision conflict",
			fmt.Sprintf("The terms have revision %d; send it as If-Match to change them.", stored.Revision)))
	}
	return checkRevision("terms", stored.Revision, *supplied)
}

// DeletePlanningAllocationTerms Clear an allocation's terms
// (DELETE /api/v1/planning/allocations/{id}/terms)
func (s *server) DeletePlanningAllocationTerms(ctx context.Context, req gen.DeletePlanningAllocationTermsRequestObject) (gen.DeletePlanningAllocationTermsResponseObject, error) {
	err := s.retryMoved(func() error { return s.clearTerms(ctx, req.Id) })
	if r, ok := asRefusal(err); ok && r.status == http.StatusNotFound {
		return gen.DeletePlanningAllocationTerms404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.DeletePlanningAllocationTerms204Response{}, nil
}

// clearTerms is DELETE /allocations/{id}/terms (S14, plan reading 2): no
// If-Match, allowed on a closed project as a row's deletion is; under the
// person's lock the terms row deleted when there is one, recorded as
// terms_cleared with the fields before. A row with no terms is left as it
// is and nothing is recorded.
func (s *server) clearTerms(ctx context.Context, id int64) error {
	current, found, err := s.allocation(ctx, id)
	if err != nil {
		return err
	}
	if !found {
		return notFound()
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return err
	}
	return s.withPersonLock(ctx, current.PersonID, func(ctx context.Context, txq *store.Queries) error {
		row, err := lockedAllocation(ctx, txq, id)
		if err != nil {
			return err
		}
		if row.PersonID != current.PersonID {
			return errRowMoved
		}
		before, err := txq.DeleteTerms(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("planning: clear the terms of allocation %d: %w", id, err)
		}
		return s.recordEvent(ctx, txq, who, withChanges(allocationEvent(row, eventTermsCleared), termsChanges(&before, nil)))
	})
}
