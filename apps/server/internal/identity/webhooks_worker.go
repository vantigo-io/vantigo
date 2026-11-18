package identity

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/secrets"
)

// The webhook sender, identity.webhook-sender (outgoing webhooks design W15
// to W19, W29 to W33, W37, W38). Each pass it claims due deliveries under a
// lease, at most 2 per endpoint and never more than 4 of one endpoint in
// flight across every process, and sends them on goroutines, at most 16 at
// once; the pass does not wait for them, so a slow endpoint holds only its
// own slots. Each result is written on a short transaction of its own, only
// while the claim's lease holds. A failure is retried on W17's schedule; a
// 410 Gone disables the endpoint; an endpoint failing for 5 days with 5
// deliveries dead by running out of attempts is disabled too, and the
// integration's contact and every Owner are told once. Once a minute it
// expires what has been pending for 3 days and deletes what is older than
// 30 days.
//
// It never holds one pooled connection while it asks for another: the
// claim, each send and each result are separate uses, and no mail is sent
// while a transaction is open, so it runs on a pool of one.

const (
	webhookSenderName = "identity.webhook-sender"
	// webhookSenderSystem is the actor of the sender's audit rows.
	webhookSenderSystem = "system:webhooks"
	// webhookLease is how long a claim holds a delivery: longer than an
	// attempt's whole budget (W15).
	webhookLease = 2 * time.Minute
	// webhookPerEndpoint is the most deliveries of one endpoint in flight,
	// webhookPerPass the most a pass claims of one, and webhookConcurrency
	// the most sends one process has in flight (W29).
	webhookPerEndpoint = 4
	webhookPerPass     = 2
	webhookConcurrency = 16
	// webhookReserve is how many of the process's slots only an endpoint
	// with nothing in flight may take, so endpoints that hang never hold
	// every slot and starve the rest (S1).
	webhookReserve = 4
	// webhookSkippedEvery is how often the fan-out records an event an
	// integration may no longer receive, per endpoint and event (S5).
	webhookSkippedEvery = 24 * time.Hour
	// webhookNoticeGiveUp is how long after its disabling a notice no one
	// could be mailed is still tried; webhookNoticeBackoff and
	// webhookNoticeBackoffMax the wait between its tries in one process.
	webhookNoticeGiveUp     = 7 * 24 * time.Hour
	webhookNoticeBackoff    = time.Minute
	webhookNoticeBackoffMax = time.Hour
	// webhookPendingLimit is how long a delivery may stay pending before it
	// expires; webhookRetention how long any delivery is kept;
	// webhookStaleOnEnable how old a delivery that queued while its
	// integration was disabled may be and still be sent (W30).
	webhookPendingLimit  = 3 * 24 * time.Hour
	webhookRetention     = 30 * 24 * time.Hour
	webhookStaleOnEnable = 24 * time.Hour
	// An endpoint failing for webhookFailingFor with webhookFailingDead
	// deliveries dead by running out of attempts is disabled (W18).
	webhookFailingFor  = 5 * 24 * time.Hour
	webhookFailingDead = 5
	// webhookBatch bounds what one statement expires or deletes;
	// webhookNoticeBatch the notices one sweep mails.
	webhookBatch       = 1000
	webhookNoticeBatch = 50
	// webhookHousekeepingEvery is how often a sender expires, prunes and
	// mails the notices still owed.
	webhookHousekeepingEvery = time.Minute
	// webhookJitter is the share a retry's delay is moved by, either way.
	webhookJitter = 0.1
)

// webhookRetrySchedule is the wait after the n-th failed attempt of a run,
// n from 1 (W17): with the first attempt at once, 8 attempts in about 45
// hours.
var webhookRetrySchedule = []time.Duration{
	30 * time.Second, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour,
}

