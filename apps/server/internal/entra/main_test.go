package entra_test

import (
	"os"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// recorder validates every exchange of every entra test against entra.yaml
// and records which operations answered successfully. It is shared by all
// tests, parallel ones included; TestMain turns it into the coverage gate,
// which is what proves every operation is exercised.
var recorder = contracttest.NewForModule("entra")

// pendingOperations are the contract's operations no test covers yet: the
// whole contract, whose handlers answer 501 until task 3 builds them
// (stubs_test.go proves each is mounted and guarded meanwhile). Task 3
// removes each as its test covers it, and RequireCoverage fails the run if
// an entry left here was in fact exercised.
var pendingOperations = []string{
	"getEntraConnection",
	"putEntraConnection",
	"postEntraConnectionCredentials",
	"deleteEntraConnectionCredentialsById",
	"postEntraConnectionTest",
	"putEntraConnectionSettings",
	"getEntraConnectionSkus",
	"getEntraEvents",
	"getEntraAccounts",
}

func TestMain(m *testing.M) {
	os.Exit(contracttest.RequireCoverage(m, recorder, pendingOperations...))
}
