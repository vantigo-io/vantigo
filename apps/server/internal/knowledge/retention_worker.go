package knowledge

// The knowledge.retention worker (knowledge design K9–K11, K17): once a day
// it deletes the trash groups trashed more than 30 days ago, prunes the
// versions older than 365 days beyond each article's newest 20, then the
// files that neither an article's content nor any version left names (K19)
// and the objects of every file row gone, removes the favourites of articles
// their person can no longer read, and sweeps the users identity has erased.
//
// One replica runs a cycle: it takes an advisory lease on a pooled connection
// of its own and runs the purges on that connection, so it never asks the
// pool for a second one while it holds one — safe on a pool of one. The
// erasure sweep asks identity, whose queries take their own connections, so
// it runs after the lease's connection is back in the pool, and only on the
// replica that won the lease; it is idempotent.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/knowledge/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/storage"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

const (
	retentionWorkerName = "knowledge.retention"
	retentionInterval   = 24 * time.Hour
	retentionLeaseKey   = int64(0x4b4e4f5752455431) // "KNOWRET1"
	// versionRetention and versionsKept are the history's retention (K9).
	versionRetention = 365 * 24 * time.Hour
	versionsKept     = 20
	// erasureChunk is how many user ids one directory read asks about (K17).
	erasureChunk = 200
	// fileGrace is how long an uploaded file is kept before a document
	// names it: an editor may upload and save a while later (K19).
	fileGrace = 7 * 24 * time.Hour
	// objectChunk is how many queued objects one cycle removes at most.
	objectChunk = 1000
	// objectDeleteTimeout bounds one object-store delete.
	objectDeleteTimeout = 10 * time.Second
)

// workers is the module's Workers.
func workers(d module.Deps) []worker.Worker {
	return []worker.Worker{&retentionWorker{deps: d}}
}

// retentionWorker runs the retention cycle every interval.
type retentionWorker struct{ deps module.Deps }

func (w *retentionWorker) Name() string            { return retentionWorkerName }
func (w *retentionWorker) Interval() time.Duration { return retentionInterval }

