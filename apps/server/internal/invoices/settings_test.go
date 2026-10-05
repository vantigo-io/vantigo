package invoices_test

import (
	"net/http"
	"os"
	"regexp"
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
	NextNumber              *int64   `json:"nextNumber"`
	PeppolID                *string  `json:"peppolId"`
	KidLength               *int32   `json:"kidLength"`
	KidAlgorithm            *string  `json:"kidAlgorithm"`
	MissingSellerFields     []string `json:"missingSellerFields"`
	Warnings                []string `json:"warnings"`
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
	SourceKind        *string             `json:"sourceKind"`
	SourceID          *int64              `json:"sourceId"`
	AllowedIssueDates []string            `json:"allowedIssueDates"`
	PeppolRegistered  *bool               `json:"peppolRegistered"`
	PeppolCanReceive  *bool               `json:"peppolCanReceive"`
	Rules             []struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	} `json:"rules"`
}

func problemOf(t *testing.T, res interface{ JSON(any) }) problemJSON {
	t.Helper()
	var p problemJSON
	res.JSON(&p)
	return p
}

// completeSeller is a seller body that passes every rule and is complete: a
// VAT-registered AS in Oslo with Brønnøysundregistrene's own organisation
// number and DNB's sample account. It names no Peppol id, which defaults to
// 0192:974760673, and no KID agreement — the three required-nullable fields
// are sent, as null.
func completeSeller(revision int32) map[string]any {
	return map[string]any{
		"legalName": "Kraft-Verket AS", "organisationNumber": "974 760 673",
		"vatRegistered": true, "inForetaksregisteret": true,
		"addressLine1": "Storgata 1", "addressLine2": "", "postalCode": "0155", "city": "Oslo", "country": "no",
		"bankAccount": "8601.11.17947", "iban": "NO93 8601 1117 947", "bic": "dnbanokkxxx",
		"email": "faktura@kraft-verket.no", "defaultPaymentTermsDays": 14, "defaultCurrency": "NOK",
		"footerText": "Takk for handelen.", "seriesStart": 1,
		"peppolId": nil, "kidLength": nil, "kidAlgorithm": nil, "revision": revision,
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
		{"seriesStart", int64(1) << 53},
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

// The settings answer the number the next issue takes (reading 19): the
// series start while nothing is issued — the request's own on a PUT — and the
// counter's next from the first issue on, on GET and PUT alike.
func TestSettings_TheNextNumber(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	next := func(what string, s settingsJSON, want int64) {
		t.Helper()
		if s.NextNumber == nil || *s.NextNumber != want {
			t.Errorf("%s: nextNumber = %v, want %d", what, s.NextNumber, want)
		}
	}
	get := func() settingsJSON {
		t.Helper()
		var read settingsJSON
		h.SignIn(t, "invoices:access").Do(http.MethodGet, settingsPath, nil).JSON(&read)
		return read
	}
	next("a fresh installation's GET", get(), get().SeriesStart)
	body := completeSeller(1)
	body["seriesStart"] = 1000
	next("the PUT that moves the start", saveSeller(t, h, body), 1000)
	next("the GET after it", get(), 1000)

	issuedAcme(t, h)
	next("the GET after the first issue", get(), 1001)
	body = completeSeller(2)
	body["seriesStart"] = 1000
	next("a PUT after the first issue", saveSeller(t, h, body), 1001)
}

// The seller's Peppol id (EHF and KID design D2): null or empty defaults it
// to 0192 and the organisation number, and to nothing without one; any other
// is a four-digit scheme, a colon and an identifier, kept as given, and a
// 0192 id must be the seller's own organisation number.
func TestSettings_PeppolIdDefaultsAndValidates(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager := h.SignIn(t, "invoices:access", "invoices:manage")

	saved := saveSeller(t, h, completeSeller(1))
	if saved.PeppolID == nil || *saved.PeppolID != "0192:974760673" {
		t.Fatalf("peppolId = %v, want the default 0192:974760673", saved.PeppolID)
	}
	body := completeSeller(saved.Revision)
	body["peppolId"] = ""
	body["organisationNumber"] = ""
	if saved = saveSeller(t, h, body); saved.PeppolID != nil {
		t.Errorf("an empty id without an organisation number = %q, want null", *saved.PeppolID)
	}
	for given, want := range map[string]string{" 0192:974760673 ": "0192:974760673", "9908:974760673": "9908:974760673", "0088:7080000000003": "0088:7080000000003"} {
		body = completeSeller(saved.Revision)
		body["peppolId"] = given
		if saved = saveSeller(t, h, body); saved.PeppolID == nil || *saved.PeppolID != want {
			t.Errorf("peppolId %q saved as %v, want %q", given, saved.PeppolID, want)
		}
	}
	for _, bad := range []any{"974760673", "192:974760673", "0192:", "0192:923609016", "0192:97476067 3", 42} {
		body = completeSeller(saved.Revision)
		body["peppolId"] = bad
		res := manager.Do(http.MethodPut, settingsPath, body)
		if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["peppolId"]) == 0 {
			t.Errorf("peppolId %v = %d %s, want 400 on peppolId", bad, res.Status, res.Body)
		}
	}
	// Without an organisation number a 0192 id is nobody's own.
	body = completeSeller(saved.Revision)
	body["organisationNumber"], body["peppolId"] = "", "0192:974760673"
	if res := manager.Do(http.MethodPut, settingsPath, body); res.Status != http.StatusBadRequest {
		t.Errorf("a 0192 id without an organisation number = %d, want 400", res.Status)
	}
}

// The KID agreement (D3): a pair of 4-25 and mod10 or mod11, or nothing; the
// next number must fit in the length less one — judged against the
// request's own seriesStart before the first issue and against the counter
// from then on — and fewer than two digits to spare is the
// kid_headroom_low warning, on the PUT and on GET. Clearing is allowed.
func TestSettings_TheKidAgreement(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager := h.SignIn(t, "invoices:access", "invoices:manage")
	revision := int32(1)
	kid := func(length, algorithm, seriesStart any) map[string]any {
		body := completeSeller(revision)
		body["kidLength"], body["kidAlgorithm"], body["seriesStart"] = length, algorithm, seriesStart
		return body
	}
	refused := func(body map[string]any, field string) {
		t.Helper()
		res := manager.Do(http.MethodPut, settingsPath, body)
		if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors[field]) == 0 {
			t.Errorf("kidLength %v kidAlgorithm %v seriesStart %v = %d %s, want 400 on %s",
				body["kidLength"], body["kidAlgorithm"], body["seriesStart"], res.Status, res.Body, field)
		}
	}
	refused(kid(3, "mod10", 1), "kidLength")
	refused(kid(26, "mod10", 1), "kidLength")
	refused(kid("ten", "mod10", 1), "kidLength")
	refused(kid(10, "mod12", 1), "kidAlgorithm")
	refused(kid(10, nil, 1), "kidAlgorithm")
	refused(kid(nil, "mod11", 1), "kidLength")
	// The request's own series start: 1000 needs four digits, a length of 4
	// leaves three.
	refused(kid(4, "mod10", 1000), "kidLength")

	saved := saveSeller(t, h, kid(4, "mod10", 1))
	if saved.KidLength == nil || *saved.KidLength != 4 || saved.KidAlgorithm == nil || *saved.KidAlgorithm != "mod10" || len(saved.Warnings) != 0 {
		t.Fatalf("saved = length %v algorithm %v warnings %v, want 4, mod10 and no warning", saved.KidLength, saved.KidAlgorithm, saved.Warnings)
	}
	revision = saved.Revision
	saved = saveSeller(t, h, kid(4, "mod11", 100))
	if !slices.Equal(saved.Warnings, []string{"kid_headroom_low"}) {
		t.Errorf("100 under a length of 4: warnings %v, want kid_headroom_low", saved.Warnings)
	}
	var read settingsJSON
	h.SignIn(t, "invoices:access").Do(http.MethodGet, settingsPath, nil).JSON(&read)
	if !slices.Equal(read.Warnings, []string{"kid_headroom_low"}) || read.KidAlgorithm == nil || *read.KidAlgorithm != "mod11" {
		t.Errorf("GET = algorithm %v warnings %v, want mod11 and kid_headroom_low", read.KidAlgorithm, read.Warnings)
	}

	// From the first issue on, the counter's next number is judged.
	revision = saved.Revision
	h.Exec(t, `INSERT INTO invoices.counters (counter_name, next_value) VALUES ('documents', 100000)`)
	refused(kid(6, "mod10", 100), "kidLength")
	saved = saveSeller(t, h, kid(7, "mod10", 100))
	if !slices.Equal(saved.Warnings, []string{"kid_headroom_low"}) {
		t.Errorf("100000 under a length of 7: warnings %v, want kid_headroom_low", saved.Warnings)
	}
	revision = saved.Revision
	if saved = saveSeller(t, h, kid(9, "mod10", 100)); len(saved.Warnings) != 0 {
		t.Errorf("100000 under a length of 9: warnings %v, want none", saved.Warnings)
	}

	// Clearing the pair is allowed.
	revision = saved.Revision
	if saved = saveSeller(t, h, kid(nil, nil, 100)); saved.KidLength != nil || saved.KidAlgorithm != nil || len(saved.Warnings) != 0 {
		t.Errorf("cleared = length %v algorithm %v warnings %v, want nulls and no warning", saved.KidLength, saved.KidAlgorithm, saved.Warnings)
	}
}

