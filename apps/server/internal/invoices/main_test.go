package invoices_test

import (
	"os"
	"testing"

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
	os.Exit(contracttest.RequireCoverage(m, recorder))
}
