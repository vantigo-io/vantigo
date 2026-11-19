package identity_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// The delivery log and resend (outgoing webhooks design W22, W23, W35).

// logDelivery is WebhookDelivery as these tests read it.
type logDelivery struct {
	ID             uuid.UUID  `json:"id"`
	MessageID      string     `json:"messageId"`
	EventID        *int64     `json:"eventId"`
	EventName      string     `json:"eventName"`
	Kind           string     `json:"kind"`
	State          string     `json:"state"`
	StateReason    *string    `json:"stateReason"`
	RunAttempts    int        `json:"runAttempts"`
	MaxRunAttempts int        `json:"maxRunAttempts"`
	TotalAttempts  int        `json:"totalAttempts"`
	LastStatus     *int       `json:"lastStatus"`
	LastError      *string    `json:"lastError"`
	CompletedAt    *time.Time `json:"completedAt"`
	Resendable     bool       `json:"resendable"`
}

type logPage struct {
	Deliveries []logDelivery `json:"deliveries"`
	NextCursor *string       `json:"nextCursor"`
}

type logDetail struct {
	Delivery logDelivery `json:"delivery"`
	Body     string      `json:"body"`
	Attempts []struct {
		At         time.Time `json:"at"`
		StatusCode *int      `json:"statusCode"`
		DurationMs int       `json:"durationMs"`
		Error      *string   `json:"error"`
		Excerpt    *string   `json:"excerpt"`
		SecretUsed string    `json:"secretUsed"`
	} `json:"attempts"`
}

func deliveriesPath(integration, endpoint uuid.UUID) string {
	return webhooksPath(integration) + "/" + endpoint.String() + "/deliveries"
}

func resendPath(integration, endpoint, delivery uuid.UUID) string {
	return deliveriesPath(integration, endpoint) + "/" + delivery.String() + "/resend"
}

// settle sets a delivery's state as a finished run leaves it.
func (h *harness) settle(t testing.TB, id uuid.UUID, state, reason string) {
	t.Helper()
	var r *string
	if reason != "" {
		r = &reason
	}
	h.exec(t, `UPDATE identity.webhook_deliveries SET state = $2, state_reason = $3, next_attempt_at = NULL, completed_at = $4,
	    run_attempts = max_run_attempts, total_attempts = max_run_attempts WHERE id = $1`, id, state, r, h.now())
}

