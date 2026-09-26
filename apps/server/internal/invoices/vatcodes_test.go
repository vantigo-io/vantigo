package invoices_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

const vatCodesPath = "/api/v1/invoices/vat-codes"

// The seeded codes' fixed ids (00034).
const (
	vat25        = 1
	vat15        = 2
	vatZero      = 5
	vatReverse   = 6
	vatExport    = 7
	vatExempt    = 8
	vatOutside   = 9
	vatUnknownID = 9999
)

type vatRateJSON struct {
	ID          int32   `json:"id"`
	RatePercent float64 `json:"ratePercent"`
	ValidFrom   string  `json:"validFrom"`
	ValidTo     *string `json:"validTo"`
}

type vatCodeJSON struct {
	ID              int32         `json:"id"`
	Code            string        `json:"code"`
	Name            string        `json:"name"`
	SafTCode        string        `json:"safTCode"`
	EhfCategory     string        `json:"ehfCategory"`
	ExemptionReason *string       `json:"exemptionReason"`
	Active          bool          `json:"active"`
	InUse           bool          `json:"inUse"`
	Revision        int32         `json:"revision"`
	Rates           []vatRateJSON `json:"rates"`
}

func manager(t *testing.T, h *harness) *modtest.Client {
	t.Helper()
	return h.SignIn(t, "invoices:access", "invoices:manage")
}

func listVatCodes(t *testing.T, h *harness) map[int32]vatCodeJSON {
	t.Helper()
	res := h.SignIn(t, "invoices:access").Do(http.MethodGet, vatCodesPath, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /vat-codes = %d %s", res.Status, res.Body)
	}
	var codes []vatCodeJSON
	res.JSON(&codes)
	out := map[int32]vatCodeJSON{}
	for _, c := range codes {
		out[c.ID] = c
	}
	return out
}

// periods renders a code's periods as from..to, for one comparison.
func periods(c vatCodeJSON) string {
	out := ""
	for _, r := range c.Rates {
		to := "open"
		if r.ValidTo != nil {
			to = *r.ValidTo
		}
		out += fmt.Sprintf("[%v %s..%s]", r.RatePercent, r.ValidFrom, to)
	}
	return out
}

// plantIssuedDocument writes an issued document directly, bypassing the
// handlers — what a date rule or a gap check needs to see — and returns its id.
func plantIssuedDocument(t *testing.T, h *harness, number int64, issueDate string) int64 {
	t.Helper()
	return modtest.One[int64](t, h.Harness, `
		INSERT INTO invoices.invoices (kind, status, number, customer_id, issue_date, issued_at, created_by_user_id, created_at, updated_at)
		VALUES ('invoice', 'issued', $1, 1, $2::date, now(), $3, now(), now())
		RETURNING id`, number, issueDate, uuid.New())
}

// plantDraftLine writes a draft with one line on vatCodeID directly, which is
// all "in use" needs.
func plantDraftLine(t *testing.T, h *harness, vatCodeID int32) {
	t.Helper()
	id := modtest.One[int64](t, h.Harness, `
		INSERT INTO invoices.invoices (kind, customer_id, created_by_user_id, created_at, updated_at)
		VALUES ('invoice', 1, $1, now(), now()) RETURNING id`, uuid.New())
	h.Exec(t, `
		INSERT INTO invoices.lines (invoice_id, position, description, quantity, unit_price, vat_code_id, line_gross, line_allowance, line_net)
		VALUES ($1, 1, 'Konsulenttime', 1, 100, $2, 100, 0, 100)`, id, vatCodeID)
}

// The seed is on GET /vat-codes as it is on meta, each with its one open
// 2026 period, none in use.
func TestVatCodes_TheSeed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	codes := listVatCodes(t, h)
	if len(codes) != 9 {
		t.Fatalf("codes = %d, want the nine seeded", len(codes))
	}
	if c := codes[vatExempt]; c.Code != "6" || c.EhfCategory != "E" || periods(c) != "[0 2026-01-01..open]" || c.InUse {
		t.Errorf("code 6 = %+v, want E at 0 from 2026-01-01, not in use", c)
	}
	if c := codes[vatOutside]; c.Code != "7" || c.EhfCategory != "O" || c.ExemptionReason == nil {
		t.Errorf("code 7 = %+v, want O with its reason", c)
	}
	if c := codes[vat25]; periods(c) != "[25 2026-01-01..open]" || c.ExemptionReason != nil {
		t.Errorf("code 3 = %+v, want 25 %% open-ended and no reason", c)
	}
}