// webhookSender is identity.webhook-sender.
type webhookSender struct {
	deps    module.Deps
	pool    *pgxpool.Pool
	q       *store.Queries
	client  *webhookClient
	users   *userDirectory
	catalog map[string]contracts.Permission
	clock   func() time.Time
	// jitter is in [0, 1); a test fixes it.
	jitter func() float64
	// slots holds a token per send in flight.
	slots chan struct{}
	sends sync.WaitGroup
	// wake is nudged when a send finishes, so Run passes again at once
	// instead of waiting for the next tick to use the slot it freed.
	wake chan struct{}

	mu          sync.Mutex
	housekeptAt time.Time
	// noticeRetry is when the sweep may next try an endpoint's notice that
	// no one could be mailed, and the wait it doubles from (W32).
	noticeRetry map[uuid.UUID]webhookNoticeRetry
}

// webhookNoticeRetry is an unmailed notice's backoff in this process.
type webhookNoticeRetry struct {
	at   time.Time
	wait time.Duration
}

func newWebhookSender(a *Access, d module.Deps) *webhookSender {
	clock := d.Clock
	if clock == nil {
		clock = time.Now
	}
	return &webhookSender{
		deps:    d,
		pool:    d.Pool,
		q:       store.New(d.Pool),
		client:  newWebhookClient(a, clock, d.Logger),
		users:   &userDirectory{q: store.New(d.Pool), sso: a.sso, mail: d.Mail},
		catalog: d.Catalog,
		clock:   clock,
		jitter:  rand.Float64,
		slots:   make(chan struct{}, webhookConcurrency),
		wake:    make(chan struct{}, 1),

		noticeRetry: map[uuid.UUID]webhookNoticeRetry{},
	}
}

func (*webhookSender) Name() string              { return webhookSenderName }
func (w *webhookSender) Interval() time.Duration { return w.deps.Config.IdentityWebhooksPoll }

// Run passes until ctx is done, then waits for the sends in flight, which
// ctx's end aborts and which record nothing. The poll is the idle cadence:
// a pass that leased all it could — the process's free slots, or a pass's
// share of some endpoint — is followed by the next at once, and a send that
// finishes wakes the loop, so a fast receiver is not held to two messages a
// poll.
func (w *webhookSender) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.Interval())
	defer ticker.Stop()
	defer w.Wait()
	for {
		_, more, err := w.pass(ctx)
		if err != nil && ctx.Err() == nil {
			w.deps.Logger.Error("identity webhook sender pass failed", "worker", w.Name(), "error", err)
		}
		if more && err == nil && ctx.Err() == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		case <-w.wake:
		}
	}
}

// Wait blocks until every send the passes started has recorded its result.
func (w *webhookSender) Wait() { w.sends.Wait() }

// RunPass is one pass: claim and start the sends due, disable the endpoints
// that have failed long enough, and, once a minute, housekeep. It answers how
// many sends it started, without waiting for them; a failure in one part does
// not keep the next from running, and the pass answers them joined.
func (w *webhookSender) RunPass(ctx context.Context) (int, error) {
	started, _, err := w.pass(ctx)
	return started, err
}

// pass is RunPass, also answering whether the claim leased all it could, so
// more may be due at once.
func (w *webhookSender) pass(ctx context.Context) (int, bool, error) {
	started, more, err := w.claimAndSend(ctx)
	if ctx.Err() != nil {
		return started, more, err
	}
	if derr := w.disableFailing(ctx); derr != nil {
		err = errors.Join(err, derr)
	}
	now := w.clock()
	w.mu.Lock()
	due := w.housekeptAt.IsZero() || now.Sub(w.housekeptAt) >= webhookHousekeepingEvery
	if due {
		w.housekeptAt = now
	}
	w.mu.Unlock()
	if due && ctx.Err() == nil {
		if herr := w.Housekeep(ctx); herr != nil {
			err = errors.Join(err, herr)
		}
	}
	return started, more, err
}

// webhookClaim is one leased delivery and what its attempt needs of its
// endpoint, read when it was claimed.
type webhookClaim struct {
	id, endpoint, lease uuid.UUID
	kind, eventName     string
	body                string
	runAttempts, maxRun int32
	url                 string
	secret, previous    []byte
	previousUntil       *time.Time
}

