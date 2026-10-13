package invoices_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/payments/vipps/vippstest"
)

const (
	vippsSettingsPath = "/api/v1/invoices/settings/vipps"
	vippsVerifyPath   = "/api/v1/invoices/settings/vipps/verify"
	// vippsPurpose is the secrets box purpose the keys are sealed under.
	vippsPurpose = "invoices/payment-provider-credential"
)

// vippsJSON is the Vipps settings answer as a client reads it.
type vippsJSON struct {
	Msn            *string    `json:"msn"`
	HasCredentials bool       `json:"hasCredentials"`
	RejectedAt     *time.Time `json:"rejectedAt"`
}

// vippsKeys is a PUT body for msn; each key left out when "".
func vippsKeys(msn, clientID, clientSecret, subscriptionKey string) map[string]any {
	body := map[string]any{"msn": msn}
	for field, v := range map[string]string{"clientId": clientID, "clientSecret": clientSecret, "subscriptionKey": subscriptionKey} {
		if v != "" {
			body[field] = v
		}
	}
	return body
}

// vippstestKeys is the body vippstest accepts.
func vippstestKeys() map[string]any {
	return vippsKeys(vippstest.MSN, vippstest.ClientID, vippstest.ClientSecret, vippstest.SubscriptionKey)
}

func vippsManager(t *testing.T, h *harness) *modtest.Client {
	t.Helper()
	return h.SignIn(t, "invoices:access", "invoices:manage")
}

// putVipps stores the credentials, failing unless the PUT answered 200.
func putVipps(t *testing.T, h *harness, body map[string]any) vippsJSON {
	t.Helper()
	res := vippsManager(t, h).Do(http.MethodPut, vippsSettingsPath, body)
	if res.Status != http.StatusOK {
		t.Fatalf("PUT %s = %d %s, want 200", vippsSettingsPath, res.Status, res.Body)
	}
	var out vippsJSON
	res.JSON(&out)
	return out
}

// storedVipps is the credentials row as stored.
func storedVipps(t *testing.T, h *harness) store.InvoicesPaymentProviderCredential {
	t.Helper()
	row, err := store.New(h.Pool()).GetPaymentCredentials(context.Background())
	if err != nil {
		t.Fatalf("read the payment credentials row: %v", err)
	}
	return row
}

// openedVippsKeys is the stored keys, opened as the module opens them.
func openedVippsKeys(t *testing.T, h *harness) string {
	t.Helper()
	keys, err := h.Deps().Secrets.OpenString(vippsPurpose, storedVipps(t, h).SecretCiphertext)
	if err != nil {
		t.Fatalf("open the stored keys: %v", err)
	}
	return keys
}

// verifyVipps runs the verify as a new manager and answers its result,
// failing unless 200.
func verifyVipps(t *testing.T, h *harness) string {
	t.Helper()
	return verifyVippsAs(t, vippsManager(t, h))
}

func verifyVippsAs(t *testing.T, c *modtest.Client) string {
	t.Helper()
	res := c.Do(http.MethodPost, vippsVerifyPath, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("POST %s = %d %s, want 200", vippsVerifyPath, res.Status, res.Body)
	}
	var out struct {
		Result string `json:"result"`
	}
	res.JSON(&out)
	return out.Result
}

// liveAttemptStates is every way an attempt is live to the credentials
// (D14): its state, or captured with a release pending.
var liveAttemptStates = []string{"creating", "created", "authorized", "capturing", "capture_unknown", "cancelling", "release_pending"}

// plantAttempt writes an on-site attempt of a fresh issued invoice in state
// ("release_pending" is a captured attempt whose release is pending).
func plantAttempt(t *testing.T, h *harness, state string) {
	t.Helper()
	invoice := issuedAcme(t, h).ID
	columns, values := "", ""
	switch state {
	case "capturing", "capture_unknown":
		columns, values = ", authorized_amount, reserved_amount", ", 100, 100"
	case "release_pending":
		state, columns, values = "captured", ", authorized_amount, release_pending", ", 100, true"
	}
	h.Exec(t, `INSERT INTO invoices.payment_attempts (invoice_id, provider, msn, flow, amount, cancel_key, state,
		created_at, expires_at`+columns+`)
		VALUES ($1, 'vipps', '123456', 'on_site', 100, gen_random_uuid(), $2, now(), now() + interval '10 minutes'`+values+`)`,
		invoice, state)
}

