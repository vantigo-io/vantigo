package staff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is what sickness adds to an absence's write (design S9, S10,
// §4 Egenmelding, §9, plan Task 5) and egenmelding's standing and
// withdrawal. Under the person's lock, before the row is written, an
// egenmelding is judged against the policy (self_certification.go), the
// withdrawal and the months of employment, and against its case's sixteenth
// day; after a sick row is written, a sykmelding trims the egenmelding it
// lies over, and the person's cases are derived again from their sick rows
// (cases.go). Approved ferie under a sykmelding stays as it is until HR
// records the employee's demand to postpone it (ferieloven § 9, Task 5
// review H1: postponeHoliday); meanwhile HR's attention list names it. Ending or fixing a registered sick
// row is HR's job (plan pre-flight M8): the person asks.

// selfCertRefusal is the 409 of an egenmelding refused, with its reason and
// what to do instead.
func selfCertRefusal(code, title, reason string) error {
	return refuseConflict(conflict(code, title, reason+" Register a sykmelding from a doctor for these days instead."))
}

// personEgenmelding is the person's registered sick rows but exclude, as
// spans, and their egenmelding rows as days.
func personSickSpans(ctx context.Context, txq *store.Queries, personID, exclude int64) ([]sickSpan, []daySpan, error) {
	rows, err := txq.PersonSickRows(ctx, personID)
	if err != nil {
		return nil, nil, fmt.Errorf("staff: read person %d's sick rows: %w", personID, err)
	}
	var spans []sickSpan
	var self []daySpan
	for _, r := range rows {
		if r.ID == exclude {
			continue
		}
		spans = append(spans, sickSpan{First: civil(r.FirstDay), Last: civil(r.LastDay), ID: r.ID, Kind: r.Kind, CaseID: r.CaseID})
		if r.Kind == kindSickSelf {
			self = append(self, daySpan{First: civil(r.FirstDay), Last: civil(r.LastDay)})
		}
	}
	return spans, self, nil
}

// selfCertFrom is the first day the months of employment allow egenmelding
// on day, judged on the employment in force that day: its start plus the
// policy's months; nil when no employment is in force on it.
func selfCertFrom(employments []store.StaffEmployment, day time.Time, months int) *time.Time {
	e, ok := employmentOn(employments, day)
	if !ok {
		return nil
	}
	from := addMonths(civil(e.StartDate), months)
	return &from
}

// employmentOn is the employment in force on day — the earliest started of
// several — else, when none is, the latest started.
func employmentOn(employments []store.StaffEmployment, day time.Time) (store.StaffEmployment, bool) {
	var on, latest *store.StaffEmployment
	for i := range employments {
		e := &employments[i]
		start := civil(e.StartDate)
		if !start.After(day) && (!e.EndDate.Valid || !e.EndDate.Time.Before(day)) && (on == nil || start.Before(civil(on.StartDate))) {
			on = e
		}
		if latest == nil || start.After(civil(latest.StartDate)) {
			latest = e
		}
	}
	switch {
	case on != nil:
		return *on, true
	case latest != nil:
		return *latest, true
	}
	return store.StaffEmployment{}, false
}

