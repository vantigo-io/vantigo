package invoices_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/invoices/accesspoint/storecovetest"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

const (
	accessPointPath       = "/api/v1/invoices/settings/access-point"
	accessPointVerifyPath = "/api/v1/invoices/settings/access-point/verify"
	// accessPointPurpose is the secrets box purpose the key is sealed under.
	accessPointPurpose = "invoices/access-point-credential"
)

// accessPointJSON is the access-point answer as a client reads it.
type accessPointJSON struct {
	Provider       string     `json:"provider"`
	LegalEntityID  int64      `json:"legalEntityId"`
	HasCredentials bool       `json:"hasCredentials"`
	RejectedAt     *time.Time `json:"rejectedAt"`
}

// accessPointBody is a PUT body naming the test Storecove's legal entity;
// key nil leaves apiKey out.
func accessPointBody(key *string) map[string]any {
	body := map[string]any{"provider": "storecove", "legalEntityId": storecovetest.LegalEntityID}
	if key != nil {
		body["apiKey"] = *key
	}
	return body
}

// putAccessPoint stores the credentials as an invoices:manage holder.
func putAccessPoint(t *testing.T, h *harness, body map[string]any) accessPointJSON {
	t.Helper()
	res := h.SignIn(t, "invoices:access", "invoices:manage").Do(http.MethodPut, accessPointPath, body)
	if res.Status != http.StatusOK {
		t.Fatalf("PUT %s = %d %s, want 200", accessPointPath, res.Status, res.Body)
	}
	var out accessPointJSON
	res.JSON(&out)
	return out
}

// storedCredentials is the credentials row as stored.
func storedCredentials(t *testing.T, h *harness) store.InvoicesAccessPointCredential {
	t.Helper()
	row, err := store.New(h.Pool()).GetAccessPointCredentials(context.Background())
	if err != nil {
		t.Fatalf("read the credentials row: %v", err)
	}
	return row
}

// openedKey is the stored key, opened as the module opens it.
func openedKey(t *testing.T, h *harness) string {
	t.Helper()
	key, err := h.Deps().Secrets.OpenString(accessPointPurpose, storedCredentials(t, h).SecretCiphertext)
	if err != nil {
		t.Fatalf("open the stored key: %v", err)
	}
	return key
}

func verify(t *testing.T, h *harness) string {
	t.Helper()
	res := h.SignIn(t, "invoices:access", "invoices:manage").Do(http.MethodPost, accessPointVerifyPath, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("POST %s = %d %s, want 200", accessPointVerifyPath, res.Status, res.Body)
	}
	var out struct {
		Result string `json:"result"`
	}
	res.JSON(&out)
	return out.Result
}

// The key goes in and never comes out: not in the PUT's answer, not in the
// settings or meta, not in the row's clear columns — only sealed, under the
// module's own purpose.
func TestAccessPoint_TheKeyIsNeverReturned(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	key := storecovetest.APIKey

	res := h.SignIn(t, "invoices:access", "invoices:manage").Do(http.MethodPut, accessPointPath, accessPointBody(&key))
	if res.Status != http.StatusOK {
		t.Fatalf("PUT = %d %s, want 200", res.Status, res.Body)
	}
	var got accessPointJSON
	res.JSON(&got)
	want := accessPointJSON{Provider: "storecove", LegalEntityID: storecovetest.LegalEntityID, HasCredentials: true}
	if got != want {
		t.Errorf("answer = %+v, want %+v", got, want)
	}
	reader := h.SignIn(t, "invoices:access", "invoices:manage")
	for name, body := range map[string][]byte{
		"the PUT's answer": res.Body,
		"GET /settings":    reader.Do(http.MethodGet, settingsPath, nil).Body,
		"GET /meta":        reader.Do(http.MethodGet, metaPath, nil).Body,
	} {
		if strings.Contains(string(body), key) || strings.Contains(string(body), "apiKey") {
			t.Errorf("%s carries the key: %s", name, body)
		}
	}
	row := storedCredentials(t, h)
	if strings.Contains(row.SecretCiphertext, key) || strings.Contains(row.SettingsJson, key) {
		t.Errorf("the row holds the key in clear: %+v", row)
	}
	if row.SettingsJson != `{"legalEntityId":4711}` || row.Provider != "storecove" {
		t.Errorf("row = provider %q settings %s, want storecove and the legal entity", row.Provider, row.SettingsJson)
	}
	if opened := openedKey(t, h); opened != key {
		t.Errorf("the sealed key opens to %q, want %q", opened, key)
	}
	if _, err := h.Deps().Secrets.OpenString("communications/channel-smtp-credential", row.SecretCiphertext); err == nil {
		t.Error("the key opens under another purpose; want it sealed under invoices/access-point-credential only")
	}
}

