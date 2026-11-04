package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/google"
	"github.com/vantigo-io/vantigo/server/internal/google/googletest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Gmail mail channels through Communications' own outbox and mailbox sync
// (Google Workspace design G33, G34, delivery F): the real communications,
// customers and google modules composed together, Communications picking
// Google's ChannelMailbox by the channel's provider as cmd/vantigo does,
// against the fake Google. Nothing is imitated but Google.

// The fake organisation.
const (
	gmailAdmin    = "admin@nordlys.no"
	gmailPost     = "post@nordlys.no"
	gmailSAEmail  = "vantigo@nordlys-vantigo.iam.gserviceaccount.com"
	gmailClientID = "102345678901234567890"
)

// gmailRecorder validates every exchange against the three modules'
// contracts together.
var gmailRecorder = mergedRecorder("communications", "customers", "google")

// The service account, made once: a 2048-bit key takes a while.
var (
	gmailAccountOnce sync.Once
	gmailAccount     googletest.ServiceAccount
)

// gmailRig is the installation: Mail channels on and tested, post@ a
// licensed user.
type gmailRig struct {
	*modtest.Harness
	g     *googletest.FakeGoogle
	admin *modtest.Client
	post  googletest.User
}

func newGmailRig(t *testing.T, opts ...modtest.Option) *gmailRig {
	t.Helper()
	gmailAccountOnce.Do(func() {
		gmailAccount = googletest.NewServiceAccount(gmailSAEmail, gmailClientID, "nordlys-vantigo")
	})
	g := googletest.New()
	g.Admins = []string{gmailAdmin}
	g.CustomerID = "C03az79cb"
	g.Domains = []googletest.Domain{{Name: "nordlys.no", Verified: true, IsPrimary: true}}
	g.AddKey(gmailAccount)
	g.Delegate(gmailClientID,
		googletest.ScopeCustomer, googletest.ScopeDomains, googletest.ScopeUsers, googletest.ScopeGroups,
		googletest.ScopeGroupsSettings, googletest.ScopeLicensing, googletest.ScopeGmailModify, googletest.ScopeGmailSend)
	h := modtest.New(t, append([]modtest.Option{
		modtest.WithRecorder(gmailRecorder),
		modtest.WithModule(customers.Module()),
		modtest.WithModule(communications.Module()),
		modtest.WithModule(google.Module()),
		modtest.WithTransport(g),
		modtest.WithBackoff(func(int) time.Duration { return 0 }),
		modtest.WithEnv("STORAGE_PROVIDER", "fs"),
		modtest.WithEnv("STORAGE_FS_ROOT", t.TempDir()),
		modtest.WithEnv("STORAGE_FS_ALLOW_INSECURE_ROOT", "1"),
	}, opts...)...)
	g.Configure(func(g *googletest.FakeGoogle) { g.Now = h.Now })
	post := g.AddUser(googletest.User{PrimaryEmail: gmailPost, GivenName: "Post", FamilyName: "Nordlys"})

	owner, ownerID := h.SignInUser(t)
	h.Exec(t, `INSERT INTO identity.user_roles (user_id, role_id) SELECT $1, id FROM identity.roles WHERE name = 'Owner'`, ownerID)
	gmailDo(t, owner, http.MethodPost, "/api/v1/google/connection/credentials", map[string]any{"json": gmailAccount.KeyFile()}, http.StatusCreated, nil)
	var conn struct {
		Version int32 `json:"version"`
	}
	gmailDo(t, owner, http.MethodGet, "/api/v1/google/connection", nil, http.StatusOK, &conn)
	gmailDo(t, owner, http.MethodPut, "/api/v1/google/connection", map[string]any{"adminSubject": gmailAdmin}, http.StatusOK, nil,
		modtest.Header("If-Match", strconv.Itoa(int(conn.Version))))
	gmailDo(t, owner, http.MethodPut, "/api/v1/google/connection/mail-channels", map[string]any{"enabled": true}, http.StatusOK, nil)
	var test struct {
		Status string `json:"status"`
	}
	gmailDo(t, owner, http.MethodPost, "/api/v1/google/connection/test", nil, http.StatusOK, &test)
	if test.Status != "connected" {
		t.Fatalf("the test: %+v", test)
	}
	admin := h.SignIn(t, "communications:channels-manage", "communications:conversations-view",
		"communications:conversations-manage", "communications:conversations-reply")
	return &gmailRig{Harness: h, g: g, admin: admin, post: post}
}

