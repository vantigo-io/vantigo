package assistant_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/assistant"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The Assistant on AI_PROVIDER=anthropic (the Assistant design S18): the
// history it sends is append-only, so a Claude model's thinking blocks stay
// valid.

func newClaudeHarness(t *testing.T, model *modtest.FakeClaude, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	base := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(assistant.Module()),
		modtest.WithEnv("AI_PROVIDER", "anthropic"),
		modtest.WithEnv("AI_API_KEY", "sk-ant-test-key"),
		modtest.WithEnv("AI_MODELS", "claude-sonnet-5-5"),
		modtest.WithEnv("OWNERS_REQUIRE_MFA", "0"),
		modtest.WithTransport(model),
	}
	return modtest.New(t, append(base, opts...)...)
}

func isSummary(req modtest.ClaudeRequest) bool {
	return strings.Contains(req.System, "memory of a conversation")
}

func TestAnthropic_TheSystemPromptAndTheToolsAreFrozenForTheConversation(t *testing.T) {
	t.Parallel()
	model := modtest.NewFakeClaude(func(_ int, req modtest.ClaudeRequest) modtest.ClaudeTurn {
		if len(req.Messages) == 1 {
			return modtest.ClaudeTurn{ToolCalls: []modtest.ChatToolCall{{Name: "search_docs", Args: map[string]any{"query": "invoice"}}}}
		}
		return modtest.ClaudeTurn{Text: "Done."}
	})
	h := newClaudeHarness(t, model)
	c, _ := person(t, h)
	conv := startConversation(t, c)

	send := func(text string, chip map[string]any) {
		t.Helper()
		body := map[string]any{"text": text}
		if chip != nil {
			body["context"] = chip
		}
		if r := c.Do(http.MethodPost, "/api/v1/assistant/conversations/"+conv.ID+"/messages", body); r.Status != http.StatusOK {
			t.Fatalf("send: %d %s", r.Status, r.Body)
		}
	}
	send("How do I find an invoice?", nil)
	send("And this one?", map[string]any{"app": "invoices", "route": "/invoices/42", "label": "Invoice 42"})

	reqs := model.Requests()
	if len(reqs) != 3 {
		t.Fatalf("requests = %d, want the tool round (2) and the second message (1)", len(reqs))
	}
	for i := 1; i < len(reqs); i++ {
		if reqs[i].System != reqs[0].System {
			t.Errorf("request %d: the system prompt changed", i)
		}
		if !bytes.Equal(reqs[i].Tools, reqs[0].Tools) {
			t.Errorf("request %d: the tools changed", i)
		}
	}
	if strings.Contains(reqs[0].System, "Invoice 42") || strings.Contains(reqs[0].System, "Today is") {
		t.Errorf("the frozen prompt holds what changes per message:\n%s", reqs[0].System)
	}
	last := string(reqs[2].Messages[len(reqs[2].Messages)-1])
	if !strings.Contains(last, "Invoice 42") || !strings.Contains(last, "Today is") || !strings.Contains(last, "And this one?") {
		t.Errorf("the second turn does not carry the date and the chip: %s", last)
	}
	if v := modtest.One[string](t, h, `SELECT prompt_version FROM assistant.conversation_state`); v == "" {
		t.Error("no prompt version was stored")
	}
	if n := modtest.One[int](t, h, `SELECT count(*) FROM assistant.conversation_state`); n != 1 {
		t.Errorf("conversation_state rows = %d, want 1", n)
	}
}