func TestWebhookLog_ListsNewestFirstByCursorWithItsFilters(t *testing.T) {
	t.Parallel()
	h, owner, _, integration := webhookHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{"events": []string{confirmed, cancelled}})
	// The ping creating the endpoint queued is in the log, kind ping (W19).
	var ids []uuid.UUID
	for i := range 4 {
		h.advance(time.Minute)
		id := h.insertDelivery(t, e.ID, int64(i+1))
		ids = append(ids, id)
	}
	h.settle(t, ids[0], "dead", "attempts_exhausted")
	h.settle(t, ids[1], "succeeded", "")
	h.exec(t, `UPDATE identity.webhook_deliveries SET event_name = $2 WHERE id = $1`, ids[2], cancelled)

	path := deliveriesPath(integration, e.ID)
	var seen []logDelivery
	cursor := ""
	for pages := 0; ; pages++ {
		q := path + "?limit=2"
		if cursor != "" {
			q += "&cursor=" + url.QueryEscape(cursor)
		}
		r := owner.do(http.MethodGet, q, nil)
		if r.status != http.StatusOK {
			t.Fatalf("list: %d %s", r.status, r.body)
		}
		if strings.Contains(string(r.body), `"body"`) {
			t.Errorf("the log answers a body: %s", r.body)
		}
		var page logPage
		r.json(&page)
		seen = append(seen, page.Deliveries...)
		if page.NextCursor == nil {
			if pages != 2 {
				t.Errorf("%d pages, want 3", pages+1)
			}
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 5 {
		t.Fatalf("%d deliveries, want 5: %+v", len(seen), seen)
	}
	for i, want := range []uuid.UUID{ids[3], ids[2], ids[1], ids[0]} {
		if seen[i].ID != want {
			t.Errorf("row %d is %s, want %s (newest first)", i, seen[i].ID, want)
		}
	}
	ping := seen[4]
	if ping.Kind != "ping" || ping.EventName != "webhook.ping" || ping.EventID != nil || ping.MaxRunAttempts != 1 || ping.Resendable {
		t.Errorf("the ping %+v", ping)
	}
	if d := seen[3]; d.MessageID != msgID(ids[0]) || d.State != "dead" || d.StateReason == nil || *d.StateReason != "attempts_exhausted" ||
		d.RunAttempts != 8 || d.TotalAttempts != 8 || d.EventID == nil || *d.EventID != 1 || !d.Resendable || d.CompletedAt == nil {
		t.Errorf("a dead delivery %+v", d)
	}
	if d := seen[0]; d.State != "pending" || d.Resendable {
		t.Errorf("a pending delivery %+v", d)
	}

	for filter, want := range map[string][]uuid.UUID{
		"state=dead":                       {ids[0]},
		"state=succeeded":                  {ids[1]},
		"event=" + cancelled:               {ids[2]},
		"state=pending&event=" + confirmed: {ids[3]},
		"event=webhook.ping":               {ping.ID},
		"state=skipped":                    {},
		"event=" + overdue:                 {},
	} {
		var page logPage
		r := owner.do(http.MethodGet, path+"?"+filter, nil)
		if r.status != http.StatusOK {
			t.Errorf("%s: %d %s", filter, r.status, r.body)
			continue
		}
		r.json(&page)
		got := make([]uuid.UUID, 0, len(page.Deliveries))
		for _, d := range page.Deliveries {
			got = append(got, d.ID)
		}
		if len(got) != len(want) || (len(want) > 0 && got[0] != want[0]) || page.NextCursor != nil {
			t.Errorf("%s: %v, want %v", filter, got, want)
		}
	}
	for _, bad := range []string{"cursor=nope", "cursor=" + url.QueryEscape("eyJ0IjoxfQ"), "limit=0", "limit=101", "state=lost"} {
		if r := owner.do(http.MethodGet, path+"?"+bad, nil, skipContract("a refused parameter")); r.status != http.StatusBadRequest {
			t.Errorf("%s: %d %s", bad, r.status, r.body)
		}
	}
}

func TestWebhookLog_IsAnOwnersAndScopedByIntegrationAndEndpoint(t *testing.T) {
	t.Parallel()
	h, owner, _, integration := webhookHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{})
	other, _ := createWebhook(t, owner, integration, map[string]any{"url": "https://b.example.com/in"})
	role := h.insertRole(t, "Other", "orders:access")
	elsewhere := createIntegration(t, owner, "Elsewhere", role).Integration.ID
	foreign, _ := createWebhook(t, owner, elsewhere, map[string]any{})
	mine := h.insertDelivery(t, e.ID, 1)
	h.settle(t, mine, "dead", "attempts_exhausted")
	theirs := h.insertDelivery(t, foreign.ID, 1)
	h.settle(t, theirs, "dead", "attempts_exhausted")

	if r := owner.do(http.MethodGet, deliveriesPath(integration, e.ID)+"/"+mine.String(), nil); r.status != http.StatusOK {
		t.Fatalf("own delivery: %d %s", r.status, r.body)
	}
	// A delivery is found only under its own endpoint of its own integration.
	for name, path := range map[string]string{
		"another endpoint's delivery":            deliveriesPath(integration, e.ID) + "/" + theirs.String(),
		"under a sibling endpoint":               deliveriesPath(integration, other.ID) + "/" + mine.String(),
		"under another integration":              deliveriesPath(elsewhere, e.ID) + "/" + mine.String(),
		"the endpoint under another integration": deliveriesPath(elsewhere, e.ID),
		"no such integration":                    deliveriesPath(uuid.New(), e.ID),
		"no such delivery":                       deliveriesPath(integration, e.ID) + "/" + uuid.New().String(),
	} {
		if r := owner.do(http.MethodGet, path, nil); r.status != http.StatusNotFound {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	for name, path := range map[string]string{
		"another endpoint's":        resendPath(integration, e.ID, theirs),
		"under a sibling endpoint":  resendPath(integration, other.ID, mine),
		"under another integration": resendPath(elsewhere, e.ID, mine),
	} {
		if r := owner.do(http.MethodPost, path, nil); r.status != http.StatusNotFound {
			t.Errorf("resend %s: %d %s", name, r.status, r.body)
		}
	}
	if d := h.delivery(t, theirs); d.State != "dead" {
		t.Errorf("another endpoint's delivery was resent: %+v", d)
	}

	// No one but an Owner reads or resends, and no API key does.
	person := h.signIn(t, h.createUser(t, owner, "person@example.test", identity.RoleUser), false)
	_, key := createKey(t, owner, integration, nil)
	bearer := header("Authorization", "Bearer "+key)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, deliveriesPath(integration, e.ID)},
		{http.MethodGet, deliveriesPath(integration, e.ID) + "/" + mine.String()},
		{http.MethodPost, resendPath(integration, e.ID, mine)},
	} {
		if r := person.do(c.method, c.path, nil); r.status != http.StatusForbidden {
			t.Errorf("a person's %s %s: %d", c.method, c.path, r.status)
		}
		if r := h.client(t).do(c.method, c.path, nil, bearer); r.status != http.StatusForbidden || r.code() != "api_not_available" {
			t.Errorf("a key's %s %s: %d %s", c.method, c.path, r.status, r.body)
		}
	}
	if d := h.delivery(t, mine); d.State != "dead" {
		t.Errorf("a refused resend changed the delivery: %+v", d)
	}
}

