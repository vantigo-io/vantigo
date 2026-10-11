package staff

import (
	"context"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the § 14-6 check (design S5 as amended, plan Task 4's table):
// the seventeen items aml. § 14-6 (1) a–q asks a written employment contract
// to hold, each present, missing or not applicable to this employment, with
// the column it is read from. It is informative and never blocks a write.

// The states an item answers.
const (
	itemPresent       = "present"
	itemMissing       = "missing"
	itemNotApplicable = "not_applicable"
)

// contractItem is one item of the check.
type contractItem struct{ Item, State, Source string }

// set reports whether an optional text holds anything.
func set(v *string) bool { return v != nil && strings.TrimSpace(*v) != "" }

// hiredOut reports whether an appointment form is one of the two utleid.
func hiredOut(t *store.StaffEmploymentTerm) bool {
	return t != nil && strings.HasSuffix(t.AppointmentForm, "_utleid")
}

// contractCheck answers the seventeen items for employment e with t, the
// terms row judged (nil when it has none). pensionProvider is the settings'
// own, which answers item q when the row says nothing.
func contractCheck(e store.StaffEmployment, t *store.StaffEmploymentTerm, pensionProvider *string) []contractItem {
	var row store.StaffEmploymentTerm
	if t != nil {
		row = *t
	}
	state := func(present bool) string {
		if present {
			return itemPresent
		}
		return itemMissing
	}
	applies := func(applicable, present bool) string {
		if !applicable {
			return itemNotApplicable
		}
		return state(present)
	}
	temporary := row.EmploymentType == "midlertidig"
	special := row.WorkingHoursScheme != "" && row.WorkingHoursScheme != "not_shift" || set(row.SpecialHoursText)
	return []contractItem{
		{"a", state(e.PersonID != 0 && e.BusinessUnitID != 0), "employment.personId, employment.businessUnitId"},
		{"b", state(row.LocationID != nil), "terms.locationId"},
		{"c", state(set(row.Title) || set(row.OccupationCode)), "terms.title, terms.occupationCode"},
		{"d", state(e.StartDate.Valid), "employment.startDate"},
		{"e", applies(temporary, row.ExpectedEndDate.Valid && row.TemporaryGround != nil), "terms.expectedEndDate, terms.temporaryGround"},
		{"f", applies(row.ProbationEndDate.Valid, true), "terms.probationEndDate"},
		{"g", state(set(row.HolidayTermsText)), "terms.holidayTermsText"},
		{"h", state(row.NoticeEmployee != nil && row.NoticeEmployer != nil && row.NoticeUnit != nil), "terms.noticeEmployee, terms.noticeEmployer, terms.noticeUnit"},
		{"i", state(len(payKey(row.PayComponents)) > 0 && set(row.PayMethod) && set(row.PaydayText)), "terms.payComponents, terms.payMethod, terms.paydayText"},
		{"j", state(set(row.WorkingTimeText)), "terms.workingTimeText"},
		{"k", state(row.BreakMinutes != nil), "terms.breakMinutes"},
		{"l", applies(special, set(row.SpecialHoursText)), "terms.specialHoursText"},
		{"m", state(set(row.OvertimeRuleText)), "terms.overtimeRuleText"},
		{"n", applies(set(row.TariffReference) || hiredOut(t), set(row.TariffReference)), "terms.tariffReference"},
		{"o", applies(hiredOut(t), set(row.HiredOutTo)), "terms.hiredOutTo"},
		{"p", state(set(row.CompetenceDevelopmentText)), "terms.competenceDevelopmentText"},
		{"q", state(set(row.SocialSecurityText) || set(pensionProvider)), "terms.socialSecurityText, settings.pensionProvider"},
	}
}

// GetStaffEmploymentsByIdContractCheck Check an employment against § 14-6
// (GET /api/v1/staff/employments/{id}/contract-check)
//
// Judged on the row in force today, held inside the employment's days — its
// first row before the start, its last at the end. Whether an item is
// present says something about the non-pay terms and the pay alike, so it is
// the audience that reads the employment group's: the person, a manager of
// theirs and HR; a colleague, who reads the dates alone, gets the 404.
func (s *server) GetStaffEmploymentsByIdContractCheck(ctx context.Context, req gen.GetStaffEmploymentsByIdContractCheckRequestObject) (gen.GetStaffEmploymentsByIdContractCheckResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	var out gen.StaffContractCheck
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		e, a, err := visibleEmployment(ctx, q, caller, req.Id)
		if err != nil {
			return err
		}
		if !a.reads(groupEmployment) {
			return notFound()
		}
		chain, err := chainOf(ctx, q, e.ID)
		if err != nil {
			return err
		}
		t := termsInForce(e, chain, judgedOn(e, caller.Today))
		items := contractCheck(e, t, caller.Settings.PensionProvider)
		out = gen.StaffContractCheck{
			EmploymentId:     e.ID,
			ContractSigned:   e.ContractSignedAt.Valid,
			ContractSignedAt: dateOf(e.ContractSignedAt),
			Items: mapRows(items, func(i contractItem) gen.StaffContractCheckItem {
				return gen.StaffContractCheckItem{Item: i.Item, State: gen.StaffContractCheckItemState(i.State), Source: i.Source}
			}),
		}
		if t != nil {
			out.TermsId = &t.ID
		}
		return nil
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffEmploymentsByIdContractCheck404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetStaffEmploymentsByIdContractCheck200JSONResponse(out), nil
}
