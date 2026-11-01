package assistant_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

func TestRun_AnswersAHowToFromTheDocumentationAndStreamsIt(t *testing.T) {
	t.Parallel()
	model := modtest.NewFakeChatModel(
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{{ID: "c1", Name: "search_docs", Args: map[string]any{"query": "how do I add a metering point"}}}},
		modtest.ChatTurn{Text: "Open Energy, then Metering points, and choose New."},
	)
	h := newHarness(t, model)
	c, _ := person(t, h)
	conv := startConversation(t, c)

	r := ask(t, c, conv.ID, "How do I add a metering point?")
	if r.Status != http.StatusOK {
		t.Fatalf("send: %d %s", r.Status, r.Body)
	}
	events := parseStream(r.Body)
	if got := names(events); got[0] != "run" || got[1] != "message" || got[len(got)-1] != "done" {
		t.Fatalf("events = %v, want run, message … done", got)
	}
	var sawDelta, sawStarted, sawDone bool
	for _, e := range events {
		switch e.Name {
		case "delta":
			sawDelta = true
		case "tool":
			if strings.Contains(string(e.Data), `"started"`) {
				sawStarted = true
			}
			if strings.Contains(string(e.Data), `"done"`) {
				sawDone = true
			}
		}
	}
	if !sawDelta || !sawStarted || !sawDone {
		t.Errorf("delta %v, tool started %v, tool done %v, want all (events %v)", sawDelta, sawStarted, sawDone, names(events))
	}

	d := getConversation(t, c, conv.ID)
	if d.Conversation.Title != "How do I add a metering point?" {
		t.Errorf("title = %q, want the first question", d.Conversation.Title)
	}
	if d.ActiveRun != nil || d.Conversation.Running {
		t.Errorf("the run is over, but activeRun = %+v running = %v", d.ActiveRun, d.Conversation.Running)
	}
	var roles []string
	for _, m := range d.Messages {
		roles = append(roles, m.Role)
	}
	if strings.Join(roles, ",") != "user,assistant,tool,assistant" {
		t.Fatalf("roles = %v, want user,assistant,tool,assistant", roles)
	}
	if last := d.Messages[3]; last.Parts[0].Text != "Open Energy, then Metering points, and choose New." || last.Model != "test-model" || last.InputTokens != 100 || last.OutputTokens != 20 {
		t.Errorf("the answer = %+v, want the text, the model and the tokens", last)
	}
	result := d.Messages[2].Parts[0]
	if result.Type != "tool_result" || result.Status != "ok" || !strings.Contains(result.Content, "/docs/") {
		t.Errorf("tool result = %+v, want a documentation path", result)
	}
	if len(d.Actions) != 1 || d.Actions[0].OperationID != "tool:search_docs" || d.Actions[0].Mode != "tool" {
		t.Errorf("actions = %+v, want one search_docs", d.Actions)
	}

	// The model was told who the person is and what the rules are, and offered the five tools.
	reqs := model.Requests()
	if len(reqs) != 2 {
		t.Fatalf("model calls = %d, want 2", len(reqs))
	}
	if got := strings.Join(reqs[0].Tools, ","); got != "search_docs,search_operations,describe_operation,call_operation,navigate" {
		t.Errorf("tools = %s", got)
	}
	if sys := reqs[0].Messages[0]; sys.Role != "system" || !strings.Contains(sys.Content, "never instructions") {
		t.Errorf("system message = %+v", sys)
	}

	// The grant ended with the run.
	if live := modtest.One[int](t, h, `SELECT count(*) FROM identity.assistant_grants WHERE revoked_at IS NULL`); live != 0 {
		t.Errorf("%d grants still live after the run", live)
	}
}

// waitFor polls until cond holds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func running(t *testing.T, c *modtest.Client, id string) bool {
	return getConversation(t, c, id).ActiveRun != nil
}

