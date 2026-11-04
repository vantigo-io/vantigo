package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/google/gen"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
	"github.com/vantigo-io/vantigo/server/internal/secrets"
)

// This file is the flow's last step and the person's own reads (G25–G28):
// complete, under the session, opening the connected cookie only for the
// person and the session that started the connect, checking the switch and
// the certain account again — read before any transaction, the lock rule,
// then the switch and the account's row once more under their locks in the
// transaction that stores the connection, so a switch turned off or a
// leaver's Deactivate racing it either sees the row or is seen —, with its
// refresh token sealed; GET, the caller's own connection and whether they
// may connect; DELETE, a disconnect that keeps the token for the removals
// that may still need it.

// PostGoogleComplete completes a connect. The connected cookie is cleared on
// every answer. 410 expired for a cookie missing, expired, or another
// person's or session's; 409 switch_off, no_account, account_mismatch or
// account_taken. A connect submitted twice is the caller's own connection
// both times. (POST /api/v1/google/complete)
func (s *server) PostGoogleComplete(ctx context.Context, _ gen.PostGoogleCompleteRequestObject) (gen.PostGoogleCompleteResponseObject, error) {
	defer s.clearPersonalCookie(ctx, personalConnectedCookie)
	p, err := requestPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	var cb personalConnectedBody
	if !s.openPersonalCookie(ctx, personalConnectedCookie, personalConnectedPurpose, &cb) || cb.UserID != p.UserID {
		return expiredComplete(), nil
	}
	mac, err := s.sessionMAC(p.SessionID)
	if err != nil {
		return nil, err
	}
	if !secrets.MACEqual(mac, cb.SessionMAC) {
		return expiredComplete(), nil
	}

	conn, creds, err := readFooting(ctx, s.q)
	if err != nil {
		return nil, err
	}
	if !personalOffered(conn, creds) {
		return gen.PostGoogleComplete409ApplicationProblemPlusJSONResponse(switchOffProblem()), nil
	}
	account, err := s.certainAccount(ctx)
	if err != nil {
		return nil, err
	}
	if account == nil {
		return gen.PostGoogleComplete409ApplicationProblemPlusJSONResponse(noAccountProblem()), nil
	}
	// The ID token's sub must be the account's Directory id (G27, U1): a
	// person signed in to Google as anyone else stores nothing.
	if cb.Sub != account.UserID {
		return gen.PostGoogleComplete409ApplicationProblemPlusJSONResponse(problem(http.StatusConflict, codeAccountMismatch,
			"Another Google account",
			"You signed in to Google with another account than the one Vantigo knows you by. Connect again, and choose that account on Google's page.")), nil
	}

	sealed, err := s.deps.Secrets.Seal(personalRefreshPurpose, []byte(cb.RefreshToken))
	if err != nil {
		return nil, fmt.Errorf("google: seal the refresh token: %w", err)
	}
	actor, err := requestActor(ctx)
	if err != nil {
		return nil, err
	}
	now := s.keys.c.now()
	var row store.GooglePersonalConnection
	taken, switchOff, noAccount := false, false, false
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		// The switch and the account again, under their locks: the
		// connection's row FOR UPDATE, so the switch's PUT, which closes
		// every live row, runs wholly before or after; the account's FOR
		// SHARE, so a leaver's Deactivate, which writes it before it closes
		// the connection, does too.
		locked, err := q.LockConnection(ctx)
		if err != nil {
			return fmt.Errorf("google: lock the connection: %w", err)
		}
		if !locked.PersonalConnection {
			switchOff = true
			return nil
		}
		acc, err := q.LockAccountShare(ctx, cb.Sub)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			noAccount = true
			return nil
		case err != nil:
			return fmt.Errorf("google: lock the account: %w", err)
		case !usableAccount(acc):
			noAccount = true
			return nil
		}
		existing, err := lockedRow(q.LockPersonal(ctx, cb.Sub))
		if err != nil {
			return err
		}
		mine, err := lockedRow(q.LockLivePersonalByUser(ctx, p.UserID))
		if err != nil {
			return err
		}
		if existing != nil && existing.State != personalClosing && existing.UserID != p.UserID {
			taken = true
			return nil
		}
		if mine != nil && mine.GoogleUserID != cb.Sub {
			// The person's account was re-pointed in Staff: their old
			// connection closes, its token kept for the old account's
			// removals.
			if err := closePersonal(ctx, q, now, actor, mine.GoogleUserID, &mine.Revision, false, personalClosedRepointed); err != nil {
				return err
			}
		}
		// The row's scopes are the token's answer (G26): connect asked the
		// features the row held again, so an answer without one of them is
		// a consent withdrawn — or an incremental grant not answered whole
		// (U9) — which the row must not claim.
		if existing != nil {
			row, err = q.TakePersonal(ctx, store.TakePersonalParams{
				UserID: p.UserID, Scopes: cb.Scopes, RefreshSealed: sealed, Now: now,
				GoogleUserID: cb.Sub, Revision: existing.Revision,
			})
		} else {
			row, err = q.InsertPersonal(ctx, store.InsertPersonalParams{
				GoogleUserID: cb.Sub, UserID: p.UserID, Scopes: cb.Scopes, RefreshSealed: sealed, Now: now,
			})
		}
		if err != nil {
			return fmt.Errorf("google: store the personal connection: %w", err)
		}
		if err := q.ClearPersonalLastError(ctx); err != nil {
			return fmt.Errorf("google: clear the connect's last error: %w", err)
		}
		return recordEvent(ctx, q, now, actor, actionPersonalConnected, cb.Sub, []string{"feature:" + cb.Feature})
	})
	switch {
	case switchOff:
		return gen.PostGoogleComplete409ApplicationProblemPlusJSONResponse(switchOffProblem()), nil
	case noAccount:
		return gen.PostGoogleComplete409ApplicationProblemPlusJSONResponse(noAccountProblem()), nil
	case db.IsUniqueViolation(err):
		// A concurrent store won: the caller's own active row is a connect
		// submitted twice, anyone else's is the account taken.
		current, rerr := s.q.GetPersonal(ctx, cb.Sub)
		if rerr == nil && current.UserID == p.UserID && current.State == personalActive {
			row, err, taken = current, nil, false
		} else {
			taken = true
		}
	}
	if taken {
		return gen.PostGoogleComplete409ApplicationProblemPlusJSONResponse(accountTakenProblem()), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PostGoogleComplete200JSONResponse(gen.GooglePersonalComplete{
		Connection: personalConnectionBody(true, account, &row),
		ReturnTo:   safeReturnTo(cb.ReturnTo),
	}), nil
}

