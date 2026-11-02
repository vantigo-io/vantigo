package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	// ProviderAnthropic is AI_PROVIDER=anthropic: Anthropic's Messages API,
	// through the official Go SDK.
	ProviderAnthropic = "anthropic"

	defaultAnthropicBaseURL = "https://api.anthropic.com/"
	defaultAnthropicEffort  = "medium"

	// anthropicCompleteTimeout is Complete's budget, as OpenAI's is.
	anthropicCompleteTimeout = 30 * time.Second
	// anthropicCompleteMaxTokens bounds a completion. Thinking counts toward
	// max_tokens, so the room is generous.
	anthropicCompleteMaxTokens = 16000
	// anthropicThinkingRoom is added to Chat's MaxTokens: ChatRequest.MaxTokens
	// bounds the answer, and a Claude model's thinking is paid from the same
	// limit, so an answer limit alone would cut the answer off after thinking.
	anthropicThinkingRoom = 12000
)

// anthropicSettings is what the provider is built from.
type anthropicSettings struct {
	apiKey      string
	baseURL     string
	effort      string
	fallbacks   bool
	chatTimeout time.Duration
	logger      *slog.Logger
}

// anthropicProvider is the Messages API behind chatProvider. The package's
// guard holds: the SDK is handed the package's own http.Client, built over
// the transport the host gives every module, refusing redirects and bounding
// the body; its retries are off (the caller owns retrying); its environment
// defaults are ignored (an ANTHROPIC_* variable in the process is never read);
// and the context carries the budget.
type anthropicProvider struct {
	client anthropic.Client
	s      anthropicSettings
}

func newAnthropic(s anthropicSettings, transport http.RoundTripper) *anthropicProvider {
	base := s.baseURL
	if base == "" {
		base = defaultAnthropicBaseURL
	}
	httpClient := &http.Client{
		Transport: boundedTransport{next: transport},
		// A redirect would re-send the request, API key included, to a host
		// no operator named.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &anthropicProvider{
		s: s,
		client: anthropic.NewClient(
			option.WithoutEnvironmentDefaults(),
			option.WithHTTPClient(httpClient),
			option.WithAPIKey(s.apiKey),
			option.WithBaseURL(base),
			option.WithMaxRetries(0),
		),
	}
}

// boundedTransport caps the bytes a response body yields, the memory bound
// chatStreamMaxBytes is for the OpenAI-compatible stream. A body cut by the
// cap ends the stream early, which is a decode error.
type boundedTransport struct{ next http.RoundTripper }

func (b boundedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := b.next.RoundTrip(r)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	resp.Body = limitedBody{Reader: io.LimitReader(resp.Body, chatStreamMaxBytes), Closer: resp.Body}
	return resp, nil
}

type limitedBody struct {
	io.Reader
	io.Closer
}

// supportsFallbacks reports whether server-side fallbacks are worth asking
// for on model: every current Claude model but Haiku, which has none.
func supportsFallbacks(model string) bool {
	return !strings.Contains(strings.ToLower(model), "haiku")
}

// params is the request both doors share: the model, the messages, the
// adaptive thinking's effort, and the fallbacks when they are on and the model
// has them. Thinking is never configured beyond adaptive and never disabled,
// and no budget_tokens, sampling parameter, prefill or forced tool choice is
// sent: current models refuse each one.
func (a *anthropicProvider) params(model string, messages []anthropic.BetaMessageParam, maxTokens int64) anthropic.BetaMessageNewParams {
	p := anthropic.BetaMessageNewParams{
		Model:        anthropic.Model(model),
		MaxTokens:    maxTokens,
		Messages:     messages,
		OutputConfig: anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffort(a.s.effort)},
	}
	if a.s.fallbacks && supportsFallbacks(model) {
		p.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
		p.Betas = append(p.Betas, anthropic.AnthropicBetaServerSideFallback2026_07_01)
	}
	return p
}

