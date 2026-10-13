package staff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is absences (design S8, S12, §4, plan Task 3): requested by the
// person, or registered — a type that is a notification — by the person or
// by HR on their behalf; changed, deleted and cancelled while nobody has
// decided them; read by everyone who sees the person, shaped per §5. A
// manager neither requests nor registers for a report (S12, pre-flight M2),
// and a registered row — a sick one included — is HR's to change or delete:
// ending a sykmelding early is HR's job, the person asks (M8, reading 16).
//
// Every write is one transaction under the person's lock (design §4
// Locking): the person, then the type it names FOR KEY SHARE (a change
// widening the type waits for it, Task 2), then their rows, read in the lock
// for the overlap rule. The decisions — approve, reject, a decider's cancel,
// HR's correction — are delivery B's Task 4's; the sick-leave case a sick
// row joins is Task 5's, until when a sick row is a plain registration with
// no case.

// The statuses of an absence (design §3.5, S12).
const (
	absenceRequested  = "requested"
	absenceApproved   = "approved"
	absenceRejected   = "rejected"
	absenceCancelled  = "cancelled"
	absenceRegistered = "registered"
)

// absenceStatuses are the statuses a list filters on.
var absenceStatuses = []string{absenceRequested, absenceApproved, absenceRejected, absenceCancelled, absenceRegistered}

// onlyColleague is an audience that sees the person as a colleague and in no
// other way: it is shown that a person is away — approved and registered
// rows — never a request or a refusal.
func (a audience) onlyColleague() bool {
	return a.Colleague && !a.Self && !a.Manager && !a.Manage && !a.PersonalData
}

// seesAbsence is whether a sees an absence in status.
func (a audience) seesAbsence(status string) bool {
	if !a.reads(groupAbsence) {
		return false
	}
	return !a.onlyColleague() || status == absenceApproved || status == absenceRegistered
}

// absenceResponse is one absence as a reads it (design §5): the dates and
// the status to everyone who sees it; the type to the person, the manager
// and HR — an HR-only type shown as leave to all but the person and
// staff:personal-data — and as absent to a colleague; percent, hours, note
// and degree never to a colleague (plan pre-flight I4).
func absenceResponse(row store.StaffAbsence, kind, visibility, unit string, a audience) gen.StaffAbsence {
	out := gen.StaffAbsence{
		Id:       row.ID,
		PersonId: row.PersonID,
		Status:   gen.StaffAbsenceStatus(row.Status),
		FirstDay: openapi_types.Date{Time: civil(row.FirstDay)},
		LastDay:  openapi_types.Date{Time: civil(row.LastDay)},
		ShownAs:  gen.StaffAbsenceShownAsAbsent,
		Revision: row.Revision,
	}
	if unit == unitDays {
		out.WorkingDays = int32(workingDays(civil(row.FirstDay), civil(row.LastDay)))
	}
	switch {
	case a.reads(groupAbsenceType) && (visibility == visibilityAbsent || a.readsHROnlyTypes()):
		out.ShownAs = gen.StaffAbsenceShownAsType
		id := row.TypeID
		out.TypeId = &id
	case a.reads(groupAbsenceType):
		out.ShownAs = gen.StaffAbsenceShownAsLeave
	}
	if a.reads(groupAbsenceDetail) {
		percent := numberOf(row.Percent)
		out.Percent = &percent
		if row.Hours.Valid {
			h := numberOf(row.Hours)
			out.Hours = &h
		}
		if row.DegreePercent.Valid {
			d := numberOf(row.DegreePercent)
			out.DegreePercent = &d
		}
		out.Note = row.Note
	}
	return out
}

