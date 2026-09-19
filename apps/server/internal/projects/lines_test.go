package projects_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The three billing-line operations (D9, D10, D15). A line is "variant +
// pricing rule": projects never calculates money, it stores the rule and
// embeds enough of the variant to render it. Two things run through every
// case here — the pricing block is shaped out for a caller who may not see
// financials (D12), and all three operations are 409 when products is off
// (D10) — so most tests read a line back through a second caller rather than
// trusting the writer's own answer.

// lineJSON decodes BillingLineResponse. pricing is a pointer because its
// absence is D12's shaping: a test that must prove the key is not there
// decodes into a map instead, since a nil pointer cannot tell "absent" from
// "null".
type lineJSON struct {
	Id                 int32        `json:"id"`
	Code               string       `json:"code"`
	TrackableCode      string       `json:"trackableCode"`
	VariantId          int32        `json:"variantId"`
	ProductName        *string      `json:"productName"`
	Sku                *string      `json:"sku"`
	Unit               *string      `json:"unit"`
	VariantMissing     bool         `json:"variantMissing"`
	CatalogUnavailable bool         `json:"catalogUnavailable"`
	Active             bool         `json:"active"`
	BudgetHours        *float64     `json:"budgetHours"`
	CreatedAt          time.Time    `json:"createdAt"`
	UpdatedAt          time.Time    `json:"updatedAt"`
	Pricing            *pricingJSON `json:"pricing"`
}

type pricingJSON struct {
	Mode            string     `json:"mode"`
	FixedAmount     *float64   `json:"fixedAmount"`
	DiscountPercent *float64   `json:"discountPercent"`
	BudgetAmount    *float64   `json:"budgetAmount"`
	ListPrice       *moneyJSON `json:"listPrice"`
}

type moneyJSON struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

func linesPath(projectID int32) string {
	return fmt.Sprintf("/api/v1/projects/%d/billing-lines", projectID)
}

func linePath(projectID, lineID int32) string {
	return fmt.Sprintf("/api/v1/projects/%d/billing-lines/%d", projectID, lineID)
}

// lineBody is a valid minimal line body — the cheapest one, a list-priced
// project manager hour — which tests override one field of at a time. A nil
// override value removes that field, the same convention createBody uses.
func lineBody(overrides map[string]any) map[string]any {
	body := map[string]any{
		"code":        "PM",
		"variantId":   variantProjectManagerHour,
		"pricingMode": "list",
	}
	for field, value := range overrides {
		if value == nil {
			delete(body, field)
			continue
		}
		body[field] = value
	}
	return body
}

func postLine(t *testing.T, c *modtest.Client, projectID int32, overrides map[string]any) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPost, linesPath(projectID), lineBody(overrides))
}

// createLine is postLine for a test that expects the line to be created.
func createLine(t *testing.T, c *modtest.Client, projectID int32, overrides map[string]any) lineJSON {
	t.Helper()
	r := postLine(t, c, projectID, overrides)
	if r.Status != http.StatusCreated {
		t.Fatalf("create line: status %d body %s, want 201", r.Status, r.Body)
	}
	var line lineJSON
	r.JSON(&line)
	return line
}

func putLine(t *testing.T, c *modtest.Client, projectID, lineID int32, body map[string]any, opts ...modtest.RequestOption) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, linePath(projectID, lineID), body, opts...)
}

// changeLine is putLine for a test that expects the change to be applied.
func changeLine(t *testing.T, c *modtest.Client, projectID, lineID int32, body map[string]any) lineJSON {
	t.Helper()
	r := putLine(t, c, projectID, lineID, body)
	if r.Status != http.StatusOK {
		t.Fatalf("change line: status %d body %s, want 200", r.Status, r.Body)
	}
	var line lineJSON
	r.JSON(&line)
	return line
}

func readLines(t *testing.T, c *modtest.Client, projectID int32) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodGet, linesPath(projectID), nil)
}

// listLines is readLines for a test that expects to be shown the lines.
func listLines(t *testing.T, c *modtest.Client, projectID int32) []lineJSON {
	t.Helper()
	r := readLines(t, c, projectID)
	if r.Status != http.StatusOK {
		t.Fatalf("list lines: status %d body %s, want 200", r.Status, r.Body)
	}
	var lines []lineJSON
	r.JSON(&lines)
	return lines
}

// payloadFields is a line entry's `fields` as the strings it holds. The
// payload is decoded from jsonb, so the array arrives as []any; a payload
// without one fails the test, because every caller of this is asking which
// fields the entry names.
func payloadFields(t *testing.T, payload map[string]any) []string {
	t.Helper()
	raw, ok := payload["fields"].([]any)
	if !ok {
		t.Fatalf("payload %v carries no 'fields' array", payload)
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		out = append(out, fmt.Sprint(v))
	}
	return out
}

// lastPayloadText is the newest entry of one type as it is actually stored —
// the whole serialised payload, not a decoded field of it. D12's "never an
// amount" can only be proven against the whole thing: a field-name array
// could not hold an amount even if the writer tried to put one somewhere
// else in the payload.
func lastPayloadText(t *testing.T, h *modtest.Harness, projectID int32, eventType string) string {
	t.Helper()
	return modtest.One[string](t, h, `SELECT payload::text FROM projects.timeline_entries
	                                  WHERE project_id = $1 AND event_type = $2 ORDER BY id DESC LIMIT 1`,
		projectID, eventType)
}

// insertLineRow inserts one billing_lines row directly, for the two cases the
// API cannot produce: a line whose variant products no longer knows, and a
// line stored before products was switched off.
func insertLineRow(t *testing.T, h *modtest.Harness, projectID int32, code string, variantID int32) int32 {
	t.Helper()
	return modtest.One[int32](t, h, `
		INSERT INTO projects.billing_lines
			(project_id, code, variant_id, pricing_mode, active, created_at, updated_at)
		VALUES ($1, $2, $3, 'list', true, now(), now())
		RETURNING id`,
		projectID, code, variantID)
}

