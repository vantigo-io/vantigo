package entra_test

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The webhook (calendar Outlook sync design O11.5): the handshake inside and
// outside the window, the notifications' checks, the flood coalesced, the
// lifecycle events, the bodies refused — and GET /connection's
// notifications (O11.7). It is anonymous: no session is needed.

// notice is a notification item of s as Graph posts it, edited by edit.
func notice(s graphtest.Subscription, edit ...func(item map[string]any)) map[string]any {
	item := map[string]any{
		"subscriptionId":                 s.ID,
		"subscriptionExpirationDateTime": s.Expiration.UTC().Format(time.RFC3339),
		"clientState":                    s.ClientState,
		"changeType":                     "updated",
		"resource":                       "Users/" + strings.TrimSuffix(strings.TrimPrefix(s.Resource, "/users/"), "/events") + "/Events/AAMkAGI2TG93AAA=",
		"tenantId":                       tenantID,
		"resourceData":                   map[string]any{"@odata.type": "#Microsoft.Graph.Event", "id": "AAMkAGI2TG93AAA="},
	}
	for _, fn := range edit {
		fn(item)
	}
	return item
}

// post posts the items to the webhook by c, answering the response.
func post(c *modtest.Client, items ...map[string]any) *modtest.Response {
	value := make([]any, len(items))
	for i, it := range items {
		value[i] = it
	}
	return c.Do(http.MethodPost, noticesPath, map[string]any{"value": value})
}

// watched is a mailbox watched through the provider, and its subscription.
func watched(t *testing.T, h *harness, g *graphtest.FakeGraph) (string, graphtest.Subscription) {
	t.Helper()
	mailbox := mailboxUser(t, h, g)
	if err := mailboxProvider(h).Watch(context.Background(), mailbox); err != nil {
		t.Fatal(err)
	}
	return mailbox, subscriptionOf(t, g, mailbox)
}

// notifiedAt is when the row of mailbox was last notified, zero when never.
func notifiedAt(t *testing.T, h *harness, mailbox string) time.Time {
	t.Helper()
	var at *time.Time
	if err := h.Pool().QueryRow(context.Background(), `SELECT notified_at FROM entra.mailbox_subscriptions WHERE object_id = $1`, mailbox).Scan(&at); err != nil {
		t.Fatal(err)
	}
	if at == nil {
		return time.Time{}
	}
	return *at
}

// The handshake is answered — 200 text/plain, nosniff, no-store, the token
// decoded — only inside the window a create opens, and only for a token of
// 1 to 1024 bytes with no control character; else 404 with no body.
func TestMailboxNotifications_Handshake(t *testing.T) {
	h, _, _ := newNoticesHarness(t)
	c := h.Client(t)
	handshake := func(token string) *modtest.Response {
		return c.Do(http.MethodPost, noticesPath+"?validationToken="+url.QueryEscape(token), nil, modtest.RawBody("text/plain; charset=utf-8", nil))
	}
	refused := func(name string, r *modtest.Response) {
		t.Helper()
		if r.Status != http.StatusNotFound || len(r.Body) != 0 {
			t.Errorf("%s: status %d body %q, want 404 and no body", name, r.Status, r.Body)
		}
	}
	token := "Validation: Testing client application reachability for subscription Request-Id: 3b3f4c7e-9d2a-4f5e-8a1b-6c7d8e9f0a1b"

	refused("no window", handshake(token))

	h.Exec(t, `UPDATE entra.connection SET notifications_validating_until = $1`, h.Now().Add(30*time.Second))
	r := handshake(token)
	if r.Status != http.StatusOK || string(r.Body) != token {
		t.Fatalf("inside the window: status %d body %q, want 200 and the token", r.Status, r.Body)
	}
	if r.Header("Content-Type") != "text/plain; charset=utf-8" || r.Header("X-Content-Type-Options") != "nosniff" || r.Header("Cache-Control") != "no-store" {
		t.Errorf("headers: %q %q %q", r.Header("Content-Type"), r.Header("X-Content-Type-Options"), r.Header("Cache-Control"))
	}
	if r := handshake(strings.Repeat("a", 1024)); r.Status != http.StatusOK {
		t.Errorf("a token of 1024 bytes: %d, want 200", r.Status)
	}
	refused("a token of 1025 bytes", handshake(strings.Repeat("a", 1025)))
	refused("a control character", handshake("abc\ndef"))
	refused("a NUL", handshake("abc\x00def"))
	refused("an empty token", handshake(""))

	h.Advance(31 * time.Second)
	refused("past the window", handshake(token))
}

