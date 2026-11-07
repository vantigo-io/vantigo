package communications_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is contacts on Messenger (Messenger design N19-N22, delivery D):
// linking a participant to a contact by hand, the suggestion from a shared
// e-mail address, the ref token of a contact's Messenger link and the referrals
// that bring it back, contracts.ContactChannels and a conversation started from
// a customer. Customers' directory is a fake that knows the contacts 2001 and
// 2002, of which only 2001 is one of customer 1001's.

// contactsDirectory is the package's directory with two contacts, 2001 and 2002,
// and one e-mail address known; customer 1001 has the contact 2001 alone.
type contactsDirectory struct {
	contactDirectory
}

func (contactsDirectory) Contact(_ context.Context, id int32) (*contracts.ContactEntry, error) {
	if id == 2001 || id == 2002 {
		return &contracts.ContactEntry{ID: id, FirstName: "Test", LastName: fmt.Sprintf("Contact %d", id)}, nil
	}
	return nil, nil
}

func (contactsDirectory) ContactsForCustomer(_ context.Context, customerID int32) ([]contracts.ContactEntry, error) {
	if customerID == 1001 {
		return []contracts.ContactEntry{{ID: 2001, FirstName: "Test", LastName: "Contact 2001"}}, nil
	}
	return nil, nil
}

func newLinkChat(t *testing.T) chatFixture {
	t.Helper()
	return newChat(t, modtest.WithDirectory(contactsDirectory{contactDirectory{email: "kari@example.test", contact: 2001, customers: []int32{1001}}}))
}

// participantOf is the id of the participant with the platform's id on the
// channel.
func participantOf(t *testing.T, f chatFixture, psid string) string {
	t.Helper()
	return modtest.One[string](t, f.h,
		`SELECT id::text FROM communications.participants WHERE channel_id = $1 AND address = $2`, f.channel.Id, psid)
}

func contactOf(t *testing.T, f chatFixture, participant string) *int32 {
	t.Helper()
	return modtest.One[*int32](t, f.h, `SELECT contact_id FROM communications.participants WHERE id = $1`, participant)
}

// messengerLinkJSON is the link block of a messenger conversation's detail.
type messengerLinkJSON struct {
	ParticipantId      string `json:"participantId"`
	ContactId          *int32 `json:"contactId"`
	SuggestedContactId *int32 `json:"suggestedContactId"`
	ReferralContactId  *int32 `json:"referralContactId"`
}

func linkOf(t *testing.T, f chatFixture, conversation string) *messengerLinkJSON {
	t.Helper()
	r := f.c.Do(http.MethodGet, "/api/v1/communications/conversations/"+conversation, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET conversation: status %d body %s", r.Status, r.Body)
	}
	var d struct {
		MessengerLink *messengerLinkJSON `json:"messengerLink"`
	}
	r.JSON(&d)
	return d.MessengerLink
}

func putContact(f chatFixture, participant string, body any) *modtest.Response {
	return f.c.Do(http.MethodPut, "/api/v1/communications/participants/"+participant+"/contact", body)
}

