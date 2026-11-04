package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is google-sweep, the module's one worker (G28). Each hour it:
//
//   - deletes the closing connections nothing needs any more — seven days
//     after they closed, or once a day has passed since they were last used
//     and since they closed —, one google.personal.deleted event each, and
//     revokes the token each still held at Google;
//   - reads, at most once a day each and at most 100 a cycle, the account of
//     every connection waiting in reconnect — in Vantigo's rows first, then
//     with users.get as the admin — and closes one whose account is
//     deleted, suspended or no longer one Vantigo manages, its token
//     forgotten: a person who never reconnects is still let go.
//
// Unlike Entra's sweep it holds no lease (the 00087 lesson): each step
// claims its rows in one statement — the DELETE … RETURNING, the UPDATE …
// FOR UPDATE SKIP LOCKED stamping account_checked_at — and calls Google
// holding no pooled connection, so two replicas never take the same row and
// a slow Google never holds the pool. It runs in the API and in worker mode
// alike, wherever module.Workers runs.

const (
	// sweepName is what the runner logs this worker as.
	sweepName = "google-sweep"
	// sweepInterval is the worker's cycle.
	sweepInterval = time.Hour
	// sweepAccountChecks bounds the accounts one cycle reads at Google; a
	// backlog drains over cycles.
	sweepAccountChecks = 100
)

// workers is the module's background work: google-sweep.
func workers(d module.Deps) []worker.Worker {
	return []worker.Worker{NewSweepWorker(d)}
}

// SweepWorker is google-sweep. It implements worker.Worker.
type SweepWorker struct {
	deps module.Deps
	q    *store.Queries
	keys *keyring
}

var _ worker.Worker = (*SweepWorker)(nil)

// NewSweepWorker builds the worker over d.
func NewSweepWorker(d module.Deps) *SweepWorker {
	q := store.New(d.Pool)
	return &SweepWorker{deps: d, q: q, keys: newKeyring(q, newGoogleClient(d))}
}

// Name identifies this worker in the runner's logs.
func (w *SweepWorker) Name() string { return sweepName }

// Interval is the worker's cycle, an hour.
func (w *SweepWorker) Interval() time.Duration { return sweepInterval }

