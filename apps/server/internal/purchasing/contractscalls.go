package purchasing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/mail"
)

// Every call out of the module goes through one accessor here, so the rule
// the transaction marks carry is checked in one place: no call that takes its
// own connection, or leaves the process, is made inside one of this module's
// transactions (Orders' contractscalls.go, copied). lockVariants and
// lockSuppliers are the two exceptions, by MB rules 21 and 28: they run ON
// the transaction, on the caller's own connection — the catalog's first, the
// supplier's second, before any row of this module's own.

// errCallInsideTransaction is an accessor's answer when it is called from
// inside one of this module's transactions: a bug, answered as a 500.
var errCallInsideTransaction = errors.New("purchasing: a call out of the module was made inside one of its transactions")

// contractCallHook is the tests' window on every call out of the module
// (SetContractCallHook): the harness records any made inside a transaction.
var contractCallHook func(ctx context.Context, method string)

// The names the hook hears the two in-transaction calls by.
const (
	lockVariantsCall  = "Products.LockVariants"
	lockSuppliersCall = "SupplierDirectory.LockSuppliers"
)

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

// lockVariants takes FOR SHARE on the variants and their products ON tx, in
// variant id order (MB rule 21), and answers the entries by id. It is the
// first thing a write that names variants does.
func (s *server) lockVariants(ctx context.Context, tx pgx.Tx, ids []int32) (map[int32]contracts.VariantEntry, error) {
	if contractCallHook != nil {
		contractCallHook(ctx, lockVariantsCall)
	}
	if s.deps.Products == nil {
		return nil, errDependencyUnavailable
	}
	out := map[int32]contracts.VariantEntry{}
	ids = distinctSorted(ids)
	if len(ids) == 0 {
		return out, nil
	}
	entries, err := s.deps.Products.LockVariants(ctx, tx, ids)
	if err != nil {
		return nil, fmt.Errorf("purchasing: lock the variants: %w", err)
	}
	for _, e := range entries {
		out[e.ID] = e
	}
	return out, nil
}

// lockSuppliers takes FOR SHARE on the suppliers ON tx (MB rule 28), after
// lockVariants and before this module's own rows, and answers their entries
// by id.
func (s *server) lockSuppliers(ctx context.Context, tx pgx.Tx, ids ...int64) (map[int64]contracts.SupplierEntry, error) {
	if contractCallHook != nil {
		contractCallHook(ctx, lockSuppliersCall)
	}
	if s.deps.SupplierDirectory == nil {
		return nil, errDependencyUnavailable
	}
	entries, err := s.deps.SupplierDirectory.LockSuppliers(ctx, tx, ids)
	if err != nil {
		return nil, fmt.Errorf("purchasing: lock the suppliers: %w", err)
	}
	out := make(map[int64]contracts.SupplierEntry, len(entries))
	for _, e := range entries {
		out[e.ID] = e
	}
	return out, nil
}

// supplier is one supplier of the register, nil when none has the id, before
// any transaction.
func (s *server) supplier(ctx context.Context, id int64) (*contracts.SupplierEntry, error) {
	if err := noteContractCall(ctx, "SupplierDirectory.Supplier"); err != nil {
		return nil, err
	}
	if s.deps.SupplierDirectory == nil {
		return nil, errDependencyUnavailable
	}
	e, err := s.deps.SupplierDirectory.Supplier(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("purchasing: read supplier %d: %w", id, err)
	}
	return e, nil
}

// suppliers is the suppliers named, by id, before any transaction.
func (s *server) suppliers(ctx context.Context, ids []int64) (map[int64]contracts.SupplierEntry, error) {
	out := map[int64]contracts.SupplierEntry{}
	ids = distinctSorted(ids)
	if len(ids) == 0 {
		return out, nil
	}
	if err := noteContractCall(ctx, "SupplierDirectory.Suppliers"); err != nil {
		return nil, err
	}
	if s.deps.SupplierDirectory == nil {
		return nil, errDependencyUnavailable
	}
	for start := 0; start < len(ids); start += contracts.MaxSuppliers {
		end := min(start+contracts.MaxSuppliers, len(ids))
		entries, err := s.deps.SupplierDirectory.Suppliers(ctx, ids[start:end])
		if err != nil {
			return nil, fmt.Errorf("purchasing: read the suppliers: %w", err)
		}
		for _, e := range entries {
			out[e.ID] = e
		}
	}
	return out, nil
}