// No key ever comes out: not in the PUT's answer, the GET, the verify or
// meta, not in the row's clear columns — only sealed, under the module's own
// purpose, the three together.
func TestVippsSettings_NeverAnswersASecret(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager := vippsManager(t, h)

	res := manager.Do(http.MethodPut, vippsSettingsPath, vippstestKeys())
	if res.Status != http.StatusOK {
		t.Fatalf("PUT = %d %s, want 200", res.Status, res.Body)
	}
	var got vippsJSON
	res.JSON(&got)
	if !got.HasCredentials || got.Msn == nil || *got.Msn != vippstest.MSN || got.RejectedAt != nil {
		t.Errorf("answer = %+v, want the MSN and hasCredentials", got)
	}
	bodies := map[string][]byte{
		"the PUT's answer": res.Body,
		"GET":              manager.Do(http.MethodGet, vippsSettingsPath, nil).Body,
		"the verify":       manager.Do(http.MethodPost, vippsVerifyPath, nil).Body,
		"GET /meta":        manager.Do(http.MethodGet, metaPath, nil).Body,
		"GET payments":     manager.Do(http.MethodGet, paymentSettingsPath, nil).Body,
	}
	for name, body := range bodies {
		for _, secret := range []string{vippstest.ClientID, vippstest.ClientSecret, vippstest.SubscriptionKey, "clientId", "clientSecret", "subscriptionKey"} {
			if strings.Contains(string(body), secret) {
				t.Errorf("%s carries %q: %s", name, secret, body)
			}
		}
	}
	row := storedVipps(t, h)
	for _, secret := range []string{vippstest.ClientID, vippstest.ClientSecret, vippstest.SubscriptionKey} {
		if strings.Contains(row.SecretCiphertext, secret) || strings.Contains(row.SettingsJson, secret) {
			t.Errorf("the row holds %q in clear", secret)
		}
	}
	if row.Provider != "vipps" || row.SettingsJson != `{"msn":"123456"}` {
		t.Errorf("row = provider %q settings %s, want vipps and the MSN", row.Provider, row.SettingsJson)
	}
	want := fmt.Sprintf(`{"clientId":%q,"clientSecret":%q,"subscriptionKey":%q}`, vippstest.ClientID, vippstest.ClientSecret, vippstest.SubscriptionKey)
	if opened := openedVippsKeys(t, h); opened != want {
		t.Errorf("the sealed keys open to %s, want %s", opened, want)
	}
	if _, err := h.Deps().Secrets.OpenString("invoices/access-point-credential", row.SecretCiphertext); err == nil {
		t.Error("the keys open under another purpose; want them sealed under invoices/payment-provider-credential only")
	}
}

