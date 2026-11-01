package communications_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// These tests pin the module's side of the AI platform (AI as a platform
// provider design T11, T12): each operation is an AI task with a key, calls
// Deps.AI with it and with no model, and records on its interaction row the
// model the platform resolved for that task. The wire, the guards and the
// asymmetries are the rest of ai_test.go's, unchanged.

// taskModels is a contracts.AIModels answering a model per task key.
type taskModels struct {
	byTask map[string]string
	err    error
}

func (m taskModels) ModelFor(_ context.Context, task string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.byTask[task], nil
}

// newTaskHarness is newAIHarness over two allowed models with the choice per
// task pinned to models.
func newTaskHarness(t *testing.T, tr *fakeChatTransport, models taskModels) *modtest.Harness {
	t.Helper()
	return newHarness(t,
		modtest.WithTransport(tr),
		modtest.WithEnv("AI_PROVIDER", "openai"),
		modtest.WithEnv("AI_API_KEY", "sk-test-key"),
		modtest.WithEnv("AI_MODELS", "gpt-4o-mini,gpt-4o"),
		modtest.WithAIModels(models),
	)
}

func interactionModel(t *testing.T, h *modtest.Harness, convID string) (provider, model string, errorSummary *string) {
	t.Helper()
	id := uuid.MustParse(convID)
	provider = modtest.One[string](t, h, `SELECT provider FROM communications.ai_interactions WHERE conversation_id = $1`, id)
	model = modtest.One[string](t, h, `SELECT model FROM communications.ai_interactions WHERE conversation_id = $1`, id)
	errorSummary = modtest.One[*string](t, h, `SELECT error_summary FROM communications.ai_interactions WHERE conversation_id = $1`, id)
	return provider, model, errorSummary
}

// TestAiTasks_EachOperationAsksForItsOwnTasksModel pins that the draft and the
// suggestion are two tasks: each request carries the model chosen for its
// task, the Authorization is the platform's key, and the interaction row names
// that model and the provider.
func TestAiTasks_EachOperationAsksForItsOwnTasksModel(t *testing.T) {
	t.Parallel()
	tr := &fakeChatTransport{respond: func(r *http.Request) (*http.Response, error) {
		return completion(`{"subject":"S","text":"T","customerId":1002,"confidence":0.91,"rationale":"R"}`, 1, 1), nil
	}}
	h := newTaskHarness(t, tr, taskModels{byTask: map[string]string{
		"communications.compose-reply":       "gpt-4o",
		"communications.customer-suggestion": "gpt-4o-mini",
	}})

	draftConv := aiConversation(t, h)
	if r := doDraft(draftClient(t, h), draftConv, draftBody("concise", "Reply.")); r.Status != http.StatusOK {
		t.Fatalf("draft: status %d body %s", r.Status, r.Body)
	}
	suggestConv := suggestable(t, h)
	if r := doSuggest(suggestClient(t, h), suggestConv); r.Status != http.StatusOK {
		t.Fatalf("suggestion: status %d body %s", r.Status, r.Body)
	}

	calls := tr.Calls()
	if len(calls) != 2 {
		t.Fatalf("%d provider calls, want 2", len(calls))
	}
	if calls[0].Model != "gpt-4o" || calls[1].Model != "gpt-4o-mini" {
		t.Errorf("models on the wire = %q, %q, want gpt-4o for the draft and gpt-4o-mini for the suggestion", calls[0].Model, calls[1].Model)
	}
	for _, c := range calls {
		if c.Authorization != "Bearer sk-test-key" {
			t.Errorf("Authorization = %q, want the platform's key", c.Authorization)
		}
	}
	if provider, model, e := interactionModel(t, h, draftConv); provider != "openai" || model != "gpt-4o" || e != nil {
		t.Errorf("draft row = %q/%q/%v, want openai/gpt-4o and no error", provider, model, e)
	}
	if provider, model, e := interactionModel(t, h, suggestConv); provider != "openai" || model != "gpt-4o-mini" || e != nil {
		t.Errorf("suggestion row = %q/%q/%v, want openai/gpt-4o-mini and no error", provider, model, e)
	}
}