// gmailDo sends one request, failing unless it answered want.
func gmailDo(t *testing.T, c *modtest.Client, method, path string, body any, want int, out any, opts ...modtest.RequestOption) *modtest.Response {
	t.Helper()
	resp := c.Do(method, path, body, opts...)
	if resp.Status != want {
		t.Fatalf("%s %s: %d %s, want %d", method, path, resp.Status, resp.Body, want)
	}
	if out != nil {
		resp.JSON(out)
	}
	return resp
}

// gmailCode is a refusal's code.
func gmailCode(t *testing.T, r *modtest.Response) string {
	t.Helper()
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	r.JSON(&e)
	return e.Error.Code
}

// channel creates the Google channel on post@: its id and mailbox.
func (r *gmailRig) channel(t *testing.T) (uuid.UUID, string) {
	t.Helper()
	var ch struct {
		ID       string `json:"id"`
		Provider string `json:"provider"`
		Mailbox  *struct {
			ID string `json:"id"`
		} `json:"mailbox"`
	}
	gmailDo(t, r.admin, http.MethodPost, "/api/v1/communications/channels",
		map[string]any{"type": "email", "provider": "google", "address": gmailPost, "displayName": "Nordlys"}, http.StatusCreated, &ch)
	if ch.Provider != "google" || ch.Mailbox == nil || ch.Mailbox.ID != r.post.ID {
		t.Fatalf("channel = %+v, want google on post@'s Directory id", ch)
	}
	return uuid.MustParse(ch.ID), ch.Mailbox.ID
}

