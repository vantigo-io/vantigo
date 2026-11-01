package identity_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The model per AI task (AI as a platform provider design T12): the import of
// COMMUNICATIONS_AI_MODEL, once, at the first API mount that finds the table
// empty.

// Tasks the way the modules declare them. The staff task is there to prove the
// import writes Communications' tasks and no one else's.
var (
	taskComposeReply = contracts.AITask{
		Key: "communications.compose-reply", Module: "communications",
		Display: "Draft a reply", Description: "Writes an editable reply.",
	}
	taskCustomerSuggestion = contracts.AITask{
		Key: "communications.customer-suggestion", Module: "communications",
		Display: "Suggest the customer", Description: "Suggests the customer.", Default: "gpt-4o",
	}
	taskStaffSummary = contracts.AITask{
		Key: "staff.summarise", Module: "staff",
		Display: "Summarise a profile", Description: "Summarises a profile.",
	}
)

// withAIPlan is an instance with an OpenAI plan allowing models.
func withAIPlan(models string) []harnessOption {
	return []harnessOption{
		withEnv("AI_PROVIDER", "openai"),
		withEnv("AI_API_KEY", "sk-identity-test-key"),
		withEnv("AI_MODELS", models),
	}
}

func newAIHarness(t *testing.T, opts ...harnessOption) *harness {
	t.Helper()
	return newHarness(t, append(opts, withAITasks(taskComposeReply, taskCustomerSuggestion, taskStaffSummary))...)
}

// remount mounts the installation once more over the same database with a
// fresh Access, as a second API process — or this one restarted — does.
func (h *harness) remount(t testing.TB) {
	t.Helper()
	mods := append([]module.Module{identity.Module(identity.NewAccess(h.deps))}, h.mods[1:]...)
	if _, err := module.Compose(h.deps, mods...); err != nil {
		t.Fatalf("remount: %v", err)
	}
}

// aiChoices is identity.ai_task_models as task key -> "model/updated_by".
func (h *harness) aiChoices(t testing.TB) map[string]string {
	t.Helper()
	rows, err := h.pool.Query(context.Background(), `SELECT task_key, model, coalesce(updated_by::text, '') FROM identity.ai_task_models`)
	if err != nil {
		t.Fatalf("ai task models: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, model, by string
		if err := rows.Scan(&key, &model, &by); err != nil {
			t.Fatalf("ai task models: %v", err)
		}
		out[key] = model + "/" + by
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("ai task models: %v", err)
	}
	return out
}

// An installation whose environment still carries COMMUNICATIONS_AI_MODEL
// imports it once at the mount: one row per Communications task and no other
// module's, no user behind them, one audit row naming the variable, the model
// and the tasks, and one warning. Mounting again imports nothing — the Owner's
// choice made meanwhile stands — and warns that the variable is ignored.
func TestAIModels_TheEnvironmentIsImportedOnce(t *testing.T) {
	t.Parallel()
	h := newAIHarness(t, append(withAIPlan("gpt-4o-mini,gpt-4o"), withEnv("COMMUNICATIONS_AI_MODEL", "gpt-4o"))...)

	want := map[string]string{
		"communications.compose-reply":       "gpt-4o/",
		"communications.customer-suggestion": "gpt-4o/",
	}
	if got := h.aiChoices(t); !equalMaps(got, want) {
		t.Fatalf("choices = %v, want %v: one row per communications task, none for staff, no user", got, want)
	}

	audits := h.auditEvents(t, "ai-task-models.imported")
	if len(audits) != 1 {
		t.Fatalf("%d ai-task-models.imported audits, want 1", len(audits))
	}
	if a := audits[0]; a.Actor != nil || a.Details != `{"action":"ai-task-models.imported"}` ||
		a.After != `{"variable":"COMMUNICATIONS_AI_MODEL","model":"gpt-4o","tasks":["communications.compose-reply","communications.customer-suggestion"]}` {
		t.Errorf("audit = %+v", a)
	}
	if n := len(h.warnings(t, "imported from the environment")); n != 1 {
		t.Errorf("%d import warnings, want 1", n)
	}
	if n := len(h.warnings(t, "is ignored")); n != 0 {
		t.Errorf("%d ignored warnings at the importing mount, want none", n)
	}

	// An Owner chooses meanwhile; a restart must neither undo it nor import again.
	h.exec(t, `UPDATE identity.ai_task_models SET model = 'gpt-4o-mini' WHERE task_key = 'communications.compose-reply'`)
	h.remount(t)
	if got := h.aiChoices(t); got["communications.compose-reply"] != "gpt-4o-mini/" {
		t.Errorf("the second mount rewrote a choice: %v", got)
	}
	if n := len(h.auditEvents(t, "ai-task-models.imported")); n != 1 {
		t.Errorf("%d imports audited after the second mount, want 1", n)
	}
	if n := len(h.warnings(t, "imported from the environment")); n != 1 {
		t.Errorf("%d import warnings after the second mount, want 1", n)
	}
	if n := len(h.warnings(t, "is ignored")); n != 1 {
		t.Errorf("%d ignored warnings after the second mount, want 1", n)
	}
}

// Two API processes starting together import once: the second waits for the
// table and finds it written.
func TestAIModels_TwoStartingProcessesImportOnce(t *testing.T) {
	t.Parallel()
	h := newAIHarness(t, append(withAIPlan("gpt-4o-mini,gpt-4o"), withEnv("COMMUNICATIONS_AI_MODEL", "gpt-4o-mini"))...)
	h.exec(t, `DELETE FROM identity.ai_task_models`)
	h.exec(t, `DELETE FROM identity.authorization_audit_events WHERE action = 'ai-task-models.imported'`)

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() { h.remount(t) })
	}
	wg.Wait()
	if got := h.aiChoices(t); len(got) != 2 {
		t.Errorf("choices = %v, want the two communications tasks once", got)
	}
	if n := len(h.auditEvents(t, "ai-task-models.imported")); n != 1 {
		t.Errorf("%d imports audited by four concurrent mounts, want 1", n)
	}
}

