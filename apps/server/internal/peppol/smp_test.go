package peppol

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The SMP is reached over real TLS in these tests: an httptest.NewTLSServer,
// with the base URL the code is given being the one its certificate is issued
// for ("example.com") and the transport's dialer sending the connection to the
// test server's port. Nothing resolves anything and nothing leaves the
// machine, but the request, the TLS handshake and the response are real —
// which matters, because two of the things being pinned here (that no Accept
// header is sent, and that the certificate is checked against the URL's host
// rather than the address dialled) live in exactly the layer a fake
// RoundTripper would replace.

// The document type identifiers that answer the question this package exists
// for, as the live SMPs publish them. Compared in full, never by prefix: the
// reminder profile below shares the invoice id's first 130 characters.
const (
	liveInvoiceHref = "https://smp.elma-smp.no/iso6523-actorid-upis%3A%3A0192%3A923609016/services/" +
		"busdox-docid-qns%3A%3Aurn%3Aoasis%3Anames%3Aspecification%3Aubl%3Aschema%3Axsd%3AInvoice-2%3A%3AInvoice" +
		"%23%23urn%3Acen.eu%3Aen16931%3A2017%23compliant%23urn%3Afdc%3Apeppol.eu%3A2017%3Apoacc%3Abilling%3A3.0%3A%3A2.1"

	reminderDocumentType = "busdox-docid-qns::urn:oasis:names:specification:ubl:schema:xsd:Invoice-2::Invoice" +
		"##urn:cen.eu:en16931:2017#compliant#urn:fdc:peppol.eu:2017:poacc:billing:3.0" +
		"#conformant#urn:fdc:anskaffelser.no:2019:ehf:reminder:3.0::2.2"

	wildcardInvoiceDocumentType = "peppol-doctype-wildcard::urn:oasis:names:specification:ubl:schema:xsd:Invoice-2::Invoice" +
		"##urn:peppol:pint:billing-1::2.1"

	orderDocumentType = "busdox-docid-qns::urn:oasis:names:specification:ubl:schema:xsd:Order-2::Order" +
		"##urn:fdc:peppol.eu:poacc:trns:order:3::2.1"
)

// everyDocumentType is the answer of a well-connected Norwegian participant:
// seventeen registered document types, the two billing ones among them, the
// EHF reminder profile whose id starts like the invoice one, and a pair of
// wildcard PINT ids. It is the fixture the happy path uses, because a real
// answer is long and full of near-misses and a one-entry fixture would hide
// every way the matching can go wrong.
func everyDocumentType() []string {
	return []string{
		orderDocumentType,
		"busdox-docid-qns::urn:oasis:names:specification:ubl:schema:xsd:OrderResponse-2::OrderResponse##urn:fdc:peppol.eu:poacc:trns:order_response:3::2.1",
		"busdox-docid-qns::urn:oasis:names:specification:ubl:schema:xsd:Order-2::Order##urn:fdc:peppol.eu:poacc:trns:order_change:3::2.1",
		"busdox-docid-qns::urn:oasis:names:specification:ubl:schema:xsd:OrderCancellation-2::OrderCancellation##urn:fdc:peppol.eu:poacc:trns:order_cancellation:3::2.1",
		"busdox-docid-qns::urn:oasis:names:specification:ubl:schema:xsd:Order-2::Order##urn:fdc:anskaffelser.no:2019:ehf:spec:order-agreement:3.0::2.2",
		"busdox-docid-qns::urn:oasis:names:specification:ubl:schema:xsd:Catalogue-2::Catalogue##urn:fdc:peppol.eu:poacc:trns:catalogue:3::2.1",
		"busdox-docid-qns::urn:oasis:names:specification:ubl:schema:xsd:Catalogue-2::Catalogue##urn:fdc:peppol.eu:poacc:trns:punch_out:3::2.1",
		"busdox-docid-qns::urn:oasis:names:specification:ubl:schema:xsd:ApplicationResponse-2::ApplicationResponse##urn:fdc:peppol.eu:poacc:trns:catalogue_response:3::2.1",
		"busdox-docid-qns::urn:oasis:names:specification:ubl:schema:xsd:DespatchAdvice-2::DespatchAdvice##urn:fdc:peppol.eu:poacc:trns:despatch_advice:3::2.1",
		"busdox-docid-qns::urn:oasis:names:specification:ubl:schema:xsd:ApplicationResponse-2::ApplicationResponse##urn:fdc:peppol.eu:poacc:trns:mlr:3::2.1",
		"busdox-docid-qns::urn:oasis:names:specification:ubl:schema:xsd:ApplicationResponse-2::ApplicationResponse##urn:fdc:peppol.eu:poacc:trns:invoice_response:3::2.1",
		invoiceDocumentType,
		creditNoteDocumentType,
		reminderDocumentType,
		"busdox-docid-qns::urn:oasis:names:specification:ubl:schema:xsd:Invoice-2::Invoice##urn:fdc:anskaffelser.no:2019:ehf:payment-request:3.0::2.2",
		wildcardInvoiceDocumentType,
		"peppol-doctype-wildcard::urn:oasis:names:specification:ubl:schema:xsd:CreditNote-2::CreditNote##urn:peppol:pint:billing-1::2.1",
	}
}