// claimAndSend claims as many due deliveries as the process has free slots
// for, in one short transaction (W29), and starts a send for each. It
// answers whether the claim leased all it could, so more may be due.
func (w *webhookSender) claimAndSend(ctx context.Context) (int, bool, error) {
	free := cap(w.slots) - len(w.slots)
	if free <= 0 {
		return 0, false, nil
	}
	claimed, more, err := w.claim(ctx, free)
	if err != nil {
		return 0, false, err
	}
	for _, c := range claimed {
		w.slots <- struct{}{}
		w.sends.Add(1)
		go func() {
			defer w.sends.Done()
			defer func() {
				<-w.slots
				select {
				case w.wake <- struct{}{}:
				default:
				}
			}()
			w.attempt(ctx, c)
		}()
	}
	return len(claimed), more, nil
}

// claim is W29's claim at the clock's now, at most limit deliveries: (a)
// the candidate endpoints locked FOR UPDATE SKIP LOCKED, then (b-d), in a
// statement of its own, their unexpired leases counted and their due
// deliveries ranked and leased. Before it commits, each integration's
// permission is read once and the leased events it no longer may receive
// are settled skipped instead (W33).
//
// It also answers whether it leased all it could — limit in all, or a pass's
// share of some endpoint — so the next pass may run at once.
func (w *webhookSender) claim(ctx context.Context, limit int) ([]webhookClaim, bool, error) {
	now := w.clock()
	lease := uuid.New()
	var out []webhookClaim
	more := false
	err := pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		endpoints, err := q.LockDueWebhookEndpoints(ctx, now)
		if err != nil {
			return fmt.Errorf("identity: lock the webhook endpoints due: %w", err)
		}
		if len(endpoints) == 0 {
			return nil
		}
		byID := make(map[uuid.UUID]store.LockDueWebhookEndpointsRow, len(endpoints))
		ids := make([]uuid.UUID, 0, len(endpoints))
		for _, e := range endpoints {
			byID[e.ID] = e
			ids = append(ids, e.ID)
		}
		rows, err := q.LeaseWebhookDeliveries(ctx, store.LeaseWebhookDeliveriesParams{
			LeaseID: &lease, LeaseUntil: now.Add(webhookLease), Now: now, EndpointIds: ids,
			PerPass: webhookPerPass, PerEndpoint: webhookPerEndpoint, BatchSize: int32(min(limit, 1<<20)), //nolint:gosec // bounded
			Reserve: webhookReserve,
		})
		if err != nil {
			return fmt.Errorf("identity: lease the webhook deliveries: %w", err)
		}
		perEndpoint := map[uuid.UUID]int{}
		for _, d := range rows {
			perEndpoint[d.EndpointID]++
			if perEndpoint[d.EndpointID] >= webhookPerPass {
				more = true
			}
		}
		if len(rows) >= limit {
			more = true
		}
		held := map[uuid.UUID][]string{}
		var skipped []uuid.UUID
		for _, d := range rows {
			e := byID[d.EndpointID]
			if d.Kind == webhookKindEvent {
				ok, err := w.stillPermitted(ctx, q, held, e.IntegrationID, d.EventName)
				if err != nil {
					return err
				}
				if !ok {
					skipped = append(skipped, d.ID)
					continue
				}
			}
			out = append(out, webhookClaim{
				id: d.ID, endpoint: d.EndpointID, lease: lease, kind: d.Kind, eventName: d.EventName, body: d.Body,
				runAttempts: d.RunAttempts, maxRun: d.MaxRunAttempts, url: e.Url,
				secret: e.SecretCiphertext, previous: e.PreviousSecretCiphertext, previousUntil: e.PreviousSecretExpiresAt,
			})
		}
		if len(skipped) > 0 {
			if _, err := q.SkipWebhookDeliveries(ctx, store.SkipWebhookDeliveriesParams{Now: now, Ids: skipped, LeaseID: &lease}); err != nil {
				return fmt.Errorf("identity: skip the webhook deliveries no longer permitted: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return out, more, nil
}

// stillPermitted reports whether the integration still may receive the
// event named name (W7, W33), reading its permissions once per claim into
// held. An event the catalogue no longer has is not permitted.
func (w *webhookSender) stillPermitted(ctx context.Context, q *store.Queries, held map[uuid.UUID][]string, integration uuid.UUID, name string) (bool, error) {
	entry, ok := contracts.LookupWebhookEvent(name)
	if !ok {
		return false, nil
	}
	keys, ok := held[integration]
	if !ok {
		var err error
		keys, err = q.EffectivePermissionsForUser(ctx, integration)
		if err != nil {
			return false, fmt.Errorf("identity: read the integration's permissions: %w", err)
		}
		held[integration] = keys
	}
	return webhookPermitted(w.catalog, keys, entry), nil
}

// attempt sends one claimed delivery and records what happened (W16). The
// previous secret signs beside the current one only while its overlap is
// after the attempt's time (W9). An attempt that ctx's end aborted records
// nothing: its lease runs out and the next claim sends it again.
//
// A current secret that does not open — APP_SECRET changed, a row damaged —
// sends nothing and is recorded as a failed attempt, error other, so the
// run backs off and dies, and the endpoint can be disabled as failing,
// rather than being claimed again every lease.
func (w *webhookSender) attempt(ctx context.Context, c webhookClaim) {
	at := w.clock()
	key, err := openWebhookKey(w.deps.Secrets, c.endpoint, c.secret)
	if err != nil {
		w.deps.Logger.Error("identity: a webhook secret could not be opened", "worker", w.Name(), "endpoint", c.endpoint)
		if rerr := w.record(context.WithoutCancel(ctx), c, webhookAttempt{at: at, errKind: webhookErrOther}, webhookSecretUsedCurrent); rerr != nil {
			w.deps.Logger.Error("identity: a webhook attempt could not be recorded", "worker", w.Name(), "delivery", c.id, "error", rerr)
		}
		return
	}
	keys, used := [][]byte{key}, webhookSecretUsedCurrent
	if c.previous != nil && c.previousUntil != nil && c.previousUntil.After(at) {
		if pkey, err := openWebhookKey(w.deps.Secrets, c.endpoint, c.previous); err == nil {
			keys, used = append(keys, pkey), webhookSecretUsedBoth
		}
	}
	result := w.client.send(ctx, webhookMessage{url: c.url, id: webhookMessageID(c.id), body: []byte(c.body), keys: keys})
	if result.aborted {
		return
	}
	// The attempt happened: its result is written even if ctx ends now.
	if err := w.record(context.WithoutCancel(ctx), c, result, used); err != nil {
		w.deps.Logger.Error("identity: a webhook attempt could not be recorded", "worker", w.Name(), "delivery", c.id, "error", err)
	}
}

// openWebhookKey opens a sealed secret of the endpoint into its HMAC key.
func openWebhookKey(box *secrets.Box, endpoint uuid.UUID, sealed []byte) ([]byte, error) {
	secret, err := openWebhookSecret(box, endpoint, sealed)
	if err != nil {
		return nil, err
	}
	return webhookSecretKey(secret)
}

// nextAttempt is when a run that has failed attempts times retries after a
// failure at at: W17's step jittered ±10%, or what the receiver's
// Retry-After asked for when that is later (W16).
func (w *webhookSender) nextAttempt(at time.Time, attempts int32, retryAfter time.Duration) time.Time {
	step := webhookRetrySchedule[min(int(attempts), len(webhookRetrySchedule))-1]
	delay := time.Duration(float64(step) * (1 - webhookJitter + 2*webhookJitter*w.jitter())).Round(time.Millisecond)
	return at.Add(max(delay, retryAfter))
}

// record writes an attempt's result on one short transaction (W38): the
// endpoint is held first, as every writer that settles its deliveries holds
// it; the delivery is updated only while the claim's lease holds it, and the
// attempt is inserted only if it was. A 410 disables the endpoint (gone),
// and its notice is mailed once the transaction is over.
func (w *webhookSender) record(ctx context.Context, c webhookClaim, a webhookAttempt, used string) error {
	attempts := c.runAttempts + 1
	params := store.RecordWebhookDeliveryResultParams{ID: c.id, LeaseID: &c.lease}
	if a.status != 0 {
		status := int16(a.status) //nolint:gosec // an HTTP status
		params.LastStatus = &status
	}
	if a.errKind != "" {
		params.LastError = &a.errKind
	}
	gone := a.status == 410
	switch {
	case a.succeeded():
		params.State, params.CompletedAt = webhookSucceeded, &a.at
	case gone:
		reason := webhookReasonGone
		params.State, params.StateReason, params.CompletedAt = webhookDead, &reason, &a.at
	case attempts >= c.maxRun:
		reason := webhookReasonExhausted
		params.State, params.StateReason, params.CompletedAt = webhookDead, &reason, &a.at
	default:
		next := w.nextAttempt(a.at, attempts, a.retryAfter)
		params.State, params.NextAttemptAt = webhookPending, &next
	}
	disabled := false
	err := pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		e, err := q.LockWebhookEndpointByID(ctx, c.endpoint)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // deleted mid-attempt: nothing is kept (§8)
		}
		if err != nil {
			return fmt.Errorf("identity: hold the webhook endpoint: %w", err)
		}
		n, err := q.RecordWebhookDeliveryResult(ctx, params)
		if err != nil {
			return fmt.Errorf("identity: record the webhook delivery: %w", err)
		}
		if n == 0 {
			return nil // the lease was lost: not an attempt (W29)
		}
		var excerpt *string
		if a.excerpt != "" {
			excerpt = &a.excerpt
		}
		if err := q.InsertWebhookAttempt(ctx, store.InsertWebhookAttemptParams{
			DeliveryID: c.id, At: a.at, StatusCode: params.LastStatus, DurationMs: int32(min(a.duration.Milliseconds(), 1<<30)), //nolint:gosec // bounded
			Error: params.LastError, Excerpt: excerpt, SecretUsed: used,
		}); err != nil {
			return fmt.Errorf("identity: record the webhook attempt: %w", err)
		}
		// A ping is never counted toward disabling the endpoint (W19).
		if c.kind == webhookKindEvent {
			if a.succeeded() {
				err = q.RecordWebhookEndpointSuccess(ctx, store.RecordWebhookEndpointSuccessParams{At: a.at, ID: e.ID})
			} else {
				err = q.RecordWebhookEndpointFailure(ctx, store.RecordWebhookEndpointFailureParams{At: a.at, ID: e.ID})
			}
			if err != nil {
				return fmt.Errorf("identity: record the webhook endpoint's health: %w", err)
			}
		}
		if gone && e.Status == webhookEnabled {
			if _, err := disableWebhookEndpoint(ctx, q, w.deps.Secrets, nil, e, webhookReasonGone, webhookReasonEndpointClosed,
				auditEvent{system: webhookSenderSystem}, a.at); err != nil {
				return err
			}
			disabled = true
		}
		return nil
	})
	if err != nil {
		return err
	}
	if disabled {
		if _, err := w.sendWebhookNotice(ctx, c.endpoint); err != nil {
			return err
		}
	}
	return nil
}

