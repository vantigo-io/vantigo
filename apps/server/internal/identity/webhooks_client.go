package identity

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vantigo-io/vantigo/server/internal/netguard"
)

// This file is the webhooks' guarded HTTP client (outgoing webhooks design
// W13, W16, W20, W23, W28): one POST of one signed message to one endpoint,
// and the attempt it made, in the closed set of words the log keeps. The
// sender (identity.webhook-sender) calls send; nothing here retries.
//
// The transport is calendar's (srv/calendar/subscriptions_fetch.go), copied
// rather than imported because no module imports another's code.

const (
	// webhookAttemptBudget is one attempt's whole budget: the dial, the TLS
	// handshake, the request, the headers and the body (W16).
	webhookAttemptBudget = 15 * time.Second
	// The transport's own bounds (W20).
	webhookDialTimeout           = 10 * time.Second
	webhookTLSHandshakeTimeout   = 10 * time.Second
	webhookResponseHeaderTimeout = 10 * time.Second
	webhookMaxResponseHeaderSize = 64 << 10
	// webhookMaxBodyRead is the most of an answer read, the rest discarded
	// unread; webhookMaxExcerpt is the most of it the log keeps, for a
	// status that is not 2xx (W23).
	webhookMaxBodyRead = 1 << 10
	webhookMaxExcerpt  = 512
	// webhookUserAgent names the sender to the receiver.
	webhookUserAgent = "Vantigo-Webhooks/1"
)

// The errors an attempt records (W23): a closed set, never an error string
// from the network stack, which can carry an address or the URL.
const (
	webhookErrTimeout           = "timeout"
	webhookErrConnectionRefused = "connection_refused"
	webhookErrDNS               = "dns"
	webhookErrTLS               = "tls"
	webhookErrBlockedAddress    = "blocked_address"
	webhookErrRedirect          = "redirect"
	webhookErrHTTPStatus        = "http_status"
	webhookErrTooLarge          = "too_large"
	webhookErrOther             = "other"
)

// webhookTransport is the production transport, built once for the
// process: its dialler goes through netguard, so every address the host
// resolves to is checked against the one table of forbidden addresses and
// the checked literal is dialled, and DNS cannot change between the check
// and the connect.
var webhookTransport = sync.OnceValue(func() *http.Transport {
	return webhookGuardedTransport(net.DefaultResolver, (&net.Dialer{Timeout: webhookDialTimeout}).DialContext)
})

// webhookGuardedTransport is a transport dialling through netguard over
// resolver. Proxy is nil on purpose: a proxy would send the request to an
// address the guard never checked. TLS keeps the transport's defaults, which
// verify the certificate against the URL's host, not the dialled address.
// Keep-alives are off: no connection to a stranger's server is kept open.
func webhookGuardedTransport(resolver netguard.Resolver, dial func(ctx context.Context, network, address string) (net.Conn, error)) *http.Transport {
	return &http.Transport{
		Proxy:                  nil,
		DisableKeepAlives:      true,
		DialContext:            netguard.DialContext(resolver, dial),
		ForceAttemptHTTP2:      true,
		TLSHandshakeTimeout:    webhookTLSHandshakeTimeout,
		ResponseHeaderTimeout:  webhookResponseHeaderTimeout,
		MaxResponseHeaderBytes: webhookMaxResponseHeaderSize,
	}
}

