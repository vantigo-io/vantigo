package staff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the sick-leave cases as stored and served (design S10, §3.5,
// §5's case rows, plan Task 5). A case is derived from its person's sick
// rows (sick_cases.go) and stored under the person's lock whenever a sick
// row is written: its days, its rows' case, and its milestones' dates — a
// milestone's done day, waiver and note are never touched by it. Its own
// writes — the accommodation note and the estimate, a milestone done,
// waived or noted, HR's close — take the person's lock, then the case.
//
// Who reads a case: the person, their manager today, the Approver
// (staff:approve) and HR — the readers of the absence detail; the
// accommodation note all but the Approver; a milestone's note the person and
// staff:personal-data alone. Who writes: milestones done and waived by the
// manager, the Approver and HR; the accommodation note by the manager and
// HR; the estimate and the close by staff:manage; the milestone's note by
// staff:personal-data. Anyone else gets the 404 of a case they cannot write,
// whatever its state (authority before status, review C1).

// The bounds of a case's notes (design §3.5).
const (
	accommodationNoteMax = 2000
	milestoneNoteMax     = 2000
	waiveReasonMax       = 500
)

// invalidCaseTitle is the title of a case write's 400.
const invalidCaseTitle = "Invalid sick-leave case"

// caseValue is a case event's field value as the contract names it.
func caseValue(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// recordCaseEvent records kind about caseID for personID, from before to
// after, every field class E, with the case's types (typeIds) and the
// milestone it is about when it is one's.
func (s *server) recordCaseEvent(ctx context.Context, txq *store.Queries, personID, caseID int64, kind, milestone string, who actor, before, after map[string]json.RawMessage) error {
	payload, classes := classedPayload("caseId", caseID, diffFields(before, after), func(string) string { return healthClass })
	types, err := txq.CaseTypeIDs(ctx, &caseID)
	if err != nil {
		return fmt.Errorf("staff: read case %d's types: %w", caseID, err)
	}
	typeClasses := make([]string, len(types))
	for i := range typeClasses {
		typeClasses[i] = healthClass
	}
	payload["typeIds"] = types
	payload["typeClasses"] = typeClasses
	if milestone != "" {
		payload["milestone"] = milestone
	}
	return s.recordClassedEvent(ctx, txq, personID, kind, who, payload, classes)
}

// dayValue is a date as a case event names it, or null.
func dayValue(d pgtype.Date) json.RawMessage {
	if !d.Valid {
		return caseValue(nil)
	}
	return caseValue(d.Time.Format(time.DateOnly))
}

// reconcileCases derives personID's cases again from their registered sick
// rows, under the person's lock: the rows gathered by the join
// (chainsOf); each case keeps the lowest id its rows named that no earlier
// case took, else one is opened (case_opened); its days stored — a row that
// joined after HR closed it opens it again — its rows named, its milestones
// dated. A case no row names any more is removed, its documents moving to
// the case that took its rows, else to none.
func (s *server) reconcileCases(ctx context.Context, txq *store.Queries, who actor, personID int64) error {
	spans, _, err := personSickSpans(ctx, txq, personID, 0)
	if err != nil {
		return err
	}
	stored, err := txq.LockPersonCases(ctx, personID)
	if err != nil {
		return fmt.Errorf("staff: lock person %d's cases: %w", personID, err)
	}
	byID := make(map[int64]store.StaffSickCase, len(stored))
	for _, c := range stored {
		byID[c.ID] = c
	}
	now := s.deps.Clock()
	claimed := map[int64]bool{}
	heir := map[int64]int64{} // a case's former rows' new case
	for _, chain := range chainsOf(spans) {
		var id int64
		for _, sp := range chain.Spans {
			if sp.CaseID == nil || claimed[*sp.CaseID] {
				continue
			}
			if _, ok := byID[*sp.CaseID]; ok && (id == 0 || *sp.CaseID < id) {
				id = *sp.CaseID
			}
		}
		if id == 0 {
			c, err := txq.InsertSickCase(ctx, store.InsertSickCaseParams{PersonID: personID, FirstDay: dayParam(chain.First), LastDay: dayParam(chain.Last), Now: now})
			if err != nil {
				return fmt.Errorf("staff: open person %d's case: %w", personID, err)
			}
			id = c.ID
			if err := s.setCaseRows(ctx, txq, id, chain); err != nil {
				return err
			}
			if err := s.recordCaseEvent(ctx, txq, personID, id, eventCaseOpened, "", who, nil, map[string]json.RawMessage{
				"firstDay": dayValue(c.FirstDay), "lastDay": dayValue(c.LastDay),
			}); err != nil {
				return err
			}
		} else {
			c := byID[id]
			reopen := c.ClosedAt != nil && chain.Last.After(civil(c.LastDay))
			if reopen || !chain.First.Equal(civil(c.FirstDay)) || !chain.Last.Equal(civil(c.LastDay)) {
				if err := txq.SetSickCaseDays(ctx, store.SetSickCaseDaysParams{
					FirstDay: dayParam(chain.First), LastDay: dayParam(chain.Last), Reopen: reopen, Now: now, ID: id,
				}); err != nil {
					return fmt.Errorf("staff: store case %d's days: %w", id, err)
				}
			}
			if err := s.setCaseRows(ctx, txq, id, chain); err != nil {
				return err
			}
		}
		claimed[id] = true
		for _, sp := range chain.Spans {
			if sp.CaseID != nil && *sp.CaseID != id {
				if _, ok := heir[*sp.CaseID]; !ok {
					heir[*sp.CaseID] = id
				}
			}
		}
		if err := s.dateMilestones(ctx, txq, id, chain, now); err != nil {
			return err
		}
	}
	for _, c := range stored {
		if claimed[c.ID] {
			continue
		}
		var to *int64
		if h, ok := heir[c.ID]; ok {
			to = &h
		}
		from := c.ID
		if err := txq.MoveCaseDocuments(ctx, store.MoveCaseDocumentsParams{ToCase: to, FromCase: &from}); err != nil {
			return fmt.Errorf("staff: move case %d's documents: %w", c.ID, err)
		}
		if err := txq.DeleteCaseMilestones(ctx, c.ID); err != nil {
			return fmt.Errorf("staff: remove case %d's milestones: %w", c.ID, err)
		}
		if err := txq.DeleteSickCase(ctx, c.ID); err != nil {
			return fmt.Errorf("staff: remove case %d: %w", c.ID, err)
		}
	}
	return nil
}

// setCaseRows names case id on every row of chain that names another.
func (s *server) setCaseRows(ctx context.Context, txq *store.Queries, id int64, chain caseChain) error {
	for _, sp := range chain.Spans {
		if sp.CaseID != nil && *sp.CaseID == id {
			continue
		}
		if err := txq.SetAbsenceCase(ctx, store.SetAbsenceCaseParams{CaseID: &id, ID: sp.ID}); err != nil {
			return fmt.Errorf("staff: name case %d on absence %d: %w", id, sp.ID, err)
		}
	}
	return nil
}

// dateMilestones stores case id's milestones' dates from chain.
func (s *server) dateMilestones(ctx context.Context, txq *store.Queries, id int64, chain caseChain, now time.Time) error {
	dates := milestoneDates(chain.First)
	for _, kind := range milestoneKinds {
		var due pgtype.Date
		if kind == milestoneEmployerPeriodEnd {
			if end := employerPeriodEnd(chain.Spans); end != nil {
				due = dayParam(*end)
			}
		} else {
			due = dayParam(dates[kind])
		}
		if err := txq.SetMilestoneDue(ctx, store.SetMilestoneDueParams{CaseID: id, Kind: kind, DueDate: due, Now: now}); err != nil {
			return fmt.Errorf("staff: date case %d's %s: %w", id, kind, err)
		}
	}
	return nil
}

// ---- Responses -------------------------------------------------------------

// caseResponse is case c with its milestones as a reads it on today.
func caseResponse(c store.StaffSickCase, milestones []store.StaffSickCaseMilestone, a audience, today time.Time) gen.StaffCase {
	last := civil(c.LastDay)
	open := !caseClosed(c.ClosedAt != nil, last, today)
	out := gen.StaffCase{
		Id: c.ID, PersonId: c.PersonID, FirstDay: openapi_types.Date{Time: civil(c.FirstDay)}, LastDay: openapi_types.Date{Time: last},
		Open: open, ClosedAt: c.ClosedAt,
		MaxBenefitEstimate:       openapi_types.Date{Time: benefitEstimate(civil(c.FirstDay), civilPtr(c.MaxBenefitEstimate))},
		MaxBenefitEstimateEdited: c.MaxBenefitEstimate.Valid,
		Milestones:               []gen.StaffCaseMilestone{},
		Revision:                 c.Revision,
	}
	if a.reads(groupAccommodation) {
		out.AccommodationNote = c.AccommodationNote
	}
	for _, kind := range milestoneKinds {
		for _, m := range milestones {
			if m.CaseID != c.ID || m.Kind != kind {
				continue
			}
			due := civilPtr(m.DueDate)
			item := gen.StaffCaseMilestone{
				Kind: gen.StaffMilestoneKind(m.Kind), Due: milestoneDue(open, last, due), Waived: m.Waived,
				WaivedReason: m.WaivedReason, Revision: m.Revision, DueDate: dateOf(m.DueDate),
			}
			if m.DoneAt != nil {
				y, mo, d := m.DoneAt.UTC().Date()
				item.DoneAt = &openapi_types.Date{Time: time.Date(y, mo, d, 0, 0, 0, 0, time.UTC)}
			}
			if a.reads(groupHealth) {
				item.Note = m.Note
			}
			out.Milestones = append(out.Milestones, item)
		}
	}
	return out
}

// readCases is cases with their milestones as a reads them.
func readCases(ctx context.Context, q *store.Queries, cases []store.StaffSickCase, audienceOf func(personID int64) audience, today time.Time) ([]gen.StaffCase, error) {
	ids := make([]int64, 0, len(cases))
	for _, c := range cases {
		ids = append(ids, c.ID)
	}
	milestones, err := q.CaseMilestones(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("staff: read the cases' milestones: %w", err)
	}
	out := make([]gen.StaffCase, 0, len(cases))
	for _, c := range cases {
		out = append(out, caseResponse(c, milestones, audienceOf(c.PersonID), today))
	}
	return out, nil
}

// readsCase is whether a reads a person's cases: the readers of the absence
// detail.
func (a audience) readsCase() bool { return a.reads(groupAbsenceDetail) }

// GetStaffCases List sick-leave cases
// (GET /api/v1/staff/cases)
func (s *server) GetStaffCases(ctx context.Context, req gen.GetStaffCasesRequestObject) (gen.GetStaffCasesResponseObject, error) {
	prm := req.Params
	f := validatePageParams(prm.Page, prm.PageSize)
	status, msg := parseEnum("A status", (*string)(prm.Status), []string{"open", "closed", "all"})
	f.add("status", msg)
	if f.failed() {
		return gen.GetStaffCases400ApplicationProblemPlusJSONResponse(invalid(invalidListTitle, f)), nil
	}
	if status == nil {
		open := "open"
		status = &open
	}
	page, pageSize := pageParams(prm.Page, prm.PageSize)
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	var out gen.PaginatedResponseOfStaffCase
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		everyone := caller.Approve || caller.Manage || caller.PersonalData
		var reports, people []int64
		if !everyone && caller.Person != nil {
			g, err := loadManagerGraph(ctx, q, nil)
			if err != nil {
				return err
			}
			reports = g.reportsOf(*caller.Person, caller.Today)
			people = append(slices.Clone(reports), *caller.Person)
		}
		total, err := q.CountCases(ctx, store.CountCasesParams{PersonID: prm.PersonId, Everyone: everyone, PersonIds: people, Status: *status, Today: dayParam(caller.Today)})
		if err != nil {
			return fmt.Errorf("staff: count the cases: %w", err)
		}
		rows, err := q.ListCases(ctx, store.ListCasesParams{
			PersonID: prm.PersonId, Everyone: everyone, PersonIds: people, Status: *status, Today: dayParam(caller.Today),
			PageOffset: (page - 1) * pageSize, PageSize: pageSize,
		})
		if err != nil {
			return fmt.Errorf("staff: list the cases: %w", err)
		}
		data, err := readCases(ctx, q, rows, func(personID int64) audience {
			return caller.audienceOn(personID, slices.Contains(reports, personID), false)
		}, caller.Today)
		if err != nil {
			return err
		}
		out = gen.PaginatedResponseOfStaffCase{Data: data, Pagination: apicommon.Pagination(page, pageSize, int32(total))}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return gen.GetStaffCases200JSONResponse(out), nil
}

// caseFor reads case id and the caller's audience on its person; notFound
// when there is none or the caller does not read it.
func caseFor(ctx context.Context, q *store.Queries, caller *callerContext, id int64) (store.StaffSickCase, audience, error) {
	c, err := q.GetSickCase(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, audience{}, notFound()
	} else if err != nil {
		return c, audience{}, fmt.Errorf("staff: read case %d: %w", id, err)
	}
	a, err := personAudience(ctx, q, caller, c.PersonID)
	if err != nil {
		return c, a, err
	}
	if !a.readsCase() {
		return c, a, notFound()
	}
	return c, a, nil
}

// GetStaffCasesById Get a sick-leave case
// (GET /api/v1/staff/cases/{id})
func (s *server) GetStaffCasesById(ctx context.Context, req gen.GetStaffCasesByIdRequestObject) (gen.GetStaffCasesByIdResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	var out gen.StaffCase
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		c, a, err := caseFor(ctx, q, caller, req.Id)
		if err != nil {
			return err
		}
		cases, err := readCases(ctx, q, []store.StaffSickCase{c}, func(int64) audience { return a }, caller.Today)
		if err != nil {
			return err
		}
		out = cases[0]
		return nil
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffCasesById404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetStaffCasesById200JSONResponse(out), nil
}

// The writes of a case, each with its writers.
const (
	caseWriteNotes     = iota // the accommodation note: the manager and HR
	caseWriteMilestone        // done and waived: the manager, the Approver and HR
	caseWriteHR               // the close: staff:manage (the router holds it)
	caseWriteNote             // a milestone's note: staff:personal-data (the router holds it)
)

// mayWrite is whether a writes a case by write.
func (a audience) mayWrite(write int) bool {
	switch write {
	case caseWriteNotes:
		return a.Manager || a.Manage || a.PersonalData
	case caseWriteMilestone:
		return a.Manager || a.Approver || a.Manage || a.PersonalData
	case caseWriteHR:
		return a.Manage
	case caseWriteNote:
		return a.PersonalData
	}
	return false
}

// writeCase judges the caller's authority on case id for write before
// anything else, then runs fn under the person's lock on the case as locked
// there, and answers the case after it as the caller reads it.
func (s *server) writeCase(ctx context.Context, caller *callerContext, id int64, write int,
	fn func(ctx context.Context, txq *store.Queries, c store.StaffSickCase, a audience, who actor) error,
) (gen.StaffCase, error) {
	var out gen.StaffCase
	c, a, err := caseFor(ctx, store.New(s.deps.Pool), caller, id)
	if err != nil {
		return out, err
	}
	if !a.mayWrite(write) {
		return out, notFound()
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return out, err
	}
	err = s.withPersonLock(ctx, c.PersonID, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		locked, err := txq.LockSickCase(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && locked.PersonID != c.PersonID) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("staff: lock case %d: %w", id, err)
		}
		if err := fn(ctx, txq, locked, a, who); err != nil {
			return err
		}
		after, err := txq.GetSickCase(ctx, id)
		if err != nil {
			return fmt.Errorf("staff: read case %d: %w", id, err)
		}
		cases, err := readCases(ctx, txq, []store.StaffSickCase{after}, func(int64) audience { return a }, caller.Today)
		if err != nil {
			return err
		}
		out = cases[0]
		return nil
	})
	if errors.Is(err, errPersonNotFound) {
		return out, notFound()
	}
	return out, err
}

