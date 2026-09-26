package invoices_test

import (
	"os"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// recorder validates every exchange of every invoices test against
// invoices.yaml and records which operations answered successfully. It is
// shared by all tests, parallel ones included; TestMain turns it into the
// coverage gate. Invoices has no frozen corpus file (openapi/testdata/
// exchanges holds none for it): this gate is what proves every operation is
// exercised.
var recorder = contracttest.NewForModule("invoices")

func TestMain(m *testing.M) {
	// The whole suite's locking guarantee, installed once before any test
	// runs: every call this module makes to the directory or the object store
	// is reported here, and one made from inside a locked transaction is
	// recorded for the harness to fail on (newInvoicesHarness).
	invoices.SetContractCallHook(lockedContractCalls.note)
	os.Exit(contracttest.RequireCoverage(m, recorder))
}