// D9 and D15 in one exchange: the line names the variant it is pinned to, and
// the response embeds the product's name, SKU and unit so a member never
// needs a products permission to read "Project manager hour". The trackable
// code is the pair a later module quotes, <project>-<line>.
func TestPostProjectsByIdBillingLines_CreatesALinePinnedToAVariant(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "KVEM1000"})

	line := createLine(t, c, project.Id, nil)

	if line.Code != "PM" || line.TrackableCode != "KVEM1000-PM" {
		t.Errorf("Code = %q, TrackableCode = %q, want \"PM\" and \"KVEM1000-PM\"", line.Code, line.TrackableCode)
	}
	if line.VariantId != variantProjectManagerHour || line.VariantMissing {
		t.Errorf("VariantId = %d, VariantMissing = %v, want %d and false", line.VariantId, line.VariantMissing, variantProjectManagerHour)
	}
	if line.ProductName == nil || *line.ProductName != "Project manager hour" ||
		line.Sku == nil || *line.Sku != "PM-H" || line.Unit == nil || *line.Unit != "hour" {
		t.Errorf("ProductName = %v, Sku = %v, Unit = %v, want the catalog's variant details",
			line.ProductName, line.Sku, line.Unit)
	}
	if !line.Active {
		t.Error("Active = false, want a new line to be active")
	}
	if line.Pricing == nil || line.Pricing.Mode != "list" {
		t.Errorf("Pricing = %+v, want mode 'list' for the project's manager", line.Pricing)
	}

	lines := listLines(t, c, project.Id)
	if len(lines) != 1 || lines[0].Id != line.Id || lines[0].TrackableCode != "KVEM1000-PM" {
		t.Errorf("list = %+v, want exactly the line just created", lines)
	}

	// The code is trimmed and upper-cased like the project's own (D2).
	lower := createLine(t, c, project.Id, map[string]any{"code": " dev ", "variantId": variantDeveloperHour})
	if lower.Code != "DEV" || lower.TrackableCode != "KVEM1000-DEV" {
		t.Errorf("Code = %q, TrackableCode = %q, want \"DEV\" and \"KVEM1000-DEV\"", lower.Code, lower.TrackableCode)
	}
}

// The trackable code is computed from the project's code as it stands, not
// stored beside the line: renaming the project (D1) moves every line's
// trackable code with it rather than leaving the old prefix behind.
func TestGetProjectsByIdBillingLines_ProjectCodeChanged_MovesTheTrackableCode(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "OLD1000"})
	line := createLine(t, c, project.Id, nil)
	if line.TrackableCode != "OLD1000-PM" {
		t.Fatalf("TrackableCode = %q, want \"OLD1000-PM\"", line.TrackableCode)
	}

	putProject(t, c, project, map[string]any{"code": "NEW1000"})

	lines := listLines(t, c, project.Id)
	if len(lines) != 1 || lines[0].TrackableCode != "NEW1000-PM" {
		t.Errorf("list = %+v, want the trackable code to follow the project's new code", lines)
	}
}

// Design §4.1's line rules, one case each. A rule that fails names the field
// it is about, because that is what the form renders the message under.
func TestPostProjectsByIdBillingLines_InvalidBody_Returns400OnTheField(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	priced := createProject(t, c, map[string]any{"code": "LVAL1000", "currency": "NOK"})
	unpriced := createProject(t, c, map[string]any{"code": "LVAL1001"})

	cases := []struct {
		name        string
		overrides   map[string]any
		noCurrency  bool
		wantedField string
	}{
		{"blank code", map[string]any{"code": "  "}, false, "code"},
		{"code with a hyphen", map[string]any{"code": "PM-H"}, false, "code"},
		{"code longer than ten characters", map[string]any{"code": "PROJECTMANAGER"}, false, "code"},
		{"variant that does not exist", map[string]any{"variantId": variantUnknown}, false, "variantId"},
		{"blank pricing mode", map[string]any{"pricingMode": ""}, false, "pricingMode"},
		{"pricing mode outside the set", map[string]any{"pricingMode": "hourly"}, false, "pricingMode"},
		{"fixed line without an amount", map[string]any{"pricingMode": "fixed"}, false, "fixedAmount"},
		{"fixed line with an amount of zero", map[string]any{"pricingMode": "fixed", "fixedAmount": 0}, false, "fixedAmount"},
		{"fixed line with a discount percent", map[string]any{"pricingMode": "fixed", "fixedAmount": 900, "discountPercent": 10}, false, "discountPercent"},
		{"fixed line on a project with no currency", map[string]any{"pricingMode": "fixed", "fixedAmount": 900}, true, "pricingMode"},
		{"discount line without a percent", map[string]any{"pricingMode": "discount"}, false, "discountPercent"},
		{"discount of zero", map[string]any{"pricingMode": "discount", "discountPercent": 0}, false, "discountPercent"},
		{"discount above a hundred percent", map[string]any{"pricingMode": "discount", "discountPercent": 100.5}, false, "discountPercent"},
		{"discount line with a fixed amount", map[string]any{"pricingMode": "discount", "discountPercent": 10, "fixedAmount": 900}, false, "fixedAmount"},
		{"list line with a fixed amount", map[string]any{"fixedAmount": 900}, false, "fixedAmount"},
		{"list line with a discount percent", map[string]any{"discountPercent": 10}, false, "discountPercent"},
		{"budget hours of zero", map[string]any{"budgetHours": 0}, false, "budgetHours"},
		{"negative budget hours", map[string]any{"budgetHours": -8}, false, "budgetHours"},
		{"budget amount of zero", map[string]any{"budgetAmount": 0}, false, "budgetAmount"},
		{"negative budget amount", map[string]any{"budgetAmount": -900}, false, "budgetAmount"},
		{"budget amount on a project with no currency", map[string]any{"budgetAmount": 900}, true, "budgetAmount"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			project := priced
			if tc.noCurrency {
				project = unpriced
			}
			r := postLine(t, c, project.Id, tc.overrides)
			if r.Status != http.StatusBadRequest {
				t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
			}
			var problem validationProblemJSON
			r.JSON(&problem)
			if len(problem.Errors[tc.wantedField]) == 0 {
				t.Errorf("errors = %v, want a message on %q", problem.Errors, tc.wantedField)
			}
		})
	}
}

