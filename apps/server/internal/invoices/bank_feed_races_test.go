package invoices_test

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile/bankfiletest"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// TestBankFeed_TwoPutsRace: two connection PUTs at once. The first writes the
// row and parks before its commit; the second, its key verified, waits on the
// first's row — proved by pg_blocking_pids — and writes over it once the
// first commits. One row, the second's.
func TestBankFeed_TwoPutsRace(t *testing.T) {
	h := feedHarness(t, modtest.WithPoolMaxConns(2))
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)
	hook, parked, release := parkOnce(t)
	restore := invoices.SetBankFeedPutAfterWrite(hook)
	defer restore()

	first := startRequest(feedManager(t, h), http.MethodPut, bankFeedPath, folioKey())
	firstPID := parkedPID(t, "the first PUT", parked)
	secondManager, secondID := h.SignInUser(t, "invoices:access", "invoices:manage")
	second := startRequest(secondManager, http.MethodPut, bankFeedPath, folioKey())
	waiter := newWaiter(t, probeConn)
	if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{firstPID}) {
		t.Fatalf("the second PUT waits on %v, want the first PUT's %d", got, firstPID)
	}
	release.Close()

	finished(t, "the first PUT", first, http.StatusOK)
	finished(t, "the second PUT", second, http.StatusOK)
	if n := h.Count(t, `SELECT count(*) FROM invoices.bank_feed_connections`); n != 1 {
		t.Errorf("connections = %d, want one", n)
	}
	row, err := store.New(h.Pool()).GetBankFeedConnection(context.Background())
	if err != nil || row.ConnectedByUserID != secondID {
		t.Errorf("the stored connection = %+v, %v, want the second PUT's", row, err)
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("deadlocks = %d, want %d", after, before)
	}
}

// TestBankFeed_SwitchAgainstFormatPut: a switch and a format PUT on the same
// account queue on its bank import row, and the format PUT judges the feed
// after the switch committed. A switch off parked under the row lets the
// format PUT through once it commits; a switch on parked under it makes the
// format PUT's change away from feed a 409 bank_feed_enabled.
func TestBankFeed_SwitchAgainstFormatPut(t *testing.T) {
	h := feedHarness(t, modtest.WithPoolMaxConns(2))
	imported(t, importer(t, h), camtOn("OLD-1", olderAccount, 5, bankfiletest.CamtTx{AmountMinor: 125000, KID: "0010017"}))
	connect(t, h)
	switchedOn(t, h, sellerAccount, "")
	probeConn := ownConn(t, h)
	manager := feedManager(t, h)

	// A switch off, parked: the format PUT waits, then changes the format.
	hook, parked, release := parkOnce(t)
	restore := invoices.SetBankFeedSwitchAfterLock(hook)
	off := startRequest(manager, http.MethodPut, bankFeedAccountsPath+"/"+sellerAccount, map[string]any{"enabled": false})
	switchPID := parkedPID(t, "the switch off", parked)
	format := startRequest(manager, http.MethodPut, bankAccountsPath+"/"+sellerAccount+"/format", map[string]any{"format": "camt054"})
	waiter := newWaiter(t, probeConn)
	if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{switchPID}) {
		t.Fatalf("the format PUT waits on %v, want the switch's %d", got, switchPID)
	}
	release.Close()
	finished(t, "the switch off", off, http.StatusOK)
	finished(t, "the format PUT after the switch off", format, http.StatusOK)
	restore()

	// A switch on, parked: the format PUT waits, then finds the feed on.
	hook, parked, release = parkOnce(t)
	restore = invoices.SetBankFeedSwitchAfterLock(hook)
	defer restore()
	on := startRequest(manager, http.MethodPut, bankFeedAccountsPath+"/"+olderAccount, map[string]any{"enabled": true})
	switchPID = parkedPID(t, "the switch on", parked)
	format = startRequest(manager, http.MethodPut, bankAccountsPath+"/"+olderAccount+"/format", map[string]any{"format": "ocr"})
	waiter = newWaiter(t, probeConn, waiter)
	if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{switchPID}) {
		t.Fatalf("the format PUT waits on %v, want the switch's %d", got, switchPID)
	}
	release.Close()
	finished(t, "the switch on", on, http.StatusOK)
	res := finished(t, "the format PUT after the switch on", format, http.StatusConflict)
	if code := problemOf(t, res).Code; code != "bank_feed_enabled" {
		t.Errorf("the format PUT's code = %q, want bank_feed_enabled", code)
	}
	if row := importAccount(t, h, olderAccount); row.Format != "feed" {
		t.Errorf("the format = %s, want feed", row.Format)
	}
}

// TestBankFeed_SwitchOnAgainstDelete: a switch on and a DELETE, both orders.
// A switch on parked under its locks — the account's import row, the
// connection shared — makes the DELETE wait on it, and the DELETE then
// switches the account off with the rest. A DELETE parked before its commit
// makes the switch on wait on the connection, find it gone, and answer 409
// bank_feed_not_connected, leaving no import row behind.
func TestBankFeed_SwitchOnAgainstDelete(t *testing.T) {
	h := feedHarness(t, modtest.WithPoolMaxConns(2))
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)
	manager := feedManager(t, h)
	connect(t, h)

	// The switch first.
	hook, parked, release := parkOnce(t)
	restore := invoices.SetBankFeedSwitchAfterLock(hook)
	on := startRequest(manager, http.MethodPut, bankFeedAccountsPath+"/"+sellerAccount, map[string]any{"enabled": true})
	switchPID := parkedPID(t, "the switch on", parked)
	del := startRequest(manager, http.MethodDelete, bankFeedPath, nil)
	waiter := newWaiter(t, probeConn)
	if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{switchPID}) {
		t.Fatalf("the DELETE waits on %v, want the switch's %d", got, switchPID)
	}
	release.Close()
	finished(t, "the switch on", on, http.StatusOK)
	finished(t, "the DELETE after the switch", del, http.StatusNoContent)
	restore()
	if n := h.Count(t, `SELECT count(*) FROM invoices.bank_feed_accounts WHERE enabled`); n != 0 {
		t.Errorf("enabled accounts after the DELETE = %d, want none", n)
	}

	// The DELETE first, on an account no file named.
	connect(t, h)
	hook, parked, release = parkOnce(t)
	restore = invoices.SetBankFeedDeleteAfterWrite(hook)
	defer restore()
	del = startRequest(manager, http.MethodDelete, bankFeedPath, nil)
	deletePID := parkedPID(t, "the DELETE", parked)
	on = startRequest(manager, http.MethodPut, bankFeedAccountsPath+"/"+olderAccount, map[string]any{"enabled": true})
	waiter = newWaiter(t, probeConn, waiter)
	if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{deletePID}) {
		t.Fatalf("the switch on waits on %v, want the DELETE's %d", got, deletePID)
	}
	release.Close()
	finished(t, "the DELETE", del, http.StatusNoContent)
	res := finished(t, "the switch on after the DELETE", on, http.StatusConflict)
	if code := problemOf(t, res).Code; code != "bank_feed_not_connected" {
		t.Errorf("the switch on's code = %q, want bank_feed_not_connected", code)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.bank_import_accounts WHERE account = $1`, olderAccount); n != 0 {
		t.Errorf("import rows of the account = %d, want none", n)
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("deadlocks = %d, want %d", after, before)
	}
}
