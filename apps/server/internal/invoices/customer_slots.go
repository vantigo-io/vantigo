package invoices

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is the two many-provider slots every module holding customer ids
// implements (D10). Both run whether or not the module is enabled — every
// schema is migrated whatever MODULES says — so their constructors need
// nothing a disabled module's Deps lacks, and neither reads a contract.

// The kinds this module reports, "<module>.<what>" in the API's camelCase.
const (
	kindInvoicesInvoices   = "invoices.invoices"
	kindInvoicesDrafts     = "invoices.drafts"
	kindInvoicesDocuments  = "invoices.documents"
	kindInvoicesPayments   = "invoices.payments"
	kindInvoicesDeliveries = "invoices.deliveries"
)

// customerReferenceHolder is this module's contracts.CustomerReferenceHolder:
// when two customers are merged, every document of the absorbed one — draft
// and issued — names the survivor from then on. An issued document keeps its
// buyer snapshot: the id is not printed, the snapshot is.
type customerReferenceHolder struct {
	clock func() time.Time
}

var _ contracts.CustomerReferenceHolder = (*customerReferenceHolder)(nil)

// newCustomerReferenceHolder is Module's CustomerReferences. It takes the
// clock, as projects' does, for a draft's updated_at.
func newCustomerReferenceHolder(d module.Deps) contracts.CustomerReferenceHolder {
	return &customerReferenceHolder{clock: d.Clock}
}

// RepointCustomer moves every document of from to into, inside the caller's
// transaction. from == into writes nothing and reports zero. It locks the
// documents newest first before it writes them: the order a credit note's
// issue takes them in (LockCustomerDocuments), so the two never deadlock.
func (h *customerReferenceHolder) RepointCustomer(ctx context.Context, tx pgx.Tx, from, into int32) ([]contracts.RepointedReferences, error) {
	if from == into {
		return []contracts.RepointedReferences{{Kind: kindInvoicesInvoices, Count: 0}}, nil
	}
	q := store.New(tx)
	if err := q.LockCustomerDocuments(ctx, store.LockCustomerDocumentsParams{FromCustomerID: from, IntoCustomerID: into}); err != nil {
		return nil, fmt.Errorf("invoices: lock customer %d's and %d's documents: %w", from, into, err)
	}
	n, err := q.RepointCustomer(ctx, store.RepointCustomerParams{
		FromCustomerID: from, IntoCustomerID: into, Now: h.clock(),
	})
	if err != nil {
		return nil, fmt.Errorf("invoices: re-point customer %d's documents to %d: %w", from, into, err)
	}
	return []contracts.RepointedReferences{{Kind: kindInvoicesInvoices, Count: n}}, nil
}

// customerPersonalData is this module's contracts.CustomerPersonalData: what
// was invoiced to a private person, handed over, and on anonymisation the
// drafts erased while the issued documents stay — their payments with the
// notes blanked, their deliveries with the address blanked — and the
// customer marked erased, so no later send reaches them (D6).
type customerPersonalData struct {
	pool *pgxpool.Pool
	// clock is Deps.Clock, for the erased-customer marker's erased_at
	// (payments and delivery design D6).
	clock func() time.Time
}

var _ contracts.CustomerPersonalData = customerPersonalData{}

// newCustomerPersonalData is Module's CustomerPersonalData.
func newCustomerPersonalData(d module.Deps) contracts.CustomerPersonalData {
	return customerPersonalData{pool: d.Pool, clock: d.Clock}
}

type invoicesSection struct {
	Documents []exportedDocument `json:"documents"`
	Drafts    []exportedDocument `json:"drafts"`
}

type exportedDocument struct {
	Number          *int64           `json:"number,omitempty"`
	Kind            string           `json:"kind"`
	Credits         *exportedCredits `json:"credits,omitempty"`
	IssueDate       *string          `json:"issueDate,omitempty"`
	DeliveryDate    *string          `json:"deliveryDate,omitempty"`
	DeliveryFrom    *string          `json:"deliveryFrom,omitempty"`
	DeliveryTo      *string          `json:"deliveryTo,omitempty"`
	DeliveryAddress *exportedAddress `json:"deliveryAddress,omitempty"`
	DueDate         *string          `json:"dueDate,omitempty"`
	Currency        string           `json:"currency"`
	NetTotal        string           `json:"netTotal"`
	VatTotal        string           `json:"vatTotal"`
	GrossTotal      string           `json:"grossTotal"`
	Buyer           *exportedBuyer   `json:"buyer,omitempty"`
	YourReference   string           `json:"yourReference,omitempty"`
	OurReference    string           `json:"ourReference,omitempty"`
	OrderReference  string           `json:"orderReference,omitempty"`
	Note            string           `json:"note,omitempty"`
	InternalNote    string           `json:"internalNote,omitempty"`
	Lines           []exportedLine   `json:"lines"`
	// Payments and Deliveries are an issued document's, empty when it has
	// none; a draft has neither, and nil leaves the key out (payments and
	// delivery design D6).
	Payments   []exportedPayment  `json:"payments,omitzero"`
	Deliveries []exportedDelivery `json:"deliveries,omitzero"`
}