// A line code is unique inside its project and nowhere else: two projects
// both billing "PM" is the normal case, and it is what makes the trackable
// code — project plus line — the identifier that is unique globally.
func TestPostProjectsByIdBillingLines_DuplicateCode_IsRefusedPerProject(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	first := createProject(t, c, map[string]any{"code": "DUP1000"})
	second := createProject(t, c, map[string]any{"code": "DUP1001"})
	createLine(t, c, first.Id, nil)

	r := postLine(t, c, first.Id, map[string]any{"variantId": variantDeveloperHour})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("same code in the same project: status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if len(problem.Errors["code"]) == 0 {
		t.Errorf("errors = %v, want a message on 'code'", problem.Errors)
	}

	if line := createLine(t, c, second.Id, nil); line.TrackableCode != "DUP1001-PM" {
		t.Errorf("TrackableCode = %q, want the same code to be free in another project", line.TrackableCode)
	}
}

// There is no DELETE: a line other modules may already have billed against is
// deactivated, never removed, and it stays listed so the rule behind an old
// hour can still be read. Each direction writes its own timeline entry, and
// the toggle on its own is not a change to the line's fields.
func TestPutProjectsByIdBillingLines_DeactivateAndReactivate_WriteTheirOwnEntries(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "ACT1000"})
	line := createLine(t, c, project.Id, nil)

	deactivated := changeLine(t, c, project.Id, line.Id, lineBody(map[string]any{"active": false}))
	if deactivated.Active {
		t.Error("Active = true, want the line deactivated")
	}
	if payload := lastPayload(t, h, project.Id, "line-deactivated"); payload["code"] != "PM" {
		t.Errorf("line-deactivated payload = %v, want the line's code", payload)
	}
	if types := eventTypes(t, h, project.Id); contains(types, "line-changed") {
		t.Errorf("timeline = %v, want no line-changed entry for a toggle of 'active' alone", types)
	}

	// A deactivated line stays listed: the rule behind an hour logged last
	// month is still the answer to what that hour cost.
	lines := listLines(t, c, project.Id)
	if len(lines) != 1 || lines[0].Active {
		t.Errorf("list = %+v, want the deactivated line to stay listed", lines)
	}

	reactivated := changeLine(t, c, project.Id, line.Id, lineBody(map[string]any{"active": true}))
	if !reactivated.Active {
		t.Error("Active = false, want the line reactivated")
	}
	if payload := lastPayload(t, h, project.Id, "line-reactivated"); payload["code"] != "PM" {
		t.Errorf("line-reactivated payload = %v, want the line's code", payload)
	}

	// A body that leaves 'active' out leaves the line as it stands.
	unchanged := changeLine(t, c, project.Id, line.Id, lineBody(nil))
	if !unchanged.Active {
		t.Error("Active = false, want a body without 'active' to leave it alone")
	}
}

// What one change writes: the fields that moved, by name and never by amount
// (D12), and nothing at all for a change that changed nothing.
func TestPutProjectsByIdBillingLines_RecordsWhichFieldsChanged(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "LCHG1000", "currency": "NOK"})
	line := createLine(t, c, project.Id, nil)

	changed := changeLine(t, c, project.Id, line.Id, lineBody(map[string]any{
		"code": "PM2", "variantId": variantDeveloperHour, "pricingMode": "fixed", "fixedAmount": 900,
	}))
	if changed.Code != "PM2" || changed.TrackableCode != "LCHG1000-PM2" || changed.VariantId != variantDeveloperHour {
		t.Errorf("changed = %+v, want the new code and variant", changed)
	}
	if changed.Pricing == nil || changed.Pricing.Mode != "fixed" ||
		changed.Pricing.FixedAmount == nil || *changed.Pricing.FixedAmount != 900 {
		t.Errorf("Pricing = %+v, want a fixed amount of 900", changed.Pricing)
	}
	payload := lastPayload(t, h, project.Id, "line-changed")
	if payload["code"] != "PM2" {
		t.Errorf("line-changed payload = %v, want the line's code", payload)
	}
	fields := payloadFields(t, payload)
	for _, want := range []string{"code", "variantId", "pricingMode", "fixedAmount"} {
		if !contains(fields, want) {
			t.Errorf("fields = %v, want %q named", fields, want)
		}
	}
	// The whole stored payload, not only the field names: an amount must not
	// reach the timeline anywhere in it (D12).
	raw := lastPayloadText(t, h, project.Id, "line-changed")
	if !strings.Contains(raw, "fixedAmount") {
		t.Errorf("line-changed payload = %s, want the field name in it", raw)
	}
	if strings.Contains(raw, "900") {
		t.Errorf("line-changed payload = %s, want no amount in it (900 leaked)", raw)
	}

	before := eventTypes(t, h, project.Id)
	changeLine(t, c, project.Id, line.Id, lineBody(map[string]any{
		"code": "PM2", "variantId": variantDeveloperHour, "pricingMode": "fixed", "fixedAmount": 900,
	}))
	if after := eventTypes(t, h, project.Id); len(after) != len(before) {
		t.Errorf("timeline = %v, want no entry for a change that changed nothing (was %v)", after, before)
	}
}

// D12: a member sees the lines — the product, the unit, which rule applies —
// and the pricing block is simply not in their copy. The assertion is on the
// raw JSON, because a nil pointer cannot tell an absent key from a null one.
func TestGetProjectsByIdBillingLines_Member_SeesNoPricingKey(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "LMEM1000", "currency": "NOK"})
	createLine(t, c, project.Id, map[string]any{
		"pricingMode": "fixed", "fixedAmount": 900, "budgetHours": 40, "budgetAmount": 5000,
	})
	member, memberID := signIn(t, h)
	addRole(t, h, project.Id, memberID, "member")

	r := readLines(t, member, project.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var raw []map[string]any
	if err := json.Unmarshal(r.Body, &raw); err != nil {
		t.Fatalf("decode %s: %v", r.Body, err)
	}
	if len(raw) != 1 {
		t.Fatalf("body %s, want exactly one line", r.Body)
	}
	if _, present := raw[0]["pricing"]; present {
		t.Errorf("body %s carries a 'pricing' key, want it absent (not null) for a caller who may not see it", r.Body)
	}
	if raw[0]["productName"] != "Project manager hour" {
		t.Errorf("body %s, want the variant details a member may read (D15)", r.Body)
	}
	// budgetHours is planning data, outside the financial shaping: a member
	// sees it even though budgetAmount (inside the absent pricing block) is
	// not theirs to see.
	if raw[0]["budgetHours"] != 40.0 {
		t.Errorf("budgetHours = %v, want 40 visible to a member", raw[0]["budgetHours"])
	}
}