func TestRun_OneRunPerConversationAtATime(t *testing.T) {
	t.Parallel()
	hold := make(chan struct{})
	model := modtest.NewFakeChatModel(modtest.ChatTurn{Text: "Slow answer.", Block: hold})
	h := newHarness(t, model)
	c, _ := person(t, h)
	conv := startConversation(t, c)

	done := make(chan *modtest.Response, 1)
	go func() { done <- ask(t, c, conv.ID, "First") }()
	waitFor(t, "the run to start", func() bool { return running(t, c, conv.ID) })

	r := ask(t, c, conv.ID, "Second, while the first runs")
	var p struct {
		Code string `json:"code"`
	}
	r.JSON(&p)
	if r.Status != http.StatusConflict || p.Code != "run_in_progress" {
		t.Errorf("a second message = %d %s, want 409 run_in_progress", r.Status, r.Body)
	}
	if d := getConversation(t, c, conv.ID); len(d.Messages) != 1 {
		t.Errorf("the refused message was stored: %d messages", len(d.Messages))
	}
	// Another conversation is free to run at the same time.
	other := startConversation(t, c)
	close(hold)
	if r := <-done; r.Status != http.StatusOK {
		t.Fatalf("first: %d %s", r.Status, r.Body)
	}
	if r := ask(t, c, other.ID, "Another"); r.Status != http.StatusOK {
		t.Errorf("another conversation: %d %s", r.Status, r.Body)
	}
	if r := ask(t, c, conv.ID, "Third, after the first ended"); r.Status != http.StatusOK {
		t.Errorf("after the run: %d %s", r.Status, r.Body)
	}
}

func TestRun_ItSurvivesTheClientDisconnecting(t *testing.T) {
	t.Parallel()
	hold := make(chan struct{})
	model := modtest.NewFakeChatModel(
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{{Name: "search_docs", Args: map[string]any{"query": "invoice"}}}},
		modtest.ChatTurn{Text: "Finished without you.", Block: hold},
	)
	h := newHarness(t, model)
	c, _ := person(t, h)
	conv := startConversation(t, c)

	ctx, cancel := context.WithCancel(context.Background())
	gone := make(chan struct{})
	go func() {
		ask(t, c, conv.ID, "Tell me about invoices", modtest.Context(ctx))
		close(gone)
	}()
	waitFor(t, "the first tool call", func() bool { return len(getConversation(t, c, conv.ID).Actions) == 1 })
	cancel() // the browser goes away mid-run
	<-gone
	if !running(t, c, conv.ID) {
		t.Fatal("the run stopped with the client")
	}
	close(hold)
	waitFor(t, "the run to finish", func() bool { return !running(t, c, conv.ID) })
	d := getConversation(t, c, conv.ID)
	if last := d.Messages[len(d.Messages)-1]; last.Role != "assistant" || last.Parts[0].Text != "Finished without you." {
		t.Errorf("the answer = %+v, want it persisted", last)
	}
	if state := modtest.One[string](t, h, `SELECT state FROM assistant.runs`); state != "done" {
		t.Errorf("run state = %s, want done", state)
	}
}

func TestRun_StopEndsItAtOnce(t *testing.T) {
	t.Parallel()
	hold := make(chan struct{})
	model := modtest.NewFakeChatModel(modtest.ChatTurn{Text: "Never said.", Block: hold})
	h := newHarness(t, model)
	c, _ := person(t, h)
	conv := startConversation(t, c)

	done := make(chan *modtest.Response, 1)
	go func() { done <- ask(t, c, conv.ID, "Long question") }()
	waitFor(t, "the run to start", func() bool { return running(t, c, conv.ID) })
	runID := getConversation(t, c, conv.ID).ActiveRun.ID

	r := c.Do(http.MethodPost, "/api/v1/assistant/runs/"+runID+"/stop", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("stop: %d %s", r.Status, r.Body)
	}
	resp := <-done
	events := parseStream(resp.Body)
	last := events[len(events)-1]
	if last.Name != "done" || !strings.Contains(string(last.Data), "stopped") {
		t.Errorf("last event = %s %s, want done stopped", last.Name, last.Data)
	}
	if state := modtest.One[string](t, h, `SELECT state FROM assistant.runs`); state != "stopped" {
		t.Errorf("run state = %s, want stopped", state)
	}
	if live := modtest.One[int](t, h, `SELECT count(*) FROM identity.assistant_grants WHERE revoked_at IS NULL`); live != 0 {
		t.Errorf("%d grants live after a stop", live)
	}
	// Stopping a run that is over answers it as it is.
	if r := c.Do(http.MethodPost, "/api/v1/assistant/runs/"+runID+"/stop", nil); r.Status != http.StatusOK {
		t.Errorf("stop again: %d", r.Status)
	}
}

