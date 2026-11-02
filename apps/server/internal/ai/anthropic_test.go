package ai_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/ai"
	"github.com/vantigo-io/vantigo/server/internal/config"
)

// Anthropic is tested as the other providers are: the real client, and through
// it the real SDK, over a fake transport that serves canned Messages streams.

func anthropicConfig(models ...string) *config.Config {
	if len(models) == 0 {
		models = []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"}
	}
	return &config.Config{AIProvider: "anthropic", AIAPIKey: "sk-ant-test-key", AIModels: models, AIEffort: "medium", AIFallbacks: true}
}

// sse renders (event, data) pairs as a Messages stream.
func sse(pairs ...string) string {
	var sb strings.Builder
	for i := 0; i+1 < len(pairs); i += 2 {
		sb.WriteString("event: " + pairs[i] + "\ndata: " + pairs[i+1] + "\n\n")
	}
	return sb.String()
}

func start(usage string) []string {
	return []string{"message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":` + usage + `}}`}
}

func block(i int, start, deltas string, deltaType string) []string {
	out := []string{"content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":%s}`, i, start)}
	for _, d := range strings.Split(deltas, "\x00") {
		if d == "" {
			continue
		}
		out = append(out, "content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":%s}`, i, d))
	}
	return append(out, "content_block_stop", fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, i))
}

func finish(stop, usage string, extra string) []string {
	return []string{
		"message_delta", `{"type":"message_delta","delta":{"stop_reason":"` + stop + `","stop_sequence":null` + extra + `},"usage":` + usage + `}`,
		"message_stop", `{"type":"message_stop"}`,
	}
}

func join(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func textDelta(s string) string {
	b, _ := json.Marshal(s)
	return `{"type":"text_delta","text":` + string(b) + `}`
}

func jsonDelta(s string) string {
	b, _ := json.Marshal(s)
	return `{"type":"input_json_delta","partial_json":` + string(b) + `}`
}

const openingUsage = `{"input_tokens":100,"output_tokens":1,"cache_creation_input_tokens":20,"cache_read_input_tokens":300}`

var searchTools = []ai.Tool{
	{Name: "search_docs", Description: "Search the documentation.", Parameters: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`)},
	{Name: "no_arguments"},
}

func newAnthropicClient(t *testing.T, cfg *config.Config, tr http.RoundTripper, logger *slog.Logger) *ai.Client {
	t.Helper()
	opts := []ai.Option{}
	if logger != nil {
		opts = append(opts, ai.WithLogger(logger))
	}
	c := ai.New(cfg, tr, choose{"assistant.chat": "claude-opus-5-5", "haiku": "claude-haiku-5-5", "sonnet": "claude-sonnet-5-5", "other": "gpt-4o"}, opts...)
	if c == nil {
		t.Fatal("New returned nil for an Anthropic configuration")
	}
	return c
}