// The line budgets (design §3.1): budgetHours is planning data returned
// alongside every other field a caller who sees the project sees; budgetAmount
// is an amount, so it rides inside the line's existing financial shaping.
// Omitting them on a later PUT clears them, the same full-replace semantics
// every other optional line field already has.
func TestBillingLines_Budgets_StoredReturnedAndCleared(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "LBUD1000", "currency": "NOK"})

	line := createLine(t, c, project.Id, map[string]any{"budgetHours": 40, "budgetAmount": 5000})
	if line.BudgetHours == nil || *line.BudgetHours != 40 {
		t.Errorf("BudgetHours = %v, want 40", line.BudgetHours)
	}
	if line.Pricing == nil || line.Pricing.BudgetAmount == nil || *line.Pricing.BudgetAmount != 5000 {
		t.Errorf("Pricing = %+v, want a budgetAmount of 5000", line.Pricing)
	}

	reread := listLines(t, c, project.Id)
	if len(reread) != 1 || reread[0].BudgetHours == nil || *reread[0].BudgetHours != 40 {
		t.Errorf("re-read = %+v, want the stored budgetHours", reread)
	}

	cleared := changeLine(t, c, project.Id, line.Id, lineBody(nil))
	if cleared.BudgetHours != nil {
		t.Errorf("BudgetHours = %v, want cleared by a body that omits it", cleared.BudgetHours)
	}
	if cleared.Pricing == nil || cleared.Pricing.BudgetAmount != nil {
		t.Errorf("Pricing = %+v, want budgetAmount cleared", cleared.Pricing)
	}
}

// budgetAmount lives inside pricing, not at the line's top level: a caller
// with financial rights sees it there and nowhere else. The assertion is on
// raw JSON, the only way to tell "not present at the top level" from "never
// serialised at all".
func TestBillingLines_BudgetAmount_LivesInsidePricingNotAtTheTopLevel(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "LBUD1001", "currency": "NOK"})
	createLine(t, c, project.Id, map[string]any{"budgetAmount": 5000})

	r := readLines(t, c, project.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var raw []map[string]any
	if err := json.Unmarshal(r.Body, &raw); err != nil {
		t.Fatalf("decode %s: %v", r.Body, err)
	}
	if len(raw) != 1 {
		t.Fatalf("body %s, want exactly one line", r.Body)
	}
	if _, present := raw[0]["budgetAmount"]; present {
		t.Errorf("body %s carries a top-level 'budgetAmount', want it only inside 'pricing'", r.Body)
	}
	pricing, ok := raw[0]["pricing"].(map[string]any)
	if !ok {
		t.Fatalf("body %s, want a 'pricing' object for a manager", r.Body)
	}
	if pricing["budgetAmount"] != 5000.0 {
		t.Errorf("pricing.budgetAmount = %v, want 5000", pricing["budgetAmount"])
	}
}

// A PUT that changes only a budget still writes line-changed: budgetHours
// and budgetAmount are fields like any other, named (never valued, D12) in
// the timeline, and a change that touched only them must not read as if
// nothing happened.
func TestPutProjectsByIdBillingLines_ChangingOnlyABudget_WritesLineChanged(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "LBUDCHG1000", "currency": "NOK"})
	line := createLine(t, c, project.Id, map[string]any{"budgetHours": 40, "budgetAmount": 5000})

	changeLine(t, c, project.Id, line.Id, lineBody(map[string]any{"budgetHours": 80, "budgetAmount": 10000}))

	if got := eventTypes(t, h, project.Id); len(got) != 3 || got[2] != "line-changed" {
		t.Fatalf("timeline = %v, want [project-created line-added line-changed]", got)
	}
	payload := lastPayload(t, h, project.Id, "line-changed")
	fields := payloadFields(t, payload)
	for _, want := range []string{"budgetHours", "budgetAmount"} {
		if !contains(fields, want) {
			t.Errorf("fields = %v, want %q named", fields, want)
		}
	}
	raw := lastPayloadText(t, h, project.Id, "line-changed")
	for _, leaked := range []string{"80", "10000"} {
		if strings.Contains(raw, leaked) {
			t.Errorf("line-changed payload = %s, want no amount in it (%s leaked)", raw, leaked)
		}
	}
}

// D13's guard extended past a 'fixed' line (design §3.3): a milestone that is
// not cancelled also denominates an amount in the project's currency, so the
// currency cannot be cleared while one exists — but a cancelled milestone
// bills nothing and does not lock it. milestones.go is Task 2's; the row is
// inserted directly (insertMilestone, harness_test.go).
func TestPutProjectsById_ClearingTheCurrencyWithAMilestone_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "MCUR1000", "currency": "NOK"})
	insertMilestone(t, h, project.Id, nil)

	r := updateProject(t, c, project, map[string]any{"currency": nil})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if len(problem.Errors["currency"]) == 0 {
		t.Errorf("errors = %v, want a message on 'currency'", problem.Errors)
	}
}

func TestPutProjectsById_ClearingTheCurrencyWithOnlyACancelledMilestone_IsAllowed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "MCUR1001", "currency": "NOK"})
	insertMilestone(t, h, project.Id, map[string]any{"status": "cancelled"})

	if cleared := putProject(t, c, project, map[string]any{"currency": nil}); cleared.Financials == nil || cleared.Financials.Currency != nil {
		t.Errorf("Financials = %+v, want the currency cleared with only a cancelled milestone", cleared.Financials)
	}
}

// D13's guard, its other new trigger: a line's own budget amount is an
// amount in the project's currency exactly as a 'fixed' line's price is.
func TestPutProjectsById_ClearingTheCurrencyWithABudgetedLine_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "BCUR1000", "currency": "NOK"})
	createLine(t, c, project.Id, map[string]any{"budgetAmount": 5000})

	r := updateProject(t, c, project, map[string]any{"currency": nil})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if len(problem.Errors["currency"]) == 0 {
		t.Errorf("errors = %v, want a message on 'currency'", problem.Errors)
	}
}

