package invoices

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is the two many-provider slots every module holding customer ids
// implements (D10). Both run whether or not the module is enabled — every
// schema is migrated whatever MODULES says — so their constructors need
// nothing a disabled module's Deps lacks, and neither reads a contract.

// The kinds this module reports, "<module>.<what>" in the API's camelCase.
const (
	kindInvoicesInvoices  = "invoices.invoices"
	kindInvoicesDrafts    = "invoices.drafts"
	kindInvoicesDocuments = "invoices.documents"
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
// drafts erased while the issued documents stay.
type customerPersonalData struct {
	pool *pgxpool.Pool
}

var _ contracts.CustomerPersonalData = customerPersonalData{}

// newCustomerPersonalData is Module's CustomerPersonalData.
func newCustomerPersonalData(d module.Deps) contracts.CustomerPersonalData {
	return customerPersonalData{pool: d.Pool}
}

type invoicesSection struct {
	Documents []exportedDocument `json:"documents"`
	Drafts    []exportedDocument `json:"drafts"`
}

type exportedDocument struct {
	Number         *int64         `json:"number,omitempty"`
	Kind           string         `json:"kind"`
	IssueDate      *string        `json:"issueDate,omitempty"`
	DeliveryDate   *string        `json:"deliveryDate,omitempty"`
	DeliveryFrom   *string        `json:"deliveryFrom,omitempty"`
	DeliveryTo     *string        `json:"deliveryTo,omitempty"`
	DueDate        *string        `json:"dueDate,omitempty"`
	Currency       string         `json:"currency"`
	NetTotal       string         `json:"netTotal"`
	VatTotal       string         `json:"vatTotal"`
	GrossTotal     string         `json:"grossTotal"`
	BuyerName      *string        `json:"buyerName,omitempty"`
	BuyerAddress   []string       `json:"buyerAddress,omitempty"`
	YourReference  string         `json:"yourReference,omitempty"`
	OurReference   string         `json:"ourReference,omitempty"`
	OrderReference string         `json:"orderReference,omitempty"`
	Note           string         `json:"note,omitempty"`
	InternalNote   string         `json:"internalNote,omitempty"`
	Lines          []exportedLine `json:"lines"`
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
// every issued document and every draft, with the internal note: the
// customers export treats staff-written notes as data held about the person.
func (p customerPersonalData) ExportCustomerData(ctx context.Context, customerID int32) (any, error) {
	q := store.New(p.pool)
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
	section := invoicesSection{Documents: []exportedDocument{}, Drafts: []exportedDocument{}}
	for _, d := range docs {
		e := exportedDocument{
			Number: d.Number, Kind: d.Kind, IssueDate: dateText(d.IssueDate), DeliveryDate: dateText(d.DeliveryDate),
			DeliveryFrom: dateText(d.DeliveryFrom), DeliveryTo: dateText(d.DeliveryTo), DueDate: dateText(d.DueDate),
			Currency: d.Currency, BuyerName: d.BuyerName, YourReference: d.YourReference, OurReference: d.OurReference,
			OrderReference: d.OrderReference, Note: d.Note, InternalNote: d.InternalNote, Lines: []exportedLine{},
		}
		for _, part := range []*string{d.BuyerAddressLine1, d.BuyerAddressLine2, d.BuyerPostalCode, d.BuyerCity, d.BuyerCountry} {
			if part != nil && *part != "" {
				e.BuyerAddress = append(e.BuyerAddress, *part)
			}
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
			section.Documents = append(section.Documents, e)
		} else {
			section.Drafts = append(section.Drafts, e)
		}
	}
	return section, nil
}

// EraseCustomerData deletes the person's drafts and keeps their issued
// documents. A draft is not a salgsdokument, so it has no retention basis and
// GDPR art. 17 applies; an issued document and its buyer snapshot are kept
// under bokføringsloven § 13 — five years after the end of the financial year —
// which is why invoices.documents reports 0. contracts.ErasedData carries no
// reason; docs/invoices.md and the anonymisation table in docs/customers.md
// say it.
func (customerPersonalData) EraseCustomerData(ctx context.Context, tx pgx.Tx, customerID int32) ([]contracts.ErasedData, error) {
	n, err := store.New(tx).DeleteCustomerDrafts(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("invoices: erase customer %d's drafts: %w", customerID, err)
	}
	return []contracts.ErasedData{
		{Kind: kindInvoicesDrafts, Count: n},
		{Kind: kindInvoicesDocuments, Count: 0},
	}, nil
}
