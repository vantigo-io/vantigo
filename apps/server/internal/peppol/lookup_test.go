package peppol

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/vantigo-io/vantigo/server/internal/netguard"
)

// lookupClient wires one Client to a stub name server and a stub SMP, which is
// what a whole Lookup needs: a NAPTR answer pointing at an https base URL whose
// host the SMP's certificate covers.
func lookupClient(t *testing.T, dns *stubDNS, smp http.HandlerFunc) (*Client, *smpRecorder) {
	t.Helper()
	transport, recorder := smpTransport(t, smp)
	return NewClient(Options{
		Zone:          prodZone,
		DNSServers:    []string{dns.addr},
		Timeout:       5 * time.Second,
		HTTPTransport: transport,
	}), recorder
}

// smpNAPTR is the answer a registered participant's name carries: one U-flag
// Meta:SMP record whose regexp yields base.
func smpNAPTR(base string) []answerRecord {
	return []answerRecord{{rdata: naptrRDATA(100, 10, "U", smpService, "!.*!"+base+"!", ".")}}
}

func TestLookup_RegisteredParticipant(t *testing.T) {
	t.Parallel()
	dns := newStubDNS(t, answersWith(t, reply{answers: smpNAPTR(testBase + "/")}))
	client, smp := lookupClient(t, dns, serviceGroupHandler("0192:923609016", everyDocumentType()))

	got, err := client.Lookup(context.Background(), "0192:923609016")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	want := Result{Registered: true, SMPHost: "example.com", CanReceiveInvoice: true, CanReceiveCreditNote: true}
	if got != want {
		t.Errorf("Lookup:\n got %+v\nwant %+v", got, want)
	}

	// The name asked about is the participant host for the configured zone —
	// the one thing a wrong answer would never reveal.
	queries := dns.seen()
	if len(queries) != 1 {
		t.Fatalf("the name server saw %d queries, want 1", len(queries))
	}
	var query dnsmessage.Message
	if err := query.Unpack(queries[0].message); err != nil {
		t.Fatalf("unpacking the query: %v", err)
	}
	if want := ParticipantHost(prodZone, "0192:923609016") + "."; query.Questions[0].Name.String() != want {
		t.Errorf("queried %q, want %q", query.Questions[0].Name.String(), want)
	}
	if n := smp.requests(); n != 1 {
		t.Errorf("the SMP served %d requests, want 1", n)
	}
}

// TestLookup_InvoiceWithoutCreditNote: the two capabilities are reported
// separately because they really do differ in the wild.
func TestLookup_InvoiceWithoutCreditNote(t *testing.T) {
	t.Parallel()
	dns := newStubDNS(t, answersWith(t, reply{answers: smpNAPTR(testBase)}))
	client, _ := lookupClient(t, dns, serviceGroupHandler("0192:923609016", []string{orderDocumentType, invoiceDocumentType}))

	got, err := client.Lookup(context.Background(), "0192:923609016")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	want := Result{Registered: true, SMPHost: "example.com", CanReceiveInvoice: true}
	if got != want {
		t.Errorf("Lookup:\n got %+v\nwant %+v", got, want)
	}
}

