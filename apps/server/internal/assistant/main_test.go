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

// pending is the operations not built yet.
var pending = []string{}

func TestMain(m *testing.M) {
	os.Exit(contracttest.RequireCoverage(m, recorder, pending...))
}
