package communications_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// personalDataOf is the module's own contracts.CustomerPersonalData, built
// from the harness's dependencies exactly as Compose builds it.
func personalDataOf(t *testing.T, h *modtest.Harness) contracts.CustomerPersonalData {
	t.Helper()
	build := communications.Module().CustomerPersonalData
	if build == nil {
		t.Fatal("the module declares no customer personal data")
	}
	return build(h.Deps())
}

// eraseCommunicationsCustomer runs the module's erase inside a transaction the
// test owns — the customers anonymisation's position — and commits it or rolls
// it back as told.
func eraseCommunicationsCustomer(t *testing.T, h *modtest.Harness, customerID int32, commit bool) []contracts.ErasedData {
	t.Helper()
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	erased, err := personalDataOf(t, h).EraseCustomerData(ctx, tx, customerID)
	if err != nil {
		t.Fatalf("EraseCustomerData(%d): %v", customerID, err)
	}
	if commit {
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
	return erased
}

// personsConversation is seedRetentionMessage's whole shape — a delivery with
// an event on it (the RESTRICT that makes the delete order load-bearing), an
// attachment, an idempotency record, an AI interaction — made a person's: the
// conversation is about customerID, and its message says something.
func personsConversation(t *testing.T, h *modtest.Harness, customerID int32, attachmentKey string) retentionFixture {
	t.Helper()
	fx := seedRetentionMessage(t, h, retentionSeed{attachmentKey: attachmentKey, idempotency: true, ai: true})
	h.Exec(t, `UPDATE communications.conversations SET customer_id = $2 WHERE id = $1`, fx.conversationID, customerID)
	h.Exec(t, `UPDATE communications.conversation_messages SET text_body = 'Hei, strømmen er borte igjen.' WHERE id = $1`, fx.messageID)
	return fx
}

// TestCustomerPersonalData_ExportsThePersonsCorrespondence is communications'
// section of a private person's export (customers GDPR design D2): each
// conversation about them with its subject and dates, each message's
// direction, date and body — the HTML one too, which is all an HTML-only
// message has — each attachment's name, and nothing of anybody else's. A
// customer with no conversation has no section at all.
func TestCustomerPersonalData_ExportsThePersonsCorrespondence(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	fx := personsConversation(t, h, 1001, "objects/"+uuid.NewString()+".txt")
	personsConversation(t, h, 1002, "")
	// A reply sent with only htmlBody, which validateSubjectAndBody accepts.
	h.Exec(t, `INSERT INTO communications.conversation_messages
		(id, conversation_id, direction, participant_id, html_body, occurred_at, created_at)
		VALUES ($1, $2, 'outbound', $3, '<p>Vi kommer i morgen.</p>', $4, $4)`,
		uuid.New(), fx.conversationID, fx.participantID, h.Now().Add(time.Minute))

	section, err := personalDataOf(t, h).ExportCustomerData(context.Background(), 1001)
	if err != nil {
		t.Fatalf("ExportCustomerData: %v", err)
	}
	body, err := json.Marshal(section)
	if err != nil {
		t.Fatalf("marshal the section: %v", err)
	}
	var got struct {
		Conversations []struct {
			ID       uuid.UUID `json:"id"`
			Subject  string    `json:"subject"`
			Status   string    `json:"status"`
			Messages []struct {
				Direction   string   `json:"direction"`
				TextBody    *string  `json:"textBody"`
				HTMLBody    *string  `json:"htmlBody"`
				Attachments []string `json:"attachments"`
			} `json:"messages"`
		} `json:"conversations"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if len(got.Conversations) != 1 || got.Conversations[0].ID != fx.conversationID || got.Conversations[0].Subject != "Retention fixture" {
		t.Fatalf("conversations = %s, want customer 1001's one", body)
	}
	messages := got.Conversations[0].Messages
	if len(messages) != 2 {
		t.Fatalf("messages = %s, want the conversation's two", body)
	}
	if first := messages[0]; first.Direction != "outbound" || first.TextBody == nil || *first.TextBody != "Hei, strømmen er borte igjen." ||
		first.HTMLBody != nil || !slices.Equal(first.Attachments, []string{"a.txt"}) {
		t.Errorf("first message = %s, want its text, no HTML, and its attachment name", body)
	}
	if second := messages[1]; second.TextBody != nil || second.HTMLBody == nil || *second.HTMLBody != "<p>Vi kommer i morgen.</p>" ||
		len(second.Attachments) != 0 {
		t.Errorf("the HTML-only message = %s, want its htmlBody and no textBody", body)
	}

	none, err := personalDataOf(t, h).ExportCustomerData(context.Background(), 1003)
	if err != nil || none != nil {
		t.Errorf("a customer with no conversation = %v, %v; want nil, nil", none, err)
	}
}

// TestCustomerPersonalData_ErasesThroughTheCleanupLedgerInsideTheCallersTransaction
// is the anonymisation's half (customers GDPR design D2): the person's
// conversations and every row under them go in retention's own order — the
// event before the delivery it names — the attachment's and the staged upload's
// objects are queued on the cleanup ledger, never deleted here, and a
// suggestion or candidate row naming
// the person on somebody else's conversation goes too. Rolled back, nothing
// went; run again, it finds nothing.
func TestCustomerPersonalData_ErasesThroughTheCleanupLedgerInsideTheCallersTransaction(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	key := "objects/" + uuid.NewString() + ".txt"
	fx := personsConversation(t, h, 1001, key)
	other := personsConversation(t, h, 1002, "")
	h.Exec(t, `UPDATE communications.conversations
	           SET suggested_customer_id = 1001, suggested_customer_confidence = 0.9, suggested_customer_reasoning = 'Kari skrev fra samme adresse'
	           WHERE id = $1`, other.conversationID)
	insertCandidate(t, h, other.conversationID.String(), 1001)
	// A clean upload staged on the person's conversation: its row cascades
	// with the conversation, so its object must reach the ledger first.
	staged := "staged-attachments/" + uuid.NewString() + "/a.bin"
	h.Exec(t, `INSERT INTO communications.attachment_uploads
		(id, conversation_id, uploaded_by_user_id, file_name, content_type, size_bytes, content_hash,
		 storage_key, is_inline, idempotency_key, expires_at, created_at)
		VALUES ($1, $2, $3, 'a.bin', 'application/octet-stream', 2, 'hash', $4, false, $5, $6, $7)`,
		uuid.New(), fx.conversationID, uuid.New(), staged, uuid.NewString(), h.Now().Add(time.Hour), h.Now())

	if erased := eraseCommunicationsCustomer(t, h, 1001, false); len(erased) != 5 {
		t.Fatalf("EraseCustomerData = %+v, want the five kinds", erased)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.conversations WHERE id = $1`, fx.conversationID); n != 1 {
		t.Fatal("a rolled-back erase deleted the conversation: it wrote outside the caller's transaction")
	}

	erased := eraseCommunicationsCustomer(t, h, 1001, true)
	want := []contracts.ErasedData{
		{Kind: "communications.conversations", Count: 1},
		{Kind: "communications.messages", Count: 1},
		{Kind: "communications.objects", Count: 2},
		{Kind: "communications.conversationSuggestions", Count: 1},
		{Kind: "communications.conversationCandidates", Count: 1},
	}
	if !slices.Equal(erased, want) {
		t.Errorf("EraseCustomerData = %+v, want %+v", erased, want)
	}
	for table, id := range map[string]uuid.UUID{
		"conversations":         fx.conversationID,
		"conversation_messages": fx.messageID,
		"message_deliveries":    fx.deliveryID,
		"message_events":        fx.eventID,
		"message_attachments":   fx.attachmentID,
	} {
		if n := h.Count(t, `SELECT count(*) FROM communications.`+pgx.Identifier{table}.Sanitize()+` WHERE id = $1`, id); n != 0 {
			t.Errorf("communications.%s still holds %s", table, id)
		}
	}
	for what, k := range map[string]string{"the attachment": key, "the staged upload": staged} {
		if n := h.Count(t, `SELECT count(*) FROM communications.attachment_cleanup_records WHERE storage_key = $1 AND status = 'pending'`, k); n != 1 {
			t.Errorf("pending cleanup records for %s = %d, want 1: its object goes through the ledger", what, n)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.conversations
	                     WHERE id = $1 AND customer_id = 1002 AND suggested_customer_id IS NULL
	                       AND suggested_customer_confidence IS NULL AND suggested_customer_reasoning IS NULL`, other.conversationID); n != 1 {
		t.Error("the other conversation lost its customer, or kept the suggestion naming the person")
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.conversation_customer_candidates WHERE customer_id = 1001`); n != 0 {
		t.Errorf("%d candidate rows still name the person", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.conversation_messages WHERE id = $1`, other.messageID); n != 1 {
		t.Error("the other customer's message was deleted")
	}

	for _, again := range eraseCommunicationsCustomer(t, h, 1001, true) {
		if again.Count != 0 {
			t.Errorf("a second erase %s = %d, want 0", again.Kind, again.Count)
		}
	}
}
