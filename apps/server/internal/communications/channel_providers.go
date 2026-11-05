package communications

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the channels' providers (Outlook connection design M12, plan
// reading 13; Google Workspace design G4): GET /channel-providers, which the
// channels page reads instead of any provider's API; the resolution of a
// mailbox channel's mailbox that create, the switch of provider and verify
// share; and the size budget a mailbox channel's mail is held to when it is
// written (M13). No provider is named here: a mailbox channel's provider is
// the Key of a filled ChannelMailbox, and every sentence names it by its
// DisplayName.

// GetCommunicationsChannelProviders List the providers a channel can use
// (GET /api/v1/communications/channel-providers)
//
// SMTP always, available and with no budget of its own; then every filled
// mail channels provider in slot order, with its status — available, or not
// with the reason — the tenant_wide warning and MaxMessage; then every filled
// messenger provider, of type messenger, with its status and the Pages it has
// connected.
func (s *server) GetCommunicationsChannelProviders(ctx context.Context, _ gen.GetCommunicationsChannelProvidersRequestObject) (gen.GetCommunicationsChannelProvidersResponseObject, error) {
	out := []gen.ChannelProviderResponse{{Key: providerSMTP, DisplayName: "SMTP", Available: true, Type: apicommon.Ptr(channelTypeEmail)}}
	for _, m := range s.deps.ChannelMailboxes {
		if m == nil {
			continue
		}
		if err := noteContractCall(ctx, "ChannelMailbox.Key"); err != nil {
			return nil, err
		}
		entry, err := s.channelProviderEntry(ctx, m)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	for _, m := range s.deps.ChannelMessengers {
		if m == nil {
			continue
		}
		if err := noteContractCall(ctx, "ChannelMessenger.Key"); err != nil {
			return nil, err
		}
		entry, err := s.messengerProviderEntry(ctx, m)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return gen.GetCommunicationsChannelProviders200JSONResponse(out), nil
}

// messengerProviderEntry is one filled messenger provider as
// /channel-providers lists it: its status — available, or not with the reason —
// and, while it is available, the Pages it has connected for the form's
// picker. A status or Pages that do not answer are not offered, and logged.
func (s *server) messengerProviderEntry(ctx context.Context, m contracts.ChannelMessenger) (gen.ChannelProviderResponse, error) {
	key := m.Key()
	name, err := s.messengerDisplayName(ctx, m)
	if err != nil {
		return gen.ChannelProviderResponse{}, err
	}
	entry := gen.ChannelProviderResponse{Key: key, DisplayName: name, Type: apicommon.Ptr(channelTypeMessenger)}
	status, err := messengerStatus(ctx, m)
	switch {
	case errors.Is(err, errCallInsideTransaction):
		return gen.ChannelProviderResponse{}, err
	case err != nil:
		s.deps.Logger.WarnContext(ctx, "communications: the messenger provider's status did not answer",
			"provider", key, "err", err)
		entry.Reason = apicommon.Ptr("status_unavailable")
		return entry, nil
	case !status.Available:
		entry.Reason = apicommon.Ptr(status.Reason)
		return entry, nil
	}
	entry.Available = true
	endpoints, err := messengerEndpoints(ctx, m)
	switch {
	case errors.Is(err, errCallInsideTransaction):
		return gen.ChannelProviderResponse{}, err
	case err != nil:
		s.deps.Logger.WarnContext(ctx, "communications: the messenger provider's Pages did not answer",
			"provider", key, "err", err)
		entry.Available, entry.Reason = false, apicommon.Ptr("endpoints_unavailable")
		return entry, nil
	}
	pages := make([]gen.ChannelEndpointResponse, 0, len(endpoints))
	for _, e := range endpoints {
		page := gen.ChannelEndpointResponse{Address: e.Address, DisplayName: e.DisplayName, Available: e.Available}
		if !e.Available {
			page.Reason = apicommon.Ptr(e.Reason)
		}
		pages = append(pages, page)
	}
	entry.Endpoints = &pages
	return entry, nil
}

// channelProviderEntry is one filled mail channels provider as
// /channel-providers lists it.
func (s *server) channelProviderEntry(ctx context.Context, m contracts.ChannelMailbox) (gen.ChannelProviderResponse, error) {
	key := m.Key()
	name, err := s.mailboxDisplayName(ctx, m)
	if err != nil {
		return gen.ChannelProviderResponse{}, err
	}
	budget, err := s.mailBudget(ctx, channelTypeEmail, key)
	if err != nil {
		return gen.ChannelProviderResponse{}, err
	}
	entry := gen.ChannelProviderResponse{Key: key, DisplayName: name, MaxMessageBytes: budget, Type: apicommon.Ptr(channelTypeEmail)}
	status, err := s.mailboxStatus(ctx, m)
	switch {
	case errors.Is(err, errCallInsideTransaction):
		return gen.ChannelProviderResponse{}, err
	case err != nil:
		s.deps.Logger.WarnContext(ctx, "communications: the mail channels provider's status did not answer",
			"provider", key, "err", err)
		entry.Reason = apicommon.Ptr("status_unavailable")
	case status.State != contracts.MailboxAvailable:
		entry.Reason = apicommon.Ptr(status.Reason)
	case status.Reason == contracts.MailboxTenantWide:
		entry.Available, entry.Warning = true, apicommon.Ptr(contracts.MailboxTenantWide)
	default:
		entry.Available = true
	}
	return entry, nil
}

// mailboxRefusal is why an address cannot be (or no longer is) a mailbox
// channel's mailbox: the status create and the switch answer it with, and
// its code and message. Verify answers every one with 422.
type mailboxRefusal struct {
	status        int
	code, message string
}

// The refusals that name the provider take its DisplayName (G4); a
// provider no longer filled is named by its key.
func refusalMailChannelsOff(provider string) *mailboxRefusal {
	return &mailboxRefusal{http.StatusConflict, "mail_channels_off", fmt.Sprintf(
		"Mail channels through %s are not available. An administrator turns them on under Integrations.", provider)}
}

func refusalMailboxUnknown(provider string) *mailboxRefusal {
	return &mailboxRefusal{http.StatusUnprocessableEntity, "mailbox_unknown",
		fmt.Sprintf("No mailbox with this address was found in %s.", provider)}
}

func refusalProviderUnreachable(provider string) *mailboxRefusal {
	return &mailboxRefusal{http.StatusServiceUnavailable, "provider_unreachable",
		fmt.Sprintf("%s did not answer. Try again.", provider)}
}

// refusalPersonalMailbox and refusalMailboxDenied name the provider, as the
// other refusals do: the words are the same for each, never Exchange's alone.
func refusalPersonalMailbox(provider string) *mailboxRefusal {
	return &mailboxRefusal{http.StatusUnprocessableEntity, "personal_mailbox", fmt.Sprintf(
		"The address is a person's own mailbox in %s. Only a mailbox that is no person's own can be a channel.", provider)}
}

func refusalMailboxDenied(provider string) *mailboxRefusal {
	return &mailboxRefusal{http.StatusUnprocessableEntity, "mailbox_denied", fmt.Sprintf(
		"%s does not let Vantigo use this mailbox. An administrator gives the access as the mail channels section of the %s administration guide describes.",
		provider, provider)}
}

var (
	refusalGroupAddress = &mailboxRefusal{http.StatusUnprocessableEntity, "group_address",
		"The address is a group. A group has no mailbox Vantigo can read; use a licensed mailbox."}
	refusalMailboxChanged = &mailboxRefusal{http.StatusUnprocessableEntity, "mailbox_changed",
		"The address now names another mailbox than the channel's."}
)

// resolveSharedMailbox is the M12 table, run before any transaction opens:
// the provider filled under provider and available (else mail_channels_off),
// then Check of address — a shared mailbox it reaches is the answer; a
// personal one, a group, one outside the scope, one unknown or gone, the
// provider unavailable are refusals; anything else (throttled, the provider
// not answering) is provider_unreachable, logged. Check records the mailbox
// on the provider's side, so Send may later write to it.
func (s *server) resolveSharedMailbox(ctx context.Context, provider, address string) (contracts.ChannelMailboxInfo, *mailboxRefusal, error) {
	m, err := s.channelMailbox(ctx, provider)
	if err != nil {
		return contracts.ChannelMailboxInfo{}, nil, err
	}
	if m == nil {
		return contracts.ChannelMailboxInfo{}, refusalMailChannelsOff(provider), nil
	}
	name, err := s.mailboxDisplayName(ctx, m)
	if err != nil {
		return contracts.ChannelMailboxInfo{}, nil, err
	}
	status, err := s.mailboxStatus(ctx, m)
	if errors.Is(err, errCallInsideTransaction) {
		return contracts.ChannelMailboxInfo{}, nil, err
	}
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "communications: the mail channels provider's status did not answer",
			"provider", provider, "err", err)
		return contracts.ChannelMailboxInfo{}, refusalProviderUnreachable(name), nil
	}
	if status.State != contracts.MailboxAvailable {
		return contracts.ChannelMailboxInfo{}, refusalMailChannelsOff(name), nil
	}
	info, err := s.mailboxCheck(ctx, m, address)
	switch {
	case err == nil && !info.Shared:
		return contracts.ChannelMailboxInfo{}, refusalPersonalMailbox(name), nil
	case err == nil:
		return info, nil, nil
	case errors.Is(err, errCallInsideTransaction):
		return contracts.ChannelMailboxInfo{}, nil, err
	case errors.Is(err, contracts.ErrMailboxGroup):
		return contracts.ChannelMailboxInfo{}, refusalGroupAddress, nil
	case errors.Is(err, contracts.ErrMailboxDenied):
		return contracts.ChannelMailboxInfo{}, refusalMailboxDenied(name), nil
	case errors.Is(err, contracts.ErrMailboxUnknown), errors.Is(err, contracts.ErrMailboxGone):
		return contracts.ChannelMailboxInfo{}, refusalMailboxUnknown(name), nil
	case errors.Is(err, contracts.ErrMailboxUnavailable):
		return contracts.ChannelMailboxInfo{}, refusalMailChannelsOff(name), nil
	default:
		s.deps.Logger.WarnContext(ctx, "communications: the mail channels provider did not answer a mailbox check",
			"provider", provider, "err", err)
		return contracts.ChannelMailboxInfo{}, refusalProviderUnreachable(name), nil
	}
}