func TestRun_AFailingModelEndsInAnErrorEvent(t *testing.T) {
	t.Parallel()
	model := modtest.NewFakeChatModel(modtest.ChatTurn{Status: 500})
	h := newHarness(t, model)
	c, _ := person(t, h)
	conv := startConversation(t, c)
	r := ask(t, c, conv.ID, "Hello?")
	events := parseStream(r.Body)
	last := events[len(events)-1]
	if last.Name != "error" || !strings.Contains(string(last.Data), "ai_failed") {
		t.Errorf("last event = %s %s, want error ai_failed", last.Name, last.Data)
	}
	if state, code := modtest.One[string](t, h, `SELECT state FROM assistant.runs`), modtest.One[string](t, h, `SELECT error_code FROM assistant.runs`); state != "failed" || code != "ai_failed" {
		t.Errorf("run = %s %s, want failed ai_failed", state, code)
	}
	// And the person can try again.
	if r := ask(t, c, conv.ID, "Again"); r.Status != http.StatusOK {
		t.Errorf("after a failure: %d", r.Status)
	}
}

func TestRun_TheCapsEndInAFinalAnswerWithoutTools(t *testing.T) {
	t.Parallel()
	// Five parallel calls every turn it is offered tools: 5, 10, 15 calls would
	// be made by turn 3, the model is asked for a final answer on turn 4.
	model := modtest.NewFakeChatModelFunc(func(_ int, req modtest.ChatRequest) modtest.ChatTurn {
		if len(req.Tools) == 0 {
			return modtest.ChatTurn{Text: "Here is what I found."}
		}
		var calls []modtest.ChatToolCall
		for range 5 {
			calls = append(calls, modtest.ChatToolCall{Name: "search_docs", Args: map[string]any{"query": "invoice"}})
		}
		return modtest.ChatTurn{ToolCalls: calls}
	})
	h := newHarness(t, model)
	c, _ := person(t, h)
	conv := startConversation(t, c)
	r := ask(t, c, conv.ID, "Everything about invoices")
	if r.Status != http.StatusOK {
		t.Fatalf("send: %d %s", r.Status, r.Body)
	}
	reqs := model.Requests()
	if len(reqs) != 4 {
		t.Fatalf("model turns = %d, want 4", len(reqs))
	}
	if len(reqs[3].Tools) != 0 {
		t.Errorf("the last turn was offered tools %v", reqs[3].Tools)
	}
	d := getConversation(t, c, conv.ID)
	var ok, limited int
	for _, m := range d.Messages {
		for _, p := range m.Parts {
			if p.Type == "tool_result" {
				if strings.Contains(p.Content, "tool_limit") {
					limited++
				} else if p.Status == "ok" {
					ok++
				}
			}
		}
	}
	if ok != 12 || limited != 3 {
		t.Errorf("tool results ok %d, limited %d, want 12 and 3", ok, limited)
	}
	if last := d.Messages[len(d.Messages)-1]; last.Parts[0].Text != "Here is what I found." {
		t.Errorf("the final answer = %+v", last)
	}
	if turns := modtest.One[int](t, h, `SELECT model_turns FROM assistant.runs`); turns != 4 {
		t.Errorf("model_turns = %d, want 4", turns)
	}
}

