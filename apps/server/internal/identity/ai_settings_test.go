package identity_test

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// The Owner's AI models endpoints (AI as a platform provider design T12, T13):
// the plan as facts, the model each task uses, and the Owner's choice.

const (
	aiSettingsPath     = "/api/v1/identity/owner/ai/settings"
	aiTaskPathPrefix   = "/api/v1/identity/owner/ai/tasks/"
	aiTaskModelChanged = "ai-task-model.changed"
	aiTestKey          = "sk-identity-test-key"
)

type aiTaskJSON struct {
	Key         string  `json:"key"`
	Display     string  `json:"display"`
	Description string  `json:"description"`
	Model       string  `json:"model"`
	Default     *string `json:"default"`
	Source      string  `json:"source"`
	Version     string  `json:"version"`
}

type aiSettingsJSON struct {
	Provider      string   `json:"provider"`
	AllowedModels []string `json:"allowedModels"`
	PlanDefault   string   `json:"planDefault"`
	KeyConfigured bool     `json:"keyConfigured"`
	Modules       []struct {
		Module string       `json:"module"`
		Tasks  []aiTaskJSON `json:"tasks"`
	} `json:"modules"`
}

func getAISettings(t testing.TB, c *client) aiSettingsJSON {
	t.Helper()
	r := c.do(http.MethodGet, aiSettingsPath, nil)
	if r.status != http.StatusOK {
		t.Fatalf("GET %s: status %d body %s", aiSettingsPath, r.status, r.body)
	}
	var s aiSettingsJSON
	r.json(&s)
	return s
}

func (s aiSettingsJSON) task(t testing.TB, key string) aiTaskJSON {
	t.Helper()
	for _, m := range s.Modules {
		for _, task := range m.Tasks {
			if task.Key == key {
				return task
			}
		}
	}
	t.Fatalf("task %s is not in the settings: %+v", key, s)
	return aiTaskJSON{}
}

func putAITask(c *client, key, version string, body any) *resp {
	return c.do(http.MethodPut, aiTaskPathPrefix+key, body, header("If-Match", version))
}

// newAISettingsHarness is an instance whose plan allows three models, with the
// declared tasks of newAIHarness.
func newAISettingsHarness(t *testing.T, opts ...harnessOption) *harness {
	t.Helper()
	return newAIHarness(t, append(withAIPlan("gpt-4o-mini,gpt-4o,gpt-4.1"), opts...)...)
}

// The settings state the plan as facts and never the key; the tasks come
// grouped by module in key order, each with the model it uses, the module's
// recommendation and where the model comes from — and a module that is off
// has no tasks to list.
func TestAISettings_ThePlanAsFactsAndTheTasksByModule(t *testing.T) {
	t.Parallel()
	h := newAISettingsHarness(t)
	owner, _ := h.bootstrapOwner(t)

	r := owner.do(http.MethodGet, aiSettingsPath, nil)
	if r.status != http.StatusOK {
		t.Fatalf("status %d body %s", r.status, r.body)
	}
	if strings.Contains(string(r.body), aiTestKey) || strings.Contains(strings.ToLower(string(r.body)), "apikey") {
		t.Errorf("the response carries the key or a field for it: %s", r.body)
	}
	var s aiSettingsJSON
	r.json(&s)
	if s.Provider != "openai" || !slices.Equal(s.AllowedModels, []string{"gpt-4o-mini", "gpt-4o", "gpt-4.1"}) ||
		s.PlanDefault != "gpt-4o-mini" || !s.KeyConfigured {
		t.Errorf("facts = %+v, want openai, the three models, the first as the plan's default, a key configured", s)
	}
	var modules []string
	for _, m := range s.Modules {
		modules = append(modules, m.Module)
	}
	if !slices.Equal(modules, []string{"communications", "staff"}) {
		t.Fatalf("modules = %v, want communications and staff in order", modules)
	}
	var comms []string
	for _, task := range s.Modules[0].Tasks {
		comms = append(comms, task.Key)
	}
	if !slices.Equal(comms, []string{"communications.compose-reply", "communications.customer-suggestion"}) {
		t.Errorf("communications tasks = %v, want the two in key order", comms)
	}

	reply := s.task(t, "communications.compose-reply")
	if reply.Model != "gpt-4o-mini" || reply.Source != "plan" || reply.Default != nil || reply.Version != "0" ||
		reply.Display != "Draft a reply" || reply.Description == "" {
		t.Errorf("compose-reply = %+v, want the plan's default, source plan, no recommendation, version 0", reply)
	}
	suggest := s.task(t, "communications.customer-suggestion")
	if suggest.Model != "gpt-4o" || suggest.Source != "default" || suggest.Default == nil || *suggest.Default != "gpt-4o" {
		t.Errorf("customer-suggestion = %+v, want the module's recommendation gpt-4o, source default", suggest)
	}
}

