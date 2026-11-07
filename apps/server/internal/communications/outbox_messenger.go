package communications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is the outbox's messenger branch (Messenger design N6, N7, N12):
// a message on a Messenger channel is sent through the ChannelMessenger filled
// under the channel's provider as ordered parts — the text, each file, then the
// carousel built from the card images —, because Messenger takes one attachment
// per message. The answer options ride on the last part, and a card attachment
// is never a part of its own.
//
// The progress of each part is in messenger_parts, each write a statement of its
// own outside any transaction: attempted_at immediately before the part's Send,
// platform_message_id and sent_at immediately after. A job claimed again skips
// the parts already sent; one with attempted_at and no sent_at is the per-part
// possible duplicate — logged, counted in the existing metric, and sent again:
// the job-level marker's rule (delivery_attempted_at, never stamped on this
// path), applied to each part. Before every part after the first of a run the
// job's lease is renewed by a conditional update on its lease_id; a renewal that
// finds the lease taken stops the job without sending.

// errOutboxLeaseLost is a Messenger job that found its lease taken between two
// parts: it stops, sending and writing nothing more, and the new holder carries
// on from the parts recorded.
var errOutboxLeaseLost = errors.New("communications: the outbox job's lease was taken between two parts")

// messengerPart is one part of a message before it is handed to the provider:
// what the provider is sent, with whether it is the carousel.
type messengerPart struct {
	contracts.MessengerPart
}

// deliverMessenger is deliver's send phase for a Messenger channel, after the
// steps the channels share: the provider, the window checked again, the parts,
// each sent once and recorded, and the completion — the e-mail path's, which
// accepts every delivery and writes relay_accepted. An error is the failure path
// ProcessOne turns into a retry, a hold or a terminal failure (outboxTerminalCode,
// outboxProviderHeld).
func (w *OutboxWorker) deliverMessenger(ctx context.Context, job store.CommunicationsOutboxJob, leaseID string,
	message store.GetOutboundMessageForSendRow, sendable []store.ListMessageDeliveriesForSendRow,
	attachments []store.ListMessageAttachmentsForSendRow) error {
	q := store.New(w.deps.Pool)
	provider, err := w.outboxMessenger(ctx, message.ChannelProvider)
	if err != nil {
		return err
	}
	if provider == nil {
		return fmt.Errorf("communications: no messenger provider under %q: %w", message.ChannelProvider, contracts.ErrMessengerUnavailable)
	}
	status, err := messengerStatus(ctx, provider)
	if err != nil {
		return fmt.Errorf("communications: read the messenger provider's status: %w", err)
	}
	if !status.Available {
		return fmt.Errorf("communications: the messenger provider is not available: %w", contracts.ErrMessengerUnavailable)
	}
	caps, err := messengerCapabilities(ctx, provider)
	if err != nil {
		return err
	}

	// The window again, at send time (N6): a job that missed it fails terminally
	// and does not retry; one past the first 24 hours is sent tagged.
	last, err := latestInbound(ctx, q, message.ConversationID)
	if err != nil {
		return err
	}
	window := replyWindowOf(w.now(), last, caps)
	if window.state == windowClosed {
		return fmt.Errorf("communications: the reply window is closed: %w", contracts.ErrReplyWindowClosed)
	}

	recipient := messengerRecipient(sendable)
	if recipient == "" {
		return errChannelMisconfigured
	}
	parts, err := w.messengerParts(ctx, message, attachments, recipient, window.state == windowHumanAgent)
	if err != nil {
		return err
	}

	recorded, err := q.ListMessengerParts(ctx, job.MessageID)
	if err != nil {
		return fmt.Errorf("communications: list the message's parts: %w", err)
	}
	progress := make(map[int32]store.ListMessengerPartsRow, len(recorded))
	for _, p := range recorded {
		progress[p.PartIndex] = p
	}

	first := true
	for i, part := range parts {
		index := int32(i)
		prior, attempted := progress[index]
		if attempted && prior.SentAt != nil {
			continue
		}
		if !first {
			leaseUntil := w.now().Add(outboxLeaseDuration)
			rows, err := q.RenewOutboxJobLease(ctx, store.RenewOutboxJobLeaseParams{LeaseUntil: &leaseUntil, ID: job.ID, LeaseID: &leaseID})
			if err != nil {
				return fmt.Errorf("communications: renew the outbox job's lease: %w", err)
			}
			if rows == 0 {
				return errOutboxLeaseLost
			}
		}
		first = false
		if attempted && prior.AttemptedAt != nil {
			w.metrics.possibleDuplicateSends.Add(ctx, 1)
			w.logger().Warn("possible duplicate outbound send of a part",
				"worker", outboxWorkerName, "job", job.ID, "message", job.MessageID, "part", index,
				"attemptedAt", *prior.AttemptedAt)
		}
		attemptedAt := w.now()
		if err := q.StampMessengerPartAttempt(ctx, store.StampMessengerPartAttemptParams{
			MessageID: job.MessageID, PartIndex: index, AttemptedAt: &attemptedAt,
		}); err != nil {
			return fmt.Errorf("communications: stamp a part's attempt: %w", err)
		}
		sendCtx, cancel := context.WithTimeout(ctx, outboxSendTimeout)
		sent, err := messengerSend(sendCtx, provider, message.ChannelAddress, part.MessengerPart)
		cancel()
		if err != nil {
			// A part the provider held — it is unavailable — was never handed over:
			// its marker is cleared, as the e-mail path clears the job's, so the next
			// run counts no possible duplicate.
			if errors.Is(err, contracts.ErrMessengerUnavailable) {
				if cerr := q.ClearMessengerPartAttempt(ctx, store.ClearMessengerPartAttemptParams{MessageID: job.MessageID, PartIndex: index}); cerr != nil {
					w.logger().Warn("could not clear a held part's attempt marker",
						"worker", outboxWorkerName, "job", job.ID, "part", index, "error", cerr)
				}
			}
			return fmt.Errorf("communications: send part %d of the outbound message: %w", index, err)
		}
		sentAt := w.now()
		var platformAt *time.Time
		if !sent.At.IsZero() {
			platformAt = ptrTime(sent.At.UTC())
		}
		if err := q.RecordMessengerPartSent(ctx, store.RecordMessengerPartSentParams{
			MessageID: job.MessageID, PartIndex: index, PlatformMessageID: &sent.PlatformMessageID, PlatformAt: platformAt, SentAt: &sentAt,
		}); err != nil {
			return fmt.Errorf("communications: record a part as sent: %w", err)
		}
	}
	if err := w.complete(ctx, job, leaseID, sendable, contracts.SentMessage{}); err != nil {
		return err
	}
	// A watermark that arrived while the message was still being sent found its
	// deliveries 'sending' and moved nothing: the newest of each kind the
	// participant has had is applied now (N2).
	w.reapplyWatermarks(ctx, message.ChannelID, recipient)
	return nil
}

