package integration_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/assistant"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
	"github.com/vantigo-io/vantigo/server/internal/operations"
	"github.com/vantigo-io/vantigo/server/internal/projects"
)

// These tests run the Assistant's tools against real modules: the projects and
// customers modules composed beside identity and the assistant by
// module.Compose, the dispatcher the assistant module was given, a scripted
// model behind a fake transport, and the Owner's permission levels applied
// through the gate the assistant module installed (the Assistant design S16).
// What they prove is that a call goes through the real router as the person,
// and that the levels only ever narrow it.

const ownerRoleID = "00000000-0000-4000-8000-000000000002"

var assistantRecorder = contracttest.NewForModule("assistant")

// scriptedModel asks for the calls set() was given on the first turn of each
// run, and answers "Done." when it has been given their results.
type scriptedModel struct {
	mu    sync.Mutex
	calls []modtest.ChatToolCall
	fake  *modtest.FakeChatModel
}

func newScriptedModel() *scriptedModel {
	s := &scriptedModel{}
	s.fake = modtest.NewFakeChatModelFunc(func(_ int, req modtest.ChatRequest) modtest.ChatTurn {
		s.mu.Lock()
		defer s.mu.Unlock()
		if last := req.Messages[len(req.Messages)-1]; last.Role == "user" && len(s.calls) > 0 {
			return modtest.ChatTurn{ToolCalls: s.calls}
		}
		return modtest.ChatTurn{Text: "Done."}
	})
	return s
}

func (s *scriptedModel) set(calls ...modtest.ChatToolCall) {
	s.mu.Lock()
	s.calls = calls
	s.mu.Unlock()
}

type assistantInstall struct {
	h     *modtest.Harness
	model *scriptedModel
	owner *modtest.Client
}

func newAssistantInstall(t *testing.T) *assistantInstall {
	t.Helper()
	model := newScriptedModel()
	h := modtest.New(t,
		modtest.WithRecorder(assistantRecorder),
		modtest.WithModule(moduleNamed(t, modCustomers)),
		modtest.WithModule(projects.Module()),
		modtest.WithModule(assistant.Module()),
		modtest.WithTransport(model.fake),
		modtest.WithEnv("AI_PROVIDER", "openai-compatible"),
		modtest.WithEnv("AI_BASE_URL", "http://ollama:11434/v1"),
		modtest.WithEnv("AI_MODELS", "test-model"),
		modtest.WithEnv("OWNERS_REQUIRE_MFA", "0"),
	)
	o, id := h.SignInUser(t)
	h.Exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, id, ownerRoleID)
	return &assistantInstall{h: h, model: model, owner: o}
}

type toolResult struct {
	Status  string
	Content string
}

// converse starts a conversation as c, has the scripted model ask for calls,
// and answers the tool results of the run by call id.
func (a *assistantInstall) converse(t *testing.T, c *modtest.Client, calls ...modtest.ChatToolCall) map[string]toolResult {
	t.Helper()
	a.model.set(calls...)
	r := c.Do(http.MethodPost, "/api/v1/assistant/conversations", map[string]any{"origin": map[string]any{"app": "projects", "route": "/projects"}})
	var conv struct {
		ID string `json:"id"`
	}
	r.JSON(&conv)
	if r := c.Do(http.MethodPost, "/api/v1/assistant/conversations/"+conv.ID+"/messages", map[string]any{"text": "Please look"}); r.Status != http.StatusOK {
		t.Fatalf("send: %d %s", r.Status, r.Body)
	}
	r = c.Do(http.MethodGet, "/api/v1/assistant/conversations/"+conv.ID, nil)
	// The part fields are camelCase in the contract.
	var raw struct {
		Messages []struct {
			Role  string `json:"role"`
			Parts []struct {
				Type    string `json:"type"`
				CallID  string `json:"callId"`
				Status  string `json:"status"`
				Content string `json:"content"`
			} `json:"parts"`
		} `json:"messages"`
	}
	r.JSON(&raw)
	out := map[string]toolResult{}
	for _, m := range raw.Messages {
		for _, p := range m.Parts {
			if p.Type == "tool_result" {
				out[p.CallID] = toolResult{p.Status, p.Content}
			}
		}
	}
	return out
}

