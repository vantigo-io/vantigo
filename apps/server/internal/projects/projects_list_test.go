package projects_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// GET /api/v1/projects: design §5's "the list query filters in SQL, so
// paging and counts are correct" — every visibility case here asserts the
// envelope's totalCount as well as the rows, because a filter applied in Go
// would keep the rows right and the count wrong. D12 is taken one step
// further on a summary: the financial fields are not shaped, they are not in
// the schema at all, so no caller ever sees them in a list.

// projectSummaryJSON decodes ProjectSummaryResponse: ProjectResponse without
// financials, capabilities, billingLinesAvailable or description.
type projectSummaryJSON struct {
	Id           int32        `json:"id"`
	Code         string       `json:"code"`
	Name         string       `json:"name"`
	CustomerId   *int32       `json:"customerId"`
	CustomerName *string      `json:"customerName"`
	Internal     bool         `json:"internal"`
	Status       string       `json:"status"`
	StartDate    *string      `json:"startDate"`
	EndDate      *string      `json:"endDate"`
	BillingType  string       `json:"billingType"`
	BudgetHours  *float64     `json:"budgetHours"`
	Revision     int32        `json:"revision"`
	Managers     []personJSON `json:"managers"`
}

type projectListJSON struct {
	Data       []projectSummaryJSON `json:"data"`
	Pagination paginationJSON       `json:"pagination"`
}

// listProjects reads one page of the list and fails the test unless it
// answered 200. query is the raw query string, without the leading "?".
func listProjects(t *testing.T, c *modtest.Client, query string) projectListJSON {
	t.Helper()
	path := "/api/v1/projects"
	if query != "" {
		path += "?" + query
	}
	r := c.Do(http.MethodGet, path, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list %q: status %d body %s, want 200", query, r.Status, r.Body)
	}
	var list projectListJSON
	r.JSON(&list)
	return list
}

// codes is the codes of a page, in the order the list answered them.
func codes(list projectListJSON) []string {
	out := make([]string, 0, len(list.Data))
	for _, p := range list.Data {
		out = append(out, p.Code)
	}
	return out
}

// D7 in the plural: a caller with no global permission sees exactly the
// projects they hold a role on, and the envelope's totalCount counts only
// those — the predicate is in the query, not applied to a page after it was
// counted.
func TestGetProjects_Member_SeesOnlyTheProjectsTheyHoldARoleOn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	mine := createProject(t, creator, map[string]any{"code": "MINE1000"})
	createProject(t, creator, map[string]any{"code": "THEIRS1000"})

	member, memberID := signIn(t, h)
	addRole(t, h, mine.Id, memberID, "member")

	list := listProjects(t, member, "")
	if got := codes(list); len(got) != 1 || got[0] != "MINE1000" {
		t.Errorf("codes = %v, want exactly [MINE1000]", got)
	}
	if list.Pagination.TotalCount != 1 {
		t.Errorf("TotalCount = %d, want 1: the count must be filtered the same way the rows are", list.Pagination.TotalCount)
	}
}

// A caller with no role and no global permission sees an empty list, not a
// 403: the app is theirs to use (D8), it simply has nothing in it for them.
func TestGetProjects_Outsider_SeesAnEmptyList(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	createProject(t, creator, map[string]any{"code": "HIDDEN1000"})

	outsider, _ := signIn(t, h)
	list := listProjects(t, outsider, "")
	if len(list.Data) != 0 || list.Pagination.TotalCount != 0 {
		t.Errorf("list = %+v, want no rows and totalCount 0", list)
	}
}

func TestGetProjects_ViewAll_SeesEveryProject(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	createProject(t, creator, map[string]any{"code": "AAA1000"})
	createProject(t, creator, map[string]any{"code": "BBB1000"})

	viewer, _ := signIn(t, h, "projects:view-all")
	list := listProjects(t, viewer, "")
	if got := codes(list); len(got) != 2 {
		t.Errorf("codes = %v, want both projects", got)
	}
	if list.Pagination.TotalCount != 2 {
		t.Errorf("TotalCount = %d, want 2", list.Pagination.TotalCount)
	}
}