// complete sends prompt as a single user message and answers its text.
func (a *anthropicProvider) complete(ctx context.Context, model, prompt string) (completion, error) {
	ctx, cancel := context.WithTimeout(ctx, anthropicCompleteTimeout)
	defer cancel()

	p := a.params(model, []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(prompt))}, anthropicCompleteMaxTokens)
	msg, err := a.client.Beta.Messages.New(ctx, p)
	if err != nil {
		return completion{}, mapAnthropicError(ctx, err)
	}
	if err := checkStop(msg); err != nil {
		return completion{}, err
	}
	var text strings.Builder
	for _, b := range msg.Content {
		if b.Type == "text" {
			text.WriteString(b.Text)
		}
	}
	if text.Len() == 0 {
		return completion{}, aiEmptyResponseError{}
	}
	in, out, _, _ := anthropicUsage(msg.Usage)
	return completion{text: text.String(), inputTokens: in, outputTokens: out}, nil
}

// chat streams one turn. The request carries the system prompt and the tools
// as the stable prefix (the system block is marked for caching, which caches
// the tools before it), the adaptive thinking's effort, and the binding
// control that turns a thinking block the API cannot honour into a dropped
// block instead of a refused request. The answer is accumulated by the SDK;
// text deltas go to onDelta as they arrive and thinking is never surfaced.
func (a *anthropicProvider) chat(ctx context.Context, model string, req ChatRequest, onDelta func(text string)) (ChatResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, a.s.chatTimeout)
	defer cancel()

	maxTokens := int64(anthropicCompleteMaxTokens)
	if req.MaxTokens > 0 {
		maxTokens = int64(req.MaxTokens) + anthropicThinkingRoom
	}
	p := a.params(model, anthropicMessages(req.Messages), maxTokens)
	p.Thinking = anthropic.BetaThinkingConfigParamUnion{OfAdaptive: &anthropic.BetaThinkingConfigAdaptiveParam{
		BlockBinding: anthropic.BetaThinkingBlockBindingParam{PrefixMismatchBehavior: anthropic.BetaThinkingPrefixMismatchBehaviorDropBlock},
	}}
	p.Betas = append(p.Betas, anthropic.AnthropicBetaThinkingBindingControls2026_08_01)
	if req.System != "" {
		p.System = []anthropic.BetaTextBlockParam{{Text: req.System, CacheControl: anthropic.NewBetaCacheControlEphemeralParam()}}
	}
	validators := map[string]*jsonschema.Schema{}
	for _, t := range req.Tools {
		schema := t.Parameters
		if len(bytes.TrimSpace(schema)) == 0 {
			schema = json.RawMessage(emptyToolSchema)
		}
		p.Tools = append(p.Tools, anthropic.BetaToolUnionParam{OfTool: &anthropic.BetaToolParam{
			Name:                t.Name,
			Description:         anthropic.String(t.Description),
			InputSchema:         inputSchema(schema),
			EagerInputStreaming: anthropic.Bool(true),
		}})
		if v := compileSchema(schema); v != nil {
			validators[t.Name] = v
		}
	}
	if req.ToolsDisabled && len(p.Tools) > 0 {
		p.ToolChoice = anthropic.BetaToolChoiceUnionParam{OfNone: &anthropic.BetaToolChoiceNoneParam{}}
	}

	stream := a.client.Beta.Messages.NewStreaming(ctx, p)
	defer func() { _ = stream.Close() }()
	var (
		msg     anthropic.BetaMessage
		stopped bool
	)
	for stream.Next() {
		ev := stream.Current()
		if err := msg.Accumulate(ev); err != nil {
			return ChatResponse{}, aiDecodeError{}
		}
		switch ev.Type {
		case "content_block_delta":
			if ev.Delta.Type == "text_delta" && ev.Delta.Text != "" && onDelta != nil {
				onDelta(ev.Delta.Text)
			}
		case "message_stop":
			stopped = true
		}
	}
	if err := stream.Err(); err != nil {
		return ChatResponse{}, mapAnthropicError(ctx, err)
	}
	// A stream that ends before message_stop is half an answer, never handed
	// on as whole.
	if !stopped {
		return ChatResponse{}, aiDecodeError{}
	}
	a.logTransformations(msg)
	if err := checkStop(&msg); err != nil {
		return ChatResponse{}, err
	}
	return assembleChat(&msg, validators)
}

