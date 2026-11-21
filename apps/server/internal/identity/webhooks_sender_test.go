package identity_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The webhooks' fan-out and sender (outgoing webhooks design W7, W12-W19,
// W29-W33, W37, W38), against a real TLS receiver reached through the
// guarded transport.

// received is one request the receiver took.
type received struct {
	host   string
	header http.Header
	body   string
}

// receiver is an httptest TLS server every webhook reaches (SetWebhookServer).
// It answers each host by its rule, 204 when it has none; a host it is told
// to hold keeps each request until release, and counts how many it held at
// once.
type receiver struct {
	srv *httptest.Server

	mu       sync.Mutex
	got      []received
	rules    map[string]func(w http.ResponseWriter)
	holds    map[string]bool
	held     map[string]int
	maxHeld  map[string]int
	released chan struct{}
	once     sync.Once
}

func newReceiver(t *testing.T) *receiver {
	t.Helper()
	rc := &receiver{
		rules: map[string]func(http.ResponseWriter){}, holds: map[string]bool{},
		held: map[string]int{}, maxHeld: map[string]int{}, released: make(chan struct{}),
	}
	rc.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		host := r.Host
		rc.mu.Lock()
		rc.got = append(rc.got, received{host: host, header: r.Header.Clone(), body: string(body)})
		rule, hold := rc.rules[host], rc.holds[host]
		if hold {
			rc.held[host]++
			rc.maxHeld[host] = max(rc.maxHeld[host], rc.held[host])
		}
		rc.mu.Unlock()
		if hold {
			var abandoned bool
			select {
			case <-rc.released:
			case <-r.Context().Done():
				abandoned = true
			}
			rc.mu.Lock()
			rc.held[host]--
			rc.mu.Unlock()
			if abandoned {
				// The sender gave up on this request: answer nothing, so no
				// status can reach a client that has not yet noticed.
				panic(http.ErrAbortHandler)
			}
		}
		if rule != nil {
			rule(w)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(func() {
		rc.release()
		rc.srv.Close()
	})
	return rc
}

func (rc *receiver) answer(host string, rule func(w http.ResponseWriter)) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.rules[host] = rule
}

func (rc *receiver) status(host string, code int) {
	rc.answer(host, func(w http.ResponseWriter) { w.WriteHeader(code) })
}

func (rc *receiver) hold(host string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.holds[host] = true
}

func (rc *receiver) release() { rc.once.Do(func() { close(rc.released) }) }

func (rc *receiver) requests(host string) []received {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	var out []received
	for _, r := range rc.got {
		if host == "" || r.host == host {
			out = append(out, r)
		}
	}
	return out
}

func (rc *receiver) heldNow(host string) int {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.held[host]
}

func (rc *receiver) mostHeld(host string) int {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.maxHeld[host]
}

// senderHarness is webhookHarness with a receiver every webhook reaches.
func senderHarness(t *testing.T) (*harness, *client, uuid.UUID, uuid.UUID, *receiver) {
	t.Helper()
	h, owner, ownerID, integration := webhookHarness(t)
	rc := newReceiver(t)
	identity.SetWebhookServer(h.access, rc.srv)
	return h, owner, ownerID, integration, rc
}

// webhookSender is identity.webhook-sender as module.Workers builds it,
// its jitter fixed at none.
func (h *harness) webhookSender(t testing.TB) identity.WebhookSender {
	t.Helper()
	for _, w := range module.Workers(h.deps, h.mods...) {
		if s, ok := w.(identity.WebhookSender); ok && s.Name() == "identity.webhook-sender" {
			identity.SetWebhookJitter(s, func() float64 { return 0.5 })
			return s
		}
	}
	t.Fatal("no identity.webhook-sender")
	return nil
}

// pass runs one pass of s and waits for its sends.
func pass(t testing.TB, s identity.WebhookSender) int {
	t.Helper()
	n, err := s.RunPass(context.Background())
	if err != nil {
		t.Fatalf("RunPass: %v", err)
	}
	s.Wait()
	return n
}

