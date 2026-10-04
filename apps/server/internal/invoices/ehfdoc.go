package invoices

import (
	"fmt"
	"strconv"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices/ehf"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// ehfDocumentOf is an issued document as its EHF carries it (EHF and KID
// design D4): its own rows and snapshots, read through pdfDocumentOf — the
// same parties, totals, VAT rows and KID the stored PDF prints, the KID
// re-verified against its stored algorithm there — plus what only the EHF
// needs: each line's gross, allowance, position and VAT category, the
// buyer's Peppol id, region and type, the seller's Peppol id at the time of
// sending (D2: not part of the snapshot), and the stored PDF's bytes with
// the download's file name. Never the settings beyond that id, the directory
// or the VAT tables.
func ehfDocumentOf(inv store.InvoicesInvoice, lines []store.InvoicesLine, sums []store.InvoicesVatSummary,
	original *store.InvoicesInvoice, sellerPeppolID string, pdf []byte,
) (ehf.Document, error) {
	if inv.Status != statusIssued || inv.Number == nil {
		return ehf.Document{}, fmt.Errorf("invoices: document %d is not issued; it has no EHF", inv.ID)
	}
	p, err := pdfDocumentOf(inv, lines, sums, original)
	if err != nil {
		return ehf.Document{}, err
	}
	str := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	date := func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return t.Format(time.DateOnly)
	}
	address := func(a pdfParty, region string) ehf.Address {
		return ehf.Address{Line1: a.line1, Line2: a.line2, PostalCode: a.postalCode, City: a.city, Region: region, Country: a.country}
	}
	language := "nb"
	if p.language == "en" {
		language = "en"
	}
	d := ehf.Document{
		Kind: p.kind, Language: language, Number: strconv.FormatInt(*inv.Number, 10),
		IssueDate: p.issueDate.Format(time.DateOnly), DueDate: date(p.dueDate), Currency: p.currency,
		BuyerReference: p.yourReference, OrderReference: p.orderRef,
		DeliveryDate: date(p.deliveryDate), DeliveryFrom: date(p.deliveryFrom), DeliveryTo: date(p.deliveryTo),
		Seller: ehf.Seller{
			PeppolID: sellerPeppolID, Name: p.seller.name, Address: address(p.seller, ""),
			OrganisationNumber: p.seller.organisationNumber, VATRegistered: p.seller.vatRegistered,
			Foretaksregisteret: p.seller.foretaksregisteret, Email: p.seller.email,
		},
		Buyer: ehf.Buyer{
			PeppolID: str(inv.BuyerPeppolID), Name: p.buyer.name, Address: address(p.buyer, str(inv.BuyerRegion)),
			OrganisationNumber: p.buyer.organisationNumber, ForeignID: p.buyer.foreignID,
			Person: str(inv.BuyerType) == "person",
		},
		Payment:  ehf.Payment{BankAccount: p.bankAccount, IBAN: p.iban, BIC: p.bic, KID: p.kid},
		NetTotal: p.totals.net, VATTotal: p.totals.vat, GrossTotal: p.totals.gross,
		PDF: pdf, PDFName: fileName(inv),
	}
	if p.kid != "" {
		d.Payment.KIDAlgorithm = str(inv.KidAlgorithm)
	}
	if p.deliveryPlace != nil {
		place := address(*p.deliveryPlace, "")
		d.DeliveryPlace = &place
	}
	if p.credits != nil {
		d.Original = &ehf.DocumentReference{
			Number: strconv.FormatInt(p.credits.number, 10), IssueDate: p.credits.issueDate.Format(time.DateOnly),
		}
	}
	for i, l := range lines {
		pl := p.lines[i]
		line := ehf.Line{
			ID: strconv.FormatInt(int64(l.Position), 10), Description: pl.description, Unit: pl.unit,
			Quantity: pl.quantity, UnitPrice: pl.unitPrice, DiscountPercent: pl.discount, Net: pl.net,
			Category: str(l.VatCategory), Rate: pl.rate,
		}
		if line.Gross, err = ratFromNumeric(l.LineGross); err != nil {
			return ehf.Document{}, err
		}
		if line.Allowance, err = ratFromNumeric(l.LineAllowance); err != nil {
			return ehf.Document{}, err
		}
		if line.Category == "" {
			return ehf.Document{}, fmt.Errorf("invoices: document %d's line %d has no VAT snapshot", inv.ID, l.Position)
		}
		d.Lines = append(d.Lines, line)
	}
	for _, s := range p.summaries {
		row := ehf.VATRow{Category: s.category, Rate: s.rate, Taxable: s.taxable, Amount: s.vat}
		if s.reason != nil {
			row.ExemptionReason = *s.reason
		}
		d.VAT = append(d.VAT, row)
	}
	return d, nil
}
