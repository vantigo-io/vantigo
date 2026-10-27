package energy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
)

// This file is what the tariffs (energy consumption billing design D3) share
// with the metering points and the supply periods: the words stored in the
// new columns, a period's terms, the assignment's read and rule and the names a
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

// shareTariff reads the tariff id names FOR SHARE (GetTariffForShare), inside
// the caller's transaction: nil for no id or no such tariff. Every write that
// assigns a tariff takes this first, before its other locks (the supply-period
// lock, the metering point's row), so they are always taken in one order.
func shareTariff(ctx context.Context, q *store.Queries, id *int32) (*store.GetTariffForShareRow, error) {
	if id == nil {
		return nil, nil
	}
	t, err := q.GetTariffForShare(ctx, *id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("energy: read tariff %d: %w", *id, err)
	}
	return &t, nil
}

// tariffRefusal is the assignment rule (energy tariffs plan, reading 5) for a
// request's tariff id, judged against t, the tariff shareTariff read for it
// inside the write's transaction: nil (none) always passes; otherwise the
// tariff must exist, be of wantKind and be active — except that the tariff
// the row already holds (current) passes even archived, so a write that
// repeats it is not refused for a status it cannot change. It answers the
// field's message, "" when the id may be written. Because t is held FOR SHARE
// until the write commits, an archive or a delete in flight was waited for and
// is seen, and neither can start before the write is done.
func tariffRefusal(id *int32, t *store.GetTariffForShareRow, wantKind string, current *int32) string {
	if id == nil {
		return ""
	}
	if t == nil {
		return tariffMissingMessage(*id)
	}
	if t.Kind != wantKind {
		return fmt.Sprintf("Tariff %d is a %s tariff, not a %s tariff.", t.ID, t.Kind, wantKind)
	}
	if t.Status != tariffStatusActive && (current == nil || *current != t.ID) {
		return fmt.Sprintf("Tariff %d is archived.", t.ID)
	}
	return ""
}

func tariffMissingMessage(id int32) string {
	return fmt.Sprintf("Tariff %d does not exist.", id)
}

// The foreign keys an assignment writes. Under FOR SHARE a tariff cannot be
// deleted between the judgement and the write, so a violation of these is a
// backstop only, answered as the field's "does not exist" (tariffFKRefusal).
const (
	supplyPeriodTariffFK = "supply_periods_tariff_id_fkey"
	meteringPointGridFK  = "metering_points_grid_tariff_id_fkey"
)

// tariffFKRefusal answers the field refusal for err when it is a violation of
// constraint by id, nil otherwise.
func tariffFKRefusal(err error, constraint, field string, id *int32) map[string][]string {
	if id == nil || !db.IsForeignKeyViolation(err, constraint) {
		return nil
	}
	return map[string][]string{field: {tariffMissingMessage(*id)}}
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
