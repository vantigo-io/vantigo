package ehf_test

import (
	"bytes"
	"encoding/base64"
	"flag"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/invoices/ehf"
)

// update rewrites testdata/golden from the fixtures: go test ./internal/invoices/ehf -update.
// It never touches testdata/invalid.
var update = flag.Bool("update", false, "rewrite testdata/golden from the fixtures")

const (
	nsInvoice    = "urn:oasis:names:specification:ubl:schema:xsd:Invoice-2"
	nsCreditNote = "urn:oasis:names:specification:ubl:schema:xsd:CreditNote-2"
)

// render is d rendered and read back as a namespace-resolved tree.
func render(t *testing.T, d ehf.Document) *ehf.Node {
	t.Helper()
	body, err := ehf.Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	root, err := ehf.Parse(body)
	if err != nil {
		t.Fatalf("Parse: %v\n%s", err, body)
	}
	return root
}

func expect(t *testing.T, root *ehf.Node, want string, path ...string) {
	t.Helper()
	if got := root.Value(path...); got != want {
		t.Errorf("%s = %q, want %q", strings.Join(path, "/"), got, want)
	}
}

func absent(t *testing.T, root *ehf.Node, path ...string) {
	t.Helper()
	if root.Has(path...) {
		t.Errorf("%s is present, want absent", strings.Join(path, "/"))
	}
}

// The schema's sequence of the top-level elements this writer can emit, in
// order (UBL 2.1 maindoc XSDs); the oracle's XSD layer enforces the whole
// order, this pins the hand writer's part of it without a JVM.
var (
	invoiceOrder = []string{
		"CustomizationID", "ProfileID", "ID", "IssueDate", "DueDate", "InvoiceTypeCode", "DocumentCurrencyCode",
		"BuyerReference", "InvoicePeriod", "OrderReference", "BillingReference", "AdditionalDocumentReference",
		"AccountingSupplierParty", "AccountingCustomerParty", "Delivery", "PaymentMeans", "PaymentTerms",
		"TaxTotal", "LegalMonetaryTotal", "InvoiceLine",
	}
	creditNoteOrder = []string{
		"CustomizationID", "ProfileID", "ID", "IssueDate", "CreditNoteTypeCode", "DocumentCurrencyCode",
		"BuyerReference", "InvoicePeriod", "OrderReference", "BillingReference", "AdditionalDocumentReference",
		"AccountingSupplierParty", "AccountingCustomerParty", "Delivery", "PaymentMeans", "PaymentTerms",
		"TaxTotal", "LegalMonetaryTotal", "CreditNoteLine",
	}
)

// The identifiers (D4): the customization and profile ids, no UBLVersionID,
// 380 or 381 by kind under the right root namespace, the number and dates,
// the currency; elements in the schema's order.
func TestEHF_Identifiers(t *testing.T) {
	t.Parallel()
	inv := render(t, fixture(t, "invoice-every-category"))
	if inv.Name.Space != nsInvoice || inv.Name.Local != "Invoice" {
		t.Errorf("root = %v, want Invoice in %s", inv.Name, nsInvoice)
	}
	expect(t, inv, ehf.CustomizationID, "cbc:CustomizationID")
	expect(t, inv, ehf.ProfileID, "cbc:ProfileID")
	absent(t, inv, "cbc:UBLVersionID")
	expect(t, inv, "10042", "cbc:ID")
	expect(t, inv, "2026-10-01", "cbc:IssueDate")
	expect(t, inv, "2026-10-15", "cbc:DueDate")
	expect(t, inv, "380", "cbc:InvoiceTypeCode")
	absent(t, inv, "cbc:CreditNoteTypeCode")
	expect(t, inv, "NOK", "cbc:DocumentCurrencyCode")

	cn := render(t, fixture(t, "credit-note"))
	if cn.Name.Space != nsCreditNote || cn.Name.Local != "CreditNote" {
		t.Errorf("root = %v, want CreditNote in %s", cn.Name, nsCreditNote)
	}
	expect(t, cn, "381", "cbc:CreditNoteTypeCode")
	absent(t, cn, "cbc:InvoiceTypeCode")
	absent(t, cn, "cbc:DueDate")
	absent(t, cn, "cbc:UBLVersionID")

	for name, d := range fixtures(t) {
		root := render(t, d)
		order := invoiceOrder
		if d.Kind == ehf.KindCreditNote {
			order = creditNoteOrder
		}
		last := -1
		for _, c := range root.Children {
			i := slices.Index(order, c.Name.Local)
			if i < 0 {
				t.Errorf("%s: unexpected top-level element %s", name, c.Name.Local)
				continue
			}
			if i < last {
				t.Errorf("%s: %s out of the schema's order", name, c.Name.Local)
			}
			last = i
		}
	}
}

