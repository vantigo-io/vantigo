package communications_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is Communications with no provider named (Google Workspace
// design G3, G4, G5): two ChannelMailbox fakes side by side — the listing,
// a channel served only by its own provider, the switch between two refused,
// one mailbox per provider —, personal sending choosing among two
// PersonalMailbox fakes and sending through the one it recorded, and what a
// provider answers of a sent message threading a reply.

// twoMailboxes is a harness with two mail channels providers, entra first
// and google second.
func twoMailboxes(t *testing.T) (*modtest.Harness, *modtest.FakeChannelMailbox, *modtest.FakeChannelMailbox) {
	t.Helper()
	entra := modtest.NewFakeChannelMailbox()
	google := modtest.NewFakeChannelMailbox().WithKey("google")
	h := newHarness(t, modtest.WithChannelMailbox(entra), modtest.WithChannelMailbox(google))
	return h, entra, google
}

// mailboxChannelBody is a CreateChannelRequest for a channel on provider.
func mailboxChannelBody(provider, address string) map[string]any {
	return map[string]any{"type": "email", "provider": provider, "address": address}
}

// methodsOf is the methods fake was called with, in order.
func methodsOf(fake *modtest.FakeChannelMailbox) []string {
	var out []string
	for _, c := range fake.Calls() {
		out = append(out, c.Method)
	}
	return out
}

// TestChannelProviders_ListsEveryProvider: SMTP, then every filled provider
// in slot order, each with its own name and status.
func TestChannelProviders_ListsEveryProvider(t *testing.T) {
	t.Parallel()
	h, _, google := twoMailboxes(t)
	google.SetStatus(contracts.MailboxUnavailable, "switch_off")
	got := channelProviders(t, h.SignIn(t, "communications:channels-manage"))
	if len(got) != 3 {
		t.Fatalf("providers = %+v, want smtp, entra and google", got)
	}
	for i, want := range []struct {
		key, name string
		available bool
	}{{"smtp", "SMTP", true}, {"entra", "Microsoft 365", true}, {"google", "google", false}} {
		if got[i].Key != want.key || got[i].DisplayName != want.name || got[i].Available != want.available {
			t.Errorf("providers[%d] = %+v, want %s %s available %v", i, got[i], want.key, want.name, want.available)
		}
	}
	if got[2].Reason == nil || *got[2].Reason != "switch_off" {
		t.Errorf("google's reason = %v, want switch_off", got[2].Reason)
	}
}

// TestMailboxChannel_ServedOnlyByItsOwnProvider: a channel on google is
// checked by google alone, follows google's status and never entra's, and
// its refusals name google.
func TestMailboxChannel_ServedOnlyByItsOwnProvider(t *testing.T) {
	t.Parallel()
	h, entra, google := twoMailboxes(t)
	c := h.SignIn(t, "communications:channels-manage")

	address := channelAddress(t)
	sharedMailbox(google, address, "")
	ch := createChannel(t, c, mailboxChannelBody("google", address))
	if ch.Provider != "google" || ch.Mailbox == nil {
		t.Fatalf("channel = %+v, want google with its mailbox", ch)
	}
	if calls := methodsOf(entra); len(calls) != 0 {
		t.Errorf("entra was called %v for a google channel, want never", calls)
	}

	read := func() gen.ChannelResponse {
		r := c.Do(http.MethodGet, "/api/v1/communications/channels/"+ch.Id, nil)
		var got gen.ChannelResponse
		r.JSON(&got)
		return got
	}
	entra.SetStatus(contracts.MailboxUnavailable, "switch_off")
	if got := read(); got.Available == nil || !*got.Available {
		t.Errorf("available with entra off = %v, want true: google serves it", got.Available)
	}
	entra.SetStatus(contracts.MailboxAvailable, "")
	google.SetStatus(contracts.MailboxUnavailable, "switch_off")
	if got := read(); got.Available == nil || *got.Available {
		t.Errorf("available with google off = %v, want false", got.Available)
	}

	r := c.Do(http.MethodPost, "/api/v1/communications/channels", mailboxChannelBody("google", channelAddress(t)))
	wantCode(t, r, http.StatusConflict, "mail_channels_off")
	var e commErrorJSON
	r.JSON(&e)
	if e.Error.Message != "Mail channels through google are not available. An administrator turns them on under Integrations." {
		t.Errorf("message = %q, want google named", e.Error.Message)
	}

	google.SetStatus(contracts.MailboxAvailable, "")
	r = c.Do(http.MethodPost, "/api/v1/communications/channels", mailboxChannelBody("google", channelAddress(t)))
	wantCode(t, r, http.StatusUnprocessableEntity, "mailbox_unknown")
	r.JSON(&e)
	if e.Error.Message != "No mailbox with this address was found in google." {
		t.Errorf("message = %q, want google named", e.Error.Message)
	}
	r = c.Do(http.MethodPost, "/api/v1/communications/channels", entraChannelBody(channelAddress(t)))
	wantCode(t, r, http.StatusUnprocessableEntity, "mailbox_unknown")
	r.JSON(&e)
	if e.Error.Message != "No mailbox with this address was found in Microsoft 365." {
		t.Errorf("message = %q, want Microsoft 365 named", e.Error.Message)
	}
}

// TestMailboxChannel_GroupAddress: a provider answering ErrMailboxGroup is
// group_address, 422, on create, the switch and verify.
func TestMailboxChannel_GroupAddress(t *testing.T) {
	t.Parallel()
	h, _, google := twoMailboxes(t)
	c := h.SignIn(t, "communications:channels-manage")
	google.FailWith("Check", contracts.ErrMailboxGroup)
	r := c.Do(http.MethodPost, "/api/v1/communications/channels", mailboxChannelBody("google", channelAddress(t)))
	wantCode(t, r, http.StatusUnprocessableEntity, "group_address")
	var e commErrorJSON
	r.JSON(&e)
	if e.Error.Message != "The address is a group. A group has no mailbox Vantigo can read; use a licensed mailbox." {
		t.Errorf("message = %q", e.Error.Message)
	}
	smtp := createChannel(t, c, newChannelBody(channelAddress(t)))
	wantCode(t, c.Do(http.MethodPost, "/api/v1/communications/channels/"+smtp.Id+"/provider",
		map[string]any{"provider": "google"}), http.StatusUnprocessableEntity, "group_address")
}

// TestSwitchChannelProvider_BetweenTwoMailboxProviders: an SMTP channel
// switches to google; to google again is provider_unchanged naming it; to
// entra is provider_switch_unsupported, 409 — back to SMTP first — and the
// channel stays on google; an unlisted key is a 400.
func TestSwitchChannelProvider_BetweenTwoMailboxProviders(t *testing.T) {
	t.Parallel()
	h, entra, google := twoMailboxes(t)
	c := h.SignIn(t, "communications:channels-manage")
	ch := createChannel(t, c, newChannelBody(channelAddress(t)))
	sharedMailbox(google, ch.Address, "")
	sharedMailbox(entra, ch.Address, "")

	switchTo := func(provider string) *modtest.Response {
		return c.Do(http.MethodPost, "/api/v1/communications/channels/"+ch.Id+"/provider", map[string]any{"provider": provider})
	}
	if r := switchTo("google"); r.Status != http.StatusOK {
		t.Fatalf("switch to google: %d %s", r.Status, r.Body)
	}
	r := switchTo("google")
	wantCode(t, r, http.StatusUnprocessableEntity, "provider_unchanged")
	var e commErrorJSON
	r.JSON(&e)
	if e.Error.Message != "The channel already uses google." {
		t.Errorf("message = %q", e.Error.Message)
	}
	wantCode(t, switchTo("entra"), http.StatusConflict, "provider_switch_unsupported")
	wantCode(t, switchTo("yahoo"), http.StatusConflict, "provider_switch_unsupported")
	if p := modtest.One[string](t, h, `SELECT provider FROM communications.channels WHERE id = $1`, ch.Id); p != "google" {
		t.Errorf("provider = %q, want google", p)
	}
	for _, call := range entra.Calls() {
		if call.Method == "Check" {
			t.Errorf("entra was asked to Check for the refused switch")
		}
	}

	// A PUT naming the other mailbox provider stays refused.
	r = c.Do(http.MethodPut, "/api/v1/communications/channels/"+ch.Id, map[string]any{"provider": "entra"})
	wantCode(t, r, http.StatusBadRequest, "invalid_request")
	r.JSON(&e)
	if len(e.field("provider")) != 1 {
		t.Errorf("fields = %v, want one on provider", e.Error.Fields)
	}
}

// TestMailboxChannel_OneChannelPerMailboxPerProvider: one mailbox id under
// two providers is two mailboxes; twice under one is mailbox_exists.
func TestMailboxChannel_OneChannelPerMailboxPerProvider(t *testing.T) {
	t.Parallel()
	h, entra, google := twoMailboxes(t)
	c := h.SignIn(t, "communications:channels-manage")
	mailbox := uuid.NewString()
	shared := func(fake *modtest.FakeChannelMailbox) string {
		address := channelAddress(t)
		fake.SetMailbox(address, contracts.ChannelMailboxInfo{Mailbox: mailbox, Address: address, Shared: true})
		return address
	}
	createChannel(t, c, entraChannelBody(shared(entra)))
	createChannel(t, c, mailboxChannelBody("google", shared(google)))
	wantCode(t, c.Do(http.MethodPost, "/api/v1/communications/channels", mailboxChannelBody("google", shared(google))),
		http.StatusConflict, "mailbox_exists")
	if n := h.Count(t, `SELECT count(*) FROM communications.channels WHERE settings->>'mailbox' = $1`, mailbox); n != 2 {
		t.Errorf("channels on the mailbox = %d, want 2", n)
	}
}

// twoPersonalMailboxes is a harness with two personal mailbox providers,
// entra first and google second, and an SMTP channel.
func twoPersonalMailboxes(t *testing.T) (*modtest.Harness, *modtest.FakePersonalMailbox, *modtest.FakePersonalMailbox, string) {
	t.Helper()
	entra := modtest.NewFakePersonalMailbox()
	google := modtest.NewFakePersonalMailbox().WithKey("google")
	h := newHarness(t, modtest.WithPersonalMailbox(entra), modtest.WithPersonalMailbox(google))
	return h, entra, google, setupChannel(t, h)
}

// personalSender reads GET /personal-sender for c.
func personalSender(t *testing.T, c *modtest.Client) gen.PersonalSenderResponse {
	t.Helper()
	r := c.Do(http.MethodGet, "/api/v1/communications/personal-sender", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("personal-sender: %d %s", r.Status, r.Body)
	}
	var resp gen.PersonalSenderResponse
	r.JSON(&resp)
	return resp
}

// TestPersonalSending_ChoosesAmongProviders: the first Available answer,
// else the first that is not no_account, else no_account, each named by its
// provider; unavailable with none (TestPersonalSender_GetEndpoint).
func TestPersonalSending_ChoosesAmongProviders(t *testing.T) {
	t.Parallel()
	describe := func(r gen.PersonalSenderResponse) string {
		s := func(p *string) string {
			if p == nil {
				return "-"
			}
			return *p
		}
		if r.Available {
			return "available " + s(r.ProviderDisplayName) + " " + s(r.Address)
		}
		return s(r.Reason) + " " + s(r.ProviderDisplayName)
	}
	cases := []struct {
		name          string
		entra, google *contracts.PersonalMailStatus
		want          string
	}{
		{"neither knows the caller", nil, nil, "no_account Outlook"},
		{"google can send", nil, &contracts.PersonalMailStatus{Available: true, Address: "kari@example.no", Mailbox: "g"}, "available google kari@example.no"},
		{"both can send: the first", &contracts.PersonalMailStatus{Available: true, Address: "kari@example.com", Mailbox: "e"},
			&contracts.PersonalMailStatus{Available: true, Address: "kari@example.no", Mailbox: "g"}, "available Outlook kari@example.com"},
		{"google knows the caller, not connected", nil, &contracts.PersonalMailStatus{Reason: "not_connected"}, "not_connected google"},
		{"entra must reconnect, google can send", &contracts.PersonalMailStatus{Reason: "reconnect", ErrorCode: "expired"},
			&contracts.PersonalMailStatus{Available: true, Address: "kari@example.no", Mailbox: "g"}, "available google kari@example.no"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h, entra, google, _ := twoPersonalMailboxes(t)
			c, user := h.SignInUser(t, "communications:conversations-view")
			if tc.entra != nil {
				entra.SetUser(user, *tc.entra)
			}
			if tc.google != nil {
				google.SetUser(user, *tc.google)
			}
			if got := describe(personalSender(t, c)); got != tc.want {
				t.Errorf("personal sender = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPersonalSending_RecordsAndSendsThroughItsProvider: a message sent
// from the caller's address records the provider that could send, the
// outbox sends through it alone, and when that provider is gone the job
// fails connection_needed — never through the other one, which can send.
func TestPersonalSending_RecordsAndSendsThroughItsProvider(t *testing.T) {
	t.Parallel()
	h, entra, google, channelID := twoPersonalMailboxes(t)
	c, user := h.SignInUser(t, "communications:conversations-reply", "communications:conversations-view")
	google.SetUser(user, contracts.PersonalMailStatus{Available: true, Address: "kari@example.no", Mailbox: "g"})

	send := func() uuid.UUID {
		t.Helper()
		res := c.Do(http.MethodPost, "/api/v1/communications/conversations", map[string]any{
			"channelId": channelID, "subject": "Subject", "textBody": "Hello", "sendFromPersonal": true,
			"to": []map[string]any{{"email": "customer@example.test"}},
		}, modtest.Header("Idempotency-Key", uuid.NewString()))
		if res.Status != http.StatusCreated {
			t.Fatalf("create: %d %s", res.Status, res.Body)
		}
		var mut conversationMutationJSON
		res.JSON(&mut)
		return uuid.MustParse(*mut.MessageId)
	}
	process := func() {
		t.Helper()
		if processed, err := communications.NewOutboxWorker(h.Deps()).ProcessOne(context.Background()); err != nil || !processed {
			t.Fatalf("ProcessOne = %v, %v", processed, err)
		}
	}

	first := send()
	if p := modtest.One[string](t, h, `SELECT send_provider FROM communications.conversation_messages WHERE id = $1`, first); p != "google" {
		t.Errorf("send_provider = %q, want google", p)
	}
	process()
	if n := len(google.Sent()); n != 1 {
		t.Errorf("google sends = %d, want 1", n)
	}
	if n := len(entra.Sent()); n != 0 {
		t.Errorf("entra sends = %d, want none", n)
	}

	// The provider recorded is gone: entra can send now, and is never used.
	second := send()
	entra.SetUser(user, contracts.PersonalMailStatus{Available: true, Address: "kari@example.com", Mailbox: "e"})
	h.Exec(t, `UPDATE communications.conversation_messages SET send_provider = 'gone' WHERE id = $1`, second)
	process()
	if job := readOutboxJob(t, h, second); job.status != "failed" || job.attempts != 1 {
		t.Errorf("job = %s after %d attempts, want failed after 1", job.status, job.attempts)
	}
	deliveries := readDeliveries(t, h, second)
	if len(deliveries) != 1 || deliveries[0].status != "submission_failed" {
		t.Errorf("deliveries = %+v, want one submission_failed", deliveries)
	}
	if code := modtest.One[string](t, h, `
		SELECT coalesce(string_agg(DISTINCT data_json::jsonb->>'code', ','), '')
		FROM communications.message_events WHERE message_id = $1 AND data_json IS NOT NULL`, second); code != "connection_needed" {
		t.Errorf("the failure's code = %q, want connection_needed", code)
	}
	if n := len(entra.Sent()); n != 0 {
		t.Errorf("entra sends = %d, want none: a message goes through its own provider only", n)
	}
}

// TestOutbox_RecordsWhatTheProviderSent: a provider answering the
// Message-ID it replaced and its thread has both recorded on the message;
// a reply naming that Message-ID, and a message in that thread naming
// nothing, thread onto the conversation we started — the thread across
// directions (G5).
func TestOutbox_RecordsWhatTheProviderSent(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	writer := in.h.SignIn(t, "communications:conversations-reply")
	sent := createConversation(t, writer, newConversationBody("customer@example.test"))
	messageID := uuid.MustParse(*sent.MessageId)
	in.fake.SetSentMessage(contracts.SentMessage{MessageID: "gmail-1@mail.example.test", ThreadID: "thread-1"})
	if processed, err := communications.NewOutboxWorker(in.h.Deps()).ProcessOne(context.Background()); err != nil || !processed {
		t.Fatalf("ProcessOne = %v, %v", processed, err)
	}
	got := modtest.One[string](t, in.h, `
		SELECT coalesce(sent_message_id, '-') || ' ' || coalesce(channel_metadata_json->>'providerThreadId', '-')
		FROM communications.conversation_messages WHERE id = $1`, messageID)
	if got != "<gmail-1@mail.example.test> thread-1" {
		t.Errorf("recorded = %q, want the bracketed Message-ID and the thread", got)
	}

	reply := in.plainMail("re")
	reply.InReplyTo = "<gmail-1@mail.example.test>"
	if c := in.importOne(t, reply, "").conversationID.String(); c != sent.ConversationId {
		t.Errorf("a reply to the sent Message-ID: conversation %s, want ours %s", c, sent.ConversationId)
	}
	if c := in.importOne(t, in.plainMail("same thread"), "thread-1").conversationID.String(); c != sent.ConversationId {
		t.Errorf("a message in the provider's thread of our message: conversation %s, want ours %s", c, sent.ConversationId)
	}
}

// TestOutbox_RecordsNothingOfTheMessagesOwnID: a provider answering the
// message's own Message-ID, and no thread, leaves the message as it was.
func TestOutbox_RecordsNothingOfTheMessagesOwnID(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	writer := in.h.SignIn(t, "communications:conversations-reply")
	sent := createConversation(t, writer, newConversationBody("customer@example.test"))
	messageID := uuid.MustParse(*sent.MessageId)
	in.fake.SetSentMessage(contracts.SentMessage{MessageID: expectedMessageID(messageID)})
	if processed, err := communications.NewOutboxWorker(in.h.Deps()).ProcessOne(context.Background()); err != nil || !processed {
		t.Fatalf("ProcessOne = %v, %v", processed, err)
	}
	got := modtest.One[string](t, in.h, `
		SELECT coalesce(sent_message_id, '-') || ' ' || coalesce(channel_metadata_json->>'providerThreadId', '-')
		FROM communications.conversation_messages WHERE id = $1`, messageID)
	if got != "- -" {
		t.Errorf("recorded = %q, want nothing", got)
	}
}

// TestPersonalSending_SendsAMessageRecordedWithoutAProvider: a personal
// message queued by a replica older than send_provider (migration 00111)
// goes through the only provider filled, or, with two, the one the author's
// status names — never failing connection_needed for the missing column.
func TestPersonalSending_SendsAMessageRecordedWithoutAProvider(t *testing.T) {
	t.Parallel()
	queue := func(t *testing.T, h *modtest.Harness, c *modtest.Client, channelID string) uuid.UUID {
		t.Helper()
		res := c.Do(http.MethodPost, "/api/v1/communications/conversations", map[string]any{
			"channelId": channelID, "subject": "Subject", "textBody": "Hello", "sendFromPersonal": true,
			"to": []map[string]any{{"email": "customer@example.test"}},
		}, modtest.Header("Idempotency-Key", uuid.NewString()))
		if res.Status != http.StatusCreated {
			t.Fatalf("create: %d %s", res.Status, res.Body)
		}
		var mut conversationMutationJSON
		res.JSON(&mut)
		id := uuid.MustParse(*mut.MessageId)
		h.Exec(t, `UPDATE communications.conversation_messages SET send_provider = NULL WHERE id = $1`, id)
		return id
	}
	process := func(t *testing.T, h *modtest.Harness) {
		t.Helper()
		if processed, err := communications.NewOutboxWorker(h.Deps()).ProcessOne(context.Background()); err != nil || !processed {
			t.Fatalf("ProcessOne = %v, %v", processed, err)
		}
	}

	t.Run("the only provider", func(t *testing.T) {
		t.Parallel()
		fake := modtest.NewFakePersonalMailbox()
		h := newHarness(t, modtest.WithPersonalMailbox(fake))
		channelID := setupChannel(t, h)
		c, user := h.SignInUser(t, "communications:conversations-reply", "communications:conversations-view")
		fake.SetUser(user, contracts.PersonalMailStatus{Available: true, Address: "kari@example.com", Mailbox: "e"})
		id := queue(t, h, c, channelID)
		process(t, h)
		if job := readOutboxJob(t, h, id); job.status != "completed" {
			t.Errorf("job = %s, want completed", job.status)
		}
		if n := len(fake.Sent()); n != 1 {
			t.Errorf("sends = %d, want 1", n)
		}
	})

	t.Run("two providers, the one the author's status names", func(t *testing.T) {
		t.Parallel()
		h, entra, google, channelID := twoPersonalMailboxes(t)
		c, user := h.SignInUser(t, "communications:conversations-reply", "communications:conversations-view")
		google.SetUser(user, contracts.PersonalMailStatus{Available: true, Address: "kari@example.no", Mailbox: "g"})
		id := queue(t, h, c, channelID)
		process(t, h)
		if job := readOutboxJob(t, h, id); job.status != "completed" {
			t.Errorf("job = %s, want completed", job.status)
		}
		if n := len(google.Sent()); n != 1 {
			t.Errorf("google sends = %d, want 1", n)
		}
		if n := len(entra.Sent()); n != 0 {
			t.Errorf("entra sends = %d, want none", n)
		}
	})

	t.Run("a failed one is retried", func(t *testing.T) {
		t.Parallel()
		fake := modtest.NewFakePersonalMailbox()
		h := newHarness(t, modtest.WithPersonalMailbox(fake))
		channelID := setupChannel(t, h)
		c, user := h.SignInUser(t, "communications:conversations-reply", "communications:conversations-view")
		fake.SetUser(user, contracts.PersonalMailStatus{Available: true, Address: "kari@example.com", Mailbox: "e"})
		id := queue(t, h, c, channelID)
		fake.FailWith("Send", contracts.ErrConnectionNeeded)
		process(t, h)
		if job := readOutboxJob(t, h, id); job.status != "failed" {
			t.Fatalf("job = %s, want failed", job.status)
		}
		fake.FailWith("Send", nil)
		conv := modtest.One[uuid.UUID](t, h, `SELECT conversation_id FROM communications.conversation_messages WHERE id = $1`, id)
		res := c.Do(http.MethodPost, "/api/v1/communications/conversations/"+conv.String()+"/messages/"+id.String()+"/retry", nil)
		if res.Status != http.StatusOK {
			t.Fatalf("retry: %d %s, want 200", res.Status, res.Body)
		}
	})
}
