package orders

// The other modules' holders (orders design O29, O30; module-boundaries rules
// 8, 9 and 20). Each runs its own SQL on this module's schema, on the
// caller's transaction where it is handed one, never begins or ends a
// transaction, reads no contract, and runs whether or not Orders is enabled:
// each is built from the pool and the clock alone.
//
// A user's erasure (MB rule 19) is not one of them: the salesperson and every
// actor are kept on quotes, orders, mailings and the change log with their id
// and display name, as records of an agreement — Inventory's default for its
// ledger — so no sweep against contracts.UserDirectory runs here.

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// The kinds the holders report (contracts.RepointedReferences,
// contracts.ErasedData).
const (
	kindOrdersQuotes      = "orders.quotes"
	kindOrdersOrders      = "orders.orders"
	kindOrdersDraftOrders = "orders.draftOrders"
	kindOrdersMailings    = "orders.mailings"
)

// customerReferenceHolder re-points a merged-away customer's quotes and
// orders (O29).
type customerReferenceHolder struct{}

var _ contracts.CustomerReferenceHolder = customerReferenceHolder{}

func newCustomerReferenceHolder(module.Deps) contracts.CustomerReferenceHolder {
	return customerReferenceHolder{}
}

// RepointCustomer moves every quote and order of from to into, inside the
// merge's transaction. The snapshot is not touched: a frozen document keeps
// the customer as it was. A write of a document holds only its own row (after
// its variants'), so the merge waits for it, and never the other way round.
func (customerReferenceHolder) RepointCustomer(ctx context.Context, tx pgx.Tx, from, into int32) ([]contracts.RepointedReferences, error) {
	if from == into {
		return []contracts.RepointedReferences{{Kind: kindOrdersQuotes}, {Kind: kindOrdersOrders}}, nil
	}
	q := store.New(tx)
	quotes, err := q.RepointCustomerQuotes(ctx, store.RepointCustomerQuotesParams{FromCustomerID: from, IntoCustomerID: into})
	if err != nil {
		return nil, fmt.Errorf("orders: re-point customer %d's quotes to %d: %w", from, into, err)
	}
	orders, err := q.RepointCustomerOrders(ctx, store.RepointCustomerOrdersParams{FromCustomerID: from, IntoCustomerID: into})
	if err != nil {
		return nil, fmt.Errorf("orders: re-point customer %d's orders to %d: %w", from, into, err)
	}
	return []contracts.RepointedReferences{{Kind: kindOrdersQuotes, Count: quotes}, {Kind: kindOrdersOrders, Count: orders}}, nil
}

// customerPersonalData is what Orders holds about a person (O29): handed
// over, and on an erase taken out of every quote and every order that is no
// record at once, and out of a confirmed order once its retention has run
// (O54, the orders.retention worker).
type customerPersonalData struct {
	pool   *pgxpool.Pool
	clock  func() time.Time
	logger *slog.Logger
}

var _ contracts.CustomerPersonalData = customerPersonalData{}

func newCustomerPersonalData(d module.Deps) contracts.CustomerPersonalData {
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return customerPersonalData{pool: d.Pool, clock: d.Clock, logger: logger}
}

// The export's section: every quote and order of the customer, each with its
// snapshot, its lines and its sends.
type exportedOrders struct {
	Quotes []exportedDocument `json:"quotes"`
	Orders []exportedDocument `json:"orders"`
}

type exportedDocument struct {
	Number          int64             `json:"number"`
	Status          string            `json:"status"`
	Revision        int32             `json:"revision,omitempty"`
	Version         int32             `json:"version,omitempty"`
	Currency        string            `json:"currency"`
	NetTotal        string            `json:"netTotal"`
	GrossTotal      string            `json:"grossTotal"`
	CustomerName    string            `json:"customerName"`
	InvoiceAddress  string            `json:"invoiceAddress,omitempty"`
	DeliveryAddress string            `json:"deliveryAddress,omitempty"`
	Contact         string            `json:"contact,omitempty"`
	InvoiceEmail    string            `json:"invoiceEmail,omitempty"`
	YourReference   string            `json:"yourReference,omitempty"`
	IntroText       string            `json:"introText,omitempty"`
	ClosingText     string            `json:"closingText,omitempty"`
	InternalNote    string            `json:"internalNote,omitempty"`
	CreatedAt       time.Time         `json:"createdAt"`
	Lines           []exportedLine    `json:"lines"`
	Mailings        []exportedMailing `json:"mailings"`
}

