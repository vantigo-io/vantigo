package invoices

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/bankfeed"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is the module's contracts.BankFeed (accounting design A29): the
// one connection to the bank and its sealed key stay this module's (bank
// feed design F2, F3), and a ledger reads through them what the port answers
// — every account, and every direction and kind of an account's
// transactions — which this module itself keeps only the incoming payments
// of. Each call goes through bankFeed, so it is reported as a contract call
// and a 401 or 403 records the key as refused, as for this module's own
// calls; it opens no transaction and stores nothing.

// bankFeedProvider is this module's contracts.BankFeed.
type bankFeedProvider struct {
	s *server
}

var _ contracts.BankFeed = (*bankFeedProvider)(nil)

// newBankFeed is Module's BankFeed: nil — no contract at all — while
// INVOICES_BANK_FEED_ENABLED is off, so nothing composed can reach a bank the
// operator switched off.
func newBankFeed(d module.Deps) contracts.BankFeed {
	if d.Config == nil || !d.Config.InvoicesBankFeedEnabled {
		return nil
	}
	return &bankFeedProvider{s: &server{deps: d}}
}

// Status is the connection as this module holds it, read on the pool.
func (p *bankFeedProvider) Status(ctx context.Context) (contracts.BankFeedStatus, error) {
	// The cadence this module's own sync reads on (INVOICES_BANK_FEED_INTERVAL,
	// F5), which a reader of its own keeps.
	out := contracts.BankFeedStatus{Interval: p.s.deps.Config.InvoicesBankFeedInterval}
	row, err := store.New(p.s.deps.Pool).GetBankFeedConnection(ctx)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return out, nil
	case err != nil:
		return out, fmt.Errorf("invoices: read the bank connection: %w", err)
	}
	out.Connected, out.Refused, out.Provider = true, row.Status == "unauthorized", row.Provider
	return out, nil
}

// feed is the stored connection's feed, its errors the contract's. The
// bank limits the key, not a reader: while the bank's last 429 to this
// module's own sync still holds (rate_limited_until, 00179) no call is made,
// and the wait that is left is answered as the bank's, so a reader backs off
// with the sync rather than spend the requests the bank refused it.
func (p *bankFeedProvider) feed(ctx context.Context) (bankfeed.Feed, error) {
	now := p.s.deps.Clock()
	switch until, err := store.New(p.s.deps.Pool).BankFeedRateLimitedUntil(ctx, now); {
	case err == nil && until != nil:
		return nil, &bankfeed.ErrRateLimited{RetryAfter: until.Sub(now)}
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return nil, fmt.Errorf("invoices: read the bank's wait: %w", err)
	}
	feed, err := p.s.bankFeed(ctx)
	switch {
	case errors.Is(err, errBankFeedNotConnected), errors.Is(err, errBankFeedKeyUnreadable), errors.Is(err, errBankFeedSwitchedOff):
		return nil, fmt.Errorf("%w: %w", contracts.ErrBankFeedNotConnected, err)
	case err != nil:
		return nil, err
	}
	return feed, nil
}

// Accounts lists the accounts the stored key reads, every kind.
func (p *bankFeedProvider) Accounts(ctx context.Context) ([]bankfeed.Account, error) {
	feed, err := p.feed(ctx)
	if err != nil {
		return nil, err
	}
	return feed.Accounts(ctx)
}

// Transactions answers an account's booked transactions, every direction
// and kind, as the bank answered them.
func (p *bankFeedProvider) Transactions(ctx context.Context, account string, from, to time.Time) ([]bankfeed.Transaction, error) {
	feed, err := p.feed(ctx)
	if err != nil {
		return nil, err
	}
	return feed.Transactions(ctx, account, from, to)
}
