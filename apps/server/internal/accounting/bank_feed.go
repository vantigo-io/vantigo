package accounting

// The bank feed as a statement's third source (A29): worker
// accounting.bankfeed reads each bank account whose source is feed through
// contracts.BankFeed — Invoices' one connection and sealed key — on the
// feed's own interval, every direction and kind: debits, card purchases and
// transfers between the company's accounts too, which Invoices does not
// keep. A cycle, per account due:
//
//  1. claims the account's lease in one short transaction;
//  2. reads the bank with no transaction open, from the later of the start
//     day and the latest line it wrote less ten days, through today — the
//     overlap is read again, since a bank may book a transaction on an
//     earlier day than it first showed it;
//  3. writes the lines it does not have, the bank's transaction id as each
//     line's id, as one statement of source feed, under the claim;
//  4. releases the claim with what the bank said and when the account is
//     due again: a refused key or a rate limit is recorded on the account
//     and shown on its page, and the account read after the bank's wait.
//
// With the feed not composed — Invoices off or INVOICES_BANK_FEED_ENABLED off
// — the worker does nothing, and nothing is read before the books are set up.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/accounting/bankstmt"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
	"github.com/vantigo-io/vantigo/server/internal/bankfeed"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

const (
	// bankFeedWorkerName is the worker's name in the runner's logs.
	bankFeedWorkerName = "accounting.bankfeed"
	// bankFeedPoll is how often the worker looks for an account that is due.
	bankFeedPoll = time.Minute
	// bankFeedDefaultInterval is an account's cadence when the feed names
	// none.
	bankFeedDefaultInterval = 30 * time.Minute
	// bankFeedOverlapDays is how far back from the latest line a read starts.
	bankFeedOverlapDays = 10
	// bankFeedPerRun bounds the accounts one run reads.
	bankFeedPerRun = 50
	// bankFeedReleaseTimeout bounds the release, on a context the worker's
	// shutdown does not reach.
	bankFeedReleaseTimeout = 10 * time.Second
)

// bankFeedLease is the lease a claim takes. A test may shorten it.
var bankFeedLease = 5 * time.Minute

// errFeedLeaseLost is a write that found the account no longer under the
// claim.
var errFeedLeaseLost = errors.New("accounting: the bank feed's lease was lost")

// The feed's states on a bank account.
const (
	feedOK           = "ok"
	feedUnauthorized = "unauthorized"
	feedRateLimited  = "rate_limited"
	feedFailing      = "failing"
	feedOff          = "off"
)

// BankFeedWorker reads the bank accounts whose source is the feed. It
// implements worker.Worker.
type BankFeedWorker struct {
	deps module.Deps
}

var _ worker.Worker = (*BankFeedWorker)(nil)

// NewBankFeedWorker builds the worker over worker mode's Deps: the pool, the
// clock, the business calendar and Invoices' bank feed, which module.Workers
// resolves.
func NewBankFeedWorker(d module.Deps) *BankFeedWorker { return &BankFeedWorker{deps: d} }

// Name identifies this worker in the runner's logs.
func (w *BankFeedWorker) Name() string { return bankFeedWorkerName }

// Interval is the poll cadence; each account is due on the feed's own.
func (w *BankFeedWorker) Interval() time.Duration { return bankFeedPoll }

