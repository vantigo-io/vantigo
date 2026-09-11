package identity_test

import (
	"os"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// pendingOperations are the identity operations still stubbed in
// unimplemented.go (TestPendingOperationsAreExactlyTheStubs keeps the two
// lists equal). The coverage gate excuses them; each area removes the
// operations it implements, and an entry a successful exchange exercised
// fails the run. The list was generated from the contract's operationIds;
// it goes away once every operation is implemented. Every operation now is,
// so it is empty; Task 20 deletes it with unimplemented.go.
var pendingOperations = []string{}

func TestMain(m *testing.M) {
	os.Exit(contracttest.RequireCoverage(m, recorder, pendingOperations...))
}
