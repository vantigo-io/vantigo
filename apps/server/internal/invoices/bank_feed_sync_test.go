package invoices_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/bankfeed"
	"github.com/vantigo-io/vantigo/server/internal/bankfeed/folio/foliotest"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The bank feed's sync (invoices bank feed design F5, F6; plan C2, C3, C6):
// the batch refused whole or kept as incoming credit transfers, what was
// stored before read again and dropped, the document stored, the import's
// own transaction, and matching by no user.

const (
	// payerAccount is the account the customers pay from.
	payerAccount = strangerAccount
	// feedStart is the start day every sync test switches the feed on from.
	feedStart = "2026-09-10"
)

// syncHarness is matchHarness — a KID agreement, an invoice issued on
// olderAccount, the seller on sellerAccount — whose bank, the fake, answers
// the seller's account. The clock is still modtest.Start's; feedReady moves
// it to bankDay and switches the feed on.
func syncHarness(t *testing.T, opts ...modtest.Option) (*harness, invoiceJSON) {
	t.Helper()
	h, first := matchHarness(t, opts...)
	h.folio.SetAccounts(foliotest.Account{AccountNumber: sellerAccount, Name: "Drift", Type: "Operational", Balance: "0.00"})
	return h, first
}

// feedReady moves h to bankDay, stores the fake's key and switches the feed
// on for sellerAccount from feedStart.
func feedReady(t *testing.T, h *harness) {
	t.Helper()
	toMatchDay(h)
	connect(t, h)
	switchedOn(t, h, sellerAccount, feedStart)
}

// txID is the n-th Folio transaction id, a UUID as Folio writes them.
func txID(n int) string { return fmt.Sprintf("0000000%d-aaaa-4bbb-8ccc-%012d", n%10, n) }

// incoming is a payment of amount ("1000.00") into the seller's account,
// booked on day ("2026-10-06"), with KID k ("" for none).
func incoming(id, day, amount, k string) foliotest.Transaction {
	return foliotest.Transaction{
		ID: id, EventID: "event-" + id, Description: "Betaling faktura", BookingDate: day, KID: k,
		Creditor:          &foliotest.Party{Name: "Selger AS", AccountNumber: sellerAccount},
		Debtor:            &foliotest.Party{Name: "Kari Nordmann", AccountNumber: payerAccount},
		TransactionAmount: foliotest.Amount{Amount: amount, Currency: "NOK"}, Complete: true,
	}
}

// outgoing is a payment of amount out of the seller's account.
func outgoing(id, day, amount string) foliotest.Transaction {
	return foliotest.Transaction{
		ID: id, EventID: "event-" + id, Description: "Lønn oktober", BookingDate: day,
		Creditor:          &foliotest.Party{Name: "Ola Ansatt", AccountNumber: payerAccount},
		Debtor:            &foliotest.Party{Name: "Selger AS", AccountNumber: sellerAccount},
		TransactionAmount: foliotest.Amount{Amount: amount, Currency: "NOK"}, Complete: true,
	}
}

// feedWorker is the bank feed worker Module.Workers hands the runner for d.
func feedWorker(t *testing.T, d module.Deps) *invoices.BankFeedWorker {
	t.Helper()
	for _, w := range invoices.Module().Workers(d) {
		if fw, ok := w.(*invoices.BankFeedWorker); ok {
			return fw
		}
	}
	t.Fatal("Module.Workers has no bank feed worker")
	return nil
}

// syncNow runs one cycle of the bank feed worker over every account due.
func syncNow(t *testing.T, h *harness) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := feedWorker(t, h.Deps()).RunCycle(ctx); err != nil {
		t.Fatalf("the bank feed worker's cycle: %v", err)
	}
}

// feedState is an account's bank feed row as the sync left it.
type feedState struct {
	status, reason, detail string
	failures               int32
	next                   *time.Time
	lastFile               *int64
	leased                 bool
}

func feedStateOf(t *testing.T, h *harness, account string) feedState {
	t.Helper()
	var s feedState
	var reason *string
	if err := h.Pool().QueryRow(context.Background(), `SELECT status, reason, detail, failures, next_sync_at, last_bank_file_id, lease_id IS NOT NULL
		FROM invoices.bank_feed_accounts WHERE account = $1`, account).Scan(&s.status, &reason, &s.detail, &s.failures, &s.next, &s.lastFile, &s.leased); err != nil {
		t.Fatalf("read account %s's bank feed row: %v", account, err)
	}
	if reason != nil {
		s.reason = *reason
	}
	return s
}

