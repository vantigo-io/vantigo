package communications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	netmail "net/mail"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/events"
)

// This file is the composer's Reply operation — Reply -> QueueOutboundAsync
// (EP/ConversationEndpoints.cs:96-101, :264-338; communications inventory
// §2's "Reply -> QueueOutboundAsync" bullet, §5.5, §19.1 item 3, §19.2 item
// 16; design doc §1.1, §3): postCommunicationsConversationsByIdReply, the
// densest ordering in the module. The inventory's own transcription
// (`:264-338`) is ten steps, of which task 7's brief named only one; all
// ten are pinned in conversations_reply_test.go:
//
//  1. handler antiforgery -> 400 csrf_validation_failed. Not ported here —
//     inventory §19.2 item 9 says to implement CSRF once, at the platform
//     middleware layer, never per-handler; no other operation in this
//     module implements it either (conversations_create.go,
//     conversations_notes.go, attachments.go all skip it the same way).
//  2. validateReply -> 400 invalid_request + fields.
//  3. Idempotency-Key header validity -> 400 idempotency_key_required.
//  4. conversation (+ channel) lookup -> 404 bare.
//  5. !channel.IsActive -> 422 channel_inactive.
//  6. idempotency replay -> 200 same-fingerprint / 409 idempotency_key_reused.
//  7. no inbound participant -> 422 recipients_missing.
//  8. staged-attachment preflight -> 409 attachments_not_ready.
//  9. suppression check -> 422 recipient_suppressed + fields.recipients.
//  10. inside the transaction, the clean -> claimed conditional claim -> 409
//     attachments_not_ready again (the race fence a preflight-only
//     implementation would leave open between check and claim).
//
// A reply on a Messenger conversation (Messenger design N4, N6, N7) takes the
// same steps with the e-mail-only inputs refused or ignored: sendFromPersonal
// and replyMode reply_all are 422 not_supported_on_channel, the subject and the
// HTML are ignored, the text is at most 2000 characters (422 text_too_long),
// the components are checked against the provider's Capabilities (422
// components_unsupported, components_invalid), the person answered is the
// conversation's participant and no Cc exists, and a conversation whose reply
// window is closed answers 422 reply_window_closed — after the idempotency
// replay, which an already-accepted reply still gets.
//
// Step 7 finds the latest inbound message with a sender: the mailbox sync
// (mailbox_sync.go) writes inbound messages for a mailbox channel, so a
// conversation it imported is answered; one this module's own API created,
// with no message received, is still refused there. A reply carries the
// threading headers of the message it answers (In-Reply-To, References), and
// reply-all its Cc (resolveReplyRecipients).
//
// History: fix round 1 first tried to reach steps 8-10 with an overridable
// recipient-resolution seam on *server, because at the time no fixture could
// construct a qualifying row at all — the CHECK narrowed
// conversation_messages.direction to ('outbound', 'internal_note'). Fix round
// 2 reverted that narrowing (design doc §1.1's correction), and the seam was
// deleted (server.go's own comment on the removed field has the fuller
// history).

// errAttachmentsNotReadyRace is queueReply's own signal that the step-10
// claim (inside the transaction) affected fewer rows than the step-8
// preflight promised — the TOCTOU race the second gate exists to close
// (inventory §5.5 item 2). db.WithTx propagates it as the transaction's
// error, which is then translated back to the same 409 attachments_not_ready
// the preflight itself answers, and the transaction rolls back (nothing
// from this reply is written).
var errAttachmentsNotReadyRace = errors.New("communications: a staged attachment was claimed or expired between the preflight and the transaction")

// rfcMessageIDFor is EmailMessageId.For(message.Id) (`SV/EmailMessageId.cs`,
// used at `:388`): a deterministic Message-Id from the message's own id.
// Duplicated from conversations_create.go's identical inline expression
// rather than factored out, matching this codebase's preference for each
// operation's file staying self-contained (attachments.go's hexN/stagedAttachmentStorageKey
// are the same shape of small, file-local helper).
func rfcMessageIDFor(id uuid.UUID) string {
	return fmt.Sprintf("<%s@vantigo.invalid>", hexN(id))
}

// replySource is what a reply answers (M15; inventory §4.3, §4.4): the
// latest inbound message with a sender — its sender's address, its Message-Id
// and References for the reply's threading headers — and reply-all's Cc, the
// participants that message's Cc named.
type replySource struct {
	senderAddress string
	rfcMessageID  *string
	references    []string
	cc            []store.CommunicationsParticipant
}