// The first PUT needs all three keys; a blank key and a bad MSN are 400s on
// their field; nothing is stored by a refused PUT.
func TestVippsSettings_FirstPutNeedsAllThree(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager := vippsManager(t, h)
	for name, c := range map[string]struct {
		body  map[string]any
		field string
	}{
		"no clientId":        {vippsKeys(vippstest.MSN, "", vippstest.ClientSecret, vippstest.SubscriptionKey), "clientId"},
		"no clientSecret":    {vippsKeys(vippstest.MSN, vippstest.ClientID, "", vippstest.SubscriptionKey), "clientSecret"},
		"no subscriptionKey": {vippsKeys(vippstest.MSN, vippstest.ClientID, vippstest.ClientSecret, ""), "subscriptionKey"},
		"a blank clientId":   {vippsKeys(vippstest.MSN, "   ", vippstest.ClientSecret, vippstest.SubscriptionKey), "clientId"},
		"letters in the MSN": {vippsKeys("12a456", vippstest.ClientID, vippstest.ClientSecret, vippstest.SubscriptionKey), "msn"},
		"an empty MSN":       {vippsKeys("", vippstest.ClientID, vippstest.ClientSecret, vippstest.SubscriptionKey), "msn"},
		"eleven digits":      {vippsKeys("12345678901", vippstest.ClientID, vippstest.ClientSecret, vippstest.SubscriptionKey), "msn"},
		"a key too long": {vippsKeys(vippstest.MSN, vippstest.ClientID, strings.Repeat("s", 1001), vippstest.SubscriptionKey),
			"clientSecret"},
	} {
		res := manager.Do(http.MethodPut, vippsSettingsPath, c.body)
		if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors[c.field]) == 0 {
			t.Errorf("%s: PUT = %d %s, want 400 on %s", name, res.Status, res.Body, c.field)
		}
	}
	if h.Count(t, `SELECT count(*) FROM invoices.payment_provider_credentials`) != 0 {
		t.Error("a refused PUT stored a row")
	}
	putVipps(t, h, vippstestKeys())
}

// An omitted key keeps the stored one, opened and sealed again; a given one
// replaces only itself.
func TestVippsSettings_OmittedSecretKept(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	putVipps(t, h, vippsKeys(vippstest.MSN, "old-id", "old-secret", "old-sub"))
	before := storedVipps(t, h).SecretCiphertext

	got := putVipps(t, h, vippsKeys(vippstest.MSN, "", "new-secret", ""))
	if !got.HasCredentials {
		t.Errorf("answer = %+v, want hasCredentials", got)
	}
	if want := `{"clientId":"old-id","clientSecret":"new-secret","subscriptionKey":"old-sub"}`; openedVippsKeys(t, h) != want {
		t.Errorf("keys = %s, want %s", openedVippsKeys(t, h), want)
	}
	putVipps(t, h, vippsKeys("654321", "", "", ""))
	if want := `{"clientId":"old-id","clientSecret":"new-secret","subscriptionKey":"old-sub"}`; openedVippsKeys(t, h) != want {
		t.Errorf("keys after an MSN-only PUT = %s, want %s", openedVippsKeys(t, h), want)
	}
	if row := storedVipps(t, h); row.SecretCiphertext == before || row.SettingsJson != `{"msn":"654321"}` {
		t.Errorf("row = %+v, want sealed anew with the new MSN", row)
	}
}

// While an attempt is live — each live state planted — a change of the MSN,
// the client id or the subscription key is refused, counted under the row's
// lock; a new client secret alone is a rotation and allowed.
func TestVippsSettings_SalesUnitChangeRefusedWhileLive(t *testing.T) {
	t.Parallel()
	for _, state := range liveAttemptStates {
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			h := readyToIssue(t)
			putVipps(t, h, vippstestKeys())
			plantAttempt(t, h, state)
			manager := vippsManager(t, h)
			for name, body := range map[string]map[string]any{
				"the MSN":              vippsKeys("654321", "", "", ""),
				"the client id":        vippsKeys(vippstest.MSN, "another-id", "", ""),
				"the subscription key": vippsKeys(vippstest.MSN, "", "", "another-sub"),
			} {
				res := manager.Do(http.MethodPut, vippsSettingsPath, body)
				if res.Status != http.StatusConflict || problemOf(t, res).Code != "payment_attempts_active" {
					t.Errorf("a change of %s while %s = %d %s, want 409 payment_attempts_active", name, state, res.Status, res.Body)
				}
			}
			// The same keys again, and the same MSN, are no change.
			putVipps(t, h, vippstestKeys())
			putVipps(t, h, vippsKeys(vippstest.MSN, "", "rotated-secret", ""))
			if keys := openedVippsKeys(t, h); !strings.Contains(keys, `"clientSecret":"rotated-secret"`) || !strings.Contains(keys, vippstest.ClientID) {
				t.Errorf("after the rotation the keys are %s", keys)
			}
		})
	}
	// With nothing live, every change is allowed — a finished attempt does
	// not hold the credentials.
	h := readyToIssue(t)
	putVipps(t, h, vippstestKeys())
	plantAttempt(t, h, "cancelled")
	putVipps(t, h, vippsKeys("654321", "another-id", "", "another-sub"))
}

