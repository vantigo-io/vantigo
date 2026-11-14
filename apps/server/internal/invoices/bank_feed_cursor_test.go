package invoices_test

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/bankfeed/folio/foliotest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The bank feed's read cursor and windows (00179; plan C review, item 1):
// each read starts ten days before the last day read, not the last payment
// kept; a long read is cut in windows of at most 31 days, each its own
// batch, the account due again at once until the backlog is read; a window
// too large for one answer is halved.

// transactionWindows is every transactions read the fake received, as
// "start..end".
func transactionWindows(t *testing.T, h *harness) []string {
	t.Helper()
	var out []string
	for _, r := range h.folio.Requests() {
		path, query, _ := strings.Cut(r.Path, "?")
		if !strings.HasSuffix(path, "/transactions") {
			continue
		}
		v, err := url.ParseQuery(query)
		if err != nil {
			t.Fatalf("parse %s: %v", r.Path, err)
		}
		out = append(out, v.Get("startDate")+".."+v.Get("endDate"))
	}
	return out
}

// readThroughOf is the account's read cursor, "-" for none.
func readThroughOf(t *testing.T, h *harness, account string) string {
	t.Helper()
	return modtest.One[string](t, h.Harness, `SELECT coalesce(read_through::text, '-') FROM invoices.bank_feed_accounts WHERE account = $1`, account)
}

// An account that only pays out keeps no line, yet each daily read starts
// ten days before the last day read — yesterday's read, so eleven days
// before today: the cursor moves with every window read to its end, so the
// window never grows.
func TestBankFeedCursor_AnAccountWithOnlyDebitsReadsTenDays(t *testing.T) {
	t.Parallel()
	h, _ := syncHarness(t)
	feedReady(t, h)
	for day := range 60 {
		h.folio.AddTransaction(sellerAccount, outgoing(txID(day+1), h.Now().Format(time.DateOnly), "100.00"))
		syncNow(t, h)
		windows := transactionWindows(t, h)
		today := h.Now().Format(time.DateOnly)
		if day > 0 {
			want := h.Now().AddDate(0, 0, -11).Format(time.DateOnly) + ".." + today
			if got := windows[len(windows)-1]; got != want {
				t.Fatalf("day %d: the read = %s, want %s", day, got, want)
			}
		}
		if got := readThroughOf(t, h, sellerAccount); got != today {
			t.Fatalf("day %d: read through %s, want %s", day, got, today)
		}
		h.Advance(24 * time.Hour)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.bank_files`); n != 0 {
		t.Errorf("%d batches stored for an account that only pays out", n)
	}
}

// A first read of thirteen months is read in windows of at most 31 days,
// each its own batch, the account due again at once until the backlog is
// read — in one run of the worker — and each payment is stored once, and
// once again not at the next read. Debits read in the overlap are counted on
// one batch, not on every batch.
func TestBankFeedCursor_ALongFirstReadDrainsInWindows(t *testing.T) {
	t.Parallel()
	h, _ := syncHarness(t)
	inv := kidInvoice(t, h)
	toMatchDay(h)
	connect(t, h)
	start := h.Now().AddDate(0, -13, 0).Format(time.DateOnly)
	switchedOn(t, h, sellerAccount, start)
	for m := range 13 {
		day := h.Now().AddDate(0, -m, -1).Format(time.DateOnly)
		h.folio.AddTransaction(sellerAccount, incoming(txID(m+1), day, "10.00", ""))
	}
	h.folio.AddTransaction(sellerAccount, incoming(txID(100), "2026-10-06", "1000.00", *inv.Kid))
	h.folio.AddTransaction(sellerAccount, outgoing(txID(200), "2026-10-06", "30.00"))
	syncNow(t, h)

	windows := transactionWindows(t, h)
	if len(windows) < 13 {
		t.Errorf("the read was cut in %d windows, want at least 13: %v", len(windows), windows)
	}
	for _, w := range windows {
		from, to, _ := strings.Cut(w, "..")
		a, _ := time.Parse(time.DateOnly, from)
		b, _ := time.Parse(time.DateOnly, to)
		if days := int(b.Sub(a).Hours()/24) + 1; days > 31 {
			t.Errorf("window %s is %d days, want at most 31", w, days)
		}
	}
	if !strings.HasPrefix(windows[0], start+"..") || !strings.HasSuffix(windows[len(windows)-1], ".."+h.Now().Format(time.DateOnly)) {
		t.Errorf("the windows = %v, want from %s to today", windows, start)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.bank_transactions WHERE format = 'feed'`); n != 14 {
		t.Errorf("%d feed lines, want the 14 payments once each", n)
	}
	if got := livePaymentsOf(t, h, inv.ID); !slices.Equal(got, []string{"1000.00 bank_feed"}) {
		t.Errorf("the invoice's payments = %v", got)
	}
	if got := readThroughOf(t, h, sellerAccount); got != h.Now().Format(time.DateOnly) {
		t.Errorf("read through %s, want today", got)
	}
	if s := feedStateOf(t, h, sellerAccount); s.status != "ok" || s.next == nil || !s.next.Equal(h.Now().Add(30*time.Minute)) {
		t.Errorf("after the backlog: %+v, want ok and due after the interval", s)
	}
	h.Advance(30 * time.Minute)
	syncNow(t, h)
	if n := h.Count(t, `SELECT count(*) FROM invoices.bank_transactions WHERE format = 'feed'`); n != 14 {
		t.Errorf("after the next read: %d feed lines, want still 14", n)
	}
	if n := h.Count(t, `SELECT coalesce(sum((ignored_kinds->>'debit')::int), 0) FROM invoices.bank_files WHERE format = 'feed'`); n != 1 {
		t.Errorf("the payment out was counted %d times across the batches, want once", n)
	}
}