// TestParticipantContact_SetClearAndIdempotent: PUT links, again with the same
// contact is 200, another contact replaces it, DELETE clears and is 204 again;
// a contact that does not exist is 422 contact_unknown; a participant that does
// not exist is 404.
func TestParticipantContact_SetClearAndIdempotent(t *testing.T) {
	t.Parallel()
	f := newLinkChat(t)
	f.text("4242", "m1", "Hello", 0)
	f.sync(t)
	p := participantOf(t, f, "4242")

	for range 2 {
		if r := putContact(f, p, map[string]any{"contactId": 2001}); r.Status != http.StatusOK {
			t.Fatalf("PUT: status %d body %s, want 200", r.Status, r.Body)
		}
	}
	if got := contactOf(t, f, p); got == nil || *got != 2001 {
		t.Fatalf("contact = %v, want 2001", got)
	}
	if r := putContact(f, p, map[string]any{"contactId": 2002}); r.Status != http.StatusOK {
		t.Fatalf("PUT another: status %d body %s", r.Status, r.Body)
	}
	if got := contactOf(t, f, p); got == nil || *got != 2002 {
		t.Errorf("contact = %v, want 2002", got)
	}

	r := putContact(f, p, map[string]any{"contactId": 9999})
	if r.Status != http.StatusUnprocessableEntity || r.Code() != "contact_unknown" {
		t.Errorf("unknown contact: status %d code %q body %s, want 422 contact_unknown", r.Status, r.Code(), r.Body)
	}
	if r := putContact(f, uuid.NewString(), map[string]any{"contactId": 2001}); r.Status != http.StatusNotFound {
		t.Errorf("unknown participant: status %d, want 404", r.Status)
	}
	if r := putContact(f, p, map[string]any{}); r.Status != http.StatusBadRequest {
		t.Errorf("no contact: status %d, want 400", r.Status)
	}

	path := "/api/v1/communications/participants/" + p + "/contact"
	for range 2 {
		if r := f.c.Do(http.MethodDelete, path, nil); r.Status != http.StatusNoContent {
			t.Fatalf("DELETE: status %d body %s, want 204", r.Status, r.Body)
		}
	}
	if got := contactOf(t, f, p); got != nil {
		t.Errorf("contact = %d after DELETE, want none", *got)
	}
	if r := f.c.Do(http.MethodDelete, "/api/v1/communications/participants/"+uuid.NewString()+"/contact", nil); r.Status != http.StatusNotFound {
		t.Errorf("DELETE unknown participant: status %d, want 404", r.Status)
	}
}

// TestParticipantContact_NeedsManage: linking is communications:conversations-manage.
func TestParticipantContact_NeedsManage(t *testing.T) {
	t.Parallel()
	f := newLinkChat(t)
	f.text("4242", "m1", "Hello", 0)
	f.sync(t)
	p := participantOf(t, f, "4242")
	reader := f.h.SignIn(t, "communications:conversations-view", "communications:conversations-reply")
	r := reader.Do(http.MethodPut, "/api/v1/communications/participants/"+p+"/contact", map[string]any{"contactId": 2001})
	if r.Status != http.StatusForbidden {
		t.Errorf("PUT without manage: status %d, want 403", r.Status)
	}
	if r := reader.Do(http.MethodDelete, "/api/v1/communications/participants/"+p+"/contact", nil); r.Status != http.StatusForbidden {
		t.Errorf("DELETE without manage: status %d, want 403", r.Status)
	}
}

// TestParticipantContact_MustBeOneOfTheConversationsCustomer: when the
// conversation is linked to a customer, the contact has to be one of that
// customer's; otherwise any contact will do.
func TestParticipantContact_MustBeOneOfTheConversationsCustomer(t *testing.T) {
	t.Parallel()
	f := newLinkChat(t)
	f.text("4242", "m1", "Hello", 0)
	f.sync(t)
	conv := f.conversations(t)[0]
	p := participantOf(t, f, "4242")
	f.h.Exec(t, `UPDATE communications.conversations SET customer_id = 1001 WHERE id = $1`, conv)

	r := putContact(f, p, map[string]any{"contactId": 2002, "conversationId": conv})
	if r.Status != http.StatusUnprocessableEntity || r.Code() != "contact_not_of_customer" {
		t.Fatalf("another customer's contact: status %d code %q body %s, want 422 contact_not_of_customer", r.Status, r.Code(), r.Body)
	}
	if got := contactOf(t, f, p); got != nil {
		t.Errorf("contact = %d after a refusal, want none", *got)
	}
	if r := putContact(f, p, map[string]any{"contactId": 2001, "conversationId": conv}); r.Status != http.StatusOK {
		t.Errorf("the customer's contact: status %d body %s, want 200", r.Status, r.Body)
	}
	if r := putContact(f, p, map[string]any{"contactId": 2002}); r.Status != http.StatusOK {
		t.Errorf("without a conversation: status %d body %s, want 200", r.Status, r.Body)
	}
}

