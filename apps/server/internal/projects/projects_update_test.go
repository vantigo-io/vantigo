package projects_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// PUT /api/v1/projects/{id}: the same §4.1 rules the create runs, plus the
// revision guard and the timeline entries an update owes. D12's shaping is
// what makes the billing entry's payload interesting: it records *which*
// financial fields changed and never their values, so the timeline itself
// needs no shaping.

// updateBody is the update request a project round-trips into: every field
// as it currently stands, carrying the revision the caller read, which a
// test then overrides one field of. A nil override value removes that field,
// the same convention createBody uses.
func updateBody(p projectJSON, overrides map[string]any) map[string]any {
	body := map[string]any{
		"code":        p.Code,
		"name":        p.Name,
		"billingType": p.BillingType,
		"revision":    p.Revision,
	}
	if p.Description != nil {
		body["description"] = *p.Description
	}
	if p.CustomerId != nil {
		body["customerId"] = *p.CustomerId
	}
	if p.StartDate != nil {
		body["startDate"] = *p.StartDate
	}
	if p.EndDate != nil {
		body["endDate"] = *p.EndDate
	}
	if p.BudgetHours != nil {
		body["budgetHours"] = *p.BudgetHours
	}
	if p.Financials != nil {
		if p.Financials.Currency != nil {
			body["currency"] = *p.Financials.Currency
		}
		if p.Financials.FixedPriceAmount != nil {
			body["fixedPriceAmount"] = *p.Financials.FixedPriceAmount
		}
		if p.Financials.BudgetAmount != nil {
			body["budgetAmount"] = *p.Financials.BudgetAmount
		}
		if p.Financials.DefaultBillRate != nil {
			body["defaultBillRate"] = *p.Financials.DefaultBillRate
		}
	}
	maps.Copy(body, overrides)
	for field, value := range overrides {
		if value == nil {
			delete(body, field)
		}
	}
	return body
}

// updateProject sends one update and returns the response.
func updateProject(t *testing.T, c *modtest.Client, p projectJSON, overrides map[string]any) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, fmt.Sprintf("/api/v1/projects/%d", p.Id), updateBody(p, overrides))
}

// putProject is updateProject for a test that expects it to be applied.
func putProject(t *testing.T, c *modtest.Client, p projectJSON, overrides map[string]any) projectJSON {
	t.Helper()
	r := updateProject(t, c, p, overrides)
	if r.Status != http.StatusOK {
		t.Fatalf("update: status %d body %s, want 200", r.Status, r.Body)
	}
	var updated projectJSON
	r.JSON(&updated)
	return updated
}

// eventTypes is the timeline event types one project carries, oldest first.
func eventTypes(t *testing.T, h *modtest.Harness, projectID int32) []string {
	t.Helper()
	raw := modtest.One[string](t, h, `SELECT coalesce(string_agg(event_type, ',' ORDER BY id), '')
	                                  FROM projects.timeline_entries WHERE project_id = $1`, projectID)
	if raw == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

func TestPutProjectsById_Manager_AppliesTheChangeAndBumpsTheRevision(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "UPD1000"})

	updated := putProject(t, c, project, map[string]any{
		"name": "Kraft-Verket fase to", "description": "Andre byggetrinn",
		"startDate": "2026-04-01", "endDate": "2026-12-31", "budgetHours": 320,
	})

	if updated.Name != "Kraft-Verket fase to" {
		t.Errorf("Name = %q, want the new name", updated.Name)
	}
	if updated.Description == nil || *updated.Description != "Andre byggetrinn" {
		t.Errorf("Description = %v, want the new description", updated.Description)
	}
	if updated.StartDate == nil || *updated.StartDate != "2026-04-01" || updated.EndDate == nil || *updated.EndDate != "2026-12-31" {
		t.Errorf("dates = %v..%v, want 2026-04-01..2026-12-31", updated.StartDate, updated.EndDate)
	}
	if updated.BudgetHours == nil || *updated.BudgetHours != 320 {
		t.Errorf("BudgetHours = %v, want 320", updated.BudgetHours)
	}
	if updated.Revision != project.Revision+1 {
		t.Errorf("Revision = %d, want %d", updated.Revision, project.Revision+1)
	}
	if !updated.UpdatedAt.After(project.UpdatedAt) && !updated.UpdatedAt.Equal(project.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want it stamped no earlier than the create's %v", updated.UpdatedAt, project.UpdatedAt)
	}

	// The change survives the round trip, and details-changed names the
	// fields that moved — never the fields that did not.
	fetched := getProject(t, c, project.Id)
	var reread projectJSON
	fetched.JSON(&reread)
	if reread.Name != updated.Name || reread.Revision != updated.Revision {
		t.Errorf("re-read %+v, want the updated project", reread)
	}
	if got := eventTypes(t, h, project.Id); len(got) != 2 || got[1] != "details-changed" {
		t.Fatalf("timeline = %v, want [project-created details-changed]", got)
	}
	fields := changedFields(t, h, project.Id, "details-changed")
	for _, want := range []string{"name", "description", "startDate", "endDate", "budgetHours"} {
		if !contains(fields, want) {
			t.Errorf("details-changed fields = %v, want %q among them", fields, want)
		}
	}
	if contains(fields, "code") {
		t.Errorf("details-changed fields = %v, want the code reported by its own entry", fields)
	}
}

