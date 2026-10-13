package invoices_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

const (
	vippsPaymentsPath    = invoicesPath + "/vipps-payments"
	vippsPaymentsCSVPath = invoicesPath + "/vipps-payments.csv"
	reconciliationsPath  = vippsPaymentsPath + "/reconciliations"
)

// vippsPaymentJSON is one row of the day's list.
type vippsPaymentJSON struct {
	AttemptID     int64     `json:"attemptId"`
	Reference     string    `json:"reference"`
	InvoiceID     int64     `json:"invoiceId"`
	InvoiceNumber int64     `json:"invoiceNumber"`
	BuyerName     string    `json:"buyerName"`
	Flow          string    `json:"flow"`
	Amount        float64   `json:"amount"`
	CapturedAt    time.Time `json:"capturedAt"`
}

// reconciliationJSON is a day's reconciliation record.
type reconciliationJSON struct {
	Day          string    `json:"day"`
	Payments     int32     `json:"payments"`
	Total        float64   `json:"total"`
	OnSiteTotal  float64   `json:"onSiteTotal"`
	PayLinkTotal float64   `json:"payLinkTotal"`
	ReconciledBy string    `json:"reconciledBy"`
	ReconciledAt time.Time `json:"reconciledAt"`
	Note         string    `json:"note"`
}

// vippsDayJSON is the day's list.
type vippsDayJSON struct {
	Date           string              `json:"date"`
	Payments       []vippsPaymentJSON  `json:"payments"`
	Count          int32               `json:"count"`
	Total          float64             `json:"total"`
	OnSiteTotal    float64             `json:"onSiteTotal"`
	PayLinkTotal   float64             `json:"payLinkTotal"`
	Reconciliation *reconciliationJSON `json:"reconciliation"`
}

// plantCaptured writes a captured attempt of invoice id on flow — a
// pay-link one under its live link — of amount, captured at.
func plantCaptured(t *testing.T, h *harness, id int64, flow, amount string, at time.Time) int64 {
	t.Helper()
	link, nonce := "NULL", "NULL"
	if flow == "pay_link" {
		link = "(SELECT id FROM invoices.pay_links WHERE invoice_id = $1 AND revoked_at IS NULL)"
		nonce = "repeat('0', 64)"
	}
	return modtest.One[int64](t, h.Harness, `
		INSERT INTO invoices.payment_attempts (invoice_id, provider, msn, flow, pay_link_id, return_nonce_sha256, amount, cancel_key,
		    state, created_at, expires_at, authorized_amount, authorized_at, captured_amount, captured_at, finished_at)
		VALUES ($1, 'vipps', '123456', $2::text, `+link+`, `+nonce+`, $3::numeric, gen_random_uuid(), 'captured',
		    $4::timestamptz - interval '1 minute', $4::timestamptz + interval '9 minutes', $3::numeric,
		    $4::timestamptz - interval '30 seconds', $3::numeric, $4::timestamptz, $4::timestamptz)
		RETURNING id`, id, flow, amount, at)
}

func vippsDay(t *testing.T, c *modtest.Client, date string) vippsDayJSON {
	t.Helper()
	res := c.Do(http.MethodGet, vippsPaymentsPath+"?date="+date, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET the day's list %s = %d %s, want 200", date, res.Status, res.Body)
	}
	var out vippsDayJSON
	res.JSON(&out)
	return out
}

