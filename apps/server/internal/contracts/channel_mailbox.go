package contracts

import (
	"context"
	"errors"
	"time"
)

// ChannelMailbox is a provider's mailboxes as Communications' channels use them
// (Outlook connection design M11, rule 15): Entra's Exchange Online shared
// mailboxes today. Communications is the authority on which mailboxes are
// channels; the provider writes to, and reads from, only a mailbox Check
// resolved. Key, DisplayName, Status and MaxMessage make no network call;
// Check, Forget, Send, Delta and Raw do, and are never called inside a
// transaction. A single-provider slot.
//
// A module fills it through module.Module.ChannelMailbox, and Compose and
// Workers resolve the enabled module's into module.Deps.ChannelMailbox,
// refusing two. A channel names its provider by Key and its mailbox by the
// object id Check answered. The errors are MailboxCalendar's —
// ErrMailboxUnavailable, ErrMailboxUnknown, ErrMailboxDenied, ErrMailboxGone,
// and ErrMailboxDeltaExpired for Delta —, ErrMailTooLarge, ErrBccUnsupported
// and ErrMailItemGone; anything else is a failure to retry, never an answer.
//
// It declares only what Communications calls today; the notices arrive with
// the delivery that calls them.
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

	// Check resolves address to a mailbox the provider reaches and, when it
	// is a shared mailbox, records it, so Send may write to it:
	// ErrMailboxUnknown when no such mailbox exists, ErrMailboxGone when its
	// user has no mailbox, ErrMailboxDenied when the provider's scope does
	// not reach it, ErrMailboxUnavailable when Status is not available. It
	// answers a personal mailbox too (Shared false) but never records one,
	// so Send refuses it; the caller decides what it accepts.
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
	// the provider refuses it as too large; ErrBccUnsupported when the
	// provider would not keep every blind copy, nothing sent. Acceptance is
	// not delivery, and
	// a failure after the provider took the message may send it again on a
	// retry: at least once, as SMTP.
	Send(ctx context.Context, mailbox string, mime []byte, bcc []string) error

	// Delta reads one page of the changes to folder ("inbox") of mailbox, a
	// mailbox Check recorded: link "" starts afresh with the messages
	// received at or after since; any other link is one an earlier page
	// answered. Items are at most MailDeltaPageMax, in no promised order.
	// ErrMailboxDeltaExpired when the provider no longer takes link; the
	// errors of Send otherwise. No message content is read.
	Delta(ctx context.Context, mailbox, folder, link string, since time.Time) (MailDelta, error)

	// Raw answers the message whole, as RFC 5322 bytes, at most max of them:
	// ErrMailTooLarge above, ErrMailItemGone when the message is no longer
	// there; the errors of Send otherwise.
	Raw(ctx context.Context, mailbox, messageID string, max int64) ([]byte, error)
}

// MailDelta is one page Delta answered.
type MailDelta struct {
	Items []MailItem
	Link  string // with Done the delta link to keep; else the next page's
	Done  bool
}

// MailItem is one message a Delta page names; no content of it.
type MailItem struct {
	ID             string    // the provider's immutable id; opaque
	ReceivedAt     time.Time // UTC; zero when the provider answered none that reads
	MessageID      string    // the Internet Message-Id as the provider answers it, "" if none
	ConversationID string    // the provider's thread id; opaque, "" if none
	Removed        bool      // gone from the folder: ID alone is set
}

// MailDeltaPageMax is the most items one Delta page holds.
const MailDeltaPageMax = 50

// ErrMailItemGone: the message Raw was asked for is no longer in the mailbox
// — deleted or moved between the delta and the read. Terminal for the message.
var ErrMailItemGone = errors.New("contracts: the mail item is gone")

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

// ErrBccUnsupported: the provider would not carry every blind copy of the
// message, so it sent nothing rather than send it without them. Terminal for
// the message.
var ErrBccUnsupported = errors.New("contracts: the mailbox provider would not keep every blind copy")

// MailboxTenantWide is MailboxStatus.Reason while available but the provider's grant
// reaches every mailbox in the tenant (M10): a warning, never a refusal.
const MailboxTenantWide = "tenant_wide"