// TestLookup_NotRegistered covers both definitive negatives, and pins that
// neither of them reaches the SMP unnecessarily: NXDOMAIN means there is
// nothing to ask.
func TestLookup_NotRegistered(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		dns         reply
		smp         http.HandlerFunc
		wantSMPCall bool
	}{
		{
			name: "NXDOMAIN: the participant is not in the network",
			dns:  reply{rcode: dnsmessage.RCodeNameError},
		},
		{
			name: "the name exists but carries no NAPTR records",
			dns:  reply{},
		},
		{
			name: "the SMP publishes nothing for the participant",
			dns:  reply{answers: smpNAPTR(testBase)},
			smp: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "not found", http.StatusNotFound)
			},
			wantSMPCall: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			smpHandler := tc.smp
			if smpHandler == nil {
				smpHandler = func(w http.ResponseWriter, _ *http.Request) {
					t.Error("the SMP was asked, want no request")
					w.WriteHeader(500)
				}
			}
			dns := newStubDNS(t, answersWith(t, tc.dns))
			client, smp := lookupClient(t, dns, smpHandler)

			got, err := client.Lookup(context.Background(), "0192:923609016")
			if err != nil {
				t.Fatalf("Lookup: %v", err)
			}
			if got != (Result{}) {
				t.Errorf("Lookup = %+v, want the zero Result", got)
			}
			if called := smp.requests() > 0; called != tc.wantSMPCall {
				t.Errorf("the SMP served %d requests, want %v", smp.requests(), tc.wantSMPCall)
			}
		})
	}
}

// TestLookup_RegisteredWithoutAnSMP is the design's third DNS outcome carried
// all the way out: the participant is in the network, nothing points at an SMP,
// so it is registered and can receive nothing — and no HTTP request is made,
// because there is no URL to make one to.
func TestLookup_RegisteredWithoutAnSMP(t *testing.T) {
	t.Parallel()
	dns := newStubDNS(t, answersWith(t, reply{answers: []answerRecord{
		{rdata: naptrRDATA(100, 10, "U", "E2U+email", "!.*!mailto:post@example.test!", ".")},
	}}))
	client, smp := lookupClient(t, dns, func(http.ResponseWriter, *http.Request) {
		t.Error("the SMP was asked, but no record named one")
	})

	got, err := client.Lookup(context.Background(), "0192:923609016")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if want := (Result{Registered: true}); got != want {
		t.Errorf("Lookup = %+v, want %+v", got, want)
	}
	if n := smp.requests(); n != 0 {
		t.Errorf("the SMP served %d requests, want none", n)
	}
}

// TestLookup_FailuresAreErrors: everything that is not one of the two
// definitive negatives comes back as an error, so a caller can answer "try
// again" instead of "this customer cannot receive EHF".
func TestLookup_FailuresAreErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		dns  reply
		smp  http.HandlerFunc
		want string
	}{
		{
			name: "SERVFAIL from the name server",
			dns:  reply{rcode: dnsmessage.RCodeServerFailure},
			want: "RCodeServerFailure",
		},
		{
			name: "a NAPTR record pointing at plain http",
			dns:  reply{answers: smpNAPTR("http://smp.example.test/")},
			want: "not https",
		},
		{
			name: "a NAPTR record with a broken substitution",
			dns:  reply{answers: smpNAPTR("")},
			want: "regexp",
		},
		{
			name: "the SMP is broken",
			dns:  reply{answers: smpNAPTR(testBase)},
			smp:  func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
			want: "500",
		},
		{
			name: "the SMP answers something that is not a ServiceGroup",
			dns:  reply{answers: smpNAPTR(testBase)},
			smp:  func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html/>")) },
			want: "ServiceGroup",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			smpHandler := tc.smp
			if smpHandler == nil {
				smpHandler = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }
			}
			dns := newStubDNS(t, answersWith(t, tc.dns))
			client, _ := lookupClient(t, dns, smpHandler)

			got, err := client.Lookup(context.Background(), "0192:923609016")
			if err == nil {
				t.Fatalf("Lookup = %+v, want an error", got)
			}
			if got != (Result{}) {
				t.Errorf("Lookup returned %+v alongside an error, want the zero Result", got)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// TestLookup_RejectsUnusableArguments: the zone is required (there is no
// default here — configuration supplies it, and a lookup in the wrong zone
// would answer "not registered" about a perfectly registered customer), and the
// participant has to be something that can go into a DNS name and a URL path.
func TestLookup_RejectsUnusableArguments(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		zone        string
		participant string
	}{
		{"no zone", "", "0192:923609016"},
		{"no participant", prodZone, ""},
		{"a participant with a space", prodZone, "0192: 923609016"},
		{"a participant with a slash", prodZone, "0192:923609016/../.."},
		{"a participant with a newline", prodZone, "0192:923609016\n"},
		{"a participant with a control character", prodZone, "0192:923609016\x00"},
		{"a participant that is not an identifier", prodZone, "923609016"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dns := newStubDNS(t, answersWith(t, reply{answers: smpNAPTR(testBase)}))
			client, _ := lookupClient(t, dns, serviceGroupHandler(tc.participant, everyDocumentType()))
			client.zone = tc.zone

			if got, err := client.Lookup(context.Background(), tc.participant); err == nil {
				t.Fatalf("Lookup(%q) = %+v, want an error", tc.participant, got)
			}
			if n := len(dns.seen()); n != 0 {
				t.Errorf("the name server saw %d queries, want none — the arguments never got that far", n)
			}
		})
	}
}

