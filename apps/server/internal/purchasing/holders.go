package purchasing

// What Purchasing answers inside other modules' transactions (P7, P10, A8,
// A17, A23; module-boundaries rules 20, 27 and 28), each on the caller's tx
// with this module's own SQL, reading no contract, never leaving the
// process, and composed whatever MODULES says:
//
//   - the variant holder: a variant on any supplier item or order line is not
//     deleted; a product with an open goods line of a draft or a sent order
//     does not become a service; a tracking change is never held.
//   - the supplier holder: any order, drafts included, or supplier item
//     naming a supplier holds its delete (documents); a draft, sent or
//     partially received order its anonymisation (open_documents).
//   - the supplier's personal data: the export answers the orders' snapshots
//     naming the supplier; the erase blanks them, the mailings' addresses
//     and messages, marks the stored PDFs erased and writes their keys to
//     the deletion ledger the purchasing.deletions worker finishes.

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/store"
)

// variantReferenceHolder is the module's contracts.VariantReferenceHolder.
type variantReferenceHolder struct{}

var _ contracts.VariantReferenceHolder = variantReferenceHolder{}

func newVariantReferenceHolder(module.Deps) contracts.VariantReferenceHolder {
	return variantReferenceHolder{}
}

func (variantReferenceHolder) VariantHeld(ctx context.Context, tx pgx.Tx, variantID int32, purpose contracts.VariantHoldPurpose) (*contracts.VariantHold, error) {
	q := store.New(tx)
	var n int32
	var err error
	switch purpose {
	case contracts.VariantHoldDelete:
		n, err = q.VariantDocuments(ctx, variantID)
	case contracts.VariantHoldTypeChange:
		n, err = q.VariantOpenGoodsOrders(ctx, variantID)
	default:
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("purchasing: read what holds variant %d: %w", variantID, err)
	}
	if n == 0 {
		return nil, nil
	}
	return &contracts.VariantHold{Kind: contracts.VariantHoldKindOpenDocuments, Documents: int(n)}, nil
}

// supplierReferenceHolder is the module's contracts.SupplierReferenceHolder.
type supplierReferenceHolder struct{}

var _ contracts.SupplierReferenceHolder = supplierReferenceHolder{}

func newSupplierReferenceHolder(module.Deps) contracts.SupplierReferenceHolder {
	return supplierReferenceHolder{}
}

func (supplierReferenceHolder) SupplierHeld(ctx context.Context, tx pgx.Tx, supplierID int64, purpose contracts.SupplierHoldPurpose) (*contracts.SupplierHold, error) {
	q := store.New(tx)
	var n int32
	var err error
	kind := contracts.SupplierHoldKindDocuments
	switch purpose {
	case contracts.SupplierHoldDelete:
		n, err = q.SupplierDocuments(ctx, supplierID)
	case contracts.SupplierHoldAnonymise:
		n, err = q.SupplierOpenOrders(ctx, supplierID)
		kind = contracts.SupplierHoldKindOpenDocuments
	default:
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("purchasing: read what holds supplier %d: %w", supplierID, err)
	}
	if n == 0 {
		return nil, nil
	}
	return &contracts.SupplierHold{Kind: kind, Documents: int(n)}, nil
}

// supplierPersonalData is the module's contracts.SupplierPersonalData.
type supplierPersonalData struct {
	deps  module.Deps
	clock func() time.Time
}

var _ contracts.SupplierPersonalData = supplierPersonalData{}

func newSupplierPersonalData(d module.Deps) contracts.SupplierPersonalData {
	clock := d.Clock
	if clock == nil {
		clock = time.Now
	}
	return supplierPersonalData{deps: d, clock: clock}
}

// exportedOrders is the export's section (P7): the orders naming the
// supplier with what each kept of it, and where each was mailed.
type exportedOrders struct {
	Orders []exportedOrder `json:"orders"`
}

type exportedOrder struct {
	ID                 int64             `json:"id"`
	Number             *int64            `json:"number,omitempty"`
	Status             string            `json:"status"`
	OrderDate          string            `json:"orderDate"`
	SupplierName       string            `json:"supplierName"`
	OrganisationNumber string            `json:"organisationNumber,omitempty"`
	Address            string            `json:"address,omitempty"`
	PostalCode         string            `json:"postalCode,omitempty"`
	City               string            `json:"city,omitempty"`
	Country            string            `json:"country,omitempty"`
	Email              string            `json:"email,omitempty"`
	Contact            string            `json:"contact,omitempty"`
	Mailings           []exportedMailing `json:"mailings,omitempty"`
}

