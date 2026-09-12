package products_test

import (
	"context"
	"os"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/vantigo-io/vantigo/server/internal/openapi"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// recorder validates every exchange of every products test against
// products.yaml and records which operations answered successfully. It is
// shared by all tests, parallel ones included; TestMain turns it into the
// coverage gate.
var recorder = contracttest.New(loadContract())

func loadContract() *openapi3.T {
	doc, err := openapi.Load(context.Background(), "products")
	if err != nil {
		panic(err)
	}
	return doc
}

// pendingOperations are the contract's operations no test covers yet. Task
// 11 implemented and removed the thirteen products/variants/pricing
// operations; Task 12 implemented and removed the remaining thirteen
// (categories, tax categories, stats) — every products.yaml operation now
// has a test, so this is empty.
var pendingOperations = []string{}

func TestMain(m *testing.M) {
	os.Exit(contracttest.RequireCoverage(m, recorder, pendingOperations...))
}
