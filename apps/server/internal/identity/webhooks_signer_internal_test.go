package identity

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

// TestWebhookSigner_ReproducesTheSvixVector is the Standard Webhooks
// symmetric test vector Svix publishes (outgoing webhooks research §2,
// design W13): nothing else is built on the signer until it holds.
func TestWebhookSigner_ReproducesTheSvixVector(t *testing.T) {
	t.Parallel()
	const (
		secret    = "whsec_plJ3nmyCDGBKInavdOK15jsl"
		id        = "msg_loFOjxBNrRLzqYUf"
		timestamp = int64(1731705121)
		body      = `{"event_type":"ping","data":{"success":true}}`
		want      = "v1,rAvfW3dJ/X/qxhsaXPOyyCGmRKsaKWcsNccKXlIktD0="
	)
	key, err := webhookSecretKey(secret)
	if err != nil {
		t.Fatal(err)
	}
	if got := signWebhook(id, timestamp, []byte(body), key); got != want {
		t.Errorf("signature = %s, want %s", got, want)
	}
	if !verifyWebhookSignature(want, id, timestamp, []byte(body), key) {
		t.Error("the vector's own signature does not verify")
	}
}

// TestWebhookSigner_TheReferenceExample is the signed example
// reference/webhooks.md shows, so the page stays true.
func TestWebhookSigner_TheReferenceExample(t *testing.T) {
	t.Parallel()
	key, err := webhookSecretKey("whsec_YW4gZXhhbXBsZSBzZWNyZXQgZm9yIHRoZSBkb2NzISE=")
	if err != nil {
		t.Fatal(err)
	}
	body := `{"id":"evt_8421","type":"orders.sales_order.confirmed","version":1,"timestamp":"2026-11-18T10:15:00Z","sequence":8421,"data":{"resource":{"type":"sales_order","id":"42","href":"/api/v1/orders/orders/42"}}}`
	got := signWebhook("msg_2f1c0c5e8a7b4d6e9f0a1b2c3d4e5f60", 1794996903, []byte(body), key)
	if want := "v1,PC7oLLVZMjENZBAqlHjINCU8CI1QCvSh4RHm2eDlY84="; got != want {
		t.Errorf("signature = %s, want %s", got, want)
	}
}

func TestWebhookSigner_HeaderCarriesOneSignaturePerSecret(t *testing.T) {
	t.Parallel()
	current, previous := newWebhookSecret(), newWebhookSecret()
	ck, err := webhookSecretKey(current)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := webhookSecretKey(previous)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"id":"evt_1"}`)
	header := webhookSignatureHeader("msg_1", 1700000000, body, ck, pk)
	parts := strings.Split(header, " ")
	if len(parts) != 2 {
		t.Fatalf("header %q, want two space-separated signatures", header)
	}
	if !verifyWebhookSignature(parts[0], "msg_1", 1700000000, body, ck) || !verifyWebhookSignature(parts[1], "msg_1", 1700000000, body, pk) {
		t.Errorf("header %q: each signature verifies under its own secret", header)
	}
	if verifyWebhookSignature(parts[0], "msg_1", 1700000001, body, ck) {
		t.Error("a signature verified under another timestamp")
	}
	if verifyWebhookSignature(parts[0], "msg_1", 1700000000, []byte(`{"id":"evt_2"}`), ck) {
		t.Error("a signature verified over another body")
	}
	if got := webhookSignatureHeader("msg_1", 1700000000, body, ck); strings.Contains(got, " ") || got != parts[0] {
		t.Errorf("one secret's header = %q, want %q alone", got, parts[0])
	}
}

func TestWebhookSigner_SecretsAreStandardWebhooksSecrets(t *testing.T) {
	t.Parallel()
	a, b := newWebhookSecret(), newWebhookSecret()
	if a == b {
		t.Fatal("two secrets are equal")
	}
	raw, ok := strings.CutPrefix(a, "whsec_")
	if !ok {
		t.Fatalf("secret %q has no whsec_ prefix", a)
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(key) != webhookSecretBytes {
		t.Fatalf("secret %q decodes to %d bytes (%v), want %d", a, len(key), err, webhookSecretBytes)
	}
	// The key is the decoded bytes, as a receiver's library takes them.
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("msg_1.1.{}"))
	want := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	got, err := webhookSecretKey(a)
	if err != nil {
		t.Fatal(err)
	}
	if s := signWebhook("msg_1", 1, []byte("{}"), got); s != want {
		t.Errorf("signature %s, want %s", s, want)
	}
	for _, bad := range []string{"", "plJ3nmyCDGBKInavdOK15jsl", "whsec_", "whsec_!!!"} {
		if _, err := webhookSecretKey(bad); err == nil {
			t.Errorf("secret %q was accepted", bad)
		}
	}
}

func TestWebhookSigner_MessageIDIsTheDeliveryWithoutDashes(t *testing.T) {
	t.Parallel()
	id := webhookMessageID([16]byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef})
	if id != "msg_0123456789abcdef0123456789abcdef" {
		t.Errorf("message id = %s", id)
	}
}
