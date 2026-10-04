// Package ehf is the EHF document (EHF and KID design D4, D5, D11): a Peppol
// BIS Billing 3.0 UBL 2.1 invoice or credit note, rendered deterministically
// from an issued document's snapshot with its stored PDF embedded, and the
// checks run on the rendered bytes before a send.
//
// The package holds no database, clock, network or randomness: Document is
// exact decimals (math/big.Rat) and strings, Render is a pure function of it,
// and the same Document renders the same bytes. The invoices module builds a
// Document from its rows (ehfDocumentOf) and owns everything around it — the
// store, the transmission, the access point. The only module package it
// imports is invoices/kid, a leaf.
package ehf

import "math/big"

// The two kinds of document, as the invoices module stores them.
const (
	KindInvoice    = "invoice"
	KindCreditNote = "credit_note"
)

// Document is everything an EHF is rendered from: an issued document's own
// rows and snapshots, the seller's current Peppol id and the stored PDF's
// bytes. Dates are ISO 8601 (YYYY-MM-DD); an empty string is absent.
type Document struct {
	Kind     string // KindInvoice or KindCreditNote
	Language string // "nb" or "en", the buyer's; the fixed words of the terms note and the attachment
	Number   string
	// IssueDate and DueDate (an invoice's only).
	IssueDate, DueDate string
	Currency           string // ISO 4217
	// BuyerReference (BT-10) and OrderReference (BT-13): Peppol needs one of
	// the two (PEPPOL-EN16931-R003).
	BuyerReference, OrderReference string
	// Original is the invoice a credit note credits (BT-25/26).
	Original *DocumentReference
	// DeliveryDate, or the period DeliveryFrom–DeliveryTo; DeliveryPlace,
	// written only when it has a country (BR-57).
	DeliveryDate, DeliveryFrom, DeliveryTo string
	DeliveryPlace                          *Address
	Seller                                 Seller
	Buyer                                  Buyer
	Payment                                Payment
	Lines                                  []Line
	VAT                                    []VATRow
	// The document's totals: net (line nets summed), VAT, gross.
	NetTotal, VATTotal, GrossTotal *big.Rat
	// PDF is the stored PDF's bytes, embedded as the visual copy; PDFName its
	// download's file name.
	PDF     []byte
	PDFName string
}

// DocumentReference is a preceding invoice.
type DocumentReference struct {
	Number, IssueDate string
}

// Address is a postal address; Country is ISO 3166-1 alpha-2.
type Address struct {
	Line1, Line2, PostalCode, City, Region, Country string
}

// Seller is the seller snapshot plus the seller's Peppol id at render time
// (D2: not part of the snapshot).
type Seller struct {
	PeppolID           string // "<scheme>:<value>", e.g. "0192:123456785"
	Name               string // the legal name
	Address            Address
	OrganisationNumber string
	VATRegistered      bool // NO<orgnr>MVA as the VAT id (NO-R-001)
	Foretaksregisteret bool // "Foretaksregisteret" as the TAX scheme's id (NO-R-002)
	Email              string
}

// Buyer is the buyer snapshot.
type Buyer struct {
	PeppolID string // "<scheme>:<value>"
	Name     string
	Address  Address
	// OrganisationNumber for a Norwegian business, ForeignID for a foreign
	// one, neither for a person.
	OrganisationNumber, ForeignID string
	Person                        bool
}

// Payment is the seller's payment details and an invoice's KID.
type Payment struct {
	BankAccount, IBAN, BIC string
	// KID and KIDAlgorithm ("mod10" | "mod11") as issued; empty without an
	// agreement, and then the EHF carries no PaymentID (D3).
	KID, KIDAlgorithm string
}

// Line is one document line as stored.
type Line struct {
	ID          string // the position
	Description string
	Unit        string // free text; UnitCode maps it
	// Quantity (3 decimals) and UnitPrice (4) at their stored scale;
	// DiscountPercent; Gross, Allowance and Net (2) as computed at save.
	Quantity, UnitPrice, DiscountPercent *big.Rat
	Gross, Allowance, Net                *big.Rat
	// Category is the line's VAT category (S, Z, E, AE, G, O, K) and Rate
	// its percentage, both from the issue snapshot.
	Category string
	Rate     *big.Rat
}

// VATRow is one (category, rate) row of the document's VAT summary.
type VATRow struct {
	Category        string
	Rate            *big.Rat
	Taxable, Amount *big.Rat
	// ExemptionReason is the VAT code's free-text reason, written only for
	// category E (BT-120); AE, G and O carry a VATEX code instead, and Z and
	// S nothing (BR-Z-10, BR-S-10).
	ExemptionReason string
}
