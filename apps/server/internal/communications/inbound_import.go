package communications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	platformmail "github.com/vantigo-io/vantigo/server/internal/mail"
)

// This file is one inbound message's import (Outlook connection design M14;
// delivery D, reading 14): the message read whole, recognised as one we sent
// or a copy of one already imported, parsed and sanitised, its original and
// its attachments stored before any row names them, threaded, linked, and
// written in one transaction with the receipt that makes the import happen
// once.

const (
	// inboundUnreadableText is the text of a message Vantigo could not read;
	// its original is kept for download.
	inboundUnreadableText = "Vantigo could not read this message."
	// inboundMaxBody is the most a stored text_body or sanitised html_body
	// holds, in bytes: the detail answers every message's bodies at once.
	inboundMaxBody = 1 << 20
	// inboundDefaultMaxBytes is COMMUNICATIONS_INBOUND_MAX_BYTES's default,
	// for a worker built from a bare module.Deps.
	inboundDefaultMaxBytes = 25 << 20
	// maxRfcMessageID is conversation_messages.rfc_message_id's width; a
	// longer Message-Id is not kept.
	maxRfcMessageID = 998
	// maxAttachmentContentID is message_attachments.content_id's width.
	maxAttachmentContentID = 500
)

// The receipts' outcomes and reasons (inbound_receipts.outcome, .reason).
const (
	receiptImported  = "imported"
	receiptDuplicate = "duplicate"
	receiptSkipped   = "skipped"
	receiptFailed    = "failed"

	reasonTooLarge    = "too_large"
	reasonGone        = "gone"
	reasonOwnMessage  = "own_message"
	reasonUnparseable = "unparseable"
)

// ownMessageID is the Message-Id the outbox gives each message we send
// (rfcMessageIDFor): 32 hex digits at vantigo.invalid.
var ownMessageID = regexp.MustCompile(`^<([0-9a-f]{32})@vantigo\.invalid>$`)

// inboundMetadata is an inbound message's channel_metadata_json (M14): the
// addresses bare and lower case, the ids bracketed, references oldest first.
// Reply and reply-all read cc and references back (conversations_reply.go);
// the detail reads autoSubmitted and unreadable.
type inboundMetadata struct {
	From                string   `json:"from"`
	To                  []string `json:"to"`
	Cc                  []string `json:"cc"`
	InReplyTo           string   `json:"inReplyTo"`
	References          []string `json:"references"`
	AutoSubmitted       bool     `json:"autoSubmitted"`
	GraphConversationID string   `json:"graphConversationId"`
	Unreadable          bool     `json:"unreadable,omitempty"`
	HTMLDropped         bool     `json:"htmlDropped,omitempty"`
}

// inboundObject is one object the import stores before its transaction.
type inboundObject struct {
	key, contentType string
	content          []byte
}

// importItem carries one delta item to a receipt: skipped, a duplicate, or
// imported — readable or not. An error ends the cycle without a receipt, so
// the message is tried again.
func (w *MailboxSyncWorker) importItem(ctx context.Context, c *syncCycle, item contracts.MailItem) error {
	q := store.New(w.deps.Pool)
	raw, err := mailboxRaw(ctx, c.provider, c.mailbox, item.ID, w.inboundMaxBytes())
	switch {
	case errors.Is(err, contracts.ErrMailTooLarge):
		return w.writeReceipt(ctx, q, c, item, rfcOf(platformmail.NormalizeMessageID(item.MessageID)), nil, receiptSkipped, reasonTooLarge)
	case errors.Is(err, contracts.ErrMailItemGone):
		return w.writeReceipt(ctx, q, c, item, rfcOf(platformmail.NormalizeMessageID(item.MessageID)), nil, receiptSkipped, reasonGone)
	case err != nil:
		return err
	}

	// The Message-Id as the message itself says, read before the full parse.
	messageID := headerMessageID(raw)
	if messageID == "" {
		messageID = platformmail.NormalizeMessageID(item.MessageID)
	}
	rfc := rfcOf(messageID)

	// A message we sent, delivered back to the mailbox (a Cc of the shared
	// mailbox itself, say): only an id of this database is ours.
	if m := ownMessageID.FindStringSubmatch(messageID); m != nil {
		if id, perr := uuid.Parse(m[1]); perr == nil {
			own, err := q.MessageExists(ctx, id)
			if err != nil {
				return fmt.Errorf("communications: look for our own message: %w", err)
			}
			if own {
				return w.writeReceipt(ctx, q, c, item, rfc, nil, receiptSkipped, reasonOwnMessage)
			}
		}
	}

	// A copy of a message the channel already imported (the same mail
	// delivered twice, or moved out and back in under another id).
	if rfc != nil {
		original, err := q.InboundReceiptByRfcID(ctx, store.InboundReceiptByRfcIDParams{ChannelID: c.channelID, RfcMessageID: rfc})
		switch {
		case err == nil:
			return w.writeReceipt(ctx, q, c, item, rfc, original, receiptDuplicate, "")
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("communications: look for an imported copy: %w", err)
		}
	}

	in, perr := platformmail.ParseInbound(raw, platformmail.DefaultInboundLimits)
	return w.importMessage(ctx, c, item, raw, in, perr != nil)
}

