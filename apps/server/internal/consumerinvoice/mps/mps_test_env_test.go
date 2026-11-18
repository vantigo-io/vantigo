//go:build mps

package mps_test

import (
	"context"
	"crypto/tls"
	"os"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/consumerinvoice"
	"github.com/vantigo-io/vantigo/server/internal/consumerinvoice/mps"
)

// The test-environment test runs the adapter against MPS's own test
// environment (invoices phase 6 plan D13), once Vantigo has submitter access
// there:
//
//	go test -tags mps -run TestMpsTestEnv ./internal/consumerinvoice/mps/
//
// with MPS_TEST_EFAKTURA_URL, MPS_TEST_MANDATE_URL, MPS_TEST_CERT_FILE,
// MPS_TEST_KEY_FILE, MPS_TEST_USERNAME, MPS_TEST_PASSWORD, MPS_TEST_ISSUER
// and, for AvtaleGiro, MPS_TEST_CREDITOR_ACCOUNT, from a machine whose
// address MPS has allow-listed. It confirms what the adapter marks UNSURE —
// the namespace, the lookup's order, the coverage parameter — and measures
// the lookup's hit rate without a fødselsnummer over MPS_TEST_LOOKUP_PHONES
// (comma-separated "phone|e-mail|yyyy-mm-dd" keys from the shared test data).
func TestMpsTestEnv_Verify(t *testing.T) {
	need := func(name string) string {
		v := os.Getenv(name)
		if v == "" {
			t.Skipf("%s is not set", name)
		}
		return v
	}
	cert, err := tls.LoadX509KeyPair(need("MPS_TEST_CERT_FILE"), need("MPS_TEST_KEY_FILE"))
	if err != nil {
		t.Fatalf("load the client certificate: %v", err)
	}
	a, err := mps.New(mps.Config{
		EfakturaBaseURL: need("MPS_TEST_EFAKTURA_URL"), MandateBaseURL: need("MPS_TEST_MANDATE_URL"),
		Certificate: cert, Username: need("MPS_TEST_USERNAME"), Password: need("MPS_TEST_PASSWORD"),
		Issuer: need("MPS_TEST_ISSUER"), CreditorAccount: os.Getenv("MPS_TEST_CREDITOR_ACCOUNT"), Clock: time.Now,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := a.Verify(ctx); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if _, err := a.Lookup(ctx, []consumerinvoice.Consumer{{Phone: "+4700000000", Email: "nobody@example.invalid"}}); err != nil {
		t.Errorf("lookup: %v", err)
	}
}
