package ehf_test

import (
	"math/big"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/invoices/ehf"
	"github.com/vantigo-io/vantigo/server/internal/invoices/kid"
)

// The fixed documents the goldens are rendered from (EHF and KID design
// D11). Every organisation number passes MOD11, the Swedish one Luhn and the
// GLN its GS1 check, so the oracle's identifier rules (PEPPOL-COMMON-R040,
// R041, R049) hold; every amount is what the module's arithmetic would have
// stored (money.go): line gross = round(quantity × price), the allowance on
// the gross, VAT per (category, rate) on the summed nets.

// testPDF is the fixed small PDF blob every golden embeds — never the
// renderer's output, so a PDF layout change cannot move an EHF golden.
var testPDF = []byte("%PDF-1.4\n" +
	"1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n" +
	"2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n" +
	"3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 595 842]>>endobj\n" +
	"trailer<</Root 1 0 R>>\n%%EOF\n")

const (
	sellerOrg = "923456783"
	buyerOrg  = "987654325"
	soleOrg   = "876543214" // a sole proprietorship: neither VAT- nor Foretaksregisteret-registered
)

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("not a decimal: " + s)
	}
	return r
}

// line is a fixture line: quantity, unit price and discount give gross,
// allowance and net by the module's rule.
func line(id, description, unit, quantity, price, discount, category, rate string) ehf.Line {
	q, p, d := rat(quantity), rat(price), rat(discount)
	gross := rat(new(big.Rat).Mul(q, p).FloatString(2))
	allowance := rat(new(big.Rat).Quo(new(big.Rat).Mul(gross, d), big.NewRat(100, 1)).FloatString(2))
	return ehf.Line{
		ID: id, Description: description, Unit: unit, Quantity: q, UnitPrice: p, DiscountPercent: d,
		Gross: gross, Allowance: allowance, Net: new(big.Rat).Sub(gross, allowance), Category: category, Rate: rat(rate),
	}
}

func vat(category, rate, taxable, amount, reason string) ehf.VATRow {
	return ehf.VATRow{Category: category, Rate: rat(rate), Taxable: rat(taxable), Amount: rat(amount), ExemptionReason: reason}
}

func seller() ehf.Seller {
	return ehf.Seller{
		PeppolID: "0192:" + sellerOrg, Name: "Fjordkraft Konsult AS",
		Address:            ehf.Address{Line1: "Storgata 1", Line2: "3. etasje", PostalCode: "0155", City: "Oslo", Country: "NO"},
		OrganisationNumber: sellerOrg, VATRegistered: true, Foretaksregisteret: true, Email: "faktura@fjordkraft-konsult.example",
	}
}

func norwegianBuyer() ehf.Buyer {
	return ehf.Buyer{
		PeppolID: "0192:" + buyerOrg, Name: "Nordlys Bygg AS",
		Address:            ehf.Address{Line1: "Industriveien 12", PostalCode: "7080", City: "Heimdal", Country: "NO"},
		OrganisationNumber: buyerOrg,
	}
}

func payment() ehf.Payment {
	return ehf.Payment{BankAccount: "86011117947", IBAN: "NO9386011117947", BIC: "DNBANOKKXXX"}
}

// baseInvoice is a one-line 25 % invoice the other fixtures vary.
func baseInvoice(number string) ehf.Document {
	return ehf.Document{
		Kind: ehf.KindInvoice, Language: "nb", Number: number, IssueDate: "2026-10-01", DueDate: "2026-10-15",
		Currency: "NOK", BuyerReference: "Ola Nordmann", Seller: seller(), Buyer: norwegianBuyer(), Payment: payment(),
		Lines:    []ehf.Line{line("1", "Konsulenttimer", "timer", "10", "1200", "0", "S", "25")},
		VAT:      []ehf.VATRow{vat("S", "25", "12000.00", "3000.00", "")},
		NetTotal: rat("12000.00"), VATTotal: rat("3000.00"), GrossTotal: rat("15000.00"),
		PDF: testPDF, PDFName: "faktura-" + number + ".pdf",
	}
}