// feedFiles is the ids of every feed batch, oldest first.
func feedFiles(t *testing.T, h *harness) []string {
	t.Helper()
	return texts(t, h, `SELECT id::text FROM invoices.bank_files WHERE format = 'feed' ORDER BY id`)
}

// feedLine is the live line the bank's transaction id became.
func feedLine(t *testing.T, h *harness, id string) int64 {
	t.Helper()
	return modtest.One[int64](t, h.Harness, `SELECT id FROM invoices.bank_transactions WHERE format = 'feed' AND line_ref = $1 AND duplicate_of_id IS NULL`, id)
}

// A sync registers an incoming KID payment by no user (F5, F6): one batch,
// format feed, provider folio, no uploader; the payment bank_feed and the
// line's matched event by no one; the outgoing payment, the card purchase
// and the internal transfer counted and absent from the stored document;
// the account ok with its batch. The wire answers each without the user.
func TestBankFeedSync_RegistersAPaymentByNoUser(t *testing.T) {
	t.Parallel()
	h, _ := syncHarness(t)
	inv := kidInvoice(t, h)
	feedReady(t, h)
	card := incoming(txID(3), "2026-10-05", "250.00", "")
	card.CardAuthorization = &foliotest.CardAuthorization{CardID: "card-1", MerchantName: "Kiosk", Amount: foliotest.Amount{Amount: "250.00", Currency: "NOK"}}
	internal := incoming(txID(4), "2026-10-05", "5000.00", "")
	internal.Debtor = &foliotest.Party{Name: "Selger AS", AccountNumber: olderAccount}
	internal.CreditorEventID = "event-other-side"
	for _, tx := range []foliotest.Transaction{
		incoming(txID(1), "2026-10-06", "1000.00", *inv.Kid), outgoing(txID(2), "2026-10-06", "30000.00"), card, internal,
	} {
		h.folio.AddTransaction(sellerAccount, tx)
	}
	syncNow(t, h)

	files := feedFiles(t, h)
	if len(files) != 1 {
		t.Fatalf("feed batches = %v, want one", files)
	}
	if got := modtest.One[string](t, h.Harness, `SELECT concat_ws(' ', provider, coalesce(uploaded_by_user_id::text, 'nobody'), transactions, ignored, ignored_kinds::text, file_identity LIKE 'folio|86011117947|%')
		FROM invoices.bank_files WHERE format = 'feed'`); got != `folio nobody 1 3 {"debit": 1, "not_booked": 0, "zero_amount": 0, "card_information": 1, "internal_transfer": 1} t` {
		t.Errorf("the batch = %s", got)
	}
	line := feedLine(t, h, txID(1))
	if got := stateOf(t, h, line); got != "matched -" {
		t.Errorf("the line = %s, want matched", got)
	}
	if got := modtest.One[string](t, h.Harness, `SELECT concat_ws(' ', archive_ref, kid, debtor_name, debtor_account, ordinal) FROM invoices.bank_transactions WHERE id = $1`, line); got != strings.ReplaceAll(txID(1), "-", "")+" "+*inv.Kid+" Kari Nordmann "+payerAccount+" 1" {
		t.Errorf("the line's fields = %s", got)
	}
	if got := paymentsFrom(t, h, line); !slices.Equal(got, []string{paid(inv, "1000.00", "bank_feed", "2026-10-06", *inv.Kid)}) {
		t.Errorf("the payments = %v", got)
	}
	if got := texts(t, h, `SELECT coalesce(registered_by_user_id::text, 'nobody') FROM invoices.payments WHERE bank_transaction_id = $1`, line); !slices.Equal(got, []string{"nobody"}) {
		t.Errorf("registered by %v, want nobody", got)
	}
	if got := eventsOf(t, h, line); !slices.Equal(got, []string{"matched -"}) {
		t.Errorf("the line's events = %v, want matched by no one", got)
	}
	state := feedStateOf(t, h, sellerAccount)
	if state.status != "ok" || state.failures != 0 || state.leased || state.lastFile == nil || fmt.Sprint(*state.lastFile) != files[0] ||
		state.next == nil || !state.next.Equal(h.Now().Add(30*time.Minute)) {
		t.Errorf("the account = %+v, want ok with its batch, due in 30 minutes", state)
	}

	// The document: the kept transaction's bytes alone, under bank-files/<sha256>.json.
	key := modtest.One[string](t, h.Harness, `SELECT object_key FROM invoices.bank_files WHERE format = 'feed'`)
	sha := modtest.One[string](t, h.Harness, `SELECT sha256 FROM invoices.bank_files WHERE format = 'feed'`)
	if key != "bank-files/"+sha+".json" {
		t.Errorf("the object key = %s, want bank-files/<sha256>.json", key)
	}
	var doc []map[string]any
	if err := json.Unmarshal(h.objects.object(key), &doc); err != nil {
		t.Fatalf("the stored document: %v", err)
	}
	if len(doc) != 1 || doc[0]["id"] != txID(1) {
		t.Errorf("the stored document = %v, want the incoming payment alone", doc)
	}
	for _, absent := range []string{txID(2), txID(3), txID(4), "Lønn oktober", "Kiosk"} {
		if strings.Contains(string(h.objects.object(key)), absent) {
			t.Errorf("the stored document carries %q", absent)
		}
	}

	// The wire: no uploader, no registrar, no event user — and no 500.
	var list struct {
		Data []map[string]any `json:"data"`
	}
	res := importer(t, h).Do(http.MethodGet, bankFilesPath, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /bank-files = %d %s", res.Status, res.Body)
	}
	res.JSON(&list)
	if f := list.Data[0]; f["format"] != "feed" || f["provider"] != "folio" || f["uploadedBy"] != nil {
		t.Errorf("the listed batch = %v, want feed by folio without uploadedBy", f)
	}
	detail := importer(t, h).Do(http.MethodGet, bankFilesPath+"/"+files[0], nil)
	if detail.Status != http.StatusOK || strings.Contains(string(detail.Body), `"by"`) || !strings.Contains(string(detail.Body), `"provider":"folio"`) {
		t.Errorf("GET the batch = %d %s, want its events without a by and its file's provider", detail.Status, detail.Body)
	}
	got := feedManager(t, h).Do(http.MethodGet, fmt.Sprintf("%s/%d", invoicesPath, inv.ID), nil)
	if got.Status != http.StatusOK || strings.Contains(string(got.Body), "registeredByUserId") || !strings.Contains(string(got.Body), `"source":"bank_feed"`) {
		t.Errorf("GET the invoice = %d %s, want a bank_feed payment without registeredByUserId", got.Status, got.Body)
	}
}

