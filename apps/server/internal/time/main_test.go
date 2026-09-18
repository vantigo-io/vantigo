package timetracking_test

import (
	"os"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// recorder validates every exchange of every time test against time.yaml and
// records which operations answered successfully. It is shared by all tests,
// parallel ones included; TestMain turns it into the coverage gate.
var recorder = contracttest.NewForModule("time")

func TestMain(m *testing.M) {
	os.Exit(contracttest.RequireCoverage(m, recorder))
}
