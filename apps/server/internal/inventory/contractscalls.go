package inventory

import (
	"context"
	"errors"
	"fmt"
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