// DELETE is refused while an attempt is live, and is a 204 otherwise — also
// with nothing stored.
func TestVippsSettings_DeleteRefusedWhileLive(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	manager := vippsManager(t, h)
	if res := manager.Do(http.MethodDelete, vippsSettingsPath, nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE with nothing stored = %d %s, want 204", res.Status, res.Body)
	}
	putVipps(t, h, vippstestKeys())
	plantAttempt(t, h, "authorized")
	res := manager.Do(http.MethodDelete, vippsSettingsPath, nil)
	if res.Status != http.StatusConflict || problemOf(t, res).Code != "payment_attempts_active" {
		t.Fatalf("DELETE while live = %d %s, want 409 payment_attempts_active", res.Status, res.Body)
	}
	if h.Count(t, `SELECT count(*) FROM invoices.payment_provider_credentials`) != 1 {
		t.Error("a refused DELETE removed the row")
	}

	other := readyToIssue(t)
	putVipps(t, other, vippstestKeys())
	plantAttempt(t, other, "expired")
	if res := vippsManager(t, other).Do(http.MethodDelete, vippsSettingsPath, nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE with nothing live = %d %s, want 204", res.Status, res.Body)
	}
	var got vippsJSON
	vippsManager(t, other).Do(http.MethodGet, vippsSettingsPath, nil).JSON(&got)
	if got.HasCredentials || got.Msn != nil {
		t.Errorf("GET after DELETE = %+v, want nothing stored", got)
	}
}

// The verify fetches a token and reads the webhooks — the sales unit — and
// answers ok (clearing a refusal), unauthorized (recording one) or
// unreachable; without credentials it is a 409. Every call is one
// payments.Verify outside any lock.
func TestVippsSettings_Verify(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, userID := h.SignInUser(t, "invoices:access", "invoices:manage")

	if res := manager.Do(http.MethodPost, vippsVerifyPath, nil); res.Status != http.StatusConflict || problemOf(t, res).Code != "payments_unavailable" {
		t.Fatalf("verify without credentials = %d %s, want 409 payments_unavailable", res.Status, res.Body)
	}
	putVipps(t, h, vippstestKeys())
	h.Exec(t, `UPDATE invoices.payment_provider_credentials SET rejected_at = now()`)
	if got := verifyVippsAs(t, manager); got != "ok" {
		t.Errorf("verify = %q, want ok", got)
	}
	if storedVipps(t, h).RejectedAt != nil {
		t.Error("an ok verify left the credentials flagged as rejected")
	}

	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		h.vipps.Fail(vippstest.OpToken, vippstest.Failure{Status: status})
		if got := verifyVippsAs(t, manager); got != "unauthorized" {
			t.Errorf("verify against a %d = %q, want unauthorized", status, got)
		}
		if storedVipps(t, h).RejectedAt == nil {
			t.Errorf("a %d did not flag the credentials as rejected", status)
		}
		if meta := paymentMeta(t, h, "invoices:access"); !meta.VippsCredentialsRejected {
			t.Error("meta does not report the refused credentials")
		}
	}
	h.vipps.Clear(vippstest.OpToken)
	for name, f := range map[string]vippstest.Failure{
		"a 503": {Status: http.StatusServiceUnavailable}, "a reset": {Reset: true}, "a 429": {Status: http.StatusTooManyRequests},
	} {
		h.vipps.Fail(vippstest.OpWebhooks, f)
		if got := verifyVippsAs(t, manager); got != "unreachable" {
			t.Errorf("verify against %s = %q, want unreachable", name, got)
		}
	}
	h.vipps.Clear(vippstest.OpWebhooks)

	var calls []string
	for _, c := range contractCalls.by(userID) {
		if strings.HasPrefix(c.method, "payments.") {
			if c.locked {
				t.Errorf("%s was called under a lock", c.method)
			}
			calls = append(calls, c.method)
		}
	}
	if len(calls) != 6 || slices.ContainsFunc(calls, func(m string) bool { return m != "payments.Verify" }) {
		t.Errorf("this user's provider calls = %v, want six payments.Verify", calls)
	}
}

