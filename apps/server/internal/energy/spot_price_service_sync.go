package energy

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
)

// This file is the spot price worker's cycle when the shared spot-price
// service is the source (spot-price service design S4–S6, plan readings
// 8–11): ENERGY_SPOT_PRICE_SERVICE_URL set, under delivery A's lease
// ENRGSPT1, every request through spot_price_service_client.go. A cycle
// does three things, at most twenty requests between them, in this order:
//
//  1. Areas. GET /api/v1/areas once. The areas followed are those the
//     service is active for (design S4: an area in use that it does not
//     follow gets no fetch rows; the coverage says so); they each keep a row in energy.spot_price_history, its floor the Oslo
//     day of the service's first interval, raised to
//     ENERGY_SPOT_PRICE_HISTORY_FROM when that is later, and kept as it was
//     while the service's coverage is null. When the request fails, every
//     history row records the failure and backs off, and the live window
//     still runs for the areas in use: it needs nothing the answer gives.
//  2. The live window, per followed area: the civil days in Europe/Oslo from
//     today − 14 to tomorrow, read in one request when any of them is due —
//     no fetch row yet, or its next_attempt_at come — or when the window was
//     last read more than a day ago, so a correction's version bump arrives.
//     Each day of the answer is judged on its own: whole and every interval
//     with NOK, it is written (ok when every interval is final, pending and
//     asked again in an hour while any is preliminary); absent, incomplete or
//     without NOK it is pending for today and tomorrow, missing for a past
//     day (daily, given up after the seventh); over an imported day of
//     another resolution it is failed with delivery A's sentence. A failed
//     request fails every due day, backed off as delivery A does, or until
//     a 429's Retry-After when that is later.
//  3. History, with what the budget leaves, per active area while its walk
//     is not complete and not backing off: 35 civil days a request, from the
//     day before the live window backwards to the floor, each window written
//     in one transaction with the walk's row. A window's days that cannot be
//     written are counted in days_skipped and left for the file import; no
//     fetch row is recorded for them. A failed request backs the walk off
//     without losing its cursor; a floor moved earlier resumes it.
//
// While any active area's walk can go on, the worker's next cycle comes
// after a minute instead of the poll (design S5), so history copies in
// about half an hour rather than days. A 429 ends the cycle's requests.

const (
	// spotServiceLiveBackDays is how far back the live window reaches: the
	// fourteen days the service itself re-checks Nord Pool's corrections
	// over (design S5).
	spotServiceLiveBackDays = 14
	// spotServiceHistoryDays is one history window, the service's limit on
	// /api/v1/prices.
	spotServiceHistoryDays = 35
	// maxSpotServiceWindow is that limit as the service checks it, in
	// hours: a 35-day window across the autumn change is 841 hours, so it
	// is a day shorter.
	maxSpotServiceWindow = spotServiceHistoryDays * 24 * time.Hour
	// spotServiceRereadAfter is how old the live window's last read may be
	// before it is read again with nothing due.
	spotServiceRereadAfter = 24 * time.Hour
	// spotServiceCopyingWait is the wait between cycles while history is
	// being copied.
	spotServiceCopyingWait = time.Minute
)

// The sentences of a day the service holds nothing writable of.
const (
	spotServiceNoDay = "the spot-price service holds no prices for the day"
	spotServiceNoNOK = "the spot-price service holds no NOK price for the day"
)

// spotServiceAreaCode is the zones the sync follows (plan reading 8): the
// contract's twelve, and nothing longer than the column's four characters.
var spotServiceAreaCode = regexp.MustCompile(`^(NO[1-5]|SE[1-4]|DK[12]|FI)$`)

// spotServiceDay is one civil day of an answer, judged (design S6): its
// rows when it is written (verdict ""), else the sentence of why not —
// failed when the sentence is a value Vantigo cannot take rather than a day
// the service does not hold whole.
type spotServiceDay struct {
	day         time.Time
	rows        []spotPriceRow
	verdict     string
	failed      bool
	preliminary bool
}