// resolveReplyRecipients is QueueOutboundAsync's inline recipient
// derivation (`:277`, `:283`) and ReplyRecipients (`:446-455`): the latest
// inbound message with a participant, or nil when the conversation has none.
// Reply and the detail both read it.
func (s *server) resolveReplyRecipients(ctx context.Context, q *store.Queries, conversationID uuid.UUID) (*replySource, error) {
	row, err := q.GetReplyRecipientsSource(ctx, conversationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("communications: get latest inbound participant: %w", err)
	}
	var metadata inboundMetadata
	if len(row.ChannelMetadataJson) > 0 {
		if err := json.Unmarshal(row.ChannelMetadataJson, &metadata); err != nil {
			return nil, fmt.Errorf("communications: decode the latest inbound message's metadata: %w", err)
		}
	}
	source := &replySource{senderAddress: row.SenderAddress, rfcMessageID: row.RfcMessageID, references: metadata.References}

	cc := replyAllCcOf(metadata.Cc, row.ChannelAddress, row.SenderAddress)
	if len(cc) == 0 {
		return source, nil
	}
	rows, err := q.ListParticipantsByChannelAndAddressesFold(ctx, store.ListParticipantsByChannelAndAddressesFoldParams{
		ChannelID: row.ChannelID, Addresses: cc,
	})
	if err != nil {
		return nil, fmt.Errorf("communications: list reply-all's participants: %w", err)
	}
	byAddress := make(map[string]store.CommunicationsParticipant, len(rows))
	for _, p := range rows {
		key := strings.ToLower(p.Address)
		if _, ok := byAddress[key]; !ok {
			byAddress[key] = p
		}
	}
	for _, address := range cc {
		// One the import did not make a participant of is left out.
		if p, ok := byAddress[address]; ok {
			source.cc = append(source.cc, p)
		}
	}
	return source, nil
}

// maxReplyAllCc is how many Cc addresses reply-all answers (inventory §4.3's
// Take(100)).
const maxReplyAllCc = 100

// replyAllCcOf is reply-all's Cc, the inventory §4.3 rule ported unchanged:
// the latest inbound message's Cc, each a valid bare address, compared
// normalised (Trim().ToUpperInvariant()), without the channel's own address
// and the sender, distinct in any case, the first 100 — answered in lower
// case, as the import stores them.
func replyAllCcOf(cc []string, channelAddress, sender string) []string {
	skip := map[string]bool{normalizeEmail(channelAddress): true, normalizeEmail(sender): true}
	out := make([]string, 0, len(cc))
	for _, a := range cc {
		parsed, err := netmail.ParseAddress(strings.TrimSpace(a))
		if err != nil || parsed.Name != "" {
			continue
		}
		key := normalizeEmail(parsed.Address)
		if key == "" || skip[key] {
			continue
		}
		skip[key] = true
		out = append(out, strings.ToLower(parsed.Address))
		if len(out) == maxReplyAllCc {
			break
		}
	}
	return out
}

// maxThreadReferences is how many ids a reply's References carries.
const maxThreadReferences = 20

// threadReferences is a reply's References: the answered message's own,
// then its Message-Id, distinct in any case (.NET's Distinct(OrdinalIgnoreCase)),
// and — a plan decision (owner question 1), not .NET's Take(20), which keeps
// the oldest 20 and drops the very message answered once a thread is that
// long — the first id and the last 19, RFC 5322 §3.6.4's advice.
func threadReferences(references []string, rfcMessageID string) []string {
	all := make([]string, 0, len(references)+1)
	seen := map[string]bool{}
	for _, id := range append(slices.Clone(references), rfcMessageID) {
		key := strings.ToLower(strings.TrimSpace(id))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		all = append(all, id)
	}
	if len(all) <= maxThreadReferences {
		return all
	}
	return append([]string{all[0]}, all[len(all)-(maxThreadReferences-1):]...)
}

// replyMetadata is a reply's channel_metadata_json: the threading headers the
// outbox writes (outboxChannelMetadata), nil when the answered message has no
// Message-Id.
func replyMetadata(source *replySource) ([]byte, error) {
	if source == nil || source.rfcMessageID == nil {
		return nil, nil
	}
	b, err := json.Marshal(outboxChannelMetadata{
		InReplyTo:  *source.rfcMessageID,
		References: threadReferences(source.references, *source.rfcMessageID),
	})
	if err != nil {
		return nil, fmt.Errorf("communications: encode the reply's threading headers: %w", err)
	}
	return b, nil
}

// replyFingerprint is EmailPayloadFingerprint.Create's role for Reply
// (`:271`: `new { conversationId, request.TextBody, request.HtmlBody,
// request.Subject, request.ReplyMode, request.AttachmentIds }`) — the exact
// same "same conclusion, not same bytes" latitude fingerprintOf's own
// comment (conversations_create.go) already claims for CreateConversation.
//
// The components are part of it (Messenger design N4), with each card's image:
// an idempotent replay with different components is the existing mismatch
// refusal. omitempty keeps the fingerprint of a reply with none what it was.
func replyFingerprint(conversationID uuid.UUID, body gen.ReplyRequest) (string, error) {
	return fingerprintOf(struct {
		ConversationId uuid.UUID              `json:"conversationId"`
		TextBody       *string                `json:"textBody"`
		HtmlBody       *string                `json:"htmlBody"`
		Subject        *string                `json:"subject"`
		ReplyMode      *string                `json:"replyMode"`
		AttachmentIds  *[]uuid.UUID           `json:"attachmentIds"`
		Components     *gen.MessageComponents `json:"components,omitempty"`
	}{
		ConversationId: conversationID, TextBody: body.TextBody, HtmlBody: body.HtmlBody,
		Subject: body.Subject, ReplyMode: body.ReplyMode, AttachmentIds: body.AttachmentIds,
		Components: body.Components,
	})
}

