package orders

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
// transactions (Inventory's contractscalls.go, copied). lockVariants is the
// one exception, by MB rule 21: it runs ON the transaction, on the caller's
// own connection, and is the first thing a write that touches catalog lines
// does.

// errCallInsideTransaction is an accessor's answer when it is called from
// inside one of this module's transactions: a bug, answered as a 500.
var errCallInsideTransaction = errors.New("orders: a call out of the module was made inside one of its transactions")

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
		return nil, fmt.Errorf("orders: read the catalog's variants: %w", err)
	}
	for _, e := range entries {
		out[e.ID] = e
	}
	return out, nil
}

// listPrice is a variant's list price in currency at a moment (O16), nil when
// it has none, before any transaction.
func (s *server) listPrice(ctx context.Context, variantID int32, currency string, at time.Time) (*contracts.Money, error) {
	if err := noteContractCall(ctx, "Products.ListPrice"); err != nil {
		return nil, err
	}
	if s.deps.Products == nil {
		return nil, errDependencyUnavailable
	}
	m, err := s.deps.Products.ListPrice(ctx, variantID, currency, at)
	if err != nil {
		return nil, fmt.Errorf("orders: read variant %d's list price: %w", variantID, err)
	}
	return m, nil
}

// lockVariants takes FOR SHARE on the variants and their products ON tx, in
// variant id order (MB rule 21): the one call out of the module made inside a
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

// taxCodes maps Products' tax categories to Invoices' VAT codes valid at at
// (O7, O16), before any transaction; an unmapped category is absent.
func (s *server) taxCodes(ctx context.Context, categories []int32, at time.Time) (map[int32]contracts.VATCodeEntry, error) {
	out := map[int32]contracts.VATCodeEntry{}
	if len(categories) == 0 {
		return out, nil
	}
	if err := noteContractCall(ctx, "SalesTax.ForTaxCategories"); err != nil {
		return nil, err
	}
	if s.deps.SalesTax == nil {
		return nil, errDependencyUnavailable
	}
	m, err := s.deps.SalesTax.ForTaxCategories(ctx, categories, at)
	if err != nil {
		return nil, fmt.Errorf("orders: map the tax categories to VAT codes: %w", err)
	}
	return m, nil
}

// vatCodes is every active VAT code with its rate at at, before any
// transaction: a free-text line's picker and its default (O16).
func (s *server) vatCodes(ctx context.Context, at time.Time) ([]contracts.VATCodeEntry, error) {
	if err := noteContractCall(ctx, "SalesTax.VATCodes"); err != nil {
		return nil, err
	}
	if s.deps.SalesTax == nil {
		return nil, errDependencyUnavailable
	}
	codes, err := s.deps.SalesTax.VATCodes(ctx, at)
	if err != nil {
		return nil, fmt.Errorf("orders: read the VAT codes: %w", err)
	}
	return codes, nil
}

// seller is the seller block Invoices prints (O24), before any transaction.
// Worker mode composes no sales tax, so a worker reads an empty seller.
func (s *server) seller(ctx context.Context) (contracts.SellerProfile, error) {
	if err := noteContractCall(ctx, "SalesTax.Seller"); err != nil {
		return contracts.SellerProfile{}, err
	}
	if s.deps.SalesTax == nil {
		return contracts.SellerProfile{}, nil
	}
	p, err := s.deps.SalesTax.Seller(ctx)
	if err != nil {
		return contracts.SellerProfile{}, fmt.Errorf("orders: read the seller: %w", err)
	}
	return p, nil
}

// billingProfile is the customer as a document snapshots it (O13), with its
// delivery addresses and contacts, before any transaction; nil when the
// customer does not exist.
func (s *server) billingProfile(ctx context.Context, customerID int32) (*contracts.CustomerBillingProfile, error) {
	if err := noteContractCall(ctx, "Directory.BillingProfile"); err != nil {
		return nil, err
	}
	if s.deps.Directory == nil {
		return nil, errDependencyUnavailable
	}
	p, err := s.deps.Directory.BillingProfile(ctx, customerID, contracts.BillingProfileOptions{WithDelivery: true, WithContacts: true})
	if err != nil {
		return nil, fmt.Errorf("orders: read customer %d's billing profile: %w", customerID, err)
	}
	return p, nil
}

// project is a project, checked to be the document's customer's (O13),
// before any transaction; nil when it does not exist. Without Projects every
// project id is unknown.
func (s *server) project(ctx context.Context, id int32) (*contracts.ProjectEntry, error) {
	if err := noteContractCall(ctx, "Projects.Project"); err != nil {
		return nil, err
	}
	if s.deps.Projects == nil {
		return nil, nil
	}
	p, err := s.deps.Projects.Project(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("orders: read project %d: %w", id, err)
	}
	return p, nil
}

// user is identity's user, the salesperson's and an actor's display name,
// before any transaction; nil when it does not exist.
func (s *server) user(ctx context.Context, id uuid.UUID) (*contracts.UserEntry, error) {
	if err := noteContractCall(ctx, "Users.User"); err != nil {
		return nil, err
	}
	if s.deps.Users == nil {
		return nil, nil
	}
	u, err := s.deps.Users.User(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("orders: read user %s: %w", id, err)
	}
	return u, nil
}

// businessZone is the workspace's business time zone, before any
// transaction: the zone "today" — a list price's day, a quote's valid-until —
// is judged in.
func (s *server) businessZone(ctx context.Context) (*time.Location, error) {
	if err := noteContractCall(ctx, "Calendar.Zone"); err != nil {
		return nil, err
	}
	if s.deps.Calendar == nil {
		return time.UTC, nil
	}
	zone, err := s.deps.Calendar.Zone(ctx)
	if err != nil {
		return nil, fmt.Errorf("orders: read the business time zone: %w", err)
	}
	return zone, nil
}

// outboundChannel is the chosen channel as Communications answers it (O25,
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

// objectPut, objectGet and objectExists are the object store's calls (O24),
// each outside any transaction.
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
