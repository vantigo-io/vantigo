package planning

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// The wishes' reads (design S21, S26): the planners' list, a person's own on
// My planning and their timeline, and the shape every one is answered in.
// A wish is the person's voice — a kind and a date, no free text — and
// planning:manage's to read; the person sees their own open wishes and
// nothing of the closed ones, which are deleted. The writes are
// wishes_write.go's.

// maxOpenWishes is how many open wishes one person may have (S21's
// wish_limit).
const maxOpenWishes = 5

// invalidWishTitle is the title of a wish's field errors.
const invalidWishTitle = "Invalid wish"

// wishUnansweredReason is the reason a wish_withdrawn event carries when the
// module deleted the wish because Staff no longer answers its person (S26,
// plan reading 4): the person erased or left — the module cannot tell which
// (§4.4 can), so the reason names what it saw.
const wishUnansweredReason = "unanswered"

// ListPlanningWishes List the open wishes
// (GET /api/v1/planning/wishes)
//
// planning:manage. Oldest first; personId narrows to one person.
func (s *server) ListPlanningWishes(ctx context.Context, req gen.ListPlanningWishesRequestObject) (gen.ListPlanningWishesResponseObject, error) {
	out, err := s.listWishes(ctx, req.Params.PersonId)
	if status, problem := readProblem(err); status == http.StatusServiceUnavailable {
		return gen.ListPlanningWishes503ApplicationProblemPlusJSONResponse(problem), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.ListPlanningWishes200JSONResponse(out), nil
}

// listWishes builds GET /wishes's answer. The people are read through
// Workforce.People over today to the bound's far end and the projects
// through the directory, before any transaction. S26: when People answered
// somebody — the caller sees the workforce — a listed wish whose person it
// did not answer is an erased person's, and is deleted, in one transaction
// under the persons' locks, recorded as wish_withdrawn with the reason
// unanswered, and left out of the answer. When People answered nobody the
// caller cannot see the workforce, and nothing is deleted (reading 4).
func (s *server) listWishes(ctx context.Context, personID *int64) (gen.PlanningWishList, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return gen.PlanningWishList{}, err
	}
	rows, err := store.New(s.deps.Pool).ListOpenWishes(ctx, personID)
	if err != nil {
		return gen.PlanningWishList{}, fmt.Errorf("planning: read the open wishes: %w", err)
	}
	people, err := s.wishPeople(ctx, c)
	if err != nil {
		return gen.PlanningWishList{}, err
	}
	if len(people) > 0 {
		var erased []store.PlanningWish
		erased, rows = splitErased(rows, people)
		if err := s.deleteErasedWishes(ctx, erased); err != nil {
			return gen.PlanningWishList{}, err
		}
	}
	projects, err := s.projectEntries(ctx, wishProjectIDs(nil, rows))
	if err != nil {
		return gen.PlanningWishList{}, err
	}
	return gen.PlanningWishList{Wishes: wishesView(rows, people, projects)}, nil
}

// wishPeople is the people Staff answers the caller over today to the
// bound's far end, by id: whom a wish is named after on the planners'
// screens.
func (s *server) wishPeople(ctx context.Context, c *callerContext) (map[int64]*contracts.PersonEntry, error) {
	_, hi := workforceBound(c.Today)
	people, err := s.workforcePeople(ctx, c.Today, hi)
	if err != nil {
		return nil, err
	}
	return peopleByID(people), nil
}

// splitErased is rows split into the wishes of a person people does not
// hold — an erased person's (S26) — and the rest, each in order.
func splitErased(rows []store.PlanningWish, people map[int64]*contracts.PersonEntry) (erased, kept []store.PlanningWish) {
	kept = make([]store.PlanningWish, 0, len(rows))
	for _, w := range rows {
		if people[w.PersonID] == nil {
			erased = append(erased, w)
		} else {
			kept = append(kept, w)
		}
	}
	return erased, kept
}

// deleteErasedWishes deletes wishes in one transaction under their persons'
// locks, each with a wish_withdrawn event carrying the reason unanswered; a
// wish already gone is skipped. The actor is the caller, read before.
func (s *server) deleteErasedWishes(ctx context.Context, wishes []store.PlanningWish) error {
	if len(wishes) == 0 {
		return nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return err
	}
	var set lockSet
	for _, w := range wishes {
		set.persons = append(set.persons, w.PersonID)
	}
	return s.withLocks(ctx, set, func(ctx context.Context, txq *store.Queries) error {
		for _, w := range wishes {
			row, err := txq.DeleteWish(ctx, w.ID)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return fmt.Errorf("planning: delete wish %d of an erased person: %w", w.ID, err)
			}
			e := wishEvent(row, eventWishWithdrawn)
			payload := wishPayload(row)
			payload["reason"] = wishUnansweredReason
			e.Payload = payload
			if err := s.recordEvent(ctx, txq, who, e); err != nil {
				return err
			}
		}
		return nil
	})
}

