package peppol

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/netguard"
)

const (
	// dialTimeout, tlsHandshakeTimeout and responseHeaderTimeout bound the
	// stages of the SMP request. Options.Timeout already bounds a whole Lookup,
	// so these are belts on top of it: they keep one stage of one request from
	// eating the entire budget and leaving nothing for the rest.
	dialTimeout           = 5 * time.Second
	tlsHandshakeTimeout   = 5 * time.Second
	responseHeaderTimeout = 10 * time.Second

	// defaultTimeout is what an unset Options.Timeout means. It is a real
	// number rather than "no bound" because the HTTP client's own Timeout is set
	// from it, and that is the only thing bounding the SMP body read: the
	// transport's timeouts cover the dial, the handshake and the response
	// headers and stop there, so an SMP that answers its headers and then drips
	// the body one byte at a time would otherwise hold a goroutine for as long
	// as it liked. 10s matches PEPPOL_TIMEOUT's own default in internal/config.
	defaultTimeout = 10 * time.Second

	// maxResponseHeaderBytes caps the response headers, the one part of an
	// answer that is read before the body cap can apply.
	maxResponseHeaderBytes = 64 << 10

	// maxParticipantLength is a sanity bound on an identifier value. A real one
	// is around fifteen characters; this only keeps something absurd out of a
	// DNS name and a URL path.
	maxParticipantLength = 256
)

// Options configures a Client.
//
// Zone is required and has no default here: the SML zone is operator
// configuration (PEPPOL_SML_ZONE), and a hard-coded default in this package
// would be a second place for it to be wrong. A lookup in the wrong zone
// answers "not registered" about a perfectly registered customer, which is the
// worst way for a mistake like that to show up.
//
// DNSServers, when empty, means the server's own name servers
// (/etc/resolv.conf). Timeout bounds one Lookup end to end, DNS and SMP
// together, and defaults to defaultTimeout (10s) when it is zero or negative —
// it is never "no bound", because it is also the HTTP client's own timeout and
// so the only thing bounding the SMP body read. HTTPTransport replaces the
// guarded default transport; it is the test seam (and the one the Customers
// module's harness uses), not something an operator configures.
type Options struct {
	Zone          string
	DNSServers    []string
	Timeout       time.Duration
	HTTPTransport http.RoundTripper
}

// Client answers capability questions about Peppol participants. It is safe for
// concurrent use and holds no per-lookup state.
type Client struct {
	zone     string
	timeout  time.Duration
	resolver *Resolver
	http     *http.Client
}

// Result is what one lookup found.
//
// Registered false is a definitive negative: the participant is not in the
// network (NXDOMAIN), or the SMP its records name publishes nothing for it
// (404). It is never a way of reporting a failure — a lookup that could not
// find out returns an error instead, and the caller must not store or show its
// zero Result as an answer.
//
// Registered true with both capabilities false is the real case of a
// participant that is in the network but has not registered the EHF billing
// document types (order or response profiles only), or whose records name no
// SMP at all. SMPHost is the host the capabilities were read from, empty in
// that last case.
type Result struct {
	Registered           bool
	SMPHost              string
	CanReceiveInvoice    bool
	CanReceiveCreditNote bool
}

