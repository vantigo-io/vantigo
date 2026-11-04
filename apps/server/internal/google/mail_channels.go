package google

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/google/gen"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
)

// This file is Mail channels' switch (Google Workspace design G33; delivery
// F, docs/superpowers/plans/2026-11-03-google-mail-channels.md): the Owner's
// PUT /connection/mail-channels, and the state the switch, the connection
// and the last test make together — what GET /connection answers as
// mailChannels and what the channel mailbox provider's Status reads.

// mailNotTested is Mail channels' reason while no test has answered for the
// Gmail scopes since the switch was turned on.
const mailNotTested = "not_tested"

// mailChannelsState is whether Mail channels can work now, from the rows
// and no network, and why not: switch_off while the switch is off; the
// connection's reason — or its status when it names none — while it is not
// connected; not_delegated while the last test was refused a Gmail scope;
// not_tested while it neither granted nor refused them (the switch turned
// on since); "" and true otherwise.
func mailChannelsState(conn store.GoogleConnection, creds []store.GoogleCredential) (bool, string) {
	if !conn.MailChannels {
		return false, reasonSwitchOff
	}
	status, reason := statusOf(conn, creds)
	if status != statusConnected {
		if reason == "" {
			reason = status
		}
		return false, reason
	}
	switch {
	case slices.ContainsFunc(gmailScopes, func(s string) bool { return slices.Contains(conn.ScopesMissing, s) }):
		return false, reasonNotDelegated
	case slices.ContainsFunc(gmailScopes, func(s string) bool { return !slices.Contains(conn.ScopesGranted, s) }):
		return false, mailNotTested
	}
	return true, ""
}

// mailChannelsBody is Mail channels as GET /connection answers it: off
// while the switch is; unavailable with its reason while a channel cannot
// work; ready otherwise; and how many mailboxes a channel was checked
// against.
func mailChannelsBody(conn store.GoogleConnection, creds []store.GoogleCredential, mailboxes int64) gen.GoogleMailChannels {
	body := gen.GoogleMailChannels{Enabled: conn.MailChannels, Status: gen.GoogleMailChannelsStatusOff, Mailboxes: int32(min(mailboxes, 1<<31-1))}
	if !conn.MailChannels {
		return body
	}
	if ok, reason := mailChannelsState(conn, creds); !ok {
		body.Status = gen.GoogleMailChannelsStatusUnavailable
		body.Reason = &reason
		return body
	}
	body.Status = gen.GoogleMailChannelsStatusReady
	return body
}

// PutGoogleConnectionMailChannels turns Mail channels on or off (G33), the
// Owner's. The mailboxes already checked are kept either way: while it is
// off every Gmail channel is unavailable and Communications holds it. The
// same value again writes nothing. Audited google.mail_channels.switched.
// (PUT /api/v1/google/connection/mail-channels)
func (s *server) PutGoogleConnectionMailChannels(ctx context.Context, req gen.PutGoogleConnectionMailChannelsRequestObject) (gen.PutGoogleConnectionMailChannelsResponseObject, error) {
	if req.Body == nil {
		return nil, errors.New("google: Mail channels without a body")
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
		if conn.MailChannels != enabled {
			if conn, err = q.SetMailChannels(ctx, enabled); err != nil {
				return fmt.Errorf("google: set Mail channels: %w", err)
			}
			code := "off"
			if enabled {
				code = "on"
			}
			if err := recordEvent(ctx, q, now, actor, actionMailChannelsSwitched, "", []string{code}); err != nil {
				return err
			}
		}
		body, err = readConnection(ctx, q, s.deps.Config, conn)
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.PutGoogleConnectionMailChannels200JSONResponse(body), nil
}
