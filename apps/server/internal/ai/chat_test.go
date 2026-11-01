package ai_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/ai"
	"github.com/vantigo-io/vantigo/server/internal/config"
)

// Chat is tested as Complete is: the real client over a fake transport that
// answers canned server-sent bodies in the shapes the real servers send.

// sseTransport records each request's raw body and headers and answers the
// canned response.
type sseTransport struct {
	mu      sync.Mutex
	bodies  []map[string]any
	headers []http.Header
	urls    []string
	respond func(*http.Request) (*http.Response, error)
}

func (s *sseTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	s.mu.Lock()
	s.bodies = append(s.bodies, body)
	s.headers = append(s.headers, r.Header.Clone())
	s.urls = append(s.urls, r.URL.String())
	s.mu.Unlock()
	return s.respond(r)
}

func (s *sseTransport) last(t *testing.T) (map[string]any, http.Header, string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.bodies) == 0 {
		t.Fatal("no request was made")
	}
	i := len(s.bodies) - 1
	return s.bodies[i], s.headers[i], s.urls[i]
}

func (s *sseTransport) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.bodies)
}

// events renders data payloads as a server-sent stream.
func events(data ...string) string {
	var sb strings.Builder
	for _, d := range data {
		sb.WriteString("data: " + d + "\n\n")
	}
	return sb.String()
}

func sseOK(body string) func(*http.Request) (*http.Response, error) {
	return func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": {"text/event-stream"}}}, nil
	}
}

const openAIStream = `{"id":"c1","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`

func chatRequest() ai.ChatRequest {
	return ai.ChatRequest{
		System:   "You are the assistant.",
		Messages: []ai.Message{{Role: ai.RoleUser, Text: "How do I add a customer?"}},
	}
}

func TestChat_StreamsTextDeltasAndAnswersTheAssembledMessageWithTokens(t *testing.T) {
	tr := &sseTransport{respond: sseOK(events(
		openAIStream,
		`{"choices":[{"index":0,"delta":{"content":"Open "},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"content":"Customers."},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":42,"completion_tokens":5,"total_tokens":47}}`,
		`[DONE]`,
	))}
	c := ai.New(configured(), tr, choose{"assistant.chat": "gpt-4o"})

	var deltas []string
	req := chatRequest()
	req.MaxTokens = 500
	req.Tools = []ai.Tool{{
		Name:        "search_docs",
		Description: "Search the documentation.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`),
	}, {Name: "no_arguments"}}
	got, err := c.Chat(context.Background(), "assistant.chat", req, func(text string) { deltas = append(deltas, text) })
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	if strings.Join(deltas, "|") != "Open |Customers." {
		t.Errorf("deltas = %q, want the two content deltas in order and no empty one", deltas)
	}
	if got.Message.Role != ai.RoleAssistant || got.Message.Text != "Open Customers." || len(got.Message.ToolCalls) != 0 {
		t.Errorf("message = %+v, want the assembled assistant text", got.Message)
	}
	if got.FinishReason != "stop" || got.Provider != "openai" || got.Model != "gpt-4o" {
		t.Errorf("response = %+v, want finish stop from openai/gpt-4o", got)
	}
	if got.InputTokens == nil || *got.InputTokens != 42 || got.OutputTokens == nil || *got.OutputTokens != 5 {
		t.Errorf("tokens = %v/%v, want 42/5 from the usage chunk", got.InputTokens, got.OutputTokens)
	}

	body, header, url := tr.last(t)
	if url != "https://api.openai.com/v1/chat/completions" || header.Get("Authorization") != "Bearer sk-test-key" {
		t.Errorf("request = %s with %q", url, header.Get("Authorization"))
	}
	if body["model"] != "gpt-4o" || body["stream"] != true {
		t.Errorf("model/stream = %v/%v", body["model"], body["stream"])
	}
	if opts, _ := body["stream_options"].(map[string]any); opts["include_usage"] != true {
		t.Errorf("stream_options = %v, want include_usage", body["stream_options"])
	}
	if body["max_completion_tokens"] != float64(500) || body["max_tokens"] != nil {
		t.Errorf("token limit = %v / %v, want max_completion_tokens on OpenAI's own endpoint", body["max_completion_tokens"], body["max_tokens"])
	}
	messages, _ := body["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("messages = %v, want the system message then the user's", messages)
	}
	if first, _ := messages[0].(map[string]any); first["role"] != "system" || first["content"] != "You are the assistant." {
		t.Errorf("first message = %v, want the system message", first)
	}
	if second, _ := messages[1].(map[string]any); second["role"] != "user" || second["content"] != "How do I add a customer?" {
		t.Errorf("second message = %v, want the user's", second)
	}
	tools, _ := body["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tools = %v, want two", tools)
	}
	fn, _ := tools[0].(map[string]any)["function"].(map[string]any)
	if tools[0].(map[string]any)["type"] != "function" || fn["name"] != "search_docs" || fn["description"] != "Search the documentation." {
		t.Errorf("tool 0 = %v", tools[0])
	}
	if params, _ := fn["parameters"].(map[string]any); params["type"] != "object" {
		t.Errorf("tool 0 parameters = %v, want the schema as sent", fn["parameters"])
	}
	fn1, _ := tools[1].(map[string]any)["function"].(map[string]any)
	if params, _ := fn1["parameters"].(map[string]any); params["type"] != "object" {
		t.Errorf("tool 1 parameters = %v, want an empty object schema for a function that takes none", fn1["parameters"])
	}
}

