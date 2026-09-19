package projects_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The billing milestones (design §2 E1/E5, §3.2): the invoice plan a project
// is read off, each milestone either a flat amount or a percentage of the
// project's fixed price. Three things run through every case here.
//
// The first is that a milestone is money: every one of these operations needs
// financial rights on the project, so a member who sees the project gets 403
// and an outsider gets the bare 404 an unknown id gets. The second is that
// the amount the plan counts is computed on read (the "effective amount"), so
// an open percent milestone follows a change to the fixed price while an
// invoiced one, whose amount was frozen, does not. The third is that
// everything a caller may do with a milestone is answered in its own
// `capabilities`, so the frontend never re-derives §3.2's move table.
//
// The status moves themselves live in milestones_status_test.go; the two
// races the ordering and the moves leave to the database live in
// milestones_concurrency_test.go.

// The milestone statuses, spelled here the way the contract spells them so a
// test reads as the API does.
const (
	milestonePlanned   = "planned"
	milestoneReady     = "ready"
	milestoneInvoiced  = "invoiced"
	milestoneCancelled = "cancelled"
)

// fixedPriceProject is the fixture most of these tests need: a fixed-price
// project in NOK, whose creator is its manager and so may do everything.
func fixedPriceProject(t *testing.T, c *modtest.Client, code string, price float64) projectJSON {
	t.Helper()
	return createProject(t, c, map[string]any{
		"code": code, "billingType": "fixed-price", "fixedPriceAmount": price, "currency": "NOK",
	})
}

// amountProject is the other fixture: an ordinary time-and-materials project
// with a currency, which may carry flat-amount milestones but no percent ones.
func amountProject(t *testing.T, c *modtest.Client, code string) projectJSON {
	t.Helper()
	return createProject(t, c, map[string]any{"code": code, "currency": "NOK"})
}

// A create appends: the new milestone takes the number after the last one,
// its effective amount is the amount as entered, and its manager may do
// everything §3.2 allows a 'planned' milestone.
func TestPostProjectsByIdMilestones_CreatesAnAmountMilestoneAppendedLast(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "MS1000")

	first := createMilestone(t, c, project.Id, map[string]any{
		"name": "Oppstart", "amount": 100000, "description": "Faktureres ved kickoff",
		"plannedDate": "2026-10-01",
	})
	second := createMilestone(t, c, project.Id, map[string]any{"name": "Leveranse", "amount": 250000})

	if first.Position != 1 || second.Position != 2 {
		t.Errorf("positions = %d, %d, want 1 and 2", first.Position, second.Position)
	}
	if first.ProjectId != project.Id {
		t.Errorf("ProjectId = %d, want %d", first.ProjectId, project.Id)
	}
	if first.Status != milestonePlanned {
		t.Errorf("Status = %q, want %q", first.Status, milestonePlanned)
	}
	if first.Amount == nil || *first.Amount != 100000 {
		t.Errorf("Amount = %v, want 100000", first.Amount)
	}
	if first.Percent != nil {
		t.Errorf("Percent = %v, want absent on an amount milestone", first.Percent)
	}
	if got := effectiveAmount(t, first); got != 100000 {
		t.Errorf("EffectiveAmount = %v, want the amount as entered", got)
	}
	if first.Currency == nil || *first.Currency != "NOK" {
		t.Errorf("Currency = %v, want the project's", first.Currency)
	}
	if first.Description == nil || *first.Description != "Faktureres ved kickoff" {
		t.Errorf("Description = %v, want the one sent", first.Description)
	}
	if first.PlannedDate == nil || *first.PlannedDate != "2026-10-01" {
		t.Errorf("PlannedDate = %v, want 2026-10-01", first.PlannedDate)
	}
	if first.Revision != 1 {
		t.Errorf("Revision = %d, want 1", first.Revision)
	}
	if first.ReadyAt != nil || first.InvoicedAt != nil || first.InvoiceReference != nil {
		t.Errorf("a new milestone carries stamps: %+v", first)
	}
	want := milestoneCapabilitiesJSON{CanEdit: true, CanDelete: true, CanMarkReady: true, CanCancel: true}
	if first.Capabilities != want {
		t.Errorf("Capabilities = %+v, want %+v", first.Capabilities, want)
	}
	if got := eventTypes(t, h, project.Id); got[len(got)-1] != "milestone-added" {
		t.Errorf("timeline = %v, want it to end with milestone-added", got)
	}
}

// A milestone-added entry names the milestone and never its amount: the
// timeline is read by anyone who can see the project, including a member who
// may not see its money (D12).
func TestPostProjectsByIdMilestones_TimelineEntryNamesItAndNoAmount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "MS1001")
	created := createMilestone(t, c, project.Id, map[string]any{"name": "Oppstart", "amount": 123456})

	payload := lastPayloadText(t, h, project.Id, "milestone-added")
	if !strings.Contains(payload, "Oppstart") {
		t.Errorf("payload %s does not name the milestone", payload)
	}
	if !strings.Contains(payload, fmt.Sprintf("%d", created.Id)) {
		t.Errorf("payload %s does not carry the milestone's id", payload)
	}
	if strings.Contains(payload, "123456") {
		t.Errorf("payload %s carries an amount", payload)
	}
}

