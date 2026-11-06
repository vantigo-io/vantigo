package integration_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/meta"
	"github.com/vantigo-io/vantigo/server/internal/meta/metatest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Facebook Messenger through Communications' own messenger sync and outbox
// (Messenger design N2, N5, N7, N11, N12, delivery B): the real communications,
// customers and meta modules composed together, Communications picking Meta's
// ChannelMessenger by the channel's provider as cmd/vantigo does, against the
// fake Graph API. Nothing is imitated but Meta.

var metaRecorder = mergedRecorder("communications", "customers", "meta")

const (
	metaPageID    = "1111111111"
	metaPageToken = "page-token-1"
	metaSecret    = "0123456789abcdef0123456789abcdef"
	metaPSID      = "4242424242"
)

type metaRig struct {
	*modtest.Harness
	graph *metatest.FakeGraph
	admin *modtest.Client
}

func newMetaRig(t *testing.T) *metaRig {
	t.Helper()
	g := metatest.NewFakeGraph()
	g.AddPage(metaPageID, "Nordlys", metaPageToken, "MESSAGING")
	g.AddUser(metaPSID, "Kari Nordmann")
	h := modtest.New(t,
		modtest.WithRecorder(metaRecorder),
		modtest.WithModule(customers.Module()),
		modtest.WithModule(communications.Module()),
		modtest.WithModule(meta.Module()),
		modtest.WithTransport(g),
		modtest.WithBackoff(func(int) time.Duration { return 0 }),
		modtest.WithEnv("PUBLIC_BASE_URL", "https://public.example.com"),
		modtest.WithEnv("STORAGE_PROVIDER", "fs"),
		modtest.WithEnv("STORAGE_FS_ROOT", t.TempDir()),
		modtest.WithEnv("STORAGE_FS_ALLOW_INSECURE_ROOT", "1"),
	)
	owner, ownerID := h.SignInUser(t)
	h.Exec(t, `INSERT INTO identity.user_roles (user_id, role_id) SELECT $1, id FROM identity.roles WHERE name = 'Owner'`, ownerID)
	gmailDo(t, owner, http.MethodPut, "/api/v1/meta/settings", map[string]any{"appId": "1234567890", "appSecret": metaSecret}, http.StatusOK, nil)
	gmailDo(t, owner, http.MethodPost, "/api/v1/meta/pages", map[string]any{"accessToken": metaPageToken}, http.StatusCreated, nil)
	admin := h.SignIn(t, "communications:channels-manage", "communications:conversations-view",
		"communications:conversations-manage", "communications:conversations-reply")
	return &metaRig{Harness: h, graph: g, admin: admin}
}