func TestChat_OllamaShape_NoUsageNoKeyAWholeToolCallInOneChunk(t *testing.T) {
	tr := &sseTransport{respond: sseOK(events(
		`{"id":"chatcmpl-1","model":"qwen3:14b","choices":[{"index":0,"delta":{"role":"assistant","content":"","tool_calls":[{"id":"call_a1","index":0,"type":"function","function":{"name":"search_operations","arguments":"{\"query\":\"customers\"}"}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":"stop"}]}`,
		`[DONE]`,
	))}
	cfg := &config.Config{AIProvider: "openai-compatible", AIBaseURL: "http://ollama:11434/v1", AIModels: []string{"qwen3:14b"}}
	c := ai.New(cfg, tr, nil)
	if c == nil {
		t.Fatal("New returned nil: a base URL makes the key optional")
	}
	if c.Provider() != "openai-compatible" {
		t.Errorf("Provider = %q, want the name the operator set", c.Provider())
	}

	req := chatRequest()
	req.MaxTokens = 300
	got, err := c.Chat(context.Background(), "assistant.chat", req, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(got.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v, want one", got.Message.ToolCalls)
	}
	call := got.Message.ToolCalls[0]
	if call.ID != "call_a1" || call.Name != "search_operations" || string(call.Arguments) != `{"query":"customers"}` {
		t.Errorf("tool call = %+v", call)
	}
	if got.InputTokens != nil || got.OutputTokens != nil {
		t.Errorf("tokens = %v/%v, want nil: the server reported no usage", got.InputTokens, got.OutputTokens)
	}
	if got.FinishReason != "stop" || got.Provider != "openai-compatible" {
		t.Errorf("response = %+v", got)
	}

	body, header, url := tr.last(t)
	if url != "http://ollama:11434/v1/chat/completions" {
		t.Errorf("URL = %q", url)
	}
	if _, sent := header["Authorization"]; sent {
		t.Errorf("Authorization = %q, want no header for a keyless endpoint", header.Get("Authorization"))
	}
	if body["max_tokens"] != float64(300) || body["max_completion_tokens"] != nil {
		t.Errorf("token limit = %v / %v, want max_tokens for a compatible server", body["max_tokens"], body["max_completion_tokens"])
	}
}

func TestChat_AToolCallSplitOverChunks(t *testing.T) {
	tr := &sseTransport{respond: sseOK(events(
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":null,"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"describe_operation","arguments":""}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"id\":"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"getCust"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"omers\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":20}}`,
		`[DONE]`,
	))}
	got, err := ai.New(configured(), tr, nil).Chat(context.Background(), "t", chatRequest(), nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(got.Message.ToolCalls) != 1 || got.Message.Text != "" {
		t.Fatalf("message = %+v, want one tool call and no text", got.Message)
	}
	call := got.Message.ToolCalls[0]
	if call.ID != "call_1" || call.Name != "describe_operation" || string(call.Arguments) != `{"id":"getCustomers"}` {
		t.Errorf("tool call = %+v, want the fragments joined", call)
	}
	if got.FinishReason != "tool_calls" {
		t.Errorf("FinishReason = %q", got.FinishReason)
	}
}

func TestChat_ParallelToolCalls(t *testing.T) {
	t.Run("interleaved by index", func(t *testing.T) {
		tr := &sseTransport{respond: sseOK(events(
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"name":"one","arguments":"{\"x\":"}},{"index":1,"id":"call_b","function":{"name":"two","arguments":"{"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"\"y\":2}"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`[DONE]`,
		))}
		got, err := ai.New(configured(), tr, nil).Chat(context.Background(), "t", chatRequest(), nil)
		if err != nil {
			t.Fatal(err)
		}
		calls := got.Message.ToolCalls
		if len(calls) != 2 || calls[0].ID != "call_a" || string(calls[0].Arguments) != `{"x":1}` || calls[1].ID != "call_b" || string(calls[1].Arguments) != `{"y":2}` {
			t.Errorf("tool calls = %+v, want two, each with its own arguments, in index order", calls)
		}
	})
	t.Run("numbered from zero by a server that repeats the index", func(t *testing.T) {
		tr := &sseTransport{respond: sseOK(events(
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"name":"one","arguments":"{}"}},{"index":0,"id":"call_b","function":{"name":"two","arguments":"{\"k\":true}"}}]},"finish_reason":"stop"}]}`,
			`[DONE]`,
		))}
		got, err := ai.New(configured(), tr, nil).Chat(context.Background(), "t", chatRequest(), nil)
		if err != nil {
			t.Fatal(err)
		}
		calls := got.Message.ToolCalls
		if len(calls) != 2 || calls[0].Name != "one" || calls[1].Name != "two" || string(calls[1].Arguments) != `{"k":true}` {
			t.Errorf("tool calls = %+v, want a changed id at the same index to start a new call", calls)
		}
	})
}