// serviceGroupXML is the ServiceGroup an SMP answers with: the publishing
// namespace on the root, the identifier namespace on the participant, and one
// ServiceMetadataReference per registered document type whose href ends in the
// escaped identifier after "/services/".
func serviceGroupXML(participant string, documentTypes []string) string {
	var refs strings.Builder
	for _, documentType := range documentTypes {
		href := fmt.Sprintf("https://smp.example.test/%s/services/%s",
			escapeIdentifier(Scheme+"::"+participant), escapeIdentifier(documentType))
		refs.WriteString(`    <ServiceMetadataReference href="` + href + `"/>` + "\n")
	}
	return `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<ServiceGroup xmlns="http://busdox.org/serviceMetadata/publishing/1.0/"` + "\n" +
		`              xmlns:id="http://busdox.org/transport/identifiers/1.0/">` + "\n" +
		`  <id:ParticipantIdentifier scheme="` + Scheme + `">` + participant + `</id:ParticipantIdentifier>` + "\n" +
		`  <ServiceMetadataReferenceCollection>` + "\n" +
		refs.String() +
		`  </ServiceMetadataReferenceCollection>` + "\n" +
		`</ServiceGroup>` + "\n"
}

// smpRecorder keeps what an SMP stub served, behind a mutex: it is written on
// the server's goroutine and read on the test's.
type smpRecorder struct {
	mu     sync.Mutex
	served int
	last   *http.Request
}

func (r *smpRecorder) record(request *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.served++
	r.last = request.Clone(context.Background())
}

func (r *smpRecorder) lastRequest(t *testing.T) *http.Request {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.last == nil {
		t.Fatal("the SMP stub served no request")
	}
	return r.last
}

// requests is how many requests the SMP stub has served.
func (r *smpRecorder) requests() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.served
}

// smpTransport starts an SMP on TLS and returns a transport that reaches it at
// any base URL whose host its certificate covers ("example.com"), by sending
// the connection to the test server's port. Nothing is resolved and nothing
// leaves the machine, but the handshake and the request are real.
func smpTransport(t *testing.T, handler http.HandlerFunc) (http.RoundTripper, *smpRecorder) {
	t.Helper()
	recorder := &smpRecorder{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.record(r)
		handler(w, r)
	}))
	t.Cleanup(server.Close)

	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	return transport, recorder
}

// smpClient is smpTransport with a Client wrapped around it, built from opts
// (Zone defaults to the production one, HTTPTransport is the stub's).
func smpClient(t *testing.T, opts Options, handler http.HandlerFunc) (*Client, *smpRecorder) {
	t.Helper()
	transport, recorder := smpTransport(t, handler)
	if opts.Zone == "" {
		opts.Zone = prodZone
	}
	opts.HTTPTransport = transport
	return NewClient(opts), recorder
}

// smpServer is smpClient with default options.
func smpServer(t *testing.T, handler http.HandlerFunc) (*Client, *smpRecorder) {
	t.Helper()
	return smpClient(t, Options{}, handler)
}

