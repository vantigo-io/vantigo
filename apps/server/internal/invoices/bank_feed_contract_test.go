package invoices_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/bankfeed"
	"github.com/vantigo-io/vantigo/server/internal/bankfeed/folio/foliotest"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
)

// contracts.BankFeed (accounting design A29): the one connection and its
// sealed key are this module's; a ledger reads through them what the port
// answers — every account and every direction and kind — and nothing at all
// while the instance switch is off.
func TestBankFeedContract(t *testing.T) {
	ctx := context.Background()
	off := bankHarness(t)
	if feed := invoices.Module().BankFeed(off.Deps()); feed != nil {
		t.Fatalf("with INVOICES_BANK_FEED_ENABLED off the contract is %v, want nil", feed)
	}

	h := feedHarness(t)
	feed := invoices.Module().BankFeed(h.Deps())
	if feed == nil {
		t.Fatal("no contract with the feed on")
	}
	st, err := feed.Status(ctx)
	if err != nil || st.Connected || st.Interval != 30*time.Minute {
		t.Fatalf("before a connection: %+v %v", st, err)
	}
	if _, err := feed.Accounts(ctx); !errors.Is(err, contracts.ErrBankFeedNotConnected) {
		t.Fatalf("accounts before a connection: %v", err)
	}
	connect(t, h)
	if st, err = feed.Status(ctx); err != nil || !st.Connected || st.Refused || st.Provider != "folio" {
		t.Fatalf("connected: %+v %v", st, err)
	}
	accounts, err := feed.Accounts(ctx)
	if err != nil || len(accounts) != 5 || accounts[2].Kind != bankfeed.AccountCard || accounts[3].Kind != bankfeed.AccountTax {
		t.Fatalf("accounts = %+v %v", accounts, err)
	}
	h.folio.AddTransaction(strangerAccount, foliotest.Transaction{ID: "tx-in", EventID: "e1", BookingDate: "2026-09-10",
		Description: "Innbetaling", KID: "0000101012", Debtor: &foliotest.Party{Name: "Acme AS", AccountNumber: "70580001112"},
		Creditor:          &foliotest.Party{Name: "Fjord Bygg AS", AccountNumber: strangerAccount},
		TransactionAmount: foliotest.Amount{Amount: "1250.00", Currency: "NOK"}, Complete: true})
	h.folio.AddTransaction(strangerAccount, foliotest.Transaction{ID: "tx-out", EventID: "e2", BookingDate: "2026-09-11",
		Description: "Husleie", Creditor: &foliotest.Party{Name: "Gårdeier AS", AccountNumber: "86011117947"},
		Debtor:            &foliotest.Party{Name: "Fjord Bygg AS", AccountNumber: strangerAccount},
		TransactionAmount: foliotest.Amount{Amount: "8000.00", Currency: "NOK"}, Complete: true})
	txs, err := feed.Transactions(ctx, strangerAccount, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC))
	if err != nil || len(txs) != 2 {
		t.Fatalf("transactions = %+v %v", txs, err)
	}
	if txs[0].Direction != bankfeed.DirectionCredit || txs[0].AmountMinor != 125000 || txs[1].Direction != bankfeed.DirectionDebit {
		t.Fatalf("directions = %+v", txs)
	}
	// A refused key is recorded as Invoices' own calls record it.
	h.folio.Fail(foliotest.OpTransactions, foliotest.Failure{Status: http.StatusUnauthorized})
	if _, err := feed.Transactions(ctx, strangerAccount, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)); !errors.Is(err, bankfeed.ErrUnauthorized) {
		t.Fatalf("refused: %v", err)
	}
	if st, err = feed.Status(ctx); err != nil || !st.Refused {
		t.Fatalf("after the refusal: %+v %v", st, err)
	}
}