// disableFailing disables, as failing, every enabled endpoint failing for 5
// days with 5 deliveries dead by running out of attempts since (W18, W32),
// each on its own transaction that holds it first, and mails each notice
// once that transaction is over.
func (w *webhookSender) disableFailing(ctx context.Context) error {
	now := w.clock()
	cutoff := now.Add(-webhookFailingFor)
	ids, err := w.q.ListFailingWebhookEndpoints(ctx, store.ListFailingWebhookEndpointsParams{Cutoff: cutoff, MinDead: webhookFailingDead})
	if err != nil {
		return fmt.Errorf("identity: read the failing webhook endpoints: %w", err)
	}
	var errs error
	for _, id := range ids {
		disabled := false
		err := pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
			q := store.New(tx)
			e, err := q.LockFailingWebhookEndpoint(ctx, store.LockFailingWebhookEndpointParams{ID: id, Cutoff: cutoff, MinDead: webhookFailingDead})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("identity: hold the failing webhook endpoint: %w", err)
			}
			if _, err := disableWebhookEndpoint(ctx, q, w.deps.Secrets, nil, e, webhookReasonFailing, webhookReasonEndpointClosed,
				auditEvent{system: webhookSenderSystem}, now); err != nil {
				return err
			}
			disabled = true
			return nil
		})
		if err == nil && disabled {
			_, err = w.sendWebhookNotice(ctx, id)
		}
		errs = errors.Join(errs, err)
	}
	return errs
}

