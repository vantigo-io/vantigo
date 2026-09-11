package mail

import "crypto/x509"

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
