package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

const (
	// chatStreamMaxBytes bounds one streamed answer in total. A model that
	// never stops is also bounded by the call's timeout; this is the memory
	// bound.
	chatStreamMaxBytes = 8 << 20
	// chatLineMaxBytes bounds one server-sent line, a single chunk's JSON.
	chatLineMaxBytes = 1 << 20
	// emptyToolSchema describes a function that takes no arguments.
	emptyToolSchema = `{"type":"object","properties":{}}`
)

// chat streams one Chat Completions turn: stream and usage are asked for, the
// server-sent events are read up to [DONE], text deltas go to onDelta as they
// arrive, and tool calls are assembled from their fragments by index.
//
// It speaks to every OpenAI-compatible server, which differ in small ways it
// tolerates: Ollama may send no usage, a server may send a whole tool call in
// one chunk, and some number every call of a parallel batch from 0 (a call
// whose id differs from the one already at its index is a new call).
func (o *openAI) chat(ctx context.Context, model string, req ChatRequest, onDelta func(text string)) (ChatResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, o.chatTimeout)
	defer cancel()

	payload, err := o.chatPayload(model, req)
	if err != nil {
		return ChatResponse{}, aiDecodeError{}
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+chatCompletionsPath, bytes.NewReader(payload))
	if err != nil {
		return ChatResponse{}, aiTransportError{err: err}
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "text/event-stream")
	o.authorize(hreq)

	resp, err := o.client.Do(hreq)
	if err != nil {
		return ChatResponse{}, aiTransportError{err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ChatResponse{}, aiStatusError{status: resp.StatusCode}
	}

	return readChatStream(io.LimitReader(resp.Body, chatStreamMaxBytes), onDelta)
}

