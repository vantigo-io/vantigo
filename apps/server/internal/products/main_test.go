package products_test

import (
	"os"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// recorder validates every exchange of every products test against
// products.yaml and records which operations answered successfully. It is
// shared by all tests, parallel ones included; TestMain turns it into the
// coverage gate.
var recorder = contracttest.NewForModule("products")

func TestMain(m *testing.M) {
	os.Exit(contracttest.RequireCoverage(m, recorder))
}
