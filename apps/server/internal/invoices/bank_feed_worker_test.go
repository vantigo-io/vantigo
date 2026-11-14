package invoices_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/bankfeed/folio/foliotest"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The invoices-bank-feed worker (invoices bank feed design F5, F7; plan C4,
// C6): F7's outcomes, the backoff, the claim no two workers share, Fetch now
// through the same claim, and the cycle on a pool of one connection.

// transactionReads is how many reads of transactions the fake received.
func transactionReads(h *harness) int {
	n := 0
	for _, r := range h.folio.Requests() {
		if strings.HasSuffix(r.Path, "/transactions") || strings.Contains(r.Path, "/transactions?") {
			n++
		}
	}
	return n
}

// The backoff (F7): the interval doubled with each failure in a row, at
// most six hours.
func TestBankFeedWorker_Backoff(t *testing.T) {
	t.Parallel()
	for failures, want := range map[int32]time.Duration{
		1: time.Hour, 2: 2 * time.Hour, 3: 4 * time.Hour, 4: 6 * time.Hour, 10: 6 * time.Hour, 40: 6 * time.Hour,
	} {
		if got := invoices.BankFeedBackoff(30*time.Minute, failures); got != want {
			t.Errorf("after %d failures: %v, want %v", failures, got, want)
		}
	}
	if got := invoices.BankFeedBackoff(5*time.Hour, 1); got != 6*time.Hour {
		t.Errorf("a 5-hour interval after one failure: %v, want the 6-hour cap", got)
	}
}