// backfillStatement is 00036's backfill of the seller's Peppol id, read from
// the migration itself.
var backfillStatement = regexp.MustCompile(`(?s)UPDATE invoices\.settings SET peppol_id = .*?;`)

// An installation that had an organisation number before 00036 has its
// Peppol id without re-saving (D2): the migration's own backfill, run over
// a settings row as it stood, answers through GET /settings, and a save of
// what was read keeps it.
func TestSettings_PeppolIdIsBackfilled(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	migration, err := os.ReadFile("../db/migrations/00036_invoices_ehf_kid.sql")
	if err != nil {
		t.Fatalf("read 00036: %v", err)
	}
	backfill := backfillStatement.Find(migration)
	if backfill == nil {
		t.Fatal("00036 has no backfill of the seller's Peppol id")
	}
	h.Exec(t, `UPDATE invoices.settings SET organisation_number = '974760673', peppol_id = NULL`)
	h.Exec(t, string(backfill))

	var read settingsJSON
	h.SignIn(t, "invoices:access").Do(http.MethodGet, settingsPath, nil).JSON(&read)
	if read.PeppolID == nil || *read.PeppolID != "0192:974760673" {
		t.Fatalf("GET after the backfill: peppolId %v, want 0192:974760673", read.PeppolID)
	}
	body := completeSeller(read.Revision)
	body["peppolId"] = *read.PeppolID
	if saved := saveSeller(t, h, body); saved.PeppolID == nil || *saved.PeppolID != "0192:974760673" {
		t.Errorf("a save of what was read: peppolId %v, want it kept", saved.PeppolID)
	}
}