// §3.2's percent milestone and the exact-decimal rule behind it, through the
// API: the effective amount is the project's fixed price times the percent,
// half up to the cent. The three cases are the ones float64 arithmetic gets
// wrong (see TestPercentOfPrice, which pins the same rule on the function).
func TestPostProjectsByIdMilestones_PercentOfTheFixedPrice_ResolvesExactly(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		code    string
		price   float64
		percent float64
		want    float64
	}{
		{"a third of a round price", "MSPCT1", 300000.00, 33.33, 99990.00},
		{"a half cent that rounds down", "MSPCT2", 100000.01, 12.5, 12500.00},
		{"a half cent that rounds up", "MSPCT3", 999.99, 50, 500.00},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			c, _ := signIn(t, h, "projects:create")
			project := fixedPriceProject(t, c, tc.code, tc.price)

			created := createMilestone(t, c, project.Id, map[string]any{
				"name": "Andel", "amount": nil, "percent": tc.percent,
			})

			if created.Amount != nil {
				t.Errorf("Amount = %v, want absent on a percent milestone", created.Amount)
			}
			if created.Percent == nil || *created.Percent != tc.percent {
				t.Errorf("Percent = %v, want %v", created.Percent, tc.percent)
			}
			if got := effectiveAmount(t, created); got != tc.want {
				t.Errorf("EffectiveAmount = %v, want %v", got, tc.want)
			}
		})
	}
}

// A percent only means something against a fixed price (§3.2), so a project
// without one refuses it on the field that carries it.
func TestPostProjectsByIdMilestones_PercentWithoutAFixedPrice_Returns400OnPercent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "MS1002")

	r := postMilestone(t, c, project.Id, map[string]any{"amount": nil, "percent": 25})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if len(problem.Errors["percent"]) == 0 {
		t.Errorf("errors = %v, want a message on 'percent'", problem.Errors)
	}
}

// Any milestone is an amount, and an amount needs a currency to be
// denominated in (D13) — the same rule a 'fixed' billing line follows.
func TestPostProjectsByIdMilestones_WithoutAProjectCurrency_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "MS1003"})

	r := postMilestone(t, c, project.Id, nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if len(problem.Errors["amount"]) == 0 {
		t.Errorf("errors = %v, want a message on 'amount'", problem.Errors)
	}
}

// Design §3.2's content rules, one case each. A rule that fails names the
// field it is about, because that is what the form renders the message under.
func TestPostProjectsByIdMilestones_InvalidBody_Returns400OnTheField(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := fixedPriceProject(t, c, "MS1004", 500000)

	cases := []struct {
		name      string
		overrides map[string]any
		field     string
	}{
		{"a blank name", map[string]any{"name": "   "}, "name"},
		{"no name at all", map[string]any{"name": nil}, "name"},
		{"a name past 200 characters", map[string]any{"name": strings.Repeat("x", 201)}, "name"},
		{"a description past 2000 characters", map[string]any{"description": strings.Repeat("x", 2001)}, "description"},
		{"neither an amount nor a percent", map[string]any{"amount": nil}, "amount"},
		{"both an amount and a percent", map[string]any{"percent": 10}, "amount"},
		{"an amount of zero", map[string]any{"amount": 0}, "amount"},
		{"a negative amount", map[string]any{"amount": -1}, "amount"},
		{"an amount past the column", map[string]any{"amount": 10000000000.00}, "amount"},
		{"a percent of zero", map[string]any{"amount": nil, "percent": 0}, "percent"},
		{"a percent past a hundred", map[string]any{"amount": nil, "percent": 100.01}, "percent"},
		{"a percent with three decimals", map[string]any{"amount": nil, "percent": 33.333}, "percent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := postMilestone(t, c, project.Id, tc.overrides)
			if r.Status != http.StatusBadRequest {
				t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
			}
			var problem validationProblemJSON
			r.JSON(&problem)
			if len(problem.Errors[tc.field]) == 0 {
				t.Errorf("errors = %v, want a message on %q", problem.Errors, tc.field)
			}
		})
	}
}

// The plan (§3.2): the milestones in position order plus what they add up to,
// one total per status. Against a fixed price the plan also says how much of
// it nobody has planned yet — planned + ready + invoiced against the price.
func TestGetProjectsByIdMilestones_Plan_TotalsPerStatusAndWhatIsUnplanned(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := fixedPriceProject(t, c, "MSPLAN1", 1000000)

	createMilestone(t, c, project.Id, map[string]any{"name": "Oppstart", "amount": 100000})
	ready := createMilestone(t, c, project.Id, map[string]any{"name": "Fase 1", "amount": 200000})
	invoiced := createMilestone(t, c, project.Id, map[string]any{"name": "Fase 2", "amount": 300000})
	movedMilestone(t, c, ready, milestoneReady, nil)
	invoiced = movedMilestone(t, c, invoiced, milestoneReady, nil)
	movedMilestone(t, c, invoiced, milestoneInvoiced, nil)

	plan := getMilestones(t, c, project.Id)
	if got := milestoneNames(plan); !equalStrings(got, []string{"Oppstart", "Fase 1", "Fase 2"}) {
		t.Errorf("names = %v, want them in position order", got)
	}
	if plan.Totals.Currency == nil || *plan.Totals.Currency != "NOK" {
		t.Errorf("Totals.Currency = %v, want NOK", plan.Totals.Currency)
	}
	if plan.Totals.Planned != 100000 || plan.Totals.Ready != 200000 || plan.Totals.Invoiced != 300000 {
		t.Errorf("Totals = %+v, want 100000 / 200000 / 300000", plan.Totals)
	}
	if plan.Totals.Cancelled != 0 {
		t.Errorf("Totals.Cancelled = %v, want 0", plan.Totals.Cancelled)
	}
	if plan.Totals.FixedPrice == nil || *plan.Totals.FixedPrice != 1000000 {
		t.Errorf("Totals.FixedPrice = %v, want 1000000", plan.Totals.FixedPrice)
	}
	if plan.Totals.Unplanned == nil || *plan.Totals.Unplanned != 400000 {
		t.Errorf("Totals.Unplanned = %v, want 400000", plan.Totals.Unplanned)
	}
	if plan.Totals.OverPlanned != nil {
		t.Errorf("Totals.OverPlanned = %v, want absent while there is something left to plan", plan.Totals.OverPlanned)
	}
}