// TestAiTasks_AGuardRowNamesTheModelTheTaskWouldHaveUsed pins that a call the
// module refuses before the provider (the protected-customer guard) still
// records which model the task would have asked, and sends nothing.
func TestAiTasks_AGuardRowNamesTheModelTheTaskWouldHaveUsed(t *testing.T) {
	t.Parallel()
	tr := &fakeChatTransport{}
	h := newTaskHarness(t, tr, taskModels{byTask: map[string]string{"communications.customer-suggestion": "gpt-4o"}})
	convID := suggestable(t, h)
	h.Exec(t, `UPDATE communications.conversations SET customer_id = 1001 WHERE id = $1`, uuid.MustParse(convID))

	if r := doSuggest(suggestClient(t, h), convID); r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	if n := len(tr.Calls()); n != 0 {
		t.Errorf("%d provider calls, want none: the guard refuses first", n)
	}
	if _, model, _ := interactionModel(t, h, convID); model != "gpt-4o" {
		t.Errorf("model on the guard row = %q, want gpt-4o", model)
	}
}

// TestAiTasks_AModelOutsideThePlanIsAnAuditedException pins that a resolver
// naming a model AI_MODELS does not allow reaches no provider: the draft
// answers its ordinary 422 ai_failed, and the row says which model it asked and
// which kind of failure it was, never more.
func TestAiTasks_AModelOutsideThePlanIsAnAuditedException(t *testing.T) {
	t.Parallel()
	tr := &fakeChatTransport{}
	h := newTaskHarness(t, tr, taskModels{byTask: map[string]string{"communications.compose-reply": "gpt-5-unlisted"}})
	convID := aiConversation(t, h)

	r := doDraft(draftClient(t, h), convID, draftBody("concise", "Reply."))
	if r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d body %s, want 422 ai_failed", r.Status, r.Body)
	}
	if n := len(tr.Calls()); n != 0 {
		t.Errorf("%d provider calls, want none: the key must not travel for a model the plan does not allow", n)
	}
	_, model, e := interactionModel(t, h, convID)
	if model != "gpt-5-unlisted" || e == nil || *e != "aiModelNotAllowedError" {
		t.Errorf("row = %q / %v, want the asked model and aiModelNotAllowedError", model, e)
	}
}

// TestAiTasks_AChoiceThatCannotBeReadIsAnAuditedExceptionWithNoModel pins the
// other resolver failure: the draft fails as an outage does, the row is still
// written, and it records no model rather than inventing one.
func TestAiTasks_AChoiceThatCannotBeReadIsAnAuditedExceptionWithNoModel(t *testing.T) {
	t.Parallel()
	tr := &fakeChatTransport{}
	h := newTaskHarness(t, tr, taskModels{err: errors.New("connection refused to 10.0.0.5")})
	convID := aiConversation(t, h)

	r := doDraft(draftClient(t, h), convID, draftBody("concise", "Reply."))
	if r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d body %s, want 422 ai_failed", r.Status, r.Body)
	}
	if len(tr.Calls()) != 0 {
		t.Error("the provider was called")
	}
	_, model, e := interactionModel(t, h, convID)
	if model != "" || e == nil || *e != "aiModelUnavailableError" {
		t.Errorf("row = %q / %v, want no model and aiModelUnavailableError", model, e)
	}
	if strings.Contains(string(r.Body), "10.0.0.5") || (e != nil && strings.Contains(*e, "10.0.0.5")) {
		t.Errorf("the resolver's failure leaked: %s / %v", r.Body, e)
	}
}

// TestAiTasks_AreDeclaredByTheModule pins the two task keys the module
// declares, each valid, so the AI models page lists exactly what the module
// does and no task is declared for a feature that does not exist yet.
func TestAiTasks_AreDeclaredByTheModule(t *testing.T) {
	t.Parallel()
	var keys []string
	for _, task := range communications.Module().AITasks {
		if err := contracts.ValidateAITask("communications", task); err != nil {
			t.Errorf("task %q: %v", task.Key, err)
		}
		keys = append(keys, task.Key)
	}
	want := []string{"communications.compose-reply", "communications.customer-suggestion"}
	if !slices.Equal(keys, want) {
		t.Errorf("tasks = %v, want exactly %v", keys, want)
	}
}