// webhook posts body to the public webhook, signed as Meta signs it.
func (r *metaRig) webhook(t *testing.T, body any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte(metaSecret))
	mac.Write(raw)
	resp := r.Client(t).Do(http.MethodPost, "/api/v1/meta/webhook", nil,
		modtest.RawBody("application/octet-stream", raw),
		modtest.Header("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil))))
	if resp.Status != http.StatusOK {
		t.Fatalf("webhook: %d %s", resp.Status, resp.Body)
	}
}

// messaging is one webhook post carrying one messaging item for the Page.
func messaging(item map[string]any) map[string]any {
	return map[string]any{"object": "page", "entry": []any{map[string]any{
		"id": metaPageID, "time": 0, "messaging": []any{item},
	}}}
}

func metaWorker[W any](t *testing.T, r *metaRig) W {
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

// drain runs the splitting worker, then Communications' messenger sync, and
// finally its outbox, until each is out of work.
func (r *metaRig) drain(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if err := metaWorker[*meta.SplitWorker](t, r).RunOnce(ctx); err != nil {
		t.Fatalf("meta-split: %v", err)
	}
	sync := metaWorker[*communications.MessengerSyncWorker](t, r)
	for range 20 {
		ok, err := sync.ProcessOne(ctx)
		if err != nil {
			t.Fatalf("messenger sync: %v", err)
		}
		if !ok {
			break
		}
	}
	out := metaWorker[*communications.OutboxWorker](t, r)
	for range 20 {
		ok, err := out.ProcessOne(ctx)
		if err != nil {
			t.Fatalf("outbox: %v", err)
		}
		if !ok {
			break
		}
	}
}

func TestMetaMessenger_InboundBecomesAConversationAndAReplyIsSent(t *testing.T) {
	t.Parallel()
	r := newMetaRig(t)

	var providers []struct {
		Key         string `json:"key"`
		DisplayName string `json:"displayName"`
		Available   bool   `json:"available"`
	}
	gmailDo(t, r.admin, http.MethodGet, "/api/v1/communications/channel-providers", nil, http.StatusOK, &providers)
	found := false
	for _, p := range providers {
		if p.Key == "meta" && p.DisplayName == "Facebook Messenger" && p.Available {
			found = true
		}
	}
	if !found {
		t.Fatalf("providers = %+v, want Facebook Messenger available", providers)
	}
	var ch struct {
		ID string `json:"id"`
	}
	gmailDo(t, r.admin, http.MethodPost, "/api/v1/communications/channels",
		map[string]any{"type": "messenger", "provider": "meta", "address": metaPageID}, http.StatusCreated, &ch)

	r.Advance(time.Minute)
	r.webhook(t, messaging(map[string]any{
		"sender": map[string]any{"id": metaPSID}, "recipient": map[string]any{"id": metaPageID},
		"timestamp": r.Now().UnixMilli(),
		"message":   map[string]any{"mid": "m_in_1", "text": "Hei, jeg lurer på avtalen."},
	}))
	r.drain(t)

	conv := modtest.One[uuid.UUID](t, r.Harness, `SELECT m.conversation_id FROM communications.inbound_receipts r JOIN communications.conversation_messages m ON m.id = r.message_id WHERE r.provider_message_id = 'm_in_1'`)
	if name := modtest.One[string](t, r.Harness, `SELECT coalesce(display_name, '') FROM communications.participants WHERE channel_id = $1 AND address = $2`, ch.ID, metaPSID); name != "Kari Nordmann" {
		t.Errorf("participant name = %q, want the profile's", name)
	}

	// The reply, with answer options, leaves through Send.
	var queued struct {
		MessageID *string `json:"messageId"`
	}
	gmailDo(t, r.admin, http.MethodPost, "/api/v1/communications/conversations/"+conv.String()+"/reply",
		map[string]any{"textBody": "Skal vi ringe deg?", "components": map[string]any{
			"answerOptions": []map[string]any{{"id": "yes", "title": "Ja"}, {"id": "no", "title": "Nei"}},
		}}, http.StatusCreated, &queued, modtest.Header("Idempotency-Key", uuid.NewString()))
	r.drain(t)
	sent := r.graph.Sent()
	if len(sent) != 1 || sent[0].Recipient != metaPSID || sent[0].Text != "Skal vi ringe deg?" || sent[0].MessagingType != "RESPONSE" {
		t.Fatalf("sent to Meta = %+v", sent)
	}
	payload := fmt.Sprintf("v1:%s:yes", *queued.MessageID)
	if !strings.Contains(fmt.Sprint(sent[0].Message), payload) {
		t.Errorf("the quick replies do not carry %q: %v", payload, sent[0].Message)
	}
	if status := modtest.One[string](t, r.Harness, `SELECT status FROM communications.outbox_jobs WHERE message_id = $1`, *queued.MessageID); status != "completed" {
		t.Errorf("the reply's job = %s, want completed", status)
	}

	// The tap comes back as an answer; an echo of our own send is ignored.
	r.webhook(t, messaging(map[string]any{
		"sender": map[string]any{"id": metaPSID}, "recipient": map[string]any{"id": metaPageID},
		"timestamp": r.Now().UnixMilli(),
		"message":   map[string]any{"mid": "m_in_2", "text": "Ja", "quick_reply": map[string]any{"payload": payload}},
	}))
	r.webhook(t, messaging(map[string]any{
		"sender": map[string]any{"id": metaPageID}, "recipient": map[string]any{"id": metaPSID},
		"timestamp": r.Now().UnixMilli(),
		"message":   map[string]any{"mid": "m_1", "text": "Skal vi ringe deg?", "is_echo": true},
	}))
	r.drain(t)
	if n := r.Count(t, `SELECT count(*) FROM communications.conversation_messages WHERE conversation_id = $1 AND channel_metadata_json::text LIKE '%"yes"%' AND direction = 'inbound'`, conv); n != 1 {
		t.Errorf("answers stored = %d, want 1", n)
	}
	if n := r.Count(t, `SELECT count(*) FROM communications.conversation_messages WHERE conversation_id = $1`, conv); n != 3 {
		t.Errorf("messages = %d, want the question, the reply and the answer (the echo of our own is ignored)", n)
	}
}
