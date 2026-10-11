package staff

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is an employment's terms (design S5, §3.3, §4 Terms): one row per
// change, in force from its validFrom until the next row's, never
// overwritten — except as HR's correction of a row, which is an event with
// the values before and after (reading 11). A row's own rules are
// terms_validation.go's; the ones that need the employment, its other rows
// and the manager graph are here.

// termsValidFromIndex is the unique index that holds one row per employment
// and day.
const termsValidFromIndex = "ux_employment_terms_valid_from"

// ownTermsFields are a terms response's fields that are not data about the
// person: an event's payload leaves them out.
var ownTermsFields = []string{"id", "employmentId", "revision", "validTo"}

// termsWriteError is a terms write's own database refusal: a day another row
// of the employment took between the check and the write — which the
// person's lock rules out, but the index is the backstop — is
// terms_valid_from_taken.
func termsWriteError(err error) error {
	if db.IsUniqueViolation(err, termsValidFromIndex) {
		return validFromTaken(time.Time{})
	}
	return err
}

// validFromTaken is the 409 of a day the employment already has a row on.
func validFromTaken(day time.Time) error {
	detail := "The employment already has a terms row on this day; change that row instead."
	if !day.IsZero() {
		detail = fmt.Sprintf("The employment already has a terms row from %s; change that row instead.", day.Format(time.DateOnly))
	}
	return refuseConflict(conflict(codeTermsValidFromTaken, "Day taken", detail))
}

// checkValidFrom is the rule a row's day answers to within its employment
// (design §4 Terms): on or after the start, on or before the end — the end
// day itself is still worked — and not a day another row of chain already
// has. self is the row being replaced, 0 for a new one.
func checkValidFrom(e store.StaffEmployment, chain []store.StaffEmploymentTerm, day time.Time, self int64) error {
	start := civil(e.StartDate)
	switch {
	case day.Before(start):
		return refuseConflict(conflict(codeTermsBeforeStart, "Terms before the start",
			fmt.Sprintf("The employment starts on %s; a terms row takes effect on or after it.", start.Format(time.DateOnly))))
	case e.EndDate.Valid && day.After(e.EndDate.Time):
		return refuseConflict(conflict(codeTermsAfterEnd, "Terms after the end",
			fmt.Sprintf("The employment ends on %s; a terms row takes effect on or before it.", e.EndDate.Time.Format(time.DateOnly))))
	}
	for _, r := range chain {
		if r.ID != self && civil(r.ValidFrom).Equal(day) {
			return validFromTaken(day)
		}
	}
	return nil
}

// checkTermsReferences is a row's references (design §4 Managers, plan Task
// 2's archive rule), judged where they change from baseline — the row before
// a new one, or the row a replace replaces; nil for an employment's first
// row: the manager exists and is never the person; the department and the
// location exist and are not archived. A reference baseline already holds is
// kept as it is, archived or not. The department and the location are held
// against an archive until the transaction ends. prefix names the fields.
func checkTermsReferences(ctx context.Context, txq *store.Queries, personID int64, t store.StaffEmploymentTerm, baseline *store.StaffEmploymentTerm, prefix string) error {
	same := func(a, b *int64) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
	var before store.StaffEmploymentTerm
	if baseline != nil {
		before = *baseline
	}
	if m := t.ManagerPersonID; m != nil {
		if *m == personID {
			return refuseConflict(conflict(codeSelfManager, "Own manager", "A person is never their own manager."))
		}
		if !same(m, before.ManagerPersonID) {
			exists, err := txq.PersonExists(ctx, *m)
			if err != nil {
				return fmt.Errorf("staff: read person %d: %w", *m, err)
			}
			if !exists {
				return refuseInvalid(invalidTermsTitle, fieldErrors{errs: fieldError(prefix+"managerPersonId", fmt.Sprintf("No person has id %d", *m))})
			}
		}
	}
	if d := t.DepartmentID; d != nil && !same(d, before.DepartmentID) {
		dept, err := txq.ShareDepartment(ctx, *d)
		if errors.Is(err, pgx.ErrNoRows) {
			return refuseInvalid(invalidTermsTitle, fieldErrors{errs: fieldError(prefix+"departmentId", fmt.Sprintf("No department has id %d", *d))})
		} else if err != nil {
			return fmt.Errorf("staff: read department %d: %w", *d, err)
		}
		if dept.ArchivedAt != nil {
			return refuseConflict(structureArchived(fmt.Sprintf("Department %s is archived; a terms row cannot be put in it.", dept.Code)))
		}
	}
	if l := t.LocationID; l != nil && !same(l, before.LocationID) {
		loc, err := txq.ShareLocation(ctx, *l)
		if errors.Is(err, pgx.ErrNoRows) {
			return refuseInvalid(invalidTermsTitle, fieldErrors{errs: fieldError(prefix+"locationId", fmt.Sprintf("No location has id %d", *l))})
		} else if err != nil {
			return fmt.Errorf("staff: read location %d: %w", *l, err)
		}
		if loc.ArchivedAt != nil {
			return refuseConflict(structureArchived(fmt.Sprintf("Location %s is archived; a terms row cannot name it.", loc.Name)))
		}
	}
	return nil
}

