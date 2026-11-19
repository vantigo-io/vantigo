package customers_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the default brand of a customer and of a customer group (brands
// design B28, delivery E1): PUT /customers/{id}/brand, the brandId of a group,
// GET /customers/brands, the brand block on every customer response, and the
// two ids the customer directory carries. The harness composes the real
// identity beside this module, so contracts.Brands is identity's own; a test
// makes a brand by writing its row, as a published or archived brand reads.

type brandChoiceJSON struct {
	Id    uuid.UUID `json:"id"`
	Name  *string   `json:"name"`
	State string    `json:"state"`
}

type brandInfoJSON struct {
	Own       *brandChoiceJSON `json:"own"`
	Group     *brandChoiceJSON `json:"group"`
	Effective struct {
		Id     uuid.UUID `json:"id"`
		Name   *string   `json:"name"`
		Source string    `json:"source"`
	} `json:"effective"`
	Unavailable []string `json:"unavailable"`
}

type brandedCustomerJSON struct {
	Id       int32          `json:"id"`
	Revision int32          `json:"revision"`
	Brand    *brandInfoJSON `json:"brand"`
}

type brandOptionJSON struct {
	Id        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	IsDefault bool      `json:"isDefault"`
}

// insertBrand writes a brand of the given state ("published", "archived" or
// "draft"), named name, and returns its id. A draft has no published version.
func insertBrand(t *testing.T, h *modtest.Harness, name, state string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	version := fmt.Sprintf(`{"name":%q,"title":%q,"primary":"#0f766e","radius":"md","support":{},"logos":{},"algorithm":2,"tokens":{}}`, name, name)
	var published any
	if state != "draft" {
		published = version
	}
	h.Exec(t, `INSERT INTO identity.brands (id, state, published, published_version, draft, name_key, created_at, updated_at)
	           VALUES ($1, $2, $3::jsonb, $4, $5::jsonb, lower($6), $7, $7)`,
		id, state, published, map[bool]int{true: 1, false: 0}[state != "draft"], version, name, h.Now())
	return id
}

func setBrandState(t *testing.T, h *modtest.Harness, id uuid.UUID, state string) {
	t.Helper()
	h.Exec(t, `UPDATE identity.brands SET state = $2 WHERE id = $1`, id, state)
}

func putCustomerBrand(t *testing.T, c *modtest.Client, id int32, body map[string]any) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/brand", id), body)
}

func brandOf(t *testing.T, r *modtest.Response) brandedCustomerJSON {
	t.Helper()
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var out brandedCustomerJSON
	r.JSON(&out)
	if out.Brand == nil {
		t.Fatalf("the response has no brand block: %s", r.Body)
	}
	return out
}