// caseRefusal is a case write's error as a status and a problem: 0 with no
// error, a bare 404, or the refusal's.
func caseRefusal(err error) (int, gen.StaffProblem, error) {
	if err == nil {
		return 0, gen.StaffProblem{}, nil
	}
	if r, ok := asRefusal(err); ok {
		return r.status, r.problem, nil
	}
	return 0, gen.StaffProblem{}, err
}

// noteText is an optional note: trimmed, empty is none, at most limit
// characters.
func noteText(raw *string, limit int, field string, f *fieldErrors) *string {
	if raw == nil {
		return nil
	}
	v := strings.TrimSpace(*raw)
	if v == "" {
		return nil
	}
	if utf8.RuneCountInString(v) > limit {
		f.add(field, fmt.Sprintf("At most %d characters", limit))
	}
	return &v
}

// PutStaffCasesById Change a sick-leave case
// (PUT /api/v1/staff/cases/{id})
func (s *server) PutStaffCasesById(ctx context.Context, req gen.PutStaffCasesByIdRequestObject) (gen.PutStaffCasesByIdResponseObject, error) {
	var body gen.StaffCaseChangeRequest
	if req.Body != nil {
		body = *req.Body
	}
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutStaffCasesById400ApplicationProblemPlusJSONResponse(invalid(invalidCaseTitle, badIfMatch())), nil
	}
	if f := s.unknownProperties(ctx, "StaffCaseChangeRequest"); f.failed() {
		return gen.PutStaffCasesById400ApplicationProblemPlusJSONResponse(invalid(invalidCaseTitle, f)), nil
	}
	var f fieldErrors
	note := noteText(body.AccommodationNote, accommodationNoteMax, "accommodationNote", &f)
	if f.failed() {
		return gen.PutStaffCasesById400ApplicationProblemPlusJSONResponse(invalid(invalidCaseTitle, f)), nil
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	out, err := s.writeCase(ctx, caller, req.Id, caseWriteNotes, func(ctx context.Context, txq *store.Queries, c store.StaffSickCase, a audience, who actor) error {
		if err := checkRevision("case", c.Revision, revision); err != nil {
			return err
		}
		estimate := c.MaxBenefitEstimate
		switch {
		case a.Manage:
			estimate = pgtype.Date{}
			if body.MaxBenefitEstimate != nil {
				estimate = dayParam(body.MaxBenefitEstimate.Time)
			}
		case body.MaxBenefitEstimate != nil:
			return refuseInvalid(invalidCaseTitle, fieldErrors{errs: fieldError("maxBenefitEstimate", "Only HR sets the 52-week estimate")})
		}
		saved, err := txq.UpdateCaseNotes(ctx, store.UpdateCaseNotesParams{AccommodationNote: note, MaxBenefitEstimate: estimate, Now: s.deps.Clock(), ID: c.ID})
		if err != nil {
			return fmt.Errorf("staff: change case %d: %w", c.ID, err)
		}
		values := func(c store.StaffSickCase) map[string]json.RawMessage {
			out := map[string]json.RawMessage{"maxBenefitEstimate": dayValue(c.MaxBenefitEstimate)}
			if c.AccommodationNote != nil {
				out["accommodationNote"] = caseValue(*c.AccommodationNote)
			}
			return out
		}
		return s.recordCaseEvent(ctx, txq, c.PersonID, c.ID, eventCaseUpdated, "", who, values(c), values(saved))
	})
	status, problem, err := caseRefusal(err)
	switch {
	case err != nil:
		return nil, err
	case status == http.StatusBadRequest:
		return gen.PutStaffCasesById400ApplicationProblemPlusJSONResponse(problem), nil
	case status == http.StatusConflict:
		return gen.PutStaffCasesById409ApplicationProblemPlusJSONResponse(problem), nil
	case status != 0:
		return gen.PutStaffCasesById404Response{}, nil
	}
	return gen.PutStaffCasesById200JSONResponse(out), nil
}