// publish publishes ev as orders would, at the harness clock.
func (h *harness) publish(t testing.TB, ev contracts.Event) {
	t.Helper()
	ctx := context.Background()
	if err := pgx.BeginFunc(ctx, h.pool, func(tx pgx.Tx) error {
		return events.Publish(ctx, tx, "orders", ev, h.now())
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

// webhookHandler is identity.webhooks as module.EventHandlers builds it.
func (h *harness) webhookHandler(t testing.TB) events.Handler {
	t.Helper()
	handlers, err := module.EventHandlers(h.deps, h.mods...)
	if err != nil {
		t.Fatal(err)
	}
	for _, handler := range handlers {
		if handler.Name == "identity.webhooks" {
			return handler
		}
	}
	t.Fatal("no identity.webhooks handler")
	return events.Handler{}
}

// fanOut runs the outbox's dispatcher once over identity's handler.
func (h *harness) fanOut(t testing.TB) {
	t.Helper()
	if err := events.NewDispatcher(h.pool, []events.Handler{h.webhookHandler(t)}, nil, h.now).RunOnce(context.Background()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
}

type deliveryRow struct {
	ID          uuid.UUID
	Endpoint    uuid.UUID
	Kind        string
	Body        string
	State       string
	Reason      string
	RunAttempts int
	Total       int
	NextAt      *time.Time
	CompletedAt *time.Time
	LastStatus  *int
	LastError   string
}

func (h *harness) deliveries(t testing.TB, where string, args ...any) []deliveryRow {
	t.Helper()
	rows, err := h.pool.Query(context.Background(), `SELECT id, endpoint_id, kind, body, state, coalesce(state_reason, ''), run_attempts,
	    total_attempts, next_attempt_at, completed_at, last_status, coalesce(last_error, '')
	    FROM identity.webhook_deliveries WHERE `+where+` ORDER BY created_at, id`, args...)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (deliveryRow, error) {
		var d deliveryRow
		var status *int16
		err := r.Scan(&d.ID, &d.Endpoint, &d.Kind, &d.Body, &d.State, &d.Reason, &d.RunAttempts, &d.Total, &d.NextAt, &d.CompletedAt, &status, &d.LastError)
		if status != nil {
			s := int(*status)
			d.LastStatus = &s
		}
		return d, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (h *harness) delivery(t testing.TB, id uuid.UUID) deliveryRow {
	t.Helper()
	got := h.deliveries(t, "id = $1", id)
	if len(got) != 1 {
		t.Fatalf("delivery %s: %d rows", id, len(got))
	}
	return got[0]
}

// dropPings deletes the pings creating an endpoint queued, for a test about
// other messages.
func (h *harness) dropPings(t testing.TB) {
	t.Helper()
	h.exec(t, `DELETE FROM identity.webhook_deliveries WHERE kind = 'ping'`)
}

// verifies reports whether one of header's signatures is body's under
// secret, as a receiver checks it.
func verifies(t testing.TB, secret string, r received) bool {
	t.Helper()
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(r.header.Get("webhook-id") + "." + r.header.Get("webhook-timestamp") + "." + r.body))
	want := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	for _, sig := range strings.Fields(r.header.Get("webhook-signature")) {
		if hmac.Equal([]byte(sig), []byte(want)) {
			return true
		}
	}
	return false
}

func msgID(id uuid.UUID) string { return "msg_" + hex.EncodeToString(id[:]) }

func TestWebhookFanOut_QueuesOneThinMessagePerEndpointOnAPoolOfOne(t *testing.T) {
	t.Parallel()
	h, owner, _, integration := webhookHarness(t)
	first, _ := createWebhook(t, owner, integration, map[string]any{"url": "https://a.example.com/in"})
	second, _ := createWebhook(t, owner, integration, map[string]any{"url": "https://b.example.com/in", "events": []string{confirmed, cancelled}})
	createWebhook(t, owner, integration, map[string]any{"url": "https://c.example.com/in", "events": []string{cancelled}})
	off, _ := createWebhook(t, owner, integration, map[string]any{"url": "https://d.example.com/in"})
	if r := owner.do(http.MethodPost, webhooksPath(integration)+"/"+off.ID.String()+"/disable", nil); r.status != http.StatusOK {
		t.Fatalf("disable: %d", r.status)
	}
	// An integration that lost orders:access after subscribing, and one that
	// is disabled.
	lostRole := h.insertRole(t, "Lost", "orders:access")
	lost := createIntegration(t, owner, "Lost access", lostRole).Integration.ID
	lostEndpoint, _ := createWebhook(t, owner, lost, map[string]any{"url": "https://lost.example.com/in"})
	h.exec(t, `DELETE FROM identity.role_permissions WHERE role_id = $1`, lostRole)
	asleep := createIntegration(t, owner, "Asleep", h.insertRole(t, "Asleep", "orders:access")).Integration.ID
	createWebhook(t, owner, asleep, map[string]any{"url": "https://asleep.example.com/in"})
	if r := owner.do(http.MethodPost, integrationsPath+"/"+asleep.String()+"/disable", nil); r.status != http.StatusOK {
		t.Fatalf("disable the integration: %d", r.status)
	}
	h.dropPings(t)

	h.publish(t, contracts.SalesOrderConfirmed{OrderID: 42, Number: 1001, CustomerID: 7, Currency: "NOK", NetTotal: "1234.50"})
	var eventID int64
	if err := h.pool.QueryRow(context.Background(), `SELECT id FROM platform.events ORDER BY id DESC LIMIT 1`).Scan(&eventID); err != nil {
		t.Fatal(err)
	}

	// The dispatcher and the handler on one connection: nothing reaches
	// for a second one while it holds the first.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	one, err := db.Open(ctx, h.pool.Config().ConnString(), db.WithMaxConns(1))
	if err != nil {
		t.Fatalf("open a pool of one: %v", err)
	}
	defer one.Close()
	if err := events.NewDispatcher(one, []events.Handler{h.webhookHandler(t)}, nil, h.now).RunOnce(ctx); err != nil {
		t.Fatalf("dispatch on a pool of one: %v", err)
	}

	got := h.deliveries(t, "true")
	if len(got) != 3 {
		t.Fatalf("deliveries %+v, want one each to a, b and the integration that lost access", got)
	}
	want := `{"id":"evt_` + strconv.FormatInt(eventID, 10) + `","type":"orders.sales_order.confirmed","version":1,"timestamp":"2026-09-11T12:00:00Z","sequence":` +
		strconv.FormatInt(eventID, 10) + `,"data":{"resource":{"type":"sales_order","id":"42","href":"/api/v1/orders/orders/42"}}}`
	states := map[uuid.UUID]deliveryRow{}
	for _, d := range got {
		states[d.Endpoint] = d
		if d.Body != want {
			t.Errorf("body\n got %s\nwant %s", d.Body, want)
		}
	}
	for _, e := range []uuid.UUID{first.ID, second.ID} {
		if d := states[e]; d.State != "pending" || d.NextAt == nil || d.CompletedAt != nil || d.Kind != "event" {
			t.Errorf("endpoint %s: %+v, want pending", e, d)
		}
	}
	if d := states[lostEndpoint.ID]; d.State != "skipped" || d.Reason != "permission_not_held" || d.CompletedAt == nil || d.NextAt != nil {
		t.Errorf("the integration that lost access: %+v, want skipped with completed_at", d)
	}

	// The outbox is at-least-once: the same event again queues nothing.
	if err := pgx.BeginFunc(ctx, h.pool, func(tx pgx.Tx) error {
		return h.webhookHandler(t).Handle(ctx, tx, events.Delivered{
			ID: eventID, Name: confirmed, Version: 1, Module: "orders", AggregateID: "42",
			Payload: []byte(`{"orderId":42}`), OccurredAt: h.now(),
		})
	}); err != nil {
		t.Fatal(err)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.webhook_deliveries`); n != 3 {
		t.Errorf("after the event again: %d deliveries, want 3", n)
	}
}

func TestWebhookFanOut_AnEventWithoutItsResourceIDIsAnError(t *testing.T) {
	t.Parallel()
	h, owner, _, integration := webhookHarness(t)
	createWebhook(t, owner, integration, map[string]any{})
	handler := h.webhookHandler(t)
	ctx := context.Background()
	for _, payload := range []string{`{"number":1}`, `{"orderId":null}`, `{"orderId":""}`, `{"orderId":{"id":1}}`, `not json`} {
		err := pgx.BeginFunc(ctx, h.pool, func(tx pgx.Tx) error {
			return handler.Handle(ctx, tx, events.Delivered{ID: 9, Name: confirmed, Version: 1, Module: "orders", AggregateID: "1", Payload: []byte(payload), OccurredAt: h.now()})
		})
		if err == nil {
			t.Errorf("payload %s: no error", payload)
		}
	}
	// A string id is the resource's id as it is.
	if err := pgx.BeginFunc(ctx, h.pool, func(tx pgx.Tx) error {
		return handler.Handle(ctx, tx, events.Delivered{ID: 10, Name: confirmed, Version: 1, Module: "orders", AggregateID: "x", Payload: []byte(`{"orderId":"A/1"}`), OccurredAt: h.now()})
	}); err != nil {
		t.Fatal(err)
	}
	if got := h.deliveries(t, "event_id = 10"); len(got) != 1 || !strings.Contains(got[0].Body, `"id":"A/1","href":"/api/v1/orders/orders/A%2F1"`) {
		t.Errorf("a string id: %+v", got)
	}
}

// TestWebhookFanOut_NeverQueuesAfterADisableSettledTheEndpoint is W31: a
// disable holds the endpoint FOR UPDATE while it settles the pending rows; a
// fan-out waiting on its FOR SHARE then sees the endpoint disabled and
// queues nothing.
func TestWebhookFanOut_NeverQueuesAfterADisableSettledTheEndpoint(t *testing.T) {
	t.Parallel()
	h, owner, _, integration := webhookHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{})
	h.dropPings(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	disable, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = disable.Rollback(ctx) }()
	if _, err := disable.Exec(ctx, `SELECT 1 FROM identity.webhook_endpoints WHERE id = $1 FOR UPDATE`, e.ID); err != nil {
		t.Fatal(err)
	}

	handler := h.webhookHandler(t)
	done := make(chan error, 1)
	go func() {
		done <- pgx.BeginFunc(ctx, h.pool, func(tx pgx.Tx) error {
			return handler.Handle(ctx, tx, events.Delivered{ID: 77, Name: confirmed, Version: 1, Module: "orders", AggregateID: "1", Payload: []byte(`{"orderId":1}`), OccurredAt: h.now()})
		})
	}()
	// Wait until the fan-out waits on the endpoint's lock.
	for {
		var waiting int
		if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND datname = current_database()`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("the fan-out did not wait: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, err := disable.Exec(ctx, `UPDATE identity.webhook_endpoints SET status = 'disabled', disabled_reason = 'manual', disabled_at = $2 WHERE id = $1`, e.ID, h.now()); err != nil {
		t.Fatal(err)
	}
	if err := disable.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("fan-out: %v", err)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.webhook_deliveries`); n != 0 {
		t.Errorf("%d deliveries queued for an endpoint disabled under the fan-out", n)
	}
}

func TestWebhookSender_SendsSignedMessagesAndAPingFirst(t *testing.T) {
	t.Parallel()
	h, owner, _, integration, rc := senderHarness(t)
	e, secret := createWebhook(t, owner, integration, map[string]any{"url": "https://hooks.example.com/in?token=Q-T0K3N"})
	s := h.webhookSender(t)

	// The ping creating the endpoint queued.
	if n := pass(t, s); n != 1 {
		t.Fatalf("first pass sent %d, want the ping", n)
	}
	pings := h.deliveries(t, "kind = 'ping'")
	got := rc.requests("hooks.example.com")
	if len(pings) != 1 || len(got) != 1 || pings[0].State != "succeeded" || pings[0].RunAttempts != 1 {
		t.Fatalf("ping %+v, requests %d", pings, len(got))
	}
	ping := got[0]
	var body map[string]any
	if err := json.Unmarshal([]byte(ping.body), &body); err != nil {
		t.Fatal(err)
	}
	if body["id"] != "evt_ping_"+pings[0].ID.String() || body["type"] != "webhook.ping" || body["version"] != float64(1) ||
		body["timestamp"] != "2026-09-11T12:00:00Z" || body["sequence"] != nil || len(body["data"].(map[string]any)) != 0 {
		t.Errorf("ping body %s", ping.body)
	}
	if ping.header.Get("webhook-id") != msgID(pings[0].ID) || ping.header.Get("webhook-timestamp") != strconv.FormatInt(h.now().Unix(), 10) ||
		ping.header.Get("Content-Type") != "application/json" || ping.header.Get("User-Agent") != "Vantigo-Webhooks/1" || !verifies(t, secret, ping) {
		t.Errorf("ping headers %v", ping.header)
	}
	if n := len(strings.Fields(ping.header.Get("webhook-signature"))); n != 1 {
		t.Errorf("%d signatures, want one", n)
	}
	// A ping counts for nothing toward the endpoint's health (W19).
	var lastSuccess *time.Time
	if err := h.pool.QueryRow(context.Background(), `SELECT last_success_at FROM identity.webhook_endpoints WHERE id = $1`, e.ID).Scan(&lastSuccess); err != nil || lastSuccess != nil {
		t.Errorf("last_success_at after a ping = %v, %v", lastSuccess, err)
	}

	h.advance(time.Minute)
	h.publish(t, contracts.SalesOrderConfirmed{OrderID: 42})
	h.fanOut(t)
	if n := pass(t, s); n != 1 {
		t.Fatalf("sent %d, want the event", n)
	}
	event := h.deliveries(t, "kind = 'event'")
	got = rc.requests("hooks.example.com")
	if len(event) != 1 || event[0].State != "succeeded" || event[0].CompletedAt == nil || event[0].LastStatus == nil || *event[0].LastStatus != 204 || len(got) != 2 {
		t.Fatalf("event %+v, %d requests", event, len(got))
	}
	if got[1].body != event[0].Body || !verifies(t, secret, got[1]) || got[1].header.Get("webhook-id") != msgID(event[0].ID) {
		t.Errorf("event request %v %s", got[1].header, got[1].body)
	}
	var status, duration int
	var used string
	var excerpt *string
	if err := h.pool.QueryRow(context.Background(), `SELECT status_code, duration_ms, secret_used, excerpt FROM identity.webhook_attempts WHERE delivery_id = $1`, event[0].ID).
		Scan(&status, &duration, &used, &excerpt); err != nil || status != 204 || used != "current" || excerpt != nil || duration < 0 {
		t.Errorf("attempt %d %d %s %v %v", status, duration, used, excerpt, err)
	}
	if err := h.pool.QueryRow(context.Background(), `SELECT last_success_at FROM identity.webhook_endpoints WHERE id = $1`, e.ID).Scan(&lastSuccess); err != nil || lastSuccess == nil || !lastSuccess.Equal(h.now()) {
		t.Errorf("last_success_at %v %v", lastSuccess, err)
	}
	// Nothing is due now.
	if n := pass(t, s); n != 0 {
		t.Errorf("an idle pass sent %d", n)
	}
	assertNoSecretIn(t, "the log", string(h.log.Bytes()), secret)
}

func TestWebhookSender_RetriesOnTheScheduleAndDiesOnTheEighth(t *testing.T) {
	t.Parallel()
	h, owner, _, integration, rc := senderHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{})
	h.dropPings(t)
	rc.answer("hooks.example.com", func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("down\x1b[31m for maintenance"))
	})
	s := h.webhookSender(t)
	id := h.insertDelivery(t, e.ID, 1)

	for attempt := 1; attempt <= 8; attempt++ {
		before := h.now()
		if n := pass(t, s); n != 1 {
			t.Fatalf("attempt %d: sent %d", attempt, n)
		}
		d := h.delivery(t, id)
		if d.RunAttempts != attempt || d.Total != attempt || d.LastStatus == nil || *d.LastStatus != 500 || d.LastError != "http_status" {
			t.Fatalf("after attempt %d: %+v", attempt, d)
		}
		if attempt == 8 {
			if d.State != "dead" || d.Reason != "attempts_exhausted" || d.NextAt != nil || d.CompletedAt == nil {
				t.Fatalf("after the eighth: %+v", d)
			}
			break
		}
		step := identity.WebhookRetrySchedule[attempt-1]
		if d.State != "pending" || d.NextAt == nil || !d.NextAt.Equal(before.Add(step)) {
			t.Fatalf("after attempt %d: next %v, want %v", attempt, d.NextAt, before.Add(step))
		}
		// Not before it is due.
		h.advance(step - time.Second)
		if n := pass(t, s); n != 0 {
			t.Fatalf("attempt %d retried early", attempt)
		}
		h.advance(time.Second)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.webhook_attempts WHERE delivery_id = $1 AND excerpt = 'down [31m for maintenance'`, id); n != 8 {
		t.Errorf("%d attempts kept the excerpt with its control character blanked", n)
	}
	var failingSince *time.Time
	if err := h.pool.QueryRow(context.Background(), `SELECT failing_since FROM identity.webhook_endpoints WHERE id = $1`, e.ID).Scan(&failingSince); err != nil || failingSince == nil || !failingSince.Equal(start) {
		t.Errorf("failing_since %v, want the first failure %v", failingSince, start)
	}

	// The schedule is jittered by up to 10% either way.
	for _, c := range []struct {
		draw float64
		want time.Duration
	}{{0, 27 * time.Second}, {1, 33 * time.Second}} {
		identity.SetWebhookJitter(s, func() float64 { return c.draw })
		jittered := h.insertDelivery(t, e.ID, int64(100+c.draw))
		at := h.now()
		pass(t, s)
		if d := h.delivery(t, jittered); d.NextAt == nil || !d.NextAt.Equal(at.Add(c.want)) {
			t.Errorf("draw %v: next %v, want %v", c.draw, d.NextAt, at.Add(c.want))
		}
	}
}

func TestWebhookSender_ARedirectIsAFailureAndNeverFollowed(t *testing.T) {
	t.Parallel()
	h, owner, _, integration, rc := senderHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{})
	h.dropPings(t)
	rc.answer("hooks.example.com", func(w http.ResponseWriter) {
		w.Header().Set("Location", "https://elsewhere.example.com/in")
		w.WriteHeader(http.StatusFound)
	})
	s := h.webhookSender(t)
	id := h.insertDelivery(t, e.ID, 1)
	pass(t, s)
	if d := h.delivery(t, id); d.State != "pending" || d.LastError != "redirect" || d.LastStatus == nil || *d.LastStatus != 302 {
		t.Errorf("after a 302: %+v", d)
	}
	if n := len(rc.requests("")); n != 1 {
		t.Errorf("%d requests, want the one, not followed", n)
	}
}

func TestWebhookSender_RetryAfterDelaysTheNextAttempt(t *testing.T) {
	t.Parallel()
	h, owner, _, integration, rc := senderHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{})
	h.dropPings(t)
	retryAfter := "600"
	rc.answer("hooks.example.com", func(w http.ResponseWriter) {
		w.Header().Set("Retry-After", retryAfter)
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	s := h.webhookSender(t)
	id := h.insertDelivery(t, e.ID, 1)
	at := h.now()
	pass(t, s)
	if d := h.delivery(t, id); d.NextAt == nil || !d.NextAt.Equal(at.Add(10*time.Minute)) {
		t.Errorf("next %v, want ten minutes on", d.NextAt)
	}
	// A shorter Retry-After than the schedule's step leaves the step.
	retryAfter = "5"
	h.advance(10 * time.Minute)
	at = h.now()
	pass(t, s)
	if d := h.delivery(t, id); d.NextAt == nil || !d.NextAt.Equal(at.Add(5*time.Minute)) {
		t.Errorf("next %v, want the schedule's 5 minutes", d.NextAt)
	}
	// An hour at most.
	retryAfter = "86400"
	h.advance(5 * time.Minute)
	at = h.now()
	pass(t, s)
	if d := h.delivery(t, id); d.NextAt == nil || !d.NextAt.Equal(at.Add(time.Hour)) {
		t.Errorf("next %v, want an hour", d.NextAt)
	}
}

func TestWebhookSender_GoneDisablesTheEndpointAndTellsTheOwners(t *testing.T) {
	t.Parallel()
	h, owner, ownerID, integration, rc := senderHarness(t)
	contact := h.insertUser(t, "contact@example.test")
	h.exec(t, `UPDATE identity.users SET preferred_language = 'nb' WHERE id = $1`, contact)
	h.exec(t, `UPDATE identity.integrations SET contact_user_id = $1 WHERE user_id = $2`, contact, integration)
	e, _ := createWebhook(t, owner, integration, map[string]any{"url": secretURL})
	h.dropPings(t)
	rc.status("hooks.example.com", http.StatusGone)
	s := h.webhookSender(t)
	first := h.insertDelivery(t, e.ID, 1)
	h.advance(time.Second)
	waiting := h.insertDelivery(t, e.ID, 2)
	// Only the first is sent this pass: the second is claimed too, but the
	// first's 410 settles it whichever finishes first. Run it alone.
	h.exec(t, `UPDATE identity.webhook_deliveries SET next_attempt_at = $2 WHERE id = $1`, waiting, h.now().Add(time.Hour))
	pass(t, s)

	if d := h.delivery(t, first); d.State != "dead" || d.Reason != "gone" || d.LastStatus == nil || *d.LastStatus != 410 {
		t.Errorf("the 410's delivery: %+v", d)
	}
	if d := h.delivery(t, waiting); d.State != "dead" || d.Reason != "endpoint_disabled" {
		t.Errorf("the waiting delivery: %+v", d)
	}
	r := owner.do(http.MethodGet, webhooksPath(integration)+"/"+e.ID.String(), nil)
	var got webhookEndpoint
	r.json(&got)
	if got.Status != "disabled" || got.DisabledReason == nil || *got.DisabledReason != "gone" {
		t.Errorf("endpoint %+v", got)
	}
	audit := h.auditEvents(t, "webhook.disabled")
	if len(audit) != 1 || audit[0].Actor != nil || !strings.Contains(audit[0].Details, "system:webhooks") || !strings.Contains(audit[0].After, `"disabledReason":"gone"`) {
		t.Errorf("audit %+v", audit)
	}
	assertNoSecretIn(t, "the audit", audit[0].Before+audit[0].After)

	en, nb := h.mailTo(ownerEmail), h.mailTo("contact@example.test")
	if len(en) != 1 || en[0].Subject != "A webhook endpoint of Webshop sync was disabled" || !strings.Contains(en[0].TextBody, "410 Gone") ||
		!strings.Contains(en[0].TextBody, "https://hooks.example.com ") {
		t.Errorf("the Owner's notice %+v", en)
	}
	if len(nb) != 1 || nb[0].Subject != "Et webhook-endepunkt for Webshop sync er deaktivert" || !strings.Contains(nb[0].TextBody, "410 Gone") {
		t.Errorf("the contact's notice %+v", nb)
	}
	for _, m := range append(en, nb...) {
		assertNoSecretIn(t, "a notice", m.TextBody)
	}
	_ = ownerID

	// Nothing more is sent, and the notice is not mailed again.
	h.advance(2 * time.Minute)
	pass(t, s)
	if err := s.Housekeep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(rc.requests("")); n != 1 {
		t.Errorf("%d requests, want the one", n)
	}
	if len(h.mailTo(ownerEmail)) != 1 {
		t.Error("the notice went twice")
	}
}

func TestWebhookSender_ATimeoutIsAFailedAttempt(t *testing.T) {
	t.Parallel()
	h, owner, _, integration, rc := senderHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{})
	h.dropPings(t)
	rc.hold("hooks.example.com")
	s := h.webhookSender(t)
	identity.SetWebhookAttemptBudget(s, 300*time.Millisecond)
	id := h.insertDelivery(t, e.ID, 1)
	pass(t, s)
	if d := h.delivery(t, id); d.State != "pending" || d.LastError != "timeout" || d.LastStatus != nil || d.RunAttempts != 1 {
		t.Errorf("after a timeout: %+v", d)
	}
}

func TestWebhookSender_AnAbortedAttemptRecordsNothing(t *testing.T) {
	t.Parallel()
	h, owner, _, integration, rc := senderHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{})
	h.dropPings(t)
	rc.hold("hooks.example.com")
	s := h.webhookSender(t)
	id := h.insertDelivery(t, e.ID, 1)
	ctx, cancel := context.WithCancel(context.Background())
	if n, err := s.RunPass(ctx); n != 1 || err != nil {
		t.Fatalf("RunPass %d %v", n, err)
	}
	waitFor(t, func() bool { return rc.heldNow("hooks.example.com") == 1 })
	cancel()
	s.Wait()
	if d := h.delivery(t, id); d.State != "pending" || d.RunAttempts != 0 || d.Total != 0 || d.LastError != "" {
		t.Errorf("after an aborted attempt: %+v", d)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.webhook_attempts`); n != 0 {
		t.Errorf("%d attempts recorded", n)
	}
}

// waitFor polls cond for up to ten seconds.
func waitFor(t testing.TB, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestWebhookSender_ALostLeaseIsNotAnAttempt is W29: an attempt whose lease
// ran out and was claimed again by another sender records nothing; the
// other's attempt is the one kept.
func TestWebhookSender_ALostLeaseIsNotAnAttempt(t *testing.T) {
	t.Parallel()
	h, owner, _, integration, rc := senderHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{})
	h.dropPings(t)
	rc.hold("hooks.example.com")
	slow, fast := h.webhookSender(t), h.webhookSender(t)
	id := h.insertDelivery(t, e.ID, 1)

	if n, err := slow.RunPass(context.Background()); n != 1 || err != nil {
		t.Fatalf("slow pass %d %v", n, err)
	}
	waitFor(t, func() bool { return rc.heldNow("hooks.example.com") == 1 })
	// While the lease holds, no other sender claims it.
	if n, err := fast.RunPass(context.Background()); n != 0 || err != nil {
		t.Fatalf("a leased delivery was claimed again: %d %v", n, err)
	}
	h.advance(3 * time.Minute)
	rc.mu.Lock()
	rc.holds["hooks.example.com"] = false
	rc.mu.Unlock()
	if n := pass(t, fast); n != 1 {
		t.Fatalf("the expired lease was not claimed: %d", n)
	}
	rc.release()
	slow.Wait()
	if d := h.delivery(t, id); d.State != "succeeded" || d.RunAttempts != 1 || d.Total != 1 {
		t.Errorf("delivery %+v, want one attempt, the second sender's", d)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.webhook_attempts WHERE delivery_id = $1`, id); n != 1 {
		t.Errorf("%d attempts recorded", n)
	}
}

// TestWebhookSender_FourInFlightPerEndpointAndTheRestGoOn is W29's cap and
// fairness: two endpoints that never answer hold 4 sends each, no more, and
// a third endpoint's messages are all sent meanwhile.
func TestWebhookSender_FourInFlightPerEndpointAndTheRestGoOn(t *testing.T) {
	t.Parallel()
	h, owner, _, integration, rc := senderHarness(t)
	slowA, _ := createWebhook(t, owner, integration, map[string]any{"url": "https://slow-a.example.com/in"})
	slowB, _ := createWebhook(t, owner, integration, map[string]any{"url": "https://slow-b.example.com/in"})
	fast, _ := createWebhook(t, owner, integration, map[string]any{"url": "https://fast.example.com/in"})
	h.dropPings(t)
	rc.hold("slow-a.example.com")
	rc.hold("slow-b.example.com")
	var fastIDs []uuid.UUID
	for i := range 10 {
		h.insertDelivery(t, slowA.ID, int64(i))
		h.insertDelivery(t, slowB.ID, int64(i))
		fastIDs = append(fastIDs, h.insertDelivery(t, fast.ID, int64(i)))
		h.advance(time.Millisecond)
	}
	s := h.webhookSender(t)
	ctx := context.Background()
	// Pass until the fast endpoint has all ten, the slow ones hanging.
	waitFor(t, func() bool {
		if _, err := s.RunPass(ctx); err != nil {
			t.Fatal(err)
		}
		return h.count(t, `SELECT count(*) FROM identity.webhook_deliveries WHERE endpoint_id = $1 AND state = 'succeeded'`, fast.ID) == 10
	})
	waitFor(t, func() bool { return rc.heldNow("slow-a.example.com") == 4 && rc.heldNow("slow-b.example.com") == 4 })
	for _, host := range []string{"slow-a.example.com", "slow-b.example.com"} {
		if got := rc.mostHeld(host); got != 4 {
			t.Errorf("%s held %d at once, want 4", host, got)
		}
	}
	// Every one of the fast endpoint's ten went out, each once. Their order
	// is not asserted: sends of one pass run concurrently, so the order in
	// which the requests arrive is the scheduler's (W21 promises none).
	sent := map[string]int{}
	for _, r := range rc.requests("fast.example.com") {
		sent[r.header.Get("webhook-id")]++
	}
	for _, id := range fastIDs {
		if sent[msgID(id)] != 1 {
			t.Errorf("the fast endpoint's message %s was sent %d times, want once", msgID(id), sent[msgID(id)])
		}
	}
	rc.release()
	s.Wait()
}

// TestWebhookSender_TwoSendersNeverSendOneDeliveryTwice is W29 across
// processes: two senders passing at once keep to one endpoint's cap of 4
// and send each message once.
func TestWebhookSender_TwoSendersNeverSendOneDeliveryTwice(t *testing.T) {
	t.Parallel()
	h, owner, _, integration, rc := senderHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{})
	h.dropPings(t)
	rc.hold("hooks.example.com")
	for i := range 12 {
		h.insertDelivery(t, e.ID, int64(i))
	}
	a, b := h.webhookSender(t), h.webhookSender(t)
	ctx := context.Background()
	for range 5 {
		var wg sync.WaitGroup
		for _, s := range []identity.WebhookSender{a, b} {
			wg.Go(func() {
				if _, err := s.RunPass(ctx); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
	}
	// Every pass has returned: what is leased now is all that can be in
	// flight, and the receiver holds exactly that.
	if n := h.count(t, `SELECT count(*) FROM identity.webhook_deliveries WHERE lease_until > $1`, h.now()); n != 4 {
		t.Errorf("%d leased across two senders, want 4", n)
	}
	waitFor(t, func() bool { return rc.heldNow("hooks.example.com") == 4 })
	if got := rc.mostHeld("hooks.example.com"); got != 4 {
		t.Errorf("%d in flight at once across two senders, want 4", got)
	}
	rc.mu.Lock()
	rc.holds["hooks.example.com"] = false
	rc.mu.Unlock()
	rc.release()
	a.Wait()
	b.Wait()
	for range 10 {
		pass(t, a)
		pass(t, b)
	}
	seen := map[string]int{}
	for _, r := range rc.requests("hooks.example.com") {
		seen[r.header.Get("webhook-id")]++
	}
	if len(seen) != 12 {
		t.Errorf("%d distinct messages, want 12", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("%s sent %d times", id, n)
		}
	}
	if n := h.count(t, `SELECT count(*) FROM identity.webhook_deliveries WHERE state = 'succeeded' AND total_attempts = 1`); n != 12 {
		t.Errorf("%d delivered once, want 12", n)
	}
}

func TestWebhookSender_BothSecretsSignDuringTheOverlapAndNotAfter(t *testing.T) {
	t.Parallel()
	h, owner, _, integration, rc := senderHarness(t)
	e, old := createWebhook(t, owner, integration, map[string]any{})
	h.dropPings(t)
	r := owner.do(http.MethodPost, webhooksPath(integration)+"/"+e.ID.String()+"/roll-secret", map[string]any{"overlapHours": 2})
	if r.status != http.StatusCreated {
		t.Fatalf("roll: %d %s", r.status, r.body)
	}
	var rolled struct {
		Secret string `json:"secret"`
	}
	r.json(&rolled)
	s := h.webhookSender(t)
	during := h.insertDelivery(t, e.ID, 1)
	pass(t, s)
	got := rc.requests("hooks.example.com")
	if len(got) != 1 || len(strings.Fields(got[0].header.Get("webhook-signature"))) != 2 || !verifies(t, rolled.Secret, got[0]) || !verifies(t, old, got[0]) {
		t.Fatalf("during the overlap: %v", got)
	}
	if first := strings.Fields(got[0].header.Get("webhook-signature"))[0]; !verifies(t, rolled.Secret, received{header: http.Header{
		"Webhook-Id": {got[0].header.Get("webhook-id")}, "Webhook-Timestamp": {got[0].header.Get("webhook-timestamp")}, "Webhook-Signature": {first},
	}, body: got[0].body}) {
		t.Error("the current secret's signature is not first")
	}
	var used string
	if err := h.pool.QueryRow(context.Background(), `SELECT secret_used FROM identity.webhook_attempts WHERE delivery_id = $1`, during).Scan(&used); err != nil || used != "both" {
		t.Errorf("secret_used %q %v", used, err)
	}

	// After the overlap the previous secret never signs, though its columns
	// are only cleared by the endpoint's next write.
	h.advance(2*time.Hour + time.Second)
	after := h.insertDelivery(t, e.ID, 2)
	pass(t, s)
	got = rc.requests("hooks.example.com")
	if len(got) != 2 || len(strings.Fields(got[1].header.Get("webhook-signature"))) != 1 || !verifies(t, rolled.Secret, got[1]) || verifies(t, old, got[1]) {
		t.Errorf("after the overlap: %v", got[1].header)
	}
	if err := h.pool.QueryRow(context.Background(), `SELECT secret_used FROM identity.webhook_attempts WHERE delivery_id = $1`, after).Scan(&used); err != nil || used != "current" {
		t.Errorf("secret_used %q %v", used, err)
	}
}

// failFor makes an endpoint look as if it had failed for six days with
// five deliveries dead by running out of attempts.
func (h *harness) failFor(t testing.TB, endpoint uuid.UUID, base int64) {
	t.Helper()
	h.exec(t, `UPDATE identity.webhook_endpoints SET failing_since = $2 WHERE id = $1`, endpoint, h.now().Add(-6*24*time.Hour))
	for i := range int64(5) {
		h.exec(t, `INSERT INTO identity.webhook_deliveries (id, endpoint_id, event_id, event_name, kind, body, state, state_reason, run_attempts, max_run_attempts, total_attempts, created_at, completed_at)
		           VALUES ($1, $2, $3, $4, 'event', '{}', 'dead', 'attempts_exhausted', 8, 8, 8, $5, $5)`,
			uuid.New(), endpoint, base+i, confirmed, h.now().Add(-time.Hour))
	}
}

func TestWebhookSender_AFailingEndpointIsDisabledAndTheNoticeGoesOncePerDisabling(t *testing.T) {
	t.Parallel()
	h, owner, _, integration, rc := senderHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{"url": secretURL})
	h.dropPings(t)
	rc.status("hooks.example.com", http.StatusInternalServerError)
	s := h.webhookSender(t)
	path := webhooksPath(integration) + "/" + e.ID.String()

	// Four dead and five days failing is not enough; nor five dead and four
	// days; nor five that died another way.
	h.exec(t, `UPDATE identity.webhook_endpoints SET failing_since = $2 WHERE id = $1`, e.ID, h.now().Add(-6*24*time.Hour))
	for i, reason := range []string{"attempts_exhausted", "attempts_exhausted", "attempts_exhausted", "attempts_exhausted", "expired", "endpoint_disabled"} {
		h.exec(t, `INSERT INTO identity.webhook_deliveries (id, endpoint_id, event_id, event_name, kind, body, state, state_reason, max_run_attempts, created_at, completed_at)
		           VALUES ($1, $2, $3, $4, 'event', '{}', 'dead', $5, 8, $6, $6)`, uuid.New(), e.ID, 900+i, confirmed, reason, h.now())
	}
	h.exec(t, `INSERT INTO identity.webhook_deliveries (id, endpoint_id, event_name, kind, body, state, state_reason, max_run_attempts, created_at, completed_at)
	           VALUES ($1, $2, 'webhook.ping', 'ping', '{}', 'dead', 'attempts_exhausted', 1, $3, $3)`, uuid.New(), e.ID, h.now())
	pass(t, s)
	if r := owner.do(http.MethodGet, path, nil); !strings.Contains(string(r.body), `"status":"enabled"`) {
		t.Fatalf("disabled too early: %s", r.body)
	}
	h.exec(t, `DELETE FROM identity.webhook_deliveries`)

	h.failFor(t, e.ID, 1)
	pending := h.insertDelivery(t, e.ID, 50)
	h.exec(t, `UPDATE identity.webhook_deliveries SET next_attempt_at = $2 WHERE id = $1`, pending, h.now().Add(time.Hour))
	pass(t, s)
	var got webhookEndpoint
	owner.do(http.MethodGet, path, nil).json(&got)
	if got.Status != "disabled" || got.DisabledReason == nil || *got.DisabledReason != "failing" {
		t.Fatalf("endpoint %+v", got)
	}
	if d := h.delivery(t, pending); d.State != "dead" || d.Reason != "endpoint_disabled" {
		t.Errorf("its pending delivery %+v", d)
	}
	mails := h.mailTo(ownerEmail)
	if len(mails) != 1 || !strings.Contains(mails[0].TextBody, "failed for 5 days") || !strings.Contains(mails[0].TextBody, "https://hooks.example.com ") {
		t.Fatalf("notices %+v", mails)
	}
	assertNoSecretIn(t, "the notice", mails[0].TextBody)
	audit := h.auditEvents(t, "webhook.disabled")
	if len(audit) != 1 || !strings.Contains(audit[0].Details, "system:webhooks") || !strings.Contains(audit[0].After, `"disabledReason":"failing"`) {
		t.Errorf("audit %+v", audit)
	}
	// Once per disabling.
	pass(t, s)
	if err := s.Housekeep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(h.mailTo(ownerEmail)); n != 1 {
		t.Errorf("%d notices, want one", n)
	}

	// Enabled again, it starts afresh with a ping, which fails without
	// counting; failing again, it is disabled and the notice goes again.
	if r := owner.do(http.MethodPost, path+"/enable", nil); r.status != http.StatusOK {
		t.Fatalf("enable: %d", r.status)
	}
	h.advance(time.Minute)
	pass(t, s)
	if pings := h.deliveries(t, "kind = 'ping' AND endpoint_id = $1", e.ID); len(pings) != 1 || pings[0].State != "dead" || pings[0].Reason != "attempts_exhausted" || pings[0].RunAttempts != 1 {
		t.Errorf("the re-enable's ping %+v", pings)
	}
	var failingSince *time.Time
	if err := h.pool.QueryRow(context.Background(), `SELECT failing_since FROM identity.webhook_endpoints WHERE id = $1`, e.ID).Scan(&failingSince); err != nil || failingSince != nil {
		t.Errorf("a failed ping set failing_since %v %v", failingSince, err)
	}
	h.exec(t, `DELETE FROM identity.webhook_deliveries`)
	h.failFor(t, e.ID, 100)
	pass(t, s)
	if n := len(h.mailTo(ownerEmail)); n != 2 {
		t.Errorf("%d notices after a second disabling, want two", n)
	}
}

func TestWebhookSender_ANoticeNobodyGotIsTriedAgain(t *testing.T) {
	t.Parallel()
	h, owner, ownerID, integration, rc := senderHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{})
	h.dropPings(t)
	rc.status("hooks.example.com", http.StatusGone)
	s := h.webhookSender(t)
	// No one can be mailed: the only Owner is disabled.
	h.exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, ownerID)
	h.insertDelivery(t, e.ID, 1)
	pass(t, s)
	var sentAt *time.Time
	if err := h.pool.QueryRow(context.Background(), `SELECT disabled_notice_sent_at FROM identity.webhook_endpoints WHERE id = $1`, e.ID).Scan(&sentAt); err != nil || sentAt != nil {
		t.Fatalf("an unmailed notice stayed claimed: %v %v", sentAt, err)
	}
	h.exec(t, `UPDATE identity.users SET is_disabled = false WHERE id = $1`, ownerID)
	// Not before its backoff, a minute after the first try.
	if err := s.Housekeep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(h.mailTo(ownerEmail)); n != 0 {
		t.Errorf("%d notices before the backoff passed, want none", n)
	}
	h.advance(61 * time.Second)
	if err := s.Housekeep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(h.mailTo(ownerEmail)); n != 1 {
		t.Errorf("%d notices once someone could be mailed, want one", n)
	}

	// A notice owed for an endpoint disabled more than a week ago is given
	// up.
	old, _ := createWebhook(t, h.signIn(t, ownerID, false), integration, map[string]any{"url": "https://old.example.com/in"})
	h.exec(t, `UPDATE identity.webhook_endpoints SET status = 'disabled', disabled_reason = 'gone', disabled_at = $2 WHERE id = $1`,
		old.ID, h.now().Add(-8*24*time.Hour))
	if err := s.Housekeep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(h.mailTo(ownerEmail)); n != 1 {
		t.Errorf("%d notices, want none for an endpoint disabled 8 days ago", n)
	}
}

// TestWebhookSender_ANoticeNamesTheIntegrationWithoutControlCharacters is S6:
// the integration's name is an Owner's text, and no line break or
// bidirectional override of it reaches a notice.
func TestWebhookSender_ANoticeNamesTheIntegrationWithoutControlCharacters(t *testing.T) {
	t.Parallel()
	h, owner, _, integration, rc := senderHarness(t)
	h.exec(t, `UPDATE identity.integrations SET name = $2 WHERE user_id = $1`, integration, "Web\r\nBcc: x@example.com\u202eshop")
	e, _ := createWebhook(t, owner, integration, map[string]any{})
	h.dropPings(t)
	rc.status("hooks.example.com", http.StatusGone)
	h.insertDelivery(t, e.ID, 1)
	pass(t, h.webhookSender(t))
	mails := h.mailTo(ownerEmail)
	if len(mails) != 1 {
		t.Fatalf("%d notices", len(mails))
	}
	for _, text := range []string{mails[0].Subject, mails[0].TextBody} {
		if strings.ContainsAny(text, "\r\u202e") || strings.Contains(text, "Web\n") {
			t.Errorf("a notice carries the name's control characters: %q", text)
		}
	}
	if !strings.Contains(mails[0].Subject, "Web  Bcc: x@example.com shop") {
		t.Errorf("subject %q", mails[0].Subject)
	}
}

func TestWebhookSender_APermissionLostSinceTheFanOutSkipsTheDelivery(t *testing.T) {
	t.Parallel()
	h, owner, _, integration, rc := senderHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{})
	h.dropPings(t)
	id := h.insertDelivery(t, e.ID, 1)
	h.exec(t, `DELETE FROM identity.role_permissions WHERE permission_key = 'orders:access'`)
	s := h.webhookSender(t)
	pass(t, s)
	if d := h.delivery(t, id); d.State != "skipped" || d.Reason != "permission_not_held" || d.RunAttempts != 0 || d.CompletedAt == nil {
		t.Errorf("delivery %+v", d)
	}
	if n := len(rc.requests("")); n != 0 {
		t.Errorf("%d requests sent", n)
	}
}

func TestWebhookSender_ExpiresWhatWaitedThreeDaysAndPrunesWhatIsAMonthOld(t *testing.T) {
	t.Parallel()
	h, owner, _, integration, _ := senderHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{})
	h.dropPings(t)
	old := h.insertDelivery(t, e.ID, 1)
	recent := h.insertDelivery(t, e.ID, 2)
	ancient := h.insertDelivery(t, e.ID, 3)
	h.exec(t, `UPDATE identity.webhook_deliveries SET next_attempt_at = $1 WHERE true`, h.now().Add(48*time.Hour))
	h.exec(t, `UPDATE identity.webhook_deliveries SET created_at = $2 WHERE id = $1`, old, h.now().Add(-73*time.Hour))
	h.exec(t, `UPDATE identity.webhook_deliveries SET created_at = $2 WHERE id = $1`, recent, h.now().Add(-71*time.Hour))
	h.exec(t, `UPDATE identity.webhook_deliveries SET state = 'succeeded', next_attempt_at = NULL, completed_at = $2, created_at = $2 WHERE id = $1`,
		ancient, h.now().Add(-31*24*time.Hour))
	s := h.webhookSender(t)
	if err := s.Housekeep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := h.delivery(t, old); d.State != "dead" || d.Reason != "expired" || d.CompletedAt == nil {
		t.Errorf("three days pending: %+v", d)
	}
	if d := h.delivery(t, recent); d.State != "pending" {
		t.Errorf("under three days: %+v", d)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.webhook_deliveries WHERE id = $1`, ancient); n != 0 {
		t.Error("a delivery 31 days old was kept")
	}
	// A pending one a month old is pruned too, whatever its state.
	h.exec(t, `UPDATE identity.webhook_deliveries SET created_at = $2 WHERE id = $1`, recent, h.now().Add(-31*24*time.Hour))
	if err := s.Housekeep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.webhook_deliveries WHERE id = $1`, recent); n != 0 {
		t.Error("a pending delivery 31 days old was kept")
	}
}