// Housekeep expires the deliveries pending for 3 days, deletes those older
// than 30 days, each a batch at a time, and mails the notices still owed
// (W30, W32).
func (w *webhookSender) Housekeep(ctx context.Context) error {
	now := w.clock()
	var errs error
	for ctx.Err() == nil {
		n, err := w.q.ExpireWebhookDeliveries(ctx, store.ExpireWebhookDeliveriesParams{Now: now, Cutoff: now.Add(-webhookPendingLimit), BatchSize: webhookBatch})
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("identity: expire the webhook deliveries: %w", err))
			break
		}
		if n < webhookBatch {
			break
		}
	}
	for ctx.Err() == nil {
		n, err := w.q.PruneWebhookDeliveries(ctx, store.PruneWebhookDeliveriesParams{Cutoff: now.Add(-webhookRetention), BatchSize: webhookBatch})
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("identity: prune the webhook deliveries: %w", err))
			break
		}
		if n < webhookBatch {
			break
		}
	}
	// The notices no one could be mailed yet: none when the installation
	// has no mail, none for an endpoint disabled more than a week ago, and
	// each tried again only once its backoff has passed.
	if w.deps.Mail == nil {
		return errs
	}
	due, err := w.q.ListWebhookNoticesDue(ctx, store.ListWebhookNoticesDueParams{Since: now.Add(-webhookNoticeGiveUp), BatchSize: webhookNoticeBatch})
	if err != nil {
		return errors.Join(errs, fmt.Errorf("identity: read the webhook notices owed: %w", err))
	}
	for _, id := range due {
		w.mu.Lock()
		retry, waiting := w.noticeRetry[id]
		w.mu.Unlock()
		if waiting && now.Before(retry.at) {
			continue
		}
		if _, err := w.sendWebhookNotice(ctx, id); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	return errs
}

