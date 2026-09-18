package projects_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// GET /api/v1/projects/code-suggestion: design §4.2's mnemonic starting
// point for a new project's code. It is a suggestion, never a reservation —
// the caller may type anything else instead, and asking for one any number
// of times must never change what an actual create later allocates (see the
// "does not advance the counter" test below; only PostProjects' own
// NextCounterValue call does that, projects.go).

type codeSuggestionJSON struct {
	Code string `json:"code"`
}

func intPtr(v int32) *int32 { return &v }

// suggestCode calls the endpoint with the given optional customerId and
// name, failing the test unless it answered 200.
func suggestCode(t *testing.T, c *modtest.Client, customerID *int32, name string) string {
	t.Helper()
	q := url.Values{}
	if customerID != nil {
		q.Set("customerId", fmt.Sprint(*customerID))
	}
	if name != "" {
		q.Set("name", name)
	}
	path := "/api/v1/projects/code-suggestion"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	r := c.Do(http.MethodGet, path, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("code suggestion: status %d body %s, want 200", r.Status, r.Body)
	}
	var suggestion codeSuggestionJSON
	r.JSON(&suggestion)
	return suggestion.Code
}

// Nobody has created a project for Kraft-Verket yet, so the counter has no
// row at all: the suggestion treats that as 1000, exactly the number
// NextCounterValue itself would allocate first (design §4.2).
func TestGetProjectsCodeSuggestion_FirstSuggestionForCustomer(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)

	code := suggestCode(t, c, intPtr(customerKraftVerket), "Energy migration")
	if code != "KVEM1000" {
		t.Errorf("code = %q, want KVEM1000", code)
	}
}

// Accepting the first suggestion and creating the project with it advances
// the real counter (PostProjects' own NextCounterValue call), so the next
// suggestion for the same customer moves on to the next number.
func TestGetProjectsCodeSuggestion_AfterCreate_MovesToTheNextNumber(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	first := suggestCode(t, c, intPtr(customerKraftVerket), "Energy migration")
	if first != "KVEM1000" {
		t.Fatalf("first suggestion = %q, want KVEM1000", first)
	}
	createProject(t, c, map[string]any{"code": first})

	second := suggestCode(t, c, intPtr(customerKraftVerket), "Energy migration")
	if second != "KVEM1001" {
		t.Errorf("second suggestion = %q, want KVEM1001", second)
	}
}

// No customerId means an internal project: the prefix is the fixed "INT",
// never derived from anything.
func TestGetProjectsCodeSuggestion_Internal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)

	code := suggestCode(t, c, nil, "Competence")
	if !strings.HasPrefix(code, "INTCO") {
		t.Errorf("code = %q, want it to start with INTCO", code)
	}
}

// The suggestion reads the counter, it never allocates from it: ten calls
// must leave next_value exactly where PostProjects' own create last left it.
func TestGetProjectsCodeSuggestion_RepeatedCalls_DoNotAdvanceTheCounter(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	createProject(t, c, nil) // KVEM1000, which leaves the counter's next_value at 1001

	before := modtest.One[int64](t, h, `SELECT next_value FROM projects.counters WHERE counter_name = 'project_code'`)
	for range 10 {
		suggestCode(t, c, intPtr(customerKraftVerket), "Energy migration")
	}
	after := modtest.One[int64](t, h, `SELECT next_value FROM projects.counters WHERE counter_name = 'project_code'`)
	if after != before {
		t.Errorf("counter next_value = %d after ten suggestions, want unchanged at %d", after, before)
	}
}

// A hand-typed code can occupy a number the counter itself has never
// allocated (task 6 brief) — the row below exists without any create ever
// having called NextCounterValue for it. The suggestion must notice its own
// first candidate is taken and move on rather than offer a code that would
// just fail create's uniqueness check.
func TestGetProjectsCodeSuggestion_TakenCandidateIsSkipped(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := signIn(t, h)
	h.Exec(t, `INSERT INTO projects.projects
		(code, name, customer_id, billing_type, created_by_user_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, now(), now())`,
		"KVEM1000", "Hand typed", customerKraftVerket, "time-and-materials", userID)

	code := suggestCode(t, c, intPtr(customerKraftVerket), "Energy migration")
	if code != "KVEM1001" {
		t.Errorf("code = %q, want KVEM1001 (KVEM1000 is already taken)", code)
	}
}

// customerId given but unresolvable contributes no letters, exactly as a
// customer whose name yields none would: the endpoint asks for nothing but
// projects:access (D8), so a refusal naming the customer would answer "does
// customer N exist?" for every N. The create this feeds keeps its own 400,
// where the caller holds customers:view anyway.
func TestGetProjectsCodeSuggestion_UnknownCustomer_FallsBackToTheProjectLetters(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)

	code := suggestCode(t, c, intPtr(customerUnknown), "Energy migration")
	if code != "EM1000" {
		t.Errorf("code = %q, want EM1000 — the project's own letters and the next number", code)
	}
}

// The router must not confuse the static "code-suggestion" path segment with
// GetProjectsById's {id}: module.Router gives a literal segment precedence
// over a {param} at the same position (module/router.go), so this request
// must reach the suggestion handler and never the bare 404 an unrouted or
// misrouted "project id 'code-suggestion'" lookup would answer.
func TestGetProjectsCodeSuggestion_NotConfusedWithProjectId(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)

	r := c.Do(http.MethodGet, "/api/v1/projects/code-suggestion", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200, not the 404 a misrouted {id} lookup would answer", r.Status, r.Body)
	}
	var suggestion codeSuggestionJSON
	r.JSON(&suggestion)
	if suggestion.Code == "" {
		t.Errorf("code = %q, want a non-empty suggestion", suggestion.Code)
	}
}
