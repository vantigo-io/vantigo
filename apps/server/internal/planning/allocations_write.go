package planning

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// This file is the allocations' writes (design S1–S7, S19, S23, amendments 5
// and 6): create, replace, delete and confirm. Each reads everything it asks
// another module about before its transaction (rule 3) — the actor, the
// project, the people Staff answers over the weeks the write touches, their
// capacity — then takes S23's locks, judges its revision and S7's
// overbooking on the person's confirmed rows under the person's lock, writes
// the row and its events, and only after the commit mails the person
// (notifications.go).

// errRowMoved is a write's answer when the row changed between the read
// before its transaction and the lock: what was read about it no longer
// matches it, so the write starts again from the read.
var errRowMoved = errors.New("planning: the allocation changed while the write read its neighbours")

// maxWriteAttempts is how often a write without If-Match starts again when
// the row moved under it (errRowMoved). A row still moving after the last
// attempt is the 409 revision_conflict (§13 amendment 35).
const maxWriteAttempts = 3

// answerOf sorts a write's error: a refusal or a missing contract is an
// answer with its status and problem; anything else is a fault.
func answerOf(err error) (int, gen.PlanningProblem, bool) {
	if r, ok := asRefusal(err); ok {
		return r.status, r.problem, true
	}
	if errors.Is(err, errDependencyUnavailable) {
		return http.StatusServiceUnavailable, dependencyUnavailable(), true
	}
	return 0, gen.PlanningProblem{}, false
}

// staffRead is what a write asked Staff over the weeks it touches, cut to
// the Workforce bound: lo..hi, the people answered and, for the confirmed
// write, the person's capacity.
type staffRead struct {
	lo, hi   time.Time
	ok       bool
	people   []contracts.PersonEntry
	capacity contracts.PersonCapacity
}

// readStaff asks Staff about from..to cut to the bound — the people, and
// capacityOf's capacity when capacityOf is not 0 — before any transaction.
// Nothing is asked when no day of it is inside the bound.
func (s *server) readStaff(ctx context.Context, c *callerContext, from, to time.Time, capacityOf int64) (staffRead, error) {
	var r staffRead
	if r.lo, r.hi, r.ok = clipToBound(from, to, c.Today); !r.ok {
		return r, nil
	}
	people, err := s.workforcePeople(ctx, r.lo, r.hi)
	if err != nil {
		return staffRead{}, outOfStaffBound(err, r.lo, c.Today)
	}
	r.people = people
	if capacityOf != 0 {
		capacity, err := s.workforceCapacity(ctx, r.lo, r.hi, []int64{capacityOf})
		if err != nil {
			return staffRead{}, outOfStaffBound(err, r.lo, c.Today)
		}
		r.capacity = capacity[capacityOf]
	}
	return r, nil
}

// outOfStaffBound is err as a write answers it: Staff's refusal of a window
// outside its bound (contracts.ErrWindowOutOfBounds) — which the clip to
// this module's today cannot rule out when Staff's today is another day, at
// midnight — as boundErrors' field error window_out_of_bounds, on fromDate
// when the window starts on the bound's near end, else on toDate. Any other
// error is answered as it is.
func outOfStaffBound(err error, lo, today time.Time) error {
	if !errors.Is(err, contracts.ErrWindowOutOfBounds) {
		return err
	}
	field, message := "toDate", "The period reaches past the last day Staff answers about"
	if near, _ := workforceBound(today); !lo.After(near) {
		field, message = "fromDate", "The period reaches before the first day Staff answers about"
	}
	var f fieldErrors
	f.addCoded(field, codeWindowOutOfBounds, message)
	return refuseInvalid(invalidAllocationTitle, f)
}

// employs reports whether person is employed on every day of from..to Staff
// was asked about (S4).
func (r staffRead) employs(person *contracts.PersonEntry, from, to time.Time) bool {
	return person != nil && r.ok && employedThroughout(person.Employed, from, to, r.lo, r.hi)
}

