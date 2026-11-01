package ai_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/ai"
	"github.com/vantigo-io/vantigo/server/internal/config"
)

// These tests drive the real client through a fake http.RoundTripper, the way
// every consumer's tests do (see the package comment: the provider is the
// boundary, so the network is what is replaced, never the client).

type call struct {
	URL           string
	Method        string
	Authorization string
	ContentType   string
	Model         string `json:"model"`
	Messages      []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

type fakeTransport struct {
	mu      sync.Mutex
	calls   []call
	respond func(*http.Request) (*http.Response, error)
}

func (f *fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	var c call
	if r.Body != nil {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &c)
	}
	c.URL, c.Method = r.URL.String(), r.Method
	c.Authorization, c.ContentType = r.Header.Get("Authorization"), r.Header.Get("Content-Type")
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
	if f.respond != nil {
		return f.respond(r)
	}
	return reply(http.StatusOK, `{"choices":[{"message":{"content":"hello"}}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`), nil
}

func (f *fakeTransport) Calls() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func reply(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}

func configured(models ...string) *config.Config {
	if len(models) == 0 {
		models = []string{"gpt-4o-mini", "gpt-4o"}
	}
	return &config.Config{AIProvider: "openai", AIAPIKey: "sk-test-key", AIModels: models}
}

// choose is a Resolver answering a fixed model per task.
type choose map[string]string

func (c choose) ModelFor(_ context.Context, task string) (string, error) {
	if m, ok := c[task]; ok {
		return m, nil
	}
	return "", errors.New("no such task: " + task)
}

type failing struct{}

func (failing) ModelFor(context.Context, string) (string, error) {
	return "", errors.New("database is down, password=hunter2")
}

func typeName(err error) string {
	name := strings.TrimPrefix(fmt.Sprintf("%T", err), "*")
	return name[strings.LastIndex(name, ".")+1:]
}

func TestNew_IsNilUnlessAProviderAKeyAndAModelAreConfigured(t *testing.T) {
	for name, cfg := range map[string]*config.Config{
		"no config":   nil,
		"no provider": {AIAPIKey: "sk", AIModels: []string{"m"}},
		"no key":      {AIProvider: "openai", AIModels: []string{"m"}},
		"no model":    {AIProvider: "openai", AIAPIKey: "sk"},
		"an unknown provider": {
			AIProvider: "mystery", AIAPIKey: "sk", AIModels: []string{"m"},
		},
	} {
		if c := ai.New(cfg, nil, nil); c != nil {
			t.Errorf("%s: New returned a client, want nil (the unavailable state)", name)
		}
	}
	c := ai.New(configured(), nil, nil)
	if c == nil {
		t.Fatal("New returned nil for a complete configuration")
	}
	if c.Provider() != "openai" || !slices.Equal(c.Models(), []string{"gpt-4o-mini", "gpt-4o"}) {
		t.Errorf("provider/models = %q/%v", c.Provider(), c.Models())
	}
	c.Models()[0] = "changed"
	if c.Models()[0] != "gpt-4o-mini" {
		t.Error("Models hands out the client's own slice, want a copy")
	}
}

func TestComplete_SendsOneUserMessageToTheChatEndpointWithTheResolvedModel(t *testing.T) {
	tr := &fakeTransport{}
	c := ai.New(configured(), tr, choose{"mail.draft": "gpt-4o"})

	got, err := c.Complete(context.Background(), "mail.draft", ai.Request{Prompt: "Write a reply."})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got.Text != "hello" || got.Model != "gpt-4o" || got.Provider != "openai" {
		t.Errorf("response = %+v, want text hello from openai/gpt-4o", got)
	}
	if got.InputTokens == nil || *got.InputTokens != 11 || got.OutputTokens == nil || *got.OutputTokens != 7 {
		t.Errorf("tokens = %v/%v, want 11/7", got.InputTokens, got.OutputTokens)
	}

	calls := tr.Calls()
	if len(calls) != 1 {
		t.Fatalf("%d requests, want 1", len(calls))
	}
	r := calls[0]
	if r.URL != "https://api.openai.com/v1/chat/completions" || r.Method != http.MethodPost {
		t.Errorf("request = %s %s, want POST to the chat completions endpoint", r.Method, r.URL)
	}
	if r.Authorization != "Bearer sk-test-key" || r.ContentType != "application/json" {
		t.Errorf("headers = %q / %q", r.Authorization, r.ContentType)
	}
	if r.Model != "gpt-4o" {
		t.Errorf("model on the wire = %q, want the one the resolver chose for the task", r.Model)
	}
	if len(r.Messages) != 1 || r.Messages[0].Role != "user" || r.Messages[0].Content != "Write a reply." {
		t.Errorf("messages = %+v, want exactly one user message and no system message", r.Messages)
	}
}