// chatPayload is the request body: the system message first, then the
// messages in the Chat Completions shape.
func (o *openAI) chatPayload(model string, req ChatRequest) ([]byte, error) {
	messages := make([]any, 0, len(req.Messages)+1)
	if req.System != "" {
		messages = append(messages, map[string]any{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case RoleTool:
			messages = append(messages, map[string]any{"role": "tool", "tool_call_id": m.ToolCallID, "content": m.Text})
		case RoleAssistant:
			msg := map[string]any{"role": "assistant", "content": m.Text}
			if len(m.ToolCalls) > 0 {
				calls := make([]any, len(m.ToolCalls))
				for i, c := range m.ToolCalls {
					args := string(c.Arguments)
					if strings.TrimSpace(args) == "" {
						args = "{}"
					}
					calls[i] = map[string]any{
						"id":       c.ID,
						"type":     "function",
						"function": map[string]any{"name": c.Name, "arguments": args},
					}
				}
				msg["tool_calls"] = calls
				if m.Text == "" {
					// OpenAI refuses an empty string beside tool calls on some models;
					// null is what its own answers carry.
					msg["content"] = nil
				}
			}
			messages = append(messages, msg)
		default:
			messages = append(messages, map[string]any{"role": "user", "content": m.Text})
		}
	}

	body := map[string]any{
		"model":          model,
		"messages":       messages,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	if len(req.Tools) > 0 {
		tools := make([]any, len(req.Tools))
		for i, t := range req.Tools {
			params := t.Parameters
			if len(bytes.TrimSpace(params)) == 0 {
				params = json.RawMessage(emptyToolSchema)
			}
			tools[i] = map[string]any{
				"type":     "function",
				"function": map[string]any{"name": t.Name, "description": t.Description, "parameters": params},
			}
		}
		body["tools"] = tools
	}
	if req.MaxTokens > 0 {
		// OpenAI's newer models refuse max_tokens and its own endpoint accepts
		// max_completion_tokens; the compatible servers understand max_tokens
		// everywhere and max_completion_tokens only in their newer releases.
		if o.baseURL == defaultOpenAIBaseURL {
			body["max_completion_tokens"] = req.MaxTokens
		} else {
			body["max_tokens"] = req.MaxTokens
		}
	}
	return json.Marshal(body)
}

// streamChunk is one server-sent chunk of a streamed completion.
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content   *string `json:"content"`
			ToolCalls []struct {
				Index    *int   `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     *int64 `json:"prompt_tokens"`
		CompletionTokens *int64 `json:"completion_tokens"`
	} `json:"usage"`
	// Error is how some servers report a failure after the stream began.
	Error json.RawMessage `json:"error"`
}

// toolCallAcc accumulates one tool call from its fragments.
type toolCallAcc struct {
	id   string
	name string
	args strings.Builder
}

// readChatStream reads server-sent events from r up to [DONE] and assembles
// the answer. A stream that ends before [DONE] — the connection dropped, the
// budget ran out — is an error: half an answer is never handed on as whole.
func readChatStream(r io.Reader, onDelta func(text string)) (ChatResponse, error) {
	var (
		text      strings.Builder
		calls     []*toolCallAcc
		byIndex   = map[int]*toolCallAcc{}
		finish    string
		sawChoice bool
		out       ChatResponse
		done      bool
	)

	br := bufio.NewReaderSize(r, 64<<10)
	for !done {
		line, err := readLine(br)
		if err != nil && !(errors.Is(err, io.EOF) && line != "") {
			if errors.Is(err, io.EOF) {
				return ChatResponse{}, aiDecodeError{}
			}
			if errors.Is(err, errLineTooLong) {
				return ChatResponse{}, aiDecodeError{}
			}
			return ChatResponse{}, aiTransportError{err: err}
		}

		data, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), "data:")
		if !ok {
			// A blank separator, a comment (": keep-alive") or an event/id/retry field.
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			done = true
			continue
		}
		if data == "" {
			continue
		}

		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return ChatResponse{}, aiDecodeError{}
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			return ChatResponse{}, aiDecodeError{}
		}
		if chunk.Usage != nil {
			out.InputTokens = tokenCount(chunk.Usage.PromptTokens)
			out.OutputTokens = tokenCount(chunk.Usage.CompletionTokens)
		}
		for _, choice := range chunk.Choices {
			// Even a role-only opening delta is a choice: the model answered.
			sawChoice = true
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				finish = *choice.FinishReason
			}
			if c := choice.Delta.Content; c != nil && *c != "" {
				text.WriteString(*c)
				if onDelta != nil {
					onDelta(*c)
				}
			}
			for _, tc := range choice.Delta.ToolCalls {
				var acc *toolCallAcc
				switch {
				case tc.Index != nil:
					acc = byIndex[*tc.Index]
					if acc == nil || (tc.ID != "" && acc.id != "" && tc.ID != acc.id) {
						acc = &toolCallAcc{}
						calls = append(calls, acc)
						byIndex[*tc.Index] = acc
					}
				case tc.ID != "" || len(calls) == 0:
					acc = &toolCallAcc{}
					calls = append(calls, acc)
				default:
					acc = calls[len(calls)-1]
				}
				if tc.ID != "" {
					acc.id = tc.ID
				}
				if tc.Function.Name != "" {
					acc.name = tc.Function.Name
				}
				acc.args.WriteString(tc.Function.Arguments)
			}
		}
	}

	if !sawChoice && text.Len() == 0 && len(calls) == 0 {
		return ChatResponse{}, aiEmptyResponseError{}
	}

	msg := Message{Role: RoleAssistant, Text: text.String()}
	for _, acc := range calls {
		if acc.name == "" {
			return ChatResponse{}, aiDecodeError{}
		}
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: acc.id, Name: acc.name, Arguments: validArguments(acc.args.String())})
	}
	out.Message, out.FinishReason = msg, finish
	return out, nil
}

var errLineTooLong = errors.New("ai: a server-sent line is too long")

// readLine reads one line including its newline. At the end of the stream it
// returns what it read with io.EOF, so a final line without a newline is not lost.
func readLine(br *bufio.Reader) (string, error) {
	var sb strings.Builder
	for {
		chunk, isPrefix, err := br.ReadLine()
		if err != nil {
			return sb.String(), err
		}
		sb.Write(chunk)
		if sb.Len() > chatLineMaxBytes {
			return "", errLineTooLong
		}
		if !isPrefix {
			return sb.String(), nil
		}
	}
}

// validArguments is raw as JSON: itself when it is valid (an empty string is
// the empty object), else the text as a JSON string, so the answer can always
// be marshalled and a caller can see the model did not send an object.
func validArguments(raw string) json.RawMessage {
	if strings.TrimSpace(raw) == "" {
		return json.RawMessage("{}")
	}
	if json.Valid([]byte(raw)) {
		return json.RawMessage(raw)
	}
	quoted, err := json.Marshal(raw)
	if err != nil {
		return json.RawMessage(`""`)
	}
	return quoted
}