// exportedPayment is one registration of money received, as it was
// registered, and its removal when it was removed: a bank reference often
// names the payer, and a note is staff free text about them.
type exportedPayment struct {
	PaidOn        string     `json:"paidOn"`
	Amount        string     `json:"amount"`
	Currency      string     `json:"currency"`
	Reference     string     `json:"reference,omitempty"`
	Note          string     `json:"note,omitempty"`
	RegisteredAt  time.Time  `json:"registeredAt"`
	RemovedAt     *time.Time `json:"removedAt,omitempty"`
	RemovalReason string     `json:"removalReason,omitempty"`
}

// exportedDelivery is one e-mail that handed a document over: to whom, when
// and with what subject. The recipient is "" once the customer was
// anonymised.
type exportedDelivery struct {
	Recipient string    `json:"recipient"`
	SentAt    time.Time `json:"sentAt"`
	Subject   string    `json:"subject"`
}

// exportedCredits is the issued invoice a credit note credits, as it was
// printed: its number and issue date.
type exportedCredits struct {
	Number    *int64  `json:"number,omitempty"`
	IssueDate *string `json:"issueDate,omitempty"`
}

// exportedBuyer is an issued document's whole buyer snapshot — what it
// printed about the person, and so what is held about them for as long as
// the document is kept. An unset optional field is omitted, as the wire
// omits it.
type exportedBuyer struct {
	CustomerNumber     *int64           `json:"customerNumber,omitempty"`
	Type               string           `json:"type,omitempty"`
	Name               string           `json:"name,omitempty"`
	OrganisationNumber string           `json:"organisationNumber,omitempty"`
	ForeignID          string           `json:"foreignId,omitempty"`
	Address            *exportedAddress `json:"address,omitempty"`
	PeppolID           string           `json:"peppolId,omitempty"`
	GLN                string           `json:"gln,omitempty"`
	Language           string           `json:"language,omitempty"`
}

// exportedAddress is the buyer's address or the place of delivery, each part
// in its own field; a place of delivery has no region.
type exportedAddress struct {
	Line1      string `json:"line1,omitempty"`
	Line2      string `json:"line2,omitempty"`
	PostalCode string `json:"postalCode,omitempty"`
	City       string `json:"city,omitempty"`
	Region     string `json:"region,omitempty"`
	Country    string `json:"country,omitempty"`
}

// orEmpty is an optional column's value verbatim, "" when unset (optionalText
// trims, which an export must not).
func orEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// addressOf is an address from its columns, nil when every one is unset.
func addressOf(line1, line2, postalCode, city, region, country *string) *exportedAddress {
	a := exportedAddress{
		Line1: orEmpty(line1), Line2: orEmpty(line2), PostalCode: orEmpty(postalCode),
		City: orEmpty(city), Region: orEmpty(region), Country: orEmpty(country),
	}
	if a == (exportedAddress{}) {
		return nil
	}
	return &a
}

// buyerOf is a document's buyer snapshot: an issued document's, written at
// issue (D4), and a credit-note draft's, the one it copied from its original
// (D8); nil on an invoice draft, which has none yet.
func buyerOf(d store.InvoicesInvoice) *exportedBuyer {
	b := exportedBuyer{
		CustomerNumber: d.BuyerCustomerNumber, Type: orEmpty(d.BuyerType), Name: orEmpty(d.BuyerName),
		OrganisationNumber: orEmpty(d.BuyerOrganisationNumber), ForeignID: orEmpty(d.BuyerForeignID),
		Address: addressOf(d.BuyerAddressLine1, d.BuyerAddressLine2, d.BuyerPostalCode,
			d.BuyerCity, d.BuyerRegion, d.BuyerCountry),
		PeppolID: orEmpty(d.BuyerPeppolID), GLN: orEmpty(d.BuyerGln), Language: orEmpty(d.BuyerLanguage),
	}
	if b == (exportedBuyer{}) {
		return nil
	}
	return &b
}