// Each rule of F5 step 2 refuses the batch whole, judged on everything the
// bank answered: a booking day after today, a currency other than NOK, a
// direction the adapter cannot place, one id twice, an id that does not fit.
// A KID longer than 25 characters is no KID; a transaction before the start
// day is left out uncounted.
func TestBankFeedSync_JudgesTheBatch(t *testing.T) {
	t.Parallel()
	today, start := oct(7), time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	credit := func(id string, day time.Time, k string) bankfeed.Transaction {
		return bankfeed.Transaction{ID: id, BookedOn: day, AmountMinor: 100000, Currency: "NOK", Direction: bankfeed.DirectionCredit, Kind: bankfeed.KindTransfer, KID: k, Raw: json.RawMessage(`{}`)}
	}
	with := func(tx bankfeed.Transaction, f func(*bankfeed.Transaction)) bankfeed.Transaction { f(&tx); return tx }
	ok := credit("a", oct(6), "0010017")
	for name, c := range map[string]struct {
		txs    []bankfeed.Transaction
		reason string
	}{
		"booked after today": {[]bankfeed.Transaction{ok, credit("b", oct(8), "")}, "booked_in_future"},
		"not NOK":            {[]bankfeed.Transaction{ok, with(credit("b", oct(6), ""), func(tx *bankfeed.Transaction) { tx.Currency = "EUR" })}, "currency_not_nok"},
		"direction unknown":  {[]bankfeed.Transaction{with(credit("b", oct(6), ""), func(tx *bankfeed.Transaction) { tx.Direction = bankfeed.DirectionUnknown }), ok}, "direction_unknown"},
		"an outgoing unknown": {[]bankfeed.Transaction{ok, with(credit("b", oct(6), ""), func(tx *bankfeed.Transaction) {
			tx.Direction, tx.Kind = bankfeed.DirectionUnknown, bankfeed.KindCard
		})}, "direction_unknown"},
		"one id twice":       {[]bankfeed.Transaction{ok, credit("a", oct(5), "")}, "duplicate_transaction"},
		"no id":              {[]bankfeed.Transaction{ok, credit("", oct(5), "")}, "transaction_unreadable"},
		"an id too long":     {[]bankfeed.Transaction{credit(strings.Repeat("x", 61), oct(5), "")}, "transaction_unreadable"},
		"an amount too high": {[]bankfeed.Transaction{with(ok, func(tx *bankfeed.Transaction) { tx.AmountMinor = 100_000_000_000_000 })}, "transaction_unreadable"},
	} {
		if kept, _, reason := invoices.JudgeFeedBatch(c.txs, sellerAccount, today, start); reason != c.reason || kept != nil {
			t.Errorf("%s: kept %v, reason %q, want the batch refused %s", name, kept, reason, c.reason)
		}
	}
	long := strings.Repeat("1", 26)
	kept, ignored, reason := invoices.JudgeFeedBatch([]bankfeed.Transaction{
		ok, credit("long", oct(6), long), credit("early", time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC), ""),
		with(credit("zero", oct(6), ""), func(tx *bankfeed.Transaction) { tx.AmountMinor = 0 }),
		with(credit("out", oct(6), ""), func(tx *bankfeed.Transaction) { tx.Direction = bankfeed.DirectionDebit }),
		with(credit("card", oct(6), ""), func(tx *bankfeed.Transaction) { tx.Kind = bankfeed.KindCard }),
		with(credit("own", oct(6), ""), func(tx *bankfeed.Transaction) { tx.Kind = bankfeed.KindInternal }),
	}, sellerAccount, today, start)
	if reason != "" || !slices.Equal(kept, []string{"a kid=0010017", "long kid="}) {
		t.Errorf("kept %v (reason %q), want the KID payment and the one whose KID is too long, without a KID", kept, reason)
	}
	if want := map[string]int{"zero_amount": 1, "debit": 1, "card_information": 1, "internal_transfer": 1}; !mapsEqual(ignored, want) {
		t.Errorf("ignored %v, want %v", ignored, want)
	}
}

