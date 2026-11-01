package modtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// FakeChatModel stands in for an OpenAI-compatible chat endpoint (Ollama, vLLM,
// OpenAI itself) as an http.RoundTripper, for a test of the Assistant: pass it
// to WithTransport, with AI_PROVIDER=openai-compatible, AI_BASE_URL and
// AI_MODELS set through WithEnv. It answers each chat completion with a
// streamed body in the shape the real servers send, from a script of turns, and
// records every request so a test can assert what the module sent.
type FakeChatModel struct {
	mu       sync.Mutex
	script   []ChatTurn
	fn       func(n int, req ChatRequest) ChatTurn
	requests []ChatRequest
}

// ChatTurn is one answer of the fake model.
type ChatTurn struct {
	// Text is streamed as content deltas of at most 8 bytes each.
	Text string
	// ToolCalls are the calls the model asks for.
	ToolCalls []ChatToolCall
	// Status, when not zero, makes the endpoint answer that HTTP status and no
	// stream.
	Status int
	// Block, when not nil, is waited on before the answer is written, so a test
	// can hold a run mid-turn.
	Block <-chan struct{}
}

// ChatToolCall is one tool call of a turn.
type ChatToolCall struct {
	ID   string
	Name string
	Args any
}

// ChatRequest is what the module sent: the messages and the names of the tools.
type ChatRequest struct {
	Model    string
	Messages []ChatMessage
	Tools    []string
}

// ChatMessage is one message of a request.
type ChatMessage struct {
	Role       string
	Content    string
	ToolCallID string
	ToolCalls  []string // names, for an assistant message
}

// NewFakeChatModel answers each request with the next turn of script, and a
// plain "OK" once the script is spent.
func NewFakeChatModel(script ...ChatTurn) *FakeChatModel { return &FakeChatModel{script: script} }

// NewFakeChatModelFunc answers request n (from 0) with fn(n, request).
func NewFakeChatModelFunc(fn func(n int, req ChatRequest) ChatTurn) *FakeChatModel {
	return &FakeChatModel{fn: fn}
}

// Requests are the requests the model has received, oldest first.
func (f *FakeChatModel) Requests() []ChatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ChatRequest(nil), f.requests...)
}

// RoundTrip implements http.RoundTripper.
func (f *FakeChatModel) RoundTrip(r *http.Request) (*http.Response, error) {
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		Model    string `json:"model"`
		Messages []struct {
			Role       string `json:"role"`
			Content    any    `json:"content"`
			ToolCallID string `json:"tool_call_id"`
			ToolCalls  []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	_ = json.Unmarshal(raw, &body)
	req := ChatRequest{Model: body.Model}
	for _, m := range body.Messages {
		cm := ChatMessage{Role: m.Role, ToolCallID: m.ToolCallID}
		if s, ok := m.Content.(string); ok {
			cm.Content = s
		}
		for _, c := range m.ToolCalls {
			cm.ToolCalls = append(cm.ToolCalls, c.Function.Name)
		}
		req.Messages = append(req.Messages, cm)
	}
	for _, t := range body.Tools {
		req.Tools = append(req.Tools, t.Function.Name)
	}

	f.mu.Lock()
	n := len(f.requests)
	f.requests = append(f.requests, req)
	var turn ChatTurn
	switch {
	case f.fn != nil:
		fn := f.fn
		f.mu.Unlock()
		turn = fn(n, req)
	case n < len(f.script):
		turn = f.script[n]
		f.mu.Unlock()
	default:
		f.mu.Unlock()
		turn = ChatTurn{Text: "OK"}
	}

	if turn.Block != nil {
		select {
		case <-turn.Block:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	}
	if turn.Status != 0 {
		return &http.Response{StatusCode: turn.Status, Body: io.NopCloser(strings.NewReader(`{"error":"fake"}`)), Header: http.Header{"Content-Type": {"application/json"}}}, nil
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(turn.stream())), Header: http.Header{"Content-Type": {"text/event-stream"}}}, nil
}

func (t ChatTurn) stream() []byte {
	var sb strings.Builder
	chunk := func(delta map[string]any, finish any, usage map[string]any) {
		c := map[string]any{"id": "fake", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
		if usage != nil {
			c["usage"] = usage
		}
		b, _ := json.Marshal(c)
		fmt.Fprintf(&sb, "data: %s\n\n", b)
	}
	chunk(map[string]any{"role": "assistant", "content": ""}, nil, nil)
	for rest := t.Text; rest != ""; {
		n := min(8, len(rest))
		for n < len(rest) && rest[n]&0xC0 == 0x80 { // never cut a UTF-8 rune
			n++
		}
		chunk(map[string]any{"content": rest[:n]}, nil, nil)
		rest = rest[n:]
	}
	for i, c := range t.ToolCalls {
		args, _ := json.Marshal(c.Args)
		if c.Args == nil {
			args = []byte("{}")
		}
		id := c.ID
		if id == "" {
			id = fmt.Sprintf("call_%d", i)
		}
		chunk(map[string]any{"tool_calls": []any{map[string]any{
			"index": i, "id": id, "type": "function", "function": map[string]any{"name": c.Name, "arguments": string(args)},
		}}}, nil, nil)
	}
	finish := "stop"
	if len(t.ToolCalls) > 0 {
		finish = "tool_calls"
	}
	chunk(map[string]any{}, finish, map[string]any{"prompt_tokens": 100, "completion_tokens": 20})
	sb.WriteString("data: [DONE]\n\n")
	return []byte(sb.String())
}