// Run is the worker loop: a cycle, then the interval. A failure is logged
// and the loop goes on — the runner never restarts a worker.
func (w *SweepWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.Interval())
	defer ticker.Stop()
	for {
		if err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.keys.c.logger.Error("google: the sweep failed", "worker", sweepName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle is one cycle: the spent connections deleted and their tokens
// revoked, then the due accounts read.
func (w *SweepWorker) RunCycle(ctx context.Context) error {
	if err := w.deleteSpent(ctx); err != nil {
		return err
	}
	return w.checkAccounts(ctx)
}

// sweepActor is the module's own system actor, the sweep's.
var sweepActor = contracts.Actor{System: systemActor}

// deleteSpent deletes the spent closing connections in one statement — the
// claim —, then revokes each token they held, holding no connection, and
// records each deletion. A revocation Google refuses or does not answer is
// recorded revoke_failed and not tried again: the row and its token are
// gone, and Google lets an unused refresh token lapse.
func (w *SweepWorker) deleteSpent(ctx context.Context) error {
	now := w.keys.c.now()
	rows, err := w.q.DeleteSpentPersonal(ctx, now)
	if err != nil {
		return fmt.Errorf("google: delete the spent personal connections: %w", err)
	}
	failed := 0
	for _, r := range rows {
		var codes []string
		if r.RefreshSealed != nil {
			if err := w.revoke(ctx, r.RefreshSealed); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				failed++
				codes = append(codes, "revoke_failed")
			} else {
				codes = append(codes, "revoked")
			}
		}
		if err := recordEvent(ctx, w.q, w.keys.c.now(), sweepActor, actionPersonalDeleted, r.GoogleUserID, codes); err != nil {
			return err
		}
	}
	if len(rows) > 0 {
		w.keys.c.logger.InfoContext(ctx, "google: spent personal connections deleted", "worker", sweepName, "deleted", len(rows), "revoke_failed", failed)
	}
	return nil
}

// revoke revokes the refresh token sealed at Google. A token Google no
// longer knows (400 invalid_token) is revoked already.
func (w *SweepWorker) revoke(ctx context.Context, sealed []byte) error {
	c := w.keys.c
	if c.box == nil {
		return errors.New("google: no secrets box to open the refresh token with")
	}
	token, err := c.box.Open(personalRefreshPurpose, sealed)
	if err != nil {
		return fmt.Errorf("google: the refresh token does not open on this server: %w", err)
	}
	status, data, err := c.postForm(ctx, revokeURL, url.Values{"token": {string(token)}})
	if err != nil {
		return err
	}
	switch {
	case status == http.StatusOK:
		return nil
	case status == http.StatusBadRequest && oauthErrorOf(data) == "invalid_token":
		return nil
	}
	return &googleError{Status: status, Reason: oauthErrorOf(data)}
}

// oauthErrorOf is an OAuth refusal's error code, "" for another shape.
// error_description is never read.
func oauthErrorOf(data []byte) string {
	var a tokenAnswer
	if err := json.Unmarshal(data, &a); err != nil {
		return ""
	}
	return a.Error
}

// checkAccounts claims the connections waiting in reconnect that are due
// (G28) and reads each one's account: one deleted, or no longer one Vantigo
// manages, closes without asking Google; otherwise users.get answers it, and
// a 404 or a suspended user closes it. Each close is guarded by the revision
// claimed: a person who reconnected while the account was read keeps the
// new connection. A failure at Google leaves the rest for the next day's
// claim.
func (w *SweepWorker) checkAccounts(ctx context.Context) error {
	now := w.keys.c.now()
	due, err := w.q.ClaimPersonalAccountChecks(ctx, store.ClaimPersonalAccountChecksParams{Now: now, RowLimit: sweepAccountChecks})
	if err != nil {
		return fmt.Errorf("google: claim the personal connections' accounts to check: %w", err)
	}
	if len(due) == 0 {
		return nil
	}
	var users tokenSource
	for _, d := range due {
		code := ""
		account, err := w.q.GetAccount(ctx, d.GoogleUserID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			code = personalClosedDeleted
		case err != nil:
			return fmt.Errorf("google: read the account: %w", err)
		case account.State == stateDeleted:
			code = personalClosedDeleted
		case !usableAccount(account):
			code = personalClosedDisabled
		default:
			if users == nil {
				conn, err := w.q.GetConnection(ctx)
				if err != nil {
					return fmt.Errorf("google: read the connection: %w", err)
				}
				if conn.AdminSubject == nil {
					w.keys.c.logger.InfoContext(ctx, "google: the sweep cannot read accounts at Google: no admin is named", "worker", sweepName)
					return nil
				}
				users = w.keys.tokenSource([]string{scopeUsers}, *conn.AdminSubject)
			}
			var u struct {
				Suspended bool `json:"suspended"`
			}
			err := w.keys.c.get(ctx, users, userURL(d.GoogleUserID, "?projection=basic&fields=suspended"), &u)
			switch {
			case errors.Is(err, errNotFound):
				code = personalClosedDeleted
			case err != nil:
				if ctx.Err() != nil {
					return ctx.Err()
				}
				w.keys.c.logger.InfoContext(ctx, "google: the sweep could not read an account; the rest wait for the next cycle",
					"worker", sweepName, "google_user_id", d.GoogleUserID, "error", err.Error())
				return nil
			case u.Suspended:
				code = personalClosedSuspended
			}
		}
		if code == "" {
			continue
		}
		revision := d.Revision
		if err := closePersonalIn(ctx, w.deps.Pool, w.q, w.keys.c.now(), sweepActor, d.GoogleUserID, &revision, true, code); err != nil {
			return err
		}
	}
	return nil
}