// noteNoticeTried records how an endpoint's notice went: mailed, or not
// owed, forgets it; mailed to no one doubles its wait before the sweep
// tries again, from a minute up to an hour.
func (w *webhookSender) noteNoticeTried(endpoint uuid.UUID, mailed bool, now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if mailed {
		delete(w.noticeRetry, endpoint)
		return
	}
	wait := webhookNoticeBackoff
	if prev, ok := w.noticeRetry[endpoint]; ok {
		wait = min(prev.wait*2, webhookNoticeBackoffMax)
	}
	w.noticeRetry[endpoint] = webhookNoticeRetry{at: now.Add(wait), wait: wait}
}

// sendWebhookNotice mails the notice of an endpoint the sender disabled to
// the integration's contact and every Owner, in each one's language, as
// sendAPIKeyNotice mails a key's (W32): claimed first, by its
// disabled_notice_sent_at, which another sender cannot claim again; mailed
// with no transaction open; and taken back when no one was mailed, so it is
// tried again. Re-enabling the endpoint clears the column, so a later
// disabling sends a notice again.
func (w *webhookSender) sendWebhookNotice(ctx context.Context, endpoint uuid.UUID) (bool, error) {
	// The column keeps microseconds; the claim is taken back by its value.
	now := w.clock().UTC().Truncate(time.Microsecond)
	row, err := w.q.ClaimWebhookNotice(ctx, store.ClaimWebhookNoticeParams{Now: now, ID: endpoint})
	if errors.Is(err, pgx.ErrNoRows) {
		w.noteNoticeTried(endpoint, true, now)
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("identity: claim the webhook notice: %w", err)
	}
	owners, err := w.q.ListOwnerIDs(ctx)
	if err != nil {
		w.releaseWebhookNotice(ctx, endpoint, now)
		return false, fmt.Errorf("identity: read the Owners: %w", err)
	}
	recipients := slices.Clone(owners)
	if row.ContactUserID != nil && !slices.Contains(recipients, *row.ContactUserID) {
		recipients = append([]uuid.UUID{*row.ContactUserID}, recipients...)
	}
	notice := webhookDisabledNotice(row)
	mailed := 0
	for _, id := range recipients {
		sent, err := w.users.notify(ctx, id, notice)
		if err != nil {
			w.deps.Logger.Warn("identity: a webhook notice could not be mailed", "endpoint", endpoint, "error", err)
		}
		if sent {
			mailed++
		}
	}
	w.noteNoticeTried(endpoint, mailed > 0, now)
	if mailed == 0 {
		w.releaseWebhookNotice(ctx, endpoint, now)
		return false, nil
	}
	return true, nil
}

