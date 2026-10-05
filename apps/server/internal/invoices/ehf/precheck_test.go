package ehf_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/invoices/ehf"
	"github.com/vantigo-io/vantigo/server/internal/invoices/kid"
)

func ids(rules []ehf.Rule) []string {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.ID)
	}
	slices.Sort(out)
	return out
}

func mustRender(t *testing.T, d ehf.Document) []byte {
	t.Helper()
	body, err := ehf.Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return body
}

// tamper replaces exactly one occurrence of old in a rendered document: how
// a test makes bytes the writer would never write.
func tamper(t *testing.T, doc []byte, old, replacement string) []byte {
	t.Helper()
	if n := bytes.Count(doc, []byte(old)); n != 1 {
		t.Fatalf("%q occurs %d times, want once", old, n)
	}
	return bytes.Replace(doc, []byte(old), []byte(replacement), 1)
}

func precheck(t *testing.T, doc []byte) []string {
	t.Helper()
	rules, err := ehf.Precheck(doc)
	if err != nil {
		t.Fatalf("Precheck: %v", err)
	}
	return ids(rules)
}

func invariants(t *testing.T, doc []byte, d ehf.Document) []string {
	t.Helper()
	rules, err := ehf.Invariants(doc, d)
	if err != nil {
		t.Fatalf("Invariants: %v", err)
	}
	return ids(rules)
}

// Every golden passes the pre-check and the invariants: nothing the module
// renders from a sound Document is refused.
func TestPrecheck_EveryFixturePasses(t *testing.T) {
	t.Parallel()
	for name, d := range fixtures(t) {
		doc := mustRender(t, d)
		if got := precheck(t, doc); len(got) != 0 {
			t.Errorf("%s: Precheck = %v", name, got)
		}
		if got := invariants(t, doc, d); len(got) != 0 {
			t.Errorf("%s: Invariants = %v", name, got)
		}
	}
}

// Each user-facing rule on a document that breaks it, and only it.
func TestPrecheck_EachRule(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		doc  func(t *testing.T) []byte
		want []string
	}{
		"neither reference (R003)": {func(t *testing.T) []byte {
			d := fixture(t, "invoice-person")
			d.BuyerReference = ""
			return mustRender(t, d)
		}, []string{ehf.RuleBuyerReference}},
		"an order reference is enough": {func(t *testing.T) []byte {
			d := fixture(t, "invoice-person")
			d.BuyerReference, d.OrderReference = "", "PO-1"
			return mustRender(t, d)
		}, nil},
		"a scheme off the EAS list (CL008)": {func(t *testing.T) []byte {
			d := fixture(t, "invoice-person")
			d.Buyer.PeppolID = "9999:7080000000012"
			return mustRender(t, d)
		}, []string{ehf.RuleEndpointScheme}},
		"no buyer endpoint (R010)": {func(t *testing.T) []byte {
			return tamper(t, mustRender(t, fixture(t, "invoice-person")),
				`<cbc:EndpointID schemeID="0088">7080000000012</cbc:EndpointID>`, "")
		}, []string{ehf.RuleBuyerEndpoint}},
		"a seller VAT id failing MOD11 (NO-R-001)": {func(t *testing.T) []byte {
			d := fixture(t, "invoice-person")
			d.Seller.OrganisationNumber = "923456784"
			return mustRender(t, d)
		}, []string{ehf.RuleSellerVATID}},
		"a seller VAT id out of shape (NO-R-001)": {func(t *testing.T) []byte {
			return tamper(t, mustRender(t, fixture(t, "invoice-person")), "NO"+sellerOrg+"MVA", "NO"+sellerOrg)
		}, []string{ehf.RuleSellerVATID}},
		"a foreign seller VAT id is not NO-R-001's": {func(t *testing.T) []byte {
			return tamper(t, mustRender(t, fixture(t, "invoice-person")), "NO"+sellerOrg+"MVA", "SE556036079301")
		}, nil},
		"a K line (D4)": {func(t *testing.T) []byte {
			d := fixture(t, "invoice-person")
			d.Lines[0].Category, d.Lines[0].Rate = "K", rat("0")
			d.VAT = []ehf.VATRow{vat("K", "0", "2000.00", "0.00", "")}
			d.VATTotal, d.GrossTotal = rat("0.00"), rat("2000.00")
			return mustRender(t, d)
		}, []string{ehf.RuleCategoryK}},
		"two at once": {func(t *testing.T) []byte {
			d := fixture(t, "invoice-person")
			d.BuyerReference, d.Buyer.PeppolID = "", "9999:1"
			return mustRender(t, d)
		}, []string{ehf.RuleEndpointScheme, ehf.RuleBuyerReference}},
	} {
		got := precheck(t, c.doc(t))
		want := slices.Clone(c.want)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s: Precheck = %v, want %v", name, got, want)
		}
	}
}

