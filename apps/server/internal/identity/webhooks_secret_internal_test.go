package identity

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/secrets"
)

func testBox(t *testing.T) *secrets.Box {
	t.Helper()
	box, err := secrets.New([]byte("webhook-secret-test-app-secret-0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	return box
}

// TestWebhookSecret_SealedUnderItsOwnPurpose holds W8: a secret is sealed,
// opens back to itself, never stands in clear in its ciphertext, and opens
// under no other purpose nor for another endpoint.
func TestWebhookSecret_SealedUnderItsOwnPurpose(t *testing.T) {
	t.Parallel()
	box := testBox(t)
	secret := newWebhookSecret()
	endpoint := uuid.New()
	sealed, err := sealWebhookSecret(box, endpoint, secret)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte(secret)) || bytes.Contains(sealed, []byte(strings.TrimPrefix(secret, "whsec_"))) {
		t.Error("the ciphertext holds the secret")
	}
	opened, err := openWebhookSecret(box, endpoint, sealed)
	if err != nil || opened != secret {
		t.Errorf("opened %q, %v", opened, err)
	}
	if _, err := box.Open(totpSecretPurpose, sealed); err == nil {
		t.Error("a webhook secret opened under another purpose")
	}
	// Bound to its endpoint (S6): copied into another endpoint's row it opens
	// for neither that endpoint nor any other.
	if _, err := openWebhookSecret(box, uuid.New(), sealed); err == nil {
		t.Error("a webhook secret opened for another endpoint")
	}
	if _, err := openWebhookSecret(box, endpoint, append(sealed[:len(sealed)-1:len(sealed)-1], sealed[len(sealed)-1]^1)); err == nil {
		t.Error("a tampered ciphertext opened")
	} else if strings.Contains(err.Error(), secret) {
		t.Errorf("the error carries the secret: %v", err)
	}
}

// TestWebhookSecret_TheURLDigestIsShortKeyedAndStable holds W28's digest: the
// same URL, the same digest; another path or query, another; and nothing of
// the URL in it.
func TestWebhookSecret_TheURLDigestIsShortKeyedAndStable(t *testing.T) {
	t.Parallel()
	box := testBox(t)
	a, err := webhookURLDigest(box, "https://hooks.slack.com/services/T000/B000/XXXX")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := webhookURLDigest(box, "https://hooks.slack.com/services/T000/B000/XXXX")
	other, _ := webhookURLDigest(box, "https://hooks.slack.com/services/T000/B000/YYYY")
	if a != again || a == other || len(a) != 2*webhookURLDigestBytes {
		t.Errorf("digests %q %q %q", a, again, other)
	}
	otherBox, err := secrets.New([]byte("another-installation-app-secret-0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	if elsewhere, _ := webhookURLDigest(otherBox, "https://hooks.slack.com/services/T000/B000/XXXX"); elsewhere == a {
		t.Error("the digest is the same under another installation's secret")
	}
}