func TestAnthropic_ChatStreamsTextAndSendsTheStablePrefixAndTheControls(t *testing.T) {
	tr := &sseTransport{respond: sseOK(sse(join(
		start(openingUsage),
		block(0, `{"type":"thinking","thinking":"","signature":""}`, `{"type":"signature_delta","signature":"sig-abc"}`, ""),
		block(1, `{"type":"text","text":""}`, textDelta("Open ")+"\x00"+textDelta("Customers."), ""),
		finish("end_turn", `{"output_tokens":9}`, ""),
	)...))}
	c := newAnthropicClient(t, anthropicConfig(), tr, nil)

	var deltas []string
	req := chatRequest()
	req.MaxTokens = 500
	req.Tools = searchTools
	got, err := c.Chat(context.Background(), "assistant.chat", req, func(s string) { deltas = append(deltas, s) })
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if strings.Join(deltas, "|") != "Open |Customers." {
		t.Errorf("deltas = %q", deltas)
	}
	if got.Message.Text != "Open Customers." || len(got.Message.ToolCalls) != 0 || got.FinishReason != "stop" || got.Provider != "anthropic" || got.Model != "claude-opus-5-5" {
		t.Errorf("response = %+v", got)
	}
	// The prompt is its uncached part plus both cache counts.
	if got.InputTokens == nil || *got.InputTokens != 420 || got.OutputTokens == nil || *got.OutputTokens != 9 {
		t.Errorf("tokens = %v/%v, want 420/9", got.InputTokens, got.OutputTokens)
	}
	if got.CacheReadTokens == nil || *got.CacheReadTokens != 300 || got.CacheWriteTokens == nil || *got.CacheWriteTokens != 20 {
		t.Errorf("cache = %v/%v, want 300 read and 20 written", got.CacheReadTokens, got.CacheWriteTokens)
	}
	var blocks []map[string]any
	if err := json.Unmarshal(got.Message.Provider, &blocks); err != nil || len(blocks) != 2 || blocks[0]["type"] != "thinking" || blocks[0]["signature"] != "sig-abc" {
		t.Errorf("provider blocks = %s, want the thinking block with its signature then the text", got.Message.Provider)
	}

	body, header, url := tr.last(t)
	if url != "https://api.anthropic.com/v1/messages?beta=true" {
		t.Errorf("URL = %q", url)
	}
	if header.Get("X-Api-Key") != "sk-ant-test-key" {
		t.Errorf("api key header = %q", header.Get("X-Api-Key"))
	}
	betas := header.Get("Anthropic-Beta")
	if !strings.Contains(betas, "server-side-fallback-2026-07-01") || !strings.Contains(betas, "thinking-binding-controls-2026-08-01") {
		t.Errorf("anthropic-beta = %q, want the fallback and the binding betas", betas)
	}
	if body["model"] != "claude-opus-5-5" || body["stream"] != true || body["max_tokens"] != float64(12500) {
		t.Errorf("model/stream/max_tokens = %v/%v/%v", body["model"], body["stream"], body["max_tokens"])
	}
	for _, forbidden := range []string{"temperature", "top_p", "top_k", "budget_tokens"} {
		if _, sent := body[forbidden]; sent {
			t.Errorf("%s was sent: current models refuse it", forbidden)
		}
	}
	thinking, _ := body["thinking"].(map[string]any)
	binding, _ := thinking["block_binding"].(map[string]any)
	if thinking["type"] != "adaptive" || binding["prefix_mismatch_behavior"] != "drop_block" || thinking["budget_tokens"] != nil {
		t.Errorf("thinking = %v, want adaptive with drop_block", thinking)
	}
	if oc, _ := body["output_config"].(map[string]any); oc["effort"] != "medium" {
		t.Errorf("output_config = %v", body["output_config"])
	}
	if body["fallbacks"] != "default" {
		t.Errorf("fallbacks = %v, want default", body["fallbacks"])
	}
	if _, forced := body["tool_choice"]; forced {
		t.Errorf("tool_choice = %v, want none sent", body["tool_choice"])
	}
	system, _ := body["system"].([]any)
	if len(system) != 1 {
		t.Fatalf("system = %v", body["system"])
	}
	sys := system[0].(map[string]any)
	if sys["text"] != "You are the assistant." || sys["cache_control"].(map[string]any)["type"] != "ephemeral" {
		t.Errorf("system block = %v, want the text marked for caching", sys)
	}
	tools, _ := body["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tools = %v", tools)
	}
	t0 := tools[0].(map[string]any)
	schema := t0["input_schema"].(map[string]any)
	if t0["name"] != "search_docs" || t0["eager_input_streaming"] != true || schema["type"] != "object" || schema["additionalProperties"] != false || schema["required"].([]any)[0] != "query" {
		t.Errorf("tool 0 = %v", t0)
	}
	if s1 := tools[1].(map[string]any)["input_schema"].(map[string]any); s1["type"] != "object" {
		t.Errorf("tool 1 schema = %v, want an empty object schema", s1)
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 1 || msgs[0].(map[string]any)["role"] != "user" {
		t.Errorf("messages = %v", msgs)
	}
}