// TestVippsPayments_DayList (D17 rule 4): one Oslo day's Vipps payments by
// when they were captured — Oslo's midnight the edge at both ends, in
// summer two hours before UTC's — both flows, oldest first, with the
// totals per flow; the CSV is the same rows in the module's CSV form. Read
// with invoices:access; a bad or missing date is a 400.
func TestVippsPayments_DayList(t *testing.T) {
	t.Parallel()
	h, _ := payReady(t)
	a, b := issuedAcme(t, h), issuedAcme(t, h)
	start := time.Date(2026, 9, 10, 22, 0, 0, 0, time.UTC) // 2026-09-11 00:00 in Oslo
	plantCaptured(t, h, a.ID, "on_site", "100.00", start.Add(-time.Second))
	first := plantCaptured(t, h, a.ID, "pay_link", "1250.50", start)
	second := plantCaptured(t, h, b.ID, "on_site", "300.25", start.Add(13*time.Hour))
	last := plantCaptured(t, h, b.ID, "pay_link", "49.75", start.Add(24*time.Hour-time.Second))
	plantCaptured(t, h, b.ID, "on_site", "7.00", start.Add(24*time.Hour))
	// A captured attempt's live peers that are not captured are not listed.
	h.Exec(t, `INSERT INTO invoices.payment_attempts (invoice_id, provider, msn, flow, amount, cancel_key, state, created_at, expires_at)
		VALUES ($1, 'vipps', '123456', 'on_site', 10, gen_random_uuid(), 'created', $2::timestamptz, $2::timestamptz + interval '10 minutes')`,
		a.ID, start.Add(time.Hour))

	reader := h.SignIn(t, "invoices:access")
	day := vippsDay(t, reader, "2026-09-11")
	var ids []int64
	for _, p := range day.Payments {
		ids = append(ids, p.AttemptID)
	}
	if len(ids) != 3 || ids[0] != first || ids[1] != second || ids[2] != last {
		t.Fatalf("the day's attempts = %v, want %d, %d, %d", ids, first, second, last)
	}
	if day.Date != "2026-09-11" || day.Count != 3 || day.Total != 1600.5 || day.OnSiteTotal != 300.25 || day.PayLinkTotal != 1300.25 || day.Reconciliation != nil {
		t.Errorf("the day = %+v, want 3 payments of 1600.50: 300.25 on site, 1300.25 by pay link, not reconciled", day)
	}
	p := day.Payments[0]
	if p.InvoiceID != a.ID || p.InvoiceNumber != *a.Number || p.BuyerName != "Acme AS Norge" || p.Flow != "pay_link" || p.Amount != 1250.5 ||
		!p.CapturedAt.Equal(start) || p.Reference == "" {
		t.Errorf("the first row = %+v", p)
	}

	res := reader.Do(http.MethodGet, vippsPaymentsCSVPath+"?date=2026-09-11", nil)
	if res.Status != http.StatusOK || !strings.HasPrefix(res.Header("Content-Type"), "text/csv") ||
		res.Header("Cache-Control") != "private, no-store" ||
		!strings.Contains(res.Header("Content-Disposition"), `filename="vipps-payments-2026-09-11.csv"`) {
		t.Fatalf("the CSV = %d %s", res.Status, res.Body)
	}
	want := csvBOM + strings.Join([]string{
		"Date;Time;Invoice number;Buyer;Flow;Amount;Reference",
		"2026-09-11;00:00:00;" + itoa(*a.Number) + ";Acme AS Norge;pay_link;1250,50;" + day.Payments[0].Reference,
		"2026-09-11;13:00:00;" + itoa(*b.Number) + ";Acme AS Norge;on_site;300,25;" + day.Payments[1].Reference,
		"2026-09-11;23:59:59;" + itoa(*b.Number) + ";Acme AS Norge;pay_link;49,75;" + day.Payments[2].Reference,
		"",
	}, "\r\n")
	if got := string(res.Body); got != want {
		t.Errorf("the CSV is\n%q\nwant the JSON's rows\n%q", got, want)
	}

	if empty := vippsDay(t, reader, "2026-08-01"); empty.Count != 0 || len(empty.Payments) != 0 || empty.Total != 0 {
		t.Errorf("a day without payments = %+v", empty)
	}
	for _, q := range []string{"", "?date=2026-13-01", "?date=yesterday"} {
		if res := reader.Do(http.MethodGet, vippsPaymentsPath+q, nil); res.Status != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", q, res.Status)
		}
		if res := reader.Do(http.MethodGet, vippsPaymentsCSVPath+q, nil); res.Status != http.StatusBadRequest {
			t.Errorf("GET the CSV %s = %d, want 400", q, res.Status)
		}
	}
}