// TestLookup_TimeoutBoundsTheWholeLookup: Options.Timeout is one lookup end to
// end, so a name server that never answers cannot hold an HTTP handler open
// for the 3s-per-attempt the resolver would otherwise spend.
func TestLookup_TimeoutBoundsTheWholeLookup(t *testing.T) {
	t.Parallel()
	dns := newStubDNS(t, func([]byte, string) [][]byte { return nil })
	client := NewClient(Options{Zone: prodZone, DNSServers: []string{dns.addr}, Timeout: 150 * time.Millisecond})

	start := time.Now()
	if got, err := client.Lookup(context.Background(), "0192:923609016"); err == nil {
		t.Fatalf("Lookup = %+v, want a timeout", got)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("Lookup took %v, want it bounded by the 150ms timeout", elapsed)
	}
}

// fakeNetResolver hands the guarded transport a canned DNS answer, so the
// default transport can be tested without touching real DNS. It records that it
// was consulted, which is how a test proves the request really went through the
// guard rather than around it.
type fakeNetResolver struct {
	addrs  []netip.Addr
	mu     sync.Mutex
	called bool
}

func (r *fakeNetResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	r.mu.Lock()
	r.called = true
	r.mu.Unlock()
	return r.addrs, nil
}

func (r *fakeNetResolver) wasCalled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.called
}

// TestGuardedTransport_RefusesAPrivateAddress is the reason the default
// transport is not http.DefaultTransport: the SMP host comes from a DNS record
// a third party publishes, so a record resolving to loopback or a private
// address must be refused rather than dialled. netguard is the one table of
// those addresses; this test proves the transport is actually built on it.
func TestGuardedTransport_RefusesAPrivateAddress(t *testing.T) {
	t.Parallel()
	for _, address := range []string{"127.0.0.1", "10.0.0.5", "169.254.169.254", "::1"} {
		t.Run(address, func(t *testing.T) {
			t.Parallel()
			dialled := false
			transport := guardedTransport(
				&fakeNetResolver{addrs: []netip.Addr{netip.MustParseAddr(address)}},
				func(context.Context, string, string) (net.Conn, error) {
					dialled = true
					return nil, errors.New("the dialler should never be reached")
				})
			client := &http.Client{Transport: transport}

			request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://smp.example.test/", nil)
			if err != nil {
				t.Fatalf("http.NewRequestWithContext: %v", err)
			}
			response, err := client.Do(request)
			if err == nil {
				_ = response.Body.Close()
				t.Fatalf("the guarded transport dialled a host resolving to %s", address)
			}
			if !errors.Is(err, netguard.ErrDisallowed) {
				t.Errorf("error %q is not netguard.ErrDisallowed", err)
			}
			if dialled {
				t.Error("the guard called the dialler for an address it had already refused")
			}
		})
	}
}

