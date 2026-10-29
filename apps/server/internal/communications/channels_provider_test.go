package communications_test

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is delivery C's Microsoft 365 channels (Outlook connection
// design M12; plan reading 13): a channel created by a shared mailbox's
// address through contracts.ChannelMailbox, an SMTP channel switched to one
// and back, verify, GET /channel-providers and the rate limit their Graph
// reads share. Entra is never composed here: modtest.FakeChannelMailbox
// stands in for its provider. Every harness's own clean-up fails the test if
// the slot was called from inside one of the module's transactions
// (harness_test.go).

// mailboxHarness is a communications installation with fake as its mail
// channels provider.
func mailboxHarness(t *testing.T) (*modtest.Harness, *modtest.FakeChannelMailbox) {
	t.Helper()
	fake := modtest.NewFakeChannelMailbox()
	return newHarness(t, modtest.WithChannelMailbox(fake)), fake
}

// sharedMailbox makes fake answer address with a shared mailbox of a fresh
// object id, its primary address primary (address itself when "").
func sharedMailbox(fake *modtest.FakeChannelMailbox, address, primary string) contracts.ChannelMailboxInfo {
	if primary == "" {
		primary = strings.ToLower(address)
	}
	info := contracts.ChannelMailboxInfo{
		Mailbox: uuid.NewString(), Address: primary, DisplayName: "Post", Shared: true,
	}
	fake.SetMailbox(address, info)
	return info
}

// entraChannelBody is a CreateChannelRequest for a Microsoft 365 channel.
func entraChannelBody(address string) map[string]any {
	return map[string]any{"type": "email", "provider": "entra", "address": address}
}

// switchProvider posts the switch of provider for channel id.
func switchProvider(c *modtest.Client, id string) *modtest.Response {
	return c.Do(http.MethodPost, "/api/v1/communications/channels/"+id+"/provider", map[string]any{"provider": "entra"})
}

// wantCode fails t unless r answered status with the error code.
func wantCode(t *testing.T, r *modtest.Response, status int, code string) {
	t.Helper()
	if r.Status != status {
		t.Fatalf("status %d body %s, want %d %s", r.Status, r.Body, status, code)
	}
	var body commErrorJSON
	r.JSON(&body)
	if body.Error.Code != code {
		t.Errorf("code = %q (%s), want %q", body.Error.Code, body.Error.Message, code)
	}
}

