package expenses_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

func TestExpensesCategories_TheSeededOnesAreThereInOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employee, _ := signIn(t, h)

	got := listCategories(t, employee)
	if names := categoryNames(got); !slices.Equal(names, seededCategories) {
		t.Fatalf("categories = %v, want %v", names, seededCategories)
	}
	for i, c := range got {
		if !c.Active {
			t.Errorf("%q ships inactive, want every seeded category active", c.Name)
		}
		if c.Position != int32(i+1) {
			t.Errorf("%q sits at %d, want %d", c.Name, c.Position, i+1)
		}
	}
}

func TestExpensesCategories_AddRenameAndDeactivate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")

	added := createCategory(t, admin, map[string]any{"name": "  Parking  "})
	if added.Name != "Parking" {
		t.Errorf("name = %q, want it trimmed", added.Name)
	}
	if !added.Active {
		t.Error("a new category is inactive, want it active unless the caller said otherwise")
	}
	if added.Position != int32(len(seededCategories)+1) {
		t.Errorf("position = %d, want a category with no position to go last", added.Position)
	}

	renamed := updateCategory(t, admin, added.Id, map[string]any{"name": "Parking and tolls", "active": true, "position": 2})
	if renamed.Name != "Parking and tolls" || renamed.Position != 2 {
		t.Errorf("renamed = %+v, want the new name and position", renamed)
	}

	deactivated := updateCategory(t, admin, added.Id, map[string]any{"name": "Parking and tolls", "active": false, "position": 2})
	if deactivated.Active {
		t.Error("the category is still active, want it deactivated")
	}

	// It is still listed — an expense booked on it keeps its name — and it
	// now sits where its position says.
	listed := listCategories(t, admin)
	if len(listed) != len(seededCategories)+1 {
		t.Fatalf("categories = %v, want the deactivated one still listed", categoryNames(listed))
	}
	if listed[1].Name != "Parking and tolls" {
		t.Errorf("categories = %v, want the moved category second", categoryNames(listed))
	}
}

func TestExpensesCategories_ANameIsTakenHoweverItIsCased(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")

	errs := refused(t, admin, http.MethodPost, categoriesPath, map[string]any{"name": "travel"}, "Invalid category")
	if len(errs["name"]) == 0 {
		t.Errorf("adding a second Travel: errors = %v, want one on name", errs)
	}

	// A category already in use cannot be renamed onto another's name either.
	added := createCategory(t, admin, map[string]any{"name": "Parking"})
	errs = refused(t, admin, http.MethodPut, categoryPath(added.Id),
		map[string]any{"name": "TRAVEL", "active": true, "position": 9}, "Invalid category")
	if len(errs["name"]) == 0 {
		t.Errorf("renaming onto Travel: errors = %v, want one on name", errs)
	}

	// Its own name is not a duplicate of itself.
	updateCategory(t, admin, added.Id, map[string]any{"name": "parking", "active": true, "position": 9})
}

func TestExpensesCategories_RefusesANameItCannotStore(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")

	for name, body := range map[string]map[string]any{
		"no name":         {"name": ""},
		"a blank name":    {"name": "   "},
		"a name too long": {"name": strings.Repeat("a", 101)},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			errs := refused(t, admin, http.MethodPost, categoriesPath, body, "Invalid category")
			if len(errs["name"]) == 0 {
				t.Errorf("errors = %v, want one on name", errs)
			}
		})
	}
}

func TestExpensesCategories_AnUnknownIdIsA404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")

	body := map[string]any{"name": "Nothing", "active": true, "position": 1}
	if r := admin.Do(http.MethodPut, categoryPath(999_999), body); r.Status != http.StatusNotFound {
		t.Errorf("change a category nobody has: status %d body %s, want 404", r.Status, r.Body)
	}
}

// Everyone who may use the app reads the categories — the expense form needs
// them — and only an administrator changes them.
func TestExpensesCategories_ChangingThemNeedsManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employee, _ := signIn(t, h)

	listCategories(t, employee)
	if r := employee.Do(http.MethodPost, categoriesPath, map[string]any{"name": "Parking"}); r.Status != http.StatusForbidden {
		t.Errorf("an employee adding a category: status %d body %s, want 403", r.Status, r.Body)
	}
	body := map[string]any{"name": "Parking", "active": true, "position": 1}
	if r := employee.Do(http.MethodPut, categoryPath(1001), body); r.Status != http.StatusForbidden {
		t.Errorf("an employee changing a category: status %d body %s, want 403", r.Status, r.Body)
	}
}

