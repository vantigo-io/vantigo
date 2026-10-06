package invoices_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The collection agency's file (invoices payments and reminders design D11):
// GET /invoices/collection-export.csv, on Saturday 12 September 2026.

const collectionExportPath = invoicesPath + "/collection-export.csv"

// collectionHeader is D11's header row, written out by hand.
const collectionHeader = "Invoice number;Issue date;Due date;Delivery;Delivered;KID;Customer number;Debtor;Debtor type;Org no;" +
	"Foreign id;Address line 1;Address line 2;Postal code;City;Country;E-mail;Gross;Credited;Paid;Principal open;Payments;" +
	"Fees claimed;Compensation claimed;Charges waived;Interest rate;Interest from;Interest to;Interest accrued;Charges paid;" +
	"Letters;Notice sent;Notice deadline;Disputed;Handed on;Agency;Agency reference"

// collectionCSV GETs the file for query as a payer.
func collectionCSV(t *testing.T, h *harness, query string) *modtest.Response {
	t.Helper()
	return chargePayer(t, h).Do(http.MethodGet, collectionExportPath+"?"+query, nil)
}

// collectionRows asserts res is the file and answers its rows by invoice
// number, the header checked.
func collectionRows(t *testing.T, what string, res *modtest.Response) map[string][]string {
	t.Helper()
	if res.Status != http.StatusOK {
		t.Fatalf("%s = %d %s, want 200", what, res.Status, res.Body)
	}
	header, rows := exportTable(t, res.Body)
	if got := strings.Join(header, ";"); got != collectionHeader {
		t.Errorf("%s: the header = %s, want %s", what, got, collectionHeader)
	}
	out := map[string][]string{}
	for _, r := range rows {
		out[r[0]] = r
	}
	return out
}

// badCollection asserts res is a 400.
func badCollection(t *testing.T, what string, res *modtest.Response) {
	t.Helper()
	if res.Status != http.StatusBadRequest {
		t.Errorf("%s = %d %s, want 400", what, res.Status, res.Body)
	}
}