// serviceGroupHandler answers every request with the ServiceGroup of a
// participant that registered documentTypes.
func serviceGroupHandler(participant string, documentTypes []string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(serviceGroupXML(participant, documentTypes)))
	}
}

// testBase is a base URL the httptest certificate is valid for, so the TLS
// handshake in these tests is a real one.
const testBase = "https://example.com"

func TestFetchDocumentTypes_RequestShape(t *testing.T) {
	t.Parallel()
	client, recorder := smpServer(t, serviceGroupHandler("0192:923609016", everyDocumentType()))

	// Every base that passes the policy must produce the SAME request target,
	// on the wire and not merely in the *url.URL: the participant identifier
	// belongs in the path, and a base with a stray slash or an empty fragment
	// must not push it into a query string or produce the "//" ELMA answers 400
	// to. A 404 to a mangled target would read as "not registered".
	const want = "/iso6523-actorid-upis%3A%3A0192%3A923609016"
	for _, base := range []string{testBase, testBase + "/", testBase + "//", testBase + "///", testBase + "/#"} {
		target, err := smpRequestURL(base, "0192:923609016")
		if err != nil {
			t.Fatalf("smpRequestURL(%q): %v", base, err)
		}
		if _, _, err := client.fetchDocumentTypes(context.Background(), target); err != nil {
			t.Fatalf("fetchDocumentTypes for base %q: %v", base, err)
		}
		recorded := recorder.lastRequest(t)
		if recorded.RequestURI != want {
			t.Errorf("base %q issued the request URI %q, want %q", base, recorded.RequestURI, want)
		}
		if recorded.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", recorded.Method)
		}
		// ELMA answers 406 to "Accept: application/xml", and Go sends no Accept
		// of its own — so this request must not grow one.
		if accept, ok := recorded.Header["Accept"]; ok {
			t.Errorf("the request sent Accept: %v, want no Accept header at all", accept)
		}
	}
}

// TestSMPRequestURL_Path covers the path escaping on its own, including the
// "#" a document-type-shaped identifier would carry and the trailing slash the
// base URL may or may not have (both seen live; "//" answers 400).
func TestSMPRequestURL_Path(t *testing.T) {
	t.Parallel()
	cases := []struct {
		base        string
		participant string
		wantPath    string
	}{
		{"https://smp.elma-smp.no", "0192:923609016", "/iso6523-actorid-upis%3A%3A0192%3A923609016"},
		{"https://smp.elma-smp.no/", "0192:923609016", "/iso6523-actorid-upis%3A%3A0192%3A923609016"},
		{"https://smp.elma-smp.no///", "0192:923609016", "/iso6523-actorid-upis%3A%3A0192%3A923609016"},
		{"https://smp.example.test/smp/", "0088:123abc", "/smp/iso6523-actorid-upis%3A%3A0088%3A123abc"},
		{"https://smp.example.test:443", "0192:923609016", "/iso6523-actorid-upis%3A%3A0192%3A923609016"},
		{"https://smp.example.test", "0192:9236#016", "/iso6523-actorid-upis%3A%3A0192%3A9236%23016"},
		{"https://smp.example.test", "0192:92 36", "/iso6523-actorid-upis%3A%3A0192%3A92%2036"},
		// The slashes are trimmed from the PARSED path, so a base that hides one
		// behind an empty fragment is normalised like any other.
		{"https://smp.example.test//", "0192:923609016", "/iso6523-actorid-upis%3A%3A0192%3A923609016"},
		{"https://smp.example.test/#", "0192:923609016", "/iso6523-actorid-upis%3A%3A0192%3A923609016"},
		{"https://smp.example.test//#", "0192:923609016", "/iso6523-actorid-upis%3A%3A0192%3A923609016"},
		{"https://smp.example.test/smp//", "0088:123abc", "/smp/iso6523-actorid-upis%3A%3A0088%3A123abc"},
		{"https://smp.example.test/smp/#", "0088:123abc", "/smp/iso6523-actorid-upis%3A%3A0088%3A123abc"},
	}
	for _, tc := range cases {
		t.Run(tc.base+" "+tc.participant, func(t *testing.T) {
			t.Parallel()
			got, err := smpRequestURL(tc.base, tc.participant)
			if err != nil {
				t.Fatalf("smpRequestURL(%q, %q): %v", tc.base, tc.participant, err)
			}
			if got.EscapedPath() != tc.wantPath {
				t.Errorf("escaped path = %q, want %q", got.EscapedPath(), tc.wantPath)
			}
			// RequestURI is what goes on the wire: it catches an identifier that
			// ended up in a query string rather than in the path.
			if got.RequestURI() != tc.wantPath {
				t.Errorf("request URI = %q, want %q", got.RequestURI(), tc.wantPath)
			}
			if got.RawQuery != "" || got.ForceQuery {
				t.Errorf("URL %q carries a query", got)
			}
			if got.Fragment != "" || got.RawFragment != "" {
				t.Errorf("URL %q carries a fragment", got)
			}
		})
	}
}

