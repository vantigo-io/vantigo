package invoices_test

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile/bankfiletest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The exception queue (invoices payments and reminders design D5): the
// lines matching did not match, read with what is applied from them, their
// suggestions and their events, and the seven actions on one — each judged
// on the pool and again under the line's lock, the apply and the reversal
// then locking their invoices in descending id. The files are bankfiletest's
// camt.054 notifications on matchHarness's installation.

const queuePath = invoicesPath + "/bank-transactions"

// queueActionPath is POST /bank-transactions/{id}/{action}.
func queueActionPath(id int64, action string) string {
	return fmt.Sprintf("%s/%d/%s", queuePath, id, action)
}

// appliedJSON is a payment or charge payment of a line as a client reads it.
type appliedJSON struct {
	Kind      string  `json:"kind"`
	ID        int64   `json:"id"`
	InvoiceID int64   `json:"invoiceId"`
	Number    *int64  `json:"number"`
	Amount    float64 `json:"amount"`
	Removed   bool    `json:"removed"`
}

// suggestionJSON is one suggestion.
type suggestionJSON struct {
	InvoiceID  int64   `json:"invoiceId"`
	Number     int64   `json:"number"`
	CustomerID int32   `json:"customerId"`
	BuyerName  string  `json:"buyerName"`
	OpenAmount float64 `json:"openAmount"`
	Why        string  `json:"why"`
}

// eventJSON is one of a line's events.
type eventJSON struct {
	ID     int64     `json:"id"`
	Event  string    `json:"event"`
	Reason *string   `json:"reason"`
	Note   string    `json:"note"`
	By     uuid.UUID `json:"by"`
	At     time.Time `json:"at"`
}

// queueLineJSON is a line as the queue answers it.
type queueLineJSON struct {
	ID            int64   `json:"id"`
	LineRef       string  `json:"lineRef"`
	Status        string  `json:"status"`
	Reason        *string `json:"reason"`
	Amount        float64 `json:"amount"`
	BookedOn      string  `json:"bookedOn"`
	Kid           *string `json:"kid"`
	DuplicateOfID *int64  `json:"duplicateOfId"`
	BankFile      struct {
		ID         int64     `json:"id"`
		Format     string    `json:"format"`
		UploadedAt time.Time `json:"uploadedAt"`
	} `json:"bankFile"`
	Applied             []appliedJSON     `json:"applied"`
	UnappliedAmount     float64           `json:"unappliedAmount"`
	Suggestions         *[]suggestionJSON `json:"suggestions"`
	SuggestedInvoiceID  *int64            `json:"suggestedInvoiceId"`
	PossibleDuplicateOf *struct {
		ID         int64         `json:"id"`
		BankFileID int64         `json:"bankFileId"`
		LineRef    string        `json:"lineRef"`
		BookedOn   string        `json:"bookedOn"`
		Amount     float64       `json:"amount"`
		Kid        *string       `json:"kid"`
		Status     string        `json:"status"`
		Applied    []appliedJSON `json:"applied"`
		Reversed   bool          `json:"reversed"`
	} `json:"possibleDuplicateOf"`
	Resolution     *string     `json:"resolution"`
	ResolvedBy     *uuid.UUID  `json:"resolvedBy"`
	ResolvedAt     *time.Time  `json:"resolvedAt"`
	ResolutionNote string      `json:"resolutionNote"`
	Events         []eventJSON `json:"events"`
}

// queueRefusalJSON is a queue action's 409.
type queueRefusalJSON struct {
	Code               string   `json:"code"`
	Detail             string   `json:"detail"`
	InvoiceID          *int64   `json:"invoiceId"`
	OpenAmount         *float64 `json:"openAmount"`
	ChargesOutstanding *float64 `json:"chargesOutstanding"`
}

// queueList reads GET /bank-transactions with query, answering the page and
// the total.
func queueList(t *testing.T, h *harness, query string) ([]queueLineJSON, int) {
	t.Helper()
	res := importer(t, h).Do(http.MethodGet, queuePath+"?"+query, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /bank-transactions?%s = %d %s, want 200", query, res.Status, res.Body)
	}
	var body struct {
		Data       []queueLineJSON `json:"data"`
		Pagination struct {
			TotalCount int `json:"totalCount"`
		} `json:"pagination"`
	}
	res.JSON(&body)
	return body.Data, body.Pagination.TotalCount
}

// lineIDsOf is the lines' ids, in order.
func lineIDsOf(lines []queueLineJSON) []int64 {
	out := make([]int64, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.ID)
	}
	return out
}

// queueLine is line id as the queue's list answers it.
func queueLine(t *testing.T, h *harness, id int64) queueLineJSON {
	t.Helper()
	file := modtest.One[int64](t, h.Harness, `SELECT bank_file_id FROM invoices.bank_transactions WHERE id = $1`, id)
	lines, _ := queueList(t, h, fmt.Sprintf("bankFileId=%d&pageSize=100", file))
	for _, l := range lines {
		if l.ID == id {
			return l
		}
	}
	t.Fatalf("line %d is not on its file's page", id)
	return queueLineJSON{}
}

// acted posts action on line id with body and answers the line, failing
// unless it is 200.
func acted(t *testing.T, c *modtest.Client, id int64, action string, body any) queueLineJSON {
	t.Helper()
	res := c.Do(http.MethodPost, queueActionPath(id, action), body)
	if res.Status != http.StatusOK {
		t.Fatalf("POST …/%d/%s %v = %d %s, want 200", id, action, body, res.Status, res.Body)
	}
	var l queueLineJSON
	res.JSON(&l)
	return l
}

// queueRefused asserts res is a 409 with code and answers it.
func queueRefused(t *testing.T, what string, res *modtest.Response, code string) queueRefusalJSON {
	t.Helper()
	var p queueRefusalJSON
	if res.Status != http.StatusConflict {
		t.Errorf("%s = %d %s, want 409 %s", what, res.Status, res.Body, code)
		return p
	}
	res.JSON(&p)
	if p.Code != code {
		t.Errorf("%s = %s (%s), want %s", what, p.Code, p.Detail, code)
	}
	return p
}

// invalidOn asserts res is a 400 with a message on field.
func invalidOn(t *testing.T, what string, res *modtest.Response, field string) {
	t.Helper()
	if res.Status != http.StatusBadRequest {
		t.Errorf("%s = %d %s, want 400 on %s", what, res.Status, res.Body, field)
		return
	}
	if p := problemOf(t, res); len(p.Errors[field]) == 0 {
		t.Errorf("%s = %+v, want a message on %s", what, p.Errors, field)
	}
}

// noKidEntry is a camt.054 entry booked on day of one transaction without a
// KID, of amount, with text, its AcctSvcrRef ref (which names its line).
func noKidEntry(day int, amount float64, text, ref string) bankfiletest.CamtEntry {
	return bankfiletest.CamtEntry{BookedOn: oct(day), Txs: []bankfiletest.CamtTx{{AmountMinor: minor(amount), Ustrd: text, AcctSvcrRef: ref}}}
}

// reversalEntry is a camt.054 reversal booked on day of amount, its
// AcctSvcrRef ref.
func reversalEntry(day int, amount float64, ref string) bankfiletest.CamtEntry {
	return bankfiletest.CamtEntry{BookedOn: oct(day), CreditDebit: "DBIT", Reversal: true,
		Txs: []bankfiletest.CamtTx{{AmountMinor: minor(amount), AcctSvcrRef: ref}}}
}

// kidEntry is a camt.054 entry booked on day of one transaction of amount
// with KID k and a debtor account, its AcctSvcrRef ref.
func kidEntry(day int, amount float64, k, debtorAccount, ref string) bankfiletest.CamtEntry {
	return bankfiletest.CamtEntry{BookedOn: oct(day), Txs: []bankfiletest.CamtTx{{AmountMinor: minor(amount), KID: k, DebtorAccount: debtorAccount, AcctSvcrRef: ref}}}
}

// camtLines imports a camt.054 notification msgID on sellerAccount of
// entries and answers the file and its lines' ids by their AcctSvcrRef.
func camtLines(t *testing.T, h *harness, c *modtest.Client, msgID string, entries ...bankfiletest.CamtEntry) (importJSON, map[string]int64) {
	t.Helper()
	r := imported(t, c, camtFile(msgID, sellerAccount, entries...))
	ids := map[string]int64{}
	for _, e := range entries {
		for _, tx := range e.Txs {
			ids[tx.AcctSvcrRef] = lineID(t, h, r.File.ID, tx.AcctSvcrRef)
		}
	}
	return r, ids
}

// paymentOf is the id of the one payment line id registered.
func paymentOf(t *testing.T, h *harness, line int64) int64 {
	t.Helper()
	return modtest.One[int64](t, h.Harness, `SELECT id FROM invoices.payments WHERE bank_transaction_id = $1`, line)
}

// allocate is one allocation of an apply's body.
func allocate(inv invoiceJSON, amount float64, charges ...float64) map[string]any {
	a := map[string]any{"invoiceId": inv.ID, "amount": amount}
	if len(charges) > 0 {
		a["chargesAmount"] = charges[0]
	}
	return a
}

// applyBody is an apply's body of allocations.
func applyBody(allocations ...map[string]any) map[string]any {
	return map[string]any{"allocations": allocations}
}

// queueWrites is everything a queue action may write — the payments and charge
// payments, the lines' states and their events — so a refusal can be shown
// to have written nothing.
func queueWrites(t *testing.T, h *harness) string {
	t.Helper()
	return strings.Join(texts(t, h, `
		SELECT 'p ' || count(*) || ' ' || count(*) FILTER (WHERE removed_at IS NOT NULL) FROM invoices.payments
		UNION ALL SELECT 'c ' || count(*) || ' ' || count(*) FILTER (WHERE removed_at IS NOT NULL) FROM invoices.charge_payments
		UNION ALL SELECT 'w ' || count(*) FROM invoices.charge_waivers
		UNION ALL SELECT 'e ' || count(*) FROM invoices.bank_transaction_events
		UNION ALL SELECT 's ' || string_agg(id || status || coalesce(reason, '') || coalesce(resolution, ''), ',' ORDER BY id) FROM invoices.bank_transactions`), "; ")
}