// judgeOverbooking is S7 under the person's lock: the person's other
// confirmed rows over the weeks previous and candidate touch, judged with
// capacity read before the transaction. Under refuse a write that makes an
// excess grow is overbooked, and nothing is written.
func judgeOverbooking(ctx context.Context, txq *store.Queries, rule string, personID, rowID int64, capacity contracts.PersonCapacity, previous, candidate *capacityRow) (overbooking, error) {
	from, to := candidate.From, candidate.To
	if previous != nil {
		from, to = earliestOf(from, previous.From), latestOf(to, previous.To)
	}
	from, to = rowWindow(from, to)
	rows, err := txq.PersonConfirmedAllocationsInWindow(ctx, store.PersonConfirmedAllocationsInWindowParams{
		PersonID: personID, FromDate: dayParam(from), ToDate: dayParam(to),
	})
	if err != nil {
		return overbooking{}, fmt.Errorf("planning: read person %d's confirmed allocations: %w", personID, err)
	}
	o := overbookingOf(capacity, othersThan(rows, rowID), previous, candidate)
	if o.Grows && rule == string(gen.Refuse) {
		return overbooking{}, refuseOverbooked(o.Over)
	}
	return o, nil
}

// fieldRefusal is the 400 of one field's fault.
func fieldRefusal(field, message string) error {
	return refuseInvalid(invalidAllocationTitle, fieldErrors{errs: fieldError(field, message)})
}

// The field errors of an id neither contract resolves (S4).
const (
	unknownPersonMessage  = "Staff answers no person with this id in employment over the period"
	unknownProjectMessage = "No project has this id"
)

// CreatePlanningAllocation Create an allocation
// (POST /api/v1/planning/allocations)
func (s *server) CreatePlanningAllocation(ctx context.Context, req gen.CreatePlanningAllocationRequestObject) (gen.CreatePlanningAllocationResponseObject, error) {
	var body gen.PlanningAllocationInput
	if req.Body != nil {
		body = *req.Body
	}
	out, err := s.create(ctx, body)
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusBadRequest:
			return gen.CreatePlanningAllocation400ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusConflict:
			return gen.CreatePlanningAllocation409ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusServiceUnavailable:
			return gen.CreatePlanningAllocation503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.CreatePlanningAllocation201JSONResponse(out), nil
}

// parseWrite is the body's field rules and the bound's, for a create or a
// replace.
func (s *server) parseWrite(ctx context.Context, c *callerContext, body gen.PlanningAllocationInput, requireStatus bool) (allocationFields, error) {
	f := s.unknownProperties(ctx, allocationRequestSchema)
	fields, invalidFields := parseAllocation(body, requireStatus)
	f.merge(invalidFields)
	if !f.failed() {
		f.merge(boundErrors(fields.from, fields.to, c.Today))
	}
	if f.failed() {
		return allocationFields{}, refuseInvalid(invalidAllocationTitle, f)
	}
	return fields, nil
}

