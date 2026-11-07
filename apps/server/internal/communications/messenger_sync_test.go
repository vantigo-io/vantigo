package communications_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the messenger sync (Messenger design N3, N3a, N5, N12, delivery
// A): the claim, the cursor and the Ack, the import of a message, an echo, an
// answer and a watermark, threading by participant, and what a failure does to
// the row. contracts.ChannelMessenger is a fake (fake_messenger_test.go).

// chatFixture is a communications installation with a Messenger channel on a fake
// Page.
type chatFixture struct {
	h       *modtest.Harness
	fake    *fakeMessenger
	c       *modtest.Client
	channel channelJSON
}

func newChat(t *testing.T, opts ...modtest.Option) chatFixture {
	t.Helper()
	h, fake := messengerHarness(t, opts...)
	c := signInChat(t, h)
	return chatFixture{h: h, fake: fake, c: c, channel: newMessengerChannel(t, c, fake)}
}

// signInChat is a user with every permission a Messenger conversation needs.
func signInChat(t *testing.T, h *modtest.Harness) *modtest.Client {
	t.Helper()
	return h.SignIn(t, "communications:channels-manage", "communications:conversations-view",
		"communications:conversations-reply", "communications:conversations-manage")
}

// resign signs the user in again: a test that moves the clock a day or more has
// outlived the session.
func (f *chatFixture) resign(t *testing.T) { f.c = signInChat(t, f.h) }

// worker is a messenger sync worker, as one replica.
func (f chatFixture) worker() *communications.MessengerSyncWorker {
	return communications.NewMessengerSyncWorker(f.h.Deps())
}

// sync drains the worker: ProcessOne until no channel is due.
func (f chatFixture) sync(t *testing.T) {
	t.Helper()
	w := f.worker()
	for range 20 {
		processed, err := w.ProcessOne(context.Background())
		if err != nil {
			t.Fatalf("ProcessOne: %v", err)
		}
		if !processed {
			return
		}
	}
	t.Fatal("ProcessOne kept finding work")
}

// text pushes a message of the participant's at an offset from the harness's now.
func (f chatFixture) text(participant, platformID, text string, offset time.Duration) int64 {
	return f.fake.Text(f.channel.Address, participant, platformID, text, f.h.Now().Add(offset))
}

// conversations is the channel's conversations, newest first.
func (f chatFixture) conversations(t *testing.T) []string {
	t.Helper()
	rows, err := f.h.Pool().Query(context.Background(), `
		SELECT id::text FROM communications.conversations WHERE channel_id = $1 ORDER BY created_at DESC, id DESC`, f.channel.Id)
	if err != nil {
		t.Fatalf("list conversations: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}

// chatDetailJSON is the part of a conversation detail a Messenger conversation
// adds.
type chatDetailJSON struct {
	ChannelType     string `json:"channelType"`
	ComponentLimits *struct {
		MaxText          int  `json:"maxText"`
		MaxAnswerOptions int  `json:"maxAnswerOptions"`
		MaxAnswerTitle   int  `json:"maxAnswerTitle"`
		ShareEmail       bool `json:"shareEmail"`
		MaxCards         int  `json:"maxCards"`
	} `json:"componentLimits"`
	ReplyWindow *struct {
		State    string     `json:"state"`
		ClosesAt *time.Time `json:"closesAt"`
	} `json:"replyWindow"`
	Subject      *string `json:"subject"`
	Participants []struct {
		Address     string  `json:"address"`
		DisplayName *string `json:"displayName"`
	} `json:"participants"`
	Messages []chatMessageJSON `json:"messages"`
}

type chatMessageJSON struct {
	Id         string    `json:"id"`
	Direction  string    `json:"direction"`
	TextBody   *string   `json:"textBody"`
	OccurredAt time.Time `json:"occurredAt"`
	Echo       bool      `json:"echo"`
	AnswerTo   *struct {
		MessageId string `json:"messageId"`
		ChoiceId  string `json:"choiceId"`
		Title     string `json:"title"`
		Product   *struct {
			ProductId    int32   `json:"productId"`
			Name         string  `json:"name"`
			AttachmentId *string `json:"attachmentId"`
		} `json:"product"`
	} `json:"answerTo"`
	Components *struct {
		AnswerOptions *[]struct {
			Id    string `json:"id"`
			Title string `json:"title"`
		} `json:"answerOptions"`
		ShareEmail *bool `json:"shareEmail"`
		Cards      *[]struct {
			Title        string  `json:"title"`
			AttachmentId *string `json:"attachmentId"`
		} `json:"cards"`
	} `json:"components"`
	Attachments []struct {
		Id           string `json:"id"`
		FileName     string `json:"fileName"`
		Role         string `json:"role"`
		DownloadPath string `json:"downloadPath"`
	} `json:"attachments"`
	Deliveries []struct {
		Status string `json:"status"`
	} `json:"deliveries"`
}

// detail reads a conversation's detail through the API.
func (f chatFixture) detail(t *testing.T, id string) chatDetailJSON {
	t.Helper()
	r := f.c.Do(http.MethodGet, "/api/v1/communications/conversations/"+id, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET conversation: status %d body %s", r.Status, r.Body)
	}
	var d chatDetailJSON
	r.JSON(&d)
	return d
}

// cursor is the channel's messenger_sync cursor.
func (f chatFixture) cursor(t *testing.T) int64 {
	t.Helper()
	return modtest.One[int64](t, f.h, `SELECT cursor FROM communications.messenger_sync WHERE channel_id = $1`, f.channel.Id)
}

// messageCount is how many messages the channel's conversations hold.
func (f chatFixture) messageCount(t *testing.T) int {
	t.Helper()
	return f.h.Count(t, `
		SELECT count(*) FROM communications.conversation_messages m
		JOIN communications.conversations c ON c.id = m.conversation_id WHERE c.channel_id = $1`, f.channel.Id)
}

// outbound writes a message the Page sent in conversation, as the outbox leaves
// it, its delivery accepted at accepted when that is not zero.
func (f chatFixture) outbound(t *testing.T, conversation, text, components string, accepted time.Time) (message, delivery uuid.UUID) {
	t.Helper()
	message, delivery = uuid.New(), uuid.New()
	var comp any
	if components != "" {
		comp = components
	}
	f.h.Exec(t, `
		INSERT INTO communications.conversation_messages (id, conversation_id, direction, text_body, components, occurred_at, created_at)
		VALUES ($1, $2, 'outbound', $3, $4::jsonb, $5, $5)`, message, conversation, text, comp, f.h.Now())
	var at any
	if !accepted.IsZero() {
		at = accepted
	}
	f.h.Exec(t, `
		INSERT INTO communications.message_deliveries (id, message_id, recipient_address, recipient_type, status, attempts, accepted_at, created_at)
		VALUES ($1, $2, '4242', 'to', 'relay_accepted', 1, $3, now())`, delivery, message, at)
	if !accepted.IsZero() {
		// The one part it was sent as, stamped by the platform when it was accepted.
		f.h.Exec(t, `
			INSERT INTO communications.messenger_parts (message_id, part_index, attempted_at, platform_at, sent_at)
			VALUES ($1, 0, $2, $2, $2)`, message, accepted)
	}
	return message, delivery
}

func (f chatFixture) deliveryStatus(t *testing.T, delivery uuid.UUID) string {
	t.Helper()
	return modtest.One[string](t, f.h, `SELECT status FROM communications.message_deliveries WHERE id = $1`, delivery)
}

// TestMessengerSync_ImportsThreadsAndAcks: the events of a Page become the
// participant's messages in one conversation with no subject, the participant
// is named from the platform's profile once, the cursor is the provider's id of
// the last event and the provider is acknowledged up to it; a later message of
// the participant joins the same open conversation and reads no profile again.
func TestMessengerSync_ImportsThreadsAndAcks(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	f.fake.SetProfile("4242", "Kari Nordmann")
	f.text("4242", "m1", "Hello", 0)
	last := f.text("4242", "m2", "Is anyone there?", time.Second)

	f.sync(t)
	convs := f.conversations(t)
	if len(convs) != 1 {
		t.Fatalf("conversations = %v, want one", convs)
	}
	d := f.detail(t, convs[0])
	if d.ChannelType != "messenger" || d.Subject != nil {
		t.Errorf("channelType %q subject %v, want messenger and no subject", d.ChannelType, d.Subject)
	}
	if len(d.Participants) != 1 || d.Participants[0].Address != "4242" || d.Participants[0].DisplayName == nil || *d.Participants[0].DisplayName != "Kari Nordmann" {
		t.Errorf("participants = %+v, want the PSID, named", d.Participants)
	}
	if len(d.Messages) != 2 || d.Messages[0].Direction != "inbound" || *d.Messages[0].TextBody != "Hello" || *d.Messages[1].TextBody != "Is anyone there?" {
		t.Fatalf("messages = %+v, want the two in order", d.Messages)
	}
	if got := f.cursor(t); got != last {
		t.Errorf("cursor = %d, want %d", got, last)
	}
	if got := f.fake.Acked(f.channel.Address); got != last {
		t.Errorf("acked = %d, want %d", got, last)
	}
	if got := f.fake.CallCount("Profile"); got != 1 {
		t.Errorf("Profile calls = %d, want 1", got)
	}
	if got := modtest.One[string](t, f.h, `SELECT outcome FROM communications.inbound_receipts WHERE channel_id = $1 AND provider_message_id = 'm1'`, f.channel.Id); got != "imported" {
		t.Errorf("receipt = %q, want imported", got)
	}
	if got := modtest.One[string](t, f.h, `SELECT state || '/' || coalesce(last_error, '-') FROM communications.messenger_sync WHERE channel_id = $1`, f.channel.Id); got != "active/-" {
		t.Errorf("sync row = %s, want active with no error", got)
	}

	// Due again after 60 s regardless; the same participant joins the open
	// conversation and has its name, so no profile is read.
	f.h.Advance(2 * time.Minute)
	f.text("4242", "m3", "Hello again", 0)
	f.sync(t)
	if got := f.conversations(t); len(got) != 1 {
		t.Errorf("conversations = %v, want the same one", got)
	}
	if len(f.detail(t, convs[0]).Messages) != 3 {
		t.Error("the third message did not join the open conversation")
	}
	if got := f.fake.CallCount("Profile"); got != 1 {
		t.Errorf("Profile calls = %d, want still 1: the name is fresh", got)
	}
}

// TestMessengerSync_ProfileIsReadAgainAfter30Days: a participant's name older
// than 30 days is read from the platform again, and a profile that cannot be
// read leaves the message imported, unnamed.
func TestMessengerSync_ProfileIsReadAgainAfter30Days(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	f.fake.SetProfile("4242", "Kari")
	f.text("4242", "m1", "Hello", 0)
	f.sync(t)

	f.fake.SetProfile("4242", "Kari Nordmann")
	f.h.Advance(29 * 24 * time.Hour)
	f.text("4242", "m2", "Again", 0)
	f.sync(t)
	if got := f.fake.CallCount("Profile"); got != 1 {
		t.Fatalf("Profile calls after 29 days = %d, want 1", got)
	}
	f.h.Advance(2 * 24 * time.Hour)
	f.text("4242", "m3", "And again", 0)
	f.sync(t)
	if got := f.fake.CallCount("Profile"); got != 2 {
		t.Errorf("Profile calls after 31 days = %d, want 2", got)
	}
	if got := modtest.One[string](t, f.h, `SELECT display_name FROM communications.participants WHERE channel_id = $1 AND address = '4242'`, f.channel.Id); got != "Kari Nordmann" {
		t.Errorf("display_name = %q, want the new name", got)
	}

	// A profile that fails: the message is imported, the participant unnamed.
	f.fake.FailWith("Profile", errors.New("graph down"))
	f.text("5555", "m4", "Hi", 0)
	f.sync(t)
	if got := f.h.Count(t, `SELECT count(*) FROM communications.participants WHERE channel_id = $1 AND address = '5555' AND display_name IS NULL`, f.channel.Id); got != 1 {
		t.Errorf("an unnamed participant: %d rows, want 1", got)
	}
	if got := f.h.Count(t, `SELECT count(*) FROM communications.inbound_receipts WHERE channel_id = $1 AND provider_message_id = 'm4' AND outcome = 'imported'`, f.channel.Id); got != 1 {
		t.Errorf("the message was not imported while the profile failed")
	}
}

// TestMessengerSync_ReplayIsIdempotent: events read again — the cursor lost,
// the provider not having forgotten — are not imported twice: the receipts
// decide, and the cursor goes forward again.
func TestMessengerSync_ReplayIsIdempotent(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	f.text("4242", "m1", "One", 0)
	last := f.text("4242", "m2", "Two", time.Second)
	f.sync(t)
	if got := f.messageCount(t); got != 2 {
		t.Fatalf("messages = %d, want 2", got)
	}

	f.h.Exec(t, `UPDATE communications.messenger_sync SET cursor = 0, next_sync_at = now() - interval '1 hour' WHERE channel_id = $1`, f.channel.Id)
	f.fake.OnPending(nil)
	f.fakeResetAck()
	f.sync(t)
	if got := f.messageCount(t); got != 2 {
		t.Errorf("messages after the replay = %d, want still 2", got)
	}
	if got := f.h.Count(t, `SELECT count(*) FROM communications.inbound_receipts WHERE channel_id = $1`, f.channel.Id); got != 2 {
		t.Errorf("receipts = %d, want 2", got)
	}
	if got := f.cursor(t); got != last {
		t.Errorf("cursor = %d, want %d again", got, last)
	}
}

// fakeResetAck makes the provider forget it was acknowledged, as a replay of
// what it still holds.
func (f chatFixture) fakeResetAck() {
	f.fake.mu.Lock()
	defer f.fake.mu.Unlock()
	f.fake.acked[f.channel.Address] = 0
}

// TestMessengerSync_EventsOutOfOrder: the cursor is the provider's insertion id
// and the time only orders the display — an event that arrived later but
// happened earlier is imported after, and shown before.
func TestMessengerSync_EventsOutOfOrder(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	f.text("4242", "late", "Second said", 10*time.Second)
	last := f.text("4242", "early", "First said", 0)
	f.sync(t)

	d := f.detail(t, f.conversations(t)[0])
	if len(d.Messages) != 2 || *d.Messages[0].TextBody != "First said" || *d.Messages[1].TextBody != "Second said" {
		t.Errorf("messages = %+v, want ordered by what the platform says of when", d.Messages)
	}
	if got := f.cursor(t); got != last {
		t.Errorf("cursor = %d, want the last insertion id %d", got, last)
	}
}

// TestMessengerSync_AClosedConversationStaysClosed: a message of a participant
// whose conversation was closed starts a new one, and the closed one is left as
// it is; another participant never joins it.
func TestMessengerSync_AClosedConversationStaysClosed(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	f.text("4242", "m1", "Hello", 0)
	f.text("7777", "m2", "Someone else", 0)
	f.sync(t)
	convs := f.conversations(t)
	if len(convs) != 2 {
		t.Fatalf("conversations = %v, want one per participant", convs)
	}
	first := modtest.One[string](t, f.h, `
		SELECT c.id::text FROM communications.conversations c
		JOIN communications.conversation_participants cp ON cp.conversation_id = c.id
		JOIN communications.participants p ON p.id = cp.participant_id WHERE p.address = '4242'`)
	f.h.Exec(t, `UPDATE communications.conversations SET status = 'closed' WHERE id = $1`, first)

	f.h.Advance(2 * time.Minute)
	f.text("4242", "m3", "Hello again", 0)
	f.sync(t)
	if got := f.conversations(t); len(got) != 3 {
		t.Fatalf("conversations = %v, want a third, new one", got)
	}
	if got := modtest.One[string](t, f.h, `SELECT status FROM communications.conversations WHERE id = $1`, first); got != "closed" {
		t.Errorf("the closed conversation is %s, want closed", got)
	}
	if got := f.h.Count(t, `SELECT count(*) FROM communications.conversation_messages WHERE conversation_id = $1`, first); got != 1 {
		t.Errorf("the closed conversation holds %d messages, want its one", got)
	}
}

// TestMessengerSync_AttachmentsComeThroughOpenAttachment: an attachment of an
// event is read through the provider, stored in Communications' own storage and
// downloadable from the thread.
func TestMessengerSync_AttachmentsComeThroughOpenAttachment(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	f.fake.AddFile("ref-1", "photo.png", "image/png", []byte("PNGBYTES"))
	f.fake.Push(f.channel.Address, contracts.MessengerEvent{
		Kind: contracts.MessengerEventMessage, Participant: "4242", At: f.h.Now(), PlatformMessageID: "m1", Text: "Look",
		Attachments: []contracts.MessengerAttachmentRef{{Ref: "ref-1", FileName: "photo.png", ContentType: "image/png"}},
	})
	f.sync(t)

	d := f.detail(t, f.conversations(t)[0])
	if len(d.Messages) != 1 || len(d.Messages[0].Attachments) != 1 {
		t.Fatalf("messages = %+v, want one with its attachment", d.Messages)
	}
	a := d.Messages[0].Attachments[0]
	if a.FileName != "photo.png" || a.Role != "file" {
		t.Errorf("attachment = %+v, want photo.png of role file", a)
	}
	r := f.c.Do(http.MethodGet, a.DownloadPath, nil, modtest.SkipContract("a binary download"))
	if r.Status != http.StatusOK || string(r.Body) != "PNGBYTES" {
		t.Errorf("download: status %d body %q, want the bytes the provider opened", r.Status, r.Body)
	}
	if got := f.h.Count(t, `SELECT count(*) FROM communications.attachment_cleanup_records WHERE status = 'owned'`); got != 1 {
		t.Errorf("owned objects = %d, want 1", got)
	}
}

// TestMessengerSync_ARolledBackImportLeavesACleanupRecord: an event whose
// transaction Postgres refuses — a NUL in its text — is given up on with a
// failed receipt, the cursor going past it, and the object stored for it is
// queued for deletion, never owned.
func TestMessengerSync_ARolledBackImportLeavesACleanupRecord(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	f.fake.AddFile("ref-1", "photo.png", "image/png", []byte("PNGBYTES"))
	cursor := f.fake.Push(f.channel.Address, contracts.MessengerEvent{
		Kind: contracts.MessengerEventMessage, Participant: "4242", At: f.h.Now(), PlatformMessageID: "bad", Text: "a\x00b",
		Attachments: []contracts.MessengerAttachmentRef{{Ref: "ref-1", FileName: "photo.png", ContentType: "image/png"}},
	})
	f.text("4242", "good", "Fine", time.Second)
	f.sync(t)

	if got := modtest.One[string](t, f.h, `SELECT outcome || '/' || coalesce(reason, '-') FROM communications.inbound_receipts WHERE channel_id = $1 AND provider_message_id = 'bad'`, f.channel.Id); got != "failed/import_failed" {
		t.Errorf("receipt = %q, want failed/import_failed", got)
	}
	if got := f.messageCount(t); got != 1 {
		t.Errorf("messages = %d, want only the good one", got)
	}
	if got := f.h.Count(t, `SELECT count(*) FROM communications.attachment_cleanup_records WHERE status = 'pending'`); got != 1 {
		t.Errorf("pending cleanup records = %d, want the rolled-back import's object", got)
	}
	if got := f.h.Count(t, `SELECT count(*) FROM communications.attachment_cleanup_records WHERE status = 'owned'`); got != 0 {
		t.Errorf("owned objects = %d, want none", got)
	}
	if got := f.cursor(t); got <= cursor {
		t.Errorf("cursor = %d, want past the event given up on (%d)", got, cursor)
	}
}

// TestMessengerSync_ProfileIsCalledOutsideTheTransaction: Profile, like every
// call of the slot, goes through an accessor that refuses a call made inside one
// of the module's transactions. The harness fails every test whose calls were
// made inside one; this proves the guard itself trips.
func TestMessengerSync_ProfileIsCalledOutsideTheTransaction(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	f.text("4242", "m1", "Hello", 0)
	f.sync(t)
	if got := f.fake.CallCount("Profile"); got != 1 {
		t.Fatalf("Profile calls = %d, want 1", got)
	}
	// The guard: a call made with a context inside a transaction is refused.
	if err := communications.MessengerProfileInTxForTest(f.fake); !errors.Is(err, communications.ErrCallInsideTransactionForTest) {
		t.Errorf("a Profile call inside a transaction = %v, want errCallInsideTransaction", err)
	}
	if got := f.fake.CallCount("Profile"); got != 1 {
		t.Errorf("Profile calls = %d, want still 1: the refused call never reached the provider", got)
	}
}

// TestMessengerSync_Watermarks: delivered and read events move the deliveries
// accepted at or before their watermark, once each, and never backwards.
func TestMessengerSync_Watermarks(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	f.text("4242", "m1", "Hello", 0)
	f.sync(t)
	conv := f.conversations(t)[0]
	base := f.h.Now()
	_, early := f.outbound(t, conv, "First", "", base.Add(time.Minute))
	_, late := f.outbound(t, conv, "Second", "", base.Add(3*time.Minute))
	f.h.Advance(2 * time.Minute)

	push := func(kind string, watermark time.Time) {
		f.fake.Push(f.channel.Address, contracts.MessengerEvent{
			Kind: kind, Participant: "4242", At: watermark, Watermark: watermark,
		})
		f.h.Advance(2 * time.Minute)
		f.sync(t)
	}
	push(contracts.MessengerEventDelivered, base.Add(2*time.Minute))
	if got := [2]string{f.deliveryStatus(t, early), f.deliveryStatus(t, late)}; got != [2]string{"delivered", "relay_accepted"} {
		t.Errorf("after delivered = %v, want the first only", got)
	}
	push(contracts.MessengerEventRead, base.Add(2*time.Minute))
	if got := [2]string{f.deliveryStatus(t, early), f.deliveryStatus(t, late)}; got != [2]string{"read", "relay_accepted"} {
		t.Errorf("after read = %v, want the first read", got)
	}
	// A delivered watermark after read never moves it back; a read one reaches
	// the second, which was never delivered.
	push(contracts.MessengerEventDelivered, base.Add(10*time.Minute))
	if got := [2]string{f.deliveryStatus(t, early), f.deliveryStatus(t, late)}; got != [2]string{"read", "delivered"} {
		t.Errorf("after a later delivered = %v, want read and delivered", got)
	}
	push(contracts.MessengerEventRead, base.Add(10*time.Minute))
	if got := [2]string{f.deliveryStatus(t, early), f.deliveryStatus(t, late)}; got != [2]string{"read", "read"} {
		t.Errorf("after a later read = %v, want both read", got)
	}
	// Idempotent: the same watermark again writes no event.
	events := f.h.Count(t, `SELECT count(*) FROM communications.message_events WHERE event_type IN ('delivered', 'read')`)
	push(contracts.MessengerEventRead, base.Add(10*time.Minute))
	if got := f.h.Count(t, `SELECT count(*) FROM communications.message_events WHERE event_type IN ('delivered', 'read')`); got != events {
		t.Errorf("events = %d after a repeated watermark, want %d", got, events)
	}
	// Watermarks go through no receipt.
	if got := f.h.Count(t, `SELECT count(*) FROM communications.inbound_receipts WHERE channel_id = $1`, f.channel.Id); got != 1 {
		t.Errorf("receipts = %d, want the one message's", got)
	}
}

// TestMessengerSync_Echoes: an echo of a part Vantigo sent is ignored; one sent
// from elsewhere is imported as an outbound message marked "Sent from Meta"; an
// echo that matches no part while a part is in flight is held, not imported.
func TestMessengerSync_Echoes(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	f.text("4242", "m1", "Hello", 0)
	f.sync(t)
	conv := f.conversations(t)[0]
	sent, _ := f.outbound(t, conv, "Our reply", "", f.h.Now())
	f.h.Exec(t, `UPDATE communications.messenger_parts SET platform_message_id = 'm_ours' WHERE message_id = $1`, sent)

	echo := func(id, text string) {
		f.fake.Push(f.channel.Address, contracts.MessengerEvent{
			Kind: contracts.MessengerEventMessage, Participant: "4242", At: f.h.Now(), PlatformMessageID: id, Text: text, Echo: true,
		})
	}
	f.h.Advance(2 * time.Minute)
	echo("m_ours", "Our reply")
	f.sync(t)
	if got := f.h.Count(t, `SELECT count(*) FROM communications.conversation_messages WHERE conversation_id = $1`, conv); got != 2 {
		t.Errorf("messages = %d after our own echo, want 2: it is ignored", got)
	}
	if got := modtest.One[string](t, f.h, `SELECT outcome || '/' || reason FROM communications.inbound_receipts WHERE provider_message_id = 'm_ours'`); got != "skipped/own_message" {
		t.Errorf("receipt = %q, want skipped/own_message", got)
	}

	// An echo from Meta Business Suite is imported, in the participant's thread.
	f.h.Advance(2 * time.Minute)
	echo("m_suite", "Sent from the Business Suite")
	f.sync(t)
	d := f.detail(t, conv)
	if len(d.Messages) != 3 {
		t.Fatalf("messages = %d, want the foreign echo imported", len(d.Messages))
	}
	var found bool
	for _, m := range d.Messages {
		if m.TextBody != nil && *m.TextBody == "Sent from the Business Suite" {
			found = true
			if m.Direction != "outbound" || !m.Echo {
				t.Errorf("foreign echo = %+v, want outbound and marked echo", m)
			}
		}
	}
	if !found {
		t.Error("the foreign echo is not in the thread")
	}

	// A part in flight holds an echo that matches no part.
	pending := uuid.New()
	f.h.Exec(t, `
		INSERT INTO communications.conversation_messages (id, conversation_id, direction, text_body, occurred_at, created_at)
		VALUES ($1, $2, 'outbound', 'In flight', now(), now())`, pending, conv)
	f.h.Exec(t, `INSERT INTO communications.messenger_parts (message_id, part_index, attempted_at) VALUES ($1, 0, $2)`, pending, f.h.Now())
	f.h.Advance(2 * time.Minute)
	f.h.Exec(t, `UPDATE communications.messenger_parts SET attempted_at = $2 WHERE message_id = $1`, pending, f.h.Now())
	cursorBefore := f.cursor(t)
	echo("m_inflight", "Maybe ours")
	f.sync(t)
	if got := f.messageCount(t); got != 4 {
		t.Errorf("messages = %d, want the held echo not imported (3 and the in-flight one)", got)
	}
	if got := f.cursor(t); got != cursorBefore {
		t.Errorf("cursor = %d, want %d: the cycle stopped at the held echo", got, cursorBefore)
	}
	// The part is recorded as sent: its echo is ours, and the cursor goes on.
	f.h.Exec(t, `UPDATE communications.messenger_parts SET platform_message_id = 'm_inflight', sent_at = now() WHERE message_id = $1`, pending)
	f.h.Advance(time.Minute)
	f.sync(t)
	if got := f.messageCount(t); got != 4 {
		t.Errorf("messages = %d after the part was recorded, want 4: the echo is ours", got)
	}
	if got := f.cursor(t); got <= cursorBefore {
		t.Errorf("cursor = %d, want past the echo", got)
	}
}

// TestMessengerSync_Answers: a tapped answer option or card choice is placed under
// the question its payload names, with the title the person saw; one the server
// cannot place stays plain text; an e-mail address shared in answer to the quick
// reply that asks for one is placed under that question and offered to the
// contact linker as a suggestion, never a link.
func TestMessengerSync_Answers(t *testing.T) {
	t.Parallel()
	directory := contactDirectory{email: "kari@example.test", contact: 2001, customers: []int32{1001, 1002}}
	f := newChat(t, modtest.WithDirectory(directory))
	f.text("4242", "m1", "Hello", 0)
	f.sync(t)
	conv := f.conversations(t)[0]
	question, _ := f.outbound(t, conv, "Shall we?", `{"answerOptions":[{"id":"yes","title":"Yes"},{"id":"no","title":"No"}]}`, time.Time{})
	askEmail, _ := f.outbound(t, conv, "What is your e-mail?", `{"shareEmail":true}`, time.Time{})
	other := uuid.New() // a message of no conversation here

	answer := func(id, text string, a *contracts.MessengerAnswer) {
		f.h.Advance(2 * time.Minute)
		f.fake.Push(f.channel.Address, contracts.MessengerEvent{
			Kind: contracts.MessengerEventMessage, Participant: "4242", At: f.h.Now(), PlatformMessageID: id, Text: text, Answer: a,
		})
		f.sync(t)
	}
	answer("a1", "Yes", &contracts.MessengerAnswer{MessageID: question.String(), ChoiceID: "yes"})
	answer("a2", "Whatever", &contracts.MessengerAnswer{MessageID: other.String(), ChoiceID: "yes"})
	answer("a3", "Odd", &contracts.MessengerAnswer{MessageID: "not-a-uuid", ChoiceID: "yes"})
	answer("a5", "Maybe", &contracts.MessengerAnswer{MessageID: question.String(), ChoiceID: "maybe"})
	answer("a4", "kari@example.test", &contracts.MessengerAnswer{ChoiceID: contracts.MessengerShareEmail})

	d := f.detail(t, conv)
	byText := map[string]chatMessageJSON{}
	for _, m := range d.Messages {
		if m.TextBody != nil {
			byText[*m.TextBody] = m
		}
	}
	if a := byText["Yes"].AnswerTo; a == nil || a.MessageId != question.String() || a.ChoiceId != "yes" || a.Title != "Yes" {
		t.Errorf("answer to the question = %+v, want yes under it with the title the person saw", a)
	}
	if byText["Maybe"].AnswerTo != nil {
		t.Errorf("an answer naming a choice the question never offered is placed: %+v", byText["Maybe"].AnswerTo)
	}
	if byText["Whatever"].AnswerTo != nil || byText["Odd"].AnswerTo != nil {
		t.Errorf("an answer that cannot be placed stands as an answer: %+v %+v", byText["Whatever"].AnswerTo, byText["Odd"].AnswerTo)
	}
	if a := byText["kari@example.test"].AnswerTo; a == nil || a.MessageId != askEmail.String() || a.ChoiceId != "share-email" {
		t.Errorf("shared e-mail = %+v, want share-email under the question that asked", a)
	}
	if got := f.h.Count(t, `SELECT count(*) FROM communications.conversation_customer_candidates WHERE conversation_id = $1`, conv); got != 2 {
		t.Errorf("candidate customers = %d, want the two the address names", got)
	}
	if got := f.h.Count(t, `SELECT count(*) FROM communications.conversations WHERE id = $1 AND customer_id IS NULL`, conv); got != 1 {
		t.Error("the conversation was linked to a customer: an address is a suggestion")
	}
	if got := modtest.One[*int32](t, f.h, `SELECT contact_id FROM communications.participants WHERE channel_id = $1 AND address = '4242'`, f.channel.Id); got != nil {
		t.Errorf("participant contact = %d, want none: an address is a suggestion (N20)", *got)
	}
	if got := modtest.One[int32](t, f.h, `
		SELECT h.suggested_contact_id FROM communications.participant_contact_hints h
		JOIN communications.participants p ON p.id = h.participant_id
		WHERE p.channel_id = $1 AND p.address = '4242'`, f.channel.Id); got != 2001 {
		t.Errorf("suggested contact = %d, want the one contact with the address", got)
	}
}

// contactDirectory is the package's directory with one e-mail address known: to
// contact on customers.
type contactDirectory struct {
	fakeDirectory
	email     string
	contact   int32
	customers []int32
}

func (d contactDirectory) ContactsByEmail(_ context.Context, email string) ([]contracts.ContactMatch, error) {
	if strings.EqualFold(email, d.email) {
		return []contracts.ContactMatch{{ContactID: d.contact, CandidateCustomerIDs: d.customers}}, nil
	}
	return nil, nil
}

// TestMessengerSync_FailuresBackOffAndHold: a Pending or Ack that fails backs the
// channel off as the mailbox sync does, a provider that is unavailable holds it
// without spending an attempt, and a cycle whose Ack failed acknowledges on the
// next, with no new event.
func TestMessengerSync_FailuresBackOffAndHold(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	row := func() string {
		return modtest.One[string](t, f.h, `
			SELECT state || '/' || coalesce(last_error, '-') || '/' || attempts || '/' || (next_sync_at - $2::timestamptz)::text
			FROM communications.messenger_sync WHERE channel_id = $1`, f.channel.Id, f.h.Now())
	}
	f.text("4242", "m1", "Hello", 0)

	f.fake.FailWith("Pending", errors.New("boom"))
	f.sync(t)
	if got := row(); got != "active/sync_failed/1/00:01:00" {
		t.Errorf("after a failed Pending = %s, want a minute's backoff, one attempt", got)
	}
	f.fake.FailWith("Pending", nil)
	f.h.Advance(time.Minute)

	f.fake.FailWith("Ack", errors.New("boom"))
	f.sync(t)
	if got := f.messageCount(t); got != 1 {
		t.Errorf("messages = %d, want the event imported before its Ack failed", got)
	}
	if got := row(); got != "active/sync_failed/2/00:02:00" {
		t.Errorf("after a failed Ack = %s, want two minutes' backoff, two attempts", got)
	}
	f.fake.FailWith("Ack", nil)
	f.h.Advance(2 * time.Minute)
	f.sync(t)
	if got := f.fake.Acked(f.channel.Address); got != f.cursor(t) || got == 0 {
		t.Errorf("acked = %d, want the cursor %d: the next cycle acknowledges with no new event", got, f.cursor(t))
	}
	if got := row(); !strings.HasPrefix(got, "active/-/0/") {
		t.Errorf("after a good cycle = %s, want the failure forgotten", got)
	}

	// An unavailable provider holds the channel without spending an attempt.
	f.h.Advance(2 * time.Minute)
	f.fake.SetStatus(false, "not_configured")
	f.sync(t)
	if got := row(); got != "active/provider_unavailable/0/00:05:00" {
		t.Errorf("held = %s, want five minutes, no attempt spent", got)
	}
}

// TestMessengerSync_NoticesAndTheMinuteFallback: a Page notified is due at once,
// a channel with nothing new is due again every 60 seconds, and one not due is
// left alone.
func TestMessengerSync_NoticesAndTheMinuteFallback(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	f.sync(t) // the first cycle, due at creation
	calls := f.fake.CallCount("Pending")
	f.sync(t)
	if got := f.fake.CallCount("Pending"); got != calls {
		t.Errorf("Pending calls = %d, want %d: the channel is not due", got, calls)
	}

	f.h.Advance(5 * time.Second)
	f.text("4242", "m1", "Hello", 0)
	f.sync(t)
	if got := f.messageCount(t); got != 1 {
		t.Errorf("messages = %d, want the notice to make the channel due at once", got)
	}

	calls = f.fake.CallCount("Pending")
	f.h.Advance(59 * time.Second)
	f.sync(t)
	if got := f.fake.CallCount("Pending"); got != calls {
		t.Errorf("Pending calls = %d at 59 s, want %d", got, calls)
	}
	f.h.Advance(2 * time.Second)
	f.sync(t)
	if got := f.fake.CallCount("Pending"); got != calls+1 {
		t.Errorf("Pending calls = %d at 61 s, want %d: due every 60 s regardless", got, calls+1)
	}
}

// TestMessengerSync_SkipsAnInactiveChannel: a deactivated channel is not read.
func TestMessengerSync_SkipsAnInactiveChannel(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	f.text("4242", "m1", "Hello", 0)
	f.h.Exec(t, `UPDATE communications.channels SET is_active = false WHERE id = $1`, f.channel.Id)
	f.sync(t)
	if got := f.fake.CallCount("Pending"); got != 0 {
		t.Errorf("Pending calls = %d, want none for an inactive channel", got)
	}
}