// judgeSelfCertification judges an egenmelding p for personID under the
// person's lock, exclude being the row it changes: the withdrawal, the
// months of employment, the policy's counters, and the case's sixteenth day.
func judgeSelfCertification(ctx context.Context, txq *store.Queries, settings store.StaffSetting, personID, exclude int64, p parsedAbsence) error {
	w, err := txq.PersonSelfCertWithdrawal(ctx, personID)
	if err != nil {
		return fmt.Errorf("staff: read person %d's withdrawal: %w", personID, err)
	}
	// From the day the withdrawal was decided (review L1) until its last day.
	if w.SelfCertWithdrawnUntil.Valid && !w.SelfCertWithdrawnUntil.Time.Before(p.First) &&
		(!w.SelfCertWithdrawnFrom.Valid || !p.First.Before(w.SelfCertWithdrawnFrom.Time)) {
		return selfCertRefusal(codeSelfCertWithdrawn, "Egenmelding withdrawn",
			fmt.Sprintf("HR has withdrawn the right to egenmelding until %s.", w.SelfCertWithdrawnUntil.Time.Format(time.DateOnly)))
	}
	policy := policyOf(settings)
	employments, err := txq.ListPersonEmployments(ctx, personID)
	if err != nil {
		return fmt.Errorf("staff: read person %d's employments: %w", personID, err)
	}
	if from := selfCertFrom(employments, p.First, int(policy.AfterMonths)); from == nil || from.After(p.First) {
		reason := "Egenmelding is allowed once the employment has lasted the months the policy asks."
		if from != nil {
			reason = fmt.Sprintf("Egenmelding is allowed from %s, after %d months of employment.", from.Format(time.DateOnly), policy.AfterMonths)
		}
		return selfCertRefusal(codeSelfCertTooEarly, "Too early for egenmelding", reason)
	}
	spans, self, err := personSickSpans(ctx, txq, personID, exclude)
	if err != nil {
		return err
	}
	limits := limitsOf(policy)
	switch judgeSelfCert(limits, self, daySpan{First: p.First, Last: p.Last}, sykmeldingDays(spans)) {
	case selfCertOverOccurrence:
		return selfCertRefusal(codeSelfCertExhausted, "Egenmelding used up",
			fmt.Sprintf("One egenmelding is at most %d calendar days, days off between included.", limits.DaysPerOccurrence))
	case selfCertOverOccurrences:
		return selfCertRefusal(codeSelfCertExhausted, "Egenmelding used up",
			fmt.Sprintf("Egenmelding has been used %d times in the last twelve months.", limits.OccurrencesPerYear))
	case selfCertOverDays:
		return selfCertRefusal(codeSelfCertExhausted, "Egenmelding used up",
			fmt.Sprintf("Egenmelding is at most %d days in twelve months.", limits.DaysPerYear))
	}
	candidate := sickSpan{First: p.First, Last: p.Last, Kind: kindSickSelf}
	for _, c := range chainsOf(append(spans, candidate)) {
		if c.First.After(p.First) || c.Last.Before(p.Last) {
			continue
		}
		if end := employerPeriodEnd(c.Spans); end != nil && p.Last.After(*end) {
			return selfCertRefusal(codeSelfCertExhausted, "Past the employer period",
				fmt.Sprintf("The sick leave's sixteenth day was %s: egenmelding does not reach past it.", end.Format(time.DateOnly)))
		}
	}
	return nil
}

// judgeSickness is the rules a sick row adds under the person's lock: an
// egenmelding's (judgeSelfCertification); nothing for any other type.
func judgeSickness(ctx context.Context, txq *store.Queries, settings store.StaffSetting, personID, exclude int64, p parsedAbsence, t store.StaffAbsenceType) error {
	if t.Kind != kindSickSelf {
		return nil
	}
	return judgeSelfCertification(ctx, txq, settings, personID, exclude, p)
}

// sykmeldingDays is the days of spans' sykmeldinger, which join the
// egenmelding on either side into one occurrence (review M4).
func sykmeldingDays(spans []sickSpan) []daySpan {
	var out []daySpan
	for _, sp := range spans {
		if sp.Kind == kindSickCertified {
			out = append(out, daySpan{First: sp.First, Last: sp.Last})
		}
	}
	return out
}

// afterSickWrite is what a write of a sick row — or of a row that was one —
// does next under the person's lock: a sykmelding trims the egenmelding it
// lies over, then the person's cases are derived again. row is the sick row
// as written, nil after a deletion.
func (s *server) afterSickWrite(ctx context.Context, txq *store.Queries, who actor, personID int64, row *store.StaffAbsence, t store.StaffAbsenceType) error {
	if row != nil && t.Kind == kindSickCertified {
		if err := s.trimUnder(ctx, txq, who, personID, *row); err != nil {
			return err
		}
	}
	return s.reconcileCases(ctx, txq, who, personID)
}

