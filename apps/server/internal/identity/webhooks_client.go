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
	// webhookMaxRetryAfter caps the delay a receiver may ask for (W16).
	webhookMaxRetryAfter = time.Hour
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

// webhookNetwork is what the guarded transport resolves and dials with. The
// zero value is production's: the system resolver, a plain dialler and TLS's
// defaults. It is the only seam a test has (Access.webhookNet): a test swaps
// the resolver, the dialler and the roots it trusts, and every attempt still
// goes through netguard.DialContext, which checks each address the resolver
// answers before the dialler sees any. Deps.HTTPTransport, which other
// modules' clients take from tests, never replaces this transport.
type webhookNetwork struct {
	resolver netguard.Resolver
	dial     func(ctx context.Context, network, address string) (net.Conn, error)
	tls      *tls.Config
}

// webhookTransport is the production transport, built once for the
// process: its dialler goes through netguard, so every address the host
// resolves to is checked against the one table of forbidden addresses and
// the checked literal is dialled, and DNS cannot change between the check
// and the connect.
var webhookTransport = sync.OnceValue(func() *http.Transport {
	return webhookGuardedTransport(webhookNetwork{})
})

// webhookGuardedTransport is a transport dialling through netguard over n.
// Proxy is nil on purpose: a proxy would send the request to an address the
// guard never checked. TLS verifies the certificate against the URL's host,
// not the dialled address. Keep-alives are off: no connection to a stranger's
// server is kept open.
func webhookGuardedTransport(n webhookNetwork) *http.Transport {
	resolver := n.resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dial := n.dial
	if dial == nil {
		dial = (&net.Dialer{Timeout: webhookDialTimeout}).DialContext
	}
	return &http.Transport{
		Proxy:                  nil,
		DisableKeepAlives:      true,
		DialContext:            netguard.DialContext(resolver, dial),
		TLSClientConfig:        n.tls,
		ForceAttemptHTTP2:      true,
		TLSHandshakeTimeout:    webhookTLSHandshakeTimeout,
		ResponseHeaderTimeout:  webhookResponseHeaderTimeout,
		MaxResponseHeaderBytes: webhookMaxResponseHeaderSize,
	}
}

// newWebhookClient is the client the sender posts through: the process's
// guarded transport, or one built over a test's network when Access has one.
// Redirects are never followed (W16): a 3xx is handed back and recorded as a
// failure.
func newWebhookClient(a *Access, now func() time.Time, log *slog.Logger) *webhookClient {
	transport := webhookTransport()
	if a.webhookNet != nil {
		transport = webhookGuardedTransport(*a.webhookNet)
	}
	return &webhookClient{
		http: &http.Client{
			Transport:     transport,
			Timeout:       webhookAttemptBudget,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		now: now,
		log: log,
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
// excerpt is set for a status that is not 2xx; retryAfter is the delay a 429,
// 502, 503 or 504 asked for, bounded to an hour (W16), zero for none.
//
// aborted is an attempt the caller's context ended — the worker stopping, a
// lease given up — rather than the receiver: it is not an attempt that
// happened, and the sender records nothing for it (W16, W38).
type webhookAttempt struct {
	at         time.Time
	status     int
	duration   time.Duration
	errKind    string
	excerpt    string
	retryAfter time.Duration
	aborted    bool
}

// succeeded reports whether the attempt was answered 2xx, the only success.
func (a webhookAttempt) succeeded() bool { return a.errKind == "" && !a.aborted }

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
	if ctx.Err() != nil {
		attempt.aborted = true
		return attempt
	}
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
		if ctx.Err() != nil {
			// The caller gave up, not the receiver: the client's own budget
			// is its Timeout, which leaves ctx alone.
			attempt.aborted = true
			return attempt
		}
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
		switch resp.StatusCode {
		case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			attempt.retryAfter = webhookRetryAfter(resp.Header.Get("Retry-After"), at)
		}
	}
	if readErr == nil {
		attempt.excerpt = webhookExcerpt(read)
	}
	c.logFailure(ctx, m.url, attempt)
	return attempt
}

// webhookRetryAfter reads a Retry-After header, delay-seconds or an
// HTTP-date, as the delay from at it asks for, between zero and an hour.
// Anything else is zero: the header's text is never kept.
func webhookRetryAfter(header string, at time.Time) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	var d time.Duration
	if secs, err := strconv.ParseInt(header, 10, 64); err == nil {
		if secs > int64(webhookMaxRetryAfter/time.Second) {
			return webhookMaxRetryAfter
		}
		d = time.Duration(secs) * time.Second
	} else if when, err := http.ParseTime(header); err == nil {
		d = when.Sub(at)
	}
	return min(max(d, 0), webhookMaxRetryAfter)
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
		// net/http has no sentinel for MaxResponseHeaderBytes: its words
		// are the one signal, and a test holds them.
		return webhookErrTooLarge
	}
	return webhookErrOther
}

// webhookExcerpt is at most webhookMaxExcerpt bytes of an answer as plain
// text: valid UTF-8, every control and format character (the bidirectional
// overrides, zero-width joiners) a space, cut at a rune.
func webhookExcerpt(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	text := strings.ToValidUTF8(string(body), "�")
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
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
