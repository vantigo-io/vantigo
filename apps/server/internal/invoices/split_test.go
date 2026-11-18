package invoices_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile/bankfiletest"
)

// The automatic split (invoices phase 6 design I14): one bank line whose
// remittance names several issued NOK invoices of one customer, whose open
// amounts add up to it exactly, is matched across them, oldest due first, by
// the uploader, with an auto_split event; any doubt queues it as before.

// camtText is one camt.054 entry booked on day of amount with KID k (none
// when ""), its unstructured text, its further structured references and
// its AcctSvcrRef ref.
func camtText(day int, amount float64, k, text string, refs []string, ref string) bankfiletest.CamtEntry {
	return bankfiletest.CamtEntry{BookedOn: oct(day), Txs: []bankfiletest.CamtTx{{
		AmountMinor: minor(amount), KID: k, Ustrd: text, Refs: refs, AcctSvcrRef: ref,
	}}}
}

// num is an invoice's number as text.
func num(inv invoiceJSON) string { return fmt.Sprint(*inv.Number) }

func TestSplit_OneLineSeveralInvoices(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	a, b, c, d := kidInvoice(t, h), kidInvoice(t, h), kidInvoice(t, h), kidInvoice(t, h)
	e, f := kidInvoice(t, h), kidInvoice(t, h)
	registered(t, h, f.ID, pay(400, "2026-09-12")) // 600 open
	toMatchDay(h)
	late := kidded(t, issued(t, h, createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 10, 80, vat25))).ID))
	uploader, user := h.SignInUser(t, "invoices:access", "invoices:payments")

	r := imported(t, uploader, camtFile("S1", sellerAccount,
		// The text names the later invoice first: the earlier due is paid first.
		camtText(7, 2000, "", "Faktura "+num(late)+" og "+num(a), nil, "1"),
		// Three structured references: a KID and two invoice numbers.
		camtText(7, 2600, *b.Kid, "", []string{num(c), "Fakt " + num(f)}, "2"),
		// The KID's invoice and one more: what would be exceeds_open.
		camtText(7, 2000, *d.Kid, "og "+num(e), nil, "3"),
	))
	if r.Matched != 3 || r.Exceptions != 0 {
		t.Fatalf("the import = matched %d, exceptions %d; want 3, 0", r.Matched, r.Exceptions)
	}
	one, two, three := lineID(t, h, r.File.ID, "1"), lineID(t, h, r.File.ID, "2"), lineID(t, h, r.File.ID, "3")
	if got, want := paymentsFrom(t, h, one), []string{
		paid(a, "1000.00", "camt054", "2026-10-07", ""), paid(late, "1000.00", "camt054", "2026-10-07", ""),
	}; !slices.Equal(got, want) {
		t.Errorf("line 1's payments = %v, want %v (the oldest due first)", got, want)
	}
	if got, want := paymentsFrom(t, h, two), []string{
		paid(b, "1000.00", "camt054", "2026-10-07", *b.Kid), paid(c, "1000.00", "camt054", "2026-10-07", *b.Kid),
		paid(f, "600.00", "camt054", "2026-10-07", *b.Kid),
	}; !slices.Equal(got, want) {
		t.Errorf("line 2's payments = %v, want %v", got, want)
	}
	if got := paymentsFrom(t, h, three); len(got) != 2 {
		t.Errorf("line 3's payments = %v, want the KID's invoice and the other", got)
	}
	for _, id := range []int64{one, two, three} {
		if got := stateOf(t, h, id); got != "matched -" {
			t.Errorf("line %d = %s, want matched", id, got)
		}
		if got := eventsOf(t, h, id); !slices.Equal(got, []string{"auto_split - " + user.String()}) {
			t.Errorf("line %d's events = %v, want auto_split by the uploader", id, got)
		}
	}
	if got := texts(t, h, `SELECT array_to_string(remittance_refs, '|') FROM invoices.bank_transactions WHERE id = $1`, two); !slices.Equal(got, []string{*b.Kid + "|" + num(c) + "|Fakt " + num(f)}) {
		t.Errorf("line 2's references = %v, want the three in the file's order", got)
	}
	for _, inv := range []invoiceJSON{a, b, c, d, e, f, late} {
		if got := getInvoice(t, h, inv.ID); got.State != "paid" {
			t.Errorf("invoice %s = %s, want paid", num(inv), got.State)
		}
	}
}

