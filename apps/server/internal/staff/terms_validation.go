package staff

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is a terms row's own rules (design §4 Terms, S5; plan, Exact
// values): each field's value, and the rules that tie the fields of one row
// together — the required pair, the temporary ground, the appointment form,
// the probation and the hours for a full position. The rules that need the
// employment's other rows or the manager graph are terms.go's.

// The widths of the terms' text columns, in characters.
const (
	titleMaxLength        = 120
	hiredOutToMaxLength   = 200
	payMethodMaxLength    = 40
	paydayMaxLength       = 100
	termsTextMaxLength    = 1000
	referenceMaxLength    = 200
	payGradeMaxLength     = 20
	payNameMaxLength      = 100
	payComponentsMax      = 20
	breakMinutesMax       = 24 * 60
	noticeMax             = 999
	hoursMaxHundredths    = 168 * 100
	payAmountMaxHundredth = 9_999_999_999_99 // numeric(12,2)
)

// occupationPattern is a STYRK-08 yrkeskode: exactly seven digits.
var occupationPattern = regexp.MustCompile(`^[0-9]{7}$`)

// currencyPattern is an ISO 4217 code as a contract states it: three letters.
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

// defaultFullTimeHours is the hours per week for a full position a scheme
// implies, in hundredths (plan, Exact values; reading 14 for other_shift).
func defaultFullTimeHours(scheme string) int64 {
	switch scheme {
	case "shift_365":
		return 3650
	case "offshore_336", "continuous_336":
		return 3360
	case "round_the_clock_355":
		return 3550
	}
	return 3750
}

// fullTimeHoursMismatch is the a-melding's rule for the hours of a full
// position under a scheme (spec S5 as amended): exactly 36.5 for shift_365,
// exactly 35.5 for round_the_clock_355, 28 to 33.6 for the two 336 schemes,
// free for not_shift and other_shift. hours is in hundredths.
func fullTimeHoursMismatch(scheme string, hours int64) bool {
	switch scheme {
	case "shift_365":
		return hours != 3650
	case "round_the_clock_355":
		return hours != 3550
	case "offshore_336", "continuous_336":
		return hours < 2800 || hours > 3360
	}
	return false
}

// appointmentFormAgrees is whether an ansettelsesform goes with an employment
// type: fast and fast_utleid with a permanent employment, the other three
// with a temporary one.
func appointmentFormAgrees(employmentType, form string) bool {
	switch employmentType {
	case "fast":
		return form == "fast" || form == "fast_utleid"
	case "midlertidig":
		return form == "midlertidig" || form == "midlertidig_utleid" || form == "midlertidig_tilkallingsvikar"
	}
	return false
}

// probationTooLong is the probation's rule (aml. § 15-6; design §4 Terms),
// answering the field it refuses and why, "" when it holds. The probation
// ends at most six months after the employment's start, and for a temporary
// employment with an expected end at most half the expected term after it.
// An extension (§ 15-6 (4)) needs the agreement, a reason and a probation to
// extend; it lies after the probation's end, by at most the probation's own
// length — the extension answers for absence during the probation, which
// cannot outlast it.
func probationTooLong(start time.Time, t store.StaffEmploymentTerm) (string, string) {
	end := civilPtr(t.ProbationEndDate)
	if end != nil {
		if limit := addMonths(start, 6); end.After(limit) {
			return "probationEndDate", fmt.Sprintf("A probation ends at most six months after the start, on %s at the latest", limit.Format(time.DateOnly))
		}
		if expected := civilPtr(t.ExpectedEndDate); t.EmploymentType == "midlertidig" && expected != nil &&
			2*daysBetween(start, *end) > daysBetween(start, *expected) {
			return "probationEndDate", "A temporary employment's probation lasts at most half its expected term"
		}
	}
	extended := civilPtr(t.ProbationExtendedTo)
	if extended == nil {
		return "", ""
	}
	switch {
	case !t.ProbationExtensionAgreed:
		return "probationExtendedTo", "A probation is extended only as agreed in writing; set probationExtensionAgreed"
	case t.ProbationExtensionReason == nil:
		return "probationExtendedTo", "An extended probation needs its reason"
	case end == nil:
		return "probationExtendedTo", "Only a probation with an end date can be extended"
	case !extended.After(*end):
		return "probationExtendedTo", "The extended probation ends after the probation's own end"
	case daysBetween(*end, *extended) > daysBetween(start, *end):
		return "probationExtendedTo", "A probation is extended by at most its own length, the absence it answers for"
	}
	return "", ""
}

