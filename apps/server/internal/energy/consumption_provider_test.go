package energy_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/energy"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The consumption provider's tests (energy consumption billing design D1,
// energy settlements plan readings 7–9). The fixture is one metering point in
// NO1 with a fixed-price power tariff (1.25 NOK/kWh, no grid tariff) and a
// monthly supply period from July 2026, every hour of July carrying 1 kWh: the
// July settlement is 744.000 kWh and 930.00 NOK. The harness clock stands at
// 2026-09-12, so July and August are both closed; a Through of August's
// first midnight settles July alone.

var (
	stlOslo      = mustStlLocation("Europe/Oslo")
	stlJulyStart = time.Date(2026, time.July, 1, 0, 0, 0, 0, stlOslo)
	stlAugStart  = time.Date(2026, time.August, 1, 0, 0, 0, 0, stlOslo)
)

func mustStlLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// stlFixedTariffBody is a power tariff of one fixed-energy component.
func stlFixedTariffBody(name string) json.RawMessage {
	return json.RawMessage(`{"kind":"power","name":"` + name + `","versions":[{"validFrom":"2026-01-01","components":[` +
		`{"kind":"fixed_energy","name":"Fastpris","rate":1.25}]}]}`)
}

// stlFixture is one point, one supply period and the provider over them.
type stlFixture struct {
	h        *modtest.Harness
	c        *modtest.Client
	point    int32
	tariff   int32
	supply   int32
	customer int32
	provider contracts.ConsumptionProvider
}

// newStlFixture builds the fixture; withTariff false leaves the supply
// period without a power tariff (no_tariff). July's intervals are written.
func newStlFixture(t *testing.T, withTariff bool) stlFixture {
	t.Helper()
	h := newHarness(t)
	c := h.SignIn(t, tariffPermissions...)
	f := stlFixture{h: h, c: c, customer: 1001}
	f.point = createMeteringPoint(t, c).Id
	var tariffID any
	if withTariff {
		f.tariff = createTariff(t, c, stlFixedTariffBody(fmt.Sprintf("Fastpris %d", f.point))).Id
		tariffID = f.tariff
	}
	f.supply = stlInsertSupplyPeriod(t, h, f.point, f.customer, stlJulyStart, tariffID)
	stlWriteHours(t, h, f.point, stlJulyStart, stlAugStart, h.Now())
	f.provider = energy.Module().Consumption(h.Deps())
	return f
}

// stlInsertSupplyPeriod inserts an open monthly supply period.
func stlInsertSupplyPeriod(t *testing.T, h *modtest.Harness, point, customer int32, start time.Time, tariffID any) int32 {
	t.Helper()
	return modtest.One[int32](t, h, `INSERT INTO energy.supply_periods (metering_point_id, customer_id, start, status, tariff_id, cadence)
		VALUES ($1, $2, $3, 'Active', $4::integer, 'monthly') RETURNING id`, point, customer, start, tariffID)
}

// stlWriteHours writes 1 kWh for every hour of [from, to), measured, received at.
func stlWriteHours(t *testing.T, h *modtest.Harness, point int32, from, to, received time.Time) {
	t.Helper()
	for m := time.Date(from.UTC().Year(), from.UTC().Month(), 1, 0, 0, 0, 0, time.UTC); m.Before(to); m = m.AddDate(0, 1, 0) {
		h.Exec(t, `SELECT energy.ensure_consumption_partition($1::date)`, m.Format(time.DateOnly))
	}
	h.Exec(t, `INSERT INTO energy.consumption_intervals (metering_point_id, start, "end", quantity_kwh, quality, source, received_at)
		SELECT $1, g, g + interval '1 hour', 1, 'Measured', 'Elhub', $2::timestamptz
		FROM generate_series($3::timestamptz, $4::timestamptz - interval '1 hour', interval '1 hour') g`,
		point, received, from, to)
}

// stlChangeHour sets the hour starting at start to kwh, received at.
func stlChangeHour(t *testing.T, h *modtest.Harness, point int32, start time.Time, kwh string, received time.Time) {
	t.Helper()
	h.Exec(t, `UPDATE energy.consumption_intervals SET quantity_kwh = $3::numeric, received_at = $4
		WHERE metering_point_id = $1 AND start = $2 AND is_current`, point, start, kwh, received)
}

