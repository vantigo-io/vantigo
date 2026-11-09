package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agents"
	"github.com/vantigo-io/vantigo/server/internal/assistant"
	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/knowledge"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/products"
)

// Colleagues talk to agents (virtual employees design V31, V32), end to end.

// TestMention_ANoteThatMentionsTheAgentWakesItWithTheNoteAsDataFromAColleague: a
// colleague's internal note on a conversation mentions the agent; the real
// Communications stores the mention and publishes the event, the trigger makes the
// request due at once, and the run starts with the note as colleagueNote, under
// the prompt that says a colleague asked. The agent answers the colleague with a
// note, through the real gate and grants.
func TestMention_ANoteThatMentionsTheAgentWakesItWithTheNoteAsDataFromAColleague(t *testing.T) {
	t.Parallel()
	r := newRunRig(t, "review",
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{toolCall("note", map[string]any{"text": "The invoice is on its way."})}},
		modtest.ChatTurn{Text: "I answered the colleague in a note."},
	)
	conv := r.mail(t, "Kari Nordmann <kari@example.test>", "", "Spørsmål", "Hei, hvor er fakturaen min?", "")

	const ask = "Please check the invoice for this customer and tell me what is missing."
	var created struct {
		MessageID string `json:"messageId"`
	}
	okJSON(t, r.staff, http.MethodPost, "/api/v1/communications/conversations/"+conv.String()+"/notes",
		map[string]any{"textBody": "@Hanna " + ask, "mentions": []string{r.userID.String()}}, &created)
	d := events.NewDispatcher(r.h.Pool(), agents.Module().EventHandlers(r.h.Deps()), r.h.Deps().Logger, r.h.Now)
	if err := d.RunOnce(context.Background()); err != nil {
		t.Fatalf("deliver the events: %v", err)
	}

	if state := r.await(t, conv); state == "failed" {
		t.Fatalf("the run failed; log:\n%s", r.h.Logs())
	}
	seeds := r.script.startedWith()
	if len(seeds) != 1 {
		t.Fatalf("%d runs started, want the one the mention and the message share", len(seeds))
	}
	seed := seeds[0]
	if !strings.Contains(seed, "colleagueNote") || !strings.Contains(seed, ask) {
		t.Errorf("the run did not start with the colleague's note as data: %s", seed)
	}
	if !strings.Contains(seed, "A colleague mentioned you in an internal note") {
		t.Errorf("the run's first message does not say a colleague asked: %s", seed)
	}
	var answered bool
	for _, n := range r.notes(t, conv) {
		answered = answered || strings.Contains(n, "The invoice is on its way.")
	}
	if !answered {
		t.Errorf("the agent's note is not on the conversation: %v", r.notes(t, conv))
	}
}

// The panel (virtual employees design V31): a person talks to an agent in the
// Assistant's panel. The run is the person's, so the router judges every call as
// them; the agent narrows it to what it may do too. These tests compose the real
// agents, Communications, Customers and Assistant, and prove the double check at
// the database: what is written, and when.

var panelRecorder = mergedRecorder("agents", "assistant", modCustomers, modInvoices, modProducts, "communications", "knowledge")

const panelStyle = "Short and friendly. Use first names."

// panelModel asks for the calls set() was given on the first turn of each run,
// answers "Done." after, and keeps the system prompt and the tools of the last
// request.
type panelModel struct {
	*scriptedModel
	mu     sync.Mutex
	system string
	tools  []string
}

func newPanelModel() *panelModel {
	p := &panelModel{scriptedModel: &scriptedModel{}}
	p.fake = modtest.NewFakeChatModelFunc(func(_ int, req modtest.ChatRequest) modtest.ChatTurn {
		p.mu.Lock()
		for _, m := range req.Messages {
			if m.Role == "system" {
				p.system = m.Content
			}
		}
		p.tools = req.Tools
		p.mu.Unlock()
		p.scriptedModel.mu.Lock()
		defer p.scriptedModel.mu.Unlock()
		if last := req.Messages[len(req.Messages)-1]; last.Role == "user" && len(p.calls) > 0 {
			return modtest.ChatTurn{ToolCalls: p.calls}
		}
		return modtest.ChatTurn{Text: "Done."}
	})
	return p
}

func (p *panelModel) prompt() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.system
}

func (p *panelModel) offered() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.tools...)
}

type panelRig struct {
	h       *modtest.Harness
	model   *panelModel
	owner   *modtest.Client
	agentID uuid.UUID
	userID  uuid.UUID
	channel uuid.UUID
}

