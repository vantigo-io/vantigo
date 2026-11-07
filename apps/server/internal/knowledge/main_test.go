package knowledge_test

import (
	"os"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// recorder validates every exchange of every knowledge test against
// knowledge.yaml and records which operations answered successfully;
// TestMain turns it into the coverage gate. No operation is pending: every
// one is exercised by a successful, contract-conforming exchange here.
var recorder = contracttest.NewForModule("knowledge")

func TestMain(m *testing.M) {
	os.Exit(contracttest.RequireCoverage(m, recorder))
}
