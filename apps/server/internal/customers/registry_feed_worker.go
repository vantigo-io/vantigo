package customers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the feed worker (registry workers design D1–D5): the thing that
// keeps a registry record current without anyone clicking Refresh.
//
// The three things about it a reasonable implementer would get wrong, all
// pinned by tests:
//
//  1. **The cursor is written AFTER a page is processed, and the id stored is
//     the page's last id PLUS ONE.** oppdateringsid is inclusive ("from and
//     including" — the registry's own docs), so storing the last id itself
//     re-reads that entry every cycle forever. Writing the cursor before the
//     page is processed is worse: those entities changed, this installation
//     skipped them, and nothing will ever say so again. A feed request that
//     fails must therefore leave the cursor exactly where it was, while a
//     *refresh* that fails must not — the hint below is what remembers that.
//  2. **The hint is written before the refresh, in its own statement.** A
//     successful refresh leaves fetched_at >= registry_updated_hint; a failed
//     one leaves hint > fetched_at, which is the single definition of stale and
//     the only reason the sweep ever finds it again (design D2, D3). Writing
//     the hint after the refresh, or inside its transaction, would make a
//     failed refresh indistinguishable from one that never happened.
//  3. **Every entry counts, including Ukjent, and none of them is the
//     authority.** The feed says *that* something changed, never what: the
//     entity is re-read whole through delivery A's own refresh path, which is
//     where a 410 becomes a deletion and a SlettetEnhet body becomes a
//     deletion date. A worker that mapped endringstype onto an outcome itself
//     would be a second, divergent copy of that ruling.
//
// The advisory lease (D5) is communications/retention.go's underLease,
// reproduced rather than shared: the two are the only users, and a shared
// helper would be a third module boundary to design for no benefit.

const (
	// registryFeedWorkerName is what the runner logs this worker as. It follows
	// the <module>-<worker> spelling communications' three workers established
	// (communications-outbox, communications-retention,
	// communications-attachment-cleanup) rather than the design doc's dotted
	// "customers.registry-feed": an operator greps one set of worker names, and
	// two spellings in one log would be the first thing to explain.
	registryFeedWorkerName = "customers-registry-feed"

	// registryFeedLeaseKey is the ASCII string "CUSTREG1" read as a big-endian
	// 64-bit value (design D5). Postgres advisory locks are per-database, so
	// this key shares one space with every other advisory-lock user in the
	// installation — which is why it is a recognisable constant rather than a
	// small number, and why the one-argument pg_try_advisory_lock(bigint)
	// overload is used rather than the two-argument one identity and energy
	// take for their own row-scoped locks.
	registryFeedLeaseKey int64 = 0x4355535452454731

	// registryFeedPageBudget is how many pages one cycle reads before stopping
	// (design D1). At registryFeedPageSize entries a page that is 20 000
	// entries a cycle — a week's churn of the whole register clears in two
	// cycles — while a worker that fell a month behind still never holds its
	// lease for an hour.
	registryFeedPageBudget = 20

	// registryFeedStaleBatch and registryFeedBackfillBatch bound the sweep
	// (design D3): up to 50 records whose refresh has not caught up with their
	// hint, and up to 25 customers that have no record at all. The second is
	// smaller on purpose — it is unbounded work the first time a worker ever runs
	// on an old installation, and 25 per cycle is about a hundred an hour at the
	// default poll, so a few thousand customers are caught up within a day or two
	// without ever looking like an outage to the registry.
	registryFeedStaleBatch    = 50
	registryFeedBackfillBatch = 25

	// defaultRegistryFeedPoll is the cadence a worker built from a Deps with no
	// Config falls back on — the same value config.go defaults
	// CUSTOMERS_REGISTRY_FEED_POLL to, repeated here so a worker built from a
	// bare module.Deps is still well-defined rather than spinning.
	defaultRegistryFeedPoll = 15 * time.Minute
)