// judgeSpotServiceDays splits intervals into the civil days in zone from
// first to last (dates, inclusive) and judges each: written only when
// every interval is one Vantigo can take, the day is whole
// (judgeSpotPriceDay) and every interval carries NOK.
func judgeSpotServiceDays(intervals []spotServiceInterval, first, last time.Time, zone *time.Location) []spotServiceDay {
	byDay := map[string][]spotServiceInterval{}
	for _, iv := range intervals {
		key := iv.row.Start.In(zone).Format(time.DateOnly)
		byDay[key] = append(byDay[key], iv)
	}
	var out []spotServiceDay
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		day := spotServiceDay{day: d}
		ivs := byDay[spotFeedDayText(d)]
		noNok := false
		for _, iv := range ivs {
			if iv.failure != "" && !day.failed {
				day.verdict, day.failed = iv.failure, true
			}
			noNok = noNok || !iv.hasNok
			day.preliminary = day.preliminary || iv.row.State == spotPriceStatePreliminary
			day.rows = append(day.rows, iv.row)
		}
		switch {
		case day.failed:
		case len(ivs) == 0:
			day.verdict = spotServiceNoDay
		default:
			if reason := judgeSpotPriceDay(day.rows, d, zone); reason != "" {
				day.verdict = reason
			} else if noNok {
				day.verdict = spotServiceNoNOK
			}
		}
		if day.verdict != "" {
			day.rows = nil
		}
		out = append(out, day)
	}
	return out
}

// spotServiceWriteCounts is what a write of days did.
type spotServiceWriteCounts struct {
	inserted, replaced, unchanged, deleted int
}

// writeSpotServiceDays writes the writable days on tx, the area's lock
// held: planned together by planSpotPriceDaysWrite, a day in conflict with
// an imported day of another resolution is left out and the rest planned
// again, so nothing the exclusion constraint would refuse is written and a
// day is all or nothing. It answers the days refused, by spotFeedDayText.
func writeSpotServiceDays(ctx context.Context, tx pgx.Tx, area string, days []spotServiceDay, zone *time.Location, now time.Time) (map[string]bool, spotServiceWriteCounts, error) {
	q := store.New(tx)
	refused := map[string]bool{}
	for {
		var rows []spotPriceRow
		for _, d := range days {
			if d.verdict == "" && !refused[spotFeedDayText(d.day)] {
				rows = append(rows, d.rows...)
			}
		}
		if len(rows) == 0 {
			return refused, spotServiceWriteCounts{}, nil
		}
		plan, err := planSpotPriceDaysWrite(ctx, q, area, rows, zone)
		if err != nil {
			return nil, spotServiceWriteCounts{}, err
		}
		if len(plan.conflicts) == 0 {
			if err := applySpotPriceWrite(ctx, tx, plan, spotPriceSourceService, now); err != nil {
				return nil, spotServiceWriteCounts{}, err
			}
			inserted, replaced, unchanged := plan.Counts()
			return refused, spotServiceWriteCounts{inserted, replaced, unchanged, plan.Deleted()}, nil
		}
		// Each pass refuses at least one more day, so this ends.
		for _, c := range plan.conflicts {
			refused[rows[c.Index].Start.In(zone).Format(time.DateOnly)] = true
		}
	}
}

// spotServiceWindowDay is one day of the live window and whether it is
// due.
type spotServiceWindowDay struct {
	spotFeedDue
	due bool
}

// planSpotServiceWindow is reading 9's planner, a pure function: the days
// from today − 14 to tomorrow, each due when it has no fetch row or its
// next_attempt_at has come — an ok row, or a missing one given up on, has
// none — and the window due when any day is, or when lastRead (the newest
// fetched_at of its days) is absent or more than a day old.
func planSpotServiceWindow(now, today time.Time, area string, fetches map[string]store.EnergySpotPriceFetch, lastRead *time.Time) ([]spotServiceWindowDay, bool) {
	days := make([]spotServiceWindowDay, 0, spotServiceLiveBackDays+2)
	windowDue := lastRead == nil || now.Sub(*lastRead) > spotServiceRereadAfter
	for offset := -spotServiceLiveBackDays; offset <= 1; offset++ {
		day := today.AddDate(0, 0, offset)
		d := spotServiceWindowDay{spotFeedDue: spotFeedDue{area: area, day: day, tomorrow: offset == 1, past: offset < 0}}
		if f, ok := fetches[spotFeedDayText(day)]; ok {
			d.status, d.attempts = f.Status, f.Attempts
			d.due = f.NextAttemptAt != nil && !f.NextAttemptAt.After(now)
		} else {
			d.due = true
		}
		windowDue = windowDue || d.due
		days = append(days, d)
	}
	return days, windowDue
}