// A module that is off declares no tasks, so the settings do not list it.
func TestAISettings_AModuleThatIsOffIsNotListed(t *testing.T) {
	t.Parallel()
	h := newHarness(t, append(withAIPlan("gpt-4o-mini"), withAITasks(taskComposeReply), withEnv("MODULES", "customers"))...)
	owner, _ := h.bootstrapOwner(t)
	s := getAISettings(t, owner)
	if len(s.Modules) != 0 {
		t.Errorf("modules = %+v, want none: communications is not in MODULES", s.Modules)
	}
}

// An Owner's choice is in effect at once: the settings answer it with a new
// version, the resolver the AI client asks gives it on the very next call, and
// one audit row says what the task used before and uses now.
func TestAISettings_AChoiceIsInEffectAtOnceAndAudited(t *testing.T) {
	t.Parallel()
	h := newAISettingsHarness(t)
	owner, ownerID := h.bootstrapOwner(t)
	key := "communications.compose-reply"

	r := putAITask(owner, key, "0", map[string]string{"model": "gpt-4.1"})
	if r.status != http.StatusOK {
		t.Fatalf("PUT: status %d body %s", r.status, r.body)
	}
	var saved aiTaskJSON
	r.json(&saved)
	if saved.Model != "gpt-4.1" || saved.Source != "chosen" || saved.Version == "0" || saved.Key != key {
		t.Errorf("saved = %+v, want gpt-4.1 chosen under a new version", saved)
	}
	if got := getAISettings(t, owner).task(t, key); got != saved {
		t.Errorf("settings after the save = %+v, want the saved task %+v", got, saved)
	}

	model, err := identity.Module(h.access).AIModels(h.deps).ModelFor(t.Context(), key)
	if err != nil || model != "gpt-4.1" {
		t.Errorf("the resolver answers %q, %v, want gpt-4.1 at once, without waiting out the cache", model, err)
	}

	events := h.auditEvents(t, aiTaskModelChanged)
	if len(events) != 1 {
		t.Fatalf("%d %s audits, want 1", len(events), aiTaskModelChanged)
	}
	e := events[0]
	if e.Actor == nil || *e.Actor != ownerID || e.Details != `{"action":"ai-task-model.changed"}` ||
		e.Before != `{"task":"communications.compose-reply","model":"gpt-4o-mini","source":"plan"}` ||
		e.After != `{"task":"communications.compose-reply","model":"gpt-4.1","source":"chosen"}` {
		t.Errorf("audit = %+v", e)
	}
	if strings.Contains(e.Before+e.After+e.Details, aiTestKey) {
		t.Error("the audit carries the key")
	}

	// A second save is conditional on the first's version.
	h.advance(time.Second)
	again := putAITask(owner, key, saved.Version, map[string]string{"model": "gpt-4o"})
	if again.status != http.StatusOK {
		t.Fatalf("second PUT: status %d body %s", again.status, again.body)
	}
	var second aiTaskJSON
	again.json(&second)
	if second.Version == saved.Version || second.Model != "gpt-4o" {
		t.Errorf("second = %+v, want gpt-4o under a newer version than %s", second, saved.Version)
	}
	if n := len(h.auditEvents(t, aiTaskModelChanged)); n != 2 {
		t.Errorf("%d audits, want 2", n)
	}
}