// Stored keys the box cannot open: the verify and a PUT keeping a key are
// 503 payments_unavailable, nothing reaches Vipps, the credentials are
// flagged and the operator is told; a PUT of all three heals it.
func TestVippsSettings_UnopenableSecret(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	putVipps(t, h, vippstestKeys())
	h.Exec(t, `UPDATE invoices.payment_provider_credentials SET secret_ciphertext = 'not-sealed'`)
	manager := vippsManager(t, h)
	before := len(h.vipps.Requests())

	res := manager.Do(http.MethodPost, vippsVerifyPath, nil)
	if res.Status != http.StatusServiceUnavailable || problemOf(t, res).Code != "payments_unavailable" {
		t.Fatalf("verify with unopenable keys = %d %s, want 503 payments_unavailable", res.Status, res.Body)
	}
	if n := len(h.vipps.Requests()) - before; n != 0 {
		t.Errorf("Vipps saw %d requests, want none", n)
	}
	if storedVipps(t, h).RejectedAt == nil {
		t.Error("the unopenable keys were not flagged as rejected")
	}
	if !strings.Contains(h.Logs(), `"level":"ERROR"`) || !strings.Contains(h.Logs(), "Vipps keys cannot be opened") {
		t.Errorf("no error was logged for the unopenable keys:\n%s", h.Logs())
	}

	h.Exec(t, `UPDATE invoices.payment_provider_credentials SET rejected_at = NULL`)
	res = manager.Do(http.MethodPut, vippsSettingsPath, vippsKeys(vippstest.MSN, "", "rotated", ""))
	if res.Status != http.StatusServiceUnavailable || problemOf(t, res).Code != "payments_unavailable" {
		t.Fatalf("a PUT keeping unopenable keys = %d %s, want 503 payments_unavailable", res.Status, res.Body)
	}
	if row := storedVipps(t, h); row.RejectedAt == nil || row.SecretCiphertext != "not-sealed" {
		t.Errorf("after the refused PUT: rejected %v, ciphertext %q; want flagged and untouched", row.RejectedAt, row.SecretCiphertext)
	}
	if got := putVipps(t, h, vippstestKeys()); got.RejectedAt != nil {
		t.Errorf("a PUT of all three answers rejectedAt %v, want none", got.RejectedAt)
	}
	if verifyVipps(t, h) != "ok" {
		t.Error("verify after the new keys is not ok")
	}
}

// All four operations are invoices:manage's.
func TestVippsSettings_Permissions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, permissions := range [][]string{
		{"invoices:access"},
		{"invoices:access", "invoices:create", "invoices:issue", "invoices:payments"},
		{"invoices:manage"},
	} {
		c := h.SignIn(t, permissions...)
		for _, req := range []struct {
			method, path string
			body         any
		}{
			{http.MethodGet, vippsSettingsPath, nil},
			{http.MethodPut, vippsSettingsPath, vippstestKeys()},
			{http.MethodDelete, vippsSettingsPath, nil},
			{http.MethodPost, vippsVerifyPath, nil},
		} {
			if res := c.Do(req.method, req.path, req.body); res.Status != http.StatusForbidden {
				t.Errorf("%v: %s %s = %d, want 403", permissions, req.method, req.path, res.Status)
			}
		}
	}
	if h.Count(t, `SELECT count(*) FROM invoices.payment_provider_credentials`) != 0 {
		t.Error("a refused PUT stored a row")
	}
	if len(h.vipps.Requests()) != 0 {
		t.Error("a refused verify reached Vipps")
	}
}

