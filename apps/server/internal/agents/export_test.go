package agents

import (
	"context"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agents/gen"
	"github.com/vantigo-io/vantigo/server/internal/agents/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// ClaimDue claims one due request as replica and answers the run's id, "" when
// none was due.
func ClaimDue(d module.Deps, replica uuid.UUID) (uuid.UUID, error) {
	run, ok, err := newServer(d).claim(context.Background(), replica)
	if !ok {
		return uuid.Nil, err
	}
	return run.ID, err
}

// WorkerCycle is one pass of the agents-runs worker.
func WorkerCycle(d module.Deps) error {
	return (&runsWorker{s: newServer(d)}).cycle(context.Background())
}

// HandOffOverBudget is the runner's step for the agents over their budget.
func HandOffOverBudget(d module.Deps) error {
	return newServer(d).handOffOverBudget(context.Background())
}

// ReplicaClaimFor is how long a claim lives.
const ReplicaClaimFor = claimFor

// FinishHandOffs is the runner's step that writes the notes and gives up the assignees of
// the hand-offs the workers and the decisions recorded.
func FinishHandOffs(d module.Deps) error { return newServer(d).finishHandOffs(context.Background()) }

// ReviewsCycle is one pass of the agents-reviews worker.
func ReviewsCycle(d module.Deps) error {
	return (&reviewsWorker{s: newServer(d)}).cycle(context.Background())
}

// RetentionCycle is one pass of the agents-retention worker.
func RetentionCycle(d module.Deps) error {
	return (&retentionWorker{s: newServer(d)}).cycle(context.Background())
}

// Label is one decided reply, as calibration reads it.
type Label = label

// Calibrate is V21's figures from labels.
func Calibrate(labels []Label, threshold int) gen.RoleCalibration {
	return calibrate(labels, threshold)
}

// EditRatio is the share of a text a reviewer changed.
func EditRatio(before, after string) float64 { return editRatio(before, after) }

// Blocking are the rules that stop an approval.
func Blocking(held, previously []string, edited bool) []string {
	return blocking(held, previously, edited)
}

// NotifyHeldItem mails a role's hand-off person that an item waits.
func NotifyHeldItem(d module.Deps, handOff *uuid.UUID, agentName string, itemID uuid.UUID, what string) {
	newServer(d).notifyHeldItem(context.Background(), store.AgentsRole{HandOffUserID: handOff}, agentName, itemID, what)
}

// RetentionCapMonths is the longest retention Communications' own allows.
func RetentionCapMonths(d module.Deps) int { return newServer(d).retentionCapMonths() }

// SeeKeys are the permissions a person needs to see what the front desk read.
func SeeKeys() []string { return frontDesk.seeKeys() }