// checkStop turns a stop reason that is no answer into a typed error: the
// provider's safeguards declined, or the token limit cut the answer off.
func checkStop(msg *anthropic.BetaMessage) error {
	switch msg.StopReason {
	case anthropic.BetaStopReasonRefusal:
		return aiRefusalError{category: string(msg.StopDetails.Category)}
	case anthropic.BetaStopReasonMaxTokens:
		return aiMaxTokensError{}
	}
	return nil
}

// logTransformations reports each change the API made to the request, so a
// regression in the append-only history shows. It logs the type, the reason
// and the path of the entry, never content.
func (a *anthropicProvider) logTransformations(msg anthropic.BetaMessage) {
	for _, t := range msg.InputTransformations {
		a.s.logger.Warn("ai: the provider changed the request", "transformation", t.Type, "reason", t.Reason, "path", t.Path)
	}
}

// assembleChat builds the answer from the accumulated message: its text, its
// tool calls in order with each input validated against the tool's schema,
// the turn's content blocks as received for replay, and the token counts.
func assembleChat(msg *anthropic.BetaMessage, validators map[string]*jsonschema.Schema) (ChatResponse, error) {
	// After a fallback that served the answer mid-output, what came before
	// its marker must not be echoed back: thinking, redacted thinking and tool
	// use of the declined attempt. The marker itself is an audit note only.
	lastFallback := -1
	for i, b := range msg.Content {
		if b.Type == "fallback" {
			lastFallback = i
		}
	}

	var (
		text   strings.Builder
		calls  []ToolCall
		blocks []json.RawMessage
	)
	for i, b := range msg.Content {
		if b.Type == "fallback" {
			continue
		}
		if i < lastFallback && (b.Type == "thinking" || b.Type == "redacted_thinking" || b.Type == "tool_use") {
			continue
		}
		blocks = append(blocks, json.RawMessage(b.RawJSON()))
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "tool_use":
			calls = append(calls, ToolCall{ID: b.ID, Name: b.Name, Arguments: checkedArguments(b.Input, validators[b.Name])})
		}
	}
	if len(blocks) == 0 {
		return ChatResponse{}, aiEmptyResponseError{}
	}
	provider, err := json.Marshal(blocks)
	if err != nil {
		return ChatResponse{}, aiDecodeError{}
	}

	out := ChatResponse{
		Message:      Message{Role: RoleAssistant, Text: text.String(), ToolCalls: calls, Provider: provider},
		FinishReason: finishReason(msg.StopReason),
	}
	out.InputTokens, out.OutputTokens, out.CacheReadTokens, out.CacheWriteTokens = anthropicUsage(msg.Usage)
	return out, nil
}

// finishReason is the provider's stop reason in the neutral words Chat's other
// providers use.
func finishReason(r anthropic.BetaStopReason) string {
	switch r {
	case anthropic.BetaStopReasonEndTurn, anthropic.BetaStopReasonStopSequence:
		return "stop"
	case anthropic.BetaStopReasonToolUse:
		return "tool_calls"
	}
	return string(r)
}

// anthropicUsage is the token counts of a message. The prompt's size is its
// uncached part plus what was read from and written to the cache.
func anthropicUsage(u anthropic.BetaUsage) (in, out, cacheRead, cacheWrite *int32) {
	read, write, output := u.CacheReadInputTokens, u.CacheCreationInputTokens, u.OutputTokens
	total := u.InputTokens + read + write
	return tokenCount(&total), tokenCount(&output), tokenCount(&read), tokenCount(&write)
}

// checkedArguments is a tool's input as Chat hands it on: itself when it is a
// valid instance of the tool's schema. Eager input streaming validates
// nothing, and a call cut short or malformed would otherwise run as an object
// that is not what the model meant, so anything else is carried as a JSON
// string of the text received, as validArguments does for text that is no
// JSON, and the caller tells the model so.
func checkedArguments(raw json.RawMessage, schema *jsonschema.Schema) json.RawMessage {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		text = "{}"
	}
	if !json.Valid([]byte(text)) {
		return validArguments(text)
	}
	if schema != nil {
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(text))
		if err != nil || schema.Validate(inst) != nil {
			quoted, mErr := json.Marshal(text)
			if mErr != nil {
				return json.RawMessage(`""`)
			}
			return quoted
		}
	}
	return json.RawMessage(text)
}