// Any other error (F7): one more failure in a row, failing from the third,
// read again after the backoff, nothing else written; a later success is ok
// with no failures. The log names the failure and never the key.
func TestBankFeedWorker_FailuresThenFailing(t *testing.T) {
	t.Parallel()
	h, _ := syncHarness(t)
	feedReady(t, h)
	h.folio.Fail(foliotest.OpTransactions, foliotest.Failure{Status: http.StatusInternalServerError})
	wants := []struct {
		status string
		wait   time.Duration
	}{{"ok", time.Hour}, {"ok", 2 * time.Hour}, {"failing", 4 * time.Hour}}
	for i, want := range wants {
		syncNow(t, h)
		s := feedStateOf(t, h, sellerAccount)
		if s.status != want.status || s.failures != int32(i+1) || s.next == nil || !s.next.Equal(h.Now().Add(want.wait)) || s.leased {
			t.Errorf("after failure %d: %+v, want %s due in %v", i+1, s, want.status, want.wait)
		}
		syncNow(t, h)
		if n := transactionReads(h); n != i+1 {
			t.Errorf("after failure %d the bank was read %d times, want %d: an account is not read before its next try", i+1, n, i+1)
		}
		h.Advance(want.wait)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.bank_files`); n != 0 {
		t.Errorf("%d files stored by failed cycles", n)
	}
	logs := h.Logs()
	if !strings.Contains(logs, "the bank could not be reached") || strings.Contains(logs, foliotest.APIKey) {
		t.Errorf("the log names no failure, or carries the key:\n%s", logs)
	}

	h.folio.Clear(foliotest.OpTransactions)
	syncNow(t, h)
	if s := feedStateOf(t, h, sellerAccount); s.status != "ok" || s.failures != 0 || s.detail != "" {
		t.Errorf("after a success: %+v, want ok, no failures", s)
	}
}

// The bank's 429 (F7): the status kept, the wait noted, the next read after
// the wait and never before the interval.
func TestBankFeedWorker_RetryAfterIsHonoured(t *testing.T) {
	t.Parallel()
	for retry, wait := range map[string]time.Duration{"120": 30 * time.Minute, "7200": 2 * time.Hour} {
		t.Run(retry, func(t *testing.T) {
			t.Parallel()
			h, _ := syncHarness(t)
			feedReady(t, h)
			h.folio.Fail(foliotest.OpTransactions, foliotest.Failure{Status: http.StatusTooManyRequests, RetryAfter: retry})
			syncNow(t, h)
			s := feedStateOf(t, h, sellerAccount)
			if s.status != "ok" || s.failures != 0 || !strings.Contains(s.detail, "wait") || s.next == nil || !s.next.Equal(h.Now().Add(wait)) {
				t.Errorf("after a 429 with Retry-After %s: %+v, want ok, the wait noted, due in %v", retry, s, wait)
			}
		})
	}
}

// A key the bank refuses (F7) stops every account the feed reads: the
// connection and each account unauthorized, no next sync, and no read until
// a manager stores a key again — which makes them ok and due.
func TestBankFeedWorker_UnauthorizedStopsEveryAccount(t *testing.T) {
	t.Parallel()
	h, _ := syncHarness(t)
	h.folio.SetAccounts(
		foliotest.Account{AccountNumber: sellerAccount, Name: "Drift", Type: "Operational", Balance: "0.00"},
		foliotest.Account{AccountNumber: olderAccount, Name: "Gamle driftskonto", Type: "Savings", Balance: "0.00"},
	)
	feedReady(t, h)
	switchedOn(t, h, olderAccount, feedStart)
	h.folio.Fail(foliotest.OpTransactions, foliotest.Failure{Status: http.StatusUnauthorized, Times: 1})
	syncNow(t, h)
	if got := modtest1(t, h, `SELECT status FROM invoices.bank_feed_connections`); got != "unauthorized" {
		t.Errorf("the connection = %s, want unauthorized", got)
	}
	for _, account := range []string{sellerAccount, olderAccount} {
		if s := feedStateOf(t, h, account); s.status != "unauthorized" || s.next != nil || s.leased {
			t.Errorf("account %s = %+v, want unauthorized with no next sync", account, s)
		}
	}
	reads := transactionReads(h)
	h.Advance(24 * time.Hour)
	syncNow(t, h)
	if res := importer(t, h).Do(http.MethodPost, bankFeedPath+"/sync", map[string]any{}); res.Status != http.StatusAccepted {
		t.Errorf("Fetch now = %d %s", res.Status, res.Body)
	}
	syncNow(t, h)
	if n := transactionReads(h) - reads; n != 0 {
		t.Errorf("the bank was read %d times with the key refused, want none", n)
	}
	connect(t, h)
	for _, account := range []string{sellerAccount, olderAccount} {
		if s := feedStateOf(t, h, account); s.status != "ok" || s.next == nil || !s.next.Equal(h.Now()) {
			t.Errorf("account %s after a new key = %+v, want ok and due now", account, s)
		}
	}
	syncNow(t, h)
	if n := transactionReads(h) - reads; n != 2 {
		t.Errorf("after a new key the bank was read %d times, want both accounts", n)
	}
}

// Two workers never take one account, and Fetch now goes through the same
// claim: made due, the account is read once however many workers run.
func TestBankFeedWorker_OneClaimPerAccount(t *testing.T) {
	t.Parallel()
	h, _ := syncHarness(t)
	feedReady(t, h)
	syncNow(t, h)
	if n := transactionReads(h); n != 1 {
		t.Fatalf("the first sync read %d times", n)
	}
	syncNow(t, h)
	if n := transactionReads(h); n != 1 {
		t.Errorf("an account not due was read: %d reads", n)
	}
	res := importer(t, h).Do(http.MethodPost, bankFeedPath+"/sync", map[string]any{"account": sellerAccount})
	if res.Status != http.StatusAccepted || !strings.Contains(string(res.Body), sellerAccount) {
		t.Fatalf("Fetch now = %d %s, want 202 naming the account", res.Status, res.Body)
	}
	if s := feedStateOf(t, h, sellerAccount); s.next == nil || !s.next.Equal(h.Now()) {
		t.Errorf("after Fetch now: %+v, want due now", s)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := feedWorker(t, h.Deps()).RunCycle(ctx); err != nil {
				t.Errorf("a worker's cycle: %v", err)
			}
		})
	}
	wg.Wait()
	if n := transactionReads(h); n != 2 {
		t.Errorf("four workers after Fetch now read %d times in all, want one more read", n)
	}
}

// Fetch now's refusals: the switch off, no key, an account not 11 digits,
// one the feed is not switched on for, and a caller without
// invoices:payments.
func TestBankFeedSync_FetchNowRefusals(t *testing.T) {
	t.Parallel()
	h, _ := syncHarness(t)
	toMatchDay(h)
	syncPath := bankFeedPath + "/sync"
	if res := importer(t, h).Do(http.MethodPost, syncPath, map[string]any{}); res.Status != http.StatusConflict || !strings.Contains(string(res.Body), "bank_feed_not_connected") {
		t.Errorf("without a key = %d %s, want 409 bank_feed_not_connected", res.Status, res.Body)
	}
	connect(t, h)
	if res := importer(t, h).Do(http.MethodPost, syncPath, map[string]any{"account": "123"}); res.Status != http.StatusBadRequest {
		t.Errorf("a bad account = %d, want 400", res.Status)
	}
	if res := importer(t, h).Do(http.MethodPost, syncPath, map[string]any{"account": olderAccount}); res.Status != http.StatusNotFound {
		t.Errorf("an account the feed does not read = %d, want 404", res.Status)
	}
	if res := feedManager(t, h).Do(http.MethodPost, syncPath, map[string]any{}); res.Status != http.StatusForbidden {
		t.Errorf("a manager without invoices:payments = %d, want 403", res.Status)
	}
	if res := importer(t, h).Do(http.MethodPost, syncPath, map[string]any{}); res.Status != http.StatusAccepted || !strings.Contains(string(res.Body), `"accounts":[]`) {
		t.Errorf("with nothing switched on = %d %s, want 202 and no account", res.Status, res.Body)
	}

	off, _ := syncHarness(t, modtest.WithEnv("INVOICES_BANK_FEED_ENABLED", "0"))
	if res := importer(t, off).Do(http.MethodPost, syncPath, map[string]any{}); res.Status != http.StatusConflict || !strings.Contains(string(res.Body), "bank_feed_disabled") {
		t.Errorf("with the switch off = %d %s, want 409 bank_feed_disabled", res.Status, res.Body)
	}
}

// A cycle needs no connection while it works (the lease workers' pool rule;
// memory lease-worker-pool-fix): the claim is one statement, and the bank,
// the object store, the import's transaction and each line's match take and
// release one each. On a pool of one, a cycle that held a connection while
// reaching for another would wait for itself; the deadline turns that hang
// into this test's failure. Every path runs: nothing due; a batch stored and
// matched; and a key the bank refuses.
func TestBankFeedWorker_RunsTheCycleOnAPoolOfOne(t *testing.T) {
	t.Parallel()
	h, _ := syncHarness(t)
	inv := kidInvoice(t, h)
	toMatchDay(h)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	one, err := db.Open(ctx, h.Pool().Config().ConnString(), db.WithMaxConns(1))
	if err != nil {
		t.Fatalf("open a pool of one: %v", err)
	}
	defer one.Close()
	d := h.Deps()
	d.Pool = one
	w := feedWorker(t, d)

	if err := w.RunCycle(ctx); err != nil {
		t.Fatalf("RunCycle with nothing due on a pool of one: %v", err)
	}
	connect(t, h)
	switchedOn(t, h, sellerAccount, feedStart)
	h.folio.AddTransaction(sellerAccount, incoming(txID(1), "2026-10-06", "1000.00", *inv.Kid))
	if err := w.RunCycle(ctx); err != nil {
		t.Fatalf("RunCycle storing a batch on a pool of one: %v", err)
	}
	if got := livePaymentsOf(t, h, inv.ID); !slices.Equal(got, []string{"1000.00 bank_feed"}) {
		t.Errorf("payments = %v, want the batch matched", got)
	}
	h.folio.Fail(foliotest.OpTransactions, foliotest.Failure{Status: http.StatusForbidden})
	h.Advance(time.Hour)
	if err := w.RunCycle(ctx); err != nil {
		t.Fatalf("RunCycle with the key refused on a pool of one: %v", err)
	}
	if s := feedStateOf(t, h, sellerAccount); s.status != "unauthorized" {
		t.Errorf("the account = %+v, want unauthorized", s)
	}
}

// modtest1 is one text a query answers.
func modtest1(t *testing.T, h *harness, sql string, args ...any) string {
	t.Helper()
	got := texts(t, h, sql, args...)
	if len(got) != 1 {
		t.Fatalf("%s answered %v, want one row", sql, got)
	}
	return got[0]
}

// The bank feed's attention items (F7): one per account unauthorized,
// failing or refused, for a caller holding invoices:manage — none for one
// without it, and none for an ok account or one switched off.
func TestBankFeedWorker_Attention(t *testing.T) {
	t.Parallel()
	h, _ := syncHarness(t)
	h.folio.SetAccounts(
		foliotest.Account{AccountNumber: sellerAccount, Name: "Drift", Type: "Operational", Balance: "0.00"},
		foliotest.Account{AccountNumber: olderAccount, Name: "Gamle driftskonto", Type: "Savings", Balance: "0.00"},
	)
	feedReady(t, h)
	switchedOn(t, h, olderAccount, feedStart)
	feedItems := func(items []attentionItemJSON) []string {
		var out []string
		for _, i := range items {
			if strings.HasPrefix(i.Type, "bankFeed") {
				out = append(out, i.ID+" "+i.EntityID+" "+i.Title)
			}
		}
		slices.Sort(out)
		return out
	}
	manager := feedManager(t, h)
	if got := feedItems(attentionOf(t, manager)); len(got) != 0 {
		t.Errorf("with every account ok: %v, want none", got)
	}
	h.Exec(t, `UPDATE invoices.bank_feed_accounts SET status = 'failing', updated_at = $2 WHERE account = $1`, sellerAccount, h.Now())
	h.Exec(t, `UPDATE invoices.bank_feed_accounts SET status = 'refused', reason = 'direction_unknown', updated_at = $2 WHERE account = $1`, olderAccount, h.Now())
	want := []string{
		"bankFeedFailing/" + sellerAccount + " " + sellerAccount + " 7947",
		"bankFeedRefused/" + olderAccount + " " + olderAccount + " 8903",
	}
	if got := feedItems(attentionOf(t, manager)); !slices.Equal(got, want) {
		t.Errorf("attention = %v, want %v", got, want)
	}
	if got := feedItems(attentionOf(t, importer(t, h))); len(got) != 0 {
		t.Errorf("a caller without invoices:manage sees %v, want none", got)
	}
	h.Exec(t, `UPDATE invoices.bank_feed_accounts SET status = 'unauthorized' WHERE account = $1`, sellerAccount)
	if res := switchFeed(manager, olderAccount, false, ""); res.Status != http.StatusOK {
		t.Fatalf("switch off = %d %s", res.Status, res.Body)
	}
	if got := feedItems(attentionOf(t, manager)); !slices.Equal(got, []string{"bankFeedUnauthorized/" + sellerAccount + " " + sellerAccount + " 7947"}) {
		t.Errorf("attention = %v, want the unauthorized account alone", got)
	}
}

// A sync's match against an apply from the queue on the same invoice (plan
// C6; D4's race): the sync's line parked after its own lock, holding no
// invoice, while a person applies a queued line of a file to the invoice's
// whole open amount. Released, the sync locks the invoice, finds it settled
// and queues its line invoice_settled — one payment, the person's. No
// deadlock.
func TestBankFeedSync_MatchRacesAnApply(t *testing.T) {
	h, _ := syncHarness(t, modtest.WithPoolMaxConns(3))
	inv := kidInvoice(t, h)
	toMatchDay(h)
	r := imported(t, importer(t, h), camtFile("RACE-FEED", olderAccount, noKidEntry(5, 1000, "Faktura", "Q1")))
	queued := lineID(t, h, r.File.ID, "Q1")
	connect(t, h)
	switchedOn(t, h, sellerAccount, feedStart)
	h.folio.AddTransaction(sellerAccount, incoming(txID(1), "2026-10-06", "1000.00", *inv.Kid))
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)
	hook, parked := parkEach()
	restore := invoices.SetMatchAfterLineLock(hook)
	defer restore()

	syncDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		syncDone <- feedWorker(t, h.Deps()).RunCycle(ctx)
	}()
	p := waitParked(t, "the sync's match", parked)
	if got := heldMode(t, probeConn, "invoices.invoices", "id = $1", inv.ID); got != "" {
		t.Errorf("the invoice is held %q while the sync is parked on its line, want free", got)
	}
	acted(t, importer(t, h), queued, "apply", applyBody(allocate(inv, 1000)))
	close(p.release)
	select {
	case err := <-syncDone:
		if err != nil {
			t.Fatalf("the sync: %v", err)
		}
	case <-time.After(40 * time.Second):
		t.Fatal("the sync never finished")
	}
	if got := livePaymentsOf(t, h, inv.ID); !slices.Equal(got, []string{"1000.00 camt054"}) {
		t.Errorf("the invoice's payments = %v, want the person's alone", got)
	}
	if got := stateOf(t, h, feedLine(t, h, txID(1))); got != "exception invoice_settled" {
		t.Errorf("the feed line = %s, want queued invoice_settled", got)
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("Postgres broke %d deadlock(s)", after-before)
	}
}