// Run runs a cycle, sleeps the interval, and repeats until ctx is done.
func (w *BankFeedWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(bankFeedPoll)
	defer ticker.Stop()
	for {
		if err := w.RunOnce(ctx); err != nil && ctx.Err() == nil {
			w.logger().ErrorContext(ctx, "accounting: the bank feed failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (w *BankFeedWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}

// RunOnce reads every account that is due, one claim at a time.
func (w *BankFeedWorker) RunOnce(ctx context.Context) error {
	if w.deps.Pool == nil || w.deps.BankFeed == nil {
		return nil
	}
	if _, err := store.New(w.deps.Pool).GetSettings(ctx); noRows(err) {
		return nil
	} else if err != nil {
		return err
	}
	now := clockOf(w.deps)
	var errs []error
	for range bankFeedPerRun {
		lease := uuid.New()
		claimed, err := store.New(w.deps.Pool).ClaimFeedAccount(ctx, store.ClaimFeedAccountParams{LeaseID: &lease,
			LeaseUntil: ptr(now().Add(bankFeedLease)), Now: now()})
		if noRows(err) {
			break
		}
		if err != nil {
			return errors.Join(append(errs, err)...)
		}
		if err := w.sync(ctx, claimed, lease); err != nil {
			errs = append(errs, fmt.Errorf("bank account %d: %w", claimed.ID, err))
		}
	}
	return errors.Join(errs...)
}

// feedOutcome is how a read ended, for the release.
type feedOutcome struct {
	status, detail string
	next           time.Time
	synced         bool
}

// sync reads one claimed account and releases it.
func (w *BankFeedWorker) sync(ctx context.Context, account store.ClaimFeedAccountRow, lease uuid.UUID) error {
	now := clockOf(w.deps)
	outcome, runErr := w.read(ctx, account, lease)
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bankFeedReleaseTimeout)
	defer cancel()
	if err := store.New(w.deps.Pool).ReleaseFeedAccount(releaseCtx, store.ReleaseFeedAccountParams{ID: account.ID, LeaseID: &lease,
		FeedStatus: &outcome.status, FeedDetail: truncate(outcome.detail, 500), NextSyncAt: &outcome.next, Synced: outcome.synced,
		Now: now()}); err != nil {
		return errors.Join(runErr, err)
	}
	return runErr
}

// read is one claimed read: the bank asked with no transaction open, then
// the new lines written under the claim. The error is the module's own
// failure; what the bank said is the outcome.
func (w *BankFeedWorker) read(ctx context.Context, account store.ClaimFeedAccountRow, lease uuid.UUID) (feedOutcome, error) {
	now := clockOf(w.deps)
	feed := w.deps.BankFeed
	interval := bankFeedDefaultInterval
	failed := func(status, detail string, after time.Duration) feedOutcome {
		return feedOutcome{status: status, detail: detail, next: now().Add(max(after, interval))}
	}
	st, err := feed.Status(ctx)
	if err != nil {
		return failed(feedFailing, "The bank connection in Invoices could not be read.", 0), nil
	}
	if st.Interval > 0 {
		interval = st.Interval
	}
	switch {
	case !st.Connected:
		return failed(feedOff, "No bank is connected in Invoices' Bank integration.", 0), nil
	case st.Refused:
		return failed(feedUnauthorized, "The bank refused the key Invoices holds: store a new one in Invoices' Bank integration.", 0), nil
	}
	today, err := w.today(ctx)
	if err != nil {
		return failed(feedFailing, "The business day could not be read.", 0), err
	}
	from := deref(account.FeedStartDay)
	latest, err := store.New(w.deps.Pool).LatestFeedLineDay(ctx, account.ID)
	if err != nil {
		return failed(feedFailing, "The account's lines could not be read.", 0), err
	}
	if d, err := time.Parse(time.DateOnly, latest); err == nil && d.AddDate(0, 0, -bankFeedOverlapDays).After(from) {
		from = d.AddDate(0, 0, -bankFeedOverlapDays)
	}
	if from.After(today) {
		return feedOutcome{status: feedOK, next: now().Add(interval), synced: true}, nil
	}
	// The bank, with no transaction open (A29, the lease workers' rule).
	txs, err := feed.Transactions(ctx, account.AccountNumber, from, today)
	if err != nil {
		var limited *bankfeed.ErrRateLimited
		switch {
		case errors.Is(err, bankfeed.ErrUnauthorized):
			return failed(feedUnauthorized, "The bank refused the key Invoices holds: store a new one in Invoices' Bank integration.", 0), nil
		case errors.As(err, &limited):
			return failed(feedRateLimited, "The bank is limiting requests; the account is read again after its wait.", limited.RetryAfter), nil
		case errors.Is(err, contracts.ErrBankFeedNotConnected):
			return failed(feedOff, "No bank is connected in Invoices' Bank integration.", 0), nil
		}
		w.logger().WarnContext(ctx, "accounting: the bank feed could not be read", "bank_account_id", account.ID, "error", err.Error())
		return failed(feedFailing, "The bank could not be read; it is tried again.", 0), nil
	}
	lines, skipped := feedLines(txs, today)
	ids := make([]string, 0, len(lines))
	for _, l := range lines {
		ids = append(ids, l.BankID)
	}
	have, err := store.New(w.deps.Pool).ExistingBankIDs(ctx, store.ExistingBankIDsParams{BankAccountID: account.ID, Ids: ids})
	if err != nil {
		return failed(feedFailing, "The account's lines could not be read.", 0), err
	}
	known := map[string]bool{}
	for _, id := range have {
		known[id] = true
	}
	st2 := bankstmt.Statement{Account: account.AccountNumber, Currency: currency, From: from, To: today}
	for _, l := range lines {
		if !known[l.BankID] {
			st2.Lines = append(st2.Lines, l)
		}
	}
	detail := ""
	if skipped > 0 {
		detail = fmt.Sprintf("%d transactions the bank could not place (a direction unknown, another currency) were left out.", skipped)
	}
	if len(st2.Lines) > 0 {
		srv := &server{deps: w.deps}
		if _, err := srv.writeStatement(ctx, account.ID, account.AccountNumber, st2, statementFeed, nil, map[string]int{}, nil, &lease); err != nil {
			if errors.Is(err, errFeedLeaseLost) {
				return feedOutcome{status: feedOK, next: now()}, nil
			}
			return failed(feedFailing, "The lines could not be written; they are read again.", 0), err
		}
	}
	return feedOutcome{status: feedOK, detail: detail, next: now().Add(interval), synced: true}, nil
}

// today is the business calendar's day.
func (w *BankFeedWorker) today(ctx context.Context) (time.Time, error) {
	return (&server{deps: w.deps}).today(ctx)
}

// feedLines is the transactions as statement lines, signed by direction, the
// bank's id each line's: a transaction whose direction is unknown, in
// another currency, of no amount or booked after today is left out and
// counted, never guessed.
func feedLines(txs []bankfeed.Transaction, today time.Time) ([]bankstmt.Line, int) {
	var out []bankstmt.Line
	skipped := 0
	for _, t := range txs {
		if t.ID == "" || t.AmountMinor <= 0 || (t.Currency != "" && t.Currency != currency) || t.BookedOn.After(today) {
			skipped++
			continue
		}
		amount := big.NewRat(t.AmountMinor, 100)
		l := bankstmt.Line{BookedOn: t.BookedOn, Amount: amount, Text: clipText(t.Text, 1000), KID: clipText(t.KID, 25),
			BankID: clipText(t.ID, 140), Kind: feedKind(t.Kind)}
		switch t.Direction {
		case bankfeed.DirectionCredit:
			l.CounterpartyName, l.CounterpartyAccount = clipText(t.DebtorName, 140), clipText(t.DebtorAccount, 34)
			if bban, ok := bankstmt.NormaliseAccount(l.CounterpartyAccount); ok {
				l.CounterpartyAccount = bban
			}
		case bankfeed.DirectionDebit:
			l.Amount = neg(amount)
		default:
			skipped++
			continue
		}
		out = append(out, l)
	}
	return out, skipped
}

// feedKind is the port's kind as a line's.
func feedKind(k bankfeed.Kind) string {
	switch k {
	case bankfeed.KindTransfer:
		return bankstmt.KindTransfer
	case bankfeed.KindCard:
		return bankstmt.KindCard
	case bankfeed.KindInternal:
		return bankstmt.KindInternal
	}
	return bankstmt.KindOther
}