// RegistryFeedWorker reads Brreg's incremental update feed and re-reads the
// entities this installation is a customer of. It implements worker.Worker, so
// module.Workers hands it to cmd/vantigo's runner in worker mode and in api
// mode when WORKERS_IN_PROCESS=1.
type RegistryFeedWorker struct {
	deps module.Deps
	// srv is the module's own operations, built exactly as mount builds them:
	// the worker refreshes through refreshRegistryRecord — the same
	// network-then-transaction path a click takes, with the same Brreg client
	// from the same Deps.HTTPTransport seam — rather than reimplementing the
	// four outcomes beside it.
	srv *server
}

var _ worker.Worker = (*RegistryFeedWorker)(nil)

// NewRegistryFeedWorker builds the worker over d.
func NewRegistryFeedWorker(d module.Deps) *RegistryFeedWorker {
	return &RegistryFeedWorker{deps: d, srv: newServer(d)}
}

// Name identifies this worker in the runner's logs.
func (w *RegistryFeedWorker) Name() string { return registryFeedWorkerName }

// Interval is the poll cadence between cycles: the operator's
// CUSTOMERS_REGISTRY_FEED_POLL when this worker was built from a Deps that
// carries one, defaultRegistryFeedPoll otherwise. The feed is one small
// request per poll, so the cadence is about how soon a change is noticed, not
// about load.
func (w *RegistryFeedWorker) Interval() time.Duration {
	if w.deps.Config != nil && w.deps.Config.CustomersRegistryFeedPoll > 0 {
		return w.deps.Config.CustomersRegistryFeedPoll
	}
	return defaultRegistryFeedPoll
}

// Run is the worker loop: run a cycle, sleep the poll interval, repeat until
// ctx is done. The interval is computed once, before the loop, as the retention
// worker computes it. A failing cycle is logged and the loop continues — the
// loop never dies, which is the property the runner depends on since it never
// restarts a worker.
func (w *RegistryFeedWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.Interval())
	defer ticker.Stop()
	for {
		if _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			// The raw error text is safe here, unlike a per-refresh failure's
			// (refresh logs a KIND for that reason): a cycle only ever fails on the
			// lease, on one of the sweep's SELECTs, or on the feed read — and none of
			// those error texts can carry a customer's identity. The feed URL has a
			// date and an update id in it and nothing else; a pgx *PgError formats
			// its message and code but never its Detail, which is where Postgres puts
			// offending values; and the one query that takes identities as arguments,
			// CustomersByOrganisationNumbers, takes them as a []string, so pgx has no
			// encode error to spell them into. Changing that argument to a struct or
			// a jsonb payload would change this reasoning (final fix wave M3).
			w.logger().Error("registry feed cycle failed", "worker", registryFeedWorkerName, "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle is one cycle under the advisory lease, reporting whether it ran:
// the sweep first, then the feed. false means another replica holds the lease
// and this one skipped, which is a normal, logged outcome and not an error.
//
// The sweep runs first because it is the half that only ever RETRIES work the
// feed has already accounted for (design D3): ordering it ahead means a cycle
// whose feed request fails still caught up whatever was outstanding, and a
// cycle that ends early leaves the cursor where the last fully processed page
// put it either way.
//
// The outage counter is made here and shared by both halves (final fix wave
// I5): a registry that is down is down for the whole cycle, so a sweep that gave
// up on it must not be followed by a feed that spends the same fifteen seconds
// per entity discovering the same thing.
func (w *RegistryFeedWorker) RunCycle(ctx context.Context) (bool, error) {
	return w.underLease(ctx, func(ctx context.Context) error {
		if err := w.ensureCursor(ctx); err != nil {
			return err
		}
		outage := &registryOutage{logger: w.logger()}
		if _, err := w.Sweep(ctx, outage); err != nil {
			return err
		}
		if outage.abandoned {
			// Abandoned, not failed: the Warn has already been logged, the position
			// and the cursor are where they were, and the next poll tries again.
			return nil
		}
		_, err := w.ReadFeed(ctx, outage)
		return err
	})
}

// underLease is design D5's non-blocking, installation-wide lease, the same
// shape communications' retention worker takes (retention.go's own underLease,
// whose comment explains the session scope and the WithoutCancel release in
// full). Reproduced rather than shared: this worker and the Peppol re-check
// worker beside it are the only two users in this module, and a shared helper
// would be a third module boundary to design.
func (w *RegistryFeedWorker) underLease(ctx context.Context, action func(context.Context) error) (bool, error) {
	conn, err := w.deps.Pool.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("customers: acquire a connection for the registry feed lease: %w", err)
	}
	defer conn.Release()

	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, registryFeedLeaseKey).Scan(&acquired); err != nil {
		return false, fmt.Errorf("customers: take the registry feed lease: %w", err)
	}
	if !acquired {
		w.logger().Debug("the customers registry feed lease is held by another replica; skipping this cycle",
			"worker", registryFeedWorkerName)
		return false, nil
	}
	defer func() {
		release := context.WithoutCancel(ctx)
		if _, err := conn.Exec(release, `SELECT pg_advisory_unlock($1)`, registryFeedLeaseKey); err != nil {
			// A session that still holds the lease must never go back into the
			// pool: every later cycle in this process would draw it, find the
			// lock held by its own session, and skip silently forever. Closing
			// the connection makes Release destroy it.
			w.logger().Error("releasing the registry feed lease failed; discarding the connection",
				"worker", registryFeedWorkerName, "error", err)
			_ = conn.Conn().Close(release)
		}
	}()
	return true, action(ctx)
}