// numbers is a file's invoice numbers, sorted.
func numbers(rows map[string][]string) []string {
	out := make([]string, 0, len(rows))
	for n := range rows {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// The two selections (D11): the live hand-offs made from handedFrom to
// handedTo — not one made before, not a withdrawn one, not an invoice never
// handed off — or the invoices named by invoiceId, repeated, handed off or
// not. 400 for neither, both, one date alone, handedFrom after handedTo, an
// id that is not an issued invoice, more than 500 ids — counted after each
// is taken once — and more than 500 rows — never a file cut short.
func TestCollectionExport_Selections(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := chargePayer(t, h)
	inPeriod := deliveredOn(t, h, 11, customerAcme, "2026-08-03")
	before := deliveredOn(t, h, 12, customerAcme, "2026-08-03")
	withdrawn := deliveredOn(t, h, 13, customerAcme, "2026-08-03")
	never := deliveredOn(t, h, 14, customerPerson, "2026-08-03")
	answered(t, "in the period", c.Do(http.MethodPost, handoffPath(inPeriod), handoffBody("2026-09-10", "Kredinor AS")))
	answered(t, "before it", c.Do(http.MethodPost, handoffPath(before), handoffBody("2026-09-07", "Kredinor AS")))
	answered(t, "withdrawn", c.Do(http.MethodPost, handoffPath(withdrawn), handoffBody("2026-09-10", "Kredinor AS")))
	answered(t, "its withdrawal", c.Do(http.MethodPost, handoffBackPath(withdrawn), map[string]any{"withdrawnOn": "2026-09-11", "reason": "Betalt"}))

	if got := numbers(collectionRows(t, "by dates", collectionCSV(t, h, "handedFrom=2026-09-08&handedTo=2026-09-12"))); !slices.Equal(got, []string{"11"}) {
		t.Errorf("by dates = %v, want the live hand-off in the period alone", got)
	}
	byID := collectionRows(t, "by id", collectionCSV(t, h, fmt.Sprintf("invoiceId=%d&invoiceId=%d&invoiceId=%d", never, inPeriod, never)))
	if got := numbers(byID); !slices.Equal(got, []string{"11", "14"}) {
		t.Errorf("by id = %v, want 11 and 14 once each", got)
	}
	if r := byID["14"]; r[34] != "" || r[35] != "" || r[33] != "no" {
		t.Errorf("the invoice never handed off = handed on %q agency %q disputed %q, want no hand-off", r[34], r[35], r[33])
	}

	badCollection(t, "neither", collectionCSV(t, h, ""))
	badCollection(t, "both", collectionCSV(t, h, fmt.Sprintf("handedFrom=2026-09-01&handedTo=2026-09-12&invoiceId=%d", never)))
	badCollection(t, "handedFrom alone", collectionCSV(t, h, "handedFrom=2026-09-01"))
	badCollection(t, "handedTo alone", collectionCSV(t, h, "handedTo=2026-09-01"))
	badCollection(t, "from after to", collectionCSV(t, h, "handedFrom=2026-09-12&handedTo=2026-09-01"))
	badCollection(t, "an id that is no issued invoice", collectionCSV(t, h, fmt.Sprintf("invoiceId=%d&invoiceId=99999", never)))
	ids := make([]string, 501)
	for i := range ids {
		ids[i] = fmt.Sprintf("invoiceId=%d", i+1)
	}
	badCollection(t, "501 ids", collectionCSV(t, h, strings.Join(ids, "&")))
	// The ids are compacted before the cap counts them: one invoice named
	// 501 times is one invoice.
	for i := range ids {
		ids[i] = fmt.Sprintf("invoiceId=%d", never)
	}
	if got := numbers(collectionRows(t, "one id 501 times", collectionCSV(t, h, strings.Join(ids, "&")))); !slices.Equal(got, []string{"14"}) {
		t.Errorf("one id 501 times = %v, want 14 once", got)
	}

	// 501 live hand-offs in the period: more than a file holds.
	h.Exec(t, `
		INSERT INTO invoices.invoices (kind, status, number, customer_id, issue_date, due_date, exchange_rate_date,
		    seller_legal_name, buyer_name, gross_total, issued_at, created_by_user_id, created_at, updated_at)
		SELECT 'invoice', 'issued', 1000 + n, 2001, DATE '2026-07-01', DATE '2026-07-15', DATE '2026-07-01',
		    'Selger AS', 'Kunde', 100, now(), gen_random_uuid(), now(), now()
		FROM generate_series(1, 501) n`)
	h.Exec(t, `
		INSERT INTO invoices.collection_handoffs (invoice_id, handed_on, agency, created_at, created_by_user_id)
		SELECT id, DATE '2026-09-01', 'Kredinor AS', now(), gen_random_uuid() FROM invoices.invoices WHERE number > 1000`)
	badCollection(t, "501 rows", collectionCSV(t, h, "handedFrom=2026-09-01&handedTo=2026-09-01"))
	if got := numbers(collectionRows(t, "the period without them", collectionCSV(t, h, "handedFrom=2026-09-08&handedTo=2026-09-12"))); !slices.Equal(got, []string{"11"}) {
		t.Errorf("by dates after the 501 = %v, want 11", got)
	}
}

// Every column of D11's header, in order, for one invoice that has each:
// invoice 7 to Acme — named like a formula, with a separator in its address
// — issued 1 July 2026, due Wednesday 15 July, delivered 30 June, handed
// over on 1 July (e-mailed on the 5th too), 1 000 gross, a credit note of
// 100 on 10 July, a payment of 200 on 1 August; a reminder sent 30 July
// with a fee of 35 waived goodwill, and the collection notice sent 20
// August with a fee of 35 and 3.00 of interest, of which 1.00 waived; a
// charge payment of 10; disputed, and handed off on 10 September to an
// agency named like a formula, guarded as every text column is. The
// principal stands apart: Principal open 700. Fees claimed is net of the
// waiver, 35; Charges waived 36 (the fee and the interest). The interest is
// the engine's at 12.25 % from 16 July to today, on 900 for 17 days and on
// 700 for 42 — 5.13 + 9.87, exactly 15.0020… — less the 1.00 waived: 14.00.
// Letters names each sent letter's day, level, deadline and fee, the waived
// one marked. The file is text/csv, never cached, named for today.
func TestCollectionExport_EachColumn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	remindersOn(t, h, "late_interest = true")
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.ReminderEmail = "purring@acme.example" })
	c := chargePayer(t, h)
	id := plantID(t, h, `
		INSERT INTO invoices.invoices (kind, status, number, customer_id, issue_date, due_date, delivery_date, exchange_rate_date,
		    seller_legal_name, buyer_customer_number, buyer_name, buyer_type, buyer_organisation_number, buyer_address_line1,
		    buyer_address_line2, buyer_postal_code, buyer_city, buyer_country, kid, kid_algorithm, gross_total, issued_at,
		    created_by_user_id, created_at, updated_at)
		VALUES ('invoice', 'issued', 7, 2001, DATE '2026-07-01', DATE '2026-07-15', DATE '2026-06-30', DATE '2026-07-01',
		    'Selger AS', 10001, '=Acme AS', 'business', '923609016', 'Storgata 1', 'c/o Regnskap; 2. etg', '0155', 'Oslo', 'NO',
		    '0000070', 'mod10', 1000, now(), gen_random_uuid(), now(), now())
		RETURNING id`)
	h.Exec(t, `
		INSERT INTO invoices.invoices (kind, status, number, customer_id, issue_date, credits_invoice_id, exchange_rate_date,
		    seller_legal_name, buyer_name, gross_total, issued_at, created_by_user_id, created_at, updated_at)
		VALUES ('credit_note', 'issued', 8, 2001, DATE '2026-07-10', $1, DATE '2026-07-10', 'Selger AS', '=Acme AS', 100, now(),
		    gen_random_uuid(), now(), now())`, id)
	plantPayment(t, h, id, "200", "2026-08-01")
	plantManualDelivery(t, h, id, "2026-07-01")
	plantEmail(t, h, id, h.Now().AddDate(0, -2, -7)) // 5 July
	reminder := plantLetterSent(t, h, id, 1, "reminder", "2026-07-30", "35", "0")
	notice := plantLetterSent(t, h, id, 2, "collection_notice", "2026-08-20", "35", "3.00")
	h.Exec(t, `INSERT INTO invoices.charge_waivers (invoice_id, reminder_id, kind, amount, interest_through, reason, waived_by_user_id, waived_at)
		VALUES ($1, $2, 'fee', 35, NULL, 'goodwill', gen_random_uuid(), now()),
		       ($1, $3, 'interest', 1, DATE '2026-08-20', 'goodwill', gen_random_uuid(), now())`, id, reminder, notice)
	plantChargePayment(t, h, id, "2026-09-01", "10")
	answered(t, "the hold", c.Do(http.MethodPost, holdPath(id), holdNote("Bestrider renten")))
	answered(t, "the hand-off", c.Do(http.MethodPost, handoffPath(id),
		withField(handoffBody("2026-09-10", "=Kredinor AS"), "agencyReference", "+47 K-1")))

	res := collectionCSV(t, h, "handedFrom=2026-09-10&handedTo=2026-09-10")
	if res.Status != http.StatusOK {
		t.Fatalf("the file = %d %s", res.Status, res.Body)
	}
	for header, want := range map[string]string{
		"Content-Type":        "text/csv; charset=utf-8",
		"Content-Disposition": `attachment; filename="invoices-collection-2026-09-12.csv"`,
		"Cache-Control":       "private, no-store",
	} {
		if got := res.Header(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	want := csvBOM + collectionHeader + "\r\n" +
		`7;2026-07-01;2026-07-15;2026-06-30;handed_over 2026-07-01;0000070;10001;'=Acme AS;business;923609016;;Storgata 1;` +
		`"c/o Regnskap; 2. etg";0155;Oslo;NO;purring@acme.example;1000,00;100,00;200,00;700,00;2026-08-01 200,00;` +
		`35,00;0,00;36,00;12,25;2026-07-16;2026-09-12;14,00;10,00;` +
		`2026-07-30 reminder deadline 2026-08-13 fee 35,00 (waived) | 2026-08-20 collection_notice deadline 2026-09-03 fee 35,00;` +
		`2026-08-20;2026-09-03;yes;2026-09-10;'=Kredinor AS;'+47 K-1` + "\r\n"
	if got := string(res.Body); got != want {
		t.Errorf("the file =\n%q\nwant\n%q", got, want)
	}
}

// The E-mail column is the directory's (D11), read before any query: when
// the read fails, the file is still made with the cell empty, and the
// failure is logged at warn.
func TestCollectionExport_DirectoryFailure(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.ReminderEmail = "purring@acme.example" })
	id := deliveredOn(t, h, 21, customerAcme, "2026-08-03")
	rows := collectionRows(t, "the address read", collectionCSV(t, h, fmt.Sprintf("invoiceId=%d", id)))
	if got := rows["21"][16]; got != "purring@acme.example" {
		t.Fatalf("E-mail = %q, want the reminder address", got)
	}
	h.customers.failProfiles(errors.New("the directory is down"))
	rows = collectionRows(t, "the directory failing", collectionCSV(t, h, fmt.Sprintf("invoiceId=%d", id)))
	if got := rows["21"][16]; got != "" {
		t.Errorf("E-mail = %q, want it empty when the directory fails", got)
	}
	logs := h.Logs()
	if !strings.Contains(logs, "reminder address could not be read") || !strings.Contains(logs, `"level":"WARN"`) {
		t.Errorf("the logs = %s, want the failure at warn", logs)
	}
}