// The refusals of a Messenger channel's Page, as the mailbox's: create and
// verify answer them with the status, the code and the message.
func refusalMessengerOff(provider string) *mailboxRefusal {
	return &mailboxRefusal{http.StatusConflict, "messenger_off", fmt.Sprintf(
		"Messenger channels through %s are not available. An administrator turns them on under Integrations.", provider)}
}

func refusalEndpointUnknown(provider string) *mailboxRefusal {
	return &mailboxRefusal{http.StatusUnprocessableEntity, "endpoint_unknown",
		fmt.Sprintf("No Page with this id is connected in %s.", provider)}
}

func refusalEndpointUnavailable(provider string) *mailboxRefusal {
	return &mailboxRefusal{http.StatusUnprocessableEntity, "endpoint_unavailable",
		fmt.Sprintf("The Page is not available in %s right now.", provider)}
}

// resolveMessengerEndpoint is resolveSharedMailbox for a Messenger channel,
// run before any transaction opens: the provider filled under provider and
// available (else messenger_off), then Check of address — a Page it has
// connected and that is available is the answer; an unknown one, an
// unavailable one, the provider unavailable are refusals; anything else is
// provider_unreachable, logged.
func (s *server) resolveMessengerEndpoint(ctx context.Context, provider, address string) (contracts.MessengerEndpoint, *mailboxRefusal, error) {
	m, err := s.channelMessenger(ctx, provider)
	if err != nil {
		return contracts.MessengerEndpoint{}, nil, err
	}
	if m == nil {
		return contracts.MessengerEndpoint{}, refusalMessengerOff(provider), nil
	}
	name, err := s.messengerDisplayName(ctx, m)
	if err != nil {
		return contracts.MessengerEndpoint{}, nil, err
	}
	status, err := messengerStatus(ctx, m)
	if errors.Is(err, errCallInsideTransaction) {
		return contracts.MessengerEndpoint{}, nil, err
	}
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "communications: the messenger provider's status did not answer",
			"provider", provider, "err", err)
		return contracts.MessengerEndpoint{}, refusalProviderUnreachable(name), nil
	}
	if !status.Available {
		return contracts.MessengerEndpoint{}, refusalMessengerOff(name), nil
	}
	endpoint, err := messengerCheck(ctx, m, address)
	switch {
	case err == nil && !endpoint.Available:
		return contracts.MessengerEndpoint{}, refusalEndpointUnavailable(name), nil
	case err == nil:
		return endpoint, nil, nil
	case errors.Is(err, errCallInsideTransaction):
		return contracts.MessengerEndpoint{}, nil, err
	case errors.Is(err, contracts.ErrMessengerEndpointUnknown):
		return contracts.MessengerEndpoint{}, refusalEndpointUnknown(name), nil
	case errors.Is(err, contracts.ErrMessengerUnavailable):
		return contracts.MessengerEndpoint{}, refusalMessengerOff(name), nil
	default:
		s.deps.Logger.WarnContext(ctx, "communications: the messenger provider did not answer a Page check",
			"provider", provider, "err", err)
		return contracts.MessengerEndpoint{}, refusalProviderUnreachable(name), nil
	}
}

