package accounting

// The bank feed as a statement's third source (A29): worker
// accounting.bankfeed reads each bank account whose source is feed through
// contracts.BankFeed — Invoices' one connection and sealed key — on the
// feed's own interval, every direction and kind: debits, card purchases and
// transfers between the company's accounts too, which Invoices does not
// keep. A cycle, per account due:
//
//  1. claims the account's lease in one short transaction;
//  2. reads the bank with no transaction open, a window of at most 31 days
//     at a time — forward from the day after its read cursor (or the start
//     day) through today, then the ten days before the cursor again, since a
//     bank may book a transaction on an earlier day than it first showed it.
//     A window the bank answers too large is halved, down to one day; a day
//     still too large is recorded as too_large. At most eight calls a cycle:
//     an account far behind is due again at the next poll, where it stopped;
//  3. writes each window's lines it does not have, the bank's transaction id
//     as each line's id, as one statement of source feed, and moves the
//     cursor to the window's end, under the claim;
//  4. releases the claim with what the bank said and when the account is
//     due again: a refused key, a rate limit or a day too large is recorded
//     on the account and shown on its page, and the account read after the
//     bank's wait. While Invoices' own sync is reading the same account the
//     contract answers it busy: the account is read again shortly, its state
//     as it was.
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
	// bankFeedWindowDays is the most days one call asks the bank for, as
	// Invoices' own sync asks.
	bankFeedWindowDays = 31
	// bankFeedReleaseTimeout bounds the release, on a context the worker's
	// shutdown does not reach.
	bankFeedReleaseTimeout = 10 * time.Second
)

// bankFeedLease is the lease a claim takes. A test may shorten it.
var bankFeedLease = 5 * time.Minute

// bankFeedCallsPerCycle is the calls to the bank one account's cycle makes
// at most, as Invoices' own sync makes, so one account far behind neither
// outlives its lease nor holds the others up.
var bankFeedCallsPerCycle = 8

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
	feedTooLarge     = "too_large"
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
		cycleCtx, endCycle := worker.StartCycle(ctx, w.Name())
		err := w.RunOnce(cycleCtx)
		endCycle(err)
		if err != nil && ctx.Err() == nil {
			w.logger().ErrorContext(cycleCtx, "accounting: the bank feed failed", "error", err)
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
	status := &outcome.status
	if outcome.status == "" {
		// Busy before the account was ever read: no state yet.
		status = nil
	}
	if err := store.New(w.deps.Pool).ReleaseFeedAccount(releaseCtx, store.ReleaseFeedAccountParams{ID: account.ID, LeaseID: &lease,
		FeedStatus: status, FeedDetail: truncate(outcome.detail, 500), NextSyncAt: &outcome.next, Synced: outcome.synced,
		Now: now()}); err != nil {
		return errors.Join(runErr, err)
	}
	return runErr
}

