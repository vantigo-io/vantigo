package staff

import (
	"context"
	"fmt"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is care days (design S11, plan Task 5, Revision 2 Q7b): the
// year's entitlement per ftrl. § 9-6, derived from the three care-day
// fields HR types on the person (reading 29 — childrenUnder12 the children
// through the year they turn twelve without NAV's extra-days decision,
// chronicallyIllChildren those with one, through the year they turn
// eighteen; their sum is the number § 9-6 counts), and the year's child_sick
// days against it. The employer pays every day, bears the first ten and is
// refunded from day eleven (ftrl. § 9-8) — from day one when the only
// qualifying children are over twelve with extra days, which the reference
// states for payroll and this module does not model. The right needs four
// weeks' employment (§ 9-8), judged for each day used on the unbroken
// employment it lies in; a day before it is no care day. Using more than the
// entitlement is shown, never refused.

// The care-day numbers of ftrl. § 9-6: 10 days for one or two children, 15
// for three or more, both doubled for a sole carer; 10 more per chronically
// ill or disabled child, 20 for a sole carer; the employer's first ten days;
// four weeks' employment before the right.
const (
	careDaysFewChildren     = 10
	careDaysManyChildren    = 15
	careDaysManyFrom        = 3
	careDaysPerIllChild     = 10
	careDaysEmployerBears   = 10
	careDaysEmploymentWeeks = 4
)

// careDayEntitlement is the year's care days for under12 children under
// twelve and ill chronically ill or disabled ones, sole being whether the
// person is alone in caring for them.
func careDayEntitlement(under12, ill int, sole bool) int {
	n := under12 + ill
	if n <= 0 {
		return 0
	}
	base := careDaysFewChildren
	if n >= careDaysManyFrom {
		base = careDaysManyChildren
	}
	extra := ill * careDaysPerIllChild
	if sole {
		base, extra = base*2, extra*2
	}
	return base + extra
}

// careDaySplit is the used days the employer bears, and the ones beyond —
// refunded by NAV — counted no further than entitlement (whole-branch
// review): a day used past the entitlement is no care day. An entitlement
// below zero is one not recorded, which caps nothing.
func careDaySplit(used, entitlement int) (employer, beyond int) {
	if entitlement >= 0 {
		used = min(used, entitlement)
	}
	if used <= careDaysEmployerBears {
		return used, 0
	}
	return careDaysEmployerBears, used - careDaysEmployerBears
}

// careDaysResponse is personID's care days for year, judged on today.
func careDaysResponse(ctx context.Context, q *store.Queries, personID int64, year int, today time.Time) (gen.StaffCareDays, error) {
	out := gen.StaffCareDays{PersonId: personID, Year: int32(year)}
	person, err := readPerson(ctx, q, personID)
	if err != nil {
		return out, err
	}
	under12, ill := int16Value(person.ChildrenUnder12), int16Value(person.ChronicallyIllChildren)
	sole := person.SoleCarer != nil && *person.SoleCarer
	out.Recorded = person.ChildrenUnder12 != nil || person.ChronicallyIllChildren != nil || person.SoleCarer != nil
	out.ChildrenCounted = int32(under12 + ill)
	entitlement := careDayEntitlement(under12, ill, sole)
	from := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(year, time.December, 31, 0, 0, 0, 0, time.UTC)
	rows, err := q.ChildSickRows(ctx, store.ChildSickRowsParams{PersonID: personID, FromDay: dayParam(from), ToDay: dayParam(to)})
	if err != nil {
		return out, fmt.Errorf("staff: read person %d's child-sick rows: %w", personID, err)
	}
	employments, err := q.ListPersonEmployments(ctx, personID)
	if err != nil {
		return out, fmt.Errorf("staff: read person %d's employments: %w", personID, err)
	}
	spans := employmentSpans(employments)
	// Each working day is a care day only once the right has come (§ 9-8):
	// a day in the first four weeks is counted apart.
	used, ineligible := 0, 0
	for _, r := range rows {
		for d := later(civil(r.FirstDay), from); !d.After(earlier(civil(r.LastDay), to)); d = d.AddDate(0, 0, 1) {
			if !worksOn(d) {
				continue
			}
			if right := careDayRight(spans, d); right != nil && !d.Before(*right) {
				used++
			} else {
				ineligible++
			}
		}
	}
	known := entitlement
	if !out.Recorded {
		known = -1
	}
	employer, beyond := careDaySplit(used, known)
	out.Entitlement, out.Used, out.IneligibleDays, out.Remaining = int32(entitlement), int32(used), int32(ineligible), int32(entitlement-used)
	out.Exceeded = used > entitlement
	out.EmployerPaid, out.BeyondEmployer = int32(employer), int32(beyond)
	if right := careDayRight(spans, today); right != nil {
		out.EligibleFrom = &openapi_types.Date{Time: *right}
		out.Eligible = !today.Before(*right)
	}
	return out, nil
}

// int16Value is a nullable smallint as an int, 0 for NULL.
func int16Value(v *int16) int {
	if v == nil {
		return 0
	}
	return int(*v)
}

// later and earlier are the later and the earlier of two days.
func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// GetStaffPeopleByIdCareDays Get a person's care days
// (GET /api/v1/staff/people/{id}/care-days)
//
// The care-day fields' readers — the person, staff:manage,
// staff:personal-data: the entitlement says what the fields hold (a sole
// carer, a chronically ill child), so a manager, an Approver and a colleague
// get the 404 an unknown person is.
func (s *server) GetStaffPeopleByIdCareDays(ctx context.Context, req gen.GetStaffPeopleByIdCareDaysRequestObject) (gen.GetStaffPeopleByIdCareDaysResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	year, f := parseAccountYear(req.Params.Year, caller.Today)
	if f.failed() {
		return gen.GetStaffPeopleByIdCareDays400ApplicationProblemPlusJSONResponse(invalid(invalidCareDaysTitle, f)), nil
	}
	var out gen.StaffCareDays
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := readPerson(ctx, q, req.Id); err != nil {
			return err
		}
		a, err := personAudience(ctx, q, caller, req.Id)
		if err != nil {
			return err
		}
		if !a.reads(groupCareDays) {
			return notFound()
		}
		out, err = careDaysResponse(ctx, q, req.Id, year, caller.Today)
		return err
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffPeopleByIdCareDays404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetStaffPeopleByIdCareDays200JSONResponse(out), nil
}

// invalidCareDaysTitle is the title of a care-day read's 400.
const invalidCareDaysTitle = "Invalid care days"

// GetStaffMeCareDays Get my own care days
// (GET /api/v1/staff/me/care-days)
func (s *server) GetStaffMeCareDays(ctx context.Context, req gen.GetStaffMeCareDaysRequestObject) (gen.GetStaffMeCareDaysResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	if caller.Person == nil {
		return gen.GetStaffMeCareDays404ApplicationProblemPlusJSONResponse(noPerson()), nil
	}
	year, f := parseAccountYear(req.Params.Year, caller.Today)
	if f.failed() {
		return gen.GetStaffMeCareDays400ApplicationProblemPlusJSONResponse(invalid(invalidCareDaysTitle, f)), nil
	}
	var out gen.StaffCareDays
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		out, err = careDaysResponse(ctx, q, *caller.Person, year, caller.Today)
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.GetStaffMeCareDays200JSONResponse(out), nil
}

// unbrokenStart is the first day of the unbroken employment on lies in:
// the employment in force on it, and every one before that it continues —
// one in force the day before another began (ftrl. § 9-8's four weeks are
// four weeks with the employer, review L10); nil when none is in force on
// it.
func unbrokenStart(employments []daySpan, on time.Time) *time.Time {
	var start *time.Time
	for _, e := range employments {
		if !e.First.After(on) && !e.Last.Before(on) && (start == nil || e.First.Before(*start)) {
			first := e.First
			start = &first
		}
	}
	for changed := start != nil; changed; {
		changed = false
		day := start.AddDate(0, 0, -1)
		for _, e := range employments {
			if !e.First.After(day) && !e.Last.Before(day) && e.First.Before(*start) {
				first := e.First
				start, changed = &first, true
			}
		}
	}
	return start
}

// employmentSpans is employments as their days, an open one to the end of
// time.
func employmentSpans(employments []store.StaffEmployment) []daySpan {
	out := make([]daySpan, 0, len(employments))
	for _, e := range employments {
		last := time.Date(9999, time.December, 31, 0, 0, 0, 0, time.UTC)
		if e.EndDate.Valid {
			last = e.EndDate.Time
		}
		out = append(out, daySpan{First: civil(e.StartDate), Last: last})
	}
	return out
}

// careDayRight is the first day of the right to care days on the unbroken
// employment on lies in — four weeks after its start — nil when none is in
// force on it.
func careDayRight(spans []daySpan, on time.Time) *time.Time {
	start := unbrokenStart(spans, on)
	if start == nil {
		return nil
	}
	from := start.AddDate(0, 0, 7*careDaysEmploymentWeeks)
	return &from
}
