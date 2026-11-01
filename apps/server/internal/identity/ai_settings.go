package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// This file is the Owner's AI models endpoints (AI as a platform provider
// design T12, T13): what the instance's AI plan is, as facts, and which model
// each AI task uses, with a write for the Owner's choice. The provider, the
// allowed models and the key are instance configuration read once at boot: they
// are shown and never edited, and the key is not shown at all — the settings
// say only that one is configured. The choice is a row of identity.ai_task_models
// per task, written in one transaction under the task's lock, audited
// ai-task-model.changed with the model the task used before and the one it uses
// now, and evicted from this process's resolver after the commit; every other
// process reads the change within aiModelsCacheTTL.

// aiTaskModelChanged is the audit action of an Owner's choice.
const aiTaskModelChanged = "ai-task-model.changed"

// The refusals' fixed words.
const (
	aiNotConfiguredMessage   = "The instance has no AI provider configured."
	aiTaskNotFoundMessage    = "No enabled module declares that AI task."
	aiModelNotAllowedMessage = "The model is not one the AI plan allows."
	aiVersionMismatchMessage = "The AI task has been changed since it was read. Reload the AI models and try again."
	aiIfMatchMessage         = "If-Match is the version the task was read at, such as 0."
	aiModelRequiredMessage   = "A model is required."
)

// aiTaskAudit is ai-task-model.changed's before and after: the task and the
// model it uses, and where that came from. Configuration and declarations,
// never a secret.
type aiTaskAudit struct {
	Task   string `json:"task"`
	Model  string `json:"model"`
	Source string `json:"source"`
}

// aiTaskVersion is a task's version for If-Match: the microseconds of its
// choice's last change since the epoch, "0" while nobody has chosen.
func aiTaskVersion(row *store.IdentityAiTaskModel) string {
	if row == nil {
		return "0"
	}
	return strconv.FormatInt(row.UpdatedAt.UnixMicro(), 10)
}

// parseAITaskVersion is an If-Match as the version it names, bare or quoted.
func parseAITaskVersion(raw string) (string, bool) {
	v := strings.TrimPrefix(strings.TrimSpace(raw), "W/")
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return "", false
	}
	return strconv.FormatInt(n, 10), true
}

// aiTaskBody is one task as the settings answer it: what it is, the model it
// uses and where that comes from, and the version a save is conditional on.
func aiTaskBody(task contracts.AITask, row *store.IdentityAiTaskModel, allowed []string) gen.AiSettingsTask {
	chosen := ""
	if row != nil {
		chosen = row.Model
	}
	model, source := resolveTaskModel(chosen, task.Default, allowed)
	body := gen.AiSettingsTask{
		Key:         task.Key,
		Display:     task.Display,
		Description: task.Description,
		Model:       model,
		Source:      gen.AiSettingsTaskSource(source),
		Version:     aiTaskVersion(row),
	}
	if task.Default != "" {
		recommended := task.Default
		body.Default = &recommended
	}
	return body
}

// aiConfigured is whether the instance has an AI plan: a provider and at least
// one allowed model.
func (s *server) aiConfigured() bool {
	cfg := s.deps.Config
	return cfg != nil && cfg.AIProvider != "" && len(cfg.AIModels) > 0
}

// GetIdentityOwnerAiSettings answers the AI plan and every task's model, read
// from the table rather than the cache so an Owner sees their own save at once.
// (GET /api/v1/identity/owner/ai/settings)
func (s *server) GetIdentityOwnerAiSettings(ctx context.Context, _ gen.GetIdentityOwnerAiSettingsRequestObject) (gen.GetIdentityOwnerAiSettingsResponseObject, error) {
	if !s.aiConfigured() {
		return refuse(http.StatusNotFound, "ai_not_configured", aiNotConfiguredMessage, nil), nil
	}
	rows, err := s.q.ListAiTaskModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("identity: read the AI task models: %w", err)
	}
	choices := make(map[string]store.IdentityAiTaskModel, len(rows))
	for _, row := range rows {
		choices[row.TaskKey] = row
	}

	allowed := s.deps.Config.AIModels
	byModule := map[string][]gen.AiSettingsTask{}
	for _, task := range s.deps.AITasks {
		var row *store.IdentityAiTaskModel
		if choice, ok := choices[task.Key]; ok {
			row = &choice
		}
		byModule[task.Module] = append(byModule[task.Module], aiTaskBody(task, row, allowed))
	}
	modules := make([]gen.AiSettingsModule, 0, len(byModule))
	for name, tasks := range byModule {
		slices.SortFunc(tasks, func(a, b gen.AiSettingsTask) int { return strings.Compare(a.Key, b.Key) })
		modules = append(modules, gen.AiSettingsModule{Module: name, Tasks: tasks})
	}
	slices.SortFunc(modules, func(a, b gen.AiSettingsModule) int { return strings.Compare(a.Module, b.Module) })

	return gen.GetIdentityOwnerAiSettings200JSONResponse(gen.AiSettings{
		Provider:      gen.AiSettingsProvider(s.deps.Config.AIProvider),
		AllowedModels: slices.Clone(allowed),
		PlanDefault:   allowed[0],
		KeyConfigured: s.deps.Config.AIAPIKey != "",
		Modules:       modules,
	}), nil
}