func (f stlFixture) settle(t *testing.T, through time.Time) contracts.SettlementsPage {
	t.Helper()
	page, err := f.provider.Settle(context.Background(), contracts.SettleRequest{CustomerIDs: []int32{f.customer}, Through: through})
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	return page
}

// only is the page's one settlement.
func only(t *testing.T, page contracts.SettlementsPage) contracts.Settlement {
	t.Helper()
	if len(page.Settlements) != 1 || page.More {
		t.Fatalf("answered %d settlements (more %v), want exactly one: %+v", len(page.Settlements), page.More, page.Settlements)
	}
	return page.Settlements[0]
}

// stlRower is a pool or a transaction.
type stlRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// stlInsertSettlement writes a settlement row by hand: a regular one of
// [start, end) for the fixture's supply period, in state, blocked when
// blocked is not "", stamped by invoice 6001 / number 9001 unless a draft.
// 930.00 NOK, 744 kWh, no lines: for the tests whose point is judged before
// any recomputation.
func (f stlFixture) insertSettlement(t *testing.T, q stlRower, start, end time.Time, state, blocked string) int64 {
	t.Helper()
	var invoiceID, number, invoicedAt, invoiceDate, releasedAt, blockedValue any
	if state != contracts.SettlementDraft {
		invoiceID, number, invoicedAt, invoiceDate = int64(6001), int64(9001), f.h.Now(), "2026-09-01"
	}
	if state == contracts.SettlementReleased {
		releasedAt = f.h.Now()
	}
	if blocked != "" {
		blockedValue = blocked
	}
	var id int64
	if err := q.QueryRow(context.Background(), `
		INSERT INTO energy.settlements (supply_period_id, metering_point_id, customer_id, kind, period_start, period_end,
		    state, kwh, interval_count, expected_interval_count, blocked, amount, contract_name,
		    computed_at, touched_at, created_at, updated_at,
		    invoiced_invoice_id, invoiced_number, invoiced_at, invoice_date, released_at)
		VALUES ($1, $2, $3, 'regular', $4, $5, $6, 744, 744, 744, $7::varchar, 930, 'Fastpris',
		    $8, $8, $8, $8, $9::bigint, $10::bigint, $11::timestamptz, $12::date, $13::timestamptz)
		RETURNING id`,
		f.supply, f.point, f.customer, start, end, state, blockedValue, f.h.Now(),
		invoiceID, number, invoicedAt, invoiceDate, releasedAt).Scan(&id); err != nil {
		t.Fatalf("insert a settlement: %v", err)
	}
	return id
}

// TestConsumptionProvider_RefusesWhatValidateRefuses: each method refuses a
// request its Validate refuses, before it touches the database (the
// provider here has no pool at all).
func TestConsumptionProvider_RefusesWhatValidateRefuses(t *testing.T) {
	t.Parallel()
	p := energy.Module().Consumption(module.Deps{Clock: func() time.Time { return modtest.Start }})
	ctx := context.Background()
	through := modtest.Start
	if _, err := p.DueSupplies(ctx, contracts.DueSuppliesRequest{Through: through}); err == nil {
		t.Error("DueSupplies with no customer answered no error")
	}
	if _, err := p.DueSupplies(ctx, contracts.DueSuppliesRequest{CustomerIDs: []int32{1, 1}, Through: through}); err == nil {
		t.Error("DueSupplies naming a customer twice answered no error")
	}
	if _, err := p.Settle(ctx, contracts.SettleRequest{CustomerIDs: []int32{1}, SupplyPeriodIDs: []int32{1}, Through: through}); err == nil {
		t.Error("Settle naming customers and supply periods answered no error")
	}
	if _, err := p.Settle(ctx, contracts.SettleRequest{SupplyPeriodIDs: []int32{1}}); err == nil {
		t.Error("Settle with no through answered no error")
	}
	if _, err := p.Settlements(ctx, nil); err == nil {
		t.Error("Settlements of no id answered no error")
	}
	if _, err := p.Settlements(ctx, []int64{3, 3}); err == nil {
		t.Error("Settlements naming an id twice answered no error")
	}
}

