package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is the module's contracts.PersonalMailbox (Google Workspace
// design G35; delivery G, docs/superpowers/plans/2026-11-03-google-personal-sending.md):
// a message Communications sends from a person's own Gmail address, through
// their own Google connection with the mail feature (gmail.send, delivery D)
// — never through the service account's delegation — so it lands in their
// own Sent with Reply-To the channel, which Communications has written into
// the MIME. The Outlook design's M20 and Q5–Q7 hold unchanged: a connection
// that cannot serve the send is contracts.ErrConnectionNeeded at once, and
// nothing is ever sent from another address instead.
//
// Status reads the module's rows and Staff's DirectoryAccount, no network.
// Send checks the same rows, then takes the person's token from
// personalTokens (the account checked on use, the revision-guarded refresh)
// and uploads the MIME through F's gmailUpload. Nothing here logs an
// address, a token, the message or Google's words: the person is named by
// their Directory id.

// personalMailFeature is the feature of the person's connection a send asks
// for (G26): gmail.send.
const personalMailFeature = "mail"

// personalMailboxName is the mail service a person reads in the composer's
// "Send from my Gmail address".
const personalMailboxName = "Gmail"

// metadataScopes are the scopes that let a person's token read a message's
// headers back (users.messages.get, format=metadata). The mail feature asks
// gmail.send alone, which reads nothing; a connection holding one of these
// besides has the Message-ID Gmail sent with read back (U6).
var metadataScopes = []string{
	"https://mail.google.com/",
	scopePrefix + "gmail.modify",
	scopePrefix + "gmail.readonly",
	scopePrefix + "gmail.metadata",
}

// personalMailbox is the module's contracts.PersonalMailbox: the people's
// tokens and the module's client and rows.
type personalMailbox struct {
	deps   module.Deps
	c      *googleClient
	q      *store.Queries
	tokens *personalTokens
}

var _ contracts.PersonalMailbox = (*personalMailbox)(nil)

// newPersonalMailbox builds the module's personal mailbox provider over d,
// for Compose and Workers alike.
func newPersonalMailbox(d module.Deps) contracts.PersonalMailbox {
	t := newPersonalTokens(d)
	return &personalMailbox{deps: d, c: t.c, q: t.q, tokens: t}
}

// Key is "google", with no context and no network.
func (p *personalMailbox) Key() string { return providerKey }

// DisplayName is "Gmail", with no context and no network.
func (p *personalMailbox) DisplayName() string { return personalMailboxName }

// Status reports whether the caller in contracts.UserID(ctx) can send from
// their own Gmail address now (G35, M20): unavailable while Connection for
// people is off or the web client is not ready; no_account without the
// caller's certain account (G27); then, with Mailbox the account's
// Directory id and Address its primary email, not_connected without a live
// connection of that account or with one lacking gmail.send, reconnect with
// the connection's code, else available.
func (p *personalMailbox) Status(ctx context.Context) (contracts.PersonalMailStatus, error) {
	userID, ok := contracts.UserID(ctx)
	if !ok {
		return contracts.PersonalMailStatus{Reason: "unavailable"}, nil
	}
	conn, creds, err := readFooting(ctx, p.q)
	if err != nil {
		return contracts.PersonalMailStatus{}, err
	}
	if !personalOffered(conn, creds) {
		return contracts.PersonalMailStatus{Reason: "unavailable"}, nil
	}
	account, err := certainAccountOf(ctx, p.deps.Workforce, p.q)
	if err != nil {
		return contracts.PersonalMailStatus{}, err
	}
	if account == nil {
		return contracts.PersonalMailStatus{Reason: "no_account"}, nil
	}
	status := contracts.PersonalMailStatus{Mailbox: account.UserID, Address: account.PrimaryEmail}
	row, err := p.q.GetLivePersonalByUser(ctx, userID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		status.Reason = "not_connected"
		return status, nil
	case err != nil:
		return contracts.PersonalMailStatus{}, fmt.Errorf("google: read the personal connection: %w", err)
	case row.GoogleUserID != account.UserID, row.State == personalClosing:
		status.Reason = "not_connected"
		return status, nil
	case row.State == personalReconnect:
		status.Reason = "reconnect"
		status.ErrorCode = personalCodeSignInAgain
		if row.ErrorCode != nil {
			status.ErrorCode = *row.ErrorCode
		}
		return status, nil
	case !hasScopes(row.Scopes, personalFeatures[personalMailFeature]):
		status.Reason = "not_connected"
		return status, nil
	}
	status.Available = true
	return status, nil
}