// PostStaffCasesByIdClose Close a sick-leave case
// (POST /api/v1/staff/cases/{id}/close)
func (s *server) PostStaffCasesByIdClose(ctx context.Context, req gen.PostStaffCasesByIdCloseRequestObject) (gen.PostStaffCasesByIdCloseResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	out, err := s.writeCase(ctx, caller, req.Id, caseWriteHR, func(ctx context.Context, txq *store.Queries, c store.StaffSickCase, _ audience, who actor) error {
		if c.ClosedAt != nil {
			return nil
		}
		saved, err := txq.CloseSickCase(ctx, store.CloseSickCaseParams{Now: s.deps.Clock(), ID: c.ID})
		if err != nil {
			return fmt.Errorf("staff: close case %d: %w", c.ID, err)
		}
		return s.recordCaseEvent(ctx, txq, c.PersonID, c.ID, eventCaseClosed, "", who, nil,
			map[string]json.RawMessage{"closedAt": caseValue(saved.ClosedAt)})
	})
	status, problem, err := caseRefusal(err)
	switch {
	case err != nil:
		return nil, err
	case status == http.StatusConflict:
		return gen.PostStaffCasesByIdClose409ApplicationProblemPlusJSONResponse(problem), nil
	case status != 0:
		return gen.PostStaffCasesByIdClose404Response{}, nil
	}
	return gen.PostStaffCasesByIdClose200JSONResponse(out), nil
}