// TestBankQueue_List: every filter — status, reason, bankFileId, the account,
// the amount by value, the booking days from and to, unapplied — the open lines first, oldest booking day
// first, then the rest, or with order=newest every line newest first; paging after the filter; unapplied=true lists the
// matched and resolved lines with a rest (a matched line whose payment was
// removed, a line applied in part) and never a dismissed one; each line's
// file, what is applied from it and its rest; and the 400s.
func TestBankQueue_List(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	a, b := kidInvoice(t, h), kidInvoice(t, h)
	toMatchDay(h)
	c := importer(t, h)
	first, ids := camtLines(t, h, c, "LIST-1",
		kidEntry(5, 400, *a.Kid, "", "PAID"),
		noKidEntry(3, 7.77, "Takk for sist", "NOKID-3"),
		noKidEntry(4, 8.88, "Renter", "NOKID-4"),
		reversalEntry(7, 50, "REV-7"),
	)
	second, ids2 := camtLines(t, h, c, "LIST-2", kidEntry(6, 1200, *b.Kid, "", "OVER"))
	if got := stateOf(t, h, ids2["OVER"]); got != "exception exceeds_open" {
		t.Fatalf("the 1200 line = %s, want exceeds_open", got)
	}
	acted(t, c, ids["NOKID-4"], "dismiss", map[string]any{"note": "Renter fra banken"})
	acted(t, c, ids2["OVER"], "apply", applyBody(allocate(b, 1000)))
	if res := payer(t, h).Do(http.MethodPost, removalPath(a.ID, paymentOf(t, h, ids["PAID"])), map[string]any{"reason": "Feil"}); res.Status != http.StatusOK {
		t.Fatalf("the removal = %d %s", res.Status, res.Body)
	}

	all, total := queueList(t, h, "")
	want := []int64{ids["NOKID-3"], ids["REV-7"], ids["NOKID-4"], ids["PAID"], ids2["OVER"]}
	if got := lineIDsOf(all); !slices.Equal(got, want) || total != 5 {
		t.Errorf("every line = %v (total %d), want the open ones oldest first, then the rest oldest first: %v", got, total, want)
	}
	byID := map[int64]queueLineJSON{}
	for _, l := range all {
		byID[l.ID] = l
	}
	for _, w := range []struct {
		id        int64
		file      int64
		unapplied float64
		applied   int
	}{
		{ids["PAID"], first.File.ID, 400, 1}, // its payment removed: all of it is unapplied
		{ids2["OVER"], second.File.ID, 200, 1},
		{ids["NOKID-3"], first.File.ID, 7.77, 0},
		{ids["NOKID-4"], first.File.ID, 0, 0}, // dismissed: not money waiting
		{ids["REV-7"], first.File.ID, 0, 0},   // a reversal: never
	} {
		l := byID[w.id]
		if l.BankFile.ID != w.file || l.BankFile.Format != "camt054" || l.UnappliedAmount != w.unapplied || len(l.Applied) != w.applied {
			t.Errorf("line %s = file %+v, unapplied %v, applied %+v; want file %d, unapplied %v, %d applied",
				l.LineRef, l.BankFile, l.UnappliedAmount, l.Applied, w.file, w.unapplied, w.applied)
		}
	}
	if p := byID[ids["PAID"]].Applied; len(p) != 1 || p[0].Kind != "payment" || !p[0].Removed || p[0].InvoiceID != a.ID || p[0].Number == nil || *p[0].Number != *a.Number {
		t.Errorf("the paid line's applied = %+v, want its removed payment on invoice %d", p, a.ID)
	}

	for _, w := range []struct {
		query string
		want  []int64
	}{
		{"status=exception", []int64{ids["NOKID-3"], ids["REV-7"]}},
		{"status=resolved", []int64{ids["NOKID-4"], ids2["OVER"]}},
		{"reason=reversal", []int64{ids["REV-7"]}},
		{"reason=no_kid", []int64{ids["NOKID-3"], ids["NOKID-4"]}},
		{"bankFileId=" + strconv.FormatInt(second.File.ID, 10), []int64{ids2["OVER"]}},
		{"from=2026-10-03&to=2026-10-04", []int64{ids["NOKID-3"], ids["NOKID-4"]}},
		{"from=2026-10-05", []int64{ids["REV-7"], ids["PAID"], ids2["OVER"]}},
		{"to=2026-10-03", []int64{ids["NOKID-3"]}},
		{"unapplied=true", []int64{ids["PAID"], ids2["OVER"]}},
		{"unapplied=true&status=matched", []int64{ids["PAID"]}},
		{"unapplied=false&status=exception", []int64{ids["NOKID-3"], ids["REV-7"]}},
		// The account and the amount, compared by value — a reversal's candidates.
		{"amount=7.77", []int64{ids["NOKID-3"]}},
		{"amount=400.00", []int64{ids["PAID"]}},
		{"amount=50&status=exception", []int64{ids["REV-7"]}},
		{"account=" + sellerAccount + "&amount=1200&status=resolved", []int64{ids2["OVER"]}},
		{"account=" + sellerAccount, []int64{ids["NOKID-3"], ids["REV-7"], ids["NOKID-4"], ids["PAID"], ids2["OVER"]}},
		{"account=" + olderAccount, []int64{}},
		{"amount=50&status=matched", []int64{}},
		{"pageSize=2&page=2", []int64{ids["NOKID-4"], ids["PAID"]}},
		{"pageSize=2&page=3", []int64{ids2["OVER"]}},
		// order=newest: every line newest booking day first, the open ones not
		// put first — a reversal's latest candidates on the first page.
		{"order=newest", []int64{ids["REV-7"], ids2["OVER"], ids["PAID"], ids["NOKID-4"], ids["NOKID-3"]}},
		{"order=queue", []int64{ids["NOKID-3"], ids["REV-7"], ids["NOKID-4"], ids["PAID"], ids2["OVER"]}},
		{"order=newest&status=resolved&to=2026-10-06", []int64{ids2["OVER"], ids["NOKID-4"]}},
		{"pageSize=2&page=1&order=newest", []int64{ids["REV-7"], ids2["OVER"]}},
		{"pageSize=2&page=2&order=newest", []int64{ids["PAID"], ids["NOKID-4"]}},
	} {
		got, n := queueList(t, h, w.query)
		if !slices.Equal(lineIDsOf(got), w.want) {
			t.Errorf("?%s = %v, want %v", w.query, lineIDsOf(got), w.want)
		}
		if strings.HasPrefix(w.query, "pageSize") {
			if n != 5 {
				t.Errorf("?%s's total = %d, want 5 — paged after the filter", w.query, n)
			}
		} else if n != len(w.want) {
			t.Errorf("?%s's total = %d, want %d", w.query, n, len(w.want))
		}
	}
	for _, q := range []string{
		"status=open", "reason=lost", "from=2026-10-05&to=2026-10-04", "pageSize=101",
		"account=1234", "account=8601111794x", "amount=0", "amount=-50", "amount=7.777", "order=oldest",
	} {
		if res := importer(t, h).Do(http.MethodGet, queuePath+"?"+q, nil); res.Status != http.StatusBadRequest {
			t.Errorf("?%s = %d %s, want 400", q, res.Status, res.Body)
		}
	}
	if res := h.SignIn(t, "invoices:access").Do(http.MethodGet, queuePath, nil); res.Status != http.StatusForbidden {
		t.Errorf("the queue without invoices:payments = %d, want 403", res.Status)
	}
}

