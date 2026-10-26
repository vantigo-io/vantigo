package energy

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
	"github.com/vantigo-io/vantigo/server/internal/netguard"
)

// SetImportRunHook makes f run inside every import run the worker starts,
// before its first row (import_worker.go's importRunHookForTest); it answers
// the function that takes the hook away again. The hook is the package's, so
// the test that sets it does not run in parallel.
func SetImportRunHook(f func(ctx context.Context, importID int32) error) func() {
	previous := importRunHookForTest
	importRunHookForTest = f
	return func() { importRunHookForTest = previous }
}

// ImportLeaseKeyForTest is the import worker's advisory-lease key, exported so
// a test can take the same lock from a second connection and prove a cycle
// skips (energy import design D6).
const ImportLeaseKeyForTest = importLeaseKey

// SupplyPeriodLockClassForTest is the supply periods' advisory-lock class,
// exported so a test can hold a point's supply-period lock as an endpoint
// does and prove what an import row's own locks leave free.
const SupplyPeriodLockClassForTest = supplyPeriodLockClass

// SpotPriceLeaseKeyForTest is the spot price worker's advisory-lease key,
// exported so a test can hold it from a second connection and prove a cycle
// skips (plan reading 12).
const SpotPriceLeaseKeyForTest = spotPriceLeaseKey

// SpotFeedInterval is one interval the feed's parser answered.
type SpotFeedInterval struct {
	PriceArea    string
	Start, End   time.Time
	NokPerKwh    float64
	EurPerKwh    *float64
	ExchangeRate *float64
}

// ParseSpotFeedDayForTest is parseSpotFeedDay over body for area on day
// (YYYY-MM-DD, a civil date in Europe/Oslo): the intervals, or the sentence
// of the failure.
func ParseSpotFeedDayForTest(body []byte, area, day string) ([]SpotFeedInterval, string) {
	rows, failure := parseSpotFeedDay(body, area, mustSpotFeedDay(day), mustSpotFeedZone())
	out := make([]SpotFeedInterval, 0, len(rows))
	for _, r := range rows {
		out = append(out, SpotFeedInterval{
			PriceArea: r.PriceArea, Start: r.Start, End: r.End,
			NokPerKwh: r.NokPerKwh, EurPerKwh: r.EurPerKwh, ExchangeRate: r.ExchangeRate,
		})
	}
	return out, failure
}

// RenderSpotFeedURLForTest validates template as the worker does and renders
// it for area on day (YYYY-MM-DD).
func RenderSpotFeedURLForTest(template, area, day string) (string, error) {
	t, err := parseSpotFeedTemplate(template)
	if err != nil {
		return "", err
	}
	return t.render(area, mustSpotFeedDay(day)), nil
}

// SpotFeedPlanArea is what the planner knows of one area: the days
// (YYYY-MM-DD) a price starts in, and its fetch rows.
type SpotFeedPlanArea struct {
	Area       string
	StoredDays []string
	Fetches    []store.EnergySpotPriceFetch
}

// SpotFeedPlanned is one (area, day) the planner asks for.
type SpotFeedPlanned struct {
	Area, Day string
	Tomorrow  bool
	Attempts  int32
}

// PlanSpotFeedFetchesForTest is the planner, planSpotFeedFetches, at now.
func PlanSpotFeedFetchesForTest(now time.Time, areas []SpotFeedPlanArea) []SpotFeedPlanned {
	records := make([]spotFeedAreaRecord, 0, len(areas))
	for _, a := range areas {
		r := spotFeedAreaRecord{area: a.Area, stored: map[string]bool{}, fetches: map[string]store.EnergySpotPriceFetch{}}
		for _, d := range a.StoredDays {
			r.stored[d] = true
		}
		for _, f := range a.Fetches {
			r.fetches[spotFeedDayText(f.Day.Time)] = f
		}
		records = append(records, r)
	}
	var out []SpotFeedPlanned
	for _, d := range planSpotFeedFetches(now, mustSpotFeedZone(), records) {
		out = append(out, SpotFeedPlanned{Area: d.area, Day: spotFeedDayText(d.day), Tomorrow: d.tomorrow, Attempts: d.attempts})
	}
	return out
}

// SpotFeedBackoffForTest is a failed day's wait after its attempts-th
// request.
func SpotFeedBackoffForTest(attempts int32) time.Duration { return spotFeedBackoff(attempts) }

func mustSpotFeedDay(day string) time.Time {
	d, err := time.Parse(time.DateOnly, day)
	if err != nil {
		panic(err)
	}
	return d
}

func mustSpotFeedZone() *time.Location {
	zone, err := time.LoadLocation(spotPriceDayZone)
	if err != nil {
		panic(err)
	}
	return zone
}

// SpotPriceRow is one interval as the write takes it.
type SpotPriceRow = spotPriceRow

// SpotPriceWriteCounts is what one write of the spot-price service's days
// did: the plan's counts, and the conflicts that stopped it.
type SpotPriceWriteCounts struct {
	Inserted, Replaced, Unchanged, Deleted, Conflicts int
}

// WriteSpotPriceDaysForTest is the spot-price service's write of rows, whole
// civil days of area in Europe/Oslo, at now: in one transaction under the
// area's lock, planned by planSpotPriceDaysWrite and applied with the Service
// source — or, when a conflict is left, nothing written.
func WriteSpotPriceDaysForTest(ctx context.Context, pool *pgxpool.Pool, area string, rows []SpotPriceRow, now time.Time) (SpotPriceWriteCounts, error) {
	var out SpotPriceWriteCounts
	err := db.WithTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockSpotPriceAreas(ctx, tx, []string{area}); err != nil {
			return err
		}
		plan, err := planSpotPriceDaysWrite(ctx, store.New(tx), area, rows, mustSpotFeedZone())
		if err != nil {
			return err
		}
		out.Inserted, out.Replaced, out.Unchanged = plan.Counts()
		out.Deleted, out.Conflicts = plan.Deleted(), len(plan.conflicts)
		if out.Conflicts > 0 {
			return nil
		}
		return applySpotPriceWrite(ctx, tx, plan, spotPriceSourceService, now)
	})
	return out, err
}

