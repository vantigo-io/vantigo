package entra

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
)

// sendMIME sends mime from the mailbox at base — /users/{id} — with src's
// token, base64-encoded as text/plain, for a channel mailbox (the
// application credential, Outlook connection design M13) and a person's own
// (their delegated token, M20) alike. Without bcc, POST …/sendMail; with
// them, a draft: POST …/messages, its bccRecipients checked to hold every
// address in bcc, then POST …/messages/{id}/send. A draft that lost a blind
// copy, or whose send failed, is deleted best-effort and nothing is sent by
// it. Neither call is retried on a 5xx. answer is a call's error as src's
// owner answers it, before channelRefusal names the contract's.
func (c *graphClient) sendMIME(ctx context.Context, src tokenSource, base string, mime []byte, bcc []string, answer func(error) error) error {
	body := []byte(base64.StdEncoding.EncodeToString(mime))
	if len(bcc) == 0 {
		return channelRefusal(answer(c.postRawAs(ctx, src, base+"/sendMail", "text/plain", body, nil, noRetry)))
	}
	var draft struct {
		ID            string `json:"id"`
		BccRecipients []struct {
			EmailAddress struct {
				Address string `json:"address"`
			} `json:"emailAddress"`
		} `json:"bccRecipients"`
	}
	// The create answers the message whole, its body included: read
	// through graphMaxBatchBody, as a message of MaxMessage may answer more
	// than graphMaxBody.
	if err := c.postRawAs(ctx, src, base+"/messages", "text/plain", body, &draft, noRetry, largeAnswer); err != nil {
		return channelRefusal(answer(err))
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
			c.dropDraft(ctx, src, path)
			return errBccLost
		}
	}
	if err := c.postAs(ctx, src, path+"/send", nil, nil, noRetry); err != nil {
		c.dropDraft(ctx, src, path)
		return channelRefusal(answer(err))
	}
	return nil
}

// dropDraft deletes the draft at path, best-effort and past the caller's
// context's end; a failure is logged by the tenant alone.
func (c *graphClient) dropDraft(ctx context.Context, src tokenSource, path string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), channelDraftCleanup)
	defer cancel()
	if err := c.delAs(ctx, src, path, noWords); err != nil {
		c.logger.WarnContext(ctx, "entra: a mailbox's draft was not deleted",
			"tenant", src.tenant().String(), "error", err.Error())
	}
}