// A batch the bank answers in a way the sync refuses stores nothing (F5 step
// 2): no file, no line, no object; the account refused with the reason and
// the transaction's id — never its text — and due again after the interval.
// The format rule refuses without asking the bank.
func TestBankFeedSync_RefusalStoresNothing(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		tx     foliotest.Transaction
		reason string
	}{
		"not NOK": {func() foliotest.Transaction {
			tx := incoming(txID(2), "2026-10-06", "10.00", "")
			tx.TransactionAmount.Currency = "EUR"
			return tx
		}(), "currency_not_nok"},
		"no parties": {func() foliotest.Transaction {
			tx := incoming(txID(2), "2026-10-06", "10.00", "")
			tx.Creditor, tx.Debtor = nil, nil
			return tx
		}(), "direction_unknown"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h, _ := syncHarness(t)
			inv := kidInvoice(t, h)
			feedReady(t, h)
			h.folio.AddTransaction(sellerAccount, incoming(txID(1), "2026-10-06", "1000.00", *inv.Kid))
			c.tx.Description = "Hemmelig tekst"
			h.folio.AddTransaction(sellerAccount, c.tx)
			syncNow(t, h)
			if n := h.Count(t, `SELECT count(*) FROM invoices.bank_files`) + h.Count(t, `SELECT count(*) FROM invoices.bank_transactions`); n != 0 {
				t.Errorf("%d files and lines were stored", n)
			}
			if keys, _, _ := h.objects.stored(); slices.ContainsFunc(keys, func(k string) bool { return strings.HasPrefix(k, "bank-files/") }) {
				t.Errorf("the object store holds a batch: %v", keys)
			}
			state := feedStateOf(t, h, sellerAccount)
			if state.status != "refused" || state.reason != c.reason || !strings.Contains(state.detail, txID(2)) || strings.Contains(state.detail, "Hemmelig") ||
				state.next == nil || !state.next.Equal(h.Now().Add(30*time.Minute)) || state.leased {
				t.Errorf("the account = %+v, want refused %s naming the transaction, due in 30 minutes", state, c.reason)
			}
		})
	}

	t.Run("format", func(t *testing.T) {
		t.Parallel()
		h, _ := syncHarness(t)
		feedReady(t, h)
		h.Exec(t, `UPDATE invoices.bank_import_accounts SET format = 'camt054' WHERE account = $1`, sellerAccount)
		before := folioRequests(h)
		syncNow(t, h)
		if state := feedStateOf(t, h, sellerAccount); state.status != "refused" || state.reason != "format_mismatch" {
			t.Errorf("the account = %+v, want refused format_mismatch", state)
		}
		if n := folioRequests(h) - before; n != 0 {
			t.Errorf("the bank was asked %d times for an account whose format is not feed", n)
		}
	})
}