// TestConsumptionProvider_SettleIsIdempotent: Settle writes July's draft at
// revision 1 and a second Settle answers the same row, the same revision,
// the same figures — and no second row.
func TestConsumptionProvider_SettleIsIdempotent(t *testing.T) {
	t.Parallel()
	f := newStlFixture(t, true)

	first := only(t, f.settle(t, stlAugStart))
	if first.ID == 0 || first.Revision != 1 || first.State != contracts.SettlementDraft || first.Kind != contracts.SettlementRegular {
		t.Fatalf("first Settle = id %d revision %d state %s kind %s, want a stored draft regular at revision 1",
			first.ID, first.Revision, first.State, first.Kind)
	}
	if first.KWh != "744.000" || first.Amount != "930.00" || first.Currency != "NOK" || first.Blocked != "" {
		t.Errorf("July = %s kWh, %s %s, blocked %q; want 744.000 kWh, 930.00 NOK, not blocked",
			first.KWh, first.Amount, first.Currency, first.Blocked)
	}
	if !first.PeriodStart.Equal(stlJulyStart) || !first.PeriodEnd.Equal(stlAugStart) || first.SupplyPeriodID != f.supply ||
		first.CustomerID != f.customer || first.MeteringPointID != f.point {
		t.Errorf("July = %+v, want July of supply period %d, customer %d, point %d", first, f.supply, f.customer, f.point)
	}

	f.h.Advance(time.Hour)
	second := only(t, f.settle(t, stlAugStart))
	if second.ID != first.ID || second.Revision != 1 || second.Amount != first.Amount {
		t.Errorf("second Settle = id %d revision %d amount %s, want id %d revision 1 amount %s",
			second.ID, second.Revision, second.Amount, first.ID, first.Amount)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM energy.settlements WHERE supply_period_id = $1`, f.supply); n != 1 {
		t.Errorf("settlements stored = %d, want 1", n)
	}
	if touched := modtest.One[time.Time](t, f.h, `SELECT touched_at FROM energy.settlements WHERE id = $1`, first.ID); !touched.Equal(f.h.Now()) {
		t.Errorf("touched_at = %v, want the second Settle's now %v", touched, f.h.Now())
	}
}

// TestConsumptionProvider_AChangedIntervalBumpsTheRevision: an interval
// changed under a draft makes the next Settle rewrite it at revision 2.
func TestConsumptionProvider_AChangedIntervalBumpsTheRevision(t *testing.T) {
	t.Parallel()
	f := newStlFixture(t, true)
	first := only(t, f.settle(t, stlAugStart))

	stlChangeHour(t, f.h, f.point, stlJulyStart.Add(5*time.Hour), "2", f.h.Now())
	second := only(t, f.settle(t, stlAugStart))
	if second.ID != first.ID || second.Revision != 2 || second.KWh != "745.000" || second.Amount != "931.25" {
		t.Errorf("after the change = id %d revision %d %s kWh %s NOK, want id %d revision 2 745.000 kWh 931.25 NOK",
			second.ID, second.Revision, second.KWh, second.Amount, first.ID)
	}
}

// TestConsumptionProvider_ABlockedSettlementIsAnswered: a supply period with
// no power tariff is settled blocked no_tariff, with no lines and a zero
// amount, and DueSupplies names the blocker without writing anything.
func TestConsumptionProvider_ABlockedSettlementIsAnswered(t *testing.T) {
	t.Parallel()
	f := newStlFixture(t, false)
	ctx := context.Background()

	due, err := f.provider.DueSupplies(ctx, contracts.DueSuppliesRequest{CustomerIDs: []int32{f.customer}, Through: stlAugStart})
	if err != nil {
		t.Fatalf("DueSupplies: %v", err)
	}
	if len(due.Supplies) != 1 || due.More {
		t.Fatalf("DueSupplies = %+v, want one supply", due)
	}
	if got := due.Supplies[0]; got.SupplyPeriodID != f.supply || got.DuePeriods != 1 || got.Blocked != contracts.SettlementNoTariff ||
		got.BilledThrough != nil || got.Cadence != contracts.CadenceMonthly || got.Address != "Testgata 1, 0001 Oslo" || got.PriceArea != "NO1" {
		t.Errorf("DueSupplies[0] = %+v, want supply period %d, one due period, blocked no_tariff, never billed", got, f.supply)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM energy.settlements`); n != 0 {
		t.Errorf("DueSupplies wrote %d settlements, want none", n)
	}

	got := only(t, f.settle(t, stlAugStart))
	if got.Blocked != contracts.SettlementNoTariff || got.BlockedDetail == "" || len(got.Lines) != 0 || got.Amount != "0.00" || got.ID == 0 {
		t.Errorf("Settle = blocked %q (%q), %d lines, amount %s, id %d; want a stored no_tariff with its sentence, no lines, 0.00",
			got.Blocked, got.BlockedDetail, len(got.Lines), got.Amount, got.ID)
	}
}

