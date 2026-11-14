package accounting_test

import (
	"os"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// recorder validates every exchange of every accounting test against
// accounting.yaml and records which operations answered successfully;
// TestMain turns it into the coverage gate, which is what proves every
// operation is exercised.
var recorder = contracttest.NewForModule("accounting")

// pending is the operations not built yet, which the gate lets go uncovered:
// none.
var pending []string

func TestMain(m *testing.M) {
	os.Exit(contracttest.RequireCoverage(m, recorder, pending...))
}
