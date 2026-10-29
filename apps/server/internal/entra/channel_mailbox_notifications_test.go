package entra_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

func newChannelNoticesHarness(t *testing.T, opts ...modtest.Option) (*harness, *graphtest.FakeGraph, *modtest.Client) {
	t.Helper()
	h, g := newTenantHarness(t, append([]modtest.Option{
		modtest.WithEnv("PUBLIC_BASE_URL", publicBase),
	}, opts...)...)
	o := connected(t, h)
	setMailChannels(t, o, true)
	webhook := h.Client(t)
	g.Configure(func(g *graphtest.FakeGraph) { g.Handshake = handshakeThrough(webhook) })
	return h, g, o
}

func mailSubscriptionOf(t *testing.T, g *graphtest.FakeGraph, mailbox string) graphtest.Subscription {
	t.Helper()
	wantResource := "/users/" + mailbox + "/mailFolders('inbox')/messages"
	var found []graphtest.Subscription
	for _, s := range g.Subscriptions() {
		if strings.EqualFold(s.Resource, wantResource) {
			found = append(found, s)
		}
	}
	if len(found) != 1 {
		t.Fatalf("mail subscriptions on %s = %+v, want 1", mailbox, found)
	}
	return found[0]
}

// Watch creates a subscription on /users/{oid}/mailFolders('inbox')/messages
// with changeType "created" on the application token (CreatorID ""), renews it
// when within three days of expiry, recovers from a 409 conflict, and Unwatch
// deletes it and removes the row.
func TestChannelMailbox_WatchRenewsAndUnwatches(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h, g, _ := newChannelNoticesHarness(t)
	sharedMailbox(g)
	p := channelProvider(h)
	info := checked(t, p, "post@nordlys.no")

	// Gate checks: unrecorded mailbox and non-inbox folder.
	if err := p.Watch(ctx, "00000000-0000-0000-0000-000000000099", "inbox"); !errors.Is(err, contracts.ErrMailboxUnknown) {
		t.Errorf("Watch unrecorded = %v, want ErrMailboxUnknown", err)
	}
	if err := p.Watch(ctx, info.Mailbox, "sentitems"); err == nil {
		t.Error("Watch sentitems succeeded, want error")
	}

	if err := p.Watch(ctx, info.Mailbox, "inbox"); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	sub := mailSubscriptionOf(t, g, info.Mailbox)
	if sub.ChangeType != "created" || sub.CreatorID != "" || sub.NotificationURL != webhookURL {
		t.Errorf("mail subscription = %+v, want changeType=created, CreatorID empty", sub)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.mailbox_subscriptions WHERE object_id = $1 AND resource = 'messages' AND subscription_id = $2 AND state = 'active'`,
		info.Mailbox, sub.ID); n != 1 {
		t.Errorf("stored subscription row count = %d, want 1", n)
	}

	// Second Watch with >3 days remaining makes no Graph call.
	before := len(g.Calls())
	if err := p.Watch(ctx, info.Mailbox, "inbox"); err != nil {
		t.Fatalf("Watch again: %v", err)
	}
	if got := subscriptionCalls(g.Calls()[before:]); len(got) != 0 {
		t.Errorf("Watch with live subscription called %+v, want none", got)
	}

	// Advance clock to within 3 days of expiry -> PATCH renewal.
	h.Advance(10000*time.Minute - 48*time.Hour)
	before = len(g.Calls())
	if err := p.Watch(ctx, info.Mailbox, "inbox"); err != nil {
		t.Fatalf("Watch renew: %v", err)
	}
	if got := methods(subscriptionCalls(g.Calls()[before:])); !slices.Equal(got, []string{"PATCH OK"}) {
		t.Errorf("Watch renewal calls = %v, want [PATCH OK]", got)
	}

	// Simulate lost DB row while Graph still holds the subscription -> 409 cleared and recreated.
	h.Exec(t, `DELETE FROM entra.mailbox_subscriptions WHERE object_id = $1 AND resource = 'messages'`, info.Mailbox)
	if err := p.Watch(ctx, info.Mailbox, "inbox"); err != nil {
		t.Fatalf("Watch after lost row (409 conflict recovery): %v", err)
	}
	recreated := mailSubscriptionOf(t, g, info.Mailbox)
	if recreated.ID == sub.ID {
		t.Errorf("recreated subscription kept old ID %s", sub.ID)
	}

	// Unwatch deletes the subscription in Graph and drops the DB row.
	if err := p.Unwatch(ctx, info.Mailbox, "inbox"); err != nil {
		t.Fatalf("Unwatch: %v", err)
	}
	if subs := g.Subscriptions(); len(subs) != 0 {
		t.Errorf("Graph subscriptions after Unwatch = %+v, want none", subs)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.mailbox_subscriptions WHERE object_id = $1 AND resource = 'messages'`, info.Mailbox); n != 0 {
		t.Errorf("DB rows after Unwatch = %d, want 0", n)
	}

	// Switch off refuses Watch with ErrMailboxUnavailable.
	setMailChannels(t, mustOwner(t, h), false)
	if err := p.Watch(ctx, info.Mailbox, "inbox"); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Watch with switch off = %v, want ErrMailboxUnavailable", err)
	}
}

