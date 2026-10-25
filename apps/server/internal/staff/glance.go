package staff

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is a person at a glance (person-glance design S4, S5 and §4's
// table): every card the caller may read in one response, computed on read
// in one snapshot, "today" and "this year" in the business zone (reading 6).
// Each section is read through the rule its own endpoint already has — the
// holiday account, the egenmelding standing, the care days, the payroll
// file's per-day shares, an absence row's shaping, the § 14-6 verdict — so
// the glance never counts a day differently from the tab it summarises. A
// section the audience does not read is absent, never zero (glanceFieldGroups).

// glancePageSize is the page the person's absence rows are read in.
const glancePageSize = 500

// glanceInput is what one person's glance is assembled from, read in one
// snapshot: the employments and their terms chains (by employment id), the
// absence rows from 1 January of this year on — or, to a caller who sees the
// person only as a colleague, the coalesced spans of their whole absence
// instead (colleagueSpans) — and, each only for an audience that reads it,
// the holiday account's input, the egenmelding standing, the care days and
// the sick-leave case open today.
type glanceInput struct {
	employments []store.StaffEmployment
	chains      map[int64][]store.StaffEmploymentTerm
	rows        []store.ListAbsencesRow
	spans       []gen.StaffAbsence
	account     *accountInput
	selfCert    *gen.StaffSelfCertification
	careDays    *gen.StaffCareDays
	openCase    *store.StaffSickCase
}

// GetStaffPeopleByIdGlance Get a person at a glance
// (GET /api/v1/staff/people/{id}/glance)
//
// Whoever sees the person reads it, each section shaped by its field group;
// anyone else gets the bare 404. Time's hours are read before the snapshot
// opens, with the request's context, and only for the Hours group's readers
// (design §4 Locking): the link is read in a short snapshot of its own first,
// together with the person and the audience. The audience is judged again in
// the glance's own snapshot, since time's call sits between the two: a
// caller who no longer sees the person is the bare 404, the sections are
// shaped by the second judgement, and the hours go only to a caller who
// reads them in both. A provider that fails fails the glance (module
// boundaries: a failing provider is an error, never a zero).
func (s *server) GetStaffPeopleByIdGlance(ctx context.Context, req gen.GetStaffPeopleByIdGlanceRequestObject) (gen.GetStaffPeopleByIdGlanceResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	var (
		a    audience
		link *string
	)
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := readPerson(ctx, q, req.Id); err != nil {
			return err
		}
		var err error
		if a, err = personAudience(ctx, q, caller, req.Id); err != nil {
			return err
		}
		if !a.seesPerson() {
			return notFound()
		}
		link, err = linkedUserID(ctx, q, req.Id)
		return err
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffPeopleByIdGlance404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	weekStart := weekStartOf(caller.Today)
	week, err := s.glanceWeek(ctx, a, weekStart, link)
	if err != nil {
		return nil, err
	}
	var (
		in    glanceInput
		again audience
	)
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := readPerson(ctx, q, req.Id); err != nil {
			return err
		}
		var err error
		if again, err = personAudience(ctx, q, caller, req.Id); err != nil {
			return err
		}
		if !again.seesPerson() {
			return notFound()
		}
		in, err = readGlanceInput(ctx, q, caller, req.Id, again)
		return err
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffPeopleByIdGlance404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := glanceOf(in, again, caller.Today)
	if a.reads(glanceGroupOf("hours")) && again.reads(glanceGroupOf("hours")) {
		out.Hours = hoursSection(s.deps.Timesheets != nil, weekStart, expectedWeekOf(in.employments, in.chains, caller.Today), week)
	}
	return gen.GetStaffPeopleByIdGlance200JSONResponse(out), nil
}

// readGlanceInput reads personID's glance on q for a, reading nothing a
// section a does not read needs. A person gone since the audience was judged
// is the 404.
func readGlanceInput(ctx context.Context, q *store.Queries, caller *callerContext, personID int64, a audience) (glanceInput, error) {
	var in glanceInput
	if _, err := readPerson(ctx, q, personID); err != nil {
		return in, err
	}
	today := caller.Today
	employments, err := q.ListPersonEmployments(ctx, personID)
	if err != nil {
		return in, fmt.Errorf("staff: read person %d's employments: %w", personID, err)
	}
	in.employments = employments
	in.chains = map[int64][]store.StaffEmploymentTerm{}
	if len(employments) > 0 {
		ids := make([]int64, 0, len(employments))
		for _, e := range employments {
			ids = append(ids, e.ID)
		}
		terms, err := q.ListTermsOfEmployments(ctx, ids)
		if err != nil {
			return in, fmt.Errorf("staff: read person %d's terms: %w", personID, err)
		}
		for _, t := range terms {
			in.chains[t.EmploymentID] = append(in.chains[t.EmploymentID], t)
		}
	}
	switch {
	case a.onlyColleague():
		// The whole history, so a run across New Year — or one row
		// registered beside another — reads as the one span GET /absences
		// gives the colleague, never where one row ends.
		spans, err := colleagueSpans(ctx, q, []int64{personID}, pgtype.Date{}, pgtype.Date{})
		if err != nil {
			return in, err
		}
		in.spans = spans
	case a.reads(glanceGroupOf("absence")):
		jan1 := time.Date(today.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
		for page := int32(0); ; page++ {
			rows, err := q.ListAbsences(ctx, store.ListAbsencesParams{
				PersonID: &personID, FromDay: dayParam(jan1), Everyone: true,
				PageOffset: page * glancePageSize, PageSize: glancePageSize,
			})
			if err != nil {
				return in, fmt.Errorf("staff: read person %d's absences: %w", personID, err)
			}
			in.rows = append(in.rows, rows...)
			if len(rows) < glancePageSize {
				break
			}
		}
	}
	if a.reads(glanceGroupOf("holiday")) {
		account, err := readAccountInput(ctx, q, caller.Settings, personID)
		if err != nil {
			return in, err
		}
		in.account = &account
	}
	if a.reads(glanceGroupOf("selfCertification")) {
		standing, err := selfCertResponse(ctx, q, caller.Settings, personID, today, a)
		if err != nil {
			return in, err
		}
		in.selfCert = &standing
	}
	if a.reads(glanceGroupOf("childSick")) {
		care, err := careDaysResponse(ctx, q, personID, today.Year(), today)
		if err != nil {
			return in, err
		}
		in.careDays = &care
	}
	if a.readsCase() {
		// The newest open cases first; a case whose first day is still ahead
		// is not the one open today (openCaseOn).
		cases, err := q.ListCases(ctx, store.ListCasesParams{
			PersonID: &personID, Everyone: true, Today: dayParam(today), Status: "open", PageSize: 2,
		})
		if err != nil {
			return in, fmt.Errorf("staff: read person %d's open case: %w", personID, err)
		}
		in.openCase = openCaseOn(cases, today)
	}
	return in, nil
}

// openCaseOn is the case of cases — open ones — that has begun by today,
// nil when none has.
func openCaseOn(cases []store.StaffSickCase, today time.Time) *store.StaffSickCase {
	for i := range cases {
		if !civil(cases[i].FirstDay).After(today) {
			return &cases[i]
		}
	}
	return nil
}

// glanceOf is the glance a reads of in on today, the hours aside (the
// handler adds them from time's answer).
func glanceOf(in glanceInput, a audience, today time.Time) gen.StaffPersonGlance {
	var out gen.StaffPersonGlance
	year := today.Year()
	if a.reads(glanceGroupOf("sickDays")) {
		self, certified := sickDaysOf(decidedRows(in.rows), year)
		out.SickDays = &gen.StaffGlanceSickDays{SelfCertified: float64(self) / wholeDay, Certified: float64(certified) / wholeDay}
	}
	if a.reads(glanceGroupOf("selfCertification")) && in.selfCert != nil {
		out.SelfCertification = &gen.StaffGlanceSelfCertification{
			OccurrencesUsed: in.selfCert.OccurrencesUsed, OccurrencesLimit: in.selfCert.OccurrencesPerYear,
			DaysUsed: in.selfCert.DaysUsed, DaysLimit: in.selfCert.DaysPerYear,
		}
	}
	if a.reads(glanceGroupOf("holiday")) && in.account != nil {
		account := accountFor(*in.account, year)
		taken, planned := holidaySplit(in.account.Absences, today)
		out.Holiday = &gen.StaffGlanceHoliday{
			TakenToDate: daysOf(taken), Planned: daysOf(planned), Remaining: daysOf(account.Remaining), Overdrawn: account.Overdrawn,
		}
	}
	if a.reads(glanceGroupOf("childSick")) && in.careDays != nil {
		out.ChildSick = &gen.StaffGlanceChildSick{Days: in.careDays.Used + in.careDays.IneligibleDays}
		if a.reads(groupCareDays) && in.careDays.Recorded {
			entitlement, remaining := in.careDays.Entitlement, in.careDays.Remaining
			out.ChildSick.Entitlement, out.ChildSick.Remaining = &entitlement, &remaining
		}
	}
	if a.reads(glanceGroupOf("employment")) {
		out.Employment = employmentSection(in.employments, in.chains, a, today)
	}
	if a.reads(glanceGroupOf("absence")) {
		if a.onlyColleague() {
			out.Absence = colleagueAbsenceSection(in.spans, today)
		} else {
			out.Absence = absenceSection(in.rows, in.openCase, a, today)
		}
	}
	// The § 14-6 verdict is staff:manage's alone (person-glance design §4's
	// contract row) — its own group, not HR's notes, which
	// staff:personal-data reads too.
	if a.reads(glanceGroupOf("contract")) {
		out.Contract = &gen.StaffGlanceContract{}
		if place := placeOf(in.employments, today); place != nil {
			signed := place.ContractSignedAt.Valid
			out.Contract.Signed = &signed
		}
	}
	return out
}

// decidedRows is rows' approved and registered ones, as the payroll file's
// per-day shares read them (sharesOn): a request, a refusal and a cancel
// count nothing.
func decidedRows(rows []store.ListAbsencesRow) []store.AbsencesBetweenRow {
	var out []store.AbsencesBetweenRow
	for _, r := range rows {
		a := r.StaffAbsence
		if a.Status != absenceApproved && a.Status != absenceRegistered {
			continue
		}
		out = append(out, store.AbsencesBetweenRow{
			ID: a.ID, PersonID: a.PersonID, TypeID: a.TypeID, FirstDay: a.FirstDay, LastDay: a.LastDay,
			Hours: a.Hours, Percent: a.Percent, DegreePercent: a.DegreePercent, Status: a.Status, Kind: r.Kind, Unit: r.Unit,
		})
	}
	return out
}

// sickDaysOf is year's sick days of rows — approved and registered — each
// working day counted as the payroll file counts it (sharesOn): by its
// degree, less leave beside it, and nothing on a holiday day; egenmelding
// and sykmelding apart, in hundredths of a percent of a day (wholeDay is
// one). The whole calendar year counts, a sykmelding registered ahead
// included.
func sickDaysOf(rows []store.AbsencesBetweenRow, year int) (self, certified int64) {
	jan1 := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
	dec31 := time.Date(year, time.December, 31, 0, 0, 0, 0, time.UTC)
	for d := jan1; !d.After(dec31); d = d.AddDate(0, 0, 1) {
		if !worksOn(d) {
			continue
		}
		s := sharesOn(rows, d)
		self += s.SelfSick
		certified += s.CertifiedSick
	}
	return self, certified
}

// holidaySplit is this year's holiday of absences — the account's rows, each
// with its share — split against today, in dayUnit: taken is the working
// days on or before today of the approved and registered rows, planned the
// working days after today of the approved, registered and requested ones.
func holidaySplit(absences []accountAbsence, today time.Time) (taken, planned int64) {
	jan1 := time.Date(today.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
	dec31 := time.Date(today.Year(), time.December, 31, 0, 0, 0, 0, time.UTC)
	tomorrow := today.AddDate(0, 0, 1)
	for _, a := range absences {
		ahead := int64(workingDays(maxTime(a.First, tomorrow), minTime(a.Last, dec31))) * a.Share
		switch a.Status {
		case absenceApproved, absenceRegistered:
			taken += int64(workingDays(maxTime(a.First, jan1), minTime(a.Last, today))) * a.Share
			planned += ahead
		case absenceRequested:
			planned += ahead
		}
	}
	return taken, planned
}

// placeOf is the person's place on today: of employments, the earliest in
// force by start, then id — the rule a group's rule and the department's
// default profile judge a person on (rulePlaces, placeDepartmentOf) — nil
// when none is in force. The glance reads the title, the percentage and the
// probation of the place's terms alone (employmentSection) and its
// contract's signature (the contract card); the expected week is every
// employment's in force (expectedWeekOf), not the place's.
func placeOf(employments []store.StaffEmployment, today time.Time) *store.StaffEmployment {
	var place *store.StaffEmployment
	for i := range employments {
		e := &employments[i]
		if !inForce(*e, today) {
			continue
		}
		if place != nil {
			cur, start := civil(place.StartDate), civil(e.StartDate)
			if cur.Before(start) || (cur.Equal(start) && place.ID < e.ID) {
				continue
			}
		}
		place = e
	}
	return place
}

// placeTermsOf is the terms row in force on today of the person's place,
// nil when no employment is in force or it has no row yet.
func placeTermsOf(employments []store.StaffEmployment, chains map[int64][]store.StaffEmploymentTerm, today time.Time) *store.StaffEmploymentTerm {
	place := placeOf(employments, today)
	if place == nil {
		return nil
	}
	return termsInForce(*place, chains[place.ID], today)
}

// termsInForceOn is the terms row in force on today of every employment of
// employments in force today that has one, in the employments' order.
func termsInForceOn(employments []store.StaffEmployment, chains map[int64][]store.StaffEmploymentTerm, today time.Time) []*store.StaffEmploymentTerm {
	var out []*store.StaffEmploymentTerm
	for _, e := range employments {
		if t := termsInForce(e, chains[e.ID], today); t != nil {
			out = append(out, t)
		}
	}
	return out
}

// expectedWeekOf is the week the person's contracts expect on today: the
// sum over every employment in force (expectedHours) — two employments at
// 50 % each expect the full week — nil when none has terms in force.
func expectedWeekOf(employments []store.StaffEmployment, chains map[int64][]store.StaffEmploymentTerm, today time.Time) *float64 {
	return expectedHours(termsInForceOn(employments, chains, today))
}

// wholeMonths is the whole months from from to to: the most n for which the
// day n months after from (addMonths) is not after to; none when to is
// before from.
func wholeMonths(from, to time.Time) int {
	if to.Before(from) {
		return 0
	}
	n := (to.Year()-from.Year())*12 + int(to.Month()) - int(from.Month())
	if addMonths(from, n).After(to) {
		n--
	}
	return n
}

// probationEnd is the last day of the probation t agrees — the extension's
// when one is agreed — nil when there is none or it ended before today.
func probationEnd(t *store.StaffEmploymentTerm, today time.Time) *openapi_types.Date {
	end := t.ProbationEndDate
	if t.ProbationExtensionAgreed && t.ProbationExtendedTo.Valid {
		end = t.ProbationExtendedTo
	}
	if !end.Valid || end.Time.Before(today) {
		return nil
	}
	return &openapi_types.Date{Time: end.Time}
}

// employmentSection is the employment card: the start of the unbroken run
// of employments in force today and the whole months since, the percentage
// and the title of the place's terms in force — and, to the employment
// group's readers, the probation's end while it lasts.
func employmentSection(employments []store.StaffEmployment, chains map[int64][]store.StaffEmploymentTerm, a audience, today time.Time) *gen.StaffGlanceEmployment {
	out := &gen.StaffGlanceEmployment{}
	if start := unbrokenStart(employmentSpans(employments), today); start != nil {
		months := int32(wholeMonths(*start, today))
		out.StartDate, out.TenureMonths = &openapi_types.Date{Time: *start}, &months
	}
	t := placeTermsOf(employments, chains, today)
	if t == nil {
		return out
	}
	fte := numberOf(t.FtePercent)
	out.FtePercent, out.Title = &fte, t.Title
	if a.reads(groupEmployment) {
		out.ProbationEndsOn = probationEnd(t, today)
	}
	return out
}

// glanceStatuses are the statuses an absence card names: a request, a
// decided absence and a registration — never a refusal or a cancel.
var glanceStatuses = []string{absenceRequested, absenceApproved, absenceRegistered}

// absenceSection is the absence card as a reads it: the day row covering
// today (a decided one before a request) and the first one starting after
// it, each among the rows a sees (seesAbsence) and shaped as GET /absences
// shapes it for a, reduced to its days and how its type is shown; and, to
// the absence detail's readers, the sick-leave case open today and its day.
// An hour row is no absence of the day and named by neither.
func absenceSection(rows []store.ListAbsencesRow, openCase *store.StaffSickCase, a audience, today time.Time) *gen.StaffGlanceAbsence {
	out := &gen.StaffGlanceAbsence{}
	var now, next *store.ListAbsencesRow
	for i := range rows {
		r := &rows[i]
		if r.Unit != unitDays || !slices.Contains(glanceStatuses, r.StaffAbsence.Status) ||
			!a.seesAbsence(r.StaffAbsence.Status, r.Visibility, r.Unit) {
			continue
		}
		first, last := civil(r.StaffAbsence.FirstDay), civil(r.StaffAbsence.LastDay)
		switch {
		case !first.After(today) && !last.Before(today):
			if now == nil || nowBefore(*r, *now) {
				now = r
			}
		case first.After(today):
			if next == nil || startsBefore(*r, *next) {
				next = r
			}
		}
	}
	if now != nil {
		out.Now = spanOf(*now, a)
	}
	if next != nil {
		out.Next = spanOf(*next, a)
	}
	if a.readsCase() && openCase != nil {
		first := civil(openCase.FirstDay)
		out.OpenCase = &gen.StaffGlanceOpenCase{FirstDay: openapi_types.Date{Time: first}, Day: int32(calendarDays(first, today))}
	}
	return out
}

// nowBefore is whether r is named as today's absence before o: a decided
// row before a request, then the earlier start, then the lower id.
func nowBefore(r, o store.ListAbsencesRow) bool {
	rr, or := r.StaffAbsence.Status == absenceRequested, o.StaffAbsence.Status == absenceRequested
	if rr != or {
		return !rr
	}
	return startsBefore(r, o)
}

// startsBefore is whether r starts before o, the lower id first on a tie.
func startsBefore(r, o store.ListAbsencesRow) bool {
	return cmp.Or(civil(r.StaffAbsence.FirstDay).Compare(civil(o.StaffAbsence.FirstDay)), cmp.Compare(r.StaffAbsence.ID, o.StaffAbsence.ID)) < 0
}

// colleagueAbsenceSection is the absence card as a colleague reads it: of
// spans — the person's approved and registered day rows coalesced as GET
// /absences gives them to a colleague (colleagueSpans) — the one covering
// today and the first starting after it, each its days shown as absent and
// no type, so a sykmelding over ferie, or a row registered beside another,
// never shows where one row ends and the next begins. No case.
func colleagueAbsenceSection(spans []gen.StaffAbsence, today time.Time) *gen.StaffGlanceAbsence {
	out := &gen.StaffGlanceAbsence{}
	for _, sp := range spans {
		first, last := sp.FirstDay.Time, sp.LastDay.Time
		span := &gen.StaffGlanceAbsenceSpan{FirstDay: sp.FirstDay, LastDay: sp.LastDay, ShownAs: gen.StaffAbsenceShownAsAbsent}
		switch {
		case !first.After(today) && !last.Before(today):
			out.Now = span
		case first.After(today) && (out.Next == nil || first.Before(out.Next.FirstDay.Time)):
			out.Next = span
		}
	}
	return out
}

// noAbsenceTag is the tagger a span is shaped with: a span carries no etag.
func noAbsenceTag(int64, int32) string { return "" }

// spanOf is row r as a reads it (absenceResponse), reduced to its days and
// how its type is shown: the type to its readers, leave for an HR-only one
// to all but the person and staff:personal-data, absent to a colleague.
func spanOf(r store.ListAbsencesRow, a audience) *gen.StaffGlanceAbsenceSpan {
	shaped := absenceResponse(r.StaffAbsence, r.Kind, r.Visibility, r.Unit, a, noAbsenceTag)
	return &gen.StaffGlanceAbsenceSpan{FirstDay: shaped.FirstDay, LastDay: shaped.LastDay, ShownAs: shaped.ShownAs, TypeId: shaped.TypeId}
}
