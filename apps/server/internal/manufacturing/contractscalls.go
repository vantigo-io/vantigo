package manufacturing

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
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

// stockLocations is, per variant, the active locations of the site holding
// it, the primary first then by full code, before any transaction: how a
// production splits a line's consumption (M22a).
func (s *server) stockLocations(ctx context.Context, variantIDs []int32, siteID int64) (map[int32][]contracts.StockLocation, error) {
	out := map[int32][]contracts.StockLocation{}
	ids := slices.Clone(variantIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) == 0 {
		return out, nil
	}
	if err := noteContractCall(ctx, "Stock.Locations"); err != nil {
		return nil, err
	}
	if s.deps.Stock == nil {
		return nil, errDependencyUnavailable
	}
	for batch := range slices.Chunk(ids, contracts.MaxStockVariants) {
		locations, err := s.deps.Stock.Locations(ctx, batch, siteID)
		if err != nil {
			return nil, fmt.Errorf("manufacturing: read where the components lie: %w", err)
		}
		for _, l := range locations {
			out[l.VariantID] = append(out[l.VariantID], l)
		}
	}
	for id := range out {
		slices.SortStableFunc(out[id], func(a, b contracts.StockLocation) int {
			if a.Primary != b.Primary {
				if a.Primary {
					return -1
				}
				return 1
			}
			return strings.Compare(a.FullCode, b.FullCode)
		})
	}
	return out, nil
}

// users is the users named by ids, before any transaction, by id.
func (s *server) users(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]contracts.UserEntry, error) {
	out := map[uuid.UUID]contracts.UserEntry{}
	ids = slices.Compact(slices.SortedFunc(slices.Values(ids), func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) }))
	if len(ids) == 0 || s.deps.Users == nil {
		return out, nil
	}
	if err := noteContractCall(ctx, "Users.Users"); err != nil {
		return nil, err
	}
	entries, err := s.deps.Users.Users(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("manufacturing: read the users: %w", err)
	}
	for _, e := range entries {
		out[e.ID] = e
	}
	return out, nil
}

// searchUsers is the user directory's search, before any transaction.
func (s *server) searchUsers(ctx context.Context, q string, limit int) ([]contracts.UserEntry, error) {
	if s.deps.Users == nil {
		return nil, nil
	}
	if err := noteContractCall(ctx, "Users.SearchUsers"); err != nil {
		return nil, err
	}
	found, err := s.deps.Users.SearchUsers(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("manufacturing: search the users: %w", err)
	}
	return found, nil
}

// today is the business calendar's day of now, at midnight UTC, and the
// instant the next business day starts, before any transaction.
func (s *server) today(ctx context.Context) (time.Time, time.Time, error) {
	now := s.deps.Clock()
	if s.deps.Calendar == nil {
		day := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
		return day, day.AddDate(0, 0, 1), nil
	}
	if err := noteContractCall(ctx, "Calendar.Today"); err != nil {
		return time.Time{}, time.Time{}, err
	}
	zone, err := s.deps.Calendar.Zone(ctx)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("manufacturing: read the business zone: %w", err)
	}
	local := now.In(zone)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	return day, time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, zone), nil
}

// permInventoryExpiredOverride is Inventory's key that lets a person consume
// an expired lot (manufacturing phase 2 M62, M68), read on the pool before a
// production's transaction and passed to the ledger as data.
const permInventoryExpiredOverride = "inventory:expired-override"

// stockDay is today in the business zone, before any transaction: the day
// Inventory's ledger judges a lot's expiry on (manufacturing phase 2 M62,
// M63), passed to it as data since no ledger call makes a contract call of
// its own.
func (s *server) stockDay(ctx context.Context) (time.Time, error) {
	day, _, err := s.today(ctx)
	return day, err
}

// businessZone is the business time zone, before any transaction: UTC when
// no calendar is composed. The schedule's days are worked out in it (§11).
func (s *server) businessZone(ctx context.Context) (*time.Location, error) {
	if s.deps.Calendar == nil {
		return time.UTC, nil
	}
	if err := noteContractCall(ctx, "Calendar.Zone"); err != nil {
		return nil, err
	}
	zone, err := s.deps.Calendar.Zone(ctx)
	if err != nil {
		return nil, fmt.Errorf("manufacturing: read the business zone: %w", err)
	}
	return zone, nil
}

// salesOrderLines is Orders' lines of one sales order, before any
// transaction (M43a: consumer-gated, so the caller has decided). Orders off
// is errDependencyUnavailable.
func (s *server) salesOrderLines(ctx context.Context, orderID int64) ([]contracts.SalesOrderLine, error) {
	if err := noteContractCall(ctx, "SalesOrderLines.OrderLines"); err != nil {
		return nil, err
	}
	if s.deps.SalesOrderLines == nil {
		return nil, errDependencyUnavailable
	}
	lines, err := s.deps.SalesOrderLines.OrderLines(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("manufacturing: read sales order %d's lines: %w", orderID, err)
	}
	return lines, nil
}

// salesOrderLine is one sales order line, before any transaction; nil when
// Orders does not answer it.
func (s *server) salesOrderLine(ctx context.Context, lineID int64) (*contracts.SalesOrderLine, error) {
	if err := noteContractCall(ctx, "SalesOrderLines.Lines"); err != nil {
		return nil, err
	}
	if s.deps.SalesOrderLines == nil {
		return nil, errDependencyUnavailable
	}
	lines, err := s.deps.SalesOrderLines.Lines(ctx, []int64{lineID})
	if err != nil {
		return nil, fmt.Errorf("manufacturing: read sales order line %d: %w", lineID, err)
	}
	for _, l := range lines {
		if l.LineID == lineID {
			return &l, nil
		}
	}
	return nil, nil
}

// customerName is a customer's name through the customer directory, before
// any transaction; "" when the directory is not composed or does not know
// them. Only a caller holding orders:access is shown it (M43a).
func (s *server) customerName(ctx context.Context, id int32) (string, error) {
	if s.deps.Directory == nil {
		return "", nil
	}
	if err := noteContractCall(ctx, "Directory.Customer"); err != nil {
		return "", err
	}
	c, err := s.deps.Directory.Customer(ctx, id)
	if err != nil {
		return "", fmt.Errorf("manufacturing: read customer %d: %w", id, err)
	}
	if c == nil {
		return "", nil
	}
	return c.Name, nil
}