// If-Match is the task's version: a save from a screen that read it before
// someone else's is a 412 and changes nothing, and so is a first choice made
// against a version that is not 0.
func TestAISettings_IfMatchIsTheTasksVersion(t *testing.T) {
	t.Parallel()
	h := newAISettingsHarness(t)
	owner, _ := h.bootstrapOwner(t)
	key := "communications.customer-suggestion"

	stale := putAITask(owner, key, "12345", map[string]string{"model": "gpt-4.1"})
	if stale.status != http.StatusPreconditionFailed || !strings.Contains(string(stale.body), "version_mismatch") {
		t.Fatalf("a first choice against a version that is not 0: status %d body %s, want 412 version_mismatch", stale.status, stale.body)
	}
	first := putAITask(owner, key, `"0"`, map[string]string{"model": "gpt-4.1"})
	if first.status != http.StatusOK {
		t.Fatalf("first choice: status %d body %s", first.status, first.body)
	}
	h.advance(time.Second)
	// Another Owner's screen still holds version 0.
	late := putAITask(owner, key, "0", map[string]string{"model": "gpt-4o-mini"})
	if late.status != http.StatusPreconditionFailed {
		t.Errorf("a save from a screen that read 0 after another's: status %d, want 412", late.status)
	}
	if got := getAISettings(t, owner).task(t, key); got.Model != "gpt-4.1" {
		t.Errorf("the task uses %q after the refused saves, want the first choice gpt-4.1", got.Model)
	}
	if n := len(h.auditEvents(t, aiTaskModelChanged)); n != 1 {
		t.Errorf("%d audits, want only the one that wrote", n)
	}
}

// The refusals: a model the plan does not allow is 422 and writes nothing, an
// unknown task is 404, an If-Match that is not a version or a body without a
// model is 400.
func TestAISettings_Refusals(t *testing.T) {
	t.Parallel()
	h := newAISettingsHarness(t)
	owner, _ := h.bootstrapOwner(t)
	key := "communications.compose-reply"

	for _, tc := range []struct {
		name    string
		key     string
		version string
		body    any
		status  int
		code    string
	}{
		{"a model the plan does not allow", key, "0", map[string]string{"model": "gpt-5-unlisted"}, http.StatusUnprocessableEntity, "model_not_allowed"},
		{"a model that only differs in case", key, "0", map[string]string{"model": "GPT-4o"}, http.StatusUnprocessableEntity, "model_not_allowed"},
		{"an unknown task", "communications.no-such-task", "0", map[string]string{"model": "gpt-4o"}, http.StatusNotFound, "not_found"},
		{"another module's task that is not declared", "invoices.summarise", "0", map[string]string{"model": "gpt-4o"}, http.StatusNotFound, "not_found"},
		{"an If-Match that is not a version", key, "soon", map[string]string{"model": "gpt-4o"}, http.StatusBadRequest, "invalid_request"},
		{"a negative version", key, "-1", map[string]string{"model": "gpt-4o"}, http.StatusBadRequest, "invalid_request"},
		{"no model", key, "0", map[string]string{"model": "  "}, http.StatusBadRequest, "invalid_request"},
	} {
		r := putAITask(owner, tc.key, tc.version, tc.body)
		if r.status != tc.status || !strings.Contains(string(r.body), tc.code) {
			t.Errorf("%s: status %d body %s, want %d %s", tc.name, r.status, r.body, tc.status, tc.code)
		}
	}
	if n := h.count(t, `SELECT count(*) FROM identity.ai_task_models`); n != 0 {
		t.Errorf("%d choices written by refused requests, want none", n)
	}
	if n := len(h.auditEvents(t, aiTaskModelChanged)); n != 0 {
		t.Errorf("%d audits after refused requests, want none", n)
	}
}

