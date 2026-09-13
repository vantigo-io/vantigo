package communications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/config"
)

// This file is the AI feature's provider boundary: the chat client
// CommunicationsAiOptions configures and DB/CommunicationsDatabaseConfiguration.cs:45-57
// registers (communications inventory §17.1). It is the module's second
// outbound HTTP dependency after none — customers' brreg client is the
// project's precedent and this follows its shape: base URL and credential
// from config, transport from Deps.HTTPTransport (nil in production, meaning
// http.DefaultTransport; a fake in every test, so no test opens a real
// socket).
//
// Why a real client here rather than an injectable chat interface: the
// provider is a genuine external-network boundary, and replacing the network
// is dependency injection at that boundary. Replacing the *client* with a
// test double would instead move this file's own request building, status
// handling, response decoding and token counting out of the tested path —
// a seam that keeps passing after the production implementation is deleted,
// which is the definition of a blind one. Every AI test drives this file for
// real through a fake RoundTripper (ai_test.go's fakeChatTransport).

const (
	// aiProviderName is the one provider this port implements, and the value
	// ai_interactions.provider always carries — hardcoded in .NET too
	// (SV/CommunicationsAiService.cs:214-226), independent of what the
	// operator configured.
	aiProviderName = "openai"
	// aiDefaultModel is CommunicationsAiOptions.Model's default (:37).
	aiDefaultModel = "gpt-4o-mini"
	// aiModelMaxChars is .NET's Limit(Options.Model.Trim(), 150) (:37), which
	// is also ai_interactions.model's column width.
	aiModelMaxChars = 150
	// aiBaseURL is the OpenAI chat-completions host. Not configurable: .NET
	// exposes no base-URL option either, and tests replace the transport
	// rather than the address.
	aiBaseURL             = "https://api.openai.com/v1"
	aiChatCompletionsPath = "/chat/completions"
	// aiRequestTimeout bounds one completion end to end. .NET sets no
	// explicit timeout here; this port adds one deliberately, because an
	// unbounded call to a third party would hold an inbound request open for
	// as long as the provider cared to stall it.
	aiRequestTimeout = 30 * time.Second
)

// The AI call's failure modes, as distinct types. .NET records
// `exception.GetType().Name` in ai_interactions.error_summary and never the
// message (inventory §17.2 step 11, §17.5); errorTypeName does the same with
// these, so the audit row stays a short, non-identifying label. Every error
// complete returns is one of these, so that label is always meaningful —
// a bare *url.Error would reduce to the useless name "Error".
type (
	// aiTransportError is a request that never produced a response: a dial
	// failure, a timeout, a cancelled context, or an unreadable body. It
	// keeps the underlying cause so callers can tell a cancellation apart
	// from a genuine provider failure with errors.Is — see callerGaveUp in
	// ai.go, which must not record a vanished caller as an outage. The cause
	// is never rendered into error_summary: errorTypeName reads the type, not
	// the message.
	aiTransportError struct{ err error }
	// aiStatusError is a response with a non-2xx status.
	aiStatusError struct{ status int }
	// aiDecodeError is a 2xx response whose body is not the shape the
	// provider's API documents.
	aiDecodeError struct{}
	// aiEmptyResponseError is a well-formed response carrying no choice, so
	// there is no completion text to draft or parse from.
	aiEmptyResponseError struct{}
)

func (aiTransportError) Error() string { return "communications: the AI provider could not be reached" }

// Unwrap exposes the cause to errors.Is only; nothing ever formats it into a
// persisted or returned string.
func (e aiTransportError) Unwrap() error { return e.err }
func (e aiStatusError) Error() string {
	return fmt.Sprintf("communications: the AI provider responded %d", e.status)
}
func (aiDecodeError) Error() string {
	return "communications: the AI provider's response could not be decoded"
}
func (aiEmptyResponseError) Error() string {
	return "communications: the AI provider returned no completion"
}

// aiChatClient is the configured provider. A nil *aiChatClient is the
// unavailable state: .NET registers no chat client at all when the feature is
// disabled, the provider is not "openai", or the key is blank, and
// IsAvailable() then answers false (inventory §17.1). Both operations check
// for nil and answer 503 ai_unavailable, so an unconfigured deployment never
// constructs a network client, and never panics.
type aiChatClient struct {
	model  string
	apiKey string
	client *http.Client
}

// newAIChatClient builds the client from cfg, or returns nil when the three
// option checks .NET's registration performs do not all pass. transport is
// Deps.HTTPTransport: nil in production, a fake in tests.
func newAIChatClient(cfg *config.Config, transport http.RoundTripper) *aiChatClient {
	if cfg == nil || !cfg.CommunicationsAIEnabled {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(cfg.CommunicationsAIProvider), aiProviderName) {
		return nil
	}
	apiKey := strings.TrimSpace(cfg.CommunicationsAIAPIKey)
	if apiKey == "" {
		return nil
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	model := strings.TrimSpace(cfg.CommunicationsAIModel)
	if model == "" {
		model = aiDefaultModel
	}
	return &aiChatClient{
		model:  limitUTF16(model, aiModelMaxChars),
		apiKey: apiKey,
		// Redirects are never followed, for the same reason brregClient does
		// not follow them: a redirect would re-send this request — API key
		// included — to a host no operator named.
		client: &http.Client{
			Transport:     transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// chatCompletion is one provider answer: the text of its single choice and
// the token counts the interaction row records.
type chatCompletion struct {
	text         string
	inputTokens  *int32
	outputTokens *int32
}

// complete sends prompt as a single user message and returns the completion.
// .NET sends exactly one ChatRole.User message with no system message
// (inventory §17.2 step 8), and so does this.
func (c *aiChatClient) complete(ctx context.Context, prompt string) (chatCompletion, error) {
	ctx, cancel := context.WithTimeout(ctx, aiRequestTimeout)
	defer cancel()

	payload, err := json.Marshal(map[string]any{
		"model":    c.model,
		"messages": []any{map[string]any{"role": "user", "content": prompt}},
	})
	if err != nil {
		return chatCompletion{}, aiDecodeError{}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, aiBaseURL+aiChatCompletionsPath, bytes.NewReader(payload))
	if err != nil {
		return chatCompletion{}, aiTransportError{err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return chatCompletion{}, aiTransportError{err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return chatCompletion{}, aiTransportError{err: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return chatCompletion{}, aiStatusError{status: resp.StatusCode}
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     *int64 `json:"prompt_tokens"`
			CompletionTokens *int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return chatCompletion{}, aiDecodeError{}
	}
	if len(parsed.Choices) == 0 {
		return chatCompletion{}, aiEmptyResponseError{}
	}

	out := chatCompletion{text: parsed.Choices[0].Message.Content}
	if parsed.Usage != nil {
		out.inputTokens = tokenCount(parsed.Usage.PromptTokens)
		out.outputTokens = tokenCount(parsed.Usage.CompletionTokens)
	}
	return out, nil
}

// tokenCount narrows a reported token count to the int32 the column stores.
// .NET's `checked((int)…)` would throw on a value that does not fit, failing
// the whole call; a count that large is not reachable from any real provider,
// and losing the audit row's summary over one would be the worse trade, so an
// unrepresentable count is recorded as absent instead.
func tokenCount(v *int64) *int32 {
	if v == nil {
		return nil
	}
	const maxInt32, minInt32 = int64(1)<<31 - 1, -(int64(1) << 31)
	if *v > maxInt32 || *v < minInt32 {
		return nil
	}
	n := int32(*v)
	return &n
}