// A position is an administrator-visible setting, so it is held to a rule: the
// picker's slots are 1-based, the way projects numbers a task among its
// siblings. Anything below one would sort a category ahead of the ones the
// product ships with for no reason a person could see.
func TestExpensesCategories_RefuseAPositionBelowOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")

	for name, body := range map[string]map[string]any{
		"a create at zero":     {"name": "Parkering", "position": 0},
		"a create below zero":  {"name": "Bompenger", "position": -5},
		"a replace below zero": {"name": "Materials", "active": true, "position": -1},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			method, path := http.MethodPost, categoriesPath
			if _, ok := body["active"]; ok {
				method, path = http.MethodPut, categoryPath(materialsCategory)
			}
			errs := refused(t, admin, method, path, body, "Invalid category")
			if len(errs["position"]) == 0 {
				t.Errorf("errors = %v, want one on position", errs)
			}
		})
	}

	// One is the first slot and is accepted, and a category with no position
	// still goes last.
	first := createCategory(t, admin, map[string]any{"name": "Ferge", "position": 1})
	if first.Position != 1 {
		t.Errorf("position = %d, want the first slot", first.Position)
	}
	last := createCategory(t, admin, map[string]any{"name": "Parkering"})
	if last.Position <= int32(len(seededCategories)) {
		t.Errorf("position = %d, want a category with none to go last", last.Position)
	}
}

// denseFrom1 fails the test unless every category's position is its own place
// in the list: a dense 1..n with no gap and no two rows sharing a number. It
// is asked of the database rather than of the response, because the listing
// would hide a tie by breaking it on the name.
func denseFrom1(t *testing.T, h *harness, when string) {
	t.Helper()
	if n := h.Count(t, `
		SELECT count(*) FROM (
			SELECT position, row_number() OVER (ORDER BY position, name) AS place
			FROM expenses.categories
		) ordered WHERE position <> place`); n != 0 {
		t.Errorf("%s: %d categories do not sit at their own place: %s", when, n, categoryPositions(t, h))
	}
	total := h.Count(t, `SELECT count(*) FROM expenses.categories`)
	if distinct := h.Count(t, `SELECT count(DISTINCT position) FROM expenses.categories`); distinct != total {
		t.Errorf("%s: %d categories share %d positions: %s", when, total, distinct, categoryPositions(t, h))
	}
}

// categoryPositions is the table as it actually stands, for a failure message
// that says what happened.
func categoryPositions(t *testing.T, h *harness) string {
	t.Helper()
	return modtest.One[string](t, h.Harness, `
		SELECT string_agg(format('%s=%s', position, name), ' ' ORDER BY position, name)
		FROM expenses.categories`)
}

// The picker's order is the server's to keep. A client moving a category sends
// the slot it should land in, and every other category shuffles around it —
// storing the number and leaving the rest alone would let two categories share
// a slot, and then a "move up" that ties with its neighbour would not move at
// all, because the listing breaks the tie by name.
func TestExpensesCategories_MovingOneMovesIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")

	// Materials Subcontractor "Equipment hire" Travel Accommodation Meals
	// "Phone and internet" Other — the seeded eight, 1..8.
	move := func(name string, to int32) {
		t.Helper()
		var id int32
		for _, c := range listCategories(t, admin) {
			if c.Name == name {
				id = c.Id
			}
		}
		if id == 0 {
			t.Fatalf("no category is called %q", name)
		}
		moved := updateCategory(t, admin, id, map[string]any{"name": name, "active": true, "position": to})
		if moved.Position != to && to <= int32(len(seededCategories)) {
			t.Errorf("%q answered position %d, want %d", name, moved.Position, to)
		}
	}

	// Down: Travel (4) to 6 puts it after Meals.
	move("Travel", 6)
	want := []string{
		"Materials", "Subcontractor", "Equipment hire", "Accommodation", "Meals",
		"Travel", "Phone and internet", "Other",
	}
	if got := categoryNames(listCategories(t, admin)); !slices.Equal(got, want) {
		t.Fatalf("after moving Travel down: %v, want %v", got, want)
	}
	denseFrom1(t, h, "after moving down")

	// Up by one — the move a "move up" button makes, and the one that used to
	// tie with its neighbour and stay put.
	move("Meals", 4)
	want = []string{
		"Materials", "Subcontractor", "Equipment hire", "Meals", "Accommodation",
		"Travel", "Phone and internet", "Other",
	}
	if got := categoryNames(listCategories(t, admin)); !slices.Equal(got, want) {
		t.Fatalf("after moving Meals up one: %v, want %v", got, want)
	}
	denseFrom1(t, h, "after moving up")

	// To the very front, and past the very end — a picker dragged to the
	// bottom sends whatever number the list happened to have.
	move("Other", 1)
	if got := categoryNames(listCategories(t, admin)); got[0] != "Other" {
		t.Errorf("after moving Other to 1: %v, want it first", got)
	}
	denseFrom1(t, h, "after moving to the front")

	move("Materials", 99)
	got := categoryNames(listCategories(t, admin))
	if got[len(got)-1] != "Materials" {
		t.Errorf("after moving Materials past the end: %v, want it last", got)
	}
	denseFrom1(t, h, "after moving past the end")

	// A replace that leaves the position where it was renames and nothing else.
	before := categoryNames(listCategories(t, admin))
	for _, c := range listCategories(t, admin) {
		if c.Name == "Meals" {
			updateCategory(t, admin, c.Id, map[string]any{"name": "Mat", "active": true, "position": c.Position})
		}
	}
	after := categoryNames(listCategories(t, admin))
	for i := range before {
		if before[i] == "Meals" {
			before[i] = "Mat"
		}
	}
	if !slices.Equal(after, before) {
		t.Errorf("a rename at the same position reordered the list: %v, want %v", after, before)
	}
	denseFrom1(t, h, "after a rename in place")
}