func TestChat_ArgumentsAreAlwaysValidJSON(t *testing.T) {
	tr := &sseTransport{respond: sseOK(events(
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"empty","arguments":""}},{"index":1,"id":"b","function":{"name":"broken","arguments":"{\"q\": \"unterminated"}}]},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	))}
	got, err := ai.New(configured(), tr, nil).Chat(context.Background(), "t", chatRequest(), nil)
	if err != nil {
		t.Fatal(err)
	}
	calls := got.Message.ToolCalls
	if string(calls[0].Arguments) != `{}` {
		t.Errorf("empty arguments = %s, want {}", calls[0].Arguments)
	}
	if !json.Valid(calls[1].Arguments) || string(calls[1].Arguments) != `"{\"q\": \"unterminated"` {
		t.Errorf("broken arguments = %s, want the text carried as a JSON string", calls[1].Arguments)
	}
	if _, err := json.Marshal(got.Message); err != nil {
		t.Errorf("the message cannot be marshalled: %v", err)
	}
}

func TestChat_SendsTheConversationIncludingToolCallsAndTheirResults(t *testing.T) {
	tr := &sseTransport{respond: sseOK(events(`{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`, `[DONE]`))}
	req := ai.ChatRequest{
		Messages: []ai.Message{
			{Role: ai.RoleUser, Text: "List customers"},
			{Role: ai.RoleAssistant, ToolCalls: []ai.ToolCall{{ID: "call_1", Name: "call_operation", Arguments: json.RawMessage(`{"id":"listCustomers"}`)}, {ID: "call_2", Name: "no_args"}}},
			{Role: ai.RoleTool, ToolCallID: "call_1", Text: `{"status":200}`},
			{Role: ai.RoleTool, ToolCallID: "call_2", Text: "{}"},
			{Role: ai.RoleAssistant, Text: "Here they are."},
			{Role: ai.RoleUser, Text: "Thanks"},
		},
	}
	if _, err := ai.New(configured(), tr, nil).Chat(context.Background(), "t", req, nil); err != nil {
		t.Fatal(err)
	}
	body, _, _ := tr.last(t)
	raw, _ := json.Marshal(body["messages"])
	want := `[{"content":"List customers","role":"user"},` +
		`{"content":null,"role":"assistant","tool_calls":[` +
		`{"function":{"arguments":"{\"id\":\"listCustomers\"}","name":"call_operation"},"id":"call_1","type":"function"},` +
		`{"function":{"arguments":"{}","name":"no_args"},"id":"call_2","type":"function"}]},` +
		`{"content":"{\"status\":200}","role":"tool","tool_call_id":"call_1"},` +
		`{"content":"{}","role":"tool","tool_call_id":"call_2"},` +
		`{"content":"Here they are.","role":"assistant"},` +
		`{"content":"Thanks","role":"user"}]`
	if string(raw) != want {
		t.Errorf("messages on the wire =\n%s\nwant\n%s", raw, want)
	}
	if _, has := body["tools"]; has {
		t.Errorf("tools = %v, want none when the request names none", body["tools"])
	}
}