func (w *webhookSender) releaseWebhookNotice(ctx context.Context, endpoint uuid.UUID, claimed time.Time) {
	if err := w.q.ReleaseWebhookNotice(context.WithoutCancel(ctx), store.ReleaseWebhookNoticeParams{ID: endpoint, ClaimedAt: claimed}); err != nil {
		w.deps.Logger.Error("identity: an unsent webhook notice could not be taken back", "endpoint", endpoint, "error", err)
	}
}

// webhookDisabledNotice is the mail of an endpoint's disabling, in English
// and Norwegian bokmål. It names the endpoint by its URL's scheme and host
// only, never its path or query (W28).
//
// The integration's name is an Owner's text: its control and format
// characters become spaces, so no line break or bidirectional override
// reaches a subject or a body. The mail senders encode the subject header
// themselves (RFC 2047).
func webhookDisabledNotice(row store.ClaimWebhookNoticeRow) contracts.Notice {
	row.IntegrationName = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, row.IntegrationName))
	origin := webhookURLOrigin(row.Url)
	gone := row.DisabledReason != nil && *row.DisabledReason == webhookReasonGone
	enWhy := "its messages have failed for 5 days, and at least 5 of them were given up after every retry."
	nbWhy := "meldingene til det har feilet i 5 dager, og minst 5 av dem ble gitt opp etter alle nye forsøk."
	if gone {
		enWhy = "it answered 410 Gone, which says the address is no longer in use."
		nbWhy = "det svarte 410 Gone, som sier at adressen ikke lenger er i bruk."
	}
	en := strings.Join([]string{
		fmt.Sprintf("The webhook endpoint at %s of the integration “%s” has been disabled: %s Nothing more is sent to it, and the messages that were waiting for it were dropped.", origin, row.IntegrationName, enWhy),
		"When the receiver works again, enable the endpoint on the integration's Webhooks tab under Workspace admin › API access. A test event is sent to it at once.",
	}, "\n\n")
	nb := strings.Join([]string{
		fmt.Sprintf("Webhook-endepunktet på %s for integrasjonen «%s» er deaktivert: %s Ingenting mer sendes til det, og meldingene som ventet på det, er forkastet.", origin, row.IntegrationName, nbWhy),
		"Når mottakeren virker igjen, aktiverer du endepunktet i fanen Webhooks for integrasjonen under Administrer arbeidsområde › API-tilgang. En testhendelse sendes til det med en gang.",
	}, "\n\n")
	return contracts.Notice{Texts: map[string]contracts.NoticeText{
		"en": {Subject: "A webhook endpoint of " + row.IntegrationName + " was disabled", Body: en},
		"nb": {Subject: "Et webhook-endepunkt for " + row.IntegrationName + " er deaktivert", Body: nb},
	}}
}

// expireStaleWebhookDeliveries settles as dead (expired), on q, the pending
// deliveries of an integration being enabled again that have been due for
// more than 24 hours — overdue because it was disabled (W2, W30): its
// endpoints are held first, in id order (W31).
func expireStaleWebhookDeliveries(ctx context.Context, q *store.Queries, integration uuid.UUID, now time.Time) error {
	endpoints, err := q.LockIntegrationWebhookEndpoints(ctx, integration)
	if err != nil {
		return fmt.Errorf("identity: read the integration's webhook endpoints: %w", err)
	}
	if len(endpoints) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(endpoints))
	for _, e := range endpoints {
		ids = append(ids, e.ID)
	}
	if _, err := q.DeadLetterStaleWebhookDeliveries(ctx, store.DeadLetterStaleWebhookDeliveriesParams{
		EndpointIds: ids, Cutoff: now.Add(-webhookStaleOnEnable), Now: now,
	}); err != nil {
		return fmt.Errorf("identity: expire the integration's stale webhook deliveries: %w", err)
	}
	return nil
}
