package communications

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the channels' providers (Outlook connection design M12, plan
// reading 13): GET /channel-providers, which the channels page reads instead
// of Entra's API; the resolution of a Microsoft 365 channel's mailbox that
// create, the switch of provider and verify share; and the size budget a
// Microsoft 365 channel's mail is held to when it is written (M13).

// GetCommunicationsChannelProviders List the providers a channel can use
// (GET /api/v1/communications/channel-providers)
//
// SMTP always, available and with no budget of its own; Microsoft 365 only
// while a mail channels provider fills the slot, with its status — available,
// or not with the reason — the tenant_wide warning and MaxMessage.
func (s *server) GetCommunicationsChannelProviders(ctx context.Context, _ gen.GetCommunicationsChannelProvidersRequestObject) (gen.GetCommunicationsChannelProvidersResponseObject, error) {
	out := []gen.ChannelProviderResponse{{Key: providerSMTP, DisplayName: "SMTP", Available: true}}
	m, err := s.channelMailbox(ctx)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return gen.GetCommunicationsChannelProviders200JSONResponse(out), nil
	}
	name, err := s.mailboxDisplayName(ctx, m)
	if err != nil {
		return nil, err
	}
	budget, err := s.mailBudget(ctx, providerEntra)
	if err != nil {
		return nil, err
	}
	entry := gen.ChannelProviderResponse{Key: providerEntra, DisplayName: name, MaxMessageBytes: budget}
	status, err := s.mailboxStatus(ctx, m)
	switch {
	case err != nil:
		s.deps.Logger.WarnContext(ctx, "communications: the mail channels provider's status did not answer", "err", err)
		entry.Reason = apicommon.Ptr("status_unavailable")
	case status.State != contracts.MailboxAvailable:
		entry.Reason = apicommon.Ptr(status.Reason)
	case status.Reason == contracts.MailboxTenantWide:
		entry.Available, entry.Warning = true, apicommon.Ptr(contracts.MailboxTenantWide)
	default:
		entry.Available = true
	}
	return gen.GetCommunicationsChannelProviders200JSONResponse(append(out, entry)), nil
}

// mailboxRefusal is why an address cannot be (or no longer is) a Microsoft
// 365 channel's mailbox: the status create and the switch answer it with,
// and its code and message. Verify answers every one with 422.
type mailboxRefusal struct {
	status        int
	code, message string
}

var (
	refusalMailChannelsOff = &mailboxRefusal{http.StatusConflict, "mail_channels_off",
		"Microsoft 365 mail channels are not available. The Owner turns them on in the Entra module."}
	refusalPersonalMailbox = &mailboxRefusal{http.StatusUnprocessableEntity, "personal_mailbox",
		"The address is a personal mailbox. Only a shared mailbox can be a channel."}
	refusalMailboxDenied = &mailboxRefusal{http.StatusUnprocessableEntity, "mailbox_denied",
		"The application may not use this mailbox. Add it to the mail channels' scope in Exchange Online."}
	refusalMailboxUnknown = &mailboxRefusal{http.StatusUnprocessableEntity, "mailbox_unknown",
		"No mailbox with this address was found in Microsoft 365."}
	refusalProviderUnreachable = &mailboxRefusal{http.StatusServiceUnavailable, "provider_unreachable",
		"Microsoft 365 did not answer. Try again."}
	refusalMailboxChanged = &mailboxRefusal{http.StatusUnprocessableEntity, "mailbox_changed",
		"The address now names another mailbox than the channel's."}
)

// resolveSharedMailbox is the M12 table, run before any transaction opens:
// the provider filled under "entra" and available (else mail_channels_off),
// then Check of address — a shared mailbox it reaches is the answer; a
// personal one, one outside the scope, one unknown or gone, the provider
// unavailable are refusals; anything else (throttled, Graph not answering) is
// provider_unreachable, logged. Check records the mailbox on the provider's
// side, so Send may later write to it.
func (s *server) resolveSharedMailbox(ctx context.Context, address string) (contracts.ChannelMailboxInfo, *mailboxRefusal, error) {
	m, err := s.channelMailbox(ctx)
	if err != nil {
		return contracts.ChannelMailboxInfo{}, nil, err
	}
	if m == nil {
		return contracts.ChannelMailboxInfo{}, refusalMailChannelsOff, nil
	}
	status, err := s.mailboxStatus(ctx, m)
	if errors.Is(err, errCallInsideTransaction) {
		return contracts.ChannelMailboxInfo{}, nil, err
	}
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "communications: the mail channels provider's status did not answer", "err", err)
		return contracts.ChannelMailboxInfo{}, refusalProviderUnreachable, nil
	}
	if status.State != contracts.MailboxAvailable {
		return contracts.ChannelMailboxInfo{}, refusalMailChannelsOff, nil
	}
	info, err := s.mailboxCheck(ctx, m, address)
	switch {
	case err == nil && !info.Shared:
		return contracts.ChannelMailboxInfo{}, refusalPersonalMailbox, nil
	case err == nil:
		return info, nil, nil
	case errors.Is(err, errCallInsideTransaction):
		return contracts.ChannelMailboxInfo{}, nil, err
	case errors.Is(err, contracts.ErrMailboxDenied):
		return contracts.ChannelMailboxInfo{}, refusalMailboxDenied, nil
	case errors.Is(err, contracts.ErrMailboxUnknown), errors.Is(err, contracts.ErrMailboxGone):
		return contracts.ChannelMailboxInfo{}, refusalMailboxUnknown, nil
	case errors.Is(err, contracts.ErrMailboxUnavailable):
		return contracts.ChannelMailboxInfo{}, refusalMailChannelsOff, nil
	default:
		s.deps.Logger.WarnContext(ctx, "communications: the mail channels provider did not answer a mailbox check", "err", err)
		return contracts.ChannelMailboxInfo{}, refusalProviderUnreachable, nil
	}
}

// mailHeadroom is the estimate's allowance for a message's headers and MIME
// boundaries (M13).
const mailHeadroom = 16 * 1024

// base64Grown is n bytes once base64- (or quoted-printable-) encoded, with
// the spec's 37 % margin: × 1.37, rounded up.
func base64Grown(n int64) int64 { return (n*137 + 99) / 100 }

// mailEstimate is a message's size as MIME before it is built (M13): every
// attachment and the HTML body grown by the encoding, the text as it is, and
// the headroom. The HTML part may go base64 or quoted-printable, so it takes
// the margin too (plan reading 15's decision).
func mailEstimate(attachmentSizes []int64, text, html *string) int64 {
	n := int64(mailHeadroom)
	for _, size := range attachmentSizes {
		n += base64Grown(size)
	}
	if text != nil {
		n += int64(len(*text))
	}
	if html != nil {
		n += base64Grown(int64(len(*html)))
	}
	return n
}

// budgetInMB is budget as "about {n} MB" says it: megabytes, as few digits as
// say it exactly.
func budgetInMB(budget int64) string {
	return strconv.FormatFloat(float64(budget)/1e6, 'f', -1, 64)
}