// ensureCursor plants the cursor row the first time this installation runs a
// cycle, stamping the moment it joined the feed (design D1). Idempotent, and
// deliberately not an upsert: started_at must never move.
func (w *RegistryFeedWorker) ensureCursor(ctx context.Context) error {
	if err := store.New(w.deps.Pool).EnsureRegistryFeedCursor(ctx, w.now()); err != nil {
		return fmt.Errorf("customers: ensure the registry feed cursor: %w", err)
	}
	return nil
}

// Sweep is design D3: up to registryFeedStaleBatch records whose refresh has
// not caught up with their hint (oldest hint first), then up to
// registryFeedBackfillBatch Norwegian business customers with no record at all
// (lowest id first). It answers how many refreshes succeeded.
//
// A refresh that fails here is logged and left for the next cycle — never
// returned: one unreachable company must not stop the sweep from catching up
// on the other 74, and a stale row is still stale next cycle, which is the
// whole retry mechanism. A failure of the SELECTs themselves is returned: that
// is the database, not the registry. The one failure that does end the sweep is
// the registry going quiet altogether — registryOutageLimit of them in a row,
// with the position left where it was (final fix wave I5).
//
// The sweep never touches the FEED cursor — it is not reading the feed, so it
// has no feed position to advance, and advancing one on its behalf would claim
// pages nobody read. It does keep its own position, backfill_after_id, for the
// reason the migration's comment gives: without one, a customer the register
// cannot resolve pins the backfill to the same 25 rows forever. Reading that
// position means the cursor row has to exist, which is why RunCycle calls
// ensureCursor ahead of this and why anything driving Sweep on its own must too.
func (w *RegistryFeedWorker) Sweep(ctx context.Context, outage *registryOutage) (int, error) {
	q := store.New(w.deps.Pool)

	cursor, err := q.GetRegistryFeedCursor(ctx)
	if err != nil {
		return 0, fmt.Errorf("customers: read the registry feed cursor: %w", err)
	}
	stale, err := q.StaleRegistryRecords(ctx, registryFeedStaleBatch)
	if err != nil {
		return 0, fmt.Errorf("customers: select stale registry records: %w", err)
	}
	var tally refreshTally
	// staleAttempted and backfillAttempted are what the cycle's own line reports
	// (final fix wave I2): how many refreshes this sweep actually made, not how
	// many rows it selected — a cycle abandoned or cancelled part-way through a
	// batch reached fewer, and a line saying 50 either way would be reporting the
	// query rather than the work.
	var staleAttempted, backfillAttempted int
	for _, row := range stale {
		if ctx.Err() != nil {
			return tally.refreshed, nil
		}
		staleAttempted++
		outcome := w.refresh(ctx, row.CustomerID, row.Type, row.LegalCountry, row.LegalID, row.LegalName, row.LegalSource, row.LegalType)
		tally.add(outcome)
		if outage.record(outcome) {
			// The registry is not answering (final fix wave I5): stop here with the
			// backfill position untouched, exactly as a cancelled cycle does. Nothing
			// already done needs undoing — a hint stands, and a record that was not
			// refreshed is still stale, which is what the next cycle reads.
			w.logSweep(staleAttempted, backfillAttempted, tally, cursor.BackfillAfterID)
			return tally.refreshed, nil
		}
	}

	missing, err := q.CustomersWithoutRegistryRecord(ctx, store.CustomersWithoutRegistryRecordParams{
		AfterID: cursor.BackfillAfterID, RowLimit: registryFeedBackfillBatch,
	})
	if err != nil {
		return tally.refreshed, fmt.Errorf("customers: select customers without a registry record: %w", err)
	}
	for _, row := range missing {
		if ctx.Err() != nil {
			// A cancelled cycle claims no ground at all: the position is not
			// written, so the rows this batch never reached are attempted again
			// NEXT CYCLE rather than after a whole pass of everyone else. The one
			// row that was attempted costs one repeated request for that; a
			// position moved past customers nobody looked at costs them a pass.
			return tally.refreshed, nil
		}
		backfillAttempted++
		outcome := w.refresh(ctx, row.ID, row.Type, row.LegalCountry, row.LegalID, row.LegalName, row.LegalSource, row.LegalType)
		tally.add(outcome)
		if outage.record(outcome) {
			// An abandoned batch claims no ground either, for the cancelled case's
			// own reason: the customers this cycle never reached are next cycle's.
			w.logSweep(staleAttempted, backfillAttempted, tally, cursor.BackfillAfterID)
			return tally.refreshed, nil
		}
	}
	// A full batch leaves the position at its last id, so the next cycle
	// continues; a short one means the end of the installation, and 0 starts the
	// next pass from the front.
	switch {
	case len(missing) == 0 && cursor.BackfillAfterID != 0:
		// Nothing after the position: either the previous batch reached the end
		// of the installation, or everything behind it has a record now. Either
		// way the position has to go back to 0, or the pass is over and nothing
		// ever starts another one — a full batch of 25 unresolvable customers
		// followed by an empty batch would otherwise park the backfill there
		// permanently, which is the same starvation the position exists to
		// prevent, one cycle later.
		if err := q.SetRegistryBackfillPosition(ctx, 0); err != nil {
			return tally.refreshed, fmt.Errorf("customers: store the registry backfill position: %w", err)
		}
	case len(missing) > 0:
		var next int32 // 0: there is nothing after this batch, so start over
		if len(missing) == registryFeedBackfillBatch {
			// A full batch, so there may well be a 26th customer behind it.
			next = missing[len(missing)-1].ID
		}
		if err := q.SetRegistryBackfillPosition(ctx, next); err != nil {
			return tally.refreshed, fmt.Errorf("customers: store the registry backfill position: %w", err)
		}
	}
	w.logSweep(staleAttempted, backfillAttempted, tally, cursor.BackfillAfterID)
	return tally.refreshed, nil
}

