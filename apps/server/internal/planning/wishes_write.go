package planning

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// This file is the wishes' writes (design S21, S23): the person files and
// withdraws their own on My planning; the planners stamp one seen and
// resolve it. Each reads what it asks another module about before its
// transaction (rule 3) — the caller's person, the project, the actor, the
// planners to mail — then takes the person's lock (S23), judges and writes
// the wish and its event. A resolved or withdrawn wish is deleted, its event
// keeping the kind, the project id and the date (S21).

// wishUniqueIndex is the backstop of wish_exists (§3.5).
const wishUniqueIndex = "ux_wishes_person_project_off"

// CreatePlanningMyWish File a wish
// (POST /api/v1/planning/me/wishes)
//
// planning:access. A caller Staff answers no person for is the 404
// no_person.
func (s *server) CreatePlanningMyWish(ctx context.Context, req gen.CreatePlanningMyWishRequestObject) (gen.CreatePlanningMyWishResponseObject, error) {
	var body gen.PlanningWishInput
	if req.Body != nil {
		body = *req.Body
	}
	out, err := s.createWish(ctx, body)
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusBadRequest:
			return gen.CreatePlanningMyWish400ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusNotFound:
			return gen.CreatePlanningMyWish404ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusConflict:
			return gen.CreatePlanningMyWish409ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusServiceUnavailable:
			return gen.CreatePlanningMyWish503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.CreatePlanningMyWish201JSONResponse(out), nil
}

// wishFieldErrors is S21's field rules on a wish's body: a kind the
// contract names, a project for off and none for new, and a date on or
// after today.
func wishFieldErrors(body gen.PlanningWishInput, today time.Time) fieldErrors {
	var f fieldErrors
	switch body.Kind {
	case gen.Off:
		if body.ProjectId == nil {
			f.add("projectId", "A wish off a project names the project")
		}
	case gen.New:
		if body.ProjectId != nil {
			f.add("projectId", "A wish for something new names no project")
		}
	default:
		f.add("kind", "The kind is off or new")
	}
	if body.FromDate.Before(today) {
		f.add("fromDate", "The date is today or later")
	}
	return f
}

// createWish is POST /me/wishes (S21): the caller's wish, judged under
// their lock — an off wish needs a confirmed row of theirs on the project
// ending today or later (wish_no_booking), at most five are open
// (wish_limit), one off wish a project (wish_exists) — and wish_created.
// After the commit every planning:manage holder but the wisher is mailed
// once (reading 6).
func (s *server) createWish(ctx context.Context, body gen.PlanningWishInput) (gen.PlanningWish, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return gen.PlanningWish{}, err
	}
	if f := s.unknownProperties(ctx, wishRequestSchema); f.failed() {
		return gen.PlanningWish{}, refuseInvalid(invalidWishTitle, f)
	}
	self, err := s.callerSelf(ctx, c)
	if err != nil {
		return gen.PlanningWish{}, err
	}
	if self == nil {
		return gen.PlanningWish{}, &refusal{status: http.StatusNotFound, problem: noPerson()}
	}
	if f := wishFieldErrors(body, c.Today); f.failed() {
		return gen.PlanningWish{}, refuseInvalid(invalidWishTitle, f)
	}
	var project *contracts.ProjectEntry
	if body.Kind == gen.Off {
		// A closed project is no refusal: wishing off a cancelled
		// project is harmless, and the booking rule judges it.
		if project, err = s.projectEntry(ctx, *body.ProjectId); err != nil {
			return gen.PlanningWish{}, err
		}
		if project == nil {
			return gen.PlanningWish{}, refuseInvalid(invalidWishTitle, fieldErrors{errs: fieldError("projectId", unknownProjectMessage)})
		}
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return gen.PlanningWish{}, err
	}
	holders, err := s.usersPermissionHolders(ctx, permManage)
	if err != nil {
		// The mail is a courtesy: a directory that fails tells nobody,
		// and the wish is written all the same.
		s.deps.Logger.WarnContext(ctx, "planning: the planners to tell of a wish could not be read",
			"person_id", self.ID, "error", err.Error())
		holders = nil
	}

	var row store.PlanningWish
	err = s.withPersonLock(ctx, self.ID, func(ctx context.Context, txq *store.Queries) error {
		if body.Kind == gen.Off {
			booked, err := txq.PersonHasConfirmedRowOnProjectFrom(ctx, store.PersonHasConfirmedRowOnProjectFromParams{
				PersonID: self.ID, ProjectID: *body.ProjectId, Today: dayParam(c.Today),
			})
			if err != nil {
				return fmt.Errorf("planning: read the bookings of person %d on project %d: %w", self.ID, *body.ProjectId, err)
			}
			if !booked {
				return refuseConflict(conflict(codeWishNoBooking, "No booking on the project",
					"You have no confirmed booking on this project from today on, so there is nothing to wish off."))
			}
		}
		open, err := txq.CountOpenWishesOfPerson(ctx, self.ID)
		if err != nil {
			return fmt.Errorf("planning: count the open wishes of person %d: %w", self.ID, err)
		}
		if open >= maxOpenWishes {
			return refuseWishLimit()
		}
		if body.Kind == gen.Off {
			exists, err := txq.OffWishExists(ctx, store.OffWishExistsParams{PersonID: self.ID, ProjectID: body.ProjectId})
			if err != nil {
				return fmt.Errorf("planning: read the off wishes of person %d: %w", self.ID, err)
			}
			if exists {
				return refuseWishExists()
			}
		}
		row, err = txq.InsertWish(ctx, store.InsertWishParams{
			PersonID: self.ID, Kind: string(body.Kind), ProjectID: body.ProjectId,
			FromDate: dayParam(body.FromDate.Time), Now: s.deps.Clock(),
		})
		if db.IsUniqueViolation(err, wishUniqueIndex) {
			return refuseWishExists()
		}
		if err != nil {
			return fmt.Errorf("planning: file a wish of person %d: %w", self.ID, err)
		}
		return s.recordEvent(ctx, txq, who, wishEvent(row, eventWishCreated))
	})
	if err != nil {
		return gen.PlanningWish{}, err
	}
	s.sendWishNotices(ctx, wishRecipients(holders, c.UserID, self), wishNotice{person: self, wish: row, project: project})
	return wishResponse(row, self, project), nil
}

