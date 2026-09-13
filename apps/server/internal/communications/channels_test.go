package communications_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the Channels area's tests (EP/ChannelEndpoints.cs,
// communications inventory §1.1, §2's CreateChannel/UpdateChannel/
// VerifyChannel bullets, §3.1's field-error table, §15.6, §19.2 items
// 10-13). Task 4's brief and its corrections are the authority for what
// must be pinned here — in particular UpdateChannel's split validation
// around the 404 (item 4 of the brief's corrections), which this file
// tests in both directions.

// channelJSON decodes ChannelResponse (EP/ChannelEndpoints.cs's
// ToChannelResponse). Settings mirrors the contract's anonymous nested
// object: host/port/useSsl/username for SMTP, domain/region always nil in
// this port (see channels.go's Mailgun-branch note).
type channelJSON struct {
	Id             string    `json:"id"`
	Type           string    `json:"type"`
	Address        string    `json:"address"`
	DisplayName    *string   `json:"displayName"`
	Provider       string    `json:"provider"`
	IsDefault      bool      `json:"isDefault"`
	IsActive       bool      `json:"isActive"`
	HasCredentials bool      `json:"hasCredentials"`
	CreatedAt      time.Time `json:"createdAt"`
	Settings       *struct {
		Domain   *string `json:"domain"`
		Host     *string `json:"host"`
		Port     *int32  `json:"port"`
		Region   *string `json:"region"`
		UseSsl   *bool   `json:"useSsl"`
		Username *string `json:"username"`
	} `json:"settings"`
}

// commErrorJSON decodes CommunicationErrorResponse
// ({"error":{"code","message","fields"?}}, inventory §3) — this module's
// own error vocabulary, distinct from the stats endpoints' ProblemDetails
// and from the access layer's AuthErrorResponse (modtest.Response.Code
// already decodes that one).
type commErrorJSON struct {
	Error struct {
		Code    string              `json:"code"`
		Message string              `json:"message"`
		Fields  map[string][]string `json:"fields"`
	} `json:"error"`
}

func (r commErrorJSON) field(name string) []string { return r.Error.Fields[name] }

// channelAddressCounter mints unique channel addresses (channels.yaml's
// (type, address) unique index, inventory §8) so parallel subtests never
// collide on 409 channel_exists by accident.
var channelAddressCounter atomic.Uint64

func channelAddress(t *testing.T) string {
	t.Helper()
	n := channelAddressCounter.Add(1)
	name := strings.NewReplacer("/", "-", " ", "-").Replace(strings.ToLower(t.Name()))
	if len(name) > 20 {
		name = name[:20]
	}
	return fmt.Sprintf("channel-%d-%s@example.test", n, name)
}

// newChannelBody is a valid CreateChannelRequest: email type, a unique
// address, and a valid smtp credential (host + port in range).
func newChannelBody(address string) map[string]any {
	return map[string]any{
		"type":    "email",
		"address": address,
		"smtp":    map[string]any{"host": "smtp.example.test", "port": 587},
	}
}

// createChannel posts body and fails t unless the response is 201,
// returning the decoded channel.
func createChannel(t *testing.T, c *modtest.Client, body map[string]any) channelJSON {
	t.Helper()
	r := c.Do(http.MethodPost, "/api/v1/communications/channels", body)
	if r.Status != http.StatusCreated {
		t.Fatalf("create channel: status %d body %s, want 201", r.Status, r.Body)
	}
	var ch channelJSON
	r.JSON(&ch)
	return ch
}