// create is POST /allocations (S1–S7): the row, its event allocation_created,
// and the person's mail when it is created confirmed.
func (s *server) create(ctx context.Context, body gen.PlanningAllocationInput) (gen.PlanningAllocationWritten, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return gen.PlanningAllocationWritten{}, err
	}
	fields, err := s.parseWrite(ctx, c, body, false)
	if err != nil {
		return gen.PlanningAllocationWritten{}, err
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return gen.PlanningAllocationWritten{}, err
	}
	project, err := s.projectEntry(ctx, fields.projectID)
	if err != nil {
		return gen.PlanningAllocationWritten{}, err
	}
	if project == nil {
		return gen.PlanningAllocationWritten{}, fieldRefusal("projectId", unknownProjectMessage)
	}
	if projectClosed(project) {
		return gen.PlanningAllocationWritten{}, refuseProjectClosed(project)
	}
	status := defaultStatus(*project)
	if fields.status != nil {
		status = *fields.status
	}
	capacityOf := int64(0)
	if status == statusConfirmed {
		capacityOf = fields.personID
	}
	lo, hi := rowWindow(fields.from, fields.to)
	staff, err := s.readStaff(ctx, c, lo, hi, capacityOf)
	if err != nil {
		return gen.PlanningAllocationWritten{}, err
	}
	person := personIn(staff.people, fields.personID)
	if person == nil {
		return gen.PlanningAllocationWritten{}, fieldRefusal("personId", unknownPersonMessage)
	}
	if !staff.employs(person, fields.from, fields.to) {
		return gen.PlanningAllocationWritten{}, refuseOutsideEmployment()
	}
	rules, err := settings(ctx, store.New(s.deps.Pool))
	if err != nil {
		return gen.PlanningAllocationWritten{}, err
	}

	now := s.deps.Clock()
	var (
		row store.PlanningAllocation
		o   overbooking
	)
	err = s.withPersonLock(ctx, fields.personID, func(ctx context.Context, txq *store.Queries) error {
		if status == statusConfirmed {
			candidate := capacityRowOf(fields)
			if o, err = judgeOverbooking(ctx, txq, rules.Overbooking, fields.personID, 0, staff.capacity, nil, &candidate); err != nil {
				return err
			}
		}
		params := store.InsertAllocationParams{
			PersonID: fields.personID, ProjectID: fields.projectID,
			FromDate: dayParam(fields.from), ToDate: dayParam(fields.to),
			LoadUnit: fields.loadUnit, Load: numericOfHundredths(fields.load), Status: status,
			Title: fields.title, Note: fields.note, Now: now, UserID: &c.UserID,
		}
		if status == statusConfirmed {
			params.ConfirmedAt = &now
		}
		if row, err = txq.InsertAllocation(ctx, params); err != nil {
			return fmt.Errorf("planning: create an allocation: %w", err)
		}
		return s.recordChanges(ctx, txq, who, allocationEvent(row, eventAllocationCreated), changesOf(nil, &row, false))
	})
	if err != nil {
		return gen.PlanningAllocationWritten{}, err
	}
	if status == statusConfirmed {
		s.sendNotices(ctx, noticeTo(noticePlanned, person, row, project))
	}
	return gen.PlanningAllocationWritten{
		Allocation: allocationResponse(row, person, project, c.Manage),
		Warnings:   allocationWarnings(o, project, fields.from, fields.to),
	}, nil
}

// PutPlanningAllocation Change an allocation
// (PUT /api/v1/planning/allocations/{id})
func (s *server) PutPlanningAllocation(ctx context.Context, req gen.PutPlanningAllocationRequestObject) (gen.PutPlanningAllocationResponseObject, error) {
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutPlanningAllocation400ApplicationProblemPlusJSONResponse(invalid(invalidAllocationTitle, badIfMatch())), nil
	}
	var body gen.PlanningAllocationInput
	if req.Body != nil {
		body = *req.Body
	}
	out, err := s.replace(ctx, req.Id, revision, body)
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusBadRequest:
			return gen.PutPlanningAllocation400ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusNotFound:
			return gen.PutPlanningAllocation404Response{}, nil
		case http.StatusConflict:
			return gen.PutPlanningAllocation409ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusServiceUnavailable:
			return gen.PutPlanningAllocation503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.PutPlanningAllocation200JSONResponse(out), nil
}

