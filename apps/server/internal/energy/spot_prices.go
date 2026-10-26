package energy

import (
	"context"
	"fmt"
	"slices"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
)

// This file is the two reads of spot prices (energy consumption billing
// design D2): the intervals of one area over a window, and what is held per
// area — the Prices page's chart and its first card. Prices are market data,
// not a customer's, so neither read has a customer gate. The write both the
// import and the feed use is spot_prices_write.go.

// maxSpotPriceWindow is the widest window GET /spot-prices answers (the
// plan's reading 8): a month at a quarter-hour resolution is about 3 000
// intervals, so a month and its edges fit in one answer.
const maxSpotPriceWindow = 35 * 24 * time.Hour

// spotPriceCoverageDays is how many civil days, today included, the
// coverage counts the missing ones of.
const spotPriceCoverageDays = 30

// GetEnergySpotPrices List spot prices of a price area
// (GET /api/v1/energy/spot-prices)
//
// Design D2 and the plan's reading 8: the area as a metering point carries
// it, from before to and the window at most 35 days — every failing check is
// reported at once — then the intervals starting in [from, to), ascending.
func (s *server) GetEnergySpotPrices(ctx context.Context, req gen.GetEnergySpotPricesRequestObject) (gen.GetEnergySpotPricesResponseObject, error) {
	p := req.Params
	errs := map[string][]string{}
	if !priceAreaValid(p.Area) {
		errs["area"] = []string{"Price area must contain two uppercase letters followed by one or two digits."}
	}
	switch {
	case !p.From.Before(p.To):
		errs["to"] = []string{"The to field must be later than from."}
	case p.To.Sub(p.From) > maxSpotPriceWindow:
		errs["to"] = []string{"The to field must be at most 35 days after from."}
	}
	if len(errs) > 0 {
		return gen.GetEnergySpotPrices400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(validationErrorsOneOrMore, errs)), nil
	}
	rows, err := store.New(s.deps.Pool).ListSpotPrices(ctx, store.ListSpotPricesParams{PriceArea: p.Area, FromTs: p.From, ToTs: p.To})
	if err != nil {
		return nil, fmt.Errorf("energy: list spot prices: %w", err)
	}
	out := make(gen.GetEnergySpotPrices200JSONResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, gen.SpotPriceInterval{
			Id: r.ID, PriceArea: r.PriceArea, Start: r.Start, End: r.End,
			NokPerKwh: floatFromNumeric(r.NokPerKwh), EurPerKwh: floatPtrFromNumeric(r.EurPerKwh),
			ExchangeRate: floatPtrFromNumeric(r.ExchangeRate), Source: r.Source,
			State: r.State, Upstream: r.Upstream, RateSource: r.RateSource, UpstreamVersion: r.UpstreamVersion,
		})
	}
	return out, nil
}