// TestCreateChannel_EntraSharedMailbox: a shared mailbox becomes an active
// channel on provider entra with the mailbox in channels.settings, the
// address Check answered (an alias typed becomes the mailbox's own), no
// credential row, and — the first channel — the default.
func TestCreateChannel_EntraSharedMailbox(t *testing.T) {
	t.Parallel()
	h, fake := mailboxHarness(t)
	c := h.SignIn(t, "communications:channels-manage")
	typed := channelAddress(t)
	info := sharedMailbox(fake, typed, "post-"+uuid.NewString()[:8]+"@example.test")

	body := entraChannelBody(typed)
	body["displayName"] = "Post"
	ch := createChannel(t, c, body)
	if ch.Provider != "entra" || !ch.IsActive || !ch.IsDefault {
		t.Errorf("provider %q active %v default %v, want entra, active, the default", ch.Provider, ch.IsActive, ch.IsDefault)
	}
	if ch.Address != info.Address {
		t.Errorf("address = %q, want Check's %q", ch.Address, info.Address)
	}
	if ch.Mailbox == nil || ch.Mailbox.Id != info.Mailbox {
		t.Errorf("mailbox = %+v, want %s", ch.Mailbox, info.Mailbox)
	}
	if ch.HasCredentials || ch.Settings != nil {
		t.Errorf("hasCredentials %v settings %+v, want false and null", ch.HasCredentials, ch.Settings)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.channel_credentials WHERE channel_id = $1`, ch.Id); n != 0 {
		t.Errorf("credential rows = %d, want 0", n)
	}
	if got := modtest.One[string](t, h, `SELECT settings->>'mailbox' FROM communications.channels WHERE id = $1`, ch.Id); got != info.Mailbox {
		t.Errorf("settings.mailbox = %q, want %q", got, info.Mailbox)
	}

	got := c.Do(http.MethodGet, "/api/v1/communications/channels/"+ch.Id, nil)
	var read channelJSON
	got.JSON(&read)
	if read.Mailbox == nil || read.Mailbox.Id != info.Mailbox || read.HasCredentials {
		t.Errorf("GET: mailbox %+v hasCredentials %v, want %s and false", read.Mailbox, read.HasCredentials, info.Mailbox)
	}
	var methods []string
	for _, call := range fake.Calls() {
		methods = append(methods, call.Method)
	}
	if strings.Join(methods, ",") != "Status,Check" {
		t.Errorf("calls = %v, want Status then Check", methods)
	}
}

// TestCreateChannel_EntraRefusals: each answer of Status and Check, and no
// channel written for any of them.
func TestCreateChannel_EntraRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		shape  func(f *modtest.FakeChannelMailbox, address string)
		status int
		code   string
	}{
		{"a personal mailbox", func(f *modtest.FakeChannelMailbox, a string) {
			f.SetMailbox(a, contracts.ChannelMailboxInfo{Mailbox: uuid.NewString(), Address: a, Shared: false})
		}, http.StatusUnprocessableEntity, "personal_mailbox"},
		{"outside the scope", func(f *modtest.FakeChannelMailbox, _ string) {
			f.FailWith("Check", contracts.ErrMailboxDenied)
		}, http.StatusUnprocessableEntity, "mailbox_denied"},
		{"no such mailbox", func(*modtest.FakeChannelMailbox, string) {}, http.StatusUnprocessableEntity, "mailbox_unknown"},
		{"a user without a mailbox", func(f *modtest.FakeChannelMailbox, _ string) {
			f.FailWith("Check", contracts.ErrMailboxGone)
		}, http.StatusUnprocessableEntity, "mailbox_unknown"},
		{"the switch off", func(f *modtest.FakeChannelMailbox, _ string) {
			f.SetStatus(contracts.MailboxUnavailable, "switch_off")
		}, http.StatusConflict, "mail_channels_off"},
		{"Check unavailable", func(f *modtest.FakeChannelMailbox, _ string) {
			f.FailWith("Check", contracts.ErrMailboxUnavailable)
		}, http.StatusConflict, "mail_channels_off"},
		{"Graph not answering", func(f *modtest.FakeChannelMailbox, _ string) {
			f.FailWith("Check", errors.New("graph: throttled"))
		}, http.StatusServiceUnavailable, "provider_unreachable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h, fake := mailboxHarness(t)
			c := h.SignIn(t, "communications:channels-manage")
			address := channelAddress(t)
			tc.shape(fake, address)
			wantCode(t, c.Do(http.MethodPost, "/api/v1/communications/channels", entraChannelBody(address)), tc.status, tc.code)
			if n := h.Count(t, `SELECT count(*) FROM communications.channels`); n != 0 {
				t.Errorf("channels = %d, want none", n)
			}
		})
	}
}

// TestCreateChannel_EntraWithoutTheProvider: no slot filled, or one under
// another key, is mail_channels_off — never a channel on a provider that
// cannot send.
func TestCreateChannel_EntraWithoutTheProvider(t *testing.T) {
	t.Parallel()
	for name, opts := range map[string][]modtest.Option{
		"no slot":     nil,
		"another key": {modtest.WithChannelMailbox(modtest.NewFakeChannelMailbox().WithKey("other"))},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, opts...)
			c := h.SignIn(t, "communications:channels-manage")
			wantCode(t, c.Do(http.MethodPost, "/api/v1/communications/channels", entraChannelBody(channelAddress(t))),
				http.StatusConflict, "mail_channels_off")
		})
	}
}

// TestCreateChannel_EntraRefusesAnSmtpBlock: SMTP credentials on a
// Microsoft 365 channel are a 400, before anything reaches the provider.
func TestCreateChannel_EntraRefusesAnSmtpBlock(t *testing.T) {
	t.Parallel()
	h, fake := mailboxHarness(t)
	c := h.SignIn(t, "communications:channels-manage")
	body := entraChannelBody(channelAddress(t))
	body["smtp"] = map[string]any{"host": "smtp.example.test", "port": 587}
	r := c.Do(http.MethodPost, "/api/v1/communications/channels", body)
	wantCode(t, r, http.StatusBadRequest, "invalid_request")
	var e commErrorJSON
	r.JSON(&e)
	if got := e.field("smtp"); len(got) != 1 || got[0] != "SMTP credentials require the smtp provider." {
		t.Errorf("fields[smtp] = %v", got)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("calls = %v, want none", calls)
	}
}

// TestCreateChannel_EntraOneChannelPerMailbox: a second channel on the same
// mailbox is 409 channel_exists, whether by the same primary address or by
// an alias Check answers with another (ux_channels_entra_mailbox).
func TestCreateChannel_EntraOneChannelPerMailbox(t *testing.T) {
	t.Parallel()
	h, fake := mailboxHarness(t)
	c := h.SignIn(t, "communications:channels-manage")
	address := channelAddress(t)
	info := sharedMailbox(fake, address, "")
	createChannel(t, c, entraChannelBody(address))

	wantCode(t, c.Do(http.MethodPost, "/api/v1/communications/channels", entraChannelBody(address)),
		http.StatusConflict, "channel_exists")

	alias := channelAddress(t)
	other := info
	other.Address = strings.ToLower(alias)
	fake.SetMailbox(alias, other)
	r := c.Do(http.MethodPost, "/api/v1/communications/channels", entraChannelBody(alias))
	wantCode(t, r, http.StatusConflict, "channel_exists")
	var e commErrorJSON
	r.JSON(&e)
	if e.Error.Message != "A channel for this mailbox already exists." {
		t.Errorf("message = %q", e.Error.Message)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.channels`); n != 1 {
		t.Errorf("channels = %d, want 1", n)
	}
}