// PutIdentityOwnerAiTasksByKey chooses the model an AI task uses, under
// If-Match on the task's version. The checks that need no row come first —
// there is a plan, an enabled module declares the task, the version is one, the
// body names a model and the plan allows it — and the version is compared
// inside the transaction, with the task's lock held, against the row it
// replaces.
// (PUT /api/v1/identity/owner/ai/tasks/{key})
func (s *server) PutIdentityOwnerAiTasksByKey(ctx context.Context, req gen.PutIdentityOwnerAiTasksByKeyRequestObject) (gen.PutIdentityOwnerAiTasksByKeyResponseObject, error) {
	if !s.aiConfigured() {
		return refuse(http.StatusNotFound, "ai_not_configured", aiNotConfiguredMessage, nil), nil
	}
	task, ok := s.deps.AITasks[req.Key]
	if !ok {
		return refuse(http.StatusNotFound, "not_found", aiTaskNotFoundMessage, nil), nil
	}
	version, ok := parseAITaskVersion(req.Params.IfMatch)
	if !ok {
		return refuse(http.StatusBadRequest, "invalid_request", aiIfMatchMessage,
			map[string][]string{"If-Match": {aiIfMatchMessage}}), nil
	}
	var in gen.AiTaskRequest
	if req.Body != nil {
		in = *req.Body
	}
	model := strings.TrimSpace(in.Model)
	if model == "" {
		return refuse(http.StatusBadRequest, "invalid_request", invalidRequestMessage,
			map[string][]string{"model": {aiModelRequiredMessage}}), nil
	}
	allowed := s.deps.Config.AIModels
	if !slices.Contains(allowed, model) {
		return refuse(http.StatusUnprocessableEntity, "model_not_allowed", aiModelNotAllowedMessage,
			map[string][]string{"model": {aiModelNotAllowedMessage}}), nil
	}

	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()
	var answer gen.AiSettingsTask
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.LockAiTaskModel(ctx, task.Key); err != nil {
			return fmt.Errorf("identity: lock the AI task model: %w", err)
		}
		var current *store.IdentityAiTaskModel
		row, err := q.GetAiTaskModel(ctx, task.Key)
		switch {
		case err == nil:
			current = &row
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("identity: read the AI task model: %w", err)
		}
		if aiTaskVersion(current) != version {
			return refuse(http.StatusPreconditionFailed, "version_mismatch", aiVersionMismatchMessage, nil)
		}
		before := aiTaskBody(task, current, allowed)
		if err := q.SaveAiTaskModel(ctx, store.SaveAiTaskModelParams{TaskKey: task.Key, Model: model, Now: now, UpdatedBy: &p.UserID}); err != nil {
			return fmt.Errorf("identity: save the AI task model: %w", err)
		}
		saved, err := q.GetAiTaskModel(ctx, task.Key)
		if err != nil {
			return fmt.Errorf("identity: read the saved AI task model: %w", err)
		}
		answer = aiTaskBody(task, &saved, allowed)
		return writeAudit(ctx, q, r, now, auditEvent{
			actor:  &p.UserID,
			action: aiTaskModelChanged,
			before: aiTaskAudit{Task: task.Key, Model: before.Model, Source: string(before.Source)},
			after:  aiTaskAudit{Task: task.Key, Model: answer.Model, Source: string(answer.Source)},
			mfa:    p.MFAVerified,
		})
	})
	if err != nil {
		return refusalOr[gen.PutIdentityOwnerAiTasksByKeyResponseObject](err)
	}
	s.access.aiModelsFor(s.deps).evict()
	return gen.PutIdentityOwnerAiTasksByKey200JSONResponse(answer), nil
}

func (r refusal) VisitGetIdentityOwnerAiSettingsResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPutIdentityOwnerAiTasksByKeyResponse(w http.ResponseWriter) error {
	return r.write(w)
}