// TestVippsPayments_Reconcile (D17 rule 4, M15): a day's reconciliation
// records the totals as they stand, by whom and when, once; the day must
// be over; the record never changes — a capture dated that day later
// changes the list, not the record. It needs invoices:payments.
func TestVippsPayments_Reconcile(t *testing.T) {
	t.Parallel()
	h, _ := payReady(t)
	inv := issuedAcme(t, h)
	start := time.Date(2026, 9, 10, 22, 0, 0, 0, time.UTC) // 2026-09-11 in Oslo
	plantCaptured(t, h, inv.ID, "on_site", "1000.00", start.Add(9*time.Hour))
	plantCaptured(t, h, inv.ID, "pay_link", "250.00", start.Add(15*time.Hour))

	if res := h.SignIn(t, "invoices:access").Do(http.MethodPost, reconciliationsPath, map[string]any{"day": "2026-09-11"}); res.Status != http.StatusForbidden {
		t.Errorf("without invoices:payments = %d, want 403", res.Status)
	}
	c, userID := h.SignInUser(t, "invoices:access", "invoices:payments")
	for name, body := range map[string]map[string]any{
		"no day":          {"note": "x"},
		"a note over 500": {"day": "2026-09-11", "note": strings.Repeat("x", 501)},
	} {
		if res := c.Do(http.MethodPost, reconciliationsPath, body); res.Status != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", name, res.Status, res.Body)
		}
	}
	for _, day := range []string{"2026-09-12", "2026-09-13"} {
		onSiteRefused(t, "reconciling "+day, c.Do(http.MethodPost, reconciliationsPath, map[string]any{"day": day}), "day_not_over")
	}

	res := c.Do(http.MethodPost, reconciliationsPath, map[string]any{"day": "2026-09-11", "note": "  Stemmer med Vipps-oppgjøret  "})
	if res.Status != http.StatusCreated {
		t.Fatalf("the reconciliation = %d %s, want 201", res.Status, res.Body)
	}
	var rec reconciliationJSON
	res.JSON(&rec)
	if rec.Day != "2026-09-11" || rec.Payments != 2 || rec.Total != 1250 || rec.OnSiteTotal != 1000 || rec.PayLinkTotal != 250 ||
		rec.ReconciledBy != userID.String() || !rec.ReconciledAt.Equal(h.Now()) || rec.Note != "Stemmer med Vipps-oppgjøret" {
		t.Errorf("the reconciliation = %+v", rec)
	}
	onSiteRefused(t, "reconciling the day again", c.Do(http.MethodPost, reconciliationsPath, map[string]any{"day": "2026-09-11"}), "day_already_reconciled")

	// A capture registered later, dated that day: the list moves, the record does not.
	plantCaptured(t, h, inv.ID, "on_site", "40.00", start.Add(20*time.Hour))
	day := vippsDay(t, h.SignIn(t, "invoices:access"), "2026-09-11")
	if day.Count != 3 || day.Total != 1290 || day.Reconciliation == nil || day.Reconciliation.Total != 1250 || day.Reconciliation.Payments != 2 {
		t.Errorf("the day after a late capture = %+v (record %+v), want 3 payments of 1290 and the record of 2 and 1250", day, day.Reconciliation)
	}
	if _, err := h.Pool().Exec(t.Context(), `UPDATE invoices.vipps_day_reconciliations SET total = 1290`); err == nil {
		t.Error("the record was changed")
	}

	// A day with no payments reconciles at zero.
	res = c.Do(http.MethodPost, reconciliationsPath, map[string]any{"day": "2026-09-01"})
	if res.Status != http.StatusCreated {
		t.Fatalf("an empty day = %d %s, want 201", res.Status, res.Body)
	}
	res.JSON(&rec)
	if rec.Payments != 0 || rec.Total != 0 || rec.Note != "" {
		t.Errorf("an empty day's record = %+v", rec)
	}
}