// A valid notification marks its subscription's row notified at once and
// the connection's last notification; Notified answers the mailbox, in the
// lower case the other calls take, after since and not at it. The ids and
// the tenant are read whatever their case.
func TestMailboxNotifications_AValidOneMarks(t *testing.T) {
	ctx := context.Background()
	h, g, o := newNoticesHarness(t)
	c := h.Client(t)
	mailbox, s := watched(t, h, g)
	if !notifiedAt(t, h, mailbox).IsZero() {
		t.Fatal("notified before any notification")
	}

	r := post(c, notice(s, func(it map[string]any) {
		it["subscriptionId"] = strings.ToUpper(s.ID)
		it["tenantId"] = strings.ToUpper(tenantID)
	}))
	if r.Status != http.StatusAccepted || len(r.Body) != 0 {
		t.Fatalf("status %d body %q, want 202 and no body", r.Status, r.Body)
	}
	at := h.Now()
	if got := notifiedAt(t, h, mailbox); !got.Equal(at) {
		t.Errorf("notified at %s, want %s", got, at)
	}
	if got := getConnection(t, o).CalendarSync.Notifications.LastNotificationAt; got == nil || !got.Equal(at) {
		t.Errorf("lastNotificationAt = %v, want %s", got, at)
	}

	p := mailboxProvider(h)
	notices, err := p.Notified(ctx, at.Add(-time.Minute))
	if err != nil || len(notices) != 1 || notices[0].Mailbox != strings.ToLower(mailbox) || !notices[0].At.Equal(at) {
		t.Errorf("Notified = %+v %v, want %s at %s", notices, err, mailbox, at)
	}
	if notices, err := p.Notified(ctx, at); err != nil || len(notices) != 0 {
		t.Errorf("Notified since the notice = %+v %v, want none", notices, err)
	}
}

