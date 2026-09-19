package expenses_test

import (
	"os"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/expenses"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// recorder validates every exchange of every expenses test against
// expenses.yaml and records which operations answered successfully. It is
// shared by all tests, parallel ones included; TestMain turns it into the
// coverage gate.
var recorder = contracttest.NewForModule("expenses")

func TestMain(m *testing.M) {
	// The whole suite's locking guarantee, installed once before any test
	// runs: every call this module makes into another module's contract is
	// reported here, and one made from inside a locked transaction is
	// recorded for the harness to fail on (newExpensesHarness). Installing it
	// per harness would race with the parallel tests already making requests.
	expenses.SetContractCallHook(lockedContractCalls.note)
	os.Exit(contracttest.RequireCoverage(m, recorder))
}