// The other side of the same comparison: a plan that adds up to more than the
// fixed price says so, and says nothing about anything being unplanned.
func TestGetProjectsByIdMilestones_Plan_OverPlannedAgainstTheFixedPrice(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := fixedPriceProject(t, c, "MSPLAN2", 100000)

	createMilestone(t, c, project.Id, map[string]any{"name": "En", "amount": 80000})
	createMilestone(t, c, project.Id, map[string]any{"name": "To", "amount": 50000})

	plan := getMilestones(t, c, project.Id)
	if plan.Totals.OverPlanned == nil || *plan.Totals.OverPlanned != 30000 {
		t.Errorf("Totals.OverPlanned = %v, want 30000", plan.Totals.OverPlanned)
	}
	if plan.Totals.Unplanned != nil {
		t.Errorf("Totals.Unplanned = %v, want absent when the plan is over the price", plan.Totals.Unplanned)
	}
}

// A project with no fixed price has nothing to compare the plan against, so
// neither figure is there — the plan is still a plan, it just is not measured
// against anything.
func TestGetProjectsByIdMilestones_Plan_NoFixedPriceMeansNoComparison(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "MSPLAN3")
	createMilestone(t, c, project.Id, map[string]any{"name": "En", "amount": 80000})

	plan := getMilestones(t, c, project.Id)
	if plan.Totals.FixedPrice != nil || plan.Totals.Unplanned != nil || plan.Totals.OverPlanned != nil {
		t.Errorf("Totals = %+v, want no fixed-price comparison", plan.Totals)
	}
	if plan.Totals.Planned != 80000 {
		t.Errorf("Totals.Planned = %v, want 80000", plan.Totals.Planned)
	}
}

// A cancelled milestone bills nothing: it has its own total, it is left out of
// what the plan counts against the fixed price, and it sorts last whatever
// number it carries.
func TestGetProjectsByIdMilestones_Cancelled_SortsLastAndCountsAgainstNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := fixedPriceProject(t, c, "MSPLAN4", 100000)

	first := createMilestone(t, c, project.Id, map[string]any{"name": "Først", "amount": 10000})
	createMilestone(t, c, project.Id, map[string]any{"name": "Så", "amount": 20000})
	movedMilestone(t, c, first, milestoneCancelled, nil)

	plan := getMilestones(t, c, project.Id)
	if got := milestoneNames(plan); !equalStrings(got, []string{"Så", "Først"}) {
		t.Errorf("names = %v, want the cancelled one last despite its position", got)
	}
	if got := milestonePositions(plan); !equalInt32s(got, []int32{2, 1}) {
		t.Errorf("positions = %v, want the stored numbers, only the order moved", got)
	}
	if plan.Totals.Cancelled != 10000 || plan.Totals.Planned != 20000 {
		t.Errorf("Totals = %+v, want cancelled 10000 and planned 20000", plan.Totals)
	}
	if plan.Totals.Unplanned == nil || *plan.Totals.Unplanned != 80000 {
		t.Errorf("Totals.Unplanned = %v, want 80000 — the cancelled milestone counts against nothing", plan.Totals.Unplanned)
	}
}

// The effective amount is computed on read, which is what makes an open
// percent milestone follow a change to the fixed price. An invoiced one froze
// its amount when it was invoiced (§3.2, E5) and does not move with it.
func TestGetProjectsByIdMilestones_PercentFollowsTheFixedPrice_AnInvoicedOneDoesNot(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := fixedPriceProject(t, c, "MSPCT4", 100000)

	createMilestone(t, c, project.Id, map[string]any{"name": "Åpen", "amount": nil, "percent": 25})
	frozen := createMilestone(t, c, project.Id, map[string]any{"name": "Fakturert", "amount": nil, "percent": 25})
	frozen = movedMilestone(t, c, frozen, milestoneReady, nil)
	movedMilestone(t, c, frozen, milestoneInvoiced, nil)

	putProject(t, c, project, map[string]any{"fixedPriceAmount": 200000})

	plan := getMilestones(t, c, project.Id)
	byName := map[string]milestoneJSON{}
	for _, m := range plan.Milestones {
		byName[m.Name] = m
	}
	if got := effectiveAmount(t, byName["Åpen"]); got != 50000 {
		t.Errorf("the open milestone's effective amount = %v, want 50000 — it follows the new price", got)
	}
	if got := effectiveAmount(t, byName["Fakturert"]); got != 25000 {
		t.Errorf("the invoiced milestone's effective amount = %v, want the frozen 25000", got)
	}
}

