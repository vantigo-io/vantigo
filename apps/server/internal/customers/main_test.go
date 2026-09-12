package customers_test

import (
	"context"
	"os"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/vantigo-io/vantigo/server/internal/openapi"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// recorder validates every exchange of every customers test against
// customers.yaml and records which operations answered successfully. It is
// shared by all tests, parallel ones included; TestMain turns it into the
// coverage gate.
var recorder = contracttest.New(loadContract())

func loadContract() *openapi3.T {
	doc, err := openapi.Load(context.Background(), "customers")
	if err != nil {
		panic(err)
	}
	return doc
}

// pendingOperations are the contract's operations no test covers yet. Task 6
// implemented customer CRUD and dashboard stats (nine operations), Task 7
// contacts and customer-contact associations (ten more), Task 8 legal
// identity and the Brreg lookup (four more), and Task 9 the customer
// timeline (six more, the module's last) — every one of the module's 29
// operations is now covered, so this list is empty. RequireCoverage still
// fails the run if it is ever exercised as non-empty by accident.
var pendingOperations = []string{}

func TestMain(m *testing.M) {
	os.Exit(contracttest.RequireCoverage(m, recorder, pendingOperations...))
}
