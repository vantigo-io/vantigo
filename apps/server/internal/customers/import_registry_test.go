package customers_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the import asking Brønnøysund for what a row of a Norwegian
// business leaves out (import_registry.go): a new customer's name and the
// blanks of its postal address, the file's cells winning, an address on file
// never replaced, the registry's record stored for a row it answered, and a
// row it could not help decided as before with the reason on the legal id.
// Every registry here is registry_test.go's fake transport; no test opens a
// socket.

// kontorRegistryBody is an entity with a business address and no postal
// address, for 974760673: the postal address falls back to it.
const kontorRegistryBody = `{
	"organisasjonsnummer": "974760673",
	"navn": "KONTOR OG KAFFE AS",
	"organisasjonsform": {"kode": "AS", "beskrivelse": "Aksjeselskap"},
	"forretningsadresse": {"landkode": "NO", "postnummer": "5003", "poststed": "BERGEN", "adresse": ["Strandkaien 2", "3. etasje"], "kommune": "BERGEN"},
	"konkurs": false,
	"underAvvikling": false,
	"underTvangsavviklingEllerTvangsopplosning": false
}`

// registryOf answers each organisation number in bodies with its record and
// every other with the registry's 404.
func registryOf(bodies map[string]string) *registryTransport {
	return &registryTransport{respond: func(path string) (*http.Response, error) {
		for orgnr, body := range bodies {
			if strings.HasSuffix(path, "/"+orgnr) {
				return registryEntityResponse(http.StatusOK, body), nil
			}
		}
		return registryEntityResponse(http.StatusNotFound, ""), nil
	}}
}

// unknownToTheRegistry is a registry that knows no organisation, for the
// import tests whose rows look an organisation number up without the answer
// being what they test: their rows are decided as before, and none of them
// reaches the network.
func unknownToTheRegistry() modtest.Option {
	return modtest.WithTransport(registryStatus(http.StatusNotFound, ""))
}

// customerIDByLegalID is the one customer holding orgnr.
func customerIDByLegalID(t *testing.T, h *modtest.Harness, orgnr string) int32 {
	t.Helper()
	return modtest.One[int32](t, h, `SELECT id FROM customers.customers WHERE legal_id = $1`, orgnr)
}

// primaryPostal is the customer's one address, which must be its primary
// postal address.
func primaryPostal(t *testing.T, c *modtest.Client, id int32) addressJSON {
	t.Helper()
	var addresses addressListJSON
	getAddresses(t, c, id).JSON(&addresses)
	if len(addresses.Data) != 1 || addresses.Data[0].Type != "postal" || !addresses.Data[0].IsPrimary {
		t.Fatalf("addresses of %d = %+v, want one primary postal address", id, addresses.Data)
	}
	return addresses.Data[0]
}

