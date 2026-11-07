package communications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	netmail "net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is one Messenger event's import (Messenger design N3, N3a, N5, N12):
// a message of the participant's, an echo of the Page's own, or a delivered or
// read watermark. A message is threaded by participant, its attachments and the
// participant's profile read before any transaction, and written in one
// transaction with the receipt that makes the import happen once and the
// cursor's advance, which the lease guards.

// messengerMetadata is a Messenger message's channel_metadata_json: the
// platform's id of the message, whether it is an echo of the Page's own sent
// from elsewhere (N12), and, for an inbound one, the answer it is (N5). The
// detail reads echo and answer back (queries/conversations.sql).
type messengerMetadata struct {
	PlatformMessageID string        `json:"platformMessageId,omitempty"`
	Echo              bool          `json:"echo,omitempty"`
	Answer            *storedAnswer `json:"answer,omitempty"`
}

// receiptKeyOf is the id an event's receipt is written under: the platform's
// own, else — an event the provider gave none — its cursor.
func receiptKeyOf(event contracts.MessengerEvent) string {
	if event.PlatformMessageID != "" {
		return event.PlatformMessageID
	}
	return fmt.Sprintf("cursor:%d", event.Cursor)
}

// eventTime is the instant an event happened by the platform's account, or now
// when it gave none.
func eventTime(event contracts.MessengerEvent, now time.Time) time.Time {
	if event.At.IsZero() {
		return now
	}
	return event.At.UTC()
}

// importEvent carries one event to its place: a message imported, a watermark
// applied, or an event skipped. An error ends the cycle without advancing past
// the event, so it is tried again, unless the cycle gives it up (giveUp); one at
// the provider or the store is marked errImportTransient.
func (w *MessengerSyncWorker) importEvent(ctx context.Context, c *messengerCycle, event contracts.MessengerEvent) error {
	switch event.Kind {
	case contracts.MessengerEventMessage:
		return w.importMessage(ctx, c, event)
	case contracts.MessengerEventDelivered, contracts.MessengerEventRead:
		return w.importWatermark(ctx, c, event)
	}
	w.logger().Info("messenger sync skipped an event of a kind it does not know",
		"worker", messengerSyncWorkerName, "channel", c.channelID, "kind", event.Kind)
	return w.advanceOnly(ctx, c, event.Cursor)
}

// advanceOnly moves the cursor past an event that writes nothing.
func (w *MessengerSyncWorker) advanceOnly(ctx context.Context, c *messengerCycle, cursor int64) error {
	return w.guarded(store.New(w.deps.Pool).AdvanceMessengerSyncCursor(ctx, store.AdvanceMessengerSyncCursorParams{
		Cursor: cursor, Now: w.now(), ChannelID: c.channelID, LeaseID: &c.leaseID,
	}))
}