// An omitted key keeps the stored one, sealed again in the PUT's own
// transaction; the first PUT must carry one, and a blank one is never
// stored. The provider and the legal entity are checked.
func TestAccessPoint_TheKeyIsKeptWhenOmitted(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager := h.SignIn(t, "invoices:access", "invoices:manage")

	for name, c := range map[string]struct {
		body  map[string]any
		field string
	}{
		"no key yet":           {accessPointBody(nil), "apiKey"},
		"a null key, none yet": {map[string]any{"provider": "storecove", "legalEntityId": 4711, "apiKey": nil}, "apiKey"},
		"a blank key":          {accessPointBody(ptr("   ")), "apiKey"},
		"another provider":     {map[string]any{"provider": "qvalia", "legalEntityId": 4711, "apiKey": "k"}, "provider"},
		"no legal entity":      {map[string]any{"provider": "storecove", "legalEntityId": 0, "apiKey": "k"}, "legalEntityId"},
		"a key too long":       {accessPointBody(ptr(strings.Repeat("k", 1001))), "apiKey"},
	} {
		res := manager.Do(http.MethodPut, accessPointPath, c.body)
		if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors[c.field]) == 0 {
			t.Errorf("%s: PUT = %d %s, want 400 on %s", name, res.Status, res.Body, c.field)
		}
	}
	if h.Count(t, `SELECT count(*) FROM invoices.access_point_credentials`) != 0 {
		t.Fatal("a refused PUT stored a row")
	}

	key := storecovetest.APIKey
	putAccessPoint(t, h, accessPointBody(ptr("  "+key+"  ")))
	first := storedCredentials(t, h)
	if openedKey(t, h) != key {
		t.Fatalf("stored %q, want the key trimmed", openedKey(t, h))
	}

	got := putAccessPoint(t, h, map[string]any{"provider": "storecove", "legalEntityId": 4712})
	if !got.HasCredentials || got.LegalEntityID != 4712 {
		t.Errorf("answer = %+v, want hasCredentials and the new legal entity", got)
	}
	kept := storedCredentials(t, h)
	if kept.SecretCiphertext == first.SecretCiphertext {
		t.Error("the kept key was not sealed again")
	}
	if openedKey(t, h) != key {
		t.Errorf("the kept key opens to %q, want %q", openedKey(t, h), key)
	}
	putAccessPoint(t, h, map[string]any{"provider": "storecove", "legalEntityId": storecovetest.LegalEntityID, "apiKey": nil})
	if verify(t, h) != "ok" {
		t.Fatal("verify with the kept key is not ok")
	}
}

// plantTransmission inserts a transmission of an issued document at status,
// through Task 2's queries and an UPDATE the trigger allows.
func plantTransmission(t *testing.T, h *harness, status string) int64 {
	t.Helper()
	ctx := context.Background()
	now := h.Now()
	row, err := store.New(h.Pool()).InsertTransmission(ctx, store.InsertTransmissionParams{
		InvoiceID: issuedAcme(t, h).ID, Provider: "storecove", IdempotencyKey: uuid.New(),
		SenderParticipant: "0192:974760673", ReceiverParticipant: "0192:923609016",
		DocumentType: "urn:oasis:names:specification:ubl:schema:xsd:Invoice-2::Invoice", ProcessID: "urn:fdc:peppol.eu:2017:poacc:billing:01:1.0",
		UblObjectKey: "documents/1/1-1001.xml", UblSha256: strings.Repeat("a", 64), PdfSha256: strings.Repeat("b", 64),
		Now: now, LookupRegistered: true, LookupCanReceive: true, LookupAt: now, CreatedByUserID: uuid.New(),
	})
	if err != nil {
		t.Fatalf("insert a transmission: %v", err)
	}
	if status != "queued" {
		h.Exec(t, `UPDATE invoices.transmissions SET status = $2::varchar, submitted_at = $3,
			delivered_at = CASE WHEN $2::varchar = 'delivered' THEN $3::timestamptz END WHERE id = $1`,
			row.ID, status, now)
	}
	return row.ID
}