// The overlap is read again every time and stores nothing again (F5 steps 1
// and 4): a second read of the same transactions makes no batch — not even
// when the bank has since moved one to another booking day — and the account
// keeps its last batch. Two genuine payments of one day, amount and KID are
// two lines.
func TestBankFeedSync_OverlapStoresNothingAgain(t *testing.T) {
	t.Parallel()
	h, _ := syncHarness(t)
	inv := kidInvoice(t, h)
	feedReady(t, h)
	h.folio.AddTransaction(sellerAccount, incoming(txID(1), "2026-10-06", "500.00", *inv.Kid))
	h.folio.AddTransaction(sellerAccount, incoming(txID(2), "2026-10-06", "500.00", *inv.Kid))
	syncNow(t, h)
	first := feedFiles(t, h)
	if len(first) != 1 || h.Count(t, `SELECT count(*) FROM invoices.bank_transactions WHERE format = 'feed'`) != 2 {
		t.Fatalf("batches %v; want one of two lines", first)
	}
	if got := livePaymentsOf(t, h, inv.ID); !slices.Equal(got, []string{"500.00 bank_feed", "500.00 bank_feed"}) {
		t.Errorf("the invoice's payments = %v, want the two of 500", got)
	}

	h.Advance(30 * time.Minute)
	syncNow(t, h)
	// The bank books the first on another day than it did.
	h.folio.SetTransactions(sellerAccount, incoming(txID(1), "2026-10-05", "500.00", *inv.Kid), incoming(txID(2), "2026-10-06", "500.00", *inv.Kid))
	h.Advance(30 * time.Minute)
	syncNow(t, h)
	if got := feedFiles(t, h); !slices.Equal(got, first) {
		t.Errorf("batches after two more reads = %v, want %v alone", got, first)
	}
	if got := livePaymentsOf(t, h, inv.ID); len(got) != 2 {
		t.Errorf("the invoice's payments = %v, want still two", got)
	}
	if state := feedStateOf(t, h, sellerAccount); state.status != "ok" || state.lastFile == nil || fmt.Sprint(*state.lastFile) != first[0] {
		t.Errorf("the account = %+v, want ok keeping batch %s", state, first[0])
	}
	// From the last day read less ten days.
	reqs := h.folio.Requests()
	if last := reqs[len(reqs)-1].Path; !strings.Contains(last, "startDate=2026-09-27") || !strings.Contains(last, "endDate=2026-10-07") {
		t.Errorf("the last read = %s, want from 2026-09-27 — ten days before the last day read — to 2026-10-07", last)
	}
}

// A KID longer than a KID can be is no KID: the line is queued no_kid for a
// person, who applies it — and then the payment is bank_feed by that person
// (plan finding 3).
func TestBankFeedSync_LongKidIsQueuedAndAppliedByAPerson(t *testing.T) {
	t.Parallel()
	h, _ := syncHarness(t)
	inv := kidInvoice(t, h)
	feedReady(t, h)
	h.folio.AddTransaction(sellerAccount, incoming(txID(1), "2026-10-06", "1000.00", strings.Repeat("7", 26)))
	syncNow(t, h)
	line := feedLine(t, h, txID(1))
	if got := stateOf(t, h, line); got != "exception no_kid" {
		t.Fatalf("the line = %s, want queued no_kid", got)
	}
	if got := eventsOf(t, h, line); !slices.Equal(got, []string{"queued no_kid"}) {
		t.Errorf("the line's events = %v, want queued by no one", got)
	}
	c, user := h.SignInUser(t, "invoices:access", "invoices:payments")
	acted(t, c, line, "apply", applyBody(allocate(inv, 1000)))
	if got := texts(t, h, `SELECT concat_ws(' ', source, registered_by_user_id) FROM invoices.payments WHERE bank_transaction_id = $1`, line); !slices.Equal(got, []string{"bank_feed " + user.String()}) {
		t.Errorf("the applied payment = %v, want bank_feed by %s", got, user)
	}
}

