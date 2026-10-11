package staff

import "testing"

// The cycle check walks the parent graph from the proposed parent upward with
// a visited set: the department itself, or any department below it, as the
// parent is a cycle; anything else is not — and a graph that already holds a
// cycle (written past this module) still answers rather than looping.
func TestDepartmentCycle(t *testing.T) {
	t.Parallel()
	p := func(v int64) *int64 { return &v }
	// 1 ← 2 ← 3, and 4 alone; 7 ↔ 8 is a cycle somebody wrote by hand.
	parents := map[int64]*int64{1: nil, 2: p(1), 3: p(2), 4: nil, 7: p(8), 8: p(7)}
	for _, c := range []struct {
		name         string
		id, parentID int64
		want         bool
	}{
		{"itself", 2, 2, true},
		{"its child", 1, 2, true},
		{"its grandchild", 1, 3, true},
		{"its own parent", 3, 2, false},
		{"another tree", 1, 4, false},
		{"under a sibling's child", 4, 3, false},
		{"into a hand-made cycle", 4, 7, false},
		{"a new department", 0, 3, false},
	} {
		if got := departmentCycle(parents, c.id, c.parentID); got != c.want {
			t.Errorf("%s: departmentCycle(%d, %d) = %v, want %v", c.name, c.id, c.parentID, got, c.want)
		}
	}
}

// The If-Match header carries a revision bare or quoted, the way a client
// that treats it as an ETag sends it back; anything else is no revision.
func TestParseIfMatch(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]int32{"3": 3, `"3"`: 3, ` "12" `: 12, `W/"4"`: 4} {
		if got, ok := parseIfMatch(raw); !ok || got != want {
			t.Errorf("parseIfMatch(%q) = %d, %v, want %d", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"", "abc", `"3`, "-1", "0", "*", "99999999999"} {
		if _, ok := parseIfMatch(raw); ok {
			t.Errorf("parseIfMatch(%q) accepted it, want no revision", raw)
		}
	}
}
