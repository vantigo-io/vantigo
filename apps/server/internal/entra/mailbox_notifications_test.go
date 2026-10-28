package entra_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The mailbox provider's change notifications (calendar Outlook sync design
// O11.2–O11.4, O11.7) against the fake Graph: Watch's create, its renewal,
// its conflict and its refusals, the handshake failing; Unwatch; and the
// status GET /connection answers.

// The installation's public address, and the webhook's under it.
const (
	publicBase = "https://public.example.com"
	webhookURL = publicBase + noticesPath
)

// newNoticesHarness is newMailboxHarness with PUBLIC_BASE_URL set — the
// provider offering notifications — and the fake's handshake made through
// the module's own webhook, as Microsoft makes it: POST
// {notificationUrl}?validationToken=… with an empty text/plain body, the
// token answered back.
func newNoticesHarness(t *testing.T, opts ...modtest.Option) (*harness, *graphtest.FakeGraph, *modtest.Client) {
	t.Helper()
	h, g, o := newMailboxHarness(t, append([]modtest.Option{modtest.WithEnv("PUBLIC_BASE_URL", publicBase)}, opts...)...)
	webhook := h.Client(t)
	g.Configure(func(g *graphtest.FakeGraph) { g.Handshake = handshakeThrough(webhook) })
	return h, g, o
}

// handshakeThrough is a Handshake answered by the module's webhook through
// c; a URL other than webhookURL is unreachable.
func handshakeThrough(c *modtest.Client) func(string, string) (int, string) {
	return func(u, token string) (int, string) {
		if u != webhookURL {
			return http.StatusNotFound, ""
		}
		r := c.Do(http.MethodPost, noticesPath+"?validationToken="+url.QueryEscape(token), nil, modtest.RawBody("text/plain; charset=utf-8", nil))
		return r.Status, string(r.Body)
	}
}

// subscriptionCalls are the calls made to /subscriptions among calls.
func subscriptionCalls(calls []graphtest.Call) []graphtest.Call {
	var out []graphtest.Call
	for _, c := range calls {
		if strings.HasPrefix(c.Path, "/v1.0/subscriptions") {
			out = append(out, c)
		}
	}
	return out
}

// methods is calls' methods with their statuses, "POST 201".
func methods(calls []graphtest.Call) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.Method + " " + http.StatusText(c.Status)
	}
	return out
}

