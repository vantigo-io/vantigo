package identity

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// This file is the webhook signer (outgoing webhooks design W8, W13):
// Standard Webhooks v1, which Svix also uses. The signed content is
// "{webhook-id}.{webhook-timestamp}.{body}", HMAC-SHA256 keyed by the
// base64-decoded bytes after whsec_, written v1,<base64>; one signature per
// active secret, space-separated, while a roll's overlap runs (W9).

const (
	// webhookSecretPrefix is the Standard Webhooks secret's prefix.
	webhookSecretPrefix = "whsec_"
	// webhookSecretBytes is a secret's random bytes, within the standard's
	// 24 to 64.
	webhookSecretBytes = 32
	// webhookSignatureVersion is the scheme every signature carries.
	webhookSignatureVersion = "v1"
)

// errWebhookSecret is a secret that is not whsec_ and base64.
var errWebhookSecret = errors.New("identity: not a webhook secret")

// newWebhookSecret is a new endpoint secret: whsec_ and the base64 of 32
// random bytes.
func newWebhookSecret() string {
	b := make([]byte, webhookSecretBytes)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error; it crashes the process instead
	return webhookSecretPrefix + base64.StdEncoding.EncodeToString(b)
}

// webhookSecretKey is the HMAC key of secret: the bytes after whsec_,
// base64-decoded.
func webhookSecretKey(secret string) ([]byte, error) {
	raw, ok := strings.CutPrefix(secret, webhookSecretPrefix)
	if !ok || raw == "" {
		return nil, errWebhookSecret
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(key) == 0 {
		return nil, errWebhookSecret
	}
	return key, nil
}

// webhookMessageID is the webhook-id of a delivery: msg_ and its uuid
// without dashes, the same on every attempt and resend (W13).
func webhookMessageID(delivery uuid.UUID) string {
	return "msg_" + hex.EncodeToString(delivery[:])
}

// signWebhook is one v1 signature of body, sent as id at timestamp, under
// key.
func signWebhook(id string, timestamp int64, body, key []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id))
	mac.Write([]byte{'.'})
	mac.Write([]byte(strconv.FormatInt(timestamp, 10)))
	mac.Write([]byte{'.'})
	mac.Write(body)
	return webhookSignatureVersion + "," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// webhookSignatureHeader is the webhook-signature header: one signature per
// key, the current secret's first, space-separated.
func webhookSignatureHeader(id string, timestamp int64, body []byte, keys ...[]byte) string {
	sigs := make([]string, 0, len(keys))
	for _, key := range keys {
		sigs = append(sigs, signWebhook(id, timestamp, body, key))
	}
	return strings.Join(sigs, " ")
}

// verifyWebhookSignature reports whether signature is body's v1 signature
// under key, compared in constant time: what a receiver does, and what the
// tests check a message with.
func verifyWebhookSignature(signature, id string, timestamp int64, body, key []byte) bool {
	return hmac.Equal([]byte(signature), []byte(signWebhook(id, timestamp, body, key)))
}
