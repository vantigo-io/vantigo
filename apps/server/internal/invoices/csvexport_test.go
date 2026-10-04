package invoices_test

import (
	"bytes"
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the accountant's CSV export (payments and delivery design D5):
// GET /invoices/export.csv. The bytes are a contract with a spreadsheet, so
// the golden test pins them exactly, from the byte order mark to the last
// CRLF; the rest read the file the way a spreadsheet would.

const exportPath = "/api/v1/invoices/export.csv"

// csvBOM is the UTF-8 byte order mark the file opens with.
const csvBOM = "\xef\xbb\xbf"

// exportHeader is D5's header row, fixed and English.
const exportHeader = "Number;Kind;Issue date;Delivery;Due;Customer number;Buyer;Buyer org no;Currency;SAF-T code;Rate;Base;VAT;Base NOK;VAT NOK;Credits number;KID"

// exportCSV GETs the export of query (no leading '?') as an invoices:access
// holder and fails on anything but 200.
func exportCSV(t *testing.T, h *harness, query string) *modtest.Response {
	t.Helper()
	res := h.SignIn(t, "invoices:access").Do(http.MethodGet, exportPath+"?"+query, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /export.csv?%s = %d %s, want 200", query, res.Status, res.Body)
	}
	return res
}

// exportTable reads a file the way a spreadsheet does: the BOM off,
// semicolons, quotes — the header first.
func exportTable(t *testing.T, body []byte) (header []string, rows [][]string) {
	t.Helper()
	reader := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(body, []byte(csvBOM))))
	reader.Comma = ';'
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("read the file: %v\n%s", err, body)
	}
	if len(records) == 0 {
		t.Fatalf("the file has no header row")
	}
	return records[0], records[1:]
}

// plantExportDocuments writes count issued invoices numbered from first on,
// each issued on issueDate with one VAT row, by SQL — the way the document
// lands when issued, but set-based, so five thousand cost three statements.
// Child rows first: each document is inserted as a draft, its VAT row while it
// is a draft, and only then is it issued — the immutability triggers allow
// every one of those writes because OLD is a draft, and nothing is disabled.
// Each is in EUR at 11.234567, its VAT row 25 % on 100.01, so its Base NOK is
// 100.01 × 11.234567 = 1123.56904567, at øre 1123,57.
func plantExportDocuments(t *testing.T, h *harness, first, count int, issueDate string) {
	t.Helper()
	batch := uuid.New()
	h.Exec(t, `
		INSERT INTO invoices.invoices (kind, customer_id, currency, exchange_rate, created_by_user_id, created_at, updated_at)
		SELECT 'invoice', 1, 'EUR', 11.234567, $1, now(), now() FROM generate_series(1, $2::int)`, batch, count)
	h.Exec(t, `
		INSERT INTO invoices.vat_summaries (invoice_id, vat_category, rate_percent, saf_t_code, taxable_amount, vat_amount, vat_amount_nok)
		SELECT id, 'S', 25, '3', 100.01, 25.00, 280.86 FROM invoices.invoices WHERE created_by_user_id = $1`, batch)
	h.Exec(t, `
		UPDATE invoices.invoices i
		SET status = 'issued', number = n.number, issue_date = $3::date, issued_at = now(), due_date = $3::date + 14,
		    seller_legal_name = 'Selger AS', buyer_name = 'Kunde AS', exchange_rate_date = $3::date
		FROM (SELECT id, $2::bigint - 1 + row_number() OVER (ORDER BY id) AS number
		      FROM invoices.invoices WHERE created_by_user_id = $1) n
		WHERE i.id = n.id`, batch, first, issueDate)
}

// TestExportCSV_IsExactlyTheseBytes is the golden sample (D5): an invoice with
// two VAT rows — 25 % and a 0 % exempt one — over a delivery period, to a
// buyer named like a formula; a second invoice to a person whose name holds
// the separator, delivered on a day; and a credit note of the first. One row
// per document and VAT row, in number order and in each document by category
// then rate; the credit note negative in every amount and unguarded there —
// a guarded -2000,00 is text in a spreadsheet — while every text column is
// guarded. The full file is written out by hand.
func TestExportCSV_IsExactlyTheseBytes(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.Name, p.LegalName = "=cmd", "=cmd" })
	h.customers.edit(customerPerson, func(p *contracts.CustomerBillingProfile) { p.Name = "Nordmann; Kari" })

	body := draftBody(customerAcme, line("Konsulenttime", 10, 1234.55, vat25), line("Kurs", 1, 2000, vatExempt))
	delete(body, "deliveryDate")
	body["deliveryFrom"], body["deliveryTo"] = "2026-09-01", "2026-09-10"
	original := issued(t, h, createDraft(t, h, body).ID)
	issued(t, h, createDraft(t, h, draftBody(customerPerson, line("Bok", 3, 99.9, vatZero))).ID)
	issued(t, h, creditDraft(t, h, original.ID).ID)

	want := csvBOM + strings.Join([]string{
		exportHeader,
		"1;invoice;2026-09-12;2026-09-01/2026-09-10;2026-10-12;10001;'=cmd;923609016;NOK;6;0,00;2000,00;0,00;2000,00;0,00;;",
		"1;invoice;2026-09-12;2026-09-01/2026-09-10;2026-10-12;10001;'=cmd;923609016;NOK;3;25,00;12345,50;3086,38;12345,50;3086,38;;",
		`2;invoice;2026-09-12;2026-09-10;2026-09-26;10002;"Nordmann; Kari";;NOK;5;0,00;299,70;0,00;299,70;0,00;;`,
		"3;credit_note;2026-09-12;2026-09-01/2026-09-10;;10001;'=cmd;923609016;NOK;6;0,00;-2000,00;0,00;-2000,00;0,00;1;",
		"3;credit_note;2026-09-12;2026-09-01/2026-09-10;;10001;'=cmd;923609016;NOK;3;25,00;-12345,50;-3086,38;-12345,50;-3086,38;1;",
		"",
	}, "\r\n")

	if got := string(exportCSV(t, h, "from=2026-09-01&to=2026-09-30").Body); got != want {
		t.Errorf("the export is\n%q\nwant\n%q", got, want)
	}
}