// BT-10 and BT-13: the buyer reference when set, the order reference when
// set, and neither element when empty.
func TestEHF_BuyerAndOrderReference(t *testing.T) {
	t.Parallel()
	root := render(t, fixture(t, "invoice-every-category"))
	expect(t, root, "Ola Nordmann", "cbc:BuyerReference")
	absent(t, root, "cac:OrderReference")

	root = render(t, fixture(t, "invoice-discount"))
	absent(t, root, "cbc:BuyerReference")
	expect(t, root, "PO-4471", "cac:OrderReference", "cbc:ID")
}

// A credit note names the invoice it credits (BT-25/26); an invoice has no
// billing reference.
func TestEHF_CreditNoteBillingReference(t *testing.T) {
	t.Parallel()
	root := render(t, fixture(t, "credit-note"))
	expect(t, root, "10040", "cac:BillingReference", "cac:InvoiceDocumentReference", "cbc:ID")
	expect(t, root, "2026-09-15", "cac:BillingReference", "cac:InvoiceDocumentReference", "cbc:IssueDate")
	absent(t, render(t, fixture(t, "invoice-every-category")), "cac:BillingReference")
}

// The delivery: the date as ActualDeliveryDate, the period as InvoicePeriod,
// the place under the delivery only when it has a country (BR-57).
func TestEHF_DeliveryDatePeriodAndPlace(t *testing.T) {
	t.Parallel()
	root := render(t, fixture(t, "invoice-every-category"))
	expect(t, root, "2026-09-30", "cac:Delivery", "cbc:ActualDeliveryDate")
	absent(t, root, "cac:InvoicePeriod")
	absent(t, root, "cac:Delivery", "cac:DeliveryLocation")

	period := fixture(t, "invoice-delivery-period-and-place")
	root = render(t, period)
	expect(t, root, "2026-09-01", "cac:InvoicePeriod", "cbc:StartDate")
	expect(t, root, "2026-09-30", "cac:InvoicePeriod", "cbc:EndDate")
	absent(t, root, "cac:Delivery", "cbc:ActualDeliveryDate")
	address := []string{"cac:Delivery", "cac:DeliveryLocation", "cac:Address"}
	expect(t, root, "Lagerveien 5", append(address, "cbc:StreetName")...)
	expect(t, root, "0581", append(address, "cbc:PostalZone")...)
	expect(t, root, "Oslo", append(address, "cbc:CityName")...)
	expect(t, root, "NO", append(address, "cac:Country", "cbc:IdentificationCode")...)

	period.DeliveryPlace.Country = ""
	root = render(t, period)
	absent(t, root, "cac:Delivery")
	expect(t, root, "2026-09-01", "cac:InvoicePeriod", "cbc:StartDate")

	none := fixture(t, "invoice-person")
	root = render(t, none)
	absent(t, root, "cac:Delivery")
	absent(t, root, "cac:InvoicePeriod")
}

// The seller (D4): the Peppol id as the endpoint, the name, the address, the
// VAT id only when VAT-registered, Foretaksregisteret only when registered,
// the organisation number under 0192, the e-mail when set.
func TestEHF_SellerParty(t *testing.T) {
	t.Parallel()
	root := render(t, fixture(t, "invoice-every-category"))
	party := root.First("cac:AccountingSupplierParty", "cac:Party")
	if e := party.First("cbc:EndpointID"); e.Text != sellerOrg || e.Attr("schemeID") != "0192" {
		t.Errorf("seller EndpointID = %q scheme %q", e.Text, e.Attr("schemeID"))
	}
	expect(t, party, "Fjordkraft Konsult AS", "cac:PartyName", "cbc:Name")
	expect(t, party, "Storgata 1", "cac:PostalAddress", "cbc:StreetName")
	expect(t, party, "3. etasje", "cac:PostalAddress", "cbc:AdditionalStreetName")
	expect(t, party, "Oslo", "cac:PostalAddress", "cbc:CityName")
	expect(t, party, "0155", "cac:PostalAddress", "cbc:PostalZone")
	expect(t, party, "NO", "cac:PostalAddress", "cac:Country", "cbc:IdentificationCode")
	schemes := map[string]string{}
	for _, s := range party.All("cac:PartyTaxScheme") {
		schemes[s.Value("cac:TaxScheme", "cbc:ID")] = s.Value("cbc:CompanyID")
	}
	if schemes["VAT"] != "NO"+sellerOrg+"MVA" || schemes["TAX"] != "Foretaksregisteret" || len(schemes) != 2 {
		t.Errorf("seller tax schemes = %v", schemes)
	}
	expect(t, party, "Fjordkraft Konsult AS", "cac:PartyLegalEntity", "cbc:RegistrationName")
	if c := party.First("cac:PartyLegalEntity", "cbc:CompanyID"); c.Text != sellerOrg || c.Attr("schemeID") != "0192" {
		t.Errorf("seller legal CompanyID = %q scheme %q", c.Text, c.Attr("schemeID"))
	}
	expect(t, party, "faktura@fjordkraft-konsult.example", "cac:Contact", "cbc:ElectronicMail")

	sole := render(t, fixture(t, "invoice-not-vat-registered")).First("cac:AccountingSupplierParty", "cac:Party")
	absent(t, sole, "cac:PartyTaxScheme")
	absent(t, sole, "cac:Contact")
	expect(t, sole, soleOrg, "cbc:EndpointID")

	// Each marker on its own.
	d := fixture(t, "invoice-person")
	d.Seller.Foretaksregisteret = false
	only := render(t, d).First("cac:AccountingSupplierParty", "cac:Party").All("cac:PartyTaxScheme")
	if len(only) != 1 || only[0].Value("cac:TaxScheme", "cbc:ID") != "VAT" {
		t.Errorf("a VAT-registered seller outside Foretaksregisteret: %d tax schemes", len(only))
	}
	d.Seller.Foretaksregisteret, d.Seller.VATRegistered = true, false
	only = render(t, d).First("cac:AccountingSupplierParty", "cac:Party").All("cac:PartyTaxScheme")
	if len(only) != 1 || only[0].Value("cbc:CompanyID") != "Foretaksregisteret" {
		t.Errorf("a registered seller outside the VAT register: %d tax schemes", len(only))
	}
}