// logSweep is the sweep's one line per cycle, at INFO (final fix wave I2):
// docs/customers.md promises an operator a line per cycle for the sweep beside
// the one per page, and a Debug line is one they would have to turn the whole
// process's logging up to see. It says what the sweep attempted of each half and
// how those attempts came out, so "nothing happened" and "fifty attempts, fifty
// failures" are different lines rather than both being absent.
func (w *RegistryFeedWorker) logSweep(staleAttempted, backfillAttempted int, tally refreshTally, backfillFrom int32) {
	w.logger().Info("registry sweep finished", "worker", registryFeedWorkerName,
		"stale", staleAttempted, "backfill", backfillAttempted, "refreshed", tally.refreshed,
		"unknown", tally.unknown, "failed", tally.failed,
		"backfillFrom", backfillFrom)
}

// ReadFeed reads pages from the stored cursor until a page comes back short or
// the page budget is spent, answering how many pages it processed (design D1).
//
// A feed request that fails ends the cycle with the cursor untouched and the
// error returned, so the next cycle re-reads the same page. Everything a page
// costs — the hint, the refresh — happens before its cursor is written, so a
// page is either fully accounted for or read again.
func (w *RegistryFeedWorker) ReadFeed(ctx context.Context, outage *registryOutage) (int, error) {
	q := store.New(w.deps.Pool)
	pages := 0
	for ; pages < registryFeedPageBudget; pages++ {
		if ctx.Err() != nil {
			return pages, nil
		}
		cursor, err := q.GetRegistryFeedCursor(ctx)
		if err != nil {
			return pages, fmt.Errorf("customers: read the registry feed cursor: %w", err)
		}
		page, err := w.srv.brreg.updates(ctx, feedCursor{
			UpdateID: cursor.NextUpdateID,
			Since:    cursor.StartedAt,
			Size:     registryFeedPageSize,
		})
		if err != nil {
			return pages, fmt.Errorf("customers: read the registry update feed: %w", err)
		}
		if len(page.Entries) == 0 {
			// Nothing to process and no position to claim: only the fact that
			// this installation is still asking.
			if err := q.TouchRegistryFeedCursor(ctx, w.now()); err != nil {
				return pages, fmt.Errorf("customers: record the registry feed poll: %w", err)
			}
			return pages, nil
		}
		if err := w.handlePage(ctx, q, page, outage); err != nil {
			return pages, err
		}
		if outage.abandoned {
			// The registry stopped answering part-way through this page (final fix
			// wave I5): handlePage left the cursor where it was, so the page is read
			// again next cycle — hints never move backwards and a refresh is
			// idempotent, so re-reading it costs nothing but the requests.
			return pages, nil
		}
		if len(page.Entries) < registryFeedPageSize {
			// A short page means the feed is caught up; one more request would
			// only ask the registry to say so again.
			return pages + 1, nil
		}
	}
	w.logger().Debug("registry feed page budget spent; the backlog continues next cycle",
		"worker", registryFeedWorkerName, "pages", pages)
	return pages, nil
}