// importMessage imports a message event: already seen, an echo of a part we sent
// (ignored), or written. See the file's header.
func (w *MessengerSyncWorker) importMessage(ctx context.Context, c *messengerCycle, event contracts.MessengerEvent) error {
	q := store.New(w.deps.Pool)
	key := receiptKeyOf(event)
	seen, err := q.InboundReceiptExists(ctx, store.InboundReceiptExistsParams{ChannelID: c.channelID, ProviderMessageID: key})
	if err != nil {
		return fmt.Errorf("communications: look for a receipt: %w", err)
	}
	if seen {
		return w.advanceOnly(ctx, c, event.Cursor)
	}
	now := w.now()

	// An echo of a part Vantigo sent is no message of the Page's from elsewhere
	// (N12); one that matches no part may still be of a part whose Send has not
	// yet recorded its id, so while one is in flight the cycle waits for it.
	if event.Echo {
		ours, err := q.MessengerPartExists(ctx, &event.PlatformMessageID)
		if err != nil {
			return fmt.Errorf("communications: look for the part an echo is of: %w", err)
		}
		if ours {
			return w.skipWithReceipt(ctx, c, event, key, now)
		}
		inFlight, err := q.MessengerPartInFlight(ctx, store.MessengerPartInFlightParams{
			ChannelID: c.channelID, Since: ptrTime(now.Add(-messengerEchoWait)),
		})
		if err != nil {
			return fmt.Errorf("communications: look for a part in flight: %w", err)
		}
		if inFlight {
			return errMessengerEchoPending
		}
	}

	// Objects before rows: each attachment is fetched through the provider and
	// stored before the transaction that names it; any failure queues what was
	// reserved.
	messageID := uuid.New()
	objects, attachments, err := w.fetchAttachments(ctx, c, event, messageID, now)
	if err != nil {
		return err
	}

	// The participant's profile, read before the transaction — it is a call out
	// of the module — when the participant is new or its name is old (N3).
	name, fetched := w.profileOf(ctx, q, c, event.Participant, now)

	// The e-mail address a person shared feeds the contact linker as a
	// suggestion (N3a), read before the transaction as well.
	var link senderLink
	if event.Answer != nil && event.Answer.ChoiceID == shareEmailChoiceID {
		if address, err := netmail.ParseAddress(strings.TrimSpace(event.Text)); err == nil && address.Name == "" {
			link = lookupSenderLink(ctx, w.deps, w.logger(), messengerSyncWorkerName, c.channelID, address.Address)
		}
	}

	occurredAt := eventTime(event, now)
	locked := context.WithValue(ctx, lockedTxKey{}, true)
	txErr := db.WithTx(locked, w.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		ctx := locked
		txq := store.New(tx)

		participantID, err := w.messengerParticipant(ctx, txq, c.channelID, event.Participant, name, fetched, now)
		if err != nil {
			return err
		}
		conversationID, isNew, customerID, err := w.messengerThread(ctx, txq, c.channelID, participantID, occurredAt, now, event.Text)
		if err != nil {
			return err
		}

		metadata := messengerMetadata{PlatformMessageID: event.PlatformMessageID, Echo: event.Echo}
		if !event.Echo && event.Answer != nil {
			metadata.Answer, err = w.placeAnswer(ctx, txq, c, conversationID, event)
			if err != nil {
				return err
			}
		}
		metadataJSON, err := json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("communications: encode the message's metadata: %w", err)
		}
		text := capText(event.Text)
		direction, messageParticipant := "inbound", &participantID
		if event.Echo {
			direction, messageParticipant = "outbound", nil
		}
		if err := txq.InsertMessengerMessage(ctx, store.InsertMessengerMessageParams{
			ID: messageID, ConversationID: conversationID, Direction: direction, ParticipantID: messageParticipant,
			TextBody: &text, ChannelMetadataJson: metadataJSON, OccurredAt: occurredAt, CreatedAt: now,
		}); err != nil {
			return fmt.Errorf("communications: insert the message: %w", err)
		}
		for _, a := range attachments {
			if err := txq.InsertInboundMessageAttachment(ctx, store.InsertInboundMessageAttachmentParams{
				ID: a.id, MessageID: messageID, FileName: a.fileName, ContentType: a.contentType,
				SizeBytes: int64(len(a.content)), ContentHash: attachmentFingerprint(a.content),
				StorageKey: a.key, IsInline: false, CreatedAt: now,
			}); err != nil {
				return fmt.Errorf("communications: insert an attachment: %w", err)
			}
		}
		for _, o := range objects {
			if err := txq.MarkCleanupRecordOwned(ctx, o.key); err != nil {
				return fmt.Errorf("communications: mark an object owned: %w", err)
			}
		}
		if !isNew {
			if err := txq.UpdateConversationActivityForInbound(ctx, store.UpdateConversationActivityForInboundParams{
				OccurredAt: occurredAt, PreviewText: preview(text), ID: conversationID,
			}); err != nil {
				return fmt.Errorf("communications: update the conversation's activity: %w", err)
			}
		}
		if err := link.suggest(ctx, txq, conversationID, customerID, participantID, now); err != nil {
			return err
		}
		if err := txq.InsertInboundReceipt(ctx, store.InsertInboundReceiptParams{
			ID: uuid.New(), ChannelID: c.channelID, ProviderMessageID: key, MessageID: &messageID,
			Outcome: receiptImported, ReceivedAt: occurredAt, CreatedAt: now,
		}); err != nil {
			return fmt.Errorf("communications: write the receipt: %w", err)
		}
		if err := notifyMessage(ctx, tx, conversationID, eventMessageAdded, messageID); err != nil {
			return err
		}
		return w.advanceCursor(ctx, txq, c, event.Cursor, now)
	})
	if txErr == nil {
		return nil
	}

	// Nothing of this import was committed: its objects are no one's.
	forgetInboundObjects(ctx, w.deps.Pool, w.logger(), messengerSyncWorkerName, objects, now)
	if db.IsUniqueViolation(txErr, "ux_inbound_receipts_provider") {
		// Another holder imported this very event first.
		return w.advanceOnly(ctx, c, event.Cursor)
	}
	if errors.Is(txErr, errSyncLeaseLost) {
		return txErr
	}
	return fmt.Errorf("communications: import a messenger event: %w", txErr)
}