// absenceValues is a row's fields as an absence event names them, each value
// as the contract would: the type by its id, the days as dates, the percent,
// the hours and the degree as numbers, the note and the status as text.
func absenceValues(row store.StaffAbsence) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	put := func(name string, v any) {
		b, _ := json.Marshal(v)
		out[name] = b
	}
	put(fieldType, row.TypeID)
	put("firstDay", civil(row.FirstDay).Format(time.DateOnly))
	put("lastDay", civil(row.LastDay).Format(time.DateOnly))
	put("status", row.Status)
	put("percent", numberOf(row.Percent))
	if row.Hours.Valid {
		put("hours", numberOf(row.Hours))
	}
	if row.DegreePercent.Valid {
		put(fieldDegree, numberOf(row.DegreePercent))
	}
	if row.Note != nil {
		put("note", *row.Note)
	}
	return out
}

// recordAbsence records kind about row from before to after (either nil);
// health is whether a type on either side is health data.
func (s *server) recordAbsence(ctx context.Context, txq *store.Queries, personID int64, kind string, who actor, id int64, before, after *store.StaffAbsence, health bool) error {
	var b, a map[string]json.RawMessage
	if before != nil {
		b = absenceValues(*before)
	}
	if after != nil {
		a = absenceValues(*after)
	}
	payload, classes := absenceEventPayload(id, b, a, health)
	return s.recordClassedEvent(ctx, txq, personID, kind, who, payload, classes)
}

// absenceWrite is what a write under the person's lock judged and wrote: the
// row, its type, and — for a type drawing on the holiday account — the
// accounts of the years it touches after the write.
type absenceWrite struct {
	row      store.StaffAbsence
	typ      store.StaffAbsenceType
	accounts []holidayAccount
	audience audience
}

// judgeAbsence reads the type p names under the person's lock and judges
// every rule of an absence of it for personID: the type's (judgeType), the
// days inside one of the person's employments, and overlap with the person's
// other rows — exclude is the row being changed, 0 for a new one.
func judgeAbsence(ctx context.Context, txq *store.Queries, personID, exclude int64, p parsedAbsence) (parsedAbsence, store.StaffAbsenceType, error) {
	t, err := txq.ShareAbsenceType(ctx, p.TypeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, t, refuseInvalid(invalidAbsenceTitle, fieldErrors{errs: fieldError("typeId", fmt.Sprintf("No absence type has id %d", p.TypeID))})
	} else if err != nil {
		return p, t, fmt.Errorf("staff: read absence type %d: %w", p.TypeID, err)
	}
	var f fieldErrors
	p = judgeType(t, p, &f)
	employments, err := txq.ListPersonEmployments(ctx, personID)
	if err != nil {
		return p, t, fmt.Errorf("staff: read person %d's employments: %w", personID, err)
	}
	if !withinEmployment(employments, p.First, p.Last) {
		f.add("firstDay", "The days must lie within one of the person's employments")
	}
	if f.failed() {
		return p, t, refuseInvalid(invalidAbsenceTitle, f)
	}
	rows, err := txq.OverlappingAbsences(ctx, store.OverlappingAbsencesParams{
		PersonID: personID, Exclude: exclude, FirstDay: dayParam(p.First), LastDay: dayParam(p.Last),
	})
	if err != nil {
		return p, t, fmt.Errorf("staff: read person %d's absences: %w", personID, err)
	}
	existing := make([]overlapRow, 0, len(rows))
	for _, r := range rows {
		existing = append(existing, overlapOf(r.StaffAbsence, r.Kind, r.Unit, r.AllowsPercent))
	}
	candidate := overlapRow{First: p.First, Last: p.Last, AllowsPercent: t.AllowsPercent, Share: shareOf(t.Kind, p.Percent, p.Degree)}
	if t.Unit == unitHours {
		candidate.Hours = hundredthsOfNumeric(p.Hours)
	}
	if overlapRefused(candidate, existing) {
		return p, t, refuseConflict(conflict(codeAbsenceOverlap, "Overlapping absence",
			"The person already has an absence on one of these days that this one cannot stand beside."))
	}
	return p, t, nil
}

