package energy

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/energy/store"
)

// This file is the one write of spot prices (energy consumption billing
// design D2), shared by the file import (spot_prices_import.go), the feed
// worker (spot_prices_worker.go) and the spot-price service's sync
// (spot-price service design S6): the rows an area is about to take are read
// first and each is classified — inserted where the area holds nothing at
// the row's start, replaced where it holds the same interval with other
// figures, unchanged where it holds it with the same, and a conflict where
// it holds an interval of another length touching the row's — then written
// in the caller's transaction. The counts a caller answers are exact because
// every writer of an area — the import and the worker alike — takes the
// area's advisory lock (lockSpotPriceAreas) in that transaction before it
// classifies, so writers of one area are serialised and none classifies
// against rows another is about to commit; the exclusion constraint
// spot_prices_no_overlap stays the backstop behind it.

// The three sources a price is written with.
const (
	spotPriceSourceFeed    = "Feed"
	spotPriceSourceImport  = "Import"
	spotPriceSourceService = "Service"
)

// The civil zone a feed day is read in, whatever the area: the Nordic
// day-ahead market's day is a CET day.
const spotPriceDayZone = "Europe/Oslo"

// spotPriceRow is one price interval as a caller hands it to the write:
// half-open [Start, End), the NOK price per kWh and, when the source gave
// them, the EUR price and the exchange rate. State is final or preliminary
// ("" is final: the feed and a file publish a day once); Upstream,
// RateSource and UpstreamVersion are the spot-price service's provenance of
// a Service row (spot-price service design S2), nil for the others.
type spotPriceRow struct {
	PriceArea       string
	Start, End      time.Time
	NokPerKwh       float64
	EurPerKwh       *float64
	ExchangeRate    *float64
	State           string
	Upstream        *string
	RateSource      *string
	UpstreamVersion *int32
}

// The two states a price is in (spot-price service design S2).
const (
	spotPriceStateFinal       = "final"
	spotPriceStatePreliminary = "preliminary"
)

// judgeSpotPriceDay is whether rows, ascending by start, are one whole
// civil day in zone (delivery A's plan reading 5, spot-price service design
// S6): contiguous, the first starting at the day's local midnight and the
// last ending at the next — 24 hours, or 23 and 25 on the DST nights, at any
// resolution. It answers "" for a whole day and the sentence of the first
// thing wrong otherwise; day is the civil date at any instant of its UTC
// calendar day. Every source judges a day by it before writing anything of
// the day.
func judgeSpotPriceDay(rows []spotPriceRow, day time.Time, zone *time.Location) string {
	if len(rows) == 0 {
		return spotFeedEmpty
	}
	midnight := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, zone)
	next := time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, zone)
	for i, r := range rows {
		switch {
		case i == 0 && !r.Start.Equal(midnight):
			return spotFeedLateStart
		case i > 0 && !r.Start.Equal(rows[i-1].End):
			return spotFeedNotContiguous
		}
	}
	if !rows[len(rows)-1].End.Equal(next) {
		return spotFeedEarlyEnd
	}
	return ""
}

// state is r's state as the column stores it.
func (r spotPriceRow) state() string {
	if r.State == "" {
		return spotPriceStateFinal
	}
	return r.State
}

// spotPriceConflict is a row the area cannot take: it touches a stored
// interval that is not the same interval. Index is the row's position in
// the slice the caller gave.
type spotPriceConflict struct {
	Index  int
	Stored store.EnergySpotPrice
}

// spotPriceWritePlan is what the write will do, per row. deletes are the
// stored rows a fetched day at another resolution replaces (replaceDays);
// they are removed before the inserts.
type spotPriceWritePlan struct {
	inserts   []spotPriceRow
	replaces  []spotPriceReplace
	unchanged int
	conflicts []spotPriceConflict
	deletes   []int64
}

type spotPriceReplace struct {
	id  int64
	row spotPriceRow
}

// Counts are the plan's figures as a caller answers them.
func (p spotPriceWritePlan) Counts() (inserted, replaced, unchanged int) {
	return len(p.inserts), len(p.replaces), p.unchanged
}

// Deleted is how many stored rows the plan removes to replace their days at
// another resolution.
func (p spotPriceWritePlan) Deleted() int { return len(p.deletes) }