// The one case a milestone has no currency to report: Task 1's guard counts
// only milestones that still bill something, so a project whose whole plan
// was cancelled may clear its currency — and the cancelled milestones survive
// it. They are history at that point, which is why the field is absent rather
// than the request being refused.
func TestGetProjectsByIdMilestones_CancelledMilestone_OutlivesTheProjectsCurrency(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "MS1019")
	m := createMilestone(t, c, project.Id, map[string]any{"name": "Avlyst"})
	movedMilestone(t, c, m, milestoneCancelled, nil)

	project = putProject(t, c, project, map[string]any{"currency": nil})

	plan := getMilestones(t, c, project.Id)
	if len(plan.Milestones) != 1 {
		t.Fatalf("milestones = %v, want the cancelled one still listed", milestoneNames(plan))
	}
	if plan.Milestones[0].Currency != nil {
		t.Errorf("Currency = %v, want absent once the project has none", plan.Milestones[0].Currency)
	}
	if plan.Totals.Cancelled != 100000 {
		t.Errorf("Totals.Cancelled = %v, want the amount it still carries", plan.Totals.Cancelled)
	}
}

// One milestone is addressed by its own id, without its project: the project
// — and with it the caller's access — is resolved by loading the milestone
// first, the way a task is.
func TestGetProjectsMilestonesByMilestoneId_ReadsOneByItsOwnId(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "MS1005")
	created := createMilestone(t, c, project.Id, map[string]any{"name": "Oppstart"})

	got := getMilestone(t, c, created.Id)
	if got.Id != created.Id || got.Name != "Oppstart" || got.ProjectId != project.Id {
		t.Errorf("milestone = %+v, want the one created", got)
	}
}

// An update carries every field of the milestone as it should stand
// afterwards, plus the revision the caller read it at — a project's own
// update, applied to a milestone. Where it sits is not part of it: position is
// the move's, so an edit saved from an open drawer cannot undo a reordering.
func TestPutProjectsMilestonesByMilestoneId_ReplacesTheContentAndBumpsTheRevision(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := fixedPriceProject(t, c, "MS1006", 400000)
	created := createMilestone(t, c, project.Id, map[string]any{
		"name": "Oppstart", "amount": 100000, "description": "Noe", "plannedDate": "2026-11-01",
	})

	changed := changeMilestone(t, c, created, map[string]any{
		"name": "Oppstart, revidert", "amount": nil, "percent": 25,
		"description": nil, "plannedDate": nil,
	})

	if changed.Name != "Oppstart, revidert" {
		t.Errorf("Name = %q, want the new one", changed.Name)
	}
	if changed.Amount != nil {
		t.Errorf("Amount = %v, want it replaced by the percent", changed.Amount)
	}
	if changed.Percent == nil || *changed.Percent != 25 {
		t.Errorf("Percent = %v, want 25", changed.Percent)
	}
	if got := effectiveAmount(t, changed); got != 100000 {
		t.Errorf("EffectiveAmount = %v, want 25 %% of 400000", got)
	}
	if changed.Description != nil || changed.PlannedDate != nil {
		t.Errorf("milestone = %+v, want the omitted fields cleared", changed)
	}
	if changed.Revision != created.Revision+1 {
		t.Errorf("Revision = %d, want %d", changed.Revision, created.Revision+1)
	}
	if changed.Position != created.Position {
		t.Errorf("Position = %d, want the update to leave it alone", changed.Position)
	}
}

// Every milestone write is revision guarded (§5): a second edit carrying the
// revision the first one moved past is refused rather than silently applied.
func TestPutProjectsMilestonesByMilestoneId_StaleRevision_Returns409(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "MS1007")
	created := createMilestone(t, c, project.Id, nil)
	changeMilestone(t, c, created, map[string]any{"name": "Først"})

	r := putMilestone(t, c, created, map[string]any{"name": "Så"})
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	if !strings.Contains(problem.Detail, "revision 2") {
		t.Errorf("detail = %q, want it to name the revision the milestone now carries", problem.Detail)
	}
}