// Matching by no user writes the deadline_met waiver and the charge payment
// by no one (F6), and the invoice's wire answers both without the user.
func TestBankFeedSync_WaiverAndChargePaymentByNoUser(t *testing.T) {
	t.Parallel()
	h, _ := syncHarness(t)
	onTime, late := kidInvoice(t, h), kidInvoice(t, h)
	letters := map[int64]int64{}
	for _, inv := range []invoiceJSON{onTime, late} {
		plantSent(t, h, inv.ID, sentFacts{1, "2026-09-15", "none", "", "", "0"}) // deadline 2026-09-29
		letters[inv.ID] = plantSent(t, h, inv.ID, sentFacts{2, "2026-10-01", "reminder_fee", "35", "", "0"})
	}
	feedReady(t, h)
	h.folio.AddTransaction(sellerAccount, incoming(txID(1), "2026-09-28", "1000.00", *onTime.Kid))
	h.folio.AddTransaction(sellerAccount, incoming(txID(2), "2026-10-06", "1035.00", *late.Kid))
	syncNow(t, h)
	if got := texts(t, h, `SELECT concat_ws(' ', reminder_id, reason, coalesce(waived_by_user_id::text, 'nobody')) FROM invoices.charge_waivers WHERE invoice_id = $1`, onTime.ID); !slices.Equal(got, []string{fmt.Sprintf("%d deadline_met nobody", letters[onTime.ID])}) {
		t.Errorf("the waivers = %v, want deadline_met by nobody", got)
	}
	if got := texts(t, h, `SELECT concat_ws(' ', amount, source, coalesce(registered_by_user_id::text, 'nobody')) FROM invoices.charge_payments WHERE invoice_id = $1`, late.ID); !slices.Equal(got, []string{"35.00 bank_feed nobody"}) {
		t.Errorf("the charge payments = %v, want 35.00 bank_feed by nobody", got)
	}
	for _, inv := range []invoiceJSON{onTime, late} {
		res := feedManager(t, h).Do(http.MethodGet, fmt.Sprintf("%s/%d", invoicesPath, inv.ID), nil)
		if res.Status != http.StatusOK || strings.Contains(string(res.Body), "registeredByUserId") || strings.Contains(string(res.Body), "waivedBy") {
			t.Errorf("GET invoice %d = %d %s, want no registrar and no waiver's user", inv.ID, res.Status, res.Body)
		}
	}
}

// The cutover from files (F4, F6): an account taken from camt054 to the feed
// holds back a feed line booked on or before the cutover — the files may
// have registered it — and a second payment of the same day, amount and KID
// in a later batch is held back as possible_duplicate by the soft key.
func TestBankFeedSync_CutoverAndSoftKeyHoldBack(t *testing.T) {
	t.Parallel()
	h, _ := syncHarness(t)
	camt, fresh, again := kidInvoice(t, h), kidInvoice(t, h), kidInvoice(t, h)
	toMatchDay(h)
	imported(t, importer(t, h), camtFile("CUT-1", sellerAccount, camtPay(2, 1000, *camt.Kid, "C1")))
	connect(t, h)
	switchedOn(t, h, sellerAccount, feedStart)
	h.folio.AddTransaction(sellerAccount, incoming(txID(1), "2026-10-02", "1000.00", *camt.Kid))
	h.folio.AddTransaction(sellerAccount, incoming(txID(2), "2026-10-06", "1000.00", *fresh.Kid))
	h.folio.AddTransaction(sellerAccount, incoming(txID(3), "2026-10-06", "400.00", *again.Kid))
	syncNow(t, h)
	if got := stateOf(t, h, feedLine(t, h, txID(1))); got != "exception possible_duplicate" {
		t.Errorf("the feed line on the cutover = %s, want held back possible_duplicate", got)
	}
	if got := livePaymentsOf(t, h, camt.ID); !slices.Equal(got, []string{"1000.00 camt054"}) {
		t.Errorf("the camt054 invoice's payments = %v, want the file's alone", got)
	}
	if got := stateOf(t, h, feedLine(t, h, txID(2))); got != "matched -" {
		t.Errorf("the feed line after the cutover = %s, want matched", got)
	}

	h.folio.AddTransaction(sellerAccount, incoming(txID(4), "2026-10-06", "400.00", *again.Kid))
	h.Advance(30 * time.Minute)
	syncNow(t, h)
	if got := stateOf(t, h, feedLine(t, h, txID(4))); got != "exception possible_duplicate" {
		t.Errorf("the same day, amount and KID in a later batch = %s, want held back possible_duplicate", got)
	}
	if got := livePaymentsOf(t, h, again.ID); !slices.Equal(got, []string{"400.00 bank_feed"}) {
		t.Errorf("the invoice's payments = %v, want the first 400 alone", got)
	}
}

