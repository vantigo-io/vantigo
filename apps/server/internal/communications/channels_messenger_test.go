package communications_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is Messenger channels (Messenger design N1, delivery A): the
// channel type and its address rule, the provider that has to serve the type,
// the default channel of each type, and the mailbox sync never claiming a
// channel that is not an e-mail one. contracts.ChannelMessenger is a fake
// (fake_messenger_test.go): the Meta module is never composed here.

// pageIDCounter mints unique Page ids so parallel tests never collide on
// 409 channel_exists.
var pageIDCounter atomic.Uint64

func pageID() string { return fmt.Sprintf("1000%06d", pageIDCounter.Add(1)) }

// messengerChannelBody is a CreateChannelRequest for a Messenger channel on
// the fake provider.
func messengerChannelBody(address string) map[string]any {
	return map[string]any{"type": "messenger", "provider": fakeMessengerKey, "address": address}
}

// messengerChannelJSON is a channel with its sync.
type messengerChannelJSON struct {
	channelJSON
	channelSyncJSON
}

// newMessengerChannel connects a Page on fake and creates a channel on it,
// answering the channel.
func newMessengerChannel(t *testing.T, c *modtest.Client, fake *fakeMessenger) channelJSON {
	t.Helper()
	address := pageID()
	fake.AddPage(address, "Acme Page "+address)
	return createChannel(t, c, messengerChannelBody(address))
}

// TestCreateChannel_MessengerPage: a connected Page becomes an active channel of
// type messenger on the fake's key, named by the Page, with no credential row
// and no mailbox, the default of its type — the first of it —, and a sync row
// that starts at cursor 0.
func TestCreateChannel_MessengerPage(t *testing.T) {
	t.Parallel()
	h, fake := messengerHarness(t)
	c := h.SignIn(t, "communications:channels-manage")
	address := pageID()
	fake.AddPage(address, "Acme Page")

	r := c.Do(http.MethodPost, "/api/v1/communications/channels", messengerChannelBody(address))
	if r.Status != http.StatusCreated {
		t.Fatalf("create: status %d body %s, want 201", r.Status, r.Body)
	}
	var ch messengerChannelJSON
	r.JSON(&ch)
	if ch.Type != "messenger" || ch.Provider != fakeMessengerKey || ch.Address != address || !ch.IsActive || !ch.IsDefault {
		t.Errorf("channel = %+v, want an active default messenger channel on %s", ch, address)
	}
	if ch.DisplayName == nil || *ch.DisplayName != "Acme Page" {
		t.Errorf("displayName = %v, want the Page's name", ch.DisplayName)
	}
	if ch.HasCredentials || ch.Settings != nil || ch.Mailbox != nil {
		t.Errorf("hasCredentials %v settings %+v mailbox %+v, want none", ch.HasCredentials, ch.Settings, ch.Mailbox)
	}
	if ch.Available == nil || !*ch.Available {
		t.Errorf("available = %v, want true: the provider and the Page are available", ch.Available)
	}
	if ch.Sync == nil || ch.Sync.State != "active" {
		t.Errorf("sync = %+v, want active", ch.Sync)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.channel_credentials WHERE channel_id = $1`, ch.Id); n != 0 {
		t.Errorf("credential rows = %d, want 0", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.channel_sync WHERE channel_id = $1`, ch.Id); n != 0 {
		t.Errorf("mailbox sync rows = %d, want 0: a Messenger channel has the messenger sync's", n)
	}
	if got := modtest.One[int64](t, h, `SELECT cursor FROM communications.messenger_sync WHERE channel_id = $1`, ch.Id); got != 0 {
		t.Errorf("cursor = %d, want 0", got)
	}
	if got := modtest.One[string](t, h, `SELECT settings::text FROM communications.channels WHERE id = $1`, ch.Id); got != "{}" {
		t.Errorf("settings = %s, want {}", got)
	}
	// Status and Check make the channel.
	if got := strings.Join(fake.Calls(), ","); got != "Status,Check" {
		t.Errorf("calls = %s, want Status then Check", got)
	}

	read := c.Do(http.MethodGet, "/api/v1/communications/channels/"+ch.Id, nil)
	var got messengerChannelJSON
	read.JSON(&got)
	if got.Type != "messenger" || got.Available == nil || !*got.Available || got.Sync == nil {
		t.Errorf("GET = %+v, want the messenger channel, available, with its sync", got)
	}
}

