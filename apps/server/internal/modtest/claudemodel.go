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

// FakeClaude stands in for Anthropic's Messages API as an http.RoundTripper,
// for a test of the Assistant on AI_PROVIDER=anthropic: pass it to
// WithTransport, with AI_PROVIDER, AI_API_KEY and AI_MODELS set through
// WithEnv. It answers each streamed request with a Messages stream from a
// script, every turn opening with a signed thinking block as a current model
// does, and records every request body so a test can assert what was sent.
type FakeClaude struct {
	mu       sync.Mutex
	fn       func(n int, req ClaudeRequest) ClaudeTurn
	requests []ClaudeRequest
}

// ClaudeTurn is one answer of the fake model.
type ClaudeTurn struct {
	Text      string
	ToolCalls []ChatToolCall
	// Refusal, when not empty, ends the turn with the refusal stop reason and
	// this policy category.
	Refusal string
	// Status, when not zero, makes the endpoint answer that status and no stream.
	Status int
}

// ClaudeRequest is what the module sent.
type ClaudeRequest struct {
	// Raw is the request body exactly as sent.
	Raw []byte
	// System is the text of the system blocks, Tools the raw tools array and
	// Messages the raw messages.
	System   string
	Tools    json.RawMessage
	Messages []json.RawMessage
	// ToolChoice is the type of the tool choice, "" when none was sent.
	ToolChoice string
}

// NewFakeClaude answers request n (from 0) with fn(n, request).
func NewFakeClaude(fn func(n int, req ClaudeRequest) ClaudeTurn) *FakeClaude {
	return &FakeClaude{fn: fn}
}

// Requests are the requests received, oldest first.
func (f *FakeClaude) Requests() []ClaudeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ClaudeRequest(nil), f.requests...)
}

// RoundTrip implements http.RoundTripper.
func (f *FakeClaude) RoundTrip(r *http.Request) (*http.Response, error) {
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		System []struct {
			Text string `json:"text"`
		} `json:"system"`
		Tools      json.RawMessage   `json:"tools"`
		Messages   []json.RawMessage `json:"messages"`
		ToolChoice struct {
			Type string `json:"type"`
		} `json:"tool_choice"`
	}
	_ = json.Unmarshal(raw, &body)
	req := ClaudeRequest{Raw: raw, Tools: body.Tools, Messages: body.Messages, ToolChoice: body.ToolChoice.Type}
	for _, s := range body.System {
		req.System += s.Text
	}
	f.mu.Lock()
	n := len(f.requests)
	f.requests = append(f.requests, req)
	f.mu.Unlock()

	turn := f.fn(n, req)
	if turn.Status != 0 {
		return &http.Response{StatusCode: turn.Status, Body: io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"api_error","message":"fake"}}`)), Header: http.Header{"Content-Type": {"application/json"}}}, nil
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(turn.stream(n))), Header: http.Header{"Content-Type": {"text/event-stream"}}}, nil
}

func (t ClaudeTurn) stream(n int) []byte {
	var sb strings.Builder
	event := func(name string, v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(&sb, "event: %s\ndata: %s\n\n", name, b)
	}
	m := func(kv ...any) map[string]any {
		out := map[string]any{}
		for i := 0; i+1 < len(kv); i += 2 {
			out[kv[i].(string)] = kv[i+1]
		}
		return out
	}
	event("message_start", m("type", "message_start", "message", m(
		"id", fmt.Sprintf("msg_%d", n), "type", "message", "role", "assistant", "model", "claude-sonnet-5-5", "content", []any{},
		"stop_reason", nil, "stop_sequence", nil, "input_transformations", []any{},
		"usage", m("input_tokens", 50, "output_tokens", 1, "cache_creation_input_tokens", 0, "cache_read_input_tokens", 0))))
	index := 0
	open := func(block map[string]any, delta map[string]any) {
		event("content_block_start", m("type", "content_block_start", "index", index, "content_block", block))
		if delta != nil {
			event("content_block_delta", m("type", "content_block_delta", "index", index, "delta", delta))
		}
		event("content_block_stop", m("type", "content_block_stop", "index", index))
		index++
	}
	if t.Refusal != "" {
		event("message_delta", m("type", "message_delta", "delta", m("stop_reason", "refusal", "stop_sequence", nil,
			"stop_details", m("type", "refusal", "category", t.Refusal)), "usage", m("output_tokens", 0)))
		event("message_stop", m("type", "message_stop"))
		return []byte(sb.String())
	}
	open(m("type", "thinking", "thinking", "", "signature", fmt.Sprintf("sig-turn-%d", n)), nil)
	if t.Text != "" {
		open(m("type", "text", "text", ""), m("type", "text_delta", "text", t.Text))
	}
	for i, c := range t.ToolCalls {
		args, _ := json.Marshal(c.Args)
		if c.Args == nil {
			args = []byte("{}")
		}
		id := c.ID
		if id == "" {
			id = fmt.Sprintf("toolu_%d_%d", n, i)
		}
		open(m("type", "tool_use", "id", id, "name", c.Name, "input", m()), m("type", "input_json_delta", "partial_json", string(args)))
	}
	stop := "end_turn"
	if len(t.ToolCalls) > 0 {
		stop = "tool_use"
	}
	event("message_delta", m("type", "message_delta", "delta", m("stop_reason", stop, "stop_sequence", nil), "usage", m("output_tokens", 20)))
	event("message_stop", m("type", "message_stop"))
	return []byte(sb.String())
}
