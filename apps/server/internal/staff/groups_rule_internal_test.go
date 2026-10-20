package staff

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// MaterialiseGroups is materialiseGroups on a server over d, by the accounts
// worker's own actor, for the external tests of the materialisation — the
// worker's cycle calls it, and these tests call it on a day of their own.
func MaterialiseGroups(ctx context.Context, d module.Deps, today time.Time) error {
	return (&server{deps: d}).materialiseGroups(ctx, accountsWorkerRun.who, today)
}

// EvaluateRule is evaluateRule on d's pool, for the external tests that
// build a register and judge each kind on it.
func EvaluateRule(ctx context.Context, d module.Deps, rule gen.StaffGroupRule, today time.Time) ([]int64, error) {
	return evaluateRule(ctx, store.New(d.Pool), rule, today)
}

// A group's key is 1 to 60 lower-case letters, digits or hyphens, starting
// with a letter or a digit, set on create; a change leaves it out or sends
// the group's own, and another is a field error.
func TestParseGroup_Key(t *testing.T) {
	t.Parallel()
	ptr := func(s string) *string { return &s }
	current := &store.StaffGroup{Key: "sales"}
	cases := []struct {
		name    string
		key     *string
		current *store.StaffGroup
		ok      bool
	}{
		{"a letter", ptr("a"), nil, true},
		{"a digit first", ptr("1st-floor"), nil, true},
		{"sixty characters", ptr(strings.Repeat("a", 60)), nil, true},
		{"sixty-one characters", ptr(strings.Repeat("a", 61)), nil, false},
		{"left out on create", nil, nil, false},
		{"empty on create", ptr(""), nil, false},
		{"a hyphen first", ptr("-sales"), nil, false},
		{"upper case", ptr("Sales"), nil, false},
		{"an underscore", ptr("sales_team"), nil, false},
		{"a space", ptr("sales team"), nil, false},
		{"a letter beyond ASCII", ptr("salg-ø"), nil, false},
		{"left out on change", nil, current, true},
		{"its own on change", ptr("sales"), current, true},
		{"another on change", ptr("marketing"), current, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			parsed, f := parseGroup(gen.StaffGroupRequest{Key: c.key, Name: "Sales", Kind: gen.Listed}, c.current)
			_, refused := f.errs["key"]
			if refused == c.ok {
				t.Fatalf("key %v: field errors %v, want ok %v", c.key, f.errs, c.ok)
			}
			if c.ok && c.current != nil && parsed.Key != c.current.Key {
				t.Errorf("a change's key = %q, want the group's own %q", parsed.Key, c.current.Key)
			}
		})
	}
}

// A group's name is 1 to 120 characters and its description at most 500; a
// derived group needs a rule and a listed one refuses one; the kind is one
// of the two.
func TestParseGroup_Fields(t *testing.T) {
	t.Parallel()
	ptr := func(s string) *string { return &s }
	rule := &gen.StaffGroupRule{Kind: gen.Employed}
	cases := []struct {
		name  string
		body  gen.StaffGroupRequest
		field string
	}{
		{"no name", gen.StaffGroupRequest{Key: ptr("a"), Name: " ", Kind: gen.Listed}, "name"},
		{"a name too long", gen.StaffGroupRequest{Key: ptr("a"), Name: strings.Repeat("n", 121), Kind: gen.Listed}, "name"},
		{"a description too long", gen.StaffGroupRequest{Key: ptr("a"), Name: "N", Description: ptr(strings.Repeat("d", 501)), Kind: gen.Listed}, "description"},
		{"an unknown kind", gen.StaffGroupRequest{Key: ptr("a"), Name: "N", Kind: "dynamic"}, "kind"},
		{"a derived group with no rule", gen.StaffGroupRequest{Key: ptr("a"), Name: "N", Kind: gen.Derived}, "rule"},
		{"a listed group with a rule", gen.StaffGroupRequest{Key: ptr("a"), Name: "N", Kind: gen.Listed, Rule: rule}, "rule"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, f := parseGroup(c.body, nil)
			if _, ok := f.errs[c.field]; !ok || len(f.errs) != 1 {
				t.Fatalf("field errors %v, want one on %s", f.errs, c.field)
			}
		})
	}
	if _, f := parseGroup(gen.StaffGroupRequest{Key: ptr("a"), Name: strings.Repeat("n", 120), Description: ptr(strings.Repeat("d", 500)), Kind: gen.Derived, Rule: rule}, nil); f.failed() {
		t.Errorf("the longest name and description: %v, want none", f.errs)
	}
}