// reapplyWatermarks applies the newest delivered and read watermark of a
// participant again, after a message's completion. A failure is logged: the next
// watermark covers it.
func (w *OutboxWorker) reapplyWatermarks(ctx context.Context, channelID uuid.UUID, address string) {
	q := store.New(w.deps.Pool)
	participant, err := q.FindParticipantIDByAddress(ctx, store.FindParticipantIDByAddressParams{ChannelID: channelID, Address: address})
	if err != nil {
		return
	}
	marks, err := q.ListMessengerWatermarks(ctx, participant)
	if err != nil {
		w.logger().Warn("could not read a participant's watermarks", "worker", outboxWorkerName, "error", err)
		return
	}
	now := w.now()
	byKind := map[string]time.Time{}
	for _, m := range marks {
		byKind[m.Kind] = m.Watermark
	}
	// delivered first, then read, as the sync's events arrive.
	for _, step := range []struct {
		kind string
		from []string
	}{{"delivered", []string{"relay_accepted"}}, {"read", []string{"relay_accepted", "delivered"}}} {
		mark, ok := byKind[step.kind]
		if !ok {
			continue
		}
		err := db.WithTx(ctx, w.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			return applyWatermark(ctx, tx, channelID, participant, step.kind, step.from, mark, now)
		})
		if err != nil {
			w.logger().Warn("could not apply a participant's watermark", "worker", outboxWorkerName, "kind", step.kind, "error", err)
		}
	}
}

// messengerRecipient is the participant the message goes to: the address of the
// first sendable delivery.
func messengerRecipient(sendable []store.ListMessageDeliveriesForSendRow) string {
	for _, d := range sendable {
		if strings.EqualFold(d.RecipientType, "to") {
			return d.RecipientAddress
		}
	}
	return ""
}

