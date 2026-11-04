package communications

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file fills contracts.OutboundMail (integrations design T4,
// module-boundaries rule 17): Communications' channels as Invoices and Calendar
// send customer mail through them. A consumer stores the id of a channel it
// chose from Channels and sends with it; Send resolves the channel's provider
// and sends, synchronously, under the caller's context — SMTP over the
// channel's own sealed credentials, a mailbox channel through the
// ChannelMailbox filled under its provider — never inside a transaction and never on the
// installation's own SMTP credentials, which are the system mailbox's, for the
// tenant's employees (T5).
//
// Channels and Channel read this module's own rows and the providers' own
// state and make no network call. They answer to any module without a
// Communications permission: names and addresses only.

// providerNameSMTP is the SMTP provider's name as an admin reads it, the one
// GET /channel-providers answers.
const providerNameSMTP = "SMTP"

// knownProviderNames names a mail channels provider no module fills now by
// the name its module gave it, rather than by its key: Entra's, the one
// provider channels had before the providers became many (Google Workspace
// design G4).
var knownProviderNames = map[string]string{"entra": "Microsoft 365"}

// channelTypeEmail is the channel type OutboundMail lists: the mail type.
const channelTypeEmail = "email"

// outboundMail is the module's contracts.OutboundMail over its pool and the
// providers composed beside it.
type outboundMail struct {
	deps module.Deps
}

var _ contracts.OutboundMail = (*outboundMail)(nil)

// newOutboundMail builds the module's OutboundMail over d. It holds no state
// of its own, so api mode with WORKERS_IN_PROCESS builds it twice, once for
// Compose and once for Workers, and the two answer alike.
func newOutboundMail(d module.Deps) contracts.OutboundMail {
	return &outboundMail{deps: d}
}

// mailboxProvider is a filled mail channels provider as a consumer's channel
// reads it: the provider, its display name and whether it is offered now
// (its Status says available).
type mailboxProvider struct {
	m       contracts.ChannelMailbox
	name    string
	offered bool
}

// mailboxProviders is every filled mail channels provider by its Key. Key,
// DisplayName and Status make no network call.
func (o *outboundMail) mailboxProviders(ctx context.Context) (map[string]mailboxProvider, error) {
	out := make(map[string]mailboxProvider, len(o.deps.ChannelMailboxes))
	for _, m := range o.deps.ChannelMailboxes {
		if m == nil {
			continue
		}
		if err := noteContractCall(ctx, "ChannelMailbox.Key"); err != nil {
			return nil, err
		}
		p, err := o.mailboxProviderOf(ctx, m)
		if err != nil {
			return nil, err
		}
		out[m.Key()] = p
	}
	return out, nil
}

// mailboxProviderFor is the provider filled under key, ok false when none
// is: a channel is never served by a provider under another key.
func (o *outboundMail) mailboxProviderFor(ctx context.Context, key string) (mailboxProvider, bool, error) {
	if err := noteContractCall(ctx, "ChannelMailbox.Key"); err != nil {
		return mailboxProvider{}, false, err
	}
	m := channelMailboxFor(o.deps, key)
	if m == nil {
		return mailboxProvider{}, false, nil
	}
	p, err := o.mailboxProviderOf(ctx, m)
	return p, err == nil, err
}

// mailboxProviderOf is m's name and whether it is offered now. A status that
// does not answer is not offered, and logged.
func (o *outboundMail) mailboxProviderOf(ctx context.Context, m contracts.ChannelMailbox) (mailboxProvider, error) {
	if err := noteContractCall(ctx, "ChannelMailbox.DisplayName"); err != nil {
		return mailboxProvider{}, err
	}
	p := mailboxProvider{m: m, name: m.DisplayName()}
	status, err := syncMailboxStatus(ctx, m)
	if errors.Is(err, errCallInsideTransaction) {
		return mailboxProvider{}, err
	}
	if err != nil {
		o.deps.Logger.WarnContext(ctx, "communications: the mail channels provider's status did not answer",
			"provider", m.Key(), "err", err)
		return p, nil
	}
	p.offered = status.State == contracts.MailboxAvailable
	return p, nil
}

// outboundChannelOf is row as a consumer sees it: whether it can send is its
// provider filled and offered and its own credentials or mailbox present.
// cred is the SMTP credential's settings, nil when the channel has none;
// providers the filled mail channels providers by Key.
func outboundChannelOf(row channelRow, cred *string, providers map[string]mailboxProvider) (contracts.OutboundChannel, error) {
	out := contracts.OutboundChannel{ID: row.ID, Address: row.Address, Provider: row.Provider}
	if row.DisplayName != nil {
		out.DisplayName = *row.DisplayName
	}
	if !isMailboxProvider(row.Provider) {
		out.ProviderDisplayName = providerNameSMTP
		out.Available = cred != nil
		return out, nil
	}
	p, filled := providers[row.Provider]
	out.ProviderDisplayName = row.Provider
	if name, known := knownProviderNames[row.Provider]; known {
		out.ProviderDisplayName = name
	}
	if filled && p.name != "" {
		out.ProviderDisplayName = p.name
	}
	mailbox, err := channelMailboxOf(row.Settings)
	if err != nil {
		return contracts.OutboundChannel{}, err
	}
	out.Available = filled && p.offered && mailbox != ""
	return out, nil
}

// listed reports whether a channel row is one OutboundMail answers: an active
// channel of the mail type.
func listed(row channelRow) bool {
	return row.IsActive && row.Type == channelTypeEmail
}

// Channels lists the active mail channels, oldest first, each with whether it
// can send now.
func (o *outboundMail) Channels(ctx context.Context) ([]contracts.OutboundChannel, error) {
	providers, err := o.mailboxProviders(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := store.New(o.deps.Pool).ListChannels(ctx)
	if err != nil {
		return nil, fmt.Errorf("communications: list the channels for a consumer: %w", err)
	}
	out := make([]contracts.OutboundChannel, 0, len(rows))
	for _, row := range rows {
		r := channelRowFromList(row)
		if !listed(r) {
			continue
		}
		c, err := outboundChannelOf(r, row.CredentialSettingsJson, providers)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// Channel answers one channel, ErrChannelUnknown when none active of the mail
// type has the id.
func (o *outboundMail) Channel(ctx context.Context, id uuid.UUID) (contracts.OutboundChannel, error) {
	row, err := o.channelRow(ctx, id)
	if err != nil {
		return contracts.OutboundChannel{}, err
	}
	providers := map[string]mailboxProvider{}
	if isMailboxProvider(row.Provider) {
		p, filled, err := o.mailboxProviderFor(ctx, row.Provider)
		if err != nil {
			return contracts.OutboundChannel{}, err
		}
		if filled {
			providers[row.Provider] = p
		}
	}
	return outboundChannelOf(channelRowFromGet(row), row.CredentialSettingsJson, providers)
}

// channelRow reads the channel a consumer named: ErrChannelUnknown for an id
// that is not one, or whose channel is inactive or not of the mail type.
func (o *outboundMail) channelRow(ctx context.Context, id uuid.UUID) (store.GetChannelByIDRow, error) {
	row, err := store.New(o.deps.Pool).GetChannelByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.GetChannelByIDRow{}, contracts.ErrChannelUnknown
	}
	if err != nil {
		return store.GetChannelByIDRow{}, fmt.Errorf("communications: read channel %s for a consumer: %w", id, err)
	}
	if !listed(channelRowFromGet(row)) {
		return store.GetChannelByIDRow{}, contracts.ErrChannelUnknown
	}
	return row, nil
}

// Send sends out from the channel through the channel's provider, From its
// address with the caller's name beside it, the caller's Reply-To and
// Message-ID kept. A channel that cannot send is refused before anything is
// built or dialled.
func (o *outboundMail) Send(ctx context.Context, id uuid.UUID, out mail.Outbound) error {
	row, err := o.channelRow(ctx, id)
	if err != nil {
		return err
	}
	if !isMailboxProvider(row.Provider) {
		return o.sendSMTP(ctx, row, out)
	}
	return o.sendMailbox(ctx, row, out)
}

// sendSMTP is Send over an SMTP channel's own credentials: the very reading
// the outbox makes of them (smtpConfigOf), through the same seam
// (Deps.SMTPSend, else mail.SendOutbound). No credential row is
// ErrChannelUnavailable — the channel is Unavailable, never the host's SMTP.
func (o *outboundMail) sendSMTP(ctx context.Context, row store.GetChannelByIDRow, out mail.Outbound) error {
	if row.CredentialSettingsJson == nil {
		return fmt.Errorf("%w: %w", contracts.ErrChannelUnavailable, errChannelNoCredentials)
	}
	cred, err := store.New(o.deps.Pool).GetChannelCredentialByChannelID(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("communications: read channel %s's credential: %w", row.ID, err)
	}
	cfg, err := smtpConfigOf(o.deps.Secrets, row.Address, row.CredentialSettingsJson, &cred.SecretCiphertext)
	if err != nil {
		return fmt.Errorf("communications: channel %s's credential: %w", row.ID, err)
	}
	if err := o.smtpSender()(ctx, cfg, out); err != nil {
		return fmt.Errorf("communications: send through channel %s: %w", row.ID, err)
	}
	return nil
}

// smtpSender is Deps.SMTPSend, or mail.SendOutbound when no harness
// substituted one — the outbox's seam.
func (o *outboundMail) smtpSender() func(context.Context, config.MailConfig, mail.Outbound) error {
	if o.deps.SMTPSend != nil {
		return o.deps.SMTPSend
	}
	return mail.SendOutbound
}

// sendMailbox is Send over a mailbox channel: the message rendered as
// MIME from the channel's address and handed to the provider's Send with the
// mailbox the channel names. A provider that is not filled or not offered, and
// a mailbox it will not write to, are ErrChannelUnavailable; a message too
// large for it is ErrMailTooLarge.
func (o *outboundMail) sendMailbox(ctx context.Context, row store.GetChannelByIDRow, out mail.Outbound) error {
	mailbox, err := channelMailboxOf(row.Settings)
	if err != nil {
		return err
	}
	if mailbox == "" {
		return fmt.Errorf("%w: %w", contracts.ErrChannelUnavailable, errChannelMisconfigured)
	}
	p, filled, err := o.mailboxProviderFor(ctx, row.Provider)
	if err != nil {
		return err
	}
	m := p.m
	if !filled || !p.offered {
		return fmt.Errorf("%w: the mail channels provider is not available", contracts.ErrChannelUnavailable)
	}
	var opts []mail.RenderOption
	if len(out.Bcc) > 0 {
		opts = append(opts, mail.WithBcc())
	}
	mime, err := mail.RenderMIME(row.Address, out, opts...)
	if err != nil {
		return err
	}
	_, err = mailboxSend(ctx, m, mailbox, mime, out.Bcc)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, contracts.ErrMailTooLarge):
		return fmt.Errorf("communications: send through channel %s: %w", row.ID, err)
	case errors.Is(err, contracts.ErrMailboxUnavailable), errors.Is(err, contracts.ErrMailboxUnknown),
		errors.Is(err, contracts.ErrMailboxDenied), errors.Is(err, contracts.ErrMailboxGone):
		return fmt.Errorf("%w: %w", contracts.ErrChannelUnavailable, err)
	default:
		return fmt.Errorf("communications: send through channel %s: %w", row.ID, err)
	}
}