// Content edits are allowed while a milestone is planned or ready and refused
// once it is invoiced or cancelled (§3.2): an invoiced milestone is a record
// of something that was billed, and the way back is the status, not the form.
func TestPutProjectsMilestonesByMilestoneId_InvoicedOrCancelled_Returns400OnStatus(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "MS1008")

	invoiced := createMilestone(t, c, project.Id, map[string]any{"name": "Fakturert"})
	invoiced = movedMilestone(t, c, invoiced, milestoneReady, nil)
	invoiced = movedMilestone(t, c, invoiced, milestoneInvoiced, nil)
	cancelled := createMilestone(t, c, project.Id, map[string]any{"name": "Avlyst"})
	cancelled = movedMilestone(t, c, cancelled, milestoneCancelled, nil)

	for _, tc := range []struct {
		status string
		m      milestoneJSON
		says   string
	}{
		{milestoneInvoiced, invoiced, "undo"},
		{milestoneCancelled, cancelled, "reopen"},
	} {
		r := putMilestone(t, c, tc.m, map[string]any{"name": "Nytt navn"})
		if r.Status != http.StatusBadRequest {
			t.Fatalf("%s: status %d body %s, want 400", tc.status, r.Status, r.Body)
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		msgs := problem.Errors["status"]
		if len(msgs) == 0 {
			t.Fatalf("%s: errors = %v, want a message on 'status'", tc.status, problem.Errors)
		}
		if !strings.Contains(strings.ToLower(msgs[0]), tc.says) {
			t.Errorf("%s: message = %q, want it to say how to get back", tc.status, msgs[0])
		}
	}
}

// A mis-created milestone is deleted; anything that has ever moved is
// cancelled instead (§3.2), so the plan keeps the record of what was billed.
// The delete closes the gap it leaves behind, exactly as a task's does.
func TestDeleteProjectsMilestonesByMilestoneId_PlannedAndNeverMoved_RemovesItAndRenumbers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "MS1009")
	first := createMilestone(t, c, project.Id, map[string]any{"name": "En"})
	createMilestone(t, c, project.Id, map[string]any{"name": "To"})
	createMilestone(t, c, project.Id, map[string]any{"name": "Tre"})

	r := c.Do(http.MethodDelete, milestonePath(first.Id), nil)
	if r.Status != http.StatusNoContent {
		t.Fatalf("status %d body %s, want 204", r.Status, r.Body)
	}

	plan := getMilestones(t, c, project.Id)
	if got := milestoneNames(plan); !equalStrings(got, []string{"To", "Tre"}) {
		t.Errorf("names = %v, want the deleted one gone", got)
	}
	if got := milestonePositions(plan); !equalInt32s(got, []int32{1, 2}) {
		t.Errorf("positions = %v, want 1..n with no gap", got)
	}
	if got := eventTypes(t, h, project.Id); got[len(got)-1] != "milestone-removed" {
		t.Errorf("timeline = %v, want it to end with milestone-removed", got)
	}
	if again := c.Do(http.MethodDelete, milestonePath(first.Id), nil); again.Status != http.StatusNotFound {
		t.Errorf("deleting it twice: status %d, want 404", again.Status)
	}
}

// The two milestones a delete refuses: one that is no longer 'planned', and
// one that came back to 'planned' after having moved. Both are cancelled
// instead, and the refusal says so.
func TestDeleteProjectsMilestonesByMilestoneId_MovedOrNotPlanned_Returns400OnStatus(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "MS1010")

	ready := createMilestone(t, c, project.Id, map[string]any{"name": "Klar"})
	ready = movedMilestone(t, c, ready, milestoneReady, nil)
	returned := createMilestone(t, c, project.Id, map[string]any{"name": "Tilbake"})
	returned = movedMilestone(t, c, returned, milestoneReady, nil)
	returned = movedMilestone(t, c, returned, milestonePlanned, nil)

	for _, m := range []milestoneJSON{ready, returned} {
		r := c.Do(http.MethodDelete, milestonePath(m.Id), nil)
		if r.Status != http.StatusBadRequest {
			t.Fatalf("%s: status %d body %s, want 400", m.Name, r.Status, r.Body)
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		msgs := problem.Errors["status"]
		if len(msgs) == 0 {
			t.Fatalf("%s: errors = %v, want a message on 'status'", m.Name, problem.Errors)
		}
		if !strings.Contains(strings.ToLower(msgs[0]), "cancel") {
			t.Errorf("%s: message = %q, want it to point at cancelling", m.Name, msgs[0])
		}
	}
	if returned.Capabilities.CanDelete {
		t.Errorf("a milestone that has moved says canDelete: %+v", returned.Capabilities)
	}
}

// A move renumbers the whole project's milestones 1..n, so the order never has
// a gap or a duplicate — the task list's rule, applied to the invoice plan.
func TestPutProjectsMilestonesByMilestoneIdPosition_RenumbersOneToN(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "MS1011")
	createMilestone(t, c, project.Id, map[string]any{"name": "En"})
	createMilestone(t, c, project.Id, map[string]any{"name": "To"})
	third := createMilestone(t, c, project.Id, map[string]any{"name": "Tre"})

	r := moveMilestone(t, c, third, 1)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var moved milestoneJSON
	r.JSON(&moved)
	if moved.Position != 1 {
		t.Errorf("Position = %d, want 1", moved.Position)
	}

	plan := getMilestones(t, c, project.Id)
	if got := milestoneNames(plan); !equalStrings(got, []string{"Tre", "En", "To"}) {
		t.Errorf("names = %v, want the moved one first", got)
	}
	if got := milestonePositions(plan); !equalInt32s(got, []int32{1, 2, 3}) {
		t.Errorf("positions = %v, want 1..n", got)
	}
}

