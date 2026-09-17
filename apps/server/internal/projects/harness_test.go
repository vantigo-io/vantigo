package projects_test

import (
	"context"
	"maps"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/projects"
)

// newHarness is one projects installation for one test: internal/modtest's
// shared harness composing this module beside identity, with every exchange
// validated against projects.yaml through the package recorder. It stubs
// Deps.Directory and Deps.Products with fakes — depguard forbids
// internal/projects/** from importing internal/customers or
// internal/products, even in tests, so both are built directly against
// internal/contracts, the same seam energy uses for customers. The user
// directory is not stubbed: identity is always composed, and it provides the
// real one.
func newHarness(t *testing.T, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	return newProjectsHarness(t, true, opts...)
}

// newHarnessWithoutProducts is newHarness with the products module off:
// Deps.Products stays nil, which is the optional contract's absent case
// (D10) and what billingLinesAvailable answers false for.
func newHarnessWithoutProducts(t *testing.T, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	return newProjectsHarness(t, false, opts...)
}

// newProjectsHarness is the one composition both harnesses are: products is
// the only thing they differ in, so it is the only thing either of them
// says. A second copy of the option list would drift the moment this module
// grows another dependency.
func newProjectsHarness(t *testing.T, products bool, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	base := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(projects.Module()),
		modtest.WithDirectory(fakeDirectory{}),
	}
	if products {
		base = append(base, modtest.WithProducts(newFakeCatalog()))
	}
	return modtest.New(t, append(base, opts...)...)
}

// The customers the fake directory knows. 1003 is archived, which resolves
// and is allowed: a project can outlive the relationship that started it
// (design §4.1).
const (
	customerKraftVerket     = 1001
	customerAcme            = 1002
	customerArchived        = 1003
	customerUnknown         = 9999
	customerKraftVerketName = "Kraft-Verket"
	customerAcmeName        = "Acme Industrier AS"
	customerArchivedName    = "Nedlagt Handel AS"
)

// fakeDirectory is contracts.CustomerDirectory over three customers.
// Everything else does not exist, which is what makes the unknown-customer
// validation rule testable without composing customers beside this module.
// Contact and ContactsByEmail satisfy the interface and are never exercised
// here: projects never resolves a contact.
type fakeDirectory struct{}

var _ contracts.CustomerDirectory = fakeDirectory{}

func (fakeDirectory) Customer(_ context.Context, id int32) (*contracts.CustomerEntry, error) {
	switch id {
	case customerKraftVerket:
		return &contracts.CustomerEntry{ID: id, Name: customerKraftVerketName}, nil
	case customerAcme:
		return &contracts.CustomerEntry{ID: id, Name: customerAcmeName}, nil
	case customerArchived:
		return &contracts.CustomerEntry{ID: id, Name: customerArchivedName, Archived: true}, nil
	default:
		return nil, nil
	}
}

func (fakeDirectory) Contact(context.Context, int32) (*contracts.ContactEntry, error) {
	return nil, nil
}

func (fakeDirectory) ContactsByEmail(context.Context, string) ([]contracts.ContactMatch, error) {
	return nil, nil
}

// fakeCatalog is contracts.ProductCatalog over one service variant. This
// task never reads it — only whether Deps.Products is set at all decides
// billingLinesAvailable — so it exists to make "products enabled" a real
// composition rather than a flag, and to give the billing-line task (Task
// 10) something already wired.
type fakeCatalog struct {
	variants map[int32]contracts.VariantEntry
}

var _ contracts.ProductCatalog = (*fakeCatalog)(nil)

const variantProjectManagerHour = 2001

func newFakeCatalog() *fakeCatalog {
	return &fakeCatalog{variants: map[int32]contracts.VariantEntry{
		variantProjectManagerHour: {
			ID: variantProjectManagerHour, ProductID: 3001, ProductName: "Project manager hour",
			SKU: "PM-HOUR", Unit: "hour", ProductType: "Service", ProductStatus: "Active",
		},
	}}
}

func (c *fakeCatalog) Variant(_ context.Context, id int32) (*contracts.VariantEntry, error) {
	v, ok := c.variants[id]
	if !ok {
		return nil, nil
	}
	return &v, nil
}