func TestSplit_AnyDoubtQueues(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	a, b, credited, writtenOffInv, k1, k2 := kidInvoice(t, h), kidInvoice(t, h), kidInvoice(t, h), kidInvoice(t, h), kidInvoice(t, h), kidInvoice(t, h)
	other := kidded(t, issued(t, h, createDraft(t, h, draftBody(customerPerson, line("Konsulenttime", 10, 80, vat25))).ID))
	issued(t, h, creditDraft(t, h, credited.ID).ID)
	writeOffPlanted(t, h, writtenOffInv.ID, "1000")
	charged := kidInvoice(t, h)
	plantSent(t, h, charged.ID, sentFacts{1, "2026-09-28", "reminder_fee", "35", "", "0"})
	toMatchDay(h)

	r := imported(t, importer(t, h), camtFile("S2", sellerAccount,
		camtText(6, 1999, "", "Faktura "+num(a)+" og "+num(b), nil, "1"),             // a sum that differs
		camtText(6, 2000, "", "Faktura "+num(a)+" og "+num(other), nil, "2"),         // two customers
		camtText(6, 1000, "", "Faktura "+num(a)+" og "+num(credited), nil, "3"),      // a credited invoice
		camtText(6, 1000, "", "Faktura "+num(a)+" og "+num(writtenOffInv), nil, "4"), // a written-off one
		camtText(6, 1000, "", "Faktura "+num(a), nil, "5"),                           // one invoice is no split
		camtText(6, 2035, "", "Faktura "+num(b)+" og "+num(charged), nil, "6"),       // charges are never taken
		camtText(6, 2000, "", "Faktura 1 250,00 og NOK "+num(b), nil, "7"),           // amounts are no invoice numbers
		camtText(6, 1500, *k1.Kid, "og "+num(k2), nil, "8"),                          // exceeds_open, the sum differs
	))
	if r.Matched != 0 {
		t.Errorf("the import matched %d lines, want none", r.Matched)
	}
	for ref, want := range map[string]string{
		"1": "exception no_kid", "2": "exception no_kid", "3": "exception no_kid", "4": "exception no_kid",
		"5": "exception no_kid", "6": "exception no_kid", "7": "exception no_kid", "8": "exception exceeds_open",
	} {
		id := lineID(t, h, r.File.ID, ref)
		if got := stateOf(t, h, id); got != want {
			t.Errorf("line %s = %s, want %s", ref, got, want)
		}
		if got := paymentsFrom(t, h, id); len(got) != 0 {
			t.Errorf("line %s registered %v, want nothing", ref, got)
		}
	}
	if got := suggestedOf(t, h, lineID(t, h, r.File.ID, "8")); got != idKey(k1.ID) {
		t.Errorf("the exceeds_open line suggests %s, want the KID's invoice %d", got, k1.ID)
	}
	// The queue names the invoices a line's text named.
	lines, _ := queueList(t, h, "status=exception&pageSize=50")
	first, found := lineID(t, h, r.File.ID, "1"), false
	for _, it := range lines {
		if it.ID != first || it.Suggestions == nil {
			continue
		}
		found = true
		var ids []int64
		for _, s := range *it.Suggestions {
			ids = append(ids, s.InvoiceID)
		}
		if !slices.Contains(ids, a.ID) || !slices.Contains(ids, b.ID) {
			t.Errorf("line 1's suggestions = %v, want %d and %d", ids, a.ID, b.ID)
		}
	}
	if !found {
		t.Error("line 1 is not on the queue's first page")
	}
}

// A payment registered by hand between the split's reading on the pool and
// its locks is seen under them: the sum no longer holds, and the line is
// queued as it would have been.
func TestSplit_JudgedAgainUnderTheLocks(t *testing.T) {
	h, _ := matchHarness(t)
	a, b := kidInvoice(t, h), kidInvoice(t, h)
	toMatchDay(h)
	defer invoices.SetSplitBeforeLocks(func(context.Context, int64) error {
		registered(t, h, b.ID, pay(10, "2026-10-06"))
		return nil
	})()
	r := imported(t, importer(t, h), camtFile("S3", sellerAccount,
		camtText(6, 2000, "", "Faktura "+num(a)+" og "+num(b), nil, "1")))
	id := lineID(t, h, r.File.ID, "1")
	if got := stateOf(t, h, id); got != "exception no_kid" {
		t.Errorf("the line = %s, want exception no_kid", got)
	}
	if got := livePaymentsOf(t, h, a.ID); len(got) != 0 {
		t.Errorf("invoice a's payments = %v, want none", got)
	}
}

