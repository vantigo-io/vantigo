package entra

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is entra-outlook-sweep, the module's one worker (Outlook
// connection design M6, M7; delivery A's plan reading 18). Each hour, under
// an advisory lease taken on one pooled connection — every query on that
// connection, so a cycle never waits for a second while it holds the first
// —, it:
//
//   - deletes the closing connections nothing needs any more: seven days
//     after they closed, or once a day has passed since they were last
//     used (or closed) with no subscription of the mailbox left for a
//     removal to delete; one entra.outlook.deleted event each;
//   - reads, at most once a day each, the account of every connection
//     waiting in reconnect — in Vantigo first, then accountEnabled in the
//     tenant with the application's token —, and closes one whose account
//     is deleted or disabled, its token forgotten: Microsoft refuses it for
//     good, and a person who never reconnects is still let go.
//
// It has no setting: nothing else runs the deletion M7 requires, and both
// steps are a few indexed statements when there is nothing to do. It runs
// in the API and in worker mode alike, wherever module.Workers runs.

const (
	// outlookSweepName is what the runner logs this worker as.
	outlookSweepName = "entra-outlook-sweep"
	// outlookSweepInterval is the worker's cycle.
	outlookSweepInterval = time.Hour
	// outlookSweepLeaseKey is the ASCII string "ENTOLSW1" read as a
	// big-endian 64-bit value: this worker's own advisory lease.
	outlookSweepLeaseKey int64 = 0x454E544F4C535731
	// outlookAccountChecks bounds the accounts one cycle reads in the
	// tenant; a backlog drains over cycles.
	outlookAccountChecks = 100
)

// workers is the module's background work: entra-outlook-sweep.
func workers(d module.Deps) []worker.Worker {
	return []worker.Worker{NewOutlookSweepWorker(d)}
}

// OutlookSweepWorker is entra-outlook-sweep. It implements worker.Worker.
type OutlookSweepWorker struct {
	deps   module.Deps
	client *graphClient
}

var _ worker.Worker = (*OutlookSweepWorker)(nil)

// NewOutlookSweepWorker builds the worker over d.
func NewOutlookSweepWorker(d module.Deps) *OutlookSweepWorker {
	return &OutlookSweepWorker{deps: d, client: newGraphClient(d)}
}

// Name identifies this worker in the runner's logs.
func (w *OutlookSweepWorker) Name() string { return outlookSweepName }

// Interval is the worker's cycle, an hour.
func (w *OutlookSweepWorker) Interval() time.Duration { return outlookSweepInterval }

// Run is the worker loop: a cycle, then the interval. A failure is logged
// and the loop goes on — the runner never restarts a worker.
func (w *OutlookSweepWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.Interval())
	defer ticker.Stop()
	for {
		if _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.client.logger.Error("entra: the Outlook sweep failed", "worker", outlookSweepName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle is one cycle under the lease; it reports whether it ran — false
// when another replica holds the lease. The unlock runs on a context
// stripped of cancellation, so a cancelled cycle still gives the lease
// back; an unlock that fails closes the connection, which ends the lock.
func (w *OutlookSweepWorker) RunCycle(ctx context.Context) (bool, error) {
	conn, err := w.deps.Pool.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("entra: acquire a connection for the Outlook sweep's lease: %w", err)
	}
	defer conn.Release()
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, outlookSweepLeaseKey).Scan(&acquired); err != nil {
		return false, fmt.Errorf("entra: take the Outlook sweep's lease: %w", err)
	}
	if !acquired {
		return false, nil
	}
	defer func() {
		release := context.WithoutCancel(ctx)
		if _, err := conn.Exec(release, `SELECT pg_advisory_unlock($1)`, outlookSweepLeaseKey); err != nil {
			w.client.logger.ErrorContext(release, "entra: releasing the Outlook sweep's lease failed; discarding the connection",
				"worker", outlookSweepName, "error", err.Error())
			_ = conn.Conn().Close(release)
		}
	}()

	q := store.New(conn)
	if err := w.deleteSpent(ctx, conn, q); err != nil {
		return true, err
	}
	return true, w.checkAccounts(ctx, conn, q)
}