func TestAnthropic_ToolInputArrivesInFragmentsAndIsValidated(t *testing.T) {
	tr := &sseTransport{respond: sseOK(sse(join(
		start(`{"input_tokens":10,"output_tokens":1}`),
		block(0, `{"type":"text","text":""}`, textDelta("Looking."), ""),
		block(1, `{"type":"tool_use","id":"toolu_1","name":"search_docs","input":{}}`, jsonDelta(`{"que`)+"\x00"+jsonDelta(`ry":"cust`)+"\x00"+jsonDelta(`omer"}`), ""),
		block(2, `{"type":"tool_use","id":"toolu_2","name":"search_docs","input":{}}`, jsonDelta(`{"query":7}`), ""),
		block(3, `{"type":"tool_use","id":"toolu_3","name":"no_arguments","input":{}}`, "", ""),
		finish("tool_use", `{"output_tokens":30}`, ""),
	)...))}
	c := newAnthropicClient(t, anthropicConfig(), tr, nil)
	req := chatRequest()
	req.Tools = searchTools
	got, err := c.Chat(context.Background(), "assistant.chat", req, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	calls := got.Message.ToolCalls
	if len(calls) != 3 || got.FinishReason != "tool_calls" {
		t.Fatalf("calls = %+v, finish = %q, want three parallel calls", calls, got.FinishReason)
	}
	if calls[0].ID != "toolu_1" || calls[0].Name != "search_docs" || string(calls[0].Arguments) != `{"query":"customer"}` {
		t.Errorf("call 0 = %+v, want the fragments joined", calls[0])
	}
	// An input that is not valid for the schema is carried as a string, so the
	// caller can see it is not an object.
	if calls[1].ID != "toolu_2" || !strings.HasPrefix(string(calls[1].Arguments), `"`) {
		t.Errorf("call 1 = %+v, want an invalid input carried as a JSON string", calls[1])
	}
	if calls[2].ID != "toolu_3" || string(calls[2].Arguments) != `{}` {
		t.Errorf("call 2 = %+v, want an empty object", calls[2])
	}
	if got.Message.Text != "Looking." {
		t.Errorf("text = %q", got.Message.Text)
	}
}

// A two-turn tool conversation replays the first turn's content blocks, the
// thinking block included, byte for byte, and answers its parallel calls in
// one user message.
func TestAnthropic_ReplaysTheProviderBlocksVerbatimAndGroupsToolResults(t *testing.T) {
	first := sse(join(
		start(`{"input_tokens":10,"output_tokens":1}`),
		block(0, `{"type":"thinking","thinking":"","signature":"EqQBCkYI-signature=="}`, "", ""),
		block(1, `{"type":"tool_use","id":"toolu_a","name":"search_docs","input":{}}`, jsonDelta(`{"query":"a"}`), ""),
		block(2, `{"type":"tool_use","id":"toolu_b","name":"search_docs","input":{}}`, jsonDelta(`{"query":"b"}`), ""),
		finish("tool_use", `{"output_tokens":5}`, ""),
	)...)
	second := sse(join(
		start(`{"input_tokens":10,"output_tokens":1}`),
		block(0, `{"type":"text","text":""}`, textDelta("Done."), ""),
		finish("end_turn", `{"output_tokens":2}`, ""),
	)...)
	answers := []string{first, second}
	tr := &sseTransport{}
	tr.respond = func(r *http.Request) (*http.Response, error) {
		body := answers[0]
		if len(answers) > 1 {
			answers = answers[1:]
		}
		return sseOK(body)(r)
	}
	c := newAnthropicClient(t, anthropicConfig(), tr, nil)

	req := chatRequest()
	req.Tools = searchTools
	one, err := c.Chat(context.Background(), "assistant.chat", req, nil)
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	req.Messages = append(req.Messages, one.Message,
		ai.Message{Role: ai.RoleTool, ToolCallID: "toolu_a", Text: "result a"},
		ai.Message{Role: ai.RoleTool, ToolCallID: "toolu_b", Text: "result b"},
	)
	if _, err := c.Chat(context.Background(), "assistant.chat", req, nil); err != nil {
		t.Fatalf("second turn: %v", err)
	}

	body, _, _ := tr.last(t)
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages = %v, want the question, the assistant turn and ONE user message of results", msgs)
	}
	replayed, _ := json.Marshal(msgs[1].(map[string]any)["content"])
	var want []json.RawMessage
	if err := json.Unmarshal(one.Message.Provider, &want); err != nil {
		t.Fatal(err)
	}
	var got []json.RawMessage
	if err := json.Unmarshal(replayed, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) || len(got) != 3 {
		t.Fatalf("replayed %d blocks, want %d", len(got), len(want))
	}
	for i := range want {
		var a, b any
		_ = json.Unmarshal(want[i], &a)
		_ = json.Unmarshal(got[i], &b)
		if fmt.Sprint(a) != fmt.Sprint(b) {
			t.Errorf("block %d changed on replay:\n got %s\nwant %s", i, got[i], want[i])
		}
	}
	if !bytes.Contains(replayed, []byte(`"signature":"EqQBCkYI-signature=="`)) {
		t.Errorf("replayed = %s, want the signature intact", replayed)
	}
	results := msgs[2].(map[string]any)
	content, _ := results["content"].([]any)
	if results["role"] != "user" || len(content) != 2 || content[0].(map[string]any)["type"] != "tool_result" || content[1].(map[string]any)["tool_use_id"] != "toolu_b" {
		t.Errorf("results message = %v, want both tool results in one user message", results)
	}
}

