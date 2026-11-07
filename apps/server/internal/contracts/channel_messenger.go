package contracts

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"
)

// ChannelMessenger is a messaging provider as Communications' channels use it
// (Messenger design N2, docs/superpowers/specs/2026-11-05-communications-messenger-design.md):
// Facebook Messenger through a Facebook Page today. Where ChannelMailbox is a
// mailbox read as a delta of mail, a messenger is a person-to-person chat: the
// provider queues what it receives in an inbox of its own, which Communications
// reads from a cursor and acknowledges, and it sends one part of a message at a
// time. Communications is the authority on which Pages are channels; the provider
// sends through, and reads from, only an address Check resolved.
//
// It is a many-provider slot keyed by Key, collected as ChannelMailboxes are: a
// module fills it through module.Module.ChannelMessenger, and Compose and
// Workers collect every enabled module's into module.Deps.ChannelMessengers,
// refusing a Key two claim — and a Key that is also filled in the ChannelMailbox
// slot, because a channel's provider names one kind of provider. A channel of
// type messenger names its provider by Key and its Page by address, the Page's
// id. A caller picks the provider whose Key the channel's provider names, and
// none when no provider is filled under it.
//
// Key, DisplayName, Status and Capabilities make no network call; every other
// method is a provider call, and is never made inside a transaction. The errors
// are the Err* below; a temporary failure is wrapped by MessengerTemporary, and
// anything else Send answers is a failure to retry on the outbox's backoff.
//
// The cursor Pending and Ack speak of is the provider's own insertion id, a
// bigserial and never a timestamp, because webhooks arrive out of order; the
// timestamp of an event only orders the display. Attachments cross the boundary
// through OpenAttachment, never as a storage key: Communications copies the bytes
// into its own storage before the transaction that names them, and after Ack
// the provider deletes its own copy.
type ChannelMessenger interface {
	// Key is the provider's key — a channel's provider value, "meta" —
	// answered with no context and no network.
	Key() string

	// DisplayName is the provider's name as an admin reads it, "Facebook
	// Messenger".
	DisplayName() string

	// Status is whether channels may use the provider now: Available, or
	// not with a reason code ("not_configured"). It reads the provider's own
	// state and never the network.
	Status(ctx context.Context) (MessengerStatus, error)

	// Capabilities is what a message on the provider may carry and how long
	// a reply may follow the person's last message: the limits of design N4
	// and the window of N6. No network.
	Capabilities() MessengerCapabilities

	// Endpoints is the Pages the provider has connected, each with its own
	// status, for the channel form's picker.
	Endpoints(ctx context.Context) ([]MessengerEndpoint, error)

	// Check resolves address, a Page's id, to a connected Page: its name,
	// and available or the reason it is not. ErrMessengerEndpointUnknown
	// when no such Page is connected; ErrMessengerUnavailable when Status is
	// not available.
	Check(ctx context.Context, address string) (MessengerEndpoint, error)

	// Send sends one part of a message from the Page address to
	// part.Participant. MessengerSent carries the platform's id of what it
	// sent, which matches answers and echoes to the message. ErrRecipientUnreachable,
	// ErrReplyWindowClosed, ErrHumanAgentNotPermitted and
	// ErrMessengerRejected are terminal; ErrMessengerUnavailable holds the job;
	// a MessengerTemporary error is retried; acceptance is not delivery, and a
	// failure after the platform took the part may send it again on a retry.
	Send(ctx context.Context, address string, part MessengerPart) (MessengerSent, error)

	// Notified answers the addresses of the Pages with events not yet
	// acknowledged that arrived after since: the provider's own rows, no
	// platform call. A notice is a nudge and nothing more — Pending stays the
	// truth.
	Notified(ctx context.Context, since time.Time) ([]string, error)

	// Pending reads the events of the Page after the cursor, oldest first, at
	// most limit.
	Pending(ctx context.Context, address string, after int64, limit int) ([]MessengerEvent, error)

	// OpenAttachment opens the attachment the event named by ref, with what
	// is known of the file. The caller closes the reader.
	OpenAttachment(ctx context.Context, address, ref string) (io.ReadCloser, MessengerFile, error)

	// Ack tells the provider every event of the Page up to and including upTo
	// is in Communications; it deletes its own copies. Idempotent.
	Ack(ctx context.Context, address string, upTo int64) error

	// Profile is what the platform knows of one of the Page's participants,
	// by the platform's id for the person: a display name.
	Profile(ctx context.Context, address, participant string) (MessengerProfile, error)
}

// MessengerStatus is the provider's state as Status answers it.
type MessengerStatus struct {
	// Available is whether channels may use the provider now.
	Available bool
	// Reason is a code — never the platform's words — for a provider that is
	// not available; "" while it is.
	Reason string
}