type exportedLine struct {
	Kind        string `json:"kind"`
	SKU         string `json:"sku,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Quantity    string `json:"quantity,omitempty"`
	Unit        string `json:"unit,omitempty"`
	UnitPrice   string `json:"unitPrice,omitempty"`
	Discount    string `json:"discountPercent,omitempty"`
	LineNet     string `json:"lineNet"`
	Delivered   string `json:"delivered,omitempty"`
}

type exportedMailing struct {
	Recipient string     `json:"recipient"`
	Cc        string     `json:"cc,omitempty"`
	Subject   string     `json:"subject"`
	Status    string     `json:"status"`
	SentAt    *time.Time `json:"sentAt,omitempty"`
}

// ExportCustomerData answers nil for a customer with no quote and no order;
// otherwise every one with its lines and sends, read in one REPEATABLE READ,
// READ ONLY snapshot.
func (p customerPersonalData) ExportCustomerData(ctx context.Context, customerID int32) (any, error) {
	var section *exportedOrders
	err := pgx.BeginTxFunc(ctx, p.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		q := store.New(tx)
		quotes, err := q.CustomerQuotes(ctx, customerID)
		if err != nil {
			return fmt.Errorf("orders: read customer %d's quotes: %w", customerID, err)
		}
		orders, err := q.CustomerOrders(ctx, customerID)
		if err != nil {
			return fmt.Errorf("orders: read customer %d's orders: %w", customerID, err)
		}
		if len(quotes) == 0 && len(orders) == 0 {
			return nil
		}
		section = &exportedOrders{Quotes: []exportedDocument{}, Orders: []exportedDocument{}}
		quoteDocs := make([]exportedDocument, 0, len(quotes))
		quoteIDs := make([]int64, 0, len(quotes))
		for _, r := range quotes {
			quoteIDs = append(quoteIDs, r.ID)
			d := exportedOf(headerOfQuote(r), r.Number, r.Status, r.Currency, r.NetTotal, r.GrossTotal, r.CreatedAt)
			d.Revision = r.Revision
			quoteDocs = append(quoteDocs, d)
		}
		orderDocs := make([]exportedDocument, 0, len(orders))
		orderIDs := make([]int64, 0, len(orders))
		for _, r := range orders {
			orderIDs = append(orderIDs, r.ID)
			d := exportedOf(headerOfOrder(r), r.Number, r.Status, r.Currency, r.NetTotal, r.GrossTotal, r.CreatedAt)
			d.Version = r.Version
			orderDocs = append(orderDocs, d)
		}
		if section.Quotes, err = withLinesAndMailings(ctx, q, ownerQuote, quoteIDs, quoteDocs); err != nil {
			return err
		}
		section.Orders, err = withLinesAndMailings(ctx, q, ownerOrder, orderIDs, orderDocs)
		return err
	})
	if err != nil || section == nil {
		return nil, err
	}
	return section, nil
}

// exportedOf is a document's snapshot as the export writes it.
func exportedOf(h header, number int64, status, currency string, net, gross pgtype.Numeric, created time.Time) exportedDocument {
	return exportedDocument{
		Number: number, Status: status, Currency: currency,
		NetTotal: ratOrZero(net).FloatString(amountPlaces), GrossTotal: ratOrZero(gross).FloatString(amountPlaces),
		CustomerName: h.CustomerName,
		InvoiceAddress: joinText(deref(h.InvoiceAddressLine1), deref(h.InvoiceAddressLine2), deref(h.InvoicePostalCode),
			deref(h.InvoiceCity), deref(h.InvoiceCountry)),
		DeliveryAddress: joinText(deref(h.DeliveryName), deref(h.DeliveryAddressLine1), deref(h.DeliveryAddressLine2),
			deref(h.DeliveryPostalCode), deref(h.DeliveryCity), deref(h.DeliveryCountry)),
		Contact:      joinText(deref(h.ContactName), deref(h.ContactEmail), deref(h.ContactPhone)),
		InvoiceEmail: deref(h.CustomerInvoiceEmail), YourReference: h.YourReference,
		IntroText: h.IntroText, ClosingText: h.ClosingText, InternalNote: h.InternalNote, CreatedAt: created.UTC(),
		Lines: []exportedLine{}, Mailings: []exportedMailing{},
	}
}

// joinText joins the parts that are not empty with ", ".
func joinText(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ", ")
}

// withLinesAndMailings fills docs, in the order of ids, with their lines and
// sends.
func withLinesAndMailings(ctx context.Context, q *store.Queries, kind string, ids []int64, docs []exportedDocument) ([]exportedDocument, error) {
	if len(ids) == 0 {
		return docs, nil
	}
	at := map[int64]int{}
	for i, id := range ids {
		at[id] = i
	}
	lines, err := q.LinesOfOwners(ctx, store.LinesOfOwnersParams{OwnerKind: kind, OwnerIds: ids})
	if err != nil {
		return nil, fmt.Errorf("orders: read the %ss' lines: %w", kind, err)
	}
	for _, l := range lines {
		e := exportedLine{Kind: l.Kind, SKU: deref(l.Sku), Name: deref(l.Name), Description: l.Description, Unit: l.Unit,
			LineNet: ratOrZero(l.LineNet).FloatString(amountPlaces)}
		if v := ratOf(l.Quantity); v != nil {
			e.Quantity = v.FloatString(quantityPlaces)
		}
		if v := ratOf(l.UnitPrice); v != nil {
			e.UnitPrice = v.FloatString(entryPlaces)
		}
		if v := ratOrZero(l.DiscountPercent); v.Sign() > 0 {
			e.Discount = v.FloatString(discountPlaces)
		}
		if kind == ownerOrder {
			if v := ratOrZero(l.QtyDelivered); v.Sign() > 0 {
				e.Delivered = v.FloatString(quantityPlaces)
			}
		}
		i := at[l.OwnerID]
		docs[i].Lines = append(docs[i].Lines, e)
	}
	mailings, err := q.MailingsOfOwners(ctx, store.MailingsOfOwnersParams{OwnerKind: kind, OwnerIds: ids})
	if err != nil {
		return nil, fmt.Errorf("orders: read the %ss' sends: %w", kind, err)
	}
	for _, m := range mailings {
		i := at[m.OwnerID]
		docs[i].Mailings = append(docs[i].Mailings, exportedMailing{Recipient: m.Recipient, Cc: m.Cc, Subject: m.Subject, Status: m.Status, SentAt: m.SentAt})
	}
	return docs, nil
}

// EraseCustomerData takes the person out of Orders (O29, O54), inside the
// customers module's anonymisation transaction: it locks their quotes, then
// their orders, FOR UPDATE in id order; blanks the name, the legal ids, the
// addresses, the contact, the invoice e-mail, your reference and the internal
// note on every quote — a quote is offer text, no bookkeeping record — and on
// every order never confirmed; blanks the recipient and the copies of every
// send of theirs and fails a queued one; and, when a confirmed order is left,
// records the erasure, so the orders.retention worker anonymises it once
// bokføringsloven § 13 no longer keeps it: 3 years and 6 months after the end
// of the financial year the order was done or cancelled in. Until then the
// confirmed order keeps its snapshot, as Invoices keeps an issued invoice's;
// orders.orders reports 0, and the order numbers kept are logged — the
// answer carries counts only. Run again, it reports zeros.
//
// The stored PDFs print the person too. A confirmed order's are bookkeeping
// material and are kept with it through its retention; a quote's — a sent
// quote's, the only quotes that have any — are no record and go with the
// quote. Deleting an object is a call out of the database, which this
// transaction may not make, so the erase records the customer whenever a
// stored PDF of theirs exists, and the orders.retention worker deletes the
// stored PDFs (objects and orders.documents rows) of every anonymised quote
// and order of theirs on its next cycle: the quotes' at once, a confirmed
// order's once its retention has run and it was anonymised.
func (p customerPersonalData) EraseCustomerData(ctx context.Context, tx pgx.Tx, customerID int32) ([]contracts.ErasedData, error) {
	now := p.clock()
	q := store.New(tx)
	if err := q.LockCustomerQuotes(ctx, customerID); err != nil {
		return nil, fmt.Errorf("orders: lock customer %d's quotes: %w", customerID, err)
	}
	if err := q.LockCustomerOrders(ctx, customerID); err != nil {
		return nil, fmt.Errorf("orders: lock customer %d's orders: %w", customerID, err)
	}
	quotes, err := q.AnonymiseCustomerQuotes(ctx, store.AnonymiseCustomerQuotesParams{CustomerID: customerID, Now: &now})
	if err != nil {
		return nil, fmt.Errorf("orders: anonymise customer %d's quotes: %w", customerID, err)
	}
	drafts, err := q.AnonymiseCustomerOrders(ctx, store.AnonymiseCustomerOrdersParams{CustomerID: customerID, Now: &now})
	if err != nil {
		return nil, fmt.Errorf("orders: anonymise customer %d's draft orders: %w", customerID, err)
	}
	mailings, err := q.BlankCustomerMailings(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("orders: blank customer %d's sends: %w", customerID, err)
	}
	kept, err := q.KeptOrderNumbers(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("orders: read customer %d's kept orders: %w", customerID, err)
	}
	documents, err := q.CustomerHasDocuments(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("orders: read whether customer %d has stored PDFs: %w", customerID, err)
	}
	if len(kept) > 0 || documents {
		if err := q.RecordErasure(ctx, store.RecordErasureParams{CustomerID: customerID, RequestedAt: now}); err != nil {
			return nil, fmt.Errorf("orders: record customer %d's erasure: %w", customerID, err)
		}
	}
	if len(kept) > 0 {
		p.logger.InfoContext(ctx, "orders: confirmed orders are kept as bookkeeping material until their retention has run (bokføringsloven § 13); orders.retention anonymises them then",
			"customer_id", customerID, "order_numbers", kept)
	}
	return []contracts.ErasedData{
		{Kind: kindOrdersQuotes, Count: quotes},
		{Kind: kindOrdersDraftOrders, Count: drafts},
		{Kind: kindOrdersOrders, Count: 0},
		{Kind: kindOrdersMailings, Count: mailings},
	}, nil
}

// variantReferenceHolder answers whether open documents name a variant
// (O30): a delete is refused while a draft or sent quote, or an order line not
// done, names it; a change to Service never is.
type variantReferenceHolder struct{}

var _ contracts.VariantReferenceHolder = variantReferenceHolder{}

func newVariantReferenceHolder(module.Deps) contracts.VariantReferenceHolder {
	return variantReferenceHolder{}
}

// VariantHeld counts the open documents on Products' transaction, which holds
// the variant FOR UPDATE; every writer of a line holds it FOR SHARE first
// (lockLines), so the count is true when it is read.
func (variantReferenceHolder) VariantHeld(ctx context.Context, tx pgx.Tx, variantID int32, purpose contracts.VariantHoldPurpose) (*contracts.VariantHold, error) {
	if purpose != contracts.VariantHoldDelete {
		return nil, nil
	}
	n, err := store.New(tx).OpenDocumentsOfVariant(ctx, &variantID)
	if err != nil {
		return nil, fmt.Errorf("orders: count the open documents of variant %d: %w", variantID, err)
	}
	if n == 0 {
		return nil, nil
	}
	return &contracts.VariantHold{Kind: contracts.VariantHoldKindOpenDocuments, Documents: int(n)}, nil
}
