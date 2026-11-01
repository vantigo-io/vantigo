// Package ai is the platform's AI provider: the one client for the provider the
// instance is configured with (AI_PROVIDER, AI_API_KEY, AI_BASE_URL), the one
// place the key lives, and the one door every module's AI work goes through.
// Like mail and payments it is a platform package: no SQL and no module import.
//
// A module never names a model or holds a key. It calls Client.Complete with a
// task key it declared (contracts.AITask, module.Module.AITasks) and a
// provider-neutral Request; the client asks its Resolver which model the task
// is to use, refuses one that AI_MODELS does not allow, and calls the provider
// under the guard every outbound client in this repository has: no redirect is
// followed, the transport is the one the host hands every module
// (Deps.HTTPTransport), and the call has a budget. The keys and the allowed
// models are instance configuration; which allowed model a task uses is the
// workspace's, chosen by an Owner and kept by identity (contracts.AIModels).
//
// Client.Chat is the second door, for a conversation: a system message, typed
// messages and JSON Schema tools, streamed, against any OpenAI-compatible
// endpoint — OpenAI, or a local model served by Ollama, vLLM or LM Studio,
// which needs no key. It is the same guard under a longer budget (AI_TIMEOUT).
//
// A nil *Client is the unavailable state. New answers it for any configuration
// without a provider, a model, and a key or a base URL, so an installation that has no AI
// constructs no network client and every caller's availability check is one
// nil test.
//
// # Why a real client and a fake transport
//
// The provider is a genuine external-network boundary, and replacing the
// network is dependency injection at that boundary. Replacing the client with a
// test double would instead move this package's own request building, status
// handling, response decoding and token counting out of the tested path — a
// seam that keeps passing after the production implementation is deleted,
// which is the definition of a blind one. Every consumer's tests drive this
// package for real through a fake http.RoundTripper.
package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/config"
)

// Request is one completion asked of the provider, in terms no provider owns.
// Today that is a prompt answered with text; a system message, a list of
// messages or a response format join it when a task needs them, and a second
// provider maps them in its own file.
type Request struct {
	// Prompt is sent as a single user message, with no system message.
	Prompt string
}

// Response is what the provider answered, or — with an error — what is known
// of the call that failed.
type Response struct {
	// Text is the completion: the content of the provider's single choice.
	Text string
	// InputTokens and OutputTokens are the counts the provider reported, nil
	// when it reported none or one that does not fit an int32.
	InputTokens  *int32
	OutputTokens *int32
	// Provider and Model name what served the call. Both are set whenever the
	// model was resolved, also on an error, so a consumer's audit row can say
	// which model it asked even when the call failed.
	Provider string
	Model    string
}

// Role says who wrote a Message. The system message is not one of them: it is
// ChatRequest.System, so a provider that keeps it elsewhere than in the list
// (Anthropic's separate field) maps it in its own file.
type Role string

const (
	// RoleUser is the person's message.
	RoleUser Role = "user"
	// RoleAssistant is the model's earlier answer: its text, its tool calls, or both.
	RoleAssistant Role = "assistant"
	// RoleTool is the result of one ToolCall, named by ToolCallID.
	RoleTool Role = "tool"
)

// Message is one turn of a chat, in terms no provider owns.
type Message struct {
	Role Role
	// Text is the message's content: what the person wrote, what the model
	// said (possibly empty beside ToolCalls), or a tool's result.
	Text string
	// ToolCalls are the calls an assistant message asked for.
	ToolCalls []ToolCall
	// ToolCallID names the ToolCall a RoleTool message answers.
	ToolCallID string
}

// Tool is a function the model may call, described by a JSON Schema.
type Tool struct {
	Name        string
	Description string
	// Parameters is the JSON Schema of the arguments, an object schema. Empty
	// means a function that takes none.
	Parameters json.RawMessage
}

// ToolCall is one call the model asked for.
type ToolCall struct {
	// ID pairs the call with the RoleTool message that answers it.
	ID   string
	Name string
	// Arguments is always valid JSON, normally an object. Text the model sent
	// that is not JSON (local models do) is carried as a JSON string instead,
	// so a caller can see it is not an object and tell the model so.
	Arguments json.RawMessage
}

// ChatRequest is one chat turn asked of the provider.
type ChatRequest struct {
	System   string
	Messages []Message
	Tools    []Tool
	// MaxTokens bounds the answer; zero leaves it to the provider.
	MaxTokens int
}

// ChatResponse is the model's answer, or — with an error — what is known of
// the call that failed.
type ChatResponse struct {
	// Message is the assistant's answer: its text and the tool calls it asks for.
	Message Message
	// FinishReason is the provider's own word for why the answer ended
	// ("stop", "tool_calls", "length"). Servers differ — Ollama says "stop"
	// beside tool calls — so a caller decides by Message.ToolCalls.
	FinishReason string
	// InputTokens and OutputTokens are as Response's: nil when the provider
	// reported none, as Ollama's compatible endpoint may not.
	InputTokens  *int32
	OutputTokens *int32
	// Provider and Model name what served the call, set whenever the model
	// was resolved, also on an error.
	Provider string
	Model    string
}

// DefaultChatTimeout is the budget of one Chat call when the configuration
// names none (AI_TIMEOUT's default): local models are slow.
const DefaultChatTimeout = 120 * time.Second

// Resolver answers which model a task is to use. Identity's table of choices
// is the production one (contracts.AIModels satisfies it structurally); a nil
// Resolver gives every task the plan's default, the first of AI_MODELS.
type Resolver interface {
	ModelFor(ctx context.Context, task string) (string, error)
}

