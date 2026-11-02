package assistant

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/assistant/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// RetentionCycle runs one cycle of the retention worker: the conversations it
// expired, the people the erasure sweep found erased.
func RetentionCycle(ctx context.Context, d module.Deps) (expired, erased int, err error) {
	return (&retentionWorker{deps: d}).RunCycle(ctx)
}

// StaleRunsCycle runs one cycle of the stale-runs worker.
func StaleRunsCycle(ctx context.Context, d module.Deps) (int, error) {
	return (&staleRunsWorker{deps: d}).RunCycle(ctx)
}

// BulkOps is the dispatcher as bulk jobs use it, for a test to script.
type BulkOps = bulkOps

// bulkHost is the assistant server composed over pool.
func bulkHost(pool *pgxpool.Pool) *server {
	v, ok := bulkHosts.Load(pool)
	if !ok {
		panic("assistant: no server composed over that pool")
	}
	return v.(*server)
}

// UseBulkOps has the server composed over pool run bulk jobs through ops.
func UseBulkOps(pool *pgxpool.Pool, ops BulkOps) { bulkHost(pool).opsOverride = ops }

// DieAfterInFlight has the runner of the server composed over pool stop, as a
// replica that died would, right after it marks an item in flight and before it
// calls it, when die answers true.
func DieAfterInFlight(pool *pgxpool.Pool, die func(job uuid.UUID, seq int32) bool) {
	bulkHost(pool).afterInFlight = die
}

// ClaimID is the name the server composed over pool puts on its claims.
func ClaimID(pool *pgxpool.Pool) string { return bulkHost(pool).claimID }

// RunBulk takes the claim of a job that has none (or whose claim lapsed) and
// runs it here, to where it ends or pauses.
func RunBulk(ctx context.Context, pool *pgxpool.Pool, jobID uuid.UUID) error {
	s := bulkHost(pool)
	now := s.deps.Clock()
	job, err := store.New(pool).ClaimBulkJob(ctx, store.ClaimBulkJobParams{ID: jobID, ClaimedBy: s.claimedBy(), ClaimedUntil: now.Add(bulkClaimTTL), Now: now})
	if err != nil {
		return err
	}
	runCtx, release := s.bulkBackground(job, time.Minute)
	defer release()
	s.runBulk(runCtx, job, nil)
	return nil
}

// TakeOver claims a job whose claim lapsed for the named replica, as a takeover
// does, and answers the job as it then stands.
func TakeOver(ctx context.Context, pool *pgxpool.Pool, jobID uuid.UUID, replica string) (store.AssistantBulkJob, error) {
	now := bulkHost(pool).deps.Clock()
	return store.New(pool).ClaimBulkJob(ctx, store.ClaimBulkJobParams{ID: jobID, ClaimedBy: &replica, ClaimedUntil: now.Add(bulkClaimTTL), Now: now})
}

// ResumeLapsed has the server composed over pool take over every lapsed job, as
// a read of one does, and answers at once: the jobs run on goroutines.
func ResumeLapsed(ctx context.Context, pool *pgxpool.Pool) {
	bulkHost(pool).resumeLapsed(ctx, uuid.Nil)
}

// SelectorLimits are the constants a test of the selector needs.
const (
	MaxBulkItems   = maxBulkItems
	BulkCanarySize = bulkCanarySize
	BulkChunkSize  = bulkChunkSize
)

// FailuresCSV renders failed items as the download does.
func FailuresCSV(labels, codes, titles []string) ([]byte, error) {
	rows := make([]store.AssistantBulkJobItem, len(labels))
	for i := range labels {
		item, _ := json.Marshal(bulkItem{Bound: map[string]any{"id": i + 1}, Label: labels[i]})
		code, title := codes[i], titles[i]
		status := int32(403)
		rows[i] = store.AssistantBulkJobItem{Seq: int32(i), Item: item, Status: "failed", HttpStatus: &status, ProblemCode: &code, ProblemTitle: &title}
	}
	return failuresCSV(rows)
}