// writeReceipt records a message the cycle will not import. A receipt
// another holder wrote first is the same answer.
func (w *MailboxSyncWorker) writeReceipt(ctx context.Context, q *store.Queries, c *syncCycle, item contracts.MailItem,
	rfc *string, messageID *uuid.UUID, outcome, reason string) error {
	err := q.InsertInboundReceipt(ctx, store.InsertInboundReceiptParams{
		ID: uuid.New(), ChannelID: c.channelID, ProviderMessageID: item.ID, RfcMessageID: rfc,
		MessageID: messageID, Outcome: outcome, Reason: nonEmpty(reason),
		ReceivedAt: receivedAt(item, w.now()), CreatedAt: w.now(),
	})
	if db.IsUniqueViolation(err, "ux_inbound_receipts_provider") {
		return nil
	}
	if err != nil {
		return fmt.Errorf("communications: write a receipt: %w", err)
	}
	return nil
}

// importMessage stores one message: the objects first, then one transaction.
// unreadable is a message ParseInbound could not read: its text says so, it
// keeps no HTML and no attachments, and it starts a conversation of its own.
func (w *MailboxSyncWorker) importMessage(ctx context.Context, c *syncCycle, item contracts.MailItem,
	raw []byte, in platformmail.Inbound, unreadable bool) error {
	now := w.now()
	messageID := uuid.New()
	prefix := fmt.Sprintf("inbound/%s/%s", hexN(c.channelID), hexN(messageID))
	rawKey := prefix + "/raw.eml"
	objects := []inboundObject{{key: rawKey, contentType: "message/rfc822", content: raw}}

	if unreadable {
		in.Text, in.HTML, in.Attachments = inboundUnreadableText, "", nil
	}

	// The attachments' ids first, so the HTML's cid: images can name their
	// download path.
	type attachmentRow struct {
		id  uuid.UUID
		key string
		a   platformmail.InboundAttachment
	}
	attachments := make([]attachmentRow, 0, len(in.Attachments))
	for _, a := range in.Attachments {
		id := uuid.New()
		key := fmt.Sprintf("%s/attachments/%s", prefix, hexN(id))
		attachments = append(attachments, attachmentRow{id: id, key: key, a: a})
		objects = append(objects, inboundObject{key: key, contentType: sanitizedAttachmentContentType(a.ContentType), content: a.Content})
	}
	inlineURL := func(contentID string) (string, bool) {
		for _, r := range attachments {
			if r.a.ContentID != "" && r.a.ContentID == contentID {
				return fmt.Sprintf("/api/v1/communications/attachments/%s/download", r.id), true
			}
		}
		for _, r := range attachments {
			if r.a.ContentID != "" && strings.EqualFold(r.a.ContentID, contentID) {
				return fmt.Sprintf("/api/v1/communications/attachments/%s/download", r.id), true
			}
		}
		return "", false
	}

	// The bodies: the HTML sanitised, the text the message's own or, when it
	// has only HTML, derived from the sanitised HTML — every message has a
	// text, which the AI context reads.
	metadata := inboundMetadataOf(in, item, unreadable)
	var htmlBody *string
	if in.HTML != "" {
		sanitized := platformmail.SanitizeHTML(in.HTML, inlineURL)
		switch {
		case len(sanitized) > inboundMaxBody:
			metadata.HTMLDropped = true
		case sanitized != "":
			htmlBody = &sanitized
		}
		if strings.TrimSpace(in.Text) == "" && sanitized != "" {
			in.Text = platformmail.TextFromHTML(sanitized)
		}
	}
	metadata.HTMLDropped = metadata.HTMLDropped || in.HTMLDropped
	text := capText(in.Text)
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("communications: encode the message's metadata: %w", err)
	}

	// Objects before rows (reading 4): each reserved and stored before the
	// transaction that names it; any failure queues what was reserved.
	if err := w.storeObjects(ctx, objects, now); err != nil {
		return err
	}

	// The linker and the thread, read before the transaction: the directory
	// is a call out of the module, and the thread is re-checked inside.
	var sender string
	if in.From != nil {
		sender = in.From.Address
	}
	link := w.lookupSender(ctx, c.channelID, sender)
	var threadID uuid.UUID
	if !unreadable {
		if threadID, err = findThread(ctx, store.New(w.deps.Pool), c.channelID, in, item, now); err != nil {
			w.forgetObjects(ctx, objects, now)
			return err
		}
	}

	occurredAt := receivedAt(item, now)
	rfc := rfcOf(in.MessageID)
	subject := nonEmpty(in.Subject)
	locked := context.WithValue(ctx, lockedTxKey{}, true)
	txErr := db.WithTx(locked, w.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		ctx := locked
		txq := store.New(tx)

		conversationID, isNew, customerID, err := w.threadInTx(ctx, txq, threadID, in.AutoSubmitted)
		if err != nil {
			return err
		}
		if isNew {
			conversationID = uuid.New()
			var customer *int32
			var source *string
			if len(link.customers) == 1 {
				customer, source = &link.customers[0], ptrString("automatic")
			}
			if _, err := txq.InsertConversation(ctx, store.InsertConversationParams{
				ID: conversationID, ChannelID: c.channelID, Subject: subject, CustomerID: customer,
				CustomerAssociationSource: source, LastActivityAt: occurredAt, PreviewText: preview(text), CreatedAt: now,
			}); err != nil {
				return fmt.Errorf("communications: insert the conversation: %w", err)
			}
			customerID = customer
		}

		// The participants: the sender, and each Cc but the mailbox itself.
		var senderID *uuid.UUID
		if in.From != nil && in.From.Address != "" {
			id, err := inboundParticipant(ctx, txq, c.channelID, *in.From, now)
			if err != nil {
				return err
			}
			senderID = &id
			if err := txq.InsertConversationParticipant(ctx, store.InsertConversationParticipantParams{
				ConversationID: conversationID, ParticipantID: id,
			}); err != nil {
				return fmt.Errorf("communications: link the sender: %w", err)
			}
		}
		for _, cc := range in.Cc {
			if cc.Address == "" || strings.EqualFold(cc.Address, c.channelAddress) || strings.EqualFold(cc.Address, sender) {
				continue
			}
			id, err := inboundParticipant(ctx, txq, c.channelID, cc, now)
			if err != nil {
				return err
			}
			if err := txq.InsertConversationParticipant(ctx, store.InsertConversationParticipantParams{
				ConversationID: conversationID, ParticipantID: id,
			}); err != nil {
				return fmt.Errorf("communications: link a Cc: %w", err)
			}
		}

		if err := txq.InsertInboundMessage(ctx, store.InsertInboundMessageParams{
			ID: messageID, ConversationID: conversationID, ParticipantID: senderID, Subject: subject,
			TextBody: &text, HtmlBody: htmlBody, ChannelMetadataJson: metadataJSON, RawPayloadStorageKey: &rawKey,
			OccurredAt: occurredAt, CreatedAt: now, RfcMessageID: rfc,
		}); err != nil {
			return fmt.Errorf("communications: insert the message: %w", err)
		}
		for _, r := range attachments {
			if err := txq.InsertInboundMessageAttachment(ctx, store.InsertInboundMessageAttachmentParams{
				ID: r.id, MessageID: messageID, FileName: safeAttachmentFileName(r.a.FileName),
				ContentType: sanitizedAttachmentContentType(r.a.ContentType), SizeBytes: int64(len(r.a.Content)),
				ContentHash: attachmentFingerprint(r.a.Content), ContentID: attachmentContentID(r.a.ContentID),
				StorageKey: r.key, IsInline: r.a.Inline, CreatedAt: now,
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
				Subject: subject, OccurredAt: occurredAt, PreviewText: preview(text), ID: conversationID,
			}); err != nil {
				return fmt.Errorf("communications: update the conversation's activity: %w", err)
			}
		}
		if err := link.write(ctx, txq, conversationID, isNew, customerID, senderID, now); err != nil {
			return err
		}

		outcome, reason := receiptImported, ""
		if unreadable {
			outcome, reason = receiptFailed, reasonUnparseable
		}
		if err := txq.InsertInboundReceipt(ctx, store.InsertInboundReceiptParams{
			ID: uuid.New(), ChannelID: c.channelID, ProviderMessageID: item.ID, RfcMessageID: rfc,
			MessageID: &messageID, Outcome: outcome, Reason: nonEmpty(reason), ReceivedAt: occurredAt, CreatedAt: now,
		}); err != nil {
			return fmt.Errorf("communications: write the receipt: %w", err)
		}
		return nil
	})
	if txErr == nil {
		return nil
	}

	// Nothing of this import was committed: its objects are no one's.
	w.forgetObjects(ctx, objects, now)
	switch {
	case db.IsUniqueViolation(txErr, "ux_inbound_receipts_provider"):
		// Another holder imported this very message first.
		return nil
	case db.IsUniqueViolation(txErr, "ux_inbound_receipts_rfc"):
		// Another holder imported a copy of it, under another id, first.
		q := store.New(w.deps.Pool)
		original, err := q.InboundReceiptByRfcID(ctx, store.InboundReceiptByRfcIDParams{ChannelID: c.channelID, RfcMessageID: rfc})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("communications: read the imported copy: %w", err)
		}
		return w.writeReceipt(ctx, q, c, item, rfc, original, receiptDuplicate, "")
	}
	return fmt.Errorf("communications: import a message: %w", txErr)
}