// edgesDiffer is whether two rows give different edges in the manager graph:
// a manager or a department named, cleared or changed (locks.go's rule for
// when the graph's lock is taken).
func edgesDiffer(a, b *store.StaffEmploymentTerm) bool {
	same := func(x, y *int64) bool { return (x == nil) == (y == nil) && (x == nil || *x == *y) }
	var ax, bx store.StaffEmploymentTerm
	if a != nil {
		ax = *a
	}
	if b != nil {
		bx = *b
	}
	return !same(ax.ManagerPersonID, bx.ManagerPersonID) || !same(ax.DepartmentID, bx.DepartmentID)
}

// insertTerms writes a new row.
func insertTerms(ctx context.Context, txq *store.Queries, t store.StaffEmploymentTerm, now time.Time) (store.StaffEmploymentTerm, error) {
	row, err := txq.InsertTerms(ctx, store.InsertTermsParams{
		EmploymentID: t.EmploymentID, ValidFrom: t.ValidFrom, EmploymentType: t.EmploymentType, TemporaryGround: t.TemporaryGround,
		ExpectedEndDate: t.ExpectedEndDate, AppointmentForm: t.AppointmentForm, HiredOutTo: t.HiredOutTo, FtePercent: t.FtePercent,
		WorkingHoursScheme: t.WorkingHoursScheme, FullTimeHoursPerWeek: t.FullTimeHoursPerWeek, OccupationCode: t.OccupationCode,
		Title: t.Title, EmployeeType: t.EmployeeType, RemunerationType: t.RemunerationType, PayComponents: t.PayComponents,
		PayMethod: t.PayMethod, PaydayText: t.PaydayText, DepartmentID: t.DepartmentID, LocationID: t.LocationID,
		ManagerPersonID: t.ManagerPersonID, CostCentre: t.CostCentre, Division: t.Division, WorkingTimeText: t.WorkingTimeText,
		BreakMinutes: t.BreakMinutes, SpecialHoursText: t.SpecialHoursText, OvertimeRuleText: t.OvertimeRuleText,
		ProbationEndDate: t.ProbationEndDate, ProbationExtensionAgreed: t.ProbationExtensionAgreed,
		ProbationExtendedTo: t.ProbationExtendedTo, ProbationExtensionReason: t.ProbationExtensionReason,
		NoticeEmployee: t.NoticeEmployee, NoticeEmployer: t.NoticeEmployer, NoticeUnit: t.NoticeUnit,
		TariffReference: t.TariffReference, HolidayTermsText: t.HolidayTermsText,
		CompetenceDevelopmentText: t.CompetenceDevelopmentText, SocialSecurityText: t.SocialSecurityText,
		SeniorityDate: t.SeniorityDate, PayGrade: t.PayGrade, Note: t.Note, Now: now,
	})
	if err != nil {
		return row, termsWriteError(fmt.Errorf("staff: add a terms row to employment %d: %w", t.EmploymentID, err))
	}
	return row, nil
}