// A code is created with its first, open period; the label is unique ignoring
// case; a reason is required unless S; an S rate is above 0, every other 0.
func TestVatCodes_CreateAndItsRules(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	m := manager(t, h)

	res := m.Do(http.MethodPost, vatCodesPath, map[string]any{
		"code": "3H", "name": "Høy sats", "safTCode": "3", "ehfCategory": "S", "ratePercent": 25, "validFrom": "2026-01-01",
	})
	if res.Status != http.StatusCreated {
		t.Fatalf("POST /vat-codes = %d %s, want 201", res.Status, res.Body)
	}
	var created vatCodeJSON
	res.JSON(&created)
	if created.Code != "3H" || !created.Active || periods(created) != "[25 2026-01-01..open]" {
		t.Errorf("created = %+v", created)
	}

	for _, c := range []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"the same label in another case", map[string]any{"code": "3h", "name": "X", "safTCode": "3", "ehfCategory": "S", "ratePercent": 25, "validFrom": "2026-01-01"}, "code"},
		{"a Z code without a reason", map[string]any{"code": "Z1", "name": "X", "safTCode": "5", "ehfCategory": "Z", "ratePercent": 0, "validFrom": "2026-01-01"}, "exemptionReason"},
		{"an S code at 0", map[string]any{"code": "S0", "name": "X", "safTCode": "3", "ehfCategory": "S", "ratePercent": 0, "validFrom": "2026-01-01"}, "ratePercent"},
		{"an S code over 100", map[string]any{"code": "S9", "name": "X", "safTCode": "3", "ehfCategory": "S", "ratePercent": 100.5, "validFrom": "2026-01-01"}, "ratePercent"},
		{"a Z code with a rate", map[string]any{"code": "Z2", "name": "X", "safTCode": "5", "ehfCategory": "Z", "exemptionReason": "Fritatt", "ratePercent": 5, "validFrom": "2026-01-01"}, "ratePercent"},
		{"three decimals", map[string]any{"code": "S3", "name": "X", "safTCode": "3", "ehfCategory": "S", "ratePercent": 11.111, "validFrom": "2026-01-01"}, "ratePercent"},
		{"an unknown category", map[string]any{"code": "Q", "name": "X", "safTCode": "3", "ehfCategory": "Q", "ratePercent": 0, "validFrom": "2026-01-01"}, "ehfCategory"},
		{"a code too long", map[string]any{"code": "ABCDEFGHIJK", "name": "X", "safTCode": "3", "ehfCategory": "S", "ratePercent": 25, "validFrom": "2026-01-01"}, "code"},
	} {
		res := m.Do(http.MethodPost, vatCodesPath, c.body)
		if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors[c.field]) == 0 {
			t.Errorf("%s = %d %s, want 400 on %s", c.name, res.Status, res.Body, c.field)
		}
	}
	if res := h.SignIn(t, "invoices:access").Do(http.MethodPost, vatCodesPath, map[string]any{
		"code": "X", "name": "X", "safTCode": "3", "ehfCategory": "S", "ratePercent": 25, "validFrom": "2026-01-01",
	}); res.Status != http.StatusForbidden {
		t.Errorf("POST without invoices:manage = %d, want 403", res.Status)
	}
}