func TestAnthropic_EveryRequestExtendsThePreviousOneByteForByte(t *testing.T) {
	t.Parallel()
	model := modtest.NewFakeClaude(func(_ int, req modtest.ClaudeRequest) modtest.ClaudeTurn {
		// A tool round on every message: tool call first, then the answer.
		if strings.Contains(string(req.Messages[len(req.Messages)-1]), "tool_result") {
			return modtest.ClaudeTurn{Text: "Done."}
		}
		return modtest.ClaudeTurn{Text: "Looking.", ToolCalls: []modtest.ChatToolCall{
			{Name: "search_docs", Args: map[string]any{"query": "a"}},
			{Name: "search_docs", Args: map[string]any{"query": "b"}},
		}}
	})
	h := newClaudeHarness(t, model)
	c, _ := person(t, h)
	conv := startConversation(t, c)
	for _, text := range []string{"First", "Second", "Third"} {
		if r := ask(t, c, conv.ID, text); r.Status != http.StatusOK {
			t.Fatalf("send: %d %s", r.Status, r.Body)
		}
	}
	reqs := model.Requests()
	if len(reqs) != 6 {
		t.Fatalf("requests = %d, want 6", len(reqs))
	}
	for i := 1; i < len(reqs); i++ {
		prev, cur := reqs[i-1].Messages, reqs[i].Messages
		if len(cur) <= len(prev) {
			t.Fatalf("request %d has %d messages after %d", i, len(cur), len(prev))
		}
		for j := range prev {
			if !bytes.Equal(prev[j], cur[j]) {
				t.Errorf("request %d changed message %d:\n was %s\n now %s", i, j, prev[j], cur[j])
			}
		}
	}
	// The first turn's thinking block, signature and all, is replayed.
	if !strings.Contains(string(reqs[1].Messages[1]), `"signature":"sig-turn-0"`) {
		t.Errorf("the assistant turn was replayed as %s, want its thinking block", reqs[1].Messages[1])
	}
	// Both results of one turn came back in one user message.
	if got := strings.Count(string(reqs[1].Messages[2]), `"type":"tool_result"`); got != 2 {
		t.Errorf("results message = %s, want both tool results", reqs[1].Messages[2])
	}
	// The provider's record is stored with the turn and never shown.
	if n := modtest.One[int](t, h, `SELECT count(*) FROM assistant.messages WHERE role = 'assistant' AND provider IS NOT NULL`); n != 6 {
		t.Errorf("assistant turns with a provider record = %d, want 6", n)
	}
	d := c.Do(http.MethodGet, "/api/v1/assistant/conversations/"+conv.ID, nil)
	if strings.Contains(string(d.Body), "sig-turn") || strings.Contains(string(d.Body), "signature") {
		t.Errorf("the conversation exposes the provider's blocks: %s", d.Body)
	}
}

func TestAnthropic_TheLastTurnKeepsTheToolsAndForbidsThem(t *testing.T) {
	t.Parallel()
	model := modtest.NewFakeClaude(func(_ int, req modtest.ClaudeRequest) modtest.ClaudeTurn {
		if req.ToolChoice == "none" {
			return modtest.ClaudeTurn{Text: "Here is what I found."}
		}
		var calls []modtest.ChatToolCall
		for range 5 {
			calls = append(calls, modtest.ChatToolCall{Name: "search_docs", Args: map[string]any{"query": "invoice"}})
		}
		return modtest.ClaudeTurn{ToolCalls: calls}
	})
	h := newClaudeHarness(t, model)
	c, _ := person(t, h)
	conv := startConversation(t, c)
	if r := ask(t, c, conv.ID, "Everything about invoices"); r.Status != http.StatusOK {
		t.Fatalf("send: %d %s", r.Status, r.Body)
	}
	reqs := model.Requests()
	if len(reqs) != 4 {
		t.Fatalf("requests = %d, want 4", len(reqs))
	}
	if reqs[3].ToolChoice != "none" || !bytes.Equal(reqs[3].Tools, reqs[0].Tools) || len(reqs[3].Tools) < 10 {
		t.Errorf("the last turn: tool_choice = %q, tools equal = %v", reqs[3].ToolChoice, bytes.Equal(reqs[3].Tools, reqs[0].Tools))
	}
	if reqs[2].ToolChoice != "" {
		t.Errorf("an earlier turn forbade tools: %q", reqs[2].ToolChoice)
	}
}