// skipWithReceipt writes a skipped receipt for an echo of a part we sent and
// advances the cursor past it, in one transaction.
func (w *MessengerSyncWorker) skipWithReceipt(ctx context.Context, c *messengerCycle, event contracts.MessengerEvent, key string, now time.Time) error {
	locked := context.WithValue(ctx, lockedTxKey{}, true)
	err := db.WithTx(locked, w.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		ctx := locked
		txq := store.New(tx)
		if err := txq.InsertInboundReceipt(ctx, store.InsertInboundReceiptParams{
			ID: uuid.New(), ChannelID: c.channelID, ProviderMessageID: key, Outcome: receiptSkipped,
			Reason: nonEmpty(reasonOwnMessage), ReceivedAt: eventTime(event, now), CreatedAt: now,
		}); err != nil && !db.IsUniqueViolation(err, "ux_inbound_receipts_provider") {
			return fmt.Errorf("communications: write a receipt: %w", err)
		}
		return w.advanceCursor(ctx, txq, c, event.Cursor, now)
	})
	return err
}

// inboundFile is one attachment of an event, fetched and keyed.
type inboundFile struct {
	id          uuid.UUID
	key         string
	fileName    string
	contentType string
	content     []byte
}

// fetchAttachments opens each of an event's attachments through the provider and
// stores its bytes in Communications' own storage (N2): after the objects are
// reserved, before any row names them. An attachment larger than the inbound
// limit is left out and logged. A failure at the provider or the store is
// errImportTransient.
func (w *MessengerSyncWorker) fetchAttachments(ctx context.Context, c *messengerCycle, event contracts.MessengerEvent,
	messageID uuid.UUID, now time.Time) ([]inboundObject, []inboundFile, error) {
	if len(event.Attachments) == 0 {
		return nil, nil, nil
	}
	max := w.inboundMaxBytes()
	prefix := fmt.Sprintf("inbound/%s/%s", hexN(c.channelID), hexN(messageID))
	var objects []inboundObject
	var files []inboundFile
	for _, ref := range event.Attachments {
		body, file, err := messengerOpenAttachment(ctx, c.provider, c.address, ref.Ref)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %w", errImportTransient, err)
		}
		content, err := io.ReadAll(io.LimitReader(body, max+1))
		_ = body.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("%w: read an attachment: %w", errImportTransient, err)
		}
		if int64(len(content)) > max {
			w.logger().Warn("messenger sync left an attachment out: it is larger than the inbound limit",
				"worker", messengerSyncWorkerName, "channel", c.channelID, "code", reasonTooLarge)
			continue
		}
		id := uuid.New()
		fileName, contentType := file.FileName, file.ContentType
		if fileName == "" {
			fileName = ref.FileName
		}
		if contentType == "" {
			contentType = ref.ContentType
		}
		f := inboundFile{
			id: id, key: fmt.Sprintf("%s/attachments/%s", prefix, hexN(id)),
			fileName: safeAttachmentFileName(fileName), contentType: sanitizedAttachmentContentType(contentType), content: content,
		}
		files = append(files, f)
		objects = append(objects, inboundObject{key: f.key, contentType: f.contentType, content: content})
	}
	if err := storeInboundObjects(ctx, w.deps.Pool, w.store, w.storeErr, w.logger(), messengerSyncWorkerName, objects, now); err != nil {
		return nil, nil, fmt.Errorf("%w: %w", errImportTransient, err)
	}
	return objects, files, nil
}