// The move is revision guarded like every other milestone write, so a drag
// made against a stale copy of the plan is refused rather than applied to
// somebody else's ordering.
func TestPutProjectsMilestonesByMilestoneIdPosition_StaleRevision_Returns409(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "MS1012")
	first := createMilestone(t, c, project.Id, map[string]any{"name": "En"})
	createMilestone(t, c, project.Id, map[string]any{"name": "To"})
	changeMilestone(t, c, first, map[string]any{"name": "En, endret"})

	if r := moveMilestone(t, c, first, 2); r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
}

// A position outside the plan is the ends of it: dragging something to the
// bottom of a list sends whatever number that list happened to have.
func TestPutProjectsMilestonesByMilestoneIdPosition_PastTheEnd_IsTheEnd(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "MS1013")
	first := createMilestone(t, c, project.Id, map[string]any{"name": "En"})
	createMilestone(t, c, project.Id, map[string]any{"name": "To"})

	if r := moveMilestone(t, c, first, 99); r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	if got := milestoneNames(getMilestones(t, c, project.Id)); !equalStrings(got, []string{"To", "En"}) {
		t.Errorf("names = %v, want the moved one last", got)
	}
	if r := moveMilestone(t, c, getMilestone(t, c, first.Id), 0); r.Status != http.StatusBadRequest {
		t.Fatalf("a position of zero: status %d body %s, want 400", r.Status, r.Body)
	}
}

// `overdue` is the server's own clock against the planned date, as plain UTC
// dates (§3.2): a milestone still waiting to be billed after the day it was
// planned for is overdue, and one already invoiced or cancelled never is.
func TestGetProjectsByIdMilestones_Overdue_IsTheServerClockAgainstThePlannedDate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "MS1014")
	today := h.Now().UTC().Format("2006-01-02")

	past := createMilestone(t, c, project.Id, map[string]any{"name": "Forfalt", "plannedDate": "2026-01-01"})
	createMilestone(t, c, project.Id, map[string]any{"name": "I dag", "plannedDate": today})
	createMilestone(t, c, project.Id, map[string]any{"name": "Senere", "plannedDate": "2099-01-01"})
	createMilestone(t, c, project.Id, map[string]any{"name": "Udatert"})
	billed := createMilestone(t, c, project.Id, map[string]any{"name": "Fakturert", "plannedDate": "2026-01-01"})
	billed = movedMilestone(t, c, billed, milestoneReady, nil)
	movedMilestone(t, c, billed, milestoneInvoiced, nil)

	overdue := map[string]bool{}
	for _, m := range getMilestones(t, c, project.Id).Milestones {
		overdue[m.Name] = m.Overdue
	}
	want := map[string]bool{
		"Forfalt": true, "I dag": false, "Senere": false, "Udatert": false, "Fakturert": false,
	}
	for name, w := range want {
		if overdue[name] != w {
			t.Errorf("%q overdue = %v, want %v", name, overdue[name], w)
		}
	}

	// A 'ready' milestone is still waiting to be billed, so it is overdue too.
	past = movedMilestone(t, c, past, milestoneReady, nil)
	if !getMilestone(t, c, past.Id).Overdue {
		t.Errorf("a ready milestone past its date is not overdue")
	}
}

// D7, for milestones: a caller with no role on the project is answered the
// bare 404 an unknown id gets, byte for byte, on every one of the operations —
// they must not be able to tell a milestone they may not see from one that
// does not exist.
func TestMilestones_Outsider_GetsTheBare404OfAnUnknownId(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h, "projects:create")
	project := amountProject(t, owner, "MS1015")
	created := createMilestone(t, owner, project.Id, nil)

	outsider, _ := signIn(t, h)
	const unknown = 999999
	for _, tc := range []struct {
		name         string
		method, path string
		body         any
	}{
		{"the plan", http.MethodGet, milestonesPath(project.Id), nil},
		{"a create", http.MethodPost, milestonesPath(project.Id), milestoneBody(nil)},
		{"a read", http.MethodGet, milestonePath(created.Id), nil},
		{"an update", http.MethodPut, milestonePath(created.Id), map[string]any{"name": "X", "amount": 1, "revision": 1}},
		{"a delete", http.MethodDelete, milestonePath(created.Id), nil},
		{"a move", http.MethodPut, milestonePositionPath(created.Id), map[string]any{"position": 1, "revision": 1}},
		{"a status change", http.MethodPost, milestoneStatusPath(created.Id), map[string]any{"status": milestoneReady, "revision": 1}},
	} {
		r := outsider.Do(tc.method, tc.path, tc.body)
		if r.Status != http.StatusNotFound {
			t.Errorf("%s: status %d body %s, want 404", tc.name, r.Status, r.Body)
		}
		if strings.TrimSpace(string(r.Body)) != "" {
			t.Errorf("%s: body %q, want the bare 404 an unknown id gets", tc.name, r.Body)
		}
	}
	// And the same answer for an id nobody has, which is the point.
	if r := outsider.Do(http.MethodGet, milestonePath(unknown), nil); r.Status != http.StatusNotFound {
		t.Errorf("an unknown id: status %d, want 404", r.Status)
	}
}

