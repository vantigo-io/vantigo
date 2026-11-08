package agents_test

import (
	"os"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// recorder validates every exchange of every agents test against agents.yaml
// and records which operations answered successfully; TestMain turns it into
// the coverage gate.
var recorder = contracttest.NewForModule("agents")

func TestMain(m *testing.M) {
	os.Exit(contracttest.RequireCoverage(m, recorder))
}