// TestGetChannels_EmptyArray proves the list is a bare array — [], never a
// paginated envelope and never null — when no channel exists (inventory
// §1.1: "200 array"; §19.2 item 19: channels order CreatedAt ASC).
func TestGetChannels_EmptyArray(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:channels-manage")

	r := c.Do(http.MethodGet, "/api/v1/communications/channels", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	if strings.TrimSpace(string(r.Body)) == "null" {
		t.Fatalf("body = %s, want a bare empty array, not null", r.Body)
	}
	var channels []channelJSON
	r.JSON(&channels)
	if len(channels) != 0 {
		t.Fatalf("channels = %v, want none", channels)
	}
}

// TestGetChannels_OrderedByCreatedAtAscending pins the list ordering
// (inventory §19.2 item 19): two channels created at different instants
// come back oldest first, and the response carries no pagination envelope.
func TestGetChannels_OrderedByCreatedAtAscending(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:channels-manage")

	first := createChannel(t, c, newChannelBody(channelAddress(t)))
	h.Advance(time.Minute)
	second := createChannel(t, c, newChannelBody(channelAddress(t)))

	r := c.Do(http.MethodGet, "/api/v1/communications/channels", nil)
	var channels []channelJSON
	r.JSON(&channels)
	if len(channels) != 2 {
		t.Fatalf("channels = %d, want 2", len(channels))
	}
	if channels[0].Id != first.Id || channels[1].Id != second.Id {
		t.Errorf("order = [%s, %s], want [%s, %s] (CreatedAt ascending)",
			channels[0].Id, channels[1].Id, first.Id, second.Id)
	}
}

// TestCreateChannel_FirstChannelIsForcedDefault pins CreateChannel's
// `request.IsDefault == true || !AnyChannelExists` rule (inventory §2): the
// first channel ever created is default even though the request never asks
// for it, and a second channel created without isDefault stays non-default.
// A mutation that dropped the "!AnyChannelExists" half — forcing default
// only when explicitly requested — would still pass every other test in
// this file but fail this one directly.
func TestCreateChannel_FirstChannelIsForcedDefault(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:channels-manage")

	first := createChannel(t, c, newChannelBody(channelAddress(t)))
	if !first.IsDefault {
		t.Error("first channel IsDefault = false, want true (forced default)")
	}

	second := createChannel(t, c, newChannelBody(channelAddress(t)))
	if second.IsDefault {
		t.Error("second channel (no isDefault requested) IsDefault = true, want false")
	}
}

// TestCreateChannel_ExplicitDefaultDemotesOthers pins the other half of the
// same rule: an explicit isDefault:true on a later channel demotes every
// other row (inventory §2's CreateChannel bullet).
func TestCreateChannel_ExplicitDefaultDemotesOthers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:channels-manage")

	first := createChannel(t, c, newChannelBody(channelAddress(t)))
	body := newChannelBody(channelAddress(t))
	body["isDefault"] = true
	second := createChannel(t, c, body)
	if !second.IsDefault {
		t.Fatal("second channel IsDefault = false, want true (explicitly requested)")
	}

	r := c.Do(http.MethodGet, "/api/v1/communications/channels/"+first.Id, nil)
	var got channelJSON
	r.JSON(&got)
	if got.IsDefault {
		t.Error("first channel still IsDefault = true after a later explicit default, want false")
	}
}

// TestCreateChannel_DuplicateAddressConflicts pins the 409 channel_exists
// path (inventory §1.1/§2): a unique violation on (type, address) becomes a
// 409, not a 500 or a silently-succeeding duplicate.
func TestCreateChannel_DuplicateAddressConflicts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:channels-manage")
	address := channelAddress(t)
	createChannel(t, c, newChannelBody(address))

	r := c.Do(http.MethodPost, "/api/v1/communications/channels", newChannelBody(address))
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var body commErrorJSON
	r.JSON(&body)
	if body.Error.Code != "channel_exists" {
		t.Errorf("code = %q, want channel_exists", body.Error.Code)
	}
	if body.Error.Message != "A channel with this address already exists." {
		t.Errorf("message = %q, want the exact channel_exists text", body.Error.Message)
	}
}

// TestCreateChannel_ValidationMessages pins every field-keyed validation
// message byte for byte (inventory §3.1, task 4 brief's correction 6),
// including the three the brief calls out verbatim.
func TestCreateChannel_ValidationMessages(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:channels-manage")

	cases := []struct {
		name  string
		body  func() map[string]any
		field string
		want  string
	}{
		{"missing type", func() map[string]any {
			b := newChannelBody(channelAddress(t))
			delete(b, "type")
			return b
		}, "type", "Only the email channel is currently supported."},
		{"wrong type", func() map[string]any {
			b := newChannelBody(channelAddress(t))
			b["type"] = "sms"
			return b
		}, "type", "Only the email channel is currently supported."},
		{"missing address", func() map[string]any {
			b := newChannelBody(channelAddress(t))
			delete(b, "address")
			return b
		}, "address", "A valid channel email address is required."},
		{"display-name-form address is rejected (round-trip strict)", func() map[string]any {
			b := newChannelBody(channelAddress(t))
			b["address"] = "Bob <bob@example.test>"
			return b
		}, "address", "A valid channel email address is required."},
		{"address with internal whitespace", func() map[string]any {
			b := newChannelBody(channelAddress(t))
			b["address"] = "bo b@example.test"
			return b
		}, "address", "A valid channel email address is required."},
		{"missing smtp entirely", func() map[string]any {
			b := newChannelBody(channelAddress(t))
			delete(b, "smtp")
			return b
		}, "smtp", "SMTP credentials require a host and valid port."},
		{"blank smtp host", func() map[string]any {
			b := newChannelBody(channelAddress(t))
			b["smtp"] = map[string]any{"host": "   ", "port": 587}
			return b
		}, "smtp", "SMTP credentials require a host and valid port."},
		{"smtp port zero", func() map[string]any {
			b := newChannelBody(channelAddress(t))
			b["smtp"] = map[string]any{"host": "smtp.example.test", "port": 0}
			return b
		}, "smtp", "SMTP credentials require a host and valid port."},
		{"smtp port too large", func() map[string]any {
			b := newChannelBody(channelAddress(t))
			b["smtp"] = map[string]any{"host": "smtp.example.test", "port": 65536}
			return b
		}, "smtp", "SMTP credentials require a host and valid port."},
		{"provider mailgun is not smtp", func() map[string]any {
			b := newChannelBody(channelAddress(t))
			b["provider"] = "mailgun"
			return b
		}, "smtp", "SMTP credentials require the smtp provider."},
		{"mailgun credential supplied alongside smtp", func() map[string]any {
			b := newChannelBody(channelAddress(t))
			b["mailgun"] = map[string]any{"domain": "example.test", "region": "us"}
			return b
		}, "credentials", "Only the selected provider credential may be supplied."},
		{"displayName too long", func() map[string]any {
			b := newChannelBody(channelAddress(t))
			b["displayName"] = strings.Repeat("x", 201)
			return b
		}, "displayName", "DisplayName is invalid."},
		{"displayName untrimmed", func() map[string]any {
			b := newChannelBody(channelAddress(t))
			b["displayName"] = " Support "
			return b
		}, "displayName", "DisplayName is invalid."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := c.Do(http.MethodPost, "/api/v1/communications/channels", tc.body())
			if r.Status != http.StatusBadRequest {
				t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
			}
			var body commErrorJSON
			r.JSON(&body)
			if body.Error.Code != "invalid_request" {
				t.Errorf("code = %q, want invalid_request", body.Error.Code)
			}
			if body.Error.Message != "The request is invalid." {
				t.Errorf("message = %q, want %q", body.Error.Message, "The request is invalid.")
			}
			got := body.field(tc.field)
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("fields[%q] = %v, want [%q]", tc.field, got, tc.want)
			}
		})
	}
}