// refuseWishLimit is S21's wish_limit.
func refuseWishLimit() error {
	return refuseConflict(conflict(codeWishLimit, "Too many open wishes",
		fmt.Sprintf("You have %d open wishes, the most there may be. Withdraw one first.", maxOpenWishes)))
}

// refuseWishExists is S21's wish_exists.
func refuseWishExists() error {
	return refuseConflict(conflict(codeWishExists, "The wish exists",
		"You already wish off this project."))
}

// DeletePlanningMyWish Withdraw a wish
// (DELETE /api/v1/planning/me/wishes/{id})
//
// planning:access. A wish that is not the caller's is the 404 an unknown
// id gets; a caller Staff answers no person for is no_person.
func (s *server) DeletePlanningMyWish(ctx context.Context, req gen.DeletePlanningMyWishRequestObject) (gen.DeletePlanningMyWishResponseObject, error) {
	err := s.withdrawWish(ctx, req.Id)
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusNotFound:
			return gen.DeletePlanningMyWish404ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusServiceUnavailable:
			return gen.DeletePlanningMyWish503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.DeletePlanningMyWish204Response{}, nil
}

// withdrawWish deletes one of the caller's own wishes under their lock and
// records wish_withdrawn (S21).
func (s *server) withdrawWish(ctx context.Context, id int64) error {
	c, err := s.callerFor(ctx)
	if err != nil {
		return err
	}
	self, err := s.callerSelf(ctx, c)
	if err != nil {
		return err
	}
	if self == nil {
		return &refusal{status: http.StatusNotFound, problem: noPerson()}
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return err
	}
	return s.withPersonLock(ctx, self.ID, func(ctx context.Context, txq *store.Queries) error {
		current, err := lockedWish(ctx, txq, id)
		if err != nil {
			return err
		}
		if current.PersonID != self.ID {
			return wishNotFound()
		}
		return s.deleteWish(ctx, txq, who, id, eventWishWithdrawn)
	})
}

// MarkPlanningWishSeen Stamp a wish seen
// (POST /api/v1/planning/wishes/{id}/seen)
//
// planning:manage. A wish already stamped is answered as it is.
func (s *server) MarkPlanningWishSeen(ctx context.Context, req gen.MarkPlanningWishSeenRequestObject) (gen.MarkPlanningWishSeenResponseObject, error) {
	out, err := s.seeWish(ctx, req.Id)
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusNotFound:
			return gen.MarkPlanningWishSeen404Response{}, nil
		case http.StatusServiceUnavailable:
			return gen.MarkPlanningWishSeen503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.MarkPlanningWishSeen200JSONResponse(out), nil
}