// While a transmission is queued, submitted or unconfirmed the credentials
// still serve it: removing them is refused. Replacing the key is not — a
// refused key must be replaceable while documents wait for it — and once
// nothing is in flight they can go.
func TestAccessPoint_RefusedWhileTransmissionsAreActive(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	key := storecovetest.APIKey
	putAccessPoint(t, h, accessPointBody(&key))
	manager := h.SignIn(t, "invoices:access", "invoices:manage")

	for _, status := range []string{"queued", "submitted", "unconfirmed"} {
		id := plantTransmission(t, h, status)
		res := manager.Do(http.MethodDelete, accessPointPath, nil)
		if res.Status != http.StatusConflict || problemOf(t, res).Code != "transmissions_active" {
			t.Errorf("DELETE with a %s transmission = %d %s, want 409 transmissions_active", status, res.Status, res.Body)
		}
		h.Exec(t, `UPDATE invoices.transmissions SET status = 'failed', failed_at = $2 WHERE id = $1`, id, h.Now())
	}
	if h.Count(t, `SELECT count(*) FROM invoices.access_point_credentials`) != 1 {
		t.Fatal("a refused DELETE removed the credentials")
	}

	plantTransmission(t, h, "queued")
	putAccessPoint(t, h, accessPointBody(ptr("a-new-key")))
	if openedKey(t, h) != "a-new-key" {
		t.Errorf("a key rotation while a transmission is queued stored %q", openedKey(t, h))
	}
	h.Exec(t, `UPDATE invoices.transmissions SET status = 'cancelled', cancelled_at = $1 WHERE status = 'queued'`, h.Now())

	plantTransmission(t, h, "delivered")
	if res := manager.Do(http.MethodDelete, accessPointPath, nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE with nothing in flight = %d %s, want 204", res.Status, res.Body)
	}
	if h.Count(t, `SELECT count(*) FROM invoices.access_point_credentials`) != 0 {
		t.Error("the credentials are still stored after the DELETE")
	}
	if res := manager.Do(http.MethodDelete, accessPointPath, nil); res.Status != http.StatusNoContent {
		t.Errorf("a second DELETE = %d, want 204", res.Status)
	}
}

// Verify asks the provider with the stored key, outside any lock, and
// answers ok, unauthorized or unreachable. ok clears a refusal; unauthorized
// records one, which meta reports. Without credentials there is nothing to
// verify.
func TestAccessPoint_Verify(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, userID := h.SignInUser(t, "invoices:access", "invoices:manage")

	if res := manager.Do(http.MethodPost, accessPointVerifyPath, nil); res.Status != http.StatusConflict || problemOf(t, res).Code != "ehf_unavailable" {
		t.Fatalf("verify without credentials = %d %s, want 409 ehf_unavailable", res.Status, res.Body)
	}

	key := storecovetest.APIKey
	putAccessPoint(t, h, accessPointBody(&key))
	if err := store.New(h.Pool()).MarkAccessPointRejected(context.Background(), h.Now()); err != nil {
		t.Fatal(err)
	}
	res := manager.Do(http.MethodPost, accessPointVerifyPath, nil)
	var answer struct {
		Result string `json:"result"`
	}
	res.JSON(&answer)
	if res.Status != http.StatusOK || answer.Result != "ok" {
		t.Fatalf("verify = %d %s, want 200 ok", res.Status, res.Body)
	}
	if storedCredentials(t, h).RejectedAt != nil {
		t.Error("an ok verify left the key flagged as rejected")
	}

	h.storecove.Fail(storecovetest.Unauthorized)
	if got := verify(t, h); got != "unauthorized" {
		t.Errorf("verify against a 401 = %q, want unauthorized", got)
	}
	if meta := getMeta(t, h, "invoices:access"); !meta.AccessPointRejected {
		t.Error("meta does not report the refused key")
	}
	h.storecove.Fail(storecovetest.Forbidden)
	if got := verify(t, h); got != "unauthorized" {
		t.Errorf("verify against a 403 = %q, want unauthorized", got)
	}
	for _, mode := range []storecovetest.Mode{storecovetest.ServerError, storecovetest.Throttled} {
		h.storecove.Fail(mode)
		if got := verify(t, h); got != "unreachable" {
			t.Errorf("verify against %s = %q, want unreachable", mode, got)
		}
	}
	h.storecove.Fail(storecovetest.Normal)
	putAccessPoint(t, h, map[string]any{"provider": "storecove", "legalEntityId": storecovetest.LegalEntityID + 1})
	if got := verify(t, h); got != "unreachable" {
		t.Errorf("verify of a legal entity the key does not reach = %q, want unreachable", got)
	}

	var calls []contractCall
	for _, c := range contractCalls.by(userID) {
		if strings.HasPrefix(c.method, "AccessPoint.") {
			calls = append(calls, c)
		}
	}
	if len(calls) != 1 || calls[0].method != "AccessPoint.Verify" || calls[0].locked {
		t.Errorf("this user's access-point calls = %+v, want one AccessPoint.Verify outside any lock", calls)
	}
}

