package entra

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
	"github.com/vantigo-io/vantigo/server/internal/secrets"
)

// This file is the flow's last step and the person's own reads (Outlook
// connection design M3, M4, M6, M7): complete, under the session, opening
// the connected cookie only for the person and the session that started
// the connect, checking the switch and the certain account again — read
// before any transaction, the lock rule, then the switch and the account's
// row once more under their locks in the transaction that stores the
// connection, so a switch turned off or a leaver's Deactivate racing it
// either sees the row or is seen —, with its refresh token sealed; GET, the caller's own connection and whether
// they may connect; DELETE, a disconnect that keeps the token for the
// removals that may still need it and never revokes the person's sessions.

// PostEntraOutlookComplete completes a connect. The connected cookie is
// cleared on every answer. 410 expired for a cookie missing, expired, or
// another person's or session's; 409 switch_off, no_account,
// account_mismatch or account_taken. A connect submitted twice is the
// caller's own connection both times. (POST /api/v1/entra/outlook/complete)
func (s *server) PostEntraOutlookComplete(ctx context.Context, _ gen.PostEntraOutlookCompleteRequestObject) (gen.PostEntraOutlookCompleteResponseObject, error) {
	defer s.clearOutlookCookie(ctx, outlookConnectedCookie)
	p, err := requestPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	var cb outlookConnectedBody
	if !s.openOutlookCookie(ctx, outlookConnectedCookie, outlookConnectedPurpose, &cb) || cb.UserID != p.UserID {
		return expiredComplete(), nil
	}
	mac, err := s.sessionMAC(p.SessionID)
	if err != nil {
		return nil, err
	}
	if !secrets.MACEqual(mac, cb.SessionMAC) {
		return expiredComplete(), nil
	}

	conn, err := s.q.GetConnection(ctx)
	if err != nil {
		return nil, fmt.Errorf("entra: read the connection: %w", err)
	}
	if !conn.OutlookConnect || conn.TenantID == nil {
		return gen.PostEntraOutlookComplete409ApplicationProblemPlusJSONResponse(switchOffProblem()), nil
	}
	account, err := s.certainAccount(ctx)
	if err != nil {
		return nil, err
	}
	if account == nil {
		return gen.PostEntraOutlookComplete409ApplicationProblemPlusJSONResponse(noAccountProblem()), nil
	}
	if cb.OID != account.ObjectID || cb.TID != *conn.TenantID {
		return gen.PostEntraOutlookComplete409ApplicationProblemPlusJSONResponse(problem(http.StatusConflict, codeAccountMismatch,
			"Another Microsoft account",
			fmt.Sprintf("You signed in as %s; Vantigo knows you as %s.", cb.UPN, account.UserPrincipalName))), nil
	}

	sealed, err := s.deps.Secrets.Seal(outlookRefreshPurpose, []byte(cb.RefreshToken))
	if err != nil {
		return nil, fmt.Errorf("entra: seal the Outlook refresh token: %w", err)
	}
	actor, err := requestActor(ctx)
	if err != nil {
		return nil, err
	}
	now := s.client.now()
	var row store.EntraOutlookConnection
	taken, switchOff, noAccount := false, false, false
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		// The switch and the account again, under their locks: the
		// connection's row FOR UPDATE — this transaction writes it, and a
		// share lock two completes upgrade at once is a deadlock — so the
		// switch's PUT, which closes every live row, runs wholly before or
		// after; the account's FOR SHARE, so a leaver's Deactivate, which
		// writes it before it closes the connection, does too.
		locked, err := q.LockConnection(ctx)
		if err != nil {
			return fmt.Errorf("entra: lock the connection: %w", err)
		}
		if !locked.OutlookConnect || locked.TenantID == nil {
			switchOff = true
			return nil
		}
		acc, err := q.LockAccountShare(ctx, cb.OID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			noAccount = true
			return nil
		case err != nil:
			return fmt.Errorf("entra: lock the account: %w", err)
		case !trusted(acc) || acc.State != stateEnabled:
			noAccount = true
			return nil
		}
		existing, err := lockedRow(q.LockOutlookConnection(ctx, cb.OID))
		if err != nil {
			return err
		}
		mine, err := lockedRow(q.LockLiveOutlookConnectionByUser(ctx, p.UserID))
		if err != nil {
			return err
		}
		if existing != nil && existing.State != outlookClosing && existing.UserID != p.UserID {
			taken = true
			return nil
		}
		if mine != nil && mine.ObjectID != cb.OID {
			// The person's account was re-pointed in Staff: their old
			// connection closes, its token kept for the old mailbox's
			// removals.
			if _, err := q.CloseOutlookConnection(ctx, store.CloseOutlookConnectionParams{Forget: false, Now: now, ObjectID: mine.ObjectID, Revision: &mine.Revision}); err != nil {
				return fmt.Errorf("entra: close the re-pointed Outlook connection: %w", err)
			}
			oldOID := mine.ObjectID
			if err := recordEvent(ctx, q, now, actor, actionOutlookClosed, &oldOID, accountUPN(ctx, q, oldOID), []string{"repointed"}); err != nil {
				return err
			}
		}
		// The row's scopes are the token's answer, as a refresh records
		// them (design M20): connect asked the features the row held again,
		// so an answer without one of them is a consent withdrawn, which the
		// row must not claim.
		if existing != nil {
			row, err = q.TakeOutlookConnection(ctx, store.TakeOutlookConnectionParams{
				UserID: p.UserID, TenantID: cb.TID, Scopes: cb.Scopes, RefreshSealed: sealed, Now: now,
				ObjectID: cb.OID, Revision: existing.Revision,
			})
		} else {
			row, err = q.InsertOutlookConnection(ctx, store.InsertOutlookConnectionParams{
				ObjectID: cb.OID, UserID: p.UserID, TenantID: cb.TID, Scopes: cb.Scopes, RefreshSealed: sealed, Now: now,
			})
		}
		if err != nil {
			return fmt.Errorf("entra: store the Outlook connection: %w", err)
		}
		if err := q.ClearOutlookLastError(ctx); err != nil {
			return fmt.Errorf("entra: clear the Outlook connect's last error: %w", err)
		}
		oid := cb.OID
		return recordEvent(ctx, q, now, actor, actionOutlookConnected, &oid, account.UserPrincipalName, []string{"feature:" + cb.Feature})
	})
	switch {
	case switchOff:
		return gen.PostEntraOutlookComplete409ApplicationProblemPlusJSONResponse(switchOffProblem()), nil
	case noAccount:
		return gen.PostEntraOutlookComplete409ApplicationProblemPlusJSONResponse(noAccountProblem()), nil
	case db.IsUniqueViolation(err):
		// A concurrent store won: the caller's own active row is a connect
		// submitted twice, anyone else's is the account taken.
		current, rerr := s.q.GetOutlookConnection(ctx, cb.OID)
		if rerr == nil && current.UserID == p.UserID && current.State == outlookActive {
			row, err, taken = current, nil, false
		} else {
			taken = true
		}
	}
	if taken {
		return gen.PostEntraOutlookComplete409ApplicationProblemPlusJSONResponse(accountTakenProblem()), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PostEntraOutlookComplete200JSONResponse(gen.EntraOutlookComplete{
		Connection: outlookConnectionBody(conn, account, &row),
		ReturnTo:   safeReturnTo(cb.ReturnTo),
	}), nil
}