// ownWishes is person's open wishes, oldest first, with the projects
// already read for the rows beside them (projects) — My planning and the
// self timeline.
func ownWishes(rows []store.PlanningWish, person *contracts.PersonEntry, projects map[int32]contracts.ProjectEntry) []gen.PlanningWish {
	return wishesView(rows, map[int64]*contracts.PersonEntry{person.ID: person}, projects)
}

// openWishesOf is one person's open wishes, oldest first.
func (s *server) openWishesOf(ctx context.Context, personID int64) ([]store.PlanningWish, error) {
	rows, err := store.New(s.deps.Pool).OpenWishesOfPerson(ctx, personID)
	if err != nil {
		return nil, fmt.Errorf("planning: read the open wishes of person %d: %w", personID, err)
	}
	return rows, nil
}

// wishProjectIDs is the projects rows are on and wishes name, each once,
// ascending: one directory read names both.
func wishProjectIDs(rows []store.PlanningAllocation, wishes []store.PlanningWish) []int32 {
	var ids []int32
	for _, a := range rows {
		ids = append(ids, a.ProjectID)
	}
	for _, w := range wishes {
		if w.ProjectID != nil {
			ids = append(ids, *w.ProjectID)
		}
	}
	return slices.Compact(slices.Sorted(slices.Values(ids)))
}

// wishesView is rows as the contract answers them, in order. Never null.
func wishesView(rows []store.PlanningWish, people map[int64]*contracts.PersonEntry, projects map[int32]contracts.ProjectEntry) []gen.PlanningWish {
	out := make([]gen.PlanningWish, 0, len(rows))
	for _, w := range rows {
		var project *contracts.ProjectEntry
		if w.ProjectID != nil {
			if e, ok := projects[*w.ProjectID]; ok {
				project = &e
			}
		}
		out = append(out, wishResponse(w, people[w.PersonID], project))
	}
	return out
}

// wishResponse is w as the contract answers it: the person named when
// Staff answered them (displayName null otherwise, rule 4), the project
// when the wish names one — by its id alone when the directory does not
// resolve it — and the seen stamp once a planner set it.
func wishResponse(w store.PlanningWish, person *contracts.PersonEntry, project *contracts.ProjectEntry) gen.PlanningWish {
	out := gen.PlanningWish{
		Id:        w.ID,
		Kind:      gen.PlanningWishKind(w.Kind),
		Person:    gen.PlanningPersonRef{Id: w.PersonID},
		FromDate:  apiDate(civil(w.FromDate)),
		CreatedAt: w.CreatedAt,
		SeenAt:    w.SeenAt,
	}
	if person != nil {
		name := person.DisplayName
		out.Person.DisplayName = &name
		if person.EmployeeNumber != "" {
			number := person.EmployeeNumber
			out.Person.EmployeeNumber = &number
		}
	}
	if w.ProjectID != nil {
		out.Project = &gen.PlanningProjectRef{Id: *w.ProjectID}
		if project != nil {
			code, name, status := project.Code, project.Name, gen.PlanningProjectStatus(project.Status)
			out.Project.Code, out.Project.Name, out.Project.Status = &code, &name, &status
		}
	}
	return out
}

// wishEvent is w's event of kind (S23): filed under the person and, for an
// off wish, the project, its payload the kind, the project id and the date
// — never more (S21).
func wishEvent(w store.PlanningWish, kind string) event {
	personID := w.PersonID
	return event{
		EntityKind: entityWish, EntityID: w.ID, PersonID: &personID, ProjectID: w.ProjectID,
		Kind: kind, Payload: wishPayload(w),
	}
}

// wishPayload is what a wish's event keeps of it: the kind, the date and,
// for an off wish, the project id.
func wishPayload(w store.PlanningWish) map[string]any {
	payload := map[string]any{"kind": w.Kind, "fromDate": civil(w.FromDate).Format(time.DateOnly)}
	if w.ProjectID != nil {
		payload["projectId"] = *w.ProjectID
	}
	return payload
}