// TestBankQueue_Suggestions: an exception without an invoice is given the
// issued invoices it may pay, each with why — its number a whole word of the
// text, its open amount the line's amount, its customer's earlier payments
// from the same debtor account — each invoice once, under the first reason;
// one unambiguous suggestion is kept as the line's suggested invoice when it
// is queued, an ambiguous pair keeps none. A possible duplicate — the soft
// key's, and a duplicate row — shows the line it may repeat with that line's
// payments, and no suggestions when its KID named an invoice.
func TestBankQueue_Suggestions(t *testing.T) {
	t.Parallel()
	h, first := matchHarness(t)
	a, b := kidInvoice(t, h), kidInvoice(t, h)
	p := kidded(t, issued(t, h, createDraft(t, h, draftBody(customerPerson, line("Bok", 1, 100, vat25))).ID))
	toMatchDay(h)
	c := importer(t, h)
	const debtor = "15032080119"
	_, paidIDs := camtLines(t, h, c, "SUGG-1", kidEntry(5, 50, *p.Kid, debtor, "PERSON"))
	_, ids := camtLines(t, h, c, "SUGG-2",
		noKidEntry(6, 123.45, fmt.Sprintf("Faktura %d, takk", *a.Number), "NUMBER"),
		noKidEntry(6, 1000, "Takk", "AMOUNT"),
		bankfiletest.CamtEntry{BookedOn: oct(6), Txs: []bankfiletest.CamtTx{{AmountMinor: 3333, Ustrd: "Bok", DebtorAccount: debtor, AcctSvcrRef: "DEBTOR"}}},
		bankfiletest.CamtEntry{BookedOn: oct(4), Txs: []bankfiletest.CamtTx{{AmountMinor: 3334, Ustrd: "Bok", DebtorAccount: debtor, AcctSvcrRef: "DEBTOR-EARLY"}}},
		noKidEntry(6, 1000, fmt.Sprintf("Faktura nr. %d", *b.Number), "BOTH"),
		noKidEntry(6, 5.55, fmt.Sprintf("Ordre %d0", *a.Number), "NONE"),
	)
	sugg := func(inv invoiceJSON, open float64, why string) suggestionJSON {
		return suggestionJSON{InvoiceID: inv.ID, Number: *inv.Number, CustomerID: inv.CustomerID, OpenAmount: open, Why: why}
	}
	for _, w := range []struct {
		ref       string
		want      []suggestionJSON
		suggested string
	}{
		{"NUMBER", []suggestionJSON{sugg(a, 1000, "number_in_text")}, idKey(a.ID)},
		{"AMOUNT", []suggestionJSON{sugg(first, 1000, "amount_equals_open"), sugg(a, 1000, "amount_equals_open"), sugg(b, 1000, "amount_equals_open")}, "-"},
		{"DEBTOR", []suggestionJSON{sugg(p, 75, "debtor_account")}, idKey(p.ID)},
		// Booked before the debtor's earlier payment: that payment is no
		// earlier history of this line, so it suggests nothing.
		{"DEBTOR-EARLY", []suggestionJSON{}, "-"},
		{"BOTH", []suggestionJSON{sugg(b, 1000, "number_in_text"), sugg(first, 1000, "amount_equals_open"), sugg(a, 1000, "amount_equals_open")}, "-"},
		{"NONE", []suggestionJSON{}, "-"},
	} {
		l := queueLine(t, h, ids[w.ref])
		var got []suggestionJSON
		if l.Suggestions != nil {
			got = *l.Suggestions
		}
		for i := range got {
			if got[i].BuyerName == "" {
				t.Errorf("line %s's suggestion of %d names no buyer", w.ref, got[i].InvoiceID)
			}
			got[i].BuyerName = ""
		}
		if l.Suggestions == nil || !slices.Equal(got, w.want) {
			t.Errorf("line %s's suggestions = %+v, want %+v", w.ref, got, w.want)
		}
		if gotS := suggestedOf(t, h, ids[w.ref]); gotS != w.suggested {
			t.Errorf("line %s's suggested invoice = %s, want %s", w.ref, gotS, w.suggested)
		}
	}

	// The same payment from another notification: held back by the soft key
	// under its KID's invoice — the twin shown with its payment, no
	// suggestions — and the same file again under a new identity: a
	// duplicate row of it.
	_, dupIDs := camtLines(t, h, c, "SUGG-3", kidEntry(5, 50, *p.Kid, debtor, "PERSON-AGAIN"))
	twin := queueLine(t, h, dupIDs["PERSON-AGAIN"])
	if twin.Status != "exception" || twin.Reason == nil || *twin.Reason != "possible_duplicate" || twin.SuggestedInvoiceID == nil || *twin.SuggestedInvoiceID != p.ID {
		t.Fatalf("the second notification = %s %v suggested %v, want possible_duplicate suggesting %d", twin.Status, twin.Reason, twin.SuggestedInvoiceID, p.ID)
	}
	if twin.Suggestions != nil {
		t.Errorf("a possible duplicate whose KID named an invoice has suggestions %+v, want none", *twin.Suggestions)
	}
	if d := twin.PossibleDuplicateOf; d == nil || d.ID != paidIDs["PERSON"] || d.Amount != 50 || d.BookedOn != "2026-10-05" || d.Status != "matched" ||
		len(d.Applied) != 1 || d.Applied[0].InvoiceID != p.ID || d.Applied[0].Amount != 50 || d.Applied[0].Removed {
		t.Errorf("the possible duplicate's twin = %+v, want the matched line %d with its payment of 50 on %d", d, paidIDs["PERSON"], p.ID)
	}
	again := imported(t, c, camtFile("SUGG-1-COPY", sellerAccount, kidEntry(5, 50, *p.Kid, debtor, "PERSON")))
	dup := queueLine(t, h, modtest.One[int64](t, h.Harness, `SELECT id FROM invoices.bank_transactions WHERE bank_file_id = $1`, again.File.ID))
	if dup.Status != "duplicate" || dup.PossibleDuplicateOf == nil || dup.PossibleDuplicateOf.ID != paidIDs["PERSON"] || len(dup.PossibleDuplicateOf.Applied) != 1 {
		t.Errorf("the duplicate row = %s, twin %+v; want its twin the line it duplicates, with its payment", dup.Status, dup.PossibleDuplicateOf)
	}
	if dup.UnappliedAmount != 0 {
		t.Errorf("a duplicate row's unapplied amount = %v, want 0", dup.UnappliedAmount)
	}
	// The file's detail carries the twin too, but no suggestions.
	for _, l := range detailLines(t, h, again.File.ID) {
		if l.PossibleDuplicateOf == nil || l.Suggestions != nil || len(l.Events) != 0 {
			t.Errorf("the detail's duplicate row = twin %v, suggestions %v, events %v; want the twin alone", l.PossibleDuplicateOf, l.Suggestions, l.Events)
		}
	}
}

// detailLines is GET /bank-files/{id}'s lines with the queue's fields.
func detailLines(t *testing.T, h *harness, id int64) []queueLineJSON {
	t.Helper()
	res := importer(t, h).Do(http.MethodGet, fmt.Sprintf("%s/%d", bankFilesPath, id), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /bank-files/%d = %d %s", id, res.Status, res.Body)
	}
	var d struct {
		Transactions []queueLineJSON `json:"transactions"`
	}
	res.JSON(&d)
	return d.Transactions
}

// TestBankQueue_ApplyRefusalsInOrder: the apply's refusals, each request
// built to pass every guard before its own and trip that one — the body's
// 400s, the 404, a line that is no exception, a reversal and a negative line,
// then under the locks per invoice: not an issued invoice (a credit note, a
// draft, no document), more than the open amount (naming the invoice and
// its open amount), more than the charges outstanding, an invoice issued
// after the line was booked, and the allocations past what is left of the
// line. An earlier guard wins over a later one. Nothing is written by a
// refusal; the same line then applies.
func TestBankQueue_ApplyRefusalsInOrder(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	a, b, fee := kidInvoice(t, h), kidInvoice(t, h), kidInvoice(t, h)
	plantSent(t, h, fee.ID, sentFacts{1, "2026-09-28", "reminder_fee", "35", "", "0"})
	credited := kidInvoice(t, h)
	note := issued(t, h, creditDraft(t, h, credited.ID).ID)
	draft := createDraft(t, h, draftBody(customerAcme, line("Utkast", 1, 100, vat25)))
	toMatchDay(h)
	late := kidded(t, issued(t, h, createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 10, 80, vat25))).ID))
	c := importer(t, h)
	_, ids := camtLines(t, h, c, "APPLY-1",
		noKidEntry(6, 100, "Innbetaling", "LINE"),
		kidEntry(6, 10, *a.Kid, "", "MATCHED"),
		reversalEntry(6, 20, "REV"),
	)
	negative := imported(t, c, bankfiletest.OCR("1", bankfiletest.OCRPayment{Type: 10, Account: olderAccount, Settled: oct(6), Ordered: oct(6),
		AmountMinor: 3000, Negative: true, KID: *a.Kid, ArchiveRef: "77"},
		bankfiletest.OCRPayment{Type: 13, Account: olderAccount, Settled: oct(6), AmountMinor: 5000, ArchiveRef: "78"}))
	negativeLine := lineID(t, h, negative.File.ID, "77")
	line := ids["LINE"]
	before := queueWrites(t, h)

	long := strings.Repeat("x", 501)
	twentyOne := make([]map[string]any, 21)
	for i := range twentyOne {
		twentyOne[i] = map[string]any{"invoiceId": int64(5000 + i), "amount": 1}
	}
	for _, w := range []struct {
		what  string
		body  map[string]any
		field string
	}{
		{"no allocation", applyBody(), "allocations"},
		{"21 allocations", map[string]any{"allocations": twentyOne}, "allocations"},
		{"an invoice twice", applyBody(allocate(a, 10), allocate(a, 20)), "allocations[1].invoiceId"},
		{"a negative amount", applyBody(allocate(a, -1)), "allocations[0].amount"},
		{"three decimals", applyBody(allocate(a, 1.005)), "allocations[0].amount"},
		{"negative charges", applyBody(allocate(a, 1, -1)), "allocations[0].chargesAmount"},
		{"both 0", applyBody(allocate(a, 0, 0)), "allocations[0].amount"},
		{"a long note", map[string]any{"allocations": []map[string]any{allocate(a, 1)}, "note": long}, "note"},
	} {
		invalidOn(t, w.what, c.Do(http.MethodPost, queueActionPath(line, "apply"), w.body), w.field)
	}
	// The body before the line: a bad body on a missing line is still 400.
	invalidOn(t, "a bad body on no line", c.Do(http.MethodPost, queueActionPath(999999, "apply"), applyBody()), "allocations")
	if res := c.Do(http.MethodPost, queueActionPath(999999, "apply"), applyBody(allocate(a, 1))); res.Status != http.StatusNotFound {
		t.Errorf("no such line = %d %s, want 404", res.Status, res.Body)
	}
	for _, w := range []struct {
		what string
		id   int64
		code string
	}{
		{"a matched line", ids["MATCHED"], "bank_transaction_not_open"},
		{"a reversal", ids["REV"], "bank_transaction_not_applicable"},
		{"a negative line", negativeLine, "bank_transaction_not_applicable"},
	} {
		queueRefused(t, w.what, c.Do(http.MethodPost, queueActionPath(w.id, "apply"), applyBody(allocate(a, 1))), w.code)
	}
	for _, w := range []struct {
		what string
		body map[string]any
		code string
	}{
		{"a credit note", applyBody(allocate(note, 1)), "allocation_not_an_invoice"},
		{"a draft", applyBody(allocate(invoiceJSON{ID: draft.ID}, 1)), "allocation_not_an_invoice"},
		{"no document", applyBody(map[string]any{"invoiceId": 999999, "amount": 1}), "allocation_not_an_invoice"},
		{"a credit note before an excess", applyBody(allocate(a, 1000.01), allocate(note, 1)), "allocation_not_an_invoice"},
		{"more than is open", applyBody(allocate(b, 1000.01)), "payment_exceeds_open"},
		{"more than is open, issued too late", applyBody(allocate(late, 1000.01)), "payment_exceeds_open"},
		{"more than the charges", applyBody(allocate(fee, 0, 35.01)), "charge_payment_exceeds_outstanding"},
		{"charges where none is outstanding", applyBody(allocate(a, 0, 1)), "charge_payment_exceeds_outstanding"},
		{"issued after the booking", applyBody(allocate(late, 1)), "paid_before_issue"},
		{"more than the line", applyBody(allocate(a, 60), allocate(b, 40), allocate(fee, 0, 0.01)), "allocation_exceeds_transaction"},
	} {
		p := queueRefused(t, w.what, c.Do(http.MethodPost, queueActionPath(line, "apply"), w.body), w.code)
		switch w.code {
		case "payment_exceeds_open":
			if p.InvoiceID == nil || p.OpenAmount == nil || *p.OpenAmount != 1000 {
				t.Errorf("%s = invoiceId %v openAmount %v, want the invoice and 1000", w.what, p.InvoiceID, money(p.OpenAmount))
			}
		case "charge_payment_exceeds_outstanding":
			if p.ChargesOutstanding == nil {
				t.Errorf("%s carries no chargesOutstanding", w.what)
			}
		}
	}
	if p := queueRefused(t, "the excess on a", c.Do(http.MethodPost, queueActionPath(line, "apply"), applyBody(allocate(b, 1), allocate(a, 1000.01))), "payment_exceeds_open"); p.InvoiceID == nil || *p.InvoiceID != a.ID {
		t.Errorf("payment_exceeds_open names invoice %v, want %d", p.InvoiceID, a.ID)
	}
	if after := queueWrites(t, h); after != before {
		t.Errorf("the refusals wrote: %s, was %s", after, before)
	}
	l := acted(t, c, line, "apply", applyBody(allocate(a, 60), allocate(b, 40)))
	if l.Status != "resolved" || l.Resolution == nil || *l.Resolution != "applied" || l.UnappliedAmount != 0 {
		t.Errorf("the apply = %s %v rest %v, want resolved, applied, nothing left", l.Status, l.Resolution, l.UnappliedAmount)
	}
	queueRefused(t, "the line applied again", c.Do(http.MethodPost, queueActionPath(line, "apply"), applyBody(allocate(a, 1))), "bank_transaction_not_open")

	// What is left of a line is its amount less what its live payments
	// already apply — none can, through the queue, while it is an exception,
	// so one is planted: 50 of a line of 100 applied, 60 more is too much.
	_, part := camtLines(t, h, c, "APPLY-2", noKidEntry(6, 100, "Delvis", "PART"))
	h.Exec(t, `INSERT INTO invoices.payments (invoice_id, paid_on, amount, currency, source, bank_transaction_id, reference,
		    registered_by_user_id, registered_at)
		VALUES ($1, DATE '2026-10-06', 50, 'NOK', 'camt054', $2, '', gen_random_uuid(), now())`, b.ID, part["PART"])
	queueRefused(t, "more than is left", c.Do(http.MethodPost, queueActionPath(part["PART"], "apply"), applyBody(allocate(b, 50.01))), "allocation_exceeds_transaction")
	if l := acted(t, c, part["PART"], "apply", applyBody(allocate(b, 50))); l.UnappliedAmount != 0 || len(l.Applied) != 2 {
		t.Errorf("the rest applied = %v left, %d applied; want nothing left, two payments", l.UnappliedAmount, len(l.Applied))
	}
}

