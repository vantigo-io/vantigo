package energy

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/energy/store"
)

// This file is the one write of spot prices (energy consumption billing
// design D2), shared by the file import (spot_prices_import.go) and the feed
// worker (spot_prices_worker.go): the rows an area is about to take are read
// first and each is classified — inserted where the area holds nothing at
// the row's start, replaced where it holds the same interval with other
// figures, unchanged where it holds it with the same, and a conflict where
// it holds an interval of another length touching the row's — then written
// in the caller's transaction. The counts a caller answers are exact because
// the classification is made under the same transaction as the write; the
// exclusion constraint spot_prices_no_overlap is the backstop behind it.

// The two sources a price is written with.
const (
	spotPriceSourceFeed   = "Feed"
	spotPriceSourceImport = "Import"
)

// The civil zone a feed day is read in, whatever the area: the Nordic
// day-ahead market's day is a CET day.
const spotPriceDayZone = "Europe/Oslo"

// spotPriceRow is one price interval as a caller hands it to the write:
// half-open [Start, End), the NOK price per kWh and, when the source gave
// them, the EUR price and the exchange rate.
type spotPriceRow struct {
	PriceArea    string
	Start, End   time.Time
	NokPerKwh    float64
	EurPerKwh    *float64
	ExchangeRate *float64
}

// spotPriceConflict is a row the area cannot take: it touches a stored
// interval that is not the same interval. Index is the row's position in
// the slice the caller gave.
type spotPriceConflict struct {
	Index  int
	Stored store.EnergySpotPrice
}

// spotPriceWritePlan is what the write will do, per row.
type spotPriceWritePlan struct {
	inserts   []spotPriceRow
	replaces  []spotPriceReplace
	unchanged int
	conflicts []spotPriceConflict
}

type spotPriceReplace struct {
	id  int64
	row spotPriceRow
}

// Counts are the plan's figures as a caller answers them.
func (p spotPriceWritePlan) Counts() (inserted, replaced, unchanged int) {
	return len(p.inserts), len(p.replaces), p.unchanged
}

// planSpotPriceWrite classifies rows — all of one area, any order, no two
// with the same start — against what q holds. Rows that overlap each other
// are the caller's to refuse before this; the exclusion constraint refuses
// them at the write otherwise.
func planSpotPriceWrite(ctx context.Context, q *store.Queries, area string, rows []spotPriceRow) (spotPriceWritePlan, error) {
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
	return plan, nil
}

// sameSpotPriceFigures is whether a stored interval already carries r's
// figures, compared as the column stores them (six decimals).
func sameSpotPriceFigures(s store.EnergySpotPrice, r spotPriceRow) bool {
	return floatFromNumeric(s.NokPerKwh) == roundPrice(r.NokPerKwh) &&
		samePricePtr(floatPtrFromNumeric(s.EurPerKwh), r.EurPerKwh) &&
		samePricePtr(floatPtrFromNumeric(s.ExchangeRate), r.ExchangeRate)
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
// writes the inserts and the replaces only.
func applySpotPriceWrite(ctx context.Context, tx pgx.Tx, plan spotPriceWritePlan, source string, now time.Time) error {
	q := store.New(tx)
	for _, r := range plan.inserts {
		nok, eur, exr, err := spotPriceNumerics(r)
		if err != nil {
			return err
		}
		if err := q.InsertSpotPrice(ctx, store.InsertSpotPriceParams{
			PriceArea: r.PriceArea, Start: r.Start, EndAt: r.End,
			NokPerKwh: nok, EurPerKwh: eur, ExchangeRate: exr, Source: source, ReceivedAt: now,
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