func TestAnthropic_ATurnWithoutProviderBlocksIsRebuiltFromTextAndCalls(t *testing.T) {
	tr := &sseTransport{respond: sseOK(sse(join(
		start(`{"input_tokens":1,"output_tokens":1}`),
		block(0, `{"type":"text","text":""}`, textDelta("ok"), ""),
		finish("end_turn", `{"output_tokens":1}`, ""),
	)...))}
	c := newAnthropicClient(t, anthropicConfig(), tr, nil)
	req := chatRequest()
	req.Tools = searchTools
	req.Messages = append(req.Messages,
		ai.Message{Role: ai.RoleAssistant, Text: "Checking.", ToolCalls: []ai.ToolCall{{ID: "call_1", Name: "search_docs", Arguments: json.RawMessage(`{"query":"x"}`)}}},
		ai.Message{Role: ai.RoleTool, ToolCallID: "call_1", Text: "r"},
	)
	if _, err := c.Chat(context.Background(), "assistant.chat", req, nil); err != nil {
		t.Fatal(err)
	}
	body, _, _ := tr.last(t)
	msgs := body["messages"].([]any)
	blocks := msgs[1].(map[string]any)["content"].([]any)
	if len(blocks) != 2 || blocks[0].(map[string]any)["type"] != "text" || blocks[1].(map[string]any)["type"] != "tool_use" || blocks[1].(map[string]any)["id"] != "call_1" {
		t.Errorf("rebuilt turn = %v", blocks)
	}
}

func TestAnthropic_ToolsDisabledKeepsTheToolsAndForbidsTheirUse(t *testing.T) {
	tr := &sseTransport{respond: sseOK(sse(join(
		start(`{"input_tokens":1,"output_tokens":1}`),
		block(0, `{"type":"text","text":""}`, textDelta("ok"), ""),
		finish("end_turn", `{"output_tokens":1}`, ""),
	)...))}
	c := newAnthropicClient(t, anthropicConfig(), tr, nil)
	req := chatRequest()
	req.Tools = searchTools
	req.ToolsDisabled = true
	if _, err := c.Chat(context.Background(), "assistant.chat", req, nil); err != nil {
		t.Fatal(err)
	}
	body, _, _ := tr.last(t)
	if tc, _ := body["tool_choice"].(map[string]any); tc["type"] != "none" || len(body["tools"].([]any)) != 2 {
		t.Errorf("tool_choice = %v, tools = %v, want none with both tools kept", body["tool_choice"], body["tools"])
	}
}

