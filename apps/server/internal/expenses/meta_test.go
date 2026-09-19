package expenses_test

import (
	"slices"
	"testing"
)

// seededCategories are the categories design §3.3 says an installation starts
// with, in the order it names them. The migration writes them; meta and the
// category list both answer them in this order.
var seededCategories = []string{
	"Materials", "Subcontractor", "Equipment hire", "Travel",
	"Accommodation", "Meals", "Phone and internet", "Other",
}

func TestExpensesMeta_WithProjects_SaysSoAndCarriesWhatAFormNeeds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// A project manager holding nothing but expenses:access: the app is open
	// to them, and the capabilities say what they may not do.
	member, _ := signInAs(t, h, projectKraftVerket, roleManager)

	meta := getMeta(t, member)
	if !meta.ProjectsAvailable {
		t.Error("projectsAvailable = false with the projects module enabled, want true")
	}
	if meta.DefaultCurrency != "NOK" || meta.DefaultMarkupPercent != 0 {
		t.Errorf("defaults = %s / %v, want NOK / 0", meta.DefaultCurrency, meta.DefaultMarkupPercent)
	}
	if meta.LockedBefore != nil || meta.ReceiptRequiredOver != nil {
		t.Errorf("a fresh installation: lockedBefore %v receiptRequiredOver %v, want neither",
			meta.LockedBefore, meta.ReceiptRequiredOver)
	}
	if got := categoryNames(meta.Categories); !slices.Equal(got, seededCategories) {
		t.Errorf("categories = %v, want %v", got, seededCategories)
	}
	if c := meta.Capabilities; c.CanApprove || c.CanViewAll || c.CanManage {
		t.Errorf("capabilities of an access-only caller = %+v, want all false", c)
	}
}

func TestExpensesMeta_WithoutProjects_SaysSoAndIsOtherwiseTheSame(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutProjects(t)
	c, _ := signIn(t, h)

	meta := getMeta(t, c)
	if meta.ProjectsAvailable {
		t.Error("projectsAvailable = true without the projects module, want false")
	}
	if meta.DefaultCurrency != "NOK" {
		t.Errorf("defaultCurrency = %q, want NOK — the rest of meta does not depend on projects", meta.DefaultCurrency)
	}
	if got := categoryNames(meta.Categories); !slices.Equal(got, seededCategories) {
		t.Errorf("categories = %v, want %v", got, seededCategories)
	}
}

func TestExpensesMeta_NamesEveryPermissionTheCallerHolds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	approver, _ := signIn(t, h, "expenses:approve")
	if c := getMeta(t, approver).Capabilities; !c.CanApprove || c.CanViewAll || c.CanManage {
		t.Errorf("an approver's capabilities = %+v, want canApprove alone", c)
	}
	viewer, _ := signIn(t, h, "expenses:view-all")
	if c := getMeta(t, viewer).Capabilities; c.CanApprove || !c.CanViewAll || c.CanManage {
		t.Errorf("a view-all holder's capabilities = %+v, want canViewAll alone", c)
	}
	admin, _ := signIn(t, h, "expenses:manage")
	if c := getMeta(t, admin).Capabilities; c.CanApprove || c.CanViewAll || !c.CanManage {
		t.Errorf("an administrator's capabilities = %+v, want canManage alone", c)
	}
}

// Meta is the one read the app makes before it draws anything, so the settings
// it carries must be the ones in force, not the ones it shipped with.
func TestExpensesMeta_FollowsTheSettingsAndTheCategories(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")

	putSettings(t, admin, settingsBody(map[string]any{
		"defaultCurrency":      "EUR",
		"defaultMarkupPercent": 12.5,
		"lockedBefore":         "2026-08-31",
		"receiptRequiredOver":  500,
	}))
	createCategory(t, admin, map[string]any{"name": "Parking"})

	meta := getMeta(t, admin)
	if meta.DefaultCurrency != "EUR" || meta.DefaultMarkupPercent != 12.5 {
		t.Errorf("defaults = %s / %v, want EUR / 12.5", meta.DefaultCurrency, meta.DefaultMarkupPercent)
	}
	if meta.LockedBefore == nil || *meta.LockedBefore != "2026-08-31" {
		t.Errorf("lockedBefore = %v, want the lock that was just set", meta.LockedBefore)
	}
	if meta.ReceiptRequiredOver == nil || *meta.ReceiptRequiredOver != 500 {
		t.Errorf("receiptRequiredOver = %v, want 500", meta.ReceiptRequiredOver)
	}
	if got := categoryNames(meta.Categories); !slices.Contains(got, "Parking") {
		t.Errorf("categories = %v, want the new one among them", got)
	}
}
