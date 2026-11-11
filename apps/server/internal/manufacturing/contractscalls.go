package manufacturing

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// Every call out of the module goes through one accessor here, so the rule
// the transaction marks carry is checked in one place: no call that takes its
// own connection, or leaves the process, is made inside one of this module's
// transactions (Orders' and Inventory's contractscalls.go, copied).
// lockVariants is the one exception, by MB rule 21: it runs ON the
// transaction, on the caller's own connection, and is the first thing a BoM
// write does (M10a).

// errCallInsideTransaction is an accessor's answer when it is called from
// inside one of this module's transactions: a bug, answered as a 500.
var errCallInsideTransaction = errors.New("manufacturing: a call out of the module was made inside one of its transactions")

// contractCallHook is the tests' window on every call out of the module
// (SetContractCallHook): the harness records any made inside a transaction.
var contractCallHook func(ctx context.Context, method string)

// noteContractCall reports the call to the hook and refuses it inside a
// transaction.
func noteContractCall(ctx context.Context, method string) error {
	if contractCallHook != nil {
		contractCallHook(ctx, method)
	}
	if inOpenTx(ctx) {
		return errCallInsideTransaction
	}
	return nil
}

// catalogVariants is the catalog's batch read, before any transaction. An id
// the catalog does not know is absent.
func (s *server) catalogVariants(ctx context.Context, ids []int32) (map[int32]contracts.VariantEntry, error) {
	out := map[int32]contracts.VariantEntry{}
	if len(ids) == 0 {
		return out, nil
	}
	if err := noteContractCall(ctx, "Products.Variants"); err != nil {
		return nil, err
	}
	if s.deps.Products == nil {
		return nil, errDependencyUnavailable
	}
	entries, err := s.deps.Products.Variants(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("manufacturing: read the catalog's variants: %w", err)
	}
	for _, e := range entries {
		out[e.ID] = e
	}
	return out, nil
}

// lockVariants takes FOR SHARE on the variants and their products ON tx, in
// variant id order (MB rule 21, M10a): the one call out of the module made
// inside a transaction, and so the one accessor that does not refuse one. The
// hook still hears of it, under a name the harness exempts. Whether each is
// goods is judged from what it answers, never from an earlier read.
func (s *server) lockVariants(ctx context.Context, tx pgx.Tx, ids []int32) (map[int32]contracts.VariantEntry, error) {
	if contractCallHook != nil {
		contractCallHook(ctx, lockVariantsCall)
	}
	if s.deps.Products == nil {
		return nil, errDependencyUnavailable
	}
	out := map[int32]contracts.VariantEntry{}
	if len(ids) == 0 {
		return out, nil
	}
	entries, err := s.deps.Products.LockVariants(ctx, tx, ids)
	if err != nil {
		return nil, fmt.Errorf("manufacturing: lock the variants: %w", err)
	}
	for _, e := range entries {
		out[e.ID] = e
	}
	return out, nil
}

// lockVariantsCall is the name the hook hears lockVariants by.
const lockVariantsCall = "Products.LockVariants"

// searchVariants is the catalog's search for a BoM's product or component,
// before any transaction.
func (s *server) searchVariants(ctx context.Context, q string, search contracts.VariantSearch) ([]contracts.VariantEntry, error) {
	if err := noteContractCall(ctx, "Products.SearchVariants"); err != nil {
		return nil, err
	}
	if s.deps.Products == nil {
		return nil, errDependencyUnavailable
	}
	v, err := s.deps.Products.SearchVariants(ctx, q, search)
	if err != nil {
		return nil, fmt.Errorf("manufacturing: search the catalog: %w", err)
	}
	return v, nil
}

// stockSites is every site that is not archived, by code, before any
// transaction: what a work center may stand at (M8).
func (s *server) stockSites(ctx context.Context) ([]contracts.StockSite, error) {
	if err := noteContractCall(ctx, "Stock.Sites"); err != nil {
		return nil, err
	}
	if s.deps.Stock == nil {
		return nil, errDependencyUnavailable
	}
	sites, err := s.deps.Stock.Sites(ctx)
	if err != nil {
		return nil, fmt.Errorf("manufacturing: read the sites: %w", err)
	}
	return sites, nil
}

// stockAvailable is each variant's available quantity at siteID (every site
// when nil), before any transaction: the ids deduplicated and read in batches
// of contracts.MaxStockVariants (M15a), answered by variant.
func (s *server) stockAvailable(ctx context.Context, variantIDs []int32, siteID *int64) (map[int32]contracts.StockQuantity, error) {
	out := map[int32]contracts.StockQuantity{}
	ids := slices.Clone(variantIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) == 0 {
		return out, nil
	}
	if err := noteContractCall(ctx, "Stock.Available"); err != nil {
		return nil, err
	}
	if s.deps.Stock == nil {
		return nil, errDependencyUnavailable
	}
	for batch := range slices.Chunk(ids, contracts.MaxStockVariants) {
		quantities, err := s.deps.Stock.Available(ctx, batch, siteID)
		if err != nil {
			return nil, fmt.Errorf("manufacturing: read the available stock: %w", err)
		}
		for _, q := range quantities {
			out[q.VariantID] = q
		}
	}
	return out, nil
}

// siteLocations is every location of the site, archived ones too, by full
// code, before any transaction: where an order takes its components from and
// puts its output (M2). An unknown site has none.
func (s *server) siteLocations(ctx context.Context, siteID int64) ([]contracts.StockSiteLocation, error) {
	if err := noteContractCall(ctx, "Stock.SiteLocations"); err != nil {
		return nil, err
	}
	if s.deps.Stock == nil {
		return nil, errDependencyUnavailable
	}
	locations, err := s.deps.Stock.SiteLocations(ctx, siteID)
	if err != nil {
		return nil, fmt.Errorf("manufacturing: read site %d's locations: %w", siteID, err)
	}
	return locations, nil
}