func TestAnthropic_FallbacksFollowTheSettingAndTheModel(t *testing.T) {
	stream := sse(join(
		start(`{"input_tokens":1,"output_tokens":1}`),
		block(0, `{"type":"text","text":""}`, textDelta("ok"), ""),
		finish("end_turn", `{"output_tokens":1}`, ""),
	)...)
	for _, tc := range []struct {
		name      string
		task      string
		fallbacks bool
		want      bool
	}{
		{"opus, on", "assistant.chat", true, true},
		{"sonnet, on", "sonnet", true, true},
		{"haiku has none", "haiku", true, false},
		{"opus, off", "assistant.chat", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := &sseTransport{respond: sseOK(stream)}
			cfg := anthropicConfig()
			cfg.AIFallbacks = tc.fallbacks
			c := newAnthropicClient(t, cfg, tr, nil)
			if _, err := c.Chat(context.Background(), tc.task, chatRequest(), nil); err != nil {
				t.Fatal(err)
			}
			body, header, _ := tr.last(t)
			if (body["fallbacks"] == "default") != tc.want || strings.Contains(header.Get("Anthropic-Beta"), "server-side-fallback") != tc.want {
				t.Errorf("fallbacks = %v, beta = %q, want sent = %v", body["fallbacks"], header.Get("Anthropic-Beta"), tc.want)
			}
			if !strings.Contains(header.Get("Anthropic-Beta"), "thinking-binding-controls-2026-08-01") {
				t.Errorf("anthropic-beta = %q, want the binding beta always", header.Get("Anthropic-Beta"))
			}
		})
	}
}

func TestAnthropic_EffortIsTheConfiguredOne(t *testing.T) {
	tr := &sseTransport{respond: sseOK(sse(join(
		start(`{"input_tokens":1,"output_tokens":1}`),
		block(0, `{"type":"text","text":""}`, textDelta("ok"), ""),
		finish("end_turn", `{"output_tokens":1}`, ""),
	)...))}
	cfg := anthropicConfig()
	cfg.AIEffort = "xhigh"
	c := newAnthropicClient(t, cfg, tr, nil)
	if _, err := c.Chat(context.Background(), "assistant.chat", chatRequest(), nil); err != nil {
		t.Fatal(err)
	}
	body, _, _ := tr.last(t)
	if oc, _ := body["output_config"].(map[string]any); oc["effort"] != "xhigh" {
		t.Errorf("output_config = %v", body["output_config"])
	}
}

func TestAnthropic_InputTransformationsAreLoggedWithoutContent(t *testing.T) {
	stream := sse(join(
		[]string{"message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1},"input_transformations":[{"type":"thinking_dropped","path":"messages.1.content.0","reason":"prefix_binding_mismatch"}]}}`},
		block(0, `{"type":"text","text":""}`, textDelta("SECRET-ANSWER"), ""),
		finish("end_turn", `{"output_tokens":1}`, ""),
	)...)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c := newAnthropicClient(t, anthropicConfig(), &sseTransport{respond: sseOK(stream)}, logger)
	if _, err := c.Chat(context.Background(), "assistant.chat", chatRequest(), nil); err != nil {
		t.Fatal(err)
	}
	out := logs.String()
	for _, want := range []string{"level=WARN", "thinking_dropped", "prefix_binding_mismatch", "messages.1.content.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("log = %q, want it to contain %q", out, want)
		}
	}
	if strings.Contains(out, "SECRET-ANSWER") {
		t.Errorf("log = %q, must hold no content", out)
	}
}