func TestWebhookSender_EnablingAnIntegrationExpiresWhatQueuedADayAgo(t *testing.T) {
	t.Parallel()
	h, owner, ownerID, integration, rc := senderHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{})
	h.dropPings(t)
	s := h.webhookSender(t)
	if r := owner.do(http.MethodPost, integrationsPath+"/"+integration.String()+"/disable", nil); r.status != http.StatusOK {
		t.Fatalf("disable: %d", r.status)
	}
	// While disabled, the fan-out queues nothing and the sender sends nothing.
	h.publish(t, contracts.SalesOrderConfirmed{OrderID: 1})
	h.fanOut(t)
	if n := h.count(t, `SELECT count(*) FROM identity.webhook_deliveries`); n != 0 {
		t.Errorf("%d queued for a disabled integration", n)
	}
	stale := h.insertDelivery(t, e.ID, 1)
	// Queued as long ago, but its retry fell due only half an hour before
	// the enabling: it is not that overdue, and keeps its run.
	retrying := h.insertDelivery(t, e.ID, 3)
	h.exec(t, `UPDATE identity.webhook_deliveries SET next_attempt_at = $2 WHERE id = $1`, retrying, h.now().Add(24*time.Hour+30*time.Minute))
	h.advance(25 * time.Hour)
	fresh := h.insertDelivery(t, e.ID, 2)
	pass(t, s)
	if n := len(rc.requests("")); n != 0 {
		t.Fatalf("%d sent for a disabled integration", n)
	}
	// The Owner signs in again, a day on.
	if r := h.signIn(t, ownerID, false).do(http.MethodPost, integrationsPath+"/"+integration.String()+"/enable", nil); r.status != http.StatusOK {
		t.Fatalf("enable: %d", r.status)
	}
	if d := h.delivery(t, stale); d.State != "dead" || d.Reason != "expired" {
		t.Errorf("due 25 hours ago: %+v", d)
	}
	if d := h.delivery(t, retrying); d.State != "pending" {
		t.Errorf("queued 25 hours ago but due half an hour ago: %+v", d)
	}
	pass(t, s)
	if d := h.delivery(t, fresh); d.State != "succeeded" {
		t.Errorf("queued just now: %+v", d)
	}
}