// TestConsumptionProvider_DueSuppliesForEveryCustomer: Every answers every
// customer's due supply periods (the billing run's read) — the fixture's
// among them, as the customer-named read answers it — and writes nothing.
func TestConsumptionProvider_DueSuppliesForEveryCustomer(t *testing.T) {
	t.Parallel()
	f := newStlFixture(t, true)
	ctx := context.Background()

	named, err := f.provider.DueSupplies(ctx, contracts.DueSuppliesRequest{CustomerIDs: []int32{f.customer}, Through: stlAugStart})
	if err != nil {
		t.Fatalf("DueSupplies by customer: %v", err)
	}
	every, err := f.provider.DueSupplies(ctx, contracts.DueSuppliesRequest{Every: true, Through: stlAugStart})
	if err != nil {
		t.Fatalf("DueSupplies for every customer: %v", err)
	}
	if len(named.Supplies) != 1 {
		t.Fatalf("DueSupplies by customer = %+v, want one supply", named)
	}
	found := false
	for _, d := range every.Supplies {
		if d.SupplyPeriodID == f.supply {
			found = true
			if d != named.Supplies[0] {
				t.Errorf("every customer's answer for supply %d = %+v, want %+v", f.supply, d, named.Supplies[0])
			}
		}
	}
	if !found {
		t.Errorf("DueSupplies for every customer = %+v, want supply period %d among them", every.Supplies, f.supply)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM energy.settlements`); n != 0 {
		t.Errorf("DueSupplies wrote %d settlements, want none", n)
	}
}

// TestConsumptionProvider_SettlementsByID: a draft is recomputed and
// rewritten when a figure changed, so the revision answered is the one
// stored; a missing id is absent.
func TestConsumptionProvider_SettlementsByID(t *testing.T) {
	t.Parallel()
	f := newStlFixture(t, true)
	draft := only(t, f.settle(t, stlAugStart))
	ctx := context.Background()

	page, err := f.provider.Settlements(ctx, []int64{draft.ID, 999999})
	if err != nil {
		t.Fatalf("Settlements: %v", err)
	}
	if got := only(t, page); got.ID != draft.ID || got.Revision != 1 {
		t.Errorf("Settlements = id %d revision %d, want id %d revision 1 (nothing changed)", got.ID, got.Revision, draft.ID)
	}

	stlChangeHour(t, f.h, f.point, stlJulyStart, "3", f.h.Now())
	page, err = f.provider.Settlements(ctx, []int64{draft.ID})
	if err != nil {
		t.Fatalf("Settlements: %v", err)
	}
	got := only(t, page)
	if got.Revision != 2 || got.KWh != "746.000" {
		t.Errorf("after the change Settlements = revision %d %s kWh, want revision 2 746.000", got.Revision, got.KWh)
	}
	if stored := modtest.One[int32](t, f.h, `SELECT revision FROM energy.settlements WHERE id = $1`, draft.ID); stored != got.Revision {
		t.Errorf("stored revision %d, answered %d: the answer must be what is stored", stored, got.Revision)
	}

	if page, err := f.provider.Settlements(ctx, []int64{999998}); err != nil || len(page.Settlements) != 0 {
		t.Errorf("Settlements(missing) = %+v, %v; want an empty page", page, err)
	}
}

// TestConsumptionProvider_ACorrectionFollowsAnInvoicedPeriodThatChanged:
// July is stamped invoiced (by the holder, committed); an hour of July is
// then received anew with 1 kWh more; the next Settle answers a correction of
// July with the difference, and leaves the invoiced regular as it was.
func TestConsumptionProvider_ACorrectionFollowsAnInvoicedPeriodThatChanged(t *testing.T) {
	t.Parallel()
	f := newStlFixture(t, true)
	regular := only(t, f.settle(t, stlAugStart))
	f.stamp(t, regular)

	if page := f.settle(t, stlAugStart); len(page.Settlements) != 0 {
		t.Fatalf("Settle with nothing changed = %+v, want nothing (July is invoiced, no correction)", page.Settlements)
	}

	f.h.Advance(24 * time.Hour)
	stlChangeHour(t, f.h, f.point, stlJulyStart.Add(10*time.Hour), "2", f.h.Now())
	correction := only(t, f.settle(t, stlAugStart))
	if correction.Kind != contracts.SettlementCorrection || correction.CorrectsID == nil || *correction.CorrectsID != regular.ID {
		t.Fatalf("Settle = kind %s corrects %v, want a correction of %d", correction.Kind, correction.CorrectsID, regular.ID)
	}
	if correction.KWh != "1.000" || correction.Amount != "1.25" || correction.State != contracts.SettlementDraft || correction.ID == 0 {
		t.Errorf("correction = %s kWh %s NOK state %s id %d, want a stored draft of 1.000 kWh and 1.25 NOK",
			correction.KWh, correction.Amount, correction.State, correction.ID)
	}
	if state := modtest.One[string](t, f.h, `SELECT state FROM energy.settlements WHERE id = $1`, regular.ID); state != contracts.SettlementInvoiced {
		t.Errorf("the regular is %s, want invoiced still", state)
	}

	// Put the hour back: the correction has nothing left to correct and goes.
	f.h.Advance(time.Hour)
	stlChangeHour(t, f.h, f.point, stlJulyStart.Add(10*time.Hour), "1", f.h.Now())
	if page := f.settle(t, stlAugStart); len(page.Settlements) != 0 {
		t.Errorf("Settle after the hour was put back = %+v, want nothing", page.Settlements)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM energy.settlements WHERE kind = 'correction'`); n != 0 {
		t.Errorf("corrections stored = %d, want the draft deleted", n)
	}
}