// The seller's Peppol id is read at render, not from the snapshot (D2): a
// different id renders a different endpoint and different bytes.
func TestEHF_DiffersWhenTheSellerPeppolIdChanges(t *testing.T) {
	t.Parallel()
	d := fixture(t, "invoice-every-category")
	a, err := ehf.Render(d)
	if err != nil {
		t.Fatal(err)
	}
	d.Seller.PeppolID = "0088:7080000000012"
	b, err := ehf.Render(d)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("the same bytes for two seller Peppol ids")
	}
	root, err := ehf.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if e := root.First("cac:AccountingSupplierParty", "cac:Party", "cbc:EndpointID"); e.Text != "7080000000012" || e.Attr("schemeID") != "0088" {
		t.Errorf("seller EndpointID = %q scheme %q", e.Text, e.Attr("schemeID"))
	}
}

// The buyer: the snapshot's Peppol id split into scheme and value, the
// address, the registration name always (BR-07), the organisation number
// under 0192 for a Norwegian business, a foreign id without a scheme, and no
// legal id for a person.
func TestEHF_BuyerParty(t *testing.T) {
	t.Parallel()
	party := render(t, fixture(t, "invoice-every-category")).First("cac:AccountingCustomerParty", "cac:Party")
	if e := party.First("cbc:EndpointID"); e.Text != buyerOrg || e.Attr("schemeID") != "0192" {
		t.Errorf("buyer EndpointID = %q scheme %q", e.Text, e.Attr("schemeID"))
	}
	expect(t, party, "Industriveien 12", "cac:PostalAddress", "cbc:StreetName")
	expect(t, party, "Heimdal", "cac:PostalAddress", "cbc:CityName")
	expect(t, party, "7080", "cac:PostalAddress", "cbc:PostalZone")
	expect(t, party, "NO", "cac:PostalAddress", "cac:Country", "cbc:IdentificationCode")
	absent(t, party, "cac:PostalAddress", "cbc:AdditionalStreetName")
	expect(t, party, "Nordlys Bygg AS", "cac:PartyLegalEntity", "cbc:RegistrationName")
	if c := party.First("cac:PartyLegalEntity", "cbc:CompanyID"); c.Text != buyerOrg || c.Attr("schemeID") != "0192" {
		t.Errorf("buyer CompanyID = %q scheme %q", c.Text, c.Attr("schemeID"))
	}
	absent(t, party, "cac:PartyName")
	absent(t, party, "cac:PartyTaxScheme")

	foreign := render(t, fixture(t, "invoice-foreign-buyer")).First("cac:AccountingCustomerParty", "cac:Party")
	if e := foreign.First("cbc:EndpointID"); e.Text != "5560360793" || e.Attr("schemeID") != "0007" {
		t.Errorf("foreign EndpointID = %q scheme %q", e.Text, e.Attr("schemeID"))
	}
	if c := foreign.First("cac:PartyLegalEntity", "cbc:CompanyID"); c == nil || c.Text != "SE5560360793" || len(c.Attrs) != 0 {
		t.Errorf("foreign CompanyID = %+v, want SE5560360793 without a scheme", c)
	}
	expect(t, foreign, "SE", "cac:PostalAddress", "cac:Country", "cbc:IdentificationCode")
	expect(t, foreign, "Stockholms län", "cac:PostalAddress", "cbc:CountrySubentity")
	absent(t, party, "cac:PostalAddress", "cbc:CountrySubentity")

	person := render(t, fixture(t, "invoice-person")).First("cac:AccountingCustomerParty", "cac:Party")
	expect(t, person, "Kari Nordmann", "cac:PartyLegalEntity", "cbc:RegistrationName")
	absent(t, person, "cac:PartyLegalEntity", "cbc:CompanyID")
}

