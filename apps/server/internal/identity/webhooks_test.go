package identity_test

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// The webhook endpoints service (outgoing webhooks design W1-W3, W6-W11,
// W24, W25, W28, W31, W36).

var (
	ordersAccess   = contracts.Permission{Key: "orders:access", Display: "Use Orders", Description: "Read and work orders.", Category: "Orders", Delegable: true}
	invoicesAccess = contracts.Permission{Key: "invoices:access", Display: "Use Invoices", Description: "Read invoices.", Category: "Invoices", Delegable: true}
)

// webhookHosts is the harness's resolver for webhook hosts: every name is a
// public address but private.example.com (a private one), mixed.example.com
// (a public and a private one) and nxdomain.example.com (no such host).
var webhookHosts = hostResolver{}

type hostResolver struct{}

func (hostResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	switch host {
	case "private.example.com":
		return []netip.Addr{netip.MustParseAddr("10.0.0.7")}, nil
	case "mixed.example.com":
		return []netip.Addr{netip.MustParseAddr("93.184.215.14"), netip.MustParseAddr("169.254.169.254")}, nil
	case "nxdomain.example.com":
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	return []netip.Addr{netip.MustParseAddr("93.184.215.14")}, nil
}

// webhookEndpoint is WebhookEndpoint as these tests read it.
type webhookEndpoint struct {
	ID                      uuid.UUID  `json:"id"`
	IntegrationID           uuid.UUID  `json:"integrationId"`
	URL                     string     `json:"url"`
	Description             *string    `json:"description"`
	Events                  []string   `json:"events"`
	Status                  string     `json:"status"`
	DisabledReason          *string    `json:"disabledReason"`
	FailingSince            *time.Time `json:"failingSince"`
	PreviousSecretExpiresAt *time.Time `json:"previousSecretExpiresAt"`
	Version                 string     `json:"version"`
}

const (
	confirmed = "orders.sales_order.confirmed"
	cancelled = "orders.sales_order.cancelled"
	overdue   = "invoices.invoice.overdue"
	// secretURL carries a token in its path and its query, which the audit
	// and the log must never hold (W28).
	secretURL = "https://hooks.example.com/catch/T0K3N-PATH?token=Q-T0K3N"
)

// webhookHarness is an Owner and an integration whose role holds
// orders:access but not invoices:access.
func webhookHarness(t *testing.T) (*harness, *client, uuid.UUID, uuid.UUID) {
	t.Helper()
	h := newHarness(t, withPermissions(ordersAccess, invoicesAccess))
	owner, ownerID := h.bootstrapOwner(t)
	role := h.insertRole(t, "Webshop", "orders:access")
	return h, owner, ownerID, createIntegration(t, owner, "Webshop sync", role).Integration.ID
}

func webhooksPath(integration uuid.UUID) string {
	return integrationsPath + "/" + integration.String() + "/webhooks"
}

// createWebhook creates an endpoint with body as the Owner c, which must
// succeed, and answers it and its secret.
func createWebhook(t testing.TB, c *client, integration uuid.UUID, body map[string]any) (webhookEndpoint, string) {
	t.Helper()
	if _, ok := body["url"]; !ok {
		body["url"] = "https://hooks.example.com/in"
	}
	if _, ok := body["events"]; !ok {
		body["events"] = []string{confirmed}
	}
	r := c.do(http.MethodPost, webhooksPath(integration), body)
	if r.status != http.StatusCreated {
		t.Fatalf("create webhook: status %d body %s", r.status, r.body)
	}
	if got := r.header("Cache-Control"); got != "no-store" {
		t.Errorf("a secret's Cache-Control = %q, want no-store", got)
	}
	var out struct {
		Endpoint webhookEndpoint `json:"endpoint"`
		Secret   string          `json:"secret"`
	}
	r.json(&out)
	return out.Endpoint, out.Secret
}

// sealedSecrets reads an endpoint's current and previous secret from the
// database and opens them.
func (h *harness) sealedSecrets(t testing.TB, id uuid.UUID) (current, previous string) {
	t.Helper()
	var cur, prev []byte
	if err := h.pool.QueryRow(context.Background(),
		`SELECT secret_ciphertext, previous_secret_ciphertext FROM identity.webhook_endpoints WHERE id = $1`, id).Scan(&cur, &prev); err != nil {
		t.Fatal(err)
	}
	open := func(sealed []byte) string {
		if sealed == nil {
			return ""
		}
		plain, err := h.deps.Secrets.Open(identity.WebhookSecretPurpose(id), sealed)
		if err != nil {
			t.Fatalf("open the secret: %v", err)
		}
		return string(plain)
	}
	if strings.Contains(string(cur), "whsec_") {
		t.Error("the secret is stored in clear")
	}
	return open(cur), open(prev)
}

// insertDelivery queues a pending delivery for the endpoint, as the fan-out
// will, and answers its id.
func (h *harness) insertDelivery(t testing.TB, endpoint uuid.UUID, eventID int64) uuid.UUID {
	t.Helper()
	id := uuid.New()
	h.exec(t, `INSERT INTO identity.webhook_deliveries (id, endpoint_id, event_id, event_name, kind, body, max_run_attempts, next_attempt_at, created_at)
	           VALUES ($1, $2, $3, $4, 'event', '{}', 8, $5, $5)`, id, endpoint, eventID, confirmed, h.now())
	return id
}

func (h *harness) deliveryState(t testing.TB, id uuid.UUID) (state, reason string) {
	t.Helper()
	var r *string
	if err := h.pool.QueryRow(context.Background(), `SELECT state, state_reason FROM identity.webhook_deliveries WHERE id = $1`, id).Scan(&state, &r); err != nil {
		t.Fatal(err)
	}
	if r != nil {
		reason = *r
	}
	return state, reason
}

// assertNoSecretIn fails t when text holds an endpoint secret or a URL's
// path or query token.
func assertNoSecretIn(t testing.TB, what, text string, secrets ...string) {
	t.Helper()
	for _, s := range append(secrets, "whsec_", "T0K3N", "/catch") {
		if s != "" && strings.Contains(text, s) {
			t.Errorf("%s holds %q: %s", what, s, text)
		}
	}
}

func TestWebhooks_CreateAnswersTheSecretOnceAndAuditsNeitherItNorTheURLsPath(t *testing.T) {
	t.Parallel()
	h, owner, ownerID, integration := webhookHarness(t)

	e, secret := createWebhook(t, owner, integration, map[string]any{
		"url": secretURL, "description": " Orders to the ERP ", "events": []string{confirmed, cancelled, confirmed},
	})
	if !strings.HasPrefix(secret, "whsec_") || len(secret) != len("whsec_")+44 {
		t.Errorf("secret %q", secret)
	}
	if e.URL != secretURL || e.Status != "enabled" || e.DisabledReason != nil || e.IntegrationID != integration ||
		e.Description == nil || *e.Description != "Orders to the ERP" || !slices.Equal(e.Events, []string{cancelled, confirmed}) {
		t.Errorf("created %+v", e)
	}
	if current, previous := h.sealedSecrets(t, e.ID); current != secret || previous != "" {
		t.Errorf("stored secret %q previous %q", current, previous)
	}

	// The secret is never answered again; the URL in full is, to the Owner.
	for _, path := range []string{webhooksPath(integration), webhooksPath(integration) + "/" + e.ID.String()} {
		r := owner.do(http.MethodGet, path, nil)
		if r.status != http.StatusOK || !strings.Contains(string(r.body), e.ID.String()) || !strings.Contains(string(r.body), "Q-T0K3N") {
			t.Errorf("GET %s: %d %s", path, r.status, r.body)
		}
		if strings.Contains(string(r.body), secret) || strings.Contains(string(r.body), "whsec_") {
			t.Errorf("GET %s answers the secret", path)
		}
	}

	events := h.auditEvents(t, "webhook.created")
	if len(events) != 1 || events[0].Actor == nil || *events[0].Actor != ownerID || events[0].TargetUser == nil || *events[0].TargetUser != integration {
		t.Fatalf("audit %+v", events)
	}
	if !strings.Contains(events[0].After, `"url":"https://hooks.example.com"`) || !strings.Contains(events[0].After, `"urlDigest":"`) ||
		!strings.Contains(events[0].After, confirmed) {
		t.Errorf("audit after %s", events[0].After)
	}
	assertNoSecretIn(t, "the audit", events[0].Before+events[0].After, secret)
	assertNoSecretIn(t, "the log", string(h.log.Bytes()), secret)

	// No one but an Owner reaches the endpoints, and no API key does.
	contact := h.createUser(t, owner, "person@example.test", identity.RoleUser)
	if r := h.signIn(t, contact, false).do(http.MethodGet, webhooksPath(integration), nil); r.status != http.StatusForbidden {
		t.Errorf("a person's list: %d", r.status)
	}
	_, key := createKey(t, owner, integration, nil)
	bearer := header("Authorization", "Bearer "+key)
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, webhooksPath(integration), nil},
		{http.MethodPost, webhooksPath(integration), map[string]any{"url": "https://a.example.com", "events": []string{confirmed}}},
		{http.MethodGet, "/api/v1/identity/webhook-events?integrationId=" + integration.String(), nil},
	} {
		if r := h.client(t).do(c.method, c.path, c.body, bearer); r.status != http.StatusForbidden || r.code() != "api_not_available" {
			t.Errorf("a key's %s %s: %d %s", c.method, c.path, r.status, r.body)
		}
	}
}