// threadInTx re-reads the conversation findThread matched, locked: gone or
// archived starts a new one; closed is reopened unless the message is an
// automatic one (an out-of-office answer does not reopen). It answers the
// conversation, whether a new one is to be made, and its customer.
func (w *MailboxSyncWorker) threadInTx(ctx context.Context, txq *store.Queries, threadID uuid.UUID, auto bool) (uuid.UUID, bool, *int32, error) {
	if threadID == uuid.Nil {
		return uuid.Nil, true, nil, nil
	}
	conv, err := txq.LockConversationForInbound(ctx, threadID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, true, nil, nil
	}
	if err != nil {
		return uuid.Nil, false, nil, fmt.Errorf("communications: lock the thread's conversation: %w", err)
	}
	switch conv.Status {
	case "archived":
		return uuid.Nil, true, nil, nil
	case "closed":
		if !auto {
			if err := txq.ReopenConversation(ctx, conv.ID); err != nil {
				return uuid.Nil, false, nil, fmt.Errorf("communications: reopen the conversation: %w", err)
			}
		}
	}
	return conv.ID, false, conv.CustomerID, nil
}

// inboundParticipant finds the channel's participant for a, in any case, or
// makes one with the address in lower case (reading 21).
func inboundParticipant(ctx context.Context, txq *store.Queries, channelID uuid.UUID, a platformmail.Address, now time.Time) (uuid.UUID, error) {
	address := strings.ToLower(a.Address)
	found, err := txq.FindParticipantByChannelAndAddressFold(ctx, store.FindParticipantByChannelAndAddressFoldParams{
		ChannelID: channelID, Address: address,
	})
	if err == nil {
		return found.ID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("communications: find a participant: %w", err)
	}
	var name *string
	if n := strings.TrimSpace(a.Name); n != "" && utf8.RuneCountInString(n) <= 200 {
		name = &n
	}
	inserted, err := txq.InsertParticipant(ctx, store.InsertParticipantParams{
		ID: uuid.New(), ChannelID: channelID, Address: address, DisplayName: name, CreatedAt: now,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("communications: insert a participant: %w", err)
	}
	return inserted.ID, nil
}

// storeObjects reserves and stores each object, in order. On a failure the
// reservations already made are queued for deletion, and the error ends the
// cycle.
func (w *MailboxSyncWorker) storeObjects(ctx context.Context, objects []inboundObject, now time.Time) error {
	if w.storeErr != nil {
		return fmt.Errorf("communications: attachment storage is required: %w", w.storeErr)
	}
	for i, o := range objects {
		if err := reserveObjectKey(ctx, w.deps.Pool, o.key, now); err != nil {
			w.forgetObjects(ctx, objects[:i], now)
			return fmt.Errorf("communications: reserve an inbound object: %w", err)
		}
		if err := w.store.Put(ctx, o.key, bytes.NewReader(o.content), o.contentType); err != nil {
			w.forgetObjects(ctx, objects[:i+1], now)
			return fmt.Errorf("communications: store an inbound object: %w", err)
		}
	}
	return nil
}

// forgetObjects queues objects whose import did not commit for deletion, so
// the cleanup worker removes them now rather than when their reservation
// expires. A failure is logged: the reservation's expiry is the fallback.
func (w *MailboxSyncWorker) forgetObjects(ctx context.Context, objects []inboundObject, now time.Time) {
	if len(objects) == 0 {
		return
	}
	err := db.WithTx(ctx, w.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		for _, o := range objects {
			if err := queueObjectForDeletion(ctx, q, o.key, nil, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		w.logger().Warn("mailbox sync could not queue an import's objects for deletion; their reservations expire",
			"worker", mailboxSyncWorkerName, "error", err)
	}
}

// inboundMetadataOf is the message's channel_metadata_json.
func inboundMetadataOf(in platformmail.Inbound, item contracts.MailItem, unreadable bool) inboundMetadata {
	m := inboundMetadata{
		To: []string{}, Cc: []string{}, References: []string{},
		InReplyTo: in.InReplyTo, AutoSubmitted: in.AutoSubmitted, GraphConversationID: item.ConversationID,
		Unreadable: unreadable,
	}
	if in.From != nil {
		m.From = strings.ToLower(in.From.Address)
	}
	for _, a := range in.To {
		if a.Address != "" {
			m.To = append(m.To, strings.ToLower(a.Address))
		}
	}
	for _, a := range in.Cc {
		if a.Address != "" {
			m.Cc = append(m.Cc, strings.ToLower(a.Address))
		}
	}
	m.References = append(m.References, in.References...)
	return m
}

// headerMessageID is the message's own Message-Id, bracketed, or "" when its
// header block does not parse or names none.
func headerMessageID(raw []byte) string {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return ""
	}
	return platformmail.NormalizeMessageID(msg.Header.Get("Message-Id"))
}

// rfcOf is a Message-Id as stored: nil when there is none or it is wider
// than the column.
func rfcOf(id string) *string {
	if id == "" || len(id) > maxRfcMessageID {
		return nil
	}
	return &id
}

// attachmentContentID is a received part's Content-ID as stored: nil when it
// has none or it is wider than the column.
func attachmentContentID(id string) *string {
	if id == "" || utf8.RuneCountInString(id) > maxAttachmentContentID {
		return nil
	}
	return &id
}

// capText cuts a text body to inboundMaxBody bytes, with "…".
func capText(s string) string {
	if len(s) <= inboundMaxBody {
		return s
	}
	cut := inboundMaxBody - len("…")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// receivedAt is the item's receivedDateTime, or now when the provider gave
// none.
func receivedAt(item contracts.MailItem, now time.Time) time.Time {
	if item.ReceivedAt.IsZero() {
		return now
	}
	return item.ReceivedAt.UTC()
}

// inboundMaxBytes is COMMUNICATIONS_INBOUND_MAX_BYTES: the largest message
// the worker reads.
func (w *MailboxSyncWorker) inboundMaxBytes() int64 {
	if w.deps.Config != nil && w.deps.Config.CommunicationsInboundMaxBytes > 0 {
		return w.deps.Config.CommunicationsInboundMaxBytes
	}
	return inboundDefaultMaxBytes
}

func ptrString(s string) *string { return &s }
