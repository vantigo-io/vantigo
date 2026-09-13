package communications_test

import (
	"context"
	"os"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/vantigo-io/vantigo/server/internal/openapi"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// recorder validates every exchange of every communications test against
// communications.yaml and records which operations answered successfully.
// It is shared by all tests, parallel ones included; TestMain turns it into
// the coverage gate.
var recorder = contracttest.New(loadContract())

func loadContract() *openapi3.T {
	doc, err := openapi.Load(context.Background(), "communications")
	if err != nil {
		panic(err)
	}
	return doc
}

// pendingOperations are the contract's operations no test covers yet. Every
// one of the module's 28 operations started as a stub answering 501; each
// later task removed the operations of the area it implemented, and
// RequireCoverage fails the run if an entry left here was in fact exercised.
//
// It is now empty, and stays empty: task 10 implemented the AI draft and
// customer suggestion, the last two stubs, so every operation of
// communications.yaml is exercised by a successful, contract-conforming
// exchange in this package. A new operation added to the contract must be
// implemented and covered rather than parked here.
var pendingOperations = []string{}

func TestMain(m *testing.M) {
	os.Exit(contracttest.RequireCoverage(m, recorder, pendingOperations...))
}
