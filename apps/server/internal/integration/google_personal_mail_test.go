package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/google/googletest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Sending from a person's own Gmail address through Communications' own
// composer and outbox (Google Workspace design G35, delivery G): the real
// communications, customers and google modules composed together, against
// the fake Google, on a Gmail channel post@. Communications picks Google's
// PersonalMailbox by the author's status and records it, as cmd/vantigo
// does. Staff is imitated — DirectoryAccount answers each person's account
// — and a person's connection is seeded as a completed connect for Gmail
// leaves it (the flow is google's own suite's). Entra stands in as a fake
// PersonalMailbox under its key: its own Send is its package's.

// gmailPersonScopes are what a connect for the calendar and then Gmail is
// granted.
var gmailPersonScopes = []string{
	"email", "https://www.googleapis.com/auth/calendar.app.created", "https://www.googleapis.com/auth/calendar.events",
	"https://www.googleapis.com/auth/gmail.send", "openid", "profile",
}

// personalRig is the Gmail channel's installation with Connection for
// people on, Staff answering each person's account, and Entra's personal
// mailbox faked beside Google's.
type personalRig struct {
	*gmailRig
	wf    *gcalWorkforce
	entra *modtest.FakePersonalMailbox
	conv  string
}

func newPersonalRig(t *testing.T) *personalRig {
	t.Helper()
	wf := &gcalWorkforce{accounts: map[uuid.UUID]contracts.DirectoryAccount{}}
	entra := modtest.NewFakePersonalMailbox()
	r := newGmailRig(t, modtest.WithWorkforce(wf), modtest.WithPersonalMailbox(entra))
	r.g.AddWebClient(gcalWebClient, gcalSecret)
	sealed, err := r.Deps().Secrets.Seal("google/oauth-client-secret", []byte(gcalSecret))
	if err != nil {
		t.Fatal(err)
	}
	r.Exec(t, `INSERT INTO google.credentials (kind, sealed, created_at) VALUES ('oauth_client_secret', $1, $2)`, sealed, r.Now())
	r.Exec(t, `UPDATE google.connection SET oauth_client_id = $1, personal_connection = true, personal_connection_at = $2`, gcalWebClient, r.Now())
	channelID, _ := r.channel(t)
	return &personalRig{gmailRig: r, wf: wf, entra: entra, conv: channelID.String()}
}

// person is a signed-in user who may write conversations, whose Staff
// record names a Google account Vantigo made, connected for the calendar and
// Gmail: the client, the user and the account's Directory id.
func (r *personalRig) person(t *testing.T, email string) (*modtest.Client, uuid.UUID, string) {
	t.Helper()
	c, id := r.SignInUser(t, "communications:conversations-view", "communications:conversations-reply")
	u := r.g.AddUser(googletest.User{PrimaryEmail: email, GivenName: "Kari", FamilyName: "Nordmann"})
	local, _, _ := strings.Cut(email, "@")
	r.Exec(t, `INSERT INTO google.accounts (user_id, employee_number, primary_email, state, created_by_us, created_at, updated_at)
	           VALUES ($1, $2, $3, 'enabled', true, $4, $4)`, u.ID, "E-"+local, email, r.Now())
	r.connect(t, u.ID, id, email)
	r.wf.mu.Lock()
	r.wf.accounts[id] = contracts.DirectoryAccount{Provider: "google", ExternalID: u.ID, Enabled: true}
	r.wf.mu.Unlock()
	return c, id, u.ID
}

// connect leaves gid's connection as a completed connect for Gmail does:
// active, a fresh refresh token, the revision moved on.
func (r *personalRig) connect(t *testing.T, gid string, userID uuid.UUID, email string) {
	t.Helper()
	refresh := r.g.IssueRefresh(googletest.CodeGrant{ClientID: gcalWebClient, Sub: gid, Email: email, HD: "nordlys.no", Granted: gmailPersonScopes})
	sealed, err := r.Deps().Secrets.Seal("google/connection-refresh", []byte(refresh))
	if err != nil {
		t.Fatal(err)
	}
	r.Exec(t, `INSERT INTO google.connections (google_user_id, user_id, scopes, refresh_sealed, state, connected_at, changed_at, created_at, updated_at)
	           VALUES ($1, $2, $3, $4, 'active', $5, $5, $5, $5)
	           ON CONFLICT (google_user_id) DO UPDATE SET refresh_sealed = EXCLUDED.refresh_sealed, scopes = EXCLUDED.scopes,
	               state = 'active', error_code = NULL, changed_at = EXCLUDED.changed_at, revision = google.connections.revision + 1`,
		gid, userID, gmailPersonScopes, sealed, r.Now())
}