// The guard's trigger is "changed or cleared" (`!equalStringPtr`), not just
// "cleared": swapping NOK for SEK would silently reprice a milestone or a
// budgeted line exactly as clearing the currency would.
func TestPutProjectsById_ChangingTheCurrencyWithAMilestone_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "MCUR1002", "currency": "NOK"})
	insertMilestone(t, h, project.Id, nil)

	r := updateProject(t, c, project, map[string]any{"currency": "SEK"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if len(problem.Errors["currency"]) == 0 {
		t.Errorf("errors = %v, want a message on 'currency'", problem.Errors)
	}
}

func TestPutProjectsById_ChangingTheCurrencyWithABudgetedLine_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "BCUR1001", "currency": "NOK"})
	createLine(t, c, project.Id, map[string]any{"budgetAmount": 5000})

	r := updateProject(t, c, project, map[string]any{"currency": "SEK"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if len(problem.Errors["currency"]) == 0 {
		t.Errorf("errors = %v, want a message on 'currency'", problem.Errors)
	}
}

// The list price is resolved through ProductCatalog in the project's own
// currency at the moment of the read (D13): a project with no currency, or
// one in a currency the variant has no price in, simply has no listPrice.
func TestGetProjectsByIdBillingLines_ListPrice_FollowsTheProjectCurrency(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	cases := []struct {
		name     string
		code     string
		currency any
		want     *moneyJSON
	}{
		{"the catalog's currency", "LPRI1000", "NOK", &moneyJSON{Amount: 1600, Currency: "NOK"}},
		{"another currency", "LPRI1001", "EUR", nil},
		{"no currency at all", "LPRI1002", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			project := createProject(t, c, map[string]any{"code": tc.code, "currency": tc.currency})
			line := createLine(t, c, project.Id, nil)
			if line.Pricing == nil {
				t.Fatalf("Pricing is absent, want it present for the project's manager")
			}
			switch {
			case tc.want == nil && line.Pricing.ListPrice != nil:
				t.Errorf("ListPrice = %+v, want none", line.Pricing.ListPrice)
			case tc.want != nil && (line.Pricing.ListPrice == nil ||
				line.Pricing.ListPrice.Amount != tc.want.Amount || line.Pricing.ListPrice.Currency != tc.want.Currency):
				t.Errorf("ListPrice = %+v, want %+v", line.Pricing.ListPrice, tc.want)
			}
		})
	}
}

// view-financials sees the pricing of every project it can see, without
// managing any of them: the same shaping the project's own financials get.
func TestGetProjectsByIdBillingLines_ViewFinancials_SeesThePricing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "LFIN1000", "currency": "NOK"})
	createLine(t, c, project.Id, map[string]any{"pricingMode": "discount", "discountPercent": 12.5})
	viewer, _ := signIn(t, h, "projects:view-all", "projects:view-financials")

	lines := listLines(t, viewer, project.Id)
	if len(lines) != 1 || lines[0].Pricing == nil {
		t.Fatalf("lines = %+v, want the pricing present for projects:view-financials", lines)
	}
	pricing := lines[0].Pricing
	if pricing.Mode != "discount" || pricing.DiscountPercent == nil || *pricing.DiscountPercent != 12.5 {
		t.Errorf("Pricing = %+v, want a 12.5%% discount", pricing)
	}
	if pricing.ListPrice == nil || pricing.ListPrice.Amount != 1600 || pricing.ListPrice.Currency != "NOK" {
		t.Errorf("ListPrice = %+v, want 1600 NOK, the price the discount applies to", pricing.ListPrice)
	}
}

// A variant products no longer knows does not take the line with it: the row
// is what an already-billed hour resolves through, so it stays listed and
// says that its variant is gone rather than inventing a name.
func TestGetProjectsByIdBillingLines_UnknownVariant_IsListedAsMissing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "LGONE1000"})
	lineID := insertLineRow(t, h, project.Id, "OLD", variantUnknown)

	lines := listLines(t, c, project.Id)
	if len(lines) != 1 || lines[0].Id != lineID {
		t.Fatalf("lines = %+v, want the line whose variant is gone to stay listed", lines)
	}
	if !lines[0].VariantMissing {
		t.Errorf("VariantMissing = false, want true for a variant the catalog no longer knows")
	}
	if lines[0].ProductName != nil || lines[0].Sku != nil || lines[0].Unit != nil {
		t.Errorf("line = %+v, want no product name, SKU or unit for a missing variant", lines[0])
	}
	if lines[0].TrackableCode != "LGONE1000-OLD" {
		t.Errorf("TrackableCode = %q, want it built from the project and line codes all the same", lines[0].TrackableCode)
	}
}

// Writing a line is the manager's job; reading them is everyone's who sees
// the project. An outsider gets the bare 404 every operation on a project
// they cannot see gets (D7), never a 403 that would confirm it exists.
func TestBillingLines_Authorization_MemberIsForbiddenAndOutsiderIsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "LAUTH1000"})
	line := createLine(t, c, project.Id, nil)
	member, memberID := signIn(t, h)
	addRole(t, h, project.Id, memberID, "member")
	outsider, _ := signIn(t, h)

	if r := postLine(t, member, project.Id, map[string]any{"code": "DEV"}); r.Status != http.StatusForbidden {
		t.Errorf("member POST: status %d body %s, want 403", r.Status, r.Body)
	}
	if r := putLine(t, member, project.Id, line.Id, lineBody(nil)); r.Status != http.StatusForbidden {
		t.Errorf("member PUT: status %d body %s, want 403", r.Status, r.Body)
	}
	if r := readLines(t, member, project.Id); r.Status != http.StatusOK {
		t.Errorf("member GET: status %d body %s, want 200", r.Status, r.Body)
	}

	for _, r := range []*modtest.Response{
		readLines(t, outsider, project.Id),
		postLine(t, outsider, project.Id, map[string]any{"code": "DEV"}),
		putLine(t, outsider, project.Id, line.Id, lineBody(nil)),
	} {
		if r.Status != http.StatusNotFound {
			t.Errorf("outsider: status %d body %s, want 404", r.Status, r.Body)
		}
	}

	// A line id belonging to another project is a 404 too, not somebody
	// else's line.
	other := createProject(t, c, map[string]any{"code": "LAUTH1001"})
	if r := putLine(t, c, other.Id, line.Id, lineBody(nil)); r.Status != http.StatusNotFound {
		t.Errorf("line of another project: status %d body %s, want 404", r.Status, r.Body)
	}
}

