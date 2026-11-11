package manufacturing_test

import (
	"os"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/manufacturing"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// recorder validates every exchange of every manufacturing test against
// manufacturing.yaml and records which operations answered successfully;
// TestMain turns it into the coverage gate, which is what proves every
// operation is exercised.
var recorder = contracttest.NewForModule("manufacturing")

// pending is the operations not built yet, which the gate lets go uncovered:
// none.
var pending []string

func TestMain(m *testing.M) {
	// The whole suite's lock rule, installed once before any test runs: every
	// call this module makes out of its own database is reported here and,
	// when made from inside one of the module's transactions, recorded for
	// the harness to fail on — all but LockVariants, which runs on the
	// transaction by design (M10a).
	manufacturing.SetContractCallHook(lockedContractCalls.note)
	os.Exit(contracttest.RequireCoverage(m, recorder, pending...))
}
