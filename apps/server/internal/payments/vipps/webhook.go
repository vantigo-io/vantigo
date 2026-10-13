package vipps

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/payments"
)

// The window an x-ms-date must fall in. Vipps retries a callback for up to
// seven days (R4 §4.7), so a genuine one can be that old when it finally
// lands; a day more is slack, and nothing older is accepted, so a captured
// callback cannot be replayed for ever. Five minutes ahead is clock skew.
const (
	maxWebhookAge  = 8 * 24 * time.Hour
	maxWebhookSkew = 5 * time.Minute
)

// contentHash is base64(SHA-256(body)), a webhook's x-ms-content-sha256.
func contentHash(body []byte) string {
	sum := sha256.Sum256(body)
	return base64.StdEncoding.EncodeToString(sum[:])
}

// VerifyWebhook authenticates a callback by R4 §4.7's algorithm: the body's
// content hash against x-ms-content-sha256, then
//
//	POST\n<pathAndQuery>\n<x-ms-date>;<host>;<x-ms-content-sha256>
//
// (with \n, not \r\n) signed by HMAC-SHA256 under the secret's UTF-8 bytes,
// compared in constant time with the Authorization header's. The path,
// query and host are the registered URL's, never the request line's: a proxy
// may rewrite those, and what Vipps signed is the URL it was given. The
// x-ms-date must be an RFC 1123 date no more than eight days old and no more
// than five minutes ahead by the adapter's clock. Anything short of all that
// is ErrInvalidSignature, the reason unsaid.
func (c *Client) VerifyWebhook(r payments.WebhookRequest, secret string, registeredURL *url.URL) (payments.WebhookEvent, error) {
	if registeredURL == nil || secret == "" || r.Method != http.MethodPost {
		return payments.WebhookEvent{}, payments.ErrInvalidSignature
	}
	date := r.Header.Get("x-ms-date")
	hash := r.Header.Get("x-ms-content-sha256")
	if date == "" || hash == "" || !hmac.Equal([]byte(hash), []byte(contentHash(r.Body))) {
		return payments.WebhookEvent{}, payments.ErrInvalidSignature
	}
	sent, err := time.Parse(time.RFC1123, date)
	if err != nil {
		return payments.WebhookEvent{}, payments.ErrInvalidSignature
	}
	now := time.Now
	if c.now != nil {
		now = c.now
	}
	if at := now(); sent.Before(at.Add(-maxWebhookAge)) || sent.After(at.Add(maxWebhookSkew)) {
		return payments.WebhookEvent{}, payments.ErrInvalidSignature
	}
	toSign := "POST\n" + registeredURL.RequestURI() + "\n" + date + ";" + registeredURL.Host + ";" + hash
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(toSign))
	want := "HMAC-SHA256 SignedHeaders=x-ms-date;host;x-ms-content-sha256&Signature=" + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(r.Header.Get("Authorization")), []byte(want)) {
		return payments.WebhookEvent{}, payments.ErrInvalidSignature
	}
	var body struct {
		Reference    string `json:"reference"`
		PSPReference string `json:"pspReference"`
		Name         string `json:"name"`
		Success      bool   `json:"success"`
	}
	if err := json.Unmarshal(r.Body, &body); err != nil || body.Reference == "" || body.Name == "" {
		return payments.WebhookEvent{}, errors.New("vipps: an authenticated webhook's body does not decode")
	}
	return payments.WebhookEvent{Reference: body.Reference, Name: body.Name, PSPReference: body.PSPReference, Success: body.Success}, nil
}
