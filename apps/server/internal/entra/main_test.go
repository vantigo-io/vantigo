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
// directory's two reads, which answer 503 until they are built (directory.go).
// RequireCoverage fails the run if an operation is never answered with a
// success, or if an entry listed here was in fact exercised.
var pendingOperations = []string{"getEntraDirectoryUsers", "getEntraDirectoryMatches"}

func TestMain(m *testing.M) {
	os.Exit(contracttest.RequireCoverage(m, recorder, pendingOperations...))
}