// TestCreateChannel_MessengerAddressRule: a Messenger channel's address is a
// Page id, digits only and at most 30, whereas an e-mail channel's is an e-mail
// address; each type refuses the other's.
func TestCreateChannel_MessengerAddressRule(t *testing.T) {
	t.Parallel()
	h, _ := messengerHarness(t)
	c := h.SignIn(t, "communications:channels-manage")
	for name, address := range map[string]string{
		"an e-mail address": "page@example.test",
		"letters":           "12ab",
		"blank":             "",
		"31 digits":         strings.Repeat("1", 31),
		"a space":           "1234 5678",
	} {
		r := c.Do(http.MethodPost, "/api/v1/communications/channels", messengerChannelBody(address))
		wantCode(t, r, http.StatusBadRequest, "invalid_request")
		var body commErrorJSON
		r.JSON(&body)
		if got := body.field("address"); len(got) != 1 || got[0] != "A valid Facebook Page id is required." {
			t.Errorf("%s: address errors = %v, want the Page id sentence", name, got)
		}
	}
	// An e-mail channel does not take a Page id.
	body := newChannelBody("123456")
	r := c.Do(http.MethodPost, "/api/v1/communications/channels", body)
	wantCode(t, r, http.StatusBadRequest, "invalid_request")
}

// TestCreateChannel_MessengerRefusals: a Page the provider does not know, one
// that is unavailable, a provider that is off, one nobody fills, and one that
// does not answer are each a refusal with the mailbox's own shape.
func TestCreateChannel_MessengerRefusals(t *testing.T) {
	t.Parallel()
	h, fake := messengerHarness(t)
	c := h.SignIn(t, "communications:channels-manage")

	r := c.Do(http.MethodPost, "/api/v1/communications/channels", messengerChannelBody(pageID()))
	wantCode(t, r, http.StatusUnprocessableEntity, "endpoint_unknown")

	down := pageID()
	fake.AddPage(down, "Down")
	fake.SetPageUnavailable(down, "token_rejected")
	r = c.Do(http.MethodPost, "/api/v1/communications/channels", messengerChannelBody(down))
	wantCode(t, r, http.StatusUnprocessableEntity, "endpoint_unavailable")

	body := messengerChannelBody(pageID())
	body["provider"] = "nobody"
	r = c.Do(http.MethodPost, "/api/v1/communications/channels", body)
	wantCode(t, r, http.StatusConflict, "messenger_off")

	fake.SetStatus(false, "not_configured")
	r = c.Do(http.MethodPost, "/api/v1/communications/channels", messengerChannelBody(pageID()))
	wantCode(t, r, http.StatusConflict, "messenger_off")
	fake.SetStatus(true, "")

	fake.FailWith("Check", context.DeadlineExceeded)
	r = c.Do(http.MethodPost, "/api/v1/communications/channels", messengerChannelBody(pageID()))
	wantCode(t, r, http.StatusServiceUnavailable, "provider_unreachable")
	if n := h.Count(t, `SELECT count(*) FROM communications.channels`); n != 0 {
		t.Errorf("channels = %d, want none after the refusals", n)
	}
}