// TestCreateChannel_MailgunProviderNeverCreatesAChannel is the Mailgun-branch
// port decision made concrete: a request naming provider "mailgun" is
// refused (see the case above), and — the thing a field-error assertion
// alone cannot prove — no row is ever inserted. A mutation that validated
// the provider field but inserted the channel anyway regardless of the
// error would pass the 400 assertion above and fail only this one.
func TestCreateChannel_MailgunProviderNeverCreatesAChannel(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:channels-manage")
	address := channelAddress(t)

	body := newChannelBody(address)
	body["provider"] = "mailgun"
	r := c.Do(http.MethodPost, "/api/v1/communications/channels", body)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}

	if n := h.Count(t, `SELECT count(*) FROM communications.channels WHERE address = $1`, address); n != 0 {
		t.Errorf("channels with address %q = %d, want 0 (mailgun channel must never be created)", address, n)
	}
}

// TestCreateChannel_NeverExposesCredentials proves the password never
// appears in the response — not the plaintext, not any encoded form of it —
// and that hasCredentials/settings expose only host/port/useSsl/username
// (inventory §15.6 item 13, task 4's required assertion). It posts with a
// password chosen to be easy to spot in any encoding.
func TestCreateChannel_NeverExposesCredentials(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:channels-manage")

	const secretMarker = "sUp3r-S3cr3t-PASSWORD-marker"
	body := newChannelBody(channelAddress(t))
	body["smtp"] = map[string]any{"host": "smtp.example.test", "port": 587, "username": "svc", "password": secretMarker}
	r := c.Do(http.MethodPost, "/api/v1/communications/channels", body)
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
	}
	if strings.Contains(string(r.Body), secretMarker) {
		t.Fatalf("create response body contains the plaintext password: %s", r.Body)
	}
	var ch channelJSON
	r.JSON(&ch)
	if !ch.HasCredentials {
		t.Error("HasCredentials = false, want true")
	}
	if ch.Settings == nil || ch.Settings.Host == nil || *ch.Settings.Host != "smtp.example.test" {
		t.Errorf("Settings = %+v, want host smtp.example.test", ch.Settings)
	}

	// The same guarantee holds on every other endpoint that can return this
	// channel: get-by-id and the list.
	getR := c.Do(http.MethodGet, "/api/v1/communications/channels/"+ch.Id, nil)
	if strings.Contains(string(getR.Body), secretMarker) {
		t.Fatalf("get-by-id response body contains the plaintext password: %s", getR.Body)
	}
	listR := c.Do(http.MethodGet, "/api/v1/communications/channels", nil)
	if strings.Contains(string(listR.Body), secretMarker) {
		t.Fatalf("list response body contains the plaintext password: %s", listR.Body)
	}
}

// TestGetChannel_NotFound pins the bare 404 (inventory §1.1: "404 bare").
func TestGetChannel_NotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:channels-manage")

	r := c.Do(http.MethodGet, "/api/v1/communications/channels/"+uuid.NewString(), nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
	if len(r.Body) != 0 {
		t.Errorf("body = %s, want empty (bare 404)", r.Body)
	}
}

