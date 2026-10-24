package energy

import (
	"context"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/energy/store"
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
		out = append(out, SpotFeedInterval(r))
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