// No object store, or one that fails: the cycle fails as any other error and
// is tried again; nothing is stored.
func TestBankFeedSync_NeedsTheObjectStore(t *testing.T) {
	t.Parallel()
	h, _ := syncHarness(t)
	inv := kidInvoice(t, h)
	feedReady(t, h)
	h.folio.AddTransaction(sellerAccount, incoming(txID(1), "2026-10-06", "1000.00", *inv.Kid))
	h.objects.failPuts(errors.New("the store is down"))
	syncNow(t, h)
	if n := h.Count(t, `SELECT count(*) FROM invoices.bank_files`); n != 0 {
		t.Errorf("%d files stored without the document", n)
	}
	if state := feedStateOf(t, h, sellerAccount); state.status != "ok" || state.failures != 1 || !strings.Contains(state.detail, "object store") {
		t.Errorf("the account = %+v, want one failure naming the object store", state)
	}
	h.objects.failPuts(nil)
	h.Advance(time.Hour)
	syncNow(t, h)
	if got := livePaymentsOf(t, h, inv.ID); !slices.Equal(got, []string{"1000.00 bank_feed"}) {
		t.Errorf("after the store is back: payments %v, want one", got)
	}

	// An installation without an object store fails the cycle the same way.
	bare := newHarnessWithoutStore(t)
	saveSeller(t, bare, completeSeller(1))
	bare.Advance(bankDay.Sub(bare.Now()))
	bare.folio.SetAccounts(foliotest.Account{AccountNumber: sellerAccount, Name: "Drift", Type: "Operational", Balance: "0.00"})
	connect(t, bare)
	switchedOn(t, bare, sellerAccount, feedStart)
	bare.folio.AddTransaction(sellerAccount, incoming(txID(1), "2026-10-06", "1000.00", ""))
	syncNow(t, bare)
	if state := feedStateOf(t, bare, sellerAccount); state.failures != 1 || bare.Count(t, `SELECT count(*) FROM invoices.bank_files`) != 0 {
		t.Errorf("without an object store: the account = %+v, want one failure and nothing stored", state)
	}
}

// The crash points (F5's idempotence): a cycle that stopped after its batch
// committed and before matching leaves the lines pending, and the next cycle
// stores nothing again and matches them — one payment; a cycle whose import
// rolled back after the document was stored stores the batch once the next
// time, reusing the document.
func TestBankFeedSync_CrashPointsRegisterOnePayment(t *testing.T) {
	h, _ := syncHarness(t)
	inv, other := kidInvoice(t, h), kidInvoice(t, h)
	feedReady(t, h)
	h.folio.AddTransaction(sellerAccount, incoming(txID(1), "2026-10-06", "1000.00", *inv.Kid))

	restore := invoices.SetBankFeedSyncAfterStore(func(context.Context, int64) error { return errors.New("the process stopped") })
	syncNow(t, h)
	restore()
	if got := stateOf(t, h, feedLine(t, h, txID(1))); got != "pending -" {
		t.Fatalf("the line after the stop = %s, want pending", got)
	}
	h.Advance(2 * time.Hour)
	syncNow(t, h)
	if got := feedFiles(t, h); len(got) != 1 {
		t.Errorf("batches = %v, want the first alone", got)
	}
	if got := livePaymentsOf(t, h, inv.ID); !slices.Equal(got, []string{"1000.00 bank_feed"}) {
		t.Errorf("payments = %v, want one", got)
	}

	h.folio.AddTransaction(sellerAccount, incoming(txID(2), "2026-10-07", "1000.00", *other.Kid))
	restore = invoices.SetBankImportAfterInsert(func(context.Context, int64) error { return errors.New("the import rolled back") })
	h.Advance(time.Hour)
	syncNow(t, h)
	restore()
	if n := h.Count(t, `SELECT count(*) FROM invoices.bank_transactions WHERE line_ref = $1`, txID(2)); n != 0 {
		t.Fatalf("%d lines kept from a rolled-back import", n)
	}
	_, putsBefore, _ := h.objects.stored()
	h.Advance(2 * time.Hour)
	syncNow(t, h)
	if _, puts, _ := h.objects.stored(); puts != putsBefore {
		t.Errorf("the document was put %d times more, want reused", puts-putsBefore)
	}
	if got := livePaymentsOf(t, h, other.ID); !slices.Equal(got, []string{"1000.00 bank_feed"}) {
		t.Errorf("payments = %v, want one", got)
	}
	if got := feedFiles(t, h); len(got) != 2 {
		t.Errorf("batches = %v, want two", got)
	}
}