// replace is PUT /allocations/{id}: the row replaced whole under S23's locks
// — the projects' first when it moves to another project, then both persons'
// ascending — its events, and the mails amendment 5 names.
func (s *server) replace(ctx context.Context, id int64, revision int32, body gen.PlanningAllocationInput) (gen.PlanningAllocationWritten, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return gen.PlanningAllocationWritten{}, err
	}
	fields, err := s.parseWrite(ctx, c, body, true)
	if err != nil {
		return gen.PlanningAllocationWritten{}, err
	}
	current, found, err := s.allocation(ctx, id)
	if err != nil {
		return gen.PlanningAllocationWritten{}, err
	}
	if !found {
		return gen.PlanningAllocationWritten{}, notFound()
	}
	if err := checkRevision("allocation", current.Revision, revision); err != nil {
		return gen.PlanningAllocationWritten{}, err
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return gen.PlanningAllocationWritten{}, err
	}
	project, err := s.projectEntry(ctx, fields.projectID)
	if err != nil {
		return gen.PlanningAllocationWritten{}, err
	}
	if project == nil {
		return gen.PlanningAllocationWritten{}, fieldRefusal("projectId", unknownProjectMessage)
	}
	previousProject := project
	if current.ProjectID != fields.projectID {
		if previousProject, err = s.projectEntry(ctx, current.ProjectID); err != nil {
			return gen.PlanningAllocationWritten{}, err
		}
	}
	for _, p := range []*contracts.ProjectEntry{previousProject, project} {
		if projectClosed(p) {
			return gen.PlanningAllocationWritten{}, refuseProjectClosed(p)
		}
	}
	status := *fields.status
	capacityOf := int64(0)
	if status == statusConfirmed {
		capacityOf = fields.personID
	}
	lo, hi := rowWindow(earliestOf(fields.from, civil(current.FromDate)), latestOf(fields.to, civil(current.ToDate)))
	staff, err := s.readStaff(ctx, c, lo, hi, capacityOf)
	if err != nil {
		return gen.PlanningAllocationWritten{}, err
	}
	person := personIn(staff.people, fields.personID)
	if person == nil {
		return gen.PlanningAllocationWritten{}, fieldRefusal("personId", unknownPersonMessage)
	}
	if !staff.employs(person, fields.from, fields.to) {
		return gen.PlanningAllocationWritten{}, refuseOutsideEmployment()
	}
	previousPerson := personIn(staff.people, current.PersonID)
	rules, err := settings(ctx, store.New(s.deps.Pool))
	if err != nil {
		return gen.PlanningAllocationWritten{}, err
	}

	locks := lockSet{persons: []int64{current.PersonID, fields.personID}}
	if current.ProjectID != fields.projectID {
		locks.projects = []int32{current.ProjectID, fields.projectID}
	}
	now := s.deps.Clock()
	var (
		before, row store.PlanningAllocation
		o           overbooking
	)
	err = s.withLocks(ctx, locks, func(ctx context.Context, txq *store.Queries) error {
		if before, err = lockedAllocation(ctx, txq, id); err != nil {
			return err
		}
		// The neighbours were read for the row at current's revision; one
		// written since is a revision the caller has not seen either.
		if before.Revision != current.Revision {
			return checkRevision("allocation", before.Revision, revision)
		}
		params := replacement(before, fields, now, c)
		if !changedRow(before, params) {
			row = before
			return nil
		}
		if status == statusConfirmed {
			var previous *capacityRow
			if before.PersonID == fields.personID && before.Status == statusConfirmed {
				p := rowOf(before)
				previous = &p
			}
			candidate := capacityRowOf(fields)
			if o, err = judgeOverbooking(ctx, txq, rules.Overbooking, fields.personID, id, staff.capacity, previous, &candidate); err != nil {
				return err
			}
		}
		if row, err = txq.UpdateAllocation(ctx, params); err != nil {
			return fmt.Errorf("planning: change allocation %d: %w", id, err)
		}
		if err := s.recordChanges(ctx, txq, who, allocationEvent(row, eventAllocationChanged), changesOf(&before, &row, true)); err != nil {
			return err
		}
		return s.recordStatusChange(ctx, txq, who, before, row)
	})
	if err != nil {
		return gen.PlanningAllocationWritten{}, err
	}
	s.sendNotices(ctx, replaceNotices(before, row, previousPerson, person, previousProject, project)...)
	return gen.PlanningAllocationWritten{
		Allocation: allocationResponse(row, person, project, c.Manage),
		Warnings:   allocationWarnings(o, project, fields.from, fields.to),
	}, nil
}

// replacement is the row fields replace before with: every field as the
// body gives it, a field left out cleared; confirmed_at set when the row
// becomes confirmed, kept while it stays confirmed, cleared when it is set
// tentative.
func replacement(before store.PlanningAllocation, fields allocationFields, now time.Time, c *callerContext) store.UpdateAllocationParams {
	p := store.UpdateAllocationParams{
		ID: before.ID, PersonID: fields.personID, ProjectID: fields.projectID,
		FromDate: dayParam(fields.from), ToDate: dayParam(fields.to),
		LoadUnit: fields.loadUnit, Load: numericOfHundredths(fields.load), Status: *fields.status,
		Title: fields.title, Note: fields.note, Now: now, UserID: &c.UserID,
	}
	switch {
	case p.Status == statusConfirmed && before.Status == statusConfirmed:
		p.ConfirmedAt = before.ConfirmedAt
	case p.Status == statusConfirmed:
		p.ConfirmedAt = &now
	}
	return p
}