// A window whose answer is too large is halved and asked again within the
// cycle; one day still too large fails the account with its own reason,
// never as a bank that could not be reached.
func TestBankFeedCursor_ATooLargeWindowIsHalved(t *testing.T) {
	t.Parallel()
	h, _ := syncHarness(t)
	inv := kidInvoice(t, h)
	feedReady(t, h)
	h.folio.AddTransaction(sellerAccount, incoming(txID(1), "2026-10-06", "1000.00", *inv.Kid))
	h.folio.Fail(foliotest.OpTransactions, foliotest.Failure{Oversized: true, Times: 1})
	syncNow(t, h)
	windows := transactionWindows(t, h)
	if want := []string{feedStart + "..2026-10-07", feedStart + "..2026-09-23", "2026-09-24..2026-10-07"}; !slices.Equal(windows, want) {
		t.Errorf("the reads = %v, want %v", windows, want)
	}
	if got := livePaymentsOf(t, h, inv.ID); !slices.Equal(got, []string{"1000.00 bank_feed"}) {
		t.Errorf("the invoice's payments = %v", got)
	}

	bad, _ := syncHarness(t)
	feedReady(t, bad)
	bad.folio.Fail(foliotest.OpTransactions, foliotest.Failure{Oversized: true})
	syncNow(t, bad)
	s := feedStateOf(t, bad, sellerAccount)
	if s.reason != "answer_too_large" || s.failures != 1 || !strings.Contains(s.detail, "one day is too large") || s.status != "ok" {
		t.Errorf("one day too large: %+v, want one failure with reason answer_too_large", s)
	}
	if got := transactionWindows(t, bad); got[len(got)-1] != feedStart+".."+feedStart {
		t.Errorf("the last read = %s, want the start day alone", got[len(got)-1])
	}
	if strings.Contains(bad.Logs(), "the bank could not be reached") {
		t.Error("a too-large day was logged as a bank that could not be reached")
	}
}

// Switching the feed on sets a start day and clears the cursor: the next
// read starts from the new start day.
func TestBankFeedCursor_ANewStartDayResetsTheCursor(t *testing.T) {
	t.Parallel()
	h, _ := syncHarness(t)
	feedReady(t, h)
	syncNow(t, h)
	if got := readThroughOf(t, h, sellerAccount); got != "2026-10-07" {
		t.Fatalf("read through %s, want 2026-10-07", got)
	}
	manager := feedManager(t, h)
	if res := switchFeed(manager, sellerAccount, false, ""); res.Status != http.StatusOK {
		t.Fatalf("switch off = %d %s", res.Status, res.Body)
	}
	switchedOn(t, h, sellerAccount, "2026-08-01")
	if got := readThroughOf(t, h, sellerAccount); got != "-" {
		t.Errorf("read through after a new start day = %s, want none", got)
	}
	syncNow(t, h)
	windows := transactionWindows(t, h)
	if got := windows[1]; got != "2026-08-01..2026-08-31" {
		t.Errorf("the first read after the new start day = %s, want from it", got)
	}
}