// PostCommunicationsConversationsByIdReply Reply to a conversation
// (POST /api/v1/communications/conversations/{id}/reply)
//
// Reply -> QueueOutboundAsync, steps 1-7 (this file's own top-of-file
// comment has the full ten-step order; steps 8-10 live in queueReply).
func (s *server) PostCommunicationsConversationsByIdReply(ctx context.Context, req gen.PostCommunicationsConversationsByIdReplyRequestObject) (gen.PostCommunicationsConversationsByIdReplyResponseObject, error) {
	caller, err := callerUserID(ctx)
	if err != nil {
		return nil, err
	}
	body := gen.ReplyRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	// Step 2: validateReply -> 400. Body validation wins over existence
	// (inventory §2's closing note): this runs before the conversation is
	// ever looked up, so an invalid body against an unknown conversation id
	// answers 400, not 404 (conversations_reply_test.go pins this).
	//
	// replyModeExplicitlyNull recovers inventory §4.1's explicit-null
	// asymmetry: encoding/json collapses "replyMode omitted" (valid,
	// defaults to "reply") and "replyMode: null" (invalid — it defeats the
	// DTO's own default and hits .NET's null branch, 400) into the same nil
	// *string. module.go's withRawJSONBody/rawJSONBodyFrom (task 5's
	// PATCH-body machinery, extended to this route in fix round 2) captures
	// the raw body so this handler can tell them apart the same way
	// conversations_patch.go already does for customerId/assignedUserId.
	replyModeExplicitlyNull := false
	if raw, ok := rawJSONBodyFrom(ctx); ok && len(raw) > 0 {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err == nil {
			if v, present := fields["replyMode"]; present && string(bytes.TrimSpace(v)) == "null" {
				replyModeExplicitlyNull = true
			}
		}
	}
	errs := validateReply(body, replyModeExplicitlyNull)
	// A Messenger reply may be attachments alone (N7: "the text, if any"): the
	// body's refusal alone, with attachments named, waits for the channel's type.
	var deferredBody map[string][]string
	if _, only := errs["body"]; only && len(errs) == 1 && body.AttachmentIds != nil && len(*body.AttachmentIds) > 0 {
		deferredBody, errs = errs, map[string][]string{}
	}
	if len(errs) > 0 {
		return gen.PostCommunicationsConversationsByIdReply400JSONResponse(validationErrorBody(errs)), nil
	}

	// Step 3: Idempotency-Key validity -> 400. Also precedes the lookup, so
	// an invalid key against an unknown conversation id is 400, not 404
	// (conversations_reply_test.go pins this too).
	key := req.Params.IdempotencyKey
	if !validIdempotencyKey(key) {
		return gen.PostCommunicationsConversationsByIdReply400JSONResponse(flatErrorBody(
			"idempotency_key_required", "A valid Idempotency-Key header is required.")), nil
	}

	q := store.New(s.deps.Pool)

	// A reply made through an agent grant (virtual employees design V11, V15,
	// V22): never reply-all, and marked as the agent's. agentID is set from the
	// principal alone.
	var agentID *uuid.UUID
	if p, ok := contracts.PrincipalFrom(ctx); ok && p.Via != nil && p.Via.Kind == contracts.ViaAgent {
		id := p.Via.AgentID
		agentID = &id
		if normalizedReplyMode(body.ReplyMode) == "reply_all" {
			return gen.PostCommunicationsConversationsByIdReply422JSONResponse(flatErrorBody(
				"reply_all_not_allowed", "An agent never answers everyone the message was sent to.")), nil
		}
		if body.InReplyToMessageId == nil {
			return gen.PostCommunicationsConversationsByIdReply400JSONResponse(validationErrorBody(map[string][]string{
				"inReplyToMessageId": {"An agent's reply names the inbound message it answers."},
			})), nil
		}
	}

	// Step 4: conversation (+ channel) lookup -> 404 bare.
	conv, err := q.GetConversationForReply(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostCommunicationsConversationsByIdReply404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("communications: get conversation for reply: %w", err)
	}
	// The sender moved on while the agent worked: a newer inbound message is
	// there, perhaps from someone else, and the reply would go to the wrong person.
	if body.InReplyToMessageId != nil {
		latest, err := q.GetLatestInboundMessageID(ctx, req.Id)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("communications: get the latest inbound message: %w", err)
		}
		if err != nil || latest != *body.InReplyToMessageId {
			return gen.PostCommunicationsConversationsByIdReply409JSONResponse(flatErrorBody(
				"conversation_moved", "A newer message has arrived in the conversation.")), nil
		}
	}
	// Someone took the conversation while the agent was working (design §10):
	// the agent's reply is refused, and the agent withdraws it.
	if agentID != nil && conv.AssignedUserID != nil && *conv.AssignedUserID != caller {
		return gen.PostCommunicationsConversationsByIdReply409JSONResponse(flatErrorBody(
			"conversation_taken", "The conversation is assigned to someone else.")), nil
	}

	// Step 5: channel inactive -> 422, *ahead of* the idempotency replay
	// below (task 7 dispatch's explicit hazard: a replay of an
	// already-accepted request against a since-deactivated channel answers
	// 422, not the cached 200 — see conversations_reply_test.go's
	// TestReply_ChannelInactivePrecedesIdempotencyReplay).
	if !conv.ChannelIsActive {
		return gen.PostCommunicationsConversationsByIdReply422JSONResponse(flatErrorBody(
			"channel_inactive", "The conversation channel is inactive.")), nil
	}

	// A reply on a Messenger channel is refused, before the replay, what that
	// channel cannot take (N4, N7); a reply on an e-mail channel what only
	// Messenger takes.
	messenger := conv.ChannelType == channelTypeMessenger
	if deferredBody != nil && !messenger {
		return gen.PostCommunicationsConversationsByIdReply400JSONResponse(validationErrorBody(deferredBody)), nil
	}
	var state messengerConversationState
	if messenger {
		if state, err = s.messengerStateOf(ctx, q, req.Id, conv.ChannelProvider, s.deps.Clock()); err != nil {
			return nil, err
		}
		if refusal := messengerReplyRefusal(body, state.caps, s.publicBaseURL()); refusal != nil {
			return refusal, nil
		}
	} else if hasComponents(body.Components) {
		return gen.PostCommunicationsConversationsByIdReply422JSONResponse(flatErrorBody(
			"components_unsupported", "This channel takes no answer options or cards.")), nil
	}

	fingerprint, err := replyFingerprint(req.Id, body)
	if err != nil {
		return nil, err
	}

	// Step 6: idempotency replay -> 200 same-fingerprint / 409
	// idempotency_key_reused. Unlike attachment staging's replay (which has
	// no fingerprint and always answers 200, task 6 fix round 1), Reply
	// forks on the fingerprint, exactly like CreateConversation.
	if existing, err := q.GetIdempotencyRecordByKey(ctx, key); err == nil {
		if existing.PayloadFingerprint == fingerprint {
			return gen.PostCommunicationsConversationsByIdReply200JSONResponse(gen.ConversationMutationResponse{
				ConversationId: req.Id, MessageId: &existing.MessageID, Status: "queued", IdempotencyKey: &key,
			}), nil
		}
		return gen.PostCommunicationsConversationsByIdReply409JSONResponse(flatErrorBody(
			"idempotency_key_reused", "The Idempotency-Key was already used with a different payload.")), nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("communications: get idempotency record: %w", err)
	}

	// A Messenger conversation's window: the person must have written within it
	// (N6).
	// An agent's message is never sent tagged as a human agent's (V22): the
	// window must be open.
	if messenger && (state.window.state == windowClosed || (agentID != nil && state.window.state != windowOpen)) {
		return gen.PostCommunicationsConversationsByIdReply422JSONResponse(flatErrorBody(
			"reply_window_closed", "The customer must write again before you can answer here.")), nil
	}

	// Step 7: no inbound participant -> 422 recipients_missing: a
	// conversation with no message received — one this module's own API
	// created — has nobody to answer. On Messenger the person answered is the
	// conversation's participant.
	var source *replySource
	if messenger {
		source, err = s.messengerReplySource(ctx, q, req.Id)
	} else {
		source, err = s.resolveReplyRecipients(ctx, q, req.Id)
	}
	if err != nil {
		return nil, err
	}
	if source == nil {
		return gen.PostCommunicationsConversationsByIdReply422JSONResponse(flatErrorBody(
			"recipients_missing", "The conversation has no inbound participant to reply to.")), nil
	}
	// Reply-all copies the answered message's Cc (resolveReplyRecipients
	// already left out the sender, the To here, and the channel's own
	// address); reply answers the sender alone.
	var ccAddresses []string
	if body.ReplyMode != nil && strings.ToLower(strings.TrimSpace(*body.ReplyMode)) == "reply_all" {
		for _, p := range source.cc {
			if !strings.EqualFold(p.Address, source.senderAddress) {
				ccAddresses = append(ccAddresses, p.Address)
			}
		}
	}
	metadata, err := replyMetadata(source)
	if err != nil {
		return nil, err
	}

	messageSubject := body.Subject
	if messageSubject == nil {
		messageSubject = conv.Subject
	}
	var components []byte
	var images *cardImages
	now := s.deps.Clock()
	if messenger {
		// No subject and no HTML: a chat is plain text (N8).
		messageSubject, body.HtmlBody = nil, nil
		stored := storedComponentsOf(body.Components)
		if len(stored.Cards) > 0 {
			// A card's image is a copy of a product's, made before the transaction
			// (N16): the attachment id a client sent is never the card's.
			var refusal gen.PostCommunicationsConversationsByIdReplyResponseObject
			if images, refusal, err = s.copyCardImages(ctx, req.Id, *body.Components.Cards, now); err != nil || refusal != nil {
				return refusal, err
			}
			for i := range stored.Cards {
				id := images.copies[i].id
				stored.Cards[i].AttachmentID = &id
			}
		}
		if !stored.empty() {
			if components, err = json.Marshal(stored); err != nil {
				s.forgetCardImages(ctx, imageCopies(images), now)
				return nil, fmt.Errorf("communications: encode the reply's components: %w", err)
			}
		}
		// The metadata names the product the cards showed (N16) and, for an
		// agent's reply, the agent (V11) - from the principal, never the body.
		if images != nil || agentID != nil {
			meta := messengerMetadata{}
			if images != nil {
				meta.ProductID = &images.productID
			}
			if agentID != nil {
				meta.Agent = agentID.String()
			}
			if metadata, err = json.Marshal(meta); err != nil {
				s.forgetCardImages(ctx, imageCopies(images), now)
				return nil, fmt.Errorf("communications: encode the reply's metadata: %w", err)
			}
		}
	}
	resp, err := s.queueReply(ctx, replyQueueParams{
		conversationID: req.Id, channelType: conv.ChannelType, channelProvider: conv.ChannelProvider, caller: caller, key: key,
		fingerprint: fingerprint, body: body, messageSubject: messageSubject,
		primaryAddress: source.senderAddress, ccAddresses: ccAddresses, metadata: metadata, components: components,
		cards: images, attachmentMax: state.caps.MaxAttachmentBytes, now: now, agentID: agentID,
	})
	if _, queued := resp.(gen.PostCommunicationsConversationsByIdReply201JSONResponse); !queued && images != nil {
		// Nothing of this reply was committed: the copies are no one's.
		s.forgetCardImages(ctx, images.copies, now)
	}
	return resp, err
}

