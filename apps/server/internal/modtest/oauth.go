package modtest

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// OAuthClientHost is a fake host that serves OAuth client metadata
// documents over TLS (OAuth connections design C6, A2), for the tests of a
// flow that fetches them: every name it was made for resolves to a public
// address, netguard checks it, and the dial reaches this server, whose
// certificate names those hosts and is the only root trusted.
type OAuthClientHost struct {
	srv   *httptest.Server
	roots *x509.CertPool

	mu      sync.Mutex
	docs    map[string]oauthDocument
	fetches map[string]int
}

type oauthDocument struct {
	body         []byte
	cacheControl string
}

// NewOAuthClientHost starts a host answering for hosts, closed with t.
func NewOAuthClientHost(t testing.TB, hosts ...string) *OAuthClientHost {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "modtest OAuth client host"},
		DNSNames:              hosts,
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	o := &OAuthClientHost{roots: x509.NewCertPool(), docs: map[string]oauthDocument{}, fetches: map[string]int{}}
	o.roots.AddCert(cert)
	o.srv = httptest.NewUnstartedServer(http.HandlerFunc(o.serve))
	o.srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
	o.srv.StartTLS()
	t.Cleanup(o.srv.Close)
	return o
}

// Serve makes the host answer a GET of url with body, as application/json
// with cacheControl when it is not empty.
func (o *OAuthClientHost) Serve(url string, body []byte, cacheControl string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.docs[url] = oauthDocument{body: body, cacheControl: cacheControl}
}

// Fetches is how many times url was asked for.
func (o *OAuthClientHost) Fetches(url string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.fetches[url]
}

func (o *OAuthClientHost) serve(w http.ResponseWriter, r *http.Request) {
	url := "https://" + r.Host + r.URL.RequestURI()
	o.mu.Lock()
	o.fetches[url]++
	doc, ok := o.docs[url]
	o.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if doc.cacheControl != "" {
		w.Header().Set("Cache-Control", doc.cacheControl)
	}
	_, _ = w.Write(doc.body)
}

// Attach makes every client document a fetches reach this host.
func (o *OAuthClientHost) Attach(a *identity.Access) {
	addr := o.srv.Listener.Addr().String()
	a.UseOAuthNetwork(oauthPublicResolver{}, func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}, &tls.Config{RootCAs: o.roots, MinVersion: tls.VersionTLS12})
}

// oauthPublicResolver answers every name with a public address netguard
// lets through.
type oauthPublicResolver struct{}

func (oauthPublicResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("93.184.215.14")}, nil
}

// WithOAuthClientHost makes the installation fetch every OAuth client
// metadata document from o.
func WithOAuthClientHost(o *OAuthClientHost) Option {
	return func(s *setup) { s.oauthHost = o }
}