// updateTerms replaces row id with t's values.
func updateTerms(ctx context.Context, txq *store.Queries, id int64, t store.StaffEmploymentTerm, now time.Time) (store.StaffEmploymentTerm, error) {
	row, err := txq.UpdateTerms(ctx, store.UpdateTermsParams{
		ID: id, ValidFrom: t.ValidFrom, EmploymentType: t.EmploymentType, TemporaryGround: t.TemporaryGround,
		ExpectedEndDate: t.ExpectedEndDate, AppointmentForm: t.AppointmentForm, HiredOutTo: t.HiredOutTo, FtePercent: t.FtePercent,
		WorkingHoursScheme: t.WorkingHoursScheme, FullTimeHoursPerWeek: t.FullTimeHoursPerWeek, OccupationCode: t.OccupationCode,
		Title: t.Title, EmployeeType: t.EmployeeType, RemunerationType: t.RemunerationType, PayComponents: t.PayComponents,
		PayMethod: t.PayMethod, PaydayText: t.PaydayText, DepartmentID: t.DepartmentID, LocationID: t.LocationID,
		ManagerPersonID: t.ManagerPersonID, CostCentre: t.CostCentre, Division: t.Division, WorkingTimeText: t.WorkingTimeText,
		BreakMinutes: t.BreakMinutes, SpecialHoursText: t.SpecialHoursText, OvertimeRuleText: t.OvertimeRuleText,
		ProbationEndDate: t.ProbationEndDate, ProbationExtensionAgreed: t.ProbationExtensionAgreed,
		ProbationExtendedTo: t.ProbationExtendedTo, ProbationExtensionReason: t.ProbationExtensionReason,
		NoticeEmployee: t.NoticeEmployee, NoticeEmployer: t.NoticeEmployer, NoticeUnit: t.NoticeUnit,
		TariffReference: t.TariffReference, HolidayTermsText: t.HolidayTermsText,
		CompetenceDevelopmentText: t.CompetenceDevelopmentText, SocialSecurityText: t.SocialSecurityText,
		SeniorityDate: t.SeniorityDate, PayGrade: t.PayGrade, Note: t.Note, Now: now,
	})
	if err != nil {
		return row, termsWriteError(fmt.Errorf("staff: change terms row %d: %w", id, err))
	}
	return row, nil
}

// recordTermsChange records kind for a terms row from before to after —
// before nil for a new row, after nil for a deleted one — naming the row
// and its employment.
func (s *server) recordTermsChange(ctx context.Context, txq *store.Queries, personID int64, kind string, who actor, before, after *store.StaffEmploymentTerm) error {
	from, err := payloadValues(termsOrNil(before), ownTermsFields...)
	if err != nil {
		return err
	}
	to, err := payloadValues(termsOrNil(after), ownTermsFields...)
	if err != nil {
		return err
	}
	row := after
	if row == nil {
		row = before
	}
	return s.recordRowChange(ctx, txq, personID, kind, who, row.EmploymentID, row.ID, diffFields(from, to))
}

// termsOrNil is a row as an event reads its values — every group — or nil.
func termsOrNil(t *store.StaffEmploymentTerm) any {
	if t == nil {
		return nil
	}
	return termsResponse(*t, nil, everything)
}

// next is the row of chain after the one with id, nil for the latest.
func next(chain []store.StaffEmploymentTerm, id int64) *store.StaffEmploymentTerm {
	for i, r := range chain {
		if r.ID == id && i+1 < len(chain) {
			return &chain[i+1]
		}
	}
	return nil
}

// validToOf is the last day row is in force within chain — the day before
// the next row's — nil for the latest.
func validToOf(chain []store.StaffEmploymentTerm, id int64) *time.Time {
	n := next(chain, id)
	if n == nil {
		return nil
	}
	d := civil(n.ValidFrom).AddDate(0, 0, -1)
	return &d
}

// respondTerms is a row as the caller reads it, after any transaction.
func (s *server) respondTerms(ctx context.Context, caller *callerContext, personID int64, row store.StaffEmploymentTerm) (gen.StaffTerms, error) {
	q := store.New(s.deps.Pool)
	a, err := personAudience(ctx, q, caller, personID)
	if err != nil {
		return gen.StaffTerms{}, err
	}
	chain, err := chainOf(ctx, q, row.EmploymentID)
	if err != nil {
		return gen.StaffTerms{}, err
	}
	return termsResponse(row, validToOf(chain, row.ID), a), nil
}

// GetStaffEmploymentsByIdTerms List an employment's terms
// (GET /api/v1/staff/employments/{id}/terms)
func (s *server) GetStaffEmploymentsByIdTerms(ctx context.Context, req gen.GetStaffEmploymentsByIdTermsRequestObject) (gen.GetStaffEmploymentsByIdTermsResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	var out []gen.StaffTerms
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		e, a, err := visibleEmployment(ctx, q, caller, req.Id)
		if err != nil {
			return err
		}
		chain, err := chainOf(ctx, q, e.ID)
		if err != nil {
			return err
		}
		out = termsListResponse(chain, a)
		return nil
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffEmploymentsByIdTerms404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetStaffEmploymentsByIdTerms200JSONResponse(out), nil
}

