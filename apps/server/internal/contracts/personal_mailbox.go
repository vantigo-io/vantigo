package contracts

import "context"

// MaxPersonalMessageBytes is the largest message, in bytes of MIME,
// PersonalMailbox.Send takes (3 MB of MIME, matching a channel mailbox).
const MaxPersonalMessageBytes int64 = 3_000_000

// MaxPersonalAttachmentBytes is the largest raw attachment total, in bytes,
// personal sending accepts ((3 MB − 16 KiB headroom) × 100 / 137 rounded down
// to 0.1 MB, matching a channel mailbox: 2 100 000 bytes).
const MaxPersonalAttachmentBytes int64 = 2_100_000

// PersonalMailbox is the one sanctioned way a module sends mail from a
// person's own mailbox in an external mail provider (Outlook connection and
// Microsoft 365 mail design M20,
// docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md):
// an in-process port through which Communications checks whether the caller
// can send from their own connected address and sends an outbound RFC 5322
// MIME message through the author's delegated connection, so the message
// lands in the person's own Sent Items with Reply-To set to the
// conversation's channel.
//
// It is optional: module.Deps.PersonalMailboxes is empty when no enabled
// module provides one, which a caller answers as Reason "unavailable". It is
// a many-provider slot keyed by Key (Google Workspace design G2): Compose and
// Workers collect every enabled module's Module.PersonalMailbox, refusing a
// Key two claim. A caller picks as G3 says: it asks every provider's Status
// — each answers "no_account" for a caller whose directory account is not
// its own — and uses the first Available answer, else the first whose
// Reason is not "no_account", else "no_account"; it records the Key of the
// provider it chose with the message and sends through that one only,
// never another (Outlook connection design Q7).
//
// The contract is provider-neutral: no type, method or error names a vendor
// (module-boundaries rule 12).
//
// Five rules hold:
//
//   - The lock rule (module-boundaries rule 10). A caller never calls a
//     method inside one of its own database transactions or while holding a
//     pooled connection across the call: Status reads the provider's tables
//     and Workforce.DirectoryAccount on the shared pool, and Send reaches the
//     external mail provider over the network.
//   - The principal is the context's. Both Status and Send read the user from
//     contracts.UserID(ctx) — the HTTP session's caller for Status and at
//     compose time, and contracts.AsUser(ctx, authorUserID) when the outbox
//     worker sends on the author's behalf.
//   - Incremental delegated consent. A live connection is Available for
//     personal mail only when the mail scope (Mail.Send) is among its granted
//     scopes; a connection active for calendar alone answers Reason
//     "not_connected" on Status and *ConnectionNeededError{Reason:
//     "consent_missing"} on Send without disturbing its calendar state.
//   - Immediate failure on connection needed. When Send answers
//     ErrConnectionNeeded (*ConnectionNeededError), the outbox marks the job
//     failed after one attempt with code "connection_needed" and never falls
//     back to the channel's address.
//   - Size budget. A personal send travels through the same provider MIME
//     endpoint as a channel send, so MaxChannelMessageBytes (3 MB of MIME)
//     and MaxChannelAttachmentBytes (2.1 MB of raw attachments) apply.
type PersonalMailbox interface {
	// Key is the provider's key — the DirectoryAccount.Provider value it
	// serves, "entra" — answered with no context and no network.
	Key() string
	// DisplayName is the mail service's name as a person reads it in the
	// composer ("Outlook"), answered with no context and no network.
	DisplayName() string
	// Status reports whether the caller in contracts.UserID(ctx) can send
	// mail from their own address right now, and the directory address they
	// would send from.
	Status(ctx context.Context) (PersonalMailStatus, error)
	// Send sends mime, a complete RFC 5322 message, from the mailbox of the
	// user in contracts.UserID(ctx) using their delegated connection, and
	// saves it to their Sent Items. bcc are the envelope's blind-copy
	// addresses, which a provider whose MIME send drops a Bcc header
	// delivers through a draft.
	//
	// Errors: *ConnectionNeededError (matching ErrConnectionNeeded) when the
	// switch is off, the user has no enabled directory account, or their
	// connection is missing, closing, waiting to reconnect, lacks the mail
	// scope, or is refused by the provider; ErrMailTooLarge when mime is
	// over MaxMessage or refused by the provider as too large;
	// ErrBccUnsupported when the provider would not keep every blind copy;
	// ErrMailboxDenied when the provider refuses a recipient or the message;
	// ErrMailboxUnavailable when the provider cannot be reached or throttles
	// past retries. On success it answers what the provider knows of the
	// message it sent (SentMessage), the zero value when nothing.
	Send(ctx context.Context, mime []byte, bcc []string) (SentMessage, error)
}

// PersonalMailStatus is what PersonalMailbox.Status answers for the caller.
type PersonalMailStatus struct {
	// Available reports whether the caller can send from their own address
	// right now: the provider and its switch are on, the caller has an
	// enabled directory account, and their live connection is active with the
	// mail scope granted.
	Available bool
	// Reason says why Available is false, "" while Available is true:
	//   - "unavailable": no provider is enabled, the provider's connection is
	//     not working, or its Outlook connection switch is off
	//   - "no_account": the caller has no enabled directory account that the
	//     provider manages
	//   - "not_connected": the caller has not connected their mailbox, or has
	//     connected it without granting the mail scope
	//   - "reconnect": the provider refused the caller's connection and they
	//     must reconnect it
	Reason string
	// ErrorCode is the provider's refusal code when Reason is "reconnect"
	// ("expired", "sign_in_again", "mfa", "blocked", "consent_missing");
	// "" otherwise.
	ErrorCode string
	// Mailbox is the provider's stable identifier of the caller's mailbox,
	// set whenever the switch is on and the caller has an enabled directory
	// account.
	Mailbox string
	// Address is the caller's directory email address, set whenever Mailbox
	// is set.
	Address string
}
