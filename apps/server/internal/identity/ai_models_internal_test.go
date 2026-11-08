package identity

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// The three steps of the resolution: the Owner's choice if the plan still
// allows it, else the task's recommended model if the plan allows it, else the
// plan's default — and the source each step reports.
func TestResolveTaskModel_ThreeSteps(t *testing.T) {
	t.Parallel()
	allowed := []string{"small", "medium", "large"}
	for _, tc := range []struct {
		name                  string
		chosen, recommended   string
		wantModel, wantSource string
	}{
		{name: "the Owner's choice", chosen: "large", recommended: "medium", wantModel: "large", wantSource: "chosen"},
		{name: "a choice the plan no longer allows falls to the recommendation", chosen: "retired", recommended: "medium", wantModel: "medium", wantSource: "default"},
		{name: "no choice uses the recommendation", recommended: "medium", wantModel: "medium", wantSource: "default"},
		{name: "a recommendation the plan does not allow falls to the plan", recommended: "retired", wantModel: "small", wantSource: "plan"},
		{name: "a retired choice and a retired recommendation fall to the plan", chosen: "retired", recommended: "gone", wantModel: "small", wantSource: "plan"},
		{name: "nothing at all is the plan's default", wantModel: "small", wantSource: "plan"},
	} {
		model, source := resolveTaskModel(tc.chosen, tc.recommended, allowed)
		if model != tc.wantModel || source != tc.wantSource {
			t.Errorf("%s: resolved %q from %q, want %q from %q", tc.name, model, source, tc.wantModel, tc.wantSource)
		}
	}
}

// aiReads counts the queries that read identity.ai_task_models.
type aiReads struct{ n atomic.Int32 }

func (c *aiReads) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "FROM identity.ai_task_models") {
		c.n.Add(1)
	}
	return ctx
}