// A notification on a 'messages' subscription is answered by
// ChannelMailbox.Notified and not by MailboxCalendar.Notified, and vice versa
// for an 'events' subscription (Testing › E).
func TestChannelMailbox_NotifiedIsolatesMessagesAndEvents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h, g, _ := newNoticesHarness(t)
	setMailChannels(t, mustOwner(t, h), true)

	calProvider := mailboxProvider(h)
	mailProvider := channelProvider(h)

	personID := mailboxUser(t, h, g)
	sharedMailbox(g)
	mailInfo := checked(t, mailProvider, "post@nordlys.no")

	if err := calProvider.Watch(ctx, personID); err != nil {
		t.Fatalf("calProvider.Watch: %v", err)
	}
	if err := mailProvider.Watch(ctx, mailInfo.Mailbox, "inbox"); err != nil {
		t.Fatalf("mailProvider.Watch: %v", err)
	}
	calSub := subscriptionOf(t, g, personID)
	mailSub := mailSubscriptionOf(t, g, mailInfo.Mailbox)

	since := h.Now().Add(-time.Minute)
	webhook := h.Client(t)

	// Deliver a notification for the mail subscription only.
	r := webhook.Do(http.MethodPost, noticesPath, map[string]any{
		"value": []map[string]any{{
			"subscriptionId": mailSub.ID,
			"clientState":    mailSub.ClientState,
			"tenantId":       g.TenantID,
			"changeType":     "created",
			"resource":       "Users/" + mailInfo.Mailbox + "/Messages/AAMk1",
		}},
	})
	if r.Status != http.StatusAccepted {
		t.Fatalf("webhook mail notification: %d %s", r.Status, r.Body)
	}

	mailNotices, err := mailProvider.Notified(ctx, since)
	if err != nil || len(mailNotices) != 1 || mailNotices[0].Mailbox != mailInfo.Mailbox {
		t.Fatalf("mailProvider.Notified = %+v, %v; want [%s]", mailNotices, err, mailInfo.Mailbox)
	}
	calNotices, err := calProvider.Notified(ctx, since)
	if err != nil || len(calNotices) != 0 {
		t.Fatalf("calProvider.Notified after mail notice = %+v, %v; want empty", calNotices, err)
	}

	// Deliver a notification for the calendar events subscription only.
	h.Advance(10 * time.Second)
	afterMail := h.Now().Add(-time.Second)
	r = webhook.Do(http.MethodPost, noticesPath, map[string]any{
		"value": []map[string]any{{
			"subscriptionId": calSub.ID,
			"clientState":    calSub.ClientState,
			"tenantId":       g.TenantID,
			"changeType":     "updated",
			"resource":       "Users/" + personID + "/Events/AAMk2",
		}},
	})
	if r.Status != http.StatusAccepted {
		t.Fatalf("webhook calendar notification: %d %s", r.Status, r.Body)
	}

	calNotices, err = calProvider.Notified(ctx, afterMail)
	if err != nil || len(calNotices) != 1 || calNotices[0].Mailbox != personID {
		t.Fatalf("calProvider.Notified = %+v, %v; want [%s]", calNotices, err, personID)
	}
	mailNoticesAfter, err := mailProvider.Notified(ctx, afterMail)
	if err != nil || len(mailNoticesAfter) != 0 {
		t.Fatalf("mailProvider.Notified after calendar notice = %+v, %v; want empty", mailNoticesAfter, err)
	}
}