func TestAnthropic_RefusalAndMaxTokensAreTypedErrors(t *testing.T) {
	refusal := sse(join(
		start(`{"input_tokens":1,"output_tokens":1}`),
		finish("refusal", `{"output_tokens":0}`, `,"stop_details":{"type":"refusal","category":"cyber","explanation":"no"}`),
	)...)
	c := newAnthropicClient(t, anthropicConfig(), &sseTransport{respond: sseOK(refusal)}, nil)
	_, err := c.Chat(context.Background(), "assistant.chat", chatRequest(), nil)
	if cat, ok := ai.RefusalCategory(err); !ok || cat != "cyber" {
		t.Errorf("err = %v, want a refusal with category cyber", err)
	}
	if got := fmt.Sprintf("%T", err); got != "ai.aiRefusalError" {
		t.Errorf("label = %s", got)
	}

	truncated := sse(join(
		start(`{"input_tokens":1,"output_tokens":1}`),
		block(0, `{"type":"tool_use","id":"toolu_1","name":"search_docs","input":{}}`, jsonDelta(`{"query":"cu`), ""),
		finish("max_tokens", `{"output_tokens":99}`, ""),
	)...)
	c = newAnthropicClient(t, anthropicConfig(), &sseTransport{respond: sseOK(truncated)}, nil)
	req := chatRequest()
	req.Tools = searchTools
	if _, err := c.Chat(context.Background(), "assistant.chat", req, nil); !ai.IsMaxTokens(err) {
		t.Errorf("err = %v, want max tokens: a cut-off tool call is never returned", err)
	}
}

func TestAnthropic_StatusErrors(t *testing.T) {
	for _, status := range []int{429, 529, 400, 401} {
		tr := &sseTransport{respond: func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"overloaded_error","message":"secret detail"}}`))}, nil
		}}
		c := newAnthropicClient(t, anthropicConfig(), tr, nil)
		_, err := c.Chat(context.Background(), "assistant.chat", chatRequest(), nil)
		if err == nil || fmt.Sprintf("%T", err) != "ai.aiStatusError" || !strings.Contains(err.Error(), fmt.Sprint(status)) || strings.Contains(err.Error(), "secret") {
			t.Errorf("status %d: err = %T %v, want aiStatusError naming the status only", status, err, err)
		}
		if tr.count() != 1 {
			t.Errorf("status %d: %d requests, want exactly one: the SDK's retries are off", status, tr.count())
		}
	}
}

func TestAnthropic_AStreamCutBeforeMessageStopIsADecodeError(t *testing.T) {
	cut := sse(join(
		start(`{"input_tokens":1,"output_tokens":1}`),
		[]string{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		[]string{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":` + textDelta("half") + `}`},
	)...)
	c := newAnthropicClient(t, anthropicConfig(), &sseTransport{respond: sseOK(cut)}, nil)
	var deltas []string
	_, err := c.Chat(context.Background(), "assistant.chat", chatRequest(), func(s string) { deltas = append(deltas, s) })
	if fmt.Sprintf("%T", err) != "ai.aiDecodeError" {
		t.Errorf("err = %T %v, want aiDecodeError", err, err)
	}
}

func TestAnthropic_ATransportFailureIsATransportError(t *testing.T) {
	tr := &sseTransport{respond: func(*http.Request) (*http.Response, error) { return nil, errors.New("connection refused") }}
	c := newAnthropicClient(t, anthropicConfig(), tr, nil)
	_, err := c.Chat(context.Background(), "assistant.chat", chatRequest(), nil)
	if fmt.Sprintf("%T", err) != "ai.aiTransportError" || strings.Contains(err.Error(), "refused") {
		t.Errorf("err = %T %v", err, err)
	}
	if tr.count() != 1 {
		t.Errorf("%d requests, want one", tr.count())
	}
}