// overlapOf is a stored row as the overlap rule reads it.
func overlapOf(a store.StaffAbsence, kind, unit string, allowsPercent bool) overlapRow {
	out := overlapRow{First: civil(a.FirstDay), Last: civil(a.LastDay), AllowsPercent: allowsPercent, Share: shareOf(kind, a.Percent, a.DegreePercent)}
	if unit == unitHours {
		out.Hours = hundredthsOfNumeric(a.Hours)
	}
	return out
}

// statusFor is the status a new or changed row of t starts in: requested when
// the type needs a decision, registered when it is a notification (S12).
func statusFor(t store.StaffAbsenceType) string {
	if t.NeedsDecision {
		return absenceRequested
	}
	return absenceRegistered
}

// accountsAfter is, for a type drawing on the holiday account, the person's
// account of each year row touches, read in the write's transaction.
func (s *server) accountsAfter(ctx context.Context, txq *store.Queries, settings store.StaffSetting, t store.StaffAbsenceType, row store.StaffAbsence) ([]holidayAccount, error) {
	if !t.DrawsHoliday || t.Unit != unitDays {
		return nil, nil
	}
	in, err := readAccountInput(ctx, txq, settings, row.PersonID)
	if err != nil {
		return nil, err
	}
	var out []holidayAccount
	for y := civil(row.FirstDay).Year(); y <= civil(row.LastDay).Year(); y++ {
		out = append(out, accountFor(in, y))
	}
	return out, nil
}

// respondWrite is a write's answer: the row as the writer reads it, with the
// accounts after it and whether one is overdrawn.
func respondWrite(w absenceWrite, personID int64) gen.StaffAbsence {
	out := absenceResponse(w.row, w.typ.Kind, w.typ.Visibility, w.typ.Unit, w.audience)
	if w.accounts != nil {
		accounts := make([]gen.StaffHolidayAccount, 0, len(w.accounts))
		overdrawn := false
		for _, a := range w.accounts {
			accounts = append(accounts, holidayAccountResponse(personID, a))
			overdrawn = overdrawn || a.Overdrawn
		}
		out.Accounts, out.Overdrawn = &accounts, &overdrawn
	}
	return out
}

// createAbsence requests or registers p for personID under the person's
// lock, recorded as absence_requested or absence_registered.
func (s *server) createAbsence(ctx context.Context, caller *callerContext, who actor, personID int64, p parsedAbsence) (absenceWrite, error) {
	var out absenceWrite
	err := s.withPersonLock(ctx, personID, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		p, t, err := judgeAbsence(ctx, txq, personID, 0, p)
		if err != nil {
			return err
		}
		row, err := txq.InsertAbsence(ctx, store.InsertAbsenceParams{
			PersonID: personID, TypeID: t.ID, FirstDay: dayParam(p.First), LastDay: dayParam(p.Last), Hours: p.Hours,
			Percent: p.Percent, Status: statusFor(t), Note: p.Note, DegreePercent: p.Degree,
			RequestedByUserID: caller.UserID, Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("staff: write person %d's absence: %w", personID, err)
		}
		kind := eventAbsenceRegistered
		if row.Status == absenceRequested {
			kind = eventAbsenceRequested
		}
		if err := s.recordAbsence(ctx, txq, personID, kind, who, row.ID, nil, &row, healthKind(t.Kind)); err != nil {
			return err
		}
		out = absenceWrite{row: row, typ: t}
		if out.accounts, err = s.accountsAfter(ctx, txq, caller.Settings, t, row); err != nil {
			return err
		}
		out.audience, err = personAudience(ctx, txq, caller, personID)
		return err
	})
	return out, err
}