// TestNewClient_DefaultTransport pins the shape of the transport a production
// Client gets: no proxy from the environment (a proxy would send the request
// somewhere the address guard never checked, defeating it), and a dialler of
// its own.
func TestNewClient_DefaultTransport(t *testing.T) {
	t.Parallel()
	client := NewClient(Options{Zone: prodZone})

	transport, ok := client.http.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("the default transport is %T, want *http.Transport", client.http.Transport)
	}
	if transport.Proxy != nil {
		t.Error("the default transport takes a proxy from the environment")
	}
	if transport.DialContext == nil {
		t.Error("the default transport has no DialContext, so it does not dial through the guard")
	}
	if transport.TLSHandshakeTimeout == 0 || transport.ResponseHeaderTimeout == 0 {
		t.Error("the default transport has no handshake or response-header timeout")
	}
	if transport.MaxResponseHeaderBytes == 0 {
		t.Error("the default transport puts no bound on the response headers")
	}
	if transport.TLSClientConfig != nil {
		t.Errorf("the default transport carries a TLS config (%+v), want the defaults so the URL's host is verified", transport.TLSClientConfig)
	}
	if client.http.CheckRedirect == nil {
		t.Fatal("the client follows redirects")
	}
	if err := client.http.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Errorf("CheckRedirect returned %v, want http.ErrUseLastResponse", err)
	}
}

// TestNewClient_DefaultTimeout: an unset Options.Timeout is 10s, not "no
// bound". It has to be a real number rather than zero because the HTTP client's
// own Timeout is set from it: that is the only thing bounding the SMP body read
// (the transport's timeouts cover the handshake and the response headers and
// stop there), and an SMP dripping a body would otherwise hold the goroutine
// for as long as it pleased.
func TestNewClient_DefaultTimeout(t *testing.T) {
	t.Parallel()
	unset := NewClient(Options{Zone: prodZone})
	if unset.timeout != defaultTimeout {
		t.Errorf("timeout = %v, want the %v default", unset.timeout, defaultTimeout)
	}
	if unset.http.Timeout != defaultTimeout {
		t.Errorf("the HTTP client's timeout = %v, want the %v default", unset.http.Timeout, defaultTimeout)
	}

	explicit := NewClient(Options{Zone: prodZone, Timeout: 3 * time.Second})
	if explicit.timeout != 3*time.Second || explicit.http.Timeout != 3*time.Second {
		t.Errorf("timeout = %v and HTTP client timeout = %v, want both 3s", explicit.timeout, explicit.http.Timeout)
	}
}

// TestLookup_RegisteredWithoutTheBillingDocumentTypes is IBM Norge and Dell AS:
// in the network, answering 200, and registered for order and response profiles
// only. Registered, and unable to receive an EHF invoice.
func TestLookup_RegisteredWithoutTheBillingDocumentTypes(t *testing.T) {
	t.Parallel()
	dns := newStubDNS(t, answersWith(t, reply{answers: smpNAPTR(testBase)}))
	orderProfiles := []string{
		orderDocumentType,
		"busdox-docid-qns::urn:oasis:names:specification:ubl:schema:xsd:ApplicationResponse-2::ApplicationResponse##urn:fdc:peppol.eu:poacc:trns:mlr:3::2.1",
		reminderDocumentType,
		wildcardInvoiceDocumentType,
	}
	client, _ := lookupClient(t, dns, serviceGroupHandler("0192:923609016", orderProfiles))

	got, err := client.Lookup(context.Background(), "0192:923609016")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	want := Result{Registered: true, SMPHost: "example.com"}
	if got != want {
		t.Errorf("Lookup:\n got %+v\nwant %+v", got, want)
	}
}

// TestNewClient_HTTPTransportReplacesTheDefault is the seam the Customers
// module's tests use.
func TestNewClient_HTTPTransportReplacesTheDefault(t *testing.T) {
	t.Parallel()
	transport := &http.Transport{}
	client := NewClient(Options{Zone: prodZone, HTTPTransport: transport})
	if client.http.Transport != transport {
		t.Errorf("the client uses %v, want the injected transport", client.http.Transport)
	}
}