// A create with a position inserts there rather than landing on top of
// whatever already sits in that slot.
func TestExpensesCategories_ACreateWithAPositionInsertsThere(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")

	added := createCategory(t, admin, map[string]any{"name": "Parkering", "position": 3})
	if added.Position != 3 {
		t.Errorf("position = %d, want the slot it asked for", added.Position)
	}
	got := categoryNames(listCategories(t, admin))
	want := []string{
		"Materials", "Subcontractor", "Parkering", "Equipment hire", "Travel",
		"Accommodation", "Meals", "Phone and internet", "Other",
	}
	if !slices.Equal(got, want) {
		t.Errorf("categories = %v, want %v", got, want)
	}
	denseFrom1(t, h, "after inserting in the middle")

	// And one with no position still goes last, without disturbing anything.
	last := createCategory(t, admin, map[string]any{"name": "Ferge"})
	if last.Position != int32(len(seededCategories)+2) {
		t.Errorf("position = %d, want it appended", last.Position)
	}
	denseFrom1(t, h, "after appending")
}

// A category nobody may choose any more keeps its place: deactivating one must
// not shuffle the picker, and the numbering counts it like any other row.
func TestExpensesCategories_ADeactivatedOneKeepsItsPlace(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")

	var travel int32
	for _, c := range listCategories(t, admin) {
		if c.Name == "Travel" {
			travel = c.Id
		}
	}
	off := updateCategory(t, admin, travel, map[string]any{"name": "Travel", "active": false, "position": 4})
	if off.Active || off.Position != 4 {
		t.Errorf("deactivated = %+v, want it inactive and still fourth", off)
	}
	denseFrom1(t, h, "after deactivating one")

	// A move around it counts it: Other to 5 lands after the inactive Travel,
	// not in its place.
	var other int32
	for _, c := range listCategories(t, admin) {
		if c.Name == "Other" {
			other = c.Id
		}
	}
	updateCategory(t, admin, other, map[string]any{"name": "Other", "active": true, "position": 5})
	want := []string{
		"Materials", "Subcontractor", "Equipment hire", "Travel", "Other",
		"Accommodation", "Meals", "Phone and internet",
	}
	if got := categoryNames(listCategories(t, admin)); !slices.Equal(got, want) {
		t.Errorf("categories = %v, want %v — the inactive one still holds slot 4", got, want)
	}
	denseFrom1(t, h, "after moving around an inactive one")
}

// Two administrators reordering at once: both moves take every category's row
// lock in one order, so they queue rather than interleave, and the list they
// leave is still a dense 1..n whichever went first.
func TestExpensesCategories_TwoMovesAtOnceLeaveADenseList(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")

	byName := map[string]int32{}
	for _, c := range listCategories(t, admin) {
		byName[c.Name] = c.Id
	}
	for round := range raceRounds {
		var first, second int
		race(
			func() {
				first = admin.Do(http.MethodPut, categoryPath(byName["Meals"]),
					map[string]any{"name": "Meals", "active": true, "position": 1}).Status
			},
			func() {
				second = admin.Do(http.MethodPut, categoryPath(byName["Travel"]),
					map[string]any{"name": "Travel", "active": true, "position": 8}).Status
			},
		)
		if first != http.StatusOK || second != http.StatusOK {
			t.Fatalf("round %d: %d and %d, want both to succeed", round, first, second)
		}
		denseFrom1(t, h, fmt.Sprintf("after round %d of two moves at once", round))
		if n := h.Count(t, `SELECT count(*) FROM expenses.categories`); n != len(seededCategories) {
			t.Fatalf("round %d: %d categories, want the eight seeded ones", round, n)
		}
	}
}