// seeWish stamps one wish seen by the caller under its person's lock and
// records wish_seen, once: a wish stamped already — before the read, or by
// another planner meanwhile — is answered as it is, with no event. The
// person and the project are read before, to name it.
func (s *server) seeWish(ctx context.Context, id int64) (gen.PlanningWish, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return gen.PlanningWish{}, err
	}
	current, err := s.wish(ctx, id)
	if err != nil {
		return gen.PlanningWish{}, err
	}
	people, err := s.wishPeople(ctx, c)
	if err != nil {
		return gen.PlanningWish{}, err
	}
	projects, err := s.projectEntries(ctx, wishProjectIDs(nil, []store.PlanningWish{current}))
	if err != nil {
		return gen.PlanningWish{}, err
	}
	if current.SeenAt != nil {
		return wishesView([]store.PlanningWish{current}, people, projects)[0], nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return gen.PlanningWish{}, err
	}
	row := current
	err = s.withPersonLock(ctx, current.PersonID, func(ctx context.Context, txq *store.Queries) error {
		locked, err := lockedWish(ctx, txq, id)
		if err != nil {
			return err
		}
		if row = locked; locked.SeenAt != nil {
			return nil
		}
		if row, err = txq.MarkWishSeen(ctx, store.MarkWishSeenParams{ID: id, Now: s.deps.Clock(), UserID: &c.UserID}); err != nil {
			return fmt.Errorf("planning: stamp wish %d seen: %w", id, err)
		}
		return s.recordEvent(ctx, txq, who, wishEvent(row, eventWishSeen))
	})
	if err != nil {
		return gen.PlanningWish{}, err
	}
	return wishesView([]store.PlanningWish{row}, people, projects)[0], nil
}

// ResolvePlanningWish Resolve a wish
// (POST /api/v1/planning/wishes/{id}/resolve)
//
// planning:manage.
func (s *server) ResolvePlanningWish(ctx context.Context, req gen.ResolvePlanningWishRequestObject) (gen.ResolvePlanningWishResponseObject, error) {
	err := s.resolveWish(ctx, req.Id)
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusNotFound:
			return gen.ResolvePlanningWish404Response{}, nil
		case http.StatusServiceUnavailable:
			return gen.ResolvePlanningWish503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.ResolvePlanningWish204Response{}, nil
}

// resolveWish deletes one wish under its person's lock and records
// wish_resolved (S21).
func (s *server) resolveWish(ctx context.Context, id int64) error {
	current, err := s.wish(ctx, id)
	if err != nil {
		return err
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return err
	}
	return s.withPersonLock(ctx, current.PersonID, func(ctx context.Context, txq *store.Queries) error {
		if _, err := lockedWish(ctx, txq, id); err != nil {
			return err
		}
		return s.deleteWish(ctx, txq, who, id, eventWishResolved)
	})
}

// wish reads one wish before any transaction: whose it is, so its
// person's lock can be taken. An unknown id is the bare 404.
func (s *server) wish(ctx context.Context, id int64) (store.PlanningWish, error) {
	row, err := store.New(s.deps.Pool).WishByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.PlanningWish{}, wishNotFound()
	}
	if err != nil {
		return store.PlanningWish{}, fmt.Errorf("planning: read wish %d: %w", id, err)
	}
	return row, nil
}

// lockedWish reads one wish FOR UPDATE under its person's lock; a wish
// gone meanwhile is the bare 404.
func lockedWish(ctx context.Context, txq *store.Queries, id int64) (store.PlanningWish, error) {
	row, err := txq.LockWish(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.PlanningWish{}, wishNotFound()
	}
	if err != nil {
		return store.PlanningWish{}, fmt.Errorf("planning: lock wish %d: %w", id, err)
	}
	return row, nil
}

// deleteWish deletes one locked wish and records kind about it.
func (s *server) deleteWish(ctx context.Context, txq *store.Queries, who actor, id int64, kind string) error {
	row, err := txq.DeleteWish(ctx, id)
	if err != nil {
		return fmt.Errorf("planning: delete wish %d: %w", id, err)
	}
	return s.recordEvent(ctx, txq, who, wishEvent(row, kind))
}

// wishNotFound is the 404 of a wish that does not exist, or is not the
// caller's to withdraw: one body for both, so a wish's id tells nobody
// whose it is. The withdrawal's contract answers a problem; the planners'
// operations answer the bare 404.
func wishNotFound() error {
	status := int32(http.StatusNotFound)
	title := "No such wish"
	return &refusal{status: http.StatusNotFound, problem: gen.PlanningProblem{Title: &title, Status: &status}}
}

// wishRecipients is the planners a new wish is mailed to (S21): every
// holder once, by id, leaving out the wisher — the caller, and the user
// their person is linked to (reading 6).
func wishRecipients(holders []contracts.Holder, caller uuid.UUID, self *contracts.PersonEntry) []uuid.UUID {
	seen := map[uuid.UUID]bool{caller: true}
	if self.UserID != nil {
		seen[*self.UserID] = true
	}
	var out []uuid.UUID
	for _, h := range holders {
		if seen[h.ID] {
			continue
		}
		seen[h.ID] = true
		out = append(out, h.ID)
	}
	return out
}