// TestSwitchChannelProvider_KeepsTheChannel: an SMTP channel switched to
// Microsoft 365 keeps its id, address, conversations and default flag; its
// credential row is gone.
func TestSwitchChannelProvider_KeepsTheChannel(t *testing.T) {
	t.Parallel()
	h, fake := mailboxHarness(t)
	admin := h.SignIn(t, "communications:channels-manage")
	ch := createChannel(t, admin, newChannelBody(channelAddress(t)))
	writer := h.SignIn(t, "communications:conversations-reply")
	createConversation(t, writer, newConversationBody("someone@example.test"))
	info := sharedMailbox(fake, ch.Address, "")

	r := switchProvider(admin, ch.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var got channelJSON
	r.JSON(&got)
	if got.Id != ch.Id || got.Address != ch.Address || !got.IsDefault || got.Provider != "entra" {
		t.Errorf("switched = %+v, want the same id, address and default on entra", got)
	}
	if got.Mailbox == nil || got.Mailbox.Id != info.Mailbox || got.HasCredentials || got.Settings != nil {
		t.Errorf("mailbox %+v hasCredentials %v settings %+v", got.Mailbox, got.HasCredentials, got.Settings)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.channel_credentials WHERE channel_id = $1`, ch.Id); n != 0 {
		t.Errorf("credential rows = %d, want 0", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.conversations WHERE channel_id = $1`, ch.Id); n != 1 {
		t.Errorf("conversations on the channel = %d, want 1", n)
	}

	wantCode(t, switchProvider(admin, ch.Id), http.StatusUnprocessableEntity, "provider_unchanged")
}

// TestSwitchChannelProvider_Refusals: the request's shape, the 404, and
// Check's answers on the channel's own address, each leaving the SMTP
// channel and its credential as they were.
func TestSwitchChannelProvider_Refusals(t *testing.T) {
	t.Parallel()
	h, fake := mailboxHarness(t)
	admin := h.SignIn(t, "communications:channels-manage")

	if r := admin.Do(http.MethodPost, "/api/v1/communications/channels/"+uuid.NewString()+"/provider",
		map[string]any{"provider": "entra"}); r.Status != http.StatusNotFound {
		t.Errorf("unknown channel: %d, want 404", r.Status)
	}
	ch := createChannel(t, admin, newChannelBody(channelAddress(t)))
	wantCode(t, admin.Do(http.MethodPost, "/api/v1/communications/channels/"+ch.Id+"/provider",
		map[string]any{"provider": "smtp"}), http.StatusBadRequest, "invalid_request")

	wantCode(t, switchProvider(admin, ch.Id), http.StatusUnprocessableEntity, "mailbox_unknown")
	fake.SetMailbox(ch.Address, contracts.ChannelMailboxInfo{Mailbox: uuid.NewString(), Address: ch.Address})
	wantCode(t, switchProvider(admin, ch.Id), http.StatusUnprocessableEntity, "personal_mailbox")
	fake.FailWith("Check", contracts.ErrMailboxDenied)
	wantCode(t, switchProvider(admin, ch.Id), http.StatusUnprocessableEntity, "mailbox_denied")
	fake.FailWith("Check", errors.New("graph: 503"))
	wantCode(t, switchProvider(admin, ch.Id), http.StatusServiceUnavailable, "provider_unreachable")
	fake.FailWith("Check", nil)
	fake.SetStatus(contracts.MailboxUnavailable, "switch_off")
	wantCode(t, switchProvider(admin, ch.Id), http.StatusConflict, "mail_channels_off")

	if p := modtest.One[string](t, h, `SELECT provider FROM communications.channels WHERE id = $1`, ch.Id); p != "smtp" {
		t.Errorf("provider = %q, want smtp", p)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.channel_credentials WHERE channel_id = $1`, ch.Id); n != 1 {
		t.Errorf("credential rows = %d, want 1", n)
	}
}

// TestSwitchChannelProvider_OneTransaction: the provider's update and the
// credential's delete are one transaction — a failure injected at the
// delete, after the update, leaves both as they were.
func TestSwitchChannelProvider_OneTransaction(t *testing.T) {
	t.Parallel()
	h, fake := mailboxHarness(t)
	admin := h.SignIn(t, "communications:channels-manage")
	ch := createChannel(t, admin, newChannelBody(channelAddress(t)))
	sharedMailbox(fake, ch.Address, "")
	h.Exec(t, `CREATE FUNCTION communications.refuse_credential_delete() RETURNS trigger LANGUAGE plpgsql AS
		$$ BEGIN RAISE EXCEPTION 'injected'; END $$`)
	h.Exec(t, `CREATE TRIGGER refuse_credential_delete BEFORE DELETE ON communications.channel_credentials
		FOR EACH ROW EXECUTE FUNCTION communications.refuse_credential_delete()`)

	r := admin.Do(http.MethodPost, "/api/v1/communications/channels/"+ch.Id+"/provider",
		map[string]any{"provider": "entra"}, modtest.SkipContract("an injected database failure answers 500"))
	if r.Status != http.StatusInternalServerError {
		t.Fatalf("status %d body %s, want 500", r.Status, r.Body)
	}
	if p := modtest.One[string](t, h, `SELECT provider || ' ' || settings::text FROM communications.channels WHERE id = $1`, ch.Id); p != "smtp {}" {
		t.Errorf("channel = %q, want smtp {} (the update rolled back)", p)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.channel_credentials WHERE channel_id = $1`, ch.Id); n != 1 {
		t.Errorf("credential rows = %d, want 1", n)
	}
}

// TestUpdateChannel_EntraBackToSmtp: a PUT with provider smtp and a full
// SMTP block writes the credential, clears the mailbox and, after commit,
// forgets it; without a password it is refused.
func TestUpdateChannel_EntraBackToSmtp(t *testing.T) {
	t.Parallel()
	h, fake := mailboxHarness(t)
	c := h.SignIn(t, "communications:channels-manage")
	address := channelAddress(t)
	info := sharedMailbox(fake, address, "")
	ch := createChannel(t, c, entraChannelBody(address))

	r := c.Do(http.MethodPut, "/api/v1/communications/channels/"+ch.Id, map[string]any{
		"provider": "smtp", "smtp": map[string]any{"host": "smtp.example.test", "port": 587},
	})
	wantCode(t, r, http.StatusBadRequest, "invalid_request")
	var e commErrorJSON
	r.JSON(&e)
	if got := e.field("smtp"); len(got) != 1 || got[0] != "A password is required to switch a channel to SMTP." {
		t.Errorf("fields[smtp] = %v", got)
	}

	r = c.Do(http.MethodPut, "/api/v1/communications/channels/"+ch.Id, map[string]any{
		"provider": "smtp", "smtp": map[string]any{"host": "smtp.example.test", "port": 587, "password": "pw"},
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var got channelJSON
	r.JSON(&got)
	if got.Provider != "smtp" || got.Mailbox != nil || !got.HasCredentials || got.Id != ch.Id {
		t.Errorf("back = %+v, want smtp with credentials and no mailbox", got)
	}
	if s := modtest.One[string](t, h, `SELECT settings::text FROM communications.channels WHERE id = $1`, ch.Id); s != "{}" {
		t.Errorf("settings = %s, want {}", s)
	}
	if f := fake.Forgotten(); len(f) != 1 || f[0] != info.Mailbox {
		t.Errorf("forgotten = %v, want [%s]", f, info.Mailbox)
	}
}

// TestUpdateChannel_EntraBackToSmtpKeepsTheAnswerWhenForgetFails: Forget is
// after commit and best-effort — a failure is logged and the 200 stands.
func TestUpdateChannel_EntraBackToSmtpKeepsTheAnswerWhenForgetFails(t *testing.T) {
	t.Parallel()
	h, fake := mailboxHarness(t)
	c := h.SignIn(t, "communications:channels-manage")
	address := channelAddress(t)
	sharedMailbox(fake, address, "")
	ch := createChannel(t, c, entraChannelBody(address))
	fake.FailWith("Forget", errors.New("database down"))

	r := c.Do(http.MethodPut, "/api/v1/communications/channels/"+ch.Id, map[string]any{
		"provider": "smtp", "smtp": map[string]any{"host": "smtp.example.test", "port": 587, "password": "pw"},
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	if !strings.Contains(h.Logs(), "communications: forget the channel's mailbox") {
		t.Errorf("logs hold no Forget failure:\n%s", h.Logs())
	}
}

// TestUpdateChannel_EntraKeepsItsProvider: a PUT without provider keeps the
// channel's own — isActive alone needs no credential — and provider entra on
// an SMTP channel is refused (the switch is its own operation).
func TestUpdateChannel_EntraKeepsItsProvider(t *testing.T) {
	t.Parallel()
	h, fake := mailboxHarness(t)
	c := h.SignIn(t, "communications:channels-manage")
	address := channelAddress(t)
	sharedMailbox(fake, address, "")
	ch := createChannel(t, c, entraChannelBody(address))

	r := c.Do(http.MethodPut, "/api/v1/communications/channels/"+ch.Id, map[string]any{"isActive": false})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var got channelJSON
	r.JSON(&got)
	if got.IsActive || got.Provider != "entra" || got.Mailbox == nil {
		t.Errorf("updated = %+v, want inactive and still entra", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.channel_credentials WHERE channel_id = $1`, ch.Id); n != 0 {
		t.Errorf("credential rows = %d, want 0", n)
	}

	smtpCh := createChannel(t, c, newChannelBody(channelAddress(t)))
	r = c.Do(http.MethodPut, "/api/v1/communications/channels/"+smtpCh.Id, map[string]any{"provider": "entra"})
	wantCode(t, r, http.StatusBadRequest, "invalid_request")
	var e commErrorJSON
	r.JSON(&e)
	if len(e.field("provider")) != 1 {
		t.Errorf("fields = %v, want one on provider", e.Error.Fields)
	}
}

// TestVerifyChannel_Entra: verify runs Check again on the channel's address:
// ok while it reaches the same shared mailbox, else the table's code with
// 422, mailbox_changed when the address names another mailbox now.
func TestVerifyChannel_Entra(t *testing.T) {
	t.Parallel()
	h, fake := mailboxHarness(t)
	c := h.SignIn(t, "communications:channels-manage")
	address := channelAddress(t)
	info := sharedMailbox(fake, address, "")
	ch := createChannel(t, c, entraChannelBody(address))
	verify := func() *modtest.Response {
		return c.Do(http.MethodPost, "/api/v1/communications/channels/"+ch.Id+"/verify", nil)
	}

	r := verify()
	if r.Status != http.StatusOK || !strings.Contains(string(r.Body), `"ok":true`) {
		t.Fatalf("status %d body %s, want 200 ok", r.Status, r.Body)
	}

	moved := info
	moved.Mailbox = uuid.NewString()
	fake.SetMailbox(address, moved)
	wantCode(t, verify(), http.StatusUnprocessableEntity, "mailbox_changed")

	personal := info
	personal.Shared = false
	fake.SetMailbox(address, personal)
	wantCode(t, verify(), http.StatusUnprocessableEntity, "personal_mailbox")

	fake.FailWith("Check", contracts.ErrMailboxDenied)
	wantCode(t, verify(), http.StatusUnprocessableEntity, "mailbox_denied")
	fake.FailWith("Check", contracts.ErrMailboxUnknown)
	wantCode(t, verify(), http.StatusUnprocessableEntity, "mailbox_unknown")
	fake.FailWith("Check", errors.New("graph: throttled"))
	wantCode(t, verify(), http.StatusUnprocessableEntity, "provider_unreachable")
	fake.FailWith("Check", nil)
	fake.SetStatus(contracts.MailboxUnavailable, "switch_off")
	wantCode(t, verify(), http.StatusUnprocessableEntity, "mail_channels_off")
}

// channelProviderJSON decodes ChannelProviderResponse.
type channelProviderJSON struct {
	Key             string  `json:"key"`
	DisplayName     string  `json:"displayName"`
	Available       bool    `json:"available"`
	Reason          *string `json:"reason"`
	Warning         *string `json:"warning"`
	MaxMessageBytes *int64  `json:"maxMessageBytes"`
}

func channelProviders(t *testing.T, c *modtest.Client) []channelProviderJSON {
	t.Helper()
	r := c.Do(http.MethodGet, "/api/v1/communications/channel-providers", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var out []channelProviderJSON
	r.JSON(&out)
	return out
}

func strOf(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

// TestChannelProviders: SMTP always; Microsoft 365 only while a slot is
// filled, with its status, the tenant-wide warning and its size budget.
func TestChannelProviders(t *testing.T) {
	t.Parallel()

	without := newHarness(t)
	got := channelProviders(t, without.SignIn(t, "communications:channels-manage"))
	if len(got) != 1 || got[0].Key != "smtp" || got[0].DisplayName != "SMTP" || !got[0].Available || got[0].MaxMessageBytes != nil {
		t.Errorf("without a slot = %+v, want SMTP alone", got)
	}

	h, fake := mailboxHarness(t)
	c := h.SignIn(t, "communications:channels-manage")
	describe := func() string {
		ps := channelProviders(t, c)
		if len(ps) != 2 || ps[0].Key != "smtp" {
			t.Fatalf("providers = %+v, want smtp and entra", ps)
		}
		p := ps[1]
		mb := "<nil>"
		if p.MaxMessageBytes != nil {
			mb = fmt.Sprint(*p.MaxMessageBytes)
		}
		return fmt.Sprintf("%s %s %v %s %s %s", p.Key, p.DisplayName, p.Available, strOf(p.Reason), strOf(p.Warning), mb)
	}
	if d := describe(); d != "entra Microsoft 365 true <nil> <nil> 3000000" {
		t.Errorf("available = %q", d)
	}
	fake.SetStatus(contracts.MailboxAvailable, contracts.MailboxTenantWide)
	if d := describe(); d != "entra Microsoft 365 true <nil> tenant_wide 3000000" {
		t.Errorf("tenant-wide = %q", d)
	}
	fake.SetStatus(contracts.MailboxUnavailable, "switch_off")
	if d := describe(); d != "entra Microsoft 365 false switch_off <nil> 3000000" {
		t.Errorf("switched off = %q", d)
	}

	if r := h.SignIn(t, "communications:conversations-view").Do(http.MethodGet, "/api/v1/communications/channel-providers", nil); r.Status != http.StatusForbidden {
		t.Errorf("without channels-manage: %d, want 403", r.Status)
	}
}

// TestChannels_CheckLimit: creating, switching and verifying a channel share
// communications-channel-check's 30 a minute per client address — each can
// reach Graph up to three times.
func TestChannels_CheckLimit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:channels-manage")
	for i := range 30 {
		path := "/api/v1/communications/channels/" + uuid.NewString() + "/verify"
		var body any
		if i%2 == 1 {
			path, body = "/api/v1/communications/channels/"+uuid.NewString()+"/provider", map[string]any{"provider": "entra"}
		}
		if r := c.Do(http.MethodPost, path, body); r.Status == http.StatusTooManyRequests {
			t.Fatalf("request %d: 429 within the limit", i+1)
		}
	}
	if r := c.Do(http.MethodPost, "/api/v1/communications/channels", newChannelBody(channelAddress(t))); r.Status != http.StatusTooManyRequests {
		t.Errorf("the 31st: %d, want 429", r.Status)
	}
	if r := c.Do(http.MethodGet, "/api/v1/communications/channels", nil); r.Status != http.StatusOK {
		t.Errorf("the list is not limited: %d", r.Status)
	}
}
