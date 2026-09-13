package communications

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is CreateConversation (EP/ConversationEndpoints.cs:238-262,
// AddGenericDeliveriesAsync `:394-436`, communications inventory §1.2, §2's
// CreateConversation bullet, §4's composer notes): postCommunicationsConversations,
// the one operation of task 5's ten whose permission pairing is asymmetric
// with every reply-shaped endpoint in the module — conversations-reply
// ALONE, no +view (task 5 dispatch point 6; communications.yaml's
// x-vantigo-access on this one operation carries no `+communications:conversations-view`).
// module.Router enforces it; TestCreateConversation_ReplyAloneSuffices and
// its companion in conversations_test.go pin it with a request that would
// fail if +view were ever added.

// validIdempotencyKey is the identical rule CreateConversation, Reply and
// StageAttachment each inline in .NET (`EP/ConversationEndpoints.cs:110`,
// `:243`, `:267`, inventory §5.1): non-blank, at most 200 characters,
// already trimmed, no control characters.
func validIdempotencyKey(key string) bool {
	if strings.TrimSpace(key) == "" || len(key) > 200 || key != strings.TrimSpace(key) {
		return false
	}
	return !containsControl(key)
}

// fingerprintOf is EmailPayloadFingerprint.Create's role, not its exact
// bytes: .NET hashes a camelCase JSON serialization for cross-process
// stability with itself; this Go port only ever compares a fingerprint it
// computed against one it computed earlier for the same operation, so a
// deterministic Go-internal encoding (json.Marshal's own fixed field order)
// serves the identical purpose — same payload -> same hash, different
// payload -> a different one — without needing byte-for-byte parity with
// .NET's serializer.
func fingerprintOf(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("communications: encode fingerprint payload: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// deliveryPlan is one delivery this handler is about to write: the
// participant it resolved to, the address to store on the delivery row, and
// "to"/"cc"/"bcc".
type deliveryPlan struct {
	participantID    uuid.UUID
	recipientAddress string
	recipientType    string
}

// findOrCreateParticipantByAddress is the find-or-create half both
// AddGenericDeliveriesAsync branches share: look up (channelId, address),
// insert if absent. contactID is only ever applied on the insert path — an
// already-existing participant's ContactId is never overwritten, matching
// .NET exactly (neither branch ever assigns to an existing Participant's
// ContactId).
func findOrCreateParticipantByAddress(ctx context.Context, q *store.Queries, channelID uuid.UUID, address string, contactID *int32, now time.Time) (store.CommunicationsParticipant, error) {
	existing, err := q.FindParticipantByChannelAndAddress(ctx, store.FindParticipantByChannelAndAddressParams{ChannelID: channelID, Address: address})
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return store.CommunicationsParticipant{}, err
	}
	return q.InsertParticipant(ctx, store.InsertParticipantParams{
		ID: uuid.New(), ChannelID: channelID, Address: address, DisplayName: nil, ContactID: contactID, CreatedAt: now,
	})
}

// resolveSimpleDeliveries is AddGenericDeliveriesAsync's `recipients.Count
// == 0 && channel.Type == "email"` branch (`:398-410`): every to/cc email
// becomes a delivery, the participant found-or-created by its *normalised*
// address, but the delivery row itself keeps the caller's *raw* address
// (EmailSuppression.Normalize is applied only to the participant lookup key,
// never to what MessageDelivery.RecipientAddress stores).
func resolveSimpleDeliveries(ctx context.Context, q *store.Queries, channelID uuid.UUID, to, cc []gen.EmailRecipientRequest, now time.Time) ([]deliveryPlan, error) {
	var plans []deliveryPlan
	add := func(recipients []gen.EmailRecipientRequest, kind string) error {
		for _, r := range recipients {
			if r.Email == nil {
				continue
			}
			participant, err := findOrCreateParticipantByAddress(ctx, q, channelID, normalizeEmail(*r.Email), nil, now)
			if err != nil {
				return err
			}
			plans = append(plans, deliveryPlan{participantID: participant.ID, recipientAddress: *r.Email, recipientType: kind})
		}
		return nil
	}
	if err := add(to, "to"); err != nil {
		return nil, err
	}
	if err := add(cc, "cc"); err != nil {
		return nil, err
	}
	return plans, nil
}

// errContactNotFound is AddGenericDeliveriesAsync's
// `throw new InvalidOperationException("The selected contact does not exist.")`
// (`:430-431`): an unhandled exception in .NET, becoming a sanitised 500 —
// ported as an error this handler returns unwrapped-to-500 rather than a
// 4xx, the same faithful-not-hardened treatment task 5 dispatch point 7
// asks for on PATCH's assignedUserId.
var errContactNotFound = errors.New("communications: the selected contact does not exist")

// resolveGenericDeliveries is AddGenericDeliveriesAsync's other branch
// (`:412-435`): a delivery per recipients[] entry, in order, resolved by
// participantId or by address, skipped entirely when neither resolves to a
// participant, and validated against contracts.CustomerDirectory when a
// contactId is given.
func (s *server) resolveGenericDeliveries(ctx context.Context, q *store.Queries, channelID uuid.UUID, channelType string, recipients []gen.ChannelRecipientRequest, now time.Time) ([]deliveryPlan, error) {
	var plans []deliveryPlan
	for _, r := range recipients {
		var participant *store.CommunicationsParticipant
		if r.ParticipantId != nil {
			p, err := q.FindParticipantByIDAndChannel(ctx, store.FindParticipantByIDAndChannelParams{ID: *r.ParticipantId, ChannelID: channelID})
			switch {
			case err == nil:
				participant = &p
			case errors.Is(err, pgx.ErrNoRows):
				// falls through to the address branch below, exactly as
				// .NET's `participant is null` does.
			default:
				return nil, err
			}
		}
		if participant == nil && r.Address != nil && strings.TrimSpace(*r.Address) != "" {
			normalized := strings.TrimSpace(*r.Address)
			if channelType == "email" {
				normalized = normalizeEmail(*r.Address)
			}
			p, err := findOrCreateParticipantByAddress(ctx, q, channelID, normalized, r.ContactId, now)
			if err != nil {
				return nil, err
			}
			participant = &p
		}
		if participant == nil {
			continue
		}

		if r.ContactId != nil {
			contact, derr := s.deps.Directory.Contact(ctx, *r.ContactId)
			if derr != nil {
				return nil, derr
			}
			if contact == nil {
				return nil, errContactNotFound
			}
		}

		recipientType := "to"
		if r.Type != nil {
			t := strings.ToLower(strings.TrimSpace(*r.Type))
			if t == "cc" || t == "bcc" {
				recipientType = t
			}
		}
		plans = append(plans, deliveryPlan{participantID: participant.ID, recipientAddress: participant.Address, recipientType: recipientType})
	}
	return plans, nil
}

// PostCommunicationsConversations Create a conversation
// (POST /api/v1/communications/conversations)
//
// CreateConversation (inventory §2): (1) ValidateConversation -> 400; (2)
// Idempotency-Key validity -> 400 idempotency_key_required (a header the
// contract marks `required: true` — an entirely *missing* header never
// reaches this handler at all, refused earlier by the generated decoder as
// a bare decode-error 400, the same platform-level split every other
// required-header operation in this codebase already has; a *present but
// blank or malformed* key reaches here and gets this module's own
// idempotency_key_required); (3) replay lookup by the global (not per-user)
// idempotency key -> 200 same-fingerprint / 409 idempotency_key_reused; (4)
// channel resolution -> 422 channel_invalid; (5) customer existence via
// contracts.CustomerDirectory -> 422 customer_invalid. No 404 path exists.
func (s *server) PostCommunicationsConversations(ctx context.Context, req gen.PostCommunicationsConversationsRequestObject) (gen.PostCommunicationsConversationsResponseObject, error) {
	caller, err := callerUserID(ctx)
	if err != nil {
		return nil, err
	}
	body := gen.CreateConversationRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	if errs := validateConversation(body); len(errs) > 0 {
		return gen.PostCommunicationsConversations400JSONResponse(validationErrorBody(errs)), nil
	}

	key := req.Params.IdempotencyKey
	if !validIdempotencyKey(key) {
		return gen.PostCommunicationsConversations400JSONResponse(flatErrorBody(
			"idempotency_key_required", "A valid Idempotency-Key header is required.")), nil
	}

	fingerprint, err := fingerprintOf(body)
	if err != nil {
		return nil, err
	}

	q := store.New(s.deps.Pool)
	if existing, err := q.GetIdempotencyRecordByKey(ctx, key); err == nil {
		if existing.PayloadFingerprint == fingerprint {
			return gen.PostCommunicationsConversations200JSONResponse(gen.ConversationMutationResponse{
				ConversationId: existing.ConversationID, MessageId: &existing.MessageID, Status: "queued", IdempotencyKey: &key,
			}), nil
		}
		return gen.PostCommunicationsConversations409JSONResponse(flatErrorBody(
			"idempotency_key_reused", "The Idempotency-Key was already used with a different payload.")), nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("communications: get idempotency record: %w", err)
	}

	var channelID uuid.UUID
	var channelType string
	if body.ChannelId != nil {
		row, err := q.GetActiveChannelByID(ctx, *body.ChannelId)
		if errors.Is(err, pgx.ErrNoRows) {
			return gen.PostCommunicationsConversations422JSONResponse(flatErrorBody(
				"channel_invalid", "The selected channel does not exist or is inactive.")), nil
		} else if err != nil {
			return nil, fmt.Errorf("communications: get channel: %w", err)
		}
		channelID, channelType = row.ID, row.Type
	} else {
		row, err := q.GetDefaultActiveEmailChannel(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return gen.PostCommunicationsConversations422JSONResponse(flatErrorBody(
				"channel_invalid", "The selected channel does not exist or is inactive.")), nil
		} else if err != nil {
			return nil, fmt.Errorf("communications: get default channel: %w", err)
		}
		channelID, channelType = row.ID, row.Type
	}

	if body.CustomerId != nil {
		entry, derr := s.deps.Directory.Customer(ctx, *body.CustomerId)
		if derr != nil {
			return nil, fmt.Errorf("communications: look up customer: %w", derr)
		}
		if entry == nil {
			return gen.PostCommunicationsConversations422JSONResponse(flatErrorBody(
				"customer_invalid", "The selected customer does not exist.")), nil
		}
	}

	now := s.deps.Clock()
	previewText := ""
	if body.TextBody != nil {
		previewText = *body.TextBody
	} else if body.HtmlBody != nil {
		previewText = *body.HtmlBody
	}

	var customerAssociationSource *string
	if body.CustomerId != nil {
		manual := "manual"
		customerAssociationSource = &manual
	}

	conversationID := uuid.New()
	messageID := uuid.New()
	rfcMessageID := fmt.Sprintf("<%s@vantigo.invalid>", strings.ReplaceAll(messageID.String(), "-", ""))

	txErr := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)

		if _, err := txq.InsertConversation(ctx, store.InsertConversationParams{
			ID: conversationID, ChannelID: channelID, Subject: body.Subject, CustomerID: body.CustomerId,
			CustomerAssociationSource: customerAssociationSource, LastActivityAt: now, PreviewText: preview(previewText), CreatedAt: now,
		}); err != nil {
			return err
		}

		if _, err := txq.InsertConversationMessage(ctx, store.InsertConversationMessageParams{
			ID: messageID, ConversationID: conversationID, Direction: "outbound", AuthorUserID: &caller,
			Subject: body.Subject, TextBody: body.TextBody, HtmlBody: body.HtmlBody, OccurredAt: now, CreatedAt: now,
			RfcMessageID: &rfcMessageID,
		}); err != nil {
			return err
		}

		var recipients []gen.ChannelRecipientRequest
		if body.Recipients != nil {
			recipients = *body.Recipients
		}
		var plans []deliveryPlan
		var perr error
		if len(recipients) == 0 && channelType == "email" {
			var to, cc []gen.EmailRecipientRequest
			if body.To != nil {
				to = *body.To
			}
			if body.Cc != nil {
				cc = *body.Cc
			}
			plans, perr = resolveSimpleDeliveries(ctx, txq, channelID, to, cc, now)
		} else {
			plans, perr = s.resolveGenericDeliveries(ctx, txq, channelID, channelType, recipients, now)
		}
		if perr != nil {
			return perr
		}

		deliveryIDs := make([]uuid.UUID, 0, len(plans))
		for _, p := range plans {
			if err := txq.InsertConversationParticipant(ctx, store.InsertConversationParticipantParams{
				ConversationID: conversationID, ParticipantID: p.participantID,
			}); err != nil {
				return err
			}
			deliveryID, err := txq.InsertMessageDelivery(ctx, store.InsertMessageDeliveryParams{
				ID: uuid.New(), MessageID: messageID, RecipientAddress: p.recipientAddress,
				RecipientType: p.recipientType, RecipientParticipantID: &p.participantID, CreatedAt: now,
			})
			if err != nil {
				return err
			}
			deliveryIDs = append(deliveryIDs, deliveryID)
		}

		// AddQueuedEvents (`:438`): one "queued" event per delivery, plus one
		// message-level "message_queued" event with no delivery.
		for _, id := range deliveryIDs {
			deliveryID := id
			if err := txq.InsertMessageEvent(ctx, store.InsertMessageEventParams{
				ID: uuid.New(), MessageID: messageID, DeliveryID: &deliveryID, EventType: "queued", OccurredAt: now,
			}); err != nil {
				return err
			}
		}
		if err := txq.InsertMessageEvent(ctx, store.InsertMessageEventParams{
			ID: uuid.New(), MessageID: messageID, DeliveryID: nil, EventType: "message_queued", OccurredAt: now,
		}); err != nil {
			return err
		}

		if err := txq.InsertOutboxJob(ctx, store.InsertOutboxJobParams{
			ID: uuid.New(), MessageID: messageID, NextAttemptAt: now, CreatedAt: now,
		}); err != nil {
			return err
		}

		return txq.InsertIdempotencyRecord(ctx, store.InsertIdempotencyRecordParams{
			ID: uuid.New(), Key: key, PayloadFingerprint: fingerprint, ConversationID: conversationID,
			MessageID: messageID, CreatedAt: now,
		})
	})
	if txErr != nil {
		return nil, fmt.Errorf("communications: create conversation: %w", txErr)
	}

	return gen.PostCommunicationsConversations201JSONResponse(gen.ConversationMutationResponse{
		ConversationId: conversationID, MessageId: &messageID, Status: "queued", IdempotencyKey: &key,
	}), nil
}