func fetchBranded(t *testing.T, c *modtest.Client, id int32) brandedCustomerJSON {
	t.Helper()
	return brandOf(t, c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d", id), nil))
}

// A customer with no brand and no group takes the workspace default brand, and
// says where it comes from.
func TestCustomerBrand_DefaultsToTheWorkspaceBrand(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Plain Co")

	got := fetchBranded(t, c, customer.Id)
	if got.Brand.Own != nil || got.Brand.Group != nil {
		t.Errorf("own/group = %+v/%+v, want none", got.Brand.Own, got.Brand.Group)
	}
	if got.Brand.Effective.Source != "default" || got.Brand.Effective.Name == nil {
		t.Errorf("effective = %+v, want the workspace default", got.Brand.Effective)
	}
	if len(got.Brand.Unavailable) != 0 {
		t.Errorf("unavailable = %v, want none", got.Brand.Unavailable)
	}
}

// The order is the customer's own brand, then its group's, then the workspace
// default (B28), and clearing a level hands the choice to the next.
func TestCustomerBrand_ResolutionOrderCustomerThenGroupThenDefault(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	nordlys := insertBrand(t, h, "Nordlys", "published")
	fjord := insertBrand(t, h, "Fjordkraft Demo", "published")
	group := createGroup(t, c, map[string]any{"name": "Retail", "brandId": fjord})
	customer := createCustomer(t, c, "Ordered Co")
	if r := putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": group.Id}); r.Status != http.StatusOK {
		t.Fatalf("put in the group: %d %s", r.Status, r.Body)
	}

	// Group only: the group's brand applies.
	got := fetchBranded(t, c, customer.Id)
	if got.Brand.Effective.Source != "group" || got.Brand.Effective.Id != fjord || got.Brand.Group == nil || got.Brand.Group.Id != fjord {
		t.Fatalf("group only: %+v, want the group's brand", got.Brand)
	}

	// The customer's own choice wins over the group's.
	set := brandOf(t, putCustomerBrand(t, c, customer.Id, map[string]any{"brandId": nordlys}))
	if set.Brand.Effective.Source != "customer" || set.Brand.Effective.Id != nordlys || set.Brand.Own == nil || set.Brand.Own.State != "published" {
		t.Fatalf("customer set: %+v, want the customer's brand", set.Brand)
	}
	if set.Revision != got.Revision+1 {
		t.Errorf("revision = %d, want %d: the brand is a column on the row", set.Revision, got.Revision+1)
	}

	// Cleared: back to the group's.
	cleared := brandOf(t, putCustomerBrand(t, c, customer.Id, map[string]any{"brandId": nil}))
	if cleared.Brand.Effective.Source != "group" || cleared.Brand.Own != nil {
		t.Fatalf("customer cleared: %+v, want the group's brand again", cleared.Brand)
	}

	// The group's cleared too (a PUT is a full replace): the workspace default.
	if r := c.Do(http.MethodPut, "/api/v1/customers/groups/"+group.Id, map[string]any{"name": "Retail"}); r.Status != http.StatusOK {
		t.Fatalf("clear the group's brand: %d %s", r.Status, r.Body)
	}
	if got := fetchBranded(t, c, customer.Id); got.Brand.Effective.Source != "default" || got.Brand.Group != nil {
		t.Errorf("both cleared: %+v, want the workspace default", got.Brand)
	}
}

// A brand archived after it was chosen is skipped, not refused: the customer's
// default falls back to the next in the order and the response says which
// choice it skipped.
func TestCustomerBrand_AnArchivedBrandFallsBack(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	own := insertBrand(t, h, "Own", "published")
	grp := insertBrand(t, h, "Grouped", "published")
	group := createGroup(t, c, map[string]any{"name": "Key accounts", "brandId": grp})
	customer := createCustomer(t, c, "Falling Co")
	putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": group.Id})
	brandOf(t, putCustomerBrand(t, c, customer.Id, map[string]any{"brandId": own}))

	setBrandState(t, h, own, "archived")
	got := fetchBranded(t, c, customer.Id)
	if got.Brand.Effective.Source != "group" || got.Brand.Effective.Id != grp {
		t.Errorf("own archived: effective = %+v, want the group's", got.Brand.Effective)
	}
	if len(got.Brand.Unavailable) != 1 || got.Brand.Unavailable[0] != "customer" {
		t.Errorf("unavailable = %v, want [customer]", got.Brand.Unavailable)
	}
	if got.Brand.Own == nil || got.Brand.Own.State != "archived" || got.Brand.Own.Name == nil || *got.Brand.Own.Name != "Own" {
		t.Errorf("own = %+v, want the archived brand still named", got.Brand.Own)
	}

	setBrandState(t, h, grp, "archived")
	got = fetchBranded(t, c, customer.Id)
	if got.Brand.Effective.Source != "default" || len(got.Brand.Unavailable) != 2 {
		t.Errorf("both archived: %+v, want the default with both unavailable", got.Brand)
	}

	// A brand nobody resolves any more reads as unknown, and falls back as well.
	ghost := uuid.New()
	h.Exec(t, `UPDATE customers.customers SET brand_id = $2 WHERE id = $1`, customer.Id, ghost)
	got = fetchBranded(t, c, customer.Id)
	if got.Brand.Own == nil || got.Brand.Own.State != "unknown" || got.Brand.Own.Name != nil {
		t.Errorf("own = %+v, want an unknown brand", got.Brand.Own)
	}

	// Restoring the brand ends the fall-back: nothing was rewritten.
	h.Exec(t, `UPDATE customers.customers SET brand_id = $2 WHERE id = $1`, customer.Id, own)
	setBrandState(t, h, own, "published")
	if got := fetchBranded(t, c, customer.Id); got.Brand.Effective.Source != "customer" {
		t.Errorf("restored: effective = %+v, want the customer's own again", got.Brand.Effective)
	}
}