// milestoneValues is a milestone's facts as its events name them.
func milestoneValues(m store.StaffSickCaseMilestone) map[string]json.RawMessage {
	out := map[string]json.RawMessage{"waived": caseValue(m.Waived)}
	if m.DoneAt != nil {
		out["doneAt"] = caseValue(m.DoneAt.UTC().Format(time.DateOnly))
	}
	if m.WaivedReason != nil {
		out["waivedReason"] = caseValue(*m.WaivedReason)
	}
	if m.Note != nil {
		out["note"] = caseValue(*m.Note)
	}
	return out
}

// lockMilestone takes case c's milestone of kind, notFound when it has none.
func lockMilestone(ctx context.Context, txq *store.Queries, c store.StaffSickCase, kind string) (store.StaffSickCaseMilestone, error) {
	m, err := txq.LockMilestone(ctx, store.LockMilestoneParams{CaseID: c.ID, Kind: kind})
	if errors.Is(err, pgx.ErrNoRows) {
		return m, notFound()
	} else if err != nil {
		return m, fmt.Errorf("staff: lock case %d's %s: %w", c.ID, kind, err)
	}
	return m, nil
}

// PostStaffCasesByIdMilestonesByKindDone Mark a milestone done
// (POST /api/v1/staff/cases/{id}/milestones/{kind}/done)
func (s *server) PostStaffCasesByIdMilestonesByKindDone(ctx context.Context, req gen.PostStaffCasesByIdMilestonesByKindDoneRequestObject) (gen.PostStaffCasesByIdMilestonesByKindDoneResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	doneOn := caller.Today
	if req.Body != nil && req.Body.DoneAt != nil {
		doneOn = req.Body.DoneAt.Time
	}
	if doneOn.After(caller.Today) {
		return gen.PostStaffCasesByIdMilestonesByKindDone400ApplicationProblemPlusJSONResponse(invalid(invalidCaseTitle,
			fieldErrors{errs: fieldError("doneAt", "A milestone is done on a day that has come")})), nil
	}
	kind := string(req.Kind)
	out, err := s.writeCase(ctx, caller, req.Id, caseWriteMilestone, func(ctx context.Context, txq *store.Queries, c store.StaffSickCase, _ audience, who actor) error {
		before, err := lockMilestone(ctx, txq, c, kind)
		if err != nil {
			return err
		}
		userID := caller.UserID
		after, err := txq.MarkMilestoneDone(ctx, store.MarkMilestoneDoneParams{DoneAt: doneOn, ByUserID: &userID, Now: s.deps.Clock(), CaseID: c.ID, Kind: kind})
		if err != nil {
			return fmt.Errorf("staff: mark case %d's %s done: %w", c.ID, kind, err)
		}
		return s.recordCaseEvent(ctx, txq, c.PersonID, c.ID, eventMilestoneDone, kind, who, milestoneValues(before), milestoneValues(after))
	})
	status, problem, err := caseRefusal(err)
	switch {
	case err != nil:
		return nil, err
	case status == http.StatusConflict:
		return gen.PostStaffCasesByIdMilestonesByKindDone409ApplicationProblemPlusJSONResponse(problem), nil
	case status != 0:
		return gen.PostStaffCasesByIdMilestonesByKindDone404Response{}, nil
	}
	return gen.PostStaffCasesByIdMilestonesByKindDone200JSONResponse(out), nil
}

