package module

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/ai"
	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

func aiTask(module, name string) contracts.AITask {
	return contracts.AITask{Key: module + "." + name, Module: module, Display: "Task " + name, Description: "does " + name}
}

// fakeAIModels is a contracts.AIModels that answers one model for every task
// and remembers the tasks Deps carried when it was built.
type fakeAIModels struct {
	model string
	tasks map[string]contracts.AITask
}

func (f *fakeAIModels) ModelFor(context.Context, string) (string, error) { return f.model, nil }

// chatTransport answers every request with one choice and remembers the body.
type chatTransport struct{ body string }

func (c *chatTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	b, _ := io.ReadAll(r.Body)
	c.body = string(b)
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`)),
		Header:     http.Header{},
	}, nil
}

func TestCompose_CollectsTheEnabledModulesAITasks(t *testing.T) {
	specs := map[string]string{"alpha": alphaContract, "beta": betaContract}
	var got Deps
	_, err := compose(
		Deps{Access: &fakeAccess{}, Config: &config.Config{Modules: []string{"alpha"}}},
		fakeLoad(specs),
		Module{Name: "alpha", AITasks: []contracts.AITask{aiTask("alpha", "draft"), aiTask("alpha", "tag-it")}, Mount: func(d Deps) (http.Handler, error) {
			got = d
			return staticHandler("alpha")(d)
		}},
		Module{Name: "beta", AITasks: []contracts.AITask{aiTask("beta", "summarise")}, Mount: staticHandler("beta")},
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if len(got.AITasks) != 2 || got.AITasks["alpha.draft"].Display != "Task draft" || got.AITasks["alpha.tag-it"].Module != "alpha" {
		t.Errorf("Deps.AITasks = %v, want alpha's two tasks", got.AITasks)
	}
	if _, ok := got.AITasks["beta.summarise"]; ok {
		t.Error("Deps.AITasks holds a disabled module's task, want it to vanish with the module's permissions")
	}
}

func TestCompose_RefusesAnInvalidOrDuplicateAITask(t *testing.T) {
	specs := map[string]string{"alpha": alphaContract}
	for name, tasks := range map[string][]contracts.AITask{
		"a duplicate key":             {aiTask("alpha", "draft"), aiTask("alpha", "draft")},
		"another module's prefix":     {aiTask("beta", "draft")},
		"a key that is not a task":    {{Key: "alpha", Module: "alpha", Display: "d", Description: "d"}},
		"upper case":                  {{Key: "alpha.Draft", Module: "alpha", Display: "d", Description: "d"}},
		"a module that is not hers":   {{Key: "alpha.draft", Module: "beta", Display: "d", Description: "d"}},
		"a blank display":             {{Key: "alpha.draft", Module: "alpha", Display: " ", Description: "d"}},
		"a blank description":         {{Key: "alpha.draft", Module: "alpha", Display: "d"}},
		"a task without a dot in key": {{Key: "alpha-draft", Module: "alpha", Display: "d", Description: "d"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := compose(Deps{Access: &fakeAccess{}}, fakeLoad(specs),
				Module{Name: "alpha", AITasks: tasks, Mount: staticHandler("alpha")})
			if err == nil {
				t.Fatal("compose succeeded, want the task refused")
			}
		})
	}
}

func TestCompose_ResolvesTheAIModelsSlotAndBuildsTheClientOverIt(t *testing.T) {
	specs := map[string]string{"alpha": alphaContract, "beta": betaContract}
	cfg := &config.Config{Modules: []string{"alpha", "beta"}, AIProvider: "openai", AIAPIKey: "sk-test", AIModels: []string{"small", "large"}}
	provider := &fakeAIModels{model: "large"}
	tr := &chatTransport{}
	var got Deps
	_, err := compose(Deps{Access: &fakeAccess{}, Config: cfg, HTTPTransport: tr}, fakeLoad(specs),
		Module{Name: "beta", AITasks: []contracts.AITask{aiTask("beta", "draft")}, Mount: staticHandler("beta"),
			AIModels: func(d Deps) contracts.AIModels {
				provider.tasks = d.AITasks
				return provider
			}},
		Module{Name: "alpha", Mount: func(d Deps) (http.Handler, error) {
			got = d
			return staticHandler("alpha")(d)
		}},
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if got.AIModels != provider {
		t.Errorf("Deps.AIModels = %v, want the provider's", got.AIModels)
	}
	if _, ok := provider.tasks["beta.draft"]; !ok {
		t.Errorf("tasks the provider saw while built = %v, want Deps.AITasks already set", provider.tasks)
	}
	if got.AI == nil {
		t.Fatal("Deps.AI = nil, want a client for a configured provider")
	}
	res, err := got.AI.Complete(context.Background(), "beta.draft", ai.Request{Prompt: "hi"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if res.Model != "large" || !strings.Contains(tr.body, `"model":"large"`) {
		t.Errorf("model = %q, body %s, want the model the AIModels slot chose", res.Model, tr.body)
	}
}

func TestCompose_NoAIProviderMeansNoClient(t *testing.T) {
	var got Deps
	_, err := compose(Deps{Access: &fakeAccess{}, Config: &config.Config{Modules: []string{"alpha"}}}, fakeLoad(map[string]string{"alpha": alphaContract}),
		Module{Name: "alpha", Mount: func(d Deps) (http.Handler, error) {
			got = d
			return staticHandler("alpha")(d)
		}},
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if got.AI != nil {
		t.Errorf("Deps.AI = %v, want nil when AI_PROVIDER is unset", got.AI)
	}
}

func TestCompose_RefusesTwoAIModelsProviders(t *testing.T) {
	provide := func(Deps) contracts.AIModels { return &fakeAIModels{} }
	_, err := compose(Deps{Access: &fakeAccess{}}, fakeLoad(map[string]string{"alpha": alphaContract, "beta": betaContract}),
		Module{Name: "alpha", AIModels: provide, Mount: staticHandler("alpha")},
		Module{Name: "beta", AIModels: provide, Mount: staticHandler("beta")},
	)
	if err == nil || !strings.Contains(err.Error(), "AI models") || !strings.Contains(err.Error(), "alpha") || !strings.Contains(err.Error(), "beta") {
		t.Errorf("compose = %v, want an error naming AI models, alpha and beta", err)
	}
}

func TestCompose_KeepsAPresetAIClient(t *testing.T) {
	preset := ai.New(&config.Config{AIProvider: "openai", AIAPIKey: "sk", AIModels: []string{"m"}}, nil, nil)
	var got Deps
	_, err := compose(Deps{Access: &fakeAccess{}, Config: &config.Config{Modules: []string{"alpha"}}, AI: preset}, fakeLoad(map[string]string{"alpha": alphaContract}),
		Module{Name: "alpha", Mount: func(d Deps) (http.Handler, error) {
			got = d
			return staticHandler("alpha")(d)
		}},
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if got.AI != preset {
		t.Error("Deps.AI is not the preset client")
	}
}