// expiredComplete is complete's 410: the cookie missing, expired, or
// another person's or session's, answered alike.
func expiredComplete() gen.PostEntraOutlookComplete410ApplicationProblemPlusJSONResponse {
	return gen.PostEntraOutlookComplete410ApplicationProblemPlusJSONResponse(problem(http.StatusGone, codeExpired,
		"Connect expired", "The connect has expired or was started in another session. Connect Outlook again."))
}

// lockedRow is a locking read's row, nil for none.
func lockedRow(row store.EntraOutlookConnection, err error) (*store.EntraOutlookConnection, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("entra: lock the Outlook connection: %w", err)
	}
	return &row, nil
}

// accountUPN is the UPN the module keeps for oid, "" without a row: an
// event's address.
func accountUPN(ctx context.Context, q *store.Queries, oid uuid.UUID) string {
	row, err := q.GetAccount(ctx, oid)
	if err != nil {
		return ""
	}
	return row.UserPrincipalName
}

// GetEntraOutlookConnection answers the caller's own connection, whether
// they may connect and the account Vantigo knows them by. Always 200.
// (GET /api/v1/entra/outlook/connection)
func (s *server) GetEntraOutlookConnection(ctx context.Context, _ gen.GetEntraOutlookConnectionRequestObject) (gen.GetEntraOutlookConnectionResponseObject, error) {
	p, err := requestPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := s.q.GetConnection(ctx)
	if err != nil {
		return nil, fmt.Errorf("entra: read the connection: %w", err)
	}
	account, err := s.certainAccount(ctx)
	if err != nil {
		return nil, err
	}
	row, err := s.callerConnection(ctx, p.UserID, account)
	if err != nil {
		return nil, err
	}
	return gen.GetEntraOutlookConnection200JSONResponse(outlookConnectionBody(conn, account, row)), nil
}

// DeleteEntraOutlookConnection disconnects the caller's Outlook: their live
// connection closes, its token kept for the removals that may still need
// it (design M7). 204 whether or not there was one; the person's sign-in
// sessions are never revoked. (DELETE /api/v1/entra/outlook/connection)
func (s *server) DeleteEntraOutlookConnection(ctx context.Context, _ gen.DeleteEntraOutlookConnectionRequestObject) (gen.DeleteEntraOutlookConnectionResponseObject, error) {
	p, err := requestPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	actor, err := requestActor(ctx)
	if err != nil {
		return nil, err
	}
	now := s.client.now()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		live, err := lockedRow(q.LockLiveOutlookConnectionByUser(ctx, p.UserID))
		if err != nil || live == nil {
			return err
		}
		if _, err := q.CloseOutlookConnection(ctx, store.CloseOutlookConnectionParams{Forget: false, Now: now, ObjectID: live.ObjectID, Revision: &live.Revision}); err != nil {
			return fmt.Errorf("entra: close the Outlook connection: %w", err)
		}
		oid := live.ObjectID
		return recordEvent(ctx, q, now, actor, actionOutlookDisconnected, &oid, accountUPN(ctx, q, oid), nil)
	})
	if err != nil {
		return nil, err
	}
	return gen.DeleteEntraOutlookConnection204Response{}, nil
}
