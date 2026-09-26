package invoices_test

import (
	"net/http"
	"slices"
	"testing"
)

const settingsPath = "/api/v1/invoices/settings"

// settingsJSON is the settings as a client reads them.
type settingsJSON struct {
	LegalName               string   `json:"legalName"`
	OrganisationNumber      string   `json:"organisationNumber"`
	VatRegistered           bool     `json:"vatRegistered"`
	InForetaksregisteret    bool     `json:"inForetaksregisteret"`
	AddressLine1            string   `json:"addressLine1"`
	AddressLine2            string   `json:"addressLine2"`
	PostalCode              string   `json:"postalCode"`
	City                    string   `json:"city"`
	Country                 string   `json:"country"`
	BankAccount             string   `json:"bankAccount"`
	Iban                    string   `json:"iban"`
	Bic                     string   `json:"bic"`
	Email                   string   `json:"email"`
	DefaultPaymentTermsDays int32    `json:"defaultPaymentTermsDays"`
	DefaultCurrency         string   `json:"defaultCurrency"`
	FooterText              string   `json:"footerText"`
	SeriesStart             int64    `json:"seriesStart"`
	SeriesLocked            bool     `json:"seriesLocked"`
	MissingSellerFields     []string `json:"missingSellerFields"`
	Revision                int32    `json:"revision"`
}

// problemJSON is a refusal as a client reads it: the conflict's code, the
// validation's field errors.
type problemJSON struct {
	Title             string              `json:"title"`
	Detail            string              `json:"detail"`
	Code              string              `json:"code"`
	Errors            map[string][]string `json:"errors"`
	MergedInto        *int32              `json:"mergedInto"`
	LinePosition      *int32              `json:"linePosition"`
	AllowedIssueDates []string            `json:"allowedIssueDates"`
}

func problemOf(t *testing.T, res interface{ JSON(any) }) problemJSON {
	t.Helper()
	var p problemJSON
	res.JSON(&p)
	return p
}

// completeSeller is a seller body that passes every rule and is complete: a
// VAT-registered AS in Oslo with Brønnøysundregistrene's own organisation
// number and DNB's sample account.
func completeSeller(revision int32) map[string]any {
	return map[string]any{
		"legalName": "Kraft-Verket AS", "organisationNumber": "974 760 673",
		"vatRegistered": true, "inForetaksregisteret": true,
		"addressLine1": "Storgata 1", "addressLine2": "", "postalCode": "0155", "city": "Oslo", "country": "no",
		"bankAccount": "8601.11.17947", "iban": "NO93 8601 1117 947", "bic": "dnbanokkxxx",
		"email": "faktura@kraft-verket.no", "defaultPaymentTermsDays": 14, "defaultCurrency": "NOK",
		"footerText": "Takk for handelen.", "seriesStart": 1, "revision": revision,
	}
}

// saveSeller replaces the settings with body as an invoices:manage holder.
func saveSeller(t *testing.T, h *harness, body map[string]any) settingsJSON {
	t.Helper()
	res := h.SignIn(t, "invoices:access", "invoices:manage").Do(http.MethodPut, settingsPath, body)
	if res.Status != http.StatusOK {
		t.Fatalf("PUT /settings = %d %s, want 200", res.Status, res.Body)
	}
	var s settingsJSON
	res.JSON(&s)
	return s
}

// A complete seller is stored normalised — the separators dropped, the codes
// upper-cased — and from then on meta calls the seller complete.
func TestSettings_AReplaceStoresTheSellerNormalised(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	saved := saveSeller(t, h, completeSeller(1))

	if saved.OrganisationNumber != "974760673" || saved.BankAccount != "86011117947" ||
		saved.Iban != "NO9386011117947" || saved.Bic != "DNBANOKKXXX" || saved.Country != "NO" {
		t.Errorf("saved = %+v, want the numbers without separators and the codes upper-cased", saved)
	}
	if saved.Revision != 2 || saved.SeriesLocked || len(saved.MissingSellerFields) != 0 {
		t.Errorf("revision %d, locked %v, missing %v; want 2, false, none", saved.Revision, saved.SeriesLocked, saved.MissingSellerFields)
	}
	if meta := getMeta(t, h, "invoices:access"); !meta.SellerComplete {
		t.Errorf("meta after a complete seller = incomplete, missing %v", meta.MissingSellerFields)
	}
	var read settingsJSON
	h.SignIn(t, "invoices:access").Do(http.MethodGet, settingsPath, nil).JSON(&read)
	if read.LegalName != "Kraft-Verket AS" || read.FooterText != "Takk for handelen." {
		t.Errorf("GET /settings = %+v, want what was saved", read)
	}
}