// daysBetween is the number of calendar days from a to b.
func daysBetween(a, b time.Time) int {
	return int(b.Sub(a).Hours() / 24)
}

// termsSource is where each field of a new row comes from: given reports
// whether the request names a field (null included), and base is the row the
// fields it does not name take their values from — the row in force before
// the new one on POST /employments/{id}/terms, nothing for an employment's
// first row or a replace.
type termsSource struct {
	given func(name string) bool
	base  *store.StaffEmploymentTerm
}

// pick is a field's value: the request's when it names the field — nil
// clearing it — else the base row's.
func pick[T any](src termsSource, name string, sent *T, inherited func(store.StaffEmploymentTerm) *T) *T {
	if src.given(name) || src.base == nil {
		return sent
	}
	return inherited(*src.base)
}

// parseTerms is a request's terms row, every failure collected: the fields
// it names validated, the others taken from src.base, then the rules that tie
// the row together judged on the result. start is the employment's start,
// which the probation is measured from. prefix names the fields in the
// errors ("terms." for an employment's nested first row). The row answered
// carries no id, employment or revision; the caller sets them.
func parseTerms(body gen.StaffTermsRequest, src termsSource, start time.Time, prefix string) (store.StaffEmploymentTerm, fieldErrors) {
	var f fieldErrors
	var out store.StaffEmploymentTerm
	field := func(name string) string { return prefix + name }
	add := func(name, message string) { f.add(field(name), message) }
	text := func(name, label string, sent *string, inherited func(store.StaffEmploymentTerm) *string, limit int) *string {
		if src.given(name) || src.base == nil {
			v, msg := optionalText(label, sent, limit)
			add(name, msg)
			return v
		}
		return inherited(*src.base)
	}
	enum := func(name, label string, sent *string, inherited func(store.StaffEmploymentTerm) *string, values []string) *string {
		if src.given(name) || src.base == nil {
			v, msg := parseEnum(label, sent, values)
			add(name, msg)
			return v
		}
		return inherited(*src.base)
	}

	// The required pair (spec S5, reading 2): refused when missing, never
	// defaulted by the module — only taken from the row before.
	var missing []string
	if v := enum("employmentType", "An employment type", (*string)(body.EmploymentType),
		func(r store.StaffEmploymentTerm) *string { return nonEmpty(r.EmploymentType) }, employmentTypes); v != nil {
		out.EmploymentType = *v
	} else if len(f.errs[field("employmentType")]) == 0 {
		missing = append(missing, "employmentType")
	}
	if src.given("ftePercent") || src.base == nil {
		if body.FtePercent == nil {
			missing = append(missing, "ftePercent")
		} else if h, ok := hundredthsOf(*body.FtePercent); !ok || h <= 0 || h > 10000 {
			add("ftePercent", fmt.Sprintf("A percentage is more than 0 and at most 100, with at most two decimals, but was %v", *body.FtePercent))
		} else {
			out.FtePercent = numericOfHundredths(h)
		}
	} else {
		out.FtePercent = src.base.FtePercent
	}
	for _, name := range missing {
		f.addCoded(field(name), codeTermsRequiredFields, "Every terms row states the "+map[string]string{
			"employmentType": "employment type (fast or midlertidig)", "ftePercent": "percentage of a full position",
		}[name]+"; the law presumes the employee's claim when it is missing")
	}

	// The temporary employment's own fields go with a permanent one only when
	// sent: a row that turns permanent and leaves them out drops them.
	fast := out.EmploymentType == "fast"
	temporaryInherited := func(get func(store.StaffEmploymentTerm) *string) func(store.StaffEmploymentTerm) *string {
		return func(r store.StaffEmploymentTerm) *string {
			if fast {
				return nil
			}
			return get(r)
		}
	}
	out.TemporaryGround = enum("temporaryGround", "A temporary ground", (*string)(body.TemporaryGround),
		temporaryInherited(func(r store.StaffEmploymentTerm) *string { return r.TemporaryGround }), temporaryGrounds)
	if src.given("expectedEndDate") || src.base == nil {
		out.ExpectedEndDate = pgDate(civilDate(body.ExpectedEndDate))
	} else if !fast {
		out.ExpectedEndDate = src.base.ExpectedEndDate
	}

	// The appointment form follows the type when it is left out and the one
	// before does not agree with it, or there is none before.
	if src.given("appointmentForm") {
		if v := enum("appointmentForm", "An appointment form", (*string)(body.AppointmentForm), nil, appointmentForms); v != nil {
			out.AppointmentForm = *v
		}
	}
	if out.AppointmentForm == "" && len(f.errs[field("appointmentForm")]) == 0 {
		if src.base != nil && !src.given("appointmentForm") && appointmentFormAgrees(out.EmploymentType, src.base.AppointmentForm) {
			out.AppointmentForm = src.base.AppointmentForm
		} else {
			out.AppointmentForm = out.EmploymentType
		}
	}

	out.HiredOutTo = text("hiredOutTo", "The hirer", body.HiredOutTo, func(r store.StaffEmploymentTerm) *string { return r.HiredOutTo }, hiredOutToMaxLength)

	// The scheme and its hours: a new scheme left without hours takes its
	// own default rather than the hours the old scheme had.
	scheme := enum("workingHoursScheme", "A working-hours scheme", (*string)(body.WorkingHoursScheme),
		func(r store.StaffEmploymentTerm) *string { return nonEmpty(r.WorkingHoursScheme) }, workingHoursSchemes)
	if scheme == nil {
		def := "not_shift"
		scheme = &def
	}
	out.WorkingHoursScheme = *scheme
	switch {
	case body.FullTimeHoursPerWeek != nil && (src.given("fullTimeHoursPerWeek") || src.base == nil):
		if h, ok := hundredthsOf(*body.FullTimeHoursPerWeek); !ok || h <= 0 || h > hoursMaxHundredths {
			add("fullTimeHoursPerWeek", fmt.Sprintf("Hours per week are more than 0 and at most 168, with at most two decimals, but were %v", *body.FullTimeHoursPerWeek))
		} else {
			out.FullTimeHoursPerWeek = numericOfHundredths(h)
		}
	case src.base != nil && !src.given("fullTimeHoursPerWeek") && src.base.WorkingHoursScheme == out.WorkingHoursScheme:
		out.FullTimeHoursPerWeek = src.base.FullTimeHoursPerWeek
	default:
		out.FullTimeHoursPerWeek = numericOfHundredths(defaultFullTimeHours(out.WorkingHoursScheme))
	}

	if src.given("occupationCode") || src.base == nil {
		if body.OccupationCode != nil {
			v := strings.TrimSpace(*body.OccupationCode)
			if !occupationPattern.MatchString(v) {
				add("occupationCode", "An occupation code (STYRK-08) is seven digits")
			} else {
				out.OccupationCode = &v
			}
		}
	} else {
		out.OccupationCode = src.base.OccupationCode
	}
	out.Title = text("title", "A title", body.Title, func(r store.StaffEmploymentTerm) *string { return r.Title }, titleMaxLength)
	out.EmployeeType = enum("employeeType", "An employee type", (*string)(body.EmployeeType),
		func(r store.StaffEmploymentTerm) *string { return r.EmployeeType }, employeeTypes)
	out.RemunerationType = enum("remunerationType", "A remuneration type", (*string)(body.RemunerationType),
		func(r store.StaffEmploymentTerm) *string { return r.RemunerationType }, remunerationTypes)
	if src.given("payComponents") || src.base == nil {
		out.PayComponents = parsePayComponents(body.PayComponents, prefix, &f)
	} else {
		out.PayComponents = src.base.PayComponents
	}
	out.PayMethod = text("payMethod", "A pay method", body.PayMethod, func(r store.StaffEmploymentTerm) *string { return r.PayMethod }, payMethodMaxLength)
	out.PaydayText = text("paydayText", "A payday", body.PaydayText, func(r store.StaffEmploymentTerm) *string { return r.PaydayText }, paydayMaxLength)

	out.DepartmentID = pick(src, "departmentId", body.DepartmentId, func(r store.StaffEmploymentTerm) *int64 { return r.DepartmentID })
	out.LocationID = pick(src, "locationId", body.LocationId, func(r store.StaffEmploymentTerm) *int64 { return r.LocationID })
	out.ManagerPersonID = pick(src, "managerPersonId", body.ManagerPersonId, func(r store.StaffEmploymentTerm) *int64 { return r.ManagerPersonID })
	out.CostCentre = text("costCentre", "A cost centre", body.CostCentre, func(r store.StaffEmploymentTerm) *string { return r.CostCentre }, costCentreMaxLength)
	out.Division = text("division", "A division", body.Division, func(r store.StaffEmploymentTerm) *string { return r.Division }, divisionMaxLength)
	out.WorkingTimeText = text("workingTimeText", "The working time", body.WorkingTimeText, func(r store.StaffEmploymentTerm) *string { return r.WorkingTimeText }, termsTextMaxLength)
	out.BreakMinutes = smallint(src, "breakMinutes", body.BreakMinutes, func(r store.StaffEmploymentTerm) *int16 { return r.BreakMinutes }, breakMinutesMax, prefix, &f)
	out.SpecialHoursText = text("specialHoursText", "The special arrangement", body.SpecialHoursText, func(r store.StaffEmploymentTerm) *string { return r.SpecialHoursText }, termsTextMaxLength)
	out.OvertimeRuleText = text("overtimeRuleText", "The overtime rule", body.OvertimeRuleText, func(r store.StaffEmploymentTerm) *string { return r.OvertimeRuleText }, termsTextMaxLength)

	if src.given("probationEndDate") || src.base == nil {
		out.ProbationEndDate = pgDate(civilDate(body.ProbationEndDate))
	} else {
		out.ProbationEndDate = src.base.ProbationEndDate
	}
	if agreed := pick(src, "probationExtensionAgreed", body.ProbationExtensionAgreed, func(r store.StaffEmploymentTerm) *bool {
		return &r.ProbationExtensionAgreed
	}); agreed != nil {
		out.ProbationExtensionAgreed = *agreed
	}
	if src.given("probationExtendedTo") || src.base == nil {
		out.ProbationExtendedTo = pgDate(civilDate(body.ProbationExtendedTo))
	} else {
		out.ProbationExtendedTo = src.base.ProbationExtendedTo
	}
	out.ProbationExtensionReason = text("probationExtensionReason", "The extension's reason", body.ProbationExtensionReason,
		func(r store.StaffEmploymentTerm) *string { return r.ProbationExtensionReason }, referenceMaxLength)

	out.NoticeEmployee = smallint(src, "noticeEmployee", body.NoticeEmployee, func(r store.StaffEmploymentTerm) *int16 { return r.NoticeEmployee }, noticeMax, prefix, &f)
	out.NoticeEmployer = smallint(src, "noticeEmployer", body.NoticeEmployer, func(r store.StaffEmploymentTerm) *int16 { return r.NoticeEmployer }, noticeMax, prefix, &f)
	out.NoticeUnit = enum("noticeUnit", "A notice unit", (*string)(body.NoticeUnit), func(r store.StaffEmploymentTerm) *string { return r.NoticeUnit }, noticeUnits)
	if (out.NoticeEmployee != nil || out.NoticeEmployer != nil) && out.NoticeUnit == nil && len(f.errs[field("noticeUnit")]) == 0 {
		add("noticeUnit", "A notice period needs its unit: days, weeks or months")
	}
	out.TariffReference = text("tariffReference", "A tariff reference", body.TariffReference, func(r store.StaffEmploymentTerm) *string { return r.TariffReference }, referenceMaxLength)
	out.HolidayTermsText = text("holidayTermsText", "The holiday terms", body.HolidayTermsText, func(r store.StaffEmploymentTerm) *string { return r.HolidayTermsText }, termsTextMaxLength)
	out.CompetenceDevelopmentText = text("competenceDevelopmentText", "The competence development", body.CompetenceDevelopmentText,
		func(r store.StaffEmploymentTerm) *string { return r.CompetenceDevelopmentText }, termsTextMaxLength)
	out.SocialSecurityText = text("socialSecurityText", "The social security and pension", body.SocialSecurityText,
		func(r store.StaffEmploymentTerm) *string { return r.SocialSecurityText }, termsTextMaxLength)
	if src.given("seniorityDate") || src.base == nil {
		out.SeniorityDate = pgDate(civilDate(body.SeniorityDate))
	} else {
		out.SeniorityDate = src.base.SeniorityDate
	}
	out.PayGrade = text("payGrade", "A pay grade", body.PayGrade, func(r store.StaffEmploymentTerm) *string { return r.PayGrade }, payGradeMaxLength)
	out.Note = text("note", "A note", body.Note, func(r store.StaffEmploymentTerm) *string { return r.Note }, termsTextMaxLength)

	// The rules that tie the row together, judged only on fields that passed.
	if out.EmploymentType == "midlertidig" && out.TemporaryGround == nil && len(f.errs[field("temporaryGround")]) == 0 {
		f.addCoded(field("temporaryGround"), codeTemporaryGroundRequired, "A temporary employment states its ground: aml. § 14-9 (2) a–e, or a tariff agreement's")
	}
	if out.EmploymentType != "" && out.AppointmentForm != "" && !appointmentFormAgrees(out.EmploymentType, out.AppointmentForm) {
		f.addCoded(field("appointmentForm"), codeAppointmentFormMismatch,
			fmt.Sprintf("The appointment form '%s' does not go with a %s employment", out.AppointmentForm, out.EmploymentType))
	}
	if name, msg := probationTooLong(start, out); name != "" {
		f.addCoded(field(name), codeProbationTooLong, msg)
	}
	if end := civilPtr(out.ProbationEndDate); end != nil && end.Before(start) {
		add("probationEndDate", "A probation ends on or after the employment's start")
	}
	if out.FullTimeHoursPerWeek.Valid && len(f.errs[field("fullTimeHoursPerWeek")]) == 0 &&
		fullTimeHoursMismatch(out.WorkingHoursScheme, hundredthsOfNumeric(out.FullTimeHoursPerWeek)) {
		f.addCoded(field("fullTimeHoursPerWeek"), codeFullTimeHoursMismatch, fullTimeHoursRule(out.WorkingHoursScheme))
	}
	return out, f
}

