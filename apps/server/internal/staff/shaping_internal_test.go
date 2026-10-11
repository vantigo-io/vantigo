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
	own := map[string]bool{"id": true, "capabilities": true, "revision": true}
	for _, typ := range []reflect.Type{reflect.TypeOf(gen.StaffPerson{}), reflect.TypeOf(gen.StaffPersonRequest{})} {
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

// The groups each audience reads (design §5's field group × audience matrix
// for the person, with HR's notes their own group): a table test, so a change
// to who reads what is a change to this table.
func TestAudience_ReadsItsGroups(t *testing.T) {
	t.Parallel()
	groups := []fieldGroup{groupWork, groupPrivate, groupHRNotes, groupPay, groupNationalID, groupBankAccount, groupLinks, groupNobody}
	for _, c := range []struct {
		name   string
		a      audience
		reads  []fieldGroup
		events bool
	}{
		{"nobody", audience{}, nil, false},
		{"self", audience{Self: true}, []fieldGroup{groupWork, groupPrivate, groupPay, groupNationalID, groupBankAccount, groupLinks}, true},
		{"manager", audience{Manager: true}, []fieldGroup{groupWork}, true},
		{"colleague", audience{Colleague: true}, []fieldGroup{groupWork}, false},
		{"staff:manage", audience{Manage: true}, []fieldGroup{groupWork, groupPrivate, groupHRNotes, groupPay, groupNationalID, groupBankAccount, groupLinks}, true},
		{"staff:personal-data", audience{PersonalData: true}, []fieldGroup{groupWork, groupPrivate, groupHRNotes, groupPay, groupNationalID, groupBankAccount, groupLinks}, true},
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
// audience reads.
func TestShapePayload_PerField(t *testing.T) {
	t.Parallel()
	stored := []byte(`{"fields":{"notes":{"from":"a","to":"b"},"givenName":{"to":"Kari"},"phoneMobile":{"from":"1"},"mystery":{"to":"x"}}}`)
	self := shapePayload(stored, audience{Self: true})
	fields := self["fields"].(map[string]any)
	if fields["notes"] != "changed" || fields["mystery"] != "changed" {
		t.Errorf("self: %v, want notes and mystery changed", fields)
	}
	if f, ok := fields["phoneMobile"].(map[string]any); !ok || f["from"] != "1" {
		t.Errorf("self: phoneMobile = %v, want its value", fields["phoneMobile"])
	}
	hr := shapePayload(stored, audience{Manage: true})["fields"].(map[string]any)
	if f, ok := hr["notes"].(map[string]any); !ok || f["to"] != "b" {
		t.Errorf("HR: notes = %v, want its value", hr["notes"])
	}
}
