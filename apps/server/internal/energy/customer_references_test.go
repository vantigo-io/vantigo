package energy_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/energy"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// repointEnergyCustomer runs the module's own holder inside a transaction the
// test owns — the customers merge's position — and commits it or rolls it back
// as told.
func repointEnergyCustomer(t *testing.T, h *modtest.Harness, from, into int32, commit bool) []contracts.RepointedReferences {
	t.Helper()
	build := energy.Module().CustomerReferences
	if build == nil {
		t.Fatal("the module declares no customer reference holder")
	}
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	moved, err := build(h.Deps()).RepointCustomer(ctx, tx, from, into)
	if err != nil {
		t.Fatalf("RepointCustomer(%d, %d): %v", from, into, err)
	}
	if commit {
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
	return moved
}

// TestCustomerReferences_RepointsEverySupplyPeriodOfTheAbsorbedCustomer is
// energy's half of a customer merge (customers merge design D1): every supply
// period of the absorbed customer supplies the survivor. The overlap
// constraint is per metering point, so the survivor already having a period
// on the same point is no conflict; the survivor's own period is not written.
func TestCustomerReferences_RepointsEverySupplyPeriodOfTheAbsorbedCustomer(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	point := createMeteringPoint(t, h.SignIn(t, allEnergyPermissions...))
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	first := insertActiveSupplyPeriod(t, h, point.Id, 1001, start, start.AddDate(0, 1, 0))
	second := insertActiveSupplyPeriod(t, h, point.Id, 1001, start.AddDate(0, 2, 0), start.AddDate(0, 3, 0))
	own := insertActiveSupplyPeriod(t, h, point.Id, 1002, start.AddDate(0, 4, 0), start.AddDate(0, 5, 0))

	moved := repointEnergyCustomer(t, h, 1001, 1002, true)

	if want := []contracts.RepointedReferences{{Kind: "energy.supplyPeriods", Count: 2}}; !slices.Equal(moved, want) {
		t.Errorf("RepointCustomer = %+v, want %+v", moved, want)
	}
	for _, id := range []int32{first, second, own} {
		if got := modtest.One[int32](t, h, `SELECT customer_id FROM energy.supply_periods WHERE id = $1`, id); got != 1002 {
			t.Errorf("supply period %d customer_id = %d, want 1002", id, got)
		}
	}
}

// Rolled back, nothing moved; a customer with no supply periods is a zero.
func TestCustomerReferences_WritesOnlyInsideTheCallersTransaction(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	point := createMeteringPoint(t, h.SignIn(t, allEnergyPermissions...))
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	id := insertActiveSupplyPeriod(t, h, point.Id, 1001, start, start.AddDate(0, 1, 0))

	if moved := repointEnergyCustomer(t, h, 1001, 1002, false); len(moved) != 1 || moved[0].Count != 1 {
		t.Fatalf("RepointCustomer = %+v, want one supply period", moved)
	}
	if got := modtest.One[int32](t, h, `SELECT customer_id FROM energy.supply_periods WHERE id = $1`, id); got != 1001 {
		t.Errorf("after a rollback customer_id = %d, want 1001 still", got)
	}
	want := []contracts.RepointedReferences{{Kind: "energy.supplyPeriods", Count: 0}}
	if moved := repointEnergyCustomer(t, h, 4242, 1002, true); !slices.Equal(moved, want) {
		t.Errorf("RepointCustomer for a customer with no supply periods = %+v, want %+v", moved, want)
	}
}
