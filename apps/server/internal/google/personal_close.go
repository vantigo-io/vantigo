package google

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/google/gen"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
)

// This file is closing a person's Google connection for a reason other than
// their own disconnect (G27, G28): a leaver and a deleted account, whose
// tokens are forgotten at once; an account found suspended, disabled or gone,
// likewise; and the Owner's switch turned off, which closes everyone's and
// keeps the tokens for the removals. Each move is one google.personal.closed
// event, its code the reason; never a token. The switch itself is here too.

// The closed events' codes.
const (
	personalClosedLeaver    = "leaver"
	personalClosedDeleted   = "deleted"
	personalClosedDisabled  = "disabled"
	personalClosedSuspended = "suspended"
	personalClosedSwitchOff = "switch_off"
	personalClosedRepointed = "repointed"
)

// closePersonal closes gid's live connection through q at now by actor,
// recording google.personal.closed with code when it moved the row. forget
// also drops its token. revision, when not nil, is the revision the caller
// read its reason at: a row moved on since is left alone, token and all.
// Without one, a connection closing already has its token dropped too when
// forget asks. No connection is nothing to do.
func closePersonal(ctx context.Context, q *store.Queries, now time.Time, actor contracts.Actor, gid string, revision *int64, forget bool, code string) error {
	_, err := q.ClosePersonal(ctx, store.ClosePersonalParams{Forget: forget, Now: now, GoogleUserID: gid, Revision: revision})
	moved := true
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		moved = false
	case err != nil:
		return fmt.Errorf("google: close the personal connection: %w", err)
	}
	if forget && !moved && revision == nil {
		if err := q.ForgetPersonalRefresh(ctx, store.ForgetPersonalRefreshParams{Now: now, GoogleUserID: gid}); err != nil {
			return fmt.Errorf("google: forget the personal connection's token: %w", err)
		}
	}
	if !moved {
		return nil
	}
	return recordEvent(ctx, q, now, actor, actionPersonalClosed, gid, []string{code})
}

// closePersonalIn is closePersonal in a transaction of its own on pool.
func closePersonalIn(ctx context.Context, pool db.TxBeginner, q *store.Queries, now time.Time, actor contracts.Actor, gid string, revision *int64, forget bool, code string) error {
	return db.WithTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return closePersonal(ctx, q.WithTx(tx), now, actor, gid, revision, forget, code)
	})
}

// closeAllPersonal closes every live connection through q at now by actor,
// their tokens kept for the removals: the Owner turned the switch off.
func closeAllPersonal(ctx context.Context, q *store.Queries, now time.Time, actor contracts.Actor) error {
	rows, err := q.CloseAllPersonal(ctx, now)
	if err != nil {
		return fmt.Errorf("google: close the personal connections: %w", err)
	}
	for _, r := range rows {
		if err := recordEvent(ctx, q, now, actor, actionPersonalClosed, r.GoogleUserID, []string{personalClosedSwitchOff}); err != nil {
			return err
		}
	}
	return nil
}

// PutGoogleConnectionPersonal turns Connection for people on or off (G28,
// G29), the Owner's. Turning it off closes every live connection, tokens
// kept; turning it on stamps when. The same value again writes nothing.
// Audited google.personal.switched. (PUT /api/v1/google/connection/personal)
func (s *server) PutGoogleConnectionPersonal(ctx context.Context, req gen.PutGoogleConnectionPersonalRequestObject) (gen.PutGoogleConnectionPersonalResponseObject, error) {
	if req.Body == nil {
		return nil, errors.New("google: Connection for people without a body")
	}
	enabled := req.Body.Enabled
	actor, err := requestActor(ctx)
	if err != nil {
		return nil, err
	}
	now := s.keys.c.now()
	var body gen.GoogleConnection
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		conn, err := q.LockConnection(ctx)
		if err != nil {
			return fmt.Errorf("google: lock the connection: %w", err)
		}
		if conn.PersonalConnection != enabled {
			if conn, err = q.SetPersonalSwitch(ctx, store.SetPersonalSwitchParams{Enabled: enabled, Now: now}); err != nil {
				return fmt.Errorf("google: set Connection for people: %w", err)
			}
			if !enabled {
				// Everyone must connect again once it is back on (G28).
				if err := closeAllPersonal(ctx, q, now, actor); err != nil {
					return err
				}
			}
			code := "off"
			if enabled {
				code = "on"
			}
			if err := recordEvent(ctx, q, now, actor, actionPersonalSwitched, "", []string{code}); err != nil {
				return err
			}
		}
		body, err = readConnection(ctx, q, s.deps.Config, conn)
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.PutGoogleConnectionPersonal200JSONResponse(body), nil
}