// Without an AI provider the page does not exist: both operations are 404
// ai_not_configured, which is how the host knows to hide the page.
func TestAISettings_AbsentWithoutAProvider(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withAITasks(taskComposeReply))
	owner, _ := h.bootstrapOwner(t)

	if r := owner.do(http.MethodGet, aiSettingsPath, nil); r.status != http.StatusNotFound || !strings.Contains(string(r.body), "ai_not_configured") {
		t.Errorf("GET: status %d body %s, want 404 ai_not_configured", r.status, r.body)
	}
	if r := putAITask(owner, "communications.compose-reply", "0", map[string]string{"model": "gpt-4o"}); r.status != http.StatusNotFound ||
		!strings.Contains(string(r.body), "ai_not_configured") {
		t.Errorf("PUT: status %d body %s, want 404 ai_not_configured", r.status, r.body)
	}
}

// Both operations are the Owner's, and under OWNERS_REQUIRE_MFA an Owner's
// session must have verified a second factor: a user and an Owner without MFA
// are 403 and anonymous is 401.
func TestAISettings_OnlyAnOwnerWithMFA(t *testing.T) {
	t.Parallel()
	h := newAISettingsHarness(t, withEnv("OWNERS_REQUIRE_MFA", "1"))
	_, ownerID := h.bootstrapOwner(t)
	user := h.signIn(t, h.insertUser(t, "user@example.test", identity.RoleUserID), true)
	ownerWithoutMFA := h.signIn(t, ownerID, false)
	owner := h.signIn(t, ownerID, true)
	key := "communications.compose-reply"

	type op struct {
		method, path string
		body         any
		opts         []reqOpt
	}
	ops := []op{
		{http.MethodGet, aiSettingsPath, nil, nil},
		{http.MethodPut, aiTaskPathPrefix + key, map[string]string{"model": "gpt-4o"}, []reqOpt{header("If-Match", "0")}},
	}
	for _, o := range ops {
		for name, c := range map[string]*client{"a user": user, "an Owner without MFA": ownerWithoutMFA} {
			if r := c.do(o.method, o.path, o.body, o.opts...); r.status != http.StatusForbidden {
				t.Errorf("%s %s as %s: status %d, want 403", o.method, o.path, name, r.status)
			}
		}
		if r := h.client(t).do(o.method, o.path, o.body, o.opts...); r.status != http.StatusUnauthorized {
			t.Errorf("%s %s anonymously: status %d, want 401", o.method, o.path, r.status)
		}
	}
	if n := h.count(t, `SELECT count(*) FROM identity.ai_task_models`); n != 0 {
		t.Errorf("%d choices written by refused requests, want none", n)
	}
	for _, o := range ops {
		if r := owner.do(o.method, o.path, o.body, o.opts...); r.status != http.StatusOK {
			t.Errorf("%s %s as an Owner with MFA: status %d body %s, want 200", o.method, o.path, r.status, r.body)
		}
	}
}

// A choice for a model the plan used to allow stays in the table but no longer
// counts: the task uses what the resolution gives without it, and the settings
// say so (the source is no longer chosen).
func TestAISettings_AChoiceThePlanNoLongerAllowsFallsBack(t *testing.T) {
	t.Parallel()
	h := newAISettingsHarness(t)
	owner, _ := h.bootstrapOwner(t)
	h.exec(t, `INSERT INTO identity.ai_task_models (task_key, model, updated_at) VALUES ('communications.customer-suggestion', 'gpt-3.5-retired', $1)`, h.now())

	got := getAISettings(t, owner).task(t, "communications.customer-suggestion")
	if got.Model != "gpt-4o" || got.Source != "default" {
		t.Errorf("task = %+v, want the module's recommendation: the retired choice is not allowed", got)
	}
	if got.Version == "0" {
		t.Errorf("version = %q, want the stale row's version so a save replaces it", got.Version)
	}
	if r := putAITask(owner, "communications.customer-suggestion", got.Version, map[string]string{"model": "gpt-4.1"}); r.status != http.StatusOK {
		t.Errorf("replacing the stale choice: status %d body %s", r.status, r.body)
	}
	if _, err := strconv.ParseInt(got.Version, 10, 64); err != nil {
		t.Errorf("version %q is not a number: %v", got.Version, err)
	}
}