// lockSpotPriceAreas takes the transaction-scoped advisory lock of each of
// areas on tx, in sorted order so two writers holding several areas never
// wait on each other in a circle. It is held until tx ends.
func lockSpotPriceAreas(ctx context.Context, tx pgx.Tx, areas []string) error {
	for _, area := range slices.Sorted(slices.Values(areas)) {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('energy.spot_prices:' || $1::text, 0))`, area); err != nil {
			return fmt.Errorf("energy: lock the spot prices of %s: %w", area, err)
		}
	}
	return nil
}

// planSpotPriceWrite classifies rows — all of one area, any order, no two
// with the same start — against what q holds. Rows that overlap each other
// are the caller's to refuse before this; the exclusion constraint refuses
// them at the write otherwise.
func planSpotPriceWrite(ctx context.Context, q *store.Queries, area string, rows []spotPriceRow) (spotPriceWritePlan, error) {
	return planSpotPriceWriteIn(ctx, q, area, rows, nil)
}

// planSpotPriceDaysWrite is planSpotPriceWrite for the spot-price service
// (spot-price service design S6), whose rows are whole civil days in zone:
// a day whose rows conflict with stored rows of another resolution, every
// one of them fetched (Feed or Service), is replaced — the stored rows of
// that day are deleted and the day's rows inserted. A conflict with any
// imported row stays a conflict: an operator's file is not overwritten.
// The conflicts a caller sees are of the days that could not be replaced.
func planSpotPriceDaysWrite(ctx context.Context, q *store.Queries, area string, rows []spotPriceRow, zone *time.Location) (spotPriceWritePlan, error) {
	return planSpotPriceWriteIn(ctx, q, area, rows, zone)
}

func planSpotPriceWriteIn(ctx context.Context, q *store.Queries, area string, rows []spotPriceRow, dayZone *time.Location) (spotPriceWritePlan, error) {
	var plan spotPriceWritePlan
	if len(rows) == 0 {
		return plan, nil
	}
	from, to := rows[0].Start, rows[0].End
	for _, r := range rows {
		if r.Start.Before(from) {
			from = r.Start
		}
		if r.End.After(to) {
			to = r.End
		}
	}
	stored, err := q.ListSpotPricesOverlapping(ctx, store.ListSpotPricesOverlappingParams{PriceArea: area, FromTs: from, ToTs: to})
	if err != nil {
		return plan, fmt.Errorf("energy: read the stored spot prices of %s: %w", area, err)
	}
	// stored is ordered by start; each row finds what touches it by a
	// binary search over the starts, then walks the few that overlap.
	for i, r := range rows {
		first := sort.Search(len(stored), func(k int) bool { return stored[k].End.After(r.Start) })
		var same *store.EnergySpotPrice
		conflict := false
		for k := first; k < len(stored) && stored[k].Start.Before(r.End); k++ {
			s := stored[k]
			if s.Start.Equal(r.Start) && s.End.Equal(r.End) {
				same = &stored[k]
				continue
			}
			conflict = true
			plan.conflicts = append(plan.conflicts, spotPriceConflict{Index: i, Stored: s})
			break
		}
		switch {
		case conflict:
		case same == nil:
			plan.inserts = append(plan.inserts, r)
		case sameSpotPriceFigures(*same, r):
			plan.unchanged++
		default:
			plan.replaces = append(plan.replaces, spotPriceReplace{id: same.ID, row: r})
		}
	}
	if dayZone != nil && len(plan.conflicts) > 0 {
		return replaceSpotPriceDays(plan, rows, stored, dayZone), nil
	}
	return plan, nil
}

// replaceSpotPriceDays turns a plan's conflicts into day replacements where
// it may: per civil day in zone with a conflict, when every stored row
// touching the day is fetched, those rows are deleted and every row of the
// day is inserted (a row counted as replaced or unchanged against a stored
// row of the day is inserted again, since that row goes too). A day with an
// imported row keeps its conflicts and the rest of the plan stands.
func replaceSpotPriceDays(plan spotPriceWritePlan, rows []spotPriceRow, stored []store.EnergySpotPrice, zone *time.Location) spotPriceWritePlan {
	dayOf := func(t time.Time) string { return t.In(zone).Format(time.DateOnly) }
	conflicted := map[string]bool{}
	for _, c := range plan.conflicts {
		conflicted[dayOf(rows[c.Index].Start)] = true
	}
	// A day is replaceable when none of the stored rows touching it is an
	// import. A stored row touches a day when it starts in it or runs into
	// it; the service's days are whole, so a start's day is enough for the
	// rows, and a stored row is judged by both its first and last instant.
	imported := map[string]bool{}
	for _, s := range stored {
		if s.Source != spotPriceSourceImport {
			continue
		}
		imported[dayOf(s.Start)] = true
		imported[dayOf(s.End.Add(-time.Nanosecond))] = true
	}
	replace := map[string]bool{}
	for day := range conflicted {
		if !imported[day] {
			replace[day] = true
		}
	}
	if len(replace) == 0 {
		return plan
	}
	var next spotPriceWritePlan
	for _, s := range stored {
		if replace[dayOf(s.Start)] || replace[dayOf(s.End.Add(-time.Nanosecond))] {
			next.deletes = append(next.deletes, s.ID)
		}
	}
	deleted := make(map[int64]bool, len(next.deletes))
	for _, id := range next.deletes {
		deleted[id] = true
	}
	for _, r := range plan.inserts {
		if !replace[dayOf(r.Start)] {
			next.inserts = append(next.inserts, r)
		}
	}
	for _, rp := range plan.replaces {
		if !deleted[rp.id] {
			next.replaces = append(next.replaces, rp)
		}
	}
	for i, r := range rows {
		if replace[dayOf(r.Start)] {
			next.inserts = append(next.inserts, rows[i])
		}
	}
	for _, c := range plan.conflicts {
		if !replace[dayOf(rows[c.Index].Start)] {
			next.conflicts = append(next.conflicts, c)
		}
	}
	// unchanged rows of a replaced day are inserted again above; the count
	// keeps only those of the days that stand.
	next.unchanged = plan.unchanged
	for i, r := range rows {
		if !replace[dayOf(r.Start)] {
			continue
		}
		for k := range stored {
			s := stored[k]
			if s.Start.Equal(r.Start) && s.End.Equal(r.End) && sameSpotPriceFigures(s, rows[i]) {
				next.unchanged--
				break
			}
		}
	}
	return next
}

// sameSpotPriceFigures is whether a stored interval already carries r's
// figures, compared as the column stores them (six decimals).
func sameSpotPriceFigures(s store.EnergySpotPrice, r spotPriceRow) bool {
	return floatFromNumeric(s.NokPerKwh) == roundPrice(r.NokPerKwh) &&
		samePricePtr(floatPtrFromNumeric(s.EurPerKwh), r.EurPerKwh) &&
		samePricePtr(floatPtrFromNumeric(s.ExchangeRate), r.ExchangeRate) &&
		s.State == r.state() &&
		sameStringPtr(s.Upstream, r.Upstream) &&
		sameStringPtr(s.RateSource, r.RateSource) &&
		sameInt32Ptr(s.UpstreamVersion, r.UpstreamVersion)
}

func sameStringPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func sameInt32Ptr(a, b *int32) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func samePricePtr(a, b *float64) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	}
	return *a == roundPrice(*b)
}

// roundPrice is v as a numeric(12,6) column stores it: six decimals, half
// away from zero, on v's shortest decimal text, as roundKwh does for three.
func roundPrice(v float64) float64 {
	return roundDecimals(v, 6)
}

// applySpotPriceWrite writes plan on tx with source and now as every row's
// received_at. A conflict in the plan is the caller's to have refused: this
// writes the deletes, the inserts and the replaces only, the deletes first
// so a day replaced at another resolution never overlaps itself.
func applySpotPriceWrite(ctx context.Context, tx pgx.Tx, plan spotPriceWritePlan, source string, now time.Time) error {
	q := store.New(tx)
	if len(plan.deletes) > 0 {
		if err := q.DeleteSpotPricesByIDs(ctx, plan.deletes); err != nil {
			return fmt.Errorf("energy: delete the spot prices a day replaces: %w", err)
		}
	}
	for _, r := range plan.inserts {
		nok, eur, exr, err := spotPriceNumerics(r)
		if err != nil {
			return err
		}
		if err := q.InsertSpotPrice(ctx, store.InsertSpotPriceParams{
			PriceArea: r.PriceArea, Start: r.Start, EndAt: r.End,
			NokPerKwh: nok, EurPerKwh: eur, ExchangeRate: exr, Source: source, ReceivedAt: now,
			State: r.state(), Upstream: r.Upstream, RateSource: r.RateSource, UpstreamVersion: r.UpstreamVersion,
		}); err != nil {
			return fmt.Errorf("energy: insert the spot price of %s at %s: %w", r.PriceArea, r.Start.Format(time.RFC3339), err)
		}
	}
	for _, rep := range plan.replaces {
		nok, eur, exr, err := spotPriceNumerics(rep.row)
		if err != nil {
			return err
		}
		if err := q.UpdateSpotPrice(ctx, store.UpdateSpotPriceParams{
			NokPerKwh: nok, EurPerKwh: eur, ExchangeRate: exr, Source: source, ReceivedAt: now, ID: rep.id,
			State: rep.row.state(), Upstream: rep.row.Upstream, RateSource: rep.row.RateSource, UpstreamVersion: rep.row.UpstreamVersion,
		}); err != nil {
			return fmt.Errorf("energy: replace the spot price %d: %w", rep.id, err)
		}
	}
	return nil
}

func spotPriceNumerics(r spotPriceRow) (nok, eur, exr pgtype.Numeric, err error) {
	if nok, err = numericFromFloat(r.NokPerKwh); err != nil {
		return
	}
	if eur, err = numericFromFloatPtr(r.EurPerKwh); err != nil {
		return
	}
	exr, err = numericFromFloatPtr(r.ExchangeRate)
	return
}