func (*aiReads) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// The resolver reads the table through a 20-second cache that a save in this
// process evicts: one read serves every task, a later call within the period
// reads nothing, one after it reads again, and an eviction reads at once. The
// answer is always one the plan allows, a task nobody declares gets the plan's
// default, and an instance with no plan answers an error, not a model.
func TestAIModels_ReadsTheTableThroughItsCache(t *testing.T) {
	t.Parallel()
	pool, databaseURL := testdb.Migrated(t)
	pcfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	reads := &aiReads{}
	pcfg.ConnConfig.Tracer = reads
	pcfg.MaxConns = 2
	traced, err := pgxpool.NewWithConfig(context.Background(), pcfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(traced.Close)

	now := internalNow
	clock := func() time.Time { return now }
	d := module.Deps{
		Pool:  traced,
		Clock: clock,
		Config: &config.Config{
			AIProvider: "openai", AIAPIKey: "sk", AIModels: []string{"small", "medium", "large"},
		},
		AITasks: map[string]contracts.AITask{
			"communications.a": {Key: "communications.a", Module: "communications", Display: "A", Description: "a", Default: "medium"},
			"communications.b": {Key: "communications.b", Module: "communications", Display: "B", Description: "b"},
		},
	}
	m := newAIModels(d)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ask := func(task string) string {
		t.Helper()
		model, err := m.ModelFor(ctx, task)
		if err != nil {
			t.Fatalf("ModelFor(%s): %v", task, err)
		}
		return model
	}

	if a, b := ask("communications.a"), ask("communications.b"); a != "medium" || b != "small" {
		t.Fatalf("with no choices: a = %q, b = %q, want the recommendation and the plan's default", a, b)
	}
	if n := reads.n.Load(); n != 1 {
		t.Errorf("%d reads for two tasks, want 1: one read serves every task", n)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO identity.ai_task_models (task_key, model, updated_at) VALUES ('communications.a', 'large', $1), ('communications.b', 'retired', $1)`, now); err != nil {
		t.Fatal(err)
	}
	if a := ask("communications.a"); a != "medium" {
		t.Errorf("a = %q right after the insert, want the cached answer: the row is not read inside the period", a)
	}
	now = now.Add(aiModelsCacheTTL - time.Second)
	if a := ask("communications.a"); a != "medium" || reads.n.Load() != 1 {
		t.Errorf("a = %q after %d reads, want the cache to hold until the period ends", a, reads.n.Load())
	}
	now = now.Add(2 * time.Second)
	if a, b := ask("communications.a"), ask("communications.b"); a != "large" || b != "small" {
		t.Errorf("after the period: a = %q, b = %q, want the choice, and the plan's default for a choice the plan does not allow", a, b)
	}
	if n := reads.n.Load(); n != 2 {
		t.Errorf("%d reads after the period, want 2", n)
	}

	if _, err := pool.Exec(ctx, `UPDATE identity.ai_task_models SET model = 'small' WHERE task_key = 'communications.a'`); err != nil {
		t.Fatal(err)
	}
	m.evict()
	if a := ask("communications.a"); a != "small" {
		t.Errorf("a = %q after evict, want the table read again", a)
	}

	if got := ask("nobody.declares-this"); got != "small" {
		t.Errorf("an undeclared task resolved %q, want the plan's default", got)
	}

	none := newAIModels(module.Deps{Pool: traced, Clock: clock, Config: &config.Config{}})
	if _, err := none.ModelFor(ctx, "communications.a"); !errors.Is(err, errAINotConfigured) {
		t.Errorf("ModelFor with no plan = %v, want errAINotConfigured", err)
	}
}

// A PerSubject task resolves a subject's choice first, then the task's own,
// then its recommendation and the plan's default; a task that is not PerSubject
// refuses a subject, and SetModelFor writes, validates and forgets a choice.
func TestAIModels_AModelPerSubject(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	now := internalNow
	d := module.Deps{
		Pool:  pool,
		Clock: func() time.Time { return now },
		Config: &config.Config{
			AIProvider: "openai", AIAPIKey: "sk", AIModels: []string{"small", "medium", "large"},
		},
		AITasks: map[string]contracts.AITask{
			"agents.run": {Key: "agents.run", Module: "agents", Display: "Agent at work", Description: "r", Default: "medium", PerSubject: true},
			"agents.one": {Key: "agents.one", Module: "agents", Display: "One", Description: "o"},
		},
	}
	m := newAIModels(d)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ask := func(task string) string {
		t.Helper()
		model, err := m.ModelFor(ctx, task)
		if err != nil {
			t.Fatalf("ModelFor(%s): %v", task, err)
		}
		return model
	}

	if got := ask("agents.run#a1"); got != "medium" {
		t.Errorf("a subject with no choice = %q, want the task's recommendation", got)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO identity.ai_task_models (task_key, model, updated_at) VALUES ('agents.run', 'large', $1)`, now); err != nil {
		t.Fatal(err)
	}
	m.evict()
	if got := ask("agents.run#a1"); got != "large" {
		t.Errorf("a subject with no choice = %q, want the task's own choice", got)
	}
	if err := m.SetModelFor(ctx, "agents.run", "a1", "small"); err != nil {
		t.Fatal(err)
	}
	if a1, a2, own := ask("agents.run#a1"), ask("agents.run#a2"), ask("agents.run"); a1 != "small" || a2 != "large" || own != "large" {
		t.Errorf("a1 = %q, a2 = %q, task = %q; want the subject's choice for a1 only", a1, a2, own)
	}
	if err := m.SetModelFor(ctx, "agents.run", "a1", "retired"); err == nil {
		t.Error("SetModelFor(a model the plan does not allow) = nil, want an error")
	}
	if err := m.SetModelFor(ctx, "agents.one", "a1", "small"); err == nil {
		t.Error("SetModelFor(a task that is not PerSubject) = nil, want an error")
	}
	if _, err := m.ModelFor(ctx, "agents.one#a1"); err == nil {
		t.Error("ModelFor(a subject on a task that is not PerSubject) = nil error, want a refusal")
	}
	if _, err := m.ModelFor(ctx, "agents.run#"); err == nil {
		t.Error("ModelFor(a blank subject after #) = nil error, want a refusal")
	}
	if err := m.SetModelFor(ctx, "agents.run", "a1", ""); err != nil {
		t.Fatal(err)
	}
	if got := ask("agents.run#a1"); got != "large" {
		t.Errorf("after forgetting = %q, want the task's choice again", got)
	}
}