// A rule's static rules by kind: an id on the department, business unit,
// location and reportsTo kinds and on none other; a value on employeeType
// alone, one of the terms' employee types; includeBelow on department alone.
func TestParseGroupRule_PerKind(t *testing.T) {
	t.Parallel()
	id := int64(1001)
	yes, no := true, false
	value := func(s string) *string { return &s }
	cases := []struct {
		name  string
		rule  gen.StaffGroupRule
		field string // "" when it passes
	}{
		{"employed", gen.StaffGroupRule{Kind: gen.Employed}, ""},
		{"employed with an id", gen.StaffGroupRule{Kind: gen.Employed, Id: &id}, "rule.id"},
		{"department", gen.StaffGroupRule{Kind: gen.Department, Id: &id}, ""},
		{"department below", gen.StaffGroupRule{Kind: gen.Department, Id: &id, IncludeBelow: &yes}, ""},
		{"department without an id", gen.StaffGroupRule{Kind: gen.Department}, "rule.id"},
		{"businessUnit", gen.StaffGroupRule{Kind: gen.BusinessUnit, Id: &id}, ""},
		{"businessUnit without an id", gen.StaffGroupRule{Kind: gen.BusinessUnit}, "rule.id"},
		{"businessUnit below", gen.StaffGroupRule{Kind: gen.BusinessUnit, Id: &id, IncludeBelow: &yes}, "rule.includeBelow"},
		{"businessUnit below false", gen.StaffGroupRule{Kind: gen.BusinessUnit, Id: &id, IncludeBelow: &no}, ""},
		{"location", gen.StaffGroupRule{Kind: gen.Location, Id: &id}, ""},
		{"location below", gen.StaffGroupRule{Kind: gen.Location, Id: &id, IncludeBelow: &yes}, "rule.includeBelow"},
		{"reportsTo", gen.StaffGroupRule{Kind: gen.ReportsTo, Id: &id}, ""},
		{"reportsTo without an id", gen.StaffGroupRule{Kind: gen.ReportsTo}, "rule.id"},
		{"employeeType", gen.StaffGroupRule{Kind: gen.EmployeeType, Value: value("contractor")}, ""},
		{"employeeType unknown", gen.StaffGroupRule{Kind: gen.EmployeeType, Value: value("freelancer")}, "rule.value"},
		{"employeeType without a value", gen.StaffGroupRule{Kind: gen.EmployeeType}, "rule.value"},
		{"employeeType with an id", gen.StaffGroupRule{Kind: gen.EmployeeType, Value: value("intern"), Id: &id}, "rule.id"},
		{"a value on location", gen.StaffGroupRule{Kind: gen.Location, Id: &id, Value: value("x")}, "rule.value"},
		{"an unknown kind", gen.StaffGroupRule{Kind: "manager"}, "rule.kind"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			out, f := parseGroupRule(c.rule)
			if c.field == "" {
				if f.failed() {
					t.Fatalf("field errors %v, want none", f.errs)
				}
				if out.IncludeBelow != nil && !*out.IncludeBelow {
					t.Errorf("includeBelow false is stored, want it left out")
				}
				return
			}
			if _, ok := f.errs[c.field]; !ok || len(f.errs) != 1 {
				t.Fatalf("field errors %v, want one on %s", f.errs, c.field)
			}
		})
	}
}

// A department is beneath another when its parent chain reaches it; a loop
// written past the module ends the climb.
func TestDepartmentBeneath(t *testing.T) {
	t.Parallel()
	p := func(id int64) *int64 { return &id }
	parents := map[int64]*int64{1: nil, 2: p(1), 3: p(2), 4: nil, 5: p(6), 6: p(5)}
	cases := []struct {
		id, top int64
		want    bool
	}{
		{2, 1, true}, {3, 1, true}, {3, 2, true}, {1, 1, false}, {1, 2, false}, {4, 1, false}, {5, 1, false},
	}
	for _, c := range cases {
		if got := departmentBeneath(parents, c.id, c.top); got != c.want {
			t.Errorf("departmentBeneath(%d, %d) = %v, want %v", c.id, c.top, got, c.want)
		}
	}
}