// TestMessengerSync_SharedEmailSuggestsAContactAndNeverLinksIt: the address a
// person shares names one contact, which the thread offers; nothing is linked
// until the agent confirms, and linking clears the suggestion.
func TestMessengerSync_SharedEmailSuggestsAContactAndNeverLinksIt(t *testing.T) {
	t.Parallel()
	f := newLinkChat(t)
	f.text("4242", "m1", "Hello", 0)
	f.sync(t)
	conv := f.conversations(t)[0]
	f.outbound(t, conv, "What is your e-mail?", `{"shareEmail":true}`, time.Time{})
	f.h.Advance(time.Minute)
	f.fake.Push(f.channel.Address, contracts.MessengerEvent{
		Kind: contracts.MessengerEventMessage, Participant: "4242", At: f.h.Now(), PlatformMessageID: "m2",
		Text: "kari@example.test", Answer: &contracts.MessengerAnswer{ChoiceID: contracts.MessengerShareEmail},
	})
	f.sync(t)

	p := participantOf(t, f, "4242")
	if got := contactOf(t, f, p); got != nil {
		t.Fatalf("participant contact = %d, want none: a shared address is a suggestion", *got)
	}
	link := linkOf(t, f, conv)
	if link == nil || link.ParticipantId != p || link.ContactId != nil || link.SuggestedContactId == nil || *link.SuggestedContactId != 2001 {
		t.Fatalf("link = %+v, want participant %s suggested contact 2001 and no contact", link, p)
	}

	if r := putContact(f, p, map[string]any{"contactId": 2001, "conversationId": conv}); r.Status != http.StatusOK {
		t.Fatalf("confirm: status %d body %s", r.Status, r.Body)
	}
	link = linkOf(t, f, conv)
	if link.ContactId == nil || *link.ContactId != 2001 || link.SuggestedContactId != nil {
		t.Errorf("link after confirm = %+v, want contact 2001 and no suggestion", link)
	}
}

// refOfLink makes a contact's Messenger link for the channel through the API and
// answers its URL and ref.
func refOfLink(t *testing.T, f chatFixture, channel channelJSON, contact int) (link, ref string) {
	t.Helper()
	r := f.c.Do(http.MethodPost, "/api/v1/communications/channels/"+channel.Id+"/contact-links", map[string]any{"contactId": contact})
	if r.Status != http.StatusOK {
		t.Fatalf("contact-links: status %d body %s, want 200", r.Status, r.Body)
	}
	var body struct {
		URL       string    `json:"url"`
		ExpiresAt time.Time `json:"expiresAt"`
	}
	r.JSON(&body)
	u, err := url.Parse(body.URL)
	if err != nil {
		t.Fatalf("link %q: %v", body.URL, err)
	}
	if want := f.h.Now().Add(90 * 24 * time.Hour); body.ExpiresAt.Sub(want).Abs() > time.Minute {
		t.Errorf("expiresAt = %s, want 90 days from now (%s)", body.ExpiresAt, want)
	}
	return body.URL, u.Query().Get("ref")
}