func (c *fakeCatalog) Variants(_ context.Context, ids []int32) ([]contracts.VariantEntry, error) {
	out := make([]contracts.VariantEntry, 0, len(ids))
	for _, id := range ids {
		if v, ok := c.variants[id]; ok {
			out = append(out, v)
		}
	}
	return out, nil
}

func (c *fakeCatalog) ListPrice(_ context.Context, variantID int32, currency string, _ time.Time) (*contracts.Money, error) {
	if _, ok := c.variants[variantID]; !ok {
		return nil, nil
	}
	return &contracts.Money{Amount: 1250, Currency: currency}, nil
}

// signIn seeds a caller holding projects:access plus whatever else the test
// needs, and returns their client and user id. Every operation requires
// projects:access (D8), so adding it here keeps each test's permission list
// to the thing it is actually about.
func signIn(t *testing.T, h *modtest.Harness, permissions ...string) (*modtest.Client, uuid.UUID) {
	t.Helper()
	return h.SignInUser(t, append([]string{"projects:access"}, permissions...)...)
}

// createBody is a valid minimal create body, which tests override one field
// of at a time. Kraft-Verket is the customer, so the default project is a
// customer project rather than an internal one.
func createBody(overrides map[string]any) map[string]any {
	body := map[string]any{
		"code":        "KVEM1000",
		"name":        "Kraft-Verket modernisering",
		"customerId":  customerKraftVerket,
		"billingType": "time-and-materials",
	}
	maps.Copy(body, overrides)
	for field, value := range overrides {
		if value == nil {
			delete(body, field)
		}
	}
	return body
}

// createProject creates a project with createBody(overrides) and fails the
// test if it is not created. A nil override value removes that field, which
// is how a test builds an internal project (no customerId).
func createProject(t *testing.T, c *modtest.Client, overrides map[string]any) projectJSON {
	t.Helper()
	r := c.Do(http.MethodPost, "/api/v1/projects", createBody(overrides))
	if r.Status != http.StatusCreated {
		t.Fatalf("create project: status %d body %s, want 201", r.Status, r.Body)
	}
	var project projectJSON
	r.JSON(&project)
	return project
}

// projectJSON decodes ProjectResponse. financials is a pointer because its
// absence is the whole of D12: a test that must prove the key is not there
// decodes into a map instead (rawJSON), since a nil pointer cannot tell
// "absent" from "null".
type projectJSON struct {
	Id                    int32            `json:"id"`
	Code                  string           `json:"code"`
	Name                  string           `json:"name"`
	Description           *string          `json:"description"`
	CustomerId            *int32           `json:"customerId"`
	CustomerName          *string          `json:"customerName"`
	Internal              bool             `json:"internal"`
	Status                string           `json:"status"`
	StartDate             *string          `json:"startDate"`
	EndDate               *string          `json:"endDate"`
	BillingType           string           `json:"billingType"`
	BudgetHours           *float64         `json:"budgetHours"`
	Revision              int32            `json:"revision"`
	CreatedAt             time.Time        `json:"createdAt"`
	UpdatedAt             time.Time        `json:"updatedAt"`
	Managers              []personJSON     `json:"managers"`
	Capabilities          capabilitiesJSON `json:"capabilities"`
	BillingLinesAvailable bool             `json:"billingLinesAvailable"`
	Financials            *financialsJSON  `json:"financials"`
}

type personJSON struct {
	UserId      uuid.UUID `json:"userId"`
	DisplayName string    `json:"displayName"`
}

type capabilitiesJSON struct {
	CanManage        bool `json:"canManage"`
	CanSeeFinancials bool `json:"canSeeFinancials"`
}

type financialsJSON struct {
	Currency         *string  `json:"currency"`
	FixedPriceAmount *float64 `json:"fixedPriceAmount"`
	BudgetAmount     *float64 `json:"budgetAmount"`
}

// validationProblemJSON decodes the field-error body every §4.1 refusal
// answers with.
type validationProblemJSON struct {
	Title  string              `json:"title"`
	Errors map[string][]string `json:"errors"`
}