// customerMail is a message from a customer to post@.
func customerMail(id, inReplyTo, text string) []byte {
	b := fmt.Sprintf("From: Kari Nordmann <kari@example.test>\r\nTo: %s\r\nSubject: Strømavtale\r\nMessage-ID: %s\r\n", gmailPost, id)
	if inReplyTo != "" {
		b += "In-Reply-To: " + inReplyTo + "\r\n"
	}
	return []byte(b + "Date: Thu, 29 Oct 2026 10:00:00 +0000\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + text + "\r\n")
}

// gmailWorker is Communications' worker of type W as worker mode builds it:
// its Deps carrying the providers Workers collected, Google's among them.
func gmailWorker[W any](t *testing.T, r *gmailRig) W {
	t.Helper()
	for _, w := range r.Workers(r.Deps()) {
		if mine, ok := w.(W); ok {
			return mine
		}
	}
	var zero W
	t.Fatalf("no worker of type %T", zero)
	return zero
}

// sync drains Communications' mailbox sync: due channels and marks.
func (r *gmailRig) sync(t *testing.T) {
	t.Helper()
	w := gmailWorker[*communications.MailboxSyncWorker](t, r)
	for range 20 {
		processed, err := w.ProcessOne(context.Background())
		if err != nil {
			t.Fatalf("mailbox sync: %v", err)
		}
		if !processed {
			return
		}
	}
	t.Fatal("the mailbox sync never ran out of work")
}

// send drains Communications' outbox.
func (r *gmailRig) send(t *testing.T) {
	t.Helper()
	w := gmailWorker[*communications.OutboxWorker](t, r)
	for range 20 {
		processed, err := w.ProcessOne(context.Background())
		if err != nil {
			t.Fatalf("outbox: %v", err)
		}
		if !processed {
			return
		}
	}
	t.Fatal("the outbox never ran out of work")
}

// conversationOf is the conversation the Gmail message id was imported to.
func (r *gmailRig) conversationOf(t *testing.T, gmailID string) uuid.UUID {
	t.Helper()
	return modtest.One[uuid.UUID](t, r.Harness, `SELECT m.conversation_id FROM communications.inbound_receipts r
		JOIN communications.conversation_messages m ON m.id = r.message_id WHERE r.provider_message_id = $1`, gmailID)
}

// receiptOf is the outcome and reason of the Gmail message id's receipt.
func (r *gmailRig) receiptOf(t *testing.T, gmailID string) string {
	t.Helper()
	return modtest.One[string](t, r.Harness, `SELECT outcome || '/' || coalesce(reason, '') FROM communications.inbound_receipts
		WHERE provider_message_id = $1`, gmailID)
}

// reply answers the conversation: the queued message's id.
func (r *gmailRig) reply(t *testing.T, conv uuid.UUID, text string) uuid.UUID {
	t.Helper()
	var queued struct {
		MessageID *string `json:"messageId"`
	}
	gmailDo(t, r.admin, http.MethodPost, "/api/v1/communications/conversations/"+conv.String()+"/reply",
		map[string]any{"textBody": text}, http.StatusCreated, &queued, modtest.Header("Idempotency-Key", uuid.NewString()))
	return uuid.MustParse(*queued.MessageID)
}

// TestGoogleMail_Channels: Google is listed with the tenant-wide warning and
// its budget; a channel on a licensed mailbox is created; a group's address,
// a person's own mailbox and an unknown address are refused, each its way.
func TestGoogleMail_Channels(t *testing.T) {
	t.Parallel()
	r := newGmailRig(t)
	var providers []struct {
		Key             string  `json:"key"`
		DisplayName     string  `json:"displayName"`
		Available       bool    `json:"available"`
		Warning         *string `json:"warning"`
		MaxMessageBytes *int64  `json:"maxMessageBytes"`
	}
	gmailDo(t, r.admin, http.MethodGet, "/api/v1/communications/channel-providers", nil, http.StatusOK, &providers)
	i := slices.IndexFunc(providers, func(p struct {
		Key             string  `json:"key"`
		DisplayName     string  `json:"displayName"`
		Available       bool    `json:"available"`
		Warning         *string `json:"warning"`
		MaxMessageBytes *int64  `json:"maxMessageBytes"`
	}) bool {
		return p.Key == "google"
	})
	if i < 0 || providers[i].DisplayName != "Google Workspace" || !providers[i].Available ||
		providers[i].Warning == nil || *providers[i].Warning != "tenant_wide" || providers[i].MaxMessageBytes == nil || *providers[i].MaxMessageBytes != 25_000_000 {
		t.Fatalf("providers = %+v, want Google Workspace available with the warning and 25 MB", providers)
	}
	r.channel(t)

	r.g.AddGroup(googletest.Group{Email: "salg@nordlys.no", Name: "Salg"})
	kari := r.g.AddUser(googletest.User{PrimaryEmail: "kari@nordlys.no", GivenName: "Kari", FamilyName: "Nordmann"})
	r.Exec(t, `INSERT INTO google.accounts (user_id, employee_number, primary_email, state, created_by_us, created_at, updated_at)
	           VALUES ($1, 'E-1', $2, 'enabled', true, now(), now())`, kari.ID, kari.PrimaryEmail)
	for address, want := range map[string]string{
		"salg@nordlys.no":   "group_address",
		"kari@nordlys.no":   "personal_mailbox",
		"nobody@nordlys.no": "mailbox_unknown",
	} {
		resp := gmailDo(t, r.admin, http.MethodPost, "/api/v1/communications/channels",
			map[string]any{"type": "email", "provider": "google", "address": address}, http.StatusUnprocessableEntity, nil)
		if got := gmailCode(t, resp); got != want {
			t.Errorf("%s: %s, want %s", address, got, want)
		}
	}
	if n := r.Count(t, `SELECT count(*) FROM google.channel_mailboxes`); n != 1 {
		t.Errorf("mailboxes recorded = %d, want post@ alone", n)
	}
	if strings.Contains(r.Logs(), "kari@nordlys.no") || strings.Contains(r.Logs(), r.g.Words) {
		t.Error("the log holds an address or Google's words")
	}
}

// TestGoogleMail_SendReceiveThread: a customer's mail is imported; the reply
// leaves through Gmail, which replaces its Message-ID, recorded as the sent
// Message-ID with Gmail's thread; the customer's answer to Gmail's id
// threads onto the conversation, and so does a message Gmail put in the
// thread with no header naming ours. With notifications active the channel
// stays on the normal poll (Watch: ErrWatchUnsupported). All on a pool of
// one connection.
func TestGoogleMail_SendReceiveThread(t *testing.T) {
	t.Parallel()
	r := newGmailRig(t, modtest.WithPoolMaxConns(1), modtest.WithEnv("PUBLIC_BASE_URL", "https://public.example.com"))
	r.g.SetReplaceMessageID(true)
	channelID, _ := r.channel(t)
	r.Advance(time.Minute)

	first := r.g.Deliver(gmailPost, customerMail("<q1@example.test>", "", "Hei, jeg lurer på avtalen."), r.Now())
	r.sync(t)
	conv := r.conversationOf(t, first.ID)
	// Notifications are active on the installation, but Gmail has no push:
	// the channel stays on the five-minute poll, with nothing logged.
	if next := modtest.One[time.Time](t, r.Harness, `SELECT next_sync_at FROM communications.channel_sync WHERE channel_id = $1`, channelID); !next.Equal(r.Now().Add(5 * time.Minute)) {
		t.Errorf("next sync at %v, want the five-minute poll from %v", next, r.Now())
	}
	if strings.Contains(r.Logs(), "could not watch") {
		t.Error("Gmail's missing push was logged as a failed watch")
	}

	replyID := r.reply(t, conv, "Takk, vi ser på det.")
	r.send(t)
	var sentID, thread *string
	if err := r.Pool().QueryRow(context.Background(), `SELECT sent_message_id, channel_metadata_json->>'providerThreadId'
		FROM communications.conversation_messages WHERE id = $1`, replyID).Scan(&sentID, &thread); err != nil {
		t.Fatal(err)
	}
	if sentID == nil || !strings.HasSuffix(*sentID, "@mail.gmail.com>") || thread == nil || *thread != first.ThreadID {
		t.Fatalf("the reply's sent Message-ID %v and thread %v, want Gmail's and %s", sentID, thread, first.ThreadID)
	}
	if status := modtest.One[string](t, r.Harness, `SELECT status FROM communications.outbox_jobs WHERE message_id = $1`, replyID); status != "completed" {
		t.Errorf("the reply's job = %s, want completed", status)
	}
	var inSent bool
	for _, m := range r.g.GmailMessages(gmailPost) {
		if slices.Contains(m.Labels, "SENT") && m.MessageID == *sentID {
			inSent = true
		}
	}
	if !inSent {
		t.Error("the reply is not in the mailbox's Sent")
	}

	// The customer answers Gmail's Message-ID.
	r.Advance(10 * time.Minute)
	answer := r.g.Deliver(gmailPost, customerMail("<q2@example.test>", *sentID, "Flott."), r.Now())
	r.sync(t)
	if got := r.conversationOf(t, answer.ID); got != conv {
		t.Errorf("the answer to Gmail's Message-ID went to %s, want %s", got, conv)
	}
	// A message Gmail threads with no header naming any of ours.
	r.Advance(10 * time.Minute)
	loose := r.g.DeliverInThread(gmailPost, customerMail("<q3@example.test>", "", "En ting til."), r.Now(), first.ThreadID)
	r.sync(t)
	if got := r.conversationOf(t, loose.ID); got != conv {
		t.Errorf("a message in Gmail's thread went to %s, want %s", got, conv)
	}
	if n := r.Count(t, `SELECT count(*) FROM communications.conversations WHERE channel_id = $1`, channelID); n != 1 {
		t.Errorf("conversations = %d, want 1", n)
	}
}

// TestGoogleMail_TooLargeRestartAndMarks: an outbound message Gmail refuses
// as too large fails for good; an inbound one above the cap is skipped; a
// history start Gmail no longer keeps restarts the sync without importing
// twice; closing marks the messages read, then moves them under a label.
func TestGoogleMail_TooLargeRestartAndMarks(t *testing.T) {
	t.Parallel()
	r := newGmailRig(t, modtest.WithEnv("COMMUNICATIONS_INBOUND_MAX_BYTES", strconv.Itoa(1<<20)))
	channelID, _ := r.channel(t)
	r.Advance(time.Minute)

	first := r.g.Deliver(gmailPost, customerMail("<a1@example.test>", "", "Hei"), r.Now())
	big := r.g.Deliver(gmailPost, customerMail("<a2@example.test>", "", strings.Repeat("x", 1<<20+10)), r.Now())
	r.sync(t)
	conv := r.conversationOf(t, first.ID)
	if got := r.receiptOf(t, big.ID); got != "skipped/too_large" {
		t.Errorf("the large message's receipt = %s, want skipped/too_large", got)
	}

	// Outbound: Gmail answers 413.
	r.g.SetGmailSendLimit(100)
	replyID := r.reply(t, conv, "Et svar som er for stort for denne testen.")
	r.send(t)
	if status := modtest.One[string](t, r.Harness, `SELECT status || '/' || attempts FROM communications.outbox_jobs WHERE message_id = $1`, replyID); status != "failed/1" {
		t.Errorf("the job = %s, want failed at its first attempt", status)
	}
	if data := modtest.One[string](t, r.Harness, `SELECT data_json FROM communications.message_events
		WHERE message_id = $1 AND event_type = 'submission_failed' LIMIT 1`, replyID); !strings.Contains(data, `"code":"too_large"`) {
		t.Errorf("the failure = %s, want too_large", data)
	}
	r.g.SetGmailSendLimit(0)

	// The history start expired: the sync restarts and imports the new
	// message once.
	r.g.ExpireHistory(gmailPost)
	r.Advance(10 * time.Minute)
	next := r.g.Deliver(gmailPost, customerMail("<a3@example.test>", "<a1@example.test>", "Hallo?"), r.Now())
	r.sync(t)
	if got := r.conversationOf(t, next.ID); got != conv {
		t.Errorf("after the restart: %s, want %s", got, conv)
	}
	if n := r.Count(t, `SELECT count(*) FROM communications.inbound_receipts WHERE channel_id = $1`, channelID); n != 3 {
		t.Errorf("receipts = %d, want 3: nothing imported twice", n)
	}

	// Close with read, then with move.
	gmailDo(t, r.admin, http.MethodPut, "/api/v1/communications/channels/"+channelID.String(), map[string]any{"onClose": "read"}, http.StatusOK, nil)
	gmailDo(t, r.admin, http.MethodPatch, "/api/v1/communications/conversations/"+conv.String(), map[string]any{"status": "closed"}, http.StatusOK, nil)
	r.sync(t)
	for _, id := range []string{first.ID, next.ID} {
		if m, _ := r.g.GmailMessageOf(gmailPost, id); slices.Contains(m.Labels, "UNREAD") {
			t.Errorf("%s still unread: %v", id, m.Labels)
		}
	}
	gmailDo(t, r.admin, http.MethodPut, "/api/v1/communications/channels/"+channelID.String(), map[string]any{"onClose": "move"}, http.StatusOK, nil)
	r.Advance(10 * time.Minute)
	again := r.g.Deliver(gmailPost, customerMail("<a4@example.test>", "<a3@example.test>", "Takk!"), r.Now())
	r.sync(t)
	gmailDo(t, r.admin, http.MethodPatch, "/api/v1/communications/conversations/"+conv.String(), map[string]any{"status": "closed"}, http.StatusOK, nil)
	r.sync(t)
	label := r.g.GmailLabels(gmailPost)["Handled in Vantigo"]
	if m, _ := r.g.GmailMessageOf(gmailPost, again.ID); label == "" || !slices.Contains(m.Labels, label) || slices.Contains(m.Labels, "INBOX") {
		t.Errorf("moved: labels %v, want %q without INBOX", m.Labels, label)
	}
}