// TestCreateChannel_ProviderTypeMismatch: the provider has to serve the
// channel's type — a messenger provider for an e-mail channel, SMTP or a mailbox
// provider for a Messenger one, are each 422 provider_type_mismatch, whichever
// way the channel is made or changed.
func TestCreateChannel_ProviderTypeMismatch(t *testing.T) {
	t.Parallel()
	mailbox := modtest.NewFakeChannelMailbox()
	h, fake := messengerHarness(t, modtest.WithChannelMailbox(mailbox))
	c := h.SignIn(t, "communications:channels-manage")
	page := pageID()
	fake.AddPage(page, "Page")

	cases := map[string]map[string]any{
		"an e-mail channel on a messenger provider":  {"type": "email", "provider": fakeMessengerKey, "address": channelAddress(t)},
		"a messenger channel on smtp, by default":    {"type": "messenger", "address": page},
		"a messenger channel on smtp, named":         {"type": "messenger", "provider": "smtp", "address": page},
		"a messenger channel on a mailbox provider":  {"type": "messenger", "provider": "entra", "address": page},
		"a messenger channel on a mailbox, mixed in": {"type": "messenger", "provider": "ENTRA", "address": page},
	}
	for name, body := range cases {
		r := c.Do(http.MethodPost, "/api/v1/communications/channels", body)
		if r.Status != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status %d body %s, want 422", name, r.Status, r.Body)
		}
		var got commErrorJSON
		r.JSON(&got)
		if got.Error.Code != "provider_type_mismatch" {
			t.Errorf("%s: code = %q, want provider_type_mismatch", name, got.Error.Code)
		}
	}

	// A Messenger channel's provider cannot be changed or switched, and an
	// e-mail channel cannot be switched to a messenger provider.
	messenger := createChannel(t, c, messengerChannelBody(page))
	email := createChannel(t, c, newChannelBody(channelAddress(t)))
	r := c.Do(http.MethodPut, "/api/v1/communications/channels/"+messenger.Id, map[string]any{"provider": "smtp", "smtp": map[string]any{"host": "smtp.example.test", "port": 587, "password": "x"}})
	if r.Status != http.StatusBadRequest {
		t.Errorf("PUT a messenger channel to smtp: status %d body %s, want 400", r.Status, r.Body)
	}
	r = c.Do(http.MethodPost, "/api/v1/communications/channels/"+messenger.Id+"/provider", map[string]any{"provider": "entra"})
	wantCode(t, r, http.StatusUnprocessableEntity, "provider_type_mismatch")
	r = c.Do(http.MethodPost, "/api/v1/communications/channels/"+email.Id+"/provider", map[string]any{"provider": fakeMessengerKey})
	wantCode(t, r, http.StatusUnprocessableEntity, "provider_type_mismatch")
	r = c.Do(http.MethodPut, "/api/v1/communications/channels/"+email.Id, map[string]any{"provider": fakeMessengerKey})
	if r.Status != http.StatusBadRequest {
		t.Errorf("PUT an e-mail channel to a messenger provider: status %d body %s, want 400", r.Status, r.Body)
	}
}

// TestChannels_DefaultsArePerType: the default channel is the default of its
// type (design divergence 16): the first Messenger channel is the Messenger
// default while an e-mail default exists and keeps it, a second default of
// either type demotes only the first of its own type, and a PUT of isDefault
// does the same.
func TestChannels_DefaultsArePerType(t *testing.T) {
	t.Parallel()
	h, fake := messengerHarness(t)
	c := h.SignIn(t, "communications:channels-manage")
	defaultOf := func(channelType string) string {
		t.Helper()
		return modtest.One[string](t, h, `SELECT coalesce((SELECT id::text FROM communications.channels WHERE type = $1 AND is_default), '')`, channelType)
	}

	email1 := createChannel(t, c, newChannelBody(channelAddress(t)))
	messenger1 := newMessengerChannel(t, c, fake)
	if !email1.IsDefault || !messenger1.IsDefault {
		t.Fatalf("e-mail default %v, messenger default %v, want both: the first of each type", email1.IsDefault, messenger1.IsDefault)
	}
	if defaultOf("email") != email1.Id || defaultOf("messenger") != messenger1.Id {
		t.Errorf("the Messenger default demoted the e-mail default, or the reverse")
	}

	// A second, not default, of each type.
	email2 := createChannel(t, c, newChannelBody(channelAddress(t)))
	messenger2 := newMessengerChannel(t, c, fake)
	if email2.IsDefault || messenger2.IsDefault {
		t.Errorf("a second channel of a type was made default without asking")
	}

	// A Messenger default asked for demotes the Messenger default alone.
	body := messengerChannelBody(pageID())
	fake.AddPage(body["address"].(string), "Third")
	body["isDefault"] = true
	messenger3 := createChannel(t, c, body)
	if !messenger3.IsDefault || defaultOf("messenger") != messenger3.Id || defaultOf("email") != email1.Id {
		t.Errorf("after a third Messenger channel made default: messenger %s email %s, want %s and %s", defaultOf("messenger"), defaultOf("email"), messenger3.Id, email1.Id)
	}
	// An e-mail default asked for demotes the e-mail default alone.
	body = newChannelBody(channelAddress(t))
	body["isDefault"] = true
	email3 := createChannel(t, c, body)
	if defaultOf("email") != email3.Id || defaultOf("messenger") != messenger3.Id {
		t.Errorf("after a third e-mail channel made default: email %s messenger %s, want %s and %s", defaultOf("email"), defaultOf("messenger"), email3.Id, messenger3.Id)
	}

	// The same through a PUT.
	if r := c.Do(http.MethodPut, "/api/v1/communications/channels/"+messenger2.Id, map[string]any{"isDefault": true}); r.Status != http.StatusOK {
		t.Fatalf("PUT messenger default: status %d body %s", r.Status, r.Body)
	}
	if defaultOf("messenger") != messenger2.Id || defaultOf("email") != email3.Id {
		t.Errorf("after a PUT: messenger %s email %s, want %s and %s", defaultOf("messenger"), defaultOf("email"), messenger2.Id, email3.Id)
	}
	if r := c.Do(http.MethodPut, "/api/v1/communications/channels/"+email2.Id, map[string]any{"isDefault": true}); r.Status != http.StatusOK {
		t.Fatalf("PUT e-mail default: status %d body %s", r.Status, r.Body)
	}
	if defaultOf("email") != email2.Id || defaultOf("messenger") != messenger2.Id {
		t.Errorf("after a second PUT: email %s messenger %s, want %s and %s", defaultOf("email"), defaultOf("messenger"), email2.Id, messenger2.Id)
	}

	// A PUT of a Messenger channel leaves its settings as they were.
	if got := modtest.One[string](t, h, `SELECT settings::text FROM communications.channels WHERE id = $1`, messenger2.Id); got != "{}" {
		t.Errorf("settings after a PUT = %s, want {}", got)
	}
}

