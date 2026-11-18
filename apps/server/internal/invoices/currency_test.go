package invoices_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile/bankfiletest"
	"github.com/vantigo-io/vantigo/server/internal/invoices/kid"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is delivery C of invoices phase 6, foreign currency (design
// I17–I28; plan C12): the settings' currencies, the rates, a draft and an
// invoice in EUR, its payments and their exchange difference, the queue, and
// the refusals that keep a foreign amount away from Vipps and matching.

const ratesPath = "/api/v1/invoices/exchange-rates"

// failMatchesOf makes every match of a bank line carrying KID k fail inside
// its transaction, and answers the restore. The hook is the package's: a
// test using it does not run in parallel.
func failMatchesOf(t *testing.T, h *harness, k string) func() {
	t.Helper()
	return invoices.SetMatchAfterLineLock(func(ctx context.Context, id int64) error {
		var got string
		if err := h.Pool().QueryRow(ctx, `SELECT coalesce(kid, '') FROM invoices.bank_transactions WHERE id = $1`, id).Scan(&got); err != nil {
			return err
		}
		if got == k {
			return errors.New("test: this line's match fails")
		}
		return nil
	})
}

// settingsRevision is the settings' revision now.
func settingsRevision(t *testing.T, h *harness) int32 {
	t.Helper()
	var s settingsJSON
	h.SignIn(t, "invoices:access").Do(http.MethodGet, settingsPath, nil).JSON(&s)
	return s.Revision
}

// withCurrencies is the complete seller with currencies turned on, under the
// KID agreement when withKid.
func withCurrencies(t *testing.T, h *harness, withKid bool, currencies ...string) map[string]any {
	t.Helper()
	body := completeSeller(settingsRevision(t, h))
	if withKid {
		body["kidLength"], body["kidAlgorithm"] = 7, kid.Mod10
	}
	body["currencies"] = currencies
	return body
}

// euroOn publishes EUR at 11.5 for the autumn and turns it on.
func euroOn(t *testing.T, h *harness, withKid bool) {
	t.Helper()
	h.rates.SetRange("EUR", "2026-08-01", "2026-12-31", "11.5")
	saveSeller(t, h, withCurrencies(t, h, withKid, "EUR"))
}

// euroInvoice is an issued invoice of EUR 1000.00 (800 + 25 % VAT) to the
// customer whose profile is in EUR, at 11.5: NOK 11 500.00.
func euroInvoice(t *testing.T, h *harness) invoiceJSON {
	t.Helper()
	inv := issued(t, h, createDraft(t, h, draftBody(customerEuro, line("Konsulenttime", 10, 80, vat25))).ID)
	if inv.Currency != "EUR" || inv.ExchangeRate != 11.5 || inv.GrossTotal != 1000 {
		t.Fatalf("the invoice = %s %v at %v, want EUR 1000 at 11.5", inv.Currency, inv.GrossTotal, inv.ExchangeRate)
	}
	return inv
}

// openNOK is invoices.open_nok of id, as text.
func openNOK(t *testing.T, h *harness, id int64) string {
	t.Helper()
	return modtest.One[string](t, h.Harness, `SELECT invoices.open_nok($1)::text`, id)
}

// Turning a currency on (I17, I18, I22): the IBAN and BIC first, then a rate
// within 7 days — Norges Bank's last 30 days are fetched before it is judged,
// a manual rate entered earlier satisfies it — and the fetch row is listed.
func TestCurrency_TurningOnNeedsTheIBANAndARate(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	m := h.SignIn(t, "invoices:access", "invoices:manage")

	body := withCurrencies(t, h, false, "EUR")
	body["iban"], body["bic"] = "", ""
	if p := problemOf(t, m.Do(http.MethodPut, settingsPath, body)); p.Code != "iban_required" {
		t.Errorf("without the IBAN = %+v, want iban_required", p)
	}
	h.rates.FailOn("EUR", http.StatusServiceUnavailable)
	res := m.Do(http.MethodPut, settingsPath, withCurrencies(t, h, false, "EUR"))
	if p := problemOf(t, res); res.Status != http.StatusConflict || p.Code != "exchange_rate_missing" {
		t.Errorf("Norges Bank failing = %d %+v, want 409 exchange_rate_missing", res.Status, p)
	}
	if res := m.Do(http.MethodPut, ratesPath+"/EUR/2026-09-11", map[string]any{"rate": 11.7234}); res.Status != http.StatusOK {
		t.Fatalf("a manual rate = %d %s", res.Status, res.Body)
	}
	if res := m.Do(http.MethodPut, settingsPath, withCurrencies(t, h, false, "EUR")); res.Status != http.StatusOK {
		t.Fatalf("with a manual rate = %d %s, want 200", res.Status, res.Body)
	}
	var s struct {
		Currencies  []string `json:"currencies"`
		RateFetches []struct {
			Currency  string  `json:"currency"`
			LastError *string `json:"lastError"`
		} `json:"rateFetches"`
	}
	m.Do(http.MethodGet, settingsPath, nil).JSON(&s)
	if !slices.Equal(s.Currencies, []string{"EUR"}) || len(s.RateFetches) != 1 || s.RateFetches[0].LastError == nil ||
		*s.RateFetches[0].LastError != "answered_503" {
		t.Errorf("the settings = %+v, want EUR on with its failed fetch named", s)
	}
	for _, bad := range [][]string{{"NOK"}, {"XXX"}} {
		if res := m.Do(http.MethodPut, settingsPath, withCurrencies(t, h, false, bad...)); res.Status != http.StatusBadRequest {
			t.Errorf("currencies %v = %d, want 400", bad, res.Status)
		}
	}
}

// The rates (I18, reading 4): a fetch never overwrites a manual rate, a
// fetched rate is never removed, a manual one is and its day is fetched
// again at once (review minor 7); the seven-day rule after a weekend.
func TestCurrency_ManualRatesAndTheFetch(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	euroOn(t, h, false)
	m := h.SignIn(t, "invoices:access", "invoices:manage")
	if res := m.Do(http.MethodPut, ratesPath+"/EUR/2026-09-10", map[string]any{"rate": 12}); res.Status != http.StatusOK {
		t.Fatalf("a manual rate = %d %s", res.Status, res.Body)
	}
	h.rates.Set("EUR", "2026-09-10", "11.9")
	if res := m.Do(http.MethodPost, ratesPath+"/fetch", nil); res.Status != http.StatusOK {
		t.Fatalf("Fetch now = %d %s", res.Status, res.Body)
	}
	if got := modtest.One[string](t, h.Harness, `SELECT rate::text || ' ' || source FROM invoices.exchange_rates WHERE currency = 'EUR' AND rate_date = '2026-09-10'`); got != "12.0000000000 manual" {
		t.Errorf("the manual day after a fetch = %s, want it kept", got)
	}
	if p := problemOf(t, m.Do(http.MethodDelete, ratesPath+"/EUR/2026-09-09", nil)); p.Code != "exchange_rate_fetched" {
		t.Errorf("removing a fetched rate = %+v, want exchange_rate_fetched", p)
	}
	if res := m.Do(http.MethodDelete, ratesPath+"/EUR/2026-09-10", nil); res.Status != http.StatusNoContent {
		t.Errorf("removing the manual rate = %d, want 204", res.Status)
	}
	if got := modtest.One[string](t, h.Harness, `SELECT rate::text || ' ' || source FROM invoices.exchange_rates WHERE currency = 'EUR' AND rate_date = '2026-09-10'`); got != "11.9000000000 norges_bank" {
		t.Errorf("the day after removing its manual rate = %s, want Norges Bank's fetched again", got)
	}
	if _, err := h.Pool().Exec(context.Background(), `UPDATE invoices.exchange_rates SET source = 'norges_bank', entered_by_user_id = NULL WHERE source = 'manual'`); err != nil {
		t.Fatal(err)
	}
	h.Exec(t, `INSERT INTO invoices.exchange_rates (currency, rate_date, rate, source, fetched_at, entered_by_user_id)
		VALUES ('EUR', '2026-08-30', 13, 'manual', now(), gen_random_uuid())`)
	if _, err := h.Pool().Exec(context.Background(), `UPDATE invoices.exchange_rates SET source = 'norges_bank', entered_by_user_id = NULL
		WHERE currency = 'EUR' AND rate_date = '2026-08-30'`); err == nil {
		t.Error("the trigger let a manual rate become a fetched one")
	}
	var list struct {
		Items []struct {
			Day    string  `json:"day"`
			Rate   float64 `json:"rate"`
			Source string  `json:"source"`
		} `json:"items"`
	}
	h.SignIn(t, "invoices:access").Do(http.MethodGet, ratesPath+"?currency=EUR&from=2026-09-05&to=2026-09-10", nil).JSON(&list)
	if len(list.Items) != 4 || list.Items[0].Day != "2026-09-10" || list.Items[0].Source != "norges_bank" {
		t.Errorf("the list = %+v, want the four business days, the newest first", list.Items)
	}
}

// A draft in EUR (I19, I28): the profile's currency when it is on, the rate
// with its day and source, an agreed rate kept at issue, an automatic one
// resolved again for the issue date; refused without a rate or the IBAN.
func TestCurrency_TheDraftAndTheIssue(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	before := createDraft(t, h, draftBody(customerEuro, line("Konsulenttime", 10, 80, vat25)))
	if before.Currency != "NOK" {
		t.Errorf("a draft for a EUR profile before EUR is on = %s, want NOK", before.Currency)
	}
	euroOn(t, h, false)
	d := createDraft(t, h, draftBody(customerEuro, line("Konsulenttime", 10, 80, vat25)))
	if d.Currency != "EUR" || d.ExchangeRate != 11.5 || d.VatTotalNok != 2300 {
		t.Fatalf("the draft = %s at %v, VAT NOK %v; want EUR at 11.5, 2300", d.Currency, d.ExchangeRate, d.VatTotalNok)
	}
	// Norges Bank's rate of the issue date is taken at issue.
	h.Exec(t, `UPDATE invoices.exchange_rates SET rate = 11.6 WHERE currency = 'EUR' AND rate_date = '2026-09-11'`)
	inv := issued(t, h, d.ID)
	if inv.ExchangeRate != 11.6 || inv.ExchangeRateDate == nil || *inv.ExchangeRateDate != "2026-09-11" {
		t.Errorf("issued at %v of %v, want 11.6 of 2026-09-11 (the Friday before)", inv.ExchangeRate, inv.ExchangeRateDate)
	}
	if got := modtest.One[string](t, h.Harness, `SELECT exchange_rate_source || ' ' || (invoices.document_nok(id)).gross_nok::text FROM invoices.invoices WHERE id = $1`, inv.ID); got != "norges_bank 11600.00" {
		t.Errorf("the issued invoice = %s, want norges_bank 11600.00", got)
	}
	// An agreed rate stands.
	agreed := draftBody(customerEuro, line("Konsulenttime", 1, 100, vat25))
	agreed["exchangeRate"], agreed["exchangeRateDate"] = 11.1, "2026-09-01"
	a := issued(t, h, createDraft(t, h, agreed).ID)
	if a.ExchangeRate != 11.1 || *a.ExchangeRateDate != "2026-09-01" {
		t.Errorf("the agreed rate = %v of %v, want 11.1 of 2026-09-01", a.ExchangeRate, a.ExchangeRateDate)
	}
	nok := draftBody(customerAcme, line("Konsulenttime", 1, 100, vat25))
	nok["exchangeRate"] = 11.1
	if res := creator(t, h).Do(http.MethodPost, invoicesPath, nok); res.Status != http.StatusBadRequest {
		t.Errorf("a rate on NOK = %d, want 400", res.Status)
	}
	// Without a rate within 7 days the issue refuses.
	missing := createDraft(t, h, draftBody(customerEuro, line("Konsulenttime", 1, 100, vat25)))
	h.Exec(t, `DELETE FROM invoices.exchange_rates WHERE source = 'manual'`)
	h.Exec(t, `ALTER TABLE invoices.exchange_rates DISABLE TRIGGER tr_exchange_rates_overwrite`)
	h.Exec(t, `DELETE FROM invoices.exchange_rates WHERE rate_date >= '2026-09-04'`)
	h.Exec(t, `ALTER TABLE invoices.exchange_rates ENABLE TRIGGER tr_exchange_rates_overwrite`)
	if p := refusedWith(t, h, missing.ID, "", "exchange_rate_missing"); p.Code != "exchange_rate_missing" {
		t.Errorf("the issue = %+v", p)
	}
}

// An invoice in EUR is paid in NOK (I24): each payment carries what arrived
// in NOK and its exchange difference; the closing one clears the NOK rest
// exactly, so no phantom øre is left; Vipps, a pay link and charges are
// refused currency_not_nok (I22, reading 6).
func TestCurrency_PaymentsAndTheExchangeDifference(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	euroOn(t, h, false)
	inv := euroInvoice(t, h)
	c := payer(t, h)
	if p := problemOf(t, c.Do(http.MethodPost, paymentsPath(inv.ID), pay(333.33, "2026-09-12"))); len(p.Errors["amountNok"]) == 0 {
		t.Errorf("a payment without amountNok = %+v, want 400 on amountNok", p)
	}
	first := pay(333.33, "2026-09-12")
	first["amountNok"] = 3900
	if res := c.Do(http.MethodPost, paymentsPath(inv.ID), first); res.Status != http.StatusOK {
		t.Fatalf("the first payment = %d %s", res.Status, res.Body)
	}
	if got := openNOK(t, h, inv.ID); got != "7666.70" {
		t.Errorf("open in NOK after 333.33 = %s, want 11500.00 − round(333.33 × 11.5) = 7666.70", got)
	}
	second := pay(666.67, "2026-09-12")
	second["amountNok"] = 7600.01
	if res := c.Do(http.MethodPost, paymentsPath(inv.ID), second); res.Status != http.StatusOK {
		t.Fatalf("the closing payment = %d %s", res.Status, res.Body)
	}
	if got := texts(t, h, `SELECT amount || ' ' || amount_nok || ' ' || exchange_difference_nok FROM invoices.payments WHERE invoice_id = $1 ORDER BY id`, inv.ID); !slices.Equal(got,
		[]string{"333.33 3900.00 66.70", "666.67 7600.01 -66.69"}) {
		t.Errorf("the payments = %v, want a gain of 66.70 and the closing one's loss against the NOK rest", got)
	}
	if got := openNOK(t, h, inv.ID); got != "0.00" {
		t.Errorf("open in NOK after the closing payment = %s, want exactly 0.00", got)
	}
	if p := problemOf(t, h.SignIn(t, "invoices:access", "invoices:issue").Do(http.MethodPost, payLinkPath(inv.ID), nil)); p.Code != "currency_not_nok" {
		t.Errorf("a pay link = %+v, want currency_not_nok", p)
	}
	if p := problemOf(t, chargePayer(t, h).Do(http.MethodPost, chargePaymentsPath(inv.ID), pay(10, "2026-09-12"))); p.Code != "currency_not_nok" {
		t.Errorf("a charge payment = %+v, want currency_not_nok", p)
	}
}

// open_nok() is the open amount for NOK (plan C1): an invoice, partly paid,
// credited and written off.
func TestCurrency_OpenNOKIsTheOpenAmountForNOK(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := thousand(t, h)
	payer(t, h).Do(http.MethodPost, paymentsPath(inv.ID), pay(123.45, "2026-09-12"))
	got := modtest.One[string](t, h.Harness, `SELECT invoices.open_nok(i.id)::text || ' ' || (i.gross_total - invoices.settled_amount(i.id))::text
		FROM invoices.invoices i WHERE i.id = $1`, inv.ID)
	if got != "876.55 876.55" {
		t.Errorf("open_nok and the open amount = %s, want equal", got)
	}
}

// The queue (I24): matching never pays an invoice in another currency — the
// line is queued currency_differs, its invoice the suggestion — and a person
// applies it with the NOK share; the line's NOK rest is kept as credit in
// NOK.
func TestCurrency_TheQueue(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	euroOn(t, h, true)
	inv := kidded(t, euroInvoice(t, h))
	toMatchDay(h)
	r := imported(t, importer(t, h), bankfiletest.OCR("1", kidPay(sellerAccount, 6, 12000, *inv.Kid, "1")))
	id := lineID(t, h, r.File.ID, "1")
	if got := stateOf(t, h, id); got != "exception currency_differs" {
		t.Fatalf("the line = %s, want queued currency_differs", got)
	}
	if got := paymentsFrom(t, h, id); len(got) != 0 {
		t.Errorf("payments from the line = %v, want none", got)
	}
	c := importer(t, h)
	noShare := map[string]any{"allocations": []map[string]any{{"invoiceId": inv.ID, "amount": 1000}}}
	invalidOn(t, "an apply without the NOK share", c.Do(http.MethodPost, queueActionPath(id, "apply"), noShare), "allocations[0].amountNok")
	keep := map[string]any{"invoiceId": inv.ID, "amountNok": 11400}
	if res := c.Do(http.MethodPost, queueActionPath(id, "keep-as-credit"), keep); res.Status != http.StatusOK {
		t.Fatalf("keep as credit = %d %s", res.Status, res.Body)
	}
	if got := texts(t, h, `SELECT amount || ' ' || amount_nok || ' ' || exchange_difference_nok FROM invoices.payments WHERE bank_transaction_id = $1`, id); !slices.Equal(got, []string{"1000.00 11400.00 -100.00"}) {
		t.Errorf("the payment = %v, want EUR 1000 for NOK 11400, a loss of 100 against 11500", got)
	}
	if got := texts(t, h, `SELECT currency || ' ' || amount FROM invoices.customer_credits WHERE bank_transaction_id = $1`, id); !slices.Equal(got, []string{"NOK 600.00"}) {
		t.Errorf("the credit = %v, want the line's NOK 600 rest kept in NOK", got)
	}
	if got := openNOK(t, h, inv.ID); got != "0.00" {
		t.Errorf("open in NOK = %s, want 0.00", got)
	}
}

// The worker on a pool of one (plan C2; memory: lease worker pool fix): the
// claim is one statement, the fetch holds no connection, and each currency's
// write is a short transaction — a cycle that held a connection while it
// reached for another would hang; the deadline turns that into a failure.
// Before 16:30 on a business day nothing of the day is due.
func TestRatesWorker_FetchesOnAPoolOfOne(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	euroOn(t, h, false)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	one, err := db.Open(ctx, h.Pool().Config().ConnString(), db.WithMaxConns(1))
	if err != nil {
		t.Fatalf("open a pool of one: %v", err)
	}
	defer one.Close()
	d := h.Deps()
	d.Pool = one
	w := invoices.NewRatesWorker(d)
	before := len(h.rates.Requests())
	if err := w.RunCycle(ctx); err != nil {
		t.Fatalf("RunCycle with nothing due: %v", err)
	}
	if n := len(h.rates.Requests()) - before; n != 0 {
		t.Errorf("a cycle right after the backfill asked Norges Bank %d times, want none", n)
	}
	h.Advance(72 * time.Hour)
	h.rates.Set("EUR", h.Now().Format(time.DateOnly), "11.8")
	if err := w.RunCycle(ctx); err != nil {
		t.Fatalf("RunCycle with EUR due on a pool of one: %v", err)
	}
	if n := len(h.rates.Requests()) - before; n != 1 {
		t.Errorf("the cycle asked Norges Bank %d times, want once", n)
	}
	if got := h.Count(t, `SELECT count(*) FROM invoices.rate_fetches WHERE lease_id IS NULL AND last_success_at IS NOT NULL`); got != 1 {
		t.Errorf("%d fetch rows released with a success, want 1", got)
	}
}
