package identity_test

import (
	"os"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// pendingOperations are the contract's operations no test covers yet: the
// Owner's single sign-on endpoints (Entra design D2), whose contract lands
// before their handlers. The task that implements them removes them, and
// RequireCoverage fails the run if an entry left here was in fact exercised.
var pendingOperations = []string{
	"getIdentityOwnerSso",
	"putIdentityOwnerSsoOidc",
	"deleteIdentityOwnerSsoOidc",
	"postIdentityOwnerSsoScimToken",
	"deleteIdentityOwnerSsoScimPrevious",
	"deleteIdentityOwnerSsoScim",
}

func TestMain(m *testing.M) {
	os.Exit(contracttest.RequireCoverage(m, recorder, pendingOperations...))
}
