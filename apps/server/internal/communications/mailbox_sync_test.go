package communications_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the communications-mailbox-sync worker (Outlook connection
// design M14; delivery D, task 5): a Microsoft 365 channel's Inbox read through
// modtest.FakeChannelMailbox, each message parsed by the real internal/mail and
// imported once by its receipt. Every harness's own clean-up fails the test if
// the slot or the directory was called from inside one of the module's
// transactions (harness_test.go).

// inbox is one Microsoft 365 channel on a fake provider, made through the
// real API.
type inbox struct {
	h         *modtest.Harness
	fake      *modtest.FakeChannelMailbox
	mailbox   contracts.ChannelMailboxInfo
	channelID uuid.UUID
	address   string
}

// newInbox is a harness with a fake provider and one Microsoft 365 channel on
// a shared mailbox. A mailbox other than fake (a wrapper around it) may be
// given to the slot.
func newInbox(t *testing.T, slot contracts.ChannelMailbox, fake *modtest.FakeChannelMailbox, opts ...modtest.Option) *inbox {
	t.Helper()
	if fake == nil {
		fake = modtest.NewFakeChannelMailbox()
	}
	if slot == nil {
		slot = fake
	}
	h := newHarness(t, append([]modtest.Option{modtest.WithChannelMailbox(slot)}, opts...)...)
	return addInbox(t, h, fake)
}

// addInbox makes one more Microsoft 365 channel on fake in h.
func addInbox(t *testing.T, h *modtest.Harness, fake *modtest.FakeChannelMailbox) *inbox {
	t.Helper()
	admin := h.SignIn(t, "communications:channels-manage")
	address := channelAddress(t)
	info := sharedMailbox(fake, address, "")
	ch := createChannel(t, admin, entraChannelBody(address))
	return &inbox{h: h, fake: fake, mailbox: info, channelID: uuid.MustParse(ch.Id), address: strings.ToLower(address)}
}

// testMail is a message to put in an Inbox, rendered by bytes.
type testMail struct {
	From, To, Cc, Subject string
	MessageID, InReplyTo  string
	References            string
	Headers               map[string]string
	Text, HTML            string
	Attachments           []testAttachment
}

// testAttachment is a part of a testMail beside its bodies.
type testAttachment struct {
	FileName, ContentType, ContentID string
	Inline                           bool
	Content                          []byte
}

// bytes renders m as RFC 5322 with CRLF line ends: a single text part, or
// multipart/mixed with the bodies in a multipart/alternative and each
// attachment base64.
func (m testMail) bytes() []byte {
	var b strings.Builder
	header := func(name, value string) {
		if value != "" {
			fmt.Fprintf(&b, "%s: %s\r\n", name, value)
		}
	}
	header("From", m.From)
	header("To", m.To)
	header("Cc", m.Cc)
	header("Subject", m.Subject)
	header("Message-ID", m.MessageID)
	header("In-Reply-To", m.InReplyTo)
	header("References", m.References)
	header("Date", "Thu, 29 Oct 2026 10:00:00 +0000")
	for name, value := range m.Headers {
		header(name, value)
	}
	b.WriteString("MIME-Version: 1.0\r\n")
	if m.HTML == "" && len(m.Attachments) == 0 {
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
		b.WriteString(strings.ReplaceAll(m.Text, "\n", "\r\n"))
		b.WriteString("\r\n")
		return []byte(b.String())
	}
	b.WriteString("Content-Type: multipart/mixed; boundary=\"outer\"\r\n\r\n")
	b.WriteString("--outer\r\nContent-Type: multipart/alternative; boundary=\"inner\"\r\n\r\n")
	if m.Text != "" {
		b.WriteString("--inner\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + m.Text + "\r\n")
	}
	if m.HTML != "" {
		b.WriteString("--inner\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" + m.HTML + "\r\n")
	}
	b.WriteString("--inner--\r\n")
	for _, a := range m.Attachments {
		b.WriteString("--outer\r\n")
		fmt.Fprintf(&b, "Content-Type: %s; name=\"%s\"\r\n", a.ContentType, a.FileName)
		b.WriteString("Content-Transfer-Encoding: base64\r\n")
		if a.ContentID != "" {
			fmt.Fprintf(&b, "Content-ID: <%s>\r\n", a.ContentID)
		}
		disposition := "attachment"
		if a.Inline {
			disposition = "inline"
		}
		fmt.Fprintf(&b, "Content-Disposition: %s; filename=\"%s\"\r\n\r\n", disposition, a.FileName)
		b.WriteString(base64.StdEncoding.EncodeToString(a.Content) + "\r\n")
	}
	b.WriteString("--outer--\r\n")
	return []byte(b.String())
}

// plainMail is a text message from a stranger to the inbox, with a fresh
// Message-Id.
func (in *inbox) plainMail(text string) testMail {
	return testMail{
		From: "Kari Nordmann <Kari." + uuid.NewString()[:8] + "@Example.test>", To: in.address,
		Subject: "Hello " + uuid.NewString()[:8], MessageID: "<" + uuid.NewString() + "@example.test>", Text: text,
	}
}

// deliver puts m in the Inbox under a fresh id, received at received, in
// Graph's conversation conversationID; it answers the id.
func (in *inbox) deliver(m testMail, received time.Time, conversationID string) string {
	id := "AAMk" + strings.ReplaceAll(uuid.NewString(), "-", "")
	in.fake.AddInbox(in.mailbox.Mailbox, contracts.MailItem{
		ID: id, ReceivedAt: received, MessageID: m.MessageID, ConversationID: conversationID,
	}, m.bytes())
	return id
}

// worker is a mailbox sync worker on the harness's dependencies.
func (in *inbox) worker() *communications.MailboxSyncWorker {
	return communications.NewMailboxSyncWorker(in.h.Deps())
}

// sync drains the worker: ProcessOne until no channel is due.
func (in *inbox) sync(t *testing.T) {
	t.Helper()
	w := in.worker()
	for range 20 {
		processed, err := w.ProcessOne(context.Background())
		if err != nil {
			t.Fatalf("ProcessOne: %v", err)
		}
		if !processed {
			return
		}
	}
	t.Fatal("the mailbox sync never ran out of channels due")
}

// syncRow is one channel_sync row as a test reads it.
type syncRow struct {
	deltaLink  *string
	importFrom time.Time
	state      string
	errorCode  *string
	nextSyncAt time.Time
	lastSynced *time.Time
	attempts   int32
	leaseID    *string
}

func readSync(t *testing.T, h *modtest.Harness, channelID uuid.UUID) syncRow {
	t.Helper()
	var r syncRow
	if err := h.Pool().QueryRow(context.Background(),
		`SELECT delta_link, import_from, state, error, next_sync_at, last_synced_at, attempts, lease_id
		 FROM communications.channel_sync WHERE channel_id = $1`, channelID).
		Scan(&r.deltaLink, &r.importFrom, &r.state, &r.errorCode, &r.nextSyncAt, &r.lastSynced, &r.attempts, &r.leaseID); err != nil {
		t.Fatalf("read channel_sync: %v", err)
	}
	return r
}

// receipt is one inbound_receipts row.
type receipt struct {
	providerID string
	rfc        *string
	messageID  *uuid.UUID
	outcome    string
	reason     *string
}

func readReceipts(t *testing.T, h *modtest.Harness, channelID uuid.UUID) map[string]receipt {
	t.Helper()
	rows, err := h.Pool().Query(context.Background(),
		`SELECT provider_message_id, rfc_message_id, message_id, outcome, reason
		 FROM communications.inbound_receipts WHERE channel_id = $1`, channelID)
	if err != nil {
		t.Fatalf("read receipts: %v", err)
	}
	defer rows.Close()
	out := map[string]receipt{}
	for rows.Next() {
		var r receipt
		if err := rows.Scan(&r.providerID, &r.rfc, &r.messageID, &r.outcome, &r.reason); err != nil {
			t.Fatalf("scan receipt: %v", err)
		}
		out[r.providerID] = r
	}
	return out
}

// inboundMessage is one imported conversation_messages row.
type inboundMessage struct {
	id             uuid.UUID
	conversationID uuid.UUID
	direction      string
	sender         *string
	subject        *string
	text, html     *string
	rfc            *string
	rawKey         *string
	metadata       map[string]any
	occurredAt     time.Time
}

func readMessage(t *testing.T, h *modtest.Harness, id uuid.UUID) inboundMessage {
	t.Helper()
	var m inboundMessage
	var metadata []byte
	if err := h.Pool().QueryRow(context.Background(),
		`SELECT m.id, m.conversation_id, m.direction, p.address, m.subject, m.text_body, m.html_body,
		        m.rfc_message_id, m.raw_payload_storage_key, m.channel_metadata_json, m.occurred_at
		 FROM communications.conversation_messages m
		 LEFT JOIN communications.participants p ON p.id = m.participant_id
		 WHERE m.id = $1`, id).
		Scan(&m.id, &m.conversationID, &m.direction, &m.sender, &m.subject, &m.text, &m.html,
			&m.rfc, &m.rawKey, &metadata, &m.occurredAt); err != nil {
		t.Fatalf("read message %s: %v", id, err)
	}
	if len(metadata) > 0 {
		if err := json.Unmarshal(metadata, &m.metadata); err != nil {
			t.Fatalf("decode metadata: %v", err)
		}
	}
	return m
}

// importedMessage is the message the receipt of providerID points at.
func (in *inbox) importedMessage(t *testing.T, providerID string) inboundMessage {
	t.Helper()
	r, ok := readReceipts(t, in.h, in.channelID)[providerID]
	if !ok || r.messageID == nil {
		t.Fatalf("receipt of %s = %+v (found %v), want one with its message", providerID, r, ok)
	}
	return readMessage(t, in.h, *r.messageID)
}

func countInbound(t *testing.T, h *modtest.Harness, channelID uuid.UUID) int {
	t.Helper()
	return h.Count(t, `SELECT count(*) FROM communications.conversation_messages m
		JOIN communications.conversations c ON c.id = m.conversation_id
		WHERE c.channel_id = $1 AND m.direction = 'inbound'`, channelID)
}

// deltaCalls are the fake's Delta calls, in order.
func deltaCalls(fake *modtest.FakeChannelMailbox) []modtest.ChannelMailboxCall {
	var out []modtest.ChannelMailboxCall
	for _, c := range fake.Calls() {
		if c.Method == "Delta" {
			out = append(out, c)
		}
	}
	return out
}

// rawCalls are the message ids Raw was asked for, in order.
func rawCalls(fake *modtest.FakeChannelMailbox) []string {
	var out []string
	for _, c := range fake.Calls() {
		if c.Method == "Raw" {
			out = append(out, c.MessageID)
		}
	}
	return out
}

// conversationDetail is the detail of a conversation through the API.
func conversationDetail(t *testing.T, h *modtest.Harness, id uuid.UUID) map[string]any {
	t.Helper()
	c := h.SignIn(t, "communications:conversations-view")
	r := c.Do(http.MethodGet, "/api/v1/communications/conversations/"+id.String(), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get conversation: status %d body %s", r.Status, r.Body)
	}
	var detail map[string]any
	r.JSON(&detail)
	return detail
}

// detailMessage is the message id of a conversation's detail.
func detailMessage(t *testing.T, detail map[string]any, id uuid.UUID) map[string]any {
	t.Helper()
	for _, m := range detail["messages"].([]any) {
		if msg := m.(map[string]any); msg["id"] == id.String() {
			return msg
		}
	}
	t.Fatalf("the detail has no message %s", id)
	return nil
}

// TestMailboxSync_ImportsAMessage: one plain message becomes an inbound
// message of a new conversation — its sender a participant in lower case, its
// subject, text and bracketed Message-Id, its metadata, occurred_at Graph's
// receivedDateTime, its original stored and owned on the ledger — and an
// imported receipt; the first Delta reads afresh from import_from, and the
// finished page leaves the channel active with its delta link, due in five
// minutes.
func TestMailboxSync_ImportsAMessage(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	importFrom := readSync(t, in.h, in.channelID).importFrom

	mail := in.plainMail("Hei!\nCan you help?")
	mail.Cc = "Other <Other@Example.test>, " + in.address
	mail.References = "<root@example.test>"
	mail.InReplyTo = "<root@example.test>"
	received := in.h.Now().Add(time.Minute).Truncate(time.Microsecond)
	id := in.deliver(mail, received, "graph-conv-1")
	in.sync(t)

	calls := deltaCalls(in.fake)
	if len(calls) != 1 || calls[0].Link != "" || !calls[0].Since.Equal(importFrom) || calls[0].Folder != "inbox" {
		t.Fatalf("Delta calls = %+v, want one fresh read of the inbox since import_from %v", calls, importFrom)
	}
	r := readReceipts(t, in.h, in.channelID)[id]
	if r.outcome != "imported" || r.reason != nil || r.rfc == nil || *r.rfc != mail.MessageID {
		t.Errorf("receipt = %+v, want imported with the Message-Id", r)
	}
	m := in.importedMessage(t, id)
	sender := strings.ToLower(strings.TrimSuffix(strings.SplitN(mail.From, "<", 2)[1], ">"))
	switch {
	case m.direction != "inbound":
		t.Errorf("direction = %q, want inbound", m.direction)
	case m.sender == nil || *m.sender != sender:
		t.Errorf("sender = %v, want %s", m.sender, sender)
	case m.subject == nil || *m.subject != mail.Subject:
		t.Errorf("subject = %v, want %q", m.subject, mail.Subject)
	case m.text == nil || *m.text != "Hei!\nCan you help?":
		t.Errorf("text = %v", m.text)
	case m.html != nil:
		t.Errorf("html = %q, want none", *m.html)
	case m.rfc == nil || *m.rfc != mail.MessageID:
		t.Errorf("rfc_message_id = %v, want %s bracketed", m.rfc, mail.MessageID)
	case !m.occurredAt.Equal(received):
		t.Errorf("occurred_at = %v, want receivedDateTime %v", m.occurredAt, received)
	}
	if m.metadata["from"] != sender || m.metadata["graphConversationId"] != "graph-conv-1" ||
		m.metadata["inReplyTo"] != "<root@example.test>" || m.metadata["autoSubmitted"] != false {
		t.Errorf("metadata = %v", m.metadata)
	}
	if cc, _ := m.metadata["cc"].([]any); len(cc) != 2 || cc[0] != "other@example.test" {
		t.Errorf("metadata cc = %v, want the Cc in lower case", m.metadata["cc"])
	}
	if refs, _ := m.metadata["references"].([]any); len(refs) != 1 || refs[0] != "<root@example.test>" {
		t.Errorf("metadata references = %v", m.metadata["references"])
	}
	wantKey := fmt.Sprintf("inbound/%s/%s/raw.eml", strings.ReplaceAll(in.channelID.String(), "-", ""),
		strings.ReplaceAll(m.id.String(), "-", ""))
	if m.rawKey == nil || *m.rawKey != wantKey {
		t.Errorf("raw key = %v, want %s", m.rawKey, wantKey)
	}
	if recs := readCleanupRecords(t, in.h, wantKey); len(recs) != 1 || recs[0].status != "owned" {
		t.Errorf("the original's cleanup records = %+v, want one owned", recs)
	}
	// The Cc became a participant of the conversation; the mailbox did not.
	if n := in.h.Count(t, `SELECT count(*) FROM communications.conversation_participants cp
		JOIN communications.participants p ON p.id = cp.participant_id
		WHERE cp.conversation_id = $1`, m.conversationID); n != 2 {
		t.Errorf("conversation participants = %d, want the sender and the other Cc", n)
	}
	if n := in.h.Count(t, `SELECT count(*) FROM communications.participants WHERE lower(address) = $1`, in.address); n != 0 {
		t.Errorf("the mailbox's own address became a participant")
	}
	conv := modtest.One[string](t, in.h, `SELECT status FROM communications.conversations WHERE id = $1`, m.conversationID)
	if conv != "open" {
		t.Errorf("conversation status = %s, want open", conv)
	}

	s := readSync(t, in.h, in.channelID)
	if s.state != "active" || s.errorCode != nil || s.attempts != 0 || s.leaseID != nil || s.deltaLink == nil ||
		s.lastSynced == nil || !s.nextSyncAt.Equal(in.h.Now().Add(5*time.Minute)) {
		t.Errorf("channel_sync = %+v, want active with its delta link, due in five minutes", s)
	}

	// The original downloads as a file to save, byte for byte.
	c := in.h.SignIn(t, "communications:conversations-view")
	resp := c.Do(http.MethodGet, "/api/v1/communications/messages/"+m.id.String()+"/raw", nil)
	if resp.Status != http.StatusOK || string(resp.Body) != string(mail.bytes()) {
		t.Fatalf("raw download: status %d, body %d bytes, want 200 with the original", resp.Status, len(resp.Body))
	}
	if got := resp.Header("Content-Type"); got != "message/rfc822" {
		t.Errorf("Content-Type = %q", got)
	}
	wantDisposition := fmt.Sprintf(`attachment; filename="message-%s.eml"`, strings.ReplaceAll(m.id.String(), "-", ""))
	if got := resp.Header("Content-Disposition"); got != wantDisposition {
		t.Errorf("Content-Disposition = %q, want %q", got, wantDisposition)
	}
	detail := detailMessage(t, conversationDetail(t, in.h, m.conversationID), m.id)
	if detail["rawDownloadPath"] != "/api/v1/communications/messages/"+m.id.String()+"/raw" ||
		detail["autoSubmitted"] != false || detail["unreadable"] != false {
		t.Errorf("detail message = %v", detail)
	}
}

// TestMailboxSync_TheBackfilledRowIsClaimed: a channel made before receiving
// existed has the row 00092's backfill writes — pending, import_from the day
// of the upgrade — and its first Delta reads from there, never before.
func TestMailboxSync_TheBackfilledRowIsClaimed(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	upgrade := in.h.Now().Add(-time.Hour).Truncate(time.Microsecond)
	in.h.Exec(t, `DELETE FROM communications.channel_sync WHERE channel_id = $1`, in.channelID)
	in.h.Exec(t, `INSERT INTO communications.channel_sync (channel_id, folder, import_from, state, next_sync_at, updated_at)
		VALUES ($1, 'inbox', $2, 'pending', $2, $2)`, in.channelID, upgrade)
	before := in.deliver(in.plainMail("before"), upgrade.Add(-time.Minute), "")
	after := in.deliver(in.plainMail("after"), upgrade.Add(time.Minute), "")
	in.sync(t)

	calls := deltaCalls(in.fake)
	if len(calls) != 1 || !calls[0].Since.Equal(upgrade) {
		t.Fatalf("Delta calls = %+v, want one since %v", calls, upgrade)
	}
	receipts := readReceipts(t, in.h, in.channelID)
	if _, ok := receipts[before]; ok {
		t.Error("a message received before import_from has a receipt")
	}
	if receipts[after].outcome != "imported" {
		t.Errorf("receipt = %+v, want imported", receipts[after])
	}
}

// TestMailboxSync_FiftyOneMessages: the first cycle imports the 50 of a page
// cut at 50, oldest first, and stores the next page's link with next_sync_at
// now; the drain claims the channel again at once, imports the 51st and keeps
// the delta link.
func TestMailboxSync_FiftyOneMessages(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	base := in.h.Now().Add(time.Minute)
	// Added newest first, so the fake's page is in the reverse order.
	ids := make([]string, 51)
	for i := 50; i >= 0; i-- {
		ids[i] = in.deliver(in.plainMail(fmt.Sprintf("message %d", i)), base.Add(time.Duration(i)*time.Second), "")
	}
	w := in.worker()
	if processed, err := w.ProcessOne(context.Background()); err != nil || !processed {
		t.Fatalf("ProcessOne = %v, %v", processed, err)
	}
	if n := countInbound(t, in.h, in.channelID); n != 50 {
		t.Fatalf("after the first cycle %d messages, want 50", n)
	}
	s := readSync(t, in.h, in.channelID)
	if s.deltaLink == nil || s.state != "pending" || !s.nextSyncAt.Equal(in.h.Now()) || s.leaseID != nil {
		t.Errorf("after a cut page channel_sync = %+v, want the next link, pending, due now, unleased", s)
	}
	// Oldest first within the page: the 50 the page held are ids 50..1,
	// read 1..50.
	raws := rawCalls(in.fake)
	for i, id := range raws {
		if id != ids[1+i] {
			t.Fatalf("Raw call %d = %s, want %s: oldest first", i, id, ids[1+i])
		}
	}
	if processed, err := w.ProcessOne(context.Background()); err != nil || !processed {
		t.Fatalf("second ProcessOne = %v, %v", processed, err)
	}
	if n := countInbound(t, in.h, in.channelID); n != 51 {
		t.Fatalf("after the second cycle %d messages, want 51", n)
	}
	s = readSync(t, in.h, in.channelID)
	if s.state != "active" || s.deltaLink == nil || !s.nextSyncAt.Equal(in.h.Now().Add(5*time.Minute)) {
		t.Errorf("channel_sync = %+v, want active with the delta link", s)
	}
	if processed, _ := w.ProcessOne(context.Background()); processed {
		t.Error("a third cycle ran, want none due")
	}
	if calls := deltaCalls(in.fake); len(calls) != 2 || calls[1].Link == "" {
		t.Errorf("Delta calls = %+v, want the second to follow the first's link", calls)
	}
}

// TestMailboxSync_AnExpiredDeltaRestartsFromTheNewestReceipt: the stored link
// refused as expired is forgotten and the Inbox read afresh from the newest
// receipt's receivedDateTime; the overlap is skipped by its receipt, the new
// message imported once.
func TestMailboxSync_AnExpiredDeltaRestartsFromTheNewestReceipt(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	first := in.h.Now().Add(time.Minute).Truncate(time.Microsecond)
	second := first.Add(time.Minute)
	in.deliver(in.plainMail("one"), first, "")
	in.deliver(in.plainMail("two"), second, "")
	in.sync(t)

	in.fake.ExpireDelta(in.mailbox.Mailbox)
	third := in.deliver(in.plainMail("three"), second.Add(time.Minute), "")
	in.h.Advance(5 * time.Minute)
	in.sync(t)

	calls := deltaCalls(in.fake)
	if len(calls) != 3 || calls[1].Link == "" || calls[2].Link != "" || !calls[2].Since.Equal(second) {
		t.Fatalf("Delta calls = %+v, want the stored link, then a fresh read since %v", calls, second)
	}
	if n := countInbound(t, in.h, in.channelID); n != 3 {
		t.Errorf("%d messages, want 3: nothing imported twice", n)
	}
	if readReceipts(t, in.h, in.channelID)[third].outcome != "imported" {
		t.Error("the new message was not imported")
	}
	if s := readSync(t, in.h, in.channelID); s.state != "active" || s.deltaLink == nil {
		t.Errorf("channel_sync = %+v, want active with a new link", s)
	}
}

// goneMailbox is the fake with a message taken out of the Inbox between the
// delta and the read.
type goneMailbox struct {
	*modtest.FakeChannelMailbox
	mailbox, gone string
}

func (m *goneMailbox) Delta(ctx context.Context, mailbox, folder, link string, since time.Time) (contracts.MailDelta, error) {
	page, err := m.FakeChannelMailbox.Delta(ctx, mailbox, folder, link, since)
	if m.gone != "" {
		m.RemoveInbox(m.mailbox, m.gone)
	}
	return page, err
}

// TestMailboxSync_Receipts: every message gets one receipt — imported; a copy
// of an imported Message-Id under another id a duplicate pointing at the
// original; one larger than COMMUNICATIONS_INBOUND_MAX_BYTES skipped as
// too_large and one deleted before its read as gone, neither stored; one we
// sent skipped as own_message, but another installation's id with the same
// suffix imported; and one that does not parse imported as unreadable, failed
// as unparseable.
func TestMailboxSync_Receipts(t *testing.T) {
	t.Parallel()
	fake := modtest.NewFakeChannelMailbox()
	slot := &goneMailbox{FakeChannelMailbox: fake}
	in := newInbox(t, slot, fake, modtest.WithEnv("COMMUNICATIONS_INBOUND_MAX_BYTES", "1048576"))
	slot.mailbox = in.mailbox.Mailbox
	at := in.h.Now().Add(time.Minute)

	original := in.plainMail("original")
	originalID := in.deliver(original, at, "")
	copyID := in.deliver(original, at.Add(time.Second), "")

	large := in.plainMail(strings.Repeat("x", 1<<20))
	largeID := in.deliver(large, at.Add(2*time.Second), "")
	goneID := in.deliver(in.plainMail("gone"), at.Add(3*time.Second), "")
	slot.gone = goneID

	// One we sent: the outbound message of a conversation on this channel.
	c := in.h.SignIn(t, "communications:conversations-reply")
	sent := createConversation(t, c, newConversationBody("customer@example.test"))
	ours := in.plainMail("our own copy")
	ours.MessageID = "<" + expectedMessageID(uuid.MustParse(*sent.MessageId)) + ">"
	oursID := in.deliver(ours, at.Add(4*time.Second), "")
	theirs := in.plainMail("another installation's")
	theirs.MessageID = "<" + strings.ReplaceAll(uuid.NewString(), "-", "") + "@vantigo.invalid>"
	theirsID := in.deliver(theirs, at.Add(5*time.Second), "")

	unreadableID := "AAMk-unreadable"
	in.fake.AddInbox(in.mailbox.Mailbox, contracts.MailItem{ID: unreadableID, ReceivedAt: at.Add(6 * time.Second)},
		[]byte("this is not a message\r\n"))

	in.sync(t)

	receipts := readReceipts(t, in.h, in.channelID)
	for id, want := range map[string][2]string{
		originalID: {"imported", ""}, copyID: {"duplicate", ""}, largeID: {"skipped", "too_large"},
		goneID: {"skipped", "gone"}, oursID: {"skipped", "own_message"}, theirsID: {"imported", ""},
		unreadableID: {"failed", "unparseable"},
	} {
		r, ok := receipts[id]
		reason := ""
		if r.reason != nil {
			reason = *r.reason
		}
		if !ok || r.outcome != want[0] || reason != want[1] {
			t.Errorf("receipt of %s = %+v, want %s %s", id, r, want[0], want[1])
		}
	}
	if receipts[copyID].messageID == nil || receipts[originalID].messageID == nil ||
		*receipts[copyID].messageID != *receipts[originalID].messageID {
		t.Errorf("the duplicate points at %v, want the original's %v", receipts[copyID].messageID, receipts[originalID].messageID)
	}
	for _, id := range []string{largeID, goneID, oursID} {
		if receipts[id].messageID != nil {
			t.Errorf("receipt of %s has a message, want none stored", id)
		}
	}
	if n := countInbound(t, in.h, in.channelID); n != 3 {
		t.Errorf("%d inbound messages, want 3: the original, another installation's and the unreadable", n)
	}
	if n := in.h.Count(t, `SELECT count(*) FROM communications.attachment_cleanup_records`); n != 3 {
		t.Errorf("%d cleanup records, want the three originals only", n)
	}

	unreadable := readMessage(t, in.h, *receipts[unreadableID].messageID)
	if unreadable.text == nil || *unreadable.text != "Vantigo could not read this message." || unreadable.html != nil ||
		unreadable.rawKey == nil || unreadable.metadata["unreadable"] != true {
		t.Errorf("unreadable message = %+v", unreadable)
	}
	detail := detailMessage(t, conversationDetail(t, in.h, unreadable.conversationID), unreadable.id)
	if detail["unreadable"] != true || detail["rawDownloadPath"] == nil {
		t.Errorf("unreadable detail = %v", detail)
	}
}

// TestMailboxSync_IgnoresRemovedAndEarlierItems: a message gone from the Inbox
// keeps its conversation and gets nothing new; an item received before the
// channel's import_from — the delta answers changes outside its filter — is
// not read.
func TestMailboxSync_IgnoresRemovedAndEarlierItems(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	id := in.deliver(in.plainMail("kept"), in.h.Now().Add(time.Minute), "")
	in.sync(t)
	in.fake.RemoveInbox(in.mailbox.Mailbox, id)
	early := in.deliver(in.plainMail("old"), readSync(t, in.h, in.channelID).importFrom.Add(-time.Hour), "")
	in.h.Advance(5 * time.Minute)
	in.sync(t)

	if n := countInbound(t, in.h, in.channelID); n != 1 {
		t.Errorf("%d messages, want the one kept", n)
	}
	receipts := readReceipts(t, in.h, in.channelID)
	if _, ok := receipts[early]; ok || len(receipts) != 1 {
		t.Errorf("receipts = %v, want only the first message's", receipts)
	}
	for _, raw := range rawCalls(in.fake) {
		if raw == early {
			t.Error("an item received before import_from was read")
		}
	}
}

// TestMailboxSync_HTMLAndAttachments: the HTML is sanitised, its cid: image
// pointing at the inline attachment's download; the text derived from the
// HTML when there is none; every attachment unscanned and every object owned
// on the ledger. An unscanned attachment downloads as a file to save, a
// text/html one as application/octet-stream; a passive image keeps its type.
func TestMailboxSync_HTMLAndAttachments(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	png := []byte("\x89PNG\r\n\x1a\nfake")
	mail := in.plainMail("")
	mail.HTML = `<p onclick="x()">Hello <b>there</b></p><img src="cid:logo@x"><img src="https://tracker.example/p.gif"><script>alert(1)</script>`
	mail.Attachments = []testAttachment{
		{FileName: "logo.png", ContentType: "image/png", ContentID: "logo@x", Inline: true, Content: png},
		{FileName: "side ø.html", ContentType: "text/html", Content: []byte("<script>alert(1)</script>")},
	}
	id := in.deliver(mail, in.h.Now().Add(time.Minute), "")
	in.sync(t)

	m := in.importedMessage(t, id)
	rows, err := in.h.Pool().Query(context.Background(),
		`SELECT id, file_name, content_type, scan_status, is_inline, content_id, storage_key
		 FROM communications.message_attachments WHERE message_id = $1 ORDER BY file_name`, m.id)
	if err != nil {
		t.Fatal(err)
	}
	type attachment struct {
		id                      uuid.UUID
		name, contentType, scan string
		inline                  bool
		contentID               *string
		key                     string
	}
	var attachments []attachment
	for rows.Next() {
		var a attachment
		if err := rows.Scan(&a.id, &a.name, &a.contentType, &a.scan, &a.inline, &a.contentID, &a.key); err != nil {
			t.Fatal(err)
		}
		attachments = append(attachments, a)
	}
	rows.Close()
	if len(attachments) != 2 {
		t.Fatalf("attachments = %+v, want 2", attachments)
	}
	logo, page := attachments[0], attachments[1]
	if logo.name != "logo.png" || logo.scan != "unscanned" || !logo.inline || logo.contentID == nil || *logo.contentID != "logo@x" {
		t.Errorf("inline attachment = %+v", logo)
	}
	if page.scan != "unscanned" || page.contentType != "text/html" {
		t.Errorf("attachment = %+v", page)
	}
	for _, key := range []string{*m.rawKey, logo.key, page.key} {
		if recs := readCleanupRecords(t, in.h, key); len(recs) != 1 || recs[0].status != "owned" {
			t.Errorf("cleanup records of %s = %+v, want one owned", key, recs)
		}
	}

	if m.html == nil {
		t.Fatal("no html stored")
	}
	html := *m.html
	if !strings.Contains(html, `src="/api/v1/communications/attachments/`+logo.id.String()+`/download"`) {
		t.Errorf("html = %s, want the cid image at its download path", html)
	}
	for _, gone := range []string{"onclick", "<script", "tracker.example", "cid:"} {
		if strings.Contains(html, gone) {
			t.Errorf("html = %s, still has %q", html, gone)
		}
	}
	if m.text == nil || !strings.Contains(*m.text, "Hello there") {
		t.Errorf("text = %v, want it derived from the HTML", m.text)
	}

	detail := detailMessage(t, conversationDetail(t, in.h, m.conversationID), m.id)
	for _, a := range detail["attachments"].([]any) {
		if att := a.(map[string]any); att["downloadAvailable"] != true || att["scanStatus"] != "unscanned" {
			t.Errorf("detail attachment = %v, want unscanned and downloadable", att)
		}
	}

	c := in.h.SignIn(t, "communications:conversations-view")
	r := c.Do(http.MethodGet, "/api/v1/communications/attachments/"+page.id.String()+"/download", nil)
	if r.Status != http.StatusOK || string(r.Body) != "<script>alert(1)</script>" {
		t.Fatalf("download: status %d body %q", r.Status, r.Body)
	}
	if got := r.Header("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream for an unscanned text/html", got)
	}
	if got := r.Header("Content-Disposition"); got != "attachment; filename*=UTF-8''side%20%C3%B8.html" {
		t.Errorf("Content-Disposition = %q", got)
	}
	r = c.Do(http.MethodGet, "/api/v1/communications/attachments/"+logo.id.String()+"/download", nil)
	if r.Status != http.StatusOK || r.Header("Content-Type") != "image/png" ||
		!strings.HasPrefix(r.Header("Content-Disposition"), "attachment;") {
		t.Errorf("image download: status %d, %q, %q", r.Status, r.Header("Content-Type"), r.Header("Content-Disposition"))
	}
}

// TestMailboxSync_RawDownloadNotFound: a message with no original — the
// outbound one a conversation starts with — and one whose object is gone both
// answer 404.
func TestMailboxSync_RawDownloadNotFound(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	c := in.h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	sent := createConversation(t, c, newConversationBody("customer@example.test"))
	if r := c.Do(http.MethodGet, "/api/v1/communications/messages/"+*sent.MessageId+"/raw", nil); r.Status != http.StatusNotFound {
		t.Errorf("raw of an outbound message: status %d, want 404", r.Status)
	}
	id := in.deliver(in.plainMail("x"), in.h.Now().Add(time.Minute), "")
	in.sync(t)
	m := in.importedMessage(t, id)
	in.h.Exec(t, `UPDATE communications.conversation_messages SET raw_payload_storage_key = 'inbound/gone/raw.eml' WHERE id = $1`, m.id)
	if r := c.Do(http.MethodGet, "/api/v1/communications/messages/"+m.id.String()+"/raw", nil); r.Status != http.StatusNotFound {
		t.Errorf("raw whose object is gone: status %d, want 404", r.Status)
	}
	if r := c.Do(http.MethodGet, "/api/v1/communications/messages/"+uuid.NewString()+"/raw", nil); r.Status != http.StatusNotFound {
		t.Errorf("raw of an unknown message: status %d, want 404", r.Status)
	}
}

// TestMailboxSync_HoldsForAnUnavailableProvider: the provider off, or the
// slot under another key, holds the channel five minutes without spending an
// attempt or touching its state.
func TestMailboxSync_HoldsForAnUnavailableProvider(t *testing.T) {
	t.Parallel()
	for name, hold := range map[string]func(*modtest.FakeChannelMailbox){
		"status off":  func(f *modtest.FakeChannelMailbox) { f.SetStatus(contracts.MailboxUnavailable, "switch_off") },
		"another key": func(f *modtest.FakeChannelMailbox) { f.WithKey("other") },
		"delta unavailable": func(f *modtest.FakeChannelMailbox) {
			f.FailWith("Delta", contracts.ErrMailboxUnavailable)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			in := newInbox(t, nil, nil)
			in.deliver(in.plainMail("x"), in.h.Now().Add(time.Minute), "")
			hold(in.fake)
			in.sync(t)
			s := readSync(t, in.h, in.channelID)
			if s.state != "pending" || s.attempts != 0 || s.errorCode == nil || *s.errorCode != "provider_unavailable" ||
				!s.nextSyncAt.Equal(in.h.Now().Add(5*time.Minute)) || s.leaseID != nil || s.deltaLink != nil {
				t.Errorf("channel_sync = %+v, want held five minutes, no attempt spent", s)
			}
			if n := countInbound(t, in.h, in.channelID); n != 0 {
				t.Errorf("%d messages imported while held", n)
			}
		})
	}
}