// TestSMPRequestURL_Policy is the guard on a URL that arrived in a DNS record
// published by a third party. Everything the specification does not allow is
// refused before a socket is opened, because the alternative is letting a
// hostile SMP record aim this server's HTTP client wherever it likes.
func TestSMPRequestURL_Policy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		base string
	}{
		{"empty", ""},
		{"not a URL at all", "://"},
		{"a bare host", "smp.example.test"},
		{"scheme-relative", "//smp.example.test"},
		{"plain http", "http://smp.example.test/"},
		{"a file URL", "file:///etc/passwd"},
		{"no host", "https:///services"},
		{"another port", "https://smp.example.test:8443/"},
		{"port 80 spelled out", "https://smp.example.test:80/"},
		{"userinfo", "https://user@smp.example.test/"},
		{"userinfo with a password", "https://user:secret@smp.example.test/"},
		{"a query string", "https://smp.example.test/?redirect=1"},
		// A bare "?" carries no query but sets ForceQuery, and url.URL renders it
		// back as "/?" — concatenating a path onto that would put the participant
		// identifier in the query string and leave the path as "/".
		{"a forced empty query", "https://smp.example.test/?"},
		{"a forced empty query with an empty fragment", "https://smp.example.test/?#"},
		{"a forced empty query on a path", "https://smp.example.test/smp?"},
		{"a fragment", "https://smp.example.test/#anchor"},
		{"a control character", "https://smp.example.test/\x00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := smpRequestURL(tc.base, "0192:923609016")
			if err == nil {
				t.Fatalf("smpRequestURL(%q) = %q, want an error", tc.base, got)
			}
			if !errors.Is(err, errSMPURL) {
				t.Errorf("error %q is not errSMPURL", err)
			}
		})
	}
}