func TestImportRegistry_ACreateWithTheCityAloneTakesTheRestOfTheAddress(t *testing.T) {
	t.Parallel()
	registry := registryOf(map[string]string{validOrgNumbers[0]: equinorRegistryBody})
	h := newRegistryHarness(t, registry)
	c, _ := uploadClient(t, h)

	done := importNow(t, c, h,
		csvFileOf([]string{"Navn", "Org.nr", "Poststed"}, []string{"Equinor", validOrgNumbers[0], "Stavanger"}),
		mappingOf("create", "", "name", "legalId", "postalCity"))
	if done.Created != 1 || done.Failed != 0 {
		t.Fatalf("done = %+v, errors %v", done, importResultOf(t, c, done).Errors)
	}
	id := customerIDByLegalID(t, h, validOrgNumbers[0])
	got := primaryPostal(t, c, id)
	if got.Line1 != "Postboks 8500" || deref(got.PostalCode) != "4035" || deref(got.City) != "Stavanger" || got.Country != "no" {
		t.Errorf("address = %+v, want the registry's postal address under the file's city", got)
	}
	// The file's name is the friendly name; the identity is the registry's,
	// its legal name the registered one, and its record is stored as a Brreg
	// pick's is — with nothing to report, so the customer is not "renamed".
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND name = 'Equinor' AND legal_name = 'EQUINOR ASA' AND legal_source = 'brreg'`, id); n != 1 {
		t.Errorf("the customer was not created with the file's name and the registry's legal name and source")
	}
	if n := registryRowCount(t, h, id); n != 1 {
		t.Errorf("%d registry records, want the fetched one stored", n)
	}
	if events := fetchRegistryEvents(t, c, id); len(events) != 0 {
		t.Errorf("registry events = %+v, want none at creation", events)
	}
}

func TestImportRegistry_NoNameAndNoAddressColumns_TakeTheRegistrysNameAndAddress(t *testing.T) {
	t.Parallel()
	registry := registryOf(map[string]string{validOrgNumbers[0]: equinorRegistryBody, validOrgNumbers[1]: kontorRegistryBody})
	h := newRegistryHarness(t, registry)
	c, _ := uploadClient(t, h)

	done := importNow(t, c, h,
		csvFileOf([]string{"Org.nr", "E-post"}, []string{validOrgNumbers[0], "post@equinor.no"}, []string{validOrgNumbers[1], ""}),
		mappingOf("create", "", "legalId", "email"))
	if done.Created != 2 || done.Failed != 0 {
		t.Fatalf("done = %+v, errors %v", done, importResultOf(t, c, done).Errors)
	}
	equinor := customerIDByLegalID(t, h, validOrgNumbers[0])
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND name = 'EQUINOR ASA' AND legal_name = 'EQUINOR ASA' AND legal_source = 'brreg'`, equinor); n != 1 {
		t.Errorf("the first customer did not take the registry's name as its name and legal name")
	}
	if got := primaryPostal(t, c, equinor); got.Line1 != "Postboks 8500" || deref(got.PostalCode) != "4035" || deref(got.City) != "STAVANGER" || got.Country != "no" {
		t.Errorf("first address = %+v, want the registry's postal address", got)
	}
	// No postal address in the registry: the business address, its second
	// line joined into line 2.
	kontor := customerIDByLegalID(t, h, validOrgNumbers[1])
	if got := primaryPostal(t, c, kontor); got.Line1 != "Strandkaien 2" || deref(got.Line2) != "3. etasje" || deref(got.PostalCode) != "5003" || deref(got.City) != "BERGEN" {
		t.Errorf("second address = %+v, want the registry's business address", got)
	}
}