// All three operations are invoices:manage's.
func TestAccessPoint_NeedsManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	key := storecovetest.APIKey
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
			{http.MethodPut, accessPointPath, accessPointBody(&key)},
			{http.MethodDelete, accessPointPath, nil},
			{http.MethodPost, accessPointVerifyPath, nil},
		} {
			if res := c.Do(req.method, req.path, req.body); res.Status != http.StatusForbidden {
				t.Errorf("%v: %s %s = %d, want 403", permissions, req.method, req.path, res.Status)
			}
		}
	}
	if h.Count(t, `SELECT count(*) FROM invoices.access_point_credentials`) != 0 {
		t.Error("a refused PUT stored a row")
	}
}

// A stored key the secrets box cannot open — APP_SECRET changed, or the row
// was altered — is a 503 wherever it would have to be opened, and flags the
// credentials as rejected; a new PUT with a key clears the flag.
func TestAccessPoint_AFailedOpenIsA503AndFlags(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	key := storecovetest.APIKey
	putAccessPoint(t, h, accessPointBody(&key))
	h.Exec(t, `UPDATE invoices.access_point_credentials SET secret_ciphertext = 'not-a-sealed-key'`)
	manager := h.SignIn(t, "invoices:access", "invoices:manage")

	res := manager.Do(http.MethodPost, accessPointVerifyPath, nil)
	if res.Status != http.StatusServiceUnavailable || problemOf(t, res).Code != "ehf_unavailable" {
		t.Fatalf("verify with an unreadable key = %d %s, want 503 ehf_unavailable", res.Status, res.Body)
	}
	if n := h.storecove.Requests(); n != 0 {
		t.Errorf("Storecove saw %d requests, want none — nothing is sent without a key", n)
	}
	if meta := getMeta(t, h, "invoices:access"); !meta.AccessPointRejected {
		t.Error("meta does not report the unreadable key")
	}
	if !strings.Contains(h.Logs(), `"level":"ERROR"`) || !strings.Contains(h.Logs(), "access point key cannot be opened") {
		t.Errorf("no error was logged for the unreadable key:\n%s", h.Logs())
	}

	h.Exec(t, `UPDATE invoices.access_point_credentials SET rejected_at = NULL`)
	res = manager.Do(http.MethodPut, accessPointPath, accessPointBody(nil))
	if res.Status != http.StatusServiceUnavailable || problemOf(t, res).Code != "ehf_unavailable" {
		t.Fatalf("a PUT keeping an unreadable key = %d %s, want 503 ehf_unavailable", res.Status, res.Body)
	}
	row := storedCredentials(t, h)
	if row.RejectedAt == nil || row.SecretCiphertext != "not-a-sealed-key" {
		t.Errorf("after the refused PUT: rejected %v, ciphertext %q; want flagged and untouched", row.RejectedAt, row.SecretCiphertext)
	}

	got := putAccessPoint(t, h, accessPointBody(&key))
	if got.RejectedAt != nil {
		t.Errorf("a new PUT answers rejectedAt %v, want none", got.RejectedAt)
	}
	if meta := getMeta(t, h, "invoices:access"); meta.AccessPointRejected {
		t.Error("meta still reports a rejected key after a new PUT")
	}
	if verify(t, h) != "ok" {
		t.Error("verify after the new PUT is not ok")
	}
}