func TestWebhooks_TheTestEventIsAPingSentOnce(t *testing.T) {
	t.Parallel()
	h, owner, ownerID, integration, rc := senderHarness(t)
	e, secret := createWebhook(t, owner, integration, map[string]any{})
	h.dropPings(t)
	s := h.webhookSender(t)
	path := webhooksPath(integration) + "/" + e.ID.String()

	r := owner.do(http.MethodPost, path+"/test", nil)
	if r.status != http.StatusAccepted {
		t.Fatalf("test: %d %s", r.status, r.body)
	}
	var queued struct {
		DeliveryID uuid.UUID `json:"deliveryId"`
	}
	r.json(&queued)
	audit := h.auditEvents(t, "webhook.test_sent")
	if len(audit) != 1 || audit[0].Actor == nil || *audit[0].Actor != ownerID || !strings.Contains(audit[0].After, queued.DeliveryID.String()) {
		t.Errorf("audit %+v", audit)
	}
	rc.status("hooks.example.com", http.StatusInternalServerError)
	pass(t, s)
	d := h.delivery(t, queued.DeliveryID)
	got := rc.requests("hooks.example.com")
	if d.Kind != "ping" || d.State != "dead" || d.Reason != "attempts_exhausted" || d.RunAttempts != 1 || len(got) != 1 || !verifies(t, secret, got[0]) ||
		!strings.Contains(got[0].body, `"id":"evt_ping_`+queued.DeliveryID.String()+`"`) {
		t.Errorf("a failed ping: %+v, %d requests", d, len(got))
	}
	h.advance(time.Hour)
	if n := pass(t, s); n != 0 {
		t.Errorf("a ping was retried")
	}

	// A 410 to a ping disables the endpoint too (W37).
	if r := owner.do(http.MethodPost, path+"/test", nil); r.status != http.StatusAccepted {
		t.Fatalf("test: %d", r.status)
	}
	rc.status("hooks.example.com", http.StatusGone)
	pass(t, s)
	var after webhookEndpoint
	owner.do(http.MethodGet, path, nil).json(&after)
	if after.Status != "disabled" || after.DisabledReason == nil || *after.DisabledReason != "gone" {
		t.Errorf("after a ping's 410: %+v", after)
	}
	// Not to a disabled endpoint, nor on an archived integration.
	if r := owner.do(http.MethodPost, path+"/test", nil); r.status != http.StatusConflict || r.code() != "webhook_endpoint_disabled" {
		t.Errorf("a disabled endpoint's test: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPost, webhooksPath(integration)+"/"+uuid.New().String()+"/test", nil); r.status != http.StatusNotFound {
		t.Errorf("no such endpoint: %d", r.status)
	}
	if r := owner.do(http.MethodPost, integrationsPath+"/"+integration.String()+"/archive", nil); r.status != http.StatusOK {
		t.Fatalf("archive: %d", r.status)
	}
	if r := owner.do(http.MethodPost, path+"/test", nil); r.status != http.StatusConflict || r.code() != "integration_archived" {
		t.Errorf("an archived integration's test: %d %s", r.status, r.body)
	}
	// No one but an Owner sends one.
	_, key := createKey(t, owner, createIntegration(t, owner, "Other").Integration.ID, nil)
	if r := h.client(t).do(http.MethodPost, path+"/test", nil, header("Authorization", "Bearer "+key)); r.status != http.StatusForbidden {
		t.Errorf("a key's test: %d", r.status)
	}
}

func TestWebhookSender_IsSwitchedOnAndOffByItsOwnSetting(t *testing.T) {
	t.Parallel()
	names := func(h *harness) []string {
		var out []string
		for _, w := range module.Workers(h.deps, h.mods...) {
			out = append(out, w.Name())
		}
		return out
	}
	for _, c := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{}, "identity.api-keys,identity.webhook-sender"},
		// identity.api-keys also prunes the Idempotency-Keys, under its own
		// switch (idempotency design I21).
		{map[string]string{"IDENTITY_API_KEYS_ENABLED": "0"}, "identity.api-keys,identity.webhook-sender"},
		// And OAuth's prune under OAUTH_ENABLED (OAuth connections design A21).
		{map[string]string{"IDENTITY_API_KEYS_ENABLED": "0", "IDEMPOTENCY_ENABLED": "0"}, "identity.api-keys,identity.webhook-sender"},
		{map[string]string{"IDENTITY_API_KEYS_ENABLED": "0", "IDEMPOTENCY_ENABLED": "0", "OAUTH_ENABLED": "0"}, "identity.webhook-sender"},
		{map[string]string{"IDENTITY_WEBHOOKS_ENABLED": "0"}, "identity.api-keys"},
	} {
		var opts []harnessOption
		for k, v := range c.env {
			opts = append(opts, withEnv(k, v))
		}
		h := newHarness(t, opts...)
		var identityWorkers []string
		for _, n := range names(h) {
			if strings.HasPrefix(n, "identity.") {
				identityWorkers = append(identityWorkers, n)
			}
		}
		if got := strings.Join(identityWorkers, ","); got != c.want {
			t.Errorf("%v: %s, want %s", c.env, got, c.want)
		}
	}
}