// Waived charges are out of what is claimed (D11): a business invoice whose
// first letter claimed the compensation, waived objection_upheld by a
// barring lift, exports Compensation claimed 0,00 and Charges waived
// 360,00, its letter marked waived; a manual delivery is named by its own
// kind, posted.
func TestCollectionExport_WaivedChargesOut(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := chargePayer(t, h)
	id := plantOverdue(t, h, overdueSpec{number: 31, customer: customerAcme, issue: "2026-07-01", due: "2026-07-15",
		buyerType: "business", orgNo: "923609016"})
	h.Exec(t, `INSERT INTO invoices.manual_deliveries (invoice_id, kind, delivered_on, recorded_by_user_id, recorded_at)
		VALUES ($1, 'posted', DATE '2026-07-02', gen_random_uuid(), now())`, id)
	plantSent(t, h, id, sentFacts{1, "2026-07-30", "compensation", "", "360", "0"})
	answered(t, "the hold", c.Do(http.MethodPost, holdPath(id), holdNote("Bestrider")))
	answered(t, "the barring lift", c.Do(http.MethodPost, liftPath(id), liftBody(false, "")))
	row := collectionRows(t, "the file", collectionCSV(t, h, fmt.Sprintf("invoiceId=%d", id)))["31"]
	got := map[string]string{"Delivered": row[4], "Fees claimed": row[22], "Compensation claimed": row[23], "Charges waived": row[24], "Letters": row[30], "Disputed": row[33]}
	want := map[string]string{
		"Delivered": "posted 2026-07-02", "Fees claimed": "0,00", "Compensation claimed": "0,00", "Charges waived": "360,00",
		"Letters": "2026-07-30 reminder deadline 2026-08-13 compensation 360,00 (waived)", "Disputed": "no",
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %q, want %q", k, got[k], w)
		}
	}
}