func TestWebhooks_TheURLAndTheEventsAreHeldToTheirRules(t *testing.T) {
	t.Parallel()
	h, owner, ownerID, integration := webhookHarness(t)
	path := webhooksPath(integration)

	for name, c := range map[string]struct {
		body   map[string]any
		status int
		code   string
	}{
		"http":                {map[string]any{"url": "http://hooks.example.com/in", "events": []string{confirmed}}, http.StatusBadRequest, "invalid_request"},
		"an address":          {map[string]any{"url": "https://93.184.215.14/in", "events": []string{confirmed}}, http.StatusBadRequest, "invalid_request"},
		"localhost":           {map[string]any{"url": "https://localhost/in", "events": []string{confirmed}}, http.StatusBadRequest, "invalid_request"},
		"internal":            {map[string]any{"url": "https://erp.corp.internal/in", "events": []string{confirmed}}, http.StatusBadRequest, "invalid_request"},
		"a password":          {map[string]any{"url": "https://u:p@hooks.example.com/in", "events": []string{confirmed}}, http.StatusBadRequest, "invalid_request"},
		"a port":              {map[string]any{"url": "https://hooks.example.com:8443/in", "events": []string{confirmed}}, http.StatusBadRequest, "invalid_request"},
		"a fragment":          {map[string]any{"url": "https://hooks.example.com/in#x", "events": []string{confirmed}}, http.StatusBadRequest, "invalid_request"},
		"no events":           {map[string]any{"url": "https://hooks.example.com/in", "events": []string{}}, http.StatusBadRequest, "invalid_request"},
		"a long description":  {map[string]any{"url": "https://hooks.example.com/in", "events": []string{confirmed}, "description": strings.Repeat("d", 501)}, http.StatusBadRequest, "invalid_request"},
		"every event":         {map[string]any{"url": "https://hooks.example.com/in", "events": []string{"*"}}, http.StatusUnprocessableEntity, "webhook_event_unknown"},
		"an unknown event":    {map[string]any{"url": "https://hooks.example.com/in", "events": []string{"orders.sales_order.exploded"}}, http.StatusUnprocessableEntity, "webhook_event_unknown"},
		"a person's event":    {map[string]any{"url": "https://hooks.example.com/in", "events": []string{"time.week.unsubmitted"}}, http.StatusUnprocessableEntity, "webhook_event_unknown"},
		"a deleted draft":     {map[string]any{"url": "https://hooks.example.com/in", "events": []string{"accounting.supplier_invoice.deleted"}}, http.StatusUnprocessableEntity, "webhook_event_unknown"},
		"not permitted":       {map[string]any{"url": "https://hooks.example.com/in", "events": []string{confirmed, overdue}}, http.StatusUnprocessableEntity, "webhook_event_not_permitted"},
		"a private host":      {map[string]any{"url": "https://private.example.com/in", "events": []string{confirmed}}, http.StatusUnprocessableEntity, "webhook_url_blocked"},
		"one private address": {map[string]any{"url": "https://mixed.example.com/in", "events": []string{confirmed}}, http.StatusUnprocessableEntity, "webhook_url_blocked"},
		"no such host":        {map[string]any{"url": "https://nxdomain.example.com/in", "events": []string{confirmed}}, http.StatusUnprocessableEntity, "webhook_url_unresolvable"},
	} {
		// Each refusal from its own client: the writes are limited per address.
		if r := h.signIn(t, ownerID, false).do(http.MethodPost, path, c.body); r.status != c.status || r.code() != c.code {
			t.Errorf("%s: %d %s, want %d %s", name, r.status, r.body, c.status, c.code)
		}
	}
	if n := h.count(t, `SELECT count(*) FROM identity.webhook_endpoints`); n != 0 {
		t.Errorf("%d endpoints after refusals", n)
	}

	// The catalogue as the integration sees it: orders' events permitted,
	// invoices' not, and nothing about people.
	r := owner.do(http.MethodGet, "/api/v1/identity/webhook-events?integrationId="+integration.String(), nil)
	var cat struct {
		Events []struct {
			Name, Module, Resource, Path string
			Permissions                  []string
			Permitted                    bool
		} `json:"events"`
	}
	r.json(&cat)
	if r.status != http.StatusOK || len(cat.Events) != len(contracts.WebhookEvents()) {
		t.Fatalf("catalogue: %d %s", r.status, r.body)
	}
	for _, e := range cat.Events {
		if want := e.Module == "orders"; e.Permitted != want {
			t.Errorf("%s permitted %v, want %v", e.Name, e.Permitted, want)
		}
		if e.Name == confirmed && (e.Resource != "sales_order" || e.Path != "/api/v1/orders/orders/{id}" || !slices.Equal(e.Permissions, []string{"orders:access"})) {
			t.Errorf("%+v", e)
		}
	}
	if r := owner.do(http.MethodGet, "/api/v1/identity/webhook-events?integrationId="+uuid.NewString(), nil); r.status != http.StatusNotFound {
		t.Errorf("an unknown integration's catalogue: %d", r.status)
	}
	if r := owner.do(http.MethodPost, webhooksPath(uuid.New()), map[string]any{"url": "https://hooks.example.com/in", "events": []string{confirmed}}); r.status != http.StatusNotFound {
		t.Errorf("an unknown integration's create: %d %s", r.status, r.body)
	}
}