// imageCopies is the copies of images, none for nil.
func imageCopies(images *cardImages) []cardCopy {
	if images == nil {
		return nil
	}
	return images.copies
}

// messengerMaxText is a Messenger message's text limit when its provider names
// none (Messenger design N7).
const messengerMaxText = 2000

// hasComponents reports whether a reply carries components at all: answer
// options, cards, or the request for an e-mail address.
func hasComponents(c *gen.MessageComponents) bool {
	return c != nil && (c.AnswerOptions != nil || c.Cards != nil || (c.ShareEmail != nil && *c.ShareEmail))
}

// messengerReplyRefusal is what a Messenger channel refuses of a reply before
// anything is read of the conversation's recipients (N4, N7), or nil: the
// e-mail-only inputs (422 not_supported_on_channel), text that is missing (400)
// or too long (422 text_too_long), and components the provider takes none of
// (422 components_unsupported) or breaking its limits (422 components_invalid,
// with fields). Cards must each name a product image (cardsRefusal), and the
// provider's fetching them from the public host needs the instance to have one
// (422 public_url_missing).
func messengerReplyRefusal(body gen.ReplyRequest, caps contracts.MessengerCapabilities, publicBaseURL string) gen.PostCommunicationsConversationsByIdReplyResponseObject {
	if (body.SendFromPersonal != nil && *body.SendFromPersonal) || normalizedReplyMode(body.ReplyMode) == "reply_all" {
		return gen.PostCommunicationsConversationsByIdReply422JSONResponse(flatErrorBody(
			"not_supported_on_channel", "Sending from a personal address and replying to all are not available on this channel."))
	}
	text := ""
	if body.TextBody != nil {
		text = *body.TextBody
	}
	if strings.TrimSpace(text) == "" && (body.AttachmentIds == nil || len(*body.AttachmentIds) == 0) {
		return gen.PostCommunicationsConversationsByIdReply400JSONResponse(validationErrorBody(map[string][]string{
			"textBody": {"TextBody is required when there is no attachment."},
		}))
	}
	limit := caps.MaxText
	if limit <= 0 {
		limit = messengerMaxText
	}
	if utf8.RuneCountInString(text) > limit {
		return gen.PostCommunicationsConversationsByIdReply422JSONResponse(flatErrorBody(
			"text_too_long", fmt.Sprintf("The text is longer than %d characters.", limit)))
	}
	if !hasComponents(body.Components) {
		return nil
	}
	if !caps.Components() {
		return gen.PostCommunicationsConversationsByIdReply422JSONResponse(flatErrorBody(
			"components_unsupported", "This channel takes no answer options or cards."))
	}
	errs := validateComponents(body.Components, caps)
	if len(errs) > 0 {
		return gen.PostCommunicationsConversationsByIdReply422JSONResponse(fieldsErrorBody(
			"components_invalid", "The answer options or cards break the channel's limits.", errs))
	}
	return cardsRefusal(body.Components, caps, publicBaseURL)
}

