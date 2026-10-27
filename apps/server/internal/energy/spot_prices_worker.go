package energy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the spot price worker, energy-spot-prices (energy
// consumption billing design D2): on when ENERGY_SPOT_PRICE_FEED_ENABLED, a
// cycle every ENERGY_SPOT_PRICE_FEED_POLL under an advisory lease so one
// replica fetches. A cycle takes the areas in use (plan reading 7) and, for
// each, the civil days in Europe/Oslo from seven days back to tomorrow
// (reading 3); a day with a price starting in it is stored and never asked
// for, and of the rest the planner asks for those the feed's record
// (energy.spot_price_fetches) leaves due — no record yet, or next_attempt_at
// reached — at most twenty a cycle, oldest days first. Each answer is
// recorded on the day's row (reading 2) and retried as reading 4 says: a 404
// for today or tomorrow is pending, not yet published, asked again in an
// hour; a 404 for a past day is missing, asked again daily until its seventh
// missing attempt; any other failure is failed, backed off. The row's
// attempts count the requests since it last changed status, so a day pending
// for hours does not spend the missing or the failed budget. Older days, and
// a day the feed gave up on, come by file.
//
// Every write of the day's prices takes the area's advisory lock first
// (lockSpotPriceAreas), as the file import does, so a file and the feed
// writing one area are serialised; the exclusion constraint is the backstop.
//
// A day stored while its fetch row says pending, missing or failed — a file
// imported it meanwhile — is simply skipped: the row stays the feed's record
// of its own requests, and RecordSpotPriceFetch counts an attempt on every
// write, so writing it ok without a request would make the record say a
// request was made that was not.
//
// With ENERGY_SPOT_PRICE_SERVICE_URL set the same worker, under the same
// lease and on the same poll, copies from the shared spot-price service
// instead (spot-price service design S5): its cycle is
// spot_price_service_sync.go's, and config.Load refuses both sources at
// once.

const (
	// spotPriceWorkerName is what the runner logs this worker as.
	spotPriceWorkerName = "energy-spot-prices"

	// spotPriceLeaseKey is the ASCII string "ENRGSPT1" read as a big-endian
	// 64-bit value (plan reading 12), its own key beside the import
	// worker's ENRGIMP1.
	spotPriceLeaseKey int64 = 0x454E524753505431

	// defaultSpotPriceFeedPoll is config.go's own default for
	// ENERGY_SPOT_PRICE_FEED_POLL, for a worker built from a Deps with no
	// Config.
	defaultSpotPriceFeedPoll = 15 * time.Minute

	// spotPriceFeedBackDays is how far back the planner looks: a week, so a
	// worker off for a few days catches up on its own (design D2).
	spotPriceFeedBackDays = 7
	// maxSpotPriceFetchesPerCycle bounds one cycle's requests (design D2),
	// so a first cycle over five areas does not ask the feed for 45 days in
	// a row.
	maxSpotPriceFetchesPerCycle = 20

	// The retries of reading 4, each counted in its own status: today or
	// tomorrow not yet published is asked again in an hour (the day-ahead
	// auction publishes about 13:00 CET); a past day the feed has no file for
	// in a day, until its seventh missing attempt; any other failure after 15
	// minutes doubled per failed attempt, at most six hours.
	spotPricePendingRetry       = time.Hour
	spotPriceMissingRetry       = 24 * time.Hour
	maxSpotPriceMissingAttempts = 7
	spotPriceFailedBackoff      = 15 * time.Minute
	maxSpotPriceFailedBackoff   = 6 * time.Hour

	// spotFeedWriteBudget bounds the write of an answer that came: it runs
	// past a shutdown, so the answer is recorded, but not for ever.
	spotFeedWriteBudget = 30 * time.Second
)

// The statuses of a fetch row (plan reading 2).
const (
	spotFetchOK      = "ok"
	spotFetchPending = "pending"
	spotFetchMissing = "missing"
	spotFetchFailed  = "failed"
)

// spotFeedOverlaps is the sentence of a day the area cannot take because a
// stored interval of another length touches it (reading 5): a day stored at
// one resolution and fetched at another is refused rather than doubled.
const spotFeedOverlaps = "the day's intervals overlap stored prices of another length"

// SpotPriceWorker fetches the day-ahead prices of the areas in use. It
// implements worker.Worker.
type SpotPriceWorker struct {
	deps     module.Deps
	template spotFeedTemplate
	zone     *time.Location
	client   *http.Client
	// service is the spot-price service's client when it is the source,
	// nil when the feed is.
	service *spotServiceClient
	// copying is whether the last service cycle left a history walk that
	// can go on, so the next cycle comes after a minute (design S5).
	copying atomic.Bool
	// setupErr is a template or a zone the worker cannot fetch with; every
	// cycle answers it rather than the process panicking.
	setupErr error
}

var _ worker.Worker = (*SpotPriceWorker)(nil)

// NewSpotPriceWorker builds the worker over d. The feed's template is
// validated here (plan reading 13); config.Load already refused a bad one at
// start-up, so a failure here is a Config built by hand, and every cycle
// answers it. A Deps with no Config fetches from the default feed — workers
// never builds the worker then, since the switch is off.
func NewSpotPriceWorker(d module.Deps) *SpotPriceWorker {
	w := &SpotPriceWorker{deps: d, client: spotFeedClient(d.HTTPTransport)}
	raw := config.DefaultEnergySpotPriceFeedURL
	if d.Config != nil && d.Config.EnergySpotPriceFeedURL != "" {
		raw = d.Config.EnergySpotPriceFeedURL
	}
	w.template, w.setupErr = parseSpotFeedTemplate(raw)
	if d.Config != nil && d.Config.EnergySpotPriceServiceURL != "" {
		service, err := newSpotServiceClient(d.Config.EnergySpotPriceServiceURL, d.Config.EnergySpotPriceServiceKey, d.HTTPTransport, w.logger, w.now)
		w.service, w.setupErr = service, errors.Join(w.setupErr, err)
	}
	if zone, err := time.LoadLocation(spotPriceDayZone); err != nil {
		w.setupErr = errors.Join(w.setupErr, fmt.Errorf("energy: load %s: %w", spotPriceDayZone, err))
	} else {
		w.zone = zone
	}
	return w
}

// Name identifies this worker in the runner's logs.
func (w *SpotPriceWorker) Name() string { return spotPriceWorkerName }

// Interval is the poll between cycles (ENERGY_SPOT_PRICE_FEED_POLL, default
// fifteen minutes), whichever the source. While the service's history is
// being copied the loop waits a minute instead (nextWait).
func (w *SpotPriceWorker) Interval() time.Duration {
	if w.deps.Config != nil && w.deps.Config.EnergySpotPriceFeedPoll > 0 {
		return w.deps.Config.EnergySpotPriceFeedPoll
	}
	return defaultSpotPriceFeedPoll
}

// Run is the worker loop: a cycle, then the next after nextWait — the
// import worker's loop without its nudge, since nothing a person does asks
// for a price.
func (w *SpotPriceWorker) Run(ctx context.Context) error {
	for {
		if _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.logger().Error("spot price cycle failed", "worker", spotPriceWorkerName, "error", err)
		}
		timer := time.NewTimer(w.nextWait())
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

// nextWait is the wait after a cycle: a minute while the service's history
// walk can go on (spot-price service design S5), the poll otherwise.
func (w *SpotPriceWorker) nextWait() time.Duration {
	if w.copying.Load() {
		return spotServiceCopyingWait
	}
	return w.Interval()
}

// RunCycle is one cycle under the advisory lease, answering how many
// requests it made: none when another replica holds the lease or nothing is
// due. A failure of the feed or the service is recorded on the day's row (or
// the history walk's) and the cycle goes on; an error is the database's, and
// ends the cycle. With the service as the source the cycle is serviceCycle.
func (w *SpotPriceWorker) RunCycle(ctx context.Context) (int, error) {
	if w.setupErr != nil {
		return 0, w.setupErr
	}
	w.deleteUntouchedSettlementDrafts(ctx)
	requests := 0
	if w.service != nil {
		acquired, err := w.underLease(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
			n, err := w.serviceCycle(ctx, conn)
			requests = n
			return err
		})
		if !acquired {
			w.copying.Store(false)
		}
		return requests, err
	}
	_, err := w.underLease(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
		due, err := w.due(ctx, store.New(conn))
		if err != nil {
			return err
		}
		for _, d := range due {
			if ctx.Err() != nil {
				return nil
			}
			asked, err := w.fetchDay(ctx, conn, d)
			if err != nil {
				return err
			}
			if asked {
				requests++
			}
		}
		return nil
	})
	return requests, err
}