// TestContactLinks_TheLinkAndItsToken: the URL is m.me/<page>?ref=<token>, the
// token has the five parts and is URL-safe and short; a link is for a messenger
// channel and a contact that exists, by someone who may manage conversations.
func TestContactLinks_TheLinkAndItsToken(t *testing.T) {
	t.Parallel()
	f := newLinkChat(t)
	link, ref := refOfLink(t, f, f.channel, 2001)
	if !strings.HasPrefix(link, "https://m.me/"+f.channel.Address+"?ref=") {
		t.Errorf("link = %q, want https://m.me/%s?ref=…", link, f.channel.Address)
	}
	parts := strings.Split(ref, ".")
	if len(parts) != 5 || parts[0] != "v1" || parts[1] != "2001" {
		t.Errorf("ref = %q, want v1.<contact>.<channel>.<expiry>.<mac>", ref)
	}
	if len(ref) > 200 {
		t.Errorf("ref is %d characters, want far under Meta's 2083", len(ref))
	}
	if url.QueryEscape(ref) != ref {
		t.Errorf("ref %q is not URL-safe as it stands", ref)
	}
	if strings.Contains(ref, "kari") {
		t.Errorf("ref %q holds more than the contact's id", ref)
	}

	path := "/api/v1/communications/channels/" + f.channel.Id + "/contact-links"
	if r := f.c.Do(http.MethodPost, path, map[string]any{"contactId": 9999}); r.Status != http.StatusUnprocessableEntity || r.Code() != "contact_unknown" {
		t.Errorf("unknown contact: status %d code %q, want 422 contact_unknown", r.Status, r.Code())
	}
	email := setupChannel(t, f.h)
	r := f.c.Do(http.MethodPost, "/api/v1/communications/channels/"+email+"/contact-links", map[string]any{"contactId": 2001})
	if r.Status != http.StatusUnprocessableEntity || r.Code() != "not_supported_on_channel" {
		t.Errorf("e-mail channel: status %d code %q, want 422 not_supported_on_channel", r.Status, r.Code())
	}
	if r := f.c.Do(http.MethodPost, "/api/v1/communications/channels/"+uuid.NewString()+"/contact-links", map[string]any{"contactId": 2001}); r.Status != http.StatusNotFound {
		t.Errorf("unknown channel: status %d, want 404", r.Status)
	}
	reader := f.h.SignIn(t, "communications:conversations-view")
	if r := reader.Do(http.MethodPost, path, map[string]any{"contactId": 2001}); r.Status != http.StatusForbidden {
		t.Errorf("without manage: status %d, want 403", r.Status)
	}
}

// pushRef queues an event of the participant carrying ref.
func pushRef(f chatFixture, kind, psid, platformID, text, ref string) {
	f.h.Advance(time.Minute)
	f.fake.Push(f.channel.Address, contracts.MessengerEvent{
		Kind: kind, Participant: psid, At: f.h.Now(), PlatformMessageID: platformID, Text: text, Ref: ref,
	})
}

