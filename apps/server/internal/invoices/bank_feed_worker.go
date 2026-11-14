package invoices

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/bankfeed"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the invoices-bank-feed worker (invoices bank feed design F5,
// F7; plan C4): every minute it claims the accounts due, one after another,
// each under a row lease of its own — the payments worker's shape — runs one
// sync of it (bank_feed_sync.go) and ends the claim with F7's outcome. The
// claim is one statement on the pool and holds no connection while the bank,
// the object store and the import's own transaction take and release theirs,
// so the worker runs on a pool of one connection.

const (
	// bankFeedWorkerName is what the runner logs this worker as.
	bankFeedWorkerName = "invoices-bank-feed"
	// bankFeedWorkerTick is the poll cadence between cycles; an account is
	// due by its own next_sync_at.
	bankFeedWorkerTick = time.Minute
	// bankFeedWorkerDefaultLease is how long a claim keeps an account from
	// every other worker: longer than the bank's 30-second call, the object
	// write and the import together.
	bankFeedWorkerDefaultLease = 5 * time.Minute
	// bankFeedFailingAfter is how many failures in a row make an account
	// failing (F7); maxBankFeedBackoff caps the wait after one.
	bankFeedFailingAfter = 3
	maxBankFeedBackoff   = 6 * time.Hour
)

// bankFeedWorkerLease is the lease a claim takes. A test shortens it
// (export_test.go).
var bankFeedWorkerLease = bankFeedWorkerDefaultLease

// bankFeedBackoff is the wait after the n-th failure in a row: the interval
// doubled n times, at most six hours (F7; plan C4's interval << failures).
func bankFeedBackoff(interval time.Duration, failures int32) time.Duration {
	wait := interval
	for i := int32(0); i < failures && wait < maxBankFeedBackoff; i++ {
		wait *= 2
	}
	return min(wait, maxBankFeedBackoff)
}

// BankFeedWorker reads the accounts the bank feed is switched on for. It
// implements worker.Worker; Module.Workers registers it only while
// INVOICES_BANK_FEED_ENABLED is on.
type BankFeedWorker struct {
	srv *server
	// srvErr is newServer's failure, kept so the worker still starts and
	// every cycle says why it does nothing.
	srvErr error
	deps   module.Deps
}

var _ worker.Worker = (*BankFeedWorker)(nil)

// NewBankFeedWorker builds the worker over worker mode's Deps: the pool, the
// clock, the configuration, the object store and the HTTP transport the bank
// is dialled through.
func NewBankFeedWorker(d module.Deps) *BankFeedWorker {
	srv, err := newServer(d)
	return &BankFeedWorker{srv: srv, srvErr: err, deps: d}
}

// Name identifies this worker in the runner's logs.
func (w *BankFeedWorker) Name() string { return bankFeedWorkerName }

// Interval is the poll cadence between cycles.
func (w *BankFeedWorker) Interval() time.Duration { return bankFeedWorkerTick }

