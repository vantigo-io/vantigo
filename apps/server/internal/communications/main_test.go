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
// later task removes the operations of the area it implements, and
// RequireCoverage fails the run if an entry left here was in fact exercised.
// Task 9 (stats) removed getCommunicationsStatsAttention,
// getCommunicationsStatsSummary and getCommunicationsStatsTimeseries,
// leaving only task 10's two AI operations.
var pendingOperations = []string{
	"postCommunicationsConversationsByIdAiCustomerSuggestion",
	"postCommunicationsConversationsByIdAiDraft",
}

func TestMain(m *testing.M) {
	os.Exit(contracttest.RequireCoverage(m, recorder, pendingOperations...))
}