// mailHeadroom is the estimate's allowance for a message's headers and MIME
// boundaries (M13).
const mailHeadroom = 16 * 1024

// base64Grown is n bytes once base64-encoded, with the spec's 37 % margin:
// × 1.37, rounded up.
func base64Grown(n int64) int64 { return (n*137 + 99) / 100 }

// qpGrown is n bytes once quoted-printable-encoded at its worst, every byte
// written =XX: × 3.
func qpGrown(n int64) int64 { return 3 * n }

// mailEstimate is a message's size as MIME before it is built (M13): every
// attachment grown by base64, the text and the HTML bodies at
// quoted-printable's worst — a body of non-ASCII letters, each =XX twice —,
// and the headroom (the review's finding: plan reading 15's 1.37 for the
// HTML and none for the text could pass a message the provider refuses).
func mailEstimate(attachmentSizes []int64, text, html *string) int64 {
	n := int64(mailHeadroom)
	for _, size := range attachmentSizes {
		n += base64Grown(size)
	}
	if text != nil {
		n += qpGrown(int64(len(*text)))
	}
	if html != nil {
		n += qpGrown(int64(len(*html)))
	}
	return n
}

// attachmentAllowance is the attachments a budget of MIME takes with no
// body, as a reader is shown it: (budget − headroom) × 100 / 137, rounded
// down to 0.1 MB — 2 100 000 bytes of Entra's 3 000 000. One figure for the
// staging's refusal and the composer (maxAttachmentBytes).
func attachmentAllowance(budget int64) int64 {
	raw := max(budget-mailHeadroom, 0) * 100 / 137
	return raw / 100_000 * 100_000
}

// bytesInMB is n bytes as "about {n} MB" says it: megabytes, as few digits
// as say it exactly.
func bytesInMB(n int64) string {
	return strconv.FormatFloat(float64(n)/1e6, 'f', -1, 64)
}