// inboundMaxBytes is the largest attachment the worker reads: the module's
// inbound limit, as the mailbox sync's.
func (w *MessengerSyncWorker) inboundMaxBytes() int64 {
	if w.deps.Config != nil && w.deps.Config.CommunicationsInboundMaxBytes > 0 {
		return w.deps.Config.CommunicationsInboundMaxBytes
	}
	return messengerImportMaxBytes
}

// profileOf reads the participant's name from the platform when the participant
// is new or its profile is older than messengerProfileMaxAge (N3 step 4): the
// name, and whether a profile was read. A profile that cannot be read is logged
// and the message imported without a name; it is asked for again with the next.
func (w *MessengerSyncWorker) profileOf(ctx context.Context, q *store.Queries, c *messengerCycle, participant string, now time.Time) (string, bool) {
	if participant == "" {
		return "", false
	}
	known, err := q.FindParticipantByChannelAndAddress(ctx, store.FindParticipantByChannelAndAddressParams{
		ChannelID: c.channelID, Address: participant,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		w.logger().Warn("messenger sync could not look the participant up; its profile is not read",
			"worker", messengerSyncWorkerName, "channel", c.channelID, "error", err)
		return "", false
	default:
		fetchedAt, err := q.FindParticipantProfile(ctx, known.ID)
		if err == nil && now.Sub(fetchedAt) < messengerProfileMaxAge {
			return "", false
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			w.logger().Warn("messenger sync could not look the participant's profile up",
				"worker", messengerSyncWorkerName, "channel", c.channelID, "error", err)
			return "", false
		}
	}
	profile, err := messengerProfile(ctx, c.provider, c.address, participant)
	if err != nil {
		w.logger().Warn("messenger sync could not read a participant's profile; imported unnamed",
			"worker", messengerSyncWorkerName, "channel", c.channelID, "error", err)
		return "", false
	}
	name := strings.TrimSpace(profile.DisplayName)
	if utf8.RuneCountInString(name) > 200 {
		name = string([]rune(name)[:200])
	}
	return name, true
}

// messengerParticipant finds the channel's participant for the platform's id of
// the person — exactly, a PSID being no address that varies by case — or makes
// one, and writes the profile read: the name when there is one, and when it was
// read.
func (w *MessengerSyncWorker) messengerParticipant(ctx context.Context, txq *store.Queries, channelID uuid.UUID,
	address, name string, fetched bool, now time.Time) (uuid.UUID, error) {
	found, err := txq.FindParticipantByChannelAndAddress(ctx, store.FindParticipantByChannelAndAddressParams{
		ChannelID: channelID, Address: address,
	})
	var id uuid.UUID
	switch {
	case err == nil:
		id = found.ID
		if name != "" && (found.DisplayName == nil || *found.DisplayName != name) {
			if err := txq.SetParticipantDisplayName(ctx, store.SetParticipantDisplayNameParams{DisplayName: &name, ID: id}); err != nil {
				return uuid.Nil, fmt.Errorf("communications: name the participant: %w", err)
			}
		}
	case errors.Is(err, pgx.ErrNoRows):
		inserted, err := txq.InsertParticipant(ctx, store.InsertParticipantParams{
			ID: uuid.New(), ChannelID: channelID, Address: address, DisplayName: nonEmpty(name), CreatedAt: now,
		})
		if err != nil {
			return uuid.Nil, fmt.Errorf("communications: insert a participant: %w", err)
		}
		id = inserted.ID
	default:
		return uuid.Nil, fmt.Errorf("communications: find a participant: %w", err)
	}
	if fetched {
		if err := txq.UpsertParticipantProfile(ctx, store.UpsertParticipantProfileParams{ParticipantID: id, FetchedAt: now}); err != nil {
			return uuid.Nil, fmt.Errorf("communications: record the profile read: %w", err)
		}
	}
	return id, nil
}

// messengerThread is threading by participant (N3a): the message joins the
// newest open conversation of the channel the participant is in, else starts a
// new one — with no subject, its title being the participant's name. A closed
// conversation stays closed. It answers the conversation, whether it was made,
// and its customer.
func (w *MessengerSyncWorker) messengerThread(ctx context.Context, txq *store.Queries, channelID, participantID uuid.UUID,
	occurredAt, now time.Time, text string) (uuid.UUID, bool, *int32, error) {
	open, err := txq.FindOpenMessengerConversation(ctx, store.FindOpenMessengerConversationParams{
		ChannelID: channelID, ParticipantID: participantID,
	})
	switch {
	case err == nil:
		if err := txq.InsertConversationParticipant(ctx, store.InsertConversationParticipantParams{
			ConversationID: open.ID, ParticipantID: participantID,
		}); err != nil {
			return uuid.Nil, false, nil, fmt.Errorf("communications: link the participant: %w", err)
		}
		return open.ID, false, open.CustomerID, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return uuid.Nil, false, nil, fmt.Errorf("communications: find the open conversation: %w", err)
	}
	conversationID := uuid.New()
	if _, err := txq.InsertConversation(ctx, store.InsertConversationParams{
		ID: conversationID, ChannelID: channelID, LastActivityAt: occurredAt, PreviewText: preview(capText(text)), CreatedAt: now,
	}); err != nil {
		return uuid.Nil, false, nil, fmt.Errorf("communications: insert the conversation: %w", err)
	}
	if err := txq.InsertConversationParticipant(ctx, store.InsertConversationParticipantParams{
		ConversationID: conversationID, ParticipantID: participantID,
	}); err != nil {
		return uuid.Nil, false, nil, fmt.Errorf("communications: link the participant: %w", err)
	}
	return conversationID, true, nil, nil
}

// placeAnswer is where an answer an event carries belongs (N5): under the
// outbound message of this conversation its payload names — or, for an e-mail
// address shared in answer to the quick reply that asks for one, under the
// newest message of the conversation that asked. nil when it cannot be placed,
// the message then standing as plain text, logged at info.
func (w *MessengerSyncWorker) placeAnswer(ctx context.Context, txq *store.Queries, c *messengerCycle,
	conversationID uuid.UUID, event contracts.MessengerEvent) (*storedAnswer, error) {
	answer := event.Answer
	var question uuid.UUID
	if id, err := uuid.Parse(answer.MessageID); err == nil {
		ours, err := txq.OutboundMessageInConversation(ctx, store.OutboundMessageInConversationParams{
			ID: id, ConversationID: conversationID,
		})
		if err != nil {
			return nil, fmt.Errorf("communications: look for the question an answer is to: %w", err)
		}
		if ours {
			// The choice has to be one the question offered.
			raw, err := txq.GetMessageComponents(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("communications: read the question's components: %w", err)
			}
			if componentsOffer(raw, answer.ChoiceID) {
				question = id
			}
		}
	} else if answer.MessageID == "" && answer.ChoiceID == shareEmailChoiceID {
		id, err := txq.LatestShareEmailQuestion(ctx, conversationID)
		switch {
		case err == nil:
			question = id
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, fmt.Errorf("communications: look for the question an e-mail address answers: %w", err)
		}
	}
	if question == uuid.Nil {
		w.logger().Info("messenger sync could not place an answer; it is kept as plain text",
			"worker", messengerSyncWorkerName, "channel", c.channelID)
		return nil, nil
	}
	return &storedAnswer{MessageID: question.String(), ChoiceID: answer.ChoiceID, Title: capText(event.Text)}, nil
}

// suggest is the linker's writes for an e-mail address a person shared (N3a),
// inside the import's transaction: a suggestion and never a link. The customers
// its contacts belong to become candidates of a conversation that has no
// customer yet, and the participant gets the one contact when it has none.
func (l senderLink) suggest(ctx context.Context, txq *store.Queries, conversationID uuid.UUID, customerID *int32,
	participantID uuid.UUID, now time.Time) error {
	if l.contact != nil {
		if err := txq.SetParticipantContact(ctx, store.SetParticipantContactParams{ContactID: l.contact, ID: participantID}); err != nil {
			return fmt.Errorf("communications: link the participant's contact: %w", err)
		}
	}
	if customerID != nil {
		return nil
	}
	for _, id := range l.customers {
		if err := txq.InsertConversationCandidate(ctx, store.InsertConversationCandidateParams{
			ConversationID: conversationID, CustomerID: id, CreatedAt: now,
		}); err != nil {
			return fmt.Errorf("communications: add a candidate customer: %w", err)
		}
	}
	return nil
}

// importWatermark applies a delivered or read event (N2): every outbound message
// on the participant's conversations the platform accepted at or before the
// watermark becomes delivered, or read. It is monotonic and so idempotent, and
// goes through no receipt, whose outcome CHECK is shaped by the mailbox.
func (w *MessengerSyncWorker) importWatermark(ctx context.Context, c *messengerCycle, event contracts.MessengerEvent) error {
	now := w.now()
	status, from := "delivered", []string{"relay_accepted"}
	if event.Kind == contracts.MessengerEventRead {
		status, from = "read", []string{"relay_accepted", "delivered"}
	}
	watermark := event.Watermark
	if watermark.IsZero() {
		watermark = event.At
	}
	locked := context.WithValue(ctx, lockedTxKey{}, true)
	return db.WithTx(locked, w.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		ctx := locked
		txq := store.New(tx)
		participant, err := txq.FindParticipantByChannelAndAddress(ctx, store.FindParticipantByChannelAndAddressParams{
			ChannelID: c.channelID, Address: event.Participant,
		})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// Nobody we know: nothing of ours was sent to them.
		case err != nil:
			return fmt.Errorf("communications: find a participant: %w", err)
		default:
			// Kept, so a message still being sent when it arrives gets it at
			// its completion (reapplyWatermarks).
			if err := txq.UpsertMessengerWatermark(ctx, store.UpsertMessengerWatermarkParams{
				ParticipantID: participant.ID, Kind: status, Watermark: watermark,
			}); err != nil {
				return fmt.Errorf("communications: keep a watermark: %w", err)
			}
			if err := applyWatermark(ctx, tx, c.channelID, participant.ID, status, from, watermark, now); err != nil {
				return err
			}
		}
		return w.advanceCursor(ctx, txq, c, event.Cursor, now)
	})
}