// stamp marks s invoiced through the module's own holder in a committed
// transaction, as an invoice's issue would.
func (f stlFixture) stamp(t *testing.T, s contracts.Settlement) {
	t.Helper()
	holder, _ := newStlHolder(t)
	ctx := context.Background()
	tx, err := f.h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := holder.MarkInvoiced(ctx, tx, stlInvoiceRef(), []contracts.WorkSource{stlSourceOf(s)}); err != nil {
		t.Fatalf("MarkInvoiced: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestConsumptionProvider_Housekeeping: Settle deletes a draft of its supply
// period whose period is no longer due (the supply period cancelled), and
// every draft nobody has touched for thirty days.
func TestConsumptionProvider_Housekeeping(t *testing.T) {
	t.Parallel()
	f := newStlFixture(t, false)
	ctx := context.Background()

	old := only(t, f.settle(t, stlAugStart))
	f.h.Exec(t, `UPDATE energy.settlements SET touched_at = $2 WHERE id = $1`, old.ID, f.h.Now().Add(-31*24*time.Hour))
	other := stlInsertSupplyPeriod(t, f.h, createMeteringPoint(t, f.c).Id, 1002, stlJulyStart, nil)
	if _, err := f.provider.Settle(ctx, contracts.SettleRequest{SupplyPeriodIDs: []int32{other}, Through: stlAugStart}); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM energy.settlements WHERE id = $1`, old.ID); n != 0 {
		t.Errorf("a draft untouched for 31 days survived Settle")
	}
	if n := f.h.Count(t, `SELECT count(*) FROM energy.settlements WHERE supply_period_id = $1`, other); n != 1 {
		t.Errorf("the other supply period's drafts = %d, want its July", n)
	}

	f.h.Exec(t, `UPDATE energy.supply_periods SET status = 'Cancelled' WHERE id = $1`, other)
	page, err := f.provider.Settle(ctx, contracts.SettleRequest{SupplyPeriodIDs: []int32{other}, Through: stlAugStart})
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if len(page.Settlements) != 0 {
		t.Errorf("Settle of a cancelled supply period = %+v, want nothing", page.Settlements)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM energy.settlements WHERE supply_period_id = $1`, other); n != 0 {
		t.Errorf("the cancelled supply period kept %d drafts, want none", n)
	}
}

// TestConsumptionProvider_AnswersAtMostMaxBillableRows: sixteen supply
// periods settled monthly since 2000 (blocked no_tariff, the cheapest
// computation) are more than MaxBillableRows settlements: the answer stops
// at the cap and says More.
func TestConsumptionProvider_AnswersAtMostMaxBillableRows(t *testing.T) {
	if testing.Short() {
		t.Skip("settles over five thousand periods")
	}
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	start := time.Date(2000, time.January, 1, 0, 0, 0, 0, stlOslo)
	var ids []int32
	for range 16 {
		ids = append(ids, stlInsertSupplyPeriod(t, h, createMeteringPoint(t, c).Id, 1001, start, nil))
	}
	p := energy.Module().Consumption(h.Deps())
	page, err := p.Settle(context.Background(), contracts.SettleRequest{SupplyPeriodIDs: ids, Through: stlAugStart})
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if len(page.Settlements) != contracts.MaxBillableRows || !page.More {
		t.Fatalf("Settle answered %d settlements, more %v; want %d and more", len(page.Settlements), page.More, contracts.MaxBillableRows)
	}
	for i := 1; i < len(page.Settlements); i++ {
		a, b := page.Settlements[i-1], page.Settlements[i]
		if a.SupplyPeriodID > b.SupplyPeriodID || (a.SupplyPeriodID == b.SupplyPeriodID && !a.PeriodStart.Before(b.PeriodStart)) {
			t.Fatalf("settlement %d (%d, %v) follows (%d, %v): want supply period, then period order",
				i, b.SupplyPeriodID, b.PeriodStart, a.SupplyPeriodID, a.PeriodStart)
		}
	}
	if !strings.HasPrefix(page.Settlements[0].PeriodStart.In(stlOslo).Format(time.DateOnly), "2000-01-01") {
		t.Errorf("first period starts %v, want 2000-01-01", page.Settlements[0].PeriodStart)
	}
}

// TestConsumptionProvider_ADraftFollowsItsSupplyPeriod: a draft is judged
// over its supply period as it stands, never over the period it was stored
// with. The supply period's end moved to 15 July: Settlements answers July
// as 1–15 July, rewritten at revision 2 with the new end stored. The supply
// period cancelled: July is no longer a period, and Settlements answers it
// absent and deletes it.
func TestConsumptionProvider_ADraftFollowsItsSupplyPeriod(t *testing.T) {
	t.Parallel()
	f := newStlFixture(t, true)
	ctx := context.Background()
	draft := only(t, f.settle(t, stlAugStart))

	mid := time.Date(2026, time.July, 15, 0, 0, 0, 0, stlOslo)
	f.h.Exec(t, `UPDATE energy.supply_periods SET "end" = $2 WHERE id = $1`, f.supply, mid)
	page, err := f.provider.Settlements(ctx, []int64{draft.ID})
	if err != nil {
		t.Fatalf("Settlements: %v", err)
	}
	got := only(t, page)
	if got.ID != draft.ID || got.Revision != 2 || !got.PeriodEnd.Equal(mid) || got.KWh != "336.000" || got.Amount != "420.00" {
		t.Errorf("after the end moved Settlements = id %d revision %d ends %v %s kWh %s NOK, want id %d revision 2 ending %v, 336.000 kWh, 420.00 NOK",
			got.ID, got.Revision, got.PeriodEnd, got.KWh, got.Amount, draft.ID, mid)
	}
	if end := modtest.One[time.Time](t, f.h, `SELECT period_end FROM energy.settlements WHERE id = $1`, draft.ID); !end.Equal(mid) {
		t.Errorf("stored period_end = %v, want %v", end, mid)
	}

	f.h.Exec(t, `UPDATE energy.supply_periods SET status = 'Cancelled' WHERE id = $1`, f.supply)
	page, err = f.provider.Settlements(ctx, []int64{draft.ID})
	if err != nil {
		t.Fatalf("Settlements: %v", err)
	}
	if len(page.Settlements) != 0 {
		t.Errorf("Settlements of a cancelled supply period's draft = %+v, want nothing", page.Settlements)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM energy.settlements WHERE id = $1`, draft.ID); n != 0 {
		t.Errorf("the cancelled supply period's draft survived Settlements")
	}
}

// TestConsumptionProvider_AnEarlierThroughKeepsLaterDrafts: a Settle through
// 1 August leaves August's draft — written by a Settle through 1 September,
// and perhaps held by another invoice — as it is: a draft is deleted only
// when its period is no longer a period of the supply period.
func TestConsumptionProvider_AnEarlierThroughKeepsLaterDrafts(t *testing.T) {
	t.Parallel()
	f := newStlFixture(t, true)
	stlWriteHours(t, f.h, f.point, stlAugStart, time.Date(2026, time.September, 1, 0, 0, 0, 0, stlOslo), f.h.Now())
	page := f.settle(t, time.Date(2026, time.September, 1, 0, 0, 0, 0, stlOslo))
	if len(page.Settlements) != 2 {
		t.Fatalf("Settle through 1 September = %d settlements, want July and August", len(page.Settlements))
	}
	aug := page.Settlements[1]

	only(t, f.settle(t, stlAugStart))
	if n := f.h.Count(t, `SELECT count(*) FROM energy.settlements WHERE supply_period_id = $1 AND state = 'draft'`, f.supply); n != 2 {
		t.Errorf("drafts after a Settle through 1 August = %d, want July and August", n)
	}
	got, err := f.provider.Settlements(context.Background(), []int64{aug.ID})
	if err != nil {
		t.Fatalf("Settlements: %v", err)
	}
	if s := only(t, got); s.ID != aug.ID || s.Revision != aug.Revision {
		t.Errorf("Settlements(August) = id %d revision %d, want id %d revision %d", s.ID, s.Revision, aug.ID, aug.Revision)
	}
}

// TestConsumptionProvider_ABlockedCorrectionIsTouched: a correction draft
// whose recomputation is blocked is answered and kept as it stands — and
// touched, by Settlements and by Settle alike, so the thirty-day housekeeping
// never deletes a draft an invoice holds (plan reading 7).
func TestConsumptionProvider_ABlockedCorrectionIsTouched(t *testing.T) {
	t.Parallel()
	f := newStlFixture(t, true)
	f.stamp(t, only(t, f.settle(t, stlAugStart)))
	f.h.Advance(time.Hour)
	stlChangeHour(t, f.h, f.point, stlJulyStart.Add(5*time.Hour), "3", f.h.Now())
	correction := only(t, f.settle(t, stlAugStart))

	stale := f.h.Now().Add(-31 * 24 * time.Hour)
	f.h.Exec(t, `UPDATE energy.settlements SET touched_at = $2 WHERE id = $1`, correction.ID, stale)
	f.h.Exec(t, `DELETE FROM energy.consumption_intervals WHERE metering_point_id = $1 AND start = $2`, f.point, stlJulyStart.Add(9*time.Hour))
	f.h.Advance(time.Hour)
	page, err := f.provider.Settlements(context.Background(), []int64{correction.ID})
	if err != nil {
		t.Fatalf("Settlements: %v", err)
	}
	if got := only(t, page); got.Blocked != contracts.SettlementIncomplete {
		t.Errorf("Settlements = blocked %q, want incomplete", got.Blocked)
	}
	if touched := modtest.One[time.Time](t, f.h, `SELECT touched_at FROM energy.settlements WHERE id = $1`, correction.ID); !touched.Equal(f.h.Now()) {
		t.Errorf("after Settlements touched_at = %v, want %v", touched, f.h.Now())
	}

	f.h.Exec(t, `UPDATE energy.settlements SET touched_at = $2 WHERE id = $1`, correction.ID, stale)
	f.h.Advance(time.Hour)
	f.settle(t, stlAugStart)
	if n := f.h.Count(t, `SELECT count(*) FROM energy.settlements WHERE id = $1`, correction.ID); n != 1 {
		t.Fatalf("Settle deleted the blocked correction draft")
	}
	if touched := modtest.One[time.Time](t, f.h, `SELECT touched_at FROM energy.settlements WHERE id = $1`, correction.ID); !touched.Equal(f.h.Now()) {
		t.Errorf("after Settle touched_at = %v, want %v", touched, f.h.Now())
	}
}