// mine=true narrows a caller who can see everything to the projects they
// hold a role on — the "My projects" filter of the list page (§8.2).
func TestGetProjects_Mine_NarrowsAViewAllCallerToTheirOwn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	ours := createProject(t, creator, map[string]any{"code": "OURS1000"})
	createProject(t, creator, map[string]any{"code": "OTHER1000"})

	viewer, viewerID := signIn(t, h, "projects:view-all")
	addRole(t, h, ours.Id, viewerID, "viewer")

	if got := codes(listProjects(t, viewer, "")); len(got) != 2 {
		t.Fatalf("unfiltered codes = %v, want both projects", got)
	}
	list := listProjects(t, viewer, "mine=true")
	if got := codes(list); len(got) != 1 || got[0] != "OURS1000" {
		t.Errorf("mine=true codes = %v, want exactly [OURS1000]", got)
	}
	if list.Pagination.TotalCount != 1 {
		t.Errorf("mine=true TotalCount = %d, want 1", list.Pagination.TotalCount)
	}
}

// Each filter, one at a time, over one fixture set. The assertion is always
// on the whole set of codes the filter answered, so a filter that let an
// extra project through fails here rather than in whichever later test
// happened to count rows.
func TestGetProjects_Filters(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	admin, _ := signIn(t, h, "projects:manage-all")

	kraft := createProject(t, creator, map[string]any{"code": "KVEM1000", "name": "Kraft-Verket modernisering"})
	createProject(t, creator, map[string]any{"code": "ACME1000", "name": "Acme ombygging", "customerId": customerAcme})
	createProject(t, creator, map[string]any{
		"code": "INT1000", "name": "Internt opprydding", "customerId": nil, "billingType": "non-billable",
	})
	createProject(t, creator, map[string]any{"code": "RAB1000", "name": "Rabatt 50% kampanje"})

	// The status filter needs a project that is not 'planned'.
	setStatus(t, creator, kraft.Id, "active")

	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{"status", "status=active", []string{"KVEM1000"}},
		{"customerId", fmt.Sprintf("customerId=%d", customerAcme), []string{"ACME1000"}},
		{"internal true", "internal=true", []string{"INT1000"}},
		{"internal false", "internal=false", []string{"ACME1000", "KVEM1000", "RAB1000"}},
		{"search by code", "search=KVEM", []string{"KVEM1000"}},
		{"search by name", "search=" + url.QueryEscape("ombygging"), []string{"ACME1000"}},
		{"search is case-insensitive", "search=" + url.QueryEscape("internt oppryd"), []string{"INT1000"}},
		// A '%' in the search text is a character the caller typed, not a
		// wildcard: without escaping this would match every project.
		{"search with a literal percent", "search=" + url.QueryEscape("50%"), []string{"RAB1000"}},
		{"search with a literal underscore matches nothing", "search=" + url.QueryEscape("KVEM_1000"), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			list := listProjects(t, admin, tc.query)
			got := codes(list)
			if len(got) != len(tc.want) {
				t.Fatalf("codes = %v, want %v", got, tc.want)
			}
			for i, code := range tc.want {
				if got[i] != code {
					t.Fatalf("codes = %v, want %v", got, tc.want)
				}
			}
			if list.Pagination.TotalCount != int32(len(tc.want)) {
				t.Errorf("TotalCount = %d, want %d", list.Pagination.TotalCount, len(tc.want))
			}
		})
	}
}

// ORDER BY p.code: the list is alphabetical by code, whatever order the
// projects were created in.
func TestGetProjects_OrdersByCode(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	for _, code := range []string{"ZULU1000", "ALFA1000", "MIKE1000"} {
		createProject(t, creator, map[string]any{"code": code})
	}
	admin, _ := signIn(t, h, "projects:view-all")

	got := codes(listProjects(t, admin, ""))
	want := []string{"ALFA1000", "MIKE1000", "ZULU1000"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("codes = %v, want %v", got, want)
		}
	}
}

