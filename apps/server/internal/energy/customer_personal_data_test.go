package energy_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/energy"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

func energyPersonalData(t *testing.T, h *modtest.Harness) contracts.CustomerPersonalData {
	t.Helper()
	build := energy.Module().CustomerPersonalData
	if build == nil {
		t.Fatal("the module declares no customer personal data")
	}
	return build(h.Deps())
}

// TestCustomerPersonalData_ExportsSupplyPeriodsWithTheirAddress is energy's
// section of a private person's export (customers GDPR design D2): every
// supply period with its metering point's address, and none of another
// customer's; nothing at all for a customer never supplied.
func TestCustomerPersonalData_ExportsSupplyPeriodsWithTheirAddress(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	point := createMeteringPoint(t, h.SignIn(t, allEnergyPermissions...))
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	insertActiveSupplyPeriod(t, h, point.Id, 1001, start, start.AddDate(0, 1, 0))
	insertActiveSupplyPeriod(t, h, point.Id, 1002, start.AddDate(0, 2, 0), start.AddDate(0, 3, 0))
	address := modtest.One[string](t, h, `SELECT street_address FROM energy.metering_points WHERE id = $1`, point.Id)

	section, err := energyPersonalData(t, h).ExportCustomerData(context.Background(), 1001)
	if err != nil {
		t.Fatalf("ExportCustomerData: %v", err)
	}
	body, _ := json.Marshal(section)
	var got struct {
		SupplyPeriods []struct {
			Start         time.Time  `json:"start"`
			End           *time.Time `json:"end"`
			Status        string     `json:"status"`
			MeteringPoint struct {
				StreetAddress string `json:"streetAddress"`
			} `json:"meteringPoint"`
		} `json:"supplyPeriods"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if len(got.SupplyPeriods) != 1 || !got.SupplyPeriods[0].Start.Equal(start) || got.SupplyPeriods[0].Status != "Active" ||
		got.SupplyPeriods[0].MeteringPoint.StreetAddress != address {
		t.Errorf("section = %s, want customer 1001's one period at %q", body, address)
	}
	if none, err := energyPersonalData(t, h).ExportCustomerData(context.Background(), 1003); err != nil || none != nil {
		t.Errorf("a customer never supplied = %v, %v; want nil, nil", none, err)
	}
}

// A supply period carries its metering point's consumption while it ran
// (customers GDPR design D2, widened by the whole-branch review): household
// meter readings are the person's data. Monthly sums, in the point's market
// zone, of the intervals inside the period's dates — one outside them, before
// or after, is somebody else's or nobody's.
func TestCustomerPersonalData_ExportsTheConsumptionInsideEachPeriod(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	point := createMeteringPoint(t, h.SignIn(t, allEnergyPermissions...))
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	insertActiveSupplyPeriod(t, h, point.Id, 1001, start, start.AddDate(0, 2, 0))
	addElhubConsumption(t, h, point.Id, utc(2026, time.January, 5, 1), utc(2026, time.January, 5, 2), 2.5)
	addElhubConsumption(t, h, point.Id, utc(2026, time.January, 10, 1), utc(2026, time.January, 10, 2), 1.25)
	addElhubConsumption(t, h, point.Id, utc(2026, time.February, 3, 1), utc(2026, time.February, 3, 2), 4)
	// After the period ended: not the person's.
	addElhubConsumption(t, h, point.Id, utc(2026, time.March, 3, 1), utc(2026, time.March, 3, 2), 100)

	section, err := energyPersonalData(t, h).ExportCustomerData(context.Background(), 1001)
	if err != nil {
		t.Fatalf("ExportCustomerData: %v", err)
	}
	body, _ := json.Marshal(section)
	var got struct {
		SupplyPeriods []struct {
			Consumption []struct {
				Month string  `json:"month"`
				Kwh   float64 `json:"kwh"`
			} `json:"consumption"`
		} `json:"supplyPeriods"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if len(got.SupplyPeriods) != 1 {
		t.Fatalf("section = %s, want one period", body)
	}
	if c := got.SupplyPeriods[0].Consumption; len(c) != 2 || c[0].Month != "2026-01" || c[0].Kwh != 3.75 || c[1].Month != "2026-02" || c[1].Kwh != 4 {
		t.Errorf("consumption = %+v, want 2026-01 3.75 and 2026-02 4, and nothing from after the period", c)
	}
}

// A cancelled supply period never supplied anybody, and the overlap rule
// ignores it, so another customer's live period can cover the same dates on
// the same point: its readings are theirs. The cancelled period is handed over
// — it is still held — with no consumption.
func TestCustomerPersonalData_ACancelledPeriodHandsOverNoConsumption(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	point := createMeteringPoint(t, h.SignIn(t, allEnergyPermissions...))
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h.Exec(t, `INSERT INTO energy.supply_periods (metering_point_id, customer_id, start, "end", status)
	           VALUES ($1, 1001, $2, $3, 'Cancelled')`, point.Id, start, start.AddDate(0, 2, 0))
	insertActiveSupplyPeriod(t, h, point.Id, 1002, start, start.AddDate(0, 2, 0))
	addElhubConsumption(t, h, point.Id, utc(2026, time.January, 5, 1), utc(2026, time.January, 5, 2), 2.5)

	section, err := energyPersonalData(t, h).ExportCustomerData(context.Background(), 1001)
	if err != nil {
		t.Fatalf("ExportCustomerData: %v", err)
	}
	body, _ := json.Marshal(section)
	var got struct {
		SupplyPeriods []struct {
			Status      string            `json:"status"`
			Consumption []json.RawMessage `json:"consumption"`
		} `json:"supplyPeriods"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if len(got.SupplyPeriods) != 1 || got.SupplyPeriods[0].Status != "Cancelled" || got.SupplyPeriods[0].Consumption == nil ||
		len(got.SupplyPeriods[0].Consumption) != 0 {
		t.Errorf("section = %s, want the cancelled period with consumption [] — the readings are customer 1002's", body)
	}
}

// Erasing keeps everything (design D2): a supply period is the metering
// point's history, and its address is the point's, not the person's; the
// period keeps pointing at the anonymised customer. It still says it was asked.
func TestCustomerPersonalData_EraseKeepsTheSupplyPeriods(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	point := createMeteringPoint(t, h.SignIn(t, allEnergyPermissions...))
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	id := insertActiveSupplyPeriod(t, h, point.Id, 1001, start, start.AddDate(0, 1, 0))

	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	erased, err := energyPersonalData(t, h).EraseCustomerData(ctx, tx, 1001)
	if err != nil {
		t.Fatalf("EraseCustomerData: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if want := []contracts.ErasedData{{Kind: "energy.supplyPeriods", Count: 0}}; !slices.Equal(erased, want) {
		t.Errorf("EraseCustomerData = %+v, want %+v", erased, want)
	}
	if got := modtest.One[int32](t, h, `SELECT customer_id FROM energy.supply_periods WHERE id = $1`, id); got != 1001 {
		t.Errorf("the supply period's customer_id = %d, want 1001 still", got)
	}
}