func TestAnthropic_ALongConversationIsSummarisedAndNoStoredTurnIsEdited(t *testing.T) {
	t.Parallel()
	model := modtest.NewFakeClaude(func(_ int, req modtest.ClaudeRequest) modtest.ClaudeTurn {
		switch {
		case isSummary(req):
			return modtest.ClaudeTurn{Text: "The person asked about invoices and was told where to find them."}
		case strings.Contains(string(req.Messages[len(req.Messages)-1]), "tool_result"):
			return modtest.ClaudeTurn{Text: "Done."}
		case strings.Contains(string(req.Messages[len(req.Messages)-1]), "second question"):
			return modtest.ClaudeTurn{ToolCalls: []modtest.ChatToolCall{{Name: "search_docs", Args: map[string]any{"query": "x"}}}}
		}
		return modtest.ClaudeTurn{Text: "Answer."}
	})
	h := newClaudeHarness(t, model, modtest.WithEnv("AI_CONTEXT_TOKENS", "5000"))
	c, _ := person(t, h)
	conv := startConversation(t, c)

	if r := ask(t, c, conv.ID, "A short first question"); r.Status != http.StatusOK {
		t.Fatalf("send: %d %s", r.Status, r.Body)
	}
	if n := len(model.Requests()); n != 1 {
		t.Fatalf("a short conversation made %d requests, want 1: no summary", n)
	}
	before := modtest.One[string](t, h, `SELECT content::text FROM assistant.messages WHERE role = 'user'`)

	long := "second question " + strings.Repeat("with a long background. ", 300)
	if r := ask(t, c, conv.ID, long); r.Status != http.StatusOK {
		t.Fatalf("send: %d %s", r.Status, r.Body)
	}
	reqs := model.Requests()
	// The summary, then the turn (a tool round of two requests).
	if len(reqs) != 4 || !isSummary(reqs[1]) {
		t.Fatalf("requests = %d (summary at 1: %v), want one summary and a tool round after it", len(reqs), len(reqs) > 1 && isSummary(reqs[1]))
	}
	if len(reqs[1].Tools) != 0 && string(reqs[1].Tools) != "null" {
		t.Errorf("the summary request carries tools: %s", reqs[1].Tools)
	}
	chat := reqs[2]
	if len(chat.Messages) != 2 {
		t.Fatalf("the turn after the summary carries %d messages, want the summary and the new question", len(chat.Messages))
	}
	if !strings.Contains(string(chat.Messages[0]), "was told where to find them") || strings.Contains(string(chat.Messages[0]), "A short first question") {
		t.Errorf("the first message is not the summary: %s", chat.Messages[0])
	}
	if !strings.Contains(string(chat.Messages[1]), "second question") {
		t.Errorf("the second message is not the new question")
	}
	// Mid-tool-round the summary stays and nothing is summarised again.
	if got := len(reqs[3].Messages); got != 4 || bytes.Contains(reqs[3].Messages[0], []byte("A short first question")) {
		t.Errorf("the tool round carries %d messages", got)
	}
	if !bytes.Equal(reqs[3].Messages[0], chat.Messages[0]) || !bytes.Equal(reqs[3].Messages[1], chat.Messages[1]) {
		t.Error("the tool round changed the turns before it")
	}

	// The marker is stored; the turns are not touched.
	if through := modtest.One[int](t, h, `SELECT summary_through_seq FROM assistant.conversation_state`); through <= 0 {
		t.Errorf("summary_through_seq = %d", through)
	}
	if after := modtest.One[string](t, h, `SELECT content::text FROM assistant.messages WHERE role = 'user' ORDER BY seq LIMIT 1`); after != before {
		t.Errorf("a stored turn was edited:\n%s\n%s", before, after)
	}
	if n := modtest.One[int](t, h, `SELECT count(*) FROM assistant.messages`); n != 6 {
		t.Errorf("messages = %d, want all 6 kept (2 questions, 3 answers, a result)", n)
	}
}

func TestAnthropic_ARefusalEndsTheRunWithItsOwnCode(t *testing.T) {
	t.Parallel()
	model := modtest.NewFakeClaude(func(int, modtest.ClaudeRequest) modtest.ClaudeTurn { return modtest.ClaudeTurn{Refusal: "cyber"} })
	h := newClaudeHarness(t, model)
	c, _ := person(t, h)
	conv := startConversation(t, c)
	r := ask(t, c, conv.ID, "Hello?")
	events := parseStream(r.Body)
	last := events[len(events)-1]
	if last.Name != "error" || !strings.Contains(string(last.Data), "ai_refused") {
		t.Errorf("last event = %s %s, want error ai_refused", last.Name, last.Data)
	}
	var code string
	_ = json.Unmarshal([]byte(modtest.One[string](t, h, `SELECT to_json(error_code)::text FROM assistant.runs`)), &code)
	if code != "ai_refused" {
		t.Errorf("error_code = %q", code)
	}
}

func TestOpenAI_NoSummaryIsMadeAndOlderResultsAreShortenedInPlace(t *testing.T) {
	t.Parallel()
	model := modtest.NewFakeChatModel()
	h := newHarness(t, model, modtest.WithEnv("AI_CONTEXT_TOKENS", "2000"))
	c, _ := person(t, h)
	conv := startConversation(t, c)
	for _, text := range []string{"one", "two " + strings.Repeat("x", 7000), "three"} {
		if r := ask(t, c, conv.ID, text); r.Status != http.StatusOK {
			t.Fatalf("send: %d %s", r.Status, r.Body)
		}
	}
	if n := modtest.One[int](t, h, `SELECT count(*) FROM assistant.conversation_state WHERE summary IS NOT NULL`); n != 0 {
		t.Errorf("a summary was made for the OpenAI-compatible provider")
	}
	if n := len(model.Requests()); n != 3 {
		t.Errorf("requests = %d, want 3: no summarising request", n)
	}
}