// TestFetchDocumentTypes_TLSVerifiesTheURLHost is the check that makes the
// address guard safe to use: netguard dials the IP literal it checked, and
// http.Transport still derives the TLS ServerName from the URL's host, so a
// certificate issued to somebody else is rejected. If this ever stopped
// holding, every guarded request would be one DNS answer away from talking TLS
// to the wrong server.
//
// It runs against the PRODUCTION transport — guardedTransport's own
// configuration, with a fake netguard resolver mapping the name to a public
// address and the inner dialler sending the connection to the test server — so
// that a future TLSClientConfig with an InsecureSkipVerify or a fixed
// ServerName in it would fail this test rather than pass it. The only thing
// added on top is the test server's certificate as a root, because it is
// self-signed.
func TestFetchDocumentTypes_TLSVerifiesTheURLHost(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(serviceGroupHandler("0192:923609016", everyDocumentType()))
	t.Cleanup(server.Close)

	resolver := &fakeNetResolver{addrs: []netip.Addr{netip.MustParseAddr("203.0.113.5")}}
	transport := guardedTransport(resolver, func(ctx context.Context, network, address string) (net.Conn, error) {
		// The guard resolved the name and is dialling the address it checked;
		// the test server is where that connection actually goes.
		if !strings.HasPrefix(address, "203.0.113.5:") {
			t.Errorf("the guarded transport dialled %q, want the address the resolver returned", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}).Clone()
	tlsConfig := transport.TLSClientConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	tlsConfig.RootCAs = roots
	transport.TLSClientConfig = tlsConfig

	client := NewClient(Options{Zone: prodZone, HTTPTransport: transport})

	// Both requests go to the same test server; only the URL's host differs, and
	// the httptest certificate covers "example.com" alone.
	if _, _, err := client.fetchDocumentTypes(context.Background(), mustRequestURL(t, testBase, "0192:923609016")); err != nil {
		t.Fatalf("fetchDocumentTypes against the certificate's own host: %v", err)
	}
	if !resolver.wasCalled() {
		t.Error("the request did not go through netguard's resolver")
	}

	_, _, err := client.fetchDocumentTypes(context.Background(), mustRequestURL(t, "https://smp.example.test", "0192:923609016"))
	if err == nil {
		t.Fatal("fetchDocumentTypes accepted a certificate issued to another host")
	}
	if !strings.Contains(err.Error(), "certificate") {
		t.Errorf("error %q does not look like a certificate failure", err)
	}
}

// TestFetchDocumentTypes_BodyReadIsBounded: the transport's handshake and
// response-header timeouts do not cover the body, so an SMP that answers its
// headers promptly and then drips the body would hold this goroutine for as
// long as it liked. http.Client.Timeout is the floor that stops it, and it is a
// floor INDEPENDENT of the context — hence the background context here.
//
// The Client.Timeout is short in this test only because waiting out the 10s
// default would be silly; that the default really is 10s when Options.Timeout
// is unset is pinned by TestNewClient_DefaultTimeout.
func TestFetchDocumentTypes_BodyReadIsBounded(t *testing.T) {
	t.Parallel()
	client, _ := smpClient(t, Options{Timeout: 200 * time.Millisecond}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Header().Set("Content-Length", "1000000")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done() // the body never arrives
	})

	start := time.Now()
	_, _, err := client.fetchDocumentTypes(context.Background(), mustRequestURL(t, testBase, "0192:923609016"))
	if err == nil {
		t.Fatal("fetchDocumentTypes read a body that never finished")
	}
	if !strings.Contains(err.Error(), "could not be read") {
		t.Errorf("error %q does not name the failed body read", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("the read took %v, want it bounded by the client's 200ms timeout", elapsed)
	}
}

func mustRequestURL(t *testing.T, base, participant string) *url.URL {
	t.Helper()
	target, err := smpRequestURL(base, participant)
	if err != nil {
		t.Fatalf("smpRequestURL(%q, %q): %v", base, participant, err)
	}
	return target
}

// TestFetchDocumentTypes_ResponseHandling: a 404 is the SMP's own definitive
// negative, and everything else that is not a 2xx is a failure that names the
// status and never quotes the body (an SMP's error page is not ours to repeat).
func TestFetchDocumentTypes_ResponseHandling(t *testing.T) {
	t.Parallel()

	t.Run("404 is not registered at this SMP", func(t *testing.T) {
		t.Parallel()
		client, _ := smpServer(t, func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "not found", http.StatusNotFound)
		})
		found, documentTypes, err := client.fetchDocumentTypes(context.Background(), mustRequestURL(t, testBase, "0192:923609016"))
		if err != nil {
			t.Fatalf("fetchDocumentTypes: %v", err)
		}
		if found || documentTypes != nil {
			t.Errorf("found = %v, documentTypes = %v, want false and none", found, documentTypes)
		}
	})

	statuses := []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotAcceptable, http.StatusInternalServerError, http.StatusBadGateway}
	for _, status := range statuses {
		t.Run(fmt.Sprintf("%d is a failure", status), func(t *testing.T) {
			t.Parallel()
			client, _ := smpServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte("<html>a secret error page</html>"))
			})
			found, _, err := client.fetchDocumentTypes(context.Background(), mustRequestURL(t, testBase, "0192:923609016"))
			if err == nil {
				t.Fatalf("fetchDocumentTypes accepted a %d response", status)
			}
			if found {
				t.Error("found = true alongside an error")
			}
			if !strings.Contains(err.Error(), fmt.Sprint(status)) {
				t.Errorf("error %q does not name the status", err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Errorf("error %q quotes the response body", err)
			}
		})
	}

	t.Run("a redirect is not followed", func(t *testing.T) {
		t.Parallel()
		var redirected atomic.Bool
		client, _ := smpServer(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "moved") {
				redirected.Store(true)
				_, _ = w.Write([]byte(serviceGroupXML("0192:923609016", everyDocumentType())))
				return
			}
			http.Redirect(w, r, "/moved", http.StatusFound)
		})
		_, _, err := client.fetchDocumentTypes(context.Background(), mustRequestURL(t, testBase, "0192:923609016"))
		if err == nil {
			t.Fatal("fetchDocumentTypes accepted a 302")
		}
		if redirected.Load() {
			t.Error("the client followed the redirect")
		}
		if !strings.Contains(err.Error(), "302") {
			t.Errorf("error %q does not name the status", err)
		}
	})

	t.Run("a body over the cap is refused", func(t *testing.T) {
		t.Parallel()
		client, _ := smpServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<ServiceGroup xmlns="http://busdox.org/serviceMetadata/publishing/1.0/"><!--`))
			padding := strings.Repeat("x", 64<<10)
			for range 32 { // 2 MiB, twice the cap
				_, _ = w.Write([]byte(padding))
			}
			_, _ = w.Write([]byte(`--></ServiceGroup>`))
		})
		_, _, err := client.fetchDocumentTypes(context.Background(), mustRequestURL(t, testBase, "0192:923609016"))
		if err == nil {
			t.Fatal("fetchDocumentTypes read a 2 MiB body")
		}
		if !strings.Contains(err.Error(), "larger") {
			t.Errorf("error %q does not say the body was too large", err)
		}
	})

	t.Run("an unparsable body is a failure", func(t *testing.T) {
		t.Parallel()
		for name, body := range map[string]string{
			"not XML":            "<html><body>503 Service Unavailable</body></html>",
			"truncated XML":      `<ServiceGroup xmlns="http://busdox.org/serviceMetadata/publishing/1.0/"><ServiceMetadataReferenceCollection>`,
			"empty":              "",
			"another namespace":  `<ServiceGroup xmlns="http://example.test/smp"><ServiceMetadataReferenceCollection/></ServiceGroup>`,
			"another root":       `<SignedServiceMetadata xmlns="http://busdox.org/serviceMetadata/publishing/1.0/"/>`,
			"an XML declaration": `<?xml version="1.0"?>`,
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				client, _ := smpServer(t, func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write([]byte(body))
				})
				if _, _, err := client.fetchDocumentTypes(context.Background(), mustRequestURL(t, testBase, "0192:923609016")); err == nil {
					t.Fatalf("fetchDocumentTypes accepted %q as a ServiceGroup", body)
				}
			})
		}
	})
}

// TestDocumentTypesFrom_LiveHref decodes an href transcribed from ELMA's own
// answer, so the parsing is pinned against the real thing and not only against
// this file's fixture builder.
func TestDocumentTypesFrom_LiveHref(t *testing.T) {
	t.Parallel()
	body := `<?xml version="1.0" encoding="UTF-8"?>
<ServiceGroup xmlns="http://busdox.org/serviceMetadata/publishing/1.0/" xmlns:id="http://busdox.org/transport/identifiers/1.0/">
  <id:ParticipantIdentifier scheme="iso6523-actorid-upis">0192:923609016</id:ParticipantIdentifier>
  <ServiceMetadataReferenceCollection>
    <ServiceMetadataReference href="` + liveInvoiceHref + `"/>
  </ServiceMetadataReferenceCollection>
</ServiceGroup>`

	documentTypes, err := documentTypesFrom([]byte(body))
	if err != nil {
		t.Fatalf("documentTypesFrom: %v", err)
	}
	if len(documentTypes) != 1 || documentTypes[0] != invoiceDocumentType {
		t.Fatalf("documentTypesFrom = %q, want the invoice document type", documentTypes)
	}
}

// TestDocumentTypesFrom_Hrefs covers the shapes an href can take around the
// identifier: a reference with no "/services/" at all is not a document type
// and is skipped rather than guessed at.
func TestDocumentTypesFrom_Hrefs(t *testing.T) {
	t.Parallel()
	body := `<ServiceGroup xmlns="http://busdox.org/serviceMetadata/publishing/1.0/">
  <ServiceMetadataReferenceCollection>
    <ServiceMetadataReference href="https://smp.example.test/iso6523-actorid-upis%3A%3A0192%3A923609016/services/` + escapeIdentifier(orderDocumentType) + `"/>
    <ServiceMetadataReference href="https://smp.example.test/iso6523-actorid-upis%3A%3A0192%3A923609016"/>
    <ServiceMetadataReference href=""/>
    <ServiceMetadataReference/>
    <ServiceMetadataReference href="https://smp.example.test/services/"/>
    <ServiceMetadataReference href="https://smp.example.test/services/%zz"/>
  </ServiceMetadataReferenceCollection>
</ServiceGroup>`

	documentTypes, err := documentTypesFrom([]byte(body))
	if err != nil {
		t.Fatalf("documentTypesFrom: %v", err)
	}
	if len(documentTypes) != 1 || documentTypes[0] != orderDocumentType {
		t.Fatalf("documentTypesFrom = %q, want only the one href that names a document type", documentTypes)
	}
}

// TestCapabilities is the heart of the feature and the reason the allow-list is
// compared in full: three of these fixtures are participants seen live that a
// prefix, substring or wildcard match would report as EHF receivers when they
// cannot receive an EHF invoice at all.
func TestCapabilities(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		documentTypes  []string
		wantInvoice    bool
		wantCreditNote bool
	}{
		{
			name:           "a well-connected participant",
			documentTypes:  everyDocumentType(),
			wantInvoice:    true,
			wantCreditNote: true,
		},
		{
			name:          "invoices but no credit notes",
			documentTypes: []string{invoiceDocumentType},
			wantInvoice:   true,
		},
		{
			name:           "credit notes but no invoices",
			documentTypes:  []string{creditNoteDocumentType},
			wantCreditNote: true,
		},
		{
			name:          "only the EHF reminder profile, whose id begins like the invoice's",
			documentTypes: []string{reminderDocumentType},
		},
		{
			name:          "only wildcard PINT ids",
			documentTypes: []string{wildcardInvoiceDocumentType, "peppol-doctype-wildcard::urn:oasis:names:specification:ubl:schema:xsd:CreditNote-2::CreditNote##urn:peppol:pint:billing-1::2.1"},
		},
		{
			name:          "order and response profiles only",
			documentTypes: []string{orderDocumentType, "busdox-docid-qns::urn:oasis:names:specification:ubl:schema:xsd:ApplicationResponse-2::ApplicationResponse##urn:fdc:peppol.eu:poacc:trns:mlr:3::2.1"},
		},
		{
			name:          "nothing registered",
			documentTypes: nil,
		},
		{
			name:          "the invoice id with something appended",
			documentTypes: []string{invoiceDocumentType + "#extra"},
		},
		{
			name:          "the invoice id with a leading space",
			documentTypes: []string{" " + invoiceDocumentType},
		},
		{
			name:          "an older billing version",
			documentTypes: []string{strings.Replace(invoiceDocumentType, "::2.1", "::2.0", 1)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			invoice, creditNote := capabilities(tc.documentTypes)
			if invoice != tc.wantInvoice || creditNote != tc.wantCreditNote {
				t.Errorf("capabilities = invoice %v, credit note %v, want %v and %v",
					invoice, creditNote, tc.wantInvoice, tc.wantCreditNote)
			}
		})
	}
}

// TestFetchDocumentTypes_ReadsTheWholeAnswer ties the fetch and the parse
// together over a real response: every one of the seventeen registered types
// comes back, in the order the SMP listed them.
func TestFetchDocumentTypes_ReadsTheWholeAnswer(t *testing.T) {
	t.Parallel()
	want := everyDocumentType()
	client, _ := smpServer(t, serviceGroupHandler("0192:923609016", want))

	found, got, err := client.fetchDocumentTypes(context.Background(), mustRequestURL(t, testBase, "0192:923609016"))
	if err != nil {
		t.Fatalf("fetchDocumentTypes: %v", err)
	}
	if !found {
		t.Fatal("found = false, want true for a 200 response")
	}
	if len(got) != len(want) {
		t.Fatalf("got %d document types, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("document type %d = %q, want %q", i, got[i], want[i])
		}
	}
}
