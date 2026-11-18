package identity

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// Identity's background worker (API access design K15, K23, K25, K39):
//
//   - identity.api-keys audits api_key.expired once for each key that has
//     passed its expiry unrevoked, and deletes a key's row 90 days after it
//     expired; the audit rows stay.
//   - It keeps the API request log's partitions: it moves what landed in the
//     DEFAULT partition into a partition of its UTC day, makes today's and
//     the next 6 days' partitions, and drops the days older than 30. With
//     the worker switched off, no partition is made and every row lands in
//     DEFAULT, where it stays.
//   - It mails the expiry notices (apikey_notices.go).

const (
	apiKeysWorkerName = "identity.api-keys"
	// apiKeysLeaseKey is the worker's advisory lease, so one replica runs a
	// cycle.
	apiKeysLeaseKey = int64(0x49444150494b4559) // "IDAPIKEY"
	apiKeysBatch    = 200
	// apiKeyRowRetention is how long a key's row outlives its expiry (K12).
	apiKeyRowRetention = 90 * 24 * time.Hour
	// apiKeysSystem is the actor the worker's audit rows name.
	apiKeysSystem = "system:api-keys"
	// apiRequestDaysAhead is how many days' partitions the request log has
	// ready, today's included; apiRequestRetentionDays how many days back it
	// keeps (K23).
	apiRequestDaysAhead     = 7
	apiRequestRetentionDays = 30
)

// workers are identity's background workers, each behind its switch.
func workers(a *Access, d module.Deps) []worker.Worker {
	if d.Config == nil || !d.Config.IdentityIntegrationsWorkerEnabled {
		return nil
	}
	return []worker.Worker{&apiKeysWorker{deps: d, users: newUserDirectory(a, d)}}
}

// apiKeysWorker is identity.api-keys.
type apiKeysWorker struct {
	deps module.Deps
	// users mails the expiry notices, as invitations are mailed.
	users contracts.UserDirectory
}

func (*apiKeysWorker) Name() string              { return apiKeysWorkerName }
func (w *apiKeysWorker) Interval() time.Duration { return w.deps.Config.IdentityIntegrationsWorkerPoll }

func (w *apiKeysWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.Interval())
	defer ticker.Stop()
	for {
		if _, _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.deps.Logger.Error("identity API keys cycle failed", "worker", w.Name(), "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle is one cycle. Under the worker's advisory lease, on the lease's
// own connection, so it never holds one connection while asking the pool for
// another (assistant/workers.go's retentionWorker), it audits the expiries,
// deletes the long-expired keys and keeps the request log's partitions; then,
// with the lease let go, it mails the notices due. It answers how many
// expiries it audited and how many rows it deleted, both zero, and nothing
// done, when another replica holds the lease.
func (w *apiKeysWorker) RunCycle(ctx context.Context) (recorded, deleted int, err error) {
	recorded, deleted, acquired, err := w.leasedCycle(ctx)
	if err != nil || !acquired {
		return recorded, deleted, err
	}
	if _, err := w.sendAPIKeyNotices(ctx, w.deps.Clock()); err != nil {
		return recorded, deleted, err
	}
	return recorded, deleted, nil
}

// leasedCycle is the part of a cycle that runs under the lease.
func (w *apiKeysWorker) leasedCycle(ctx context.Context) (recorded, deleted int, acquired bool, err error) {
	conn, err := w.deps.Pool.Acquire(ctx)
	if err != nil {
		return 0, 0, false, fmt.Errorf("identity: acquire a connection for the API keys lease: %w", err)
	}
	defer conn.Release()
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, apiKeysLeaseKey).Scan(&acquired); err != nil {
		return 0, 0, false, fmt.Errorf("identity: take the API keys lease: %w", err)
	}
	if !acquired {
		return 0, 0, false, nil
	}
	defer func() {
		release := context.WithoutCancel(ctx)
		if _, uerr := conn.Exec(release, `SELECT pg_advisory_unlock($1)`, apiKeysLeaseKey); uerr != nil {
			// A session that still holds the lease must not go back into the pool.
			_ = conn.Conn().Close(release)
		}
	}()
	now := w.deps.Clock()
	for {
		n := 0
		err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			q := store.New(tx)
			keys, err := q.ListUnrecordedExpiredAPIKeys(ctx, store.ListUnrecordedExpiredAPIKeysParams{Now: now, BatchSize: apiKeysBatch})
			if err != nil {
				return fmt.Errorf("identity: read the expired API keys: %w", err)
			}
			for _, k := range keys {
				integration := k.IntegrationID
				if err := writeAudit(ctx, q, nil, now, auditEvent{
					system: apiKeysSystem, targetUser: &integration, action: auditAPIKeyExpired, before: struct{}{},
					after: struct {
						KeyID     string    `json:"keyId"`
						Name      string    `json:"name"`
						ExpiresAt time.Time `json:"expiresAt"`
					}{k.ID.String(), k.Name, k.ExpiresAt},
				}); err != nil {
					return err
				}
				if err := q.MarkAPIKeyExpiryRecorded(ctx, store.MarkAPIKeyExpiryRecordedParams{ID: k.ID, Now: now}); err != nil {
					return fmt.Errorf("identity: mark the API key's expiry audited: %w", err)
				}
			}
			n = len(keys)
			return nil
		})
		if err != nil {
			return recorded, deleted, true, err
		}
		recorded += n
		if n < apiKeysBatch || ctx.Err() != nil {
			break
		}
	}
	q := store.New(conn)
	for {
		n, err := q.DeleteExpiredAPIKeys(ctx, store.DeleteExpiredAPIKeysParams{Cutoff: now.Add(-apiKeyRowRetention), BatchSize: apiKeysBatch})
		if err != nil {
			return recorded, deleted, true, fmt.Errorf("identity: delete the long-expired API keys: %w", err)
		}
		deleted += int(n)
		if n < apiKeysBatch || ctx.Err() != nil {
			break
		}
	}
	return recorded, deleted, true, keepAPIRequestPartitions(ctx, q, now)
}

// keepAPIRequestPartitions keeps the request log's partitions at now (K23,
// K39): each UTC day DEFAULT holds rows of, within the 30 days kept, and
// today with the 6 days after it, get a partition, each in a transaction of
// its own that moves the day's rows out of DEFAULT first; then the days
// before the 30 are dropped, from DEFAULT too.
func keepAPIRequestPartitions(ctx context.Context, q *store.Queries, now time.Time) error {
	today := utcDay(now)
	oldest := today.AddDate(0, 0, -apiRequestRetentionDays)
	days, err := q.ListDefaultAPIRequestDays(ctx, oldest)
	if err != nil {
		return fmt.Errorf("identity: read the request log's DEFAULT partition: %w", err)
	}
	for i := range apiRequestDaysAhead {
		days = append(days, pgDate(today.AddDate(0, 0, i)))
	}
	for _, day := range days {
		if _, err := q.EnsureAPIRequestsPartition(ctx, day); err != nil {
			return fmt.Errorf("identity: make the request log's partition of %s: %w", day.Time.Format(time.DateOnly), err)
		}
	}
	if _, err := q.DropAPIRequestsBefore(ctx, pgDate(oldest)); err != nil {
		return fmt.Errorf("identity: drop the request log's old partitions: %w", err)
	}
	return nil
}

// pgDate is day as a date column.
func pgDate(day time.Time) pgtype.Date { return pgtype.Date{Time: day, Valid: true} }

// utcDay is t's UTC date at midnight.
func utcDay(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