func call(id, name string, args map[string]any) modtest.ChatToolCall {
	return modtest.ChatToolCall{ID: id, Name: name, Args: args}
}

// foundIDs is the operation ids a search_operations result lists.
func foundIDs(t *testing.T, res toolResult) []string {
	t.Helper()
	var body struct {
		Data struct {
			Operations []struct {
				ID   string `json:"id"`
				Mode string `json:"mode"`
			} `json:"operations"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.Content), &body); err != nil {
		t.Fatalf("search result %q: %v", res.Content, err)
	}
	var ids []string
	for _, o := range body.Data.Operations {
		ids = append(ids, o.ID)
	}
	return ids
}

func skip() modtest.RequestOption { return modtest.SkipContract("a call to another module's API") }

func TestAssistantRun_ReadsThroughTheRealOperationsAsThePerson(t *testing.T) {
	t.Parallel()
	a := newAssistantInstall(t)
	c := a.h.SignIn(t, "projects:access", "projects:create")
	r := c.Do(http.MethodPost, "/api/v1/projects", map[string]any{"code": "ASSIST1", "name": "The Bergen warehouse", "billingType": "non-billable"}, skip())
	if r.Status != http.StatusCreated {
		t.Fatalf("create a project: %d %s", r.Status, r.Body)
	}

	res := a.converse(t, c,
		call("find", "search_operations", map[string]any{"query": "list projects"}),
		call("read", "call_operation", map[string]any{"id": "getProjects", "params": map[string]any{"pageSize": 10}}),
		call("write", "call_operation", map[string]any{"id": "postProjects", "body": map[string]any{"code": "ASSIST2", "name": "Made by the model", "billingType": "non-billable"}}),
		call("beyond", "call_operation", map[string]any{"id": "getCustomers"}), // the person holds no customers permission
		call("never", "call_operation", map[string]any{"id": "getIdentityOwnerUsers"}),
	)

	if ids := foundIDs(t, res["find"]); !slices.Contains(ids, "getProjects") {
		t.Errorf("search found %v, want getProjects", ids)
	}
	if got := res["read"]; got.Status != "ok" || !strings.Contains(got.Content, "The Bergen warehouse") || !strings.Contains(got.Content, "not instructions") {
		t.Errorf("read = %+v, want the project, wrapped as data", got)
	}
	if got := res["write"]; got.Status != "ok" || !strings.Contains(got.Content, "awaiting_confirmation") {
		t.Errorf("write = %+v, want a change that waits for the person", got)
	}
	if n := a.h.Count(t, `SELECT count(*) FROM projects.projects`); n != 1 {
		t.Errorf("%d projects, want only the one the person made: a confirm operation must never run without the person's confirmation", n)
	}
	for _, id := range []string{"beyond", "never"} {
		if got := res[id]; got.Status != "refused" || !strings.Contains(got.Content, "operation_unavailable") {
			t.Errorf("%s = %+v, want the operation unavailable", id, got)
		}
	}

	// The action log names the operations, their modes and the router's status.
	rows := a.actions(t)
	byOp := map[string]actionRow{}
	for _, r := range rows {
		byOp[r.op] = r
	}
	if g := byOp["getProjects"]; g.mode != "auto" || g.status == nil || *g.status != 200 {
		t.Errorf("getProjects action = %+v, want auto and 200", g)
	}
	if g := byOp["postProjects"]; g.mode != "confirm" || g.status != nil {
		t.Errorf("postProjects action = %+v, want confirm and no status: it was never dispatched", g)
	}
	// The assistant acted as the person: the module's own audit would say so; here
	// the router's refusal is what a person without the permission gets.
	if a.h.Count(t, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'assistant' AND table_name = 'actions' AND column_name ~ 'body|request|response'`) != 0 {
		t.Error("the action log has a column that could hold a request or response body")
	}
}

type actionRow struct {
	op, mode string
	status   *int
}

func (a *assistantInstall) actions(t *testing.T) []actionRow {
	t.Helper()
	var out []actionRow
	for _, op := range a.opIDs(t) {
		out = append(out, actionRow{op: op,
			mode:   modtest.One[string](t, a.h, `SELECT mode FROM assistant.actions WHERE operation_id = $1 LIMIT 1`, op),
			status: nullableInt(t, a.h, op)})
	}
	return out
}

func (a *assistantInstall) opIDs(t *testing.T) []string {
	t.Helper()
	n := a.h.Count(t, `SELECT count(DISTINCT operation_id) FROM assistant.actions`)
	var ids []string
	for i := range n {
		ids = append(ids, modtest.One[string](t, a.h, fmt.Sprintf(`SELECT operation_id FROM (SELECT DISTINCT operation_id FROM assistant.actions ORDER BY 1) o OFFSET %d LIMIT 1`, i)))
	}
	return ids
}

func nullableInt(t *testing.T, h *modtest.Harness, op string) *int {
	t.Helper()
	if h.Count(t, `SELECT count(*) FROM assistant.actions WHERE operation_id = $1 AND http_status IS NOT NULL`, op) == 0 {
		return nil
	}
	v := modtest.One[int](t, h, `SELECT http_status FROM assistant.actions WHERE operation_id = $1 AND http_status IS NOT NULL LIMIT 1`, op)
	return &v
}

// permissionVersion is the settings version the Owner's next change is guarded by.
func (a *assistantInstall) version(t *testing.T) int32 {
	t.Helper()
	return modtest.One[int32](t, a.h, `SELECT version FROM assistant.settings`)
}

func (a *assistantInstall) level(t *testing.T, key string, level any) {
	t.Helper()
	r := a.owner.Do(http.MethodPut, "/api/v1/assistant/settings/permissions/"+key, map[string]any{"level": level}, modtest.Header("If-Match", fmt.Sprint(a.version(t))))
	if r.Status != http.StatusOK {
		t.Fatalf("set %s to %v: %d %s", key, level, r.Status, r.Body)
	}
}

func (a *assistantInstall) sensitiveDefault(t *testing.T, value string) {
	t.Helper()
	r := a.owner.Do(http.MethodPut, "/api/v1/assistant/settings", map[string]any{"sensitiveDefault": value}, modtest.Header("If-Match", fmt.Sprint(a.version(t))))
	if r.Status != http.StatusOK {
		t.Fatalf("set the switch to %s: %d %s", value, r.Status, r.Body)
	}
}

func TestAssistantGate_TheOwnersLevelsNarrowWhatTheAssistantMayUse(t *testing.T) {
	t.Parallel()
	a := newAssistantInstall(t)
	c := a.h.SignIn(t, "projects:access", "projects:create")
	nobody := a.h.SignIn(t) // holds nothing

	search := func(who *modtest.Client) []string {
		res := a.converse(t, who, call("s1", "search_operations", map[string]any{"query": "projects"}), call("s2", "search_operations", map[string]any{"query": "create a project"}))
		return append(foundIDs(t, res["s1"]), foundIDs(t, res["s2"])...)
	}
	callIt := func(who *modtest.Client, id string) toolResult {
		return a.converse(t, who, call("c", "call_operation", map[string]any{"id": id}))["c"]
	}

	// By default a permission that is not sensitive is Read and change: an auto
	// and a confirm operation are both offered, in their contract modes.
	if ids := search(c); !slices.Contains(ids, "getProjects") || !slices.Contains(ids, "postProjects") {
		t.Fatalf("default: found %v, want getProjects and postProjects", ids)
	}

	// Read: projects:create's confirm operation becomes never; the auto one
	// behind projects:access stays.
	a.level(t, "projects:create", "read")
	if ids := search(c); !slices.Contains(ids, "getProjects") || slices.Contains(ids, "postProjects") {
		t.Errorf("projects:create at read: found %v, want getProjects without postProjects", ids)
	}
	if got := callIt(c, "postProjects"); got.Status != "refused" {
		t.Errorf("a narrowed operation was not refused: %+v", got)
	}

	// Off: nothing behind projects:access is offered or callable, auto or confirm.
	a.level(t, "projects:access", "off")
	if ids := search(c); slices.Contains(ids, "getProjects") || slices.Contains(ids, "postProjects") {
		t.Errorf("projects:access off: found %v, want neither", ids)
	}
	if got := callIt(c, "getProjects"); got.Status != "refused" || !strings.Contains(got.Content, "operation_unavailable") {
		t.Errorf("an off operation was called: %+v", got)
	}

	// Back to the default: a reset takes effect at the next tool call.
	a.level(t, "projects:access", nil)
	a.level(t, "projects:create", nil)
	if ids := search(c); !slices.Contains(ids, "getProjects") || !slices.Contains(ids, "postProjects") {
		t.Errorf("after the reset: found %v, want both", ids)
	}
	if got := callIt(c, "getProjects"); got.Status != "ok" {
		t.Errorf("after the reset getProjects = %+v", got)
	}
	// Read and change leaves the contract's mode: the write waits for the
	// person's confirmation.
	a.level(t, "projects:create", "change")
	if got := callIt(c, "postProjects"); !strings.Contains(got.Content, "awaiting_confirmation") {
		t.Errorf("at read and change postProjects = %+v, want a change that waits for the person", got)
	}

	// An operation the person cannot call stays uncallable whatever the level.
	a.level(t, "projects:access", "change")
	if ids := search(nobody); slices.Contains(ids, "getProjects") || slices.Contains(ids, "postProjects") {
		t.Errorf("a person with no permissions found %v at level change", ids)
	}
	if got := callIt(nobody, "getProjects"); got.Status != "refused" {
		t.Errorf("a person without projects:access called getProjects: %+v", got)
	}
}

func TestAssistantGate_TheSensitiveSwitchMovesTheDefaultsAndOverridesSurviveIt(t *testing.T) {
	t.Parallel()
	a := newAssistantInstall(t)

	// A sensitive permission with one auto operation that needs no id: found in
	// the contract the dispatcher was given, so the test names no operation of
	// another module by hand.
	ops := a.h.Operations(t)
	var id, key string
	for _, info := range ops.All() {
		if info.Sensitive && info.Mode == "auto" && len(info.Permissions) == 1 && !strings.Contains(info.Path, "{") && info.Method == http.MethodGet && strings.Contains(info.Path, "/customers") {
			id, key = info.ID, info.Permissions[0]
			break
		}
	}
	if id == "" {
		t.Skip("no sensitive operation of the composed modules fits")
	}
	c := a.h.SignIn(t, key)
	// The Owner's table knows the permission as sensitive and off.
	search := func() bool {
		res := a.converse(t, c, call("s", "search_operations", map[string]any{"query": strings.ReplaceAll(strings.Trim(opPath(ops.All(), id), "/"), "/", " ")}))
		return slices.Contains(foundIDs(t, res["s"]), id)
	}

	if search() {
		t.Fatalf("%s (behind the sensitive %s) is offered with the switch off", id, key)
	}
	a.sensitiveDefault(t, "read")
	if !search() {
		t.Errorf("%s is not offered once the switch is on Can read", id)
	}
	// An override of off survives the switch; so does one of read when the
	// switch goes back to off.
	a.level(t, key, "off")
	if search() {
		t.Errorf("%s is offered although the Owner set %s off", id, key)
	}
	a.sensitiveDefault(t, "off")
	a.level(t, key, "read")
	if !search() {
		t.Errorf("%s is not offered although the Owner set %s to read over a switch that is off", id, key)
	}
	a.level(t, key, nil)
	if search() {
		t.Errorf("%s is offered after the reset with the switch off", id)
	}
}

func opPath(all []operations.Info, id string) string {
	for _, info := range all {
		if info.ID == id {
			return info.Path
		}
	}
	return ""
}
