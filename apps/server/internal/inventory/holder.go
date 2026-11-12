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
// of locations it lies on; when it lies nowhere but is reserved, what is
// reserved and at how many sites (VariantHoldKindReserved); when neither but
// it is on its way between sites, what is in transit and on how many
// transfers (VariantHoldKindInTransit); nil when none. It locks nothing: a
// posting that would add to the variant holds FOR SHARE on it
// (ProductCatalog.LockVariants), so it has committed before Products' FOR
// UPDATE was granted, or waits for Products' transaction. Stock stops all
// three purposes — a delete, a change to Service and a change of the tracking
// mode (manufacturing phase 2 M51) — so purpose changes nothing here.
func (variantHolder) VariantHeld(ctx context.Context, tx pgx.Tx, variantID int32, _ contracts.VariantHoldPurpose) (*contracts.VariantHold, error) {
	q := store.New(tx)
	row, err := q.VariantHeld(ctx, variantID)
	if err != nil {
		return nil, fmt.Errorf("inventory: read the stock of variant %d: %w", variantID, err)
	}
	if row.Locations > 0 {
		return &contracts.VariantHold{
			Kind: contracts.VariantHoldKindStock, Quantity: decimalText(ratFromText(row.Quantity)), Locations: int(row.Locations),
		}, nil
	}
	// Reserved with nothing on hand: an order still waits for it (orders
	// design O45). A reserve holds FOR SHARE on the variant too.
	reserved, err := q.VariantReserved(ctx, variantID)
	if err != nil {
		return nil, fmt.Errorf("inventory: read the reservations of variant %d: %w", variantID, err)
	}
	if reserved.Sites > 0 {
		return &contracts.VariantHold{
			Kind: contracts.VariantHoldKindReserved, Quantity: decimalText(ratFromText(reserved.Quantity)), Locations: int(reserved.Sites),
		}, nil
	}
	// On its way between sites with nothing on hand or reserved: a transfer
	// sent and not yet received, recorded missing or cancelled (phase 2
	// design I47), for a delete and a change to Service alike. A send and a
	// receive hold FOR SHARE on the variant too (LockVariants), so neither
	// slips past Products' FOR UPDATE. Answered with the module disabled as
	// well: the stock is still on its way (I50).
	transit, err := q.VariantInTransit(ctx, variantID)
	if err != nil {
		return nil, fmt.Errorf("inventory: read what of variant %d is in transit: %w", variantID, err)
	}
	if transit.Transfers == 0 {
		return nil, nil
	}
	return &contracts.VariantHold{
		Kind: contracts.VariantHoldKindInTransit, Quantity: decimalText(ratFromText(transit.Quantity)), Locations: int(transit.Transfers),
	}, nil
}