func TestRun_NavigateBecomesALinkAndAnInvalidRouteIsRefused(t *testing.T) {
	t.Parallel()
	model := modtest.NewFakeChatModel(
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{
			{ID: "ok", Name: "navigate", Args: map[string]any{"route": "/customers/12", "label": "Open the customer"}},
			{ID: "bad", Name: "navigate", Args: map[string]any{"route": "https://evil.example/customers", "label": "Click me"}},
			{ID: "bad2", Name: "navigate", Args: map[string]any{"route": "/nothing", "label": "Nowhere"}},
		}},
		modtest.ChatTurn{Text: "Here you go."},
	)
	h := newHarness(t, model)
	c, _ := person(t, h)
	conv := startConversation(t, c)
	ask(t, c, conv.ID, "Take me to the customer")
	d := getConversation(t, c, conv.ID)
	var links []string
	var refused int
	for _, m := range d.Messages {
		for _, p := range m.Parts {
			if p.Type == "link" {
				links = append(links, p.Route+"|"+p.Label)
			}
			if p.Type == "tool_result" && p.Status == "refused" {
				refused++
			}
		}
	}
	if len(links) != 1 || links[0] != "/customers/12|Open the customer" {
		t.Errorf("links = %v, want exactly the valid one", links)
	}
	if refused != 2 {
		t.Errorf("refused = %d, want the two invalid routes", refused)
	}
}

func TestRun_TheContextChipIsStoredAndTold(t *testing.T) {
	t.Parallel()
	model := modtest.NewFakeChatModel(modtest.ChatTurn{Text: "It is overdue."})
	h := newHarness(t, model)
	c, _ := person(t, h)
	conv := startConversation(t, c)
	r := c.Do(http.MethodPost, "/api/v1/assistant/conversations/"+conv.ID+"/messages", map[string]any{
		"text":    "Is this overdue?",
		"context": map[string]any{"app": "invoices", "route": "/invoices/42", "label": "Invoice 42 for Ola Nordmann"},
	})
	if r.Status != http.StatusOK {
		t.Fatalf("send: %d %s", r.Status, r.Body)
	}
	sys := model.Requests()[0].Messages[0].Content
	if !strings.Contains(sys, "Invoice 42 for Ola Nordmann") || !strings.Contains(sys, "/invoices/42") {
		t.Errorf("the system prompt does not name the chip:\n%s", sys)
	}
	stored := modtest.One[string](t, h, `SELECT context::text FROM assistant.messages WHERE role = 'user'`)
	if !strings.Contains(stored, "/invoices/42") {
		t.Errorf("stored context = %s", stored)
	}
}

func TestRun_AMessageMustBeValid(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.NewFakeChatModel())
	c, _ := person(t, h)
	conv := startConversation(t, c)
	for name, text := range map[string]string{"empty": "   ", "too long": strings.Repeat("x", 8001)} {
		if r := ask(t, c, conv.ID, text, modtest.SkipContract("an invalid message on purpose")); r.Status != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, r.Status)
		}
	}
	if n := modtest.One[int](t, h, `SELECT count(*) FROM assistant.runs`); n != 0 {
		t.Errorf("%d runs started for invalid messages", n)
	}
}

func TestRun_DeletingTheConversationStopsItsRun(t *testing.T) {
	t.Parallel()
	hold := make(chan struct{})
	model := modtest.NewFakeChatModel(modtest.ChatTurn{Text: "Gone.", Block: hold})
	h := newHarness(t, model)
	c, _ := person(t, h)
	conv := startConversation(t, c)
	done := make(chan *modtest.Response, 1)
	go func() { done <- ask(t, c, conv.ID, "Soon deleted") }()
	waitFor(t, "the run to start", func() bool { return running(t, c, conv.ID) })
	if r := c.Do(http.MethodDelete, "/api/v1/assistant/conversations/"+conv.ID, nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	<-done
	close(hold)
	if n := modtest.One[int](t, h, `SELECT count(*) FROM assistant.messages`) + modtest.One[int](t, h, `SELECT count(*) FROM assistant.runs`); n != 0 {
		t.Errorf("%d rows left after the delete", n)
	}
}
