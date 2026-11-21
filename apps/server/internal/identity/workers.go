package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

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
//     the next 6 days' partitions, and drops what is older than the 30 days
//     kept, today included, which are the days the Requests screen shows. With
//     the worker switched off, no partition is made and every row lands in
//     DEFAULT, where it stays.
//   - It mails the expiry notices (apikey_notices.go).
//   - It prunes the expired Idempotency-Keys (idempotency design I21), under a
//     lease of its own, every cycle, whatever IDEMPOTENCY_ENABLED says, so the
//     keys kept before the switch was turned off still go. The worker runs
//     when IDENTITY_API_KEYS_ENABLED or IDEMPOTENCY_ENABLED is on; the API
//     keys' part only under the first.
//   - It prunes the OAuth connections' rows (OAuth connections design C26,
//     A21), under a lease of its own, every cycle, whatever OAUTH_ENABLED
//     says: codes an hour after they were consumed or expired, tokens a day
//     past their expiry, connections never activated 10 minutes after they
//     were made, connections 90 days after they were revoked or expired, and
//     client documents a week past their expiry. It runs when OAUTH_ENABLED
//     is on too.
//
// identity.webhook-sender sends integrations' webhook messages
// (webhooks_worker.go).

const (
	apiKeysWorkerName = "identity.api-keys"
	// apiKeysLeaseKey is the worker's advisory lease, so one replica runs a
	// cycle.
	apiKeysLeaseKey = int64(0x49444150494b4559) // "IDAPIKEY"
	// idempotencyLeaseKey is the prune's own advisory lease (I21).
	idempotencyLeaseKey = int64(0x494449444d504f54) // "IDIDMPOT"
	// oauthLeaseKey is the OAuth prune's own advisory lease (A21).
	oauthLeaseKey = int64(0x49444f4155544850) // "IDOAUTHP"
	apiKeysBatch  = 200
	// apiKeyRowRetention is how long a key's row outlives its expiry (K12).
	apiKeyRowRetention = 90 * 24 * time.Hour
	// apiKeysSystem is the actor the worker's audit rows name.
	apiKeysSystem = "system:api-keys"
	// apiRequestDaysAhead is how many days' partitions the request log has
	// ready, today's included; apiRequestRetentionDays how many days it keeps,
	// today's included (K23).
	apiRequestDaysAhead     = 7
	apiRequestRetentionDays = 30
)

// workers are identity's background workers, each behind its own switch:
// the API keys worker and the webhook sender (outgoing webhooks design W29)
// run or not independently of each other.
func workers(a *Access, d module.Deps) []worker.Worker {
	if d.Config == nil {
		return nil
	}
	var out []worker.Worker
	if d.Config.IdentityIntegrationsWorkerEnabled || d.Config.IdempotencyEnabled || d.Config.OAuthEnabled {
		out = append(out, &apiKeysWorker{
			deps: d, users: &userDirectory{q: store.New(d.Pool), sso: a.sso, mail: d.Mail},
			keys: d.Config.IdentityIntegrationsWorkerEnabled,
		})
	}
	if d.Config.IdentityWebhooksEnabled {
		out = append(out, newWebhookSender(a, d))
	}
	return out
}

// apiKeysWorker is identity.api-keys.
type apiKeysWorker struct {
	deps module.Deps
	// users mails the expiry notices, as invitations are mailed, and says
	// whether each was sent.
	users *userDirectory
	// keys is IDENTITY_API_KEYS_ENABLED: the expiries, the request log's
	// partitions and the notices. The Idempotency-Keys' prune runs whatever
	// it says.
	keys bool
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
// with the lease let go, it mails the notices due. A failure in one part is
// logged and does not keep the next from running; the cycle answers them
// joined. It answers how many expiries it audited and how many rows it
// deleted, both zero, and nothing done, when another replica holds the lease.
//
// The prune of the expired Idempotency-Keys runs after, under its own lease,
// every cycle; the API keys' part runs only when IDENTITY_API_KEYS_ENABLED is.
// A failure is answered, and Run logs it once.
func (w *apiKeysWorker) RunCycle(ctx context.Context) (recorded, deleted int, err error) {
	if w.keys {
		var acquired bool
		recorded, deleted, acquired, err = w.leasedCycle(ctx)
		if acquired && ctx.Err() == nil {
			if _, nerr := w.sendAPIKeyNotices(ctx, w.deps.Clock()); nerr != nil {
				err = errors.Join(err, nerr)
			}
		}
	}
	if ctx.Err() == nil {
		if _, perr := w.pruneIdempotency(ctx); perr != nil {
			err = errors.Join(err, perr)
		}
	}
	if ctx.Err() == nil {
		if _, perr := w.pruneOAuth(ctx); perr != nil {
			err = errors.Join(err, perr)
		}
	}
	return recorded, deleted, err
}

// The OAuth prune's bounds (C26, A21).
const (
	oauthCodeRetention        = time.Hour
	oauthTokenRetention       = 24 * time.Hour
	oauthUnactivatedRetention = 10 * time.Minute
	oauthEndedRetention       = 90 * 24 * time.Hour
	oauthDocumentRetention    = 7 * 24 * time.Hour
)

// pruneOAuth deletes the OAuth rows past their keeping under the prune's
// lease, on the lease's own connection, and answers how many; nothing when
// another replica holds the lease.
func (w *apiKeysWorker) pruneOAuth(ctx context.Context) (int, error) {
	conn, err := w.deps.Pool.Acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("identity: acquire a connection for the OAuth lease: %w", err)
	}
	defer conn.Release()
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, oauthLeaseKey).Scan(&acquired); err != nil {
		return 0, fmt.Errorf("identity: take the OAuth lease: %w", err)
	}
	if !acquired {
		return 0, nil
	}
	defer func() {
		release := context.WithoutCancel(ctx)
		if _, uerr := conn.Exec(release, `SELECT pg_advisory_unlock($1)`, oauthLeaseKey); uerr != nil {
			// A session that still holds the lease must not go back into the pool.
			_ = conn.Conn().Close(release)
		}
	}()
	return pruneOAuthRows(ctx, store.New(conn), w.deps.Clock())
}