// A flood is coalesced: a row marked within the last five seconds is not
// written again, a request naming one subscription many times writes it
// once, and the connection's last notification moves at most once a
// minute. Notified answers the mailboxes oldest first.
func TestMailboxNotifications_AFloodIsCoalesced(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newNoticesHarness(t)
	c := h.Client(t)
	mailbox, s := watched(t, h, g)
	other, os := watched(t, h, g)

	items := make([]map[string]any, 50)
	for i := range items {
		items[i] = notice(s)
	}
	post(c, items...)
	first := h.Now()
	if !strings.Contains(h.Logs(), `"subscriptions":1,"written":1`) {
		t.Errorf("fifty items of one subscription were not written once:\n%s", h.Logs())
	}
	h.Advance(4 * time.Second)
	post(c, notice(s))
	if got := notifiedAt(t, h, mailbox); !got.Equal(first) {
		t.Errorf("marked again within five seconds: %s", got)
	}
	h.Advance(2 * time.Second)
	post(c, notice(s), notice(os))
	if got := notifiedAt(t, h, mailbox); !got.Equal(h.Now()) {
		t.Errorf("after five seconds: notified at %s, want %s", got, h.Now())
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.connection WHERE last_notification_at = $1`, first); n != 1 {
		t.Error("the last notification moved within the minute")
	}

	h.Advance(time.Second)
	post(c, notice(s))
	h.Advance(2 * time.Minute)
	post(c, notice(os))
	if n := h.Count(t, `SELECT count(*) FROM entra.connection WHERE last_notification_at = $1`, h.Now()); n != 1 {
		t.Error("the last notification did not move after the minute")
	}
	notices, err := mailboxProvider(h).Notified(ctx, first.Add(-time.Second))
	if err != nil || len(notices) != 2 || notices[0].Mailbox != mailbox || notices[1].Mailbox != other {
		t.Errorf("Notified = %+v %v, want %s then %s", notices, err, mailbox, other)
	}
}

// An item is ignored — nothing marked — for a wrong clientState, an unknown
// subscription, an id that is no GUID (or no canonical one), another
// tenant, none, or an item that is no object; every body that decodes is
// answered 202 with no body, the same as a valid one, and the ignored are
// counted at warning with no id, clientState or resource.
func TestMailboxNotifications_InvalidItemsAreIgnoredAlike(t *testing.T) {
	h, g, _ := newNoticesHarness(t)
	c := h.Client(t)
	mailbox, s := watched(t, h, g)
	set := func(key string, v any) func(map[string]any) { return func(it map[string]any) { it[key] = v } }

	valid := post(c, notice(s))
	h.Exec(t, `UPDATE entra.mailbox_subscriptions SET notified_at = NULL`)
	h.Exec(t, `UPDATE entra.connection SET last_notification_at = NULL`)
	h.Advance(time.Hour)
	for name, item := range map[string]map[string]any{
		"a wrong clientState":   notice(s, set("clientState", strings.Repeat("x", 43))),
		"no clientState":        notice(s, set("clientState", nil)),
		"a clientState number":  notice(s, set("clientState", 42)),
		"an unknown id":         notice(s, set("subscriptionId", "1f0e9d8c-7b6a-4958-8a7b-6c5d4e3f2a1b")),
		"an id that is no GUID": notice(s, set("subscriptionId", "not-a-guid")),
		"a GUID in braces":      notice(s, set("subscriptionId", "{"+s.ID+"}")),
		"a GUID with no dashes": notice(s, set("subscriptionId", strings.ReplaceAll(s.ID, "-", ""))),
		"another tenant":        notice(s, set("tenantId", "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d")),
		"no tenant":             notice(s, set("tenantId", "")),
		"a tenant in braces":    notice(s, set("tenantId", "{"+tenantID+"}")),
	} {
		r := post(c, item)
		if r.Status != valid.Status || !bytes.Equal(r.Body, valid.Body) {
			t.Errorf("%s: status %d body %q, want the valid one's %d %q", name, r.Status, r.Body, valid.Status, valid.Body)
		}
	}
	r := c.Do(http.MethodPost, noticesPath, map[string]any{"value": []any{"a string", 7, nil, []any{}}})
	if r.Status != http.StatusAccepted || len(r.Body) != 0 {
		t.Errorf("items that are no objects: status %d body %q, want 202", r.Status, r.Body)
	}
	if !notifiedAt(t, h, mailbox).IsZero() || h.Count(t, `SELECT count(*) FROM entra.connection WHERE last_notification_at IS NOT NULL`) != 0 {
		t.Error("an invalid item marked the row")
	}
	logs := h.Logs()
	if !strings.Contains(logs, `"msg":"entra: change notifications ignored","ignored":1`) {
		t.Errorf("the ignored are not counted at warning:\n%s", logs)
	}
	for _, secret := range []string{s.ClientState, s.ID, strings.ToUpper(s.ID), "AAMkAGI2TG93AAA="} {
		if strings.Contains(logs, secret) {
			t.Errorf("the logs carry %q", secret)
		}
	}
}

// The lifecycle events: reauthorizationRequired marks the row reauthorize —
// even within five seconds of a notification — and the next Watch renews
// it; subscriptionRemoved marks it removed, and the next Watch creates; missed is a
// notification like any other. Each marks the mailbox notified.
func TestMailboxNotifications_Lifecycle(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newNoticesHarness(t)
	c := h.Client(t)
	p := mailboxProvider(h)
	mailbox, s := watched(t, h, g)
	lifecycle := func(event string) func(map[string]any) {
		return func(it map[string]any) {
			for _, k := range []string{"changeType", "resource", "resourceData"} {
				delete(it, k)
			}
			it["lifecycleEvent"] = event
		}
	}
	state := func() string {
		var st string
		if err := h.Pool().QueryRow(ctx, `SELECT state FROM entra.mailbox_subscriptions WHERE object_id = $1`, mailbox).Scan(&st); err != nil {
			t.Fatal(err)
		}
		return st
	}

	post(c, notice(s))
	post(c, notice(s, lifecycle("reauthorizationRequired")))
	if state() != "reauthorize" {
		t.Errorf("after reauthorizationRequired: %s, want reauthorize", state())
	}
	before := len(g.Calls())
	if err := p.Watch(ctx, mailbox); err != nil {
		t.Fatal(err)
	}
	if got := methods(subscriptionCalls(g.Calls()[before:])); strings.Join(got, ",") != "PATCH OK" || state() != "active" {
		t.Errorf("Watch after reauthorizationRequired: %v, %s; want a renewal, active", got, state())
	}

	h.Advance(10 * time.Second)
	post(c, notice(s, lifecycle("missed")))
	if state() != "active" || !notifiedAt(t, h, mailbox).Equal(h.Now()) {
		t.Errorf("after missed: %s at %s, want active and notified now", state(), notifiedAt(t, h, mailbox))
	}

	h.Advance(time.Second)
	g.RemoveSubscription(s.ID)
	post(c, notice(s, lifecycle("subscriptionRemoved")), notice(s, lifecycle("reauthorizationRequired")))
	if state() != "removed" || !notifiedAt(t, h, mailbox).Equal(h.Now()) {
		t.Errorf("after subscriptionRemoved: %s, want removed and notified now", state())
	}
	before = len(g.Calls())
	if err := p.Watch(ctx, mailbox); err != nil {
		t.Fatal(err)
	}
	if got := methods(subscriptionCalls(g.Calls()[before:])); strings.Join(got, ",") != "POST Created" || state() != "active" {
		t.Errorf("Watch after subscriptionRemoved: %v, %s; want a create, active", got, state())
	}
}

// At most 1000 items are read: a valid item at the 1001st place is
// ignored, at the 1000th taken.
func TestMailboxNotifications_AThousandItems(t *testing.T) {
	h, g, _ := newNoticesHarness(t)
	c := h.Client(t)
	mailbox, s := watched(t, h, g)
	items := make([]map[string]any, 1001)
	for i := range items {
		items[i] = notice(s, func(it map[string]any) { it["clientState"] = "wrong" })
	}
	items[1000] = notice(s)
	if r := post(c, items...); r.Status != http.StatusAccepted {
		t.Fatalf("status %d", r.Status)
	}
	if !notifiedAt(t, h, mailbox).IsZero() {
		t.Error("the 1001st item was read")
	}
	items[999] = notice(s)
	post(c, items[:1000]...)
	if notifiedAt(t, h, mailbox).IsZero() {
		t.Error("the 1000th item was not read")
	}
}

// A body that is not {"value": [...]} is 400: one that is not JSON, one too
// large for the 1 MiB cap, an array, an object with no value or with a
// value that is no array, and none at all.
func TestMailboxNotifications_BodiesRefused(t *testing.T) {
	h, _, _ := newNoticesHarness(t)
	c := h.Client(t)
	large := []byte(`{"value":["` + strings.Repeat("x", 1<<20) + `"]}`)
	for name, r := range map[string]*modtest.Response{
		"not JSON":   c.Do(http.MethodPost, noticesPath, nil, modtest.RawBody("application/json", []byte(`{"value": [`)), modtest.SkipContract("a body that is not JSON")),
		"too large":  c.Do(http.MethodPost, noticesPath, nil, modtest.RawBody("application/json", large), modtest.SkipContract("a body past the cap")),
		"an array":   c.Do(http.MethodPost, noticesPath, nil, modtest.RawBody("application/json", []byte(`[{"value":[]}]`)), modtest.SkipContract("a body that is no object")),
		"no value":   c.Do(http.MethodPost, noticesPath, map[string]any{"values": []any{}}),
		"an object":  c.Do(http.MethodPost, noticesPath, map[string]any{"value": map[string]any{}}),
		"no body":    c.Do(http.MethodPost, noticesPath, nil),
		"plain text": c.Do(http.MethodPost, noticesPath, nil, modtest.RawBody("text/plain", []byte(`{"value":[]}`))),
	} {
		if r.Status != http.StatusBadRequest {
			t.Errorf("%s: status %d body %s, want 400", name, r.Status, r.Body)
		}
	}
	if r := c.Do(http.MethodPost, noticesPath, map[string]any{"value": []any{}}); r.Status != http.StatusAccepted {
		t.Errorf("an empty value: %d, want 202", r.Status)
	}
}

// GET /connection's calendarSync.notifications (design O11.7): not_configured
// while PUBLIC_BASE_URL is unset, off while
// CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED is 0, active otherwise with the
// live subscriptions counted, unreachable within the hour after a failed
// handshake.
func TestConnection_Notifications(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []modtest.Option
		want gen.EntraCalendarSyncNotificationsStatus
	}{
		{"no public base URL", nil, gen.EntraCalendarSyncNotificationsStatusNotConfigured},
		{"turned off", []modtest.Option{
			modtest.WithEnv("PUBLIC_BASE_URL", publicBase),
			modtest.WithEnv("CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED", "0"),
		}, gen.EntraCalendarSyncNotificationsStatusOff},
		{"on", []modtest.Option{modtest.WithEnv("PUBLIC_BASE_URL", publicBase)}, gen.EntraCalendarSyncNotificationsStatusActive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, tc.opts...)
			c, _ := manager(t, h)
			got := getConnection(t, c).CalendarSync.Notifications
			if got.Status != tc.want || got.Subscriptions != 0 || got.UnreachableAt != nil || got.LastNotificationAt != nil {
				t.Errorf("notifications = %+v, want %s and none", got, tc.want)
			}
		})
	}

	t.Run("unreachable", func(t *testing.T) {
		h, g, o := newNoticesHarness(t)
		watched(t, h, g)
		h.Exec(t, `UPDATE entra.connection SET notifications_unreachable_at = $1`, h.Now())
		got := getConnection(t, o).CalendarSync.Notifications
		if got.Status != gen.EntraCalendarSyncNotificationsStatusUnreachable || got.UnreachableAt == nil || got.Subscriptions != 1 {
			t.Errorf("notifications = %+v, want unreachable with one subscription", got)
		}
		h.Advance(61 * time.Minute)
		if got := getConnection(t, mustOwner(t, h)).CalendarSync.Notifications; got.Status != gen.EntraCalendarSyncNotificationsStatusActive || got.UnreachableAt != nil {
			t.Errorf("after the hour: %+v, want active", got)
		}
	})
}