// A code in use keeps its category and SAF-T code; its label, name and reason
// stay editable; a deactivated code leaves what meta offers (D3).
func TestVatCodes_TheInUseRuleAndDeactivation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	m := manager(t, h)
	update := func(id int32, body map[string]any) (int, problemJSON, vatCodeJSON) {
		res := m.Do(http.MethodPut, fmt.Sprintf("%s/%d", vatCodesPath, id), body)
		var code vatCodeJSON
		var p problemJSON
		if res.Status == http.StatusOK {
			res.JSON(&code)
		} else if len(res.Body) > 0 {
			res.JSON(&p)
		}
		return res.Status, p, code
	}

	// Not in use: the category may change, when the rates fit it.
	if status, _, code := update(vatZero, map[string]any{"code": "5", "name": "Fritatt", "safTCode": "52", "ehfCategory": "G", "exemptionReason": "Utførsel", "active": true, "revision": 1}); status != http.StatusOK || code.EhfCategory != "G" || code.Revision != 2 {
		t.Fatalf("an unused code's category change = %d %+v, want 200", status, code)
	}
	if status, p, _ := update(vatZero, map[string]any{"code": "5", "name": "Fritatt", "safTCode": "52", "ehfCategory": "S", "active": true, "revision": 2}); status != http.StatusBadRequest || len(p.Errors["ehfCategory"]) == 0 {
		t.Errorf("S over a 0 %% period = %d %+v, want 400 on ehfCategory", status, p)
	}

	plantDraftLine(t, h, vat25)
	if status, p, _ := update(vat25, map[string]any{"code": "3", "name": "Utgående mva 25 %", "safTCode": "31", "ehfCategory": "S", "active": true, "revision": 1}); status != http.StatusConflict || p.Code != "vat_code_in_use" {
		t.Errorf("an in-use code's SAF-T change = %d %+v, want 409 vat_code_in_use", status, p)
	}
	status, _, code := update(vat25, map[string]any{"code": "3A", "name": "Høy sats", "safTCode": "3", "ehfCategory": "S", "active": false, "revision": 1})
	if status != http.StatusOK || code.Code != "3A" || code.Active || !code.InUse {
		t.Fatalf("renaming and deactivating an in-use code = %d %+v, want 200", status, code)
	}
	for _, c := range getMeta(t, h, "invoices:access").VatCodes {
		if c.ID == vat25 {
			t.Error("meta still offers a deactivated code")
		}
	}
	if status, p, _ := update(vat25, map[string]any{"code": "3A", "name": "Høy sats", "safTCode": "3", "ehfCategory": "S", "active": true, "revision": 1}); status != http.StatusConflict || p.Code != "" {
		t.Errorf("a stale revision = %d %+v, want 409 without a code", status, p)
	}
	if status, _, _ := update(vatUnknownID, map[string]any{"code": "Z", "name": "Z", "safTCode": "3", "ehfCategory": "S", "active": true, "revision": 1}); status != http.StatusNotFound {
		t.Errorf("an unknown code = %d, want 404", status)
	}
}

