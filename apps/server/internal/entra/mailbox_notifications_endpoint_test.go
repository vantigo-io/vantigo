package entra_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// While the provider makes no subscription, the webhook holds no handshake
// window open — a handshake is 404 with no body — and knows no subscription,
// so a notification is 202 as one naming an unknown subscription is. It is
// anonymous: no session is needed.
func TestMailboxNotifications_NoSubscriptions(t *testing.T) {
	h := newHarness(t)
	c := h.Client(t)
	if r := c.Do(http.MethodPost, noticesPath+"?validationToken=abc", nil, modtest.RawBody("text/plain; charset=utf-8", nil)); r.Status != http.StatusNotFound || len(r.Body) != 0 {
		t.Errorf("a handshake: status %d body %q, want 404 and no body", r.Status, r.Body)
	}
	body := map[string]any{"value": []any{map[string]any{"subscriptionId": "7f1c1e8e-8d5b-4c2e-9a3f-0b1d2c3e4f50", "clientState": "x"}}}
	if r := c.Do(http.MethodPost, noticesPath, body); r.Status != http.StatusAccepted || len(r.Body) != 0 {
		t.Errorf("a notification: status %d body %q, want 202 and no body", r.Status, r.Body)
	}
}

// GET /connection's calendarSync.notifications (calendar Outlook sync design
// O11.7): not_configured while PUBLIC_BASE_URL is unset, off while
// CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED is 0, active otherwise, with no
// subscription counted.
func TestConnection_Notifications(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []modtest.Option
		want gen.EntraCalendarSyncNotificationsStatus
	}{
		{"no public base URL", nil, gen.EntraCalendarSyncNotificationsStatusNotConfigured},
		{"turned off", []modtest.Option{
			modtest.WithEnv("PUBLIC_BASE_URL", "https://public.example.com"),
			modtest.WithEnv("CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED", "0"),
		}, gen.EntraCalendarSyncNotificationsStatusOff},
		{"on", []modtest.Option{modtest.WithEnv("PUBLIC_BASE_URL", "https://public.example.com")}, gen.EntraCalendarSyncNotificationsStatusActive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, tc.opts...)
			c, _ := manager(t, h)
			got := getConnection(t, c).CalendarSync.Notifications
			if got.Status != tc.want || got.Subscriptions != 0 {
				t.Errorf("notifications = %+v, want %s and none", got, tc.want)
			}
		})
	}
}