// D10, the optional contract's absent case: with products off every line
// operation is 409, the project says so through billingLinesAvailable, and
// the lines stored while it was on stay stored — hidden, not deleted.
func TestBillingLines_WithoutProducts_Return409AndLeaveStoredLinesAlone(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutProducts(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "NOPROD1000"})
	lineID := insertLineRow(t, h, project.Id, "PM", variantProjectManagerHour)

	for _, r := range []*modtest.Response{
		readLines(t, c, project.Id),
		postLine(t, c, project.Id, map[string]any{"code": "DEV"}),
		putLine(t, c, project.Id, lineID, lineBody(nil)),
	} {
		if r.Status != http.StatusConflict {
			t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
		}
		var problem problemJSON
		r.JSON(&problem)
		if problem.Title != "Products module not enabled" {
			t.Errorf("title = %q, want %q", problem.Title, "Products module not enabled")
		}
	}

	if got := h.Count(t, `SELECT count(*) FROM projects.billing_lines WHERE project_id = $1`, project.Id); got != 1 {
		t.Errorf("stored lines = %d, want the line stored before products was switched off to stay", got)
	}

	r := getProject(t, c, project.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("get project: status %d body %s, want 200", r.Status, r.Body)
	}
	var read projectJSON
	r.JSON(&read)
	if read.BillingLinesAvailable {
		t.Error("BillingLinesAvailable = true, want false with products disabled")
	}
}

// The 409 sits behind the access gates, not in front of them (D7/D10): a
// stranger must not be able to learn that a project exists — or that this
// installation has no products module — by getting a conflict where the
// answer should have been "there is nothing here", and a member must still be
// told they may not write.
func TestBillingLines_WithoutProducts_AnswerTheAccessGatesFirst(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutProducts(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "NOPROD1001"})
	lineID := insertLineRow(t, h, project.Id, "PM", variantProjectManagerHour)
	outsider, _ := signIn(t, h)
	member, memberID := signIn(t, h)
	addRole(t, h, project.Id, memberID, "member")

	for _, r := range []*modtest.Response{
		readLines(t, outsider, project.Id),
		postLine(t, outsider, project.Id, map[string]any{"code": "DEV"}),
		putLine(t, outsider, project.Id, lineID, lineBody(nil)),
	} {
		if r.Status != http.StatusNotFound {
			t.Errorf("outsider: status %d body %s, want 404 rather than the products 409", r.Status, r.Body)
		}
	}

	for _, r := range []*modtest.Response{
		postLine(t, member, project.Id, map[string]any{"code": "DEV"}),
		putLine(t, member, project.Id, lineID, lineBody(nil)),
	} {
		if r.Status != http.StatusForbidden {
			t.Errorf("member write: status %d body %s, want 403 rather than the products 409", r.Status, r.Body)
		}
	}
	// A member may read the project's lines when products is on, so what
	// stops them here is products, not their role.
	if r := readLines(t, member, project.Id); r.Status != http.StatusConflict {
		t.Errorf("member GET: status %d body %s, want 409", r.Status, r.Body)
	}
}

// The unique index guards a rename as much as an insert: moving a line onto a
// sibling's code is the same collision, answered the same way.
func TestPutProjectsByIdBillingLines_RenamingOntoASiblingsCode_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "LDUP1000"})
	createLine(t, c, project.Id, nil)
	developer := createLine(t, c, project.Id, map[string]any{"code": "DEV", "variantId": variantDeveloperHour})

	r := putLine(t, c, project.Id, developer.Id, lineBody(map[string]any{"variantId": variantDeveloperHour}))
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if len(problem.Errors["code"]) == 0 {
		t.Errorf("errors = %v, want a message on 'code'", problem.Errors)
	}
	if got := modtest.One[string](t, h, `SELECT code FROM projects.billing_lines WHERE id = $1`, developer.Id); got != "DEV" {
		t.Errorf("stored code = %q, want the refused rename to have left it alone", got)
	}
}

// A manager must still be able to edit — above all, to deactivate — a line
// whose product has since been removed from the catalog. The variant is only
// validated when the caller is actually changing it: a body that carries the
// variant the line already has is not asking for that variant to exist today.
func TestPutProjectsByIdBillingLines_VariantGoneFromTheCatalog_StillEditable(t *testing.T) {
	t.Parallel()
	catalog := newFakeCatalog()
	h := newHarnessWithCatalog(t, catalog)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "LFORGET1000"})
	line := createLine(t, c, project.Id, nil)

	catalog.forget(variantProjectManagerHour)

	deactivated := changeLine(t, c, project.Id, line.Id, lineBody(map[string]any{"active": false}))
	if deactivated.Active {
		t.Error("Active = true, want the line deactivated")
	}
	if !deactivated.VariantMissing || deactivated.ProductName != nil {
		t.Errorf("line = %+v, want variantMissing and no product name once the catalog has forgotten the variant", deactivated)
	}
	if payload := lastPayload(t, h, project.Id, "line-deactivated"); payload["code"] != "PM" {
		t.Errorf("line-deactivated payload = %v, want the line's code", payload)
	}

	// Moving the line to a variant that never existed is still refused: the
	// rule is about changing the variant, not about skipping the check.
	r := putLine(t, c, project.Id, line.Id, lineBody(map[string]any{"variantId": variantUnknown, "active": true}))
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if len(problem.Errors["variantId"]) == 0 {
		t.Errorf("errors = %v, want a message on 'variantId'", problem.Errors)
	}
	// That refusal is decided inside the change's own transaction, so it has
	// to leave the line exactly as it stood — the reactivation the same body
	// asked for included.
	stored := modtest.One[int32](t, h, `SELECT variant_id FROM projects.billing_lines WHERE id = $1`, line.Id)
	if stored != variantProjectManagerHour {
		t.Errorf("stored variant = %d, want the refused change to have left %d", stored, variantProjectManagerHour)
	}
	if active := modtest.One[bool](t, h, `SELECT active FROM projects.billing_lines WHERE id = $1`, line.Id); active {
		t.Error("stored active = true, want the refused change rolled back whole")
	}
}