// TestMessengerSync_Referrals: each shape of a referral — an event of its own on
// a thread that exists, and a message that carries the ref, for a person new to
// the Page and one who is not — links a participant that has no contact; a
// participant that has another contact keeps it and the thread notes whose link
// was used.
func TestMessengerSync_Referrals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		push func(f chatFixture, ref string)
	}{
		{"a referral event on an existing thread", func(f chatFixture, ref string) {
			pushRef(f, contracts.MessengerEventReferral, "4242", "", "", ref)
		}},
		{"the first message of a new person", func(f chatFixture, ref string) {
			pushRef(f, contracts.MessengerEventMessage, "7777", "g1", "Get Started", ref)
		}},
		{"a message of a person who has written before", func(f chatFixture, ref string) {
			pushRef(f, contracts.MessengerEventMessage, "4242", "g2", "Hi again", ref)
		}},
	}
	for _, tc := range cases {
		psid := "4242"
		if strings.Contains(tc.name, "new person") {
			psid = "7777"
		}
		t.Run(tc.name+"/no contact", func(t *testing.T) {
			t.Parallel()
			f := newLinkChat(t)
			f.text("4242", "m1", "Hello", 0)
			f.sync(t)
			_, ref := refOfLink(t, f, f.channel, 2001)
			tc.push(f, ref)
			f.sync(t)
			got := contactOf(t, f, participantOf(t, f, psid))
			if got == nil || *got != 2001 {
				t.Fatalf("contact = %v, want 2001", got)
			}
			if f.h.Count(t, `SELECT count(*) FROM communications.participant_contact_hints`) != 0 {
				t.Error("a hint row is left after a clean link")
			}
		})
		t.Run(tc.name+"/another contact", func(t *testing.T) {
			t.Parallel()
			f := newLinkChat(t)
			f.text("4242", "m1", "Hello", 0)
			if psid != "4242" {
				f.text("7777", "m0", "Earlier", 0)
			}
			f.sync(t)
			p := participantOf(t, f, psid)
			if r := putContact(f, p, map[string]any{"contactId": 2002}); r.Status != http.StatusOK {
				t.Fatalf("link by hand: %d %s", r.Status, r.Body)
			}
			_, ref := refOfLink(t, f, f.channel, 2001)
			tc.push(f, ref)
			f.sync(t)
			if got := contactOf(t, f, p); got == nil || *got != 2002 {
				t.Fatalf("contact = %v, want 2002 left alone", got)
			}
			conv := modtest.One[string](t, f.h, `
				SELECT c.id::text FROM communications.conversations c
				JOIN communications.conversation_participants cp ON cp.conversation_id = c.id
				WHERE cp.participant_id = $1 ORDER BY c.created_at DESC LIMIT 1`, p)
			link := linkOf(t, f, conv)
			if link.ReferralContactId == nil || *link.ReferralContactId != 2001 || link.ContactId == nil || *link.ContactId != 2002 {
				t.Errorf("link = %+v, want contact 2002 with the referral notice of 2001", link)
			}
			// Unlinking, or linking again, clears the notice.
			if r := putContact(f, p, map[string]any{"contactId": 2001}); r.Status != http.StatusOK {
				t.Fatalf("link again: %d", r.Status)
			}
			if link := linkOf(t, f, conv); link.ReferralContactId != nil {
				t.Errorf("referral notice = %d after a link by hand, want none", *link.ReferralContactId)
			}
		})
	}
}

// TestMessengerSync_BadRefsAreIgnored: a ref that fails its MAC, has expired,
// names another channel or is not ours links nobody, and the message is
// imported all the same.
func TestMessengerSync_BadRefsAreIgnored(t *testing.T) {
	t.Parallel()
	f := newLinkChat(t)
	other := newMessengerChannel(t, f.c, f.fake)
	f.text("4242", "m1", "Hello", 0)
	f.sync(t)
	p := participantOf(t, f, "4242")

	_, good := refOfLink(t, f, f.channel, 2001)
	_, forOther := refOfLink(t, f, other, 2001)
	tampered := good[:len(good)-1] + map[bool]string{true: "B", false: "A"}[good[len(good)-1] == 'A']
	forged := strings.Replace(good, "v1.2001.", "v1.2002.", 1)
	for name, ref := range map[string]string{
		"a bad MAC": tampered, "another contact's number": forged, "another channel's": forOther,
		"not ours": "campaign-spring", "empty parts": "v1....", "too many parts": good + ".x",
	} {
		pushRef(f, contracts.MessengerEventMessage, "4242", "b-"+strings.ReplaceAll(name, " ", "-"), "text "+name, ref)
	}
	f.sync(t)
	if got := contactOf(t, f, p); got != nil {
		t.Errorf("contact = %d after bad refs, want none", *got)
	}
	if got := f.h.Count(t, `SELECT count(*) FROM communications.conversation_messages WHERE direction = 'inbound' AND text_body LIKE 'text %'`); got != 6 {
		t.Errorf("messages with a bad ref = %d, want 6 imported all the same", got)
	}

	// Expired: the token is good for 90 days.
	f.h.Advance(91 * 24 * time.Hour)
	f.resign(t)
	pushRef(f, contracts.MessengerEventMessage, "4242", "late", "Late", good)
	f.sync(t)
	if got := contactOf(t, f, p); got != nil {
		t.Errorf("contact = %d after an expired ref, want none", *got)
	}
}

