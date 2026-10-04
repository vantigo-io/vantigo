package ehf

import (
	"encoding/base64"
	"fmt"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The Peppol BIS Billing 3.0 identifiers (EHF and KID design D4, research
// §2.2). EHF Billing 3.0 has no customization id of its own, and the
// document carries no UBLVersionID (a Peppol warning).
const (
	CustomizationID = "urn:cen.eu:en16931:2017#compliant#urn:fdc:peppol.eu:2017:poacc:billing:3.0"
	ProfileID       = "urn:fdc:peppol.eu:2017:poacc:billing:01:1.0"
)

const (
	invoiceTypeCode    = "380" // commercial invoice
	creditNoteTypeCode = "381" // credit note — never a negative 380
	// paymentMeansCredit is credit transfer (UNCL4461 30): 58 is SEPA's EUR
	// shape and wrong on a NOK invoice.
	paymentMeansCredit = "30"
	// allowanceDiscount is UNCL5189 95, "Discount": every line allowance
	// needs a reason or a reason code (BR-42).
	allowanceDiscount = "95"
	vatScheme         = "VAT"
)

// The fixed words a document carries, in its language.
type words struct {
	due, creditTerms, invoicePDF, creditNotePDF, discount, dateLayout string
	vatRounding, decimalPoint                                         string
}

var vocabulary = map[string]words{
	"nb": {
		due: "Forfall %s", creditTerms: "Kreditnota – beløpet godskrives",
		invoicePDF: "Faktura (PDF)", creditNotePDF: "Kreditnota (PDF)", discount: "Rabatt", dateLayout: "02.01.2006",
		vatRounding: "Avrunding merverdiavgift %s %%", decimalPoint: ",",
	},
	"en": {
		due: "Due %s", creditTerms: "Credit note – the amount is credited",
		invoicePDF: "Invoice (PDF)", creditNotePDF: "Credit note (PDF)", discount: "Discount", dateLayout: time.DateOnly,
		vatRounding: "VAT rounding %s %%", decimalPoint: ".",
	},
}

// exemptionCodes are the VATEX codes of the categories that carry one (D4);
// E carries the free-text reason instead, and S, Z and K nothing.
var exemptionCodes = map[string]string{"AE": "VATEX-EU-AE", "G": "VATEX-EU-G", "O": "VATEX-EU-O"}

// Render is d as a Peppol BIS Billing 3.0 UBL 2.1 Invoice or CreditNote:
// the elements in the schema's order, amounts with two decimals, quantities
// and prices at their stored scale, the PDF embedded. It reads nothing but
// d, so the same Document renders the same bytes.
func Render(d Document) ([]byte, error) {
	root, rootNS, lineName, quantityName := "Invoice", nsInvoice, "cac:InvoiceLine", "cbc:InvoicedQuantity"
	switch d.Kind {
	case KindInvoice:
	case KindCreditNote:
		root, rootNS, lineName, quantityName = "CreditNote", nsCreditNote, "cac:CreditNoteLine", "cbc:CreditedQuantity"
	default:
		return nil, fmt.Errorf("ehf: unknown document kind %q", d.Kind)
	}
	sellerScheme, sellerID, err := endpoint(d.Seller.PeppolID)
	if err != nil {
		return nil, fmt.Errorf("ehf: the seller's Peppol id: %w", err)
	}
	buyerScheme, buyerID, err := endpoint(d.Buyer.PeppolID)
	if err != nil {
		return nil, fmt.Errorf("ehf: the buyer's Peppol id: %w", err)
	}
	v, ok := vocabulary[d.Language]
	if !ok {
		v = vocabulary["nb"]
	}
	invoice := d.Kind == KindInvoice
	cur := d.Currency

	w := newWriter()
	w.start(root, attr("xmlns", rootNS), attr("xmlns:cac", nsCAC), attr("xmlns:cbc", nsCBC))
	w.leaf("cbc:CustomizationID", CustomizationID)
	w.leaf("cbc:ProfileID", ProfileID)
	w.leaf("cbc:ID", d.Number)
	w.leaf("cbc:IssueDate", d.IssueDate)
	if invoice {
		w.leaf("cbc:DueDate", d.DueDate)
		w.leaf("cbc:InvoiceTypeCode", invoiceTypeCode)
	} else {
		w.leaf("cbc:CreditNoteTypeCode", creditNoteTypeCode)
	}
	w.leaf("cbc:DocumentCurrencyCode", cur)
	w.leaf("cbc:BuyerReference", d.BuyerReference)
	if d.DeliveryDate == "" && d.DeliveryFrom != "" && d.DeliveryTo != "" {
		w.start("cac:InvoicePeriod")
		w.leaf("cbc:StartDate", d.DeliveryFrom)
		w.leaf("cbc:EndDate", d.DeliveryTo)
		w.end("cac:InvoicePeriod")
	}
	if d.OrderReference != "" {
		w.start("cac:OrderReference")
		w.leaf("cbc:ID", d.OrderReference)
		w.end("cac:OrderReference")
	}
	if !invoice && d.Original != nil {
		w.start("cac:BillingReference")
		w.start("cac:InvoiceDocumentReference")
		w.leaf("cbc:ID", d.Original.Number)
		w.leaf("cbc:IssueDate", d.Original.IssueDate)
		w.end("cac:InvoiceDocumentReference")
		w.end("cac:BillingReference")
	}
	if len(d.PDF) > 0 {
		description := v.invoicePDF
		if !invoice {
			description = v.creditNotePDF
		}
		w.start("cac:AdditionalDocumentReference")
		w.leaf("cbc:ID", d.Number)
		w.leaf("cbc:DocumentDescription", description)
		w.start("cac:Attachment")
		w.leaf("cbc:EmbeddedDocumentBinaryObject", base64.StdEncoding.EncodeToString(d.PDF),
			attr("mimeCode", "application/pdf"), attr("filename", d.PDFName))
		w.end("cac:Attachment")
		w.end("cac:AdditionalDocumentReference")
	}

	// The seller (D4): its Peppol id at render time, the snapshot's name and
	// address, the VAT id only when VAT-registered (NO-R-001), the
	// Foretaksregisteret marker only when registered there (NO-R-002), the
	// organisation number as the legal registration, the e-mail.
	w.start("cac:AccountingSupplierParty")
	w.start("cac:Party")
	w.leaf("cbc:EndpointID", sellerID, attr("schemeID", sellerScheme))
	if d.Seller.Name != "" {
		w.start("cac:PartyName")
		w.leaf("cbc:Name", d.Seller.Name)
		w.end("cac:PartyName")
	}
	writeAddress(w, "cac:PostalAddress", d.Seller.Address)
	if d.Seller.VATRegistered {
		writeTaxScheme(w, "NO"+d.Seller.OrganisationNumber+"MVA", vatScheme)
	}
	if d.Seller.Foretaksregisteret {
		writeTaxScheme(w, "Foretaksregisteret", "TAX")
	}
	w.start("cac:PartyLegalEntity")
	w.leaf("cbc:RegistrationName", d.Seller.Name)
	w.leaf("cbc:CompanyID", d.Seller.OrganisationNumber, attr("schemeID", "0192"))
	w.end("cac:PartyLegalEntity")
	if d.Seller.Email != "" {
		w.start("cac:Contact")
		w.leaf("cbc:ElectronicMail", d.Seller.Email)
		w.end("cac:Contact")
	}
	w.end("cac:Party")
	w.end("cac:AccountingSupplierParty")

	// The buyer: the snapshot's Peppol id, address and name — the name always
	// (BT-44, BR-07) — with the organisation number under 0192, a foreign id
	// without a scheme, and no legal id for a person.
	w.start("cac:AccountingCustomerParty")
	w.start("cac:Party")
	w.leaf("cbc:EndpointID", buyerID, attr("schemeID", buyerScheme))
	writeAddress(w, "cac:PostalAddress", d.Buyer.Address)
	w.start("cac:PartyLegalEntity")
	w.leaf("cbc:RegistrationName", d.Buyer.Name)
	switch {
	case d.Buyer.Person:
	case d.Buyer.OrganisationNumber != "":
		w.leaf("cbc:CompanyID", d.Buyer.OrganisationNumber, attr("schemeID", "0192"))
	case d.Buyer.ForeignID != "":
		w.leaf("cbc:CompanyID", d.Buyer.ForeignID)
	}
	w.end("cac:PartyLegalEntity")
	w.end("cac:Party")
	w.end("cac:AccountingCustomerParty")

	// The delivery: the date, and the place only with a country (BR-57).
	place := d.DeliveryPlace != nil && d.DeliveryPlace.Country != ""
	if d.DeliveryDate != "" || place {
		w.start("cac:Delivery")
		w.leaf("cbc:ActualDeliveryDate", d.DeliveryDate)
		if place {
			w.start("cac:DeliveryLocation")
			writeAddress(w, "cac:Address", *d.DeliveryPlace)
			w.end("cac:DeliveryLocation")
		}
		w.end("cac:Delivery")
	}

	// One payment means on an invoice: credit transfer to the domestic
	// account, or to the IBAN (with the BIC) for a foreign buyer when the
	// seller has one; the KID as the payment id, and no payment id at all
	// without one (D3).
	if invoice {
		account, bic := d.Payment.BankAccount, ""
		if foreign := d.Buyer.Address.Country != "" && d.Buyer.Address.Country != "NO"; foreign && d.Payment.IBAN != "" {
			account, bic = d.Payment.IBAN, d.Payment.BIC
		}
		w.start("cac:PaymentMeans")
		w.leaf("cbc:PaymentMeansCode", paymentMeansCredit)
		w.leaf("cbc:PaymentID", d.Payment.KID)
		if account != "" {
			w.start("cac:PayeeFinancialAccount")
			w.leaf("cbc:ID", account)
			if bic != "" {
				w.start("cac:FinancialInstitutionBranch")
				w.leaf("cbc:ID", bic)
				w.end("cac:FinancialInstitutionBranch")
			}
			w.end("cac:PayeeFinancialAccount")
		}
		w.end("cac:PaymentMeans")
	}

	// The terms: the due date on an invoice; on a credit note, which has
	// none, the sentence BR-CO-25 needs for a positive amount.
	terms := v.creditTerms
	if invoice {
		due, err := time.Parse(time.DateOnly, d.DueDate)
		if err != nil {
			return nil, fmt.Errorf("ehf: the due date %q: %w", d.DueDate, err)
		}
		terms = fmt.Sprintf(v.due, due.Format(v.dateLayout))
	}
	w.start("cac:PaymentTerms")
	w.leaf("cbc:Note", terms)
	w.end("cac:PaymentTerms")

	// The VAT: one subtotal per summary row, as stored — a credit note's
	// squaring row (0.00 taxable, a small negative VAT) included, which
	// BR-CO-17 and BR-S-09 tolerate within a krone (D4).
	w.start("cac:TaxTotal")
	w.amount("cbc:TaxAmount", d.VATTotal, cur)
	for _, row := range d.VAT {
		w.start("cac:TaxSubtotal")
		w.amount("cbc:TaxableAmount", row.Taxable, cur)
		w.amount("cbc:TaxAmount", row.Amount, cur)
		writeTaxCategory(w, "cac:TaxCategory", row.Category, row.Rate, row.ExemptionReason, true)
		w.end("cac:TaxSubtotal")
	}
	w.end("cac:TaxTotal")

	w.start("cac:LegalMonetaryTotal")
	w.amount("cbc:LineExtensionAmount", d.NetTotal, cur)
	w.amount("cbc:TaxExclusiveAmount", d.NetTotal, cur)
	w.amount("cbc:TaxInclusiveAmount", d.GrossTotal, cur)
	w.amount("cbc:PayableAmount", d.GrossTotal, cur)
	w.end("cac:LegalMonetaryTotal")

	for _, l := range append(slices.Clone(d.Lines), zeroLines(d, v)...) {
		w.start(lineName)
		w.leaf("cbc:ID", l.ID)
		w.leaf(quantityName, decimal(l.Quantity, 3), attr("unitCode", UnitCode(l.Unit)))
		w.amount("cbc:LineExtensionAmount", l.Net, cur)
		if l.DiscountPercent != nil && l.DiscountPercent.Sign() != 0 {
			// The discount as an allowance on the gross (PEPPOL-EN16931-R040–R042).
			w.start("cac:AllowanceCharge")
			w.leaf("cbc:ChargeIndicator", "false")
			w.leaf("cbc:AllowanceChargeReasonCode", allowanceDiscount)
			w.leaf("cbc:AllowanceChargeReason", v.discount)
			w.leaf("cbc:MultiplierFactorNumeric", decimal(l.DiscountPercent, 2))
			w.amount("cbc:Amount", l.Allowance, cur)
			w.amount("cbc:BaseAmount", l.Gross, cur)
			w.end("cac:AllowanceCharge")
		}
		w.start("cac:Item")
		w.leaf("cbc:Name", l.Description)
		writeTaxCategory(w, "cac:ClassifiedTaxCategory", l.Category, l.Rate, "", false)
		w.end("cac:Item")
		w.start("cac:Price")
		w.leaf("cbc:PriceAmount", decimal(l.UnitPrice, 4), attr("currencyID", cur))
		w.end("cac:Price")
		w.end(lineName)
	}
	w.end(root)
	return w.bytes()
}

// zeroLines are the synthetic lines Render adds after the real ones, one
// for every VAT row with no line at its (category, rate): a final credit
// note's squaring row (a taxable amount of 0.00 and a small negative VAT)
// has none, and BR-S-08 — fatal — requires a line at the row's rate to
// exist before its ±1 tolerance applies (EHF and KID design D4, reading 20).
// Each is a zero quantity of C62 at a zero price, so it adds nothing to any
// sum; it is named for the rounding it carries, numbered after the highest
// position. The Document, the stored truth, is unchanged.
func zeroLines(d Document, v words) []Line {
	next := len(d.Lines)
	for _, l := range d.Lines {
		if n, err := strconv.Atoi(l.ID); err == nil && n > next {
			next = n
		}
	}
	var out []Line
	for _, row := range d.VAT {
		covered := slices.ContainsFunc(d.Lines, func(l Line) bool {
			return l.Category == row.Category && sameRate(l.Rate, row.Rate)
		})
		if covered {
			continue
		}
		next++
		rate := strings.TrimRight(strings.TrimRight(decimal(row.Rate, 2), "0"), ".")
		zero := new(big.Rat)
		out = append(out, Line{
			ID: strconv.Itoa(next), Description: fmt.Sprintf(v.vatRounding, strings.Replace(rate, ".", v.decimalPoint, 1)),
			Quantity: zero, UnitPrice: zero, DiscountPercent: zero, Gross: zero, Allowance: zero, Net: zero,
			Category: row.Category, Rate: row.Rate,
		})
	}
	return out
}

// sameRate compares two rates, a nil one as zero.
func sameRate(a, b *big.Rat) bool {
	if a == nil {
		a = new(big.Rat)
	}
	if b == nil {
		b = new(big.Rat)
	}
	return a.Cmp(b) == 0
}

// endpoint splits a participant id "<scheme>:<value>".
func endpoint(id string) (scheme, value string, err error) {
	scheme, value, ok := strings.Cut(id, ":")
	if !ok || scheme == "" || value == "" {
		return "", "", fmt.Errorf("%q is not <scheme>:<value>", id)
	}
	return scheme, value, nil
}

// writeAddress writes a postal address, and nothing for an empty one.
func writeAddress(w *writer, name string, a Address) {
	if a == (Address{}) {
		return
	}
	w.start(name)
	w.leaf("cbc:StreetName", a.Line1)
	w.leaf("cbc:AdditionalStreetName", a.Line2)
	w.leaf("cbc:CityName", a.City)
	w.leaf("cbc:PostalZone", a.PostalCode)
	w.leaf("cbc:CountrySubentity", a.Region)
	if a.Country != "" {
		w.start("cac:Country")
		w.leaf("cbc:IdentificationCode", a.Country)
		w.end("cac:Country")
	}
	w.end(name)
}

// writeTaxScheme writes one of the seller's PartyTaxScheme elements.
func writeTaxScheme(w *writer, companyID, scheme string) {
	w.start("cac:PartyTaxScheme")
	w.leaf("cbc:CompanyID", companyID)
	w.start("cac:TaxScheme")
	w.leaf("cbc:ID", scheme)
	w.end("cac:TaxScheme")
	w.end("cac:PartyTaxScheme")
}

// writeTaxCategory writes a VAT category by D4's rules: the rate for every
// category but O (BR-O-05); in a VAT summary row, the VATEX code for AE, G
// and O and the free-text reason for E only — never on Z (BR-Z-10) or S.
func writeTaxCategory(w *writer, name, category string, rate *big.Rat, reason string, summary bool) {
	w.start(name)
	w.leaf("cbc:ID", category)
	if category != "O" {
		w.leaf("cbc:Percent", decimal(rate, 2))
	}
	if summary {
		w.leaf("cbc:TaxExemptionReasonCode", exemptionCodes[category])
		if category == "E" {
			w.leaf("cbc:TaxExemptionReason", reason)
		}
	}
	w.start("cac:TaxScheme")
	w.leaf("cbc:ID", vatScheme)
	w.end("cac:TaxScheme")
	w.end(name)
}
