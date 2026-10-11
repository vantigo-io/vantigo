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
// error (plan reading 5) — no employment, no link, no document, named as
// nobody's manager — with their next of kin, their roles and their own history, in one
// transaction. It is the one exception to the events table's append-only
// rule, and it leaves one installation-level event behind carrying the
// employee number and nothing else. Anything that names the person keeps
// them; the retention run (A2) is the only other way a person's data leaves.

// personInUse is the 409 a person something names answers. reasons are what
// names them, in words; nothing about another person is said.
func personInUse(reasons []string) error {
	const rule = "only a person created in error — with no employment, no user link, no document, and named as nobody's manager — is deleted."
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
// still person_in_use.
func (s *server) DeleteStaffPeopleById(ctx context.Context, req gen.DeleteStaffPeopleByIdRequestObject) (gen.DeleteStaffPeopleByIdResponseObject, error) {
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	err = s.withPersonLock(ctx, req.Id, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if err := lockManagerGraph(ctx, txq); err != nil {
			return err
		}
		row, err := readPerson(ctx, txq, req.Id)
		if err != nil {
			return err
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
		} {
			if r.holds {
				reasons = append(reasons, r.reason)
			}
		}
		if len(reasons) > 0 {
			return personInUse(reasons)
		}
		for name, del := range map[string]func(context.Context, int64) error{
			"next of kin": txq.DeleteNextOfKin,
			"roles":       txq.DeletePersonRoles,
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