// TestContactChannels_OneEntryPerChannelAndNoPSID: every active Messenger channel
// is an entry of the contact, linked where a participant is, with its newest
// message and the window the provider's Capabilities give; an e-mail channel is
// none; the entry holds nothing of the person's platform id.
func TestContactChannels_OneEntryPerChannelAndNoPSID(t *testing.T) {
	t.Parallel()
	f := newLinkChat(t)
	second := newMessengerChannel(t, f.c, f.fake)
	setupChannel(t, f.h)
	f.text("4242", "m1", "Hello", 0)
	f.sync(t)
	p := participantOf(t, f, "4242")
	if r := putContact(f, p, map[string]any{"contactId": 2001}); r.Status != http.StatusOK {
		t.Fatalf("link: %d", r.Status)
	}
	f.h.Advance(2 * time.Hour)

	slot := communications.Module().ContactChannels(f.h.Deps())
	got, err := slot.ForContacts(context.Background(), []int32{2001, 2002})
	if err != nil {
		t.Fatalf("ForContacts: %v", err)
	}
	if len(got[2001]) != 2 || len(got[2002]) != 2 {
		t.Fatalf("entries = %+v, want one per active messenger channel for each contact", got)
	}
	byChannel := map[string]contracts.ContactChannelEntry{}
	for _, e := range got[2001] {
		byChannel[e.ChannelID.String()] = e
	}
	linked := byChannel[f.channel.Id]
	if !linked.Linked || linked.ParticipantID == nil || linked.ParticipantID.String() != p || linked.Type != "messenger" {
		t.Errorf("linked entry = %+v, want linked to participant %s", linked, p)
	}
	if linked.LastInboundAt == nil || linked.ReplyWindowClosesAt == nil || !linked.ReplyWindowClosesAt.After(f.h.Now()) {
		t.Errorf("linked entry = %+v, want the newest message and the window still open", linked)
	}
	if e := byChannel[second.Id]; e.Linked || e.ParticipantID != nil || e.LastInboundAt != nil || e.ReplyWindowClosesAt != nil {
		t.Errorf("entry of the other channel = %+v, want not linked", e)
	}
	for _, e := range got[2002] {
		if e.Linked {
			t.Errorf("contact 2002 has a linked entry %+v", e)
		}
	}
	if s := fmt.Sprintf("%+v", got); strings.Contains(s, "4242") {
		t.Errorf("entries %s carry the person's platform id", s)
	}

	// The window is the provider's: after 25 hours it is closed, and the entry
	// still says when the person last wrote.
	f.h.Advance(23 * time.Hour)
	got, err = slot.ForContacts(context.Background(), []int32{2001})
	if err != nil {
		t.Fatalf("ForContacts: %v", err)
	}
	for _, e := range got[2001] {
		if e.ChannelID.String() == f.channel.Id && (e.ReplyWindowClosesAt != nil || e.LastInboundAt == nil) {
			t.Errorf("entry after the window = %+v, want it closed with the last message kept", e)
		}
	}
	if empty, _ := slot.ForContacts(context.Background(), nil); len(empty) != 0 {
		t.Errorf("no ids answered %+v, want none", empty)
	}
}

func createFromParticipant(f chatFixture, body map[string]any, key string) *modtest.Response {
	return f.c.Do(http.MethodPost, "/api/v1/communications/conversations", body, modtest.Header("Idempotency-Key", key))
}