// The sync's locks (D18 and plan finding 10): the account's bank import row
// FOR SHARE, then its bank feed row under the lease; then each line and its
// invoice, as an import's match.
func TestBankFeedSync_LockOrder(t *testing.T) {
	h, _ := syncHarness(t)
	inv := kidInvoice(t, h)
	feedReady(t, h)
	h.folio.AddTransaction(sellerAccount, incoming(txID(1), "2026-10-06", "1000.00", *inv.Kid))
	seen := &lockSeen{}
	restore := invoices.SetLockTaken(seen.note)
	defer restore()
	syncNow(t, h)
	line := feedLine(t, h, txID(1))
	want := []string{"account " + sellerAccount, "bank_feed_account " + sellerAccount, fmt.Sprintf("bank_transaction %d", line), fmt.Sprintf("invoice %d", inv.ID)}
	if got := seen.take(); !slices.Equal(got, want) {
		t.Errorf("the sync's locks = %v, want %v", got, want)
	}
}

// A file uploaded for an account the feed reads is refused, and the sync's
// batches stay the account's (plan B4's pinned rule, with batches stored).
func TestBankFeedSync_UploadForAFeedAccountIsRefused(t *testing.T) {
	t.Parallel()
	h, _ := syncHarness(t)
	inv := kidInvoice(t, h)
	feedReady(t, h)
	refusedImport(t, "a camt.054 for a feed account", upload(t, importer(t, h), camtFile("FEED-1", sellerAccount, camtPay(6, 1000, *inv.Kid, "C1"))), "bank_import_format_mismatch")
	h.folio.AddTransaction(sellerAccount, incoming(txID(1), "2026-10-06", "1000.00", *inv.Kid))
	syncNow(t, h)
	if got := livePaymentsOf(t, h, inv.ID); !slices.Equal(got, []string{"1000.00 bank_feed"}) {
		t.Errorf("payments = %v, want the feed's", got)
	}
}

// One line that cannot be matched never stops the rest (plan C review, item
// 4): a payment whose KID names an invoice in EUR fails closed and stays
// pending, matching goes on with the next line and the next batch, and the
// account stays ok with the line named in its reason and detail.
func TestBankFeedSync_AFailingLineDoesNotStopLaterBatches(t *testing.T) {
	t.Parallel()
	h, _ := syncHarness(t)
	h.Exec(t, `UPDATE invoices.settings SET default_currency = 'EUR'`)
	euro := kidded(t, issued(t, h, createDraft(t, h, draftBody(customerEuro, line("Konsulenttime", 10, 80, vat25))).ID))
	h.Exec(t, `UPDATE invoices.settings SET default_currency = 'NOK'`)
	inv, later := kidInvoice(t, h), kidInvoice(t, h)
	feedReady(t, h)
	h.folio.AddTransaction(sellerAccount, incoming(txID(1), "2026-10-05", "100.00", *euro.Kid))
	h.folio.AddTransaction(sellerAccount, incoming(txID(2), "2026-10-06", "1000.00", *inv.Kid))
	syncNow(t, h)
	if got := stateOf(t, h, feedLine(t, h, txID(1))); got != "pending -" {
		t.Errorf("the line that cannot be matched = %s, want pending", got)
	}
	if got := livePaymentsOf(t, h, inv.ID); !slices.Equal(got, []string{"1000.00 bank_feed"}) {
		t.Errorf("the next line's payments = %v, want matched", got)
	}
	s := feedStateOf(t, h, sellerAccount)
	if s.status != "ok" || s.reason != "line_unmatched" || !strings.Contains(s.detail, txID(1)) {
		t.Errorf("the account = %+v, want ok naming the line that could not be matched", s)
	}

	h.folio.AddTransaction(sellerAccount, incoming(txID(3), "2026-10-07", "1000.00", *later.Kid))
	h.Advance(30 * time.Minute)
	syncNow(t, h)
	if got := livePaymentsOf(t, h, later.ID); !slices.Equal(got, []string{"1000.00 bank_feed"}) {
		t.Errorf("a later batch's payments = %v, want matched past the line that fails", got)
	}
	if got := stateOf(t, h, feedLine(t, h, txID(1))); got != "pending -" {
		t.Errorf("the line that cannot be matched = %s, want still pending", got)
	}
	if !strings.Contains(h.Logs(), "a bank line could not be matched; it stays pending") {
		t.Error("no warning names the line passed over")
	}
}