// fullTimeHoursRule is the a-melding's rule for a scheme, in words.
func fullTimeHoursRule(scheme string) string {
	switch scheme {
	case "shift_365":
		return "Under shift_365 a full position is exactly 36.5 hours a week"
	case "round_the_clock_355":
		return "Under round_the_clock_355 a full position is exactly 35.5 hours a week"
	}
	return fmt.Sprintf("Under %s a full position is 28 to 33.6 hours a week", scheme)
}

// nonEmpty is s, nil when empty: a NOT NULL column read as an optional value.
func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// smallint is an optional whole number from 0 to limit, as the smallint
// columns hold it.
func smallint(src termsSource, name string, sent *int32, inherited func(store.StaffEmploymentTerm) *int16, limit int32, prefix string, f *fieldErrors) *int16 {
	if !src.given(name) && src.base != nil {
		return inherited(*src.base)
	}
	if sent == nil {
		return nil
	}
	if *sent < 0 || *sent > limit {
		f.add(prefix+name, fmt.Sprintf("A value from 0 to %d, but was %d", limit, *sent))
		return nil
	}
	v := int16(*sent)
	return &v
}

// storedPayComponent is a pay component as pay_components stores it.
type storedPayComponent struct {
	Name     string  `json:"name"`
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
	Period   string  `json:"period"`
}