// pruneOAuthRows is the OAuth prune at now on q.
func pruneOAuthRows(ctx context.Context, q *store.Queries, now time.Time) (int, error) {
	total := 0
	for _, step := range []struct {
		what string
		run  func() (int64, error)
	}{
		{"codes", func() (int64, error) { return q.DeleteSpentOAuthCodes(ctx, now.Add(-oauthCodeRetention)) }},
		{"tokens", func() (int64, error) { return q.DeleteExpiredOAuthTokens(ctx, now.Add(-oauthTokenRetention)) }},
		{"unactivated connections", func() (int64, error) {
			return q.DeleteUnactivatedOAuthConnections(ctx, now.Add(-oauthUnactivatedRetention))
		}},
		{"ended connections", func() (int64, error) { return q.DeleteEndedOAuthConnections(ctx, now.Add(-oauthEndedRetention)) }},
		{"client documents", func() (int64, error) { return q.DeleteStaleOAuthClientDocuments(ctx, now.Add(-oauthDocumentRetention)) }},
	} {
		n, err := step.run()
		if err != nil {
			return total, fmt.Errorf("identity: prune the OAuth %s: %w", step.what, err)
		}
		total += int(n)
	}
	return total, nil
}

// pruneIdempotency deletes the expired Idempotency-Keys under the prune's
// lease, on the lease's own connection, and answers how many; nothing when
// another replica holds the lease.
func (w *apiKeysWorker) pruneIdempotency(ctx context.Context) (int, error) {
	conn, err := w.deps.Pool.Acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("identity: acquire a connection for the idempotency lease: %w", err)
	}
	defer conn.Release()
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, idempotencyLeaseKey).Scan(&acquired); err != nil {
		return 0, fmt.Errorf("identity: take the idempotency lease: %w", err)
	}
	if !acquired {
		return 0, nil
	}
	defer func() {
		release := context.WithoutCancel(ctx)
		if _, uerr := conn.Exec(release, `SELECT pg_advisory_unlock($1)`, idempotencyLeaseKey); uerr != nil {
			// A session that still holds the lease must not go back into the pool.
			_ = conn.Conn().Close(release)
		}
	}()
	return pruneIdempotencyKeys(ctx, store.New(conn), w.deps.Clock())
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
	recorded, deleted, err = w.expireKeys(ctx, conn, now)
	if err != nil {
		w.deps.Logger.Error("identity: API key expiry failed", "worker", w.Name(), "error", err)
	}
	if ctx.Err() == nil {
		if perr := w.keepAPIRequestPartitions(ctx, store.New(conn), now); perr != nil {
			err = errors.Join(err, perr)
		}
	}
	return recorded, deleted, true, err
}

// expireKeys audits each key that expired once and deletes the rows of keys
// 90 days past their expiry, on the lease's connection.
func (w *apiKeysWorker) expireKeys(ctx context.Context, conn *pgxpool.Conn, now time.Time) (recorded, deleted int, err error) {
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

// keepAPIRequestPartitions keeps the request log's partitions at now (K23,
// K39): each UTC day DEFAULT holds rows of, within the 30 days kept, and
// today with the 6 days after it, get a partition, each in a transaction of
// its own that moves the day's rows out of DEFAULT first; then the days
// before the 30 are dropped, from DEFAULT too. A day that fails — its lock
// not had within the function's lock_timeout, say — is logged and tried
// again next cycle, and the other days go on; the failures are answered
// joined.
func (w *apiKeysWorker) keepAPIRequestPartitions(ctx context.Context, q *store.Queries, now time.Time) error {
	today := utcDay(now)
	oldest := apiRequestOldestDay(now)
	days, err := q.ListDefaultAPIRequestDays(ctx, oldest)
	if err != nil {
		err = fmt.Errorf("identity: read the request log's DEFAULT partition: %w", err)
		w.deps.Logger.Error("identity: request log upkeep failed", "worker", w.Name(), "error", err)
		days = nil
	}
	for i := range apiRequestDaysAhead {
		days = append(days, pgDate(today.AddDate(0, 0, i)))
	}
	for _, day := range days {
		if ctx.Err() != nil {
			return errors.Join(err, ctx.Err())
		}
		if _, derr := q.EnsureAPIRequestsPartition(ctx, day); derr != nil {
			derr = fmt.Errorf("identity: make the request log's partition of %s: %w", day.Time.Format(time.DateOnly), derr)
			w.deps.Logger.Error("identity: request log upkeep failed", "worker", w.Name(), "error", derr)
			err = errors.Join(err, derr)
		}
	}
	if _, derr := q.DropAPIRequestsBefore(ctx, pgDate(oldest)); derr != nil {
		derr = fmt.Errorf("identity: drop the request log's old partitions: %w", derr)
		w.deps.Logger.Error("identity: request log upkeep failed", "worker", w.Name(), "error", derr)
		err = errors.Join(err, derr)
	}
	return err
}

// apiRequestOldestDay is the first UTC day the request log keeps at now: 30
// days, today included.
func apiRequestOldestDay(now time.Time) time.Time {
	return utcDay(now).AddDate(0, 0, -(apiRequestRetentionDays - 1))
}

// pgDate is day as a date column.
func pgDate(day time.Time) pgtype.Date { return pgtype.Date{Time: day, Valid: true} }

// utcDay is t's UTC date at midnight.
func utcDay(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
