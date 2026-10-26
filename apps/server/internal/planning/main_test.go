package planning_test

import (
	"os"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
	"github.com/vantigo-io/vantigo/server/internal/planning"
)

// recorder validates every exchange of every planning test against
// planning.yaml and records which operations answered successfully. It is
// shared by all tests, parallel ones included; TestMain turns it into the
// coverage gate. Planning has no frozen corpus file: this gate is what proves
// every operation is exercised.
var recorder = contracttest.NewForModule("planning")

// pending is the operations not built yet, which the gate lets go
// uncovered; the gate fails an id that is covered and still listed.
var pending = []string{}

func TestMain(m *testing.M) {
	// The whole suite's lock rule, installed once before any test runs: every
	// call this module makes out of its own database — Staff's register, the
	// project and customer directories, the users, the mail — is reported
	// here and, when it is made from inside one of the module's transactions,
	// recorded for the harness to fail on (newHarness). Installing it per
	// harness would race with the parallel tests already making requests.
	planning.SetContractCallHook(lockedContractCalls.note)
	os.Exit(contracttest.RequireCoverage(m, recorder, pending...))
}
