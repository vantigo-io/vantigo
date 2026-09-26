package invoices

import "github.com/vantigo-io/vantigo/server/internal/invoices/store"

// sellerMissingFields are the seller fields issuing still needs (D2), by their
// camelCase wire names, in the order the settings form shows them. § 5-1-2
// requires the name and the organisation number; the address and the bank
// account are a sensible gate, not a legal requirement. Empty means complete.
func sellerMissingFields(row store.InvoicesSetting) []string {
	missing := []string{}
	for _, f := range []struct {
		name  string
		value string
	}{
		{"legalName", row.LegalName},
		{"organisationNumber", row.OrganisationNumber},
		{"addressLine1", row.AddressLine1},
		{"postalCode", row.PostalCode},
		{"city", row.City},
		{"bankAccount", row.BankAccount},
	} {
		if f.value == "" {
			missing = append(missing, f.name)
		}
	}
	return missing
}