func TestCustomerBrand_RefusesUnknownDraftAndArchivedBrands(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Refusing Co")
	archived := insertBrand(t, h, "Gone", "archived")
	draft := insertBrand(t, h, "Unfinished", "draft")
	for name, id := range map[string]uuid.UUID{"unknown": uuid.New(), "archived": archived, "draft": draft} {
		r := putCustomerBrand(t, c, customer.Id, map[string]any{"brandId": id})
		if r.Status != http.StatusBadRequest {
			t.Errorf("%s brand: status %d body %s, want 400", name, r.Status, r.Body)
		}
		var problem struct {
			Errors map[string][]string `json:"errors"`
		}
		r.JSON(&problem)
		if len(problem.Errors["brandId"]) == 0 {
			t.Errorf("%s brand: errors = %v, want a field error on brandId", name, problem.Errors)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND brand_id IS NOT NULL`, customer.Id); n != 0 {
		t.Errorf("a refused brand was written")
	}
	// The same refusals for a group, on create and on update.
	if r := c.Do(http.MethodPost, "/api/v1/customers/groups", map[string]any{"name": "Bad", "brandId": uuid.New()}); r.Status != http.StatusBadRequest {
		t.Errorf("create a group with an unknown brand: status %d, want 400", r.Status)
	}
	group := createGroup(t, c, map[string]any{"name": "Good"})
	if r := c.Do(http.MethodPut, "/api/v1/customers/groups/"+group.Id, map[string]any{"name": "Good", "brandId": archived}); r.Status != http.StatusBadRequest {
		t.Errorf("set a group's brand to an archived one: status %d, want 400", r.Status)
	}
}

// Writes follow the module's rules: 404, a stale revision ahead of the no-op,
// a no-op that writes nothing, and a brand already held passes even when it has
// been archived since (owner.go's rule for a disabled owner).
func TestCustomerBrand_WriteRules(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	brand := insertBrand(t, h, "Held", "published")
	customer := createCustomer(t, c, "Writes Co")

	if r := putCustomerBrand(t, c, 999999, map[string]any{"brandId": brand}); r.Status != http.StatusNotFound {
		t.Errorf("unknown customer: status %d, want 404", r.Status)
	}
	set := brandOf(t, putCustomerBrand(t, c, customer.Id, map[string]any{"brandId": brand, "revision": 1}))
	if r := putCustomerBrand(t, c, customer.Id, map[string]any{"brandId": brand, "revision": 1}); r.Status != http.StatusConflict {
		t.Errorf("stale revision: status %d, want 409 ahead of the no-op", r.Status)
	}
	same := brandOf(t, putCustomerBrand(t, c, customer.Id, map[string]any{"brandId": brand}))
	if same.Revision != set.Revision {
		t.Errorf("revision after a no-op = %d, want %d: nothing is written", same.Revision, set.Revision)
	}
	// Archived since, the same brand resubmitted is not refused.
	setBrandState(t, h, brand, "archived")
	if r := putCustomerBrand(t, c, customer.Id, map[string]any{"brandId": brand}); r.Status != http.StatusOK {
		t.Errorf("resubmitting an archived brand already held: status %d, want 200", r.Status)
	}
	// The group keeps an archived brand across a rename.
	groupBrand := insertBrand(t, h, "Held two", "published")
	group := createGroup(t, c, map[string]any{"name": "Held group", "brandId": groupBrand})
	setBrandState(t, h, groupBrand, "archived")
	if r := c.Do(http.MethodPut, "/api/v1/customers/groups/"+group.Id, map[string]any{"name": "Renamed", "brandId": groupBrand}); r.Status != http.StatusOK {
		t.Errorf("rename a group holding an archived brand: status %d body %s, want 200", r.Status, r.Body)
	}
	// Permission: customers:view alone does not write.
	viewer := h.SignIn(t, "customers:view")
	if r := putCustomerBrand(t, viewer, customer.Id, map[string]any{"brandId": nil}); r.Status != http.StatusForbidden {
		t.Errorf("view only: status %d, want 403", r.Status)
	}
}

// The picker lists the published brands the contract offers, the default first;
// a draft and an archived brand are not offered.
func TestCustomerBrand_PickerListsPublishedBrands(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	insertBrand(t, h, "Zeta", "published")
	insertBrand(t, h, "Alfa", "published")
	insertBrand(t, h, "Old", "archived")
	insertBrand(t, h, "Pending", "draft")

	r := c.Do(http.MethodGet, "/api/v1/customers/brands", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s", r.Status, r.Body)
	}
	var options []brandOptionJSON
	r.JSON(&options)
	var names []string
	for _, o := range options {
		names = append(names, o.Name)
	}
	if len(options) != 3 || !options[0].IsDefault || names[1] != "Alfa" || names[2] != "Zeta" {
		t.Errorf("options = %v, want the default, Alfa, Zeta", names)
	}
	if r := h.SignIn(t, "customers:create").Do(http.MethodGet, "/api/v1/customers/brands", nil); r.Status != http.StatusForbidden {
		t.Errorf("without customers:view: status %d, want 403", r.Status)
	}
}

// contracts.CustomerEntry carries the customer's brand and its group's, so a
// document module needs no second call to pick the default (B28).
func TestCustomerBrand_TheDirectoryCarriesBothIds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	own := insertBrand(t, h, "Own", "published")
	grp := insertBrand(t, h, "Grouped", "published")
	group := createGroup(t, c, map[string]any{"name": "Directory group", "brandId": grp})
	branded := createCustomer(t, c, "Branded Co")
	putCustomerGroup(t, c, branded.Id, map[string]any{"groupId": group.Id})
	putCustomerBrand(t, c, branded.Id, map[string]any{"brandId": own})
	plain := createCustomer(t, c, "Unbranded Co")

	dir := newDirectory(t, h)
	got, err := dir.Customer(context.Background(), branded.Id)
	if err != nil || got == nil {
		t.Fatalf("Customer: %v, %v", got, err)
	}
	if got.BrandID == nil || *got.BrandID != own || got.GroupBrandID == nil || *got.GroupBrandID != grp {
		t.Errorf("Customer brand ids = %v/%v, want %s/%s", got.BrandID, got.GroupBrandID, own, grp)
	}
	many, err := dir.Customers(context.Background(), []int32{branded.Id, plain.Id})
	if err != nil || len(many) != 2 {
		t.Fatalf("Customers: %v, %v", many, err)
	}
	if many[0].BrandID == nil || many[0].GroupBrandID == nil || many[1].BrandID != nil || many[1].GroupBrandID != nil {
		t.Errorf("Customers brand ids = %+v, want set for the first and nil for the second", many)
	}
}

// A brand is not personal data: anonymising a person leaves the default brand
// as it is, like the owner and the billing terms.
func TestCustomerBrand_SurvivesAnonymisation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	brand := insertBrand(t, h, "Kept", "published")
	person := archivedPerson(t, c, "Brand Nordmann")
	h.Exec(t, `UPDATE customers.customers SET brand_id = $2 WHERE id = $1`, person.Id, brand)
	scheduleOn(t, h, person.Id, day(h, 0))
	h.Advance(48 * time.Hour)
	runAnonymisation(t, h)
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND anonymised_at IS NOT NULL AND brand_id = $2`, person.Id, brand); n != 1 {
		t.Errorf("anonymised customer keeps its brand: %d rows, want 1", n)
	}
}
