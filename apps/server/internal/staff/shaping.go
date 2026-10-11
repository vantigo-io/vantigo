package staff

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is who reads what (design §5 and S21 as amended: shaping is by
// field group, per field). One static table names every field's group; the
// audience reads a group or does not; one function per response type asks
// the table for each field it fills. A group the audience may not read is
// absent from a response, and "changed" in an event's payload.
//
// The groups are §5's rows for a person, with HR's notes a group of their
// own: they are class D data about the person that the person never reads.
// Nothing here keys on the retention classes, which say when data is erased,
// not who sees it — work_email is class D yet the work group's.

// fieldGroup is one of §5's field groups.
type fieldGroup int

const (
	// groupNobody is the group of a field the table does not know: no
	// audience reads it, so a field added without a row fails closed.
	groupNobody fieldGroup = iota
	groupWork
	// groupEmploymentDates is what a colleague reads of an employment and
	// its terms (design §5: "dates, type, fte"): the dates, the employment
	// type and the percentage.
	groupEmploymentDates
	// groupEmployment is the rest of an employment and its terms but the pay:
	// the person's, their manager's and HR's.
	groupEmployment
	groupPrivate
	groupHRNotes
	groupPay // the terms' pay, the seniority date and the pay grade
	groupNationalID
	groupBankAccount
	groupLinks // the account flag and the self-service link
	// groupDocuments is a person's documents but the health ones, and their
	// events (§5's "Other documents" row): the person's and HR's.
	groupDocuments
	// groupHealth is a person's health documents — those in a restricted
	// category — and every event whose payload touches class E (§5's
	// "Milestone notes, health documents" row): the person's and
	// staff:personal-data's alone, HR without it included out.
	groupHealth
	// groupHold is the legal hold, the erasure and the reassessment (plan
	// Task 2): HR's — staff:manage and staff:personal-data — and nobody
	// else's, the person included. They are about the record's retention,
	// not about the person.
	groupHold
)

func (g fieldGroup) String() string {
	return [...]string{"nobody", "work", "employment-dates", "employment", "private", "hr-notes", "pay", "national-id", "bank-account", "links", "documents", "health", "hold"}[g]
}

// fieldGroups is the one table: every field of the person — as the contract
// names it, which is also how an event payload names it — and its group. The
// care-day fields join with delivery B (plan reading 19), the terms' fields
// with Task 4.
var fieldGroups = map[string]fieldGroup{
	"employeeNumber":    groupWork,
	"givenName":         groupWork,
	"familyName":        groupWork,
	"displayName":       groupWork,
	"workEmail":         groupWork,
	"phoneWork":         groupWork,
	"workCountry":       groupWork,
	"preferredLanguage": groupWork,
	"roles":             groupWork,

	"birthDate":    groupPrivate,
	"gender":       groupPrivate,
	"phoneMobile":  groupPrivate,
	"phonePrivate": groupPrivate,
	"privateEmail": groupPrivate,
	"street":       groupPrivate,
	"postalCode":   groupPrivate,
	"city":         groupPrivate,
	"country":      groupPrivate,
	"nextOfKin":    groupPrivate,

	"notes": groupHRNotes,

	"nationalId":  groupNationalID,
	"bankAccount": groupBankAccount,
	"user":        groupLinks,

	// The person's place today, from their terms in force and their
	// employment: the manager, the department, the business unit and the
	// location are the work group's (§5's Work row), where they stand its
	// dates'. The title is too, on the person — personFieldGroups.
	"manager":      groupWork,
	"department":   groupWork,
	"businessUnit": groupWork,
	"location":     groupWork,
	"status":       groupEmploymentDates,

	// An employment and its terms (§5's "Employment and non-pay terms" and
	// "Pay terms, seniority, grade" rows). A colleague reads the dates, the
	// type and the percentage (plan Task 4); a terms row's note is HR's own.
	"startDate":      groupEmploymentDates,
	"endDate":        groupEmploymentDates,
	"validFrom":      groupEmploymentDates,
	"validTo":        groupEmploymentDates,
	"employmentType": groupEmploymentDates,
	"ftePercent":     groupEmploymentDates,
	"fteChangedAt":   groupEmploymentDates,

	"employmentNumber":          groupEmployment,
	"relationshipType":          groupEmployment,
	"businessUnitId":            groupEmployment,
	"endReason":                 groupEmployment,
	"contractSignedAt":          groupEmployment,
	"temporaryGround":           groupEmployment,
	"expectedEndDate":           groupEmployment,
	"appointmentForm":           groupEmployment,
	"hiredOutTo":                groupEmployment,
	"workingHoursScheme":        groupEmployment,
	"fullTimeHoursPerWeek":      groupEmployment,
	"occupationCode":            groupEmployment,
	"title":                     groupEmployment,
	"employeeType":              groupEmployment,
	"departmentId":              groupEmployment,
	"locationId":                groupEmployment,
	"managerPersonId":           groupEmployment,
	"costCentre":                groupEmployment,
	"division":                  groupEmployment,
	"workingTimeText":           groupEmployment,
	"breakMinutes":              groupEmployment,
	"specialHoursText":          groupEmployment,
	"overtimeRuleText":          groupEmployment,
	"probationEndDate":          groupEmployment,
	"probationExtensionAgreed":  groupEmployment,
	"probationExtendedTo":       groupEmployment,
	"probationExtensionReason":  groupEmployment,
	"noticeEmployee":            groupEmployment,
	"noticeEmployer":            groupEmployment,
	"noticeUnit":                groupEmployment,
	"tariffReference":           groupEmployment,
	"holidayTermsText":          groupEmployment,
	"competenceDevelopmentText": groupEmployment,
	"socialSecurityText":        groupEmployment,

	"remunerationType": groupPay,
	"payComponents":    groupPay,
	"payMethod":        groupPay,
	"paydayText":       groupPay,
	"seniorityDate":    groupPay,
	"payGrade":         groupPay,
	"salaryChangedAt":  groupPay,

	"note": groupHRNotes,

	"legalHold":       groupHold,
	"legalHoldReason": groupHold,
	"erasedAt":        groupHold,
	"reassessedAt":    groupHold,
}

// groupOf is a field's group, groupNobody for a field the table does not
// name.
func groupOf(field string) fieldGroup {
	if g, ok := fieldGroups[field]; ok {
		return g
	}
	return groupNobody
}

// personFieldGroups is where the person's response names a field as a terms
// row does yet reads it by another group: the title in force today is the
// work group's (§5's Work row), read by everyone who sees the person, while a
// terms row's title — the history of titles, and its events — stays the
// employment group's, which a colleague does not read (§5: a colleague's
// terms are "dates, type, fte").
var personFieldGroups = map[string]fieldGroup{"title": groupWork}

// personGroupOf is a field's group on the person's response and the
// directory's row.
func personGroupOf(field string) fieldGroup {
	if g, ok := personFieldGroups[field]; ok {
		return g
	}
	return groupOf(field)
}

// shownOnPerson is shown for a field of the person's response or the
// directory's row, judged by personGroupOf.
func shownOnPerson[T any](a audience, field string, v *T) *T {
	if !a.reads(personGroupOf(field)) {
		return nil
	}
	return v
}

// reads reports whether the audience reads group g — §5's matrix. The work
// group and an employment's dates are everyone's who may see the person; the
// rest of the employment and its terms is the person's, their manager's and
// HR's; the private group, the pay, the national identity number's kind, the
// bank account's last four digits, the link and the documents are the
// person's own and HR's; HR's notes are HR's alone; the health documents and
// health events are the person's and staff:personal-data's; the hold group is
// HR's, as the notes are.
func (a audience) reads(g fieldGroup) bool {
	hr := a.Manage || a.PersonalData
	switch g {
	case groupWork, groupEmploymentDates:
		return a.Self || a.Manager || a.Colleague || hr
	case groupEmployment:
		return a.Self || a.Manager || hr
	case groupPrivate, groupPay, groupNationalID, groupBankAccount, groupLinks, groupDocuments:
		return a.Self || hr
	case groupHRNotes, groupHold:
		return hr
	case groupHealth:
		return a.Self || a.PersonalData
	}
	return false
}

// readsDocument is whether the audience reads a document in a category that
// is restricted or not: a health document is the health group's, every other
// the documents group's.
func (a audience) readsDocument(restricted bool) bool {
	if restricted {
		return a.reads(groupHealth)
	}
	return a.reads(groupDocuments)
}

// colleagueOnly is whether the audience is staff:view and nothing more: the
// one that sees only the people active today (plan reading 15), so a person
// it is shown — a manager on a summary, a link up the org chain — must be
// active today too.
func (a audience) colleagueOnly() bool {
	return a.Colleague && !a.Self && !a.Manager && !a.Manage && !a.PersonalData
}

// shownManager is a placement's manager as the audience may read it: absent
// for a colleague alone when the manager is not active today.
func shownManager(a audience, p placement) *gen.StaffPersonSummary {
	if a.colleagueOnly() && !p.managerActive {
		return nil
	}
	return shown(a, "manager", p.manager)
}

// seesPerson is whether the person exists for this audience at all; to
// anyone else they are the 404 an unknown id is.
func (a audience) seesPerson() bool { return a.reads(groupWork) }

// seesEvents is §5's Events row: the person, their manager and HR — never a
// colleague.
func (a audience) seesEvents() bool {
	return a.Self || a.Manager || a.Manage || a.PersonalData
}

// seesHoldEvents is whether the hold's kinds of event (holdEventKinds) are
// listed to the audience: to the hold group's readers, and to the person
// themselves, who reads each as its kind with its fields "changed".
func (a audience) seesHoldEvents() bool { return a.Self || a.reads(groupHold) }

// show is whether the audience reads field.
func (a audience) show(field string) bool { return a.reads(groupOf(field)) }

// shown is v when the audience reads field, else nil — absent on the wire.
func shown[T any](a audience, field string, v *T) *T {
	if !a.show(field) {
		return nil
	}
	return v
}

// personResponse is one person as the audience may read them, placed as p
// says. user is the linked user as the directory named them, nil when there
// is no link; it is read outside any transaction, and only for an audience
// that reads the link.
func personResponse(row store.GetPersonRow, p placement, a audience, user *gen.StaffPersonUser) gen.StaffPerson {
	out := gen.StaffPerson{
		Status:            p.status,
		Manager:           shownManager(a, p),
		Department:        shown(a, "department", p.department),
		Title:             shownOnPerson(a, "title", p.title),
		BusinessUnit:      shown(a, "businessUnit", p.businessUnit),
		Location:          shown(a, "location", p.location),
		Id:                row.ID,
		EmployeeNumber:    row.EmployeeNumber,
		GivenName:         row.GivenName,
		FamilyName:        row.FamilyName,
		DisplayName:       row.DisplayName,
		WorkCountry:       row.WorkCountry,
		PreferredLanguage: gen.StaffLanguage(row.PreferredLanguage),
		Revision:          row.Revision,
		Capabilities: gen.StaffPersonCapabilities{
			CanEdit:              a.Manage,
			CanRevealIdentifiers: a.PersonalData,
			CanSeePay:            a.reads(groupPay),
			CanManageAccount:     a.Manage,
			CanSeeHealth:         a.reads(groupHealth),
			CanHold:              a.PersonalData,
		},
		WorkEmail:    shown(a, "workEmail", row.WorkEmail),
		PhoneWork:    shown(a, "phoneWork", row.PhoneWork),
		BirthDate:    shown(a, "birthDate", dateOf(row.BirthDate)),
		Gender:       shown(a, "gender", enumOf[gen.StaffGender](row.Gender)),
		PhoneMobile:  shown(a, "phoneMobile", row.PhoneMobile),
		PhonePrivate: shown(a, "phonePrivate", row.PhonePrivate),
		PrivateEmail: shown(a, "privateEmail", row.PrivateEmail),
		Street:       shown(a, "street", row.Street),
		PostalCode:   shown(a, "postalCode", row.PostalCode),
		City:         shown(a, "city", row.City),
		Country:      shown(a, "country", row.Country),
		Notes:        shown(a, "notes", row.Notes),
		User:         shown(a, "user", user),
		ErasedAt:     shown(a, "erasedAt", row.ErasedAt),
		ReassessedAt: shown(a, "reassessedAt", row.ReassessedAt),
		LegalHold:    shown(a, "legalHold", legalHoldOf(row)),
	}
	if a.show("nationalId") {
		out.NationalId = &gen.StaffPersonNationalId{
			OnFile: row.NationalIDOnFile,
			Kind:   enumOf[gen.StaffNationalIdKind](row.NationalIDKind),
		}
	}
	if a.show("bankAccount") && row.BankAccountKind != nil && row.BankAccountLast4 != nil {
		out.BankAccount = &gen.StaffPersonBankAccount{
			Kind:  gen.StaffBankAccountKind(*row.BankAccountKind),
			Last4: *row.BankAccountLast4,
		}
	}
	return out
}

// personListItemResponse is one person in the directory, placed as p says:
// the work group, which every audience that lists reads.
func personListItemResponse(row store.ListPeopleRow, p placement, a audience) gen.StaffPersonListItem {
	return gen.StaffPersonListItem{
		Status:            p.status,
		Manager:           shownManager(a, p),
		Department:        shown(a, "department", p.department),
		Title:             shownOnPerson(a, "title", p.title),
		BusinessUnit:      shown(a, "businessUnit", p.businessUnit),
		Location:          shown(a, "location", p.location),
		Id:                row.ID,
		EmployeeNumber:    row.EmployeeNumber,
		GivenName:         row.GivenName,
		FamilyName:        row.FamilyName,
		DisplayName:       row.DisplayName,
		WorkCountry:       row.WorkCountry,
		PreferredLanguage: gen.StaffLanguage(row.PreferredLanguage),
		WorkEmail:         shown(a, "workEmail", row.WorkEmail),
		PhoneWork:         shown(a, "phoneWork", row.PhoneWork),
		ErasedAt:          shown(a, "erasedAt", row.ErasedAt),
	}
}

// legalHoldOf is a person's legal hold as the contract answers it: held or
// not, and when held, the reason and when it was set.
func legalHoldOf(row store.GetPersonRow) *gen.StaffLegalHold {
	if !row.LegalHold {
		return &gen.StaffLegalHold{Held: false}
	}
	return &gen.StaffLegalHold{Held: true, Reason: row.LegalHoldReason, At: row.LegalHoldAt}
}

// changed is what a field of a group the audience may not read shows in an
// event's payload in place of its values.
const changed = "changed"

// shapePayload is a stored event payload as the audience may read it: each
// of its fields kept when the audience reads the field's group, "changed"
// otherwise — except a field of the pay or of HR's notes, which is left out
// altogether: the day the pay changed is itself a pay date (the employment's
// salaryChangedAt, which a manager does not read), and HR's notes are HR's
// alone, so an audience that may not read them does not learn when they
// moved either. Every kind this module writes names its fields this way — the
// sealed fields' and the link's changes too, as nationalId, bankAccount and
// user, each shaped by its own group — and a reveal names none, so its
// payload is empty to everyone. A payload that is not the shape this module
// writes is answered empty rather than passed through: what is not
// understood is not shown.
//
// A document's event (documentId set) is shaped by one group for the whole
// event rather than per field, since its fields — the category, the file
// name, the dates — say what the document is: the health group when classes,
// the event's payload_classes, hold E (the category's class), else the
// documents group. A reader of neither sees each field as "changed"; a health
// event is not listed at all to an audience without the health group
// (ListPersonEvents), so this is its second line, not its first.
//
// The erased event names no field: its kinds — what each erase removed, by
// count — are the documents group's, and an empty payload to anyone else.
func shapePayload(stored []byte, classes []string, a audience) map[string]any {
	var payload struct {
		Fields map[string]any `json:"fields"`
		// What the retention run erased (an erased event): the kinds and
		// their counts, read by the documents group — the person and HR.
		Kinds []any `json:"kinds"`
		// Which employment, terms row or document an event is about: ids, not
		// data about the person, kept for everyone who reads the history.
		EmploymentID *int64 `json:"employmentId"`
		TermsID      *int64 `json:"termsId"`
		DocumentID   *int64 `json:"documentId"`
	}
	out := map[string]any{}
	if err := json.Unmarshal(stored, &payload); err != nil {
		return out
	}
	if payload.Fields == nil {
		if payload.Kinds != nil && a.reads(groupDocuments) {
			out["kinds"] = payload.Kinds
		}
		return out
	}
	if payload.EmploymentID != nil {
		out["employmentId"] = *payload.EmploymentID
	}
	if payload.TermsID != nil {
		out["termsId"] = *payload.TermsID
	}
	fields := make(map[string]any, len(payload.Fields))
	if payload.DocumentID != nil {
		out["documentId"] = *payload.DocumentID
		group := groupDocuments
		if slices.Contains(classes, "E") {
			group = groupHealth
		}
		for name, value := range payload.Fields {
			fields[name] = changed
			if a.reads(group) {
				fields[name] = value
			}
		}
		out["fields"] = fields
		return out
	}
	for name, value := range payload.Fields {
		switch g := groupOf(name); {
		case a.reads(g):
			fields[name] = value
		case g == groupPay || g == groupHRNotes:
		default:
			fields[name] = changed
		}
	}
	out["fields"] = fields
	return out
}

// eventResponse is one event as the audience may read it.
func eventResponse(row store.ListPersonEventsRow, a audience) gen.StaffEvent {
	display := unknownUserDisplay
	if row.ActorDisplay != nil {
		display = *row.ActorDisplay
	}
	return gen.StaffEvent{
		Id:      row.ID,
		Kind:    row.Kind,
		At:      row.At,
		Actor:   gen.StaffEventActor{Kind: row.ActorKind, DisplayName: display, UserId: row.ActorUserID},
		Payload: shapePayload(row.Payload, row.PayloadClasses, a),
	}
}

// userResponse is the linked user as the directory named them, or — for a
// user it no longer knows — "Unknown user", not active.
func userResponse(id uuid.UUID, name string, active bool) *gen.StaffPersonUser {
	return &gen.StaffPersonUser{Id: openapi_types.UUID(id), DisplayName: name, Active: active}
}

// judgedOn is the day an employment's derived dates are judged on: today,
// held inside the employment's own days — its start before it has started,
// its end once it has ended — so a future hire and a leaver answer the dates
// of their first and their last terms.
func judgedOn(e store.StaffEmployment, today time.Time) time.Time {
	if start := civil(e.StartDate); today.Before(start) {
		return start
	}
	if e.EndDate.Valid && today.After(e.EndDate.Time) {
		return e.EndDate.Time
	}
	return today
}

// timeDate is a calendar day as the contract's date.
func timeDate(t *time.Time) *openapi_types.Date {
	if t == nil {
		return nil
	}
	return &openapi_types.Date{Time: *t}
}

// employmentResponse is one employment as the audience may read it, with the
// dates the a-melding derives from chain (its terms, oldest first) judged on
// today.
func employmentResponse(e store.StaffEmployment, chain []store.StaffEmploymentTerm, today time.Time, a audience) gen.StaffEmployment {
	on := judgedOn(e, today)
	return gen.StaffEmployment{
		Id:               e.ID,
		PersonId:         e.PersonID,
		StartDate:        openapi_types.Date{Time: civil(e.StartDate)},
		Revision:         e.Revision,
		EndDate:          shown(a, "endDate", dateOf(e.EndDate)),
		FteChangedAt:     shown(a, "fteChangedAt", timeDate(fteChangedAt(chain, on))),
		EmploymentNumber: shown(a, "employmentNumber", &e.EmploymentNumber),
		RelationshipType: shown(a, "relationshipType", enumOf[gen.StaffRelationshipType](&e.RelationshipType)),
		BusinessUnitId:   shown(a, "businessUnitId", &e.BusinessUnitID),
		EndReason:        shown(a, "endReason", enumOf[gen.StaffEndReason](e.EndReason)),
		ContractSignedAt: shown(a, "contractSignedAt", dateOf(e.ContractSignedAt)),
		SalaryChangedAt:  shown(a, "salaryChangedAt", timeDate(salaryChangedAt(chain, on))),
	}
}

// numberOf is a numeric(_,2) column as the contract's number.
func numberOf(n pgtype.Numeric) float64 { return float64(hundredthsOfNumeric(n)) / 100 }

// int32Of is a smallint column as the contract's integer, nil for NULL.
func int32Of(v *int16) *int32 {
	if v == nil {
		return nil
	}
	out := int32(*v)
	return &out
}

// payComponentsOf is the stored pay components as the contract's list, nil
// when none are recorded.
func payComponentsOf(stored []byte) *[]gen.StaffPayComponent {
	if len(stored) == 0 {
		return nil
	}
	var list []storedPayComponent
	if json.Unmarshal(stored, &list) != nil {
		return nil
	}
	out := make([]gen.StaffPayComponent, 0, len(list))
	for _, c := range list {
		out = append(out, gen.StaffPayComponent{Name: c.Name, Amount: c.Amount, Currency: c.Currency, Period: gen.StaffPayPeriod(c.Period)})
	}
	return &out
}

// termsResponse is one terms row as the audience may read it. validTo is the
// last day it is in force, nil for the latest row.
func termsResponse(t store.StaffEmploymentTerm, validTo *time.Time, a audience) gen.StaffTerms {
	hours := numberOf(t.FullTimeHoursPerWeek)
	agreed := t.ProbationExtensionAgreed
	return gen.StaffTerms{
		Id:                        t.ID,
		EmploymentId:              t.EmploymentID,
		ValidFrom:                 openapi_types.Date{Time: civil(t.ValidFrom)},
		EmploymentType:            gen.StaffEmploymentType(t.EmploymentType),
		FtePercent:                numberOf(t.FtePercent),
		Revision:                  t.Revision,
		ValidTo:                   shown(a, "validTo", timeDate(validTo)),
		TemporaryGround:           shown(a, "temporaryGround", enumOf[gen.StaffTemporaryGround](t.TemporaryGround)),
		ExpectedEndDate:           shown(a, "expectedEndDate", dateOf(t.ExpectedEndDate)),
		AppointmentForm:           shown(a, "appointmentForm", enumOf[gen.StaffAppointmentForm](&t.AppointmentForm)),
		HiredOutTo:                shown(a, "hiredOutTo", t.HiredOutTo),
		WorkingHoursScheme:        shown(a, "workingHoursScheme", enumOf[gen.StaffWorkingHoursScheme](&t.WorkingHoursScheme)),
		FullTimeHoursPerWeek:      shown(a, "fullTimeHoursPerWeek", &hours),
		OccupationCode:            shown(a, "occupationCode", t.OccupationCode),
		Title:                     shown(a, "title", t.Title),
		EmployeeType:              shown(a, "employeeType", enumOf[gen.StaffEmployeeType](t.EmployeeType)),
		RemunerationType:          shown(a, "remunerationType", enumOf[gen.StaffRemunerationType](t.RemunerationType)),
		PayComponents:             shown(a, "payComponents", payComponentsOf(t.PayComponents)),
		PayMethod:                 shown(a, "payMethod", t.PayMethod),
		PaydayText:                shown(a, "paydayText", t.PaydayText),
		DepartmentId:              shown(a, "departmentId", t.DepartmentID),
		LocationId:                shown(a, "locationId", t.LocationID),
		ManagerPersonId:           shown(a, "managerPersonId", t.ManagerPersonID),
		CostCentre:                shown(a, "costCentre", t.CostCentre),
		Division:                  shown(a, "division", t.Division),
		WorkingTimeText:           shown(a, "workingTimeText", t.WorkingTimeText),
		BreakMinutes:              shown(a, "breakMinutes", int32Of(t.BreakMinutes)),
		SpecialHoursText:          shown(a, "specialHoursText", t.SpecialHoursText),
		OvertimeRuleText:          shown(a, "overtimeRuleText", t.OvertimeRuleText),
		ProbationEndDate:          shown(a, "probationEndDate", dateOf(t.ProbationEndDate)),
		ProbationExtensionAgreed:  shown(a, "probationExtensionAgreed", &agreed),
		ProbationExtendedTo:       shown(a, "probationExtendedTo", dateOf(t.ProbationExtendedTo)),
		ProbationExtensionReason:  shown(a, "probationExtensionReason", t.ProbationExtensionReason),
		NoticeEmployee:            shown(a, "noticeEmployee", int32Of(t.NoticeEmployee)),
		NoticeEmployer:            shown(a, "noticeEmployer", int32Of(t.NoticeEmployer)),
		NoticeUnit:                shown(a, "noticeUnit", enumOf[gen.StaffNoticeUnit](t.NoticeUnit)),
		TariffReference:           shown(a, "tariffReference", t.TariffReference),
		HolidayTermsText:          shown(a, "holidayTermsText", t.HolidayTermsText),
		CompetenceDevelopmentText: shown(a, "competenceDevelopmentText", t.CompetenceDevelopmentText),
		SocialSecurityText:        shown(a, "socialSecurityText", t.SocialSecurityText),
		SeniorityDate:             shown(a, "seniorityDate", dateOf(t.SeniorityDate)),
		PayGrade:                  shown(a, "payGrade", t.PayGrade),
		Note:                      shown(a, "note", t.Note),
	}
}

// termsListResponse is an employment's chain as the audience may read it,
// each row in force until the day before the next one's.
func termsListResponse(chain []store.StaffEmploymentTerm, a audience) []gen.StaffTerms {
	out := make([]gen.StaffTerms, 0, len(chain))
	for i, t := range chain {
		var to *time.Time
		if i+1 < len(chain) {
			d := civil(chain[i+1].ValidFrom).AddDate(0, 0, -1)
			to = &d
		}
		out = append(out, termsResponse(t, to, a))
	}
	return out
}