// A line naming more invoices than a split takes is no split: queued, with
// them among its suggestions.
func TestSplit_MoreThanTwentyInvoicesIsNoSplit(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	var names []string
	for range 21 {
		names = append(names, num(kidInvoice(t, h)))
	}
	toMatchDay(h)
	r := imported(t, importer(t, h), camtFile("S21", sellerAccount,
		camtText(6, 21000, "", "Faktura "+strings.Join(names, " "), nil, "1")))
	if got := stateOf(t, h, lineID(t, h, r.File.ID, "1")); got != "exception no_kid" {
		t.Errorf("the line naming 21 invoices = %s, want exception no_kid", got)
	}
}

// The bank feed's line is split by no one (ck_payments_origin and
// ck_bank_transaction_events_by allow it), and a later sync that reads the
// same transaction again registers nothing twice.
func TestSplit_FromTheBankFeedByNoOne(t *testing.T) {
	t.Parallel()
	h, _ := syncHarness(t)
	a, b := kidInvoice(t, h), kidInvoice(t, h)
	feedReady(t, h)
	tx := incoming(txID(1), "2026-10-06", "2000.00", "")
	tx.Description = "Faktura " + num(a) + " og " + num(b)
	h.folio.AddTransaction(sellerAccount, tx)
	syncNow(t, h)
	line := feedLine(t, h, txID(1))
	if got := stateOf(t, h, line); got != "matched -" {
		t.Fatalf("the feed line = %s, want matched", got)
	}
	if got := eventsOf(t, h, line); !slices.Equal(got, []string{"auto_split -"}) {
		t.Errorf("the feed line's events = %v, want auto_split by no one", got)
	}
	if got := texts(t, h, `SELECT concat_ws(' ', invoice_id, amount, source, coalesce(registered_by_user_id::text, 'nobody'))
		FROM invoices.payments WHERE bank_transaction_id = $1 ORDER BY id`, line); !slices.Equal(got, []string{
		fmt.Sprintf("%d 1000.00 bank_feed nobody", a.ID), fmt.Sprintf("%d 1000.00 bank_feed nobody", b.ID),
	}) {
		t.Errorf("the split's payments = %v, want one of 1000 to each, by no one", got)
	}
	h.Advance(30 * time.Minute)
	syncNow(t, h)
	if n := h.Count(t, `SELECT count(*) FROM invoices.payments WHERE invoice_id = ANY($1) AND removed_at IS NULL`, []int64{a.ID, b.ID}); n != 2 {
		t.Errorf("after a second sync, %d live payments of the two invoices; want 2", n)
	}
}

// The same file imported again is refused whole, and matching it again
// finds nothing to do: a split registers once.
func TestSplit_ReimportRegistersNothingAgain(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	a, b := kidInvoice(t, h), kidInvoice(t, h)
	toMatchDay(h)
	file := camtFile("S-AGAIN", sellerAccount, camtText(6, 2000, "", "Faktura "+num(a)+" og "+num(b), nil, "1"))
	r := imported(t, importer(t, h), file)
	refusedImport(t, "the same file again", upload(t, importer(t, h), file), "bank_file_duplicate")
	if res := importer(t, h).Do(http.MethodPost, matchPath(r.File.ID), nil); res.Status != http.StatusOK {
		t.Fatalf("match again = %d %s", res.Status, res.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.payments WHERE invoice_id = ANY($1) AND removed_at IS NULL`, []int64{a.ID, b.ID}); n != 2 {
		t.Errorf("%d live payments of the two invoices; want 2", n)
	}
}

// Credit applied to one of the split's invoices between the pool's reading
// and the locks is seen under them: the sum no longer holds, the line is
// queued, and the credit stands.
func TestSplit_AgainstApplyCredit(t *testing.T) {
	h, _ := matchHarness(t)
	a, b := kidInvoice(t, h), kidInvoice(t, h)
	overpaid(t, h) // 200 of customerAcme's credit
	toMatchDay(h)
	defer invoices.SetSplitBeforeLocks(func(context.Context, int64) error {
		if res := payer(t, h).Do(http.MethodPost, applyCreditPath(b.ID), map[string]any{"amount": 50}); res.Status != http.StatusOK {
			t.Errorf("apply credit = %d %s", res.Status, res.Body)
		}
		return nil
	})()
	r := imported(t, importer(t, h), camtFile("S-CREDIT", sellerAccount,
		camtText(6, 2000, "", "Faktura "+num(a)+" og "+num(b), nil, "1")))
	if got := stateOf(t, h, lineID(t, h, r.File.ID, "1")); got != "exception no_kid" {
		t.Errorf("the line = %s, want exception no_kid", got)
	}
	if got := livePaymentsOf(t, h, b.ID); !slices.Equal(got, []string{"50.00 credit"}) {
		t.Errorf("invoice b's payments = %v, want the credit applied alone", got)
	}
}