// fixtures are D11's: each named document, rendered into
// testdata/golden/<name>.xml.
func fixtures(t *testing.T) map[string]ehf.Document {
	t.Helper()
	out := map[string]ehf.Document{}

	// S at every seeded rate, plus Z, E, AE and G; a delivery date.
	every := baseInvoice("10042")
	every.DeliveryDate = "2026-09-30"
	every.Lines = []ehf.Line{
		line("1", "Konsulenttimer", "timer", "10", "1200", "0", "S", "25"),
		line("2", "Servering", "stk.", "4", "250", "0", "S", "15"),
		line("3", "Persontransport", "stk", "1", "800", "0", "S", "12"),
		line("4", "Råfisk", "kg", "2", "45", "0", "S", "11.11"),
		line("5", "Aviser", "", "3", "30", "0", "Z", "0"),
		line("6", "Utleie av lokale", "mnd", "1", "9000", "0", "E", "0"),
		line("7", "Byggtjenester", "time", "5", "600", "0", "AE", "0"),
		line("8", "Eksportvare", "pk", "1", "1500", "0", "G", "0"),
	}
	every.VAT = []ehf.VATRow{
		vat("S", "25", "12000.00", "3000.00", ""),
		vat("S", "15", "1000.00", "150.00", ""),
		vat("S", "12", "800.00", "96.00", ""),
		vat("S", "11.11", "90.00", "10.00", ""),
		vat("AE", "0", "3000.00", "0.00", "Omvendt avgiftsplikt – Merverdiavgift ikke beregnet"),
		vat("E", "0", "9000.00", "0.00", "Unntatt fra merverdiavgift (mval. kap. 3)"),
		vat("G", "0", "1500.00", "0.00", "Utførsel av varer og tjenester"),
		vat("Z", "0", "90.00", "0.00", "Fritatt for merverdiavgift"),
	}
	every.NetTotal, every.VATTotal, every.GrossTotal = rat("27480.00"), rat("3256.00"), rat("30736.00")
	out["invoice-every-category"] = every

	// O is exclusive to a seller outside the VAT register: a second fixture.
	sole := baseInvoice("17")
	sole.Seller = ehf.Seller{
		PeppolID: "0192:" + soleOrg, Name: "Hageglede Ole Hansen",
		Address:            ehf.Address{Line1: "Bjørkeveien 3", PostalCode: "3511", City: "Hønefoss", Country: "NO"},
		OrganisationNumber: soleOrg,
	}
	sole.Payment = ehf.Payment{BankAccount: "12345678903"}
	sole.Lines = []ehf.Line{line("1", "Hagearbeid", "timer", "6", "450", "0", "O", "0")}
	sole.VAT = []ehf.VATRow{vat("O", "0", "2700.00", "0.00", "Selger er ikke registrert i Merverdiavgiftsregisteret")}
	sole.NetTotal, sole.VATTotal, sole.GrossTotal = rat("2700.00"), rat("0.00"), rat("2700.00")
	sole.PDFName = "faktura-17.pdf"
	out["invoice-not-vat-registered"] = sole

	// A line discount, and an order reference in place of the buyer's.
	discount := baseInvoice("10046")
	discount.BuyerReference, discount.OrderReference = "", "PO-4471"
	discount.Lines = []ehf.Line{
		line("1", "Kontorstoler", "stk", "4", "2499", "10", "S", "25"),
		line("2", "Montering", "timer", "2", "650", "0", "S", "25"),
	}
	discount.VAT = []ehf.VATRow{vat("S", "25", "10296.40", "2574.10", "")}
	discount.NetTotal, discount.VATTotal, discount.GrossTotal = rat("10296.40"), rat("2574.10"), rat("12870.50")
	out["invoice-discount"] = discount

	// A foreign buyer, in English, paid to the IBAN with the BIC; export (G).
	foreign := baseInvoice("10047")
	foreign.Language = "en"
	foreign.BuyerReference = "Anna Svensson"
	foreign.Buyer = ehf.Buyer{
		PeppolID: "0007:5560360793", Name: "Svea Maskin AB",
		Address:   ehf.Address{Line1: "Kungsgatan 8", PostalCode: "111 43", City: "Stockholm", Region: "Stockholms län", Country: "SE"},
		ForeignID: "SE5560360793",
	}
	foreign.Lines = []ehf.Line{line("1", "Machine parts", "pcs", "10", "320", "0", "G", "0")}
	foreign.VAT = []ehf.VATRow{vat("G", "0", "3200.00", "0.00", "Utførsel av varer og tjenester")}
	foreign.NetTotal, foreign.VATTotal, foreign.GrossTotal = rat("3200.00"), rat("0.00"), rat("3200.00")
	foreign.PDFName = "invoice-10047.pdf"
	out["invoice-foreign-buyer"] = foreign

	// A person: a name and an address, no legal id.
	person := baseInvoice("10048")
	person.BuyerReference = "Kari Nordmann"
	person.Buyer = ehf.Buyer{
		PeppolID: "0088:7080000000012", Name: "Kari Nordmann", Person: true,
		Address: ehf.Address{Line1: "Ullevålsveien 40", PostalCode: "0171", City: "Oslo", Country: "NO"},
	}
	person.Lines = []ehf.Line{line("1", "Pianotime", "time", "4", "500", "0", "S", "25")}
	person.VAT = []ehf.VATRow{vat("S", "25", "2000.00", "500.00", "")}
	person.NetTotal, person.VATTotal, person.GrossTotal = rat("2000.00"), rat("500.00"), rat("2500.00")
	out["invoice-person"] = person

	// A credit note: the original's number and date, positive amounts, the
	// credit sentence for terms, no payment means.
	credit := baseInvoice("10049")
	credit.Kind, credit.DueDate, credit.Payment = ehf.KindCreditNote, "", payment()
	credit.Original = &ehf.DocumentReference{Number: "10040", IssueDate: "2026-09-15"}
	credit.Lines = []ehf.Line{line("1", "Konsulenttimer", "timer", "5", "1200", "0", "S", "25")}
	credit.VAT = []ehf.VATRow{vat("S", "25", "6000.00", "1500.00", "")}
	credit.NetTotal, credit.VATTotal, credit.GrossTotal = rat("6000.00"), rat("1500.00"), rat("7500.00")
	credit.PDFName = "kreditnota-10049.pdf"
	out["credit-note"] = credit

	// A partial credit note: one of four discounted chairs returned.
	partial := credit
	partial.Number = "10050"
	partial.Original = &ehf.DocumentReference{Number: "10046", IssueDate: "2026-10-01"}
	partial.BuyerReference, partial.OrderReference = "", "PO-4471"
	partial.Lines = []ehf.Line{line("1", "Kontorstoler", "stk", "1", "2499", "10", "S", "25")}
	partial.VAT = []ehf.VATRow{vat("S", "25", "2249.10", "562.28", "")}
	partial.NetTotal, partial.VATTotal, partial.GrossTotal = rat("2249.10"), rat("562.28"), rat("2811.38")
	partial.PDFName = "kreditnota-10050.pdf"
	out["credit-note-partial"] = partial

	// The final credit note's squaring row: an earlier partial note
	// over-reversed the 15 % row, so this one carries it with a taxable
	// amount of 0.00 and the negative remainder (reference: "Squaring the
	// øre"), as stored (D4).
	squaring := credit
	squaring.Number = "10051"
	squaring.Original = &ehf.DocumentReference{Number: "10039", IssueDate: "2026-09-10"}
	squaring.Lines = []ehf.Line{line("1", "Konsulenttimer", "time", "1", "100", "0", "S", "25")}
	squaring.VAT = []ehf.VATRow{vat("S", "25", "100.00", "25.00", ""), vat("S", "15", "0.00", "-0.03", "")}
	squaring.NetTotal, squaring.VATTotal, squaring.GrossTotal = rat("100.00"), rat("24.97"), rat("124.97")
	squaring.PDFName = "kreditnota-10051.pdf"
	out["credit-note-squaring"] = squaring

	// A KID under each algorithm.
	for _, c := range []struct {
		name, number, algorithm string
		length                  int
	}{{"invoice-kid-mod10", "10052", kid.Mod10, 8}, {"invoice-kid-mod11", "10053", kid.Mod11, 10}} {
		d := baseInvoice(c.number)
		n := new(big.Int)
		n.SetString(c.number, 10)
		k, err := kid.Compute(n.Int64(), c.length, c.algorithm)
		if err != nil {
			t.Fatalf("kid.Compute(%s): %v", c.number, err)
		}
		d.Payment.KID, d.Payment.KIDAlgorithm = k, c.algorithm
		out[c.name] = d
	}

	// A delivery period and a place of delivery with a country.
	period := baseInvoice("10054")
	period.DeliveryFrom, period.DeliveryTo = "2026-09-01", "2026-09-30"
	period.DeliveryPlace = &ehf.Address{Line1: "Lagerveien 5", PostalCode: "0581", City: "Oslo", Country: "NO"}
	period.Lines = []ehf.Line{line("1", "Vakttjenester", "mnd", "1", "15000", "0", "S", "25")}
	period.VAT = []ehf.VATRow{vat("S", "25", "15000.00", "3750.00", "")}
	period.NetTotal, period.VATTotal, period.GrossTotal = rat("15000.00"), rat("3750.00"), rat("18750.00")
	out["invoice-delivery-period-and-place"] = period

	return out
}

// fixture is one named fixture.
func fixture(t *testing.T, name string) ehf.Document {
	t.Helper()
	d, ok := fixtures(t)[name]
	if !ok {
		t.Fatalf("no fixture %q", name)
	}
	return d
}