// handlePage is one page: intersect it with this installation, write each
// matched customer's hint, refresh it, and only then advance the cursor.
//
// A customer named several times on one page is refreshed ONCE, with the newest
// of its entries as the hint (design D1, D2): the entity is re-read whole
// whatever the reason, so three requests would spend three requests to learn
// one thing, and the oldest of three timestamps would understate how current
// the record then is.
func (w *RegistryFeedWorker) handlePage(ctx context.Context, q *store.Queries, page feedPage, outage *registryOutage) error {
	newest := make(map[string]time.Time, len(page.Entries))
	numbers := make([]string, 0, len(page.Entries))
	var highest int64
	for _, entry := range page.Entries {
		if entry.UpdateID > highest {
			// The feed is documented as monotonic ascending, so this is the last
			// entry's id in practice; taking the maximum explicitly means the
			// cursor clears every entry ON THIS PAGE even if the page itself
			// arrives unsorted, rather than stopping at whatever happens to be
			// last. It does not make the cursor monotonic — AdvanceRegistryFeedCursor
			// stores what this page says, whatever is there now. What keeps an
			// older page from ever being handled is the lease (design D5): one
			// replica reads the feed at a time, from a position it wrote itself,
			// so a page behind the cursor is never asked for.
			highest = entry.UpdateID
		}
		if entry.OrganisationNumber == "" {
			continue
		}
		if at, seen := newest[entry.OrganisationNumber]; !seen || entry.Date.After(at) {
			if !seen {
				numbers = append(numbers, entry.OrganisationNumber)
			}
			newest[entry.OrganisationNumber] = entry.Date
		}
	}

	matched, err := q.CustomersByOrganisationNumbers(ctx, numbers)
	if err != nil {
		return fmt.Errorf("customers: match a registry feed page: %w", err)
	}
	var tally refreshTally
	for _, row := range matched {
		if ctx.Err() != nil {
			// Stop without advancing the cursor: this page is only partly
			// accounted for, so the next cycle must read it again.
			return nil
		}
		hint, ok := newest[deref(row.LegalID)]
		if !ok {
			continue
		}
		// Written BEFORE the refresh and in its own statement (design D2): a
		// refresh that fails must leave hint > fetched_at, which is what the
		// sweep retries on. A customer with no record row has nowhere to keep a
		// hint, and this statement touching no row is exactly right for it.
		if err := q.SetRegistryUpdatedHint(ctx, store.SetRegistryUpdatedHintParams{
			CustomerID: row.ID, Hint: hint,
		}); err != nil {
			return fmt.Errorf("customers: write a registry updated hint: %w", err)
		}
		outcome := w.refresh(ctx, row.ID, row.Type, row.LegalCountry, row.LegalID, row.LegalName, row.LegalSource, row.LegalType)
		tally.add(outcome)
		if outage.record(outcome) {
			// The registry is not answering (final fix wave I5). Return without
			// advancing the cursor, exactly as the cancelled case above does: this
			// page is only partly accounted for, and re-reading it next cycle is the
			// one thing that cannot lose an entry.
			return nil
		}
	}

	last := page.Entries[len(page.Entries)-1].Date
	next := highest + 1 // oppdateringsid is INCLUSIVE: see this file's header.
	if err := q.AdvanceRegistryFeedCursor(ctx, store.AdvanceRegistryFeedCursorParams{
		NextUpdateID: &next, LastUpdateAt: last, LastPolledAt: w.now(),
	}); err != nil {
		return fmt.Errorf("customers: advance the registry feed cursor: %w", err)
	}
	w.logger().Info("registry feed page processed", "worker", registryFeedWorkerName,
		"entries", len(page.Entries), "matched", len(matched), "refreshed", tally.refreshed,
		"unknown", tally.unknown, "failed", tally.failed, "nextUpdateId", next)
	return nil
}