// TestMailboxSync_Failures: a refusal — denied, gone, unknown — leaves the
// channel failed with its code for an hour, an attempt spent; any other
// failure backs off from a minute, doubling, the state kept; a failing Raw
// leaves no receipt, so the message is read again; the next good cycle
// forgets the failure.
func TestMailboxSync_Failures(t *testing.T) {
	t.Parallel()
	for err, code := range map[error]string{
		contracts.ErrMailboxDenied:  "mailbox_denied",
		contracts.ErrMailboxGone:    "mailbox_gone",
		contracts.ErrMailboxUnknown: "mailbox_unknown",
	} {
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			in := newInbox(t, nil, nil)
			in.fake.FailWith("Delta", err)
			in.sync(t)
			s := readSync(t, in.h, in.channelID)
			if s.state != "failed" || s.errorCode == nil || *s.errorCode != code || s.attempts != 1 ||
				!s.nextSyncAt.Equal(in.h.Now().Add(time.Hour)) {
				t.Errorf("channel_sync = %+v, want failed %s for an hour", s, code)
			}
		})
	}

	t.Run("other", func(t *testing.T) {
		t.Parallel()
		in := newInbox(t, nil, nil)
		id := in.deliver(in.plainMail("x"), in.h.Now().Add(time.Minute), "")
		in.fake.FailWith("Raw", errors.New("graph did not answer"))
		in.sync(t)
		s := readSync(t, in.h, in.channelID)
		if s.state != "pending" || s.errorCode == nil || *s.errorCode != "sync_failed" || s.attempts != 1 ||
			!s.nextSyncAt.Equal(in.h.Now().Add(time.Minute)) || s.deltaLink != nil {
			t.Errorf("after one failure channel_sync = %+v, want sync_failed, a minute, no link", s)
		}
		if _, ok := readReceipts(t, in.h, in.channelID)[id]; ok {
			t.Error("a message whose read failed has a receipt")
		}
		in.h.Advance(time.Minute)
		in.sync(t)
		if s := readSync(t, in.h, in.channelID); s.attempts != 2 || !s.nextSyncAt.Equal(in.h.Now().Add(2*time.Minute)) {
			t.Errorf("after two failures channel_sync = %+v, want two minutes", s)
		}
		in.fake.FailWith("Raw", nil)
		in.h.Advance(2 * time.Minute)
		in.sync(t)
		s = readSync(t, in.h, in.channelID)
		if s.state != "active" || s.errorCode != nil || s.attempts != 0 {
			t.Errorf("after a good cycle channel_sync = %+v, want active, the failure forgotten", s)
		}
		if readReceipts(t, in.h, in.channelID)[id].outcome != "imported" {
			t.Error("the message was not read again")
		}
	})
}