// serviceCycle is one cycle with the service as the source, on the lease's
// connection, answering the requests it made.
func (w *SpotPriceWorker) serviceCycle(ctx context.Context, conn *pgxpool.Conn) (int, error) {
	q := store.New(conn)
	now := w.now()
	today := spotFeedToday(now, w.zone)
	inUse, err := q.ListSpotPriceAreasInUse(ctx)
	if err != nil {
		return 0, fmt.Errorf("energy: read the price areas in use: %w", err)
	}
	rows, err := q.ListSpotPriceHistory(ctx)
	if err != nil {
		return 0, fmt.Errorf("energy: read the spot price history: %w", err)
	}
	history := make(map[string]*store.EnergySpotPriceHistory, len(rows))
	for i := range rows {
		history[rows[i].PriceArea] = &rows[i]
	}

	requests := 1
	budget, cancel := context.WithTimeout(ctx, spotFeedFetchBudget)
	areas, fail := w.service.areas(budget)
	cancel()
	if ctx.Err() != nil {
		return requests, nil
	}
	write, cancelWrite := context.WithTimeout(context.WithoutCancel(ctx), spotFeedWriteBudget)
	active, err := w.recordAreas(write, q, history, areas, fail, today)
	cancelWrite()
	if err != nil {
		return requests, err
	}

	// The areas the service follows, as it answered (design S4): an area in
	// use that it does not follow gets no fetch rows — the coverage says so
	// instead. When /areas failed nothing is known of what it follows, and
	// the live window still runs for the areas in use.
	followed := slices.Clone(active)
	if fail.failed() {
		for _, area := range inUse {
			if spotServiceAreaCode.MatchString(area) && !slices.Contains(followed, area) {
				followed = append(followed, area)
			}
		}
	}
	slices.Sort(followed)
	stop := fail.rateLimited
	for _, area := range followed {
		if stop || requests >= maxSpotPriceFetchesPerCycle || ctx.Err() != nil {
			break
		}
		days, due, err := w.liveWindow(ctx, q, area, today)
		if err != nil {
			return requests, err
		}
		if !due {
			continue
		}
		requests++
		if stop, err = w.readLiveWindow(ctx, conn, area, days, today); err != nil {
			return requests, err
		}
	}
	for _, area := range active {
		h := history[area]
		for !stop && requests < maxSpotPriceFetchesPerCycle && ctx.Err() == nil && spotServiceWalkable(*h, w.now(), today) {
			requests++
			if stop, err = w.readHistoryWindow(ctx, conn, h, today); err != nil {
				return requests, err
			}
		}
	}

	copying := false
	for _, area := range active {
		copying = copying || spotServiceWalkable(*history[area], w.now(), today)
	}
	w.copying.Store(copying)
	return requests, nil
}

