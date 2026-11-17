package inventory

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// Every call out of the module goes through one accessor here, so the rule
// the transaction marks carry is checked in one place: no call that takes its
// own connection is made inside one of this module's transactions
// (Planning's contractscalls.go, copied). lockVariants is the one exception,
// by design I22: it runs ON the transaction, on the caller's own connection,
// and is the first thing a posting does after reading its key.

// errCallInsideTransaction is an accessor's answer when it is called from
// inside one of this module's transactions: a bug, answered as a 500.
var errCallInsideTransaction = errors.New("inventory: a call out of the module was made inside one of its transactions")

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

// catalogVariants is the catalog's batch read, before any transaction: the
// variants a response names. An id the catalog does not know is absent.
func (s *server) catalogVariants(ctx context.Context, ids []int32) ([]contracts.VariantEntry, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if err := noteContractCall(ctx, "Products.Variants"); err != nil {
		return nil, err
	}
	if s.deps.Products == nil {
		return nil, errDependencyUnavailable
	}
	return s.deps.Products.Variants(ctx, ids)
}

// catalogVariantsOfProduct is every variant of a product, before any
// transaction.
func (s *server) catalogVariantsOfProduct(ctx context.Context, productID int32) ([]contracts.VariantEntry, error) {
	if err := noteContractCall(ctx, "Products.VariantsOfProduct"); err != nil {
		return nil, err
	}
	if s.deps.Products == nil {
		return nil, errDependencyUnavailable
	}
	return s.deps.Products.VariantsOfProduct(ctx, productID)
}

// catalogLookupCode is the lookup's variant half (I28), before any
// transaction.
func (s *server) catalogLookupCode(ctx context.Context, code string) ([]contracts.CodeMatch, error) {
	if err := noteContractCall(ctx, "Products.LookupCode"); err != nil {
		return nil, err
	}
	if s.deps.Products == nil {
		return nil, errDependencyUnavailable
	}
	return s.deps.Products.LookupCode(ctx, code)
}

// catalogLookupSKUs is the opening-balance import's SKU resolution, every
// SKU of the file in one call, before any transaction.
func (s *server) catalogLookupSKUs(ctx context.Context, skus []string) (map[string][]contracts.VariantEntry, error) {
	if len(skus) == 0 {
		return nil, nil
	}
	if err := noteContractCall(ctx, "Products.LookupSKUs"); err != nil {
		return nil, err
	}
	if s.deps.Products == nil {
		return nil, errDependencyUnavailable
	}
	return s.deps.Products.LookupSKUs(ctx, skus)
}

// catalogSearchVariants is the line editor's picker (I22), before any
// transaction.
func (s *server) catalogSearchVariants(ctx context.Context, q string, limit int) ([]contracts.VariantEntry, error) {
	if err := noteContractCall(ctx, "Products.SearchVariants"); err != nil {
		return nil, err
	}
	if s.deps.Products == nil {
		return nil, errDependencyUnavailable
	}
	// Goods only, of every status, as the picker has always answered.
	return s.deps.Products.SearchVariants(ctx, q, contracts.VariantSearch{Types: []string{"Goods"}, Limit: limit, IncludeInactive: true})
}