// deleteUntouchedSettlementDrafts is the settlements' housekeeping in the
// worker's cycle (energy settlements plan reading 7): drafts nobody has
// touched for thirty days are deleted. A failure is logged and the cycle
// goes on: prices matter more than tidiness.
func (w *SpotPriceWorker) deleteUntouchedSettlementDrafts(ctx context.Context) {
	if w.deps.Pool == nil {
		return
	}
	if _, err := store.New(w.deps.Pool).DeleteUntouchedSettlementDrafts(ctx, w.now().Add(-untouchedDraftAge)); err != nil {
		w.logger().Error("deleting untouched settlement drafts failed", "worker", spotPriceWorkerName, "error", err)
	}
}

// spotFeedDue is one (area, day) the planner asks the feed for: day a civil
// date at UTC midnight, past whether it is before today, and status and
// attempts its fetch row's — the status last recorded ("" with no row yet)
// and the requests counted in it.
type spotFeedDue struct {
	area     string
	day      time.Time
	tomorrow bool
	past     bool
	status   string
	attempts int32
}

// attemptsIn is the attempts the row will count once this request is
// recorded with status: one more in the same status, one in a new one — what
// RecordSpotPriceFetch writes.
func (d spotFeedDue) attemptsIn(status string) int32 {
	if d.status == status {
		return d.attempts + 1
	}
	return 1
}

