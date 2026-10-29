package entra

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
)

// This file is closing a person's Outlook connection for a reason other
// than their own disconnect (Outlook connection design M6, M7): a leaver
// and a deleted account, whose tokens are forgotten at once — the leaver's
// steps end the token before any clean-up could use it —; an account the
// tenant reports disabled or gone; and the Owner's switch turned off, which
// closes everyone's and keeps the tokens for the removals. Each move is one
// entra.outlook.closed event, its code the reason; never a token.

// The closed events' codes besides repointed (outlook_complete.go).
const (
	outlookClosedLeaver    = "leaver"
	outlookClosedDeleted   = "deleted"
	outlookClosedDisabled  = "disabled"
	outlookClosedSwitchOff = "switch_off"
)

// closeOutlook closes oid's live connection through q at now by actor,
// recording entra.outlook.closed with code when it moved the row. forget
// also drops its token, a connection closing already included. No
// connection is nothing to do.
func closeOutlook(ctx context.Context, q *store.Queries, now time.Time, actor contracts.Actor, oid uuid.UUID, forget bool, code string) error {
	_, err := q.CloseOutlookConnection(ctx, store.CloseOutlookConnectionParams{Forget: forget, Now: now, ObjectID: oid})
	moved := true
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		moved = false
	case err != nil:
		return fmt.Errorf("entra: close the Outlook connection: %w", err)
	}
	if forget && !moved {
		if err := q.ForgetOutlookRefresh(ctx, store.ForgetOutlookRefreshParams{Now: now, ObjectID: oid}); err != nil {
			return fmt.Errorf("entra: forget the Outlook connection's token: %w", err)
		}
	}
	if !moved {
		return nil
	}
	return recordEvent(ctx, q, now, actor, actionOutlookClosed, &oid, accountUPN(ctx, q, oid), []string{code})
}

// closeOutlookIn is closeOutlook in a transaction of its own on pool.
func closeOutlookIn(ctx context.Context, pool db.TxBeginner, q *store.Queries, now time.Time, actor contracts.Actor, oid uuid.UUID, forget bool, code string) error {
	return db.WithTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return closeOutlook(ctx, q.WithTx(tx), now, actor, oid, forget, code)
	})
}

// closeAllOutlook closes every live connection through q at now by actor,
// their tokens kept for the removals: the Owner turned the switch off. One
// entra.outlook.closed event, switch_off, per connection.
func closeAllOutlook(ctx context.Context, q *store.Queries, now time.Time, actor contracts.Actor) error {
	rows, err := q.CloseAllOutlookConnections(ctx, now)
	if err != nil {
		return fmt.Errorf("entra: close the Outlook connections: %w", err)
	}
	for _, r := range rows {
		oid := r.ObjectID
		if err := recordEvent(ctx, q, now, actor, actionOutlookClosed, &oid, accountUPN(ctx, q, oid), []string{outlookClosedSwitchOff}); err != nil {
			return err
		}
	}
	return nil
}