// TestMailboxSync_NeverClaimsAnInactiveOrStoppedChannel: a deactivated
// channel keeps its row but is not read; nor is a stopped one.
func TestMailboxSync_NeverClaimsAnInactiveOrStoppedChannel(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	in.deliver(in.plainMail("x"), in.h.Now().Add(time.Minute), "")
	admin := in.h.SignIn(t, "communications:channels-manage")
	if r := admin.Do(http.MethodPut, "/api/v1/communications/channels/"+in.channelID.String(), map[string]any{"isActive": false}); r.Status != http.StatusOK {
		t.Fatalf("deactivate: status %d body %s", r.Status, r.Body)
	}
	if processed, err := in.worker().ProcessOne(context.Background()); err != nil || processed {
		t.Errorf("ProcessOne on an inactive channel = %v, %v; want nothing claimed", processed, err)
	}
	in.h.Exec(t, `UPDATE communications.channels SET is_active = true WHERE id = $1`, in.channelID)
	in.h.Exec(t, `UPDATE communications.channel_sync SET state = 'stopped' WHERE channel_id = $1`, in.channelID)
	if processed, err := in.worker().ProcessOne(context.Background()); err != nil || processed {
		t.Errorf("ProcessOne on a stopped channel = %v, %v; want nothing claimed", processed, err)
	}
	if len(deltaCalls(in.fake)) != 0 {
		t.Error("the Inbox was read")
	}
}

// TestInboundMetrics_AFailedCycleCountsOne: communications.inbound.cycles_failed
// counts a failed cycle, not a held one.
func TestInboundMetrics_AFailedCycleCountsOne(t *testing.T) {
	t.Parallel()
	m := newMeterHarness(t)
	in := newInbox(t, nil, nil)
	w := communications.NewMailboxSyncWorkerWithMeterForTest(in.h.Deps(), m.provider)

	in.fake.SetStatus(contracts.MailboxUnavailable, "switch_off")
	if _, err := w.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := m.count(t, "communications.inbound.cycles_failed"); got != 0 {
		t.Errorf("cycles_failed after a hold = %d, want 0", got)
	}
	in.fake.SetStatus(contracts.MailboxAvailable, "")
	in.fake.FailWith("Delta", errors.New("graph did not answer"))
	in.h.Advance(5 * time.Minute)
	if _, err := w.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := m.count(t, "communications.inbound.cycles_failed"); got != 1 {
		t.Errorf("cycles_failed = %d, want 1", got)
	}
}
