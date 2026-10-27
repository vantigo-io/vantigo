package energy

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The kinds of customer reference this module holds:
// energy.supply_periods.customer_id, and energy.settlements.customer_id, the
// copy a settlement keeps of its supply period's (energy settlements plan
// reading 13).
const (
	customerReferenceKindSupplyPeriods = "energy.supplyPeriods"
	customerReferenceKindSettlements   = "energy.settlements"
)

// customerReferenceHolder is this module's contracts.CustomerReferenceHolder
// (customers merge design D1): when two customers are merged, every metering
// point the absorbed one was supplied at is supplied to the survivor, over the
// same periods, and the settlements priced for those periods follow them.
type customerReferenceHolder struct{}

var _ contracts.CustomerReferenceHolder = customerReferenceHolder{}

// newCustomerReferenceHolder is Module's CustomerReferences. It needs nothing
// from d: a supply period carries no timestamp or revision a move would touch.
func newCustomerReferenceHolder(module.Deps) contracts.CustomerReferenceHolder {
	return customerReferenceHolder{}
}

// RepointCustomer moves every supply period of from to into, and every
// settlement with them, inside the caller's transaction
// (RepointSupplyPeriodsCustomer, RepointSettlementsCustomer). A customer
// merged into itself moves nothing — the merge refuses that before any holder
// runs (merge_self), and this guard keeps the answer honest should that
// change.
func (customerReferenceHolder) RepointCustomer(ctx context.Context, tx pgx.Tx, from, into int32) ([]contracts.RepointedReferences, error) {
	if from == into {
		return []contracts.RepointedReferences{
			{Kind: customerReferenceKindSupplyPeriods, Count: 0},
			{Kind: customerReferenceKindSettlements, Count: 0},
		}, nil
	}
	q := store.New(tx)
	periods, err := q.RepointSupplyPeriodsCustomer(ctx, store.RepointSupplyPeriodsCustomerParams{
		FromCustomerID: from, IntoCustomerID: into,
	})
	if err != nil {
		return nil, fmt.Errorf("energy: re-point customer %d's supply periods to %d: %w", from, into, err)
	}
	settlements, err := q.RepointSettlementsCustomer(ctx, store.RepointSettlementsCustomerParams{
		FromCustomerID: from, IntoCustomerID: into,
	})
	if err != nil {
		return nil, fmt.Errorf("energy: re-point customer %d's settlements to %d: %w", from, into, err)
	}
	return []contracts.RepointedReferences{
		{Kind: customerReferenceKindSupplyPeriods, Count: periods},
		{Kind: customerReferenceKindSettlements, Count: settlements},
	}, nil
}
