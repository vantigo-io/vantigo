package staff

import (
	"reflect"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// Every field a person's response or request carries, and every field an
// event payload names, has a group in the one table: a field added to the
// contract without one would be shaped by nobody's rule. id, capabilities
// and revision are the response's own, not data about the person.
func TestFieldGroups_CoverEveryPersonField(t *testing.T) {
	t.Parallel()
	own := map[string]bool{
		"id": true, "capabilities": true, "revision": true,
		// an employment's and a terms row's own: which person and which
		// employment they belong to, the request's nested first row and the
		// correction flag.
		"personId": true, "employmentId": true, "terms": true, "correction": true,
	}
	for _, typ := range []reflect.Type{
		reflect.TypeOf(gen.StaffPerson{}), reflect.TypeOf(gen.StaffPersonRequest{}), reflect.TypeOf(gen.StaffMeRequest{}),
		reflect.TypeOf(gen.StaffEmployment{}), reflect.TypeOf(gen.StaffEmploymentRequest{}), reflect.TypeOf(gen.StaffEmploymentChangeRequest{}),
		reflect.TypeOf(gen.StaffEmploymentEndRequest{}), reflect.TypeOf(gen.StaffTerms{}), reflect.TypeOf(gen.StaffTermsRequest{}),
	} {
		for i := range typ.NumField() {
			name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
			if own[name] {
				continue
			}
			if _, ok := fieldGroups[name]; !ok {
				t.Errorf("%s.%s has no field group", typ.Name(), name)
			}
		}
	}
	for _, name := range []string{"nextOfKin", "roles"} {
		if _, ok := fieldGroups[name]; !ok {
			t.Errorf("the event field %s has no field group", name)
		}
	}
	if g := groupOf("noSuchField"); g != groupNobody {
		t.Errorf("an unknown field's group = %v, want groupNobody, which no audience reads", g)
	}
}

// The groups each audience reads (design §5's field group × audience matrix,
// with HR's notes their own group, and the employment group split into the
// dates, type and percentage a colleague reads and the rest): a table test,
// so a change to who reads what is a change to this table.
func TestAudience_ReadsItsGroups(t *testing.T) {
	t.Parallel()
	groups := []fieldGroup{groupWork, groupEmploymentDates, groupEmployment, groupPrivate, groupHRNotes, groupPay, groupNationalID, groupBankAccount, groupLinks, groupNobody}
	for _, c := range []struct {
		name   string
		a      audience
		reads  []fieldGroup
		events bool
	}{
		{"nobody", audience{}, nil, false},
		{"self", audience{Self: true}, []fieldGroup{groupWork, groupEmploymentDates, groupEmployment, groupPrivate, groupPay, groupNationalID, groupBankAccount, groupLinks}, true},
		{"manager", audience{Manager: true}, []fieldGroup{groupWork, groupEmploymentDates, groupEmployment}, true},
		{"colleague", audience{Colleague: true}, []fieldGroup{groupWork, groupEmploymentDates}, false},
		{"staff:manage", audience{Manage: true}, []fieldGroup{groupWork, groupEmploymentDates, groupEmployment, groupPrivate, groupHRNotes, groupPay, groupNationalID, groupBankAccount, groupLinks}, true},
		{"staff:personal-data", audience{PersonalData: true}, []fieldGroup{groupWork, groupEmploymentDates, groupEmployment, groupPrivate, groupHRNotes, groupPay, groupNationalID, groupBankAccount, groupLinks}, true},
	} {
		for _, g := range groups {
			want := false
			for _, r := range c.reads {
				want = want || r == g
			}
			if got := c.a.reads(g); got != want {
				t.Errorf("%s reads %v = %v, want %v", c.name, g, got, want)
			}
		}
		if got := c.a.seesEvents(); got != c.events {
			t.Errorf("%s seesEvents = %v, want %v", c.name, got, c.events)
		}
		if got := c.a.seesPerson(); got != (len(c.reads) > 0) {
			t.Errorf("%s seesPerson = %v, want %v", c.name, got, len(c.reads) > 0)
		}
	}
}

// An event payload's fields are kept for a group the audience reads and
// become "changed" for one it does not — an unknown field included, which no
// audience reads — except the pay and HR's notes, which are left out
// altogether: even the day a pay field changed is a pay date.
func TestShapePayload_PerField(t *testing.T) {
	t.Parallel()
	stored := []byte(`{"fields":{"notes":{"from":"a","to":"b"},"givenName":{"to":"Kari"},"phoneMobile":{"from":"1"},"mystery":{"to":"x"}}}`)
	self := shapePayload(stored, audience{Self: true})
	fields := self["fields"].(map[string]any)
	if _, ok := fields["notes"]; ok || fields["mystery"] != "changed" {
		t.Errorf("self: %v, want notes left out and mystery changed", fields)
	}
	if f, ok := fields["phoneMobile"].(map[string]any); !ok || f["from"] != "1" {
		t.Errorf("self: phoneMobile = %v, want its value", fields["phoneMobile"])
	}
	hr := shapePayload(stored, audience{Manage: true})["fields"].(map[string]any)
	if f, ok := hr["notes"].(map[string]any); !ok || f["to"] != "b" {
		t.Errorf("HR: notes = %v, want its value", hr["notes"])
	}
}

// The sealed fields' and the link's events name their field — nationalId,
// bankAccount, user — and are shaped by its group like any other: the person
// and HR read the kind, the last four digits and the user; a manager, who
// reads the work group alone, reads "changed". A reveal carries no field, so
// every audience that reads the history reads the same empty payload.
func TestShapePayload_TheSealedAndLinkEvents(t *testing.T) {
	t.Parallel()
	for _, stored := range []string{
		`{"fields":{"nationalId":{"to":{"kind":"fodselsnummer"}}}}`,
		`{"fields":{"bankAccount":{"from":{"kind":"bban","last4":"8903"},"to":{"kind":"iban","last4":"7947"}}}}`,
		`{"fields":{"user":{"to":{"id":"00000000-0000-0000-0000-000000000001","displayName":"Kari"}}}}`,
	} {
		for name, a := range map[string]audience{"self": {Self: true}, "staff:manage": {Manage: true}, "staff:personal-data": {PersonalData: true}} {
			for field, value := range shapePayload([]byte(stored), a)["fields"].(map[string]any) {
				if value == changed {
					t.Errorf("%s reads %s as changed in %s, want its value", name, field, stored)
				}
			}
		}
		for field, value := range shapePayload([]byte(stored), audience{Manager: true})["fields"].(map[string]any) {
			if value != changed {
				t.Errorf("a manager reads %s as %v in %s, want changed", field, value, stored)
			}
		}
	}
	if got := shapePayload([]byte(`{}`), audience{Self: true}); len(got) != 0 {
		t.Errorf("a reveal's payload = %v, want empty", got)
	}
}

// A terms row's event names its fields as the contract does, and a manager
// reads the non-pay ones and nothing of the pay and HR's note; the
// payload's employmentId and termsId — which row, not data about the person —
// are kept for everyone who reads the history.
func TestShapePayload_TermsEvents(t *testing.T) {
	t.Parallel()
	stored := []byte(`{"employmentId":1001,"termsId":1002,"fields":{"title":{"to":"Developer"},"ftePercent":{"from":100,"to":80},"payComponents":{"to":[]},"note":{"to":"x"}}}`)
	manager := shapePayload(stored, audience{Manager: true})
	fields := manager["fields"].(map[string]any)
	if _, ok := fields["payComponents"]; ok {
		t.Errorf("manager: %v, want payComponents left out", fields)
	}
	if _, ok := fields["note"]; ok {
		t.Errorf("manager: %v, want note left out", fields)
	}
	for _, name := range []string{"title", "ftePercent"} {
		if _, ok := fields[name].(map[string]any); !ok {
			t.Errorf("manager: %s = %v, want its value", name, fields[name])
		}
	}
	if manager["employmentId"] != int64(1001) || manager["termsId"] != int64(1002) {
		t.Errorf("manager: %v, want the employment and the row named", manager)
	}
	hr := shapePayload(stored, audience{Manage: true})["fields"].(map[string]any)
	if _, ok := hr["payComponents"].(map[string]any); !ok {
		t.Errorf("HR: payComponents = %v, want its value", hr["payComponents"])
	}
}