// trimUnder trims every egenmelding sykmelding sick lies over — removed when
// wholly covered (reading 24).
func (s *server) trimUnder(ctx context.Context, txq *store.Queries, who actor, personID int64, sick store.StaffAbsence) error {
	rows, err := txq.OverlappingAbsences(ctx, store.OverlappingAbsencesParams{
		PersonID: personID, Exclude: sick.ID, FirstDay: sick.FirstDay, LastDay: sick.LastDay,
	})
	if err != nil {
		return fmt.Errorf("staff: read person %d's absences: %w", personID, err)
	}
	for _, r := range rows {
		if r.Kind != kindSickSelf {
			continue
		}
		t, err := txq.GetAbsenceType(ctx, r.StaffAbsence.TypeID)
		if err != nil {
			return fmt.Errorf("staff: read absence type %d: %w", r.StaffAbsence.TypeID, err)
		}
		if _, err := s.carve(ctx, txq, who, personID, r.StaffAbsence, t, civil(sick.FirstDay), civil(sick.LastDay), nil); err != nil {
			return err
		}
	}
	return nil
}

// carve takes first..last out of row: the part before stays on the row, the
// part after becomes a new row with the row's decision, and a row with
// neither is cancelled (ferie) or removed (an egenmelding). One
// absence_split event says so, by the row's own type, with added's fields.
// It answers the rows of row's left after it.
func (s *server) carve(ctx context.Context, txq *store.Queries, who actor, personID int64, row store.StaffAbsence, t store.StaffAbsenceType, first, last time.Time, added map[string]json.RawMessage) ([]store.StaffAbsence, error) {
	rowFirst, rowLast := civil(row.FirstDay), civil(row.LastDay)
	before, after := rowFirst.Before(first), rowLast.After(last)
	now := s.deps.Clock()
	var extra map[string]any
	var left []store.StaffAbsence
	if after {
		start := last.AddDate(0, 0, 1)
		if !before {
			moved, err := txq.SetAbsenceDays(ctx, store.SetAbsenceDaysParams{FirstDay: dayParam(start), LastDay: row.LastDay, Now: now, ID: row.ID})
			if err != nil {
				return nil, fmt.Errorf("staff: trim absence %d: %w", row.ID, err)
			}
			return []store.StaffAbsence{moved}, s.recordAbsenceWith(ctx, txq, personID, eventAbsenceSplit, who, row.ID, &row, &moved, nil, added, t)
		}
		piece, err := txq.InsertSplitAbsence(ctx, store.InsertSplitAbsenceParams{FirstDay: dayParam(start), Now: now, Source: row.ID})
		if err != nil {
			return nil, fmt.Errorf("staff: split absence %d: %w", row.ID, err)
		}
		extra = map[string]any{"splitAbsenceId": piece.ID}
		left = append(left, piece)
	}
	if before {
		cut, err := txq.SetAbsenceDays(ctx, store.SetAbsenceDaysParams{FirstDay: row.FirstDay, LastDay: dayParam(first.AddDate(0, 0, -1)), Now: now, ID: row.ID})
		if err != nil {
			return nil, fmt.Errorf("staff: trim absence %d: %w", row.ID, err)
		}
		return append([]store.StaffAbsence{cut}, left...), s.recordAbsenceWith(ctx, txq, personID, eventAbsenceSplit, who, row.ID, &row, &cut, extra, added, t)
	}
	if t.Kind == kindSickSelf {
		if err := txq.DeleteAbsence(ctx, row.ID); err != nil {
			return nil, fmt.Errorf("staff: delete absence %d: %w", row.ID, err)
		}
		return nil, s.recordAbsenceWith(ctx, txq, personID, eventAbsenceSplit, who, row.ID, &row, nil, nil, added, t)
	}
	cancelled, err := txq.SetAbsenceStatus(ctx, store.SetAbsenceStatusParams{ID: row.ID, Status: absenceCancelled, Now: now})
	if err != nil {
		return nil, fmt.Errorf("staff: cancel absence %d: %w", row.ID, err)
	}
	return []store.StaffAbsence{cancelled}, s.recordAbsenceWith(ctx, txq, personID, eventAbsenceSplit, who, row.ID, &row, &cancelled, nil, added, t)
}