// spotFeedAreaRecord is what the planner knows of an area over the window:
// the civil days a price starts in, and the fetch rows, both keyed by the
// day's spotFeedDayText.
type spotFeedAreaRecord struct {
	area    string
	stored  map[string]bool
	fetches map[string]store.EnergySpotPriceFetch
}

// spotFeedToday is the civil date in zone at now, as a UTC midnight: the
// planner's days are dates, compared and stepped as such, and read back
// from a date column the same way.
func spotFeedToday(now time.Time, zone *time.Location) time.Time {
	y, m, d := now.In(zone).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// planSpotFeedFetches is the planner (plan readings 3–4), a pure function of
// now and what is known of the areas: the civil days from today − 7 to today
// + 1, oldest first and, within a day, the areas in the order given; a day
// with a price starting in it is skipped whatever its fetch row says; of the
// rest, a day with no fetch row or whose next_attempt_at has come is due —
// an ok row, or a missing one given up on, has none and is never due again.
// Each due day carries its row's status and the attempts counted in it, so
// the answer's record counts on in the same status and from one in another.
// At most maxSpotPriceFetchesPerCycle are answered.
func planSpotFeedFetches(now time.Time, zone *time.Location, areas []spotFeedAreaRecord) []spotFeedDue {
	today := spotFeedToday(now, zone)
	var due []spotFeedDue
	for offset := -spotPriceFeedBackDays; offset <= 1; offset++ {
		day := today.AddDate(0, 0, offset)
		key := spotFeedDayText(day)
		for _, a := range areas {
			if a.stored[key] {
				continue
			}
			d := spotFeedDue{area: a.area, day: day, tomorrow: offset == 1, past: offset < 0}
			if f, ok := a.fetches[key]; ok {
				if f.NextAttemptAt == nil || f.NextAttemptAt.After(now) {
					continue
				}
				d.status, d.attempts = f.Status, f.Attempts
			}
			due = append(due, d)
			if len(due) == maxSpotPriceFetchesPerCycle {
				return due
			}
		}
	}
	return due
}

// due reads what the planner needs, on the lease's connection, and plans.
func (w *SpotPriceWorker) due(ctx context.Context, q *store.Queries) ([]spotFeedDue, error) {
	areas, err := q.ListSpotPriceAreasInUse(ctx)
	if err != nil {
		return nil, fmt.Errorf("energy: read the price areas in use: %w", err)
	}
	now := w.now()
	today := spotFeedToday(now, w.zone)
	from, to := today.AddDate(0, 0, -spotPriceFeedBackDays), today.AddDate(0, 0, 1)
	since := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, w.zone)
	records := make([]spotFeedAreaRecord, 0, len(areas))
	for _, area := range areas {
		days, err := q.ListSpotPriceDays(ctx, store.ListSpotPriceDaysParams{TimeZone: spotPriceDayZone, PriceArea: area, Since: since})
		if err != nil {
			return nil, fmt.Errorf("energy: read the priced days of %s: %w", area, err)
		}
		fetches, err := q.ListSpotPriceFetches(ctx, store.ListSpotPriceFetchesParams{
			PriceArea: area, FromDay: pgtype.Date{Time: from, Valid: true}, ToDay: pgtype.Date{Time: to, Valid: true},
		})
		if err != nil {
			return nil, fmt.Errorf("energy: read the spot price fetches of %s: %w", area, err)
		}
		r := spotFeedAreaRecord{area: area, stored: map[string]bool{}, fetches: map[string]store.EnergySpotPriceFetch{}}
		for _, d := range days {
			r.stored[spotFeedDayText(d.Time)] = true
		}
		for _, f := range fetches {
			r.fetches[spotFeedDayText(f.Day.Time)] = f
		}
		records = append(records, r)
	}
	return planSpotFeedFetches(now, w.zone, records), nil
}