// sender is what GET /personal-sender answers c: the provider's name, or
// the reason it cannot send.
func sender(t *testing.T, c *modtest.Client) string {
	t.Helper()
	var s struct {
		Available           bool    `json:"available"`
		Reason              *string `json:"reason"`
		Address             *string `json:"address"`
		ProviderDisplayName *string `json:"providerDisplayName"`
	}
	gmailDo(t, c, http.MethodGet, "/api/v1/communications/personal-sender", nil, http.StatusOK, &s)
	if !s.Available {
		return "unavailable " + *s.Reason
	}
	return *s.ProviderDisplayName + " " + *s.Address
}

// start begins a conversation from c's own address: the conversation and
// the message.
func (r *personalRig) start(t *testing.T, c *modtest.Client, text string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	var mut struct {
		ConversationID string  `json:"conversationId"`
		MessageID      *string `json:"messageId"`
	}
	gmailDo(t, c, http.MethodPost, "/api/v1/communications/conversations", map[string]any{
		"channelId": r.conv, "subject": "Strømavtale", "textBody": text, "sendFromPersonal": true,
		"to": []map[string]any{{"email": "kari@example.test"}},
	}, http.StatusCreated, &mut, modtest.Header("Idempotency-Key", uuid.NewString()))
	return uuid.MustParse(mut.ConversationID), uuid.MustParse(*mut.MessageID)
}

// replyFrom answers conv from c's own address: the message.
func (r *personalRig) replyFrom(t *testing.T, c *modtest.Client, conv uuid.UUID, text string) uuid.UUID {
	t.Helper()
	var queued struct {
		MessageID *string `json:"messageId"`
	}
	gmailDo(t, c, http.MethodPost, "/api/v1/communications/conversations/"+conv.String()+"/reply",
		map[string]any{"textBody": text, "sendFromPersonal": true}, http.StatusCreated, &queued, modtest.Header("Idempotency-Key", uuid.NewString()))
	return uuid.MustParse(*queued.MessageID)
}

// sentBy is the message in address's Sent whose text holds text.
func (r *personalRig) sentBy(t *testing.T, address, text string) googletest.GmailMessage {
	t.Helper()
	for _, m := range r.g.GmailMessages(address) {
		if slices.Contains(m.Labels, "SENT") && bytes.Contains(m.Raw, []byte(text)) {
			return m
		}
	}
	t.Fatalf("%s's Sent holds no message saying %q", address, text)
	return googletest.GmailMessage{}
}

// jobOf is the message's outbox job's status and attempts.
func (r *personalRig) jobOf(t *testing.T, message uuid.UUID) string {
	t.Helper()
	return modtest.One[string](t, r.Harness, `SELECT status || '/' || attempts FROM communications.outbox_jobs WHERE message_id = $1`, message)
}

// answerMail is a customer's answer to post@ naming inReplyTo and the
// references.
func answerMail(id, inReplyTo string, references []string, text string) []byte {
	return fmt.Appendf(nil, "From: Kari Nordmann <kari@example.test>\r\nTo: %s\r\nSubject: Re: Strømavtale\r\nMessage-ID: %s\r\n"+
		"In-Reply-To: %s\r\nReferences: %s\r\nDate: Thu, 29 Oct 2026 10:00:00 +0000\r\nMIME-Version: 1.0\r\n"+
		"Content-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", gmailPost, id, inReplyTo, strings.Join(references, " "), text)
}

