package invoices_test

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the stats summary (payments and delivery design D7): what is
// outstanding and overdue now, and what was issued, credited and paid in the
// period — the period's instants mapped to Oslo days, half-open on the day
// after the last instant inside it.

const statsSummaryPath = "/api/v1/invoices/stats/summary"

type statsSummaryJSON struct {
	From                  time.Time `json:"from"`
	To                    time.Time `json:"to"`
	OutstandingAmount     float64   `json:"outstandingAmount"`
	OutstandingCount      int32     `json:"outstandingCount"`
	OverdueAmount         float64   `json:"overdueAmount"`
	OverdueCount          int32     `json:"overdueCount"`
	IssuedCount           int32     `json:"issuedCount"`
	IssuedGrossTotal      float64   `json:"issuedGrossTotal"`
	IssuedGrossTotalDelta float64   `json:"issuedGrossTotalDelta"`
	CreditedCount         int32     `json:"creditedCount"`
	CreditedGrossTotal    float64   `json:"creditedGrossTotal"`
	PaidAmount            float64   `json:"paidAmount"`
	PaidCount             int32     `json:"paidCount"`
}

// statsSummary GETs the summary of query (with its leading '?', or "") as an
// invoices:access holder and fails on anything but 200.
func statsSummary(t *testing.T, h *harness, query string) statsSummaryJSON {
	t.Helper()
	res := h.SignIn(t, "invoices:access").Do(http.MethodGet, statsSummaryPath+query, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /invoices/stats/summary%s = %d %s, want 200", query, res.Status, res.Body)
	}
	var s statsSummaryJSON
	res.JSON(&s)
	return s
}

// periodQuery is ?from&to as the dashboard sends them, the offsets escaped.
func periodQuery(from, to time.Time) string {
	return "?from=" + url.QueryEscape(from.Format(time.RFC3339Nano)) + "&to=" + url.QueryEscape(to.Format(time.RFC3339Nano))
}

// plantIssuedOn writes an issued document issued on issueDate for customerAcme
// with gross: an invoice due on due, or — when credits is set — a credit note
// of that invoice, which has no due date.
func plantIssuedOn(t *testing.T, h *harness, number int64, issueDate string, credits *int64, gross, due string) int64 {
	t.Helper()
	kind, dueDate := "invoice", &due
	if credits != nil {
		kind, dueDate = "credit_note", nil
	}
	return modtest.One[int64](t, h.Harness, `
		INSERT INTO invoices.invoices (kind, status, number, customer_id, credits_invoice_id, issue_date, due_date, exchange_rate_date, seller_legal_name, buyer_name, gross_total, issued_at, created_by_user_id, created_at, updated_at)
		VALUES ($1::text, 'issued', $2, $3, $4, $5::date, $6::date, $5::date, 'Selger AS', 'Acme AS', $7::numeric, now(), $8, now(), now())
		RETURNING id`, kind, number, customerAcme, credits, issueDate, dueDate, gross, uuid.New())
}