// lockVariants takes FOR SHARE on the variants and their products ON tx, in
// variant id order (I22): the one call out of the module made inside a
// transaction, and so the one accessor that does not refuse one. The hook
// still hears of it, under a name the harness exempts.
func (s *server) lockVariants(ctx context.Context, tx pgx.Tx, ids []int32) ([]contracts.VariantEntry, error) {
	if contractCallHook != nil {
		contractCallHook(ctx, lockVariantsCall)
	}
	if s.deps.Products == nil {
		return nil, errDependencyUnavailable
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return s.deps.Products.LockVariants(ctx, tx, ids)
}

// lockVariantsCall is the name the hook hears lockVariants by.
const lockVariantsCall = "Products.LockVariants"

// usersUser is identity's user directory: the actor of an event and the
// poster of a movement (actorFor), before any transaction.
func (s *server) usersUser(ctx context.Context, id uuid.UUID) (*contracts.UserEntry, error) {
	if err := noteContractCall(ctx, "Users.User"); err != nil {
		return nil, err
	}
	return s.deps.Users.User(ctx, id)
}

// businessZone is the workspace's business time zone, read through the
// business calendar (time zones design D2), before any transaction: the zone
// a movement filter's business days and the stats' week are judged in.
func (s *server) businessZone(ctx context.Context) (*time.Location, error) {
	if err := noteContractCall(ctx, "Calendar.Zone"); err != nil {
		return nil, err
	}
	zone, err := s.deps.Calendar.Zone(ctx)
	if err != nil {
		return nil, fmt.Errorf("inventory: read the business time zone: %w", err)
	}
	return zone, nil
}

// today is the workspace's business day of now, as a date at midnight UTC
// (manufacturing phase 2 M62), read before any transaction: the day a lot's
// expiry is judged against and a new lot's dates default from.
func (s *server) today(ctx context.Context) (time.Time, error) {
	zone, err := s.businessZone(ctx)
	if err != nil {
		return time.Time{}, err
	}
	return businessToday(s.deps.Clock(), zone), nil
}

// tracedDeliveries is Orders' read of the posted deliveries a trace's issues
// were for (manufacturing phase 2 M90), before any transaction, in batches of
// contracts.MaxTracedDeliveries. A nil slot — Orders off — answers
// errNoDeliveryTrace; contracts.ErrForbidden passes through, so the caller
// shows the deliveries as their movements alone.
func (s *server) tracedDeliveries(ctx context.Context, ids []int64) ([]contracts.TracedDelivery, error) {
	if err := noteContractCall(ctx, "DeliveryTrace.Deliveries"); err != nil {
		return nil, err
	}
	if s.deps.DeliveryTrace == nil {
		return nil, errNoDeliveryTrace
	}
	var out []contracts.TracedDelivery
	for start := 0; start < len(ids); start += contracts.MaxTracedDeliveries {
		batch, err := s.deps.DeliveryTrace.Deliveries(ctx, ids[start:min(start+contracts.MaxTracedDeliveries, len(ids))])
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
	}
	return out, nil
}

// errNoDeliveryTrace is tracedDeliveries' answer when Orders is not composed.
var errNoDeliveryTrace = errors.New("inventory: no delivery trace")

// directoryCustomers is the customer directory's batch read, before any
// transaction: the names of a trace's customers, which the caller shows
// only to a holder of customers:view (M90). A nil directory answers none.
func (s *server) directoryCustomers(ctx context.Context, ids []int32) ([]contracts.CustomerEntry, error) {
	if err := noteContractCall(ctx, "Directory.Customers"); err != nil {
		return nil, err
	}
	if s.deps.Directory == nil || len(ids) == 0 {
		return nil, nil
	}
	return s.deps.Directory.Customers(ctx, ids)
}

// openPurchasesOf is Purchasing's open purchases (purchasing design P4, A3)
// of the variants given, at siteID (every site when nil), before any
// transaction; none for no variant. Nothing is on order when Purchasing is
// off.
func (s *server) openPurchasesOf(ctx context.Context, siteID *int64, variantIDs []int32) ([]openPurchase, error) {
	if s.deps.PurchaseSupply == nil || len(variantIDs) == 0 {
		return nil, nil
	}
	if err := noteContractCall(ctx, "PurchaseSupply.OpenByVariants"); err != nil {
		return nil, err
	}
	out, err := s.deps.PurchaseSupply.OpenByVariants(ctx, siteID, variantIDs)
	if err != nil {
		return nil, fmt.Errorf("inventory: read the open purchases: %w", err)
	}
	return s.readOpenPurchases(ctx, out), nil
}

// openPurchasesAll is every (variant, site) Purchasing has open, before any
// transaction: what a statement over every placement joins in. Nothing is on
// order when Purchasing is off.
func (s *server) openPurchasesAll(ctx context.Context) ([]openPurchase, error) {
	if s.deps.PurchaseSupply == nil {
		return nil, nil
	}
	if err := noteContractCall(ctx, "PurchaseSupply.OpenAll"); err != nil {
		return nil, err
	}
	out, err := s.deps.PurchaseSupply.OpenAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("inventory: read the open purchases: %w", err)
	}
	return s.readOpenPurchases(ctx, out), nil
}

// openPurchase is an open purchase with its quantity read.
type openPurchase struct {
	variantID int32
	siteID    int64
	quantity  *big.Rat
}

// readOpenPurchases reads each open purchase's quantity as the ledger reads a
// quantity — a plain decimal, more than 0, at most three decimals — and
// leaves out, with a warning, a row the provider answered with another: a
// fraction such as 1/3 must not reach a statement's numeric cast.
func (s *server) readOpenPurchases(ctx context.Context, rows []contracts.OpenPurchase) []openPurchase {
	out := make([]openPurchase, 0, len(rows))
	for _, r := range rows {
		q, err := ledgerQuantity(r.Quantity)
		if err != nil {
			s.logger().WarnContext(ctx, "inventory: an open purchase's quantity is not a decimal; it is left out",
				"variant", r.VariantID, "site", r.SiteID, "quantity", r.Quantity)
			continue
		}
		out = append(out, openPurchase{variantID: r.VariantID, siteID: r.SiteID, quantity: q})
	}
	return out
}

// openPurchaseColumns is every open purchase as the three arrays the
// low-stock statements read side by side.
func (s *server) openPurchaseColumns(ctx context.Context) ([]int32, []int64, []string, error) {
	rows, err := s.openPurchasesAll(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	variants, sites, quantities := make([]int32, 0, len(rows)), make([]int64, 0, len(rows)), make([]string, 0, len(rows))
	for _, r := range rows {
		variants, sites, quantities = append(variants, r.variantID), append(sites, r.siteID), append(quantities, decimalText(r.quantity))
	}
	return variants, sites, quantities, nil
}