func TestChat_FailuresAreTheTypedErrors(t *testing.T) {
	transportFailure := errors.New("dial tcp: connection refused")
	for _, tc := range []struct {
		name    string
		respond func(*http.Request) (*http.Response, error)
		want    string
	}{
		{"a 429", func(*http.Request) (*http.Response, error) { return reply(429, `{"error":"slow down"}`), nil }, "aiStatusError"},
		{"a 500", func(*http.Request) (*http.Response, error) { return reply(500, ``), nil }, "aiStatusError"},
		{"a transport failure", func(*http.Request) (*http.Response, error) { return nil, transportFailure }, "aiTransportError"},
		{"a truncated stream", sseOK(events(`{"choices":[{"delta":{"content":"half an ans"}}]}`)), "aiDecodeError"},
		{"a stream cut mid-line", sseOK("data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]"), "aiDecodeError"},
		{"an undecodable chunk", sseOK(events(`not json`, `[DONE]`)), "aiDecodeError"},
		{"an error after the stream began", sseOK(events(`{"choices":[{"delta":{"content":"a"}}]}`, `{"error":{"message":"out of memory"}}`, `[DONE]`)), "aiDecodeError"},
		{"a tool call without a name", sseOK(events(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"arguments":"{}"}}]}}]}`, `[DONE]`)), "aiDecodeError"},
		{"a stream with nothing in it", sseOK(events(`[DONE]`)), "aiEmptyResponseError"},
		{"a body that is not a stream", sseOK(`{"choices":[{"message":{"content":"whole"}}]}`), "aiDecodeError"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := &sseTransport{respond: tc.respond}
			got, err := ai.New(configured(), tr, choose{"t": "gpt-4o"}).Chat(context.Background(), "t", chatRequest(), nil)
			if err == nil {
				t.Fatal("Chat succeeded, want a typed error")
			}
			if name := typeName(err); name != tc.want {
				t.Errorf("error type = %s, want %s", name, tc.want)
			}
			if got.Provider != "openai" || got.Model != "gpt-4o" {
				t.Errorf("response = %+v, want the provider and model named on an error too", got)
			}
			if got.Message.Text != "" || len(got.Message.ToolCalls) != 0 {
				t.Errorf("message = %+v, want none: half an answer is never handed on", got.Message)
			}
		})
	}

	t.Run("the transport failure keeps its cause for errors.Is only", func(t *testing.T) {
		tr := &sseTransport{respond: func(*http.Request) (*http.Response, error) { return nil, transportFailure }}
		_, err := ai.New(configured(), tr, nil).Chat(context.Background(), "t", chatRequest(), nil)
		if !errors.Is(err, transportFailure) {
			t.Errorf("errors.Is(err, cause) = false, want the cause kept")
		}
		if strings.Contains(err.Error(), "connection refused") {
			t.Errorf("error %q quotes the cause", err)
		}
	})
}

func TestChat_RefusesAModelThePlanDoesNotAllowBeforeAnyRequest(t *testing.T) {
	tr := &sseTransport{respond: sseOK(events(`[DONE]`))}
	got, err := ai.New(configured(), tr, choose{"t": "gpt-5-unlisted"}).Chat(context.Background(), "t", chatRequest(), nil)
	if typeName(err) != "aiModelNotAllowedError" {
		t.Fatalf("error = %v, want aiModelNotAllowedError", err)
	}
	if tr.count() != 0 {
		t.Errorf("%d requests were made, want none", tr.count())
	}
	if got.Provider != "openai" || got.Model != "gpt-5-unlisted" {
		t.Errorf("response = %+v", got)
	}

	_, err = ai.New(configured(), tr, failing{}).Chat(context.Background(), "t", chatRequest(), nil)
	if typeName(err) != "aiModelUnavailableError" {
		t.Errorf("error = %v, want aiModelUnavailableError for a resolver failure", err)
	}
}

func TestChat_TheBudgetIsAITimeoutForTheWholeCall(t *testing.T) {
	// A server that sends one delta and then stalls until the request's context ends.
	pr, pw := io.Pipe()
	go func() { _, _ = pw.Write([]byte(events(`{"choices":[{"delta":{"content":"slow"}}]}`))) }()
	tr := &sseTransport{respond: func(r *http.Request) (*http.Response, error) {
		go func() { <-r.Context().Done(); _ = pw.CloseWithError(r.Context().Err()) }()
		return &http.Response{StatusCode: 200, Body: pr, Header: http.Header{}}, nil
	}}
	cfg := configured()
	cfg.AITimeout = 50 * time.Millisecond

	var deltas []string
	started := time.Now()
	_, err := ai.New(cfg, tr, nil).Chat(context.Background(), "t", chatRequest(), func(s string) { deltas = append(deltas, s) })
	if typeName(err) != "aiTransportError" || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v (%s), want a transport error wrapping the deadline", err, typeName(err))
	}
	if time.Since(started) > 5*time.Second {
		t.Errorf("Chat took %v, want it cut off by AI_TIMEOUT", time.Since(started))
	}
	if len(deltas) != 1 || deltas[0] != "slow" {
		t.Errorf("deltas = %q, want the one that arrived before the stall", deltas)
	}
}

func TestChat_ACancelledContextStopsIt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tr := &sseTransport{respond: func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() }}
	_, err := ai.New(configured(), tr, nil).Chat(ctx, "t", chatRequest(), nil)
	if typeName(err) != "aiTransportError" || !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want a transport error wrapping the cancellation", err)
	}
}

func TestChat_KeepsServingWhenAnOverlongLineIsNotThere(t *testing.T) {
	// A line longer than one read buffer is read whole.
	long := strings.Repeat("a", 100_000)
	tr := &sseTransport{respond: sseOK(events(`{"choices":[{"delta":{"content":"`+long+`"},"finish_reason":"stop"}]}`, `[DONE]`))}
	got, err := ai.New(configured(), tr, nil).Chat(context.Background(), "t", chatRequest(), nil)
	if err != nil || got.Message.Text != long {
		t.Errorf("Chat = %d bytes, %v, want the long delta intact", len(got.Message.Text), err)
	}
}

func TestChat_IgnoresCommentsAndOtherFieldsOfTheStream(t *testing.T) {
	body := ": keep-alive\n\nevent: message\nid: 7\nretry: 100\ndata: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\r\n\r\ndata:[DONE]\n\n"
	tr := &sseTransport{respond: sseOK(body)}
	got, err := ai.New(configured(), tr, nil).Chat(context.Background(), "t", chatRequest(), nil)
	if err != nil || got.Message.Text != "hi" {
		t.Errorf("Chat = %q, %v, want hi", got.Message.Text, err)
	}
}

func TestComplete_AKeylessClientSendsNoAuthorizationHeader(t *testing.T) {
	tr := &fakeTransport{}
	cfg := &config.Config{AIProvider: "openai-compatible", AIBaseURL: "http://127.0.0.1:11434/v1", AIModels: []string{"llama3"}}
	if _, err := ai.New(cfg, tr, nil).Complete(context.Background(), "t", ai.Request{Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	if got := tr.Calls()[0]; got.Authorization != "" || got.URL != "http://127.0.0.1:11434/v1/chat/completions" {
		t.Errorf("request = %+v, want no Authorization and the local URL", got)
	}
}

func TestNew_AKeyIsOptionalOnlyBesideABaseURL(t *testing.T) {
	if c := ai.New(&config.Config{AIProvider: "openai-compatible", AIModels: []string{"m"}}, nil, nil); c != nil {
		t.Error("New built a client with neither a key nor a base URL, want nil")
	}
	if c := ai.New(&config.Config{AIProvider: "openai-compatible", AIBaseURL: "http://ollama:11434/v1", AIModels: []string{"m"}}, nil, nil); c == nil {
		t.Error("New returned nil for a base URL without a key")
	}
}