// searchSuppliers is the register's search, before any transaction.
func (s *server) searchSuppliers(ctx context.Context, q string, limit int) ([]contracts.SupplierEntry, error) {
	if err := noteContractCall(ctx, "SupplierDirectory.SearchSuppliers"); err != nil {
		return nil, err
	}
	if s.deps.SupplierDirectory == nil {
		return nil, errDependencyUnavailable
	}
	out, err := s.deps.SupplierDirectory.SearchSuppliers(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("purchasing: search the suppliers: %w", err)
	}
	return out, nil
}

// catalogVariants is the catalog's batch read, before any transaction. An id
// the catalog does not know is absent.
func (s *server) catalogVariants(ctx context.Context, ids []int32) (map[int32]contracts.VariantEntry, error) {
	out := map[int32]contracts.VariantEntry{}
	ids = distinctSorted(ids)
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
		return nil, fmt.Errorf("purchasing: read the catalog's variants: %w", err)
	}
	for _, e := range entries {
		out[e.ID] = e
	}
	return out, nil
}

// searchVariants is the catalog's search for a line (P16), before any
// transaction.
func (s *server) searchVariants(ctx context.Context, q string, search contracts.VariantSearch) ([]contracts.VariantEntry, error) {
	if err := noteContractCall(ctx, "Products.SearchVariants"); err != nil {
		return nil, err
	}
	if s.deps.Products == nil {
		return nil, errDependencyUnavailable
	}
	v, err := s.deps.Products.SearchVariants(ctx, q, search)
	if err != nil {
		return nil, fmt.Errorf("purchasing: search the catalog: %w", err)
	}
	return v, nil
}

// lookupSKUs and lookupCode match an import's codes (P14), before any
// transaction.
func (s *server) lookupSKUs(ctx context.Context, skus []string) (map[string][]contracts.VariantEntry, error) {
	if len(skus) == 0 {
		return map[string][]contracts.VariantEntry{}, nil
	}
	if err := noteContractCall(ctx, "Products.LookupSKUs"); err != nil {
		return nil, err
	}
	if s.deps.Products == nil {
		return nil, errDependencyUnavailable
	}
	m, err := s.deps.Products.LookupSKUs(ctx, skus)
	if err != nil {
		return nil, fmt.Errorf("purchasing: look the SKUs up: %w", err)
	}
	return m, nil
}

func (s *server) lookupCode(ctx context.Context, code string) ([]contracts.CodeMatch, error) {
	if err := noteContractCall(ctx, "Products.LookupCode"); err != nil {
		return nil, err
	}
	if s.deps.Products == nil {
		return nil, errDependencyUnavailable
	}
	m, err := s.deps.Products.LookupCode(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("purchasing: look the code up: %w", err)
	}
	return m, nil
}

// sites is every site Inventory answers, by id, before any transaction.
func (s *server) sites(ctx context.Context) (map[int64]contracts.StockSite, []contracts.StockSite, error) {
	if err := noteContractCall(ctx, "Stock.Sites"); err != nil {
		return nil, nil, err
	}
	if s.deps.Stock == nil {
		return nil, nil, errDependencyUnavailable
	}
	list, err := s.deps.Stock.Sites(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("purchasing: read the sites: %w", err)
	}
	byID := make(map[int64]contracts.StockSite, len(list))
	for _, site := range list {
		byID[site.ID] = site
	}
	return byID, list, nil
}

// seller is our company as Invoices prints it (A19), before any
// transaction; empty when no sales tax is composed (worker mode).
func (s *server) seller(ctx context.Context) (contracts.SellerProfile, error) {
	if err := noteContractCall(ctx, "SalesTax.Seller"); err != nil {
		return contracts.SellerProfile{}, err
	}
	if s.deps.SalesTax == nil {
		return contracts.SellerProfile{}, nil
	}
	p, err := s.deps.SalesTax.Seller(ctx)
	if err != nil {
		return contracts.SellerProfile{}, fmt.Errorf("purchasing: read the seller: %w", err)
	}
	return p, nil
}

// user is identity's user, a buyer's and an actor's display name, before any
// transaction; nil when it does not exist.
func (s *server) user(ctx context.Context, id uuid.UUID) (*contracts.UserEntry, error) {
	if err := noteContractCall(ctx, "Users.User"); err != nil {
		return nil, err
	}
	if s.deps.Users == nil {
		return nil, nil
	}
	u, err := s.deps.Users.User(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("purchasing: read user %s: %w", id, err)
	}
	return u, nil
}

// permissionHolders is the active users holding key, before any transaction.
func (s *server) permissionHolders(ctx context.Context, key string) ([]contracts.Holder, error) {
	if err := noteContractCall(ctx, "Users.PermissionHolders"); err != nil {
		return nil, err
	}
	if s.deps.Users == nil {
		return nil, nil
	}
	h, err := s.deps.Users.PermissionHolders(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("purchasing: read the holders of %s: %w", key, err)
	}
	return h, nil
}