func TestWebhookLog_ResendSendsTheSameMessageSignedAfresh(t *testing.T) {
	t.Parallel()
	h, owner, ownerID, integration, rc := senderHarness(t)
	e, secret := createWebhook(t, owner, integration, map[string]any{})
	h.dropPings(t)
	s := h.webhookSender(t)

	// An attempt the receiver refuses, its answer attacker-written text.
	rc.answer("hooks.example.com", func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`<script>alert(1)</script><img src=x onerror=alert(2)>`))
	})
	h.publish(t, contracts.SalesOrderConfirmed{OrderID: 42})
	h.fanOut(t)
	pass(t, s)
	event := h.deliveries(t, "kind = 'event'")
	if len(event) != 1 || event[0].State != "pending" || event[0].RunAttempts != 1 {
		t.Fatalf("after a 500: %+v", event)
	}
	id := event[0].ID
	path := deliveriesPath(integration, e.ID) + "/" + id.String()

	// Pending is not resendable; dead, at the end of its run, is.
	if r := owner.do(http.MethodPost, path+"/resend", nil); r.status != http.StatusConflict || r.code() != "webhook_delivery_not_resendable" {
		t.Errorf("a pending delivery's resend: %d %s", r.status, r.body)
	}
	h.exec(t, `UPDATE identity.webhook_deliveries SET state = 'dead', state_reason = 'attempts_exhausted', next_attempt_at = NULL, completed_at = $2 WHERE id = $1`, id, h.now())

	var detail logDetail
	owner.do(http.MethodGet, path, nil).json(&detail)
	if detail.Body != event[0].Body || len(detail.Attempts) != 1 || detail.Attempts[0].StatusCode == nil || *detail.Attempts[0].StatusCode != 500 ||
		detail.Attempts[0].Error == nil || *detail.Attempts[0].Error != "http_status" || detail.Attempts[0].SecretUsed != "current" ||
		detail.Attempts[0].Excerpt == nil || !strings.Contains(*detail.Attempts[0].Excerpt, "<script>alert(1)</script>") || !detail.Delivery.Resendable {
		t.Fatalf("detail %+v", detail)
	}
	first := rc.requests("hooks.example.com")[0]

	h.advance(10 * time.Minute)
	rc.status("hooks.example.com", http.StatusNoContent)
	r := owner.do(http.MethodPost, path+"/resend", nil)
	if r.status != http.StatusAccepted {
		t.Fatalf("resend: %d %s", r.status, r.body)
	}
	var resent logDelivery
	r.json(&resent)
	if resent.ID != id || resent.State != "pending" || resent.StateReason != nil || resent.RunAttempts != 0 || resent.MaxRunAttempts != 8 ||
		resent.TotalAttempts != 1 || resent.CompletedAt != nil || resent.Resendable {
		t.Errorf("resent %+v", resent)
	}
	audit := h.auditEvents(t, "webhook.resent")
	if len(audit) != 1 || audit[0].Actor == nil || *audit[0].Actor != ownerID || audit[0].TargetUser == nil || *audit[0].TargetUser != integration ||
		!strings.Contains(audit[0].After, id.String()) {
		t.Errorf("audit %+v", audit)
	}
	assertNoSecretIn(t, "the audit", audit[0].Before+audit[0].After, secret)

	if n := pass(t, s); n != 1 {
		t.Fatalf("the resend sent %d", n)
	}
	got := rc.requests("hooks.example.com")
	again := got[len(got)-1]
	if again.header.Get("webhook-id") != msgID(id) || again.body != first.body || !verifies(t, secret, again) ||
		again.header.Get("webhook-timestamp") == first.header.Get("webhook-timestamp") ||
		again.header.Get("webhook-signature") == first.header.Get("webhook-signature") {
		t.Errorf("resent request %v %s, first %v", again.header, again.body, first.header)
	}
	owner.do(http.MethodGet, path, nil).json(&detail)
	if detail.Delivery.State != "succeeded" || detail.Delivery.RunAttempts != 1 || detail.Delivery.TotalAttempts != 2 || len(detail.Attempts) != 2 ||
		detail.Attempts[0].StatusCode == nil || *detail.Attempts[0].StatusCode != 204 || detail.Attempts[0].Excerpt != nil ||
		!detail.Attempts[0].At.After(detail.Attempts[1].At) {
		t.Errorf("after the resend %+v", detail)
	}
}