// MessengerCapabilities is what a message on the provider may carry (Messenger
// design N4) and the reply window (N6). Communications checks a reply against
// it: a zero value, which every e-mail channel has, takes no components.
type MessengerCapabilities struct {
	// MaxText is the most characters one message's text holds (Messenger: 2000).
	MaxText int
	// MaxAnswerOptions is the most answer options (quick replies) one message
	// holds, MaxAnswerTitle the most characters in a title (Messenger: 13, 20).
	MaxAnswerOptions int
	MaxAnswerTitle   int
	// ShareEmail is whether the provider has the quick reply that asks the
	// person for an e-mail address.
	ShareEmail bool
	// MaxCards is the most cards a carousel holds, MaxCardChoices the most
	// choices of one card, MaxCardTitle and MaxCardSubtitle the most
	// characters of a card's text, MaxChoiceTitle of a choice's (Messenger: 10,
	// 3, 80, 80, 20). Zero MaxCards: the provider has no carousel.
	MaxCards        int
	MaxCardChoices  int
	MaxCardTitle    int
	MaxCardSubtitle int
	MaxChoiceTitle  int
	// MaxAttachmentBytes is the largest file one part carries (Messenger: 25 MB);
	// zero when the provider names none.
	MaxAttachmentBytes int64
	// PublicMedia is whether the provider fetches a card's image from an
	// address on the public host (MessengerCard.ImageURL) instead of being
	// handed its bytes (Messenger: true, design N18).
	PublicMedia bool
	// Window is how long after the participant's newest message a reply may
	// be sent (Messenger: 24 h).
	Window time.Duration
	// HumanAgentWindow is the longer window, with the provider's Human Agent
	// permission, in which a reply after Window is sent tagged; zero when
	// the provider has not been granted it (N6).
	HumanAgentWindow time.Duration
}

// Components is whether the provider takes components on a message at all.
func (c MessengerCapabilities) Components() bool {
	return c.MaxAnswerOptions > 0 || c.ShareEmail || c.MaxCards > 0
}

// MessengerEndpoint is a Page Endpoints lists or Check resolved.
type MessengerEndpoint struct {
	// Address is the Page's id.
	Address     string
	DisplayName string
	// Available is whether a channel on the Page can send and receive now;
	// Reason is a code for one that cannot ("token_rejected", "removed").
	Available bool
	Reason    string
}

// MessengerChoice is one answer option, or one choice of a card: an id unique
// within its message, and the title the person sees.
type MessengerChoice struct {
	ID    string
	Title string
}

// MessengerAttachment is a file one part carries.
type MessengerAttachment struct {
	FileName    string
	ContentType string
	Content     []byte
}

// MessengerCard is one card of a carousel part: its image, copied into
// Communications' storage, and its choices. A provider whose Capabilities ask
// for public media is given ImageURL, an address on the instance's public host
// that serves the image for a time under a signed token (design N18); any
// other is given the bytes in Image. Never both.
type MessengerCard struct {
	Title    string
	Subtitle string
	Image    *MessengerAttachment
	ImageURL string
	Choices  []MessengerChoice
}

// MessengerPart is one thing Send sends: Communications splits a message into
// ordered parts, the text, each file, then the carousel, and the answer options
// ride on the last part (design N7). A part carries one of Text, Attachment and
// Cards, and Text beside the answer options of a text part.
type MessengerPart struct {
	// MessageID is the id of the Communications message the part belongs to,
	// which MessengerPayload puts in the payload of each choice.
	MessageID string
	// Participant is the platform's id for the person the part is sent to.
	Participant string
	Text        string
	Attachment  *MessengerAttachment
	Cards       []MessengerCard
	// AnswerOptions and ShareEmail ride on the last part of a message.
	AnswerOptions []MessengerChoice
	ShareEmail    bool
	// HumanAgent is whether the part is sent after Window, tagged for the
	// provider's Human Agent permission.
	HumanAgent bool
}

// MessengerSent is what the platform answered of a part it took: its own id for
// the part.
type MessengerSent struct {
	PlatformMessageID string
	// At is the platform's time for the part, which a delivered or read watermark
	// is compared with; the zero value when the platform answered none.
	At time.Time
}

// The kinds of MessengerEvent.Kind.
const (
	// MessengerEventMessage is a message of the participant's, or an echo.
	MessengerEventMessage = "message"
	// MessengerEventDelivered carries a watermark: every message sent to the
	// participant at or before it is delivered.
	MessengerEventDelivered = "delivered"
	// MessengerEventRead is MessengerEventDelivered's for read.
	MessengerEventRead = "read"
)

// MessengerAnswer is the answer to a question Communications asked, parsed from
// the payload the platform sent back (MessengerPayload). An e-mail address the
// person shared in answer to the quick reply that asks for one carries no
// payload of ours: its answer is ChoiceID MessengerShareEmail, with no MessageID,
// and the address as the event's Text.
type MessengerAnswer struct {
	// MessageID is the Communications message the question was in.
	MessageID string
	ChoiceID  string
}