// TestExportCSV_BaseNOKRoundsHalfAwayFromZero: Base NOK is the one computed
// column (D5), at øre half away from zero, never half to even — 100.03 at
// 1.5 is 150.045, so 150,05 on the invoice and -150,05 on its credit note,
// rounded before it is negated.
func TestExportCSV_BaseNOKRoundsHalfAwayFromZero(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// plant writes one issued document in EUR at 1.5 with one VAT row of
	// 100.03, a draft first and its VAT row under it, as plantExportDocuments
	// does.
	plant := func(kind string, number int64, credits *int64) int64 {
		t.Helper()
		id := modtest.One[int64](t, h.Harness, `
			INSERT INTO invoices.invoices (kind, customer_id, currency, exchange_rate, credits_invoice_id, created_by_user_id, created_at, updated_at)
			VALUES ($1::text, 1, 'EUR', 1.5, $2, $3, now(), now()) RETURNING id`, kind, credits, uuid.New())
		h.Exec(t, `
			INSERT INTO invoices.vat_summaries (invoice_id, vat_category, rate_percent, saf_t_code, taxable_amount, vat_amount, vat_amount_nok)
			VALUES ($1, 'S', 25, '3', 100.03, 25.01, 37.52)`, id)
		h.Exec(t, `
			UPDATE invoices.invoices
			SET status = 'issued', number = $2, issue_date = '2026-09-12', issued_at = now(),
			    due_date = CASE WHEN kind = 'invoice' THEN DATE '2026-09-26' END,
			    seller_legal_name = 'Selger AS', buyer_name = 'Kunde AS', exchange_rate_date = '2026-09-12'
			WHERE id = $1`, id, number)
		return id
	}
	invoice := plant("invoice", 1, nil)
	plant("credit_note", 2, &invoice)

	header, rows := exportTable(t, exportCSV(t, h, "from=2026-09-01&to=2026-09-30").Body)
	baseNOK := -1
	for i, name := range header {
		if name == "Base NOK" {
			baseNOK = i
		}
	}
	if baseNOK < 0 || len(rows) != 2 {
		t.Fatalf("header %v, %d rows; want Base NOK and two rows", header, len(rows))
	}
	if rows[0][baseNOK] != "150,05" || rows[1][baseNOK] != "-150,05" {
		t.Errorf("Base NOK = %q and %q, want 150,05 and -150,05: half away from zero", rows[0][baseNOK], rows[1][baseNOK])
	}
}

