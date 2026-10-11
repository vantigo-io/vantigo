package staff

import (
	"context"
	"fmt"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the erased state (design S15 as amended, plan readings 6 and
// 7). A person the retention run has erased keeps their record — what is left
// of it — and is read-only: every write of theirs answers 409 person_erased,
// judged once, centrally, where every write of a person starts — the
// person's lock (withPersonLock) — so no write path can forget it. Four
// writes are the exceptions: the legal hold, the reassessment, the unlink of
// their user (so the user can be linked to someone else) and a rehire, which
// is POST /people/{id}/employments on the erased record and opens it again.
// Every read stays open — the record, the history, the documents and their
// downloads (A documents live five years for claims), the exports.
//
// Because the erase removed the national identity number and its MAC, a
// person who comes back cannot be recognised by it; the creation form asks
// POST /people/similar-erased for erased people with the same names and
// birth date, so a second record is a choice and not an accident.

// erasedRule is how a write under withPersonLock meets the read-only rule:
// the plan's mutation flag, named for what it decides.
type erasedRule bool

const (
	// refuseErased is a write an erased person refuses: every write of a
	// person but the exceptions.
	refuseErased erasedRule = true
	// allowErased is one of the exceptions — the hold, the reassessment, the
	// unlink, the rehire — or a read that records an event (a reveal, the
	// export's audit), which the rule does not reach. The retention run's own
	// erases (delivery A2's Task 5) are this too.
	allowErased erasedRule = false
)

// personErased is the 409 a write on an erased person answers.
func personErased() error {
	return refuseConflict(conflict(codePersonErased, "Person erased",
		"This person's record has been erased under the retention rules and is read-only. A legal hold, a reassessment, an unlink of their user and a rehire are still possible."))
}

// rehireIfErased is a rehire's first write (plan reading 6), under the
// person's lock and in the employment's own transaction: on an erased person
// it clears erased_at and the reassessment and records person_rehired naming
// what it cleared; on anyone else it does nothing. What the erase blanked
// stays blank until HR types it again.
func (s *server) rehireIfErased(ctx context.Context, txq *store.Queries, personID int64, who actor) error {
	row, err := readPerson(ctx, txq, personID)
	if err != nil {
		return err
	}
	if row.ErasedAt == nil {
		return nil
	}
	if err := txq.ClearErasure(ctx, store.ClearErasureParams{ID: personID, Now: s.deps.Clock()}); err != nil {
		return fmt.Errorf("staff: rehire person %d: %w", personID, err)
	}
	cleared := changes{"erasedAt": fieldChange{From: *row.ErasedAt}}
	if row.ReassessedAt != nil {
		cleared["reassessedAt"] = fieldChange{From: *row.ReassessedAt}
	}
	return s.recordChanges(ctx, txq, personID, eventPersonRehired, who, cleared)
}

// PostStaffPeopleSimilarErased Find erased people with the same names and birth date
// (POST /api/v1/staff/people/similar-erased)
//
// The names are trimmed and compared however cased; both and the birth date
// are required.
func (s *server) PostStaffPeopleSimilarErased(ctx context.Context, req gen.PostStaffPeopleSimilarErasedRequestObject) (gen.PostStaffPeopleSimilarErasedResponseObject, error) {
	var body gen.StaffSimilarErasedRequest
	if req.Body != nil {
		body = *req.Body
	}
	given, family := strings.TrimSpace(body.GivenName), strings.TrimSpace(body.FamilyName)
	var f fieldErrors
	if given == "" {
		f.add("givenName", "A given name is required")
	}
	if family == "" {
		f.add("familyName", "A family name is required")
	}
	if body.BirthDate.IsZero() {
		f.add("birthDate", "A birth date is required")
	}
	if f.failed() {
		return gen.PostStaffPeopleSimilarErased400ApplicationProblemPlusJSONResponse(invalid(invalidPersonTitle, f)), nil
	}
	rows, err := store.New(s.deps.Pool).SimilarErasedPeople(ctx, store.SimilarErasedPeopleParams{
		GivenName: given, FamilyName: family, BirthDate: dayParam(body.BirthDate.Time),
	})
	if err != nil {
		return nil, fmt.Errorf("staff: find erased people like a new one: %w", err)
	}
	return gen.PostStaffPeopleSimilarErased200JSONResponse(mapRows(rows, func(r store.SimilarErasedPeopleRow) gen.StaffErasedPerson {
		return gen.StaffErasedPerson{Id: r.ID, EmployeeNumber: r.EmployeeNumber, DisplayName: r.DisplayName, ErasedAt: r.ErasedAt}
	})), nil
}