// The directory is read with no transaction open (D18's lock rule, and a
// pool's arithmetic): on a pool of two, two exports for two customers run at
// once; the directory's hook waits until both have reached it, then takes a
// pool connection as the real directory's read would. Were either export
// holding its snapshot's connection there, the two would hold both and the
// directory would starve. Not parallel: a pool of two.
func TestCollectionExport_TwoExportsOnAPoolOfTwo(t *testing.T) {
	h := newHarness(t, modtest.WithPoolMaxConns(2))
	acme := deliveredOn(t, h, 41, customerAcme, "2026-08-03")
	kari := deliveredOn(t, h, 42, customerPerson, "2026-08-03")
	var arrivals atomic.Int32
	both := make(chan struct{})
	h.customers.afterProfileRead(func(int32) {
		if arrivals.Add(1) == 2 {
			close(both)
		}
		select {
		case <-both:
		case <-time.After(10 * time.Second):
			t.Errorf("the two exports never both reached the directory")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		conn, err := h.Pool().Acquire(ctx)
		if err != nil {
			t.Errorf("the directory's read starved for a pool connection: %v", err)
			return
		}
		conn.Release()
	})
	c := chargePayer(t, h)
	statuses := make([]int, 2)
	var wg sync.WaitGroup
	for i, id := range []int64{acme, kari} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i] = c.Do(http.MethodGet, fmt.Sprintf("%s?invoiceId=%d", collectionExportPath, id), nil).Status
		}()
	}
	wg.Wait()
	if !slices.Equal(statuses, []int{http.StatusOK, http.StatusOK}) {
		t.Errorf("the two exports = %v, want both 200", statuses)
	}
}