// SpotServiceClientForTest is the spot-price service's client as a test
// drives it, below the worker.
type SpotServiceClientForTest struct{ c *spotServiceClient }

// NewSpotServiceClientForTest is the client over rt (wrapped in the
// host-only round-tripper, as with modtest.WithTransport), logging to
// logger, its clock now.
func NewSpotServiceClientForTest(rt http.RoundTripper, base, key string, logger *slog.Logger, now func() time.Time) (*SpotServiceClientForTest, error) {
	c, err := newSpotServiceClient(base, key, rt, func() *slog.Logger { return logger }, now)
	return &SpotServiceClientForTest{c: c}, err
}

// NewGuardedSpotServiceClientForTest is the client over the production
// transport, resolving through resolver and dialling through dial.
func NewGuardedSpotServiceClientForTest(resolver netguard.Resolver, dial func(ctx context.Context, network, address string) (net.Conn, error), base, key string) (*SpotServiceClientForTest, error) {
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	rt := spotServiceGuardedTransport(resolver, dial, u.Scheme == "http")
	c, err := newSpotServiceClient(base, key, rt, slog.Default, time.Now)
	return &SpotServiceClientForTest{c: c}, err
}

// SpotServiceFailure is a failed request: its sentence, and a 429's
// Retry-After as an instant.
type SpotServiceFailure struct {
	Sentence string
	RetryAt  time.Time
}

func spotServiceFailureForTest(f spotServiceFailure) SpotServiceFailure {
	return SpotServiceFailure{Sentence: f.sentence, RetryAt: f.retryAt}
}

// SpotServiceArea is one zone of GET /api/v1/areas.
type SpotServiceArea struct {
	Code       string
	Active     bool
	FirstStart *time.Time
}

// Areas is GET /api/v1/areas.
func (c *SpotServiceClientForTest) Areas(ctx context.Context) ([]SpotServiceArea, SpotServiceFailure) {
	areas, fail := c.c.areas(ctx)
	out := make([]SpotServiceArea, 0, len(areas))
	for _, a := range areas {
		out = append(out, SpotServiceArea{Code: a.code, Active: a.active, FirstStart: a.firstStart})
	}
	return out, spotServiceFailureForTest(fail)
}

// Get is a GET of any address through the client, as a later caller might
// make one.
func (c *SpotServiceClientForTest) Get(ctx context.Context, target string) SpotServiceFailure {
	_, fail := c.c.getURL(ctx, target)
	return spotServiceFailureForTest(fail)
}

// SpotServiceDay is one civil day of an answer as the sync judges it:
// Verdict "" when it would be written.
type SpotServiceDay struct {
	Day         string
	Intervals   int
	Verdict     string
	Failed      bool
	Preliminary bool
	Rows        []SpotPriceRow
}

// Days is GET /api/v1/prices for area over the Oslo days from first to last
// (YYYY-MM-DD, inclusive), split into days and judged.
func (c *SpotServiceClientForTest) Days(ctx context.Context, area, first, last string) ([]SpotServiceDay, SpotServiceFailure) {
	zone := mustSpotFeedZone()
	from, to := mustSpotFeedDay(first), mustSpotFeedDay(last)
	intervals, fail := c.c.prices(ctx, area,
		time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, zone),
		time.Date(to.Year(), to.Month(), to.Day()+1, 0, 0, 0, 0, zone))
	if fail.failed() {
		return nil, spotServiceFailureForTest(fail)
	}
	counts := map[string]int{}
	for _, iv := range intervals {
		counts[iv.row.Start.In(zone).Format(time.DateOnly)]++
	}
	var out []SpotServiceDay
	for _, d := range judgeSpotServiceDays(intervals, from, to, zone) {
		day := spotFeedDayText(d.day)
		out = append(out, SpotServiceDay{Day: day, Intervals: counts[day], Verdict: d.verdict, Failed: d.failed, Preliminary: d.preliminary, Rows: d.rows})
	}
	return out, SpotServiceFailure{}
}

// SpotServiceWindowDay is one day of the live window as the planner sees it.
type SpotServiceWindowDay struct {
	Day string
	Due bool
}

// PlanSpotServiceWindowForTest is the live window's planner at now over
// fetches, the window last read at lastRead.
func PlanSpotServiceWindowForTest(now time.Time, fetches []store.EnergySpotPriceFetch, lastRead *time.Time) ([]SpotServiceWindowDay, bool) {
	m := map[string]store.EnergySpotPriceFetch{}
	for _, f := range fetches {
		m[spotFeedDayText(f.Day.Time)] = f
	}
	days, due := planSpotServiceWindow(now, spotFeedToday(now, mustSpotFeedZone()), "NO1", m, lastRead)
	out := make([]SpotServiceWindowDay, 0, len(days))
	for _, d := range days {
		out = append(out, SpotServiceWindowDay{Day: spotFeedDayText(d.day), Due: d.due})
	}
	return out, due
}

// SpotPriceNextWaitForTest is the worker's wait after its last cycle.
func SpotPriceNextWaitForTest(w *SpotPriceWorker) time.Duration { return w.nextWait() }