// compileSchema compiles a tool's JSON Schema, or answers nil when it cannot
// be: such a tool's inputs are then only checked to be JSON.
func compileSchema(raw json.RawMessage) *jsonschema.Schema {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("tool.json", doc); err != nil {
		return nil
	}
	s, err := c.Compile("tool.json")
	if err != nil {
		return nil
	}
	return s
}

// inputSchema maps a JSON Schema object onto the SDK's tool schema: its
// properties and required list, and every other keyword as an extra field.
func inputSchema(raw json.RawMessage) anthropic.BetaToolInputSchemaParam {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	out := anthropic.BetaToolInputSchemaParam{}
	for k, v := range fields {
		switch k {
		case "type":
		case "properties":
			out.Properties = v
		case "required":
			var req []string
			if json.Unmarshal(v, &req) == nil {
				out.Required = req
			}
		default:
			if out.ExtraFields == nil {
				out.ExtraFields = map[string]any{}
			}
			out.ExtraFields[k] = v
		}
	}
	return out
}

// anthropicMessages maps the neutral transcript onto Messages: the person's
// turns as text, an assistant turn as the provider's own blocks when it has
// them and as text and tool_use otherwise, and the results of one turn's tool
// calls together in one user message.
func anthropicMessages(msgs []Message) []anthropic.BetaMessageParam {
	out := make([]anthropic.BetaMessageParam, 0, len(msgs))
	var results []anthropic.BetaContentBlockParamUnion
	flush := func() {
		if len(results) > 0 {
			out = append(out, anthropic.NewBetaUserMessage(results...))
			results = nil
		}
	}
	for _, m := range msgs {
		if m.Role == RoleTool {
			results = append(results, anthropic.NewBetaToolResultBlock(m.ToolCallID, m.Text, false))
			continue
		}
		flush()
		switch m.Role {
		case RoleAssistant:
			if blocks := assistantBlocks(m); len(blocks) > 0 {
				out = append(out, anthropic.BetaMessageParam{Role: anthropic.BetaMessageParamRoleAssistant, Content: blocks})
			}
		default:
			out = append(out, anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(m.Text)))
		}
	}
	flush()
	return out
}

// assistantBlocks are an assistant turn's content blocks. The provider's own
// record is replayed byte for byte, because a thinking block is valid only
// unchanged; a turn without one (an OpenAI-era turn, or one a summary
// replaced) is rebuilt from its text and tool calls.
func assistantBlocks(m Message) []anthropic.BetaContentBlockParamUnion {
	if len(m.Provider) > 0 {
		var raw []json.RawMessage
		if json.Unmarshal(m.Provider, &raw) == nil && len(raw) > 0 {
			blocks := make([]anthropic.BetaContentBlockParamUnion, len(raw))
			for i, r := range raw {
				blocks[i] = param.Override[anthropic.BetaContentBlockParamUnion](r)
			}
			return blocks
		}
	}
	var blocks []anthropic.BetaContentBlockParamUnion
	if m.Text != "" {
		blocks = append(blocks, anthropic.NewBetaTextBlock(m.Text))
	}
	for _, c := range m.ToolCalls {
		input := json.RawMessage(`{}`)
		var obj map[string]json.RawMessage
		if json.Unmarshal(c.Arguments, &obj) == nil && obj != nil {
			input = c.Arguments
		}
		blocks = append(blocks, anthropic.NewBetaToolUseBlock(c.ID, input, c.Name))
	}
	return blocks
}

// mapAnthropicError maps the SDK's error onto this package's typed errors, so
// the labels a consumer records are the ones the OpenAI provider's are.
func mapAnthropicError(ctx context.Context, err error) error {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) && ctx.Err() == nil {
		if apiErr.StatusCode >= 200 && apiErr.StatusCode < 300 {
			// An error event on a stream that opened: the body is not the
			// answer its status promised.
			return aiDecodeError{}
		}
		return aiStatusError{status: apiErr.StatusCode}
	}
	var syntax *json.SyntaxError
	var typed *json.UnmarshalTypeError
	if ctx.Err() == nil && (errors.As(err, &syntax) || errors.As(err, &typed)) {
		return aiDecodeError{}
	}
	return aiTransportError{err: err}
}