// PostStaffCasesByIdMilestonesByKindWaive Waive a milestone
// (POST /api/v1/staff/cases/{id}/milestones/{kind}/waive)
func (s *server) PostStaffCasesByIdMilestonesByKindWaive(ctx context.Context, req gen.PostStaffCasesByIdMilestonesByKindWaiveRequestObject) (gen.PostStaffCasesByIdMilestonesByKindWaiveResponseObject, error) {
	var body gen.StaffMilestoneWaiveRequest
	if req.Body != nil {
		body = *req.Body
	}
	var f fieldErrors
	reason := noteText(&body.Reason, waiveReasonMax, "reason", &f)
	if reason == nil {
		f.add("reason", "A waiver needs its reason")
	}
	if f.failed() {
		return gen.PostStaffCasesByIdMilestonesByKindWaive400ApplicationProblemPlusJSONResponse(invalid(invalidCaseTitle, f)), nil
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	kind := string(req.Kind)
	out, err := s.writeCase(ctx, caller, req.Id, caseWriteMilestone, func(ctx context.Context, txq *store.Queries, c store.StaffSickCase, _ audience, who actor) error {
		before, err := lockMilestone(ctx, txq, c, kind)
		if err != nil {
			return err
		}
		userID := caller.UserID
		after, err := txq.WaiveMilestone(ctx, store.WaiveMilestoneParams{Reason: reason, ByUserID: &userID, Now: s.deps.Clock(), CaseID: c.ID, Kind: kind})
		if err != nil {
			return fmt.Errorf("staff: waive case %d's %s: %w", c.ID, kind, err)
		}
		return s.recordCaseEvent(ctx, txq, c.PersonID, c.ID, eventMilestoneWaived, kind, who, milestoneValues(before), milestoneValues(after))
	})
	status, problem, err := caseRefusal(err)
	switch {
	case err != nil:
		return nil, err
	case status == http.StatusConflict:
		return gen.PostStaffCasesByIdMilestonesByKindWaive409ApplicationProblemPlusJSONResponse(problem), nil
	case status != 0:
		return gen.PostStaffCasesByIdMilestonesByKindWaive404Response{}, nil
	}
	return gen.PostStaffCasesByIdMilestonesByKindWaive200JSONResponse(out), nil
}

// PutStaffCasesByIdMilestonesByKindNote Write a milestone's note
// (PUT /api/v1/staff/cases/{id}/milestones/{kind}/note)
func (s *server) PutStaffCasesByIdMilestonesByKindNote(ctx context.Context, req gen.PutStaffCasesByIdMilestonesByKindNoteRequestObject) (gen.PutStaffCasesByIdMilestonesByKindNoteResponseObject, error) {
	var body gen.StaffMilestoneNoteRequest
	if req.Body != nil {
		body = *req.Body
	}
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutStaffCasesByIdMilestonesByKindNote400ApplicationProblemPlusJSONResponse(invalid(invalidCaseTitle, badIfMatch())), nil
	}
	var f fieldErrors
	note := noteText(body.Note, milestoneNoteMax, "note", &f)
	if f.failed() {
		return gen.PutStaffCasesByIdMilestonesByKindNote400ApplicationProblemPlusJSONResponse(invalid(invalidCaseTitle, f)), nil
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	kind := string(req.Kind)
	out, err := s.writeCase(ctx, caller, req.Id, caseWriteNote, func(ctx context.Context, txq *store.Queries, c store.StaffSickCase, _ audience, who actor) error {
		before, err := lockMilestone(ctx, txq, c, kind)
		if err != nil {
			return err
		}
		if err := checkRevision("milestone", before.Revision, revision); err != nil {
			return err
		}
		after, err := txq.SetMilestoneNote(ctx, store.SetMilestoneNoteParams{Note: note, Now: s.deps.Clock(), CaseID: c.ID, Kind: kind})
		if err != nil {
			return fmt.Errorf("staff: write case %d's %s note: %w", c.ID, kind, err)
		}
		return s.recordCaseEvent(ctx, txq, c.PersonID, c.ID, eventMilestoneNoteUpdated, kind, who, milestoneValues(before), milestoneValues(after))
	})
	status, problem, err := caseRefusal(err)
	switch {
	case err != nil:
		return nil, err
	case status == http.StatusConflict:
		return gen.PutStaffCasesByIdMilestonesByKindNote409ApplicationProblemPlusJSONResponse(problem), nil
	case status != 0:
		return gen.PutStaffCasesByIdMilestonesByKindNote404Response{}, nil
	}
	return gen.PutStaffCasesByIdMilestonesByKindNote200JSONResponse(out), nil
}