func TestAnthropic_AModelOutsideTheListIsRefusedBeforeAnyRequest(t *testing.T) {
	tr := &sseTransport{respond: sseOK("")}
	c := newAnthropicClient(t, anthropicConfig(), tr, nil)
	if _, err := c.Chat(context.Background(), "other", chatRequest(), nil); err == nil || tr.count() != 0 {
		t.Errorf("err = %v, requests = %d, want a refusal and no request", err, tr.count())
	}
	if _, err := c.Complete(context.Background(), "other", ai.Request{Prompt: "x"}); err == nil || tr.count() != 0 {
		t.Errorf("Complete: err = %v, requests = %d", err, tr.count())
	}
}

func TestAnthropic_CompleteIsOneNonStreamingUserMessage(t *testing.T) {
	tr := &sseTransport{respond: func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(
			`{"id":"msg_1","type":"message","role":"assistant","model":"claude-haiku-5-5","content":[{"type":"thinking","thinking":"","signature":"s"},{"type":"text","text":"Dear customer,"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":12,"output_tokens":7}}`))}, nil
	}}
	c := newAnthropicClient(t, anthropicConfig(), tr, nil)
	got, err := c.Complete(context.Background(), "haiku", ai.Request{Prompt: "Draft a reply"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "Dear customer," || got.Provider != "anthropic" || got.Model != "claude-haiku-5-5" || *got.InputTokens != 12 || *got.OutputTokens != 7 {
		t.Errorf("response = %+v", got)
	}
	body, header, _ := tr.last(t)
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 1 || body["stream"] != nil || body["system"] != nil || body["tools"] != nil || body["thinking"] != nil {
		t.Errorf("body = %v, want one user message and nothing else but the controls", body)
	}
	if strings.Contains(header.Get("Anthropic-Beta"), "server-side-fallback") {
		t.Errorf("Haiku was sent fallbacks: %q", header.Get("Anthropic-Beta"))
	}
}

func TestAnthropic_CompleteErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   string
	}{
		"status":  {529, `{"type":"error","error":{"type":"overloaded_error","message":"x"}}`, "ai.aiStatusError"},
		"empty":   {200, `{"id":"m","type":"message","role":"assistant","model":"m","content":[],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`, "ai.aiEmptyResponseError"},
		"refusal": {200, `{"id":"m","type":"message","role":"assistant","model":"m","content":[],"stop_reason":"refusal","stop_sequence":null,"stop_details":{"type":"refusal","category":"bio"},"usage":{"input_tokens":1,"output_tokens":0}}`, "ai.aiRefusalError"},
	} {
		t.Run(name, func(t *testing.T) {
			tr := &sseTransport{respond: func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			}}
			c := newAnthropicClient(t, anthropicConfig(), tr, nil)
			_, err := c.Complete(context.Background(), "haiku", ai.Request{Prompt: "x"})
			if fmt.Sprintf("%T", err) != tc.want {
				t.Errorf("err = %T %v, want %s", err, err, tc.want)
			}
		})
	}
}

func TestAnthropic_NeedsAKeyEvenWithABaseURL(t *testing.T) {
	cfg := anthropicConfig()
	cfg.AIAPIKey, cfg.AIBaseURL = "", "https://gateway.example.com"
	if ai.New(cfg, nil, nil) != nil {
		t.Error("New built an Anthropic client without a key")
	}
}

func TestAnthropic_ABaseURLIsUsedAndRedirectsAreNotFollowed(t *testing.T) {
	cfg := anthropicConfig()
	cfg.AIBaseURL = "https://gateway.example.com/anthropic"
	tr := &sseTransport{respond: func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://evil.example.com/"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	}}
	c := newAnthropicClient(t, cfg, tr, nil)
	_, err := c.Chat(context.Background(), "assistant.chat", chatRequest(), nil)
	if err == nil || tr.count() != 1 {
		t.Errorf("err = %v, requests = %d, want an error and no second request", err, tr.count())
	}
	_, _, url := tr.last(t)
	if !strings.HasPrefix(url, "https://gateway.example.com/anthropic/v1/messages") {
		t.Errorf("URL = %q", url)
	}
}