// publicBaseURL is the instance's public host's address, "" when it has none.
func (s *server) publicBaseURL() string {
	if s.deps.Config == nil {
		return ""
	}
	return s.deps.Config.PublicBaseURL
}

// messengerReplySource is who a reply on a Messenger conversation answers: its
// participant, nil when it has none.
func (s *server) messengerReplySource(ctx context.Context, q *store.Queries, conversationID uuid.UUID) (*replySource, error) {
	p, err := q.GetMessengerReplyParticipant(ctx, conversationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("communications: get the conversation's participant: %w", err)
	}
	return &replySource{senderAddress: p.Address}, nil
}

// replyQueueParams is everything queueReply needs once a primary recipient
// has already been resolved (step 7 has passed): ccAddresses is reply-all's
// Cc, and metadata the threading headers the outbox writes.
type replyQueueParams struct {
	conversationID  uuid.UUID
	channelType     string
	channelProvider string
	caller          uuid.UUID
	key             string
	fingerprint     string
	body            gen.ReplyRequest
	messageSubject  *string
	primaryAddress  string
	ccAddresses     []string
	metadata        []byte
	// components is a Messenger reply's conversation_messages.components, nil for
	// none.
	components []byte
	// cards is the product images a Messenger reply copied for its cards, nil
	// for none: the transaction creates their attachments (N16).
	cards *cardImages
	// attachmentMax is a Messenger provider's largest file in bytes, 0 for none.
	attachmentMax int64
	now           time.Time
	// agentID is the agent the reply is sent as, from the principal of an agent
	// grant; nil for a person's.
	agentID *uuid.UUID
}