// peppolId, kidLength and kidAlgorithm are required and nullable (reading
// 16): a body without one of them is a 400 on it and changes nothing — a
// client that predates them cannot clear the KID agreement by leaving them
// out — and null is a value: it clears.
func TestSettings_TheThreeFieldsAreRequiredNullable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager := h.SignIn(t, "invoices:access", "invoices:manage")
	body := completeSeller(1)
	body["kidLength"], body["kidAlgorithm"] = 10, "mod10"
	saved := saveSeller(t, h, body)

	for _, field := range []string{"peppolId", "kidLength", "kidAlgorithm"} {
		body := completeSeller(saved.Revision)
		body["kidLength"], body["kidAlgorithm"] = 10, "mod10"
		delete(body, field)
		res := manager.Do(http.MethodPut, settingsPath, body)
		if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors[field]) == 0 {
			t.Errorf("a body without %s = %d %s, want 400 on %s", field, res.Status, res.Body, field)
		}
	}
	var read settingsJSON
	h.SignIn(t, "invoices:access").Do(http.MethodGet, settingsPath, nil).JSON(&read)
	if read.Revision != saved.Revision || read.KidLength == nil || *read.KidLength != 10 {
		t.Errorf("after the refusals: revision %d length %v, want %d and 10", read.Revision, read.KidLength, saved.Revision)
	}
	if cleared := saveSeller(t, h, completeSeller(saved.Revision)); cleared.KidLength != nil || cleared.KidAlgorithm != nil {
		t.Errorf("null = length %v algorithm %v, want the agreement cleared", cleared.KidLength, cleared.KidAlgorithm)
	}
}
