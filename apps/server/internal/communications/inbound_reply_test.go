package communications_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	netmail "net/mail"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is reply, reply-all and the AI context from a message the mailbox
// sync really imported (M15; delivery D, reading 20): the To the sender, the
// Cc reply-all's, the threading headers the outbox writes.

// TestReply_FromARealImport: the detail answers the sender as replyTo and the
// message's Cc — without the mailbox and the sender, distinct in any case, at
// most 100 — as replyAllCc; reply-all queues one To and one Cc delivery each,
// reply only the To; the reply's metadata carries In-Reply-To and the first
// and last 19 of its References, and the outbox's MIME carries both.
func TestReply_FromARealImport(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	mail := in.plainMail("Please help")
	sender := senderOf(mail)
	cc := []string{strings.ToUpper(in.address), strings.ToUpper(sender), "Dup@Example.test", "dup@example.test"}
	for i := range 105 {
		cc = append(cc, fmt.Sprintf("cc%03d@example.test", i))
	}
	mail.Cc = strings.Join(cc, ", ")
	refs := make([]string, 24)
	for i := range refs {
		refs[i] = fmt.Sprintf("<ref-%02d@example.test>", i)
	}
	mail.References = strings.Join(refs, " ")
	m := in.importOne(t, mail, "")

	detail := conversationDetail(t, in.h, m.conversationID)
	rr := detail["replyRecipients"].(map[string]any)
	if rr["canReply"] != true || rr["replyTo"] != sender || rr["canReplyAll"] != true {
		t.Fatalf("replyRecipients = %v, want the sender and reply-all", rr)
	}
	var ccAddresses []string
	for _, p := range rr["replyAllCc"].([]any) {
		ccAddresses = append(ccAddresses, p.(map[string]any)["address"].(string))
	}
	if len(ccAddresses) != 100 || ccAddresses[0] != "dup@example.test" || ccAddresses[1] != "cc000@example.test" ||
		ccAddresses[99] != "cc098@example.test" {
		t.Errorf("replyAllCc = %d addresses %v…, want 100: dup once, then cc000..cc098", len(ccAddresses), ccAddresses[:min(3, len(ccAddresses))])
	}
	for _, a := range ccAddresses {
		if strings.EqualFold(a, in.address) || strings.EqualFold(a, sender) {
			t.Errorf("replyAllCc holds %s, the mailbox or the sender", a)
		}
	}

	c := in.h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	body := replyBody()
	body["replyMode"] = "reply_all"
	r := doReply(c, m.conversationID.String(), uuid.NewString(), body)
	if r.Status != http.StatusCreated {
		t.Fatalf("reply-all: status %d body %s", r.Status, r.Body)
	}
	var queued conversationMutationJSON
	r.JSON(&queued)
	replyID := uuid.MustParse(*queued.MessageId)
	deliveries := readDeliveries(t, in.h, replyID)
	var to, ccs []string
	for _, d := range deliveries {
		if d.kind == "to" {
			to = append(to, d.address)
		} else {
			ccs = append(ccs, d.address)
		}
	}
	if !slices.Equal(to, []string{sender}) || len(ccs) != 100 {
		t.Errorf("deliveries: to %v, %d cc; want the sender and the 100", to, len(ccs))
	}

	var metadata struct {
		InReplyTo  string   `json:"inReplyTo"`
		References []string `json:"references"`
	}
	raw := modtest.One[[]byte](t, in.h, `SELECT channel_metadata_json FROM communications.conversation_messages WHERE id = $1`, replyID)
	if err := json.Unmarshal(raw, &metadata); err != nil {
		t.Fatal(err)
	}
	wantRefs := append([]string{refs[0]}, append(refs[6:], mail.MessageID)...)
	if metadata.InReplyTo != mail.MessageID || !slices.Equal(metadata.References, wantRefs) {
		t.Errorf("metadata = %+v, want In-Reply-To %s and the first and last 19 of 25 references %v", metadata, mail.MessageID, wantRefs)
	}

	// Reply alone answers the sender only.
	r = doReply(c, m.conversationID.String(), uuid.NewString(), replyBody())
	if r.Status != http.StatusCreated {
		t.Fatalf("reply: status %d body %s", r.Status, r.Body)
	}
	r.JSON(&queued)
	if d := readDeliveries(t, in.h, uuid.MustParse(*queued.MessageId)); len(d) != 1 || d[0].address != sender {
		t.Errorf("reply's deliveries = %+v, want the sender alone", d)
	}

	// The outbox sends the reply-all through the mailbox with its headers.
	w := communications.NewOutboxWorker(in.h.Deps())
	for range 2 {
		if _, err := w.ProcessOne(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	var sent []byte
	for _, s := range in.fake.Sent() {
		if bytes.Contains(s.MIME, []byte(expectedMessageID(replyID))) {
			sent = s.MIME
		}
	}
	if sent == nil {
		t.Fatal("the reply-all was not sent")
	}
	msg, err := netmail.ReadMessage(bytes.NewReader(sent))
	if err != nil {
		t.Fatal(err)
	}
	if got := msg.Header.Get("In-Reply-To"); got != mail.MessageID {
		t.Errorf("In-Reply-To = %q, want %q", got, mail.MessageID)
	}
	if got := strings.Fields(msg.Header.Get("References")); !slices.Equal(got, wantRefs) {
		t.Errorf("References = %v, want %v", got, wantRefs)
	}
}

// TestReply_ThreadReferences: distinct in any case, and the first and the
// last 19 once there are more than 20.
func TestReply_ThreadReferences(t *testing.T) {
	t.Parallel()
	if got := communications.ThreadReferencesForTest([]string{"<a@x>", "<A@X>", "<b@x>"}, "<b@X>"); !slices.Equal(got, []string{"<a@x>", "<b@x>"}) {
		t.Errorf("threadReferences = %v", got)
	}
	var refs []string
	for i := range 30 {
		refs = append(refs, fmt.Sprintf("<%d@x>", i))
	}
	got := communications.ThreadReferencesForTest(refs, "<last@x>")
	if len(got) != 20 || got[0] != "<0@x>" || got[1] != "<12@x>" || got[19] != "<last@x>" {
		t.Errorf("threadReferences = %v, want <0@x>, <12@x>..<29@x>, <last@x>", got)
	}
}

// TestAiDraft_ContextFromARealImport: the AI draft's context holds the text of
// a message the mailbox sync imported — derived from its HTML when it had no
// text.
func TestAiDraft_ContextFromARealImport(t *testing.T) {
	t.Parallel()
	tr := &fakeChatTransport{respond: respondWith(func() *http.Response {
		return completion(`{"subject":"S","text":"T"}`, 1, 1)
	})}
	fake := modtest.NewFakeChannelMailbox()
	in := newInbox(t, nil, fake,
		modtest.WithTransport(tr),
		modtest.WithEnv("COMMUNICATIONS_AI_ENABLED", "1"),
		modtest.WithEnv("COMMUNICATIONS_AI_API_KEY", "sk-test-key"),
		modtest.WithEnv("COMMUNICATIONS_AI_MODEL", "gpt-4o-mini"))
	mail := in.plainMail("")
	mail.HTML = "<p>The heat pump is <b>broken</b></p>"
	m := in.importOne(t, mail, "")
	if r := doDraft(draftClient(t, in.h), m.conversationID.String(), draftBody("concise", "Reply.")); r.Status != http.StatusOK {
		t.Fatalf("draft: status %d body %s", r.Status, r.Body)
	}
	if prompt := tr.lastPrompt(t); !strings.Contains(prompt, "The heat pump is broken") {
		t.Errorf("prompt does not hold the imported text:\n%s", prompt)
	}
}
