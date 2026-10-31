package contracts

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/mail"
)

// OutboundMail is Communications' channels as other modules send through them
// (integrations design T4, module-boundaries rule 17): the one way a module
// writes to a *customer* in the tenant's name. A consumer stores the id of a
// channel it chose from Channels and sends with it; Communications resolves
// the channel's provider and sends, synchronously, under the caller's
// context. Nothing is queued on the consumer's behalf — every consumer keeps
// its own evidence and its own retry — and nothing is called inside a
// transaction. The installation's system mailbox (module.Deps.Mail) is never
// a customer's route: it writes to the tenant's own employees.
//
// Channels and Channel make no network call (Communications reads its own
// rows and the provider's own state) and answer to any module without a
// Communications permission — names and addresses, the way a catalog is
// answered to Projects. Send reaches the provider's network, and is never
// called inside a transaction. A single-provider slot.
//
// A module fills it through module.Module.OutboundMail, and Compose and
// Workers resolve the enabled module's into module.Deps.OutboundMail,
// refusing two. It is nil when Communications is off, which a consumer
// answers as "sending is disabled" whether or not its Owner chose a channel.
type OutboundMail interface {
	// Channels lists the active channels a consumer may send from: id,
	// address, display name, the provider's key and display name, and whether
	// the channel can send now — its provider offered, its credential present.
	// An inactive channel is not listed.
	Channels(ctx context.Context) ([]OutboundChannel, error)

	// Channel answers one channel, or ErrChannelUnknown when it is gone or
	// inactive.
	Channel(ctx context.Context, id uuid.UUID) (OutboundChannel, error)

	// Send sends out from the channel, to the recipients out names. The
	// message is sent From the channel's address — out.DisplayName is the
	// sender's name beside it, out.ReplyTo and out.MessageID are kept — and
	// out.MessageID is the idempotency key a provider honours. Errors:
	// ErrChannelUnknown, ErrChannelUnavailable (its provider is off or its
	// credential missing; nothing was sent), ErrMailTooLarge,
	// ErrRecipientRefused (a provider that holds a tenant to a list of
	// addresses, delivery C), or a failure to retry — at least once, as SMTP:
	// a failure after the provider took the message may send it again.
	Send(ctx context.Context, channel uuid.UUID, out mail.Outbound) error
}

// OutboundChannel is one channel as a consumer sees it.
type OutboundChannel struct {
	ID                  uuid.UUID
	Address             string // the From address, as stored
	DisplayName         string // "" when the channel has none
	Provider            string // the provider's key: "smtp", "entra"
	ProviderDisplayName string // the provider as an admin reads it: "SMTP", "Microsoft 365"
	// Available is whether the channel can send now: its provider is offered
	// and, for SMTP, its credentials are stored.
	Available bool
}

var (
	// ErrChannelUnknown: no active channel has the id — never made, or since
	// deactivated. A consumer shows its setting as disabled and acts as if it
	// were.
	ErrChannelUnknown = errors.New("contracts: the channel does not exist")

	// ErrChannelUnavailable: the channel exists but cannot send now — its
	// provider is off, or an SMTP channel has no stored credentials. Nothing
	// was sent.
	ErrChannelUnavailable = errors.New("contracts: the channel cannot send")

	// ErrRecipientRefused: the provider refused a recipient — a test tenant
	// of the hosted provider may write only to its listed addresses. Terminal
	// for the message. Defined with the slot; delivery C's provider answers it.
	ErrRecipientRefused = errors.New("contracts: the channel's provider refused a recipient")
)
