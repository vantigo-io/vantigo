package ehf

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"math/big"
	"regexp"
	"strconv"

	"github.com/vantigo-io/vantigo/server/internal/invoices/kid"
)

// This file is the pre-check (EHF and KID design D11): what can be judged on
// the rendered bytes before anything leaves the server. Precheck is what a
// request may be refused in words (409 ehf_invalid, naming the rule ids);
// Invariants is what only the module itself can get wrong — a broken
// invariant is a 500 and an error log, never the user's to mend. Neither
// replaces the oracle (the official XSD and Schematron, run in CI).

// Rule is one failed check: the official rule id where there is one, else
// the module's own name for it.
type Rule struct {
	ID      string
	Message string
}

// The rule ids the pre-check and the invariants report. The official ids
// are the Peppol BIS Billing 3.0 and EN 16931 artefacts' own.
const (
	RuleBuyerReference      = "PEPPOL-EN16931-R003" // a buyer reference or an order reference
	RuleBuyerEndpoint       = "PEPPOL-EN16931-R010" // the buyer's electronic address
	RuleEndpointScheme      = "PEPPOL-EN16931-CL008"
	RuleSellerVATID         = "NO-R-001"
	RuleCategoryK           = "vat_category_k_unsupported" // D4: K needs the buyer's VAT id, which the module does not hold
	RuleLineTotal           = "BR-CO-10"
	RuleTaxExclusive        = "BR-CO-13"
	RuleTaxInclusive        = "BR-CO-15"
	RuleTaxTotal            = "BR-CO-14"
	RuleTaxableSum          = "vat_taxable_sum"        // the VAT rows' taxable amounts sum to the net; no single EN 16931 rule says so
	RuleKID                 = "kid_invalid"            // the KID fails its stored algorithm, or the PaymentID is not it
	RulePaymentIDWithoutKID = "payment_id_without_kid" // D3: never a PaymentID without a KID
	RuleAttachment          = "pdf_attachment_missing"
	RuleUnitCode            = "unit_code_unknown"
)

// root parses a document and checks it is a UBL Invoice or CreditNote.
func root(doc []byte) (*Node, error) {
	r, err := Parse(doc)
	if err != nil {
		return nil, err
	}
	invoice := r.Name.Space == nsInvoice && r.Name.Local == "Invoice"
	creditNote := r.Name.Space == nsCreditNote && r.Name.Local == "CreditNote"
	if !invoice && !creditNote {
		return nil, fmt.Errorf("ehf: %s in %q is not a UBL Invoice or CreditNote", r.Name.Local, r.Name.Space)
	}
	return r, nil
}

// noVATID is NO-R-001's expression: a seller VAT id that starts with NO is
// NO, nine digits that pass MOD11, and MVA.
var noVATID = regexp.MustCompile(`^NO([0-9]{9})MVA$`)

// Precheck is the user-facing rules, read from the XML alone, so it judges
// a hand-made document as it judges a render: a buyer or order reference
// (PEPPOL-EN16931-R003); the buyer's endpoint present (R010) with its scheme
// on the EAS list (CL008); the seller's Norwegian VAT id in shape (NO-R-001);
// no category K (D4). The error is for a document it cannot read at all.
func Precheck(doc []byte) ([]Rule, error) {
	r, err := root(doc)
	if err != nil {
		return nil, err
	}
	var rules []Rule
	if r.Value("cbc:BuyerReference") == "" && r.Value("cac:OrderReference", "cbc:ID") == "" {
		rules = append(rules, Rule{RuleBuyerReference, "a buyer reference or an order reference is required"})
	}
	if e := r.First("cac:AccountingCustomerParty", "cac:Party", "cbc:EndpointID"); e == nil || e.Text == "" {
		rules = append(rules, Rule{RuleBuyerEndpoint, "the buyer has no electronic address"})
	} else if !EASScheme(e.Attr("schemeID")) {
		rules = append(rules, Rule{RuleEndpointScheme, fmt.Sprintf("the buyer's endpoint scheme %q is not on the EAS list", e.Attr("schemeID"))})
	}
	for _, s := range r.All("cac:AccountingSupplierParty", "cac:Party", "cac:PartyTaxScheme") {
		id := s.Value("cbc:CompanyID")
		if s.Value("cac:TaxScheme", "cbc:ID") != "VAT" || len(id) < 2 || id[:2] != "NO" {
			continue
		}
		if m := noVATID.FindStringSubmatch(id); m == nil || !organisationNumberValid(m[1]) {
			rules = append(rules, Rule{RuleSellerVATID, fmt.Sprintf("the seller's VAT id %q is not NO, a valid organisation number and MVA", id)})
		}
	}
	k := false
	for _, c := range append(r.Descendants("cac:ClassifiedTaxCategory"), r.Descendants("cac:TaxCategory")...) {
		k = k || c.Value("cbc:ID") == "K"
	}
	if k {
		rules = append(rules, Rule{RuleCategoryK, "VAT category K (intra-EEA) needs the buyer's VAT identifier, which the module does not hold"})
	}
	return rules, nil
}

// organisationNumberValid is the Norwegian organisation number's MOD11:
// weights 3, 2, 7, 6, 5, 4, 3, 2 over the first eight digits, and the ninth
// 11 − (sum mod 11), 0 for 11; 10 is never valid.
func organisationNumberValid(n string) bool {
	if len(n) != 9 {
		return false
	}
	sum := 0
	for i, w := range []int{3, 2, 7, 6, 5, 4, 3, 2} {
		sum += int(n[i]-'0') * w
	}
	check := (11 - sum%11) % 11
	return check != 10 && int(n[8]-'0') == check
}

