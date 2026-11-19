package invoices

import (
	"slices"
	"testing"
)

// § 5-1-2: a buyer in the VAT register is printed "<org nr> MVA", as the
// seller is; one that is not, and a foreign buyer, print as before.
func TestBuyerLines_TheMVASuffix(t *testing.T) {
	l := labels["nb"]
	registered := buyerLines(pdfParty{name: "Nordlys Bygg AS", organisationNumber: "923609016", vatRegistered: true}, l)
	if !slices.Contains(registered, "Org.nr. 923 609 016 MVA") {
		t.Errorf("a VAT-registered buyer = %q, want its number with MVA", registered)
	}
	plain := buyerLines(pdfParty{name: "Nordlys Bygg AS", organisationNumber: "923609016"}, l)
	if !slices.Contains(plain, "Org.nr. 923 609 016") {
		t.Errorf("a buyer outside the VAT register = %q, want its number alone", plain)
	}
	foreign := buyerLines(pdfParty{name: "Svenska AB", foreignID: "SE5560360793", vatRegistered: true}, l)
	if !slices.Contains(foreign, "VAT/Reg. no. SE5560360793") {
		t.Errorf("a foreign buyer = %q, want its id as before", foreign)
	}
}