// Run runs a cycle every interval until ctx is done.
func (w *retentionWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(retentionInterval)
	defer ticker.Stop()
	for {
		if _, err := w.cycle(ctx); err != nil && ctx.Err() == nil {
			w.deps.Logger.ErrorContext(ctx, "knowledge: the retention cycle failed", "worker", retentionWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// retentionResult is what one cycle did.
type retentionResult struct {
	Acquired          bool
	TrashPurged       int64
	VersionsPruned    int64
	FilesPruned       int64
	ObjectsRemoved    int
	FavouritesRemoved int64
	Erased            int
	// queued are the object keys to remove once the lease's connection is
	// back in the pool.
	queued []string
}

// cycle is one pass: the purges under the lease, then the erasure sweep.
func (w *retentionWorker) cycle(ctx context.Context) (retentionResult, error) {
	res, err := w.purgeUnderLease(ctx)
	if err != nil || !res.Acquired {
		return res, err
	}
	if res.ObjectsRemoved, err = w.removeQueuedObjects(ctx, res.queued); err != nil {
		return res, err
	}
	res.Erased, err = sweepErased(ctx, w.deps)
	return res, err
}

// purgeUnderLease takes the lease on a connection of its own and, when it
// wins it, runs the purges on that same connection.
func (w *retentionWorker) purgeUnderLease(ctx context.Context) (res retentionResult, err error) {
	conn, err := w.deps.Pool.Acquire(ctx)
	if err != nil {
		return res, fmt.Errorf("knowledge: acquire a connection for the retention lease: %w", err)
	}
	defer conn.Release()
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, retentionLeaseKey).Scan(&res.Acquired); err != nil {
		return res, fmt.Errorf("knowledge: take the retention lease: %w", err)
	}
	if !res.Acquired {
		return res, nil
	}
	defer func() {
		release := context.WithoutCancel(ctx)
		if _, uerr := conn.Exec(release, `SELECT pg_advisory_unlock($1)`, retentionLeaseKey); uerr != nil {
			// A session that still holds the lease must not go back into the pool.
			_ = conn.Conn().Close(release)
		}
	}()
	now := w.deps.Clock()
	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := lockTree(ctx, q); err != nil {
			return err
		}
		n, err := q.PurgeTrash(ctx, now.Add(-trashRetention))
		if err != nil {
			return fmt.Errorf("knowledge: purge the trash: %w", err)
		}
		res.TrashPurged = n
		return nil
	})
	if err != nil {
		return res, err
	}
	q := store.New(conn)
	if res.VersionsPruned, err = q.PruneVersions(ctx, store.PruneVersionsParams{Keep: versionsKept, Cutoff: now.Add(-versionRetention)}); err != nil {
		return res, fmt.Errorf("knowledge: prune the versions: %w", err)
	}
	if res.FilesPruned, err = q.PruneUnreferencedFiles(ctx, now.Add(-fileGrace)); err != nil {
		return res, fmt.Errorf("knowledge: prune the files: %w", err)
	}
	if res.queued, err = q.ListObjectDeletions(ctx, objectChunk); err != nil {
		return res, fmt.Errorf("knowledge: list the objects to remove: %w", err)
	}
	if res.FavouritesRemoved, err = q.PruneFavourites(ctx); err != nil {
		return res, fmt.Errorf("knowledge: prune the favourites: %w", err)
	}
	return res, nil
}

// removeQueuedObjects removes the objects of the file rows gone, queued by
// the trigger in the transactions that deleted them, each with a timeout of
// its own, after the lease's connection is back in the pool; then it clears
// the queue rows of those the store confirmed. A store that refuses leaves
// the row for the next cycle; removing an object twice is harmless.
func (w *retentionWorker) removeQueuedObjects(ctx context.Context, keys []string) (int, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	o := newObjects(w.deps)
	var done []string
	for _, key := range keys {
		callCtx, cancel := context.WithTimeout(ctx, objectDeleteTimeout)
		err := o.delete(callCtx, key)
		cancel()
		if err != nil && !errors.Is(err, storage.ErrNotExist) {
			w.deps.Logger.WarnContext(ctx, "knowledge: a file object could not be removed; it is tried again next cycle",
				"worker", retentionWorkerName, "error", err.Error())
			continue
		}
		done = append(done, key)
	}
	if len(done) == 0 {
		return 0, nil
	}
	if err := store.New(w.deps.Pool).DeleteObjectDeletions(ctx, done); err != nil {
		return 0, fmt.Errorf("knowledge: clear the removed objects: %w", err)
	}
	return len(done), nil
}

// sweepErased removes what Knowledge holds of users identity has erased
// (K17), found the way the Assistant finds them: the module asks
// contracts.UserDirectory.Users, 200 ids at a time, about every user it holds
// anything of, and a user it leaves out is gone. Their member rows and
// favourites go; so do the private roots they were the only writer of, with
// their subtrees and versions. Their authorship becomes null — a former user
// — and what they wrote in shared articles stays. It answers how many users
// it found erased.
func sweepErased(ctx context.Context, d module.Deps) (int, error) {
	if d.Users == nil {
		return 0, nil
	}
	after := uuid.Nil
	erased := 0
	for {
		ids, err := store.New(d.Pool).ListHeldUserIDs(ctx, store.ListHeldUserIDsParams{After: after, BatchSize: erasureChunk})
		if err != nil {
			return erased, fmt.Errorf("knowledge: list the users held: %w", err)
		}
		if len(ids) == 0 {
			return erased, nil
		}
		known, err := d.Users.Users(ctx, ids)
		if err != nil {
			return erased, fmt.Errorf("knowledge: ask which users still exist: %w", err)
		}
		exists := make(map[uuid.UUID]bool, len(known))
		for _, u := range known {
			exists[u.ID] = true
		}
		var gone []uuid.UUID
		for _, id := range ids {
			if !exists[id] {
				gone = append(gone, id)
			}
		}
		if len(gone) > 0 {
			if err := eraseUsers(ctx, d, gone); err != nil {
				return erased, err
			}
			erased += len(gone)
		}
		after = ids[len(ids)-1]
		if len(ids) < erasureChunk || ctx.Err() != nil {
			return erased, nil
		}
	}
}

// eraseUsers removes what Knowledge holds of the users in gone, in one
// transaction under the tree's lock.
func eraseUsers(ctx context.Context, d module.Deps, gone []uuid.UUID) error {
	return db.WithTx(ctx, d.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := lockTree(ctx, q); err != nil {
			return err
		}
		// The private roots go first, while the access still names their
		// only writer.
		if _, err := q.DeleteErasedPrivateArticles(ctx, gone); err != nil {
			return fmt.Errorf("knowledge: delete the erased users' private articles: %w", err)
		}
		// With every member row of theirs gone, no materialised row of theirs
		// is left either: the rest of the access is unchanged.
		if err := q.DeleteErasedMembers(ctx, gone); err != nil {
			return fmt.Errorf("knowledge: delete the erased users' memberships: %w", err)
		}
		if err := q.DeleteErasedFavourites(ctx, gone); err != nil {
			return fmt.Errorf("knowledge: delete the erased users' favourites: %w", err)
		}
		if err := q.ForgetErasedAuthors(ctx, gone); err != nil {
			return fmt.Errorf("knowledge: forget the erased users' authorship: %w", err)
		}
		if err := q.ForgetErasedFileAuthors(ctx, gone); err != nil {
			return fmt.Errorf("knowledge: forget the erased users' uploads: %w", err)
		}
		if err := q.DeleteErasedMentionNotices(ctx, gone); err != nil {
			return fmt.Errorf("knowledge: delete the erased users' mention notices: %w", err)
		}
		return notifyTree(ctx, tx)
	})
}
