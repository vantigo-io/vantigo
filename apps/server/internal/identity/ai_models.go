package identity

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The model chosen per AI task (AI as a platform provider design T12). Identity
// keeps the Owner's choices in identity.ai_task_models and fills the
// single-provider slot contracts.AIModels with them; the platform AI client
// (srv/ai) asks it which model a task uses and refuses any answer the plan
// (AI_MODELS) does not allow. Every reader goes through one short in-process
// cache, so a choice is in effect on every replica within aiModelsCacheTTL with
// no restart, and at once in the process that saved it.

// aiModelsCacheTTL is how long a reader answers the table it last read before
// it reads it again, the single sign-on settings' period.
const aiModelsCacheTTL = 20 * time.Second

// Where the model a task uses comes from, as the settings endpoint words it.
const (
	// aiSourceChosen: an Owner chose it and the plan still allows it.
	aiSourceChosen = "chosen"
	// aiSourceDefault: nobody chose; it is the model the task's module
	// recommends and the plan allows.
	aiSourceDefault = "default"
	// aiSourcePlan: nobody chose and the module recommends none the plan
	// allows; it is the plan's default, the first of AI_MODELS.
	aiSourcePlan = "plan"
)

// errAINotConfigured is a resolution asked while no model is allowed, which is
// the instance without an AI plan. The AI client does not exist then, so
// nothing asks; the endpoints answer it as not found.
var errAINotConfigured = errors.New("identity: no AI provider is configured")

// resolveTaskModel is the three-step resolution: the Owner's choice if there is
// one and the plan still allows it, else the task's recommended model if the
// plan allows it, else the plan's default (the first of allowed, which has at
// least one model). chosen and recommended are "" when there is none. It is
// pure, so the resolver and the settings endpoint cannot disagree.
func resolveTaskModel(chosen, recommended string, allowed []string) (model, source string) {
	if chosen != "" && slices.Contains(allowed, chosen) {
		return chosen, aiSourceChosen
	}
	if recommended != "" && slices.Contains(allowed, recommended) {
		return recommended, aiSourceDefault
	}
	return allowed[0], aiSourcePlan
}

// aiSubjectSeparator joins a task key and a subject in the name a PerSubject
// task is asked for, ai.TaskFor's form.
const aiSubjectSeparator = "#"

// aiChoiceKey is one row of the table: a task and a subject, "" for the task's
// own choice.
type aiChoiceKey struct{ task, subject string }

// aiModels is the table of choices through its cache, which is
// contracts.AIModels, and the endpoints' source of truth for what a task
// resolves to. Like ssoSettingsReader it never holds its mutex across the
// database read, so a reader waiting on it cannot deadlock an exhausted pool:
// two readers that find it stale at once may both fetch, which is one small
// read.
type aiModels struct {
	q       *store.Queries
	pool    *pgxpool.Pool
	clock   func() time.Time
	allowed []string
	tasks   map[string]contracts.AITask

	mu        sync.Mutex
	cached    map[aiChoiceKey]store.IdentityAiTaskModel
	fetchedAt time.Time
	fresh     bool
	// evictions counts evict calls, so a fetch that began before one does not
	// store what it read as fresh.
	evictions uint64
}

var _ contracts.AIModels = (*aiModels)(nil)

// newAIModels is the resolver over d: its pool and clock, the models the plan
// allows and the tasks the enabled modules declare.
func newAIModels(d module.Deps) *aiModels {
	var allowed []string
	if d.Config != nil {
		allowed = slices.Clone(d.Config.AIModels)
	}
	return &aiModels{q: store.New(d.Pool), pool: d.Pool, clock: d.Clock, allowed: allowed, tasks: d.AITasks}
}

// ModelFor answers the model task uses: always one of the allowed models. A
// task no module declares resolves to the plan's default, since a caller with
// no recommendation is a caller with no choice either. For a PerSubject task
// the name may carry a subject, "<key>#<subject>": the subject's choice comes
// first, then the task's own, the task's recommendation and the plan's default.
// A subject on any other task is an error, so a mistyped key cannot silently
// follow the task.
func (m *aiModels) ModelFor(ctx context.Context, task string) (string, error) {
	if len(m.allowed) == 0 {
		return "", errAINotConfigured
	}
	key, subject, hasSubject := strings.Cut(task, aiSubjectSeparator)
	if hasSubject && (subject == "" || !m.tasks[key].PerSubject) {
		return "", fmt.Errorf("identity: AI task %q does not take a subject", key)
	}
	choices, err := m.get(ctx)
	if err != nil {
		return "", err
	}
	chosen := choices[aiChoiceKey{key, ""}].Model
	if hasSubject {
		if own, ok := choices[aiChoiceKey{key, subject}]; ok && slices.Contains(m.allowed, own.Model) {
			return own.Model, nil
		}
	}
	model, _ := resolveTaskModel(chosen, m.tasks[key].Default, m.allowed)
	return model, nil
}

// SetModelFor chooses the model a PerSubject task uses for subject; a blank
// model forgets the choice. It is the agent page's write, not the Owner's AI
// models page's, so it is audited by the caller's own audit and not here.
func (m *aiModels) SetModelFor(ctx context.Context, task, subject, model string) error {
	if len(m.allowed) == 0 {
		return errAINotConfigured
	}
	if subject == "" || strings.Contains(task, aiSubjectSeparator) || !m.tasks[task].PerSubject {
		return fmt.Errorf("identity: AI task %q does not take a subject", task)
	}
	model = strings.TrimSpace(model)
	if model != "" && !slices.Contains(m.allowed, model) {
		return fmt.Errorf("identity: model %q is not one the AI plan allows", model)
	}
	err := db.WithTx(ctx, m.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.LockAiTaskModel(ctx, store.LockAiTaskModelParams{TaskKey: task, Subject: subject}); err != nil {
			return err
		}
		if model == "" {
			return q.DeleteAiTaskModel(ctx, store.DeleteAiTaskModelParams{TaskKey: task, Subject: subject})
		}
		return q.SaveAiTaskModel(ctx, store.SaveAiTaskModelParams{TaskKey: task, Subject: subject, Model: model, Now: m.clock()})
	})
	if err != nil {
		return fmt.Errorf("identity: save the AI task model for a subject: %w", err)
	}
	m.evict()
	return nil
}

// get answers the cached choices while they were read within aiModelsCacheTTL,
// else reads the table again with no lock held. A failed read is an error,
// never "no choices", and is not cached.
func (m *aiModels) get(ctx context.Context) (map[aiChoiceKey]store.IdentityAiTaskModel, error) {
	now := m.clock()
	m.mu.Lock()
	if m.fresh && now.Sub(m.fetchedAt) < aiModelsCacheTTL {
		cached := m.cached
		m.mu.Unlock()
		return cached, nil
	}
	evictions := m.evictions
	m.mu.Unlock()

	rows, err := m.q.ListAiTaskModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("identity: read the AI task models: %w", err)
	}
	choices := make(map[aiChoiceKey]store.IdentityAiTaskModel, len(rows))
	for _, row := range rows {
		choices[aiChoiceKey{row.TaskKey, row.Subject}] = row
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.evictions == evictions && (!m.fresh || !now.Before(m.fetchedAt)) {
		m.cached, m.fetchedAt, m.fresh = choices, now, true
	}
	return choices, nil
}

// evict makes the next get read the table, and keeps a fetch already under way
// from storing what it read.
func (m *aiModels) evict() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fresh = false
	m.evictions++
}