// businessZone is the workspace's business time zone, before any
// transaction: the zone today — an order's day, Late, Unconfirmed — is
// judged in.
func (s *server) businessZone(ctx context.Context) (*time.Location, error) {
	if err := noteContractCall(ctx, "Calendar.Zone"); err != nil {
		return nil, err
	}
	if s.deps.Calendar == nil {
		return time.UTC, nil
	}
	zone, err := s.deps.Calendar.Zone(ctx)
	if err != nil {
		return nil, fmt.Errorf("purchasing: read the business time zone: %w", err)
	}
	return zone, nil
}

// outboundChannel is the chosen channel as Communications answers it (P21,
// MB rule 17), before any transaction. Communications off is
// errDependencyUnavailable.
func (s *server) outboundChannel(ctx context.Context, id uuid.UUID) (contracts.OutboundChannel, error) {
	if err := noteContractCall(ctx, "OutboundMail.Channel"); err != nil {
		return contracts.OutboundChannel{}, err
	}
	if s.deps.OutboundMail == nil {
		return contracts.OutboundChannel{}, errDependencyUnavailable
	}
	return s.deps.OutboundMail.Channel(ctx, id)
}

// outboundChannels is every active channel Communications offers, before any
// transaction; none when Communications is off.
func (s *server) outboundChannels(ctx context.Context) ([]contracts.OutboundChannel, error) {
	if err := noteContractCall(ctx, "OutboundMail.Channels"); err != nil {
		return nil, err
	}
	if s.deps.OutboundMail == nil {
		return nil, nil
	}
	return s.deps.OutboundMail.Channels(ctx)
}

// outboundSend hands one mail to the channel, outside any transaction.
func (s *server) outboundSend(ctx context.Context, channel uuid.UUID, out mail.Outbound) error {
	if err := noteContractCall(ctx, "OutboundMail.Send"); err != nil {
		return err
	}
	if s.deps.OutboundMail == nil {
		return errDependencyUnavailable
	}
	return s.deps.OutboundMail.Send(ctx, channel, out)
}

// objectPut, objectGet, objectExists and objectDelete are the object store's
// calls (P21), each outside any transaction.
func (s *server) objectPut(ctx context.Context, key string, r io.Reader, contentType string) error {
	if err := noteContractCall(ctx, "Storage.Put"); err != nil {
		return err
	}
	return s.objects.Put(ctx, key, r, contentType)
}

func (s *server) objectGet(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := noteContractCall(ctx, "Storage.Get"); err != nil {
		return nil, err
	}
	return s.objects.Get(ctx, key)
}

func (s *server) objectExists(ctx context.Context, key string) (bool, error) {
	if err := noteContractCall(ctx, "Storage.Exists"); err != nil {
		return false, err
	}
	return s.objects.Exists(ctx, key)
}

func (s *server) objectDelete(ctx context.Context, key string) error {
	if err := noteContractCall(ctx, "Storage.Delete"); err != nil {
		return err
	}
	return s.objects.Delete(ctx, key)
}

// stockLocations is, per variant, the site's active locations holding it,
// before any transaction (the receive screen's default location, P24).
func (s *server) stockLocations(ctx context.Context, variantIDs []int32, siteID int64) ([]contracts.StockLocation, error) {
	if len(variantIDs) == 0 {
		return nil, nil
	}
	if err := noteContractCall(ctx, "Stock.Locations"); err != nil {
		return nil, err
	}
	if s.deps.Stock == nil {
		return nil, errDependencyUnavailable
	}
	out, err := s.deps.Stock.Locations(ctx, variantIDs, siteID)
	if err != nil {
		return nil, fmt.Errorf("purchasing: read the locations holding the variants: %w", err)
	}
	return out, nil
}

// siteLocations is every location of the site, archived ones too, before
// any transaction (P30: the locations a receipt offers are Inventory's).
func (s *server) siteLocations(ctx context.Context, siteID int64) ([]contracts.StockSiteLocation, error) {
	if err := noteContractCall(ctx, "Stock.SiteLocations"); err != nil {
		return nil, err
	}
	if s.deps.Stock == nil {
		return nil, errDependencyUnavailable
	}
	out, err := s.deps.Stock.SiteLocations(ctx, siteID)
	if err != nil {
		return nil, fmt.Errorf("purchasing: read site %d's locations: %w", siteID, err)
	}
	return out, nil
}
