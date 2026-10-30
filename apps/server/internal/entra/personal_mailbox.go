package entra

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// personalMailbox is the module's contracts.PersonalMailbox (Outlook connection
// and Microsoft 365 mail design M20): outbound mail sent from a person's own
// connected Microsoft 365 address through their delegated connection, so it
// lands in their Sent Items with Reply-To set to the channel's address.
type personalMailbox struct {
	deps   module.Deps
	client *graphClient
	tokens *outlookTokens
}

var _ contracts.PersonalMailbox = (*personalMailbox)(nil)

// newPersonalMailbox builds the module's personal mailbox provider over d, for
// Compose and Workers alike.
func newPersonalMailbox(d module.Deps) contracts.PersonalMailbox {
	c := newGraphClient(d)
	return &personalMailbox{deps: d, client: c, tokens: newOutlookTokens(d)}
}

// Status reports whether the caller in contracts.UserID(ctx) can send mail from
// their own address right now.
func (p *personalMailbox) Status(ctx context.Context) (contracts.PersonalMailStatus, error) {
	userID, ok := contracts.UserID(ctx)
	if !ok {
		return contracts.PersonalMailStatus{Available: false, Reason: "unavailable"}, nil
	}
	conn, err := p.tokens.q.GetConnection(ctx)
	if err != nil {
		return contracts.PersonalMailStatus{}, fmt.Errorf("entra: read the connection: %w", err)
	}
	if !conn.OutlookConnect || conn.TenantID == nil || conn.ClientID == nil {
		return contracts.PersonalMailStatus{Available: false, Reason: "unavailable"}, nil
	}
	account, err := p.accountFor(ctx)
	if err != nil {
		return contracts.PersonalMailStatus{}, err
	}
	if account == nil {
		return contracts.PersonalMailStatus{Available: false, Reason: "no_account"}, nil
	}
	status := contracts.PersonalMailStatus{
		Mailbox: account.ObjectID.String(),
		Address: account.UserPrincipalName,
	}
	row, err := p.tokens.q.GetLiveOutlookConnectionByUser(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		status.Reason = "not_connected"
		return status, nil
	}
	if err != nil {
		return contracts.PersonalMailStatus{}, fmt.Errorf("entra: read the Outlook connection: %w", err)
	}
	if row.ObjectID != account.ObjectID || row.State == outlookClosing {
		status.Reason = "not_connected"
		return status, nil
	}
	if row.State == outlookReconnect {
		status.Reason = "reconnect"
		if row.ErrorCode != nil {
			status.ErrorCode = *row.ErrorCode
		}
		return status, nil
	}
	if !hasScopes(row.Scopes, outlookFeatures["mail"]) {
		status.Reason = "not_connected"
		return status, nil
	}
	status.Available = true
	return status, nil
}

// Send sends mime from the mailbox of contracts.UserID(ctx) using their delegated
// connection and saves to Sent Items.
func (p *personalMailbox) Send(ctx context.Context, mime []byte, bcc []string) error {
	userID, ok := contracts.UserID(ctx)
	if !ok {
		return needsPerson(outlookNotConnected)
	}
	if int64(len(mime)) > contracts.MaxPersonalMessageBytes {
		return fmt.Errorf("%w: %d bytes of MIME", contracts.ErrMailTooLarge, len(mime))
	}
	conn, err := p.tokens.q.GetConnection(ctx)
	if err != nil {
		return fmt.Errorf("entra: read the connection: %w", err)
	}
	if !conn.OutlookConnect || conn.TenantID == nil || conn.ClientID == nil {
		return needsPerson(outlookNeedsClosing)
	}
	account, err := p.accountFor(ctx)
	if err != nil {
		return err
	}
	if account == nil {
		return needsPerson(outlookNeedsClosing)
	}
	row, err := p.tokens.q.GetLiveOutlookConnectionByUser(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return needsPerson(outlookNotConnected)
	}
	if err != nil {
		return fmt.Errorf("entra: read the Outlook connection: %w", err)
	}
	if row.ObjectID != account.ObjectID {
		return needsPerson(outlookNeedsClosing)
	}
	if row.State == outlookReconnect {
		reason := outlookCodeSignInAgain
		if row.ErrorCode != nil {
			reason = *row.ErrorCode
		}
		return needsPerson(reason)
	}
	if row.State == outlookClosing {
		return needsPerson(outlookNeedsClosing)
	}
	if !hasScopes(row.Scopes, outlookFeatures["mail"]) {
		return needsPerson(outlookCodeConsentMissing)
	}

	src := mailboxSource{delegatedSource: p.tokens.sourceWithFeature(account.ObjectID, "mail", false)}
	body := []byte(base64.StdEncoding.EncodeToString(mime))
	base := "/users/" + account.ObjectID.String()
	if len(bcc) == 0 {
		return channelRefusal(src.answer(p.client.postRawAs(ctx, src, base+"/sendMail", "text/plain", body, nil, noRetry)))
	}
	return p.sendThroughDraft(ctx, src, base, body, bcc)
}

// sendThroughDraft sends body — base64 MIME carrying a Bcc header — as a
// draft created in the user's mailbox at base: POST …/messages, checks bccRecipients,
// then POST …/messages/{id}/send.
func (p *personalMailbox) sendThroughDraft(ctx context.Context, src mailboxSource, base string, body []byte, bcc []string) error {
	var draft struct {
		ID            string `json:"id"`
		BccRecipients []struct {
			EmailAddress struct {
				Address string `json:"address"`
			} `json:"emailAddress"`
		} `json:"bccRecipients"`
	}
	if err := p.client.postRawAs(ctx, src, base+"/messages", "text/plain", body, &draft, noRetry, largeAnswer); err != nil {
		return channelRefusal(src.answer(err))
	}
	if draft.ID == "" || len(draft.ID) > mailboxMaxID {
		return fmt.Errorf("%w: the draft's id", errTenantUnreachable)
	}
	path := base + "/messages/" + url.PathEscape(draft.ID)
	for _, want := range bcc {
		kept := false
		for _, r := range draft.BccRecipients {
			if strings.EqualFold(strings.TrimSpace(r.EmailAddress.Address), strings.TrimSpace(want)) {
				kept = true
				break
			}
		}
		if !kept {
			p.dropDraft(ctx, src, path)
			return errBccLost
		}
	}
	if err := p.client.postAs(ctx, src, path+"/send", nil, nil, noRetry); err != nil {
		p.dropDraft(ctx, src, path)
		return channelRefusal(src.answer(err))
	}
	return nil
}

// dropDraft deletes the draft at path, best-effort.
func (p *personalMailbox) dropDraft(ctx context.Context, src mailboxSource, path string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), channelDraftCleanup)
	defer cancel()
	_ = p.client.delAs(ctx, src, path, noWords)
}

// accountFor returns the directory account for the user in context, checked to be
// trusted and enabled in entra.accounts.
func (p *personalMailbox) accountFor(ctx context.Context) (*store.EntraAccount, error) {
	if p.deps.Workforce == nil {
		return nil, nil
	}
	da, err := p.deps.Workforce.DirectoryAccount(ctx)
	if err != nil {
		return nil, fmt.Errorf("entra: read the directory account: %w", err)
	}
	if da == nil || da.Provider != providerKey || !da.Enabled {
		return nil, nil
	}
	oid, ok := objectID(da.ExternalID)
	if !ok {
		return nil, nil
	}
	acc, err := p.tokens.q.GetAccount(ctx, oid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("entra: read the account: %w", err)
	}
	if !trusted(acc) || acc.State != stateEnabled {
		return nil, nil
	}
	return &acc, nil
}