// TestBankQueue_ApplyChargesAlone: an allocation may pay the charges alone
// — amount 0, chargesAmount the fee left on a paid invoice — as a charge
// payment from the line by the caller; both 0 is a 400.
func TestBankQueue_ApplyChargesAlone(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	inv := kidInvoice(t, h)
	plantSent(t, h, inv.ID, sentFacts{1, "2026-09-28", "reminder_fee", "35", "", "0"})
	registered(t, h, inv.ID, pay(1000, "2026-09-12"))
	toMatchDay(h)
	c, user := h.SignInUser(t, "invoices:access", "invoices:payments")
	_, ids := camtLines(t, h, c, "FEE-1", noKidEntry(6, 35, "Purregebyr", "FEE"))
	invalidOn(t, "both 0", c.Do(http.MethodPost, queueActionPath(ids["FEE"], "apply"), applyBody(allocate(inv, 0, 0))), "allocations[0].amount")
	l := acted(t, c, ids["FEE"], "apply", applyBody(allocate(inv, 0, 35)))
	if len(l.Applied) != 1 || l.Applied[0].Kind != "charge_payment" || l.Applied[0].Amount != 35 || l.UnappliedAmount != 0 {
		t.Errorf("the line = applied %+v rest %v, want one charge payment of 35", l.Applied, l.UnappliedAmount)
	}
	if got := chargePaymentsFrom(t, h, ids["FEE"]); !slices.Equal(got, []string{paid(inv, "35.00", "camt054", "2026-10-06", "")}) {
		t.Errorf("the charge payments = %v, want 35.00 from the line, paid on its booking day", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.charge_payments WHERE bank_transaction_id = $1 AND registered_by_user_id = $2`, ids["FEE"], user); n != 1 {
		t.Errorf("%d charge payments registered by the caller, want 1", n)
	}
	if len(paymentsFrom(t, h, ids["FEE"])) != 0 {
		t.Error("a charges-only allocation registered a payment of the principal")
	}
	chargesAre(t, "the invoice", receivablesOf(t, h, inv.ID), 35, 0, 35, 0, nil)
}

// TestBankQueue_ApplyAcrossThreeInvoicesInDescendingId: an apply to three
// invoices named 3, 9, 7 locks the line, then 9, 7, 3 (D18), and registers
// each payment and charge payment by the caller, paid on the line's booking
// day; the line resolved, applied, by the caller; what the allocations leave
// stays its unappliedAmount.
func TestBankQueue_ApplyAcrossThreeInvoicesInDescendingId(t *testing.T) {
	h, _ := matchHarness(t)
	low, mid, high := kidInvoice(t, h), kidInvoice(t, h), kidInvoice(t, h)
	plantSent(t, h, mid.ID, sentFacts{1, "2026-09-28", "reminder_fee", "35", "", "0"})
	toMatchDay(h)
	c, user := h.SignInUser(t, "invoices:access", "invoices:payments")
	_, ids := camtLines(t, h, c, "THREE-1", noKidEntry(6, 2500, "Tre fakturaer", "THREE"))
	line := ids["THREE"]
	seen := &lockSeen{}
	restore := invoices.SetLockTaken(seen.note)
	l := acted(t, c, line, "apply", map[string]any{
		"allocations": []map[string]any{allocate(low, 1000), allocate(high, 500), allocate(mid, 900, 35)},
		"note":        "Tre fakturaer i én betaling",
	})
	restore()
	if got, want := seen.take(), []string{"bank_transaction " + idKey(line), "invoice " + idKey(high.ID), "invoice " + idKey(mid.ID), "invoice " + idKey(low.ID)}; !slices.Equal(got, want) {
		t.Errorf("the apply's locks = %v, want %v", got, want)
	}
	if got, want := paymentsFrom(t, h, line), []string{
		paid(high, "500.00", "camt054", "2026-10-06", ""), paid(mid, "900.00", "camt054", "2026-10-06", ""), paid(low, "1000.00", "camt054", "2026-10-06", ""),
	}; !slices.Equal(got, want) {
		t.Errorf("the payments = %v, want %v", got, want)
	}
	if got := chargePaymentsFrom(t, h, line); !slices.Equal(got, []string{paid(mid, "35.00", "camt054", "2026-10-06", "")}) {
		t.Errorf("the charge payments = %v, want 35.00 on the middle invoice", got)
	}
	if n := h.Count(t, `SELECT (SELECT count(*) FROM invoices.payments WHERE bank_transaction_id = $1 AND registered_by_user_id = $2 AND registered_at = $3)
		+ (SELECT count(*) FROM invoices.charge_payments WHERE bank_transaction_id = $1 AND registered_by_user_id = $2 AND registered_at = $3)`, line, user, h.Now()); n != 4 {
		t.Errorf("%d of the four rows registered by the caller at the request's clock read", n)
	}
	if l.Status != "resolved" || l.Resolution == nil || *l.Resolution != "applied" || l.ResolvedBy == nil || *l.ResolvedBy != user ||
		l.ResolutionNote != "Tre fakturaer i én betaling" || l.UnappliedAmount != 65 || len(l.Applied) != 4 {
		t.Errorf("the line = %s %v by %v note %q rest %v applied %d; want resolved, applied, by the caller, the note, 65 left, four rows",
			l.Status, l.Resolution, l.ResolvedBy, l.ResolutionNote, l.UnappliedAmount, len(l.Applied))
	}
	if got := getInvoice(t, h, low.ID); got.State != "paid" {
		t.Errorf("the low invoice = %s, want paid", got.State)
	}
	if got, _ := queueList(t, h, "unapplied=true"); !slices.Equal(lineIDsOf(got), []int64{line}) {
		t.Errorf("?unapplied=true = %v, want the line with its 65 left", lineIDsOf(got))
	}
}

// TestBankQueue_ApplyAPossibleDuplicate (m12): Task 8's genuine second
// payment of one day, amount and KID in another file with another reference
// — queued possible_duplicate, its money not registered — is applied from
// the queue as a distinct payment.
func TestBankQueue_ApplyAPossibleDuplicate(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	inv := kidInvoice(t, h)
	toMatchDay(h)
	c := importer(t, h)
	twice := imported(t, c, camtFile("TWICE-1", sellerAccount, camtPay(6, 100, *inv.Kid, ""), camtPay(6, 100, *inv.Kid, "")))
	if twice.Matched != 2 {
		t.Fatalf("two equal payments in one file = %+v, want both matched", twice)
	}
	_, ids := camtLines(t, h, c, "TWICE-2", kidEntry(6, 100, *inv.Kid, "", "OTHER"))
	other := ids["OTHER"]
	if got := stateOf(t, h, other); got != "exception possible_duplicate" {
		t.Fatalf("the third payment = %s, want possible_duplicate", got)
	}
	l := acted(t, c, other, "apply", map[string]any{"allocations": []map[string]any{allocate(inv, 100)}, "note": "Kunden betalte tre ganger"})
	if l.Status != "resolved" || l.Reason == nil || *l.Reason != "possible_duplicate" || *l.Resolution != "applied" {
		t.Errorf("the applied duplicate = %s %v %v, want resolved, applied, keeping its reason", l.Status, l.Reason, l.Resolution)
	}
	if got := paymentsFrom(t, h, other); !slices.Equal(got, []string{paid(inv, "100.00", "camt054", "2026-10-06", *inv.Kid)}) {
		t.Errorf("the payment = %v, want 100.00 with the KID as its reference", got)
	}
	if got := livePaymentsOf(t, h, inv.ID); len(got) != 3 {
		t.Errorf("the invoice's payments = %v, want three", got)
	}
}

// TestBankQueue_DismissRefusals: a dismissal needs a note of 1 to 500
// characters (400), a line (404), an exception (bank_transaction_not_open)
// that is not a reversal (bank_transaction_not_applicable); then it resolves
// the line not_customer_payment with the note, and a second is not_open.
func TestBankQueue_DismissRefusals(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	inv := kidInvoice(t, h)
	toMatchDay(h)
	c, user := h.SignInUser(t, "invoices:access", "invoices:payments")
	_, ids := camtLines(t, h, c, "DISMISS-1",
		bankfiletest.CamtEntry{BookedOn: oct(6), Txs: []bankfiletest.CamtTx{{AmountMinor: 20000, Ustrd: "Utb. 2000810 Vippsnr 117703", AcctSvcrRef: "VIPPS"}}},
		kidEntry(6, 10, *inv.Kid, "", "MATCHED"),
		reversalEntry(6, 20, "REV"),
	)
	before := queueWrites(t, h)
	invalidOn(t, "no note", c.Do(http.MethodPost, queueActionPath(ids["VIPPS"], "dismiss"), map[string]any{"note": "  "}), "note")
	invalidOn(t, "a long note", c.Do(http.MethodPost, queueActionPath(ids["VIPPS"], "dismiss"), map[string]any{"note": strings.Repeat("x", 501)}), "note")
	if res := c.Do(http.MethodPost, queueActionPath(999999, "dismiss"), map[string]any{"note": "x"}); res.Status != http.StatusNotFound {
		t.Errorf("no such line = %d, want 404", res.Status)
	}
	queueRefused(t, "a matched line", c.Do(http.MethodPost, queueActionPath(ids["MATCHED"], "dismiss"), map[string]any{"note": "x"}), "bank_transaction_not_open")
	queueRefused(t, "a reversal", c.Do(http.MethodPost, queueActionPath(ids["REV"], "dismiss"), map[string]any{"note": "x"}), "bank_transaction_not_applicable")
	if after := queueWrites(t, h); after != before {
		t.Errorf("the refusals wrote: %s, was %s", after, before)
	}
	l := acted(t, c, ids["VIPPS"], "dismiss", map[string]any{"note": " Utbetaling fra Vipps "})
	if l.Status != "resolved" || *l.Resolution != "not_customer_payment" || l.ResolutionNote != "Utbetaling fra Vipps" || l.ResolvedBy == nil || *l.ResolvedBy != user ||
		l.ResolvedAt == nil || !l.ResolvedAt.Equal(h.Now()) || l.Reason == nil || *l.Reason != "vipps_payout" {
		t.Errorf("the dismissed line = %+v, want resolved not_customer_payment, the note, by the caller now, keeping vipps_payout", l)
	}
	queueRefused(t, "dismissed again", c.Do(http.MethodPost, queueActionPath(ids["VIPPS"], "dismiss"), map[string]any{"note": "x"}), "bank_transaction_not_open")
}

// TestBankQueue_HandleReversal: a reversal removes the payments it takes
// back, each with the reason "Reversed by the bank: line {ref}"; or none,
// with noPayment and a note; bare — or noPayment without a note — it is
// reversal_payment_required; a payment that is not its invoice's is 404 and
// one already removed 409 payment_removed, nothing written; a line that is
// not a reversal is not_applicable; the 400s.
func TestBankQueue_HandleReversal(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	a, b := kidInvoice(t, h), kidInvoice(t, h)
	manual := registered(t, h, a.ID, pay(100, "2026-09-12"))
	toMatchDay(h)
	c, user := h.SignInUser(t, "invoices:access", "invoices:payments")
	_, ids := camtLines(t, h, c, "REVERSE-1",
		kidEntry(5, 400, *a.Kid, "", "PAY-A"),
		kidEntry(5, 300, *b.Kid, "", "PAY-B"),
		reversalEntry(6, 700, "REV-1"),
		reversalEntry(6, 50, "REV-2"),
		reversalEntry(6, 100, "REV-3"),
		noKidEntry(6, 9.99, "Takk", "NOKID"),
	)
	pa, pb := paymentOf(t, h, ids["PAY-A"]), paymentOf(t, h, ids["PAY-B"])
	manualID := manual.Payments[0].ID
	path := func(ref string) string { return queueActionPath(ids[ref], "handle-reversal") }

	before := queueWrites(t, h)
	invalidOn(t, "payments and noPayment", c.Do(http.MethodPost, path("REV-2"), map[string]any{
		"removePayments": []map[string]any{{"invoiceId": a.ID, "paymentId": pa}}, "noPayment": true, "note": "x"}), "noPayment")
	invalidOn(t, "a payment twice", c.Do(http.MethodPost, path("REV-2"), map[string]any{
		"removePayments": []map[string]any{{"invoiceId": a.ID, "paymentId": pa}, {"invoiceId": a.ID, "paymentId": pa}}}), "removePayments[1].paymentId")
	invalidOn(t, "a long note", c.Do(http.MethodPost, path("REV-2"), map[string]any{"noPayment": true, "note": strings.Repeat("x", 501)}), "note")
	queueRefused(t, "bare", c.Do(http.MethodPost, path("REV-2"), map[string]any{}), "reversal_payment_required")
	queueRefused(t, "noPayment without a note", c.Do(http.MethodPost, path("REV-2"), map[string]any{"noPayment": true}), "reversal_payment_required")
	queueRefused(t, "a line that is no reversal", c.Do(http.MethodPost, path("NOKID"), map[string]any{"noPayment": true, "note": "x"}), "bank_transaction_not_applicable")
	queueRefused(t, "a matched line", c.Do(http.MethodPost, path("PAY-A"), map[string]any{"noPayment": true, "note": "x"}), "bank_transaction_not_open")
	if res := c.Do(http.MethodPost, path("REV-3"), map[string]any{"removePayments": []map[string]any{{"invoiceId": b.ID, "paymentId": pa}}}); res.Status != http.StatusNotFound {
		t.Errorf("a payment that is not its invoice's = %d %s, want 404", res.Status, res.Body)
	}
	if after := queueWrites(t, h); after != before {
		t.Errorf("the refusals wrote: %s, was %s", after, before)
	}

	l := acted(t, c, ids["REV-1"], "handle-reversal", map[string]any{
		"removePayments": []map[string]any{{"invoiceId": a.ID, "paymentId": pa}, {"invoiceId": b.ID, "paymentId": pb}},
		"note":           "Banken returnerte begge",
	})
	if l.Status != "resolved" || *l.Resolution != "reversal_handled" || l.ResolutionNote != "Banken returnerte begge" {
		t.Errorf("the reversal = %s %v %q, want resolved, reversal_handled, the note", l.Status, l.Resolution, l.ResolutionNote)
	}
	reason := "Reversed by the bank: line " + l.LineRef
	if n := h.Count(t, `SELECT count(*) FROM invoices.payments WHERE id = ANY($1) AND removed_at = $2 AND removed_by_user_id = $3 AND removal_reason = $4`,
		[]int64{pa, pb}, h.Now(), user, reason); n != 2 {
		t.Errorf("%d of the two payments removed by the caller with %q", n, reason)
	}
	if got := livePaymentsOf(t, h, a.ID); !slices.Equal(got, []string{"100.00 manual"}) {
		t.Errorf("invoice a's payments = %v, want the manual one alone", got)
	}

	before = queueWrites(t, h)
	queueRefused(t, "a payment removed already", c.Do(http.MethodPost, path("REV-3"), map[string]any{
		"removePayments": []map[string]any{{"invoiceId": a.ID, "paymentId": manualID}, {"invoiceId": a.ID, "paymentId": pa}}}), "payment_removed")
	if after := queueWrites(t, h); after != before {
		t.Errorf("the refused reversal wrote: %s, was %s", after, before)
	}
	acted(t, c, ids["REV-3"], "handle-reversal", map[string]any{"removePayments": []map[string]any{{"invoiceId": a.ID, "paymentId": manualID}}})
	if got := livePaymentsOf(t, h, a.ID); len(got) != 0 {
		t.Errorf("invoice a's payments = %v, want none", got)
	}
	none := acted(t, c, ids["REV-2"], "handle-reversal", map[string]any{"noPayment": true, "note": "Gebyr tilbakeført, ingen faktura"})
	if none.Status != "resolved" || *none.Resolution != "reversal_handled" || none.ResolutionNote != "Gebyr tilbakeført, ingen faktura" {
		t.Errorf("the reversal with no payment = %+v, want resolved with its note", none)
	}
}

// TestBankQueue_ConfirmAndTreatAsDistinct: a duplicate row treated as
// distinct becomes an exception queued possible_duplicate, its KID's invoice
// suggested, its link kept and outside the fingerprint index — the same
// payment imported again is a duplicate of the original live line, not of
// it — and it can then be applied; a confirmed duplicate is resolved
// duplicate_confirmed with the reason possible_duplicate, which a reopen
// lands on; the refusals.
func TestBankQueue_ConfirmAndTreatAsDistinct(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	inv := kidInvoice(t, h)
	toMatchDay(h)
	c := importer(t, h)
	_, ids := camtLines(t, h, c, "DUP-1", kidEntry(6, 100, *inv.Kid, "", "X"), noKidEntry(6, 3.33, "Takk", "NOKID"))
	original := ids["X"]
	duplicateRow := func(msgID string) int64 {
		r := imported(t, c, camtFile(msgID, sellerAccount, kidEntry(6, 100, *inv.Kid, "", "X")))
		return modtest.One[int64](t, h.Harness, `SELECT id FROM invoices.bank_transactions WHERE bank_file_id = $1`, r.File.ID)
	}
	kept, confirmed := duplicateRow("DUP-2"), duplicateRow("DUP-3")

	queueRefused(t, "treat an exception", c.Do(http.MethodPost, queueActionPath(ids["NOKID"], "treat-as-distinct"), nil), "bank_transaction_not_applicable")
	queueRefused(t, "treat a matched line", c.Do(http.MethodPost, queueActionPath(original, "treat-as-distinct"), nil), "bank_transaction_not_open")
	queueRefused(t, "confirm an exception of another reason", c.Do(http.MethodPost, queueActionPath(ids["NOKID"], "confirm-duplicate"), map[string]any{}), "bank_transaction_not_applicable")
	queueRefused(t, "confirm a matched line", c.Do(http.MethodPost, queueActionPath(original, "confirm-duplicate"), map[string]any{}), "bank_transaction_not_open")
	invalidOn(t, "a long note", c.Do(http.MethodPost, queueActionPath(confirmed, "confirm-duplicate"), map[string]any{"note": strings.Repeat("x", 501)}), "note")
	if res := c.Do(http.MethodPost, queueActionPath(999999, "treat-as-distinct"), nil); res.Status != http.StatusNotFound {
		t.Errorf("no such line = %d, want 404", res.Status)
	}

	l := acted(t, c, kept, "treat-as-distinct", nil)
	if l.Status != "exception" || l.Reason == nil || *l.Reason != "possible_duplicate" || l.DuplicateOfID == nil || *l.DuplicateOfID != original ||
		l.SuggestedInvoiceID == nil || *l.SuggestedInvoiceID != inv.ID {
		t.Errorf("the kept row = %s %v dup of %v suggested %v, want an exception, possible_duplicate, still linked to %d, suggesting %d",
			l.Status, l.Reason, l.DuplicateOfID, l.SuggestedInvoiceID, original, inv.ID)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.bank_transactions t
		WHERE (t.account, t.fingerprint) = (SELECT account, fingerprint FROM invoices.bank_transactions WHERE id = $1) AND t.duplicate_of_id IS NULL`, original); n != 1 {
		t.Errorf("%d live lines of the fingerprint, want the original alone", n)
	}
	if again := duplicateRow("DUP-4"); modtest.One[int64](t, h.Harness, `SELECT duplicate_of_id FROM invoices.bank_transactions WHERE id = $1`, again) != original {
		t.Error("the payment imported again is not a duplicate of the original line")
	}
	queueRefused(t, "treat it again", c.Do(http.MethodPost, queueActionPath(kept, "treat-as-distinct"), nil), "bank_transaction_not_applicable")
	if got, live := paymentsFrom(t, h, kept), livePaymentsOf(t, h, inv.ID); len(got) != 0 || len(live) != 1 {
		t.Errorf("after treat-as-distinct the row's payments = %v and the invoice's %v, want none from it before the apply", got, live)
	}
	acted(t, c, kept, "apply", applyBody(allocate(inv, 100)))
	if got := livePaymentsOf(t, h, inv.ID); len(got) != 2 {
		t.Errorf("the invoice's payments = %v, want the original's and the kept row's", got)
	}

	livesBefore := livePaymentsOf(t, h, inv.ID)
	conf := acted(t, c, confirmed, "confirm-duplicate", map[string]any{"note": "Samme betaling"})
	if got, live := paymentsFrom(t, h, confirmed), livePaymentsOf(t, h, inv.ID); len(got) != 0 || !slices.Equal(live, livesBefore) {
		t.Errorf("after confirm-duplicate the row's payments = %v and the invoice's %v, want none from it and %v unchanged", got, live, livesBefore)
	}
	if conf.Status != "resolved" || *conf.Resolution != "duplicate_confirmed" || conf.Reason == nil || *conf.Reason != "possible_duplicate" || conf.ResolutionNote != "Samme betaling" {
		t.Errorf("the confirmed row = %s %v %v %q, want resolved, duplicate_confirmed, possible_duplicate, the note", conf.Status, conf.Resolution, conf.Reason, conf.ResolutionNote)
	}
	queueRefused(t, "confirm it again", c.Do(http.MethodPost, queueActionPath(confirmed, "confirm-duplicate"), map[string]any{}), "bank_transaction_not_open")
	re := acted(t, c, confirmed, "reopen", nil)
	if re.Status != "exception" || re.Reason == nil || *re.Reason != "possible_duplicate" || re.DuplicateOfID == nil {
		t.Errorf("the confirmed row reopened = %s %v, want an exception on possible_duplicate, still linked", re.Status, re.Reason)
	}
	again := acted(t, c, confirmed, "confirm-duplicate", map[string]any{})
	if again.Status != "resolved" || *again.Resolution != "duplicate_confirmed" {
		t.Errorf("a possible duplicate confirmed = %s %v, want resolved duplicate_confirmed", again.Status, again.Resolution)
	}
}

// TestBankQueue_Reopen: a resolved line goes back to the queue with its
// reason, its resolution cleared on the line and kept in its events; a
// matched line whose payment was removed goes back as payment_removed with
// its KID's invoice suggested; while a live payment or charge payment refers
// to a line it is refused bank_transaction_applied; a line in the queue is
// not_applicable.
func TestBankQueue_Reopen(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	a, fee := kidInvoice(t, h), kidInvoice(t, h)
	plantSent(t, h, fee.ID, sentFacts{1, "2026-09-28", "reminder_fee", "35", "", "0"})
	registered(t, h, fee.ID, pay(1000, "2026-09-12"))
	toMatchDay(h)
	c := importer(t, h)
	_, ids := camtLines(t, h, c, "REOPEN-1",
		noKidEntry(6, 4.44, "Takk", "NOKID"),
		kidEntry(6, 400, *a.Kid, "", "PAID"),
		noKidEntry(6, 35, "Gebyr", "FEE"),
	)
	if res := c.Do(http.MethodPost, queueActionPath(999999, "reopen"), nil); res.Status != http.StatusNotFound {
		t.Errorf("no such line = %d, want 404", res.Status)
	}
	queueRefused(t, "an exception", c.Do(http.MethodPost, queueActionPath(ids["NOKID"], "reopen"), nil), "bank_transaction_not_applicable")

	acted(t, c, ids["NOKID"], "dismiss", map[string]any{"note": "Ikke vår"})
	l := acted(t, c, ids["NOKID"], "reopen", nil)
	if l.Status != "exception" || l.Reason == nil || *l.Reason != "no_kid" || l.Resolution != nil || l.ResolvedBy != nil || l.ResolvedAt != nil || l.ResolutionNote != "" {
		t.Errorf("the dismissed line reopened = %+v, want an exception, no_kid, its resolution cleared", l)
	}
	if n := len(l.Events); n != 3 || l.Events[1].Event != "dismissed" || l.Events[1].Note != "Ikke vår" || l.Events[2].Event != "reopened" ||
		l.Events[2].Reason == nil || *l.Events[2].Reason != "no_kid" {
		t.Errorf("its events = %+v, want queued, dismissed with the note, reopened", l.Events)
	}

	acted(t, c, ids["FEE"], "apply", applyBody(allocate(fee, 0, 35)))
	before := queueWrites(t, h)
	queueRefused(t, "a matched line with its payment", c.Do(http.MethodPost, queueActionPath(ids["PAID"], "reopen"), nil), "bank_transaction_applied")
	queueRefused(t, "an applied line with its charge payment", c.Do(http.MethodPost, queueActionPath(ids["FEE"], "reopen"), nil), "bank_transaction_applied")
	if after := queueWrites(t, h); after != before {
		t.Errorf("the refusals wrote: %s, was %s", after, before)
	}

	if res := payer(t, h).Do(http.MethodPost, removalPath(a.ID, paymentOf(t, h, ids["PAID"])), map[string]any{"reason": "Feil faktura"}); res.Status != http.StatusOK {
		t.Fatalf("the payment's removal = %d %s", res.Status, res.Body)
	}
	if got := stateOf(t, h, ids["PAID"]); got != "matched -" {
		t.Errorf("the line after its payment's removal = %s, want matched still — a removal never writes the line", got)
	}
	m := acted(t, c, ids["PAID"], "reopen", nil)
	if m.Status != "exception" || m.Reason == nil || *m.Reason != "payment_removed" || m.SuggestedInvoiceID == nil || *m.SuggestedInvoiceID != a.ID || m.UnappliedAmount != 400 {
		t.Errorf("the matched line reopened = %s %v suggested %v rest %v, want payment_removed suggesting %d, 400 left",
			m.Status, m.Reason, m.SuggestedInvoiceID, m.UnappliedAmount, a.ID)
	}
	acted(t, c, ids["PAID"], "apply", applyBody(allocate(a, 400)))

	cp := modtest.One[int64](t, h.Harness, `SELECT id FROM invoices.charge_payments WHERE bank_transaction_id = $1`, ids["FEE"])
	if res := payer(t, h).Do(http.MethodPost, chargePaymentRemovalPath(fee.ID, cp), map[string]any{"reason": "Feil"}); res.Status != http.StatusOK {
		t.Fatalf("the charge payment's removal = %d %s", res.Status, res.Body)
	}
	f := acted(t, c, ids["FEE"], "reopen", nil)
	if f.Status != "exception" || f.Reason == nil || *f.Reason != "no_kid" {
		t.Errorf("the applied line reopened = %s %v, want an exception on its own reason", f.Status, f.Reason)
	}
}

// TestBankQueue_EveryActionWritesItsEvent: every action writes the line's
// event — what, the reason, the note, by the caller at the request's clock
// read — after the queued event matching wrote.
func TestBankQueue_EveryActionWritesItsEvent(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	inv := kidInvoice(t, h)
	toMatchDay(h)
	c, user := h.SignInUser(t, "invoices:access", "invoices:payments")
	_, ids := camtLines(t, h, c, "EVENTS-1",
		noKidEntry(6, 1.11, "Takk", "APPLY"),
		noKidEntry(6, 2.22, "Takk", "DISMISS"),
		reversalEntry(6, 3.33, "REV"),
		kidEntry(6, 100, *inv.Kid, "", "X"),
	)
	dupFile := imported(t, c, camtFile("EVENTS-2", sellerAccount, kidEntry(6, 100, *inv.Kid, "", "X")))
	dupRow := modtest.One[int64](t, h.Harness, `SELECT id FROM invoices.bank_transactions WHERE bank_file_id = $1`, dupFile.File.ID)
	dupFile2 := imported(t, c, camtFile("EVENTS-3", sellerAccount, kidEntry(6, 100, *inv.Kid, "", "X")))
	dupRow2 := modtest.One[int64](t, h.Harness, `SELECT id FROM invoices.bank_transactions WHERE bank_file_id = $1`, dupFile2.File.ID)

	h.Advance(time.Minute)
	acted(t, c, ids["APPLY"], "apply", map[string]any{"allocations": []map[string]any{allocate(inv, 1.11)}, "note": "Delbetaling"})
	acted(t, c, ids["DISMISS"], "dismiss", map[string]any{"note": "Renter"})
	acted(t, c, ids["REV"], "handle-reversal", map[string]any{"noPayment": true, "note": "Gebyr"})
	acted(t, c, dupRow, "confirm-duplicate", map[string]any{"note": "Samme"})
	acted(t, c, dupRow2, "treat-as-distinct", nil)
	acted(t, c, ids["DISMISS"], "reopen", nil)
	at := h.Now()
	for _, w := range []struct {
		id   int64
		want []string
	}{
		{ids["APPLY"], []string{"queued no_kid ", "applied no_kid Delbetaling"}},
		{ids["DISMISS"], []string{"queued no_kid ", "dismissed no_kid Renter", "reopened no_kid "}},
		{ids["REV"], []string{"queued reversal ", "reversal_handled reversal Gebyr"}},
		{dupRow, []string{"duplicate_confirmed possible_duplicate Samme"}},
		{dupRow2, []string{"treated_as_distinct possible_duplicate "}},
	} {
		l := queueLine(t, h, w.id)
		var got []string
		for i, e := range l.Events {
			got = append(got, fmt.Sprintf("%s %s %s", e.Event, deref(e.Reason), e.Note))
			if i > 0 || e.Event != "queued" {
				if e.By != user || !e.At.Equal(at) {
					t.Errorf("line %s's %s event by %s at %s, want the caller %s at %s", l.LineRef, e.Event, e.By, e.At, user, at)
				}
			}
		}
		if !slices.Equal(got, w.want) {
			t.Errorf("line %s's events = %q, want %q", l.LineRef, got, w.want)
		}
	}
}

// TestBankQueue_LockOrder: each action's locks (D18) — the apply and the
// reversal the line, then their invoices in descending id; dismiss,
// confirm-duplicate, treat-as-distinct and reopen the line alone; a refusal
// judged on the pool none.
func TestBankQueue_LockOrder(t *testing.T) {
	h, _ := matchHarness(t)
	a, b := kidInvoice(t, h), kidInvoice(t, h)
	toMatchDay(h)
	c := importer(t, h)
	_, ids := camtLines(t, h, c, "LOCKS-1",
		noKidEntry(6, 20, "Takk", "APPLY"),
		noKidEntry(6, 2.22, "Takk", "DISMISS"),
		kidEntry(5, 100, *a.Kid, "", "PAY-A"),
		kidEntry(5, 100, *b.Kid, "", "PAY-B"),
		reversalEntry(6, 200, "REV"),
		noKidEntry(6, 9.99, "Takk", "SPARE"),
	)
	// Duplicates of a line no reversal touches: a duplicate of a reversed
	// line is never treated as distinct.
	r2 := imported(t, c, camtFile("LOCKS-2", sellerAccount, noKidEntry(6, 9.99, "Takk", "SPARE")))
	dup := modtest.One[int64](t, h.Harness, `SELECT id FROM invoices.bank_transactions WHERE bank_file_id = $1`, r2.File.ID)
	r3 := imported(t, c, camtFile("LOCKS-3", sellerAccount, noKidEntry(6, 9.99, "Takk", "SPARE")))
	dup2 := modtest.One[int64](t, h.Harness, `SELECT id FROM invoices.bank_transactions WHERE bank_file_id = $1`, r3.File.ID)
	pa, pb := paymentOf(t, h, ids["PAY-A"]), paymentOf(t, h, ids["PAY-B"])

	seen := &lockSeen{}
	restore := invoices.SetLockTaken(seen.note)
	defer restore()
	bt := func(id int64) string { return "bank_transaction " + idKey(id) }
	for _, w := range []struct {
		what   string
		id     int64
		action string
		body   any
		want   []string
	}{
		{"an apply", ids["APPLY"], "apply", applyBody(allocate(a, 10), allocate(b, 10)), []string{bt(ids["APPLY"]), "invoice " + idKey(b.ID), "invoice " + idKey(a.ID)}},
		{"a reversal", ids["REV"], "handle-reversal", map[string]any{"removePayments": []map[string]any{{"invoiceId": a.ID, "paymentId": pa}, {"invoiceId": b.ID, "paymentId": pb}}},
			[]string{bt(ids["REV"]), "invoice " + idKey(b.ID), "invoice " + idKey(a.ID)}},
		{"a dismissal", ids["DISMISS"], "dismiss", map[string]any{"note": "x"}, []string{bt(ids["DISMISS"])}},
		{"a reopen", ids["DISMISS"], "reopen", nil, []string{bt(ids["DISMISS"])}},
		{"a confirmation", dup, "confirm-duplicate", map[string]any{}, []string{bt(dup)}},
		{"a duplicate kept", dup2, "treat-as-distinct", nil, []string{bt(dup2)}},
	} {
		seen.take()
		acted(t, c, w.id, w.action, w.body)
		if got := seen.take(); !slices.Equal(got, w.want) {
			t.Errorf("%s's locks = %v, want %v", w.what, got, w.want)
		}
	}
	queueRefused(t, "an apply refused on the pool", c.Do(http.MethodPost, queueActionPath(ids["APPLY"], "apply"), applyBody(allocate(a, 1))), "bank_transaction_not_open")
	if got := seen.take(); len(got) != 0 {
		t.Errorf("a refusal judged on the pool took %v, want no lock", got)
	}
}

// TestBankQueue_NoCallUnderALock: the queue calls nothing out of the module
// — no directory, no object store — so the harness's recorder sees nothing
// for its caller.
func TestBankQueue_NoCallUnderALock(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	inv := kidInvoice(t, h)
	toMatchDay(h)
	_, ids := camtLines(t, h, importer(t, h), "CALLS-1", noKidEntry(6, 20, "Takk", "APPLY"), noKidEntry(6, 2.22, "Renter", "DISMISS"))
	c, user := h.SignInUser(t, "invoices:access", "invoices:payments")
	queueList(t, h, "")
	acted(t, c, ids["APPLY"], "apply", applyBody(allocate(inv, 20)))
	acted(t, c, ids["DISMISS"], "dismiss", map[string]any{"note": "Renter"})
	acted(t, c, ids["DISMISS"], "reopen", nil)
	if calls := contractCalls.by(user); len(calls) != 0 {
		t.Errorf("the queue called %v, want nothing out of the module", calls)
	}
}

// TestBankQueue_ApplyRunsTheDeadlineMetWaiver: an apply registers what a
// match does, the deadline-met waiver included (D4's last paragraph, D9).
// Five invoices, each with letter 1 (its deadline 29 September) and letter 2
// (a fee of 35, sent 1 October); on the fifth, letter 1 claimed the
// compensation. A no-KID line ordered (OCR) or booked (camt.054) within
// letter 1's deadline, applied from the queue, has letter 2's fee waived
// deadline_met by the caller at the request's time — the compensation never;
// a line ordered or booked after the deadline waives nothing.
func TestBankQueue_ApplyRunsTheDeadlineMetWaiver(t *testing.T) {
	t.Parallel()
	h, onTime := matchHarness(t)
	lateCamt, ocr, lateOCR, comp := kidInvoice(t, h), kidInvoice(t, h), kidInvoice(t, h), kidInvoice(t, h)
	letters := map[int64]int64{}
	for _, inv := range []invoiceJSON{onTime, lateCamt, ocr, lateOCR} {
		plantSent(t, h, inv.ID, sentFacts{1, "2026-09-15", "none", "", "", "0"}) // deadline 2026-09-29
		letters[inv.ID] = plantSent(t, h, inv.ID, sentFacts{2, "2026-10-01", "reminder_fee", "35", "", "0"})
	}
	plantSent(t, h, comp.ID, sentFacts{1, "2026-09-15", "compensation", "", "360", "0"})
	letters[comp.ID] = plantSent(t, h, comp.ID, sentFacts{2, "2026-10-01", "reminder_fee", "35", "", "0"})
	toMatchDay(h)
	c, user := h.SignInUser(t, "invoices:access", "invoices:payments")
	sep := func(d int) time.Time { return time.Date(2026, 9, d, 0, 0, 0, 0, time.UTC) }
	o := imported(t, c, bankfiletest.OCR("1",
		bankfiletest.OCRPayment{Type: 13, Account: sellerAccount, Settled: oct(2), Ordered: sep(28), AmountMinor: 100000, ArchiveRef: "1"},
		bankfiletest.OCRPayment{Type: 13, Account: sellerAccount, Settled: oct(2), Ordered: sep(30), AmountMinor: 100000, ArchiveRef: "2"}))
	entry := func(day int, ref string) bankfiletest.CamtEntry {
		return bankfiletest.CamtEntry{BookedOn: sep(day), Txs: []bankfiletest.CamtTx{{AmountMinor: 100000, Ustrd: "Betaling", AcctSvcrRef: ref}}}
	}
	k := imported(t, c, camtFile("WAIVE-1", olderAccount, entry(28, "C1"), entry(30, "C2"), entry(28, "C3")))
	for _, a := range []struct {
		file int64
		ref  string
		inv  invoiceJSON
	}{
		{o.File.ID, "1", ocr}, {o.File.ID, "2", lateOCR}, {k.File.ID, "C1", onTime}, {k.File.ID, "C2", lateCamt}, {k.File.ID, "C3", comp},
	} {
		id := lineID(t, h, a.file, a.ref)
		if got := stateOf(t, h, id); got != "exception no_kid" {
			t.Fatalf("line %s = %s, want no_kid", a.ref, got)
		}
		acted(t, c, id, "apply", applyBody(allocate(a.inv, 1000)))
	}
	for _, w := range []struct {
		what   string
		inv    invoiceJSON
		waived bool
	}{
		{"the OCR line ordered within the deadline", ocr, true},
		{"the camt.054 line booked within the deadline", onTime, true},
		{"the OCR line ordered after the deadline", lateOCR, false},
		{"the camt.054 line booked after the deadline", lateCamt, false},
		{"the line within the deadline of a compensation letter", comp, true},
	} {
		got := texts(t, h, `SELECT concat_ws(' ', reminder_id, kind, amount, reason, waived_by_user_id, (waived_at = $2)::text)
			FROM invoices.charge_waivers WHERE invoice_id = $1`, w.inv.ID, h.Now())
		var want []string
		if w.waived {
			want = []string{fmt.Sprintf("%d fee 35.00 deadline_met %s true", letters[w.inv.ID], user)}
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: waivers %v, want %v", w.what, got, want)
		}
	}
	chargesAre(t, "the compensation invoice", receivablesOf(t, h, comp.ID), 395, 35, 0, 360, nil)
	chargesAre(t, "the late OCR invoice", receivablesOf(t, h, lateOCR.ID), 35, 0, 0, 35, nil)
}

// TestBankQueue_ReversedMoneyIsNeverAppliedAgain (the coordinator's decision
// at Task 9's review): a reversal that removes a payment marks the line it
// came from reversed — an event by the caller, its note naming the reversal
// — and that line, matched or applied from the queue, then has no unapplied
// rest, is out of unapplied=true, and is refused reopen
// bank_transaction_reversed. A refund recorded by an ordinary removal is
// unchanged: its line shows its rest and reopens as payment_removed.
func TestBankQueue_ReversedMoneyIsNeverAppliedAgain(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	a, b, refunded := kidInvoice(t, h), kidInvoice(t, h), kidInvoice(t, h)
	toMatchDay(h)
	c, user := h.SignInUser(t, "invoices:access", "invoices:payments")
	_, ids := camtLines(t, h, c, "REVERSED-1",
		kidEntry(4, 400, *a.Kid, "", "MATCHED"),
		noKidEntry(4, 250, "Innbetaling", "APPLIED"),
		kidEntry(4, 300, *refunded.Kid, "", "REFUNDED"),
		reversalEntry(6, 650, "REV"),
	)
	acted(t, c, ids["APPLIED"], "apply", applyBody(allocate(b, 250)))
	rev := acted(t, c, ids["REV"], "handle-reversal", map[string]any{"removePayments": []map[string]any{
		{"invoiceId": a.ID, "paymentId": paymentOf(t, h, ids["MATCHED"])},
		{"invoiceId": b.ID, "paymentId": paymentOf(t, h, ids["APPLIED"])},
	}})
	if res := payer(t, h).Do(http.MethodPost, removalPath(refunded.ID, paymentOf(t, h, ids["REFUNDED"])), map[string]any{"reason": "Tilbakebetalt kunden"}); res.Status != http.StatusOK {
		t.Fatalf("the refund's removal = %d %s", res.Status, res.Body)
	}

	for _, ref := range []string{"MATCHED", "APPLIED"} {
		l := queueLine(t, h, ids[ref])
		last := l.Events[len(l.Events)-1]
		if last.Event != "reversed" || last.Note != "Reversed by the bank: line "+rev.LineRef || last.By != user || !last.At.Equal(h.Now()) {
			t.Errorf("line %s's last event = %+v, want reversed by the caller naming the reversal", ref, last)
		}
		if l.UnappliedAmount != 0 {
			t.Errorf("line %s's unapplied amount = %v, want 0 — the money went back", ref, l.UnappliedAmount)
		}
		queueRefused(t, "reopen line "+ref, c.Do(http.MethodPost, queueActionPath(ids[ref], "reopen"), nil), "bank_transaction_reversed")
	}
	if got, _ := queueList(t, h, "unapplied=true"); !slices.Equal(lineIDsOf(got), []int64{ids["REFUNDED"]}) {
		t.Errorf("?unapplied=true = %v, want the refunded line alone", lineIDsOf(got))
	}
	if l := queueLine(t, h, ids["REFUNDED"]); l.UnappliedAmount != 300 {
		t.Errorf("the refunded line's unapplied amount = %v, want 300", l.UnappliedAmount)
	}
	if l := acted(t, c, ids["REFUNDED"], "reopen", nil); l.Reason == nil || *l.Reason != "payment_removed" {
		t.Errorf("the refunded line reopened = %v, want payment_removed", l.Reason)
	}
}

// TestBankQueue_DismissedMoneyOwedBack (the coordinator's decision at Task
// 9's review): a line dismissed after it was queued invoice_credited,
// invoice_settled or exceeds_open is money owed back to the payer — a
// refund made outside Vantigo — so its whole amount stays unapplied and it
// shows under unapplied=true; any other dismissal leaves nothing unapplied.
func TestBankQueue_DismissedMoneyOwedBack(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	credited, settled, over := kidInvoice(t, h), kidInvoice(t, h), kidInvoice(t, h)
	issued(t, h, creditDraft(t, h, credited.ID).ID)
	registered(t, h, settled.ID, pay(1000, "2026-09-12"))
	toMatchDay(h)
	c := importer(t, h)
	_, ids := camtLines(t, h, c, "OWED-1",
		kidEntry(6, 100, *credited.Kid, "", "CREDITED"),
		kidEntry(6, 200, *settled.Kid, "", "SETTLED"),
		kidEntry(6, 1300, *over.Kid, "", "OVER"),
		noKidEntry(6, 400, "Renter", "NOKID"),
	)
	want := map[string]struct {
		state     string
		unapplied float64
	}{
		"CREDITED": {"exception invoice_credited", 100},
		"SETTLED":  {"exception invoice_settled", 200},
		"OVER":     {"exception exceeds_open", 1300},
		"NOKID":    {"exception no_kid", 0},
	}
	for ref, w := range want {
		if got := stateOf(t, h, ids[ref]); got != w.state {
			t.Fatalf("line %s = %s, want %s", ref, got, w.state)
		}
		l := acted(t, c, ids[ref], "dismiss", map[string]any{"note": "Tilbakebetales utenfor Vantigo"})
		if l.UnappliedAmount != w.unapplied {
			t.Errorf("line %s dismissed: unapplied %v, want %v", ref, l.UnappliedAmount, w.unapplied)
		}
	}
	owed := []int64{ids["CREDITED"], ids["SETTLED"], ids["OVER"]}
	slices.Sort(owed) // one booking day: by id, which follows the fingerprint
	if got, n := queueList(t, h, "unapplied=true"); !slices.Equal(lineIDsOf(got), owed) || n != 3 {
		t.Errorf("?unapplied=true = %v (%d), want the three lines owed back", lineIDsOf(got), n)
	}
}

// TestMatch_SoftKeySeesAReversedLine (the confirmation pass of Task 9's
// review): a line a reversal took the payment back from still holds the
// soft key, so another notification of the same payment — another MsgId and
// AcctSvcrRef, so not a duplicate row — is queued possible_duplicate, never
// matched: reversed money is not registered again by itself. Its twin shows
// reversed, so a person sees why the twin's payment is gone; a genuine second
// payment is possible, so it can still be applied.
func TestMatch_SoftKeySeesAReversedLine(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	inv := kidInvoice(t, h)
	toMatchDay(h)
	c := importer(t, h)
	_, ids := camtLines(t, h, c, "SOFT-REV-1", kidEntry(5, 400, *inv.Kid, "", "FIRST"), reversalEntry(6, 400, "REV"))
	acted(t, c, ids["REV"], "handle-reversal", map[string]any{"removePayments": []map[string]any{
		{"invoiceId": inv.ID, "paymentId": paymentOf(t, h, ids["FIRST"])}}})

	again, ids2 := camtLines(t, h, c, "SOFT-REV-2", kidEntry(5, 400, *inv.Kid, "", "SECOND"))
	if again.Matched != 0 || again.Duplicates != 0 {
		t.Errorf("the other notification = %+v, want nothing matched and no duplicate row", again)
	}
	second := ids2["SECOND"]
	if got := stateOf(t, h, second); got != "exception possible_duplicate" || len(paymentsFrom(t, h, second)) != 0 {
		t.Fatalf("the other notification's line = %s with %v, want possible_duplicate and nothing registered", got, paymentsFrom(t, h, second))
	}
	if got := livePaymentsOf(t, h, inv.ID); len(got) != 0 {
		t.Errorf("the invoice's payments = %v, want none — the reversed money is not registered again", got)
	}
	l := queueLine(t, h, second)
	if d := l.PossibleDuplicateOf; d == nil || d.ID != ids["FIRST"] || !d.Reversed || len(d.Applied) != 1 || !d.Applied[0].Removed {
		t.Errorf("its twin = %+v, want the reversed line %d, reversed, its payment removed", d, ids["FIRST"])
	}
	acted(t, c, second, "apply", applyBody(allocate(inv, 400)))
}

// TestBankQueue_ADuplicateOfAReversedLineIsNeverApplied (the confirmation
// pass of Task 9's review): the same entry in a new envelope has the same
// fingerprint and becomes a duplicate row of the line it repeats — the same
// transaction. Once a reversal took that line's payment back, the row is
// refused treat-as-distinct, and a row kept as distinct before the reversal
// is refused apply, both 409 bank_transaction_reversed; confirming it a
// duplicate still works.
func TestBankQueue_ADuplicateOfAReversedLineIsNeverApplied(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	inv := kidInvoice(t, h)
	toMatchDay(h)
	c := importer(t, h)
	_, ids := camtLines(t, h, c, "DUP-REV-1", kidEntry(5, 400, *inv.Kid, "", "FIRST"), reversalEntry(6, 400, "REV"))
	duplicateRow := func(msgID string) int64 {
		r := imported(t, c, camtFile(msgID, sellerAccount, kidEntry(5, 400, *inv.Kid, "", "FIRST")))
		return modtest.One[int64](t, h.Harness, `SELECT id FROM invoices.bank_transactions WHERE bank_file_id = $1`, r.File.ID)
	}
	kept, row := duplicateRow("DUP-REV-2"), duplicateRow("DUP-REV-3")
	acted(t, c, kept, "treat-as-distinct", nil) // before the reversal: allowed
	acted(t, c, ids["REV"], "handle-reversal", map[string]any{"removePayments": []map[string]any{
		{"invoiceId": inv.ID, "paymentId": paymentOf(t, h, ids["FIRST"])}}})

	before := queueWrites(t, h)
	queueRefused(t, "treat-as-distinct of the reversed line's duplicate", c.Do(http.MethodPost, queueActionPath(row, "treat-as-distinct"), nil), "bank_transaction_reversed")
	queueRefused(t, "apply of the kept duplicate", c.Do(http.MethodPost, queueActionPath(kept, "apply"), applyBody(allocate(inv, 400))), "bank_transaction_reversed")
	if after := queueWrites(t, h); after != before {
		t.Errorf("the refusals wrote: %s, was %s", after, before)
	}
	if got := livePaymentsOf(t, h, inv.ID); len(got) != 0 {
		t.Errorf("the invoice's payments = %v, want none", got)
	}
	if l := queueLine(t, h, row); l.PossibleDuplicateOf == nil || !l.PossibleDuplicateOf.Reversed {
		t.Errorf("the duplicate row's twin = %+v, want it shown reversed", l.PossibleDuplicateOf)
	}
	acted(t, c, row, "confirm-duplicate", map[string]any{"note": "Samme transaksjon, tilbakeført"})
	acted(t, c, kept, "confirm-duplicate", map[string]any{})
}