// recordAreas writes what GET /api/v1/areas answered on the history rows
// and answers the active areas, sorted. On a failure every existing row
// records it and backs off, and none is active.
func (w *SpotPriceWorker) recordAreas(ctx context.Context, q *store.Queries, history map[string]*store.EnergySpotPriceHistory, areas []spotServiceArea, fail spotServiceFailure, today time.Time) ([]string, error) {
	now := w.now()
	if fail.failed() {
		w.logFailure("", fail.sentence)
		for _, h := range history {
			h.Attempts++
			spotServiceBackOff(h, fail, now)
			if err := w.saveHistory(ctx, q, *h, now); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
	var active []string
	for _, a := range areas {
		if !a.active || !spotServiceAreaCode.MatchString(a.code) || slices.Contains(active, a.code) {
			continue
		}
		active = append(active, a.code)
		h, ok := history[a.code]
		if !ok {
			h = &store.EnergySpotPriceHistory{PriceArea: a.code}
			history[a.code] = h
		}
		if a.firstStart != nil {
			y, m, d := a.firstStart.In(w.zone).Date()
			h.FloorDay = pgtype.Date{Time: time.Date(y, m, d, 0, 0, 0, 0, time.UTC), Valid: true}
		}
		if from := w.historyFrom(); h.FloorDay.Valid && from.After(h.FloorDay.Time) {
			h.FloorDay.Time = from
		}
		// A floor at or after the live window's first day leaves nothing
		// to walk: the walk is complete where it would have begun.
		if end := today.AddDate(0, 0, -spotServiceLiveBackDays); !h.CursorDay.Valid && h.FloorDay.Valid && !end.After(h.FloorDay.Time) {
			h.CursorDay = pgtype.Date{Time: end, Valid: true}
		}
		if err := w.saveHistory(ctx, q, *h, now); err != nil {
			return nil, err
		}
	}
	slices.Sort(active)
	return active, nil
}

// historyFrom is ENERGY_SPOT_PRICE_HISTORY_FROM, zero when unset.
func (w *SpotPriceWorker) historyFrom() time.Time {
	if w.deps.Config == nil {
		return time.Time{}
	}
	return w.deps.Config.EnergySpotPriceHistoryFrom
}

// spotServiceWalkable is whether h's walk can take its next window now: a
// floor known, the cursor (or, before the first window, the live window's
// first day) after it, and no backoff pending.
func spotServiceWalkable(h store.EnergySpotPriceHistory, now, today time.Time) bool {
	if !h.FloorDay.Valid || (h.NextAttemptAt != nil && h.NextAttemptAt.After(now)) {
		return false
	}
	return spotServiceWalkEnd(h, today).After(h.FloorDay.Time)
}

// spotServiceWalkEnd is the day after the next history window: the cursor,
// or the live window's first day before the first window.
func spotServiceWalkEnd(h store.EnergySpotPriceHistory, today time.Time) time.Time {
	if h.CursorDay.Valid {
		return h.CursorDay.Time
	}
	return today.AddDate(0, 0, -spotServiceLiveBackDays)
}

// spotServiceBackOff sets h's next attempt after a failure counted in
// h.Attempts: delivery A's backoff, or the 429's Retry-After when later.
func spotServiceBackOff(h *store.EnergySpotPriceHistory, fail spotServiceFailure, now time.Time) {
	sentence := fail.sentence
	next := spotServiceNextAttempt(now, h.Attempts, fail)
	h.LastError, h.NextAttemptAt = &sentence, &next
}

// spotServiceNextAttempt is when a failed request is made again after its
// attempts-th failure.
func spotServiceNextAttempt(now time.Time, attempts int32, fail spotServiceFailure) time.Time {
	next := now.Add(spotFeedBackoff(attempts))
	if fail.retryAt.After(next) {
		next = fail.retryAt
	}
	return next
}

func (w *SpotPriceWorker) saveHistory(ctx context.Context, q *store.Queries, h store.EnergySpotPriceHistory, now time.Time) error {
	if err := q.UpsertSpotPriceHistory(ctx, store.UpsertSpotPriceHistoryParams{
		PriceArea: h.PriceArea, CursorDay: h.CursorDay, FloorDay: h.FloorDay, DaysSkipped: h.DaysSkipped,
		Attempts: h.Attempts, LastError: h.LastError, NextAttemptAt: h.NextAttemptAt, UpdatedAt: now,
	}); err != nil {
		return fmt.Errorf("energy: record the spot price history of %s: %w", h.PriceArea, err)
	}
	return nil
}

// liveWindow reads what the planner needs of area's live window and plans.
func (w *SpotPriceWorker) liveWindow(ctx context.Context, q *store.Queries, area string, today time.Time) ([]spotServiceWindowDay, bool, error) {
	from, to := today.AddDate(0, 0, -spotServiceLiveBackDays), today.AddDate(0, 0, 1)
	rows, err := q.ListSpotPriceFetches(ctx, store.ListSpotPriceFetchesParams{
		PriceArea: area, FromDay: pgtype.Date{Time: from, Valid: true}, ToDay: pgtype.Date{Time: to, Valid: true},
	})
	if err != nil {
		return nil, false, fmt.Errorf("energy: read the spot price fetches of %s: %w", area, err)
	}
	fetches := make(map[string]store.EnergySpotPriceFetch, len(rows))
	for _, f := range rows {
		fetches[spotFeedDayText(f.Day.Time)] = f
	}
	reads, err := q.LatestSpotPriceWindowRead(ctx, store.LatestSpotPriceWindowReadParams{PriceArea: area, FromDay: pgtype.Date{Time: from, Valid: true}})
	if err != nil {
		return nil, false, fmt.Errorf("energy: read when the live window of %s was last read: %w", area, err)
	}
	var lastRead *time.Time
	if len(reads) > 0 {
		lastRead = &reads[0]
	}
	days, due := planSpotServiceWindow(w.now(), today, area, fetches, lastRead)
	return days, due, nil
}

// readLiveWindow is the live window's one request and its record, answering
// whether the service is rate limiting. A caller gone before the answer (a
// shutdown) records nothing; the record itself runs past it, within
// spotFeedWriteBudget, as delivery A's fetchDay does.
func (w *SpotPriceWorker) readLiveWindow(ctx context.Context, conn *pgxpool.Conn, area string, days []spotServiceWindowDay, today time.Time) (bool, error) {
	first, last := days[0].day, days[len(days)-1].day
	budget, cancel := context.WithTimeout(ctx, spotFeedFetchBudget)
	intervals, fail := w.service.prices(budget, area, w.osloMidnight(first), w.osloMidnight(last.AddDate(0, 0, 1)))
	cancel()
	if ctx.Err() != nil {
		return false, nil
	}
	write, cancelWrite := context.WithTimeout(context.WithoutCancel(ctx), spotFeedWriteBudget)
	defer cancelWrite()
	now := w.now()
	if fail.failed() {
		w.logFailure(area, fail.sentence)
		q := store.New(conn)
		for _, d := range days {
			if !d.due {
				continue
			}
			sentence := fail.sentence
			next := spotServiceNextAttempt(now, d.attemptsIn(spotFetchFailed), fail)
			if err := w.record(write, q, d.spotFeedDue, spotFetchFailed, &sentence, now, nil, &next); err != nil {
				return false, err
			}
		}
		return fail.rateLimited, nil
	}

	judged := judgeSpotServiceDays(intervals, first, last, w.zone)
	var counts spotServiceWriteCounts
	written := 0
	err := db.WithTx(write, conn, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockSpotPriceAreas(write, tx, []string{area}); err != nil {
			return err
		}
		refused, c, err := writeSpotServiceDays(write, tx, area, judged, w.zone, now)
		if err != nil {
			return err
		}
		counts = c
		tq := store.New(tx)
		for i, j := range judged {
			d := days[i]
			ok := j.verdict == "" && !refused[spotFeedDayText(j.day)]
			if ok {
				written++
			}
			if err := w.recordLiveDay(write, tq, d, j, ok, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("energy: store the spot prices of %s from the spot-price service: %w", area, err)
	}
	w.logger().Info("spot prices read from the spot-price service", "worker", spotPriceWorkerName, "area", area,
		"from", spotFeedDayText(first), "to", spotFeedDayText(last), "daysWritten", written,
		"inserted", counts.inserted, "replaced", counts.replaced, "unchanged", counts.unchanged, "deleted", counts.deleted)
	return false, nil
}

// recordLiveDay records one judged day of a live window that answered
// (reading 9). A day written is recorded whether it was due or not — a
// correction or a final day comes early — stamped fetched_at now; a day
// not written is recorded only when due, so a day missing is asked daily
// however often the window is read.
func (w *SpotPriceWorker) recordLiveDay(ctx context.Context, q *store.Queries, d spotServiceWindowDay, j spotServiceDay, written bool, now time.Time) error {
	switch {
	case written && j.preliminary:
		next := now.Add(spotPricePendingRetry)
		return w.record(ctx, q, d.spotFeedDue, spotFetchPending, nil, now, &now, &next)
	case written:
		return w.record(ctx, q, d.spotFeedDue, spotFetchOK, nil, now, &now, nil)
	case !d.due:
		return nil
	case j.verdict == "":
		// Refused: an imported day of another resolution holds it.
		return w.recordLiveFailed(ctx, q, d, spotFeedOverlaps, now)
	case j.failed:
		return w.recordLiveFailed(ctx, q, d, j.verdict, now)
	case !d.past:
		// Today or tomorrow: not published, or not whole yet.
		next := now.Add(spotPricePendingRetry)
		return w.record(ctx, q, d.spotFeedDue, spotFetchPending, nil, now, &now, &next)
	default:
		attempts := d.attemptsIn(spotFetchMissing)
		var next *time.Time
		if attempts < maxSpotPriceMissingAttempts {
			n := now.Add(spotPriceMissingRetry)
			next = &n
		}
		reason := j.verdict
		return w.record(ctx, q, d.spotFeedDue, spotFetchMissing, &reason, now, &now, next)
	}
}

func (w *SpotPriceWorker) recordLiveFailed(ctx context.Context, q *store.Queries, d spotServiceWindowDay, sentence string, now time.Time) error {
	next := now.Add(spotFeedBackoff(d.attemptsIn(spotFetchFailed)))
	w.logger().Warn("the spot-price service's day could not be stored", "worker", spotPriceWorkerName,
		"area", d.area, "day", spotFeedDayText(d.day), "error", sentence)
	return w.record(ctx, q, d.spotFeedDue, spotFetchFailed, &sentence, now, &now, &next)
}

// readHistoryWindow is one history window of h's walk (reading 10): the 35
// civil days before its end, never before the floor and a day fewer when
// they are longer than the service allows, read, written and the walk's row
// moved in one transaction. It answers whether the service is rate
// limiting. h is updated as recorded.
func (w *SpotPriceWorker) readHistoryWindow(ctx context.Context, conn *pgxpool.Conn, h *store.EnergySpotPriceHistory, today time.Time) (bool, error) {
	end := spotServiceWalkEnd(*h, today)
	start := end.AddDate(0, 0, -spotServiceHistoryDays)
	if start.Before(h.FloorDay.Time) {
		start = h.FloorDay.Time
	}
	if w.osloMidnight(end).Sub(w.osloMidnight(start)) > maxSpotServiceWindow {
		start = start.AddDate(0, 0, 1)
	}
	budget, cancel := context.WithTimeout(ctx, spotFeedFetchBudget)
	intervals, fail := w.service.prices(budget, h.PriceArea, w.osloMidnight(start), w.osloMidnight(end))
	cancel()
	if ctx.Err() != nil {
		return false, nil
	}
	write, cancelWrite := context.WithTimeout(context.WithoutCancel(ctx), spotFeedWriteBudget)
	defer cancelWrite()
	now := w.now()
	if fail.failed() {
		w.logFailure(h.PriceArea, fail.sentence)
		h.Attempts++
		spotServiceBackOff(h, fail, now)
		return fail.rateLimited, w.saveHistory(write, store.New(conn), *h, now)
	}

	judged := judgeSpotServiceDays(intervals, start, end.AddDate(0, 0, -1), w.zone)
	next := *h
	var counts spotServiceWriteCounts
	err := db.WithTx(write, conn, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockSpotPriceAreas(write, tx, []string{h.PriceArea}); err != nil {
			return err
		}
		refused, c, err := writeSpotServiceDays(write, tx, h.PriceArea, judged, w.zone, now)
		if err != nil {
			return err
		}
		counts = c
		skipped := int32(len(refused))
		for _, j := range judged {
			if j.verdict != "" {
				skipped++
			}
		}
		next.CursorDay = pgtype.Date{Time: start, Valid: true}
		next.DaysSkipped += skipped
		next.Attempts, next.LastError, next.NextAttemptAt = 0, nil, nil
		return w.saveHistory(write, store.New(tx), next, now)
	})
	if err != nil {
		return false, fmt.Errorf("energy: store the spot price history of %s from the spot-price service: %w", h.PriceArea, err)
	}
	*h = next
	w.logger().Info("spot price history copied from the spot-price service", "worker", spotPriceWorkerName, "area", h.PriceArea,
		"from", spotFeedDayText(start), "to", spotFeedDayText(end.AddDate(0, 0, -1)), "daysSkipped", h.DaysSkipped,
		"inserted", counts.inserted, "replaced", counts.replaced, "unchanged", counts.unchanged, "deleted", counts.deleted)
	return false, nil
}

// osloMidnight is the first instant of day, a civil date, in the worker's
// zone.
func (w *SpotPriceWorker) osloMidnight(day time.Time) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, w.zone)
}

// logFailure logs a failed request with the area ("" for the areas read),
// the service's host and the sentence: never the address's query, the key
// or the body.
func (w *SpotPriceWorker) logFailure(area, sentence string) {
	w.logger().Warn("the spot-price service could not be read", "worker", spotPriceWorkerName,
		"area", area, "host", w.service.base.Hostname(), "error", sentence)
}