// One payment means, code 30, on an invoice: the domestic account for a
// Norwegian buyer; the IBAN with the BIC for a foreign buyer when the seller
// has one, else the domestic account; the KID as PaymentID, and none
// without a KID; nothing on a credit note.
func TestEHF_PaymentMeans(t *testing.T) {
	t.Parallel()
	root := render(t, fixture(t, "invoice-every-category"))
	means := root.All("cac:PaymentMeans")
	if len(means) != 1 {
		t.Fatalf("%d PaymentMeans, want 1", len(means))
	}
	expect(t, means[0], "30", "cbc:PaymentMeansCode")
	expect(t, means[0], "86011117947", "cac:PayeeFinancialAccount", "cbc:ID")
	absent(t, means[0], "cac:PayeeFinancialAccount", "cac:FinancialInstitutionBranch")
	absent(t, means[0], "cbc:PaymentID")

	foreign := fixture(t, "invoice-foreign-buyer")
	means = render(t, foreign).All("cac:PaymentMeans")
	expect(t, means[0], "30", "cbc:PaymentMeansCode")
	expect(t, means[0], "NO9386011117947", "cac:PayeeFinancialAccount", "cbc:ID")
	expect(t, means[0], "DNBANOKKXXX", "cac:PayeeFinancialAccount", "cac:FinancialInstitutionBranch", "cbc:ID")
	foreign.Payment.IBAN, foreign.Payment.BIC = "", ""
	means = render(t, foreign).All("cac:PaymentMeans")
	expect(t, means[0], "86011117947", "cac:PayeeFinancialAccount", "cbc:ID")
	absent(t, means[0], "cac:PayeeFinancialAccount", "cac:FinancialInstitutionBranch")

	for _, name := range []string{"invoice-kid-mod10", "invoice-kid-mod11"} {
		d := fixture(t, name)
		means = render(t, d).All("cac:PaymentMeans")
		if d.Payment.KID == "" {
			t.Fatalf("%s has no KID", name)
		}
		expect(t, means[0], d.Payment.KID, "cbc:PaymentID")
	}

	absent(t, render(t, fixture(t, "credit-note")), "cac:PaymentMeans")
}

// The terms: the due date on an invoice in the document's language; the
// credit sentence on a credit note (BR-CO-25).
func TestEHF_PaymentTerms(t *testing.T) {
	t.Parallel()
	expect(t, render(t, fixture(t, "invoice-every-category")), "Forfall 15.10.2026", "cac:PaymentTerms", "cbc:Note")
	expect(t, render(t, fixture(t, "invoice-foreign-buyer")), "Due 2026-10-15", "cac:PaymentTerms", "cbc:Note")
	expect(t, render(t, fixture(t, "credit-note")), "Kreditnota – beløpet godskrives", "cac:PaymentTerms", "cbc:Note")
	en := fixture(t, "credit-note")
	en.Language = "en"
	expect(t, render(t, en), "Credit note – the amount is credited", "cac:PaymentTerms", "cbc:Note")
}