// TestMailboxSync_NeverClaimsAMessengerChannel: the mailbox cycle selects and
// claims e-mail channels on a mailbox provider only, so a Messenger channel is
// left alone even when a channel_sync row names it, due.
func TestMailboxSync_NeverClaimsAMessengerChannel(t *testing.T) {
	t.Parallel()
	h, fake := messengerHarness(t)
	c := h.SignIn(t, "communications:channels-manage")
	ch := newMessengerChannel(t, c, fake)
	h.Exec(t, `
		INSERT INTO communications.channel_sync (channel_id, folder, import_from, state, next_sync_at, updated_at)
		VALUES ($1, 'inbox', now() - interval '1 day', 'pending', now() - interval '1 hour', now())`, ch.Id)

	w := communications.NewMailboxSyncWorker(h.Deps())
	processed, err := w.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if processed {
		t.Error("the mailbox sync claimed a Messenger channel")
	}
	if got := modtest.One[string](t, h, `SELECT coalesce(lease_id, '') FROM communications.channel_sync WHERE channel_id = $1`, ch.Id); got != "" {
		t.Errorf("the channel_sync row of a Messenger channel was leased: %q", got)
	}
}

// TestChannelProviders_ListsMessengerProviders: GET /channel-providers lists
// the messenger provider after SMTP, with its type, its status and the Pages it
// has connected, each with its own; and the e-mail providers carry type email.
func TestChannelProviders_ListsMessengerProviders(t *testing.T) {
	t.Parallel()
	h, fake := messengerHarness(t, modtest.WithChannelMailbox(modtest.NewFakeChannelMailbox()))
	c := h.SignIn(t, "communications:channels-manage")
	fake.AddPage("111", "First")
	fake.AddPage("222", "Second")
	fake.SetPageUnavailable("222", "token_rejected")

	type endpoint struct {
		Address     string  `json:"address"`
		DisplayName string  `json:"displayName"`
		Available   bool    `json:"available"`
		Reason      *string `json:"reason"`
	}
	type provider struct {
		Key       string      `json:"key"`
		Type      string      `json:"type"`
		Available bool        `json:"available"`
		Reason    *string     `json:"reason"`
		Endpoints *[]endpoint `json:"endpoints"`
	}
	list := func() []provider {
		r := c.Do(http.MethodGet, "/api/v1/communications/channel-providers", nil)
		if r.Status != http.StatusOK {
			t.Fatalf("status %d body %s", r.Status, r.Body)
		}
		var out []provider
		r.JSON(&out)
		return out
	}
	got := list()
	if len(got) != 3 || got[0].Key != "smtp" || got[0].Type != "email" || got[1].Type != "email" || got[2].Key != fakeMessengerKey || got[2].Type != "messenger" {
		t.Fatalf("providers = %+v, want smtp, the mailbox provider, then the messenger provider", got)
	}
	if got[0].Endpoints != nil || got[1].Endpoints != nil {
		t.Errorf("an e-mail provider lists Pages: %+v", got)
	}
	m := got[2]
	if !m.Available || m.Endpoints == nil || len(*m.Endpoints) != 2 {
		t.Fatalf("messenger provider = %+v, want available with two Pages", m)
	}
	first, second := (*m.Endpoints)[0], (*m.Endpoints)[1]
	if first.Address != "111" || !first.Available || first.Reason != nil ||
		second.Address != "222" || second.Available || second.Reason == nil || *second.Reason != "token_rejected" {
		t.Errorf("Pages = %+v %+v, want 111 available and 222 refused as token_rejected", first, second)
	}

	fake.SetStatus(false, "not_configured")
	m = list()[2]
	if m.Available || m.Reason == nil || *m.Reason != "not_configured" || m.Endpoints != nil {
		t.Errorf("an unavailable messenger provider = %+v, want not_configured and no Pages", m)
	}
}

