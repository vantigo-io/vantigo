package calendar_test

import (
	"os"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/calendar"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// recorder validates every exchange of every calendar test against
// calendar.yaml and records which operations answered successfully. It is
// shared by all tests, parallel ones included; TestMain turns it into the
// coverage gate, which is what proves every operation is exercised.
var recorder = contracttest.NewForModule("calendar")

func TestMain(m *testing.M) {
	// The whole suite's lock rule, installed once before any test runs: every
	// call this module makes out of its own database is reported here, and
	// one made from inside one of its transactions is recorded for the
	// harness to fail on (newHarness). Installing it per harness would race
	// with the parallel tests already making requests.
	calendar.SetContractCallHook(lockedContractCalls.note)
	os.Exit(contracttest.RequireCoverage(m, recorder))
}