// The rate-change rule (D3): a new period closes the old one the day before;
// it must start after the open one and after the latest issue date; the latest
// future period can be removed and the previous reopened, but not an earlier
// one, not the only one, and not once a document is dated in it.
func TestVatCodes_TheRateChangeRule(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	m := manager(t, h)
	ratesPath := fmt.Sprintf("%s/%d/rates", vatCodesPath, vat25)

	res := m.Do(http.MethodPost, ratesPath, map[string]any{"ratePercent": 26, "validFrom": "2027-01-01"})
	if res.Status != http.StatusCreated {
		t.Fatalf("POST rates = %d %s, want 201", res.Status, res.Body)
	}
	var changed vatCodeJSON
	res.JSON(&changed)
	if got := periods(changed); got != "[25 2026-01-01..2026-12-31][26 2027-01-01..open]" {
		t.Errorf("periods = %s, want the old closed the day before the new", got)
	}

	for _, c := range []struct {
		name, validFrom string
	}{{"on the open period's start", "2027-01-01"}, {"before it", "2026-06-01"}} {
		res := m.Do(http.MethodPost, ratesPath, map[string]any{"ratePercent": 27, "validFrom": c.validFrom})
		if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["validFrom"]) == 0 {
			t.Errorf("a new period %s = %d %s, want 400 on validFrom", c.name, res.Status, res.Body)
		}
	}
	if res := m.Do(http.MethodPost, fmt.Sprintf("%s/%d/rates", vatCodesPath, vatZero), map[string]any{"ratePercent": 5, "validFrom": "2027-01-01"}); res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["ratePercent"]) == 0 {
		t.Errorf("a rate on a Z code = %d %s, want 400 on ratePercent", res.Status, res.Body)
	}

	// Removing: never an earlier period, never the only one.
	first, latest := changed.Rates[0].ID, changed.Rates[1].ID
	if res := m.Do(http.MethodDelete, fmt.Sprintf("%s/%d", ratesPath, first), nil); res.Status != http.StatusConflict || problemOf(t, res).Code != "rate_period_not_latest" {
		t.Errorf("removing the earlier period = %d %s, want 409 rate_period_not_latest", res.Status, res.Body)
	}
	only := listVatCodes(t, h)[vat15].Rates[0].ID
	if res := m.Do(http.MethodDelete, fmt.Sprintf("%s/%d/rates/%d", vatCodesPath, vat15, only), nil); res.Status != http.StatusConflict || problemOf(t, res).Code != "rate_period_last" {
		t.Errorf("removing the only period = %d %s, want 409 rate_period_last", res.Status, res.Body)
	}
	if res := m.Do(http.MethodDelete, fmt.Sprintf("%s/%d", ratesPath, 424242), nil); res.Status != http.StatusNotFound {
		t.Errorf("removing a period the code does not have = %d, want 404", res.Status)
	}

	// A document dated in the new period keeps it.
	plantIssuedDocument(t, h, 1, "2027-01-05")
	if res := m.Do(http.MethodDelete, fmt.Sprintf("%s/%d", ratesPath, latest), nil); res.Status != http.StatusConflict || problemOf(t, res).Code != "rate_period_in_use" {
		t.Errorf("removing a period a document is dated in = %d %s, want 409 rate_period_in_use", res.Status, res.Body)
	}
	// And no change may start on or before the latest issue date.
	for _, day := range []string{"2027-01-05", "2027-01-02"} {
		if res := m.Do(http.MethodPost, ratesPath, map[string]any{"ratePercent": 27, "validFrom": day}); res.Status != http.StatusConflict || problemOf(t, res).Code != "rate_change_in_past" {
			t.Errorf("a change from %s = %d %s, want 409 rate_change_in_past", day, res.Status, res.Body)
		}
	}
	if res := m.Do(http.MethodPost, ratesPath, map[string]any{"ratePercent": 27, "validFrom": "2027-01-06"}); res.Status != http.StatusCreated {
		t.Errorf("a change from the day after = %d %s, want 201", res.Status, res.Body)
	}
}

// Removing the latest future period reopens the one before it.
func TestVatCodes_RemovingTheLatestFuturePeriodReopensThePrevious(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	m := manager(t, h)
	ratesPath := fmt.Sprintf("%s/%d/rates", vatCodesPath, vat15)
	var changed vatCodeJSON
	m.Do(http.MethodPost, ratesPath, map[string]any{"ratePercent": 16, "validFrom": "2027-01-01"}).JSON(&changed)

	res := m.Do(http.MethodDelete, fmt.Sprintf("%s/%d", ratesPath, changed.Rates[1].ID), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("DELETE the latest period = %d %s, want 200", res.Status, res.Body)
	}
	var after vatCodeJSON
	res.JSON(&after)
	if got := periods(after); got != "[15 2026-01-01..open]" {
		t.Errorf("periods after the removal = %s, want the 2026 period open again", got)
	}
}

// Every write to the VAT codes needs invoices:manage besides invoices:access
// (D3): a reader is refused with the access layer's 403 and nothing moves.
func TestVatCodes_EveryWriteNeedsManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	reader := h.SignIn(t, "invoices:access", "invoices:create", "invoices:issue")
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPut, fmt.Sprintf("%s/%d", vatCodesPath, vat25), map[string]any{"code": "3", "name": "X", "safTCode": "3", "ehfCategory": "S", "active": true, "revision": 1}},
		{http.MethodPost, fmt.Sprintf("%s/%d/rates", vatCodesPath, vat25), map[string]any{"ratePercent": 26, "validFrom": "2027-01-01"}},
		{http.MethodDelete, fmt.Sprintf("%s/%d/rates/%d", vatCodesPath, vat25, 1001), nil},
	} {
		if res := reader.Do(c.method, c.path, c.body); res.Status != http.StatusForbidden {
			t.Errorf("%s %s without invoices:manage = %d, want 403", c.method, c.path, res.Status)
		}
	}
	if c := listVatCodes(t, h)[vat25]; c.Revision != 1 || periods(c) != "[25 2026-01-01..open]" {
		t.Errorf("code 3 after the refused writes = %+v, want it untouched", c)
	}
}