// queueReply is QueueOutboundAsync's steps 8-10 (`:291-336`): the
// staged-attachment preflight, the suppression check, and — inside one
// transaction — the conditional clean -> claimed claim (the same
// attachments_not_ready gate as the preflight, re-raised if the claim
// affects fewer rows than promised), the message/delivery/attachment/event
// writes, the outbox job, the idempotency record, and the conversation's
// own activity fields.
func (s *server) queueReply(ctx context.Context, p replyQueueParams) (gen.PostCommunicationsConversationsByIdReplyResponseObject, error) {
	q := store.New(s.deps.Pool)

	var staged []uuid.UUID
	if p.body.AttachmentIds != nil {
		staged = *p.body.AttachmentIds
	}

	// Step 8: staged-attachment preflight -> 409 attachments_not_ready.
	var stagedRows []store.ListStagedAttachmentsForClaimRow
	if len(staged) > 0 {
		rows, err := q.ListStagedAttachmentsForClaim(ctx, store.ListStagedAttachmentsForClaimParams{
			Ids: staged, ConversationID: p.conversationID, UploadedByUserID: p.caller, Now: p.now,
		})
		if err != nil {
			return nil, fmt.Errorf("communications: list staged attachments: %w", err)
		}
		stagedRows = rows
	}
	if len(stagedRows) != len(staged) {
		return gen.PostCommunicationsConversationsByIdReply409JSONResponse(flatErrorBody(
			"attachments_not_ready", "One or more attachments are still being scanned or are unavailable.")), nil
	}

	// Step 8a: a Messenger channel's largest file (N7): a staged attachment above
	// the provider's limit is refused as a channel's budget is, before anything
	// is written.
	if p.attachmentMax > 0 {
		for _, row := range stagedRows {
			if row.SizeBytes > p.attachmentMax {
				return gen.PostCommunicationsConversationsByIdReply422JSONResponse(flatErrorBody(
					"too_large", "The message and its attachments are larger than this channel can send.")), nil
			}
		}
	}

	// Step 8b: a mailbox channel's budget (M13) -> 422 too_large,
	// before anything is written: the estimate of the message as MIME over
	// what the provider sends would only fail at the outbox, after the
	// caller was told it was queued.
	budget, err := s.mailBudget(ctx, p.channelType, p.channelProvider)
	if err != nil {
		return nil, err
	}
	if budget != nil {
		sizes := make([]int64, 0, len(stagedRows))
		for _, row := range stagedRows {
			sizes = append(sizes, row.SizeBytes)
		}
		if mailEstimate(sizes, p.body.TextBody, p.body.HtmlBody) > *budget {
			return gen.PostCommunicationsConversationsByIdReply422JSONResponse(flatErrorBody(
				"too_large", "The message and its attachments are larger than this channel can send.")), nil
		}
	}

	sendFromPersonal := p.body.SendFromPersonal != nil && *p.body.SendFromPersonal
	sendAs := "channel"
	var fromAddress *string
	var fromDisplayName *string
	// sendProvider is the PersonalMailbox the message is sent through (G3),
	// written with fromAddress.
	var sendProvider *string
	if sendFromPersonal {
		status, err := personalMailStatus(ctx, s.deps)
		if err != nil {
			return nil, err
		}
		if !status.Available {
			return gen.PostCommunicationsConversationsByIdReply409JSONResponse(flatErrorBody(
				"connection_needed", personalConnectionNeeded(status))), nil
		}
		sizes := make([]int64, 0, len(stagedRows))
		for _, row := range stagedRows {
			sizes = append(sizes, row.SizeBytes)
		}
		if mailEstimate(sizes, p.body.TextBody, p.body.HtmlBody) > contracts.MaxPersonalMessageBytes {
			return gen.PostCommunicationsConversationsByIdReply422JSONResponse(flatErrorBody(
				"too_large", "The message and its attachments are larger than your personal mailbox can send.")), nil
		}
		sendAs = "personal"
		fromAddress = apicommon.Ptr(status.Address)
		sendProvider = apicommon.Ptr(status.Provider)
		if s.deps.Users != nil {
			u, err := s.deps.Users.User(ctx, p.caller)
			if err != nil {
				return nil, fmt.Errorf("communications: get user directory entry: %w", err)
			}
			if u != nil && u.DisplayName != "" {
				fromDisplayName = apicommon.Ptr(u.DisplayName)
			}
		}
	}

	// Step 9: suppression check -> 422 recipient_suppressed +
	// fields.recipients. destinations is recipients+ccAddresses,
	// normalised (uppercased, D7) and deduplicated exactly like
	// actualDestinations (`:297-300`); the message itself, and the delivery
	// rows below, still carry the caller-facing (unnormalised) address —
	// only the suppression comparison and its echoed fields.recipients use
	// the normalised form (matching what suppressions.normalized_email_address
	// actually stores, inventory §19.2 item 3).
	destinations := normalizedDedup(append([]string{p.primaryAddress}, p.ccAddresses...))
	var suppressed []string
	if p.channelType == "email" && len(destinations) > 0 {
		rows, err := q.ListSuppressedAddresses(ctx, destinations)
		if err != nil {
			return nil, fmt.Errorf("communications: list suppressed addresses: %w", err)
		}
		suppressed = rows
	}
	if len(suppressed) > 0 {
		sort.Strings(suppressed)
		return gen.PostCommunicationsConversationsByIdReply422JSONResponse(fieldsErrorBody(
			"recipient_suppressed", "One or more recipients are suppressed.",
			map[string][]string{"recipients": suppressed})), nil
	}

	messageID := uuid.New()
	rfcMessageID := rfcMessageIDFor(messageID)
	previewSource := textOrHTML(p.body.TextBody, p.body.HtmlBody)

	var resp gen.PostCommunicationsConversationsByIdReply201JSONResponse
	txErr := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)

		// Step 10: the clean -> claimed conditional claim, the race fence
		// against a concurrent expiry sweep or a second reply — see
		// errAttachmentsNotReadyRace's own comment.
		if len(staged) > 0 {
			claimed, err := txq.ClaimStagedAttachments(ctx, store.ClaimStagedAttachmentsParams{
				Ids: staged, ConversationID: p.conversationID, UploadedByUserID: p.caller, Now: p.now,
			})
			if err != nil {
				return fmt.Errorf("communications: claim staged attachments: %w", err)
			}
			if claimed != int64(len(staged)) {
				return errAttachmentsNotReadyRace
			}
		}

		if _, err := txq.InsertConversationMessage(ctx, store.InsertConversationMessageParams{
			ID: messageID, ConversationID: p.conversationID, Direction: "outbound", AuthorUserID: &p.caller,
			Subject: p.messageSubject, TextBody: p.body.TextBody, HtmlBody: p.body.HtmlBody,
			ChannelMetadataJson: p.metadata, OccurredAt: p.now, CreatedAt: p.now, RfcMessageID: &rfcMessageID,
			SendAs: sendAs, FromAddress: fromAddress, FromDisplayName: fromDisplayName, SendProvider: sendProvider,
			Components: p.components, AgentID: p.agentID,
		}); err != nil {
			return err
		}

		// AddDeliveries (`:392`): reply's own deliveries never resolve or
		// link a participant (RecipientParticipantId stays null, unlike
		// CreateConversation's simple to/cc path) — the recipient address
		// alone is stored, exactly as .NET writes it.
		deliveryIDs := make([]uuid.UUID, 0, 1+len(p.ccAddresses))
		insertDelivery := func(address, kind string) error {
			id, err := txq.InsertMessageDelivery(ctx, store.InsertMessageDeliveryParams{
				ID: uuid.New(), MessageID: messageID, RecipientAddress: address, RecipientType: kind, CreatedAt: p.now,
			})
			if err != nil {
				return err
			}
			deliveryIDs = append(deliveryIDs, id)
			return nil
		}
		if err := insertDelivery(p.primaryAddress, "to"); err != nil {
			return err
		}
		for _, cc := range p.ccAddresses {
			if err := insertDelivery(cc, "cc"); err != nil {
				return err
			}
		}

		// The promotion (`:324-331`): scan_status and content_hash both
		// carry over verbatim from the staged row rather than being
		// recomputed — the coordinator's addendum on this task names
		// content_hash specifically (Task 6's reviewer flagged it "written
		// but unread within Task 6" because this is where it is read); the
		// object itself is never moved or re-keyed (storage_key carries
		// over too, inventory §5.5's closing note).
		for _, row := range stagedRows {
			if err := txq.InsertMessageAttachment(ctx, store.InsertMessageAttachmentParams{
				ID: uuid.New(), MessageID: messageID, FileName: row.FileName, ContentType: row.ContentType,
				SizeBytes: row.SizeBytes, ContentHash: row.ContentHash, ContentID: row.ContentID,
				StorageKey: row.StorageKey, IsInline: row.IsInline, CreatedAt: p.now,
			}); err != nil {
				return err
			}
		}
		if p.cards != nil {
			for _, c := range p.cards.copies {
				if err := txq.InsertCardAttachment(ctx, store.InsertCardAttachmentParams{
					ID: c.id, MessageID: messageID, FileName: c.fileName, ContentType: c.contentType, SizeBytes: c.size,
					ContentHash: c.hash, StorageKey: c.key, CreatedAt: p.now,
				}); err != nil {
					return err
				}
				if err := txq.MarkCleanupRecordOwned(ctx, c.key); err != nil {
					return err
				}
			}
		}
		if len(stagedRows) > 0 {
			for _, row := range stagedRows {
				if err := txq.MarkCleanupRecordOwned(ctx, row.StorageKey); err != nil {
					return err
				}
			}
			if err := txq.DeleteClaimedAttachmentUploads(ctx, staged); err != nil {
				return err
			}
		}

		// AddQueuedEvents (`:326`, `:438`): one "queued" event per delivery,
		// plus one message-level "message_queued" event with no delivery.
		for _, id := range deliveryIDs {
			deliveryID := id
			if err := txq.InsertMessageEvent(ctx, store.InsertMessageEventParams{
				ID: uuid.New(), MessageID: messageID, DeliveryID: &deliveryID, EventType: "queued", OccurredAt: p.now,
			}); err != nil {
				return err
			}
		}
		if err := txq.InsertMessageEvent(ctx, store.InsertMessageEventParams{
			ID: uuid.New(), MessageID: messageID, DeliveryID: nil, EventType: "message_queued", OccurredAt: p.now,
		}); err != nil {
			return err
		}

		if err := txq.InsertOutboxJob(ctx, store.InsertOutboxJobParams{
			ID: uuid.New(), MessageID: messageID, NextAttemptAt: p.now, CreatedAt: p.now,
		}); err != nil {
			return err
		}

		if err := txq.InsertIdempotencyRecord(ctx, store.InsertIdempotencyRecordParams{
			ID: uuid.New(), Key: p.key, PayloadFingerprint: p.fingerprint, ConversationID: p.conversationID,
			MessageID: messageID, CreatedAt: p.now,
		}); err != nil {
			return err
		}

		// BuildOutboundMessage's tracked-entity mutation (`:389`), applied
		// here — inside the transaction, after every refusal check has
		// passed — rather than early, per inventory §19.2 item 16.
		if err := txq.UpdateConversationActivityForReply(ctx, store.UpdateConversationActivityForReplyParams{
			ID: p.conversationID, MessageSubject: p.messageSubject, LastActivityAt: p.now, PreviewText: preview(previewSource),
		}); err != nil {
			return err
		}
		if err := notifyMessage(ctx, tx, p.conversationID, eventMessageAdded, messageID); err != nil {
			return err
		}
		// Published under the conversation's lock, which the activity update
		// above holds (virtual employees design V13).
		if err := events.Publish(ctx, tx, moduleName, contracts.ConversationReplied{
			ConversationID: p.conversationID, MessageID: messageID, ByUserID: p.caller, ByAgent: p.agentID != nil,
		}, p.now); err != nil {
			return err
		}

		resp = gen.PostCommunicationsConversationsByIdReply201JSONResponse(gen.ConversationMutationResponse{
			ConversationId: p.conversationID, MessageId: &messageID, Status: "queued", IdempotencyKey: &p.key,
		})
		return nil
	})
	if errors.Is(txErr, errAttachmentsNotReadyRace) {
		return gen.PostCommunicationsConversationsByIdReply409JSONResponse(flatErrorBody(
			"attachments_not_ready", "One or more attachments are still being scanned or are unavailable.")), nil
	}
	if txErr != nil {
		// Fix round 2's item 2: the same ux_idempotency_records_key race
		// conversations_create.go's own comment on this fix explains in
		// full — two concurrent replies carrying the identical
		// Idempotency-Key can both pass the step-6 replay check above
		// (neither's INSERT has committed yet) and both run this entire
		// transaction; only one INSERT into idempotency_records can win the
		// unique index, and the loser's failing INSERT aborts its whole
		// transaction, so none of that reply's other writes persist either.
		// Catch that specific violation and answer exactly what step 6
		// would answer had it run after the winner committed: 200 with the
		// winner's own conversationId/messageId on a matching fingerprint,
		// 409 idempotency_key_reused otherwise.
		// TestReply_ConcurrentIdenticalReplyAnswersTheSameReplayTwice pins
		// it.
		if db.IsUniqueViolation(txErr, "ux_idempotency_records_key") {
			existing, err2 := q.GetIdempotencyRecordByKey(ctx, p.key)
			if err2 != nil {
				return nil, fmt.Errorf("communications: re-read idempotency record after reply race: %w", err2)
			}
			if existing.PayloadFingerprint == p.fingerprint {
				return gen.PostCommunicationsConversationsByIdReply200JSONResponse(gen.ConversationMutationResponse{
					ConversationId: existing.ConversationID, MessageId: &existing.MessageID, Status: "queued", IdempotencyKey: &p.key,
				}), nil
			}
			return gen.PostCommunicationsConversationsByIdReply409JSONResponse(flatErrorBody(
				"idempotency_key_reused", "The Idempotency-Key was already used with a different payload.")), nil
		}
		return nil, fmt.Errorf("communications: queue reply: %w", txErr)
	}
	return resp, nil
}

// textOrHTML is BuildOutboundMessage's `text ?? html` (`:389`): text if it
// is present at all — even an empty string, C#'s ?? only tests for null —
// else html, else "". preview() below already treats a blank result the
// same way .NET's own blank-check does, so the empty-string case (both
// present-but-blank, or genuinely neither present) converges to the same
// nil PreviewText either way.
func textOrHTML(text, html *string) string {
	if text != nil {
		return *text
	}
	if html != nil {
		return *html
	}
	return ""
}

// normalizedDedup normalises (Trim().ToUpperInvariant(), D7) every address,
// drops blanks, and deduplicates while preserving first-seen order —
// actualDestinations' role (`:297-300`: `.Select(Normalize).Concat(...).Distinct(Ordinal)`).
func normalizedDedup(addresses []string) []string {
	seen := make(map[string]bool, len(addresses))
	out := make([]string, 0, len(addresses))
	for _, a := range addresses {
		n := normalizeEmail(a)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}