func TestImportRegistry_TheFilesCellsWin(t *testing.T) {
	t.Parallel()
	registry := registryOf(map[string]string{validOrgNumbers[0]: equinorRegistryBody})
	h := newRegistryHarness(t, registry)
	c, _ := uploadClient(t, h)

	// The street given, the postal code and city blank: the street stays,
	// and the registry's second line does not join someone else's street.
	done := importNow(t, c, h,
		csvFileOf([]string{"Navn", "Org.nr", "Adresse", "Postnr", "Poststed"}, []string{"", validOrgNumbers[0], "Kontorveien 2", "", ""}),
		mappingOf("create", "", "name", "legalId", "postalLine1", "postalPostalCode", "postalCity"))
	if done.Created != 1 {
		t.Fatalf("done = %+v, errors %v", done, importResultOf(t, c, done).Errors)
	}
	id := customerIDByLegalID(t, h, validOrgNumbers[0])
	if got := primaryPostal(t, c, id); got.Line1 != "Kontorveien 2" || got.Line2 != nil || deref(got.PostalCode) != "4035" || deref(got.City) != "STAVANGER" {
		t.Errorf("address = %+v, want the file's street with the registry's postal code and city", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND name = 'EQUINOR ASA'`, id); n != 1 {
		t.Errorf("a blank name cell on a create did not take the registry's name")
	}

	// A complete address in the file asks nothing.
	asked := len(registry.requests())
	done = importNow(t, c, h,
		csvFileOf([]string{"Navn", "Org.nr", "Adresse", "Postnr", "Poststed", "Land"},
			[]string{"Kontor AS", validOrgNumbers[2], "Kontorveien 2", "0150", "Oslo", "NO"}),
		mappingOf("create", "", "name", "legalId", "postalLine1", "postalPostalCode", "postalCity", "postalCountry"))
	if done.Created != 1 || len(registry.requests()) != asked {
		t.Errorf("a complete row = %+v after %d more requests, want created with none", done, len(registry.requests())-asked)
	}
}

func TestImportRegistry_AnUpdateFillsAGapButNeverReplacesAnAddressOnFile(t *testing.T) {
	t.Parallel()
	registry := registryOf(map[string]string{validOrgNumbers[0]: equinorRegistryBody, validOrgNumbers[1]: kontorRegistryBody})
	h := newRegistryHarness(t, registry)
	c, _ := uploadClient(t, h)
	onFile := createCustomerWithIdentity(t, c, "On File AS", "no", validOrgNumbers[0])
	if r := postAddress(t, c, onFile.Id, map[string]any{"type": "postal", "line1": "Storgata 1", "postalCode": "0150", "city": "Oslo", "country": "NO", "isPrimary": true}); r.Status != http.StatusCreated {
		t.Fatalf("address: %d %s", r.Status, r.Body)
	}
	noAddress := createCustomerWithIdentity(t, c, "No Address AS", "no", validOrgNumbers[1])

	done := importNow(t, c, h,
		csvFileOf([]string{"Org.nr", "Poststed"}, []string{validOrgNumbers[0], "Bergen"}, []string{validOrgNumbers[1], "Fana"}),
		mappingOf("update", "legalId", "legalId", "postalCity"))
	if done.Updated != 2 || done.Failed != 0 {
		t.Fatalf("done = %+v, errors %v", done, importResultOf(t, c, done).Errors)
	}
	if got := primaryPostal(t, c, onFile.Id); got.Line1 != "Storgata 1" || deref(got.PostalCode) != "0150" || deref(got.City) != "Bergen" {
		t.Errorf("the address on file = %+v, want its street and code kept and the file's city", got)
	}
	if got := primaryPostal(t, c, noAddress.Id); got.Line1 != "Strandkaien 2" || deref(got.PostalCode) != "5003" || deref(got.City) != "Fana" {
		t.Errorf("the gap = %+v, want filled from the registry under the file's city", got)
	}
	// The name and the identity on file stand: an update never takes the
	// registry's name, and a row repeating the identity keeps its source.
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE name IN ('On File AS', 'No Address AS') AND legal_source = 'manual'`); n != 2 {
		t.Errorf("an update changed a name or an identity's source")
	}
	// The complete address on file asked nothing; only the gap was looked up.
	for _, path := range registry.requests() {
		if !strings.HasSuffix(path, "/"+validOrgNumbers[1]) {
			t.Errorf("asked %s, want only the customer with no address on file looked up", path)
		}
	}
}

func TestImportRegistry_AnUpdateToTheRegistrysIdentityStoresItsRecord(t *testing.T) {
	t.Parallel()
	registry := registryOf(map[string]string{validOrgNumbers[1]: kontorRegistryBody})
	h := newRegistryHarness(t, registry)
	c, _ := uploadClient(t, h)
	cust := createCustomer(t, c, "Kontor")

	done := importNow(t, c, h,
		csvFileOf([]string{"Kundenr", "Org.nr", "Poststed"}, []string{strconv.FormatInt(cust.CustomerNumber, 10), validOrgNumbers[1], "Bergen"}),
		mappingOf("update", "customerNumber", "customerNumber", "legalId", "postalCity"))
	if done.Updated != 1 {
		t.Fatalf("done = %+v, errors %v", done, importResultOf(t, c, done).Errors)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND name = 'Kontor' AND legal_name = 'KONTOR OG KAFFE AS' AND legal_source = 'brreg'`, cust.Id); n != 1 {
		t.Errorf("the update did not set the registry's identity")
	}
	if n := registryRowCount(t, h, cust.Id); n != 1 {
		t.Errorf("%d registry records, want the fetched one stored with the new identity", n)
	}
	if got := primaryPostal(t, c, cust.Id); got.Line1 != "Strandkaien 2" || deref(got.City) != "Bergen" {
		t.Errorf("address = %+v, want the registry's under the file's city", got)
	}
}

func TestImportRegistry_AnUpdateReplacingAnIdentityTakesTheRegistrysLegalName(t *testing.T) {
	t.Parallel()
	registry := registryOf(map[string]string{validOrgNumbers[0]: equinorRegistryBody})
	h := newRegistryHarness(t, registry)
	c, _ := uploadClient(t, h)
	// The customer holds another company's identity, legal name and all.
	cust := createCustomerWithIdentity(t, c, "GAMLE AS", "no", validOrgNumbers[6])

	done := importNow(t, c, h,
		csvFileOf([]string{"Kundenr", "Org.nr", "Poststed"}, []string{strconv.FormatInt(cust.CustomerNumber, 10), validOrgNumbers[0], "Stavanger"}),
		mappingOf("update", "customerNumber", "customerNumber", "legalId", "postalCity"))
	if done.Updated != 1 {
		t.Fatalf("done = %+v, errors %v", done, importResultOf(t, c, done).Errors)
	}
	// The old company's legal name is not carried over to the new one.
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND name = 'GAMLE AS' AND legal_id = $2 AND legal_name = 'EQUINOR ASA' AND legal_source = 'brreg'`, cust.Id, validOrgNumbers[0]); n != 1 {
		t.Errorf("the replaced identity did not take the registry's legal name and source")
	}
	if n := registryRowCount(t, h, cust.Id); n != 1 {
		t.Errorf("%d registry records, want the fetched one stored", n)
	}
	if events := fetchRegistryEvents(t, c, cust.Id); len(events) != 0 {
		t.Errorf("registry events = %+v, want no rename reported", events)
	}
	// The renamed attention item compares the record's name with the legal name.
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_registry_records r JOIN customers.customers c ON c.id = r.customer_id WHERE c.id = $1 AND r.name = c.legal_name`, cust.Id); n != 1 {
		t.Errorf("the stored record's name differs from the legal name: the customer would be listed as renamed")
	}
}

func TestImportRegistry_ARefreshOfExistingCustomersAsksNothing(t *testing.T) {
	t.Parallel()
	registry := registryOf(map[string]string{validOrgNumbers[0]: equinorRegistryBody, validOrgNumbers[1]: kontorRegistryBody})
	h := newRegistryHarness(t, registry)
	c, _ := uploadClient(t, h)
	createCustomerWithIdentity(t, c, "Equinor", "no", validOrgNumbers[0])
	createCustomerWithIdentity(t, c, "Kontor", "no", validOrgNumbers[1])

	// Create and update by organisation number, no postal columns: every row
	// updates, and an update never takes a name or adds an address.
	done := importNow(t, c, h,
		csvFileOf([]string{"Navn", "Org.nr", "E-post"},
			[]string{"Equinor", validOrgNumbers[0], "post@equinor.no"}, []string{"", validOrgNumbers[1], "post@kontor.no"}),
		mappingOf("createOrUpdate", "legalId", "", "legalId", "email"))
	if done.Updated != 2 || done.Created != 0 {
		t.Fatalf("done = %+v, errors %v", done, importResultOf(t, c, done).Errors)
	}
	if got := registry.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none for rows that update", got)
	}
}

func TestImportRegistry_AnAnswerRefusedForOneOrganisationKeepsAskingForTheRest(t *testing.T) {
	t.Parallel()
	// The first organisation's answer is about another organisation number —
	// refused, that answer alone — and the rest are still asked.
	bodies := map[string]string{validOrgNumbers[3]: kontorRegistryBody}
	for _, orgnr := range validOrgNumbers[4:9] {
		bodies[orgnr] = strings.Replace(kontorRegistryBody, validOrgNumbers[1], orgnr, 1)
	}
	registry := registryOf(bodies)
	h := newRegistryHarness(t, registry)
	c, _ := uploadClient(t, h)

	rows := [][]string{{"Org.nr"}}
	for _, orgnr := range validOrgNumbers[3:9] {
		rows = append(rows, []string{orgnr})
	}
	checked := checkNow(t, c, h, csvFileOf(rows...), mappingOf("create", "", "legalId"))
	result := importResultOf(t, c, checked)
	if checked.Created != 5 || checked.Failed != 1 {
		t.Fatalf("checked = %+v, errors %v", checked, result.Errors)
	}
	var reason bool
	for _, e := range result.Errors {
		if e.Row == 1 && e.Column == "Org.nr" && e.Message == "Brønnøysund's answer about organisation "+validOrgNumbers[3]+" could not be read; the name and postal address were not looked up" {
			reason = true
		}
	}
	if !reason {
		t.Errorf("errors = %+v, want row 1's answer named as unreadable", result.Errors)
	}
}

func TestImportRegistry_AnAnswerWithAnUnusableNameIsNotCalledUnknown(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("A", 300)
	registry := registryOf(map[string]string{validOrgNumbers[1]: strings.Replace(kontorRegistryBody, "KONTOR OG KAFFE AS", long, 1)})
	h := newRegistryHarness(t, registry)
	c, _ := uploadClient(t, h)

	checked := checkNow(t, c, h, csvFileOf([]string{"Org.nr"}, []string{validOrgNumbers[1]}), mappingOf("create", "", "legalId"))
	result := importResultOf(t, c, checked)
	if checked.Failed != 1 || len(result.Errors) == 0 {
		t.Fatalf("checked = %+v, errors %v, want the row refused for its name", checked, result.Errors)
	}
	for _, e := range result.Errors {
		if strings.Contains(e.Message, "Brønnøysund") {
			t.Errorf("error %+v blames the registry, which answered", e)
		}
	}
}

func TestImportRegistry_AnotherCountrysIdentityOnFileIsNotBlamedOnTheRegistry(t *testing.T) {
	t.Parallel()
	registry := registryOf(nil)
	h := newRegistryHarness(t, registry)
	c, _ := uploadClient(t, h)
	// A Swedish identity whose id happens to pass the Norwegian check digit;
	// the file maps the id but not the country, which stays the one on file.
	cust := createCustomerWithIdentity(t, c, "Svensk AB", "se", validOrgNumbers[5])

	checked := checkNow(t, c, h,
		csvFileOf([]string{"Kundenr", "Org.nr", "Poststed"}, []string{strconv.FormatInt(cust.CustomerNumber, 10), validOrgNumbers[5], "Göteborg"}),
		mappingOf("update", "customerNumber", "customerNumber", "legalId", "postalCity"))
	result := importResultOf(t, c, checked)
	if checked.Failed != 1 || len(result.Errors) == 0 {
		t.Fatalf("checked = %+v, errors %v, want the incomplete address refused", checked, result.Errors)
	}
	for _, e := range result.Errors {
		if strings.Contains(e.Message, "Brønnøysund") {
			t.Errorf("error %+v blames Brønnøysund for a Swedish identity", e)
		}
	}
	// The file alone reads as a Norwegian number, so it was asked about; the
	// row's identity is Swedish, so the 404 explains nothing.
	if len(registry.requests()) == 0 {
		t.Errorf("no request made; the test no longer covers the registry's silence")
	}
}

func TestImportRegistry_AnUnknownOrganisationFailsTheRowAsBefore_WithTheReason(t *testing.T) {
	t.Parallel()
	registry := registryOf(nil)
	h := newRegistryHarness(t, registry)
	c, _ := uploadClient(t, h)

	checked := checkNow(t, c, h,
		csvFileOf([]string{"Navn", "Org.nr", "Poststed"},
			[]string{"Ukjent AS", validOrgNumbers[3], "Oslo"},
			[]string{"Kjent AS", "", ""}),
		mappingOf("create", "", "name", "legalId", "postalCity"))
	result := importResultOf(t, c, checked)
	if checked.Created != 1 || checked.Failed != 1 {
		t.Fatalf("checked = %+v, errors %v", checked, result.Errors)
	}
	var reason bool
	for _, e := range result.Errors {
		if e.Row != 1 {
			t.Errorf("an error on row %d: %+v", e.Row, e)
		}
		if e.Column == "Org.nr" && e.Message == "Brønnøysund has no organisation "+validOrgNumbers[3] {
			reason = true
		}
	}
	if !reason || len(result.Errors) < 2 {
		t.Errorf("errors = %+v, want the address's own and the registry's reason on the organisation number", result.Errors)
	}
}

func TestImportRegistry_AnUnreachableRegistryFailsOnlyTheRowsThatNeededIt(t *testing.T) {
	t.Parallel()
	registry := registryStatus(http.StatusServiceUnavailable, "")
	h := newRegistryHarness(t, registry)
	c, _ := uploadClient(t, h)

	// Six rows that need the registry, with no name, and one that does not.
	header := []string{"Navn", "Org.nr", "Adresse", "Postnr", "Poststed", "Land"}
	rows := [][]string{header}
	for _, orgnr := range validOrgNumbers[3:9] {
		rows = append(rows, []string{"", orgnr, "Storgata 1", "0150", "Oslo", "NO"})
	}
	rows = append(rows, []string{"Komplett AS", validOrgNumbers[2], "Storgata 1", "0150", "Oslo", "NO"})
	checked := checkNow(t, c, h, csvFileOf(rows...),
		mappingOf("create", "", "name", "legalId", "postalLine1", "postalPostalCode", "postalCity", "postalCountry"))
	result := importResultOf(t, c, checked)
	if checked.State != "checked" || checked.Created != 1 || checked.Failed != 6 {
		t.Fatalf("checked = %+v, errors %v", checked, result.Errors)
	}
	reasons := map[int]bool{}
	for _, e := range result.Errors {
		if e.Column == "Org.nr" && e.Message == "Brønnøysund could not be reached; the name and postal address were not looked up" {
			reasons[e.Row] = true
		}
	}
	if len(reasons) != 6 {
		t.Errorf("errors = %+v, want the unreachable registry named on rows 1 to 6", result.Errors)
	}
	// The lookups in flight when the registry failed were the last asked:
	// the rest of the run's rows were not.
	asked := map[string]bool{}
	for _, path := range registry.requests() {
		asked[path[strings.LastIndex(path, "/")+1:]] = true
	}
	if len(asked) == 0 || len(asked) > 4 || asked[validOrgNumbers[2]] {
		t.Errorf("asked about %v, want at most the four in flight, never the complete row's", asked)
	}
}

func TestImportRegistry_OneRequestPerOrganisationNumber_NoneForPeopleOrOtherCountries(t *testing.T) {
	t.Parallel()
	registry := registryOf(map[string]string{validOrgNumbers[0]: equinorRegistryBody})
	h := newRegistryHarness(t, registry)
	c, _ := uploadClient(t, h)

	// The same company on three rows — a supplier's export — asks once.
	checked := checkNow(t, c, h,
		csvFileOf([]string{"Org.nr", "Målepunkt"},
			[]string{validOrgNumbers[0], "1"}, []string{validOrgNumbers[0], "2"}, []string{validOrgNumbers[0], "3"}),
		mappingOf("create", "", "legalId"))
	if checked.Created != 1 || checked.Skipped != 2 {
		t.Fatalf("checked = %+v, errors %v", checked, importResultOf(t, c, checked).Errors)
	}
	if got := registry.requests(); len(got) != 1 {
		t.Errorf("requests = %v, want one for the three rows", got)
	}

	// A person's row and a Swedish company's are not looked up, and fail
	// as they did.
	asked := len(registry.requests())
	checked = checkNow(t, c, h,
		csvFileOf([]string{"Navn", "Type", "Land", "Org.nr", "Poststed"},
			[]string{"Kari Nordmann", "privat", "", validOrgNumbers[0], "Oslo"},
			[]string{"Svensk AB", "", "se", "5560360793", "Göteborg"}),
		mappingOf("create", "", "name", "type", "legalCountry", "legalId", "postalCity"))
	result := importResultOf(t, c, checked)
	if checked.Failed != 2 || len(registry.requests()) != asked {
		t.Errorf("checked = %+v after %d more requests, want both refused with none", checked, len(registry.requests())-asked)
	}
	for _, e := range result.Errors {
		if strings.Contains(e.Message, "Brønnøysund") {
			t.Errorf("row %d names the registry: %+v", e.Row, e)
		}
	}
}
