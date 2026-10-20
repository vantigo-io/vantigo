package staff_test

import (
	"os"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
	"github.com/vantigo-io/vantigo/server/internal/staff"
)

// recorder validates every exchange of every staff test against staff.yaml
// and records which operations answered successfully. It is shared by all
// tests, parallel ones included; TestMain turns it into the coverage gate.
// Staff has no frozen corpus file (openapi/testdata/exchanges holds none for
// it): this gate is what proves every operation is exercised.
var recorder = contracttest.NewForModule("staff")

// pendingOperations are the contract's operations no test covers yet: the
// group operations, stubbed by Entra delivery D's task 2 (groups_stub.go)
// until task 3a implements and tests them and empties this list.
// RequireCoverage fails the run if an operation is never answered with a
// success, or if an entry listed here was in fact exercised.
var pendingOperations = []string{
	"getStaffGroups",
	"postStaffGroups",
	"deleteStaffGroupsById",
	"getStaffGroupsById",
	"putStaffGroupsById",
	"postStaffGroupsByIdArchive",
	"putStaffGroupsByIdMembers",
	"putStaffGroupsByIdOwners",
	"getStaffGroupsByIdTraits",
	"putStaffGroupsByIdTraitsByProvider",
	"getStaffMeGroups",
}

func TestMain(m *testing.M) {
	// The whole suite's lock rule, installed once before any test runs: every
	// call this module makes out of its own database — the user directory, the
	// mail — is reported here, recorded with its caller (contractCalls), and,
	// when it is made from inside one of the module's transactions, recorded
	// for the harness to fail on (newHarness). Installing it per harness would
	// race with the parallel tests already making requests.
	staff.SetContractCallHook(noteContractCall)
	os.Exit(contracttest.RequireCoverage(m, recorder, pendingOperations...))
}
