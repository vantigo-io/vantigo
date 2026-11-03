package mail

import (
	"crypto/x509"
	"net/http"

	gomail "github.com/wneessen/go-mail"
)

// AllowLoopbackForTests lets the SMTP driver's own tests connect to an
// in-process loopback listener despite the DNS-rebinding guard, and trust
// rootCAs (typically the in-process test server's self-signed certificate)
// in place of the system pool. Production code has no configuration field
// that reaches either package-level variable this sets — only a _test.go
// file, compiled solely by `go test`, can call this.
func AllowLoopbackForTests(rootCAs *x509.CertPool) (restore func()) {
	previousLoopback, previousCAs := allowLoopback, testRootCAs
	allowLoopback, testRootCAs = true, rootCAs
	return func() { allowLoopback, testRootCAs = previousLoopback, previousCAs }
}

// RenderOutboundForTests is Outbound.message, for a test of a rendering rule
// that refuses before any connection is made.
func RenderOutboundForTests(from string, out Outbound) (*gomail.Msg, error) {
	return out.message(from)
}

// UseCloudflareForTests points the cloudflare driver at base (an httptest
// server's URL) over transport, in place of api.cloudflare.com behind the
// guarded transport. Like AllowLoopbackForTests, only a _test.go file can
// reach the variables it sets.
func UseCloudflareForTests(base string, transport http.RoundTripper) (restore func()) {
	previousBase, previousTransport := testCloudflareBase, testCloudflareTransport
	testCloudflareBase, testCloudflareTransport = base, transport
	return func() { testCloudflareBase, testCloudflareTransport = previousBase, previousTransport }
}