// changedRow reports whether p changes anything of before: a replace that
// changes nothing writes nothing, records nothing and mails nobody.
func changedRow(before store.PlanningAllocation, p store.UpdateAllocationParams) bool {
	after := before
	after.PersonID, after.ProjectID, after.FromDate, after.ToDate = p.PersonID, p.ProjectID, p.FromDate, p.ToDate
	after.LoadUnit, after.Load, after.Status, after.Title, after.Note = p.LoadUnit, p.Load, p.Status, p.Title, p.Note
	return len(changesOf(&before, &after, false)) > 0
}

// replaceNotices is amendment 5 for a replace: on one person, set confirmed
// is "planned", set tentative "no longer confirmed", and a confirmed row
// whose period, load or project changed "changed"; moved to another person,
// a confirmed row is "removed" for the one it left and a confirmed row
// "planned" for the one it reached. A change of a tentative row tells nobody.
func replaceNotices(before, after store.PlanningAllocation, previousPerson, person *contracts.PersonEntry, previousProject, project *contracts.ProjectEntry) []*allocationNotice {
	wasConfirmed, isConfirmed := before.Status == statusConfirmed, after.Status == statusConfirmed
	if before.PersonID != after.PersonID {
		var out []*allocationNotice
		if wasConfirmed {
			out = append(out, noticeTo(noticeRemoved, previousPerson, before, previousProject))
		}
		if isConfirmed {
			out = append(out, noticeTo(noticePlanned, person, after, project))
		}
		return out
	}
	switch {
	case !wasConfirmed && isConfirmed:
		return []*allocationNotice{noticeTo(noticePlanned, person, after, project)}
	case wasConfirmed && !isConfirmed:
		return []*allocationNotice{noticeTo(noticeWithdrawn, person, before, previousProject)}
	case wasConfirmed && isConfirmed && planChanged(before, after):
		return []*allocationNotice{noticeTo(noticeChanged, person, after, project)}
	}
	return nil
}

// planChanged reports whether what the person is told of — the period, the
// load or the project — differs between before and after.
func planChanged(before, after store.PlanningAllocation) bool {
	c := changesOf(&before, &after, true)
	for _, field := range []string{"projectId", "fromDate", "toDate", "loadUnit", "load"} {
		if _, ok := c[field]; ok {
			return true
		}
	}
	return false
}

// DeletePlanningAllocation Delete an allocation
// (DELETE /api/v1/planning/allocations/{id})
func (s *server) DeletePlanningAllocation(ctx context.Context, req gen.DeletePlanningAllocationRequestObject) (gen.DeletePlanningAllocationResponseObject, error) {
	err := s.retryMoved(func() error { return s.remove(ctx, req.Id) })
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusBadRequest:
			return gen.DeletePlanningAllocation400ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusNotFound:
			return gen.DeletePlanningAllocation404Response{}, nil
		case http.StatusConflict:
			return gen.DeletePlanningAllocation409ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusServiceUnavailable:
			return gen.DeletePlanningAllocation503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.DeletePlanningAllocation204Response{}, nil
}

// retryMoved runs write until it does not answer errRowMoved, at most
// maxWriteAttempts times; a row still moving after the last is the 409
// revision_conflict (§13 amendment 35), not a fault.
func (s *server) retryMoved(write func() error) error {
	for range maxWriteAttempts {
		if err := write(); !errors.Is(err, errRowMoved) {
			return err
		}
	}
	return refuseConflict(conflict(codeRevisionConflict, "Revision conflict",
		"The allocation kept changing while it was being written. Read it again and retry."))
}