// TestUpdateChannel_RequiresBody pins the "request" field error
// (inventory §3.1's channel-update-only row).
func TestUpdateChannel_RequiresBody(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:channels-manage")
	ch := createChannel(t, c, newChannelBody(channelAddress(t)))

	r := c.Do(http.MethodPut, "/api/v1/communications/channels/"+ch.Id, nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var body commErrorJSON
	r.JSON(&body)
	got := body.field("request")
	if len(got) != 1 || got[0] != "A request body is required." {
		t.Errorf("fields[request] = %v, want [%q]", got, "A request body is required.")
	}
}

// TestUpdateChannel_FieldValidationRunsBeforeTheLookup is the first half of
// the split-validation ordering task 4's brief calls "the single most
// likely thing to be got wrong": an invalid field (displayName) against a
// channel id that does not exist answers 400, not 404 — field validation
// runs before the existence check. A mutation that looked the channel up
// before validating fields would turn this 400 into a 404 and only this
// test (and its 404-direction sibling below) would catch it.
func TestUpdateChannel_FieldValidationRunsBeforeTheLookup(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:channels-manage")

	r := c.Do(http.MethodPut, "/api/v1/communications/channels/"+uuid.NewString(), map[string]any{
		"displayName": strings.Repeat("x", 201),
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400 (field validation precedes the 404)", r.Status, r.Body)
	}
	var body commErrorJSON
	r.JSON(&body)
	got := body.field("displayName")
	if len(got) != 1 || got[0] != "DisplayName is invalid." {
		t.Errorf("fields[displayName] = %v, want [%q]", got, "DisplayName is invalid.")
	}
}

// TestUpdateChannel_CredentialValidationRunsAfterTheLookup is the second,
// opposite-direction half of the same ordering: an invalid or absent
// credential against a channel id that does not exist answers 404, not
// 400 — credential validation runs only after the channel is confirmed to
// exist. A mutation that validated credentials before the lookup (the
// natural, and wrong, symmetric choice) would turn every case below into a
// 400 and this test would catch every one of them.
func TestUpdateChannel_CredentialValidationRunsAfterTheLookup(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:channels-manage")

	cases := []struct {
		name string
		body map[string]any
	}{
		{"invalid smtp port", map[string]any{"smtp": map[string]any{"host": "smtp.example.test", "port": 0}}},
		{"blank smtp host", map[string]any{"smtp": map[string]any{"host": "", "port": 587}}},
		{"provider mismatch", map[string]any{"provider": "mailgun"}},
		{"mailgun credential supplied", map[string]any{"mailgun": map[string]any{"domain": "d", "region": "us"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := c.Do(http.MethodPut, "/api/v1/communications/channels/"+uuid.NewString(), tc.body)
			if r.Status != http.StatusNotFound {
				t.Errorf("status %d body %s, want 404 (credential validation follows the lookup)", r.Status, r.Body)
			}
			if len(r.Body) != 0 {
				t.Errorf("body = %s, want empty (bare 404)", r.Body)
			}
		})
	}
}

// TestUpdateChannel_CredentialValidationMessages pins the exact post-404
// credential-validation messages (inventory §3.1) against a channel that
// really exists, so the 400s below are the credential branch, not the
// field-validation branch above.
func TestUpdateChannel_CredentialValidationMessages(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:channels-manage")
	// Seeded synchronously, before any parallel subtest creates its own
	// channel: without it, two subtests' createChannel calls could both
	// observe AnyChannelExists()=false and race to become the forced
	// default, tripping the (type, is_default) partial unique index —
	// a real concurrency hazard of the forced-default rule, but not what
	// this test is about.
	createChannel(t, c, newChannelBody(channelAddress(t)))

	cases := []struct {
		name  string
		body  map[string]any
		field string
		want  string
	}{
		{"invalid smtp port", map[string]any{"smtp": map[string]any{"host": "smtp.example.test", "port": 0}},
			"smtp", "SMTP credentials require a host and valid port."},
		{"provider mismatch", map[string]any{"provider": "mailgun"},
			"smtp", "SMTP credentials require the smtp provider."},
		{"mailgun credential supplied", map[string]any{"mailgun": map[string]any{"domain": "d", "region": "us"}},
			"credentials", "Only the selected provider credential may be supplied."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ch := createChannel(t, c, newChannelBody(channelAddress(t)))
			r := c.Do(http.MethodPut, "/api/v1/communications/channels/"+ch.Id, tc.body)
			if r.Status != http.StatusBadRequest {
				t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
			}
			var body commErrorJSON
			r.JSON(&body)
			got := body.field(tc.field)
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("fields[%q] = %v, want [%q]", tc.field, got, tc.want)
			}
		})
	}
}

// TestUpdateChannel_MissingCredentialWhenNoneExists pins the defensive
// post-404 "Credentials for the selected provider are required." message
// (inventory §3.1's post-404 row) for a channel whose credential row is
// absent — unreachable through this API's own create path (every created
// channel gets one atomically), so the fixture deletes it directly to
// exercise the hazard.
func TestUpdateChannel_MissingCredentialWhenNoneExists(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:channels-manage")
	ch := createChannel(t, c, newChannelBody(channelAddress(t)))
	h.Exec(t, `DELETE FROM communications.channel_credentials WHERE channel_id = $1`, ch.Id)

	r := c.Do(http.MethodPut, "/api/v1/communications/channels/"+ch.Id, map[string]any{"isActive": false})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var body commErrorJSON
	r.JSON(&body)
	got := body.field("smtp")
	if len(got) != 1 || got[0] != "Credentials for the selected provider are required." {
		t.Errorf("fields[smtp] = %v, want [%q]", got, "Credentials for the selected provider are required.")
	}
}

// TestUpdateChannel_WritesCredentialsOntoAChannelThatHadNone is fix round
// 1's item 1: a PUT that *does* supply a valid smtp block for a channel
// whose credential row is absent writes via a plain UPDATE ... WHERE
// channel_id, which matches zero rows when there is nothing to update —
// and, before this fix, still answered 200 with hasCredentials:true. The
// reviewer proved it live: 0 rows affected, then a GET showing
// hasCredentials:false and settings:null. Reading the channel back after
// the PUT, not just trusting the PUT's own response, is what catches that —
// a lying response and an honest one look identical from the PUT call
// alone.
func TestUpdateChannel_WritesCredentialsOntoAChannelThatHadNone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:channels-manage")
	ch := createChannel(t, c, newChannelBody(channelAddress(t)))
	h.Exec(t, `DELETE FROM communications.channel_credentials WHERE channel_id = $1`, ch.Id)

	r := c.Do(http.MethodPut, "/api/v1/communications/channels/"+ch.Id, map[string]any{
		"smtp": map[string]any{"host": "recovered-smtp.example.test", "port": 587, "username": "svc"},
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var putBody channelJSON
	r.JSON(&putBody)
	if !putBody.HasCredentials {
		t.Error("PUT response HasCredentials = false, want true")
	}

	getR := c.Do(http.MethodGet, "/api/v1/communications/channels/"+ch.Id, nil)
	var got channelJSON
	getR.JSON(&got)
	if !got.HasCredentials {
		t.Fatal("GET after the PUT: HasCredentials = false, want true — the write did not actually persist")
	}
	if got.Settings == nil || got.Settings.Host == nil || *got.Settings.Host != "recovered-smtp.example.test" {
		t.Errorf("GET after the PUT: Settings = %+v, want host recovered-smtp.example.test", got.Settings)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.channel_credentials WHERE channel_id = $1`, ch.Id); n != 1 {
		t.Errorf("channel_credentials rows for %s = %d, want exactly 1", ch.Id, n)
	}
}

// TestUpdateChannel_DisplayNameTriState pins the three-state displayName
// rule (inventory §19.2 item 11): omit leaves it unchanged, "" clears it to
// null, any other valid value sets it. A mutation collapsing "omitted" and
// "" into the same behaviour would fail the first sub-test below.
func TestUpdateChannel_DisplayNameTriState(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:channels-manage")
	// See TestUpdateChannel_CredentialValidationMessages's identical seed:
	// without it, this test's three parallel subtests could race to become
	// the forced default channel.
	createChannel(t, c, newChannelBody(channelAddress(t)))

	t.Run("omitted leaves unchanged", func(t *testing.T) {
		t.Parallel()
		body := newChannelBody(channelAddress(t))
		body["displayName"] = "Support"
		ch := createChannel(t, c, body)

		r := c.Do(http.MethodPut, "/api/v1/communications/channels/"+ch.Id, map[string]any{"isActive": true})
		if r.Status != http.StatusOK {
			t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
		}
		var got channelJSON
		r.JSON(&got)
		if got.DisplayName == nil || *got.DisplayName != "Support" {
			t.Errorf("DisplayName = %v, want \"Support\" (unchanged)", got.DisplayName)
		}
	})

	t.Run("empty string clears to null", func(t *testing.T) {
		t.Parallel()
		body := newChannelBody(channelAddress(t))
		body["displayName"] = "Support"
		ch := createChannel(t, c, body)

		r := c.Do(http.MethodPut, "/api/v1/communications/channels/"+ch.Id, map[string]any{"displayName": ""})
		if r.Status != http.StatusOK {
			t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
		}
		var got channelJSON
		r.JSON(&got)
		if got.DisplayName != nil {
			t.Errorf("DisplayName = %v, want nil (cleared)", *got.DisplayName)
		}
	})

	t.Run("a new value sets it", func(t *testing.T) {
		t.Parallel()
		ch := createChannel(t, c, newChannelBody(channelAddress(t)))

		r := c.Do(http.MethodPut, "/api/v1/communications/channels/"+ch.Id, map[string]any{"displayName": "Billing"})
		if r.Status != http.StatusOK {
			t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
		}
		var got channelJSON
		r.JSON(&got)
		if got.DisplayName == nil || *got.DisplayName != "Billing" {
			t.Errorf("DisplayName = %v, want \"Billing\"", got.DisplayName)
		}
	})
}

// TestUpdateChannel_IsDefaultIsWriteOnceTrue pins inventory §19.2 item 10:
// isDefault:true promotes, but isDefault:false is silently ignored — there
// is no way to clear the default flag through this API. A mutation that
// let false demote the channel would fail the second request's assertion.
func TestUpdateChannel_IsDefaultIsWriteOnceTrue(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:channels-manage")
	// The very first channel in this harness is already forced default; a
	// second one starts non-default and this test promotes it explicitly to
	// isolate the assertion from that forced-default rule.
	createChannel(t, c, newChannelBody(channelAddress(t)))
	ch := createChannel(t, c, newChannelBody(channelAddress(t)))

	promote := c.Do(http.MethodPut, "/api/v1/communications/channels/"+ch.Id, map[string]any{"isDefault": true})
	var promoted channelJSON
	promote.JSON(&promoted)
	if !promoted.IsDefault {
		t.Fatalf("after isDefault:true, IsDefault = false, want true")
	}

	demote := c.Do(http.MethodPut, "/api/v1/communications/channels/"+ch.Id, map[string]any{"isDefault": false})
	var demoted channelJSON
	demote.JSON(&demoted)
	if !demoted.IsDefault {
		t.Errorf("after isDefault:false, IsDefault = false, want true (isDefault:false is silently ignored)")
	}
}

// TestUpdateChannel_PromotingNewDefaultDemotesOthers pins the same demotion
// rule UpdateChannel shares with CreateChannel: setting a channel default
// demotes every other channel in the same call.
func TestUpdateChannel_PromotingNewDefaultDemotesOthers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:channels-manage")
	first := createChannel(t, c, newChannelBody(channelAddress(t))) // forced default
	second := createChannel(t, c, newChannelBody(channelAddress(t)))

	r := c.Do(http.MethodPut, "/api/v1/communications/channels/"+second.Id, map[string]any{"isDefault": true})
	var got channelJSON
	r.JSON(&got)
	if !got.IsDefault {
		t.Fatal("promoted channel IsDefault = false, want true")
	}

	firstR := c.Do(http.MethodGet, "/api/v1/communications/channels/"+first.Id, nil)
	var firstNow channelJSON
	firstR.JSON(&firstNow)
	if firstNow.IsDefault {
		t.Error("original default channel still IsDefault = true after a new default was set, want false")
	}
}

// TestUpdateChannel_CredentialsAreReplaced proves a new smtp block on
// update actually rewrites the stored settings (host visible in the
// response) rather than being silently accepted and ignored.
func TestUpdateChannel_CredentialsAreReplaced(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:channels-manage")
	ch := createChannel(t, c, newChannelBody(channelAddress(t)))

	r := c.Do(http.MethodPut, "/api/v1/communications/channels/"+ch.Id, map[string]any{
		"smtp": map[string]any{"host": "new-smtp.example.test", "port": 465, "useSsl": true},
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var got channelJSON
	r.JSON(&got)
	if got.Settings == nil || got.Settings.Host == nil || *got.Settings.Host != "new-smtp.example.test" {
		t.Errorf("Settings = %+v, want host new-smtp.example.test", got.Settings)
	}
	if got.Settings.Port == nil || *got.Settings.Port != 465 {
		t.Errorf("Settings.Port = %v, want 465", got.Settings.Port)
	}
}

// channelCredentialCiphertext reads communications.channel_credentials'
// secret_ciphertext for id directly — the plaintext is never observable
// through any response (task 4's own required assertion), so a test that
// wants to know whether the stored credential actually changed has to read
// it at this level.
func channelCredentialCiphertext(t *testing.T, h *modtest.Harness, channelID string) string {
	t.Helper()
	return modtest.One[string](t, h, `SELECT secret_ciphertext FROM communications.channel_credentials WHERE channel_id = $1`, channelID)
}

// TestUpdateChannel_OmittingSmtpLeavesTheStoredCredentialByteForByteUnchanged
// is fix round 1's item 3: the reviewer found this path unverified — a
// mutation that discarded it entirely (always rewriting the credential row,
// even with a blank password, whenever any field on the channel is updated)
// left the whole suite green, because nothing read the ciphertext back.
// Reading it before and after an update that never mentions `smtp` at all
// closes that gap: the row must be byte-for-byte identical, not merely
// "still present" or "still decryptable".
func TestUpdateChannel_OmittingSmtpLeavesTheStoredCredentialByteForByteUnchanged(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:channels-manage")
	body := newChannelBody(channelAddress(t))
	body["smtp"] = map[string]any{"host": "smtp.example.test", "port": 587, "username": "svc", "password": "keep-me"}
	ch := createChannel(t, c, body)

	before := channelCredentialCiphertext(t, h, ch.Id)

	r := c.Do(http.MethodPut, "/api/v1/communications/channels/"+ch.Id, map[string]any{"isActive": true})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}

	after := channelCredentialCiphertext(t, h, ch.Id)
	if before != after {
		t.Errorf("secret_ciphertext changed after an update that omitted smtp entirely:\n before = %q\n after  = %q", before, after)
	}
}

// TestUpdateChannel_NewSettingsWithoutAPasswordReuseTheExistingOne is the
// other half of fix round 1's item 3: an update that *does* supply a new
// smtp block, but omits the password, must keep sending the old password —
// inventory §15.6's documented .NET behaviour ("the existing password is
// decrypted first and reused when the request omits one"). The stored
// ciphertext itself is expected to change here (Seal uses a fresh nonce
// every call, so re-sealing the same plaintext never reproduces the same
// bytes) — the only way to prove *reuse*, as opposed to "some password
// still there", is to observe what plaintext actually reaches a real send
// attempt. modtest.WithSMTPVerify's fake is exactly that observation point,
// reused from TestVerifyChannel_Succeeds's proof that it captures precisely
// what the handler resolved.
func TestUpdateChannel_NewSettingsWithoutAPasswordReuseTheExistingOne(t *testing.T) {
	t.Parallel()

	var gotPassword, gotHost string
	h := newHarness(t, modtest.WithSMTPVerify(func(_ context.Context, cfg config.MailConfig, _ bool) error {
		gotPassword, gotHost = cfg.Password, cfg.Host
		return nil
	}))
	c := h.SignIn(t, "communications:channels-manage")
	body := newChannelBody(channelAddress(t))
	body["smtp"] = map[string]any{"host": "old-smtp.example.test", "port": 587, "password": "the-original-password"}
	ch := createChannel(t, c, body)

	// New host, same port family (587 -> starttls, still a supported TLS
	// mode), password omitted entirely.
	r := c.Do(http.MethodPut, "/api/v1/communications/channels/"+ch.Id, map[string]any{
		"smtp": map[string]any{"host": "new-smtp.example.test", "port": 587},
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}

	verifyR := c.Do(http.MethodPost, "/api/v1/communications/channels/"+ch.Id+"/verify", nil)
	if verifyR.Status != http.StatusOK {
		t.Fatalf("verify: status %d body %s, want 200", verifyR.Status, verifyR.Body)
	}
	if gotHost != "new-smtp.example.test" {
		t.Errorf("verify saw host %q, want the new host new-smtp.example.test", gotHost)
	}
	if gotPassword != "the-original-password" {
		t.Errorf("verify saw password %q, want the original password reused, not blanked out", gotPassword)
	}
}

// TestVerifyChannel_NotFoundBeforeAnythingElse pins VerifyChannel's
// ordering (inventory §2: "lookup -> 404 before anything else"): there is
// no body to validate, so a missing channel id is the only thing this
// endpoint can ever answer besides success/failure, and it must be a bare
// 404.
func TestVerifyChannel_NotFoundBeforeAnythingElse(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:channels-manage")

	r := c.Do(http.MethodPost, "/api/v1/communications/channels/"+uuid.NewString()+"/verify", nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
	if len(r.Body) != 0 {
		t.Errorf("body = %s, want empty (bare 404)", r.Body)
	}
}

// TestVerifyChannel_DestinationRejected proves verify dispatches through
// internal/mail's real destination guard (task 4 brief: "Verify uses the
// existing internal/mail destination guard") — not a stand-in — by
// pointing a channel at a loopback address, which the guard blocks purely
// from the IP itself, before any network I/O, so this test needs no live
// SMTP server to be deterministic.
func TestVerifyChannel_DestinationRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:channels-manage")

	body := newChannelBody(channelAddress(t))
	body["smtp"] = map[string]any{"host": "127.0.0.1", "port": 465, "useSsl": true}
	ch := createChannel(t, c, body)

	r := c.Do(http.MethodPost, "/api/v1/communications/channels/"+ch.Id+"/verify", nil)
	if r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d body %s, want 422", r.Status, r.Body)
	}
	var got commErrorJSON
	r.JSON(&got)
	if got.Error.Code != "destination_rejected" {
		t.Errorf("code = %q, want destination_rejected", got.Error.Code)
	}
	if !strings.Contains(got.Error.Message, "127.0.0.1") {
		t.Errorf("message = %q, want it to name the rejected destination", got.Error.Message)
	}
}

// TestVerifyChannel_VerificationFailedForAnUnsatisfiableTLSMode proves the
// generic 422 verification_failed path fires for a channel whose settings
// cannot resolve to any supported TLS mode (useSsl false and a port other
// than 587) — a failure this port refuses before ever attempting a
// connection, so this test also needs no live SMTP server.
func TestVerifyChannel_VerificationFailedForAnUnsatisfiableTLSMode(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:channels-manage")

	body := newChannelBody(channelAddress(t))
	body["smtp"] = map[string]any{"host": "smtp.example.test", "port": 25, "useSsl": false}
	ch := createChannel(t, c, body)

	r := c.Do(http.MethodPost, "/api/v1/communications/channels/"+ch.Id+"/verify", nil)
	if r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d body %s, want 422", r.Status, r.Body)
	}
	var got commErrorJSON
	r.JSON(&got)
	if got.Error.Code != "verification_failed" {
		t.Errorf("code = %q, want verification_failed", got.Error.Code)
	}
	if got.Error.Message != "Channel verification failed." {
		t.Errorf("message = %q, want %q", got.Error.Message, "Channel verification failed.")
	}
}

// TestVerifyChannel_Succeeds proves the handler's success path: a
// connectivity check that returns nil answers 200 {ok:true} (inventory
// §1.1's "200 {ok:true}"), and that the settings and credential the handler
// resolved from the stored channel — host, port, the TLS mode smtpTLSMode
// derived, username and the decrypted password — are exactly what reaches
// the check. modtest.WithSMTPVerify stands in for mail.VerifyConnection
// (module.Deps.SMTPVerify's doc has the full reasoning) so this proves the
// handler's own wiring without a live SMTP server or the production
// destination guard's network reach — the only sound way to reach this
// path in a test.
func TestVerifyChannel_Succeeds(t *testing.T) {
	t.Parallel()

	type call struct {
		host, tls, username, password string
		port                          int
		allowInsecure                 bool
	}
	var got call
	h := newHarness(t, modtest.WithSMTPVerify(func(_ context.Context, cfg config.MailConfig, allowInsecure bool) error {
		got = call{host: cfg.Host, tls: cfg.TLS, username: cfg.Username, password: cfg.Password, port: cfg.Port, allowInsecure: allowInsecure}
		return nil
	}))
	c := h.SignIn(t, "communications:channels-manage")

	body := newChannelBody(channelAddress(t))
	body["smtp"] = map[string]any{
		"host": "smtp.verify.example.test", "port": 465, "useSsl": true,
		"username": "svc", "password": "s3cret",
	}
	ch := createChannel(t, c, body)

	r := c.Do(http.MethodPost, "/api/v1/communications/channels/"+ch.Id+"/verify", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var okBody struct {
		Ok bool `json:"ok"`
	}
	r.JSON(&okBody)
	if !okBody.Ok {
		t.Error("ok = false, want true")
	}

	if got.host != "smtp.verify.example.test" || got.port != 465 || got.tls != "implicit" {
		t.Errorf("verify call = %+v, want host smtp.verify.example.test port 465 tls implicit", got)
	}
	if got.username != "svc" || got.password != "s3cret" {
		t.Errorf("verify call credentials = %q/%q, want svc/s3cret", got.username, got.password)
	}
	if got.allowInsecure {
		t.Error("allowInsecure = true, want false")
	}
}

// channelsEndpointCases pairs every channels operation with a request shape
// that reaches its handler — the fixed-up id path below stands in for a
// real one where a case needs a body validation to pass permission
// enforcement first, since access is checked before the body is decoded.
type channelsEndpointCase struct {
	method string
	path   func(id string) string
	body   func(address string) any
}

var channelsEndpointCases = []channelsEndpointCase{
	{method: http.MethodGet, path: func(string) string { return "/api/v1/communications/channels" }},
	{method: http.MethodPost, path: func(string) string { return "/api/v1/communications/channels" },
		body: func(address string) any { return newChannelBody(address) }},
	{method: http.MethodGet, path: func(id string) string { return "/api/v1/communications/channels/" + id }},
	{method: http.MethodPut, path: func(id string) string { return "/api/v1/communications/channels/" + id },
		body: func(string) any { return map[string]any{"isActive": true} }},
	{method: http.MethodPost, path: func(id string) string { return "/api/v1/communications/channels/" + id + "/verify" }},
}

// TestChannelsRequireOnlyChannelsManage pins task 4 brief's correction 1:
// all five operations require communications:channels-manage and nothing
// else — an unrelated permission (conversations-view) never suffices, and a
// caller with no permission at all is forbidden.
func TestChannelsRequireOnlyChannelsManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	none := h.SignIn(t)
	unrelated := h.SignIn(t, "communications:conversations-view")
	authorized := h.SignIn(t, "communications:channels-manage")

	// A channel every id-scoped case can target: created by an
	// already-authorized client so the fixture itself never depends on the
	// permission under test. Its smtp settings deliberately cannot resolve
	// to any supported TLS mode (see smtpTLSMode), so the verify case below
	// fails fast with 422 rather than attempting a real network connection.
	fixtureBody := newChannelBody(channelAddress(t))
	fixtureBody["smtp"] = map[string]any{"host": "smtp.example.test", "port": 25, "useSsl": false}
	ch := createChannel(t, authorized, fixtureBody)

	for _, ec := range channelsEndpointCases {
		t.Run(ec.method+" "+ec.path("{id}"), func(t *testing.T) {
			var body any
			if ec.body != nil {
				body = ec.body(channelAddress(t))
			}
			path := ec.path(ch.Id)

			if r := none.Do(ec.method, path, body); r.Status != http.StatusForbidden {
				t.Errorf("no permission: status %d body %s, want 403", r.Status, r.Body)
			}
			if r := unrelated.Do(ec.method, path, body); r.Status != http.StatusForbidden {
				t.Errorf("conversations-view only: status %d body %s, want 403", r.Status, r.Body)
			}
			if r := authorized.Do(ec.method, path, body); r.Status == http.StatusForbidden {
				t.Errorf("channels-manage: status 403 body %s, want not forbidden", r.Body)
			}
		})
	}
}