// fetchDay asks the feed for d under the fetch budget and records the
// answer, reporting whether a request was answered to record. A caller that
// went away before the fetch answered (a shutdown) records nothing: that is
// no failure of the feed. The record itself is not cancelled with the
// caller, so an answer that came is written, within spotFeedWriteBudget.
func (w *SpotPriceWorker) fetchDay(ctx context.Context, conn *pgxpool.Conn, d spotFeedDue) (bool, error) {
	target := w.template.render(d.area, d.day)
	budget, cancel := context.WithTimeout(ctx, spotFeedFetchBudget)
	o := fetchSpotFeedDay(budget, w.client, target, d.area, d.day, w.zone)
	cancel()
	if ctx.Err() != nil {
		return false, nil
	}
	write, cancelWrite := context.WithTimeout(context.WithoutCancel(ctx), spotFeedWriteBudget)
	defer cancelWrite()
	now := w.now()
	q := store.New(conn)
	day := spotFeedDayText(d.day)
	switch {
	case o.failure != "":
		return true, w.recordFailed(write, q, d, o.failure, target, now)
	case o.notFound && !d.past:
		// Today or tomorrow: not published yet, however long it has been.
		next := now.Add(spotPricePendingRetry)
		w.logger().Debug("the day's spot prices are not published yet",
			"worker", spotPriceWorkerName, "area", d.area, "day", day)
		return true, w.record(write, q, d, spotFetchPending, nil, now, nil, &next)
	case o.notFound:
		// The seventh missing attempt is the last: the day is left missing,
		// and only a file brings it (reading 4).
		attempts := d.attemptsIn(spotFetchMissing)
		var next *time.Time
		if attempts < maxSpotPriceMissingAttempts {
			n := now.Add(spotPriceMissingRetry)
			next = &n
		}
		w.logger().Info("the spot price feed has no prices for a past day",
			"worker", spotPriceWorkerName, "area", d.area, "day", day, "attempts", attempts, "givenUp", next == nil)
		return true, w.record(write, q, d, spotFetchMissing, nil, now, nil, next)
	}

	overlaps := false
	var inserted, replaced, unchanged int
	err := db.WithTx(write, conn, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockSpotPriceAreas(write, tx, []string{d.area}); err != nil {
			return err
		}
		tq := store.New(tx)
		plan, err := planSpotPriceWrite(write, tq, d.area, o.rows)
		if err != nil {
			return err
		}
		if len(plan.conflicts) > 0 {
			overlaps = true
			return nil
		}
		if err := applySpotPriceWrite(write, tx, plan, spotPriceSourceFeed, now); err != nil {
			return err
		}
		inserted, replaced, unchanged = plan.Counts()
		return w.record(write, tq, d, spotFetchOK, nil, now, &now, nil)
	})
	// Under the area's lock no file writes between the plan and the write;
	// should the exclusion (or the unique start) refuse a row all the same,
	// it is the same refusal.
	if isSpotPriceOverlap(err) {
		overlaps, err = true, nil
	}
	if err != nil {
		return true, fmt.Errorf("energy: store the spot prices of %s on %s: %w", d.area, day, err)
	}
	if overlaps {
		return true, w.recordFailed(write, q, d, spotFeedOverlaps, target, now)
	}
	w.logger().Info("spot prices fetched", "worker", spotPriceWorkerName, "area", d.area, "day", day,
		"inserted", inserted, "replaced", replaced, "unchanged", unchanged)
	return true, nil
}