// The VAT: the total, one subtotal per summary row in the document's order,
// and D4's category rules — the rate on every category but O (BR-O-05), the
// VATEX code on AE, G and O, the free-text reason on E only, nothing on Z
// (BR-Z-10) or S.
func TestEHF_TaxTotalAndTheCategoryRules(t *testing.T) {
	t.Parallel()
	root := render(t, fixture(t, "invoice-every-category"))
	totals := root.All("cac:TaxTotal")
	if len(totals) != 1 {
		t.Fatalf("%d TaxTotal, want 1", len(totals))
	}
	if a := totals[0].First("cbc:TaxAmount"); a.Text != "3256.00" || a.Attr("currencyID") != "NOK" {
		t.Errorf("TaxTotal/TaxAmount = %q %q", a.Text, a.Attr("currencyID"))
	}
	type row struct{ id, percent, code, reason, taxable, amount string }
	var got []row
	for _, s := range totals[0].All("cac:TaxSubtotal") {
		c := s.First("cac:TaxCategory")
		got = append(got, row{
			c.Value("cbc:ID"), c.Value("cbc:Percent"), c.Value("cbc:TaxExemptionReasonCode"), c.Value("cbc:TaxExemptionReason"),
			s.Value("cbc:TaxableAmount"), s.Value("cbc:TaxAmount"),
		})
		if c.Value("cac:TaxScheme", "cbc:ID") != "VAT" {
			t.Errorf("subtotal %s: TaxScheme %q", c.Value("cbc:ID"), c.Value("cac:TaxScheme", "cbc:ID"))
		}
		if c.Has("cbc:Percent") == (c.Value("cbc:ID") == "O") {
			t.Errorf("subtotal %s: Percent present = %v", c.Value("cbc:ID"), c.Has("cbc:Percent"))
		}
	}
	want := []row{
		{"S", "25.00", "", "", "12000.00", "3000.00"},
		{"S", "15.00", "", "", "1000.00", "150.00"},
		{"S", "12.00", "", "", "800.00", "96.00"},
		{"S", "11.11", "", "", "90.00", "10.00"},
		{"AE", "0.00", "VATEX-EU-AE", "", "3000.00", "0.00"},
		{"E", "0.00", "", "Unntatt fra merverdiavgift (mval. kap. 3)", "9000.00", "0.00"},
		{"G", "0.00", "VATEX-EU-G", "", "1500.00", "0.00"},
		{"Z", "0.00", "", "", "90.00", "0.00"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("subtotals =\n%v\nwant\n%v", got, want)
	}

	o := render(t, fixture(t, "invoice-not-vat-registered")).First("cac:TaxTotal", "cac:TaxSubtotal", "cac:TaxCategory")
	expect(t, o, "O", "cbc:ID")
	absent(t, o, "cbc:Percent")
	expect(t, o, "VATEX-EU-O", "cbc:TaxExemptionReasonCode")
	absent(t, o, "cbc:TaxExemptionReason")

	// K (refused at send by the pre-check) carries its rate and no reason.
	k := fixture(t, "invoice-person")
	k.Lines[0].Category, k.Lines[0].Rate = "K", rat("0")
	k.VAT = []ehf.VATRow{vat("K", "0", "2000.00", "0.00", "Some reason")}
	kc := render(t, k).First("cac:TaxTotal", "cac:TaxSubtotal", "cac:TaxCategory")
	expect(t, kc, "0.00", "cbc:Percent")
	absent(t, kc, "cbc:TaxExemptionReasonCode")
	absent(t, kc, "cbc:TaxExemptionReason")
}

// The final credit note's squaring row is rendered as stored: a taxable
// amount of 0.00 and a negative VAT, inside BR-CO-17's and BR-S-09's krone.
func TestEHF_TheSquaringRow(t *testing.T) {
	t.Parallel()
	subs := render(t, fixture(t, "credit-note-squaring")).All("cac:TaxTotal", "cac:TaxSubtotal")
	if len(subs) != 2 {
		t.Fatalf("%d subtotals, want 2", len(subs))
	}
	expect(t, subs[1], "0.00", "cbc:TaxableAmount")
	expect(t, subs[1], "-0.03", "cbc:TaxAmount")
	expect(t, subs[1], "15.00", "cac:TaxCategory", "cbc:Percent")
	expect(t, render(t, fixture(t, "credit-note-squaring")), "24.97", "cac:TaxTotal", "cbc:TaxAmount")
}

// A VAT row with no line at its (category, rate) gets a synthetic zero line
// after the real ones (D4, reading 20): BR-S-08 requires a line at an S
// row's rate to exist before its ±1 tolerance applies, so the squaring row
// alone would be fatal. The line is a zero quantity of C62 at a zero price,
// named for the rounding in the document's language, numbered after the
// highest position; it adds nothing to the totals.
func TestEHF_ASquaringRowGetsAZeroLine(t *testing.T) {
	t.Parallel()
	d := fixture(t, "credit-note-squaring")
	root := render(t, d)
	lines := root.All("cac:CreditNoteLine")
	if len(lines) != 2 {
		t.Fatalf("%d lines, want the real one and a zero line", len(lines))
	}
	z := lines[1]
	expect(t, z, "2", "cbc:ID")
	if q := z.First("cbc:CreditedQuantity"); q.Text != "0.000" || q.Attr("unitCode") != "C62" {
		t.Errorf("zero line quantity = %q %q", q.Text, q.Attr("unitCode"))
	}
	expect(t, z, "0.00", "cbc:LineExtensionAmount")
	absent(t, z, "cac:AllowanceCharge")
	expect(t, z, "Avrunding merverdiavgift 15 %", "cac:Item", "cbc:Name")
	expect(t, z, "S", "cac:Item", "cac:ClassifiedTaxCategory", "cbc:ID")
	expect(t, z, "15.00", "cac:Item", "cac:ClassifiedTaxCategory", "cbc:Percent")
	expect(t, z, "0.0000", "cac:Price", "cbc:PriceAmount")

	// BR-S-08's own expression, for every S row: a line at the row's rate
	// exists, and the row's taxable amount is the sum of those lines' nets
	// within ±1.
	for _, sub := range root.All("cac:TaxTotal", "cac:TaxSubtotal") {
		cat := sub.First("cac:TaxCategory")
		if cat.Value("cbc:ID") != "S" {
			continue
		}
		rate, taxable := rat(cat.Value("cbc:Percent")), rat(sub.Value("cbc:TaxableAmount"))
		sum, found := new(big.Rat), false
		for _, l := range lines {
			c := l.First("cac:Item", "cac:ClassifiedTaxCategory")
			if c.Value("cbc:ID") == "S" && rat(c.Value("cbc:Percent")).Cmp(rate) == 0 {
				found = true
				sum.Add(sum, rat(l.Value("cbc:LineExtensionAmount")))
			}
		}
		diff := new(big.Rat).Sub(taxable, sum)
		if !found || diff.Abs(diff).Cmp(big.NewRat(1, 1)) >= 0 {
			t.Errorf("BR-S-08 at %s %%: a line exists = %v, taxable %s against lines %s", rate.FloatString(2), found, taxable.FloatString(2), sum.FloatString(2))
		}
	}
	if rules, err := ehf.Invariants(mustRender(t, d), d); err != nil || len(rules) != 0 {
		t.Errorf("Invariants = %v, %v", rules, err)
	}

	// Generic over the category, in English, after the highest position; a
	// row with lines gets none.
	g := fixture(t, "invoice-foreign-buyer")
	g.Lines[0].ID = "7"
	g.VAT = append(g.VAT, vat("E", "0", "0.00", "0.00", "Unntatt"), vat("O", "0", "0.00", "0.00", "Ikke registrert"))
	lines = render(t, g).All("cac:InvoiceLine")
	if len(lines) != 3 {
		t.Fatalf("%d lines, want 3", len(lines))
	}
	expect(t, lines[1], "8", "cbc:ID")
	expect(t, lines[1], "VAT rounding 0 %", "cac:Item", "cbc:Name")
	expect(t, lines[1], "E", "cac:Item", "cac:ClassifiedTaxCategory", "cbc:ID")
	expect(t, lines[1], "0.00", "cac:Item", "cac:ClassifiedTaxCategory", "cbc:Percent")
	expect(t, lines[2], "9", "cbc:ID")
	expect(t, lines[2], "O", "cac:Item", "cac:ClassifiedTaxCategory", "cbc:ID")
	absent(t, lines[2], "cac:Item", "cac:ClassifiedTaxCategory", "cbc:Percent")
	n := fixture(t, "credit-note-squaring")
	n.VAT[1].Rate = rat("11.11")
	expect(t, render(t, n).All("cac:CreditNoteLine")[1], "Avrunding merverdiavgift 11,11 %", "cac:Item", "cbc:Name")
	for name, d := range fixtures(t) {
		if name == "credit-note-squaring" {
			continue
		}
		root := render(t, d)
		if got := len(root.All("cac:InvoiceLine")) + len(root.All("cac:CreditNoteLine")); got != len(d.Lines) {
			t.Errorf("%s: %d lines rendered, %d stored", name, got, len(d.Lines))
		}
	}
}

// Text is escaped, never written raw: a name, an address and a description
// with XML's special characters read back as they were.
func TestEHF_EscapesNamesAndAddresses(t *testing.T) {
	t.Parallel()
	d := fixture(t, "invoice-person")
	d.Buyer.Name = `Hansen & Sønn <"AS"> 'x'`
	d.Buyer.Address.Line1 = "Gate 1 & 2 <bak>"
	d.Lines[0].Description = `Timer & reise <"fri">`
	root := render(t, d)
	party := root.First("cac:AccountingCustomerParty", "cac:Party")
	expect(t, party, `Hansen & Sønn <"AS"> 'x'`, "cac:PartyLegalEntity", "cbc:RegistrationName")
	expect(t, party, "Gate 1 & 2 <bak>", "cac:PostalAddress", "cbc:StreetName")
	expect(t, root, `Timer & reise <"fri">`, "cac:InvoiceLine", "cac:Item", "cbc:Name")
	if got := precheck(t, mustRender(t, d)); len(got) != 0 {
		t.Errorf("Precheck = %v", got)
	}
}

// The totals: net as the line extension and the tax-exclusive amount, gross
// as the tax-inclusive and the payable amount, no rounding amount; positive
// on a credit note.
func TestEHF_LegalMonetaryTotal(t *testing.T) {
	t.Parallel()
	for name, want := range map[string][2]string{"invoice-every-category": {"27480.00", "30736.00"}, "credit-note": {"6000.00", "7500.00"}} {
		total := render(t, fixture(t, name)).First("cac:LegalMonetaryTotal")
		expect(t, total, want[0], "cbc:LineExtensionAmount")
		expect(t, total, want[0], "cbc:TaxExclusiveAmount")
		expect(t, total, want[1], "cbc:TaxInclusiveAmount")
		expect(t, total, want[1], "cbc:PayableAmount")
		absent(t, total, "cbc:PayableRoundingAmount")
		for _, c := range total.Children {
			if c.Attr("currencyID") != "NOK" {
				t.Errorf("%s: %s currencyID %q", name, c.Name.Local, c.Attr("currencyID"))
			}
		}
	}
}

// One line element per line: the position, the quantity at its stored scale
// with the unit's code, the net, the discount as an allowance only when not
// zero, the description as the item's name, the line's category with its
// rate except for O, the unit price at its stored scale; CreditedQuantity on
// a credit note.
func TestEHF_Lines(t *testing.T) {
	t.Parallel()
	lines := render(t, fixture(t, "invoice-discount")).All("cac:InvoiceLine")
	if len(lines) != 2 {
		t.Fatalf("%d lines, want 2", len(lines))
	}
	l := lines[0]
	expect(t, l, "1", "cbc:ID")
	if q := l.First("cbc:InvoicedQuantity"); q.Text != "4.000" || q.Attr("unitCode") != "C62" {
		t.Errorf("InvoicedQuantity = %q unit %q", q.Text, q.Attr("unitCode"))
	}
	expect(t, l, "8996.40", "cbc:LineExtensionAmount")
	ac := l.First("cac:AllowanceCharge")
	expect(t, ac, "false", "cbc:ChargeIndicator")
	expect(t, ac, "95", "cbc:AllowanceChargeReasonCode")
	expect(t, ac, "Rabatt", "cbc:AllowanceChargeReason")
	expect(t, ac, "10.00", "cbc:MultiplierFactorNumeric")
	expect(t, ac, "999.60", "cbc:Amount")
	expect(t, ac, "9996.00", "cbc:BaseAmount")
	expect(t, l, "Kontorstoler", "cac:Item", "cbc:Name")
	expect(t, l, "S", "cac:Item", "cac:ClassifiedTaxCategory", "cbc:ID")
	expect(t, l, "25.00", "cac:Item", "cac:ClassifiedTaxCategory", "cbc:Percent")
	expect(t, l, "VAT", "cac:Item", "cac:ClassifiedTaxCategory", "cac:TaxScheme", "cbc:ID")
	if p := l.First("cac:Price", "cbc:PriceAmount"); p.Text != "2499.0000" || p.Attr("currencyID") != "NOK" {
		t.Errorf("PriceAmount = %q %q", p.Text, p.Attr("currencyID"))
	}
	if q := lines[1].First("cbc:InvoicedQuantity"); q.Text != "2.000" || q.Attr("unitCode") != "HUR" {
		t.Errorf("line 2 InvoicedQuantity = %q unit %q", q.Text, q.Attr("unitCode"))
	}
	absent(t, lines[1], "cac:AllowanceCharge")

	o := render(t, fixture(t, "invoice-not-vat-registered")).First("cac:InvoiceLine", "cac:Item", "cac:ClassifiedTaxCategory")
	expect(t, o, "O", "cbc:ID")
	absent(t, o, "cbc:Percent")
	absent(t, o, "cbc:TaxExemptionReasonCode")

	every := render(t, fixture(t, "invoice-every-category")).All("cac:InvoiceLine")
	for i, want := range []string{"HUR", "C62", "C62", "KGM", "C62", "MON", "HUR", "XPK"} {
		if got := every[i].First("cbc:InvoicedQuantity").Attr("unitCode"); got != want {
			t.Errorf("line %d unitCode = %q, want %q", i+1, got, want)
		}
	}
	for _, l := range every {
		absent(t, l, "cac:Item", "cac:ClassifiedTaxCategory", "cbc:TaxExemptionReasonCode")
		absent(t, l, "cac:Item", "cac:ClassifiedTaxCategory", "cbc:TaxExemptionReason")
	}

	cn := render(t, fixture(t, "credit-note"))
	absent(t, cn, "cac:InvoiceLine")
	if q := cn.First("cac:CreditNoteLine", "cbc:CreditedQuantity"); q == nil || q.Text != "5.000" || q.Attr("unitCode") != "HUR" {
		t.Errorf("CreditedQuantity = %+v", q)
	}
	expect(t, cn, "6000.00", "cac:CreditNoteLine", "cbc:LineExtensionAmount")
}

// The PDF is embedded byte for byte (D4): Base64 with its MIME code and the
// download's file name, under the document's number and a description in
// its language.
func TestEHF_TheEmbeddedPDF(t *testing.T) {
	t.Parallel()
	ref := render(t, fixture(t, "invoice-every-category")).First("cac:AdditionalDocumentReference")
	expect(t, ref, "10042", "cbc:ID")
	expect(t, ref, "Faktura (PDF)", "cbc:DocumentDescription")
	obj := ref.First("cac:Attachment", "cbc:EmbeddedDocumentBinaryObject")
	if obj.Attr("mimeCode") != "application/pdf" || obj.Attr("filename") != "faktura-10042.pdf" {
		t.Errorf("attachment attributes = %v", obj.Attrs)
	}
	got, err := base64.StdEncoding.DecodeString(obj.Text)
	if err != nil || !bytes.Equal(got, testPDF) {
		t.Errorf("embedded bytes = %q, %v; want the fixed PDF", got, err)
	}
	expect(t, render(t, fixture(t, "invoice-foreign-buyer")), "Invoice (PDF)", "cac:AdditionalDocumentReference", "cbc:DocumentDescription")
	expect(t, render(t, fixture(t, "credit-note")), "Kreditnota (PDF)", "cac:AdditionalDocumentReference", "cbc:DocumentDescription")
}

// Amounts are written with exactly two decimals and never in exponent form,
// whatever the value's size; no element is empty (PEPPOL-EN16931-R008).
func TestEHF_DecimalsAndNoEmptyElements(t *testing.T) {
	t.Parallel()
	d := fixture(t, "invoice-person")
	d.Lines = []ehf.Line{line("1", "Stor jobb", "", "0.001", "99999999.9999", "0", "S", "25")}
	d.VAT = []ehf.VATRow{vat("S", "25", "100000.00", "25000.00", "")}
	d.NetTotal, d.VATTotal, d.GrossTotal = rat("100000.00"), rat("25000.00"), rat("125000.00")
	body, err := ehf.Render(d)
	if err != nil {
		t.Fatal(err)
	}
	root, err := ehf.Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	plain := regexp.MustCompile(`^-?[0-9]+\.[0-9]+$`)
	for _, n := range append(root.Descendants("cbc:PriceAmount"), root.Descendants("cbc:InvoicedQuantity")...) {
		if !plain.MatchString(n.Text) {
			t.Errorf("%s = %q, not a plain decimal", n.Name.Local, n.Text)
		}
	}
	expect(t, root, "0.001", "cac:InvoiceLine", "cbc:InvoicedQuantity")
	expect(t, root, "99999999.9999", "cac:InvoiceLine", "cac:Price", "cbc:PriceAmount")
	expect(t, root, "100000.00", "cac:LegalMonetaryTotal", "cbc:LineExtensionAmount")
	money := regexp.MustCompile(`^-?[0-9]+\.[0-9]{2}$`)
	for name, d := range fixtures(t) {
		var walk func(n *ehf.Node)
		walk = func(n *ehf.Node) {
			if len(n.Children) == 0 && n.Text == "" {
				t.Errorf("%s: empty element %s", name, n.Name.Local)
			}
			if n.Attr("currencyID") != "" && n.Name.Local != "PriceAmount" && !money.MatchString(n.Text) {
				t.Errorf("%s: %s = %q, not two decimals", name, n.Name.Local, n.Text)
			}
			for _, c := range n.Children {
				walk(c)
			}
		}
		walk(render(t, d))
	}
}

// A Document Render cannot address is an error, never a document without an
// endpoint.
func TestEHF_RefusesWhatItCannotRender(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*ehf.Document){
		"unknown kind":       func(d *ehf.Document) { d.Kind = "receipt" },
		"no buyer Peppol id": func(d *ehf.Document) { d.Buyer.PeppolID = "" },
		"no seller scheme":   func(d *ehf.Document) { d.Seller.PeppolID = sellerOrg },
		"no due date":        func(d *ehf.Document) { d.DueDate = "" },
	} {
		d := fixture(t, "invoice-person")
		mutate(&d)
		if _, err := ehf.Render(d); err == nil {
			t.Errorf("%s: Render succeeded", name)
		}
	}
}