// A summary embeds what the list page renders without a second round trip:
// the customer's name, resolved through contracts.CustomerDirectory, and the
// project's managers, named through contracts.UserDirectory.
func TestGetProjects_Summaries_CarryCustomerNameAndManagers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, creatorID := signIn(t, h, "projects:create")
	createProject(t, creator, map[string]any{"code": "EMB1000"})
	createProject(t, creator, map[string]any{
		"code": "EMBINT1000", "customerId": nil, "billingType": "non-billable",
	})

	list := listProjects(t, creator, "")
	if len(list.Data) != 2 {
		t.Fatalf("data = %+v, want two summaries", list.Data)
	}
	withCustomer, internal := list.Data[0], list.Data[1]
	if withCustomer.Code != "EMB1000" || internal.Code != "EMBINT1000" {
		t.Fatalf("codes = %v, want [EMB1000 EMBINT1000]", codes(list))
	}
	if withCustomer.CustomerName == nil || *withCustomer.CustomerName != customerKraftVerketName {
		t.Errorf("CustomerName = %v, want %q from the directory", withCustomer.CustomerName, customerKraftVerketName)
	}
	if !internal.Internal || internal.CustomerName != nil {
		t.Errorf("internal summary = %+v, want internal with no customer name", internal)
	}
	for _, summary := range list.Data {
		if len(summary.Managers) != 1 || summary.Managers[0].UserId != creatorID {
			t.Fatalf("%s Managers = %+v, want exactly the creator %s", summary.Code, summary.Managers, creatorID)
		}
		if name := summary.Managers[0].DisplayName; name == "" || name == "Unknown user" {
			t.Errorf("%s manager display name = %q, want the name the user directory resolved", summary.Code, name)
		}
	}
}

// A summary carries no financial fields for anybody — not even the project's
// own manager, who sees them on the project itself. The assertion is on the
// raw JSON object: a decoded struct cannot tell an absent key from a null
// one, and absent is the contract.
func TestGetProjects_Summaries_NeverCarryFinancials(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	createProject(t, creator, map[string]any{
		"code": "FINL1000", "billingType": "fixed-price", "fixedPriceAmount": 125000, "currency": "NOK",
	})

	r := creator.Do(http.MethodGet, "/api/v1/projects", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var raw struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(r.Body, &raw); err != nil {
		t.Fatalf("decode %s: %v", r.Body, err)
	}
	if len(raw.Data) != 1 {
		t.Fatalf("data = %v, want one summary", raw.Data)
	}
	for _, key := range []string{"financials", "capabilities", "billingLinesAvailable", "description"} {
		if _, present := raw.Data[0][key]; present {
			t.Errorf("summary %s carries a %q key, want it absent from every summary", r.Body, key)
		}
	}
}

// Paging: customers' defaults and clamps, page 1 and 25 rows unless the
// caller says otherwise, and the envelope's derived flags.
func TestGetProjects_Paging(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	for i := range 3 {
		createProject(t, creator, map[string]any{"code": fmt.Sprintf("PAGE100%d", i)})
	}

	first := listProjects(t, creator, "page=1&pageSize=2")
	if got := codes(first); len(got) != 2 || got[0] != "PAGE1000" {
		t.Errorf("first page codes = %v, want the first two by code", got)
	}
	if p := first.Pagination; p.TotalCount != 3 || p.TotalPages != 2 || !p.HasNextPage || p.HasPreviousPage {
		t.Errorf("first page pagination = %+v, want 3 of 2 pages with a next and no previous", p)
	}

	second := listProjects(t, creator, "page=2&pageSize=2")
	if got := codes(second); len(got) != 1 || got[0] != "PAGE1002" {
		t.Errorf("second page codes = %v, want the last project", got)
	}
	if p := second.Pagination; p.HasNextPage || !p.HasPreviousPage {
		t.Errorf("second page pagination = %+v, want a previous and no next", p)
	}

	// No paging parameters at all: page 1 of 25.
	if p := listProjects(t, creator, "").Pagination; p.Page != 1 || p.PageSize != 25 {
		t.Errorf("default pagination = %+v, want page 1 of 25", p)
	}
}

func TestGetProjects_InvalidQueryParameters_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)

	for _, query := range []string{"page=0", "pageSize=0", "pageSize=101", "status=archived"} {
		r := c.Do(http.MethodGet, "/api/v1/projects?"+query, nil)
		if r.Status != http.StatusBadRequest {
			t.Errorf("%s: status %d body %s, want 400", query, r.Status, r.Body)
		}
	}
}

// Every operation requires projects:access (D8), the list included.
func TestGetProjects_WithoutAccessPermission_Returns403(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "projects:view-all")

	if r := c.Do(http.MethodGet, "/api/v1/projects", nil); r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403", r.Status, r.Body)
	}
}