type exportedLine struct {
	Description     string  `json:"description"`
	Quantity        string  `json:"quantity"`
	Unit            string  `json:"unit,omitempty"`
	UnitPrice       string  `json:"unitPrice"`
	DiscountPercent string  `json:"discountPercent"`
	VatRatePercent  *string `json:"vatRatePercent,omitempty"`
	LineNet         string  `json:"lineNet"`
}

// dateText is a date column as the API writes one, or nil.
func dateText(d pgtype.Date) *string {
	if !d.Valid {
		return nil
	}
	return ptr(d.Time.Format(time.DateOnly))
}

// decimalOf is a numeric column as exact decimal text — an export is read by
// people and programs alike, and no float rounds its money.
func decimalOf(n pgtype.Numeric, places int) (string, error) {
	r, err := ratFromNumeric(n)
	if err != nil {
		return "", err
	}
	return r.FloatString(places), nil
}

// ExportCustomerData answers nil for a customer with no document. Otherwise
// every issued document and every draft, with the buyer snapshot, the place
// of delivery and the internal note: the customers export treats
// staff-written notes as data held about the person. An issued document
// carries its payments, removed ones with their removal, and its deliveries
// (payments and delivery design D6). Every read is in one REPEATABLE READ,
// READ ONLY transaction, as the customers module reads its own part of the
// export: a payment or a send landing midway cannot make the file disagree
// with itself.
func (p customerPersonalData) ExportCustomerData(ctx context.Context, customerID int32) (any, error) {
	var section any
	err := db.WithTx(ctx, p.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var err error
		section, err = exportCustomerData(ctx, store.New(tx), customerID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return section, nil
}

// exportCustomerData is ExportCustomerData's reads and its file, with q bound
// to the export's transaction.
func exportCustomerData(ctx context.Context, q *store.Queries, customerID int32) (any, error) {
	docs, err := q.CustomerDocuments(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read customer %d's documents: %w", customerID, err)
	}
	if len(docs) == 0 {
		return nil, nil
	}
	ids := make([]int64, 0, len(docs))
	for _, d := range docs {
		ids = append(ids, d.ID)
	}
	lines, err := q.LinesOf(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("invoices: read customer %d's lines: %w", customerID, err)
	}
	byDoc := map[int64][]store.InvoicesLine{}
	for _, l := range lines {
		byDoc[l.InvoiceID] = append(byDoc[l.InvoiceID], l)
	}
	payments, err := q.PaymentsOfDocuments(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("invoices: read customer %d's payments: %w", customerID, err)
	}
	paymentsOf := map[int64][]exportedPayment{}
	for _, r := range payments {
		amount, err := decimalOf(r.Amount, 2)
		if err != nil {
			return nil, err
		}
		e := exportedPayment{
			PaidOn: orEmpty(dateText(r.PaidOn)), Amount: amount, Currency: r.Currency,
			Reference: r.Reference, Note: r.Note, RegisteredAt: r.RegisteredAt.UTC(),
			RemovalReason: orEmpty(r.RemovalReason),
		}
		if r.RemovedAt != nil {
			e.RemovedAt = ptr(r.RemovedAt.UTC())
		}
		paymentsOf[r.InvoiceID] = append(paymentsOf[r.InvoiceID], e)
	}
	deliveries, err := q.DeliveriesOfDocuments(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("invoices: read customer %d's deliveries: %w", customerID, err)
	}
	deliveriesOf := map[int64][]exportedDelivery{}
	for _, d := range deliveries {
		deliveriesOf[d.InvoiceID] = append(deliveriesOf[d.InvoiceID],
			exportedDelivery{Recipient: d.Recipient, SentAt: d.SentAt.UTC(), Subject: d.Subject})
	}
	// A credit note's customer is its original's (credits.go), so the
	// original is almost always among docs; one that is not is read.
	byID := make(map[int64]store.InvoicesInvoice, len(docs))
	for _, d := range docs {
		byID[d.ID] = d
	}
	section := invoicesSection{Documents: []exportedDocument{}, Drafts: []exportedDocument{}}
	for _, d := range docs {
		e := exportedDocument{
			Number: d.Number, Kind: d.Kind, IssueDate: dateText(d.IssueDate), DeliveryDate: dateText(d.DeliveryDate),
			DeliveryFrom: dateText(d.DeliveryFrom), DeliveryTo: dateText(d.DeliveryTo), DueDate: dateText(d.DueDate),
			DeliveryAddress: addressOf(d.DeliveryAddressLine1, d.DeliveryAddressLine2, d.DeliveryPostalCode,
				d.DeliveryCity, nil, d.DeliveryCountry),
			Currency: d.Currency, Buyer: buyerOf(d), YourReference: d.YourReference, OurReference: d.OurReference,
			OrderReference: d.OrderReference, Note: d.Note, InternalNote: d.InternalNote, Lines: []exportedLine{},
		}
		if d.CreditsInvoiceID != nil {
			original, ok := byID[*d.CreditsInvoiceID]
			if !ok {
				if original, err = q.GetInvoice(ctx, *d.CreditsInvoiceID); err != nil {
					return nil, fmt.Errorf("invoices: read credit note %d's original: %w", d.ID, err)
				}
			}
			e.Credits = &exportedCredits{Number: original.Number, IssueDate: dateText(original.IssueDate)}
		}
		for _, c := range []struct {
			dst *string
			n   pgtype.Numeric
		}{{&e.NetTotal, d.NetTotal}, {&e.VatTotal, d.VatTotal}, {&e.GrossTotal, d.GrossTotal}} {
			if *c.dst, err = decimalOf(c.n, 2); err != nil {
				return nil, err
			}
		}
		for _, l := range byDoc[d.ID] {
			line := exportedLine{Description: l.Description, Unit: l.Unit}
			for _, c := range []struct {
				dst    *string
				n      pgtype.Numeric
				places int
			}{{&line.Quantity, l.Quantity, 3}, {&line.UnitPrice, l.UnitPrice, 4}, {&line.DiscountPercent, l.DiscountPercent, 2}, {&line.LineNet, l.LineNet, 2}} {
				if *c.dst, err = decimalOf(c.n, c.places); err != nil {
					return nil, err
				}
			}
			if l.VatRatePercent.Valid {
				rate, err := decimalOf(l.VatRatePercent, 2)
				if err != nil {
					return nil, err
				}
				line.VatRatePercent = &rate
			}
			e.Lines = append(e.Lines, line)
		}
		if d.Status == statusIssued {
			e.Payments = append([]exportedPayment{}, paymentsOf[d.ID]...)
			e.Deliveries = append([]exportedDelivery{}, deliveriesOf[d.ID]...)
			section.Documents = append(section.Documents, e)
		} else {
			section.Drafts = append(section.Drafts, e)
		}
	}
	return section, nil
}

// EraseCustomerData anonymises the person in this module (payments and
// delivery design D6), in this order: it locks their documents FOR UPDATE
// newest first (the merge's statement, the issue's order), so a delivery
// insert — whose trigger takes the document FOR SHARE — waits for this
// transaction; writes the erased-customer marker, which that trigger reads
// after its wait and which refuses any later send; blanks every delivery's
// recipient; blanks every payment's note, live and removed; and deletes the
// drafts. A draft is not a salgsdokument, so it has no retention basis and
// GDPR art. 17 applies; an issued document, its buyer snapshot and its
// payments are bookkeeping material kept under bokføringsloven § 13 — five
// years after the end of the financial year — which is why
// invoices.documents reports 0 and a payment keeps its date, its amount and
// the bank's reference. A payment's note is staff free text about the
// person, which no retention rule needs: invoices.payments reports the notes
// blanked. A delivery is kept as the record of when the claim was handed to
// the mail server, its address gone. contracts.ErasedData carries no reason;
// docs/src/content/docs/en/reference/invoices.md and the anonymisation table in docs/src/content/docs/en/reference/customers.md say it.
// Run twice, it reports zeros and the marker keeps its first time.
func (p customerPersonalData) EraseCustomerData(ctx context.Context, tx pgx.Tx, customerID int32) ([]contracts.ErasedData, error) {
	q := store.New(tx)
	if err := q.LockCustomerDocuments(ctx, store.LockCustomerDocumentsParams{FromCustomerID: customerID, IntoCustomerID: customerID}); err != nil {
		return nil, fmt.Errorf("invoices: lock customer %d's documents: %w", customerID, err)
	}
	if err := q.MarkCustomerErased(ctx, store.MarkCustomerErasedParams{CustomerID: customerID, ErasedAt: p.clock()}); err != nil {
		return nil, fmt.Errorf("invoices: mark customer %d erased: %w", customerID, err)
	}
	blanked, err := q.BlankCustomerDeliveries(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("invoices: blank customer %d's deliveries: %w", customerID, err)
	}
	notes, err := q.BlankCustomerPaymentNotes(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("invoices: blank customer %d's payment notes: %w", customerID, err)
	}
	drafts, err := q.DeleteCustomerDrafts(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("invoices: erase customer %d's drafts: %w", customerID, err)
	}
	return []contracts.ErasedData{
		{Kind: kindInvoicesDrafts, Count: drafts},
		{Kind: kindInvoicesDocuments, Count: 0},
		{Kind: kindInvoicesPayments, Count: notes},
		{Kind: kindInvoicesDeliveries, Count: blanked},
	}, nil
}
