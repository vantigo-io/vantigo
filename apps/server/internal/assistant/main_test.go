package assistant_test

import (
	"os"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// recorder validates every exchange of every assistant test against
// assistant.yaml and records which operations answered successfully; TestMain
// turns it into the coverage gate.
var recorder = contracttest.NewForModule("assistant")

// pending is the operations this package's tests cannot answer successfully.
// confirmAssistantPendingAction runs a change of another module, which a test of
// this package may not compose (module boundaries rule 18; depguard covers
// tests): its refusals are exercised here, and internal/integration runs the
// confirmed change through the real modules.
var pending = []string{"confirmAssistantPendingAction"}

func TestMain(m *testing.M) {
	os.Exit(contracttest.RequireCoverage(m, recorder, pending...))
}