// read is one claimed read: the bank asked with no transaction open, a window
// at a time, each window's new lines written and the cursor moved under the
// claim. The error is the module's own failure; what the bank said is the
// outcome.
//
// The days: forward from the day after the cursor (or the start day) to
// today, oldest first, so every cycle moves the cursor on; then, once
// forward is read, the ten days before the cursor the cycle started with,
// newest first, since a bank may book a transaction on an earlier day than
// it first showed it. A window is at most bankFeedWindowDays; one the bank
// answers too large is halved and asked again, the smaller size kept for the
// rest of the cycle, down to one day — a day still too large is recorded as
// too_large. A cycle makes at most bankFeedCallsPerCycle calls: with days
// left it is due again at the next poll, where the cursor left it.
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
	startDay := deref(account.FeedStartDay)
	started := account.FeedReadThrough
	type span struct {
		from, to time.Time
		forward  bool
	}
	var spans []span
	forward := startDay
	if started != nil && started.AddDate(0, 0, 1).After(forward) {
		forward = started.AddDate(0, 0, 1)
	}
	if !forward.After(today) {
		spans = append(spans, span{forward, today, true})
	}
	if started != nil {
		back := maxDay(started.AddDate(0, 0, -bankFeedOverlapDays), startDay)
		end := *started
		if end.After(today) {
			end = today
		}
		if !back.After(end) {
			spans = append(spans, span{back, end, false})
		}
	}

	windowDays, calls, skipped := bankFeedWindowDays, 0, 0
	readThrough := started
	done := func(more bool) feedOutcome {
		detail := ""
		if skipped > 0 {
			detail = fmt.Sprintf("%d transactions the bank could not place (a direction unknown, another currency) were left out.", skipped)
		}
		next := now().Add(interval)
		if more {
			next = now().Add(bankFeedPoll)
		}
		return feedOutcome{status: feedOK, detail: detail, next: next, synced: true}
	}
	for _, sp := range spans {
		lo, hi := sp.from, sp.to
		for !lo.After(hi) {
			if calls == bankFeedCallsPerCycle {
				if !sp.forward {
					// Only the overlap is left: the next cycle reads it again.
					return done(false), nil
				}
				if readThrough == nil || (started != nil && !readThrough.After(*started)) {
					// Every call of the cycle went on halving: nothing moved.
					return failed(feedTooLarge, "The bank's answers are too large to read the account forward; it is tried again.", 0), nil
				}
				return done(true), nil
			}
			from, to := lo, lo.AddDate(0, 0, windowDays-1)
			if to.After(hi) {
				to = hi
			}
			if !sp.forward {
				from, to = maxDay(hi.AddDate(0, 0, 1-windowDays), lo), hi
			}
			calls++
			// The bank, with no transaction open (A29, the lease workers' rule).
			txs, err := feed.Transactions(ctx, account.AccountNumber, from, to)
			if errors.Is(err, bankfeed.ErrTooLarge) {
				days := int(to.Sub(from).Hours()/24) + 1
				if days == 1 {
					return failed(feedTooLarge, fmt.Sprintf("The bank's answer for %s is too large to read; it is tried again.",
						from.Format(time.DateOnly)), 0), nil
				}
				windowDays = (days + 1) / 2
				continue
			}
			if err != nil {
				return w.bankFailed(ctx, account, err, failed, now)
			}
			n, err := w.writeWindow(ctx, account, lease, txs, from, to, today)
			skipped += n
			if errors.Is(err, errFeedLeaseLost) {
				return feedOutcome{status: feedOK, next: now()}, nil
			}
			if err != nil {
				return failed(feedFailing, "The lines could not be written; they are read again.", 0), err
			}
			if readThrough == nil || to.After(*readThrough) {
				readThrough = &to
			}
			if sp.forward {
				lo = to.AddDate(0, 0, 1)
			} else {
				hi = from.AddDate(0, 0, -1)
			}
		}
	}
	return done(false), nil
}

// bankFailed is the outcome of a read the bank, or Invoices, answered with
// an error: the windows before it stand.
func (w *BankFeedWorker) bankFailed(ctx context.Context, account store.ClaimFeedAccountRow, err error,
	failed func(status, detail string, after time.Duration) feedOutcome, now func() time.Time) (feedOutcome, error) {
	var limited *bankfeed.ErrRateLimited
	var busy *contracts.ErrBankFeedBusy
	switch {
	case errors.As(err, &busy):
		// Invoices' own sync is reading the account: not an error, the
		// account is read again once its claim is over, its state as it was.
		return feedOutcome{status: deref(account.FeedStatus), detail: account.FeedDetail,
			next: now().Add(min(max(busy.RetryAfter, bankFeedPoll), bankFeedLease))}, nil
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

// writeWindow writes the lines of one window the account does not have, as
// one statement of source feed, and moves the cursor to the window's end,
// both under the claim; it answers the transactions it left out.
func (w *BankFeedWorker) writeWindow(ctx context.Context, account store.ClaimFeedAccountRow, lease uuid.UUID,
	txs []bankfeed.Transaction, from, to, today time.Time) (int, error) {
	lines, skipped := feedLines(txs, today)
	ids := make([]string, 0, len(lines))
	for _, l := range lines {
		ids = append(ids, l.BankID)
	}
	q := store.New(w.deps.Pool)
	have, err := q.ExistingBankIDs(ctx, store.ExistingBankIDsParams{BankAccountID: account.ID, Ids: ids})
	if err != nil {
		return skipped, err
	}
	known := map[string]bool{}
	for _, id := range have {
		known[id] = true
	}
	st := bankstmt.Statement{Account: account.AccountNumber, Currency: currency, From: from, To: to}
	for _, l := range lines {
		if !known[l.BankID] {
			st.Lines = append(st.Lines, l)
			known[l.BankID] = true
		}
	}
	if len(st.Lines) > 0 {
		srv := &server{deps: w.deps}
		if _, err := srv.writeStatement(ctx, account.ID, account.AccountNumber, st, statementFeed, nil, map[string]int{}, nil, &lease); err != nil {
			return skipped, err
		}
	}
	// The window is read and written to its end: the cursor moves, never
	// back. A crash before this reads the window again, its lines known.
	n, err := q.AdvanceFeedRead(ctx, store.AdvanceFeedReadParams{ReadThrough: to, ID: account.ID, LeaseID: &lease})
	if err != nil {
		return skipped, err
	}
	if n == 0 {
		return skipped, errFeedLeaseLost
	}
	return skipped, nil
}

// maxDay is the later of two days.
func maxDay(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
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
