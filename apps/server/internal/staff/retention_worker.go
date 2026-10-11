package staff

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the staff-retention worker (design S15, §8.5; plan Task 5,
// readings 19 and 20) and POST /retention/run, which runs the same code by
// hand. Every poll it does two things, each with its own connection
// discipline (the plan's blocker):
//
//   - the erase cycle runs under an advisory lease taken on ONE pooled
//     connection, and the due queries and every person's transaction — the
//     person's lock included (withPersonLockOn) — run on that connection,
//     never on the pool: a cycle that held the lease's connection and asked
//     the pool for another deadlocked the customers worker at startup with a
//     pool of four (PR #124). Another replica holding the lease means this
//     one skips the cycle;
//   - the ledger pass (object_deletions.go) takes no lease and holds no
//     connection across a call to the object store.
//
// "Once per civil day" is idempotence (reading 19): an erase finds nothing
// due a second time, so the poll can be hourly and a day that a
// daylight-saving change makes 23 or 25 hours long is still erased once.

const (
	// retentionWorkerName is what the runner logs this worker as.
	retentionWorkerName = "staff-retention"

	// retentionLeaseKey is the ASCII string "STAFFRET" read as a big-endian
	// 64-bit value: this worker's own advisory lease, apart from every other
	// module's.
	retentionLeaseKey int64 = 0x5354414646524554

	// retentionBatch bounds one erase cycle at fifty people (the customers
	// worker's bound): each is a transaction holding their row, and a cycle
	// is meant to finish; a backlog drains over cycles.
	retentionBatch = 50

	// defaultWorkersPoll is the poll of a worker built from Deps without a
	// Config — config.go's own default for STAFF_WORKERS_POLL.
	defaultWorkersPoll = time.Hour
)

// retentionActor is who the worker's erased events name: the installation.
var retentionActor = actor{Kind: actorKindSystem, Display: systemActorDisplayName}

// RetentionWorker erases what is due and finishes the deletion ledger. It
// implements worker.Worker.
type RetentionWorker struct {
	deps module.Deps
	srv  *server
}

var _ worker.Worker = (*RetentionWorker)(nil)

// NewRetentionWorker builds the worker over d. It never fails: an object
// store that cannot be built — in worker mode Deps carry none, and the
// module builds its own from Config — is kept as its error, and only the
// ledger's attempts answer it, row by row, as storage_unavailable.
func NewRetentionWorker(d module.Deps) *RetentionWorker {
	return &RetentionWorker{deps: d, srv: newServer(d)}
}

// workers is the module's background workers: staff-retention unless
// STAFF_RETENTION_ENABLED=0. A Deps with no Config cannot say, and gets none.
func workers(d module.Deps) []worker.Worker {
	if d.Config == nil || !d.Config.StaffRetentionEnabled {
		return nil
	}
	return []worker.Worker{NewRetentionWorker(d)}
}

// Name identifies this worker in the runner's logs.
func (w *RetentionWorker) Name() string { return retentionWorkerName }

// Interval is the poll (STAFF_WORKERS_POLL, default an hour).
func (w *RetentionWorker) Interval() time.Duration {
	if w.deps.Config != nil && w.deps.Config.StaffWorkersPoll > 0 {
		return w.deps.Config.StaffWorkersPoll
	}
	return defaultWorkersPoll
}

// Run is the worker loop: an erase cycle and a ledger pass, then the poll. A
// failure is logged and the loop goes on — the runner never restarts a
// worker.
func (w *RetentionWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.Interval())
	defer ticker.Stop()
	for {
		if _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.srv.logger().Error("staff: the retention erase cycle failed", "worker", retentionWorkerName, "error", err.Error())
		}
		if _, _, err := w.RunLedger(ctx); err != nil && ctx.Err() == nil {
			w.srv.logger().Error("staff: the deletion ledger pass failed", "worker", retentionWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle is one erase cycle under the lease, the installation as the
// actor, reporting whether it ran: false when another holder has the lease.
func (w *RetentionWorker) RunCycle(ctx context.Context) (bool, error) {
	ran, _, err := w.srv.eraseUnderLease(ctx, retentionActor)
	return ran, err
}

// RunLedger is one pass over the deletion ledger: the rows attempted and the
// attempts the store confirmed.
func (w *RetentionWorker) RunLedger(ctx context.Context) (attempted, succeeded int, err error) {
	return w.srv.runLedger(ctx)
}

// eraseUnderLease runs one erase cycle holding the worker's advisory lease,
// on the lease's connection alone. It answers false, and runs nothing, when
// another holder has the lease. The unlock runs on a context stripped of
// cancellation, so a cancelled cycle still gives the lease back; an unlock
// that fails closes the connection, which ends the session and the lock with
// it (communications' and customers' rule).
func (s *server) eraseUnderLease(ctx context.Context, who actor) (bool, []erasedKind, error) {
	conn, err := s.deps.Pool.Acquire(ctx)
	if err != nil {
		return false, nil, fmt.Errorf("staff: acquire a connection for the retention lease: %w", err)
	}
	defer conn.Release()

	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, retentionLeaseKey).Scan(&acquired); err != nil {
		return false, nil, fmt.Errorf("staff: take the retention lease: %w", err)
	}
	if !acquired {
		s.logger().DebugContext(ctx, "staff: the retention lease is held elsewhere; skipping this cycle", "worker", retentionWorkerName)
		return false, nil, nil
	}
	defer func() {
		release := context.WithoutCancel(ctx)
		if _, err := conn.Exec(release, `SELECT pg_advisory_unlock($1)`, retentionLeaseKey); err != nil {
			s.logger().ErrorContext(release, "staff: releasing the retention lease failed; discarding the connection",
				"worker", retentionWorkerName, "error", err.Error())
			_ = conn.Conn().Close(release)
		}
	}()
	kinds, err := s.eraseCycle(ctx, conn, who)
	return true, kinds, err
}

// logger is the module's logger, or the process's when a bare Deps has none.
func (s *server) logger() *slog.Logger {
	if s.deps.Logger != nil {
		return s.deps.Logger
	}
	return slog.Default()
}

// PostStaffRetentionRun Run retention now
// (POST /api/v1/staff/retention/run)
//
// The worker's two steps, now, by the same code and with the same
// disciplines: an erase cycle under the lease — the caller as the erased
// events' actor, resolved before any connection is held — and a ledger pass.
// The lease held elsewhere is 409 retention_busy, and nothing runs.
func (s *server) PostStaffRetentionRun(ctx context.Context, _ gen.PostStaffRetentionRunRequestObject) (gen.PostStaffRetentionRunResponseObject, error) {
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	ran, kinds, err := s.eraseUnderLease(ctx, who)
	if err != nil {
		return nil, err
	}
	if !ran {
		return gen.PostStaffRetentionRun409ApplicationProblemPlusJSONResponse(retentionBusy()), nil
	}
	attempted, succeeded, err := s.runLedger(ctx)
	if err != nil {
		return nil, err
	}
	return gen.PostStaffRetentionRun200JSONResponse{
		Erased:    gen.StaffRetentionErased{Kinds: erasedKindsResponse(kinds)},
		Deletions: gen.StaffRetentionDeletions{Attempted: int32(attempted), Succeeded: int32(succeeded)},
	}, nil
}