// MessengerShareEmail is the ChoiceID of an e-mail address a person shared.
const MessengerShareEmail = "share-email"

// MessengerAttachmentRef names one attachment of an event, for OpenAttachment.
type MessengerAttachmentRef struct {
	Ref         string
	FileName    string
	ContentType string
}

// MessengerEvent is one event Pending answers.
type MessengerEvent struct {
	// Cursor is the provider's insertion id, ascending.
	Cursor int64
	// Kind is MessengerEventMessage, MessengerEventDelivered or MessengerEventRead.
	Kind string
	// Participant is the platform's id for the person: the sender of a
	// message, the recipient of an echo, the person a watermark is about.
	Participant string
	// At is when the platform says the event happened; it orders the display.
	At time.Time
	// PlatformMessageID is the platform's id of a message, which dedupes it.
	PlatformMessageID string
	Text              string
	Attachments       []MessengerAttachmentRef
	// Answer is set when the person tapped an answer option or a card's
	// choice; Text is then the title they saw. nil for any other message, and
	// for one whose payload the provider could not read as ours.
	Answer *MessengerAnswer
	// Echo marks a message the Page sent — from Meta Business Suite, say, or
	// by Communications itself: it is not a message of the participant's.
	Echo bool
	// Watermark is the time a delivered or read event is about.
	Watermark time.Time
}

// MessengerFile is what OpenAttachment knows of the file it opened.
type MessengerFile struct {
	FileName    string
	ContentType string
	Size        int64
}

// MessengerProfile is what Profile answers.
type MessengerProfile struct {
	DisplayName string
}

// The payload the platform carries for each answer option and card choice is
// "v1:<messageId>:<choiceId>" (design N4): well under Messenger's limit of
// 1000 characters. A provider builds it with MessengerPayload and reads one
// back with ParseMessengerPayload.
const messengerPayloadVersion = "v1"

// MessengerPayload is the payload of one choice of one message.
func MessengerPayload(messageID, choiceID string) string {
	return messengerPayloadVersion + ":" + messageID + ":" + choiceID
}

// ParseMessengerPayload is the answer a payload carries; ok is false for one
// that is not ours — an unknown version, or no message or choice.
func ParseMessengerPayload(payload string) (answer MessengerAnswer, ok bool) {
	parts := strings.Split(payload, ":")
	if len(parts) != 3 || parts[0] != messengerPayloadVersion || parts[1] == "" || parts[2] == "" {
		return MessengerAnswer{}, false
	}
	return MessengerAnswer{MessageID: parts[1], ChoiceID: parts[2]}, true
}

// ErrMessengerUnavailable: the provider is not available now — switched off,
// not configured, or its Page unreachable. Send answers it for the outbox to
// hold the job; Check, Endpoints and the reads answer it for a refusal.
var ErrMessengerUnavailable = errors.New("contracts: the messenger provider is not available")

// ErrMessengerEndpointUnknown: no Page is connected under the address Check
// was asked for.
var ErrMessengerEndpointUnknown = errors.New("contracts: the messenger provider has no such Page")

// ErrRecipientUnreachable: the person cannot be written to — they blocked the
// Page, say. Terminal for the message.
var ErrRecipientUnreachable = errors.New("contracts: the recipient cannot be reached")

// ErrReplyWindowClosed: the platform refused the part because the reply window
// is closed. Terminal for the message.
var ErrReplyWindowClosed = errors.New("contracts: the reply window is closed")

// ErrHumanAgentNotPermitted: a part sent tagged for the Human Agent permission
// was refused for lack of it. Terminal for the message; the provider's status
// tells an admin to clear the setting.
var ErrHumanAgentNotPermitted = errors.New("contracts: the Human Agent permission is not granted")

// ErrMessengerRejected: the platform refused the part for a reason no retry
// mends and no sentinel above names. Terminal for the message.
var ErrMessengerRejected = errors.New("contracts: the platform rejected the message")

// MessengerTemporaryError is a failure a retry may mend — throttled, a 5xx, a
// network error — which a provider wraps with MessengerTemporary.
type MessengerTemporaryError struct{ Err error }

func (e *MessengerTemporaryError) Error() string {
	return "contracts: temporary messenger failure: " + e.Err.Error()
}
func (e *MessengerTemporaryError) Unwrap() error { return e.Err }

// MessengerTemporary wraps err as a temporary failure.
func MessengerTemporary(err error) error { return &MessengerTemporaryError{Err: err} }

// IsMessengerTemporary reports whether err is, or wraps, a temporary failure.
func IsMessengerTemporary(err error) bool {
	var temporary *MessengerTemporaryError
	return errors.As(err, &temporary)
}
