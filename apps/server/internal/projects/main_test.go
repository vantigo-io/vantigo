package projects_test

import (
	"os"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
	"github.com/vantigo-io/vantigo/server/internal/projects"
)

// recorder validates every exchange of every projects test against
// projects.yaml and records which operations answered successfully. It is
// shared by all tests, parallel ones included; TestMain turns it into the
// coverage gate.
var recorder = contracttest.NewForModule("projects")

func TestMain(m *testing.M) {
	// The whole suite's locking guarantee, installed once before any test
	// runs: every call this module makes into another module's contract is
	// reported here, and one made from inside a transaction holding the
	// project's row lock is recorded for the harness to fail on
	// (newProjectsHarness). Installing it per harness would race with the
	// parallel tests already making requests.
	projects.SetContractCallHook(lockedContractCalls.note)
	os.Exit(contracttest.RequireCoverage(m, recorder))
}
