package google_test

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/google/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The keys (credentials.go): the file's rules, what is kept, rotation,
// client_mismatch and the revoke.

// editedKeyFile is a's key file with edit applied to its fields.
func editedKeyFile(t *testing.T, file string, edit func(m map[string]any)) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(file), &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	b, _ := json.Marshal(m)
	return string(b)
}

func TestCredentials_TheKeyFilesRules(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	o, _ := owner(t, h)
	first, _, _ := accounts()
	file := first.KeyFile()
	pkcs1 := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(first.Key)}))

	for name, text := range map[string]string{
		"empty":             "",
		"not JSON":          "-----BEGIN PRIVATE KEY-----",
		"too large":         file + strings.Repeat(" ", 8<<10),
		"not a service":     editedKeyFile(t, file, func(m map[string]any) { m["type"] = "authorized_user" }),
		"another token_uri": editedKeyFile(t, file, func(m map[string]any) { m["token_uri"] = "https://evil.example/token" }),
		"a PKCS #1 key":     editedKeyFile(t, file, func(m map[string]any) { m["private_key"] = pkcs1 }),
		"no client id":      editedKeyFile(t, file, func(m map[string]any) { delete(m, "client_id") }),
		"no address":        editedKeyFile(t, file, func(m map[string]any) { m["client_email"] = "vantigo" }),
	} {
		r := o.Do(http.MethodPost, credentialsPath, map[string]any{"json": text})
		if p := problemOf(t, r); r.Status != http.StatusBadRequest || p.Errors == nil || len((*p.Errors)["json"]) == 0 {
			t.Errorf("%s: %d %s, want a field error on json", name, r.Status, r.Body)
		}
		if strings.Contains(string(r.Body), "PRIVATE KEY") {
			t.Errorf("%s: the refusal repeats the key", name)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM google.credentials`); n != 0 {
		t.Errorf("refused files stored %d credentials", n)
	}
}

func TestCredentials_AnUploadKeepsTheFourFieldsAndSealsTheKey(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	o, ownerID := owner(t, h)
	first, _, _ := accounts()

	r := o.Do(http.MethodPost, credentialsPath, map[string]any{"json": first.KeyFile()})
	if r.Status != http.StatusCreated {
		t.Fatalf("upload: %d %s", r.Status, r.Body)
	}
	if strings.Contains(string(r.Body), "PRIVATE KEY") || strings.Contains(string(r.Body), "private_key\"") {
		t.Errorf("the answer carries the key: %s", r.Body)
	}
	var cred gen.GoogleCredential
	r.JSON(&cred)
	if cred.Kind != "service_account_key" || !cred.InUse || cred.ClientEmail == nil || *cred.ClientEmail != saEmail ||
		cred.ClientId == nil || *cred.ClientId != saClientID || cred.PrivateKeyId == nil || *cred.PrivateKeyId != first.KeyID ||
		cred.ProjectId == nil || *cred.ProjectId != saProject || cred.Id != 1001 {
		t.Errorf("the key: %+v", cred)
	}
	sealed := modtest.One[[]byte](t, h.Harness, `SELECT sealed FROM google.credentials WHERE id = $1`, cred.Id)
	if strings.Contains(string(sealed), "PRIVATE KEY") {
		t.Errorf("the key is stored in the clear")
	}
	conn := getConnection(t, o)
	if conn.DelegationClientId == nil || *conn.DelegationClientId != saClientID || conn.Status != gen.GoogleConnectionStatusUnconfigured {
		t.Errorf("the connection after the key alone: %+v", conn)
	}
	codes := modtest.One[[]string](t, h.Harness, `SELECT codes FROM google.events WHERE action = 'google.credential.added' AND actor_user_id = $1`, ownerID)
	if !slices.Equal(codes, []string{"credential:1001", "kind:service_account_key", "key_id:" + first.KeyID, "client_id:" + saClientID}) {
		t.Errorf("the event's codes = %v", codes)
	}
}

func TestCredentials_AnotherServiceAccountWhileOneIsLive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	o, _ := owner(t, h)
	first, second, other := accounts()
	key := uploadKey(t, o, first)
	uploadKey(t, o, second) // another key of the same account: rotation

	r := o.Do(http.MethodPost, credentialsPath, map[string]any{"json": other.KeyFile()})
	if r.Status != http.StatusUnprocessableEntity || codeOf(t, r) != "client_mismatch" {
		t.Fatalf("another account's key: %d %s, want 422 client_mismatch", r.Status, r.Body)
	}

	// Change the service account: revoke every key of the current one.
	for _, c := range getConnection(t, o).Credentials {
		r := o.Do(http.MethodDelete, credentialsPath+"/"+itoa(c.Id), nil)
		var revoked gen.GoogleCredential
		r.JSON(&revoked)
		if r.Status != http.StatusOK || revoked.RevokedAt == nil || revoked.InUse {
			t.Fatalf("revoke %d: %d %s", c.Id, r.Status, r.Body)
		}
	}
	if r := o.Do(http.MethodDelete, credentialsPath+"/"+itoa(key.Id), nil); r.Status != http.StatusOK {
		t.Errorf("a revoked key revoked again: %d, want 200 as it is", r.Status)
	}
	if r := o.Do(http.MethodDelete, credentialsPath+"/999", nil); r.Status != http.StatusNotFound {
		t.Errorf("an unknown credential: %d, want 404", r.Status)
	}
	uploadKey(t, o, other)
	conn := getConnection(t, o)
	if conn.DelegationClientId == nil || *conn.DelegationClientId != other.ClientID || len(conn.Credentials) != 3 {
		t.Errorf("after changing account: %+v", conn)
	}
}

func TestCredentials_RotationFallsBackToAnOlderKeyWhenTheNewestIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.g.Delegate(saClientID, everyScope...)
	o, _ := configured(t, h) // the first key
	_, second, _ := accounts()
	newer := uploadKey(t, o, second)
	h.g.DeleteKey(second) // deleted in the Cloud console: Google refuses it

	if result := testConnection(t, o); result.Status != gen.GoogleConnectionTestStatusConnected {
		t.Fatalf("the test with a refused newest key: %+v, want connected through the older", result)
	}
	conn := getConnection(t, o)
	for _, c := range conn.Credentials {
		switch {
		case c.Id == newer.Id && (c.LastFailedAt == nil || c.InUse):
			t.Errorf("the refused key: %+v, want failed and not in use", c)
		case c.Id != newer.Id && (!c.InUse || c.LastUsedAt == nil):
			t.Errorf("the older key: %+v, want in use", c)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM google.events WHERE action = 'google.credential.refused' AND actor_system = 'system:google' AND subject_id = $1`, saClientID); n == 0 {
		t.Errorf("no event for the refused key")
	}
}