// A member sees the project but not its money (D12), and a milestone is
// nothing but money: they are refused with a 403 rather than a 404, because
// the milestone's existence is not the secret — its amount is.
func TestMilestones_MemberWithoutFinancialRights_Returns403OnEveryOperation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h, "projects:create")
	project := amountProject(t, owner, "MS1016")
	created := createMilestone(t, owner, project.Id, nil)

	member, memberID := signIn(t, h)
	addRole(t, h, project.Id, memberID, "member")

	for _, tc := range []struct {
		name         string
		method, path string
		body         any
	}{
		{"the plan", http.MethodGet, milestonesPath(project.Id), nil},
		{"a create", http.MethodPost, milestonesPath(project.Id), milestoneBody(nil)},
		{"a read", http.MethodGet, milestonePath(created.Id), nil},
		{"an update", http.MethodPut, milestonePath(created.Id), map[string]any{"name": "X", "amount": 1, "revision": 1}},
		{"a delete", http.MethodDelete, milestonePath(created.Id), nil},
		{"a move", http.MethodPut, milestonePositionPath(created.Id), map[string]any{"position": 1, "revision": 1}},
		{"a status change", http.MethodPost, milestoneStatusPath(created.Id), map[string]any{"status": milestoneReady, "revision": 1}},
	} {
		if r := member.Do(tc.method, tc.path, tc.body); r.Status != http.StatusForbidden {
			t.Errorf("%s: status %d body %s, want 403", tc.name, r.Status, r.Body)
		}
	}
}

// §5's capability on the project itself: the frontend's "Add milestone" switch
// is the project's manager, nobody else — a holder of projects:view-financials
// reads the plan but does not add to it.
func TestGetProjectsById_CanManageMilestones_IsTheManagersAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h, "projects:create")
	project := amountProject(t, owner, "MS1017")
	if !project.Capabilities.CanManageMilestones {
		t.Errorf("the project's manager: canManageMilestones = false, want true")
	}

	viewer, viewerID := signIn(t, h, "projects:view-financials")
	addRole(t, h, project.Id, viewerID, "viewer")
	var seen projectJSON
	r := viewer.Do(http.MethodGet, fmt.Sprintf("/api/v1/projects/%d", project.Id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	r.JSON(&seen)
	if seen.Capabilities.CanManageMilestones {
		t.Errorf("a financial viewer: canManageMilestones = true, want false")
	}
	if !seen.Capabilities.CanSeeFinancials {
		t.Errorf("a financial viewer cannot see financials: %+v", seen.Capabilities)
	}
}

// The other end of Task 1's fixed-price guard (§3.3), now that milestones can
// actually be created through the API: a project cannot leave fixed-price
// billing while a percent milestone still resolves its amount from that price,
// and the refusal names the milestone.
func TestPutProjectsById_OpenPercentMilestoneFromTheAPI_BlocksLeavingFixedPrice(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := fixedPriceProject(t, c, "MSGUARD1", 500000)
	createMilestone(t, c, project.Id, map[string]any{"name": "Sluttfaktura", "amount": nil, "percent": 40})

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
	if !strings.Contains(msgs[0], "Sluttfaktura") {
		t.Errorf("message = %q, want it to name the milestone", msgs[0])
	}

	// And the currency guard's own trigger, reachable the same way now.
	if cur := updateProject(t, c, project, map[string]any{"currency": nil}); cur.Status != http.StatusBadRequest {
		t.Errorf("clearing the currency: status %d body %s, want 400", cur.Status, cur.Body)
	}
}

// The plan of a project nobody has planned yet is an empty list and zeroes,
// not a null: a client that renders a plan must not have to special-case the
// first visit. A project with no currency cannot have milestones at all, so it
// has no currency to report either.
func TestGetProjectsByIdMilestones_EmptyPlan_IsAnEmptyListAndZeroes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "MS1018"})

	r := readMilestones(t, c, project.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var raw map[string]any
	if err := json.Unmarshal(r.Body, &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if list, ok := raw["milestones"].([]any); !ok || len(list) != 0 {
		t.Errorf("milestones = %v, want an empty array", raw["milestones"])
	}
	plan := getMilestones(t, c, project.Id)
	if plan.Totals.Planned != 0 || plan.Totals.Ready != 0 || plan.Totals.Invoiced != 0 || plan.Totals.Cancelled != 0 {
		t.Errorf("Totals = %+v, want zeroes", plan.Totals)
	}
	if plan.Totals.Currency != nil {
		t.Errorf("Totals.Currency = %v, want absent on a project with no currency", plan.Totals.Currency)
	}
}

// The one milestone whose effective amount cannot be worked out: a cancelled
// percent milestone on a project that has since left fixed-price billing.
// Task 1's guard lets the project do that — a cancelled milestone bills
// nothing — so the plan has to render it, and the honest answer is that there
// is no amount rather than 0.00, which would read as a milestone somebody
// planned at nothing.
func TestGetProjectsByIdMilestones_CancelledPercentWithNoFixedPrice_OmitsTheEffectiveAmount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := fixedPriceProject(t, c, "MSEFF1", 400000)
	m := createMilestone(t, c, project.Id, map[string]any{"name": "Andel", "amount": nil, "percent": 25})
	movedMilestone(t, c, m, milestoneCancelled, nil)
	project = putProject(t, c, project, map[string]any{
		"billingType": "time-and-materials", "fixedPriceAmount": nil,
	})

	plan := getMilestones(t, c, project.Id)
	if len(plan.Milestones) != 1 {
		t.Fatalf("milestones = %v, want the cancelled one still listed", milestoneNames(plan))
	}
	if plan.Milestones[0].EffectiveAmount != nil {
		t.Errorf("EffectiveAmount = %v, want it absent rather than zero", *plan.Milestones[0].EffectiveAmount)
	}
	if plan.Totals.Cancelled != 0 {
		t.Errorf("Totals.Cancelled = %v, want 0 — there is no amount to count", plan.Totals.Cancelled)
	}
	// The raw JSON, because a nil pointer cannot tell an absent key from a
	// null one and the contract says absent.
	r := readMilestones(t, c, project.Id)
	var raw struct {
		Milestones []map[string]any `json:"milestones"`
	}
	if err := json.Unmarshal(r.Body, &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, present := raw.Milestones[0]["effectiveAmount"]; present {
		t.Errorf("milestone = %v, want no effectiveAmount key at all", raw.Milestones[0])
	}
}

// The plan's four sums are money, so they are added in exact decimal like
// every other amount in this module: 0.10 + 0.20 is 0.30, not
// 0.30000000000000004, and three thirds of a krone add back up to it.
func TestGetProjectsByIdMilestones_Totals_AreExactToTheCent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	t.Run("two amounts whose float sum is not their decimal sum", func(t *testing.T) {
		project := amountProject(t, c, "MSCENT1")
		createMilestone(t, c, project.Id, map[string]any{"name": "En", "amount": 0.10})
		createMilestone(t, c, project.Id, map[string]any{"name": "To", "amount": 0.20})

		plan := getMilestones(t, c, project.Id)
		if plan.Totals.Planned != 0.30 {
			t.Errorf("Totals.Planned = %v, want exactly 0.3", plan.Totals.Planned)
		}
		if raw := readMilestones(t, c, project.Id); strings.Contains(string(raw.Body), "0.30000000000000004") {
			t.Errorf("body %s carries a binary artefact", raw.Body)
		}
	})

	t.Run("three shares that add back up to the whole", func(t *testing.T) {
		project := amountProject(t, c, "MSCENT2")
		createMilestone(t, c, project.Id, map[string]any{"name": "En", "amount": 33.33})
		createMilestone(t, c, project.Id, map[string]any{"name": "To", "amount": 33.33})
		createMilestone(t, c, project.Id, map[string]any{"name": "Tre", "amount": 33.34})

		if got := getMilestones(t, c, project.Id).Totals.Planned; got != 100.00 {
			t.Errorf("Totals.Planned = %v, want exactly 100", got)
		}
	})
}

