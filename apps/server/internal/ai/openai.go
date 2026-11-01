package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

const (
	// ProviderOpenAI is the provider this build implements, and the value
	// AI_PROVIDER and ai_interactions.provider carry.
	ProviderOpenAI = "openai"
	// ProviderOpenAICompatible is ProviderOpenAI under a name that reads better
	// beside a base URL that is not OpenAI's: Ollama, vLLM, LM Studio. It is the
	// same implementation; the name the operator set is the one reported.
	ProviderOpenAICompatible = "openai-compatible"
	// defaultOpenAIBaseURL is the chat-completions host, used unless
	// AI_BASE_URL names an OpenAI-compatible endpoint.
	defaultOpenAIBaseURL  = "https://api.openai.com/v1"
	chatCompletionsPath   = "/chat/completions"
	openAIRequestTimeout  = 30 * time.Second
	openAIResponseMaxByte = 4 << 20
)

// openAI is the OpenAI chat-completions implementation, moved here from
// Communications' ai_client.go unchanged on the wire: one POST with a model
// and a single user message, no system message.
type openAI struct {
	// apiKey is empty for a local model that takes none; no Authorization
	// header is sent then.
	apiKey  string
	baseURL string
	client  *http.Client
	// chatTimeout is Chat's budget (AI_TIMEOUT); complete keeps its own.
	chatTimeout time.Duration
}

func newOpenAI(apiKey, baseURL string, transport http.RoundTripper, chatTimeout time.Duration) *openAI {
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL
	}
	return &openAI{
		apiKey:      apiKey,
		baseURL:     baseURL,
		chatTimeout: chatTimeout,
		// Redirects are never followed, for the same reason the brreg client
		// does not follow them: a redirect would re-send this request — API
		// key included — to a host no operator named.
		client: &http.Client{
			Transport:     transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// complete sends prompt as a single user message and returns the completion.
// The call is bounded: an unbounded call to a third party would hold an
// inbound request open for as long as the provider cared to stall it.
func (o *openAI) complete(ctx context.Context, model, prompt string) (completion, error) {
	ctx, cancel := context.WithTimeout(ctx, openAIRequestTimeout)
	defer cancel()

	payload, err := json.Marshal(map[string]any{
		"model":    model,
		"messages": []any{map[string]any{"role": "user", "content": prompt}},
	})
	if err != nil {
		return completion{}, aiDecodeError{}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+chatCompletionsPath, bytes.NewReader(payload))
	if err != nil {
		return completion{}, aiTransportError{err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	o.authorize(req)

	resp, err := o.client.Do(req)
	if err != nil {
		return completion{}, aiTransportError{err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, openAIResponseMaxByte))
	if err != nil {
		return completion{}, aiTransportError{err: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return completion{}, aiStatusError{status: resp.StatusCode}
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
		return completion{}, aiDecodeError{}
	}
	if len(parsed.Choices) == 0 {
		return completion{}, aiEmptyResponseError{}
	}

	out := completion{text: parsed.Choices[0].Message.Content}
	if parsed.Usage != nil {
		out.inputTokens = tokenCount(parsed.Usage.PromptTokens)
		out.outputTokens = tokenCount(parsed.Usage.CompletionTokens)
	}
	return out, nil
}

// authorize sets the bearer credential, when there is one.
func (o *openAI) authorize(req *http.Request) {
	if o.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
	}
}

// tokenCount narrows a reported token count to the int32 an audit column
// stores. A count that large is not reachable from any real provider, and
// losing a consumer's whole audit row over one would be the worse trade, so an
// unrepresentable count is reported as absent instead.
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
