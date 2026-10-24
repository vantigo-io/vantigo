package staff

import (
	"fmt"
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
	for _, typ := range []reflect.Type{reflect.TypeOf(gen.StaffPerson{}), reflect.TypeOf(gen.StaffPersonListItem{})} {
		for i := range typ.NumField() {
			name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
			if !own[name] && personGroupOf(name) == groupNobody {
				t.Errorf("%s.%s has no field group", typ.Name(), name)
			}
		}
	}
	for _, name := range []string{"nextOfKin", "roles"} {
		if _, ok := fieldGroups[name]; !ok {
			t.Errorf("the event field %s has no field group", name)
		}
	}
	// A person's competence row (staff competence design §5) has a table of
	// its own, since its validFrom and validTo are not an employment's: every
	// field but the row's own ids is the Competence group's or the detail's.
	competence := reflect.TypeOf(gen.StaffPersonCompetence{})
	for i := range competence.NumField() {
		name, _, _ := strings.Cut(competence.Field(i).Tag.Get("json"), ",")
		if name != "id" && name != "personId" && competenceFieldGroup(name) == groupNobody {
			t.Errorf("%s.%s has no field group", competence.Name(), name)
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
	groups := []fieldGroup{groupWork, groupEmploymentDates, groupEmployment, groupPrivate, groupHRNotes, groupPay, groupNationalID, groupBankAccount, groupLinks, groupDocuments, groupHealth, groupCompetence, groupCompetenceDetail, groupNobody}
	for _, c := range []struct {
		name   string
		a      audience
		reads  []fieldGroup
		events bool
	}{
		{"nobody", audience{}, nil, false},
		{"self", audience{Self: true}, []fieldGroup{groupWork, groupEmploymentDates, groupEmployment, groupPrivate, groupPay, groupNationalID, groupBankAccount, groupLinks, groupDocuments, groupHealth, groupCompetence, groupCompetenceDetail}, true},
		{"manager", audience{Manager: true}, []fieldGroup{groupWork, groupEmploymentDates, groupEmployment, groupCompetence, groupCompetenceDetail}, true},
		{"colleague", audience{Colleague: true}, []fieldGroup{groupWork, groupEmploymentDates, groupCompetence}, false},
		{"staff:approve", audience{Approver: true}, []fieldGroup{groupWork, groupEmploymentDates, groupEmployment, groupCompetence, groupCompetenceDetail}, false},
		{"staff:manage", audience{Manage: true}, []fieldGroup{groupWork, groupEmploymentDates, groupEmployment, groupPrivate, groupHRNotes, groupPay, groupNationalID, groupBankAccount, groupLinks, groupDocuments, groupCompetence, groupCompetenceDetail}, true},
		{"staff:personal-data", audience{PersonalData: true}, []fieldGroup{groupWork, groupEmploymentDates, groupEmployment, groupPrivate, groupHRNotes, groupPay, groupNationalID, groupBankAccount, groupLinks, groupDocuments, groupHealth, groupCompetence, groupCompetenceDetail}, true},
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

// The person's place today is the work group's (design §5's Work row): the
// title, the business unit and the location are read by every audience that
// sees the person, while a terms row's title stays the employment group's,
// which a colleague does not read.
func TestPersonFieldGroups_ThePlaceTodayIsWork(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"title", "businessUnit", "location", "department", "manager"} {
		if g := personGroupOf(field); g != groupWork {
			t.Errorf("the person's %s is %v, want work", field, g)
		}
	}
	if g := groupOf("title"); g != groupEmployment {
		t.Errorf("a terms row's title is %v, want employment", g)
	}
	for name, a := range map[string]audience{
		"self": {Self: true}, "manager": {Manager: true}, "colleague": {Colleague: true},
		"staff:manage": {Manage: true}, "staff:personal-data": {PersonalData: true},
	} {
		title := "Developer"
		if got := shownOnPerson(a, "title", &title); got == nil {
			t.Errorf("%s does not read the person's title", name)
		}
	}
	title := "Developer"
	if got := shown(audience{Colleague: true}, "title", &title); got != nil {
		t.Errorf("a colleague reads a terms row's title %q", *got)
	}
}

// An event payload's fields are kept for a group the audience reads and
// become "changed" for one it does not — an unknown field included, which no
// audience reads — except the pay and HR's notes, which are left out
// altogether: even the day a pay field changed is a pay date.
func TestShapePayload_PerField(t *testing.T) {
	t.Parallel()
	stored := []byte(`{"fields":{"notes":{"from":"a","to":"b"},"givenName":{"to":"Kari"},"phoneMobile":{"from":"1"},"mystery":{"to":"x"}}}`)
	self := shapePayload(stored, nil, audience{Self: true})
	fields := self["fields"].(map[string]any)
	if _, ok := fields["notes"]; ok || fields["mystery"] != "changed" {
		t.Errorf("self: %v, want notes left out and mystery changed", fields)
	}
	if f, ok := fields["phoneMobile"].(map[string]any); !ok || f["from"] != "1" {
		t.Errorf("self: phoneMobile = %v, want its value", fields["phoneMobile"])
	}
	hr := shapePayload(stored, nil, audience{Manage: true})["fields"].(map[string]any)
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
			for field, value := range shapePayload([]byte(stored), nil, a)["fields"].(map[string]any) {
				if value == changed {
					t.Errorf("%s reads %s as changed in %s, want its value", name, field, stored)
				}
			}
		}
		for field, value := range shapePayload([]byte(stored), nil, audience{Manager: true})["fields"].(map[string]any) {
			if value != changed {
				t.Errorf("a manager reads %s as %v in %s, want changed", field, value, stored)
			}
		}
	}
	if got := shapePayload([]byte(`{}`), nil, audience{Self: true}); len(got) != 0 {
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
	manager := shapePayload(stored, nil, audience{Manager: true})
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
	hr := shapePayload(stored, nil, audience{Manage: true})["fields"].(map[string]any)
	if _, ok := hr["payComponents"].(map[string]any); !ok {
		t.Errorf("HR: payComponents = %v, want its value", hr["payComponents"])
	}
}

// A document's event (design §3.6, plan Task 1) is shaped by one group for
// the whole event, chosen by the classes the category gave it: the documents
// group (Self, manage, personal-data), or the health group (Self,
// personal-data) when the classes hold E. A reader of the group sees each
// field; anyone else who reads the history sees "changed" — and the
// documentId either way, an id rather than data about the person.
func TestShapePayload_DocumentEvents(t *testing.T) {
	t.Parallel()
	stored := []byte(`{"documentId":7,"fields":{"category":{"to":"contract"},"fileName":{"to":"avtale.pdf"},"validTo":{"to":"2027-01-31"}}}`)
	for _, c := range []struct {
		name    string
		classes []string
		a       audience
		shown   bool
	}{
		{"a contract to HR", []string{"A"}, audience{Manage: true}, true},
		{"a contract to the person", []string{"A"}, audience{Self: true}, true},
		{"a contract to their manager", []string{"A"}, audience{Manager: true}, false},
		{"a health document to personal-data", []string{"E"}, audience{PersonalData: true}, true},
		{"a health document to the person", []string{"E"}, audience{Self: true}, true},
		{"a health document to HR", []string{"E"}, audience{Manage: true}, false},
		{"a move out of health to HR", []string{"D", "E"}, audience{Manage: true}, false},
	} {
		got := shapePayload(stored, c.classes, c.a)
		if got["documentId"] != int64(7) {
			t.Errorf("%s: documentId = %v, want 7", c.name, got["documentId"])
		}
		fields, _ := got["fields"].(map[string]any)
		if len(fields) != 3 {
			t.Errorf("%s: fields = %v, want the three named", c.name, fields)
		}
		for name, value := range fields {
			if isChanged := value == changed; isChanged == c.shown {
				t.Errorf("%s: %s = %v, want shown %v", c.name, name, value, c.shown)
			}
		}
	}
}

// The hold's reason is named legalHoldReason in a payload, so a "reason" of
// another kind — an absence's, in delivery B — cannot fall into the hold
// group or its class by accident: "reason" is no field the tables know.
func TestFieldGroups_ReasonIsNoHoldField(t *testing.T) {
	t.Parallel()
	if g := groupOf("reason"); g != groupNobody {
		t.Errorf("reason is in the %s group, want none", g)
	}
	if class, ok := fieldClasses["reason"]; ok {
		t.Errorf("reason has class %s, want none", class)
	}
	if groupOf("legalHoldReason") != groupHold || fieldClasses["legalHoldReason"] != "F" {
		t.Errorf("legalHoldReason = %s/%s, want the hold group and class F", groupOf("legalHoldReason"), fieldClasses["legalHoldReason"])
	}
}

// An accounts worker's event (Task 4b) is the links group's whole: the
// person, HR and staff:personal-data read its step, state, code, invitation
// and flagOffMeanwhile; a manager, an Approver and a colleague read an empty
// payload — the listing shows them none, and this is the second line.
func TestShapePayload_TheAccountEvents(t *testing.T) {
	t.Parallel()
	stored := []byte(`{"step":"activate","state":"pending","code":"x","invitation":{"state":"active","issued":1},"flagOffMeanwhile":true}`)
	want := map[string]any{"step": "activate", "state": "pending", "code": "x",
		"invitation": map[string]any{"state": "active", "issued": float64(1)}, "flagOffMeanwhile": true}
	for _, a := range []audience{{Self: true}, {Manage: true}, {PersonalData: true}} {
		if got := shapePayload(stored, []string{"C"}, a); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%+v reads %v, want %v", a, got, want)
		}
	}
	for _, a := range []audience{{Manager: true}, {Approver: true}, {Colleague: true}} {
		if got := shapePayload(stored, []string{"C"}, a); len(got) != 0 {
			t.Errorf("%+v reads %v, want nothing", a, got)
		}
	}
}

// A provider link's event answers the codes the provider reported, and
// pass_revealed its provider, to the links group — the person, staff:manage,
// staff:personal-data — and an empty payload to anyone else.
func TestShapePayload_TheProviderEvents(t *testing.T) {
	t.Parallel()
	external := []byte(`{"step":"activate","state":"enabled","codes":["hire_date_not_written","licence_unavailable:EMS"]}`)
	revealed := []byte(`{"provider":"entra"}`)
	for name, a := range map[string]audience{"self": {Self: true}, "staff:manage": {Manage: true}, "staff:personal-data": {PersonalData: true}} {
		got := shapePayload(external, accountEventClasses, a)
		if codes, ok := got["codes"].([]string); !ok || len(codes) != 2 || codes[1] != "licence_unavailable:EMS" || got["step"] != "activate" {
			t.Errorf("%s: the provider event = %v, want its step and both codes", name, got)
		}
		if got := shapePayload(revealed, accountEventClasses, a); got["provider"] != "entra" {
			t.Errorf("%s: pass_revealed = %v, want its provider", name, got)
		}
	}
	for name, a := range map[string]audience{"manager": {Manager: true}, "colleague": {Colleague: true}} {
		if got := shapePayload(external, accountEventClasses, a); len(got) != 0 {
			t.Errorf("%s: the provider event = %v, want nothing", name, got)
		}
		if got := shapePayload(revealed, accountEventClasses, a); len(got) != 0 {
			t.Errorf("%s: pass_revealed = %v, want nothing", name, got)
		}
	}
}

// A group event names the group and the source to the links group — the
// person, staff:manage, staff:personal-data, who read the Account tab's
// groups — and nothing to a manager, an Approver or a colleague, who keep
// the bare kind.
func TestShapePayload_TheGroupEvents(t *testing.T) {
	t.Parallel()
	stored := []byte(`{"groupId":1001,"groupName":"Sales","groupKey":"sales","source":"derived"}`)
	want := map[string]any{"groupId": int64(1001), "groupName": "Sales", "groupKey": "sales", "source": "derived"}
	for name, a := range map[string]audience{"self": {Self: true}, "staff:manage": {Manage: true}, "staff:personal-data": {PersonalData: true}} {
		if got := shapePayload(stored, []string{"C"}, a); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s reads %v, want %v", name, got, want)
		}
	}
	for name, a := range map[string]audience{"manager": {Manager: true}, "approver": {Approver: true}, "colleague": {Colleague: true}} {
		if got := shapePayload(stored, []string{"C"}, a); len(got) != 0 {
			t.Errorf("%s reads %v, want nothing", name, got)
		}
	}
}