// NewClient builds a Client from opts. It never fails: a missing zone is
// reported by Lookup, so configuration can be validated where it is read
// rather than at two different points.
func NewClient(opts Options) *Client {
	transport := opts.HTTPTransport
	if transport == nil {
		transport = guardedTransport(net.DefaultResolver, (&net.Dialer{Timeout: dialTimeout}).DialContext)
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Client{
		zone:     opts.Zone,
		timeout:  timeout,
		resolver: &Resolver{Servers: opts.DNSServers},
		http: &http.Client{
			Transport: transport,
			// The same timeout again, on the client rather than the context: a
			// caller that reaches fetchDocumentTypes with a context that has no
			// deadline still gets the body read bounded. See defaultTimeout.
			Timeout: timeout,
			// Redirects are never followed. The base URL is third-party data
			// that has just been through the policy in smp.go; following a
			// redirect would hand the next request to a host that policy never
			// saw, and Go would do it up to ten times. A 3xx is returned to
			// fetchDocumentTypes, which treats it as the non-2xx it is.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// guardedTransport is the production transport: a plain HTTP transport whose
// dialler goes through netguard, so the SMP host is resolved, checked against
// the one table of forbidden addresses, and then dialled by the very address
// that was checked (the DNS-rebinding defence internal/mail already has for
// SMTP).
//
// Proxy is nil on purpose, and NOT http.ProxyFromEnvironment: a proxy would
// send the request to an address the guard never checked, which would defeat
// the guard entirely. If this server ever needs an outbound proxy, the guard
// has to move to where the proxy is, not be quietly dropped here.
//
// TLS is left to the transport's defaults, which is the other half of the
// guard being safe: http.Transport derives the TLS ServerName from the URL's
// host, not from the address it dialled, so a certificate issued to somebody
// else is rejected even though the connection went to an IP literal.
func guardedTransport(resolver netguard.Resolver, dial func(ctx context.Context, network, address string) (net.Conn, error)) *http.Transport {
	return &http.Transport{
		Proxy:                  nil,
		DialContext:            netguard.DialContext(resolver, dial),
		ForceAttemptHTTP2:      true,
		TLSHandshakeTimeout:    tlsHandshakeTimeout,
		ResponseHeaderTimeout:  responseHeaderTimeout,
		MaxResponseHeaderBytes: maxResponseHeaderBytes,
	}
}

// Lookup answers whether participant — an identifier VALUE such as
// "0192:923609016" — is a registered Peppol receiver, and of what.
//
// It performs at most two network operations: the NAPTR query for the
// participant's host in the configured zone, and, if that names an SMP, one
// unauthenticated GET of the participant's ServiceGroup. Both are bounded by
// Options.Timeout together.
//
// An error means "we could not find out" and nothing more: a DNS failure, an
// SMP that would not answer or answered with something unreadable, or a base
// URL that failed the policy in smp.go. The caller reports it as an upstream
// failure and keeps whatever answer it had before; it must never be turned
// into "not registered".
func (c *Client) Lookup(ctx context.Context, participant string) (Result, error) {
	if c.zone == "" {
		return Result{}, fmt.Errorf("peppol: no SML zone is configured, so %q cannot be looked up", participant)
	}
	if err := validParticipant(participant); err != nil {
		return Result{}, err
	}

	// c.timeout is never zero (NewClient defaults it), so this is unconditional:
	// one lookup's DNS and SMP steps share one budget.
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	records, found, err := c.resolver.LookupNAPTR(ctx, ParticipantHost(c.zone, participant))
	if err != nil {
		return Result{}, err
	}
	if !found {
		return Result{}, nil
	}

	base, ok, err := smpBaseURL(records)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		// Registered, with no SMP to ask: able to receive nothing, and no
		// request to make.
		return Result{Registered: true}, nil
	}

	target, err := smpRequestURL(base, participant)
	if err != nil {
		return Result{}, err
	}
	published, documentTypes, err := c.fetchDocumentTypes(ctx, target)
	if err != nil {
		return Result{}, err
	}
	if !published {
		// The SMP the network named publishes nothing for this participant: as
		// definitive as NXDOMAIN, and reported the same way.
		return Result{}, nil
	}

	invoice, creditNote := capabilities(documentTypes)
	return Result{
		Registered:           true,
		SMPHost:              target.Hostname(),
		CanReceiveInvoice:    invoice,
		CanReceiveCreditNote: creditNote,
	}, nil
}

// validParticipant rejects an identifier value that cannot be part of a DNS
// name and a URL path. The value goes into both, so anything with a space, a
// slash, a control character or a non-ASCII byte is a caller's mistake worth
// naming — silently hashing it would produce a name that does not exist, which
// reads as "not registered" and hides the bug.
//
// A value must also contain a colon: every ISO 6523 identifier is
// "<ICD>:<value>" ("0192:923609016"), so a bare organisation number is a
// caller that forgot the scheme prefix rather than a participant that is not
// registered.
func validParticipant(participant string) error {
	if participant == "" {
		return fmt.Errorf("peppol: no participant identifier to look up")
	}
	if len(participant) > maxParticipantLength {
		return fmt.Errorf("peppol: the participant identifier is %d bytes, more than the %d a real one can be", len(participant), maxParticipantLength)
	}
	for i := range len(participant) {
		if c := participant[i]; c <= ' ' || c > '~' || c == '/' {
			return fmt.Errorf("peppol: the participant identifier %q contains a character that cannot be part of a DNS name", participant)
		}
	}
	if !strings.Contains(participant, ":") {
		return fmt.Errorf("peppol: the participant identifier %q has no scheme prefix, so it is not an ISO 6523 identifier such as \"0192:923609016\"", participant)
	}
	return nil
}
