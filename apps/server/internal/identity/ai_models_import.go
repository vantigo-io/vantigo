package identity

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The second and last import of the environment into the workspace tier (AI as
// a platform provider design T1, T12), after the single sign-on settings
// (sso_settings.go): COMMUNICATIONS_AI_MODEL, the one model Communications used
// before models were chosen per task, becomes the choice for each of
// Communications' tasks at the first API mount that finds the table empty, and
// is ignored from then on. The rule has no precedence in it: the variable is
// read at most once, while no choice exists, and never again.

// aiTaskModelsImported is the audit action of the import.
const aiTaskModelsImported = "ai-task-models.imported"

// aiImportedAudit is ai-task-models.imported's after: the variable's name, the
// model it named and the tasks that now hold it — all of them configuration or
// declarations, none a secret.
type aiImportedAudit struct {
	Variable string   `json:"variable"`
	Model    string   `json:"model"`
	Tasks    []string `json:"tasks"`
}

// communicationsAIModelVariable is the variable imported.
const communicationsAIModelVariable = "COMMUNICATIONS_AI_MODEL"

// importAITaskModelsFromEnvironment imports d's COMMUNICATIONS_AI_MODEL into
// identity.ai_task_models, once, in one transaction holding the table: an
// empty table while the variable is set, an AI plan is configured, the plan
// allows that model and Communications declares tasks is written with one row
// per `communications.*` task, audited ai-task-models.imported with no actor,
// and a warning names the variable for removal. A table already written
// imports nothing, and a variable still set is named in a warning that it is
// ignored. A model the plan does not allow is not imported, and a warning says
// so; no plan, or no Communications, imports nothing and says nothing, so the
// import happens when the plan arrives. Two API processes starting together
// import once: the second waits for the table lock and finds it written. Only
// identity's mount — an API process — calls it.
func importAITaskModelsFromEnvironment(ctx context.Context, d module.Deps, resolver *aiModels) error {
	if d.Config == nil || d.Config.CommunicationsAIModel == "" {
		return nil
	}
	model := d.Config.CommunicationsAIModel
	var keys []string
	for key := range d.AITasks {
		if strings.HasPrefix(key, "communications.") {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)

	const (
		nothing = iota
		imported
		alreadyWritten
		notAllowed
	)
	outcome := nothing
	now := d.Clock()
	err := db.WithTx(ctx, d.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		// SHARE ROW EXCLUSIVE conflicts with itself and with every writer, so
		// two starting processes queue here and the second counts the rows
		// the first wrote.
		if _, err := tx.Exec(ctx, "LOCK TABLE identity.ai_task_models IN SHARE ROW EXCLUSIVE MODE"); err != nil {
			return err
		}
		q := store.New(tx)
		n, err := q.CountAiTaskModels(ctx)
		if err != nil {
			return err
		}
		switch {
		case n > 0:
			outcome = alreadyWritten
		case len(d.Config.AIModels) == 0 || len(keys) == 0:
			// No plan to choose within, or no task to choose for: the
			// variable waits for either to arrive.
		case !slices.Contains(d.Config.AIModels, model):
			outcome = notAllowed
		default:
			for _, key := range keys {
				if err := q.ImportAiTaskModel(ctx, store.ImportAiTaskModelParams{TaskKey: key, Model: model, Now: now}); err != nil {
					return err
				}
			}
			if err := writeAudit(ctx, q, nil, now, auditEvent{
				action: aiTaskModelsImported,
				after:  aiImportedAudit{Variable: communicationsAIModelVariable, Model: model, Tasks: keys},
			}); err != nil {
				return err
			}
			outcome = imported
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("identity: import the AI task models from the environment: %w", err)
	}
	switch outcome {
	case imported:
		resolver.evict()
		d.Logger.Warn("identity: "+communicationsAIModelVariable+" was imported from the environment as the model of Communications' AI tasks; "+
			"the variable is now ignored and should be removed", "variable", communicationsAIModelVariable, "model", model, "tasks", keys)
	case alreadyWritten:
		d.Logger.Warn("identity: "+communicationsAIModelVariable+" is ignored, since the model of each AI task is kept in the database "+
			"and chosen under Workspace admin; remove it", "variable", communicationsAIModelVariable)
	case notAllowed:
		d.Logger.Warn("identity: "+communicationsAIModelVariable+" was not imported, since the models the AI plan allows (AI_MODELS) do not include it; "+
			"an Owner chooses each task's model under Workspace admin", "variable", communicationsAIModelVariable, "model", model, "allowed", d.Config.AIModels)
	}
	return nil
}
