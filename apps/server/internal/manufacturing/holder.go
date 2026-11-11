package manufacturing

// The variant holder (manufacturing design M16, M16a; module-boundaries rule
// 20). It runs its own SQL on this module's schema, on Products'
// transaction, never begins or ends one, reads no contract, and runs whether
// or not Manufacturing is enabled: it is built from nothing but the module's
// queries.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// variantHolder answers whether active BoMs name a variant, as their product
// or on a line: a delete and a change to Service are both refused while one
// does (M16) — a service can be neither consumed nor produced (M16a), which
// departs from Orders' O30 on purpose. An archived BoM holds nothing.
// Delivery B adds the manufacturing orders that are not done or cancelled.
type variantHolder struct{}

var _ contracts.VariantReferenceHolder = variantHolder{}

func newVariantHolder(module.Deps) contracts.VariantReferenceHolder { return variantHolder{} }

// VariantHeld counts the active BoMs on Products' transaction, which holds
// the variant FOR UPDATE; every BoM write holds it FOR SHARE first
// (lockVariants, M10a), so the count is true when it is read. It answers a
// count, because contracts.VariantHold carries no list of documents.
func (variantHolder) VariantHeld(ctx context.Context, tx pgx.Tx, variantID int32, purpose contracts.VariantHoldPurpose) (*contracts.VariantHold, error) {
	if purpose != contracts.VariantHoldDelete && purpose != contracts.VariantHoldTypeChange {
		return nil, nil
	}
	n, err := store.New(tx).CountActiveBomsNamingVariant(ctx, variantID)
	if err != nil {
		return nil, fmt.Errorf("manufacturing: count the active BoMs of variant %d: %w", variantID, err)
	}
	if n == 0 {
		return nil, nil
	}
	return &contracts.VariantHold{Kind: contracts.VariantHoldKindOpenDocuments, Documents: int(n)}, nil
}