// expiredComplete is complete's 410: the cookie missing, expired, or another
// person's or session's, answered alike.
func expiredComplete() gen.PostGoogleComplete410ApplicationProblemPlusJSONResponse {
	return gen.PostGoogleComplete410ApplicationProblemPlusJSONResponse(problem(http.StatusGone, codeExpired,
		"Connect expired", "The connect has expired or was started in another session. Connect Google again."))
}

// GetGoogleConnectionMe answers the caller's own connection, whether they
// may connect and the account Vantigo knows them by. Always 200.
// (GET /api/v1/google/connection/me)
func (s *server) GetGoogleConnectionMe(ctx context.Context, _ gen.GetGoogleConnectionMeRequestObject) (gen.GetGoogleConnectionMeResponseObject, error) {
	p, err := requestPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	conn, creds, err := readFooting(ctx, s.q)
	if err != nil {
		return nil, err
	}
	account, err := s.certainAccount(ctx)
	if err != nil {
		return nil, err
	}
	row, err := s.callerConnection(ctx, p.UserID, account)
	if err != nil {
		return nil, err
	}
	return gen.GetGoogleConnectionMe200JSONResponse(personalConnectionBody(personalOffered(conn, creds), account, row)), nil
}

// DeleteGoogleConnectionMe disconnects the caller's own Google account: their
// live connection closes, its token kept for the removals that may still
// need it (G28). 204 whether or not there was one.
// (DELETE /api/v1/google/connection/me)
func (s *server) DeleteGoogleConnectionMe(ctx context.Context, _ gen.DeleteGoogleConnectionMeRequestObject) (gen.DeleteGoogleConnectionMeResponseObject, error) {
	p, err := requestPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	actor, err := requestActor(ctx)
	if err != nil {
		return nil, err
	}
	now := s.keys.c.now()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		live, err := lockedRow(q.LockLivePersonalByUser(ctx, p.UserID))
		if err != nil || live == nil {
			return err
		}
		if _, err := q.ClosePersonal(ctx, store.ClosePersonalParams{Forget: false, Now: now, GoogleUserID: live.GoogleUserID, Revision: &live.Revision}); err != nil {
			return fmt.Errorf("google: close the personal connection: %w", err)
		}
		return recordEvent(ctx, q, now, actor, actionPersonalDisconnected, live.GoogleUserID, nil)
	})
	if err != nil {
		return nil, err
	}
	return gen.DeleteGoogleConnectionMe204Response{}, nil
}