// The render is pure: the same Document twice is the same bytes — no clock,
// no randomness, no map order.
func TestEHF_IsPure(t *testing.T) {
	t.Parallel()
	for name, d := range fixtures(t) {
		a, err := ehf.Render(d)
		if err != nil {
			t.Fatal(err)
		}
		for range 5 {
			b, err := ehf.Render(fixtures(t)[name])
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(a, b) {
				t.Fatalf("%s: two renders differ", name)
			}
		}
	}
}

// The goldens are the fixtures rendered, byte for byte; -update rewrites
// them (and only them). They are what the oracle validates (D11).
func TestEHF_Goldens(t *testing.T) {
	t.Parallel()
	all := fixtures(t)
	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		body, err := ehf.Render(all[name])
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		path := filepath.Join("testdata", "golden", name+".xml")
		if *update {
			if err := os.WriteFile(path, body, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (run with -update to write it)", name, err)
		}
		if !bytes.Equal(body, want) {
			t.Errorf("%s: the render differs from %s (run with -update after checking the diff)", name, path)
		}
	}
	// No stale golden lingers without a fixture.
	files, err := filepath.Glob(filepath.Join("testdata", "golden", "*.xml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if _, ok := all[strings.TrimSuffix(filepath.Base(f), ".xml")]; !ok {
			t.Errorf("%s has no fixture", f)
		}
	}
}