// A document the pre-check cannot read is an error, not a refusal.
func TestPrecheck_UnreadableIsAnError(t *testing.T) {
	t.Parallel()
	for _, doc := range []string{"", "not xml", `<Invoice xmlns="urn:other"/>`, `<?xml version="1.0"?><Order/>`} {
		if _, err := ehf.Precheck([]byte(doc)); err == nil {
			t.Errorf("Precheck(%q) succeeded", doc)
		}
		if _, err := ehf.Invariants([]byte(doc), ehf.Document{}); err == nil {
			t.Errorf("Invariants(%q) succeeded", doc)
		}
	}
}

// Each invariant on a Document (or its bytes) the module could only get wrong
// itself: totals that do not sum, a KID that fails its algorithm or is not
// the payment id, a payment id without a KID, a missing or different PDF, a
// unit code outside the table.
func TestInvariants_EachCheck(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		check func(t *testing.T) []string
		want  []string
	}{
		"the net total is not the lines' (BR-CO-10)": {func(t *testing.T) []string {
			d := fixture(t, "invoice-discount")
			d.NetTotal = rat("10296.41")
			d.GrossTotal = rat("12870.51")
			return invariants(t, mustRender(t, d), d)
		}, []string{ehf.RuleLineTotal, ehf.RuleTaxableSum}},
		"the tax-exclusive amount is not the line extension (BR-CO-13)": {func(t *testing.T) []string {
			d := fixture(t, "invoice-person")
			doc := tamper(t, mustRender(t, d), `<cbc:TaxExclusiveAmount currencyID="NOK">2000.00`, `<cbc:TaxExclusiveAmount currencyID="NOK">1900.00`)
			return invariants(t, doc, d)
		}, []string{ehf.RuleTaxExclusive, ehf.RuleTaxInclusive, ehf.RuleTaxableSum}},
		"the gross is not net plus VAT (BR-CO-15)": {func(t *testing.T) []string {
			d := fixture(t, "invoice-person")
			d.GrossTotal = rat("2500.01")
			return invariants(t, mustRender(t, d), d)
		}, []string{ehf.RuleTaxInclusive}},
		"the VAT rows do not sum to the VAT total (BR-CO-14)": {func(t *testing.T) []string {
			d := fixture(t, "invoice-every-category")
			d.VAT[0].Amount = rat("2999.00")
			return invariants(t, mustRender(t, d), d)
		}, []string{ehf.RuleTaxTotal}},
		"the VAT rows' taxable amounts do not sum to the net": {func(t *testing.T) []string {
			d := fixture(t, "invoice-every-category")
			d.VAT[0].Taxable = rat("11000.00")
			return invariants(t, mustRender(t, d), d)
		}, []string{ehf.RuleTaxableSum}},
		"a KID failing its stored algorithm": {func(t *testing.T) []string {
			d := fixture(t, "invoice-kid-mod10")
			d.Payment.KIDAlgorithm = kid.Mod11
			return invariants(t, mustRender(t, d), d)
		}, []string{ehf.RuleKID}},
		"a KID of another number": {func(t *testing.T) []string {
			d := fixture(t, "invoice-kid-mod10")
			d.Number = "10053"
			return invariants(t, mustRender(t, d), d)
		}, []string{ehf.RuleKID}},
		"a payment id that is not the KID": {func(t *testing.T) []string {
			d := fixture(t, "invoice-kid-mod10")
			doc := tamper(t, mustRender(t, d), "<cbc:PaymentID>"+d.Payment.KID+"<", "<cbc:PaymentID>10052<")
			return invariants(t, doc, d)
		}, []string{ehf.RuleKID}},
		"a payment id without a KID": {func(t *testing.T) []string {
			d := fixture(t, "invoice-kid-mod10")
			doc := mustRender(t, d)
			d.Payment.KID, d.Payment.KIDAlgorithm = "", ""
			return invariants(t, doc, d)
		}, []string{ehf.RulePaymentIDWithoutKID}},
		"no PDF attached": {func(t *testing.T) []string {
			d := fixture(t, "invoice-person")
			d.PDF = nil
			doc := mustRender(t, d)
			return invariants(t, doc, d)
		}, []string{ehf.RuleAttachment}},
		"another PDF attached": {func(t *testing.T) []string {
			d := fixture(t, "invoice-person")
			doc := mustRender(t, d)
			d.PDF = []byte("%PDF-1.4 another")
			return invariants(t, doc, d)
		}, []string{ehf.RuleAttachment}},
		"a negative price (BR-27)": {func(t *testing.T) []string {
			d := fixture(t, "invoice-final-settlement")
			doc := tamper(t, mustRender(t, d), `>125000.0000<`, `>-125000.0000<`)
			return invariants(t, doc, d)
		}, []string{ehf.RulePriceNegative}},
		"a settlement's net is not its lines', negative ones counted (BR-CO-10)": {func(t *testing.T) []string {
			d := fixture(t, "invoice-final-settlement")
			d.NetTotal = rat("400000.00")
			d.GrossTotal = rat("443750.00")
			return invariants(t, mustRender(t, d), d)
		}, []string{ehf.RuleLineTotal, ehf.RuleTaxableSum}},
		"a settlement's tax-exclusive amount is not its lines' (BR-CO-13)": {func(t *testing.T) []string {
			d := fixture(t, "invoice-final-settlement")
			doc := tamper(t, mustRender(t, d), `<cbc:TaxExclusiveAmount currencyID="NOK">175000.00`, `<cbc:TaxExclusiveAmount currencyID="NOK">400000.00`)
			return invariants(t, doc, d)
		}, []string{ehf.RuleTaxExclusive, ehf.RuleTaxInclusive, ehf.RuleTaxableSum}},
		"a unit code outside the table": {func(t *testing.T) []string {
			d := fixture(t, "invoice-person")
			doc := tamper(t, mustRender(t, d), `unitCode="HUR"`, `unitCode="TNE"`)
			return invariants(t, doc, d)
		}, []string{ehf.RuleUnitCode}},
	} {
		got := c.check(t)
		want := slices.Clone(c.want)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s: Invariants = %v, want %v", name, got, want)
		}
	}
}

