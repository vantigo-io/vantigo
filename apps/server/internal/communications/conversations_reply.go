package communications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
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
// Steps 8-10 are unreached in production: step 7 fires unconditionally
// first, because no production path in this port ever writes a row with
// conversation_messages.direction = 'inbound' (design doc §1.1: the inbound
// worker was the only writer and it is out of scope). They are, however,
// reachable from a test fixture: conversation_messages.direction's CHECK
// matches .NET's full Direction domain (inbound included) as of fix round
// 2, so conversations_reply_test.go inserts a genuine inbound participant
// and message directly and drives every one of the ten steps — including
// 8, 9 and 10 — through this handler for real, no override needed.
//
// History: fix round 1 first tried to fix this with an overridable
// recipient-resolution seam on *server, because at the time no fixture
// could construct a qualifying row at all — the CHECK narrowed
// conversation_messages.direction to ('outbound', 'internal_note') on the
// (wrong) principle that the schema should encode only what this port's own
// writers produce. That narrowing is what fix round 2 reverted (design doc
// §1.1's correction); once a fixture could drive the real query to its
// success branch, the override seam was redundant and was deleted
// (server.go's own comment on the removed field has the fuller history).

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

// resolveReplyRecipients is QueueOutboundAsync's inline recipient
// derivation (`:277`, `:283`): the latest inbound message's participant
// address, or ok=false when none exists. No production path in this port
// ever writes a direction='inbound' row (design doc §1.1), so ok is false
// for every conversation created through the real API, unconditionally —
// per task 7's dispatch: "Pin that rather than working around it." A test
// fixture can insert one (GetLatestInboundParticipantAddress's own comment
// has the schema history), and conversations_reply_test.go's own fixtures
// drive this to its ok=true branch directly.
func (s *server) resolveReplyRecipients(ctx context.Context, q *store.Queries, conversationID uuid.UUID) (address string, ok bool, err error) {
	address, err = q.GetLatestInboundParticipantAddress(ctx, conversationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("communications: get latest inbound participant: %w", err)
	}
	return address, true, nil
}

// replyFingerprint is EmailPayloadFingerprint.Create's role for Reply
// (`:271`: `new { conversationId, request.TextBody, request.HtmlBody,
// request.Subject, request.ReplyMode, request.AttachmentIds }`) — the exact
// same "same conclusion, not same bytes" latitude fingerprintOf's own
// comment (conversations_create.go) already claims for CreateConversation.
func replyFingerprint(conversationID uuid.UUID, body gen.ReplyRequest) (string, error) {
	return fingerprintOf(struct {
		ConversationId uuid.UUID    `json:"conversationId"`
		TextBody       *string      `json:"textBody"`
		HtmlBody       *string      `json:"htmlBody"`
		Subject        *string      `json:"subject"`
		ReplyMode      *string      `json:"replyMode"`
		AttachmentIds  *[]uuid.UUID `json:"attachmentIds"`
	}{
		ConversationId: conversationID, TextBody: body.TextBody, HtmlBody: body.HtmlBody,
		Subject: body.Subject, ReplyMode: body.ReplyMode, AttachmentIds: body.AttachmentIds,
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
	if errs := validateReply(body, replyModeExplicitlyNull); len(errs) > 0 {
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

	// Step 4: conversation (+ channel) lookup -> 404 bare.
	conv, err := q.GetConversationForReply(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostCommunicationsConversationsByIdReply404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("communications: get conversation for reply: %w", err)
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

	// Step 7: no inbound participant -> 422 recipients_missing. This is the
	// outbound-only consequence design doc §1.1 and task 7's dispatch both
	// name: no production path in this port ever writes an inbound message,
	// so this branch is taken for every conversation created through the
	// real API, unconditionally. Called directly (fix round 1's override
	// seam was removed in fix round 2 — server.go's own comment has the
	// history): a test fixture now inserts a genuine inbound row and this
	// method finds it for real, exercising steps 8-10 below through this
	// same handler with every wiring value (caller, channelType,
	// messageSubject, fingerprint, now) genuinely computed by production
	// code.
	primaryAddress, ok, err := s.resolveReplyRecipients(ctx, q, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return gen.PostCommunicationsConversationsByIdReply422JSONResponse(flatErrorBody(
			"recipients_missing", "The conversation has no inbound participant to reply to.")), nil
	}

	// Unreached by any conversation this port's own API can produce (see
	// above), but kept as the real continuation — not a workaround — so a
	// future inbound producer needs no change here, and so queueReply's own
	// steps 8-10 have a genuine, structurally-correct caller rather than
	// existing only for tests to call directly.
	messageSubject := body.Subject
	if messageSubject == nil {
		messageSubject = conv.Subject
	}
	return s.queueReply(ctx, replyQueueParams{
		conversationID: req.Id, channelType: conv.ChannelType, caller: caller, key: key,
		fingerprint: fingerprint, body: body, messageSubject: messageSubject,
		primaryAddress: primaryAddress, now: s.deps.Clock(),
	})
}

// replyQueueParams is everything queueReply needs once a primary recipient
// has already been resolved (step 7 has passed). ccAddresses is always nil
// through the real handler above — replyAllCc is permanently empty in this
// port (design doc §1.1: deriving it needs inbound thread metadata this
// port never writes) — but stays a parameter so a test can exercise the
// dedup-against-primary and suppression-of-a-cc-address paths directly, the
// same seam primaryAddress itself exists for.
type replyQueueParams struct {
	conversationID uuid.UUID
	channelType    string
	caller         uuid.UUID
	key            string
	fingerprint    string
	body           gen.ReplyRequest
	messageSubject *string
	primaryAddress string
	ccAddresses    []string
	now            time.Time
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
			OccurredAt: p.now, CreatedAt: p.now, RfcMessageID: &rfcMessageID,
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
