package vipps

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// ContentHash is a webhook body's x-ms-content-sha256, for the published
// vector's test.
var ContentHash = contentHash

// ForceApprove is the test environment's POST
// /epayment/v1/test/payments/{reference}/approve for the test user at phone
// (R4 §4.8) — never usable in production, so it lives with the tests.
func (c *Client) ForceApprove(ctx context.Context, reference, phone string) error {
	body, err := json.Marshal(map[string]any{"customer": map[string]string{"phoneNumber": phone}})
	if err != nil {
		return err
	}
	a, err := c.call(ctx, "approve", http.MethodPost, "/epayment/v1/test/payments/"+url.PathEscape(reference)+"/approve", body, "")
	if err != nil {
		return err
	}
	return c.classify("approve", a)
}
