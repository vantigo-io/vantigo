package identity

import (
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/secrets"
)

// The secrets.Box purposes of webhooks (outgoing webhooks design W8, W28).
// An endpoint's signing secret must be recoverable for the HMAC, so it is
// sealed, never hashed. The URL digest is a MAC under a purpose of its own,
// so no key both encrypts and authenticates.
const (
	// webhookSecretPurposePrefix is the purpose a secret is sealed under,
	// followed by its endpoint's id (webhookSecretPurpose).
	webhookSecretPurposePrefix = "identity/webhook-secret/"
	webhookURLDigestPurpose    = "identity/webhook-url-digest"
	// webhookURLDigestBytes is how much of the URL's MAC the audit keeps:
	// enough to see that a URL changed, too little to stand for it.
	webhookURLDigestBytes = 8
)

// webhookSecretPurpose is the purpose an endpoint's secret is sealed under:
// identity/webhook-secret/ and the endpoint's id. The box uses the purpose
// both to derive the key and as the AEAD's additional data, so a ciphertext
// copied into another endpoint's row does not open there: a secret signs for
// the endpoint it was made for, or not at all.
func webhookSecretPurpose(endpoint uuid.UUID) string {
	return webhookSecretPurposePrefix + endpoint.String()
}

// sealWebhookSecret encrypts secret for the endpoint's secret_ciphertext or
// previous_secret_ciphertext.
func sealWebhookSecret(box *secrets.Box, endpoint uuid.UUID, secret string) ([]byte, error) {
	sealed, err := box.Seal(webhookSecretPurpose(endpoint), []byte(secret))
	if err != nil {
		return nil, fmt.Errorf("identity: seal the webhook secret: %w", err)
	}
	return sealed, nil
}

// openWebhookSecret decrypts a secret sealed for the endpoint. Its error
// never carries the ciphertext.
func openWebhookSecret(box *secrets.Box, endpoint uuid.UUID, sealed []byte) (string, error) {
	plain, err := box.Open(webhookSecretPurpose(endpoint), sealed)
	if err != nil {
		return "", fmt.Errorf("identity: open the webhook secret: %w", err)
	}
	return string(plain), nil
}

// webhookURLDigest is the short keyed digest of a full endpoint URL the
// audit, notices and log lines carry beside its scheme and host (W28): a
// change of path or query shows, and neither can be read back or guessed
// from it without the installation's secret.
func webhookURLDigest(box *secrets.Box, rawURL string) (string, error) {
	mac, err := box.MAC(webhookURLDigestPurpose, []byte(rawURL))
	if err != nil {
		return "", fmt.Errorf("identity: digest the webhook URL: %w", err)
	}
	return hex.EncodeToString(mac[:webhookURLDigestBytes]), nil
}