func TestWebhookLog_ResendIsLimitedToThreeAMinutePerDelivery(t *testing.T) {
	t.Parallel()
	h, owner, _, integration, rc := senderHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{})
	h.dropPings(t)
	s := h.webhookSender(t)
	id := h.insertDelivery(t, e.ID, 1)
	pass(t, s)
	sibling := h.insertDelivery(t, e.ID, 2)
	pass(t, s)
	path := resendPath(integration, e.ID, id)
	for i := range 3 {
		h.advance(10 * time.Second)
		if r := owner.do(http.MethodPost, path, nil); r.status != http.StatusAccepted {
			t.Fatalf("resend %d: %d %s", i+1, r.status, r.body)
		}
		pass(t, s)
	}
	h.advance(10 * time.Second)
	if r := owner.do(http.MethodPost, path, nil); r.status != http.StatusTooManyRequests || r.code() != "webhook_resend_limited" ||
		r.header("Retry-After") != "30" {
		t.Errorf("a fourth resend within the window: %d %s, Retry-After %q", r.status, r.body, r.header("Retry-After"))
	}
	// The limit is the delivery's: another one is resent.
	if r := owner.do(http.MethodPost, resendPath(integration, e.ID, sibling), nil); r.status != http.StatusAccepted {
		t.Errorf("another delivery's resend: %d %s", r.status, r.body)
	}
	// A minute after the window opened, it is resent again.
	h.advance(30 * time.Second)
	if r := owner.do(http.MethodPost, path, nil); r.status != http.StatusAccepted {
		t.Errorf("a resend in a new window: %d %s", r.status, r.body)
	}
	if n := len(rc.requests("hooks.example.com")); n != 5 {
		t.Errorf("%d requests, want 2 and 3 resends", n)
	}
}