// The figures over the planted set, at 12:00 in Oslo on 2026-09-12 and over
// the dashboard's period from the start of 2026-09-01 to the end of today:
//
//   - now: outstanding is every issued invoice with something open — open,
//     partially paid and overdue, each at its open amount; a paid and a
//     credited invoice are not, nor is the credit note; a removed payment
//     leaves its invoice open by the whole gross; overdue is the one past
//     its due date, at its open amount;
//   - in the period: the invoices issued on the first day, inside, and on
//     the last day — today, which a period ending at the end of today
//     includes — but not the one issued the day before the first; the credit
//     note; the live payments paid in it, not the removed one and not one
//     paid before it;
//   - the delta: against what was issued in the previous period of the same
//     length, [2026-08-20, 2026-09-01), which leaves out the day before it.
//
// A period ending exactly at an Oslo midnight ends with the day before it: the
// last day's invoice is then out.
func TestStatsSummary_TheFigures(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.Advance(-2 * time.Hour) // 10:00 UTC is 12:00 in Oslo (CEST)

	open := plantIssuedOn(t, h, 1, "2026-09-01", nil, "1000", "2026-09-26")
	removePayment(t, h, plantPayment(t, h, open, "500", "2026-09-05"), "Feil faktura")
	credited := plantIssuedOn(t, h, 2, "2026-09-02", nil, "700", "2026-09-20")
	paid := plantIssuedOn(t, h, 3, "2026-09-03", nil, "500", "2026-09-10")
	plantPayment(t, h, paid, "500", "2026-09-04")
	partial := plantIssuedOn(t, h, 4, "2026-09-05", nil, "2000.55", "2026-09-26")
	plantPayment(t, h, partial, "300.10", "2026-09-06")
	plantIssuedOn(t, h, 5, "2026-09-07", &credited, "700", "")
	plantIssuedOn(t, h, 6, "2026-09-12", nil, "4000", "2026-09-26")  // the last day
	plantIssuedOn(t, h, 7, "2026-08-31", nil, "16000", "2026-09-14") // the day before the first
	overdue := plantIssuedOn(t, h, 8, "2026-08-25", nil, "8000", "2026-09-08")
	plantPayment(t, h, overdue, "1000", "2026-08-30")
	older := plantIssuedOn(t, h, 9, "2026-08-19", nil, "32000", "2026-09-02") // before the previous period
	plantPayment(t, h, older, "32000", "2026-08-19")

	oslo, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, time.September, 1, 0, 0, 0, 0, oslo)
	to := time.Date(2026, time.September, 12, 23, 59, 59, 999_000_000, oslo)

	got := statsSummary(t, h, periodQuery(from, to))
	if !got.From.Equal(from) || !got.To.Equal(to) {
		t.Errorf("from/to = %s/%s, want the period as asked, %s/%s", got.From, got.To, from, to)
	}
	got.From, got.To = time.Time{}, time.Time{}
	want := statsSummaryJSON{
		// open 1000 + partial 1700.45 + the last day's 4000 + the day before's
		// 16000 + overdue 7000.
		OutstandingAmount: 29700.45, OutstandingCount: 5,
		OverdueAmount: 7000, OverdueCount: 1,
		// open 1000, credited 700, paid 500, partial 2000.55, the last day's 4000.
		IssuedCount: 5, IssuedGrossTotal: 8200.55,
		// against the day before's 16000 and overdue's 8000.
		IssuedGrossTotalDelta: -15799.45,
		CreditedCount:         1, CreditedGrossTotal: 700,
		// paid's 500 and partial's 300.10.
		PaidAmount: 800.10, PaidCount: 2,
	}
	if got != want {
		t.Errorf("summary =\n%+v\nwant\n%+v", got, want)
	}

	// The same period ending at the midnight that begins today: the last day
	// is 2026-09-11, and the invoice issued today is out.
	midnight := time.Date(2026, time.September, 12, 0, 0, 0, 0, oslo)
	if early := statsSummary(t, h, periodQuery(from, midnight)); early.IssuedCount != 4 || early.IssuedGrossTotal != 4200.55 {
		t.Errorf("ending at today's midnight: issued %d, %v; want 4, 4200.55 — without today's", early.IssuedCount, early.IssuedGrossTotal)
	}
}

// With no period the summary is the last 30 days up to now — today included,
// so a document issued today counts — and a period whose start is after its
// end is the dashboard's 400.
func TestStatsSummary_TheDefaultPeriodAndAnInvalidOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	plantIssuedOn(t, h, 1, "2026-09-12", nil, "1250.50", "2026-09-26")

	got := statsSummary(t, h, "")
	now := h.Now()
	if !got.From.Equal(now.AddDate(0, 0, -30)) || !got.To.Equal(now) {
		t.Errorf("from/to = %s/%s, want 30 days before now and now, %s", got.From, got.To, now)
	}
	if got.IssuedCount != 1 || got.IssuedGrossTotal != 1250.50 || got.IssuedGrossTotalDelta != 1250.50 ||
		got.OutstandingCount != 1 || got.OutstandingAmount != 1250.50 {
		t.Errorf("summary = %+v, want today's invoice issued in the period and outstanding", got)
	}

	res := h.SignIn(t, "invoices:access").Do(http.MethodGet,
		statsSummaryPath+"?from=2026-09-12T00:00:00Z&to=2026-09-01T00:00:00Z", nil)
	if res.Status != http.StatusBadRequest {
		t.Fatalf("from after to = %d %s, want 400", res.Status, res.Body)
	}
	if p := problemOf(t, res); p.Title != "Invalid period" {
		t.Errorf("from after to = %+v, want the title Invalid period", p)
	}
}
