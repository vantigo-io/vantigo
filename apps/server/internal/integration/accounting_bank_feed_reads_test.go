package integration_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/accounting"
	"github.com/vantigo-io/vantigo/server/internal/bankfeed/folio/foliotest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Accounting's feed worker reads in windows (accounting design A29): a start
// day far back is read forward a bounded window at a time, a window the bank
// answers too large halved down to one day, its progress kept per window, so
// later cycles go on where one stopped — Folio's fake behind Invoices'
// contracts.BankFeed, the worker built by module.Workers.

// feedWindowsStory is one installation with the operating account read
// through the feed from startDay, every fifth day booked through lastDay.
type feedWindowsStory struct {
	h        *modtest.Harness
	c        *modtest.Client
	folio    *foliotest.Server
	feed     *accounting.BankFeedWorker
	userID   uuid.UUID
	bankID   int64
	lastDay  time.Time
	expected map[string]bool
}

func newFeedWindowsStory(t *testing.T, daysBack int) *feedWindowsStory {
	t.Helper()
	names := []string{modCustomers, modInvoices, modAccounting}
	folio := foliotest.New(t)
	folio.SetAccounts(foliotest.Account{AccountNumber: receivablesAccount, Name: "Drift", Type: "Operational", Balance: "0.00"})
	h, c, userID, _ := installInvoices(t, names, []string{"accounting:read", "accounting:post", "accounting:close"},
		modtest.WithEnv("INVOICES_BANK_FEED_ENABLED", "1"), modtest.WithEnv("INVOICES_FOLIO_BASE_URL", folio.URL()),
		modtest.WithTransport(folio.Transport()))
	s := &feedWindowsStory{h: h, c: c, folio: folio, userID: userID, expected: map[string]bool{}}
	// The day before today in every zone, so the business day holds it.
	s.lastDay = time.Date(h.Now().Year(), h.Now().Month(), h.Now().Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	start := s.lastDay.AddDate(0, 0, -daysBack)
	for d, i := start, 0; !d.After(s.lastDay); d, i = d.AddDate(0, 0, 5), i+1 {
		id := fmt.Sprintf("tx-%03d", i)
		s.expected[id] = true
		folio.AddTransaction(receivablesAccount, foliotest.Transaction{ID: id, EventID: "e-" + id, BookingDate: d.Format(time.DateOnly),
			Description: "Husleie", Debtor: &foliotest.Party{Name: "Kraft-Verket AS", AccountNumber: receivablesAccount},
			Creditor:          &foliotest.Party{Name: "Gårdeier AS"},
			TransactionAmount: foliotest.Amount{Amount: "100.00", Currency: "NOK"}, Complete: true})
	}
	okJSON(t, c, http.MethodPut, invoicesBase+"/bank-feed", map[string]any{"provider": "folio", "apiKey": foliotest.APIKey}, nil)
	okJSON(t, c, http.MethodPost, accountingBase+"/setup", map[string]any{"booksStart": "2024-01-01", "vatTerm": "bimonthly", "language": "en"}, nil)
	var accounts struct {
		Items []struct {
			ID     int32  `json:"id"`
			Number string `json:"number"`
		} `json:"items"`
	}
	okJSON(t, c, http.MethodGet, accountingBase+"/accounts", nil, &accounts)
	ledger := int32(0)
	for _, a := range accounts.Items {
		if a.Number == "1920" {
			ledger = a.ID
		}
	}
	var bank struct {
		ID int64 `json:"id"`
	}
	okJSON(t, c, http.MethodPost, accountingBase+"/bank/accounts", map[string]any{"name": "Drift", "accountNumber": receivablesAccount,
		"ledgerAccountId": ledger}, &bank)
	s.bankID = bank.ID
	okJSON(t, c, http.MethodPut, fmt.Sprintf("%s/bank/accounts/%d/source", accountingBase, bank.ID),
		map[string]any{"source": "feed", "startDay": s.lastDay.Format(time.DateOnly)}, nil)
	// Further back than a person may choose (13 months): the windows' subject.
	h.Exec(t, `UPDATE accounting.bank_accounts SET feed_start_day = $1 WHERE id = $2`, start, bank.ID)
	_, s.feed = accountingWorkers(t, h, names)
	return s
}

// cycle runs one of the worker's runs.
func (s *feedWindowsStory) cycle(t *testing.T) {
	t.Helper()
	if err := s.feed.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// caughtUp reports whether the cursor reached the last booked day.
func (s *feedWindowsStory) caughtUp(t *testing.T) bool {
	t.Helper()
	return modtest.One[bool](t, s.h, `SELECT coalesce(feed_read_through >= $1, false) FROM accounting.bank_accounts WHERE id = $2`,
		s.lastDay, s.bankID)
}

// lines checks the account holds every booked transaction once, none other.
func (s *feedWindowsStory) lines(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	rows, err := s.h.Pool().Query(ctx, `SELECT bank_id, count(*)::int FROM accounting.statement_lines WHERE bank_account_id = $1 GROUP BY bank_id`, s.bankID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			t.Fatal(err)
		}
		if n != 1 || !s.expected[id] {
			t.Errorf("line %s written %d times (expected: %v)", id, n, s.expected[id])
		}
		got[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for id := range s.expected {
		if !got[id] {
			t.Errorf("transaction %s never arrived", id)
		}
	}
}

// feedState is the account's feed status and detail.
func (s *feedWindowsStory) feedState(t *testing.T) (string, string) {
	t.Helper()
	return modtest.One[string](t, s.h, `SELECT coalesce(feed_status, '') FROM accounting.bank_accounts WHERE id = $1`, s.bankID),
		modtest.One[string](t, s.h, `SELECT feed_detail FROM accounting.bank_accounts WHERE id = $1`, s.bankID)
}

// A start day 400 days back, the bank answering too large for a window over
// ten days: every line arrives, once, over a few cycles, each cycle's calls
// bounded; once caught up the overlap is read again and nothing doubles.
func TestAccounting_TheBankFeedReadsFarBackInWindows(t *testing.T) {
	s := newFeedWindowsStory(t, 400)
	s.folio.Fail(foliotest.OpTransactions, foliotest.Failure{Oversized: true, OverDays: 10})
	cycles := 0
	for ; cycles < 30 && !s.caughtUp(t); cycles++ {
		before := len(s.folio.Requests())
		s.cycle(t)
		if n := len(s.folio.Requests()) - before; n == 0 || n > 8 {
			t.Fatalf("cycle %d made %d calls, want 1 to 8", cycles, n)
		}
		if st, detail := s.feedState(t); st != "ok" {
			t.Fatalf("cycle %d: status %q %q", cycles, st, detail)
		}
		s.h.Advance(time.Minute)
	}
	if !s.caughtUp(t) || cycles < 2 {
		t.Fatalf("after %d cycles the account is not read through %s", cycles, s.lastDay.Format(time.DateOnly))
	}
	s.lines(t)
	// Caught up, the next cycle is an interval on and reads the overlap.
	before := len(s.folio.Requests())
	s.cycle(t)
	if n := len(s.folio.Requests()) - before; n != 0 {
		t.Fatalf("a caught-up account read again before its interval: %d calls", n)
	}
	s.h.Advance(31 * time.Minute)
	s.cycle(t)
	if n := len(s.folio.Requests()) - before; n == 0 {
		t.Fatal("the overlap was not read again")
	}
	s.lines(t)
}

// A single day the bank answers too large is recorded on the account as
// too_large, and not asked again before the interval.
func TestAccounting_TheBankFeedRecordsADayTooLarge(t *testing.T) {
	s := newFeedWindowsStory(t, 20)
	s.folio.Fail(foliotest.OpTransactions, foliotest.Failure{Oversized: true})
	before := len(s.folio.Requests())
	s.cycle(t)
	// About three weeks, then 11, 6, 3, 2 and 1 days: six calls.
	if n := len(s.folio.Requests()) - before; n != 6 {
		t.Fatalf("calls = %d, want the window halved to one day", n)
	}
	st, detail := s.feedState(t)
	if st != "too_large" || detail == "" {
		t.Fatalf("status = %q %q, want too_large", st, detail)
	}
	before = len(s.folio.Requests())
	s.h.Advance(time.Minute)
	s.cycle(t)
	if n := len(s.folio.Requests()) - before; n != 0 {
		t.Fatalf("a day too large was asked again at once: %d calls", n)
	}
	// The bank answers again: read through.
	s.folio.Clear(foliotest.OpTransactions)
	s.h.Advance(31 * time.Minute)
	s.cycle(t)
	if st, _ := s.feedState(t); st != "ok" || !s.caughtUp(t) {
		t.Fatalf("after the bank answered: %q, caught up %v", st, s.caughtUp(t))
	}
	s.lines(t)
}

// A worker that died mid-way — its lease held, a window written and the
// cursor not yet moved — is taken over once the lease runs out: the next
// cycles read from the cursor, the window again, and nothing doubles.
func TestAccounting_TheBankFeedResumesAfterACrash(t *testing.T) {
	s := newFeedWindowsStory(t, 400)
	s.folio.Fail(foliotest.OpTransactions, foliotest.Failure{Oversized: true, OverDays: 10})
	s.cycle(t)
	s.h.Advance(time.Minute)
	s.cycle(t)
	if s.caughtUp(t) {
		t.Fatal("caught up in two cycles; the crash would be after the end")
	}
	// The crash: the cursor eight days back from the window it wrote, and a
	// lease no live claim holds.
	s.h.Exec(t, `UPDATE accounting.bank_accounts SET feed_read_through = feed_read_through - 8,
		feed_lease_id = $1, feed_lease_until = $2, feed_next_sync_at = $3 WHERE id = $4`,
		uuid.New(), s.h.Now().Add(5*time.Minute), s.h.Now(), s.bankID)
	before := len(s.folio.Requests())
	s.cycle(t)
	if n := len(s.folio.Requests()) - before; n != 0 {
		t.Fatalf("the account was read under another's lease: %d calls", n)
	}
	s.h.Advance(6 * time.Minute)
	for i := 0; i < 30 && !s.caughtUp(t); i++ {
		s.cycle(t)
		s.h.Advance(time.Minute)
	}
	if !s.caughtUp(t) {
		t.Fatal("the account was not read through after the crash")
	}
	s.lines(t)
}

// While Invoices' own sync holds the account's claim, the contract answers
// it busy with no call to the bank: the account is skipped, no error is
// recorded, and it is read once the claim is over.
func TestAccounting_TheBankFeedWaitsForInvoicesSync(t *testing.T) {
	s := newFeedWindowsStory(t, 20)
	s.h.Exec(t, `INSERT INTO invoices.bank_feed_accounts (account, enabled, start_day, lease_id, lease_until, enabled_by_user_id, updated_at)
		VALUES ($1, true, $2, 'invoices-sync', $3, $4, $5)`,
		receivablesAccount, s.lastDay, s.h.Now().Add(3*time.Minute), s.userID, s.h.Now())
	before := len(s.folio.Requests())
	s.cycle(t)
	if n := len(s.folio.Requests()) - before; n != 0 {
		t.Fatalf("%d calls reached the bank while Invoices' sync read the account", n)
	}
	if st, detail := s.feedState(t); st != "" || detail != "" {
		t.Fatalf("a busy account recorded %q %q", st, detail)
	}
	next := modtest.One[time.Time](t, s.h, `SELECT feed_next_sync_at FROM accounting.bank_accounts WHERE id = $1`, s.bankID)
	if next.Before(s.h.Now().Add(2*time.Minute)) || next.After(s.h.Now().Add(5*time.Minute)) {
		t.Fatalf("a busy account is due at %s, want about when the sync's claim ends", next)
	}
	s.h.Exec(t, `UPDATE invoices.bank_feed_accounts SET lease_id = NULL, lease_until = NULL WHERE account = $1`, receivablesAccount)
	s.h.Advance(4 * time.Minute)
	s.cycle(t)
	if st, _ := s.feedState(t); st != "ok" || !s.caughtUp(t) {
		t.Fatalf("after the sync's claim: %q, caught up %v", st, s.caughtUp(t))
	}
	s.lines(t)
}