// PostStaffAbsences Request or register an absence
// (POST /api/v1/staff/absences)
func (s *server) PostStaffAbsences(ctx context.Context, req gen.PostStaffAbsencesRequestObject) (gen.PostStaffAbsencesResponseObject, error) {
	var body gen.StaffAbsenceRequest
	if req.Body != nil {
		body = *req.Body
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	if !caller.Manage && (caller.Person == nil || *caller.Person != body.PersonId) {
		return gen.PostStaffAbsences403JSONResponse(apicommon.ForbiddenBody()), nil
	}
	p, f := parseAbsence(fieldsOfRequest(body))
	if f.failed() {
		return gen.PostStaffAbsences400ApplicationProblemPlusJSONResponse(invalid(invalidAbsenceTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	w, err := s.createAbsence(ctx, caller, who, body.PersonId, p)
	if errors.Is(err, errPersonNotFound) {
		return gen.PostStaffAbsences404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusConflict {
			return gen.PostStaffAbsences409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.PostStaffAbsences400ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PostStaffAbsences201JSONResponse(respondWrite(w, body.PersonId)), nil
}

// absenceListFilter is a list's validated filters.
type absenceListFilter struct {
	personID       *int64
	from, to       *openapi_types.Date
	status         *string
	page, pageSize int32
}

// parseAbsenceList judges a list's filters: the paging, from on or before to,
// and a status the contract names.
func parseAbsenceList(personID *int64, from, to *openapi_types.Date, status *gen.StaffAbsenceStatus, page, pageSize *int32) (absenceListFilter, fieldErrors) {
	f := validatePageParams(page, pageSize)
	out := absenceListFilter{personID: personID, from: from, to: to}
	out.page, out.pageSize = pageParams(page, pageSize)
	if from != nil && to != nil && to.Before(from.Time) {
		f.add("to", "to cannot be before from")
	}
	var msg string
	if out.status, msg = parseEnum("A status", status, absenceStatuses); msg != "" {
		f.add("status", msg)
	}
	return out, f
}

// listAbsences is one page of the absences the audiences allow: everyone's
// rows when everyone, else full's people's every row and colleagues' people's
// approved and registered ones; audienceOf shapes each row.
func (s *server) listAbsences(ctx context.Context, q *store.Queries, filter absenceListFilter, everyone bool, full, colleagues []int64, audienceOf func(personID int64) audience) (gen.PaginatedResponseOfStaffAbsence, error) {
	params := store.ListAbsencesParams{
		PersonID: filter.personID, Status: filter.status, Everyone: everyone, FullIds: full, ColleagueIds: colleagues,
		PageSize: filter.pageSize, PageOffset: (filter.page - 1) * filter.pageSize,
	}
	if filter.from != nil {
		params.FromDay = dayParam(filter.from.Time)
	}
	if filter.to != nil {
		params.ToDay = dayParam(filter.to.Time)
	}
	total, err := q.CountAbsences(ctx, store.CountAbsencesParams{
		PersonID: params.PersonID, FromDay: params.FromDay, ToDay: params.ToDay, Status: params.Status,
		Everyone: everyone, FullIds: full, ColleagueIds: colleagues,
	})
	if err != nil {
		return gen.PaginatedResponseOfStaffAbsence{}, fmt.Errorf("staff: count the absences: %w", err)
	}
	rows, err := q.ListAbsences(ctx, params)
	if err != nil {
		return gen.PaginatedResponseOfStaffAbsence{}, fmt.Errorf("staff: list the absences: %w", err)
	}
	return gen.PaginatedResponseOfStaffAbsence{
		Data: mapRows(rows, func(r store.ListAbsencesRow) gen.StaffAbsence {
			return absenceResponse(r.StaffAbsence, r.Kind, r.Visibility, r.Unit, audienceOf(r.StaffAbsence.PersonID))
		}),
		Pagination: apicommon.Pagination(filter.page, filter.pageSize, int32(total)),
	}, nil
}

// GetStaffAbsences List absences
// (GET /api/v1/staff/absences)
//
// HR — staff:manage, staff:personal-data — sees everyone's rows. Anyone else
// sees their own, their reports' today by the one manager rule (managersOf),
// and — with staff:view — the approved and registered rows of every person
// active today, as a colleague.
func (s *server) GetStaffAbsences(ctx context.Context, req gen.GetStaffAbsencesRequestObject) (gen.GetStaffAbsencesResponseObject, error) {
	prm := req.Params
	filter, f := parseAbsenceList(prm.PersonId, prm.From, prm.To, prm.Status, prm.Page, prm.PageSize)
	if f.failed() {
		return gen.GetStaffAbsences400ApplicationProblemPlusJSONResponse(invalid(invalidListTitle, f)), nil
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	var out gen.PaginatedResponseOfStaffAbsence
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		everyone := caller.Manage || caller.PersonalData
		var reports, active []int64
		if !everyone && caller.Person != nil {
			g, err := loadManagerGraph(ctx, q, nil)
			if err != nil {
				return err
			}
			reports = g.reportsOf(*caller.Person, caller.Today)
		}
		if caller.View {
			var err error
			if active, err = q.PeopleActiveOn(ctx, dayParam(caller.Today)); err != nil {
				return fmt.Errorf("staff: read the people active today: %w", err)
			}
		}
		full := slices.Clone(reports)
		if caller.Person != nil {
			full = append(full, *caller.Person)
		}
		var colleagues []int64
		if caller.View {
			colleagues = active
		}
		var err error
		out, err = s.listAbsences(ctx, q, filter, everyone, full, colleagues, func(personID int64) audience {
			return caller.audienceOn(personID, slices.Contains(reports, personID), slices.Contains(active, personID))
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.GetStaffAbsences200JSONResponse(out), nil
}

// readAbsenceFor is one absence and the caller's audience on its person;
// notFound when there is none or the caller may not see it.
func readAbsenceFor(ctx context.Context, q *store.Queries, caller *callerContext, id int64) (store.GetAbsenceRow, audience, error) {
	row, err := q.GetAbsence(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, audience{}, notFound()
	} else if err != nil {
		return row, audience{}, fmt.Errorf("staff: read absence %d: %w", id, err)
	}
	a, err := personAudience(ctx, q, caller, row.StaffAbsence.PersonID)
	if err != nil {
		return row, a, err
	}
	if !a.seesAbsence(row.StaffAbsence.Status) {
		return row, a, notFound()
	}
	return row, a, nil
}

// GetStaffAbsencesById Get an absence
// (GET /api/v1/staff/absences/{id})
func (s *server) GetStaffAbsencesById(ctx context.Context, req gen.GetStaffAbsencesByIdRequestObject) (gen.GetStaffAbsencesByIdResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	var out gen.StaffAbsence
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		row, a, err := readAbsenceFor(ctx, q, caller, req.Id)
		if err != nil {
			return err
		}
		out = absenceResponse(row.StaffAbsence, row.Kind, row.Visibility, row.Unit, a)
		return nil
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffAbsencesById404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetStaffAbsencesById200JSONResponse(out), nil
}

// errForbidden is a write by someone who sees the row but may not change it:
// the uniform 403.
var errForbidden = errors.New("staff: not the caller's to change")

// mayChangeAbsence reads the row and answers whether the caller may write it
// at all — the person or staff:manage — before any lock: notFound for a row
// the caller does not see, errForbidden for one they see and may not touch.
func (s *server) mayChangeAbsence(ctx context.Context, caller *callerContext, id int64) (store.GetAbsenceRow, error) {
	q := store.New(s.deps.Pool)
	row, _, err := readAbsenceFor(ctx, q, caller, id)
	if err != nil {
		return row, err
	}
	self := caller.Person != nil && *caller.Person == row.StaffAbsence.PersonID
	if !self && !caller.Manage {
		return row, errForbidden
	}
	return row, nil
}

// lockAbsence takes the absence under its person's lock, notFound when it is
// gone or no longer the person's.
func lockAbsence(ctx context.Context, txq *store.Queries, id, personID int64) (store.StaffAbsence, store.StaffAbsenceType, error) {
	row, err := txq.LockAbsence(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.PersonID != personID) {
		return row, store.StaffAbsenceType{}, notFound()
	} else if err != nil {
		return row, store.StaffAbsenceType{}, fmt.Errorf("staff: lock absence %d: %w", id, err)
	}
	t, err := txq.GetAbsenceType(ctx, row.TypeID)
	if err != nil {
		return row, t, fmt.Errorf("staff: read absence type %d: %w", row.TypeID, err)
	}
	return row, t, nil
}

// undecidedFor is whether a row in status is one the caller changes or
// removes: a request, or — for staff:manage — a registration (reading 16).
func undecidedFor(status string, manage bool) bool {
	return status == absenceRequested || (manage && status == absenceRegistered)
}

// notRequested is the 409 of a change or a delete of a row that is not, or
// no longer, the caller's to change.
func notRequested() error {
	return refuseConflict(conflict(codeAbsenceNotRequested, "Not a request",
		"Only a request is changed or removed by its person, and a registered absence by HR; a decided one is cancelled or corrected instead."))
}

// PutStaffAbsencesById Change an absence
// (PUT /api/v1/staff/absences/{id})
func (s *server) PutStaffAbsencesById(ctx context.Context, req gen.PutStaffAbsencesByIdRequestObject) (gen.PutStaffAbsencesByIdResponseObject, error) {
	var body gen.StaffAbsenceChangeRequest
	if req.Body != nil {
		body = *req.Body
	}
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutStaffAbsencesById400ApplicationProblemPlusJSONResponse(invalid(invalidAbsenceTitle, badIfMatch())), nil
	}
	if f := s.unknownProperties(ctx, absenceChangeSchema); f.failed() {
		return gen.PutStaffAbsencesById400ApplicationProblemPlusJSONResponse(invalid(invalidAbsenceTitle, f)), nil
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	current, err := s.mayChangeAbsence(ctx, caller, req.Id)
	if errors.Is(err, errForbidden) {
		return gen.PutStaffAbsencesById403JSONResponse(apicommon.ForbiddenBody()), nil
	}
	if _, ok := asRefusal(err); ok {
		return gen.PutStaffAbsencesById404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	p, f := parseAbsence(fieldsOfChange(body))
	if f.failed() {
		return gen.PutStaffAbsencesById400ApplicationProblemPlusJSONResponse(invalid(invalidAbsenceTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	personID := current.StaffAbsence.PersonID
	var w absenceWrite
	err = s.withPersonLock(ctx, personID, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		before, beforeType, err := lockAbsence(ctx, txq, req.Id, personID)
		if err != nil {
			return err
		}
		if err := checkRevision("absence", before.Revision, revision); err != nil {
			return err
		}
		if !undecidedFor(before.Status, caller.Manage) {
			return notRequested()
		}
		p, t, err := judgeAbsence(ctx, txq, personID, before.ID, p)
		if err != nil {
			return err
		}
		row, err := txq.UpdateAbsence(ctx, store.UpdateAbsenceParams{
			ID: before.ID, TypeID: t.ID, FirstDay: dayParam(p.First), LastDay: dayParam(p.Last), Hours: p.Hours,
			Percent: p.Percent, DegreePercent: p.Degree, Note: p.Note, Status: statusFor(t), Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("staff: change absence %d: %w", before.ID, err)
		}
		if err := s.recordAbsence(ctx, txq, personID, eventAbsenceUpdated, who, row.ID, &before, &row,
			healthKind(beforeType.Kind) || healthKind(t.Kind)); err != nil {
			return err
		}
		w = absenceWrite{row: row, typ: t}
		if w.accounts, err = s.accountsAfter(ctx, txq, caller.Settings, t, row); err != nil {
			return err
		}
		w.audience, err = personAudience(ctx, txq, caller, personID)
		return err
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.PutStaffAbsencesById404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PutStaffAbsencesById404Response{}, nil
		case http.StatusConflict:
			return gen.PutStaffAbsencesById409ApplicationProblemPlusJSONResponse(r.problem), nil
		default:
			return gen.PutStaffAbsencesById400ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.PutStaffAbsencesById200JSONResponse(respondWrite(w, personID)), nil
}

// DeleteStaffAbsencesById Delete an absence
// (DELETE /api/v1/staff/absences/{id})
func (s *server) DeleteStaffAbsencesById(ctx context.Context, req gen.DeleteStaffAbsencesByIdRequestObject) (gen.DeleteStaffAbsencesByIdResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	current, err := s.mayChangeAbsence(ctx, caller, req.Id)
	if errors.Is(err, errForbidden) {
		return gen.DeleteStaffAbsencesById403JSONResponse(apicommon.ForbiddenBody()), nil
	}
	if _, ok := asRefusal(err); ok {
		return gen.DeleteStaffAbsencesById404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	personID := current.StaffAbsence.PersonID
	err = s.withPersonLock(ctx, personID, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		row, t, err := lockAbsence(ctx, txq, req.Id, personID)
		if err != nil {
			return err
		}
		if !undecidedFor(row.Status, caller.Manage) {
			return notRequested()
		}
		if err := txq.DeleteAbsence(ctx, row.ID); err != nil {
			return fmt.Errorf("staff: delete absence %d: %w", row.ID, err)
		}
		return s.recordAbsence(ctx, txq, personID, eventAbsenceDeleted, who, row.ID, &row, nil, healthKind(t.Kind))
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.DeleteStaffAbsencesById404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusConflict {
			return gen.DeleteStaffAbsencesById409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.DeleteStaffAbsencesById404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.DeleteStaffAbsencesById204Response{}, nil
}

// PostStaffAbsencesByIdCancel Cancel an absence request
// (POST /api/v1/staff/absences/{id}/cancel)
func (s *server) PostStaffAbsencesByIdCancel(ctx context.Context, req gen.PostStaffAbsencesByIdCancelRequestObject) (gen.PostStaffAbsencesByIdCancelResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	current, err := s.mayChangeAbsence(ctx, caller, req.Id)
	if errors.Is(err, errForbidden) {
		return gen.PostStaffAbsencesByIdCancel403JSONResponse(apicommon.ForbiddenBody()), nil
	}
	if _, ok := asRefusal(err); ok {
		return gen.PostStaffAbsencesByIdCancel404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	personID := current.StaffAbsence.PersonID
	var w absenceWrite
	err = s.withPersonLock(ctx, personID, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		before, t, err := lockAbsence(ctx, txq, req.Id, personID)
		if err != nil {
			return err
		}
		if before.Status != absenceRequested {
			return refuseConflict(conflict(codeAbsenceNotCancellable, "Not cancellable",
				"Only a request is cancelled by its person; this absence is not one."))
		}
		row, err := txq.SetAbsenceStatus(ctx, store.SetAbsenceStatusParams{ID: before.ID, Status: absenceCancelled, Now: s.deps.Clock()})
		if err != nil {
			return fmt.Errorf("staff: cancel absence %d: %w", before.ID, err)
		}
		if err := s.recordAbsence(ctx, txq, personID, eventAbsenceCancelled, who, row.ID, &before, &row, healthKind(t.Kind)); err != nil {
			return err
		}
		w = absenceWrite{row: row, typ: t}
		w.audience, err = personAudience(ctx, txq, caller, personID)
		return err
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.PostStaffAbsencesByIdCancel404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusConflict {
			return gen.PostStaffAbsencesByIdCancel409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.PostStaffAbsencesByIdCancel404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PostStaffAbsencesByIdCancel200JSONResponse(respondWrite(w, personID)), nil
}