// applyWatermark moves the deliveries a watermark covers to status, from the
// statuses in from, writing an event for each (AdvanceMessengerDeliveries), and
// tells each conversation it moved one in, once, with message.changed: the
// message when it is one, none when it is several.
func applyWatermark(ctx context.Context, tx pgx.Tx, channelID, participantID uuid.UUID,
	status string, from []string, watermark, now time.Time) error {
	txq := store.New(tx)
	moved, err := txq.AdvanceMessengerDeliveries(ctx, store.AdvanceMessengerDeliveriesParams{
		Status: status, ChannelID: channelID, ParticipantID: participantID,
		Watermark: &watermark, FromStatuses: from,
	})
	if err != nil {
		return fmt.Errorf("communications: advance the deliveries: %w", err)
	}
	for _, d := range moved {
		deliveryID := d.ID
		if err := txq.InsertMessageEvent(ctx, store.InsertMessageEventParams{
			ID: uuid.New(), MessageID: d.MessageID, DeliveryID: &deliveryID, EventType: status, OccurredAt: now,
		}); err != nil {
			return fmt.Errorf("communications: write a %s event: %w", status, err)
		}
	}
	messages := map[uuid.UUID]map[uuid.UUID]bool{}
	for _, d := range moved {
		if messages[d.ConversationID] == nil {
			messages[d.ConversationID] = map[uuid.UUID]bool{}
		}
		messages[d.ConversationID][d.MessageID] = true
	}
	for conversationID, ids := range messages {
		id := ""
		if len(ids) == 1 {
			for m := range ids {
				id = m.String()
			}
		}
		if err := notifyConversation(ctx, tx, conversationID, eventMessageChanged, id); err != nil {
			return err
		}
	}
	return nil
}

// componentsOffer reports whether a message's stored components offer choiceID:
// an answer option or a card's choice with that id, or the request for an e-mail
// address when choiceID is the one a shared address carries.
func componentsOffer(raw []byte, choiceID string) bool {
	if len(raw) == 0 {
		return false
	}
	var stored storedComponents
	if err := json.Unmarshal(raw, &stored); err != nil {
		return false
	}
	if choiceID == shareEmailChoiceID {
		return stored.ShareEmail
	}
	for _, o := range stored.AnswerOptions {
		if o.ID == choiceID {
			return true
		}
	}
	for _, card := range stored.Cards {
		for _, ch := range card.Choices {
			if ch.ID == choiceID {
				return true
			}
		}
	}
	return false
}