// refreshOutcome is what one call to refresh came to, because a cycle's log has
// to tell three things apart:
//
//   - refreshRefreshed: the register answered about the company and the record
//     on file is now what it said;
//   - refreshUnknown: the register does not know this organisation number. A
//     real 200 answer (registry.go's brregEntityUnknown) that stores nothing, so
//     counting it as refreshed would report records for customers that have
//     none — and on an installation with a few hand-typed numbers the register
//     never knew, that is a cycle claiming to have refreshed them every fifteen
//     minutes forever;
//   - refreshFailed: the attempt did not complete, and refresh has already
//     logged it by kind;
//   - refreshUnavailable: it did not complete because the REGISTRY could not be
//     reached (errBrregUnavailable). Counted as a failure like any other in the
//     log line, and told apart only for registryOutage below: a run of these is
//     the one failure that says nothing about the next customer is worth trying.
//
// refreshSkipped is counted nowhere: nothing was attempted for a customer with
// no organisation number to look up (or for one that stopped being that company
// mid-fetch, final fix wave I4), and a count of it would be a count of the
// installation's legacy identities rather than of this cycle's work.
type refreshOutcome int

const (
	refreshSkipped refreshOutcome = iota
	refreshRefreshed
	refreshUnknown
	refreshFailed
	refreshUnavailable
)

// refreshTally counts the outcomes of a run of refreshes for its log line.
type refreshTally struct {
	refreshed int
	unknown   int
	failed    int
}

func (t *refreshTally) add(outcome refreshOutcome) {
	switch outcome {
	case refreshRefreshed:
		t.refreshed++
	case refreshUnknown:
		t.unknown++
	case refreshFailed, refreshUnavailable:
		t.failed++
	case refreshSkipped:
	}
}

// registryOutageLimit is how many refreshes in a row may answer "the registry
// could not be reached" before the cycle gives up on it (final fix wave I5).
//
// The arithmetic is why it exists: a sweep is up to 75 refreshes, each bounded
// by BRREG_TIMEOUT (15 s by default) and each spending its whole retry budget
// against a registry that is black-holing requests — nineteen minutes of one
// cycle, under the lease, after which the 15-minute ticker fires again
// immediately and the next cycle does it all over. Five is enough to be sure it
// is the registry and not one company (a 404 and an unknown number are not
// failures at all, and a database failure is a different outcome), and it costs
// at most a minute and a quarter before a cycle stands down until the next poll.
const registryOutageLimit = 5