// A content edit writes its own timeline entry, the way a billing line's does:
// the milestone by name and id, the fields that actually moved by name, and
// never a value — the timeline is read by anyone who can see the project,
// including a member who may not see its money (D12).
func TestPutProjectsMilestonesByMilestoneId_RecordsWhichFieldsChanged(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := fixedPriceProject(t, c, "MSCHG1", 400000)
	created := createMilestone(t, c, project.Id, map[string]any{
		"name": "Oppstart", "amount": 100000, "plannedDate": "2026-11-01",
	})

	changeMilestone(t, c, created, map[string]any{
		"name": "Oppstart, revidert", "amount": nil, "percent": 25,
	})

	if got := eventTypes(t, h, project.Id); got[len(got)-1] != "milestone-changed" {
		t.Fatalf("timeline = %v, want it to end with milestone-changed", got)
	}
	payload := lastPayloadText(t, h, project.Id, "milestone-changed")
	for _, want := range []string{"Oppstart, revidert", "name", "amount", "percent"} {
		if !strings.Contains(payload, want) {
			t.Errorf("payload %s does not name %q", payload, want)
		}
	}
	if strings.Contains(payload, "plannedDate") {
		t.Errorf("payload %s names a field that did not move", payload)
	}
	if strings.Contains(payload, "100000") || strings.Contains(payload, "25") {
		t.Errorf("payload %s carries a value", payload)
	}
}

// An edit that changed nothing still happened — updated_at and the revision
// both move — but the timeline records events, and no event occurred. It is
// the rule a billing line's change already follows.
func TestPutProjectsMilestonesByMilestoneId_NothingChanged_WritesNoEntry(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "MSCHG2")
	created := createMilestone(t, c, project.Id, map[string]any{"name": "Oppstart"})
	before := eventTypes(t, h, project.Id)

	changeMilestone(t, c, created, nil)

	if got := eventTypes(t, h, project.Id); len(got) != len(before) {
		t.Errorf("timeline = %v, want nothing written for an edit that changed nothing", got)
	}
}

// A reorder writes nothing either: where a milestone sits is not an event on
// the project's history, and the eight status entries plus milestone-changed
// are the whole vocabulary.
func TestPutProjectsMilestonesByMilestoneIdPosition_WritesNoTimelineEntry(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "MSCHG3")
	first := createMilestone(t, c, project.Id, map[string]any{"name": "En"})
	createMilestone(t, c, project.Id, map[string]any{"name": "To"})
	before := eventTypes(t, h, project.Id)

	if r := moveMilestone(t, c, first, 2); r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	if got := eventTypes(t, h, project.Id); len(got) != len(before) {
		t.Errorf("timeline = %v, want a reorder to write nothing", got)
	}
}