// TestCreateConversation_FromAParticipant: a participant whose window is open
// starts a conversation or reuses the open one, with its first message queued as
// a reply is; a closed window is 422 reply_window_closed and leaves nothing; a
// request without a participant is still channel_cannot_start; the same key again
// is answered again.
func TestCreateConversation_FromAParticipant(t *testing.T) {
	t.Parallel()
	f := newLinkChat(t)
	f.text("4242", "m1", "Hello", 0)
	f.sync(t)
	p := participantOf(t, f, "4242")
	open := f.conversations(t)[0]

	key := uuid.NewString()
	body := map[string]any{"participantId": p, "textBody": "Hi from the customer page"}
	r := createFromParticipant(f, body, key)
	if r.Status != http.StatusCreated {
		t.Fatalf("create: status %d body %s, want 201", r.Status, r.Body)
	}
	var made struct {
		ConversationId string `json:"conversationId"`
		MessageId      string `json:"messageId"`
	}
	r.JSON(&made)
	if made.ConversationId != open {
		t.Errorf("conversation = %s, want the open one %s reused", made.ConversationId, open)
	}
	if got := f.h.Count(t, `SELECT count(*) FROM communications.conversation_messages WHERE id = $1 AND direction = 'outbound' AND text_body = 'Hi from the customer page'`, made.MessageId); got != 1 {
		t.Errorf("queued messages = %d, want the first message", got)
	}
	if got := f.h.Count(t, `SELECT count(*) FROM communications.outbox_jobs WHERE message_id = $1`, made.MessageId); got != 1 {
		t.Errorf("outbox jobs = %d, want the message queued", got)
	}
	if r := createFromParticipant(f, body, key); r.Status != http.StatusOK {
		t.Errorf("same key again: status %d body %s, want 200", r.Status, r.Body)
	}
	if r := createFromParticipant(f, map[string]any{"participantId": p, "textBody": "Other"}, key); r.Status != http.StatusConflict {
		t.Errorf("same key, other payload: status %d, want 409", r.Status)
	}

	// A closed conversation is never joined: a new one starts while the window holds.
	f.h.Exec(t, `UPDATE communications.conversations SET status = 'closed' WHERE id = $1`, open)
	r = createFromParticipant(f, map[string]any{"participantId": p, "textBody": "Second"}, uuid.NewString())
	if r.Status != http.StatusCreated {
		t.Fatalf("create after close: status %d body %s", r.Status, r.Body)
	}
	r.JSON(&made)
	if made.ConversationId == open {
		t.Error("the closed conversation was joined")
	}
	if got := f.h.Count(t, `SELECT count(*) FROM communications.conversations WHERE channel_id = $1 AND subject IS NULL`, f.channel.Id); got != 2 {
		t.Errorf("conversations = %d, want 2 without a subject", got)
	}

	// Without a participant, a messenger channel still cannot be started.
	r = createFromParticipant(f, map[string]any{"channelId": f.channel.Id, "subject": "x", "textBody": "x", "to": []map[string]any{{"email": "a@example.test"}}}, uuid.NewString())
	if r.Status != http.StatusUnprocessableEntity || r.Code() != "channel_cannot_start" {
		t.Errorf("no participant: status %d code %q, want 422 channel_cannot_start", r.Status, r.Code())
	}
	if r := createFromParticipant(f, map[string]any{"participantId": uuid.NewString(), "textBody": "x"}, uuid.NewString()); r.Status != http.StatusUnprocessableEntity || r.Code() != "participant_invalid" {
		t.Errorf("unknown participant: status %d code %q, want 422 participant_invalid", r.Status, r.Code())
	}
	if r := createFromParticipant(f, map[string]any{"participantId": p}, uuid.NewString()); r.Status != http.StatusBadRequest {
		t.Errorf("no text: status %d, want 400", r.Status)
	}
}

// TestCreateConversation_FromAParticipantWithAClosedWindow: 25 hours after the
// person's last message the Page may not write first, and nothing is made.
func TestCreateConversation_FromAParticipantWithAClosedWindow(t *testing.T) {
	t.Parallel()
	f := newLinkChat(t)
	f.text("4242", "m1", "Hello", 0)
	f.sync(t)
	p := participantOf(t, f, "4242")
	f.h.Exec(t, `UPDATE communications.conversations SET status = 'closed'`)
	before := f.h.Count(t, `SELECT count(*) FROM communications.conversations`)
	f.h.Advance(25 * time.Hour)
	f.resign(t)

	r := createFromParticipant(f, map[string]any{"participantId": p, "textBody": "Hello?"}, uuid.NewString())
	if r.Status != http.StatusUnprocessableEntity || r.Code() != "reply_window_closed" {
		t.Fatalf("closed window: status %d code %q body %s, want 422 reply_window_closed", r.Status, r.Code(), r.Body)
	}
	if got := f.h.Count(t, `SELECT count(*) FROM communications.conversations`); got != before {
		t.Errorf("conversations = %d, want %d: a refused request makes none", got, before)
	}
}