// subscriptionOf is the fake's one subscription on mailbox's events.
func subscriptionOf(t *testing.T, g *graphtest.FakeGraph, mailbox string) graphtest.Subscription {
	t.Helper()
	var found []graphtest.Subscription
	for _, s := range g.Subscriptions() {
		if strings.EqualFold(s.Resource, "/users/"+mailbox+"/events") {
			found = append(found, s)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the subscriptions on %s: %+v, want one", mailbox, found)
	}
	return found[0]
}

// A create (design O11.2): the body Graph took — the mailbox's events, the
// three change types, both URLs the webhook, an expiry 10 000 minutes ahead
// and a 43-character clientState — the handshake answered through the
// window and the window closed after; the row holds Graph's id, the expiry
// and the clientState's SHA-256, never the clientState. A second Watch
// calls nothing.
func TestMailboxWatch_Creates(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newNoticesHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	var answered []int
	webhook := h.Client(t)
	g.Configure(func(g *graphtest.FakeGraph) {
		through := handshakeThrough(webhook)
		g.Handshake = func(u, token string) (int, string) {
			status, body := through(u, token)
			answered = append(answered, status)
			return status, body
		}
	})

	if err := p.Watch(ctx, mailbox); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	s := subscriptionOf(t, g, mailbox)
	if s.ChangeType != "created,updated,deleted" || s.NotificationURL != webhookURL || s.LifecycleNotificationURL != webhookURL {
		t.Errorf("the subscription: %+v", s)
	}
	if want := h.Now().Add(10000 * time.Minute); !s.Expiration.Equal(want.Truncate(time.Second)) || s.Expiration.After(h.Now().Add(10080*time.Minute)) {
		t.Errorf("expires %s, want %s, under the 10 080 minutes", s.Expiration, want)
	}
	if raw, err := base64.RawURLEncoding.DecodeString(s.ClientState); len(s.ClientState) != 43 || err != nil || len(raw) != 32 {
		t.Errorf("the clientState %q: %d characters, %v", s.ClientState, len(s.ClientState), err)
	}
	if len(answered) != 1 || answered[0] != http.StatusOK {
		t.Errorf("the handshakes answered %v, want one 200", answered)
	}
	hash := sha256.Sum256([]byte(s.ClientState))
	if n := h.Count(t, `SELECT count(*) FROM entra.mailbox_subscriptions
	                    WHERE object_id = $1 AND subscription_id = $2 AND client_state_hash = $3 AND expires_at = $4 AND state = 'active'
	                      AND notification_url = $5`,
		mailbox, s.ID, hash[:], s.Expiration, webhookURL); n != 1 {
		t.Errorf("the row of %s is not the subscription", mailbox)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.mailbox_subscriptions m WHERE m::text LIKE '%' || $1 || '%'`, s.ClientState); n != 0 {
		t.Error("the clientState is stored plain")
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.connection WHERE notifications_validating_until IS NULL AND notifications_unreachable_at IS NULL`); n != 1 {
		t.Error("the handshake's window is left open, or the webhook marked unreachable")
	}
	if r := webhook.Do(http.MethodPost, noticesPath+"?validationToken=late", nil, modtest.RawBody("text/plain", nil)); r.Status != http.StatusNotFound {
		t.Errorf("a handshake after the create: %d, want 404", r.Status)
	}

	before := len(g.Calls())
	if err := p.Watch(ctx, mailbox); err != nil {
		t.Fatalf("Watch again: %v", err)
	}
	if calls := g.Calls()[before:]; len(calls) != 0 {
		t.Errorf("a live subscription with days left called %+v", calls)
	}
	if got := getConnection(t, mustOwner(t, h)).CalendarSync.Notifications; got.Status != gen.EntraCalendarSyncNotificationsStatusActive || got.Subscriptions != 1 {
		t.Errorf("notifications = %+v, want active with one", got)
	}
}

// mustOwner is a signed-in Owner of h.
func mustOwner(t *testing.T, h *harness) *modtest.Client {
	t.Helper()
	o, _ := owner(t, h)
	return o
}

// A subscription with three days or less left is renewed (PATCH), as is one
// the directory asked to reauthorize; a renewal Graph answers 404 forgets
// the row and creates.
func TestMailboxWatch_Renews(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newNoticesHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	if err := p.Watch(ctx, mailbox); err != nil {
		t.Fatal(err)
	}
	first := subscriptionOf(t, g, mailbox)

	h.Advance(3*24*time.Hour + 22*time.Hour)
	before := len(g.Calls())
	if err := p.Watch(ctx, mailbox); err != nil {
		t.Fatalf("Watch with more than three days left: %v", err)
	}
	if calls := subscriptionCalls(g.Calls()[before:]); len(calls) != 0 {
		t.Errorf("renewed early: %v", methods(calls))
	}
	h.Advance(3 * time.Hour)
	if err := p.Watch(ctx, mailbox); err != nil {
		t.Fatalf("Watch within three days: %v", err)
	}
	renewed := subscriptionOf(t, g, mailbox)
	if renewed.ID != first.ID || !renewed.Expiration.Equal(h.Now().Add(10000*time.Minute).Truncate(time.Second)) {
		t.Errorf("renewed: %+v, want %s to %s", renewed, first.ID, h.Now().Add(10000*time.Minute))
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.mailbox_subscriptions WHERE object_id = $1 AND expires_at = $2`, mailbox, renewed.Expiration); n != 1 {
		t.Error("the row's expiry did not move")
	}
	if calls := subscriptionCalls(g.Calls()[before:]); len(calls) != 1 || calls[0].Method != http.MethodPatch || calls[0].Path != "/v1.0/subscriptions/"+first.ID {
		t.Errorf("the renewal: %v", methods(calls))
	}

	// Reauthorize: renewed with days left, the row active again.
	h.Exec(t, `UPDATE entra.mailbox_subscriptions SET state = 'reauthorize' WHERE object_id = $1`, mailbox)
	before = len(g.Calls())
	if err := p.Watch(ctx, mailbox); err != nil {
		t.Fatal(err)
	}
	if calls := subscriptionCalls(g.Calls()[before:]); len(calls) != 1 || calls[0].Method != http.MethodPatch {
		t.Errorf("reauthorize: %v, want one PATCH", methods(calls))
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.mailbox_subscriptions WHERE object_id = $1 AND state = 'active'`, mailbox); n != 1 {
		t.Error("the row is not active after the renewal")
	}

	// Graph no longer holds it: 404, then a create.
	g.RemoveSubscription(first.ID)
	h.Exec(t, `UPDATE entra.mailbox_subscriptions SET state = 'reauthorize' WHERE object_id = $1`, mailbox)
	before = len(g.Calls())
	if err := p.Watch(ctx, mailbox); err != nil {
		t.Fatal(err)
	}
	if got := methods(subscriptionCalls(g.Calls()[before:])); strings.Join(got, ",") != "PATCH Not Found,POST Created" {
		t.Errorf("a renewal of one gone: %v, want the PATCH's 404 then a create", got)
	}
	again := subscriptionOf(t, g, mailbox)
	if again.ID == first.ID {
		t.Error("the same subscription")
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.mailbox_subscriptions WHERE object_id = $1 AND subscription_id = $2`, mailbox, again.ID); n != 1 {
		t.Error("the row does not hold the new subscription")
	}

	// Removed by Graph: a create, the old one's row replaced.
	g.RemoveSubscription(again.ID)
	h.Exec(t, `UPDATE entra.mailbox_subscriptions SET state = 'removed' WHERE object_id = $1`, mailbox)
	before = len(g.Calls())
	if err := p.Watch(ctx, mailbox); err != nil {
		t.Fatal(err)
	}
	if got := methods(subscriptionCalls(g.Calls()[before:])); strings.Join(got, ",") != "POST Created" {
		t.Errorf("a removed subscription: %v, want a create", got)
	}
}

// A create answered 409 — Graph holds this application's subscription on
// the mailbox, its row lost — lists the subscriptions page by page, deletes
// this installation's on that mailbox (the resource in any case and form)
// and creates once more; another mailbox's is kept. One on the mailbox with
// another installation's URL is never deleted, and the create then fails.
func TestMailboxWatch_Conflict(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newNoticesHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	other := mailboxUser(t, h, g)
	expires := h.Now().Add(24 * time.Hour)
	otherKept := g.AddSubscription(graphtest.Subscription{Resource: "/users/" + other + "/events", ChangeType: "created,updated,deleted",
		NotificationURL: webhookURL, Expiration: expires})
	lost := g.AddSubscription(graphtest.Subscription{Resource: "Users/" + strings.ToUpper(mailbox) + "/events", ChangeType: "created,updated,deleted",
		NotificationURL: webhookURL, Expiration: expires})
	g.AddSubscription(graphtest.Subscription{Resource: "/users/" + other + "/events", ChangeType: "created",
		NotificationURL: "https://another.example.com/hook", Expiration: expires})
	g.Configure(func(g *graphtest.FakeGraph) { g.SubscriptionPageSize = 1 })

	if err := p.Watch(ctx, mailbox); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	want := "POST Conflict,GET OK,GET OK,GET OK,DELETE No Content,POST Created"
	if got := strings.Join(methods(subscriptionCalls(g.Calls())), ","); got != want {
		t.Errorf("calls: %s, want %s", got, want)
	}
	made := subscriptionOf(t, g, mailbox)
	if made.ID == lost.ID {
		t.Error("the lost subscription kept")
	}
	if n := len(g.Subscriptions()); n != 3 {
		t.Errorf("%d subscriptions, want the other mailbox's two and the new one", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.mailbox_subscriptions WHERE object_id = $1 AND subscription_id = $2`, mailbox, made.ID); n != 1 {
		t.Error("the row does not hold the new subscription")
	}
	kept := false
	for _, s := range g.Subscriptions() {
		kept = kept || s.ID == otherKept.ID
	}
	if !kept {
		t.Error("another mailbox's subscription was deleted")
	}

	// Another installation's on a third mailbox: left alone, the row marked
	// conflict and nil answered; for a day no Watch of that mailbox calls
	// anything, and after it the create is tried again.
	third := mailboxUser(t, h, g)
	foreign := g.AddSubscription(graphtest.Subscription{Resource: "/users/" + third + "/events", ChangeType: "created,updated,deleted",
		NotificationURL: "https://another.example.com/api/v1/entra/mailbox-notifications", Expiration: expires.Add(48 * time.Hour)})
	if err := p.Watch(ctx, third); err != nil {
		t.Errorf("Watch beside another installation's: %v, want nil", err)
	}
	if subscriptionOf(t, g, third).ID != foreign.ID {
		t.Error("another installation's subscription was deleted")
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.mailbox_subscriptions WHERE object_id = $1 AND state = 'conflict' AND subscription_id IS NULL`, third); n != 1 {
		t.Error("the conflict is not recorded on the row")
	}
	h.Advance(23 * time.Hour)
	before := len(g.Calls())
	if err := p.Watch(ctx, third); err != nil || len(g.Calls()) != before {
		t.Errorf("Watch within the day: %v, called %v", err, methods(g.Calls()[before:]))
	}
	h.Advance(2 * time.Hour)
	before = len(g.Calls())
	if err := p.Watch(ctx, third); err != nil {
		t.Errorf("Watch after the day: %v", err)
	}
	if got := methods(subscriptionCalls(g.Calls()[before:])); len(got) == 0 || got[0] != "POST Conflict" {
		t.Errorf("Watch after the day called %v, want the create tried again", got)
	}
}

// A subscription made for another address — PUBLIC_BASE_URL has moved since
// its create — is never renewed: Watch deletes it, by the row's id, and
// creates one at the present address, which the row then holds; a delete
// Graph answers 404 is done. A delete refused keeps the row, and its
// refusal is answered.
func TestMailboxWatch_AMovedAddressIsResubscribed(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newNoticesHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	const oldURL = "https://old.example.com" + noticesPath
	moved := func() graphtest.Subscription {
		t.Helper()
		if err := p.Watch(ctx, mailbox); err != nil {
			t.Fatal(err)
		}
		s := subscriptionOf(t, g, mailbox)
		h.Exec(t, `UPDATE entra.mailbox_subscriptions SET notification_url = $2 WHERE object_id = $1`, mailbox, oldURL)
		return s
	}

	old := moved()
	before := len(g.Calls())
	if err := p.Watch(ctx, mailbox); err != nil {
		t.Fatalf("Watch after the move: %v", err)
	}
	if got := strings.Join(methods(subscriptionCalls(g.Calls()[before:])), ","); got != "DELETE No Content,POST Created" {
		t.Errorf("Watch after the move called %s, want the old one deleted and a create", got)
	}
	made := subscriptionOf(t, g, mailbox)
	if made.ID == old.ID || made.NotificationURL != webhookURL {
		t.Errorf("the subscription after the move: %+v", made)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.mailbox_subscriptions WHERE object_id = $1 AND subscription_id = $2 AND notification_url = $3`,
		mailbox, made.ID, webhookURL); n != 1 {
		t.Error("the row does not hold the new subscription at the present address")
	}

	// Gone from Graph already: the 404 is done, and the create follows.
	g.RemoveSubscription(moved().ID)
	before = len(g.Calls())
	if err := p.Watch(ctx, mailbox); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(methods(subscriptionCalls(g.Calls()[before:])), ","); got != "DELETE Not Found,POST Created" {
		t.Errorf("Watch after the move, the old one gone: %s", got)
	}

	// Refused: answered, the row kept as it was, nothing created.
	kept := moved()
	g.EditUser(mailbox, func(u *graphtest.User) { u.MailboxDenied = true })
	before = len(g.Calls())
	if err := p.Watch(ctx, mailbox); !errors.Is(err, contracts.ErrMailboxDenied) {
		t.Errorf("Watch, the delete refused: %v, want ErrMailboxDenied", err)
	}
	if got := strings.Join(methods(subscriptionCalls(g.Calls()[before:])), ","); got != "DELETE Forbidden" {
		t.Errorf("Watch, the delete refused, called %s", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.mailbox_subscriptions WHERE object_id = $1 AND subscription_id = $2 AND notification_url = $3`,
		mailbox, kept.ID, oldURL); n != 1 {
		t.Error("the row of the refused delete was not kept")
	}
}

// A renewal's write only touches the row that still holds the subscription
// renewed and that Graph has not removed meanwhile: a removed row is never
// made active again.
func TestMailboxWatch_ARenewalNeverRevivesARemovedRow(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newNoticesHarness(t)
	mailbox := mailboxUser(t, h, g)
	if err := mailboxProvider(h).Watch(ctx, mailbox); err != nil {
		t.Fatal(err)
	}
	s := subscriptionOf(t, g, mailbox)
	q := store.New(h.Pool())
	oid := uuid.MustParse(mailbox)
	renew := func(id string) {
		t.Helper()
		if err := q.RenewMailboxSubscription(ctx, store.RenewMailboxSubscriptionParams{
			ExpiresAt: h.Now().Add(time.Hour), Now: h.Now(), ObjectID: oid, SubscriptionID: id,
		}); err != nil {
			t.Fatal(err)
		}
	}
	h.Exec(t, `UPDATE entra.mailbox_subscriptions SET state = 'removed' WHERE object_id = $1`, mailbox)
	renew(s.ID)
	if n := h.Count(t, `SELECT count(*) FROM entra.mailbox_subscriptions WHERE object_id = $1 AND state = 'removed'`, mailbox); n != 1 {
		t.Error("a renewal made a removed row active")
	}
	h.Exec(t, `UPDATE entra.mailbox_subscriptions SET state = 'reauthorize' WHERE object_id = $1`, mailbox)
	renew(uuid.NewString())
	if n := h.Count(t, `SELECT count(*) FROM entra.mailbox_subscriptions WHERE object_id = $1 AND state = 'reauthorize'`, mailbox); n != 1 {
		t.Error("a renewal of another subscription wrote the row")
	}
	renew(s.ID)
	if n := h.Count(t, `SELECT count(*) FROM entra.mailbox_subscriptions WHERE object_id = $1 AND state = 'active' AND expires_at = $2`,
		mailbox, h.Now().Add(time.Hour)); n != 1 {
		t.Error("the renewal of the row's subscription was not written")
	}
}

// A create whose handshake fails — Graph's 400, Microsoft not reaching the
// webhook — answers nil, forgets the row and marks the connection
// unreachable; for an hour no Watch calls anything, and GET /connection says
// unreachable with when. After the hour a create is tried again, and its
// success clears the mark.
func TestMailboxWatch_Unreachable(t *testing.T) {
	ctx := context.Background()
	h, g, o := newNoticesHarness(t)
	p := mailboxProvider(h)
	first := mailboxUser(t, h, g)
	second := mailboxUser(t, h, g)
	webhook := h.Client(t)
	g.Configure(func(g *graphtest.FakeGraph) {
		g.Handshake = func(string, string) (int, string) { return http.StatusBadGateway, "" }
	})

	if err := p.Watch(ctx, first); err != nil {
		t.Fatalf("Watch, the handshake failing: %v", err)
	}
	if len(g.Subscriptions()) != 0 || h.Count(t, `SELECT count(*) FROM entra.mailbox_subscriptions`) != 0 {
		t.Error("a subscription or its row is left")
	}
	failedAt := h.Now()
	notifications := getConnection(t, o).CalendarSync.Notifications
	if notifications.Status != gen.EntraCalendarSyncNotificationsStatusUnreachable || notifications.UnreachableAt == nil || !notifications.UnreachableAt.Equal(failedAt) {
		t.Errorf("notifications = %+v, want unreachable since %s", notifications, failedAt)
	}

	h.Advance(59 * time.Minute)
	before := len(g.Calls())
	if err := p.Watch(ctx, second); err != nil {
		t.Fatalf("Watch within the hour: %v", err)
	}
	if calls := g.Calls()[before:]; len(calls) != 0 {
		t.Errorf("Watch within the hour called %+v", calls)
	}

	g.Configure(func(g *graphtest.FakeGraph) { g.Handshake = handshakeThrough(webhook) })
	h.Advance(2 * time.Minute)
	if err := p.Watch(ctx, second); err != nil {
		t.Fatalf("Watch after the hour: %v", err)
	}
	subscriptionOf(t, g, second)
	notifications = getConnection(t, o).CalendarSync.Notifications
	if notifications.Status != gen.EntraCalendarSyncNotificationsStatusActive || notifications.UnreachableAt != nil || notifications.Subscriptions != 1 {
		t.Errorf("notifications = %+v, want active with one", notifications)
	}
}

// Watch's refusals: a mailbox outside the scope ErrMailboxDenied, one gone
// ErrMailboxGone, an account the module does not manage ErrMailboxUnknown
// with nothing called, cleanup ErrMailboxUnavailable; and nil with nothing
// called while notifications are off or PUBLIC_BASE_URL unset.
func TestMailboxWatch_Refusals(t *testing.T) {
	ctx := context.Background()
	h, g, o := newNoticesHarness(t)
	p := mailboxProvider(h)
	denied := mailboxUser(t, h, g, func(u *graphtest.User) { u.MailboxDenied = true })
	noMailbox := mailboxUser(t, h, g, func(u *graphtest.User) { u.NoMailbox = true })
	missing := addAccount(t, h, "E900", "gone@nordlys.no").String()
	stranger := "6a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	for name, c := range map[string]struct {
		mailbox string
		want    error
	}{
		"denied": {denied, contracts.ErrMailboxDenied}, "no mailbox": {noMailbox, contracts.ErrMailboxGone},
		"no such user": {missing, contracts.ErrMailboxGone}, "no account": {stranger, contracts.ErrMailboxUnknown},
		"no object id": {"kari@nordlys.no", contracts.ErrMailboxUnknown},
	} {
		if err := p.Watch(ctx, c.mailbox); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	if n := len(subscriptionCalls(g.Calls())); n != 3 {
		t.Errorf("%d creates, want the three managed mailboxes'", n)
	}

	mailbox := mailboxUser(t, h, g)
	setCalendarSync(t, o, false)
	if err := p.Watch(ctx, mailbox); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Watch in cleanup: %v, want ErrMailboxUnavailable", err)
	}

	for name, opts := range map[string][]modtest.Option{
		"turned off":        {modtest.WithEnv("PUBLIC_BASE_URL", publicBase), modtest.WithEnv("CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED", "0")},
		"no public address": nil,
	} {
		h, g, _ := newMailboxHarness(t, opts...)
		p := mailboxProvider(h)
		mailbox := mailboxUser(t, h, g)
		if err := p.Watch(ctx, mailbox); err != nil {
			t.Errorf("%s: %v, want nil", name, err)
		}
		if calls := subscriptionCalls(g.Calls()); len(calls) != 0 {
			t.Errorf("%s: called %v", name, methods(calls))
		}
	}
}

// Unwatch (design O11.3): a mailbox not watched answers nil with nothing
// called; a live subscription is deleted (204) and its row forgotten, as is
// one Graph no longer holds (404); one past its expiry is forgotten with
// nothing called; a 403 keeps the row deleting and answers ErrMailboxDenied,
// and the next Unwatch deletes it. Allowed under cleanup; while unavailable
// an expired row is still forgotten and a live one refused.
func TestMailboxUnwatch(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newNoticesHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	rows := func() int {
		return h.Count(t, `SELECT count(*) FROM entra.mailbox_subscriptions WHERE object_id = $1`, mailbox)
	}

	if err := p.Unwatch(ctx, mailbox); err != nil || len(subscriptionCalls(g.Calls())) != 0 {
		t.Errorf("Unwatch of a mailbox not watched: %v", err)
	}

	watch := func() graphtest.Subscription {
		t.Helper()
		if err := p.Watch(ctx, mailbox); err != nil {
			t.Fatal(err)
		}
		return subscriptionOf(t, g, mailbox)
	}
	unwatch := func(want string) {
		t.Helper()
		before := len(g.Calls())
		if err := p.Unwatch(ctx, mailbox); err != nil {
			t.Fatalf("Unwatch: %v", err)
		}
		if got := strings.Join(methods(subscriptionCalls(g.Calls()[before:])), ","); got != want {
			t.Errorf("Unwatch called %q, want %q", got, want)
		}
		if rows() != 0 {
			t.Error("the row is kept")
		}
	}

	watch()
	unwatch("DELETE No Content")
	if len(g.Subscriptions()) != 0 {
		t.Error("the subscription is left")
	}

	g.RemoveSubscription(watch().ID)
	unwatch("DELETE Not Found")

	watch()
	h.Advance(10001 * time.Minute)
	unwatch("")
	o := mustOwner(t, h) // signed in now: a session of the start would be past its life

	s := watch()
	g.EditUser(mailbox, func(u *graphtest.User) { u.MailboxDenied = true })
	if err := p.Unwatch(ctx, mailbox); !errors.Is(err, contracts.ErrMailboxDenied) {
		t.Errorf("Unwatch refused: %v, want ErrMailboxDenied", err)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.mailbox_subscriptions WHERE object_id = $1 AND state = 'deleting' AND subscription_id = $2`, mailbox, s.ID); n != 1 {
		t.Error("the refused delete is not kept owed")
	}
	if got := getConnection(t, o).CalendarSync.Notifications.Subscriptions; got != 0 {
		t.Errorf("%d live subscriptions counted, want none while one is deleting", got)
	}
	g.EditUser(mailbox, func(u *graphtest.User) { u.MailboxDenied = false })
	unwatch("DELETE No Content")

	// Under cleanup the delete is made.
	watch()
	setCalendarSync(t, o, false)
	unwatch("DELETE No Content")

	// Unavailable: an expired row forgotten unasked, a live one refused.
	setCalendarSync(t, o, true)
	watch()
	h.Exec(t, `UPDATE entra.credentials SET revoked_at = $1`, h.Now())
	before := len(g.Calls())
	if err := p.Unwatch(ctx, mailbox); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Unwatch while unavailable: %v, want ErrMailboxUnavailable", err)
	}
	if rows() != 1 {
		t.Error("a live row forgotten while unavailable")
	}
	h.Exec(t, `UPDATE entra.mailbox_subscriptions SET expires_at = $2 WHERE object_id = $1`, mailbox, h.Now().Add(-time.Minute))
	if err := p.Unwatch(ctx, mailbox); err != nil {
		t.Errorf("Unwatch of an expired row while unavailable: %v", err)
	}
	if rows() != 0 || len(g.Calls()) != before {
		t.Error("the expired row kept, or Graph called")
	}
}