// newPanelRig is an installation with the agent Hanna, whose levels are
// the front desk's needs lowered by lower (a permission key to a level), and
// whose Identity roles hold all of them.
func newPanelRig(t *testing.T, lower map[string]string) *panelRig {
	t.Helper()
	model := newPanelModel()
	h := modtest.New(t,
		modtest.WithRecorder(panelRecorder),
		modtest.WithModule(moduleNamed(t, modCustomers)),
		modtest.WithModule(moduleNamed(t, modInvoices)),
		modtest.WithModule(products.Module()),
		modtest.WithModule(communications.Module()),
		modtest.WithModule(knowledge.Module()),
		modtest.WithModule(agents.Module()),
		modtest.WithModule(assistant.Module()),
		modtest.WithTransport(model.fake),
		modtest.WithEnv("AI_PROVIDER", "openai-compatible"),
		modtest.WithEnv("AI_BASE_URL", "http://ollama:11434/v1"),
		modtest.WithEnv("AI_MODELS", "test-model"),
		modtest.WithEnv("OWNERS_REQUIRE_MFA", "0"),
		modtest.WithEnv("STORAGE_PROVIDER", "fs"),
		modtest.WithEnv("STORAGE_FS_ROOT", t.TempDir()),
		modtest.WithEnv("STORAGE_FS_ALLOW_INSECURE_ROOT", "1"),
	)
	owner, ownerID := h.SignInUser(t)
	h.Exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, ownerID, ownerRoleID)
	a := &agentsInstall{h: h, owner: owner}
	if r := owner.Do(http.MethodPut, "/api/v1/agents/settings", map[string]any{"companyName": "Hansen Rør", "retentionMonths": 6}, modtest.Header("If-Match", "1")); r.Status != http.StatusOK {
		t.Fatalf("set the company's name: %d %s", r.Status, r.Body)
	}
	var ag struct {
		ID     uuid.UUID `json:"id"`
		UserID uuid.UUID `json:"userId"`
	}
	okJSON(t, owner, http.MethodPost, "/api/v1/agents/agents", map[string]any{
		"name": "Hanna", "style": panelStyle, "languages": "customer", "disclosureWording": "assistant", "model": "",
	}, &ag)
	okJSON(t, owner, http.MethodPost, "/api/v1/agents/agents/"+ag.ID.String()+"/roles", map[string]any{"template": "front_desk", "name": "Front desk", "instructions": ""}, nil)
	var keys []string
	for key, level := range a.needs(t, ag.ID) {
		if l, ok := lower[key]; ok {
			level = l
		}
		a.level(t, ag.ID, key, level)
		keys = append(keys, key)
	}
	a.roles(t, ag.UserID, keys...)

	// The Owner lets the Assistant use the sensitive permissions the panel reads.
	for _, key := range []string{"communications:conversations-view", "customers:view"} {
		version := modtest.One[int32](t, h, `SELECT version FROM assistant.settings`)
		if r := owner.Do(http.MethodPut, "/api/v1/assistant/settings/permissions/"+key, map[string]any{"level": "change"}, modtest.Header("If-Match", fmt.Sprint(version))); r.Status != http.StatusOK && r.Status != http.StatusNotFound {
			t.Fatalf("allow %s: %d %s", key, r.Status, r.Body)
		}
	}
	channel := uuid.New()
	h.Exec(t, `INSERT INTO communications.channels (id, type, address, created_at) VALUES ($1, 'email', $2, now())`, channel, "front-"+channel.String()[:8]+"@example.test")
	return &panelRig{h: h, model: model, owner: owner, agentID: ag.ID, userID: ag.UserID, channel: channel}
}

func (p *panelRig) conversation(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.New()
	p.h.Exec(t, `INSERT INTO communications.conversations (id, channel_id, last_activity_at, created_at) VALUES ($1, $2, now(), now())`, id, p.channel)
	return id
}

func (p *panelRig) status(t *testing.T, conv uuid.UUID) string {
	t.Helper()
	return modtest.One[string](t, p.h, `SELECT status FROM communications.conversations WHERE id = $1`, conv)
}