func TestWebhookLog_ResendIsRefusedWhenItCannotBeSent(t *testing.T) {
	t.Parallel()
	h, owner, _, integration := webhookHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{})
	var ping uuid.UUID
	if err := h.pool.QueryRow(context.Background(), `SELECT id FROM identity.webhook_deliveries WHERE kind = 'ping'`).Scan(&ping); err != nil {
		t.Fatal(err)
	}
	h.settle(t, ping, "succeeded", "")
	skipped := h.insertDelivery(t, e.ID, 1)
	h.settle(t, skipped, "skipped", "permission_not_held")
	old := h.insertDelivery(t, e.ID, 2)
	h.settle(t, old, "succeeded", "")
	h.exec(t, `UPDATE identity.webhook_deliveries SET created_at = $2 WHERE id = $1`, old, h.now().Add(-27*24*time.Hour))
	worn := h.insertDelivery(t, e.ID, 4)
	h.settle(t, worn, "dead", "attempts_exhausted")
	h.exec(t, `UPDATE identity.webhook_deliveries SET total_attempts = 80 WHERE id = $1`, worn)
	unlisted := h.insertDelivery(t, e.ID, 5)
	h.settle(t, unlisted, "dead", "attempts_exhausted")
	h.exec(t, `UPDATE identity.webhook_deliveries SET event_name = $2 WHERE id = $1`, unlisted, cancelled)
	dead := h.insertDelivery(t, e.ID, 3)
	h.settle(t, dead, "dead", "attempts_exhausted")

	for name, c := range map[string]struct {
		id   uuid.UUID
		code string
	}{
		"a ping":                    {ping, "webhook_delivery_not_resendable"},
		"a skipped one":             {skipped, "webhook_delivery_not_resendable"},
		"one 27 days old":           {old, "webhook_delivery_not_resendable"},
		"one tried 80 times":        {worn, "webhook_delivery_not_resendable"},
		"an event no longer listed": {unlisted, "webhook_event_not_subscribed"},
		"no such delivery":          {uuid.New(), "not_found"},
	} {
		r := owner.do(http.MethodPost, resendPath(integration, e.ID, c.id), nil)
		if r.code() != c.code {
			t.Errorf("%s: %d %s, want %s", name, r.status, r.body, c.code)
		}
	}

	// The integration's permission is checked again at the resend.
	h.exec(t, `DELETE FROM identity.role_permissions WHERE permission_key = 'orders:access'`)
	if r := owner.do(http.MethodPost, resendPath(integration, e.ID, dead), nil); r.status != http.StatusConflict || r.code() != "webhook_event_not_permitted" {
		t.Errorf("a permission lost: %d %s", r.status, r.body)
	}
	h.exec(t, `INSERT INTO identity.role_permissions (role_id, permission_key)
	    SELECT r.id, 'orders:access' FROM identity.roles r WHERE r.name = 'Webshop'`)

	// A disabled endpoint, then an archived integration.
	if r := owner.do(http.MethodPost, webhooksPath(integration)+"/"+e.ID.String()+"/disable", nil); r.status != http.StatusOK {
		t.Fatalf("disable: %d", r.status)
	}
	if r := owner.do(http.MethodPost, resendPath(integration, e.ID, dead), nil); r.status != http.StatusConflict || r.code() != "webhook_endpoint_disabled" {
		t.Errorf("a disabled endpoint: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPost, integrationsPath+"/"+integration.String()+"/archive", nil); r.status != http.StatusOK {
		t.Fatalf("archive: %d", r.status)
	}
	if r := owner.do(http.MethodPost, resendPath(integration, e.ID, dead), nil); r.status != http.StatusConflict || r.code() != "integration_archived" {
		t.Errorf("an archived integration: %d %s", r.status, r.body)
	}
	// An archived integration's log is still read.
	if r := owner.do(http.MethodGet, deliveriesPath(integration, e.ID), nil); r.status != http.StatusOK {
		t.Errorf("an archived integration's log: %d", r.status)
	}
	if d := h.delivery(t, dead); d.State != "dead" {
		t.Errorf("a refused resend changed the delivery: %+v", d)
	}
	if n := len(h.auditEvents(t, "webhook.resent")); n != 0 {
		t.Errorf("%d resends audited", n)
	}
}