// remove is DELETE /allocations/{id}: allowed on a closed project too (S4),
// the row's fields recorded as allocation_deleted, and the person told when
// the row was confirmed.
func (s *server) remove(ctx context.Context, id int64) error {
	c, err := s.callerFor(ctx)
	if err != nil {
		return err
	}
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
	var (
		person  *contracts.PersonEntry
		project *contracts.ProjectEntry
	)
	if current.Status == statusConfirmed {
		if project, err = s.projectEntry(ctx, current.ProjectID); err != nil {
			return err
		}
		lo, hi := rowWindow(civil(current.FromDate), civil(current.ToDate))
		staff, err := s.readStaff(ctx, c, lo, hi, 0)
		if err != nil {
			return err
		}
		person = personIn(staff.people, current.PersonID)
	}
	err = s.withPersonLock(ctx, current.PersonID, func(ctx context.Context, txq *store.Queries) error {
		before, err := lockedAllocation(ctx, txq, id)
		if err != nil {
			return err
		}
		if before.Revision != current.Revision {
			return errRowMoved
		}
		if err := txq.DeleteAllocation(ctx, id); err != nil {
			return fmt.Errorf("planning: delete allocation %d: %w", id, err)
		}
		return s.recordEvent(ctx, txq, who, withChanges(allocationEvent(before, eventAllocationDeleted), changesOf(&before, nil, false)))
	})
	if err != nil {
		return err
	}
	if current.Status == statusConfirmed {
		s.sendNotices(ctx, noticeTo(noticeRemoved, person, current, project))
	}
	return nil
}

// ConfirmPlanningAllocation Confirm an allocation
// (POST /api/v1/planning/allocations/{id}/confirm)
func (s *server) ConfirmPlanningAllocation(ctx context.Context, req gen.ConfirmPlanningAllocationRequestObject) (gen.ConfirmPlanningAllocationResponseObject, error) {
	var out gen.PlanningAllocationWritten
	err := s.retryMoved(func() error {
		var err error
		out, err = s.confirm(ctx, req.Id)
		return err
	})
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusBadRequest:
			return gen.ConfirmPlanningAllocation400ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusNotFound:
			return gen.ConfirmPlanningAllocation404Response{}, nil
		case http.StatusConflict:
			return gen.ConfirmPlanningAllocation409ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusServiceUnavailable:
			return gen.ConfirmPlanningAllocation503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.ConfirmPlanningAllocation200JSONResponse(out), nil
}

// confirm is POST /allocations/{id}/confirm (S3, amendment 6): a confirmed
// row answered as it is, with no event and no mail; a tentative one changed
// to confirmed as a PUT would change it — refused on a closed project and
// outside the employment, judged for overbooking — recorded as
// allocation_confirmed, and the person told.
func (s *server) confirm(ctx context.Context, id int64) (gen.PlanningAllocationWritten, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return gen.PlanningAllocationWritten{}, err
	}
	current, found, err := s.allocation(ctx, id)
	if err != nil {
		return gen.PlanningAllocationWritten{}, err
	}
	if !found {
		return gen.PlanningAllocationWritten{}, notFound()
	}
	project, err := s.projectEntry(ctx, current.ProjectID)
	if err != nil {
		return gen.PlanningAllocationWritten{}, err
	}
	from, to := civil(current.FromDate), civil(current.ToDate)
	lo, hi := rowWindow(from, to)
	if current.Status == statusConfirmed {
		staff, err := s.readStaff(ctx, c, lo, hi, 0)
		if err != nil {
			return gen.PlanningAllocationWritten{}, err
		}
		return gen.PlanningAllocationWritten{
			Allocation: allocationResponse(current, personIn(staff.people, current.PersonID), project, c.Manage),
			Warnings:   []gen.PlanningWarning{},
		}, nil
	}
	if projectClosed(project) {
		return gen.PlanningAllocationWritten{}, refuseProjectClosed(project)
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return gen.PlanningAllocationWritten{}, err
	}
	staff, err := s.readStaff(ctx, c, lo, hi, current.PersonID)
	if err != nil {
		return gen.PlanningAllocationWritten{}, err
	}
	person := personIn(staff.people, current.PersonID)
	if !staff.employs(person, from, to) {
		return gen.PlanningAllocationWritten{}, refuseOutsideEmployment()
	}
	rules, err := settings(ctx, store.New(s.deps.Pool))
	if err != nil {
		return gen.PlanningAllocationWritten{}, err
	}

	now := s.deps.Clock()
	var (
		row store.PlanningAllocation
		o   overbooking
	)
	err = s.withPersonLock(ctx, current.PersonID, func(ctx context.Context, txq *store.Queries) error {
		before, err := lockedAllocation(ctx, txq, id)
		if err != nil {
			return err
		}
		if before.Revision != current.Revision {
			return errRowMoved
		}
		candidate := rowOf(before)
		if o, err = judgeOverbooking(ctx, txq, rules.Overbooking, before.PersonID, id, staff.capacity, nil, &candidate); err != nil {
			return err
		}
		if row, err = txq.UpdateAllocation(ctx, store.UpdateAllocationParams{
			ID: id, PersonID: before.PersonID, ProjectID: before.ProjectID, FromDate: before.FromDate, ToDate: before.ToDate,
			LoadUnit: before.LoadUnit, Load: before.Load, Status: statusConfirmed, Title: before.Title, Note: before.Note,
			ConfirmedAt: &now, Now: now, UserID: &c.UserID,
		}); err != nil {
			return fmt.Errorf("planning: confirm allocation %d: %w", id, err)
		}
		return s.recordStatusChange(ctx, txq, who, before, row)
	})
	if err != nil {
		return gen.PlanningAllocationWritten{}, err
	}
	s.sendNotices(ctx, noticeTo(noticePlanned, person, row, project))
	return gen.PlanningAllocationWritten{
		Allocation: allocationResponse(row, person, project, c.Manage),
		Warnings:   allocationWarnings(o, project, from, to),
	}, nil
}