// PostStaffEmploymentsByIdTerms Add a terms row
// (POST /api/v1/staff/employments/{id}/terms)
//
// A field the request leaves out takes the value of the row in force before
// the new one; one it sends as null is cleared. A row whose manager or
// department differs from that row's changes an edge of the manager graph:
// it takes the graph's lock and is judged on it from its day on.
func (s *server) PostStaffEmploymentsByIdTerms(ctx context.Context, req gen.PostStaffEmploymentsByIdTermsRequestObject) (gen.PostStaffEmploymentsByIdTermsResponseObject, error) {
	var body gen.StaffTermsRequest
	if req.Body != nil {
		body = *req.Body
	}
	var f fieldErrors
	if body.ValidFrom == nil {
		f.add("validFrom", "The day the row takes effect is required")
	}
	if bodyHas(ctx, "correction") {
		f.add("correction", "correction is for changing a terms row, not for a new one")
	}
	if f.failed() {
		return gen.PostStaffEmploymentsByIdTerms400ApplicationProblemPlusJSONResponse(invalid(invalidTermsTitle, f)), nil
	}
	day := civilDate(body.ValidFrom).Time
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}

	var (
		created store.StaffEmploymentTerm
		person  int64
	)
	err = s.withEmploymentLock(ctx, req.Id, func(ctx context.Context, txq *store.Queries, e store.StaffEmployment) error {
		person = e.PersonID
		chain, err := chainOf(ctx, txq, e.ID)
		if err != nil {
			return err
		}
		if err := checkValidFrom(e, chain, day, 0); err != nil {
			return err
		}
		var before *store.StaffEmploymentTerm
		if i := rowOn(chain, day); i >= 0 {
			before = &chain[i]
		}
		row, f := parseTerms(body, termsSource{given: func(name string) bool { return bodyHas(ctx, name) }, base: before}, civil(e.StartDate), "")
		if f.failed() {
			return refuseInvalid(invalidTermsTitle, f)
		}
		row.EmploymentID, row.ValidFrom = e.ID, dayParam(day)
		edges := edgesDiffer(&row, before)
		if edges {
			if err := lockManagerGraph(ctx, txq); err != nil {
				return err
			}
		}
		if err := checkTermsReferences(ctx, txq, e.PersonID, row, before, ""); err != nil {
			return err
		}
		if created, err = insertTerms(ctx, txq, row, s.deps.Clock()); err != nil {
			return err
		}
		if edges {
			if err := checkManagerCycle(ctx, txq, e.PersonID, day); err != nil {
				return err
			}
		}
		return s.recordTermsChange(ctx, txq, e.PersonID, eventTermsAdded, who, nil, &created)
	})
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PostStaffEmploymentsByIdTerms404Response{}, nil
		case http.StatusConflict:
			return gen.PostStaffEmploymentsByIdTerms409ApplicationProblemPlusJSONResponse(r.problem), nil
		default:
			return gen.PostStaffEmploymentsByIdTerms400ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if err != nil {
		return nil, err
	}
	out, err := s.respondTerms(ctx, caller, person, created)
	if err != nil {
		return nil, err
	}
	return gen.PostStaffEmploymentsByIdTerms201JSONResponse(out), nil
}

// withTermsLock runs fn under the locks of a terms row's person and
// employment, with the employment's chain as it stands and the row's index
// in it. A row that has gone by then is the 404.
func (s *server) withTermsLock(ctx context.Context, id int64, fn func(ctx context.Context, txq *store.Queries, e store.StaffEmployment, chain []store.StaffEmploymentTerm, i int) error) error {
	row, err := store.New(s.deps.Pool).GetTerms(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound()
	} else if err != nil {
		return fmt.Errorf("staff: read terms row %d: %w", id, err)
	}
	return s.withEmploymentLock(ctx, row.EmploymentID, func(ctx context.Context, txq *store.Queries, e store.StaffEmployment) error {
		chain, err := chainOf(ctx, txq, e.ID)
		if err != nil {
			return err
		}
		for i, r := range chain {
			if r.ID == id {
				return fn(ctx, txq, e, chain, i)
			}
		}
		return notFound()
	})
}