// isSpotPriceOverlap is whether err is spot_prices_no_overlap (23P01) or the
// unique start (23505) refusing a row.
func isSpotPriceOverlap(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "23P01" || pgErr.Code == "23505")
}

// recordFailed records a failure with its backoff and logs it with the area,
// the day, the feed's host and the sentence — never the address's path or
// query, nor the body.
func (w *SpotPriceWorker) recordFailed(ctx context.Context, q *store.Queries, d spotFeedDue, sentence, target string, now time.Time) error {
	attempts := d.attemptsIn(spotFetchFailed)
	next := now.Add(spotFeedBackoff(attempts))
	w.logger().Warn("the spot price feed could not be fetched",
		"worker", spotPriceWorkerName, "area", d.area, "day", spotFeedDayText(d.day), "host", spotFeedHost(target),
		"attempts", attempts, "error", sentence)
	return w.record(ctx, q, d, spotFetchFailed, &sentence, now, nil, &next)
}

// record writes one attempt's outcome on the day's fetch row.
func (w *SpotPriceWorker) record(ctx context.Context, q *store.Queries, d spotFeedDue, status string, lastError *string, now time.Time, fetchedAt, next *time.Time) error {
	if err := q.RecordSpotPriceFetch(ctx, store.RecordSpotPriceFetchParams{
		PriceArea: d.area, Day: pgtype.Date{Time: d.day, Valid: true}, Status: status, LastError: lastError,
		AttemptedAt: now, FetchedAt: fetchedAt, NextAttemptAt: next,
	}); err != nil {
		return fmt.Errorf("energy: record the spot price fetch of %s on %s: %w", d.area, spotFeedDayText(d.day), err)
	}
	return nil
}

// spotFeedBackoff is a failed day's wait before the next request (reading
// 4): min(6h, 15min × 2^(attempts−1)), attempts the failed ones in a row,
// this one counted.
func spotFeedBackoff(attempts int32) time.Duration {
	wait := spotPriceFailedBackoff
	for i := int32(1); i < attempts && wait < maxSpotPriceFailedBackoff; i++ {
		wait *= 2
	}
	return min(wait, maxSpotPriceFailedBackoff)
}

// underLease is the EHF events worker's lease
// (internal/invoices/ehf_events_worker.go) on this worker's own key: one
// connection a cycle, the lease taken and released on it, and every
// statement of the cycle run on it. The unlock runs on a context stripped of
// cancellation, and a failed unlock discards the connection.
func (w *SpotPriceWorker) underLease(ctx context.Context, action func(context.Context, *pgxpool.Conn) error) (bool, error) {
	conn, err := w.deps.Pool.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("energy: acquire a connection for the spot price lease: %w", err)
	}
	defer conn.Release()

	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, spotPriceLeaseKey).Scan(&acquired); err != nil {
		return false, fmt.Errorf("energy: take the spot price lease: %w", err)
	}
	if !acquired {
		w.logger().Debug("the spot price lease is held by another replica; skipping this cycle",
			"worker", spotPriceWorkerName)
		return false, nil
	}
	defer func() {
		release := context.WithoutCancel(ctx)
		if _, err := conn.Exec(release, `SELECT pg_advisory_unlock($1)`, spotPriceLeaseKey); err != nil {
			w.logger().Error("releasing the spot price lease failed; discarding the connection",
				"worker", spotPriceWorkerName, "error", err)
			_ = conn.Conn().Close(release)
		}
	}()
	return true, action(ctx, conn)
}

// now is the worker's clock, so tests control "today" exactly as they do
// for the handlers.
func (w *SpotPriceWorker) now() time.Time {
	if w.deps.Clock != nil {
		return w.deps.Clock().UTC()
	}
	return time.Now().UTC()
}

func (w *SpotPriceWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}
