package staff

import (
	"encoding/json"

	"github.com/google/uuid"
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
	groupPrivate
	groupHRNotes
	groupPay // the terms' pay (Task 4)
	groupNationalID
	groupBankAccount
	groupLinks // the account flag and the self-service link
)

func (g fieldGroup) String() string {
	return [...]string{"nobody", "work", "private", "hr-notes", "pay", "national-id", "bank-account", "links"}[g]
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
}

// groupOf is a field's group, groupNobody for a field the table does not
// name.
func groupOf(field string) fieldGroup {
	if g, ok := fieldGroups[field]; ok {
		return g
	}
	return groupNobody
}

// reads reports whether the audience reads group g — §5's matrix. The work
// group is everyone's who may see the person; the private group, the pay,
// the national identity number's kind, the bank account's last four digits
// and the link are the person's own and HR's; HR's notes are HR's alone.
func (a audience) reads(g fieldGroup) bool {
	hr := a.Manage || a.PersonalData
	switch g {
	case groupWork:
		return a.Self || a.Manager || a.Colleague || hr
	case groupPrivate, groupPay, groupNationalID, groupBankAccount, groupLinks:
		return a.Self || hr
	case groupHRNotes:
		return hr
	}
	return false
}

// seesPerson is whether the person exists for this audience at all; to
// anyone else they are the 404 an unknown id is.
func (a audience) seesPerson() bool { return a.reads(groupWork) }

// seesEvents is §5's Events row: the person, their manager and HR — never a
// colleague.
func (a audience) seesEvents() bool {
	return a.Self || a.Manager || a.Manage || a.PersonalData
}

// show is whether the audience reads field.
func (a audience) show(field string) bool { return a.reads(groupOf(field)) }

// shown is v when the audience reads field, else nil — absent on the wire.
func shown[T any](a audience, field string, v *T) *T {
	if !a.show(field) {
		return nil
	}
	return v
}

// personResponse is one person as the audience may read them. user is the
// linked user as the directory named them, nil when there is no link; it is
// read outside any transaction, and only for an audience that reads the link.
func personResponse(row store.GetPersonRow, a audience, user *gen.StaffPersonUser) gen.StaffPerson {
	out := gen.StaffPerson{
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

// personListItemResponse is one person in the directory: the work group, which
// every audience that lists reads.
func personListItemResponse(row store.ListPeopleRow, a audience) gen.StaffPersonListItem {
	return gen.StaffPersonListItem{
		Id:                row.ID,
		EmployeeNumber:    row.EmployeeNumber,
		GivenName:         row.GivenName,
		FamilyName:        row.FamilyName,
		DisplayName:       row.DisplayName,
		WorkCountry:       row.WorkCountry,
		PreferredLanguage: gen.StaffLanguage(row.PreferredLanguage),
		WorkEmail:         shown(a, "workEmail", row.WorkEmail),
		PhoneWork:         shown(a, "phoneWork", row.PhoneWork),
	}
}

// changed is what a field of a group the audience may not read shows in an
// event's payload in place of its values.
const changed = "changed"

// shapePayload is a stored event payload as the audience may read it: each
// of its fields kept when the audience reads the field's group, "changed"
// otherwise. Every kind this module writes names its fields this way — the
// sealed fields' and the link's changes too, as nationalId, bankAccount and
// user, each shaped by its own group — and a reveal names none, so its
// payload is empty to everyone. A payload that is not the shape this module
// writes is answered empty rather than passed through: what is not
// understood is not shown.
func shapePayload(stored []byte, a audience) map[string]any {
	var payload struct {
		Fields map[string]any `json:"fields"`
	}
	out := map[string]any{}
	if err := json.Unmarshal(stored, &payload); err != nil || payload.Fields == nil {
		return out
	}
	fields := make(map[string]any, len(payload.Fields))
	for name, value := range payload.Fields {
		if a.show(name) {
			fields[name] = value
		} else {
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
		Payload: shapePayload(row.Payload, a),
	}
}

// userResponse is the linked user as the directory named them, or — for a
// user it no longer knows — "Unknown user", not active.
func userResponse(id uuid.UUID, name string, active bool) *gen.StaffPersonUser {
	return &gen.StaffPersonUser{Id: openapi_types.UUID(id), DisplayName: name, Active: active}
}
