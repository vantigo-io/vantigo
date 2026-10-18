package customers

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
)

// This file is the suggested mapping (customers import wizard design D3): the
// mapping the page starts from, in three tiers, the first that fits a column
// winning and a field taken by the first column that claims it — the mapping
// last saved on an import with the same header (a failed-rows file, next
// month's export from the same system), the field's own JSON name (the export
// and the template), and the alias table below, the words these files use. A
// suggestion is only a suggestion: the mapping the person saves is the mapping.

// importFieldAliases are the header words that mean a field, matched after
// headerKey: lowercased, in NFC, everything but letters and digits dropped, so
// "Org.nr", "org nr" and "ORGNR" are one word. A word that would fit two fields
// is given to the one a Norwegian file more often means by it ("land" and
// "country" are the postal address's, "adresse" the postal line; the invoice
// address's words all say faktura or invoice). The JSON names themselves are
// not listed: they are the second tier, read from the field table.
var importFieldAliases = map[string][]string{
	"customerNumber":    {"kundenummer", "kundenr", "customerno", "customernr", "custno", "kundeid", "customerid"},
	"name":              {"navn", "kundenavn", "firmanavn", "firma", "selskap", "company", "companyname", "customername", "customer", "kunde"},
	"type":              {"kundetype", "customertype"},
	"status":            {"kundestatus", "customerstatus"},
	"legalCountry":      {"juridiskland", "registreringsland", "legalcountrycode"},
	"legalType":         {"juridisktype", "identitetstype", "identitytype"},
	"legalId":           {"orgnr", "orgnummer", "organisasjonsnummer", "organisasjonsnr", "organisationnumber", "organizationnumber", "orgno", "vatnumber", "mvanummer", "fnr", "legalidentifier"},
	"legalName":         {"juridisknavn", "registrertnavn", "foretaksnavn", "registeredname"},
	"email":             {"epost", "epostadresse", "mail", "emailaddress", "epostadr"},
	"phone":             {"telefon", "telefonnummer", "tlf", "tlfnr", "mobil", "mobile", "mobilnummer", "phonenumber", "phoneno"},
	"website":           {"web", "nettside", "hjemmeside", "url", "www", "webside", "homepage"},
	"postalLine1":       {"adresse", "adresse1", "adresselinje1", "gateadresse", "postadresse", "address", "address1", "addressline1", "street", "streetaddress"},
	"postalLine2":       {"adresse2", "adresselinje2", "address2", "addressline2"},
	"postalPostalCode":  {"postnummer", "postnr", "postalcode", "postcode", "zip", "zipcode"},
	"postalCity":        {"poststed", "by", "sted", "city", "town"},
	"postalRegion":      {"fylke", "region", "state", "province"},
	"postalCountry":     {"land", "country", "landkode", "countrycode"},
	"invoiceLine1":      {"fakturaadresse", "fakturaadresse1", "fakturaadresselinje1", "billingaddress", "billingaddress1", "invoiceaddress", "invoiceaddress1", "invoiceaddressline1"},
	"invoiceLine2":      {"fakturaadresse2", "fakturaadresselinje2", "billingaddress2", "invoiceaddress2", "invoiceaddressline2"},
	"invoicePostalCode": {"fakturapostnummer", "fakturapostnr", "billingpostalcode", "billingzip", "invoicezip"},
	"invoiceCity":       {"fakturapoststed", "fakturaby", "billingcity", "invoicecity"},
	"invoiceRegion":     {"fakturafylke", "billingregion", "billingstate", "invoicestate"},
	"invoiceCountry":    {"fakturaland", "billingcountry"},
	"invoiceEmail":      {"fakturaepost", "fakturamail", "fakturaepostadresse", "billingemail", "invoicemail"},
	"reminderEmail":     {"purreepost", "purremail", "reminderemailaddress"},
	"paymentTermsDays":  {"betalingsbetingelser", "betalingsfrist", "kredittdager", "kredittid", "forfallsdager", "paymentterms", "paymentdays", "creditdays", "terms"},
	"currency":          {"valuta", "valutakode", "currencycode"},
	"language":          {"sprak", "språk", "fakturaspråk", "fakturasprak", "languagecode"},
	"invoiceDelivery":   {"fakturalevering", "leveringsmate", "leveringsmåte", "fakturaformat", "deliverymethod", "invoicedeliverymethod"},
	"reminderDelivery":  {"purrelevering", "reminderdeliverymethod"},
	"peppolId":          {"peppol", "peppolid", "ehfadresse", "ehf", "peppoladdress", "elektroniskadresse"},
	"gln":               {"glnnummer", "ean", "eannummer", "lokasjonsnummer"},
	"buyerReference":    {"kjoperreferanse", "kjøperreferanse", "deresref", "deresreferanse", "yourref", "yourreference", "bestillerreferanse"},
	"defaultBillRate":   {"timepris", "timesats", "hourlyrate", "billrate", "rate"},
	"group":             {"gruppe", "kundegruppe", "kategori", "kundekategori", "category", "customergroup", "segment"},
	"tags":              {"tag", "merkelapper", "merkelapp", "etiketter", "etikett", "stikkord", "labels", "label", "nokkelord", "nøkkelord"},
}

// headerKey is a header as the alias table matches it: NFC, lowercased, and
// nothing but letters and digits, so punctuation and spacing never decide.
func headerKey(header string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(norm.NFC.String(header)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// aliasIndex is importFieldAliases inverted, plus every field's own JSON
// name, each under its headerKey. The JSON name wins over an alias that is the
// same word ("group" is both), which the construction order guarantees.
var aliasIndex = func() map[string]string {
	index := map[string]string{}
	for field, aliases := range importFieldAliases {
		for _, alias := range aliases {
			index[headerKey(alias)] = field
		}
	}
	for _, c := range customerCSVColumns {
		if c.Group != csvGroupExportOnly {
			index[headerKey(c.Name)] = c.Name
		}
	}
	return index
}()

// suggestMapping is the mapping the page starts from (design D3): previous is
// the mapping last saved on an import with this very header, nil when none;
// otherwise each column's header looked up in the alias index, a field taken
// by the first column that claims it. The mode is createOrUpdate matched by
// legalId when a legal-id column was found — whether or not a customer number
// was too, since another system's export carries that system's numbers, which
// no customer here has — else by customerNumber when a customer-number column
// was found, otherwise create — the day-one case.
func suggestMapping(header []string, previous *gen.CustomerImportMapping) gen.CustomerImportMapping {
	if previous != nil {
		return *previous
	}
	taken := map[string]bool{}
	columns := []gen.CustomerImportMappingColumn{}
	for i, h := range header {
		field, ok := aliasIndex[headerKey(h)]
		if !ok || taken[field] {
			continue
		}
		taken[field] = true
		column := int32(i)
		columns = append(columns, gen.CustomerImportMappingColumn{Field: field, Column: &column})
	}
	mapping := gen.CustomerImportMapping{Mode: gen.Create, Columns: columns}
	switch {
	case taken["legalId"]:
		matchBy := gen.LegalId
		mapping.Mode = gen.CreateOrUpdate
		mapping.MatchBy = &matchBy
	case taken["customerNumber"]:
		matchBy := gen.CustomerNumber
		mapping.Mode = gen.CreateOrUpdate
		mapping.MatchBy = &matchBy
	}
	return mapping
}