// D13's other half, enforced where the currency is actually cleared: a
// project carrying a fixed line has an amount denominated in its currency, so
// the currency cannot go. A list line prices itself in whatever currency the
// project has, so it holds nothing back.
func TestPutProjectsById_ClearingTheCurrencyWithAFixedLine_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "LCUR1000", "currency": "NOK"})
	createLine(t, c, project.Id, map[string]any{"pricingMode": "fixed", "fixedAmount": 900})

	r := updateProject(t, c, project, map[string]any{"currency": nil})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if len(problem.Errors["currency"]) == 0 {
		t.Errorf("errors = %v, want a message on 'currency'", problem.Errors)
	}

	listOnly := createProject(t, c, map[string]any{"code": "LCUR1001", "currency": "NOK"})
	createLine(t, c, listOnly.Id, nil)
	if cleared := putProject(t, c, listOnly, map[string]any{"currency": nil}); cleared.Financials == nil || cleared.Financials.Currency != nil {
		t.Errorf("Financials = %+v, want the currency cleared on a project with only a list line", cleared.Financials)
	}
}

// The same half of D13, on the change rather than the clear: a fixed line's
// amount is denominated in the currency the project had when it was priced,
// so moving the project to another currency would silently reprice it. A
// deactivated line counts, as it does everywhere else — it can be
// reactivated, and its amount is still in the currency it was typed in.
func TestPutProjectsById_ChangingTheCurrencyWithAFixedLine_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "CCUR1000", "currency": "NOK"})
	line := createLine(t, c, project.Id, map[string]any{"pricingMode": "fixed", "fixedAmount": 900})

	r := updateProject(t, c, project, map[string]any{"currency": "EUR"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if len(problem.Errors["currency"]) == 0 {
		t.Errorf("errors = %v, want a message on 'currency'", problem.Errors)
	}

	listOnly := createProject(t, c, map[string]any{"code": "CCUR1001", "currency": "NOK"})
	createLine(t, c, listOnly.Id, nil)
	moved := putProject(t, c, listOnly, map[string]any{"currency": "EUR"})
	if moved.Financials == nil || moved.Financials.Currency == nil || *moved.Financials.Currency != "EUR" {
		t.Errorf("Financials = %+v, want the currency changed on a project with only a list line", moved.Financials)
	}

	changeLine(t, c, project.Id, line.Id, lineBody(map[string]any{
		"pricingMode": "fixed", "fixedAmount": 900, "active": false}))
	if again := updateProject(t, c, project, map[string]any{"currency": "EUR"}); again.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s after deactivating the line, want 400", again.Status, again.Body)
	}
}

// The line a manager creates through the API is the line other modules
// resolve through contracts.ProjectDirectory: one row, one rule, read by id.
func TestDirectory_BillingLine_ResolvesALineCreatedThroughTheAPI(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "LDIR1000", "currency": "NOK"})
	line := createLine(t, c, project.Id, map[string]any{"pricingMode": "fixed", "fixedAmount": 1450.5})

	got, err := newDirectory(t, h).BillingLine(context.Background(), project.Id, line.Id)
	if err != nil {
		t.Fatalf("BillingLine: %v", err)
	}
	if got == nil || got.ID != line.Id || got.ProjectID != project.Id || got.Code != "PM" ||
		got.VariantID != variantProjectManagerHour || got.PricingMode != "fixed" ||
		got.FixedAmount == nil || *got.FixedAmount != 1450.5 || got.DiscountPercent != nil || !got.Active {
		t.Errorf("BillingLine = %+v, want the line created through the API", got)
	}
}

// The line's own timeline entry, written in the same transaction as the
// insert: the code it was created under and which of its fields were set,
// never what they were set to (D12).
func TestPostProjectsByIdBillingLines_WritesTheLineAddedEntry(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "LADD1000", "currency": "NOK"})

	createLine(t, c, project.Id, map[string]any{"pricingMode": "fixed", "fixedAmount": 900})

	payload := lastPayload(t, h, project.Id, "line-added")
	if payload["code"] != "PM" {
		t.Errorf("line-added payload = %v, want the line's code", payload)
	}
	fields := payloadFields(t, payload)
	if !contains(fields, "variantId") || !contains(fields, "pricingMode") || !contains(fields, "fixedAmount") {
		t.Errorf("fields = %v, want the fields the line was created with, by name", fields)
	}
	raw := lastPayloadText(t, h, project.Id, "line-added")
	if strings.Contains(raw, "900") {
		t.Errorf("line-added payload = %s, want no amount in it (900 leaked)", raw)
	}
}

// An amount too large for its column is a bad body, not a broken server: the
// upper bounds are Go's, so an oversized budget answers 400 on the field
// rather than letting Postgres raise a 22003 the handler can only turn into a
// 500. The project's own budget fields carry the same bounds for the same
// reason.
func TestBillingLines_BudgetsPastTheirColumns_Return400OnTheField(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "LIMIT1000", "currency": "NOK"})

	for _, tc := range []struct {
		name      string
		overrides map[string]any
		field     string
	}{
		{"a budget amount past numeric(12,2)", map[string]any{"budgetAmount": 10000000000.00}, "budgetAmount"},
		{"budget hours past numeric(10,2)", map[string]any{"budgetHours": 100000000.00}, "budgetHours"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := postLine(t, c, project.Id, tc.overrides)
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

// The other half of eb894d4's prefetch: asking the catalog before the
// transaction must not make its *error* fatal to a change that never needed
// the answer. Keeping the variant a line is already pinned to is always
// allowed — "a line whose product is gone must stay editable, not least to be
// deactivated" — and a catalog that is erroring rather than answering "gone"
// has to be treated the same way, or the one moment a manager most needs to
// switch a line off is the moment they cannot.
func TestPutProjectsByIdBillingLines_CatalogDown_StillChangesAnUnchangedVariant(t *testing.T) {
	t.Parallel()
	catalog := newFakeCatalog()
	h := newHarnessWithCatalog(t, catalog)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "CATDOWN1"})
	line := createLine(t, c, project.Id, nil)

	catalog.fail(errors.New("products: the catalog is unavailable"))

	// Same variant, only `active` flipped: the catalog's answer is irrelevant.
	off := changeLine(t, c, project.Id, line.Id, lineBody(map[string]any{"active": false}))
	if off.Active {
		t.Errorf("Active = true, want the line switched off while the catalog is down")
	}

	// Moving the line to another variant does need the answer, so the failure
	// surfaces there and nowhere else. The exchange is off-contract on
	// purpose — a 500 is not a declared response — so it opts out of the
	// recorder rather than being validated against a status the contract
	// rightly does not promise.
	r := putLine(t, c, project.Id, line.Id, lineBody(map[string]any{"variantId": variantDeveloperHour}),
		modtest.SkipContract("a degraded catalog is an infrastructure failure, deliberately off-contract"))
	if r.Status != http.StatusInternalServerError {
		t.Errorf("changing the variant: status %d body %s, want 500", r.Status, r.Body)
	}
	if again := listLines(t, c, project.Id); again[0].VariantId != variantProjectManagerHour {
		t.Errorf("VariantId = %d, want the line left on its own variant", again[0].VariantId)
	}
}

