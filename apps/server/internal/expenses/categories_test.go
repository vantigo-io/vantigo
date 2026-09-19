package expenses_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
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