// talk starts a conversation with the agent as c and sends one message with the
// scripted calls; it answers the conversation's id and the tool results by call id.
func (p *panelRig) talk(t *testing.T, c *modtest.Client, calls ...modtest.ChatToolCall) (string, map[string]toolResult) {
	t.Helper()
	p.model.set(calls...)
	r := c.Do(http.MethodPost, "/api/v1/assistant/conversations", map[string]any{"agentId": p.agentID, "origin": map[string]any{"app": "agents", "route": "/agents"}})
	if r.Status != http.StatusCreated {
		t.Fatalf("start a conversation with the agent: %d %s", r.Status, r.Body)
	}
	var conv struct {
		ID    string `json:"id"`
		Agent *struct {
			ID   uuid.UUID `json:"id"`
			Name string    `json:"name"`
		} `json:"agent"`
	}
	r.JSON(&conv)
	if conv.Agent == nil || conv.Agent.ID != p.agentID || conv.Agent.Name != "Hanna" {
		t.Fatalf("conversation agent = %+v, want Hanna", conv.Agent)
	}
	if r := c.Do(http.MethodPost, "/api/v1/assistant/conversations/"+conv.ID+"/messages", map[string]any{"text": "Please help"}); r.Status != http.StatusOK {
		t.Fatalf("send: %d %s\n%s", r.Status, r.Body, p.h.Logs())
	}
	return conv.ID, toolResults(t, c, conv.ID)
}

