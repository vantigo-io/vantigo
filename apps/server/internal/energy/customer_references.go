package energy

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// customerReferenceKindSupplyPeriods is the one kind of customer reference
// this module holds: energy.supply_periods.customer_id.
const customerReferenceKindSupplyPeriods = "energy.supplyPeriods"

// customerReferenceHolder is this module's contracts.CustomerReferenceHolder
// (customers merge design D1): when two customers are merged, every metering
// point the absorbed one was supplied at is supplied to the survivor, over the
// same periods.
type customerReferenceHolder struct{}

var _ contracts.CustomerReferenceHolder = customerReferenceHolder{}

// newCustomerReferenceHolder is Module's CustomerReferences. It needs nothing
// from d: a supply period carries no timestamp or revision a move would touch.
func newCustomerReferenceHolder(module.Deps) contracts.CustomerReferenceHolder {
	return customerReferenceHolder{}
}

// RepointCustomer moves every supply period of from to into, inside the
// caller's transaction (RepointSupplyPeriodsCustomer).
func (customerReferenceHolder) RepointCustomer(ctx context.Context, tx pgx.Tx, from, into int32) ([]contracts.RepointedReferences, error) {
	n, err := store.New(tx).RepointSupplyPeriodsCustomer(ctx, store.RepointSupplyPeriodsCustomerParams{
		FromCustomerID: from, IntoCustomerID: into,
	})
	if err != nil {
		return nil, fmt.Errorf("energy: re-point customer %d's supply periods to %d: %w", from, into, err)
	}
	return []contracts.RepointedReferences{{Kind: customerReferenceKindSupplyPeriods, Count: n}}, nil
}