// changedFields reads the `fields` array of one project's single entry of
// the given type.
func changedFields(t *testing.T, h *modtest.Harness, projectID int32, eventType string) []string {
	t.Helper()
	raw := modtest.One[string](t, h, `SELECT payload::text FROM projects.timeline_entries
	                                  WHERE project_id = $1 AND event_type = $2 ORDER BY id LIMIT 1`, projectID, eventType)
	var payload struct {
		Fields []string `json:"fields"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return payload.Fields
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// Updating is managing (design §5): a member sees the project, so they get
// the 403 that says "not yours to change", not the 404 that says "no such
// project".
func TestPutProjectsById_Member_Returns403(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	project := createProject(t, creator, map[string]any{"code": "UPDMEM1000"})
	member, memberID := signIn(t, h)
	addRole(t, h, project.Id, memberID, "member")

	if r := updateProject(t, member, project, map[string]any{"name": "Nytt navn"}); r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403", r.Status, r.Body)
	}
}

func TestPutProjectsById_Outsider_Returns404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	project := createProject(t, creator, map[string]any{"code": "UPDOUT1000"})
	outsider, _ := signIn(t, h)

	existing := updateProject(t, outsider, project, map[string]any{"name": "Nytt navn"})
	if existing.Status != http.StatusNotFound {
		t.Fatalf("existing project: status %d body %s, want 404", existing.Status, existing.Body)
	}
	unknown := updateProject(t, outsider, projectJSON{Id: 999999, Code: "GHOST1000", Name: "Spøkelse", BillingType: "non-billable", Revision: 1},
		nil)
	if unknown.Status != http.StatusNotFound {
		t.Errorf("unknown id: status %d body %s, want 404", unknown.Status, unknown.Body)
	}
}

// The revision the caller read is the revision the write is applied against.
// One that has moved on answers 409 and changes nothing.
func TestPutProjectsById_StaleRevision_Returns409(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "UPDREV1000"})

	putProject(t, c, project, map[string]any{"name": "Første endring"})

	// project still carries the revision it was created with.
	r := updateProject(t, c, project, map[string]any{"name": "Andre endring"})
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	if problem.Title != "Project revision conflict" {
		t.Errorf("Title = %q, want %q", problem.Title, "Project revision conflict")
	}
	// The revision named is the one the project actually carries now, not
	// the one the caller sent — otherwise the message says the two are the
	// same number and explains nothing.
	if want := "The project has revision 2; the supplied revision was 1."; problem.Detail != want {
		t.Errorf("Detail = %q, want %q", problem.Detail, want)
	}

	name := modtest.One[string](t, h, `SELECT name FROM projects.projects WHERE id = $1`, project.Id)
	if name != "Første endring" {
		t.Errorf("name = %q, want the stale update to have changed nothing", name)
	}
}

// D1: the code is a label, editable at any time, and the change is recorded
// so an old code on a printed timesheet can still be traced.
func TestPutProjectsById_CodeChange_WritesCodeChangedWithBothCodes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "OLD1000"})

	updated := putProject(t, c, project, map[string]any{"code": "new1000"})
	if updated.Code != "NEW1000" {
		t.Errorf("Code = %q, want %q (trimmed and upper-cased on update too)", updated.Code, "NEW1000")
	}
	if got := eventTypes(t, h, project.Id); len(got) != 2 || got[1] != "code-changed" {
		t.Fatalf("timeline = %v, want [project-created code-changed]", got)
	}
	raw := modtest.One[string](t, h, `SELECT payload::text FROM projects.timeline_entries
	                                  WHERE project_id = $1 AND event_type = 'code-changed'`, project.Id)
	var payload map[string]string
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if payload["old"] != "OLD1000" || payload["new"] != "NEW1000" {
		t.Errorf("code-changed payload = %s, want old OLD1000 and new NEW1000", raw)
	}
}

// A code somebody else already holds is the same ordinary field error the
// create answers (D3), not a conflict.
func TestPutProjectsById_CodeAlreadyTaken_IsRejectedOnTheCodeField(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	createProject(t, c, map[string]any{"code": "TAKEN1000"})
	project := createProject(t, c, map[string]any{"code": "FREE1000"})

	r := updateProject(t, c, project, map[string]any{"code": "taken1000"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if len(problem.Errors["code"]) == 0 {
		t.Errorf("Errors = %v, want the failure on the 'code' field", problem.Errors)
	}
}

// D12: the billing entry records which financial fields changed and never
// what they changed to. The assertion is on the serialised payload, because
// that is the thing a later reader of the timeline actually sees.
func TestPutProjectsById_FixedPriceChange_WritesBillingChangedWithoutTheAmount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{
		"code": "UPDFIX1000", "billingType": "fixed-price", "fixedPriceAmount": 125000, "currency": "NOK",
	})

	putProject(t, c, project, map[string]any{"fixedPriceAmount": 250000})

	if got := eventTypes(t, h, project.Id); len(got) != 2 || got[1] != "billing-changed" {
		t.Fatalf("timeline = %v, want [project-created billing-changed]", got)
	}
	raw := modtest.One[string](t, h, `SELECT payload::text FROM projects.timeline_entries
	                                  WHERE project_id = $1 AND event_type = 'billing-changed'`, project.Id)
	if !strings.Contains(raw, "fixedPriceAmount") {
		t.Errorf("billing-changed payload = %s, want the field name in it", raw)
	}
	for _, amount := range []string{"250000", "125000"} {
		if strings.Contains(raw, amount) {
			t.Errorf("billing-changed payload = %s, want no amount in it (%s leaked)", raw, amount)
		}
	}
}

// defaultBillRate round-trips through an update exactly as the other amounts
// do, and its own change is named on billing-changed, never its value.
func TestPutProjectsById_DefaultBillRateChange_WritesBillingChangedWithoutTheAmount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{
		"code": "UPDRATE1000", "currency": "NOK", "defaultBillRate": 800,
	})

	updated := putProject(t, c, project, map[string]any{"defaultBillRate": 950})
	if updated.Financials == nil || updated.Financials.DefaultBillRate == nil || *updated.Financials.DefaultBillRate != 950 {
		t.Fatalf("Financials = %+v, want DefaultBillRate 950", updated.Financials)
	}

	if got := eventTypes(t, h, project.Id); len(got) != 2 || got[1] != "billing-changed" {
		t.Fatalf("timeline = %v, want [project-created billing-changed]", got)
	}
	raw := modtest.One[string](t, h, `SELECT payload::text FROM projects.timeline_entries
	                                  WHERE project_id = $1 AND event_type = 'billing-changed'`, project.Id)
	if !strings.Contains(raw, "defaultBillRate") {
		t.Errorf("billing-changed payload = %s, want the field name in it", raw)
	}
	for _, amount := range []string{"950", "800"} {
		if strings.Contains(raw, amount) {
			t.Errorf("billing-changed payload = %s, want no amount in it (%s leaked)", raw, amount)
		}
	}
}

// A full-replace PUT that omits defaultBillRate clears it, the same as any
// other optional amount — but clearing the currency it depends on while it is
// still set must fail on the currency field rather than silently drop it.
func TestPutProjectsById_ClearingCurrencyWithDefaultBillRateStillSet_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{
		"code": "UPDRATECUR1000", "currency": "NOK", "defaultBillRate": 800,
	})

	r := updateProject(t, c, project, map[string]any{"currency": nil, "defaultBillRate": 800})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if len(problem.Errors["currency"]) == 0 {
		t.Errorf("Errors = %v, want the failure on the 'currency' field", problem.Errors)
	}
}

// Omitting defaultBillRate on an otherwise complete update clears it, exactly
// as omitting budgetAmount would (a PUT is a full replace).
func TestPutProjectsById_OmittingDefaultBillRate_ClearsIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{
		"code": "UPDRATECLR1000", "currency": "NOK", "defaultBillRate": 800,
	})

	updated := putProject(t, c, project, map[string]any{"defaultBillRate": nil})
	if updated.Financials == nil || updated.Financials.DefaultBillRate != nil {
		t.Errorf("Financials = %+v, want DefaultBillRate absent after omitting it", updated.Financials)
	}
}

// D4 on an update as much as on a create: moving a project to internal
// removes the party there was to invoice, so it must become non-billable in
// the same request.
func TestPutProjectsById_MovingToInternal_RequiresNonBillable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "UPDINT1000"})

	r := updateProject(t, c, project, map[string]any{"customerId": nil})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if len(problem.Errors["billingType"]) == 0 {
		t.Errorf("Errors = %v, want the failure on the 'billingType' field", problem.Errors)
	}

	updated := putProject(t, c, project, map[string]any{"customerId": nil, "billingType": "non-billable"})
	if !updated.Internal || updated.CustomerId != nil {
		t.Errorf("updated = %+v, want an internal project with no customer", updated)
	}
	if got := eventTypes(t, h, project.Id); len(got) != 3 {
		t.Fatalf("timeline = %v, want the create plus a customer change and a billing change", got)
	}
	raw := modtest.One[string](t, h, `SELECT payload::text FROM projects.timeline_entries
	                                  WHERE project_id = $1 AND event_type = 'customer-changed'`, project.Id)
	var payload map[string]*int32
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if payload["oldCustomerId"] == nil || *payload["oldCustomerId"] != customerKraftVerket {
		t.Errorf("customer-changed payload = %s, want the old customer id", raw)
	}
	if payload["newCustomerId"] != nil {
		t.Errorf("customer-changed payload = %s, want a null new customer id", raw)
	}
}

// An update that changes nothing is not an event: the timeline records what
// happened, and nothing did.
func TestPutProjectsById_UnchangedBody_WritesNoTimelineEntry(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{
		"code": "UPDNOP1000", "description": "Uendret", "startDate": "2026-01-01", "budgetHours": 100,
	})

	// The code goes back lower-cased: it is upper-cased before it is
	// compared or stored (D2), so a case-only edit is not a code change and
	// must not write code-changed.
	updated := putProject(t, c, project, map[string]any{"code": strings.ToLower(project.Code)})
	if updated.Revision != project.Revision+1 {
		t.Errorf("Revision = %d, want %d: the write still happened", updated.Revision, project.Revision+1)
	}
	if got := eventTypes(t, h, project.Id); len(got) != 1 || got[0] != "project-created" {
		t.Errorf("timeline = %v, want only the create entry", got)
	}
}

// Every §4.1 rule the create runs, the update runs too: one case is enough
// to prove the same validator is on both paths, since values_test covers the
// rules themselves.
func TestPutProjectsById_InvalidBody_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "UPDVAL1000"})

	r := updateProject(t, c, project, map[string]any{"code": "UPD-1000"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if problem.Title != "Invalid project" || len(problem.Errors["code"]) == 0 {
		t.Errorf("problem = %+v, want an Invalid project problem on the 'code' field", problem)
	}
}

// Design §3.3's fixed-price guard: a percent milestone that is still
// 'planned' or 'ready' resolves its amount from the project's fixed price, so
// moving the project off fixed-price billing is refused while any exist. The
// caller changed billingType, not fixedPriceAmount — clearing the amount is
// only a side effect §4.1 already requires of that change — so the guard's
// error is attributed there (fixedPriceGuardField, values.go).
// milestones.go is Task 2's; the row is inserted directly (insertMilestone,
// harness_test.go).
func TestPutProjectsById_ChangingBillingTypeAwayFromFixedPrice_RefusedWhileOpenPercentMilestonesExist(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{
		"code": "FPG1000", "billingType": "fixed-price", "fixedPriceAmount": 100000, "currency": "NOK",
	})
	insertMilestone(t, h, project.Id, map[string]any{"name": "Kickoff", "percent": 50, "status": "planned"})

	r := updateProject(t, c, project, map[string]any{"billingType": "time-and-materials", "fixedPriceAmount": nil})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	msgs := problem.Errors["billingType"]
	if len(msgs) == 0 || !strings.Contains(msgs[0], "Kickoff") {
		t.Errorf("errors = %v, want a message on 'billingType' naming 'Kickoff'", problem.Errors)
	}

	// The refused request changed nothing: the project is still fixed-price.
	stored := modtest.One[string](t, h, `SELECT billing_type FROM projects.projects WHERE id = $1`, project.Id)
	if stored != "fixed-price" {
		t.Errorf("billing_type = %q, want the refused change to have left it 'fixed-price'", stored)
	}
}

// The guard is decided from status and percent, not from billing type alone:
// a milestone that has already been invoiced or was cancelled no longer
// depends on the fixed price to resolve, so it does not block the move.
func TestPutProjectsById_ChangingBillingTypeAwayFromFixedPrice_AllowedWhenMilestonesAreInvoicedOrCancelled(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{
		"code": "FPG1001", "billingType": "fixed-price", "fixedPriceAmount": 100000, "currency": "NOK",
	})
	insertMilestone(t, h, project.Id, map[string]any{"name": "Invoiced", "percent": 50, "status": "invoiced"})
	insertMilestone(t, h, project.Id, map[string]any{"name": "Cancelled", "percent": 25, "status": "cancelled"})

	updated := putProject(t, c, project, map[string]any{"billingType": "time-and-materials", "fixedPriceAmount": nil})
	if updated.BillingType != "time-and-materials" {
		t.Errorf("BillingType = %q, want 'time-and-materials'", updated.BillingType)
	}
}

// Changing the price to another value is allowed even with open percent
// milestones: they are not losing the price they depend on, only its value —
// design §3.3 says only removing it is refused.
func TestPutProjectsById_ChangingFixedPriceAmount_IsAllowedEvenWithOpenPercentMilestones(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{
		"code": "FPG1002", "billingType": "fixed-price", "fixedPriceAmount": 100000, "currency": "NOK",
	})
	insertMilestone(t, h, project.Id, map[string]any{"name": "Kickoff", "percent": 50, "status": "planned"})

	updated := putProject(t, c, project, map[string]any{"fixedPriceAmount": 150000})
	if updated.Financials == nil || updated.Financials.FixedPriceAmount == nil || *updated.Financials.FixedPriceAmount != 150000 {
		t.Errorf("Financials = %+v, want fixedPriceAmount 150000", updated.Financials)
	}
}

// The message names up to five milestones by name, in the project's own
// order, and folds the rest into a count rather than growing without bound.
func TestPutProjectsById_FixedPriceGuardMessage_NamesUpToFiveThenMore(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{
		"code": "FPG1003", "billingType": "fixed-price", "fixedPriceAmount": 100000, "currency": "NOK",
	})
	names := []string{"Alpha", "Bravo", "Charlie", "Delta", "Echo", "Foxtrot"}
	for i, name := range names {
		insertMilestone(t, h, project.Id, map[string]any{
			"name": name, "percent": 10, "status": "planned", "position": int32(i + 1),
		})
	}

	r := updateProject(t, c, project, map[string]any{"billingType": "time-and-materials", "fixedPriceAmount": nil})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	msgs := problem.Errors["billingType"]
	if len(msgs) == 0 {
		t.Fatalf("errors = %v, want a message on 'billingType'", problem.Errors)
	}
	msg := msgs[0]
	for _, name := range names[:5] {
		if !strings.Contains(msg, name) {
			t.Errorf("message %q, want %q named", msg, name)
		}
	}
	if strings.Contains(msg, "Foxtrot") {
		t.Errorf("message %q, want the sixth name folded into a count instead of named", msg)
	}
	if !strings.Contains(msg, "and 1 more") {
		t.Errorf("message %q, want \"and 1 more\"", msg)
	}
}

// Clearing fixedPriceAmount while billingType stays 'fixed-price' is already
// refused by §4.1's own rule (validateFixedPriceAmount: a fixed-price project
// must carry an amount), on the same field, before the milestone guard ever
// runs — so this is provably not a path a valid request can reach today. It
// is pinned down here rather than exercised through the guard, whose
// fixedPriceAmount branch (fixedPriceGuardField, values.go) exists for the
// day that rule loosens.
func TestPutProjectsById_ClearingFixedPriceAmountAlone_IsAlreadyRefusedByFieldValidation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{
		"code": "FPG1004", "billingType": "fixed-price", "fixedPriceAmount": 100000, "currency": "NOK",
	})
	insertMilestone(t, h, project.Id, map[string]any{"name": "Kickoff", "percent": 50, "status": "planned"})

	r := updateProject(t, c, project, map[string]any{"fixedPriceAmount": nil})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if len(problem.Errors["fixedPriceAmount"]) == 0 {
		t.Errorf("errors = %v, want a message on 'fixedPriceAmount'", problem.Errors)
	}
}

// manage-all manages every project, this one included, without holding a
// role on it (design §5).
func TestPutProjectsById_ManageAll_MayUpdate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	project := createProject(t, creator, map[string]any{"code": "UPDADM1000"})
	admin, _ := signIn(t, h, "projects:manage-all")

	if updated := putProject(t, admin, project, map[string]any{"name": "Administrert"}); updated.Name != "Administrert" {
		t.Errorf("Name = %q, want the new name", updated.Name)
	}
}