func toolResults(t *testing.T, c *modtest.Client, conv string) map[string]toolResult {
	t.Helper()
	var raw struct {
		Messages []struct {
			Parts []struct {
				Type    string `json:"type"`
				CallID  string `json:"callId"`
				Status  string `json:"status"`
				Content string `json:"content"`
			} `json:"parts"`
		} `json:"messages"`
	}
	c.Do(http.MethodGet, "/api/v1/assistant/conversations/"+conv, nil).JSON(&raw)
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

func closeIt(conv uuid.UUID) modtest.ChatToolCall {
	return call("close", "call_operation", map[string]any{
		"id": "patchCommunicationsConversationsById", "params": map[string]any{"id": conv.String()},
		"explain": "Close the conversation", "body": map[string]any{"status": "closed"},
	})
}

var panelPermissions = []string{"agents:view", "communications:conversations-view", "communications:conversations-manage"}

func TestPanel_OnlyAHolderOfAgentsViewMeetsAnAgentAndAPausedOneIsGone(t *testing.T) {
	t.Parallel()
	p := newPanelRig(t, nil)
	viewer := p.h.SignIn(t, "agents:view")
	plain := p.h.SignIn(t)

	var list struct {
		Items []struct {
			ID   uuid.UUID `json:"id"`
			Name string    `json:"name"`
		} `json:"items"`
	}
	okJSON(t, viewer, http.MethodGet, "/api/v1/assistant/agents", nil, &list)
	if len(list.Items) != 1 || list.Items[0].ID != p.agentID || list.Items[0].Name != "Hanna" {
		t.Errorf("the picker offers %+v to a holder of agents:view, want Hanna", list.Items)
	}
	okJSON(t, plain, http.MethodGet, "/api/v1/assistant/agents", nil, &list)
	if len(list.Items) != 0 {
		t.Errorf("the picker offers %+v to a person without agents:view, want none", list.Items)
	}

	body := map[string]any{"agentId": p.agentID, "origin": map[string]any{"app": "agents", "route": "/agents"}}
	if r := plain.Do(http.MethodPost, "/api/v1/assistant/conversations", body); r.Status != http.StatusForbidden || problemCode(r) != "agent_unavailable" {
		t.Errorf("a conversation with an agent without agents:view = %d %s, want 403 agent_unavailable", r.Status, r.Body)
	}
	if r := viewer.Do(http.MethodPost, "/api/v1/assistant/conversations", map[string]any{"agentId": uuid.New(), "origin": map[string]any{"app": "agents", "route": "/agents"}}); r.Status != http.StatusNotFound {
		t.Errorf("a conversation with an unknown agent = %d, want 404", r.Status)
	}

	// A conversation that exists, then the agent is paused: it is named still, and
	// the next message is refused.
	conv, _ := p.talk(t, viewer)
	if r := p.owner.Do(http.MethodPost, "/api/v1/agents/agents/"+p.agentID.String()+"/pause", nil); r.Status != http.StatusOK {
		t.Fatalf("pause: %d %s", r.Status, r.Body)
	}
	okJSON(t, viewer, http.MethodGet, "/api/v1/assistant/agents", nil, &list)
	if len(list.Items) != 0 {
		t.Errorf("the picker offers a paused agent: %+v", list.Items)
	}
	if r := viewer.Do(http.MethodPost, "/api/v1/assistant/conversations", body); r.Status != http.StatusNotFound {
		t.Errorf("a new conversation with a paused agent = %d, want 404", r.Status)
	}
	if r := viewer.Do(http.MethodPost, "/api/v1/assistant/conversations/"+conv+"/messages", map[string]any{"text": "Still there?"}); r.Status != http.StatusConflict || problemCode(r) != "agent_unavailable" {
		t.Errorf("a message to a paused agent = %d %s, want 409 agent_unavailable", r.Status, r.Body)
	}
	var d struct {
		Conversation struct {
			Agent *struct{ Name string } `json:"agent"`
		} `json:"conversation"`
	}
	okJSON(t, viewer, http.MethodGet, "/api/v1/assistant/conversations/"+conv, nil, &d)
	if d.Conversation.Agent == nil || d.Conversation.Agent.Name != "Hanna" {
		t.Errorf("the conversation with a paused agent names %+v, want Hanna still", d.Conversation.Agent)
	}
}

func TestPanel_TheAgentSpeaksWithItsPersonaAndOffersNoBulkChange(t *testing.T) {
	t.Parallel()
	p := newPanelRig(t, nil)
	c := p.h.SignIn(t, panelPermissions...)
	p.talk(t, c)

	prompt := p.model.prompt()
	for _, want := range []string{"You are Hanna", "an AI", panelStyle, "every change is shown to the colleague on a card"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the system prompt lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "You are the Assistant in Vantigo") {
		t.Errorf("the system prompt is the Assistant's own")
	}
	if tools := p.model.offered(); !slices.Contains(tools, "call_operation") || slices.Contains(tools, "propose_bulk") {
		t.Errorf("tools = %v, want the operations and no bulk change", tools)
	}
}

func TestPanel_AChangeBothMayMakeIsACardThePersonConfirmsAndNothingRunsBefore(t *testing.T) {
	t.Parallel()
	p := newPanelRig(t, nil)
	c := p.h.SignIn(t, panelPermissions...)
	conv := p.conversation(t)

	id, results := p.talk(t, c, closeIt(conv))
	if res := results["close"]; !strings.Contains(res.Content, "awaiting_confirmation") {
		t.Fatalf("the proposal = %+v, want a change that waits for the person", res)
	}
	if got := p.status(t, conv); got != "open" {
		t.Fatalf("status = %s before the person confirmed, want open", got)
	}
	var v conversationView
	okJSON(t, c, http.MethodGet, "/api/v1/assistant/conversations/"+id, nil, &v)
	if len(v.PendingActions) != 1 || v.PendingActions[0].Status != "pending" {
		t.Fatalf("cards = %+v, want one pending", v.PendingActions)
	}
	if r := c.Do(http.MethodPost, "/api/v1/assistant/pending-actions/"+v.PendingActions[0].ID+"/confirm", nil); r.Status != http.StatusOK {
		t.Fatalf("confirm: %d %s", r.Status, r.Body)
	}
	if got := p.status(t, conv); got != "closed" {
		t.Errorf("status = %s after the confirmation, want closed", got)
	}
	// It was made as the person, in the Assistant's run: no agent grant, no agents run.
	if n := p.h.Count(t, `SELECT count(*) FROM agents.runs`); n != 0 {
		t.Errorf("%d agent runs, want none: a panel conversation is no run of the agent", n)
	}
}

func TestPanel_AnOperationTheAgentMayUseAndThePersonMayNotIsRefused(t *testing.T) {
	t.Parallel()
	p := newPanelRig(t, nil)
	conv := p.conversation(t)
	// The person may read conversations but not manage them; the agent may.
	c := p.h.SignIn(t, "agents:view", "communications:conversations-view")

	_, results := p.talk(t, c, closeIt(conv), call("find", "search_operations", map[string]any{"query": "update conversation status assign"}))
	if res := results["close"]; res.Status != "refused" || !strings.Contains(res.Content, "operation_unavailable") {
		t.Errorf("the change the person may not make = %+v, want refused as unavailable", res)
	}
	if res := results["find"]; strings.Contains(res.Content, "patchCommunicationsConversationsById") {
		t.Errorf("the search offers an operation the person may not use: %s", res.Content)
	}
	if got := p.status(t, conv); got != "open" {
		t.Errorf("status = %s, want open", got)
	}
	if n := p.h.Count(t, `SELECT count(*) FROM assistant.pending_actions`); n != 0 {
		t.Errorf("%d cards for a change the person may not make", n)
	}
}

func TestPanel_AnOperationThePersonMayUseAndTheAgentMayNotIsRefused(t *testing.T) {
	t.Parallel()
	// The Owner has given Hanna read only on managing conversations.
	p := newPanelRig(t, map[string]string{"communications:conversations-manage": "read"})
	c := p.h.SignIn(t, panelPermissions...)
	conv := p.conversation(t)

	_, results := p.talk(t, c, closeIt(conv), call("find", "search_operations", map[string]any{"query": "update conversation status assign"}))
	if res := results["close"]; res.Status != "refused" || !strings.Contains(res.Content, "operation_unavailable") {
		t.Errorf("the change the agent may not make = %+v, want refused as unavailable", res)
	}
	if res := results["find"]; strings.Contains(res.Content, "patchCommunicationsConversationsById") {
		t.Errorf("the search offers an operation the agent may not use: %s", res.Content)
	}
	if n := p.h.Count(t, `SELECT count(*) FROM assistant.pending_actions`); n != 0 {
		t.Errorf("%d cards for a change the agent may not make", n)
	}

	// The same person asks the Assistant itself and gets the card.
	p.model.set(closeIt(conv))
	r := c.Do(http.MethodPost, "/api/v1/assistant/conversations", map[string]any{"origin": map[string]any{"app": "agents", "route": "/agents"}})
	var own struct {
		ID string `json:"id"`
	}
	r.JSON(&own)
	if r := c.Do(http.MethodPost, "/api/v1/assistant/conversations/"+own.ID+"/messages", map[string]any{"text": "Close it"}); r.Status != http.StatusOK {
		t.Fatalf("send: %d %s", r.Status, r.Body)
	}
	if n := p.h.Count(t, `SELECT count(*) FROM assistant.pending_actions`); n != 1 {
		t.Errorf("%d cards from the Assistant itself, want the one the agent was not allowed to make", n)
	}
}

func TestPanel_TheAgentGetsNoCustomersDataWhateverThePersonMayRead(t *testing.T) {
	t.Parallel()
	p := newPanelRig(t, nil)
	c := p.h.SignIn(t, append([]string{"customers:view", "customers:create"}, panelPermissions...)...)
	var cust struct {
		Id int32 `json:"id"`
	}
	okJSON(t, p.owner, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Acme AS"}, &cust)

	read := call("read", "call_operation", map[string]any{"id": "getCustomer", "params": map[string]any{"id": cust.Id}})
	_, results := p.talk(t, c, read)
	if res := results["read"]; res.Status != "refused" || !strings.Contains(res.Content, "operation_unavailable") || strings.Contains(res.Content, "Acme") {
		t.Errorf("the customer read through the agent = %+v, want refused: a panel run has no customer", res)
	}

	// The person's own Assistant reads it.
	p.model.set(read)
	r := c.Do(http.MethodPost, "/api/v1/assistant/conversations", map[string]any{"origin": map[string]any{"app": "customers", "route": "/customers"}})
	var own struct {
		ID string `json:"id"`
	}
	r.JSON(&own)
	if r := c.Do(http.MethodPost, "/api/v1/assistant/conversations/"+own.ID+"/messages", map[string]any{"text": "Look"}); r.Status != http.StatusOK {
		t.Fatalf("send: %d %s", r.Status, r.Body)
	}
	if res := toolResults(t, c, own.ID)["read"]; res.Status != "ok" || !strings.Contains(res.Content, "Acme") {
		t.Errorf("the Assistant's own read = %+v, want the customer", res)
	}
}

func TestPanel_ACardThatWaitsIsNotConfirmedOnceTheAgentMayNoLongerMakeIt(t *testing.T) {
	t.Parallel()
	p := newPanelRig(t, nil)
	c := p.h.SignIn(t, panelPermissions...)
	conv := p.conversation(t)
	id, _ := p.talk(t, c, closeIt(conv))
	var v conversationView
	okJSON(t, c, http.MethodGet, "/api/v1/assistant/conversations/"+id, nil, &v)
	if len(v.PendingActions) != 1 {
		t.Fatalf("cards = %+v, want one", v.PendingActions)
	}
	// The Owner lowers Hanna's level while the card waits.
	okJSON(t, p.owner, http.MethodPut, fmt.Sprintf("/api/v1/agents/agents/%s/permissions/communications:conversations-manage", p.agentID), map[string]any{"level": "read"}, nil)
	if r := c.Do(http.MethodPost, "/api/v1/assistant/pending-actions/"+v.PendingActions[0].ID+"/confirm", nil); r.Status != http.StatusForbidden || problemCode(r) != "operation_unavailable" {
		t.Errorf("confirm after the level was lowered = %d %s, want 403 operation_unavailable", r.Status, r.Body)
	}
	if got := p.status(t, conv); got != "open" {
		t.Errorf("status = %s, the change was made", got)
	}
}