type manifest struct {
	Fixtures []struct {
		File   string   `json:"file"`
		From   string   `json:"from"`
		Rules  []string `json:"rules"`
		Module []string `json:"module"`
	} `json:"fixtures"`
}

// knownRules is every id the pre-check and the invariants can report.
var knownRules = []string{
	ehf.RuleBuyerReference, ehf.RuleBuyerEndpoint, ehf.RuleEndpointScheme, ehf.RuleSellerVATID, ehf.RuleCategoryK,
	ehf.RuleLineTotal, ehf.RuleTaxExclusive, ehf.RuleTaxInclusive, ehf.RuleTaxTotal, ehf.RuleTaxableSum,
	ehf.RuleKID, ehf.RulePaymentIDWithoutKID,
	ehf.RuleAttachment, ehf.RuleUnitCode, ehf.RulePriceNegative,
}

// The pre-check agrees with the oracle's manifest where it has the rule:
// every id the manifest lists for a fixture that the pre-check or the
// invariants know is reported on it, and nothing they know is reported that
// the manifest does not list. The invariants run against an empty Document —
// the fixtures are bytes, not renders — so only their XML-alone checks
// (the totals, the attachment, the unit codes) speak. Every manifest entry
// names a fixture that exists, and every fixture is in the manifest.
func TestPrecheck_AgreesWithTheManifest(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("testdata", "invalid", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	matched := 0
	for _, f := range m.Fixtures {
		listed[f.File] = true
		doc, err := os.ReadFile(filepath.Join("testdata", "invalid", f.File))
		if err != nil {
			t.Errorf("%s: %v", f.File, err)
			continue
		}
		if len(f.Rules) == 0 {
			t.Errorf("%s: the manifest names no rule", f.File)
		}
		if _, err := os.Stat(filepath.Join("testdata", f.From)); err != nil {
			t.Errorf("%s: its golden %s: %v", f.File, f.From, err)
		}
		pre, err := ehf.Precheck(doc)
		if err != nil {
			t.Fatalf("%s: Precheck: %v", f.File, err)
		}
		inv, err := ehf.Invariants(doc, ehf.Document{})
		if err != nil {
			t.Fatalf("%s: Invariants: %v", f.File, err)
		}
		reported := ids(append(pre, inv...))
		expected := append(slices.Clone(f.Rules), f.Module...)
		for _, id := range expected {
			if slices.Contains(knownRules, id) {
				matched++
				if !slices.Contains(reported, id) {
					t.Errorf("%s: the manifest lists %s, the pre-check did not report it (reported %v)", f.File, id, reported)
				}
			}
		}
		for _, id := range reported {
			if !slices.Contains(expected, id) {
				t.Errorf("%s: reported %s, which the manifest does not list", f.File, id)
			}
		}
	}
	if matched < 5 {
		t.Errorf("only %d manifest rules are the pre-check's; the agreement checks too little", matched)
	}
	files, err := filepath.Glob(filepath.Join("testdata", "invalid", "*.xml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if !listed[filepath.Base(f)] {
			t.Errorf("%s is not in the manifest", f)
		}
	}
}