// TestVippsSettings_NoCallUnderALock: the verify's one provider call is made
// on the pool, never inside a transaction; the PUT and DELETE make none.
func TestVippsSettings_NoCallUnderALock(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, user := h.SignInUser(t, "invoices:access", "invoices:manage")
	for _, req := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPut, vippsSettingsPath, vippstestKeys()},
		{http.MethodPost, vippsVerifyPath, nil},
		{http.MethodPut, vippsSettingsPath, vippsKeys(vippstest.MSN, "", "", "")},
		{http.MethodDelete, vippsSettingsPath, nil},
	} {
		if res := manager.Do(req.method, req.path, req.body); res.Status >= 300 {
			t.Fatalf("%s %s = %d %s", req.method, req.path, res.Status, res.Body)
		}
	}
	var methods []string
	for _, call := range contractCalls.by(user) {
		if call.locked {
			t.Errorf("%s was called under a lock", call.method)
		}
		methods = append(methods, call.method)
	}
	if !slices.Equal(methods, []string{"payments.Verify"}) {
		t.Errorf("the calls out of the module = %v, want one payments.Verify", methods)
	}
}

// TestVippsSettings_LockOrder: a credentials PUT and DELETE lock the
// credentials row and nothing else, and a payment settings PUT its own row
// alone (D18's 4C rows).
func TestVippsSettings_LockOrder(t *testing.T) {
	h := newHarness(t)
	seen := &lockSeen{}
	restore := invoices.SetLockTaken(seen.note)
	defer restore()

	// The first PUT finds no row to lock: it inserts the one row, and a
	// concurrent first PUT meets it on the primary key.
	putVipps(t, h, vippstestKeys())
	if got := seen.take(); len(got) != 0 {
		t.Errorf("the first PUT's locks = %v, want none", got)
	}
	putVipps(t, h, vippsKeys("654321", "", "", ""))
	if got, want := seen.take(), []string{"payment_credentials 1"}; !slices.Equal(got, want) {
		t.Errorf("the second PUT's locks = %v, want %v", got, want)
	}
	putPaymentSettings(t, h, paymentSettingsBody(1, nil))
	if got, want := seen.take(), []string{"payment_settings 1"}; !slices.Equal(got, want) {
		t.Errorf("the payment settings PUT's locks = %v, want %v", got, want)
	}
	if res := vippsManager(t, h).Do(http.MethodDelete, vippsSettingsPath, nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", res.Status, res.Body)
	}
	if got, want := seen.take(), []string{"payment_credentials 1"}; !slices.Equal(got, want) {
		t.Errorf("the DELETE's locks = %v, want %v", got, want)
	}
}

// TestPaymentProvider_SwitchOff (m12): with INVOICES_VIPPS_ENABLED=0 no
// client is made — the verify answers 503 payments_unavailable and Vipps
// records no request — while the stored credentials stay readable and
// writable, and meta offers nothing.
func TestPaymentProvider_SwitchOff(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithEnv("INVOICES_VIPPS_ENABLED", "0"),
		modtest.WithEnv("PUBLIC_BASE_URL", "https://pay.example.com"))
	putVipps(t, h, vippstestKeys())
	enablePayments(t, h)

	res := vippsManager(t, h).Do(http.MethodPost, vippsVerifyPath, nil)
	if res.Status != http.StatusServiceUnavailable || problemOf(t, res).Code != "payments_unavailable" {
		t.Fatalf("verify with the switch off = %d %s, want 503 payments_unavailable", res.Status, res.Body)
	}
	if n := len(h.vipps.Requests()); n != 0 {
		t.Errorf("Vipps saw %d requests with the switch off, want none", n)
	}
	if storedVipps(t, h).RejectedAt != nil {
		t.Error("the switch flagged the credentials as rejected")
	}
	meta := paymentMeta(t, h, "invoices:access", "invoices:issue")
	if meta.PayLinksAvailable || meta.OnSitePaymentsAvailable || meta.Capabilities.CanTakeOnSitePayment {
		t.Errorf("meta with the switch off = %+v, want nothing available", meta)
	}
}
