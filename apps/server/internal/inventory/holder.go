package inventory

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// variantHolder is this module's contracts.VariantReferenceHolder (I23): it
// answers what stock a variant holds, so Products refuses to delete it, or to
// turn its product into a service, while it lies anywhere. It needs nothing
// of Deps, since it is built for a disabled module too: stock in the schema
// is still stock.
type variantHolder struct{}

var _ contracts.VariantReferenceHolder = variantHolder{}

// newVariantHolder is Module's VariantReferences.
func newVariantHolder(module.Deps) contracts.VariantReferenceHolder { return variantHolder{} }

// VariantHeld sums the variant's balances on the caller's transaction and
// answers the quantity, as the shortest decimal that says it, and the number
// of locations it lies on — nil when it lies nowhere. It locks nothing: a
// posting that would add to the variant holds FOR SHARE on it
// (ProductCatalog.LockVariants), so it has committed before Products' FOR
// UPDATE was granted, or waits for Products' transaction.
func (variantHolder) VariantHeld(ctx context.Context, tx pgx.Tx, variantID int32) (*contracts.VariantHold, error) {
	row, err := store.New(tx).VariantHeld(ctx, variantID)
	if err != nil {
		return nil, fmt.Errorf("inventory: read the stock of variant %d: %w", variantID, err)
	}
	if row.Locations == 0 {
		return nil, nil
	}
	return &contracts.VariantHold{Quantity: decimalText(ratFromText(row.Quantity)), Locations: int(row.Locations)}, nil
}
