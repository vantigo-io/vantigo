package contracts

import (
	"context"
	"errors"
)

// ChannelMailbox is a provider's mailboxes as Communications' channels use them
// (Outlook connection design M11, rule 15): Entra's Exchange Online shared
// mailboxes today. Communications is the authority on which mailboxes are
// channels; the provider writes only to a mailbox Check resolved. Key, DisplayName,
// Status and MaxMessage make no network call; Check, Forget and Send do, and are
// never called inside a transaction. A single-provider slot.
//
// A module fills it through module.Module.ChannelMailbox, and Compose and
// Workers resolve the enabled module's into module.Deps.ChannelMailbox,
// refusing two. A channel names its provider by Key and its mailbox by the
// object id Check answered. The errors are MailboxCalendar's —
// ErrMailboxUnavailable, ErrMailboxUnknown, ErrMailboxDenied, ErrMailboxGone —
// and ErrMailTooLarge; anything else is a failure to retry, never an answer.
//
// It declares only what Communications calls today; the inbound reads and
// the notices arrive with the deliveries that call them.
type ChannelMailbox interface {
	// Key is the provider's key — a channel's provider value, "entra" —
	// answered with no context and no network.
	Key() string

	// DisplayName is the provider's name as an admin reads it,
	// "Microsoft 365".
	DisplayName() string

	// Status is whether channels may use the provider now: MailboxAvailable
	// — with Reason MailboxTenantWide when the provider's grant reaches every
	// mailbox in the tenant — or MailboxUnavailable with a reason code
	// ("switch_off", the connection's reason). It reads the provider's own
	// state and never the network.
	Status(ctx context.Context) (MailboxStatus, error)

	// Check resolves address to a mailbox the provider reaches and records
	// it, so Send may write to it: ErrMailboxUnknown when no such mailbox
	// exists, ErrMailboxGone when its user has no mailbox, ErrMailboxDenied
	// when the provider's scope does not reach it, ErrMailboxUnavailable
	// when Status is not available. It answers a personal mailbox too
	// (Shared false); the caller decides what it accepts.
	Check(ctx context.Context, address string) (ChannelMailboxInfo, error)

	// Forget drops a mailbox Check recorded, so nothing is sent through it
	// again until another Check. Idempotent: a mailbox not recorded is done.
	Forget(ctx context.Context, mailbox string) error

	// MaxMessage is the largest message, in bytes of MIME, Send takes.
	MaxMessage() int64

	// Send sends mime — the message whole, its headers and its parts — from
	// mailbox, a mailbox Check recorded (ErrMailboxUnknown otherwise), to
	// the recipients its headers name and to bcc, the blind copies the MIME
	// may not carry. ErrMailTooLarge when mime is larger than MaxMessage or
	// the provider refuses it as too large. Acceptance is not delivery, and
	// a failure after the provider took the message may send it again on a
	// retry: at least once, as SMTP.
	Send(ctx context.Context, mailbox string, mime []byte, bcc []string) error
}

// ChannelMailboxInfo is the mailbox Check resolved.
type ChannelMailboxInfo struct {
	Mailbox     string // the object id, lower case
	Address     string // the mailbox's primary SMTP address, lower case
	DisplayName string
	Shared      bool // userPurpose = shared
}

// ErrMailTooLarge: the message is larger than MaxMessage, or the provider refused it
// as too large (413). Terminal for the message.
var ErrMailTooLarge = errors.New("contracts: the message is larger than the mailbox provider takes")

// MailboxTenantWide is MailboxStatus.Reason while available but the provider's grant
// reaches every mailbox in the tenant (M10): a warning, never a refusal.
const MailboxTenantWide = "tenant_wide"