// TestGooglePersonalMail_NewConversationAndReply: a person connected for
// Gmail starts a conversation and replies from their own address; each
// leaves through their own Gmail, lands in their Sent and not the channel's,
// carries Reply-To the channel, and is recorded under google with Gmail's
// thread; the customer's answers to the channel thread back — by our
// Message-ID while Gmail keeps it, and by References when it replaced it
// (U6).
func TestGooglePersonalMail_NewConversationAndReply(t *testing.T) {
	t.Parallel()
	r := newPersonalRig(t)
	c, _, _ := r.person(t, "ola@nordlys.no")
	if got := sender(t, c); got != "Gmail ola@nordlys.no" {
		t.Fatalf("personal sender = %q, want Gmail ola@nordlys.no", got)
	}

	conv, first := r.start(t, c, "Hei, her er tilbudet.")
	r.send(t)
	if job := r.jobOf(t, first); job != "completed/1" {
		t.Fatalf("the first job = %s, want completed", job)
	}
	sent := r.sentBy(t, "ola@nordlys.no", "Hei, her er tilbudet.")
	if !bytes.Contains(sent.Raw, []byte("Reply-To:")) || !bytes.Contains(sent.Raw, []byte(gmailPost)) || !bytes.Contains(sent.Raw, []byte("From: ")) {
		t.Errorf("the message lacks Reply-To the channel:\n%s", sent.Raw)
	}
	if !bytes.Contains(sent.Raw, []byte("ola@nordlys.no")) {
		t.Errorf("the message is not from Ola's address:\n%s", sent.Raw)
	}
	for _, m := range r.g.GmailMessages(gmailPost) {
		if slices.Contains(m.Labels, "SENT") {
			t.Fatalf("the channel's mailbox sent a message: %+v", m)
		}
	}
	var provider, ownID, thread string
	var sentID *string
	if err := r.Pool().QueryRow(context.Background(), `SELECT send_provider, rfc_message_id, sent_message_id,
		coalesce(channel_metadata_json->>'providerThreadId', '') FROM communications.conversation_messages WHERE id = $1`, first).
		Scan(&provider, &ownID, &sentID, &thread); err != nil {
		t.Fatal(err)
	}
	if provider != "google" || thread != sent.ThreadID || sentID != nil || sent.MessageID != ownID {
		t.Errorf("recorded provider %q thread %q sent id %v (own %s, Gmail's %s), want google, Gmail's thread, none: Gmail kept ours",
			provider, thread, sentID, ownID, sent.MessageID)
	}

	// The customer answers the channel, naming our Message-ID.
	r.Advance(10 * time.Minute)
	answer := r.g.Deliver(gmailPost, answerMail("<k1@example.test>", ownID, []string{ownID}, "Takk, ser fint ut."), r.Now())
	r.sync(t)
	if got := r.conversationOf(t, answer.ID); got != conv {
		t.Fatalf("the answer went to %s, want %s", got, conv)
	}

	// A reply from Ola's address, Gmail replacing its Message-ID this time.
	r.g.SetReplaceMessageID(true)
	reply := r.replyFrom(t, c, conv, "Flott, da sender jeg avtalen.")
	r.send(t)
	if job := r.jobOf(t, reply); job != "completed/1" {
		t.Fatalf("the reply's job = %s, want completed", job)
	}
	replied := r.sentBy(t, "ola@nordlys.no", "Flott, da sender jeg avtalen.")
	if !strings.HasSuffix(replied.MessageID, "@mail.gmail.com>") || !bytes.Contains(replied.Raw, []byte("<k1@example.test>")) {
		t.Errorf("the reply: Message-ID %s, want Gmail's, and References naming the customer's", replied.MessageID)
	}
	// gmail.send reads nothing back: the reply's sent id stays unknown, and
	// the customer's answer threads by its References.
	r.Advance(10 * time.Minute)
	again := r.g.Deliver(gmailPost, answerMail("<k2@example.test>", replied.MessageID,
		[]string{ownID, "<k1@example.test>", replied.MessageID}, "Mottatt."), r.Now())
	r.sync(t)
	if got := r.conversationOf(t, again.ID); got != conv {
		t.Errorf("the answer to the reply went to %s, want %s", got, conv)
	}
	if n := r.Count(t, `SELECT count(*) FROM communications.conversations WHERE channel_id = $1`, uuid.MustParse(r.conv)); n != 1 {
		t.Errorf("conversations = %d, want 1", n)
	}
	if logs := r.Logs(); strings.Contains(logs, "ola@nordlys.no") || strings.Contains(logs, r.g.Words) {
		t.Error("the log holds the person's address or Google's words")
	}
}