func TestWebhookLog_KeepsADeliveryThirtyDaysAndAResendRunsItsOwnThreeDays(t *testing.T) {
	t.Parallel()
	h, owner, _, integration, _ := senderHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{})
	h.dropPings(t)
	s := h.webhookSender(t)
	kept := h.insertDelivery(t, e.ID, 1)
	h.settle(t, kept, "dead", "attempts_exhausted")
	h.exec(t, `UPDATE identity.webhook_deliveries SET created_at = $2 WHERE id = $1`, kept, h.now().Add(-29*24*time.Hour))
	gone := h.insertDelivery(t, e.ID, 2)
	h.settle(t, gone, "succeeded", "")
	h.exec(t, `UPDATE identity.webhook_deliveries SET created_at = $2 WHERE id = $1`, gone, h.now().Add(-30*24*time.Hour-time.Minute))
	if err := s.Housekeep(context.Background()); err != nil {
		t.Fatal(err)
	}
	var page logPage
	owner.do(http.MethodGet, deliveriesPath(integration, e.ID), nil).json(&page)
	if len(page.Deliveries) != 1 || page.Deliveries[0].ID != kept || page.Deliveries[0].Resendable { // 29 days old: kept, no longer resendable
		t.Fatalf("after the prune: %+v", page.Deliveries)
	}

	// A delivery is resent only while its run can end before the prune: 29
	// days old is refused, 26 days old is resent, and its run is neither
	// pruned nor expired for its 3 days.
	h.exec(t, `UPDATE identity.webhook_deliveries SET created_at = $2 WHERE id = $1`, kept, h.now().Add(-29*24*time.Hour))
	if r := owner.do(http.MethodPost, resendPath(integration, e.ID, kept), nil); r.status != http.StatusConflict || r.code() != "webhook_delivery_not_resendable" {
		t.Errorf("29 days old: %d %s", r.status, r.body)
	}
	h.exec(t, `UPDATE identity.webhook_deliveries SET created_at = $2 WHERE id = $1`, kept, h.now().Add(-26*24*time.Hour))
	if r := owner.do(http.MethodPost, resendPath(integration, e.ID, kept), nil); r.status != http.StatusAccepted {
		t.Fatalf("resend: %d %s", r.status, r.body)
	}
	h.exec(t, `UPDATE identity.webhook_deliveries SET next_attempt_at = $2 WHERE id = $1`, kept, h.now().Add(time.Hour))
	if err := s.Housekeep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := h.delivery(t, kept); d.State != "pending" {
		t.Errorf("a resend expired at once: %+v", d)
	}
	h.advance(3*24*time.Hour - time.Minute)
	if err := s.Housekeep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := h.delivery(t, kept); d.State != "pending" {
		t.Errorf("a resend's run cut short within its 3 days: %+v", d)
	}
	h.advance(2 * time.Minute)
	if err := s.Housekeep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := h.delivery(t, kept); d.State != "dead" || d.Reason != "expired" {
		t.Errorf("a resend pending 3 days: %+v", d)
	}

	// The prune leaves a pending delivery resent since its cutoff to finish.
	running := h.insertDelivery(t, e.ID, 9)
	h.exec(t, `UPDATE identity.webhook_deliveries SET created_at = $2, resend_window_start = $3, next_attempt_at = $4 WHERE id = $1`,
		running, h.now().Add(-30*24*time.Hour-time.Hour), h.now().Add(-time.Hour), h.now().Add(time.Hour))
	if err := s.Housekeep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := h.delivery(t, running); d.State != "pending" {
		t.Errorf("a resent run was pruned: %+v", d)
	}
}