// registryOutage is that count, for one whole cycle: the sweep's two loops and
// the feed's page loop share it, because "the registry is down" is a fact about
// the cycle and not about whichever half noticed first.
//
// A skip neither counts nor clears: nothing was asked, so it says nothing about
// whether the register is answering. Any other outcome — a stored record, an
// unknown number, a database failure — means a request completed, so the count
// starts again.
type registryOutage struct {
	logger      *slog.Logger
	consecutive int
	abandoned   bool
}

// record folds one refresh outcome in and reports whether the cycle is to stop
// here. The Warn is logged once, the moment the limit is reached: a line per
// remaining customer would be the outage reported 70 times.
func (o *registryOutage) record(outcome refreshOutcome) bool {
	switch outcome {
	case refreshUnavailable:
		o.consecutive++
	case refreshSkipped:
		return o.abandoned
	default:
		o.consecutive = 0
		return false
	}
	if o.consecutive >= registryOutageLimit && !o.abandoned {
		o.abandoned = true
		o.logger.Warn("registry unavailable, cycle abandoned",
			"worker", registryFeedWorkerName, "consecutiveFailures", o.consecutive)
	}
	return o.abandoned
}

// refresh is one customer re-read through delivery A's own path with the system
// actor (design D4), reporting which of refreshOutcome's outcomes it was. A
// failure is logged by kind — never the error text, which can carry the
// organisation number and the URL it was built from, except for a "database"
// kind, which is this module's own wrapped error and carries neither
// (logRegistryFetchFailure applies the same rule to the write hooks' warning) —
// and swallowed: the caller has more customers to get through, and the hint (or
// the missing record) is what remembers this one.
//
// A customer whose identity cannot be looked up at all — a legacy legal_id that
// is not an organisation number — is skipped silently: registryOrganisationNumber
// is the one rule for that, shared with the refresh endpoint's own 409.
func (w *RegistryFeedWorker) refresh(ctx context.Context, customerID int32, customerType string, legalCountry, legalID, legalName, legalSource, legalType *string) refreshOutcome {
	identity := identityFromRow(legalCountry, legalID, legalName, legalSource, legalType)
	orgnr := registryOrganisationNumber(identity, customerType)
	if orgnr == "" {
		return refreshSkipped
	}
	result, err := w.srv.refreshRegistryRecord(ctx, customerID, orgnr, identity.Name, generatedFallbackActor)
	if err != nil {
		if errors.Is(err, errCustomerNotFound) || errors.Is(err, pgx.ErrNoRows) {
			// Archived or gone between the select and here: nothing to refresh
			// and nothing wrong.
			return refreshSkipped
		}
		if errors.Is(err, errRegistryIdentityChanged) {
			// Somebody re-identified this customer while the register was
			// answering (final fix wave I4). Nothing was written and nothing
			// failed: the company it is now has no record, so the backfill picks
			// it up, or the feed does the next time it names it.
			return refreshSkipped
		}
		if ctx.Err() != nil {
			// The cycle is being shut down, not the registry being unreachable: no
			// Warn (the Peppol worker's recheck applies the same guard, and an
			// operator reading warnings must find only things that were wrong), and
			// deliberately not an outage — the loops' own ctx checks end the cycle.
			return refreshFailed
		}
		kind := registryErrorKind(err)
		if kind == registryErrorKindDatabase {
			w.logger().Warn("customers: registry record refresh failed",
				"worker", registryFeedWorkerName, "customerId", customerID, "errorKind", kind, "error", err.Error())
		} else {
			w.logger().Warn("customers: registry record refresh failed",
				"worker", registryFeedWorkerName, "customerId", customerID, "errorKind", kind)
		}
		if errors.Is(err, errBrregUnavailable) {
			return refreshUnavailable
		}
		return refreshFailed
	}
	if result.Status == registryStatusUnknown {
		// The register does not know the number: an answer, and not this
		// worker's to act on — the identity is what needs a person's attention
		// (registry.go). Nothing was stored, so nothing was refreshed.
		return refreshUnknown
	}
	return refreshRefreshed
}

// now is the worker's clock, so tests control time exactly as they do for the
// handlers.
func (w *RegistryFeedWorker) now() time.Time {
	if w.deps.Clock != nil {
		return w.deps.Clock().UTC()
	}
	return time.Now().UTC()
}

func (w *RegistryFeedWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}
