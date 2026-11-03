package inventory_test

import (
	"os"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/inventory"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// recorder validates every exchange of every inventory test against
// inventory.yaml and records which operations answered successfully. It is
// shared by all tests, parallel ones included; TestMain turns it into the
// coverage gate, which is what proves every operation is exercised.
var recorder = contracttest.NewForModule("inventory")

// pending is the operations not built yet, which the gate lets go
// uncovered; the gate fails an id that is covered and still listed. Delivery
// B builds every operation of its contract.
var pending []string

func TestMain(m *testing.M) {
	// The whole suite's lock rule, installed once before any test runs: every
	// call this module makes out of its own database is reported here and,
	// when it is made from inside one of the module's transactions, recorded
	// for the harness to fail on (newHarness) — all but LockVariants, which
	// runs on the transaction by design (I22).
	inventory.SetContractCallHook(lockedContractCalls.note)
	os.Exit(contracttest.RequireCoverage(m, recorder, pending...))
}
