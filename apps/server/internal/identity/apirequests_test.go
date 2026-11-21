package identity_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The request log, its partitions and the expiry notices (API access design
// K23, K25, K39).

// cycler is the identity.api-keys worker as these tests drive it.
type cycler interface {
	Name() string
	RunCycle(context.Context) (int, int, error)
}

// apiKeysWorker is the identity.api-keys worker as module.Workers builds it,
// or nil when it is switched off.
func apiKeysWorker(h *harness) cycler {
	for _, candidate := range module.Workers(h.deps, h.mods...) {
		if c, ok := candidate.(cycler); ok && c.Name() == "identity.api-keys" {
			return c
		}
	}
	return nil
}

func runCycle(t *testing.T, w cycler) {
	t.Helper()
	if _, _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
}

type loggedRequest struct {
	keyID       uuid.UUID
	operationID string
	status      int
	reason      string
}

// loggedRequests is the integration's request log, oldest first, once
// identity has written what it queued.
func (h *harness) loggedRequests(t *testing.T, integration uuid.UUID) []loggedRequest {
	t.Helper()
	h.access.FlushRequestLog()
	rows, err := h.pool.Query(context.Background(),
		`SELECT key_id, operation_id, status, coalesce(reason, '') FROM identity.api_requests WHERE integration_id = $1 ORDER BY at, reason`, integration)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []loggedRequest
	for rows.Next() {
		var r loggedRequest
		if err := rows.Scan(&r.keyID, &r.operationID, &r.status, &r.reason); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestAPIRequests_RefusalsAreRecordedWithTheirReasons: every refusal of a
// key identity found is in the log with its reason and status, a key that
// matched nothing is not, and no row holds a secret.
func TestAPIRequests_RefusalsAreRecordedWithTheirReasons(t *testing.T) {
	t.Parallel()
	h, owner, integration := keyHarness(t)
	never, neverSecret := createKey(t, owner, integration, map[string]any{"name": "Never"})
	network, networkSecret := createKey(t, owner, integration, map[string]any{"name": "Network", "allowedNetworks": []string{"198.51.100.0/24"}})
	revoked, revokedSecret := createKey(t, owner, integration, map[string]any{"name": "Revoked"})
	if r := owner.do(http.MethodPost, integrationsPath+"/"+integration.String()+"/keys/"+revoked.ID.String()+"/revoke", nil); r.status != http.StatusOK {
		t.Fatalf("revoke: %d", r.status)
	}
	expiring, expiringSecret := createKey(t, owner, integration, map[string]any{"name": "Expiring", "expiresInDays": 30})

	c := h.client(t)
	use := func(secret string, want int) {
		t.Helper()
		if r := c.do(http.MethodGet, integrationsPath, nil, header("Authorization", "Bearer "+secret)); r.status != want {
			t.Fatalf("status %d, want %d: %s", r.status, want, r.body)
		}
	}
	use(neverSecret, http.StatusForbidden)
	use(networkSecret, http.StatusUnauthorized)
	use(revokedSecret, http.StatusUnauthorized)
	use("vtg_"+strings.Repeat("A", 49), http.StatusUnauthorized) // matches nothing
	// A disabled integration's key.
	other := createIntegration(t, owner, "Disabled sync").Integration.ID
	_, disabledSecret := createKey(t, owner, other, nil)
	if r := owner.do(http.MethodPost, integrationsPath+"/"+other.String()+"/disable", nil); r.status != http.StatusOK {
		t.Fatalf("disable: %d %s", r.status, r.body)
	}
	use(disabledSecret, http.StatusUnauthorized)
	if got := h.loggedRequests(t, other); len(got) != 1 || got[0].reason != "disabled" {
		t.Errorf("a disabled integration's key: %+v", got)
	}

	h.advance(31 * 24 * time.Hour)
	use(expiringSecret, http.StatusUnauthorized)

	got := h.loggedRequests(t, integration)
	// The router stamps each with the time it arrived, so they are in order.
	want := []loggedRequest{
		{never.ID, "getIdentityIntegrations", http.StatusForbidden, "never"},
		{network.ID, "getIdentityIntegrations", http.StatusUnauthorized, "network"},
		{revoked.ID, "getIdentityIntegrations", http.StatusUnauthorized, "revoked"},
		{expiring.ID, "getIdentityIntegrations", http.StatusUnauthorized, "expired"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("logged\n%+v\nwant\n%+v", got, want)
	}

	if n := h.count(t, `SELECT count(*) FROM identity.api_requests`); n != 5 {
		t.Errorf("%d rows, want 5: the unknown key is not recorded", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.api_requests r WHERE row_to_json(r)::text LIKE '%vtg_%'`); n != 0 {
		t.Errorf("%d rows hold vtg_", n)
	}
}

// partitionOf is the partition the integration's one log row is in.
func (h *harness) partitionOf(t *testing.T, integration uuid.UUID) string {
	t.Helper()
	var name string
	if err := h.pool.QueryRow(context.Background(), `SELECT tableoid::regclass::text FROM identity.api_requests WHERE integration_id = $1`, integration).Scan(&name); err != nil {
		t.Fatal(err)
	}
	return name
}

func (h *harness) logRow(t *testing.T, integration uuid.UUID, at time.Time) {
	t.Helper()
	h.exec(t, `INSERT INTO identity.api_requests (at, key_id, integration_id, method, operation_id, status, duration_ms)
	           VALUES ($1, $2, $3, 'GET', 'getCustomers', 200, 4)`, at, uuid.New(), integration)
}

func (h *harness) partitionExists(t *testing.T, name string) bool {
	t.Helper()
	return h.count(t, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'identity' AND c.relname = $1`, name) == 1
}

// TestAPIRequests_TheWorkerKeepsThePartitions is K39: a row of a day with no
// partition lands in DEFAULT; the worker moves it into its day's new
// partition, makes the next days, runs cleanly twice, and drops the days
// older than 30, from DEFAULT too.
func TestAPIRequests_TheWorkerKeepsThePartitions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	w := apiKeysWorker(h)
	if w == nil {
		t.Fatal("no identity.api-keys worker")
	}
	today, old := uuid.New(), uuid.New()
	h.logRow(t, today, h.now())
	h.logRow(t, old, h.now().AddDate(0, 0, -45))
	if got := h.partitionOf(t, today); got != "identity.api_requests_default" {
		t.Fatalf("a row of a day with no partition is in %s, want DEFAULT", got)
	}

	runCycle(t, w)
	if got := h.partitionOf(t, today); got != "identity.api_requests_20260911" {
		t.Errorf("after the cycle the row is in %s, want its day's partition", got)
	}
	for _, day := range []string{"20260911", "20260914", "20260917"} {
		if !h.partitionExists(t, "api_requests_"+day) {
			t.Errorf("no partition of %s", day)
		}
	}
	if h.partitionExists(t, "api_requests_20260918") {
		t.Error("a partition 8 days ahead")
	}
	if n := h.count(t, `SELECT count(*) FROM identity.api_requests WHERE integration_id = $1`, old); n != 0 {
		t.Errorf("a row 45 days old is kept in DEFAULT")
	}
	runCycle(t, w) // again: nothing to do, and no error
	if n := h.count(t, `SELECT count(*) FROM identity.api_requests WHERE integration_id = $1`, today); n != 1 {
		t.Errorf("%d rows after a second cycle, want the one", n)
	}

	h.advance(31 * 24 * time.Hour)
	runCycle(t, w)
	// 31 days on, today is 12 October: the 30 days kept begin on 13 September.
	if h.partitionExists(t, "api_requests_20260911") || h.partitionExists(t, "api_requests_20260912") {
		t.Error("a partition older than the 30 days kept, today included, is kept")
	}
	if !h.partitionExists(t, "api_requests_20260913") || !h.partitionExists(t, "api_requests_20261018") {
		t.Error("the 30 days and the next 7 are not all there")
	}
}

// TestAPIRequests_WithTheWorkerOffRowsLandInDefault: no partition is made,
// and every row goes to DEFAULT. The Idempotency-Key prune, which runs the
// worker by itself, is off too, and so is OAuth's prune.
func TestAPIRequests_WithTheWorkerOffRowsLandInDefault(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("IDENTITY_API_KEYS_ENABLED", "0"), withEnv("IDEMPOTENCY_ENABLED", "0"), withEnv("OAUTH_ENABLED", "0"))
	if apiKeysWorker(h) != nil {
		t.Fatal("the worker runs while switched off")
	}
	// A day no partition was made for: the harness's, before the database's
	// migration made its 7 days.
	integration := uuid.New()
	h.access.Record(context.Background(), contracts.APIRequest{
		At: h.now(), KeyID: uuid.New(), IntegrationID: integration, Method: http.MethodGet,
		OperationID: "getCustomers", Status: http.StatusOK, Duration: time.Millisecond,
	})
	if got := h.loggedRequests(t, integration); len(got) != 1 {
		t.Fatalf("%d rows, want 1", len(got))
	}
	if got := h.partitionOf(t, integration); got != "identity.api_requests_default" {
		t.Errorf("the row is in %s, want DEFAULT", got)
	}
}

// TestAPIRequests_TheScreensRead is the log endpoint: per day and status
// class, the operations called most and the latest, under each filter.
func TestAPIRequests_TheScreensRead(t *testing.T) {
	t.Parallel()
	h, owner, integration := keyHarness(t)
	a, b := uuid.New(), uuid.New()
	ins := func(at time.Time, key uuid.UUID, op string, status int, reason any) {
		h.exec(t, `INSERT INTO identity.api_requests (at, key_id, integration_id, method, operation_id, status, reason, duration_ms, client_ip)
		           VALUES ($1, $2, $3, 'GET', $4, $5, $6, 7, '192.0.2.1')`, at, key, integration, op, status, reason)
	}
	now := h.now()
	ins(now.Add(-time.Hour), a, "getCustomers", 200, nil)
	ins(now.Add(-2*time.Hour), a, "getCustomers", 200, nil)
	ins(now.Add(-3*time.Hour), b, "postCustomers", 429, "rate_limited")
	ins(now.AddDate(0, 0, -1), b, "getCustomers", 401, "expired")
	ins(now.AddDate(0, 0, -40), a, "getCustomers", 200, nil) // past the 30 days
	ins(now.Add(-time.Hour), a, "getOther", 200, nil)
	h.exec(t, `UPDATE identity.api_requests SET integration_id = $1 WHERE operation_id = 'getOther'`, uuid.New())

	type log struct {
		Days []struct {
			Day         string `json:"day"`
			StatusClass int    `json:"statusClass"`
			Requests    int    `json:"requests"`
		} `json:"days"`
		Operations []struct {
			OperationID string `json:"operationId"`
			Requests    int    `json:"requests"`
		} `json:"operations"`
		Latest []struct {
			KeyID       uuid.UUID `json:"keyId"`
			OperationID string    `json:"operationId"`
			Status      int       `json:"status"`
			Reason      *string   `json:"reason"`
		} `json:"latest"`
	}
	read := func(query string) log {
		t.Helper()
		r := owner.do(http.MethodGet, integrationsPath+"/"+integration.String()+"/requests"+query, nil)
		if r.status != http.StatusOK {
			t.Fatalf("%s: %d %s", query, r.status, r.body)
		}
		var l log
		r.json(&l)
		return l
	}

	all := read("")
	if len(all.Latest) != 4 || all.Latest[0].OperationID != "getCustomers" || all.Latest[0].KeyID != a {
		t.Errorf("latest %+v, want the 4 of the 30 days, newest first", all.Latest)
	}
	if len(all.Operations) != 2 || all.Operations[0].OperationID != "getCustomers" || all.Operations[0].Requests != 3 {
		t.Errorf("operations %+v", all.Operations)
	}
	if len(all.Days) != 3 {
		t.Errorf("days %+v, want yesterday's 4xx, and today's 2xx and 4xx", all.Days)
	}
	if l := read("?key=" + b.String()); len(l.Latest) != 2 {
		t.Errorf("by key: %+v", l.Latest)
	}
	if l := read("?statusClass=4"); len(l.Latest) != 2 {
		t.Errorf("by status class: %+v", l.Latest)
	}
	if l := read("?reason=rate_limited"); len(l.Latest) != 1 || *l.Latest[0].Reason != "rate_limited" {
		t.Errorf("by reason: %+v", l.Latest)
	}
	if l := read("?day=" + now.AddDate(0, 0, -1).Format(time.DateOnly)); len(l.Latest) != 1 || l.Latest[0].Status != 401 {
		t.Errorf("by day: %+v", l.Latest)
	}
	if r := owner.do(http.MethodGet, integrationsPath+"/"+integration.String()+"/requests?day="+now.AddDate(0, 0, -31).Format(time.DateOnly), nil); r.status != http.StatusBadRequest {
		t.Errorf("a day past the 30: %d, want 400", r.status)
	}
	if r := owner.do(http.MethodGet, integrationsPath+"/"+uuid.NewString()+"/requests", nil); r.status != http.StatusNotFound {
		t.Errorf("no such integration: %d, want 404", r.status)
	}
	_, secret := createKey(t, owner, integration, nil)
	if r := h.client(t).do(http.MethodGet, integrationsPath+"/"+integration.String()+"/requests", nil, header("Authorization", "Bearer "+secret)); r.status != http.StatusForbidden || r.code() != "api_not_available" {
		t.Errorf("a key reading the log: %d %s, want api_not_available", r.status, r.code())
	}
}

// TestAPIKeyNotices: 14 days, 3 days and the day are each mailed once, to the
// contact in their language and to the Owners; a rolled key gets none; a
// disabled contact is skipped while the Owners still get it.
func TestAPIKeyNotices(t *testing.T) {
	t.Parallel()
	h, owner, integration := keyHarness(t)
	w := apiKeysWorker(h)
	contact := h.insertUser(t, "contact@example.test")
	h.exec(t, `UPDATE identity.users SET preferred_language = 'nb' WHERE id = $1`, contact)
	h.exec(t, `UPDATE identity.integrations SET contact_user_id = $1 WHERE user_id = $2`, contact, integration)
	createKey(t, owner, integration, map[string]any{"name": "Production", "expiresInDays": 30})
	rolled, _ := createKey(t, owner, integration, map[string]any{"name": "Rolled", "expiresInDays": 30})
	if r := owner.do(http.MethodPost, integrationsPath+"/"+integration.String()+"/keys/"+rolled.ID.String()+"/roll", map[string]any{"name": "Successor", "expiresInDays": 365}); r.status != http.StatusCreated {
		t.Fatalf("roll: %d %s", r.status, r.body)
	}

	notices := func(to string) []string {
		var out []string
		for _, m := range h.mailTo(to) {
			if strings.Contains(m.TextBody, "API") {
				out = append(out, m.Subject)
			}
		}
		return out
	}
	runCycle(t, w)
	if got := notices(ownerEmail); len(got) != 0 {
		t.Fatalf("30 days ahead: %v, want none", got)
	}
	h.advance(16 * 24 * time.Hour)
	runCycle(t, w)
	runCycle(t, w)
	if got := notices(ownerEmail); len(got) != 1 || got[0] != "An API key of Webshop sync expires soon" {
		t.Errorf("the Owner at 14 days: %v", got)
	}
	if got := h.mailTo("contact@example.test"); len(got) != 1 || got[0].Subject != "En API-nøkkel for Webshop sync utløper snart" ||
		!strings.Contains(got[0].TextBody, "«Production»") || strings.Contains(got[0].TextBody, "vtg_") {
		t.Errorf("the contact at 14 days: %+v", got)
	}

	h.exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, contact)
	h.advance(11 * 24 * time.Hour)
	runCycle(t, w)
	if got := notices(ownerEmail); len(got) != 2 {
		t.Errorf("the Owner at 3 days: %v", got)
	}
	if got := h.mailTo("contact@example.test"); len(got) != 1 {
		t.Errorf("a disabled contact got %d mails, want the first only", len(got))
	}

	h.advance(3*24*time.Hour - time.Hour) // the day it expires, an hour before
	runCycle(t, w)
	runCycle(t, w)
	if got := notices(ownerEmail); len(got) != 3 || got[2] != "An API key of Webshop sync expires today" {
		t.Errorf("the Owner on the day: %v", got)
	}
	for _, m := range h.mail.Messages() {
		if strings.Contains(m.TextBody, "Rolled") || strings.Contains(m.TextBody, "Successor") {
			t.Errorf("a notice of the rolled key or its successor: %+v", m)
		}
	}
	if n := h.count(t, `SELECT count(*) FROM identity.api_key_notices`); n != 3 {
		t.Errorf("%d notices recorded, want 3", n)
	}
}

// TestAPIKeyNotices_ANoticeNoOneGotIsTriedAgain: when no recipient could be
// mailed the claim is taken back, and the next cycle sends it; a key past
// its expiry gets no notice of the day it expired.
func TestAPIKeyNotices_ANoticeNoOneGotIsTriedAgain(t *testing.T) {
	t.Parallel()
	h, owner, integration := keyHarness(t)
	w := apiKeysWorker(h)
	createKey(t, owner, integration, map[string]any{"name": "Production", "expiresInDays": 30})
	notices := func() int {
		n := 0
		for _, m := range h.mailTo(ownerEmail) {
			if strings.Contains(m.Subject, "API key") {
				n++
			}
		}
		return n
	}

	h.advance(16 * 24 * time.Hour)
	h.mail.FailNext(errors.New("the mail server is down"))
	runCycle(t, w)
	if n := notices(); n != 0 {
		t.Fatalf("%d notices while mail failed", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.api_key_notices`); n != 0 {
		t.Fatalf("%d notices recorded although no one got one, want the claim taken back", n)
	}
	runCycle(t, w)
	if n := notices(); n != 1 {
		t.Errorf("%d notices after the mail came back, want 1", n)
	}

	// Past the expiry, even on its day, there is nothing left to warn of.
	h.advance(14*24*time.Hour + time.Hour)
	runCycle(t, w)
	if n := notices(); n != 1 {
		t.Errorf("%d notices once the key had expired, want still 1", n)
	}
}