// A competence event (staff competence design S16) names its row and is
// shaped field by field by the competence groups: every reader of the
// history reads the key, the level and the basis; a field the groups do not
// know reads "changed".
func TestShapePayload_TheCompetenceEvents(t *testing.T) {
	t.Parallel()
	stored := []byte(`{"competenceId":1001,"fields":{"competenceKey":{"to":"fse"},"level":{"from":2,"to":3},"basis":{"to":"observed"},"mystery":{"to":"x"}},"classes":{"competenceKey":"D","level":"D","basis":"D","mystery":"D"}}`)
	for name, a := range map[string]audience{"self": {Self: true}, "manager": {Manager: true}, "staff:manage": {Manage: true}} {
		got := shapePayload(stored, []string{"D"}, a)
		if got["competenceId"] != int64(1001) {
			t.Errorf("%s: competenceId = %v, want 1001", name, got["competenceId"])
		}
		fields := got["fields"].(map[string]any)
		for _, field := range []string{"competenceKey", "level", "basis"} {
			if _, ok := fields[field].(map[string]any); !ok {
				t.Errorf("%s: %s = %v, want its value", name, field, fields[field])
			}
		}
		if fields["mystery"] != changed {
			t.Errorf("%s: mystery = %v, want changed", name, fields["mystery"])
		}
	}
	colleague := shapePayload(stored, []string{"D"}, audience{Colleague: true})["fields"].(map[string]any)
	if colleague["basis"] != changed {
		t.Errorf("a colleague reads basis as %v, want changed", colleague["basis"])
	}
}