// newWebhookHTTPClient is the client an attempt goes through: rt when a test
// set one (Deps.HTTPTransport), the guarded transport otherwise. Redirects
// are never followed (W16): a 3xx is handed back and recorded as a failure.
func newWebhookHTTPClient(rt http.RoundTripper) *http.Client {
	if rt == nil {
		rt = webhookTransport()
	}
	return &http.Client{
		Transport:     rt,
		Timeout:       webhookAttemptBudget,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// webhookMessage is what one attempt sends: the endpoint's URL, the
// delivery's message id and body, and the key of each active secret, the
// current one first.
type webhookMessage struct {
	url  string
	id   string
	body []byte
	keys [][]byte
}

// webhookAttempt is what one attempt did. status is 0 when no answer came;
// errKind is "" for a 2xx and otherwise one of the webhookErr* words;
// excerpt is set for a status that is not 2xx; retryAfter is the answer's
// Retry-After header as sent, for the sender to read.
type webhookAttempt struct {
	at         time.Time
	status     int
	duration   time.Duration
	errKind    string
	excerpt    string
	retryAfter string
}

// succeeded reports whether the attempt was answered 2xx, the only success.
func (a webhookAttempt) succeeded() bool { return a.errKind == "" }

// webhookClient sends messages. log takes one line per failed attempt,
// with the URL's scheme and host only (W28); nil discards them.
type webhookClient struct {
	http *http.Client
	now  func() time.Time
	log  *slog.Logger
}

// send POSTs m once, signed at this attempt's time with every key (W13), and
// reports what happened.
func (c *webhookClient) send(ctx context.Context, m webhookMessage) webhookAttempt {
	at := c.now()
	timestamp := at.Unix()
	attempt := webhookAttempt{at: at}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.url, bytes.NewReader(m.body))
	if err != nil {
		attempt.errKind = webhookErrOther
		c.logFailure(ctx, m.url, attempt)
		return attempt
	}
	req.Header.Set("webhook-id", m.id)
	req.Header.Set("webhook-timestamp", strconv.FormatInt(timestamp, 10))
	req.Header.Set("webhook-signature", webhookSignatureHeader(m.id, timestamp, m.body, m.keys...))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", webhookUserAgent)

	started := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		attempt.duration = time.Since(started)
		attempt.errKind = webhookErrorKind(err)
		c.logFailure(ctx, m.url, attempt)
		return attempt
	}
	read, readErr := io.ReadAll(io.LimitReader(resp.Body, webhookMaxBodyRead))
	_ = resp.Body.Close()
	attempt.duration = time.Since(started)
	attempt.status = resp.StatusCode
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return attempt
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		attempt.errKind = webhookErrRedirect
	default:
		attempt.errKind = webhookErrHTTPStatus
		attempt.retryAfter = resp.Header.Get("Retry-After")
	}
	if readErr == nil {
		attempt.excerpt = webhookExcerpt(read)
	}
	c.logFailure(ctx, m.url, attempt)
	return attempt
}

// logFailure writes one line for a failed attempt: the URL's scheme and host,
// never its path or query, the status and the error word.
func (c *webhookClient) logFailure(ctx context.Context, rawURL string, a webhookAttempt) {
	if c.log == nil {
		return
	}
	c.log.LogAttrs(ctx, slog.LevelInfo, "identity: webhook attempt failed",
		slog.String("origin", webhookURLOrigin(rawURL)), slog.Int("status", a.status), slog.String("error", a.errKind))
}

// webhookErrorKind is the closed-set word for a failed round trip.
func webhookErrorKind(err error) string {
	var dnsErr *net.DNSError
	var netErr net.Error
	var certErr *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	var invalidCert x509.CertificateInvalidError
	var recordErr tls.RecordHeaderError
	var alertErr tls.AlertError
	switch {
	case errors.Is(err, netguard.ErrDisallowed):
		return webhookErrBlockedAddress
	case errors.As(err, &dnsErr):
		return webhookErrDNS
	case errors.As(err, &certErr), errors.As(err, &unknownAuthority), errors.As(err, &hostnameErr),
		errors.As(err, &invalidCert), errors.As(err, &recordErr), errors.As(err, &alertErr):
		return webhookErrTLS
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return webhookErrTimeout
	case errors.Is(err, syscall.ECONNREFUSED):
		return webhookErrConnectionRefused
	case strings.Contains(err.Error(), "server response headers exceeded"):
		return webhookErrTooLarge
	}
	return webhookErrOther
}

// webhookExcerpt is at most webhookMaxExcerpt bytes of an answer as plain
// text: valid UTF-8, every control character a space, cut at a rune.
func webhookExcerpt(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	text := strings.ToValidUTF8(string(body), "�")
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
	for len(text) > webhookMaxExcerpt {
		_, size := utf8.DecodeLastRuneInString(text)
		text = text[:len(text)-size]
	}
	return text
}
