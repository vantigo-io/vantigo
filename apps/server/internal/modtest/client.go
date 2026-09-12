package modtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// clientTimeout bounds every exchange a Client makes. Without it a hung
// server (or a test that forgets to advance the harness clock past some
// server-side wait) would block Do forever instead of failing the test.
const clientTimeout = 30 * time.Second

// Client is one browser: its own cookie jar and its own client address, every
// exchange validated against the module's contract. It never follows
// redirects, so a test sees each 302 itself. It reports every failure,
// contract violations included, to the t it was made for.
type Client struct {
	t    testing.TB
	h    *Harness
	http *http.Client
	ip   string
}

// Client returns a new client for t with a unique synthetic address from
// 198.18.0.0/15, the benchmarking range no real client uses.
func (h *Harness) Client(t testing.TB) *Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("modtest: %v", err)
	}
	n := h.clients.Add(1)
	return &Client{
		t:  t,
		h:  h,
		ip: fmt.Sprintf("198.18.%d.%d", n>>8&0xff, n&0xff),
		http: &http.Client{
			Jar:           jar,
			Transport:     h.recorder.Transport(t, h.srv.Client().Transport),
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			Timeout:       clientTimeout,
		},
	}
}

// SetCookie plants a cookie in the client's jar, as a response would.
func (c *Client) SetCookie(name, value string) {
	c.http.Jar.SetCookies(c.h.base, []*http.Cookie{{Name: name, Value: value, Path: "/"}})
}

// sessionCookie returns the raw session token this client's jar carries for
// the harness's own base URL, if SignIn (or SetCookie) planted one —
// SignInDisabled's own way of finding *which* user to disable without
// duplicating SignIn's seedUser/seedRole/session plumbing itself.
func (c *Client) sessionCookie() (string, bool) {
	for _, ck := range c.http.Jar.Cookies(c.h.base) {
		if ck.Name == sessionCookieName {
			return ck.Value, true
		}
	}
	return "", false
}

// RequestOption adjusts one request.
type RequestOption func(*request)

type request struct {
	header      http.Header
	contentType string
	body        []byte
}

// Header sets a request header.
func Header(k, v string) RequestOption {
	return func(r *request) { r.header.Set(k, v) }
}

// SkipContract opts one exchange out of contract validation. reason says why
// the exchange is deliberately off-contract; a skipped exchange never counts
// toward coverage.
func SkipContract(reason string) RequestOption {
	return Header(contracttest.SkipHeader, reason)
}

// RawBody sends b as the body, with content type ct, instead of JSON.
func RawBody(ct string, b []byte) RequestOption {
	return func(r *request) { r.contentType, r.body = ct, b }
}

// Do sends one request to path (server-absolute, base path included) and
// returns the response, body read. body, when not nil, is sent as JSON.
func (c *Client) Do(method, path string, body any, opts ...RequestOption) *Response {
	c.t.Helper()
	r := &request{header: http.Header{}}
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("%s %s: encode body: %v", method, path, err)
		}
		r.contentType, r.body = "application/json", b
	}
	for _, o := range opts {
		o(r)
	}

	var reader io.Reader
	if r.body != nil {
		reader = bytes.NewReader(r.body)
	}
	req, err := http.NewRequest(method, c.h.url+path, reader)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	req.Header.Set("X-Forwarded-For", c.ip)
	if r.contentType != "" {
		req.Header.Set("Content-Type", r.contentType)
	}
	for k, v := range r.header {
		req.Header[k] = v
	}

	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		c.t.Fatalf("%s %s: read body: %v", method, path, err)
	}
	return &Response{t: c.t, Status: res.StatusCode, Body: b, headers: res.Header}
}

// Response is one response, body read.
type Response struct {
	t       testing.TB
	Status  int
	Body    []byte
	headers http.Header
}

// JSON decodes the body into v, failing the test if it does not decode.
func (r *Response) JSON(v any) {
	r.t.Helper()
	if err := json.Unmarshal(r.Body, v); err != nil {
		r.t.Fatalf("decode %d response %q: %v", r.Status, r.Body, err)
	}
}

// Code is the error code of the access layer's AuthErrorResponse
// ({"error":{"code"}}), which is how every module's 401 and 403 answer, or ""
// for any other body. A module's own refusals are bare problems and carry no
// code.
func (r *Response) Code() string {
	var body struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(r.Body, &body); err != nil || body.Error == nil {
		return ""
	}
	return body.Error.Code
}

// Header is a response header's first value.
func (r *Response) Header(k string) string {
	return r.headers.Get(k)
}
