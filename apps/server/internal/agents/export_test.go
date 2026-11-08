package agents

import (
	"context"

	"github.com/google/uuid"

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
