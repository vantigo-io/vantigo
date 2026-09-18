package projects_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
	Id             int32        `json:"id"`
	Code           string       `json:"code"`
	TrackableCode  string       `json:"trackableCode"`
	VariantId      int32        `json:"variantId"`
	ProductName    *string      `json:"productName"`
	Sku            *string      `json:"sku"`
	Unit           *string      `json:"unit"`
	VariantMissing bool         `json:"variantMissing"`
	Active         bool         `json:"active"`
	CreatedAt      time.Time    `json:"createdAt"`
	UpdatedAt      time.Time    `json:"updatedAt"`
	Pricing        *pricingJSON `json:"pricing"`
}

type pricingJSON struct {
	Mode            string     `json:"mode"`
	FixedAmount     *float64   `json:"fixedAmount"`
	DiscountPercent *float64   `json:"discountPercent"`
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

func putLine(t *testing.T, c *modtest.Client, projectID, lineID int32, body map[string]any) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, linePath(projectID, lineID), body)
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
// without one answers nothing rather than failing, because "which fields does
// this entry name" is the question every caller of it is asking.
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
	if contains(fields, "900") {
		t.Errorf("fields = %v, want field names only and never an amount (D12)", fields)
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
	createLine(t, c, project.Id, map[string]any{"pricingMode": "fixed", "fixedAmount": 900})
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
	if contains(fields, "900") {
		t.Errorf("fields = %v, want field names only and never an amount (D12)", fields)
	}
}