// PutStaffTermsById Change a terms row
// (PUT /api/v1/staff/terms/{id})
//
// A full replace of the latest row, or of any row as HR's correction. The
// first row stays dated the start. A replace that moves the row or changes
// its manager or department is judged on the manager graph from the earlier
// of its old and its new day.
func (s *server) PutStaffTermsById(ctx context.Context, req gen.PutStaffTermsByIdRequestObject) (gen.PutStaffTermsByIdResponseObject, error) {
	var body gen.StaffTermsRequest
	if req.Body != nil {
		body = *req.Body
	}
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutStaffTermsById400ApplicationProblemPlusJSONResponse(invalid(invalidTermsTitle, badIfMatch())), nil
	}
	if body.ValidFrom == nil {
		return gen.PutStaffTermsById400ApplicationProblemPlusJSONResponse(invalid(invalidTermsTitle, fieldErrors{
			errs: fieldError("validFrom", "The day the row takes effect is required"),
		})), nil
	}
	day := civilDate(body.ValidFrom).Time
	correction := body.Correction != nil && *body.Correction
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}

	var (
		saved  store.StaffEmploymentTerm
		person int64
	)
	err = s.withTermsLock(ctx, req.Id, func(ctx context.Context, txq *store.Queries, e store.StaffEmployment, chain []store.StaffEmploymentTerm, i int) error {
		person = e.PersonID
		current := chain[i]
		if err := checkRevision("terms row", current.Revision, revision); err != nil {
			return err
		}
		if i != len(chain)-1 && !correction {
			return refuseConflict(conflict(codeTermsNotLatest, "Not the latest row",
				"Only the latest terms row is changed; add a new row, or send correction true to correct this one."))
		}
		if i == 0 && !day.Equal(civil(e.StartDate)) {
			return refuseConflict(conflict(codeTermsFirstRowStart, "The first row is dated the start",
				fmt.Sprintf("An employment's first terms row takes effect on its start, %s; change the employment's start to move it.", civil(e.StartDate).Format(time.DateOnly))))
		}
		if err := checkValidFrom(e, chain, day, current.ID); err != nil {
			return err
		}
		row, f := parseTerms(body, termsSource{given: func(string) bool { return true }}, civil(e.StartDate), "")
		if f.failed() {
			return refuseInvalid(invalidTermsTitle, f)
		}
		row.EmploymentID, row.ValidFrom = e.ID, dayParam(day)
		old := civil(current.ValidFrom)
		edges := edgesDiffer(&row, &current) || !day.Equal(old)
		if edges {
			if err := lockManagerGraph(ctx, txq); err != nil {
				return err
			}
		}
		if err := checkTermsReferences(ctx, txq, e.PersonID, row, &current, ""); err != nil {
			return err
		}
		var err error
		if saved, err = updateTerms(ctx, txq, current.ID, row, s.deps.Clock()); err != nil {
			return err
		}
		if edges {
			if err := checkManagerCycle(ctx, txq, e.PersonID, earliest(day, old)); err != nil {
				return err
			}
		}
		return s.recordTermsChange(ctx, txq, e.PersonID, eventTermsCorrected, who, &current, &saved)
	})
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PutStaffTermsById404Response{}, nil
		case http.StatusConflict:
			return gen.PutStaffTermsById409ApplicationProblemPlusJSONResponse(r.problem), nil
		default:
			return gen.PutStaffTermsById400ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if err != nil {
		return nil, err
	}
	out, err := s.respondTerms(ctx, caller, person, saved)
	if err != nil {
		return nil, err
	}
	return gen.PutStaffTermsById200JSONResponse(out), nil
}

// DeleteStaffTermsById Delete the latest terms row
// (DELETE /api/v1/staff/terms/{id})
//
// The row before is in force again from the deleted row's day; when its
// manager or department differs, an edge of the manager graph changes, and
// the deletion takes the graph's lock and is judged on it from that day.
func (s *server) DeleteStaffTermsById(ctx context.Context, req gen.DeleteStaffTermsByIdRequestObject) (gen.DeleteStaffTermsByIdResponseObject, error) {
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	err = s.withTermsLock(ctx, req.Id, func(ctx context.Context, txq *store.Queries, e store.StaffEmployment, chain []store.StaffEmploymentTerm, i int) error {
		current := chain[i]
		if i == 0 {
			return refuseConflict(conflict(codeTermsFirstRowStart, "The first row stays",
				"An employment's first terms row, dated its start, is never deleted; correct it instead."))
		}
		if i != len(chain)-1 {
			return refuseConflict(conflict(codeTermsNotLatest, "Not the latest row", "Only the latest terms row is deleted."))
		}
		edges := edgesDiffer(&current, &chain[i-1])
		if edges {
			if err := lockManagerGraph(ctx, txq); err != nil {
				return err
			}
		}
		if err := txq.DeleteTerms(ctx, current.ID); err != nil {
			return fmt.Errorf("staff: delete terms row %d: %w", current.ID, err)
		}
		if edges {
			if err := checkManagerCycle(ctx, txq, e.PersonID, civil(current.ValidFrom)); err != nil {
				return err
			}
		}
		return s.recordTermsChange(ctx, txq, e.PersonID, eventTermsDeleted, who, &current, nil)
	})
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusNotFound {
			return gen.DeleteStaffTermsById404Response{}, nil
		}
		return gen.DeleteStaffTermsById409ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.DeleteStaffTermsById204Response{}, nil
}