// parsePayComponents is the pay components as the column stores them, nil
// for none sent; each component's failures are named by its index.
func parsePayComponents(sent *[]gen.StaffPayComponent, prefix string, f *fieldErrors) []byte {
	if sent == nil {
		return nil
	}
	list := *sent
	if len(list) > payComponentsMax {
		f.add(prefix+"payComponents", fmt.Sprintf("At most %d pay components, but there were %d", payComponentsMax, len(list)))
		return nil
	}
	out := make([]storedPayComponent, 0, len(list))
	for i, c := range list {
		at := fmt.Sprintf("%spayComponents[%d].", prefix, i)
		name, msg := requiredText("A name", c.Name, payNameMaxLength)
		f.add(at+"name", msg)
		if h, ok := hundredthsOf(c.Amount); !ok || h < 0 || h > payAmountMaxHundredth {
			f.add(at+"amount", fmt.Sprintf("An amount is 0 or more, with at most two decimals, but was %v", c.Amount))
		}
		currency := strings.ToUpper(strings.TrimSpace(c.Currency))
		if !currencyPattern.MatchString(currency) {
			f.add(at+"currency", "A currency is an ISO 4217 code, three letters")
		}
		if !slices.Contains(payPeriods, string(c.Period)) {
			f.add(at+"period", fmt.Sprintf("A period is one of %s", strings.Join(payPeriods, ", ")))
		}
		out = append(out, storedPayComponent{Name: name, Amount: c.Amount, Currency: currency, Period: string(c.Period)})
	}
	b, err := json.Marshal(out)
	if err != nil {
		f.add(prefix+"payComponents", "The pay components could not be stored")
		return nil
	}
	return b
}