func TestComplete_TheBaseURLIsTheConfiguredOne(t *testing.T) {
	tr := &fakeTransport{}
	cfg := configured()
	cfg.AIBaseURL = "https://llm.example.com/openai/v1"
	if _, err := ai.New(cfg, tr, nil).Complete(context.Background(), "t", ai.Request{Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	if got := tr.Calls()[0].URL; got != "https://llm.example.com/openai/v1/chat/completions" {
		t.Errorf("URL = %q, want the base URL joined with the chat completions path", got)
	}
}

func TestComplete_ANilResolverGivesEveryTaskThePlanDefault(t *testing.T) {
	tr := &fakeTransport{}
	got, err := ai.New(configured("first", "second"), tr, nil).Complete(context.Background(), "any.task", ai.Request{Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "first" || tr.Calls()[0].Model != "first" {
		t.Errorf("model = %q / %q, want the first allowed model", got.Model, tr.Calls()[0].Model)
	}
}

func TestComplete_RefusesAModelTheResolverNamesOutsideTheAllowedList(t *testing.T) {
	tr := &fakeTransport{}
	c := ai.New(configured(), tr, choose{"t": "gpt-5-unlisted"})
	got, err := c.Complete(context.Background(), "t", ai.Request{Prompt: "p"})
	if err == nil {
		t.Fatal("Complete succeeded, want a model outside AI_MODELS refused")
	}
	if name := typeName(err); name != "aiModelNotAllowedError" {
		t.Errorf("error type = %s, want aiModelNotAllowedError", name)
	}
	if n := len(tr.Calls()); n != 0 {
		t.Errorf("%d requests were made, want none: the key must not travel for a model the plan does not allow", n)
	}
	if got.Provider != "openai" || got.Model != "gpt-5-unlisted" {
		t.Errorf("response = %+v, want provider and model named for the audit row", got)
	}
}

func TestComplete_AResolverFailureIsATypedErrorThatQuotesNothing(t *testing.T) {
	tr := &fakeTransport{}
	got, err := ai.New(configured(), tr, failing{}).Complete(context.Background(), "t", ai.Request{Prompt: "p"})
	if err == nil {
		t.Fatal("Complete succeeded, want the resolver's failure")
	}
	if typeName(err) != "aiModelUnavailableError" {
		t.Errorf("error type = %s, want aiModelUnavailableError", typeName(err))
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("error %q quotes the cause", err)
	}
	if len(tr.Calls()) != 0 || got.Model != "" || got.Provider != "openai" {
		t.Errorf("calls = %d, response = %+v, want no call and no model", len(tr.Calls()), got)
	}
}

func TestComplete_EveryFailureIsOneOfTheNamedTypes(t *testing.T) {
	transportFailure := errors.New("dial tcp: lookup api.openai.com: no such host")
	for _, tc := range []struct {
		name    string
		respond func(*http.Request) (*http.Response, error)
		want    string
	}{
		{"a dial failure", func(*http.Request) (*http.Response, error) { return nil, transportFailure }, "aiTransportError"},
		{"a 500", func(*http.Request) (*http.Response, error) { return reply(500, `{"error":"upstream exploded"}`), nil }, "aiStatusError"},
		{"a redirect is not followed and is a status", func(*http.Request) (*http.Response, error) {
			r := reply(302, "")
			r.Header.Set("Location", "https://evil.example/steal")
			return r, nil
		}, "aiStatusError"},
		{"a body that is not JSON", func(*http.Request) (*http.Response, error) { return reply(200, `<html>`), nil }, "aiDecodeError"},
		{"no choice", func(*http.Request) (*http.Response, error) { return reply(200, `{"choices":[]}`), nil }, "aiEmptyResponseError"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := &fakeTransport{respond: tc.respond}
			got, err := ai.New(configured(), tr, nil).Complete(context.Background(), "t", ai.Request{Prompt: "p"})
			if err == nil {
				t.Fatal("Complete succeeded, want an error")
			}
			if typeName(err) != tc.want {
				t.Errorf("error type = %s, want %s", typeName(err), tc.want)
			}
			if strings.Contains(err.Error(), "upstream exploded") || strings.Contains(err.Error(), "evil.example") {
				t.Errorf("error %q quotes the provider", err)
			}
			if got.Model != "gpt-4o-mini" || got.Provider != "openai" {
				t.Errorf("response = %+v, want the model named even though the call failed", got)
			}
			if len(tr.Calls()) != 1 {
				t.Errorf("%d requests, want exactly one: a redirect must not be followed", len(tr.Calls()))
			}
		})
	}
}

func TestComplete_ATransportFailureKeepsItsCauseForErrorsIs(t *testing.T) {
	tr := &fakeTransport{respond: func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() }}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ai.New(configured(), tr, nil).Complete(ctx, "t", ai.Request{Prompt: "p"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want it to wrap context.Canceled so a vanished caller is not recorded as an outage", err)
	}
}

func TestComplete_AnUnreportedOrOversizedTokenCountIsAbsent(t *testing.T) {
	tr := &fakeTransport{respond: func(*http.Request) (*http.Response, error) {
		return reply(200, `{"choices":[{"message":{"content":"x"}}],"usage":{"prompt_tokens":99999999999}}`), nil
	}}
	got, err := ai.New(configured(), tr, nil).Complete(context.Background(), "t", ai.Request{Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if got.InputTokens != nil || got.OutputTokens != nil {
		t.Errorf("tokens = %v/%v, want both absent", got.InputTokens, got.OutputTokens)
	}
}