func TestWebhooks_LimitsPerIntegrationAndWorkspace(t *testing.T) {
	t.Parallel()
	h, owner, ownerID, integration := webhookHarness(t)
	for range 5 {
		createWebhook(t, owner, integration, map[string]any{})
	}
	if r := owner.do(http.MethodPost, webhooksPath(integration), map[string]any{"url": "https://hooks.example.com/in", "events": []string{confirmed}}); r.status != http.StatusConflict || r.code() != "webhook_limit_reached" {
		t.Errorf("a sixth endpoint: %d %s", r.status, r.body)
	}

	// 45 more across nine other integrations make 50 in the workspace.
	role := h.insertRole(t, "Readers", "orders:access")
	for i := range 9 {
		other := createIntegration(t, owner, "Other "+string(rune('A'+i)), role).Integration.ID
		for range 5 {
			h.exec(t, `INSERT INTO identity.webhook_endpoints (id, integration_id, url, events, secret_ciphertext, created_at, updated_at, version)
			           VALUES ($1, $2, 'https://hooks.example.com/x', '{orders.sales_order.confirmed}', '\x00', $3, $3, $4)`, uuid.New(), other, h.now(), uuid.New())
		}
	}
	fresh := createIntegration(t, owner, "Fresh", role).Integration.ID
	if r := owner.do(http.MethodPost, webhooksPath(fresh), map[string]any{"url": "https://hooks.example.com/in", "events": []string{confirmed}}); r.status != http.StatusConflict || r.code() != "webhook_limit_reached" {
		t.Errorf("a 51st endpoint: %d %s", r.status, r.body)
	}
	// An archived integration's endpoints no longer count.
	archived := h.signIn(t, ownerID, false)
	if r := archived.do(http.MethodPost, integrationsPath+"/"+integration.String()+"/archive", nil); r.status != http.StatusOK {
		t.Fatalf("archive: %d %s", r.status, r.body)
	}
	createWebhook(t, h.signIn(t, ownerID, false), fresh, map[string]any{})
}