// Send sends mime from the Gmail mailbox of contracts.UserID(ctx) through
// their own connection (G35). It checks with no call what the token cannot:
// the size (MaxPersonalMessageBytes, P4), Connection for people and the web
// client, the caller's certain account, that the caller's own live
// connection is that account's — never another person's row — and that the
// MIME's Bcc header carries every blind copy (Gmail delivers the header's).
// The rest — reconnect, closing, the account on use and gmail.send — is the
// token's to decide (personalTokens.issue, feature mail), consent_missing
// leaving a connection the calendar still uses as it is. Gmail files the
// message under the person's SENT. It answers Gmail's thread id, and the
// Message-ID Gmail sent the message with only when the connection's scopes
// read it back (metadataScopes): gmail.send alone does not.
func (p *personalMailbox) Send(ctx context.Context, mime []byte, bcc []string) (contracts.SentMessage, error) {
	userID, ok := contracts.UserID(ctx)
	if !ok {
		return contracts.SentMessage{}, needsPerson(contracts.ConnectionNotConnected)
	}
	if int64(len(mime)) > contracts.MaxPersonalMessageBytes {
		return contracts.SentMessage{}, fmt.Errorf("%w: %d bytes of MIME", contracts.ErrMailTooLarge, len(mime))
	}
	conn, creds, err := readFooting(ctx, p.q)
	if err != nil {
		return contracts.SentMessage{}, err
	}
	if !personalOffered(conn, creds) {
		return contracts.SentMessage{}, needsPerson(contracts.ConnectionClosingReason)
	}
	account, err := certainAccountOf(ctx, p.deps.Workforce, p.q)
	if err != nil {
		return contracts.SentMessage{}, err
	}
	if account == nil {
		return contracts.SentMessage{}, needsPerson(contracts.ConnectionClosingReason)
	}
	row, err := p.q.GetLivePersonalByUser(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return contracts.SentMessage{}, needsPerson(contracts.ConnectionNotConnected)
	}
	if err != nil {
		return contracts.SentMessage{}, fmt.Errorf("google: read the personal connection: %w", err)
	}
	if row.GoogleUserID != account.UserID {
		return contracts.SentMessage{}, needsPerson(contracts.ConnectionNotConnected)
	}
	if len(bcc) > 0 && !bccCarried(mime, bcc) {
		return contracts.SentMessage{}, fmt.Errorf("%w: the MIME's Bcc header does not carry every blind copy", contracts.ErrBccUnsupported)
	}

	gid := row.GoogleUserID
	src := p.tokens.source(gid, personalMailFeature, false)
	sent, err := gmailUpload(ctx, p.c, src, gid, mime)
	if err != nil {
		return contracts.SentMessage{}, p.refused(ctx, src, err)
	}
	out := contracts.SentMessage{ThreadID: sent.threadID()}
	if !validGmailID(sent.ID) || !slices.ContainsFunc(row.Scopes, func(s string) bool { return slices.Contains(metadataScopes, s) }) {
		return out, nil
	}
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), readBackBudget)
	defer cancel()
	meta, err := readMetadata(readCtx, p.c, gmailSession{gid: gid, modify: src}, sent.ID)
	if err != nil {
		p.c.logger.WarnContext(ctx, "google: the Message-ID of a personal send was not read back",
			"google_user_id", gid, "error", err.Error())
		return out, nil
	}
	out.MessageID = meta.messageID()
	return out, nil
}

// refused is err, the upload's failure, as the contract answers it (M20):
//
//   - the person's connection unable to serve it is ErrConnectionNeeded as
//     the token answered it;
//   - a 401 — Google refused the person's token after the client fetched a
//     fresh one — moves the connection to reconnect (sign_in_again) at the
//     revision the token came from (personalTokens.unauthorized), and is
//     ErrConnectionNeeded;
//   - the web client not set up or refused is ErrMailboxUnavailable, the
//     Owner's to mend;
//   - a 413 is ErrMailTooLarge; a 400 — Gmail refusing a recipient or the
//     message — or a 403 that is no usage limit is ErrMailboxDenied;
//   - a usage limit past the client's one wait, no answer and a 5xx
//     (errGoogleUnreachable) are ErrMailboxUnavailable: the outbox holds the
//     message, never sending it another way;
//   - anything else passes, a failure the outbox retries.
func (p *personalMailbox) refused(ctx context.Context, src *personalSource, err error) error {
	var needed *contracts.ConnectionNeededError
	var g *googleError
	switch {
	case errors.As(err, &needed):
		return err
	case errors.Is(err, errWebClient), errors.Is(err, errThrottled), errors.Is(err, errGoogleUnreachable):
		return fmt.Errorf("%w: %w", contracts.ErrMailboxUnavailable, err)
	case !errors.As(err, &g):
		return err
	case g.Status == http.StatusUnauthorized:
		// unauthorized answers ErrConnectionNeeded once the row moved, and
		// errGoogleUnreachable when a newer connect had moved it first.
		err := p.tokens.unauthorized(ctx, src.gid, src.lastRevision())
		if errors.Is(err, errGoogleUnreachable) {
			return fmt.Errorf("%w: %w", contracts.ErrMailboxUnavailable, err)
		}
		return err
	case g.Status == http.StatusRequestEntityTooLarge:
		return fmt.Errorf("%w: %w", contracts.ErrMailTooLarge, err)
	case g.Status == http.StatusBadRequest, g.Status == http.StatusForbidden:
		return fmt.Errorf("%w: %w", contracts.ErrMailboxDenied, err)
	}
	return err
}