// TestGooglePersonalMail_ReconnectNeededAndRetry: a connection Google
// refuses at send time fails the message at once with connection_needed —
// nothing sent, from the person's address or the channel's —; after the
// person reconnects, a retry sends it from their own address.
func TestGooglePersonalMail_ReconnectNeededAndRetry(t *testing.T) {
	t.Parallel()
	r := newPersonalRig(t)
	c, userID, gid := r.person(t, "ola@nordlys.no")
	for _, token := range r.g.RefreshTokens(gid) {
		r.g.RefuseRefresh(token, "invalid_grant")
	}
	conv, message := r.start(t, c, "Hei igjen.")
	r.send(t)
	if job := r.jobOf(t, message); job != "failed/1" {
		t.Fatalf("the job = %s, want failed at its first attempt", job)
	}
	if code := modtest.One[string](t, r.Harness, `SELECT data_json FROM communications.message_events
		WHERE message_id = $1 AND event_type = 'submission_failed' LIMIT 1`, message); !strings.Contains(code, `"code":"connection_needed"`) {
		t.Errorf("the failure = %s, want connection_needed", code)
	}
	if n := len(r.g.GmailMessages("ola@nordlys.no")) + len(r.g.GmailMessages(gmailPost)); n != 0 {
		t.Errorf("%d messages sent, want none: never from the channel instead", n)
	}
	if got := sender(t, c); got != "unavailable reconnect" {
		t.Errorf("personal sender = %q, want reconnect", got)
	}

	r.Advance(time.Minute)
	r.connect(t, gid, userID, "ola@nordlys.no")
	gmailDo(t, c, http.MethodPost, "/api/v1/communications/conversations/"+conv.String()+"/messages/"+message.String()+"/retry",
		nil, http.StatusOK, nil)
	r.send(t)
	if job := modtest.One[string](t, r.Harness, `SELECT status FROM communications.outbox_jobs WHERE message_id = $1
		ORDER BY created_at DESC LIMIT 1`, message); job != "completed" {
		t.Errorf("the retried job = %s, want completed", job)
	}
	r.sentBy(t, "ola@nordlys.no", "Hei igjen.")
}

// TestGooglePersonalMail_EachThroughTheirOwnProvider: in one installation
// an Entra person and a Google person each send from their own address
// through their own provider, and each message records it.
func TestGooglePersonalMail_EachThroughTheirOwnProvider(t *testing.T) {
	t.Parallel()
	r := newPersonalRig(t)
	ola, _, _ := r.person(t, "ola@nordlys.no")
	per, perID := r.SignInUser(t, "communications:conversations-view", "communications:conversations-reply")
	r.entra.SetUser(perID, contracts.PersonalMailStatus{Available: true, Address: "per@nordlys.onmicrosoft.com", Mailbox: "per-oid"})
	if got := sender(t, per); got != "Outlook per@nordlys.onmicrosoft.com" {
		t.Fatalf("Per's sender = %q, want Outlook", got)
	}

	_, fromOla := r.start(t, ola, "Fra Ola.")
	_, fromPer := r.start(t, per, "Fra Per.")
	r.send(t)
	for message, want := range map[uuid.UUID]string{fromOla: "google", fromPer: "entra"} {
		if got := modtest.One[string](t, r.Harness, `SELECT send_provider FROM communications.conversation_messages WHERE id = $1`, message); got != want {
			t.Errorf("send_provider = %s, want %s", got, want)
		}
		if job := r.jobOf(t, message); job != "completed/1" {
			t.Errorf("%s's job = %s, want completed", want, job)
		}
	}
	r.sentBy(t, "ola@nordlys.no", "Fra Ola.")
	sent := r.entra.Sent()
	if len(sent) != 1 || sent[0].UserID != perID {
		t.Errorf("Entra's sends = %+v, want Per's alone", sent)
	}
	if n := len(r.g.GmailMessages("ola@nordlys.no")); n != 1 {
		t.Errorf("Ola's Gmail holds %d messages, want his one", n)
	}
}