// recordStatusChange records allocation_confirmed when before was tentative
// and after is confirmed, allocation_unconfirmed for the way back, with the
// status's before and after; nothing when the status stayed.
func (s *server) recordStatusChange(ctx context.Context, txq *store.Queries, who actor, before, after store.PlanningAllocation) error {
	if before.Status == after.Status {
		return nil
	}
	kind := eventAllocationConfirmed
	if after.Status == statusTentative {
		kind = eventAllocationUnconfirmed
	}
	return s.recordChanges(ctx, txq, who, allocationEvent(after, kind),
		changes{"status": {From: before.Status, To: after.Status}})
}

// allocationEvent is an event of kind about row, touching its person and
// project.
func allocationEvent(row store.PlanningAllocation, kind string) event {
	personID, projectID := row.PersonID, row.ProjectID
	return event{EntityKind: entityAllocation, EntityID: row.ID, PersonID: &personID, ProjectID: &projectID, Kind: kind}
}

// withChanges is e carrying c as its fields, the payload recordChanges
// writes, for an event recorded whatever c holds.
func withChanges(e event, c changes) event {
	e.Payload = map[string]any{"fields": c}
	return e
}

// recordedValues is a row's fields as an event records them, by the
// contract's names, each present only when set. The note is compared but
// never written (changesOf): an event is planning:view's to read, and the
// note is planning:manage's (S5).
func recordedValues(r *store.PlanningAllocation) map[string]any {
	if r == nil {
		return map[string]any{}
	}
	v := map[string]any{
		"personId":  r.PersonID,
		"projectId": r.ProjectID,
		"fromDate":  civil(r.FromDate).Format(time.DateOnly),
		"toDate":    civil(r.ToDate).Format(time.DateOnly),
		"loadUnit":  r.LoadUnit,
		"load":      loadOf(r.Load),
		"status":    r.Status,
	}
	if r.Title != nil {
		v["title"] = *r.Title
	}
	if r.Note != nil {
		v["note"] = *r.Note
	}
	return v
}

// changesOf is the fields that differ between before and after (either nil
// for a create or a delete), their values before and after; withoutStatus
// leaves the status out, which its own event records (recordStatusChange).
// A changed note is recorded by its name alone, with neither text.
func changesOf(before, after *store.PlanningAllocation, withoutStatus bool) changes {
	b, a := recordedValues(before), recordedValues(after)
	names := make([]string, 0, len(b)+len(a))
	for name := range b {
		names = append(names, name)
	}
	for name := range a {
		names = append(names, name)
	}
	out := changes{}
	for _, name := range slices.Compact(slices.Sorted(slices.Values(names))) {
		if (withoutStatus && name == "status") || b[name] == a[name] {
			continue
		}
		if name == "note" {
			out[name] = fieldChange{}
			continue
		}
		out[name] = fieldChange{From: b[name], To: a[name]}
	}
	return out
}