// catalogUnavailableWarning is the one line the module logs per request whose
// billing lines could not be rendered in full. Tests read it to prove the
// degradation is *reported* rather than merely survived: a response quietly
// missing a product name is how an outage goes unnoticed for a week.
const catalogUnavailableWarning = "projects: the product catalog could not be read"

// A catalog that errors while a line is being *rendered* must not fail the
// request. Before this, a change that never needed the catalog's answer — a
// deactivation — was written, committed, and then answered 500 by the
// renderer, so the client retried and got a 409 on a change that had already
// been made. The write is the same as it always was; what changes is that the
// answer comes back without the fields the catalog would have supplied, and
// says so.
func TestPutProjectsByIdBillingLines_CatalogDownWhileRendering_Returns200WithoutTheCatalogsFields(t *testing.T) {
	t.Parallel()
	catalog := newFakeCatalog()
	h := newHarnessWithCatalog(t, catalog)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "RENDER1000", "currency": "NOK"})
	line := createLine(t, c, project.Id, nil)

	catalog.fail(errors.New("products: the catalog is unavailable"))

	off := changeLine(t, c, project.Id, line.Id, lineBody(map[string]any{"active": false}))
	if !off.CatalogUnavailable {
		t.Error("CatalogUnavailable = false, want the answer to say the catalog could not be read")
	}
	if off.ProductName != nil || off.Sku != nil || off.Unit != nil {
		t.Errorf("line = %+v, want no catalog-derived fields while the catalog is down", off)
	}
	// "The catalog could not be read" is not "the catalog no longer knows this
	// variant": nothing was answered, so nothing is claimed.
	if off.VariantMissing {
		t.Error("VariantMissing = true, want false — the catalog was never asked")
	}
	// Everything the line itself stores still comes back, the write included.
	if off.Active {
		t.Error("Active = true, want the line switched off")
	}
	if off.Pricing == nil || off.Pricing.Mode != "list" {
		t.Errorf("Pricing = %+v, want the stored rule regardless of the catalog", off.Pricing)
	} else if off.Pricing.ListPrice != nil {
		t.Errorf("ListPrice = %+v, want it left out while the catalog is down", off.Pricing.ListPrice)
	}
	if !strings.Contains(h.Logs(), catalogUnavailableWarning) {
		t.Errorf("nothing was logged about the catalog the request could not read:\n%s", h.Logs())
	}
}

// The same rule on the read. A list of lines is the surface a degraded catalog
// is most visible on — every line of it wants a name and a price — and a
// caller who can no longer see their own project's lines at all is worse off
// than one who sees them without their product names.
func TestGetProjectsByIdBillingLines_CatalogDownWhileRendering_Returns200WithoutTheCatalogsFields(t *testing.T) {
	t.Parallel()
	catalog := newFakeCatalog()
	h := newHarnessWithCatalog(t, catalog)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "RENDER1001", "currency": "NOK"})
	createLine(t, c, project.Id, nil)
	createLine(t, c, project.Id, map[string]any{"code": "DEV", "variantId": variantDeveloperHour})

	catalog.fail(errors.New("products: the catalog is unavailable"))

	lines := listLines(t, c, project.Id)
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want both of them", len(lines))
	}
	for _, line := range lines {
		if !line.CatalogUnavailable {
			t.Errorf("line %q: CatalogUnavailable = false, want the read to say so", line.Code)
		}
		if line.ProductName != nil || line.Sku != nil || line.Unit != nil || line.VariantMissing {
			t.Errorf("line %q = %+v, want no catalog-derived fields and no claim about the variant", line.Code, line)
		}
		if line.Pricing == nil || line.Pricing.ListPrice != nil {
			t.Errorf("line %q: Pricing = %+v, want the stored rule and no list price", line.Code, line.Pricing)
		}
	}
	// One warning for the request, not one per line: an outage that fills the
	// log with a line per row buries itself.
	if n := strings.Count(h.Logs(), catalogUnavailableWarning); n != 1 {
		t.Errorf("logged the catalog warning %d times for one request, want exactly 1:\n%s", n, h.Logs())
	}
}

// The other side of the ruling, and the one that must not move: a catalog
// error while *validating* a variant the request is actually moving the line
// to stays a 5xx. The write turns on an answer nobody gave, so it must not
// proceed — degrading a read is honest, degrading a decision is not. The
// exchange is off-contract on purpose, exactly as the change's own case is.
func TestPostProjectsByIdBillingLines_CatalogDownWhileValidating_Returns500(t *testing.T) {
	t.Parallel()
	catalog := newFakeCatalog()
	h := newHarnessWithCatalog(t, catalog)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "RENDER1002", "currency": "NOK"})

	catalog.fail(errors.New("products: the catalog is unavailable"))

	r := c.Do(http.MethodPost, linesPath(project.Id), lineBody(nil),
		modtest.SkipContract("a degraded catalog is an infrastructure failure, deliberately off-contract"))
	if r.Status != http.StatusInternalServerError {
		t.Fatalf("status %d body %s, want 500", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM projects.billing_lines WHERE project_id = $1`, project.Id); n != 0 {
		t.Errorf("%d line(s) stored, want the create refused rather than written against an unknown variant", n)
	}
}