type exportedMailing struct {
	Kind      string     `json:"kind"`
	Version   int32      `json:"version"`
	Recipient string     `json:"recipient"`
	Cc        string     `json:"cc,omitempty"`
	Status    string     `json:"status"`
	SentAt    *time.Time `json:"sentAt,omitempty"`
}

// ExportSupplierData answers the orders' snapshots naming the supplier, nil
// when none does.
func (p supplierPersonalData) ExportSupplierData(ctx context.Context, supplierID int64) (any, error) {
	if p.deps.Pool == nil {
		return nil, nil
	}
	q := store.New(p.deps.Pool)
	orders, err := q.SupplierOrders(ctx, supplierID)
	if err != nil {
		return nil, fmt.Errorf("purchasing: read supplier %d's orders: %w", supplierID, err)
	}
	if len(orders) == 0 {
		return nil, nil
	}
	mailings, err := q.SupplierMailings(ctx, supplierID)
	if err != nil {
		return nil, fmt.Errorf("purchasing: read supplier %d's mailings: %w", supplierID, err)
	}
	byOrder := map[int64][]exportedMailing{}
	for _, m := range mailings {
		byOrder[m.OrderID] = append(byOrder[m.OrderID], exportedMailing{Kind: m.Kind, Version: m.Version, Recipient: m.Recipient,
			Cc: m.Cc, Status: m.Status, SentAt: m.SentAt})
	}
	out := exportedOrders{Orders: make([]exportedOrder, 0, len(orders))}
	for _, o := range orders {
		out.Orders = append(out.Orders, exportedOrder{
			ID: o.ID, Number: o.Number, Status: o.Status, OrderDate: o.OrderDate.Time.Format(time.DateOnly),
			SupplierName: o.SupplierName, OrganisationNumber: deref(o.SupplierOrganisationNumber), Address: deref(o.SupplierAddressLine),
			PostalCode: deref(o.SupplierPostalCode), City: deref(o.SupplierCity), Country: deref(o.SupplierCountry),
			Email: deref(o.SupplierEmail), Contact: o.SupplierContact, Mailings: byOrder[o.ID],
		})
	}
	return out, nil
}

// The kinds the erase reports.
const (
	erasedOrders    = "purchasing.orders"
	erasedMailings  = "purchasing.mailings"
	erasedDocuments = "purchasing.documents"
)

// EraseSupplierData blanks what the orders keep of the supplier, inside
// Accounting's anonymisation transaction, and writes the stored PDFs to the
// deletion ledger (A8): the objects go after the commit, by the
// purchasing.deletions worker, never inside tx.
func (p supplierPersonalData) EraseSupplierData(ctx context.Context, tx pgx.Tx, supplierID int64) ([]contracts.ErasedData, error) {
	q := store.New(tx)
	now := p.clock()
	orders, err := q.EraseSupplierOrders(ctx, store.EraseSupplierOrdersParams{SupplierID: supplierID, Now: &now})
	if err != nil {
		return nil, fmt.Errorf("purchasing: erase supplier %d's orders: %w", supplierID, err)
	}
	mailings, err := q.EraseSupplierMailings(ctx, supplierID)
	if err != nil {
		return nil, fmt.Errorf("purchasing: erase supplier %d's mailings: %w", supplierID, err)
	}
	keys, err := q.EraseSupplierDocuments(ctx, store.EraseSupplierDocumentsParams{SupplierID: supplierID, Now: &now})
	if err != nil {
		return nil, fmt.Errorf("purchasing: erase supplier %d's PDFs: %w", supplierID, err)
	}
	for _, key := range keys {
		if err := q.InsertDeletion(ctx, store.InsertDeletionParams{StorageKey: key, RequestedAt: now}); err != nil {
			return nil, fmt.Errorf("purchasing: write %s to the deletion ledger: %w", key, err)
		}
	}
	if p.deps.Logger != nil && len(keys) > 0 {
		p.deps.Logger.InfoContext(ctx, "purchasing: stored PDFs written to the deletion ledger", slog.Int64("supplier_id", supplierID), slog.Int("documents", len(keys)))
	}
	return []contracts.ErasedData{
		{Kind: erasedOrders, Count: orders},
		{Kind: erasedMailings, Count: mailings},
		{Kind: erasedDocuments, Count: int64(len(keys))},
	}, nil
}
