package graphtest_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
)

// The subscriptions (delivery D's plan reading 9), with the person's own
// delegated token (Outlook connection design M9): a create validated
// through Handshake, 400 when it fails, 409 for a second on the resource,
// 403 for a mailbox Exchange refuses, for another user's events and for the
// application's token; the creator recorded; a renewal, the list of the
// caller's own in pages, another's subscription refused, a delete, and an
// expired subscription gone.
func TestSubscriptions(t *testing.T) {
	now := time.Now()
	g := tenantFake(&now)
	u := g.AddUser(graphtest.User{UserPrincipalName: "kari@nordlys.no", AccountEnabled: true})
	denied := g.AddUser(graphtest.User{UserPrincipalName: "ola@nordlys.no", AccountEnabled: true, MailboxDenied: true})
	c := personClient(t, g, u.ID)
	app := newClient(t, g)
	hook := "https://public.example.com/hook"
	create := func(user string) map[string]any {
		return map[string]any{
			"changeType": "created,updated,deleted", "notificationUrl": hook, "lifecycleNotificationUrl": hook,
			"resource": "/users/" + user + "/events", "clientState": strings.Repeat("s", 43),
			"expirationDateTime": now.Add(10000 * time.Minute).UTC().Format(time.RFC3339),
		}
	}
	var tokens []string
	answer := http.StatusOK
	g.Configure(func(g *graphtest.FakeGraph) {
		g.Handshake = func(u, token string) (int, string) {
			tokens = append(tokens, token)
			return answer, token
		}
	})

	answer = http.StatusNotFound
	if status, _, body := c.do(http.MethodPost, "/subscriptions", create(u.ID)); status != http.StatusBadRequest ||
		!strings.Contains(body["error"].(map[string]any)["message"].(string), "validation request failed") {
		t.Errorf("a failed handshake: %d %v, want Graph's 400", status, body)
	}
	answer = http.StatusOK
	status, _, sub := c.do(http.MethodPost, "/subscriptions", create(u.ID))
	id, _ := sub["id"].(string)
	if status != http.StatusCreated || id == "" || len(tokens) != 2 || !strings.HasPrefix(tokens[1], "Validation: ") {
		t.Fatalf("a create: %d %v, handshakes %q", status, sub, tokens)
	}
	if status, _, _ := c.do(http.MethodPost, "/subscriptions", create(strings.ToUpper(u.ID))); status != http.StatusConflict {
		t.Errorf("a second on the resource: %d, want 409", status)
	}
	if subs := g.Subscriptions(); len(subs) != 1 || subs[0].CreatorID != strings.ToLower(u.ID) {
		t.Errorf("the subscriptions = %+v, want one created by %s", subs, u.ID)
	}
	if status, _, body := personClient(t, g, denied.ID).do(http.MethodPost, "/subscriptions", create(denied.ID)); status != http.StatusForbidden || errorCode(body) != "ErrorAccessDenied" {
		t.Errorf("a mailbox Exchange refuses: %d %v, want 403", status, body)
	}
	if status, _, body := c.do(http.MethodPost, "/subscriptions", create(denied.ID)); status != http.StatusForbidden || errorCode(body) != "ErrorAccessDenied" {
		t.Errorf("another user's events: %d %v, want 403", status, body)
	}
	for _, path := range []string{"/subscriptions", "/subscriptions/" + id} {
		if status, _, body := app.do(http.MethodGet, path, nil); status != http.StatusForbidden || errorCode(body) != "ErrorAccessDenied" {
			t.Errorf("the application's token on %s: %d %v, want 403", path, status, body)
		}
	}
	tooLong := create(u.ID)
	tooLong["expirationDateTime"] = now.Add(10081 * time.Minute).UTC().Format(time.RFC3339)
	if status, _, _ := c.do(http.MethodPost, "/subscriptions", tooLong); status != http.StatusBadRequest {
		t.Errorf("an expiry past 10 080 minutes: %d, want 400", status)
	}

	later := now.Add(9000 * time.Minute).UTC().Format(time.RFC3339)
	if status, _, body := c.do(http.MethodPatch, "/subscriptions/"+id, map[string]any{"expirationDateTime": later}); status != http.StatusOK || body["id"] != id {
		t.Errorf("a renewal: %d %v", status, body)
	}
	g.AddSubscription(graphtest.Subscription{Resource: "/users/" + u.ID + "/events", ChangeType: "created", NotificationURL: hook, Expiration: now.Add(time.Hour)})
	foreign := g.AddSubscription(graphtest.Subscription{Resource: "/users/" + denied.ID + "/events", ChangeType: "created", NotificationURL: hook, Expiration: now.Add(time.Hour)})
	if foreign.CreatorID != strings.ToLower(denied.ID) {
		t.Errorf("a seeded subscription's creator = %q, want its resource's user", foreign.CreatorID)
	}
	if status, _, body := c.do(http.MethodDelete, "/subscriptions/"+foreign.ID, nil); status != http.StatusForbidden || errorCode(body) != "ErrorAccessDenied" {
		t.Errorf("a delete of another user's subscription: %d %v, want 403", status, body)
	}
	g.Configure(func(g *graphtest.FakeGraph) { g.SubscriptionPageSize = 1 })
	status, _, page := c.do(http.MethodGet, "/subscriptions", nil)
	next, _ := page["@odata.nextLink"].(string)
	if status != http.StatusOK || len(page["value"].([]any)) != 1 || !strings.HasPrefix(next, "https://graph.microsoft.com/v1.0/subscriptions?") {
		t.Errorf("the first page: %d %v", status, page)
	}
	if status, _, page := c.do(http.MethodGet, strings.TrimPrefix(next, "https://graph.microsoft.com/v1.0"), nil); status != http.StatusOK || page["@odata.nextLink"] != nil {
		t.Errorf("the last page: %d %v", status, page)
	}

	if status, _, _ := c.do(http.MethodDelete, "/subscriptions/"+id, nil); status != http.StatusNoContent {
		t.Errorf("a delete: %d, want 204", status)
	}
	if status, _, body := c.do(http.MethodPatch, "/subscriptions/"+id, map[string]any{"expirationDateTime": later}); status != http.StatusNotFound || errorCode(body) != "ResourceNotFound" {
		t.Errorf("a renewal of one deleted: %d %v, want 404", status, body)
	}
	now = now.Add(2 * time.Hour)
	if subs := g.Subscriptions(); len(subs) != 0 {
		t.Errorf("an expired subscription is still held: %+v", subs)
	}
}