// messengerParts is the message as ordered parts (N7): the text when there is
// any, each file attachment, then the carousel built from the card attachments
// and the stored cards. The answer options and the request for an e-mail address
// ride on the last part. A card attachment is never a part of its own.
func (w *OutboxWorker) messengerParts(ctx context.Context, message store.GetOutboundMessageForSendRow,
	attachments []store.ListMessageAttachmentsForSendRow, recipient string, humanAgent bool) ([]messengerPart, error) {
	var components storedComponents
	if len(message.Components) > 0 {
		if err := json.Unmarshal(message.Components, &components); err != nil {
			return nil, fmt.Errorf("communications: decode the message's components: %w", err)
		}
	}
	base := contracts.MessengerPart{MessageID: message.ID.String(), Participant: recipient, HumanAgent: humanAgent}

	var parts []messengerPart
	if message.TextBody != nil && strings.TrimSpace(*message.TextBody) != "" {
		p := base
		p.Text = *message.TextBody
		parts = append(parts, messengerPart{p})
	}
	cardImages := map[uuid.UUID]store.ListMessageAttachmentsForSendRow{}
	for _, a := range attachments {
		if a.Role == "card" {
			cardImages[a.ID] = a
			continue
		}
		content, err := w.attachmentContent(ctx, a.StorageKey)
		if err != nil {
			return nil, err
		}
		p := base
		p.Attachment = &contracts.MessengerAttachment{FileName: a.FileName, ContentType: a.ContentType, Content: content}
		parts = append(parts, messengerPart{p})
	}
	if len(components.Cards) > 0 {
		p := base
		for _, card := range components.Cards {
			c := contracts.MessengerCard{Title: card.Title, Subtitle: card.Subtitle, Choices: choicesOf(card.Choices)}
			if card.AttachmentID != nil {
				image, ok := cardImages[*card.AttachmentID]
				if !ok {
					return nil, errAttachmentsUnavailable
				}
				content, err := w.attachmentContent(ctx, image.StorageKey)
				if err != nil {
					return nil, err
				}
				c.Image = &contracts.MessengerAttachment{FileName: image.FileName, ContentType: image.ContentType, Content: content}
			}
			p.Cards = append(p.Cards, c)
		}
		parts = append(parts, messengerPart{p})
	}
	if len(parts) == 0 {
		return nil, errChannelMisconfigured
	}
	last := &parts[len(parts)-1]
	last.AnswerOptions = choicesOf(components.AnswerOptions)
	last.ShareEmail = components.ShareEmail
	return parts, nil
}

// choicesOf is stored choices as the contract names them.
func choicesOf(choices []storedChoice) []contracts.MessengerChoice {
	var out []contracts.MessengerChoice
	for _, c := range choices {
		out = append(out, contracts.MessengerChoice{ID: c.ID, Title: c.Title})
	}
	return out
}

// outboxMessenger is the messenger provider the outbox sends a channel on
// provider through: the slot when it is filled under that very key, else nil — a
// channel is never sent through a provider under another key. Key makes no
// network call.
func (w *OutboxWorker) outboxMessenger(ctx context.Context, provider string) (contracts.ChannelMessenger, error) {
	if err := noteContractCall(ctx, "ChannelMessenger.Key"); err != nil {
		return nil, err
	}
	return channelMessengerFor(w.deps, provider), nil
}

// messengerTerminalCode is the code of a Messenger failure no retry can mend
// (N12), "" for every other: the platform's answers that the person cannot be
// reached, the window is closed, the Human Agent permission is not granted, or
// the part is refused. ErrMessengerUnavailable is held, a temporary failure and
// any error the contract does not name retried on the backoff.
func messengerTerminalCode(err error) string {
	switch {
	case errors.Is(err, contracts.ErrRecipientUnreachable):
		return "recipient_unreachable"
	case errors.Is(err, contracts.ErrReplyWindowClosed):
		return "reply_window_closed"
	case errors.Is(err, contracts.ErrHumanAgentNotPermitted):
		return "human_agent_not_permitted"
	case errors.Is(err, contracts.ErrMessengerRejected):
		return "rejected"
	}
	return ""
}