func TestWebhooks_WritesAreRateLimited(t *testing.T) {
	t.Parallel()
	_, owner, _, integration := webhookHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{})
	path := webhooksPath(integration) + "/" + e.ID.String()
	// The create was the first of 30.
	var last *resp
	for range 29 {
		last = owner.do(http.MethodPost, path+"/enable", nil)
	}
	if last.status != http.StatusOK {
		t.Fatalf("the 30th write: %d %s", last.status, last.body)
	}
	if r := owner.do(http.MethodPost, path+"/disable", nil); r.status != http.StatusTooManyRequests {
		t.Errorf("the 31st write: %d %s, want 429", r.status, r.body)
	}
	// Reads are not limited.
	if r := owner.do(http.MethodGet, path, nil); r.status != http.StatusOK {
		t.Errorf("a read: %d", r.status)
	}
}

func TestWebhooks_ChangeUnderIfMatchKeepsTheSecret(t *testing.T) {
	t.Parallel()
	h, owner, _, integration := webhookHarness(t)
	e, secret := createWebhook(t, owner, integration, map[string]any{"description": "First"})
	path := webhooksPath(integration) + "/" + e.ID.String()
	body := map[string]any{"url": "https://other.example.com/new?k=1", "events": []string{cancelled}}

	if r := owner.do(http.MethodPatch, path, body, header("If-Match", "nope")); r.status != http.StatusBadRequest {
		t.Errorf("a bad If-Match: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPatch, path, body, header("If-Match", uuid.NewString())); r.status != http.StatusPreconditionFailed || r.code() != "version_mismatch" {
		t.Errorf("a stale If-Match: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPatch, path, map[string]any{"url": "https://hooks.example.com/in", "events": []string{overdue}}, header("If-Match", e.Version)); r.status != http.StatusUnprocessableEntity || r.code() != "webhook_event_not_permitted" {
		t.Errorf("an event not permitted: %d %s", r.status, r.body)
	}
	r := owner.do(http.MethodPatch, path, body, header("If-Match", `"`+e.Version+`"`))
	if r.status != http.StatusOK {
		t.Fatalf("patch: %d %s", r.status, r.body)
	}
	var got webhookEndpoint
	r.json(&got)
	if got.URL != "https://other.example.com/new?k=1" || got.Description != nil || !slices.Equal(got.Events, []string{cancelled}) || got.Version == e.Version {
		t.Errorf("patched %+v", got)
	}
	if current, _ := h.sealedSecrets(t, e.ID); current != secret {
		t.Error("changing the URL rolled the secret")
	}
	events := h.auditEvents(t, "webhook.updated")
	if len(events) != 1 || !strings.Contains(events[0].Before, `"url":"https://hooks.example.com"`) || !strings.Contains(events[0].After, `"url":"https://other.example.com"`) ||
		strings.Contains(events[0].After, "k=1") {
		t.Errorf("audit %+v", events)
	}
	if r := owner.do(http.MethodPatch, webhooksPath(integration)+"/"+uuid.NewString(), body, header("If-Match", got.Version)); r.status != http.StatusNotFound {
		t.Errorf("an unknown endpoint: %d", r.status)
	}
}

func TestWebhooks_DisableSettlesPendingDeliveriesAndEnableStartsAfresh(t *testing.T) {
	t.Parallel()
	h, owner, _, integration := webhookHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{})
	path := webhooksPath(integration) + "/" + e.ID.String()
	pending := h.insertDelivery(t, e.ID, 1)

	r := owner.do(http.MethodPost, path+"/disable", nil)
	var got webhookEndpoint
	r.json(&got)
	if r.status != http.StatusOK || got.Status != "disabled" || got.DisabledReason == nil || *got.DisabledReason != "manual" {
		t.Fatalf("disable: %d %s", r.status, r.body)
	}
	if state, reason := h.deliveryState(t, pending); state != "dead" || reason != "endpoint_disabled" {
		t.Errorf("the pending delivery is %s (%s)", state, reason)
	}
	// Disabled again is answered as it is, and audited once.
	if r := owner.do(http.MethodPost, path+"/disable", nil); r.status != http.StatusOK {
		t.Errorf("disable again: %d", r.status)
	}
	if n := len(h.auditEvents(t, "webhook.disabled")); n != 1 {
		t.Errorf("%d webhook.disabled rows, want 1", n)
	}

	// The sender's marks, which enabling clears (W18, W32).
	h.exec(t, `UPDATE identity.webhook_endpoints SET failing_since = $2, disabled_notice_sent_at = $2 WHERE id = $1`, e.ID, h.now())
	r = owner.do(http.MethodPost, path+"/enable", nil)
	r.json(&got)
	if r.status != http.StatusOK || got.Status != "enabled" || got.DisabledReason != nil || got.FailingSince != nil {
		t.Fatalf("enable: %d %s", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.webhook_endpoints WHERE id = $1 AND disabled_notice_sent_at IS NULL AND disabled_at IS NULL`, e.ID); n != 1 {
		t.Error("enabling kept the notice's claim")
	}
	if n := len(h.auditEvents(t, "webhook.enabled")); n != 1 {
		t.Errorf("%d webhook.enabled rows, want 1", n)
	}
}

func TestWebhooks_RollKeepsThePreviousSecretForTheOverlap(t *testing.T) {
	t.Parallel()
	h, owner, _, integration := webhookHarness(t)
	e, first := createWebhook(t, owner, integration, map[string]any{})
	path := webhooksPath(integration) + "/" + e.ID.String() + "/roll-secret"

	for _, hours := range []int{0, 169} {
		if r := owner.do(http.MethodPost, path, map[string]any{"overlapHours": hours}); r.status != http.StatusBadRequest {
			t.Errorf("an overlap of %d hours: %d %s", hours, r.status, r.body)
		}
	}
	roll := func(body any) (webhookEndpoint, string) {
		t.Helper()
		r := owner.do(http.MethodPost, path, body)
		if r.status != http.StatusCreated || r.header("Cache-Control") != "no-store" {
			t.Fatalf("roll: %d %s", r.status, r.body)
		}
		var out struct {
			Endpoint webhookEndpoint `json:"endpoint"`
			Secret   string          `json:"secret"`
		}
		r.json(&out)
		return out.Endpoint, out.Secret
	}
	got, second := roll(nil)
	if second == first || !strings.HasPrefix(second, "whsec_") || got.PreviousSecretExpiresAt == nil || !got.PreviousSecretExpiresAt.Equal(h.now().Add(24*time.Hour)) {
		t.Errorf("rolled %+v", got)
	}
	if current, previous := h.sealedSecrets(t, e.ID); current != second || previous != first {
		t.Error("the previous secret is not the first")
	}
	// Rolling again within the overlap drops the first secret at once.
	got, third := roll(map[string]any{"overlapHours": 2})
	if current, previous := h.sealedSecrets(t, e.ID); current != third || previous != second {
		t.Error("a second roll kept the first secret")
	}
	if !got.PreviousSecretExpiresAt.Equal(h.now().Add(2 * time.Hour)) {
		t.Errorf("overlap until %v", got.PreviousSecretExpiresAt)
	}
	// Once the overlap is over the screen no longer shows it.
	h.advance(3 * time.Hour)
	var read webhookEndpoint
	owner.do(http.MethodGet, webhooksPath(integration)+"/"+e.ID.String(), nil).json(&read)
	if read.PreviousSecretExpiresAt != nil {
		t.Errorf("an ended overlap is shown: %v", read.PreviousSecretExpiresAt)
	}
	events := h.auditEvents(t, "webhook.secret_rolled")
	if len(events) != 2 {
		t.Fatalf("%d webhook.secret_rolled rows", len(events))
	}
	for _, ev := range events {
		assertNoSecretIn(t, "the audit", ev.Before+ev.After, first, second, third)
	}
}

func TestWebhooks_DeleteTakesTheDeliveries(t *testing.T) {
	t.Parallel()
	h, owner, _, integration := webhookHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{"url": secretURL})
	h.insertDelivery(t, e.ID, 7)
	path := webhooksPath(integration) + "/" + e.ID.String()
	if r := owner.do(http.MethodDelete, path, nil); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodGet, path, nil); r.status != http.StatusNotFound {
		t.Errorf("a deleted endpoint: %d", r.status)
	}
	if r := owner.do(http.MethodDelete, path, nil); r.status != http.StatusNotFound {
		t.Errorf("a second delete: %d", r.status)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.webhook_deliveries WHERE endpoint_id = $1`, e.ID); n != 0 {
		t.Errorf("%d deliveries left", n)
	}
	events := h.auditEvents(t, "webhook.deleted")
	if len(events) != 1 || !strings.Contains(events[0].Before, e.ID.String()) {
		t.Fatalf("audit %+v", events)
	}
	assertNoSecretIn(t, "the audit", events[0].Before)
}

// TestWebhooks_ArchivingDisablesTheEndpoints holds W2 and W36: archiving
// disables every endpoint in its transaction and settles their pending
// deliveries, and an archived integration's endpoints are not changed again
// but may be deleted.
func TestWebhooks_ArchivingDisablesTheEndpoints(t *testing.T) {
	t.Parallel()
	h, owner, ownerID, integration := webhookHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{})
	manual, _ := createWebhook(t, owner, integration, map[string]any{})
	path := webhooksPath(integration) + "/" + e.ID.String()
	if r := owner.do(http.MethodPost, webhooksPath(integration)+"/"+manual.ID.String()+"/disable", nil); r.status != http.StatusOK {
		t.Fatal(r.status)
	}
	pending := h.insertDelivery(t, e.ID, 3)

	if r := owner.do(http.MethodPost, integrationsPath+"/"+integration.String()+"/archive", nil); r.status != http.StatusOK {
		t.Fatalf("archive: %d %s", r.status, r.body)
	}
	var got webhookEndpoint
	owner.do(http.MethodGet, path, nil).json(&got)
	if got.Status != "disabled" || got.DisabledReason == nil || *got.DisabledReason != "integration_archived" {
		t.Errorf("after the archive %+v", got)
	}
	var kept webhookEndpoint
	owner.do(http.MethodGet, webhooksPath(integration)+"/"+manual.ID.String(), nil).json(&kept)
	if kept.DisabledReason == nil || *kept.DisabledReason != "manual" {
		t.Errorf("an endpoint disabled already %+v", kept)
	}
	if state, reason := h.deliveryState(t, pending); state != "dead" || reason != "integration_archived" {
		t.Errorf("the pending delivery is %s (%s)", state, reason)
	}
	events := h.auditEvents(t, "webhook.disabled")
	if len(events) != 2 || events[1].Actor == nil || *events[1].Actor != ownerID || !strings.Contains(events[1].After, "integration_archived") {
		t.Errorf("audit %+v", events)
	}

	for name, c := range map[string]struct {
		method, path string
		body         any
		opts         []reqOpt
	}{
		"create": {http.MethodPost, webhooksPath(integration), map[string]any{"url": "https://hooks.example.com/in", "events": []string{confirmed}}, nil},
		"change": {http.MethodPatch, path, map[string]any{"url": "https://hooks.example.com/in", "events": []string{confirmed}}, []reqOpt{header("If-Match", got.Version)}},
		"enable": {http.MethodPost, path + "/enable", nil, nil},
		"roll":   {http.MethodPost, path + "/roll-secret", nil, nil},
	} {
		if r := owner.do(c.method, c.path, c.body, c.opts...); r.status != http.StatusConflict || r.code() != "integration_archived" {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	if r := owner.do(http.MethodPost, path+"/disable", nil); r.status != http.StatusOK {
		t.Errorf("disable, disabled already: %d", r.status)
	}
	if r := owner.do(http.MethodDelete, path, nil); r.status != http.StatusNoContent {
		t.Errorf("delete: %d %s", r.status, r.body)
	}
}

// TestWebhooks_CreationsRaceToTheLimit holds W36: concurrent creates never
// pass the per-integration limit.
func TestWebhooks_CreationsRaceToTheLimit(t *testing.T) {
	t.Parallel()
	h, _, ownerID, integration := webhookHarness(t)
	const n = 8
	results := make(chan int, n)
	for range n {
		c := h.signIn(t, ownerID, false)
		go func() {
			r := c.do(http.MethodPost, webhooksPath(integration),
				map[string]any{"url": "https://hooks.example.com/in", "events": []string{confirmed}}, skipContract("concurrent"))
			results <- r.status
		}()
	}
	created := 0
	for range n {
		switch status := <-results; status {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
		default:
			t.Errorf("status %d", status)
		}
	}
	if created != 5 {
		t.Errorf("%d created, want 5", created)
	}
	if got := h.count(t, `SELECT count(*) FROM identity.webhook_endpoints WHERE integration_id = $1`, integration); got != 5 {
		t.Errorf("%d rows, want 5", got)
	}
}

// TestWebhooks_CreationsAcrossIntegrationsRaceToTheWorkspaceLimit holds W36's
// advisory lock: with 48 endpoints in the workspace, creates racing on
// several integrations — each locking only its own integration row — stop at
// exactly 50.
func TestWebhooks_CreationsAcrossIntegrationsRaceToTheWorkspaceLimit(t *testing.T) {
	t.Parallel()
	h, owner, ownerID, _ := webhookHarness(t)
	role := h.insertRole(t, "Readers", "orders:access")
	// 48 endpoints: nine integrations of five and one of three.
	for i := range 10 {
		seeded := createIntegration(t, owner, "Seeded "+string(rune('A'+i)), role).Integration.ID
		per := 5
		if i == 9 {
			per = 3
		}
		for range per {
			h.exec(t, `INSERT INTO identity.webhook_endpoints (id, integration_id, url, events, secret_ciphertext, created_at, updated_at, version)
			           VALUES ($1, $2, 'https://hooks.example.com/x', '{orders.sales_order.confirmed}', '\x00', $3, $3, $4)`, uuid.New(), seeded, h.now(), uuid.New())
		}
	}
	if got := h.count(t, `SELECT count(*) FROM identity.webhook_endpoints`); got != 48 {
		t.Fatalf("%d seeded, want 48", got)
	}
	// Four fresh integrations, three creates each, all at once: none reaches
	// its own limit of five, so only the workspace's can stop them.
	type racer struct {
		c           *client
		integration uuid.UUID
	}
	var racers []racer
	for i := range 4 {
		target := createIntegration(t, owner, "Racer "+string(rune('A'+i)), role).Integration.ID
		for range 3 {
			racers = append(racers, racer{h.signIn(t, ownerID, false), target})
		}
	}
	start := make(chan struct{})
	results := make(chan *resp, len(racers))
	for _, r := range racers {
		go func() {
			<-start
			results <- r.c.do(http.MethodPost, webhooksPath(r.integration),
				map[string]any{"url": "https://hooks.example.com/in", "events": []string{confirmed}}, skipContract("concurrent"))
		}()
	}
	close(start)
	created := 0
	for range racers {
		r := <-results
		switch {
		case r.status == http.StatusCreated:
			created++
		case r.status == http.StatusConflict && r.code() == "webhook_limit_reached":
		default:
			t.Errorf("status %d %s", r.status, r.body)
		}
	}
	if created != 2 {
		t.Errorf("%d created, want 2", created)
	}
	if got := h.count(t, `SELECT count(*) FROM identity.webhook_endpoints`); got != 50 {
		t.Errorf("%d endpoints in the workspace, want exactly 50", got)
	}
}