// What is not imported: a variable that is unset, a model the plan does not
// allow (said in a warning), an instance with no AI plan or no Communications
// (silent: the variable waits for either).
func TestAIModels_NothingToImport(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		opts    []harnessOption
		tasks   bool
		warning string
	}{
		"the variable is unset":      {opts: withAIPlan("gpt-4o-mini"), tasks: true},
		"the model is not allowed":   {opts: append(withAIPlan("gpt-4o-mini"), withEnv("COMMUNICATIONS_AI_MODEL", "gpt-5-unlisted")), tasks: true, warning: "was not imported"},
		"no AI plan is configured":   {opts: []harnessOption{withEnv("COMMUNICATIONS_AI_MODEL", "gpt-4o")}, tasks: true},
		"communications declares no": {opts: append(withAIPlan("gpt-4o"), withEnv("COMMUNICATIONS_AI_MODEL", "gpt-4o"))},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			opts := tc.opts
			if tc.tasks {
				opts = append(slices.Clone(opts), withAITasks(taskComposeReply))
			}
			h := newHarness(t, opts...)
			if got := h.aiChoices(t); len(got) != 0 {
				t.Errorf("choices = %v, want none", got)
			}
			if n := len(h.auditEvents(t, "ai-task-models.imported")); n != 0 {
				t.Errorf("%d imports audited, want none", n)
			}
			if n := len(h.warnings(t, "COMMUNICATIONS_AI_MODEL")); (tc.warning == "") != (n == 0) {
				t.Errorf("%d warnings about the variable, want %q", n, tc.warning)
			}
			if tc.warning != "" && len(h.warnings(t, tc.warning)) != 1 {
				t.Errorf("no %q warning", tc.warning)
			}
			if bytesContain(h.log.Bytes(), "sk-identity-test-key") {
				t.Error("the log carries the AI key")
			}
		})
	}
}

func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func bytesContain(b []byte, s string) bool { return strings.Contains(string(b), s) }
