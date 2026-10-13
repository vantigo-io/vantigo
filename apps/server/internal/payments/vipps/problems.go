package vipps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/payments"
)

// Vipps' error codes the port names (R4 §4.4–§4.5).
const (
	codeInsufficientFunds   = "6260"
	codeCaptureFailed       = "6280"
	codeAlreadyCancelled    = "6050"
	codeAlreadyCancelledToo = "6190"
)

// problem is the part of Vipps' RFC 7807 problem the port reads.
type problem struct {
	ExtraDetails []struct {
		Name   string `json:"name"`
		Reason string `json:"reason"`
	} `json:"extraDetails"`
}

// codeOf is a problem's ErrorCode, empty when it names none or the body is
// no problem.
func codeOf(body []byte) string {
	var p problem
	if err := json.Unmarshal(body, &p); err != nil {
		return ""
	}
	for _, d := range p.ExtraDetails {
		if d.Name == "ErrorCode" {
			return d.Reason
		}
	}
	return ""
}

// alreadyCancelled reports whether a cancel was answered "already
// cancelled", which is success.
func alreadyCancelled(a answer) bool {
	if a.status < 400 || a.status >= 500 {
		return false
	}
	code := codeOf(a.body)
	return code == codeAlreadyCancelled || code == codeAlreadyCancelledToo
}

// classify is an answer as the port's errors: nil for a 2xx. Vipps' own
// codes for a failed capture come first, on any 4xx; then the status. What
// is left — a 3xx, a 5xx, any other status — is a plain error, an unknown
// outcome; Vipps' body never goes into it.
func (c *Client) classify(op string, a answer) error {
	if a.status >= 200 && a.status < 300 {
		return nil
	}
	var code string
	if a.status >= 400 && a.status < 500 {
		code = codeOf(a.body)
		switch code {
		case codeInsufficientFunds:
			return payments.ErrInsufficientFunds
		case codeCaptureFailed:
			return payments.ErrCaptureFailed
		}
	}
	switch a.status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return payments.ErrUnauthorized
	case http.StatusNotFound:
		return payments.ErrNotFound
	case http.StatusConflict:
		return &payments.ErrConflict{Code: code}
	case http.StatusBadRequest:
		return &payments.ErrRefused{Code: code}
	case http.StatusLocked, http.StatusTooManyRequests:
		return &payments.ErrThrottled{RetryAfter: retryAfter(a.retryAfter, c.now())}
	default:
		return fmt.Errorf("vipps: %s answered %d", op, a.status)
	}
}

// retryAfter reads a Retry-After header, seconds or an HTTP date judged
// against now; zero when it is absent, unreadable or already past.
func retryAfter(v string, now time.Time) time.Duration {
	if v == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(v); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(v); err == nil {
		if d := at.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}