// TestChannels_MessengerAvailabilityFollowsTheProviderAndThePage: a Messenger
// channel is available while the provider is offered and its Page is available.
func TestChannels_MessengerAvailabilityFollowsTheProviderAndThePage(t *testing.T) {
	t.Parallel()
	h, fake := messengerHarness(t)
	c := h.SignIn(t, "communications:channels-manage")
	ch := newMessengerChannel(t, c, fake)
	available := func() bool {
		r := c.Do(http.MethodGet, "/api/v1/communications/channels/"+ch.Id, nil)
		var got channelJSON
		r.JSON(&got)
		return got.Available != nil && *got.Available
	}
	if !available() {
		t.Fatal("a fresh Messenger channel is not available")
	}
	fake.SetPageUnavailable(ch.Address, "removed")
	if available() {
		t.Error("available with its Page unavailable")
	}
	fake.AddPage(ch.Address, "Back")
	fake.SetStatus(false, "not_configured")
	if available() {
		t.Error("available with its provider unavailable")
	}
	fake.SetStatus(true, "")
	if !available() {
		t.Error("not available again once both are")
	}
}

// TestVerifyChannel_Messenger: verify asks the provider again, ok while the
// Page is connected and available, a 422 with the create's code otherwise —
// verify answers every refusal 422.
func TestVerifyChannel_Messenger(t *testing.T) {
	t.Parallel()
	h, fake := messengerHarness(t)
	c := h.SignIn(t, "communications:channels-manage")
	ch := newMessengerChannel(t, c, fake)

	r := c.Do(http.MethodPost, "/api/v1/communications/channels/"+ch.Id+"/verify", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("verify: status %d body %s, want 200", r.Status, r.Body)
	}
	fake.SetPageUnavailable(ch.Address, "token_rejected")
	r = c.Do(http.MethodPost, "/api/v1/communications/channels/"+ch.Id+"/verify", nil)
	wantCode(t, r, http.StatusUnprocessableEntity, "endpoint_unavailable")
	fake.SetStatus(false, "not_configured")
	r = c.Do(http.MethodPost, "/api/v1/communications/channels/"+ch.Id+"/verify", nil)
	wantCode(t, r, http.StatusUnprocessableEntity, "messenger_off")
}

// TestCreateConversation_RefusesAMessengerChannel: a conversation on a
// Messenger channel starts when the person writes first, so create answers 422
// channel_cannot_start for one — and with no channel named still uses the
// default e-mail channel, never the Messenger default.
func TestCreateConversation_RefusesAMessengerChannel(t *testing.T) {
	t.Parallel()
	h, fake := messengerHarness(t)
	c := h.SignIn(t, "communications:channels-manage", "communications:conversations-reply", "communications:conversations-view")
	messenger := newMessengerChannel(t, c, fake)

	key := uuid.NewString()
	r := c.Do(http.MethodPost, "/api/v1/communications/conversations", map[string]any{
		"channelId": messenger.Id, "subject": "Hello", "textBody": "Hi",
		"recipients": []map[string]any{{"address": "x@example.test", "type": "to"}},
	}, modtest.Header("Idempotency-Key", key))
	wantCode(t, r, http.StatusUnprocessableEntity, "channel_cannot_start")

	// With no e-mail channel, the default is not the Messenger one.
	r = c.Do(http.MethodPost, "/api/v1/communications/conversations", map[string]any{
		"subject": "Hello", "textBody": "Hi",
		"recipients": []map[string]any{{"address": "x@example.test", "type": "to"}},
	}, modtest.Header("Idempotency-Key", uuid.NewString()))
	wantCode(t, r, http.StatusUnprocessableEntity, "channel_invalid")
}
