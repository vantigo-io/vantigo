package energy

import (
	"context"
	"fmt"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/energy/store"
)

// This file is what the tariffs (energy consumption billing design D3) share
// with the metering points and the supply periods: the words stored in the
// new columns, a period's terms, the assignment check and the names a
// response carries beside a tariff's id. tariffs.go is the tariffs' own area.

// A tariff's kind and status, stored verbatim (ck_tariffs_kind,
// ck_tariffs_status).
const (
	tariffKindPower = "power"
	tariffKindGrid  = "grid"

	tariffStatusActive   = "active"
	tariffStatusArchived = "archived"
)

// A supply period's billing cadence (design D4, ck_supply_periods_cadence).
const (
	cadenceMonthly   = "monthly"
	cadenceQuarterly = "quarterly"
	cadenceYearly    = "yearly"
)

// A metering point's electricity-tax class (design D4,
// ck_metering_points_electricity_tax_class).
const (
	taxClassOrdinary = "ordinary"
	taxClassReduced  = "reduced"
	taxClassExempt   = "exempt"
)

// supplyTerms is what a supply period is billed on: its power tariff (nil for
// none, which delivery C's settlement blocks as no_tariff) and its cadence.
// The import writes defaultSupplyTerms: the file carries no contract.
type supplyTerms struct {
	TariffID *int32
	Cadence  string
}

var defaultSupplyTerms = supplyTerms{Cadence: cadenceMonthly}

// parseCadence reads a request's cadence: absent is monthly, anything but the
// three is refused (ok false).
func parseCadence(v *string) (string, bool) {
	if v == nil {
		return cadenceMonthly, true
	}
	switch c := strings.TrimSpace(*v); c {
	case cadenceMonthly, cadenceQuarterly, cadenceYearly:
		return c, true
	}
	return "", false
}

const cadenceMessage = "Cadence must be monthly, quarterly or yearly."

// parseTaxClass reads a request's electricity-tax class: absent is ordinary,
// anything but the three is refused (ok false).
func parseTaxClass(v *string) (string, bool) {
	if v == nil {
		return taxClassOrdinary, true
	}
	switch c := strings.TrimSpace(*v); c {
	case taxClassOrdinary, taxClassReduced, taxClassExempt:
		return c, true
	}
	return "", false
}

const taxClassMessage = "Electricity tax class must be ordinary, reduced or exempt."

// checkTariffAssignable is the assignment rule (energy tariffs plan, reading
// 5) for a request's tariff id: nil (none) always passes; otherwise the
// tariff must exist, be of wantKind and be active — except that the tariff
// the row already holds (current) passes even archived, so a write that
// repeats it is not refused for a status it cannot change. It answers the
// field's message, "" when the id may be written.
func checkTariffAssignable(ctx context.Context, q *store.Queries, id *int32, wantKind string, current *int32) (string, error) {
	if id == nil {
		return "", nil
	}
	rows, err := q.ListTariffNamesByIDs(ctx, []int32{*id})
	if err != nil {
		return "", fmt.Errorf("energy: read tariff %d: %w", *id, err)
	}
	if len(rows) == 0 {
		return fmt.Sprintf("Tariff %d does not exist.", *id), nil
	}
	t := rows[0]
	if t.Kind != wantKind {
		return fmt.Sprintf("Tariff %d is a %s tariff, not a %s tariff.", t.ID, t.Kind, wantKind), nil
	}
	if t.Status != tariffStatusActive && (current == nil || *current != t.ID) {
		return fmt.Sprintf("Tariff %d is archived.", t.ID), nil
	}
	return "", nil
}

// tariffNames answers the names of the tariffs ids names (nils and repeats
// allowed), so a page of supply periods or metering points carries each
// tariff's name with one query.
func tariffNames(ctx context.Context, q *store.Queries, ids ...*int32) (map[int32]string, error) {
	seen := map[int32]bool{}
	want := make([]int32, 0, len(ids))
	for _, id := range ids {
		if id != nil && !seen[*id] {
			seen[*id] = true
			want = append(want, *id)
		}
	}
	names := make(map[int32]string, len(want))
	if len(want) == 0 {
		return names, nil
	}
	rows, err := q.ListTariffNamesByIDs(ctx, want)
	if err != nil {
		return nil, fmt.Errorf("energy: read tariff names: %w", err)
	}
	for _, r := range rows {
		names[r.ID] = r.Name
	}
	return names, nil
}

// tariffNameOf is id's name in names, nil for no tariff.
func tariffNameOf(names map[int32]string, id *int32) *string {
	if id == nil {
		return nil
	}
	if n, ok := names[*id]; ok {
		return &n
	}
	return nil
}