// Run runs a cycle, sleeps the tick, and repeats until ctx is done. A
// failing cycle is logged and the loop goes on.
func (w *BankFeedWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(bankFeedWorkerTick)
	defer ticker.Stop()
	for {
		if err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.logger().Error("invoices: a bank feed worker cycle failed", "worker", bankFeedWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle syncs every account due, one claim after another with no pause,
// until none is due.
func (w *BankFeedWorker) RunCycle(ctx context.Context) error {
	for ctx.Err() == nil {
		processed, err := w.ProcessOne(ctx)
		if err != nil || !processed {
			return err
		}
	}
	return nil
}

// ProcessOne claims at most one due account and syncs it once, answering
// whether an account was claimed. A sync that fails is the account's status,
// not the cycle's error; the error answered is the worker's own — a claim or
// an outcome it could not write.
func (w *BankFeedWorker) ProcessOne(ctx context.Context) (bool, error) {
	if w.srvErr != nil {
		return false, w.srvErr
	}
	now := w.deps.Clock().UTC() // the claim's one clock read
	lease := strings.ReplaceAll(uuid.NewString(), "-", "")
	row, err := store.New(w.deps.Pool).ClaimBankFeedAccount(ctx, store.ClaimBankFeedAccountParams{
		LeaseID: lease, LeaseUntil: now.Add(bankFeedWorkerLease), Now: now,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("invoices: claim a bank feed account: %w", err)
	}
	c := feedClaim{row: row, lease: lease, now: now}
	outcome, syncErr := w.srv.syncAccount(ctx, c)
	if err := w.end(ctx, c, outcome, syncErr); err != nil && !errors.Is(err, errFeedClaimLost) {
		return true, err
	}
	return true, nil
}

// end writes F7's outcome under the claim's lease, which it clears: success
// ok and due after the interval; a refusal refused with its reason, due
// after the interval; the bank's 429 the status kept and the wait noted,
// due after the wait and at least the interval; a refused key nothing here —
// the feed's MarkBankFeedUnauthorized stopped every account already, or a
// newer key is stored and the account is made due for it — and
// any other failure one more in a row, failing from the third, due after the
// backoff. The failure itself is logged at warn with the account, never
// with the bank's answer beyond the adapter's error, which names the status
// and never the key.
func (w *BankFeedWorker) end(ctx context.Context, c feedClaim, outcome syncOutcome, syncErr error) error {
	q := store.New(w.deps.Pool)
	interval := w.deps.Config.InvoicesBankFeedInterval
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	account := c.row.Account
	var n int64
	var err error
	var limited *bankfeed.ErrRateLimited
	switch {
	case errors.Is(syncErr, errFeedClaimLost):
		return syncErr
	case errors.Is(syncErr, bankfeed.ErrUnauthorized):
		w.logger().WarnContext(ctx, "invoices: the bank refused the stored API key; the bank feed stops until a new key is stored",
			"worker", bankFeedWorkerName, "account", account)
		if _, err := q.ReleaseBankFeedClaim(ctx, store.ReleaseBankFeedClaimParams{Now: c.now, Account: account, LeaseID: c.lease}); err != nil {
			return fmt.Errorf("invoices: end the bank feed claim on %s: %w", account, err)
		}
		return nil
	case errors.As(syncErr, &limited):
		wait := max(limited.RetryAfter, interval)
		n, err = q.BankFeedRateLimited(ctx, store.BankFeedRateLimitedParams{
			Detail:     fmt.Sprintf("The bank asked to wait %s.", limited.RetryAfter.Round(time.Second)),
			NextSyncAt: c.now.Add(wait), Account: account, LeaseID: c.lease,
		})
	case syncErr != nil:
		failures := c.row.Failures + 1
		w.logger().WarnContext(ctx, "invoices: a bank feed sync failed; the bank could not be reached or did not answer as it should",
			"worker", bankFeedWorkerName, "account", account, "failures", failures, "error", syncErr.Error())
		n, err = q.BankFeedFailed(ctx, store.BankFeedFailedParams{
			FailingAfter: bankFeedFailingAfter, Detail: feedFailureDetail(syncErr),
			NextSyncAt: c.now.Add(bankFeedBackoff(interval, failures)), Now: c.now, Account: account, LeaseID: c.lease,
		})
	case outcome.refused != nil:
		w.logger().WarnContext(ctx, "invoices: a bank feed batch was refused whole; nothing of it was stored",
			"worker", bankFeedWorkerName, "account", account, "reason", outcome.refused.reason)
		n, err = q.BankFeedRefused(ctx, store.BankFeedRefusedParams{
			Reason: outcome.refused.reason, Detail: outcome.refused.detail,
			NextSyncAt: c.now.Add(interval), Now: c.now, Account: account, LeaseID: c.lease,
		})
	default:
		n, err = q.BankFeedSynced(ctx, store.BankFeedSyncedParams{
			Now: c.now, LastBankFileID: outcome.bankFileID, NextSyncAt: c.now.Add(interval), Account: account, LeaseID: c.lease,
		})
	}
	if err != nil {
		return fmt.Errorf("invoices: end the bank feed claim on %s: %w", account, err)
	}
	if n == 0 {
		return errFeedClaimLost
	}
	return nil
}

// feedFailureDetail is what an account's detail says of a failed sync: the
// kind of failure in words a manager can act on, never the bank's answer.
func feedFailureDetail(err error) string {
	switch {
	case errors.Is(err, errBankFeedSwitchedOff):
		return "The bank feed is switched off on this installation."
	case errors.Is(err, errBankFeedNotConnected):
		return "No bank API key is stored."
	case errors.Is(err, errBankFeedKeyUnreadable):
		return "The stored bank API key cannot be read on this server. Store the key again."
	case errors.Is(err, context.DeadlineExceeded):
		return "The bank did not answer in time."
	case errors.Is(err, errFeedStorage):
		return "The batch could not be kept in the object store."
	}
	return "The bank could not be reached, or did not answer as it should."
}

func (w *BankFeedWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}