// TestExportCSV_IsServedAsAFileNobodyCaches pins the headers a browser needs
// to save the file rather than show it, and its name: the period it covers.
// A file of no documents is still a file — its header row — and the export is
// invoices:access's.
func TestExportCSV_IsServedAsAFileNobodyCaches(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	res := exportCSV(t, h, "from=2026-09-01&to=2026-09-30")
	for header, want := range map[string]string{
		"Content-Type":        "text/csv; charset=utf-8",
		"Content-Disposition": `attachment; filename="invoices-2026-09-01-2026-09-30.csv"`,
		"Cache-Control":       "private, no-store",
		"Content-Length":      strconv.Itoa(len(res.Body)),
	} {
		if got := res.Header(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if got, want := string(res.Body), csvBOM+exportHeader+"\r\n"; got != want {
		t.Errorf("an empty period's file is %q, want %q", got, want)
	}
	if res := h.SignIn(t).Do(http.MethodGet, exportPath+"?from=2026-09-01&to=2026-09-30", nil); res.Status != http.StatusForbidden {
		t.Errorf("without invoices:access = %d, want 403", res.Status)
	}
}

// TestExportCSV_ThePeriodAndTheCap is the journal's selection — issue dates
// from and to inclusive, the day before and the day after out — and the cap:
// 5000 rows are a file, 5001 a 400 asking for a narrower period, never a
// file cut short. A planted document's Base NOK is its base times its
// exchange rate, at øre.
func TestExportCSV_ThePeriodAndTheCap(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	plantExportDocuments(t, h, 1, 1, "2026-08-31")
	plantExportDocuments(t, h, 2, 1, "2026-09-01")
	plantExportDocuments(t, h, 3, 1, "2026-09-30")
	plantExportDocuments(t, h, 4, 1, "2026-10-01")

	header, rows := exportTable(t, exportCSV(t, h, "from=2026-09-01&to=2026-09-30").Body)
	if len(rows) != 2 || rows[0][0] != "2" || rows[1][0] != "3" {
		t.Fatalf("the period's rows = %q, want documents 2 and 3 alone", rows)
	}
	cells := map[string]string{}
	for i, name := range header {
		cells[name] = rows[0][i]
	}
	if cells["Currency"] != "EUR" || cells["Base"] != "100,01" || cells["Base NOK"] != "1123,57" || cells["VAT NOK"] != "280,86" {
		t.Errorf("document 2 = %v, want EUR 100,01 at 1123,57 NOK and the stored 280,86 VAT NOK", cells)
	}

	plantExportDocuments(t, h, 5, 4998, "2026-09-15")
	if _, rows := exportTable(t, exportCSV(t, h, "from=2026-09-01&to=2026-09-30").Body); len(rows) != 5000 {
		t.Fatalf("5000 rows' file has %d rows, want all 5000", len(rows))
	}

	plantExportDocuments(t, h, 5003, 1, "2026-09-30")
	res := h.SignIn(t, "invoices:access").Do(http.MethodGet, exportPath+"?from=2026-09-01&to=2026-09-30", nil)
	if res.Status != http.StatusBadRequest {
		t.Fatalf("5001 rows = %d (%d bytes), want 400", res.Status, len(res.Body))
	}
	if p := problemOf(t, res); p.Title != "Too many rows to export" || !strings.Contains(p.Detail, "5000") || !strings.Contains(p.Detail, "narrow the period") {
		t.Errorf("5001 rows = %+v, want the cap's problem naming 5000 and asking for a narrower period", p)
	}
}

// TestExportCSV_TheDateRules: from and to are both required calendar dates,
// from on or before to; one day is a period.
func TestExportCSV_TheDateRules(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "invoices:access")

	for _, bad := range []string{"", "?to=2026-09-30", "?from=2026-09-01", "?from=2026-09-01&to=2026-09-31"} {
		if res := c.Do(http.MethodGet, exportPath+bad, nil); res.Status != http.StatusBadRequest {
			t.Errorf("GET /export.csv%s = %d %s, want 400", bad, res.Status, res.Body)
		}
	}
	res := c.Do(http.MethodGet, exportPath+"?from=2026-09-30&to=2026-09-01", nil)
	if res.Status != http.StatusBadRequest {
		t.Fatalf("from after to = %d %s, want 400", res.Status, res.Body)
	}
	if p := problemOf(t, res); p.Title != "Invalid query parameters" || !strings.Contains(p.Detail, "'from'") {
		t.Errorf("from after to = %+v, want the invalid-query problem naming from", p)
	}
	if res := c.Do(http.MethodGet, exportPath+"?from=2026-09-12&to=2026-09-12", nil); res.Status != http.StatusOK {
		t.Errorf("one day = %d %s, want 200", res.Status, res.Body)
	}
}

// The KID column is the seventeenth and last (EHF and KID design D3, reading
// 8): an invoice issued under the agreement carries its KID, leading zeros
// and all, unguarded; one issued before the agreement and a credit note
// leave it empty.
func TestExportCSV_TheKIDColumnLast(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	before := issued(t, h, createDraft(t, h, draftBody(customerPerson, line("Bok", 1, 100, vat25))).ID)
	withKidAgreement(t, h, 7, "mod10")
	under := issued(t, h, createDraft(t, h, draftBody(customerPerson, line("Bok", 1, 100, vat25))).ID)
	issued(t, h, creditDraft(t, h, under.ID).ID)
	if before.Kid != nil || under.Kid == nil || *under.Kid != "0000026" {
		t.Fatalf("kids = %v and %v, want none and 0000026", before.Kid, under.Kid)
	}

	header, rows := exportTable(t, exportCSV(t, h, "from=2026-09-01&to=2026-09-30").Body)
	if len(header) != 17 || header[16] != "KID" {
		t.Fatalf("header = %v, want KID as the seventeenth column", header)
	}
	want := []string{"", "0000026", ""}
	if len(rows) != len(want) {
		t.Fatalf("rows = %v, want three", rows)
	}
	for i, r := range rows {
		if len(r) != 17 || r[16] != want[i] {
			t.Errorf("row %d = %v, want KID %q last", i+1, r, want[i])
		}
	}
}
