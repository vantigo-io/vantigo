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
// A nil *Client is the unavailable state. New answers it for any configuration
// without a provider, a key and a model, so an installation that has no AI
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
	"net/http"
	"slices"

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
// resolver, or returns nil when cfg names no provider, no key or no model.
func New(cfg *config.Config, transport http.RoundTripper, resolver Resolver) *Client {
	if cfg == nil || cfg.AIProvider == "" || cfg.AIAPIKey == "" || len(cfg.AIModels) == 0 {
		return nil
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	var chat chatProvider
	switch cfg.AIProvider {
	case ProviderOpenAI:
		chat = newOpenAI(cfg.AIAPIKey, cfg.AIBaseURL, transport)
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

// Provider is the provider's name, "openai". It is a fact the AI models page
// states; the key is not one and has no accessor.
func (c *Client) Provider() string { return c.provider }

// Models are the models the plan allows, the first being the plan's default.
// The slice is a copy.
func (c *Client) Models() []string { return slices.Clone(c.models) }

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

	var model string
	if c.resolver == nil {
		model = c.models[0]
	} else {
		var err error
		if model, err = c.resolver.ModelFor(ctx, task); err != nil {
			return out, aiModelUnavailableError{err: err}
		}
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
