package energy

import (
	"context"
	"fmt"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
)

// This file is the summary read of spot prices (spot-price service design
// S8): years of quarter-hours are too many to chart or to send, so the
// Prices page asks for an area's prices per civil hour, day, ISO week or
// month in Europe/Oslo instead, each bucket's time-weighted average with its
// lowest and highest interval.

// spotPriceSummaryWindowDays is the widest window, in civil days, each
// bucket answers (design S8): 35 days of hours, 400 of days, five years of
// weeks and thirty of months.
var spotPriceSummaryWindowDays = map[string]int{
	"hour":  35,
	"day":   400,
	"week":  1830,
	"month": 10980,
}

// GetEnergySpotPricesSummary Summarise spot prices of a price area per hour,
// day, week or month (GET /api/v1/energy/spot-prices/summary)
//
// Design S8 and the plan's reading 14: the area as a metering point carries
// it, the bucket one of the four, from and to on the bucket's boundaries in
// Europe/Oslo, from before to and the window within the bucket's limit —
// every failing check is reported at once — then the buckets holding an
// interval starting in [from, to), ascending.
func (s *server) GetEnergySpotPricesSummary(ctx context.Context, req gen.GetEnergySpotPricesSummaryRequestObject) (gen.GetEnergySpotPricesSummaryResponseObject, error) {
	p := req.Params
	loc, err := time.LoadLocation(spotPriceDayZone)
	if err != nil {
		return nil, fmt.Errorf("energy: load the spot price day zone: %w", err)
	}
	errs := map[string][]string{}
	if !priceAreaValid(p.Area) {
		errs["area"] = []string{"Price area must contain two uppercase letters followed by one or two digits."}
	}
	limit, bucketValid := spotPriceSummaryWindowDays[p.Bucket]
	if !bucketValid {
		errs["bucket"] = []string{"The bucket must be hour, day, week or month."}
	}
	if bucketValid && !onSpotPriceSummaryBoundary(p.From, p.Bucket, loc) {
		errs["from"] = []string{spotPriceSummaryBoundaryMessage("from", p.Bucket)}
	}
	switch {
	case bucketValid && !onSpotPriceSummaryBoundary(p.To, p.Bucket, loc):
		errs["to"] = []string{spotPriceSummaryBoundaryMessage("to", p.Bucket)}
	case !p.From.Before(p.To):
		errs["to"] = []string{"The to field must be later than from."}
	case bucketValid && p.To.After(addCivilDays(p.From, limit, loc)):
		errs["to"] = []string{fmt.Sprintf("The to field must be at most %d days after from for the %s bucket.", limit, p.Bucket)}
	}
	if len(errs) > 0 {
		return gen.GetEnergySpotPricesSummary400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(validationErrorsOneOrMore, errs)), nil
	}

	rows, err := store.New(s.deps.Pool).SummariseSpotPrices(ctx, store.SummariseSpotPricesParams{
		Bucket: p.Bucket, PriceArea: p.Area, FromTs: p.From, ToTs: p.To,
	})
	if err != nil {
		return nil, fmt.Errorf("energy: summarise spot prices: %w", err)
	}
	out := gen.SpotPriceSummary{
		PriceArea: p.Area, Bucket: p.Bucket, TimeZone: spotPriceDayZone,
		Buckets: make([]gen.SpotPriceSummaryBucket, 0, len(rows)),
	}
	for _, r := range rows {
		start, end := spotPriceSummaryBucketBounds(r.BucketLocal.Time, p.Bucket, loc)
		out.Buckets = append(out.Buckets, gen.SpotPriceSummaryBucket{
			Start: start, End: end,
			AvgNokPerKwh: floatFromNumeric(r.AvgNokPerKwh), MinNokPerKwh: floatFromNumeric(r.MinNokPerKwh),
			MaxNokPerKwh: floatFromNumeric(r.MaxNokPerKwh), CoveredMinutes: r.CoveredMinutes, Preliminary: r.Preliminary,
		})
	}
	return gen.GetEnergySpotPricesSummary200JSONResponse(out), nil
}

// spotPriceSummaryBucketBounds is a bucket's [start, end) as instants. The
// query answers an hour as its UTC wall clock, so the autumn's two 02:00
// hours stay apart, and the other buckets as their local start in Oslo; a
// day, a week or a month ends at the next one's local midnight, so a DST day
// is 23 or 25 hours. Every bound is in Oslo, so each carries its offset.
func spotPriceSummaryBucketBounds(local time.Time, bucket string, loc *time.Location) (time.Time, time.Time) {
	if bucket == "hour" {
		start := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), 0, 0, 0, time.UTC)
		return start.In(loc), start.Add(time.Hour).In(loc)
	}
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	switch bucket {
	case "week":
		return start, start.AddDate(0, 0, 7)
	case "month":
		return start, start.AddDate(0, 1, 0)
	default:
		return start, start.AddDate(0, 0, 1)
	}
}

// onSpotPriceSummaryBoundary is whether t starts a bucket in loc: a whole
// hour (Oslo's offset is whole hours, so the instant's hour is the local
// one), a local midnight, a Monday's or the first of a month's.
func onSpotPriceSummaryBoundary(t time.Time, bucket string, loc *time.Location) bool {
	if bucket == "hour" {
		return t.Truncate(time.Hour).Equal(t)
	}
	l := t.In(loc)
	if l.Hour() != 0 || l.Minute() != 0 || l.Second() != 0 || l.Nanosecond() != 0 {
		return false
	}
	switch bucket {
	case "week":
		return l.Weekday() == time.Monday
	case "month":
		return l.Day() == 1
	default:
		return true
	}
}

// spotPriceSummaryBoundaryMessage is the 400's sentence for a field off the
// bucket's boundary.
func spotPriceSummaryBoundaryMessage(field, bucket string) string {
	switch bucket {
	case "hour":
		return fmt.Sprintf("The %s field must be a whole hour.", field)
	case "week":
		return fmt.Sprintf("The %s field must be a Monday's midnight in Europe/Oslo.", field)
	case "month":
		return fmt.Sprintf("The %s field must be the first of a month's midnight in Europe/Oslo.", field)
	default:
		return fmt.Sprintf("The %s field must be a midnight in Europe/Oslo.", field)
	}
}

// addCivilDays is t moved n days on Oslo's calendar at the same wall clock,
// so a window across a DST change is measured in days, not in hours.
func addCivilDays(t time.Time, n int, loc *time.Location) time.Time {
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day()+n, l.Hour(), l.Minute(), l.Second(), l.Nanosecond(), loc)
}