// deleteSpent deletes the closing connections nothing needs, with their
// events, in one transaction.
func (w *OutlookSweepWorker) deleteSpent(ctx context.Context, on db.TxBeginner, q *store.Queries) error {
	now := w.client.now()
	deleted := 0
	err := db.WithTx(ctx, on, pgx.TxOptions{}, func(tx pgx.Tx) error {
		qtx := q.WithTx(tx)
		rows, err := qtx.DeleteSpentOutlookConnections(ctx, now)
		if err != nil {
			return fmt.Errorf("entra: delete the spent Outlook connections: %w", err)
		}
		for _, r := range rows {
			oid := r.ObjectID
			if err := recordEvent(ctx, qtx, now, outlookActor, actionOutlookDeleted, &oid, accountUPN(ctx, qtx, oid), nil); err != nil {
				return err
			}
		}
		deleted = len(rows)
		return nil
	})
	if err == nil && deleted > 0 {
		w.client.logger.InfoContext(ctx, "entra: spent Outlook connections deleted", "worker", outlookSweepName, "deleted", deleted)
	}
	return err
}

// checkAccounts reads the accounts of the connections waiting in
// reconnect that are due (design M6). An account deleted or no longer
// enabled in Vantigo closes without asking the tenant; otherwise the tenant
// is asked for accountEnabled. A transient failure leaves the rest for the
// next cycle.
func (w *OutlookSweepWorker) checkAccounts(ctx context.Context, on db.TxBeginner, q *store.Queries) error {
	now := w.client.now()
	due, err := q.ListOutlookAccountChecks(ctx, store.ListOutlookAccountChecksParams{Now: now, RowLimit: outlookAccountChecks})
	if err != nil || len(due) == 0 {
		if err != nil {
			return fmt.Errorf("entra: list the Outlook connections' accounts to check: %w", err)
		}
		return nil
	}
	var cred *graphCredential
	for _, d := range due {
		oid := d.ObjectID
		code := ""
		account, err := q.GetAccount(ctx, oid)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			code = outlookClosedDeleted
		case err != nil:
			return fmt.Errorf("entra: read the account: %w", err)
		case account.State == stateDeleted:
			code = outlookClosedDeleted
		case !trusted(account) || account.State != stateEnabled:
			code = outlookClosedDisabled
		default:
			if cred == nil {
				c, err := credentialFor(ctx, q, w.client)
				if err != nil {
					w.client.logger.InfoContext(ctx, "entra: the Outlook sweep cannot read accounts in the tenant", "worker", outlookSweepName, "error", err.Error())
					return nil
				}
				cred = &c
			}
			var u struct {
				AccountEnabled bool `json:"accountEnabled"`
			}
			err := w.client.get(ctx, *cred, userPath(oid)+"?"+url.Values{"$select": {"accountEnabled"}}.Encode(), &u)
			switch {
			case isStatus(err, http.StatusNotFound):
				code = outlookClosedDeleted
			case err != nil:
				if ctx.Err() != nil {
					return ctx.Err()
				}
				w.client.logger.InfoContext(ctx, "entra: the Outlook sweep could not read an account; the rest wait for the next cycle",
					"worker", outlookSweepName, "object_id", oid.String(), "error", err.Error())
				return nil
			case !u.AccountEnabled:
				code = outlookClosedDisabled
			}
		}
		if code == "" {
			if err := q.MarkOutlookAccountChecked(ctx, store.MarkOutlookAccountCheckedParams{Now: now, ObjectID: oid}); err != nil {
				return fmt.Errorf("entra: record the account checked: %w", err)
			}
			continue
		}
		// Guarded by the revision listed: a person who reconnected while the
		// account was read keeps the new connection and its token.
		revision := d.Revision
		if err := closeOutlookIn(ctx, on, q, now, outlookActor, oid, &revision, true, code); err != nil {
			return err
		}
	}
	return nil
}
