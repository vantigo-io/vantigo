package identity

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// Identity's background worker (API access design K15):
//
//   - identity.api-keys audits api_key.expired once for each key that has
//     passed its expiry unrevoked, and deletes a key's row 90 days after it
//     expired; the audit rows stay.

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
)

// workers are identity's background workers, each behind its switch.
func workers(d module.Deps) []worker.Worker {
	if d.Config == nil || !d.Config.IdentityIntegrationsWorkerEnabled {
		return nil
	}
	return []worker.Worker{&apiKeysWorker{deps: d}}
}

// apiKeysWorker is identity.api-keys.
type apiKeysWorker struct{ deps module.Deps }

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

// RunCycle is one cycle under the worker's advisory lease, on the lease's own
// connection, so it never holds one connection while asking the pool for
// another (assistant/workers.go's retentionWorker). It answers how many
// expiries it audited and how many rows it deleted, both zero when another
// replica holds the lease.
func (w *apiKeysWorker) RunCycle(ctx context.Context) (recorded, deleted int, err error) {
	conn, err := w.deps.Pool.Acquire(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("identity: acquire a connection for the API keys lease: %w", err)
	}
	defer conn.Release()
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, apiKeysLeaseKey).Scan(&acquired); err != nil {
		return 0, 0, fmt.Errorf("identity: take the API keys lease: %w", err)
	}
	if !acquired {
		return 0, 0, nil
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
			return recorded, deleted, err
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
			return recorded, deleted, fmt.Errorf("identity: delete the long-expired API keys: %w", err)
		}
		deleted += int(n)
		if n < apiKeysBatch || ctx.Err() != nil {
			return recorded, deleted, nil
		}
	}
}