// chatProvider is one provider's implementation: given the model Complete
// resolved and allowed, it makes the call and answers a completion or one of
// this package's typed errors. A second provider is a second implementation.
type chatProvider interface {
	complete(ctx context.Context, model, prompt string) (completion, error)
	// chat streams a conversation turn: onDelta (which may be nil) receives
	// each text delta as it arrives, and the answer is the assembled message.
	// The result's Provider and Model are Chat's to fill.
	chat(ctx context.Context, model string, req ChatRequest, onDelta func(text string)) (ChatResponse, error)
}

// completion is a provider's answer before Complete adds what it knows.
type completion struct {
	text         string
	inputTokens  *int32
	outputTokens *int32
}

// Client is the configured provider. Build it with New; the zero value is not
// usable and a nil *Client is the unavailable state.
type Client struct {
	provider string
	models   []string
	resolver Resolver
	chat     chatProvider
}

// New builds the client for cfg over transport (Deps.HTTPTransport: nil in
// production, meaning http.DefaultTransport; a fake in every test) and
// resolver, or returns nil when cfg names no provider, no model, or no key
// where an endpoint that needs one is configured. A key is optional once
// AI_BASE_URL names an endpoint (config.Load has already refused a key over
// plain http): Ollama, vLLM and LM Studio take none.
func New(cfg *config.Config, transport http.RoundTripper, resolver Resolver) *Client {
	if cfg == nil || cfg.AIProvider == "" || len(cfg.AIModels) == 0 {
		return nil
	}
	if cfg.AIAPIKey == "" && cfg.AIBaseURL == "" {
		return nil
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	var chat chatProvider
	switch cfg.AIProvider {
	case ProviderOpenAI, ProviderOpenAICompatible:
		timeout := cfg.AITimeout
		if timeout <= 0 {
			timeout = DefaultChatTimeout
		}
		chat = newOpenAI(cfg.AIAPIKey, cfg.AIBaseURL, transport, timeout)
	default:
		// config accepts only the providers implemented here, so this is a
		// configuration this build cannot serve: unavailable, never a panic.
		return nil
	}
	return &Client{
		provider: cfg.AIProvider,
		models:   slices.Clone(cfg.AIModels),
		resolver: resolver,
		chat:     chat,
	}
}

// Provider is the provider's name as the operator set it, "openai" or
// "openai-compatible". It is a fact the AI models page states; the key is not
// one and has no accessor.
func (c *Client) Provider() string { return c.provider }

// Models are the models the plan allows, the first being the plan's default.
// The slice is a copy.
func (c *Client) Models() []string { return slices.Clone(c.models) }

// resolve is the model the resolver names for task, allowed or not.
func (c *Client) resolve(ctx context.Context, task string) (string, error) {
	if c.resolver == nil {
		return c.models[0], nil
	}
	model, err := c.resolver.ModelFor(ctx, task)
	if err != nil {
		return "", aiModelUnavailableError{err: err}
	}
	return model, nil
}

// ModelFor is the model Complete would use for task now: what the resolver
// names, provided the plan allows it. A consumer that records which model a
// call was for even when it never makes one (a guard that refuses first) asks
// here; Complete's own Response is authoritative for a call that was made.
func (c *Client) ModelFor(ctx context.Context, task string) (string, error) {
	model, err := c.resolve(ctx, task)
	if err != nil {
		return "", err
	}
	if !slices.Contains(c.models, model) {
		return "", aiModelNotAllowedError{}
	}
	return model, nil
}

// Complete asks the provider for task's completion. The model is whatever the
// Resolver answers for task, and a model outside Models is refused before any
// request is made. Every failure is one of this package's typed errors — a
// resolver failure or refusal, a transport failure, a non-2xx status, an
// undecodable body or an empty answer — whose type names are short labels a
// caller may record without recording anything the provider or the person said.
// The returned Response names the provider, and the model once it is resolved,
// whatever the outcome.
func (c *Client) Complete(ctx context.Context, task string, req Request) (Response, error) {
	out := Response{Provider: c.provider}

	model, err := c.resolve(ctx, task)
	if err != nil {
		return out, err
	}
	out.Model = model
	if !slices.Contains(c.models, model) {
		return out, aiModelNotAllowedError{}
	}

	done, err := c.chat.complete(ctx, model, req.Prompt)
	if err != nil {
		return out, err
	}
	out.Text, out.InputTokens, out.OutputTokens = done.text, done.inputTokens, done.outputTokens
	return out, nil
}

// Chat asks the provider for the next turn of a conversation: the system
// message, the messages so far and the tools the model may call. The answer
// streams — onDelta (which may be nil) receives each text delta as it
// arrives, from the goroutine that called Chat — and the returned ChatResponse
// carries the assembled assistant message with its tool calls and the token
// counts. The model is resolved and allowed exactly as Complete's is, the
// call is under the same guard (no redirect, Deps.HTTPTransport) and the
// failures are the same typed errors; a stream that ends before the provider
// said it was done is a decode error, the budget is AI_TIMEOUT for the whole
// call, and the returned ChatResponse names the provider and the model once
// resolved whatever the outcome.
func (c *Client) Chat(ctx context.Context, task string, req ChatRequest, onDelta func(text string)) (ChatResponse, error) {
	out := ChatResponse{Provider: c.provider}

	model, err := c.resolve(ctx, task)
	if err != nil {
		return out, err
	}
	out.Model = model
	if !slices.Contains(c.models, model) {
		return out, aiModelNotAllowedError{}
	}

	done, err := c.chat.chat(ctx, model, req, onDelta)
	if err != nil {
		return out, err
	}
	done.Provider, done.Model = out.Provider, out.Model
	return done, nil
}