// ---- Postponing holiday for sickness ---------------------------------------

// The refusal codes of a demand to postpone holiday (ferieloven § 9).
const (
	codePostponementNotApplicable = "holiday_postponement_not_applicable"
	codePostponementTooLate       = "holiday_postponement_too_late"
)

// The paragraphs a postponement falls under: sickness before the holiday
// (§ 9 (1)) and during it (§ 9 (2)).
const (
	postponedBefore = "9 (1)"
	postponedDuring = "9 (2)"
)

// lastWorkingDayBefore is the last working day before d.
func lastWorkingDayBefore(d time.Time) time.Time {
	day := d.AddDate(0, 0, -1)
	for !worksOn(day) {
		day = day.AddDate(0, 0, -1)
	}
	return day
}

// postponementRefusal is the 409 of a demand that cannot postpone holiday.
func postponementRefusal(code, title, detail string) error {
	return refuseConflict(conflict(code, title, detail))
}

// PostStaffAbsencesByIdPostponeHoliday Postpone holiday for sickness
// (POST /api/v1/staff/absences/{id}/postpone-holiday)
//
// staff:manage records the employee's demand, made with a legeerklæring, to
// postpone the approved ferie a 100 % sykmelding lies over (ferieloven § 9):
// sickness before the holiday must have been demanded by the last working
// day before it (§ 9 (1)), sickness during it gives replacement days (§ 9
// (2)). Each ferie row is split around the sykmelding (carve) under the
// person's lock, its absence_split event carrying the demand. Today's § 9
// sets no least number of sick days, for either paragraph (Lovdata,
// checked 2026-10-14); § 9 (2)'s demand is to come "uten ugrunnet opphold"
// after the work is resumed, which HR judges and the module does not.
func (s *server) PostStaffAbsencesByIdPostponeHoliday(ctx context.Context, req gen.PostStaffAbsencesByIdPostponeHolidayRequestObject) (gen.PostStaffAbsencesByIdPostponeHolidayResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	demanded := caller.Today
	if req.Body != nil && req.Body.DemandedOn != nil {
		demanded = req.Body.DemandedOn.Time
	}
	if demanded.After(caller.Today) {
		return gen.PostStaffAbsencesByIdPostponeHoliday400ApplicationProblemPlusJSONResponse(invalid(invalidAbsenceTitle,
			fieldErrors{errs: fieldError("demandedOn", "A demand is made on a day that has come")})), nil
	}
	current, _, err := readAbsenceFor(ctx, store.New(s.deps.Pool), caller, req.Id)
	if _, ok := asRefusal(err); ok {
		return gen.PostStaffAbsencesByIdPostponeHoliday404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	personID := current.StaffAbsence.PersonID
	var out gen.StaffHolidayPostponement
	err = s.withPersonLock(ctx, personID, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		sick, t, err := lockAbsence(ctx, txq, req.Id, personID, hrOnlyType(current.Visibility))
		if err != nil {
			return err
		}
		if t.Kind != kindSickCertified || sick.Status != absenceRegistered || shareOf(t.Kind, sick.Percent, sick.DegreePercent) != wholeDay {
			return postponementRefusal(codePostponementNotApplicable, "Nothing to postpone",
				"Holiday is postponed for a 100 % sykmelding only: the employee is then wholly unable to work (ferieloven § 9).")
		}
		rows, err := txq.OverlappingAbsences(ctx, store.OverlappingAbsencesParams{
			PersonID: personID, Exclude: sick.ID, FirstDay: sick.FirstDay, LastDay: sick.LastDay,
		})
		if err != nil {
			return fmt.Errorf("staff: read person %d's absences: %w", personID, err)
		}
		sickFirst := civil(sick.FirstDay)
		var ferie []store.StaffAbsence
		for _, r := range rows {
			if r.Kind != kindHoliday || r.StaffAbsence.Status != absenceApproved {
				continue
			}
			// A demand follows the sickness (re-check 1): not before the
			// sykmelding's first day — under § 9 (1) the day it began before
			// the holiday, under § 9 (2) its first day within the holiday.
			if demanded.Before(sickFirst) {
				return refuseInvalid(invalidAbsenceTitle, fieldErrors{errs: fieldError("demandedOn",
					fmt.Sprintf("A demand follows the sickness: not before %s", sickFirst.Format(time.DateOnly)))})
			}
			if f := civil(r.StaffAbsence.FirstDay); sickFirst.Before(f) && demanded.After(lastWorkingDayBefore(f)) {
				return postponementRefusal(codePostponementTooLate, "Demanded too late",
					fmt.Sprintf("Sickness that began before the holiday is to be demanded by the last working day before it, %s (ferieloven § 9 (1)).",
						lastWorkingDayBefore(f).Format(time.DateOnly)))
			}
			ferie = append(ferie, r.StaffAbsence)
		}
		if len(ferie) == 0 {
			return postponementRefusal(codePostponementNotApplicable, "Nothing to postpone", "This sykmelding lies over no approved ferie.")
		}
		out = gen.StaffHolidayPostponement{DemandedOn: openapi_types.Date{Time: demanded}, Absences: []gen.StaffAbsence{}}
		a, err := personAudience(ctx, txq, caller, personID)
		if err != nil {
			return err
		}
		for _, f := range ferie {
			ft, err := txq.ShareAbsenceType(ctx, f.TypeID)
			if err != nil {
				return fmt.Errorf("staff: read absence type %d: %w", f.TypeID, err)
			}
			under := postponedDuring
			if sickFirst.Before(civil(f.FirstDay)) {
				under = postponedBefore
			}
			left, err := s.carve(ctx, txq, who, personID, f, ft, sickFirst, civil(sick.LastDay), map[string]json.RawMessage{
				fieldDemandedOn: caseValue(demanded.Format(time.DateOnly)), fieldPostponedUnder: caseValue(under),
			})
			if err != nil {
				return err
			}
			for _, r := range left {
				out.Absences = append(out.Absences, absenceResponse(r, ft.Kind, ft.Visibility, ft.Unit, a, s.absenceTag))
			}
		}
		return nil
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.PostStaffAbsencesByIdPostponeHoliday404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusConflict:
			return gen.PostStaffAbsencesByIdPostponeHoliday409ApplicationProblemPlusJSONResponse(r.problem), nil
		case http.StatusBadRequest:
			return gen.PostStaffAbsencesByIdPostponeHoliday400ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.PostStaffAbsencesByIdPostponeHoliday404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PostStaffAbsencesByIdPostponeHoliday200JSONResponse(out), nil
}

// ---- The standing and the withdrawal ---------------------------------------

// The bounds of a withdrawal (plan "Exact values"): until after today and at
// most a year ahead; the reason 1 to 500 characters.
const withdrawalReasonMax = 500

// invalidWithdrawalTitle is the title of a withdrawal's 400.
const invalidWithdrawalTitle = "Invalid withdrawal"

// selfCertResponse is personID's standing today as a reads it: the policy's
// limits by its name, the rolling year's use, the day the months of
// employment allow it from, and — to the care-day fields' readers — the
// withdrawal.
func selfCertResponse(ctx context.Context, q *store.Queries, settings store.StaffSetting, personID int64, today time.Time, a audience) (gen.StaffSelfCertification, error) {
	policy := policyOf(settings)
	limits := limitsOf(policy)
	spans, self, err := personSickSpans(ctx, q, personID, 0)
	if err != nil {
		return gen.StaffSelfCertification{}, err
	}
	occurrences, days := selfCertUsed(self, sykmeldingDays(spans), today)
	out := gen.StaffSelfCertification{
		PersonId: personID, Policy: gen.StaffSelfCertPolicy(policy.Policy), DaysPerOccurrence: int32(limits.DaysPerOccurrence),
		OccurrencesUsed: int32(occurrences), DaysUsed: int32(days),
	}
	if limits.JudgeOccurrences {
		n := int32(limits.OccurrencesPerYear)
		out.OccurrencesPerYear = &n
	}
	if limits.JudgeDays {
		n := int32(limits.DaysPerYear)
		out.DaysPerYear = &n
	}
	employments, err := q.ListPersonEmployments(ctx, personID)
	if err != nil {
		return out, fmt.Errorf("staff: read person %d's employments: %w", personID, err)
	}
	if from := selfCertFrom(employments, today, int(policy.AfterMonths)); from != nil {
		out.EligibleFrom = &openapi_types.Date{Time: *from}
	}
	if a.reads(groupCareDays) {
		w, err := q.PersonSelfCertWithdrawal(ctx, personID)
		if err != nil {
			return out, fmt.Errorf("staff: read person %d's withdrawal: %w", personID, err)
		}
		if w.SelfCertWithdrawnUntil.Valid {
			out.WithdrawnUntil = &openapi_types.Date{Time: w.SelfCertWithdrawnUntil.Time}
			out.WithdrawnFrom = dateOf(w.SelfCertWithdrawnFrom)
			out.WithdrawnReason = w.SelfCertWithdrawnReason
		}
	}
	return out, nil
}

// GetStaffPeopleByIdSelfCertification Get a person's egenmelding standing
// (GET /api/v1/staff/people/{id}/self-certification)
//
// The readers of the absence detail — the person, a manager of theirs
// today, the Approver, HR; the withdrawal to the care-day fields' readers.
func (s *server) GetStaffPeopleByIdSelfCertification(ctx context.Context, req gen.GetStaffPeopleByIdSelfCertificationRequestObject) (gen.GetStaffPeopleByIdSelfCertificationResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	var out gen.StaffSelfCertification
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := readPerson(ctx, q, req.Id); err != nil {
			return err
		}
		a, err := personAudience(ctx, q, caller, req.Id)
		if err != nil {
			return err
		}
		if !a.reads(groupAbsenceDetail) {
			return notFound()
		}
		out, err = selfCertResponse(ctx, q, caller.Settings, req.Id, caller.Today, a)
		return err
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffPeopleByIdSelfCertification404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetStaffPeopleByIdSelfCertification200JSONResponse(out), nil
}

// GetStaffMeSelfCertification Get my own egenmelding standing
// (GET /api/v1/staff/me/self-certification)
func (s *server) GetStaffMeSelfCertification(ctx context.Context, _ gen.GetStaffMeSelfCertificationRequestObject) (gen.GetStaffMeSelfCertificationResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	if caller.Person == nil {
		return gen.GetStaffMeSelfCertification404ApplicationProblemPlusJSONResponse(noPerson()), nil
	}
	var out gen.StaffSelfCertification
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		out, err = selfCertResponse(ctx, q, caller.Settings, *caller.Person, caller.Today, selfAudience)
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.GetStaffMeSelfCertification200JSONResponse(out), nil
}

// parseWithdrawal judges a withdrawal: until null (or left out) lifts it;
// else until after today and at most a year ahead, with a reason.
func parseWithdrawal(body gen.StaffSelfCertWithdrawalRequest, today time.Time) (pgtype.Date, *string, fieldErrors) {
	var f fieldErrors
	if body.Until == nil {
		return pgtype.Date{}, nil, f
	}
	until := body.Until.Time
	switch {
	case !until.After(today):
		f.add("until", "A withdrawal lasts until a day after today")
	case until.After(today.AddDate(1, 0, 0)):
		f.add("until", "A withdrawal lasts at most a year")
	}
	var reason *string
	if body.Reason != nil {
		v := strings.TrimSpace(*body.Reason)
		reason = &v
	}
	switch {
	case reason == nil || *reason == "":
		f.add("reason", "A withdrawal needs its reason")
	case utf8.RuneCountInString(*reason) > withdrawalReasonMax:
		f.add("reason", fmt.Sprintf("A reason is at most %d characters", withdrawalReasonMax))
	}
	return dayParam(until), reason, f
}

// PutStaffPeopleByIdSelfCertificationWithdrawal Withdraw or restore egenmelding
// (PUT /api/v1/staff/people/{id}/self-certification/withdrawal)
//
// staff:manage (the router holds it), under the person's lock; recorded as
// self_cert_withdrawn or self_cert_restored, class E, with the
// selfCertWithdrawal marker its shaping and its erase read.
func (s *server) PutStaffPeopleByIdSelfCertificationWithdrawal(ctx context.Context, req gen.PutStaffPeopleByIdSelfCertificationWithdrawalRequestObject) (gen.PutStaffPeopleByIdSelfCertificationWithdrawalResponseObject, error) {
	var body gen.StaffSelfCertWithdrawalRequest
	if req.Body != nil {
		body = *req.Body
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	if f := s.unknownProperties(ctx, "StaffSelfCertWithdrawalRequest"); f.failed() {
		return gen.PutStaffPeopleByIdSelfCertificationWithdrawal400ApplicationProblemPlusJSONResponse(invalid(invalidWithdrawalTitle, f)), nil
	}
	until, reason, f := parseWithdrawal(body, caller.Today)
	if f.failed() {
		return gen.PutStaffPeopleByIdSelfCertificationWithdrawal400ApplicationProblemPlusJSONResponse(invalid(invalidWithdrawalTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var out gen.StaffSelfCertification
	err = s.withPersonLock(ctx, req.Id, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		current, err := txq.PersonSelfCertWithdrawal(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("staff: read person %d's withdrawal: %w", req.Id, err)
		}
		// The day it was decided; re-issuing a withdrawal still in force
		// keeps it, so extending one opens no days between (re-check 2).
		var from pgtype.Date
		switch {
		case !until.Valid:
		case current.SelfCertWithdrawnFrom.Valid && current.SelfCertWithdrawnUntil.Valid && !current.SelfCertWithdrawnUntil.Time.Before(caller.Today):
			from = current.SelfCertWithdrawnFrom
		default:
			from = dayParam(caller.Today)
		}
		if err := txq.SetSelfCertWithdrawal(ctx, store.SetSelfCertWithdrawalParams{Until: until, Reason: reason, FromDay: from, Now: s.deps.Clock(), ID: req.Id}); err != nil {
			return fmt.Errorf("staff: write person %d's withdrawal: %w", req.Id, err)
		}
		values := func(u pgtype.Date, r *string) map[string]any {
			out := map[string]any{}
			if u.Valid {
				out["until"] = u.Time.Format(time.DateOnly)
			}
			if r != nil {
				out["reason"] = *r
			}
			return out
		}
		kind := eventSelfCertWithdrawn
		if !until.Valid {
			kind = eventSelfCertRestored
		}
		before, after := values(current.SelfCertWithdrawnUntil, current.SelfCertWithdrawnReason), values(until, reason)
		c := changes{}
		for _, name := range []string{"until", "reason"} {
			if fmt.Sprint(before[name]) != fmt.Sprint(after[name]) {
				c[name] = fieldChange{From: before[name], To: after[name]}
			}
		}
		payload, classes := classedPayload("personId", req.Id, c, func(string) string { return healthClass })
		delete(payload, "personId")
		payload["selfCertWithdrawal"] = true
		if err := s.recordClassedEvent(ctx, txq, req.Id, kind, who, payload, classes); err != nil {
			return err
		}
		out, err = selfCertResponse(ctx, txq, caller.Settings, req.Id, caller.Today, audience{Manage: true, PersonalData: caller.PersonalData})
		return err
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.PutStaffPeopleByIdSelfCertificationWithdrawal404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusConflict {
			return gen.PutStaffPeopleByIdSelfCertificationWithdrawal409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.PutStaffPeopleByIdSelfCertificationWithdrawal404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PutStaffPeopleByIdSelfCertificationWithdrawal200JSONResponse(out), nil
}
