package staff

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is DELETE /people/{id}: the hard delete of a person created in
// error (plan reading 5) — no employment, no link, no document, no absence
// or holiday adjustment (delivery B), named as nobody's manager, not under a
// legal hold — with their next of kin, their
// roles, their group memberships and ownerships, and their own history, in
// one transaction. It is the one exception to
// the events table's append-only rule, and it leaves one installation-level
// event behind carrying the employee number and nothing else. Anything that names the person keeps
// them; the retention run (A2) is the only other way a person's data leaves.
//
// The accounts worker's preparing link (delivery C) names no user yet, so it
// does not keep the person: it is deleted with them (plan reading 9) — but
// not while the worker holds it (lease_until in the future), when identity
// may be creating the user that link is for: accounts_busy (pre-flight I4).
// The link is locked last, after the person and the manager graph (design
// §4 Locking).

// personInUse is the 409 a person something names answers. reasons are what
// names them, in words; nothing about another person is said.
func personInUse(reasons []string) error {
	const rule = "only a person created in error — with no employment, no user link, no document, no absence or holiday adjustment, and named as nobody's manager — is deleted."
	detail := "This person is in use, and " + rule
	if len(reasons) > 0 {
		detail = "This person " + strings.Join(reasons, ", ") + ", and " + rule
	}
	return refuseConflict(conflict(codePersonInUse, "Person in use", detail))
}

// personDeleteError is the delete's own database refusal: a foreign key that
// still names the person — a row written past the checks — is person_in_use,
// as the check would have answered. Anything else is the error it was.
func personDeleteError(err error) error {
	if db.IsForeignKeyViolation(err) {
		return personInUse(nil)
	}
	return err
}

// DeleteStaffPeopleById Delete a person created in error
// (DELETE /api/v1/staff/people/{id})
//
// The person's row first, then the manager graph (locks.go's order): every
// write that makes someone a manager — a department's, a terms row's — takes
// the graph lock, so once it is held nobody can start naming this person
// between the check and the delete. A foreign key that refuses anyway is
// still person_in_use. An erased person is person_erased (the read-only
// rule, at the lock) and a held one legal_hold, before what names them.
func (s *server) DeleteStaffPeopleById(ctx context.Context, req gen.DeleteStaffPeopleByIdRequestObject) (gen.DeleteStaffPeopleByIdResponseObject, error) {
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	err = s.withPersonLock(ctx, req.Id, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if err := lockManagerGraph(ctx, txq); err != nil {
			return err
		}
		row, err := readPerson(ctx, txq, req.Id)
		if err != nil {
			return err
		}
		if row.LegalHold {
			return refuseConflict(conflict(codeLegalHold, "Legal hold",
				"This person is under a legal hold, which keeps their record whole. Lift the hold first."))
		}
		use, err := txq.PersonInUse(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("staff: read what names person %d: %w", req.Id, err)
		}
		var reasons []string
		for _, r := range []struct {
			holds  bool
			reason string
		}{
			{use.HasEmployment, "has an employment"},
			{use.HasLink, "is linked to a user"},
			{use.HasDocuments, "has documents"},
			{use.ManagesDepartment, "manages a department"},
			{use.NamedAsManager, "is the manager on someone's terms"},
			{use.HasAbsences, "has absences"},
			{use.HasAdjustments, "has holiday adjustments"},
			{use.HasCompetences, "has competences"},
		} {
			if r.holds {
				reasons = append(reasons, r.reason)
			}
		}
		if len(reasons) > 0 {
			return personInUse(reasons)
		}
		link, err := txq.LockPersonAccountLink(ctx, req.Id)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return fmt.Errorf("staff: lock person %d's link: %w", req.Id, err)
		case link.LeaseUntil != nil && link.LeaseUntil.After(s.deps.Clock()):
			return accountsBusy()
		case link.ExternalID != nil:
			// A user named past PersonInUse's read — judged again on the
			// locked row, so the delete never takes a link naming a user.
			return personInUse([]string{"is linked to a user"})
		default:
			if err := txq.DeletePersonLink(ctx, req.Id); err != nil {
				return fmt.Errorf("staff: delete person %d's preparing link: %w", req.Id, err)
			}
		}
		// The person's group rows go before the person, with no event
		// (Entra delivery D, reading 9): the next sync of each group removes
		// them in the provider's directory. What the person wants and their
		// competence reviews (staff competence delivery B) go too, as do the
		// profiles assigned to them (delivery C): their keys to the person
		// have no cascade.
		for name, del := range map[string]func(context.Context, int64) error{
			"next of kin":              txq.DeleteNextOfKin,
			"roles":                    txq.DeletePersonRoles,
			"groups":                   txq.DeletePersonGroupMembers,
			"group owners":             txq.DeletePersonGroupOwners,
			"interests":                txq.DeletePersonInterests,
			"preferences":              txq.DeletePersonPreferences,
			"availability declaration": txq.DeletePersonAvailability,
			"competence reviews":       txq.DeletePersonCompetenceReviews,
			"competence profiles":      txq.DeletePersonProfiles,
		} {
			if err := del(ctx, req.Id); err != nil {
				return fmt.Errorf("staff: delete person %d's %s: %w", req.Id, name, err)
			}
		}
		if err := txq.DeletePersonEvents(ctx, &req.Id); err != nil {
			return fmt.Errorf("staff: delete person %d's history: %w", req.Id, err)
		}
		if err := txq.DeletePerson(ctx, req.Id); err != nil {
			return personDeleteError(err)
		}
		// The photo's object (person-glance design §4) goes through the
		// ledger, as an erased document's does: queued here, removed once
		// the delete has committed.
		if err := s.queuePhotoObject(ctx, txq, req.Id, row.PhotoObjectKey); err != nil {
			return err
		}
		return s.recordEvent(ctx, txq, nil, eventPersonDeleted, who,
			map[string]any{"employeeNumber": row.EmployeeNumber}, []string{fieldClasses["employeeNumber"]})
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.DeleteStaffPeopleById404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		return gen.DeleteStaffPeopleById409ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: delete person %d: %w", req.Id, err)
	}
	return gen.DeleteStaffPeopleById204Response{}, nil
}
