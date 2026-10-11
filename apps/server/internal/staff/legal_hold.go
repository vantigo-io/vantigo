package staff

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the legal hold and the reassessment (design S15, plan reading
// 16), both staff:personal-data's and both among the writes an erased person
// still allows. The hold — class F — stops every erase of the person and
// their delete as created in error, for as long as it stands; its reason, who
// set it and when are kept with it and cleared when it is lifted. The
// reassessment is HR's answer to the attention item an erased person raises
// twelve months after their last employment ended: it records who looked at
// the record again, and when. Neither moves the person's revision, which
// guards HR's replace of the editable fields — the sealed fields' rule.

// legalHoldReasonMax is the longest reason a hold carries, the column's.
const legalHoldReasonMax = 500

// reassessmentAfterMonths is how long after the last employment's end the
// reassessment of an erased person falls due (plan, Exact values).
const reassessmentAfterMonths = 12

// parseLegalHold judges a hold's request: a hold carries a reason of 1 to
// 500 characters, trimmed; a lift carries none, since the lift clears it.
func parseLegalHold(ctx context.Context, body gen.StaffLegalHoldRequest) (*string, fieldErrors) {
	var f fieldErrors
	if !bodyHas(ctx, "held") {
		f.add("held", "held is required: true to set the hold, false to lift it")
		return nil, f
	}
	if !body.Held {
		if body.Reason != nil {
			f.add("reason", "A lift carries no reason; the hold's reason is cleared with it")
		}
		return nil, f
	}
	reason := ""
	if body.Reason != nil {
		reason = strings.TrimSpace(*body.Reason)
	}
	switch {
	case reason == "":
		f.add("reason", "A legal hold needs a reason")
	case len([]rune(reason)) > legalHoldReasonMax:
		f.add("reason", fmt.Sprintf("A reason is at most %d characters", legalHoldReasonMax))
	}
	return &reason, f
}

// PutStaffPeopleByIdLegalHold Set or lift a person's legal hold
// (PUT /api/v1/staff/people/{id}/legal-hold)
func (s *server) PutStaffPeopleByIdLegalHold(ctx context.Context, req gen.PutStaffPeopleByIdLegalHoldRequestObject) (gen.PutStaffPeopleByIdLegalHoldResponseObject, error) {
	var body gen.StaffLegalHoldRequest
	if req.Body != nil {
		body = *req.Body
	}
	reason, f := parseLegalHold(ctx, body)
	if f.failed() {
		return gen.PutStaffPeopleByIdLegalHold400ApplicationProblemPlusJSONResponse(invalid(invalidLegalHoldTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}

	var (
		saved store.GetPersonRow
		link  *string
	)
	err = s.withPersonLock(ctx, req.Id, allowErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		current, err := readPerson(ctx, txq, req.Id)
		if err != nil {
			return err
		}
		if link, err = linkedUserID(ctx, txq, req.Id); err != nil {
			return err
		}
		c := changes{}
		if current.LegalHold != body.Held {
			c["legalHold"] = fieldChange{From: current.LegalHold, To: body.Held}
		}
		if before, after := current.LegalHoldReason, reason; (before == nil) != (after == nil) || (before != nil && *before != *after) {
			var change fieldChange
			if before != nil {
				change.From = *before
			}
			if after != nil {
				change.To = *after
			}
			c["reason"] = change
		}
		if len(c) == 0 {
			saved = current
			return nil
		}
		params := store.SetLegalHoldParams{ID: req.Id, Held: body.Held, Now: s.deps.Clock()}
		kind := eventLegalHoldLifted
		if body.Held {
			kind = eventLegalHoldSet
			at := s.deps.Clock()
			params.Reason, params.ByUserID, params.At = reason, &caller.UserID, &at
			if current.LegalHold {
				// A new reason on a hold that stands keeps when it was set.
				params.At = current.LegalHoldAt
			}
		}
		if err := txq.SetLegalHold(ctx, params); err != nil {
			return fmt.Errorf("staff: write person %d's legal hold: %w", req.Id, err)
		}
		if saved, err = readPerson(ctx, txq, req.Id); err != nil {
			return err
		}
		return s.recordChanges(ctx, txq, req.Id, kind, who, c)
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.PutStaffPeopleByIdLegalHold404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: write person %d's legal hold: %w", req.Id, err)
	}
	out, err := s.respondPerson(ctx, caller, saved, link)
	if err != nil {
		return nil, err
	}
	return gen.PutStaffPeopleByIdLegalHold200JSONResponse(out), nil
}

// reassessmentNotDue is the 409 of a reassessment asked for too early, twice,
// or of a person the retention run has not erased.
func reassessmentNotDue() error {
	return refuseConflict(conflict(codeReassessmentNotDue, "Reassessment not due",
		fmt.Sprintf("A reassessment is due for an erased person not yet reassessed, from %d months after their last employment ended.", reassessmentAfterMonths)))
}

// PostStaffPeopleByIdReassessed Record a reassessment
// (POST /api/v1/staff/people/{id}/reassessed)
//
// Due, judged under the person's lock on today in the business time zone:
// erased, not yet reassessed, every employment ended, and today on or after
// the last end plus twelve months (addMonths: the 29th of February's due day
// is the 28th).
func (s *server) PostStaffPeopleByIdReassessed(ctx context.Context, req gen.PostStaffPeopleByIdReassessedRequestObject) (gen.PostStaffPeopleByIdReassessedResponseObject, error) {
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}

	var (
		saved store.GetPersonRow
		link  *string
	)
	err = s.withPersonLock(ctx, req.Id, allowErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		current, err := readPerson(ctx, txq, req.Id)
		if err != nil {
			return err
		}
		if current.ErasedAt == nil || current.ReassessedAt != nil {
			return reassessmentNotDue()
		}
		last, err := txq.LastEmploymentEnd(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("staff: read person %d's last end: %w", req.Id, err)
		}
		if !last.AllEnded || !last.LastEnd.Valid || caller.Today.Before(addMonths(last.LastEnd.Time, reassessmentAfterMonths)) {
			return reassessmentNotDue()
		}
		now := s.deps.Clock()
		if err := txq.SetReassessed(ctx, store.SetReassessedParams{ID: req.Id, ByUserID: &caller.UserID, Now: now}); err != nil {
			return fmt.Errorf("staff: record person %d's reassessment: %w", req.Id, err)
		}
		if saved, err = readPerson(ctx, txq, req.Id); err != nil {
			return err
		}
		if link, err = linkedUserID(ctx, txq, req.Id); err != nil {
			return err
		}
		return s.recordChanges(ctx, txq, req.Id, eventReassessed, who, changes{"reassessedAt": fieldChange{To: now}})
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.PostStaffPeopleByIdReassessed404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		return gen.PostStaffPeopleByIdReassessed409ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: record person %d's reassessment: %w", req.Id, err)
	}
	out, err := s.respondPerson(ctx, caller, saved, link)
	if err != nil {
		return nil, err
	}
	return gen.PostStaffPeopleByIdReassessed200JSONResponse(out), nil
}