func TestWebhookLog_PagesThroughDeliveriesQueuedAtTheSameInstant(t *testing.T) {
	t.Parallel()
	h, owner, _, integration := webhookHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{})
	h.dropPings(t)
	want := map[uuid.UUID]bool{}
	for i := range 5 {
		want[h.insertDelivery(t, e.ID, int64(i+1))] = true
	}
	var seen []uuid.UUID
	cursor := ""
	for range 10 {
		q := deliveriesPath(integration, e.ID) + "?limit=2"
		if cursor != "" {
			q += "&cursor=" + url.QueryEscape(cursor)
		}
		var page logPage
		owner.do(http.MethodGet, q, nil).json(&page)
		for _, d := range page.Deliveries {
			seen = append(seen, d.ID)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 5 {
		t.Fatalf("%d deliveries over the pages, want 5: %v", len(seen), seen)
	}
	for i, id := range seen {
		if !want[id] {
			t.Errorf("%s twice or unknown", id)
		}
		delete(want, id)
		if i > 0 && strings.Compare(seen[i-1].String(), id.String()) >= 0 {
			t.Errorf("ties are not by id: %v", seen)
		}
	}
}

func TestWebhookSender_AnEventTheEndpointNoLongerListsIsSkipped(t *testing.T) {
	t.Parallel()
	h, owner, _, integration, rc := senderHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{"events": []string{confirmed, cancelled}})
	h.dropPings(t)
	id := h.insertDelivery(t, e.ID, 1)
	h.exec(t, `UPDATE identity.webhook_deliveries SET event_name = $2 WHERE id = $1`, id, cancelled)
	r := owner.do(http.MethodPatch, webhooksPath(integration)+"/"+e.ID.String(),
		map[string]any{"url": "https://hooks.example.com/in", "events": []string{confirmed}}, header("If-Match", e.Version))
	if r.status != http.StatusOK {
		t.Fatalf("edit: %d %s", r.status, r.body)
	}
	pass(t, h.webhookSender(t))
	if d := h.delivery(t, id); d.State != "skipped" || d.Reason != "permission_not_held" || d.RunAttempts != 0 {
		t.Errorf("an event taken off the endpoint: %+v", d)
	}
	if n := len(rc.requests("")); n != 0 {
		t.Errorf("%d requests sent", n)
	}
}

func TestWebhookLog_EnablingAnIntegrationKeepsARunResentMeanwhile(t *testing.T) {
	t.Parallel()
	h, owner, ownerID, integration := webhookHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{})
	h.dropPings(t)
	if r := owner.do(http.MethodPost, integrationsPath+"/"+integration.String()+"/disable", nil); r.status != http.StatusOK {
		t.Fatalf("disable: %d", r.status)
	}
	stale := h.insertDelivery(t, e.ID, 1)
	resent := h.insertDelivery(t, e.ID, 2)
	h.advance(25 * time.Hour)
	h.exec(t, `UPDATE identity.webhook_deliveries SET resend_window_start = $2, resend_count = 1 WHERE id = $1`, resent, h.now().Add(-time.Hour))
	if r := h.signIn(t, ownerID, false).do(http.MethodPost, integrationsPath+"/"+integration.String()+"/enable", nil); r.status != http.StatusOK {
		t.Fatalf("enable: %d", r.status)
	}
	if d := h.delivery(t, stale); d.State != "dead" || d.Reason != "expired" {
		t.Errorf("due 25 hours ago: %+v", d)
	}
	if d := h.delivery(t, resent); d.State != "pending" {
		t.Errorf("resent an hour ago: %+v", d)
	}
}