// TestCreateConversation_FromAParticipantTakesBackAConversationItCouldNotStart:
// a text over the limit is refused by the reply, and the conversation made for
// it is taken back.
func TestCreateConversation_FromAParticipantTakesBackAConversationItCouldNotStart(t *testing.T) {
	t.Parallel()
	f := newLinkChat(t)
	f.text("4242", "m1", "Hello", 0)
	f.sync(t)
	p := participantOf(t, f, "4242")
	f.h.Exec(t, `UPDATE communications.conversations SET status = 'closed'`)
	before := f.h.Count(t, `SELECT count(*) FROM communications.conversations`)

	r := createFromParticipant(f, map[string]any{"participantId": p, "textBody": strings.Repeat("x", 2001)}, uuid.NewString())
	if r.Status != http.StatusUnprocessableEntity || r.Code() != "text_too_long" {
		t.Fatalf("long text: status %d code %q body %s, want 422 text_too_long", r.Status, r.Code(), r.Body)
	}
	if got := f.h.Count(t, `SELECT count(*) FROM communications.conversations`); got != before {
		t.Errorf("conversations = %d, want %d: the one made for the refused text is taken back", got, before)
	}
}

// TestCustomerReferences_LeaveAParticipantsContactInPlace: a customer merge moves
// a conversation's customer and leaves the contact a participant is linked to
// where it is - contacts are shared between customers and keep their ids when
// customers merge, so no repoint of contact_id exists (Messenger design N19).
func TestCustomerReferences_LeaveAParticipantsContactInPlace(t *testing.T) {
	t.Parallel()
	f := newLinkChat(t)
	f.text("4242", "m1", "Hello", 0)
	f.sync(t)
	p := participantOf(t, f, "4242")
	conv := f.conversations(t)[0]
	f.h.Exec(t, `UPDATE communications.conversations SET customer_id = 1001 WHERE id = $1`, conv)
	if r := putContact(f, p, map[string]any{"contactId": 2001}); r.Status != http.StatusOK {
		t.Fatalf("link: %d", r.Status)
	}

	repointCommunicationsCustomer(t, f.h, 1001, 1002, true)

	if got := contactOf(t, f, p); got == nil || *got != 2001 {
		t.Errorf("contact after the merge = %v, want 2001", got)
	}
	if got := f.h.Count(t, `SELECT count(*) FROM communications.conversations WHERE id = $1 AND customer_id = 1002`, conv); got != 1 {
		t.Error("the conversation's customer was not re-pointed")
	}
}

// TestContactHints_GoWithTheirParticipant: the hint row is the participant's, and
// the participant's removal removes it.
func TestContactHints_GoWithTheirParticipant(t *testing.T) {
	t.Parallel()
	f := newLinkChat(t)
	f.text("4242", "m1", "Hello", 0)
	f.sync(t)
	p := participantOf(t, f, "4242")
	f.h.Exec(t, `INSERT INTO communications.participant_contact_hints (participant_id, suggested_contact_id, updated_at) VALUES ($1, 2001, now())`, p)
	f.h.Exec(t, `DELETE FROM communications.conversation_participants WHERE participant_id = $1`, p)
	f.h.Exec(t, `DELETE FROM communications.participants WHERE id = $1`, p)
	if got := f.h.Count(t, `SELECT count(*) FROM communications.participant_contact_hints WHERE participant_id = $1`, p); got != 0 {
		t.Errorf("hint rows = %d after the participant went, want 0", got)
	}
}