// Each field's rule is a 400 on that field (D2).
func TestSettings_EveryRuleIsA400OnItsField(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager := h.SignIn(t, "invoices:access", "invoices:manage")

	for _, c := range []struct {
		field string
		value any
	}{
		{"organisationNumber", "974760674"},
		{"organisationNumber", "12345"},
		{"bankAccount", "86011117948"},
		{"iban", "NO9386011117948"},
		{"bic", "DNB"},
		{"email", "not an address"},
		{"country", "Norway"},
		{"country", "ZZ"},
		{"defaultPaymentTermsDays", 366},
		{"defaultPaymentTermsDays", -1},
		{"defaultCurrency", "EUR"},
		{"seriesStart", 0},
		{"legalName", string(make([]byte, 201))},
		{"footerText", string(make([]byte, 501))},
	} {
		body := completeSeller(1)
		body[c.field] = c.value
		res := manager.Do(http.MethodPut, settingsPath, body)
		if res.Status != http.StatusBadRequest {
			t.Errorf("%s = %v: %d %s, want 400", c.field, c.value, res.Status, res.Body)
			continue
		}
		if p := problemOf(t, res); len(p.Errors[c.field]) == 0 {
			t.Errorf("%s = %v: errors %v, want one on %s", c.field, c.value, p.Errors, c.field)
		}
	}
	body := completeSeller(1)
	body["defaultCurrency"] = "EUR"
	if p := problemOf(t, manager.Do(http.MethodPut, settingsPath, body)); !slices.Equal(p.Errors["defaultCurrency"], []string{"Only NOK in this phase"}) {
		t.Errorf("EUR: %v, want exactly \"Only NOK in this phase\"", p.Errors)
	}
}

// Meta names what the seller still lacks, field by field.
func TestSettings_MetaNamesTheMissingFields(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	body := completeSeller(1)
	body["organisationNumber"] = ""
	body["city"] = ""
	saveSeller(t, h, body)

	if meta := getMeta(t, h, "invoices:access"); meta.SellerComplete || !slices.Equal(meta.MissingSellerFields, []string{"organisationNumber", "city"}) {
		t.Errorf("meta = complete %v missing %v, want incomplete missing organisationNumber and city", meta.SellerComplete, meta.MissingSellerFields)
	}
}

// A stale revision is a 409 naming both, with no code; a replace needs
// invoices:manage.
func TestSettings_TheRevisionAndThePermission(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	saveSeller(t, h, completeSeller(1))

	res := h.SignIn(t, "invoices:access", "invoices:manage").Do(http.MethodPut, settingsPath, completeSeller(1))
	if res.Status != http.StatusConflict {
		t.Fatalf("a stale revision = %d, want 409", res.Status)
	}
	if p := problemOf(t, res); p.Code != "" || p.Detail != "The Invoice settings has revision 2; the supplied revision was 1." {
		t.Errorf("stale revision = %+v, want no code and both revisions named", p)
	}
	if res := h.SignIn(t, "invoices:access").Do(http.MethodPut, settingsPath, completeSeller(2)); res.Status != http.StatusForbidden {
		t.Errorf("PUT without invoices:manage = %d, want 403", res.Status)
	}
}

// The series start is the settings' own until something is issued; from the
// counter row on it is refused with series_locked, and every other field stays
// editable (D2).
func TestSettings_TheSeriesStartLocksAtTheFirstIssue(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	body := completeSeller(1)
	body["seriesStart"] = 1000
	if saved := saveSeller(t, h, body); saved.SeriesStart != 1000 {
		t.Fatalf("series start = %d, want 1000 while nothing is issued", saved.SeriesStart)
	}

	h.Exec(t, `INSERT INTO invoices.counters (counter_name, next_value) VALUES ('documents', 1001)`)

	body = completeSeller(2)
	body["seriesStart"] = 5000
	res := h.SignIn(t, "invoices:access", "invoices:manage").Do(http.MethodPut, settingsPath, body)
	if res.Status != http.StatusConflict || problemOf(t, res).Code != "series_locked" {
		t.Fatalf("a changed start after the first issue = %d %s, want 409 series_locked", res.Status, res.Body)
	}
	body["seriesStart"] = 1000
	body["legalName"] = "Kraft-Verket Norge AS"
	if saved := saveSeller(t, h, body); !saved.SeriesLocked || saved.LegalName != "Kraft-Verket Norge AS" {
		t.Errorf("saved = %+v, want the name changed and the series locked", saved)
	}
}