// GetEnergySpotPricesCoverage Spot price coverage per price area
// (GET /api/v1/energy/spot-prices/coverage)
//
// Design D2: every area in use (the plan's reading 7) with what is stored of
// it, how many of the last thirty civil days in Europe/Oslo — today, by the
// installation's clock, included — have no interval starting in them, and
// the feed worker's last attempt. An area in use with no prices has no
// bounds and thirty missing days.
//
// Spot-price service design S4 and S8: the source the worker fetches from,
// and with the service every area it follows too — those the sync keeps a
// history row for —, whether the service follows each area once the sync
// has run, the latest interval's length and the history walk.
func (s *server) GetEnergySpotPricesCoverage(ctx context.Context, _ gen.GetEnergySpotPricesCoverageRequestObject) (gen.GetEnergySpotPricesCoverageResponseObject, error) {
	q := store.New(s.deps.Pool)
	areas, err := q.ListSpotPriceAreasInUse(ctx)
	if err != nil {
		return nil, fmt.Errorf("energy: list the spot price areas in use: %w", err)
	}
	bounds, err := q.ListSpotPriceAreaBounds(ctx)
	if err != nil {
		return nil, fmt.Errorf("energy: read the spot price bounds: %w", err)
	}
	fetches, err := q.ListLatestSpotPriceFetches(ctx)
	if err != nil {
		return nil, fmt.Errorf("energy: read the latest spot price fetches: %w", err)
	}
	resolutions, err := q.ListLatestSpotPriceResolutions(ctx)
	if err != nil {
		return nil, fmt.Errorf("energy: read the spot price resolutions: %w", err)
	}
	source := spotPriceSourceOf(s.deps.Config)
	historyOf := map[string]store.EnergySpotPriceHistory{}
	if source == spotPriceCoverageService {
		histories, err := q.ListSpotPriceHistory(ctx)
		if err != nil {
			return nil, fmt.Errorf("energy: read the spot price history: %w", err)
		}
		for _, h := range histories {
			historyOf[h.PriceArea] = h
			if !slices.Contains(areas, h.PriceArea) {
				areas = append(areas, h.PriceArea)
			}
		}
		slices.Sort(areas)
	}
	resolutionOf := make(map[string]int64, len(resolutions))
	for _, r := range resolutions {
		resolutionOf[r.PriceArea] = r.Seconds
	}
	boundsOf := make(map[string]store.ListSpotPriceAreaBoundsRow, len(bounds))
	for _, b := range bounds {
		boundsOf[b.PriceArea] = b
	}
	fetchOf := make(map[string]store.EnergySpotPriceFetch, len(fetches))
	for _, f := range fetches {
		fetchOf[f.PriceArea] = f
	}

	loc, err := time.LoadLocation(spotPriceDayZone)
	if err != nil {
		return nil, fmt.Errorf("energy: load the spot price day zone: %w", err)
	}
	// The window's days as civil dates: counted on a UTC calendar so a DST
	// day is one day like any other, and its first instant read in Oslo.
	now := s.deps.Clock().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	days := make([]string, spotPriceCoverageDays)
	for i := range days {
		days[i] = today.AddDate(0, 0, i-(spotPriceCoverageDays-1)).Format(time.DateOnly)
	}
	since := time.Date(now.Year(), now.Month(), now.Day()-(spotPriceCoverageDays-1), 0, 0, 0, 0, loc)

	out := gen.SpotPriceCoverage{Source: source, Areas: make([]gen.SpotPriceCoverageArea, 0, len(areas))}
	for _, area := range areas {
		item := gen.SpotPriceCoverageArea{PriceArea: area, DaysMissingLast30: spotPriceCoverageDays}
		if b, ok := boundsOf[area]; ok {
			first, last := b.FirstStart, b.LastEnd
			item.FirstStart, item.LastEnd, item.IntervalCount = &first, &last, b.IntervalCount
			held, err := q.ListSpotPriceDays(ctx, store.ListSpotPriceDaysParams{TimeZone: spotPriceDayZone, PriceArea: area, Since: since})
			if err != nil {
				return nil, fmt.Errorf("energy: list the spot price days of %s: %w", area, err)
			}
			// held runs past today when tomorrow's prices are in; only the
			// window's days count.
			stored := make(map[string]bool, len(held))
			for _, d := range held {
				stored[d.Time.Format(time.DateOnly)] = true
			}
			missing := int32(0)
			for _, d := range days {
				if !stored[d] {
					missing++
				}
			}
			item.DaysMissingLast30 = missing
		}
		if f, ok := fetchOf[area]; ok {
			item.LastFetch = &gen.SpotPriceFetch{
				Day: openapi_types.Date{Time: f.Day.Time}, Status: f.Status, Attempts: f.Attempts,
				Error: f.LastError, AttemptedAt: f.AttemptedAt, NextAttemptAt: f.NextAttemptAt,
			}
		}
		if seconds, ok := resolutionOf[area]; ok && seconds > 0 {
			r := spotPriceResolution(seconds)
			item.Resolution = &r
		}
		// The sync keeps a history row for every area the service has
		// followed, marked active while the service still does, so once it
		// has run (any row at all) an area without a row, or with an
		// inactive one, is an area the service does not follow. An inactive
		// row keeps its history: it is still what was copied.
		if source == spotPriceCoverageService && len(historyOf) > 0 {
			h, held := historyOf[area]
			followed := held && h.Active
			item.FollowedByService = &followed
			if held {
				item.History = spotPriceHistoryOf(h)
			}
		}
		out.Areas = append(out.Areas, item)
	}
	return gen.GetEnergySpotPricesCoverage200JSONResponse(out), nil
}

// The coverage's three sources (spot-price service design S8).
const (
	spotPriceCoverageService = "service"
	spotPriceCoverageFeed    = "feed"
	spotPriceCoverageNone    = "none"
)

// spotPriceSourceOf is where the installation's worker fetches prices from:
// the service when its URL is set, the public feed when switched on, else
// files only. config.Load refuses both at once.
func spotPriceSourceOf(c *config.Config) string {
	switch {
	case c != nil && c.EnergySpotPriceServiceURL != "":
		return spotPriceCoverageService
	case c != nil && c.EnergySpotPriceFeedEnabled:
		return spotPriceCoverageFeed
	default:
		return spotPriceCoverageNone
	}
}

// spotPriceResolution is an interval length as an ISO 8601 duration:
// PT15M, PT60M, or the minutes of any other.
func spotPriceResolution(seconds int64) string {
	if seconds%60 != 0 {
		return fmt.Sprintf("PT%dS", seconds)
	}
	return fmt.Sprintf("PT%dM", seconds/60)
}

// spotPriceHistoryOf is an area's history walk as the coverage answers it:
// complete once the cursor has reached the floor. Before the service has
// answered a floor for the area the walk has none and is not complete.
func spotPriceHistoryOf(h store.EnergySpotPriceHistory) *gen.SpotPriceHistory {
	out := &gen.SpotPriceHistory{DaysSkipped: h.DaysSkipped, Error: h.LastError}
	if h.CursorDay.Valid {
		out.CursorDay = &openapi_types.Date{Time: h.CursorDay.Time}
	}
	if h.FloorDay.Valid {
		out.FloorDay = &openapi_types.Date{Time: h.FloorDay.Time}
	}
	out.Complete = h.CursorDay.Valid && h.FloorDay.Valid && !h.CursorDay.Time.After(h.FloorDay.Time)
	return out
}