// Invariants is what only the module can break, judged on the rendered
// bytes against the Document they came from: the totals re-summed (BR-CO-10,
// BR-CO-13, BR-CO-15) and the VAT rows re-summed against them (BR-CO-14,
// and the taxable amounts against the net); the KID re-verified against its stored algorithm and
// the PaymentID it and nothing else (D3); the PDF attached, with the
// Document's bytes when it has them; every unit code one of the table's.
func Invariants(doc []byte, d Document) ([]Rule, error) {
	r, err := root(doc)
	if err != nil {
		return nil, err
	}
	var rules []Rule
	bad := func(id, format string, args ...any) { rules = append(rules, Rule{id, fmt.Sprintf(format, args...)}) }

	total := r.First("cac:LegalMonetaryTotal")
	lineExtension, err := amount(total, "cbc:LineExtensionAmount")
	if err != nil {
		return nil, err
	}
	sum := new(big.Rat)
	lines := append(r.All("cac:InvoiceLine"), r.All("cac:CreditNoteLine")...)
	for _, l := range lines {
		net, err := amount(l, "cbc:LineExtensionAmount")
		if err != nil {
			return nil, err
		}
		sum.Add(sum, net)
	}
	if sum.Cmp(lineExtension) != 0 {
		bad(RuleLineTotal, "the lines sum to %s, the total says %s", sum.FloatString(2), lineExtension.FloatString(2))
	}
	exclusive, err := amount(total, "cbc:TaxExclusiveAmount")
	if err != nil {
		return nil, err
	}
	expected := new(big.Rat).Set(lineExtension)
	for _, adj := range []struct {
		name string
		sign int64
	}{{"cbc:ChargeTotalAmount", 1}, {"cbc:AllowanceTotalAmount", -1}} {
		if total.Has(adj.name) {
			v, err := amount(total, adj.name)
			if err != nil {
				return nil, err
			}
			expected.Add(expected, v.Mul(v, big.NewRat(adj.sign, 1)))
		}
	}
	if exclusive.Cmp(expected) != 0 {
		bad(RuleTaxExclusive, "the tax-exclusive amount is %s, the lines give %s", exclusive.FloatString(2), expected.FloatString(2))
	}
	inclusive, err := amount(total, "cbc:TaxInclusiveAmount")
	if err != nil {
		return nil, err
	}
	tax, err := amount(r.First("cac:TaxTotal"), "cbc:TaxAmount")
	if err != nil {
		return nil, err
	}
	if inclusive.Cmp(new(big.Rat).Add(exclusive, tax)) != 0 {
		bad(RuleTaxInclusive, "the tax-inclusive amount is %s, not %s plus %s", inclusive.FloatString(2), exclusive.FloatString(2), tax.FloatString(2))
	}

	subtotalTax, subtotalTaxable := new(big.Rat), new(big.Rat)
	for _, sub := range r.All("cac:TaxTotal", "cac:TaxSubtotal") {
		v, err := amount(sub, "cbc:TaxAmount")
		if err != nil {
			return nil, err
		}
		subtotalTax.Add(subtotalTax, v)
		if v, err = amount(sub, "cbc:TaxableAmount"); err != nil {
			return nil, err
		}
		subtotalTaxable.Add(subtotalTaxable, v)
	}
	if subtotalTax.Cmp(tax) != 0 {
		bad(RuleTaxTotal, "the VAT rows sum to %s, the VAT total says %s", subtotalTax.FloatString(2), tax.FloatString(2))
	}
	if subtotalTaxable.Cmp(exclusive) != 0 {
		bad(RuleTaxableSum, "the VAT rows' taxable amounts sum to %s, the tax-exclusive amount is %s", subtotalTaxable.FloatString(2), exclusive.FloatString(2))
	}

	paymentID := r.Value("cac:PaymentMeans", "cbc:PaymentID")
	switch {
	case d.Payment.KID != "":
		number, err := strconv.ParseInt(d.Number, 10, 64)
		if err != nil || !kid.Verify(d.Payment.KID, d.Payment.KIDAlgorithm, number) {
			bad(RuleKID, "the KID does not verify against its stored algorithm")
		} else if paymentID != d.Payment.KID {
			bad(RuleKID, "the payment id is not the document's KID")
		}
	case paymentID != "":
		bad(RulePaymentIDWithoutKID, "a payment id without a KID")
	}

	attached := false
	for _, obj := range r.All("cac:AdditionalDocumentReference", "cac:Attachment", "cbc:EmbeddedDocumentBinaryObject") {
		body, err := base64.StdEncoding.DecodeString(obj.Text)
		if err != nil || len(body) == 0 || obj.Attr("mimeCode") != "application/pdf" || obj.Attr("filename") == "" {
			continue
		}
		attached = attached || len(d.PDF) == 0 || bytes.Equal(body, d.PDF)
	}
	if !attached {
		bad(RuleAttachment, "the PDF is not attached")
	}

	for _, l := range lines {
		for _, q := range append(l.All("cbc:InvoicedQuantity"), l.All("cbc:CreditedQuantity")...) {
			if code := q.Attr("unitCode"); !knownUnitCode(code) {
				bad(RuleUnitCode, "line %s: unit code %q is not in the table", l.Value("cbc:ID"), code)
			}
		}
	}
	return rules, nil
}

// amount is the decimal at path below n.
func amount(n *Node, path ...string) (*big.Rat, error) {
	text := n.Value(path...)
	v, ok := new(big.Rat).SetString(text)
	if !ok {
		return nil, fmt.Errorf("ehf: %v is %q, not a decimal", path, text)
	}
	return v, nil
}
